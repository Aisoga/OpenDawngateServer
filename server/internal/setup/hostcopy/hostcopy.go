// Package hostcopy installs the match host into a patched copy of the game: our
// dgsim.dll as game\PhysXLoader.dll (the game's loader kept as PhysXLoader_orig.dll, to which dgsim.dll forwards
// PhysX's exports), game\dgsim.ini and the Lua probes (gms/engine/lua/*.lua) in game\. It is the Go port of
// re/scripts/setup_server_copy.py's install (and of setup_all.py's "host DLL" check), which stays the reference
// (ADR-3); the resulting files are byte for byte the script's.
//
// Left out on purpose: making the copy itself (setup_server_copy.py copies a client copy when the destination is
// missing; the Go setup extracts or copies the host copy and patches it first, then calls Install) and --laa (since
// Q3b the exe patch sets LARGE_ADDRESS_AWARE in every copy).
package hostcopy

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"dawngate/server/internal/setup/archive"
)

// File names in the copy's game folder.
const (
	Loader     = "PhysXLoader.dll"
	LoaderOrig = "PhysXLoader_orig.dll"
	Ini        = "dgsim.ini"
)

// defaultIni is setup_server_copy.py's DEFAULT_INI (TestDefaultIniIsTheScripts keeps them equal).
//
//go:embed default_dgsim.ini
var defaultIni string

// DefaultIni is the dgsim.ini Install writes when the copy has none, with Windows line ends (setup_server_copy.py
// writes it in text mode on Windows).
func DefaultIni() []byte {
	s := strings.ReplaceAll(defaultIni, "\r\n", "\n") // whatever the checkout did to the embedded file's line ends
	return []byte(strings.ReplaceAll(s, "\n", "\r\n"))
}

// Files says where Install takes dgsim.dll and the Lua probes from: e.g. os.DirFS(<repository>) with
// "gms/build/Release/dgsim.dll" and "gms/engine/lua", or an embedded file system of a release.
type Files struct {
	FS  fs.FS
	DLL string // dgsim.dll's path in FS
	Lua string // the Lua probes' folder in FS (every *.lua in it, not below)
	// LoaderSHA256 is the SHA-256 Check wants of the kept game loader (PhysXLoader_orig.dll); empty means build 854's.
	// Tests of the setup with a synthetic build set it.
	LoaderSHA256 string
}

// Options are setup_server_copy.py's install options.
type Options struct {
	Ini []byte // replaces game\dgsim.ini (--ini); nil: DefaultIni, written only when the copy has no dgsim.ini
}

// Result says what Install did.
type Result struct {
	RenamedOriginal bool     // the game's PhysXLoader.dll became PhysXLoader_orig.dll now
	DLLSHA256       string   // the installed PhysXLoader.dll
	Lua             []string // the Lua probes copied, by name
	WroteIni        bool     // dgsim.ini written (the default for a copy without one, or Options.Ini)
}

// PatchedFiles must differ from their .orig in a host copy (setup_server_copy.py's check_patched): the patchers ran.
var PatchedFiles = []string{"game/Dawngate.exe", "game/Data/Config.package", "game/Data/Config_Ship.package"}

// refusePristine is setup_server_copy.py's refuse_pristine (a path in client\ia_*).
func refusePristine(p string) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToLower(filepath.ToSlash(abs)), "/client/ia_") {
		return fmt.Errorf("refusing to touch a pristine extracted build: %s", p)
	}
	return nil
}

// CheckPatched refuses a copy whose exe or config packages aren't patched (each must exist, with a .orig, and
// differ from it).
func CheckPatched(copyDir string) error {
	for _, rel := range PatchedFiles {
		live := filepath.Join(copyDir, filepath.FromSlash(rel))
		a, errA := hashFile(live)
		b, errB := hashFile(live + ".orig")
		if errors.Is(errA, os.ErrNotExist) || errors.Is(errB, os.ErrNotExist) {
			return fmt.Errorf("%s or its .orig is missing; patch the copy first (exe and config packages)", live)
		}
		if errA != nil {
			return errA
		}
		if errB != nil {
			return errB
		}
		if a == b {
			return fmt.Errorf("%s is unpatched; patch the copy first (exe and config packages)", live)
		}
	}
	return nil
}

