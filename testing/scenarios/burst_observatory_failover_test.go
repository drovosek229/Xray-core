package scenarios

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/commander"
	"github.com/xtls/xray-core/app/observatory"
	observatoryservice "github.com/xtls/xray-core/app/observatory/command"
	"github.com/xtls/xray-core/app/observatory/burst"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/uuid"
	core "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/dokodemo"
	"github.com/xtls/xray-core/proxy/freedom"
	"github.com/xtls/xray-core/proxy/vmess"
	vmessinbound "github.com/xtls/xray-core/proxy/vmess/inbound"
	vmessoutbound "github.com/xtls/xray-core/proxy/vmess/outbound"
	v2httptest "github.com/xtls/xray-core/testing/servers/http"
	"github.com/xtls/xray-core/testing/servers/tcp"
	"github.com/xtls/xray-core/transport/internet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestBurstObservatoryRouteFailover(t *testing.T) {
	probePort := tcp.PickPort()
	probeServer := &v2httptest.Server{
		Port: probePort,
		PathHandler: map[string]http.HandlerFunc{
			"/probe": func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			},
		},
	}
	_, err := probeServer.Start()
	common.Must(err)
	defer probeServer.Close()

	echoServer := tcp.Server{MsgProcessor: xor}
	echoDest, err := echoServer.Start()
	common.Must(err)
	defer echoServer.Close()

	requestedPort := tcp.PickPort()
	serverUserID := protocol.NewID(uuid.New())

	serverAPort := tcp.PickPort()
	serverBPort := tcp.PickPort()
	serverA := newBurstProxyServerConfig(serverAPort, serverUserID, requestedPort, nil)
	serverB := newBurstProxyServerConfig(serverBPort, serverUserID, requestedPort, &echoDest)

	clientPort := tcp.PickPort()
	cmdPort := tcp.PickPort()
	client := &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(newObservatoryCommanderConfig(cmdPort)),
			serial.ToTypedMessage(&router.Config{
				BalancingRule: []*router.BalancingRule{
					{
						Tag:              "route-balancer",
						OutboundSelector: []string{"outer-"},
						Strategy:         "roundrobin",
					},
				},
				Rule: []*router.RoutingRule{
					{
						InboundTag: []string{"in"},
						TargetTag: &router.RoutingRule_BalancingTag{
							BalancingTag: "route-balancer",
						},
					},
				},
			}),
			serial.ToTypedMessage(newBurstObservatoryConfig("outer-", probePort)),
		},
		Inbound: []*core.InboundHandlerConfig{
			{
				Tag: "in",
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(clientPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
				ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
					Address:  net.NewIPOrDomain(net.LocalHostIP),
					Port:     uint32(requestedPort),
					Networks: []net.Network{net.Network_TCP},
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			newVMessOutbound("outer-a", serverAPort, serverUserID),
			newVMessOutbound("outer-b", serverBPort, serverUserID),
		},
	}

	servers, err := InitializeServerConfigs(serverA, serverB, client)
	common.Must(err)
	defer CloseAllServers(servers)

	cmdConn, statusClient := mustConnectObservatoryClient(t, cmdPort)
	defer cmdConn.Close()

	waitForObservedAlive(t, statusClient, map[string]bool{
		"outer-a": true,
		"outer-b": true,
	})

	if err := testTCPConn(clientPort, 1024, 5*time.Second)(); err != nil {
		t.Fatal(err)
	}

	waitForObservedAlive(t, statusClient, map[string]bool{
		"outer-a": false,
		"outer-b": true,
	})

	if err := testTCPConn(clientPort, 1024, 5*time.Second)(); err != nil {
		t.Fatal(err)
	}
}

func TestBurstObservatoryDialerProxyFailover(t *testing.T) {
	probePort := tcp.PickPort()
	probeServer := &v2httptest.Server{
		Port: probePort,
		PathHandler: map[string]http.HandlerFunc{
			"/probe": func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			},
		},
	}
	_, err := probeServer.Start()
	common.Must(err)
	defer probeServer.Close()

	echoServer := tcp.Server{MsgProcessor: xor}
	echoDest, err := echoServer.Start()
	common.Must(err)
	defer echoServer.Close()

	requestedPort := tcp.PickPort()
	serverUserID := protocol.NewID(uuid.New())

	serverAPort := tcp.PickPort()
	serverBPort := tcp.PickPort()
	serverA := newBurstProxyServerConfig(serverAPort, serverUserID, requestedPort, nil)
	serverB := newBurstProxyServerConfig(serverBPort, serverUserID, requestedPort, &echoDest)

	clientPort := tcp.PickPort()
	cmdPort := tcp.PickPort()
	client := &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(newObservatoryCommanderConfig(cmdPort)),
			serial.ToTypedMessage(&router.Config{
				BalancingRule: []*router.BalancingRule{
					{
						Tag:              "proxy-balancer",
						OutboundSelector: []string{"proxy-"},
						Strategy:         "roundrobin",
					},
				},
			}),
			serial.ToTypedMessage(newBurstObservatoryConfig("proxy-", probePort)),
		},
		Inbound: []*core.InboundHandlerConfig{
			{
				Tag: "in",
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(clientPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
				ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
					Address:  net.NewIPOrDomain(net.LocalHostIP),
					Port:     uint32(requestedPort),
					Networks: []net.Network{net.Network_TCP},
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				Tag: "outer",
				ProxySettings: serial.ToTypedMessage(&freedom.Config{}),
				SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{
					ProxySettings: &internet.ProxyConfig{Tag: "proxy-balancer"},
				}),
			},
			newVMessOutbound("proxy-a", serverAPort, serverUserID),
			newVMessOutbound("proxy-b", serverBPort, serverUserID),
		},
	}

	servers, err := InitializeServerConfigs(serverA, serverB, client)
	common.Must(err)
	defer CloseAllServers(servers)

	cmdConn, statusClient := mustConnectObservatoryClient(t, cmdPort)
	defer cmdConn.Close()

	waitForObservedAlive(t, statusClient, map[string]bool{
		"proxy-a": true,
		"proxy-b": true,
	})

	if err := testTCPConn(clientPort, 1024, 5*time.Second)(); err != nil {
		t.Fatal(err)
	}

	waitForObservedAlive(t, statusClient, map[string]bool{
		"proxy-a": false,
		"proxy-b": true,
	})

	if err := testTCPConn(clientPort, 1024, 5*time.Second)(); err != nil {
		t.Fatal(err)
	}
}

