package docker

import (
	"context"
	"net"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/miekg/dns"
)

// mockContainerInspector is a mock container inspector for testing
type mockContainerInspector struct {
	inspections map[string]container.InspectResponse
}

func (m *mockContainerInspector) ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error) {
	return m.inspections[containerID], nil
}

func mustReverseAddr(ip string) string {
	arpa, err := dns.ReverseAddr(ip)
	if err != nil {
		panic(err)
	}
	return arpa
}

func TestGenerateRecords(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		input    GenerateRecordsInput
		expected struct {
			records map[string][]net.IP
			srvs    map[string][]srvRecord
			ptrs    map[string][]string
		}
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
				ptrs    map[string][]string
			}{
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records, srvs, ptrs := generateRecords(ctx, tt.input)

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
		})
	}
}
