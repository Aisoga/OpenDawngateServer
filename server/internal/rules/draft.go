package rules

import (
	"errors"
	"fmt"
	"math/rand/v2"
)

// The draft (RULES5b; the user's decision 8 of the match rules design): before shaper select, the teams ban and pick
// shapers on the lobby's board, turn by turn, and each human then plays the shaper it drafted.
//
//   - Bans first: DraftBansPerTeam (two) per team, alternating team by team (team 1, team 2, ...), each by the team's first player
//     (its first seat). Everyone sees them.
//   - Then the picks, one per turn, by team turns 1-2-2-1-1-2-2-1-1-2 (team 1 = Mortal); a team's k-th pick is its k-th
//     player's (seat order).
//   - A shaper is banned or picked once in the whole game: it must be in the pool, not banned and not picked by either
//     team (so a draft needs ten picks and four bans in the pool: PoolForDraft).
//   - A team with fewer humans skips its turns beyond its size (a team without humans bans nothing either); the turns
//     that would never be played aren't planned at all, so nobody waits for them.
//   - A turn whose time runs out (the server's timer; Timeout): a ban is skipped, a pick takes a random free shaper of
//     the pool.
//
// Draft is the board's state only, without clocks or players: the server (package blaze) maps seats to its players, runs
// the turn timer and tells the lobby. Teams are 0 (team 1, Mortal) and 1 (team 2), as Blaze's team indexes.

// The draft's size.
const (
	DraftBansPerTeam  = 2 // the user's decision 8 (three until 2026-11-10)
	DraftPicksPerTeam = 5
)

// draftPickTeams is the pick order's team of each of the ten pick turns: 1-2-2-1-1-2-2-1-1-2.
var draftPickTeams = [2 * DraftPicksPerTeam]int{0, 1, 1, 0, 0, 1, 1, 0, 0, 1}

// The kinds of a turn.
const (
	DraftBan  = "ban"
	DraftPick = "pick"
)

// How a turn ended.
const (
	DraftChosen  = "chosen"  // the player on turn chose its shaper
	DraftTimeout = "timeout" // its time ran out: a ban skipped (no shaper), a pick a random free shaper
)

// DraftTurn is one turn of the board.
type DraftTurn struct {
	Kind   string // DraftBan or DraftPick
	Team   int    // 0 (team 1) or 1 (team 2)
	Seat   int    // the team's seat on turn (a ban: 0, the team's first player)
	Shaper string // the result, a PlayerClass; "" while the turn is to come or on, and for a skipped ban
	How    string // "" while the turn is to come or on, else DraftChosen or DraftTimeout
}

// Draft is a draft's board.
type Draft struct {
	Pool  []string    // the shapers to draft from (PlayerClasses, in the server's order)
	Turns []DraftTurn // every planned turn, in order
	Cur   int         // the current turn's index; len(Turns) once the draft is done
	used  map[string]string
}

// The reasons a player's ban or pick is refused (wrapped with the details).
var (
	ErrDraftOver     = errors.New("the draft is over")
	ErrNotYourTurn   = errors.New("not this player's turn")
	ErrWrongKind     = errors.New("the turn is another kind")
	ErrNotInPool     = errors.New("not a shaper of the draft's pool")
	ErrShaperBanned  = errors.New("the shaper is banned")
	ErrShaperPicked  = errors.New("the shaper is picked")
	errDraftBadSeats = errors.New("a team's humans must be 0 to 5")
)

// NewDraft plans a draft over pool for teams of seats[0] and seats[1] humans: the bans of each team with a human, then
// the picks of the seats that exist.
func NewDraft(pool []string, seats [2]int) (*Draft, error) {
	for _, n := range seats {
		if n < 0 || n > DraftPicksPerTeam {
			return nil, errDraftBadSeats
		}
	}
	d := &Draft{Pool: append([]string(nil), pool...), used: map[string]string{}}
	for i := 0; i < 2*DraftBansPerTeam; i++ {
		if team := i % 2; seats[team] > 0 {
			d.Turns = append(d.Turns, DraftTurn{Kind: DraftBan, Team: team})
		}
	}
	var next [2]int
	for _, team := range draftPickTeams {
		seat := next[team]
		next[team]++
		if seat < seats[team] {
			d.Turns = append(d.Turns, DraftTurn{Kind: DraftPick, Team: team, Seat: seat})
		}
	}
	return d, nil
}

// Done reports whether every turn is over.
func (d *Draft) Done() bool { return d.Cur >= len(d.Turns) }

// Current is the turn on now (false once the draft is done).
func (d *Draft) Current() (DraftTurn, bool) {
	if d.Done() {
		return DraftTurn{}, false
	}
	return d.Turns[d.Cur], true
}

// Phase is the board's phase: DraftBan or DraftPick while a turn of that kind is on, "done" after the last.
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

// Act is the player at (team, seat)'s ban or pick (kind) of shaper. It must be that player's turn of that kind and the
// shaper free; then the turn ends and the next is on. The board is unchanged by a refusal.
func (d *Draft) Act(team, seat int, kind, shaper string) error {
	t, ok := d.Current()
	switch {
	case !ok:
		return ErrDraftOver
	case t.Team != team || t.Seat != seat:
		return ErrNotYourTurn
	case t.Kind != kind:
		return fmt.Errorf("%w (%s)", ErrWrongKind, t.Kind)
	}
	if err := d.Free(shaper); err != nil {
		return err
	}
	d.end(shaper, DraftChosen)
	return nil
}

// Timeout ends the current turn by its time: a ban is skipped, a pick takes a random free shaper of the pool (r: the
// server's generator; "" when none is free, never with the checks' pool size). It returns the turn as it ended (false:
// the draft was over).
func (d *Draft) Timeout(r *rand.Rand) (DraftTurn, bool) {
	t, ok := d.Current()
	if !ok {
		return DraftTurn{}, false
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
	d.end(shaper, DraftTimeout)
	return d.Turns[d.Cur-1], true
}

// end closes the current turn with shaper (how: DraftChosen or DraftTimeout).
func (d *Draft) end(shaper, how string) {
	t := &d.Turns[d.Cur]
	t.Shaper, t.How = shaper, how
	if shaper != "" {
		d.used[shaper] = t.Kind
	}
	d.Cur++
}

// Picks are the picks so far by team and seat ("" for a seat without one).
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
