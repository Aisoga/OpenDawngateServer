package certs

import (
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"
)

// Fingerprint is the first 16 hex digits of a CA modulus's SHA-256: patch_client.py --show's ca_sha256, Info's
// CAModulusSHA256, and what Start-Server prints and Start-Client asks a player to compare.
func Fingerprint(modulus []byte) string { return sha256Prefix(modulus) }

// VerifyServerCert checks what the client checks of the redirector's certificate before it trusts the server
// (Start-Client's check of a fetched CA): cert is signed (md5WithRSAEncryption, e = 3) by the CA with the 128-byte
// modulus, and its CN is cn (the address the client copy is patched for; "" skips the CN).
func VerifyServerCert(cert *x509.Certificate, modulus []byte, cn string) error {
	if len(modulus) != 128 {
		return fmt.Errorf("a CA modulus has 128 bytes, this one %d", len(modulus))
	}
	if cert.SignatureAlgorithm != x509.MD5WithRSA {
		return fmt.Errorf("the server certificate is signed with %v, not MD5 with RSA", cert.SignatureAlgorithm)
	}
	if err := verifyMD5RSA(new(big.Int).SetBytes(modulus), 3, cert.RawTBSCertificate, cert.Signature); err != nil {
		return errors.New("the server certificate isn't signed by this CA")
	}
	if cn != "" && cert.Subject.CommonName != cn {
		return fmt.Errorf("the server certificate is for %s, not %s (the client checks the CN against the server's address)",
			cert.Subject.CommonName, cn)
	}
	return nil
}
