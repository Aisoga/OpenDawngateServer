//go:build !windows

package gmshost

// LauncherEventName is the named event Dawngate.exe expects (see event_windows.go).
const LauncherEventName = `Global\Dawngate L2G`

// LauncherEvent does nothing outside Windows: under Wine the event lives in the Wine server, where
// dgserver can't make it, so the host's DLL makes it itself before the game's main runs (H4a).
func LauncherEvent() error { return nil }
