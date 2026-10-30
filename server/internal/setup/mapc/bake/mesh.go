package bake

import (
	"encoding/binary"
	"fmt"
	"math"

	"dawngate/server/internal/setup/bmdl"
	"dawngate/server/internal/setup/mapmm/ground"
)

// Cliff is the steep faces' material: a tileable texture at Path (written as 2 x 2 repeats of the source, see
// CliffPixels), mapped from the side at Scale metres per repeat, on every triangle whose normal's z is below
// CosSlope.
type Cliff struct {
	Path     string
	Scale    float64
	CosSlope float64
}

// MaxChunk is the most cells a mesh spans per side (65 x 65 grid vertices plus the cliffs' own stay under 65,536).
const MaxChunk = 64

// spans cuts n cells into parts of at most MaxChunk, as even as possible.
func spans(n int) []int {
	k := (n + MaxChunk - 1) / MaxChunk
	out := make([]int, k+1)
	for i := 0; i <= k; i++ {
		out[i] = i * n / k
	}
	return out
}

func node(n *bmdl.Node, name string) (*bmdl.Node, error) {
	c, ok := n.F[name].(*bmdl.Node)
	if !ok {
		return nil, fmt.Errorf("bake: the template has no %s", name)
	}
	return c, nil
}

func nodes(n *bmdl.Node, name string, want int) ([]*bmdl.Node, error) {
	c, ok := n.F[name].([]*bmdl.Node)
	if !ok || len(c) < want {
		return nil, fmt.Errorf("bake: the template has fewer than %d %s", want, name)
	}
	return c, nil
}

// faceNormal is a triangle's unit normal.
func faceNormal(a, b, c [3]float64) [3]float64 {
	u := [3]float64{b[0] - a[0], b[1] - a[1], b[2] - a[2]}
	v := [3]float64{c[0] - a[0], c[1] - a[1], c[2] - a[2]}
	n := [3]float64{float64(u[1]*v[2]) - float64(u[2]*v[1]), float64(u[2]*v[0]) - float64(u[0]*v[2]),
		float64(u[0]*v[1]) - float64(u[1]*v[0])}
	l := math.Sqrt(float64(n[0]*n[0]) + float64(n[1]*n[1]) + float64(n[2]*n[2]))
	if l == 0 {
		return [3]float64{0, 0, 1}
	}
	return [3]float64{n[0] / l, n[1] / l, n[2] / l}
}

// cliffUV maps a steep triangle from the side: along x or y (whichever the face looks along less) and down z, each
// shifted by whole repeats so its smallest value lies in [0, 1), halved for the texture's 2 x 2 repeats. ok is false
// when the triangle spans a repeat or more (it then stays on the bake).
func cliffUV(p [3][3]float64, n [3]float64, scale float64) (uv [3][2]float64, ok bool) {
	var raw [3][2]float64
	for k, q := range p {
		h := q[0]
		if math.Abs(n[0]) >= math.Abs(n[1]) {
			h = q[1]
		}
		raw[k] = [2]float64{h / scale, -q[2] / scale}
	}
	for a := 0; a < 2; a++ {
		lo, hi := raw[0][a], raw[0][a]
		for _, r := range raw[1:] {
			lo, hi = math.Min(lo, r[a]), math.Max(hi, r[a])
		}
		if hi-lo >= 1 {
			return uv, false
		}
		shift := math.Floor(lo)
		for k := range raw {
			uv[k][a] = (raw[k][a] - shift) / 2
		}
	}
	return uv, true
}

