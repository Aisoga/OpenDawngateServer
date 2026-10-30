package blaze

// B15: custom games and their lobby, read statically from Dawngate.exe 854 (read-only Ghidra; docs/BLAZE.md *Custom
// games and spectators (B15)*). The client's side:
//
//   - CREATE GAME (MatchTypeUI 0x004EAF10 -> MatchOptionsUI: name, password, map) posts message 0x2F3B9235; the play
//     screen's handler 0x004EC660 -> MainMenuUI_RequestGameCreate 0x004A8ED0 -> cGameEntryManager::Game_Create
//     (0x00691DC0, type 0 Managed -> gameFlags 2; a password adds the attribute PrivateMatch "1") ->
//     cBlazeGameManager::CreateGame (0x00AC6850, "kCreateGameFlags_Custom": 3 teams TIDS [1, 2, 3] of TCAP 5, PCAP
//     [15, 0], GSET | 0x505, ExpectedPlayerCount 10, LevelId 2490188286 = 0x946D41FE, the one playable map) ->
//     GameManager::resetDedicatedServer (0x00C86050 -> sender 0x00C7C540), as a practice game. A practice group's game
//     (type 4, gameFlags 0x412) takes the same branch (no matched bit 1, no solo bit 4).
//   - The client connects to the GMS at NotifyGameSetup as for any game; its create (or join) completes with that
//     connection, and the main menu then shows the custom lobby (content 7, CustomMatchLobbyUI; a practice group's:
//     content 13). The lobby's roster is the admin's cGameRosterManager (GameRosterManager.cpp): it packs three rosters
//     (team 1, team 2, spectators) into the game attribute TeamRostersKey (TeamRosters below), the spectators' ids into
//     SpectatorPlayerIdList, the lock into RosterLockedKey, and sets them with GameManager::setGameAttributes (cmd 7)
//     plus GameManager::swapPlayersTeam (cmd 0x70) so every player's Blaze team index (TIDX; 2 = the spectators)
//     matches its roster (0x006A3840, 0x006A36D0). It adds a player at onPlayerJoinComplete (game listener slot 11,
//     0x006A11A0: NotifyPlayerJoinCompleted) and drops one at onPlayerRemoved. A non-admin's Join Team is a Messaging
//     user message to the admin (0x006A2D30: TYPE 0xC4063947, ATTR 0xB0000 the game id, 0xB0001 the team index;
//     handled by the admin's 0x006A2520 -> AdminSetTeam), relayed like any other (messaging.go).
//   - START (the lobby's start button, message 0x829E6A64 -> Game_Start 0x006850D0) locks the roster, waits until it
//     is synchronised (UpdateDeferredGameStart 0x0068E470, at most 30 s), then Game::startGame (0x00AC2200 ->
//     0x00C7DDE0: only in PRE_GAME) sends GameManager::advanceGameState {GID, GSTA IN_GAME}; NotifyGameStateChange
//     IN_GAME ends the start (the game entry manager's onGameStarted 0x00685610). The GMS then starts the pre-game.
//   - The game browser (PLAY > CUSTOM > BROWSE GAMES, MatchBrowserUI): getGameListSubscription (gamebrowser.go).
//     JOIN GAME / SPECTATE GAME post 0x0EC5CE5E -> Game_JoinPublic (vtable slot 11, 0x00692F80) with team 0xFFFF (try
//     team 0 and 1 in a random order) or 2 (spectate), or for a private game Game_JoinPrivate (0x0068CA90: a password
//     hash to the owner first, user message 0x7815553A, answered with 0x6EBDC8E6 and an auth token the joiner puts
//     into its player attribute PrivateAuthToken; the owner kicks a joiner with a wrong one, 0x00681F90) ->
//     cBlazeGameManager::JoinGame (0x00AC60E0) -> GameManager::joinGame (cmd 9) {GID, TIDX, ATTR (player
//     attributes), PNET, ...}.
//   - The lobby's chat: the game's GG-<id> chat and the team chats TG-<id>-<team> (BlazeRoomsManager_UpdateTeamChatGroups
//     0x00AAF220; a spectator joins all three), keyed chat playgroups as B11 serves them.
//   - Kick: Game_KickPlayer (0x00684540) -> removePlayer {PID the kicked player, REAS PLAYER_KICKED 8}.
//
// What dgserver does: a custom game is a hostedGame with a customLobby. The creator is its admin (ADMN). joinGame adds
// players (NotifyGameSetup with the join context and every player; NotifyPlayerJoining to the others),
// updateMeshConnection CONNECTED completes a join (NotifyPlayerJoinCompleted to everyone), setGameAttributes and
// setPlayerAttributes are kept and relayed (NotifyGameAttribChange, NotifyPlayerAttribChange), swapPlayersTeam moves
// players (NotifyGamePlayerTeamChange), removePlayer from the admin kicks, advanceGameState IN_GAME starts it. With a
// match host (--gms-dir) the host gets the lobby's roster on every change (updateRoster) and the final one at the start
// (startGame); it holds every HelloReq until then (docs/HOST_CONTROL.md). An admin who leaves before the start ends
// the game (the roster manager doesn't support another admin: "Admin migration not currently supported", 0x0069D850).

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/tdf"
)

