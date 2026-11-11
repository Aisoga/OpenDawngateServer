package blaze

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/rules"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/uichannel"
)

// The draft (RULES5b; version 2 since DRAFT1; package rules has the board, rules.Draft): a custom game whose pick rule
// is "draft" drafts its shapers in the lobby before shaper select.
//
//   - START (the admin's advanceGameState IN_GAME, after the ready check and the rules' last check) holds the start: the
//     request is refused as the ready check's refused start is (the client stays in its lobby, no dialog), but the roster
//     stays locked (RosterLockedKey "1": the admin's START set it, and the server sets it too if it didn't), and the
//     draft begins. Each team's humans are its seats, in the lobby's seat order (lobbyRoster); spectators see the board
//     and never act. A second START during the draft is refused and changes nothing.
//   - The phases: role (Server.DraftRoleTime, 20 s, everyone at once: draft.role), then the board's bans (one at a time)
//     and picks (windows 1-2-2-2-2-1; a double window's two seats lock on their own), each window Server.DraftTurn
//     (45 s), then trade (up to Server.DraftTradeTime, 30 s: draft.trade, draft.ready; skipped when no team has two
//     drafters), then the start. A window whose time runs out: each open turn whose player hovers a shaper still free
//     bans or picks it (how "hover", DRAFT1b: the user's "the intention gets locked in"); every other open ban is
//     skipped, every other open pick takes a random free shaper of the pool (the server's generator, seeded from
//     crypto/rand).
//   - A role may be declared and changed until the end; a ban, a pick or a hover needs one ("Choose your role first.").
//     A drafter who has none when its turn times out gets the timeout's result (a skipped ban, a random pick) and no
//     role for shaper select.
//   - Team-private: the roles, the hovers (draft.hover), the pending pick-order swaps (draft.swap, draft.swapAnswer) and
//     trades (draft.trade, draft.tradeAnswer) and the ready marks go only to the drafter's own team (DraftState.Own);
//     the enemy and the spectators get none. A change of only those goes to the team alone.
//   - draft.state (to everyone in the lobby who entered, at every change and to a late lobby.enter) is the whole board as
//     the receiver may see it, so a board can be drawn from the last one alone (DraftState).
//   - A team player who leaves the game (its leave, a lost session, a kick) cancels the draft in any phase: the roster is
//     unlocked as after a refused start (RosterLockedKey "0", the admins' redraw and lobby.startRefused), every ready
//     mark is reset, and draft.state says "cancelled" with the reason. The lobby stays; the seat can be refilled.
//   - While the draft runs a newcomer may only spectate, a team change is refused and the rules can't change.
//   - After the trade phase the server starts the game itself (the --custom-autostart path: the host's startGame, then
//     NotifyGameStateChange IN_GAME to everyone), and each human's list in startGame holds only its drafted shaper after
//     the trades (applyPickRules); the host locks it at once, as All Random's (RULES3). Each human's role goes along
//     (draftedSeat.Role) for the role preset in shaper select.
//
// A client without the board can't draft: its turns time out (no ban, a random pick).

// The draft's UI channel calls and event.
const (
	CallDraftBan         = "draft.ban"
	CallDraftPick        = "draft.pick"
	CallDraftRole        = "draft.role"        // DRAFT1
	CallDraftHover       = "draft.hover"       // DRAFT1
	CallDraftSwap        = "draft.swap"        // DRAFT1
	CallDraftSwapAnswer  = "draft.swapAnswer"  // DRAFT1
	CallDraftTrade       = "draft.trade"       // DRAFT1
	CallDraftTradeAnswer = "draft.tradeAnswer" // DRAFT1
	CallDraftReady       = "draft.ready"       // DRAFT1
	EventDraftState      = "draft.state"
)

// The draft's times (the user's decisions: a turn 30 s until 2026-11-11, then +50 %; the role phase about 20 s; the
// trade phase up to 30 s).
const (
	DraftTurnDefault  = 45 * time.Second
	DraftRoleDefault  = 20 * time.Second
	DraftTradeDefault = 30 * time.Second
)

// The draft's phases on the wire besides the board's "ban" and "pick".
const (
	DraftPhaseRole      = "role"
	DraftPhaseTrade     = "trade"
	DraftPhaseDone      = "done"
	DraftPhaseCancelled = "cancelled"
)

// draftArgsMax is the longest draft call's arguments taken.
const draftArgsMax = 256

// errChooseRole refuses a ban, a pick or a hover of a drafter without a role (the user's decision).
var errChooseRole = errors.New("Choose your role first.")

// DraftShaper is a shaper of the draft's pool on the board.
type DraftShaper struct {
	ID        string `json:"id"`        // its PlayerClass ("PC_Artificer"): what the calls name
	Name      string `json:"name"`      // its title from the server's content ("Petrus"); the id without "PC_" without one
	CatalogID uint32 `json:"catalogID"` // its CollectionItem (the shaper item), 0 without content

	// Portrait and Card are its pictures as the content names them (content.Shaper; the board's slots and its grid);
	// absent without them (content made before the board). The board loads only paths of a shaper picture's form.
	Portrait string `json:"portrait,omitempty"`
	Card     string `json:"card,omitempty"`
}

// DraftStateTurn is one turn of the board.
type DraftStateTurn struct {
	Kind   string `json:"kind"`   // "ban" or "pick"
	Team   int    `json:"team"`   // 1 (Mortal) or 2
	Player string `json:"player"` // the player on turn (a ban: the team's first player; a pick: after the swaps)
	Shaper string `json:"shaper"` // the result: a pool id; "" while to come or on, and for a skipped ban; after a trade the shaper the player holds now
	How    string `json:"how"`    // "" while to come or on, "chosen", "hover" (timed out: the hovered shaper, DRAFT1b), or "timeout" (a skipped ban, a random pick)
	Window int    `json:"window"` // DRAFT1: its window, the turns played at the same time (from 0)
}

// DraftYou is the receiver's place in the draft.
type DraftYou struct {
	Team int `json:"team"` // 1 or 2
	Seat int `json:"seat"` // its place in Teams' list, from 0
}

// DraftAsk is a pending request between two seats of the receiver's team (a pick-order swap or a trade).
type DraftAsk struct {
	From int `json:"from"` // the asking seat
	To   int `json:"to"`   // the asked seat, who answers
}

