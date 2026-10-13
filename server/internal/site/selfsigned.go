package site

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
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
	"slices"
	"strings"
	"time"
)

// The website's certificate without a domain (LAN servers and the test lanes): a self-signed ECDSA P-256 certificate
// for the server's addresses, made at the first start and kept in the site's folder, so the browser's warning
// (which the player accepts once per browser, after comparing the fingerprint the server prints) stays the same across
// restarts. A real certificate needs a domain name (--web-domain, acme.go).

// SelfSignedFile is the certificate and its key (PEM) in the site's folder.
const SelfSignedFile = "site_selfsigned.pem"

// selfSignedDays is the certificate's validity; it is made anew 30 days before its end, or when the hosts change.
const selfSignedDays = 397

// SelfSigned returns the self-signed certificate for hosts (IP addresses and names), from dir when the one kept there
// is current, else a new one (written to dir; dir "" = kept in memory only). fp is its SHA-256 fingerprint (hex, the
// browsers' "SHA-256 fingerprint"), fresh whether it was made now.
func SelfSigned(dir string, hosts []string, now time.Time) (cert tls.Certificate, fp string, fresh bool, err error) {
	hosts = cleanHosts(hosts)
	if len(hosts) == 0 {
		return cert, "", false, errors.New("site: a self-signed certificate needs at least one host")
	}
	file := ""
	if dir != "" {
		file = filepath.Join(dir, SelfSignedFile)
		if c, err := tls.LoadX509KeyPair(file, file); err == nil && c.Leaf != nil && covers(c.Leaf, hosts) &&
			now.Before(c.Leaf.NotAfter.Add(-30*24*time.Hour)) && !now.Before(c.Leaf.NotBefore) {
			return c, Fingerprint(c.Leaf.Raw), false, nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return cert, "", false, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return cert, "", false, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: hosts[0], Organization: []string{"Dawngate server (self-signed)"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(selfSignedDays * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true, // a self-signed leaf: some browsers want it to be its own CA to accept the exception
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else {
			tpl.DNSNames = append(tpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return cert, "", false, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return cert, "", false, err
	}
	pemBytes := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})...)
	if file != "" {
		if err := writePrivate(file, pemBytes); err != nil {
			return cert, "", false, err
		}
	}
	cert, err = tls.X509KeyPair(pemBytes, pemBytes)
	if err != nil {
		return cert, "", false, err
	}
	return cert, Fingerprint(der), true, nil
}

// Fingerprint is a certificate's SHA-256 fingerprint as browsers show it: upper-case hex pairs with colons.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	var parts []string
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

func cleanHosts(hosts []string) []string {
	var out []string
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

// covers says whether leaf names every host.
func covers(leaf *x509.Certificate, hosts []string) bool {
	for _, h := range hosts {
		if leaf.VerifyHostname(h) != nil {
			return false
		}
	}
	return true
}

// writePrivate writes a key file (owner only where the system has modes) through a temporary file.
func writePrivate(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp := file + ".partial"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("site: %v", err)
	}
	return nil
}
