package fogmap

import (
	"math"

	"dawngate/server/internal/setup/mapmm/los"
)

// The minimap's colours (SM_Map_v5's minimap: the most common colour over each kind of ground).
var (
	laneRGB    = [3]int{120, 115, 106}
	baseRGB    = [3]int{139, 141, 172}
	islandRGB  = [3]int{183, 177, 194} // SM_Map_v5's jungle
	wallRGB    = [3]int{139, 103, 103}
	wallFarRGB = [3]int{104, 78, 76} // SM_Map_v5's outer walls
	rimRGB     = [3]int{34, 28, 30}  // the dark line along every wall
	chasmRGB   = [3]int{38, 42, 58}  // a chasm (ours: SM_Map_v5 has none)
)

// MinimapSpec are the places a minimap colours apart from the lane: the two base ends (metres) and the islands.
type MinimapSpec struct {
	Bases   [2][2]float64
	Islands [][2]float64
	// Chasm marks open cells (index j*W+i, rows from the bottom) drawn as a chasm: seen through, not walked (nil: none).
	Chasm []bool
}

// MM is MM_Map_v3's: its base ends and its two corner islands.
var MM = MinimapSpec{
	Bases:   [2][2]float64{{-206.0, -206.0}, {206.0, 206.0}},
	Islands: [][2]float64{{-260.8, -260.1}, {260.6, 261.1}},
}

const (
	baseRadius   = 50.0
	baseFade     = 25.0
	islandRadius = 18.0
)

// MinimapRGBA is the minimap picture of a line-of-sight map whose cells span sx x sy metres: W x H RGBA pixels,
// rows top to bottom with +y up (pixel (x, y) shows cell (x, H-1-y)). Open ground is the lane's colour, fading
// from the bases' colour within 50 m of a base end over 25 m, and the islands' colour within 18 m of a corner
// island; blocked cells beside open ground (within 1.5 cells of an open cell centre) are a dark rim, the other
// walls fade from the near to the far wall colour between 24 and 64 cells from open ground. MM_Map_v3's minimap is
// this picture of its 512 x 512 map, DXT5-compressed.
func MinimapRGBA(img los.Image, sx, sy float64) ([]byte, error) {
	f, err := NewField(img)
	if err != nil {
		return nil, err
	}
	return MinimapRGBAFrom(img, f, sx, sy), nil
}

// MinimapRGBAFrom is MinimapRGBA with the map's field already computed.
func MinimapRGBAFrom(img los.Image, f Field, sx, sy float64) []byte {
	return MinimapRGBASpec(img, f, sx, sy, MM)
}

// MinimapRGBASpec is MinimapRGBAFrom with another map's bases and islands.
func MinimapRGBASpec(img los.Image, f Field, sx, sy float64, spec MinimapSpec) []byte {
	bases, islands := spec.Bases, spec.Islands
	w, h := img.W, img.H
	fw, fh := float64(w), float64(h)
	out := make([]byte, w*h*4)
	blend := func(a, b [3]int, u float64) [3]int {
		var c [3]int
		for k := range c {
			c[k] = int(math.RoundToEven(float64(a[k]) + float64(float64(b[k]-a[k])*u)))
		}
		return c
	}
	for j := 0; j < h; j++ {
		y := (float64(j) - fh/2) * sy / fh
		row := h - 1 - j
		for i := 0; i < w; i++ {
			x := (float64(i) - fw/2) * sx / fw
			k := j*w + i
			var c [3]int
			if img.Pix[k] == 0 && spec.Chasm != nil && spec.Chasm[k] {
				c = chasmRGB
			} else if img.Pix[k] == 0 {
				island := false
				for _, p := range islands {
					if hypot(x-p[0], y-p[1]) <= islandRadius {
						island = true
						break
					}
				}
				if island {
					c = islandRGB
				} else { // the base colour fades into the lane's over baseFade metres
					d := hypot(x-bases[0][0], y-bases[0][1])
					d = minF(d, hypot(x-bases[1][0], y-bases[1][1]))
					u := minF(1.0, maxF(0.0, (d-baseRadius)/baseFade))
					c = blend(baseRGB, laneRGB, u)
				}
			} else {
				d := f[k] + 0.5 // cells to the nearest open cell centre
				if d <= 1.5 {   // a blocked cell beside open ground (8-neighbourhood: sqrt 2 <= 1.5)
					c = rimRGB
				} else {
					u := minF(1.0, maxF(0.0, (d-24.0)/40.0))
					c = blend(wallRGB, wallFarRGB, u)
				}
			}
			p := 4 * (row*w + i)
			out[p], out[p+1], out[p+2], out[p+3] = byte(c[0]), byte(c[1]), byte(c[2]), 255
		}
	}
	return out
}
