package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"dawngate/server/internal/setup/halloween"
)

// stepHalloween installs a player copy's added archive game\Data\zz_Halloween.snap (setup_all.py's step_halloween with
// re/scripts/halloween.py): the map's Halloween decoration under a level flag, made from the copy's own AssetData.snap,
// which must be the build's. The copy shows the decoration only in a match whose server turns it on (Start-Server.ini
// halloween, dgserver --halloween), so every player copy gets it, whatever server it plays on; the match host doesn't
// need it. Current means byte for byte what halloween makes now.
func (s *Setup) stepHalloween(step, copy string) bool {
	src := filepath.Join(copy, filepath.FromSlash(halloween.Source))
	added := filepath.Join(copy, filepath.FromSlash(halloween.Rel))
	data, err := os.ReadFile(src)
	if err != nil {
		if s.Check {
			return s.report(Missing, step, fmt.Sprintf("%s (the copy is incomplete)", s.show(src)))
		}
		return s.report(Failed, step, err.Error())
	}
	if h := sha256.Sum256(data); hex.EncodeToString(h[:]) != s.untouched(halloween.Source) {
		return s.report(Failed, step, fmt.Sprintf("%s is not the untouched build-854 file (docs/file_manifest.csv); "+
			"delete %s and start again (it is extracted anew)", s.show(src), copy))
	}
	want, err := halloween.Build(data)
	if err != nil {
		return s.report(Failed, step, err.Error())
	}
	h := sha256.Sum256(want)
	line := fmt.Sprintf("%s: the Halloween decoration, shown when the server turns it on (%s)", s.show(added),
		hex.EncodeToString(h[:])[:16])
	have, err := os.ReadFile(added)
	if err == nil && bytes.Equal(have, want) {
		return s.report(OK, step, line)
	}
	why := "an older one"
	if err != nil {
		why = "not there"
	}
	if s.Check {
		st := Stale
		if err != nil {
			st = Missing
		}
		return s.report(st, step, s.show(added)+": "+why)
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
	return s.report(Done, step, fmt.Sprintf("%s (was: %s)", line, why))
}
