package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LockFile is the setup folder's lock: one setup at a time (Start-Server's and Start-Client's may be started together).
const LockFile = "setup.lock"

// ErrLocked: another process holds the setup folder's lock.
var ErrLocked = errors.New("another setup is running in this folder")

// Lock takes the setup folder's lock (creating the folder) and returns its release. A lock whose process has ended is
// taken over; one held by a running process is ErrLocked, with its pid in the message.
func Lock(root string) (func(), error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(root, LockFile)
	for try := 0; try < 2; try++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "pid=%d\n", os.Getpid())
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		b, _ := os.ReadFile(path)
		pid, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "pid=")))
		if pid > 0 && pid != os.Getpid() && processRunning(pid) {
			return nil, fmt.Errorf("%w (pid %d; %s): wait until it has finished", ErrLocked, pid, path)
		}
		os.Remove(path) // left by a process that ended
	}
	return nil, fmt.Errorf("%w: %s", ErrLocked, path)
}
