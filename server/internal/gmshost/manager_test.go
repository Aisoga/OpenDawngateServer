package gmshost

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// recorder collects a match's events.
type recorder struct {
	mu     sync.Mutex
	events []string
	ready  chan int
	end    chan Outcome
}

func newRecorder() *recorder { return &recorder{ready: make(chan int, 1), end: make(chan Outcome, 1)} }

func (r *recorder) add(s string) {
	r.mu.Lock()
	r.events = append(r.events, s)
	r.mu.Unlock()
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *recorder) Events() Events {
	return Events{
		Ready:        func(mt *Match, port int) { r.add(fmt.Sprintf("ready %d", port)); r.ready <- port },
		PlayerAdded:  func(mt *Match, p AddPlayer) { r.add(fmt.Sprintf("add %d/%d", p.User, p.Index)) },
		PlayerGone:   func(mt *Match, p DisconnectPlayer) { r.add(fmt.Sprintf("gone %d %s", p.User, p.Reason)) },
		Update:       func(mt *Match, u GMSUpdate) { r.add("update " + u.Phase) },
		PlayerFinish: func(mt *Match, p PlayerFinish) { r.add(fmt.Sprintf("player %d %v", p.User, p.Stats)) },
		Finish:       func(mt *Match, f FinishGame) { r.add(fmt.Sprintf("finish %d %s", f.WinnerTeam, f.Reason)) },
		// D8c: the karma votes, answered as dgserver does: playerRewards with the karma counted
		PlayerKarma: func(mt *Match, k PlayerKarma) {
			r.add(fmt.Sprintf("karma %d %d/%d/%d", k.User, k.Party, k.Team, k.Enemy))
			votes := k.Party + k.Team + k.Enemy
			mt.Rewards(k.User, Rewards{LootBag: 0x6401D184, BaseRoll: 400,
				Karma: &Karma{Points: 10 * votes, Party: k.Party, Team: k.Team, Enemy: k.Enemy}})
		},
		End: func(mt *Match, o Outcome) { r.add("end"); r.end <- o },
	}
}

func (r *recorder) wait(t *testing.T) Outcome {
	t.Helper()
	select {
	case o := <-r.end:
		return o
	case <-time.After(10 * time.Second):
		t.Fatalf("match did not end; events %v", r.list())
		return Outcome{}
	}
}

type fixture struct {
	m   *Manager
	l   *FakeLauncher
	log *syncBuf
}

// newFixture runs a manager on an ephemeral loopback port with a fake launcher, a pool of the given
// size (made-up port numbers; nothing binds them) and short timeouts.
func newFixture(t *testing.T, ports int, run func(env Env, p *FakeProcess)) *fixture {
	t.Helper()
	f := &fixture{l: &FakeLauncher{Run: run}, log: &syncBuf{}}
	f.m = NewManager(Config{Log: logging.New(f.log), Launcher: f.l, Pool: NewPool(20042, 20042+ports-1),
		HelloTimeout: 500 * time.Millisecond, ReadyTimeout: 500 * time.Millisecond,
		HeartbeatTimeout: 500 * time.Millisecond, ExitTimeout: 300 * time.Millisecond})
	if err := f.m.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.m.Close)
	return f
}

var practice = CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: ModePractice, Seed: 7919,
	Roster: []RosterEntry{{User: 2000000001, Name: "player", Index: 0, Team: 1}}}