// DraftOwn is the receiver's own team's private part of the board (DRAFT1): each list by seat of Teams[You.Team-1].
type DraftOwn struct {
	Roles  []string   `json:"roles"`  // each seat's role id ("gladiator", ...), "" while none
	Hovers []string   `json:"hovers"` // each seat's hovered shaper (a pool id), "" none
	Ready  []bool     `json:"ready"`  // each seat's ready mark (the trade phase)
	Swaps  []DraftAsk `json:"swaps"`  // the pending pick-order swaps
	Trades []DraftAsk `json:"trades"` // the pending trades
}

// DraftState is EventDraftState's data: the whole board as its receiver may see it.
type DraftState struct {
	Phase    string           `json:"phase"`            // "role", "ban", "pick", "trade", "done" or "cancelled"
	Reason   string           `json:"reason,omitempty"` // cancelled: why (text for the board's .text)
	Turn     int              `json:"turn"`             // the current window's first turn's index in Turns; -1 when none
	Window   int              `json:"window"`           // DRAFT1: the current window's number; -1 when none
	Left     int64            `json:"left"`             // ms left in the current step (role, window, trade) when sent; 0 when none
	TurnMs   int64            `json:"turnMs"`           // the current step's full time; a turn's when none
	Teams    [2][]string      `json:"teams"`            // each team's drafting players in seat order (team 1 first)
	Pool     []DraftShaper    `json:"pool"`             // the shapers to draft from
	Turns    []DraftStateTurn `json:"turns"`            // every turn of the draft, played or to come
	You      *DraftYou        `json:"you,omitempty"`    // the receiver's seat; absent for a spectator
	YourTurn bool             `json:"yourTurn"`         // the receiver's seat has an open turn in the current window
	Own      *DraftOwn        `json:"own,omitempty"`    // DRAFT1: the receiver's team's private part; absent for a spectator
}

// The stages of a running draft.
const (
	draftStageRole  = "role"
	draftStageBoard = "board"
	draftStageTrade = "trade"
)

// draftAsk is a pending swap or trade request.
type draftAsk struct{ from, to int64 }

// draftAskKey is a declined request, not asked again in the draft.
type draftAskKey struct {
	kind     string
	from, to int64
}

// draftedSeat is a human's result of the draft: its shaper after the trades and its role ("" none; for the role preset
// in shaper select, DRAFT3).
type draftedSeat struct {
	Shaper string
	Role   string
}

// draftRun is a lobby's draft (guarded by the hostedGame's mutex).
type draftRun struct {
	board     *rules.Draft
	seats     [2][]int64 // each team's personas in seat order
	names     map[int64]string
	pool      []DraftShaper
	turn      time.Duration
	roleTime  time.Duration
	tradeTime time.Duration
	stage     string // draftStageRole, draftStageBoard or draftStageTrade while it runs
	window    int    // the board's window whose timer runs; -1 none
	stepLen   time.Duration
	stepEnds  time.Time
	gen       int // the current step's number: a timer of an older step does nothing
	rng       *rand.Rand
	seed      uint64
	timer     *time.Timer
	aid       *time.Timer // Server.TestDraft's
	phase     string      // "" while it runs, "done" or "cancelled"
	reason    string

	roles    map[int64]string
	hovers   map[int64]string
	ready    map[int64]bool
	swaps    []draftAsk
	trades   []draftAsk
	declined map[draftAskKey]bool

	aidTried, aidSwapped, aidTraded bool // the test aid's one-off calls were made
}

// seatOf is user's (team, seat) in the draft; false for someone who doesn't draft.
func (d *draftRun) seatOf(user int64) (int, int, bool) {
	for team, ids := range d.seats {
		for seat, id := range ids {
			if id == user {
				return team, seat, true
			}
		}
	}
	return 0, 0, false
}

// hoverOf is the shaper the drafter at (team, seat) hovers, "" none (DRAFT1b: what a timed-out turn of that seat
// bans or picks for it, when still free; rules.Draft.Timeout checks that). Only the seat on turn is asked, so a
// teammate's, the enemy's or a spectator's hover (a spectator has none: its calls are refused) never steers a turn.
func (d *draftRun) hoverOf(team, seat int) string {
	if team < 0 || team > 1 || seat < 0 || seat >= len(d.seats[team]) {
		return ""
	}
	return d.hovers[d.seats[team][seat]]
}

// drafters are every drafter, team 1 first.
func (d *draftRun) drafters() []int64 {
	return append(append([]int64(nil), d.seats[0]...), d.seats[1]...)
}

// running reports whether the draft is still on.
func (d *draftRun) running() bool { return d.phase == "" }

// pendingOf is the index in asks of the request user asks or is asked; -1 when none.
func pendingOf(asks []draftAsk, user int64) int {
	for i, a := range asks {
		if a.from == user || a.to == user {
			return i
		}
	}
	return -1
}

// stageWhy names the stage that refuses a call.
func (d *draftRun) stageWhy() string {
	switch d.stage {
	case draftStageRole:
		return "the role phase runs"
	case draftStageTrade:
		return "the picks are over"
	}
	return "the board runs"
}

// draftTurn is the server's turn time.
func (s *Server) draftTurn() time.Duration {
	if s.DraftTurn > 0 {
		return s.DraftTurn
	}
	return DraftTurnDefault
}

// draftRoleTime and draftTradeTime are the server's role and trade phases.
func (s *Server) draftRoleTime() time.Duration {
	if s.DraftRoleTime > 0 {
		return s.DraftRoleTime
	}
	return DraftRoleDefault
}

func (s *Server) draftTradeTime() time.Duration {
	if s.DraftTradeTime > 0 {
		return s.DraftTradeTime
	}
	return DraftTradeDefault
}

// draftPool is the board's pool: the shapers of the rules' pool this server offers, with their titles.
func (s *Server) draftPool(pool string) []DraftShaper {
	var out []DraftShaper
	titles := map[string]DraftShaper{}
	if !s.Content.Empty() {
		for _, sh := range s.Content.Shapers {
			titles[sh.Name] = DraftShaper{ID: sh.Name, Name: sh.Title, CatalogID: sh.CatalogID, Portrait: sh.Portrait,
				Card: sh.Card}
		}
	}
	for _, class := range s.rulesPools().Shapers(pool) {
		d := titles[class]
		d.ID = class
		if d.Name == "" {
			d.Name = strings.TrimPrefix(class, "PC_")
		}
		out = append(out, d)
	}
	return out
}

