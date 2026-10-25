package blaze

import (
	"fmt"
	"sync"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// GameManager with match hosts (roadmap C1, ADR-2 decision 7; dgserver --gms-dir). Without
// Server.Hosts nothing here runs and resetDedicatedServer behaves as in B8.
//
//	resetDedicatedServer -> JoinGameResponse at once, the host is spawned (gmshost) -> hello ->
//	createGame -> gameReady -> NotifyGameSetup with the host's GMS port in HNET -> ... -> playerFinish
//	(kept) -> finishGame (B7c: stats, XP, rewards, FTUE flag stored; playerRewards to the host; the wins
//	pushed; NotifyGameStateChange POST_GAME) -> the host exits -> NotifyPlayerRemoved GAME_ENDED for
//	whoever is still in the game.
//
// D8c, a game with a karma vote (matchmade): finishGame stores all but the rewards -> after the post-game's
// vote the host's playerKarma per player -> that player's rewards decided with the karma, stored, and
// playerRewards (with karma) to the host -> ... -> the host exits (a player without playerKarma gets the
// rewards without karma now).
//
// What ends the game for the client, from the client's code (docs/HOST_CONTROL.md *Ending the game
// on the Blaze side*; read statically in C1a, confirmed live in C1b: c1b_run1-c1b_run10):
//   - NotifyGameStateChange reaches cBlazeGameManager's listener: onPreGame and onGameStarted only log;
//     onGameEnded (POST_GAME; 0x00AC2E40) logs and clears the persisted reconnect game id
//     (BGOps::setGameID ''). No game state change in the client.
//   - NotifyPlayerRemoved for the local player reaches cBlazeGameManager::onPlayerRemoved (0x00AC4CB0):
//     it drops the current game and, for every reason except the connection and leave/kick ones, the
//     persisted game id, then posts event 0x0886DA16 with the reason mapped by 0x00AC0CA0 (GAME_DESTROYED
//     and GAME_ENDED -> 1). HandleGameClientMessage (0x0066FE00) then switches the client to the
//     game state the leave asked for, kGameStateLobby (2, the main menu) by default. It also tears
//     the game network down (destroyNetworkMesh), which is why the leaver gets one too (leaveHostedGame).
//
// The order guarantee: NotifyGameSetup is pushed only after resetDedicatedServer's reply is out
// (Request.After), even if the host is ready first.
//
// Where the host runs (H2): on this machine, or on a host machine whose agent registered with the
// manager (gmshost.Match.Site). HNET names the host machine: the agent's public address
// (Match.GMSHost) with the host's port, or for a host on this machine the server's own public host
// (--public-host) as before (hostAddr).

// GameManager notifications and enum values used here (docs/data/blaze_components.txt; name tables
// in .rdata as in gamemanager.go).
const (
	NotifyPlayerRemovedID      = 0x28
	NotifyGameStateChangeID    = 0x64
	GameStateInGame            = 0x83 // GameState IN_GAME
	GameStatePostGame          = 4    // GameState POST_GAME
	RemovedBlazeServerConnLost = 2    // PlayerRemovedReason BLAZESERVER_CONN_LOST
	RemovedGameDestroyed       = 4    // PlayerRemovedReason GAME_DESTROYED
	RemovedGameEnded           = 5    // PlayerRemovedReason GAME_ENDED
	// ErrNoHostCapacity is the code half of GAMEMANAGER_ERR_DYNAMIC_GAME_CREATION_FAILED_NO_CAPACITY
	// (0x01320004): the error reply when no host can be started (port pool exhausted, launch failed, no
	// machine). C1b: GameEntryManager_HandleNetworkGameJoinResult (0x0068EFC0) shows a dialog for a
	// failed create only with this error (Locale table 0x22307350 "Labs Dialogs", string 0x0F3C9B69 "Game
	// creation failed - no servers available"); C1a's GAMEMANAGER_ERR_NO_DEDICATED_SERVER_FOUND
	// (0x012D0004) left the player without a word (c1b_run6: logged, no message, still in the lobby).
	ErrNoHostCapacity = 0x0132
)

// HostedLevel is the level hosted games load by default (SM_Map_v5, the only map of build 854's practice
// game; D2b's GamePrepareForStart level 0x946D41FE); Server.Level (dgserver --level) replaces it (level.go).
const HostedLevel = "SM_Map_v5"

// PlayerRemovedReason names (name table 0x012E7668), as removePlayer passes them to the host.
var playerRemovedReasons = map[int64]string{0: "PLAYER_JOIN_TIMEOUT", 1: "PLAYER_CONN_LOST",
	2: "BLAZESERVER_CONN_LOST", 3: "MIGRATION_FAILED", 4: "GAME_DESTROYED", 5: "GAME_ENDED", 6: "PLAYER_LEFT",
	7: "GROUP_LEFT", 8: "PLAYER_KICKED"}

func removedReasonName(r int64) string {
	if n, ok := playerRemovedReasons[r]; ok {
		return n
	}
	return fmt.Sprintf("REASON_%d", r)
}

// hostAddr is a match's GMS address for HNET: port on the host machine's public address, which is an
// agent's (H2) or, for a host on this machine (GMSHost ""), the server's own as in base.
func hostAddr(base Addr, mt *gmshost.Match, port int) Addr {
	if a, ok := ParseAddr(mt.GMSHost, port); ok {
		return a
	}
	base.Port = int64(port)
	return base
}

// GameStateChangeNotification is GameManager::NotifyGameStateChange (0x64; class
// Blaze::GameManager::NotifyGameStateChange, table 0x014619F8: GID uint64, GSTA enum GameState).
func GameStateChangeNotification(gameID, state int64) []byte {
	return tdf.NewWriter().Int("GID", gameID).Int("GSTA", state).Bytes()
}

// PlayerRemovedNotification is GameManager::NotifyPlayerRemoved (0x28; class
// Blaze::GameManager::NotifyPlayerRemoved, table 0x01461328: CNTX uint16 (PlayerRemovedTitleContext;
// onPlayerRemoved maps 1 to its own reason 6), GID uint64, PID int64, REAS enum PlayerRemovedReason).
func PlayerRemovedNotification(gameID, playerID, reason int64) []byte {
	return tdf.NewWriter().Int("CNTX", 0).Int("GID", gameID).Int("PID", playerID).Int("REAS", reason).Bytes()
}

// hostedGames are the games with a running match host, by game id.
type hostedGames struct {
	mu   sync.Mutex
	byID map[int64]*hostedGame
}

func (h *hostedGames) add(g *hostedGame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byID == nil {
		h.byID = map[int64]*hostedGame{}
	}
	h.byID[g.game.ID] = g
}

func (h *hostedGames) remove(id int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.byID, id)
}

