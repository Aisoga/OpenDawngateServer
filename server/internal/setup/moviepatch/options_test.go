package moviepatch

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

func TestOptionsTags(t *testing.T) {
	// DefineSprite 2000, DoABC "draggraboption", SymbolClass 2000 -> DragGrabOption, PlaceObject3 at depth 2000, no name
	sp := Options
	tags, err := tagsOf(sp.Tags, 0, len(sp.Tags))
	if err != nil || len(tags) != 4 || tags[3].end != len(sp.Tags) {
		t.Fatalf("options_tags.hex: %d tags, %v", len(tags), err)
	}
	for i, code := range []int{39, 82, 76, 70} {
		if tags[i].code != code {
			t.Errorf("tag %d: code %d, want %d", i, tags[i].code, code)
		}
	}
	if d := sp.Tags[tags[1].start:tags[1].end]; !bytes.HasPrefix(d, []byte("\x01\x00\x00\x00draggraboption\x00")) {
		t.Errorf("DoABC %x…: not lazy and named draggraboption", d[:16])
	}
	ps, err := pairs(sp.Tags, tags[2].start, tags[2].end)
	if err != nil || len(ps) != 1 || ps[0] != (pair{sp.CharID, sp.Class}) {
		t.Errorf("SymbolClass %+v, %v: want %d -> %s", ps, err, sp.CharID, sp.Class)
	}
	// The game's save stores every root child whose String() contains "CheckBox" as a check box (Options.as
	// _StartSaveAndClose casts it to CheckBox): our own class must never match, the box it makes must.
	if strings.Contains(sp.Class, "CheckBox") {
		t.Errorf("the class %q would be saved as a check box", sp.Class)
	}
	abc := sp.Tags[tags[1].start:tags[1].end]
	for _, s := range []string{"OptionDragGrab", "Options_GameSetCheckBox", "^Options!0x5e70f001", "Options.GetGameSettings"} {
		if !bytes.Contains(abc, []byte(s)) {
			t.Errorf("the class's ABC lacks %q", s)
		}
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
	if _, err := sp.PatchMovie(once); err == nil || !strings.Contains(err.Error(), "drag scroll check box already") {
		t.Errorf("twice: %v", err)
	}
}

// The player's own Options.snap of build 854 (skipped without the game files).
func TestBuildOptions854(t *testing.T) {
	raw, err := os.ReadFile(testgame.Data(t, "Options.snap"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Options.Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, err := snap.Parse(out)
	if err != nil || len(a.Entries) != 1 || a.Entries[0].Name != Options.MoviePath {
		t.Fatalf("the archive: %v, %+v", err, a)
	}
	movie, err := a.Read(a.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	s, err := scan(inflate(t, movie[8:]))
	if err != nil || !s.names["DragGrabOption"] || !s.names["Options_GameSettings_fla.MainTimeline"] ||
		!s.names["Options_GameSetCheckBox"] || !s.ids[2000] || !s.depths[2000] {
		t.Errorf("the patched movie: %v; %v", err, s.names)
	}
	again, _ := Options.Build(raw)
	if !bytes.Equal(again, out) {
		t.Errorf("Build isn't deterministic")
	}
	if p := os.Getenv("DAWNGATE_OPTIONS_OUT"); p != "" { // for a byte-for-byte comparison with the Python twin
		if err := os.WriteFile(p, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
