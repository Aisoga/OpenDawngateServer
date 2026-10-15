package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"dawngate/server/internal/setup/moviepatch"
)

// stepPicker installs the client copy's server picker, game\Data\zz_ServerPicker.snap (re/scripts/patch_login_movie.py):
// the copy's own login movie with the picker's class added (moviepatch), made from the copy's LoginScreen.snap, which
// must be the build's. Current means byte for byte what moviepatch makes now. Only for Start-Client's picker mode
// (Setup.Picker); without it the archive is removed (removePicker), since the picker's list file is then missing
// and the login screen is the game's own.
func (s *Setup) stepPicker(step, copy string) bool {
	src := filepath.Join(copy, filepath.FromSlash(moviepatch.Source))
	added := filepath.Join(copy, filepath.FromSlash(moviepatch.Rel))
	login, err := os.ReadFile(src)
	if err != nil {
		if s.Check {
			return s.report(Missing, step, fmt.Sprintf("%s (the copy is incomplete)", s.show(src)))
		}
		return s.report(Failed, step, err.Error())
	}
	if h := sha256.Sum256(login); hex.EncodeToString(h[:]) != s.untouched(moviepatch.Source) {
		return s.report(Failed, step, fmt.Sprintf("%s is not the untouched build-854 file (docs/file_manifest.csv); "+
			"delete %s and start again (it is extracted anew)", s.show(src), copy))
	}
	want, err := moviepatch.Build(login)
	if err != nil {
		return s.report(Failed, step, err.Error())
	}
	h := sha256.Sum256(want)
	line := fmt.Sprintf("%s: the login screen's server picker (%s)", s.show(added), hex.EncodeToString(h[:])[:16])
	have, err := os.ReadFile(added)
	if err == nil && bytes.Equal(have, want) {
		return s.report(OK, step, line)
	}
	why := "an older picker"
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

// removePicker takes a server picker of an earlier picker-mode start out of the client copy.
func (s *Setup) removePicker(step, copy string) bool {
	added := filepath.Join(copy, filepath.FromSlash(moviepatch.Rel))
	if _, err := os.Lstat(added); errors.Is(err, os.ErrNotExist) {
		return true
	}
	if s.Check {
		return s.report(Stale, step, s.show(added)+": the server picker of picker mode (removed at the next start)")
	}
	if err := os.Remove(added); err != nil {
		return s.report(Failed, step, fmt.Sprintf("%s: %v (is the game running from this copy?)", s.show(added), err))
	}
	return s.report(Done, step, s.show(added)+" removed (picker mode is off)")
}