// The whole lifecycle: spawn with the environment, hello, createGame with the match, gameReady,
// players, heartbeat, results, logoutGMS, exit 0, port back in the pool.
func TestMatchLifecycle(t *testing.T) {
	var gotGame CreateGame
	f := newFixture(t, 1, func(env Env, p *FakeProcess) {
		PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
			gotGame = cg
			for _, m := range []Message{AddPlayer{User: 2000000001}, GMSUpdate{Phase: PhaseInGame, ElapsedMs: 5000},
				PlayerFinish{User: 2000000001, Stats: map[string]int64{"kills": 3}},
				FinishGame{WinnerTeam: 1, DurationMs: 60000, Reason: "guardian"},
				DisconnectPlayer{User: 2000000001, Reason: "left"}} {
				if err := h.Send(m); err != nil {
					return err
				}
			}
			return nil
		})
	})
	rec := newRecorder()
	mt, err := f.m.Start(practice, rec.Events())
	if err != nil {
		t.Fatal(err)
	}
	o := rec.wait(t)
	if !o.Ready || !o.Finished || o.Killed || o.ExitCode != 0 || o.Reason != "guardian" || o.Result.WinnerTeam != 1 {
		t.Errorf("outcome %+v", o)
	}
	want := []string{"ready 20042", "add 2000000001/0", "update inGame", "player 2000000001 map[kills:3]",
		"finish 1 guardian", "gone 2000000001 left", "end"}
	if got := rec.list(); !reflect.DeepEqual(got, want) {
		t.Errorf("events\n got %v\nwant %v", got, want)
	}
	if !reflect.DeepEqual(gotGame, practice) {
		t.Errorf("host got %+v", gotGame)
	}
	env := f.l.Envs()[0]
	if env.Match != mt.ID || env.Bind != "127.0.0.1:20042" || env.Control != f.m.Addr() || len(env.Token) != 32 {
		t.Errorf("env %+v", env)
	}
	<-mt.Done()
	if f.m.Running() != 0 || f.m.cfg.Pool.Free() != 1 {
		t.Errorf("running %d, free ports %d", f.m.Running(), f.m.cfg.Pool.Free())
	}
	log := f.log.String()
	for _, want := range []string{
		"[host] match 1: started pid ", " for game 1 (practice, SM_Map_v5, 1 players), GMS 127.0.0.1:20042",
		"[host] match 1: hello from pid ",
		`[host] match 1 -> {"t":"createGame","gameId":1,`,
		`[host] match 1 <- {"t":"gameReady","port":20042}`,
		"[host] match 1: ready on port 20042, ",
		"[host] match 1: ended after ", ": finished (winner team 1): guardian; exit code 0, killed false; port 20042 free",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// D8c: a matchmade game's karma votes after finishGame reach Events.PlayerKarma, and the server's answer (the
// rewards with the karma counted) reaches the host as playerRewards with karma.
func TestPlayerKarma(t *testing.T) {
	got := make(chan PlayerRewards, 1)
	f := newFixture(t, 1, func(env Env, p *FakeProcess) {
		PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
			for _, m := range []Message{AddPlayer{User: 2000000001}, FinishGame{WinnerTeam: 2, DurationMs: 600000, Reason: "guardian"},
				PlayerKarma{User: 2000000001, Party: 1, Team: 1, Enemy: 2}} {
				if err := h.Send(m); err != nil {
					return err
				}
			}
			m, err := h.Recv(2 * time.Second)
			if err != nil {
				return err
			}
			got <- m.(PlayerRewards)
			return nil
		})
	})
	rec := newRecorder()
	if _, err := f.m.Start(practice, rec.Events()); err != nil {
		t.Fatal(err)
	}
	o := rec.wait(t)
	want := []string{"ready 20042", "add 2000000001/0", "finish 2 guardian", "karma 2000000001 1/1/2", "end"}
	if g := rec.list(); !reflect.DeepEqual(g, want) || o.ExitCode != 0 {
		t.Errorf("events %v, outcome %+v", g, o)
	}
	select {
	case r := <-got:
		if r.User != 2000000001 || r.Rewards.Karma == nil || *r.Rewards.Karma != (Karma{Points: 40, Party: 1, Team: 1, Enemy: 2}) {
			t.Errorf("playerRewards %+v", r)
		}
	default:
		t.Error("the host got no playerRewards")
	}
	log := f.log.String()
	for _, want := range []string{
		`[host] match 1 <- {"t":"playerKarma","user":2000000001,"party":1,"team":1,"enemy":2}`,
		`"karma":{"points":40,"party":1,"team":1,"enemy":2}}}`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// A connection with a wrong token or an unknown match is closed and doesn't count as the hello; the
// real host still gets in afterwards.
func TestTokenCheck(t *testing.T) {
	envs := make(chan Env, 1)
	release := make(chan struct{})
	f := newFixture(t, 1, func(env Env, p *FakeProcess) {
		envs <- env
		<-release
		PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
			return h.Send(FinishGame{Reason: "ok"})
		})
	})
	f.m.cfg.HelloTimeout = 3 * time.Second
	rec := newRecorder()
	if _, err := f.m.Start(practice, rec.Events()); err != nil {
		t.Fatal(err)
	}
	env := <-envs
	for _, bad := range []Env{
		{Control: env.Control, Match: env.Match, Token: "wrong"},
		{Control: env.Control, Match: env.Match + 100, Token: env.Token},
	} {
		h, err := DialHost(bad, 1, "rogue")
		if err != nil {
			t.Fatal(err)
		}
		if msg, err := h.Recv(2 * time.Second); err == nil {
			t.Errorf("rogue host got %v", msg)
		}
		h.Close()
	}
	// not a hello at all
	c, err := net.Dial("tcp4", env.Control)
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte(`{"t":"gameReady","port":1}` + "\n"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := c.Read(make([]byte, 10)); err == nil {
		t.Errorf("non-hello connection got %d bytes", n)
	}
	c.Close()
	close(release)
	if o := rec.wait(t); !o.Finished || o.Killed {
		t.Errorf("outcome %+v", o)
	}
	log := f.log.String()
	if strings.Count(log, "rejected: unknown match") != 2 || !strings.Contains(log, "rejected: first line is not hello") {
		t.Errorf("log:\n%s", log)
	}
}

