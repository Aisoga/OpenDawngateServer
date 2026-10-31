package mapedit

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestView3DOff(t *testing.T) {
	e := newEnv(t, false)
	var st struct {
		View3D struct {
			Enabled bool   `json:"enabled"`
			Reason  string `json:"reason"`
		} `json:"view3d"`
	}
	e.call("GET", "/api/state", nil, &st)
	if st.View3D.Enabled || !strings.Contains(st.View3D.Reason, "no game copy") {
		t.Errorf("without a copy: %+v", st.View3D)
	}
	var res map[string]any
	if code := e.call("GET", "/api/scene/CM_Ring", nil, &res); code != http.StatusConflict {
		t.Errorf("scene without a copy: %d %v", code, res)
	}
	if code := e.call("GET", "/api/level-scene/SM_Map_v5..", nil, &res); code != http.StatusBadRequest {
		t.Errorf("a bad level name: %d", code)
	}
	if code := e.call("GET", "/3d/../map.json", nil, nil); code != http.StatusNotFound && code != http.StatusMovedPermanently {
		t.Errorf("a path out of the cache: %d", code)
	}
}

// TestView3D exports CM_Ring's scene and ground through the API and fetches the files the page draws (needs the
// game files; skips without).
func TestView3D(t *testing.T) {
	if gameCopy() == "" {
		t.Skip("no game files")
	}
	e := newEnv(t, true)
	var res struct {
		OK    bool   `json:"ok"`
		Base  string `json:"base"`
		Scene struct {
			Map     string `json:"map"`
			Ground  string `json:"ground"`
			Objects []struct {
				Kind  string `json:"kind"`
				Parts []struct {
					Model string `json:"model"`
				} `json:"parts"`
			} `json:"objects"`
			Models map[string]struct {
				File     string   `json:"file"`
				Textures []string `json:"textures"`
			} `json:"models"`
		} `json:"scene"`
	}
	if code := e.call("GET", "/api/scene/CM_Ring", nil, &res); code != 200 || !res.OK || res.Scene.Map != "CM_Ring" ||
		res.Scene.Ground == "" || len(res.Scene.Models) < 5 { // the compiled Level: Guardians, Bindings, Cores, archons
		t.Fatalf("scene: %d %+v", code, res)
	}
	get := func(path string) []byte {
		r, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		if r.StatusCode != 200 {
			t.Fatalf("%s: %d", path, r.StatusCode)
		}
		return b
	}
	for _, m := range res.Scene.Models {
		var doc struct {
			Buffers []struct {
				URI        string `json:"uri"`
				ByteLength int    `json:"byteLength"`
			} `json:"buffers"`
		}
		if err := json.Unmarshal(get(res.Base+m.File), &doc); err != nil || len(doc.Buffers) != 1 {
			t.Fatalf("%s: %v", m.File, err)
		}
		if b := get(res.Base + doc.Buffers[0].URI); len(b) != doc.Buffers[0].ByteLength {
			t.Errorf("%s: %d bytes, want %d", doc.Buffers[0].URI, len(b), doc.Buffers[0].ByteLength)
		}
		for _, tx := range m.Textures {
			if b := get(res.Base + tx); string(b[1:4]) != "PNG" {
				t.Errorf("%s is no PNG", tx)
			}
		}
	}
	// an edit: the draft gets a wall; the ground is made again from the draft into a new folder
	file := []byte(e.text(t))
	doc := map[string]any{}
	if err := json.Unmarshal(file, &doc); err != nil {
		t.Fatal(err)
	}
	terrain := doc["terrain"].(map[string]any)
	terrain["shapes"] = append(terrain["shapes"].([]any), map[string]any{"type": "wall", "circle": []any{-150.0, -95.0}, "r": 5.0})
	var chk map[string]any
	if code := e.call("POST", "/api/check/CM_Ring", doc, &chk); code != 200 {
		t.Fatalf("check: %d %v", code, chk)
	}
	var g1, g2 struct {
		OK        bool   `json:"ok"`
		N         int    `json:"n"`
		URL       string `json:"url"`
		Triangles int    `json:"triangles"`
	}
	e.call("POST", "/api/ground/CM_Ring", nil, &g1)
	e.call("POST", "/api/ground/CM_Ring", nil, &g2)
	if !g1.OK || !g2.OK || g2.N != g1.N+1 || g1.Triangles != 204800 || !strings.HasPrefix(g2.URL, "/3d/ground/CM_Ring/") {
		t.Fatalf("ground: %+v %+v", g1, g2)
	}
	if b := get(g2.URL); !strings.Contains(string(b), `"CM_Ring_ground.bin"`) {
		t.Errorf("%s: %s", g2.URL, b[:min(200, len(b))])
	}
	if r, err := http.Get(e.srv.URL + "/3d/ground/CM_Ring/../../../x"); err == nil {
		if r.StatusCode == 200 {
			t.Error("a path out of the cache was served")
		}
		r.Body.Close()
	}
}

// text is the package's map.json as the API serves it.
func (e *env) text(t *testing.T) string {
	var m struct {
		Text string `json:"text"`
	}
	e.call("GET", "/api/map/CM_Ring", nil, &m)
	return m.Text
}
