package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dawngate/server/internal/winres"
)

// set on a small program built with the icon: the version resource is in it afterwards, the icon still is, the
// program still runs, and check refuses another version.
func TestSetAndCheck(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("set needs Windows")
	}
	if testing.Short() {
		t.Skip("builds a program")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	dir := t.TempDir()
	syso, err := os.ReadFile(filepath.Join("..", "..", "cmd", "start-client", "rsrc_windows_amd64.syso"))
	if err != nil {
		t.Fatal(err)
	}
	for n, c := range map[string]string{
		"go.mod":                  "module vertest\n\ngo 1.24\n",
		"main.go":                 "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Print(\"ran\") }\n",
		"rsrc_windows_amd64.syso": string(syso),
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(dir, "Start-Test.exe")
	cmd := exec.Command(gobin, "build", "-trimpath", "-ldflags", "-s -w", "-o", exe, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOWORK=off",
		"GOFLAGS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	if bad := Check(exe, "v1.2"); len(bad) != 1 || !strings.Contains(bad[0], "no version resource") {
		t.Fatalf("before set: %v", bad)
	}
	for _, v := range []string{"v1.2", "nightly-1"} { // twice: set replaces the first resource
		if err := set(exe, v, "OpenDawngateServer: a test"); err != nil {
			t.Fatal(err)
		}
		if bad := Check(exe, v); len(bad) != 0 {
			t.Fatalf("%s: %v", v, bad)
		}
	}
	if bad := Check(exe, "v1.2"); len(bad) == 0 {
		t.Error("check took another version")
	}
	res, err := winres.ProgramResources(exe)
	if err != nil {
		t.Fatal(err)
	}
	count := map[uint32]int{}
	for _, r := range res {
		count[r.Type]++
	}
	if count[winres.RTVersion] != 1 || count[winres.RTGroupIcon] != 1 || count[winres.RTIcon] == 0 {
		t.Errorf("resources by type: %v", count)
	}
	strs, _, _ := winres.ProgramVersion(exe)
	if strs["InternalName"] != "Start-Test" || strs["OriginalFilename"] != "Start-Test.exe" || strs["LegalCopyright"] != Copyright {
		t.Errorf("strings: %v", strs)
	}
	if out, err := exec.Command(exe).Output(); err != nil || string(out) != "ran" {
		t.Errorf("the program after set: %q, %v", out, err)
	}

	// signed: what a signing service returns (the CheckSum and the certificate table entry set, the table appended
	// after zero padding to 8) passes; any other change doesn't
	built, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum, entry, err := signatureFields(built)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(b []byte) []byte {
		s := append(bytes.Clone(b), make([]byte, (8-len(b)%8)%8)...)
		off := len(s)
		s = append(s, bytes.Repeat([]byte{0xC5}, 1000)...)
		binary.LittleEndian.PutUint32(s[sum:], 0x1234567)
		binary.LittleEndian.PutUint32(s[entry:], uint32(off))
		binary.LittleEndian.PutUint32(s[entry+4:], 1000)
		return s
	}
	good := sign(built)
	if err := SameButSignature(built, good); err != nil {
		t.Errorf("a signed copy: %v", err)
	}
	signedExe := filepath.Join(dir, "signed", "Start-Test.exe")
	os.MkdirAll(filepath.Dir(signedExe), 0o755)
	os.WriteFile(signedExe, good, 0o644)
	if err := signed(exe, signedExe, "nightly-1"); err != nil {
		t.Errorf("signed: %v", err)
	}
	if err := signed(exe, signedExe, "v1.2"); err == nil {
		t.Error("signed took another version")
	}
	changed := bytes.Clone(built)
	changed[0x1000] ^= 1
	short := bytes.Clone(good)
	binary.LittleEndian.PutUint32(short[entry+4:], 999)
	for name, s := range map[string][]byte{
		"unsigned":      built,
		"another build": sign(changed),
		"cut":           good[:len(good)-1],
		"wrong size":    short,
		"signed twice":  sign(good),
		"not a PE":      []byte("MZ but nothing else, and long enough to pass the first length check here"),
	} {
		if err := SameButSignature(built, s); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