// Each timeout kills the host: no hello, no gameReady, no heartbeat, no exit after logoutGMS.
func TestTimeouts(t *testing.T) {
	for _, c := range []struct {
		name   string
		run    func(env Env, p *FakeProcess)
		reason string
		ready  bool
	}{
		{"hello", func(env Env, p *FakeProcess) { <-p.Killed() }, "no hello within 500ms", false},
		{"ready", func(env Env, p *FakeProcess) {
			h, err := DialHost(env, p.Pid(), "slow")
			if err == nil {
				h.Recv(5 * time.Second) // createGame, then nothing
				<-p.Killed()
				h.Close()
			}
		}, "no gameReady within 500ms", false},
		{"heartbeat", func(env Env, p *FakeProcess) {
			PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
				h.Send(GMSUpdate{Phase: PhasePreGame})
				<-p.Killed()
				return errors.New("killed")
			})
		}, "no message within 500ms", true},
		{"exit", func(env Env, p *FakeProcess) {
			PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
				h.Send(LogoutGMS{})
				<-p.Killed() // the server must kill it; the connection stays open
				return errors.New("killed")
			})
		}, "no exit within 300ms of logoutGMS", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, 1, c.run)
			rec := newRecorder()
			if _, err := f.m.Start(practice, rec.Events()); err != nil {
				t.Fatal(err)
			}
			o := rec.wait(t)
			if !o.Killed || o.Finished || o.Reason != c.reason || o.Ready != c.ready || o.ExitCode != 1 {
				t.Errorf("outcome %+v", o)
			}
			if f.m.cfg.Pool.Free() != 1 {
				t.Error("port not released")
			}
			if !strings.Contains(f.log.String(), "killing pid ") {
				t.Errorf("log:\n%s", f.log.String())
			}
		})
	}
}

