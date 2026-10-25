package gmshost

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// examples reads docs/data/host_control_examples.jsonl, the lines both encoders must write.
func examples(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "docs", "data", "host_control_examples.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimRight(s.Text(), "\r")
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

// Every example decodes and encodes back byte for byte; together they cover every message type.
func TestExamplesRoundTrip(t *testing.T) {
	seen := map[string]bool{}
	for _, line := range examples(t) {
		msg, err := Decode([]byte(line))
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		seen[msg.Type()] = true
		got, err := Encode(msg)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != line+"\n" {
			t.Errorf("round trip\n got %s\nwant %s", got, line)
		}
	}
	for _, typ := range []string{THello, TCreateGame, TGameReady, TAddPlayer, TDisconnectPlayer, TReportGMSUpdate,
		TPlayerFinish, TFinishGame, TReportGMSError, TLogoutGMS, TRemovePlayer, TAbortGame, TPlayerRewards,
		TPlayerKarma, TUpdateRoster, TStartGame} {
		if !seen[typ] {
			t.Errorf("no example for %s", typ)
		}
	}
}

func TestEncodeValues(t *testing.T) {
	for _, c := range []struct {
		msg  Message
		want string
	}{
		{LogoutGMS{}, `{"t":"logoutGMS"}`},
		// B15: a custom game's lobby roster and its start (a spectator: team 0, index 10, "spectator":true)
		{UpdateRoster{Roster: []RosterEntry{{User: 2000000001, Name: "player", Team: 1}}},
			`{"t":"updateRoster","roster":[{"user":2000000001,"name":"player","index":0,"team":1,"noun":"","bot":false}]}`},
		{StartGame{Roster: []RosterEntry{{User: 2000000001, Name: "player", Team: 1},
			{User: 2000000002, Name: "second", Index: 10, Spectator: true}}},
			`{"t":"startGame","roster":[{"user":2000000001,"name":"player","index":0,"team":1,"noun":"","bot":false},` +
				`{"user":2000000002,"name":"second","index":10,"team":0,"noun":"","bot":false,"spectator":true}]}`},
		{GameReady{Port: 10042}, `{"t":"gameReady","port":10042}`},
		{CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: ModePractice, Seed: 7919,
			Roster: []RosterEntry{{User: 2000000001, Name: "player", Team: 1}}},
			`{"t":"createGame","gameId":1,"level":"SM_Map_v5","mode":"practice","seed":7919,` +
				`"roster":[{"user":2000000001,"name":"player","index":0,"team":1,"noun":"","bot":false}]}`},
		// D3-live step 4: shaper select's flag, timers and a human's content (empty content fields left out)
		{CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: ModePractice, Seed: 7919, ShaperSelect: true,
			Timers: map[string]int64{TimerShaperSelection: 8000, TimerReveal: 16},
			Roster: []RosterEntry{{User: 2000000001, Name: "player", Team: 1,
				Shapers: []ShaperOffer{{Noun: "alpha.Noun", Flags: ShaperOwned}, {Noun: "gunman.Noun", Flags: ShaperFreeRotation, Offer: 5}},
				Wards:   []string{"wardskin_default.Noun"}}}},
			`{"t":"createGame","gameId":1,"level":"SM_Map_v5","mode":"practice","seed":7919,` +
				`"roster":[{"user":2000000001,"name":"player","index":0,"team":1,"noun":"","bot":false,` +
				`"shapers":[{"noun":"alpha.Noun","flags":1},{"noun":"gunman.Noun","flags":2,"offer":5}],` +
				`"wards":["wardskin_default.Noun"]}],"timers":{"RevealTime":16,"ShaperSelectionTime":8000},"shaperSelect":true}`},
		// D3-perks: the human's perk pages after announcers (a page without stats has neither list)
		{CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: ModePractice, Seed: 7919, ShaperSelect: true,
			Roster: []RosterEntry{{User: 2000000001, Name: "player", Team: 1,
				Shapers: []ShaperOffer{{Noun: "alpha.Noun", Flags: ShaperOwned}},
				PerkPages: []PerkPage{{ID: 1, Version: 3, Attributes: []PerkAttribute{{Attribute: 0, Op: 1, Milli: 800},
					{Attribute: 2, Op: 1, Milli: 50000}}, Modifiers: []string{"modifier_MediumPerk_BushSpeed"}}, {ID: 2, Version: 1}}}}},
			`{"t":"createGame","gameId":1,"level":"SM_Map_v5","mode":"practice","seed":7919,` +
				`"roster":[{"user":2000000001,"name":"player","index":0,"team":1,"noun":"","bot":false,` +
				`"shapers":[{"noun":"alpha.Noun","flags":1}],"perkPages":[{"id":1,"version":3,"attributes":[{"attribute":0,"op":1,` +
				`"milli":800},{"attribute":2,"op":1,"milli":50000}],"modifiers":["modifier_MediumPerk_BushSpeed"]},` +
				`{"id":2,"version":1}]}],"shaperSelect":true}`},
		// B10d: a matchmade game with open seats asks for bots up to the team size (fillTeams, after shaperSelect;
		// omitted when 0, so every line above stays as it was)
		{CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: ModePvP, Seed: 7919, Descriptors: 1, FillTeams: 5,
			Roster: []RosterEntry{{User: 2000000001, Name: "alpha", Team: 1}}},
			`{"t":"createGame","gameId":1,"level":"SM_Map_v5","mode":"pvp","seed":7919,` +
				`"roster":[{"user":2000000001,"name":"alpha","index":0,"team":1,"noun":"","bot":false}],"descriptors":1,"fillTeams":5}`},
		// D8b: a party's members carry its playgroup id (party, last in the entry; omitted when 0, so every line above
		// stays as it was)
		{CreateGame{GameID: 5, Level: "SM_Map_v5", Mode: ModePvP, Seed: 39595, Descriptors: 1, FillTeams: 2,
			Roster: []RosterEntry{{User: 2000000001, Name: "leader", Team: 1, Party: 1},
				{User: 2000000002, Name: "guest", Index: 1, Team: 1, Party: 1}}},
			`{"t":"createGame","gameId":5,"level":"SM_Map_v5","mode":"pvp","seed":39595,` +
				`"roster":[{"user":2000000001,"name":"leader","index":0,"team":1,"noun":"","bot":false,"party":1},` +
				`{"user":2000000002,"name":"guest","index":1,"team":1,"noun":"","bot":false,"party":1}],"descriptors":1,"fillTeams":2}`},
		{GMSError{Code: 1, Text: "a<b>&" + string(rune(0x2028))}, `{"t":"reportGMSError","code":1,"text":"a<b>&` + "\\" + `u2028","fatal":false}`},
		// H5: the watchdog's report (control_tests.cpp's TestHostControlErrors sends the same line)
		{GMSError{Code: ErrTickStalled, Text: "no server tick for 10.0 s (tick 1834, match 61.0 s); the engine thread was in " +
			"step test.stall for 10.0 s, eip 77A42F6C (ntdll.dll+0x72F6C); minidump watchdog.dmp", Fatal: true},
			`{"t":"reportGMSError","code":2,"text":"no server tick for 10.0 s (tick 1834, match 61.0 s); the engine thread ` +
				`was in step test.stall for 10.0 s, eip 77A42F6C (ntdll.dll+0x72F6C); minidump watchdog.dmp","fatal":true}`},
		{PlayerFinish{User: 1, Stats: map[string]int64{"z": 1, "a": 2}}, `{"t":"playerFinish","user":1,"stats":{"a":2,"z":1}}`},
		// B7c: the host's own rewards in playerFinish, and the server's answer playerRewards (the C++ codec
		// writes the same lines since D8-live: control_tests.cpp, and the shared examples file)
		{PlayerFinish{User: 1, Rewards: &Rewards{LootBag: 0x103EBDF7, Destiny: 12, Items: []RewardItem{{CatalogID: 0x10A4638F}},
			FTUE: true, BaseRoll: 218, WinPoints: 200, WinAltPoints: 10}},
			`{"t":"playerFinish","user":1,"rewards":{"lootBag":272547319,"destiny":12,"cc":0,"items":[{"catalogId":279208847,` +
				`"duplicate":false,"alternate":0}],"ftue":true,"baseRoll":218,"winPoints":200,"winAltPoints":10,"firstWinPoints":0}}`},
		{PlayerRewards{User: 2000000001, Rewards: Rewards{LootBag: 0x4F079E93, Destiny: 8, CC: 50, FTUE: true, BaseRoll: 67,
			Items: []RewardItem{{CatalogID: 0x10A4638F, Duplicate: true, Alternate: 50}}}},
			`{"t":"playerRewards","user":2000000001,"rewards":{"lootBag":1325899411,"destiny":8,"cc":50,"items":[{"catalogId":279208847,` +
				`"duplicate":true,"alternate":50}],"ftue":true,"baseRoll":67,"winPoints":0,"winAltPoints":0,"firstWinPoints":0}}`},
		// D8c: a matchmade game's karma votes (host -> server) and the rewards decided with them (karma present;
		// omitted in every other playerRewards, so the lines above stay as they were)
		{PlayerKarma{User: 2000000002, Team: 1, Enemy: 2}, `{"t":"playerKarma","user":2000000002,"party":0,"team":1,"enemy":2}`},
		{PlayerRewards{User: 2000000002, Rewards: Rewards{LootBag: 0x4F079E93, Destiny: 24, Items: []RewardItem{}, BaseRoll: 218,
			WinAltPoints: 9, Karma: &Karma{Points: 30, Team: 1, Enemy: 2}}},
			`{"t":"playerRewards","user":2000000002,"rewards":{"lootBag":1325899411,"destiny":24,"cc":0,"items":[],"ftue":false,` +
				`"baseRoll":218,"winPoints":0,"winAltPoints":9,"firstWinPoints":0,"karma":{"points":30,"party":0,"team":1,"enemy":2}}}`},
		// H2: agent messages (Go only; docs/HOST_CONTROL.md *Remote host machines (H2)* has the same lines)
		{AgentHello{Name: "hostA", Nonce: "00112233445566778899aabbccddeeff", Build: "dgserver"},
			`{"t":"agentHello","name":"hostA","nonce":"00112233445566778899aabbccddeeff","build":"dgserver"}`},
		{AgentChallenge{Nonce: "ffeeddccbbaa99887766554433221100", Proof: "5d1c"},
			`{"t":"agentChallenge","nonce":"ffeeddccbbaa99887766554433221100","proof":"5d1c"}`},
		{AgentAuth{Proof: "9e07", Bind: "192.168.1.20", Public: "192.168.1.20", Ports: "10042-10051"},
			`{"t":"agentAuth","proof":"9e07","bind":"192.168.1.20","public":"192.168.1.20","ports":"10042-10051"}`},
		{AgentReady{Addr: "192.168.1.20"}, `{"t":"agentReady","addr":"192.168.1.20"}`},
		{AgentStatus{Hosts: []int64{3, 5}}, `{"t":"agentStatus","hosts":[3,5]}`},
		{LaunchHost{Match: 3, Token: "0123456789abcdef0123456789abcdef", Port: 10042},
			`{"t":"launchHost","match":3,"token":"0123456789abcdef0123456789abcdef","port":10042}`},
		{HostStarted{Match: 3, PID: 4242}, `{"t":"hostStarted","match":3,"pid":4242}`},
		{HostFailed{Match: 3, Error: "no Dawngate.exe"}, `{"t":"hostFailed","match":3,"error":"no Dawngate.exe"}`},
		{KillHost{Match: 3}, `{"t":"killHost","match":3}`},
		{HostExited{Match: 3, Code: -1}, `{"t":"hostExited","match":3,"code":-1}`},
	} {
		got, err := Encode(c.msg)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want+"\n" {
			t.Errorf("got %s want %s", got, c.want)
		}
		if back, err := Decode(got); err != nil || !reflect.DeepEqual(back, c.msg) {
			t.Errorf("decoded back: %#v (%v)", back, err)
		}
	}
}

