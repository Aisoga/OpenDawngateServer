package launcher

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// childEnv makes the test binary a stand-in "game" (TestMain): it exits with the code in the variable.
const childEnv = "DG_LAUNCHER_TEST_CHILD"

func TestMain(m *testing.M) {
	if code := os.Getenv(childEnv); code != "" {
		var n int
		fmt.Sscan(code, &n)
		time.Sleep(300 * time.Millisecond)
		os.Exit(n)
	}
	os.Exit(m.Run())
}

// startProcess (StartGame's) starts a program in its folder and Wait returns its exit code; with NoActivate too.
func TestStartProcessAndWait(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(childEnv, "7")
	for _, noActivate := range []bool{false, true} {
		g, err := startProcess(exe, StartOptions{NoActivate: noActivate})
		if err != nil {
			t.Fatal(err)
		}
		if g.Pid <= 0 || g.Exited(0) {
			t.Errorf("pid %d, exited at once", g.Pid)
		}
		code, err := g.Wait()
		if err != nil || code != 7 {
			t.Errorf("NoActivate %v: exit %d %v", noActivate, code, err)
		}
		if !g.Exited(0) {
			t.Error("not exited after Wait")
		}
		g.Release()
	}
	t.Setenv(childEnv, "0")
	g, err := startProcess(exe, StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Kill(); err != nil {
		t.Error(err)
	}
	if code, _ := g.Wait(); code != 1 {
		t.Errorf("killed: exit %d", code)
	}
	g.Release()
	if _, err := startProcess(`C:\nowhere\Dawngate.exe`, StartOptions{}); err == nil {
		t.Error("a missing exe started")
	}
}

var (
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   syscall.Handle
	icon       syscall.Handle
	cursor     syscall.Handle
	background syscall.Handle
	menuName   *uint16
	className  *uint16
	iconSm     syscall.Handle
}

// ParkWindows moves a visible window of this process from off-screen (-10000, -10000), where it never shows on a
// monitor, to the parking spot without activating it, and leaves a parked one alone.
func TestParkWindows(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cls, _ := syscall.UTF16PtrFromString("DawngateLauncherTestWindow")
	wc := wndClassEx{wndProc: procDefWindowProcW.Addr(), className: cls}
	wc.size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		t.Fatalf("RegisterClassEx: %v", err)
	}
	// Made visible with WS_VISIBLE, this off-screen test window took the foreground from the user's window once
	// (a test run's focus log, while it ran beside the game), as the test inherits the console's right to set it. Now it
	// is shown without activation.
	const wsPopup = 0x80000000
	const exNoActivateTool = 0x08000000 | 0x00000080
	off := int32(-10000)
	h, _, err := procCreateWindowExW.Call(exNoActivateTool, uintptr(unsafe.Pointer(cls)), 0, wsPopup,
		uintptr(off), uintptr(off), 64, 64, 0, 0, 0, 0)
	if h == 0 {
		t.Fatalf("CreateWindowEx: %v", err)
	}
	defer procDestroyWindow.Call(h)
	// SWP_SHOWWINDOW | SWP_NOACTIVATE | SWP_NOZORDER | SWP_NOMOVE | SWP_NOSIZE
	procSetWindowPos.Call(h, 0, 0, 0, 0, 0, 0x40|0x10|0x4|0x2|0x1)
	if v, _, _ := procIsWindowVisible.Call(h); v == 0 {
		t.Fatal("the test window isn't visible")
	}
	// the taskbar object of the park loop (Park): made in an STA, DeleteTab on a window without a button is harmless
	if r, _, _ := procCoInitializeEx.Call(0, 2); r == 0 || r == 1 {
		defer procCoUninitialize.Call()
	}
	if tb := newTaskbar(); tb == nil {
		t.Log("no ITaskbarList here (no shell); Park then keeps the buttons")
	} else {
		tb.deleteTab(syscall.Handle(h))
		tb.release()
	}
	if x, y := WindowPos(syscall.Handle(h)); x != -10000 || y != -10000 {
		t.Fatalf("made at (%d,%d)", x, y)
	}
	if n := ParkWindows(uint32(os.Getpid())); n != 1 {
		t.Errorf("moved %d windows", n)
	}
	if x, y := WindowPos(syscall.Handle(h)); x != -30000 || y != -30000 {
		t.Errorf("parked at (%d,%d)", x, y)
	}
	if n := ParkWindows(uint32(os.Getpid())); n != 0 {
		t.Errorf("a parked window moved again (%d)", n)
	}
}

// UserIdle reads the idle time and the foreground lock timeout (Park locks the foreground past it).
func TestUserIdle(t *testing.T) {
	idle, timeout := UserIdle()
	if idle < 0 || idle > 50*24*time.Hour || timeout < 0 || timeout > 24*time.Hour {
		t.Errorf("idle %v, ForegroundLockTimeout %v", idle, timeout)
	}
	t.Logf("idle %v, ForegroundLockTimeout %v", idle, timeout)
}

// PickZip's OPENFILENAMEW has the size Windows checks (lStructSize): 152 bytes on 64-bit, 88 on 32-bit. The dialog
// itself is modal and waits for a person, so no test opens it.
func TestOpenFileNameSize(t *testing.T) {
	want := uintptr(152)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		want = 88
	}
	if got := unsafe.Sizeof(openFileName{}); got != want {
		t.Errorf("OPENFILENAMEW: %d bytes, want %d", got, want)
	}
}

func TestMutexExists(t *testing.T) {
	name := fmt.Sprintf(`Local\DawngateLauncherTest%d`, os.Getpid()) // never the game's own Global\Dawngate
	if mutexExists(name) {
		t.Fatal("exists before")
	}
	u, _ := syscall.UTF16PtrFromString(name)
	h, _, err := kernel32.NewProc("CreateMutexW").Call(0, 0, uintptr(unsafe.Pointer(u)))
	if h == 0 {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(syscall.Handle(h))
	if !mutexExists(name) {
		t.Error("not found")
	}
}
