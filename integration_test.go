//go:build integration

package docker

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/plugin/test"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
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
