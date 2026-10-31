package launcher

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"dawngate/server/internal/gmshost"
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procIsWindow                 = user32.NewProc("IsWindow")
	procLockSetForegroundWindow  = user32.NewProc("LockSetForegroundWindow")
	procGetLastInputInfo         = user32.NewProc("GetLastInputInfo")
	procSystemParametersInfoW    = user32.NewProc("SystemParametersInfoW")
	procGetTickCount             = kernel32.NewProc("GetTickCount")
	procOpenMutexW               = kernel32.NewProc("OpenMutexW")
)

// UserIdle is how long the user has given no input (GetLastInputInfo), and the user's ForegroundLockTimeout
// (SPI_GETFOREGROUNDLOCKTIMEOUT, 200 s by default): once the idle time passes it, Windows lets any program take the
// foreground (in a test run the game's splash did, the user idle for 15 minutes).
func UserIdle() (idle, lockTimeout time.Duration) {
	var lii struct{ size, time uint32 }
	lii.size = 8
	procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&lii)))
	now, _, _ := procGetTickCount.Call()
	var ms uint32
	procSystemParametersInfoW.Call(0x2000, 0, uintptr(unsafe.Pointer(&ms)), 0) // SPI_GETFOREGROUNDLOCKTIMEOUT
	return time.Duration(uint32(now)-lii.time) * time.Millisecond, time.Duration(ms) * time.Millisecond
}

// ForegroundWindow is the window that has the foreground (0 when none does).
func ForegroundWindow() uintptr {
	h, _, _ := procGetForegroundWindow.Call()
	return h
}

// windowPid is the process that owns window h.
func windowPid(h uintptr) uint32 {
	var pid uint32
	procGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	return pid
}

// ClientMutex is the game's single-instance mutex: a client started without -multipleInstances creates it and exits
// when it exists (the match hosts run with -multipleInstances and don't hold it).
const ClientMutex = `Global\Dawngate`

// ClientRunning reports whether a Dawngate client holds ClientMutex.
func ClientRunning() bool { return mutexExists(ClientMutex) }

func mutexExists(mutex string) bool {
	name, _ := syscall.UTF16PtrFromString(mutex)
	h, _, err := procOpenMutexW.Call(0x00100000, 0, uintptr(unsafe.Pointer(name))) // SYNCHRONIZE
	if h != 0 {
		syscall.CloseHandle(syscall.Handle(h))
		return true
	}
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}

// StartOptions are StartGame's.
type StartOptions struct {
	Args string // the game's command line after the program (none for a player)
	// NoActivate is the test hook's start (with Park): STARTUPINFO asks for SW_SHOWNOACTIVATE. The game doesn't honour
	// it: it shows its splash itself (also with SW_HIDE asked for) and then activates its splash and its
	// still hidden main window itself, which takes the foreground whenever Windows lets this program's children do so.
	NoActivate bool
}

// Game is a started Dawngate.exe.
type Game struct {
	Pid     int
	Exe     string
	process syscall.Handle
}

// StartGame starts the copy's game\Dawngate.exe in game\ as the launcher would (launch_monitor.ps1's way): the
// launcher's event Global\Dawngate L2G is created first and held while this program runs (without it the game starts
// the launcher and exits with -1337).
func StartGame(gameDir string, o StartOptions) (*Game, error) {
	if err := gmshost.LauncherEvent(); err != nil {
		return nil, err
	}
	exe := filepath.Join(gameDir, "game", "Dawngate.exe")
	return startProcess(exe, o)
}

func startProcess(exe string, o StartOptions) (*Game, error) {
	cmdline := syscall.EscapeArg(exe)
	if o.Args != "" {
		cmdline += " " + o.Args
	}
	exeU, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return nil, err
	}
	cmdU, err := syscall.UTF16PtrFromString(cmdline)
	if err != nil {
		return nil, err
	}
	dirU, err := syscall.UTF16PtrFromString(filepath.Dir(exe))
	if err != nil {
		return nil, err
	}
	si := &syscall.StartupInfo{}
	si.Cb = uint32(unsafe.Sizeof(*si))
	if o.NoActivate {
		si.Flags = syscall.STARTF_USESHOWWINDOW
		si.ShowWindow = 4 // SW_SHOWNOACTIVATE
	}
	var pi syscall.ProcessInformation
	if err := syscall.CreateProcess(exeU, cmdU, nil, nil, false, 0, nil, dirU, si, &pi); err != nil {
		return nil, fmt.Errorf("starting %s: %w", exe, err)
	}
	syscall.CloseHandle(pi.Thread)
	return &Game{Pid: int(pi.ProcessId), Exe: exe, process: pi.Process}, nil
}

// Wait waits until the game has exited and returns its exit code.
// ID is the game's process id.
func (g *Game) ID() int { return g.Pid }

