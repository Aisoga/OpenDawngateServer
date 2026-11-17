package gmshost

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// bigStart is a custom game's startGame whose line is about size bytes (ten players with a long collection each).
func bigStart(size int) StartGame {
	sg := StartGame{}
	per := size / 10
	for i := range 10 {
		var wards []string
		for n := 0; n*20 < per; n++ {
			wards = append(wards, fmt.Sprintf("ward.%013d", n))
		}
		sg.Roster = append(sg.Roster, RosterEntry{User: int64(2000000001 + i), Name: fmt.Sprintf("p%d", i), Index: i,
			Team: 1 + i/5, Wards: wards})
	}
	return sg
}

// customGame is a custom game's createGame (the lobby's admin alone).
var customGame = CreateGame{GameID: 7, Level: "SM_Map_v5", Mode: ModeCustom, Seed: 1,
	Roster: []RosterEntry{{User: 2000000001, Name: "admin", Index: 0, Team: 1}}}

// CTLLINE: the server never sends a line over the host's limit (LegacyMaxLine without FeatureLongLines, else MaxLine),
// logs both sizes instead, and logs a sent line by its start and size; Fits tells the callers beforehand.
func TestSendLineLimit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		features []string
		size     int
		arrives  bool
	}{
		{"older host, 70 KB", nil, 70 << 10, false},
		{"older host, 50 KB", nil, 50 << 10, true},
		{"longlines, 200 KB", []string{FeatureLongLines}, 200 << 10, true},
		{"longlines, over 1 MiB", []string{FeatureLongLines}, MaxLine + 4096, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := make(chan Message, 4)
			f := newFixture(t, 1, func(env Env, p *FakeProcess) {
				PlayHostWith(env, p, tc.features, func(h *HostConn, _ CreateGame) error {
					for {
						m, err := h.Recv(0)
						if err != nil {
							return err
						}
						got <- m
						if _, ok := m.(AbortGame); ok {
							return nil
						}
					}
				})
			}, func(c *Config) { c.HeartbeatTimeout = 10 * time.Second })
			rec := newRecorder()
			mt, err := f.m.Start(customGame, rec.Events())
			if err != nil {
				t.Fatal(err)
			}
			<-rec.ready
			sg := bigStart(tc.size)
			size, fits := mt.Fits(sg)
			line, _ := Encode(sg)
			if size != len(line)-1 || fits != tc.arrives {
				t.Errorf("Fits: %d, %v (line %d bytes, limit %d)", size, fits, len(line)-1, mt.LineLimit())
			}
			mt.StartGameWith(sg)
			mt.Abort("test over")
			first := <-got
			_, isStart := first.(StartGame)
			if isStart != tc.arrives {
				t.Errorf("first message %T, startGame expected %v", first, tc.arrives)
			}
			if isStart {
				if r := first.(StartGame).Roster; len(r) != 10 || len(r[9].Wards) != len(sg.Roster[9].Wards) {
					t.Errorf("startGame arrived cut: %d players", len(r))
				}
			}
			rec.wait(t)
			log := f.log.String()
			refused := fmt.Sprintf("match %d: (startGame not sent: %d bytes, over the host's line limit of %d bytes)",
				mt.ID, size, mt.LineLimit())
			clipped := fmt.Sprintf("... (%d bytes)", size)
			if strings.Contains(log, refused) == tc.arrives || strings.Contains(log, clipped) != tc.arrives {
				t.Errorf("log (refused %v, clipped %v):\n%.3000s", strings.Contains(log, refused),
					strings.Contains(log, clipped), log)
			}
			if !strings.Contains(log, fmt.Sprintf(`match %d -> {"t":"startGame","roster":[`, mt.ID)) && tc.arrives {
				t.Errorf("the sent line's start isn't logged")
			}
		})
	}
}

// CTLLINE: a host with FeatureStartAck that doesn't confirm startGame within StartTimeout is killed and the match
// ends; one that confirms plays on; a host without the feature has no deadline.
func TestStartDeadline(t *testing.T) {
	for _, tc := range []struct {
		name     string
		features []string
		ack      bool
		killed   bool
	}{
		{"no ack", []string{FeatureLongLines, FeatureStartAck}, false, true},
		{"ack", []string{FeatureLongLines, FeatureStartAck}, true, false},
		{"no feature", []string{FeatureLongLines}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, 1, func(env Env, p *FakeProcess) {
				PlayHostWith(env, p, tc.features, func(h *HostConn, _ CreateGame) error {
					m, err := h.Recv(5 * time.Second)
					if _, ok := m.(StartGame); err != nil || !ok {
						return fmt.Errorf("no startGame: %v %v", m, err)
					}
					if tc.ack {
						if err := h.Send(GMSUpdate{Phase: PhaseLoading, ElapsedMs: 900, Started: true}); err != nil {
							return err
						}
					}
					// longer than StartTimeout: a killed host's connection ends here
					if _, err := h.Recv(600 * time.Millisecond); err != nil {
						var ne net.Error
						if errors.As(err, &ne) && ne.Timeout() {
							return h.Send(FinishGame{WinnerTeam: 1, DurationMs: 1000, Reason: "guardian"})
						}
						return err
					}
					return nil
				})
			}, func(c *Config) { c.HeartbeatTimeout, c.StartTimeout = 10*time.Second, 200*time.Millisecond })
			rec := newRecorder()
			mt, err := f.m.Start(customGame, rec.Events())
			if err != nil {
				t.Fatal(err)
			}
			<-rec.ready
			mt.StartGame(customGame.Roster)
			o := rec.wait(t)
			log := f.log.String()
			kill := fmt.Sprintf("match %d: killing pid %d: no answer to startGame within 200ms", mt.ID, mt.Pid())
			if o.Killed != tc.killed || strings.Contains(log, kill) != tc.killed || o.Finished == tc.killed {
				t.Errorf("outcome %+v; log:\n%s", o, log)
			}
			if tc.killed && o.Reason != "no answer to startGame within 200ms" {
				t.Errorf("reason %q", o.Reason)
			}
			if confirmed := strings.Contains(log, "the host confirmed startGame"); confirmed != tc.ack {
				t.Errorf("confirmed %v; log:\n%s", confirmed, log)
			}
		})
	}
}

// CTLLINE: a host's hello and an agent's first lines are read with PreAuthMaxLine; a longer first line closes the
// connection before any check.
func TestPreAuthLineLimit(t *testing.T) {
	f := newFixture(t, 1, func(env Env, p *FakeProcess) {})
	c, err := net.Dial("tcp4", f.m.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	long := `{"t":"hello","build":"` + strings.Repeat("x", PreAuthMaxLine) + `"}` + "\n"
	go c.Write([]byte(long))
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Error("the connection stayed open")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(f.log.String(), "closed before hello: bufio.Scanner: token too long") {
		if time.Now().After(deadline) {
			t.Fatalf("log:\n%s", f.log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
