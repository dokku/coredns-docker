//go:build integration

package docker

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coredns/caddy"
	_ "github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/plugin/test"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

const testContainerPrefix = "coredns-docker-test-"
const testNetworkPrefix = "coredns-docker-test-net-"

func TestMain(m *testing.M) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Skipping integration tests: cannot create Docker client: %v\n", err)
		os.Exit(0)
	}
	defer cli.Close()

	ctx := context.Background()
	_, err = cli.Ping(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Skipping integration tests: Docker daemon not reachable: %v\n", err)
		os.Exit(0)
	}

	// Clean up stale containers/networks from previous crashed runs
	containers, _ := cli.ContainerList(ctx, container.ListOptions{All: true})
	for _, c := range containers {
		for _, name := range c.Names {
			if strings.HasPrefix(strings.TrimPrefix(name, "/"), testContainerPrefix) {
				_ = cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
				break
			}
		}
	}
	networks, _ := cli.NetworkList(ctx, network.ListOptions{})
	for _, n := range networks {
		if strings.HasPrefix(n.Name, testNetworkPrefix) {
			_ = cli.NetworkRemove(ctx, n.ID)
		}
	}

	// Pull alpine:latest for test containers
	reader, err := cli.ImagePull(ctx, "alpine:latest", image.PullOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to pull alpine:latest: %v\n", err)
		os.Exit(1)
	}
	_, _ = io.Copy(io.Discard, reader)
	reader.Close()

	os.Exit(m.Run())
}

func setupIntegrationDocker(t *testing.T, networks []string) (*Docker, *client.Client) {
	t.Helper()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("Failed to create Docker client: %v", err)
	}
	t.Cleanup(func() { cli.Close() })

	d := &Docker{
		Next:        test.ErrorHandler(),
		ttl:         DefaultTTL,
		zones:       []string{"docker."},
		labelPrefix: "com.dokku.coredns-docker",
		client:      cli,
		networks:    networks,
		records:     make(map[string][]net.IP),
		srvs:        make(map[string][]srvRecord),
		ptrs:        make(map[string][]string),
		cnames:      make(map[string]string),
		txts:        make(map[string][][]string),
	}

	return d, cli
}

func createTestContainer(t *testing.T, cli *client.Client, name string, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig) string {
	t.Helper()

	ctx := context.Background()
	resp, err := cli.ContainerCreate(ctx, config, hostConfig, networkingConfig, nil, name)
	if err != nil {
		t.Fatalf("Failed to create container %s: %v", name, err)
	}

	t.Cleanup(func() {
		_ = cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
	})

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		t.Fatalf("Failed to start container %s: %v", name, err)
	}

	return resp.ID
}

func testContainerName(t *testing.T, suffix string) string {
	t.Helper()
	name := testContainerPrefix + strings.ReplaceAll(t.Name(), "/", "-")
	if suffix != "" {
		name += "-" + suffix
	}
	return strings.ToLower(name)
}

func queryDNS(t *testing.T, d *Docker, qname string, qtype uint16) (*dns.Msg, int, error) {
	t.Helper()

	m := new(dns.Msg)
	m.SetQuestion(qname, qtype)

	w := dnstest.NewRecorder(&test.ResponseWriter{})
	rcode, err := d.ServeDNS(context.Background(), w, m)
	return w.Msg, rcode, err
}

func TestIntegrationBasicARecord(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
	}, nil, nil)

	d.syncRecords(ctx)

	fqdn := name + ".docker."
	resp, _, err := queryDNS(t, d, fqdn, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", fqdn, err)
	}
	if resp == nil {
		t.Fatalf("expected DNS response for %s, got nil", fqdn)
	}
	if len(resp.Answer) == 0 {
		t.Fatalf("expected at least one A record for %s, got none", fqdn)
	}

	a, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}

	ip := a.A
	if ip == nil || ip.To4() == nil {
		t.Fatalf("expected valid IPv4 address, got %v", ip)
	}

	privateRanges := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	isPrivate := false
	for _, cidr := range privateRanges {
		_, ipNet, _ := net.ParseCIDR(cidr)
		if ipNet.Contains(ip) {
			isPrivate = true
			break
		}
	}
	if !isPrivate {
		t.Errorf("expected private IP, got %s", ip)
	}
}

