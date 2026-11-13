package dgserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
)

// historySeedKeys are the stats catalogue's keys (the match host's seat lines, gms/core/stats) that every seeded seat
// with details carries; the CC kinds and the role bonuses come and go with the seat.
var historySeedKeys = []string{"damageDealt", "damageDealtPhysical", "damageDealtMagical", "damageDealtTrue",
	"damageOverload", "largestHit", "overkill", "damageToShapers", "damageToMinions", "damageToJungle",
	"damageToObjectives", "damageToStructures", "damageToGuardian", "damageToWells", "damageToWorkers",
	"damageTaken", "damageTakenPhysical", "damageTakenMagical", "damageTakenTrue", "damageTakenFromShapers",
	"damageTakenFromNpcs", "damageAbsorbed", "shieldingDone", "healingDone", "healingSelf", "healingAllies",
	"healingOther", "healingReceived", "lifeDrain", "overheal", "killsMinions", "killsJungle", "killsObjectives",
	"killsStructures", "killsWorkers", "timeDeadMs", "wellsCaptured", "wardsPlaced",
	"goldTotal", "goldStart", "goldPassive", "goldFromMinion", "goldFromJungle", "goldFromTower", "goldFromPlayerKill",
	"goldFromAssist", "goldFromObjective", "goldFromWorkers", "goldFromWellPassive", "goldFromWellBounty", "goldRole",
	"xpTotal", "xpPassive", "xpFromMinion", "xpFromJungle", "xpFromTower", "xpFromPlayerKill", "xpFromAssist",
	"xpFromObjective", "xpRole", "ccCausedMs", "ccEnduredMs"}

