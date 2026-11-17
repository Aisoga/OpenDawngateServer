package mapc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// FolderName is the maps folder's name: the one beside the release's programs for players and operators, the
// repository's maps\ for developers (no package comes with the code or the release; the tests' packages are in
// server/testdata/maps).
const FolderName = "maps"

// Package is an installed map package: its folder, its map and what offers it.
type Package struct {
	Dir   string
	Map   *Map
	Label Label
}

// Others are the packages' names and tags, but the one at index skip (-1: none), for the uniqueness rules.
func Others(pkgs []Package, skip int) []Other {
	var out []Other
	for i, p := range pkgs {
		if i != skip {
			out = append(out, Other{Name: p.Map.Name, Tag: p.Map.Tag})
		}
	}
	return out
}

// ReadFolder reads the map packages in dir: every sub-folder with a map.json, in the folders' name order. A package
// whose map.json doesn't read, or that fails a rule CheckStructure runs (names, tag, display name, size, the fixed
// set), or whose name or tag an earlier package has, is left out with an error naming it; the geometric rules are
// the build's (Build). A dir that doesn't exist holds no maps (no error).
func ReadFolder(dir string) ([]Package, []error) {
	if dir == "" {
		return nil, nil
	}
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{err}
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	var pkgs []Package
	var errs []error
	for _, e := range ents {
		sub := filepath.Join(dir, e.Name())
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(sub, FileName)); err != nil {
			continue // not a package (a README, a draft's scratch folder)
		}
		m, err := Load(sub)
		if err != nil {
			errs = append(errs, fmt.Errorf("map %s: %v", e.Name(), err))
			continue
		}
		if r := CheckStructure(m, Others(pkgs, -1)); !r.OK {
			first := ""
			for _, f := range r.Findings {
				if f.Severity == Error {
					first = f.Message
					break
				}
			}
			errs = append(errs, fmt.Errorf("map %s (%s): %d rule(s) fail, first: %s", m.Name, sub, countErrors(r), first))
			continue
		}
		pkgs = append(pkgs, Package{Dir: sub, Map: m, Label: LabelOf(m)})
	}
	return pkgs, errs
}

// CheckStructure runs the rules that need no rasterising (Check's first part): enough to offer a map by its label.
func CheckStructure(m *Map, others []Other) *Report {
	return report(m, func(c *checker) { c.structure(others) })
}

// CheckNames runs only the names rules (names.*: the name, the tag, the display name and the prefix, against the
// other installed maps too), the same code as Check's. A step that writes a package under new names (the editor's
// New and Save as my own copy through `mapc check --names`, mapc import) runs it first and writes nothing when it
// fails, so the package's first check can't fail on the names it was given.
func CheckNames(m *Map, others []Other) *Report {
	return report(m, func(c *checker) { c.names(others) })
}

func report(m *Map, run func(c *checker)) *Report {
	c := &checker{m: m}
	run(c)
	r := &Report{Map: m.Name, Findings: c.fs}
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	r.OK = c.errors() == 0
	return r
}
