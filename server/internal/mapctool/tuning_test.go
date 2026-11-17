package mapctool

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/testmaps"
)

// tunedPkg is the fixture with balance rules (MAPRULES2's live map).
var tunedPkg = filepath.Join(testmaps.Dir(), "..", "maprules", "CM_Tuned")

type tuningJSON struct {
	Level          string            `json:"level"`
	Source         string            `json:"source"`
	Defaults       json.RawMessage   `json:"defaults"`
	Effective      json.RawMessage   `json:"effective"`
	Summary        string            `json:"summary"`
	Short          string            `json:"short"`
	VictoryChanged bool              `json:"victoryChanged"`
	BalanceChanged bool              `json:"balanceChanged"`
	Findings       []mapc.Finding    `json:"findings"`
	Constants      []json.RawMessage `json:"constants"`
}

func tuningCall(t *testing.T, args ...string) tuningJSON {
	t.Helper()
	code, out, errw := call(append([]string{"tuning", "--json"}, args...)...)
	var r tuningJSON
	if err := json.Unmarshal([]byte(out), &r); code != 0 || err != nil {
		t.Fatalf("%q: exit %d, %v\n%s%s", args, code, err, out, errw)
	}
	return r
}

func compact(t *testing.T, b []byte) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

const smDefaults = `{"startLevel":1,"respawn":{"base":15,"perLevel":0.5,"perMinute":1.25},"minions":{"firstWave":60,"every":30},` +
	`"gold":{"start":500,"perMinute":100,"from":60,"kills":1},"xp":{"speed":1},"bindings":{"respawn":0},` +
	`"wells":{"opensAt":900,"relock":240,"gold":1},"surrender":{"after":20,"allowed":true}}`

// TestTuningJSON: mapc tuning --json without a copy: the defaults from ShippedTuning (by --level), the fixture's
// effective values, texts, flags and findings; a map on stdin with a broken rule gives its finding (exit 0); the
// argument errors exit 2.
func TestTuningJSON(t *testing.T) {
	r := tuningCall(t)
	if r.Level != "SM_Map_v5" || r.Source != "shipped" || compact(t, r.Defaults) != smDefaults || r.Effective != nil ||
		r.Summary != "" || r.BalanceChanged || r.Findings == nil || len(r.Findings) != 0 || len(r.Constants) != 0 {
		t.Errorf("no map: %+v", r)
	}
	if r = tuningCall(t, "--level", "MM_Map_v3"); r.Level != "MM_Map_v3" ||
		!strings.Contains(compact(t, r.Defaults), `"respawn":{"base":10,"perLevel":0.5,"perMinute":0.5}`) {
		t.Errorf("MM_Map_v3: %s", r.Defaults)
	}
	r = tuningCall(t, "--map", tunedPkg)
	m, err := mapc.Load(tunedPkg)
	if err != nil {
		t.Fatal(err)
	}
	sum, short := mapc.DescribeRules(m.Rules)
	wantEff := `{"startLevel":1,"respawn":{"base":6,"perLevel":0.25,"perMinute":0.5},"minions":{"firstWave":30,"every":25},` +
		`"gold":{"start":1500,"perMinute":200,"from":30,"kills":1.5},"xp":{"speed":1.5},"bindings":{"respawn":60},` +
		`"wells":{"opensAt":300,"relock":120,"gold":1.5},"surrender":{"after":10,"allowed":true}}`
	if compact(t, r.Defaults) != smDefaults || compact(t, r.Effective) != wantEff || r.Summary != sum || r.Short != short ||
		short != "Tuned" || !r.BalanceChanged || r.VictoryChanged || len(r.Findings) != 0 {
		t.Errorf("CM_Tuned: %+v\n%s", r, r.Effective)
	}
	// the package's map.json by its file; the same
	if r2 := tuningCall(t, "--map", filepath.Join(tunedPkg, mapc.FileName)); compact(t, r2.Effective) != wantEff {
		t.Errorf("by file: %s", r2.Effective)
	}
	// stdin: a broken respawn and a race (kills and Bindings: the Binding race beside the Bindings' return); an MM
	// tuning level from the map
	text, err := mapc.FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	text = bytes.Replace(text, []byte(`"base": 6,`), []byte(`"base": 500,`), 1)
	text = bytes.Replace(text, []byte(`"tuning": "SM_Map_v5"`), []byte(`"tuning": "MM_Map_v3"`), 1)
	text = bytes.Replace(text, []byte(`"rules": {`), []byte(`"rules": {"victory": {"kills": 20, "bindings": 3},`), 1)
	old := stdin
	stdin = bytes.NewReader(text)
	defer func() { stdin = old }()
	r = tuningCall(t, "--map", "-")
	if r.Level != "MM_Map_v3" || !r.VictoryChanged || len(r.Findings) != 1 || r.Findings[0].Rule != "rules.respawn" ||
		r.Findings[0].Severity != mapc.Error ||
		!strings.Contains(compact(t, r.Effective), `"victory":{"kills":20,"bindings":3}`) ||
		!strings.Contains(compact(t, r.Effective), `"base":500`) || !strings.HasPrefix(r.Short, "Race 20 · Bind 3") {
		t.Errorf("stdin: %+v", r)
	}
	for _, args := range [][]string{{"tuning", "--level", "XX_Map"}, {"tuning", "--map"}, {"tuning", "extra"},
		{"tuning", "--map", filepath.Join(tunedPkg, "none.json")}, {"tuning", "--bogus"}} {
		if code, _, _ := call(args...); code != 2 {
			t.Errorf("%q: exit %d, 2 expected", args, code)
		}
	}
	// the text form
	if code, out, _ := call("tuning", "--map", tunedPkg); code != 0 || !strings.Contains(out, "respawn 6 s + 0.25 s a level") {
		t.Errorf("text: exit %d\n%s", code, out)
	}
}

