package blaze

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"dawngate/server/internal/rules"
	"dawngate/server/internal/uichannel"
)

// Army Pick and Clone Pick (ARMY1; the user's decisions of 2026-11-12): a custom game whose pick rule is "army" or
// "clone" votes in the lobby before shaper select. It is
// the draft's frame (draft.go: START holds the start with the roster locked, a leaver cancels in any phase, a newcomer
// only spectates, the rules stay, one step timer at a time with its gen, draft.state to everyone who entered, the start
// through draftDoneLocked and applyDraft), with a vote in place of the board:
//   - The vote (Server.VoteTime, 30 s): every human of a team votes for one shaper of the pool the board shows
//     (draft.vote, changeable) and may lock it (draft.lock, final); the vote ends at its time, or at once when every
//     voter has locked.
//   - Who sees the votes: Army Pick the voter's own team (DraftState.Own.Votes, the team's send alone), Clone Pick
//     everyone (DraftState.Vote.Votes, also the spectators).
//   - The draw (rules.DrawVote, the draft's generator from the server's seed): Army one of each team's votes for that
//     team (a team without votes: a random shaper of the pool; both teams may get the same), Clone one of all the votes
//     for everyone.
//   - The result (phase "done" with Vote.Drawn) stays up for Server.VoteShowTime (3 s), then the game starts: each
//     human's list is its drawn shaper alone (applyDraft), locked at once by the host, which lets a team hold it
//     several times (startGame's pick "army" or "clone").
//   - No roles, bans, hovers, swaps, trades or ready marks: those calls are refused during a vote.

// The vote's calls.
const (
	CallDraftVote = "draft.vote" // ARMY1
	CallDraftLock = "draft.lock" // ARMY1
)

// DraftPhaseVote is the vote's phase on the wire (its result is DraftPhaseDone with DraftState.Vote.Drawn).
const DraftPhaseVote = "vote"

// The vote's times (the user's 30 s; the drawn shaper shown about 3 s).
const (
	VoteTimeDefault = 30 * time.Second
	VoteShowDefault = 3 * time.Second
)

// The stages of a running vote (draftRun.stage).
const (
	draftStageVote  = "vote"
	draftStageDrawn = "drawn"
)

// DraftVote is a vote's public part of draft.state (absent in a draft).
type DraftVote struct {
	Kind string `json:"kind"` // "army" or "clone"
	// Votes and Locked are every voter's vote ("" none) and lock, by team and seat of Teams: Clone Pick only (everyone
	// sees everyone's); absent in Army Pick, whose votes only the own team sees (DraftOwn.Votes, Locked).
	Votes  *[2][]string `json:"votes,omitempty"`
	Locked *[2][]bool   `json:"locked,omitempty"`
	// Drawn is each team's drawn shaper once the vote is over (Clone: the same twice), "" before and for a team
	// without voters; How how it was drawn: "vote" (one of the votes), "random" (no votes: a random shaper of the
	// pool), "" before.
	Drawn [2]string `json:"drawn"`
	How   [2]string `json:"how"`
}

// voteRun is a running vote's state (draftRun.vote; guarded by the hostedGame's mutex).
type voteRun struct {
	kind   string           // rules.PickArmy or rules.PickClone
	votes  map[int64]string // each voter's vote
	locked map[int64]bool
	drawn  [2]string
	how    [2]string
}

// voteTime and voteShowTime are the server's vote and result times.
func (s *Server) voteTime() time.Duration {
	if s.VoteTime > 0 {
		return s.VoteTime
	}
	return VoteTimeDefault
}

func (s *Server) voteShowTime() time.Duration {
	if s.VoteShowTime > 0 {
		return s.VoteShowTime
	}
	return VoteShowDefault
}

// pickName is the pick rule's name for the log ("Army Pick").
func pickName(pick string) string {
	for _, c := range rules.Categories() {
		if c.ID == rules.Pick {
			for _, o := range c.Options {
				if o.ID == pick {
					return o.Name
				}
			}
		}
	}
	return pick
}

// beginVoteLocked starts the lobby's vote at its admin's START (hg.mu held; the rules passed their check), as
// beginDraftLocked a draft: seats is each team's humans in the lobby's seat order (no shuffle: the order changes
// nothing in a vote); lockNote ends the log line.
func (hg *hostedGame) beginVoteLocked(kind string, seats [2][]int64, names map[int64]string, pool []DraftShaper,
	lockNote string) *draftRun {
	s := hg.s
	seed := s.rollSeed()
	d := &draftRun{seats: seats, names: names, pool: pool, turn: s.voteTime(), stage: draftStageVote, window: -1,
		seed: seed, rng: draftRNG(seed), roles: map[int64]string{}, hovers: map[int64]string{}, ready: map[int64]bool{},
		declined: map[draftAskKey]bool{},
		vote:     &voteRun{kind: kind, votes: map[int64]string{}, locked: map[int64]bool{}}}
	s.Log.Logf("blaze", "   game %d: the vote begins (ARMY1; %s, seed %d, %d shapers in the pool, %v, the drawn shaper "+
		"shown %v): team 1 %v, team 2 %v; the roster stays locked%s", hg.game.ID, pickName(kind), seed, len(pool),
		d.turn, s.voteShowTime(), seats[0], seats[1], lockNote)
	return d
}