const (
	CmdGameManagerAdvanceGameState    = 0x03
	CmdGameManagerSetGameAttributes   = 0x07
	CmdGameManagerSetPlayerAttributes = 0x08
	CmdGameManagerSwapPlayersTeam     = 0x70
	NotifyPlayerJoinCompletedID       = 0x1E
	NotifyGameAttribChangeID          = 0x50
	NotifyPlayerAttribChangeID        = 0x5A
	NotifyGamePlayerTeamChangeID      = 0x75

	PlayerStateActiveConnected = 4 // PlayerState ACTIVE_CONNECTED: the join completed
	RemovedPlayerKicked        = 8 // PlayerRemovedReason PLAYER_KICKED
	// SpectatorTeam is the spectators' Blaze team index (TIDS' third team): SPECTATE GAME joins with it (0x004DE690),
	// and the roster manager's third roster holds them (SpectatorPlayerIdList, 0x0069D8D0).
	SpectatorTeam = 2
	// The code halves of the GameManager errors used here (docs/data/blaze_components.txt).
	ErrGameFull               = 0x0004 // GAMEMANAGER_ERR_GAME_FULL
	ErrInvalidGameStateAction = 0x0006 // GAMEMANAGER_ERR_INVALID_GAME_STATE_ACTION
	ErrGameInProgress         = 0x000B // GAMEMANAGER_ERR_GAME_IN_PROGRESS
	ErrPermissionDenied       = 0x001E // GAMEMANAGER_ERR_PERMISSION_DENIED
	ErrTeamFull               = 0x00FF // GAMEMANAGER_ERR_TEAM_FULL

	// GameFlags (the game attribute; cGameEntryManager::Game_Create 0x00691DC0 by game type): 1 Matched, 2 Managed
	// (custom), 4 Solo, 0x40C/0x50C PracticeSolo, 0x412 PracticeGroup; cBlazeGameManager::CreateGame (0x00AC6850)
	// takes bit 0 as matched, else bit 2 as solo, else bit 1 as custom. 0x400 is BotsFillTeams.
	GameFlagMatched       = 0x1
	GameFlagCustom        = 0x2
	GameFlagSolo          = 0x4
	GameFlagBotsFillTeams = 0x400

	// The custom lobby's game attributes (cGameRosterManager; the key strings at 0x011F7888-0x011F78AC, 0x012B4934).
	AttrTeamRosters   = "TeamRostersKey"
	AttrRosterLocked  = "RosterLockedKey"
	AttrTeamsBalanced = "TeamsBalancedKey"
	AttrSpectators    = "SpectatorPlayerIdList"
	AttrPrivateMatch  = "PrivateMatch"  // "1": the browser's padlock (0x006810E0)
	AttrGameOwnerID   = "GameOwnerId"   // the browser asserts it (hex, 0x004E40A0)
	AttrGameOwnerName = "GameOwnerName" // the browser's Owner column
	AttrLevelID       = "LevelId"

	// BotFillMarker ends a custom game's name when its creator ticked the create dialog's bot checkbox (our addition
	// to MatchOptionsUI, BOTBOX): the server strips it, fills the open seats with bots and shows the game with
	// BotFillPrefix.
	BotFillMarker = "~#bots"
	BotFillPrefix = "[Bots] "
)

// IsLobbyGame reports whether a game with these GameFlags is made in a lobby first (B15): a custom game (2) or a
// practice group's (0x412): neither matched nor solo, the custom bit set.
func IsLobbyGame(flags int64) bool {
	return flags&GameFlagMatched == 0 && flags&GameFlagSolo == 0 && flags&GameFlagCustom != 0
}

// customLobby is a custom game's Blaze side beside its hostedGame (guarded by the hostedGame's mutex).
type customLobby struct {
	admins    []int64 // ADMN: the creator
	started   bool    // advanceGameState IN_GAME came (the host got startGame)
	browsable bool    // the game browser lists it: a custom game (a practice group's isn't)
	ready     bool    // NotifyGameSetup went out (its host is ready, or it has none): the browser may list it
	region    string  // PSAS for the browser: the creator's lowest-latency ping site
	nextSlot  int64   // the next joiner's Blaze slot (SID)
}

// LobbyPlayer is a custom game's player as NotifyGameSetup's PROS, NotifyPlayerJoining's PDAT and the game browser's
// ROST describe it.
type LobbyPlayer struct {
	Player
	Slot  int64      // SID: the Blaze slot, in join order
	Attrs []tdf.Pair // PATT: the player attributes (e.g. a private game's PrivateAuthToken)
	State int64      // STAT: ACTIVE_CONNECTING until its join completed, then ACTIVE_CONNECTED
}

// writeLobbyPlayer writes a Blaze::GameManager::ReplicatedGamePlayer (0x01461EA0) of a custom game: B8's members plus
// PATT when the player has attributes.
func writeLobbyPlayer(w *tdf.Writer, gameID int64, p LobbyPlayer) {
	w.Int("GID", gameID)
	w.Int("LOC", LocaleEnUS)
	w.String("NAME", p.Name)
	if len(p.Attrs) > 0 {
		w.Map("PATT", tdf.TypeString, tdf.TypeString, p.Attrs...)
	}
	w.Int("PID", p.ID)
	w.Union("PNET", NetworkAddressIpPair, func(w *tdf.Writer) {
		w.Struct("VALU", func(w *tdf.Writer) { ipPair(w, p.Ext, p.Int) })
	})
	w.Int("SID", p.Slot)
	w.Int("SLOT", SlotPublic)
	w.Int("STAT", p.State)
	w.Int("TIDX", p.Team)
	w.Int("TIME", p.JoinTime)
	w.Int("UID", p.ID)
}

// LobbyGameSetupNotification is NotifyGameSetup for a custom game's player: GAME with the admins and the attributes
// as they are now, PROS = every player in the game, REAS = DatalessSetupContext with dctx (CREATE_GAME 0 for its
// creator, JOIN_GAME 1 for a joiner).
func LobbyGameSetupNotification(g Game, admins []int64, players []LobbyPlayer, dctx int64) []byte {
	pros := make([]*tdf.Writer, len(players))
	for i, p := range players {
		pros[i] = tdf.NewWriter()
		writeLobbyPlayer(pros[i], g.ID, p)
	}
	w := tdf.NewWriter()
	w.Struct("GAME", func(w *tdf.Writer) { replicatedGameDataAdmins(w, g, admins) })
	w.List("PROS", tdf.TypeStruct, pros)
	w.Union("REAS", setupReasonDataless, func(w *tdf.Writer) {
		w.Struct("VALU", func(w *tdf.Writer) { w.Int("DCTX", dctx) })
	})
	return w.Bytes()
}

// LobbyPlayerJoiningNotification is NotifyPlayerJoining (0x15; 0x012E7B98: GID, PDAT) for a custom game's joiner.
func LobbyPlayerJoiningNotification(gameID int64, p LobbyPlayer) []byte {
	return tdf.NewWriter().Int("GID", gameID).Struct("PDAT", func(w *tdf.Writer) { writeLobbyPlayer(w, gameID, p) }).Bytes()
}

// PlayerJoinCompletedNotification is GameManager::NotifyPlayerJoinCompleted (0x1E; 0x012E7BB8: GID, PID), which the
// client's GameManagerAPI turns into onPlayerJoinComplete (game listener slot 11): the admin's roster manager adds the
// player there (0x006A11A0 -> AdminAddPlayer 0x006A0790, its Blaze team if < 3, else the first team with room).
func PlayerJoinCompletedNotification(gameID, playerID int64) []byte {
	return tdf.NewWriter().Int("GID", gameID).Int("PID", playerID).Bytes()
}

