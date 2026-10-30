package moviepatch

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/gamecontent"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

// The made-up content (server/testdata/collection/content.json) and what both twins make of it: its item list, and a
// made-up code.snap (the synth movie) with the tags "OURTAGS" through the Collection spec. re/scripts/test_picker.py
// pins the same hashes (COLLECTION_LIST_SHA, COLLECTION_SYNTH_SHA).
const (
	collectionList  = "60ed944ce678df5c0caea1bf3ff28d8f8f6ebce6f392082d3158fd1c78a5f9ea"
	collectionSynth = "7cd32e2fbe63535ccd17a171b07a8b48227f2bbf3eab90e9cd8762fc5969dc5d"
)

func madeUpContent(t *testing.T) *content.Content {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "collection", "content.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c content.Content
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

func TestItemList(t *testing.T) {
	c := madeUpContent(t)
	got := string(ItemList(c, false))
	// by kind (basic, advanced, legendary, consumable, spell, then any other), cost, title in bytes; no cheat items
	// (the flag, Consumable_Debug_*, a name with "cheat"), no row with a tab; no title: the name; icons with slashes.
	// The filter tags first (none with a space; tabs in titles as spaces), each item's details after it: stat lines
	// (without parts the attribute's name), passives and actives as runs (a bad colour plain, a line end the pair
	// "n", ""), the cooldown rounded half up, from/into only to listed items (not the cheat item Zeta), the tags.
	// The group (COLLITEMS10): a spell's own (Blink: Utility, whatever its tags); any other item's the filter groups
	// holding one of its tags, in the filters' order (Beta's MaxHealth, Power: "Offense / Defense"), else empty
	// (content's group ignored: Gamma, Potion; a tag of no filter: Delta's Aura; a spaced tag is no tag).
	want := "dawngate-collitems\t2\n" +
		"filter\tOffense\tPower\tPower\n" +
		"filter\tOffense\tLifeDrain\tLifeDrain\n" +
		"filter\tDefense\tMaxHealth\tHealth Points\n" +
		"item\tItem_Gamma\tGamma\tbasic\t350\t\tUI/Icons/ShopIcons/textures/gamma.dds\n" +
		"item\tItem_Alpha\tAlpha\tbasic\t400\tOffense\tUI/Icons/ShopIcons/textures/alpha.dds\n" +
		"stat\t#55FF55\t+20\t\t Power\n" +
		"stat\t\tLifeDrain\n" +
		"passive\tunique\tSpark\t\tHits \t#ffff99\tburn\t\t.\tn\t\t\tTwice over.\n" +
		"into\tItem_Beta\n" +
		"tags\tPower LifeDrain OffensiveEffect\n" +
		"item\tItem_Delta\tDelta\tbasic\t400\t\tUI/Icons/ShopIcons/textures/delta.dds\n" +
		"tags\tAura\n" +
		"item\tItem_Eta\tÉta\tbasic\t400\t\t\n" +
		"item\tItem_Beta\tBeta\tadvanced\t1200\tOffense / Defense\tUI/Icons/ShopIcons/textures/beta.dds\n" +
		"passive\tstackable\t\t\tPlain text only.\n" +
		"passive\t\t\n" +
		"from\tItem_Alpha\n" +
		"into\tItem_Epsilon\n" +
		"tags\tMaxHealth Power\n" +
		"item\tItem_Epsilon\tItem_Epsilon\tlegendary\t3100\t\tUI/Icons/ShopIcons/textures/epsilon.dds\n" +
		"from\tItem_Beta\n" +
		"item\tConsumable_Potion\tPotion\tconsumable\t50\t\tUI/Icons/ShopIcons/textures/potion.dds\n" +
		"active\tunique\t\t3\t\tHeals 150 Health.\n" +
		"item\tSpell_Blink\tBlink\tspell\t0\tUtility\tUI/Icons/ShopIcons/textures/blink.dds\n" +
		"active\t\tFlash Step\t0\t#FFFFFF\tTeleports\t\t.\n" +
		"tags\tPower\n" +
		"item\tItem_Kappa\tKappa\tmythic\t10\t\t\n" +
		"end\n"
	if got != want {
		t.Errorf("the item list:\n%s\nwant:\n%s", got, want)
	}
	checkGroups(t, []byte(got), false)
	if s := sum([]byte(got)); s != collectionList {
		t.Errorf("the item list's SHA-256 %s; patch_login_movie.py's: %s", s, collectionList)
	}
	if d := string(ItemList(c, true)); d != strings.Replace(want, "\n", "\ndebug\t1\n", 1) {
		t.Errorf("with debug:\n%s", d)
	}
	if e := string(ItemList(&content.Content{}, false)); e != "dawngate-collitems\t2\nend\n" {
		t.Errorf("no items: %q", e)
	}
}

// checkGroups reads an item list as the class does and checks each item's group against the shop's filters
// (COLLITEMS10): a spell keeps its own (one of Offense, Utility, Defense); any other item's group is the filter groups
// whose tags its tags line holds, in the filter lines' order, joined by " / ". The real game (real) also needs items of
// every group, of all three and of none.
func checkGroups(t *testing.T, list []byte, real bool) {
	t.Helper()
	var order []string
	byTag := map[string]string{}
	type entry struct{ name, kind, group, tags string }
	var items []*entry
	for _, l := range strings.Split(string(list), "\n") {
		f := strings.Split(l, "\t")
		switch f[0] {
		case "filter":
			if !slices.Contains(order, f[1]) {
				order = append(order, f[1])
			}
			byTag[f[2]] = f[1]
		case "item":
			items = append(items, &entry{name: f[1], kind: f[3], group: f[5]})
		case "tags":
			items[len(items)-1].tags = f[1]
		}
	}
	seen := map[string]int{}
	for _, it := range items {
		seen[it.group]++
		if it.kind == "spell" {
			if !slices.Contains([]string{"Offense", "Utility", "Defense"}, it.group) {
				t.Errorf("spell %s: group %q", it.name, it.group)
			}
			continue
		}
		in := map[string]bool{}
		for _, tag := range strings.Fields(it.tags) {
			if g, ok := byTag[tag]; ok {
				in[g] = true
			}
		}
		var want []string
		for _, g := range order {
			if in[g] {
				want = append(want, g)
			}
		}
		if w := strings.Join(want, " / "); it.group != w {
			t.Errorf("%s (%s, tags %q): group %q, the shop's filters say %q", it.name, it.kind, it.tags, it.group, w)
		}
	}
	if !real {
		return
	}
	for _, g := range []string{"Offense", "Utility", "Defense", "Offense / Utility / Defense", ""} {
		if seen[g] == 0 {
			t.Errorf("no item of group %q (groups: %v)", g, seen)
		}
	}
	t.Logf("groups: %v", seen)
}

func TestCollectionTags(t *testing.T) {
	// DefineSprite 2000, DoABC "collectionitems", SymbolClass 2000 -> CollectionItems, PlaceObject3 at depth 2000
	sp := Collection
	tags, err := tagsOf(sp.Tags, 0, len(sp.Tags))
	if err != nil || len(tags) != 4 || tags[3].end != len(sp.Tags) {
		t.Fatalf("collection_tags.hex: %d tags, %v", len(tags), err)
	}
	for i, code := range []int{39, 82, 76, 70} {
		if tags[i].code != code {
			t.Errorf("tag %d: code %d, want %d", i, tags[i].code, code)
		}
	}
	if d := sp.Tags[tags[1].start:tags[1].end]; !bytes.HasPrefix(d, []byte("\x01\x00\x00\x00collectionitems\x00")) {
		t.Errorf("DoABC %x…: not lazy and named collectionitems", d[:16])
	}
	if !bytes.Contains(sp.Tags, []byte("Collection/dgitems.json")) || !bytes.Contains(sp.Tags, []byte("dawngate-collitems\t2")) {
		t.Error("the class doesn't read its item list, Collection/dgitems.json")
	}
	ps, err := pairs(sp.Tags, tags[2].start, tags[2].end)
	if err != nil || len(ps) != 1 || ps[0] != (pair{sp.CharID, sp.Class}) {
		t.Errorf("SymbolClass %+v, %v: want %d -> %s", ps, err, sp.CharID, sp.Class)
	}
	d := sp.Tags[tags[3].start:tags[3].end]
	if !bytes.Equal(d, cat([]byte{0x06, 0}, u16s(sp.Depth, sp.CharID), []byte{0})) {
		t.Errorf("PlaceObject3 %x: not character %d at depth %d without a name", d, sp.CharID, sp.Depth)
	}
	// without an item list the archive isn't made; with one it holds the movie and the list
	m := synth()
	source := snap.Pack([]snap.File{{Name: sp.MoviePath, Data: m}})
	if _, err := sp.Build(source); err == nil || !strings.Contains(err.Error(), "no item list") {
		t.Errorf("without an item list: %v", err)
	}
	withOurs := sp
	withOurs.Tags = []byte("OURTAGS")
	withOurs.ItemList = ItemList(madeUpContent(t), false)
	out, err := withOurs.Build(source)
	if err != nil {
		t.Fatal(err)
	}
	arc, err := snap.Parse(out)
	if err != nil || len(arc.Entries) != 2 || arc.Entries[0].Name != sp.MoviePath || arc.Entries[1].Name != ItemsPath {
		t.Fatalf("the archive: %v, %+v", err, arc)
	}
	if b, err := arc.Read(arc.Entries[1]); err != nil || !bytes.Equal(b, withOurs.ItemList) {
		t.Errorf("the item list in the archive %q, %v", b, err)
	}
	if got := sum(out); got != collectionSynth {
		t.Errorf("a made-up Collection archive: %d bytes, %s; patch_login_movie.py's: %s", len(out), got, collectionSynth)
	}
}

// The player's own code.snap of build 854 with the item list of its own game data (skipped without the game files).
func TestBuildCollection854(t *testing.T) {
	raw, err := os.ReadFile(testgame.Data(t, "code.snap"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := gamecontent.Build(testgame.Dir(t))
	if err != nil {
		t.Fatal(err)
	}
	sp := Collection
	sp.ItemList = ItemList(c, false)
	if n := bytes.Count(sp.ItemList, []byte("\nitem\t")); n < 100 {
		t.Errorf("the item list holds %d items", n)
	}
	checkGroups(t, sp.ItemList, true)
	out, err := sp.Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, err := snap.Parse(out)
	if err != nil || len(a.Entries) != 2 || a.Entries[0].Name != sp.MoviePath || a.Entries[1].Name != ItemsPath {
		t.Fatalf("the archive: %v, %+v", err, a)
	}
	movie, err := a.Read(a.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	s, err := scan(inflate(t, movie[8:]))
	if err != nil || !s.names["CollectionItems"] || !s.ids[2000] || !s.depths[2000] {
		t.Errorf("the patched movie: %v", err)
	}
	// for a byte-for-byte comparison with the Python twin: the archive, and beside it the content it was made from
	// (<out>.content.json, what the setup's content step writes), which the twin reads
	if p := os.Getenv("DAWNGATE_COLLECTION_OUT"); p != "" {
		js, err := c.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, out, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p+".content.json", js, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
