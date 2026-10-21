package blaze

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/tdf"
)

// PvP matchmaking (roadmap B10a; docs/BLAZE.md *PvP matchmaking (B10a)*, read statically).
//
// Past the first-time-user gate the play screen's PvP queue runs cGameEntryManager::Game_Create
// (0x00691DC0, type 1 Matched, flags 1) -> cBlazeGameManager::CreateGame (0x00AC6850, gameFlags bit 0:
// kCreateGameFlags_Matched) -> GameManagerAPI::startMatchmakingSession (0x00C89480) -> GameManager::
// startMatchmaking (cmd 0x0D, sender 0x00C7C9A0, RPC job 0x012E5570: StartMatchmakingResponse, error
// class MatchmakingCriteriaError). Only a PvP party's leader may queue; the party travels in BTPL
// (component 6, type 1, the playgroup id); PLST and SIDL stay empty. The client's side of the session:
//
//   - the reply's MSID becomes a MatchmakingSession (0x00C88230); every later notification finds it by
//     MSID alone (USID is never compared);
//   - NotifyGameSetup (0x00C88CE0) with REAS = MatchmakingSetupContext (union member 3) and the
//     session's MSID makes the game's setup job; the client then starts its network mesh through
//     cClientNetworkAdapter::initNetworkMesh (0x00AB6570) and connects to the GMS in HNET exactly as for
//     a practice game (B8). PROS must hold the local player (PID = its persona id) with STAT >= 2, and
//     THST.HPID must be non-zero. Only once the GMS connection is up does onMatchmakingSessionFinished
//     (0x00AC5DA0) take RSLT 0/1/2 as success and set the current game (which joins the "GG-<id>" chat,
//     B11). An unknown MSID with RSLT 0 makes the client destroy the game, with RSLT 1 leave it;
//   - a party member without a session gets REAS = IndirectMatchmakingSetupContext (member 4, GRID = the
//     party's playgroup), which always makes a setup job (onUserGroupJoinGame 0x00AC55D0) [assumed for
//     the member's side: no party was in a live run yet];
//   - NotifyMatchmakingFailed {MSID, RSLT} ends the session (0x00C88380): 3 timed out, 4 canceled (the
//     client waits for it after its own cancelMatchmaking), 5 terminated, 6 game setup failed;
//   - NotifyMatchmakingAsyncStatus only feeds a diagnostics log (0x00AC3910): not sent.
//
// The match-found dialog (MatchFoundUI) is the GMS's: accept and decline are GMS player status bits
// (0x10000/0x20000, LobbyState_OnMatchFoundResponse 0x005456F0), not Blaze requests.
//
// The server side is ours (internal/matchmaker): queues per mode and region, two teams of TeamSize,
// a game once the teams are full or, after FillAfter, with at least MinPlayers. The game is made like a
// practice game: a match host per game with --gms-dir (C1, mode "pvp", every player in the roster),
// otherwise the announced GMS port at once (B8/B9). Since B10d a game that started with open seats asks its
// host for bots in them (createGame's fillTeams = the team size; Server.MMBots, dgserver --mm-bots).
const (
	CmdGameManagerStartMatchmaking  = 0x0D
	CmdGameManagerCancelMatchmaking = 0x0E
	NotifyMatchmakingFailedID       = 0x0A
	setupReasonMatchmaking          = 3 // GameSetupReason union (0x012EA098) member 3: MatchmakingSetupContext
	setupReasonIndirectMatchmaking  = 4 // member 4: IndirectMatchmakingSetupContext
)

// MatchmakingResult (name table .rdata 0x012E83A8): RSLT of NotifyMatchmakingFailed and of the setup
// contexts. onMatchmakingSessionFinished maps 0/1/2 to success, 3/4/5/6 to the title's -4/-3/-2/-1
// (-4 and -1 show a dialog, LabsDialogs 0x0f3c9b67 and 0x0f3c9b70).
const (
	MatchmakingCreatedGame     = 0 // SUCCESS_CREATED_GAME
	MatchmakingJoinedNewGame   = 1 // SUCCESS_JOINED_NEW_GAME
	MatchmakingJoinedExisting  = 2 // SUCCESS_JOINED_EXISTING_GAME
	MatchmakingTimedOut        = 3 // SESSION_TIMED_OUT
	MatchmakingCanceled        = 4 // SESSION_CANCELED
	MatchmakingTerminated      = 5 // SESSION_TERMINATED
	MatchmakingGameSetupFailed = 6 // SESSION_ERROR_GAME_SETUP_FAILED
)

