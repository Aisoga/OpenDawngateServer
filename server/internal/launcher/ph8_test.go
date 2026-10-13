package launcher

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup"
	"dawngate/server/internal/store"
)

// the profiles and what the public one requires.
func TestProfileRules(t *testing.T) {
	for _, c := range []struct {
		v, want string
		bad     bool
	}{{"", ProfileAuto, false}, {"Public", ProfilePublic, false}, {" home ", ProfileHome, false}, {"lan", "", true}} {
		got, err := profileMode(c.v)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("profileMode(%q) = %q, %v", c.v, got, err)
		}
	}
	for _, c := range []struct {
		mode, address string
		want          bool
	}{
		{ProfileAuto, "127.0.0.1", false}, {ProfileAuto, "192.168.1.20", false}, {ProfileAuto, "8.8.8.8", true},
		{ProfilePublic, "192.168.1.20", true}, {ProfilePublic, "127.0.9.1", true}, {ProfileHome, "192.168.1.20", false},
	} {
		if got := publicProfile(c.mode, c.address); got != c.want {
			t.Errorf("publicProfile(%s, %s) = %v", c.mode, c.address, got)
		}
	}
	if err := checkProfile(ProfileHome, "8.8.8.8", TunnelAuto); err == nil || !strings.Contains(err.Error(), "profile = public") {
		t.Errorf("home on a public address: %v", err)
	}
	for _, c := range []struct{ mode, address string }{{ProfileAuto, "8.8.8.8"}, {ProfilePublic, "192.168.1.20"}} {
		if err := checkProfile(c.mode, c.address, TunnelOff); err == nil || !strings.Contains(err.Error(), "needs the encrypted tunnel") {
			t.Errorf("%s on %s without the tunnel: %v", c.mode, c.address, err)
		}
		if err := checkProfile(c.mode, c.address, TunnelAuto); err != nil {
			t.Errorf("%s on %s: %v", c.mode, c.address, err)
		}
	}
	if err := checkProfile(ProfileAuto, "192.168.1.20", TunnelOff); err != nil {
		t.Errorf("a home LAN server without the tunnel: %v", err)
	}
}

func stubLAN(t *testing.T, list ...LANAddress) {
	orig := lanAddresses
	lanAddresses = func() []LANAddress { return list }
	t.Cleanup(func() { lanAddresses = orig })
}

func askProfile(t *testing.T, answers string) (string, Config, string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ServerConfigName)
	if _, _, err := LoadOrCreate(path, ServerConfigText); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, DefaultFolder, "data")
	var out bytes.Buffer
	got, err := AskServerProfile(bufio.NewReader(strings.NewReader(answers)), &out, path, data)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Unknown(serverKeys...)) > 0 {
		t.Errorf("unknown keys written: %v", cfg.Unknown(serverKeys...))
	}
	return got, cfg, out.String(), data
}