// isDraftLocked reports whether the game's rules are a draft (hg.mu held).
func (hg *hostedGame) isDraftLocked() bool {
	return hg.custom != nil && hg.rulesOnLocked() && hg.custom.rules.Normal().Pick == "draft"
}

// beginDraftLocked starts the lobby's draft at its admin's START (hg.mu held; the rules passed their check): the seats
// from the lobby's roster, the pool, the role phase's timers. The roster is locked (the server sets RosterLockedKey "1"
// when the admin's START didn't); the returned func tells the players (outside hg.mu).
func (hg *hostedGame) beginDraftLocked() (func(), error) {
	s := hg.s
	var seats [2][]int64
	names := map[int64]string{}
	for _, e := range lobbyRoster(hg.players, hg.game.Attrs) {
		if e.Spectator || e.Team < 1 || e.Team > 2 {
			continue
		}
		seats[e.Team-1] = append(seats[e.Team-1], e.User)
		names[e.User] = e.Name
	}
	pool := s.draftPool(hg.custom.rules.Normal().ShaperPool)
	ids := make([]string, len(pool))
	for i, p := range pool {
		ids[i] = p.ID
	}
	seed := s.rollSeed()
	rng := draftRNG(seed)
	order, banners, how := shuffleDraftSeats(&seats, rng, !s.DraftFixedOrder)
	board, err := rules.NewDraftOrder(ids, [2]int{len(seats[0]), len(seats[1])}, order)
	if err != nil {
		return nil, err
	}
	d := &draftRun{board: board, seats: seats, names: names, pool: pool, turn: s.draftTurn(), roleTime: s.draftRoleTime(),
		tradeTime: s.draftTradeTime(), stage: draftStageRole, window: -1, seed: seed, rng: rng,
		roles: map[int64]string{}, hovers: map[int64]string{}, ready: map[int64]bool{}, declined: map[draftAskKey]bool{}}
	hg.custom.draft, hg.custom.lastDraft = d, d
	var lock []tdf.Pair
	if attrValue(hg.game.Attrs, AttrRosterLocked) != "1" {
		lock = []tdf.Pair{tdf.KV(AttrRosterLocked, "1")}
		hg.game.Attrs = mergeAttrs(hg.game.Attrs, lock)
	}
	windows := 0
	if n := len(board.Turns); n > 0 {
		windows = board.Turns[n-1].Window + 1
	}
	s.Log.Logf("blaze", "   game %d: the draft begins (DRAFT1; seed %d, %d shapers in the pool, %d turns in %d windows of %v; "+
		"the role phase %v, the trade phase up to %v): team 1 %v, team 2 %v (the pick order, %s); team %d starts%s: the "+
		"first ban and the first pick; the bans by %s; the roster stays locked%s", hg.game.ID, seed,
		len(pool), len(board.Turns), windows, d.turn, d.roleTime, d.tradeTime, seats[0], seats[1], how, order.First+1,
		map[bool]string{true: " (a coin flip)", false: ""}[!s.DraftFixedOrder], banners,
		map[bool]string{true: " (RosterLockedKey \"1\" set by the server)", false: ""}[lock != nil])
	hg.draftStepLocked(d, d.roleTime)
	gid := hg.game.ID
	return func() {
		if lock != nil {
			note := GameAttribChangeNotification(gid, lock)
			hg.push(NotifyGameAttribChangeID, "NotifyGameAttribChange", func(*hostedPlayer) []byte { return note })
		}
		hg.publishDraft()
	}, nil
}

// draftRNG is a draft's generator from its seed: the shuffle and the coin flip (shuffleDraftSeats), then the timeouts'
// random picks.
func draftRNG(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, seed^0x52554C4553350000)) // "RULES5"
}

// shuffleDraftSeats decides a draft's order (DRAFTSHUF, the user's decision of 2026-11-11): with shuffle, each team's
// drafters (seats, in the lobby's seat order) are shuffled into a random pick order (seat k makes the team's k-th
// pick; rng is the draft's generator, from its logged seed) and a coin flip picks the team that starts (the first ban
// and the first pick); without it (dgserver --draft-shuffle=off, Server.DraftFixedOrder) the lobby's order stays and
// team 1 starts. Either way each team's bans stay its top player's in the lobby (its first seat before the shuffle):
// the order's Banner is that player's seat. It returns the order, the bans' players and the order's kind for the log.
func shuffleDraftSeats(seats *[2][]int64, rng *rand.Rand, shuffle bool) (rules.DraftOrder, string, string) {
	var o rules.DraftOrder
	var banners []string
	how := "the lobby's"
	for team := range seats {
		ids := seats[team]
		if len(ids) == 0 {
			continue
		}
		top := ids[0]
		if shuffle {
			rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
		}
		for seat, id := range ids {
			if id == top {
				o.Banner[team] = seat
			}
		}
		banners = append(banners, fmt.Sprintf("user %d (team %d)", top, team+1))
	}
	if shuffle {
		o.First = rng.IntN(2)
		how = "shuffled"
	}
	if len(banners) == 0 {
		banners = []string{"nobody"}
	}
	return o, strings.Join(banners, " and "), how
}

// stopTimers ends the current step's timers (a timer that already fired finds its step gone: gen).
func (d *draftRun) stopTimers() {
	d.gen++
	if d.timer != nil {
		d.timer.Stop()
	}
	if d.aid != nil {
		d.aid.Stop()
	}
}

// draftStepLocked begins a step of d that lasts length (the role phase, a window, the trade phase): its timer and the
// test aid's (hg.mu held).
func (hg *hostedGame) draftStepLocked(d *draftRun, length time.Duration) {
	s := hg.s
	d.stopTimers()
	gen := d.gen
	d.stepLen, d.stepEnds = length, time.Now().Add(length)
	d.timer = time.AfterFunc(length, func() { hg.draftTimedOut(d, gen) })
	if s.TestDraft > 0 {
		d.aid = time.AfterFunc(s.TestDraft, func() { s.draftTestAid(hg, d, gen) })
	}
}

