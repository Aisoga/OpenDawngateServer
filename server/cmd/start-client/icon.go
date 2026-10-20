package main

// The program's icon: rsrc_windows_amd64.syso holds ../assets/dawngate.ico as Windows resources, and the linker
// links it into every windows/amd64 build. After a change of the icon, `go generate ./cmd/...` in server/ rewrites it.

//go:generate go run ../../tools/icosyso -ico ../assets/dawngate.ico -o rsrc_windows
