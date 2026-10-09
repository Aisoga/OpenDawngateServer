package gmshost

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// DefaultPorts is the GMS port pool of ADR-2 (UDP; dgserver --gms-ports).
const DefaultPorts = "10042-10051"

// ErrNoPort is returned when every port of the pool is in use (one per running match).
var ErrNoPort = errors.New("gmshost: no free GMS port")

// Pool hands out the GMS ports of a range, lowest free first. It doesn't bind them: the host binds
// its port (DGGMS_BIND) and reports it in gameReady.
type Pool struct {
	mu         sync.Mutex
	first, end int // [first, end)
	used       map[int]bool
}

// ParsePorts parses "a-b" or "a" (1-65535, a <= b).
func ParsePorts(s string) (first, last int, err error) {
	lo, hi, found := strings.Cut(strings.TrimSpace(s), "-")
	if first, err = strconv.Atoi(strings.TrimSpace(lo)); err != nil {
		return 0, 0, fmt.Errorf("gmshost: bad port range %q", s)
	}
	last = first
	if found {
		if last, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
			return 0, 0, fmt.Errorf("gmshost: bad port range %q", s)
		}
	}
	if first < 1 || last > 65535 || first > last {
		return 0, 0, fmt.Errorf("gmshost: bad port range %q", s)
	}
	return first, last, nil
}

// NewPool returns a pool of the ports first..last.
func NewPool(first, last int) *Pool {
	return &Pool{first: first, end: last + 1, used: map[int]bool{}}
}

// Acquire takes the lowest free port.
func (p *Pool) Acquire() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for port := p.first; port < p.end; port++ {
		if !p.used[port] {
			p.used[port] = true
			return port, nil
		}
	}
	return 0, ErrNoPort
}

// Release gives a port back; releasing a free or foreign port does nothing.
func (p *Pool) Release(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.used, port)
}

// Free returns the number of free ports.
func (p *Pool) Free() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.end - p.first - len(p.used)
}

// String returns the range as "a-b".
func (p *Pool) String() string { return fmt.Sprintf("%d-%d", p.first, p.end-1) }