// draftAdvanceLocked follows a change of the board (hg.mu held): the hovers that can't stand dropped; a new window's
// timer, with the swaps it ends; after the last window the trade phase, or the end, whose start it returns to run
// outside hg.mu.
func (hg *hostedGame) draftAdvanceLocked(d *draftRun) func() {
	s := hg.s
	for id, h := range d.hovers {
		team, seat, _ := d.seatOf(id)
		if pt := d.board.PickTurn(team, seat); d.board.Free(h) != nil || pt < 0 || d.board.Turns[pt].How != "" {
			delete(d.hovers, id)
		}
	}
	if d.stage != draftStageBoard {
		return nil
	}
	if !d.board.Done() {
		if w := d.board.CurWindow(); w != d.window {
			d.window = w
			hg.expireSwapsLocked(d)
			hg.draftStepLocked(d, d.turn)
		}
		return nil
	}
	d.window = -1
	hg.expireSwapsLocked(d)
	d.hovers = map[int64]string{}
	if len(d.seats[0]) < 2 && len(d.seats[1]) < 2 {
		s.Log.Logf("blaze", "   game %d: the picks are over; no team has two drafters: no trade phase", hg.game.ID)
		return hg.draftDoneLocked(d)
	}
	d.stage = draftStageTrade
	s.Log.Logf("blaze", "   game %d: the picks are over; the trade phase begins (up to %v)", hg.game.ID, d.tradeTime)
	hg.draftStepLocked(d, d.tradeTime)
	return nil
}

// expireSwapsLocked drops the pending swaps that can't be made any more: one of the two is on turn or picked, or the
// picks are over (hg.mu held). An expired request isn't asked again.
func (hg *hostedGame) expireSwapsLocked(d *draftRun) {
	kept := d.swaps[:0]
	for _, a := range d.swaps {
		team, from, _ := d.seatOf(a.from)
		_, to, _ := d.seatOf(a.to)
		if d.board.Done() || d.board.CheckSwap(team, from, to) != nil {
			d.declined[draftAskKey{"swap", a.from, a.to}] = true
			hg.s.Log.Logf("blaze", "   game %d: draft: user %d's swap request to user %d expired", hg.game.ID, a.from, a.to)
			continue
		}
		kept = append(kept, a)
	}
	d.swaps = kept
}

// draftTimedOut ends step gen of d by its time, unless it ended before: the role phase, a window (its open turns
// time out), the trade phase.
func (hg *hostedGame) draftTimedOut(d *draftRun, gen int) {
	s := hg.s
	hg.mu.Lock()
	if hg.custom == nil || hg.custom.draft != d || d.gen != gen || hg.ended {
		hg.mu.Unlock()
		return
	}
	var finish func()
	switch d.stage {
	case draftStageRole:
		var desc []string
		for _, id := range d.drafters() {
			role := d.roles[id]
			if role == "" {
				role = "none"
			}
			desc = append(desc, fmt.Sprintf("user %d %s", id, role))
		}
		s.Log.Logf("blaze", "   game %d: the draft's role phase is over: %s", hg.game.ID, strings.Join(desc, ", "))
		d.stage = draftStageBoard
		finish = hg.draftAdvanceLocked(d)
	case draftStageBoard:
		for _, i := range d.board.Timeout(d.rng, d.hoverOf) {
			turn := d.board.Turns[i]
			who := int64(0)
			if turn.Seat < len(d.seats[turn.Team]) {
				who = d.seats[turn.Team][turn.Seat]
			}
			what := "skipped"
			if turn.How == rules.DraftHover {
				what = "its hover " + turn.Kind + "s " + turn.Shaper // DRAFT1b
			} else if turn.Kind == rules.DraftPick {
				what = "a random pick: " + turn.Shaper
				if turn.Shaper == "" {
					what = "no shaper left to pick"
				}
			}
			s.Log.Logf("blaze", "   game %d: draft turn %d/%d (%s, team %d, user %d) timed out: %s", hg.game.ID, i+1,
				len(d.board.Turns), turn.Kind, turn.Team+1, who, what)
		}
		finish = hg.draftAdvanceLocked(d)
	case draftStageTrade:
		s.Log.Logf("blaze", "   game %d: the draft's trade phase is over (its time)", hg.game.ID)
		finish = hg.draftDoneLocked(d)
	}
	hg.mu.Unlock()
	hg.publishDraft()
	if finish != nil {
		finish()
	}
}

// decodeDraftArgs reads a draft call's arguments into v strictly: one JSON object of v's fields, at most draftArgsMax
// bytes.
func decodeDraftArgs(args json.RawMessage, v any) error {
	if len(args) > draftArgsMax {
		return errors.New("arguments too long")
	}
	d := json.NewDecoder(bytes.NewReader(args))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("arguments: %v", err)
	}
	if d.More() {
		return errors.New("arguments: more than one value")
	}
	return nil
}

// parseDraftArgs reads draft.ban's and draft.pick's arguments: {"shaper":"PC_..."}, nothing else.
func parseDraftArgs(args json.RawMessage) (string, error) {
	var a struct {
		Shaper *string `json:"shaper"`
	}
	if err := decodeDraftArgs(args, &a); err != nil {
		return "", err
	}
	if a.Shaper == nil || *a.Shaper == "" {
		return "", errors.New("no shaper in the arguments")
	}
	return *a.Shaper, nil
}

// parseDraftHover reads draft.hover's arguments: {"shaper":"PC_..."}, or "" to clear.
func parseDraftHover(args json.RawMessage) (string, error) {
	var a struct {
		Shaper *string `json:"shaper"`
	}
	if err := decodeDraftArgs(args, &a); err != nil {
		return "", err
	}
	if a.Shaper == nil {
		return "", errors.New("no shaper in the arguments")
	}
	return *a.Shaper, nil
}

// parseDraftRole reads draft.role's arguments: {"role":"<a role id>"}.
func parseDraftRole(args json.RawMessage) (string, error) {
	var a struct {
		Role *string `json:"role"`
	}
	if err := decodeDraftArgs(args, &a); err != nil {
		return "", err
	}
	if a.Role == nil || !rules.IsDraftRole(*a.Role) {
		return "", errors.New("no role of the draft's in the arguments")
	}
	return *a.Role, nil
}

// parseDraftSeat reads draft.swap's and draft.trade's arguments: {"seat":n}, a teammate's seat (0 to 4).
func parseDraftSeat(args json.RawMessage) (int, error) {
	var a struct {
		Seat *int `json:"seat"`
	}
	if err := decodeDraftArgs(args, &a); err != nil {
		return 0, err
	}
	if a.Seat == nil || *a.Seat < 0 || *a.Seat >= rules.DraftPicksPerTeam {
		return 0, errors.New("no seat (0 to 4) in the arguments")
	}
	return *a.Seat, nil
}

