package mapedit

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func grey(t *testing.T, n int, v uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, n, n))
	for i := range img.Pix {
		img.Pix[i] = v
	}
	img.Set(0, 0, color.Gray{255 - v})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type assetReply struct {
	OK    bool
	File  string
	URL   string
	Error string
}

// groundLayers returns the document's ground layers.
func groundLayers(doc map[string]any) []any {
	return doc["ground"].(map[string]any)["layers"].([]any)
}

// TestGroundPictures: a picture added to the package's store reaches the draft's check, Save copies the ones
// map.json names into the package and removes the painted masks it no longer names; the store's guards.
func TestGroundPictures(t *testing.T) {
	e := newEnv(t, false)
	var a assetReply
	if code := e.call("POST", "/api/asset/CM_Ring?name=ground/blot.png", grey(t, 64, 200), &a); code != 200 ||
		a.File != "ground/blot.png" {
		t.Fatalf("add: %d %+v", code, a)
	}
	if code := e.call("POST", "/api/asset/CM_Ring?name=ground/blot.png", grey(t, 64, 200), &a); code != 200 ||
		a.File != "ground/blot.png" {
		t.Errorf("the same picture again: %d %+v", code, a)
	}
	if code := e.call("POST", "/api/asset/CM_Ring?name=ground/blot.png", grey(t, 64, 90), &a); code != 200 ||
		a.File != "ground/blot_2.png" {
		t.Errorf("another picture of that name: %d %+v", code, a)
	}
	for _, c := range []struct {
		name string
		body []byte
	}{{"ground/../x.png", grey(t, 8, 1)}, {"x.png", grey(t, 8, 1)}, {"ground/x.jpg", grey(t, 8, 1)},
		{"ground/x.png", []byte("not a picture")}, {"ground/big.png", grey(t, 4097, 1)}} {
		if code := e.call("POST", "/api/asset/CM_Ring?name="+c.name, c.body, &a); code != 400 {
			t.Errorf("%s: %d, want 400", c.name, code)
		}
	}
	res, err := http.Get(e.srv.URL + "/api/asset/CM_Ring/ground/blot_2.png")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Equal(b, grey(t, 64, 90)) {
		t.Errorf("get: %d, %d bytes", res.StatusCode, len(b))
	}
	var list struct {
		Assets []struct {
			File  string
			Saved bool
			W, H  int
		}
	}
	if e.call("GET", "/api/assets/CM_Ring", nil, &list); len(list.Assets) != 2 || list.Assets[0].Saved ||
		list.Assets[0].W != 64 {
		t.Errorf("list: %+v", list.Assets)
	}

	// a layer masked by the picture plus a soft brush stroke and an eraser stroke: the draft has the picture
	doc, _, sha := e.open("CM_Ring")
	layers := groundLayers(doc)
	layers = append(layers, map[string]any{"name": "painted", "texture": map[string]any{"rgb": []any{200, 180, 90}},
		"image": "ground/blot.png", "shapes": []any{
			map[string]any{"path": []any{[]any{-120, -60}, []any{-100, -40}}, "width": 6, "soft": 3},
			map[string]any{"path": []any{[]any{-110, -50}, []any{-105, -45}}, "width": 3, "erase": true}}})
	doc["ground"].(map[string]any)["layers"] = layers
	if r := e.check("CM_Ring", doc); !r.Report.OK || r.Exit != 0 {
		t.Fatalf("the painted layer: %+v", r.Report.Findings)
	}
	layers[len(layers)-1].(map[string]any)["image"] = "ground/missing.png"
	if r := e.check("CM_Ring", doc); r.Report.OK || !hasRule(r, "ground.file") {
		t.Errorf("a missing picture passes: %+v", r.Report.Findings)
	}
	// save: the named picture goes into the package, the other stays in the store
	layers[len(layers)-1].(map[string]any)["image"] = "ground/blot.png"
	var sv struct {
		OK       bool
		Sha256   string
		Pictures []string
		Removed  []string
	}
	if code := e.call("POST", "/api/save/CM_Ring?base="+sha, doc, &sv); code != 200 || !sv.OK ||
		strings.Join(sv.Pictures, ",") != "ground/blot.png" {
		t.Fatalf("save: %d %+v", code, sv)
	}
	pkg := filepath.Join(e.maps, "CM_Ring")
	if _, err := os.Stat(filepath.Join(pkg, "ground", "blot.png")); err != nil {
		t.Errorf("the picture wasn't saved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pkg, "ground", "blot_2.png")); err == nil {
		t.Error("a picture map.json doesn't name was saved")
	}
	// a painted mask replaces the picture, and is replaced again: Save removes the painted mask no longer named
	if e.call("POST", "/api/asset/CM_Ring?name=ground/paint_6_ab12cd34.png", grey(t, 32, 120), &a); a.File != "ground/paint_6_ab12cd34.png" {
		t.Fatalf("paint: %+v", a)
	}
	layers[len(layers)-1].(map[string]any)["image"] = a.File
	if e.call("POST", "/api/save/CM_Ring?base="+sv.Sha256, doc, &sv); !sv.OK || len(sv.Pictures) != 1 {
		t.Fatalf("save the painted mask: %+v", sv)
	}
	layers[len(layers)-1].(map[string]any)["image"] = "ground/blot.png"
	if e.call("POST", "/api/save/CM_Ring?base="+sv.Sha256, doc, &sv); !sv.OK ||
		strings.Join(sv.Removed, ",") != "ground/paint_6_ab12cd34.png" {
		t.Errorf("save without the painted mask: %+v", sv)
	}
	if _, err := os.Stat(filepath.Join(pkg, "ground", "paint_6_ab12cd34.png")); err == nil {
		t.Error("the painted mask no longer named is still in the package")
	}
	// the store and the package serve; a path out of them doesn't
	if code := e.call("GET", "/api/asset/CM_Ring/map.json", nil, nil); code != 404 {
		t.Errorf("map.json through the asset route: %d", code)
	}
	if code := e.call("POST", "/api/bake/CM_Ring", nil, nil); code != 409 {
		t.Errorf("a bake without a copy: %d", code)
	}
	if code := e.call("GET", "/api/textures", nil, nil); code != 409 {
		t.Errorf("the palette without a copy: %d", code)
	}
}

func hasRule(r checkReply, rule string) bool {
	for _, f := range r.Report.Findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

// TestGroundBake: the palette lists the copy's ground textures and makes their pictures; the bake preview of the
// draft writes the ground, the minimap and the masks.
func TestGroundBake(t *testing.T) {
	if gameCopy() == "" {
		t.Skip("no game files")
	}
	e := newEnv(t, true)
	var tex struct {
		OK       bool
		Count    int
		Textures []struct {
			Path, Archive string
			W, H          int
			Ground        bool
		}
	}
	if code := e.call("GET", "/api/textures", nil, &tex); code != 200 || !tex.OK || tex.Count < 100 {
		t.Fatalf("textures: %d, %d", code, tex.Count)
	}
	found := false
	for _, x := range tex.Textures {
		found = found || (strings.HasSuffix(x.Path, "OutsouceGroundMap02_D.dds") && x.Archive == "anshavar3.snap" && x.W == 2048)
	}
	if !found {
		t.Error("the grass texture isn't in the palette")
	}
	var pics struct {
		OK       bool
		Textures []struct{ Path, URL, Error string }
	}
	grass := "environments/anshavar/textures/OutsouceGroundMap02_D.dds"
	if code := e.call("POST", "/api/texture-pictures", map[string]any{"paths": []string{grass}, "max": 128}, &pics); code != 200 ||
		len(pics.Textures) != 1 || pics.Textures[0].URL == "" {
		t.Fatalf("texture pictures: %d %+v", code, pics)
	}
	if res, err := http.Get(e.srv.URL + pics.Textures[0].URL); err != nil || res.StatusCode != 200 {
		t.Fatalf("the picture: %v", err)
	} else {
		res.Body.Close()
	}
	if code := e.call("POST", "/api/texture-pictures", map[string]any{"paths": []string{"../x.dds"}}, nil); code != 400 {
		t.Errorf("a bad path: %d", code)
	}
	doc, _, _ := e.open("CM_Ring")
	e.check("CM_Ring", doc)
	var bk struct {
		OK    bool
		URL   string
		HalfM float64 `json:"half_m"`
		Files int
	}
	if code := e.call("POST", "/api/bake/CM_Ring", nil, &bk); code != 200 || !bk.OK || bk.HalfM < 200 || bk.Files < 3 {
		t.Fatalf("bake: %d %+v", code, bk)
	}
	for _, f := range []string{"ground.png", "minimap.png", "mask_1.png"} {
		res, err := http.Get(e.srv.URL + bk.URL + f)
		if err != nil || res.StatusCode != 200 {
			t.Errorf("%s: %v", f, err)
			continue
		}
		res.Body.Close()
	}
}
