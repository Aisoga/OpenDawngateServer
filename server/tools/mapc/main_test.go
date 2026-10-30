package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/testmaps"
)

var ring = testmaps.Pkg("CM_Ring")

func call(args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	code := run(args, &out, &errw)
	return code, out.String(), errw.String()
}

func TestCheckJSON(t *testing.T) {
	code, out, _ := call("check", "--json", ring)
	var r mapc.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != 0 || !r.OK || r.Map != "CM_Ring" || r.Stats == nil {
		t.Fatalf("exit %d, %v: %s", code, err, out)
	}
	// a broken copy of the package: exit 1, the finding names the place in map.json
	dir := filepath.Join(t.TempDir(), "CM_Broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(ring, "map.json"))
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.Replace(b, []byte(`"shop": [-204, -204]`), []byte(`"shop": [-215, -150]`), 1)
	if err := os.WriteFile(filepath.Join(dir, "map.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = call("check", "--json", dir)
	r = mapc.Report{}
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != 1 || r.OK {
		t.Fatalf("exit %d, %v: %s", code, err, out)
	}
	found := false
	for _, f := range r.Findings {
		found = found || (f.Rule == "fixed.open" && f.Path == "base.shop" && f.At != nil && f.At[0] == -215)
	}
	if !found {
		t.Errorf("no fixed.open finding on base.shop: %s", out)
	}
	// a sibling with the same tag
	sib := filepath.Join(filepath.Dir(dir), "CM_Sibling")
	if err := os.MkdirAll(sib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sib, "map.json"), []byte(`{"name": "CM_Sibling", "tag": "ring"}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	if _, out, _ = call("check", dir); !strings.Contains(out, "names.tag-unique") {
		t.Errorf("the sibling's tag wasn't seen: %s", out)
	}
	// --maps: the installed maps are another folder's packages; the one of the package's folder name is itself
	inst := t.TempDir()
	for name, text := range map[string]string{"CM_Broken": `{"name": "CM_Ring", "tag": "broken"}`,
		"CM_Other": `{"name": "CM_Other", "tag": "ring"}`} {
		if err := os.MkdirAll(filepath.Join(inst, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(inst, name, "map.json"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, out, _ = call("check", "--maps", inst, dir); !strings.Contains(out, "names.tag-unique") ||
		strings.Contains(out, "CM_Sibling") || strings.Contains(out, "names.unique") {
		t.Errorf("--maps: %s", out)
	}
	if _, out, _ = call("check", "--maps", t.TempDir(), dir); strings.Contains(out, "names.tag-unique") {
		t.Errorf("--maps of an empty folder still saw the sibling: %s", out)
	}
	// unreadable JSON
	if err := os.WriteFile(filepath.Join(dir, "map.json"), []byte(`{"format": 1, "nme": "x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ = call("check", "--json", dir); code != 1 || !strings.Contains(out, "format.json") {
		t.Errorf("exit %d: %s", code, out)
	}
}

func TestPreview(t *testing.T) {
	dir := t.TempDir()
	code, out, errw := call("preview", "--json", ring, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errw)
	}
	b, err := os.ReadFile(filepath.Join(dir, "preview.json"))
	if err != nil {
		t.Fatal(err)
	}
	var idx mapc.PreviewIndex
	if err := json.Unmarshal(b, &idx); err != nil || len(idx.Files) != 9 || len(idx.Objects) == 0 {
		t.Fatalf("%v: %d files, %d objects", err, len(idx.Files), len(idx.Objects))
	}
	for _, f := range idx.Files {
		if _, err := os.Stat(filepath.Join(dir, f.File)); err != nil {
			t.Error(err)
		}
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"check"}, {"check", "--bogus", ring}, {"frobnicate", ring},
		{"build", ring}, {"check", "--copy"}, {"check", "--maps"}} {
		if code, _, _ := call(args...); code != 2 {
			t.Errorf("%q: exit %d, 2 expected", args, code)
		}
	}
}
