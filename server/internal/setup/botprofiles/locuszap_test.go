package botprofiles

import (
	"strings"
	"testing"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

// zapDef is a made-up ServerEventDef file: one effect and the given sounds (event names).
func zapDef(t *testing.T, ts *assetdata.Types, typ *assetdata.Type, events ...string) []byte {
	t.Helper()
	v := assetdata.NewObject()
	fx := assetdata.NewObject()
	fx.Set("name", "base_megaZap_impact_red_effect")
	fx.Set("scale", 1.0)
	v.Set("effects", []any{fx})
	var audio []any
	for _, e := range events {
		s := assetdata.NewObject()
		s.Set("name", e)
		audio = append(audio, s)
	}
	v.Set("audio", audio)
	b, err := ts.Write(typ, v, assetdata.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func soundOf(t *testing.T, ts *assetdata.Types, typ *assetdata.Type, b []byte) (string, *assetdata.Object) {
	t.Helper()
	v, err := ts.ParseExact(typ, b)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := v.Get("audio")
	list, _ := a.([]any)
	if len(list) != 1 {
		t.Fatalf("%d sounds", len(list))
	}
	n, _ := list[0].(*assetdata.Object).Get("name")
	s, _ := n.(string)
	return s, v
}

// A made-up def: the sound's event changes and nothing else; a def with another sound or two is refused; the source
// reads both defs from an AssetData.snap and refuses one without them.
func TestLocusZapMadeUp(t *testing.T) {
	ts, err := assetdata.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	typ := ts.Type("ServerEventDef")
	in := zapDef(t, ts, typ, LocusZapShipped)
	out, err := ChangeZapSound(ts, typ, in)
	if err != nil {
		t.Fatal(err)
	}
	got, v := soundOf(t, ts, typ, out)
	if got != LocusZapEvent {
		t.Errorf("the sound is %q, want %q", got, LocusZapEvent)
	}
	_, w := soundOf(t, ts, typ, in)
	if a, b := string(assetdata.AppendJSON(nil, field(v, "effects"))), string(assetdata.AppendJSON(nil, field(w, "effects"))); a != b {
		t.Errorf("effects changed: %s, was %s", a, b)
	}
	if _, err := ChangeZapSound(ts, typ, zapDef(t, ts, typ, "Play_Other")); err == nil {
		t.Error("another sound: taken")
	}
	if _, err := ChangeZapSound(ts, typ, zapDef(t, ts, typ, LocusZapShipped, LocusZapShipped)); err == nil {
		t.Error("two sounds: taken")
	}

	var files []snap.File
	for _, d := range LocusZapDefs {
		files = append(files, snap.File{Name: LocusZapFile(d), Data: in})
	}
	src, err := LocusZapSound(snap.Pack(files))
	if err != nil {
		t.Fatal(err)
	}
	if src.Name != LocusZapSource || len(src.Entries) != 0 || len(src.Files) != len(LocusZapDefs) {
		t.Fatalf("source %q: %d entries, %d files", src.Name, len(src.Entries), len(src.Files))
	}
	for i, f := range src.Files {
		if f.Name != LocusZapFile(LocusZapDefs[i]) {
			t.Errorf("file %d: %s", i, f.Name)
		}
		if got, _ := soundOf(t, ts, typ, f.Data); got != LocusZapEvent {
			t.Errorf("%s: %q", f.Name, got)
		}
	}
	if _, err := LocusZapSound(snap.Pack(files[:1])); err == nil {
		t.Error("an AssetData.snap without the blue def: taken")
	}
}

// Build 854: both shipped defs play LocusZapShipped and come out with LocusZapEvent, every other member the shipped
// one; written back with the shipped event they are the shipped bytes; the archive carries them beside the rules'
// defs (skipped without the game files).
func TestLocusZapGame(t *testing.T) {
	in := testgame.Read(t, "AssetData.snap")
	a, err := snap.Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	shipped := map[string][]byte{}
	for _, e := range a.Entries {
		for _, d := range LocusZapDefs {
			if strings.EqualFold(e.Name, LocusZapFile(d)) {
				if shipped[d], err = a.Read(e); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	ts, _ := assetdata.Embedded()
	typ := ts.Type("ServerEventDef")
	src, err := LocusZapSound(in)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range LocusZapDefs {
		old, ov := soundOf(t, ts, typ, shipped[d])
		if old != LocusZapShipped {
			t.Errorf("%s ships %q", d, old)
		}
		got, nv := soundOf(t, ts, typ, src.Files[i].Data)
		if got != LocusZapEvent {
			t.Errorf("%s: %q", d, got)
		}
		for _, m := range typ.Members {
			if m.Name == "audio" {
				continue
			}
			if x, y := string(assetdata.AppendJSON(nil, field(ov, m.Name))), string(assetdata.AppendJSON(nil, field(nv, m.Name))); x != y {
				t.Errorf("%s: %s = %s, shipped %s", d, m.Name, y, x)
			}
		}
		// the same change backwards gives the shipped file byte for byte
		a, _ := nv.Get("audio")
		a.([]any)[0].(*assetdata.Object).Set("name", LocusZapShipped)
		back, err := ts.Write(typ, nv, assetdata.WriteOptions{Like: shipped[d]})
		if err != nil {
			t.Fatal(err)
		}
		if string(back) != string(shipped[d]) {
			t.Errorf("%s: written back with the shipped event, %d bytes differ from the shipped %d", d, len(back), len(shipped[d]))
		}
	}
	rules, err := RulesFeedback()
	if err != nil {
		t.Fatal(err)
	}
	out, err := Build(in, rules, src)
	if err != nil {
		t.Fatal(err)
	}
	files := archiveFiles(t, out)
	for i, d := range LocusZapDefs {
		if string(files[LocusZapFile(d)]) != string(src.Files[i].Data) {
			t.Errorf("the archive lacks %s", LocusZapFile(d))
		}
	}
}
