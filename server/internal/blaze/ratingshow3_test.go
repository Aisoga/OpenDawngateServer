package blaze

import (
	"testing"
	"time"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/store"
)

// The custom lobby tiles' ratings (RATINGSHOW3): lobby.state names the rating of every player of the game whose rating
// players see (Server.ShownRating), spectators too, at every publish, so a newcomer's rating reaches everyone at the
// roster change; a player below the Leaderboard's rated games has none; with show_rating off there are none.
func TestLobbyRatings(t *testing.T) {
	for _, show := range []bool{true, false} {
		ratings := map[string]store.Rating{
			"alpha@example.com":   {Value: 1611.6, RD: 80, Vol: 0.06, Games: 12},
			"bravo@example.com":   {Value: 1388.2, RD: 90, Vol: 0.06, Games: 10},
			"charlie@example.com": {Value: 1700, RD: 120, Vol: 0.06, Games: 9}, // provisional: shown nowhere
			"delta@example.com":   {Value: 2000, RD: 60, Vol: 0.06, Games: 15},
		}
		ts := newTCPServer(t, func(s *Server) {
			s.ShowRating = show
			for _, email := range []string{"alpha@example.com", "bravo@example.com", "charlie@example.com", "delta@example.com"} {
				acct, _, err := s.Store.Login(email, time.Unix(testTime, 0))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.Store.Update(acct.UserID, func(a *store.Account) { a.History.Rating = ratings[email] }); err != nil {
					t.Fatal(err)
				}
			}
		})
		addr := attachUI(t, ts)
		g := golden.Load(t)
		a, b, c, d := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("charlie@example.com"),
			ts.client("delta@example.com")
		wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
		a.note(ComponentGameManager, NotifyGameSetupID)
		join := golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))
		wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, join))
		wantReply(t, "C joins", c.gm(CmdGameManagerJoinGame, join))
		wantReply(t, "D spectates", d.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))))
		ua, ub := uiConnect(t, addr, a), uiConnect(t, addr, b)

		// state is the lobby as everyone who entered hears it; bState is B's ready mark
		state := func(bState string) string {
			if show {
				return lobbyStateLine(a.name+" - 1612", b.name+" "+bState+" 1388", c.name, d.name+" - 2000")
			}
			return lobbyStateLine(a.name, b.name+" "+bState, c.name, d.name)
		}

		// the admin enters: every shown rating, its own too (B isn't in the check yet)
		ua.call(CallLobbyEnter, "")
		if show {
			ua.want(lobbyStateLine(a.name+" - 1612", b.name+" - 1388", c.name, d.name+" - 2000"))
		} else {
			ua.want(lobbyStateLine(a.name, b.name, c.name, d.name))
		}
		// B enters: both hear the lobby
		ub.call(CallLobbyEnter, "")
		ua.want(state("0"))
		ub.want(state("0"))
		// a roster change (the admin's TeamRostersKey): everyone who entered again; C and D have no class and hear nothing
		wantReply(t, "attributes", a.gm(CmdGameManagerSetGameAttributes, golden.Bytes(t, decodeCase(t, g, "setgameattributes_request"))))
		ua.want(state("0"))
		ub.want(state("0"))
		// after the start: none
		ub.call(CallLobbySetReady, `{"ready":true}`)
		ua.want(state("1"))
		ub.want(state("1"))
		wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
		wantReply(t, "attributes after the start", a.gm(CmdGameManagerSetGameAttributes, golden.Bytes(t, decodeCase(t, g, "setgameattributes_request"))))
		ua.quiet()
		ub.quiet()
	}
}
