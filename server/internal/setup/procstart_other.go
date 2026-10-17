//go:build !linux

package setup

// processStart is unknown outside Linux (procstart_linux.go): the locks then compare the pid alone, as before.
func processStart(int) string { return "" }
