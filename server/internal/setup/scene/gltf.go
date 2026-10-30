// Package scene turns the game's models and a map's placements into files a browser or a 3-D program can draw:
// glTF 2.0 (a .gltf with its .bin) per model, the textures as PNG, and a scene list of what a level or a map package
// places where. Everything is read from the player's own copy and written to a cache folder the caller names; the
// exports are game content and are not meant to be shipped or committed.
//
// Coordinates: the game is Z up (x east, y north); glTF is Y up, so a game point (x, y, z) is written as (x, z, -y)
// (a rotation, so the triangles keep their winding). The scene lists keep the game's coordinates.
package scene

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"dawngate/server/internal/setup/bmdl"
	"dawngate/server/internal/setup/dds"
)

// Version is the export format's version; a cached export of another version (or another source) is made again.
const Version = 3

// TextureDir is the folder (inside the output folder) the textures go to; the glTF files name them relative to it.
const TextureDir = "textures"

// Exporter writes models and textures into one output folder.
type Exporter struct {
	Dir        string                            // the output folder
	MaxTexture int                               // a texture's longest side is halved down to this (0: 1024)
	Read       func(path string) ([]byte, error) // a file of the copy (the depot)
	textures   map[string]string                 // texture path -> file name (written or failed: "")
}

// ModelInfo is what an export wrote.
type ModelInfo struct {
	Source    string     `json:"source"`    // the model's path in the copy, or what made it
	File      string     `json:"file"`      // the .gltf, relative to the output folder
	Meshes    int        `json:"meshes"`    // glTF meshes (the model's parts)
	Triangles int        `json:"triangles"` //
	Textures  []string   `json:"textures"`  // the PNGs, relative to the output folder
	Missing   []string   `json:"missing,omitempty"`
	Min       [3]float32 `json:"min"` // the game's coordinates
	Max       [3]float32 `json:"max"`
	Cached    bool       `json:"cached,omitempty"`
}

var unsafeName = regexp.MustCompile(`[^a-z0-9_.-]+`)

