package mapctool

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dawngate/server/internal/setup/mapc"
)

// tuning prints a tuning level's balance values and a map's rules (MAPRULES2), the map editor's data source for its
// Rules window and its read-only Gameplay Constants (MAPRULES7):
//
//	mapc tuning [--json] [--copy <copy>] [--level SM_Map_v5|MM_Map_v3] [--map <map.json>|<package>|-]
//
// --json prints one object:
//
//	{"level": "SM_Map_v5",
//	 "defaults": the level's values as a full rules section (every balance member; gold.kills, xp.speed and
//	             wells.gold 1, bindings.respawn 0 when the level has no Binding respawn, surrender allowed;
//	             startLevel 1; no victory),
//	 "effective": the same with the map's own members put in, its victory and start level too (only with --map),
//	 "summary", "short": mapc.DescribeRules' texts, "victoryChanged", "balanceChanged": the map's (false without
//	 --map), "findings": the map's rules.* and names.tuning findings, the objects check --json prints ([] without),
//	 "constants": [{"asset", "member", "default", "value"}] every member of the level's LevelGameplayTuning,
//	             RewardsTuning and LevelUpTuning (array elements by index, "tower[0].respawnTime"; an empty array's
//	             default []), "value" only where the map's rules change it (only with --copy),
//	 "source": "copy" (defaults from the copy's assets) or "shipped" (without --copy: mapc.ShippedTuning, build 854's
//	           values, which TestGameAssets keeps equal to the copy's)}
//
// The level: --level, else the map's tuning (one of mapc.SharedLevels), else SM_Map_v5. --map names a map.json, a package folder (its
// map.json) or - (map.json on stdin; the editor's unsaved map). Exit 0: printed (also when the rules have errors:
// they are findings); 1: the copy's tuning assets can't be read or compiled; 2: wrong arguments or unreadable input.
var tuningUsage = `usage:
  mapc tuning [--json] [--copy <copy>] [--level SM_Map_v5|MM_Map_v3] [--map <map.json>|<package>|-]`

// stdin is where --map - reads map.json (a variable for the tests).
var stdin io.Reader = os.Stdin

// tuningOut is what tuning --json prints.
type tuningOut struct {
	Level          string          `json:"level"`
	Source         string          `json:"source"`
	Defaults       *mapc.Rules     `json:"defaults"`
	Effective      *mapc.Rules     `json:"effective,omitempty"`
	Summary        string          `json:"summary"`
	Short          string          `json:"short"`
	VictoryChanged bool            `json:"victoryChanged"`
	BalanceChanged bool            `json:"balanceChanged"`
	Findings       []mapc.Finding  `json:"findings"`
	Constants      []mapc.Constant `json:"constants,omitempty"`
}