func TestIntegrationSRVRecord(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			"com.dokku.coredns-docker/srv._tcp._http": "80",
		},
	}, nil, nil)

	d.syncRecords(ctx)

	srvQname := "_http._tcp." + name + ".docker."
	resp, _, err := queryDNS(t, d, srvQname, dns.TypeSRV)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", srvQname, err)
	}
	if resp == nil {
		t.Fatalf("expected DNS response for %s, got nil", srvQname)
	}
	if len(resp.Answer) == 0 {
		t.Fatalf("expected at least one SRV record for %s, got none", srvQname)
	}

	srv, ok := resp.Answer[0].(*dns.SRV)
	if !ok {
		t.Fatalf("expected SRV record, got %T", resp.Answer[0])
	}
	if srv.Port != 80 {
		t.Errorf("expected port 80, got %d", srv.Port)
	}
	expectedTarget := name + ".docker."
	if srv.Target != expectedTarget {
		t.Errorf("expected target %s, got %s", expectedTarget, srv.Target)
	}
}

func TestIntegrationContainerRemoval(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	containerID := createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
	}, nil, nil)

	// First sync: record should exist
	d.syncRecords(ctx)

	fqdn := name + ".docker."
	resp, _, err := queryDNS(t, d, fqdn, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", fqdn, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for %s after first sync", fqdn)
	}

	// Remove container
	if err := cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		t.Fatalf("Failed to remove container: %v", err)
	}

	// Second sync: record should be gone
	d.syncRecords(ctx)

	resp, _, _ = queryDNS(t, d, fqdn, dns.TypeA)
	if resp != nil && len(resp.Answer) > 0 {
		t.Errorf("expected no A records for %s after removal, got %d", fqdn, len(resp.Answer))
	}
}

func TestIntegrationNetworkAlias(t *testing.T) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("Failed to create Docker client: %v", err)
	}
	t.Cleanup(func() { cli.Close() })

	ctx := context.Background()

	// Create a custom network
	networkName := testNetworkPrefix + "alias"
	netResp, err := cli.NetworkCreate(ctx, networkName, network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		t.Fatalf("Failed to create network: %v", err)
	}
	t.Cleanup(func() {
		_ = cli.NetworkRemove(context.Background(), netResp.ID)
	})

	d := &Docker{
		Next:        test.ErrorHandler(),
		ttl:         DefaultTTL,
		zones:       []string{"docker."},
		labelPrefix: "com.dokku.coredns-docker",
		client:      cli,
		networks:    []string{networkName},
		records:     make(map[string][]net.IP),
		srvs:        make(map[string][]srvRecord),
	}

	alias := "myservicealias"
	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
	}, &container.HostConfig{
		NetworkMode: container.NetworkMode(networkName),
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: {
				Aliases: []string{alias},
			},
		},
	})

	d.syncRecords(ctx)

	// Verify alias resolves
	aliasFqdn := alias + ".docker."
	resp, _, err := queryDNS(t, d, aliasFqdn, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", aliasFqdn, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for alias %s, got none", aliasFqdn)
	}

	a, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}
	if a.A == nil || a.A.To4() == nil {
		t.Fatalf("expected valid IPv4 for alias, got %v", a.A)
	}

	// Also verify the container name resolves
	containerFqdn := name + ".docker."
	resp, _, err = queryDNS(t, d, containerFqdn, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", containerFqdn, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for container name %s, got none", containerFqdn)
	}
}

func TestIntegrationFallthrough(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	d.Fall = fall.Root
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
	}, nil, nil)

	d.syncRecords(ctx)

	// Existing container should still resolve
	fqdn := name + ".docker."
	resp, _, err := queryDNS(t, d, fqdn, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", fqdn, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for %s, got none", fqdn)
	}

	// Nonexistent name should fall through to ErrorHandler (SERVFAIL)
	resp, rcode, _ := queryDNS(t, d, "nonexistent.docker.", dns.TypeA)
	if rcode != dns.RcodeServerFailure {
		t.Errorf("expected SERVFAIL for nonexistent with fallthrough, got rcode %d (resp: %v)", rcode, resp)
	}
}

func TestIntegrationNoFallthrough(t *testing.T) {
	d, _ := setupIntegrationDocker(t, nil)

	// No fallthrough configured (default)
	resp, _, err := queryDNS(t, d, "nonexistent.docker.", dns.TypeA)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Rcode != dns.RcodeNameError {
		t.Errorf("expected NXDOMAIN for nonexistent without fallthrough, got rcode %d", resp.Rcode)
	}
	if len(resp.Ns) != 1 {
		t.Fatalf("expected 1 SOA in authority section, got %d", len(resp.Ns))
	}
	soa, ok := resp.Ns[0].(*dns.SOA)
	if !ok {
		t.Fatalf("expected SOA record in authority, got %T", resp.Ns[0])
	}
	if soa.Hdr.Name != "docker." {
		t.Errorf("SOA name should be docker., got %s", soa.Hdr.Name)
	}
}

