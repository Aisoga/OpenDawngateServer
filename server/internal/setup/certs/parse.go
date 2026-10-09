package certs

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
)

type rsaPublicKeyStd = rsa.PublicKey

// parseCert reads one PEM CERTIFICATE (crypto/x509 parses MD5-signed certificates; it only won't check them).
func parseCert(b []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("not a PEM CERTIFICATE")
	}
	return x509.ParseCertificate(block.Bytes)
}

func sha256Prefix(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])[:16]
}
