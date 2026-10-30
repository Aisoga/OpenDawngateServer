package bmdl

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Model is a .bmdl's drawable content, decoded from its tree (Decode): the materials with their texture paths, the
// meshes' vertex attributes and triangle lists, and the parts that split a mesh's triangles by material. The game
// is Z up (x east, y north for a level's models) and its triangles are counter-clockwise seen from the front.
type Model struct {
	Name      string
	Bounds    []float32 // centre, radius, half extents, radius squared
	Materials []Material
	Meshes    []Mesh
	Parts     []Part
	Bones     int
}

// Material is a material's shader and its textures.
type Material struct {
	Shader, Technique string
	Textures          []Texture
	Params            map[string][]float32 // the parameters' values by name (DiffuseTint, TileUV, ...)
}

// Texture is one texture slot of a material.
type Texture struct{ Slot, Path string }

// Mesh is one vertex buffer and its u16 triangle list. The attributes a declaration lacks are nil; Colors are RGBA
// bytes, UVs as stored (D3D's top-left origin).
type Mesh struct {
	Name      string
	Material  int
	Positions [][3]float32
	Normals   [][3]float32
	UVs       [][2]float32
	Colors    [][4]uint8
	Indices   []uint16
}

// Part draws one mesh: its submeshes are ranges of the mesh's triangle list, each with a material.
type Part struct {
	Mesh      int
	Submeshes []Submesh
}

// Submesh is a range of a mesh's indices drawn with one material.
type Submesh struct{ Start, Count, Material int }

// The vertex declaration's usage byte is the engine's own enum (0 position, 1 normal, 2 tangent, 4 UV,
// 6 blend weights, 7 blend indices); 5 is the vertex colour (UBYTE4N, RGBA bytes; in 477 meshes of the 1,019 shipped models).
const (
	usagePosition = 0
	usageNormal   = 1
	usageUV       = 4
	usageColor    = 5
)

// Decode parses a .bmdl file into its drawable content.
func Decode(raw []byte) (*Model, error) {
	t, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if t.Tag != "bmdl" {
		return nil, fmt.Errorf("bmdl: a %s file holds no model", t.Tag)
	}
	mn, ok := t.Root.F["model"].(*Node)
	if !ok {
		return nil, fmt.Errorf("bmdl: no model")
	}
	m := &Model{}
	m.Name, _ = mn.F["name"].(string)
	m.Bounds, _ = mn.F["bounds"].([]float32)
	if sk, ok := t.Root.F["skeleton"].(*Node); ok {
		m.Bones = int(U32(sk.F["bone_count"]))
	}
	mats, _ := mn.F["materials"].([]*Node)
	for _, mt := range mats {
		var out Material
		out.Shader, _ = mt.F["shader"].(string)
		out.Technique, _ = mt.F["technique"].(string)
		texs, _ := mt.F["textures"].([]*Node)
		for _, tx := range texs {
			slot, _ := tx.F["slot"].(string)
			path, _ := tx.F["path"].(string)
			out.Textures = append(out.Textures, Texture{Slot: slot, Path: path})
		}
		vals, _ := mt.F["values"].([]float32)
		params, _ := mt.F["params"].([]*Node)
		for _, p := range params {
			name, _ := p.F["name"].(string)
			off, size := int(U32(p.F["offset"])), int(U32(p.F["size"]))
			// offset and size count bytes or floats; both readings are tried, the one that fits wins
			for _, unit := range []int{4, 1} {
				a, b := off/unit, (off+size)/unit
				if unit == 4 && (off%4 != 0 || size%4 != 0) {
					continue
				}
				if b <= len(vals) && a < b {
					if out.Params == nil {
						out.Params = map[string][]float32{}
					}
					out.Params[name] = vals[a:b]
					break
				}
			}
		}
		m.Materials = append(m.Materials, out)
	}
	meshes, _ := mn.F["meshes"].([]*Node)
	for k, me := range meshes {
		mesh, err := decodeMesh(me)
		if err != nil {
			return nil, fmt.Errorf("bmdl: mesh %d: %v", k, err)
		}
		m.Meshes = append(m.Meshes, mesh)
	}
	parts, _ := mn.F["parts"].([]*Node)
	for _, p := range parts {
		part := Part{Mesh: int(U32(p.F["mesh"]))}
		if part.Mesh >= len(m.Meshes) {
			return nil, fmt.Errorf("bmdl: a part names mesh %d of %d", part.Mesh, len(m.Meshes))
		}
		subs, _ := p.F["submeshes"].([]*Node)
		for _, s := range subs {
			sm := Submesh{Start: int(U32(s.F["index_start"])), Count: int(U32(s.F["index_count"])),
				Material: int(U32(s.F["material"]))}
			if sm.Start+sm.Count > len(m.Meshes[part.Mesh].Indices) || sm.Count%3 != 0 {
				return nil, fmt.Errorf("bmdl: a submesh's indices %d+%d exceed mesh %d's", sm.Start, sm.Count, part.Mesh)
			}
			part.Submeshes = append(part.Submeshes, sm)
		}
		m.Parts = append(m.Parts, part)
	}
	return m, nil
}

