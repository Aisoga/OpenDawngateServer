package mapc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/snap"
)

// AssetDataSource is the copy's AssetData archive (a path in a copy).
const AssetDataSource = "game/Data/AssetData.snap"

// Shipped is what the copy's AssetData holds that a map may name: the Noun, Level and Markerset names (lower case).
type Shipped struct {
	Nouns, Levels, Markersets map[string]bool
}

// ReadShipped lists the copy's Nouns and Levels.
func ReadShipped(copyDir string) (*Shipped, error) {
	b, err := os.ReadFile(filepath.Join(copyDir, filepath.FromSlash(AssetDataSource)))
	if err != nil {
		return nil, err
	}
	a, err := snap.Parse(b)
	if err != nil {
		return nil, err
	}
	set, err := assetdata.FromSnap(a)
	if err != nil {
		return nil, err
	}
	s := &Shipped{Nouns: map[string]bool{}, Levels: map[string]bool{}, Markersets: map[string]bool{}}
	for _, f := range set.Files("Markerset") {
		s.Markersets[strings.ToLower(f.Name)] = true
	}
	for _, f := range set.Files("Noun") {
		s.Nouns[strings.ToLower(f.Name)] = true
	}
	for _, f := range set.Files("Level") {
		s.Levels[strings.ToLower(f.Name)] = true
	}
	return s, nil
}

// CheckShipped runs the rules that need the copy's catalog: every noun the map names exists (a package holds no game
// data, so this is checked where the map is compiled), and the name is no Level's of the copy. It adds its findings
// to r.
func CheckShipped(m *Map, s *Shipped, r *Report) {
	c := &checker{m: m, fs: r.Findings}
	noun := func(n string) bool { return s.Nouns[strings.ToLower(strings.TrimSuffix(n, ".Noun"))] }
	for k, cp := range m.Camps {
		if cp.Noun != "" && !noun(cp.Noun) {
			c.errorf("nouns.exist", fmt.Sprintf("camps[%d].noun", k), pt(cp.Pos), "the copy has no noun %s", cp.Noun)
		}
	}
	c.checkShippedProps(s) // props.go
	if s.Levels[strings.ToLower(m.Name)] {
		c.errorf("names.shipped", "name", nil, "name %q is a level of the copy", m.Name)
	}
	for _, l := range []struct{ path, v string }{{"environment", m.Environment}, {"tuning", m.Tuning}} {
		if l.v != "" && !s.Levels[strings.ToLower(l.v)] {
			c.errorf("refs."+l.path, l.path, nil, "the copy has no level %s", l.v)
		}
	}
	r.Findings = c.fs
	r.OK = c.errors() == 0
}
