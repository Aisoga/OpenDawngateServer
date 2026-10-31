package mapedit

import (
	"bytes"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fetch(t *testing.T, url string) []byte {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("%s: %d", url, res.StatusCode)
	}
	return b
}

// TestGroundIO: Export writes the draft's tiles at their own size; the tiles imported again become the ground's
// picture (the layers and stamps dropped), the check and the bake pass and the bake looks as before; Save copies the
// named tiles into the package and removes an editor tile no longer named; the import's guards.
func TestGroundIO(t *testing.T) {
	if gameCopy() == "" {
		t.Skip("no game files")
	}
	e := newEnv(t, true)
	var tr assetReply
	if code := e.call("POST", "/api/ground-tile/CM_Ring?i=0&j=9", grey(t, 8, 1), &tr); code != 400 {
		t.Errorf("tile (0, 9): %d", code)
	}
	if code := e.call("POST", "/api/ground-tile/CM_Ring?i=0&j=0", []byte("no picture"), &tr); code != 400 {
		t.Errorf("not a PNG: %d", code)
	}
	doc, _, sha := e.open("CM_Ring")
	g := doc["ground"].(map[string]any)
	g["texels"] = 1024
	if r := e.check("CM_Ring", doc); !r.Report.OK {
		t.Fatalf("%+v", r.Report.Findings)
	}
	var bk struct {
		OK  bool
		URL string
	}
	if code := e.call("POST", "/api/bake/CM_Ring", nil, &bk); code != 200 || !bk.OK {
		t.Fatalf("bake: %d %+v", code, bk)
	}
	before := fetch(t, e.srv.URL+bk.URL+"ground.png")
	var ex struct {
		OK     bool
		Dir    string
		URL    string
		Margin int
		Tiles  []string `json:"tiles_png"`
	}
	if code := e.call("POST", "/api/ground-export/CM_Ring", nil, &ex); code != 200 || !ex.OK || len(ex.Tiles) != 4 ||
		ex.Margin != 4 || !strings.HasPrefix(ex.Dir, e.c.Out) {
		t.Fatalf("export: %d %+v", code, ex)
	}
	var picture []any
	for k, f := range ex.Tiles {
		b := fetch(t, e.srv.URL+ex.URL+f)
		if c, err := png.DecodeConfig(bytes.NewReader(b)); err != nil || c.Width != 1024 {
			t.Fatalf("%s: %+v %v", f, c, err)
		}
		if _, err := os.Stat(filepath.Join(ex.Dir, f)); err != nil {
			t.Errorf("%s isn't in the export folder: %v", f, err)
		}
		q := "?i=" + string(rune('0'+k%2)) + "&j=" + string(rune('0'+k/2))
		if code := e.call("POST", "/api/ground-tile/CM_Ring"+q, b, &tr); code != 200 || !tileName.MatchString(tr.File) ||
			!strings.HasPrefix(tr.File, "ground/"+strings.TrimSuffix(f, ".png")+"_") {
			t.Fatalf("import %s: %d %+v", f, code, tr)
		}
		picture = append(picture, tr.File)
	}
	delete(g, "layers")
	delete(g, "stamps")
	g["picture"] = picture
	if r := e.check("CM_Ring", doc); !r.Report.OK {
		t.Fatalf("the picture: %+v", r.Report.Findings)
	}
	if code := e.call("POST", "/api/bake/CM_Ring", nil, &bk); code != 200 || !bk.OK {
		t.Fatalf("bake with the picture: %d %+v", code, bk)
	}
	after := fetch(t, e.srv.URL+bk.URL+"ground.png")
	ia, err1 := png.Decode(bytes.NewReader(before))
	ib, err2 := png.Decode(bytes.NewReader(after))
	if err1 != nil || err2 != nil || ia.Bounds() != ib.Bounds() {
		t.Fatalf("%v %v", err1, err2)
	}
	var sum, n float64
	for y := 0; y < ia.Bounds().Dy(); y += 7 {
		for x := 0; x < ia.Bounds().Dx(); x += 7 {
			r1, g1, b1, _ := ia.At(x, y).RGBA()
			r2, g2, b2, _ := ib.At(x, y).RGBA()
			for _, d := range []int{int(r1>>8) - int(r2>>8), int(g1>>8) - int(g2>>8), int(b1>>8) - int(b2>>8)} {
				sum += float64(max(d, -d))
				n++
			}
		}
	}
	if sum/n > 3 {
		t.Errorf("the bake from the picture differs from the layers' bake by %.2f on average", sum/n)
	}
	// a layer over the picture without a mask fails the check
	g["layers"] = []any{map[string]any{"texture": map[string]any{"procedural": "dirt"}}}
	if r := e.check("CM_Ring", doc); r.Report.OK || !hasRule(r, "ground.mask") {
		t.Errorf("an unmasked layer over the picture: %+v", r.Report.Findings)
	}
	delete(g, "layers")
	// save: the four tiles go into the package; another import of a tile, saved, removes the old one
	var sv struct {
		OK       bool
		Sha256   string
		Pictures []string
		Removed  []string
	}
	if code := e.call("POST", "/api/save/CM_Ring?base="+sha, doc, &sv); code != 200 || !sv.OK || len(sv.Pictures) != 4 {
		t.Fatalf("save: %d %+v", code, sv)
	}
	pkg := filepath.Join(e.maps, "CM_Ring")
	old := picture[3].(string)
	if _, err := os.Stat(filepath.Join(pkg, filepath.FromSlash(old))); err != nil {
		t.Fatalf("the tile wasn't saved: %v", err)
	}
	if code := e.call("POST", "/api/ground-tile/CM_Ring?i=1&j=1", grey(t, 1024, 100), &tr); code != 200 || tr.File == old {
		t.Fatalf("another tile (1, 1): %d %+v", code, tr)
	}
	picture[3] = tr.File
	g["picture"] = picture
	if code := e.call("POST", "/api/save/CM_Ring?base="+sv.Sha256, doc, &sv); code != 200 || !sv.OK ||
		strings.Join(sv.Removed, ",") != old {
		t.Errorf("save with the new tile: %d %+v", code, sv)
	}
	if _, err := os.Stat(filepath.Join(pkg, filepath.FromSlash(old))); err == nil {
		t.Error("the tile no longer named is still in the package")
	}
	// the tiles aren't listed as the package's pictures
	var list struct{ Assets []struct{ File string } }
	e.call("GET", "/api/assets/CM_Ring", nil, &list)
	for _, a := range list.Assets {
		if strings.Contains(a.File, "tile_") {
			t.Errorf("a tile in the pictures' list: %s", a.File)
		}
	}
}
