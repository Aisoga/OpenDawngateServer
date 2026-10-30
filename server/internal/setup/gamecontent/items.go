package gamecontent

// The match items' parts that AssetData alone doesn't give (the item browser): the passives' names from their
// scripts in ServerData.package with the Locale, the shop filters' titles (Locale references "^ItemShop!0x…" that the
// dump leaves unresolved), and the items' icons from UI_Icons.snap, written as PNG beside the content file.

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/dbpf"
	"dawngate/server/internal/setup/luac"
	"dawngate/server/internal/setup/pytext"
)

// The inputs of the item parts, relative to the Data folder.
var (
	ServerDataPackage = "ServerData.package"
	UIIconsSnap       = "UI_Icons.snap"
)

// luaType is the resource type of ServerData's scripts (FNV-1 of "lua"); a script's group is FNV-1 of its folder
// and its instance FNV-1 of its name, both lower-cased.
const luaType = 0x3681D755

func fnv(s string) uint32 { return pytext.FNV1([]byte(strings.ToLower(s))) }

var locRef = regexp.MustCompile(`^\^?([^!]+)!0x([0-9a-fA-F]{8})$`)

// localeText resolves a Locale reference "Table!0x<id>" or "^Table!0x<id>" (the shop's) to its English text.
func localeText(loc *assetdata.Locale, ref string) string {
	m := locRef.FindStringSubmatch(ref)
	if m == nil {
		return ""
	}
	id, _ := strconv.ParseUint(m[2], 16, 32)
	s, _ := loc.Text(fnv(m[1]), uint32(id))
	return strings.TrimSpace(s)
}

// scripts finds ServerData's modifier scripts by name: by their resource key first, else by the name they register
// (one script's key differs from its name: it was shipped as source with a blank at the end of its file name).
type scripts struct {
	pkg     *dbpf.Package
	byKey   map[uint32]dbpf.Entry
	byName  map[string]*luac.Script // filled on the first miss
	scanned bool
	cache   map[string]*luac.Script // table's
}

func openScripts(data string) (*scripts, error) {
	pkg, err := dbpf.Open(filepath.Join(data, ServerDataPackage))
	if err != nil {
		return nil, err
	}
	s := &scripts{pkg: pkg, byKey: map[uint32]dbpf.Entry{}}
	// the modifiers, and the abilities a passive's text can name (ability_Item_…); a name in both: the modifier's
	for _, group := range []uint32{fnv("Abilities"), fnv("Modifiers")} {
		for _, e := range pkg.Entries {
			if e.Type == luaType && e.Group == group {
				s.byKey[uint32(e.Instance)] = e
			}
		}
	}
	return s, nil
}

func (s *scripts) load(e dbpf.Entry) *luac.Script {
	raw, err := s.pkg.Read(e)
	if err != nil {
		return nil
	}
	sc, err := luac.Load(raw)
	if err != nil {
		return nil
	}
	if sc.Env != nil { // a handler may read another script's table (a passive's _Stacks modifier)
		sc.Env.Lookup = s.table
	}
	return sc
}

// table is the table of the script that registers name, for a handler of another script (nil when none; only
// names of scripts, "modifier_…" and "ability_…", are looked up).
func (s *scripts) table(name string) luac.Value {
	if !strings.HasPrefix(name, "modifier_") && !strings.HasPrefix(name, "ability_") {
		return nil
	}
	if s.cache == nil {
		s.cache = map[string]*luac.Script{}
	}
	sc, ok := s.cache[name]
	if !ok {
		sc = s.get(name)
		s.cache[name] = sc
	}
	if sc == nil {
		return nil
	}
	return sc.Table
}

// get returns the script that registers name, or nil.
func (s *scripts) get(name string) *luac.Script {
	if e, ok := s.byKey[fnv(name)]; ok {
		if sc := s.load(e); sc != nil && strings.EqualFold(sc.Name, name) {
			return sc
		}
	}
	if !s.scanned {
		s.scanned, s.byName = true, map[string]*luac.Script{}
		keys := make([]uint32, 0, len(s.byKey))
		for k := range s.byKey {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, k := range keys {
			if sc := s.load(s.byKey[k]); sc != nil {
				if _, dup := s.byName[strings.ToLower(sc.Name)]; !dup {
					s.byName[strings.ToLower(sc.Name)] = sc
				}
			}
		}
	}
	return s.byName[strings.ToLower(name)]
}

var fontLead = regexp.MustCompile(`^\s*<font[^>]*>([^<]+)</font>\s*:`)

// guidText is the English text of a script's Locale GUID: in its localizedGroup's table, else in any table.
func guidText(sc *luac.Script, g luac.GUID, loc *assetdata.Locale) string {
	id, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(string(g)), "0x"), 16, 32)
	if err != nil {
		return ""
	}
	if grp, ok := sc.Field("localizedGroup").(luac.SPID); ok {
		if s, ok := loc.Text(fnv(string(grp)), uint32(id)); ok {
			return s
		}
	}
	s, _ := loc.TextAny(uint32(id))
	return s
}

