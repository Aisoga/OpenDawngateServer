//go:build !windows

package launcher

import (
	"errors"
	"os"
)

// OwnConsole is false outside Windows (the release is for Windows; this keeps the module building elsewhere).
func OwnConsole() bool { return false }

// IsConsole reports whether f is a terminal (a character device).
func IsConsole(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// PickZip has no dialog outside Windows.
func PickZip(string) (string, error) { return "", errors.ErrUnsupported }
