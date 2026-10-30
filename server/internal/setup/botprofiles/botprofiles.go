// Package botprofiles makes a copy's added archive game\Data\zz_BotProfiles.snap: the project's own bot profiles,
// BotClass assets that copy a shipped one and change only its role, byte for byte what re/scripts/bot_profiles.py makes
// (that script's docstring has the rule). The game ships no BotClass with the Predator role, so a Predator bot ran on
// the Warden's Tactician profile and every client showed it as a Tactician (the client sets a bot's role from its
// BotClass); PC_Warden_Predator is that profile with role Predator. PC_Golem_Hunter and PC_Survivor_Hunter put Faris
// and Freia, who ship without an AI, on Petrus's Hunter AI (a borrowed-AI profile: Profile.AI);
// PC_Desecrator_Tactician puts Desecrator, who ships without an AI too, on the Guardian's Tactician AI;
// PC_MerchantPrincess_Gladiator puts Mikella on the High Huntress's Gladiator AI; PC_Otter_Hunter puts Moya on
// Petrus's Hunter AI.
//
// The client loads every asset its catalog (AssetData/Binary/catalog_153.bin) lists, each .bin read by its path from
// the package depot, where every game\Data\*.snap is mounted alphabetically and the last archive with a path wins. So
// the archive holds the copy's own catalog with one entry appended per profile (a copy of the base asset's entry with
// the new name) and each profile's .bin (the base file with its role changed). The game's data can't ship, so the
// archive is made at setup time from the copy's own AssetData.snap. Every client and match-host copy gets it. Its
// catalog is the copy's only added one: it also lists every installed map's assets (catalog.go).
package botprofiles

import (
	"encoding/binary"
	"fmt"
	"strings"

	"dawngate/server/internal/setup/snap"
)

// The archive's name and place, and the file it is made from.
const (
	File    = "zz_BotProfiles.snap"
	Rel     = "game/Data/" + File        // in a copy
	Source  = "game/Data/AssetData.snap" // the copy's own asset data
	Catalog = "AssetData/Binary/catalog_153.bin"
)

// The layouts (the client's reflection tables): a CatalogEntry is 0x30 bytes (+0x00 assetNameType char*, +0x14 typeCrc,
// +0x1C sourceFileNameType char*, +0x24 tags {pointer, count}); a BotClass's fixed part 0x1C bytes with the role enum
// at +0x08; BotClassTypeCRC is the client's checksum of its BotClass table, which a catalog entry must carry.
const (
	entrySize       = 0x30
	botClassSize    = 0x1C
	botClassRole    = 0x08
	BotClassTypeCRC = 0xDF9D7C39
)

// Roles are RolesTuning.role[]'s indexes, the BotClass role enum.
const (
	RoleGladiator = 0
	RoleTactician = 1
	RoleHunter    = 2
	RolePredator  = 3
)

// Profile is one profile: its BotClass name (without ".BotClass"), the shipped BotClass it copies and its role. AI,
// when set, is the shipped BotClass whose AI definition (and, unless Skills or Spells are given, skill and spell order)
// the profile borrows: the base gives only its noun (a shaper that ships without an AI). The Hunter AI's gambits name
// ability slots, not abilities, so a slot casts the bot's own shaper's ability (the Tactician AI's too).
type Profile struct {
	Name, Base string
	Role       uint32
	AI         string
	Skills     []uint32 // the ability slot ranked at each level (3 Q, 4 W, 5 E, 6 R); nil: the AI donor's
	Spells     []string // nil: the AI donor's
}

