package update

import (
	"errors"
	"os"
	"syscall"
)

// errSharingViolation is ERROR_SHARING_VIOLATION: Windows refuses to open a running program's file for writing.
const errSharingViolation = syscall.Errno(32)

// Running reports whether the program file exe runs (Windows keeps a running program's file open: it can't be opened
// for writing then). A missing file doesn't run.
func Running(exe string) bool {
	f, err := os.OpenFile(exe, os.O_WRONLY, 0)
	if err == nil {
		f.Close()
		return false
	}
	return errors.Is(err, errSharingViolation)
}
