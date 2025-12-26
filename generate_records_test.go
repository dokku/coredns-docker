package docker

import (
	"context"
	"net"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
)

// mockContainerInspector is a mock container inspector for testing
type mockContainerInspector struct {
	inspections map[string]container.InspectResponse
}

func (m *mockContainerInspector) ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error) {
	return m.inspections[containerID], nil
}

func TestGenerateRecords(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		input    GenerateRecordsInput
		expected struct {
			records map[string][]net.IP
			srvs    map[string][]srvRecord
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
				Domain:      "docker.",
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
			}{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
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
				Domain:      "docker.",
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
			}{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
					"www.docker.": {net.ParseIP("172.17.0.2")},
					"app.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
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
				Domain:      "docker.",
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
			}{
				records: map[string][]net.IP{
					"myproj_mysvc_1.docker.": {net.ParseIP("172.17.0.3")},
					"myproj.mysvc.docker.":   {net.ParseIP("172.17.0.3")},
				},
				srvs: map[string][]srvRecord{},
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
				Domain:      "docker.",
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
			}{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.": {
						{target: "web.docker.", port: 80},
					},
				},
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
				Domain:      "docker.",
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
			}{
				records: map[string][]net.IP{
					"db.docker.": {net.ParseIP("172.17.0.3")},
				},
				srvs: map[string][]srvRecord{
					"_tcp._tcp.db.docker.": {
						{target: "db.docker.", port: 5432},
					},
				},
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
				Domain:      "docker.",
				LabelPrefix: "com.dokku.coredns-docker",
			},
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
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
				Domain:      "docker.",
				LabelPrefix: "com.dokku.coredns-docker",
				Networks:    []string{"bridge"},
			},
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
			}{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{},
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
				Domain:      "docker.",
				LabelPrefix: "",
			},
			expected: struct {
				records map[string][]net.IP
				srvs    map[string][]srvRecord
			}{
				records: map[string][]net.IP{
					"web.docker.": {net.ParseIP("172.17.0.2")},
				},
				srvs: map[string][]srvRecord{
					"_http._tcp.web.docker.": {
						{target: "web.docker.", port: 80},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records, srvs := generateRecords(ctx, tt.input)

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
		})
	}
}
