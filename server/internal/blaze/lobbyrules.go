package blaze

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/rules"
)

// A custom game's match rules and bots setting (M26; package rules has the catalog, the presets and the checks). Each
// custom game with a match host holds a rules combination and whether bots fill the open seats, from its create to its
// match host:
//
//   - The create gives the starting values: the standard game (dgserver --test-rules: that combination), and bots when
//     the create dialog's checkbox put BotFillMarker into the name (or dgserver --custom-bots).
//   - In the lobby, over the UI channel: lobby.state carries them (LobbyRules: the options, the preset, the text, the
//     bots, the findings and whether the receiver may change them), lobby.rulesCatalog the whole catalog (at lobby.enter),
//     and the admin's call lobby.setRules changes them. A change resets every ready mark and renames the game in the
//     browser (its prefix); it is refused for anyone but the admin, once START was pressed (the game started, or its
//     roster is locked), within lobbyRulesGap of the last change, and for a host that can't take it (below).
//   - START: the rules are checked again (a change raced against START can't slip through); findings refuse the start
//     as the ready check does. The host's startGame carries the rules (omitted for the standard game) and the bots
//     setting as fillTeams.
//   - The host's capability: a host names gmshost.FeatureRules in its hello. One that doesn't (an older build, e.g. on a
//     remote host machine) would ignore the fields, so such a game keeps the standard game and createGame's bots: a
//     change is refused, and rules set anyway (--test-rules) are a finding that refuses the start.
//   - The match: unrated (Server.unratedRules: no Leaderboard count, no rating change), the history's row has the rules,
//     and the players get one "System" chat line with them 10 s after the match begins (announceRulesLater).
//
// A practice group's game and a lobby without a match host have no rules: lobby.state leaves them out.

// The UI channel's call and event of the rules.
const (
	CallLobbySetRules      = "lobby.setRules"
	EventLobbyRulesCatalog = "lobby.rulesCatalog"
)

// lobbyRulesGap is the least time between two rules changes of one lobby: each change tells every player of the lobby
// and every open game browser (the prefix), so a modified client's bursts within the channel's call limit can't make the
// server tell them ten times a second.
const lobbyRulesGap = 250 * time.Millisecond

// LobbyRules is lobby.state's rules part (omitted for a game without rules).
type LobbyRules struct {
	Options  rules.Combination `json:"options"`  // one option per category
	Preset   string            `json:"preset"`   // the preset these options are, "" for none
	Text     string            `json:"text"`     // rules.Describe: "All Random, Shop on Death", "Standard"
	Bots     bool              `json:"bots"`     // bots fill the open seats at the start
	Findings []rules.Finding   `json:"findings"` // why the game can't start like this ([] when it can)
	CanEdit  bool              `json:"canEdit"`  // the receiver may call lobby.setRules now
	Host     bool              `json:"host"`     // the game's match host takes rules (false: an older build)
}

// rulesOnLocked reports whether the game has rules: a custom game the browser lists, with a match host (hg.mu held).
func (hg *hostedGame) rulesOnLocked() bool {
	return hg.custom != nil && hg.custom.browsable && hg.match != nil
}

// hostRulesLocked reports whether the game's match host takes rules (its hello's features; hg.mu held).
func (hg *hostedGame) hostRulesLocked() bool {
	return hg.match != nil && hg.match.Supports(gmshost.FeatureRules)
}

// rulesLockedLocked says why the rules can't change any more ("" = they can; hg.mu held): START was pressed.
func (hg *hostedGame) rulesLockedLocked() string {
	switch {
	case hg.custom.started || hg.ended:
		return "the game started"
	case hg.custom.draft != nil: // RULES5b
		return "a draft runs"
	case attrValue(hg.game.Attrs, AttrRosterLocked) == "1": // the admin's START locks the roster first [assumed: only START]
		return "its roster is locked (START was pressed)"
	}
	return ""
}

// rulesContextLocked is the checks' view of the game (hg.mu held): the bots, the most seats a team leaves to them, the
// host's capability.
func (hg *hostedGame) rulesContextLocked() rules.Context {
	ctx := rules.Context{Bots: hg.custom.bots, HostRules: hg.hostRulesLocked(), TeamSize: int(hg.game.TeamCap),
		Catalog: hg.s.rulesPools(), NoShaperSelect: !hg.s.shaperSelectRuns()} // RULES3: the pools and shaper select
	if ctx.Bots {
		var humans [2]int64
		for _, hp := range hg.players {
			if !hp.removed && (hp.p.Team == 0 || hp.p.Team == 1) {
				humans[hp.p.Team]++
			}
		}
		ctx.BotSeats = int(max(hg.game.TeamCap-humans[0], hg.game.TeamCap-humans[1], 0))
	}
	return ctx
}

