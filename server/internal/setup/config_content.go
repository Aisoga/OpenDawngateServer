package setup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/configpatch"
	"dawngate/server/internal/setup/gamecontent"
)

// stepConfig points a copy's config packages at host (setup_all.py's step_config with patch_config.py --host): current
// means each package is byte for byte what configpatch makes from its untouched file (the .orig, or the package before
// its first patch, checked against the manifest).
func (s *Setup) stepConfig(step, copy, host string) bool {
	data := filepath.Join(copy, "game", "Data")
	o := configpatch.Defaults()
	o.Host = host
	var stale []string
	for _, name := range configpatch.Packages {
		rel := "game/Data/" + name
		base, err := s.base(copy, rel)
		if err != nil {
			if s.Check {
				return s.report(Stale, step, err.Error())
			}
			return s.report(Failed, step, err.Error())
		}
		orig, err := os.ReadFile(base)
		if err != nil {
			return s.report(Failed, step, err.Error())
		}
		r, err := configpatch.Package(orig, name, o)
		if err != nil {
			return s.report(Failed, step, fmt.Sprintf("%s: %v", name, err))
		}
		want := r.Data
		if want == nil {
			want = orig
		}
		cur, err := os.ReadFile(filepath.Join(data, name))
		if err != nil || !bytes.Equal(cur, want) {
			stale = append(stale, name)
		}
	}
	if len(stale) == 0 {
		return s.report(OK, step, fmt.Sprintf("%s: the config packages point at %s", s.show(data), host))
	}
	if s.Check {
		return s.report(Stale, step, fmt.Sprintf("%s: not patched for %s: %s", s.show(data), host, strings.Join(stale, ", ")))
	}
	if _, err := configpatch.Patch(data, o); err != nil {
		return s.report(Failed, step, fmt.Sprintf("%s: %v (is the game running from this copy?)", s.show(data), err))
	}
	return s.report(Done, step, fmt.Sprintf("%s: patched for %s (was: %s)", s.show(data), host, strings.Join(stale, ", ")))
}

// stepContent builds the content file from the game data of the copy at game (setup_all.py's step_content, which goes
// through snap.py, assetdata.py and dgserver --make-content); what names that copy in the report. Current means byte
// for byte what gamecontent makes from the game data now (0.15 s for build 854; setup_all.py only checks the format
// and the perk stats). Both copies hold the same untouched AssetData.snap and Locale.package (the patchers change only
// the exe and the config packages), so a server without a player copy builds the same file from its host copy.
func (s *Setup) stepContent(game, what string) bool {
	const step = "content"
	file := s.Content()
	if !isDir(game) {
		if s.Check {
			st := Missing
			if isFile(file) {
				st = Stale
			}
			return s.report(st, step, s.show(file)+": not checked without "+what+" (its game data)")
		}
		return s.needs(step, what+" (its game data)")
	}
	t0 := time.Now()
	c, err := gamecontent.Build(game)
	if err != nil {
		return s.report(Failed, step, err.Error())
	}
	want, err := c.Marshal()
	if err != nil {
		return s.report(Failed, step, err.Error())
	}
	cur, err := os.ReadFile(file)
	why := "missing"
	switch {
	case err == nil && bytes.Equal(cur, want):
		return s.report(OK, step, fmt.Sprintf("%s (%s, %d items, %d shapers)", s.show(file), c.Format, len(c.Items), len(c.Shapers)))
	case err == nil:
		why = "not what the game data gives now"
		if old, err := content.Load(file); err == nil && old.Format != content.Format {
			why = fmt.Sprintf("format %s, want %s", old.Format, content.Format)
		}
	case !errors.Is(err, os.ErrNotExist):
		why = "unreadable: " + err.Error()
	}
	if s.Check {
		st := Stale
		if why == "missing" {
			st = Missing
		}
		return s.report(st, step, fmt.Sprintf("%s: %s", s.show(file), why))
	}
	if err := c.Write(file); err != nil {
		return s.report(Failed, step, err.Error())
	}
	return s.report(Done, step, fmt.Sprintf("%s built from the game data (%s, %d items, %d shapers; %.1f s; was: %s)",
		s.show(file), c.Format, len(c.Items), len(c.Shapers), time.Since(t0).Seconds(), why))
}
