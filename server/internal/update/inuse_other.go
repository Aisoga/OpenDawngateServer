//go:build !windows

package update

// Running reports whether the program file exe runs; only Windows tells (Start-Client is a Windows program).
func Running(exe string) bool { return false }
