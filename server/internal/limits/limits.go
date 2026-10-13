// Package limits holds dgserver's rate and count limits (docs/OPERATORS.md
// *Limits*): per client address the new and the open TCP connections of every listener together (redirector,
// Blaze, QoS, web, telemetry and the tunnel gateway), UDP datagrams and bytes (QoS, the gateway's
// channel), login attempts with a backoff per address and per account, Blaze requests per connection, and per user
// the games started and the matchmaking queues joined.
//
// A nil *Limiter is "limits off": every method allows everything, so the call sites need no switch. dgserver makes one
// when its mode (--limits auto|on|off) says so: auto = on for a public bind or announced address, off on loopback and
// LAN, so tests and home play are unchanged; on forces them (the lanes' flood test).
//
// The tunnel gateway uses the same limiter by each player's real address: Listener around its TCP
// listener (Admit before the TLS handshake, the release when the connection ends), AllowTunnelPacket for each channel
// datagram (rates of its own), TunnelSessions for its session cap.
package limits

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mode is --limits: auto (on for a public address), on, off.
type Mode int

const (
	Auto Mode = iota
	On
	Off
)

// ParseMode reads --limits.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return Auto, nil
	case "on", "1", "yes", "true":
		return On, nil
	case "off", "0", "no", "false":
		return Off, nil
	}
	return Auto, fmt.Errorf("--limits %q: want auto, on or off", s)
}

func (m Mode) String() string {
	switch m {
	case On:
		return "on"
	case Off:
		return "off"
	}
	return "auto"
}

// Active says whether limits apply: On, or Auto with any of the server's addresses (bind, announced) public.
// isPublic is exepatch.IsPublic (passed in, so this package imports nothing of the server's).
func (m Mode) Active(isPublic func(string) bool, addresses ...string) bool {
	switch m {
	case On:
		return true
	case Off:
		return false
	}
	for _, a := range addresses {
		if a != "" && isPublic(a) {
			return true
		}
	}
	return false
}

// Rate is a token bucket: PerSec tokens a second, at most Burst saved up.
type Rate struct {
	PerSec float64
	Burst  float64
}

func (r Rate) String() string {
	if r.PerSec >= 1 {
		return fmt.Sprintf("%g/s (burst %g)", r.PerSec, r.Burst)
	}
	return fmt.Sprintf("%g/min (burst %g)", r.PerSec*60, r.Burst)
}

// Backoff is a failure rule: Free failures in a row are free, then each further one locks out for Base, doubled per
// failure, at most Max; a success clears it, and so does Forget of quiet time.
type Backoff struct {
	Free   int
	Base   time.Duration
	Max    time.Duration
	Forget time.Duration // failures older than this (since the last one) are forgotten
}

// lock returns the lockout after n failures in a row.
func (b Backoff) lock(n int) time.Duration {
	if n <= b.Free || b.Base <= 0 {
		return 0
	}
	d := b.Base
	for i := b.Free + 1; i < n && d < b.Max; i++ {
		d *= 2
	}
	if b.Max > 0 && d > b.Max {
		d = b.Max
	}
	return d
}

// Config is every limit. Default() is the public profile's.
type Config struct {
	Accept      Rate // new TCP connections per address, all listeners together
	OpenPerAddr int  // open TCP connections per address, all listeners together
	Packets     Rate // UDP datagrams per address (QoS)
	Bytes       Rate // UDP bytes per address
	// TunnelPackets and TunnelBytes are the tunnel gateway's channel datagrams per real address: everything a
	// tunnelled player sends over UDP, its match traffic included, so more than the QoS port's rate
	TunnelPackets, TunnelBytes Rate
	Logins                     Rate // login attempts per address
	AccountFail                Backoff
	AddrFail                   Backoff
	Requests                   Rate          // Blaze requests per connection (a connection beyond the burst is slowed down)
	RequestHeld                time.Duration // ... and closed when every request has had to wait for this long
	Games                      Rate          // games a user starts (resetDedicatedServer: practice, custom)
	GamesAtOnce                int           // running hosted games a user is in
	Queues                     Rate          // matchmaking queues a user joins (startMatchmaking)
	// TunnelSessions caps the tunnel sessions one real address holds at the gateway (live ones and those in
	// their 60-s grace; 0 = no cap)
	TunnelSessions int
	MaxKeys        int // tracked addresses/users per table (idle ones are pruned first)
}

