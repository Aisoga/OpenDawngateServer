package ground

import (
	"fmt"

	"dawngate/server/internal/setup/bmdl"
	"dawngate/server/internal/setup/dds"
)

// The void's pieces (MAPART-VOID: Spec.Cut, Spec.Backdrop, Spec.Mist), shared by MM_Map_v3's experiment tool and
// the map compiler's void section: the planes' size, the shipped materials they borrow, and a flat texture.
const (
	BackdropHalf   = 700.0         // a plane spans -BackdropHalf to BackdropHalf on both axes
	BackdropRepeat = 175.0         // metres per texture repeat (along x; along y as the texture's sides are)
	BackdropCells  = backdropCells // the plane's grid: (BackdropCells + 1)^2 vertices, 2 BackdropCells^2 triangles

	// the shipped labsFogFace material: a fog sphere's (DiffuseTint, FogRange; position only), never placed in a level
	FogFaceArchive = "anshavar3.snap"
	FogFaceEntry   = "environments/anshavar/fogspheretest.bmdl"
	// the shipped labsUnlit material: the parasite's energy tube (material 1 of the model)
	UnlitArchive = "Creeps.snap"
	UnlitEntry   = "Creeps/Parasite/default/stage_02/parasite_stage_02.bmdl"
)

// modelMaterials parses a model and returns its materials.
func modelMaterials(raw []byte, entry string) ([]*bmdl.Node, error) {
	tr, err := bmdl.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", entry, err)
	}
	m, _ := tr.Root.F["model"].(*bmdl.Node)
	if m == nil {
		return nil, fmt.Errorf("%s: no model", entry)
	}
	mats, _ := m.F["materials"].([]*bmdl.Node)
	return mats, nil
}

// FogFaceMaterial is the fog sphere's labsFogFace material (raw: FogFaceEntry's model) with its DiffuseTint (values
// 0-2) and FogRange (value 3) set: a see-through surface whose opacity grows with the depth of what lies behind it,
// full at FogRange metres (as seen in game; not read in the shader).
func FogFaceMaterial(raw []byte, tint [3]float64, fogRange float64) (*bmdl.Node, error) {
	mats, err := modelMaterials(raw, FogFaceEntry)
	if err != nil {
		return nil, err
	}
	if len(mats) != 1 || mats[0].F["shader"] != "labsFogFace" {
		return nil, fmt.Errorf("%s: material 0 is not labsFogFace", FogFaceEntry)
	}
	mt := mats[0].Clone()
	vals, ok := mt.F["values"].([]float32)
	if !ok || len(vals) != 4 {
		return nil, fmt.Errorf("%s: unexpected values %T", FogFaceEntry, mt.F["values"])
	}
	mt.F["values"] = []float32{float32(tint[0]), float32(tint[1]), float32(tint[2]), float32(fogRange)}
	return mt, nil
}

// UnlitMaterial is the parasite's labsUnlit material (raw: UnlitEntry's model) with EmissiveLevel (value 3) set and
// GlowLevel (value 8, 8.0 on the tube: its bloom) 0. It takes no shadow and no height fog; its one texture is the
// backdrop's.
func UnlitMaterial(raw []byte, emissive float64) (*bmdl.Node, error) {
	mats, err := modelMaterials(raw, UnlitEntry)
	if err != nil {
		return nil, err
	}
	if len(mats) < 2 || mats[1].F["shader"] != "labsUnlit" {
		return nil, fmt.Errorf("%s: material 1 is not labsUnlit", UnlitEntry)
	}
	mt := mats[1].Clone()
	vals, ok := mt.F["values"].([]float32)
	if !ok || len(vals) < 9 {
		return nil, fmt.Errorf("%s: unexpected values %T", UnlitEntry, mt.F["values"])
	}
	vals = append([]float32(nil), vals...)
	vals[3], vals[8] = float32(emissive), 0
	mt.F["values"] = vals
	return mt, nil
}

// FlatDDS is a 64 x 64 one-colour DXT1 texture with its mips (RGB 0..255): a plain backdrop's.
func FlatDDS(rgb [3]byte) ([]byte, error) {
	const size = 64
	px := make([]byte, size*size*4)
	for o := 0; o < len(px); o += 4 {
		px[o], px[o+1], px[o+2], px[o+3] = rgb[0], rgb[1], rgb[2], 0xFF
	}
	return dds.Encode(size, size, px, "dxt1", 0)
}

// BackdropMaterial is b's material: a copy of b.Material, or else of base (the ground's first labsVertLit material),
// its first texture (when it has one) set to b.Texture, its flags b.Flags when set.
func BackdropMaterial(b *Backdrop, base *bmdl.Node) *bmdl.Node {
	src := base
	if b.Material != nil {
		src = b.Material
	}
	mt := src.Clone()
	if texs, _ := mt.F["textures"].([]*bmdl.Node); len(texs) > 0 {
		tx := texs[0].Clone()
		tx.F["path"], tx.F["path_hash"] = b.Texture, bmdl.FNV1(b.Texture)
		mt.F["textures"] = []*bmdl.Node{tx}
	}
	if b.Flags != nil {
		mt.F["flags"] = *b.Flags
	}
	return mt
}

// BackdropPart is b's mesh and part in the template's form (tmesh, tpart and its submesh tsub, the 32-byte layout):
// the mesh named name on material mat, the part pointing at mesh index; and the plane's points (for the model's
// bounds).
func BackdropPart(b *Backdrop, name string, mat, index uint32, tmesh, tpart, tsub *bmdl.Node) (me, part *bmdl.Node,
	pts [][3]float64) {
	vb, pts, idx := backdropMesh(b)
	me = tmesh.Clone()
	ib := make([]byte, 2*len(idx))
	for q, v := range idx {
		ib[2*q], ib[2*q+1] = byte(v), byte(v>>8)
	}
	me.F["name"], me.F["name_hash"] = name, bmdl.FNV1(name)
	me.F["material"] = mat
	me.F["vertices"], me.F["indices"] = vb, ib
	me.F["vertex_count"], me.F["index_count"] = uint32(len(pts)), uint32(len(idx))
	me.F["bounds"] = boundsOf(pts, false)
	me.F["target_count"], me.F["targets"] = uint32(0), []*bmdl.Node{}
	part = tpart.Clone()
	part.F["mesh"] = index
	part.F["bounds"] = boundsOf(pts, true)
	sm := tsub.Clone()
	sm.F["index_start"], sm.F["index_count"], sm.F["material"] = uint32(0), uint32(len(idx)), mat
	sm.F["bounds"] = boundsOf(pts, false)
	part.F["submeshes"] = []*bmdl.Node{sm}
	return me, part, pts
}

// Cuts reports whether a triangle of the ground's vertices ks lies wholly past the cut (Spec.Cut): every corner
// more than Cut metres past the walkable edge.
func (g *Ground) Cuts(ks [3]int) bool {
	return g.Cut > 0 && g.D[ks[0]] > g.Cut && g.D[ks[1]] > g.Cut && g.D[ks[2]] > g.Cut
}
