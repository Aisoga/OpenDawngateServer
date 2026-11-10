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
)

// The draft (RULES5b; package rules has the board, rules.Draft): a custom game whose pick rule is "draft" drafts its
// shapers in the lobby before shaper select.
//
//   - START (the admin's advanceGameState IN_GAME, after the ready check and the rules' last check) holds the start: the
//     request is refused as the ready check's refused start is (the client stays in its lobby, no dialog), but the roster
//     stays locked (RosterLockedKey "1": the admin's START set it, and the server sets it too if it didn't), and the
//     draft begins. Each team's humans are its seats, in the lobby's seat order (lobbyRoster); spectators see the board
//     and never act. A second START during the draft is refused and changes nothing.
//   - Each turn lasts Server.DraftTurn (30 s). The player on turn calls draft.ban or draft.pick {"shaper":"PC_..."}; the
//     server checks it (that player's turn and kind, a shaper of the pool, not banned, not picked by either team) and
//     the next turn begins. Anything else is refused: the channel logs a connection's first faults, and the caller hears
//     the board again. A turn whose time runs out: a ban is skipped, a pick takes a random free shaper of the pool (the
//     server's generator, seeded from crypto/rand).
//   - draft.state (to everyone in the lobby who entered, at every change and to a late lobby.enter) is the whole board,
//     so a board can be drawn from the last one alone (DraftState).
//   - A team player who leaves the game (its leave, a lost session, a kick) cancels the draft: the roster is unlocked
//     as after a refused start (RosterLockedKey "0", the admins' redraw and lobby.startRefused), every ready mark is
//     reset, and draft.state says "cancelled" with the reason. The lobby stays; the seat can be refilled.
//   - While the draft runs a newcomer may only spectate, a team change is refused and the rules can't change.
//   - After the last turn the server starts the game itself (the --custom-autostart path: the host's startGame, then
//     NotifyGameStateChange IN_GAME to everyone), and each human's list in startGame holds only its drafted shaper
//     (applyPickRules); the host locks it at once, as All Random's (RULES3).
//
// A client without the board can't draft: its turns time out (no ban, a random pick).

// The draft's UI channel calls and event.
const (
	CallDraftBan    = "draft.ban"
	CallDraftPick   = "draft.pick"
	EventDraftState = "draft.state"
)

// DraftTurnDefault is a draft turn's time (the user's decision: 30 s).
const DraftTurnDefault = 30 * time.Second

// draftArgsMax is the longest draft.ban/draft.pick arguments taken.
const draftArgsMax = 256

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
	Player string `json:"player"` // the player on turn (a ban: the team's first player)
	Shaper string `json:"shaper"` // the result: a pool id; "" while to come or on, and for a skipped ban
	How    string `json:"how"`    // "" while to come or on, "chosen", or "timeout" (a skipped ban, a random pick)
}

// DraftYou is the receiver's place in the draft.
type DraftYou struct {
	Team int `json:"team"` // 1 or 2
	Seat int `json:"seat"` // its place in Teams' list, from 0
}

// DraftState is EventDraftState's data: the whole board.
type DraftState struct {
	Phase    string           `json:"phase"`            // "ban", "pick", "done" or "cancelled"
	Reason   string           `json:"reason,omitempty"` // cancelled: why (text for the board's .text)
	Turn     int              `json:"turn"`             // the current turn's index in Turns; -1 when none
	Left     int64            `json:"left"`             // ms left in the current turn when sent; 0 when none
	TurnMs   int64            `json:"turnMs"`           // a turn's full time
	Teams    [2][]string      `json:"teams"`            // each team's drafting players in seat order (team 1 first)
	Pool     []DraftShaper    `json:"pool"`             // the shapers to draft from
	Turns    []DraftStateTurn `json:"turns"`            // every turn of the draft, played or to come
	You      *DraftYou        `json:"you,omitempty"`    // the receiver's seat; absent for a spectator
	YourTurn bool             `json:"yourTurn"`         // the receiver is on turn now
}