// Default is the public profile. A normal client (in a test run: a login and a practice match) used 3 TCP connections
// (redirector, Blaze, telemetry; plus short QoS and HTTP ones), at most 39 Blaze requests in one second (the menus'
// burst after the login), 22 QoS datagrams in its first 2 s, one login.
func Default() Config {
	return Config{
		Accept:      Rate{PerSec: 1, Burst: 30},
		OpenPerAddr: 24,
		Packets:     Rate{PerSec: 100, Burst: 400},
		Bytes:       Rate{PerSec: 256 << 10, Burst: 1 << 20},
		Logins:      Rate{PerSec: 1.0 / 6, Burst: 10},
		AccountFail: Backoff{Free: 5, Base: 30 * time.Second, Max: 15 * time.Minute, Forget: time.Hour},
		AddrFail:    Backoff{Free: 20, Base: time.Minute, Max: 30 * time.Minute, Forget: time.Hour},
		Requests:    Rate{PerSec: 20, Burst: 120},
		RequestHeld: 10 * time.Second,
		Games:       Rate{PerSec: 1.0 / 20, Burst: 4},
		GamesAtOnce: 2,
		Queues:      Rate{PerSec: 1.0 / 10, Burst: 6},
		// a client in a match sends at most about 35 datagrams and 280 bytes a second (a test run), its QoS
		// check at the start about 20 datagrams of up to 1,200 bytes: the match host's own per-address rule (400/s)
		// leaves room for a household's players behind one address [assumed]
		TunnelPackets: Rate{PerSec: 400, Burst: 1200},
		TunnelBytes:   Rate{PerSec: 256 << 10, Burst: 1 << 20},
		// a player holds one session; a restarted game makes a new one while the old waits out its grace. 16 leaves
		// room for a household or a LAN party behind one NAT address [assumed]
		TunnelSessions: 16,
		MaxKeys:        50000,
	}
}

// Admin is the admin page's login: the website keeps a Limiter of its own with
// this Config for it, always on (also on loopback and LAN, where the game's limits are off), and stricter than the
// game's login: 5 attempts at once per address, then one every 30 s; 3 failures in a row for a login name are free,
// the 4th locks it for 1 min, each further one twice as long up to 1 h; from an address 5 are free, then 5 min doubling
// to 24 h; failures are forgotten after a quiet day. Only its Logins, AccountFail and AddrFail are used.
func Admin() Config {
	return Config{
		Logins:      Rate{PerSec: 1.0 / 30, Burst: 5},
		AccountFail: Backoff{Free: 3, Base: time.Minute, Max: time.Hour, Forget: 24 * time.Hour},
		AddrFail:    Backoff{Free: 5, Base: 5 * time.Minute, Max: 24 * time.Hour, Forget: 24 * time.Hour},
		MaxKeys:     50000,
	}
}

// DescribeLogins is the login part of Describe (the admin login's line).
func (c Config) DescribeLogins() string {
	return fmt.Sprintf("%s logins per address; backoff after %d failures per login name (%s doubling to %s) and %d per "+
		"address (%s to %s)", c.Logins, c.AccountFail.Free, c.AccountFail.Base, c.AccountFail.Max, c.AddrFail.Free,
		c.AddrFail.Base, c.AddrFail.Max)
}

// Describe is the start-up line's text.
func (c Config) Describe() string {
	return fmt.Sprintf("per address %s new and %d open TCP connections, %s UDP datagrams and %s KB/s, %s logins; "+
		"login backoff after %d failures per account (%s doubling to %s) and %d per address (%s to %s); Blaze requests %s "+
		"per connection (closed after %s held back); per user %s games, %d at once, %s matchmaking queues; tunnel "+
		"per address %d sessions, %s datagrams and %s KB/s",
		c.Accept, c.OpenPerAddr, c.Packets, Rate{c.Bytes.PerSec / 1024, c.Bytes.Burst / 1024}, c.Logins,
		c.AccountFail.Free, c.AccountFail.Base, c.AccountFail.Max, c.AddrFail.Free, c.AddrFail.Base, c.AddrFail.Max,
		c.Requests, c.RequestHeld, c.Games, c.GamesAtOnce, c.Queues, c.TunnelSessions, c.TunnelPackets,
		Rate{c.TunnelBytes.PerSec / 1024, c.TunnelBytes.Burst / 1024})
}

