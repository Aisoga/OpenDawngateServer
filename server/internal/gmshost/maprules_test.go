package gmshost

import (
	"reflect"
	"strings"
	"testing"
)

// MAPRULES4: createGame's mapRules as the C++ codec writes it (the shared examples), each member omitted when 0, the
// field omitted when nil; the hello's feature name.
func TestMapRulesEncode(t *testing.T) {
	roster := []RosterEntry{{User: 2000000001, Name: "alpha", Index: 0, Team: 1}}
	for _, c := range []struct {
		msg  Message
		want string
	}{
		{CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: ModeCustom, Seed: 7919, Roster: roster, Descriptors: 2,
			MapRules: &MapRules{Kills: 20, Minutes: 15, StartLevel: 6}},
			`{"t":"createGame","gameId":1,"level":"SM_Map_v5","mode":"custom","seed":7919,"roster":[{"user":2000000001,` +
				`"name":"alpha","index":0,"team":1,"noun":"","bot":false}],"descriptors":2,"mapRules":{"kills":20,` +
				`"minutes":15,"startLevel":6}}`},
		{CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: ModePractice, Seed: 7919, Roster: roster, Descriptors: 1292,
			MapRules: &MapRules{Minutes: 3}},
			`{"t":"createGame","gameId":1,"level":"SM_Map_v5","mode":"practice","seed":7919,"roster":[{"user":2000000001,` +
				`"name":"alpha","index":0,"team":1,"noun":"","bot":false}],"descriptors":1292,"mapRules":{"minutes":3}}`},
		{CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: ModePractice, Seed: 7919, Roster: roster},
			`{"t":"createGame","gameId":1,"level":"SM_Map_v5","mode":"practice","seed":7919,"roster":[{"user":2000000001,` +
				`"name":"alpha","index":0,"team":1,"noun":"","bot":false}]}`},
		{Hello{Match: 1, Token: "00112233445566778899aabbccddeeff", PID: 4242, Build: "dgsim c1",
			Features: []string{FeatureRules, FeatureRules2, FeatureMapRules}},
			`{"t":"hello","match":1,"token":"00112233445566778899aabbccddeeff","pid":4242,"build":"dgsim c1",` +
				`"features":["rules","rules2","maprules"]}`},
	} {
		got, err := Encode(c.msg)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want+"\n" {
			t.Errorf("\n got %s\nwant %s", got, c.want)
		}
		back, err := Decode(got)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(back, c.msg) {
			t.Errorf("decoded %+v, want %+v", back, c.msg)
		}
	}
	// the lines of the shared examples file are among them (TestExamplesRoundTrip checks the round trip)
	all := strings.Join(examples(t), "\n")
	for _, want := range []string{`"mapRules":{"kills":20,"minutes":15,"startLevel":6}}`, `"mapRules":{"minutes":3}}`,
		`"features":["rules","rules2","maprules"]}`} {
		if !strings.Contains(all, want) {
			t.Errorf("the examples lack %s", want)
		}
	}
	if !(*MapRules)(nil).Empty() || !(&MapRules{}).Empty() || (&MapRules{StartLevel: 6}).Empty() {
		t.Error("Empty")
	}
	// a decoder reads null and {} as no rules, and refuses the wrong type
	for _, line := range []string{`{"t":"createGame","gameId":2}`, `{"t":"createGame","gameId":2,"mapRules":null}`} {
		msg, err := Decode([]byte(line))
		if err != nil || msg.(CreateGame).MapRules != nil {
			t.Errorf("%s: %+v, %v", line, msg, err)
		}
	}
	if msg, err := Decode([]byte(`{"t":"createGame","gameId":2,"mapRules":{}}`)); err != nil ||
		!msg.(CreateGame).MapRules.Empty() {
		t.Errorf("{}: %+v, %v", msg, err)
	}
	for _, line := range []string{`{"t":"createGame","mapRules":5}`, `{"t":"createGame","mapRules":{"kills":"20"}}`} {
		if _, err := Decode([]byte(line)); err == nil {
			t.Errorf("%s decoded", line)
		}
	}
}

