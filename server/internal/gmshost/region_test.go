package gmshost

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// M27: games wanted in a region (region.go). The fixture's own machine is the home region eu (ports 20042-20043); the
// machine na1 (made-up ports 21042-...) is North America's, added without an agent link (AddTestMachine).

// playUntilKilled is a fake host that gets ready and then waits until it is ended.
func playUntilKilled(env Env, p *FakeProcess) {
	PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
		<-p.Done()
		return nil
	})
}

func newRegionFixture(t *testing.T, naPorts int, na Launcher) *fixture {
	t.Helper()
	f := newFixture(t, 2, playUntilKilled)
	f.m.SetRegions("EU", map[string]string{"na1": "na"})
	if na != nil {
		f.m.AddTestMachine("na1", na, NewPool(21042, 21042+naPorts-1), "203.0.113.7")
	}
	return f
}

func custom(id int64) CreateGame {
	g := practice
	g.GameID, g.Mode = id, ModeCustom
	return g
}

func wantPlacement(t *testing.T, mt *Match, site, region, wanted, fellBack string) {
	t.Helper()
	gotSite, _, _, _ := mt.Placement()
	r, w, fb := mt.RegionPlacement()
	if gotSite != site || r != region || w != wanted || fb != fellBack {
		t.Errorf("match %d on %s, region %q wanted %q fell back %q; want %s, %q, %q, %q", mt.ID, gotSite, r, w, fb, site,
			region, wanted, fellBack)
	}
}

// PREF1: a region's display name is the operator's (SetRegionNames, also with regions off for LocalRegion), else its
// alias in capitals; CheckRegionName keeps names short and printable.
func TestRegionNames(t *testing.T) {
	m := NewManager(Config{})
	if got := m.RegionName(LocalRegion); got != "Local" {
		t.Errorf("local: %q", got)
	}
	m.SetRegionNames(map[string]string{"local": "Our LAN"})
	if got := m.RegionName("local"); got != "Our LAN" {
		t.Errorf("a named local: %q", got)
	}
	m.SetRegions("eu", map[string]string{"na1": "na", "ap1": "ap"})
	m.SetRegionNames(map[string]string{"EU": "Europe", "na": "NA (Virginia)"})
	for alias, want := range map[string]string{"eu": "Europe", "na": "NA (Virginia)", "ap": "AP", "local": "Local"} {
		if got := m.RegionName(alias); got != want {
			t.Errorf("%s: %q, want %q", alias, got, want)
		}
	}
	if got := m.Regions(); !reflect.DeepEqual(got, []string{"eu", "ap", "na"}) {
		t.Errorf("Regions %v", got)
	}
	for name, ok := range map[string]bool{"Europe": true, "NA (Virginia)": true, "Ü": true, "": false, " Europe": false,
		"Europe ": false, "a\tb": false, "a\nb": false, strings.Repeat("x", MaxRegionName): true,
		strings.Repeat("ä", MaxRegionName): true, strings.Repeat("x", MaxRegionName+1): false} {
		if why := CheckRegionName(name); (why == "") != ok {
			t.Errorf("%q: %q, want ok %v", name, why, ok)
		}
	}
}

// A game wanted in North America runs on na1, one wanted at home or with no wish here; Start never picks na1, though it
// has more free ports.
func TestStartInRegion(t *testing.T) {
	na := &FakeLauncher{Run: playUntilKilled}
	f := newRegionFixture(t, 5, na)
	if got := f.m.Regions(); !reflect.DeepEqual(got, []string{"eu", "na"}) || f.m.HomeRegion() != "eu" {
		t.Errorf("regions %v, home %q", got, f.m.HomeRegion())
	}
	recs := []*recorder{newRecorder(), newRecorder(), newRecorder()}
	away, err := f.m.StartIn(custom(1), recs[0].Events(), Want{Region: "NA"})
	if err != nil {
		t.Fatal(err)
	}
	wantPlacement(t, away, "na1", "na", "na", "")
	if _, gmsHost, port, _ := away.Placement(); gmsHost != "203.0.113.7" || port != 21042 {
		t.Errorf("GMS %s:%d", gmsHost, port)
	}
	home, err := f.m.StartIn(custom(2), recs[1].Events(), Want{Region: "eu"})
	if err != nil {
		t.Fatal(err)
	}
	wantPlacement(t, home, LocalSite, "eu", "eu", "")
	plain, err := f.m.Start(custom(3), recs[2].Events())
	if err != nil {
		t.Fatal(err)
	}
	wantPlacement(t, plain, LocalSite, "", "", "")
	for _, r := range recs {
		waitReady(t, r)
	}
	if len(na.Envs()) != 1 {
		t.Errorf("na1 launched %d hosts", len(na.Envs()))
	}
	for _, mt := range []*Match{away, home, plain} {
		mt.Abort("test over")
	}
	log := f.log.String()
	if !strings.Contains(log, " in region na for game 1 (custom, ") {
		t.Errorf("log lacks the start on na1:\n%s", log)
	}
}