type bucket struct {
	tokens float64
	last   time.Time
}

// fill brings b up to now (a new bucket starts full).
func (b *bucket) fill(r Rate, now time.Time) {
	if b.last.IsZero() {
		b.tokens, b.last = r.Burst, now
		return
	}
	if dt := now.Sub(b.last).Seconds(); dt > 0 {
		b.tokens += dt * r.PerSec
		if b.tokens > r.Burst {
			b.tokens = r.Burst
		}
		b.last = now
	}
}

// take takes n tokens if there are that many.
func (b *bucket) take(r Rate, now time.Time, n float64) bool {
	b.fill(r, now)
	if b.tokens < n {
		return false
	}
	b.tokens -= n
	return true
}

// full says whether b has been idle long enough to be forgotten (it would be full again).
func (b *bucket) full(r Rate, now time.Time) bool {
	return r.PerSec <= 0 || b.tokens+now.Sub(b.last).Seconds()*r.PerSec >= r.Burst
}

type failures struct {
	n    int
	last time.Time
}

// Kinds of refusal, for the counts and the log.
const (
	KindAccept   = "accept"     // a new connection over the address's rate
	KindOpen     = "open"       // a new connection over the address's open connections
	KindPacket   = "udp"        // a datagram over the address's rate
	KindTunnel   = "tunnel-udp" // a tunnel channel datagram over the real address's rate
	KindLogin    = "login"      // a login over the address's rate or in a backoff
	KindRequests = "requests"   // a Blaze connection closed for its request rate
	KindGame     = "game"       // a game start over the user's rate or running games
	KindQueue    = "queue"      // a matchmaking start over the user's rate
)

// Limiter applies a Config. Safe for concurrent use; a nil *Limiter allows everything.
type Limiter struct {
	cfg Config
	now func() time.Time

	mu        sync.Mutex
	accept    map[string]*bucket
	open      map[string]int
	packets   map[string]*bucket
	tpackets  map[string]*bucket // the gateway's channel datagrams
	tbytes    map[string]*bucket
	bytes     map[string]*bucket
	logins    map[string]*bucket
	acctFails map[string]*failures
	addrFails map[string]*failures
	games     map[int64]*bucket
	queues    map[int64]*bucket
	refused   map[string]int       // kind -> refusals since the last TakeReport
	who       map[string]struct{}  // kind+" "+key refused since the last TakeReport
	logged    map[string]time.Time // kind+" "+key -> when ShouldLog last said yes
	exempt    map[string]bool      // Exempt's addresses
	lastPrune time.Time
}

// New returns a Limiter with cfg; now is the clock (nil = time.Now).
func New(cfg Config, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{cfg: cfg, now: now, accept: map[string]*bucket{}, open: map[string]int{},
		packets: map[string]*bucket{}, bytes: map[string]*bucket{}, tpackets: map[string]*bucket{}, tbytes: map[string]*bucket{}, logins: map[string]*bucket{},
		acctFails: map[string]*failures{}, addrFails: map[string]*failures{}, games: map[int64]*bucket{},
		queues: map[int64]*bucket{}, refused: map[string]int{}, who: map[string]struct{}{}, logged: map[string]time.Time{},
		exempt: map[string]bool{}}
}

// Exempt takes addresses out of the per-address limits (connections, datagrams, the login rate and the address's
// login backoff; an account's backoff still applies). An earlier build exempted the server's own address as the tunnel gateway's
// source; dgserver exempts nothing (the gateway and Blaze key tunnelled players by their real address),
// so dgserver no longer calls it (kept for tests and a later list of trusted addresses).
func (l *Limiter) Exempt(addrs ...string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, a := range addrs {
		if a != "" {
			l.exempt[a] = true
		}
	}
}

// Config returns the limits in force.
func (l *Limiter) Config() Config { return l.cfg }

// HostOf is the key of a remote address: its IP without the port ("" for nil).
func HostOf(a net.Addr) string {
	if a == nil {
		return ""
	}
	switch v := a.(type) {
	case *net.TCPAddr:
		return v.IP.String()
	case *net.UDPAddr:
		return v.IP.String()
	}
	if h, _, err := net.SplitHostPort(a.String()); err == nil {
		return h
	}
	return a.String()
}