// GameManager error codes for startMatchmaking's error reply (code << 16 | 4). The client shows the
// error's localized text in a popup (PlatformServices +0x16C, 0x00AD70C0) and returns to idle.
const (
	ErrInvalidMatchmaking       = 0x00C8 // GAMEMANAGER_ERR_INVALID_MATCHMAKING_CRITERIA
	ErrMatchmakingExceededLimit = 0x00CE // GAMEMANAGER_ERR_MATCHMAKING_EXCEEDED_MAX_REQUESTS
)

var matchmakingResultNames = map[int64]string{MatchmakingCreatedGame: "SUCCESS_CREATED_GAME",
	MatchmakingJoinedNewGame: "SUCCESS_JOINED_NEW_GAME", MatchmakingJoinedExisting: "SUCCESS_JOINED_EXISTING_GAME",
	MatchmakingTimedOut: "SESSION_TIMED_OUT", MatchmakingCanceled: "SESSION_CANCELED",
	MatchmakingTerminated: "SESSION_TERMINATED", MatchmakingGameSetupFailed: "SESSION_ERROR_GAME_SETUP_FAILED"}

// DefaultMatchmaking is the matchmaker's default rules: 5v5 (the client asks for TEAM.PCAP 5 of an
// ExpectedPlayerCount of 10), and a lone player gets a game after 90 s, 30 s later for each player who
// joined meanwhile (MMWAIT, the user's rule of 2026-10-21: at most 330 s; the empty slots get bots with
// Server.MMBots, B10d).
func DefaultMatchmaking() matchmaker.Config {
	return matchmaker.Config{TeamSize: 5, MinPlayers: 1, FillAfter: 90 * time.Second, FillExtend: 30 * time.Second}
}

// MatchmakingContext is the setup context of one player of a matchmade game: MatchmakingSetupContext
// (table 0x014613E8: FIT uint32, MAXF uint32, MSID uint64, RSLT enum, USID uint64) for a session's
// owner, IndirectMatchmakingSetupContext (table 0x01461CE8: FIT, GRID objid, MAXF, MSID, RPVC bool,
// RSLT, USID) for a party member (Group set). RPVC stays 0: with 1 the client compares the game's VSTR
// with its own protocol version (0x00C7AF50).
type MatchmakingContext struct {
	Session int64 // MSID
	User    int64 // USID
	Result  int64 // RSLT
	Fit     int64 // FIT
	MaxFit  int64 // MAXF
	Group   *tdf.ObjID
}

// StartMatchmakingReply is GameManager::startMatchmaking's reply, StartMatchmakingResponse (table
// 0x012E8328: MSID uint64).
func StartMatchmakingReply(sessionID int64) []byte {
	return tdf.NewWriter().Int("MSID", sessionID).Bytes()
}

// MatchmakingFailedNotification is GameManager::NotifyMatchmakingFailed (0x0A; class table 0x01461388:
// MAXF uint32, MSID uint64, RSLT enum MatchmakingResult, USID uint64).
func MatchmakingFailedNotification(c MatchmakingContext) []byte {
	return tdf.NewWriter().Int("MAXF", c.MaxFit).Int("MSID", c.Session).Int("RSLT", c.Result).Int("USID", c.User).Bytes()
}