// rulesFindingsLocked are the game's findings now (hg.mu held): the combination's checks, and a bots setting other than
// createGame's on a host that can't take startGame's fillTeams.
func (hg *hostedGame) rulesFindingsLocked() []rules.Finding {
	ctx := hg.rulesContextLocked()
	fs := rules.Check(hg.custom.rules, ctx)
	if !ctx.HostRules && hg.custom.bots != hg.custom.botsAtCreate {
		fs = append(fs, rules.Finding{Category: rules.FindingHost, Message: "This game's match host is an older build " +
			"that keeps the bots setting of the game's creation."})
	}
	return fs
}

// lobbyRulesLocked is lobby.state's rules part for receiver (nil: the game has no rules; hg.mu held).
func (hg *hostedGame) lobbyRulesLocked(receiver int64, fs []rules.Finding) *LobbyRules {
	if !hg.rulesOnLocked() {
		return nil
	}
	c := hg.custom.rules
	lr := &LobbyRules{Options: c, Text: rules.Describe(c), Bots: hg.custom.bots, Findings: fs,
		Host: hg.hostRulesLocked()}
	if lr.Findings == nil {
		lr.Findings = []rules.Finding{}
	}
	if p, ok := rules.PresetOf(c); ok {
		lr.Preset = p.ID
	}
	lr.CanEdit = hg.isAdminLocked(receiver) && hg.rulesLockedLocked() == "" && lr.Host
	return lr
}

// lobbyName is a custom game's name in the browser: the map's label, [Bots] while bots fill the open seats, the rules'
// prefix (rules.Prefix), then the name its creator gave it.
func lobbyName(label string, bots bool, c rules.Combination, base string) string {
	name := label
	if bots {
		name += BotFillPrefix
	}
	return name + rules.Prefix(c) + base
}

// renameLocked gives the game the name its lobby's settings make; true when it changed (hg.mu held).
func (hg *hostedGame) renameLocked() bool {
	name := lobbyName(hg.custom.label, hg.custom.bots, hg.custom.rules, hg.custom.baseName)
	if name == hg.game.Name {
		return false
	}
	hg.game.Name = name
	return true
}

// setRulesArgs are lobby.setRules' arguments: the whole combination (rules) or a preset's id (preset), and/or the bots
// setting (bots; absent = unchanged). Anything else is refused.
type setRulesArgs struct {
	Rules  json.RawMessage `json:"rules"`
	Preset *string         `json:"preset"`
	Bots   *bool           `json:"bots"`
}

// parseSetRules reads lobby.setRules' arguments: (nil, nil) for a call that changes no rules (bots only).
func parseSetRules(args json.RawMessage) (*rules.Combination, *bool, error) {
	if len(args) > 2*rules.MaxJSON {
		return nil, nil, errors.New("arguments too long")
	}
	var a setRulesArgs
	d := json.NewDecoder(bytes.NewReader(args))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return nil, nil, fmt.Errorf("arguments: %v", err)
	}
	if d.More() {
		return nil, nil, errors.New("arguments: more than one value")
	}
	var c *rules.Combination
	switch {
	case len(a.Rules) > 0 && string(a.Rules) != "null" && a.Preset != nil:
		return nil, nil, errors.New("both rules and a preset")
	case len(a.Rules) > 0 && string(a.Rules) != "null":
		v, err := rules.ParseJSON(a.Rules)
		if err != nil {
			return nil, nil, err
		}
		c = &v
	case a.Preset != nil:
		p, ok := rules.FindPreset(*a.Preset)
		if !ok {
			return nil, nil, fmt.Errorf("%w preset", rules.ErrUnknown)
		}
		c = &p.Options
	}
	if c == nil && a.Bots == nil {
		return nil, nil, errors.New("neither rules, a preset nor bots")
	}
	return c, a.Bots, nil
}

