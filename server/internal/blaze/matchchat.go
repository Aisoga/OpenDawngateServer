package blaze

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"dawngate/server/internal/tdf"
)

// A match's chats (SEC6): the game chat "GG-<game id>" (B11) and the team chats "TG-<game id>-<team>" (TEAMCHAT) are
// keyed playgroups the client creates or joins itself (createPlaygroup, JOIN 1). Their keys are easy to guess (game
// ids count from 1), so the server decides who may be in them, from its own record of the game:
//
//   - GG-<game>: every player of the game who is still in it (not removed), spectators included;
//   - TG-<game>-<team>: the players on that Blaze team index, and the game's spectators (team 2) for every team of
//     their game, since the client has a spectator join all three (BlazeRoomsManager_UpdateTeamChatGroups 0x00AAF220)
//     and a spectator sees both teams anyway.
//
// The record is the hosted game (hostedGame.players: matchmade, practice and custom games with a match host, and
// custom lobbies without one) and, for a server without match hosts, the unhosted rosters below. A refused create
// or join is PLAYGROUPS_ERR_NOT_AUTHORIZED: the client's chat join callback (BlazeClientPlaygroup_OnJoinResult
// 0x00AACAD0) treats every error alike (asserts snblaze_rooms.cpp 557 and 604, "Failed to create or join chat
// playgroup") and doesn't retry (its desired key equals the key it tried). The group's info is the server's
// (matchChatInfo), whatever the request asked for, its member limit isn't enforced (the roster bounds it), and the
// lobby commands (attributes, join controls, kick, destroy) are refused on it. A player who leaves the game, is
// removed or changes team leaves the groups it may no longer be in (pruneMatchChats).

// matchChat is a parsed match chat key.
type matchChat struct {
	game int64
	team int64 // TG- only
	tg   bool
}

// MatchChatMemberLimit is the MLIM a match chat reports: the one the client asks for (GG- and TG- alike, in every
// createPlaygroup the live client sent).
const MatchChatMemberLimit = 15

// isMatchChatKey reports whether key is in a match chat's name space (GG- or TG-), well formed or not.
func isMatchChatKey(key string) bool {
	return strings.HasPrefix(key, "GG-") || strings.HasPrefix(key, "TG-")
}

// parseMatchChatKey reads GG-<game> or TG-<game>-<team> in the client's form ("GG-%llu", 0x012B33B4; the team 0-2):
// ok is false for any other spelling of a key in that name space (a leading zero, a sign, a fourth part).
func parseMatchChatKey(key string) (matchChat, bool) {
	num := func(s string) (int64, bool) {
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil && n >= 0 && strconv.FormatInt(n, 10) == s
	}
	if g, ok := strings.CutPrefix(key, "GG-"); ok {
		n, ok := num(g)
		return matchChat{game: n}, ok && n > 0
	}
	if rest, ok := strings.CutPrefix(key, "TG-"); ok {
		g, t, ok := strings.Cut(rest, "-")
		if !ok {
			return matchChat{}, false
		}
		n, ok1 := num(g)
		team, ok2 := num(t)
		return matchChat{game: n, team: team, tg: true}, ok1 && ok2 && n > 0 && team <= SpectatorTeam
	}
	return matchChat{}, false
}

// matchChatInfo is the info of a match chat as the server keeps it: what the client's own request carries (NAME and
// UKEY the key, ATTR PlaygroupKey, MLIM 15, everything else 0), so a legitimate client sees the same bytes.
func matchChatInfo(key string) PlaygroupInfo {
	return PlaygroupInfo{Name: key, Key: key, Attrs: []tdf.Pair{tdf.KV(PlaygroupKeyAttr, key)}, MemberLimit: MatchChatMemberLimit}
}

// matchChatAllowed says whether persona may be in the match chat key (a GG-/TG- key), and why not.
func (s *Server) matchChatAllowed(key string, persona int64) (bool, string) {
	mc, ok := parseMatchChatKey(key)
	if !ok {
		return false, "not a match chat key of the client's form"
	}
	team, in := s.matchTeamOf(mc.game, persona)
	switch {
	case !in:
		return false, fmt.Sprintf("not a player of game %d", mc.game)
	case mc.tg && team != mc.team && team != SpectatorTeam:
		return false, fmt.Sprintf("on team %d of game %d, not team %d", team, mc.game, mc.team)
	}
	return true, ""
}

