package main

import (
	"syscall"
	"unsafe"

	"dawngate/server/internal/winres"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	beginUpdateResource = kernel32.NewProc("BeginUpdateResourceW")
	updateResource      = kernel32.NewProc("UpdateResourceW")
	endUpdateResource   = kernel32.NewProc("EndUpdateResourceW")
)

// updateVersion replaces the file's version resource (RT_VERSION VersionID, language Lang) by data and keeps the
// other resources (Windows' resource update API).
func updateVersion(path string, data []byte) error {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, _, err := beginUpdateResource.Call(uintptr(unsafe.Pointer(p)), 0)
	if h == 0 {
		return err
	}
	ok, _, err := updateResource.Call(h, winres.RTVersion, winres.VersionID, winres.Lang,
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	if ok == 0 {
		endUpdateResource.Call(h, 1) // discard
		return err
	}
	if ok, _, err := endUpdateResource.Call(h, 0); ok == 0 {
		return err
	}
	return nil
}
