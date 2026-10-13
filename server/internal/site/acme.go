package site

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
)

// The website's certificate for a domain (--web-domain): Let's Encrypt by ACME's TLS-ALPN-01 challenge (RFC 8737),
// so the router forwards only TCP 443. The CA connects to <domain>:443 with the ALPN protocol "acme-tls/1" and the
// server answers that handshake with a challenge certificate (GetCertificate below); every other handshake gets the
// domain's certificate.
//
// This is golang.org/x/crypto/acme (the ACME client of the pinned golang.org/x/crypto, ADR-4) with a small manager of
// our own instead of x/crypto's acme/autocert: autocert imports golang.org/x/net/idna (a module ADR-4 doesn't name,
// for internationalized domain names, which sitelinks' domain rule doesn't allow anyway). The manager does what
// autocert does for one name: an account key and the certificate cached in a folder (the site folder in
// Dawngate-Data), the order with only the tls-alpn-01 challenge, a fresh ECDSA key per certificate, renewal 30 days
// before the end, and retries with a growing pause (Let's Encrypt allows 5 failed validations per hour).

// LetsEncryptURL is Let's Encrypt's production directory (acme.LetsEncryptURL); LetsEncryptStagingURL its staging one
// (untrusted certificates, higher limits: for a first try of a new setup).
const (
	LetsEncryptURL        = acme.LetsEncryptURL
	LetsEncryptStagingURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// ACME obtains and renews the certificate for Domain.
type ACME struct {
	Domain       string // lower case, ASCII (sitelinks.CheckDomain)
	Email        string // contact for the CA's expiry mail ("" = none)
	Dir          string // the cache folder: ACMEAccountFile and <Domain>.pem
	DirectoryURL string // "" = LetsEncryptURL
	Log          func(format string, a ...any)
	Now          func() time.Time
	HTTPClient   *http.Client // the CA's HTTP client (nil = http.DefaultClient; tests)
	// RenewBefore is how long before its end the certificate is renewed (0 = 30 days); Retry the first pause after a
	// failure (0 = 2 min; doubled per failure up to 1 h).
	RenewBefore, Retry time.Duration

	mu     sync.Mutex
	cert   *tls.Certificate
	chal   map[string]*tls.Certificate
	client *acme.Client
	stop   chan struct{}
	done   chan struct{}
}

// ACMEAccountFile is the ACME account key in the cache folder.
const ACMEAccountFile = "acme_account.pem"

func (m *ACME) logf(format string, a ...any) {
	if m.Log != nil {
		m.Log(format, a...)
	}
}

func (m *ACME) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *ACME) certFile() string { return filepath.Join(m.Dir, m.Domain+".pem") }

// TLSConfig is the website's TLS configuration: HTTP/1.1 and the ACME challenge's protocol.
func (m *ACME) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1", acme.ALPNProto},
		GetCertificate: m.GetCertificate}
}

// GetCertificate answers a handshake: the challenge certificate for an "acme-tls/1" handshake of a pending
// validation, the domain's certificate for every other one that names the domain (or no name).
func (m *ACME) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.TrimSuffix(strings.ToLower(hello.ServerName), ".")
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(hello.SupportedProtos) == 1 && hello.SupportedProtos[0] == acme.ALPNProto {
		if c := m.chal[name]; c != nil {
			return c, nil
		}
		return nil, fmt.Errorf("site: no ACME challenge pending for %q", hello.ServerName)
	}
	if name != "" && name != m.Domain {
		return nil, fmt.Errorf("site: no certificate for %q (the site is %s)", hello.ServerName, m.Domain)
	}
	if m.cert == nil {
		return nil, fmt.Errorf("site: the certificate for %s isn't there yet (asked from the CA)", m.Domain)
	}
	return m.cert, nil
}

// Load reads the cached certificate; it reports whether one for the domain was there and still valid.
func (m *ACME) Load() bool {
	c, err := tls.LoadX509KeyPair(m.certFile(), m.certFile())
	if err != nil || c.Leaf == nil || c.Leaf.VerifyHostname(m.Domain) != nil || !m.now().Before(c.Leaf.NotAfter) {
		return false
	}
	m.mu.Lock()
	m.cert = &c
	m.mu.Unlock()
	return true
}

// NotAfter is the current certificate's end (zero without one).
func (m *ACME) NotAfter() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cert == nil || m.cert.Leaf == nil {
		return time.Time{}
	}
	return m.cert.Leaf.NotAfter
}

// Start loads the cache and runs the loop that obtains the certificate when it is missing and renews it before its
// end, until Close.
func (m *ACME) Start() {
	m.Load()
	m.stop, m.done = make(chan struct{}), make(chan struct{})
	go m.loop()
}

// Close ends the loop (an order in progress is cancelled).
func (m *ACME) Close() {
	if m.stop == nil {
		return
	}
	close(m.stop)
	<-m.done
	m.stop = nil
}

func (m *ACME) loop() {
	defer close(m.done)
	retry := m.Retry
	if retry <= 0 {
		retry = 2 * time.Minute
	}
	renew := m.RenewBefore
	if renew <= 0 {
		renew = 30 * 24 * time.Hour
	}
	pause := retry
	for {
		wait := 12 * time.Hour
		end := m.NotAfter()
		if end.IsZero() || end.Sub(m.now()) < renew {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			go func() {
				select {
				case <-m.stop:
					cancel()
				case <-ctx.Done():
				}
			}()
			err := m.Obtain(ctx)
			cancel()
			if err != nil {
				m.logf("certificate for %s: %v; next try in %s", m.Domain, err, pause)
				wait, pause = pause, min(pause*2, time.Hour)
			} else {
				m.logf("certificate for %s from the CA, valid until %s (cached in %s)", m.Domain,
					m.NotAfter().UTC().Format("2006-01-02 15:04 UTC"), m.certFile())
				pause = retry
			}
		}
		select {
		case <-m.stop:
			return
		case <-time.After(wait):
		}
	}
}

