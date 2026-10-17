package launcher

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/tunnel"
)

// thisPCFront is the tunnel address of This PC's test: its own server then runs on ThisPCAddress, 127.0.9.11. No lane
// uses either.
const thisPCFront = "127.0.9.70"

// holdRunLock writes the setup folder's run lock as held by another running process (the test's parent, go test),
// as a Start-Server of that folder holds it; the test's end removes it.
func holdRunLock(t *testing.T, l setup.Layout, r setup.RunLock) {
	t.Helper()
	if r.Dial == "" {
		r.Dial = r.Address
	}
	text := "pid=" + itoa(os.Getppid()) + "\nprogram=" + r.Program + "\naddress=" + r.Address + "\ndial=" + r.Dial +
		"\ngateway_port=" + itoa(r.GatewayPort) + "\nstate=" + r.State + "\npin=" + r.Pin + "\n"
	path := filepath.Join(l.Root, setup.RunLockFile)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := setup.ReadRunLock(l.Root); !ok {
		t.Fatalf("the parent process (%d) doesn't hold the run lock", os.Getppid())
	}
	t.Cleanup(func() { os.Remove(path) })
}

func itoa(n int) string { return strconv.Itoa(n) }

// syncBuffer is a console several goroutines write to.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestThisPCAddress(t *testing.T) {
	for in, want := range map[string]string{"127.0.0.1": "127.0.1.11", "127.0.0.2": "127.0.2.11", "127.0.0.4": "127.0.4.11",
		"127.0.9.70": "127.0.9.11", "127.0.9.11": "127.0.9.12", "bad": "127.0.1.11"} {
		if got := ThisPCAddress(in); got != want {
			t.Errorf("ThisPCAddress(%s) = %s, want %s", in, got, want)
		}
	}
	// never an address the front, a Start-Server on 127.0.0.N (its hosts) or a lane's server machines use
	for n := 1; n <= 4; n++ {
		lane := "127.0.0." + itoa(n)
		got := ThisPCAddress(lane)
		for _, used := range []string{lane, HostsAddress(lane), "127.0." + itoa(n) + ".8", "127.0." + itoa(n) + ".9", "127.0." + itoa(n) + ".2"} {
			if got == used {
				t.Errorf("ThisPCAddress(%s) = %s, an address in use", lane, got)
			}
		}
	}
}

