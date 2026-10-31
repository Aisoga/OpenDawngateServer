package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"dawngate/server/internal/testmaps"
)

// buildTool builds one of the module's commands (pkg relative to server\) into dir, with the release name set as
// the release's build sets it, so both programs report the same version.
func buildTool(t *testing.T, dir, name, pkg string) string {
	t.Helper()
	gobin, err := exec.LookPath("go")
	if err != nil {
		gobin = filepath.Join(runtime.GOROOT(), "bin", "go")
	}
	exe := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command(gobin, "build", "-trimpath", "-ldflags",
		"-X dawngate/server/internal/version.release=mapc-test", "-o", exe, pkg)
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, out)
	}
	return exe
}

type result struct {
	code           int
	stdout, stderr string
}

func runExe(t *testing.T, exe string, args ...string) result {
	t.Helper()
	var out, errw bytes.Buffer
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = &out, &errw
	err := cmd.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		t.Fatalf("%s: %v", exe, err)
	}
	return result{cmd.ProcessState.ExitCode(), out.String(), errw.String()}
}

// readTree is every file under dir by its relative name.
func readTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestMapcSameAsTool (MAPED-REL1): Start-Client.exe --mapc <args> prints what tools/mapc <args> prints, byte for
// byte (stdout, stderr, exit code), on every fixture package: check --json, preview --json (and the files preview
// writes), version --json, a broken package and a usage error. Both programs are built as the release builds them.
func TestMapcSameAsTool(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two programs")
	}
	bin := t.TempDir()
	tool := buildTool(t, bin, "mapc", "./tools/mapc")
	client := buildTool(t, bin, "start-client", "./cmd/start-client")
	same := func(args ...string) result {
		t.Helper()
		a := runExe(t, tool, args...)
		b := runExe(t, client, append([]string{"--mapc"}, args...)...)
		if a != b {
			t.Errorf("%q: tools/mapc gave exit %d\n%s%s\nStart-Client --mapc gave exit %d\n%s%s", args, a.code,
				a.stdout, a.stderr, b.code, b.stdout, b.stderr)
		}
		return a
	}

	if r := same("version", "--json"); r.code != 0 ||
		r.stdout != "{\n  \"release\": \"mapc-test\",\n  \"format\": 1,\n  \"api\": 1\n}\n" {
		t.Errorf("version --json: exit %d: %q", r.code, r.stdout)
	}
	if r := same("frobnicate"); r.code != 2 || r.stderr == "" {
		t.Errorf("an unknown command: exit %d: %q", r.code, r.stderr)
	}
	broken := filepath.Join(t.TempDir(), "CM_Broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "map.json"), []byte(`{"format": 1, "name": `), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := same("check", "--json", broken); r.code != 1 {
		t.Errorf("a broken package: exit %d: %s", r.code, r.stdout)
	}

	ents, err := os.ReadDir(testmaps.Dir())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		pkg := filepath.Join(testmaps.Dir(), e.Name())
		if _, err := os.Stat(filepath.Join(pkg, "map.json")); err != nil {
			continue
		}
		n++
		if r := same("check", "--json", pkg); r.stdout == "" {
			t.Errorf("%s: check printed nothing (exit %d)", e.Name(), r.code)
		}
		// preview: the same folder for both (its name is in the JSON), emptied in between; the files the same too
		dir := filepath.Join(t.TempDir(), "preview")
		a := runExe(t, tool, "preview", "--json", pkg, dir)
		af := readTree(t, dir)
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		b := runExe(t, client, "--mapc", "preview", "--json", pkg, dir)
		bf := readTree(t, dir)
		if a != b || a.stdout == "" {
			t.Errorf("%s: preview: tools/mapc gave exit %d %q, Start-Client --mapc exit %d %q", e.Name(), a.code,
				a.stdout, b.code, b.stdout)
		}
		if len(af) != len(bf) {
			t.Errorf("%s: preview wrote %d and %d files", e.Name(), len(af), len(bf))
		}
		for name, data := range af {
			if !bytes.Equal(data, bf[name]) {
				t.Errorf("%s: preview's %s differs", e.Name(), name)
			}
		}
	}
	if n < 2 {
		t.Fatalf("%d fixture packages", n)
	}
}
