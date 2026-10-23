package fogmap

import (
	"fmt"
	"math"

	"dawngate/server/internal/setup/mapmm/los"
)

// Field is the signed distance field of a line-of-sight map, one value per cell centre (rows as the map's, bottom
// to top), in cells: + the distance into blocked ground (byte != 0), - into open ground, 0 on the cells' edge
// (the Euclidean distance from the centre to the nearest centre of the other kind, minus half a cell). The fog
// outlines and the minimap both start from it.
type Field []float64

// NewField computes the signed distance field of an 8-bit line-of-sight map.
func NewField(img los.Image) (Field, error) {
	w, h := img.W, img.H
	if w < 1 || h < 1 || len(img.Pix) < w*h {
		return nil, fmt.Errorf("fogmap: a %d x %d map needs %d bytes, it has %d", w, h, w*h, len(img.Pix))
	}
	blocked := make([]bool, w*h)
	for k := range blocked {
		blocked[k] = img.Pix[k] != 0
	}
	open := make([]bool, w*h)
	for k, b := range blocked {
		open[k] = !b
	}
	dOpen := edt(open, w, h)
	dBlk := edt(blocked, w, h)
	f := make(Field, w*h)
	for k := range f {
		if blocked[k] {
			f[k] = math.Sqrt(dOpen[k]) - 0.5
		} else {
			f[k] = -(math.Sqrt(dBlk[k]) - 0.5)
		}
	}
	return f, nil
}

const far = 1e30 // "no such cell" in the distance transform

// edt is the squared distance from every cell centre to the nearest cell whose mask is set (far when none):
// columns first, then rows.
func edt(mask []bool, w, h int) []float64 {
	cols := make([]float64, w*h) // column i at [i*h, (i+1)*h)
	col := make([]float64, h)
	for i := 0; i < w; i++ {
		for j := 0; j < h; j++ {
			if mask[j*w+i] {
				col[j] = 0
			} else {
				col[j] = far
			}
		}
		copy(cols[i*h:(i+1)*h], edt1(col))
	}
	out := make([]float64, w*h)
	row := make([]float64, w)
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			row[i] = cols[i*h+j]
		}
		copy(out[j*w:(j+1)*w], edt1(row))
	}
	return out
}

// edt1 is the squared distance transform of one row (Felzenszwalb and Huttenlocher's lower envelope of
// parabolas).
func edt1(f []float64) []float64 {
	n := len(f)
	v := make([]int, n)
	z := make([]float64, n+1)
	d := make([]float64, n)
	if n == 0 {
		return d
	}
	k := 0
	z[0], z[1] = -far, far
	for q := 1; q < n; q++ {
		var s float64
		for {
			s = ((f[q] + float64(q*q)) - (f[v[k]] + float64(v[k]*v[k]))) / float64(2*q-2*v[k])
			if s <= z[k] {
				k--
			} else {
				break
			}
		}
		k++
		v[k], z[k], z[k+1] = q, s, far
	}
	k = 0
	for q := 0; q < n; q++ {
		for z[k+1] < float64(q) {
			k++
		}
		d[q] = float64((q-v[k])*(q-v[k])) + f[v[k]]
	}
	return d
}
