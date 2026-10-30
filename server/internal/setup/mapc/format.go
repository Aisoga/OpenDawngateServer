// Package mapc compiles a map package into the added archive the game loads a new level from (zz_<name>.snap).
//
// A package is a folder with text only: map.json (format 1, Map); the installed maps are a folder of such packages
// (ReadFolder). The author places the gameplay points and draws the layout as shapes for one team's half; the
// compiler mirrors the other half, derives the rest (the Cores' and Bindings' places, the minion spawners, the
// Guardians' holes) and rasterises the shapes into the 512 x 512 layers every level file is made from: the
// line-of-sight map (walls block sight), the walkable mask (chasms are seen through but not walked) and the bushes.
// From those it makes the level's files as MM_Map_v3's pipeline (setup/mapmm) does: the navigation graphs, the fog
// outlines with the bushes', the minimap, the grass map, the tag map and the ground model; the PhysX mesh is the one
// shared floor every map uses (SharedMesh).
//
// Check runs the rule checks (Finding, one rule id each) without the game's files; Build makes the archive from the
// player's copy (the graph and ground templates come from the copy, no game bytes ship); Preview draws the layers. The
// same map gives the same bytes on every platform.
package mapc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"dawngate/server/internal/setup/mapmm/ground"
)

// Format is the map package format this compiler reads.
const Format = 1

// FileName is a package's map file.
const FileName = "map.json"

// Point is a position in the level's plane (metres, x east, y north, the level's centre at the origin).
type Point [2]float64

// Map is a package's map.json. Team 1 (Mortal) is authored: its base in the south-west, its half x + y <= 0; team
// 2 (Spirit) is the point mirror (x, y) -> (-x, -y).
type Map struct {
	Format      int     `json:"format"`
	Name        string  `json:"name"`        // the Level's name; [A-Za-z0-9_], the archive zz_<name>.snap
	Display     string  `json:"display"`     // the create dialog's and the lobby's name, at most 13 characters
	Tag         string  `json:"tag"`         // the custom game's ~#<tag>; [a-z0-9]
	Prefix      string  `json:"prefix"`      // the match browser's Map column prefix
	Size        float64 `json:"size"`        // the levelSize (metres), square, centred on the origin
	Symmetry    string  `json:"symmetry"`    // "point"
	Environment string  `json:"environment"` // a shipped level whose lights and atmosphere the map uses
	Tuning      string  `json:"tuning"`      // a shipped level whose gameplay tuning, item set and tunings it uses
	Lanes       []Lane  `json:"lanes"`
	Base        Base    `json:"base"`
	Wells       []Well  `json:"wells"`
	Camps       []Camp  `json:"camps"`
	Terrain     Terrain `json:"terrain"`
	Ground      Ground  `json:"ground"`
	Props       []Prop  `json:"props"`
	// Dir is the package's folder (Load sets it; the files a map names, such as its ground's pictures, are read from
	// it); "" for a map parsed from bytes alone.
	Dir string `json:"-"`

	// props.go: props spread along the walls; the props' nouns' boxes from the copy (ResolveProps), by lower case
	Scatter    []Scatter `json:"scatter,omitempty"`
	nounBounds map[string]NounBounds
	// props.go: the fords the props place (shallow water planes; the ground dips under them), from the copy
	// (ResolveProps)
	fords []ground.Dip

	// wards.go: the bots' ward spots, team 1's (MAPED4b)
	Wards []Ward `json:"wards,omitempty"`

	// void.go: what the map shows past its ground (optional)
	Void *Void `json:"void,omitempty"`

	// graph.go: the navigation graphs' square size (optional; MAPED-IMPORT-FIT)
	Graph *Graph `json:"graph,omitempty"`
}

// Lane is one lane id's path as team 1's minions walk it, from its base to the enemy's: knots (at least 2) and the
// corridor's width (metres) for shapes that open the lane. Team 2 walks the same path reversed.
type Lane struct {
	ID    int     `json:"id"` // 1 Top, 2 Bottom
	Width float64 `json:"width"`
	Knots []Point `json:"knots"`
}

// Base is team 1's fixed set; team 2's is its mirror.
type Base struct {
	Locus    Point    `json:"locus"`    // the Locus (the team's spawn structure)
	Guardian Point    `json:"guardian"` // the Guardian's titan node
	Shop     Point    `json:"shop"`     // the base zone: the item shop's reach is measured from it
	Spawns   []Point  `json:"spawns"`   // the 5 player spawn points
	Cores    Cores    `json:"cores"`    // "auto" or 5 points (A to E), or MM_Map_v3's 3 (A to C)
	Bindings Bindings `json:"bindings"` // "auto" or per lane id 3 points (Outer, Middle, Inner) or MM's 2 (Middle, Inner)
}

