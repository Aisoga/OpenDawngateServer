// Package update is Start-Client's update to the newest numbered release: the order of release names
// (v<major>.<minor>[.<patch>]), the latest release from the GitHub API, the download of its <tag>.zip, the check of
// every file against the release's BUILD.txt, and the replace of the release's files in the player's folder (the
// running Start-Client.exe renamed to Start-Client.old.exe, which the next start deletes).
//
// Only a numbered release updates itself: a developer build (dev-...) or any other name never asks. The settings a
// player made (Start-*.ini), Dawngate.zip and the setup folder Dawngate-Data are never touched.
//
// A signature check of the download (signed releases, later) slots in as Updater.Verify: it runs after the BUILD.txt
// check and before anything in the player's folder is replaced.
package update

import (
	"fmt"
	"regexp"
	"strconv"
)

// Version is a release name's number: v<Major>.<Minor>[.<Patch>] (a missing patch is 0).
type Version struct{ Major, Minor, Patch int }

var versionRE = regexp.MustCompile(`^v(\d{1,6})\.(\d{1,6})(?:\.(\d{1,6}))?$`)

// Parse reads a numbered release's name (v0.6, v1.2.3). Any other name (a developer build dev-..., the nightly, a
// dated name) is not one: ok is false.
func Parse(name string) (v Version, ok bool) {
	m := versionRE.FindStringSubmatch(name)
	if m == nil {
		return Version{}, false
	}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		v.Patch, _ = strconv.Atoi(m[3])
	}
	return v, true
}

// Compare is -1, 0 or 1 as v is older than, the same as or newer than w (by number: v0.10 is newer than v0.9).
func (v Version) Compare(w Version) int {
	for _, d := range [3]int{v.Major - w.Major, v.Minor - w.Minor, v.Patch - w.Patch} {
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
	}
	return 0
}

func (v Version) String() string {
	if v.Patch != 0 {
		return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	}
	return fmt.Sprintf("v%d.%d", v.Major, v.Minor)
}

// IsRelease reports whether name is a numbered release's (only those update themselves).
func IsRelease(name string) bool {
	_, ok := Parse(name)
	return ok
}

// Newer reports whether latest is a numbered release newer than own, itself a numbered release. A developer build or
// any other name on either side is never older or newer.
func Newer(own, latest string) bool {
	o, ok1 := Parse(own)
	l, ok2 := Parse(latest)
	return ok1 && ok2 && l.Compare(o) > 0
}
