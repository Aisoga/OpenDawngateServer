// Package matchmaker is the server's PvP matchmaker (roadmap B10a): queues per mode and region, and
// team assembly for 1v1 up to 5v5. It knows nothing about Blaze or match hosts; the Blaze side
// (internal/blaze, matchmaking.go) turns a formed Match into a game and tells its players.
//
// A Ticket is one matchmaking session (Blaze's MSID): a solo player, or a premade group (the PvP lobby's
// playgroup) that must end up on one team. Tick, called by the owner every few hundred milliseconds and
// right after every Add, forms matches from the waiting tickets:
//
//   - The oldest ticket anchors a match. Candidates are the tickets of the same mode whose region is
//     compatible with the anchor's ("" = any region; the first ticket with a region fixes it).
//   - Teams are filled oldest ticket first, each ticket onto the team with fewer players that still
//     has room for the whole group (TeamSize per team); a ticket that fits nowhere waits.
//   - A match starts at once when both teams are full. With fewer players it starts once there are at
//     least MinPlayers and its fill deadline has passed (the empty slots are for bots: M8): FillAfter
//     after the anchor queued, and never less than FillExtend after the last ticket of the match joined
//     (MMWAIT, MMLAST: 90 s, and a join with less than 30 s left sets the rest to 30 s, so a 5v5 of solo
//     players waits at most 90 + 8 × 30 = 330 s; the tenth player fills the match).
//   - A ticket whose own Timeout has passed expires (Blaze: SESSION_TIMED_OUT).
//
// This is our own design: the original matchmaker ran on EA's Blaze server and isn't in the client. The
// client only supplies the inputs (its StartMatchmakingRequest; docs/BLAZE.md *PvP matchmaking (B10a)*).
package matchmaker

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MaxTeamSize is the largest team (5v5, the game's only PvP format: GameSize 10, TCAP 5).
const MaxTeamSize = 5

// Player is one human in a ticket.
type Player struct {
	ID   int64 // Blaze id (persona id)
	Name string
}

// Ticket is one matchmaking session: its players queue and play together on one team.
type Ticket struct {
	ID      int64  // the session id (MSID), unique
	Mode    string // queue: only tickets of the same mode meet
	Region  string // "" = any region
	Players []Player
	Timeout time.Duration // 0 = none
	Data    any           // the caller's own data (the Blaze side keeps connections and requests here)

	queued time.Time
}

// Queued is when the ticket entered the queue.
func (t *Ticket) Queued() time.Time { return t.queued }

// Match is a formed match: two teams of tickets. Team 0 is Blaze's team index 0 (TIDX 0, the host's
// team 1), team 1 the other.
type Match struct {
	Mode   string
	Region string // the match's region ("" if every ticket said any)
	Teams  [2][]*Ticket
	Full   bool // both teams full; otherwise it started on its fill deadline with open slots
}

// Size returns the number of players on team i.
func (m Match) Size(i int) int {
	n := 0
	for _, t := range m.Teams[i] {
		n += len(t.Players)
	}
	return n
}

// Tickets returns every ticket of the match, team 0 first, each team in queue order.
func (m Match) Tickets() []*Ticket {
	return append(append([]*Ticket{}, m.Teams[0]...), m.Teams[1]...)
}

// Config sets the matchmaker's rules.
type Config struct {
	TeamSize   int           // players per team, 1 (1v1) to MaxTeamSize (5v5)
	MinPlayers int           // fewest humans a match starts with after its fill deadline (1 = a lone player gets a game)
	FillAfter  time.Duration // how long the oldest ticket waits for full teams before a smaller match starts
	FillExtend time.Duration // MMWAIT, MMLAST: the least time a smaller match still waits after a ticket joins it
	// MaxWait caps every ticket's Timeout (0 = the ticket's own: the client asks for 3 h, DUR). B10b: the
	// client has no queue timer of its own, so this is the server's only limit on a wait.
	MaxWait time.Duration
	Now     func() time.Time
}

// Errors from Add.
var (
	ErrEmpty         = errors.New("matchmaker: ticket without players")
	ErrTooBig        = errors.New("matchmaker: group larger than a team")
	ErrDuplicateID   = errors.New("matchmaker: session id already queued")
	ErrAlreadyQueued = errors.New("matchmaker: player already queued")
)

// Matchmaker holds the queue. All methods are safe for concurrent use.
type Matchmaker struct {
	cfg Config

	mu    sync.Mutex
	queue []*Ticket // in queue order (oldest first)
}

// New returns a matchmaker. TeamSize is clamped to 1..MaxTeamSize, MinPlayers to 1..2*TeamSize.
func New(cfg Config) *Matchmaker {
	cfg.TeamSize = min(max(cfg.TeamSize, 1), MaxTeamSize)
	cfg.MinPlayers = min(max(cfg.MinPlayers, 1), 2*cfg.TeamSize)
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Matchmaker{cfg: cfg}
}

// Config returns the rules in effect (after clamping).
func (m *Matchmaker) Config() Config { return m.cfg }

func (c Config) String() string {
	s := fmt.Sprintf("%dv%d, start with %d+ players after %s", c.TeamSize, c.TeamSize, c.MinPlayers, c.FillAfter)
	if c.FillExtend > 0 {
		s += fmt.Sprintf(" (at least %s after the last session joins)", c.FillExtend)
	}
	if c.MaxWait > 0 {
		s += fmt.Sprintf(", wait at most %s", c.MaxWait)
	}
	return s
}

// Add queues t. A player may be in one ticket only, and a group must fit on one team. MaxWait caps
// t.Timeout.
func (m *Matchmaker) Add(t *Ticket) error {
	if len(t.Players) == 0 {
		return ErrEmpty
	}
	if len(t.Players) > m.cfg.TeamSize {
		return ErrTooBig
	}
	if m.cfg.MaxWait > 0 && (t.Timeout <= 0 || t.Timeout > m.cfg.MaxWait) {
		t.Timeout = m.cfg.MaxWait
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, q := range m.queue {
		if q.ID == t.ID {
			return ErrDuplicateID
		}
		for _, p := range q.Players {
			for _, o := range t.Players {
				if p.ID == o.ID {
					return ErrAlreadyQueued
				}
			}
		}
	}
	t.queued = m.cfg.Now()
	m.queue = append(m.queue, t)
	return nil
}

// Remove takes ticket id out of the queue (cancel, disconnect); false if it isn't queued (any more).
func (m *Matchmaker) Remove(id int64) (*Ticket, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, t := range m.queue {
		if t.ID == id {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			return t, true
		}
	}
	return nil, false
}

// Find returns the queued ticket that holds player id.
func (m *Matchmaker) Find(player int64) (*Ticket, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.queue {
		for _, p := range t.Players {
			if p.ID == player {
				return t, true
			}
		}
	}
	return nil, false
}

// Len returns the number of queued tickets.
func (m *Matchmaker) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue)
}