// TestTuningGame: with build 854's copy the defaults come from its assets (the same as without), and the constants
// list every member with the fixture's changes.
func TestTuningGame(t *testing.T) {
	game := testgame.Dir(t)
	r := tuningCall(t, "--copy", game, "--map", tunedPkg)
	if r.Source != "copy" || compact(t, r.Defaults) != smDefaults || len(r.Constants) < 150 {
		t.Fatalf("%s %s, %d constants", r.Source, r.Defaults, len(r.Constants))
	}
	type constant struct {
		Asset, Member string
		Default       any
		Value         any
	}
	byMember := map[string]constant{}
	for _, raw := range r.Constants {
		var c constant
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		byMember[c.Asset+" "+c.Member] = c
	}
	for k, want := range map[string][2]any{
		"LevelGameplayTuning deathTimeBase":                 {15.0, 6.0},
		"LevelGameplayTuning surrenderVoteSuccessThreshold": {0.666, nil},
		"LevelGameplayTuning tower[0].respawnTime":          {1e8, 60.0},
		"RewardsTuning firstBloodGoldReward":                {100.0, 150.0},
		"LevelUpTuning Level[2].xpRequired":                 {260.0, 173.0},
	} {
		c, ok := byMember[k]
		if !ok || c.Default != want[0] || c.Value != want[1] {
			t.Errorf("%s: %+v, %v expected", k, c, want)
		}
	}
	// without --map: the defaults only, no value
	r = tuningCall(t, "--copy", game, "--level", "MM_Map_v3")
	values := 0
	for _, c := range r.Constants {
		if bytes.Contains(c, []byte(`"value"`)) {
			values++
		}
	}
	if !strings.Contains(compact(t, r.Defaults), `"base":10`) || values != 0 || r.Effective != nil {
		t.Errorf("MM_Map_v3 defaults %s", r.Defaults)
	}
}