// BuildModel writes the ground model: the template's tree (TemplateEntry's: its first vertex-lit material and its
// 32-byte mesh and part) with one material per bake tile (tilePaths[tj*Tiles+ti], the tile's texture) and, with
// cliff, one for the steep faces; the heightfield g cut into meshes that each lie in one tile (each a mesh and a
// part), its vertices mapped from above onto their tile. g's square must be the bake's ([-Half, Half]^2) and its
// cells must divide into the tiles. g's void (ground.Spec's Cut, Backdrop and Mist) acts as in the tiled ground: the
// triangles wholly past the cut are left out, and each plane is one more mesh on a material of its own, last.
func BuildModel(template []byte, g *ground.Ground, s *Spec, name string, tilePaths []string, cliff *Cliff) ([]byte, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	if g.Grid%s.Tiles != 0 || len(tilePaths) != s.Tiles*s.Tiles {
		return nil, fmt.Errorf("bake: %d cells don't make %d x %d tiles (%d paths)", g.Grid, s.Tiles, s.Tiles, len(tilePaths))
	}
	if d := math.Abs(float64(g.Grid/2)*g.Cell - s.Half); d > 1e-9 {
		return nil, fmt.Errorf("bake: the ground spans %g m, the bake %g", float64(g.Grid/2)*g.Cell, s.Half)
	}
	tr, err := bmdl.Parse(template)
	if err != nil {
		return nil, err
	}
	tm, err := node(tr.Root, "model")
	if err != nil {
		return nil, err
	}
	tmats, err := nodes(tm, "materials", 1)
	if err != nil {
		return nil, err
	}
	tmeshes, err := nodes(tm, "meshes", 2)
	if err != nil {
		return nil, err
	}
	tparts, err := nodes(tm, "parts", 2)
	if err != nil {
		return nil, err
	}
	tmesh, tpart := tmeshes[1], tparts[1]
	if decl, ok := tmesh.F["decl"].([]byte); !ok || bmdl.DeclStride(decl) != 32 {
		return nil, fmt.Errorf("bake: template mesh 1 is not the 32-byte layout")
	}
	tsubs, err := nodes(tpart, "submeshes", 1)
	if err != nil {
		return nil, err
	}
	paths := append([]string{}, tilePaths...)
	cliffMat := -1
	if cliff != nil {
		cliffMat = len(paths)
		paths = append(paths, cliff.Path)
	}
	var mats []*bmdl.Node
	for _, path := range paths {
		mt := tmats[0].Clone()
		texs, ok := mt.F["textures"].([]*bmdl.Node)
		if sh, _ := mt.F["shader"].(string); sh != "labsVertLit" || !ok || len(texs) != 1 {
			return nil, fmt.Errorf("bake: template material 0 is not a one-texture labsVertLit")
		}
		texs[0].F["path"], texs[0].F["path_hash"] = path, bmdl.FNV1(path)
		mats = append(mats, mt)
	}

	n := g.N
	per := g.Grid / s.Tiles
	cut := spans(per)
	var meshes, parts []*bmdl.Node
	var every [][3]float64
	type tri struct{ k [3]int }
	for tj := 0; tj < s.Tiles; tj++ {
		for cj := 0; cj+1 < len(cut); cj++ {
			for ti := 0; ti < s.Tiles; ti++ {
				for ci := 0; ci+1 < len(cut); ci++ {
					i0, i1 := ti*per+cut[ci], ti*per+cut[ci+1]
					j0, j1 := tj*per+cut[cj], tj*per+cut[cj+1]
					rowLen := i1 - i0 + 1
					local := func(k int) int { return (k/n-j0)*rowLen + (k%n - i0) }
					var vb []byte
					var pts [][3]float64
					pos := func(k int) [3]float64 { return [3]float64{g.X[k%n], g.X[k/n], g.Z[k]} }
					for j := j0; j <= j1; j++ {
						for i := i0; i <= i1; i++ {
							k := j*n + i
							p := pos(k)
							u, v := s.UV(ti, tj, p[0], p[1])
							pts = append(pts, p)
							vb = ground.EncodeVertex(vb, p, g.Normal[k], [2]float64{u, v}, g.RGB[k])
						}
					}
					var flat []int
					var steep []int
					for j := j0; j < j1; j++ {
						for i := i0; i < i1; i++ {
							a, b, c, d := j*n+i, j*n+i+1, (j+1)*n+i+1, (j+1)*n+i
							for _, t := range [2]tri{{[3]int{a, b, c}}, {[3]int{a, c, d}}} {
								if g.Cuts(t.k) { // the void's cut (ground.Spec.Cut)
									continue
								}
								if cliff != nil {
									p := [3][3]float64{pos(t.k[0]), pos(t.k[1]), pos(t.k[2])}
									fn := faceNormal(p[0], p[1], p[2])
									if fn[2] < cliff.CosSlope {
										if uv, ok := cliffUV(p, fn, cliff.Scale); ok {
											base := len(pts)
											for q := 0; q < 3; q++ {
												pts = append(pts, p[q])
												vb = ground.EncodeVertex(vb, p[q], g.Normal[t.k[q]], uv[q], g.RGB[t.k[q]])
											}
											steep = append(steep, base, base+1, base+2)
											continue
										}
									}
								}
								flat = append(flat, local(t.k[0]), local(t.k[1]), local(t.k[2]))
							}
						}
					}
					if len(pts) > 65535 {
						return nil, fmt.Errorf("bake: a mesh with %d vertices", len(pts))
					}
					type sub struct{ mat, start, count int }
					var subs []sub
					if len(flat)+len(steep) == 0 {
						flat = []int{0, 0, 0} // a mesh the cut emptied: one degenerate triangle keeps it (draws nothing)
					}
					idx := append([]int{}, flat...)
					if len(flat) > 0 {
						subs = append(subs, sub{tj*s.Tiles + ti, 0, len(flat)})
					}
					if len(steep) > 0 {
						subs = append(subs, sub{cliffMat, len(idx), len(steep)})
						idx = append(idx, steep...)
					}
					me := tmesh.Clone()
					mname := fmt.Sprintf("%s_ground_%d", name, len(meshes))
					ib := make([]byte, 2*len(idx))
					for q, v := range idx {
						binary.LittleEndian.PutUint16(ib[2*q:], uint16(v))
					}
					me.F["name"], me.F["name_hash"] = mname, bmdl.FNV1(mname)
					me.F["material"] = uint32(subs[0].mat)
					me.F["vertices"], me.F["indices"] = vb, ib
					me.F["vertex_count"], me.F["index_count"] = uint32(len(pts)), uint32(len(idx))
					me.F["bounds"] = ground.Bounds(pts, false)
					me.F["target_count"], me.F["targets"] = uint32(0), []*bmdl.Node{}
					part := tpart.Clone()
					part.F["mesh"] = uint32(len(meshes))
					part.F["bounds"] = ground.Bounds(pts, true)
					var sl []*bmdl.Node
					for _, su := range subs {
						sm := tsubs[0].Clone()
						sm.F["index_start"], sm.F["index_count"], sm.F["material"] = uint32(su.start), uint32(su.count), uint32(su.mat)
						sp := make([][3]float64, su.count)
						for q := range sp {
							sp[q] = pts[idx[su.start+q]]
						}
						sm.F["bounds"] = ground.Bounds(sp, false)
						sl = append(sl, sm)
					}
					part.F["submeshes"] = sl
					meshes = append(meshes, me)
					parts = append(parts, part)
					every = append(every, pts...)
				}
			}
		}
	}
	// the void's planes (ground.Spec.Backdrop and Mist), each on its own material after the bake's
	for k, b := range g.Backs {
		mat := uint32(len(mats))
		mats = append(mats, ground.BackdropMaterial(b, tmats[0]))
		mname := name + "_backdrop"
		if k > 0 {
			mname = fmt.Sprintf("%s_backdrop_%d", name, k)
		}
		me, part, pts := ground.BackdropPart(b, mname, mat, uint32(len(meshes)), tmesh, tpart, tsubs[0])
		meshes = append(meshes, me)
		parts = append(parts, part)
		every = append(every, pts...)
	}
	model := tm.Clone()
	mn := name + "_terrain"
	model.F["name"], model.F["name_hash"] = mn, bmdl.FNV1(mn)
	model.F["materials"], model.F["meshes"], model.F["parts"] = mats, meshes, parts
	model.F["bounds"] = ground.Bounds(every, false)
	root := &bmdl.Node{Type: "Root", F: map[string]any{}}
	for k, v := range tr.Root.F {
		root.F[k] = v
	}
	root.F["model"], root.F["skeleton"], root.F["anims"], root.F["anim_count"] = model, nil, []*bmdl.Node{}, uint32(0)
	return bmdl.Write(&bmdl.Tree{Tag: tr.Tag, Version: tr.Version, Align: tr.Align, Root: root})
}