// MatchedGameSetupNotification is NotifyGameSetup for a matchmade game: GAME as B8's (ADMN = the first
// player), PROS = every player (SID = position), REAS = the recipient's setup context.
func MatchedGameSetupNotification(g Game, players []Player, c MatchmakingContext) []byte {
	pros := make([]*tdf.Writer, len(players))
	for i, p := range players {
		pros[i] = replicatedGamePlayer(g, p, int64(i))
	}
	w := tdf.NewWriter()
	w.Struct("GAME", func(w *tdf.Writer) { replicatedGameData(w, g, players[0].ID) })
	w.List("PROS", tdf.TypeStruct, pros)
	if c.Group == nil {
		w.Union("REAS", setupReasonMatchmaking, func(w *tdf.Writer) {
			w.Struct("VALU", func(w *tdf.Writer) {
				w.Int("FIT", c.Fit).Int("MAXF", c.MaxFit).Int("MSID", c.Session).Int("RSLT", c.Result).Int("USID", c.User)
			})
		})
	} else {
		w.Union("REAS", setupReasonIndirectMatchmaking, func(w *tdf.Writer) {
			w.Struct("VALU", func(w *tdf.Writer) {
				w.Int("FIT", c.Fit).Value("GRID", tdf.TypeObjID, *c.Group).Int("MAXF", c.MaxFit).Int("MSID", c.Session)
				w.Int("RPVC", 0).Int("RSLT", c.Result).Int("USID", c.User)
			})
		})
	}
	return w.Bytes()
}

// NetworkInfo is what a client last sent in UserSessions::updateNetworkInfo.
type NetworkInfo struct {
	PingSites map[string]int64 // NLMP: ping-site alias -> latency (ms)
	Ext, Int  Addr             // ADDR (IpPairAddress)
}

// MatchRegion is the region a StartMatchmakingRequest asks for (B10a). The request carries none: the
// client puts "specificRegion" or "flexRegion" into CRIT.PSR.THLD, chosen in CreateGame from the ping
// sites it reported (ConnectionManager +0x4C, 0x00CCBF30): the lowest latency wins, and an alias of
// fewer than 3 characters ("na", "eu") means specificRegion, a longer one ("na-any", "eu-any") or no map
// flexRegion. The play screen's region choice (Client_SetGameServerRegion 0x00670B70) pushes that
// region's ping sites (GameServerRegionList InitialPing: na 50, na-any 100, eu 150, eu-any 200; R6)
// with updateNetworkInfo. So: specificRegion = the lowest-latency alias, flexRegion = any ("").
func MatchRegion(threshold string, pingSites map[string]int64) string {
	if threshold != "specificRegion" || len(pingSites) == 0 {
		return ""
	}
	aliases := make([]string, 0, len(pingSites))
	for a := range pingSites {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)
	best := aliases[0]
	for _, a := range aliases[1:] {
		if pingSites[a] < pingSites[best] {
			best = a
		}
	}
	return strings.TrimSuffix(best, "-any")
}

// mmState is the server's matchmaking bookkeeping: session ids and the timer for the next Tick.
type mmState struct {
	mu    sync.Mutex
	ids   int64 // matchmaking session ids (MSID), from 1
	timer *time.Timer
	// DISCORD2b: discordMu orders the queue message's updates (discordqueue.go); forming counts the ticks that
	// formed matches and haven't shown them yet
	discordMu sync.Mutex
	forming   int
}

func (m *mmState) next() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ids++
	return m.ids
}

// mmSession is a ticket's Blaze side (matchmaker.Ticket.Data): the leader's game settings and every
// player's connection and addresses, in the ticket's player order (the session's owner first).
type mmSession struct {
	game    Game       // from the owner's StartMatchmakingRequest: name, attributes, settings, version
	group   *tdf.ObjID // BTPL: the owner's PvP party, nil when solo
	members []mmMember
}

type mmMember struct {
	conn    *Conn
	userID  int64 // the account (store) id
	ext, in Addr  // the player's own addresses (PNET; a party member's from updateNetworkInfo)
}

