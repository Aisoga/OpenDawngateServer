package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The folder's server lock: taken, rewritten, released; a dead process's lock taken over; another running process's
// refused with its contents.
func TestRunLock(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, RunLockFile)
	h, _, err := TakeRunLock(root, RunLock{Program: "Start-Client", Address: "127.0.1.11", GatewayPort: 42128, State: RunSetup})
	if err != nil {
		t.Fatal(err)
	}
	if _, held := ReadRunLock(root); held {
		t.Error("this process's own lock read as another's")
	}
	if err := h.Set(RunRunning, "ab"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if r := parseRunLock(raw); r.Pid != os.Getpid() || r.State != RunRunning || r.Pin != "ab" || r.Dial != "127.0.1.11" || r.GatewayPort != 42128 {
		t.Errorf("the lock: %+v", r)
	}
	h.Release()
	h.Release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("released: %v", err)
	}
	// a dead process's lock is taken over
	os.WriteFile(path, []byte("pid=999999\nprogram=Start-Server\n"), 0o644)
	if h, _, err = TakeRunLock(root, RunLock{Program: "Start-Server", Address: "127.0.0.1"}); err != nil {
		t.Fatalf("a dead process's lock: %v", err)
	}
	h.Release()
	// another running process's (the test's parent) is refused
	os.WriteFile(path, []byte("pid="+strconv.Itoa(os.Getppid())+"\nprogram=Start-Server\naddress=203.0.113.7\ndial=192.168.1.20\n"+
		"gateway_port=42128\nstate=running\npin=cd\n"), 0o644)
	r, held := ReadRunLock(root)
	if !held || r.Program != "Start-Server" || r.Dial != "192.168.1.20" || r.Address != "203.0.113.7" {
		t.Errorf("ReadRunLock: %+v %v", r, held)
	}
	_, other, err := TakeRunLock(root, RunLock{Program: "Start-Client", Address: "127.0.1.11"})
	if !errors.Is(err, ErrServerRuns) || other.Pid != os.Getppid() || !strings.Contains(err.Error(), "Start-Server") {
		t.Errorf("another process's lock: %+v %v", other, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the other's lock removed: %v", err)
	}
}
