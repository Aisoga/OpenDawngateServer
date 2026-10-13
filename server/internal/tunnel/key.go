package tunnel

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The gateway's key files, next to the redirector's certificates (dgserver --certs; Start-Server's
// Dawngate-Data\certs\<address>). The key is the server's identity for the tunnel: never share it.
const (
	CertFile = "gateway_cert.pem"
	KeyFile  = "gateway_key.pem"
	// ALPN is the protocol name both sides offer; a TLS server that isn't a gateway fails the handshake.
	ALPN = "dawngate-tunnel/1"
)

// Pin is the SHA-256 of a gateway certificate's SubjectPublicKeyInfo: what a client pins.
type Pin [32]byte

// PinOf returns a certificate's pin.
func PinOf(cert *x509.Certificate) Pin { return sha256.Sum256(cert.RawSubjectPublicKeyInfo) }

// Fingerprint is what a player compares: the pin's first 16 hex digits, as the redirector CA's fingerprint.
func (p Pin) Fingerprint() string { return hex.EncodeToString(p[:8]) }

// String is the whole pin in hex (64 digits), as Start-Client stores it.
func (p Pin) String() string { return hex.EncodeToString(p[:]) }

// IsZero says whether no pin is set.
func (p Pin) IsZero() bool { return p == Pin{} }

// ParsePin reads a pin of 64 hex digits (blanks and colons ignored).
func ParsePin(s string) (Pin, error) {
	var p Pin
	s = strings.NewReplacer(" ", "", ":", "", "\r", "", "\n", "").Replace(strings.TrimSpace(s))
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(p) {
		return p, fmt.Errorf("a gateway pin is 64 hex digits (the SHA-256 of its public key), not %q", s)
	}
	copy(p[:], b)
	return p, nil
}

// Identity is the gateway's key pair.
type Identity struct {
	Cert tls.Certificate
	Pin  Pin
}

// LoadIdentity reads the gateway's key pair from dir.
func LoadIdentity(dir string) (*Identity, error) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, CertFile), filepath.Join(dir, KeyFile))
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	cert.Leaf = leaf
	return &Identity{Cert: cert, Pin: PinOf(leaf)}, nil
}

// LoadOrCreateIdentity reads the gateway's key pair from dir, or makes a new one there when neither file exists
// (created true). A half pair is an error: it is never replaced silently, since the players pinned the old key.
func LoadOrCreateIdentity(dir string) (id *Identity, created bool, err error) {
	_, errC := os.Stat(filepath.Join(dir, CertFile))
	_, errK := os.Stat(filepath.Join(dir, KeyFile))
	if errors.Is(errC, os.ErrNotExist) && errors.Is(errK, os.ErrNotExist) {
		if err := WriteIdentity(dir); err != nil {
			return nil, false, err
		}
		created = true
	}
	id, err = LoadIdentity(dir)
	if err != nil {
		return nil, false, fmt.Errorf("the gateway key in %s: %w (delete both %s and %s to make a new one; every player then confirms the new fingerprint)",
			dir, err, CertFile, KeyFile)
	}
	return id, created, nil
}

// WriteIdentity makes a new key pair (ECDSA P-256, a self-signed certificate valid for 100 years: the pin, not a CA,
// makes it trusted) and writes it into dir; the key file is written last, so a crash leaves no half pair that loads.
func WriteIdentity(dir string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return err
	}
	now := time.Now().Add(-time.Hour)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Dawngate tunnel gateway"},
		NotBefore:    now,
		NotAfter:     now.AddDate(100, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, CertFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, KeyFile), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), 0o600)
}

func writeFile(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// PinFromCertFile reads a gateway certificate (PEM) and returns its pin: how a test lane's tunnel client pins the
// key its own server made.
func PinFromCertFile(path string) (Pin, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Pin{}, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return Pin{}, fmt.Errorf("%s: no PEM certificate", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Pin{}, err
	}
	return PinOf(cert), nil
}

// ServerTLS is the gateway's TLS configuration: TLS 1.3 only, the gateway's key, ALPN.
func ServerTLS(id *Identity) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{id.Cert},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{ALPN},
	}
}

// ErrPinMismatch: the gateway presented another key than the pinned one.
var ErrPinMismatch = errors.New("the gateway's key is not the pinned one")

// ClientTLS is a tunnel client's TLS configuration: TLS 1.3 only, and the gateway's certificate accepted only when
// its public key has the pin (no CA, no name: the pin is the trust). A session cache lets the many short streams
// resume (TLS 1.3 PSK), which saves the key exchange's computation, not a round trip.
func ClientTLS(pin Pin, cache tls.ClientSessionCache) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		NextProtos:         []string{ALPN},
		ServerName:         "dawngate-gateway",
		InsecureSkipVerify: true, // the certificate is checked by its pin in VerifyConnection
		ClientSessionCache: cache,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return ErrPinMismatch
			}
			got := PinOf(cs.PeerCertificates[0])
			if subtle.ConstantTimeCompare(got[:], pin[:]) != 1 {
				return fmt.Errorf("%w (it presents %s)", ErrPinMismatch, got.Fingerprint())
			}
			if cs.NegotiatedProtocol != ALPN {
				return fmt.Errorf("the server at the gateway port speaks no Dawngate tunnel (ALPN %q)", cs.NegotiatedProtocol)
			}
			return nil
		},
	}
}

// ProbePin connects to a gateway without a pin and returns the key it presents: Start-Client's first contact (trust
// on first use; the player compares Fingerprint with the operator's). dialer may be nil.
func ProbePin(addr string, timeout time.Duration, dialer *net.Dialer) (Pin, error) {
	var got Pin
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		NextProtos:         []string{ALPN},
		ServerName:         "dawngate-gateway",
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("the gateway presented no certificate")
			}
			if cs.NegotiatedProtocol != ALPN {
				return fmt.Errorf("the server at the gateway port speaks no Dawngate tunnel (ALPN %q)", cs.NegotiatedProtocol)
			}
			got = PinOf(cs.PeerCertificates[0])
			return nil
		},
	}
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	d := *dialer
	d.Timeout = timeout
	c, err := tls.DialWithDialer(&d, "tcp4", addr, cfg)
	if err != nil {
		return got, err
	}
	c.Close()
	return got, nil
}

// SamePin compares two pins in constant time.
func SamePin(a, b Pin) bool { return subtle.ConstantTimeCompare(a[:], b[:]) == 1 }
