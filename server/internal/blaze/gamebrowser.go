package blaze

// B15: the game browser (PLAY > CUSTOM > BROWSE GAMES, MatchBrowserUI), read statically from Dawngate.exe 854
// (docs/BLAZE.md *Custom games and spectators (B15)*, *The game browser*):
//
//   - The browser's game entry manager session (0x0068DDD0) asks GameManagerAPI::getGameBrowserList (0x00C83510) for
//     list "BGDefaultList" with capacity 1000, as a subscription: GameManager::getGameListSubscription (cmd 0x65,
//     sender 0x00C7CA80, job 0x012E5590: GetGameListResponse {GLID, MAXF, NGD}, error class MatchmakingCriteriaError);
//     a snapshot would be getGameListSnapshot (cmd 0x64, the same request and reply). g3b_run10b's request: DNAM
//     'BGDefaultList', GLID (MatchmakingCriteriaData: the rules all empty), GVER the client's version, IGNO 1, LCAP
//     1000, NOJM 1 (289 bytes).
//   - The reply's GLID becomes a GameBrowserList (0x00C82330; for a job that is gone the client destroys the list,
//     destroyGameList cmd 0x66 {GLID}); NotifyGameListUpdate (0xC9: DONE, GLID, REMV list of game ids, UPDT list of
//     GameBrowserMatchData {FIT, GAM GameBrowserGameData}) is handed to the list with that GLID (0x00C87AD0): REMV
//     drops games, UPDT adds or replaces them (0x00C85C90 -> 0x00C82F40 copies GID, GNAM, GSTA, GSET, PRES, VOIP, CAP,
//     PCNT, TINF, TCAP, ATTR, CRIT, ADMN, PSID, PSAS, NTOP, HOST, HNET, QCAP, QCNT, ROST), DONE goes to the list's +0xB.
//   - A row (0x004E40A0 -> 0x004E2BF0): Game Name GNAM; Owner the attribute GameOwnerName (GameOwnerId, hex, must not be
//     0: an assert); Slots TINF's team sizes of teams 0 and 1 over 2 x TCAP; Spectators team 2's size over TCAP; Map the
//     attribute LevelId (2490188286 = 0x946D41FE, the one playable map's display name); Region PSAS (a ping site
//     alias, mapped to its region's name by 0x0066B3C0); the padlock for the attribute PrivateMatch "1". JOIN GAME
//     needs VSTR to equal the client's own version (0x004E3AB0; else a version dialog).
//
// What dgserver lists: its custom games (not a practice group's) once their NotifyGameSetup went out, until they end,
// in the lobby and while they run (SPECTATE GAME). A subscription gets every change (a game made, joined, left, its
// attributes, its state) as NotifyGameListUpdate until destroyGameList or the end of the session.

import (
	"fmt"
	"sort"
	"sync"

	"dawngate/server/internal/tdf"
)

const (
	CmdGameManagerGetGameListSnapshot     = 0x64
	CmdGameManagerGetGameListSubscription = 0x65
	CmdGameManagerDestroyGameList         = 0x66
	NotifyGameListUpdateID                = 0xC9
	// GameListFit is every listed game's fit score (GameBrowserMatchData FIT) and the reply's MAXF [assumed: the
	// browser shows no fit; every custom game fits a list without rules].
	GameListFit = 100
)

// ListedGame is a game as the game browser shows it (GameBrowserGameData).
type ListedGame struct {
	Game    Game          // its attributes as they are now
	State   int64         // GSTA
	Admins  []int64       // ADMN
	Region  string        // PSAS
	Players []LobbyPlayer // ROST, TINF and PCNT
}

