package blaze

import (
	"fmt"
	"strconv"
	"strings"

	"dawngate/server/internal/tdf"
)

// The custom lobby's ready check. The lobby has none of its own (the client's CustomGameLobbyView, its tiles and the
// admin's roster manager hold no ready state), so it is a class of ours in the front end's movie (client_ui/lobby,
// LobbyReady: a READY toggle for every human on the two teams except the admin, a mark on each counted tile, the
// admin's START held disabled with "n/m ready" above it) that talks to the server over the lobby's own game chat:
//
//	client -> server   ~#rdy hi~      once per lobby entry: the client has the class
//	                   ~#rdy 1~       its player is ready; ~#rdy 0~ not (the guest's toggle)
//	server -> client   ~#rdy 1~ / ~#rdy 0~   in the name of the player whose state it is (SRCE, NAME: that player;
//	                                         the class keys the line by the chat's event:p,<persona id> link)
//	                   ~#rdy wait~    to the admin, from itself: a start was refused because someone isn't ready
//	                   ~#rat <n>~     RATINGSHOW3: in the name of a player, the rating players see (Server.ShownRating);
//	                                  the class shows "Rating <n>" on that player's tile beside the wins
//
// A lobby line is a game chat text "~#<kind> <args>~" of a kind in lobbyLineKinds (args separated by blanks); it is
// never relayed (relay swallows it: no other client, no echo), so a client without the class never sees one, and the
// server answers only clients that said hi. The server's own kinds (rat) are swallowed too and ignored, so no client
// can put a line in its own name that another client's class would take for the server's.
//
// Who counts: the humans on teams 0 and 1 who said hi, except the admin (who is ready by starting); spectators never;
// a player who never said hi counts as ready (a client without the class doesn't block). A state goes back to 0 at a
// team change (swapPlayersTeam), a move to the spectators (then not counted) and a rejoin (the new join says hi
// again); a kicked or leaving player drops out. The admin's advanceGameState IN_GAME while a counted player isn't
// ready is refused (GAMEMANAGER_ERR_INVALID_GAME_STATE_ACTION) as a backstop only: the class keeps the admin's START
// disabled, which sends nothing. Client evidence: the chat's OnGameMessage drops a game message from a user its chat
// doesn't know (a lobby member always is) and writes the sender's link event:p,<persona id> before the text; a
// refused advanceGameState leaves the client in its lobby (cBlazeGameManager::ChangeGameStateResult 0x00AC1C70).

const (
	// LobbyLineReady is the ready check's kind: "~#rdy <arg>~".
	LobbyLineReady = "rdy"
	readyHello     = "hi"
	readyYes       = "1"
	readyNo        = "0"
	readyWait      = "wait"
	// LobbyLineRating is the tiles' rating (RATINGSHOW3): "~#rat <n>~", sent only by the server.
	LobbyLineRating = "rat"
)

// lobbyLineKinds are the lobby line kinds: a client's line of one is swallowed (handled by the server, or, for the
// server's own kinds, ignored).
var lobbyLineKinds = map[string]bool{LobbyLineReady: true, LobbyLineRating: true}

// LobbyLine is "~#<kind> <args...>~".
func LobbyLine(kind string, args ...string) string {
	return "~#" + kind + " " + strings.Join(args, " ") + "~"
}

// parseLobbyLine reads a chat text as a lobby line of a known kind: "~#rdy 1~" -> ("rdy", ["1"]).
func parseLobbyLine(text string) (kind string, args []string, ok bool) {
	if !strings.HasPrefix(text, "~#") || !strings.HasSuffix(text, "~") || len(text) < 4 {
		return "", nil, false
	}
	f := strings.Fields(text[2 : len(text)-1])
	if len(f) == 0 || !lobbyLineKinds[f[0]] {
		return "", nil, false
	}
	return f[0], f[1:], true
}

// lobbyLineOf is the lobby line msg carries (its chat text), if any.
func lobbyLineOf(msg ClientMessage) (kind string, args []string, ok bool) {
	if msg.TargetType != ObjTypeGame && msg.TargetType != ObjTypePlaygroup {
		return "", nil, false
	}
	text, found := msg.Attr(MsgAttrText)
	if !found {
		text, found = msg.Attr(MsgAttrTextUnfiltered)
	}
	if !found {
		return "", nil, false
	}
	return parseLobbyLine(text)
}

