package tunnel

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"dawngate/server/internal/logging"
)

func portsClient(g *Gateway, allow []int) *Client {
	return &Client{Log: logging.New(io.Discard), Gateway: g.Addr(), Pin: g.Identity.Pin, TunnelIP: net.ParseIP(tunIP),
		DialIP: net.ParseIP(peerIP), External: true, Deliver: func(int, *net.UDPAddr, []byte) {}, AllowTCP: allow}
}

// A client told a TCP port outside AllowTCP refuses the server, and a nil AllowTCP takes any.
func TestClientAllowTCP(t *testing.T) {
	g, _ := startGateway(t, io.Discard) // lists portTCP and RedirectorPort
	err := portsClient(g, []int{RedirectorPort}).Start()
	if !errors.Is(err, ErrTCPPort) || !strings.Contains(err.Error(), "23001") {
		t.Errorf("a TCP port outside the list: %v", err)
	}
	for _, allow := range [][]int{{RedirectorPort, portTCP}, nil} {
		c := portsClient(g, allow)
		if err := c.Start(); err != nil {
			t.Errorf("allow %v: %v", allow, err)
			continue
		}
		c.Close()
	}
}

// The rule on UDP ports: a client refuses a server that breaks it, whatever that server's gateway says.
func TestClientRefusesUDPPorts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra []int
		want  string
	}{
		{"a low port", []int{900}, "UDP port 900"},
		{"too many", portRange(2000, MaxUDPPorts-1), "UDP ports"}, // with portQoS and portHost, one too many
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := startGateway(t, io.Discard)
			g.ForwardUDP(tc.extra...) // what a hostile gateway says, past its own Start's check
			c := portsClient(g, nil)
			err := c.Start()
			if err == nil {
				c.Close()
				t.Fatal("the server wasn't refused")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refused with: %v", err)
			}
		})
	}
	t.Run("at the limit", func(t *testing.T) {
		g, _ := startGateway(t, io.Discard)
		g.ForwardUDP(portRange(2000, MaxUDPPorts-2)...)
		c := portsClient(g, nil)
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		c.Close()
	})
}

// A gateway that would be refused doesn't start, so the operator hears it instead of each player.
func TestGatewayRefusesUDPPorts(t *testing.T) {
	for _, ports := range [][]int{{900}, portRange(2000, MaxUDPPorts+1)} {
		id, _, err := LoadOrCreateIdentity(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		g := &Gateway{Log: logging.New(io.Discard), Identity: id}
		g.ForwardUDP(ports...)
		if err := g.Start(net.JoinHostPort(gwIP, "0")); err == nil {
			g.Close()
			t.Errorf("a gateway with %d ports from %d started", len(ports), ports[0])
		} else if !strings.Contains(err.Error(), "--gms-ports") {
			t.Errorf("its error doesn't say what to change: %v", err)
		}
	}
}

func portRange(first, n int) []int {
	ports := make([]int, n)
	for i := range ports {
		ports[i] = first + i
	}
	return ports
}
