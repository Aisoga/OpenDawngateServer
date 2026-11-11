package rules

import (
	"errors"
	"fmt"
	"math/rand/v2"
)

// The draft (RULES5b; version 2 since DRAFT1, the user's decisions of 2026-11-11): before shaper select, the teams ban
// and pick shapers on the lobby's board, and each human then plays the shaper it drafted.
//
//   - One team starts (DraftOrder.First; DRAFTSHUF: the server flips a coin; team 1 = Mortal in the zero order): it has
//     the first ban and the first pick.
//   - Bans first: DraftBansPerTeam (two) per team, alternating team by team from the starting team, each by the team's
//     banning seat (DraftOrder.Banner: the seat of the team's top player in the lobby), one at a time. Everyone sees them.
//   - Then the picks by team turns 1-2-2-1-1-2-2-1-1-2 ("1" the starting team), played in windows 1-2-2-2-2-1: the two
//     turns of a double window run at the same time, each seat locks on its own, and the window ends when both have
//     locked (or at its timer). A team's k-th pick is its k-th seat's (DRAFTSHUF: the server shuffles the seats), unless
//     two teammates swapped their pick turns (SwapPicks) before either's window began.
//   - A shaper is banned or picked once in the whole game: it must be in the pool, not banned and not picked by either
//     team (so a draft needs ten picks and four bans in the pool: PoolForDraft).
//   - A team with fewer humans skips its turns beyond its size (a team without humans bans nothing either); the turns
//     that would never be played aren't planned at all, so nobody waits for them. A double window with one planned turn
//     is a single one.
//   - A window whose time runs out (the server's timer; Timeout): each open ban is skipped, each open pick takes a random
//     free shaper of the pool.
//   - After the last pick, two teammates may trade their drafted shapers (Trade).
//
// Draft is the board's state only, without clocks, players, roles or hovers: the server (package blaze) maps seats to
// its players, runs the phases' timers and tells the lobby. Teams are 0 (team 1, Mortal) and 1 (team 2), as Blaze's
// team indexes.

// The draft's size.
const (
	DraftBansPerTeam  = 2 // the user's decision 8 (three until 2026-11-10)
	DraftPicksPerTeam = 5
)

// draftPickTeams is the pick order's team of each of the ten pick turns: 1-2-2-1-1-2-2-1-1-2 (0 the starting team).
var draftPickTeams = [2 * DraftPicksPerTeam]int{0, 1, 1, 0, 0, 1, 1, 0, 0, 1}

// draftPickWindows is the window of each of the ten pick turns: 1-2-2-2-2-1 (DRAFT1: a double window's two turns run at
// the same time).
var draftPickWindows = [2 * DraftPicksPerTeam]int{0, 1, 1, 2, 2, 3, 3, 4, 4, 5}

// The kinds of a turn.
const (
	DraftBan  = "ban"
	DraftPick = "pick"
)

// How a turn ended.
const (
	DraftChosen  = "chosen"  // the player on turn chose its shaper
	DraftTimeout = "timeout" // its time ran out: a ban skipped (no shaper), a pick a random free shaper
	DraftHover   = "hover"   // DRAFT1b: its time ran out with the player's hovered shaper still free: banned or picked for it
)

// DraftRole is a role a drafter declares (DRAFT1): ID is the wire's, its index in DraftRoles the host's role number
// (gms/core/protocol/shaper_select.h: 0 Gladiator, 1 Tactician, 2 Hunter, 3 Predator).
type DraftRole struct {
	ID   string
	Name string
}

// DraftRoles are the roles a drafter may declare, in the host's order. The choice is free: duplicates are allowed and
// nothing is enforced (the user's decision of 2026-11-11).
var DraftRoles = []DraftRole{{"gladiator", "Gladiator"}, {"tactician", "Tactician"}, {"hunter", "Hunter"},
	{"predator", "Predator"}}

// IsDraftRole reports whether id is one of DraftRoles' ids.
func IsDraftRole(id string) bool {
	for _, r := range DraftRoles {
		if r.ID == id {
			return true
		}
	}
	return false
}

