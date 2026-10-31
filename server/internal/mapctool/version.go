package mapctool

// `mapc version` (MAPED-REL1): which release, map format and command-line interface this mapc is, so a program
// that runs it can check that it speaks the interface it was written for before it parses any other output.

import (
	"fmt"
	"io"

	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/version"
)

// Version is `mapc version --json`'s object.
type Version struct {
	Release string `json:"release"` // the release, as Start-Client.exe --version prints it ("dev" in a checkout)
	Format  int    `json:"format"`  // the map package format this compiler reads (map.json's "format")
	API     int    `json:"api"`     // the command line's version (API)
}

// CurrentVersion is this build's Version.
func CurrentVersion() Version {
	return Version{Release: version.String(), Format: mapc.Format, API: API}
}

func runVersion(argv []string, stdout, stderr io.Writer) int {
	o, err := parse(argv)
	if err == nil && (len(o.args) != 0 || o.noMesh || o.full || o.copyDir != "" || o.mapsDir != "") {
		err = fmt.Errorf("version takes only --json")
	}
	if err != nil {
		fmt.Fprintf(stderr, "mapc: %v\n%s\n", err, usage)
		return 2
	}
	v := CurrentVersion()
	if o.json {
		(&tool{o: o, out: stdout, errw: stderr}).emit(v)
	} else {
		fmt.Fprintf(stdout, "mapc %s: map format %d, interface %d\n", v.Release, v.Format, v.API)
	}
	return 0
}
