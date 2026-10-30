// Command mmvoid writes an experimental zz_MM_Map_v3.snap into a test copy: the release's archive (mapmm.Build on
// the copy's own game files) with the ground cut back to the walkable floor and the first metres past its edge
// (ground.Spec.Cut), so what lies around the level, past its ground, can be seen in game. The copy's archive is kept
// beside it as zz_MM_Map_v3.snap.orig (written once); --restore puts it back. --backdrop adds a plane --backdrop-z
// metres below the floor, +-700 m wide (ground.Spec.Backdrop): plain is one colour (the cloud texture's sky), black
// black (with --backdrop-shader unlit: what the void looked like, without the stale frames), clouds the shipped
// anshavar1 CloudMapAlpha_D.dds made opaque (its colour channels are a dark blue sky with clouds). --fog-start and
// --fog-end (and --fog-color) set the level's black height fog (its GraphicsControl in MM_Map_v3_lights.Markerset, 10 and -225 as
// shipped): the archive then carries that markerset too, edited. --mist-z adds a see-through plane above the
// backdrop (ground.Spec.Mist) on the shipped labsFogFace material (anshavar3's fogspheretest.bmdl): DiffuseTint
// --mist-color and FogRange --mist-range.
//
// A map package states the same look in its void section (the map compiler, internal/setup/mapc/void.go), built by
// the same code (ground/void.go): for the same values its planes and their materials are this tool's.
//
// Not for a player copy: the release setup (Start-Client, play.ps1, setup_lane.py) writes the reference archive again.
//
// Usage (from server\):
//
//	go run ./tools/mmvoid [--cut 1.5] [--backdrop none|plain|black|clouds] [--backdrop-z -50] [--backdrop-flags 0] [--backdrop-shader unlit]
//	   [--backdrop-emissive 1] [--fog-start 10 --fog-end -225 [--fog-color 0,0,0]]
//	   [--mist-z -2 [--mist-range 100] [--mist-color 0.3,0.34,0.45]] <copy>
//	go run ./tools/mmvoid --restore <copy>
//
// <copy> is the folder with game\ (e.g. client\run_854_c\Dawngate).
package main

import (
	"bytes"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/bmdl"
	"dawngate/server/internal/setup/dds"
	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/mapmm"
	"dawngate/server/internal/setup/mapmm/ground"
	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/snap"
)

const (
	cloudArchive = "game/Data/anshavar1.snap"
	cloudEntry   = "environments/anshavar/textures/CloudMapAlpha_D.dds"
	backdropTex  = "environments/prototype/textures/mm_v3_backdrop_D.dds"
	lightsEntry  = "AssetData/Binary/MM_Map_v3_lights.Markerset.bin"
)

// shipped reads an entry of the copy's game/Data/<archive>.
func shipped(copy, archive, entry string) ([]byte, error) {
	arc, err := os.ReadFile(filepath.Join(copy, "game", "Data", archive))
	if err != nil {
		return nil, err
	}
	return los.FromArchive(arc, entry)
}

// fogFaceMaterial is the fog sphere's labsFogFace material with DiffuseTint (values 0-2) and FogRange (value 3) set.
func fogFaceMaterial(copy string, color []float64, rng float64) (*bmdl.Node, error) {
	raw, err := shipped(copy, ground.FogFaceArchive, ground.FogFaceEntry)
	if err != nil {
		return nil, err
	}
	return ground.FogFaceMaterial(raw, [3]float64{color[0], color[1], color[2]}, rng)
}