func (h *hostedGames) get(id int64) *hostedGame {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.byID[id]
}

// hostedGame is one game and its match host, seen from the Blaze side.
type hostedGame struct {
	s       *Server
	game    Game
	mode    string
	started time.Time
	replied chan struct{} // closed once the request that made the game has its reply out
	// setup builds one player's NotifyGameSetup (practice: B8's; matchmade: every player and the
	// matchmaking setup context, matchmaking.go).
	setup func(g Game, hp *hostedPlayer) []byte

	mu       sync.Mutex
	match    *gmshost.Match
	players  []*hostedPlayer
	state    int64     // the GSTA the clients know
	inGameAt time.Time // when state first became IN_GAME (zero before)
	ended    bool
	finishes map[int64]gmshost.PlayerFinish // the host's playerFinish per user (B7c), used at finishGame
	held     map[int64]*heldRewards         // D8c: the rewards that wait for the karma vote, by user
	// B15: a custom game's lobby (customgames.go); nil for practice and matchmade games. hg.game.Attrs is then the
	// game's attribute map as it is now (setGameAttributes changes it), sorted the way the client looks keys up.
	custom *customLobby

	// fillTeams is createGame's fillTeams (B10d; 0 = the host's auto rule): the Discord result counts the bots by it
	fillTeams int
}