func TestIntegrationSOAQuery(t *testing.T) {
	d, _ := setupIntegrationDocker(t, nil)
	d.connected = true

	resp, _, err := queryDNS(t, d, "docker.", dns.TypeSOA)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Rcode != dns.RcodeSuccess {
		t.Errorf("expected NOERROR, got rcode %d", resp.Rcode)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("expected 1 SOA answer, got %d", len(resp.Answer))
	}
	soa, ok := resp.Answer[0].(*dns.SOA)
	if !ok {
		t.Fatalf("expected SOA record, got %T", resp.Answer[0])
	}
	if soa.Hdr.Name != "docker." {
		t.Errorf("SOA name should be docker., got %s", soa.Hdr.Name)
	}
	if soa.Ns != "ns.dns.docker." {
		t.Errorf("SOA MNAME should be ns.dns.docker., got %s", soa.Ns)
	}
	if soa.Mbox != "hostmaster.docker." {
		t.Errorf("SOA RNAME should be hostmaster.docker., got %s", soa.Mbox)
	}
}

func TestIntegrationNSQuery(t *testing.T) {
	d, _ := setupIntegrationDocker(t, nil)
	d.connected = true

	resp, _, err := queryDNS(t, d, "docker.", dns.TypeNS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Rcode != dns.RcodeSuccess {
		t.Errorf("expected NOERROR, got rcode %d", resp.Rcode)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("expected 1 NS answer, got %d", len(resp.Answer))
	}
	ns, ok := resp.Answer[0].(*dns.NS)
	if !ok {
		t.Fatalf("expected NS record, got %T", resp.Answer[0])
	}
	if ns.Hdr.Name != "docker." {
		t.Errorf("NS name should be docker., got %s", ns.Hdr.Name)
	}
	if ns.Ns != "ns.dns.docker." {
		t.Errorf("NS target should be ns.dns.docker., got %s", ns.Ns)
	}
}

func TestIntegrationMultiZone(t *testing.T) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("Failed to create Docker client: %v", err)
	}
	t.Cleanup(func() { cli.Close() })

	d := &Docker{
		Next:        test.ErrorHandler(),
		ttl:         DefaultTTL,
		zones:       []string{"docker.", "internal."},
		labelPrefix: "com.dokku.coredns-docker",
		client:      cli,
		records:     make(map[string][]net.IP),
		srvs:        make(map[string][]srvRecord),
	}

	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
	}, nil, nil)

	d.syncRecords(ctx)

	// Verify container resolves under first zone
	fqdn1 := name + ".docker."
	resp, _, err := queryDNS(t, d, fqdn1, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", fqdn1, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for %s, got none", fqdn1)
	}

	// Verify container resolves under second zone
	fqdn2 := name + ".internal."
	resp, _, err = queryDNS(t, d, fqdn2, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", fqdn2, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for %s, got none", fqdn2)
	}

	// Both should resolve to the same IP
	a1 := resp.Answer[0].(*dns.A)
	resp1, _, _ := queryDNS(t, d, fqdn1, dns.TypeA)
	a2 := resp1.Answer[0].(*dns.A)
	if !a1.A.Equal(a2.A) {
		t.Errorf("expected same IP for both zones, got %s and %s", a1.A, a2.A)
	}
}

func TestIntegrationReadyConnected(t *testing.T) {
	d, _ := setupIntegrationDocker(t, nil)

	d.mu.Lock()
	d.connected = true
	d.mu.Unlock()

	d.syncRecords(context.Background())

	if !d.Ready() {
		t.Error("expected Ready() to return true when connected with synced records")
	}
}

func TestIntegrationReadyDisconnectedWithRecords(t *testing.T) {
	d, _ := setupIntegrationDocker(t, nil)

	d.syncRecords(context.Background())

	d.mu.Lock()
	d.connected = false
	d.mu.Unlock()

	if !d.Ready() {
		t.Error("expected Ready() to return true when disconnected but has synced records")
	}
}

func TestIntegrationReadyDisconnectedNoSync(t *testing.T) {
	d, _ := setupIntegrationDocker(t, nil)

	d.mu.Lock()
	d.connected = false
	d.mu.Unlock()

	if d.Ready() {
		t.Error("expected Ready() to return false when disconnected and never synced")
	}
}

func TestIntegrationReadyNoClient(t *testing.T) {
	d := &Docker{
		records: make(map[string][]net.IP),
		srvs:    make(map[string][]srvRecord),
	}

	if d.Ready() {
		t.Error("expected Ready() to return false when client is nil")
	}
}