// The list's fixed entry: first, found by tag and name, never removed or taken by an add, its last use kept in the
// file without an entry in servers; a hand-written server with its tag gets another.
func TestBookmarksThisPC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers", BookmarksFile)
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"servers":[{"tag":"pc","address":"192.0.2.5"},{"tag":"a","address":"192.0.2.6"}]}`), 0o644)
	bm, err := LoadBookmarks(path)
	if err != nil {
		t.Fatal(err)
	}
	if l := bm.List(); len(l) != 2 || l[0].Tag != "b" || l[0].Address != "192.0.2.5" {
		t.Fatalf("a server tagged pc by hand: %+v", l)
	}
	if b, ok := bm.Last(); !ok || b.Tag != "b" {
		t.Errorf("Last without This PC: %+v", b)
	}
	bm.SetThisPC("127.0.1.11", 0)
	l := bm.List()
	if len(l) != 3 || l[0].Tag != ThisPCTag || l[0].Name != ThisPCName || l[0].Gateway() != "127.0.1.11:42128" {
		t.Fatalf("This PC not first: %+v", l)
	}
	for _, key := range []string{"pc", "PC", "this pc", "127.0.1.11"} {
		if b, ok := bm.Find(key); !ok || b.Tag != ThisPCTag {
			t.Errorf("Find(%q) = %+v %v", key, b, ok)
		}
	}
	if b, ok := bm.Last(); !ok || b.Tag != ThisPCTag {
		t.Errorf("nothing used yet: Last = %+v, want This PC", b)
	}
	if _, err := bm.Remove("pc"); err != ErrFixed {
		t.Errorf("Remove(pc): %v", err)
	}
	if _, err := bm.Add(Bookmark{Tag: "pc", Address: "192.0.2.7"}); err == nil {
		t.Error("an add with This PC's tag")
	}
	if b, err := bm.Add(Bookmark{Address: "192.0.2.8"}); err != nil || b.Tag != "c" {
		t.Errorf("an add: %+v %v", b, err)
	}
	if err := bm.Used("a", time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := bm.Used(ThisPCTag, time.Date(2026, 10, 15, 11, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if b, _ := bm.Last(); b.Tag != ThisPCTag {
		t.Errorf("This PC used last: Last = %+v", b)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), `"tag": "pc"`) || !strings.Contains(string(raw), `"this_pc_last_used": "2026-10-15T11:00:00Z"`) {
		t.Errorf("the file:\n%s", raw)
	}
	// moved (a Start-Server of the folder runs); read again: the last use comes back with the entry
	bm.SetThisPC("127.0.0.1", 42128)
	if b, _ := bm.Find("pc"); b.Address != "127.0.0.1" || b.LastUsed == "" {
		t.Errorf("moved: %+v", b)
	}
	again, err := LoadBookmarks(path)
	if err != nil || len(again.List()) != 3 {
		t.Fatalf("again: %v %+v", err, again.List())
	}
	again.SetThisPC("127.0.1.11", 0)
	if b, _ := again.Last(); b.Tag != ThisPCTag {
		t.Errorf("again: Last = %+v", b)
	}
}

// This PC's dgserver flags: the folder's Start-Server.ini's game options and its certificate set (one host exe patch
// for both), its own address for every listener, the tunnel without plain listeners, no website.
func TestThisPCFlags(t *testing.T) {
	l := setup.Layout{Root: t.TempDir()}
	bm := &Bookmarks{path: BookmarksPath(l)}
	pc := newThisPC(IO{}, l, "127.0.0.1", "host", nil, bm, true, false)
	f := strings.Join(pc.flags(), " ")
	for _, want := range []string{"--certs " + l.Certs("127.0.1.11"), "--shaper-select", "--signup open", "--log-days 30",
		"--bind 127.0.1.11 --public-host 127.0.1.11 --gms-control-bind 127.0.1.11 --web-port 0 --tunnel on --gateway-port 42128 --plain off --gms-bind 127.0.1.11"} {
		if !strings.Contains(f, want) {
			t.Errorf("no Start-Server.ini: flags lack %q:\n%s", want, f)
		}
	}
	if strings.Contains(f, "--local-trust") || pc.signupMode() != signupOpen {
		t.Errorf("without local trust: %s, mode %q", f, pc.signupMode())
	}
	ini := Config{"address": "192.168.1.20", "shaper_select": "off", "signup": "invite", "server_flags": "--owned starter --web-port 443"}
	pc = newThisPC(IO{}, l, "127.0.0.1", "host", ini, bm, true, false)
	if pc.signupMode() != signupInvite {
		t.Errorf("the folder's Start-Server.ini's mode: %q", pc.signupMode())
	}
	// local trust (Start-Client.ini local_trust = on, the default): --local-trust, the list's mode local
	pc = newThisPC(IO{}, l, "127.0.0.1", "host", ini, bm, true, true)
	if f := strings.Join(pc.flags(), " "); !strings.Contains(f, "--local-trust --bind 127.0.1.11") || pc.signupMode() != signupLocal {
		t.Errorf("local trust: %s, mode %q", f, pc.signupMode())
	}
	pc = newThisPC(IO{}, l, "127.0.0.1", "host", ini, bm, true, false)
	f = strings.Join(pc.flags(), " ")
	if strings.Contains(f, "--shaper-select") || !strings.Contains(f, "--certs "+l.Certs("192.168.1.20")) || !strings.Contains(f, "--signup invite") ||
		!strings.Contains(f, "--owned starter --web-port 443 --bind 127.0.1.11") || !strings.HasSuffix(f, "--gms-bind 127.0.1.11") {
		t.Errorf("with the folder's Start-Server.ini: %s", f)
	}
}

func TestRunLockStates(t *testing.T) {
	l := setup.Layout{Root: t.TempDir()}
	pc := &thisPC{l: l, address: "127.0.9.11", gwPort: 42128, bm: &Bookmarks{path: BookmarksPath(l)}, pct: -1}
	pc.refresh()
	if st, _ := pc.status(); st != thisPCLocal {
		t.Errorf("nothing runs: %q", st)
	}
	if b, _ := pc.bm.ThisPC(); b.Gateway() != "127.0.9.11:42128" {
		t.Errorf("This PC: %+v", b)
	}
	// the folder's Start-Server starting, then running: This PC is that one, its key trusted from the lock
	pin := tunnel.Pin{}
	for i := range pin {
		pin[i] = byte(i)
	}
	holdRunLock(t, l, setup.RunLock{Program: "Start-Server", Address: "203.0.113.7", Dial: "192.168.1.20", GatewayPort: 42200, State: setup.RunSetup})
	pc.refresh()
	if st, _ := pc.status(); st != thisPCStarting {
		t.Errorf("a Start-Server starting: %q", st)
	}
	if b, _ := pc.bm.ThisPC(); b.Gateway() != "192.168.1.20:42200" {
		t.Errorf("This PC beside a Start-Server: %+v", b)
	}
	if _, err := pc.Ensure(300 * time.Millisecond); err == nil || asFrontError(err).code != FrontErrThisPCStarting {
		t.Errorf("Ensure while it starts: %v", err)
	}
	holdRunLock(t, l, setup.RunLock{Program: "Start-Server", Address: "203.0.113.7", Dial: "192.168.1.20", GatewayPort: 42200,
		State: setup.RunRunning, Pin: pin.String()})
	if b, err := pc.Ensure(time.Second); err != nil || b.Gateway() != "192.168.1.20:42200" {
		t.Errorf("Ensure beside a running Start-Server: %+v %v", b, err)
	}
	if st, _ := pc.status(); st != "" {
		t.Errorf("a running Start-Server: %q", st)
	}
	if raw, _ := os.ReadFile(TrustedGateway(l, "192.168.1.20")); strings.TrimSpace(string(raw)) != pin.String() {
		t.Errorf("its key: %q", raw)
	}
	// a Start-Server without the tunnel: This PC can't reach it
	holdRunLock(t, l, setup.RunLock{Program: "Start-Server", Address: "127.0.0.1", State: setup.RunRunning})
	if _, err := pc.Ensure(time.Second); err == nil || asFrontError(err).code != FrontErrThisPCFailed {
		t.Errorf("Ensure beside a Start-Server without the tunnel: %v", err)
	}
}

// This PC end to end on a release folder (the synthetic game): the list's first line, not removable; the first login
// sets the match host up and starts Start-Client's own server (a login that waits too short is refused and the setup
// goes on), the next login reaches it with no key to confirm; a Start-Server on the folder refuses meanwhile; Close
// stops it and frees the folder.
func TestThisPCFront(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.70's and 127.0.9.11's ports")
	}
	dir := release(t)
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		t.Fatal(err)
	}
	var console syncBuffer
	pc := newThisPC(IO{Out: &console}, l, thisPCFront, filepath.Join(dir, "host"), nil, bm, false, true)
	pc.findZip = func() (string, error) { return filepath.Join(dir, "Dawngate.zip"), nil }
	defer pc.Close()
	_, tlsCfg, err := tunnelCertSet(l, thisPCFront, false)
	if err != nil {
		t.Fatal(err)
	}
	logFront := t.TempDir()
	lf, err := os.Create(filepath.Join(logFront, "front.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	f := &Front{Log: logging.New(lf), Layout: l, Bookmarks: bm, TunnelIP: net.ParseIP(thisPCFront), RedirectorTLS: tlsCfg,
		ConnectTimeout: 3 * time.Second, ThisPC: pc, ThisPCWait: time.Millisecond, ListRefresh: -1}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if list := string(f.ServerList()); !strings.Contains(list, "preselect\t\nlast\t\nserver\tpc\tThis PC\t127.0.9.11\t42128\tlocal\t0\t\t\tfixed\t\t\tlocal\t0\nend\n") {
		t.Errorf("the list before:\n%s", list)
	}
	g := fakeGame{t: t, ip: thisPCFront}
	g.redirect()
	c := g.dial(DefaultFrontPorts.Blaze)
	defer c.Close()
	g.start(c)
	if rep := g.call(c, 1, 0x28, 2, loginBody("tpc1#-pc")); rep.Type != fire.ErrorReply || rep.Error != FrontErrFixed {
		t.Errorf("remove This PC: %v %#x", rep.Type, rep.Error)
	}
	// a login that waits only a moment: refused while the setup goes on
	if rep := g.call(c, 1, 0x28, 3, loginBody("tpc1#pc")); rep.Type != fire.ErrorReply || rep.Error != FrontErrThisPCStarting {
		t.Fatalf("the first login: %v %#x\n%s", rep.Type, rep.Error, readLogs(t, logFront))
	}
	sawSetup := false
	for deadline := time.Now().Add(60 * time.Second); ; {
		st, _ := pc.status()
		sawSetup = sawSetup || st == thisPCSetup || st == thisPCStarting
		if st == "" {
			break
		}
		if st == thisPCFailed || time.Now().After(deadline) {
			t.Fatalf("This PC: %q\n%s", st, console.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sawSetup {
		t.Error("no setup or start state seen")
	}
	// the running line follows the state
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(console.String(), "the server runs on") && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	out := console.String()
	for _, want := range []string{"This PC  starting the server on this PC (127.0.9.11)", "[done]     host copy", "[done]     host DLL",
		"[skipped]  client copy", "This PC  the server runs on 127.0.9.11"} {
		if !strings.Contains(out, want) {
			t.Errorf("the console lacks %q:\n%s", want, out)
		}
	}
	held, err := os.ReadFile(filepath.Join(l.Root, setup.RunLockFile))
	if err != nil || !strings.Contains(string(held), "program=Start-Client\n") || !strings.Contains(string(held), "state=running\n") {
		t.Errorf("the run lock: %q %v", held, err)
	}
	// the next login goes through, its key trusted from the folder (no confirmation); a request the client sent
	// before it (as BGEcommerce 0x06 after a kick) is held by the front and answered by the server after the login
	f.ThisPCWait = 20 * time.Second
	g.send(c, 0x820, 0x06, 4, nil)
	g.send(c, 1, 0x28, 5, loginBody("tpc1#pc"))
	c.SetReadDeadline(time.Now().Add(15 * time.Second))
	r := bufio.NewReader(c)
	got := map[uint32]fire.Frame{}
	for len(got) < 2 {
		_, fr, err := readFrame(r)
		if err != nil {
			t.Fatalf("the login to This PC and the held request: %v (got %v)\n%s", err, got, readLogs(t, logFront))
		}
		if fr.Type == fire.Reply || fr.Type == fire.ErrorReply {
			got[fr.ID] = fr
		}
	}
	c.SetReadDeadline(time.Time{})
	if got[5].Type != fire.Reply || got[5].Command != 0x28 || got[4].Component != 0x820 {
		t.Fatalf("the login to This PC: %+v; the held request: %+v\n%s", got[5], got[4], readLogs(t, logFront))
	}
	waitLog(t, logFront, "1 request(s) held since before the login passed to pc", 1)
	waitLog(t, l.Logs(), "    MAIL: 'tpc1'\n", 1)
	if strings.Contains(readLogs(t, logFront), "isn't trusted yet") {
		t.Error("This PC's key needed a confirmation")
	}
	// no password (local trust): the account made without one; the front's own sign-up page for This PC
	waitLog(t, l.Logs(), `created for "tpc1" (This PC: no password)`, 1)
	resp, err := http.Get("http://" + net.JoinHostPort(thisPCFront, strconv.Itoa(DefaultFrontPorts.HTTP)) + "/dawngate/pick/0/signup")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "No sign-up needed on This PC") {
		t.Errorf("This PC's sign-up page: %s", page)
	}
	if list := string(f.ServerList()); !strings.Contains(list, "server\tpc\tThis PC\t127.0.9.11\t42128\tok\t") || !strings.Contains(list, "\tfixed\t"+f.Version+"\t\tlocal\t0\n") {
		t.Errorf("the list while it runs:\n%s", list)
	}
	if b, _ := bm.Last(); b.Tag != ThisPCTag {
		t.Errorf("Last: %+v", b)
	}
	// its server's log only in its file, not on Start-Client's console
	if strings.Contains(console.String(), "[blaze]") {
		t.Errorf("the server's log on the console:\n%s", console.String())
	}
	f.Close()
	pc.Close()
	if _, err := os.Stat(filepath.Join(l.Root, setup.RunLockFile)); !os.IsNotExist(err) {
		t.Errorf("the run lock stayed: %v", err)
	}
	if held := HeldPorts("127.0.9.11", []Port{{42128, false, ""}, {42128, true, ""}, {10040, false, ""}}); len(held) > 0 {
		t.Errorf("This PC's server still holds %v", held)
	}
	// a Start-Server on the same folder refuses while a Start-Client runs its server (another process: the lock of this
	// one's own pid is taken over, as one left behind)
	holdRunLock(t, l, setup.RunLock{Program: "Start-Client", Address: "127.0.9.11", GatewayPort: 42128, State: setup.RunRunning})
	if code, out := run(t, RunServer, dir, "--non-interactive", "--setup-only"); code != 1 || !strings.Contains(out, "already runs inside Start-Client.exe") {
		t.Errorf("Start-Server beside This PC: exit %d\n%s", code, out)
	}
}
