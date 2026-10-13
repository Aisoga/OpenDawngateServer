package site

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/acme"
)

// siteFor serves m's TLS configuration on a loopback port (the website's listener) and returns its address.
func siteFor(t *testing.T, m *ACME) string {
	ln, err := tls.Listen("tcp", "127.0.0.1:0", m.TLSConfig())
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "site") })}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func TestACMEObtainCache(t *testing.T) {
	const domain = "play.example.org"
	ca := newFakeCA(t, domain)
	dir := t.TempDir()
	m := &ACME{Domain: domain, Email: "op@example.org", Dir: dir, DirectoryURL: ca.url("/dir"), HTTPClient: ca.srv.Client()}
	ca.site = siteFor(t, m)
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: domain}); err == nil {
		t.Fatal("a certificate before any order")
	}
	if err := m.Obtain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ca.validated != 1 || ca.accounts != 1 {
		t.Fatalf("validated %d, accounts %d", ca.validated, ca.accounts)
	}
	// a browser now gets the CA's certificate for the domain
	pool := x509.NewCertPool()
	pool.AddCert(ca.root)
	conn, err := tls.Dial("tcp", ca.site, &tls.Config{ServerName: domain, RootCAs: pool})
	if err != nil {
		t.Fatalf("a browser's handshake: %v", err)
	}
	conn.Close()
	// another name, or an acme-tls/1 handshake without a pending challenge, gets nothing
	if c, err := tls.Dial("tcp", ca.site, &tls.Config{ServerName: "other.example.org", RootCAs: pool}); err == nil {
		c.Close()
		t.Error("a certificate for another name")
	}
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: domain, SupportedProtos: []string{acme.ALPNProto}}); err == nil {
		t.Error("a challenge certificate with no challenge pending")
	}
	// the cache: the account key and the certificate; a new manager loads it
	for _, f := range []string{ACMEAccountFile, domain + ".pem"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("cache: %v", err)
		}
	}
	m2 := &ACME{Domain: domain, Dir: dir}
	if !m2.Load() || m2.NotAfter().IsZero() {
		t.Fatal("the cached certificate isn't loaded")
	}
	// the account key is reused by the next order
	before, _ := os.ReadFile(filepath.Join(dir, ACMEAccountFile))
	m3 := &ACME{Domain: domain, Dir: dir, DirectoryURL: ca.url("/dir"), HTTPClient: ca.srv.Client()}
	ca.mu.Lock()
	ca.site = siteFor(t, m3)
	ca.authzValid, ca.issued = false, false
	ca.mu.Unlock()
	if err := m3.Obtain(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, ACMEAccountFile))
	if string(before) != string(after) || ca.validated != 2 {
		t.Errorf("the account key changed or no validation (%d)", ca.validated)
	}
	// a cached certificate for another domain isn't taken
	os.Rename(filepath.Join(dir, domain+".pem"), filepath.Join(dir, "else.example.org.pem"))
	if (&ACME{Domain: "else.example.org", Dir: dir}).Load() {
		t.Error("a certificate for another domain loaded")
	}
}

func TestACMEFailureAndLoop(t *testing.T) {
	const domain = "play.example.org"
	ca := newFakeCA(t, domain)
	ca.refuse = true
	var mu sync.Mutex
	var lines []string
	m := &ACME{Domain: domain, Dir: t.TempDir(), DirectoryURL: ca.url("/dir"), HTTPClient: ca.srv.Client(),
		Retry: 20 * time.Millisecond, Log: func(format string, a ...any) {
			mu.Lock()
			lines = append(lines, fmt.Sprintf(format, a...))
			mu.Unlock()
		}}
	ca.site = siteFor(t, m)
	if err := m.Obtain(context.Background()); err == nil || !strings.Contains(err.Error(), "TLS-ALPN-01") {
		t.Fatalf("a refused validation: %v", err)
	}
	// the loop retries with a growing pause and gets the certificate once the validation works
	m.Start()
	defer m.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := len(lines)
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no retries")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ca.mu.Lock()
	ca.refuse = false
	ca.mu.Unlock()
	for m.NotAfter().IsZero() {
		if time.Now().After(deadline) {
			t.Fatalf("no certificate after the retries:\n%s", strings.Join(lines, "\n"))
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	all := strings.Join(lines, "\n")
	if !strings.Contains(all, "next try in 20ms") || !strings.Contains(all, "next try in 40ms") || !strings.Contains(all, "valid until") {
		t.Errorf("log:\n%s", all)
	}
}

func TestACMETLSConfig(t *testing.T) {
	c := (&ACME{Domain: "play.example.org"}).TLSConfig()
	if c.MinVersion != tls.VersionTLS12 || c.GetCertificate == nil || len(c.NextProtos) != 2 || c.NextProtos[1] != acme.ALPNProto {
		t.Errorf("%+v", c)
	}
}

func TestSelfSigned(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	c, fp, fresh, err := SelfSigned(dir, []string{"127.0.0.4", "localhost", "127.0.0.4"}, now)
	if err != nil || !fresh || len(fp) != 95 {
		t.Fatalf("%v %v %q", err, fresh, fp)
	}
	if c.Leaf.VerifyHostname("127.0.0.4") != nil || c.Leaf.VerifyHostname("localhost") != nil {
		t.Errorf("names: %v %v", c.Leaf.IPAddresses, c.Leaf.DNSNames)
	}
	// kept across restarts
	if _, fp2, fresh2, err := SelfSigned(dir, []string{"localhost", "127.0.0.4"}, now.Add(time.Hour)); err != nil || fresh2 || fp2 != fp {
		t.Errorf("not reused: %v %v", err, fresh2)
	}
	// new for another address, and 30 days before its end
	if _, fp3, fresh3, _ := SelfSigned(dir, []string{"192.168.1.20"}, now); !fresh3 || fp3 == fp {
		t.Error("not renewed for another address")
	}
	if _, _, fresh4, _ := SelfSigned(dir, []string{"192.168.1.20"}, now.Add((selfSignedDays-20)*24*time.Hour)); !fresh4 {
		t.Error("not renewed before its end")
	}
	// a browser that trusts the certificate connects
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{c}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if conn, err := ln.Accept(); err == nil {
			conn.(*tls.Conn).Handshake()
			conn.Close()
		}
	}()
	pool := x509.NewCertPool()
	pool.AddCert(c.Leaf)
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{ServerName: "localhost", RootCAs: pool})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	conn.Close()
	if _, _, _, err := SelfSigned("", nil, now); err == nil {
		t.Error("no hosts accepted")
	}
}