func TestDecode(t *testing.T) {
	// unknown fields are ignored, missing ones are zero, values come back as values (not pointers)
	msg, err := Decode([]byte(`{"future":[1,{"x":2}],"port":7,"t":"gameReady"}` + "\r\n"))
	if err != nil || !reflect.DeepEqual(msg, GameReady{Port: 7}) {
		t.Errorf("got %#v, %v", msg, err)
	}
	msg, err = Decode([]byte(`{"t":"addPlayer"}`))
	if err != nil || !reflect.DeepEqual(msg, AddPlayer{}) {
		t.Errorf("got %#v, %v", msg, err)
	}
	// 64-bit ids survive
	msg, err = Decode([]byte(`{"t":"removePlayer","user":9223372036854775807}`))
	if err != nil || msg.(RemovePlayer).User != 1<<63-1 {
		t.Errorf("got %#v, %v", msg, err)
	}
	for _, c := range []struct{ line, err string }{
		{`{"t":"nope"}`, `unknown message type "nope"`},
		{`{"port":1}`, `line without "t"`},
		{`{"t":"gameReady","port":"x"}`, `bad gameReady`},
		{`{"t":"gameReady"`, `bad line`},
		{`[1]`, `bad line`},
		{`{"t":1}`, `bad line`},
	} {
		_, err := Decode([]byte(c.line))
		if err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: got %v, want %q", c.line, err, c.err)
		}
	}
	if _, err := Decode([]byte(`{"t":"nope"}`)); !errors.Is(err, ErrUnknownType) {
		t.Errorf("unknown type: %v", err)
	}
	// every registered type decodes into its own value type
	for typ, zero := range messageTypes {
		msg, err := Decode([]byte(`{"t":"` + typ + `"}`))
		if err != nil || reflect.TypeOf(msg) != reflect.TypeOf(zero) || msg.Type() != typ {
			t.Errorf("%s: %#v %v", typ, msg, err)
		}
	}
	// the log never shows a launchHost's token
	l := LaunchHost{Match: 3, Token: "0123456789abcdef0123456789abcdef", Port: 10042}
	line, _ := Encode(l)
	if got := logged(l, line); got != `{"t":"launchHost","match":3,"token":"...","port":10042}` {
		t.Errorf("logged %s", got)
	}
	if got := logged(KillHost{Match: 3}, []byte("{\"t\":\"killHost\",\"match\":3}\n")); got != `{"t":"killHost","match":3}` {
		t.Errorf("logged %s", got)
	}
}

