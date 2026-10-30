package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/dds"
	"dawngate/server/internal/setup/snap"
)

// madeUpTextures writes a copy whose archives hold textures of every kind the listing tells apart.
func madeUpTextures(t *testing.T) string {
	t.Helper()
	copyDir := t.TempDir()
	data := filepath.Join(copyDir, "game", "Data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	pic := func(w, h int, format string) []byte {
		px := make([]byte, 4*w*h)
		for i := range px {
			px[i] = byte(i * 7 % 251)
		}
		b, err := dds.Encode(w, h, px, format, dds.DefaultMips(w, h))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	write := func(name string, files ...snap.File) {
		if err := os.WriteFile(filepath.Join(data, name), snap.Pack(files), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("anshavar3.snap",
		snap.File{Name: "environments/anshavar/textures/Grass_D.dds", Data: pic(512, 512, "dxt1")},
		snap.File{Name: "environments/anshavar/textures/Grass_N.dds", Data: pic(512, 512, "dxt5")},
		snap.File{Name: "environments/anshavar/textures/Pebble_D.dds", Data: pic(64, 64, "dxt1")},
		snap.File{Name: "environments/anshavar/textures/Strip_D.dds", Data: pic(1024, 256, "dxt1")},
		snap.File{Name: "environments/anshavar/textures/Disc.dds", Data: pic(256, 256, "dxt5")},
		snap.File{Name: "environments/shared/Twice_D.dds", Data: []byte("an older copy, not a DDS file at all")},
		snap.File{Name: "UI/Icons/Button_D.dds", Data: pic(256, 256, "dxt5")},
		snap.File{Name: "models/rock.bmdl", Data: []byte("not a texture")})
	write("anshavar5.snap", snap.File{Name: "Environments/Shared/twice_d.dds", Data: pic(256, 512, "argb")})
	write("zz_Added.snap", snap.File{Name: "environments/anshavar/textures/Added_D.dds", Data: pic(512, 512, "dxt1")})
	return copyDir
}

func TestTextures(t *testing.T) {
	copyDir := madeUpTextures(t)
	code, stdout, errw := call("textures", "--json", copyDir)
	var res struct {
		OK       bool          `json:"ok"`
		Count    int           `json:"count"`
		Textures []TextureInfo `json:"textures"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil || code != 0 || !res.OK {
		t.Fatalf("textures: exit %d, %v: %s %s", code, err, stdout, errw)
	}
	var got []string
	for _, ti := range res.Textures {
		got = append(got, ti.Archive+":"+ti.Path)
	}
	// the colour maps of a readable format, square enough and large enough, not the interface's or a zz_ archive's;
	// a path two archives hold from the last one (anshavar5's, an ARGB picture with alpha)
	want := []string{"anshavar3.snap:environments/anshavar/textures/Disc.dds",
		"anshavar3.snap:environments/anshavar/textures/Grass_D.dds",
		"anshavar5.snap:Environments/Shared/twice_d.dds"}
	if strings.Join(got, " ") != strings.Join(want, " ") || res.Count != 3 {
		t.Fatalf("textures: %v, want %v", got, want)
	}
	if g := res.Textures[1]; g.W != 512 || g.H != 512 || g.Format != "DXT1" || g.Alpha || g.Kind != "colour" || !g.Ground {
		t.Errorf("Grass_D: %+v", g)
	}
	if a := res.Textures[2]; a.W != 256 || a.H != 512 || a.Format != "ARGB" || !a.Alpha {
		t.Errorf("twice_d: %+v", a)
	}
	// --all: every .dds of the shipped archives with what it is
	code, stdout, _ = call("textures", "--json", "--all", copyDir)
	res.Textures = nil
	if json.Unmarshal([]byte(stdout), &res) != nil || code != 0 || res.Count != 7 {
		t.Fatalf("--all: exit %d: %s", code, stdout)
	}
	kinds := map[string]string{}
	for _, ti := range res.Textures {
		kinds[filepath.Base(ti.Path)] = ti.Kind + " " + ti.Format
	}
	if kinds["Grass_N.dds"] != "normal DXT5" || kinds["Pebble_D.dds"] != "colour DXT1" || kinds["Button_D.dds"] != "colour DXT5" {
		t.Errorf("--all kinds: %v", kinds)
	}
	// --min lowers the size limit; plain text names each and counts them
	code, stdout, _ = call("textures", "--min", "64", copyDir)
	if code != 0 || !strings.Contains(stdout, "Pebble_D.dds") || !strings.HasSuffix(stdout, "4 textures\n") {
		t.Errorf("--min 64: exit %d: %s", code, stdout)
	}
	// texture: PNG pictures, halved down to --max-texture
	out := t.TempDir()
	code, stdout, errw = call("texture", "--json", "--max-texture", "128", copyDir,
		"environments/anshavar/textures/Grass_D.dds", out)
	var pics struct {
		OK       bool `json:"ok"`
		Textures []struct {
			Path, File string
		} `json:"textures"`
	}
	if json.Unmarshal([]byte(stdout), &pics) != nil || code != 0 || !pics.OK || len(pics.Textures) != 1 {
		t.Fatalf("texture: exit %d: %s %s", code, stdout, errw)
	}
	b, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(pics.Textures[0].File)))
	if err != nil || len(b) < 8 || string(b[1:4]) != "PNG" {
		t.Fatalf("the picture: %v", err)
	}
	if code, _, _ = call("texture", "--json", copyDir, "nope/missing.dds", out); code != 1 {
		t.Errorf("a missing texture: exit %d", code)
	}
	for _, args := range [][]string{{"textures"}, {"textures", copyDir, "extra"}, {"textures", "--min", "x", copyDir},
		{"texture", copyDir, out}} {
		if code, _, _ := call(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