// Install puts the match host into the patched copy copyDir (its Dawngate folder) as setup_server_copy.py <source>
// <copyDir> <dll> does for an existing destination: the patch check; the game's loader renamed to
// PhysXLoader_orig.dll once (after checking it is PhysX's loader and not dgsim.dll); dgsim.dll copied in as
// PhysXLoader.dll (with its file time); every *.lua of f.Lua copied into game\ (always, so they match); dgsim.ini
// from o.Ini, else the default when there is none (an existing one is kept: the operator's changes stay).
func Install(copyDir string, f Files, o Options) (Result, error) {
	var res Result
	if err := refusePristine(copyDir); err != nil {
		return res, err
	}
	dll, dllTime, err := readFS(f.FS, f.DLL)
	if err != nil {
		return res, fmt.Errorf("dgsim.dll: %w", err)
	}
	if !bytes.Contains(dll, dgsimMarker) {
		return res, fmt.Errorf("%s is not dgsim.dll (no %q in it)", f.DLL, dgsimMarker)
	}
	if err := CheckPatched(copyDir); err != nil {
		return res, err
	}
	game := filepath.Join(copyDir, "game")
	loader, orig, ini := filepath.Join(game, Loader), filepath.Join(game, LoaderOrig), filepath.Join(game, Ini)
	if !exists(orig) {
		cur, err := os.ReadFile(loader)
		if err != nil {
			return res, err
		}
		if bytes.Contains(cur, dgsimMarker) {
			return res, fmt.Errorf("%s is already dgsim but %s is missing; restore the original by hand", loader, orig)
		}
		if !bytes.Contains(cur, []byte("NxCreatePhysicsSDK")) {
			return res, fmt.Errorf("%s doesn't look like PhysX's loader", loader)
		}
		if err := os.Rename(loader, orig); err != nil {
			return res, err
		}
		res.RenamedOriginal = true
	}
	if err := writeFile(loader, dll, dllTime); err != nil {
		return res, err
	}
	res.DLLSHA256 = sum(dll)
	luaFiles, err := luaNames(f)
	if err != nil {
		return res, err
	}
	for _, name := range luaFiles {
		b, _, err := readFS(f.FS, path.Join(f.Lua, name))
		if err != nil {
			return res, err
		}
		if err := os.WriteFile(filepath.Join(game, name), b, 0o644); err != nil { // shutil.copyfile: bytes only
			return res, err
		}
		res.Lua = append(res.Lua, name)
	}
	switch {
	case o.Ini != nil:
		if err := os.WriteFile(ini, o.Ini, 0o644); err != nil {
			return res, err
		}
		res.WroteIni = true
	case !exists(ini):
		if err := os.WriteFile(ini, DefaultIni(), 0o644); err != nil {
			return res, err
		}
		res.WroteIni = true
	}
	return res, nil
}

// dgsimMarker is the string setup_server_copy.py's is_dgsim looks for.
var dgsimMarker = []byte("dgsim.dll loaded")

// ErrNotTheGamesLoader: PhysXLoader_orig.dll isn't the game's PhysXLoader.dll (setup_all.py: run --remove, then again).
var ErrNotTheGamesLoader = errors.New("PhysXLoader_orig.dll is not the game's PhysXLoader.dll")

// gameLoaderSHA256 is build 854's PhysXLoader.dll (the manifest; tests use their own).
var gameLoaderSHA256 = func() string {
	f, _ := archive.Build854().Lookup("game/" + Loader)
	return f.SHA256
}()

