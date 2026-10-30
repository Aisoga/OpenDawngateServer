package scene

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"strings"

	"dawngate/server/internal/setup/bmdl"
	"dawngate/server/internal/setup/dds"
)

// IconInfo is a baked top-down icon: its file (relative to the output folder), its size in pixels, and how it lies
// on the ground: the model-space point at its centre and the metres a pixel spans (at the noun's graphicsScale and a
// marker scale of 1), north up.
type IconInfo struct {
	Noun     string     `json:"noun"`
	File     string     `json:"file"`
	Size     int        `json:"size"`
	Centre   [2]float64 `json:"centre"`
	PerPixel float64    `json:"metresPerPixel"`
	Cached   bool       `json:"cached,omitempty"`
}

// iconTexMax is the size the textures are halved down to for an icon.
const iconTexMax = 256

// Icon bakes a noun's model seen from straight above (orthographic, north up) into a size x size RGBA picture with
// a transparent background: each triangle's diffuse texture (nearest texel, wrapped), shaded by its face's slope
// under a light from the north-west, the topmost surface winning; cut-out and blended materials keep the texels
// with alpha of at least a half, additive ones (glows) are left out. It is a palette picture, not the game's look.
func (a *Assets) Icon(noun string, size int) ([]byte, IconInfo, error) {
	n := a.Noun(noun)
	if n == nil {
		return nil, IconInfo{}, fmt.Errorf("%s: no such noun", noun)
	}
	info := IconInfo{Noun: n.Name, Size: size}
	if n.Model == "" {
		return nil, info, fmt.Errorf("%s draws no model", noun)
	}
	raw, err := a.D.Read(n.Model)
	if err != nil {
		return nil, info, err
	}
	m, err := bmdl.Decode(raw)
	if err != nil {
		return nil, info, fmt.Errorf("%s: %v", n.Model, err)
	}
	gs := n.GraphicsScale
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, me := range m.Meshes {
		for _, p := range me.Positions {
			x, y := float64(p[0])*gs, float64(p[1])*gs
			x0, y0, x1, y1 = math.Min(x0, x), math.Min(y0, y), math.Max(x1, x), math.Max(y1, y)
		}
	}
	if !(x1 > x0 || y1 > y0) {
		return nil, info, fmt.Errorf("%s: no vertices", n.Model)
	}
	span := math.Max(x1-x0, y1-y0) * 1.04
	info.Centre = [2]float64{(x0 + x1) / 2, (y0 + y1) / 2}
	info.PerPixel = span / float64(size)
	type tex struct {
		w, h int
		rgba []byte
	}
	texs := map[string]*tex{}
	texOf := func(path string) *tex {
		k := strings.ToLower(path)
		if t, ok := texs[k]; ok {
			return t
		}
		var t *tex
		if b, err := a.D.Read(path); err == nil {
			if w, h, rgba, err := dds.Decode(b); err == nil {
				for (w > iconTexMax || h > iconTexMax) && w >= 2 && h >= 2 {
					w, h, rgba = dds.Downsample(w, h, rgba)
				}
				t = &tex{w, h, rgba}
			}
		}
		texs[k] = t
		return t
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	zbuf := make([]float64, size*size)
	for i := range zbuf {
		zbuf[i] = math.Inf(-1)
	}
	light := [3]float64{-0.4, 0.4, 0.82}
	px := func(p [3]float32) [3]float64 {
		return [3]float64{(float64(p[0])*gs-info.Centre[0])/info.PerPixel + float64(size)/2,
			float64(size)/2 - (float64(p[1])*gs-info.Centre[1])/info.PerPixel, float64(p[2]) * gs}
	}
	for _, part := range m.Parts {
		if part.Mesh < 0 || part.Mesh >= len(m.Meshes) {
			continue
		}
		me := m.Meshes[part.Mesh]
		for _, sm := range part.Submeshes {
			if sm.Material < 0 || sm.Material >= len(m.Materials) {
				continue
			}
			mt := m.Materials[sm.Material]
			draw, _, _ := DrawMode(mt.Shader)
			if draw == "add" {
				continue
			}
			var t *tex
			if p := diffuse(mt); p != "" {
				t = texOf(p)
			}
			for i := sm.Start; i+2 < sm.Start+sm.Count && i+2 < len(me.Indices); i += 3 {
				ia, ib, ic := int(me.Indices[i]), int(me.Indices[i+1]), int(me.Indices[i+2])
				if ia >= len(me.Positions) || ib >= len(me.Positions) || ic >= len(me.Positions) {
					continue
				}
				pa, pb, pc := px(me.Positions[ia]), px(me.Positions[ib]), px(me.Positions[ic])
				area := (pb[0]-pa[0])*(pc[1]-pa[1]) - (pb[1]-pa[1])*(pc[0]-pa[0])
				if area == 0 {
					continue
				}
				// the face's normal (the model's frame, z up) for the shade
				ux, uy, uz := float64(me.Positions[ib][0]-me.Positions[ia][0]), float64(me.Positions[ib][1]-me.Positions[ia][1]), float64(me.Positions[ib][2]-me.Positions[ia][2])
				vx, vy, vz := float64(me.Positions[ic][0]-me.Positions[ia][0]), float64(me.Positions[ic][1]-me.Positions[ia][1]), float64(me.Positions[ic][2]-me.Positions[ia][2])
				nx, ny, nz := uy*vz-uz*vy, uz*vx-ux*vz, ux*vy-uy*vx
				if nl := math.Sqrt(nx*nx + ny*ny + nz*nz); nl > 0 {
					nx, ny, nz = nx/nl, ny/nl, nz/nl
				}
				if nz < 0 { // seen from below: the other side
					nx, ny, nz = -nx, -ny, -nz
				}
				shade := 0.45 + 0.55*math.Max(0, nx*light[0]+ny*light[1]+nz*light[2])
				bx0 := max(int(math.Floor(math.Min(pa[0], math.Min(pb[0], pc[0])))), 0)
				bx1 := min(int(math.Ceil(math.Max(pa[0], math.Max(pb[0], pc[0])))), size-1)
				by0 := max(int(math.Floor(math.Min(pa[1], math.Min(pb[1], pc[1])))), 0)
				by1 := min(int(math.Ceil(math.Max(pa[1], math.Max(pb[1], pc[1])))), size-1)
				for y := by0; y <= by1; y++ {
					for x := bx0; x <= bx1; x++ {
						cx, cy := float64(x)+0.5, float64(y)+0.5
						w0 := ((pb[0]-cx)*(pc[1]-cy) - (pb[1]-cy)*(pc[0]-cx)) / area
						w1 := ((pc[0]-cx)*(pa[1]-cy) - (pc[1]-cy)*(pa[0]-cx)) / area
						w2 := 1 - w0 - w1
						if w0 < 0 || w1 < 0 || w2 < 0 {
							continue
						}
						z := w0*pa[2] + w1*pb[2] + w2*pc[2]
						k := y*size + x
						if z <= zbuf[k] {
							continue
						}
						c := [4]float64{180, 180, 180, 255}
						if t != nil && len(me.UVs) == len(me.Positions) {
							u := w0*float64(me.UVs[ia][0]) + w1*float64(me.UVs[ib][0]) + w2*float64(me.UVs[ic][0])
							v := w0*float64(me.UVs[ia][1]) + w1*float64(me.UVs[ib][1]) + w2*float64(me.UVs[ic][1])
							tx := int(math.Floor((u - math.Floor(u)) * float64(t.w)))
							ty := int(math.Floor((v - math.Floor(v)) * float64(t.h)))
							o := 4 * (min(ty, t.h-1)*t.w + min(tx, t.w-1))
							c = [4]float64{float64(t.rgba[o]), float64(t.rgba[o+1]), float64(t.rgba[o+2]), float64(t.rgba[o+3])}
						}
						if draw != "opaque" && c[3] < 128 {
							continue
						}
						zbuf[k] = z
						o := img.PixOffset(x, y)
						for ch := range 3 {
							img.Pix[o+ch] = byte(math.Min(255, c[ch]*shade))
						}
						img.Pix[o+3] = 255
					}
				}
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, info, err
	}
	return b.Bytes(), info, nil
}