// draftRun is a lobby's draft (guarded by the hostedGame's mutex).
type draftRun struct {
	board    *rules.Draft
	seats    [2][]int64 // each team's personas in seat order
	names    map[int64]string
	pool     []DraftShaper
	turn     time.Duration
	turnEnds time.Time
	gen      int // the current turn's number: a timer of an older turn does nothing
	rng      *rand.Rand
	seed     uint64
	timer    *time.Timer
	aid      *time.Timer // Server.TestDraft's
	phase    string      // "" while it runs, "done" or "cancelled"
	reason   string
	aidTried bool // the test aid's call out of turn was made
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

// onTurn is the persona on turn (0 when none).
func (d *draftRun) onTurn() int64 {
	t, ok := d.board.Current()
	if !ok || t.Seat >= len(d.seats[t.Team]) {
		return 0
	}
	return d.seats[t.Team][t.Seat]
}

// draftTurn is the server's turn time.
func (s *Server) draftTurn() time.Duration {
	if s.DraftTurn > 0 {
		return s.DraftTurn
	}
	return DraftTurnDefault
}

// draftPoolLocked is the board's pool: the shapers of the rules' pool this server offers, with their titles.
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
// from the lobby's roster, the pool, the first turn's timers. The roster is locked (the server sets RosterLockedKey "1"
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
	board, err := rules.NewDraft(ids, [2]int{len(seats[0]), len(seats[1])})
	if err != nil {
		return nil, err
	}
	seed := s.rollSeed()
	d := &draftRun{board: board, seats: seats, names: names, pool: pool, turn: s.draftTurn(), seed: seed,
		rng: rand.New(rand.NewPCG(seed, seed^0x52554C4553350000))} // "RULES5"
	hg.custom.draft, hg.custom.lastDraft = d, d
	var lock []tdf.Pair
	if attrValue(hg.game.Attrs, AttrRosterLocked) != "1" {
		lock = []tdf.Pair{tdf.KV(AttrRosterLocked, "1")}
		hg.game.Attrs = mergeAttrs(hg.game.Attrs, lock)
	}
	s.Log.Logf("blaze", "   game %d: the draft begins (RULES5b; seed %d, %d shapers in the pool, %d turns of %v): team 1 %v, "+
		"team 2 %v; the roster stays locked%s", hg.game.ID, seed, len(pool), len(board.Turns), d.turn, seats[0], seats[1],
		map[bool]string{true: " (RosterLockedKey \"1\" set by the server)", false: ""}[lock != nil])
	finish := hg.draftNextLocked(d)
	gid := hg.game.ID
	return func() {
		if lock != nil {
			note := GameAttribChangeNotification(gid, lock)
			hg.push(NotifyGameAttribChangeID, "NotifyGameAttribChange", func(*hostedPlayer) []byte { return note })
		}
		hg.publishDraft()
		if finish != nil {
			finish()
		}
	}, nil
}

// draftNextLocked begins the board's current turn (its timer, the test aid's), or, when the board is done, ends the
// draft and returns the start to run outside hg.mu (hg.mu held).
func (hg *hostedGame) draftNextLocked(d *draftRun) func() {
	s := hg.s
	d.gen++
	if d.timer != nil {
		d.timer.Stop()
	}
	if d.aid != nil {
		d.aid.Stop()
	}
	if d.board.Done() {
		return hg.draftDoneLocked(d)
	}
	gen := d.gen
	d.turnEnds = time.Now().Add(d.turn)
	d.timer = time.AfterFunc(d.turn, func() { hg.draftTimedOut(d, gen) })
	if s.TestDraft > 0 {
		d.aid = time.AfterFunc(s.TestDraft, func() { s.draftTestAid(hg, d, gen) })
	}
	return nil
}

// draftTimedOut ends turn gen of d by its time, unless it ended before.
func (hg *hostedGame) draftTimedOut(d *draftRun, gen int) {
	s := hg.s
	hg.mu.Lock()
	if hg.custom == nil || hg.custom.draft != d || d.gen != gen || hg.ended {
		hg.mu.Unlock()
		return
	}
	who := d.onTurn()
	turn, _ := d.board.Timeout(d.rng)
	what := "skipped"
	if turn.Kind == rules.DraftPick {
		what = "a random pick: " + turn.Shaper
		if turn.Shaper == "" {
			what = "no shaper left to pick"
		}
	}
	s.Log.Logf("blaze", "   game %d: draft turn %d/%d (%s, team %d, user %d) timed out: %s", hg.game.ID, d.board.Cur,
		len(d.board.Turns), turn.Kind, turn.Team+1, who, what)
	finish := hg.draftNextLocked(d)
	hg.mu.Unlock()
	hg.publishDraft()
	if finish != nil {
		finish()
	}
}