// H5: reportGMSError's codes and names, the same numbers and names as gms/core/control (kErr*, ErrorCodeName).
func TestErrorNames(t *testing.T) {
	for _, c := range []struct {
		code int
		want string
	}{{ErrGMSEndpoint, "GMS endpoint"}, {ErrTickStalled, "tick stalled"}, {ErrStepFailing, "step failing"},
		{ErrStepException, "step exception"}, {ErrLevelLoad, "level load"}, {0, "error"}, {99, "error"}} {
		if got := ErrorName(c.code); got != c.want {
			t.Errorf("ErrorName(%d) = %q, want %q", c.code, got, c.want)
		}
	}
	if ErrGMSEndpoint != 1 || ErrTickStalled != 2 || ErrStepFailing != 3 || ErrStepException != 4 || ErrLevelLoad != 5 {
		t.Error("the codes moved: both sides and the example lines use 1-5")
	}
}

func TestLineReader(t *testing.T) {
	r := NewLineReader(strings.NewReader("a\r\n\n  \nb\nc"))
	for _, want := range []string{"a", "b", "c"} {
		line, err := r.Next()
		if err != nil || string(line) != want {
			t.Fatalf("got %q, %v; want %q", line, err, want)
		}
	}
	if _, err := r.Next(); err != io.EOF {
		t.Errorf("end: %v", err)
	}
	r = NewLineReader(strings.NewReader(strings.Repeat("x", MaxLine) + "\n" + strings.Repeat("y", MaxLine+1) + "\n"))
	if line, err := r.Next(); err != nil || len(line) != MaxLine {
		t.Errorf("MaxLine: %d, %v", len(line), err)
	}
	if _, err := r.Next(); !errors.Is(err, bufio.ErrTooLong) {
		t.Errorf("too long: %v", err)
	}
}