// lobbySetRules is the admin's lobby.setRules: the lobby's rules and bots. Only the admin of a custom game with rules,
// before START, not within the gap, and only what the host can take; a change resets every ready mark, renames the game
// in the browser and tells the lobby. A refused call changes nothing (the caller hears the state again).
func (s *Server) lobbySetRules(user int64, args json.RawMessage) error {
	c, bots, err := parseSetRules(args)
	if err != nil {
		return err
	}
	hg := s.lobbyOf(user)
	if hg == nil {
		return errors.New("no announced lobby (none, or its host is starting)")
	}
	hg.mu.Lock()
	refuse := func(why string) error {
		hg.mu.Unlock()
		hg.sendLobbyState(user)
		return errors.New(why)
	}
	switch {
	case !hg.rulesOnLocked():
		return refuse("a game without rules (a practice group's, or without a match host)")
	case !hg.isAdminLocked(user):
		return refuse("not the game's admin")
	case hg.rulesLockedLocked() != "":
		return refuse("the rules are locked: " + hg.rulesLockedLocked())
	case s.rulesGap > 0 && time.Since(hg.custom.rulesSetAt) < s.rulesGap:
		return refuse(fmt.Sprintf("a change within %v of the last", s.rulesGap))
	}
	next, nextBots := hg.custom.rules, hg.custom.bots
	if c != nil {
		next = *c
	}
	if bots != nil {
		nextBots = *bots
	}
	if !hg.hostRulesLocked() && (!next.IsDefault() || nextBots != hg.custom.botsAtCreate) {
		return refuse("the match host is an older build without rules (its hello lacks " + gmshost.FeatureRules + ")")
	}
	if next == hg.custom.rules && nextBots == hg.custom.bots {
		hg.mu.Unlock()
		hg.sendLobbyState(user) // the same again: only the caller hears it
		return nil
	}
	hg.custom.rules, hg.custom.bots, hg.custom.rulesSetAt = next, nextBots, time.Now()
	for _, hp := range hg.players {
		hg.custom.check.Set(hp.p.ID, false) // a change resets every ready mark
	}
	renamed := hg.renameLocked()
	findings := hg.rulesFindingsLocked()
	gid := hg.game.ID
	hg.mu.Unlock()
	note := "none"
	if len(findings) > 0 {
		note = rules.DescribeFindings(findings)
	}
	s.Log.Logf("blaze", "   game %d: rules set by admin %d: %s, bots %v; every ready mark reset; findings: %s", gid, user,
		rules.Describe(next), nextBots, note)
	hg.publishLobby()
	if renamed {
		s.browserChanged(hg)
	}
	return nil
}

// startRulesLocked checks the rules again at the start (hg.mu held): the findings that refuse it, and otherwise what
// startGame carries for them (nil, nil for a host without rules or a game without them).
func (hg *hostedGame) startRulesLocked() (fs []rules.Finding, r *gmshost.Rules, fill *int) {
	if !hg.rulesOnLocked() {
		return nil, nil, nil
	}
	if fs = hg.rulesFindingsLocked(); len(fs) > 0 {
		return fs, nil, nil
	}
	if !hg.hostRulesLocked() {
		return nil, nil, nil // the standard game and createGame's bots: nothing to send
	}
	if !hg.custom.rules.IsDefault() {
		w := gmshost.Rules(hg.custom.rules.Normal())
		r = &w
	}
	n := 0
	if hg.custom.bots {
		n = int(hg.game.TeamCap)
	}
	hg.fillTeams = n // the result's and the home page's bot count
	return nil, r, &n
}

// describeStartRules is the start's log text for startGame's rules and bots ("" when it carries none).
func describeStartRules(r *gmshost.Rules, fill *int) string {
	if fill == nil {
		return ""
	}
	text := "the standard game"
	if r != nil {
		text = "rules " + rules.Describe(rules.Combination(*r))
	}
	return fmt.Sprintf("; %s, fillTeams %d", text, *fill)
}

// unratedRulesLocked reports whether the game's rules make it unrated (any category away from its default; hg.mu held).
func (hg *hostedGame) unratedRulesLocked() bool {
	return hg.custom != nil && !hg.custom.rules.IsDefault()
}

// rulesRecordLocked is the match history's rules column: the combination's JSON, "" for the standard game (hg.mu held).
func (hg *hostedGame) rulesRecordLocked() string {
	if !hg.unratedRulesLocked() {
		return ""
	}
	b, err := json.Marshal(hg.custom.rules.Normal())
	if err != nil {
		return ""
	}
	return string(b)
}