// DraftTurn is one turn of the board.
type DraftTurn struct {
	Kind   string // DraftBan or DraftPick
	Team   int    // 0 (team 1) or 1 (team 2)
	Seat   int    // the team's seat on turn (a ban: the team's DraftOrder.Banner)
	Shaper string // the result, a PlayerClass; "" while the turn is to come or on, and for a skipped ban (after a trade: the shaper the seat holds now)
	How    string // "" while the turn is to come or on, else DraftChosen, DraftHover or DraftTimeout
	Window int    // its window: the turns that run at the same time, numbered from 0 over the planned turns
}

// Draft is a draft's board.
type Draft struct {
	Pool  []string    // the shapers to draft from (PlayerClasses, in the server's order)
	Turns []DraftTurn // every planned turn, in order
	Cur   int         // the index of the current window's first turn; len(Turns) once the draft is done
	Last  int         // the index of the turn that ended last; -1 before the first
	used  map[string]string
}

// The reasons a player's call is refused (wrapped with the details).
var (
	ErrDraftOver      = errors.New("the draft is over")
	ErrNotYourTurn    = errors.New("not this player's turn")
	ErrWrongKind      = errors.New("the turn is another kind")
	ErrNotInPool      = errors.New("not a shaper of the draft's pool")
	ErrShaperBanned   = errors.New("the shaper is banned")
	ErrShaperPicked   = errors.New("the shaper is picked")
	ErrLockedIn       = errors.New("this player has locked in")
	ErrSameSeat       = errors.New("a player can't swap or trade with itself")
	ErrNoPickTurn     = errors.New("a seat without a pick turn")
	ErrAlreadyPicked  = errors.New("one of the two has picked")
	ErrWindowOn       = errors.New("one of the two is on turn")
	ErrDraftNotOver   = errors.New("the picks aren't over")
	ErrNothingToTrade = errors.New("one of the two has no shaper")
	errDraftBadSeats  = errors.New("a team's humans must be 0 to 5")
	errDraftBadOrder  = errors.New("the starting team must be 0 or 1, each banning seat one of its team's")
)

// DraftOrder is who starts and who bans (DRAFTSHUF). The zero value is the order before it: team 1 starts, each team's
// first seat bans.
type DraftOrder struct {
	First  int    // the starting team, 0 (team 1) or 1 (team 2): the first ban and the first pick
	Banner [2]int // each team's seat that bans
}

// NewDraft plans a draft over pool for teams of seats[0] and seats[1] humans in DraftOrder{} (team 1 starts, the first
// seats ban): NewDraftOrder's.
func NewDraft(pool []string, seats [2]int) (*Draft, error) {
	return NewDraftOrder(pool, seats, DraftOrder{})
}

// NewDraftOrder plans a draft over pool for teams of seats[0] and seats[1] humans in order o: the bans of each team
// with a human, alternating from o.First's, each by the team's o.Banner seat; then the picks of the seats that exist,
// in their windows (o.First's team has the first).
func NewDraftOrder(pool []string, seats [2]int, o DraftOrder) (*Draft, error) {
	for _, n := range seats {
		if n < 0 || n > DraftPicksPerTeam {
			return nil, errDraftBadSeats
		}
	}
	if o.First != 0 && o.First != 1 {
		return nil, errDraftBadOrder
	}
	for team, b := range o.Banner {
		if b < 0 || (seats[team] > 0 && b >= seats[team]) {
			return nil, errDraftBadOrder
		}
	}
	d := &Draft{Pool: append([]string(nil), pool...), Last: -1, used: map[string]string{}}
	window := 0
	for i := 0; i < 2*DraftBansPerTeam; i++ {
		if team := (i + o.First) % 2; seats[team] > 0 {
			d.Turns = append(d.Turns, DraftTurn{Kind: DraftBan, Team: team, Seat: o.Banner[team], Window: window})
			window++
		}
	}
	var next [2]int
	last := -1 // the last planned pick's window in draftPickWindows
	for i, t := range draftPickTeams {
		team := t ^ o.First
		seat := next[team]
		next[team]++
		if seat >= seats[team] {
			continue
		}
		if w := draftPickWindows[i]; w != last {
			if last >= 0 {
				window++
			}
			last = w
		}
		d.Turns = append(d.Turns, DraftTurn{Kind: DraftPick, Team: team, Seat: seat, Window: window})
	}
	return d, nil
}