func registerMatchmaking(s *Server) {
	// startMatchmaking (Blaze::GameManager::StartMatchmakingRequest 0x01461D78): queue the session; the
	// reply carries its id, and a game or a failure follows as a notification (never before the reply).
	s.Register(ComponentGameManager, CmdGameManagerStartMatchmaking, "startMatchmaking", func(r *Request) []byte {
		sess := r.Conn.Session
		if sess == nil || sess.Persona.ID == 0 {
			r.Drop("startMatchmaking before login")
			return nil
		}
		t, err := r.Server.ticketFromRequest(r)
		if err == nil && r.Server.MMRefuse {
			err = errors.New("refused by --mm-refuse (test aid)")
		}
		if err == nil {
			err = r.Server.MM.Add(t)
		}
		if err != nil {
			code := uint16(ErrInvalidMatchmaking)
			if errors.Is(err, matchmaker.ErrAlreadyQueued) {
				code = ErrMatchmakingExceededLimit
			}
			r.Server.Log.Logf("blaze", "   matchmaking for %d refused: %v", sess.Persona.ID, err)
			r.Fail(code)
			return nil
		}
		region := t.Region
		if region == "" {
			region = "any"
		}
		r.Server.Log.Logf("blaze", "   matchmaking session %d: %s, region %s, timeout %s; %d sessions queued (%s)",
			t.ID, playerList(t.Players), region, t.Timeout, r.Server.MM.Len(), r.Server.MM.Config())
		for _, m := range t.Data.(*mmSession).members {
			conn, id := m.conn, t.ID
			conn.OnSessionEnd(func() { r.Server.endMatchmaking(id, MatchmakingTerminated, "session ended") })
		}
		r.After(r.Server.mmTick) // a match that forms now must follow the reply
		return StartMatchmakingReply(t.ID)
	})
	// cancelMatchmaking (CancelMatchmakingRequest 0x012E8348: MSID; cGameEntryManager::Matchmaking_Cancel
	// 0x0068D670 -> cancelMatchmakingSession 0x00C87470 -> sender 0x00C7B670, generic job 0x011F7C00
	// without a response class): an empty reply, then NotifyMatchmakingFailed SESSION_CANCELED, which the
	// client waits for to end the session and reset its UI.
	s.Register(ComponentGameManager, CmdGameManagerCancelMatchmaking, "cancelMatchmaking", func(r *Request) []byte {
		id, _ := r.Body.Int("MSID")
		t, ok := r.Server.MM.Remove(id)
		if !ok {
			r.Server.Log.Logf("blaze", "   matchmaking session %d: cancel, but it isn't queued (any more)", id)
			return nil
		}
		r.After(func() {
			r.Server.matchmakingFailed(t, MatchmakingCanceled, "canceled")
			r.Server.discordQueueSync(nil, false)
		})
		return nil
	})
}

func playerList(ps []matchmaker.Player) string {
	ids := make([]int64, len(ps))
	for i, p := range ps {
		ids[i] = p.ID
	}
	if len(ids) == 1 {
		return fmt.Sprintf("player %d", ids[0])
	}
	return fmt.Sprintf("players %v", ids)
}

// ticketFromRequest makes the queue ticket of a StartMatchmakingRequest: the owner, and with BTPL naming
// a PvP party it leads, every member of that playgroup.
func (s *Server) ticketFromRequest(r *Request) (*matchmaker.Ticket, error) {
	sess, req := r.Conn.Session, r.Body
	g := Game{}
	g.Name, _ = req.Str("GNAM")
	g.Settings, _ = req.Int("GSET")
	g.Version, _ = req.Str("GVER")
	g.Attrs = stringMap(req, "ATTR")
	ext, in := unionAddress(req, "PNET")
	if ext == (Addr{}) && in == (Addr{}) {
		ext, in = r.Conn.Net.Ext, r.Conn.Net.Int
	}
	threshold := ""
	if crit, ok := req.Sub("CRIT"); ok {
		if psr, ok := crit.Sub("PSR"); ok {
			threshold, _ = psr.Str("THLD")
		}
	}
	ms := &mmSession{game: g, members: []mmMember{{conn: r.Conn, userID: sess.Account.UserID,
		ext: s.ClientAddr(ext), in: s.ClientAddr(in)}}}
	t := &matchmaker.Ticket{ID: s.mm.next(), Mode: gmshost.ModePvP, Region: MatchRegion(threshold, r.Conn.Net.PingSites),
		Players: []matchmaker.Player{{ID: sess.Persona.ID, Name: sess.Persona.Name}}, Data: ms}
	if dur, ok := req.Int("DUR"); ok && dur > 0 {
		t.Timeout = time.Duration(dur) * time.Millisecond
	}
	if f, ok := req.Get("BTPL"); ok {
		if o, ok := f.Value.(tdf.ObjID); ok && o.Component == ComponentPlaygroups && o.ID != 0 {
			members, owner, found := s.groups.snapshot(o.ID)
			switch {
			case !found:
				return nil, fmt.Errorf("party %d doesn't exist", o.ID)
			case owner != sess.Persona.ID:
				return nil, fmt.Errorf("party %d is led by %d", o.ID, owner)
			}
			ms.group = &o
			for _, m := range members {
				if m.BlazeID == sess.Persona.ID || m.conn == nil {
					continue
				}
				t.Players = append(t.Players, matchmaker.Player{ID: m.BlazeID, Name: m.Name})
				ms.members = append(ms.members, mmMember{conn: m.conn, userID: m.AccountID,
					ext: s.ClientAddr(m.conn.Net.Ext), in: s.ClientAddr(m.conn.Net.Int)})
			}
		}
	}
	return t, nil
}