func getBucket[K comparable](m map[K]*bucket, k K) *bucket {
	b := m[k]
	if b == nil {
		b = &bucket{}
		m[k] = b
	}
	return b
}

// refuse counts a refusal (l.mu held).
func (l *Limiter) refuse(kind, key string) {
	l.refused[kind]++
	if len(l.who) < 10000 {
		l.who[kind+" "+key] = struct{}{}
	}
}

// prune forgets idle buckets and old failures once a minute, or when a table is over MaxKeys (l.mu held).
func (l *Limiter) prune(now time.Time) {
	over := l.cfg.MaxKeys > 0 && (len(l.accept) > l.cfg.MaxKeys || len(l.packets) > l.cfg.MaxKeys || len(l.tpackets) > l.cfg.MaxKeys ||
		len(l.logins) > l.cfg.MaxKeys || len(l.acctFails) > l.cfg.MaxKeys || len(l.addrFails) > l.cfg.MaxKeys)
	if !over && now.Sub(l.lastPrune) < time.Minute {
		return
	}
	l.lastPrune = now
	pruneBuckets(l.accept, l.cfg.Accept, now)
	pruneBuckets(l.packets, l.cfg.Packets, now)
	pruneBuckets(l.tpackets, l.cfg.TunnelPackets, now)
	pruneBuckets(l.tbytes, l.cfg.TunnelBytes, now)
	pruneBuckets(l.bytes, l.cfg.Bytes, now)
	pruneBuckets(l.logins, l.cfg.Logins, now)
	pruneBuckets(l.games, l.cfg.Games, now)
	pruneBuckets(l.queues, l.cfg.Queues, now)
	pruneFailures(l.acctFails, l.cfg.AccountFail, now)
	pruneFailures(l.addrFails, l.cfg.AddrFail, now)
	for k, t := range l.logged {
		if now.Sub(t) > time.Minute {
			delete(l.logged, k)
		}
	}
	// still over (a flood from many addresses): forget the fullest half rather than grow without bound
	if l.cfg.MaxKeys > 0 {
		capBuckets(l.accept, l.cfg.MaxKeys)
		capBuckets(l.packets, l.cfg.MaxKeys)
		capBuckets(l.tpackets, l.cfg.MaxKeys)
		capBuckets(l.tbytes, l.cfg.MaxKeys)
		capBuckets(l.bytes, l.cfg.MaxKeys)
		capBuckets(l.logins, l.cfg.MaxKeys)
	}
}

func pruneBuckets[K comparable](m map[K]*bucket, r Rate, now time.Time) {
	for k, b := range m {
		if b.full(r, now) {
			delete(m, k)
		}
	}
}

func capBuckets[K comparable](m map[K]*bucket, max int) {
	if len(m) <= max {
		return
	}
	type kv struct {
		k K
		t float64
	}
	all := make([]kv, 0, len(m))
	for k, b := range m {
		all = append(all, kv{k, b.tokens})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t > all[j].t })
	for _, e := range all[:len(all)-max/2] {
		delete(m, e.k)
	}
}

func pruneFailures(m map[string]*failures, b Backoff, now time.Time) {
	for k, f := range m {
		if b.Forget > 0 && now.Sub(f.last) > b.Forget {
			delete(m, k)
		}
	}
}

// Admit is a new TCP connection from addr (an IP, HostOf): ok, with release to call once when it closes; or refused
// with why. A nil Limiter admits everything (release is a no-op).
func (l *Limiter) Admit(addr string) (release func(), ok bool, why string) {
	if l == nil {
		return func() {}, true, ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.exempt[addr] {
		return func() {}, true, ""
	}
	now := l.now()
	l.prune(now)
	if l.cfg.OpenPerAddr > 0 && l.open[addr] >= l.cfg.OpenPerAddr {
		l.refuse(KindOpen, addr)
		return nil, false, fmt.Sprintf("%d connections open from %s", l.open[addr], addr)
	}
	if l.cfg.Accept.PerSec > 0 && !getBucket(l.accept, addr).take(l.cfg.Accept, now, 1) {
		l.refuse(KindAccept, addr)
		return nil, false, fmt.Sprintf("new connections from %s over %s", addr, l.cfg.Accept)
	}
	l.open[addr]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.open[addr] <= 1 {
				delete(l.open, addr)
			} else {
				l.open[addr]--
			}
		})
	}, true, ""
}

