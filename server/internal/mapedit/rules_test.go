package mapedit

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dawngate/server/internal/mapctool"
	"dawngate/server/internal/setup/mapc"
)

// The rules routes ask `mapc tuning --json`. These tests run the test binary as a stand-in mapc (TestMain calls
// fakeMapcMain): --fakemapc answers `tuning` in the reply's agreed shape (the level's values from fakeDefaults, the
// summary from mapc.DescribeRules of the map it was given on stdin) and passes every other command to mapctool;
// --oldmapc refuses `tuning` as a mapc from before it does. DG_FAKEMAPC_LOG names a file each `tuning` call adds a line
// to.

// fakeDefaults are the levels' values as the fake reports them (the shipped values the page's fallback holds too).
var fakeDefaults = map[string]map[string]any{
	"SM_Map_v5": {"startLevel": 1, "respawn": map[string]any{"base": 15, "perLevel": 0.5, "perMinute": 1.25}},
	"MM_Map_v3": {"startLevel": 1, "respawn": map[string]any{"base": 10, "perLevel": 0.5, "perMinute": 0.5}},
}

// fakeMapcMain runs the stand-in mapc for the test binary's arguments; ok is false when they aren't its.
func fakeMapcMain(args []string) (code int, ok bool) {
	if len(args) < 2 || (args[0] != "--fakemapc" && args[0] != "--oldmapc") {
		return 0, false
	}
	if args[1] != "tuning" {
		return mapctool.Run(args[1:], os.Stdout, os.Stderr), true
	}
	if f := os.Getenv("DG_FAKEMAPC_LOG"); f != "" {
		if lf, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(lf, strings.Join(args[1:], " "))
			lf.Close()
		}
	}
	if args[0] == "--oldmapc" {
		fmt.Fprintln(os.Stderr, `mapc: unknown command "tuning"`)
		return 2, true
	}
	level, mapArg := "", ""
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--level":
			i++
			level = args[i]
		case "--map":
			i++
			mapArg = args[i]
		}
	}
	out := map[string]any{"findings": []any{}}
	var m *mapc.Map
	if mapArg == "-" {
		b, _ := io.ReadAll(os.Stdin)
		var err error
		if m, err = mapc.Parse(b); err != nil {
			fmt.Fprintln(os.Stderr, "mapc tuning:", err)
			return 1, true
		}
		if level == "" {
			level = m.Tuning
		}
	}
	def, known := fakeDefaults[level]
	if !known {
		fmt.Fprintf(os.Stderr, "mapc tuning: level %q: one of SM_Map_v5, MM_Map_v3\n", level)
		return 1, true
	}
	out["level"], out["defaults"] = level, def
	base := def["respawn"].(map[string]any)["base"]
	c := map[string]any{"asset": "LevelGameplayTuning", "member": "deathTimeBase", "default": base, "value": base}
	if m != nil {
		out["summary"], out["short"] = mapc.DescribeRules(m.Rules)
		out["victoryChanged"], out["balanceChanged"] = m.Rules.VictoryChanged(), m.Rules.BalanceChanged()
		out["name"] = m.Name
		if r := m.Rules; r != nil && r.Respawn != nil && r.Respawn.Base != nil {
			c["value"] = *r.Respawn.Base
		}
	}
	out["constants"] = []any{c}
	json.NewEncoder(os.Stdout).Encode(out)
	return 0, true
}

// rulesEnv serves CM_Ring with the stand-in mapc (fake: "--fakemapc" or "--oldmapc") and a fresh call log.
func rulesEnv(t *testing.T, fake string) (*env, string) {
	t.Helper()
	if mapcExe == "" {
		t.Skip("no mapc")
	}
	logf := filepath.Join(t.TempDir(), "calls.txt")
	t.Setenv("DG_FAKEMAPC_LOG", logf)
	maps := t.TempDir()
	copyPackage(t, ringDir, filepath.Join(maps, "CM_Ring"))
	c := Config{Maps: maps, Mapc: mapcExe, MapcArgs: []string{fake}, Out: filepath.Join(t.TempDir(), "out"),
		Work: t.TempDir(), Cache: filepath.Join(t.TempDir(), "3d"), Pages: testPages(t, API)}
	return serve(t, c, maps), logf
}

func calls(logf string) []string {
	b, _ := os.ReadFile(logf)
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(b)), " ", "_"))
}