// Abort: the host gets abortGame and exits by itself; a host that ignores it is killed after the exit
// timeout; before the hello the host is killed at once. RemovePlayer is forwarded.
func TestAbort(t *testing.T) {
	obedient := func(env Env, p *FakeProcess) {
		PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
			for {
				msg, err := h.Recv(5 * time.Second)
				if err != nil {
					return err
				}
				if a, ok := msg.(AbortGame); ok {
					h.Send(GMSUpdate{Phase: "aborted:" + a.Reason})
					return nil
				}
				if r, ok := msg.(RemovePlayer); ok {
					h.Send(DisconnectPlayer{User: r.User, Reason: "removed " + r.Reason})
				}
			}
		})
	}
	t.Run("obeyed", func(t *testing.T) {
		f := newFixture(t, 1, obedient)
		f.m.cfg.HeartbeatTimeout = 5 * time.Second
		rec := newRecorder()
		mt, err := f.m.Start(practice, rec.Events())
		if err != nil {
			t.Fatal(err)
		}
		<-rec.ready
		mt.RemovePlayer(2000000001, "PLAYER_LEFT")
		mt.Abort("no players left")
		o := rec.wait(t)
		if o.Killed || o.Finished || o.Reason != "abortGame: no players left" || o.ExitCode != 0 {
			t.Errorf("outcome %+v", o)
		}
		want := []string{"ready 20042", "gone 2000000001 removed PLAYER_LEFT", "update aborted:no players left", "end"}
		if got := rec.list(); !reflect.DeepEqual(got, want) {
			t.Errorf("events %v", got)
		}
	})
	t.Run("ignored", func(t *testing.T) {
		f := newFixture(t, 1, func(env Env, p *FakeProcess) {
			PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
				for {
					if _, err := h.Recv(0); err != nil {
						return err
					}
					h.Send(GMSUpdate{Phase: PhaseInGame}) // keeps talking, never exits
				}
			})
		})
		f.m.cfg.HeartbeatTimeout = 5 * time.Second
		rec := newRecorder()
		mt, err := f.m.Start(practice, rec.Events())
		if err != nil {
			t.Fatal(err)
		}
		<-rec.ready
		mt.Abort("test")
		o := rec.wait(t)
		if !o.Killed || o.Reason != "abortGame: test" {
			t.Errorf("outcome %+v", o)
		}
		if !strings.Contains(f.log.String(), "killing pid ") || !strings.Contains(f.log.String(), "no exit within 300ms of abortGame") {
			t.Errorf("log:\n%s", f.log.String())
		}
	})
	t.Run("before hello", func(t *testing.T) {
		f := newFixture(t, 1, func(env Env, p *FakeProcess) { <-p.Killed() })
		f.m.cfg.HelloTimeout = 5 * time.Second
		rec := newRecorder()
		mt, err := f.m.Start(practice, rec.Events())
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		mt.Abort("early")
		o := rec.wait(t)
		if !o.Killed || o.Ready || o.Reason != "abortGame: early" || time.Since(start) > 2*time.Second {
			t.Errorf("outcome %+v after %v", o, time.Since(start))
		}
	})
}

// A host that exits (crashes) without finishGame is an abort; so is one whose control connection
// drops while the process lives on (it is killed).
func TestHostDies(t *testing.T) {
	t.Run("crash", func(t *testing.T) {
		f := newFixture(t, 1, func(env Env, p *FakeProcess) {
			PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
				h.Send(AddPlayer{User: 2000000001})
				h.Close()
				p.Exit(3)
				return errors.New("crashed")
			})
		})
		rec := newRecorder()
		if _, err := f.m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		o := rec.wait(t)
		if o.Killed || o.Finished || !o.Ready || o.ExitCode != 3 || o.Reason != "exited with code 3 without finishGame" {
			t.Errorf("outcome %+v", o)
		}
		if got := rec.list(); !reflect.DeepEqual(got, []string{"ready 20042", "add 2000000001/0", "end"}) {
			t.Errorf("events %v", got)
		}
	})
	t.Run("connection lost", func(t *testing.T) {
		f := newFixture(t, 1, func(env Env, p *FakeProcess) {
			PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
				h.Close()
				<-p.Killed()
				return errors.New("killed")
			})
		})
		rec := newRecorder()
		if _, err := f.m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		if o := rec.wait(t); !o.Killed || o.Reason != "control connection lost" {
			t.Errorf("outcome %+v", o)
		}
	})
}

