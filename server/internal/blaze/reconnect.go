package blaze

// G3: a player whose client lost a running hosted game comes back into it (docs/BLAZE.md *Rejoining a running game
// (G3)*). The client's side, read from Dawngate.exe 854 (read-only Ghidra):
//   - NotifyGameSetup's ReplicatedGameData PGID (the persisted game id, table 0x014615A8 +0x1E0, kept at the game's
//     +0x158) is what cBlazeGameManager::SaveReconnectGameId (0x00AC2680) persists with BGOps::setGameID {ACID 0, GMID}
//     (0x00AC2570) once the game is set up; GMID '' clears it (the game's end, POST_GAME, a removal other than a lost
//     connection, a failed reconnect check). Before G3 our NotifyGameSetup had no PGID, so the client saved ''.
//   - At the next login, with the app property EnableReconnects (Config.package: bool true) the client sends
//     BGOps::getGameID; a non-empty GMID makes HandleGetGameIDResponse (0x00AC4040) send GameManager::getGameDataFromId
//     {PIDL [GMID]} (cmd 0x69, GetGameDataFromIdRequest 0x012E748C: DNAM, GLST list<int>, PIDL list<string>;
//     FUN_00ac3eb0 pushes the string), answered by a GameBrowserDataList (0x012E74B4: GDAT list of GameBrowserGameData
//     0x01461800).
//   - Its callback DiscoCheck_GetGameInfoCB (0x00AC2930) takes the first game if its VSTR equals the client's own
//     version string, its GSTA is IN_GAME (0x83) and PCNT's first entry isn't 0, reads SpectatorPlayerIdList from its
//     ATTR (whether the player was a spectator), and raises event 0x37F0634E with the game id (LoginState waits for it);
//     otherwise it clears the saved id. The main menu then shows DiscoGameDlg; its accept calls
//     cGameEntryManager::Game_JoinReconnect (0x00693210) -> JoinGame(reconnect, 0x006928F0) -> getGameDataFromId again
//     -> GetGameInfoForJoinCB (0x00AC5880): GSTA IN_GAME and the player's team index from the game attributes
//     PlayerIdList_TeamCount ("<n>") and PlayerIdList_Team_<i> (the team's player ids, u64 little-endian, base64:
//     0x00AB46F0, 0x00AB44A0 "(dataLength % perEntryBytes) == 0", at most 10) -> GameManager::joinGame (cmd 9,
//     JoinGameRequest 0x01461470) through 0x00C865D0 with that team index. Without the attributes it logs "Failed to
//     retrieve team index from game attribute map. Cannot reconnect to game as we do not know which team to join."
//   - The join is a JoinGameResponse {GID, JGS} and NotifyGameSetup (here with DatalessSetupContext
//     JOIN_GAME_SETUP_CONTEXT); the client connects to the GMS again and the host gives the user its old slot (the
//     session's RejoinMap; gms_host.cpp's Rejoining).
// What dgserver does: every hosted game gets a PGID; setGameID keeps each persona's GMID (memory only: a game doesn't
// outlive dgserver), getGameID returns it; getGameDataFromId answers for running hosted games (by PIDL or GLST), with
// the PlayerIdList attributes from the roster; joinGame lets a roster player of a running hosted game back in (its
// removal undone, its new connection's session end hooked) and sends it the game, and the players still in it get
// NotifyPlayerJoining [the original's notification for a joiner; not read by the client's GMS path].

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"dawngate/server/internal/tdf"
)

const (
	CmdGameManagerJoinGame          = 0x09
	CmdGameManagerGetGameDataFromID = 0x69
	NotifyPlayerJoiningID           = 0x15
	SetupContextJoinGame            = 1 // DatalessSetupContext.DCTX JOIN_GAME_SETUP_CONTEXT
	// The code halves of GAMEMANAGER_ERR_INVALID_GAME_ID (0x00020004) and GAMEMANAGER_ERR_PLAYER_NOT_FOUND (0x00650004).
	ErrInvalidGameID  = 0x0002
	ErrPlayerNotFound = 0x0065
	// The game attributes GetGameInfoForJoinCB reads (strings at 0x01456B40/44/48).
	AttrPlayerIDTeamCount = "PlayerIdList_TeamCount"
	AttrPlayerIDTeam      = "PlayerIdList_Team_"
)

