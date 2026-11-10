package botprofiles

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

// archiveFiles are an archive's files by name.
func archiveFiles(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	a, err := snap.Parse(archive)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, e := range a.Entries {
		b, err := a.Read(e)
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name] = b
	}
	return out
}

// The match rules' defs: listed in the merged catalog with the class's typeCrc and their own source, carried as files
// in the archive, each file reading back as its value (the text key's hashes those of the LabsAlerts line) and written
// again to the same bytes; the asset ids the match host looks them up by.
func TestRulesFeedbackMadeUp(t *testing.T) {
	rules, err := RulesFeedback()
	if err != nil {
		t.Fatal(err)
	}
	in := MadeUp()
	grove, ring := madeUpMaps()
	out, err := Build(in, rules, ring, grove)
	if err != nil {
		t.Fatal(err)
	}
	alone, _ := Build(in, ring, grove)
	entries, base := catalogOf(t, out), catalogOf(t, alone)
	if len(entries) != len(base)+2 {
		t.Fatalf("%d catalog entries, without the rules %d", len(entries), len(base))
	}
	for i, d := range RulesFeedbackDefs { // "match rules" sorts after the CM_ maps
		e := entries[len(base)+i]
		if field(e, "assetNameType") != d.Name+".ClientFeedbackDef" || field(e, "typeCrc") != uint64(0x400C7E8D) ||
			field(e, "sourceFileNameType") != "ClientFeedback/"+d.Name+".ClientFeedbackDef" ||
			field(e, "location") != uint64(4) || field(e, "status") != uint64(4) {
			t.Errorf("entry %d: %s", len(base)+i, assetdata.AppendJSON(nil, e))
		}
	}
	files, files0 := archiveFiles(t, out), archiveFiles(t, alone)
	if len(files) != len(files0)+2 {
		t.Errorf("%d files, without the rules %d", len(files), len(files0))
	}
	ts, _ := assetdata.Embedded()
	typ := ts.Type("ClientFeedbackDef")
	le := binary.LittleEndian
	for i, d := range RulesFeedbackDefs {
		b, ok := files["AssetData/Binary/"+d.Name+".ClientFeedbackDef.bin"]
		if !ok {
			t.Fatalf("no file for %s", d.Name)
		}
		if !bytes.Equal(b, rules.Files[i].Data) {
			t.Errorf("%s: the archive's file is not RulesFeedback's", d.Name)
		}
		v, err := ts.ParseExact(typ, b)
		if err != nil {
			t.Fatalf("%s: %v", d.Name, err)
		}
		for k, want := range map[string]any{"priority": uint64(300), "channel": "character", "lifetime": uint64(3000),
			"cooldown": uint64(2000), "cooldownPerObject": false, "textType": "normal", "showTextInAlert": true,
			"chatMessageFilterType": "off", "doNotQueue": false, "pingMinimap": false, "soundID": nil} {
			if got := field(v, k); got != want {
				t.Errorf("%s: %s = %v, want %v", d.Name, k, got, want)
			}
		}
		if got := string(assetdata.AppendJSON(nil, field(v, "textLocaleKey"))); got != `"`+d.TextKey+`"` {
			t.Errorf("%s: textLocaleKey %s", d.Name, got)
		}
		// the Key's hash words before its text pointer: instance, type, group (FNV-1 of labsalerts)
		if inst, grp := le.Uint32(b[0x14:]), le.Uint32(b[0x1C:]); inst != 0x5E70E001+uint32(i) || grp != 0xB6F72728 {
			t.Errorf("%s: key hashes %08x/%08x", d.Name, inst, grp)
		}
		if again, err := ts.Write(typ, v, assetdata.WriteOptions{}); err != nil || !bytes.Equal(again, b) {
			t.Errorf("%s: written back, not the same bytes (%v)", d.Name, err)
		}
	}
	if content.AssetID("Rules_ShopClosed.ClientFeedbackDef") != 0xDCA47D52 ||
		content.AssetID("Rules_ItemNotAllowed.ClientFeedbackDef") != 0xDB7B5E92 {
		t.Error("the defs' asset ids moved")
	}
	// a file the archive already holds is refused
	dup := CatalogSource{Name: "CM_Dup", Files: []snap.File{{Name: strings.ToUpper(rules.Files[0].Name)}}}
	if _, err := Build(in, rules, dup); err == nil {
		t.Error("a file path twice: taken")
	}
}

// Build 854: the defs' values are the shipped OutOfRange's except the text key and the cooldown, and neither name is
// in the shipped catalog (skipped without the game files).
func TestRulesFeedbackGame(t *testing.T) {
	in := testgame.Read(t, "AssetData.snap")
	a, err := snap.Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	var shipped []byte
	for _, e := range a.Entries {
		if strings.EqualFold(e.Name, "AssetData/Binary/OutOfRange.ClientFeedbackDef.bin") {
			shipped, err = a.Read(e)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if shipped == nil {
		t.Fatal("no OutOfRange.ClientFeedbackDef.bin")
	}
	ts, _ := assetdata.Embedded()
	typ := ts.Type("ClientFeedbackDef")
	oor, err := ts.ParseExact(typ, shipped)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := RulesFeedback()
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range RulesFeedbackDefs {
		ours, err := ts.ParseExact(typ, rules.Files[i].Data)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range typ.Members {
			k := m.Name
			if k == "textLocaleKey" || k == "cooldown" {
				continue
			}
			if a, b := string(assetdata.AppendJSON(nil, field(oor, k))), string(assetdata.AppendJSON(nil, field(ours, k))); a != b {
				t.Errorf("%s: %s = %s, OutOfRange's %s", d.Name, k, b, a)
			}
		}
	}
	if _, err := Build(in, rules); err != nil {
		t.Errorf("build 854 with the rules' defs: %v", err)
	}
}