// AllowPacket is a UDP datagram of n bytes from addr (an IP) to the QoS port.
func (l *Limiter) AllowPacket(addr string, n int) bool {
	if l == nil {
		return true
	}
	return l.allowUDP(KindPacket, addr, n, l.packets, l.bytes, l.cfg.Packets, l.cfg.Bytes)
}

// AllowTunnelPacket is a tunnel channel datagram of n bytes from the real address addr (an IP), before the gateway
// looks at it: the channel carries all of a tunnelled player's UDP (QoS, the match), so it has rates of its
// own.
func (l *Limiter) AllowTunnelPacket(addr string, n int) bool {
	if l == nil {
		return true
	}
	return l.allowUDP(KindTunnel, addr, n, l.tpackets, l.tbytes, l.cfg.TunnelPackets, l.cfg.TunnelBytes)
}

func (l *Limiter) allowUDP(kind, addr string, n int, pk, bk map[string]*bucket, pr, br Rate) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.exempt[addr] {
		return true
	}
	now := l.now()
	l.prune(now)
	if pr.PerSec > 0 && !getBucket(pk, addr).take(pr, now, 1) {
		l.refuse(kind, addr)
		return false
	}
	if br.PerSec > 0 && !getBucket(bk, addr).take(br, now, float64(n)) {
		l.refuse(kind, addr)
		return false
	}
	return true
}

// AccountKey is the account key of a login's name (MAIL): trimmed and lower-cased, as login names compare.
func AccountKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Login is a login attempt from addr for account (AccountKey): 0 = go ahead (the caller reports the result with
// LoginDone), else how long the caller should have waited (the attempt is refused; refused attempts don't count as
// failures, so a waiting player isn't punished further).
func (l *Limiter) Login(addr, account string) (wait time.Duration, why string) {
	if l == nil {
		return 0, ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.prune(now)
	exempt := l.exempt[addr]
	if d := locked(l.addrFails[addr], l.cfg.AddrFail, now); d > 0 && !exempt {
		l.refuse(KindLogin, addr)
		return d, fmt.Sprintf("%d failed logins from %s in a row; locked for %s more", l.addrFails[addr].n, addr, round(d))
	}
	if d := locked(l.acctFails[account], l.cfg.AccountFail, now); d > 0 && account != "" {
		l.refuse(KindLogin, addr)
		return d, fmt.Sprintf("%d failed logins for %q in a row; locked for %s more", l.acctFails[account].n, account, round(d))
	}
	if l.cfg.Logins.PerSec > 0 && !exempt {
		b := getBucket(l.logins, addr)
		if !b.take(l.cfg.Logins, now, 1) {
			l.refuse(KindLogin, addr)
			d := time.Duration((1 - b.tokens) / l.cfg.Logins.PerSec * float64(time.Second))
			return d, fmt.Sprintf("logins from %s over %s", addr, l.cfg.Logins)
		}
	}
	return 0, ""
}

// locked is the time left of f's lockout.
func locked(f *failures, b Backoff, now time.Time) time.Duration {
	if f == nil {
		return 0
	}
	if b.Forget > 0 && now.Sub(f.last) > b.Forget {
		return 0
	}
	return f.last.Add(b.lock(f.n)).Sub(now)
}

func round(d time.Duration) time.Duration { return d.Round(time.Second) }

// LoginDone reports a login's result: a success clears the account's and the address's failures, a failure adds one
// to each.
func (l *Limiter) LoginDone(addr, account string, ok bool) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if ok {
		delete(l.addrFails, addr)
		delete(l.acctFails, account)
		return
	}
	if !l.exempt[addr] {
		addFailure(l.addrFails, addr, l.cfg.AddrFail, now)
	}
	if account != "" {
		addFailure(l.acctFails, account, l.cfg.AccountFail, now)
	}
}

func addFailure(m map[string]*failures, k string, b Backoff, now time.Time) {
	f := m[k]
	if f == nil || (b.Forget > 0 && now.Sub(f.last) > b.Forget) {
		f = &failures{}
		m[k] = f
	}
	f.n++
	f.last = now
}

// Gate is one connection's request rate (Blaze): Next before each request.
type Gate struct {
	l        *Limiter
	b        bucket
	held     bool      // the last request had to wait
	heldFrom time.Time // since when every request had to wait
}

// RequestGate returns a new connection's gate (nil Limiter: a gate that never waits).
func (l *Limiter) RequestGate() *Gate { return &Gate{l: l} }

// Next takes a request's token: wait > 0 = the request is to be handled that much later (the caller sleeps, so a
// client beyond its burst is slowed down to the rate by TCP's own back pressure); ok false = every request of the
// connection has had to wait for longer than RequestHeld (a flood, not a burst), and it should be closed.
func (g *Gate) Next() (wait time.Duration, ok bool) {
	if g == nil || g.l == nil || g.l.cfg.Requests.PerSec <= 0 {
		return 0, true
	}
	r := g.l.cfg.Requests
	now := g.l.now()
	g.b.fill(r, now)
	if g.b.tokens >= 1 { // a whole token waiting: the client has slowed down
		g.held = false
	}
	g.b.tokens--
	if g.b.tokens >= 0 {
		return 0, true
	}
	if !g.held {
		g.held, g.heldFrom = true, now
	}
	if g.l.cfg.RequestHeld > 0 && now.Sub(g.heldFrom) > g.l.cfg.RequestHeld {
		g.l.mu.Lock()
		g.l.refuse(KindRequests, "")
		g.l.mu.Unlock()
		return 0, false
	}
	return time.Duration(-g.b.tokens / r.PerSec * float64(time.Second)), true
}

// ErrGameRate and ErrGamesAtOnce are StartGame's refusals.
var (
	ErrGameRate    = errors.New("games started over the user's rate")
	ErrGamesAtOnce = errors.New("the user is in too many running games")
	ErrQueueRate   = errors.New("matchmaking queues joined over the user's rate")
)

// StartGame is a user starting a game (practice, a custom lobby) while it is in running games already.
func (l *Limiter) StartGame(user int64, running int) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.prune(now)
	key := fmt.Sprint(user)
	if l.cfg.GamesAtOnce > 0 && running >= l.cfg.GamesAtOnce {
		l.refuse(KindGame, key)
		return fmt.Errorf("%w (%d running, at most %d)", ErrGamesAtOnce, running, l.cfg.GamesAtOnce)
	}
	if l.cfg.Games.PerSec > 0 && !getBucket(l.games, user).take(l.cfg.Games, now, 1) {
		l.refuse(KindGame, key)
		return fmt.Errorf("%w (%s)", ErrGameRate, l.cfg.Games)
	}
	return nil
}

