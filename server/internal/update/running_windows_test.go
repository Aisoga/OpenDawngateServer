package update_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"dawngate/server/internal/update"
	"dawngate/server/internal/update/updatetest"
)

// helperEnv makes the test binary a program that just runs (a stand-in for a running Start-Client or Start-Server).
const helperEnv = "DG_UPDATE_TEST_SLEEP"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		time.Sleep(60 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runCopy copies the test binary to path and runs it until the test ends.
func runCopy(t *testing.T, path string) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, "-test.run=^$")
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	for i := 0; i < 50 && !update.Running(path); i++ {
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRunningProgram(t *testing.T) {
	self, _ := os.Executable()
	if !update.Running(self) {
		t.Fatal("the running test binary isn't Running")
	}
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.exe")
	os.WriteFile(plain, []byte("x"), 0o644)
	if update.Running(plain) || update.Running(filepath.Join(dir, "missing.exe")) {
		t.Fatal("a file that doesn't run is Running")
	}
}

// The running Start-Client.exe is renamed (Windows allows that), replaced, and deleted at the next start once it ended.
func TestReplaceRunningClient(t *testing.T) {
	dir := player(t, release("v0.5"))
	exe := filepath.Join(dir, "Start-Client.exe")
	runCopy(t, exe)
	nw := release("v0.6")
	nw.Files["Start-Client.exe"] = "client v0.6"
	root, b := stage(t, nw)
	res, err := update.Replace(dir, root, b, exe)
	if err != nil {
		t.Fatal(err)
	}
	if res.OldExe != "Start-Client.old.exe" || read(t, dir, "Start-Client.exe") != "client v0.6" {
		t.Fatalf("result %+v", res)
	}
	old := filepath.Join(dir, "Start-Client.old.exe")
	if !update.Running(old) {
		t.Fatal("the renamed program doesn't run")
	}
	if gone := update.CleanOld(dir); len(gone) != 0 || !exists(old) {
		t.Fatalf("CleanOld deleted a running program: %v", gone)
	}
}

// A running Start-Server.exe of the folder refuses the update before anything is downloaded.
func TestInstallRefusesRunningServer(t *testing.T) {
	f := updatetest.NewFakeGitHub()
	defer f.Close()
	f.Tag, f.ZipBytes = "v0.6", release("v0.6").Zip(nil)
	src, _ := update.Loopback(f.URL, "test")
	dir := player(t, release("v0.5"))
	runCopy(t, filepath.Join(dir, "Start-Server.exe"))
	r, err := src.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := f.Count()
	u := update.Updater{Source: src, Dir: dir, Work: filepath.Join(dir, "Dawngate-Data", "update"), Out: io.Discard}
	if _, err := u.Install(context.Background(), r); !errors.Is(err, update.ErrServerRunning) || f.Count() != n {
		t.Fatalf("Install with Start-Server running: %v (%d requests)", err, f.Count()-n)
	}
	if read(t, dir, "Start-Client.exe") != "client v0.5" {
		t.Fatal("the folder changed")
	}
}