// parseDraftArgs reads draft.ban's and draft.pick's arguments: {"shaper":"PC_..."}, nothing else.
func parseDraftArgs(args json.RawMessage) (string, error) {
	if len(args) > draftArgsMax {
		return "", errors.New("arguments too long")
	}
	var a struct {
		Shaper *string `json:"shaper"`
	}
	d := json.NewDecoder(bytes.NewReader(args))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return "", fmt.Errorf("arguments: %v", err)
	}
	if d.More() {
		return "", errors.New("arguments: more than one value")
	}
	if a.Shaper == nil || *a.Shaper == "" {
		return "", errors.New("no shaper in the arguments")
	}
	return *a.Shaper, nil
}

// draftCall is user's draft.ban or draft.pick (kind) of shaper. Only a drafter of a running draft, on turn, with a
// free shaper of the pool; a refusal changes nothing and the caller hears the board again.
func (s *Server) draftCall(user int64, kind, shaper string) error {
	hg := s.lobbyOf(user)
	if hg == nil {
		return errors.New("not in a lobby")
	}
	hg.mu.Lock()
	d := hg.custom.draft
	refuse := func(why string) error {
		hg.mu.Unlock()
		hg.sendDraftTo(user)
		return errors.New(why)
	}
	if d == nil {
		return refuse("no draft runs")
	}
	team, seat, ok := d.seatOf(user)
	if !ok {
		return refuse("not a drafter (a spectator, or not on a team at the draft's start)")
	}
	if err := d.board.Act(team, seat, kind, shaper); err != nil {
		return refuse(fmt.Sprintf("%s %s: %v", kind, clipText(shaper), err))
	}
	s.Log.Logf("blaze", "   game %d: draft turn %d/%d: user %d (team %d) %ss %s", hg.game.ID, d.board.Cur,
		len(d.board.Turns), user, team+1, kind, shaper)
	finish := hg.draftNextLocked(d)
	hg.mu.Unlock()
	hg.publishDraft()
	if finish != nil {
		finish()
	}
	return nil
}

// clipText is a client's text for the log, at most 40 bytes.
func clipText(v string) string {
	if len(v) > 40 {
		v = v[:40] + "..."
	}
	return fmt.Sprintf("%q", v)
}

// draftTestAid (Server.TestDraft) acts for the player on turn of d's turn gen, through draftCall as its call would:
// a ban of the pool's last free shaper, a pick of its first. Its first action also calls once as a drafter who isn't
// on turn (if there is one), which must be refused.
func (s *Server) draftTestAid(hg *hostedGame, d *draftRun, gen int) {
	hg.mu.Lock()
	if hg.custom == nil || hg.custom.draft != d || d.gen != gen || hg.ended {
		hg.mu.Unlock()
		return
	}
	who := d.onTurn()
	t, _ := d.board.Current()
	shaper := ""
	for i := range d.pool {
		k := i
		if t.Kind == rules.DraftBan {
			k = len(d.pool) - 1 - i
		}
		if d.board.Free(d.pool[k].ID) == nil {
			shaper = d.pool[k].ID
			break
		}
	}
	var other int64
	if !d.aidTried {
		d.aidTried = true
		for _, ids := range d.seats {
			for _, id := range ids {
				if id != who && other == 0 {
					other = id
				}
			}
		}
	}
	gid := hg.game.ID
	hg.mu.Unlock()
	if other != 0 {
		err := s.draftCall(other, t.Kind, shaper)
		s.Log.Logf("blaze", "   game %d: draft (--test-draft, a test aid): user %d's %s out of turn: %v", gid, other, t.Kind, err)
	}
	if who == 0 || shaper == "" {
		return
	}
	s.Log.Logf("blaze", "   game %d: draft (--test-draft, a test aid): user %d %ss %s", gid, who, t.Kind, shaper)
	if err := s.draftCall(who, t.Kind, shaper); err != nil {
		s.Log.Logf("blaze", "   game %d: draft (--test-draft): refused: %v", gid, err)
	}
}

