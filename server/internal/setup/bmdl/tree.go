package bmdl

// The .bmdl model file as a tree: parse it and lay it out again byte for byte.
//
// A .bmdl is a 32-byte header (u32 1, the tag "bmdl" or "banm", the version, the data's start, its size, the
// alignment, 0, the relocation count), the relocation table (the data offsets that hold pointers) padded to the
// alignment, and the data: a memory image of the structures below with pointers as data offsets, strings
// NUL-terminated and the gaps filled with 0xBA. The writer reproduces the build tool's block order: the root, then
// for each of its pointers the target block and its subtree; in a subtree, for each structure of a block, all its
// pointer targets in field order, then a descent into each of them.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

const pad = 0xBA

type kind struct {
	k     string // u32 i32 f32 f32n u32n str arr arr1 farr uarr blob
	n     int    // f32n, u32n: the value count
	typ   string // arr, arr1: the structure; blob: the blob kind
	count string // arr, farr, uarr: the count field
}

type field struct {
	name string
	kind kind
}

var (
	kU32 = kind{k: "u32"}
	kI32 = kind{k: "i32"}
	kF32 = kind{k: "f32"}
	kStr = kind{k: "str"}
)

func f32n(n int) kind          { return kind{k: "f32n", n: n} }
func u32n(n int) kind          { return kind{k: "u32n", n: n} }
func arr(t, count string) kind { return kind{k: "arr", typ: t, count: count} }
func arr1(t string) kind       { return kind{k: "arr1", typ: t} }
func farr(count string) kind   { return kind{k: "farr", count: count} }
func uarr(count string) kind   { return kind{k: "uarr", count: count} }
func blob(b string) kind       { return kind{k: "blob", typ: b} }
func (k kind) scalar() bool {
	return k.k == "u32" || k.k == "i32" || k.k == "f32" || k.k == "f32n" || k.k == "u32n"
}
func fl(name string, k kind) field { return field{name, k} }

func (k kind) width() int {
	if k.k == "f32n" || k.k == "u32n" {
		return 4 * k.n
	}
	return 4
}

type typeDef struct {
	align  int
	fields func(v uint32) []field
}

var types map[string]typeDef

func init() {
	same := func(f ...field) func(uint32) []field { return func(uint32) []field { return f } }
	types = map[string]typeDef{
		"Root": {4, same(fl("model", arr1("Model")), fl("skeleton", arr1("Skeleton")), fl("anim_count", kU32),
			fl("anims", arr("Anim", "anim_count")))},
		"Model": {4, same(fl("bounds", f32n(8)), fl("name", kStr), fl("name_hash", kU32),
			fl("material_count", kU32), fl("materials", arr("Material", "material_count")),
			fl("mesh_count", kU32), fl("meshes", arr("Mesh", "mesh_count")),
			fl("part_count", kU32), fl("parts", arr("Part", "part_count")), fl("unk_40", kU32))},
		"Material": {4, same(fl("shader", kStr), fl("shader_hash", kU32), fl("technique", kStr),
			fl("technique_hash", kU32), fl("flags", kU32),
			fl("param_count", kU32), fl("params", arr("Param", "param_count")),
			fl("value_count", kU32), fl("values", farr("value_count")),
			fl("texture_count", kU32), fl("textures", arr("Texture", "texture_count")),
			fl("stream_count", kU32), fl("streams", arr("Stream", "stream_count")))},
		"Param":   {4, same(fl("name", kStr), fl("name_hash", kU32), fl("offset", kU32), fl("size", kU32))},
		"Texture": {4, same(fl("slot", kStr), fl("slot_hash", kU32), fl("path", kStr), fl("path_hash", kU32))},
		"Stream":  {4, same(fl("name", kStr), fl("name_hash", kU32), fl("source", kStr), fl("source_hash", kU32))},
		"Mesh": {4, func(v uint32) []field {
			f := []field{fl("bounds", f32n(8)), fl("name", kStr), fl("name_hash", kU32), fl("material", kU32),
				fl("decl_count", kU32), fl("decl", blob("decl")), fl("vertices", blob("vertices")),
				fl("indices", blob("indices")), fl("vertex_count", kU32), fl("index_count", kU32)}
			if v >= 5 {
				f = append(f, fl("target_count", kU32), fl("targets", arr("BlendTarget", "target_count")))
			}
			return f
		}},
		"Part": {4, same(fl("bounds", f32n(8)), fl("mesh", kU32), fl("submesh_count", kU32),
			fl("submeshes", arr("Submesh", "submesh_count")))},
		"Submesh": {4, same(fl("bounds", f32n(8)), fl("index_start", kU32), fl("index_count", kU32),
			fl("material", kU32), fl("palette", u32n(16)))},
		"Skeleton": {4, same(fl("bone_count", kU32), fl("count2", kU32), fl("bones", arr("Bone", "bone_count")))},
		"Bone": {16, same(fl("name", kStr), fl("name_hash", kU32), fl("parent", kI32), fl("flags", kU32),
			fl("inverse_bind", f32n(16)), fl("rotation", f32n(4)), fl("translation", f32n(4)), fl("scale", f32n(4)))},
		"Anim": {4, func(v uint32) []field {
			f := []field{fl("name", kStr), fl("name_hash", kU32), fl("frame_time", kF32), fl("unk_0c", kU32),
				fl("event_count", kU32), fl("events", arr("Event", "event_count")), fl("channel_count", kU32),
				fl("channels", arr("Channel", "channel_count"))}
			if v >= 5 {
				f = append(f, fl("v5_a", kU32), fl("v5_b", kU32))
			}
			return f
		}},
		"BlendTarget": {4, same(fl("name", kStr), fl("name_hash", kU32), fl("count", kU32),
			fl("indices", blob("target_indices")), fl("deltas", blob("target_deltas")))},
		"Event": {4, same(fl("bone", kStr), fl("bone_hash", kU32), fl("kind", kStr), fl("kind_hash", kU32),
			fl("key_count", kU32), fl("times", farr("key_count")), fl("texts", arr("Text", "key_count")),
			fl("text_hashes", uarr("key_count")))},
		"Text": {4, same(fl("text", kStr))},
		"Channel": {4, same(fl("name", kStr), fl("bone_hash", kU32), fl("attr_hash", kU32),
			fl("type", kStr), fl("type_hash", kU32), fl("components", kU32),
			fl("frame_time", kF32), fl("flags", kU32), fl("offset", kF32), fl("scale", kF32),
			fl("key_count", kU32), fl("times", blob("times")), fl("values", blob("values")))},
	}
}