// Profiles is the table, in the archive's order (bot_profiles.py's PROFILES and the match host's
// gms/core/bots/profiles.h list the same).
var Profiles = []Profile{
	{Name: "PC_Warden_Predator", Base: "PC_Warden", Role: RolePredator},
	// Faris and Freia as Hunters on Petrus's AI; their skill orders: Faris Q (never cast for him, but its ranks power
	// his basic attack), W, E; Freia Q, W, E; R at 6, 10, 15
	{Name: "PC_Golem_Hunter", Base: "PC_Golem", Role: RoleHunter, AI: "PC_Artificer",
		Skills: []uint32{3, 4, 5, 3, 3, 6, 3, 4, 3, 6, 4, 4, 4, 5, 6, 5, 5, 5}},
	{Name: "PC_Survivor_Hunter", Base: "PC_Survivor", Role: RoleHunter, AI: "PC_Artificer",
		Skills: []uint32{3, 4, 5, 3, 3, 6, 3, 4, 3, 6, 4, 4, 4, 5, 6, 5, 5, 5}},
	// Desecrator as a Tactician on the Guardian's AI, after a 2014 player guide: Q maxed first, E second, W last (R at
	// levels 6, 11, 16; the entries are the ability points of levels 1-9 and 11-19); Drain at level 1, Tailwind at 10,
	// Wither at 20
	{Name: "PC_Desecrator_Tactician", Base: "PC_Desecrator", Role: RoleTactician, AI: "PC_Guardian",
		Skills: []uint32{3, 5, 4, 3, 3, 6, 3, 5, 3, 6, 5, 5, 5, 4, 6, 4, 4, 4},
		Spells: []string{"Spell_Drain.Item", "Spell_Tailwind.Item", "Spell_Wither.Item"}},
	// Mikella as a Gladiator on the High Huntress's AI, after a 2014 player guide: Q maxed first, E second, W last (R at
	// levels 6, 11, 16); Dispel at level 1, Blitz at 10, Blink at 20 (each where the AI casts that kind of spell)
	{Name: "PC_MerchantPrincess_Gladiator", Base: "PC_MerchantPrincess", Role: RoleGladiator, AI: "PC_HighHuntress",
		Skills: []uint32{3, 5, 3, 4, 3, 6, 3, 5, 3, 6, 5, 5, 5, 4, 6, 4, 4, 4},
		Spells: []string{"Spell_Dispel.Item", "Spell_Blitz.Item", "Spell_Blink.Item"}},
	// Moya as a Hunter on Petrus's AI, after a 2014 player guide: Q maxed first, two points in E early, then W, E last
	// (R at levels 6, 11, 16); Vanquish at level 1 (the AI's camp spell), Blitz at 10 (its gank spell), Wither at 20
	{Name: "PC_Otter_Hunter", Base: "PC_Otter", Role: RoleHunter, AI: "PC_Artificer",
		Skills: []uint32{3, 5, 4, 3, 3, 6, 3, 5, 3, 6, 4, 4, 4, 4, 6, 5, 5, 5},
		Spells: []string{"Spell_Vanquish.Item", "Spell_Blitz.Item", "Spell_Wither.Item"}},
}

// BuildBotClass is the base BotClass file with its role field set to role.
func BuildBotClass(base []byte, role uint32) ([]byte, error) {
	if len(base) < botClassSize {
		return nil, fmt.Errorf("botprofiles: BotClass: shorter than its fixed part")
	}
	if old := binary.LittleEndian.Uint32(base[botClassRole:]); old > RolePredator {
		return nil, fmt.Errorf("botprofiles: BotClass: role %d is not a role", old)
	}
	out := append([]byte(nil), base...)
	binary.LittleEndian.PutUint32(out[botClassRole:], role)
	return out, nil
}

// botClass is a BotClass file's fields: the 7 fixed words (nounDef, aiDefinition, role, skillOrder pointer and count,
// spellOrder pointer and count) and the out-of-line data, in member order: the noun's name, the AI's name, the skill
// order's words, the spell order's pointer words, then each spell's name.
type botClass struct {
	words      [7]uint32
	noun, ai   string
	skills     []uint32
	spells     []string
	spellWords []uint32
}

func parseBotClass(d []byte) (*botClass, error) {
	le := binary.LittleEndian
	if len(d) < botClassSize {
		return nil, fmt.Errorf("botprofiles: BotClass: shorter than its fixed part")
	}
	b := &botClass{}
	for i := range b.words {
		b.words[i] = le.Uint32(d[4*i:])
	}
	pos := botClassSize
	var err error
	if b.words[0] != 0 {
		if b.noun, pos, err = cstr(d, pos); err != nil {
			return nil, err
		}
	}
	if b.words[1] != 0 {
		if b.ai, pos, err = cstr(d, pos); err != nil {
			return nil, err
		}
	}
	nSkill, nSpell := int(b.words[4]), int(b.words[6])
	if nSkill > (len(d)-pos)/4 || nSpell > (len(d)-pos)/4 {
		return nil, fmt.Errorf("botprofiles: BotClass: an array exceeds the file")
	}
	for i := 0; i < nSkill; i++ {
		b.skills = append(b.skills, le.Uint32(d[pos:]))
		pos += 4
	}
	if nSpell*4 > len(d)-pos {
		return nil, fmt.Errorf("botprofiles: BotClass: an array exceeds the file")
	}
	for i := 0; i < nSpell; i++ {
		b.spellWords = append(b.spellWords, le.Uint32(d[pos:]))
		pos += 4
	}
	for _, w := range b.spellWords {
		if w == 0 {
			return nil, fmt.Errorf("botprofiles: BotClass: a spell without a name")
		}
		var s string
		if s, pos, err = cstr(d, pos); err != nil {
			return nil, err
		}
		b.spells = append(b.spells, s)
	}
	if pos != len(d) {
		return nil, fmt.Errorf("botprofiles: BotClass: the walk ends at %#x, the file at %#x", pos, len(d))
	}
	if b.words[2] > RolePredator {
		return nil, fmt.Errorf("botprofiles: BotClass: role %d is not a role", b.words[2])
	}
	return b, nil
}