// browserGameData writes a Blaze::GameManager::GameBrowserGameData (0x01461800) for the game browser: ADMN, ATTR,
// CAP, GID, GNAM, GSET, GSTA, HNET (the GMS), HOST (the dedicated server), NTOP, PCNT [players, 0], PRES, PSAS, ROST
// (GameBrowserPlayerData 0x01461A88 per player: EXID, LOC, NAME, PATT, PID, STAT, TIDX), TCAP, TINF (GameBrowserTeamInfo
// 0x012E73A8 per team of TIDS: TID, TSZE the team's players), VSTR. CRIT, GURL, PSID, QCAP, QCNT, SID and VOIP keep
// their defaults.
func browserGameData(w *tdf.Writer, lg ListedGame) {
	g := lg.Game
	w.List("ADMN", tdf.TypeInt, lg.Admins)
	w.Map("ATTR", tdf.TypeString, tdf.TypeString, sortedAttrs(g.Attrs)...)
	w.List("CAP", tdf.TypeInt, g.Capacity)
	w.Int("GID", g.ID)
	w.String("GNAM", g.Name)
	w.Int("GSET", g.Settings)
	w.Int("GSTA", lg.State)
	w.List("HNET", tdf.TypeStruct, []tdf.UnionItem{{Member: NetworkAddressIpPair,
		Body: func() *tdf.Writer { x := tdf.NewWriter(); ipPair(x, g.GMS, g.GMS); return x }()}})
	w.Int("HOST", DedicatedServerID)
	w.Int("NTOP", TopologyClientServerDedicated)
	w.List("PCNT", tdf.TypeInt, []int64{int64(len(lg.Players)), 0})
	w.Int("PRES", PresenceModeStandard)
	w.String("PSAS", lg.Region)
	rost := make([]*tdf.Writer, len(lg.Players))
	sizes := make([]int64, len(g.TeamIDs))
	for i, p := range lg.Players {
		x := tdf.NewWriter().Int("EXID", 0).Int("LOC", LocaleEnUS).String("NAME", p.Name)
		if len(p.Attrs) > 0 {
			x.Map("PATT", tdf.TypeString, tdf.TypeString, p.Attrs...)
		}
		rost[i] = x.Int("PID", p.ID).Int("STAT", p.State).Int("TIDX", p.Team)
		if p.Team >= 0 && p.Team < int64(len(sizes)) {
			sizes[p.Team]++
		}
	}
	w.List("ROST", tdf.TypeStruct, rost)
	w.Int("TCAP", g.TeamCap)
	tinf := make([]*tdf.Writer, len(g.TeamIDs))
	for i, id := range g.TeamIDs {
		tinf[i] = tdf.NewWriter().Int("TID", id).Int("TSZE", sizes[i])
	}
	w.List("TINF", tdf.TypeStruct, tinf)
	w.String("VSTR", g.Version)
}

// GameListReply is getGameListSubscription's (and getGameListSnapshot's) reply, Blaze::GameManager::GetGameListResponse
// (0x012E7418: GLID uint64, MAXF uint32, NGD uint32 the number of games).
func GameListReply(listID, games int64) []byte {
	return tdf.NewWriter().Int("GLID", listID).Int("MAXF", GameListFit).Int("NGD", games).Bytes()
}

// GameListUpdateNotification is GameManager::NotifyGameListUpdate (0xC9; 0x012E74C0: DONE uint8, GLID, REMV list<int>
// the removed game ids, UPDT list of GameBrowserMatchData 0x012E7474 {FIT, GAM}). DONE is 1 (the list is complete);
// REMV and UPDT are left out when empty, as the client's own encoder leaves out empty lists (B10b).
func GameListUpdateNotification(listID int64, updated []ListedGame, removed []int64) []byte {
	w := tdf.NewWriter().Int("DONE", 1).Int("GLID", listID)
	if len(removed) > 0 {
		w.List("REMV", tdf.TypeInt, removed)
	}
	if len(updated) > 0 {
		items := make([]*tdf.Writer, len(updated))
		for i, lg := range updated {
			items[i] = tdf.NewWriter().Int("FIT", GameListFit).Struct("GAM", func(w *tdf.Writer) { browserGameData(w, lg) })
		}
		w.List("UPDT", tdf.TypeStruct, items)
	}
	return w.Bytes()
}

// gameBrowser keeps the game lists the clients asked for.
type gameBrowser struct {
	mu    sync.Mutex
	next  int64
	lists map[int64]*browserList
}

type browserList struct {
	id       int64
	conn     *Conn
	owner    int64
	capacity int64
	shown    map[int64]bool // the game ids the client's list has
}

// listed is what the browser shows of hg; false when it doesn't list it (not a custom game, not ready, ended).
func (hg *hostedGame) listed() (ListedGame, bool) {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	if hg.custom == nil || !hg.custom.browsable || !hg.custom.ready || hg.ended {
		return ListedGame{}, false
	}
	return ListedGame{Game: hg.game, State: hg.state, Admins: append([]int64(nil), hg.custom.admins...),
		Region: hg.custom.region, Players: hg.lobbyPlayersLocked()}, true
}

// listedGames is every game the browser lists, by game id.
func (s *Server) listedGames() []ListedGame {
	s.hosted.mu.Lock()
	all := make([]*hostedGame, 0, len(s.hosted.byID))
	for _, hg := range s.hosted.byID {
		all = append(all, hg)
	}
	s.hosted.mu.Unlock()
	var out []ListedGame
	for _, hg := range all {
		if lg, ok := hg.listed(); ok {
			out = append(out, lg)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Game.ID < out[b].Game.ID })
	return out
}

