package gmshost

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

// LauncherEventName is the named event Dawngate.exe's main (0x006B2E20) opens at start: without it
// the game starts ..\Launcher\Launcher.exe and exits with -1337; with it, it signals the event and
// carries on (docs/RUNNING_THE_CLIENT.md, Q2). launch_monitor.ps1 creates it for the client; the
// server creates it for its hosts.
const LauncherEventName = `Global\Dawngate L2G`

var (
	eventOnce   sync.Once
	eventHandle syscall.Handle
	eventErr    error
)

// LauncherEvent creates (or opens) the launcher event once and keeps it open for this process's
// lifetime, so every host started later finds it. Two processes holding it (the client's monitor and
// dgserver) share the same event.
func LauncherEvent() error {
	eventOnce.Do(func() {
		name, err := syscall.UTF16PtrFromString(LauncherEventName)
		if err != nil {
			eventErr = err
			return
		}
		// CreateEventW(NULL, bManualReset TRUE, bInitialState FALSE, name), as launch_monitor.ps1 does
		proc := syscall.NewLazyDLL("kernel32.dll").NewProc("CreateEventW")
		h, _, callErr := proc.Call(0, 1, 0, uintptr(unsafe.Pointer(name)))
		if h == 0 {
			eventErr = fmt.Errorf("gmshost: CreateEvent %s: %v", LauncherEventName, callErr)
			return
		}
		eventHandle = syscall.Handle(h)
	})
	return eventErr
}
