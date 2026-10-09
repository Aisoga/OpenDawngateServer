package redirector

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/logging"
)

func TestGetServerInstanceReplyMatchesPythonStub(t *testing.T) {
	want := golden.Load(t).WriterCase(t, "getserverinstance_reply")
	got, err := GetServerInstanceReply("127.0.0.1", 10041)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != want.Hex {
		t.Errorf("got %x\nwant %s", got, want.Hex)
	}
	if _, err := GetServerInstanceReply("localhost", 10041); err == nil {
		t.Error("no error for a host name")
	}
}

// writeTestCerts writes a throwaway self-signed RSA-1024 pair. (The real pair, RSA-1024 with e = 3
// and MD5, comes from server/tools/make_certs.py; Go can't create it. TestRealCerts uses it if present.)
func writeTestCerts(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, typ string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("server_cert.pem", "CERTIFICATE", der)
	write("server_key.pem", "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
	return dir
}

// exchange runs one getServerInstance over TLS the way ProtoSSL does (TLS <= 1.1, RSA key exchange
// with AES-SHA, no SNI) and returns the connection state, the reply frame and the log.
func exchange(t *testing.T, certs string) (tls.ConnectionState, []byte, string) {
	t.Helper()
	cfg, err := TLSConfig(certs)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	s := &Server{Log: logging.New(&logBuf), Host: "127.0.0.1", BlazePort: 10041}
	done := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		ln.Close()
		if err == nil {
			s.ServeConn(tls.Server(c, cfg))
		}
		close(done)
	}()

	c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		InsecureSkipVerify: true, // ProtoSSL checks against our CA; this test only checks the protocol
		MinVersion:         tls.VersionTLS10,
		MaxVersion:         tls.VersionTLS11,
		CipherSuites:       []uint16{tls.TLS_RSA_WITH_AES_128_CBC_SHA, tls.TLS_RSA_WITH_AES_256_CBC_SHA},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := fire.Encode(fire.Frame{Component: ComponentRedirector, Command: CmdGetServerInstance, Type: fire.Request, ID: 0,
		Payload: golden.Bytes(t, decodeCase(t, "getserverinstance_request"))})
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	f, err := fire.NewReader(c).Next()
	if err != nil {
		t.Fatal(err)
	}
	state := c.ConnectionState()
	c.Close()
	<-done
	reply, _ := fire.Encode(f)
	return state, reply, logBuf.String()
}

func decodeCase(t *testing.T, name string) string {
	for _, c := range golden.Load(t).Decode {
		if c.Name == name {
			return c.Hex
		}
	}
	t.Fatalf("no decode case %q", name)
	return ""
}

func checkExchange(t *testing.T, certs string) {
	g := golden.Load(t)
	state, reply, log := exchange(t, certs)
	if state.Version != tls.VersionTLS11 {
		t.Errorf("negotiated %s, want TLSv1.1", VersionName(state.Version))
	}
	if want := g.Frame(t, "getserverinstance_reply").Hex; hex.EncodeToString(reply) != want {
		t.Errorf("reply frame\n got %x\nwant %s", reply, want)
	}
	for _, want := range []string{
		"[redirector] connection from ('127.0.0.1', ",
		"), TLSv1.1 AES",
		"[redirector] <- Redirector(0x0005) cmd 0x0001 request id 0 err 0x0, ",
		"    NAME: 'battlegrounds-1-pc'\n",
		"[redirector] -> getServerInstance reply: 127.0.0.1:10041, SECU=0\n",
		"[redirector] connection closed\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

func TestTLS11Exchange(t *testing.T) { checkExchange(t, writeTestCerts(t)) }

// TestRealCerts repeats the exchange with the e = 3, MD5-signed pair the client trusts, if it exists.
func TestRealCerts(t *testing.T) {
	certs := filepath.Join(golden.Dir(), "..", "certs")
	if _, err := os.Stat(filepath.Join(certs, "server_key.pem")); err != nil {
		t.Skip("no server/certs (make them with server/tools/make_certs.py)")
	}
	checkExchange(t, certs)
}

// A client that insists on TLS 1.2 is refused: the redirector speaks what ProtoSSL speaks.
func TestRefusesTLS12Only(t *testing.T) {
	cfg, err := TLSConfig(writeTestCerts(t))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			tls.Server(c, cfg).Handshake()
			c.Close()
		}
	}()
	if c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}); err == nil {
		c.Close()
		t.Error("TLS 1.2-only handshake succeeded")
	}
}
