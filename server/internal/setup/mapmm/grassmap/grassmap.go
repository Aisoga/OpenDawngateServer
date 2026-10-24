// Package grassmap makes MM_Map_v3's grass map (images/32bit/MM_Map_v3_GrassMap.tga) and its bushes' fog outlines from
// the level's grass markers, byte for byte as the Python reference does.
//
// A level's bushes are the Grass markers of its generated_grass markerset. Each one's grass data names the level's
// grass map (cut), its patch (outline, a name) and the patch's rectangle in the map's texture space (cutTopLeft,
// cutBottomRight: u, v from 0 to 1; world x = (u - 0.5) * levelSize, y likewise). The game reads them so:
//   - a patch's id is FNV-1 of its outline name in lower case (the first map's 28 patches all are);
//   - the grass map holds the id in every cell of the patch (the image depot's R, G, B, A are the id's bytes from the
//     lowest; the TGA file stores B, G, R, A) and the same colour with alpha 0 on a ring of one cell round it; the
//     game's grass test takes the cells with alpha;
//   - the grass renderer puts a blade only where the cell under it holds the patch's id, and fills the whole rectangle
//     when the map is missing (square bushes that hide nothing: MM_Map_v3 as it ships);
//   - the fog renderer's outline file holds each patch as a grass outline (its id and a closed polygon, whose bounding
//     box is the rectangle): it shadows a vision circle whose source stands outside that patch.
//
// MM_Map_v3 ships neither its grass map nor grass outlines, and the patches' own shapes are lost, so each patch is the
// ellipse inscribed in its rectangle: the cells the ellipse touches hold the id, then the ring; the outline is the
// ellipse as a 24-gon (a point every 15 degrees from +x, counter-clockwise).
package grassmap

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/mapmm/fogmap"
)

const (
	// Entry is the grass map's path (MM_Map_v3.Level's grassMap and every Grass marker's cut).
	Entry = "images/32bit/MM_Map_v3_GrassMap.tga"
	// MarkersetEntry is the level's grass markers, in AssetData.snap.
	MarkersetEntry = "AssetData/Binary/MM_Map_v3_generated_grass.Markerset.bin"
)

// Patch is one bush: its outline name and its rectangle in the grass map's texture space.
type Patch struct {
	Name   string
	TL, BR [2]float64
}

// cos24 holds cos(k * 15 degrees), k = 0..23, made from square roots so that both implementations get the same doubles.
var cos24 = func() [24]float64 {
	q := [7]float64{1, (math.Sqrt(6) + math.Sqrt(2)) / 4, math.Sqrt(3) / 2, math.Sqrt(2) / 2, 0.5,
		(math.Sqrt(6) - math.Sqrt(2)) / 4, 0}
	var c [24]float64
	for k := 0; k <= 6; k++ {
		c[k] = q[k]
	}
	for k := 7; k <= 12; k++ {
		c[k] = -q[12-k]
	}
	for k := 13; k <= 18; k++ {
		c[k] = -q[k-12]
	}
	for k := 19; k <= 23; k++ {
		c[k] = q[24-k]
	}
	return c
}()

func sin24(k int) float64 { return cos24[(k+18)%24] }

// ID is a patch's grass id: FNV-1 (32 bits) of its outline name in lower case; the name must be ASCII.
func ID(name string) (uint32, error) {
	h := uint32(0x811C9DC5)
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b >= 0x80 {
			return 0, fmt.Errorf("grassmap: the outline name %q is not ASCII", name)
		}
		if 'A' <= b && b <= 'Z' {
			b += 'a' - 'A'
		}
		h *= 0x01000193
		h ^= uint32(b)
	}
	return h, nil
}