// heldRewards is one player's finished game whose rewards wait for the host's playerKarma (D8c).
type heldRewards struct {
	hp          *hostedPlayer
	game        progress.Game
	hostRewards *gmshost.Rewards // the host's own proposal (playerFinish.rewards), if any
}

type hostedPlayer struct {
	p       Player
	userID  int64 // the account (store) id
	conn    *Conn
	joined  bool  // the host reported addPlayer
	removed bool  // left on the Blaze side, or removed by us
	party   int64 // D8b: the PvP party (playgroup id) the player queued with, 0 when solo (createGame's roster)
	// B15, a custom game's player: its player attributes (PATT: the join's ATTR, then setPlayerAttributes), its Blaze
	// slot (SID, in join order), and the setup context its NotifyGameSetup carries (create for the creator, join for
	// the others). Every hosted game's player (TEAMCHAT): whether its join completed (NotifyPlayerJoinCompleted went
	// out after its mesh connection; reset by a rejoin).
	attrs     []tdf.Pair
	slot      int64
	completed bool
	setupCtx  int64
}

// startHostedGame is resetDedicatedServer with a match host: reply now, announce the game once the
// host is ready. The roster is the creator alone (practice, B8): gameplay index 0, team TIDX + 1.
func (s *Server) startHostedGame(r *Request, g Game, p Player) []byte {
	hp := &hostedPlayer{p: p, userID: r.Conn.Session.Account.UserID, conn: r.Conn}
	hg, err := s.launchHostedGame(g, gmshost.ModePractice, 0, []*hostedPlayer{hp}, make(chan struct{}),
		func(g Game, hp *hostedPlayer) []byte { return GameSetupNotification(g, hp.p) }, nil)
	if err != nil {
		r.Fail(ErrNoHostCapacity)
		return nil
	}
	r.After(func() { close(hg.replied) })
	return JoinGameReply(g.ID)
}

// launchHostedGame starts a match host for g with players as its roster (gameplay index = position,
// team = TIDX + 1). NotifyGameSetup (setup's) goes to every player once the host is ready and replied
// is closed. Each player's ended Blaze session (a closed connection, or since B12 a logout) takes the player
// out of the game (sessionEnded). fillTeams > 0 asks the host for bots up to that many seats a team (B10d,
// createGame's fillTeams; 0 = the host decides by the descriptors). B15: custom (non-nil for a custom game) is its
// lobby; the roster is then lobbyRoster's, and the setup is the lobby's (lobbySetup). On an error
// nothing is left behind (the caller answers with ErrNoHostCapacity).
func (s *Server) launchHostedGame(g Game, mode string, fillTeams int, players []*hostedPlayer, replied chan struct{},
	setup func(Game, *hostedPlayer) []byte, custom *customLobby) (*hostedGame, error) {
	g.PersistedID = PersistedGameID(g.ID) // G3: the client saves it for a reconnect (reconnect.go)
	hg := &hostedGame{s: s, game: g, mode: mode, fillTeams: fillTeams, started: s.Now(), replied: replied, setup: setup,
		state: GameStatePreGame, players: players, custom: custom}
	if custom != nil {
		hg.setup = hg.lobbySetup
	}
	each := hg.setup // every player's NotifyGameSetup with the GMS address its client reaches (tunnel.go)
	hg.setup = func(g Game, hp *hostedPlayer) []byte { return each(hp.conn.ForClient(g), hp) }
	spec := gmshost.CreateGame{GameID: g.ID, Level: s.gameLevel(g), Mode: mode, Seed: g.Seed,
		Descriptors: int64(g.descriptors()), // D8-live: the game's GameFlags
		FillTeams:   fillTeams}              // B10d: a matchmade game's open seats
	if s.Halloween {
		spec.LevelFlags = gmshost.LevelFlagHalloween // the Halloween decoration (zz_Halloween.snap)
	}
	ids := make([]int64, len(players))
	for i, hp := range players {
		spec.Roster = append(spec.Roster, gmshost.RosterEntry{User: hp.p.ID, Name: hp.p.Name, Index: i, Team: int(hp.p.Team) + 1,
			Party: hp.party}) // D8b: the party, for the karma vote's party votes
		ids[i] = hp.p.ID
	}
	if custom != nil {
		spec.Roster = lobbyRoster(players, g.Attrs) // B15: the lobby's slots (team blocks, spectators 10-14)
	}
	s.shaperSelectSpec(&spec, players) // D3-live step 4: with --shaper-select
	s.hosted.add(hg)
	mt, err := s.Hosts.Start(spec, hg.events())
	if err != nil {
		s.hosted.remove(g.ID)
		s.Log.Logf("blaze", "   game %d %q: no match host (%v); error reply", g.ID, g.Name, err)
		return nil, err
	}
	hg.mu.Lock()
	hg.match = mt
	hg.mu.Unlock()
	who := fmt.Sprintf("player %d", ids[0])
	if len(ids) > 1 {
		who = fmt.Sprintf("players %v", ids)
	}
	if fillTeams > 0 {
		who += fmt.Sprintf(", bots fill each team to %d", fillTeams)
	}
	on := ""
	site, gmsHost, port, pid := mt.Placement() // the match's goroutine may move it already (gmshost's port retry)
	if site != gmshost.LocalSite {
		on = fmt.Sprintf(" on agent %s (GMS host %s)", site, gmsHost)
	}
	s.Log.Logf("blaze", "   game %d %q: match host %d starting%s (pid %d, GMS port %d), %s; NotifyGameSetup follows gameReady",
		g.ID, g.Name, mt.ID, on, pid, port, who)
	for _, hp := range players {
		conn, id := hp.conn, hp.p.ID
		conn.OnSessionEnd(func() { hg.sessionEnded(conn, id) })
	}
	return hg, nil
}