// voteTell is whom a change of user's vote or lock tells: Army its team (1 or 2), Clone everyone (0).
func (d *draftRun) voteTell(team int) int {
	if d.vote.kind == rules.PickArmy {
		return team + 1
	}
	return 0
}

// inPool reports whether shaper is one of the board's (the pool the vote offers).
func (d *draftRun) inPool(shaper string) bool {
	return slices.ContainsFunc(d.pool, func(p DraftShaper) bool { return p.ID == shaper })
}

// draftVoteCall is user's draft.vote: in the vote, before its lock, of a shaper of the pool; changeable. Army its team
// hears it, Clone everyone; the same vote again tells nobody. Votes aren't logged one by one (they may change often):
// the draw's line lists them.
func (s *Server) draftVoteCall(user int64, shaper string) error {
	return s.draftDo(user, func(hg *hostedGame, d *draftRun, team, _ int) (draftTell, error) {
		switch {
		case d.vote == nil:
			return draftTell{}, errors.New("vote: a draft runs, not a vote")
		case d.stage != draftStageVote:
			return draftTell{}, fmt.Errorf("vote: %s", d.stageWhy())
		case d.vote.locked[user]:
			return draftTell{}, errors.New("vote: this player has locked in")
		case !d.inPool(shaper):
			return draftTell{}, fmt.Errorf("vote %s: not in the pool", clipText(shaper))
		case d.vote.votes[user] == shaper:
			return draftTell{quiet: true}, nil
		}
		d.vote.votes[user] = shaper
		return draftTell{team: d.voteTell(team)}, nil
	})
}

// draftLockCall is user's draft.lock: its vote made final (it needs one); once every voter has locked the vote ends
// at once and the shapers are drawn. A second lock changes nothing.
func (s *Server) draftLockCall(user int64) error {
	return s.draftDo(user, func(hg *hostedGame, d *draftRun, team, _ int) (draftTell, error) {
		switch {
		case d.vote == nil:
			return draftTell{}, errors.New("lock: a draft runs, not a vote")
		case d.stage != draftStageVote:
			return draftTell{}, fmt.Errorf("lock: %s", d.stageWhy())
		case d.vote.locked[user]:
			return draftTell{quiet: true}, nil
		case d.vote.votes[user] == "":
			return draftTell{}, errors.New("Vote for a shaper first.")
		}
		d.vote.locked[user] = true
		n, all := 0, d.drafters()
		for _, id := range all {
			if d.vote.locked[id] {
				n++
			}
		}
		s.Log.Logf("blaze", "   game %d: vote: user %d (team %d) locks %s (%d/%d)", hg.game.ID, user, team+1,
			d.vote.votes[user], n, len(all))
		if n < len(all) {
			return draftTell{team: d.voteTell(team)}, nil
		}
		hg.drawVoteLocked(d, "every voter has locked: the vote ends early")
		return draftTell{}, nil
	})
}

// drawVoteLocked ends d's vote (why, for the log) and draws (hg.mu held): Army per team with voters one of the team's
// votes, Clone one of everyone's for both teams; then the result's step (Server.VoteShowTime), whose end starts the
// game (draftTimedOut).
func (hg *hostedGame) drawVoteLocked(d *draftRun, why string) {
	v := d.vote
	ids := make([]string, len(d.pool))
	for i, p := range d.pool {
		ids[i] = p.ID
	}
	votesOf := func(teams ...int) []string {
		var out []string
		for _, team := range teams {
			for _, id := range d.seats[team] {
				out = append(out, v.votes[id])
			}
		}
		return out
	}
	how := func(fromVote bool) string { return map[bool]string{true: "vote", false: "random"}[fromVote] }
	var desc []string
	if v.kind == rules.PickClone {
		all := votesOf(0, 1)
		shaper, fromVote := rules.DrawVote(all, ids, d.rng)
		v.drawn, v.how = [2]string{shaper, shaper}, [2]string{how(fromVote), how(fromVote)}
		desc = append(desc, "everyone "+describeDraw(shaper, fromVote, all))
	} else {
		for team := range d.seats {
			if len(d.seats[team]) == 0 {
				continue
			}
			votes := votesOf(team)
			shaper, fromVote := rules.DrawVote(votes, ids, d.rng)
			v.drawn[team], v.how[team] = shaper, how(fromVote)
			desc = append(desc, fmt.Sprintf("team %d %s", team+1, describeDraw(shaper, fromVote, votes)))
		}
	}
	hg.s.Log.Logf("blaze", "   game %d: the vote is over (%s): %s; shown %v, then the game starts", hg.game.ID, why,
		strings.Join(desc, "; "), hg.s.voteShowTime())
	d.stage = draftStageDrawn
	hg.draftStepLocked(d, hg.s.voteShowTime())
}

