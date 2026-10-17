package launcher

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"flag"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/logging"
	"dawngate/server/internal/redirector"
	"dawngate/server/internal/setup/certs"
	"dawngate/server/internal/web"
)

func TestConfigFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ServerConfigName)
	c, created, err := LoadOrCreate(path, ServerConfigText)
	if err != nil || !created {
		t.Fatalf("LoadOrCreate: %v %v", created, err)
	}
	want := Config{"address": "127.0.0.1", "bind": "", "i_accept_the_risk": "no", "profile": "auto", "tunnel": "auto", "gateway_port": "42128",
		"shaper_select": "on", "dedicated": "no", "signup": "open", "signups_per_address": "3", "log_days": "30", "web_port": "443", "web_domain": "",
		"web_email": "", "web_proxy": "off", "folder": "Dawngate-Data",
		"zip": "", "server_flags": ""}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("defaults %v", c)
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Contains(raw, []byte("\r\naddress = 127.0.0.1\r\n")) {
		t.Error("the file isn't written with Windows line ends")
	}
	if c, created, _ = LoadOrCreate(path, "address = 1.2.3.4\n"); created || c["address"] != "127.0.0.1" {
		t.Error("an existing file was replaced")
	}
	if err := SetKey(path, "address", "192.168.1.20"); err != nil {
		t.Fatal(err)
	}
	if err := SetKey(path, "extra", "x"); err != nil {
		t.Fatal(err)
	}
	c, _ = ReadConfig(path)
	if c["address"] != "192.168.1.20" || c["extra"] != "x" || c["folder"] != "Dawngate-Data" {
		t.Errorf("after SetKey %v", c)
	}
	if u := c.Unknown(serverKeys...); !reflect.DeepEqual(u, []string{"extra"}) {
		t.Errorf("unknown %v", u)
	}
	// Notepad's BOM, quotes, case, comments
	c, err = ParseConfig(strings.NewReader(bom+"; c\r\n# c\r\n[x]\r\nAddress = \"10.0.0.5\"\r\nshaper_select=OFF\r\n"), "t")
	if err != nil || c["address"] != "10.0.0.5" {
		t.Errorf("%v %v", c, err)
	}
	if on, err := c.Bool("shaper_select", true); on || err != nil {
		t.Errorf("Bool off: %v %v", on, err)
	}
	if on, err := c.Bool("missing", true); !on || err != nil {
		t.Errorf("Bool default: %v %v", on, err)
	}
	if _, err := (Config{"x": "maybe"}).Bool("x", true); err == nil {
		t.Error("Bool took maybe")
	}
	if _, err := ParseConfig(strings.NewReader("no equals sign\n"), "t"); err == nil {
		t.Error("a line without = passed")
	}
}

func TestSplitFlags(t *testing.T) {
	got := SplitFlags(` --owned starter  --broadcast "hello world"	--mm-bots=false `)
	want := []string{"--owned", "starter", "--broadcast", "hello world", "--mm-bots=false"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%q", got)
	}
	if got := SplitFlags(""); got != nil {
		t.Errorf("empty: %q", got)
	}
}

func TestZipCandidatesAndAsk(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "release")
	c := ZipCandidates(exe, "")
	if c[0] != filepath.Join(exe, "Dawngate.zip") || c[1] != filepath.Join(filepath.Dir(exe), "Dawngate.zip") ||
		!strings.HasSuffix(c[len(c)-1], filepath.Join("Downloads", "Dawngate.zip")) {
		t.Errorf("candidates %v", c)
	}
	if c := ZipCandidates(exe, `..\x.zip`); len(c) != 1 || c[0] != filepath.Join(exe, `..\x.zip`) {
		t.Errorf("configured %v", c)
	}
	// the question: a wrong file, then a folder with the zip in it (as dragged), quotes stripped
	dir := t.TempDir()
	notZip := filepath.Join(dir, "notes.txt")
	os.WriteFile(notZip, []byte("hello"), 0o644)
	zp := writeZip(t, dir, map[string]string{"Dawngate/game/a.txt": "a"})
	var out bytes.Buffer
	got, err := AskZip(bufio.NewReader(strings.NewReader(notZip+"\n\""+dir+"\"\n")), &out, nil)
	if err != nil || got != zp {
		t.Errorf("AskZip %q %v\n%s", got, err, out.String())
	}
	if !strings.Contains(out.String(), "is not a zip archive") {
		t.Errorf("no complaint about the wrong file:\n%s", out.String())
	}
	other := writeZip(t, t.TempDir(), map[string]string{"Other/x": "x"})
	if _, err := AskZip(bufio.NewReader(strings.NewReader(other+"\n")), io.Discard, nil); err == nil {
		t.Error("a zip without Dawngate/ was taken")
	}
}

