package launcher

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/dgserver"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/archive"
	"dawngate/server/internal/setup/hostcopy"
	"dawngate/server/internal/tunnel"
)

// "This PC" is the server list's fixed first entry (bookmarks.go): the server on this PC, so that a player needs
// nothing but Start-Client.exe and Dawngate.zip. Picking it and logging in reaches, in this order:
//   - the server this setup folder's Start-Server.exe runs, while it runs (the folder's run lock, setup.RunLock, names
//     its address and key: no confirmation, the key is in the folder);
//   - else a server Start-Client runs in its own process, as Start-Server does in its: dgserver with the tunnel
//     (--plain off: no plain listener) on ThisPCAddress, its match hosts there too, the accounts in the same
//     Dawngate-Data\data. It starts at the first login to This PC (not at Start-Client's start: a player who only
//     plays on other servers pays nothing for it), and its first start sets up the match-host copy (about half a
//     minute and 1.5 GB) while the login waits; the list shows the setup's progress and the console its steps. It
//     takes the folder's run lock, so a Start-Server on the same folder refuses to start while it runs, and it ends
//     with Start-Client (its match hosts with it: they are in dgserver's job object).

// ThisPCAddress is the loopback address Start-Client runs the server for This PC on, from its tunnel address:
// 127.0.N.11 for 127.0.0.N (a player's 127.0.0.1: 127.0.1.11), else the tunnel address's /24 with .11 (.12 when that is
// the tunnel address). Its gateway (42128), control port and match hosts all bind it, so it shares no port with the
// front (the game's ports on the tunnel address), with a Start-Server on 127.0.0.N (its gateway there, its hosts on
// HostsAddress, 127.0.N.10) or with the test lanes' server machines (127.0.N.8, .9).
func ThisPCAddress(tunnelIP string) string {
	ip := net.ParseIP(tunnelIP).To4()
	if ip == nil {
		ip = net.IPv4(127, 0, 0, 1).To4()
	}
	if ip[1] == 0 && ip[2] == 0 {
		return fmt.Sprintf("%d.0.%d.11", ip[0], ip[3])
	}
	last := 11
	if ip[3] == 11 {
		last = 12
	}
	return fmt.Sprintf("%d.%d.%d.%d", ip[0], ip[1], ip[2], last)
}

// ThisPCWait is how long a login to This PC waits for its server (the setup at its first start, the start itself):
// the client gives a login 80 s, and the front's connect and the replayed requests need a little of that. A setup that
// takes longer goes on; the login is refused with FrontErrThisPCStarting and the list shows the progress.
const ThisPCWait = 50 * time.Second

// This PC's states in the login screen's server list (the status field; the picker shows its own words for them).
const (
	thisPCLocal    = "local"    // not running: Start-Client starts it at the login
	thisPCSetup    = "setup"    // its setup runs (the ping field: the percentage)
	thisPCStarting = "starting" // the server starts (or this folder's Start-Server is starting)
	thisPCFailed   = "failed"   // its setup or start failed (Start-Client's console says why); the next login tries again
	thisPCRunning  = "running"  // internal: it answers (the list probes it as any server)
)

// thisPC runs This PC's server for Start-Client (newThisPC; Ensure, status, Close).
type thisPC struct {
	out     io.Writer // Start-Client's console
	live    bool
	l       setup.Layout
	address string // where Start-Client's own server runs (ThisPCAddress)
	// certsAddr names its certificate set (certs\<certsAddr>, also the gateway key): this folder's Start-Server's
	// address when there is one, else address
	certsAddr string
	gwPort    int
	hostDir   string // the release's host\ folder: dgsim.dll and lua\
	findZip   func() (string, error)
	// serverIni is this folder's Start-Server.ini (nil: none): its game options (shaper_select, signup,
	// signups_per_address, log_days, server_flags) are This PC's too
	serverIni Config
	bm        *Bookmarks
	// localTrust runs Start-Client's own server without passwords (dgserver --local-trust; Start-Client.ini
	// local_trust): any name logs in with any password. Not for this folder's Start-Server, which checks them.
	localTrust bool
	run        func([]string, dgserver.Hooks) int // dgserver.Run
	logf       func(format string, a ...any)      // the front's log (may be nil)
	changed    func()                             // the list should be written again (the front's wantList)
	check      bool                               // write nothing (Start-Client --check)
	// skipFailedMaps (the map editor) leaves a maps-folder package whose build fails out of the host copy instead of
	// failing This PC (setup.Setup.SkipFailedMaps)
	skipFailedMaps bool

	mu        sync.Mutex
	testMap   string // the map editor's Test: the map every game of the next start hosts (SetMap)
	state     string // "", thisPCSetup, thisPCStarting, thisPCRunning, thisPCFailed
	pct       int    // the setup's progress (-1: none yet)
	failure   string
	done      chan struct{} // closed when the current start has ended (running or failed)
	stop      chan struct{} // closing it stops the server
	exited    chan struct{} // closed when dgserver.Run has returned
	lock      *setup.RunLockHandle
	other     setup.RunLock // the folder's server in another program (otherHeld)
	otherHeld bool
	closed    bool
	info      dgserver.Info
}

