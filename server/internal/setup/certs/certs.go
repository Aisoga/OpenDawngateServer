// Package certs makes the redirector's certificates for one install: a stand-in for EA's "OTG3 Certificate
// Authority" and a server certificate signed by it. It is the Go port of
// server/tools/make_certs.py, which stays the reference (ADR-3), with the same files, subject, issuer, extensions,
// validity and serial rules:
//
//   - RSA-1024 keys with public exponent 3, generated here from crypto/rand (crypto/rsa only makes e = 65537);
//   - md5WithRSAEncryption signatures, encoded and signed by hand with math/big (crypto/x509 refuses MD5);
//   - the CA: self-signed, CN=OTG3 Certificate Authority/C=US/ST=California/L=Redwood City/O=Electronic Arts, Inc./
//     OU=Online Technology Group/emailAddress=dirtysock-contact@ea.com (PrintableString, the e-mail IA5String), a
//     random 159-bit serial, valid from now for 7300 days, basicConstraints critical CA:TRUE, subjectKeyIdentifier;
//   - the server: C=US/ST=California/O=Electronic Arts, Inc./OU=Global Online Studio/CN=<the server's address>,
//     serial 0x03C2, valid 2015-05-07 12:13:18 .. 2035-05-02 12:13:18 UTC (EA's redirector certificate's dates),
//     basicConstraints CA:FALSE, subjectKeyIdentifier, authorityKeyIdentifier (keyid);
//   - files ca_key.pem, ca_cert.pem, ca_modulus.bin (the CA's 128-byte modulus, what exepatch writes into the exe),
//     server_key.pem, server_cert.pem (PKCS#1 "RSA PRIVATE KEY" and "CERTIFICATE" PEM).
//
// Every install makes its own set: never ship or share a key, or any server could impersonate any other to clients
// patched with that CA. The client checks the server certificate's CN against the redirector host it was patched
// with, so CN is the server's address (127.0.0.1, or its LAN address).
package certs

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// The five files of a set, in make_certs.py's names.
const (
	CAKeyFile      = "ca_key.pem"
	CACertFile     = "ca_cert.pem"
	CAModulusFile  = "ca_modulus.bin"
	ServerKeyFile  = "server_key.pem"
	ServerCertFile = "server_cert.pem"
)

// Files are the five files of a set (setup_all.py's CERT_FILES order).
var Files = []string{CAKeyFile, CACertFile, CAModulusFile, ServerKeyFile, ServerCertFile}

// The server certificate's fixed serial and dates (make_certs.py: serial.txt 03C2, NOT_BEFORE, NOT_AFTER); the CA is
// valid CADays from its creation (openssl req -days 7300).
var (
	ServerSerial    = big.NewInt(0x03C2)
	ServerNotBefore = time.Date(2015, 5, 7, 12, 13, 18, 0, time.UTC)
	ServerNotAfter  = time.Date(2035, 5, 2, 12, 13, 18, 0, time.UTC)
)

// CADays is the CA certificate's validity.
const CADays = 7300

var (
	oidMD5WithRSA      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 4}
	oidRSAEncryption   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidCommonName      = asn1.ObjectIdentifier{2, 5, 4, 3}
	oidCountry         = asn1.ObjectIdentifier{2, 5, 4, 6}
	oidLocality        = asn1.ObjectIdentifier{2, 5, 4, 7}
	oidState           = asn1.ObjectIdentifier{2, 5, 4, 8}
	oidOrganization    = asn1.ObjectIdentifier{2, 5, 4, 10}
	oidOrgUnit         = asn1.ObjectIdentifier{2, 5, 4, 11}
	oidEmailAddress    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}
	oidBasicConstraint = asn1.ObjectIdentifier{2, 5, 29, 19}
	oidSubjectKeyID    = asn1.ObjectIdentifier{2, 5, 29, 14}
	oidAuthorityKeyID  = asn1.ObjectIdentifier{2, 5, 29, 35}
	// DigestInfo's prefix for MD5 (RFC 8017 9.2 note 1)
	md5DigestInfoPrefix = []byte{0x30, 0x20, 0x30, 0x0c, 0x06, 0x08, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x02, 0x05, 0x05, 0x00, 0x04, 0x10}
)

type attr struct {
	oid   asn1.ObjectIdentifier
	value string
	tag   int // asn1.TagPrintableString or asn1.TagIA5String
}