// JoinQueue is a user starting matchmaking.
func (l *Limiter) JoinQueue(user int64) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.prune(now)
	if l.cfg.Queues.PerSec > 0 && !getBucket(l.queues, user).take(l.cfg.Queues, now, 1) {
		l.refuse(KindQueue, fmt.Sprint(user))
		return fmt.Errorf("%w (%s)", ErrQueueRate, l.cfg.Queues)
	}
	return nil
}

// ShouldLog says whether a refusal of kind for key should get its own log line: the first one per key and kind a
// minute (a flood is summed up by TakeReport instead).
func (l *Limiter) ShouldLog(kind, key string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	k := kind + " " + key
	now := l.now()
	if t, ok := l.logged[k]; ok && now.Sub(t) < time.Minute {
		return false
	}
	l.logged[k] = now
	return true
}

// TakeReport returns the refusals since the last call as one line ("" = none): per kind the count and the number of
// addresses or users.
func (l *Limiter) TakeReport() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.refused) == 0 {
		return ""
	}
	keys := map[string]int{}
	for w := range l.who {
		keys[strings.SplitN(w, " ", 2)[0]]++
	}
	var parts []string
	for _, kind := range []string{KindAccept, KindOpen, KindPacket, KindTunnel, KindLogin, KindRequests, KindGame, KindQueue} {
		if n := l.refused[kind]; n > 0 {
			who := ""
			if k := keys[kind]; k > 0 && kind != KindRequests {
				noun := "addresses"
				if kind == KindGame || kind == KindQueue {
					noun = "users"
				}
				who = fmt.Sprintf(" (%d %s)", k, noun)
			}
			parts = append(parts, fmt.Sprintf("%s %d%s", kind, n, who))
		}
	}
	l.refused = map[string]int{}
	l.who = map[string]struct{}{}
	return "refused: " + strings.Join(parts, ", ")
}

// Open is the number of open connections from addr (tests, the status page).
func (l *Limiter) Open(addr string) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.open[addr]
}
