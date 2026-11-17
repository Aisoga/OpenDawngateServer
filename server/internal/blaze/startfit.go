package blaze

import (
	"fmt"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/rules"
)

// startTooLarge checks a custom game's built startGame against its match host's line limit before the start commits
// (a host skips a longer line without a word, and the lobby then hung on its START for everyone). On a miss it returns
// the lobby.startRefused finding for the admin and the log's reason; ok when the line fits.
func startTooLarge(mt *gmshost.Match, sg gmshost.StartGame) (f rules.Finding, why string, ok bool) {
	size, fits := mt.Fits(sg)
	if fits {
		return rules.Finding{}, "", true
	}
	limit := mt.LineLimit()
	f = rules.Finding{Category: rules.FindingHost, Message: fmt.Sprintf("The game can't start: its player data is too "+
		"large for the match host (%d of %d bytes). Please report this.", size, limit)}
	why = fmt.Sprintf("its startGame is %d bytes, over the match host's line limit of %d bytes", size, limit)
	return f, why, false
}

// refuseLargeStart is the miss of a start without a request (the autostart; the draft's end has its own, draft.go):
// the game stays in its lobby (hg.custom.started reset), the roster unlocked, and the admins get lobby.startRefused
// with the finding.
func (s *Server) refuseLargeStart(hg *hostedGame, what string, f rules.Finding, why string) {
	hg.mu.Lock()
	hg.custom.started = false
	tell := hg.unlockRosterLocked()
	hg.mu.Unlock()
	s.Log.Logf("blaze", "   game %d: %s refused: %s; the lobby stays", hg.game.ID, what, why)
	tell(startRefusedData([]rules.Finding{f}))
}

// startRefusedData is lobby.startRefused's data with findings (refuseStart's).
func startRefusedData(findings []rules.Finding) any {
	return struct {
		Findings []rules.Finding `json:"findings"`
	}{findings}
}