// GameAttribChangeNotification is GameManager::NotifyGameAttribChange (0x50; 0x012E7C18: ATTR map<string,string>,
// GID): the attributes a setGameAttributes changed (the roster manager reads TeamRostersKey and RosterLockedKey from
// it, game listener slot 5, 0x006A2670).
func GameAttribChangeNotification(gameID int64, attrs []tdf.Pair) []byte {
	return tdf.NewWriter().Map("ATTR", tdf.TypeString, tdf.TypeString, attrs...).Int("GID", gameID).Bytes()
}

// PlayerAttribChangeNotification is GameManager::NotifyPlayerAttribChange (0x5A; 0x012E7C80: ATTR, GID, PID).
func PlayerAttribChangeNotification(gameID, playerID int64, attrs []tdf.Pair) []byte {
	return tdf.NewWriter().Map("ATTR", tdf.TypeString, tdf.TypeString, attrs...).Int("GID", gameID).Int("PID", playerID).Bytes()
}

// GamePlayerTeamChangeNotification is GameManager::NotifyGamePlayerTeamChange (0x75; 0x012E7D38: GID, PID, TIDX):
// a player's Blaze team index after swapPlayersTeam (the team chat follows it, 0x00AAF220).
func GamePlayerTeamChangeNotification(gameID, playerID, team int64) []byte {
	return tdf.NewWriter().Int("GID", gameID).Int("PID", playerID).Int("TIDX", team).Bytes()
}

// TeamRosters is the custom lobby's roster as its admin's cGameRosterManager packs it into the game attribute
// TeamRostersKey (0x006A1570 -> 0x006A1460: u64 the packing (local) player's id, u16 the sequence, never 0; then
// cGameRosters::Pack 0x006A0F20: u8 the team count; per team cTeamRoster::Pack 0x006A0C40: u8 the player capacity
// (at most 5), u8 a bit per slot in use, then each used slot's u64 player id; all little-endian, base64 with the
// standard alphabet and a newline after every 76 characters (0x00834CC0), at most 160 bytes before the base64).
// Team 2 is the spectators.
type TeamRosters struct {
	Author   int64
	Sequence uint16
	Teams    [][]int64 // per team its slots in order, 0 = an empty slot
}

// DecodeTeamRosters reads a TeamRostersKey value.
func DecodeTeamRosters(s string) (TeamRosters, error) {
	var t TeamRosters
	raw, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\r", "", "\n", "").Replace(s))
	if err != nil {
		return t, err
	}
	if len(raw) < 11 {
		return t, errors.New("too short")
	}
	t.Author = int64(binary.LittleEndian.Uint64(raw))
	t.Sequence = binary.LittleEndian.Uint16(raw[8:])
	n, at := int(raw[10]), 11
	if n > 3 {
		return t, fmt.Errorf("%d teams", n)
	}
	for k := 0; k < n; k++ {
		if at+2 > len(raw) {
			return t, errors.New("truncated team")
		}
		capacity, mask := int(raw[at]), raw[at+1]
		at += 2
		if capacity > 5 {
			return t, fmt.Errorf("team %d capacity %d", k, capacity)
		}
		slots := make([]int64, capacity)
		for i := 0; i < capacity; i++ {
			if mask&(1<<i) == 0 {
				continue
			}
			if at+8 > len(raw) {
				return t, errors.New("truncated player")
			}
			slots[i] = int64(binary.LittleEndian.Uint64(raw[at:]))
			at += 8
		}
		t.Teams = append(t.Teams, slots)
	}
	return t, nil
}

