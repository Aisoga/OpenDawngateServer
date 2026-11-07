//go:build !windows

package launcher

import "net"

// peerProcess: the release's Start-Client runs on Windows only (wine.go), so nothing else asks who holds a connection.
func peerProcess(net.Conn) (uint32, string, error) { return 0, "", errOwnerUnknown }
