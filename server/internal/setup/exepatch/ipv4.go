package exepatch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// ipv4 is an IPv4 address as Python's ipaddress.IPv4Address parses it (patch_client.py's rules come from there).
type ipv4 [4]byte

func (a ipv4) String() string { return fmt.Sprintf("%d.%d.%d.%d", a[0], a[1], a[2], a[3]) }

// parseIPv4 accepts what ipaddress.IPv4Address(str) accepts: four decimal octets of 1-3 ASCII digits, no leading
// zero, each at most 255.
func parseIPv4(s string) (ipv4, error) {
	var a ipv4
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return a, fmt.Errorf("expected 4 octets in %q", s)
	}
	for i, p := range parts {
		if p == "" || len(p) > 3 {
			return a, fmt.Errorf("bad octet %q in %q", p, s)
		}
		v := 0
		for _, c := range []byte(p) {
			if c < '0' || c > '9' {
				return a, fmt.Errorf("bad octet %q in %q", p, s)
			}
			v = v*10 + int(c-'0')
		}
		if len(p) > 1 && p[0] == '0' {
			return a, fmt.Errorf("leading zeros are not permitted in %q", p)
		}
		if v > 255 {
			return a, fmt.Errorf("octet %d (> 255) in %q", v, s)
		}
		a[i] = byte(v)
	}
	return a, nil
}

func (a ipv4) in(prefix ipv4, bits int) bool {
	v := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	p := uint32(prefix[0])<<24 | uint32(prefix[1])<<16 | uint32(prefix[2])<<8 | uint32(prefix[3])
	if bits == 0 {
		return true
	}
	mask := ^uint32(0) << (32 - bits)
	return v&mask == p&mask
}

func isLoopback(a ipv4) bool    { return a.in(ipv4{127, 0, 0, 0}, 8) }
func isUnspecified(a ipv4) bool { return a == ipv4{} }
func isMulticast(a ipv4) bool   { return a.in(ipv4{224, 0, 0, 0}, 4) }
func isReserved(a ipv4) bool    { return a.in(ipv4{240, 0, 0, 0}, 4) }

// Python 3.12.4+'s IPv4Address._private_networks and their exceptions (.venv runs 3.12.7).
var privateNets = []struct {
	p    ipv4
	bits int
}{
	{ipv4{0, 0, 0, 0}, 8}, {ipv4{10, 0, 0, 0}, 8}, {ipv4{127, 0, 0, 0}, 8}, {ipv4{169, 254, 0, 0}, 16},
	{ipv4{172, 16, 0, 0}, 12}, {ipv4{192, 0, 0, 0}, 24}, {ipv4{192, 0, 0, 170}, 31}, {ipv4{192, 0, 2, 0}, 24},
	{ipv4{192, 168, 0, 0}, 16}, {ipv4{198, 18, 0, 0}, 15}, {ipv4{198, 51, 100, 0}, 24}, {ipv4{203, 0, 113, 0}, 24},
	{ipv4{240, 0, 0, 0}, 4}, {ipv4{255, 255, 255, 255}, 32},
}

var privateExceptions = []ipv4{{192, 0, 0, 9}, {192, 0, 0, 10}}

func isPrivate(a ipv4) bool {
	for _, x := range privateExceptions {
		if a == x {
			return false
		}
	}
	for _, n := range privateNets {
		if a.in(n.p, n.bits) {
			return true
		}
	}
	return false
}

// isGlobal: not in the shared address space 100.64.0.0/10 and not private (Python 3.12.4+).
func isGlobal(a ipv4) bool { return !a.in(ipv4{100, 64, 0, 0}, 10) && !isPrivate(a) }

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
