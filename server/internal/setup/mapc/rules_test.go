package mapc

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"dawngate/server/internal/testmaps"
)

func intp(v int) *int         { return &v }
func f64p(v float64) *float64 { return &v }
func boolp(v bool) *bool      { return &v }
func rulesOf(s string) *Rules { var r *Rules; _ = strictUnmarshal([]byte(s), &r); return r }
func describeFile() string    { return filepath.Join(testmaps.Dir(), "..", "maprules", "describe.json") }
func rulesFindings(r *Report) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if strings.HasPrefix(f.Rule, "rules.") || f.Rule == "names.tuning" {
			out = append(out, f)
		}
	}
	return out
}

// everyMember is a section with every member set (the design's example).
const everyMember = `{"victory": {"kills": 20, "minutes": 15}, "startLevel": 6,
	"respawn": {"base": 8, "perLevel": 0.5, "perMinute": 0.5}, "minions": {"firstWave": 30, "every": 25},
	"gold": {"start": 1500, "perMinute": 200, "from": 30, "kills": 1.5}, "xp": {"speed": 1.5},
	"bindings": {"respawn": 300}, "wells": {"opensAt": 300, "relock": 120, "gold": 1.5},
	"surrender": {"after": 10, "allowed": true}}`

// TestRulesFormat: the section is written after tuning, a member a line, and read back the same; a map without one
// writes none and reads back none; an empty section and a member set to 0 stay as they are; an unknown member
// anywhere in it is an error; the section doesn't change the compiled map.
func TestRulesFormat(t *testing.T) {
	m := ring(t)
	plain, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte(`"rules"`)) {
		t.Errorf("a map without rules writes them")
	}
	if back, err := Parse(plain); err != nil || back.Rules != nil {
		t.Errorf("absent rules read back as %+v (%v)", back.Rules, err)
	}
	m.Rules = rulesOf(everyMember)
	if m.Rules == nil {
		t.Fatal("the section doesn't parse")
	}
	text, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	want := "  \"tuning\": \"SM_Map_v5\",\n  \"rules\": {\n    \"victory\": {\"kills\": 20, \"minutes\": 15},\n" +
		"    \"startLevel\": 6,\n    \"respawn\": {\"base\": 8, \"perLevel\": 0.5, \"perMinute\": 0.5},\n" +
		"    \"minions\": {\"firstWave\": 30, \"every\": 25},\n" +
		"    \"gold\": {\"start\": 1500, \"perMinute\": 200, \"from\": 30, \"kills\": 1.5},\n" +
		"    \"xp\": {\"speed\": 1.5},\n    \"bindings\": {\"respawn\": 300},\n" +
		"    \"wells\": {\"opensAt\": 300, \"relock\": 120, \"gold\": 1.5},\n" +
		"    \"surrender\": {\"after\": 10, \"allowed\": true}\n  },\n  \"lanes\": ["
	if !strings.Contains(string(text), want) {
		t.Errorf("the rules written:\n%s", text)
	}
	back, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Rules, m.Rules) {
		t.Errorf("read back %+v", back.Rules)
	}
	if again, _ := FormatMap(back); !bytes.Equal(again, text) {
		t.Errorf("written twice, the texts differ")
	}
	// the rest of the file as without the section
	if !bytes.Equal(bytes.Replace(text, []byte(want), []byte("  \"tuning\": \"SM_Map_v5\",\n  \"lanes\": ["), 1), plain) {
		t.Errorf("the section changed the rest of the file")
	}
	for _, c := range []struct{ in, out string }{
		{`{}`, `"rules": {},`},
		{`{"respawn": {"base": 0}, "surrender": {"allowed": false}}`,
			"\"rules\": {\n    \"respawn\": {\"base\": 0},\n    \"surrender\": {\"allowed\": false}\n  },"},
		{`{"victory": {}}`, "\"rules\": {\n    \"victory\": {}\n  },"},
	} {
		m.Rules = rulesOf(c.in)
		text, err := FormatMap(m)
		if err != nil {
			t.Fatal(err)
		}
		back, err := Parse(text)
		if err != nil || !strings.Contains(string(text), c.out) || !reflect.DeepEqual(back.Rules, m.Rules) {
			t.Errorf("%s: written\n%s\nread back %+v (%v)", c.in, text, back.Rules, err)
		}
	}
	if b := rulesOf(`{"respawn": {"base": 0}}`); b.Respawn.Base == nil || *b.Respawn.Base != 0 || b.Respawn.PerLevel != nil {
		t.Errorf("0 and absent: %+v", b.Respawn)
	}
	m.Rules = rulesOf(everyMember)
	text, _ = FormatMap(m)
	for _, bad := range [][2]string{{`"startLevel": 6`, `"startLevel": 6, "lobbyPresets": "draft"`},
		{`"startLevel": 6`, `"startLevel": 6, "lobbyPreset": 3`}, {`{"speed": 1.5}`, `{"speed": 1.5, "fast": true}`}, {`"startLevel": 6`, `"startLevel": 6.5`},
		{`"kills": 20,`, `"kills": "20",`}} {
		if _, err := Parse(bytes.Replace(text, []byte(bad[0]), []byte(bad[1]), 1)); err == nil {
			t.Errorf("%s was read", bad[1])
		}
	}
	// the compiled map is the same with the section
	r0, cm0 := Check(ring(t), nil)
	r1, cm1 := Check(m, nil)
	if !r0.OK || !r1.OK || !reflect.DeepEqual(r0.Stats, r1.Stats) || !reflect.DeepEqual(cm0.NavSpec(), cm1.NavSpec()) {
		t.Errorf("the compiled map differs: %+v / %+v; %+v", r0.Stats, r1.Stats, r1.Findings)
	}
}

