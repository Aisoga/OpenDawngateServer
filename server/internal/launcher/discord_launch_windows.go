package launcher

import (
	"syscall"
	"unsafe"
)

// userRegistry reads and writes string values under HKEY_CURRENT_USER (advapi32; the standard library has no
// registry package of its own outside internal/).
type userRegistry struct{}

var (
	advapi32            = syscall.NewLazyDLL("advapi32.dll")
	procRegCreateKeyExW = advapi32.NewProc("RegCreateKeyExW")
	procRegOpenKeyExW   = advapi32.NewProc("RegOpenKeyExW")
	procRegQueryValueEx = advapi32.NewProc("RegQueryValueExW")
	procRegSetValueExW  = advapi32.NewProc("RegSetValueExW")
	procRegCloseKey     = advapi32.NewProc("RegCloseKey")
)

const (
	hkeyCurrentUser = 0x80000001
	keyRead         = 0x20019
	keyWrite        = 0x20006
	regSZ           = 1
)

func (userRegistry) Get(path, name string) (string, bool) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	var h syscall.Handle
	if r, _, _ := procRegOpenKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(p)), 0, keyRead, uintptr(unsafe.Pointer(&h))); r != 0 {
		return "", false
	}
	defer procRegCloseKey.Call(uintptr(h))
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", false
	}
	var typ, size uint32
	if r, _, _ := procRegQueryValueEx.Call(uintptr(h), uintptr(unsafe.Pointer(n)), 0, uintptr(unsafe.Pointer(&typ)), 0,
		uintptr(unsafe.Pointer(&size))); r != 0 || typ != regSZ {
		return "", false
	}
	if size == 0 {
		return "", true
	}
	buf := make([]uint16, size/2+1)
	if r, _, _ := procRegQueryValueEx.Call(uintptr(h), uintptr(unsafe.Pointer(n)), 0, uintptr(unsafe.Pointer(&typ)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))); r != 0 {
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}

func (userRegistry) Set(path, name, value string) error {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var h syscall.Handle
	if r, _, _ := procRegCreateKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(p)), 0, 0, 0, keyWrite|keyRead, 0,
		uintptr(unsafe.Pointer(&h)), 0); r != 0 {
		return syscall.Errno(r)
	}
	defer procRegCloseKey.Call(uintptr(h))
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	v, err := syscall.UTF16FromString(value)
	if err != nil {
		return err
	}
	if r, _, _ := procRegSetValueExW.Call(uintptr(h), uintptr(unsafe.Pointer(n)), 0, regSZ, uintptr(unsafe.Pointer(&v[0])),
		uintptr(len(v)*2)); r != 0 {
		return syscall.Errno(r)
	}
	return nil
}
