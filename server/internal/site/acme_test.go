package site

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/acme"
)

// fakeCA is a minimal ACME server (RFC 8555) for one order, in the test's process: it checks the TLS-ALPN-01 challenge
// the way a real CA does (a TLS handshake to the site with ALPN acme-tls/1 and the domain as SNI, the challenge
// certificate's acmeIdentifier extension = SHA-256 of the key authorization) and issues from its own test root. Nothing
// leaves the machine: the site is dialled at the listener address the test gives (a real CA dials <domain>:443).
type fakeCA struct {
	t       *testing.T
	srv     *httptest.Server
	site    string // the site's TLS address (host:port) the validation dials
	domain  string
	rootKey *ecdsa.PrivateKey
	root    *x509.Certificate

	mu         sync.Mutex
	thumb      string // the account key's JWK thumbprint
	authzValid bool
	issued     bool
	accounts   int
	validated  int
	refuse     bool // fail every validation (the "DNS not pointing here" case)
	csrKey     any  // the finalized CSR's public key
}

func newFakeCA(t *testing.T, domain string) *fakeCA {
	ca := &fakeCA{t: t, domain: domain}
	ca.rootKey, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake ACME root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &ca.rootKey.PublicKey, ca.rootKey)
	ca.root, _ = x509.ParseCertificate(der)
	ca.srv = httptest.NewServer(http.HandlerFunc(ca.serve))
	t.Cleanup(ca.srv.Close)
	return ca
}

func (ca *fakeCA) url(p string) string { return ca.srv.URL + p }

type jws struct {
	Protected, Payload, Signature string
}

func (ca *fakeCA) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Replay-Nonce", fmt.Sprintf("n%d", time.Now().UnixNano()))
	if r.URL.Path == "/dir" {
		json.NewEncoder(w).Encode(map[string]any{"newNonce": ca.url("/nonce"), "newAccount": ca.url("/account"),
			"newOrder": ca.url("/order"), "revokeCert": ca.url("/revoke"), "keyChange": ca.url("/keychange"),
			"meta": map[string]any{"termsOfService": ca.url("/tos")}})
		return
	}
	if r.URL.Path == "/nonce" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var req jws
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "not JWS", 400)
		return
	}
	prot, _ := base64.RawURLEncoding.DecodeString(req.Protected)
	payload, _ := base64.RawURLEncoding.DecodeString(req.Payload)
	ca.mu.Lock()
	defer ca.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	order := func(status string) map[string]any {
		o := map[string]any{"status": status, "identifiers": []any{map[string]string{"type": "dns", "value": ca.domain}},
			"authorizations": []string{ca.url("/authz/1")}, "finalize": ca.url("/finalize/1")}
		if status == "valid" {
			o["certificate"] = ca.url("/cert/1")
		}
		return o
	}
	authz := func() map[string]any {
		st := "pending"
		if ca.authzValid {
			st = "valid"
		}
		return map[string]any{"status": st, "identifier": map[string]string{"type": "dns", "value": ca.domain},
			"challenges": []any{
				map[string]string{"type": "http-01", "url": ca.url("/chal/http"), "token": "tokhttp", "status": "pending"},
				map[string]string{"type": "tls-alpn-01", "url": ca.url("/chal/1"), "token": "tok1", "status": st}}}
	}
	switch r.URL.Path {
	case "/account":
		var p struct {
			JWK json.RawMessage `json:"jwk"`
		}
		json.Unmarshal(prot, &p)
		var k struct{ Crv, Kty, X, Y string }
		json.Unmarshal(p.JWK, &k)
		x, _ := base64.RawURLEncoding.DecodeString(k.X)
		y, _ := base64.RawURLEncoding.DecodeString(k.Y)
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		ca.thumb, _ = acme.JWKThumbprint(pub)
		ca.accounts++
		w.Header().Set("Location", ca.url("/acct/1"))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"status": "valid"})
	case "/order":
		w.Header().Set("Location", ca.url("/order/1"))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(order("pending"))
	case "/order/1":
		st := "pending"
		if ca.issued {
			st = "valid"
		} else if ca.authzValid {
			st = "ready"
		}
		json.NewEncoder(w).Encode(order(st))
	case "/authz/1":
		json.NewEncoder(w).Encode(authz())
	case "/chal/1":
		if err := ca.validate(); err != nil {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"type": "urn:ietf:params:acme:error:unauthorized", "detail": err.Error()})
			return
		}
		ca.authzValid = true
		ca.validated++
		json.NewEncoder(w).Encode(map[string]string{"type": "tls-alpn-01", "url": ca.url("/chal/1"), "token": "tok1", "status": "valid"})
	case "/finalize/1":
		var p struct{ CSR string }
		json.Unmarshal(payload, &p)
		der, _ := base64.RawURLEncoding.DecodeString(p.CSR)
		csr, err := x509.ParseCertificateRequest(der)
		if err != nil || !ca.authzValid || len(csr.DNSNames) != 1 || csr.DNSNames[0] != ca.domain {
			http.Error(w, "bad CSR", 400)
			return
		}
		ca.issued, ca.csrKey = true, csr.PublicKey
		w.Header().Set("Location", ca.url("/order/1"))
		json.NewEncoder(w).Encode(order("valid"))
	case "/cert/1":
		w.Header().Set("Content-Type", "application/pem-certificate-chain")
		w.Write(ca.issue())
	case "/revoke", "/authz-deactivate":
		json.NewEncoder(w).Encode(map[string]any{})
	default:
		http.Error(w, "unknown "+r.URL.Path, 404)
	}
}

// validate does what the CA does for TLS-ALPN-01 (RFC 8737 section 3).
func (ca *fakeCA) validate() error {
	if ca.refuse {
		return fmt.Errorf("connection refused")
	}
	conn, err := tls.Dial("tcp", ca.site, &tls.Config{ServerName: ca.domain, NextProtos: []string{acme.ALPNProto},
		InsecureSkipVerify: true})
	if err != nil {
		return err
	}
	defer conn.Close()
	st := conn.ConnectionState()
	if st.NegotiatedProtocol != acme.ALPNProto {
		return fmt.Errorf("ALPN %q", st.NegotiatedProtocol)
	}
	leaf := st.PeerCertificates[0]
	if leaf.VerifyHostname(ca.domain) != nil {
		return fmt.Errorf("challenge certificate for %v", leaf.DNSNames)
	}
	sum := sha256.Sum256([]byte("tok1." + ca.thumb))
	want, _ := asn1.Marshal(sum[:])
	for _, e := range leaf.Extensions {
		if e.Id.Equal(asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 31}) {
			if string(e.Value) == string(want) && e.Critical {
				return nil
			}
			return fmt.Errorf("wrong acmeIdentifier")
		}
	}
	return fmt.Errorf("no acmeIdentifier extension")
}

// issue makes the leaf for the domain with the finalized CSR's key, and the chain with the root.
func (ca *fakeCA) issue() []byte {
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: ca.domain},
		DNSNames: []string{ca.domain}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.root, ca.csrKey, ca.rootKey)
	if err != nil {
		ca.t.Errorf("issue: %v", err)
	}
	return append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.root.Raw})...)
}