// persistedPrefix makes a hosted game's PGID; the id after it is the Blaze game id.
const persistedPrefix = "dawngate-game-"

// PersistedGameID is the PGID of game id.
func PersistedGameID(id int64) string { return persistedPrefix + strconv.FormatInt(id, 10) }

// gameIDOfPersisted is the game id a PGID names (false for anything else).
func gameIDOfPersisted(pgid string) (int64, bool) {
	if !strings.HasPrefix(pgid, persistedPrefix) {
		return 0, false
	}
	id, err := strconv.ParseInt(pgid[len(persistedPrefix):], 10, 64)
	return id, err == nil && id > 0
}

// reconnectIDs keeps what each persona last saved with BGOps::setGameID.
type reconnectIDs struct {
	mu   sync.Mutex
	byID map[int64]string
}

func (r *reconnectIDs) set(persona int64, gmid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byID == nil {
		r.byID = map[int64]string{}
	}
	if gmid == "" {
		delete(r.byID, persona)
	} else {
		r.byID[persona] = gmid
	}
}

func (r *reconnectIDs) get(persona int64) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byID[persona]
}

// PlayerIDListAttrs are the game attributes GetGameInfoForJoinCB finds a player's team in: PlayerIdList_TeamCount and
// one PlayerIdList_Team_<i> per team (base64 of the team's player ids as u64 little-endian).
func PlayerIDListAttrs(teams [][]int64) []tdf.Pair {
	out := []tdf.Pair{tdf.KV(AttrPlayerIDTeamCount, strconv.Itoa(len(teams)))}
	for i, ids := range teams {
		raw := make([]byte, 8*len(ids))
		for k, id := range ids {
			binary.LittleEndian.PutUint64(raw[8*k:], uint64(id))
		}
		out = append(out, tdf.KV(fmt.Sprintf("%s%d", AttrPlayerIDTeam, i), base64.StdEncoding.EncodeToString(raw)))
	}
	return out
}

// sortedAttrs merges attribute lists in the order the client looks them up in. Its attribute map is a vector that
// keeps the wire order and is searched with a lower bound over _stricmp (0x0066D5B0 with 0x00C6F020), so the keys
// must come sorted case-insensitively (lower case: '_' < 'c'): byte order put PlayerIdList_TeamCount before
// PlayerIdList_Team_0, and the lookup missed it ("Team count: 0", g3_run6d).
func sortedAttrs(lists ...[]tdf.Pair) []tdf.Pair {
	var out []tdf.Pair
	for _, l := range lists {
		out = append(out, l...)
	}
	slices.SortStableFunc(out, func(a, b tdf.Pair) int { return StricmpCompare(a.Key.(string), b.Key.(string)) })
	return out
}

