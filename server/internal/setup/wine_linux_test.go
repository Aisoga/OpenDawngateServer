package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeWine writes a stand-in for wine: --version, wineboot --init (a 64-bit prefix: system.reg and SysWOW64's cmd.exe)
// and running that cmd.exe (exit 7). mode "no32" answers as Debian's wine without wine32 when the 32-bit cmd.exe runs;
// "nowow" makes a 64-bit prefix without the 32-bit folder (Wine without its 32-bit part). It logs each call's
// WINEPREFIX and WINEDLLOVERRIDES.
func fakeWine(t *testing.T, mode string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.txt")
	run32 := `exit 7`
	if mode == "no32" {
		run32 = `echo "it looks like wine32 is missing, you should install it."; exit 1`
	}
	win := "syswow64"
	if mode == "nowow" {
		win = "system32"
	}
	script := `#!/bin/sh
echo "$WINEPREFIX $WINEDLLOVERRIDES $*" >>` + log + `
case "$1" in
--version) echo "wine-8.0 (Debian 8.0~repack-4)";;
wineboot) mkdir -p "$WINEPREFIX/drive_c/windows/` + win + `" && echo "#arch=win64" >"$WINEPREFIX/system.reg" && touch "$WINEPREFIX/drive_c/windows/` + win + `/cmd.exe";;
*cmd.exe) ` + run32 + `;;
*) exit 3;;
esac
`
	wine := filepath.Join(dir, "wine")
	if err := os.WriteFile(wine, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wineserver"), []byte("#!/bin/sh\necho \"wineserver $*\" >>"+log+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return wine, log
}

// The Wine step: missing Wine reported with what to install; a new prefix made, its 32-bit check, the marker; a second
// run current; Check reports without writing; a Wine without its 32-bit part fails and leaves no prefix behind.
func TestWine(t *testing.T) {
	t.Setenv("WINEDLLOVERRIDES", "")
	os.Unsetenv("WINEDLLOVERRIDES")
	root := t.TempDir()
	var out bytes.Buffer
	s := &Setup{Layout: Layout{Root: root}, Out: &out}
	if _, err := s.Wine(WineOptions{Command: "no-such-wine-here"}); err == nil || !strings.Contains(out.String(), "[MISSING]  wine") {
		t.Fatalf("no wine: %v\n%s", err, out.String())
	}

	wine, log := fakeWine(t, "")
	out.Reset()
	s = &Setup{Layout: Layout{Root: root}, Out: &out}
	env, err := s.Wine(WineOptions{Command: wine})
	if err != nil || env.Wine != wine || env.Prefix != filepath.Join(root, "wine") || env.Version != "wine-8.0 (Debian 8.0~repack-4)" {
		t.Fatalf("a new prefix: %v %+v\n%s", err, env, out.String())
	}
	if !strings.Contains(out.String(), "[done]     wine prefix    wine made in") {
		t.Errorf("no [done] wine prefix:\n%s", out.String())
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), env.Prefix+" mscoree,mshtml= wineboot --init") || !strings.Contains(string(calls), "cmd.exe /c exit 7") ||
		!strings.Contains(string(calls), "wineserver -w") {
		t.Errorf("the calls:\n%s", calls)
	}
	out.Reset()
	if _, err := (&Setup{Layout: Layout{Root: root}, Out: &out}).Wine(WineOptions{Command: wine}); err != nil ||
		!strings.Contains(out.String(), "[ok]       wine prefix") {
		t.Errorf("current: %v\n%s", err, out.String())
	}
	// another Wine version: checked again, the prefix kept
	os.WriteFile(filepath.Join(env.Prefix, wineMarker), []byte("wine-7.0\n"), 0o644)
	out.Reset()
	if _, err := (&Setup{Layout: Layout{Root: root}, Out: &out, Check: true}).Wine(WineOptions{Command: wine}); err == nil ||
		!strings.Contains(out.String(), "[STALE]") {
		t.Errorf("check, another version: %v\n%s", err, out.String())
	}
	out.Reset()
	if _, err := (&Setup{Layout: Layout{Root: root}, Out: &out}).Wine(WineOptions{Command: wine}); err != nil ||
		!strings.Contains(out.String(), "checked again with wine-8.0") {
		t.Errorf("another version: %v\n%s", err, out.String())
	}

	// no 32-bit part (the 32-bit cmd.exe doesn't run, or the prefix has none): failed, and the prefix made for it
	// removed; a WINEPREFIX of the environment is never removed
	root2 := t.TempDir()
	for mode, want := range map[string]string{"no32": "wine32 is missing", "nowow": "no 32-bit Windows folder"} {
		wine32, _ := fakeWine(t, mode)
		out.Reset()
		if _, err := (&Setup{Layout: Layout{Root: root2}, Out: &out}).Wine(WineOptions{Command: wine32}); err == nil ||
			!strings.Contains(out.String(), "[FAILED]   wine 32-bit") || !strings.Contains(out.String(), want) {
			t.Errorf("%s: %v\n%s", mode, err, out.String())
		}
		if _, err := os.Stat(filepath.Join(root2, "wine")); !os.IsNotExist(err) {
			t.Errorf("%s: the prefix stayed: %v", mode, err)
		}
		theirs := filepath.Join(t.TempDir(), "their-prefix")
		if _, err := (&Setup{Layout: Layout{Root: root2}, Out: &out}).Wine(WineOptions{Command: wine32, Prefix: theirs}); err == nil {
			t.Errorf("%s: their prefix passed", mode)
		}
		if _, err := os.Stat(filepath.Join(theirs, "system.reg")); err != nil {
			t.Errorf("%s: their prefix removed: %v", mode, err)
		}
	}
	// Check on a folder without a prefix
	out.Reset()
	if _, err := (&Setup{Layout: Layout{Root: root2}, Out: &out, Check: true}).Wine(WineOptions{Command: wine}); err == nil ||
		!strings.Contains(out.String(), "[MISSING]  wine prefix") {
		t.Errorf("check, no prefix: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(root2, "wine")); !os.IsNotExist(err) {
		t.Errorf("Check made the prefix: %v", err)
	}
}

// A lock's pid with another start (a reused pid) isn't its holder; the same start is.
func TestLockReusedPid(t *testing.T) {
	parent := os.Getppid()
	if processStart(parent) == "" {
		t.Skip("no /proc")
	}
	if !holderRunning(parent, processStart(parent)) || !holderRunning(parent, "") {
		t.Error("the running parent not read as the holder")
	}
	if holderRunning(parent, "another-boot:1") {
		t.Error("a reused pid read as the holder")
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, LockFile), []byte("pid="+strconv.Itoa(parent)+"\nstarted=another-boot:1\n"), 0o644)
	release, err := Lock(root)
	if err != nil {
		t.Fatalf("a reused pid's setup lock: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(root, LockFile))
	if !strings.Contains(string(b), "started="+processStart(os.Getpid())) {
		t.Errorf("the lock: %q", b)
	}
	release()
}