// groupBreaks: a field named here first finishes the subtrees placed before it.
var groupBreaks = map[string]string{"Anim": "channels"}

var blobAlign = map[string]int{"decl": 2, "vertices": 4, "indices": 2, "times": 2, "values": 2,
	"target_indices": 2, "target_deltas": 4}

// D3DDECLTYPE sizes.
var declSizes = map[byte]int{0: 4, 1: 8, 2: 12, 3: 16, 4: 4, 5: 4, 6: 4, 7: 8, 8: 4, 9: 4, 10: 8,
	11: 4, 12: 8, 13: 4, 14: 4, 15: 4, 16: 8, 17: 0}

func typeSize(name string, v uint32) int {
	n := 0
	for _, f := range types[name].fields(v) {
		n += f.kind.width()
	}
	return n
}

// Node is one structure. Its fields: uint32, int32, float32, []float32, []uint32 (scalars and fixed lists); a
// pointer field holds nil (no target) or a string, []byte (a blob), []*Node (an array), *Node (a single
// structure), []float32 or []uint32 (a pointed-to list).
type Node struct {
	Type string
	F    map[string]any
}

type Tree struct {
	Tag     string
	Version uint32
	Align   uint32
	Root    *Node
}

func FNV1(text string) uint32 {
	h := uint32(0x811C9DC5)
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c >= 'A' && c <= 'Z' || c >= 0xC0 && c <= 0xDE && c != 0xD7 {
			c += 0x20
		}
		h *= 0x01000193
		h ^= uint32(c)
	}
	return h
}

// DeclStride is a vertex declaration's vertex size: 8-byte elements up to the stream 0xFF end.
func DeclStride(decl []byte) int {
	stride := 0
	for p := 0; p+8 <= len(decl); p += 8 {
		stream, off, typ := binary.LittleEndian.Uint16(decl[p:]), int(binary.LittleEndian.Uint16(decl[p+2:])), decl[p+4]
		if stream == 0xFF {
			break
		}
		stride = max(stride, off+declSizes[typ])
	}
	return stride
}

func U32(v any) uint32 {
	switch x := v.(type) {
	case uint32:
		return x
	case int:
		return uint32(x)
	case int32:
		return uint32(x)
	}
	panic(fmt.Sprintf("bmdl: %T is no u32", v))
}

func isNone(v any) bool { return v == nil }

func isEmptyList(v any) bool {
	switch x := v.(type) {
	case []*Node:
		return len(x) == 0
	case []float32:
		return len(x) == 0
	case []uint32:
		return len(x) == 0
	}
	return false
}