type rulesReply struct {
	OK             bool
	Error          string
	Level          string
	Name           string
	Defaults       map[string]any
	Summary, Short string
	VictoryChanged bool
	BalanceChanged bool
	Constants      []struct {
		Asset, Member  string
		Default, Value float64
	}
}

// TestRulesTuning: GET /api/tuning asks mapc for a level's values once (then from the cache), defaults to SM_Map_v5,
// and refuses a bad level name.
func TestRulesTuning(t *testing.T) {
	e, logf := rulesEnv(t, "--fakemapc")
	var r rulesReply
	if code := e.call("GET", "/api/tuning?level=MM_Map_v3", nil, &r); code != 200 || !r.OK || r.Level != "MM_Map_v3" ||
		r.Defaults["respawn"].(map[string]any)["base"] != 10.0 {
		t.Fatalf("MM_Map_v3: %d %+v", code, r)
	}
	if code := e.call("GET", "/api/tuning?level=MM_Map_v3", nil, &r); code != 200 || !r.OK {
		t.Fatalf("again: %d %+v", code, r)
	}
	if got := calls(logf); len(got) != 1 || got[0] != "tuning_--json_--level_MM_Map_v3" {
		t.Errorf("mapc asked %q, want once", got)
	}
	r = rulesReply{}
	if code := e.call("GET", "/api/tuning", nil, &r); code != 200 || !r.OK || r.Level != "SM_Map_v5" {
		t.Errorf("no level: %d %+v", code, r)
	}
	if code := e.call("GET", "/api/tuning?level=..%2Fx", nil, nil); code != 400 {
		t.Errorf("a bad level: %d", code)
	}
	r = rulesReply{}
	if code := e.call("GET", "/api/tuning?level=XX_Map", nil, &r); code != 200 || r.OK || !strings.Contains(r.Error, "XX_Map") {
		t.Errorf("an unknown level: %d %+v", code, r)
	}
}

// TestRulesDescribe: POST /api/rules/{pkg} gives mapc the draft (the document merged into map.json, as the check
// does) on its standard input and relays its reply: for each of DescribeRules' vectors the summary and the short text
// players read, the victory's and the balance's flags, the constants with this map's value.
func TestRulesDescribe(t *testing.T) {
	e, logf := rulesEnv(t, "--fakemapc")
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "maprules", "describe.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vec struct {
		Cases []struct {
			Name           string
			Rules          json.RawMessage
			Summary, Short string
		}
	}
	if err := json.Unmarshal(b, &vec); err != nil {
		t.Fatal(err)
	}
	doc, _, _ := e.open("CM_Ring")
	doc["name"] = "CM_Ring" // the draft's own name reaches mapc
	for _, c := range vec.Cases {
		var rules any
		json.Unmarshal(c.Rules, &rules)
		if rules == nil {
			delete(doc, "rules")
		} else {
			doc["rules"] = rules
		}
		var mr *mapc.Rules
		json.Unmarshal(c.Rules, &mr)
		var r rulesReply
		if code := e.call("POST", "/api/rules/CM_Ring", doc, &r); code != 200 || !r.OK {
			t.Fatalf("%s: %d %+v", c.Name, code, r)
		}
		if r.Summary != c.Summary || r.Short != c.Short || r.Name != "CM_Ring" || r.Level != "SM_Map_v5" ||
			r.VictoryChanged != mr.VictoryChanged() || r.BalanceChanged != mr.BalanceChanged() {
			t.Errorf("%s: %+v, want %q %q", c.Name, r, c.Summary, c.Short)
		}
	}
	if n := len(calls(logf)); n != len(vec.Cases) {
		t.Errorf("mapc asked %d times for %d drafts", n, len(vec.Cases))
	}
	// Anshavar Pass's pace and a changed respawn: the level and the constant's value follow the draft
	doc["tuning"] = "MM_Map_v3"
	doc["rules"] = map[string]any{"respawn": map[string]any{"base": 6}}
	var r rulesReply
	if code := e.call("POST", "/api/rules/CM_Ring", doc, &r); code != 200 || !r.OK || r.Level != "MM_Map_v3" ||
		len(r.Constants) != 1 || r.Constants[0].Default != 10 || r.Constants[0].Value != 6 ||
		r.Summary != "Respawn 6 s." || r.Short != "Tuned" {
		t.Errorf("MM_Map_v3 with respawn 6: %d %+v", code, r)
	}
	if code := e.call("POST", "/api/rules/NoSuchMap", doc, nil); code != 404 {
		t.Errorf("no package: %d", code)
	}
	if code := e.call("POST", "/api/rules/CM_Ring", "{not json", nil); code != 400 {
		t.Errorf("a broken document: %d", code)
	}
}