// --test-history-seed: an empty history gets N matches with the login's persona in each (its account made without a
// password), every fifth with the basics only, the others with every catalogue key, sources that sum to the damage
// and healing, and a timeline whose lines end at the list's values; a history that holds a match is left alone.
func TestHistorySeed(t *testing.T) {
	st := store.New()
	c := historyContent(t)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	note, err := seedHistory(st, c, "12:mhist3@x.io", now)
	if err != nil || !strings.Contains(note, "seeded 12 made-up matches, mhist3 (mhist3@x.io) in every match") {
		t.Fatalf("seed: %q, %v", note, err)
	}
	cred, found, err := st.Credentials("mhist3@x.io")
	if err != nil || !found || !accounts.Verify(cred.Hash, historySeedPassword) {
		t.Fatalf("the login's account: %+v %v %v (want one with the password %s: SEEDPW)", cred, found, err, historySeedPassword)
	}
	if cred, found, err := st.Credentials("kestrel@seed.invalid"); err != nil || !found || cred.Hash != "" {
		t.Errorf("a made-up player's account: %+v %v %v (want one without a password)", cred, found, err)
	}
	_, me, ok := st.PersonaByName("mhist3")
	if !ok {
		t.Fatal("no persona mhist3")
	}
	mine, err := st.MatchesOf(me.ID, 0, 50)
	if err != nil || len(mine) != 12 {
		t.Fatalf("the player's matches: %d, %v", len(mine), err)
	}
	// PROFILE2: the stored profile (All Modes) holds the 11 matches that weren't made Unrated (the eighth was)
	if acct, _, _ := st.PersonaByID(me.ID); acct.Stats[progress.PlayerStats]["PLAYED"] != "11" || len(acct.History.Shapers) == 0 {
		t.Errorf("the seeded profile: %v, shapers %v", acct.Stats[progress.PlayerStats], acct.History.Shapers)
	}
	for id := int64(1); id <= 12; id++ {
		m, found, err := st.Match(id)
		if err != nil || !found || len(m.Seats) != 10 {
			t.Fatalf("match %d: %v %v, %d seats", id, found, err, len(m.Seats))
		}
		basic := (id-1)%5 == 4
		if basic != (m.Timeline == nil) {
			t.Errorf("match %d: basic %v, timeline %s", id, basic, m.Timeline)
		}
		humans := 0
		for _, s := range m.Seats {
			if !s.Bot {
				humans++
			}
			if s.Noun == 0 {
				t.Errorf("match %d seat %d: no shaper", id, s.Seat)
			}
			if basic {
				if s.Sources != nil || len(s.Stats) != map[bool]int{true: 6, false: 16}[s.Bot] {
					t.Errorf("match %d seat %d: a basic match with details: %v %s", id, s.Seat, s.Stats, s.Sources)
				}
				continue
			}
			if !s.Bot && (s.Stats["durationMs"] != m.DurationMs || s.Stats["role"] != int64(s.Role) || s.Stats["noun"] != int64(s.Noun)) {
				t.Errorf("match %d seat %d: playerFinish keys %v", id, s.Seat, s.Stats)
			}
			for _, k := range historySeedKeys {
				if _, ok := s.Stats[k]; !ok {
					t.Errorf("match %d seat %d: no %s", id, s.Seat, k)
				}
			}
			for _, k := range []string{"damageToWards", "killsWards", "wardsDestroyed"} { // dropped: always 0
				if _, ok := s.Stats[k]; ok {
					t.Errorf("match %d seat %d: the dropped key %s", id, s.Seat, k)
				}
			}
			var xs []gmshost.StatSource
			if err := json.Unmarshal(s.Sources, &xs); err != nil {
				t.Fatal(err)
			}
			var dmg, heal int64
			for _, x := range xs {
				dmg += x.Damage
				heal += x.Heal
			}
			if dmg != s.Stats["damageDealt"] || heal != s.Stats["healingDone"] {
				t.Errorf("match %d seat %d: sources %d/%d, stats %d/%d", id, s.Seat, dmg, heal, s.Stats["damageDealt"], s.Stats["healingDone"])
			}
			if s.Stats["goldTotal"] != s.Gold {
				t.Errorf("match %d seat %d: goldTotal %d, gold %d", id, s.Seat, s.Stats["goldTotal"], s.Gold)
			}
		}
		if m.Kind == "practice" && humans != 1 || m.Kind != "practice" && humans < 2 {
			t.Errorf("match %d (%s): %d humans", id, m.Kind, humans)
		}
		// PROFILE1: the kinds cycle through every game type, the eighth an Unrated draft game
		k := historySeedKinds[(id-1)%int64(len(historySeedKinds))]
		if m.Kind != k.kind || m.Rules != k.rules || m.Unrated != k.unrated {
			t.Errorf("match %d: %s %q unrated %v, want %+v", id, m.Kind, m.Rules, m.Unrated, k)
		}
		if want := []string{"standard", "practice", "standard", "standard", "practice", "draft", "custom", "draft"}[(id-1)%8]; historyType(m) != want {
			t.Errorf("match %d: type %s, want %s", id, historyType(m), want)
		}
		if basic {
			continue
		}
		var tl gmshost.MatchTimeline
		if err := json.Unmarshal(m.Timeline, &tl); err != nil {
			t.Fatal(err)
		}
		n := len(tl.Sec)
		if n == 0 || len(tl.Seats) != 10 || len(tl.Teams) != 2 || len(tl.Events) == 0 {
			t.Fatalf("match %d: timeline %d samples, %d seats, %d teams, %d events", id, n, len(tl.Seats), len(tl.Teams), len(tl.Events))
		}
		for _, ts := range tl.Seats {
			if len(ts.Gold) != n || ts.Gold[n-1] != m.Seats[ts.Index].Gold {
				t.Errorf("match %d seat %d: gold line %v, gold %d", id, ts.Index, ts.Gold, m.Seats[ts.Index].Gold)
			}
		}
		for i := 1; i < len(tl.Events); i++ {
			if tl.Events[i].Ms < tl.Events[i-1].Ms {
				t.Errorf("match %d: events out of order", id)
			}
		}
	}
	// The JSON answers: the player's list pages, with the names.
	h := newHistory(st, c, true, func() time.Time { return now }, func(string, string, ...any) {})
	code, body := ask(h, "games", "player=mhist3&me=mhist3&n=5")
	if code != 200 || !strings.Contains(body, `"more":true`) || !strings.Contains(body, `"name":"Kestrel"`) ||
		!strings.Contains(body, `"you":true`) {
		t.Errorf("games.json: %d %s", code, body[:min(len(body), 300)])
	}
	if note, err := seedHistory(st, c, "3", now); err != nil || !strings.Contains(note, "nothing seeded") {
		t.Errorf("second seed: %q, %v", note, err)
	}
	for _, bad := range []string{"", "0", "501", "x", "-2:a@b.c", "3:a@b.c|"} {
		if _, _, _, err := parseHistorySeed(bad); err == nil {
			t.Errorf("parseHistorySeed(%q) accepted", bad)
		}
	}
	if n, login, pw, err := parseHistorySeed("7"); err != nil || n != 7 || login != "" || pw != historySeedPassword {
		t.Errorf("parseHistorySeed(7): %d %q %q %v", n, login, pw, err)
	}
	if n, login, pw, err := parseHistorySeed("4:P1@X.io|secret 2"); err != nil || n != 4 || login != "P1@X.io" || pw != "secret 2" {
		t.Errorf("parseHistorySeed with a password: %d %q %q %v", n, login, pw, err)
	}
}

// SEEDPW: the seed's login account gets the password when it has none (an account of an earlier seed), and an
// account with a password keeps its own.
func TestHistorySeedPassword(t *testing.T) {
	st := store.New()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	if _, _, err := seedPersona(st, "old@x.io", "", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := seedPersona(st, "own@x.io", "mine", now); err != nil {
		t.Fatal(err)
	}
	if _, err := seedHistory(st, nil, "2:old@x.io|fresh", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := seedPersona(st, "own@x.io", "other", now); err != nil {
		t.Fatal(err)
	}
	for login, pw := range map[string]string{"old@x.io": "fresh", "own@x.io": "mine"} {
		if cred, found, err := st.Credentials(login); err != nil || !found || !accounts.Verify(cred.Hash, pw) {
			t.Errorf("%s: %+v %v %v, want the password %q", login, cred, found, err, pw)
		}
	}
}
