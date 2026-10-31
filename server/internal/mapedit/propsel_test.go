package mapedit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"testing"
)

// placedMarker is one marker of the check reply's placed list.
type placedMarker struct {
	Noun  string     `json:"noun"`
	Pos   [3]float64 `json:"pos"`
	Rot   [3]float64 `json:"rot"`
	Scale float64    `json:"scale"`
	Team  int        `json:"team"`
	Path  string     `json:"path"`
	Card  bool       `json:"card"`
}

func normDeg(a float64) float64 {
	a = math.Mod(math.Mod(a, 360)+360, 360)
	if a > 180 {
		a -= 360
	}
	return a
}

func r3(v float64) float64 { return math.Round(v*1000) / 1000 }

// deg1 is an angle to a tenth of a degree, -180 to 180, without a negative zero.
func deg1(a float64) float64 { return math.Round(normDeg(a)*10)/10 + 0 }

// differ lists the lines of a not in b ("+") and of b not in a ("-").
func differ(a, b []string) []string {
	in := func(xs []string) map[string]int {
		m := map[string]int{}
		for _, x := range xs {
			m[x]++
		}
		return m
	}
	ma, mb := in(a), in(b)
	var out []string
	for x, n := range ma {
		if mb[x] < n {
			out = append(out, "+ "+x)
		}
	}
	for x, n := range mb {
		if ma[x] < n {
			out = append(out, "- "+x)
		}
	}
	sort.Strings(out)
	return out
}

// TestSplitLossless is what the page's Split does, through the API with the game files (skips without): a group of
// SM_Map_v5's camp, turned 30 degrees and scaled 1.2, placed by the check; then its east members taken out (a hole in
// the level's coordinates, each member a single prop with its height, turn, scale and tilt) and the rest split too.
// Each time the check places the same markers (noun, team, place, turn, scale) as the group did.
func TestSplitLossless(t *testing.T) {
	if gameCopy() == "" {
		t.Skip("no game files")
	}
	e := newEnv(t, true)
	doc := map[string]any{}
	json.Unmarshal([]byte(e.text(t)), &doc)
	at, pos, rot, scale := [2]float64{-87.5, -52.6}, [2]float64{-140, -110}, 30.0, 1.2
	group := map[string]any{"level": "SM_Map_v5", "sets": []any{"D_Rocks", "D_Trees", "D_Foliage", "D_IvyNFlowers"},
		"at": []any{at[0], at[1]}, "r": 12.0}
	doc["props"] = []any{map[string]any{"group": group, "pos": []any{pos[0], pos[1]}, "rot": rot, "scale": scale}}
	check := func() []placedMarker {
		t.Helper()
		var r struct {
			Exit   int            `json:"exit"`
			Placed []placedMarker `json:"placed"`
			Report struct {
				OK       bool  `json:"ok"`
				Findings []any `json:"findings"`
			} `json:"report"`
		}
		if code := e.call("POST", "/api/check/CM_Ring", doc, &r); code != 200 || r.Exit != 0 || !r.Report.OK {
			t.Fatalf("check: %d exit %d %v", code, r.Exit, r.Report.Findings)
		}
		return r.Placed
	}
	sig := func(ps []placedMarker) []string {
		var out []string
		for _, p := range ps {
			if !p.Card {
				out = append(out, fmt.Sprintf("%s %d %.2f %.2f %.2f %.1f %.1f %.1f %.3f", strings.ToLower(p.Noun), p.Team,
					p.Pos[0], p.Pos[1], p.Pos[2], deg1(p.Rot[0]), deg1(p.Rot[1]), deg1(p.Rot[2]), p.Scale))
			}
		}
		sort.Strings(out)
		return out
	}
	// the single prop a member stands as (the page's asProp)
	single := func(p placedMarker) map[string]any {
		s := map[string]any{"noun": strings.TrimSuffix(p.Noun, ".Noun"), "pos": []any{r3(p.Pos[0]), r3(p.Pos[1]), r3(p.Pos[2])},
			"rot": r3(normDeg(p.Rot[2])), "scale": r3(p.Scale)}
		if r3(p.Rot[0]) != 0 || r3(p.Rot[1]) != 0 {
			s["tilt"] = []any{r3(p.Rot[0]), r3(p.Rot[1])}
		}
		return s
	}
	// a point of the map in the group's level coordinates (the page's toLevel)
	toLevel := func(q [2]float64) []any {
		a := -rot * math.Pi / 180
		x, y := (q[0]-pos[0])/scale, (q[1]-pos[1])/scale
		return []any{r3(at[0] + x*math.Cos(a) - y*math.Sin(a)), r3(at[1] + x*math.Sin(a) + y*math.Cos(a))}
	}
	before := check()
	var members []placedMarker
	tilted := 0
	for _, p := range before {
		if p.Path == "props[0]" && p.Team == 1 {
			members = append(members, p)
			if r3(p.Rot[0]) != 0 || r3(p.Rot[1]) != 0 {
				tilted++
			}
		}
	}
	if len(members) < 8 {
		t.Fatalf("the group placed %d members on team 1", len(members))
	}
	t.Logf("%d members on team 1, %d of them tilted", len(members), tilted)
	xs := make([]float64, len(members))
	for i, p := range members {
		xs[i] = p.Pos[0]
	}
	sort.Float64s(xs)
	box := [4]float64{xs[len(xs)/2] - 0.01, -140, -100, -80} // the map's rectangle over the east half
	var hole []any
	for _, q := range [][2]float64{{box[0], box[1]}, {box[2], box[1]}, {box[2], box[3]}, {box[0], box[3]}} {
		hole = append(hole, toLevel(q))
	}
	group["holes"] = []any{hole}
	took := 0
	for _, p := range members {
		if p.Pos[0] >= box[0] && p.Pos[0] <= box[2] && p.Pos[1] >= box[1] && p.Pos[1] <= box[3] {
			doc["props"] = append(doc["props"].([]any), single(p))
			took++
		}
	}
	after := check()
	if a, b := sig(after), sig(before); strings.Join(a, "\n") != strings.Join(b, "\n") || took == 0 || took == len(members) {
		t.Errorf("split %d of %d: %d markers, want the group's %d the same; differ:\n%s", took, len(members), len(a),
			len(b), strings.Join(differ(a, b), "\n"))
	}
	// the rest split too: no group left
	rest := doc["props"].([]any)[1:]
	for _, p := range after {
		if p.Path == "props[0]" && p.Team == 1 {
			rest = append(rest, single(p))
		}
	}
	doc["props"] = rest
	if a, b := sig(check()), sig(before); strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Errorf("split whole: %d markers, want the group's %d the same", len(a), len(b))
	}
}