// caSubject is make_certs.py's SUBJ_OTG3 in its order.
var caSubject = []attr{
	{oidCommonName, "OTG3 Certificate Authority", asn1.TagPrintableString},
	{oidCountry, "US", asn1.TagPrintableString},
	{oidState, "California", asn1.TagPrintableString},
	{oidLocality, "Redwood City", asn1.TagPrintableString},
	{oidOrganization, "Electronic Arts, Inc.", asn1.TagPrintableString},
	{oidOrgUnit, "Online Technology Group", asn1.TagPrintableString},
	{oidEmailAddress, "dirtysock-contact@ea.com", asn1.TagIA5String},
}

// serverSubject is make_certs.py's SUBJ_SERVER for cn.
func serverSubject(cn string) []attr {
	return []attr{
		{oidCountry, "US", asn1.TagPrintableString},
		{oidState, "California", asn1.TagPrintableString},
		{oidOrganization, "Electronic Arts, Inc.", asn1.TagPrintableString},
		{oidOrgUnit, "Global Online Studio", asn1.TagPrintableString},
		{oidCommonName, cn, asn1.TagPrintableString},
	}
}

// Set is one install's certificates, as the files hold them.
type Set struct {
	CAKey      []byte // PEM, RSA PRIVATE KEY
	CACert     []byte // PEM, CERTIFICATE
	CAModulus  []byte // 128 bytes, big-endian
	ServerKey  []byte // PEM, RSA PRIVATE KEY
	ServerCert []byte // PEM, CERTIFICATE
}

// Generate makes a new set for the server address cn (the redirector host the clients are patched with), from
// random (nil: crypto/rand) at time now (the CA's start; zero: time.Now()).
func Generate(cn string, random io.Reader, now time.Time) (*Set, error) {
	if err := checkPrintable(cn); err != nil {
		return nil, err
	}
	if random == nil {
		random = rand.Reader
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC().Truncate(time.Second)
	caKey, err := generateKey(random)
	if err != nil {
		return nil, err
	}
	serverKey, err := generateKey(random)
	if err != nil {
		return nil, err
	}
	caName, err := encodeName(caSubject)
	if err != nil {
		return nil, err
	}
	serverName, err := encodeName(serverSubject(cn))
	if err != nil {
		return nil, err
	}
	caSerial, err := randomSerial(random)
	if err != nil {
		return nil, err
	}
	caKeyID := keyID(&caKey.pub)
	caCert, err := certificate(caKey, caSerial, caName, caName, now, now.AddDate(0, 0, CADays), &caKey.pub, []pkix.Extension{
		{Id: oidBasicConstraint, Critical: true, Value: []byte{0x30, 0x03, 0x01, 0x01, 0xFF}}, // CA:TRUE
		{Id: oidSubjectKeyID, Value: octetString(caKeyID)},
	})
	if err != nil {
		return nil, err
	}
	akid, err := asn1.Marshal(struct {
		KeyID []byte `asn1:"optional,tag:0"`
	}{caKeyID})
	if err != nil {
		return nil, err
	}
	serverCert, err := certificate(caKey, ServerSerial, caName, serverName, ServerNotBefore, ServerNotAfter, &serverKey.pub,
		[]pkix.Extension{
			{Id: oidBasicConstraint, Value: []byte{0x30, 0x00}}, // CA:FALSE
			{Id: oidSubjectKeyID, Value: octetString(keyID(&serverKey.pub))},
			{Id: oidAuthorityKeyID, Value: akid},
		})
	if err != nil {
		return nil, err
	}
	return &Set{
		CAKey:      pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: caKey.pkcs1()}),
		CACert:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert}),
		CAModulus:  caKey.pub.n.FillBytes(make([]byte, 128)),
		ServerKey:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: serverKey.pkcs1()}),
		ServerCert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert}),
	}, nil
}

// Write writes the set's five files into dir (made if missing), replacing files of the same names.
func (s *Set) Write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{{CAKeyFile, s.CAKey, 0o600}, {ServerKeyFile, s.ServerKey, 0o600}, {CACertFile, s.CACert, 0o644},
		{ServerCertFile, s.ServerCert, 0o644}, {CAModulusFile, s.CAModulus, 0o644}} {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.data, f.mode); err != nil {
			return err
		}
	}
	return nil
}