// H5: a host that gives up (a fatal reportGMSError: its watchdog saw the tick stall, or a step failed in
// every run) ends the match as aborted with that reason and its port is free for the next game; one that
// doesn't exit after it is killed; a fatal report after finishGame leaves the result; non-fatal reports
// (step exceptions) are only counted.
func TestHostGivesUp(t *testing.T) {
	stalled := GMSError{Code: ErrTickStalled, Text: "no server tick for 10.0 s (tick 1834, match 61.0 s)", Fatal: true}
	stepped := GMSError{Code: ErrStepException, Text: "step tick.lua threw C0000005 at 0085C1A3 (exception #1 of this step)"}
	t.Run("stalled, then the next game", func(t *testing.T) {
		var n atomic.Int32
		f := newFixture(t, 1, func(env Env, p *FakeProcess) {
			first := n.Add(1) == 1
			PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
				if !first { // the next game on the freed port: a normal one
					return h.Send(FinishGame{WinnerTeam: 2, Reason: "guardian"})
				}
				for _, m := range []Message{AddPlayer{User: 2000000001}, stepped, stalled, LogoutGMS{}} {
					if err := h.Send(m); err != nil {
						return err
					}
				}
				h.Close()
				p.Exit(6) // the watchdog's exit code
				return errors.New("stalled")
			})
		})
		rec := newRecorder()
		mt, err := f.m.Start(practice, rec.Events())
		if err != nil {
			t.Fatal(err)
		}
		o := rec.wait(t)
		if !o.Ready || o.Finished || o.Killed || o.ExitCode != 6 || o.Errors != 1 || o.Error == nil ||
			*o.Error != stalled || o.Reason != "host error 2 (tick stalled): "+stalled.Text {
			t.Errorf("outcome %+v (error %+v)", o, o.Error)
		}
		if got := rec.list(); !reflect.DeepEqual(got, []string{"ready 20042", "add 2000000001/0", "end"}) {
			t.Errorf("events %v", got)
		}
		<-mt.Done()
		if f.m.Running() != 0 || f.m.cfg.Pool.Free() != 1 {
			t.Errorf("running %d, free ports %d", f.m.Running(), f.m.cfg.Pool.Free())
		}
		log := f.log.String()
		for _, want := range []string{
			"[host] match 1: host error 4 (step exception, fatal false): step tick.lua threw",
			"[host] match 1: host error 2 (tick stalled, fatal true): no server tick for 10.0 s",
			": aborted: host error 2 (tick stalled): no server tick for 10.0 s (tick 1834, match 61.0 s); exit code 6, " +
				"killed false; port 20042 free",
		} {
			if !strings.Contains(log, want) {
				t.Errorf("log lacks %q:\n%s", want, log)
			}
		}
		// the slot is free: the next game gets a host on the same port
		rec2 := newRecorder()
		mt2, err := f.m.Start(practice, rec2.Events())
		if err != nil {
			t.Fatal(err)
		}
		if mt2.Port != 20042 {
			t.Errorf("next game on port %d", mt2.Port)
		}
		if o := rec2.wait(t); !o.Finished || o.Error != nil || o.ExitCode != 0 || o.Reason != "guardian" {
			t.Errorf("next game: outcome %+v", o)
		}
	})
	t.Run("no exit after it", func(t *testing.T) {
		failing := GMSError{Code: ErrStepFailing, Text: "step tick.lua threw in every run for 10.0 s", Fatal: true}
		f := newFixture(t, 1, func(env Env, p *FakeProcess) {
			PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
				h.Send(failing)
				<-p.Killed() // keeps the connection, never exits: the exit timeout kills it
				return errors.New("killed")
			})
		})
		f.m.cfg.HeartbeatTimeout = 10 * time.Second
		rec := newRecorder()
		if _, err := f.m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		o := rec.wait(t)
		if !o.Killed || o.Finished || o.Error == nil || o.Error.Code != ErrStepFailing ||
			o.Reason != "host error 3 (step failing): "+failing.Text {
			t.Errorf("outcome %+v", o)
		}
		if !strings.Contains(f.log.String(), "killing pid ") || !strings.Contains(f.log.String(), "no exit within 300ms of a fatal reportGMSError") {
			t.Errorf("log:\n%s", f.log.String())
		}
	})
	t.Run("after finishGame", func(t *testing.T) {
		f := newFixture(t, 1, func(env Env, p *FakeProcess) {
			PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
				for _, m := range []Message{FinishGame{WinnerTeam: 1, Reason: "guardian"}, stalled, LogoutGMS{}} {
					h.Send(m)
				}
				h.Close()
				p.Exit(6)
				return errors.New("stalled in the post-game")
			})
		})
		rec := newRecorder()
		if _, err := f.m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		if o := rec.wait(t); !o.Finished || o.Reason != "guardian" || o.Error == nil || o.ExitCode != 6 {
			t.Errorf("outcome %+v", o)
		}
	})
	t.Run("not fatal", func(t *testing.T) {
		f := newFixture(t, 1, func(env Env, p *FakeProcess) {
			PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
				for _, m := range []Message{stepped, stepped, FinishGame{WinnerTeam: 1, Reason: "guardian"}} {
					if err := h.Send(m); err != nil {
						return err
					}
				}
				return nil
			})
		})
		rec := newRecorder()
		if _, err := f.m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		if o := rec.wait(t); !o.Finished || o.Error != nil || o.Errors != 2 || o.Killed || o.ExitCode != 0 {
			t.Errorf("outcome %+v", o)
		}
	})
}

