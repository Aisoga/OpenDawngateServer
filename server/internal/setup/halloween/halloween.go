// Package halloween makes a copy's added archive game\Data\zz_Halloween.snap: the game's Halloween decoration for its
// map, switched by the server per match. Byte for byte what re/scripts/halloween.py makes (that script's docstring has
// the evidence).
//
// The game ships the markerset SM_Map_v5_holiday_treatment (94 markers: candles, pumpkins, jack-o'-lanterns, a tomb,
// lights and decals; every model ships), which SM_Map_v5.Level doesn't list, so it never loads. The client's level
// loader loads a markerset whose condition list names value c only when bit c of the level flags is set, and the level
// flags come from the match host (GamePrepareForStart). No shipped markerset has a condition and nothing else reads the
// level flags, so the archive lists the holiday markerset in the level (appended last) with the condition
// hordeLevelsOnly (0): a client with the archive shows the decoration exactly when its server sends level flags with
// LevelFlag set. Both assets are in the catalog already; the client reads each .bin by its path from the package depot,
// where the alphabetically last archive with a path wins. The game's data can't ship, so the archive is made at setup
// time from the copy's own AssetData.snap. Every client copy gets it; the match host doesn't need it (the markers are
// decoration only: client-only nouns and static props without physics or navigation, like the map's own).
package halloween

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"

	"dawngate/server/internal/setup/snap"
)

// The archive's name and place, the file it is made from, and the two assets it replaces.
const (
	File      = "zz_Halloween.snap"
	Rel       = "game/Data/" + File        // in a copy
	Source    = "game/Data/AssetData.snap" // the copy's own asset data
	Level     = "SM_Map_v5.Level"
	Markerset = "SM_Map_v5_holiday_treatment.Markerset"
)

// Condition is the markerset condition the archive gives the decoration (the enum's hordeLevelsOnly, a Darkspore name),
// LevelFlag the level flags' bit that turns it on: what the match host sends in GamePrepareForStart for a match with
// the decoration.
const (
	Condition = 0
	LevelFlag = 1 << Condition
)

// The layouts (the client's reflection tables): a Level's fixed part is 0x620 bytes with the markersets array
// {pointer, count} at +0x00; a Markerset's 0x28 bytes with markers {pointer, count} at +0x00, the group Key's text
// pointer at +0x14 and condition {pointer, count} at +0x20, its last member.
const (
	levelSize     = 0x620
	markersetSize = 0x28
)

func binPath(asset string) string { return "AssetData/Binary/" + asset + ".bin" }

// MarkersetNames are the markerset names a Level file lists.
func MarkersetNames(level []byte) ([]string, error) {
	if len(level) < levelSize+4 {
		return nil, fmt.Errorf("halloween: a Level file has at least %#x bytes, this one %d", levelSize+4, len(level))
	}
	le := binary.LittleEndian
	ptr, count := le.Uint32(level[0:]), le.Uint32(level[4:])
	if ptr == 0 || count == 0 || count > 1000 {
		return nil, fmt.Errorf("halloween: the level's markerset list is unexpected (pointer %#x, count %d)", ptr, count)
	}
	pos := levelSize + 4*int(count)
	if pos > len(level) {
		return nil, fmt.Errorf("halloween: the level's markerset list is cut")
	}
	names := make([]string, 0, count)
	for i := 0; i < int(count); i++ {
		end := bytes.IndexByte(level[pos:], 0)
		if end < 0 {
			return nil, fmt.Errorf("halloween: unterminated string at %#x", pos)
		}
		names = append(names, string(level[pos:pos+end]))
		pos += end + 1
	}
	return names, nil
}

// AddMarkerset is the Level file with the markerset name ("X.Markerset") appended to its list: one more pointer word
// (the last one's value; non-zero means present) after the last word, the name after the last name.
func AddMarkerset(level []byte, name string) ([]byte, error) {
	names, err := MarkersetNames(level)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		if strings.EqualFold(n, name) {
			return nil, fmt.Errorf("halloween: the level lists %s already", name)
		}
	}
	wordsEnd := levelSize + 4*len(names)
	pos := wordsEnd
	for _, n := range names {
		pos += len(n) + 1
	}
	out := make([]byte, 0, len(level)+4+len(name)+1)
	out = append(out, level[:wordsEnd]...)
	out = append(out, level[wordsEnd-4:wordsEnd]...)
	out = append(out, level[wordsEnd:pos]...)
	out = append(append(out, name...), 0)
	out = append(out, level[pos:]...)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(names)+1))
	return out, nil
}

// SetCondition is the Markerset file with the condition list values instead of its empty one: the count set, the
// pointer word set to the markers array's (non-zero), the values appended (condition is the last member, so its
// elements are the file's last bytes).
func SetCondition(markerset []byte, values []uint32) ([]byte, error) {
	if len(markerset) < markersetSize {
		return nil, fmt.Errorf("halloween: a Markerset file has at least %#x bytes, this one %d", markersetSize, len(markerset))
	}
	le := binary.LittleEndian
	markers, keyText := le.Uint32(markerset[0x00:]), le.Uint32(markerset[0x14:])
	if cptr, ccount := le.Uint32(markerset[0x20:]), le.Uint32(markerset[0x24:]); cptr != 0 || ccount != 0 {
		return nil, fmt.Errorf("halloween: the markerset has a condition list already (pointer %#x, count %d)", cptr, ccount)
	}
	if markers == 0 || keyText == 0 || markerset[len(markerset)-1] != 0 {
		return nil, fmt.Errorf("halloween: the markerset doesn't end with its group's text (no markers, or no group text)")
	}
	out := append([]byte(nil), markerset...)
	le.PutUint32(out[0x20:], markers)
	le.PutUint32(out[0x24:], uint32(len(values)))
	for _, v := range values {
		out = le.AppendUint32(out, v)
	}
	return out, nil
}

// Build is the archive's bytes, made from a copy's AssetData.snap (the whole file): the level first, then the
// markerset.
func Build(assetData []byte) ([]byte, error) {
	a, err := snap.Parse(assetData)
	if err != nil {
		return nil, err
	}
	byName := map[string]snap.Entry{}
	for _, e := range a.Entries {
		byName[strings.ToLower(e.Name)] = e
	}
	read := func(asset string) ([]byte, error) {
		e, ok := byName[strings.ToLower(binPath(asset))]
		if !ok {
			return nil, fmt.Errorf("halloween: AssetData.snap has no %s", binPath(asset))
		}
		return a.Read(e)
	}
	level, err := read(Level)
	if err != nil {
		return nil, err
	}
	if level, err = AddMarkerset(level, Markerset); err != nil {
		return nil, err
	}
	ms, err := read(Markerset)
	if err != nil {
		return nil, err
	}
	if ms, err = SetCondition(ms, []uint32{Condition}); err != nil {
		return nil, err
	}
	return snap.Pack([]snap.File{{Name: binPath(Level), Data: level}, {Name: binPath(Markerset), Data: ms}}), nil
}