// parseDraftAnswer reads draft.swapAnswer's and draft.tradeAnswer's arguments: {"accept":true|false}.
func parseDraftAnswer(args json.RawMessage) (bool, error) {
	var a struct {
		Accept *bool `json:"accept"`
	}
	if err := decodeDraftArgs(args, &a); err != nil {
		return false, err
	}
	if a.Accept == nil {
		return false, errors.New("no accept in the arguments")
	}
	return *a.Accept, nil
}

// parseDraftNone reads draft.ready's arguments: none, or {}.
func parseDraftNone(args json.RawMessage) error {
	if len(args) == 0 {
		return nil
	}
	var a struct{}
	return decodeDraftArgs(args, &a)
}

// attachDraftCalls registers the draft's calls on ui.
func (s *Server) attachDraftCalls(ui *uichannel.Server) {
	for call, kind := range map[string]string{CallDraftBan: rules.DraftBan, CallDraftPick: rules.DraftPick} { // RULES5b
		ui.Handle(call, func(user int64, args json.RawMessage) error {
			shaper, err := parseDraftArgs(args)
			if err != nil {
				return err
			}
			return s.draftCall(user, kind, shaper)
		})
	}
	ui.Handle(CallDraftRole, func(user int64, args json.RawMessage) error {
		role, err := parseDraftRole(args)
		if err != nil {
			return err
		}
		return s.draftRoleCall(user, role)
	})
	ui.Handle(CallDraftHover, func(user int64, args json.RawMessage) error {
		shaper, err := parseDraftHover(args)
		if err != nil {
			return err
		}
		return s.draftHoverCall(user, shaper)
	})
	for call, trade := range map[string]bool{CallDraftSwap: false, CallDraftTrade: true} {
		ui.Handle(call, func(user int64, args json.RawMessage) error {
			seat, err := parseDraftSeat(args)
			if err != nil {
				return err
			}
			return s.draftAskCall(user, trade, seat)
		})
	}
	for call, trade := range map[string]bool{CallDraftSwapAnswer: false, CallDraftTradeAnswer: true} {
		ui.Handle(call, func(user int64, args json.RawMessage) error {
			accept, err := parseDraftAnswer(args)
			if err != nil {
				return err
			}
			return s.draftAnswerCall(user, trade, accept)
		})
	}
	ui.Handle(CallDraftReady, func(user int64, args json.RawMessage) error {
		if err := parseDraftNone(args); err != nil {
			return err
		}
		return s.draftReadyCall(user)
	})
}

// draftTell is what an accepted draft call tells: the board to everyone, or (team 1 or 2) to that team's drafters only,
// then finish (the start after the end) outside hg.mu.
type draftTell struct {
	team   int
	finish func()
}

// draftDo runs user's draft call: act gets the running draft and the caller's seat under hg.mu. Only a drafter of a
// running draft; a refusal changes nothing and the caller hears the board again.
func (s *Server) draftDo(user int64, act func(hg *hostedGame, d *draftRun, team, seat int) (draftTell, error)) error {
	hg := s.lobbyOf(user)
	if hg == nil {
		return errors.New("not in a lobby")
	}
	hg.mu.Lock()
	d := hg.custom.draft
	refuse := func(err error) error {
		hg.mu.Unlock()
		hg.sendDraftTo(user)
		return err
	}
	if d == nil {
		return refuse(errors.New("no draft runs"))
	}
	team, seat, ok := d.seatOf(user)
	if !ok {
		return refuse(errors.New("not a drafter (a spectator, or not on a team at the draft's start)"))
	}
	tell, err := act(hg, d, team, seat)
	if err != nil {
		return refuse(err)
	}
	hg.mu.Unlock()
	if tell.team != 0 {
		hg.publishDraftTeam(tell.team)
	} else {
		hg.publishDraft()
	}
	if tell.finish != nil {
		tell.finish()
	}
	return nil
}

// draftCall is user's draft.ban or draft.pick (kind) of shaper: on the board, with a role, with an open turn of that
// kind in the current window, of a free shaper of the pool.
func (s *Server) draftCall(user int64, kind, shaper string) error {
	return s.draftDo(user, func(hg *hostedGame, d *draftRun, team, seat int) (draftTell, error) {
		if d.stage != draftStageBoard {
			return draftTell{}, fmt.Errorf("%s %s: %s", kind, clipText(shaper), d.stageWhy())
		}
		if d.roles[user] == "" {
			return draftTell{}, errChooseRole
		}
		if err := d.board.Act(team, seat, kind, shaper); err != nil {
			return draftTell{}, fmt.Errorf("%s %s: %v", kind, clipText(shaper), err)
		}
		s.Log.Logf("blaze", "   game %d: draft turn %d/%d: user %d (team %d) %ss %s", hg.game.ID, d.board.Last+1,
			len(d.board.Turns), user, team+1, kind, shaper)
		delete(d.hovers, user)
		return draftTell{finish: hg.draftAdvanceLocked(d)}, nil
	})
}

// draftRoleCall is user's draft.role: declared or changed in any phase until the end; its team hears it.
func (s *Server) draftRoleCall(user int64, role string) error {
	return s.draftDo(user, func(hg *hostedGame, d *draftRun, team, _ int) (draftTell, error) {
		if d.roles[user] != role {
			d.roles[user] = role
			s.Log.Logf("blaze", "   game %d: draft: user %d (team %d) declares the role %s", hg.game.ID, user, team+1, role)
		}
		return draftTell{team: team + 1}, nil
	})
}

// draftHoverCall is user's draft.hover: on the board, with a role, before its own pick, of a free shaper of the pool
// ("" clears it); its team hears it.
func (s *Server) draftHoverCall(user int64, shaper string) error {
	return s.draftDo(user, func(hg *hostedGame, d *draftRun, team, seat int) (draftTell, error) {
		if d.stage != draftStageBoard {
			return draftTell{}, fmt.Errorf("hover: %s", d.stageWhy())
		}
		if d.roles[user] == "" {
			return draftTell{}, errChooseRole
		}
		if pt := d.board.PickTurn(team, seat); pt < 0 || d.board.Turns[pt].How != "" {
			return draftTell{}, errors.New("hover: this player has picked")
		}
		if shaper == "" {
			delete(d.hovers, user)
			return draftTell{team: team + 1}, nil
		}
		if err := d.board.Free(shaper); err != nil {
			return draftTell{}, fmt.Errorf("hover %s: %v", clipText(shaper), err)
		}
		d.hovers[user] = shaper
		return draftTell{team: team + 1}, nil
	})
}