// sessionEnded takes a player whose Blaze session ended out of the game: its connection closed (the client
// quit or crashed) or, since B12, it logged out (Authentication::logout, which the client sends after its
// AFK kick; docs/BLAZE.md *Logout and the AFK kick (B12)*). The host gets removePlayer BLAZESERVER_CONN_LOST
// (abortGame once nobody is left), and the players still in a running game get NotifyPlayerRemoved with that
// reason, as Blaze tells every member about a removal (BlazeSDK's reason for a player whose Blaze session
// was lost or logged out [assumed from the name]; onPlayerRemoved 0x00AC4CB0 keeps the reconnect id for it).
// The leaver itself gets nothing: its session is gone, and at an AFK kick the client has already torn its
// game down (c1b_run11: onGameDestructing SYS_GAME_ENDING before the logout).
func (hg *hostedGame) sessionEnded(conn *Conn, playerID int64) {
	if !hg.leave(conn, playerID, RemovedBlazeServerConnLost) {
		return
	}
	hg.mu.Lock()
	ended := hg.ended
	hg.mu.Unlock()
	if !ended {
		payload := PlayerRemovedNotification(hg.game.ID, playerID, RemovedBlazeServerConnLost)
		hg.push(NotifyPlayerRemovedID, "NotifyPlayerRemoved", func(*hostedPlayer) []byte { return payload })
	}
	hg.lobbyLeft(playerID) // B15: a custom game's roster, its admin, the game browser
}

// leaveHostedGame handles removePlayer for a hosted game; false if gameID has no host.
//
// C1b: after the reply the leaving player (and whoever is still in the game) gets NotifyPlayerRemoved
// with the request's reason, as Blaze tells every game member about a removal. The client's
// onPlayerRemoved (0x00AC4CB0) handles its own PLAYER_LEFT (it keeps the reconnect id for that reason)
// and tears its game network down (cClientNetworkAdapter::destroyNetworkMesh: a clean RakNet disconnect
// from the GMS). Without it, the client that left a finished practice game by itself (ClientLeaveGame
// from kGameStateGameOver) kept its GMS connection until it timed out after the host had gone
// (c1b_run4: "ERROR: Game server connection lost" 19 s later in the main menu, LABS_LOST_SERVER_GMS).
// The notification goes out leaveNotifyDelay after the reply: sent together with it (c1b_run5), it reached
// the client in the frame in which its own ClientLeaveGame had already set the next game state, and the
// second state change asserted twice (StateMachine.cpp 271 and 222); a frame later onPlayerRemoved's state
// change finds the client in the lobby already, which is no change.
func (s *Server) leaveHostedGame(r *Request, gameID, playerID, reason int64) bool {
	hg := s.hosted.get(gameID)
	if hg == nil {
		return false
	}
	if hg.leave(r.Conn, playerID, reason) {
		conn := r.Conn
		payload := PlayerRemovedNotification(gameID, playerID, reason)
		r.After(func() {
			time.AfterFunc(leaveNotifyDelay, func() {
				conn.Push(s, ComponentGameManager, NotifyPlayerRemovedID, "NotifyPlayerRemoved", payload)
				hg.push(NotifyPlayerRemovedID, "NotifyPlayerRemoved", func(*hostedPlayer) []byte { return payload })
			})
			hg.lobbyLeft(playerID) // B15: a custom game's roster, its admin, the game browser
		})
	}
	return true
}