// Tick forms every match the queue allows now and removes its tickets, and removes the tickets whose
// timeout has passed. The caller starts the matches and tells the expired tickets' players.
func (m *Matchmaker) Tick() (matches []Match, expired []*Ticket) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.cfg.Now()
	keep := m.queue[:0]
	for _, t := range m.queue {
		if t.Timeout > 0 && now.Sub(t.queued) >= t.Timeout {
			expired = append(expired, t)
			continue
		}
		keep = append(keep, t)
	}
	m.queue = keep
	// Each ticket anchors at most once per tick, oldest first; a formed match takes its tickets out.
	for i := 0; i < len(m.queue); {
		mt, ok := m.form(m.queue[i], now)
		if !ok {
			i++
			continue
		}
		matches = append(matches, mt)
		taken := map[*Ticket]bool{}
		for _, t := range mt.Tickets() {
			taken[t] = true
		}
		keep := m.queue[:0]
		for _, t := range m.queue {
			if !taken[t] {
				keep = append(keep, t)
			}
		}
		m.queue = keep
		// m.queue[i] is now the next ticket (the anchor itself was taken)
	}
	return matches, expired
}

// form builds the match anchored at a, if one may start now. The anchor goes to team 0; the other
// candidates follow in queue order.
func (m *Matchmaker) form(a *Ticket, now time.Time) (Match, bool) {
	mt := m.assemble(a)
	if !mt.Full && (mt.Size(0)+mt.Size(1) < m.cfg.MinPlayers || now.Before(m.fillDeadline(mt))) {
		return Match{}, false
	}
	for i := range mt.Teams { // each team in queue order (an older ticket may follow the anchor)
		sort.SliceStable(mt.Teams[i], func(x, y int) bool { return mt.Teams[i][x].queued.Before(mt.Teams[i][y].queued) })
	}
	return mt, true
}