// accountKey returns the ACME account key, from the cache or new (and cached).
func (m *ACME) accountKey() (crypto.Signer, error) {
	file := filepath.Join(m.Dir, ACMEAccountFile)
	if b, err := os.ReadFile(file); err == nil {
		if blk, _ := pem.Decode(b); blk != nil && blk.Type == "EC PRIVATE KEY" {
			return x509.ParseECPrivateKey(blk.Bytes)
		}
		return nil, fmt.Errorf("%s holds no EC private key", file)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return key, writePrivate(file, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
}

// acmeClient returns the ACME client with the account registered.
func (m *ACME) acmeClient(ctx context.Context) (*acme.Client, error) {
	m.mu.Lock()
	c := m.client
	m.mu.Unlock()
	if c != nil {
		return c, nil
	}
	key, err := m.accountKey()
	if err != nil {
		return nil, err
	}
	url := m.DirectoryURL
	if url == "" {
		url = LetsEncryptURL
	}
	c = &acme.Client{Key: key, DirectoryURL: url, HTTPClient: m.HTTPClient, UserAgent: "dawngate-server"}
	var contact []string
	if m.Email != "" {
		contact = []string{"mailto:" + m.Email}
	}
	if _, err := c.Register(ctx, &acme.Account{Contact: contact}, acme.AcceptTOS); err != nil &&
		!errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, fmt.Errorf("ACME account: %w", err)
	}
	m.mu.Lock()
	m.client = c
	m.mu.Unlock()
	return c, nil
}

// Obtain orders a certificate for the domain now (the loop calls it; tests too), proves the domain by TLS-ALPN-01
// and installs and caches the certificate.
func (m *ACME) Obtain(ctx context.Context) error {
	c, err := m.acmeClient(ctx)
	if err != nil {
		return err
	}
	o, err := c.AuthorizeOrder(ctx, acme.DomainIDs(m.Domain))
	if err != nil {
		return fmt.Errorf("order: %w", err)
	}
	var pending []string
	defer func() { // a failed order leaves no authorization pending (autocert does the same)
		for _, u := range pending {
			c.RevokeAuthorization(context.Background(), u)
		}
	}()
	for _, u := range o.AuthzURLs {
		z, err := c.GetAuthorization(ctx, u)
		if err != nil {
			return fmt.Errorf("authorization: %w", err)
		}
		if z.Status == acme.StatusValid {
			continue
		}
		if z.Status != acme.StatusPending {
			return fmt.Errorf("authorization for %s is %s", z.Identifier.Value, z.Status)
		}
		pending = append(pending, z.URI)
		i := slices.IndexFunc(z.Challenges, func(ch *acme.Challenge) bool { return ch.Type == "tls-alpn-01" })
		if i < 0 {
			return fmt.Errorf("the CA offers no tls-alpn-01 challenge for %s", z.Identifier.Value)
		}
		ch := z.Challenges[i]
		cert, err := c.TLSALPN01ChallengeCert(ch.Token, z.Identifier.Value)
		if err != nil {
			return err
		}
		m.setChallenge(z.Identifier.Value, &cert)
		_, err = c.Accept(ctx, ch)
		if err == nil {
			_, err = c.WaitAuthorization(ctx, z.URI)
		}
		m.setChallenge(z.Identifier.Value, nil)
		if err != nil {
			return fmt.Errorf("validation of %s (TLS-ALPN-01 on port 443): %w", z.Identifier.Value, err)
		}
		pending = pending[:len(pending)-1]
	}
	if o, err = c.WaitOrder(ctx, o.URI); err != nil {
		return fmt.Errorf("order: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: m.Domain}, DNSNames: []string{m.Domain}}, key)
	if err != nil {
		return err
	}
	der, _, err := c.CreateOrderCert(ctx, o.FinalizeURL, csr, true)
	if err != nil {
		return fmt.Errorf("certificate: %w", err)
	}
	cert, err := m.check(der, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	for _, b := range der {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: b})...)
	}
	if err := writePrivate(m.certFile(), out); err != nil {
		return err
	}
	m.mu.Lock()
	m.cert = cert
	m.mu.Unlock()
	return nil
}

// check verifies what the CA sent: a leaf for the domain, valid now, for this key.
func (m *ACME) check(der [][]byte, key *ecdsa.PrivateKey) (*tls.Certificate, error) {
	if len(der) == 0 {
		return nil, errors.New("the CA sent no certificate")
	}
	leaf, err := x509.ParseCertificate(der[0])
	if err != nil {
		return nil, err
	}
	if err := leaf.VerifyHostname(m.Domain); err != nil {
		return nil, fmt.Errorf("the CA's certificate: %w", err)
	}
	if now := m.now(); now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, fmt.Errorf("the CA's certificate is valid %s to %s", leaf.NotBefore, leaf.NotAfter)
	}
	if pub, ok := leaf.PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) {
		return nil, errors.New("the CA's certificate is for another key")
	}
	return &tls.Certificate{Certificate: der, PrivateKey: key, Leaf: leaf}, nil
}

func (m *ACME) setChallenge(name string, c *tls.Certificate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chal == nil {
		m.chal = map[string]*tls.Certificate{}
	}
	if c == nil {
		delete(m.chal, strings.ToLower(name))
	} else {
		m.chal[strings.ToLower(name)] = c
	}
}
