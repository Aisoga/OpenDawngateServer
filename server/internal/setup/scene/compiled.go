package scene

import (
	"fmt"
	"strings"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/mapc"
)

// ---------------------------------------------------------------- a package's objects from its compiled Level

// attachRadius is how far from a derived object (preview.json's) a compiled marker may stand and still be drawn as a
// part of that object, so a viewer moves it with the object while its place is edited. The values are the groups'
// (PackageScene); a marker farther from every object stands alone.
var attachRadius = map[string]float64{
	"guardian": 3, "binding": 2, "core": 2, "spawns": 14, "shop": 32, "spawner": 10, "well": 2, "locus": 2,
}

// attachByNoun names markers the compiler places relative to an object farther away than attachRadius: the shop's
// archons move with the spawn points' centre (mapc's design markerset), so they belong to the team's "spawns".
var attachByNoun = map[string]string{"itemshoppe_archon_": "spawns"}

// attachTo is the index of the object (objs) a marker with a model is drawn with, or -1.
func attachTo(m Marker, objs []mapc.PreviewObject) int {
	noun := strings.ToLower(m.Noun)
	kind := ""
	for prefix, k := range attachByNoun {
		if strings.HasPrefix(noun, prefix) {
			kind = k
		}
	}
	best, bestD := -1, 0.0
	for i, o := range objs {
		d := dist2(m.Pos, o.Pos)
		if kind != "" {
			if o.Kind != kind {
				continue
			}
		} else if r, ok := attachRadius[o.Kind]; !ok || d > r*r {
			continue
		}
		if best < 0 || d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// onFloor are the noun types the game spawns as game objects (the networked towers, creatures and interactables, and
// the titans): the simulation puts them on the ground (nGameObject.GetGroundPosition asks the navigation mesh), so
// they stand on a compiled map's floor (z 0) whatever height the marker copied from a shipped level says (the shop
// archons keep SM_Map_v5's z of about 5 m, yet stand on the ground in the game). Dressing keeps its marker's height.
var onFloor = map[string]bool{"tower": true, "creature": true, "interactable": true, "titan": true}

// floorZ is the height a marker of the noun is drawn at: 0 for a game object, else the marker's.
func (a *Assets) floorZ(noun string, z float64) float64 {
	if n := a.Noun(noun); n != nil && onFloor[strings.ToLower(n.Type)] {
		return 0
	}
	return z
}

// creatures are the units the game spawns at play time where an object's marker stands (not markers themselves):
// the Guardian at its titan spawn point (titanNodeDef names it).
var creatures = map[string]struct{ noun, at string }{
	"guardian": {"Guardian%T_v5", "TitanSpawnPoint"},
}

// num is a number member's value: parsed assets hold float64, the compiler's written ones float32 too.
func num(v any) float64 {
	switch x := v.(type) {
	case float32:
		return float64(x)
	case float64:
		return x
	}
	return 0
}

func vec3f(v any) [3]float64 {
	var out [3]float64
	if l, ok := v.([]any); ok {
		for k := 0; k < 3 && k < len(l); k++ {
			out[k] = num(l[k])
		}
	}
	return out
}

// markerOf reads one marker of a markerset as Markers does.
func markerOf(o *assetdata.Object, set string) Marker {
	mk := Marker{Markerset: set, Scale: 1, Visible: true}
	if v, ok := o.Get("markerName"); ok {
		mk.Name, _ = v.(string)
	}
	if v, ok := o.Get("nounDef"); ok {
		mk.Noun, _ = v.(string)
	}
	if v, ok := o.Get("pos"); ok {
		mk.Pos = vec3f(v)
	}
	if v, ok := o.Get("rotDegrees"); ok {
		mk.Rot = vec3f(v)
	}
	if v, ok := o.Get("scale"); ok {
		if f := num(v); f > 0 {
			mk.Scale = f
		}
	}
	if v, ok := o.Get("visible"); ok {
		if b, ok := v.(bool); ok {
			mk.Visible = b
		}
	}
	if v, ok := o.Get("onlyShadows"); ok {
		if b, ok := v.(bool); ok && b {
			mk.Visible = false
		}
	}
	return mk
}

// CompiledMarkers lists the markers of a compiled package's Level (ma from cm.Assets), markerset by markerset in the
// Level's order. A markerset the package doesn't write is read from the copy (a Level may list a shipped one).
func (a *Assets) CompiledMarkers(cm *mapc.Compiled, ma *mapc.MapAssets) ([]Marker, error) {
	lv := ma.Values[cm.Map.Name+".Level"]
	if lv == nil {
		return nil, fmt.Errorf("scene: the compiled assets have no %s.Level", cm.Map.Name)
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
		ms := ma.Values[name+".Markerset"]
		if ms == nil {
			var err error
			if ms, err = a.Get("Markerset", name); err != nil {
				continue
			}
		}
		mv, _ := ms.Get("markers")
		ml, _ := mv.([]any)
		for _, x := range ml {
			if o, ok := x.(*assetdata.Object); ok {
				out = append(out, markerOf(o, name))
			}
		}
	}
	return out, nil
}

// CompiledScene lists what a compiled package's Level places (ma from cm.Assets, the markers `mapc build` writes into
// the map's archive), so a viewer shows what the game will place. Each marker that draws a model is a part of the
// derived object (keyed as preview.json keys it, plus a "spawns" object per team) it stands at (attachRadius), the
// other markers are objects of their own (kind "marker"); the Guardian, spawned at play time, is drawn at its titan
// spawn point. The map's terrain marker is left out: the ground is the package's generated one (ExportGround).
func (a *Assets) CompiledScene(cm *mapc.Compiled, ma *mapc.MapAssets) (*Scene, error) {
	ms, err := a.CompiledMarkers(cm, ma)
	if err != nil {
		return nil, err
	}
	sc := &Scene{Map: cm.Map.Name, Source: "package", Level: cm.Map.Name + ".Level", Size: cm.Map.Size,
		Models: map[string]ModelInfo{}}
	objs := cm.Objects()
	for t := range cm.Layout.Teams {
		objs = append(objs, mapc.PreviewObject{Kind: "spawns", Team: t + 1, Name: "spawn platform",
			Pos: cm.Layout.Teams[t].SpawnCentre()})
	}
	out := make([]Object, len(objs))
	for i, o := range objs {
		out[i] = Object{Kind: o.Kind, Team: o.Team, Name: o.Name, Lane: o.Lane, Pos: [3]float64{o.Pos[0], o.Pos[1], 0}}
		if o.Kind == "spawns" {
			out[i].At = "spawns"
		}
	}
	terrain := strings.ToLower(mapc.TerrainNoun(cm.Map.Name))
	props := mapc.PropsMarkerset(cm.Map.Name)
	var alone []Object
	for _, m := range ms {
		if strings.ToLower(m.Noun) == terrain {
			continue
		}
		p := a.part(m.Noun, [3]float64{0, 0, a.floorZ(m.Noun, m.Pos[2])}, m.Rot, m.Scale)
		best := -1
		// a prop is placed by its own map.json entry, never with an object: it stays a marker of its own
		if p != nil && m.Visible && !strings.EqualFold(m.Markerset, props) {
			best = attachTo(m, objs)
		}
		if best >= 0 {
			p.Offset = [3]float64{m.Pos[0] - objs[best].Pos[0], m.Pos[1] - objs[best].Pos[1], p.Offset[2]}
			out[best].Parts = append(out[best].Parts, *p)
			continue
		}
		o := Object{Kind: "marker", Name: m.Name, Noun: strings.TrimSuffix(m.Noun, ".Noun"), Markerset: m.Markerset,
			Pos: [3]float64{m.Pos[0], m.Pos[1], 0}, Hidden: !m.Visible}
		if p != nil {
			o.Parts = []Part{*p}
		}
		alone = append(alone, o)
	}
	for i, o := range objs {
		c, ok := creatures[o.Kind]
		if !ok {
			continue
		}
		var at *Marker
		for k := range ms {
			m := &ms[k]
			if strings.EqualFold(strings.TrimSuffix(m.Noun, ".Noun"), c.at) &&
				(at == nil || dist2(m.Pos, o.Pos) < dist2(at.Pos, o.Pos)) {
				at = m
			}
		}
		var z float64
		var rot [3]float64
		if at != nil {
			z, rot = at.Pos[2], at.Rot
		}
		noun := expand(c.noun, o.Team, o)
		if p := a.part(noun, [3]float64{0, 0, a.floorZ(noun, z)}, rot, 1); p != nil {
			out[i].Parts = append([]Part{*p}, out[i].Parts...)
		} else {
			sc.Missing = append(sc.Missing, fmt.Sprintf("%s team %d: no model for %s", o.Kind, o.Team, noun))
		}
	}
	sc.Objects = append(out, alone...)
	return sc, nil
}