func TestIntegrationSyncMetrics(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			"com.dokku.coredns-docker/srv._tcp._http": "80",
		},
	}, nil, nil)

	getSyncSampleCount := func() uint64 {
		m := &dto.Metric{}
		_ = syncDuration.(prometheus.Metric).Write(m)
		return m.GetHistogram().GetSampleCount()
	}

	syncCountBefore := getSyncSampleCount()

	d.syncRecords(ctx)

	// Verify record gauges are set
	records := testutil.ToFloat64(recordsCount)
	if records < 1 {
		t.Errorf("expected records_total >= 1, got %f", records)
	}

	srvRecords := testutil.ToFloat64(srvRecordsCount)
	if srvRecords < 1 {
		t.Errorf("expected srv_records_total >= 1, got %f", srvRecords)
	}

	ptrRecords := testutil.ToFloat64(ptrRecordsCount)
	if ptrRecords < 1 {
		t.Errorf("expected ptr_records_total >= 1, got %f", ptrRecords)
	}

	containers := testutil.ToFloat64(containersCount)
	if containers < 1 {
		t.Errorf("expected containers_total >= 1, got %f", containers)
	}

	// Verify sync duration was observed
	syncCountAfter := getSyncSampleCount()
	if syncCountAfter != syncCountBefore+1 {
		t.Errorf("expected sync_duration_seconds sample count to increase by 1, before=%d after=%d", syncCountBefore, syncCountAfter)
	}
}

func TestIntegrationHostnameLabel(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			"com.dokku.coredns-docker/hostname": "myapp,otherapp",
		},
	}, nil, nil)

	d.syncRecords(ctx)

	// Verify container name resolves
	fqdn := name + ".docker."
	resp, _, err := queryDNS(t, d, fqdn, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", fqdn, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for %s, got none", fqdn)
	}

	containerA, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}

	// Verify first hostname label resolves
	hostnameFqdn1 := "myapp.docker."
	resp, _, err = queryDNS(t, d, hostnameFqdn1, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", hostnameFqdn1, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for hostname %s, got none", hostnameFqdn1)
	}

	a1, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}
	if !a1.A.Equal(containerA.A) {
		t.Errorf("expected hostname %s to resolve to %s, got %s", hostnameFqdn1, containerA.A, a1.A)
	}

	// Verify second hostname label resolves
	hostnameFqdn2 := "otherapp.docker."
	resp, _, err = queryDNS(t, d, hostnameFqdn2, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", hostnameFqdn2, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for hostname %s, got none", hostnameFqdn2)
	}

	a2, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}
	if !a2.A.Equal(containerA.A) {
		t.Errorf("expected hostname %s to resolve to %s, got %s", hostnameFqdn2, containerA.A, a2.A)
	}
}

func TestIntegrationCNAMELabel(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			"com.dokku.coredns-docker/cname": "external.example.com",
		},
	}, nil, nil)

	d.syncRecords(ctx)

	fqdn := name + ".docker."

	// CNAME'd container must not appear in the A/AAAA map.
	d.mu.RLock()
	if _, ok := d.records[fqdn]; ok {
		d.mu.RUnlock()
		t.Fatalf("expected no A records for CNAME'd container %s, but found some", fqdn)
	}
	// The cname map must contain the FQDN with the target normalized to a
	// trailing dot.
	target, ok := d.cnames[fqdn]
	d.mu.RUnlock()
	if !ok {
		t.Fatalf("expected CNAME entry for %s, not found", fqdn)
	}
	if target != "external.example.com." {
		t.Errorf("expected CNAME target external.example.com., got %s", target)
	}

	// CNAME type query returns the CNAME answer.
	resp, _, err := queryDNS(t, d, fqdn, dns.TypeCNAME)
	if err != nil {
		t.Fatalf("ServeDNS error for %s CNAME: %v", fqdn, err)
	}
	if resp == nil || len(resp.Answer) != 1 {
		t.Fatalf("expected 1 CNAME answer for %s, got %+v", fqdn, resp)
	}
	cname, ok := resp.Answer[0].(*dns.CNAME)
	if !ok {
		t.Fatalf("expected *dns.CNAME, got %T", resp.Answer[0])
	}
	if cname.Target != "external.example.com." {
		t.Errorf("expected CNAME target external.example.com., got %s", cname.Target)
	}

	// A query on the CNAME name also returns a CNAME (resolver chases it).
	resp, _, err = queryDNS(t, d, fqdn, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s A: %v", fqdn, err)
	}
	if resp == nil || len(resp.Answer) != 1 {
		t.Fatalf("expected 1 CNAME answer for %s A query, got %+v", fqdn, resp)
	}
	if _, ok := resp.Answer[0].(*dns.CNAME); !ok {
		t.Errorf("expected CNAME answer for A query on CNAME name, got %T", resp.Answer[0])
	}
}

func TestIntegrationLastSyncTimeSet(t *testing.T) {
	d, _ := setupIntegrationDocker(t, nil)

	if !d.lastSyncTime.IsZero() {
		t.Error("expected lastSyncTime to be zero before sync")
	}

	d.syncRecords(context.Background())

	d.mu.RLock()
	syncTime := d.lastSyncTime
	d.mu.RUnlock()

	if syncTime.IsZero() {
		t.Error("expected lastSyncTime to be set after sync")
	}
}

