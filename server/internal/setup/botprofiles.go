package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"dawngate/server/internal/setup/botprofiles"
)

// stepBotProfiles installs a copy's added archive game\Data\zz_BotProfiles.snap (setup_all.py's step_bot_profiles with
// re/scripts/bot_profiles.py): the project's own bot profiles (a Warden with the Predator role), made from the copy's
// own AssetData.snap, which must be the build's. Current means byte for byte what botprofiles makes now, so a copy set
// up before a profile was added gets the new archive. Every client and match-host copy: the host gives its bots these
// profiles, and each client reads a bot's role from the profile its player update names.
func (s *Setup) stepBotProfiles(step, copy string) bool {
	src := filepath.Join(copy, filepath.FromSlash(botprofiles.Source))
	added := filepath.Join(copy, filepath.FromSlash(botprofiles.Rel))
	data, err := os.ReadFile(src)
	if err != nil {
		if s.Check {
			return s.report(Missing, step, fmt.Sprintf("%s (the copy is incomplete)", s.show(src)))
		}
		return s.report(Failed, step, err.Error())
	}
	if h := sha256.Sum256(data); hex.EncodeToString(h[:]) != s.untouched(botprofiles.Source) {
		return s.report(Failed, step, fmt.Sprintf("%s is not the untouched build-854 file (docs/file_manifest.csv); "+
			"delete %s and start again (it is extracted anew)", s.show(src), copy))
	}
	maps := s.mapCatalogs()
	want, err := botprofiles.Build(data, maps...)
	if err != nil {
		return s.report(Failed, step, err.Error())
	}
	h := sha256.Sum256(want)
	line := fmt.Sprintf("%s: %d bot profile(s) (%s)", s.show(added), len(botprofiles.Profiles), hex.EncodeToString(h[:])[:16])
	if len(maps) > 0 {
		line = fmt.Sprintf("%s: %d bot profile(s) and the catalog entries of %d map(s) (%s)", s.show(added),
			len(botprofiles.Profiles), len(maps), hex.EncodeToString(h[:])[:16])
	}
	have, err := os.ReadFile(added)
	if err == nil && bytes.Equal(have, want) {
		return s.report(OK, step, line)
	}
	why := "older profiles"
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