func TestTrustCA(t *testing.T) {
	store := filepath.Join(t.TempDir(), "servers", "192.168.1.20", "ca_modulus.bin")
	a, b := bytes.Repeat([]byte{1}, 128), bytes.Repeat([]byte{2}, 128)
	// no way to ask: refused, nothing stored
	if _, err := TrustCA("192.168.1.20", a, TrustOptions{Store: store}); err == nil || exists(store) {
		t.Fatalf("an unconfirmed CA: %v", err)
	}
	var asked []string
	no := func(q string) (bool, error) { asked = append(asked, q); return false, nil }
	yes := func(q string) (bool, error) { asked = append(asked, q); return true, nil }
	if _, err := TrustCA("192.168.1.20", a, TrustOptions{Store: store, Ask: no}); err == nil || exists(store) {
		t.Fatalf("a declined CA: %v", err)
	}
	if !strings.Contains(asked[0], certs.Fingerprint(a)) {
		t.Errorf("the question has no fingerprint: %q", asked[0])
	}
	if tr, err := TrustCA("192.168.1.20", a, TrustOptions{Store: store, Ask: yes}); tr != TrustNew || err != nil {
		t.Fatalf("confirmed: %v %v", tr, err)
	}
	if tr, err := TrustCA("192.168.1.20", a, TrustOptions{Store: store}); tr != TrustKnown || err != nil {
		t.Errorf("known: %v %v", tr, err)
	}
	// a changed CA: refused without a question, asked with a warning
	if _, err := TrustCA("192.168.1.20", b, TrustOptions{Store: store}); err == nil {
		t.Error("a changed CA passed")
	}
	asked = nil
	if tr, err := TrustCA("192.168.1.20", b, TrustOptions{Store: store, Ask: yes}); tr != TrustChanged || err != nil {
		t.Errorf("changed: %v %v", tr, err)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "WARNING") || !strings.Contains(asked[0], certs.Fingerprint(a)) {
		t.Errorf("the change's question %q", asked)
	}
	if got, _ := os.ReadFile(store); !bytes.Equal(got, b) {
		t.Error("the new CA isn't stored")
	}
	if tr, err := TrustCA("192.168.1.20", a, TrustOptions{Store: store, Accept: true}); tr != TrustChanged || err != nil {
		t.Errorf("--trust: %v %v", tr, err)
	}
}

// FetchCA and CheckServerCert against dgserver's own HTTP server (--serve-ca's answer) and redirector TLS, on
// ephemeral loopback ports.
func TestFetchAndCheckCA(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "certs")
	set, err := certs.Make(dir, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	log, err := logging.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	hl, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hl.Close()
	go (&web.HTTP{Log: log, Extra: []web.Builtin{{Path: web.CAModulusPath, ContentType: "application/octet-stream",
		Body: set.CAModulus}}}).Serve(hl)
	httpPort := hl.Addr().(*net.TCPAddr).Port
	got, err := FetchCA("127.0.0.1", httpPort, 5*time.Second)
	if err != nil || !bytes.Equal(got, set.CAModulus) {
		t.Fatalf("FetchCA: %v", err)
	}
	empty, _ := net.Listen("tcp4", "127.0.0.1:0")
	go (&web.HTTP{Log: log}).Serve(empty)
	defer empty.Close()
	if _, err := FetchCA("127.0.0.1", empty.Addr().(*net.TCPAddr).Port, 5*time.Second); err == nil ||
		!strings.Contains(err.Error(), "404") {
		t.Errorf("FetchCA without --serve-ca: %v", err)
	}

	cfg, err := redirector.TLSConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	tl, err := tls.Listen("tcp4", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tl.Close()
	go func() {
		for {
			c, err := tl.Accept()
			if err != nil {
				return
			}
			go func() { c.(*tls.Conn).Handshake(); c.Close() }()
		}
	}()
	port := tl.Addr().(*net.TCPAddr).Port
	if err := Answers("127.0.0.1", port, time.Second); err != nil {
		t.Errorf("Answers: %v", err)
	}
	if err := CheckServerCert("127.0.0.1", port, set.CAModulus, 5*time.Second); err != nil {
		t.Errorf("the server's own CA: %v", err)
	}
	if err := CheckServerCert("127.0.0.1", port, bytes.Repeat([]byte{0x5A}, 128), 5*time.Second); err == nil {
		t.Error("another CA passed")
	}
	if Answers("127.0.0.1", httpPortClosed(t), time.Second) == nil {
		t.Error("a closed port answers")
	}
}

// httpPortClosed is a port nothing listens on.
func httpPortClosed(t *testing.T) int {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

func TestHeldPorts(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	p := ln.Addr().(*net.TCPAddr).Port
	free := httpPortClosed(t)
	held := HeldPorts("127.0.0.1", []Port{{p, false, "taken"}, {free, false, "free"}, {free, true, "free udp"}})
	if len(held) != 1 || !strings.Contains(held[0], "taken") {
		t.Errorf("held %v", held)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return !errors.Is(err, os.ErrNotExist) }

func TestParseInterspersed(t *testing.T) {
	for _, c := range []struct {
		args, pos []string
		ca        string
		setup     bool
	}{
		{[]string{"192.168.1.20", "--ca", "x.bin"}, []string{"192.168.1.20"}, "x.bin", false},
		{[]string{"--setup-only", "192.168.1.20", "--ca", "x.bin"}, []string{"192.168.1.20"}, "x.bin", true},
		{[]string{"192.168.1.20"}, []string{"192.168.1.20"}, "", false},
		{[]string{"--ca", "x.bin", "--", "--setup-only"}, []string{"--setup-only"}, "x.bin", false},
		{[]string{"a", "--setup-only", "b"}, []string{"a", "b"}, "", true},
		{nil, nil, "", false},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		ca := fs.String("ca", "", "")
		setupOnly := fs.Bool("setup-only", false, "")
		pos, err := parseInterspersed(fs, c.args)
		if err != nil || !slices.Equal(pos, c.pos) || *ca != c.ca || *setupOnly != c.setup {
			t.Errorf("%q: %q ca %q setup %v, %v", c.args, pos, *ca, *setupOnly, err)
		}
	}
}
