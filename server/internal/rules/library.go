package rules

import (
	"bufio"
	"bytes"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The operator's presets and a map's suggested lobby setup (RULES6).
//
// An operator names combinations in a file of the server's data folder (Start-Server.ini's rule_presets, dgserver
// --rule-presets), one a line: "id | Name | <rules>", where <rules> is the short form ("pick=random,victory=bind1") or
// a JSON object. They join the built-in presets in a Library: a map's suggestion and the server's default lobby start
// (default_rules) may name them, and a lobby whose combination equals one shows "[Name] " in the game browser. The
// lobby's panel keeps the built-in presets only (Standard, Draft, Custom), so such a lobby shows as Custom there with its
// categories.

// Limits of the operator's file: its size, its presets, an id's and a name's length.
const (
	MaxPresetFile = 64 << 10
	MaxOwnPresets = 50
	MaxPresetID   = 24
	MaxPresetName = 24
)

// bom is the byte order mark Notepad may put at the file's start.
const bom = "\uFEFF"

// Library is the presets a server knows: the built-in ones and the operator's own. A nil Library has the built-in ones
// only.
type Library struct {
	own []Preset
}

// Own returns the operator's presets (a copy), in the file's order.
func (l *Library) Own() []Preset {
	if l == nil {
		return nil
	}
	return append([]Preset(nil), l.own...)
}

// IsBuiltIn reports whether id names a built-in preset (case-insensitive).
func IsBuiltIn(id string) bool {
	_, ok := FindPreset(id)
	return ok
}

// Find returns preset id (case-insensitive): a built-in one, else the operator's.
func (l *Library) Find(id string) (Preset, bool) {
	if p, ok := FindPreset(id); ok {
		return p, true
	}
	if l != nil {
		for _, p := range l.own {
			if strings.EqualFold(p.ID, id) {
				return p, true
			}
		}
	}
	return Preset{}, false
}

// Of returns the preset whose combination c is exactly: a built-in one, else the operator's (ok false: none).
func (l *Library) Of(c Combination) (Preset, bool) {
	if p, ok := PresetOf(c); ok {
		return p, true
	}
	return l.ownOf(c)
}

func (l *Library) ownOf(c Combination) (Preset, bool) {
	if l == nil {
		return Preset{}, false
	}
	c = c.Normal()
	for _, p := range l.own {
		if p.Options == c {
			return p, true
		}
	}
	return Preset{}, false
}

// Prefix is Prefix with the operator's presets: a combination equal to one of them (its win condition included) is
// "[<its name>] ", then the win condition's prefix; one that equals an operator's preset only without its win condition
// (the preset keeps the Guardian's) the same; otherwise Prefix's built-in rule.
func (l *Library) Prefix(c Combination) string {
	c = c.Normal()
	win := c.Goal().Prefix()
	if p, ok := l.ownOf(c); ok {
		return "[" + p.Name + "] " + win
	}
	rest := c
	rest.Victory = ""
	if rest.IsDefault() {
		return win
	}
	if p, ok := PresetOf(rest); ok {
		return "[" + p.Name + "] " + win
	}
	if p, ok := l.ownOf(rest); ok {
		return "[" + p.Name + "] " + win
	}
	return CustomPrefix + win
}

// Resolve reads a preset's id (built-in or the operator's), a combination's JSON or its short form, as Parse does.
func (l *Library) Resolve(spec string) (Combination, error) {
	spec = strings.TrimSpace(spec)
	if strings.HasPrefix(spec, "{") || strings.Contains(spec, "=") {
		return Parse(spec)
	}
	if p, ok := l.Find(spec); ok {
		return p.Options, nil
	}
	return Combination{}, fmt.Errorf("rules: %w preset %q", ErrUnknown, clip(spec))
}

// ValidPresetID reports whether id can name a preset: 1 to MaxPresetID letters, digits, '-' and '_', starting with a
// letter (the short form's "=" and ",", a JSON object's "{" never occur in one).
func ValidPresetID(id string) bool {
	if id == "" || len(id) > MaxPresetID {
		return false
	}
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && (r >= '0' && r <= '9' || r == '-' || r == '_'):
		default:
			return false
		}
	}
	return true
}

// validPresetName: 1 to MaxPresetName printable characters, none of the browser prefix's brackets or the file's "|".
func validPresetName(name string) string {
	switch {
	case name == "":
		return "no name"
	case !utf8.ValidString(name):
		return "the name isn't UTF-8"
	case utf8.RuneCountInString(name) > MaxPresetName:
		return fmt.Sprintf("the name is longer than %d characters", MaxPresetName)
	case strings.ContainsAny(name, "[]|"):
		return "the name has [, ] or |"
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return "the name has a control character"
		}
	}
	return ""
}