// MAPRULES4: the manager hands createGame's mapRules only to a host whose hello names "maprules"; an older host gets
// the game without them (and the server's log says so), and empty rules are never sent.
func TestMapRulesStrip(t *testing.T) {
	game := practice
	game.MapRules = &MapRules{Kills: 3, Minutes: 3, StartLevel: 6}
	for _, c := range []struct {
		name     string
		features []string
		rules    *MapRules
		want     *MapRules
		logged   bool
	}{
		{"with the feature", []string{FeatureRules, FeatureRules2, FeatureMapRules}, game.MapRules, game.MapRules, false},
		{"an older host", []string{FeatureRules, FeatureRules2}, game.MapRules, nil, true},
		{"no features at all", nil, game.MapRules, nil, true},
		{"empty rules", []string{FeatureMapRules}, &MapRules{}, nil, false},
		{"no rules", []string{FeatureMapRules}, nil, nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := make(chan CreateGame, 1)
			f := newFixture(t, 1, func(env Env, p *FakeProcess) {
				PlayHostWith(env, p, c.features, func(h *HostConn, cg CreateGame) error {
					got <- cg
					for _, m := range []Message{FinishGame{WinnerTeam: 1, DurationMs: 60000, Reason: "kills"}} {
						if err := h.Send(m); err != nil {
							return err
						}
					}
					return nil
				})
			})
			g := practice
			g.MapRules = c.rules
			rec := newRecorder()
			if _, err := f.m.Start(g, rec.Events()); err != nil {
				t.Fatal(err)
			}
			o := rec.wait(t)
			cg := <-got
			if !reflect.DeepEqual(cg.MapRules, c.want) {
				t.Errorf("host got mapRules %+v, want %+v", cg.MapRules, c.want)
			}
			cg.MapRules = nil
			if !reflect.DeepEqual(cg, practice) {
				t.Errorf("host got %+v", cg)
			}
			// a rule's finishGame reason reaches the server as it is ("kills", "time", "suddenDeath")
			if !o.Finished || o.Reason != "kills" || o.Result.WinnerTeam != 1 {
				t.Errorf("outcome %+v", o)
			}
			log := f.log.String()
			if strings.Contains(log, "left out of createGame") != c.logged {
				t.Errorf("log (want the strip line %v):\n%s", c.logged, log)
			}
			if c.logged && !strings.Contains(log, `the map's rules (kills 3, minutes 3, start level 6) left out of `+
				`createGame: the host's hello lacks "maprules"`) {
				t.Errorf("log:\n%s", log)
			}
		})
	}
}

// WINBIND-B: the Binding race's wire as the C++ codec writes it (the shared examples): createGame's mapRules.bindings,
// startGame's victory, finishGame's bindings and the hello's "maprules2".
func TestMapRules2Encode(t *testing.T) {
	roster := []RosterEntry{{User: 2000000001, Name: "alpha", Index: 0, Team: 1}}
	fill := 4
	for _, c := range []struct {
		msg  Message
		want string
	}{
		{Hello{Match: 1, Token: "00112233445566778899aabbccddeeff", PID: 4242, Build: "dgsim c1",
			Features: []string{FeatureRules, FeatureRules2, FeatureMapRules, FeatureMapRules2}},
			`{"t":"hello","match":1,"token":"00112233445566778899aabbccddeeff","pid":4242,"build":"dgsim c1",` +
				`"features":["rules","rules2","maprules","maprules2"]}`},
		{CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: ModePractice, Seed: 7919, Roster: roster, Descriptors: 1292,
			MapRules: &MapRules{Minutes: 3, Bindings: 2}},
			`{"t":"createGame","gameId":1,"level":"SM_Map_v5","mode":"practice","seed":7919,"roster":[{"user":2000000001,` +
				`"name":"alpha","index":0,"team":1,"noun":"","bot":false}],"descriptors":1292,"mapRules":{"minutes":3,` +
				`"bindings":2}}`},
		{StartGame{Roster: roster, Rules: &Rules{Pick: "blind", ShaperPool: "all", Shop: "normal", ItemPool: "all",
			Victory: "bind1"}, FillTeams: &fill, Victory: &Victory{Bindings: 1}}, // WINLOBBY: the rules name it too
			`{"t":"startGame","roster":[{"user":2000000001,"name":"alpha","index":0,"team":1,"noun":"","bot":false}],` +
				`"rules":{"pick":"blind","shaperPool":"all","shop":"normal","itemPool":"all","victory":"bind1"},` +
				`"fillTeams":4,"victory":{"bindings":1}}`},
		{FinishGame{WinnerTeam: 2, DurationMs: 805546, Reason: "bindings", Bindings: []int{0, 1}},
			`{"t":"finishGame","winnerTeam":2,"durationMs":805546,"reason":"bindings","bindings":[0,1]}`},
		{FinishGame{WinnerTeam: 1, DurationMs: 180010, Reason: "time", Bindings: []int{2, 1}},
			`{"t":"finishGame","winnerTeam":1,"durationMs":180010,"reason":"time","bindings":[2,1]}`},
		{FinishGame{WinnerTeam: 1, DurationMs: 1234567, Reason: "guardian"},
			`{"t":"finishGame","winnerTeam":1,"durationMs":1234567,"reason":"guardian"}`},
	} {
		got, err := Encode(c.msg)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want+"\n" {
			t.Errorf("\n got %s\nwant %s", got, c.want)
		}
		back, err := Decode(got)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(back, c.msg) {
			t.Errorf("decoded %+v, want %+v", back, c.msg)
		}
		if _, ok := c.msg.(FinishGame); ok && c.msg.(FinishGame).Bindings == nil {
			continue // the guardian line is MAPRULES4's
		}
		if !strings.Contains(strings.Join(examples(t), "\n"), c.want) {
			t.Errorf("the examples lack %s", c.want)
		}
	}
	if !(*Victory)(nil).Empty() || !(&Victory{}).Empty() || (&Victory{Bindings: 1}).Empty() {
		t.Error("Victory.Empty")
	}
	if (&MapRules{Bindings: 1}).Empty() {
		t.Error("MapRules.Empty with bindings")
	}
}

