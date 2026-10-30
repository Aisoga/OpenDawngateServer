package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/mapc/bake"
)

// bake (a map whose ground is baked) writes the bake as pictures, from the copy's textures, without building the
// archive:
//
//	mapc bake [--json] [--full] [--maps <dir>] <package> <copy> <dir>
//
// <dir> gets ground.png (the whole ground from above, north up, at most 2048 pixels a side), minimap.png (the
// minimap as the archive gets it) and mask_<k>.png (layer k's mask, white where it is drawn; the first layer has
// none unless the ground has a picture); --json prints {"ok", "texels", "tiles", "texel_m", "half_m", "margin",
// "files", "ms"} (with --full also "tiles_png": the tiles' file names, west to east, then south to north).
//
// --full also writes every tile at its own size, tile_<i>_<j>.png (i from the west, j from the south): exactly the
// tile's texture in the archive, margins included (the outer "margin" pixels of each side are the neighbours'
// ground). Listed in that order as the ground's "picture", they bake back the same tiles.

const bakeSide = 2048

func (t *tool) bakePictures() int {
	pkg, copyDir, dir := t.o.args[0], t.o.args[1], t.o.args[2]
	m, code := t.load(pkg)
	if m == nil {
		return code
	}
	if !m.Baked() {
		return t.fail(1, "%s: the ground's style is %q, not \"baked\"", m.Name, m.Ground.Style)
	}
	if code := t.resolvePropsFrom(m, copyDir); code != 0 { // the props' footprints and fords, as build has them
		return code
	}
	r, cm := mapc.Check(m, others(pkg, t.o.mapsDir))
	if cm == nil || !r.OK {
		if t.o.json {
			t.emit(map[string]any{"ok": false, "error": "the map fails a rule", "report": r})
		} else {
			t.printReport(r)
		}
		return 1
	}
	in, err := mapc.ReadInputs(copyDir)
	if err != nil {
		return t.fail(2, "%v", err)
	}
	start := time.Now()
	b, err := cm.Bake(in.Shipped())
	if err != nil {
		return t.fail(1, "%v", err)
	}
	s := b.Spec
	side := min(bakeSide, s.Tiles*s.Texels)
	files := map[string][]byte{}
	png := func(name string, w, h int, rgba []byte) error {
		p, err := bake.EncodePNG(w, h, rgba)
		files[name] = p
		return err
	}
	if err := png("ground.png", side, side, s.Picture(side, -s.Half, -s.Half, 2*s.Half)); err != nil {
		return t.fail(1, "%v", err)
	}
	mm, err := cm.BakedMinimap(b)
	if err == nil {
		err = png("minimap.png", mapc.Grid, mapc.Grid, mm)
	}
	if err != nil {
		return t.fail(1, "%v", err)
	}
	var tileNames []string
	if t.o.full {
		tiles, err := mapc.PictureTiles(s)
		if err != nil {
			return t.fail(1, "%v", err)
		}
		pngs := make([][]byte, len(tiles))
		errs := make([]error, len(tiles))
		var wg sync.WaitGroup
		for k := range tiles { // the encoding is the slow part (4096 x 4096 each): one goroutine a tile
			wg.Add(1)
			go func() {
				defer wg.Done()
				pngs[k], errs[k] = bake.EncodePNG(s.Texels, s.Texels, tiles[k])
				tiles[k] = nil
			}()
		}
		wg.Wait()
		for k := range tiles {
			if errs[k] != nil {
				return t.fail(1, "%v", errs[k])
			}
			name := mapc.PictureTileFile(k%s.Tiles, k/s.Tiles)
			files[name] = pngs[k]
			tileNames = append(tileNames, name)
		}
	}
	for k, l := range s.Layers {
		if l.Mask == nil {
			continue
		}
		n := l.Mask.N
		px := make([]byte, 4*n*n)
		for j := 0; j < n; j++ {
			for i := 0; i < n; i++ {
				v := byte(min(255, max(0, int(255*l.Mask.V[j*n+i]+0.5))))
				o := 4 * ((n-1-j)*n + i)
				px[o], px[o+1], px[o+2], px[o+3] = v, v, v, 255
			}
		}
		if err := png(fmt.Sprintf("mask_%d.png", k), n, n, px); err != nil {
			return t.fail(1, "%v", err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return t.fail(1, "%v", err)
	}
	var names []string
	for n, data := range files {
		if err := os.WriteFile(filepath.Join(dir, n), data, 0o644); err != nil {
			return t.fail(1, "%v", err)
		}
		names = append(names, n)
	}
	ms := time.Since(start).Milliseconds()
	if t.o.json {
		res := map[string]any{"ok": true, "texels": s.Texels, "tiles": s.Tiles, "texel_m": s.TexelSize(),
			"half_m": s.Half, "margin": s.Margin, "files": len(names), "ms": ms}
		if t.o.full {
			res["tiles_png"] = tileNames
		}
		t.emit(res)
		return 0
	}
	fmt.Fprintf(t.out, "%s: %d x %d tiles of %d texels (%.3f m a texel over +-%.1f m), %d pictures in %s (%d ms)\n",
		m.Name, s.Tiles, s.Tiles, s.Texels, s.TexelSize(), s.Half, len(names), dir, ms)
	return 0
}
