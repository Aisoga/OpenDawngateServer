// Package botprofiles makes a copy's added archive game\Data\zz_BotProfiles.snap: the project's own bot profiles,
// BotClass assets that copy a shipped one and change only its role, byte for byte what re/scripts/bot_profiles.py makes
// (that script's docstring has the rule). The game ships no BotClass with the Predator role, so a Predator bot ran on
// the Warden's Tactician profile and every client showed it as a Tactician (the client sets a bot's role from its
// BotClass); PC_Warden_Predator is that profile with role Predator.
//
// The client loads every asset its catalog (AssetData/Binary/catalog_153.bin) lists, each .bin read by its path from
// the package depot, where every game\Data\*.snap is mounted alphabetically and the last archive with a path wins. So
// the archive holds the copy's own catalog with one entry appended per profile (a copy of the base asset's entry with
// the new name) and each profile's .bin (the base file with its role changed). The game's data can't ship, so the
// archive is made at setup time from the copy's own AssetData.snap. Every client and match-host copy gets it.
package botprofiles

import (
	"bytes"
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

// Profile is one profile: its BotClass name (without ".BotClass"), the shipped BotClass it copies and its role.
type Profile struct {
	Name, Base string
	Role       uint32
}

// Profiles is the table, in the archive's order (bot_profiles.py's PROFILES and the match host's
// gms/core/bots/profiles.h list the same).
var Profiles = []Profile{
	{Name: "PC_Warden_Predator", Base: "PC_Warden", Role: RolePredator},
}

type entry struct {
	fixed        []byte
	name, source string
	tags         []string
}

func cstr(d []byte, pos int) (string, int, error) {
	end := bytes.IndexByte(d[pos:], 0)
	if end < 0 {
		return "", 0, fmt.Errorf("botprofiles: catalog: unterminated string at %#x", pos)
	}
	return string(d[pos : pos+end]), pos + end + 1, nil
}

// parseCatalog walks the catalog as the client does (the entry block, then each entry's name, source and tags in
// member order) and requires the walk to end on the last byte.
func parseCatalog(d []byte) ([]entry, error) {
	le := binary.LittleEndian
	if len(d) < 8 {
		return nil, fmt.Errorf("botprofiles: catalog: too short")
	}
	ptr, count := le.Uint32(d), int(le.Uint32(d[4:]))
	if ptr == 0 || count == 0 {
		return nil, fmt.Errorf("botprofiles: catalog: no entries")
	}
	pos := 8 + count*entrySize
	if count > len(d)/entrySize || pos > len(d) {
		return nil, fmt.Errorf("botprofiles: catalog: the entry block exceeds the file")
	}
	out := make([]entry, count)
	var err error
	for i := range out {
		e := &out[i]
		e.fixed = d[8+i*entrySize : 8+(i+1)*entrySize]
		if le.Uint32(e.fixed) != 0 {
			if e.name, pos, err = cstr(d, pos); err != nil {
				return nil, err
			}
		}
		if le.Uint32(e.fixed[0x1C:]) != 0 {
			if e.source, pos, err = cstr(d, pos); err != nil {
				return nil, err
			}
		}
		if n := int(le.Uint32(e.fixed[0x28:])); n > 0 {
			if n > (len(d)-pos)/4 {
				return nil, fmt.Errorf("botprofiles: catalog: entry %d's tags exceed the file", i)
			}
			ptrs := d[pos : pos+4*n]
			pos += 4 * n
			for k := 0; k < n; k++ {
				if le.Uint32(ptrs[4*k:]) != 0 {
					var t string
					if t, pos, err = cstr(d, pos); err != nil {
						return nil, err
					}
					e.tags = append(e.tags, t)
				}
			}
		}
	}
	if pos != len(d) {
		return nil, fmt.Errorf("botprofiles: catalog: the walk ends at %#x, the file at %#x", pos, len(d))
	}
	return out, nil
}

// BuildCatalog is the catalog with one entry appended per addition {new name, base name} (full asset names, e.g.
// "PC_Warden_Predator.BotClass"): the base entry's fixed bytes and source, the new name.
func BuildCatalog(d []byte, additions [][2]string) ([]byte, error) {
	entries, err := parseCatalog(d)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	count := len(entries)
	byName := map[string]entry{}
	for _, e := range entries {
		if e.name != "" {
			byName[strings.ToLower(e.name)] = e
		}
	}
	head := append([]byte(nil), d[:8+count*entrySize]...)
	var tail []byte
	for _, a := range additions {
		name, base := a[0], a[1]
		if _, ok := byName[strings.ToLower(name)]; ok {
			return nil, fmt.Errorf("botprofiles: catalog: %s is already listed", name)
		}
		e, ok := byName[strings.ToLower(base)]
		if !ok {
			return nil, fmt.Errorf("botprofiles: catalog: %s is not listed", base)
		}
		if crc := le.Uint32(e.fixed[0x14:]); crc != BotClassTypeCRC {
			return nil, fmt.Errorf("botprofiles: catalog: %s has typeCrc %#08x, not BotClass's %#08x", base, crc, BotClassTypeCRC)
		}
		if e.source == "" {
			return nil, fmt.Errorf("botprofiles: catalog: %s has no source file", base)
		}
		if len(e.tags) > 0 || le.Uint32(e.fixed[0x28:]) != 0 {
			return nil, fmt.Errorf("botprofiles: catalog: %s has tags, which this writer does not copy", base)
		}
		head = append(head, e.fixed...)
		tail = append(append(append(append(tail, name...), 0), e.source...), 0)
		byName[strings.ToLower(name)] = entry{fixed: e.fixed, name: name, source: e.source}
	}
	le.PutUint32(head[4:], uint32(count+len(additions)))
	out := append(append(head, d[8+count*entrySize:]...), tail...)
	check, err := parseCatalog(out)
	if err != nil {
		return nil, err
	}
	for i, a := range additions {
		if check[count+i].name != a[0] {
			return nil, fmt.Errorf("botprofiles: catalog: the new entries do not read back")
		}
	}
	return out, nil
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

// Build is the archive's bytes, made from a copy's AssetData.snap (the whole file).
func Build(assetData []byte) ([]byte, error) {
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
	var adds [][2]string
	for _, p := range Profiles {
		adds = append(adds, [2]string{p.Name + ".BotClass", p.Base + ".BotClass"})
	}
	cat, err = BuildCatalog(cat, adds)
	if err != nil {
		return nil, err
	}
	files := []snap.File{{Name: Catalog, Data: cat}}
	for _, p := range Profiles {
		base, err := read("AssetData/Binary/" + p.Base + ".BotClass.bin")
		if err != nil {
			return nil, err
		}
		b, err := BuildBotClass(base, p.Role)
		if err != nil {
			return nil, err
		}
		files = append(files, snap.File{Name: "AssetData/Binary/" + p.Name + ".BotClass.bin", Data: b})
	}
	return snap.Pack(files), nil
}