// Make generates a new set for cn and writes it into dir (make_certs.py <dir> <cn>).
func Make(dir, cn string) (*Set, error) {
	s, err := Generate(cn, nil, time.Time{})
	if err != nil {
		return nil, err
	}
	return s, s.Write(dir)
}

// Info describes a checked set.
type Info struct {
	CN              string // the server certificate's common name
	CAModulusSHA256 string // the first 16 hex digits of ca_modulus.bin's SHA-256 (patch_client.py --show's ca_sha256)
}

// ErrNoSet: not all five files are there.
var ErrNoSet = errors.New("no complete certificate set")

// Check reads the set in dir as setup_all.py's certificate step does: all five files, ca_modulus.bin and the server
// certificate belong to ca_cert.pem (modulus; issuer = the CA's subject; the signature), the keys to their
// certificates, and, unless cn is "", the server certificate's CN is cn.
func Check(dir, cn string) (Info, error) {
	var info Info
	data := map[string][]byte{}
	for _, name := range Files {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			return info, fmt.Errorf("%s: %w (%s missing)", dir, ErrNoSet, name)
		}
		if err != nil {
			return info, err
		}
		data[name] = b
	}
	ca, err := parseCert(data[CACertFile])
	if err != nil {
		return info, fmt.Errorf("%s: %v", CACertFile, err)
	}
	srv, err := parseCert(data[ServerCertFile])
	if err != nil {
		return info, fmt.Errorf("%s: %v", ServerCertFile, err)
	}
	caPub, ok1 := ca.PublicKey.(*rsaPublicKeyStd)
	srvPub, ok2 := srv.PublicKey.(*rsaPublicKeyStd)
	if !ok1 || !ok2 || caPub.E != 3 || srvPub.E != 3 || caPub.N.BitLen() != 1024 || srvPub.N.BitLen() != 1024 {
		return info, fmt.Errorf("%s: the keys are not RSA-1024 with exponent 3", dir)
	}
	if !bytes.Equal(caPub.N.FillBytes(make([]byte, 128)), data[CAModulusFile]) || !bytes.Equal(srv.RawIssuer, ca.RawSubject) {
		return info, fmt.Errorf("%s: ca_modulus.bin or server_cert.pem doesn't belong to ca_cert.pem", dir)
	}
	if err := verifyMD5RSA(caPub.N, caPub.E, srv.RawTBSCertificate, srv.Signature); err != nil {
		return info, fmt.Errorf("%s: server_cert.pem's signature: %v", dir, err)
	}
	for _, k := range []struct {
		name string
		pub  *rsaPublicKeyStd
	}{{CAKeyFile, caPub}, {ServerKeyFile, srvPub}} {
		block, _ := pem.Decode(data[k.name])
		if block == nil || block.Type != "RSA PRIVATE KEY" {
			return info, fmt.Errorf("%s: not a PEM RSA PRIVATE KEY", k.name)
		}
		priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return info, fmt.Errorf("%s: %v", k.name, err)
		}
		if priv.N.Cmp(k.pub.N) != 0 || priv.E != k.pub.E {
			return info, fmt.Errorf("%s doesn't belong to its certificate", k.name)
		}
	}
	info.CN = srv.Subject.CommonName
	info.CAModulusSHA256 = sha256Prefix(data[CAModulusFile])
	if cn != "" && info.CN != cn {
		return info, fmt.Errorf("%s is for CN %s, not %s (the client checks the CN against the server's address)", dir, info.CN, cn)
	}
	return info, nil
}

// --- RSA with e = 3

type publicKey struct {
	n *big.Int
	e int
}

type privateKey struct {
	pub        publicKey
	d, p, q    *big.Int
	dp, dq, qi *big.Int
}

var bigOne = big.NewInt(1)