// Encode packs t as the client does (for tests and logs; without the client's line breaks).
func (t TeamRosters) Encode() string {
	raw := binary.LittleEndian.AppendUint64(nil, uint64(t.Author))
	raw = binary.LittleEndian.AppendUint16(raw, t.Sequence)
	raw = append(raw, byte(len(t.Teams)))
	for _, slots := range t.Teams {
		var mask byte
		for i, id := range slots {
			if id != 0 {
				mask |= 1 << i
			}
		}
		raw = append(raw, byte(len(slots)), mask)
		for _, id := range slots {
			if id != 0 {
				raw = binary.LittleEndian.AppendUint64(raw, uint64(id))
			}
		}
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// attrValue is attribute key's value in attrs ("" when absent).
func attrValue(attrs []tdf.Pair, key string) string {
	for _, a := range attrs {
		if k, _ := a.Key.(string); strings.EqualFold(k, key) {
			v, _ := a.Value.(string)
			return v
		}
	}
	return ""
}

// lobbyRoster is the host's roster of a custom game's players (updateRoster, startGame): team 1 at gameplay indexes
// 0-4 and team 2 at 5-9 in the order of the admin's TeamRostersKey (when it names them; else in join order), the
// spectators (Blaze team 2) at 10-14 with team 0. The players that left are not on it.
func lobbyRoster(players []*hostedPlayer, attrs []tdf.Pair) []gmshost.RosterEntry {
	order := map[int64]int{} // player -> its place in the packed rosters
	if t, err := DecodeTeamRosters(attrValue(attrs, AttrTeamRosters)); err == nil {
		for k, slots := range t.Teams {
			for i, id := range slots {
				if id != 0 {
					order[id] = k*8 + i
				}
			}
		}
	}
	var teams [3][]*hostedPlayer
	for _, hp := range players {
		if hp.removed {
			continue
		}
		t := hp.p.Team
		if t < 0 || t > SpectatorTeam {
			t = 0
		}
		teams[t] = append(teams[t], hp)
	}
	var out []gmshost.RosterEntry
	for t, list := range teams {
		sort.SliceStable(list, func(a, b int) bool {
			oa, okA := order[list[a].p.ID]
			ob, okB := order[list[b].p.ID]
			switch {
			case okA && okB:
				return oa < ob
			case okA != okB:
				return okA
			}
			return list[a].slot < list[b].slot
		})
		for i, hp := range list {
			e := gmshost.RosterEntry{User: hp.p.ID, Name: hp.p.Name, Index: 5*t + i, Team: t + 1}
			if t == SpectatorTeam {
				e.Team, e.Spectator = 0, true
			}
			out = append(out, e)
		}
	}
	return out
}

// lobbyPlayersLocked is the game's players as its notifications describe them (hg.mu held).
func (hg *hostedGame) lobbyPlayersLocked() []LobbyPlayer {
	var out []LobbyPlayer
	for _, hp := range hg.players {
		if hp.removed {
			continue
		}
		out = append(out, hp.lobbyPlayer())
	}
	return out
}

func (hp *hostedPlayer) lobbyPlayer() LobbyPlayer {
	state := int64(PlayerStateActiveConnecting)
	if hp.completed {
		state = PlayerStateActiveConnected
	}
	return LobbyPlayer{Player: hp.p, Slot: hp.slot, Attrs: hp.attrs, State: state}
}

// lobbySetup is a custom game's NotifyGameSetup for hp (hostedGame.setup): the game as it is now (g carries the GMS
// address of the moment), the admins, every player, the player's setup context.
func (hg *hostedGame) lobbySetup(g Game, hp *hostedPlayer) []byte {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	g.Attrs = hg.game.Attrs
	g.State = hg.state
	return LobbyGameSetupNotification(g, hg.custom.admins, hg.lobbyPlayersLocked(), hp.setupCtx)
}

func (hg *hostedGame) isAdminLocked(id int64) bool {
	for _, a := range hg.custom.admins {
		if a == id {
			return true
		}
	}
	return false
}

func (hg *hostedGame) playerLocked(id int64) *hostedPlayer {
	for _, hp := range hg.players {
		if hp.p.ID == id {
			return hp
		}
	}
	return nil
}

// syncHostRoster sends the host the lobby's roster (updateRoster) after each change. Since B15b also after the start:
// then only spectators join (and players or spectators leave), and the host keeps startGame's players and takes the
// roster's spectators (gms/core/control WithSpectatorsOf), so a spectator of the running game may connect. A host from
// before B15b ignores the line after the start.
func (hg *hostedGame) syncHostRoster() {
	hg.mu.Lock()
	mt, started, ended := hg.match, hg.custom.started, hg.ended
	roster := lobbyRoster(hg.players, hg.game.Attrs)
	players := append([]*hostedPlayer(nil), hg.players...)
	hg.mu.Unlock()
	if mt == nil || ended {
		return
	}
	if !started {
		hg.s.lobbyContent(roster, players)
	}
	mt.UpdateRoster(roster)
}

// lobbyContent gives a custom game's roster players their account content for shaper select (with --shaper-select,
// as createGame's entries get it from shaperSelectSpec): the host replaces its roster with updateRoster's and
// startGame's, so a player who joined the lobby needs its own shapers, skins and perk pages there. Spectators pick
// nothing.
func (s *Server) lobbyContent(roster []gmshost.RosterEntry, players []*hostedPlayer) {
	if !s.ShaperSelect.On || s.Store == nil {
		return
	}
	for i := range roster {
		if roster[i].Bot || roster[i].Spectator {
			continue
		}
		for _, hp := range players {
			if hp.p.ID != roster[i].User {
				continue
			}
			if a, ok := s.Store.Account(hp.userID); ok {
				ShaperContent(s.Content, a, s.ShaperSelect.FreeRotation, &roster[i])
			}
			break
		}
	}
}

// lobbyRegion is the game browser's Region (PSAS) for a game its creator made: the ping site the creator's client
// reported with the lowest latency (updateNetworkInfo's NLMP: "na", "eu", ...; the browser shows the region it belongs
// to, 0x0066B3C0), "local" without one.
func lobbyRegion(sites map[string]int64) string {
	best, bestMs := "", int64(-1)
	for alias, ms := range sites {
		if bestMs < 0 || ms < bestMs || ms == bestMs && alias < best {
			best, bestMs = alias, ms
		}
	}
	if best == "" {
		return "local"
	}
	return best
}

// startCustomGame is resetDedicatedServer for a custom game (or a practice group's): the creator is its admin and
// Blaze slot 0. With a match host (--gms-dir) the host starts at once in mode custom and holds the lobby's HelloReqs;
// without one NotifyGameSetup names --gms-port at once, as B8's practice game.
func (s *Server) startCustomGame(r *Request, g Game, p Player) []byte {
	flags := int64(g.descriptors())
	g.Attrs = mergeAttrs(g.Attrs, []tdf.Pair{tdf.KV(ServerBuildVersionAttr, g.Version)})
	if p.Team < 0 || p.Team >= SpectatorTeam || flags&GameFlagBotsFillTeams != 0 {
		p.Team = 0 // a practice group's humans are all on team index 0, the bots on the other
	}
	hp := &hostedPlayer{p: p, userID: r.Conn.Session.Account.UserID, conn: r.Conn, setupCtx: SetupContextCreateGame}
	lobby := &customLobby{admins: []int64{p.ID}, browsable: flags&GameFlagBotsFillTeams == 0,
		region: lobbyRegion(r.Conn.Net.PingSites), nextSlot: 1}
	kind := "custom game"
	if !lobby.browsable {
		kind = "practice group game"
	}
	bots := false
	name, marked, tag, tagged := cutNameTags(g.Name)
	if marked || tagged {
		g.Name = name
		if g.Name == "" {
			g.Name = p.Name + "'s Game"
		}
	}
	if marked && lobby.browsable && s.Hosts != nil {
		// the create dialog's bot checkbox; a practice group gets its bots anyway and a lobby without a match host
		// has none to fill, so there only the marker goes
		bots = true
		g.Name = BotFillPrefix + g.Name
	}
	// the map: the server's, or the one the name's tag picks if this server offers it (level.go)
	level, why := s.hostedLevel(), "the server's map"
	if tagged {
		if s.customLevelOffered(tag.Name) {
			level, why = tag.Name, "the name's tag "+MapTagPrefix+tag.Tag
		} else {
			why = "the name's tag " + MapTagPrefix + tag.Tag + " asks for " + tag.Name + ", which this server doesn't " +
				"offer (--custom-maps), so the server's map"
		}
	}
	// MAPGUARD: a creator whose copy may lack the map's files gets SM_Map_v5 (mapguard.go)
	fellBack := false
	if lv, changed := s.createLevel(r, g.ID, level); changed {
		why = why + " " + level + ", but its creator's copy may lack its files (map check), so " + lv
		level, fellBack = lv, true
	}
	if !strings.EqualFold(level, s.hostedLevel()) {
		g.Level = level
	}
	g.Attrs = mergeAttrs(g.Attrs, levelAttrs(level)) // the game's level, not the dialog's
	if l, ok := FindLevel(level); ok && l.Label != "" {
		g.Name = l.Label + g.Name // the browser's Map column shows only SM_Map_v5's name
	}
	s.Log.Logf("blaze", "   game %d %q: a %s (GameFlags %#x, %d teams of %d, private %v); admin %d", g.ID, g.Name, kind,
		flags, len(g.TeamIDs), g.TeamCap, attrValue(g.Attrs, AttrPrivateMatch) == "1", p.ID)
	if bots {
		s.Log.Logf("blaze", "   game %d: bots fill the open seats (the creator's checkbox)", g.ID)
	}
	if tagged || fellBack || !strings.EqualFold(level, HostedLevel) {
		s.Log.Logf("blaze", "   game %d: map %s (LevelId %d; %s)", g.ID, level, LevelAssetID(level), why)
	}
	if s.Hosts != nil {
		fill := 0
		if (s.CustomBots || bots) && lobby.browsable {
			fill = int(g.TeamCap)
		}
		hg, err := s.launchHostedGame(g, gmshost.ModeCustom, fill, []*hostedPlayer{hp}, make(chan struct{}), nil, lobby)
		if err != nil {
			r.Fail(ErrNoHostCapacity)
			return nil
		}
		r.After(func() { close(hg.replied) })
		return JoinGameReply(g.ID)
	}
	// no match host: the lobby works on the Blaze side, NotifyGameSetup names --gms-port (B8)
	g.PersistedID = PersistedGameID(g.ID)
	hg := &hostedGame{s: s, game: g, mode: gmshost.ModeCustom, started: s.Now(), replied: make(chan struct{}),
		state: GameStatePreGame, players: []*hostedPlayer{hp}, custom: lobby}
	close(hg.replied)
	hg.setup = func(g Game, hp *hostedPlayer) []byte { return hg.lobbySetup(hp.conn.ForClient(g), hp) } // the tunnel (tunnel.go)
	lobby.ready = true
	s.hosted.add(hg)
	conn, id := r.Conn, p.ID
	conn.OnSessionEnd(func() { hg.sessionEnded(conn, id) })
	s.Log.Logf("blaze", "   game %d %q: GMS %s (HNET), no match host; player %d", g.ID, g.Name, g.GMS, p.ID)
	r.Notify(ComponentGameManager, NotifyGameSetupID, "NotifyGameSetup", hg.lobbySetup(r.Conn.ForClient(g), hp))
	r.After(func() { s.browserChanged(hg) })
	return JoinGameReply(g.ID)
}

// joinCustomGame is joinGame for a custom game (B15): JOIN GAME (TIDX 0xFFFF or a team) and SPECTATE GAME (TIDX 2)
// from the browser, or a player of the game coming back (a reconnect, or a lobby player who had left). A new player
// gets a team with room (0xFFFF: the smaller of teams 0 and 1), joins only before the start unless it spectates, and
// takes the next Blaze slot. A practice group's player always gets team 0, the group's own: its lobby shows only that
// team, and the bots fill the other (VPSLIVE: a client answering a stale invite asked for TIDX 1 and played unseen
// against its group). The reply is JoinGameResponse, then NotifyGameSetup with the join context; the others get
// NotifyPlayerJoining (the admin's roster manager adds the player at its NotifyPlayerJoinCompleted).
func (s *Server) joinCustomGame(r *Request, hg *hostedGame) []byte {
	me := r.Conn.Session.Persona.ID
	gid, _ := r.Body.Int("GID")
	tidx, ok := r.Body.Int("TIDX")
	if !ok {
		tidx = 0xFFFF
	}
	attrs := sortedAttrs(stringMap(r.Body, "ATTR"))
	ext, in := unionAddress(r.Body, "PNET")
	// MAPGUARD: a game on a map whose files the joiner's copy may lack (mapguard.go): JOIN, SPECTATE, an invite or a
	// join code's YES, a reconnect
	hg.mu.Lock()
	level, gname := s.gameLevel(hg.game), hg.game.Name
	hg.mu.Unlock()
	if s.mapJoinRefused(r, gid, gname, level) {
		return nil
	}
	hg.mu.Lock()
	fail := func(code uint16, why string) []byte {
		hg.mu.Unlock()
		s.Log.Logf("blaze", "   joinGame: game %d: %s; error reply", gid, why)
		r.Fail(code)
		return nil
	}
	if hg.ended || !hg.custom.ready {
		return fail(ErrInvalidGameID, "not running (or not ready yet)")
	}
	counts := [3]int64{}
	for _, x := range hg.players {
		if !x.removed && x.p.Team >= 0 && x.p.Team <= SpectatorTeam && x.p.ID != me {
			counts[x.p.Team]++
		}
	}
	hp := hg.playerLocked(me)
	back := hp != nil
	if !back {
		switch {
		case tidx == SpectatorTeam:
		case hg.custom.started:
			return fail(ErrGameInProgress, "started: only a spectator may join")
		case !hg.custom.browsable: // a practice group
			if tidx != 0 && tidx != 0xFFFF {
				s.Log.Logf("blaze", "   joinGame: game %d: player %d asked for team %d; a practice group's players join team 0",
					gid, me, tidx)
			}
			tidx = 0
		case tidx == 0 || tidx == 1:
		default: // 0xFFFF: any player team, the one with fewer players
			tidx = 0
			if counts[1] < counts[0] {
				tidx = 1
			}
		}
		if counts[tidx] >= hg.game.TeamCap {
			if tidx != SpectatorTeam && counts[0] >= hg.game.TeamCap && counts[1] >= hg.game.TeamCap {
				return fail(ErrGameFull, "both teams full")
			}
			return fail(ErrTeamFull, fmt.Sprintf("team %d full", tidx))
		}
		hp = &hostedPlayer{p: Player{ID: me, Name: r.Conn.Session.Persona.Name, Team: tidx, JoinTime: s.Now().UnixMicro()},
			userID: r.Conn.Session.Account.UserID, slot: hg.custom.nextSlot}
		hg.custom.nextSlot++
		hg.players = append(hg.players, hp)
	}
	hp.conn, hp.removed, hp.completed, hp.setupCtx = r.Conn, false, false, SetupContextJoinGame
	hp.p.Ext, hp.p.Int = s.ClientAddr(ext), s.ClientAddr(in)
	if len(attrs) > 0 {
		hp.attrs = mergeAttrs(hp.attrs, attrs)
	}
	g := hg.game
	g.State = hg.state
	admins := append([]int64(nil), hg.custom.admins...)
	players := hg.lobbyPlayersLocked()
	joining := hp.lobbyPlayer()
	started := hg.custom.started
	hg.mu.Unlock()
	conn := r.Conn
	conn.OnSessionEnd(func() { hg.sessionEnded(conn, me) })
	what := "joins"
	if back {
		what = "is back"
	}
	team := fmt.Sprintf("team %d", hp.p.Team)
	if hp.p.Team == SpectatorTeam {
		team = "the spectators"
	}
	s.Log.Logf("blaze", "   game %d: player %d %s (%s, slot %d, %d players now; started %v); NotifyGameSetup names the GMS %s",
		gid, me, what, team, hp.slot, len(players), started, g.GMS)
	r.Notify(ComponentGameManager, NotifyGameSetupID, "NotifyGameSetup", LobbyGameSetupNotification(r.Conn.ForClient(g), admins, players, SetupContextJoinGame))
	note := LobbyPlayerJoiningNotification(gid, joining)
	r.After(func() {
		hg.pushExcept(me, NotifyPlayerJoiningID, "NotifyPlayerJoining", note)
		hg.syncHostRoster()
		s.browserChanged(hg)
	})
	return JoinGameReply(gid)
}

// hostedJoinCompleted completes player's join of hosted game gid once its client reports the GMS connection
// (updateMeshConnection CONNECTED to the dedicated server): NotifyPlayerJoinCompleted to every player, once per join
// (a rejoin completes again). B15 sent it for custom games only; since TEAMCHAT for every hosted game (practice,
// matchmade, custom), as BlazeSDK's server does: the client's chat takes its team from it. The rooms manager's
// game listener slot 11 (onPlayerJoinComplete, vtable 0x012B3760 -> 0x00AB15C0 -> 0x00AB1540) sets the local
// player's team (+0xF0 = the player's team index) and joins TG-<game>-<team> (BlazeRoomsManager_UpdateTeamChatGroups
// 0x00AAF220); without it +0xF0 stays unset and the team send (0x00AB1E50) drops every line. The other listeners on
// the slot: cBlazeGameManager (0x00AC4AF0: for the local player it registers its game-report result handler, and it
// logs and posts the "N Player in game" message), MatchInviteUI (0x004E7C40: drops the player from its pending
// invites), the custom lobby's roster manager (0x006A11A0, custom games only); cGameEntryManager has none.
func (s *Server) hostedJoinCompleted(gid, player int64) {
	hg := s.hosted.get(gid)
	if hg == nil {
		return
	}
	hg.mu.Lock()
	hp := hg.playerLocked(player)
	if hp == nil || hp.removed || hp.completed || hg.ended {
		hg.mu.Unlock()
		return
	}
	hp.completed = true
	hg.mu.Unlock()
	s.Log.Logf("blaze", "   game %d: player %d's join completed (its GMS connection is up); NotifyPlayerJoinCompleted", gid, player)
	note := PlayerJoinCompletedNotification(gid, player)
	hg.push(NotifyPlayerJoinCompletedID, "NotifyPlayerJoinCompleted", func(*hostedPlayer) []byte { return note })
}

// customGameOf is the custom game gid for a request that changes it, with an error reply when there is none or the
// sender isn't one of its admins (adminOnly) or players.
func (s *Server) customGameOf(r *Request, gid int64, adminOnly bool, what string) *hostedGame {
	if r.Conn.Session == nil {
		r.Drop(what + " before login")
		return nil
	}
	me := r.Conn.Session.Persona.ID
	hg := s.hosted.get(gid)
	if hg == nil || hg.custom == nil {
		s.Log.Logf("blaze", "   %s: game %d is no custom game of ours; error reply", what, gid)
		r.Fail(ErrInvalidGameID)
		return nil
	}
	hg.mu.Lock()
	hp := hg.playerLocked(me)
	member := hp != nil && !hp.removed
	admin := hg.isAdminLocked(me)
	hg.mu.Unlock()
	if !member || adminOnly && !admin {
		s.Log.Logf("blaze", "   %s: player %d is no %s of game %d; error reply", what, me,
			map[bool]string{true: "admin", false: "player"}[adminOnly], gid)
		r.Fail(ErrPermissionDenied)
		return nil
	}
	return hg
}

// kickFromCustomGame is removePlayer naming another player: an admin removes it from its custom game. The host gets
// removePlayer, every player (the kicked one too) NotifyPlayerRemoved with the request's reason.
func (s *Server) kickFromCustomGame(r *Request, gid, pid, reason int64) {
	hg := s.customGameOf(r, gid, true, "removePlayer (kick)")
	if hg == nil {
		return
	}
	hg.mu.Lock()
	hp := hg.playerLocked(pid)
	if hp == nil || hp.removed {
		hg.mu.Unlock()
		s.Log.Logf("blaze", "   removePlayer (kick): player %d isn't in game %d", pid, gid)
		r.Fail(ErrPlayerNotFound)
		return
	}
	hp.removed = true
	kicked, mt, ended := hp.conn, hg.match, hg.ended
	hg.mu.Unlock()
	name := removedReasonName(reason)
	s.Log.Logf("blaze", "   game %d: player %d removed by admin %d (%s)", gid, pid, r.Conn.Session.Persona.ID, name)
	if mt != nil && !ended {
		mt.RemovePlayer(pid, name)
	}
	note := PlayerRemovedNotification(gid, pid, reason)
	r.After(func() {
		if kicked != nil {
			kicked.Push(s, ComponentGameManager, NotifyPlayerRemovedID, "NotifyPlayerRemoved", note)
		}
		hg.push(NotifyPlayerRemovedID, "NotifyPlayerRemoved", func(*hostedPlayer) []byte { return note })
		hg.lobbyLeft(pid)
	})
}

// lobbyLeft follows a player's removal from a custom game (it left, its session ended, it was kicked): before the
// start an admin's leave ends the game (nobody else may start it); otherwise the host gets the new roster, and the
// game browser the new player count. A game without a host ends with its last player.
func (hg *hostedGame) lobbyLeft(playerID int64) {
	if hg.custom == nil {
		return
	}
	s := hg.s
	hg.mu.Lock()
	started, ended, mt := hg.custom.started, hg.ended, hg.match
	wasAdmin := hg.isAdminLocked(playerID)
	left, adminLeft := 0, false
	for _, hp := range hg.players {
		if hp.removed {
			continue
		}
		left++
		if hg.isAdminLocked(hp.p.ID) {
			adminLeft = true
		}
	}
	hg.mu.Unlock()
	switch {
	case ended:
		return
	case left == 0: // a host was aborted by leave ("no players left")
		if mt == nil {
			hg.endLobby("no players left")
		}
		return
	case !started && wasAdmin && !adminLeft:
		hg.endLobby("its admin left the lobby")
		return
	}
	hg.syncHostRoster()
	s.browserChanged(hg)
}

// endLobby ends a custom game before its match: its host is aborted (its end then tells the players GAME_DESTROYED
// and takes the game out of the browser); a game without a host does both here.
func (hg *hostedGame) endLobby(why string) {
	s := hg.s
	hg.mu.Lock()
	mt := hg.match
	if mt == nil {
		hg.ended = true
	}
	hg.mu.Unlock()
	s.Log.Logf("blaze", "   game %d ends: %s", hg.game.ID, why)
	if mt != nil {
		mt.Abort(why)
		return
	}
	s.hosted.remove(hg.game.ID)
	hg.push(NotifyPlayerRemovedID, "NotifyPlayerRemoved", func(hp *hostedPlayer) []byte {
		return PlayerRemovedNotification(hg.game.ID, hp.p.ID, RemovedGameDestroyed)
	})
	s.browserRemoved(hg.game.ID)
}

// describeRosters is a TeamRostersKey for the log.
func describeRosters(v string) string {
	t, err := DecodeTeamRosters(v)
	if err != nil {
		return fmt.Sprintf("(undecodable: %v)", err)
	}
	var parts []string
	for k, slots := range t.Teams {
		names := []string{"team 1", "team 2", "spectators"}
		label := strconv.Itoa(k)
		if k < len(names) {
			label = names[k]
		}
		parts = append(parts, fmt.Sprintf("%s %v", label, slots))
	}
	return fmt.Sprintf("sequence %d by %d: %s", t.Sequence, t.Author, strings.Join(parts, ", "))
}

func registerCustomGames(s *Server) {
	registerGameBrowser(s)
	// setGameAttributes (cmd 7, Blaze::GameManager::SetGameAttributesRequest 0x012E79F8: ATTR, GID; sender 0x00C7C050,
	// a callback job without response class): Game::setGameAttributeMap (0x00C7FFF0), the roster manager's packed
	// rosters and lock. Only an admin may; every player gets NotifyGameAttribChange with the changed attributes.
	s.Register(ComponentGameManager, CmdGameManagerSetGameAttributes, "setGameAttributes", func(r *Request) []byte {
		gid, _ := r.Body.Int("GID")
		hg := r.Server.customGameOf(r, gid, true, "setGameAttributes")
		if hg == nil {
			return nil
		}
		set := sortedAttrs(stringMap(r.Body, "ATTR"))
		hg.mu.Lock()
		hg.game.Attrs = mergeAttrs(hg.game.Attrs, set)
		hg.mu.Unlock()
		var keys []string
		for _, a := range set {
			k, _ := a.Key.(string)
			v, _ := a.Value.(string)
			switch k {
			case AttrTeamRosters:
				keys = append(keys, k+" ("+describeRosters(v)+")")
			default:
				keys = append(keys, fmt.Sprintf("%s %q", k, v))
			}
		}
		r.Server.Log.Logf("blaze", "   game %d: attributes set: %s", gid, strings.Join(keys, ", "))
		note := GameAttribChangeNotification(gid, set)
		r.After(func() {
			hg.push(NotifyGameAttribChangeID, "NotifyGameAttribChange", func(*hostedPlayer) []byte { return note })
			hg.syncHostRoster() // the packed rosters order the host's slots
			r.Server.browserChanged(hg)
		})
		return nil
	})
	// setPlayerAttributes (cmd 8, SetPlayerAttributesRequest 0x012E7A18: ATTR, GID, PID; sender 0x00C7CC60 from
	// Player::setPlayerAttributeValue 0x00C826F0): the player itself or an admin; NotifyPlayerAttribChange to all.
	s.Register(ComponentGameManager, CmdGameManagerSetPlayerAttributes, "setPlayerAttributes", func(r *Request) []byte {
		gid, _ := r.Body.Int("GID")
		pid, _ := r.Body.Int("PID")
		self := r.Conn.Session != nil && pid == r.Conn.Session.Persona.ID
		hg := r.Server.customGameOf(r, gid, !self, "setPlayerAttributes")
		if hg == nil {
			return nil
		}
		set := sortedAttrs(stringMap(r.Body, "ATTR"))
		hg.mu.Lock()
		hp := hg.playerLocked(pid)
		if hp == nil || hp.removed {
			hg.mu.Unlock()
			r.Fail(ErrPlayerNotFound)
			return nil
		}
		hp.attrs = mergeAttrs(hp.attrs, set)
		hg.mu.Unlock()
		r.Server.Log.Logf("blaze", "   game %d: player %d's attributes set: %d", gid, pid, len(set))
		note := PlayerAttribChangeNotification(gid, pid, set)
		r.After(func() {
			hg.push(NotifyPlayerAttribChangeID, "NotifyPlayerAttribChange", func(*hostedPlayer) []byte { return note })
		})
		return nil
	})
	// swapPlayersTeam (cmd 0x70, SwapPlayersTeamRequest 0x012E7AC0: GID, LGAM list of SwapPlayerTeamData {PID, TIDX};
	// sender 0x0069DAF0 from the roster manager's sync 0x006A3840, generic job without response class): every player's
	// Blaze team index as the admin's rosters have it. Admin only; a team over its capacity refuses the whole list
	// (GAMEMANAGER_ERR_TEAM_FULL); every moved player's NotifyGamePlayerTeamChange goes to all. A practice group's
	// players stay on team 0: a move elsewhere is ignored.
	s.Register(ComponentGameManager, CmdGameManagerSwapPlayersTeam, "swapPlayersTeam", func(r *Request) []byte {
		gid, _ := r.Body.Int("GID")
		hg := r.Server.customGameOf(r, gid, true, "swapPlayersTeam")
		if hg == nil {
			return nil
		}
		type move struct{ pid, team int64 }
		var moves []move
		if f, ok := r.Body.Get("LGAM"); ok {
			if l, ok := f.Value.(*tdf.List); ok {
				for _, it := range l.Items {
					if st, ok := it.(*tdf.Struct); ok {
						pid, _ := st.Int("PID")
						team, _ := st.Int("TIDX")
						moves = append(moves, move{pid, team})
					}
				}
			}
		}
		hg.mu.Lock()
		teams := map[int64]int64{}
		for _, hp := range hg.players {
			if !hp.removed {
				teams[hp.p.ID] = hp.p.Team
			}
		}
		ignored := 0
		for _, m := range moves {
			if _, ok := teams[m.pid]; ok && m.team >= 0 && m.team < int64(len(hg.game.TeamIDs)) {
				if !hg.custom.browsable && m.team != 0 {
					ignored++
					continue
				}
				teams[m.pid] = m.team
			}
		}
		counts := map[int64]int64{}
		for _, t := range teams {
			counts[t]++
		}
		for t, n := range counts {
			if n > hg.game.TeamCap {
				hg.mu.Unlock()
				r.Server.Log.Logf("blaze", "   swapPlayersTeam: game %d: team %d would have %d players; error reply", gid, t, n)
				r.Fail(ErrTeamFull)
				return nil
			}
		}
		var moved []move
		for _, hp := range hg.players {
			if t, ok := teams[hp.p.ID]; ok && !hp.removed && t != hp.p.Team {
				hp.p.Team = t
				moved = append(moved, move{hp.p.ID, t})
			}
		}
		hg.mu.Unlock()
		r.Server.Log.Logf("blaze", "   game %d: teams swapped: %d asked, %d moved %v", gid, len(moves), len(moved), moved)
		if ignored > 0 {
			r.Server.Log.Logf("blaze", "   game %d: %d moves off team 0 ignored (a practice group)", gid, ignored)
		}
		r.After(func() {
			for _, m := range moved {
				note := GamePlayerTeamChangeNotification(gid, m.pid, m.team)
				hg.push(NotifyGamePlayerTeamChangeID, "NotifyGamePlayerTeamChange", func(*hostedPlayer) []byte { return note })
			}
			if len(moved) > 0 {
				hg.syncHostRoster()
				r.Server.browserChanged(hg)
				r.Server.armAutoStart(hg)
			}
		})
		return nil
	})
	// advanceGameState (cmd 3, AdvanceGameStateRequest 0x01461950: GID, GSTA; sender 0x00C7C200, a callback job
	// without response class; Game::startGame 0x00C7DDE0 sends it with IN_GAME from PRE_GAME). Admin only. IN_GAME starts
	// the custom game: NotifyGameStateChange IN_GAME to every player (it ends the client's Game_Start,
	// 0x00685610) and the host's startGame with the final roster.
	s.Register(ComponentGameManager, CmdGameManagerAdvanceGameState, "advanceGameState", func(r *Request) []byte {
		gid, _ := r.Body.Int("GID")
		state, _ := r.Body.Int("GSTA")
		hg := r.Server.customGameOf(r, gid, true, "advanceGameState")
		if hg == nil {
			return nil
		}
		hg.mu.Lock()
		start := state == GameStateInGame && !hg.custom.started
		if state == GameStateInGame && hg.custom.started {
			hg.mu.Unlock()
			r.Fail(ErrInvalidGameStateAction)
			return nil
		}
		if start {
			hg.custom.started = true
		}
		roster := lobbyRoster(hg.players, hg.game.Attrs)
		players := append([]*hostedPlayer(nil), hg.players...)
		mt := hg.match
		hg.mu.Unlock()
		if start {
			r.Server.Log.Logf("blaze", "   game %d starts (its admin's advanceGameState IN_GAME): roster %s", gid, describeRoster(roster))
			if mt != nil {
				r.Server.lobbyContent(roster, players)
				mt.StartGame(roster)
			}
		} else {
			r.Server.Log.Logf("blaze", "   game %d: advanceGameState %#x", gid, state)
		}
		r.After(func() { hg.setState(state) })
		return nil
	})
}

// onlySpectators reports whether every human still in the custom game spectates and it hasn't started (hg.mu held).
func (hg *hostedGame) onlySpectators() bool {
	if hg.custom == nil || hg.custom.started {
		return false
	}
	n := 0
	for _, hp := range hg.players {
		if hp.removed {
			continue
		}
		if hp.p.Team != SpectatorTeam {
			return false
		}
		n++
	}
	return n > 0
}

// armAutoStart (dgserver --custom-autostart) starts a custom game CustomAutoStart after a team change that left only
// spectators in it: its lobby offers the admin no Start Game without players on the teams, so a game of bots alone (with
// CustomBots) needs it. A human joining a team before then keeps the game in its lobby.
func (s *Server) armAutoStart(hg *hostedGame) {
	if s.CustomAutoStart <= 0 || hg.custom == nil || !hg.custom.browsable {
		return
	}
	hg.mu.Lock()
	ok := hg.onlySpectators()
	hg.mu.Unlock()
	if !ok {
		return
	}
	s.Log.Logf("blaze", "   game %d: every human spectates: it starts by itself in %v (--custom-autostart)", hg.game.ID, s.CustomAutoStart)
	time.AfterFunc(s.CustomAutoStart, func() { s.autoStartCustom(hg) })
}

// autoStartCustom is armAutoStart's start: the admin's advanceGameState IN_GAME's (the host's startGame with the final
// roster, NotifyGameStateChange IN_GAME to every player), unless a human joined a team or the game started meanwhile.
func (s *Server) autoStartCustom(hg *hostedGame) {
	hg.mu.Lock()
	if !hg.onlySpectators() {
		hg.mu.Unlock()
		s.Log.Logf("blaze", "   game %d: no start by itself (a human is on a team, or it started)", hg.game.ID)
		return
	}
	hg.custom.started = true
	roster := lobbyRoster(hg.players, hg.game.Attrs)
	players := append([]*hostedPlayer(nil), hg.players...)
	mt := hg.match
	hg.mu.Unlock()
	s.Log.Logf("blaze", "   game %d starts (--custom-autostart: every human spectates): roster %s", hg.game.ID, describeRoster(roster))
	if mt != nil {
		s.lobbyContent(roster, players)
		mt.StartGame(roster)
	}
	hg.setState(GameStateInGame)
}

// describeRoster is a roster for the log: "user@index/team" each, "spectator" for an observer.
func describeRoster(roster []gmshost.RosterEntry) string {
	var parts []string
	for _, e := range roster {
		if e.Spectator {
			parts = append(parts, fmt.Sprintf("%d@%d/spectator", e.User, e.Index))
		} else {
			parts = append(parts, fmt.Sprintf("%d@%d/team %d", e.User, e.Index, e.Team))
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
