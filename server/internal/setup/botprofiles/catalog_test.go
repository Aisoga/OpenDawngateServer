package botprofiles

import (
	"bytes"
	"strings"
	"testing"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

// Two made-up maps' catalog entries: new Level, Markerset and tuning entries (one with tags) and one copied from a
// listed BotClass under its own source file.
func madeUpMaps() (grove, ring CatalogSource) {
	grove = CatalogSource{Name: "CM_Grove", Entries: []CatalogEntry{
		{Name: "CM_Grove.Level"},
		{Name: "CM_Grove_Gameplay.Markerset"},
		{Name: "CM_Grove.LevelGameplayTuning", Source: "Maps/CM_Grove.LevelGameplayTuning", Tags: []string{"placeable", "npc"}},
	}}
	ring = CatalogSource{Name: "CM_Ring", Entries: []CatalogEntry{
		{Name: "CM_Ring.Level"},
		{Name: "CM_Ring_Gameplay.Markerset"},
		{Name: "CM_Ring_Warden.BotClass", Base: "PC_Warden.BotClass", Source: "Maps/CM_Ring_Warden.BotClass"},
	}}
	return grove, ring
}

// madeUpMerged is the SHA-256 of the archive Build makes from MadeUp() and the two made-up maps.
const madeUpMerged = "56739bdd162adc2ee181e15f427a0a076a8e9d89f8d6a95a4f6f1df962db7c43"

// catalogOf reads an archive's catalog with the client's CatalogEntry layout.
func catalogOf(t *testing.T, archive []byte) []*assetdata.Object {
	t.Helper()
	a, err := snap.Parse(archive)
	if err != nil {
		t.Fatal(err)
	}
	if a.Entries[0].Name != Catalog {
		t.Fatalf("the archive's first file is %s", a.Entries[0].Name)
	}
	cat, err := a.Read(a.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	ts, err := assetdata.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	obj, bad, err := ts.Parse(ts.Type("Catalog"), cat, nil)
	if err != nil || len(bad) > 0 {
		t.Fatalf("the catalog doesn't parse: %v %v", err, bad)
	}
	v, _ := obj.Get("entries")
	var out []*assetdata.Object
	for _, e := range v.([]any) {
		out = append(out, e.(*assetdata.Object))
	}
	return out
}

func field(o *assetdata.Object, k string) any {
	v, _ := o.Get(k)
	return v
}

// The bot profiles and two made-up maps merge into one catalog: the copy's own entries unchanged, then the profiles,
// then the maps by name whatever order they come in, each entry with its class's typeCrc.
func TestMergeCatalogMadeUp(t *testing.T) {
	in := MadeUp()
	grove, ring := madeUpMaps()
	out, err := Build(in, ring, grove)
	if err != nil {
		t.Fatal(err)
	}
	other, err := Build(in, grove, ring)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, other) {
		t.Error("the catalog depends on the order of the maps")
	}
	if got := digest(out); got != madeUpMerged {
		t.Errorf("the merged archive: SHA-256 %s, want %s", got, madeUpMerged)
	}
	alone, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if digest(alone) != madeUpArchive {
		t.Error("without maps the archive is not the bot profiles' alone")
	}
	// the files are the profiles' alone: the maps' .bin files are in their own archives
	a, _ := snap.Parse(out)
	b, _ := snap.Parse(alone)
	if len(a.Entries) != len(b.Entries) {
		t.Errorf("%d files, the profiles' archive %d", len(a.Entries), len(b.Entries))
	}

	ts, _ := assetdata.Embedded()
	entries := catalogOf(t, out)
	var names []string
	for _, e := range entries[10:] {
		names = append(names, field(e, "assetNameType").(string))
	}
	want := []string{"PC_Warden_Predator.BotClass", "PC_Golem_Hunter.BotClass", "PC_Survivor_Hunter.BotClass",
		"PC_Desecrator_Tactician.BotClass", "PC_MerchantPrincess_Gladiator.BotClass", "PC_Otter_Hunter.BotClass",
		"CM_Grove.Level", "CM_Grove_Gameplay.Markerset", "CM_Grove.LevelGameplayTuning",
		"CM_Ring.Level", "CM_Ring_Gameplay.Markerset", "CM_Ring_Warden.BotClass"}
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Errorf("added entries %v", names)
	}
	for _, e := range entries {
		name := field(e, "assetNameType").(string)
		_, class, _ := strings.Cut(name, ".")
		if class == "Noun" {
			continue // the made-up catalog's lantern.Noun has a made-up typeCrc
		}
		if typ := ts.Type(class); typ == nil || field(e, "typeCrc") != uint64(typ.CRC) {
			t.Errorf("%s: typeCrc %v", name, field(e, "typeCrc"))
		}
		if field(e, "location") != uint64(4) || field(e, "status") != uint64(4) || field(e, "compileTime") != int64(1415127097) {
			t.Errorf("%s: location %v, status %v, compile time %v", name, field(e, "location"), field(e, "status"),
				field(e, "compileTime"))
		}
	}
	srcOf := map[string]any{}
	tagsOf := map[string]string{}
	for _, e := range entries {
		n := field(e, "assetNameType").(string)
		srcOf[n] = field(e, "sourceFileNameType")
		tagsOf[n] = string(assetdata.AppendJSON(nil, field(e, "tags")))
	}
	for n, s := range map[string]string{"CM_Grove.Level": "CM_Grove.Level", "CM_Grove.LevelGameplayTuning": "Maps/CM_Grove.LevelGameplayTuning",
		"CM_Ring_Warden.BotClass": "Maps/CM_Ring_Warden.BotClass", "PC_Warden_Predator.BotClass": "Heroes/warden/PC_Warden.Noun"} {
		if srcOf[n] != s {
			t.Errorf("%s: source %v, want %s", n, srcOf[n], s)
		}
	}
	if tagsOf["CM_Grove.LevelGameplayTuning"] != `["placeable","npc"]` || tagsOf["lantern.Noun"] != `["placeable","npc"]` ||
		tagsOf["CM_Grove.Level"] != `[]` {
		t.Errorf("tags %v", tagsOf)
	}
}