func TestIntegrationWildcard(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			"com.dokku.coredns-docker/wildcard": "true",
		},
	}, nil, nil)

	d.syncRecords(ctx)

	// Wildcard subdomain should resolve
	wildcardFqdn := "anything." + name + ".docker."
	resp, _, err := queryDNS(t, d, wildcardFqdn, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", wildcardFqdn, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for wildcard %s, got none", wildcardFqdn)
	}

	wildcardA, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}

	// Exact match should also resolve
	exactFqdn := name + ".docker."
	resp, _, err = queryDNS(t, d, exactFqdn, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", exactFqdn, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for exact %s, got none", exactFqdn)
	}

	exactA, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}

	// Both should resolve to the same IP
	if !wildcardA.A.Equal(exactA.A) {
		t.Errorf("expected wildcard and exact to resolve to same IP, got %s and %s", wildcardA.A, exactA.A)
	}

	// Deep subdomain should NOT match (RFC 4592)
	deepFqdn := "deep.sub." + name + ".docker."
	resp, _, _ = queryDNS(t, d, deepFqdn, dns.TypeA)
	if resp != nil && resp.Rcode != dns.RcodeNameError {
		t.Errorf("expected NXDOMAIN for deep subdomain %s, got rcode %d", deepFqdn, resp.Rcode)
	}
}

func TestIntegrationWildcardExactPrecedence(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	// Create a wildcard container
	wildcardName := testContainerName(t, "wildcard")
	createTestContainer(t, cli, wildcardName, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			"com.dokku.coredns-docker/wildcard": "true",
		},
	}, nil, nil)

	// Create a container whose name would match the wildcard pattern
	exactName := testContainerName(t, "exact")
	createTestContainer(t, cli, exactName, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			"com.dokku.coredns-docker/hostname": exactName + "." + wildcardName,
		},
	}, nil, nil)

	d.syncRecords(ctx)

	// Query the hostname that matches both exact and wildcard
	// The exact match should win
	fqdn := exactName + "." + wildcardName + ".docker."
	resp, _, err := queryDNS(t, d, fqdn, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", fqdn, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for %s, got none", fqdn)
	}

	// Verify the response uses the exact container's IP (from the hostname label)
	a, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}

	// Get the exact container's IP to verify
	exactResp, _, _ := queryDNS(t, d, exactName+".docker.", dns.TypeA)
	if exactResp == nil || len(exactResp.Answer) == 0 {
		t.Fatalf("expected A record for exact container %s, got none", exactName+".docker.")
	}
	exactA := exactResp.Answer[0].(*dns.A)
	if !a.A.Equal(exactA.A) {
		t.Errorf("expected exact match IP %s, got %s (wildcard may have taken precedence)", exactA.A, a.A)
	}
}

func TestIntegrationPTRRecord(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
	}, nil, nil)

	d.syncRecords(ctx)

	// First, get the container's IP via A record
	fqdn := name + ".docker."
	resp, _, err := queryDNS(t, d, fqdn, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", fqdn, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for %s, got none", fqdn)
	}

	a := resp.Answer[0].(*dns.A)
	ip := a.A.String()

	// Compute the reverse ARPA name
	arpa, err := dns.ReverseAddr(ip)
	if err != nil {
		t.Fatalf("Failed to compute reverse address for %s: %v", ip, err)
	}

	// Query the PTR record
	ptrResp, _, err := queryDNS(t, d, arpa, dns.TypePTR)
	if err != nil {
		t.Fatalf("ServeDNS error for PTR %s: %v", arpa, err)
	}
	if ptrResp == nil || len(ptrResp.Answer) == 0 {
		t.Fatalf("expected PTR record for %s, got none", arpa)
	}

	ptr, ok := ptrResp.Answer[0].(*dns.PTR)
	if !ok {
		t.Fatalf("expected PTR record, got %T", ptrResp.Answer[0])
	}

	if ptr.Ptr != fqdn {
		t.Errorf("expected PTR target %s, got %s", fqdn, ptr.Ptr)
	}
}

