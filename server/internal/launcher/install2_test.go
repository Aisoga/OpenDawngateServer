package launcher

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/setup"
	"dawngate/server/internal/tunnel"
)

// Start-Client.ini's gateway_pin with the picker (the default): the first start puts the ini's server into the new
// list, preselects it and trusts the ini's key; the same key again changes nothing; a key that differs from the one
// trusted before is not taken; a malformed key, or a key without the server, is refused.
func TestIniGatewayPinPicker(t *testing.T) {
	dir := release(t)
	pin := strings.Repeat("cd", 32)
	p, _ := tunnel.ParsePin(pin)
	ini := filepath.Join(dir, ClientConfigName)
	os.WriteFile(ini, []byte("server = 203.0.113.12\r\ngateway_pin = "+pin+"\r\n"), 0o644)
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	store := TrustedGateway(l, "203.0.113.12")

	// first use: listed, preselected, trusted (no fingerprint stop at the first login)
	code, out := run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal)
	if code != 0 || strings.Contains(out, "unknown option") ||
		!strings.Contains(out, "203.0.113.12:42128 added to the server list as a (Start-Client.ini's server)") ||
		!strings.Contains(out, "203.0.113.12's tunnel key "+p.Fingerprint()+" trusted ("+iniPinSource+")") {
		t.Fatalf("first use: exit %d\n%s", code, out)
	}
	if raw, _ := os.ReadFile(store); strings.TrimSpace(string(raw)) != pin {
		t.Fatalf("the ini's key not stored: %q", raw)
	}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		t.Fatal(err)
	}
	c := serverChoice{picker: true, bm: bm, ini: Config{"server": "203.0.113.12", GatewayPinKey: pin},
		add: []listAdd{{b: Bookmark{Address: "203.0.113.12", GatewayPort: 42128}, why: "Start-Client.ini's server", onlyFirst: true}}}
	// the ini's server is preselected while no server of the list was used (also at a second start before a
	// login), then the list's last one decides
	var sb strings.Builder
	pre, err := pickerBookmarks(IO{Out: &sb}, l, c, "", false, nil)
	if b, ok := bm.Find(pre); err != nil || !ok || b.Address != "203.0.113.12" {
		t.Errorf("the ini's server not preselected: %q %v\n%s", pre, err, sb.String())
	}
	if err := bm.Used(pre, time.Now()); err != nil {
		t.Fatal(err)
	}
	if b, err := bm.Add(Bookmark{Address: "198.51.100.3", GatewayPort: 42128}); err != nil {
		t.Fatal(err)
	} else if err := bm.Used(b.Tag, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if pre, err := pickerBookmarks(IO{Out: &sb}, l, c, "", false, nil); err != nil || pre != "" {
		t.Errorf("preselected %q after a login used another server (%v)", pre, err)
	}

	// the same key again: nothing to say, nothing changed
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal)
	if code != 0 || strings.Contains(out, "trusted (") || strings.Contains(out, "not taken") {
		t.Errorf("the same key again: exit %d\n%s", code, out)
	}

	// another key trusted before (the player confirmed the server's new key): the ini's key is not taken
	other := strings.Repeat("ef", 32)
	op, _ := tunnel.ParsePin(other)
	os.WriteFile(store, []byte(other+"\n"), 0o644)
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal)
	if code != 0 || !strings.Contains(out, "isn't the key trusted for 203.0.113.12 before ("+op.Fingerprint()+"): not taken") {
		t.Errorf("a different trusted key: exit %d\n%s", code, out)
	}
	if raw, _ := os.ReadFile(store); strings.TrimSpace(string(raw)) != other {
		t.Errorf("the trusted key was replaced: %q", raw)
	}

	// a malformed key; a key without its server
	os.WriteFile(ini, []byte("server = 203.0.113.12\r\ngateway_pin = 12ab\r\n"), 0o644)
	if code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal); code != 1 ||
		!strings.Contains(out, "gateway_pin = 12ab: a gateway pin is 64 hex digits") {
		t.Errorf("a malformed key: exit %d\n%s", code, out)
	}
	os.WriteFile(ini, []byte("gateway_pin = "+pin+"\r\n"), 0o644)
	if code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal); code != 1 ||
		!strings.Contains(out, "set server = to that server's address") {
		t.Errorf("a key without its server: exit %d\n%s", code, out)
	}
}

// Start-Client.ini's gateway_pin with one server (picker = off): the gateway's key is trusted at the first start
// without a question; a key that differs from the one trusted before is not taken (the changed-key refusal stays);
// a key the server doesn't present is refused.
func TestIniGatewayPinOneServer(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.120's server ports")
	}
	const server, local = "127.0.9.120", "127.0.9.121"
	dir := release(t)
	port, fp := serveTunnelOn(t, server)
	served, err := tunnel.ProbePin(net.JoinHostPort(server, strconv.Itoa(port)), 5*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ini := filepath.Join(dir, ClientConfigName)
	writeIni := func(pin string) {
		os.WriteFile(ini, []byte("server = "+server+"\r\npicker = off\r\ntunnel = on\r\ngateway_port = "+strconv.Itoa(port)+
			"\r\ngateway_pin = "+pin+"\r\n"), 0o644)
	}
	args := []string{"--non-interactive", "--setup-only", "--tunnel-address", local}
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	store := TrustedGateway(l, server)

	// a key the server doesn't present
	wrong := strings.Repeat("ab", 32)
	writeIni(wrong)
	if code, out := run(t, RunClient, dir, args...); code != 1 || !strings.Contains(out, "not the one given with "+iniPinSource) || exists(store) {
		t.Fatalf("a wrong key: exit %d\n%s", code, out)
	}
	// first use: trusted without a question (--non-interactive would refuse an unconfirmed key)
	writeIni(served.String())
	if code, out := run(t, RunClient, dir, args...); code != 0 || !strings.Contains(out, "trusting the server's tunnel key "+fp) {
		t.Fatalf("first use: exit %d\n%s", code, out)
	}
	// the same key again
	if code, out := run(t, RunClient, dir, args...); code != 0 || strings.Contains(out, "trusting") {
		t.Errorf("the same key again: exit %d\n%s", code, out)
	}
	// another key trusted before: the ini's key is not taken, the changed key is refused as without it
	other := strings.Repeat("ef", 32)
	op, _ := tunnel.ParsePin(other)
	os.WriteFile(store, []byte(other+"\n"), 0o644)
	code, out := run(t, RunClient, dir, args...)
	if code != 1 || !strings.Contains(out, "not taken") ||
		!strings.Contains(out, "has another tunnel key ("+fp+") than the one trusted before ("+op.Fingerprint()+")") {
		t.Errorf("a different trusted key: exit %d\n%s", code, out)
	}
	if raw, _ := os.ReadFile(store); strings.TrimSpace(string(raw)) != other {
		t.Errorf("the trusted key was replaced: %q", raw)
	}
}