// One port per running match: a full pool refuses the next match until one ends; a failed launch
// gives its port back.
func TestPoolExhaustion(t *testing.T) {
	finish := make(chan struct{})
	f := newFixture(t, 2, func(env Env, p *FakeProcess) {
		PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
			<-finish
			return h.Send(FinishGame{Reason: "done"})
		})
	})
	f.m.cfg.HeartbeatTimeout = 10 * time.Second
	var recs []*recorder
	for i := range 2 {
		rec := newRecorder()
		mt, err := f.m.Start(practice, rec.Events())
		if err != nil {
			t.Fatal(err)
		}
		if mt.Port != 20042+i {
			t.Errorf("match %d port %d", i, mt.Port)
		}
		<-rec.ready
		recs = append(recs, rec)
	}
	if _, err := f.m.Start(practice, Events{}); !errors.Is(err, ErrNoPort) {
		t.Fatalf("third match: %v", err)
	}
	if !strings.Contains(f.log.String(), "match 3 (game 1): not started: gmshost: no free GMS port") {
		t.Errorf("log:\n%s", f.log.String())
	}
	close(finish)
	for _, rec := range recs {
		rec.wait(t)
	}
	f.l.Err = errors.New("no such exe")
	if _, err := f.m.Start(practice, Events{}); err == nil || err.Error() != "no such exe" {
		t.Fatalf("launch error: %v", err)
	}
	if f.m.cfg.Pool.Free() != 2 {
		t.Errorf("free ports %d", f.m.cfg.Pool.Free())
	}
}

// Close kills every running host and refuses new matches.
func TestClose(t *testing.T) {
	f := newFixture(t, 2, func(env Env, p *FakeProcess) {
		PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
			for {
				if _, err := h.Recv(0); err != nil {
					return err
				}
			}
		})
	})
	f.m.cfg.HeartbeatTimeout = 10 * time.Second
	rec := newRecorder()
	if _, err := f.m.Start(practice, rec.Events()); err != nil {
		t.Fatal(err)
	}
	<-rec.ready
	f.m.Close()
	if o := rec.wait(t); !o.Killed || o.Reason != "server shutting down" {
		t.Errorf("outcome %+v", o)
	}
	if _, err := f.m.Start(practice, Events{}); !errors.Is(err, ErrClosed) {
		t.Errorf("start after close: %v", err)
	}
}

// The C++ side (gms/core/control) against this supervisor: the fake host built by that folder's
// CMakeLists (dg_control_fakehost.exe) plays a whole match, and an aborted one. Skipped unless
// DG_CONTROL_FAKEHOST names the exe (the Go tests don't build C++).
func TestCppFakeHost(t *testing.T) {
	exe := os.Getenv("DG_CONTROL_FAKEHOST")
	if exe == "" {
		t.Skip("DG_CONTROL_FAKEHOST not set (build gms/core/control, see its CMakeLists.txt)")
	}
	for _, mode := range []string{"finish", "abort", "karma", "stall"} {
		t.Run(mode, func(t *testing.T) {
			l := &ExecLauncher{Path: exe}
			if mode != "finish" {
				l.Args = []string{mode}
			}
			log := &syncBuf{}
			m := NewManager(Config{Log: logging.New(log), Launcher: l, Pool: NewPool(20042, 20042),
				HelloTimeout: 10 * time.Second, ReadyTimeout: 10 * time.Second, HeartbeatTimeout: 10 * time.Second})
			if err := m.Listen("127.0.0.1:0"); err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			rec := newRecorder()
			mt, err := m.Start(practice, rec.Events())
			if err != nil {
				t.Fatal(err)
			}
			<-rec.ready
			if mode == "abort" {
				mt.Abort("interop")
			}
			o := rec.wait(t)
			want := []string{"ready 20042", "add 2000000001/0", "update inGame", "player 2000000001 map[kills:3]",
				"finish 1 fake host: SM_Map_v5", "end"}
			switch mode {
			case "abort":
				want = []string{"ready 20042", "end"}
			case "karma": // D8c: playerKarma after finishGame; the host exits 0 only with playerRewards carrying the karma
				want = []string{"ready 20042", "add 2000000001/0", "update inGame", "player 2000000001 map[kills:3]",
					"finish 1 fake host: SM_Map_v5", "karma 2000000001 0/1/2", "end"}
			case "stall": // H5: the watchdog's report (fatal reportGMSError tick stalled), logoutGMS, exit code 6
				want = []string{"ready 20042", "add 2000000001/0", "end"}
			}
			wantCode, finished := 0, mode != "abort" && mode != "stall"
			if mode == "stall" {
				wantCode = 6
				if o.Error == nil || o.Error.Code != ErrTickStalled ||
					o.Reason != "host error 2 (tick stalled): no server tick for 10.0 s (fake host)" {
					t.Errorf("stall: outcome %+v (error %+v)", o, o.Error)
				}
			}
			if got := rec.list(); !reflect.DeepEqual(got, want) || o.Killed || o.ExitCode != wantCode || o.Finished != finished {
				t.Errorf("events %v, outcome %+v\n%s", got, o, log.String())
			}
			if !strings.Contains(log.String(), fmt.Sprintf("match 1: hello from pid %d (fake_host.cpp)", mt.Pid())) {
				t.Errorf("log:\n%s", log.String())
			}
		})
	}
}

