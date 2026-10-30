package mapdist

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"dawngate/server/internal/setup/mapc"
)

// Offer is a map the server offers that it may hand out: a maps folder package's name and tag. The server offers only
// maps with a version (the host copy's archive), since the receiver installs a package only when it compiles to it.
type Offer struct{ Name, Tag string }

// Served is the set a server hands out: the packages of its maps folder that it offers, each read and hashed once at
// the start. A request streams the files again (Package.WriteTo); a package edited since then comes out with another
// hash, which the receiver refuses, until the server is restarted.
type Served struct {
	byTag map[string]served
	names map[string]bool // lower-case map names
	text  []byte
}

type served struct {
	e Entry
	p *Package
}

// NewServed reads the packages of mapsDir that offers name (by map name), within lim (a package over it isn't served).
// The notes are its start-up lines: what it hands out, and each offered package it can't.
func NewServed(mapsDir string, offers []Offer, lim Limits) (*Served, []string) {
	s := &Served{byTag: map[string]served{}, names: map[string]bool{}}
	pkgs, _ := mapc.ReadFolder(mapsDir) // a broken package is the maps folder's own warning
	var notes, shown []string
	for _, o := range offers {
		var dir string
		for _, p := range pkgs {
			if strings.EqualFold(p.Map.Name, o.Name) {
				dir = p.Dir
			}
		}
		if dir == "" {
			continue // not a folder map (MM_Map_v3)
		}
		p, err := ReadPackage(dir, lim)
		var sum [32]byte
		var n int64
		if err == nil {
			sum, n, err = p.Hash()
		}
		if err != nil {
			notes = append(notes, fmt.Sprintf("WARNING: map delivery: %s (%s) isn't handed out: %v", o.Name, dir, err))
			continue
		}
		e := Entry{Tag: strings.ToLower(o.Tag), Name: p.Map.Name, Size: n, SHA256: sum}
		s.byTag[e.Tag] = served{e: e, p: p}
		s.names[strings.ToLower(e.Name)] = true
		shown = append(shown, fmt.Sprintf("%s (~#%s, %s, package %s)", e.Name, e.Tag, Bytes(n), e.Short()))
	}
	var es []Entry
	for _, v := range s.byTag {
		es = append(es, v.e)
	}
	s.text = []byte(FormatManifest(es))
	sort.Strings(shown)
	if len(shown) > 0 {
		notes = append([]string{"map delivery: hands out " + strings.Join(shown, ", ") + " to the players' Start-Clients " +
			"(through the tunnel)"}, notes...)
	}
	return s, notes
}

// Manifest is the manifest's text (FormatManifest).
func (s *Served) Manifest() []byte { return s.text }

// Package is the bundle for a tag: its size and a function that writes it; ok false for a tag not handed out.
func (s *Served) Package(tag string) (size int64, write func(io.Writer) error, ok bool) {
	v, ok := s.byTag[strings.ToLower(tag)]
	if !ok {
		return 0, nil, false
	}
	return v.e.Size, func(w io.Writer) error { _, err := v.p.WriteTo(w); return err }, true
}

// Serves says whether the server hands out the map (its name, any case).
func (s *Served) Serves(name string) bool { return s != nil && s.names[strings.ToLower(name)] }

// Entries are the served maps' manifest entries, by tag.
func (s *Served) Entries() []Entry {
	var es []Entry
	for _, v := range s.byTag {
		es = append(es, v.e)
	}
	sort.Slice(es, func(i, j int) bool { return es[i].Tag < es[j].Tag })
	return es
}

// Bytes is a size for a player: "3.1 MB", "512 KB".
func Bytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", (n+512)>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}
