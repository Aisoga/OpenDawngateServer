package launcher

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/logging"
	"dawngate/server/internal/tunnel"
)

// The flood test's addresses: the front, the main server's gateway, and a "match gateway" that takes TCP connections
// and never answers (a victim's address: each connect hangs there until it is cancelled or times out).
const (
	floodFront = "127.0.9.150"
	floodMain  = "127.0.9.151"
	floodSink  = "127.0.9.152"
)

// sink counts the TCP connections it holds open, and the most it held at once.
type sink struct {
	ln       net.Listener
	mu       sync.Mutex
	open, hi int
	total    int
}

func newSink(t *testing.T) *sink {
	t.Helper()
	ln, err := net.Listen("tcp4", floodSink+":0")
	if err != nil {
		t.Fatal(err)
	}
	k := &sink{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			k.mu.Lock()
			k.open++
			k.total++
			k.hi = max(k.hi, k.open)
			k.mu.Unlock()
			go func() {
				io.Copy(io.Discard, c) // until the client closes it
				c.Close()
				k.mu.Lock()
				k.open--
				k.mu.Unlock()
			}()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return k
}

func (k *sink) counts() (open, hi, total int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.open, k.hi, k.total
}

func (k *sink) addr() netip.AddrPort { return netip.MustParseAddrPort(k.ln.Addr().String()) }

var droppedRE = regexp.MustCompile(`away: (\d+) away messages of the server dropped \(too fast\)`)

// TestFrontAwayFlood: a hostile main server's away messages cost the player's PC (and the address it names) only a
// bounded number of connects: a message for the same port cancels the earlier connect (one in flight per port), the
// tickets for a connecting session stop at tunnel.MaxTicketUsers, and a flood of messages is dropped past the rate,
// counted in one line instead of one line each.
func TestFrontAwayFlood(t *testing.T) {
	key := make([]byte, 32)
	nonce := uint16(0)
	ticket := func(match int64) []byte {
		nonce++
		tk := tunnel.Ticket{Machine: "na1", Match: match, User: 2000000001, Issued: time.Now()}
		tk.Nonce[0], tk.Nonce[1] = byte(nonce), byte(nonce>>8)
		b, err := tunnel.MintTicket(key, tk)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	id, _, err := tunnel.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := &tunnel.Gateway{Log: logging.New(io.Discard), Identity: id, BindIP: net.ParseIP(floodMain)}
	main.ForwardUDP(awayMatch, awayOther)
	if err := main.Start(net.JoinHostPort(floodMain, "0")); err != nil {
		t.Fatal(err)
	}
	defer main.Close()
	victim := newSink(t)

	logBuf := &syncBuffer{}
	ip := net.ParseIP(floodFront).To4()
	f := &Front{Log: logging.New(logBuf), TunnelIP: ip, Ports: DefaultFrontPorts, done: make(chan struct{}),
		tcpLns: map[int]net.Listener{}, udpLns: map[int]*net.UDPConn{}, conns: map[net.Conn]struct{}{},
		ConnectTimeout: 30 * time.Second, AwayIdle: time.Minute}
	s := &frontSession{b: Bookmark{Tag: "main", Address: floodMain}}
	s.c = &tunnel.Client{Log: f.Log, Gateway: main.Addr(), Pin: main.Identity.Pin, TunnelIP: ip, External: true,
		DialTimeout: 3 * time.Second}
	if err := s.c.Start(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.cur = s
	f.mu.Unlock()
	closed := false
	defer func() {
		if !closed {
			f.Close()
		}
	}()
	mainIP := s.c.GatewayIP()
	away := func(port int, match int64) {
		f.takeAway(s, mainIP, port, tunnel.Away{Gateway: victim.addr(), Ticket: ticket(match)})
	}
	refill := func() {
		s.awayMu.Lock()
		s.awayRate = tokenBucket{}
		s.awayMu.Unlock()
	}
	waitFor := func(what string, ok func(open, total int) bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			if open, _, total := victim.counts(); ok(open, total) {
				return
			}
			if time.Now().After(deadline) {
				open, hi, total := victim.counts()
				t.Fatalf("connects at the victim (want %s): %d open, %d at most, %d in all; log:\n%s", what, open, hi, total, logBuf.String())
			}
		}
	}
	waitOpen := func(want int) {
		t.Helper()
		waitFor(fmt.Sprintf("%d open", want), func(open, _ int) bool { return open == want })
	}

	// the same port with a new match each time, each after the one before reached the victim: each message cancels
	// the connect before it, and its own waits for that one to end
	for m := 1; m <= 10; m++ {
		away(awayMatch, int64(m))
		waitFor(fmt.Sprintf("%d in all", m), func(_, total int) bool { return total == m })
	}
	waitOpen(1)
	if _, hi, total := victim.counts(); hi != 1 || total != 10 {
		t.Errorf("replacing a connecting session: %d connects at once (want 1), %d in all (want 10)", hi, total)
	}

	// the same match while it connects: the tickets stop at MaxTicketUsers, none dialed meanwhile
	refill()
	for range 3 * tunnel.MaxTicketUsers {
		away(awayMatch, 10)
	}
	s.awayMu.Lock()
	as := s.away[awayMatch]
	tickets, queued := as.tickets, len(as.joins)
	s.awayMu.Unlock()
	if tickets != tunnel.MaxTicketUsers || queued != tunnel.MaxTicketUsers-1 {
		t.Errorf("a connecting session holds %d tickets, %d queued (want %d, %d)", tickets, queued, tunnel.MaxTicketUsers,
			tunnel.MaxTicketUsers-1)
	}
	if !strings.Contains(logBuf.String(), "refused: the session to that match has 8 tickets already (at most 8)") {
		t.Errorf("no refusal of the ninth ticket:\n%s", logBuf.String())
	}
	if _, hi, _ := victim.counts(); hi != 1 {
		t.Errorf("%d connects at once while tickets queued", hi)
	}

	// a flood: past the burst every message is dropped unlogged, then counted in one line
	refill()
	before := len(logBuf.String())
	for range 1000 {
		f.takeAway(s, mainIP, 23999, tunnel.Away{Gateway: victim.addr(), Ticket: ticket(11)}) // a port not listed
	}
	text := logBuf.String()[before:]
	if lines := strings.Count(text, "\n"); lines > awayBurst+3 { // the burst, the flood's line, a refill or two
		t.Errorf("1000 away messages wrote %d log lines (at most %d)", lines, awayBurst+3)
	}
	if strings.Count(text, "come faster than 2 a second; dropping them") != 1 {
		t.Errorf("the flood's line, once:\n%s", text)
	}
	time.Sleep(600 * time.Millisecond) // a token again
	away(23999, 11)
	if all := droppedRE.FindAllStringSubmatch(logBuf.String(), -1); len(all) == 0 {
		t.Errorf("no line with the dropped messages' count:\n%s", logBuf.String())
	} else if n, _ := strconv.Atoi(all[len(all)-1][1]); n < 900 {
		t.Errorf("%d away messages dropped, want about 984", n)
	}

	// Close ends the connect at once (not after ConnectTimeout)
	start := time.Now()
	f.Close()
	closed = true
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Close took %s with an away session connecting", d)
	}
	waitOpen(0)
}
