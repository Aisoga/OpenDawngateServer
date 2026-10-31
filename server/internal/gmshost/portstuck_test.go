package gmshost

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// PORTSTUCK: a GMS port its host can't bind goes into quarantine and the game is started once more on
// the next free port. The hosts here bind their port for real (UDP on 127.0.0.1, as the host DLL does
// after createGame), and a port is "stuck" because the test holds it with a socket of its own, as the
// socket left behind in the wineserver after an earlier match can hold a port under Wine.

// freeRange finds n consecutive UDP ports on 127.0.0.1 that bind now, and returns the first.
func freeRange(t *testing.T, n int) int {
	t.Helper()
	for range 50 {
		base := 30000 + rand.IntN(25000)
		ok := true
		for i := range n {
			if probeUDP("127.0.0.1", base+i) != nil {
				ok = false
				break
			}
		}
		if ok {
			return base
		}
	}
	t.Fatalf("no %d free consecutive UDP ports on 127.0.0.1", n)
	return 0
}

// holdPort binds 127.0.0.1:port (UDP) until the returned function is called (or the test ends).
func holdPort(t *testing.T, port int) func() {
	t.Helper()
	c, err := net.ListenPacket("udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return func() { c.Close() }
}

// How bindingHost tells the server that its port is taken.
const (
	tellReport = iota // reportGMSError ErrGMSEndpoint, then exit code 2 (the host DLL since PORTSTUCK, gms_script)
	tellExit2         // exit code 2 only (an older host DLL)
	tellLevel         // its level did not load: reportGMSError ErrLevelLoad, exit code 2 (never the port)
)

// bindingHost is a fake host that, after createGame, binds DGGMS_BIND for real (UDP). If that fails it
// tells the server as how says and exits 2; else it answers gameReady with the bound port, holds the
// port and exits 0 after abortGame (4 when the connection ends).
func bindingHost(how int) func(env Env, p *FakeProcess) {
	return func(env Env, p *FakeProcess) {
		h, err := DialHost(env, p.Pid(), "fake")
		if err != nil {
			p.Exit(1)
			return
		}
		go func() { <-p.Done(); h.Close() }()
		if msg, err := h.Recv(10 * time.Second); err != nil {
			p.Exit(1)
			return
		} else if _, ok := msg.(CreateGame); !ok {
			p.Exit(5)
			return
		}
		c, err := net.ListenPacket("udp4", env.Bind)
		if how == tellLevel && err == nil {
			c.Close()
			err = errors.New("the level did not load")
		}
		if err != nil {
			switch how {
			case tellReport:
				h.Send(GMSError{Code: ErrGMSEndpoint, Text: "GMS endpoint: " + err.Error(), Fatal: true})
			case tellLevel:
				h.Send(GMSError{Code: ErrLevelLoad, Text: "level load: " + err.Error(), Fatal: true})
			}
			h.Send(LogoutGMS{})
			h.Close()
			p.Exit(2)
			return
		}
		defer c.Close()
		if err := h.Send(GameReady{Port: c.LocalAddr().(*net.UDPAddr).Port}); err != nil {
			p.Exit(1)
			return
		}
		for {
			msg, err := h.Recv(0)
			if err != nil {
				p.Exit(4)
				return
			}
			if _, ok := msg.(AbortGame); ok {
				h.Send(LogoutGMS{})
				h.Close()
				p.Exit(0)
				return
			}
		}
	}
}

// newPortFixture is newFixture on the real ports base..base+n-1 with a quarantine of q.
func newPortFixture(t *testing.T, base, n int, q time.Duration, run func(env Env, p *FakeProcess), set ...func(*Config)) *fixture {
	t.Helper()
	return newFixture(t, n, run, append([]func(*Config){func(c *Config) {
		c.Pool = NewPool(base, base+n-1)
		c.Quarantine = q
		c.HeartbeatTimeout = 30 * time.Second // the hosts here send no heartbeat
		c.ReadyTimeout = 5 * time.Second
	}}, set...)...)
}

func waitReady(t *testing.T, rec *recorder) int {
	t.Helper()
	select {
	case port := <-rec.ready:
		return port
	case <-time.After(10 * time.Second):
		t.Fatalf("no gameReady; events %v", rec.list())
		return 0
	}
}

func bindPorts(envs []Env) []string {
	var out []string
	for _, e := range envs {
		_, p, _ := net.SplitHostPort(e.Bind)
		out = append(out, p)
	}
	return out
}

// One stuck port costs no game: the host on it can't bind it, so the port goes into quarantine and the
// same game (same match, a new token) starts on the next free port; while the quarantine lasts the port
// is skipped; after it the port is handed out again and works once its holder is gone. The same with an
// older host that says nothing but exit code 2.
func TestStuckPort(t *testing.T) {
	for _, c := range []struct {
		name, why string
		how       int
	}{
		{"report", "its host could not bind it (host error 1 (GMS endpoint): GMS endpoint: listen udp4 127.0.0.1:", tellReport},
		{"exit2", "its host could not bind it (exit code 2 before gameReady)", tellExit2},
	} {
		t.Run(c.name, func(t *testing.T) {
			base := freeRange(t, 3)
			release := holdPort(t, base)
			const q = 400 * time.Millisecond
			f := newPortFixture(t, base, 3, q, bindingHost(c.how))

			rec1 := newRecorder()
			mt1, err := f.m.Start(practice, rec1.Events())
			if err != nil {
				t.Fatal(err)
			}
			quarantinedAt := time.Now()
			if got := waitReady(t, rec1); got != base+1 {
				t.Fatalf("game 1 ready on %d, want %d (the next port)", got, base+1)
			}
			if _, _, port, _ := mt1.Placement(); port != base+1 || mt1.Port != base+1 {
				t.Errorf("match 1 placed on %d / %d", port, mt1.Port)
			}
			envs := f.l.Envs()
			if got, want := bindPorts(envs), []string{strconv.Itoa(base), strconv.Itoa(base + 1)}; !reflect.DeepEqual(got, want) {
				t.Fatalf("launches on %v, want %v", got, want)
			}
			if envs[0].Match != envs[1].Match || envs[0].Token == envs[1].Token {
				t.Errorf("the second host: match %d/%d, a new token %v", envs[0].Match, envs[1].Match, envs[0].Token != envs[1].Token)
			}
			hasAll(t, "server", f.log.String(),
				fmt.Sprintf("match 1: port %d quarantined for %v: %s", base, q, c.why),
				"match 1: started pid ",
				fmt.Sprintf("for game 1 again (the first host could not bind its port), GMS 127.0.0.1:%d", base+1),
				fmt.Sprintf("match 1: ready on port %d", base+1))
			if s := f.m.Sites(); s[0].Free != 1 || s[0].Quarantined != 1 {
				t.Errorf("sites %+v", s)
			}

			// while the quarantine lasts: the stuck port is skipped, no host is started on it
			rec2 := newRecorder()
			mt2, err := f.m.Start(practice, rec2.Events())
			if err != nil {
				t.Fatal(err)
			}
			if got := waitReady(t, rec2); got != base+2 {
				t.Fatalf("game 2 ready on %d, want %d", got, base+2)
			}
			if n := len(f.l.Envs()); n != 3 {
				t.Errorf("%d launches, want 3", n)
			}

			release()
			for _, mt := range []*Match{mt1, mt2} {
				mt.Abort("test over")
			}
			if o := rec1.wait(t); !o.Ready || o.Retries != 1 || o.ExitCode != 0 {
				t.Errorf("match 1: %+v", o)
			}
			rec2.wait(t)
			hasAll(t, "server", f.log.String(), fmt.Sprintf("killed false; port %d free", base+1))

			// after the quarantine the port is back, and with its holder gone it works
			time.Sleep(time.Until(quarantinedAt.Add(q + 50*time.Millisecond)))
			rec3 := newRecorder()
			mt3, err := f.m.Start(practice, rec3.Events())
			if err != nil {
				t.Fatal(err)
			}
			if got := waitReady(t, rec3); got != base {
				t.Fatalf("game 3 ready on %d, want %d (back from quarantine)", got, base)
			}
			hasAll(t, "server", f.log.String(), fmt.Sprintf("port %d back in the pool: its quarantine of %v is over", base, q))
			mt3.Abort("test over")
			rec3.wait(t)
			if s := f.m.Sites(); s[0].Free != 3 || s[0].Quarantined != 0 {
				t.Errorf("sites at the end %+v", s)
			}
		})
	}
}

// Every port stuck: the first host fails, the second (on the other port) too, and the game fails as a
// host gone before gameReady (Blaze's dead-GMS path), with both ports in quarantine; no third host. The
// next game isn't started at all (ErrNoPort, naming the quarantine). With a single port there is
// nothing to start again on.
func TestAllPortsStuck(t *testing.T) {
	t.Run("two", func(t *testing.T) {
		base := freeRange(t, 2)
		holdPort(t, base)
		holdPort(t, base+1)
		f := newPortFixture(t, base, 2, time.Minute, bindingHost(tellReport))
		rec := newRecorder()
		if _, err := f.m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		o := rec.wait(t)
		if o.Ready || o.Retries != 1 || o.ExitCode != 2 || o.Error == nil || o.Error.Code != ErrGMSEndpoint ||
			!strings.HasPrefix(o.Reason, "host error 1 (GMS endpoint): GMS endpoint: ") {
			t.Errorf("outcome %+v", o)
		}
		if got := len(f.l.Envs()); got != 2 {
			t.Errorf("%d launches, want 2", got)
		}
		hasAll(t, "server", f.log.String(),
			fmt.Sprintf("match 1: port %d quarantined for 1m0s", base),
			fmt.Sprintf("match 1: port %d quarantined for 1m0s", base+1),
			"match 1: game 1 not started again: it was started again once already",
			fmt.Sprintf("exit code 2, killed false; port %d in quarantine", base+1))
		if got := rec.list(); !reflect.DeepEqual(got, []string{"end"}) {
			t.Errorf("events %v", got)
		}
		_, err := f.m.Start(practice, Events{})
		if !errors.Is(err, ErrNoPort) || !strings.Contains(err.Error(), "(2 in quarantine: their hosts could not bind them)") {
			t.Fatalf("next game: %v", err)
		}
		hasAll(t, "server", f.log.String(), "match 2 (game 1): not started: gmshost: no free GMS port (2 in quarantine")
		if got := len(f.l.Envs()); got != 2 {
			t.Errorf("%d launches after the refused start, want 2", got)
		}
	})
	t.Run("one", func(t *testing.T) {
		base := freeRange(t, 1)
		holdPort(t, base)
		f := newPortFixture(t, base, 1, time.Minute, bindingHost(tellExit2))
		rec := newRecorder()
		if _, err := f.m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		if o := rec.wait(t); o.Ready || o.Retries != 0 || o.ExitCode != 2 {
			t.Errorf("outcome %+v", o)
		}
		hasAll(t, "server", f.log.String(),
			"match 1: game 1 not started again: gmshost: no free GMS port (1 in quarantine: their hosts could not bind them)",
			fmt.Sprintf("port %d in quarantine", base))
		if got := len(f.l.Envs()); got != 1 {
			t.Errorf("%d launches, want 1", got)
		}
	})
}

// Not the port's fault, or too late: no quarantine and no second host for a level that didn't load
// (the host DLL's ErrLevelLoad before its exit code 2), a host the server killed, or a game whose
// retry window is over (the port still goes into quarantine then).
func TestStuckPortNoRetry(t *testing.T) {
	t.Run("level", func(t *testing.T) {
		base := freeRange(t, 2)
		f := newPortFixture(t, base, 2, time.Minute, bindingHost(tellLevel))
		rec := newRecorder()
		if _, err := f.m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		if o := rec.wait(t); o.Ready || o.Retries != 0 || o.ExitCode != 2 || o.Error == nil || o.Error.Code != ErrLevelLoad {
			t.Errorf("outcome %+v", o)
		}
		if strings.Contains(f.log.String(), "quarantined") || len(f.l.Envs()) != 1 {
			t.Errorf("%d launches; log:\n%s", len(f.l.Envs()), f.log.String())
		}
		hasAll(t, "server", f.log.String(), "aborted: host error 5 (level load): level load: the level did not load; exit code 2",
			fmt.Sprintf("port %d free", base))
		if f.m.cfg.Pool.Free() != 2 {
			t.Errorf("free %d", f.m.cfg.Pool.Free())
		}
	})
	t.Run("killed", func(t *testing.T) {
		base := freeRange(t, 2)
		f := newPortFixture(t, base, 2, time.Minute, func(env Env, p *FakeProcess) {}, // never says hello
			func(c *Config) { c.HelloTimeout = 100 * time.Millisecond })
		rec := newRecorder()
		if _, err := f.m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		if o := rec.wait(t); !o.Killed || o.Retries != 0 {
			t.Errorf("outcome %+v", o)
		}
		if strings.Contains(f.log.String(), "quarantined") || len(f.l.Envs()) != 1 {
			t.Errorf("log:\n%s", f.log.String())
		}
	})
	t.Run("window", func(t *testing.T) {
		base := freeRange(t, 2)
		holdPort(t, base)
		// The host reports its failed bind a few clock ticks after the start. Windows' monotonic clock
		// advances once per timer tick (up to about 15.6 ms), so a report within the first tick reads
		// 0 s since the start: inside even a 1 ns window, and the game would be started again.
		late := func(env Env, p *FakeProcess) {
			time.Sleep(50 * time.Millisecond)
			bindingHost(tellReport)(env, p)
		}
		f := newPortFixture(t, base, 2, time.Minute, late, func(c *Config) { c.RetryWindow = time.Nanosecond })
		rec := newRecorder()
		if _, err := f.m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		if o := rec.wait(t); o.Ready || o.Retries != 0 {
			t.Errorf("outcome %+v", o)
		}
		hasAll(t, "server", f.log.String(), fmt.Sprintf("match 1: port %d quarantined for 1m0s", base),
			"match 1: game 1 not started again: ", "s since its start, more than 1ns")
		if len(f.l.Envs()) != 1 {
			t.Errorf("%d launches", len(f.l.Envs()))
		}
	})
}

// ProbePorts (dgserver with --gms-dir): the server binds a port itself before a start and skips one
// that is taken, so no host is started on it at all.
func TestProbePorts(t *testing.T) {
	base := freeRange(t, 2)
	release := holdPort(t, base)
	const q = 300 * time.Millisecond
	f := newPortFixture(t, base, 2, q, bindingHost(tellReport), func(c *Config) { c.ProbePorts = true })
	rec := newRecorder()
	mt, err := f.m.Start(practice, rec.Events())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	if mt.Port != base+1 {
		t.Errorf("started on %d, want %d", mt.Port, base+1)
	}
	if got := waitReady(t, rec); got != base+1 {
		t.Errorf("ready on %d", got)
	}
	if got := bindPorts(f.l.Envs()); !reflect.DeepEqual(got, []string{strconv.Itoa(base + 1)}) {
		t.Errorf("launches on %v", got)
	}
	hasAll(t, "server", f.log.String(), fmt.Sprintf("port %d quarantined for %v: it is taken before the start (listen udp4 127.0.0.1:%d", base, q, base))
	mt.Abort("test over")
	rec.wait(t)
	release()
	time.Sleep(time.Until(at.Add(q + 50*time.Millisecond)))
	rec = newRecorder()
	mt, err = f.m.Start(practice, rec.Events())
	if err != nil || mt.Port != base {
		t.Fatalf("after the quarantine: %v, port %d", err, mt.Port)
	}
	waitReady(t, rec)
	mt.Abort("test over")
	rec.wait(t)
	hasAll(t, "server", f.log.String(), fmt.Sprintf("port %d back in the pool: its quarantine of %v is over", base, q))
}

// The quarantine's length doubles while the port keeps failing, up to the cap, and starts short again
// once a host got ready on it; a quarantined port stays out of Free and Acquire, and Release doesn't
// end its quarantine.
func TestPoolQuarantine(t *testing.T) {
	p := NewPool(10042, 10044)
	const base, max = 20 * time.Millisecond, 50 * time.Millisecond
	if port, _ := p.Acquire(); port != 10042 {
		t.Fatal(port)
	}
	if d := p.Quarantine(10042, base, max); d != base {
		t.Errorf("first quarantine %v", d)
	}
	p.Release(10042)
	if p.Free() != 2 || !reflect.DeepEqual(p.InQuarantine(), []int{10042}) {
		t.Errorf("free %d, quarantine %v", p.Free(), p.InQuarantine())
	}
	for _, want := range []int{10043, 10044} {
		if port, err := p.Acquire(); port != want || err != nil {
			t.Errorf("acquire %d %v, want %d", port, err, want)
		}
	}
	if _, err := p.Acquire(); !errors.Is(err, ErrNoPort) || err.Error() != "gmshost: no free GMS port (1 of 3 in quarantine: their hosts could not bind them)" {
		t.Errorf("exhausted: %v", err)
	}
	if e := p.Expire(); len(e) != 0 {
		t.Errorf("expired early: %v", e)
	}
	time.Sleep(base + 10*time.Millisecond)
	if e := p.Expire(); !reflect.DeepEqual(e, []QuarantineEnd{{Port: 10042, After: base}}) {
		t.Errorf("expired %v", e)
	}
	if port, err := p.Acquire(); port != 10042 || err != nil {
		t.Errorf("back: %d %v", port, err)
	}
	for _, want := range []time.Duration{2 * base, max, max} { // 40 ms, then the cap
		if d := p.Quarantine(10042, base, max); d != want {
			t.Errorf("quarantine %v, want %v", d, want)
		}
	}
	p.Good(10042)
	if d := p.Quarantine(10042, base, max); d != base {
		t.Errorf("after a good host: %v", d)
	}
	if d := p.Quarantine(1, base, max); d != 0 || len(p.InQuarantine()) != 1 {
		t.Errorf("foreign port: %v %v", d, p.InQuarantine())
	}
}

// probeUDP fails on a port a socket holds and binds a free one (and leaves it free).
func TestProbeUDP(t *testing.T) {
	base := freeRange(t, 1)
	if err := probeUDP("127.0.0.1", base); err != nil {
		t.Fatal(err)
	}
	if err := probeUDP("127.0.0.1", base); err != nil {
		t.Fatalf("the probe left the port bound: %v", err)
	}
	holdPort(t, base)
	if err := probeUDP("127.0.0.1", base); err == nil {
		t.Fatal("a held port probed free")
	}
}

// The agents (H2): an agent's host that exits 2 on its port reaches the main server as hostExited
// (code 2) and its own reportGMSError; the main server quarantines that port of the agent's pool and
// asks the agent for a host on the next one. (No probe: the agent's ports are on its machine.)
func TestAgentStuckPort(t *testing.T) {
	m, log := newMain(t, nil, 0, func(c *Config) { c.Quarantine = time.Minute })
	runAgent(t, m, "hostA", "192.168.1.20", "20042-20043", func(env Env, p *FakeProcess) {
		if !strings.HasSuffix(env.Bind, ":20042") {
			untilKilled(env, p)
			return
		}
		h, err := DialHost(env, p.Pid(), "fake")
		if err != nil {
			p.Exit(1)
			return
		}
		h.Recv(5 * time.Second) // createGame
		h.Send(GMSError{Code: ErrGMSEndpoint, Text: "GMS endpoint: RakPeer::Startup on " + env.Bind + " failed (result 5; port in use?)", Fatal: true})
		h.Send(LogoutGMS{})
		h.Close()
		p.Exit(2)
	})
	rec := newRecorder()
	mt, err := m.Start(practice, rec.Events())
	if err != nil {
		t.Fatal(err)
	}
	if got := waitReady(t, rec); got != 20043 {
		t.Fatalf("ready on %d", got)
	}
	if site, host, port, _ := mt.Placement(); site != "hostA" || host != "192.168.1.20" || port != 20043 {
		t.Errorf("placement %s %s %d", site, host, port)
	}
	hasAll(t, "server", log.String(),
		"match 1: port 20042 on agent hostA quarantined for 1m0s: its host could not bind it (host error 1 (GMS endpoint): GMS endpoint: RakPeer::Startup on 10.1.2.3:20042 failed (result 5; port in use?))",
		"on agent hostA for game 1 again (the first host could not bind its port), GMS 10.1.2.3:20043")
	if s := m.Sites(); len(s) != 1 || s[0].Free != 0 || s[0].Quarantined != 1 {
		t.Errorf("sites %+v", s)
	}
	mt.Abort("test over")
	if o := rec.wait(t); !o.Ready || o.Retries != 1 {
		t.Errorf("outcome %+v", o)
	}
}
