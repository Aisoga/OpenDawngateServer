package gmshost

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// gone says whether pid has ended: no such process, or a zombie (its parent is gone, and init reaps it
// later). /proc/<pid>/stat's third field is the state.
func gone(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i < 0 || i+2 >= len(s) || s[i+2] == 'Z' || s[i+2] == 'X'
}

func goneWithinLinux(pid int, d time.Duration) bool {
	for deadline := time.Now().Add(d); ; time.Sleep(20 * time.Millisecond) {
		if gone(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

// H4a: a process that started a host with an ExecLauncher is killed: its host is gone at once (the
// parent-death signal), as the job object does it on Windows (TestHostsEndWithLauncher).
func TestHostsEndWithLauncherLinux(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), launcherEnv+"=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	var pid int
	if _, perr := fmt.Sscanf(line, "host %d", &pid); err != nil || perr != nil {
		t.Fatalf("launcher said %q (%v)", line, err)
	}
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
	if gone(pid) {
		t.Fatal("the host ended before its launcher was killed")
	}
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
		t.Errorf("the host's process group is %d (%v), want its own (%d)", pgid, err, pid)
	}
	start := time.Now()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if !goneWithinLinux(pid, 5*time.Second) {
		t.Fatalf("host pid %d still runs 5 s after its launcher was killed", pid)
	}
	t.Logf("host pid %d gone %v after its launcher pid %d was killed", pid, time.Since(start).Round(time.Millisecond), cmd.Process.Pid)
}

// H4a: a host started through a runner (as "wine Dawngate.exe ..."): the runner gets the exe and its
// arguments, the host its data folder as a Wine path and Wine's defaults, and a kill ends the host's
// whole process group (what the host started too), with exit code -1 (killed).
func TestRunnerLaunchLinux(t *testing.T) {
	data := t.TempDir()
	t.Setenv("WINEDEBUG", "warn+all") // the operator's own setting stays
	script := `echo "args $0 $1"; printf 'env %s|%s|%s|%s\n' "$DGGMS_DATA" "$WINEDLLOVERRIDES" "$WINEDEBUG" "$DGGMS_MATCH"; ` +
		`sleep 300 & echo "child $!"; wait`
	l := &ExecLauncher{Path: "/games/Dawngate/game/Dawngate.exe", Args: []string{"-multipleInstances"},
		Dir: data, DataDir: data, Runner: []string{"/bin/sh", "-c", script}}
	p, err := l.Launch(Env{Match: 7, Control: "127.0.0.1:1", Bind: "127.0.0.1:2"})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(data, "7", "stdout.txt")
	var text string
	var child int
	for deadline := time.Now().Add(10 * time.Second); child == 0; time.Sleep(20 * time.Millisecond) {
		b, _ := os.ReadFile(out)
		text = string(b)
		if i := strings.Index(text, "child "); i >= 0 && strings.HasSuffix(text, "\n") {
			fmt.Sscanf(text[i:], "child %d", &child)
		}
		if time.Now().After(deadline) {
			p.Kill()
			p.Wait()
			t.Fatalf("no child line in stdout.txt: %q", text)
		}
	}
	t.Cleanup(func() { syscall.Kill(child, syscall.SIGKILL) })
	wantData := WinePath(filepath.Join(data, "7"))
	for _, want := range []string{"args /games/Dawngate/game/Dawngate.exe -multipleInstances\n",
		"env " + wantData + "|mscoree,mshtml=|warn+all|7\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("stdout.txt %q lacks %q", text, want)
		}
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	code, err := p.Wait()
	if err != nil || code != -1 {
		t.Errorf("Wait after Kill: code %d, err %v; want -1 (killed)", code, err)
	}
	if !goneWithinLinux(child, 5*time.Second) {
		t.Errorf("the host's child %d still runs after the host was killed (process group)", child)
	}
}
