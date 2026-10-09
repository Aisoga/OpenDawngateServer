package gmshost

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// waitUntil polls cond for up to 5 s.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// H1-live: the warm pool. FillWarm starts Config.Warm hosts with DGGMS_WARM; they say hello and wait
// for a game without a timeout. Start gives the game to the oldest without a launch (createGame at
// once, gameReady), and a replacement starts in the background.
func TestWarmPool(t *testing.T) {
	got := make(chan CreateGame, 4)
	f := newFixture(t, 3, func(env Env, p *FakeProcess) {
		PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
			got <- cg
			return nil
		})
	})
	f.m.cfg.Warm = 2
	f.m.FillWarm()
	waitUntil(t, "two idle warm hosts", func() bool { idle, starting := f.m.Warm(); return idle == 2 && starting == 0 })
	for _, e := range f.l.Envs() {
		if !e.Warm || !strings.Contains(strings.Join(e.Vars(), " "), "DGGMS_WARM=1") {
			t.Errorf("warm host env %+v", e)
		}
	}
	// longer than the hello and ready timeouts (500 ms): an idle warm host is not killed
	time.Sleep(800 * time.Millisecond)
	if idle, _ := f.m.Warm(); idle != 2 || len(f.l.Envs()) != 2 {
		t.Fatalf("idle %d, launches %d", idle, len(f.l.Envs()))
	}

	rec := newRecorder()
	start := time.Now()
	mt, err := f.m.Start(practice, rec.Events())
	if err != nil {
		t.Fatal(err)
	}
	// the first warm host that said hello (match 1 or 2: the two dial at once), on its own port
	if (mt.ID != 1 && mt.ID != 2) || mt.Port != 20041+int(mt.ID) {
		t.Errorf("Start gave match %d port %d, want a warm host (1 or 2) on its port", mt.ID, mt.Port)
	}
	select {
	case port := <-rec.ready:
		if port != mt.Port {
			t.Errorf("ready on %d, want %d", port, mt.Port)
		}
		t.Logf("gameReady %v after Start", time.Since(start))
	case <-time.After(2 * time.Second):
		t.Fatal("no gameReady")
	}
	if cg := <-got; cg.GameID != practice.GameID || cg.Seed != practice.Seed || len(cg.Roster) != 1 {
		t.Errorf("host got %+v", cg)
	}
	o := rec.wait(t)
	if !o.Ready || o.Killed || o.ExitCode != 0 {
		t.Errorf("outcome %+v", o)
	}
	// the replacement: two idle again, three launches
	waitUntil(t, "the pool refilled", func() bool { idle, _ := f.m.Warm(); return idle == 2 && len(f.l.Envs()) == 3 })
	if e := f.l.Envs()[2]; e.Match != 3 || !e.Warm {
		t.Errorf("replacement env %+v", e)
	}
	log := f.log.String()
	id, port := mt.ID, mt.Port
	for _, want := range []string{
		"[host] match 1: warm host started, pid ", "[host] match 2: warm host started, pid ", "(it waits for a game)",
		fmt.Sprintf("[host] match %d: warm host ready, waiting for a game", id),
		fmt.Sprintf("[host] match %d: started pid ", id), "(warm host, ready for ",
		fmt.Sprintf(") for game 1 (practice, SM_Map_v5, 1 players), GMS 127.0.0.1:%d", port),
		fmt.Sprintf(`[host] match %d -> {"t":"createGame","gameId":1,`, id),
		fmt.Sprintf("[host] match %d: ready on port %d, ", id, port),
		"[host] match 3: warm host started, pid ",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// A warm host that ends while idle is replaced without any event; warmMaxFails hosts in a row that
// end before their hello stop the refilling, and a game then starts a host the usual way.
func TestWarmPoolReplacesAndPauses(t *testing.T) {
	var n atomic.Int32
	f := newFixture(t, 3, func(env Env, p *FakeProcess) {
		if n.Add(1) == 1 { // the first says hello, waits a moment and dies
			h, err := DialHost(env, p.Pid(), "fake")
			if err == nil {
				time.Sleep(200 * time.Millisecond)
				h.Close()
			}
			p.Exit(3)
			return
		}
		p.Exit(4) // the others die before their hello
	})
	f.m.cfg.Warm = 1
	f.m.FillWarm()
	waitUntil(t, "the pool paused", func() bool { return strings.Contains(f.log.String(), "the pool is not refilled") })
	time.Sleep(200 * time.Millisecond)
	if got := len(f.l.Envs()); got != 1+warmMaxFails { // Launch records synchronously; Run counts in its goroutine
		t.Errorf("launches %d, want %d", got, 1+warmMaxFails)
	}
	waitUntil(t, "no match left", func() bool { return f.m.Running() == 0 })
	if idle, starting := f.m.Warm(); idle != 0 || starting != 0 || f.m.cfg.Pool.Free() != 3 {
		t.Errorf("idle %d, starting %d, free ports %d", idle, starting, f.m.cfg.Pool.Free())
	}
	log := f.log.String()
	for _, want := range []string{
		"[host] match 1: warm host ready, waiting for a game",
		"[host] match 1: warm host ended without a game after ", "(after its hello; exit code 3, killed false); port 20042 free",
		"[host] match 2: warm host ended without a game after ", "(before its hello; exit code 4",
		"warm pool: 3 warm hosts in a row ended before their hello; the pool is not refilled",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	// a game still gets a host: a cold launch, since nobody is idle
	if _, err := f.m.Start(practice, newRecorder().Events()); err != nil {
		t.Fatal(err)
	}
	if got := len(f.l.Envs()); got != 2+warmMaxFails {
		t.Errorf("launches %d after Start, want %d", got, 2+warmMaxFails)
	}
}