// leaveNotifyDelay is how long after the removePlayer reply the leaver's NotifyPlayerRemoved waits (see
// leaveHostedGame).
var leaveNotifyDelay = 500 * time.Millisecond

// leave takes a player out of the game on the Blaze side: the host gets removePlayer, and once no
// player is left, abortGame (a finished host is on its way out anyway). False if the player wasn't
// in the game (or had left already).
func (hg *hostedGame) leave(conn *Conn, playerID, reason int64) bool {
	hg.mu.Lock()
	var hp *hostedPlayer
	left := 0
	for _, x := range hg.players {
		if x.p.ID == playerID && x.conn == conn && !x.removed {
			hp = x
			x.removed = true
		}
		if !x.removed {
			left++
		}
	}
	mt, ended := hg.match, hg.ended
	hg.mu.Unlock()
	if hp == nil {
		return false
	}
	if ended || mt == nil {
		return true
	}
	name := removedReasonName(reason)
	hg.s.Log.Logf("blaze", "   game %d: player %d left (%s); %d still in the game", hg.game.ID, playerID, name, left)
	mt.RemovePlayer(playerID, name)
	if left == 0 {
		mt.Abort("no players left")
	}
	return true
}

// push sends a notification to every player still in the game.
func (hg *hostedGame) push(id uint16, name string, payload func(hp *hostedPlayer) []byte) {
	hg.mu.Lock()
	var to []*hostedPlayer
	for _, hp := range hg.players {
		if !hp.removed {
			to = append(to, hp)
		}
	}
	hg.mu.Unlock()
	for _, hp := range to {
		hp.conn.Push(hg.s, ComponentGameManager, id, name, payload(hp))
	}
}

func (hg *hostedGame) setState(state int64) {
	hg.mu.Lock()
	changed := hg.state != state
	hg.state = state
	if state == GameStateInGame && hg.inGameAt.IsZero() {
		hg.inGameAt = hg.s.Now() // HOME1: the home page's match minutes count from here
	}
	hg.mu.Unlock()
	if changed {
		hg.push(NotifyGameStateChangeID, "NotifyGameStateChange", func(*hostedPlayer) []byte {
			return GameStateChangeNotification(hg.game.ID, state)
		})
		if hg.custom != nil {
			hg.s.browserChanged(hg) // B15: the game browser shows the state
		}
	}
}