// Regions off, or a region this server doesn't have: Start's way, no wish recorded.
func TestStartInRegionsOff(t *testing.T) {
	f := newFixture(t, 1, playUntilKilled)
	rec := newRecorder()
	mt, err := f.m.StartIn(custom(1), rec.Events(), Want{Region: "na"})
	if err != nil {
		t.Fatal(err)
	}
	wantPlacement(t, mt, LocalSite, "", "", "")
	waitReady(t, rec)
	mt.Abort("test over")
	if f.m.Regions() != nil {
		t.Errorf("regions %v with regions off", f.m.Regions())
	}

	g := newRegionFixture(t, 1, &FakeLauncher{Run: playUntilKilled})
	rec = newRecorder()
	mt, err = g.m.StartIn(custom(1), rec.Events(), Want{Region: "ap"})
	if err != nil {
		t.Fatal(err)
	}
	wantPlacement(t, mt, LocalSite, "eu", "", "")
	waitReady(t, rec)
	mt.Abort("test over")
}

// slowLauncher answers its launches only after its delay.
type slowLauncher struct {
	delay time.Duration
	mu    sync.Mutex
	procs []*FakeProcess
}

func (l *slowLauncher) Launch(env Env) (Process, error) {
	time.Sleep(l.delay)
	p := NewFakeProcess()
	l.mu.Lock()
	l.procs = append(l.procs, p)
	l.mu.Unlock()
	return p, nil
}