// What the merge refuses: a name listed twice (shipped, by the profiles, by two maps, in another case), two sources of
// one name, a map named like the profiles' source, a class the client doesn't know, a base of another class.
func TestMergeCatalogRefuses(t *testing.T) {
	in := MadeUp()
	grove, ring := madeUpMaps()
	one := func(name string, e ...CatalogEntry) CatalogSource { return CatalogSource{Name: name, Entries: e} }
	for what, maps := range map[string][]CatalogSource{
		"a shipped name":        {one("CM_A", CatalogEntry{Name: "PC_Warden.BotClass"})},
		"a profile's name":      {one("CM_A", CatalogEntry{Name: "PC_Warden_Predator.BotClass", Base: "PC_Warden.BotClass"})},
		"a name in two maps":    {grove, one("CM_A", CatalogEntry{Name: "CM_Grove.Level"})},
		"a name in other case":  {grove, one("CM_A", CatalogEntry{Name: "cm_grove.level"})},
		"a name twice in a map": {one("CM_A", CatalogEntry{Name: "CM_A.Level"}, CatalogEntry{Name: "CM_A.Level"})},
		"two maps of one name":  {grove, one("cm_grove", CatalogEntry{Name: "CM_Other.Level"})},
		"the profiles' name":    {one(ProfilesSource, CatalogEntry{Name: "CM_A.Level"})},
		"an unknown class":      {one("CM_A", CatalogEntry{Name: "CM_A.Nope"})},
		"no class":              {one("CM_A", CatalogEntry{Name: "CM_A"})},
		"a base of other class": {one("CM_A", CatalogEntry{Name: "CM_A.Level", Base: "PC_Warden.BotClass"})},
		"a missing base":        {one("CM_A", CatalogEntry{Name: "CM_A.BotClass", Base: "PC_Nobody.BotClass"})},
		"tags on a copy":        {one("CM_A", CatalogEntry{Name: "CM_A.BotClass", Base: "PC_Warden.BotClass", Tags: []string{"npc"}})},
		"a NUL in a name":       {one("CM_A", CatalogEntry{Name: "CM_A\x00.Level"})},
	} {
		if _, err := Build(in, maps...); err == nil {
			t.Errorf("%s: taken", what)
		}
	}
	if _, err := Build(in, ring, grove); err != nil {
		t.Errorf("the made-up maps: %v", err)
	}
}

// Build 854's AssetData.snap with the two made-up maps: the shipped catalog's 6,969 entries unchanged, then the
// profiles' and the maps' (skipped without the game files).
func TestMergeCatalogGame(t *testing.T) {
	in := testgame.Read(t, "AssetData.snap")
	grove, ring := madeUpMaps()
	out, err := Build(in, grove, ring)
	if err != nil {
		t.Fatal(err)
	}
	alone, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	got, base := catalogOf(t, out), catalogOf(t, alone)
	if len(got) != 6969+len(Profiles)+6 || len(base) != 6969+len(Profiles) {
		t.Fatalf("%d entries (alone %d)", len(got), len(base))
	}
	for i, e := range base {
		if !bytes.Equal(assetdata.AppendJSON(nil, e), assetdata.AppendJSON(nil, got[i])) {
			t.Errorf("entry %d changed", i)
		}
	}
	last := got[len(got)-1]
	if field(last, "assetNameType") != "CM_Ring_Warden.BotClass" {
		t.Errorf("the last entry %v", field(last, "assetNameType"))
	}
}