func (hg *hostedGame) events() gmshost.Events {
	s, gid := hg.s, hg.game.ID
	return gmshost.Events{
		Ready: func(mt *gmshost.Match, port int) {
			<-hg.replied // NotifyGameSetup must not overtake the reply
			hg.mu.Lock()
			hg.game.GMS = hostAddr(hg.game.GMS, mt, port)
			g := hg.game
			hg.mu.Unlock()
			after := "resetDedicatedServer"
			if hg.mode != gmshost.ModePractice {
				after = "the match formed"
			}
			s.Log.Logf("blaze", "   game %d: GMS %s (HNET) ready %.1f s after %s", gid, g.GMS,
				s.Now().Sub(hg.started).Seconds(), after)
			if hg.custom != nil {
				// B15: joinable from now on, set before the creator hears it so a join right after can't be
				// refused; the creator's NotifyGameSetup goes to the players of this moment only (a joiner has
				// its own), then the game browser lists the game
				hg.mu.Lock()
				hg.custom.ready = true
				var to []*hostedPlayer
				for _, hp := range hg.players {
					if !hp.removed {
						to = append(to, hp)
					}
				}
				hg.mu.Unlock()
				for _, hp := range to {
					hp.conn.Push(s, ComponentGameManager, NotifyGameSetupID, "NotifyGameSetup", hg.setup(g, hp))
				}
				s.browserChanged(hg)
				return
			}
			hg.push(NotifyGameSetupID, "NotifyGameSetup", func(hp *hostedPlayer) []byte {
				return hg.setup(g, hp)
			})
		},
		PlayerAdded: func(mt *gmshost.Match, a gmshost.AddPlayer) {
			hg.mu.Lock()
			for _, hp := range hg.players {
				if hp.p.ID == a.User {
					hp.joined = true
				}
			}
			hg.mu.Unlock()
			s.Log.Logf("blaze", "   game %d: player %d joined the GMS (index %d)", gid, a.User, a.Index)
		},
		PlayerGone: func(mt *gmshost.Match, d gmshost.DisconnectPlayer) {
			s.Log.Logf("blaze", "   game %d: player %d left the GMS (%s)", gid, d.User, d.Reason)
		},
		Update: func(mt *gmshost.Match, u gmshost.GMSUpdate) {
			switch u.Phase {
			case gmshost.PhaseInGame:
				hg.setState(GameStateInGame)
			case gmshost.PhasePostGame:
				hg.setState(GameStatePostGame)
			}
		},
		PlayerFinish: func(mt *gmshost.Match, f gmshost.PlayerFinish) {
			s.Log.Logf("blaze", "   game %d: player %d result %v (stored at finishGame)", gid, f.User, f.Stats)
			hg.mu.Lock()
			if hg.finishes == nil {
				hg.finishes = map[int64]gmshost.PlayerFinish{}
			}
			hg.finishes[f.User] = f
			hg.mu.Unlock()
		},
		Finish: func(mt *gmshost.Match, f gmshost.FinishGame) {
			s.Log.Logf("blaze", "   game %d finished: winner team %d, %.1f s, %s", gid, f.WinnerTeam,
				float64(f.DurationMs)/1000, f.Reason)
			hg.persist(mt, f)
			hg.postResult(f) // DISCORD2c: the result on Discord (results.go), store or not
			hg.setState(GameStatePostGame)
		},
		PlayerKarma: func(mt *gmshost.Match, k gmshost.PlayerKarma) {
			s.Log.Logf("blaze", "   game %d: player %d karma votes: party %d, team %d, enemy %d", gid, k.User, k.Party,
				k.Team, k.Enemy)
			hg.mu.Lock()
			h := hg.held[k.User]
			delete(hg.held, k.User)
			hg.mu.Unlock()
			if h == nil {
				s.Log.Logf("blaze", "   game %d: (no rewards of player %d wait for karma: playerKarma ignored)", gid, k.User)
				return
			}
			h.game.Karma = &progress.Karma{Party: k.Party, Team: k.Team, Enemy: k.Enemy}
			hg.grantHeld(mt, h, "the karma vote")
		},
		End: func(mt *gmshost.Match, o gmshost.Outcome) {
			<-hg.replied
			s.hosted.remove(gid)
			if hg.custom != nil {
				defer s.browserRemoved(gid) // B15: out of the game browser
			}
			hg.mu.Lock()
			hg.ended = true
			g := hg.game
			held := hg.held
			hg.held = nil
			hg.mu.Unlock()
			// D8c: rewards still waiting for a karma vote the host never reported (it ended, crashed or is an
			// older build): decided without karma, so the player still gets them (no playerRewards: the host is gone)
			for _, hp := range hg.players {
				if h := held[hp.p.ID]; h != nil {
					hg.grantHeld(nil, h, "the host's end without playerKarma (no karma)")
				}
			}
			// H5: a host that gave up (its watchdog saw the tick stall, or a step failed in every run) ends the
			// game as an aborted one below (GAME_DESTROYED, the client back at the main menu); its port is free
			if o.Error != nil {
				s.Log.Logf("blaze", "   game %d: the match host gave up: host error %d (%s): %s", gid, o.Error.Code,
					gmshost.ErrorName(o.Error.Code), o.Error.Text)
			}
			if o.Errors > 0 {
				s.Log.Logf("blaze", "   game %d: the match host sent %d non-fatal error report(s) (step exceptions)", gid,
					o.Errors)
			}
			switch {
			case !o.Ready:
				// Never announced: the client still waits for NotifyGameSetup. Name the dead port, so it
				// takes B8's path (the GMS connection fails after about 21 s; the client returns to the
				// Practice screen and sends removePlayer; b8_run5/b8_run6).
				g.GMS = hostAddr(g.GMS, mt, mt.Port)
				again := ""
				if o.Retries > 0 { // gmshost started it once more on another port, and that host failed too
					again = ", the second host too"
				}
				s.Log.Logf("blaze", "   game %d: match host gone before gameReady (%s%s); NotifyGameSetup names the dead GMS %s", gid,
					o.Reason, again, g.GMS)
				hg.push(NotifyGameSetupID, "NotifyGameSetup", func(hp *hostedPlayer) []byte { return hg.setup(g, hp) })
			case o.Finished:
				hg.push(NotifyPlayerRemovedID, "NotifyPlayerRemoved", func(hp *hostedPlayer) []byte {
					return PlayerRemovedNotification(gid, hp.p.ID, RemovedGameEnded)
				})
			default:
				s.Log.Logf("blaze", "   game %d: match host aborted (%s)", gid, o.Reason)
				hg.push(NotifyPlayerRemovedID, "NotifyPlayerRemoved", func(hp *hostedPlayer) []byte {
					return PlayerRemovedNotification(gid, hp.p.ID, RemovedGameDestroyed)
				})
			}
		},
	}
}

