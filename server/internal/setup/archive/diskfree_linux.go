package archive

import "syscall"

// freeBytes asks statfs for the bytes free to an unprivileged user on dir's file system (Start-Server on Linux).
func freeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
