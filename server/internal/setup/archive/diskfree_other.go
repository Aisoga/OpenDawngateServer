//go:build !windows

package archive

import "errors"

// freeBytes isn't implemented off Windows (the client and its setup are Windows-only); Extract then skips the check.
func freeBytes(string) (uint64, error) { return 0, errors.ErrUnsupported }
