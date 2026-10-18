package moviepatch

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

func TestMatchOptionsTags(t *testing.T) {
	// DefineSprite 2000, DoABC "botcheckbox", SymbolClass 2000 -> BotCheckbox, PlaceObject3 at depth 2000, no name
	sp := MatchOptions
	tags, err := tagsOf(sp.Tags, 0, len(sp.Tags))
	if err != nil || len(tags) != 4 || tags[3].end != len(sp.Tags) {
		t.Fatalf("matchoptions_tags.hex: %d tags, %v", len(tags), err)
	}
	for i, code := range []int{39, 82, 76, 70} {
		if tags[i].code != code {
			t.Errorf("tag %d: code %d, want %d", i, tags[i].code, code)
		}
	}
	ps, err := pairs(sp.Tags, tags[2].start, tags[2].end)
	if err != nil || len(ps) != 1 || ps[0] != (pair{sp.CharID, sp.Class}) {
		t.Errorf("SymbolClass %+v, %v: want %d -> %s", ps, err, sp.CharID, sp.Class)
	}
	d := sp.Tags[tags[3].start:tags[3].end]
	if !bytes.Equal(d, cat([]byte{0x06, 0}, u16s(sp.Depth, sp.CharID), []byte{0})) {
		t.Errorf("PlaceObject3 %x: not character %d at depth %d without a name", d, sp.CharID, sp.Depth)
	}
	// the same splice as the picker's (whose bytes the Python twin pins); refused twice
	m := synth()
	withOurs := sp
	withOurs.Tags = []byte("OURTAGS")
	a, err1 := withOurs.PatchMovie(m)
	b, err2 := PatchMovie(m, []byte("OURTAGS"))
	if err1 != nil || err2 != nil || !bytes.Equal(a, b) {
		t.Errorf("the spec's patch differs from the picker's: %v, %v", err1, err2)
	}
	once, err := sp.PatchMovie(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.PatchMovie(once); err == nil || !strings.Contains(err.Error(), "the bot check box already") {
		t.Errorf("twice: %v", err)
	}
}

// The player's own Battlegrounds.snap of build 854 (skipped without the game files).
func TestBuildMatchOptions854(t *testing.T) {
	raw, err := os.ReadFile(testgame.Data(t, "Battlegrounds.snap"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := MatchOptions.Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, err := snap.Parse(out)
	if err != nil || len(a.Entries) != 1 || a.Entries[0].Name != MatchOptions.MoviePath {
		t.Fatalf("the archive: %v, %+v", err, a)
	}
	movie, err := a.Read(a.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	s, err := scan(inflate(t, movie[8:]))
	if err != nil || !s.names["BotCheckbox"] || !s.names["maxis.MatchOptionsUI"] || !s.ids[2000] || !s.depths[2000] {
		t.Errorf("the patched movie: %v; %v", err, s.names)
	}
	again, _ := MatchOptions.Build(raw)
	if !bytes.Equal(again, out) {
		t.Errorf("Build isn't deterministic")
	}
	if p := os.Getenv("DAWNGATE_MATCHOPTIONS_OUT"); p != "" { // for a byte-for-byte comparison with the Python twin
		if err := os.WriteFile(p, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