// draftAskCall is user's draft.swap (trade false: before the trade phase, CheckSwap's conditions) or draft.trade (the
// trade phase, CheckTrade's, neither ready) asking its teammate at seat. One pending request of each kind per player
// (asking or asked), and a declined or expired one isn't asked again; its team hears it.
func (s *Server) draftAskCall(user int64, trade bool, toSeat int) error {
	kind := map[bool]string{false: "swap", true: "trade"}[trade]
	return s.draftDo(user, func(hg *hostedGame, d *draftRun, team, seat int) (draftTell, error) {
		asks := &d.swaps
		if trade {
			asks = &d.trades
		}
		switch {
		case trade && d.stage != draftStageTrade:
			return draftTell{}, errors.New("trade: only in the trade phase")
		case !trade && d.stage == draftStageTrade:
			return draftTell{}, fmt.Errorf("swap: %s", d.stageWhy())
		case toSeat >= len(d.seats[team]):
			return draftTell{}, fmt.Errorf("%s: no teammate at seat %d", kind, toSeat)
		}
		to := d.seats[team][toSeat]
		var err error
		if trade {
			err = d.board.CheckTrade(team, seat, toSeat)
		} else {
			err = d.board.CheckSwap(team, seat, toSeat)
		}
		switch {
		case err != nil:
			return draftTell{}, fmt.Errorf("%s: %v", kind, err)
		case pendingOf(*asks, user) >= 0 || pendingOf(*asks, to) >= 0:
			return draftTell{}, fmt.Errorf("%s: one of the two has a request pending", kind)
		case d.declined[draftAskKey{kind, user, to}]:
			return draftTell{}, fmt.Errorf("%s: asked before", kind)
		case trade && (d.ready[user] || d.ready[to]):
			return draftTell{}, errors.New("trade: one of the two is ready")
		}
		*asks = append(*asks, draftAsk{user, to})
		s.Log.Logf("blaze", "   game %d: draft: user %d (team %d) asks user %d for a %s", hg.game.ID, user, team+1, to,
			map[bool]string{false: "pick-order swap", true: "trade"}[trade])
		return draftTell{team: team + 1}, nil
	})
}

// draftAnswerCall is user's draft.swapAnswer or draft.tradeAnswer to the request it was asked: accepted, the two seats'
// pick turns (or drafted shapers) change places and everyone hears the board; declined, the team hears it.
func (s *Server) draftAnswerCall(user int64, trade, accept bool) error {
	kind := map[bool]string{false: "swap", true: "trade"}[trade]
	return s.draftDo(user, func(hg *hostedGame, d *draftRun, team, seat int) (draftTell, error) {
		asks := &d.swaps
		if trade {
			asks = &d.trades
		}
		i := -1
		for j, a := range *asks {
			if a.to == user {
				i = j
			}
		}
		if i < 0 {
			return draftTell{}, fmt.Errorf("%sAnswer: no request to this player", kind)
		}
		a := (*asks)[i]
		_, from, _ := d.seatOf(a.from)
		if !accept {
			*asks = append((*asks)[:i], (*asks)[i+1:]...)
			d.declined[draftAskKey{kind, a.from, a.to}] = true
			s.Log.Logf("blaze", "   game %d: draft: user %d declines user %d's %s", hg.game.ID, user, a.from, kind)
			return draftTell{team: team + 1}, nil
		}
		var err error
		if trade {
			err = d.board.Trade(team, from, seat)
		} else {
			err = d.board.SwapPicks(team, from, seat)
		}
		if err != nil {
			return draftTell{}, fmt.Errorf("%sAnswer: %v", kind, err)
		}
		*asks = append((*asks)[:i], (*asks)[i+1:]...)
		if trade {
			p := d.board.Picks()
			s.Log.Logf("blaze", "   game %d: draft: user %d accepts user %d's trade: user %d plays %s, user %d %s",
				hg.game.ID, user, a.from, a.from, p[team][from], user, p[team][seat])
		} else {
			s.Log.Logf("blaze", "   game %d: draft: user %d accepts user %d's swap: their pick turns changed places",
				hg.game.ID, user, a.from)
		}
		return draftTell{}, nil
	})
}

// draftReadyCall is user's draft.ready in the trade phase: its pending trade dropped; once every drafter is ready the
// phase ends early and the game starts.
func (s *Server) draftReadyCall(user int64) error {
	return s.draftDo(user, func(hg *hostedGame, d *draftRun, team, _ int) (draftTell, error) {
		if d.stage != draftStageTrade {
			return draftTell{}, errors.New("ready: only in the trade phase")
		}
		if d.ready[user] {
			return draftTell{team: team + 1}, nil
		}
		d.ready[user] = true
		if i := pendingOf(d.trades, user); i >= 0 {
			d.trades = append(d.trades[:i], d.trades[i+1:]...)
		}
		n, all := 0, d.drafters()
		for _, id := range all {
			if d.ready[id] {
				n++
			}
		}
		s.Log.Logf("blaze", "   game %d: draft: user %d is ready (%d/%d)", hg.game.ID, user, n, len(all))
		if n < len(all) {
			return draftTell{team: team + 1}, nil
		}
		s.Log.Logf("blaze", "   game %d: every drafter is ready: the trade phase ends early", hg.game.ID)
		return draftTell{finish: hg.draftDoneLocked(d)}, nil
	})
}

// clipText is a client's text for the log, at most 40 bytes.
func clipText(v string) string {
	if len(v) > 40 {
		v = v[:40] + "..."
	}
	return fmt.Sprintf("%q", v)
}