// draftDoneLocked ends d after its last turn (hg.mu held) and returns the game's start, run outside hg.mu: the rules'
// last check, then the --custom-autostart path with each human's drafted shaper. Findings cancel the draft instead.
func (hg *hostedGame) draftDoneLocked(d *draftRun) func() {
	s := hg.s
	hg.custom.draft = nil
	if hg.ended || hg.custom.started {
		d.phase = "cancelled"
		return nil
	}
	fs, rl, fill := hg.startRulesLocked()
	if len(fs) > 0 {
		d.phase, d.reason = "cancelled", "The rules can't be played: "+rules.DescribeFindings(fs)
		tell := hg.cancelDraftLocked()
		gid := hg.game.ID
		return func() {
			s.Log.Logf("blaze", "   game %d: the draft ended, but the rules' findings refuse the start (%s); cancelled",
				gid, rules.DescribeFindings(fs))
			tell()
		}
	}
	d.phase = "done"
	picks := map[int64]string{}
	var desc []string
	board := d.board.Picks()
	for team, ids := range d.seats {
		for seat, id := range ids {
			if seat < len(board[team]) && board[team][seat] != "" {
				picks[id] = board[team][seat]
				desc = append(desc, fmt.Sprintf("user %d (team %d) %s", id, team+1, board[team][seat]))
			}
		}
	}
	hg.custom.started = true
	roster := lobbyRoster(hg.players, hg.game.Attrs)
	players := append([]*hostedPlayer(nil), hg.players...)
	mt := hg.match
	gid := hg.game.ID
	return func() {
		s.Log.Logf("blaze", "   game %d: the draft is done: %s; bans %v", gid, strings.Join(desc, ", "), d.board.Bans())
		s.Log.Logf("blaze", "   game %d starts (the draft ended): roster %s%s", gid, describeRoster(roster),
			describeStartRules(rl, fill))
		if mt != nil {
			s.lobbyContent(roster, players)
			sg := gmshost.StartGame{Roster: roster, Rules: rl, FillTeams: fill}
			s.applyPickRules(gid, &sg, picks)
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
	d.phase, d.reason = "cancelled", why
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
	d.gen++
	if d.timer != nil {
		d.timer.Stop()
	}
	if d.aid != nil {
		d.aid.Stop()
	}
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

// draftStatesLocked are the board's events (hg.mu held): to only, or (0) to everyone in the lobby who entered; nil
// without a draft.
func (hg *hostedGame) draftStatesLocked(only int64) map[int64]DraftState {
	d := hg.custom.lastDraft
	if d == nil {
		return nil
	}
	base := DraftState{Phase: d.phase, Reason: d.reason, Turn: -1, TurnMs: d.turn.Milliseconds(), Pool: d.pool}
	if base.Phase == "" {
		base.Phase = d.board.Phase()
	}
	if d.phase == "" && !d.board.Done() {
		base.Turn = d.board.Cur
		base.Left = max(time.Until(d.turnEnds).Milliseconds(), 0)
	}
	for team, ids := range d.seats {
		base.Teams[team] = []string{}
		for _, id := range ids {
			base.Teams[team] = append(base.Teams[team], d.names[id])
		}
	}
	for _, t := range d.board.Turns {
		player := ""
		if t.Seat < len(d.seats[t.Team]) {
			player = d.names[d.seats[t.Team][t.Seat]]
		}
		base.Turns = append(base.Turns, DraftStateTurn{Kind: t.Kind, Team: t.Team + 1, Player: player, Shaper: t.Shaper, How: t.How})
	}
	if base.Pool == nil {
		base.Pool = []DraftShaper{}
	}
	if base.Turns == nil {
		base.Turns = []DraftStateTurn{}
	}
	onTurn := int64(0)
	if base.Turn >= 0 {
		onTurn = d.onTurn()
	}
	out := map[int64]DraftState{}
	for _, hp := range hg.players {
		id := hp.p.ID
		if hp.removed || !hg.custom.check.Has(id) || only != 0 && id != only {
			continue
		}
		st := base
		if team, seat, ok := d.seatOf(id); ok {
			st.You = &DraftYou{Team: team + 1, Seat: seat}
		}
		st.YourTurn = onTurn != 0 && id == onTurn
		out[id] = st
	}
	return out
}

// publishDraft tells everyone in the lobby who entered the board as it is now (its snapshot and its sends one step,
// hg.lobbySend, so nobody hears an older board after a newer one).
func (hg *hostedGame) publishDraft() { hg.sendDraft(0) }

// sendDraftTo tells only user the board (a refused call, a late lobby.enter), also the last one after a cancel.
func (hg *hostedGame) sendDraftTo(user int64) { hg.sendDraft(user) }

func (hg *hostedGame) sendDraft(only int64) {
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
	states := hg.draftStatesLocked(only)
	hg.mu.Unlock()
	for id, st := range states {
		s.UI.Send(id, EventDraftState, st)
	}
}