// lobbyLine is a swallowed lobby line: the game it is for (0: not a game's chat), its kind and arguments, and the
// message as sent (the server's own lines copy its form: attributes, flags, target).
type lobbyLine struct {
	game int64
	kind string
	args []string
	msg  ClientMessage
}

// lobbyGameOf is the game whose chat target t, id is: the game itself or its chat playgroup GG-<game id>.
func (s *Server) lobbyGameOf(t tdf.ObjType, id int64) int64 {
	if t == ObjTypeGame {
		return id
	}
	s.groups.mu.Lock()
	pg := s.groups.byID[id]
	key := ""
	if pg != nil {
		key = pg.info.Key
	}
	s.groups.mu.Unlock()
	if g, ok := strings.CutPrefix(key, "GG-"); ok {
		if n, err := strconv.ParseInt(g, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// withText is the lobby chat message m with its text attributes set to text (the form the client sent).
func withText(m ClientMessage, text string) ClientMessage {
	out := m
	out.Attrs = make([]MsgAttr, len(m.Attrs))
	for i, a := range m.Attrs {
		if a.Key == MsgAttrText || a.Key == MsgAttrTextUnfiltered {
			a.Value = text
		}
		out.Attrs[i] = a
	}
	return out
}

// readySend is one server line: to a class holder, in the name of the player about, with arg.
type readySend struct {
	to, about *hostedPlayer
	arg       string
}

// readyCountedLocked reports whether hp's state counts for the start: a class holder on team 0 or 1, not an admin
// (hg.mu held).
func (hg *hostedGame) readyCountedLocked(hp *hostedPlayer) bool {
	return !hp.removed && hp.readyClass && (hp.p.Team == 0 || hp.p.Team == 1) && !hg.isAdminLocked(hp.p.ID)
}

// readyHoldersLocked are the players who get the lines: in the game, said hi (hg.mu held).
func (hg *hostedGame) readyHoldersLocked() []*hostedPlayer {
	var out []*hostedPlayer
	for _, hp := range hg.players {
		if !hp.removed && hp.readyClass && hp.conn != nil {
			out = append(out, hp)
		}
	}
	return out
}

// readyTellLocked are the lines that tell every class holder (but skip) about's state arg (hg.mu held).
func (hg *hostedGame) readyTellLocked(about *hostedPlayer, arg string, skip *hostedPlayer) []readySend {
	var out []readySend
	for _, h := range hg.readyHoldersLocked() {
		if h != skip {
			out = append(out, readySend{h, about, arg})
		}
	}
	return out
}

// readyClearLocked puts hp's state back to 0 (a team change, a rejoin) and tells the holders (hg.mu held). Nothing
// for a player without the class.
func (hg *hostedGame) readyClearLocked(hp *hostedPlayer, skip *hostedPlayer) []readySend {
	if !hp.readyClass || hg.custom == nil || hg.custom.started {
		return nil
	}
	hp.ready = false
	return hg.readyTellLocked(hp, readyNo, skip)
}

// readyWaitingLocked are the counted players who aren't ready (hg.mu held).
func (hg *hostedGame) readyWaitingLocked() []int64 {
	var out []int64
	for _, hp := range hg.players {
		if hg.readyCountedLocked(hp) && !hp.ready {
			out = append(out, hp.p.ID)
		}
	}
	return out
}

// sendReady sends the ready check's lines (outside hg.mu).
func (hg *hostedGame) sendReady(lines []readySend) {
	hg.sendLines(LobbyLineReady, lines, true)
}

// sendLines sends lobby lines of kind (outside hg.mu) as NotifyMessage in the name of each line's player, in the lobby
// chat message's form the game's class holders sent (customLobby.readyForm); logged one by one, or all in one line.
func (hg *hostedGame) sendLines(kind string, lines []readySend, logEach bool) {
	if len(lines) == 0 {
		return
	}
	hg.mu.Lock()
	form := hg.custom.readyForm
	hg.mu.Unlock()
	if form == nil {
		return
	}
	s := hg.s
	var sent []string
	for _, l := range lines {
		text := LobbyLine(kind, l.arg)
		note := MessageNotification(ServerMessage{ID: s.msgs.newID(), Name: l.about.p.Name,
			Payload: withText(*form, text), Source: l.about.p.ID, Time: s.Now().Unix()})
		if logEach {
			s.Log.Logf("blaze", "   game %d: ready check: %q from %d to %d", hg.game.ID, text, l.about.p.ID, l.to.p.ID)
		} else {
			sent = append(sent, fmt.Sprintf("%d %s to %d", l.about.p.ID, l.arg, l.to.p.ID))
		}
		l.to.conn.Push(s, ComponentMessaging, NotifyMessageID, "NotifyMessage", note)
	}
	if len(sent) > 0 {
		s.Log.Logf("blaze", "   game %d: the tiles' ratings (~#%s <n>~): %s", hg.game.ID, kind, strings.Join(sent, ", "))
	}
}

// sendRatings tells class holders the rating each player of the game shows (RATINGSHOW3): one "~#rat <n>~" per
// player with a shown rating (Server.ShownRating: show_rating on and enough rated games; with the switch off nothing
// is sent and the tiles stay as they were), in that player's name; the spectators too (a spectator can join a team;
// the class draws a rating on the team tiles only, as EA's spectator tile has no wins either). to nil: every holder
// (after a roster change, for a newcomer's rating: the admin's TeamRostersKey follows every join, and by then every
// member's chat knows the newcomer); else that holder only (its hi: everyone's). The lines are sent again at every
// roster change rather than remembered per holder, so a line a client dropped comes again.
func (hg *hostedGame) sendRatings(to *hostedPlayer) {
	hg.mu.Lock()
	if hg.custom == nil || hg.custom.started || hg.ended {
		hg.mu.Unlock()
		return
	}
	var holders, about []*hostedPlayer
	for _, h := range hg.readyHoldersLocked() {
		if to == nil || h == to {
			holders = append(holders, h)
		}
	}
	for _, hp := range hg.players {
		if !hp.removed {
			about = append(about, hp)
		}
	}
	hg.mu.Unlock()
	if len(holders) == 0 {
		return
	}
	var lines []readySend
	for _, a := range about {
		acct, _, ok := hg.s.Store.PersonaByID(a.p.ID)
		if !ok {
			continue
		}
		v, shown := hg.s.ShownRating(acct.History)
		if !shown {
			continue
		}
		for _, h := range holders {
			lines = append(lines, readySend{h, a, strconv.FormatInt(v, 10)})
		}
	}
	hg.sendLines(LobbyLineRating, lines, false)
}

// readyLine handles a swallowed ~#rdy line from sender (after sendMessage's reply).
func (s *Server) readyLine(sender int64, line lobbyLine) {
	arg := ""
	if len(line.args) > 0 {
		arg = line.args[0]
	}
	hg := s.hosted.get(line.game)
	if hg == nil || hg.custom == nil {
		s.Log.Logf("blaze", "   ready check: %q from %d for game %d: no custom game; ignored", arg, sender, line.game)
		return
	}
	hg.mu.Lock()
	hp := hg.playerLocked(sender)
	if hp == nil || hp.removed || hg.custom.started || hg.ended {
		hg.mu.Unlock()
		s.Log.Logf("blaze", "   game %d: ready check: %q from %d: not in the lobby (or it started); ignored", line.game, arg, sender)
		return
	}
	form := line.msg
	hg.custom.readyForm = &form
	var out []readySend
	why := ""
	switch arg {
	case readyHello:
		hp.readyClass, hp.ready = true, false
		for _, q := range hg.players {
			if hg.readyCountedLocked(q) {
				state := readyNo
				if q.ready {
					state = readyYes
				}
				out = append(out, readySend{hp, q, state})
			}
		}
		if hg.readyCountedLocked(hp) {
			out = append(out, hg.readyTellLocked(hp, readyNo, hp)...)
		}
		why = fmt.Sprintf("has the class; %d counted now", len(hg.readyCountedOfLocked()))
	case readyYes, readyNo:
		hp.readyClass = true
		if !hg.readyCountedLocked(hp) {
			why = "not counted (the admin or a spectator); ignored"
			break
		}
		hp.ready = arg == readyYes
		out = hg.readyTellLocked(hp, arg, nil)
		why = fmt.Sprintf("ready %v; waiting for %v", hp.ready, hg.readyWaitingLocked())
	default:
		why = "unknown; ignored"
	}
	hg.mu.Unlock()
	s.Log.Logf("blaze", "   game %d: ready check: %q from %d: %s", line.game, arg, sender, why)
	hg.sendReady(out)
	if arg == readyHello {
		hg.sendRatings(hp) // RATINGSHOW3: the newcomer's tiles get every player's rating
	}
}

// readyCountedOfLocked are the counted players (hg.mu held).
func (hg *hostedGame) readyCountedOfLocked() []int64 {
	var out []int64
	for _, hp := range hg.players {
		if hg.readyCountedLocked(hp) {
			out = append(out, hp.p.ID)
		}
	}
	return out
}

// refuseUnreadyStart refuses the admin's advanceGameState IN_GAME while counted players aren't ready (hg.mu held; it
// unlocks it). The class keeps START disabled, so this is a backstop for a modified or racing client. The client
// survives the error (cBlazeGameManager::ChangeGameStateResult 0x00AC1C70 -> HandleNetworkGameStartFailed
// 0x00684D70: no dialog, the lobby stays), but its START had locked the roster (RosterLockedKey "1"), which nothing of
// the client's unlocks again; so the server sets RosterLockedKey "0" for every player (the roster manager's
// onGameAttributeUpdated 0x006A2670 takes "0"/"1" into its lock, 0x0069FBE0), and tells an admin with the class why
// (~#rdy wait~ in its own name).
//
// READY2b: the unlock alone leaves the admin's join buttons and kick X hidden. 0x0069FBE0 tells the roster listeners
// (0x0069F4C0 -> 0x0069F1E0, slot 5), the lobby's slot 5 (0x00427E30 -> 0x00427800) sets the view's rosterLocked,
// whose setter only redraws the lock's parts (drawTeamChangeAvailable: dragging, the join buttons' enabled state); the
// join buttons' and kick buttons' visibility comes from drawRoster (showJoinTeamResource, showKickButton), which runs
// only at invalidateRoster. (The lobby's own 0x00425DA0 calls the view's "invalidateRosterIsLocked", a name the view
// doesn't have: its method is invalidateIsRosterLocked.) So the admin also gets its own extended data after the
// unlock, as RATING2b's redraw after BALANCE: the lobby's UserManager listener (0x004260E0) calls invalidateRoster.
// why is added to the log line.
func (s *Server) refuseUnreadyStart(r *Request, hg *hostedGame, waiting []int64, why string) {
	gid := hg.game.ID
	unlock := []tdf.Pair{tdf.KV(AttrRosterLocked, "0")}
	hg.game.Attrs = mergeAttrs(hg.game.Attrs, unlock)
	var lines []readySend
	var admins []*hostedPlayer
	for _, a := range hg.custom.admins {
		hp := hg.playerLocked(a)
		if hp == nil || hp.removed || hp.conn == nil {
			continue
		}
		admins = append(admins, hp)
		if hp.readyClass {
			lines = append(lines, readySend{hp, hp, readyWait})
		}
	}
	hg.mu.Unlock()
	s.Log.Logf("blaze", "   game %d: advanceGameState IN_GAME refused: not ready %v (the ready check)%s; RosterLockedKey \"0\" to every player, the admin its extended data (its lobby's redraw)",
		gid, waiting, why)
	r.Fail(ErrInvalidGameStateAction)
	note := GameAttribChangeNotification(gid, unlock)
	r.After(func() {
		hg.push(NotifyGameAttribChangeID, "NotifyGameAttribChange", func(*hostedPlayer) []byte { return note })
		for _, a := range admins {
			if own := s.OwnExtendedData(a.p.ID); own != nil {
				a.conn.Push(s, ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, "UserSessionExtendedDataUpdate", own)
			}
		}
		hg.sendReady(lines)
	})
}
