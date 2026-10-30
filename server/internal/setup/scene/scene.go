package scene

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/bmdl"
	"dawngate/server/internal/setup/depot"
	"dawngate/server/internal/setup/mapc"
)

// Scene is a scene list (<name>.scene.json): the objects a level or a map package places, each with the models
// drawn for it, and the models' exports. Positions are the game's (x east, y north, z up, metres); rotations are
// the markers' rotDegrees (x, y, z), applied z, then y, then x [assumed order; the shipped markers turn almost only
// about z].
type Scene struct {
	Map     string               `json:"map"`
	Source  string               `json:"source"`          // "level" (a level of the copy) or "package" (a map package)
	Level   string               `json:"level,omitempty"` // a package's: the compiled Level its objects come from
	Size    float64              `json:"size,omitempty"`
	Ground  string               `json:"ground,omitempty"` // a package's generated ground (.gltf, relative)
	Objects []Object             `json:"objects"`
	Models  map[string]ModelInfo `json:"models"` // by the model's path in the copy
	Missing []string             `json:"missing,omitempty"`
}

// Object is one placed thing: a marker of a level, or a map package's derived object with the shipped models a
// level of the copy draws at the same kind of object. A package's object is keyed like preview.json's (kind, team,
// name), so a viewer can move its parts with the object.
type Object struct {
	Kind      string     `json:"kind"`
	Team      int        `json:"team,omitempty"`
	Name      string     `json:"name"`
	Lane      int        `json:"lane,omitempty"`
	Noun      string     `json:"noun,omitempty"`
	Markerset string     `json:"markerset,omitempty"`
	Pos       [3]float64 `json:"pos"`
	At        string     `json:"at,omitempty"` // "spawns": the object stands at the mean of its team's spawns
	Hidden    bool       `json:"hidden,omitempty"`
	Parts     []Part     `json:"parts,omitempty"`
}

// Part is one model drawn for an object, at the object's position plus Offset.
type Part struct {
	Model  string     `json:"model"` // the model's path in the copy (a key of Scene.Models)
	Noun   string     `json:"noun"`
	Offset [3]float64 `json:"offset"`
	Rot    [3]float64 `json:"rot"`
	Scale  float64    `json:"scale"`
}

// Assets reads AssetData instances through the depot, so an added archive's assets win as in the game.
type Assets struct {
	D     *depot.Depot
	Types *assetdata.Types
	nouns map[string]*Noun
}

// NewAssets uses the embedded type tables.
func NewAssets(d *depot.Depot) (*Assets, error) {
	ts, err := assetdata.Embedded()
	if err != nil {
		return nil, err
	}
	return &Assets{D: d, Types: ts, nouns: map[string]*Noun{}}, nil
}

// Get reads and parses an instance ("SM_Map_v5", "Level").
func (a *Assets) Get(class, name string) (*assetdata.Object, error) {
	t := a.Types.Type(class)
	if t == nil {
		return nil, fmt.Errorf("scene: no type %s", class)
	}
	b, err := a.D.Read("AssetData/Binary/" + name + "." + class + ".bin")
	if err != nil {
		return nil, err
	}
	obj, _, err := a.Types.Parse(t, b, nil)
	if err != nil {
		return nil, fmt.Errorf("scene: %s.%s: %v", name, class, err)
	}
	return obj, nil
}

// Noun is what a viewer needs of a Noun.
type Noun struct {
	Name          string
	Model         string // modelPath; "" for a noun without graphics
	GraphicsScale float64
	Type          string
}

// Noun reads a noun ("x" or "x.Noun"); nil when the copy has none.
func (a *Assets) Noun(name string) *Noun {
	name = strings.TrimSuffix(name, ".Noun")
	key := strings.ToLower(name)
	if n, ok := a.nouns[key]; ok {
		return n
	}
	var n *Noun
	if obj, err := a.Get("Noun", name); err == nil {
		n = &Noun{Name: name, GraphicsScale: 1}
		if v, ok := obj.Get("modelPath"); ok {
			n.Model, _ = v.(string)
		}
		if v, ok := obj.Get("graphicsScale"); ok {
			if f, ok := v.(float64); ok && f > 0 {
				n.GraphicsScale = f
			}
		}
		if v, ok := obj.Get("nounType"); ok {
			n.Type = fmt.Sprint(v)
		}
	}
	a.nouns[key] = n
	return n
}

// Marker is a level's marker.
type Marker struct {
	Name, Noun, Markerset string
	Pos, Rot              [3]float64
	Scale                 float64
	Visible               bool
}

func vec3(v any) [3]float64 {
	var out [3]float64
	if l, ok := v.([]any); ok {
		for k := 0; k < 3 && k < len(l); k++ {
			out[k], _ = l[k].(float64)
		}
	}
	return out
}

