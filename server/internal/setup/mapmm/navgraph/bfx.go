// Package navgraph makes MM_Map_v3's two NavPower navigation graphs (levels/MM_Map_v3/MM_Map_v3_Player.bfx and
// _NPC.bfx) from the map's line-of-sight map, byte for byte as the Python reference does: squares of 2 x 2 open
// cells, the Guardians' platforms cut out, the NPC graph's own octagon island on each platform, SM_Map_v5's
// shipped graphs as the template for the header values.
//
// The .bfx format is a NavPower 2.x resource image, little endian, its 32-bit pointers stored as offsets from
// the graph header:
//
//	image header 0x18   endianFlag 0, version 2, imageSize (the bytes after this header), checksum (the body's
//	                    big-endian u32 words summed mod 2^32), runtimeFlags, constantFlags
//	section      0x0C   id 0x10000 (the NavGraph component), size, pointerSize 0 (32 bit)
//	navset       0x0C   endianFlag (u8, 3 pad), version (0x25 or 0x26), numGraphs
//	graph header 0x13C  version, layer, areaBytes, totalBytes (header to the graph's end), buildScale, voxSize,
//	                    radius, step, height, bbox min xyz, max xyz, buildUpAxis (2 = z), 0x7C reserved bytes,
//	                    a 128-byte constant (copied from the template)
//	areas               area 0x38 {4 runtime pointers, pos xyz (the polygon's centroid), radius (the largest vertex
//	                    distance from pos in the plane), searchCost, usageFlags, flags1 (bits 0-6 the edge count,
//	                    7-24 the island), flags2 (bits 24-31 the basis vertex), flags3, flags4} followed by its
//	                    edges 0x18 each, counter-clockwise {adjacent area (graph offset, 0 = boundary), start
//	                    vertex xyz, flags1 (bit 15: the edge joins two clusters), flags2 (linked: the cost
//	                    ceil(500 x the distance between the two centres), boundary: 0)}
//	kd tree             header {bbox (6 floats), size} then the nodes: inner {u32 (bit 31 clear, bits 28-30 the
//	                    split axis, bits 0-27 the right child's offset from this node), f32 left max, f32 right
//	                    min}, the left child right after it; leaf u32 (bit 31 set, bits 0-30 the area's offset)
package navgraph

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Sizes of the format's fixed records.
const (
	SectionNavGraph = 0x10000
	AreaSize        = 0x38
	EdgeSize        = 0x18
	GraphHeaderSize = 0x13C
	KDHeaderSize    = 0x1C
	imageHeaderSize = 0x18
)

// Edge is one side of an area, starting at Pos (counter-clockwise seen from +z).
type Edge struct {
	Adj            uint32 // the adjacent area's graph offset, 0 on the boundary
	Pos            [3]float32
	Flags1, Flags2 uint32
}

// Area is one convex polygon of the graph.
type Area struct {
	Offset     uint32 // from the graph header (derived when written)
	Ptrs       [4]uint32
	Pos        [3]float32
	Radius     float32
	SearchCost uint32
	Usage      uint32
	Flags      [4]uint32
	Edges      []Edge
}

// KDNode is a node of the graph's kd tree: a leaf names one area, an inner node splits on Axis.
type KDNode struct {
	Leaf        bool
	Area        uint32 // leaf: the area's graph offset
	Axis        uint32
	LMax, RMin  float32
	Left, Right *KDNode
}

// Graph is one navigation graph (the game's files hold one per image).
type Graph struct {
	Version, Layer                            uint32
	BuildScale, VoxSize, Radius, Step, Height float32
	BBox                                      [6]float32
	UpAxis                                    uint32
	Reserved                                  [0x7C]byte
	Constant                                  [128]byte
	Areas                                     []Area
	KDBBox                                    [6]float32
	KD                                        *KDNode
}

// Image is a whole .bfx file.
type Image struct {
	Version, RuntimeFlags, ConstantFlags uint32
	NavsetEndian                         uint8
	NavsetVersion                        uint32
	Graphs                               []*Graph
}

// NewGraph returns a graph with the shipped graphs' header defaults (used without a template).
func NewGraph() *Graph {
	return &Graph{Version: 0x26, BuildScale: 2.0, VoxSize: 0.4, Radius: 1.59, Step: 0.5, Height: 5.0, UpAxis: 2}
}

// NewImage returns an empty image with the shipped defaults.
func NewImage() *Image { return &Image{Version: 2, NavsetVersion: 0x26} }

// Checksum is NavPower's image checksum: the sum of the body's big-endian u32 words, mod 2^32.
func Checksum(body []byte) uint32 {
	var s uint32
	for i := 0; i+4 <= len(body); i += 4 {
		s += binary.BigEndian.Uint32(body[i:])
	}
	return s
}

var errShort = errors.New("navgraph: the image is cut short")

type reader struct{ b []byte }

func (r reader) u32(o int) (uint32, error) {
	if o < 0 || o+4 > len(r.b) {
		return 0, errShort
	}
	return binary.LittleEndian.Uint32(r.b[o:]), nil
}