func TestIntegrationHostModeARecord(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	d.hostMode = true
	ctx := context.Background()

	name := testContainerName(t, "")
	containerID := createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		ExposedPorts: nat.PortSet{
			nat.Port("5432/tcp"): struct{}{},
		},
	}, &container.HostConfig{
		PortBindings: nat.PortMap{
			nat.Port("5432/tcp"): []nat.PortBinding{
				{HostIP: "127.0.0.1", HostPort: "0"},
			},
		},
	}, nil)

	// Inspect to discover the host port Docker assigned.
	inspect, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		t.Fatalf("Failed to inspect container: %v", err)
	}
	bindings := inspect.NetworkSettings.Ports[nat.Port("5432/tcp")]
	if len(bindings) == 0 {
		t.Fatalf("expected at least one binding for 5432/tcp, got none")
	}
	hostPortStr := bindings[0].HostPort
	if hostPortStr == "" {
		t.Fatalf("expected non-empty HostPort, got empty")
	}

	d.syncRecords(ctx)

	fqdn := name + ".docker."
	resp, _, err := queryDNS(t, d, fqdn, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", fqdn, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for %s in host mode, got none", fqdn)
	}
	a, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}
	if a.A.String() != "127.0.0.1" {
		t.Errorf("expected host IP 127.0.0.1 in host mode, got %s", a.A.String())
	}

	// SRV fallback should emit a _tcp._tcp record pointing at the host port.
	srvQname := "_tcp._tcp." + fqdn
	srvResp, _, err := queryDNS(t, d, srvQname, dns.TypeSRV)
	if err != nil {
		t.Fatalf("ServeDNS error for SRV %s: %v", srvQname, err)
	}
	if srvResp == nil || len(srvResp.Answer) == 0 {
		t.Fatalf("expected SRV record for %s in host mode, got none", srvQname)
	}
	srv, ok := srvResp.Answer[0].(*dns.SRV)
	if !ok {
		t.Fatalf("expected SRV record, got %T", srvResp.Answer[0])
	}
	if strconv.Itoa(int(srv.Port)) != hostPortStr {
		t.Errorf("expected SRV host port %s, got %d", hostPortStr, srv.Port)
	}
}

func TestIntegrationHostModeSRVLabel(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	d.hostMode = true
	ctx := context.Background()

	name := testContainerName(t, "")
	containerID := createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			"com.dokku.coredns-docker/srv._tcp._http": "80",
		},
		ExposedPorts: nat.PortSet{
			nat.Port("80/tcp"): struct{}{},
		},
	}, &container.HostConfig{
		PortBindings: nat.PortMap{
			nat.Port("80/tcp"): []nat.PortBinding{
				{HostIP: "127.0.0.1", HostPort: "0"},
			},
		},
	}, nil)

	inspect, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		t.Fatalf("Failed to inspect container: %v", err)
	}
	bindings := inspect.NetworkSettings.Ports[nat.Port("80/tcp")]
	if len(bindings) == 0 {
		t.Fatalf("expected at least one binding for 80/tcp, got none")
	}
	hostPortStr := bindings[0].HostPort

	d.syncRecords(ctx)

	srvQname := "_http._tcp." + name + ".docker."
	resp, _, err := queryDNS(t, d, srvQname, dns.TypeSRV)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", srvQname, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected SRV record for %s in host mode, got none", srvQname)
	}
	srv, ok := resp.Answer[0].(*dns.SRV)
	if !ok {
		t.Fatalf("expected SRV record, got %T", resp.Answer[0])
	}
	if strconv.Itoa(int(srv.Port)) != hostPortStr {
		t.Errorf("expected SRV host port %s, got %d", hostPortStr, srv.Port)
	}
}

func TestIntegrationHostModeNoBindings(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	d.hostMode = true
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
	}, nil, nil)

	d.syncRecords(ctx)

	fqdn := name + ".docker."
	resp, _, err := queryDNS(t, d, fqdn, dns.TypeA)
	if err != nil {
		t.Fatalf("ServeDNS error for %s: %v", fqdn, err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Rcode != dns.RcodeNameError {
		t.Errorf("expected NXDOMAIN for container without bindings in host mode, got rcode %d", resp.Rcode)
	}
}

func TestIntegrationHostModePTRDefault(t *testing.T) {
	// By default host_mode does not emit PTR records.
	d, cli := setupIntegrationDocker(t, nil)
	d.hostMode = true
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		ExposedPorts: nat.PortSet{
			nat.Port("80/tcp"): struct{}{},
		},
	}, &container.HostConfig{
		PortBindings: nat.PortMap{
			nat.Port("80/tcp"): []nat.PortBinding{
				{HostIP: "127.0.0.1", HostPort: "0"},
			},
		},
	}, nil)

	d.syncRecords(ctx)

	arpa, _ := dns.ReverseAddr("127.0.0.1")
	_, rcode, _ := queryDNS(t, d, arpa, dns.TypePTR)
	if rcode != dns.RcodeServerFailure {
		// With no PTR records stored, the plugin falls through to the
		// test ErrorHandler which returns SERVFAIL.
		t.Errorf("expected PTR for %s to pass to next plugin (SERVFAIL) in default host mode, got rcode %d", arpa, rcode)
	}
}

