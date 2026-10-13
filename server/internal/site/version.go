package site

import (
	"runtime/debug"
	"strings"
)

// Version is the running build's version for the status page: the git commit Go stamped into the binary (go build in
// a checkout records vcs.revision, vcs.time and vcs.modified), as "<12 hex digits> (<UTC date>)", with "+changes"
// when the checkout had uncommitted changes; "development build" when there is none (go run, tests).
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "development build"
	}
	var rev, when string
	modified := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			when = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if rev == "" {
		return "development build"
	}
	v := rev[:min(12, len(rev))]
	if modified {
		v += "+changes"
	}
	if d, _, ok := strings.Cut(when, "T"); ok {
		v += " (" + d + ")"
	}
	return v
}
