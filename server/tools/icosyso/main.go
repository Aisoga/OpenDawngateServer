// Command icosyso writes a program's icon as a .syso file that the Go linker links into its Windows build: the
// RT_ICON and RT_GROUP_ICON resources of an .ico file in a COFF object (package winres). The commands that carry
// the icon run it through go generate, so after a change of the .ico, `go generate ./cmd/...` in server/ rewrites
// their .syso files:
//
//	go run ./tools/icosyso -ico <icon.ico> -o <dir>/rsrc_windows [-arch amd64,386]
//
// It writes <-o>_<arch>.syso for each architecture (default amd64, the release's).
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"dawngate/server/internal/winres"
)

func main() {
	ico := flag.String("ico", "", "the icon file (.ico)")
	out := flag.String("o", "rsrc_windows", "the output path without _<arch>.syso")
	archs := flag.String("arch", "amd64", "the Go architectures, comma-separated ("+strings.Join(winres.Archs, ", ")+")")
	flag.Parse()
	if *ico == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*ico, *out, strings.Split(*archs, ",")); err != nil {
		fmt.Fprintln(os.Stderr, "icosyso:", err)
		os.Exit(1)
	}
}

func run(icoPath, out string, archs []string) error {
	b, err := os.ReadFile(icoPath)
	if err != nil {
		return err
	}
	icons, err := winres.ParseICO(b)
	if err != nil {
		return err
	}
	for _, a := range archs {
		syso, err := winres.IconSyso(icons, strings.TrimSpace(a))
		if err != nil {
			return err
		}
		if err := os.WriteFile(out+"_"+strings.TrimSpace(a)+".syso", syso, 0o644); err != nil {
			return err
		}
	}
	return nil
}