func listLen(v any) int {
	switch x := v.(type) {
	case nil:
		return 0
	case []*Node:
		return len(x)
	case []float32:
		return len(x)
	case []uint32:
		return len(x)
	}
	panic(fmt.Sprintf("bmdl: %T is no list", v))
}

func blobSize(b string, obj *Node, at func(o, k int) ([]byte, error)) (int, error) {
	switch b {
	case "decl":
		for n := 8; ; n += 8 {
			x, err := at(n-8, 2)
			if err != nil {
				return 0, err
			}
			if binary.LittleEndian.Uint16(x) == 0xFF {
				return n, nil
			}
		}
	case "vertices":
		return int(U32(obj.F["vertex_count"])) * DeclStride(obj.F["decl"].([]byte)), nil
	case "indices":
		return int(U32(obj.F["index_count"])) * 2, nil
	case "times":
		return int(U32(obj.F["key_count"])) * 2, nil
	case "values":
		return int(U32(obj.F["key_count"])) * int(U32(obj.F["components"])) * 2, nil
	case "target_indices":
		return int(U32(obj.F["count"])) * 2, nil
	case "target_deltas":
		return int(U32(obj.F["count"])) * 24, nil
	}
	return 0, fmt.Errorf("bmdl: blob kind %s", b)
}

type ptrTarget struct {
	ok  bool
	off int
}

// Parse reads the whole file; every relocation must be consumed.
func Parse(raw []byte) (*Tree, error) {
	le := binary.LittleEndian
	if len(raw) < 0x20 || (string(raw[4:8]) != "bmdl" && string(raw[4:8]) != "banm") || le.Uint32(raw) != 1 {
		return nil, fmt.Errorf("bmdl: not a bmdl/banim file")
	}
	version, data0, dataSize, align := le.Uint32(raw[8:]), int(le.Uint32(raw[12:])), int(le.Uint32(raw[16:])), le.Uint32(raw[20:])
	n := int(le.Uint32(raw[28:]))
	if data0+dataSize != len(raw) || data0 < 0x20 {
		return nil, fmt.Errorf("bmdl: data size %d + start %d != file size %d", dataSize, data0, len(raw))
	}
	if 0x20+4*n > len(raw) {
		return nil, fmt.Errorf("bmdl: the relocation table is cut")
	}
	relset := make(map[int]bool, n)
	for i := 0; i < n; i++ {
		relset[int(le.Uint32(raw[0x20+4*i:]))] = true
	}
	used := make(map[int]bool, n)
	D := raw[data0:]
	get := func(off, k int) ([]byte, error) {
		if off < 0 || k < 0 || off+k > len(D) {
			return nil, fmt.Errorf("bmdl: read of %d bytes at +0x%x past the data", k, off)
		}
		return D[off : off+k], nil
	}
	var readStruct func(t string, off int) (*Node, error)
	readStruct = func(t string, off int) (*Node, error) {
		obj := &Node{Type: t, F: map[string]any{}}
		fields := types[t].fields(version)
		ptrs := map[string]ptrTarget{}
		p := off
		for _, fd := range fields {
			b, err := get(p, fd.kind.width())
			if err != nil {
				return nil, err
			}
			switch fd.kind.k {
			case "u32":
				obj.F[fd.name] = le.Uint32(b)
			case "i32":
				obj.F[fd.name] = int32(le.Uint32(b))
			case "f32":
				obj.F[fd.name] = math.Float32frombits(le.Uint32(b))
			case "f32n":
				v := make([]float32, fd.kind.n)
				for i := range v {
					v[i] = math.Float32frombits(le.Uint32(b[4*i:]))
				}
				obj.F[fd.name] = v
			case "u32n":
				v := make([]uint32, fd.kind.n)
				for i := range v {
					v[i] = le.Uint32(b[4*i:])
				}
				obj.F[fd.name] = v
			default:
				if relset[p] {
					used[p] = true
					ptrs[fd.name] = ptrTarget{true, int(le.Uint32(b))}
				} else {
					ptrs[fd.name] = ptrTarget{}
				}
			}
			if relset[p] && fd.kind.scalar() {
				return nil, fmt.Errorf("bmdl: %s.%s at +0x%x is relocated", t, fd.name, p)
			}
			p += fd.kind.width()
		}
		for _, fd := range fields {
			if fd.kind.scalar() {
				continue
			}
			pt := ptrs[fd.name]
			if !pt.ok {
				obj.F[fd.name] = nil
				continue
			}
			target := pt.off
			switch fd.kind.k {
			case "str":
				if target < 0 || target > len(D) {
					return nil, fmt.Errorf("bmdl: string at +0x%x past the data", target)
				}
				win := D[target:min(target+4096, len(D))]
				end := bytes.IndexByte(win, 0)
				if end < 0 {
					return nil, fmt.Errorf("bmdl: string at +0x%x has no end", target)
				}
				obj.F[fd.name] = string(win[:end])
			case "arr":
				cnt := int(U32(obj.F[fd.kind.count]))
				size := typeSize(fd.kind.typ, version)
				if target < 0 || target+cnt*size > len(D) {
					return nil, fmt.Errorf("bmdl: %d %s at +0x%x past the data", cnt, fd.kind.typ, target)
				}
				list := make([]*Node, cnt)
				for i := range list {
					o, err := readStruct(fd.kind.typ, target+i*size)
					if err != nil {
						return nil, err
					}
					list[i] = o
				}
				obj.F[fd.name] = list
			case "arr1":
				o, err := readStruct(fd.kind.typ, target)
				if err != nil {
					return nil, err
				}
				obj.F[fd.name] = o
			case "farr", "uarr":
				cnt := int(U32(obj.F[fd.kind.count]))
				b, err := get(target, 4*cnt)
				if err != nil {
					return nil, err
				}
				if fd.kind.k == "farr" {
					v := make([]float32, cnt)
					for i := range v {
						v[i] = math.Float32frombits(le.Uint32(b[4*i:]))
					}
					obj.F[fd.name] = v
				} else {
					v := make([]uint32, cnt)
					for i := range v {
						v[i] = le.Uint32(b[4*i:])
					}
					obj.F[fd.name] = v
				}
			case "blob":
				size, err := blobSize(fd.kind.typ, obj, func(o, k int) ([]byte, error) { return get(target+o, k) })
				if err != nil {
					return nil, err
				}
				b, err := get(target, size)
				if err != nil {
					return nil, err
				}
				obj.F[fd.name] = bytes.Clone(b)[:size:size]
			}
		}
		return obj, nil
	}
	root, err := readStruct("Root", 0)
	if err != nil {
		return nil, err
	}
	if len(used) != len(relset) {
		first := -1
		for r := range relset {
			if !used[r] && (first < 0 || r < first) {
				first = r
			}
		}
		return nil, fmt.Errorf("bmdl: %d relocations not consumed, first at +0x%x", len(relset)-len(used), first)
	}
	return &Tree{Tag: string(raw[4:8]), Version: version, Align: align, Root: root}, nil
}