// RulesNoteID is the broadcast record id of the line with a match's rules (sysmsg.go's ids 1-16 are the operator's,
// OnlineCountID 100, MapNoteID 101).
const RulesNoteID = 102

// rulesNoteSeconds is how long the client keeps the record [our choice]: it is shown at once.
const rulesNoteSeconds = 600

// rulesNoteDelay is the default of Server.RulesNoteDelay [our choice; RULES1b]: the client puts a System line into the
// match's chat only once its in-match chat window is up, and a line that comes earlier is lost for good (below).
const rulesNoteDelay = 10 * time.Second

// announceRulesLater starts the rules line's timer at the host's first inGame (announceRules, once). RULES1b
// (rules1b_line2b): sent in the same millisecond as the host's EnterGame, the line never showed. The client's path
// (B14's flag 2, GameUI_UpdateBroadcastMessages 0x004B5DE0) marks the record shown and calls
// ChatUI_AddSystemChatLine (0x0049B1E0), whose only in-match output is the chat movie's
// ChatManager.OnGameSystemMessage (0x004961F0): that reaches only an activated GameChatWindow (or the post-game
// lobby's), and BottomBarUI.HandleMessage, its "System" window's path, drops every line in in-game mode. At
// EnterGame no GameChatWindow is activated yet, so the line went nowhere.
func (hg *hostedGame) announceRulesLater() {
	hg.mu.Lock()
	if hg.custom == nil || hg.custom.rules.IsDefault() || hg.custom.announced || hg.custom.announceDue {
		hg.mu.Unlock()
		return
	}
	hg.custom.announceDue = true
	gid := hg.game.ID
	hg.mu.Unlock()
	d := hg.s.RulesNoteDelay
	if d <= 0 {
		d = rulesNoteDelay
	}
	hg.s.Log.Logf("blaze", "   game %d: the match begins; its rules go out as a System chat line in %s", gid, d)
	time.AfterFunc(d, hg.announceRules)
}

// announceRules sends every player of a custom game with rules, spectators too, one "System" chat line with them once
// the match runs (announceRulesLater): "Rules: All Random, Shop on Death". It is the client's own system line (a
// broadcast record shown in the match's chat, BroadcastChatLine; sysmsg.go), not a message in a chat channel, so no
// player can be mistaken for its sender. The client shows a record's text once, so a text this client already showed
// gets a number.
func (hg *hostedGame) announceRules() {
	s := hg.s
	hg.mu.Lock()
	if hg.custom == nil || hg.custom.rules.IsDefault() || hg.custom.announced {
		hg.mu.Unlock()
		return
	}
	hg.custom.announced = true
	hg.custom.announceDue = false
	text := "Rules: " + rules.Describe(hg.custom.rules)
	var to []*Conn
	for _, hp := range hg.players {
		if !hp.removed && hp.conn != nil {
			to = append(to, hp.conn)
		}
	}
	gid := hg.game.ID
	hg.mu.Unlock()
	s.Log.Logf("blaze", "   game %d: the match's rules: %q to %d player(s) as a System chat line", gid, text, len(to))
	sm := &s.sys
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.rulesNotes == nil {
		sm.rulesNotes = map[*Conn]rulesNote{}
	}
	for _, c := range to {
		n, line := sm.rulesNotes[c], text
		if n.text == text {
			n.repeats++
			line = fmt.Sprintf("%s (%d)", text, n.repeats+1)
		} else {
			n = rulesNote{text: text}
		}
		sm.rulesNotes[c] = n
		s.pushSystemLocked(c, []ClientMessage{RulesNoteMessage(line)})
	}
}

// rulesNote is the last rules line a connection got: its text, and how often in a row (forgotten with the connection,
// forgetMapNotes).
type rulesNote struct {
	text    string
	repeats int
}

// RulesNoteMessage is the line with a match's rules: a broadcast (TAG 2) with id RulesNoteID, shown as a "System" chat
// line in the match.
func RulesNoteMessage(text string) ClientMessage {
	return SystemMessage(SystemTagBroadcast, []MsgAttr{
		{BroadcastAttrID, fmt.Sprint(RulesNoteID)},
		{BroadcastAttrFlags, fmt.Sprint(BroadcastChatLine)},
		{BroadcastAttrSeconds, fmt.Sprint(rulesNoteSeconds)},
		{BroadcastAttrText, text},
	})
}