// TestScriptHost runs gms/script's gms_script.exe in host mode (C1b) under the supervisor, as
// run_session.ps1 -GmsHost does through dgserver: hello, createGame, the GMS port bound, gameReady,
// then abortGame (logoutGMS, exit 0); --exit-before-ready (exit 3 without gameReady); --ready-delay-ms
// (gameReady late). With DataDir its stdout lands in <dir>\<match>\stdout.txt. Set DG_GMS_SCRIPT to the
// exe (gms\build\Release\gms_script.exe) to run it.
func TestScriptHost(t *testing.T) {
	exe := os.Getenv("DG_GMS_SCRIPT")
	if exe == "" {
		t.Skip("DG_GMS_SCRIPT not set (build gms/, see gms/CMakeLists.txt)")
	}
	for _, mode := range []string{"abort", "before-ready", "ready-delay"} {
		t.Run(mode, func(t *testing.T) {
			data := t.TempDir()
			l := &ExecLauncher{Path: exe, Args: []string{"--script-a"}, DataDir: data, Options: "unused=1"}
			switch mode {
			case "before-ready":
				l.Args = append(l.Args, "--exit-before-ready")
			case "ready-delay":
				l.Args = append(l.Args, "--ready-delay-ms", "1500")
			}
			log := &syncBuf{}
			m := NewManager(Config{Log: logging.New(log), Launcher: l, Pool: NewPool(20043, 20043),
				HelloTimeout: 10 * time.Second, ReadyTimeout: 10 * time.Second, HeartbeatTimeout: 10 * time.Second})
			if err := m.Listen("127.0.0.1:0"); err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			rec := newRecorder()
			start := time.Now()
			mt, err := m.Start(practice, rec.Events())
			if err != nil {
				t.Fatal(err)
			}
			var o Outcome
			if mode == "before-ready" {
				o = rec.wait(t)
				if o.Ready || o.Killed || o.ExitCode != 3 || o.Finished {
					t.Errorf("outcome %+v\n%s", o, log.String())
				}
			} else {
				select {
				case <-rec.ready:
				case <-time.After(10 * time.Second):
					t.Fatalf("no gameReady\n%s", log.String())
				}
				readyAfter := time.Since(start)
				if mode == "ready-delay" && readyAfter < 1400*time.Millisecond {
					t.Errorf("gameReady after %v, want >= 1.5 s", readyAfter)
				}
				mt.Abort("interop")
				o = rec.wait(t)
				if !o.Ready || o.Killed || o.ExitCode != 0 || o.Finished || o.Reason != "abortGame: interop" {
					t.Errorf("outcome %+v\n%s", o, log.String())
				}
				if !strings.Contains(log.String(), `match 1 <- {"t":"logoutGMS"}`) {
					t.Errorf("no logoutGMS:\n%s", log.String())
				}
			}
			if !strings.Contains(log.String(), fmt.Sprintf("match 1: hello from pid %d (gms_script)", mt.Pid())) {
				t.Errorf("log:\n%s", log.String())
			}
			out, err := os.ReadFile(filepath.Join(data, "1", "stdout.txt"))
			if err != nil || !strings.Contains(string(out), "host: createGame: game 1, practice, level SM_Map_v5 (0x946d41fe), seed 7919, 1 roster entry") {
				t.Errorf("stdout.txt (%v):\n%s", err, out)
			}
		})
	}
}

