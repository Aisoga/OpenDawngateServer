package gamecontent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/dbpf"
	"dawngate/server/internal/setup/luac"
)

// The client's number rule (0x004CD5D0 over _fcvt): floor without a precision, "0" for a fraction, "1" from 0.95,
// else rounded to the precision with the trailing zeros dropped; the arithmetic is float32's.
func TestFormatNumber(t *testing.T) {
	f32 := func(x float64) float64 { return float64(float32(x)) }
	for _, c := range []struct {
		v    float64
		prec int
		want string
	}{
		{15, 0, "15"}, {15.9, 0, "15"}, {0.5, 0, "0"}, {0.5, 1, "0.5"}, {0.96, 1, "1"}, {0.25, 2, "0.25"},
		{f32(f32(1-f32(0.9)) * 100), 1, "10"}, {2.50, 2, "2.5"}, {3, 2, "3"}, {0, 0, "0"}, {-10, 0, "-10"},
		{f32(f32(0.15) * 100), 0, "15"},
	} {
		if got := formatNumber(c.v, c.prec); got != c.want {
			t.Errorf("formatNumber(%v, %d) = %q, want %q", c.v, c.prec, got, c.want)
		}
	}
	if got := formatScaled(luac.Scaled{Attr: "Power", Factor: float64(float32(0.4))}, 0); got != "40% of Power" {
		t.Errorf("scaled %q", got)
	}
	if got := formatScaled(luac.Scaled{Attr: "MaxHealth", Factor: 0.05, Offset: 20}, 0); got != "20 + 5% of maximum Health" {
		t.Errorf("scaled with offset %q", got)
	}
}

// A made-up passive (scripts shipped as source): its description at its rank with tokens of its own script and of
// another, a precision, a per-rank field, a token the plain branch turns (* 100), a "Current" line and an unknown
// token's line left out, the colours kept as runs.
func TestPassiveText(t *testing.T) {
	dir := t.TempDir()
	main := "modifier_Item_Test_Bolt = nAbility.create()\n" +
		"modifier_Item_Test_Bolt.localizedGroup = nUtil.SPID(\"ItemModifiers\")\n" +
		"modifier_Item_Test_Bolt.localizedDescription = nUtil.ToGUID(\"0x00000050\")\n" +
		"modifier_Item_Test_Bolt.damage = 75\n" +
		"modifier_Item_Test_Bolt.ratio = 0.125\n" +
		"modifier_Item_Test_Bolt.duration = {2, 3}\n" +
		"modifier_Item_Test_Bolt[ nAbilityFns.TranslateToken ] = function( myTable )\n" +
		"    if myTable.token == \"ratioPct\" then myTable.translatedToken = self.ratio * 100\n" +
		"    end\nend\n" +
		"nAbility.RegisterAbility(\"modifier_Item_Test_Bolt\")\n"
	other := "modifier_Item_Test_Bolt_Slow = nAbility.create()\nmodifier_Item_Test_Bolt_Slow.slow = 0.3\n" +
		"nAbility.RegisterAbility(\"modifier_Item_Test_Bolt_Slow\")\n"
	os.WriteFile(filepath.Join(dir, ServerDataPackage), dbpf.WritePackage([]dbpf.NewEntry{
		{Type: luaType, Group: fnv("Modifiers"), Instance: uint64(fnv("modifier_Item_Test_Bolt")), Data: []byte(main)},
		{Type: luaType, Group: fnv("Modifiers"), Instance: uint64(fnv("modifier_Item_Test_Bolt_Slow")), Data: []byte(other)},
	}), 0o644)
	desc := `<font color="#FFFF99">Bolt</font>: Deals ~modifier_Item_Test_Bolt.damage~ (<font color="#55ff55">+~precision1.modifier_Item_Test_Bolt.ratioPct~%</font>) ` +
		`damage &amp; slows by ~precision2.modifier_Item_Test_Bolt_Slow.slow~ for ~ modifier_Item_Test_Bolt.duration~ seconds.<br><br>` +
		`Current Bonus: ~modifier_Item_Test_Bolt.stacks~<br>Unknown: ~nowhere.x~`
	pkg, err := dbpf.Parse(dbpf.WritePackage([]dbpf.NewEntry{
		{Type: 1, Group: 1, Instance: uint64(fnv("ItemModifiers")), Data: []byte("0x00000050 " + desc + "\n")},
	}))
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := assetdata.LoadLocale(pkg)
	sc, err := openScripts(dir)
	if err != nil {
		t.Fatal(err)
	}
	text, parts := passiveText(sc, sc.get("modifier_Item_Test_Bolt"), 2, loc)
	if want := "Deals 75 (+12.5%) damage & slows by 0.3 for 3 seconds."; text != want {
		t.Errorf("text %q\nwant %q", text, want)
	}
	wantParts := []content.TextPart{{Text: "Deals 75 ("}, {Text: "+12.5%", Color: "#55FF55"},
		{Text: ") damage & slows by 0.3 for 3 seconds."}}
	if !reflect.DeepEqual(parts, wantParts) {
		t.Errorf("parts %+v", parts)
	}
	if got := markupParts(`a<b>b</b><font color="#123456">c<font color=red>d</font>e</font>f<br>`); !reflect.DeepEqual(got,
		[]content.TextPart{{Text: "ab"}, {Text: "cde", Color: "#123456"}, {Text: "f"}}) {
		t.Errorf("markup %+v", got)
	}
}
