package mapc

import (
	"fmt"
	"strings"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/dds"
	"dawngate/server/internal/setup/mapc/bake"
	"dawngate/server/internal/setup/mapmm/ground"
	"dawngate/server/internal/setup/snap"
)

// The void (optional): what the map shows past its ground. The levels' ground fills the camera's view, and the client
// clears no colour buffer, so wherever no triangle is drawn the earlier frames stay. A map may instead keep its ground
// to the walkable floor and the first metres past its edge (Cut) and lay a plane under the level (Backdrop), a
// see-through mist layer above that plane (Mist), and set its environment's height fog (Fog). A map without a void
// builds as before, byte for byte.

// Void is the map's void section.
type Void struct {
	Cut      float64       `json:"cut,omitempty"`      // metres past the walkable edge the ground ends (0: the whole square)
	Backdrop *VoidBackdrop `json:"backdrop,omitempty"` // the plane under the level
	Mist     *VoidMist     `json:"mist,omitempty"`     // the mist layer above the backdrop
	Fog      *VoidFog      `json:"fog,omitempty"`      // the environment's height fog, set for this map
}

// VoidBackdrop is a flat plane at height Z (metres, below the floor), ±ground.BackdropHalf metres wide, its texture
// repeated every Scale metres (default ground.BackdropRepeat; along y as the texture's sides are). Lit (the default)
// is the ground's own vertex-lit material: the sun, the shadows and the height fog fall on it, and the mist sees it;
// Unlit is the parasite's unlit material: no shadow, no fog, and a mist over it sees nothing behind.
type VoidBackdrop struct {
	Z       float64    `json:"z"`
	Texture TextureRef `json:"texture"`
	Scale   float64    `json:"scale,omitempty"`
	Unlit   bool       `json:"unlit,omitempty"`
}

// VoidMist is the shipped fog surface (ground.FogFaceMaterial) as a plane at height Z: what lies below it is tinted
// Color (the material's DiffuseTint, each 0-2), more the deeper it lies, fully at Range metres (its FogRange).
type VoidMist struct {
	Z     float64    `json:"z"`
	Range float64    `json:"range"`
	Color [3]float64 `json:"color"`
}

// VoidFog is the environment's height fog for this map: the GraphicsControl's fog_ground_start (Start, z where it
// begins) and fog_ground_end (End, lower, where it is full), and fog_color (each 0-1; nil: the environment's).
type VoidFog struct {
	Start float64     `json:"start"`
	End   float64     `json:"end"`
	Color *[3]float64 `json:"color,omitempty"`
}

// The void's limits.
const (
	MinCut           = 0.5
	MaxCut           = 50.0
	BackdropMinZ     = -150.0
	BackdropMaxZ     = -1.0
	MinMistRange     = 0.5
	MaxMistRange     = 1000.0
	MaxFogHeight     = 1000.0
	MistFordTint     = 0.05 // a ford's water tinted more than this by the mist above it: a warning
	backdropEmissive = 1.0  // the unlit backdrop's EmissiveLevel
)

// BackdropEntry is the archive's path of a map's backdrop texture.
func BackdropEntry(name string) string {
	n := strings.ToLower(name)
	return "environments/community/" + n + "/textures/" + n + "_backdrop_D.dds"
}

func (m *Map) voidCut() float64 {
	if m.Void == nil {
		return 0
	}
	return m.Void.Cut
}

// ---------------------------------------------------------------- the rules

// voidRules runs the void's rules (structure; the fords need the copy, ResolveProps).
func (c *checker) voidRules() {
	v := c.m.Void
	if v == nil {
		return
	}
	if v.Cut != 0 && !(v.Cut >= MinCut && v.Cut <= MaxCut) {
		c.errorf("void.cut", "void.cut", nil, "cut %g: 0 (none) or %g to %g metres", v.Cut, MinCut, MaxCut)
	}
	if v.Cut > 0 && v.Backdrop == nil {
		c.errorf("void.backdrop", "void", nil, "a cut ground needs a backdrop: where nothing is drawn the client "+
			"shows its earlier frames")
	}
	if b := v.Backdrop; b != nil {
		if !(b.Z >= BackdropMinZ && b.Z <= BackdropMaxZ) {
			c.errorf("void.backdrop", "void.backdrop.z", nil, "z %g: %g to %g metres", b.Z, BackdropMinZ, BackdropMaxZ)
		}
		if b.Scale != 0 && !(b.Scale >= 1 && b.Scale <= 2*ground.BackdropHalf) {
			c.errorf("void.backdrop", "void.backdrop.scale", nil, "scale %g: 1 to %g metres", b.Scale,
				2*ground.BackdropHalf)
		}
		c.textureRef("void.backdrop.texture", b.Texture, true)
	}
	if mi := v.Mist; mi != nil {
		switch {
		case !(mi.Z < 0):
			c.errorf("void.mist-floor", "void.mist.z", nil, "z %g: the mist must lie below the walkable floor (z 0)", mi.Z)
		case v.Backdrop != nil && !(mi.Z > v.Backdrop.Z):
			c.errorf("void.mist", "void.mist.z", nil, "z %g: above the backdrop (z %g), or the backdrop hides it", mi.Z,
				v.Backdrop.Z)
		case !(mi.Z >= BackdropMinZ):
			c.errorf("void.mist", "void.mist.z", nil, "z %g: at least %g metres", mi.Z, BackdropMinZ)
		}
		if !(mi.Range >= MinMistRange && mi.Range <= MaxMistRange) {
			c.errorf("void.mist", "void.mist.range", nil, "range %g: %g to %g metres", mi.Range, MinMistRange, MaxMistRange)
		}
		for _, x := range mi.Color {
			if !(x >= 0 && x <= 2) {
				c.errorf("void.mist", "void.mist.color", nil, "color %v: each 0 to 2", mi.Color)
				break
			}
		}
		if v.Backdrop != nil && v.Backdrop.Unlit {
			c.warnf("void.mist-unlit", "void.mist", nil, "a mist over an unlit backdrop sees nothing behind it: "+
				"the void shows its full colour, and stale patterns where nothing else is drawn")
		}
		for _, f := range c.m.fords { // known with the copy
			water := f.Depth + FordWater
			if tint := (mi.Z - water) / mi.Range; mi.Range > 0 && tint > MistFordTint {
				c.warnf("void.mist-ford", "void.mist.z", pt(Point{f.X, f.Y}), "the ford's water (z %.2f) lies %.2f m "+
					"under the mist: tinted about %.0f %% of the mist's colour", water, mi.Z-water, 100*min(tint, 1))
			}
		}
	}
	if f := v.Fog; f != nil {
		if !(f.Start <= MaxFogHeight && f.End >= -MaxFogHeight && f.Start > f.End) {
			c.errorf("void.fog", "void.fog", nil, "start %g, end %g: the start above the end, each within ±%g metres",
				f.Start, f.End, MaxFogHeight)
		}
		if f.Color != nil {
			for _, x := range f.Color {
				if !(x >= 0 && x <= 1) {
					c.errorf("void.fog", "void.fog.color", nil, "color %v: each 0 to 1", *f.Color)
					break
				}
			}
		}
	}
}