// generateKey makes an RSA-1024 key with e = 3: two 512-bit primes (top two bits set, so n has exactly 1024 bits)
// with p, q = 2 mod 3 (so 3 is invertible), d = 3^-1 mod lcm(p-1, q-1) as OpenSSL computes it.
func generateKey(random io.Reader) (*privateKey, error) {
	e := big.NewInt(3)
	for tries := 0; tries < 100; tries++ {
		p, err := prime3(random)
		if err != nil {
			return nil, err
		}
		q, err := prime3(random)
		if err != nil {
			return nil, err
		}
		if p.Cmp(q) == 0 {
			continue
		}
		if p.Cmp(q) < 0 { // OpenSSL keeps p > q
			p, q = q, p
		}
		n := new(big.Int).Mul(p, q)
		if n.BitLen() != 1024 {
			continue
		}
		p1, q1 := new(big.Int).Sub(p, bigOne), new(big.Int).Sub(q, bigOne)
		g := new(big.Int).GCD(nil, nil, p1, q1)
		lambda := new(big.Int).Div(new(big.Int).Mul(p1, q1), g)
		d := new(big.Int).ModInverse(e, lambda)
		if d == nil {
			continue
		}
		k := &privateKey{pub: publicKey{n: n, e: 3}, d: d, p: p, q: q,
			dp: new(big.Int).Mod(d, p1), dq: new(big.Int).Mod(d, q1), qi: new(big.Int).ModInverse(q, p)}
		// a round trip as the check
		m := big.NewInt(0x5A5A5A)
		c := new(big.Int).Exp(m, e, n)
		if new(big.Int).Exp(c, d, n).Cmp(m) != 0 {
			continue
		}
		return k, nil
	}
	return nil, errors.New("certs: no RSA key with e = 3 found")
}

// prime3 is a random 512-bit prime with its top two bits set and p = 2 mod 3.
func prime3(random io.Reader) (*big.Int, error) {
	three := big.NewInt(3)
	for {
		p, err := rand.Prime(random, 512)
		if err != nil {
			return nil, err
		}
		if new(big.Int).Mod(p, three).Int64() == 2 {
			return p, nil
		}
	}
}

// pkcs1 is the key's PKCS#1 RSAPrivateKey DER ("RSA PRIVATE KEY", as cryptography's TraditionalOpenSSL writes it).
func (k *privateKey) pkcs1() []byte {
	b, err := asn1.Marshal(struct {
		Version               int
		N                     *big.Int
		E                     int
		D, P, Q, Dp, Dq, Qinv *big.Int
	}{0, k.pub.n, k.pub.e, k.d, k.p, k.q, k.dp, k.dq, k.qi})
	if err != nil {
		panic(err) // only big.Ints and ints
	}
	return b
}

// rsaPublicKeyDER is the RSAPublicKey SEQUENCE {n, e}.
func rsaPublicKeyDER(pub *publicKey) []byte {
	b, err := asn1.Marshal(struct {
		N *big.Int
		E int
	}{pub.n, pub.e})
	if err != nil {
		panic(err)
	}
	return b
}

// keyID is OpenSSL's subjectKeyIdentifier "hash": SHA-1 of the subjectPublicKey BIT STRING's contents.
func keyID(pub *publicKey) []byte {
	h := sha1.Sum(rsaPublicKeyDER(pub))
	return h[:]
}

// signMD5 is an md5WithRSAEncryption signature (EMSA-PKCS1-v1_5 with MD5's DigestInfo, s = EM^d mod n).
func signMD5(k *privateKey, msg []byte) []byte {
	h := md5.Sum(msg)
	t := append(bytes.Clone(md5DigestInfoPrefix), h[:]...)
	em := make([]byte, 128)
	em[1] = 0x01
	for i := 2; i < 128-len(t)-1; i++ {
		em[i] = 0xFF
	}
	copy(em[128-len(t):], t)
	m := new(big.Int).SetBytes(em)
	s := new(big.Int).Exp(m, k.d, k.pub.n)
	if new(big.Int).Exp(s, big.NewInt(int64(k.pub.e)), k.pub.n).Cmp(m) != 0 {
		panic("certs: signature check failed") // a broken key; generateKey's round trip makes this unreachable
	}
	return s.FillBytes(make([]byte, 128))
}

// verifyMD5RSA checks an md5WithRSAEncryption signature with the public key (n, e).
func verifyMD5RSA(n *big.Int, e int, msg, sig []byte) error {
	k := (n.BitLen() + 7) / 8
	if len(sig) != k {
		return errors.New("wrong signature length")
	}
	m := new(big.Int).Exp(new(big.Int).SetBytes(sig), big.NewInt(int64(e)), n)
	em := m.FillBytes(make([]byte, k))
	h := md5.Sum(msg)
	t := append(bytes.Clone(md5DigestInfoPrefix), h[:]...)
	want := make([]byte, k)
	want[1] = 0x01
	for i := 2; i < k-len(t)-1; i++ {
		want[i] = 0xFF
	}
	copy(want[k-len(t):], t)
	if !bytes.Equal(em, want) {
		return errors.New("the MD5-with-RSA signature doesn't verify")
	}
	return nil
}