// newThisPC prepares This PC for a setup folder and puts it into the list (bm); check changes no file (Start-Client
// --check). Its findZip is set by the caller (nil: the archive isn't found).
// mapsDir is the release's maps folder, beside its host\ folder (MapsDir); "" without a host folder.
func (p *thisPC) mapsDir() string {
	if p.hostDir == "" {
		return ""
	}
	return MapsDir(filepath.Dir(p.hostDir))
}

func newThisPC(o IO, l setup.Layout, tunnelIP, hostDir string, serverIni Config, bm *Bookmarks, check, localTrust bool) *thisPC {
	p := &thisPC{out: o.Out, live: o.Live, l: l, address: ThisPCAddress(tunnelIP), gwPort: tunnel.DefaultPort, hostDir: hostDir,
		serverIni: serverIni, bm: bm, run: dgserver.Run, pct: -1, check: check, localTrust: localTrust}
	p.certsAddr = p.address
	if a := serverIni["address"]; a != "" {
		if _, err := setup.ClientOptions(a); err == nil {
			// the folder's Start-Server's certificate set: the host copy's exe is patched with one CA for both, so
			// neither patches it again after the other ran
			p.certsAddr = a
		}
	}
	p.refresh()
	return p
}

// describe is This PC's line in Start-Client's list of servers.
func (p *thisPC) describe() string {
	p.mu.Lock()
	o, held := p.other, p.otherHeld
	p.mu.Unlock()
	if held {
		state := "running"
		if o.State != setup.RunRunning {
			state = "starting"
		}
		return fmt.Sprintf("%s: this folder's %s (%s, %s)", ThisPCName, o.Program, net.JoinHostPort(o.Dial, strconv.Itoa(o.GatewayPort)), state)
	}
	what := "starts when you log in to it"
	if p.localTrust {
		what += "; without passwords (local_trust): any login name logs in"
	}
	if _, err := os.Stat(filepath.Join(p.l.HostCopy(), "game", "Dawngate.exe")); err != nil {
		what += "; the first time it sets up the match host: about half a minute, 1.5 GB"
	}
	return fmt.Sprintf("%s: the server on this PC, run by Start-Client on %s (it %s)", ThisPCName, p.address, what)
}

// running is the folder's Start-Server while it serves This PC (its lock: the players' address, the gateway port);
// false while Start-Client's own server serves it, which listens on this PC only.
func (p *thisPC) running() (setup.RunLock, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.other, p.otherHeld && p.other.State == setup.RunRunning && p.other.GatewayPort > 0
}

func (p *thisPC) say(format string, a ...any) {
	if p.out != nil {
		fmt.Fprintf(p.out, "  This PC  "+format+"\n", a...)
	}
	if p.logf != nil {
		p.logf("This PC: "+format, a...)
	}
}

func (p *thisPC) notify() {
	if p.changed != nil {
		p.changed()
	}
}

// refresh reads the folder's run lock (a Start-Server of this folder that runs) and points the list's entry at the
// server that serves This PC now: that one, else Start-Client's own. A running Start-Server's key is trusted from the
// lock (it is the folder's: no confirmation).
func (p *thisPC) refresh() {
	held, ok := setup.ReadRunLock(p.l.Root) // never this process's own lock
	p.mu.Lock()
	p.other, p.otherHeld = held, ok
	p.mu.Unlock()
	if ok && held.GatewayPort > 0 {
		p.bm.SetThisPC(held.Dial, held.GatewayPort)
		if held.State == setup.RunRunning && held.Pin != "" {
			p.pin(held.Dial, held.Pin)
		}
		return
	}
	p.bm.SetThisPC(p.address, p.gwPort)
}