func TestEnv(t *testing.T) {
	e := Env{Control: "127.0.0.1:10040", Match: 3, Token: "tok", Bind: "127.0.0.1:10044"}
	vars := map[string]string{}
	for _, v := range e.Vars() {
		k, val, _ := strings.Cut(v, "=")
		vars[k] = val
	}
	lookup := func(k string) (string, bool) { v, ok := vars[k]; return v, ok }
	got, ok, err := EnvFromLookup(lookup)
	if !ok || err != nil || got != e {
		t.Errorf("got %+v %v %v", got, ok, err)
	}
	if _, ok, _ := EnvFromLookup(func(string) (string, bool) { return "", false }); ok {
		t.Error("no DGGMS_CONTROL must mean no host mode")
	}
	vars["DGGMS_MATCH"] = "x"
	if _, _, err := EnvFromLookup(lookup); err == nil {
		t.Error("bad DGGMS_MATCH accepted")
	}
}

func TestPorts(t *testing.T) {
	for _, c := range []struct {
		s           string
		first, last int
		ok          bool
	}{
		{"10042-10051", 10042, 10051, true}, {" 7 - 9 ", 7, 9, true}, {"10042", 10042, 10042, true},
		{"10-5", 0, 0, false}, {"0-5", 0, 0, false}, {"1-65536", 0, 0, false}, {"a-b", 0, 0, false}, {"", 0, 0, false},
		{"5-", 0, 0, false},
	} {
		f, l, err := ParsePorts(c.s)
		if (err == nil) != c.ok || f != c.first || l != c.last {
			t.Errorf("%q: %d %d %v", c.s, f, l, err)
		}
	}
	p := NewPool(10042, 10044)
	var got []int
	for range 3 {
		port, err := p.Acquire()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, port)
	}
	if !reflect.DeepEqual(got, []int{10042, 10043, 10044}) || p.Free() != 0 {
		t.Errorf("acquired %v, free %d", got, p.Free())
	}
	if _, err := p.Acquire(); !errors.Is(err, ErrNoPort) {
		t.Errorf("exhausted pool: %v", err)
	}
	p.Release(10043)
	p.Release(10043)
	p.Release(1) // not ours
	if port, err := p.Acquire(); port != 10043 || err != nil {
		t.Errorf("reuse: %d %v", port, err)
	}
	if p.String() != "10042-10044" {
		t.Error(p.String())
	}
}

func TestResolveExe(t *testing.T) {
	dir := t.TempDir()
	if _, err := ResolveExe(dir); err == nil {
		t.Error("empty dir accepted")
	}
	game := filepath.Join(dir, "Dawngate", "game")
	if err := os.MkdirAll(game, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(game, "Dawngate.exe")
	if err := os.WriteFile(exe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{dir, filepath.Join(dir, "Dawngate"), game, exe} {
		if got, err := ResolveExe(in); err != nil || got != exe {
			t.Errorf("%s: %s %v", in, got, err)
		}
	}
}
