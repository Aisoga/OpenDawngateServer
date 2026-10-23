package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"dawngate/server/internal/setup/mapmm"
)

// stepMapMM installs a copy's added archive game\Data\zz_MM_Map_v3.snap: the second map's level files, which the
// game doesn't ship (the navigation graphs, the PhysX mesh, the fog outlines, the minimap and the ground), made from
// the copy's own game files (mapmm.Build), which must be the build's. Every player copy and the match-host copy get
// it, whatever the server hosts: a server picks the map (Start-Server.ini map, custom_maps), and a player's copy must
// hold the files of every map a server may host. The PhysX mesh ships cooked (physmesh.Cooked) and is used when the
// copy's line-of-sight map has the walls it was cooked from.
//
// Current means: on build 854 the archive's SHA-256 is mapmm.Game854SHA256 (no build needed); on another build
// (the tests' synthetic one) byte for byte what mapmm.Build makes now. A build whose manifest has no images.snap
// (no MM_Map_v3 line-of-sight map) skips the step.
func (s *Setup) stepMapMM(step, copy string) bool {
	added := filepath.Join(copy, filepath.FromSlash(mapmm.Rel))
	if s.untouched(mapmm.Sources[0]) == "" {
		return s.report(Skipped, step, "this build has no "+mapmm.Sources[0])
	}
	pinned := ""
	if s.Manifest == nil {
		pinned = mapmm.Game854SHA256
	}
	line := func(sum string) string {
		return fmt.Sprintf("%s: the second map MM_Map_v3's level files (%s)", s.show(added), sum[:16])
	}
	have, haveErr := os.ReadFile(added)
	if pinned != "" && haveErr == nil && digest(have) == pinned {
		return s.report(OK, step, line(pinned))
	}
	if pinned != "" && s.Check {
		return s.mapMMStale(step, added, haveErr)
	}
	want, err := s.buildMapMM(copy)
	if err != nil {
		if s.Check {
			return s.report(Missing, step, err.Error())
		}
		return s.report(Failed, step, err.Error())
	}
	sum := digest(want)
	if haveErr == nil && bytes.Equal(have, want) {
		return s.report(OK, step, line(sum))
	}
	if s.Check {
		return s.mapMMStale(step, added, haveErr)
	}
	note := ""
	if pinned != "" && sum != pinned {
		// the files are deterministic on the build's inputs, so this would be a fault of this code: the archive is
		// still written (the game loads it), and the line says so
		note = fmt.Sprintf("; not the reference archive %s", pinned[:16])
	}
	tmp := added + ".partial"
	err = os.WriteFile(tmp, want, 0o644)
	if err == nil {
		err = os.Rename(tmp, added)
	}
	if err != nil {
		os.Remove(tmp)
		return s.report(Failed, step, fmt.Sprintf("%s: %v (is the game running from this copy?)", s.show(added), err))
	}
	why := "an older one"
	if haveErr != nil {
		why = "not there"
	}
	return s.report(Done, step, fmt.Sprintf("%s%s (was: %s)", line(sum), note, why))
}

// MapMM runs only the MM_Map_v3 step on one copy (the developers' setups call it through server/tools/mapmm): nil
// when the archive is current or was written, ErrIncomplete otherwise (Bad says why).
func (s *Setup) MapMM(step, copy string) error {
	s.stepMapMM(step, copy)
	return s.result()
}

func (s *Setup) mapMMStale(step, added string, haveErr error) bool {
	if haveErr != nil {
		return s.report(Missing, step, s.show(added)+": not there")
	}
	return s.report(Stale, step, s.show(added)+": an older one")
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// buildMapMM reads the copy's inputs (each checked against the manifest) and builds the archive; a second copy of
// the same setup reuses the first one's bytes (the inputs are the build's either way).
func (s *Setup) buildMapMM(copy string) ([]byte, error) {
	if s.mapMM != nil {
		return s.mapMM, nil
	}
	read := func(rel string) ([]byte, error) {
		p := filepath.Join(copy, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("%s (the copy is incomplete)", s.show(p))
		}
		if digest(b) != s.untouched(rel) {
			return nil, fmt.Errorf("%s is not the untouched file of the build (docs/file_manifest.csv); delete %s "+
				"and start again (it is extracted anew)", s.show(p), copy)
		}
		return b, nil
	}
	var in mapmm.Inputs
	for i, dst := range []*[]byte{&in.Images, &in.SMMap, &in.Anshavar3, &in.Anshavar4, &in.AssetData} {
		b, err := read(mapmm.Sources[i])
		if err != nil {
			return nil, err
		}
		*dst = b
	}
	b, err := mapmm.Build(in)
	if err != nil {
		return nil, err
	}
	s.mapMM = b
	return b, nil
}
