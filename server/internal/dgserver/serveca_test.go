package dgserver

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/web"
)

// httpGet fetches path from the server's HTTP port and returns the status line and body.
func httpGet(t *testing.T, port int, path string) (string, []byte) {
	t.Helper()
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: x\r\n\r\n", path)
	raw, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	head, body, _ := bytes.Cut(raw, []byte("\r\n\r\n"))
	status, _, _ := bytes.Cut(head, []byte("\r\n"))
	return string(status), body
}

// --serve-ca serves ca_modulus.bin at /dawngate/ca_modulus.bin; without it the path is a 404.
func TestServeCA(t *testing.T) {
	certs := testCerts(t)
	modulus := bytes.Repeat([]byte{0x5A}, 128)
	if err := os.WriteFile(filepath.Join(certs, "ca_modulus.bin"), modulus, 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := start(config{bind: "127.0.0.1", certs: certs, logDir: t.TempDir(), serveCA: true})
	if err != nil {
		t.Fatal(err)
	}
	status, body := httpGet(t, in.ports[3], "/"+web.CAModulusPath)
	info := in.info(config{bind: "127.0.0.1"})
	in.stop()
	if status != "HTTP/1.1 200 OK" || !bytes.Equal(body, modulus) {
		t.Errorf("--serve-ca: %q, %d bytes", status, len(body))
	}
	if info.CAFingerprint != web.Fingerprint(modulus) || len(info.TCPPorts) != 5 {
		t.Errorf("info %+v", info)
	}

	off, err := start(config{bind: "127.0.0.1", certs: certs, logDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer off.stop()
	if status, _ := httpGet(t, off.ports[3], "/"+web.CAModulusPath); status != "HTTP/1.1 404 Not Found" {
		t.Errorf("without --serve-ca: %q", status)
	}
	if err := os.Remove(filepath.Join(certs, "ca_modulus.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := start(config{bind: "127.0.0.1", certs: certs, logDir: t.TempDir(), serveCA: true}); err == nil ||
		!strings.Contains(err.Error(), "--serve-ca") {
		t.Errorf("--serve-ca without ca_modulus.bin: %v", err)
	}
}

// Main parses dgserver's flags from its arguments (a FlagSet): --print-ports and a bad flag.
func TestMainArguments(t *testing.T) {
	if code := Main([]string{"--no-such-flag"}); code != 2 {
		t.Errorf("a bad flag: exit %d, want 2", code)
	}
	if code := Main([]string{"-h"}); code != 0 {
		t.Errorf("-h: exit %d, want 0", code)
	}
	if code := Main([]string{"--print-ports", "--blaze-port", "20041"}); code != 0 {
		t.Errorf("--print-ports: exit %d", code)
	}
}
