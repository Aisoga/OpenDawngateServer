package botprofiles

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"dawngate/server/internal/setup/assetdata"
)

// The copy's one catalog. The client loads only AssetData/Binary/catalog_153.bin, and of the archives that hold it the
// alphabetically last wins, so every asset a copy adds (the bot profiles, every installed map's Level, Markersets and
// tuning) must be listed in this archive's catalog: an added map's archive (zz_CM_<Name>.snap, which sorts after this
// one) carries its .bin files but no catalog, or it would hide the bot profiles'.

// CatalogEntry is one asset a source adds to the catalog.
type CatalogEntry struct {
	Name string // the asset's full name, "CM_Ring.Level" (the class is what follows the first dot)
	// Base is a listed asset of the same class whose entry this one copies (its fixed bytes: compile time, typeCrc,
	// location and status; and its source file unless Source is set), or "" for an entry written anew: the class's
	// typeCrc from the client's tables, the catalog's newest compile time, location and status 4 (on disk, loaded).
	Base   string
	Source string   // the source file name; "" = the base's, or the asset's own name for a new entry
	Tags   []string // a new entry's tags (an entry copied from a base takes none)
}

// CatalogSource is one contributor's entries: the bot profiles or one installed map.
type CatalogSource struct {
	Name    string
	Entries []CatalogEntry
}

// ProfilesSource is the name of the bot profiles' source.
const ProfilesSource = "bot profiles"

// ProfileEntries are the bot profiles' catalog entries: each profile copies its base's entry.
func ProfileEntries() CatalogSource {
	s := CatalogSource{Name: ProfilesSource}
	for _, p := range Profiles {
		s.Entries = append(s.Entries, CatalogEntry{Name: p.Name + ".BotClass", Base: p.Base + ".BotClass"})
	}
	return s
}

// SortSources puts the bot profiles first and the other sources after them by name (case-insensitively, then byte
// order), so the catalog doesn't depend on the order the maps were found in.
func SortSources(srcs []CatalogSource) []CatalogSource {
	out := append([]CatalogSource(nil), srcs...)
	rank := func(s CatalogSource) int {
		if s.Name == ProfilesSource {
			return 0
		}
		return 1
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra < rb
		}
		if la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name); la != lb {
			return la < lb
		}
		return a.Name < b.Name
	})
	return out
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

// classOf is an asset name's class, what follows its first dot (the client's rule).
func classOf(name string) string {
	_, c, _ := strings.Cut(name, ".")
	return c
}

// BuildCatalog is the catalog with one entry appended per addition {new name, base name} (full asset names, e.g.
// "PC_Warden_Predator.BotClass"): the base entry's fixed bytes and source, the new name.
func BuildCatalog(d []byte, additions [][2]string) ([]byte, error) {
	s := CatalogSource{Name: ProfilesSource}
	for _, a := range additions {
		s.Entries = append(s.Entries, CatalogEntry{Name: a[0], Base: a[1]})
	}
	return MergeCatalog(d, []CatalogSource{s})
}

// MergeCatalog is the catalog d (the copy's own) with every source's entries appended, source by source in the order
// given (SortSources gives the copy's order), each source's entries in its order. The copy's own entries stay byte for
// byte; each added entry's text follows all of theirs. A name listed twice (in the copy's catalog or by two sources,
// in any case) is refused, as are two sources of the same name, a base that isn't listed or is of another class, and a
// class whose typeCrc the client's tables don't give.
func MergeCatalog(d []byte, sources []CatalogSource) ([]byte, error) {
	entries, err := parseCatalog(d)
	if err != nil {
		return nil, err
	}
	ts, err := assetdata.Embedded()
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	count := len(entries)
	byName := map[string]entry{}
	var newest int64
	for _, e := range entries {
		if e.name != "" {
			byName[strings.ToLower(e.name)] = e
		}
		newest = max(newest, int64(le.Uint64(e.fixed[0x08:])))
	}
	typeCRC := func(name string) (uint32, error) {
		t := ts.Type(classOf(name))
		if classOf(name) == "" || t == nil || !t.HasCRC {
			return 0, fmt.Errorf("botprofiles: catalog: %s: no class the client knows", name)
		}
		return t.CRC, nil
	}
	head := append([]byte(nil), d[:8+count*entrySize]...)
	var tail []byte
	var added []string
	seenSrc := map[string]bool{}
	for _, src := range sources {
		if seenSrc[strings.ToLower(src.Name)] {
			return nil, fmt.Errorf("botprofiles: catalog: two sources named %q", src.Name)
		}
		seenSrc[strings.ToLower(src.Name)] = true
		for _, a := range src.Entries {
			if _, ok := byName[strings.ToLower(a.Name)]; ok {
				return nil, fmt.Errorf("botprofiles: catalog: %s (%s) is already listed", a.Name, src.Name)
			}
			crc, err := typeCRC(a.Name)
			if err != nil {
				return nil, err
			}
			var e addedEntry
			if a.Base != "" {
				e, err = copiedEntry(byName, a, crc)
			} else {
				e, err = newEntry(ts, a, crc, newest)
			}
			if err != nil {
				return nil, err
			}
			head = append(head, e.fixed...)
			tail = append(tail, e.data...)
			byName[strings.ToLower(a.Name)] = e.entry
			added = append(added, a.Name)
		}
	}
	le.PutUint32(head[4:], uint32(count+len(added)))
	out := append(append(head, d[8+count*entrySize:]...), tail...)
	check, err := parseCatalog(out)
	if err != nil {
		return nil, err
	}
	for i, name := range added {
		if check[count+i].name != name {
			return nil, fmt.Errorf("botprofiles: catalog: the new entries do not read back")
		}
	}
	return out, nil
}

