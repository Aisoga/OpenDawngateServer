// Package version names the build: one string that Start-Server.exe, Start-Client.exe and dgserver.exe of one build
// share. A player's Start-Client and the server it logs in to must have the same one: the tunnel's gateway answers it
// (tunnel.ProbeInfo), the login screen's server list shows it, and Start-Client refuses a login to a server of
// another version or of none it can tell.
//
// A release build sets it with the linker (re/scripts/make_release.ps1):
//
//	go build -ldflags "-X dawngate/server/internal/version.release=<release name>"
//
// Any other build is a developer build, named after the commit Go stamped into it (go build in a git checkout records
// vcs.revision and vcs.modified): dev-<10 hex digits>, with +dirty when the checkout had uncommitted changes, so
// builds of one commit match each other. Without that stamp (go run, go test, -buildvcs=false) it is "dev".
package version

import (
	"runtime/debug"
	"strings"
	"sync"
)

// release is the release's name, set by the linker (-X); empty in a developer build.
var release string

// MaxLen bounds a version string (the gateway's answer, the list's field).
const MaxLen = 64

var (
	once   sync.Once
	cached string
)

// String is this build's version.
func String() string {
	once.Do(func() { cached = compute(release, readBuildInfo) })
	return cached
}

func readBuildInfo() (*debug.BuildInfo, bool) { return debug.ReadBuildInfo() }

// compute is String's rule: the release's name when set, else the developer build's name from the build info.
func compute(rel string, info func() (*debug.BuildInfo, bool)) string {
	if r := Clean(rel); r != "" {
		return r
	}
	bi, ok := info()
	if !ok || bi == nil {
		return "dev"
	}
	var rev string
	modified := false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if Clean(rev) == "" {
		return "dev"
	}
	v := "dev-" + rev[:min(10, len(rev))]
	if modified {
		v += "+dirty"
	}
	return v
}

// Clean keeps a version string safe for a log line and the list file: only letters, digits and . _ + - (anything
// else becomes -), at most MaxLen characters, no blanks around it.
func Clean(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > MaxLen {
		s = s[:MaxLen]
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '+', r == '-':
			return r
		}
		return '-'
	}, s)
}

// Same reports whether a server's version matches this build's: equal strings, and a server that named none (an
// older release, or no answer) never matches.
func Same(own, server string) bool { return server != "" && own == server }