// describeDraw is a draw for the log: "draws PC_X (votes PC_X 2, PC_Y 1)" or "draws PC_X at random (no votes)".
func describeDraw(shaper string, fromVote bool, votes []string) string {
	if !fromVote {
		if shaper == "" {
			return "draws nothing (no votes, an empty pool)"
		}
		return "draws " + shaper + " at random from the pool (no votes)"
	}
	count := map[string]int{}
	for _, v := range votes {
		if v != "" {
			count[v]++
		}
	}
	var parts []string
	for v, n := range count {
		parts = append(parts, fmt.Sprintf("%s %d", v, n))
	}
	sort.Strings(parts)
	return fmt.Sprintf("draws %s (votes %s)", shaper, strings.Join(parts, ", "))
}

// voteDrafted is each human's drawn shaper for the start (no role: shaper select's role pages as usual) and the
// draw's description for the log.
func (d *draftRun) voteDrafted() (map[int64]draftedSeat, []string) {
	drafted := map[int64]draftedSeat{}
	var desc []string
	for team, ids := range d.seats {
		for _, id := range ids {
			if sh := d.vote.drawn[team]; sh != "" {
				drafted[id] = draftedSeat{Shaper: sh}
				desc = append(desc, fmt.Sprintf("user %d (team %d) %s", id, team+1, sh))
			}
		}
	}
	return drafted, desc
}

// voteStateLocked fills a vote's parts of base, the public board (hg.mu held): the phase, Vote, and Clone's votes.
func (d *draftRun) voteStateLocked(base *DraftState) {
	v := d.vote
	dv := &DraftVote{Kind: v.kind, Drawn: v.drawn, How: v.how}
	if v.kind == rules.PickClone {
		var votes [2][]string
		var locked [2][]bool
		for team, ids := range d.seats {
			votes[team], locked[team] = make([]string, len(ids)), make([]bool, len(ids))
			for seat, id := range ids {
				votes[team][seat], locked[team][seat] = v.votes[id], v.locked[id]
			}
		}
		dv.Votes, dv.Locked = &votes, &locked
	}
	base.Vote = dv
	if d.running() {
		base.Phase = DraftPhaseVote
		if d.stage == draftStageDrawn {
			base.Phase = DraftPhaseDone
		}
	}
}

// ownVoteLocked adds team's votes and locks to its private part (a vote only).
func (d *draftRun) ownVoteLocked(own *DraftOwn, team int) {
	ids := d.seats[team]
	own.Votes, own.Locked = make([]string, len(ids)), make([]bool, len(ids))
	for seat, id := range ids {
		own.Votes[seat], own.Locked[seat] = d.vote.votes[id], d.vote.locked[id]
	}
}

// voteTestAid is the test aid's part in a vote (Server.TestDraft): every voter who hasn't locked votes and locks, seat
// k of team t the pool's shaper (k + t) mod the pool's size (so a 1 v 1 votes the pool's first two and Army's teams
// differ), through the players' calls.
func (s *Server) voteTestAid(hg *hostedGame, d *draftRun) func() {
	type vote struct {
		who    int64
		shaper string
	}
	var votes []vote
	if d.stage == draftStageVote && len(d.pool) > 0 {
		for team, ids := range d.seats {
			for seat, id := range ids {
				if !d.vote.locked[id] {
					votes = append(votes, vote{id, d.pool[(seat+team)%len(d.pool)].ID})
				}
			}
		}
	}
	gid := hg.game.ID
	return func() {
		for _, v := range votes {
			s.Log.Logf("blaze", "   game %d: vote (--test-draft, a test aid): user %d votes %s (%s) and locks (%s)", gid,
				v.who, v.shaper, errText(s.draftVoteCall(v.who, v.shaper)), errText(s.draftLockCall(v.who)))
		}
	}
}

// attachVoteCalls registers the vote's calls on ui (attachDraftCalls).
func (s *Server) attachVoteCalls(ui *uichannel.Server) {
	ui.Handle(CallDraftVote, func(user int64, args json.RawMessage) error {
		shaper, err := parseDraftArgs(args)
		if err != nil {
			return err
		}
		return s.draftVoteCall(user, shaper)
	})
	ui.Handle(CallDraftLock, func(user int64, args json.RawMessage) error {
		if err := parseDraftNone(args); err != nil {
			return err
		}
		return s.draftLockCall(user)
	})
}