func (r reader) f32(o int) (float32, error) {
	v, err := r.u32(o)
	return math.Float32frombits(v), err
}

func (r reader) parseKD(o, end int) (*KDNode, int, error) {
	if o+4 > end {
		return nil, 0, errShort
	}
	d, err := r.u32(o)
	if err != nil {
		return nil, 0, err
	}
	if d&0x80000000 != 0 {
		return &KDNode{Leaf: true, Area: d & 0x7FFFFFFF}, o + 4, nil
	}
	n := &KDNode{Axis: (d >> 28) & 7}
	roff := int(d & 0x0FFFFFFF)
	if n.LMax, err = r.f32(o + 4); err != nil {
		return nil, 0, err
	}
	if n.RMin, err = r.f32(o + 8); err != nil {
		return nil, 0, err
	}
	left, lend, err := r.parseKD(o+12, end)
	if err != nil {
		return nil, 0, err
	}
	if lend != o+roff {
		return nil, 0, fmt.Errorf("navgraph: kd node at 0x%x: right child at +0x%x, the left one ends at +0x%x", o, roff, lend-o)
	}
	right, rend, err := r.parseKD(o+roff, end)
	if err != nil {
		return nil, 0, err
	}
	n.Left, n.Right = left, right
	return n, rend, nil
}

// Parse reads a .bfx image (checking its size, checksum, sections and kd tree).
func Parse(b []byte) (*Image, error) {
	if len(b) < imageHeaderSize {
		return nil, errShort
	}
	r := reader{b}
	le := binary.LittleEndian
	img := NewImage()
	endian, size, csum := le.Uint32(b[0:]), le.Uint32(b[8:]), le.Uint32(b[12:])
	img.Version, img.RuntimeFlags, img.ConstantFlags = le.Uint32(b[4:]), le.Uint32(b[16:]), le.Uint32(b[20:])
	if endian != 0 {
		return nil, errors.New("navgraph: a big-endian image is not supported")
	}
	if int64(size)+imageHeaderSize != int64(len(b)) {
		return nil, fmt.Errorf("navgraph: image size 0x%x + 0x18 != file size 0x%x", size, len(b))
	}
	if Checksum(b[imageHeaderSize:]) != csum {
		return nil, errors.New("navgraph: checksum mismatch")
	}
	for o := imageHeaderSize; o < len(b); {
		if o+24 > len(b) {
			return nil, errShort
		}
		sid, ssize, ptrsize := le.Uint32(b[o:]), le.Uint32(b[o+4:]), le.Uint32(b[o+8:])
		if sid != SectionNavGraph || ptrsize != 0 {
			return nil, fmt.Errorf("navgraph: unsupported section 0x%x (pointer size %d)", sid, ptrsize)
		}
		s := o + 12
		img.NavsetEndian = b[s]
		img.NavsetVersion = le.Uint32(b[s+4:])
		ngraphs := le.Uint32(b[s+8:])
		g0 := s + 12
		for k := uint32(0); k < ngraphs; k++ {
			if g0+GraphHeaderSize > len(b) {
				return nil, errShort
			}
			g := &Graph{}
			g.Version, g.Layer = le.Uint32(b[g0:]), le.Uint32(b[g0+4:])
			areaBytes, total := int(le.Uint32(b[g0+8:])), int(le.Uint32(b[g0+12:]))
			fl := make([]float32, 11)
			for i := range fl {
				fl[i] = math.Float32frombits(le.Uint32(b[g0+16+4*i:]))
			}
			g.BuildScale, g.VoxSize, g.Radius, g.Step, g.Height = fl[0], fl[1], fl[2], fl[3], fl[4]
			copy(g.BBox[:], fl[5:])
			g.UpAxis = le.Uint32(b[g0+0x3C:])
			copy(g.Reserved[:], b[g0+0x40:g0+0xBC])
			copy(g.Constant[:], b[g0+0xBC:g0+GraphHeaderSize])
			a := g0 + GraphHeaderSize
			aend := a + areaBytes
			if aend > len(b) || aend < a {
				return nil, errShort
			}
			for a < aend {
				if a+AreaSize > len(b) {
					return nil, errShort
				}
				var ar Area
				ar.Offset = uint32(a - g0)
				for i := 0; i < 4; i++ {
					ar.Ptrs[i] = le.Uint32(b[a+4*i:])
					ar.Flags[i] = le.Uint32(b[a+40+4*i:])
				}
				for i := 0; i < 3; i++ {
					ar.Pos[i] = math.Float32frombits(le.Uint32(b[a+16+4*i:]))
				}
				ar.Radius = math.Float32frombits(le.Uint32(b[a+28:]))
				ar.SearchCost, ar.Usage = le.Uint32(b[a+32:]), le.Uint32(b[a+36:])
				n := int(ar.Flags[0] & 0x7F)
				if a+AreaSize+EdgeSize*n > len(b) {
					return nil, errShort
				}
				ar.Edges = make([]Edge, n)
				for i := range ar.Edges {
					eo := a + AreaSize + EdgeSize*i
					e := &ar.Edges[i]
					e.Adj = le.Uint32(b[eo:])
					for c := 0; c < 3; c++ {
						e.Pos[c] = math.Float32frombits(le.Uint32(b[eo+4+4*c:]))
					}
					e.Flags1, e.Flags2 = le.Uint32(b[eo+16:]), le.Uint32(b[eo+20:])
				}
				g.Areas = append(g.Areas, ar)
				a += AreaSize + EdgeSize*n
			}
			if a != aend {
				return nil, errors.New("navgraph: the areas overrun their size")
			}
			for i := 0; i < 6; i++ {
				v, err := r.f32(a + 4*i)
				if err != nil {
					return nil, err
				}
				g.KDBBox[i] = v
			}
			kdSize, err := r.u32(a + 24)
			if err != nil {
				return nil, err
			}
			k0 := a + KDHeaderSize
			kend := k0 + int(kdSize)
			if kend > len(b) || kend < k0 {
				return nil, errShort
			}
			kd, end, err := r.parseKD(k0, kend)
			if err != nil {
				return nil, err
			}
			if end != kend || kend != g0+total {
				return nil, errors.New("navgraph: kd tree size mismatch")
			}
			g.KD = kd
			img.Graphs = append(img.Graphs, g)
			g0 += total
		}
		o += 12 + int(ssize)
		if o != g0 {
			return nil, errors.New("navgraph: section size mismatch")
		}
	}
	return img, nil
}