// Markers lists a level's markers, markerset by markerset in the Level's order.
func (a *Assets) Markers(level string) ([]Marker, error) {
	lv, err := a.Get("Level", level)
	if err != nil {
		return nil, err
	}
	sets, _ := lv.Get("markersets")
	list, _ := sets.([]any)
	var out []Marker
	for _, s := range list {
		so, ok := s.(*assetdata.Object)
		if !ok {
			continue
		}
		v, _ := so.Get("markersetAsset")
		name, _ := v.(string)
		name = strings.TrimSuffix(name, ".Markerset")
		if name == "" {
			continue
		}
		ms, err := a.Get("Markerset", name)
		if err != nil {
			continue // a level may name a markerset no archive holds
		}
		mv, _ := ms.Get("markers")
		ml, _ := mv.([]any)
		for _, x := range ml {
			o, ok := x.(*assetdata.Object)
			if !ok {
				continue
			}
			mk := Marker{Markerset: name, Scale: 1, Visible: true}
			if v, ok := o.Get("markerName"); ok {
				mk.Name, _ = v.(string)
			}
			if v, ok := o.Get("nounDef"); ok {
				mk.Noun, _ = v.(string)
			}
			if v, ok := o.Get("pos"); ok {
				mk.Pos = vec3(v)
			}
			if v, ok := o.Get("rotDegrees"); ok {
				mk.Rot = vec3(v)
			}
			if v, ok := o.Get("scale"); ok {
				if f, ok := v.(float64); ok && f > 0 {
					mk.Scale = f
				}
			}
			if v, ok := o.Get("visible"); ok {
				if b, ok := v.(bool); ok {
					mk.Visible = b
				}
			}
			if v, ok := o.Get("onlyShadows"); ok { // casts a shadow, draws nothing
				if b, ok := v.(bool); ok && b {
					mk.Visible = false
				}
			}
			out = append(out, mk)
		}
	}
	return out, nil
}

// part is a marker's model part, or nil when its noun draws nothing the copy has.
func (a *Assets) part(noun string, offset, rot [3]float64, scale float64) *Part {
	n := a.Noun(noun)
	if n == nil || n.Model == "" || !a.D.Has(n.Model) {
		return nil
	}
	return &Part{Model: depot.Key(n.Model), Noun: n.Name, Offset: offset, Rot: rot, Scale: scale * n.GraphicsScale}
}

// LevelScene lists a level's markers with their models.
func (a *Assets) LevelScene(level string) (*Scene, error) {
	ms, err := a.Markers(level)
	if err != nil {
		return nil, err
	}
	sc := &Scene{Map: level, Source: "level", Models: map[string]ModelInfo{}}
	if lv, err := a.Get("Level", level); err == nil {
		if v, ok := lv.Get("levelSize"); ok {
			if l, ok := v.([]any); ok && len(l) > 0 {
				sc.Size, _ = l[0].(float64)
			}
		}
	}
	for _, m := range ms {
		o := Object{Kind: "marker", Name: m.Name, Noun: strings.TrimSuffix(m.Noun, ".Noun"), Markerset: m.Markerset,
			Pos: m.Pos, Hidden: !m.Visible}
		if p := a.part(m.Noun, [3]float64{}, m.Rot, m.Scale); p != nil {
			o.Parts = []Part{*p}
		}
		sc.Objects = append(sc.Objects, o)
	}
	return sc, nil
}

// ---------------------------------------------------------------- a package's objects

// A group says which of a template level's markers stand for a kind of derived object: the anchor (a marker of the
// object's team, or the mean of several) and the dressing around it that is drawn with the object. Names take %T
// (Mortal, Spirit), %t (mortal, spirit), %K (Outer, Middle, Inner), %L (the Core's letter), %S (Top, Bottom).
type group struct {
	kind     string
	anchor   string   // the anchor marker's noun; "mean:" + noun for the mean of the team's markers of that noun
	at       string   // Object.At
	nouns    []string // the dressing's nouns, taken within radius of the anchor
	radius   float64
	creature string // a noun spawned at the anchor at play time (not a marker): drawn at the anchor's rotation
}

