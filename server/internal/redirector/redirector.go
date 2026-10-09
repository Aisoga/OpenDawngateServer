// Package redirector is the Blaze redirector: TLS on port 42127, answering
// Redirector::getServerInstance (component 5, command 1) with our own Blaze address.
//
// The patched client (re/scripts/patch_client.py) looks up gosredirector.ea.com as 127.0.0.1 and trusts
// our CA. ProtoSSL (DirtySDK 8.18) negotiates at most TLS 1.1, offers only RSA key exchange
// (AES128/256-SHA, RC4-SHA, RC4-MD5) and sends no SNI; the certificate is RSA-1024 with e = 3,
// MD5-signed by our stand-in for EA's OTG3 CA (server/tools/make_certs.py). See docs/BLAZE.md and ADR-1.
package redirector

import (
	"crypto/tls"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/tdf"
)

// Port is the redirector port compiled into the client.
const Port = 42127

// Component and command of getServerInstance.
const (
	ComponentRedirector  = 5
	CmdGetServerInstance = 1
)

// TLSConfig loads server_cert.pem and server_key.pem from certsDir. Go 1.22+ leaves TLS 1.0/1.1 and
// RSA key exchange out of its defaults (GODEBUG tls10server, tlsrsakex), so both are set explicitly
// here; explicit settings don't depend on those switches.
func TLSConfig(certsDir string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(certsDir, "server_cert.pem"), filepath.Join(certsDir, "server_key.pem"))
	if err != nil {
		return nil, fmt.Errorf("loading the redirector certificate from %s (make it with server/tools/make_certs.py): %w", certsDir, err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS10,
		MaxVersion:   tls.VersionTLS11,
		// Go picks by its own preference order, which puts AES128 first; the Python stub (server
		// preference) picked AES256-SHA. The client offers both.
		CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_256_CBC_SHA, tls.TLS_RSA_WITH_AES_128_CBC_SHA},
	}, nil
}

// GetServerInstanceReply is the getServerInstance reply: ADDR = union member 0 (an IP address) whose
// VALU holds HOST, IP and PORT of our Blaze server; SECU = 0 makes the client connect in plain TCP;
// XDNS = 0. host must be a dotted IPv4 address.
func GetServerInstanceReply(host string, blazePort int) ([]byte, error) {
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return nil, fmt.Errorf("redirector: %q is not an IPv4 address", host)
	}
	w := tdf.NewWriter()
	w.Union("ADDR", 0, func(w *tdf.Writer) {
		w.Struct("VALU", func(w *tdf.Writer) {
			w.String("HOST", host)
			w.Int("IP", int64(ip[0])<<24|int64(ip[1])<<16|int64(ip[2])<<8|int64(ip[3]))
			w.Int("PORT", int64(blazePort))
		})
	})
	w.Int("SECU", 0)
	w.Int("XDNS", 0)
	return w.Bytes(), nil
}

// Server answers getServerInstance on TLS connections.
type Server struct {
	Log       *logging.Log
	Host      string // our Blaze address as clients reach it (dgserver --public-host)
	BlazePort int
}

// Serve accepts connections from a TLS listener until it is closed.
func (s *Server) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.ServeConn(c)
	}
}

// ServeConn handles one connection. c must be a *tls.Conn.
func (s *Server) ServeConn(c net.Conn) {
	defer c.Close()
	const who = "redirector"
	peer := logging.Peer(c.RemoteAddr())
	if tc, ok := c.(*tls.Conn); ok {
		tc.SetDeadline(time.Now().Add(60 * time.Second)) // asyncio's default TLS handshake timeout
		if err := tc.Handshake(); err != nil {
			s.Log.Logf(who, "TLS handshake with %s failed: %v", peer, err)
			return
		}
		tc.SetDeadline(time.Time{})
		st := tc.ConnectionState()
		s.Log.Logf(who, "connection from %s, %s %s", peer, VersionName(st.Version), CipherName(st.CipherSuite))
	} else {
		s.Log.Logf(who, "connection from %s (no TLS)", peer)
	}
	r := fire.NewReader(c)
	for {
		f, err := r.Next()
		if err != nil {
			fire.LogEnd(s.Log, who, r, err)
			break
		}
		fire.Describe(s.Log, who, f)
		if f.Component != ComponentRedirector || f.Command != CmdGetServerInstance {
			continue
		}
		body, err := GetServerInstanceReply(s.Host, s.BlazePort)
		if err == nil {
			var out []byte
			out, err = fire.Encode(fire.Frame{Component: f.Component, Command: f.Command, Type: fire.Reply, ID: f.ID, Payload: body})
			if err == nil {
				_, err = c.Write(out)
			}
		}
		if err != nil {
			s.Log.Logf(who, "getServerInstance reply failed: %v", err)
			break
		}
		s.Log.Logf(who, "-> getServerInstance reply: %s:%d, SECU=0", s.Host, s.BlazePort)
	}
	s.Log.Log(who, "connection closed")
}

// VersionName names a TLS version as Python's ssl module does ("TLSv1.1").
func VersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLSv1"
	case tls.VersionTLS11:
		return "TLSv1.1"
	case tls.VersionTLS12:
		return "TLSv1.2"
	case tls.VersionTLS13:
		return "TLSv1.3"
	}
	return fmt.Sprintf("TLS 0x%04x", v)
}

// CipherName names a cipher suite as OpenSSL does ("AES256-SHA").
func CipherName(id uint16) string {
	switch id {
	case tls.TLS_RSA_WITH_AES_256_CBC_SHA:
		return "AES256-SHA"
	case tls.TLS_RSA_WITH_AES_128_CBC_SHA:
		return "AES128-SHA"
	}
	return tls.CipherSuiteName(id)
}
