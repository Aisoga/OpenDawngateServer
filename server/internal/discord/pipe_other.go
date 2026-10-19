//go:build !windows

package discord

import (
	"errors"
	"fmt"
)

func pipePath(name string) string { return name }

// dialPipe: the status is shown only on Windows (the game runs there; the app's Linux socket isn't used).
func dialPipe(path string) (Conn, error) {
	return nil, fmt.Errorf("%s: %w", path, errors.ErrUnsupported)
}