// TestRulesPass: in-range sections, the limits included, give no finding; the cross-warnings stay quiet where they
// don't apply.
func TestRulesPass(t *testing.T) {
	for _, s := range []string{
		everyMember,
		`{}`,
		`{"victory": {"kills": 5, "minutes": 90}, "startLevel": 20}`,
		`{"victory": {"kills": 200, "minutes": 21}, "startLevel": 1}`,
		`{"respawn": {"base": 0, "perLevel": 0, "perMinute": 0}, "minions": {"firstWave": 0, "every": 15}}`,
		`{"respawn": {"base": 120, "perLevel": 10, "perMinute": 10}, "minions": {"firstWave": 600, "every": 300}}`,
		`{"gold": {"start": 0, "perMinute": 0, "from": 0, "kills": 0}, "xp": {"speed": 0.25}}`,
		`{"gold": {"start": 20000, "perMinute": 5000, "from": 3600, "kills": 10}, "xp": {"speed": 5}}`,
		`{"bindings": {"respawn": 30}, "wells": {"opensAt": 0, "relock": 30, "gold": 0}}`,
		`{"bindings": {"respawn": 3600}, "wells": {"opensAt": 3600, "relock": 3600, "gold": 10}}`,
		`{"surrender": {"after": 5}}`,
		`{"surrender": {"after": 60, "allowed": false}}`,
		`{"victory": {"minutes": 15}, "surrender": {"after": 10}}`, // the wells' 900 s at the limit: not after it
		`{"victory": {"minutes": 10}, "surrender": {"allowed": false}, "wells": {"opensAt": 600}}`,
		`{"bindings": {"respawn": 30}, "minions": {"every": 30}}`, // back with the next wave
		`{"bindings": {"respawn": 30}}`,                           // the tuning's 30 s waves
	} {
		m := ring(t)
		m.Rules = rulesOf(s)
		if m.Rules == nil {
			t.Fatalf("%s doesn't parse", s)
		}
		if f := rulesFindings(CheckStructure(m, nil)); len(f) != 0 {
			t.Errorf("%s: %+v", s, f)
		}
	}
	m := ring(t)
	m.Name = "Tuning"
	m.Rules = rulesOf(`{"victory": {"kills": 20}, "startLevel": 6}`) // no tuning asset of its own
	if f := rulesFindings(CheckStructure(m, nil)); len(f) != 0 {
		t.Errorf("Tuning with host rules only: %+v", f)
	}
	m.Rules.XP = &XPRules{Speed: f64p(2)}
	if r := CheckStructure(m, nil); !has(r, "names.tuning", Error) {
		t.Errorf("Tuning with its own XP: %+v", r.Findings)
	}
	// the messages name the value and the range in words
	m = ring(t)
	m.Rules = rulesOf(`{"victory": {"kills": 0}, "gold": {"start": 25000}}`)
	var msgs []string
	for _, f := range CheckStructure(m, nil).Findings {
		msgs = append(msgs, f.Path+": "+f.Message)
	}
	if got := strings.Join(msgs, " | "); got != "rules.victory.kills: the kill race's target (kills) 0: from 1 to 200 | "+
		"rules.gold.start: the starting gold 25,000: from 0 to 20,000" {
		t.Errorf("messages: %s", got)
	}
}

