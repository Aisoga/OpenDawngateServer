package assetdata

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// The synthetic Thing (every kind of member) written back: with the file as Like, from ParseExact's value
// and from Parse's dump value with a Locale, byte for byte; without Like, the same but for the pointer
// words (DefaultPointerBase plus the data's offset), the char[8]'s bytes after its NUL and the empty
// array's pointer word, which no value carries.
func TestWriteSyntheticRoundTrip(t *testing.T) {
	ts, err := ParseTypes([]byte(synthTypes))
	if err != nil {
		t.Fatal(err)
	}
	typ, data := ts.Type("Thing"), thing()
	exact, err := ts.ParseExact(typ, data)
	if err != nil {
		t.Fatal(err)
	}
	dump, _, err := ts.Parse(typ, data, synthLocale(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, obj := range map[string]*Object{"exact": exact, "dump": dump} {
		got, err := ts.Write(typ, obj, WriteOptions{Like: data})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("%s with Like: byte %#x differs\n%x\n%x", name, firstDiff(got, data), got, data)
		}
	}
	got, err := ts.Write(typ, exact, WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), data...)
	copy(want[0x20:0x24], "\x00\x00\x00\x00") // "abc\x00junk": the junk
	copy(want[0x9c:0xa0], "\x00\x00\x00\x00") // the empty array's pointer 0x1234
	ptrs, err := ts.PointerWords(typ, exact)
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	for _, p := range ptrs {
		if v := le.Uint32(got[p:]); v < DefaultPointerBase || int(v-DefaultPointerBase) >= len(got) {
			t.Errorf("pointer word at %#x: %#x", p, v)
		}
		copy(want[p:p+4], got[p:p+4])
	}
	if len(ptrs) != 15 {
		t.Errorf("%d pointer words, want 15", len(ptrs))
	}
	if !bytes.Equal(got, want) {
		t.Errorf("without Like: byte %#x differs", firstDiff(got, want))
	}
	// golden: the pointer words are the data's offsets (str's "hello" is the first data, at 0xa8)
	if v := le.Uint32(got[0x30:]); v != DefaultPointerBase+0xa8 {
		t.Errorf("str's pointer word %#x", v)
	}
	if h := sha256.Sum256(got); hex.EncodeToString(h[:]) != thingWritten {
		t.Errorf("SHA-256 %x, want %s", h, thingWritten)
	}
	again, err := ts.ParseExact(typ, got)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := AppendJSON(nil, exact), AppendJSON(nil, again); !bytes.Equal(a, b) {
		t.Errorf("the written file reads back as\n%s\nnot\n%s", b, a)
	}
}

// thingWritten is the SHA-256 of the synthetic Thing written without Like.
const thingWritten = "b3d310db6953d65ccc5af8fb413242e88858e0e407e8d9aaab8f95713bcce72a"

// subset reports where got differs from want in the members want sets (got may have more).
func subset(path string, want, got any) []string {
	if wo, ok := want.(*Object); ok {
		goo, ok := got.(*Object)
		if !ok {
			return []string{path + ": not an object"}
		}
		var out []string
		for _, k := range wo.Keys() {
			wv, _ := wo.Get(k)
			gv, _ := goo.Get(k)
			out = append(out, subset(path+"."+k, wv, gv)...)
		}
		return out
	}
	if wl, ok := want.([]any); ok {
		gl, ok := got.([]any)
		if !ok || len(gl) != len(wl) {
			return []string{path + ": not a list of the same length"}
		}
		var out []string
		for i := range wl {
			out = append(out, subset(fmt.Sprintf("%s[%d]", path, i), wl[i], gl[i])...)
		}
		return out
	}
	a, b := AppendJSON(nil, want), AppendJSON(nil, got)
	if !bytes.Equal(a, b) {
		return []string{path + ": " + string(a) + ", got " + string(b)}
	}
	return nil
}

