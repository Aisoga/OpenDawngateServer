package tunnel

import (
	"errors"
	"net"
	"testing"
)

// TestListenTCPAndUDPExcluded plays a machine whose excluded UDP ranges hold a block of 2,000 ports around the port
// TCP picks first (GOFLAKE2 (6): Windows hands out port-0 ports one after the other, through blocks of 100 and more,
// and 20 tries that followed TCP's picks all failed): ListenTCPAndUDP finds a port both take, 30 times in a row; a
// fixed port is tried once, with the bind's error.
func TestListenTCPAndUDPExcluded(t *testing.T) {
	excluded := errors.New("bind: an excluded port")
	block, tries, refused := 0, 0, 0
	listenUDP = func(network string, a *net.UDPAddr) (*net.UDPConn, error) {
		tries++
		if a.Port != 0 { // the system never picks a port excluded for the protocol it picks for
			if block == 0 {
				block = a.Port
			}
			if block < 0 || a.Port > block-1000 && a.Port < block+1000 {
				refused++
				return nil, excluded
			}
		}
		return net.ListenUDP(network, a)
	}
	t.Cleanup(func() { listenUDP = net.ListenUDP })
	lo := net.IPv4(127, 0, 0, 1)
	for i := range 30 {
		block, tries, refused = 0, 0, 0
		tln, udp, err := ListenTCPAndUDP(lo, 0)
		if err != nil {
			t.Fatalf("try %d: %v after %d UDP binds", i, err, tries)
		}
		tp, up := tln.Addr().(*net.TCPAddr).Port, udp.LocalAddr().(*net.UDPAddr).Port
		tln.Close()
		udp.Close()
		if tp != up || tp == 0 || refused == 0 {
			t.Errorf("try %d: TCP port %d, UDP port %d after %d UDP binds (%d refused)", i, tp, up, tries, refused)
		}
	}
	// a fixed port: once (every port excluded)
	probe, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: lo})
	if err != nil {
		t.Fatal(err)
	}
	fixed := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	block, tries = -1, 0
	if _, _, err := ListenTCPAndUDP(lo, fixed); !errors.Is(err, excluded) || tries != 1 {
		t.Errorf("a fixed excluded port: %v after %d UDP binds", err, tries)
	}
}

// TestListenTCPAndUDP: one port for both on a real machine; a fixed port another socket holds is refused.
func TestListenTCPAndUDP(t *testing.T) {
	lo := net.IPv4(127, 0, 0, 1)
	tln, udp, err := ListenTCPAndUDP(lo, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tln.Close()
	defer udp.Close()
	p := tln.Addr().(*net.TCPAddr).Port
	if udp.LocalAddr().(*net.UDPAddr).Port != p {
		t.Errorf("TCP %s, UDP %s", tln.Addr(), udp.LocalAddr())
	}
	if l2, u2, err := ListenTCPAndUDP(lo, p); err == nil {
		l2.Close()
		u2.Close()
		t.Errorf("port %d bound twice", p)
	}
}
