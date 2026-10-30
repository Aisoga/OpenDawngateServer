package blaze

// The level a server's hosted games load (dgserver --map, its older name --level) and a custom game's own pick (a tag
// at the end of its name, --custom-maps). The match host loads the level that createGame's `level` names
// (AssetCatalog_FindByName of "<level>.Level", 0x008CF0F0) and its GamePrepareForStart carries that level's asset id,
// which the client resolves to the same Level asset. MatchOptionsUI's custom-game dialog (OK handler 0x004EA5B0)
// always sends SM_Map_v5's id (the constant 0x946D41FE at 0x013E2B7C, kNumPlayableMaps = 1), so the server decides
// the map: a game on another level gets that level's LevelId attribute instead of the client's.
//
// The client names a map only from its one-entry table at 0x013F3A78 ({L"^labs_global!0x30000001" "The Dawngate",
// 0x946D41FE}), which GameEntryManager's map-name method (0x00684380, vtable 0x011F4DA0 + 0x28) searches for the
// game's LevelId attribute: for any other id it returns null, so the game browser's Map column stays empty
// (0x004E4485) and the custom lobby asserts "resultName" (CustomMatchLobbyUI.cpp 397, 0x00428509) and shows its
// fallback key ^FE_CustomGameLobby!0x0f2b57c5, which the shipped table lacks. So a game on another map carries the map
// in its name too (PlayableLevel.Label). The setup's exe patch (exepatch's maptable) sends that null case to a cave that
// looks up ^FE_CustomGameLobby!0x<LevelId> instead, and the setup writes that line for every map the copy has
// (localepatch.MapLines), so a patched client names every installed map in the lobby and the browser.

import (
	"fmt"
	"strconv"
	"strings"

	"dawngate/server/internal/content"
	"dawngate/server/internal/tdf"
)

// maxLevelName keeps "<name>.Level" inside the host's 96-byte level buffer.
const maxLevelName = 80

// PlayableLevel is a level a server may host: one whose files a game copy can have.
type PlayableLevel struct {
	Name    string // the Level asset's name (createGame's level without ".Level")
	Tag     string // a custom game's name tag picks it: "<name>~#<Tag>" (MapTagPrefix)
	Label   string // put in front of the game's name in the game browser, whose Map column can't show the level
	Archive string // the added archive in game\Data that holds its files ("" = they ship with the game)
	Display string // the map's name in what players read (the create dialog's list and the lobby's map line say it too)
}

// PlayableLevels are the levels dgserver accepts. SM_Map_v5 ships whole; MM_Map_v3 (the cut one-lane map) needs
// zz_MM_Map_v3.snap in every player's copy and the host copy, which the release's setup makes from the copy's own
// files; a player whose copy may lack it is kept out of its games (the map check, mapguard.go). Every other shipped
// Level asset lacks its graphs, physics mesh and ground. The maps of a maps folder come after these (AddLevels).
// MM_Map_v3 is shown to players as Anshavar Pass ("[Pass] " in front of a game's name); its name, tag and archive
// stay the internal ones.
var PlayableLevels = []PlayableLevel{
	{Name: HostedLevel, Tag: "sm", Display: "The Dawngate"},
	{Name: "MM_Map_v3", Tag: "mm", Label: "[Pass] ", Archive: "zz_MM_Map_v3.snap", Display: "Anshavar Pass"},
}

// AddLevels adds the maps of a maps folder (dgserver --maps: the packages' names, tags, prefixes, display names and
// archives) to PlayableLevels, after the built-in ones. Like MM_Map_v3 each needs its archive in every player's copy
// and the host copy (the setup compiles it there), and the map check keeps a player whose copy may lack it out of its
// games. A level whose name or tag is taken, or isn't one ParseLevel would take, is refused (nothing is added then).
func AddLevels(ls ...PlayableLevel) (err error) {
	orig := PlayableLevels
	defer func() {
		if err != nil {
			PlayableLevels = orig
		}
	}()
	added := append([]PlayableLevel(nil), PlayableLevels...)
	for _, l := range ls {
		name, err := ParseLevel(l.Name)
		if err == nil {
			return fmt.Errorf("map %s: a map of this name is already offered (%s)", l.Name, name)
		}
		if l.Tag == "" || l.Archive == "" || l.Display == "" {
			return fmt.Errorf("map %s: a tag, an archive and a display name are needed", l.Name)
		}
		for _, c := range l.Name {
			if !(c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
				return fmt.Errorf("map %q: only letters, digits and underscores (a Level asset's name)", l.Name)
			}
		}
		if len(l.Name) > maxLevelName {
			return fmt.Errorf("map %s: at most %d characters", l.Name, maxLevelName)
		}
		for _, o := range added {
			if strings.EqualFold(o.Tag, l.Tag) || strings.EqualFold(o.Name, l.Tag) || strings.EqualFold(o.Tag, l.Name) {
				return fmt.Errorf("map %s: its tag or name %q is %s's", l.Name, l.Tag, o.Name)
			}
		}
		added = append(added, l)
		PlayableLevels = added // ParseLevel sees the ones added so far
	}
	return nil
}

// LevelDisplay is the map's name for players' messages: its Display, else the level's own name.
func LevelDisplay(level string) string {
	if l, ok := FindLevel(level); ok && l.Display != "" {
		return l.Display
	}
	return level
}

// MapTagPrefix starts a custom game's map tag at the end of its name ("Friday fun~#mm"); the server strips it, as it
// does BotFillMarker (the two in either order).
const MapTagPrefix = "~#"

// LevelAssetID is a level's AssetCatalog id, FNV-1 of the lower-cased "<name>.level" (SM_Map_v5 -> 0x946D41FE =
// 2490188286, MM_Map_v3 -> 0x420DE52E = 1108206894).
func LevelAssetID(name string) uint32 { return content.AssetID(name + ".Level") }