// --- the certificate

type tbsCertificate struct {
	Version            int `asn1:"optional,explicit,default:0,tag:0"`
	SerialNumber       *big.Int
	SignatureAlgorithm pkix.AlgorithmIdentifier
	Issuer             asn1.RawValue
	Validity           validity
	Subject            asn1.RawValue
	PublicKey          publicKeyInfo
	Extensions         []pkix.Extension `asn1:"optional,explicit,tag:3"`
}

type validity struct {
	NotBefore, NotAfter time.Time // UTCTime (all dates here lie before 2050)
}

type publicKeyInfo struct {
	Algorithm pkix.AlgorithmIdentifier
	PublicKey asn1.BitString
}

type certificateDER struct {
	TBS                asn1.RawValue
	SignatureAlgorithm pkix.AlgorithmIdentifier
	Signature          asn1.BitString
}

func certificate(signer *privateKey, serial *big.Int, issuer, subject []byte, notBefore, notAfter time.Time,
	pub *publicKey, exts []pkix.Extension) ([]byte, error) {
	md5rsa := pkix.AlgorithmIdentifier{Algorithm: oidMD5WithRSA, Parameters: asn1.NullRawValue}
	keyDER := rsaPublicKeyDER(pub)
	tbs, err := asn1.Marshal(tbsCertificate{
		Version:            2, // v3
		SerialNumber:       serial,
		SignatureAlgorithm: md5rsa,
		Issuer:             asn1.RawValue{FullBytes: issuer},
		Validity:           validity{notBefore.UTC(), notAfter.UTC()},
		Subject:            asn1.RawValue{FullBytes: subject},
		PublicKey: publicKeyInfo{
			Algorithm: pkix.AlgorithmIdentifier{Algorithm: oidRSAEncryption, Parameters: asn1.NullRawValue},
			PublicKey: asn1.BitString{Bytes: keyDER, BitLength: 8 * len(keyDER)},
		},
		Extensions: exts,
	})
	if err != nil {
		return nil, err
	}
	sig := signMD5(signer, tbs)
	return asn1.Marshal(certificateDER{
		TBS:                asn1.RawValue{FullBytes: tbs},
		SignatureAlgorithm: md5rsa,
		Signature:          asn1.BitString{Bytes: sig, BitLength: 8 * len(sig)},
	})
}

// encodeName is the DER Name: one SET with one attribute per RDN, in the given order, with the given string types.
func encodeName(attrs []attr) ([]byte, error) {
	var rdns pkix.RDNSequence
	for _, a := range attrs {
		rdns = append(rdns, pkix.RelativeDistinguishedNameSET{{
			Type:  a.oid,
			Value: asn1.RawValue{Tag: a.tag, Class: asn1.ClassUniversal, Bytes: []byte(a.value)},
		}})
	}
	return asn1.Marshal(rdns)
}

func octetString(b []byte) []byte {
	out, err := asn1.Marshal(b)
	if err != nil {
		panic(err)
	}
	return out
}

// randomSerial is openssl req -x509's serial: a random number of up to 159 bits (positive, so at most 20 bytes).
func randomSerial(random io.Reader) (*big.Int, error) {
	for {
		b := make([]byte, 20)
		if _, err := io.ReadFull(random, b); err != nil {
			return nil, err
		}
		b[0] &= 0x7F
		s := new(big.Int).SetBytes(b)
		if s.Sign() > 0 {
			return s, nil
		}
	}
}

// checkPrintable: the CN must fit a PrintableString (an IPv4 address or a host name does).
func checkPrintable(s string) error {
	if s == "" || len(s) > 64 {
		return fmt.Errorf("certs: the server name must be 1-64 characters, got %q", s)
	}
	for _, c := range []byte(s) {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		for _, x := range []byte(" '()+,-./:=?") {
			ok = ok || c == x
		}
		if !ok {
			return fmt.Errorf("certs: %q has a character a PrintableString can't hold", s)
		}
	}
	return nil
}
