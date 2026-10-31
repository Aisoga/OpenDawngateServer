// Command mapc checks, compiles and previews a map package (a folder with map.json, format 1): the developers' way
// to run the map compiler's command line from a checkout. The code and the full usage are in internal/mapctool; the
// release runs the same code as Start-Client.exe --mapc <args>, with the same output.
//
// Usage (from server\):
//
//	go run ./tools/mapc version [--json]
//	go run ./tools/mapc check   [--json] [--copy <copy>] [--maps <dir>] <package>
//	go run ./tools/mapc build   [--json] [--no-mesh] [--maps <dir>] <package> <copy> <out>
//	go run ./tools/mapc preview [--json] [--copy <copy>] [--maps <dir>] <package> <dir>
//	go run ./tools/mapc bake | model | ground | scene | props | nouns | icon | textures | texture | import ...
package main

import (
	"os"

	"dawngate/server/internal/mapctool"
)

func main() {
	os.Exit(mapctool.Run(os.Args[1:], os.Stdout, os.Stderr))
}