func runTuning(argv []string, stdout, stderr io.Writer) int {
	var o opts
	var level, mapArg string
	hasMap := false
	for i := 0; i < len(argv); i++ {
		a := "-" + strings.Replace(argv[i], "--", "-", 1) // -x and --x
		switch a {
		case "--json":
			o.json = true
		case "--copy", "--level", "--map":
			if i+1 == len(argv) {
				fmt.Fprintf(stderr, "mapc: %s needs a value\n%s\n", a, tuningUsage)
				return 2
			}
			i++
			switch a {
			case "--copy":
				o.copyDir = argv[i]
			case "--level":
				level = argv[i]
			default:
				mapArg, hasMap = argv[i], true
			}
		default:
			fmt.Fprintf(stderr, "mapc: tuning: unknown argument %s\n%s\n", argv[i], tuningUsage)
			return 2
		}
	}
	t := &tool{o: o, out: stdout, errw: stderr}
	var m *mapc.Map
	if hasMap {
		var err error
		if m, err = readMapArg(mapArg); err != nil {
			return t.fail(2, "%v", err)
		}
	}
	if level == "" && m != nil {
		if _, ok := mapc.ShippedTuning[m.Tuning]; ok {
			level = m.Tuning
		}
	}
	if level == "" {
		level = mapc.TemplateLevel
	}
	shipped, ok := mapc.ShippedTuning[level]
	if !ok {
		return t.fail(2, "--level %q: one of %s", level, strings.Join(mapc.SharedLevels, ", "))
	}
	out := tuningOut{Level: level, Source: "shipped", Defaults: shipped.Rules(), Findings: []mapc.Finding{}}
	var rules *mapc.Rules
	if m != nil {
		rules = m.Rules
		out.Effective = mapc.EffectiveRules(out.Defaults, rules)
		out.Summary, out.Short = mapc.DescribeRules(rules)
		out.VictoryChanged, out.BalanceChanged = rules.VictoryChanged(), rules.BalanceChanged()
		for _, f := range mapc.CheckStructure(m, nil).Findings {
			if strings.HasPrefix(f.Rule, "rules.") || f.Rule == "names.tuning" {
				out.Findings = append(out.Findings, f)
			}
		}
	}
	if o.copyDir != "" {
		ad, err := os.ReadFile(filepath.Join(o.copyDir, filepath.FromSlash(mapc.AssetDataSource)))
		if err != nil {
			return t.fail(2, "the copy: %v", err)
		}
		lt, err := mapc.ReadLevelTuning(ad, level, rules)
		if err != nil {
			return t.fail(1, "the copy's tuning: %v", err)
		}
		out.Source, out.Defaults, out.Constants = "copy", lt.Defaults().Rules(), lt.Constants()
		if m != nil {
			out.Effective = mapc.EffectiveRules(out.Defaults, rules)
		}
	}
	if o.json {
		t.emit(out)
		return 0
	}
	t.printTuning(&out)
	return 0
}

// readMapArg reads --map's map: a map.json, a package folder or - (stdin).
func readMapArg(arg string) (*mapc.Map, error) {
	if arg == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("stdin: %v", err)
		}
		return mapc.Parse(b)
	}
	if st, err := os.Stat(arg); err == nil && st.IsDir() {
		return mapc.Load(arg)
	}
	b, err := os.ReadFile(arg)
	if err != nil {
		return nil, err
	}
	return mapc.Parse(b)
}

// printTuning is the text form: the level, the map's texts and findings, the effective values, the changed
// constants.
func (t *tool) printTuning(o *tuningOut) {
	fmt.Fprintf(t.out, "tuning level %s (%s values)\n", o.Level, o.Source)
	if o.Summary != "" {
		fmt.Fprintf(t.out, "rules: %s [%s]\n", o.Summary, o.Short)
	}
	for _, f := range o.Findings {
		fmt.Fprintf(t.out, "%s %s: %s: %s\n", f.Severity, f.Rule, f.Path, f.Message)
	}
	r := o.Defaults
	if o.Effective != nil {
		r = o.Effective
	}
	v := func(p *float64) string {
		if p == nil {
			return "-"
		}
		return fmt.Sprint(*p)
	}
	fmt.Fprintf(t.out, "respawn %s s + %s s a level + %s s a minute; minions at %s s, every %s s\n", v(r.Respawn.Base),
		v(r.Respawn.PerLevel), v(r.Respawn.PerMinute), v(r.Minions.FirstWave), v(r.Minions.Every))
	fmt.Fprintf(t.out, "gold %s at the start, %s a minute from %s s, kills x%s; XP x%s; Bindings back after %s s "+
		"(0: never)\n", v(r.Gold.Start), v(r.Gold.PerMinute), v(r.Gold.From), v(r.Gold.Kills), v(r.XP.Speed),
		v(r.Bindings.Respawn))
	allowed := r.Surrender.Allowed == nil || *r.Surrender.Allowed
	fmt.Fprintf(t.out, "wells open at %s s, relock %s s, gold x%s; surrender from minute %s (allowed %v)\n",
		v(r.Wells.OpensAt), v(r.Wells.Relock), v(r.Wells.Gold), v(r.Surrender.After), allowed)
	for _, c := range o.Constants {
		if c.Value != nil {
			fmt.Fprintf(t.out, "%s.%s: %v -> %v\n", c.Asset, c.Member, c.Default, c.Value)
		}
	}
}