// pin stores a key of this folder's server as the one trusted for its address (replacing a stale one: the folder is
// the authority, as pinOwnServer's rule).
func (p *thisPC) pin(address, pinHex string) {
	pin, err := tunnel.ParsePin(pinHex)
	if err != nil || p.check {
		return
	}
	store := TrustedGateway(p.l, address)
	if raw, err := os.ReadFile(store); err == nil {
		if known, err := tunnel.ParsePin(strings.TrimSpace(string(raw))); err == nil && tunnel.SamePin(known, pin) {
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err == nil {
		if err := os.WriteFile(store, []byte(pin.String()+"\n"), 0o644); err == nil {
			p.say("its key %s trusted (the server of this folder)", pin.Fingerprint())
		}
	}
}

// signupMode is This PC's sign-up mode for the list while nothing answers there (the probe tells it once something
// does): local without passwords (Start-Client's own server with localTrust), else the mode of this folder's
// Start-Server.ini (signup; open without one).
func (p *thisPC) signupMode() string {
	p.mu.Lock()
	held := p.otherHeld
	p.mu.Unlock()
	switch {
	case held:
		return "" // this folder's Start-Server: its own mode, told once it answers
	case p.localTrust:
		return signupLocal
	}
	if m, err := accounts.ParseMode(firstNonEmpty(p.serverIni["signup"], signupOpen)); err == nil {
		return m.String()
	}
	return signupOpen
}

// status is This PC's state for the list: "" when a server answers there (the list probes it like any server), else
// thisPCLocal, thisPCSetup (with the percentage), thisPCStarting or thisPCFailed.
func (p *thisPC) status() (string, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.otherHeld {
		if p.other.State == setup.RunRunning || p.other.GatewayPort == 0 {
			return "", 0
		}
		return thisPCStarting, 0
	}
	switch p.state {
	case thisPCRunning:
		return "", 0
	case thisPCSetup:
		return thisPCSetup, max(p.pct, 0)
	case thisPCStarting:
		return thisPCStarting, 0
	case thisPCFailed:
		return thisPCFailed, 0
	}
	return thisPCLocal, 0
}

// Ensure makes This PC's server answer for a login: this folder's Start-Server when it runs, else Start-Client's
// own, started now when it isn't running (its setup first, when needed). It waits up to wait and returns the list's
// entry to connect to, or the login's refusal: FrontErrThisPCStarting while a setup or start goes on, FrontErrThisPCFailed
// when it failed (a later login tries again).
func (p *thisPC) Ensure(wait time.Duration) (Bookmark, error) {
	deadline := time.Now().Add(wait)
	tried := false
	for {
		p.refresh()
		p.mu.Lock()
		if p.otherHeld {
			o := p.other
			p.mu.Unlock()
			if o.GatewayPort == 0 {
				return Bookmark{}, frontErr(FrontErrThisPCFailed, "this folder's server runs in %s (pid %d) without the tunnel "+
					"(tunnel = off): This PC needs it with the tunnel", o.Program, o.Pid)
			}
			if o.State == setup.RunRunning {
				b, _ := p.bm.ThisPC()
				return b, nil
			}
			if time.Now().After(deadline) {
				return Bookmark{}, frontErr(FrontErrThisPCStarting, "this folder's server in %s (pid %d) is still starting", o.Program, o.Pid)
			}
			time.Sleep(250 * time.Millisecond)
			continue
		}
		switch p.state {
		case thisPCRunning:
			p.mu.Unlock()
			b, _ := p.bm.ThisPC()
			return b, nil
		case thisPCFailed:
			if tried {
				failure := p.failure
				p.mu.Unlock()
				return Bookmark{}, frontErr(FrontErrThisPCFailed, "the server on this PC didn't start: %s", failure)
			}
			fallthrough
		case "":
			if p.closed {
				p.mu.Unlock()
				return Bookmark{}, errClosedFront
			}
			tried = true
			p.state, p.pct, p.failure = thisPCSetup, -1, ""
			p.done = make(chan struct{})
			go p.start(p.done)
		}
		done := p.done
		p.mu.Unlock()
		p.notify()
		left := time.Until(deadline)
		if left <= 0 {
			left = time.Millisecond
		}
		t := time.NewTimer(left)
		select {
		case <-done:
			t.Stop()
		case <-t.C:
			st, pct := p.status()
			what := "starting"
			if st == thisPCSetup {
				what = fmt.Sprintf("being set up (%d %%)", pct)
			}
			return Bookmark{}, frontErr(FrontErrThisPCStarting, "the server on this PC is still %s after %s; it goes on", what,
				wait.Round(time.Second))
		}
	}
}

// start sets up and starts Start-Client's own server for This PC (in its own goroutine); done is closed when it
// answers or failed.
func (p *thisPC) start(done chan struct{}) {
	finish := func(state, failure string) {
		p.mu.Lock()
		p.state, p.failure = state, failure
		p.mu.Unlock()
		close(done)
		p.notify()
		if state == thisPCFailed {
			p.say("FAILED: %s", failure)
		}
	}
	lock, held, err := setup.TakeRunLock(p.l.Root, setup.RunLock{Program: "Start-Client", Address: p.address,
		GatewayPort: p.gwPort, State: setup.RunSetup})
	if errors.Is(err, setup.ErrServerRuns) { // a Start-Server of this folder started meanwhile: This PC is that one
		p.mu.Lock()
		p.other, p.otherHeld = held, true
		p.mu.Unlock()
		finish("", "")
		return
	}
	if err != nil {
		finish(thisPCFailed, fmt.Sprintf("the folder's server lock: %v", err))
		return
	}
	p.mu.Lock()
	p.lock = lock
	p.mu.Unlock()
	fail := func(failure string) {
		lock.Release()
		finish(thisPCFailed, failure)
	}
	if _, err := os.Stat(filepath.Join(p.hostDir, "dgsim.dll")); err != nil {
		fail(fmt.Sprintf("the match host's files are missing: %s has no dgsim.dll. Unpack the whole release zip, with "+
			"its host folder, next to Start-Client.exe.", p.hostDir))
		return
	}
	t0 := time.Now()
	first := ""
	if _, err := os.Stat(filepath.Join(p.l.HostCopy(), "game", "Dawngate.exe")); err != nil {
		first = "; its first start sets up the match host (about half a minute, 1.5 GB):"
	}
	p.say("starting the server on this PC (%s)%s", p.address, first)
	release, err := setup.Lock(p.l.Root)
	if err != nil {
		fail(err.Error())
		return
	}
	s := &setup.Setup{Layout: p.l, Out: p.out, Live: p.live, FindZip: p.findZip, Manifest: manifest, Progress: p.progress,
		HostFiles: hostcopy.Files{FS: os.DirFS(p.hostDir), DLL: "dgsim.dll", Lua: "lua"}, Maps: p.mapsDir(),
		SkipFailedMaps: p.skipFailedMaps}
	err = s.Server(setup.ServerOptions{Address: p.certsAddr, NoClient: true})
	release()
	if err != nil {
		fail(fmt.Sprintf("the setup is not complete:\n%s\n%s", lines(s.Bad()), setupAdvice(s.Bad(), p.l.Root)))
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		fail("Start-Client is closing")
		return
	}
	p.state = thisPCStarting
	stop, exited := make(chan struct{}), make(chan struct{})
	p.stop, p.exited = stop, exited
	p.mu.Unlock()
	p.notify()
	started, failed := make(chan dgserver.Info, 1), make(chan error, 1)
	flags := p.flags()
	go func() {
		defer close(exited)
		code := p.run(flags, dgserver.Hooks{Stop: stop, Quiet: true,
			Started: func(i dgserver.Info) { started <- i }, StartFailed: func(e error) { failed <- e }})
		p.mu.Lock()
		wasRunning := p.state == thisPCRunning
		if wasRunning {
			p.state = ""
		}
		p.stop = nil
		p.mu.Unlock()
		lock.Release()
		if wasRunning {
			p.say("the server stopped (exit code %d)", code)
			p.notify()
		}
	}()
	select {
	case info := <-started:
		if info.GatewayPin != "" {
			p.pin(p.address, info.GatewayPin)
		}
		if err := lock.Set(setup.RunRunning, info.GatewayPin); err != nil {
			p.say("note: the folder's server lock: %v", err)
		}
		p.mu.Lock()
		p.info = info
		p.mu.Unlock()
		finish(thisPCRunning, "")
		p.say("the server runs on %s (ready %s after the login; log %s); it stops when the game ends", p.address,
			time.Since(t0).Round(100*time.Millisecond), info.Log)
	case e := <-failed:
		<-exited
		finish(thisPCFailed, fmt.Sprintf("the server didn't start: %v", e))
	case <-exited:
		finish(thisPCFailed, "the server didn't start (see the lines above)")
	}
}

// progress follows the host copy's extraction for the list (in 5 % steps): its three phases make one percentage.
func (p *thisPC) progress(step, phase string, pct int) {
	base := map[string]int{archive.PhaseHashZip: 0, archive.PhaseExtract: 1, archive.PhaseVerify: 2}[phase]
	all := (base*100 + pct) / 3
	p.mu.Lock()
	moved := all/5 != p.pct/5 || p.pct < 0
	p.pct = all
	p.mu.Unlock()
	if moved {
		p.notify()
	}
}

// flags are dgserver's for This PC: Start-Server's for the folder (ServerFlags, the game options of this folder's
// Start-Server.ini when there is one), the tunnel without plain listeners on ThisPCAddress, its match hosts there,
// no website.
func (p *thisPC) flags() []string {
	ini := p.serverIni
	if ini == nil {
		ini = Config{}
	}
	shaperSelect, err := ini.Bool("shaper_select", true)
	if err != nil {
		shaperSelect = true
	}
	own, err := LoginFlags(ini)
	if err != nil {
		own, _ = LoginFlags(Config{})
	}
	own = append(own, OnlineCountFlags(ini)...)  // the folder's players-online count settings, as Start-Server's
	own = append(own, ChatRoomsFlags(ini)...)    // and its chat rooms
	own = append(own, MatchHistoryFlags(ini)...) // and its match history names
	own = append(own, HalloweenFlags(ini)...)    // and its Halloween decoration
	own = append(own, MatchRecordFlags(ini)...)  // and its match recorder
	own = append(own, MapFlags(ini)...)          // and its maps
	if dir := p.mapsDir(); dir != "" {
		own = append(own, MapsFlags(filepath.Dir(dir))...) // the maps folder beside the programs
	}
	f := ServerFlags(p.l, p.certsAddr, shaperSelect, append(own, SplitFlags(ini["server_flags"])...))
	if p.localTrust { // no passwords: the server binds only ThisPCAddress, a loopback address (dgserver checks it)
		f = append(f, "--local-trust")
	}
	f = append(f, "--bind", p.address, "--public-host", p.address, "--gms-control-bind", p.address, "--web-port", "0",
		"--tunnel", "on", "--gateway-port", strconv.Itoa(p.gwPort), "--plain", "off", "--gms-bind", HostsAddress(p.address))
	p.mu.Lock()
	m := p.testMap
	p.mu.Unlock()
	if m != "" {
		// the map editor's Test: every game hosts the map (practice too); a custom game may still pick the folder's
		// own custom maps by their tags. After server_flags, so these win.
		custom := m
		if c := strings.TrimSpace(ini["custom_maps"]); c != "" && !strings.EqualFold(c, "none") {
			custom += "," + c
		}
		f = append(f, "--map="+m, "--custom-maps="+custom)
	}
	return append(f, noDiscordFlags()...) // This PC never posts to Discord, whatever server_flags say
}

// SetMap names the map every game of This PC's own server hosts from its next start on (the map editor's Test,
// with Restart); "" the folder's own.
func (p *thisPC) SetMap(name string) {
	p.mu.Lock()
	p.testMap = name
	p.mu.Unlock()
}

// folderServer reads the folder's run lock again: the Start-Server of this folder, while one runs (then This PC is
// that one, which Restart can't change).
func (p *thisPC) folderServer() (setup.RunLock, bool) {
	p.refresh()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.other, p.otherHeld
}

// Restart stops Start-Client's own server for This PC, as Close does but for good only until the next login: a
// start that goes on is waited for first, then the server (and its match hosts) end and the folder's run lock is
// freed. The next Ensure (the next login to This PC) sets the host copy up again and starts it with fresh flags (a
// map of SetMap). A failed start is forgotten, so the next login tries again.
func (p *thisPC) Restart() {
	p.mu.Lock()
	done := p.done
	busy := p.state == thisPCSetup || p.state == thisPCStarting
	p.mu.Unlock()
	if busy && done != nil {
		<-done
	}
	p.mu.Lock()
	stop, exited := p.stop, p.exited
	p.stop = nil
	if p.state == thisPCFailed {
		p.state, p.failure = "", ""
	}
	p.mu.Unlock()
	if stop != nil {
		p.say("stopping the server for the map editor's Test (it starts again at the next login)")
		close(stop)
		select {
		case <-exited:
		case <-time.After(20 * time.Second):
			p.say("note: the server didn't stop within 20 s")
		}
	}
	p.notify()
}

// Close stops Start-Client's own server (and its match hosts) and frees the folder's run lock.
func (p *thisPC) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.closed = true
	stop, exited := p.stop, p.exited
	p.stop = nil
	p.mu.Unlock()
	if stop != nil {
		close(stop)
		select {
		case <-exited:
		case <-time.After(20 * time.Second):
		}
	}
	p.mu.Lock()
	lock := p.lock
	p.mu.Unlock()
	lock.Release()
}