// TestRulesOldMapc: with a mapc that has no tuning command both routes say so (ok false, the reason) instead of
// failing, so the page falls back to its own values; the check still reports the rules' findings at their paths.
func TestRulesOldMapc(t *testing.T) {
	e, _ := rulesEnv(t, "--oldmapc")
	var r rulesReply
	if code := e.call("GET", "/api/tuning?level=SM_Map_v5", nil, &r); code != 200 || r.OK || !strings.Contains(r.Error, "tuning") {
		t.Errorf("tuning: %d %+v", code, r)
	}
	doc, _, _ := e.open("CM_Ring")
	doc["rules"] = map[string]any{"victory": map[string]any{"kills": 500}, "minions": map[string]any{"every": 12}}
	r = rulesReply{}
	if code := e.call("POST", "/api/rules/CM_Ring", doc, &r); code != 200 || r.OK || !strings.Contains(r.Error, "unknown command") {
		t.Errorf("rules: %d %+v", code, r)
	}
	c := e.check("CM_Ring", doc)
	if c.Report.OK || !hasFinding(c, "rules.victory", "rules.victory.kills") || !hasFinding(c, "rules.minions", "rules.minions.every") {
		t.Errorf("the check: %+v", c.Report.Findings)
	}
}

// TestRulesSaveKeepsBytes: a rules section the page writes (Brawl, in the format's order) saves as added lines (and
// the comma after the member before it);
// a changed member changes its one line; the section removed again (Classic) gives the original file back, byte for
// byte; mapc reads the saved rules as sent.
func TestRulesSaveKeepsBytes(t *testing.T) {
	e := newEnv(t, false)
	_, text, sha := e.open("CM_Ring")
	brawl := `{"victory": {"kills": 25, "minutes": 20}, "startLevel": 6, "respawn": {"base": 6, "perLevel": 0.25}, ` +
		`"gold": {"start": 1500, "kills": 1.5}, "surrender": {"allowed": false}}`
	withRules := func(rules string) string { // the page's document: map.json's members, then rules
		var doc map[string]json.RawMessage
		json.Unmarshal([]byte(text), &doc)
		if rules == "" {
			delete(doc, "rules")
		} else {
			doc["rules"] = json.RawMessage(rules)
		}
		b, _ := json.Marshal(doc)
		return string(b)
	}
	type saveReply struct {
		OK, Written bool
		Sha256      string
	}
	save := func(body string) string {
		t.Helper()
		var s saveReply
		if code := e.call("POST", "/api/save/CM_Ring?base="+sha, body, &s); code != 200 || !s.OK {
			t.Fatalf("save: %d %+v", code, s)
		}
		sha = s.Sha256
		b, _ := os.ReadFile(filepath.Join(e.maps, "CM_Ring", "map.json"))
		return string(b)
	}
	saved := save(withRules(brawl))
	if n := changedLines(saved, text); n != 1 { // the last member's line gets its comma
		t.Errorf("%d of the original lines changed:\n%s", n, saved)
	}
	m, err := mapc.Parse([]byte(saved))
	if err != nil {
		t.Fatal(err)
	}
	var want *mapc.Rules
	json.Unmarshal([]byte(brawl), &want)
	if !reflect.DeepEqual(m.Rules, want) {
		got, _ := json.Marshal(m.Rules)
		t.Errorf("the saved rules: %s", got)
	}
	if s, _ := mapc.DescribeRules(m.Rules); !strings.HasPrefix(s,
		"First to 25 kills or most kills after 20 minutes wins; everyone starts at level 6;") {
		t.Errorf("the saved rules read %q", s)
	}
	again := save(withRules(strings.Replace(brawl, `"base": 6`, `"base": 7`, 1)))
	if n := changedLines(saved, again); n != 1 {
		t.Errorf("respawn 7: %d lines changed:\n%s", n, again)
	}
	if back := save(withRules("")); back != text {
		t.Errorf("Classic: the file isn't the original:\n%s", back)
	}
	if out, err := mapcCmd("check", filepath.Join(e.maps, "CM_Ring")).CombinedOutput(); err != nil {
		t.Errorf("mapc check: %v %s", err, out)
	}
}