// persist stores what a finished game changes for every player who joined it (B7c, progression.go):
// PlayerStats, account XP and level, the rewards (Destiny, items, crafting currency for duplicates), the
// first win of the day, and FLAG bit 0; since B7d also the shaper and role history (playerFinish.stats noun
// and role; the profile's ShaperStats, ShaperHistoryStats and RoleHistoryStats, stats.go). The host gets
// each player's rewards back (playerRewards), and a player still connected a UserSessionExtendedDataUpdate
// with the new wins.
//
// FLAG bit 0 (store.FlagFTUEDone, "first-time-user experience done", B7a): a finished practice game sets
// it, so PLAY opens the normal play screen next time (so does a reward with the FTUE flag, the C1 04
// byte that sets the client's copy, D8-static). Who set the flag in the original is unknown (B7a's open
// question); finishing a practice game is the condition the client's own text names ("Completing a
// practice match will unlock the ability to play with real players", B8).
//
// A player's result: won = its team (Blaze TIDX + 1) is finishGame's winner, unless the host's
// playerFinish says otherwise (stats "won"); the match length is finishGame's durationMs. A player the
// host sent no playerFinish for still gets the game counted, with no kills or assists.
//
// D8c: in a game with a karma vote (progress.Game.KarmaVote: matchmade) the rewards are held (hg.held) until
// the host's playerKarma for the player (grantHeld), so the karma votes count in the bag score; everything
// else is stored here as before, and the wins are pushed now.
func (hg *hostedGame) persist(mt *gmshost.Match, f gmshost.FinishGame) {
	s := hg.s
	if s.Store == nil {
		return
	}
	practice := hg.mode == gmshost.ModePractice
	hg.mu.Lock()
	ranked := hg.ranked()
	var joined []*hostedPlayer
	for _, hp := range hg.players {
		// B15b: a custom game's spectator joins the GMS (the host's addPlayer) but plays nothing: no result, no
		// history, no rewards
		if hp.joined && !(hg.custom != nil && hp.p.Team == SpectatorTeam) {
			joined = append(joined, hp)
		} else if hp.joined {
			s.Log.Logf("blaze", "   account %d: game %d not stored (a spectator)", hp.userID, hg.game.ID)
		}
	}
	finishes := hg.finishes
	game := hg.game
	hg.mu.Unlock()
	for _, hp := range joined {
		pf := finishes[hp.p.ID]
		won := f.WinnerTeam != 0 && int(hp.p.Team)+1 == f.WinnerTeam
		if v, ok := pf.Stats[progress.StatWon]; ok {
			won = v != 0
		}
		g := progress.Game{Descriptors: game.descriptors(), Won: won, Seconds: float64(f.DurationMs) / 1000}
		hold := g.KarmaVote()
		acct, res, err := s.applyGame(hp.userID, g, pf.Stats, pf.Rewards, practice, ranked, hold)
		if err != nil {
			s.Log.Logf("blaze", "   (account %d: game %d not stored: %v)", hp.userID, game.ID, err)
			if err == store.ErrNoAccount {
				continue
			}
		}
		s.Log.Logf("blaze", "   account %d: game %d %s", hp.userID, game.ID, res.describe())
		if err == nil {
			s.Log.Logf("blaze", "   account %d: game %d history: %s", hp.userID, game.ID, res.describeHistory(s.Content))
			s.Log.Logf("blaze", "   account %d: game %d leaderboard: %s", hp.userID, game.ID, res.describeRanked(hg.mode))
		}
		if practice {
			s.Log.Logf("blaze", "   account %d: FTUE done (practice game %d finished)", hp.userID, game.ID)
		}
		hg.mu.Lock()
		if hold {
			if hg.held == nil {
				hg.held = map[int64]*heldRewards{}
			}
			hg.held[hp.p.ID] = &heldRewards{hp: hp, game: g, hostRewards: pf.Rewards}
		}
		removed := hp.removed
		hg.mu.Unlock()
		if mt != nil && !hold {
			mt.Rewards(hp.p.ID, toWire(res.Rewards))
		}
		if !removed {
			hp.conn.Push(s, ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, "UserSessionExtendedDataUpdate",
				ExtendedDataUpdateNotification(hp.p.ID, acct.UserInfo, res.Wins))
		}
		if err == nil {
			s.refreshKnownUser(hp.p.ID) // B14: the clients that know the player (its party, its game's chat) see the new wins
		}
	}
}

