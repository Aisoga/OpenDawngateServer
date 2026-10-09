package hostcopy

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"dawngate/server/internal/setup/setuptest"
)

// The embedded default ini is setup_server_copy.py's DEFAULT_INI (copy it into default_dgsim.ini after
// the script's changes).
func TestDefaultIniIsTheScripts(t *testing.T) {
	repo := setuptest.Repo()
	if repo == "" {
		t.Skip("no checkout")
	}
	src, err := os.ReadFile(filepath.Join(repo, "re", "scripts", "setup_server_copy.py"))
	if err != nil {
		t.Skip(err)
	}
	s := strings.ReplaceAll(string(src), "\r\n", "\n")
	_, rest, ok := strings.Cut(s, `DEFAULT_INI = """`)
	body, _, ok2 := strings.Cut(rest, `"""`)
	if !ok || !ok2 {
		t.Fatal("no DEFAULT_INI in setup_server_copy.py")
	}
	body = strings.ReplaceAll(body, `\\`, `\`) // the only escape the Python string uses
	want := strings.ReplaceAll(body, "\n", "\r\n")
	if got := string(DefaultIni()); got != want {
		t.Errorf("default_dgsim.ini differs from setup_server_copy.py's DEFAULT_INI (%d vs %d bytes)", len(got), len(want))
	}
	if !strings.HasPrefix(string(DefaultIni()), "[dgsim]\r\n") || strings.Contains(strings.ReplaceAll(string(DefaultIni()), "\r\n", ""), "\n") {
		t.Error("line ends")
	}
}

var physx = []byte("MZ ... NxCreatePhysicsSDK ... the game's PhysX loader")

// fakeCopy makes a patched copy's few files that Install looks at.
func fakeCopy(t *testing.T, dir string) string {
	t.Helper()
	game := filepath.Join(dir, "Dawngate", "game")
	for rel, data := range map[string]string{
		"Dawngate.exe": "patched exe", "Dawngate.exe.orig": "exe",
		"Data/Config.package": "patched config", "Data/Config.package.orig": "config",
		"Data/Config_Ship.package": "patched ship", "Data/Config_Ship.package.orig": "ship",
		Loader: string(physx),
	} {
		p := filepath.Join(game, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "Dawngate")
}

var dllTime = time.Date(2026, 10, 9, 11, 35, 0, 0, time.Local)

func inputs() Files {
	return Files{FS: fstest.MapFS{
		"build/dgsim.dll":       {Data: []byte("MZ ... dgsim.dll loaded ..."), ModTime: dllTime},
		"lua/dgsim_a.lua":       {Data: []byte("-- a\r\nprint(1)\r\n")},
		"lua/dgsim_b.lua":       {Data: []byte("-- b\n")},
		"lua/notes.txt":         {Data: []byte("not a probe")},
		"lua/sub/dgsim_c.lua":   {Data: []byte("-- below: not copied")},
		"other/dgsim_other.lua": {Data: []byte("-- elsewhere")},
	}, DLL: "build/dgsim.dll", Lua: "lua"}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallCheckRemove(t *testing.T) {
	cp := fakeCopy(t, t.TempDir())
	game := filepath.Join(cp, "game")
	f := inputs()
	old := gameLoaderSHA256
	gameLoaderSHA256 = sum(physx)
	defer func() { gameLoaderSHA256 = old }()

	if probs, err := Check(cp, f); err != nil || len(probs) != 4 {
		t.Errorf("before: %q %v", probs, err)
	}
	res, err := Install(cp, f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.RenamedOriginal || !res.WroteIni || strings.Join(res.Lua, ",") != "dgsim_a.lua,dgsim_b.lua" || res.DLLSHA256 != sum([]byte("MZ ... dgsim.dll loaded ...")) {
		t.Errorf("result %+v", res)
	}
	if read(t, filepath.Join(game, LoaderOrig)) != string(physx) || read(t, filepath.Join(game, Loader)) != "MZ ... dgsim.dll loaded ..." {
		t.Error("loader files")
	}
	if st, _ := os.Stat(filepath.Join(game, Loader)); !st.ModTime().Equal(dllTime) {
		t.Errorf("PhysXLoader.dll time %v, want dgsim.dll's %v", st.ModTime(), dllTime)
	}
	if read(t, filepath.Join(game, "dgsim_a.lua")) != "-- a\r\nprint(1)\r\n" || read(t, filepath.Join(game, Ini)) != string(DefaultIni()) {
		t.Error("lua or ini bytes")
	}
	for _, n := range []string{"notes.txt", "dgsim_c.lua", "dgsim_other.lua"} {
		if _, err := os.Stat(filepath.Join(game, n)); err == nil {
			t.Errorf("%s copied", n)
		}
	}
	if probs, err := Check(cp, f); err != nil || len(probs) != 0 {
		t.Errorf("after: %q %v", probs, err)
	}
	// again: the original stays, the operator's dgsim.ini stays, the probes are refreshed
	if err := os.WriteFile(filepath.Join(game, Ini), []byte("[host]\r\ncore_only=0\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(game, "dgsim_b.lua"), []byte("-- changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if probs, _ := Check(cp, f); len(probs) != 1 || probs[0] != "1 Lua probe(s) differ from gms/engine/lua (dgsim_b.lua...)" {
		t.Errorf("changed probe: %q", probs)
	}
	if err := os.WriteFile(filepath.Join(game, "dgsim_a.lua"), []byte("-- a\nprint(1)\n"), 0o644); err != nil { // line ends only
		t.Fatal(err)
	}
	res, err = Install(cp, f, Options{})
	if err != nil || res.RenamedOriginal || res.WroteIni || read(t, filepath.Join(game, Ini)) != "[host]\r\ncore_only=0\r\n" ||
		read(t, filepath.Join(game, "dgsim_b.lua")) != "-- b\n" {
		t.Errorf("second install: %+v %v", res, err)
	}
	if _, err := Install(cp, f, Options{Ini: []byte("[dgsim]\n")}); err != nil || read(t, filepath.Join(game, Ini)) != "[dgsim]\n" {
		t.Errorf("--ini: %v", err)
	}
	gameLoaderSHA256 = sum([]byte("another"))
	if _, err := Check(cp, f); !errors.Is(err, ErrNotTheGamesLoader) {
		t.Errorf("a foreign original: %v", err)
	}
	gameLoaderSHA256 = sum(physx)
	if err := Remove(cp); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(game, Loader)) != string(physx) {
		t.Error("loader not restored")
	}
	entries, _ := os.ReadDir(game)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "Data,Dawngate.exe,Dawngate.exe.orig,PhysXLoader.dll" {
		t.Errorf("after remove: %v", names)
	}
}

func TestInstallRefusals(t *testing.T) {
	f := inputs()
	// not patched: the exe equals its .orig
	cp := fakeCopy(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(cp, "game", "Dawngate.exe"), []byte("exe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(cp, f, Options{}); err == nil || !strings.Contains(err.Error(), "unpatched") {
		t.Errorf("unpatched: %v", err)
	}
	// a .orig missing
	cp = fakeCopy(t, t.TempDir())
	if err := os.Remove(filepath.Join(cp, "game", "Data", "Config_Ship.package.orig")); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(cp, f, Options{}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("no .orig: %v", err)
	}
	// a loader that isn't PhysX's, or is dgsim already without the original
	for data, want := range map[string]string{"MZ something else": "doesn't look like PhysX's loader",
		"MZ dgsim.dll loaded": "already dgsim"} {
		cp = fakeCopy(t, t.TempDir())
		if err := os.WriteFile(filepath.Join(cp, "game", Loader), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Install(cp, f, Options{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", data, err)
		}
	}
	// a DLL that isn't dgsim.dll, a missing one
	cp = fakeCopy(t, t.TempDir())
	bad := inputs()
	bad.FS.(fstest.MapFS)["build/dgsim.dll"] = &fstest.MapFile{Data: []byte("MZ not ours")}
	if _, err := Install(cp, bad, Options{}); err == nil || !strings.Contains(err.Error(), "is not dgsim.dll") {
		t.Errorf("foreign DLL: %v", err)
	}
	bad.DLL = "build/none.dll"
	if _, err := Install(cp, bad, Options{}); err == nil {
		t.Error("a missing DLL accepted")
	}
	// the pristine extraction
	pristine := fakeCopy(t, filepath.Join(t.TempDir(), "client", "ia_Dawngate"))
	if _, err := Install(pristine, f, Options{}); err == nil || !strings.Contains(err.Error(), "pristine") {
		t.Errorf("client/ia_*: %v", err)
	}
}

// On request (DG_PYTHON=<.venv's python.exe>): setup_server_copy.py and Install on two equal fake copies, with the
// repository's Lua probes and the main checkout's dgsim.dll (or a stand-in), give the same files byte for byte.
func TestInstallMatchesSetupServerCopy(t *testing.T) {
	py := os.Getenv("DG_PYTHON")
	if py == "" {
		t.Skip("set DG_PYTHON=<.venv\\Scripts\\python.exe> to compare with setup_server_copy.py")
	}
	repo := setuptest.Repo()
	root := t.TempDir()
	dll := setuptest.Find(os.Getenv("DG_DGSIM_DLL"), "gms/build/Release/dgsim.dll")
	in := filepath.Join(root, "in")
	if err := os.MkdirAll(filepath.Join(in, "lua"), 0o755); err != nil {
		t.Fatal(err)
	}
	dllBytes := []byte("MZ stand-in: dgsim.dll loaded")
	if dll != "" {
		var err error
		if dllBytes, err = os.ReadFile(dll); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(in, "dgsim.dll"), dllBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	luaDir := filepath.Join(repo, "gms", "engine", "lua")
	entries, err := os.ReadDir(luaDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(luaDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(in, "lua", e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pyCopy := fakeCopy(t, filepath.Join(root, "py"))
	goCopy := fakeCopy(t, filepath.Join(root, "go"))
	cmd := exec.Command(py, "-I", filepath.Join(repo, "re", "scripts", "setup_server_copy.py"), pyCopy, pyCopy,
		filepath.Join(in, "dgsim.dll"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setup_server_copy.py: %v\n%s", err, out)
	}
	if _, err := Install(goCopy, Files{FS: os.DirFS(in), DLL: "dgsim.dll", Lua: "lua"}, Options{}); err != nil {
		t.Fatal(err)
	}
	a, b := tree(t, pyCopy), tree(t, goCopy)
	if len(a) != len(b) {
		t.Errorf("%d files from setup_server_copy.py, %d from Install", len(a), len(b))
	}
	var names []string
	for n := range a {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !bytes.Equal(a[n], b[n]) {
			t.Errorf("%s differs", n)
		}
	}
	lua := 0
	for _, n := range names {
		if strings.HasSuffix(n, ".lua") {
			lua++
		}
	}
	t.Logf("%d files equal (dgsim.dll %s, %d Lua probes)", len(names), map[bool]string{true: "built", false: "stand-in"}[dll != ""], lua)
}

func tree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