// browserChanged sends every subscribed list hg as it is now (UPDT), or its removal (REMV) from the lists that have it
// when the browser no longer lists it.
func (s *Server) browserChanged(hg *hostedGame) {
	lg, ok := hg.listed()
	if !ok {
		s.browserRemoved(hg.game.ID)
		return
	}
	type push struct {
		conn *Conn
		note []byte
	}
	var pushes []push
	s.browser.mu.Lock()
	for _, l := range s.browser.lists {
		if !l.shown[lg.Game.ID] && int64(len(l.shown)) >= l.capacity {
			continue
		}
		l.shown[lg.Game.ID] = true
		pushes = append(pushes, push{l.conn, GameListUpdateNotification(l.id, l.conn.forClientListed([]ListedGame{lg}), nil)})
	}
	s.browser.mu.Unlock()
	for _, p := range pushes {
		p.conn.Push(s, ComponentGameManager, NotifyGameListUpdateID, "NotifyGameListUpdate", p.note)
	}
}

// browserRemoved takes game gid out of every list that has it.
func (s *Server) browserRemoved(gid int64) {
	type push struct {
		conn *Conn
		note []byte
	}
	var pushes []push
	s.browser.mu.Lock()
	for _, l := range s.browser.lists {
		if l.shown[gid] {
			delete(l.shown, gid)
			pushes = append(pushes, push{l.conn, GameListUpdateNotification(l.id, nil, []int64{gid})})
		}
	}
	s.browser.mu.Unlock()
	for _, p := range pushes {
		p.conn.Push(s, ComponentGameManager, NotifyGameListUpdateID, "NotifyGameListUpdate", p.note)
	}
}

func registerGameBrowser(s *Server) {
	list := func(subscribe bool) func(r *Request) []byte {
		return func(r *Request) []byte {
			if r.Conn.Session == nil {
				r.Drop("game list before login")
				return nil
			}
			name, _ := r.Body.Str("DNAM")
			capacity, ok := r.Body.Int("LCAP")
			if !ok || capacity <= 0 {
				capacity = 0xFFFFFFFF
			}
			games := r.Server.listedGames()
			if int64(len(games)) > capacity {
				games = games[:int(capacity)]
			}
			b := &r.Server.browser
			b.mu.Lock()
			b.next++
			l := &browserList{id: b.next, conn: r.Conn, owner: r.Conn.Session.Persona.ID, capacity: capacity,
				shown: map[int64]bool{}}
			for _, g := range games {
				l.shown[g.Game.ID] = true
			}
			if subscribe {
				if b.lists == nil {
					b.lists = map[int64]*browserList{}
				}
				b.lists[l.id] = l
			}
			b.mu.Unlock()
			kind := "snapshot"
			if subscribe {
				kind = "subscription"
				id, conn := l.id, r.Conn
				conn.OnSessionEnd(func() { r.Server.dropGameList(id, conn) })
			}
			ids := make([]int64, len(games))
			for i, g := range games {
				ids[i] = g.Game.ID
			}
			r.Server.Log.Logf("blaze", "   game list %d (%q, %s, capacity %d): games %v", l.id, name, kind, capacity, ids)
			r.Notify(ComponentGameManager, NotifyGameListUpdateID, "NotifyGameListUpdate", GameListUpdateNotification(l.id, r.Conn.forClientListed(games), nil))
			return GameListReply(l.id, int64(len(games)))
		}
	}
	s.Register(ComponentGameManager, CmdGameManagerGetGameListSubscription, "getGameListSubscription", list(true))
	s.Register(ComponentGameManager, CmdGameManagerGetGameListSnapshot, "getGameListSnapshot", list(false))
	// destroyGameList (cmd 0x66, DestroyGameListRequest 0x012E7440: GLID; sender 0x00C7B740, generic job without
	// response class): the list's updates stop.
	s.Register(ComponentGameManager, CmdGameManagerDestroyGameList, "destroyGameList", func(r *Request) []byte {
		id, _ := r.Body.Int("GLID")
		if !r.Server.dropGameList(id, r.Conn) {
			r.Server.Log.Logf("blaze", "   destroyGameList: list %d is none of this connection's", id)
		}
		return nil
	})
}

// dropGameList forgets list id of conn; false if conn has no such list.
func (s *Server) dropGameList(id int64, conn *Conn) bool {
	b := &s.browser
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.lists[id]
	if l == nil || l.conn != conn {
		return false
	}
	delete(b.lists, id)
	s.Log.Logf("blaze", "   game list %d destroyed", id)
	return true
}

// String is a listed game for logs.
func (lg ListedGame) String() string {
	return fmt.Sprintf("game %d %q (%d players, state %#x)", lg.Game.ID, lg.Game.Name, len(lg.Players), lg.State)
}
