package tunnel

import (
	"math/rand/v2"
	"net"
)

// listenUDP is net.ListenUDP; the tests replace it to play a machine's excluded port ranges.
var listenUDP = net.ListenUDP

// listenAttempts is how often ListenTCPAndUDP tries with port 0; dynamicLow and dynamicHigh are the range its random
// tries pick from (IANA's dynamic ports, Windows' default ephemeral range).
const (
	listenAttempts          = 40
	dynamicLow, dynamicHigh = 49152, 65535
)

// ListenTCPAndUDP binds TCP and UDP on one port of ip (nil: every address); a fixed port is tried once.
//
// With port 0 (tests) the system picks the port. Windows keeps a list of excluded port ranges per protocol (Hyper-V,
// WinNAT: blocks of 100 ports and more, on GitHub's runners as on a player's PC), and the two lists differ, so the
// number the system picks for one protocol may be excluded for the other (`bind: An attempt was made to access a
// socket in a way forbidden by its access permissions`). Windows hands out port-0 ports one after the other, so
// following its picks stays inside such a block (GOFLAKE2 (6): 20 tries all failed). The first try lets TCP pick,
// the second UDP (neither is ever given a port excluded for itself), and the rest take random ports of the dynamic
// range, which leave any block behind.
func ListenTCPAndUDP(ip net.IP, port int) (*net.TCPListener, *net.UDPConn, error) {
	var err error
	for attempt := range listenAttempts {
		var tln *net.TCPListener
		var udp *net.UDPConn
		switch {
		case port != 0 || attempt == 0: // the given port, or TCP picks
			if tln, err = net.ListenTCP("tcp4", &net.TCPAddr{IP: ip, Port: port}); err != nil {
				return nil, nil, err
			}
			if udp, err = listenUDP("udp4", &net.UDPAddr{IP: ip, Port: tln.Addr().(*net.TCPAddr).Port}); err == nil {
				return tln, udp, nil
			}
			tln.Close()
			if port != 0 {
				return nil, nil, err
			}
		case attempt == 1: // UDP picks
			if udp, err = listenUDP("udp4", &net.UDPAddr{IP: ip}); err != nil {
				return nil, nil, err
			}
			if tln, err = net.ListenTCP("tcp4", &net.TCPAddr{IP: ip, Port: udp.LocalAddr().(*net.UDPAddr).Port}); err == nil {
				return tln, udp, nil
			}
			udp.Close()
		default: // a random port (taken or excluded: the next try)
			p := dynamicLow + rand.IntN(dynamicHigh-dynamicLow+1)
			if tln, err = net.ListenTCP("tcp4", &net.TCPAddr{IP: ip, Port: p}); err != nil {
				continue
			}
			if udp, err = listenUDP("udp4", &net.UDPAddr{IP: ip, Port: p}); err == nil {
				return tln, udp, nil
			}
			tln.Close()
		}
	}
	return nil, nil, err
}
