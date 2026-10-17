//go:build !windows && !linux

package archive

import "errors"

// freeBytes isn't implemented outside Windows and Linux; Extract then skips the check.
func freeBytes(string) (uint64, error) { return 0, errors.ErrUnsupported }
