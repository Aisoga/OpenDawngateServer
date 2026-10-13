package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"dawngate/server/internal/setup/localepatch"
)

// stepLocale installs the client copy's added locale package game\Data\Locale\en-us\AA_Server.package (
// setup_all.py's step_locale with re/scripts/patch_locale.py): the login screen's texts and its links at the server's
// pages for host (or WebDomain), made from the copy's own Locale.package, which must be the build's. Current means
// byte for byte what localepatch makes now. Never a host copy: the server simulator shows no text.
func (s *Setup) stepLocale(step, copy, host string) bool {
	src := filepath.Join(copy, filepath.FromSlash(localepatch.Source))
	added := filepath.Join(copy, filepath.FromSlash(localepatch.Rel))
	locale, err := os.ReadFile(src)
	if err != nil {
		if s.Check {
			return s.report(Missing, step, fmt.Sprintf("%s (the copy is incomplete)", s.show(src)))
		}
		return s.report(Failed, step, err.Error())
	}
	if h := sha256.Sum256(locale); hex.EncodeToString(h[:]) != s.untouched(localepatch.Source) {
		return s.report(Failed, step, fmt.Sprintf("%s is not the untouched build-854 file (docs/file_manifest.csv); "+
			"delete %s and start again (it is extracted anew)", s.show(src), copy))
	}
	o := localepatch.Options{Host: host, WebDomain: s.WebDomain}
	want, err := localepatch.Build(locale, o)
	if err != nil {
		return s.report(Failed, step, err.Error())
	}
	base, _ := o.Base()
	h := sha256.Sum256(want)
	line := fmt.Sprintf("%s: login texts, links at %s (%s)", s.show(added), base, hex.EncodeToString(h[:])[:16])
	have, err := os.ReadFile(added)
	if err == nil && bytes.Equal(have, want) {
		return s.report(OK, step, line)
	}
	why := "another server or an older text"
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