// ParsePresets reads the operator's preset file: a line "id | Name | <rules>" each, blank lines and lines starting with
// # or ; skipped. A bad line is left out with a problem ("line 3: ..."): too many fields, a bad id or name, rules that
// don't parse, an id of a built-in preset or one used before, a combination a built-in or earlier preset already is
// (PresetOf takes the first match, so a second name would never show), more than MaxOwnPresets. A file over
// MaxPresetFile is refused whole. The Library is never nil.
func ParsePresets(data []byte) (*Library, []string) {
	l := &Library{}
	if len(data) > MaxPresetFile {
		return l, []string{fmt.Sprintf("the file has %d bytes, more than %d: no presets read", len(data), MaxPresetFile)}
	}
	var problems []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), MaxPresetFile+1)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), bom))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		bad := func(format string, a ...any) {
			problems = append(problems, fmt.Sprintf("line %d: ", n)+fmt.Sprintf(format, a...))
		}
		parts := strings.Split(line, "|")
		if len(parts) != 3 {
			bad("not \"id | Name | rules\" (%d fields)", len(parts))
			continue
		}
		id, name, spec := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
		if !ValidPresetID(id) {
			bad("the id %q isn't 1 to %d letters, digits, - and _ starting with a letter", clip(id), MaxPresetID)
			continue
		}
		if why := validPresetName(name); why != "" {
			bad("%s: %s", id, why)
			continue
		}
		if !strings.HasPrefix(spec, "{") && !strings.Contains(spec, "=") {
			bad("%s: the rules %q are no category=option list (e.g. pick=random,shop=onDeath)", id, clip(spec))
			continue
		}
		c, err := Parse(spec)
		if err != nil {
			bad("%s: %v", id, err)
			continue
		}
		c = c.Normal()
		if IsBuiltIn(id) {
			bad("%s: the id is a built-in preset's", id)
			continue
		}
		if p, ok := l.Find(id); ok {
			bad("%s: the id is used before (%s)", id, p.Name)
			continue
		}
		if p, ok := l.Of(c); ok {
			bad("%s: the same rules as the preset %s (%s)", id, p.ID, p.Name)
			continue
		}
		if len(l.own) == MaxOwnPresets {
			bad("%s: more than %d presets", id, MaxOwnPresets)
			continue
		}
		l.own = append(l.own, Preset{ID: id, Name: name, Options: c})
	}
	if err := sc.Err(); err != nil {
		problems = append(problems, "reading: "+err.Error())
	}
	return l, problems
}

// AllHostFeatures are the host features of the catalog's options: Check with them assumes a host that plays every
// option (a map's suggestion is judged before any host is known).
var AllHostFeatures = []string{HostRules2, HostMapRules2}

// SuggestionContext is the checks' context for a suggested setup judged away from a game: a host that plays every
// option, no bots, the server's shaper select on.
func SuggestionContext(mapVictory bool) Context {
	return Context{HostRules: true, HostFeatures: slices.Clone(AllHostFeatures), MapVictory: mapVictory}
}

// CheckSuggestion judges a map's suggested lobby setup (mapc's rules.lobbyPreset) with the built-in presets: errs make
// the map wrong (no preset id or rules, rules that don't parse, a win condition on a map with its own), warns don't (an
// id this catalog doesn't have: only servers that define that preset use it; a combination the lobby's checks refuse,
// so the lobby starts on Standard). Empty spec: nothing.
func CheckSuggestion(spec string, mapVictory bool) (errs, warns []string) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	var c Combination
	if strings.HasPrefix(spec, "{") || strings.Contains(spec, "=") {
		var err error
		if c, err = Parse(spec); err != nil {
			return []string{strings.TrimPrefix(err.Error(), "rules: ")}, nil
		}
	} else if p, ok := FindPreset(spec); ok {
		c = p.Options
	} else if ValidPresetID(spec) {
		return nil, []string{fmt.Sprintf("no built-in preset is called %q: only servers that define this preset use it "+
			"(the others start the lobby as usual)", spec)}
	} else {
		return []string{fmt.Sprintf("%q is neither a preset's id nor rules such as pick=random,victory=bind1", clip(spec))}, nil
	}
	c = c.Normal()
	if mapVictory && c.Victory != "guardian" {
		return []string{"the map has its own win condition, so the lobby's can't be suggested: keep the Win condition on Guardian only"}, nil
	}
	for _, f := range Check(c, SuggestionContext(mapVictory)) {
		warns = append(warns, f.Message+" The lobby then starts on Standard.")
	}
	return nil, warns
}
