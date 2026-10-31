package textjson

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `{
  "format": 1,
  "name": "CM_Test",
  "size": 440.0,
  "future": {"kept": [1, 2, 3], "note": "a member the editor doesn't know"},
  "lanes": [
    {"id": 1, "width": 24, "knots": [[-170, -145], [-170, -100], [-174, -30], [-174, 30], [-165, 115],
      [-115, 165], [-30, 174]]}
  ],
  "base": {
    "locus": [-196, -196],
    "spawns": [[-188.9, -183.9], [-183.3, -189.8]]
  },
  "terrain": {
    "default": "wall",
    "shapes": [
      {"type": "open", "lane": 1},
      {"type": "bush", "polygon": [[-186, -24], [-178, -24], [-178, -4], [-186, -4]]}
    ]
  },
  "props": []
}
`

// roundTrip is what the page does: parse the file, change it, serialise it with JSON.stringify's layout.
func roundTrip(t *testing.T, text string, edit func(doc map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(doc)
	}
	b, err := json.Marshal(doc) // reorders the members: Merge must not care
	if err != nil {
		t.Fatal(err)
	}
	out, err := Merge([]byte(text), b)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUnchanged(t *testing.T) {
	texts := []string{sample, strings.ReplaceAll(sample, "\n", "\r\n"), "[]", " {\"a\":1e2 , \"b\" : [ ] }\n\n"}
	ring, err := os.ReadFile(filepath.Join("..", "..", "server", "testdata", "maps", "CM_Ring", "map.json"))
	if err == nil {
		texts = append(texts, string(ring))
	}
	for _, text := range texts {
		var v any
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		out, err := Merge([]byte(text), b)
		if err != nil || string(out) != text {
			t.Errorf("%v: changed\n%s", err, out)
		}
	}
}

func TestPointMoved(t *testing.T) {
	out := roundTrip(t, sample, func(doc map[string]any) {
		doc["base"].(map[string]any)["spawns"].([]any)[1] = []any{-180.5, -189.8}
	})
	want := strings.Replace(sample, "[-183.3, -189.8]", "[-180.5, -189.8]", 1)
	if string(out) != want {
		t.Errorf("got\n%s", out)
	}
}

func TestAddRemove(t *testing.T) {
	out := roundTrip(t, sample, func(doc map[string]any) {
		ter := doc["terrain"].(map[string]any)
		ter["shapes"] = append(ter["shapes"].([]any), map[string]any{"type": "wall", "circle": []any{-100, -60}, "r": 12})
		delete(doc, "props")
		doc["camps"] = []any{}
	})
	s := string(out)
	for _, want := range []string{
		"[[-186, -24], [-178, -24], [-178, -4], [-186, -4]]},\n      {\"circle\": [-100, -60], \"r\": 12, \"type\": \"wall\"}\n    ]",
		"\"future\": {\"kept\": [1, 2, 3]",
		"  },\n  \"camps\": []\n}\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, "props") {
		t.Errorf("props kept:\n%s", s)
	}
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("%v\n%s", err, s)
	}
}

func TestKnotInserted(t *testing.T) {
	out := roundTrip(t, sample, func(doc map[string]any) {
		l := doc["lanes"].([]any)[0].(map[string]any)
		k := l["knots"].([]any)
		k = append(k[:2], append([]any{[]any{-172, -65}}, k[2:]...)...)
		l["knots"] = k
	})
	want := strings.Replace(sample, "[-170, -100], [-174, -30]", "[-170, -100], [-172, -65], [-174, -30]", 1)
	if string(out) != want {
		t.Errorf("got\n%s", out)
	}
	// one deleted: the wrap stays where it was
	out = roundTrip(t, sample, func(doc map[string]any) {
		l := doc["lanes"].([]any)[0].(map[string]any)
		k := l["knots"].([]any)
		l["knots"] = append(k[:4:4], k[5:]...)
	})
	want = strings.Replace(sample, "[-174, 30], [-165, 115],\n", "[-174, 30],\n", 1)
	if string(out) != want {
		t.Errorf("got\n%s", out)
	}
}

func TestNumbersKept(t *testing.T) {
	// 440.0 and 440 are the same number: the file's spelling stays
	out := roundTrip(t, sample, func(doc map[string]any) { doc["name"] = "CM_Other" })
	if !strings.Contains(string(out), `"size": 440.0`) || !strings.Contains(string(out), `"name": "CM_Other"`) {
		t.Errorf("got\n%s", out)
	}
}

func TestLongValueWraps(t *testing.T) {
	var pts []any
	for i := 0; i < 30; i++ {
		pts = append(pts, []any{-100 - i, -50})
	}
	out := roundTrip(t, sample, func(doc map[string]any) {
		doc["terrain"].(map[string]any)["shapes"].([]any)[1].(map[string]any)["polygon"] = pts
	})
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) > Width+1 {
			t.Errorf("a line of %d: %s", len(line), line)
		}
	}
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// An object written on one line that grows into several lines is written fresh, one member per line, not with its
// inline start kept (MAPED-EDIT5: a template's {"style": "tiled"} ground painted).
func TestInlineGrowsFresh(t *testing.T) {
	var layers []any
	for i := 0; i < 4; i++ {
		layers = append(layers, map[string]any{"name": "layer with a long name " + strings.Repeat("x", 20),
			"texture": map[string]any{"procedural": "dirt"}, "scale": 16 + i, "shapes": []any{}})
	}
	out := roundTrip(t, sample, func(doc map[string]any) {
		f := doc["future"].(map[string]any)
		f["layers"] = layers
	})
	if strings.Contains(string(out), `"future": {"kept"`) || !strings.Contains(string(out), "\"future\": {\n    \"kept\": [1, 2, 3],\n") {
		t.Errorf("the grown object kept its inline start:\n%s", out)
	}
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// a one-line object that stays on one line keeps its layout
	out = roundTrip(t, sample, func(doc map[string]any) { doc["future"].(map[string]any)["note"] = "short" })
	if !strings.Contains(string(out), `"future": {"kept": [1, 2, 3], "note": "short"},`) {
		t.Errorf("a small change rewrote the object:\n%s", out)
	}
}

func TestSetMembers(t *testing.T) {
	out, err := SetMembers([]byte(sample), map[string]string{"name": `"CM_New"`, "display": `"New"`},
		[]string{"name", "display"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "\"name\": \"CM_New\",\n") || !strings.Contains(s, "  \"props\": [],\n  \"display\": \"New\"\n}") {
		t.Errorf("got\n%s", s)
	}
}

func TestParseErrors(t *testing.T) {
	for _, bad := range []string{"", "{", `{"a" 1}`, `[1,]`, `{"a":1}x`, `01`, `"\x"`, `tru`} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
