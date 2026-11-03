package blaze

import (
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/store"
)

// The custom lobby tiles' ratings (RATINGSHOW3, ready.go's sendRatings): a class holder hears "~#rat <n>~" in the name
// of every player of the game with a shown rating (Server.ShownRating) at its hi and again after every roster change
// (the admin's TeamRostersKey); a player below the Leaderboard's rated games gets none; a client's own rat line is
// swallowed and ignored; with show_rating off nothing is sent.
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
		g := golden.Load(t)
		a, b, c, d := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("charlie@example.com"),
			ts.client("delta@example.com")
		wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
		a.note(ComponentGameManager, NotifyGameSetupID)
		join := golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))
		wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, join))
		wantReply(t, "C joins", c.gm(CmdGameManagerJoinGame, join))
		wantReply(t, "D spectates", d.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))))
		gg := joinGG(t, a, b, c, d)
		line := func(text string) []byte {
			return msg(MessageTypeChat, MessageFlagEcho|MessageFlagFilter, ObjTypePlaygroup, chatLine(text), gg)
		}
		quiet := func(cs ...*tcpClient) {
			t.Helper()
			for _, x := range cs {
				x.none(ComponentMessaging, NotifyMessageID, 60*time.Millisecond)
			}
		}
		ratingsTo := func(x *tcpClient) {
			t.Helper()
			if show {
				x.readyFrom(a, "~#rat 1612~")
				x.readyFrom(b, "~#rat 1388~")
				x.readyFrom(d, "~#rat 2000~") // a spectator too; C (9 rated games) none
			}
		}

		// the admin's hi: every shown rating, its own too
		wantOK(t, "A hi", a.sendMsg(line("~#rdy hi~")))
		ratingsTo(a)
		quiet(a, b, c, d)
		// B's hi: its ready state first (as READY2), then the ratings; to B only
		wantOK(t, "B hi", b.sendMsg(line("~#rdy hi~")))
		b.readyFrom(b, "~#rdy 0~")
		a.readyFrom(b, "~#rdy 0~")
		ratingsTo(b)
		quiet(a, b, c, d)
		// a client's own rat line: swallowed and ignored (no client could show a number in its own name)
		wantOK(t, "C's own rating line", c.sendMsg(line("~#rat 3000~")))
		quiet(a, b, c, d)
		// a roster change (the admin's TeamRostersKey): every holder again; C has no class, so it hears nothing
		wantReply(t, "attributes", a.gm(CmdGameManagerSetGameAttributes, golden.Bytes(t, decodeCase(t, g, "setgameattributes_request"))))
		ratingsTo(a)
		ratingsTo(b)
		quiet(a, b, c, d)
		// after the start: none
		wantOK(t, "B ready", b.sendMsg(line("~#rdy 1~")))
		a.readyFrom(b, "~#rdy 1~")
		b.readyFrom(b, "~#rdy 1~")
		wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
		wantReply(t, "attributes after the start", a.gm(CmdGameManagerSetGameAttributes, golden.Bytes(t, decodeCase(t, g, "setgameattributes_request"))))
		quiet(a, b, c, d)

		log := ts.log.String()
		if !strings.Contains(log, "a lobby line only the server sends (rat); ignored") {
			t.Errorf("show %v: log without the ignored rat line", show)
		}
		if got := strings.Contains(log, "the tiles' ratings"); got != show {
			t.Errorf("show %v: ratings logged %v", show, got)
		}
	}
}
