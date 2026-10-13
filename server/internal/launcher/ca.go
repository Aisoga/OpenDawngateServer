package launcher

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"dawngate/server/internal/setup/certs"
	"dawngate/server/internal/web"
)

// The server's ports Start-Client talks to (dgserver's defaults; the game's config packages name the HTTP port).
const (
	RedirectorPort = 42127
	HTTPPort       = 18080
)

// Answers reports whether something accepts TCP connections at address:port within timeout.
func Answers(address string, port int, timeout time.Duration) error {
	c, err := net.DialTimeout("tcp4", net.JoinHostPort(address, strconv.Itoa(port)), timeout)
	if err != nil {
		return err
	}
	return c.Close()
}

// FetchCA gets the CA modulus a server serves (dgserver --serve-ca, Start-Server.exe) from
// http://<address>:<port>/dawngate/ca_modulus.bin.
func FetchCA(address string, port int, timeout time.Duration) ([]byte, error) {
	cl := &http.Client{Timeout: timeout}
	url := "http://" + net.JoinHostPort(address, strconv.Itoa(port)) + "/" + web.CAModulusPath
	resp, err := cl.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s (the server doesn't serve its CA: is it Start-Server.exe, or dgserver without --serve-ca?)", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, err
	}
	if len(b) != 128 {
		return nil, fmt.Errorf("%s: %d bytes, not a CA modulus (128)", url, len(b))
	}
	return b, nil
}

// CheckServerCert connects to the redirector at address:port as the client does (TLS 1.1, RSA key exchange) and
// checks that its certificate is signed by the CA with this modulus and made for address (the CN the client checks).
func CheckServerCert(address string, port int, modulus []byte, timeout time.Duration) error {
	cert, err := ServerCert(address, port, timeout)
	if err != nil {
		return err
	}
	return certs.VerifyServerCert(cert, modulus, address)
}

// ServerCert connects to the redirector at address:port as the client does (TLS 1.1, RSA key exchange; one complete
// handshake, so the server logs an ordinary connection) and returns its certificate; an error when nothing answers
// there or it speaks no such TLS.
func ServerCert(address string, port int, timeout time.Duration) (*x509.Certificate, error) {
	d := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(d, "tcp4", net.JoinHostPort(address, strconv.Itoa(port)), &tls.Config{
		InsecureSkipVerify: true, // MD5 and e = 3: checked by hand (certs.VerifyServerCert), as the client's ProtoSSL does
		MinVersion:         tls.VersionTLS10,
		MaxVersion:         tls.VersionTLS11,
		CipherSuites:       []uint16{tls.TLS_RSA_WITH_AES_128_CBC_SHA, tls.TLS_RSA_WITH_AES_256_CBC_SHA},
	})
	if err != nil {
		return nil, fmt.Errorf("the redirector at %s:%d: %v", address, port, err)
	}
	defer conn.Close()
	peers := conn.ConnectionState().PeerCertificates
	if len(peers) == 0 {
		return nil, errors.New("the redirector sent no certificate")
	}
	return peers[0], nil
}

// Trust is the outcome of TrustCA.
type Trust int

// TrustCA's outcomes.
const (
	TrustKnown   Trust = iota // the CA is the one trusted before
	TrustNew                  // first contact: the player confirmed the fingerprint (or Accept was set)
	TrustChanged              // the server's CA changed since; the player confirmed the new one
)

// TrustOptions are TrustCA's.
type TrustOptions struct {
	Store  string                              // the trusted CA's file for this server (setup.Layout.TrustedCA)
	Accept bool                                // take a new or changed CA without asking (a test aid, Start-Client --trust)
	Ask    func(question string) (bool, error) // asks the player yes/no; nil: never asks (refuses instead)
}

// TrustCA decides whether to trust the CA modulus a server served: the one stored before is trusted; a first or a
// changed one only when the player confirms its fingerprint (trust on first use: the operator's Start-Server window
// shows the fingerprint), then it is stored.
func TrustCA(server string, served []byte, o TrustOptions) (Trust, error) {
	return trustOnFirstUse(server, served, certs.Fingerprint, firstUse{
		noun: "certificate fingerprint", changed: "another certificate authority", short: "CA"}, o)
}

// firstUse words trustOnFirstUse's questions and errors for one kind of key.
type firstUse struct{ noun, changed, short string }

// trustOnFirstUse is TrustCA's rule for any served key (the CA modulus; the tunnel gateway's key pin): the
// one stored before is trusted; a first or a changed one only when the player confirms its fingerprint.
func trustOnFirstUse(server string, served []byte, fingerprint func([]byte) string, w firstUse, o TrustOptions) (Trust, error) {
	fp := fingerprint(served)
	known, err := os.ReadFile(o.Store)
	switch {
	case err == nil && bytes.Equal(known, served):
		return TrustKnown, nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return 0, err
	}
	changed := err == nil
	question := fmt.Sprintf("First connection to the server %s. Its %s is\n\n      %s\n\n"+
		"  Ask the server's operator: their Start-Server window shows its fingerprint. Only if it is the same,\n"+
		"  answer y. Same fingerprint? [y/N]: ", server, w.noun, fp)
	if changed {
		question = fmt.Sprintf("WARNING: the server %s now has %s than the last time\n"+
			"  (was %s, now %s). That happens when the operator set the server up anew; it could also be\n"+
			"  someone else answering at that address. Ask the operator which fingerprint their Start-Server\n"+
			"  window shows. Is it %s? [y/N]: ", server, w.changed, fingerprint(known), fp, fp)
	}
	ok := o.Accept
	if !ok {
		if o.Ask == nil {
			if changed {
				return 0, fmt.Errorf("the server %s has another %s (%s) than the one trusted before (%s); start Start-Client.exe in a window to confirm it", server, w.short, fp, fingerprint(known))
			}
			return 0, fmt.Errorf("the server %s's %s (fingerprint %s) isn't trusted yet; start Start-Client.exe in a window to confirm it", server, w.short, fp)
		}
		if ok, err = o.Ask(question); err != nil {
			return 0, err
		}
	}
	if !ok {
		return 0, fmt.Errorf("the %s of %s (fingerprint %s) was not confirmed; nothing was changed", w.short, server, fp)
	}
	if err := os.MkdirAll(filepath.Dir(o.Store), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(o.Store, served, 0o644); err != nil {
		return 0, err
	}
	if changed {
		return TrustChanged, nil
	}
	return TrustNew, nil
}