// --- a real process: this test binary re-executed as a fake host ---

const (
	helperEnv   = "GMSHOST_TEST_HELPER"
	launcherEnv = "GMSHOST_TEST_LAUNCHER" // H2b: the test binary as a process that starts a host
)

// TestMain lets the test binary play a host process (ExecLauncher tests): with GMSHOST_TEST_HELPER
// set it runs that scenario against DGGMS_* and exits instead of running the tests. With
// GMSHOST_TEST_LAUNCHER set it plays the process that starts a host (helperLauncher, H2b).
func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		os.Exit(helperHost(mode))
	}
	if os.Getenv(launcherEnv) != "" {
		os.Exit(helperLauncher())
	}
	os.Exit(m.Run())
}

// helperLauncher starts this test binary as a "sleep" host with an ExecLauncher, prints "host <pid>"
// and waits until it is killed (job_windows_test.go: the host must die with it).
func helperLauncher() int {
	exe, err := os.Executable()
	if err != nil {
		return 20
	}
	os.Unsetenv(launcherEnv)
	os.Setenv(helperEnv, "sleep") // for the host only: TestMain has already looked
	p, err := (&ExecLauncher{Path: exe, Args: []string{"-test.run=^$"}}).Launch(Env{Match: 1})
	if err != nil {
		fmt.Println("error", err)
		return 21
	}
	fmt.Printf("host %d\n", p.Pid())
	time.Sleep(2 * time.Minute) // until the test kills us
	return 22
}

func helperHost(mode string) int {
	if mode == "sleep" { // H2b: a host that only runs, without a control connection, until it is killed
		fmt.Println("running")
		time.Sleep(2 * time.Minute)
		return 14
	}
	env, ok, err := EnvFromLookup(os.LookupEnv)
	if !ok || err != nil {
		return 10
	}
	h, err := DialHost(env, os.Getpid(), "helper "+mode)
	if err != nil {
		return 11
	}
	msg, err := h.Recv(10 * time.Second)
	cg, isCreate := msg.(CreateGame)
	if err != nil || !isCreate || cg.GameID != practice.GameID {
		return 12
	}
	if mode == "hang" {
		time.Sleep(time.Minute) // until the server kills us
		return 13
	}
	_, port, _ := net.SplitHostPort(env.Bind)
	var n int
	fmt.Sscan(port, &n)
	h.Send(GameReady{Port: n})
	if mode == "crash" {
		return 3
	}
	h.Send(FinishGame{WinnerTeam: 2, DurationMs: 1, Reason: "helper"})
	h.Send(LogoutGMS{})
	h.Close()
	return 0
}

func TestExecLauncher(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		mode     string
		finished bool
		killed   bool
		code     int
	}{
		{"finish", true, false, 0},
		{"crash", false, false, 3},
		{"hang", false, true, killedExitCode},
	} {
		t.Run(c.mode, func(t *testing.T) {
			prepared := 0
			l := &ExecLauncher{Path: exe, Args: []string{"-test.run=^$"}, Prepare: func() error { prepared++; return nil }}
			t.Setenv(helperEnv, c.mode) // inherited by the child through os.Environ()
			log := &syncBuf{}
			m := NewManager(Config{Log: logging.New(log), Launcher: l, Pool: NewPool(20042, 20042),
				HelloTimeout: 20 * time.Second, ReadyTimeout: time.Second, HeartbeatTimeout: 20 * time.Second,
				ExitTimeout: 5 * time.Second})
			if err := m.Listen("127.0.0.1:0"); err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			rec := newRecorder()
			mt, err := m.Start(practice, rec.Events())
			if err != nil {
				t.Fatal(err)
			}
			o := rec.wait(t)
			if o.Finished != c.finished || o.Killed != c.killed || o.ExitCode != c.code {
				t.Errorf("outcome %+v\n%s", o, log.String())
			}
			if prepared != 1 {
				t.Errorf("Prepare ran %d times", prepared)
			}
			if !strings.Contains(log.String(), fmt.Sprintf("match 1: hello from pid %d (helper %s)", mt.Pid(), c.mode)) {
				t.Errorf("log:\n%s", log.String())
			}
		})
	}
}