func TestIntegrationHostModePTREnabled(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	d.hostMode = true
	d.hostModePTR = true
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		ExposedPorts: nat.PortSet{
			nat.Port("80/tcp"): struct{}{},
		},
	}, &container.HostConfig{
		PortBindings: nat.PortMap{
			nat.Port("80/tcp"): []nat.PortBinding{
				{HostIP: "127.0.0.1", HostPort: "0"},
			},
		},
	}, nil)

	d.syncRecords(ctx)

	arpa, _ := dns.ReverseAddr("127.0.0.1")
	resp, _, err := queryDNS(t, d, arpa, dns.TypePTR)
	if err != nil {
		t.Fatalf("ServeDNS error for PTR %s: %v", arpa, err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected PTR record for %s with host_mode ptr, got none", arpa)
	}
	ptr, ok := resp.Answer[0].(*dns.PTR)
	if !ok {
		t.Fatalf("expected PTR record, got %T", resp.Answer[0])
	}
	expected := name + ".docker."
	if ptr.Ptr != expected {
		t.Errorf("expected PTR target %s, got %s", expected, ptr.Ptr)
	}
}

func TestIntegrationPTRAfterRemoval(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	containerID := createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
	}, nil, nil)

	d.syncRecords(ctx)

	// Get the container's IP
	fqdn := name + ".docker."
	resp, _, _ := queryDNS(t, d, fqdn, dns.TypeA)
	if resp == nil || len(resp.Answer) == 0 {
		t.Fatalf("expected A record for %s, got none", fqdn)
	}
	a := resp.Answer[0].(*dns.A)
	arpa, _ := dns.ReverseAddr(a.A.String())

	// Verify PTR exists
	ptrResp, _, _ := queryDNS(t, d, arpa, dns.TypePTR)
	if ptrResp == nil || len(ptrResp.Answer) == 0 {
		t.Fatalf("expected PTR record for %s before removal", arpa)
	}

	// Remove container and re-sync
	_ = cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
	d.syncRecords(ctx)

	// PTR should now pass to next plugin (ErrorHandler returns SERVFAIL)
	_, rcode, _ := queryDNS(t, d, arpa, dns.TypePTR)
	if rcode != dns.RcodeServerFailure {
		t.Errorf("expected SERVFAIL for PTR %s after removal, got rcode %d", arpa, rcode)
	}
}

// assertTxtAnswer fatals if the DNS response does not contain a single
// TXT answer whose Txt slice exactly matches want. Common helper for the
// TXT integration tests below.
func assertTxtAnswer(t *testing.T, d *Docker, qname string, want []string) {
	t.Helper()
	resp, _, err := queryDNS(t, d, qname, dns.TypeTXT)
	if err != nil {
		t.Fatalf("ServeDNS error for %s TXT: %v", qname, err)
	}
	if resp == nil {
		t.Fatalf("expected DNS response for %s TXT, got nil", qname)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("expected exactly one TXT answer for %s, got %d", qname, len(resp.Answer))
	}
	txt, ok := resp.Answer[0].(*dns.TXT)
	if !ok {
		t.Fatalf("expected TXT record for %s, got %T", qname, resp.Answer[0])
	}
	if len(txt.Txt) != len(want) {
		t.Fatalf("expected %d character-strings for %s, got %d: %#v", len(want), qname, len(txt.Txt), txt.Txt)
	}
	for i, w := range want {
		if txt.Txt[i] != w {
			t.Errorf("expected Txt[%d]=%q for %s, got %q", i, w, qname, txt.Txt[i])
		}
	}
}

func TestIntegrationTXTRecord(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			"com.dokku.coredns-docker/txt": "version=1.0.0",
		},
	}, nil, nil)

	d.syncRecords(ctx)

	assertTxtAnswer(t, d, name+".docker.", []string{"version=1.0.0"})
}

func TestIntegrationTXTKeyedRecord(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			"com.dokku.coredns-docker/txt._acme-challenge": "tok123",
		},
	}, nil, nil)

	d.syncRecords(ctx)

	assertTxtAnswer(t, d, "_acme-challenge."+name+".docker.", []string{"tok123"})
}

func TestIntegrationTXTQuotedMultiString(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			// Quoted-form value producing one TXT RR with two character-strings.
			"com.dokku.coredns-docker/txt": `"part1" "part2"`,
		},
	}, nil, nil)

	d.syncRecords(ctx)

	assertTxtAnswer(t, d, name+".docker.", []string{"part1", "part2"})
}

func TestIntegrationTXTQuotedEscapedQuote(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			// `\"` must be post-processed to a literal `"` in the stored
			// character-string.
			"com.dokku.coredns-docker/txt": `"say \"hi\""`,
		},
	}, nil, nil)

	d.syncRecords(ctx)

	assertTxtAnswer(t, d, name+".docker.", []string{`say "hi"`})
}