func obj(kv ...any) *Object {
	o := NewObject()
	for i := 0; i < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// New assets of the classes a map adds, made from values only (the embedded tables, no game file), read
// back as those values, and a missing member reads as zero.
func TestWriteNewAssets(t *testing.T) {
	ts, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	f := func(v float32) any { return v }
	marker := func(name, noun string, x, z float32) *Object {
		return obj("markerName", name, "markerId", uint64(0x1234), "nounDef", noun,
			"pos", []any{f(x), f(0), f(z)}, "rotDegrees", []any{f(0), f(90), f(0)}, "scale", f(1),
			"visible", true, "childMarkerIds", []any{uint64(1), uint64(2)})
	}
	cases := []struct {
		class string
		v     *Object
	}{
		{"Level", obj("markersets", []any{obj("markersetAsset", "CM_Test_Gameplay.Markerset")},
			"music", "Music!CM_Test.ogg", "navMeshPathPlayer", "Levels/CM_Test/player.bfx",
			"physicsMesh", "Levels/CM_Test/CM_Test.phys", "levelSize", []any{f(400), f(400)},
			"name", uint64(0x0d0712d6), "cameraSettings", obj("pitchOverride", f(55)),
			"gameplayTuning", "CM_Test.LevelGameplayTuning", "notGameplayLevel", false)},
		{"Markerset", obj("markers", []any{marker("Locus_Mortal", "Locus_Mortal.Noun", -100, -100),
			marker("Locus_Spirit", "Locus_Spirit.Noun", 100.5, 100.25)}, "group", "CM_Test!Gameplay.group",
			"weight", f(1))},
		{"LevelGameplayTuning", obj("deathTimeBase", f(7.5), "surrenderEnableOn_MatchTimeInMinutes", int64(20),
			"goldMineCaptureDurationInSeconds", []any{f(16), f(14), f(12)}, "goldMineFollowerCountOnSpawn", uint64(3),
			"tower", []any{obj("respawnTime", f(60))}, "striderMortal", []any{"Strider_Mortal.Noun"},
			"towerRespawnEnabled", true)},
		{"CatalogEntry", obj("assetNameType", "CM_Test.Level", "compileTime", int64(1415127097),
			"typeCrc", uint64(0xa70b4c39), "sourceFileNameType", "CM_Test.Level", "location", uint64(4),
			"status", uint64(4), "tags", []any{"placeable", "npc"})},
	}
	for _, c := range cases {
		typ := ts.Type(c.class)
		b, err := ts.Write(typ, c.v, WriteOptions{})
		if err != nil {
			t.Fatalf("%s: %v", c.class, err)
		}
		back, err := ts.ParseExact(typ, b)
		if err != nil {
			t.Fatalf("%s: the new file doesn't parse: %v", c.class, err)
		}
		if d := subset(c.class, c.v, back); len(d) > 0 {
			t.Errorf("%s doesn't read back:\n  %s", c.class, strings.Join(d, "\n  "))
		}
		// the read value writes the same bytes again
		b2, err := ts.Write(typ, back, WriteOptions{})
		if err != nil || !bytes.Equal(b, b2) {
			t.Errorf("%s: written again, byte %#x differs (%v)", c.class, firstDiff(b, b2), err)
		}
		if _, bad, err := ts.Parse(typ, b, nil); err != nil || len(bad) > 0 {
			t.Errorf("%s: Parse: %v, bad references %v", c.class, err, bad)
		}
	}
	// a member the value leaves out is zero; Keys get their hashes
	lv, _ := ts.Write(ts.Type("Level"), obj("music", "Music!CM_Test.ogg"), WriteOptions{})
	le := binary.LittleEndian
	if h := keyHashes("Music!CM_Test.ogg"); le.Uint32(lv[0x08:]) != h[0] || le.Uint32(lv[0x0c:]) != h[1] ||
		le.Uint32(lv[0x10:]) != h[2] || h[2] != fnv1("Music") {
		t.Errorf("music's hash part %x", lv[0x08:0x14])
	}
	if len(lv) != 0x620+len("Music!CM_Test.ogg")+1 || le.Uint32(lv[0x14:]) != DefaultPointerBase+0x620 {
		t.Errorf("a Level with only music: %d bytes, pointer %#x", len(lv), le.Uint32(lv[0x14:]))
	}
}

// What Write refuses.
func TestWriteRefuses(t *testing.T) {
	ts, err := ParseTypes([]byte(synthTypes))
	if err != nil {
		t.Fatal(err)
	}
	typ := ts.Type("Thing")
	for name, v := range map[string]*Object{
		"unknown member":   obj("nope", uint64(1)),
		"NUL in a string":  obj("str", "a\x00b"),
		"char[8] too long": obj("name", "123456789"),
		"enum name":        obj("e", "three"),
		"bitfield name":    obj("bits", []any{"a", "z"}),
		"u8 too big":       obj("u8", uint64(256)),
		"float text":       obj("f", "1.5"),
		"vector length":    obj("v", []any{1.0, 2.0}),
		"array type":       obj("arr", "x"),
		"element type":     obj("arr", []any{"x"}),
		"Key type":         obj("plain", uint64(3)),
		"tObjID text":      obj("id", "deadbeef"),
		"_unk entry":       obj("_unk", obj("+0x4", "zz")),
		"_unk outside":     obj("_unk", obj("+0xa8", "00")),
		"inner member":     obj("inner", obj("y", uint64(1))),
	} {
		if _, err := ts.Write(typ, v, WriteOptions{}); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	// what fits: negative ints, a u8 of 255, a bool given as 2, enum and bitfield numbers
	if _, err := ts.Write(typ, obj("i", int64(-5), "u8", uint64(255), "b2", uint64(2), "e", int64(9),
		"bits", uint64(0x1b)), WriteOptions{}); err != nil {
		t.Error(err)
	}
}
