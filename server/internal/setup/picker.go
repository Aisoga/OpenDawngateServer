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
	return s.stepMovie(step, copy, moviepatch.Picker, "the login screen's server picker", "an older picker")
}

// stepMatchOptions installs the CREATE GAME dialog's "Fill open seats with bots" check box,
// game\Data\zz_MatchOptions.snap: the copy's own MatchOptionsUI movie (from Battlegrounds.snap, which must be the
// build's) with the check box's class added (moviepatch.MatchOptions). Every player copy gets it: ticked, the class
// ends the game's name with the marker the server reads (blaze.BotFillMarker); unticked it changes nothing. The same
// archive holds the dialog's map list: the maps besides The Dawngate whose archives the copy has (moviepatch.CopyMaps),
// which the class offers in the dialog's map dropdown (none: no dropdown). A step that adds such a map's archive runs
// before this one. Start-Client's front narrows the list in place to the maps the server of each login offers
// (moviepatch.WriteMapList); a check counts such a list as current, the setup writes the copy's list again.
func (s *Setup) stepMatchOptions(step, copy string) bool {
	return s.stepMovie(step, copy, moviepatch.MatchOptions, "the create dialog's bot check box",
		"an older check box or map list")
}

// stepChatRooms installs the class that opens the chat rooms the server marks as open by default,
// game\Data\zz_Chat.snap: the copy's own chat movie (from Global.snap, which must be the build's) with the class added
// (moviepatch.ChatRooms). Every player copy gets it: once per login it joins each room the server puts in a marked
// category (blaze.AutoOpenCategoryMin) and opens its window minimised, without the focus; a server that marks no
// room changes nothing.
func (s *Setup) stepChatRooms(step, copy string) bool {
	return s.stepMovie(step, copy, moviepatch.ChatRooms, "the chat rooms opened by default", "an older chat rooms class")
}

// stepMovie writes spec's added archive into the copy when it isn't byte for byte what moviepatch makes now.
func (s *Setup) stepMovie(step, copy string, spec moviepatch.Spec, what, older string) bool {
	src := filepath.Join(copy, filepath.FromSlash(spec.Source))
	added := filepath.Join(copy, filepath.FromSlash(spec.Rel))
	source, err := os.ReadFile(src)
	if err != nil {
		if s.Check {
			return s.report(Missing, step, fmt.Sprintf("%s (the copy is incomplete)", s.show(src)))
		}
		return s.report(Failed, step, err.Error())
	}
	if h := sha256.Sum256(source); hex.EncodeToString(h[:]) != s.untouched(spec.Source) {
		return s.report(Failed, step, fmt.Sprintf("%s is not the untouched build-854 file (docs/file_manifest.csv); "+
			"delete %s and start again (it is extracted anew)", s.show(src), copy))
	}
	want, err := spec.BuildMaps(source, moviepatch.CopyMaps(filepath.Dir(src)))
	if err != nil {
		return s.report(Failed, step, err.Error())
	}
	h := sha256.Sum256(want)
	line := fmt.Sprintf("%s: %s (%s)", s.show(added), what, hex.EncodeToString(h[:])[:16])
	have, err := os.ReadFile(added)
	if err == nil && bytes.Equal(have, want) {
		return s.report(OK, step, line)
	}
	why := older
	if err != nil {
		why = "not there"
	} else if spec.MapList && moviepatch.MapListOnly(have, want) {
		// Start-Client's front wrote the maps of the server a login went to (moviepatch.WriteMapList): current for a
		// check; the setup puts the copy's own list back (the front writes the server's again at the next login)
		if s.Check {
			return s.report(OK, step, line+" (with the map list of the server logged in to last)")
		}
		why = "the map list of the server logged in to last"
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