// TestHostRules: the host's part and the groups' flags.
func TestHostRules(t *testing.T) {
	var none *Rules
	if !none.HostRules().IsZero() || none.VictoryChanged() || none.BalanceChanged() {
		t.Errorf("a nil section")
	}
	r := rulesOf(everyMember)
	if h := r.HostRules(); h != (HostRules{Kills: 20, Minutes: 15, StartLevel: 6}) {
		t.Errorf("host %+v", h)
	}
	if !r.VictoryChanged() || !r.GameplayChanged() || !r.RewardsChanged() || !r.LevelUpChanged() || !r.BalanceChanged() {
		t.Errorf("every member: the flags")
	}
	for _, c := range []struct {
		s                                   string
		victory, gameplay, rewards, levelUp bool
	}{
		{`{}`, false, false, false, false},
		{`{"victory": {}, "respawn": {}, "minions": {}, "gold": {}, "xp": {}, "bindings": {}, "wells": {}, "surrender": {}}`,
			false, false, false, false},
		{`{"victory": {"minutes": 10}, "startLevel": 3}`, true, false, false, false},
		{`{"victory": {"bindings": 1}}`, true, false, false, false},
		{`{"bindings": {"respawn": 120}}`, false, true, false, false}, // their return isn't the victory's
		{`{"startLevel": 3}`, false, false, false, false},
		{`{"respawn": {"perMinute": 0}}`, false, true, false, false},
		{`{"minions": {"every": 20}}`, false, true, false, false},
		{`{"bindings": {"respawn": 60}}`, false, true, false, false},
		{`{"wells": {"gold": 2}}`, false, true, false, false},
		{`{"surrender": {"allowed": true}}`, false, true, false, false},
		{`{"gold": {"from": 0}}`, false, false, true, false},
		{`{"xp": {"speed": 2}}`, false, false, false, true},
	} {
		r := rulesOf(c.s)
		if r.VictoryChanged() != c.victory || r.GameplayChanged() != c.gameplay || r.RewardsChanged() != c.rewards ||
			r.LevelUpChanged() != c.levelUp || r.BalanceChanged() != (c.gameplay || c.rewards || c.levelUp) {
			t.Errorf("%s: victory %v gameplay %v rewards %v levelUp %v", c.s, r.VictoryChanged(), r.GameplayChanged(),
				r.RewardsChanged(), r.LevelUpChanged())
		}
	}
	if h := rulesOf(`{"startLevel": 6}`).HostRules(); h != (HostRules{StartLevel: 6}) || h.IsZero() {
		t.Errorf("start level only: %+v", h)
	}
	h := rulesOf(`{"victory": {"kills": 20, "minutes": 20, "bindings": 3}, "bindings": {"respawn": 120}}`).HostRules()
	if h != (HostRules{Kills: 20, Minutes: 20, Bindings: 3}) {
		t.Errorf("a Binding race: %+v", h)
	}
}

