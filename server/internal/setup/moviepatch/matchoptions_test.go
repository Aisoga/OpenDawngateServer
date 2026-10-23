package moviepatch

import (
	"bytes"
	"os"
	"path/filepath"
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
	// the movie, then the map list (no map besides The Dawngate: Build)
	if err != nil || len(a.Entries) != 2 || a.Entries[0].Name != MatchOptions.MoviePath || a.Entries[1].Name != MapListPath {
		t.Fatalf("the archive: %v, %+v", err, a)
	}
	if list, err := a.Read(a.Entries[1]); err != nil || len(list) != MapListSize ||
		strings.TrimRight(string(list), "\n") != "dawngate-maps\t1\nend" || a.Entries[1].Stored() {
		t.Errorf("the map list %q, %v", strings.TrimRight(string(list), "\n"), err)
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

// The create dialog's map list: its bytes, the maps a copy has, and the archive with it (test_picker.py pins the same).
func TestMatchOptionsMapList(t *testing.T) {
	// the text padded with line ends to the fixed size (the class reads up to "end")
	pad := func(text string) string { return text + strings.Repeat("\n", MapListSize-len(text)) }
	if got, want := string(MapList(nil)), pad("dawngate-maps\t1\nend\n"); got != want {
		t.Errorf("MapList(nil) = %q", strings.TrimRight(got, "\n"))
	}
	if got, want := string(MapList(ExtraMaps)), pad("dawngate-maps\t1\nmap\tmm\tMM (one lane)\nend\n"); got != want {
		t.Errorf("MapList(ExtraMaps) = %q", strings.TrimRight(got, "\n"))
	}
	many := make([]Map, 100) // more than fit: cut at a whole line, still the fixed size
	for i := range many {
		many[i] = Map{Tag: "t", Label: "a label of some twenty chars"}
	}
	if got := MapList(many); len(got) != MapListSize || !strings.Contains(string(got), "\nend\n") {
		t.Errorf("MapList(100 maps): %d bytes", len(got))
	}
	dir := t.TempDir()
	if got := CopyMaps(dir); len(got) != 0 {
		t.Errorf("an empty folder has the maps %v", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "zz_MM_Map_v3.snap"), 0o755); err != nil { // a folder isn't the archive
		t.Fatal(err)
	}
	if got := CopyMaps(dir); len(got) != 0 {
		t.Errorf("a folder named as the archive counts: %v", got)
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "zz_MM_Map_v3.snap"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CopyMaps(other); len(got) != 1 || got[0] != ExtraMaps[0] {
		t.Errorf("CopyMaps = %v, want MM", got)
	}
	// a made-up Battlegrounds.snap with the synth movie at the dialog's path, made-up tags
	src := snap.Pack([]snap.File{{Name: MatchOptions.MoviePath, Data: synth()}})
	sp := MatchOptions
	sp.Tags = []byte("OURTAGS")
	for _, c := range []struct {
		maps []Map
		want string
	}{
		{nil, "e4994e8df80b86886df2a24a92f90ab698bd91a572be6e0a66cff9ab3a881471"},
		{ExtraMaps, "ea32a187414f66c510cea4473c00b166be6db1188d0710b61e6622c43b3e5e9b"},
	} {
		out, err := sp.BuildMaps(src, c.maps)
		if err != nil {
			t.Fatal(err)
		}
		a, err := snap.Parse(out)
		if err != nil || len(a.Entries) != 2 || a.Entries[0].Name != MatchOptions.MoviePath || a.Entries[1].Name != MapListPath {
			t.Fatalf("the archive: %v, %+v", err, a)
		}
		if list, err := a.Read(a.Entries[1]); err != nil || !bytes.Equal(list, MapList(c.maps)) {
			t.Errorf("the archive's map list %q, %v", list, err)
		}
		if got := sum(out); got != c.want {
			t.Errorf("BuildMaps(%d maps): %d bytes, %s; patch_login_movie.py's: %s", len(c.maps), len(out), got, c.want)
		}
	}
	plain, _ := sp.Build(src)
	if none, _ := sp.BuildMaps(src, nil); !bytes.Equal(plain, none) {
		t.Error("Build isn't BuildMaps without maps")
	}
	// the other specs have no list
	cr := ChatRooms
	cr.Tags = []byte("OURTAGS")
	out, err := cr.BuildMaps(snap.Pack([]snap.File{{Name: ChatRooms.MoviePath, Data: synth()}}), ExtraMaps)
	if err != nil {
		t.Fatal(err)
	}
	if a, err := snap.Parse(out); err != nil || len(a.Entries) != 1 {
		t.Errorf("the chat rooms archive: %v, %+v", err, a)
	}
}