// Patches are a generated_grass markerset's patches in marker order: the markers whose grass data names cut (any
// case) as its grass map. The values are the asset reader's (floats to 7 significant digits).
func Patches(ts *assetdata.Types, set []byte, cut string) ([]Patch, error) {
	t := ts.Type("Markerset")
	if t == nil {
		return nil, errors.New("grassmap: no Markerset type")
	}
	obj, _, err := ts.Parse(t, set, nil)
	if err != nil {
		return nil, fmt.Errorf("grassmap: the markerset: %v", err)
	}
	ms, _ := obj.Get("markers")
	list, _ := ms.([]any)
	var out []Patch
	for _, m := range list {
		mo, ok := m.(*assetdata.Object)
		if !ok {
			continue
		}
		gv, _ := mo.Get("grassData")
		g, ok := gv.(*assetdata.Object)
		if !ok || g == nil {
			continue
		}
		if c, _ := g.Get("cut"); !isText(c, cut) {
			continue
		}
		name, _ := g.Get("outline")
		ns, _ := name.(string)
		if ns == "" {
			return nil, errors.New("grassmap: a grass marker without an outline name")
		}
		p := Patch{Name: ns}
		for k, key := range []string{"cutTopLeft", "cutBottomRight"} {
			v, _ := g.Get(key)
			vs, ok := v.([]any)
			if !ok || len(vs) != 2 {
				return nil, fmt.Errorf("grassmap: %s: no %s", ns, key)
			}
			for i := range vs {
				f, ok := vs[i].(float64)
				if !ok {
					return nil, fmt.Errorf("grassmap: %s: %s is not a vector", ns, key)
				}
				if k == 0 {
					p.TL[i] = f
				} else {
					p.BR[i] = f
				}
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func isText(v any, want string) bool {
	s, ok := v.(string)
	return ok && strings.EqualFold(s, want)
}

// Rect is the patch's world rectangle x0, y0, x1, y1 on a level of the given size.
func (p Patch) Rect(size float64) (x0, y0, x1, y1 float64) {
	xa, xb := (p.TL[0]-0.5)*size, (p.BR[0]-0.5)*size
	ya, yb := (p.TL[1]-0.5)*size, (p.BR[1]-0.5)*size
	return math.Min(xa, xb), math.Min(ya, yb), math.Max(xa, xb), math.Max(ya, yb)
}

func (p Patch) ellipse(size float64) (cx, cy, a, b float64) {
	x0, y0, x1, y1 := p.Rect(size)
	return (x0 + x1) * 0.5, (y0 + y1) * 0.5, (x1 - x0) * 0.5, (y1 - y0) * 0.5
}

// Outline is the patch's fog outline: the inscribed ellipse as a 24-gon, counter-clockwise from +x.
func (p Patch) Outline(size float64) (fogmap.Outline, error) {
	id, err := ID(p.Name)
	if err != nil {
		return fogmap.Outline{}, err
	}
	cx, cy, a, b := p.ellipse(size)
	pts := make([]fogmap.Point, 24)
	for k := range pts {
		pts[k] = fogmap.Point{X: cx + float64(a*cos24[k]), Y: cy + float64(b*sin24(k))}
	}
	return fogmap.Outline{Grass: id, Points: pts}, nil
}

// Outlines are the patches' fog outlines in order.
func Outlines(ps []Patch, size float64) ([]fogmap.Outline, error) {
	out := make([]fogmap.Outline, len(ps))
	for i, p := range ps {
		o, err := p.Outline(size)
		if err != nil {
			return nil, err
		}
		out[i] = o
	}
	return out, nil
}

// solidCells are the cells (i, j; j from the map's bottom row) the patch's ellipse touches: the point of the cell
// nearest the ellipse's centre lies in it.
func (p Patch) solidCells(size float64, w, h int) ([][2]int, error) {
	cx, cy, a, b := p.ellipse(size)
	if a <= 0 || b <= 0 {
		return nil, fmt.Errorf("grassmap: %s: an empty rectangle", p.Name)
	}
	x0, y0, x1, y1 := p.Rect(size)
	fw, fh := float64(w), float64(h)
	i0 := max(0, int(math.Floor((x0/size+0.5)*fw))-3)
	i1 := min(w-1, int(math.Floor((x1/size+0.5)*fw))+3)
	j0 := max(0, int(math.Floor((y0/size+0.5)*fh))-3)
	j1 := min(h-1, int(math.Floor((y1/size+0.5)*fh))+3)
	var cells [][2]int
	for j := j0; j <= j1; j++ {
		ya, yb := (float64(j)-fh/2)*size/fh, (float64(j+1)-fh/2)*size/fh
		v := (math.Min(math.Max(cy, ya), yb) - cy) / b
		for i := i0; i <= i1; i++ {
			xa, xb := (float64(i)-fw/2)*size/fw, (float64(i+1)-fw/2)*size/fw
			u := (math.Min(math.Max(cx, xa), xb) - cx) / a
			if float64(u*u)+float64(v*v) <= 1 {
				cells = append(cells, [2]int{i, j})
			}
		}
	}
	return cells, nil
}

// cellValues are the grass map's non-zero cells (index j*w+i) as the image depot hands them over, a u32 each: every
// patch's solid cells first (an earlier patch keeps a shared cell), then each patch's ring, the 8 neighbours of its
// solid cells still empty, with the id's low 24 bits (alpha 0).
func cellValues(ps []Patch, size float64, w, h int) (map[int]uint32, error) {
	vals := map[int]uint32{}
	type solid struct {
		id    uint32
		cells [][2]int
	}
	var solids []solid
	for _, p := range ps {
		id, err := ID(p.Name)
		if err != nil {
			return nil, err
		}
		if id>>24 == 0 {
			return nil, fmt.Errorf("grassmap: %s: its id %08X has alpha 0 (the game would take no cell as grass)", p.Name, id)
		}
		cells, err := p.solidCells(size, w, h)
		if err != nil {
			return nil, err
		}
		solids = append(solids, solid{id, cells})
		for _, c := range cells {
			if _, ok := vals[c[1]*w+c[0]]; !ok {
				vals[c[1]*w+c[0]] = id
			}
		}
	}
	for _, s := range solids {
		for _, c := range s.cells {
			for dj := -1; dj <= 1; dj++ {
				for di := -1; di <= 1; di++ {
					i, j := c[0]+di, c[1]+dj
					if i < 0 || i >= w || j < 0 || j >= h {
						continue
					}
					if _, ok := vals[j*w+i]; !ok {
						vals[j*w+i] = s.id & 0x00FFFFFF
					}
				}
			}
		}
	}
	return vals, nil
}

// Image is the grass map: an uncompressed 32-bit TGA laid out as the first map's (18-byte header, 8 alpha bits, rows
// from the bottom, pixels B, G, R, A), every cell 0 but the patches'.
func Image(ps []Patch, size float64, w, h int) ([]byte, error) {
	if w <= 0 || h <= 0 || w > 0xFFFF || h > 0xFFFF {
		return nil, fmt.Errorf("grassmap: a %d x %d map", w, h)
	}
	vals, err := cellValues(ps, size, w, h)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 18+4*w*h)
	out[2] = 2
	binary.LittleEndian.PutUint16(out[12:], uint16(w))
	binary.LittleEndian.PutUint16(out[14:], uint16(h))
	out[16], out[17] = 32, 8
	for k, v := range vals {
		o := 18 + 4*k
		out[o], out[o+1], out[o+2], out[o+3] = byte(v>>16), byte(v>>8), byte(v), byte(v>>24)
	}
	return out, nil
}