// fillDeadline is when mt, if not full, may start with open slots: FillAfter after its oldest ticket
// queued, and at least FillExtend after its last ticket joined (MMLAST, the user's rule of 2026-10-25:
// the time left never adds up per join, a join only lifts it to FillExtend if less was left). The oldest
// ticket, not the anchor: a younger anchor must not start the match before the older ticket's deadline.
func (m *Matchmaker) fillDeadline(mt Match) time.Time {
	joined := mt.Tickets()
	sort.SliceStable(joined, func(x, y int) bool { return joined[x].queued.Before(joined[y].queued) })
	at := joined[0].queued.Add(m.cfg.FillAfter)
	if m.cfg.FillExtend <= 0 || len(joined) == 1 {
		return at
	}
	if last := joined[len(joined)-1].queued.Add(m.cfg.FillExtend); last.After(at) {
		at = last
	}
	return at
}

// assemble puts the candidates for the match anchored at a onto the two teams.
func (m *Matchmaker) assemble(a *Ticket) Match {
	size := m.cfg.TeamSize
	mt := Match{Mode: a.Mode, Region: a.Region}
	mt.Teams[0] = []*Ticket{a}
	count := [2]int{len(a.Players), 0}
	for _, t := range m.queue {
		if count[0] == size && count[1] == size {
			break
		}
		if t == a || t.Mode != a.Mode || !compatible(mt.Region, t.Region) {
			continue
		}
		n := len(t.Players)
		team := 0
		if count[1] < count[0] {
			team = 1
		}
		if count[team]+n > size {
			team = 1 - team
			if count[team]+n > size {
				continue
			}
		}
		if mt.Region == "" {
			mt.Region = t.Region
		}
		mt.Teams[team] = append(mt.Teams[team], t)
		count[team] += n
	}
	mt.Full = count[0]+count[1] == 2*size
	return mt
}

// compatible reports whether a ticket of region r can join a match of region want ("" = any).
func compatible(want, r string) bool { return want == "" || r == "" || want == r }

// QueueState is what one mode's queue looks like now (Snapshot): for the Discord queue message (DISCORD2b).
type QueueState struct {
	Mode     string
	Tickets  []TicketState // in queue order (oldest first)
	Players  int           // humans in Tickets (a party counts each member)
	Capacity int           // seats of one match (2 × TeamSize)
	// StartsAt is the earliest time a match of this mode starts by the rules above: now when one is full already
	// (Tick forms it at once), else the earliest fill deadline (FillAfter after its oldest ticket, at least
	// FillExtend after its last one) among the matches the queued tickets would anchor that have MinPlayers. Zero: none has
	// MinPlayers yet (or the queue is empty), so no start time is known.
	StartsAt time.Time
}

// TicketState is one queued ticket in a QueueState.
type TicketState struct {
	ID      int64
	Players []Player // the session's owner (a party's leader) first
	Queued  time.Time
}

// Snapshot returns mode's queue as it is now.
func (m *Matchmaker) Snapshot(mode string) QueueState {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.cfg.Now()
	st := QueueState{Mode: mode, Capacity: 2 * m.cfg.TeamSize}
	for _, t := range m.queue {
		if t.Mode != mode {
			continue
		}
		st.Tickets = append(st.Tickets, TicketState{ID: t.ID, Players: append([]Player(nil), t.Players...), Queued: t.queued})
		st.Players += len(t.Players)
		mt := m.assemble(t)
		at := now
		switch {
		case mt.Full:
		case mt.Size(0)+mt.Size(1) < m.cfg.MinPlayers:
			continue
		default:
			at = m.fillDeadline(mt)
		}
		if st.StartsAt.IsZero() || at.Before(st.StartsAt) {
			st.StartsAt = at
		}
	}
	return st
}

// NextDeadline returns when Tick next has something to do without a new ticket: the earliest fill
// deadline of a match a queued ticket would anchor, or end of a timeout (zero if the queue is empty).
func (m *Matchmaker) NextDeadline() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	var next time.Time
	earlier := func(at time.Time) {
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	for _, t := range m.queue {
		earlier(m.fillDeadline(m.assemble(t)))
		if t.Timeout > 0 {
			earlier(t.queued.Add(t.Timeout))
		}
	}
	return next
}
