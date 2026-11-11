package blaze

import (
	"strings"
	"testing"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/logging"
)

// DRAFT3: --test-roles gives startGame's humans their roles in roster order, cycling, skipping bots, spectators and a
// human that has one already; without the aid nothing changes. It runs for every start (rules or not).
func TestApplyTestRoles(t *testing.T) {
	roster := func() []gmshost.RosterEntry {
		return []gmshost.RosterEntry{
			{User: 2000000001, Index: 0, Team: 1},
			{Index: 1, Team: 1, Bot: true},
			{User: 2000000002, Index: 5, Team: 2},
			{User: 2000000003, Index: 6, Team: 2, Role: "predator"},
			{User: 2000000004, Index: 7, Team: 2},
			{User: 2000000005, Index: 10, Spectator: true},
		}
	}
	var log strings.Builder
	s := &Server{Log: logging.New(&log)}
	sg := gmshost.StartGame{Roster: roster()}
	s.applyPickRules(1, &sg, nil)
	for _, e := range sg.Roster {
		if e.Role != map[int]string{6: "predator"}[e.Index] {
			t.Errorf("without the aid index %d got role %q", e.Index, e.Role)
		}
	}
	s.TestRoles = []string{"hunter", "gladiator"}
	sg = gmshost.StartGame{Roster: roster(), Rules: &gmshost.Rules{Pick: "blind", ShaperPool: "all", Shop: "normal",
		ItemPool: "all"}}
	s.applyPickRules(1, &sg, nil)
	want := map[int]string{0: "hunter", 1: "", 5: "gladiator", 6: "predator", 7: "hunter", 10: ""}
	for _, e := range sg.Roster {
		if e.Role != want[e.Index] {
			t.Errorf("index %d: role %q, want %q", e.Index, e.Role, want[e.Index])
		}
	}
	if !strings.Contains(log.String(), "game 1: declared roles (--test-roles, a test aid): user 2000000001 (index 0) hunter; "+
		"user 2000000002 (index 5) gladiator; user 2000000004 (index 7) hunter") {
		t.Errorf("log:\n%s", log.String())
	}
}