// FileName is a copy path's export name: lower case, folders joined by "_", unsafe characters replaced.
func FileName(path, ext string) string {
	p := strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	p = strings.TrimSuffix(p, filepath.Ext(p))
	p = unsafeName.ReplaceAllString(strings.ReplaceAll(strings.Trim(p, "/"), "/", "_"), "_")
	return p + ext
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Model exports a model of the copy (its path) as <name>.gltf and <name>.bin with its textures. An export of the
// same source bytes and Version already in the folder is kept.
func (x *Exporter) Model(path string) (ModelInfo, error) {
	raw, err := x.Read(path)
	if err != nil {
		return ModelInfo{}, err
	}
	name := FileName(path, "")
	source := strings.ToLower(strings.ReplaceAll(path, `\`, "/")) // as the client matches paths
	return x.write(name, source, digest(raw), func() (*bmdl.Model, error) { return bmdl.Decode(raw) })
}

// Generated exports a model made by the caller (no source file in the copy) under the given name; tag identifies
// its content for the cache (e.g. the hash of what it was made from).
func (x *Exporter) Generated(name, tag string, m *bmdl.Model) (ModelInfo, error) {
	return x.write(name, "generated:"+name, tag, func() (*bmdl.Model, error) { return m, nil })
}

type cacheTag struct {
	Version int       `json:"version"`
	Source  string    `json:"source"`
	SHA256  string    `json:"sha256"`
	Info    ModelInfo `json:"info"`
}

func (x *Exporter) write(name, source, tag string, model func() (*bmdl.Model, error)) (ModelInfo, error) {
	gltfPath := filepath.Join(x.Dir, name+".gltf")
	if old, err := os.ReadFile(gltfPath); err == nil {
		var doc struct {
			Asset struct {
				Extras cacheTag `json:"extras"`
			} `json:"asset"`
		}
		if json.Unmarshal(old, &doc) == nil && doc.Asset.Extras.Version == Version && doc.Asset.Extras.SHA256 == tag &&
			doc.Asset.Extras.Source == source {
			if _, err := os.Stat(filepath.Join(x.Dir, name+".bin")); err == nil {
				info := doc.Asset.Extras.Info
				info.Cached = true
				return info, nil
			}
		}
	}
	m, err := model()
	if err != nil {
		return ModelInfo{}, err
	}
	doc, bin, info, err := x.build(name, m)
	if err != nil {
		return ModelInfo{}, err
	}
	info.Source = source
	doc.Asset.Extras = cacheTag{Version: Version, Source: source, SHA256: tag, Info: info}
	js, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return ModelInfo{}, err
	}
	if err := os.MkdirAll(x.Dir, 0o755); err != nil {
		return ModelInfo{}, err
	}
	if err := os.WriteFile(filepath.Join(x.Dir, name+".bin"), bin, 0o644); err != nil {
		return ModelInfo{}, err
	}
	if err := os.WriteFile(gltfPath, js, 0o644); err != nil {
		return ModelInfo{}, err
	}
	return info, nil
}

// ---------------------------------------------------------------- glTF

type gltfDoc struct {
	Asset struct {
		Version   string   `json:"version"`
		Generator string   `json:"generator"`
		Extras    cacheTag `json:"extras"`
	} `json:"asset"`
	ExtensionsUsed []string       `json:"extensionsUsed,omitempty"`
	Scene          int            `json:"scene"`
	Scenes         []gltfScene    `json:"scenes"`
	Nodes          []gltfNode     `json:"nodes"`
	Meshes         []gltfMesh     `json:"meshes"`
	Materials      []gltfMaterial `json:"materials,omitempty"`
	Textures       []gltfTexture  `json:"textures,omitempty"`
	Images         []gltfImage    `json:"images,omitempty"`
	Samplers       []gltfSampler  `json:"samplers,omitempty"`
	Accessors      []gltfAccessor `json:"accessors"`
	BufferViews    []gltfView     `json:"bufferViews"`
	Buffers        []gltfBuffer   `json:"buffers"`
}

type gltfScene struct {
	Nodes []int `json:"nodes"`
}
type gltfNode struct {
	Name string `json:"name,omitempty"`
	Mesh int    `json:"mesh"`
}
type gltfMesh struct {
	Name       string          `json:"name,omitempty"`
	Primitives []gltfPrimitive `json:"primitives"`
}
type gltfPrimitive struct {
	Attributes map[string]int `json:"attributes"`
	Indices    int            `json:"indices"`
	Material   *int           `json:"material,omitempty"`
}
type gltfTexRef struct {
	Index int `json:"index"`
}
type gltfMaterial struct {
	Name string `json:"name,omitempty"`
	PBR  struct {
		BaseColorTexture *gltfTexRef `json:"baseColorTexture,omitempty"`
		BaseColorFactor  []float32   `json:"baseColorFactor,omitempty"`
		MetallicFactor   float32     `json:"metallicFactor"`
		RoughnessFactor  float32     `json:"roughnessFactor"`
	} `json:"pbrMetallicRoughness"`
	AlphaMode   string         `json:"alphaMode,omitempty"`
	AlphaCutoff float32        `json:"alphaCutoff,omitempty"`
	DoubleSided bool           `json:"doubleSided,omitempty"`
	Extensions  map[string]any `json:"extensions,omitempty"`
	Extras      MaterialExtras `json:"extras"`
}

// MaterialExtras say how the game's shader draws a material, for a viewer that approximates it: "opaque", "mask"
// (cut out at alpha 0.5), "blend" or "add" (additive); lit or unlit; whether the vertex colours multiply.
type MaterialExtras struct {
	Shader    string   `json:"shader"`
	Technique string   `json:"technique,omitempty"`
	Draw      string   `json:"draw"`
	Unlit     bool     `json:"unlit,omitempty"`
	VertColor bool     `json:"vertColor,omitempty"`
	Textures  []string `json:"textures,omitempty"` // slot=path, every slot
}
type gltfTexture struct {
	Source  int `json:"source"`
	Sampler int `json:"sampler"`
}
type gltfImage struct {
	URI string `json:"uri"`
}
type gltfSampler struct {
	MagFilter int `json:"magFilter"`
	MinFilter int `json:"minFilter"`
	WrapS     int `json:"wrapS"`
	WrapT     int `json:"wrapT"`
}
type gltfAccessor struct {
	BufferView    int       `json:"bufferView"`
	ByteOffset    int       `json:"byteOffset,omitempty"`
	ComponentType int       `json:"componentType"`
	Normalized    bool      `json:"normalized,omitempty"`
	Count         int       `json:"count"`
	Type          string    `json:"type"`
	Min           []float32 `json:"min,omitempty"`
	Max           []float32 `json:"max,omitempty"`
}
type gltfView struct {
	Buffer     int `json:"buffer"`
	ByteOffset int `json:"byteOffset"`
	ByteLength int `json:"byteLength"`
	ByteStride int `json:"byteStride,omitempty"`
	Target     int `json:"target,omitempty"`
}
type gltfBuffer struct {
	URI        string `json:"uri"`
	ByteLength int    `json:"byteLength"`
}

const (
	glFloat         = 5126
	glUnsignedByte  = 5121
	glUnsignedShort = 5123
	glArrayBuffer   = 34962
	glElementBuffer = 34963
	glLinear        = 9729
	glLinearMipmap  = 9987
	glRepeat        = 10497
)

// DrawMode is how a viewer should draw a material of the game's shader, judged by the shader's name: the additive
// shaders (labsUnlit...Additive, labsSphereMaskedScrollAdditive) and the unlit scrolling ones (labsUnlitScroll,
// labsUnlitEnvLayeredScroll: the Cores' energy streams, waterfalls) add, the unlit blend and masked ones blend, the
// flora cuts out, everything else is opaque. The vertex colours tint only the vertex-lit ground (labsVertLit) and
// labsGenericVertColors; elsewhere (the flora, the chrome, the effects) they hold other data [assumed: drawn as
// colour they turned trees and waterfalls black; drawn opaque the energy streams were solid white].
func DrawMode(shader string) (draw string, unlit, vertColor bool) {
	s := strings.ToLower(shader)
	additive := strings.Contains(s, "additive") ||
		(strings.HasPrefix(s, "labsunlit") && strings.Contains(s, "scroll") && !strings.Contains(s, "masked"))
	unlit = strings.HasPrefix(s, "labsunlit") || additive || strings.Contains(s, "energyline") ||
		strings.Contains(s, "rune")
	vertColor = s == "labsvertlit" || strings.HasPrefix(s, "labsgenericvertcolor")
	switch {
	case additive:
		draw = "add"
	case unlit && (strings.Contains(s, "blend") || strings.Contains(s, "masked")):
		draw = "blend"
	case strings.HasPrefix(s, "labsflora"):
		draw = "mask"
	default:
		draw = "opaque"
	}
	return
}

// diffuse picks a material's colour texture: the diffuseMap slot, else the first.
func diffuse(m bmdl.Material) string {
	for _, t := range m.Textures {
		if strings.EqualFold(t.Slot, "diffuseMap") {
			return t.Path
		}
	}
	if len(m.Textures) > 0 {
		return m.Textures[0].Path
	}
	return ""
}

func (x *Exporter) build(name string, m *bmdl.Model) (*gltfDoc, []byte, ModelInfo, error) {
	doc := &gltfDoc{}
	doc.Asset.Version, doc.Asset.Generator = "2.0", "mapc (Dawngate .bmdl)"
	info := ModelInfo{File: name + ".gltf"}
	var bin bytes.Buffer
	le := binary.LittleEndian
	align := func() {
		for bin.Len()%4 != 0 {
			bin.WriteByte(0)
		}
	}
	view := func(b []byte, target int) int {
		align()
		doc.BufferViews = append(doc.BufferViews, gltfView{ByteOffset: bin.Len(), ByteLength: len(b), Target: target})
		bin.Write(b)
		return len(doc.BufferViews) - 1
	}
	accessor := func(a gltfAccessor) int {
		doc.Accessors = append(doc.Accessors, a)
		return len(doc.Accessors) - 1
	}
	// materials
	doc.Samplers = []gltfSampler{{MagFilter: glLinear, MinFilter: glLinearMipmap, WrapS: glRepeat, WrapT: glRepeat}}
	texIndex := map[string]int{}
	unlitUsed := false
	for k, mt := range m.Materials {
		var gm gltfMaterial
		gm.Name = fmt.Sprintf("%s_%d", mt.Shader, k)
		gm.PBR.MetallicFactor, gm.PBR.RoughnessFactor = 0, 1
		draw, unlit, vc := DrawMode(mt.Shader)
		gm.Extras = MaterialExtras{Shader: mt.Shader, Technique: mt.Technique, Draw: draw, Unlit: unlit, VertColor: vc}
		for _, t := range mt.Textures {
			gm.Extras.Textures = append(gm.Extras.Textures, t.Slot+"="+t.Path)
		}
		switch draw {
		case "mask":
			gm.AlphaMode, gm.AlphaCutoff, gm.DoubleSided = "MASK", 0.5, true
		case "blend", "add":
			gm.AlphaMode, gm.DoubleSided = "BLEND", true
		}
		if unlit {
			gm.Extensions = map[string]any{"KHR_materials_unlit": map[string]any{}}
			unlitUsed = true
		}
		if tint, ok := mt.Params["DiffuseTint"]; ok && len(tint) >= 3 {
			gm.PBR.BaseColorFactor = []float32{tint[0], tint[1], tint[2], 1}
		}
		if p := diffuse(mt); p != "" {
			file, err := x.Texture(p)
			if err != nil || file == "" {
				info.Missing = append(info.Missing, p)
			} else {
				ti, ok := texIndex[file]
				if !ok {
					doc.Images = append(doc.Images, gltfImage{URI: file})
					doc.Textures = append(doc.Textures, gltfTexture{Source: len(doc.Images) - 1})
					ti = len(doc.Textures) - 1
					texIndex[file] = ti
					info.Textures = append(info.Textures, file)
				}
				gm.PBR.BaseColorTexture = &gltfTexRef{Index: ti}
			}
		}
		doc.Materials = append(doc.Materials, gm)
	}
	if unlitUsed {
		doc.ExtensionsUsed = []string{"KHR_materials_unlit"}
	}
	// vertex data per mesh
	type meshAcc struct {
		attrs map[string]int
		ib    int
	}
	accs := make([]meshAcc, len(m.Meshes))
	lo := [3]float32{float32(math.Inf(1)), float32(math.Inf(1)), float32(math.Inf(1))}
	hi := [3]float32{float32(math.Inf(-1)), float32(math.Inf(-1)), float32(math.Inf(-1))}
	for k, me := range m.Meshes {
		n := len(me.Positions)
		attrs := map[string]int{}
		pos := make([]byte, 0, 12*n)
		amin := [3]float32{float32(math.Inf(1)), float32(math.Inf(1)), float32(math.Inf(1))}
		amax := [3]float32{float32(math.Inf(-1)), float32(math.Inf(-1)), float32(math.Inf(-1))}
		for _, p := range me.Positions {
			q := [3]float32{p[0], p[2], -p[1]}
			for c := 0; c < 3; c++ {
				pos = le.AppendUint32(pos, math.Float32bits(q[c]))
				amin[c], amax[c] = min(amin[c], q[c]), max(amax[c], q[c])
				lo[c], hi[c] = min(lo[c], p[c]), max(hi[c], p[c])
			}
		}
		attrs["POSITION"] = accessor(gltfAccessor{BufferView: view(pos, glArrayBuffer), ComponentType: glFloat,
			Count: n, Type: "VEC3", Min: amin[:], Max: amax[:]})
		if me.Normals != nil {
			b := make([]byte, 0, 12*n)
			for _, v := range me.Normals {
				q := [3]float32{v[0], v[2], -v[1]}
				l := float32(math.Sqrt(float64(q[0]*q[0] + q[1]*q[1] + q[2]*q[2])))
				if l < 1e-6 {
					q, l = [3]float32{0, 1, 0}, 1
				}
				for c := 0; c < 3; c++ {
					b = le.AppendUint32(b, math.Float32bits(q[c]/l))
				}
			}
			attrs["NORMAL"] = accessor(gltfAccessor{BufferView: view(b, glArrayBuffer), ComponentType: glFloat, Count: n,
				Type: "VEC3"})
		}
		if me.UVs != nil {
			b := make([]byte, 0, 8*n)
			for _, v := range me.UVs {
				b = le.AppendUint32(b, math.Float32bits(v[0]))
				b = le.AppendUint32(b, math.Float32bits(v[1]))
			}
			attrs["TEXCOORD_0"] = accessor(gltfAccessor{BufferView: view(b, glArrayBuffer), ComponentType: glFloat,
				Count: n, Type: "VEC2"})
		}
		if me.Colors != nil {
			b := make([]byte, 0, 4*n)
			for _, c := range me.Colors {
				b = append(b, c[0], c[1], c[2], c[3])
			}
			attrs["COLOR_0"] = accessor(gltfAccessor{BufferView: view(b, glArrayBuffer), ComponentType: glUnsignedByte,
				Normalized: true, Count: n, Type: "VEC4"})
		}
		ib := make([]byte, 2*len(me.Indices))
		for q, v := range me.Indices {
			le.PutUint16(ib[2*q:], v)
		}
		accs[k] = meshAcc{attrs: attrs, ib: view(ib, glElementBuffer)}
	}
	// one glTF mesh per part, a primitive per submesh
	parts := m.Parts
	if len(parts) == 0 {
		for k, me := range m.Meshes {
			parts = append(parts, bmdl.Part{Mesh: k, Submeshes: []bmdl.Submesh{{Start: 0, Count: len(me.Indices),
				Material: me.Material}}})
		}
	}
	for pi, p := range parts {
		gmesh := gltfMesh{Name: m.Meshes[p.Mesh].Name}
		for _, s := range p.Submeshes {
			if s.Count == 0 {
				continue
			}
			ia := accessor(gltfAccessor{BufferView: accs[p.Mesh].ib, ByteOffset: 2 * s.Start,
				ComponentType: glUnsignedShort, Count: s.Count, Type: "SCALAR"})
			prim := gltfPrimitive{Attributes: accs[p.Mesh].attrs, Indices: ia}
			if s.Material >= 0 && s.Material < len(doc.Materials) {
				mi := s.Material
				prim.Material = &mi
			}
			gmesh.Primitives = append(gmesh.Primitives, prim)
			info.Triangles += s.Count / 3
		}
		if len(gmesh.Primitives) == 0 {
			continue
		}
		doc.Meshes = append(doc.Meshes, gmesh)
		doc.Nodes = append(doc.Nodes, gltfNode{Name: fmt.Sprintf("part%d", pi), Mesh: len(doc.Meshes) - 1})
	}
	if len(doc.Meshes) == 0 {
		return nil, nil, info, fmt.Errorf("%s: nothing to draw", name)
	}
	nodes := make([]int, len(doc.Nodes))
	for k := range nodes {
		nodes[k] = k
	}
	doc.Scenes = []gltfScene{{Nodes: nodes}}
	align()
	doc.Buffers = []gltfBuffer{{URI: name + ".bin", ByteLength: bin.Len()}}
	info.Meshes, info.Min, info.Max = len(doc.Meshes), lo, hi
	return doc, bin.Bytes(), info, nil
}

// ---------------------------------------------------------------- textures

// Texture exports a .dds of the copy as textures/<name>.png (its top level, halved to MaxTexture) and returns the
// file relative to the output folder; a texture already exported in this run, or present from an earlier one, is
// not written again.
func (x *Exporter) Texture(path string) (string, error) {
	if x.textures == nil {
		x.textures = map[string]string{}
	}
	if f, ok := x.textures[strings.ToLower(path)]; ok {
		return f, nil
	}
	x.textures[strings.ToLower(path)] = ""
	raw, err := x.Read(path)
	if err != nil {
		return "", err
	}
	file, err := x.TextureBytes(FileName(path, ""), raw)
	if err != nil {
		return "", err
	}
	x.textures[strings.ToLower(path)] = file
	return file, nil
}

// TextureBytes exports a .dds held in memory under a name; the name carries a hash of the bytes, so a changed
// texture gets a new file and an unchanged one is not written again.
func (x *Exporter) TextureBytes(name string, raw []byte) (string, error) {
	file := TextureDir + "/" + name + "_" + digest(raw)[:8] + ".png"
	out := filepath.Join(x.Dir, filepath.FromSlash(file))
	if _, err := os.Stat(out); err == nil {
		return file, nil
	}
	w, h, rgba, err := dds.Decode(raw)
	if err != nil {
		return "", err
	}
	limit := x.MaxTexture
	if limit <= 0 {
		limit = 1024
	}
	for (w > limit || h > limit) && w >= 2 && h >= 2 {
		w, h, rgba = dds.Downsample(w, h, rgba)
	}
	img := &image.NRGBA{Pix: rgba, Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&b, img); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(out, b.Bytes(), 0o644); err != nil {
		return "", err
	}
	return file, nil
}