// groups are SM_Map_v5's [data: its markersets]: the Guardian stands in its bath at the titan spawn point; a Binding
// is the gameplay noun (scale 1.3) over the darius_tower_binding graphic; a Core is its nub over a hole; the spawn
// platform is the dais with the Locus sculptures around the five spawn points (the Locus marker itself draws
// nothing); the shop is its two item shops with their archons and decor; a lane's minion spawner is the
// darius_spawner_well beside its spawn point. A gold well draws nothing (SM_Map_v5's gold markers have no model).
var groups = []group{
	{kind: "guardian", anchor: "TitanSpawnPoint", nouns: []string{"darius_titan_bath_%t"}, radius: 3,
		creature: "Guardian%T_v5"},
	{kind: "binding", anchor: "Binding_%T_%K", nouns: []string{"Binding_%T_%K", "darius_tower_binding"}, radius: 2},
	{kind: "core", anchor: "nub_%t_v5_%L", nouns: []string{"nub_%t_v5_%L", "darius_tower_binding_hole"}, radius: 2},
	{kind: "spawns", anchor: "mean:SpawnPoint_Team%n", at: "spawns", radius: 14,
		nouns: []string{"darius_base_dais", "darius_base_loci_sculpture", "darius_base_shrinewall_01"}},
	{kind: "shop", anchor: "mean:itemshoppe_base", radius: 32,
		nouns: []string{"itemshoppe_base", "item_shoppe_decor_01", "itemshoppe_archon_%T"}},
	{kind: "spawner", anchor: "SpawnPoint_v5_%T_%S", nouns: []string{"darius_spawner_well"}, radius: 10},
}

// TemplateLevel is the level whose markers the groups are read from.
const TemplateLevel = "SM_Map_v5"

var teamNames = [3]string{"", "Mortal", "Spirit"}

func expand(s string, team int, o mapc.PreviewObject) string {
	r := strings.NewReplacer("%T", teamNames[team], "%t", strings.ToLower(teamNames[team]),
		"%n", fmt.Sprint(team), "%K", tierOf(o.Name), "%L", letterOf(o.Name), "%S", map[int]string{1: "Top", 2: "Bottom"}[o.Lane])
	return r.Replace(s)
}

func tierOf(name string) string {
	for _, t := range mapc.Tiers {
		if strings.HasSuffix(name, t) {
			return t
		}
	}
	return ""
}

func letterOf(name string) string {
	if strings.HasPrefix(name, "Core ") && len(name) == 6 {
		return name[5:]
	}
	return ""
}

// onSide: team 1's half is x + y < 0; a lane's side is Top (1) for y > x.
func onSide(p [3]float64, team, lane int) bool {
	if (team == 1) != (p[0]+p[1] < 0) {
		return false
	}
	if lane == 1 {
		return p[1] > p[0]
	}
	if lane == 2 {
		return p[1] < p[0]
	}
	return true
}

// anchorOf finds a group's anchor among the template's markers.
func anchorOf(ms []Marker, g group, team int, o mapc.PreviewObject, nearTo [2]float64) (Marker, bool) {
	spec := g.anchor
	mean := strings.HasPrefix(spec, "mean:")
	noun := strings.ToLower(expand(strings.TrimPrefix(spec, "mean:"), team, o))
	lane := 0
	if g.kind == "binding" {
		lane = o.Lane
	}
	var found []Marker
	for _, m := range ms {
		if strings.ToLower(strings.TrimSuffix(m.Noun, ".Noun")) == noun && onSide(m.Pos, team, lane) {
			found = append(found, m)
		}
	}
	if len(found) == 0 {
		return Marker{}, false
	}
	if mean {
		var c Marker
		c.Rot = found[0].Rot
		for _, m := range found {
			for k := 0; k < 3; k++ {
				c.Pos[k] += m.Pos[k] / float64(len(found))
			}
		}
		return c, true
	}
	// several: the one nearest the object's own place (wells)
	sort.SliceStable(found, func(i, j int) bool {
		return dist2(found[i].Pos, nearTo) < dist2(found[j].Pos, nearTo)
	})
	return found[0], true
}

func dist2(p [3]float64, q [2]float64) float64 {
	return (p[0]-q[0])*(p[0]-q[0]) + (p[1]-q[1])*(p[1]-q[1])
}