func newBurstProxyServerConfig(listenPort net.Port, userID *protocol.ID, requestedPort net.Port, override *net.Destination) *core.Config {
	config := &core.Config{
		Inbound: []*core.InboundHandlerConfig{
			{
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(listenPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
				ProxySettings: serial.ToTypedMessage(&vmessinbound.Config{
					User: []*protocol.User{
						{
							Account: serial.ToTypedMessage(&vmess.Account{Id: userID.String()}),
						},
					},
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				Tag:           "direct",
				ProxySettings: serial.ToTypedMessage(&freedom.Config{}),
			},
		},
	}

	if override == nil {
		return config
	}

	config.App = []*serial.TypedMessage{
		serial.ToTypedMessage(&router.Config{
			Rule: []*router.RoutingRule{
				{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(requestedPort)}},
					TargetTag: &router.RoutingRule_Tag{
						Tag: "echo",
					},
				},
			},
		}),
	}
	config.Outbound = append(config.Outbound, &core.OutboundHandlerConfig{
		Tag: "echo",
		ProxySettings: serial.ToTypedMessage(&freedom.Config{
			DestinationOverride: &freedom.DestinationOverride{
				Server: &protocol.ServerEndpoint{
					Address: net.NewIPOrDomain(override.Address),
					Port:    uint32(override.Port),
				},
			},
		}),
	})
	return config
}

func newVMessOutbound(tag string, port net.Port, userID *protocol.ID) *core.OutboundHandlerConfig {
	return &core.OutboundHandlerConfig{
		Tag: tag,
		ProxySettings: serial.ToTypedMessage(&vmessoutbound.Config{
			Receiver: &protocol.ServerEndpoint{
				Address: net.NewIPOrDomain(net.LocalHostIP),
				Port:    uint32(port),
				User: &protocol.User{
					Account: serial.ToTypedMessage(&vmess.Account{Id: userID.String()}),
				},
			},
		}),
	}
}

func newObservatoryCommanderConfig(cmdPort net.Port) *commander.Config {
	return &commander.Config{
		Tag:    "api",
		Listen: fmt.Sprintf("127.0.0.1:%d", cmdPort),
		Service: []*serial.TypedMessage{
			serial.ToTypedMessage(&observatoryservice.Config{}),
		},
	}
}

func newBurstObservatoryConfig(selector string, probePort net.Port) *burst.Config {
	return &burst.Config{
		SubjectSelector: []string{selector},
		PingConfig: &burst.HealthPingConfig{
			Destination:   fmt.Sprintf("http://127.0.0.1:%d/probe", probePort),
			Interval:      int64(2 * time.Second),
			SamplingCount: 1,
			Timeout:       int64(2 * time.Second),
			HttpMethod:    http.MethodGet,
		},
	}
}

func mustConnectObservatoryClient(t *testing.T, cmdPort net.Port) (*grpc.ClientConn, observatoryservice.ObservatoryServiceClient) {
	t.Helper()

	conn, err := grpc.Dial(
		fmt.Sprintf("127.0.0.1:%d", cmdPort),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return conn, observatoryservice.NewObservatoryServiceClient(conn)
}

func waitForObservedAlive(t *testing.T, client observatoryservice.ObservatoryServiceClient, expected map[string]bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		statusMap, err := getObservedStatusMap(client)
		if err == nil {
			matched := true
			for tag, alive := range expected {
				status, found := statusMap[tag]
				if !found || status.Alive != alive {
					matched = false
					break
				}
			}
			if matched {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	statusMap, err := getObservedStatusMap(client)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("timed out waiting for observatory states %v, got %+v", expected, statusMap)
}

func getObservedStatusMap(client observatoryservice.ObservatoryServiceClient) (map[string]*observatory.OutboundStatus, error) {
	resp, err := client.GetOutboundStatus(context.Background(), &observatoryservice.GetOutboundStatusRequest{})
	if err != nil {
		return nil, err
	}
	statusMap := make(map[string]*observatory.OutboundStatus, len(resp.GetStatus().GetStatus()))
	for _, status := range resp.GetStatus().GetStatus() {
		statusMap[status.OutboundTag] = status
	}
	return statusMap, nil
}