// rgb parses "r,g,b".
func rgb(s string) ([]float64, error) {
	var out []float64
	for _, f := range strings.Split(s, ",") {
		v, err := strconv.ParseFloat(strings.TrimSpace(f), 64)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if len(out) != 3 {
		return nil, fmt.Errorf("%q: want r,g,b", s)
	}
	return out, nil
}

// fogLights is MM_Map_v3_lights.Markerset (from AssetData.snap) with its GraphicsControl's height fog from start (z,
// metres) down to end. The parsed asset must write back to the same bytes before it is changed.
func fogLights(assetData []byte, start, end float64, color []float64) ([]byte, error) {
	a, err := snap.Parse(assetData)
	if err != nil {
		return nil, err
	}
	var raw []byte
	for _, e := range a.Entries {
		if e.Name == lightsEntry {
			if raw, err = a.Read(e); err != nil {
				return nil, err
			}
		}
	}
	if raw == nil {
		return nil, fmt.Errorf("AssetData.snap has no %s", lightsEntry)
	}
	ts, err := assetdata.Embedded()
	if err != nil {
		return nil, err
	}
	t := ts.Type("Markerset")
	o, err := ts.ParseExact(t, raw)
	if err != nil {
		return nil, err
	}
	if same, err := ts.Write(t, o, assetdata.WriteOptions{Like: raw}); err != nil || !bytes.Equal(same, raw) {
		return nil, fmt.Errorf("%s: does not write back unchanged (%v)", lightsEntry, err)
	}
	var c *[3]float64
	if color != nil {
		c = &[3]float64{color[0], color[1], color[2]}
	}
	if n := mapc.SetHeightFog(o, start, end, c); n != 1 {
		return nil, fmt.Errorf("%s: %d graphics controls, want 1", lightsEntry, n)
	}
	return ts.Write(t, o, assetdata.WriteOptions{Like: raw})
}

// unlitMaterial is the parasite's labsUnlit material with EmissiveLevel (value 3) set and GlowLevel (value 8, 8.0
// on the tube: its bloom) 0.
func unlitMaterial(copy string, emissive float64) (*bmdl.Node, error) {
	raw, err := shipped(copy, ground.UnlitArchive, ground.UnlitEntry)
	if err != nil {
		return nil, err
	}
	return ground.UnlitMaterial(raw, emissive)
}

func main() {
	cut := flag.Float64("cut", 1.5, "keep the ground up to this many metres past the walkable edge")
	restore := flag.Bool("restore", false, "put the copy's own archive back")
	back := flag.String("backdrop", "none", "none, plain, black or clouds: a plane under the level")
	backZ := flag.Float64("backdrop-z", -50, "the backdrop's height (metres)")
	backFlags := flag.Int64("backdrop-flags", -1, "the backdrop material's flags (-1: the dirt's, 0x106)")
	backShader := flag.String("backdrop-shader", "lit", "lit (the dirt's labsVertLit) or unlit (the parasite's labsUnlit)")
	backEmissive := flag.Float64("backdrop-emissive", 1, "unlit: the material's EmissiveLevel")
	fogStart := flag.Float64("fog-start", math.NaN(), "the height fog's start (z, metres; shipped 10)")
	fogColor := flag.String("fog-color", "", "the height fog's colour, r,g,b from 0 to 1 (shipped 0,0,0)")
	mistZ := flag.Float64("mist-z", math.NaN(), "a see-through mist plane at this height (metres; none if unset)")
	mistRange := flag.Float64("mist-range", 100, "the mist's FogRange (the fog sphere's: 110)")
	mistColor := flag.String("mist-color", "0.3,0.34,0.45", "the mist's colour, r,g,b from 0 to 1")
	fogEnd := flag.Float64("fog-end", math.NaN(), "the height fog's end, where it is full (z, metres; shipped -225)")
	flag.Parse()
	if flag.NArg() != 1 || *cut <= 0 || (*back != "none" && *back != "plain" && *back != "black" && *back != "clouds") {
		fmt.Fprintln(os.Stderr, "usage: mmvoid [--cut <m>] [--backdrop none|plain|black|clouds] [--backdrop-z <m>] | --restore <copy with the game folder>")
		os.Exit(2)
	}
	if math.IsNaN(*fogStart) != math.IsNaN(*fogEnd) {
		fmt.Fprintln(os.Stderr, "mmvoid: --fog-start and --fog-end go together")
		os.Exit(2)
	}
	var color []float64
	if *fogColor != "" {
		var err error
		if color, err = rgb(*fogColor); err != nil {
			fmt.Fprintln(os.Stderr, "mmvoid: --fog-color:", err)
			os.Exit(2)
		}
		if math.IsNaN(*fogStart) {
			fmt.Fprintln(os.Stderr, "mmvoid: --fog-color is r,g,b and needs --fog-start and --fog-end")
			os.Exit(2)
		}
	}
	var mist *mistSpec
	if !math.IsNaN(*mistZ) {
		c, err := rgb(*mistColor)
		if err != nil {
			fmt.Fprintln(os.Stderr, "mmvoid: --mist-color:", err)
			os.Exit(2)
		}
		mist = &mistSpec{z: *mistZ, rng: *mistRange, color: c}
	}
	if err := run(flag.Arg(0), *cut, *back, *backZ, *backFlags, *backShader, *backEmissive, *fogStart, *fogEnd,
		color, mist, *restore); err != nil {
		fmt.Fprintln(os.Stderr, "mmvoid:", err)
		os.Exit(1)
	}
}

// mistSpec is the --mist-* flags.
type mistSpec struct {
	z, rng float64
	color  []float64
}

func run(copy string, cut float64, back string, backZ float64, backFlags int64, backShader string,
	backEmissive, fogStart, fogEnd float64, fogColor []float64, mist *mistSpec, restore bool) error {
	added := filepath.Join(copy, filepath.FromSlash(mapmm.Rel))
	orig := added + ".orig"
	if restore {
		if err := os.Rename(orig, added); err != nil {
			return err
		}
		fmt.Println(added + ": the copy's own archive again")
		return nil
	}
	var in mapmm.Inputs
	for i, dst := range []*[]byte{&in.Images, &in.SMMap, &in.Anshavar3, &in.Anshavar4, &in.AssetData} {
		b, err := os.ReadFile(filepath.Join(copy, filepath.FromSlash(mapmm.Sources[i])))
		if err != nil {
			return err
		}
		*dst = b
	}
	in.GroundCut = cut
	if back != "none" {
		if err := backdrop(copy, back, backZ, &in); err != nil {
			return err
		}
		if backShader == "unlit" {
			mt, err := unlitMaterial(copy, backEmissive)
			if err != nil {
				return err
			}
			in.Backdrop.Material = mt
		}
		if backFlags >= 0 {
			fl := uint32(backFlags)
			in.Backdrop.Flags = &fl
		}
	}
	if mist != nil {
		mt, err := fogFaceMaterial(copy, mist.color, mist.rng)
		if err != nil {
			return err
		}
		in.Mist = &ground.Backdrop{Z: mist.z, Half: ground.BackdropHalf, RepeatX: ground.BackdropRepeat,
			RepeatY: ground.BackdropRepeat, Material: mt}
		fmt.Printf("mist: z %.1f m, FogRange %.0f, colour %v\n", mist.z, mist.rng, mist.color)
	}
	files, err := mapmm.Files(in)
	if err != nil {
		return err
	}
	if !math.IsNaN(fogStart) {
		lights, err := fogLights(in.AssetData, fogStart, fogEnd, fogColor)
		if err != nil {
			return err
		}
		files = append(files, snap.File{Name: lightsEntry, Data: lights})
		fmt.Printf("height fog: from z %.1f to %.1f m\n", fogStart, fogEnd)
	}
	b := snap.Pack(files)
	if _, err := os.Stat(orig); os.IsNotExist(err) {
		if err := os.Rename(added, orig); err != nil {
			return err
		}
	}
	if err := os.WriteFile(added, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: the ground cut %.1f m past the walkable edge, backdrop %s, %d bytes (the copy's own kept as %s)\n",
		added, cut, back, len(b), filepath.Base(orig))
	return nil
}

// backdrop sets in.Backdrop and its texture: the cloud texture made opaque (clouds), its sky's mean colour (plain) or
// black.
func backdrop(copy, kind string, z float64, in *mapmm.Inputs) error {
	arc, err := os.ReadFile(filepath.Join(copy, filepath.FromSlash(cloudArchive)))
	if err != nil {
		return err
	}
	raw, err := los.FromArchive(arc, cloudEntry)
	if err != nil {
		return err
	}
	w, h, px, err := dds.Decode(raw)
	if err != nil {
		return err
	}
	var sum [3]float64
	n := 0
	for o := 0; o < len(px); o += 4 {
		if px[o+3] < 16 { // no cloud: the sky
			for c := 0; c < 3; c++ {
				sum[c] += float64(px[o+c])
			}
			n++
		}
		px[o+3] = 0xFF
	}
	in.Backdrop = &ground.Backdrop{Z: z, Half: ground.BackdropHalf, RepeatX: ground.BackdropRepeat,
		RepeatY: ground.BackdropRepeat / 2, Texture: backdropTex}
	if kind == "plain" || kind == "black" {
		var c [3]byte
		if kind == "plain" {
			for k := range c {
				c[k] = byte(sum[k] / float64(n))
			}
		}
		fmt.Printf("%s backdrop: RGB %d %d %d\n", kind, c[0], c[1], c[2])
		in.Backdrop.RepeatY = ground.BackdropRepeat // a square texture (the map compiler's flat backdrop's, mapc void.go)
		in.BackdropDDS, err = ground.FlatDDS(c)
		return err
	}
	in.BackdropDDS, err = dds.Encode(w, h, px, "dxt1", 0)
	return err
}
