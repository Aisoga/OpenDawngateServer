//go:build !windows

package setup

import (
	"os"
	"syscall"
)

// processRunning reports whether the process pid still runs (signal 0).
func processRunning(pid int) bool {
	p, err := os.FindProcess(pid)
	return err == nil && p.Signal(syscall.Signal(0)) == nil
}