// unionAddress reads a NetworkAddress union member (IpPairAddress, member 2) of a request.
func unionAddress(req *tdf.Struct, label string) (ext, in Addr) {
	f, ok := req.Get(label)
	if !ok {
		return
	}
	u, ok := f.Value.(*tdf.Union)
	if !ok || u.Member != NetworkAddressIpPair || u.Field == nil {
		return
	}
	v, ok := u.Field.Value.(*tdf.Struct)
	if !ok {
		return
	}
	get := func(label string) Addr {
		s, ok := v.Sub(label)
		if !ok {
			return Addr{}
		}
		ip, _ := s.Int("IP")
		port, _ := s.Int("PORT")
		return Addr{IP: uint32(ip), Port: port}
	}
	return get("EXIP"), get("INIP")
}

// endMatchmaking takes session id out of the queue (one of its players' connections closed) and tells
// its owner.
func (s *Server) endMatchmaking(id, result int64, why string) {
	if t, ok := s.MM.Remove(id); ok {
		s.matchmakingFailed(t, result, why)
		s.discordQueueSync(nil, false)
	}
}

// matchmakingFailed sends NotifyMatchmakingFailed to the session's owner (the only client that knows the
// session; a party's members follow its leader through the playgroup, MatchmakingStatus).
func (s *Server) matchmakingFailed(t *matchmaker.Ticket, result int64, why string) {
	s.Log.Logf("blaze", "   matchmaking session %d ended: %s (%s)", t.ID, matchmakingResultNames[result], why)
	owner := t.Data.(*mmSession).members[0]
	owner.conn.Push(s, ComponentGameManager, NotifyMatchmakingFailedID, "NotifyMatchmakingFailed",
		MatchmakingFailedNotification(MatchmakingContext{Session: t.ID, User: t.Players[0].ID, Result: result}))
}

// mmTick forms what the queue allows now, ends timed-out sessions and arms the timer for the next
// deadline (no timer while the queue is empty). Then the Discord queue message shows the queue (DISCORD2b: every
// join comes through here, and every deadline).
func (s *Server) mmTick() {
	s.mm.discordMu.Lock()
	matches, expired := s.MM.Tick()
	formed := len(matches) > 0
	if formed {
		s.mm.forming++
	}
	s.mm.discordMu.Unlock()
	for _, t := range expired {
		s.matchmakingFailed(t, MatchmakingTimedOut, "timed out")
	}
	var started []MatchStarted
	for _, m := range matches {
		if ms, ok := s.startMatch(m); ok {
			started = append(started, ms)
		}
	}
	s.discordQueueSync(started, formed)
	next := s.MM.NextDeadline()
	s.mm.mu.Lock()
	defer s.mm.mu.Unlock()
	if s.mm.timer != nil {
		s.mm.timer.Stop()
		s.mm.timer = nil
	}
	if !next.IsZero() {
		s.mm.timer = time.AfterFunc(max(time.Until(next), 0)+10*time.Millisecond, s.mmTick)
	}
}

