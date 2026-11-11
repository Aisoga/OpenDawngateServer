package blaze

import (
	"fmt"
	"strings"

	"dawngate/server/internal/gmshost"
)

// The declared roles in startGame (DRAFT3): a human's roster entry carries the role it declared before the match
// (gmshost.RosterEntry.Role), and the match host presets it in the human's shaper select record, so the client's role
// page opens with that role card selected; the player can still pick another one there. The draft (DRAFT1) declares
// them; until it does, the test aid --test-roles gives them.

// applyTestRoles gives startGame's humans (not bots, not spectators) without a role the test aid's roles in roster
// order, cycling (Server.TestRoles; none: nothing). Logged per game.
func (s *Server) applyTestRoles(gid int64, sg *gmshost.StartGame) {
	if len(s.TestRoles) == 0 {
		return
	}
	var parts []string
	k := 0
	for i := range sg.Roster {
		e := &sg.Roster[i]
		if e.Bot || e.Spectator || e.Role != "" {
			continue
		}
		e.Role = s.TestRoles[k%len(s.TestRoles)]
		k++
		parts = append(parts, fmt.Sprintf("user %d (index %d) %s", e.User, e.Index, e.Role))
	}
	if len(parts) > 0 {
		s.Log.Logf("blaze", "   game %d: declared roles (--test-roles, a test aid): %s", gid, strings.Join(parts, "; "))
	}
}
