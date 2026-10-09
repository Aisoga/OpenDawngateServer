package certs

import (
	"bytes"
	"crypto"
	"crypto/md5"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/redirector"
	"dawngate/server/internal/setup/setuptest"
)

func makeSet(t *testing.T, cn string) (string, *Set) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "certs")
	s, err := Make(dir, cn)
	if err != nil {
		t.Fatal(err)
	}
	return dir, s
}

func TestGenerate(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	s, err := Generate("192.168.1.20", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := s.Write(dir); err != nil {
		t.Fatal(err)
	}
	info, err := Check(dir, "192.168.1.20")
	if err != nil {
		t.Fatal(err)
	}
	if info.CN != "192.168.1.20" || len(info.CAModulusSHA256) != 16 {
		t.Errorf("info %+v", info)
	}
	ca, _ := parseCert(s.CACert)
	srv, _ := parseCert(s.ServerCert)
	for _, c := range []*x509.Certificate{ca, srv} {
		pub := c.PublicKey.(*rsa.PublicKey)
		if c.Version != 3 || c.SignatureAlgorithm != x509.MD5WithRSA || pub.E != 3 || pub.N.BitLen() != 1024 {
			t.Errorf("%s: v%d %v e=%d %d bits", c.Subject, c.Version, c.SignatureAlgorithm, pub.E, pub.N.BitLen())
		}
	}
	if !ca.NotBefore.Equal(now) || !ca.NotAfter.Equal(now.AddDate(0, 0, 7300)) || ca.SerialNumber.BitLen() > 159 ||
		ca.SerialNumber.Sign() <= 0 {
		t.Errorf("CA validity %v..%v serial %x", ca.NotBefore, ca.NotAfter, ca.SerialNumber)
	}
	if !srv.NotBefore.Equal(ServerNotBefore) || !srv.NotAfter.Equal(ServerNotAfter) || srv.SerialNumber.Int64() != 0x3C2 {
		t.Errorf("server validity %v..%v serial %x", srv.NotBefore, srv.NotAfter, srv.SerialNumber)
	}
	if !ca.IsCA || !ca.BasicConstraintsValid || srv.IsCA || !srv.BasicConstraintsValid {
		t.Errorf("basic constraints: CA %v/%v server %v/%v", ca.IsCA, ca.BasicConstraintsValid, srv.IsCA, srv.BasicConstraintsValid)
	}
	if !bytes.Equal(srv.AuthorityKeyId, ca.SubjectKeyId) || len(ca.SubjectKeyId) != 20 || len(srv.SubjectKeyId) != 20 {
		t.Errorf("key ids: AKID %x, CA SKID %x, server SKID %x", srv.AuthorityKeyId, ca.SubjectKeyId, srv.SubjectKeyId)
	}
	if ca.Subject.CommonName != "OTG3 Certificate Authority" || srv.Subject.CommonName != "192.168.1.20" ||
		srv.Subject.OrganizationalUnit[0] != "Global Online Studio" || ca.Subject.Locality[0] != "Redwood City" {
		t.Errorf("subjects %s / %s", ca.Subject, srv.Subject)
	}
	if !bytes.Equal(ca.RawIssuer, ca.RawSubject) || !bytes.Equal(srv.RawIssuer, ca.RawSubject) {
		t.Error("issuers")
	}
	if !bytes.Equal(ca.PublicKey.(*rsa.PublicKey).N.FillBytes(make([]byte, 128)), s.CAModulus) {
		t.Error("ca_modulus.bin is not the CA's modulus")
	}
	// the signatures, checked by the standard library too (PKCS#1 v1.5 with MD5)
	for _, c := range []*x509.Certificate{ca, srv} {
		h := md5.Sum(c.RawTBSCertificate)
		if err := rsa.VerifyPKCS1v15(ca.PublicKey.(*rsa.PublicKey), crypto.MD5, h[:], c.Signature); err != nil {
			t.Errorf("%s: %v", c.Subject.CommonName, err)
		}
	}
	// the keys load as PKCS#1 ("RSA PRIVATE KEY") and fit their certificates
	for key, cert := range map[string]string{CAKeyFile: CACertFile, ServerKeyFile: ServerCertFile} {
		if !strings.HasPrefix(string(pemOf(t, dir, key)), "-----BEGIN RSA PRIVATE KEY-----\n") {
			t.Errorf("%s: not a PKCS#1 PEM", key)
		}
		if _, err := tls.X509KeyPair(pemOf(t, dir, cert), pemOf(t, dir, key)); err != nil {
			t.Errorf("%s: %v", key, err)
		}
	}
}

// DG_CERTS_OUT=<dir> [DG_CERTS_CN=<cn>] writes a set there for a look with openssl.
func TestWriteSetForInspection(t *testing.T) {
	out := os.Getenv("DG_CERTS_OUT")
	if out == "" {
		t.Skip("set DG_CERTS_OUT=<dir> to write a set")
	}
	cn := os.Getenv("DG_CERTS_CN")
	if cn == "" {
		cn = "127.0.0.1"
	}
	if _, err := Make(out, cn); err != nil {
		t.Fatal(err)
	}
}

func pemOf(t *testing.T, dir, name string) []byte {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEveryInstallGetsItsOwnKeys(t *testing.T) {
	a, err := Generate("127.0.0.1", nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate("127.0.0.1", nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.CAModulus, b.CAModulus) || bytes.Equal(a.ServerKey, b.ServerKey) {
		t.Error("two sets share a key")
	}
}

// dgserver's redirector loads the set (its TLSConfig) and a TLS 1.1 client with RSA key exchange, as ProtoSSL
// speaks it, completes the handshake and gets our certificate.
func TestRedirectorLoadsTheSet(t *testing.T) {
	dir, s := makeSet(t, "127.0.0.1")
	cfg, err := redirector.TLSConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		_, err = io.Copy(c, io.LimitReader(c, 4))
		done <- err
	}()
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		InsecureSkipVerify: true, // MD5: checked by hand below, as ProtoSSL does against the patched-in CA
		MinVersion:         tls.VersionTLS11,
		MaxVersion:         tls.VersionTLS11,
		CipherSuites:       []uint16{tls.TLS_RSA_WITH_AES_128_CBC_SHA, tls.TLS_RSA_WITH_AES_256_CBC_SHA},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(conn, echo); err != nil || string(echo) != "ping" {
		t.Fatalf("echo %q %v", echo, err)
	}
	st := conn.ConnectionState()
	if st.Version != tls.VersionTLS11 {
		t.Errorf("version %x", st.Version)
	}
	leaf := st.PeerCertificates[0]
	want, _ := parseCert(s.ServerCert)
	if !bytes.Equal(leaf.Raw, want.Raw) {
		t.Error("the server sent another certificate")
	}
	n := new(big.Int).SetBytes(s.CAModulus)
	if err := verifyMD5RSA(n, 3, leaf.RawTBSCertificate, leaf.Signature); err != nil {
		t.Error(err)
	}
	if err := <-done; err != nil {
		t.Error(err)
	}
}

func TestCheckRefusals(t *testing.T) {
	dir, _ := makeSet(t, "127.0.0.1")
	if _, err := Check(dir, "192.168.1.20"); err == nil || !strings.Contains(err.Error(), "is for CN 127.0.0.1") {
		t.Errorf("wrong CN: %v", err)
	}
	other, _ := makeSet(t, "127.0.0.1")
	// a server certificate of another CA
	mixed := t.TempDir()
	for _, name := range Files {
		src := dir
		if name == ServerCertFile || name == ServerKeyFile {
			src = other
		}
		if err := os.WriteFile(filepath.Join(mixed, name), pemOf(t, src, name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Check(mixed, "127.0.0.1"); err == nil {
		t.Error("a server certificate of another CA passed")
	}
	// a modulus of another CA
	if err := os.WriteFile(filepath.Join(mixed, ServerCertFile), pemOf(t, dir, ServerCertFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mixed, ServerKeyFile), pemOf(t, dir, ServerKeyFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(mixed, "127.0.0.1"); err != nil {
		t.Errorf("the set put back together: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mixed, CAModulusFile), pemOf(t, other, CAModulusFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(mixed, "127.0.0.1"); err == nil {
		t.Error("another CA's modulus passed")
	}
	if err := os.Remove(filepath.Join(mixed, CAKeyFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(mixed, ""); !errors.Is(err, ErrNoSet) {
		t.Errorf("a missing file: %v", err)
	}
	for _, cn := range []string{"", "a_b", "名前", strings.Repeat("x", 65)} {
		if _, err := Generate(cn, nil, time.Time{}); err == nil {
			t.Errorf("CN %q accepted", cn)
		}
	}
}

// The reference: a set make_certs.py made (DG_PY_CERTS, else the main checkout's server/certs; read only). Go's
// checks accept it, and its certificates have the structure of Go's field by field: everything but the keys, the
// signatures, the CA's serial and its dates.
func TestStructureMatchesMakeCerts(t *testing.T) {
	ref := os.Getenv("DG_PY_CERTS")
	if ref == "" {
		if p := setuptest.Find("server/certs/ca_cert.pem"); p != "" {
			ref = filepath.Dir(p)
		}
	}
	if ref == "" {
		t.Skip("no make_certs.py set (DG_PY_CERTS or server/certs)")
	}
	info, err := Check(ref, "")
	if err != nil {
		t.Fatalf("Go's check of make_certs.py's set: %v", err)
	}
	s, err := Generate(info.CN, nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ name, goPEM string }{{CACertFile, string(s.CACert)}, {ServerCertFile, string(s.ServerCert)}} {
		py, err := parseCert(pemOf(t, ref, f.name))
		if err != nil {
			t.Fatal(err)
		}
		gc, _ := parseCert([]byte(f.goPEM))
		var a, b tbsCertificate
		if _, err := asn1.Unmarshal(py.RawTBSCertificate, &a); err != nil {
			t.Fatal(err)
		}
		if _, err := asn1.Unmarshal(gc.RawTBSCertificate, &b); err != nil {
			t.Fatal(err)
		}
		if a.Version != b.Version || !a.SignatureAlgorithm.Algorithm.Equal(b.SignatureAlgorithm.Algorithm) ||
			!bytes.Equal(a.SignatureAlgorithm.Parameters.FullBytes, b.SignatureAlgorithm.Parameters.FullBytes) {
			t.Errorf("%s: version or signature algorithm", f.name)
		}
		if !bytes.Equal(a.Issuer.FullBytes, b.Issuer.FullBytes) || !bytes.Equal(a.Subject.FullBytes, b.Subject.FullBytes) {
			t.Errorf("%s: issuer or subject bytes differ:\n py %x %x\n go %x %x", f.name, a.Issuer.FullBytes,
				a.Subject.FullBytes, b.Issuer.FullBytes, b.Subject.FullBytes)
		}
		if !a.PublicKey.Algorithm.Algorithm.Equal(b.PublicKey.Algorithm.Algorithm) || len(a.PublicKey.PublicKey.Bytes) != len(b.PublicKey.PublicKey.Bytes) {
			t.Errorf("%s: public key info", f.name)
		}
		if len(a.Extensions) != len(b.Extensions) {
			t.Fatalf("%s: %d extensions, make_certs.py %d", f.name, len(b.Extensions), len(a.Extensions))
		}
		for i := range a.Extensions {
			x, y := a.Extensions[i], b.Extensions[i]
			if !x.Id.Equal(y.Id) || x.Critical != y.Critical || len(x.Value) != len(y.Value) {
				t.Errorf("%s: extension %d: %v %v %d, make_certs.py %v %v %d", f.name, i, y.Id, y.Critical, len(y.Value), x.Id, x.Critical, len(x.Value))
			}
			if x.Id.Equal(oidBasicConstraint) && !bytes.Equal(x.Value, y.Value) {
				t.Errorf("%s: basicConstraints %x, make_certs.py %x", f.name, y.Value, x.Value)
			}
		}
		if f.name == ServerCertFile {
			if a.SerialNumber.Cmp(b.SerialNumber) != 0 || !a.Validity.NotBefore.Equal(b.Validity.NotBefore) ||
				!a.Validity.NotAfter.Equal(b.Validity.NotAfter) {
				t.Errorf("server serial or dates differ")
			}
		} else if py.NotAfter.Sub(py.NotBefore) != gc.NotAfter.Sub(gc.NotBefore) || a.SerialNumber.BitLen() > 159 {
			t.Errorf("CA validity length or serial size differ")
		}
		if f.name == ServerCertFile && len(py.Raw) != len(gc.Raw) { // the same structure, one fixed-size key
			t.Errorf("%s: %d bytes, make_certs.py's %d", f.name, len(gc.Raw), len(py.Raw))
		}
	}
	for _, name := range []string{CAKeyFile, ServerKeyFile} {
		block := pemOf(t, ref, name)
		if !strings.HasPrefix(string(block), "-----BEGIN RSA PRIVATE KEY-----\n") {
			t.Errorf("make_certs.py's %s is not PKCS#1 (the test's assumption)", name)
		}
	}
}
