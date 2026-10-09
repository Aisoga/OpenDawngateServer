package launcher

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procGetConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	comdlg32                  = syscall.NewLazyDLL("comdlg32.dll")
	procGetOpenFileNameW      = comdlg32.NewProc("GetOpenFileNameW")
	ole32                     = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx        = ole32.NewProc("CoInitializeEx")
	procCoUninitialize        = ole32.NewProc("CoUninitialize")
)

// OwnConsole reports whether this program is the only one attached to its console: it was started by a double-click
// (Windows made the console for it), so the window closes when it exits and a message must wait for Enter.
func OwnConsole() bool {
	var pids [4]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n == 1
}

// IsConsole reports whether f is a console (not a file or a pipe).
func IsConsole(f *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}

// openFileName is OPENFILENAMEW.
type openFileName struct {
	structSize    uint32
	owner         uintptr
	instance      uintptr
	filter        *uint16
	customFilter  *uint16
	maxCustFilter uint32
	filterIndex   uint32
	file          *uint16
	maxFile       uint32
	fileTitle     *uint16
	maxFileTitle  uint32
	initialDir    *uint16
	title         *uint16
	flags         uint32
	fileOffset    uint16
	fileExtension uint16
	defExt        *uint16
	custData      uintptr
	hook          uintptr
	templateName  *uint16
	reserved      uintptr
	reserved2     uint32
	flagsEx       uint32
}

// PickZip shows Windows' file dialog for Dawngate.zip (comdlg32 GetOpenFileNameW, owned by the console window) and
// returns the chosen path; an error when it was cancelled.
func PickZip(initialDir string) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// the Explorer-style dialog wants a single-threaded COM apartment on its thread (CoInitializeEx, STA)
	if r, _, _ := procCoInitializeEx.Call(0, 2); r == 0 || r == 1 { // S_OK, S_FALSE: ours to undo
		defer procCoUninitialize.Call()
	}
	filter := []uint16{}
	for _, s := range []string{"Dawngate.zip", "Dawngate.zip;*.zip", "All files", "*.*"} {
		u, _ := syscall.UTF16FromString(s)
		filter = append(filter, u...)
	}
	filter = append(filter, 0)
	buf := make([]uint16, 1024)
	title, _ := syscall.UTF16PtrFromString("Where is Dawngate.zip?")
	var dir *uint16
	if initialDir != "" {
		dir, _ = syscall.UTF16PtrFromString(initialDir)
	}
	owner, _, _ := procGetConsoleWindow.Call()
	ofn := openFileName{owner: owner, filter: &filter[0], filterIndex: 1, file: &buf[0], maxFile: uint32(len(buf)),
		initialDir: dir, title: title,
		flags: 0x00001000 | 0x00000800 | 0x00000008 | 0x00080000} // FILEMUSTEXIST | PATHMUSTEXIST | NOCHANGEDIR | EXPLORER
	ofn.structSize = uint32(unsafe.Sizeof(ofn))
	ok, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ok == 0 {
		return "", errors.New("cancelled")
	}
	return syscall.UTF16ToString(buf), nil
}
