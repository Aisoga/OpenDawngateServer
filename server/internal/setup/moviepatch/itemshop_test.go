package moviepatch

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

// itemShopSynth is a made-up HUD.snap (the synth movie) built with the tags "OURTAGS" through the ItemShop spec; the
// bytes re/scripts/test_picker.py pins too (ITEMSHOP_SYNTH_SHA).
const itemShopSynth = "5094c226d16433e5616eaede8b29ea86fb503aa34bb508d9b2a017b9ccb80009"

func TestItemShopTags(t *testing.T) {
	// DefineSprite 2000, DoABC "shoprefund", SymbolClass 2000 -> ShopRefund, PlaceObject3 at depth 2000, no name
	sp := ItemShop
	tags, err := tagsOf(sp.Tags, 0, len(sp.Tags))
	if err != nil || len(tags) != 4 || tags[3].end != len(sp.Tags) {
		t.Fatalf("itemshop_tags.hex: %d tags, %v", len(tags), err)
	}
	for i, code := range []int{39, 82, 76, 70} {
		if tags[i].code != code {
			t.Errorf("tag %d: code %d, want %d", i, tags[i].code, code)
		}
	}
	if d := sp.Tags[tags[1].start:tags[1].end]; !bytes.HasPrefix(d, []byte("\x01\x00\x00\x00shoprefund\x00")) {
		t.Errorf("DoABC %x…: not lazy and named shoprefund", d[:16])
	}
	if !bytes.Contains(sp.Tags, []byte("HUD/dgrefund.json")) || !bytes.Contains(sp.Tags, []byte("dawngate-refund\t1")) {
		t.Error("the class doesn't read its optional settings, HUD/dgrefund.json")
	}
	ps, err := pairs(sp.Tags, tags[2].start, tags[2].end)
	// the class, and the shop's own vim symbol (character 250, placed as mcCoins) bound to ShopVim: the badge
	if err != nil || len(ps) != 2 || ps[0] != (pair{sp.CharID, sp.Class}) || ps[1] != (pair{250, "ShopVim"}) {
		t.Errorf("SymbolClass %+v, %v: want %d -> %s, 250 -> ShopVim", ps, err, sp.CharID, sp.Class)
	}
	d := sp.Tags[tags[3].start:tags[3].end]
	if !bytes.Equal(d, cat([]byte{0x06, 0}, u16s(sp.Depth, sp.CharID), []byte{0})) {
		t.Errorf("PlaceObject3 %x: not character %d at depth %d without a name", d, sp.CharID, sp.Depth)
	}
	// the same splice as the picker's; refused twice; the archive holds the movie only
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
	if _, err := sp.PatchMovie(once); err == nil || !strings.Contains(err.Error(), "the shop's inline refund already") {
		t.Errorf("twice: %v", err)
	}
	out, err := withOurs.Build(snap.Pack([]snap.File{{Name: sp.MoviePath, Data: m}}))
	if err != nil {
		t.Fatal(err)
	}
	arc, err := snap.Parse(out)
	if err != nil || len(arc.Entries) != 2 || arc.Entries[0].Name != sp.MoviePath || arc.Entries[1].Name != RefundSettingsPath {
		t.Fatalf("the archive: %v, %+v", err, arc)
	}
	// the default settings: a normal install must give the class its file (else FileDepot.cpp 138 at each match)
	if b, err := arc.Read(arc.Entries[1]); err != nil || string(b) != "dawngate-refund\t1\nend\n" {
		t.Errorf("the default settings %q, %v", b, err)
	}
	if got := sum(out); got != itemShopSynth {
		t.Errorf("a made-up shop archive: %d bytes, %s; patch_login_movie.py's: %s", len(out), got, itemShopSynth)
	}
}

// The player's own HUD.snap of build 854 (skipped without the game files).
func TestBuildItemShop854(t *testing.T) {
	raw, err := os.ReadFile(testgame.Data(t, "HUD.snap"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := ItemShop.Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, err := snap.Parse(out)
	if err != nil || len(a.Entries) != 2 || a.Entries[0].Name != ItemShop.MoviePath || a.Entries[1].Name != RefundSettingsPath {
		t.Fatalf("the archive: %v, %+v", err, a)
	}
	movie, err := a.Read(a.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	s, err := scan(inflate(t, movie[8:]))
	if err != nil || !s.names["ShopRefund"] || !s.names["ShopVim"] || !s.names["maxis.Hud.ItemShopUI"] ||
		!s.ids[250] || !s.ids[2000] || !s.depths[2000] {
		t.Errorf("the patched movie: %v; %v", err, s.names)
	}
	again, _ := ItemShop.Build(raw)
	if !bytes.Equal(again, out) {
		t.Errorf("Build isn't deterministic")
	}
	if p := os.Getenv("DAWNGATE_ITEMSHOP_OUT"); p != "" { // for a byte-for-byte comparison with the Python twin
		if err := os.WriteFile(p, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