// realReply is the real `mapc tuning --json` as the routes relay it (constants' defaults may be arrays or flags).
type realReply struct {
	OK                  bool
	Error, Level        string
	Source              string
	Defaults, Effective map[string]any
	Summary, Short      string
	VictoryChanged      bool
	BalanceChanged      bool
	Findings            []mapc.Finding
	Constants           []map[string]any
}

// TestRulesRealMapc: both routes on the real mapc (`mapc tuning`), as the page reads them. Without a game copy the
// level's values come from mapc's own table ("shipped", no constants); with one (the checkout's extract, else the
// test is skipped) from the copy's assets ("copy") with every member of the tuning, "value" only on the members the
// map's rules change. A rule mapc refuses is a finding at its path in a reply that is still ok.
func TestRulesRealMapc(t *testing.T) {
	brawlish := map[string]any{"victory": map[string]any{"kills": 25, "bindings": 3},
		"respawn": map[string]any{"base": 6, "perLevel": 0.25}}
	var want *mapc.Rules
	b, _ := json.Marshal(brawlish)
	json.Unmarshal(b, &want)
	wantSummary, wantShort := mapc.DescribeRules(want)
	for _, withCopy := range []bool{false, true} {
		source := map[bool]string{false: "shipped", true: "copy"}[withCopy]
		if withCopy && gameCopy() == "" {
			t.Log("no game copy: the copy's half skipped")
			continue
		}
		e := newEnv(t, withCopy)
		var r realReply
		if code := e.call("GET", "/api/tuning?level=MM_Map_v3", nil, &r); code != 200 || !r.OK || r.Level != "MM_Map_v3" ||
			r.Source != source || r.Defaults["respawn"].(map[string]any)["base"] != 10.0 ||
			r.Defaults["respawn"].(map[string]any)["perMinute"] != 0.5 || r.Defaults["startLevel"] != 1.0 {
			t.Fatalf("%s: MM_Map_v3: %d %+v", source, code, r)
		}
		doc, _, _ := e.open("CM_Ring")
		doc["rules"] = brawlish
		r = realReply{}
		if code := e.call("POST", "/api/rules/CM_Ring", doc, &r); code != 200 || !r.OK || r.Level != "SM_Map_v5" ||
			r.Source != source || r.Summary != wantSummary || r.Short != wantShort || !r.VictoryChanged || !r.BalanceChanged ||
			len(r.Findings) != 0 {
			t.Fatalf("%s: the draft: %d %+v", source, code, r)
		}
		resp := r.Effective["respawn"].(map[string]any)
		if resp["base"] != 6.0 || resp["perLevel"] != 0.25 || resp["perMinute"] != 1.25 {
			t.Errorf("%s: effective respawn %v", source, resp)
		}
		changed := map[string]any{}
		for _, c := range r.Constants {
			if v, ok := c["value"]; ok {
				changed[c["member"].(string)] = v
			}
		}
		if !withCopy && len(r.Constants) != 0 {
			t.Errorf("shipped: %d constants, want none", len(r.Constants))
		}
		if withCopy && (len(r.Constants) < 150 ||
			!reflect.DeepEqual(changed, map[string]any{"deathTimeBase": 6.0, "deathTimePerLevel": 0.25})) {
			t.Errorf("copy: %d constants, changed %v", len(r.Constants), changed)
		}
		doc["rules"] = map[string]any{"victory": map[string]any{"kills": 500}}
		r = realReply{}
		if code := e.call("POST", "/api/rules/CM_Ring", doc, &r); code != 200 || !r.OK || len(r.Findings) == 0 ||
			r.Findings[0].Path != "rules.victory.kills" || r.Findings[0].Severity != "error" {
			t.Errorf("%s: kills 500: %d %+v", source, code, r)
		}
	}
	// a copy whose tuning can't be read: the table's values
	maps := t.TempDir()
	copyPackage(t, ringDir, filepath.Join(maps, "CM_Ring"))
	e := serve(t, Config{Maps: maps, Mapc: mapcExe, MapcArgs: mapcArgs, Copy: t.TempDir(), Out: filepath.Join(t.TempDir(), "out"),
		Work: t.TempDir(), Cache: filepath.Join(t.TempDir(), "3d"), Pages: testPages(t, API)}, maps)
	var r realReply
	if code := e.call("GET", "/api/tuning?level=SM_Map_v5", nil, &r); code != 200 || !r.OK || r.Source != "shipped" ||
		r.Defaults["respawn"].(map[string]any)["base"] != 15.0 {
		t.Errorf("an unreadable copy: %d %+v", code, r)
	}
}