// FindLevel is the playable level of this name (any case), or false.
func FindLevel(name string) (PlayableLevel, bool) {
	for _, l := range PlayableLevels {
		if strings.EqualFold(l.Name, name) {
			return l, true
		}
	}
	return PlayableLevel{}, false
}

// ParseLevel checks a level name as dgserver --map takes it ("MM_Map_v3", "MM_Map_v3.Level", or its tag "mm") and
// returns its PlayableLevels name. A name that isn't a playable level is refused: no game copy has its files, and a
// host without them doesn't start the match (its log's "level FAILED").
func ParseLevel(s string) (string, error) {
	name := strings.TrimSpace(s)
	if len(name) > len(".Level") && strings.EqualFold(name[len(name)-len(".Level"):], ".Level") {
		name = name[:len(name)-len(".Level")]
	}
	if name == "" || len(name) > maxLevelName {
		return "", fmt.Errorf("--map must be a level name such as %s (1 to %d characters), not %q", HostedLevel,
			maxLevelName, s)
	}
	for _, c := range name {
		if !(c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return "", fmt.Errorf("--map %q: only letters, digits and underscores (a Level asset's name)", s)
		}
	}
	for _, l := range PlayableLevels {
		if strings.EqualFold(l.Name, name) || strings.EqualFold(l.Tag, name) {
			return l.Name, nil
		}
	}
	return "", fmt.Errorf("--map %q: not a map this server can host (%s)", s, levelNames())
}

// ParseLevels is dgserver --custom-maps: comma-separated ParseLevel names, "" or "none" = none.
func ParseLevels(s string) ([]string, error) {
	var out []string
	if strings.EqualFold(strings.TrimSpace(s), "none") {
		return nil, nil
	}
	for _, part := range strings.Split(s, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		name, err := ParseLevel(part)
		if err != nil {
			return nil, err
		}
		if !containsFold(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// levelNames lists PlayableLevels for an error: "SM_Map_v5 (sm), MM_Map_v3 (mm)".
func levelNames() string {
	var b []string
	for _, l := range PlayableLevels {
		b = append(b, l.Name+" ("+l.Tag+")")
	}
	return strings.Join(b, ", ")
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// hostedLevel is the level this server's hosted games load: Server.Level, else HostedLevel (also for a nil *Server, a
// test's bare hostedGame).
func (s *Server) hostedLevel() string {
	if s != nil && s.Level != "" {
		return s.Level
	}
	return HostedLevel
}

// gameLevel is the level game g loads: its own (a custom game's pick), else the server's.
func (s *Server) gameLevel(g Game) string {
	if g.Level != "" {
		return g.Level
	}
	return s.hostedLevel()
}

// customLevelOffered reports whether a custom game's creator may pick level by its name tag: SM_Map_v5 (every copy
// has it), the server's own level, and --custom-maps.
func (s *Server) customLevelOffered(level string) bool {
	return strings.EqualFold(level, HostedLevel) || strings.EqualFold(level, s.hostedLevel()) ||
		containsFold(s.CustomLevels, level)
}

// OfferedMapTags are the name tags of the maps besides SM_Map_v5 that a custom game's creator may pick on a server
// whose own level is level ("" = SM_Map_v5) and whose --custom-maps are custom (customLevelOffered), in
// PlayableLevels' order: the tunnel's FactMaps, from which Start-Client lists the maps in the CREATE GAME dialog.
func OfferedMapTags(level string, custom []string) []string {
	var tags []string
	for _, l := range OfferedLevels(level, custom) {
		tags = append(tags, l.Tag)
	}
	return tags
}

// OfferedLevels are OfferedMapTags' levels themselves (the home page names them by their Display, HOME1).
func OfferedLevels(level string, custom []string) []PlayableLevel {
	var out []PlayableLevel
	for _, l := range PlayableLevels {
		if !strings.EqualFold(l.Name, HostedLevel) && (strings.EqualFold(l.Name, level) || containsFold(custom, l.Name)) {
			out = append(out, l)
		}
	}
	return out
}

// levelAttrs is what a custom game's attributes get for its level: its LevelId when it is another level than the
// dialog's (none otherwise, so the client's own attribute stays as it sent it).
func levelAttrs(level string) []tdf.Pair {
	if level == "" || strings.EqualFold(level, HostedLevel) {
		return nil
	}
	return []tdf.Pair{tdf.KV(AttrLevelID, strconv.FormatUint(uint64(LevelAssetID(level)), 10))}
}

// cutNameTags takes a custom game's tags off the end of its name, in any order: BotFillMarker (the create dialog's
// bot checkbox) and map tags ("~#mm"), all of them stripped. Of several map tags the first in the name wins: a tag
// typed at the end of the name comes before the one the create dialog's map list appends (the dialog adds "~#sm" for
// its default, The Dawngate), so typing a tag beats the list. A "~#" that is no known tag stays part of the name.
func cutNameTags(name string) (rest string, bots bool, tag PlayableLevel, hasTag bool) {
	rest = name
	for {
		lower := strings.ToLower(rest)
		if strings.HasSuffix(lower, BotFillMarker) {
			rest, bots = rest[:len(rest)-len(BotFillMarker)], true
			continue
		}
		found := false
		for _, l := range PlayableLevels {
			if t := MapTagPrefix + l.Tag; strings.HasSuffix(lower, t) {
				// cut from the end, so the one cut last is the first in the name
				rest, found = rest[:len(rest)-len(t)], true
				tag, hasTag = l, true
				break
			}
		}
		if !found {
			return strings.TrimSpace(rest), bots, tag, hasTag
		}
	}
}
