package docker

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sort"
	"testing"
	"text/template"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/miekg/dns"
)

var errInspectFailed = errors.New("simulated inspect failure")

// mockContainerInspector is a mock container inspector for testing
type mockContainerInspector struct {
	inspections map[string]container.InspectResponse
	errors      map[string]error
}

func (m *mockContainerInspector) ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error) {
	if err, ok := m.errors[containerID]; ok {
		return container.InspectResponse{}, err
	}
	return m.inspections[containerID], nil
}

func mustReverseAddr(ip string) string {
	arpa, err := dns.ReverseAddr(ip)
	if err != nil {
		panic(err)
	}
	return arpa
}

type generateRecordsExpected struct {
	records map[string][]net.IP
	srvs    map[string][]srvRecord
	ptrs    map[string][]string
	cnames  map[string]string
	txts    map[string][][]string
}

func TestGenerateRecords(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		input    GenerateRecordsInput
		expected generateRecordsExpected
	}{
		{
			name: "basic container name",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with network aliases",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
										Aliases:   []string{"www", "app"},
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
					"www.docker.": {net.ParseIP("172.17.0.2")},
					"app.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "www.docker.", "app.docker."}},
			},
		},
		{
			name: "container with compose project/service",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/myproj_mysvc_1",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.docker.compose.project": "myproj",
									"com.docker.compose.service": "mysvc",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.3",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"myproj_mysvc_1.docker.": {net.ParseIP("172.17.0.3")},
					"myproj.mysvc.docker.":   {net.ParseIP("172.17.0.3")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.3"): {"myproj_mysvc_1.docker.", "myproj.mysvc.docker."}},
			},
		},
		{
			name: "container with SRV label",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv._tcp._http": "80",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.": {
						{target: "web.docker.", port: 80},
					},
				},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with port mapping fallback",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/db",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.3",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("5432/tcp"): {},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"db.docker.": {net.ParseIP("172.17.0.3")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.db.docker.": {
						{target: "db.docker.", port: 5432},
					},
				},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.3"): {"db.docker."}},
			},
		},
		{
			name: "container with port mapping without protocol",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/app",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.4",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("8080"): {},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"app.docker.": {net.ParseIP("172.17.0.4")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.app.docker.": {
						{target: "app.docker.", port: 8080},
					},
					"_udp._udp.app.docker.": {
						{target: "app.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.4"): {"app.docker."}},
			},
		},
		{
			name: "network filtering",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
						"container2": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/db",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("custom"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"custom": {
										IPAddress: "172.17.0.3",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
					{ID: "container2"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				Networks:    []string{"bridge"},
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "empty label prefix",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"srv._tcp._http": "80",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.": {
						{target: "web.docker.", port: 80},
					},
				},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with invalid port 0 in label",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv._tcp._http": "0",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with invalid port 65536 in label",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv._tcp._http": "65536",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with invalid port 0 in port mapping",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("0/tcp"): {},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with invalid port 65536 in port mapping",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("65536/tcp"): {},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with valid boundary ports in label",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv._tcp._http":  "1",
									"com.dokku.coredns-docker/srv._tcp._https": "65535",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.": {
						{target: "web.docker.", port: 1},
					},
					"_https._tcp.web.docker.": {
						{target: "web.docker.", port: 65535},
					},
				},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with valid boundary ports in port mapping",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("1/tcp"):     {},
									nat.Port("65535/udp"): {},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 1},
					},
					"_udp._udp.web.docker.": {
						{target: "web.docker.", port: 65535},
					},
				},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with nil ContainerJSONBase",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: nil,
							Config:            &container.Config{Labels: map[string]string{}},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {IPAddress: "172.17.0.2"},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
			},
		},
		{
			name: "container with nil NetworkSettings",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config:          &container.Config{Labels: map[string]string{}},
							NetworkSettings: nil,
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
			},
		},
		{
			name: "container with nil Config",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: nil,
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {IPAddress: "172.17.0.2"},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "multi-network container with filter matching secondary",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{Labels: map[string]string{}},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {IPAddress: "172.17.0.2"},
									"custom": {IPAddress: "10.0.0.2"},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				Networks:    []string{"custom"},
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("10.0.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("10.0.0.2"): {"web.docker."}},
			},
		},
		{
			name: "multi-network container with filter matching both",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{Labels: map[string]string{}},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {IPAddress: "172.17.0.2"},
									"custom": {IPAddress: "10.0.0.2"},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				Networks:    []string{"bridge", "custom"},
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2"), net.ParseIP("10.0.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}, mustReverseAddr("10.0.0.2"): {"web.docker."}},
			},
		},
		{
			name: "multi-network container with no filter uses primary only",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{Labels: map[string]string{}},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {IPAddress: "172.17.0.2"},
									"custom": {IPAddress: "10.0.0.2"},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "multi-zone basic container",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker.", "internal."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("172.17.0.2")},
					"web.internal.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "web.internal."}},
			},
		},
		{
			name: "multi-zone with SRV label",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv._tcp._http": "80",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker.", "internal."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("172.17.0.2")},
					"web.internal.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.": {
						{target: "web.docker.", port: 80},
					},
					"_http._tcp.web.internal.": {
						{target: "web.internal.", port: 80},
					},
				},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "web.internal."}},
			},
		},
		{
			name: "container with single hostname label",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/hostname": "myapp",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("172.17.0.2")},
					"myapp.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "myapp.docker."}},
			},
		},
		{
			name: "container with comma-separated hostname labels",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/hostname": "app1,app2,app3",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":  {net.ParseIP("172.17.0.2")},
					"app1.docker.": {net.ParseIP("172.17.0.2")},
					"app2.docker.": {net.ParseIP("172.17.0.2")},
					"app3.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "app1.docker.", "app2.docker.", "app3.docker."}},
			},
		},
		{
			name: "container with hostname label with whitespace",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/hostname": " app1 , app2 , app3 ",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":  {net.ParseIP("172.17.0.2")},
					"app1.docker.": {net.ParseIP("172.17.0.2")},
					"app2.docker.": {net.ParseIP("172.17.0.2")},
					"app3.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "app1.docker.", "app2.docker.", "app3.docker."}},
			},
		},
		{
			name: "container with empty hostname label",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/hostname": "",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with hostname label containing only commas and spaces",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/hostname": " , , ",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with hostname label and empty label prefix",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"hostname": "myapp",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("172.17.0.2")},
					"myapp.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "myapp.docker."}},
			},
		},
		{
			name: "container with hostname label and SRV records",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/hostname":      "myapp",
									"com.dokku.coredns-docker/srv._tcp._http": "80",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("172.17.0.2")},
					"myapp.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.": {
						{target: "web.docker.", port: 80},
					},
					"_http._tcp.myapp.docker.": {
						{target: "myapp.docker.", port: 80},
					},
				},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "myapp.docker."}},
			},
		},
		{
			name: "container with hostname label and multi-zone",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/hostname": "myapp",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker.", "internal."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":     {net.ParseIP("172.17.0.2")},
					"web.internal.":   {net.ParseIP("172.17.0.2")},
					"myapp.docker.":   {net.ParseIP("172.17.0.2")},
					"myapp.internal.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "web.internal.", "myapp.docker.", "myapp.internal."}},
			},
		},
		{
			name: "container with wildcard enabled",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/wildcard": "true",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("172.17.0.2")},
					"*.web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with wildcard disabled",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/wildcard": "false",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with wildcard and SRV labels",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/wildcard":       "true",
									"com.dokku.coredns-docker/srv._tcp._http": "80",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("172.17.0.2")},
					"*.web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.": {
						{target: "web.docker.", port: 80},
					},
					"_http._tcp.*.web.docker.": {
						{target: "web.docker.", port: 80},
					},
				},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with wildcard and empty label prefix",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"wildcard": "true",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("172.17.0.2")},
					"*.web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with wildcard and hostname label",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/wildcard": "true",
									"com.dokku.coredns-docker/hostname": "myapp",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":     {net.ParseIP("172.17.0.2")},
					"*.web.docker.":   {net.ParseIP("172.17.0.2")},
					"myapp.docker.":   {net.ParseIP("172.17.0.2")},
					"*.myapp.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "myapp.docker."}},
			},
		},
		{
			name: "container with wildcard and multi-zone",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/wildcard": "true",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker.", "internal."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":     {net.ParseIP("172.17.0.2")},
					"*.web.docker.":   {net.ParseIP("172.17.0.2")},
					"web.internal.":   {net.ParseIP("172.17.0.2")},
					"*.web.internal.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "web.internal."}},
			},
		},
		{
			name: "container with wildcard and network aliases",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/wildcard": "true",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
										Aliases:   []string{"www"},
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("172.17.0.2")},
					"*.web.docker.": {net.ParseIP("172.17.0.2")},
					"www.docker.":   {net.ParseIP("172.17.0.2")},
					"*.www.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "www.docker."}},
			},
		},
		{
			name: "container name overlaps with alias (dedup)",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
										Aliases:   []string{"web", "api"},
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
					"api.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "api.docker."}},
			},
		},
		{
			name: "container name overlaps with DNSNames (dedup)",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
										DNSNames:  []string{"web", "abc123"},
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":    {net.ParseIP("172.17.0.2")},
					"abc123.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "abc123.docker."}},
			},
		},
		{
			name: "alias and DNSNames both overlap with container name (dedup)",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
										Aliases:   []string{"web"},
										DNSNames:  []string{"web", "WEB"},
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "SRV dedup when name overlaps with alias",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv._tcp._http": "8080",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
										Aliases:   []string{"web"},
										DNSNames:  []string{"web"},
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.": {{target: "web.docker.", port: 8080}},
				},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "wildcard dedup when name overlaps with alias",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/wildcard":       "true",
									"com.dokku.coredns-docker/srv._tcp._http": "8080",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
										Aliases:   []string{"web"},
										DNSNames:  []string{"web"},
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("172.17.0.2")},
					"*.web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.":   {{target: "web.docker.", port: 8080}},
					"_http._tcp.*.web.docker.": {{target: "web.docker.", port: 8080}},
				},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "multi-network same IP deduplicated",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
									"custom": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				Networks:    []string{"bridge", "custom"},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "host_mode basic A record with explicit host binding",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): []nat.PortBinding{
										{HostIP: "192.168.1.10", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("192.168.1.10")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode wildcard host IP 0.0.0.0 normalizes to 127.0.0.1",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): []nat.PortBinding{
										{HostIP: "0.0.0.0", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("127.0.0.1")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode wildcard host IP :: normalizes to ::1",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): []nat.PortBinding{
										{HostIP: "::", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("::1")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode SRV label translated to host port",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv._tcp._http": "80",
								},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): []nat.PortBinding{
										{HostIP: "127.0.0.1", HostPort: "18080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("127.0.0.1")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.": {
						{target: "web.docker.", port: 18080},
					},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode multiple bindings for same container port",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv._tcp._http": "80",
								},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): []nat.PortBinding{
										{HostIP: "192.168.1.10", HostPort: "8080"},
										{HostIP: "192.168.1.11", HostPort: "9090"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					// sorted ascending by string() form
					"web.docker.": {net.ParseIP("192.168.1.10"), net.ParseIP("192.168.1.11")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
						{target: "web.docker.", port: 9090},
					},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode SRV label with no matching binding is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv._tcp._http": "80",
								},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("443/tcp"): []nat.PortBinding{
										{HostIP: "127.0.0.1", HostPort: "8443"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				// Label is present so fallback does NOT run; no _http SRV
				// is emitted and no fallback _tcp._tcp is emitted either.
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("127.0.0.1")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode container with no port bindings is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
			},
		},
		{
			name: "host_mode empty HostPort skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): []nat.PortBinding{
										{HostIP: "127.0.0.1", HostPort: ""},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				// empty HostPort binding is skipped -> container has no
				// usable bindings -> no records at all.
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
			},
		},
		{
			name: "host_mode unions names across multiple networks",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
											Aliases:   []string{"alpha"},
										},
										"custom": {
											IPAddress: "10.0.0.2",
											Aliases:   []string{"beta"},
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): []nat.PortBinding{
										{HostIP: "127.0.0.1", HostPort: "18080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				Networks:    []string{"bridge", "custom"},
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("127.0.0.1")},
					"alpha.docker.": {net.ParseIP("127.0.0.1")},
					"beta.docker.":  {net.ParseIP("127.0.0.1")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.":   {{target: "web.docker.", port: 18080}},
					"_tcp._tcp.alpha.docker.": {{target: "alpha.docker.", port: 18080}},
					"_tcp._tcp.beta.docker.":  {{target: "beta.docker.", port: 18080}},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode wildcard label generates wildcard A and SRV",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/wildcard":       "true",
									"com.dokku.coredns-docker/srv._tcp._http": "80",
								},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): []nat.PortBinding{
										{HostIP: "127.0.0.1", HostPort: "18080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("127.0.0.1")},
					"*.web.docker.": {net.ParseIP("127.0.0.1")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.":   {{target: "web.docker.", port: 18080}},
					"_http._tcp.*.web.docker.": {{target: "web.docker.", port: 18080}},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode with HostModePTR emits PTR records",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/wildcard": "true",
								},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): []nat.PortBinding{
										{HostIP: "192.168.1.10", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
				HostModePTR: true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("192.168.1.10")},
					"*.web.docker.": {net.ParseIP("192.168.1.10")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.":   {{target: "web.docker.", port: 8080}},
					"_tcp._tcp.*.web.docker.": {{target: "web.docker.", port: 8080}},
				},
				// Wildcard FQDN must NOT appear in PTR records.
				ptrs: map[string][]string{
					mustReverseAddr("192.168.1.10"): {"web.docker."},
				},
			},
		},
		{
			name: "container with cname label",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/cname": "external.example.com.",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				// CNAME suppresses A/AAAA/SRV/PTR.
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
				cnames: map[string]string{
					"web.docker.": "external.example.com.",
				},
			},
		},
		{
			name: "container with cname label missing trailing dot",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/cname": "external.example.com",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
				cnames: map[string]string{
					"web.docker.": "external.example.com.",
				},
			},
		},
		{
			name: "container with empty cname label falls back to A records",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/cname": "",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with cname and hostname label uses cname for all names",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/cname":    "external.example.com.",
									"com.dokku.coredns-docker/hostname": "myapp,www",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
										Aliases:   []string{"alias1"},
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
				cnames: map[string]string{
					"web.docker.":    "external.example.com.",
					"alias1.docker.": "external.example.com.",
					"myapp.docker.":  "external.example.com.",
					"www.docker.":    "external.example.com.",
				},
			},
		},
		{
			name: "container with cname label suppresses srv labels",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/cname":          "external.example.com.",
									"com.dokku.coredns-docker/srv._tcp._http": "80",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
				cnames: map[string]string{
					"web.docker.": "external.example.com.",
				},
			},
		},
		{
			name: "container with cname and wildcard label",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/cname":    "external.example.com.",
									"com.dokku.coredns-docker/wildcard": "true",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
				cnames: map[string]string{
					"web.docker.":   "external.example.com.",
					"*.web.docker.": "external.example.com.",
				},
			},
		},
		{
			name: "container with cname label and multi-zone",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/cname": "external.example.com.",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker.", "internal."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
				cnames: map[string]string{
					"web.docker.":   "external.example.com.",
					"web.internal.": "external.example.com.",
				},
			},
		},
		{
			name: "container with cname label and empty label prefix",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"cname": "external.example.com.",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
				cnames: map[string]string{
					"web.docker.": "external.example.com.",
				},
			},
		},
		{
			name: "container with cname label in host mode",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/cname": "external.example.com.",
								},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): []nat.PortBinding{
										{HostIP: "127.0.0.1", HostPort: "18080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
				HostModePTR: true,
			},
			expected: generateRecordsExpected{
				// Host mode: CNAME still short-circuits before host bindings are
				// examined, so no A/AAAA/SRV/PTR records are emitted.
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
				cnames: map[string]string{
					"web.docker.": "external.example.com.",
				},
			},
		},
		{
			name: "container with simple TXT label",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/txt": "v=spf1 -all",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
				txts: map[string][][]string{
					"web.docker.": {{"v=spf1 -all"}},
				},
			},
		},
		{
			name: "container with keyed TXT label",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/txt._acme-challenge": "tok123",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
				txts: map[string][][]string{
					"_acme-challenge.web.docker.": {{"tok123"}},
				},
			},
		},
		{
			name: "container with multiple TXT labels",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/txt":         "v=spf1 -all",
									"com.dokku.coredns-docker/txt.info":    "version=1.0.0",
									"com.dokku.coredns-docker/txt.contact": "admin@example.com",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
				txts: map[string][][]string{
					"web.docker.":         {{"v=spf1 -all"}},
					"info.web.docker.":    {{"version=1.0.0"}},
					"contact.web.docker.": {{"admin@example.com"}},
				},
			},
		},
		{
			name: "container with TXT label and empty label prefix",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"txt":      "hello",
									"txt.info": "world",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
				txts: map[string][][]string{
					"web.docker.":      {{"hello"}},
					"info.web.docker.": {{"world"}},
				},
			},
		},
		{
			name: "container with TXT label and network aliases",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/txt": "metadata",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
										Aliases:   []string{"www"},
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
					"www.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker.", "www.docker."}},
				txts: map[string][][]string{
					"web.docker.": {{"metadata"}},
					"www.docker.": {{"metadata"}},
				},
			},
		},
		{
			name: "container with TXT label and wildcard",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/txt":      "metadata",
									"com.dokku.coredns-docker/wildcard": "true",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("172.17.0.2")},
					"*.web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
				txts: map[string][][]string{
					"web.docker.":   {{"metadata"}},
					"*.web.docker.": {{"metadata"}},
				},
			},
		},
		{
			name: "container with TXT label suppressed under cname",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/cname": "external.example.com.",
									"com.dokku.coredns-docker/txt":   "metadata",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				// CNAME fully suppresses all other record types for the container,
				// including TXT, per RFC 1034 §3.6.2.
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
				cnames: map[string]string{
					"web.docker.": "external.example.com.",
				},
			},
		},
		{
			name: "container with empty TXT label value",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/txt.empty": "",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
				txts: map[string][][]string{
					"empty.web.docker.": {{""}},
				},
			},
		},
		{
			name: "container with bare txt. label is ignored",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/txt.": "ignored",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with quoted-form TXT label, single string",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/txt": `"hello world"`,
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
				txts: map[string][][]string{
					// Quotes get stripped by the master-file parser, leaving one
					// character-string with the quoted contents.
					"web.docker.": {{"hello world"}},
				},
			},
		},
		{
			name: "container with quoted-form TXT label, multi-string",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/txt": `"part1" "part2"`,
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
				txts: map[string][][]string{
					// One TXT RR with two character-strings.
					"web.docker.": {{"part1", "part2"}},
				},
			},
		},
		{
			name: "container with quoted-form TXT label, escaped quote",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									`com.dokku.coredns-docker/txt`: `"say \"hi\""`,
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
				txts: map[string][][]string{
					// Backslash-escaped quotes become literal quotes in the
					// stored character-string.
					"web.docker.": {{`say "hi"`}},
				},
			},
		},
		{
			name: "container with quoted-form TXT label, decimal escape",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									// \065 decimal == 'A' (0x41)
									"com.dokku.coredns-docker/txt": `"hello\065world"`,
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
				txts: map[string][][]string{
					"web.docker.": {{"helloAworld"}},
				},
			},
		},
		{
			name: "container with malformed quoted-form TXT label falls back to verbatim",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/txt": `"unterminated`,
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
				txts: map[string][][]string{
					// Parse failed → raw label value stored verbatim as a single
					// character-string, leading quote preserved.
					"web.docker.": {{`"unterminated`}},
				},
			},
		},
		{
			name: "container inspect error is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					errors: map[string]error{
						"container1": errInspectFailed,
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{},
		},
		{
			name: "container with empty NetworkMode falls back to bridge",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode(""),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with default NetworkMode falls back to bridge",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("default"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container whose primary network is not in NetworkSettings is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("missing-net"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{},
		},
		{
			name: "container with non-numeric SRV label port is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv._tcp._http": "notanumber",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with malformed SRV label shape is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									// Only one segment after the srv. prefix; parse() splits
									// on "." expecting exactly two parts.
									"com.dokku.coredns-docker/srv.onesegment": "80",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with non-numeric port in port mapping is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("abc/tcp"): {},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "container with invalid IP address on network is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "not-an-ip",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{},
		},
		{
			name: "zone without trailing dot has one appended",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				// Zone intentionally missing the trailing dot: the code path at
				// docker.go line 1092 adds it.
				Zones:       []string{"docker"},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
			},
		},
		{
			name: "cname zone without trailing dot has one appended",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/cname": "external.example.com",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker"},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
				cnames: map[string]string{
					"web.docker.": "external.example.com.",
				},
			},
		},
		{
			name: "cname container with compose project and service",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/cname": "external.example.com.",
									"com.docker.compose.project":     "myproj",
									"com.docker.compose.service":     "mysvc",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {
										IPAddress: "172.17.0.2",
									},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{},
				srvs:    map[string][]srvRecord{},
				ptrs:    map[string][]string{},
				cnames: map[string]string{
					"web.docker.":          "external.example.com.",
					"myproj.mysvc.docker.": "external.example.com.",
				},
			},
		},
		{
			name: "host_mode with non-numeric SRV label port is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv._tcp._http": "notanumber",
								},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): {
										{HostIP: "127.0.0.1", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("127.0.0.1")},
				},
				// When no SRV label parses, host-mode falls back to
				// emitting SRV records from the container's port bindings.
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode with malformed SRV label shape is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv.onesegment": "80",
								},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): {
										{HostIP: "127.0.0.1", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("127.0.0.1")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode with out-of-range SRV label port is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv._tcp._http": "99999",
								},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): {
										{HostIP: "127.0.0.1", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("127.0.0.1")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode with unparseable HostIP is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): {
										{HostIP: "not-an-ip", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{},
		},
		{
			name: "host_mode with non-numeric host port is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): {
										{HostIP: "127.0.0.1", HostPort: "abc"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{},
		},
		{
			name: "host_mode with compose project and service",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.docker.compose.project": "myproj",
									"com.docker.compose.service": "mysvc",
								},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): {
										{HostIP: "127.0.0.1", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":          {net.ParseIP("127.0.0.1")},
					"myproj.mysvc.docker.": {net.ParseIP("127.0.0.1")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
					"_tcp._tcp.myproj.mysvc.docker.": {
						{target: "myproj.mysvc.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode with TXT labels",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/txt":      "v=spf1 -all",
									"com.dokku.coredns-docker/txt.info": "version=1.0.0",
								},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): {
										{HostIP: "127.0.0.1", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("127.0.0.1")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{},
				txts: map[string][][]string{
					"web.docker.":      {{"v=spf1 -all"}},
					"info.web.docker.": {{"version=1.0.0"}},
				},
			},
		},
		{
			name: "host_mode with wildcard and TXT labels",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/wildcard": "true",
									"com.dokku.coredns-docker/txt":      "v=spf1 -all",
									"com.dokku.coredns-docker/txt.info": "version=1.0.0",
								},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): {
										{HostIP: "127.0.0.1", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("127.0.0.1")},
					"*.web.docker.": {net.ParseIP("127.0.0.1")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
					"_tcp._tcp.*.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{},
				txts: map[string][][]string{
					"web.docker.":        {{"v=spf1 -all"}},
					"info.web.docker.":   {{"version=1.0.0"}},
					"*.web.docker.":      {{"v=spf1 -all"}},
					"info.*.web.docker.": {{"version=1.0.0"}},
				},
			},
		},
		{
			name: "host_mode zone without trailing dot has one appended",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("80/tcp"): {
										{HostIP: "127.0.0.1", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker"},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("127.0.0.1")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode port binding fallback with non-numeric container port is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								// Two bindings: one bogus container port (skipped at the
								// host-mode SRV fallback split) and one legitimate one so
								// there is still a record left over for the A entry.
								ns.Ports = nat.PortMap{
									nat.Port("abc/tcp"): {
										{HostIP: "127.0.0.1", HostPort: "9999"},
									},
									nat.Port("80/tcp"): {
										{HostIP: "127.0.0.1", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("127.0.0.1")},
				},
				// Only the 80/tcp binding survives the fallback SRV generation.
				// The 9999 host port for the "abc/tcp" spec is dropped because
				// the container-side port did not parse.
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "host_mode port binding fallback with out-of-range container port is skipped",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									nat.Port("99999/tcp"): {
										{HostIP: "127.0.0.1", HostPort: "9999"},
									},
									nat.Port("80/tcp"): {
										{HostIP: "127.0.0.1", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("127.0.0.1")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{},
			},
		},
		{
			name: "multi-network SRV label fires dup check on second network",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/srv._tcp._http": "80",
									"com.dokku.coredns-docker/wildcard":       "true",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {IPAddress: "172.17.0.2"},
									"custom": {IPAddress: "10.0.0.2"},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				// Filter selects both networks so the container is processed twice.
				// On the second pass, the SRV emission at _http._tcp.web.docker.
				// hits the isDupSrv branch because the first pass already wrote
				// an identical entry.
				Networks: []string{"bridge", "custom"},
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("172.17.0.2"), net.ParseIP("10.0.0.2")},
					"*.web.docker.": {net.ParseIP("172.17.0.2"), net.ParseIP("10.0.0.2")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.": {
						{target: "web.docker.", port: 80},
					},
					"_http._tcp.*.web.docker.": {
						{target: "web.docker.", port: 80},
					},
				},
				ptrs: map[string][]string{
					mustReverseAddr("172.17.0.2"): {"web.docker."},
					mustReverseAddr("10.0.0.2"):   {"web.docker."},
				},
			},
		},
		{
			name: "container with keyed TXT label and wildcard",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{
									"com.dokku.coredns-docker/txt.info": "version=1",
									"com.dokku.coredns-docker/wildcard": "true",
								},
							},
							NetworkSettings: &container.NetworkSettings{
								Networks: map[string]*network.EndpointSettings{
									"bridge": {IPAddress: "172.17.0.2"},
								},
							},
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.":   {net.ParseIP("172.17.0.2")},
					"*.web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
				ptrs: map[string][]string{mustReverseAddr("172.17.0.2"): {"web.docker."}},
				txts: map[string][][]string{
					"info.web.docker.":   {{"version=1"}},
					"info.*.web.docker.": {{"version=1"}},
				},
			},
		},
		{
			name: "host_mode port binding fallback with no protocol segment",
			input: GenerateRecordsInput{
				Inspector: &mockContainerInspector{
					inspections: map[string]container.InspectResponse{
						"container1": {
							ContainerJSONBase: &container.ContainerJSONBase{
								Name: "/web",
								HostConfig: &container.HostConfig{
									NetworkMode: container.NetworkMode("bridge"),
								},
							},
							Config: &container.Config{
								Labels: map[string]string{},
							},
							NetworkSettings: func() *container.NetworkSettings {
								ns := &container.NetworkSettings{
									Networks: map[string]*network.EndpointSettings{
										"bridge": {
											IPAddress: "172.17.0.2",
										},
									},
								}
								ns.Ports = nat.PortMap{
									// No "/tcp" or "/udp" suffix — len(parts) == 1.
									nat.Port("80"): {
										{HostIP: "127.0.0.1", HostPort: "8080"},
									},
								}
								return ns
							}(),
						},
					},
				},
				Containers: []container.Summary{
					{ID: "container1"},
				},
				Zones:       []string{"docker."},
				LabelPrefix: "com.dokku.coredns-docker",
				HostMode:    true,
			},
			expected: generateRecordsExpected{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("127.0.0.1")},
				},
				// Without a protocol segment, fallback emits both _tcp._tcp and _udp._udp.
				srvs: map[string][]srvRecord{
					"_tcp._tcp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
					"_udp._udp.web.docker.": {
						{target: "web.docker.", port: 8080},
					},
				},
				ptrs: map[string][]string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records, srvs, ptrs, cnames, txts := generateRecords(ctx, tt.input)

			// Check records
			if len(records) != len(tt.expected.records) {
				t.Errorf("expected %d records, got %d", len(tt.expected.records), len(records))
				for fqdn := range records {
					if _, ok := tt.expected.records[fqdn]; !ok {
						t.Errorf("unexpected record: %s", fqdn)
					}
				}
				for fqdn := range tt.expected.records {
					if _, ok := records[fqdn]; !ok {
						t.Errorf("missing record: %s", fqdn)
					}
				}
			}
			for fqdn, expectedIPs := range tt.expected.records {
				actualIPs, ok := records[fqdn]
				if !ok {
					t.Errorf("expected record for %s, not found", fqdn)
					continue
				}
				if len(actualIPs) != len(expectedIPs) {
					t.Errorf("expected %d IPs for %s, got %d", len(expectedIPs), fqdn, len(actualIPs))
					continue
				}
				for i, expectedIP := range expectedIPs {
					if !actualIPs[i].Equal(expectedIP) {
						t.Errorf("expected IP %s for %s at index %d, got %s", expectedIP, fqdn, i, actualIPs[i])
					}
				}
			}

			// Check SRV records
			if len(srvs) != len(tt.expected.srvs) {
				t.Errorf("expected %d SRV records, got %d", len(tt.expected.srvs), len(srvs))
				for srvName := range srvs {
					if _, ok := tt.expected.srvs[srvName]; !ok {
						t.Errorf("unexpected SRV record: %s", srvName)
					}
				}
				for srvName := range tt.expected.srvs {
					if _, ok := srvs[srvName]; !ok {
						t.Errorf("missing SRV record: %s", srvName)
					}
				}
			}
			for srvName, expectedSrvs := range tt.expected.srvs {
				actualSrvs, ok := srvs[srvName]
				if !ok {
					t.Errorf("expected SRV record for %s, not found", srvName)
					continue
				}
				if len(actualSrvs) != len(expectedSrvs) {
					t.Errorf("expected %d SRV records for %s, got %d", len(expectedSrvs), srvName, len(actualSrvs))
					continue
				}
				for i, expectedSrv := range expectedSrvs {
					if actualSrvs[i].target != expectedSrv.target || actualSrvs[i].port != expectedSrv.port {
						t.Errorf("expected SRV %+v for %s at index %d, got %+v", expectedSrv, srvName, i, actualSrvs[i])
					}
				}
			}

			// Check PTR records
			if len(ptrs) != len(tt.expected.ptrs) {
				t.Errorf("expected %d PTR records, got %d", len(tt.expected.ptrs), len(ptrs))
				for arpa := range ptrs {
					if _, ok := tt.expected.ptrs[arpa]; !ok {
						t.Errorf("unexpected PTR record: %s", arpa)
					}
				}
				for arpa := range tt.expected.ptrs {
					if _, ok := ptrs[arpa]; !ok {
						t.Errorf("missing PTR record: %s", arpa)
					}
				}
			}
			for arpa, expectedFqdns := range tt.expected.ptrs {
				actualFqdns, ok := ptrs[arpa]
				if !ok {
					t.Errorf("expected PTR record for %s, not found", arpa)
					continue
				}
				if len(actualFqdns) != len(expectedFqdns) {
					t.Errorf("expected %d FQDNs for %s, got %d", len(expectedFqdns), arpa, len(actualFqdns))
					continue
				}
				for i, expectedFqdn := range expectedFqdns {
					if actualFqdns[i] != expectedFqdn {
						t.Errorf("expected FQDN %s for %s at index %d, got %s", expectedFqdn, arpa, i, actualFqdns[i])
					}
				}
			}

			// Check CNAME records
			if len(cnames) != len(tt.expected.cnames) {
				t.Errorf("expected %d CNAME records, got %d", len(tt.expected.cnames), len(cnames))
				for fqdn := range cnames {
					if _, ok := tt.expected.cnames[fqdn]; !ok {
						t.Errorf("unexpected CNAME record: %s", fqdn)
					}
				}
				for fqdn := range tt.expected.cnames {
					if _, ok := cnames[fqdn]; !ok {
						t.Errorf("missing CNAME record: %s", fqdn)
					}
				}
			}
			for fqdn, expectedTarget := range tt.expected.cnames {
				actualTarget, ok := cnames[fqdn]
				if !ok {
					t.Errorf("expected CNAME record for %s, not found", fqdn)
					continue
				}
				if actualTarget != expectedTarget {
					t.Errorf("expected CNAME target %s for %s, got %s", expectedTarget, fqdn, actualTarget)
				}
			}

			// Check TXT records
			if len(txts) != len(tt.expected.txts) {
				t.Errorf("expected %d TXT record names, got %d", len(tt.expected.txts), len(txts))
				for fqdn := range txts {
					if _, ok := tt.expected.txts[fqdn]; !ok {
						t.Errorf("unexpected TXT record: %s", fqdn)
					}
				}
				for fqdn := range tt.expected.txts {
					if _, ok := txts[fqdn]; !ok {
						t.Errorf("missing TXT record: %s", fqdn)
					}
				}
			}
			for fqdn, expectedRRs := range tt.expected.txts {
				actualRRs, ok := txts[fqdn]
				if !ok {
					t.Errorf("expected TXT record for %s, not found", fqdn)
					continue
				}
				if len(actualRRs) != len(expectedRRs) {
					t.Errorf("expected %d TXT RRs for %s, got %d", len(expectedRRs), fqdn, len(actualRRs))
					continue
				}
				for i, expectedRR := range expectedRRs {
					if !reflect.DeepEqual(actualRRs[i], expectedRR) {
						t.Errorf("expected TXT RR %+v for %s at index %d, got %+v", expectedRR, fqdn, i, actualRRs[i])
					}
				}
			}
		})
	}
}