func decodeMesh(me *Node) (Mesh, error) {
	out := Mesh{}
	out.Name, _ = me.F["name"].(string)
	out.Material = int(U32(me.F["material"]))
	decl, _ := me.F["decl"].([]byte)
	vb, _ := me.F["vertices"].([]byte)
	ib, _ := me.F["indices"].([]byte)
	n, stride := int(U32(me.F["vertex_count"])), DeclStride(decl)
	if stride == 0 || len(vb) < n*stride {
		return out, fmt.Errorf("vertex buffer %d bytes for %d vertices of %d", len(vb), n, stride)
	}
	le := binary.LittleEndian
	for p := 0; p+8 <= len(decl); p += 8 {
		if le.Uint16(decl[p:]) == 0xFF {
			break
		}
		off, typ, usage, index := int(le.Uint16(decl[p+2:])), decl[p+4], decl[p+6], decl[p+7]
		if index != 0 {
			continue
		}
		get := func(v int) []float32 { return element(vb[v*stride+off:], typ) }
		switch usage {
		case usagePosition, usageNormal:
			list := make([][3]float32, n)
			for v := range list {
				e := get(v)
				if len(e) < 3 {
					return out, fmt.Errorf("a position or normal of type %d", typ)
				}
				list[v] = [3]float32{e[0], e[1], e[2]}
			}
			if usage == usagePosition {
				out.Positions = list
			} else {
				out.Normals = list
			}
		case usageUV:
			list := make([][2]float32, n)
			for v := range list {
				e := get(v)
				if len(e) < 2 {
					return out, fmt.Errorf("a UV of type %d", typ)
				}
				list[v] = [2]float32{e[0], e[1]}
			}
			out.UVs = list
		case usageColor:
			if typ != 4 && typ != 8 {
				continue
			}
			list := make([][4]uint8, n)
			for v := range list {
				b := vb[v*stride+off:]
				if typ == 4 { // D3DCOLOR: B, G, R, A
					list[v] = [4]uint8{b[2], b[1], b[0], b[3]}
				} else {
					list[v] = [4]uint8{b[0], b[1], b[2], b[3]}
				}
			}
			out.Colors = list
		}
	}
	if out.Positions == nil {
		return out, fmt.Errorf("no positions")
	}
	cnt := int(U32(me.F["index_count"]))
	if len(ib) < 2*cnt {
		return out, fmt.Errorf("index buffer cut")
	}
	out.Indices = make([]uint16, cnt)
	for q := range out.Indices {
		out.Indices[q] = le.Uint16(ib[2*q:])
		if int(out.Indices[q]) >= n {
			return out, fmt.Errorf("index %d past %d vertices", out.Indices[q], n)
		}
	}
	return out, nil
}

// element reads one D3DDECLTYPE value as floats; normalised byte normals and tangents come back as -1 .. 1.
func element(b []byte, typ byte) []float32 {
	le := binary.LittleEndian
	f := func(k int) float32 { return math.Float32frombits(le.Uint32(b[4*k:])) }
	switch typ {
	case 0, 1, 2, 3: // FLOAT1 .. FLOAT4
		out := make([]float32, typ+1)
		for k := range out {
			out[k] = f(k)
		}
		return out
	case 8: // UBYTE4N, stored as round((x + 1) * 127)
		return []float32{float32(b[0])/127 - 1, float32(b[1])/127 - 1, float32(b[2])/127 - 1, float32(b[3])/127 - 1}
	case 9: // SHORT2N
		return []float32{float32(int16(le.Uint16(b))) / 32767, float32(int16(le.Uint16(b[2:]))) / 32767}
	case 10: // SHORT4N
		out := make([]float32, 4)
		for k := range out {
			out[k] = float32(int16(le.Uint16(b[2*k:]))) / 32767
		}
		return out
	case 11: // USHORT2N
		return []float32{float32(le.Uint16(b)) / 65535, float32(le.Uint16(b[2:])) / 65535}
	case 15: // FLOAT16_2
		return []float32{half(le.Uint16(b)), half(le.Uint16(b[2:]))}
	case 16: // FLOAT16_4
		return []float32{half(le.Uint16(b)), half(le.Uint16(b[2:])), half(le.Uint16(b[4:])), half(le.Uint16(b[6:]))}
	}
	return nil
}

func half(h uint16) float32 {
	sign, exp, frac := uint32(h>>15), int(h>>10&0x1F), uint32(h&0x3FF)
	var v float64
	switch exp {
	case 0:
		v = float64(frac) / 1024 * math.Pow(2, -14)
	case 31:
		v = math.Inf(1)
	default:
		v = (1 + float64(frac)/1024) * math.Pow(2, float64(exp-15))
	}
	if sign != 0 {
		v = -v
	}
	return float32(v)
}