// TestSplitOnce is the page's Split of a group that stands once ("mirror": false, as mapc import --dress writes a
// level's dressing), through the API with the game files (skips without): SM_Map_v5's dressing within 30 m of its
// centre, on both halves, where the level has it (team 0 only); its members east of the centre taken out (a hole, each
// a single prop with "mirror": false), then the rest. Each time the check places the same markers, all once.
func TestSplitOnce(t *testing.T) {
	if gameCopy() == "" {
		t.Skip("no game files")
	}
	e := newEnv(t, true)
	doc := map[string]any{}
	json.Unmarshal([]byte(e.text(t)), &doc)
	group := map[string]any{"level": "SM_Map_v5", "sets": []any{"D_Rocks", "D_Objects", "D_Foliage", "D_Trees",
		"D_IvyNFlowers"}, "at": []any{3.0, 3.0}, "r": 30.0}
	doc["props"] = []any{map[string]any{"group": group, "pos": []any{3.0, 3.0}, "rot": 0, "scale": 1, "mirror": false}}
	check := func() []placedMarker {
		t.Helper()
		var r struct {
			Exit   int            `json:"exit"`
			Placed []placedMarker `json:"placed"`
			Report struct {
				OK       bool  `json:"ok"`
				Findings []any `json:"findings"`
			} `json:"report"`
		}
		if code := e.call("POST", "/api/check/CM_Ring", doc, &r); code != 200 || r.Exit != 0 || !r.Report.OK {
			t.Fatalf("check: %d exit %d %v", code, r.Exit, r.Report.Findings)
		}
		return r.Placed
	}
	sig := func(ps []placedMarker) []string {
		var out []string
		for _, p := range ps {
			out = append(out, fmt.Sprintf("%s %d %.2f %.2f %.2f %.1f %.1f %.1f %.3f", strings.ToLower(p.Noun), p.Team,
				p.Pos[0], p.Pos[1], p.Pos[2], deg1(p.Rot[0]), deg1(p.Rot[1]), deg1(p.Rot[2]), p.Scale))
		}
		sort.Strings(out)
		return out
	}
	single := func(p placedMarker) map[string]any { // the page's asProp for a member of a group that stands once
		s := map[string]any{"noun": strings.TrimSuffix(p.Noun, ".Noun"), "pos": []any{r3(p.Pos[0]), r3(p.Pos[1]), r3(p.Pos[2])},
			"rot": r3(normDeg(p.Rot[2])), "scale": r3(p.Scale), "mirror": false}
		if r3(p.Rot[0]) != 0 || r3(p.Rot[1]) != 0 {
			s["tilt"] = []any{r3(p.Rot[0]), r3(p.Rot[1])}
		}
		return s
	}
	before := check()
	east, half2 := 0, 0
	for _, p := range before {
		if p.Team != 0 {
			t.Fatalf("a member of the group that stands once placed for team %d: %+v", p.Team, p)
		}
		if p.Pos[0] > 3 {
			east++
		}
		if p.Pos[0]+p.Pos[1] > 0 {
			half2++
		}
	}
	if len(before) < 8 || east == 0 || east == len(before) || half2 == 0 {
		t.Fatalf("the group placed %d markers, %d east of x 3, %d on team 2's half", len(before), east, half2)
	}
	group["holes"] = []any{[]any{[]any{3.0, -40.0}, []any{40.0, -40.0}, []any{40.0, 40.0}, []any{3.0, 40.0}}}
	for _, p := range before {
		if p.Pos[0] > 3 {
			doc["props"] = append(doc["props"].([]any), single(p))
		}
	}
	if a, b := sig(check()), sig(before); strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Errorf("split %d of %d: differ:\n%s", east, len(before), strings.Join(differ(a, b), "\n"))
	}
	rest := doc["props"].([]any)[1:]
	for _, p := range before {
		if p.Pos[0] <= 3 {
			rest = append(rest, single(p))
		}
	}
	doc["props"] = rest
	if a, b := sig(check()), sig(before); strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Errorf("split whole: %d markers, want the group's %d the same", len(a), len(b))
	}
	t.Logf("%d markers, %d on team 2's half, all once", len(before), half2)
}

