//go:build !windows

package main

import "errors"

func updateVersion(string, []byte) error {
	return errors.New("set needs Windows (its resource update API)")
}