// Write lays the Tree out the way the build tool did and returns the file bytes.
func Write(t *Tree) ([]byte, error) {
	version := t.Version
	var buf []byte
	var relocs []int
	le := binary.LittleEndian
	alloc := func(size, align int) int {
		for len(buf)%align != 0 {
			buf = append(buf, pad)
		}
		off := len(buf)
		buf = append(buf, make([]byte, size)...)
		return off
	}
	writeScalars := func(tn string, obj *Node, off int) error {
		p := off
		for _, fd := range types[tn].fields(version) {
			v := obj.F[fd.name]
			switch fd.kind.k {
			case "u32":
				le.PutUint32(buf[p:], U32(v))
			case "i32":
				le.PutUint32(buf[p:], uint32(v.(int32)))
			case "f32":
				le.PutUint32(buf[p:], math.Float32bits(v.(float32)))
			case "f32n":
				x := v.([]float32)
				if len(x) != fd.kind.n {
					return fmt.Errorf("bmdl: %s.%s needs %d values", tn, fd.name, fd.kind.n)
				}
				for i, f := range x {
					le.PutUint32(buf[p+4*i:], math.Float32bits(f))
				}
			case "u32n":
				x := v.([]uint32)
				if len(x) != fd.kind.n {
					return fmt.Errorf("bmdl: %s.%s needs %d values", tn, fd.name, fd.kind.n)
				}
				for i, u := range x {
					le.PutUint32(buf[p+4*i:], u)
				}
			}
			p += fd.kind.width()
		}
		return nil
	}
	counts := func(tn string, obj *Node) {
		for _, fd := range types[tn].fields(version) {
			switch fd.kind.k {
			case "arr", "farr", "uarr":
				obj.F[fd.kind.count] = uint32(listLen(obj.F[fd.name]))
			}
		}
	}
	place := func(k kind, value any) (int, error) {
		switch k.k {
		case "str":
			s := value.(string)
			off := alloc(len(s)+1, 1)
			copy(buf[off:], s)
			return off, nil
		case "blob":
			b := value.([]byte)
			off := alloc(len(b), blobAlign[k.typ])
			copy(buf[off:], b)
			return off, nil
		case "farr":
			x := value.([]float32)
			off := alloc(4*len(x), 4)
			for i, f := range x {
				le.PutUint32(buf[off+4*i:], math.Float32bits(f))
			}
			return off, nil
		case "uarr":
			x := value.([]uint32)
			off := alloc(4*len(x), 4)
			for i, u := range x {
				le.PutUint32(buf[off+4*i:], u)
			}
			return off, nil
		}
		items := structItems(k, value)
		size := typeSize(k.typ, version)
		off := alloc(size*len(items), types[k.typ].align)
		for i, it := range items {
			counts(k.typ, it)
			if err := writeScalars(k.typ, it, off+i*size); err != nil {
				return 0, err
			}
		}
		return off, nil
	}
	type placed struct {
		k   kind
		v   any
		off int
	}
	var descend func(k kind, value any, off int) error
	descend = func(k kind, value any, off int) error {
		if k.k != "arr" && k.k != "arr1" {
			return nil
		}
		size := typeSize(k.typ, version)
		for i, it := range structItems(k, value) {
			var done []placed
			p := off + i*size
			for _, fd := range types[k.typ].fields(version) {
				v := it.F[fd.name]
				if fd.kind.scalar() {
					p += fd.kind.width()
					continue
				}
				if groupBreaks[k.typ] == fd.name {
					for _, d := range done {
						if err := descend(d.k, d.v, d.off); err != nil {
							return err
						}
					}
					done = nil
				}
				if !isNone(v) && !isEmptyList(v) {
					tg, err := place(fd.kind, v)
					if err != nil {
						return err
					}
					le.PutUint32(buf[p:], uint32(tg))
					relocs = append(relocs, p)
					done = append(done, placed{fd.kind, v, tg})
				}
				p += fd.kind.width()
			}
			for _, d := range done {
				if err := descend(d.k, d.v, d.off); err != nil {
					return err
				}
			}
		}
		return nil
	}
	root := t.Root
	counts("Root", root)
	alloc(typeSize("Root", version), 4)
	if err := writeScalars("Root", root, 0); err != nil {
		return nil, err
	}
	p := 0
	for _, fd := range types["Root"].fields(version) {
		v := root.F[fd.name]
		if (fd.kind.k == "arr" || fd.kind.k == "arr1") && !isNone(v) && !isEmptyList(v) {
			tg, err := place(fd.kind, v)
			if err != nil {
				return nil, err
			}
			le.PutUint32(buf[p:], uint32(tg))
			relocs = append(relocs, p)
			if err := descend(fd.kind, v, tg); err != nil {
				return nil, err
			}
		}
		p += 4
	}
	sort.Ints(relocs)
	align := int(t.Align)
	if align <= 0 {
		return nil, fmt.Errorf("bmdl: alignment %d", align)
	}
	head := make([]byte, 32, 32+4*len(relocs)+align)
	le.PutUint32(head[0:], 1)
	copy(head[4:8], t.Tag)
	le.PutUint32(head[8:], version)
	le.PutUint32(head[16:], uint32(len(buf)))
	le.PutUint32(head[20:], uint32(align))
	le.PutUint32(head[28:], uint32(len(relocs)))
	for _, r := range relocs {
		head = le.AppendUint32(head, uint32(r))
	}
	for len(head)%align != 0 {
		head = append(head, pad)
	}
	le.PutUint32(head[12:], uint32(len(head)))
	return append(head, buf...), nil
}

func structItems(k kind, value any) []*Node {
	if k.k == "arr1" {
		return []*Node{value.(*Node)}
	}
	return value.([]*Node)
}

// deepCopy copies a Tree value (a Node, its lists and blobs).
func deepCopy(v any) any {
	switch x := v.(type) {
	case *Node:
		return x.Clone()
	case []*Node:
		out := make([]*Node, len(x))
		for i, n := range x {
			out[i] = n.Clone()
		}
		return out
	case []byte:
		return append(make([]byte, 0, len(x)), x...)
	case []float32:
		return append(make([]float32, 0, len(x)), x...)
	case []uint32:
		return append(make([]uint32, 0, len(x)), x...)
	}
	return v
}

func (n *Node) Clone() *Node {
	out := &Node{Type: n.Type, F: make(map[string]any, len(n.F))}
	for k, v := range n.F {
		out.F[k] = deepCopy(v)
	}
	return out
}