func (g *Game) Wait() (uint32, error) {
	if _, err := syscall.WaitForSingleObject(g.process, syscall.INFINITE); err != nil {
		return 0, err
	}
	var code uint32
	err := syscall.GetExitCodeProcess(g.process, &code)
	return code, err
}

// Exited waits up to d for the game to exit.
func (g *Game) Exited(d time.Duration) bool {
	ev, _ := syscall.WaitForSingleObject(g.process, uint32(d/time.Millisecond))
	return ev == syscall.WAIT_OBJECT_0
}

// Close asks the game to close (WM_CLOSE to its windows; in a match the game ignores it, then Kill).
func (g *Game) Close() {
	for _, h := range windowsOf(uint32(g.Pid)) {
		procPostMessageW.Call(uintptr(h), 0x0010, 0, 0) // WM_CLOSE
	}
}

// Kill ends the game.
func (g *Game) Kill() error { return syscall.TerminateProcess(g.process, 1) }

// Release closes the process handle.
func (g *Game) Release() { syscall.CloseHandle(g.process) }

// ParkEvents are what Park tells its caller (each may be nil).
type ParkEvents struct {
	Moved          func(n int)          // n windows were moved off the desktop
	ForegroundBack func(n int, ok bool) // the game had taken the foreground (the n-th time); given back if ok
	Lock           func(locked bool)    // the foreground lock was taken (true) or released (false)
}

// Park keeps the game's windows off the desktop and out of the user's way until stop is closed or the game exits
// (the test hook). Every 50 ms:
//   - each visible window that isn't parked yet goes to (-30000, -30000) and the bottom of the z-order without being
//     activated (SetWindowPos with SWP_NOSIZE | SWP_NOACTIVATE, posted: parkWindows), as launch_monitor.ps1
//     -Background does; posted window messages (the test side's clicks and keys) still reach it;
//   - while the user has been idle longer than ForegroundLockTimeout (UserIdle), when Windows lets every program take
//     the foreground, the foreground is locked (LockSetForegroundWindow), and released as soon as the user is back, so
//     that the user's own programs aren't held back;
//   - when a game window has the foreground anyway, it goes back to the window that had it before (prev: the
//     foreground when the game started, then the last one that wasn't the game's); in a test run its splash had it for 30 ms;
//   - the game's windows lose their taskbar buttons (ITaskbarList::DeleteTab, at each park and every second): the
//     game flashes its button for about 10 s when shaper select opens, and a click on it would bring it in.
func (g *Game) Park(stop <-chan struct{}, prev uintptr, ev ParkEvents) {
	runtime.LockOSThread() // the taskbar object lives in this thread's COM apartment
	defer runtime.UnlockOSThread()
	if r, _, _ := procCoInitializeEx.Call(0, 2); r == 0 || r == 1 { // STA; S_OK, S_FALSE: ours to undo
		defer procCoUninitialize.Call()
	}
	tb := newTaskbar()
	defer tb.release()
	backs, tried, locked, triedAt := 0, false, false, 0
	defer func() {
		if locked {
			procLockSetForegroundWindow.Call(2) // LSFW_UNLOCK
		}
	}()
	for round := 0; ; round++ {
		if idle, timeout := UserIdle(); idle >= timeout && timeout > 0 {
			if r, _, _ := procLockSetForegroundWindow.Call(1); r != 0 && !locked { // LSFW_LOCK
				locked = true
				if ev.Lock != nil {
					ev.Lock(true)
				}
			}
		} else if locked {
			procLockSetForegroundWindow.Call(2) // LSFW_UNLOCK: the user is back
			locked = false
			if ev.Lock != nil {
				ev.Lock(false)
			}
		}
		if fg := ForegroundWindow(); fg != 0 {
			if windowPid(fg) != uint32(g.Pid) {
				prev, tried = fg, false
			} else if ok, _, _ := procIsWindow.Call(prev); prev != 0 && ok != 0 && (!tried || (locked && round-triedAt >= 20)) {
				// once per time the game takes it while the user is here (a refused SetForegroundWindow would flash
				// prev's taskbar button); every second while the user is away and the game keeps it (in a test run one
				// refused call at a match load left the game in the foreground for 90 s). Our own lock refuses our own
				// SetForegroundWindow too: lifted for the call.
				if locked {
					procLockSetForegroundWindow.Call(2) // LSFW_UNLOCK
				}
				r, _, _ := procSetForegroundWindow.Call(prev)
				if locked {
					procLockSetForegroundWindow.Call(1) // LSFW_LOCK
				}
				backs, tried, triedAt = backs+1, true, round
				if ev.ForegroundBack != nil {
					ev.ForegroundBack(backs, r != 0)
				}
			}
		}
		parked := parkWindows(uint32(g.Pid))
		for _, h := range parked {
			tb.deleteTab(h)
		}
		if round%20 == 0 {
			for _, h := range visibleWindowsOf(uint32(g.Pid)) {
				tb.deleteTab(h)
			}
		}
		if len(parked) > 0 && ev.Moved != nil {
			ev.Moved(len(parked))
		}
		select {
		case <-stop:
			return
		case <-time.After(50 * time.Millisecond):
		}
		if g.Exited(0) {
			return
		}
	}
}