// TestBindingRace: the Binding race's target against the enemy's Bindings (BindingsPerTeam: "auto" 3 a lane, else
// the places, a one-path map's one lane included): above them an error unless destroyed Bindings come back; the
// victory written after kills and minutes.
func TestBindingRace(t *testing.T) {
	pass, err := Load(testmaps.Pkg("CM_Pass"))
	if err != nil {
		t.Fatal(err)
	}
	m := ring(t)
	if n, p := m.BindingsPerTeam(), pass.BindingsPerTeam(); n != 6 || p != 2 {
		t.Fatalf("Bindings a team: CM_Ring %d, CM_Pass %d", n, p)
	}
	for _, c := range []struct {
		m     *Map
		rules string
		err   bool
	}{
		{m, `{"victory": {"bindings": 6}}`, false},
		{m, `{"victory": {"bindings": 7}}`, true},
		{m, `{"victory": {"bindings": 12}, "bindings": {"respawn": 300}}`, false},
		{pass, `{"victory": {"bindings": 2, "minutes": 25}}`, false},
		{pass, `{"victory": {"bindings": 3}}`, true},
		{pass, `{"victory": {"bindings": 3}, "bindings": {"respawn": 60}}`, false},
	} {
		c.m.Rules = rulesOf(c.rules)
		f := rulesFindings(CheckStructure(c.m, nil))
		if c.err != (len(f) == 1 && f[0].Rule == "rules.victory" && f[0].Path == "rules.victory.bindings" &&
			f[0].Severity == Error) || (!c.err && len(f) != 0) {
			t.Errorf("%s %s: %+v", c.m.Name, c.rules, f)
		}
		if c.err && !strings.Contains(f[0].Message, "can never be reached") {
			t.Errorf("%s %s: %s", c.m.Name, c.rules, f[0].Message)
		}
	}
	// the short text's room: "Tuned" goes first, then the start level (not in describe.json: the System line's first
	// line can't yet hold every win condition, a start level and an old host's "not played" tail in 200 characters)
	for _, c := range [][2]string{
		{`{"victory": {"kills": 20, "minutes": 20, "bindings": 3}, "startLevel": 6, "gold": {"start": 1500}}`,
			"Race 20 · Bind 3 · 20 min"},
		{`{"victory": {"kills": 200, "minutes": 90, "bindings": 12}, "startLevel": 20}`, "Race 200 · Bind 12 · 90 min"},
		{`{"victory": {"kills": 1, "minutes": 3, "bindings": 1}, "startLevel": 1, "xp": {"speed": 2}}`,
			"Race 1 · Bind 1 · 3 min · Lv 1"},
		{`{"victory": {"kills": 20, "bindings": 3}, "startLevel": 6, "xp": {"speed": 2}}`, "Race 20 · Bind 3 · Lv 6"},
	} {
		summary, short := DescribeRules(rulesOf(c[0]))
		if short != c[1] || !strings.Contains(summary, "everyone starts at level") {
			t.Errorf("%s: %q / %q", c[0], short, summary)
		}
	}
	m.Rules = rulesOf(`{"victory": {"bindings": 3, "kills": 20}}`)
	if text, _ := FormatMap(m); !strings.Contains(string(text), `"victory": {"kills": 20, "bindings": 3}`) {
		t.Errorf("the victory written:\n%s", text)
	}
}

// describeCase is one vector of server/testdata/maprules/describe.json (the map editor's self-test reads the file too).
type describeCase struct {
	Name    string          `json:"name"`
	Rules   json.RawMessage `json:"rules"`
	Summary string          `json:"summary"`
	Short   string          `json:"short"`
}

// TestDescribeRules: DescribeRules gives each vector's texts; the short text stays within ShortMax, the design's
// example's summary within a chat line's 200 characters.
func TestDescribeRules(t *testing.T) {
	b, err := os.ReadFile(describeFile())
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		About string         `json:"about"`
		Cases []describeCase `json:"cases"`
	}
	if err := strictUnmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) < 15 {
		t.Fatalf("%d vectors", len(f.Cases))
	}
	for _, c := range f.Cases {
		var r *Rules
		if err := strictUnmarshal(c.Rules, &r); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		summary, short := DescribeRules(r)
		if summary != c.Summary || short != c.Short {
			t.Errorf("%s:\n got %q / %q\nwant %q / %q", c.Name, summary, short, c.Summary, c.Short)
		}
		if s2, sh2 := DescribeRules(r); s2 != summary || sh2 != short {
			t.Errorf("%s: a second call differs", c.Name)
		}
		if n := utf8.RuneCountInString(short); n > ShortMax {
			t.Errorf("%s: short %q, %d characters", c.Name, short, n)
		}
		if c.Name == "the design's example" && len(summary) > 200 {
			t.Errorf("the design's example: %d characters", len(summary))
		}
		if (summary == "") != (short == "") {
			t.Errorf("%s: one text empty", c.Name)
		}
	}
}