func TestIntegrationTXTQuotedEscapedBackslash(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			// `\\` must collapse to a single literal backslash.
			"com.dokku.coredns-docker/txt": `"path\\to\\file"`,
		},
	}, nil, nil)

	d.syncRecords(ctx)

	assertTxtAnswer(t, d, name+".docker.", []string{`path\to\file`})
}

func TestIntegrationTXTQuotedDecimalEscape(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			// `\065` decimal → 'A'.
			"com.dokku.coredns-docker/txt": `"hello\065world"`,
		},
	}, nil, nil)

	d.syncRecords(ctx)

	assertTxtAnswer(t, d, name+".docker.", []string{"helloAworld"})
}

func TestIntegrationTXTQuotedMalformedFallback(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			// Leading `"` triggers quoted-form parsing, but no closing quote
			// exists. miekg returns an error and the parser falls back to
			// storing the raw label value verbatim (including the leading
			// quote) as a single character-string.
			"com.dokku.coredns-docker/txt": `"unterminated`,
		},
	}, nil, nil)

	d.syncRecords(ctx)

	assertTxtAnswer(t, d, name+".docker.", []string{`"unterminated`})
}

func TestIntegrationTXTUnquotedVerbatim(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)
	ctx := context.Background()

	name := testContainerName(t, "")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Labels: map[string]string{
			// Non-quoted values bypass master-file parsing entirely, so
			// characters like `=` and `;` pass through untouched.
			"com.dokku.coredns-docker/txt": "key=val;other=val2",
		},
	}, nil, nil)

	d.syncRecords(ctx)

	assertTxtAnswer(t, d, name+".docker.", []string{"key=val;other=val2"})
}

// TestIntegrationSetup drives setup() end-to-end against a real Docker
// daemon via a caddy test controller. Covers the body of setup.go:setup,
// which instantiates a Docker client, pings it, and registers lifecycle
// hooks with the caddy controller.
func TestIntegrationSetup(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		shouldErr bool
	}{
		{
			name:      "default",
			input:     `docker`,
			shouldErr: false,
		},
		{
			name: "custom options",
			input: `docker {
				zone example.org
				ttl 60
				label_prefix com.example
				max_backoff 30s
			}`,
			shouldErr: false,
		},
		{
			name: "parse error propagates",
			input: `docker {
				unknown_option
			}`,
			shouldErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := caddy.NewTestController("dns", tc.input)
			err := setup(c)
			if tc.shouldErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// TestIntegrationStartEventLoop exercises the startEventLoop happy path
// and ctx.Done exit branch. The loop syncs records, connects to Docker's
// event stream, and waits until the supplied context is cancelled.
func TestIntegrationStartEventLoop(t *testing.T) {
	d, _ := setupIntegrationDocker(t, nil)

	// Ensure startEventLoop observes at least one container event by
	// creating a container right after the loop starts.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		d.startEventLoop(ctx)
		close(done)
	}()

	// Wait briefly for the loop to establish its Events subscription and
	// flip d.connected to true. The loop sets connected inside the for
	// header, after calling Events, so a short poll is sufficient.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.RLock()
		connected := d.connected
		d.mu.RUnlock()
		if connected {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	d.mu.RLock()
	if !d.connected {
		d.mu.RUnlock()
		t.Fatal("startEventLoop did not mark d.connected within 1s")
	}
	d.mu.RUnlock()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("startEventLoop did not return within 2s after ctx cancel")
	}
}

// TestIntegrationStartEventLoopHandlesContainerEvent verifies the event
// dispatch branch of startEventLoop: when a container starts, the loop
// receives a message on its events channel and triggers a resync. This
// covers the `case msg := <-msgs` arm of the select and the `backoff =
// 1 * time.Second` reset.
func TestIntegrationStartEventLoopHandlesContainerEvent(t *testing.T) {
	d, cli := setupIntegrationDocker(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		d.startEventLoop(ctx)
		close(done)
	}()

	// Wait for the loop to establish its event subscription.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.RLock()
		connected := d.connected
		d.mu.RUnlock()
		if connected {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Create a container. This should fire a "create" / "start" event
	// which the loop's select will receive.
	name := testContainerName(t, "evt")
	createTestContainer(t, cli, name, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "3600"},
	}, nil, nil)

	// Poll until the container shows up in d.records, which indicates
	// that the event-driven resync has completed.
	fqdn := name + ".docker."
	recordSeen := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.RLock()
		_, recordSeen = d.records[fqdn]
		d.mu.RUnlock()
		if recordSeen {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !recordSeen {
		t.Fatalf("startEventLoop did not resync after container create event; %s missing from records", fqdn)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("startEventLoop did not return within 2s after ctx cancel")
	}
}