// draftTestAid (Server.TestDraft) acts for every drafter in d's step gen, through the calls a player would make: the
// role of each drafter without one (seat k: DraftRoles[k mod 4]); on the board, once, a pick-order swap of a team's
// first two seats (accepted), then a ban of the pool's last free shaper or a pick of its first for every open seat of
// the window; in the trade phase, once, a trade of a team's first two seats (accepted), then every drafter ready. Its
// first board action also calls once as a drafter who isn't on turn, which must be refused.
func (s *Server) draftTestAid(hg *hostedGame, d *draftRun, gen int) {
	type act struct {
		who          int64
		kind, shaper string
	}
	hg.mu.Lock()
	if hg.custom == nil || hg.custom.draft != d || d.gen != gen || hg.ended {
		hg.mu.Unlock()
		return
	}
	gid := hg.game.ID
	var roles []act
	for _, ids := range d.seats {
		for seat, id := range ids {
			if d.roles[id] == "" {
				roles = append(roles, act{who: id, shaper: rules.DraftRoles[seat%len(rules.DraftRoles)].ID})
			}
		}
	}
	var pair [2]int64 // a swap's or a trade's two seats (asker, answerer)
	var acts []act
	var other act
	var readies []int64
	switch d.stage {
	case draftStageBoard:
		if !d.aidSwapped {
			d.aidSwapped = true
			for team, ids := range d.seats {
				if pair[0] == 0 && len(ids) >= 2 && d.board.CheckSwap(team, 0, 1) == nil {
					pair = [2]int64{ids[0], ids[1]}
				}
			}
		}
		taken := map[string]bool{}
		for _, i := range d.board.Open() {
			t := d.board.Turns[i]
			shaper := ""
			for k := range d.pool {
				if t.Kind == rules.DraftBan {
					k = len(d.pool) - 1 - k
				}
				if id := d.pool[k].ID; !taken[id] && d.board.Free(id) == nil {
					shaper, taken[id] = id, true
					break
				}
			}
			if t.Seat < len(d.seats[t.Team]) && shaper != "" {
				acts = append(acts, act{d.seats[t.Team][t.Seat], t.Kind, shaper})
			}
		}
		if !d.aidTried && len(acts) > 0 {
			d.aidTried = true
			for team, ids := range d.seats {
				for seat, id := range ids {
					if other.who == 0 && !d.board.OnTurn(team, seat) {
						other = act{id, acts[0].kind, acts[0].shaper}
					}
				}
			}
		}
	case draftStageTrade:
		if !d.aidTraded {
			d.aidTraded = true
			for team, ids := range d.seats {
				if pair[0] == 0 && len(ids) >= 2 && d.board.CheckTrade(team, 0, 1) == nil {
					pair = [2]int64{ids[0], ids[1]}
				}
			}
		}
		readies = d.drafters()
	}
	stage := d.stage
	hg.mu.Unlock()
	aid := func(format string, args ...any) {
		s.Log.Logf("blaze", "   game %d: draft (--test-draft, a test aid): "+format, append([]any{gid}, args...)...)
	}
	for _, r := range roles {
		aid("user %d declares the role %s (%v)", r.who, r.shaper, errText(s.draftRoleCall(r.who, r.shaper)))
	}
	if pair[0] != 0 {
		trade := stage == draftStageTrade
		kind := map[bool]string{false: "swap", true: "trade"}[trade]
		aid("user %d asks user %d for a %s (%v)", pair[0], pair[1], kind, errText(s.draftAskCall(pair[0], trade, 1)))
		aid("user %d accepts (%v)", pair[1], errText(s.draftAnswerCall(pair[1], trade, true)))
	}
	if other.who != 0 {
		aid("user %d's %s out of turn: %v", other.who, other.kind, s.draftCall(other.who, other.kind, other.shaper))
	}
	for _, a := range acts {
		aid("user %d %ss %s", a.who, a.kind, a.shaper)
		if err := s.draftCall(a.who, a.kind, a.shaper); err != nil {
			aid("refused: %v", err)
		}
	}
	for _, id := range readies {
		if err := s.draftReadyCall(id); err != nil {
			aid("user %d's ready refused: %v", id, err)
		}
	}
}

