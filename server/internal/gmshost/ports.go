package gmshost

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultPorts is the GMS port pool of ADR-2 (UDP; dgserver --gms-ports).
const DefaultPorts = "10042-10051"

// ErrNoPort is returned when every port of the pool is in use (one per running match) or in quarantine.
var ErrNoPort = errors.New("gmshost: no free GMS port")

// Pool hands out the GMS ports of a range, lowest free first. It doesn't bind them: the host binds
// its port (DGGMS_BIND) and reports it in gameReady.
//
// PORTSTUCK: a port whose host could not bind it is put in quarantine (Quarantine): it isn't handed out
// until the quarantine is over (Expire), so one stuck port (a socket left behind by an earlier host)
// costs one host start, not every game. Each new quarantine of the same port is twice as long as the one
// before (up to a cap) until a host gets ready on it again (Good).
type Pool struct {
	mu         sync.Mutex
	first, end int // [first, end)
	used       map[int]bool
	quarantine map[int]quarantined // not used, not handed out until the time
	strikes    map[int]int         // quarantines since the port's last good host
}

type quarantined struct {
	until time.Time
	d     time.Duration
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
	return &Pool{first: first, end: last + 1, used: map[int]bool{}, quarantine: map[int]quarantined{},
		strikes: map[int]int{}}
}

// Acquire takes the lowest port that is neither in use nor in quarantine. With none left the error is
// ErrNoPort, which says how many are in quarantine.
func (p *Pool) Acquire() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for port := p.first; port < p.end; port++ {
		if _, q := p.quarantine[port]; !p.used[port] && !q {
			p.used[port] = true
			return port, nil
		}
	}
	if n := len(p.quarantine); n > 0 {
		return 0, fmt.Errorf("%w (%d of %d in quarantine: their hosts could not bind them)", ErrNoPort, n, p.end-p.first)
	}
	return 0, ErrNoPort
}

// Release gives a port back; releasing a free or foreign port does nothing, and a port in quarantine
// stays there.
func (p *Pool) Release(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.used, port)
}

// Quarantine gives a port back but keeps it from being handed out: for base the first time, then twice
// as long each time it fails again before a host got ready on it (Good), at most max. It returns the
// length chosen. A foreign port is ignored (0).
func (p *Pool) Quarantine(port int, base, max time.Duration) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if port < p.first || port >= p.end {
		return 0
	}
	delete(p.used, port)
	d := base
	for i := p.strikes[port]; i > 0 && d < max; i-- {
		d *= 2
	}
	if d > max {
		d = max
	}
	p.strikes[port]++
	p.quarantine[port] = quarantined{until: time.Now().Add(d), d: d}
	return d
}

// Good says a host got ready on port: its next quarantine starts at the base length again.
func (p *Pool) Good(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.strikes, port)
}

// QuarantineEnd is a port whose quarantine is over (Expire).
type QuarantineEnd struct {
	Port  int
	After time.Duration // the quarantine's length
}

// Expire ends the quarantines that are over: their ports can be handed out again. Lowest port first.
func (p *Pool) Expire() []QuarantineEnd {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	var out []QuarantineEnd
	for port, q := range p.quarantine {
		if !now.Before(q.until) {
			delete(p.quarantine, port)
			out = append(out, QuarantineEnd{Port: port, After: q.d})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// InQuarantine returns the ports in quarantine, lowest first.
func (p *Pool) InQuarantine() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int, 0, len(p.quarantine))
	for port := range p.quarantine {
		out = append(out, port)
	}
	sort.Ints(out)
	return out
}

// Free returns the number of ports Acquire could hand out (neither in use nor in quarantine).
func (p *Pool) Free() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.end - p.first - len(p.used) - len(p.quarantine)
}

// String returns the range as "a-b".
func (p *Pool) String() string { return fmt.Sprintf("%d-%d", p.first, p.end-1) }

// probeUDP binds host:port (UDP, without SO_REUSEADDR) and closes it at once: an error means something
// holds the port, so a host started on it could not bind it either (PORTSTUCK; Config.ProbePorts). It
// misses a holder that lets others share the port (Windows lets a specific address bind beside another
// socket's wildcard one), which the quarantine after the host's exit code 2 still catches.
func probeUDP(host string, port int) error {
	c, err := net.ListenPacket("udp4", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return c.Close()
}
