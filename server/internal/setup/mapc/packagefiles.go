package mapc

import "sort"

// PackageFiles are the files of a package besides map.json that the compiler reads: the PNGs its map names (a baked
// ground's layer textures and masks, stamps, the cliff's texture and the picture's tiles; the void backdrop's
// texture), as relative paths with "/", each once, in name order. A name that isn't a clean package path (PackagePath)
// is left out: the rules refuse it, and nothing outside the package is ever named. Map delivery bundles exactly
// these with map.json, so what a server hands out is what its own setup compiled.
func PackageFiles(m *Map) []string {
	seen := map[string]bool{}
	var out []string
	add := func(f string) {
		if f == "" || PackagePath(f, ".png") != nil || seen[f] {
			return
		}
		seen[f] = true
		out = append(out, f)
	}
	if m.Baked() { // the checker and the bake read these only for a baked ground (groundFiles)
		for _, l := range m.Ground.Layers {
			add(l.Texture.File)
			add(l.Image)
		}
		for _, s := range m.Ground.Stamps {
			add(s.Image.File)
		}
		if cl := m.Ground.Cliff; cl != nil {
			add(cl.Texture.File)
		}
		for _, f := range m.Ground.Picture {
			add(f)
		}
	}
	if m.Void != nil && m.Void.Backdrop != nil {
		add(m.Void.Backdrop.Texture.File)
	}
	sort.Strings(out)
	return out
}