// errText is err for a log line: "ok" for nil.
func errText(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

// draftDoneLocked ends d after its last step (hg.mu held) and returns the game's start, run outside hg.mu: the rules'
// last check, then the --custom-autostart path with each human's drafted shaper (after the trades) and role. Findings
// cancel the draft instead.
func (hg *hostedGame) draftDoneLocked(d *draftRun) func() {
	s := hg.s
	hg.custom.draft = nil
	d.stopTimers()
	if hg.ended || hg.custom.started {
		d.phase = DraftPhaseCancelled
		return nil
	}
	fs, rl, fill := hg.startRulesLocked()
	if len(fs) > 0 {
		d.phase, d.reason = DraftPhaseCancelled, "The rules can't be played: "+rules.DescribeFindings(fs)
		tell := hg.cancelDraftLocked()
		gid := hg.game.ID
		return func() {
			s.Log.Logf("blaze", "   game %d: the draft ended, but the rules' findings refuse the start (%s); cancelled",
				gid, rules.DescribeFindings(fs))
			tell()
		}
	}
	d.phase = DraftPhaseDone
	drafted := map[int64]draftedSeat{}
	var desc, roles []string
	board := d.board.Picks()
	for team, ids := range d.seats {
		for seat, id := range ids {
			role := d.roles[id]
			if seat < len(board[team]) && board[team][seat] != "" {
				drafted[id] = draftedSeat{Shaper: board[team][seat], Role: role}
				desc = append(desc, fmt.Sprintf("user %d (team %d) %s", id, team+1, board[team][seat]))
			}
			if role == "" {
				role = "none"
			}
			roles = append(roles, fmt.Sprintf("user %d %s", id, role))
		}
	}
	hg.custom.started = true
	roster := lobbyRoster(hg.players, hg.game.Attrs)
	players := append([]*hostedPlayer(nil), hg.players...)
	mt := hg.match
	gid := hg.game.ID
	return func() {
		s.Log.Logf("blaze", "   game %d: the draft is done: %s; bans %v; roles %s", gid, strings.Join(desc, ", "),
			d.board.Bans(), strings.Join(roles, ", "))
		s.Log.Logf("blaze", "   game %d starts (the draft ended): roster %s%s", gid, describeRoster(roster),
			describeStartRules(rl, fill))
		if mt != nil {
			s.lobbyContent(roster, players)
			sg := gmshost.StartGame{Roster: roster, Rules: rl, FillTeams: fill}
			s.applyPickRules(gid, &sg, drafted)
			mt.StartGameWith(sg)
		}
		hg.setState(GameStateInGame)
	}
}

// cancelDraft cancels the lobby's running draft for why (a player left): the roster unlocked, every ready mark reset,
// the board's last state "cancelled". Nothing without a draft.
func (hg *hostedGame) cancelDraft(why string) {
	hg.mu.Lock()
	d := hg.custom.draft
	if d == nil {
		hg.mu.Unlock()
		return
	}
	hg.custom.draft = nil
	d.phase, d.reason = DraftPhaseCancelled, why
	tell := hg.cancelDraftLocked()
	gid := hg.game.ID
	hg.mu.Unlock()
	hg.s.Log.Logf("blaze", "   game %d: the draft is cancelled: %s; the roster unlocked (RosterLockedKey \"0\"), every "+
		"ready mark reset", gid, why)
	tell()
}

// cancelDraftLocked is a cancel's part under hg.mu (the draft already taken out): the timers stopped, every ready mark
// reset, the roster unlocked; the returned func tells everyone (outside hg.mu).
func (hg *hostedGame) cancelDraftLocked() func() {
	d := hg.custom.lastDraft
	d.stopTimers()
	d.swaps, d.trades = nil, nil
	for _, hp := range hg.players {
		hg.custom.check.Set(hp.p.ID, false)
	}
	if hg.ended {
		return func() { hg.publishDraft() }
	}
	unlock := hg.unlockRosterLocked()
	return func() {
		hg.publishDraft()
		unlock(nil)
		hg.publishLobby()
	}
}

// draftLeft: player left the game during a draft; a drafter's leave cancels it.
func (hg *hostedGame) draftLeft(player int64) {
	hg.mu.Lock()
	d := hg.custom.draft
	drafter, name := false, ""
	if d != nil {
		_, _, drafter = d.seatOf(player)
		name = d.names[player]
	}
	hg.mu.Unlock()
	if drafter {
		hg.cancelDraft(fmt.Sprintf("%s left the game.", name))
	}
}

// ownLocked is team's private part of the board (hg.mu held): fresh lists, never shared with another team's.
func (d *draftRun) ownLocked(team int) *DraftOwn {
	ids := d.seats[team]
	own := &DraftOwn{Roles: make([]string, len(ids)), Hovers: make([]string, len(ids)), Ready: make([]bool, len(ids)),
		Swaps: []DraftAsk{}, Trades: []DraftAsk{}}
	for seat, id := range ids {
		own.Roles[seat], own.Hovers[seat], own.Ready[seat] = d.roles[id], d.hovers[id], d.ready[id]
	}
	for _, x := range []struct {
		asks []draftAsk
		out  *[]DraftAsk
	}{{d.swaps, &own.Swaps}, {d.trades, &own.Trades}} {
		for _, a := range x.asks {
			if t, from, _ := d.seatOf(a.from); t == team {
				_, to, _ := d.seatOf(a.to)
				*x.out = append(*x.out, DraftAsk{From: from, To: to})
			}
		}
	}
	return own
}

// draftStatesLocked are the board's events (hg.mu held): to only, or (0) to everyone in the lobby who entered, and of
// those (team 1 or 2) only that team's drafters; nil without a draft. Each drafter's carries its own team's private
// part; a spectator's none.
func (hg *hostedGame) draftStatesLocked(only int64, team int) map[int64]DraftState {
	d := hg.custom.lastDraft
	if d == nil {
		return nil
	}
	base := DraftState{Phase: d.phase, Reason: d.reason, Turn: -1, Window: -1, TurnMs: d.turn.Milliseconds(), Pool: d.pool}
	onBoard := d.running() && d.stage == draftStageBoard && !d.board.Done()
	if d.running() {
		base.Left = max(time.Until(d.stepEnds).Milliseconds(), 0)
		base.TurnMs = d.stepLen.Milliseconds()
		switch d.stage {
		case draftStageRole:
			base.Phase = DraftPhaseRole
		case draftStageTrade:
			base.Phase = DraftPhaseTrade
		default:
			base.Phase = d.board.Phase()
		}
	}
	if onBoard {
		base.Turn, base.Window = d.board.Cur, d.board.CurWindow()
	}
	for t, ids := range d.seats {
		base.Teams[t] = []string{}
		for _, id := range ids {
			base.Teams[t] = append(base.Teams[t], d.names[id])
		}
	}
	for _, t := range d.board.Turns {
		player := ""
		if t.Seat < len(d.seats[t.Team]) {
			player = d.names[d.seats[t.Team][t.Seat]]
		}
		base.Turns = append(base.Turns, DraftStateTurn{Kind: t.Kind, Team: t.Team + 1, Player: player, Shaper: t.Shaper,
			How: t.How, Window: t.Window})
	}
	if base.Pool == nil {
		base.Pool = []DraftShaper{}
	}
	if base.Turns == nil {
		base.Turns = []DraftStateTurn{}
	}
	var own [2]*DraftOwn
	out := map[int64]DraftState{}
	for _, hp := range hg.players {
		id := hp.p.ID
		if hp.removed || !hg.custom.check.Has(id) || only != 0 && id != only {
			continue
		}
		rteam, rseat, drafter := d.seatOf(id)
		if team != 0 && (!drafter || rteam+1 != team) {
			continue
		}
		st := base
		if drafter {
			if own[rteam] == nil {
				own[rteam] = d.ownLocked(rteam)
			}
			st.You, st.Own = &DraftYou{Team: rteam + 1, Seat: rseat}, own[rteam]
			st.YourTurn = onBoard && d.board.OnTurn(rteam, rseat)
		}
		out[id] = st
	}
	return out
}

// publishDraft tells everyone in the lobby who entered the board as it is now (its snapshot and its sends one step,
// hg.lobbySend, so nobody hears an older board after a newer one).
func (hg *hostedGame) publishDraft() { hg.sendDraft(0, 0) }

// publishDraftTeam tells only team's (1 or 2) drafters the board: a change of the team's private part alone (a role, a
// hover, a request, a ready mark), which the enemy and the spectators don't hear.
func (hg *hostedGame) publishDraftTeam(team int) { hg.sendDraft(0, team) }

// sendDraftTo tells only user the board (a refused call, a late lobby.enter), also the last one after a cancel.
func (hg *hostedGame) sendDraftTo(user int64) { hg.sendDraft(user, 0) }

func (hg *hostedGame) sendDraft(only int64, team int) {
	s := hg.s
	if s.UI == nil {
		return
	}
	hg.lobbySend.Lock()
	defer hg.lobbySend.Unlock()
	hg.mu.Lock()
	if hg.custom == nil {
		hg.mu.Unlock()
		return
	}
	states := hg.draftStatesLocked(only, team)
	hg.mu.Unlock()
	for id, st := range states {
		s.UI.Send(id, EventDraftState, st)
	}
}