// Check lists what keeps the copy from running the given dgsim.dll and Lua probes, as setup_all.py's host DLL step
// words it: no dgsim.dll installed, PhysXLoader.dll not this dgsim.dll, no dgsim.ini, Lua probes that differ (line
// ends aside). An empty list: current. ErrNotTheGamesLoader when the kept original isn't the build's loader.
func Check(copyDir string, f Files) ([]string, error) {
	game := filepath.Join(copyDir, "game")
	var out []string
	orig := filepath.Join(game, LoaderOrig)
	if !exists(orig) {
		out = append(out, "no dgsim.dll installed (no PhysXLoader_orig.dll)")
	} else {
		got, err := hashFile(orig)
		if err != nil {
			return nil, err
		}
		want := f.LoaderSHA256
		if want == "" {
			want = gameLoaderSHA256
		}
		if got != want {
			return nil, ErrNotTheGamesLoader
		}
	}
	dll, _, err := readFS(f.FS, f.DLL)
	switch {
	case err != nil:
		out = append(out, "no dgsim.dll to install ("+f.DLL+")")
	default:
		cur, err := hashFile(filepath.Join(game, Loader))
		if err != nil || cur != sum(dll) {
			out = append(out, "PhysXLoader.dll is not the current dgsim.dll")
		}
	}
	if !exists(filepath.Join(game, Ini)) {
		out = append(out, "no dgsim.ini")
	}
	names, err := luaNames(f)
	if err != nil {
		return nil, err
	}
	var diff []string
	lf := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
	for _, name := range names {
		want, _, err := readFS(f.FS, path.Join(f.Lua, name))
		if err != nil {
			return nil, err
		}
		got, err := os.ReadFile(filepath.Join(game, name))
		if err != nil || !bytes.Equal(lf(got), lf(want)) {
			diff = append(diff, name)
		}
	}
	if len(diff) > 0 {
		out = append(out, fmt.Sprintf("%d Lua probe(s) differ from gms/engine/lua (%s...)", len(diff), diff[0]))
	}
	return out, nil
}

// Remove undoes Install (setup_server_copy.py --remove): the original loader back as PhysXLoader.dll, dgsim.ini and
// the dgsim_*.lua probes deleted; the copy stays.
func Remove(copyDir string) error {
	if err := refusePristine(copyDir); err != nil {
		return err
	}
	game := filepath.Join(copyDir, "game")
	if exists(filepath.Join(game, LoaderOrig)) {
		if err := os.Rename(filepath.Join(game, LoaderOrig), filepath.Join(game, Loader)); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(game)
	if err != nil {
		return err
	}
	for _, e := range entries {
		n := e.Name()
		if n == Ini || strings.HasPrefix(n, "dgsim_") && strings.HasSuffix(n, ".lua") {
			if err := os.Remove(filepath.Join(game, n)); err != nil {
				return err
			}
		}
	}
	return nil
}

// luaNames lists the *.lua files in f.Lua, sorted (setup_server_copy.py: sorted(os.listdir), names ending in .lua).
func luaNames(f Files) ([]string, error) {
	entries, err := fs.ReadDir(f.FS, f.Lua)
	if err != nil {
		return nil, fmt.Errorf("the Lua probes: %w", err)
	}
	var out []string
	for _, e := range entries { // fs.ReadDir sorts by name
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".lua") {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func readFS(fsys fs.FS, name string) ([]byte, fs.FileInfo, error) {
	if fsys == nil {
		return nil, nil, errors.New("no file system given")
	}
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, nil, err
	}
	st, err := fs.Stat(fsys, name)
	if err != nil {
		return nil, nil, err
	}
	return b, st, nil
}

// writeFile writes b and gives the file the source's modification time when it has one (shutil.copy2).
func writeFile(p string, b []byte, src fs.FileInfo) error {
	if err := os.WriteFile(p, b, 0o644); err != nil {
		return err
	}
	if src != nil && !src.ModTime().IsZero() {
		return os.Chtimes(p, src.ModTime(), src.ModTime())
	}
	return nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