// rankedDescription is the description a passive shows at its rank: what its GetLocalizedDescription handler
// returns for p.rank (the ranked passives: rankOneLoc, rankTwoLoc, rankThreeLoc for ranks 1-3), else its text
// without a creature or its localizedDescription.
func rankedDescription(sc *luac.Script, rank int, loc *assetdata.Locale) string {
	p := luac.NewTable()
	p.Set("rank", float64(rank))
	if g, ok := sc.Call("GetLocalizedDescription", p); ok {
		if g, ok := g.(luac.GUID); ok {
			if s := guidText(sc, g, loc); s != "" {
				return s
			}
		}
	}
	// localizedDescriptionNoObject: the text a passive has without a creature (23 scripts; the client's string at
	// 0x01241B74), which is the item browser's case; else the description
	for _, f := range []string{"localizedDescriptionNoObject", "localizedDescription"} {
		if g, ok := sc.Field(f).(luac.GUID); ok {
			if s := guidText(sc, g, loc); s != "" {
				return s
			}
		}
	}
	return ""
}

// passiveName is a passive's English name at its rank: the name its ranked description starts with
// ("<font …>Toughness II</font>: …") for a passive with a GetLocalizedDescription handler, else its localizedName,
// else the name its description starts with.
func passiveName(sc *luac.Script, rank int, loc *assetdata.Locale) string {
	lead := func() string {
		if m := fontLead.FindStringSubmatch(rankedDescription(sc, rank, loc)); m != nil {
			return strings.TrimSpace(m[1])
		}
		return ""
	}
	if sc.Handler("GetLocalizedDescription") != nil {
		if n := lead(); n != "" {
			return n
		}
	}
	if g, ok := sc.Field("localizedName").(luac.GUID); ok {
		if n := strings.TrimSpace(stripTags(guidText(sc, g, loc))); n != "" {
			return n
		}
	}
	return lead()
}

var tag = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string { return tag.ReplaceAllString(s, "") }

// resolveItems fills the titles content.BuildFrom leaves empty: the shop filters' (from the Locale) and the
// passives' (from their scripts). Without ServerData.package the passives keep no title.
func resolveItems(c *content.Content, data string, loc *assetdata.Locale) error {
	var passive func(content.ItemPassive) string
	sc, err := openScripts(data)
	switch {
	case err == nil:
		passive = func(p content.ItemPassive) string {
			if s := sc.get(p.Modifier); s != nil {
				return passiveName(s, p.Rank, loc)
			}
			return ""
		}
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("gamecontent: %w", err)
	}
	c.ResolveTitles(func(ref string) string { return localeText(loc, ref) }, passive)
	for i := range c.MatchItems { // the stat lines as the shop writes them (statline.go)
		for j := range c.MatchItems[i].Stats {
			st := &c.MatchItems[i].Stats[j]
			st.Parts = statParts(*st, loc)
		}
	}
	if sc != nil { // the passives' texts with their numbers (passivetext.go) and labels (statline.go)
		for i := range c.MatchItems {
			if it := &c.MatchItems[i]; it.Active != "" { // the spells' and consumables' actives, the same way
				if s := sc.get(it.Active); s != nil {
					a := &content.ItemActive{Title: passiveLead(s, 1, loc), Kind: passiveKind(s)}
					a.Text, a.Parts = passiveText(sc, s, 1, loc)
					if cd, ok := ranked(s.Field("cooldown"), 1).(float64); ok && cd > 0 {
						a.Cooldown = cd
					}
					if a.Text != "" {
						it.ActiveInfo = a
					}
				}
			}
			for j := range c.MatchItems[i].Passives {
				p := &c.MatchItems[i].Passives[j]
				if s := sc.get(p.Modifier); s != nil {
					p.Text, p.Parts = passiveText(sc, s, p.Rank, loc)
					p.Kind = passiveKind(s)
					if lead := passiveLead(s, p.Rank, loc); lead != "" { // the name as the shop shows it (Consume I)
						p.Title = lead
					}
				}
			}
		}
	}
	return nil
}

// iconKey is an icon path as UI_Icons.snap's index compares it: lower case, "/" for "\".
func iconKey(p string) string { return strings.ToLower(strings.ReplaceAll(p, `\`, "/")) }