// ranked reports whether the game counts for the main menu's Leaderboard (its games and wins, store.History.Ranked;
// the operator's rule): a PvP game (matchmade, queued solo or as a group) and a custom game do; a practice game
// doesn't, nor does a practice group's, which the server makes as a custom game with the bots-fill bit in its
// GameFlags (0x412; customgames.go). The profile's PlayerStats and history count every game. Called with hg.mu held.
func (hg *hostedGame) ranked() bool {
	return Ranked(hg.mode, hg.game.descriptors())
}

// Ranked is hostedGame.ranked's rule by the game's mode (createGame's mode) and GameFlags (createGame's descriptors):
// the import of the Leaderboard's games from older servers' logs decides by it too, so both count alike.
func Ranked(mode string, gameFlags uint32) bool {
	switch mode {
	case gmshost.ModePvP:
		return true
	case gmshost.ModeCustom:
		return gameFlags&GameFlagBotsFillTeams == 0
	}
	return false
}

// grantHeld decides and stores a held player's rewards (D8c), with the karma votes in h.game if the host reported
// them, and sends them to the host (playerRewards with karma; mt nil = the host is gone).
func (hg *hostedGame) grantHeld(mt *gmshost.Match, h *heldRewards, after string) {
	s := hg.s
	r, err := s.applyRewards(h.hp.userID, h.game, h.hostRewards)
	if err != nil {
		s.Log.Logf("blaze", "   (account %d: game %d rewards not stored: %v)", h.hp.userID, hg.game.ID, err)
		if err == store.ErrNoAccount {
			return
		}
	}
	s.Log.Logf("blaze", "   account %d: game %d rewards after %s: %s", h.hp.userID, hg.game.ID, after, describeRewards(r))
	if mt != nil {
		mt.Rewards(h.hp.p.ID, toWire(r))
	}
}
