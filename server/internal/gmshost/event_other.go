//go:build !windows

package gmshost

// LauncherEventName is the named event Dawngate.exe expects (see event_windows.go).
const LauncherEventName = `Global\Dawngate L2G`

// LauncherEvent does nothing outside Windows (the host is a Windows program).
func LauncherEvent() error { return nil }