// matchTeamOf is persona's Blaze team index in game, in false when the server's record of the game doesn't list it
// as a player still in it (or there is no such game).
func (s *Server) matchTeamOf(game, persona int64) (team int64, in bool) {
	if hg := s.hosted.get(game); hg != nil {
		hg.mu.Lock()
		defer hg.mu.Unlock()
		if hp := hg.playerLocked(persona); hp != nil && !hp.removed {
			return hp.p.Team, true
		}
		return 0, false
	}
	return s.unhosted.teamOf(game, persona)
}

// pruneMatchChats takes every member out of game's match chats who may no longer be in them (it left the game, was
// removed, changed team). The members that stay get NotifyMemberRemovedFromPlaygroup (REAS 0, as for the client's
// own leavePlaygroup); the one taken out gets nothing: its client leaves the group itself on its removal or team
// change (an empty reply, nothing left to remove), and until then it simply gets no more of the group's messages.
func (s *Server) pruneMatchChats(game int64) {
	type entry struct {
		pg     int64
		key    string
		member int64
	}
	var all []entry
	s.groups.mu.Lock()
	for key, pg := range s.groups.byKey {
		if mc, ok := parseMatchChatKey(key); ok && mc.game == game {
			for _, m := range pg.members {
				all = append(all, entry{pg.info.ID, key, m.BlazeID})
			}
		}
	}
	s.groups.mu.Unlock()
	for _, e := range all {
		if ok, why := s.matchChatAllowed(e.key, e.member); !ok {
			s.Log.Logf("blaze", "   playgroup %d %q: %d taken out (%s)", e.pg, e.key, e.member, why)
			s.playgroupLeft(e.pg, e.member, nil, PlaygroupMemberRemoveDefault)
		}
	}
}

// removedLeft follows a player's removal from a hosted game while its session goes on (its removePlayer, a lobby kick,
// the operator's kick): its match chats, then the custom lobby's part (lobbyLeft). A lost session needs no prune: the
// playgroups' own session-end hook takes the member out (REAS 1 DISCONNECTED, as before).
func (hg *hostedGame) removedLeft(playerID int64) {
	hg.s.pruneMatchChats(hg.game.ID)
	hg.lobbyLeft(playerID)
}

// unhostedRosters are the players of the practice and matchmade games of a server without match hosts (dgserver
// without --gms-dir: NotifyGameSetup names --gms-port, B8), which keeps no hosted game for them: by game id, each
// player's team. A player leaves with its removePlayer or its session's end.
type unhostedRosters struct {
	mu     sync.Mutex
	byGame map[int64]map[int64]int64
}

func (u *unhostedRosters) add(game, persona, team int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.byGame == nil {
		u.byGame = map[int64]map[int64]int64{}
	}
	if u.byGame[game] == nil {
		u.byGame[game] = map[int64]int64{}
	}
	u.byGame[game][persona] = team
}

// remove takes persona out of game; false if it wasn't in it.
func (u *unhostedRosters) remove(game, persona int64) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	ps := u.byGame[game]
	if _, ok := ps[persona]; !ok {
		return false
	}
	delete(ps, persona)
	if len(ps) == 0 {
		delete(u.byGame, game)
	}
	return true
}

func (u *unhostedRosters) teamOf(game, persona int64) (int64, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	t, ok := u.byGame[game][persona]
	return t, ok
}

// unhostedJoined records p as a player of the unhosted game, until conn's session ends or its removePlayer.
func (s *Server) unhostedJoined(conn *Conn, game int64, p Player) {
	s.unhosted.add(game, p.ID, p.Team)
	if conn != nil {
		// the playgroups' own session-end hook takes the member out of the chats (REAS 1)
		conn.OnSessionEnd(func() { s.unhosted.remove(game, p.ID) })
	}
}

// unhostedLeft takes persona out of the unhosted game and its match chats.
func (s *Server) unhostedLeft(game, persona int64) {
	if s.unhosted.remove(game, persona) {
		s.pruneMatchChats(game)
	}
}