// Done reports whether every turn is over.
func (d *Draft) Done() bool { return d.Cur >= len(d.Turns) }

// Current is the current window's first turn (false once the draft is done).
func (d *Draft) Current() (DraftTurn, bool) {
	if d.Done() {
		return DraftTurn{}, false
	}
	return d.Turns[d.Cur], true
}

// CurWindow is the current window's number; -1 once the draft is done.
func (d *Draft) CurWindow() int {
	if d.Done() {
		return -1
	}
	return d.Turns[d.Cur].Window
}

// Window is the indexes of the current window's turns (none once the draft is done).
func (d *Draft) Window() []int {
	var out []int
	for i := d.Cur; i < len(d.Turns) && d.Turns[i].Window == d.Turns[d.Cur].Window; i++ {
		out = append(out, i)
	}
	return out
}

// Open is the indexes of the current window's turns not yet ended.
func (d *Draft) Open() []int {
	var out []int
	for _, i := range d.Window() {
		if d.Turns[i].How == "" {
			out = append(out, i)
		}
	}
	return out
}

// OnTurn reports whether (team, seat) has an open turn in the current window.
func (d *Draft) OnTurn(team, seat int) bool {
	for _, i := range d.Open() {
		if d.Turns[i].Team == team && d.Turns[i].Seat == seat {
			return true
		}
	}
	return false
}

// PickTurn is the index of (team, seat)'s pick turn; -1 when it has none.
func (d *Draft) PickTurn(team, seat int) int {
	for i, t := range d.Turns {
		if t.Kind == DraftPick && t.Team == team && t.Seat == seat {
			return i
		}
	}
	return -1
}

// Phase is the board's phase: DraftBan or DraftPick while a window of that kind is on, "done" after the last.
func (d *Draft) Phase() string {
	if t, ok := d.Current(); ok {
		return t.Kind
	}
	return "done"
}

// inPool reports whether shaper is one of the pool's.
func (d *Draft) inPool(shaper string) bool {
	for _, s := range d.Pool {
		if s == shaper {
			return true
		}
	}
	return false
}

// Free reports why shaper can't be banned or picked now (nil: it can).
func (d *Draft) Free(shaper string) error {
	switch {
	case !d.inPool(shaper):
		return ErrNotInPool
	case d.used[shaper] == DraftBan:
		return ErrShaperBanned
	case d.used[shaper] == DraftPick:
		return ErrShaperPicked
	}
	return nil
}

// Act is the player at (team, seat)'s ban or pick (kind) of shaper. It must have an open turn of that kind in the
// current window and the shaper must be free; then its turn ends (Last), and the next window is on once every turn of
// this one has ended. The board is unchanged by a refusal.
func (d *Draft) Act(team, seat int, kind, shaper string) error {
	if d.Done() {
		return ErrDraftOver
	}
	i := -1
	for _, j := range d.Window() {
		if d.Turns[j].Team == team && d.Turns[j].Seat == seat {
			i = j
		}
	}
	switch {
	case i < 0:
		return ErrNotYourTurn
	case d.Turns[i].How != "":
		return ErrLockedIn
	case d.Turns[i].Kind != kind:
		return fmt.Errorf("%w (%s)", ErrWrongKind, d.Turns[i].Kind)
	}
	if err := d.Free(shaper); err != nil {
		return err
	}
	d.end(i, shaper, DraftChosen)
	return nil
}

// Timeout ends the current window by its time. hover (nil: none) gives each seat's hovered shaper, "" for none: an open
// turn whose seat hovers a shaper that is still free bans or picks it (DraftHover, DRAFT1b: the user's "the intention
// gets locked in"). Every other open ban is skipped and every other open pick takes a random free shaper of the pool
// (r: the server's generator; "" when none is free, never with the checks' pool size), both DraftTimeout. The open
// turns end in order, so when both seats of a double window hover the same shaper the first takes it and the second
// gets a random pick. It returns the indexes of the turns it ended, in order (none: the draft was over).
//
// The hover is resolved here rather than by the caller through Act: Act on a window's last open turn advances the
// window, and the caller's Timeout after it would end the next window's turns instead.
func (d *Draft) Timeout(r *rand.Rand, hover func(team, seat int) string) []int {
	open := d.Open()
	for _, i := range open {
		t := d.Turns[i]
		if hover != nil {
			if h := hover(t.Team, t.Seat); h != "" && d.Free(h) == nil {
				d.end(i, h, DraftHover)
				continue
			}
		}
		shaper := ""
		if t.Kind == DraftPick {
			var free []string
			for _, s := range d.Pool {
				if d.Free(s) == nil {
					free = append(free, s)
				}
			}
			if len(free) > 0 {
				shaper = free[r.IntN(len(free))]
			}
		}
		d.end(i, shaper, DraftTimeout)
	}
	return open
}