// startMatch makes the game of a formed match and announces it to its players: PROS in team order (team
// index = TIDX), the anchor session's owner SUCCESS_CREATED_GAME, the other owners
// SUCCESS_JOINED_NEW_GAME, party members the indirect context with their leader's session and result. It returns
// the match's humans, bots and empty seats for the Discord queue message, ok false when no host could take it.
func (s *Server) startMatch(m matchmaker.Match) (MatchStarted, bool) {
	tickets := m.Tickets()
	g := tickets[0].Data.(*mmSession).game
	g.ID = s.gameIDs.Add(1)
	g.GMS, _ = ParseAddr(s.gmsHost(), s.GMSPort)
	g.Seed = g.ID * 7919
	size := int64(s.MM.Config().TeamSize)
	g.Capacity = []int64{2 * size, 0}
	g.TeamCap = size
	g.TeamIDs = []int64{1, 2}
	now := s.Now().UnixMicro()
	var players []Player
	ctx := map[int64]MatchmakingContext{}
	var hps []*hostedPlayer
	for team := range m.Teams {
		for _, t := range m.Teams[team] {
			ms := t.Data.(*mmSession)
			result := int64(MatchmakingJoinedNewGame)
			if t == tickets[0] {
				result = MatchmakingCreatedGame
			}
			for i, p := range t.Players {
				mb := ms.members[i]
				pl := Player{ID: p.ID, Name: p.Name, Team: int64(team), Ext: mb.ext, Int: mb.in, JoinTime: now}
				players = append(players, pl)
				c := MatchmakingContext{Session: t.ID, User: p.ID, Result: result}
				if i > 0 {
					c.Group = ms.group
				}
				ctx[p.ID] = c
				hp := &hostedPlayer{p: pl, userID: mb.userID, conn: mb.conn}
				if ms.group != nil { // D8b: a party's members carry its playgroup id into createGame
					hp.party = ms.group.ID
				}
				hps = append(hps, hp)
			}
		}
	}
	if g.Name == "" {
		g.Name = players[0].Name + "'s Game"
	}
	// B10d: a match that formed with open seats (after FillAfter) asks its host to fill them with bots up to the
	// team size, unless the operator turned that off (--mm-bots=false); a full match has no seat to fill.
	fill, seats := 0, ""
	if !m.Full && s.Hosts != nil {
		if s.MMBots {
			fill, seats = int(size), fmt.Sprintf("; open seats: bots up to %d a team", size)
		} else {
			seats = "; open seats stay empty (--mm-bots=false)"
		}
	}
	s.Log.Logf("blaze", "   matchmaking: game %d %q, %d v %d (full %v, region %q): sessions %v v %v%s",
		g.ID, g.Name, m.Size(0), m.Size(1), m.Full, m.Region, ticketIDs(m.Teams[0]), ticketIDs(m.Teams[1]), seats)
	setup := func(g Game, hp *hostedPlayer) []byte { return MatchedGameSetupNotification(g, players, ctx[hp.p.ID]) }
	started := MatchStarted{Players: len(players)}
	if fill > 0 {
		started.Bots = 2*fill - len(players)
	} else {
		started.Open = int(2*size) - len(players)
	}
	if s.Hosts != nil {
		replied := make(chan struct{})
		close(replied) // the sessions' replies went out before the match formed
		if _, err := s.launchHostedGame(g, gmshost.ModePvP, fill, hps, replied, setup, nil); err != nil {
			for _, t := range tickets {
				s.matchmakingFailed(t, MatchmakingGameSetupFailed, err.Error())
			}
			return MatchStarted{}, false
		}
		return started, true
	}
	s.Log.Logf("blaze", "   game %d %q: GMS %s (HNET), players %v", g.ID, g.Name, g.GMS, playerIDs(players))
	for _, hp := range hps {
		hp.conn.Push(s, ComponentGameManager, NotifyGameSetupID, "NotifyGameSetup", setup(hp.conn.ForClient(g), hp))
	}
	return started, true
}

func ticketIDs(ts []*matchmaker.Ticket) []int64 {
	out := make([]int64, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func playerIDs(ps []Player) []int64 {
	out := make([]int64, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}