// TestGrassPreview checks what the 3-D view lays its grass blades over: the check reply's preview.bushes for CM_Ring,
// each bush of team 1 with its mirror of team 2 (the same shape, every point negated); with the game files, the blade
// texture as a PNG through the texture pictures route.
func TestGrassPreview(t *testing.T) {
	e := newEnv(t, gameCopy() != "")
	doc, _, _ := e.open("CM_Ring")
	type bush struct {
		Name, ID    string
		Shape, Team int
		Outline     [][2]float64
	}
	var r struct {
		Exit    int
		Preview *struct{ Bushes []bush }
	}
	if code := e.call("POST", "/api/check/CM_Ring", doc, &r); code != 200 || r.Exit != 0 || r.Preview == nil {
		t.Fatalf("check: %d exit %d", code, r.Exit)
	}
	bs := r.Preview.Bushes
	pairs := 0
	for _, a := range bs {
		if a.Team != 1 {
			continue
		}
		for _, b := range bs {
			if b.Team != 2 || b.Shape != a.Shape || len(b.Outline) != len(a.Outline) || len(a.Outline) < 3 {
				continue
			}
			same := true
			for i := range a.Outline {
				same = same && math.Abs(a.Outline[i][0]+b.Outline[i][0]) < 1e-6 && math.Abs(a.Outline[i][1]+b.Outline[i][1]) < 1e-6
			}
			if same {
				pairs++
			}
		}
	}
	if len(bs) < 4 || pairs*2 != len(bs) {
		t.Errorf("%d bushes, %d mirrored pairs: %+v", len(bs), pairs, bs)
	}
	if gameCopy() == "" {
		return
	}
	var pics struct {
		OK       bool `json:"ok"`
		Textures []struct {
			URL string `json:"url"`
		} `json:"textures"`
	}
	const blades = "environments/anshavar/textures/Foliage_GrassBlades2_D.dds"
	if code := e.call("POST", "/api/texture-pictures", map[string]any{"paths": []string{blades}, "max": 128}, &pics); code != 200 ||
		!pics.OK || len(pics.Textures) != 1 || pics.Textures[0].URL == "" {
		t.Fatalf("the blade texture: %d %+v", code, pics)
	}
	res, err := http.Get(e.srv.URL + pics.Textures[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil || img.Bounds().Dx() != 128 || img.Bounds().Dy() != 64 {
		t.Errorf("the blade texture's picture: %v %v", err, img)
	}
}
