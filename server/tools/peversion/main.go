// Command peversion writes and checks the version resource of the release's Windows programs and DLL
// (Start-Server.exe, Start-Client.exe, host\dgsim.dll; package winres). Code signing for open-source projects
// (SignPath) requires every signed file to carry the project's name as its product name and one product version for
// the whole build, so make_release.ps1 sets both after building and checks them:
//
//	peversion set   -version <release> -description <text> <file>   (Windows only: UpdateResourceW)
//	peversion check -version <release> <file>...                     (exit 1 when a file's version differs)
//	peversion signed -version <release> <built file> <signed file>   (the signed one is the built one plus a signature)
//
// set writes ProductName (Product), ProductVersion and FileVersion (the release's name), FileDescription,
// InternalName and OriginalFilename (the file's name), LegalCopyright (Copyright) and the fixed part's numbers
// (winres.NumbersOf); it replaces a version resource the file already has and keeps every other resource.
//
// signed is make_release.ps1 -SignedFiles' check of a file that came back from the code signing service: its version
// is right and it is the file this build made, byte for byte, apart from what an Authenticode signature adds (the
// optional header's CheckSum, its certificate table entry, the zero padding to 8 bytes and the certificate table at
// the end). Whether the signature itself is valid is Windows' check (Get-AuthenticodeSignature in the script).
package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dawngate/server/internal/winres"
)

// Product is the project's name: every signed file's product name.
const Product = "OpenDawngateServer"

// Copyright is the files' copyright line (the README's).
const Copyright = "Copyright (C) 2026 the OpenDawngateServer contributors. AGPL-3.0-only."

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	version := fs.String("version", "", "the release's name (its version)")
	desc := fs.String("description", "", "set: the file's description")
	_ = fs.Parse(os.Args[2:])
	if *version == "" || fs.NArg() == 0 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "set":
		if *desc == "" || fs.NArg() != 1 {
			usage()
		}
		err = set(fs.Arg(0), *version, *desc)
	case "check":
		err = check(fs.Args(), *version)
	case "signed":
		if fs.NArg() != 2 {
			usage()
		}
		err = signed(fs.Arg(0), fs.Arg(1), *version)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "peversion:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: peversion set -version <release> -description <text> <file>\n"+
		"       peversion check -version <release> <file>...\n"+
		"       peversion signed -version <release> <built file> <signed file>")
	os.Exit(2)
}

// Info is the version resource set writes into the file at path.
func Info(path, version, desc string) (winres.Version, error) {
	f, err := pe.Open(path)
	if err != nil {
		return winres.Version{}, err
	}
	dll := f.Characteristics&pe.IMAGE_FILE_DLL != 0
	f.Close()
	name := filepath.Base(path)
	return winres.Version{
		ProductName: Product, ProductVersion: version, FileVersion: version, FileDescription: desc,
		InternalName: strings.TrimSuffix(name, filepath.Ext(name)), OriginalFilename: name,
		LegalCopyright: Copyright, Numbers: winres.NumbersOf(version), DLL: dll,
	}, nil
}

func set(path, version, desc string) error {
	v, err := Info(path, version, desc)
	if err != nil {
		return err
	}
	if err := updateVersion(path, v.Resource()); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return check([]string{path}, version)
}

// Check gives the problems of one file's version resource: the product name, both versions and the numbers.
func Check(path, version string) []string {
	strs, nums, err := winres.ProgramVersion(path)
	if err != nil {
		return []string{err.Error()}
	}
	var bad []string
	for _, kv := range [][2]string{{"ProductName", Product}, {"ProductVersion", version}, {"FileVersion", version},
		{"OriginalFilename", filepath.Base(path)}} {
		if strs[kv[0]] != kv[1] {
			bad = append(bad, fmt.Sprintf("%s is %q, not %q", kv[0], strs[kv[0]], kv[1]))
		}
	}
	if strs["FileDescription"] == "" {
		bad = append(bad, "no FileDescription")
	}
	if nums != winres.NumbersOf(version) {
		bad = append(bad, fmt.Sprintf("the numbers are %v, not %v", nums, winres.NumbersOf(version)))
	}
	return bad
}

func check(paths []string, version string) error {
	failed := false
	for _, p := range paths {
		if bad := Check(p, version); len(bad) > 0 {
			failed = true
			fmt.Printf("%s: %s\n", p, strings.Join(bad, "; "))
			continue
		}
		fmt.Printf("%s: %s %s\n", p, Product, version)
	}
	if failed {
		return errors.New("a file's version resource is wrong")
	}
	return nil
}

func signed(builtPath, signedPath, version string) error {
	built, err := os.ReadFile(builtPath)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(signedPath)
	if err != nil {
		return err
	}
	if err := SameButSignature(built, sig); err != nil {
		return fmt.Errorf("%s against %s: %w", signedPath, builtPath, err)
	}
	return check([]string{signedPath}, version)
}

// The file offsets of the optional header's CheckSum and of its certificate table entry (data directory 4).
func signatureFields(b []byte) (checksum, certEntry int, err error) {
	if len(b) < 0x40 || b[0] != 'M' || b[1] != 'Z' {
		return 0, 0, errors.New("not a PE file")
	}
	le := binary.LittleEndian
	peOff := int(le.Uint32(b[0x3C:]))
	opt := peOff + 4 + 20
	if peOff < 0 || opt+2 > len(b) || !bytes.Equal(b[peOff:peOff+4], []byte("PE\x00\x00")) {
		return 0, 0, errors.New("no PE header")
	}
	dirs := opt + 96 // PE32
	switch le.Uint16(b[opt:]) {
	case 0x10b:
	case 0x20b:
		dirs = opt + 112 // PE32+
	default:
		return 0, 0, errors.New("unknown optional header")
	}
	if int(le.Uint16(b[peOff+4+16:])) < dirs-opt+5*8 || dirs+5*8 > len(b) {
		return 0, 0, errors.New("no certificate table entry")
	}
	return opt + 64, dirs + 4*8, nil
}

// SameButSignature checks that signed is unsigned with an Authenticode signature added and nothing else changed.
func SameButSignature(unsigned, signed []byte) error {
	sum, entry, err := signatureFields(unsigned)
	if err != nil {
		return err
	}
	le := binary.LittleEndian
	if le.Uint64(unsigned[entry:]) != 0 {
		return errors.New("the built file has a signature already")
	}
	if len(signed) < len(unsigned) {
		return errors.New("the signed file is shorter")
	}
	off, size := int(le.Uint32(signed[entry:])), int(le.Uint32(signed[entry+4:]))
	if size == 0 {
		return errors.New("the signed file has no signature")
	}
	if off < len(unsigned) || off-len(unsigned) >= 8 || off%8 != 0 || off+size != len(signed) {
		return fmt.Errorf("the certificate table at %d (%d bytes) is not appended to the %d bytes built", off, size, len(unsigned))
	}
	if !bytes.Equal(signed[len(unsigned):off], make([]byte, off-len(unsigned))) {
		return errors.New("the padding before the certificate table isn't zero")
	}
	a, b := bytes.Clone(unsigned), bytes.Clone(signed[:len(unsigned)])
	for _, x := range [][]byte{a, b} {
		clear(x[sum : sum+4])
		clear(x[entry : entry+8])
	}
	if !bytes.Equal(a, b) {
		return errors.New("the signed file differs from the built one beyond its signature")
	}
	return nil
}