// PackageScene lists a compiled package's derived objects with the template level's models for each kind
// (Groups), and adds a "spawns" object per team for the spawn platform.
func (a *Assets) PackageScene(cm *mapc.Compiled) (*Scene, error) {
	ms, err := a.Markers(TemplateLevel)
	if err != nil {
		return nil, fmt.Errorf("scene: the template level %s: %v", TemplateLevel, err)
	}
	sc := &Scene{Map: cm.Map.Name, Source: "package", Size: cm.Map.Size, Models: map[string]ModelInfo{}}
	objs := cm.Objects()
	for t := range cm.Layout.Teams {
		c := cm.Layout.Teams[t].SpawnCentre()
		objs = append(objs, mapc.PreviewObject{Kind: "spawns", Team: t + 1, Name: "spawn platform", Pos: c})
	}
	byKind := map[string]group{}
	for _, g := range groups {
		byKind[g.kind] = g
	}
	for _, o := range objs {
		obj := Object{Kind: o.Kind, Team: o.Team, Name: o.Name, Lane: o.Lane, Pos: [3]float64{o.Pos[0], o.Pos[1], 0}}
		g, ok := byKind[o.Kind]
		if !ok {
			sc.Objects = append(sc.Objects, obj)
			continue
		}
		obj.At = g.at
		an, ok := anchorOf(ms, g, o.Team, o, o.Pos)
		if !ok {
			sc.Missing = append(sc.Missing, fmt.Sprintf("%s team %d %s: no %s in %s", o.Kind, o.Team, o.Name,
				expand(g.anchor, o.Team, o), TemplateLevel))
			sc.Objects = append(sc.Objects, obj)
			continue
		}
		if g.creature != "" {
			if p := a.part(expand(g.creature, o.Team, o), [3]float64{0, 0, an.Pos[2]}, an.Rot, 1); p != nil {
				obj.Parts = append(obj.Parts, *p)
			}
		}
		want := map[string]bool{}
		for _, n := range g.nouns {
			want[strings.ToLower(expand(n, o.Team, o))] = true
		}
		for _, m := range ms {
			if !want[strings.ToLower(strings.TrimSuffix(m.Noun, ".Noun"))] ||
				dist2(m.Pos, [2]float64{an.Pos[0], an.Pos[1]}) > g.radius*g.radius {
				continue
			}
			off := [3]float64{m.Pos[0] - an.Pos[0], m.Pos[1] - an.Pos[1], m.Pos[2]}
			if p := a.part(m.Noun, off, m.Rot, m.Scale); p != nil {
				obj.Parts = append(obj.Parts, *p)
			}
		}
		sc.Objects = append(sc.Objects, obj)
	}
	return sc, nil
}

// ExportModels exports every model the scene's parts name (once each) and records them in Models.
func (sc *Scene) ExportModels(x *Exporter) {
	seen := map[string]bool{}
	for _, o := range sc.Objects {
		for _, p := range o.Parts {
			if seen[p.Model] {
				continue
			}
			seen[p.Model] = true
			info, err := x.Model(p.Model)
			if err != nil {
				sc.Missing = append(sc.Missing, fmt.Sprintf("%s: %v", p.Model, err))
				continue
			}
			sc.Models[p.Model] = info
		}
	}
}

// ---------------------------------------------------------------- a package's ground

// GroundFiles makes a compiled package's ground as the compiler does (the model and its textures, tiled or baked,
// at the paths the model names) from the copy's files.
func GroundFiles(cm *mapc.Compiled, d *depot.Depot) (model []byte, files map[string][]byte, err error) {
	gf, _, err := cm.GroundFiles(DepotFiles(d))
	if err != nil {
		return nil, nil, err
	}
	e := mapc.EntriesFor(cm.Map.Name)
	files = map[string][]byte{}
	for _, f := range gf {
		if f.Name == e.Model {
			model = f.Data
		} else {
			files[depot.Key(f.Name)] = f.Data
		}
	}
	return model, files, nil
}

// DepotFiles reads the shipped files a map names through a copy's depot: a path from the archive the map names (the
// depot's own answer must come from that archive).
func DepotFiles(d *depot.Depot) mapc.ShippedFiles {
	return func(archive, path string) ([]byte, error) {
		if from := filepath.Base(d.Archive(path)); !strings.EqualFold(from, archive) {
			return nil, fmt.Errorf("%s: read from %q, not from %s", path, from, archive)
		}
		return d.Read(path)
	}
}

// ExportGround writes a compiled package's ground as <name>_ground.gltf (and .bin, textures) into the exporter's
// folder and returns its info.
func ExportGround(cm *mapc.Compiled, d *depot.Depot, x *Exporter) (ModelInfo, error) {
	model, files, err := GroundFiles(cm, d)
	if err != nil {
		return ModelInfo{}, err
	}
	m, err := bmdl.Decode(model)
	if err != nil {
		return ModelInfo{}, err
	}
	gx := *x
	gx.textures = nil
	gx.Read = func(path string) ([]byte, error) {
		if b, ok := files[depot.Key(path)]; ok {
			return b, nil
		}
		return d.Read(path)
	}
	// the tag holds the textures too: a baked ground's model stays the same when only its painting changes
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tag := digest(model)
	for _, k := range keys {
		tag += "|" + k + ":" + digest(files[k])
	}
	if len(keys) > 0 {
		tag = digest([]byte(tag))
	}
	return gx.Generated(cm.Map.Name+"_ground", tag, m)
}
