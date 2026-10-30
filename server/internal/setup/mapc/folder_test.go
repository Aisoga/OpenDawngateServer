package mapc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePkg writes a package folder dir/sub with m's map.json (or raw text).
func writePkg(t *testing.T, dir, sub string, m *Map, raw string) {
	t.Helper()
	p := filepath.Join(dir, sub)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	b := []byte(raw)
	if m != nil {
		var err error
		if b, err = json.Marshal(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(p, FileName), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReadFolder: the packages in name order, with their labels; a broken map.json, a map failing a structural rule
// and a second map with a taken tag are left out with an error each; a folder without map.json and a file are
// ignored; a missing folder holds no maps.
func TestReadFolder(t *testing.T) {
	dir := t.TempDir()
	writePkg(t, dir, "CM_Ring", ring(t), "")
	other := ring(t)
	other.Name, other.Tag, other.Display, other.Prefix = "CM_Other", "other", "Other", "[O] "
	writePkg(t, dir, "A_Other", other, "")
	same := ring(t)
	same.Name = "CM_Same" // the tag "ring" is CM_Ring's, read after it
	writePkg(t, dir, "Z_Same", same, "")
	writePkg(t, dir, "B_Broken", nil, "{not json")
	bad := ring(t)
	bad.Name, bad.Tag = "SM_Map_v5", "smx"
	writePkg(t, dir, "C_Shipped", bad, "")
	if err := os.MkdirAll(filepath.Join(dir, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("maps"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgs, errs := ReadFolder(dir)
	var names []string
	for _, p := range pkgs {
		names = append(names, p.Label.Name+"/"+p.Label.Tag)
	}
	if got := strings.Join(names, ","); got != "CM_Other/other,CM_Ring/ring" {
		t.Errorf("packages %s", got)
	}
	if len(errs) != 3 {
		t.Errorf("%d errors: %v", len(errs), errs)
	}
	for i, want := range []string{"B_Broken", "SM_Map_v5", "CM_Same"} {
		if i < len(errs) && !strings.Contains(errs[i].Error(), want) {
			t.Errorf("error %d: %v, want %s", i, errs[i], want)
		}
	}
	if pkgs[1].Label.Archive != "zz_CM_Ring.snap" || pkgs[1].Label.Display != "Test Ring" ||
		pkgs[1].Dir != filepath.Join(dir, "CM_Ring") {
		t.Errorf("%+v", pkgs[1])
	}
	if o := Others(pkgs, 1); len(o) != 1 || o[0].Name != "CM_Other" {
		t.Errorf("others %+v", o)
	}
	if p, e := ReadFolder(filepath.Join(dir, "missing")); p != nil || e != nil {
		t.Errorf("a missing folder: %v %v", p, e)
	}
	// the test maps' fixture folder (server/testdata/maps): CM_Dress, CM_Grove and CM_Ring among its packages
	p, e := ReadFolder(filepath.Dir(ringDir))
	inRepo := map[string]bool{}
	for _, pk := range p {
		inRepo[pk.Map.Name] = true
	}
	if len(e) != 0 || !inRepo["CM_Ring"] || !inRepo["CM_Grove"] || !inRepo["CM_Dress"] {
		t.Errorf("the fixture maps: %d packages, %v", len(p), e)
	}
}
