package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"
)

// The agent link (REGION1): a host machine's agent dials the main server's gateway like a player's Start-Client
// (TLS 1.3, ALPN, the main server's key pinned) and says the hello head "DGTN", 1, kindAgent. Nothing else of the
// tunnel's wire follows: the gateway hands the connection to Gateway.Agents, which speaks the host manager's JSON
// lines on it (gmshost.Manager.ServeAgent; the agent's side gmshost.Agent). The registration's proofs are bound to
// this TLS session by AgentExporter, so a proof can't be used on another connection.

// kindAgent is the agent link's hello kind.
const kindAgent byte = 4

// AgentExporterLabel is the TLS exporter label (RFC 8446 §7.5) of the agent link's binding value.
const AgentExporterLabel = "EXPORTER-dawngate-agent-v1"

// AgentExporter is a TLS connection's binding value for the agent link's proofs: 32 bytes both ends of this TLS
// session derive alike, and nobody else can.
func AgentExporter(c *tls.Conn) ([]byte, error) {
	cs := c.ConnectionState()
	if !cs.HandshakeComplete {
		return nil, errors.New("tunnel: the TLS handshake isn't done")
	}
	return cs.ExportKeyingMaterial(AgentExporterLabel, nil, 32)
}

// agentHello is the agent link's hello head.
func agentHello() []byte { return append([]byte(magic), version, kindAgent) }

// DialAgent opens an agent link to a gateway at addr ("host:port"): TCP, the TLS handshake with pin checked
// (ClientTLS; a wrong key fails with ErrPinMismatch), then the hello head. The caller owns the connection; timeout
// limits the dial and the handshake.
func DialAgent(ctx context.Context, addr string, pin Pin, timeout time.Duration) (*tls.Conn, error) {
	if pin.IsZero() {
		return nil, errors.New("tunnel: an agent link needs the main server's pin")
	}
	d := net.Dialer{Timeout: timeout, KeepAlive: 15 * time.Second}
	raw, err := d.DialContext(ctx, "tcp4", addr)
	if err != nil {
		return nil, err
	}
	c := tls.Client(raw, ClientTLS(pin, nil))
	c.SetDeadline(time.Now().Add(timeout))
	if err := c.HandshakeContext(ctx); err != nil {
		c.Close()
		return nil, err
	}
	if _, err := c.Write(agentHello()); err != nil {
		c.Close()
		return nil, fmt.Errorf("tunnel: agent hello: %w", err)
	}
	c.SetDeadline(time.Time{})
	return c, nil
}

// serveAgent hands an agent link to Gateway.Agents (nil: this gateway takes no agents, closed). The link ends with
// the gateway too.
func (g *Gateway) serveAgent(c *tls.Conn, peer string) {
	if g.Agents == nil {
		g.logf("%s: agent link refused: this server takes no host machines", peer)
		c.Close()
		return
	}
	c.SetDeadline(time.Time{})
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-g.done:
			c.Close()
		case <-stop:
		}
	}()
	g.Agents(c, peer)
}