// Each reason the region can't take the game: the game starts at home and says why.
func TestStartInFallback(t *testing.T) {
	slow := &slowLauncher{delay: 300 * time.Millisecond}
	for _, c := range []struct {
		why   string
		na    Launcher
		ports int
		set   func(f *fixture)
	}{
		{FellBackDown, nil, 0, nil},
		{FellBackVersion, nil, 0, func(f *fixture) { f.m.MachineRefused("na1", FellBackVersion) }},
		{FellBackMap, &FakeLauncher{Run: playUntilKilled}, 1,
			func(f *fixture) { f.m.SetMachineMaps("na1", []string{"MM_Map_v3"}) }},
		{FellBackFull, &FakeLauncher{Run: playUntilKilled}, 1, func(f *fixture) {
			// the one port of na1 taken by another game
			mt, err := f.m.StartIn(custom(9), newRecorder().Events(), Want{Region: "na"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { mt.Abort("test over") })
		}},
		{FellBackFailed, &FakeLauncher{Err: errors.New("hostFailed: no host copy")}, 1, nil},
		{FellBackFailed + " (no answer)", slow, 1, func(f *fixture) { f.m.reg.limit = 50 * time.Millisecond }},
	} {
		t.Run(c.why, func(t *testing.T) {
			f := newRegionFixture(t, c.ports, c.na)
			if c.set != nil {
				c.set(f)
			}
			rec := newRecorder()
			mt, err := f.m.StartIn(custom(1), rec.Events(), Want{Region: "na"})
			if err != nil {
				t.Fatal(err)
			}
			why := strings.Fields(c.why)[0]
			wantPlacement(t, mt, LocalSite, "eu", "na", why)
			waitReady(t, rec)
			mt.Abort("test over")
			if log := f.log.String(); !strings.Contains(log, "game 1: wanted na, runs on local (eu): "+why) {
				t.Errorf("log lacks the fallback:\n%s", log)
			}
		})
	}
	// the launch that came too late is killed
	time.Sleep(400 * time.Millisecond)
	slow.mu.Lock()
	defer slow.mu.Unlock()
	for _, p := range slow.procs {
		select {
		case <-p.Killed():
		default:
			t.Error("a late host of na1 still runs")
		}
	}
}

// lostProcess is a host on a machine whose link is lost: it never says hello, and its Wait ends with the agent's error.
type lostProcess struct {
	lost chan struct{}
	once sync.Once
}

func (p *lostProcess) Pid() int    { return 77 }
func (p *lostProcess) Kill() error { p.end(); return nil }
func (p *lostProcess) end()        { p.once.Do(func() { close(p.lost) }) }
func (p *lostProcess) Wait() (int, error) {
	<-p.lost
	return -1, errors.New("agent na1 lost")
}

type lostLauncher struct{ procs chan *lostProcess }

func (l *lostLauncher) Launch(env Env) (Process, error) {
	p := &lostProcess{lost: make(chan struct{})}
	l.procs <- p
	return p, nil
}

// A host in North America that ends before gameReady: its game starts once more at home (its port on na1 back), as
// failed, or as down when the machine's link was lost; Retries counts it, and a second end isn't retried.
func TestStartInRestartHome(t *testing.T) {
	t.Run("failed", func(t *testing.T) {
		na := &FakeLauncher{Run: func(env Env, p *FakeProcess) { p.Exit(1) }} // dies before its hello
		f := newRegionFixture(t, 1, na)
		rec := newRecorder()
		mt, err := f.m.StartIn(custom(1), rec.Events(), Want{Region: "na"})
		if err != nil {
			t.Fatal(err)
		}
		waitReady(t, rec)
		wantPlacement(t, mt, LocalSite, "eu", "na", FellBackFailed)
		mt.Abort("test over")
		if o := rec.wait(t); o.Retries != 1 {
			t.Errorf("retries %d", o.Retries)
		}
		if f.m.sites[1].pool.Free() != 1 {
			t.Error("na1's port not back")
		}
		log := f.log.String()
		for _, want := range []string{
			"game 1: wanted na, runs on local (eu): failed (its host on na1 ended before gameReady)",
			"match 1: started pid ", " for game 1 again at home, GMS 127.0.0.1:2004",
		} {
			if !strings.Contains(log, want) {
				t.Errorf("log lacks %q:\n%s", want, log)
			}
		}
	})
	t.Run("down", func(t *testing.T) {
		na := &lostLauncher{procs: make(chan *lostProcess, 1)}
		f := newRegionFixture(t, 1, na)
		rec := newRecorder()
		mt, err := f.m.StartIn(custom(1), rec.Events(), Want{Region: "na"})
		if err != nil {
			t.Fatal(err)
		}
		(<-na.procs).end()
		waitReady(t, rec)
		wantPlacement(t, mt, LocalSite, "eu", "na", FellBackDown)
		mt.Abort("test over")
		rec.wait(t)
	})
	t.Run("once", func(t *testing.T) {
		// at home the host dies too: no second retry, the game ends
		f := newFixture(t, 1, func(env Env, p *FakeProcess) { p.Exit(1) })
		f.m.SetRegions("eu", map[string]string{"na1": "na"})
		f.m.AddTestMachine("na1", &FakeLauncher{Run: func(env Env, p *FakeProcess) { p.Exit(1) }}, NewPool(21042, 21042), "")
		rec := newRecorder()
		if _, err := f.m.StartIn(custom(1), rec.Events(), Want{Region: "na"}); err != nil {
			t.Fatal(err)
		}
		if o := rec.wait(t); o.Ready || o.Retries != 1 {
			t.Errorf("outcome %+v", o)
		}
	})
	t.Run("not after gameReady", func(t *testing.T) {
		na := &FakeLauncher{Run: func(env Env, p *FakeProcess) {
			PlayHost(env, p, func(h *HostConn, cg CreateGame) error { return errors.New("crash") })
		}}
		f := newRegionFixture(t, 1, na)
		rec := newRecorder()
		mt, err := f.m.StartIn(custom(1), rec.Events(), Want{Region: "na"})
		if err != nil {
			t.Fatal(err)
		}
		waitReady(t, rec)
		if o := rec.wait(t); !o.Ready || o.Retries != 0 {
			t.Errorf("outcome %+v", o)
		}
		wantPlacement(t, mt, "na1", "na", "na", "")
	})
}