func writeKD(n *KDNode, out []byte) []byte {
	start := len(out)
	if n.Leaf {
		return binary.LittleEndian.AppendUint32(out, 0x80000000|n.Area)
	}
	out = append(out, make([]byte, 12)...)
	out = writeKD(n.Left, out)
	roff := uint32(len(out) - start)
	binary.LittleEndian.PutUint32(out[start:], n.Axis<<28|roff)
	binary.LittleEndian.PutUint32(out[start+4:], math.Float32bits(n.LMax))
	binary.LittleEndian.PutUint32(out[start+8:], math.Float32bits(n.RMin))
	return writeKD(n.Right, out)
}

func appendF32(b []byte, v float32) []byte {
	return binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
}

// Write serializes the image (the edge counts in flags1, the sizes and the checksum derived).
func Write(img *Image) []byte {
	le := binary.LittleEndian
	var graphs []byte
	for _, g := range img.Graphs {
		var areas []byte
		for _, ar := range g.Areas {
			for _, p := range ar.Ptrs {
				areas = le.AppendUint32(areas, p)
			}
			for _, p := range ar.Pos {
				areas = appendF32(areas, p)
			}
			areas = appendF32(areas, ar.Radius)
			areas = le.AppendUint32(areas, ar.SearchCost)
			areas = le.AppendUint32(areas, ar.Usage)
			areas = le.AppendUint32(areas, ar.Flags[0]&^0x7F|uint32(len(ar.Edges)))
			for _, f := range ar.Flags[1:] {
				areas = le.AppendUint32(areas, f)
			}
			for _, e := range ar.Edges {
				areas = le.AppendUint32(areas, e.Adj)
				for _, p := range e.Pos {
					areas = appendF32(areas, p)
				}
				areas = le.AppendUint32(areas, e.Flags1)
				areas = le.AppendUint32(areas, e.Flags2)
			}
		}
		kd := writeKD(g.KD, nil)
		total := GraphHeaderSize + len(areas) + KDHeaderSize + len(kd)
		graphs = le.AppendUint32(graphs, g.Version)
		graphs = le.AppendUint32(graphs, g.Layer)
		graphs = le.AppendUint32(graphs, uint32(len(areas)))
		graphs = le.AppendUint32(graphs, uint32(total))
		for _, f := range []float32{g.BuildScale, g.VoxSize, g.Radius, g.Step, g.Height} {
			graphs = appendF32(graphs, f)
		}
		for _, f := range g.BBox {
			graphs = appendF32(graphs, f)
		}
		graphs = le.AppendUint32(graphs, g.UpAxis)
		graphs = append(graphs, g.Reserved[:]...)
		graphs = append(graphs, g.Constant[:]...)
		graphs = append(graphs, areas...)
		for _, f := range g.KDBBox {
			graphs = appendF32(graphs, f)
		}
		graphs = le.AppendUint32(graphs, uint32(len(kd)))
		graphs = append(graphs, kd...)
	}
	var section []byte
	section = le.AppendUint32(section, SectionNavGraph)
	section = le.AppendUint32(section, uint32(12+len(graphs)))
	section = le.AppendUint32(section, 0)
	section = append(section, img.NavsetEndian, 0, 0, 0)
	section = le.AppendUint32(section, img.NavsetVersion)
	section = le.AppendUint32(section, uint32(len(img.Graphs)))
	section = append(section, graphs...)
	out := make([]byte, 0, imageHeaderSize+len(section))
	out = le.AppendUint32(out, 0)
	out = le.AppendUint32(out, img.Version)
	out = le.AppendUint32(out, uint32(len(section)))
	out = le.AppendUint32(out, Checksum(section))
	out = le.AppendUint32(out, img.RuntimeFlags)
	out = le.AppendUint32(out, img.ConstantFlags)
	return append(out, section...)
}