// The first start's questions: Enter keeps a home server on this PC; 2 takes a home-network address; 3 is the public
// profile with its questions (address, router, sign-up, domain, admin, the risk).
func TestAskServerProfile(t *testing.T) {
	stubLAN(t, LANAddress{IP: "192.168.20.40", Interface: "Ethernet", Route: true}, LANAddress{IP: "192.168.56.1", Interface: "VirtualBox"})

	got, cfg, out, _ := askProfile(t, "\n")
	if got != ProfileHome || cfg["address"] != "127.0.0.1" || cfg["profile"] != ProfileAuto || !strings.Contains(out, "only this PC") {
		t.Errorf("Enter: %s %v\n%s", got, cfg, out)
	}
	got, cfg, out, _ = askProfile(t, "2\n2\n")
	if got != ProfileHome || cfg["address"] != "192.168.56.1" || cfg["signup"] != "open" || !strings.Contains(out, "(the one your router connects)") {
		t.Errorf("home network: %s %v\n%s", got, cfg, out)
	}
	got, cfg, _, _ = askProfile(t, "x\n2\n\n") // a wrong answer, then 2, then Enter = the route's address
	if got != ProfileHome || cfg["address"] != "192.168.20.40" {
		t.Errorf("home network, default: %s %v", got, cfg)
	}

	// the public profile: 10.1.2.3 isn't public, 8.8.8.8 is (and not this PC's: behind a router), the router forwards
	// to the first LAN address, invite (Enter), a domain, no e-mail, an admin, the risk accepted
	got, cfg, out, data := askProfile(t, "3\n10.1.2.3\n8.8.8.8\n1\n\nPlay.Example.org\n\nBoss1\nyes\n")
	want := Config{"profile": ProfilePublic, "address": "8.8.8.8", "bind": "192.168.20.40", "tunnel": TunnelAuto,
		"signup": "invite", "web_domain": "play.example.org", RiskAckKey: "yes"}
	for k, v := range want {
		if cfg[k] != v {
			t.Errorf("public: %s = %q, want %q", k, cfg[k], v)
		}
	}
	for _, s := range []string{"no public internet address", "behind a router", "A PUBLIC SERVER: 8.8.8.8",
		"only TCP and UDP 42128 (the tunnel) and TCP 443 (the website, for Let's Encrypt too)", "Saved in"} {
		if !strings.Contains(out, s) {
			t.Errorf("public: no %q in\n%s", s, out)
		}
	}
	if strings.Contains(out, "holds the public address itself") {
		t.Error("public behind a router: the no-router note")
	}
	if got != ProfilePublic {
		t.Errorf("public: %s", got)
	}
	st, err := store.Open(filepath.Join(data, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	admins, _ := st.Admins()
	st.Close()
	if len(admins) != 1 || !strings.EqualFold(admins[0].Name, "boss1") {
		t.Errorf("admins %+v", admins)
	}

	// closed, no domain, no admin, the risk declined
	_, cfg, out, _ = askProfile(t, "3\n8.8.4.4\n\n2\n\n\nno\n")
	if cfg["signup"] != "closed" || cfg["web_domain"] != "" || cfg[RiskAckKey] != "no" || cfg["profile"] != ProfilePublic {
		t.Errorf("public, closed: %v\n%s", cfg, out)
	}
	if _, err := AskServerProfile(bufio.NewReader(strings.NewReader("7\n9\n0\n")), &bytes.Buffer{}, filepath.Join(t.TempDir(), "x.ini"), ""); err == nil {
		t.Error("three wrong answers accepted")
	}
}

// The risk notice: what the public profile covers and what remains, and the no-router case.
func TestServerRiskNotice(t *testing.T) {
	var b bytes.Buffer
	ServerRiskNotice(&b, RiskInfo{Address: "8.8.8.8", GatewayPort: 42199, Signup: "open", LogDays: 30})
	out := b.String()
	for _, s := range []string{"A PUBLIC SERVER: 8.8.8.8", "TCP and UDP 42199", "anyone who reaches the server can make accounts",
		"attack its players' game", "share the limits", "Data-protection law", "privacy.html", "accounts.db",
		"TCP 10040", "log_days = 30", "holds the public address itself", "Let your firewall in only TCP and UDP 42199"} {
		if !strings.Contains(out, s) {
			t.Errorf("no %q in\n%s", s, out)
		}
	}
	if strings.Contains(out, "Nothing limits") {
		t.Error("the old notice's 'Nothing limits'")
	}
	b.Reset()
	ServerRiskNotice(&b, RiskInfo{Address: "8.8.8.8", Bind: "192.168.1.20", Signup: "invite"})
	if out := b.String(); strings.Contains(out, "holds the public address itself") || !strings.Contains(out, "invite code") ||
		!strings.Contains(out, "TCP and UDP 42128") {
		t.Errorf("behind a router, invite:\n%s", out)
	}
}

// Start-Server with the public profile on a (loopback) LAN-like address: the tunnel and the limits on without being
// asked, the banner says so; tunnel = off is refused; the first start in a console asks the questions.
func TestStartServerPublicProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.1's server ports")
	}
	dir := release(t)
	cfg := filepath.Join(dir, ServerConfigName)
	// the first start in a console: the questions (1 = this PC), then the setup (--setup-only)
	var out bytes.Buffer
	o := IO{In: strings.NewReader("1\n"), Out: &out, ExeDir: dir, Interactive: true}
	if code := RunServer([]string{"--setup-only"}, o); code != 0 || !strings.Contains(out.String(), "Who will play on this server?") {
		t.Fatalf("first start: exit %d\n%s", code, out.String())
	}
	// a second start doesn't ask again
	out.Reset()
	o.In = strings.NewReader("")
	if code := RunServer([]string{"--setup-only"}, o); code != 0 || strings.Contains(out.String(), "Who will play") {
		t.Errorf("second start: exit %d\n%s", code, out.String())
	}
	// --configure without a console
	if code, out := run(t, RunServer, dir, "--configure", "--non-interactive"); code != 1 || !strings.Contains(out, "--configure asks questions") {
		t.Errorf("--configure --non-interactive: exit %d\n%s", code, out)
	}
	SetKey(cfg, "address", testAddress)
	SetKey(cfg, "profile", ProfilePublic)
	SetKey(cfg, "tunnel", TunnelOff)
	if code, out := run(t, RunServer, dir, "--non-interactive", "--seconds", "1"); code != 1 || !strings.Contains(out, "needs the encrypted tunnel") {
		t.Errorf("public profile, tunnel off: exit %d\n%s", code, out)
	}
	SetKey(cfg, "tunnel", TunnelAuto)
	SetKey(cfg, "signup", "invite")
	SetKey(cfg, "gateway_port", "42197")
	SetKey(cfg, "web_port", "0")
	code, out2 := run(t, RunServer, dir, "--non-interactive", "--seconds", "1")
	if code != 0 {
		t.Fatalf("public profile: exit %d\n%s", code, out2)
	}
	for _, s := range []string{"the public profile: the tunnel, the limits, sign-up invite", "The encrypted tunnel listens on " + testAddress + " port 42197",
		"--tunnel on", "The public profile: players only through the tunnel, the limits on", "invite code"} {
		if !strings.Contains(out2, s) {
			t.Errorf("no %q in\n%s", s, out2)
		}
	}
	if strings.Contains(out2, "A PUBLIC SERVER") {
		t.Error("the risk notice on a non-public address")
	}
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	logs, _ := filepath.Glob(filepath.Join(l.Logs(), "*.log"))
	var all string
	for _, f := range logs {
		b, _ := os.ReadFile(f)
		all += string(b)
	}
	if !strings.Contains(all, "limits on") || !strings.Contains(all, "tunnel gateway on") {
		t.Errorf("the server log has no limits or gateway line:\n%s", all)
	}
}