// StricmpCompare orders two keys as MSVC's _stricmp does: byte by byte after mapping A-Z to lower case.
func StricmpCompare(a, b string) int {
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

// BrowserGame is one running game as getGameDataFromId describes it.
type BrowserGame struct {
	Game    Game
	State   int64     // GSTA (IN_GAME 0x83 for a reconnect)
	Admin   int64     // ADMN's one entry
	Teams   [][]int64 // player ids by team index (PlayerIdList_Team_<i>; g3_run5d: "PlayerIdList_Team<i>" left the teams empty)
	Players int64     // PCNT's first entry
}

// GameBrowserData is GameManager::getGameDataFromId's reply, Blaze::GameManager::GameBrowserDataList (0x012E74B4: GDAT
// list of GameBrowserGameData 0x01461800). Per game: ADMN, ATTR (the game's attributes plus the PlayerIdList ones from
// Teams), CAP, GID, GNAM, GSET, GSTA, HNET (the GMS), NTOP, PCNT [players, 0], PRES, PSAS, TCAP, VSTR.
func GameBrowserData(games []BrowserGame) []byte {
	items := make([]*tdf.Writer, len(games))
	for i, b := range games {
		g := b.Game
		w := tdf.NewWriter()
		w.List("ADMN", tdf.TypeInt, []int64{b.Admin})
		w.Map("ATTR", tdf.TypeString, tdf.TypeString, sortedAttrs(gameAttrs(g), PlayerIDListAttrs(b.Teams))...)
		w.List("CAP", tdf.TypeInt, g.Capacity)
		w.Int("GID", g.ID)
		w.String("GNAM", g.Name)
		w.Int("GSET", g.Settings)
		w.Int("GSTA", b.State)
		w.List("HNET", tdf.TypeStruct, []tdf.UnionItem{{Member: NetworkAddressIpPair,
			Body: func() *tdf.Writer { x := tdf.NewWriter(); ipPair(x, g.GMS, g.GMS); return x }()}})
		w.Int("NTOP", TopologyClientServerDedicated)
		w.List("PCNT", tdf.TypeInt, []int64{b.Players, 0})
		w.Int("PRES", PresenceModeStandard)
		w.String("PSAS", "local")
		w.Int("TCAP", g.TeamCap)
		w.String("VSTR", g.Version)
		items[i] = w
	}
	return tdf.NewWriter().List("GDAT", tdf.TypeStruct, items).Bytes()
}

// RejoinGameSetupNotification is NotifyGameSetup for a player joining a running game again: GAME as at the start (PGID,
// GSTA = the game's state now), PROS = every player, REAS = DatalessSetupContext JOIN_GAME_SETUP_CONTEXT.
func RejoinGameSetupNotification(g Game, players []Player) []byte {
	pros := make([]*tdf.Writer, len(players))
	for i, p := range players {
		pros[i] = replicatedGamePlayer(g, p, int64(i))
	}
	w := tdf.NewWriter()
	w.Struct("GAME", func(w *tdf.Writer) { replicatedGameData(w, g, players[0].ID) })
	w.List("PROS", tdf.TypeStruct, pros)
	w.Union("REAS", setupReasonDataless, func(w *tdf.Writer) {
		w.Struct("VALU", func(w *tdf.Writer) { w.Int("DCTX", SetupContextJoinGame) })
	})
	return w.Bytes()
}

// PlayerJoiningNotification is GameManager::NotifyPlayerJoining (0x15; Blaze::GameManager::NotifyPlayerJoining
// 0x012E7B98: GID, PDAT ReplicatedGamePlayer).
func PlayerJoiningNotification(g Game, p Player, slot int64) []byte {
	return tdf.NewWriter().Int("GID", g.ID).Struct("PDAT", func(w *tdf.Writer) {
		writeReplicatedGamePlayer(w, g, p, slot)
	}).Bytes()
}

// stringList reads a list<string> member of a request.
func stringList(s *tdf.Struct, label string) []string {
	var out []string
	if f, ok := s.Get(label); ok {
		if l, ok := f.Value.(*tdf.List); ok {
			for _, it := range l.Items {
				if v, ok := it.(string); ok {
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// browserGame is what getGameDataFromId says about a hosted game; false once it has ended.
func (hg *hostedGame) browserGame() (BrowserGame, bool) {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	if hg.ended {
		return BrowserGame{}, false
	}
	b := BrowserGame{Game: hg.game, State: hg.state, Teams: [][]int64{{}, {}}}
	for i, hp := range hg.players {
		if i == 0 {
			b.Admin = hp.p.ID
		}
		t := int(hp.p.Team)
		for len(b.Teams) <= t {
			b.Teams = append(b.Teams, []int64{})
		}
		b.Teams[t] = append(b.Teams[t], hp.p.ID)
		if !hp.removed {
			b.Players++
		}
	}
	if b.Players == 0 {
		b.Players = int64(len(hg.players)) // the leaver asks while it is the only one (its own removal counted)
	}
	return b, true
}

// rejoin lets a roster player of a running game back in on conn: false if it isn't on the roster or the game has
// ended. Returns the game (its state now) and every player.
func (hg *hostedGame) rejoin(conn *Conn, playerID int64) (Game, []Player, *hostedPlayer, bool) {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	if hg.ended || hg.match == nil {
		return Game{}, nil, nil, false
	}
	var me *hostedPlayer
	players := make([]Player, len(hg.players))
	for i, hp := range hg.players {
		players[i] = hp.p
		if hp.p.ID == playerID {
			me = hp
		}
	}
	if me == nil {
		return Game{}, nil, nil, false
	}
	me.removed = false
	me.conn = conn
	me.completed = false // TEAMCHAT: its new GMS connection completes the join again (the client's team chat)
	g := hg.game
	g.State = hg.state
	return g, players, me, true
}

func registerReconnect(s *Server) {
	// getGameDataFromId: the running hosted games the request names (PIDL: PGIDs, GLST: game ids).
	s.Register(ComponentGameManager, CmdGameManagerGetGameDataFromID, "getGameDataFromId", func(r *Request) []byte {
		var ids []int64
		for _, p := range stringList(r.Body, "PIDL") {
			if id, ok := gameIDOfPersisted(p); ok {
				ids = append(ids, id)
			} else {
				r.Server.Log.Logf("blaze", "   getGameDataFromId: persisted id %q is none of ours", p)
			}
		}
		ids = append(ids, intList(r.Body, "GLST")...)
		var games []BrowserGame
		for _, id := range ids {
			if hg := r.Server.hosted.get(id); hg != nil {
				if b, ok := hg.browserGame(); ok {
					b.Game = r.Conn.ForClient(b.Game) // a tunnelled client sees its tunnel address
					games = append(games, b)
					r.Server.Log.Logf("blaze", "   getGameDataFromId: game %d, state %#x, %d players in it, teams %v", id,
						b.State, b.Players, b.Teams)
					continue
				}
			}
			r.Server.Log.Logf("blaze", "   getGameDataFromId: game %d is not running", id)
		}
		return GameBrowserData(games)
	})
	// joinGame: G3's reconnect into a running hosted game (the client's Game_JoinReconnect); nothing else joins by id.
	s.Register(ComponentGameManager, CmdGameManagerJoinGame, "joinGame", func(r *Request) []byte {
		if r.Conn.Session == nil {
			r.Drop("joinGame before login")
			return nil
		}
		gid, _ := r.Body.Int("GID")
		tidx, _ := r.Body.Int("TIDX")
		me := r.Conn.Session.Persona.ID
		hg := r.Server.hosted.get(gid)
		if hg == nil {
			r.Server.Log.Logf("blaze", "   joinGame: game %d has no running host", gid)
			r.Fail(ErrInvalidGameID)
			return nil
		}
		if hg.custom != nil { // B15: the game browser's JOIN GAME / SPECTATE GAME, or a reconnect into a custom game
			return r.Server.joinCustomGame(r, hg)
		}
		g, players, hp, ok := hg.rejoin(r.Conn, me)
		if !ok {
			r.Server.Log.Logf("blaze", "   joinGame: player %d is not on game %d's roster (or it ended)", me, gid)
			r.Fail(ErrPlayerNotFound)
			return nil
		}
		conn := r.Conn
		conn.OnSessionEnd(func() { hg.sessionEnded(conn, me) })
		r.Server.Log.Logf("blaze", "   game %d: player %d rejoins (team index %d asked, %d on the roster); NotifyGameSetup "+
			"names the GMS %s again", gid, me, tidx, hp.p.Team, g.GMS)
		r.Notify(ComponentGameManager, NotifyGameSetupID, "NotifyGameSetup", RejoinGameSetupNotification(r.Conn.ForClient(g), players))
		slot := int64(0)
		for i, p := range players {
			if p.ID == me {
				slot = int64(i)
			}
		}
		joining := PlayerJoiningNotification(g, hp.p, slot)
		r.After(func() {
			hg.pushExcept(me, NotifyPlayerJoiningID, "NotifyPlayerJoining", joining)
		})
		return JoinGameReply(gid)
	})
}

// pushExcept sends a notification to every player still in the game but `except`.
func (hg *hostedGame) pushExcept(except int64, id uint16, name string, payload []byte) {
	hg.mu.Lock()
	var to []*hostedPlayer
	for _, hp := range hg.players {
		if !hp.removed && hp.p.ID != except {
			to = append(to, hp)
		}
	}
	hg.mu.Unlock()
	for _, hp := range to {
		hp.conn.Push(hg.s, ComponentGameManager, id, name, payload)
	}
}