// Cores are "auto" (SM_Map_v5's five around the Guardian) or the five places in the order A to E, or three (A to C:
// MM_Map_v3's set, whose Guardian's shield drops with the third; MAPED5).
type Cores struct {
	Auto   bool
	Points []Point
}

// Bindings are "auto" (at SM_Map_v5's fractions of each lane) or per lane id ("1", "2") the Outer, Middle and Inner
// Binding's places, or the Middle and Inner one's only (MM_Map_v3's two tiers; every lane the same count; MAPED5).
type Bindings struct {
	Auto  bool
	Lanes map[string][]Point
}

// Well is a gold well (a titan node with worker spots): team 1's, mirrored for team 2. Its kind picks the shipped
// noun it is placed as: "well" (or none) SM_Map_v5's spirit well, 18 worker spots about 22 m round it; "mine"
// MM_Map_v3's gold mine, 36 spots 15.5 to 18.2 m round it (WellKinds).
type Well struct {
	Kind string `json:"kind,omitempty"`
	Pos  Point  `json:"pos"`
}

// The well kinds (Well.Kind; none is WellSpirit).
const (
	WellSpirit = "well" // SM_Map_v5's gold_<colour>_side
	WellMine   = "mine" // MM_Map_v3's gold_<colour>_side_MM
)

// WellKinds are the kinds a well may name.
var WellKinds = []string{WellSpirit, WellMine}

// IsMine tells whether the well is placed as MM_Map_v3's mine.
func (w Well) IsMine() bool { return w.Kind == WellMine }

// Camp is a jungle camp: kind camp, buff or parasite, its shipped spawner noun and its place.
type Camp struct {
	Kind string `json:"kind"`
	Noun string `json:"noun"`
	Pos  Point  `json:"pos"`
}

// Terrain is the layout: every cell starts as Default, then the shapes are drawn in order, each with its mirror.
type Terrain struct {
	Default string  `json:"default"` // wall, open or chasm
	Shapes  []Shape `json:"shapes"`
}

// Shape is one layout shape: its type (open: walked and seen through; wall: neither; chasm: seen through, not walked;
// island: seen through, walked by NPCs only, so a well on it is out of the players' reach; bush: grass on the cells,
// which keep their ground) and exactly one geometry: a lane id (its corridor at the lane's width; not for a bush or an
// island), a circle (centre and r), a polygon (at least 3 corners) or a path (at least 2 points) with a width. A chasm
// may hold water (water.go).
type Shape struct {
	Type    string  `json:"type"`
	Lane    int     `json:"lane,omitempty"`
	Circle  *Point  `json:"circle,omitempty"`
	R       float64 `json:"r,omitempty"`
	Polygon []Point `json:"polygon,omitempty"`
	Path    []Point `json:"path,omitempty"`
	Width   float64 `json:"width,omitempty"`
	Water   *Water  `json:"water,omitempty"`
}

// Ground is how the ground model is made: style "tiled" (the heightfield with tiled dirt and grass) or "baked" (its
// texture painted from layers and stamps, groundtex.go's GroundBake).
type Ground struct {
	Style string `json:"style"`
	GroundBake
}

// UnmarshalJSON reads "auto" or a list of points.
func (c *Cores) UnmarshalJSON(b []byte) error {
	if s, ok := autoText(b); ok {
		if s != "auto" {
			return fmt.Errorf(`cores: %q is neither "auto" nor a list of points`, s)
		}
		*c = Cores{Auto: true}
		return nil
	}
	*c = Cores{}
	return strictUnmarshal(b, &c.Points)
}

// MarshalJSON writes "auto" or the points.
func (c Cores) MarshalJSON() ([]byte, error) {
	if c.Auto {
		return []byte(`"auto"`), nil
	}
	return json.Marshal(c.Points)
}

// UnmarshalJSON reads "auto" or an object of lane ids to points.
func (bs *Bindings) UnmarshalJSON(b []byte) error {
	if s, ok := autoText(b); ok {
		if s != "auto" {
			return fmt.Errorf(`bindings: %q is neither "auto" nor an object of lane ids`, s)
		}
		*bs = Bindings{Auto: true}
		return nil
	}
	*bs = Bindings{}
	return strictUnmarshal(b, &bs.Lanes)
}

// MarshalJSON writes "auto" or the lanes' points.
func (bs Bindings) MarshalJSON() ([]byte, error) {
	if bs.Auto {
		return []byte(`"auto"`), nil
	}
	return json.Marshal(bs.Lanes)
}

func autoText(b []byte) (string, bool) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(b, &s) != nil {
		return "", false
	}
	return s, true
}

func strictUnmarshal(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("more than one JSON value")
	}
	return nil
}

// Parse reads a map.json: strict JSON (an unknown member is an error), any format; Check judges the values.
func Parse(b []byte) (*Map, error) {
	var m Map
	if err := strictUnmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Load reads dir/map.json.
func Load(dir string) (*Map, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	m, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, FileName), err)
	}
	m.Dir = dir
	return m, nil
}