// ParkWindows moves the visible top-level windows of process pid that are on the desktop to (-30000, -30000) and the
// bottom of the z-order without activating them; it returns how many it moved. The move is posted to the window's
// thread (SWP_ASYNCWINDOWPOS): a game busy loading doesn't process window messages for seconds (its splash
// and its first main window), and a plain SetWindowPos from here waited that long, so nothing else was parked.
func ParkWindows(pid uint32) int { return len(parkWindows(pid)) }

func visibleWindowsOf(pid uint32) []syscall.Handle {
	var out []syscall.Handle
	for _, h := range windowsOf(pid) {
		if v, _, _ := procIsWindowVisible.Call(uintptr(h)); v != 0 {
			out = append(out, h)
		}
	}
	return out
}

func parkWindows(pid uint32) []syscall.Handle {
	var moved []syscall.Handle
	for _, h := range windowsOf(pid) {
		if v, _, _ := procIsWindowVisible.Call(uintptr(h)); v == 0 {
			continue
		}
		var r struct{ L, T, R, B int32 }
		procGetWindowRect.Call(uintptr(h), uintptr(unsafe.Pointer(&r)))
		if r.L <= -20000 {
			continue
		}
		// HWND_BOTTOM, SWP_NOSIZE | SWP_NOACTIVATE | SWP_ASYNCWINDOWPOS
		x := int32(-30000)
		procSetWindowPos.Call(uintptr(h), 1, uintptr(x), uintptr(x), 0, 0, 0x4011)
		moved = append(moved, h)
	}
	return moved
}

// taskbar is the shell's ITaskbarList (CLSID_TaskbarList): DeleteTab takes a window's button off the taskbar until the
// window is shown again. A nil *taskbar does nothing (no COM, no shell).
type taskbar struct{ obj unsafe.Pointer }

var (
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	clsidTaskbarList     = syscall.GUID{Data1: 0x56FDF344, Data2: 0xFD6D, Data3: 0x11D0, Data4: [8]byte{0x95, 0x8A, 0x00, 0x60, 0x97, 0xC9, 0xA0, 0x90}}
	iidTaskbarList       = syscall.GUID{Data1: 0x56FDF342, Data2: 0xFD6D, Data3: 0x11D0, Data4: [8]byte{0x95, 0x8A, 0x00, 0x60, 0x97, 0xC9, 0xA0, 0x90}}
)

func newTaskbar() *taskbar {
	var obj unsafe.Pointer
	r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidTaskbarList)), 0, 1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(&iidTaskbarList)), uintptr(unsafe.Pointer(&obj)))
	if r != 0 || obj == nil {
		return nil
	}
	t := &taskbar{obj}
	if t.call(3) != 0 { // HrInit
		t.release()
		return nil
	}
	return t
}

// call invokes the method in vtable slot i (IUnknown 0-2, then HrInit 3, AddTab 4, DeleteTab 5, ...).
func (t *taskbar) call(i int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(t.obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(i)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(t.obj)}, args...)...)
	return r
}

func (t *taskbar) deleteTab(h syscall.Handle) {
	if t != nil {
		t.call(5, uintptr(h))
	}
}

func (t *taskbar) release() {
	if t != nil {
		t.call(2)
	}
}

// WindowPos is a window's top-left corner (for tests and logs).
func WindowPos(h syscall.Handle) (int, int) {
	var r struct{ L, T, R, B int32 }
	procGetWindowRect.Call(uintptr(h), uintptr(unsafe.Pointer(&r)))
	return int(r.L), int(r.T)
}

var (
	enumMu    sync.Mutex
	enumPid   uint32
	enumFound []syscall.Handle
	enumCB    = syscall.NewCallback(func(h syscall.Handle, _ uintptr) uintptr {
		var pid uint32
		procGetWindowThreadProcessId.Call(uintptr(h), uintptr(unsafe.Pointer(&pid)))
		if pid == enumPid {
			enumFound = append(enumFound, h)
		}
		return 1
	})
)

// windowsOf lists the top-level windows of process pid.
func windowsOf(pid uint32) []syscall.Handle {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumPid, enumFound = pid, nil
	procEnumWindows.Call(enumCB, 0)
	return append([]syscall.Handle(nil), enumFound...)
}