// TestRulesReadFolder: ReadFolder (and so dgserver) leaves out a package whose rules are out of range or that is
// named as the game's own tuning with compiled rules, and keeps one whose rules only warn, with its section.
func TestRulesReadFolder(t *testing.T) {
	dir := t.TempDir()
	good := ring(t)
	good.Rules = rulesOf(everyMember)
	writePkg(t, dir, "CM_Ring", good, "")
	warn := ring(t)
	warn.Name, warn.Tag, warn.Display = "CM_Warn", "warn", "Warn"
	warn.Rules = rulesOf(`{"victory": {"kills": 3, "minutes": 10}}`)
	writePkg(t, dir, "CM_Warn", warn, "")
	bad := ring(t)
	bad.Name, bad.Tag, bad.Display = "CM_Bad", "bad", "Bad"
	bad.Rules = rulesOf(`{"startLevel": 25}`)
	writePkg(t, dir, "CM_Bad", bad, "")
	tun := ring(t)
	tun.Name, tun.Tag, tun.Display = "Tuning", "tuning", "Tuning"
	tun.Rules = rulesOf(`{"gold": {"start": 1000}}`)
	writePkg(t, dir, "Tuning", tun, "")
	writePkg(t, dir, "CM_Typo", nil, strings.Replace(string(mustFormat(t, good)), `"startLevel"`, `"startLvl"`, 1))
	pkgs, errs := ReadFolder(dir)
	var names []string
	for _, p := range pkgs {
		names = append(names, p.Map.Name)
	}
	if got := strings.Join(names, ","); got != "CM_Ring,CM_Warn" {
		t.Errorf("packages %s", got)
	}
	if len(errs) != 3 {
		t.Fatalf("%d errors: %v", len(errs), errs)
	}
	for i, want := range []string{"the starting level 25: from 1 to 20", "startLvl", "the game's own tuning"} {
		if !strings.Contains(errs[i].Error(), want) {
			t.Errorf("error %d: %v, want %q", i, errs[i], want)
		}
	}
	if len(pkgs) == 2 && (!reflect.DeepEqual(pkgs[0].Map.Rules, good.Rules) || pkgs[1].Map.Rules.HostRules().Kills != 3) {
		t.Errorf("the sections read: %+v, %+v", pkgs[0].Map.Rules, pkgs[1].Map.Rules)
	}
}

func mustFormat(t *testing.T, m *Map) []byte {
	t.Helper()
	b, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestLobbyPreset (RULES6): the suggested lobby setup is written last in the section and read back; it isn't compiled
// (nor in the rules' texts); the lobby's catalog judges it: a bad spec or a win condition on a map with its own is an
// error, an id no built-in preset has and a combination the lobby's checks refuse are warnings.
func TestLobbyPreset(t *testing.T) {
	m := ring(t)
	r0, cm0 := Check(m, nil)
	m.Rules = rulesOf(`{"xp": {"speed": 2}, "lobbyPreset": "pick=random,victory=bind1"}`)
	text, err := FormatMap(m)
	if err != nil || !strings.Contains(string(text), `    "xp": {"speed": 2},
    "lobbyPreset": "pick=random,victory=bind1"
  }`) {
		t.Fatalf("written (%v):\n%s", err, text)
	}
	if back, err := Parse(text); err != nil || back.Rules.LobbyPreset != "pick=random,victory=bind1" {
		t.Errorf("read back %+v (%v)", back.Rules, err)
	}
	m.Rules = rulesOf(`{"lobbyPreset": "draft"}`)
	r1, cm1 := Check(m, nil)
	if !r1.OK || len(rulesFindings(r1)) != 0 || !reflect.DeepEqual(r0.Stats, r1.Stats) ||
		!reflect.DeepEqual(cm0.NavSpec(), cm1.NavSpec()) || m.Rules.BalanceChanged() || m.Rules.VictoryChanged() {
		t.Errorf("the suggestion alone changes the map: %+v; %+v", r1.Stats, r1.Findings)
	}
	if s, short := DescribeRules(m.Rules); s != "" || short != "" {
		t.Errorf("the rules' texts name the suggestion: %q / %q", s, short)
	}
	for _, c := range []struct {
		rules, sev, msg string
	}{
		{`{"lobbyPreset": "pick=random,shop=onDeath,victory=bind1"}`, "", ""},
		{`{"lobbyPreset": "brawl"}`, Warning, "only servers that define this preset use it"},
		{`{"lobbyPreset": "pick=fast"}`, Error, "the suggested lobby setup: unknown option"},
		{`{"lobbyPreset": "pick draft"}`, Error, "neither a preset's id nor rules"},
		{`{"victory": {"kills": 20}, "lobbyPreset": "victory=bind1"}`, Error, "the map has its own win condition"},
		{`{"victory": {"kills": 20}, "lobbyPreset": "pick=draft"}`, "", ""},
		{`{"lobbyPreset": "pick=draft,shaperPool=tank"}`, Warning, "The lobby then starts on Standard."},
	} {
		m.Rules = rulesOf(c.rules)
		f := rulesFindings(CheckStructure(m, nil))
		if c.sev == "" {
			if len(f) != 0 {
				t.Errorf("%s: %+v", c.rules, f)
			}
			continue
		}
		if len(f) != 1 || f[0].Rule != "rules.lobby-preset" || f[0].Path != "rules.lobbyPreset" || f[0].Severity != c.sev ||
			!strings.Contains(f[0].Message, c.msg) {
			t.Errorf("%s: %+v", c.rules, f)
		}
	}
}