func (b *botClass) bytes() []byte {
	le := binary.LittleEndian
	w := b.words
	if b.noun == "" {
		w[0] = 0
	}
	if b.ai == "" {
		w[1] = 0
	}
	if len(b.skills) == 0 {
		w[3] = 0
	}
	if len(b.spells) == 0 {
		w[5] = 0
	}
	w[4], w[6] = uint32(len(b.skills)), uint32(len(b.spells))
	var out []byte
	for _, x := range w {
		out = le.AppendUint32(out, x)
	}
	for _, s := range []string{b.noun, b.ai} {
		if s != "" {
			out = append(append(out, s...), 0)
		}
	}
	for _, x := range b.skills {
		out = le.AppendUint32(out, x)
	}
	for _, x := range b.spellWords {
		out = le.AppendUint32(out, x)
	}
	for _, s := range b.spells {
		out = append(append(out, s...), 0)
	}
	return out
}

// BuildBorrowed is a borrowed-AI profile: the base BotClass's noun on the donor's AI definition with role; the skill
// and spell order the given ones (nil: the donor's); the pointer words the donor's (a spell beyond the donor's count
// gets the previous word + 0xE8: any non-zero word reads as present).
func BuildBorrowed(base, donor []byte, role uint32, skills []uint32, spells []string) ([]byte, error) {
	b, err := parseBotClass(base)
	if err != nil {
		return nil, err
	}
	d, err := parseBotClass(donor)
	if err != nil {
		return nil, err
	}
	if b.noun == "" || d.ai == "" {
		return nil, fmt.Errorf("botprofiles: BotClass: the base has no noun or the donor no AI definition")
	}
	if skills == nil {
		skills = d.skills
	}
	if spells == nil {
		spells = d.spells
	}
	out := &botClass{words: d.words, noun: b.noun, ai: d.ai, skills: append([]uint32(nil), skills...),
		spells: append([]string(nil), spells...)}
	out.words[2] = role
	if (len(skills) > 0 && d.words[3] == 0) || (len(spells) > 0 && d.words[5] == 0) {
		return nil, fmt.Errorf("botprofiles: BotClass: the donor has no pointer word for a member the profile fills")
	}
	for _, s := range skills {
		if s < 3 || s > 6 {
			return nil, fmt.Errorf("botprofiles: BotClass: a skill order entry is not a ranked slot (3-6)")
		}
	}
	for i := range spells {
		if i < len(d.spellWords) {
			out.spellWords = append(out.spellWords, d.spellWords[i])
			continue
		}
		prev := out.words[5]
		if len(out.spellWords) > 0 {
			prev = out.spellWords[len(out.spellWords)-1]
		}
		out.spellWords = append(out.spellWords, prev+0xE8)
	}
	res := out.bytes()
	if _, err := parseBotClass(res); err != nil {
		return nil, err
	}
	return res, nil
}

// Build is the archive's bytes, made from a copy's AssetData.snap (the whole file). Its catalog lists the bot profiles
// and then the entries of every source in maps (the installed maps' assets, whose files their own archives hold),
// sorted by SortSources; without maps it is the bot profiles' archive alone.
func Build(assetData []byte, maps ...CatalogSource) ([]byte, error) {
	a, err := snap.Parse(assetData)
	if err != nil {
		return nil, err
	}
	byName := map[string]snap.Entry{}
	for _, e := range a.Entries {
		byName[strings.ToLower(e.Name)] = e
	}
	read := func(rel string) ([]byte, error) {
		e, ok := byName[strings.ToLower(rel)]
		if !ok {
			return nil, fmt.Errorf("botprofiles: AssetData.snap has no %s", rel)
		}
		return a.Read(e)
	}
	cat, err := read(Catalog)
	if err != nil {
		return nil, err
	}
	for _, m := range maps {
		if m.Name == ProfilesSource {
			return nil, fmt.Errorf("botprofiles: a map source can't be named %q", ProfilesSource)
		}
	}
	cat, err = MergeCatalog(cat, SortSources(append([]CatalogSource{ProfileEntries()}, maps...)))
	if err != nil {
		return nil, err
	}
	files := []snap.File{{Name: Catalog, Data: cat}}
	for _, p := range Profiles {
		base, err := read("AssetData/Binary/" + p.Base + ".BotClass.bin")
		if err != nil {
			return nil, err
		}
		var b []byte
		if p.AI == "" {
			b, err = BuildBotClass(base, p.Role)
		} else {
			var donor []byte
			if donor, err = read("AssetData/Binary/" + p.AI + ".BotClass.bin"); err == nil {
				b, err = BuildBorrowed(base, donor, p.Role, p.Skills, p.Spells)
			}
		}
		if err != nil {
			return nil, err
		}
		files = append(files, snap.File{Name: "AssetData/Binary/" + p.Name + ".BotClass.bin", Data: b})
	}
	return snap.Pack(files), nil
}