// addedEntry is an entry to append: its fixed bytes and its out-of-line data (name, source, tags).
type addedEntry struct {
	entry
	data []byte
}

// copiedEntry copies a base entry's fixed bytes and source under a new name.
func copiedEntry(byName map[string]entry, a CatalogEntry, crc uint32) (addedEntry, error) {
	le := binary.LittleEndian
	b, ok := byName[strings.ToLower(a.Base)]
	if !ok {
		return addedEntry{}, fmt.Errorf("botprofiles: catalog: %s is not listed", a.Base)
	}
	if !strings.EqualFold(classOf(b.name), classOf(a.Name)) {
		return addedEntry{}, fmt.Errorf("botprofiles: catalog: %s is not of %s's class", a.Base, a.Name)
	}
	if got := le.Uint32(b.fixed[0x14:]); got != crc {
		return addedEntry{}, fmt.Errorf("botprofiles: catalog: %s has typeCrc %#08x, not its class's %#08x", a.Base, got, crc)
	}
	if len(b.tags) > 0 || le.Uint32(b.fixed[0x28:]) != 0 || len(a.Tags) > 0 {
		return addedEntry{}, fmt.Errorf("botprofiles: catalog: %s: an entry copied from %s takes no tags", a.Name, a.Base)
	}
	source := a.Source
	if source == "" {
		source = b.source
	}
	if source == "" {
		return addedEntry{}, fmt.Errorf("botprofiles: catalog: %s has no source file", a.Base)
	}
	if a.Name == "" || strings.IndexByte(a.Name+source, 0) >= 0 {
		return addedEntry{}, fmt.Errorf("botprofiles: catalog: bad name %q or source %q", a.Name, source)
	}
	fixed := append([]byte(nil), b.fixed...)
	if le.Uint32(fixed[0x1C:]) == 0 {
		le.PutUint32(fixed[0x1C:], assetdata.DefaultPointerBase)
	}
	data := append(append(append(append([]byte(nil), a.Name...), 0), source...), 0)
	return addedEntry{entry{fixed: fixed, name: a.Name, source: source}, data}, nil
}

// newEntry writes an entry anew with the client's CatalogEntry layout.
func newEntry(ts *assetdata.Types, a CatalogEntry, crc uint32, compileTime int64) (addedEntry, error) {
	source := a.Source
	if source == "" {
		source = a.Name
	}
	tags := []any{}
	for _, t := range a.Tags {
		tags = append(tags, t)
	}
	v := assetdata.NewObject()
	v.Set("assetNameType", a.Name)
	v.Set("compileTime", compileTime)
	v.Set("typeCrc", uint64(crc))
	v.Set("sourceFileNameType", source)
	v.Set("location", uint64(4))
	v.Set("status", uint64(4))
	v.Set("tags", tags)
	b, err := ts.Write(ts.Type("CatalogEntry"), v, assetdata.WriteOptions{})
	if err != nil {
		return addedEntry{}, fmt.Errorf("botprofiles: catalog: %s: %w", a.Name, err)
	}
	if len(b) < entrySize {
		return addedEntry{}, fmt.Errorf("botprofiles: catalog: CatalogEntry is not %#x bytes", entrySize)
	}
	return addedEntry{entry{fixed: b[:entrySize], name: a.Name, source: source, tags: a.Tags}, b[entrySize:]}, nil
}
