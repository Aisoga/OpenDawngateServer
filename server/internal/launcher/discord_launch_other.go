//go:build !windows

package launcher

import "errors"

// userRegistry has no registry outside Windows: Discord's launch of a game is a Windows matter here.
type userRegistry struct{}

func (userRegistry) Get(string, string) (string, bool) { return "", false }

func (userRegistry) Set(string, string, string) error { return errors.ErrUnsupported }
