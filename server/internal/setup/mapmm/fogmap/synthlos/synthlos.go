// Package synthlos builds the made-up line-of-sight maps the map generators' tests run on without the game files
// (the Python reference's test builds the same maps from the same integer formula, so their outputs' hashes can
// be pinned).
package synthlos

import "dawngate/server/internal/setup/mapmm/los"

// Blocked is the formula: the map border, a solid block with an open pocket and an island in it, a ring round an
// open pocket with an island, crossing diagonal walls, a checkerboard patch (saddles), one-cell walls, a comb of
// one-cell slots, a block on the right edge and sparse noise that toggles single cells outside the solid block.
func Blocked(i, j, w, h int64) bool {
	m := min(w, h)
	b := i < 3 || j < 3 || i >= w-3 || j >= h-3
	ax0, ax1, ay0, ay1 := w/2, w/2+w*3/8, h/16, h/16+h*3/8
	inA := ax0 <= i && i < ax1 && ay0 <= j && j < ay1
	if inA {
		px0, py0 := ax0+w/16, ay0+h/16
		px1, py1 := px0+max(3, w/24), py0+max(3, h/24)
		b = !(px0 <= i && i < px1 && py0 <= j && j < py1) || (i == px0+1 && j == py0+1)
	}
	cx, cy, ro, ri := w/4, h*3/4, m/8, m/16
	d2 := (i-cx)*(i-cx) + (j-cy)*(j-cy)
	if (ri*ri <= d2 && d2 < ro*ro) || d2 == 0 {
		b = true
	}
	if k := i - w/8; 0 <= k && k < m/4 && (j == h/2+k || j == h/2+m/4-k) {
		b = true
	}
	if w*5/8 <= i && i < w*5/8+8 && h*5/8 <= j && j < h*5/8+8 && (i+j)%2 == 0 {
		b = true
	}
	if (i == w*3/4 && h/2 <= j && j < h/2+h/8) || (j == h*7/8 && w/2 <= i && i < w/2+w/8) {
		b = true
	}
	if cx0, cy0 := w/16, h/2+h/5; cx0 <= i && i < cx0+w/6 && cy0 <= j && j < cy0+h/8 {
		b = !((i-cx0)%3 == 1 && j >= cy0+2)
	}
	if i >= w-w/10 && h/3 <= j && j < h/2 {
		b = true
	}
	if !inA {
		n := (i * 92837111) ^ (j * 689287499) ^ (i * j * 283923481)
		if (n>>5)%53 == 0 {
			b = !b
		}
	}
	return b
}

// Map is the w x h made-up map (128 blocked, 0 open, rows bottom to top).
func Map(w, h int) los.Image {
	pix := make([]byte, w*h)
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			if Blocked(int64(i), int64(j), int64(w), int64(h)) {
				pix[j*w+i] = 128
			}
		}
	}
	return los.Image{W: w, H: h, Bpp: 8, Pix: pix}
}
