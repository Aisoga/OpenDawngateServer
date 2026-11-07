package launcher

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

var (
	modIPHelper             = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTCPTable = modIPHelper.NewProc("GetExtendedTcpTable")
	modKernel32Guard        = syscall.NewLazyDLL("kernel32.dll")
	procQueryImageName      = modKernel32Guard.NewProc("QueryFullProcessImageNameW")
)

const (
	afInet                         = 2
	tcpTableOwnerPidConnections    = 4 // TCP_TABLE_OWNER_PID_CONNECTIONS: the established ones, each with its program
	errorInsufficientBuffer        = 122
	processQueryLimitedInformation = 0x1000
	tcpRowSize                     = 24 // MIB_TCPROW_OWNER_PID: state, local address, local port, remote address, remote port, pid
)

// peerProcess is the program holding the far end of a loopback connection: its pid and its file. errOwnerUnknown: the
// system's table can't be read. Any other error refuses the connection: it isn't in the table (it has closed), or the
// program can't be asked, as another Windows user's can't.
func peerProcess(c net.Conn) (uint32, string, error) {
	local, ok1 := c.LocalAddr().(*net.TCPAddr)
	remote, ok2 := c.RemoteAddr().(*net.TCPAddr)
	if !ok1 || !ok2 {
		return 0, "", fmt.Errorf("%w: not a TCP connection", errOwnerUnknown)
	}
	table, err := tcpTable()
	if err != nil {
		return 0, "", fmt.Errorf("%w: %v", errOwnerUnknown, err)
	}
	pid, found := ownerIn(table, remote, local)
	if !found {
		return 0, "", errors.New("the connection isn't in the system's table")
	}
	image, err := imageOf(pid)
	if err != nil {
		return pid, "", fmt.Errorf("program with pid %d: %v", pid, err)
	}
	return pid, image, nil
}

// tcpTable reads the system's IPv4 connections with their programs. A connection opened between the two calls can make the
// buffer too small, so it retries.
func tcpTable() ([]byte, error) {
	size := uint32(64 * 1024)
	for range 4 {
		buf := make([]byte, size)
		r, _, _ := procGetExtendedTCPTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPidConnections, 0)
		switch r {
		case 0:
			return buf, nil
		case errorInsufficientBuffer:
			size += 16 * 1024 // and what the call asked for: it set size
		default:
			return nil, fmt.Errorf("GetExtendedTcpTable: error %d", r)
		}
	}
	return nil, errors.New("GetExtendedTcpTable: the table keeps growing")
}

// ownerIn finds the row whose local end is local and whose remote end is remote. Rows hold addresses and ports in network
// byte order. The caller's own end of the connection has its own row, the other way round.
func ownerIn(table []byte, local, remote *net.TCPAddr) (uint32, bool) {
	if len(table) < 4 {
		return 0, false
	}
	n := int(binary.LittleEndian.Uint32(table))
	lip, rip := local.IP.To4(), remote.IP.To4()
	if lip == nil || rip == nil {
		return 0, false
	}
	for i := range n {
		off := 4 + i*tcpRowSize
		if off+tcpRowSize > len(table) {
			return 0, false
		}
		row := table[off : off+tcpRowSize]
		if !net.IP(row[4:8]).Equal(lip) || int(binary.BigEndian.Uint16(row[8:10])) != local.Port ||
			!net.IP(row[12:16]).Equal(rip) || int(binary.BigEndian.Uint16(row[16:18])) != remote.Port {
			continue
		}
		return binary.LittleEndian.Uint32(row[20:24]), true
	}
	return 0, false
}

// imageOf is a program's file.
func imageOf(pid uint32) (string, error) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	r, _, e := procQueryImageName.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return "", e
	}
	return syscall.UTF16ToString(buf[:size]), nil
}
