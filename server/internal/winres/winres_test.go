package winres

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// a made-up icon file: a 16x16 BMP image and a 256x256 PNG image (only their headers matter here)
func testICO() []byte {
	le := binary.LittleEndian
	bmp := make([]byte, 40+16*16*4+16*4)
	le.PutUint32(bmp, 40)
	le.PutUint32(bmp[4:], 16)
	le.PutUint32(bmp[8:], 32)
	bmp[100] = 0xAB
	png := append(append([]byte{}, pngMagic...), []byte("rest of a png, odd length")...)
	b := make([]byte, 6+2*16)
	le.PutUint16(b[2:], 1)
	le.PutUint16(b[4:], 2)
	e := b[6:]
	e[0], e[1] = 16, 16
	le.PutUint16(e[4:], 1)
	le.PutUint16(e[6:], 32)
	le.PutUint32(e[8:], uint32(len(bmp)))
	le.PutUint32(e[12:], uint32(len(b)))
	e = b[22:]
	le.PutUint16(e[4:], 1)
	le.PutUint16(e[6:], 32)
	le.PutUint32(e[8:], uint32(len(png)))
	le.PutUint32(e[12:], uint32(len(b)+len(bmp)))
	return append(append(b, bmp...), png...)
}

func TestParseICO(t *testing.T) {
	icons, err := ParseICO(testICO())
	if err != nil {
		t.Fatal(err)
	}
	if len(icons) != 2 || icons[0].Width != 16 || icons[1].Width != 0 || icons[0].BitCount != 32 ||
		icons[0].Data[100] != 0xAB || !bytes.HasPrefix(icons[1].Data, pngMagic) {
		t.Fatalf("parsed %+v", icons)
	}
	for name, b := range map[string][]byte{
		"empty":     nil,
		"not icon":  []byte("MZ\x90\x00\x03\x00\x00\x00"),
		"no images": {0, 0, 1, 0, 0, 0},
		"truncated": testICO()[:30],
		"cut image": testICO()[:len(testICO())-1],
	} {
		if _, err := ParseICO(b); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	bad := testICO()
	binary.LittleEndian.PutUint32(bad[38:], 12) // the BMP's header size
	if _, err := ParseICO(bad); err == nil {
		t.Error("a BMP with a 12-byte header: no error")
	}
}

// checkSyso checks a .syso as a COFF object (debug/pe reads it) and reads its resources back.
func checkSyso(t *testing.T, syso []byte, goarch string, icons []Icon) {
	t.Helper()
	f, err := pe.NewFile(bytes.NewReader(syso))
	if err != nil {
		t.Fatal(err)
	}
	if f.Machine != machines[goarch].machine || len(f.Sections) != 1 || f.TimeDateStamp != 0 {
		t.Fatalf("%s: machine %#x, %d sections, time stamp %d", goarch, f.Machine, len(f.Sections), f.TimeDateStamp)
	}
	s := f.Sections[0]
	if s.Name != ".rsrc" || s.Characteristics != 0x40000040 || len(s.Relocs) != len(icons)+1 {
		t.Fatalf("%s: section %q %#x with %d relocations", goarch, s.Name, s.Characteristics, len(s.Relocs))
	}
	if len(f.COFFSymbols) != 1 || f.COFFSymbols[0].SectionNumber != 1 || f.COFFSymbols[0].StorageClass != 3 {
		t.Fatalf("%s: symbols %+v", goarch, f.COFFSymbols)
	}
	if n, _ := f.COFFSymbols[0].FullName(f.StringTable); n != ".rsrc" {
		t.Fatalf("%s: symbol %q", goarch, n)
	}
	data, err := s.Data()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range s.Relocs {
		if r.SymbolTableIndex != 0 || r.Type != machines[goarch].reloc || r.VirtualAddress%4 != 0 {
			t.Fatalf("%s: relocation %+v", goarch, r)
		}
	}
	checkResources(t, goarch, data, 0, icons)
}

// checkResources compares a resource section with the icon's resources: RT_ICON 1..n, RT_GROUP_ICON GroupID.
func checkResources(t *testing.T, what string, sec []byte, base uint32, icons []Icon) {
	t.Helper()
	res, err := ReadResources(sec, base)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if len(res) != len(icons)+1 {
		t.Fatalf("%s: %d resources, want %d", what, len(res), len(icons)+1)
	}
	for i, ic := range icons {
		r := res[i]
		if r.Type != RTIcon || r.ID != uint32(i+1) || r.Lang != Lang || !bytes.Equal(r.Data, ic.Data) {
			t.Errorf("%s: resource %d is %d/%d/%#x (%d bytes)", what, i, r.Type, r.ID, r.Lang, len(r.Data))
		}
	}
	g := res[len(icons)]
	if g.Type != RTGroupIcon || g.ID != GroupID || g.Lang != Lang || !bytes.Equal(g.Data, GroupIcon(icons)) {
		t.Errorf("%s: group icon %d/%d/%#x % x", what, g.Type, g.ID, g.Lang, g.Data)
	}
}

func TestIconSyso(t *testing.T) {
	icons, err := ParseICO(testICO())
	if err != nil {
		t.Fatal(err)
	}
	g := GroupIcon(icons)
	want := []byte{0, 0, 1, 0, 2, 0,
		16, 16, 0, 0, 1, 0, 32, 0, byte(len(icons[0].Data)), byte(len(icons[0].Data) >> 8), 0, 0, 1, 0,
		0, 0, 0, 0, 1, 0, 32, 0, byte(len(icons[1].Data)), 0, 0, 0, 2, 0}
	if !bytes.Equal(g, want) {
		t.Fatalf("group icon\n% x\nwant\n% x", g, want)
	}
	for _, a := range Archs {
		syso, err := IconSyso(icons, a)
		if err != nil {
			t.Fatal(err)
		}
		checkSyso(t, syso, a, icons)
		again, _ := IconSyso(icons, a)
		if !bytes.Equal(syso, again) {
			t.Errorf("%s: two runs differ", a)
		}
	}
	if _, err := IconSyso(icons, "arm"); err == nil {
		t.Error("arm: no error")
	}
}

// The programs' committed .syso files are what icosyso makes of the committed icon (go generate ./cmd/... in
// server/ after a change of the icon).
func TestCommittedSyso(t *testing.T) {
	ico, err := os.ReadFile(filepath.Join("..", "..", "cmd", "assets", "dawngate.ico"))
	if err != nil {
		t.Fatal(err)
	}
	icons, err := ParseICO(ico)
	if err != nil {
		t.Fatal(err)
	}
	want, err := IconSyso(icons, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"start-client", "start-server"} {
		got, err := os.ReadFile(filepath.Join("..", "..", "cmd", cmd, "rsrc_windows_amd64.syso"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("cmd/%s/rsrc_windows_amd64.syso doesn't match cmd/assets/dawngate.ico: run go generate ./cmd/... in server/", cmd)
		}
	}
}

// The linker takes the .syso: a small program built for Windows with it carries the icon in its resources.
func TestLinkedIcon(t *testing.T) {
	if testing.Short() {
		t.Skip("builds programs")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		gobin = filepath.Join(runtime.GOROOT(), "bin", "go")
		if _, err := os.Stat(gobin); err != nil {
			t.Skip("no go command")
		}
	}
	icons, err := ParseICO(testICO())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range Archs {
		dir := t.TempDir()
		syso, _ := IconSyso(icons, a)
		files := map[string]string{
			"go.mod":                      "module icontest\n\ngo 1.24\n",
			"main.go":                     "package main\n\nfunc main() {}\n",
			"rsrc_windows_" + a + ".syso": string(syso),
		}
		for n, c := range files {
			if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		exe := filepath.Join(dir, "icontest.exe")
		cmd := exec.Command(gobin, "build", "-o", exe, ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+a, "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOWORK=off",
			"GOFLAGS=")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: go build: %v\n%s", a, err, strings.TrimSpace(string(out)))
		}
		f, err := pe.Open(exe)
		if err != nil {
			t.Fatal(err)
		}
		s := f.Section(".rsrc")
		if s == nil {
			f.Close()
			t.Fatalf("%s: no .rsrc section", a)
		}
		data, err := s.Data()
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		checkResources(t, a, data[:min(int(s.VirtualSize), len(data))], s.VirtualAddress, icons)
	}
}