// ModelInfo is what CheckModel counts.
type ModelInfo struct {
	Materials, Meshes, Triangles int
	Bounds                       []float32
}

// CheckModel parses a ground model back: the round trip byte for byte, every index in range, every submesh's
// material there, the submeshes covering the indices. It takes any ground model of the 32-byte layout (the tiled
// ground's two materials too).
func CheckModel(raw []byte) (ModelInfo, error) {
	tr, err := bmdl.Parse(raw)
	if err != nil {
		return ModelInfo{}, err
	}
	again, err := bmdl.Write(tr)
	if err != nil {
		return ModelInfo{}, err
	}
	if string(again) != string(raw) {
		return ModelInfo{}, fmt.Errorf("bake: the model does not round-trip")
	}
	m, err := node(tr.Root, "model")
	if err != nil {
		return ModelInfo{}, err
	}
	mats, _ := m.F["materials"].([]*bmdl.Node)
	meshes, _ := m.F["meshes"].([]*bmdl.Node)
	parts, _ := m.F["parts"].([]*bmdl.Node)
	if len(mats) == 0 || len(meshes) == 0 || len(meshes) != len(parts) {
		return ModelInfo{}, fmt.Errorf("bake: %d materials, %d meshes, %d parts", len(mats), len(meshes), len(parts))
	}
	tris := 0
	for pi, me := range meshes {
		part := parts[pi]
		nIdx, nVert := int(bmdl.U32(me.F["index_count"])), int(bmdl.U32(me.F["vertex_count"]))
		ib, _ := me.F["indices"].([]byte)
		if nIdx == 0 || len(ib) < 2*nIdx {
			return ModelInfo{}, fmt.Errorf("bake: mesh %d has no indices", pi)
		}
		top := 0
		for q := 0; q < nIdx; q++ {
			top = max(top, int(binary.LittleEndian.Uint16(ib[2*q:])))
		}
		if top >= nVert || int(bmdl.U32(part.F["mesh"])) != pi {
			return ModelInfo{}, fmt.Errorf("bake: mesh %d: index out of range", pi)
		}
		subs, _ := part.F["submeshes"].([]*bmdl.Node)
		cover := 0
		for _, s := range subs {
			if int(bmdl.U32(s.F["material"])) >= len(mats) {
				return ModelInfo{}, fmt.Errorf("bake: mesh %d: a submesh's material %d of %d", pi, bmdl.U32(s.F["material"]), len(mats))
			}
			cover += int(bmdl.U32(s.F["index_count"]))
		}
		if cover != nIdx {
			return ModelInfo{}, fmt.Errorf("bake: mesh %d: submeshes do not cover the indices", pi)
		}
		tris += nIdx / 3
	}
	b, _ := m.F["bounds"].([]float32)
	return ModelInfo{Materials: len(mats), Meshes: len(meshes), Triangles: tris, Bounds: b}, nil
}
