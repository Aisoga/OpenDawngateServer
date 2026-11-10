package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// REGION1: a host machine's agent link through the gateway (hello kind 4): handed to Gateway.Agents with the TLS
// connection, both ends derive the same exporter value, a wrong pin fails before anything is sent, a gateway
// without the hook closes it, and the link ends with the gateway.
func TestAgentLink(t *testing.T) {
	var log syncBuf
	type seen struct {
		exporter string
		peer     string
	}
	got := make(chan seen, 1)
	g, _ := startGatewayWith(t, &log, func(g *Gateway) {
		g.Agents = func(c *tls.Conn, peer string) {
			exp, err := AgentExporter(c)
			if err != nil {
				t.Errorf("exporter: %v", err)
			}
			got <- seen{hex.EncodeToString(exp), peer}
			r := bufio.NewReader(c)
			for { // an echo of lines until the link ends
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				io.WriteString(c, "echo "+line)
			}
		}
	})
	c, err := DialAgent(context.Background(), g.Addr(), g.Identity.Pin, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exp, err := AgentExporter(c)
	if err != nil {
		t.Fatal(err)
	}
	s := <-got
	if s.exporter != hex.EncodeToString(exp) || len(exp) != 32 {
		t.Errorf("exporter: gateway %s, agent %x", s.exporter, exp)
	}
	if s.peer == "" {
		t.Errorf("peer %q", s.peer)
	}
	io.WriteString(c, "{\"t\":\"agentHello\"}\n")
	r := bufio.NewReader(c)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if line, err := r.ReadString('\n'); err != nil || line != "echo {\"t\":\"agentHello\"}\n" {
		t.Errorf("echo %q, %v", line, err)
	}
	// another TLS session, another exporter value: a proof bound to one is no good on the other
	c2, err := DialAgent(context.Background(), g.Addr(), g.Identity.Pin, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	exp2, _ := AgentExporter(c2)
	if s2 := <-got; bytes.Equal(exp, exp2) || s2.exporter != hex.EncodeToString(exp2) {
		t.Errorf("two sessions' exporters %x %x", exp, exp2)
	}
	// the link ends with the gateway
	g.Close()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := r.ReadString('\n'); err == nil {
		t.Error("the link outlived the gateway")
	}
}

func TestAgentLinkRefused(t *testing.T) {
	var log syncBuf
	g, _ := startGatewayWith(t, &log, nil) // no Agents hook
	c, err := DialAgent(context.Background(), g.Addr(), g.Identity.Pin, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := c.Read(make([]byte, 1)); err == nil {
		t.Errorf("read %d bytes from a gateway that takes no agents", n)
	}
	for deadline := time.Now().Add(2 * time.Second); !strings.Contains(log.String(), "agent link refused: this server takes no host machines"); {
		if time.Now().After(deadline) {
			t.Fatalf("log:\n%s", log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// a wrong pin: the handshake fails, nothing is sent; no pin at all: not even dialled
	other := g.Identity.Pin
	other[0] ^= 1
	if _, err := DialAgent(context.Background(), g.Addr(), other, 2*time.Second); !errors.Is(err, ErrPinMismatch) {
		t.Errorf("wrong pin: %v", err)
	}
	if _, err := DialAgent(context.Background(), g.Addr(), Pin{}, 2*time.Second); err == nil {
		t.Error("no pin: dialled")
	}
}