func TestGenerateRecordsNameFromLabels(t *testing.T) {
	mustTmpl := func(body string) *template.Template {
		tmpl, err := parseNameTemplate(body)
		if err != nil {
			t.Fatalf("parseNameTemplate(%q): %v", body, err)
		}
		return tmpl
	}

	makeContainer := func(name, ip string, labels map[string]string) (string, container.InspectResponse) {
		return name, container.InspectResponse{
			ContainerJSONBase: &container.ContainerJSONBase{
				Name: "/" + name,
				HostConfig: &container.HostConfig{
					NetworkMode: container.NetworkMode("bridge"),
				},
			},
			Config: &container.Config{Labels: labels},
			NetworkSettings: &container.NetworkSettings{
				Networks: map[string]*network.EndpointSettings{
					"bridge": {IPAddress: ip},
				},
			},
		}
	}

	dokkuLabels := func(process string) map[string]string {
		return map[string]string{
			"com.dokku.app-name":     "docs",
			"com.dokku.process-type": process,
		}
	}

	id1, c1 := makeContainer("docs.web.1", "172.17.0.4", dokkuLabels("web"))
	id2, c2 := makeContainer("docs.web.2", "172.17.0.14", dokkuLabels("web"))
	id3, c3 := makeContainer("docs.web.3", "172.17.0.16", dokkuLabels("web"))
	id4, c4 := makeContainer("docs.worker.1", "172.17.0.20", dokkuLabels("worker"))
	id5, c5 := makeContainer("untagged", "172.17.0.30", map[string]string{})

	input := GenerateRecordsInput{
		Inspector: &mockContainerInspector{
			inspections: map[string]container.InspectResponse{
				id1: c1, id2: c2, id3: c3, id4: c4, id5: c5,
			},
		},
		Containers: []container.Summary{
			{ID: id1}, {ID: id2}, {ID: id3}, {ID: id4}, {ID: id5},
		},
		Zones:       []string{"docker."},
		LabelPrefix: "com.dokku.coredns-docker",
		NameTemplates: []*template.Template{
			mustTmpl(`{{label "com.dokku.app-name"}}.{{label "com.dokku.process-type"}}`),
			mustTmpl(`{{label "com.dokku.app-name"}}`),
		},
	}

	records, _, _, _, _ := generateRecords(context.Background(), input)

	assertIPs := func(fqdn string, want ...string) {
		t.Helper()
		got, ok := records[fqdn]
		if !ok {
			t.Errorf("expected records for %s, none found", fqdn)
			return
		}
		gotStrs := make([]string, len(got))
		for i, ip := range got {
			gotStrs[i] = ip.String()
		}
		sort.Strings(gotStrs)
		wantSorted := append([]string(nil), want...)
		sort.Strings(wantSorted)
		if !reflect.DeepEqual(gotStrs, wantSorted) {
			t.Errorf("for %s: got %v, want %v", fqdn, gotStrs, wantSorted)
		}
	}

	// docs.web (the {{label app}}.{{label process}} template) collects all
	// three web dynos.
	assertIPs("docs.web.docker.", "172.17.0.4", "172.17.0.14", "172.17.0.16")

	// docs (the {{label app}} template) collects every container that
	// carries the app-name label, which is all four Dokku containers.
	assertIPs("docs.docker.", "172.17.0.4", "172.17.0.14", "172.17.0.16", "172.17.0.20")

	// docs.worker collects just the worker container.
	assertIPs("docs.worker.docker.", "172.17.0.20")

	// The untagged container does not contribute to either templated FQDN
	// (both reference labels it does not carry) but is still reachable
	// under its container name.
	if _, ok := records["untagged.docker."]; !ok {
		t.Errorf("expected per-container record for untagged.docker.")
	}
	if ips, ok := records["docs.web.docker."]; ok {
		for _, ip := range ips {
			if ip.String() == "172.17.0.30" {
				t.Errorf("untagged container leaked into docs.web.docker.")
			}
		}
	}

	// Per-container names are unaffected: each docs.web.N still resolves
	// to exactly its own IP.
	assertIPs("docs.web.1.docker.", "172.17.0.4")
	assertIPs("docs.web.2.docker.", "172.17.0.14")
	assertIPs("docs.web.3.docker.", "172.17.0.16")
}