// WINBIND-B: the manager hands mapRules.bindings only to a host whose hello names "maprules2"; a MAPRULES4 host gets
// the rest (or nothing, when bindings were all), with the log line; finishGame's bindings reach the outcome.
func TestMapRules2Strip(t *testing.T) {
	all := []string{FeatureRules, FeatureRules2, FeatureMapRules, FeatureMapRules2}
	mr4 := []string{FeatureRules, FeatureRules2, FeatureMapRules}
	for _, c := range []struct {
		name     string
		features []string
		rules    *MapRules
		want     *MapRules
		logged   string
	}{
		{"with maprules2", all, &MapRules{Minutes: 3, Bindings: 2}, &MapRules{Minutes: 3, Bindings: 2}, ""},
		{"a MAPRULES4 host", mr4, &MapRules{Minutes: 3, Bindings: 2}, &MapRules{Minutes: 3},
			`the map's Binding race (bindings 2) left out of createGame: the host's hello lacks "maprules2"`},
		{"a MAPRULES4 host, bindings only", mr4, &MapRules{Bindings: 1}, nil,
			`the map's Binding race (bindings 1) left out of createGame: the host's hello lacks "maprules2"`},
		{"an older host", []string{FeatureRules}, &MapRules{Kills: 3, Bindings: 1}, nil,
			`the map's rules (kills 3, minutes 0, start level 0, bindings 1) left out of createGame: the host's hello ` +
				`lacks "maprules"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := make(chan CreateGame, 1)
			f := newFixture(t, 1, func(env Env, p *FakeProcess) {
				PlayHostWith(env, p, c.features, func(h *HostConn, cg CreateGame) error {
					got <- cg
					return h.Send(FinishGame{WinnerTeam: 2, DurationMs: 60000, Reason: "bindings", Bindings: []int{0, 1}})
				})
			})
			g := practice
			g.MapRules = c.rules
			rec := newRecorder()
			if _, err := f.m.Start(g, rec.Events()); err != nil {
				t.Fatal(err)
			}
			o := rec.wait(t)
			cg := <-got
			if !reflect.DeepEqual(cg.MapRules, c.want) {
				t.Errorf("host got mapRules %+v, want %+v", cg.MapRules, c.want)
			}
			if !o.Finished || o.Reason != "bindings" || !reflect.DeepEqual(o.Result.Bindings, []int{0, 1}) {
				t.Errorf("outcome %+v", o)
			}
			log := f.log.String()
			if c.logged == "" && strings.Contains(log, "left out of createGame") {
				t.Errorf("log:\n%s", log)
			}
			if c.logged != "" && !strings.Contains(log, c.logged) {
				t.Errorf("log lacks %q:\n%s", c.logged, log)
			}
		})
	}
}