// end closes turn i with shaper (how: DraftChosen, DraftHover or DraftTimeout) and moves on when its window is complete.
func (d *Draft) end(i int, shaper, how string) {
	t := &d.Turns[i]
	t.Shaper, t.How = shaper, how
	if shaper != "" {
		d.used[shaper] = t.Kind
	}
	d.Last = i
	if len(d.Open()) == 0 {
		d.Cur += len(d.Window())
	}
}

// CheckSwap reports why the teammates a and b of team can't swap their pick turns now (nil: they can): both need a pick
// turn, neither may have picked, and neither's window may be on.
func (d *Draft) CheckSwap(team, a, b int) error {
	if a == b {
		return ErrSameSeat
	}
	ia, ib := d.PickTurn(team, a), d.PickTurn(team, b)
	switch {
	case ia < 0 || ib < 0:
		return ErrNoPickTurn
	case d.Turns[ia].How != "" || d.Turns[ib].How != "":
		return ErrAlreadyPicked
	case d.Turns[ia].Window == d.CurWindow() || d.Turns[ib].Window == d.CurWindow():
		return ErrWindowOn
	}
	return nil
}

// SwapPicks exchanges the pick turns of team's seats a and b (CheckSwap's conditions): each then picks in the other's
// turn. The bans stay the banning seat's.
func (d *Draft) SwapPicks(team, a, b int) error {
	if err := d.CheckSwap(team, a, b); err != nil {
		return err
	}
	ia, ib := d.PickTurn(team, a), d.PickTurn(team, b)
	d.Turns[ia].Seat, d.Turns[ib].Seat = b, a
	return nil
}

// CheckTrade reports why the teammates a and b of team can't trade their drafted shapers now (nil: they can): the
// picks must be over and both must hold a shaper.
func (d *Draft) CheckTrade(team, a, b int) error {
	if a == b {
		return ErrSameSeat
	}
	if !d.Done() {
		return ErrDraftNotOver
	}
	ia, ib := d.PickTurn(team, a), d.PickTurn(team, b)
	switch {
	case ia < 0 || ib < 0:
		return ErrNoPickTurn
	case d.Turns[ia].Shaper == "" || d.Turns[ib].Shaper == "":
		return ErrNothingToTrade
	}
	return nil
}

// Trade exchanges the drafted shapers of team's seats a and b (CheckTrade's conditions): each pick turn then holds the
// other's shaper (and how it was picked), so Picks gives the shapers after the trades.
func (d *Draft) Trade(team, a, b int) error {
	if err := d.CheckTrade(team, a, b); err != nil {
		return err
	}
	ia, ib := d.PickTurn(team, a), d.PickTurn(team, b)
	ta, tb := &d.Turns[ia], &d.Turns[ib]
	ta.Shaper, tb.Shaper = tb.Shaper, ta.Shaper
	ta.How, tb.How = tb.How, ta.How
	return nil
}

// Picks are the picks so far by team and seat ("" for a seat without one), after the trades.
func (d *Draft) Picks() [2][DraftPicksPerTeam]string {
	var out [2][DraftPicksPerTeam]string
	for _, t := range d.Turns {
		if t.Kind == DraftPick && t.Shaper != "" {
			out[t.Team][t.Seat] = t.Shaper
		}
	}
	return out
}

// Bans are the bans so far ("" skipped ones left out).
func (d *Draft) Bans() []string {
	var out []string
	for _, t := range d.Turns {
		if t.Kind == DraftBan && t.Shaper != "" {
			out = append(out, t.Shaper)
		}
	}
	return out
}
