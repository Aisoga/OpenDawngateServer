package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RunLockFile is the setup folder's server lock: the program that runs the folder's server (Start-Server.exe, or
// Start-Client.exe for "This PC") holds it while the server runs, so a folder never has two servers (both would use
// its accounts, its match-host copy and its certificates), and a Start-Client on the same folder finds the running
// server through it.
const RunLockFile = "server.lock"

// The run lock's states.
const (
	RunSetup   = "setup"   // the holder sets the folder up or starts its server
	RunRunning = "running" // the server answers (Dial, GatewayPort)
)

// RunLock is what the lock file says about the folder's server.
type RunLock struct {
	Pid     int
	Program string // Start-Server, Start-Client
	// Address is the server's address (its certificate folder certs\<Address>); Dial the address its tunnel's gateway
	// listens on (Start-Server's bind; = Address but behind a router); GatewayPort its tunnel port (0: no tunnel).
	Address, Dial string
	GatewayPort   int
	State         string // RunSetup, RunRunning
	Pin           string // the gateway key's pin (64 hex digits), once running with the tunnel
}

func (r RunLock) text() string {
	return fmt.Sprintf("pid=%d\nprogram=%s\naddress=%s\ndial=%s\ngateway_port=%d\nstate=%s\npin=%s\n", r.Pid, r.Program,
		r.Address, r.Dial, r.GatewayPort, r.State, r.Pin)
}

func parseRunLock(b []byte) RunLock {
	var r RunLock
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r", ""), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "pid":
			r.Pid, _ = strconv.Atoi(v)
		case "program":
			r.Program = v
		case "address":
			r.Address = v
		case "dial":
			r.Dial = v
		case "gateway_port":
			r.GatewayPort, _ = strconv.Atoi(v)
		case "state":
			r.State = v
		case "pin":
			r.Pin = v
		}
	}
	if r.Dial == "" {
		r.Dial = r.Address
	}
	return r
}

// ReadRunLock returns the folder's server lock when a running process other than this one holds it.
func ReadRunLock(root string) (RunLock, bool) {
	r, held, _ := readRunLock(filepath.Join(root, RunLockFile))
	return r, held
}

// readRunLock reads the lock file: held when a running process other than this one has it. A file being replaced
// (Set renames a new one over it) may refuse a read for a moment: it is read again, and an error that stays is
// returned, so that nobody takes over a lock it couldn't read.
func readRunLock(path string) (RunLock, bool, error) {
	var b []byte
	var err error
	for try := 0; try < 10; try++ {
		if b, err = os.ReadFile(path); err == nil || errors.Is(err, os.ErrNotExist) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		return RunLock{}, false, err
	}
	r := parseRunLock(b)
	return r, r.Pid > 0 && r.Pid != os.Getpid() && processRunning(r.Pid), nil
}

// RunLockHandle is a held run lock.
type RunLockHandle struct {
	path string
	mu   sync.Mutex
	lock RunLock
	gone bool
}

// ErrServerRuns: another running process holds the folder's server lock (TakeRunLock's held says which).
var ErrServerRuns = errors.New("this folder's server runs in another program")

// TakeRunLock takes the folder's server lock for this process with r's contents (Pid set here). A lock whose process
// has ended (or this process's own) is taken over; one held by another running process returns ErrServerRuns and
// that lock's contents.
func TakeRunLock(root string, r RunLock) (*RunLockHandle, RunLock, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, RunLock{}, err
	}
	r.Pid = os.Getpid()
	if r.Dial == "" {
		r.Dial = r.Address
	}
	path := filepath.Join(root, RunLockFile)
	for try := 0; try < 2; try++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, werr := f.WriteString(r.text())
			f.Close()
			if werr != nil {
				os.Remove(path)
				return nil, RunLock{}, werr
			}
			return &RunLockHandle{path: path, lock: r}, RunLock{}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, RunLock{}, err
		}
		held, ok, err := readRunLock(path)
		if errors.Is(err, os.ErrNotExist) {
			continue // released meanwhile
		}
		if err != nil {
			return nil, RunLock{}, err
		}
		if ok {
			return nil, held, fmt.Errorf("%w (%s, pid %d; %s)", ErrServerRuns, held.Program, held.Pid, path)
		}
		os.Remove(path) // left by a process that ended, or by this one
	}
	return nil, RunLock{}, fmt.Errorf("%w: %s", ErrServerRuns, path)
}

// Set rewrites the lock's state and pin (the server started).
func (h *RunLockHandle) Set(state, pin string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.gone {
		return nil
	}
	h.lock.State, h.lock.Pin = state, pin
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(h.lock.text()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, h.path)
}

// Release removes the lock (once).
func (h *RunLockHandle) Release() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.gone {
		h.gone = true
		os.Remove(h.path)
	}
}