// voidFiles checks that a backdrop's package picture is there and reads (Check, when the map was loaded from its
// folder).
func (c *checker) voidFiles() {
	m := c.m
	if m.Dir == "" || m.Void == nil || m.Void.Backdrop == nil || m.Void.Backdrop.Texture.File == "" {
		return
	}
	file := m.Void.Backdrop.Texture.File
	if PackagePath(file, ".png") != nil {
		return
	}
	if _, err := readPicture(m, file); err != nil {
		c.errorf("ground.file", "void.backdrop.texture.file", nil, "%v", err)
	}
}

// ---------------------------------------------------------------- the build

// voidPlanes are the void's planes for the ground model (ground.Spec's Backdrop and Mist, nil when not set) and the
// backdrop's texture, archived at BackdropEntry.
func (cm *Compiled) voidPlanes(read ShippedFiles) (back, mist *ground.Backdrop, files []snap.File, err error) {
	v := cm.Map.Void
	if v == nil {
		return nil, nil, nil, nil
	}
	if b := v.Backdrop; b != nil {
		var tex []byte
		w, h := 1, 1
		if rgb := b.Texture.RGB; rgb != nil {
			var c [3]byte
			for k, x := range rgb {
				c[k] = byte(min(255, max(0, int(x+0.5))))
			}
			tex, err = ground.FlatDDS(c)
		} else {
			tx := &textures{m: cm.Map, read: read, cache: map[string]*bake.Image{}}
			im, gerr := tx.get(b.Texture)
			if gerr != nil {
				return nil, nil, nil, fmt.Errorf("void.backdrop.texture: %v", gerr)
			}
			px := append([]byte(nil), im.Top()...)
			for o := 3; o < len(px); o += 4 {
				px[o] = 0xFF // opaque: the plane is
			}
			w, h = im.W, im.H
			tex, err = dds.Encode(w, h, px, "dxt1", 0)
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("void.backdrop.texture: %v", err)
		}
		scale := b.Scale
		if scale == 0 {
			scale = ground.BackdropRepeat
		}
		path := BackdropEntry(cm.Map.Name)
		back = &ground.Backdrop{Z: b.Z, Half: ground.BackdropHalf, RepeatX: scale, RepeatY: scale * float64(h) / float64(w),
			Texture: path}
		if b.Unlit {
			raw, err := read(ground.UnlitArchive, ground.UnlitEntry)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("void.backdrop.unlit: %v", err)
			}
			if back.Material, err = ground.UnlitMaterial(raw, backdropEmissive); err != nil {
				return nil, nil, nil, err
			}
		}
		files = append(files, snap.File{Name: path, Data: tex})
	}
	if mi := v.Mist; mi != nil {
		raw, err := read(ground.FogFaceArchive, ground.FogFaceEntry)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("void.mist: %v", err)
		}
		mt, err := ground.FogFaceMaterial(raw, mi.Color, mi.Range)
		if err != nil {
			return nil, nil, nil, err
		}
		mist = &ground.Backdrop{Z: mi.Z, Half: ground.BackdropHalf, RepeatX: ground.BackdropRepeat,
			RepeatY: ground.BackdropRepeat, Material: mt}
	}
	return back, mist, files, nil
}

// SetHeightFog sets the height fog of every GraphicsControl in o (a marker, a markerset or any value holding them):
// fog_ground_start, fog_ground_end and, when color isn't nil, fog_color. It returns how many it set.
func SetHeightFog(o any, start, end float64, color *[3]float64) int {
	n := 0
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case *assetdata.Object:
			if _, ok := x.Get("fog_ground_start"); ok {
				x.Set("fog_ground_start", float32(start))
				x.Set("fog_ground_end", float32(end))
				if color != nil {
					x.Set("fog_color", []any{float32(color[0]), float32(color[1]), float32(color[2])})
				}
				n++
			}
			for _, k := range x.Keys() {
				e, _ := x.Get(k)
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(o)
	return n
}
