// MAPRULES4: a map's own victory rules and start level on the match host. The rules are engine-free in
// core/matchflow/maprules.h (gms_tests MapRules*); this part holds the match's rules, reads the teams' kills each tick,
// sends the alerts, ends the match through match::EndByRule and grants the start level.
// Where the rules come from: createGame.mapRules {kills, minutes, startLevel, bindings} (host/control.cpp calls
// SetFromCreate when createGame arrives; the host names the feature "maprules" in its hello, "maprules2" for bindings),
// startGame.victory {kills, minutes, bindings} (WINBIND-B: the lobby's win condition; SetFromStart sets the victory and
// keeps createGame's start level, in either order), or the test overrides [host] map_kills, map_minutes, start_level
// and map_bindings (0 = createGame's; set, each wins over createGame's and startGame's member). The host clamps the
// values (gms::maprules::Clamp).
// The Binding race (WINBIND-B): match.cpp's TowerDestroyed calls OnBindingDestroyed with the destroying team (3 - the
// Binding's owner) for every destruction, a respawned Binding's next one too; Tick hands the counts to the race, and
// hostctl::Finish puts them into finishGame.bindings when a Binding race ran (Bindings).
// Hooks: match::Tick calls Tick after the Guardian check while the match runs; death::OnPlayerDeployed calls
// OnSeatDeployed for every seat's deploy (humans and bots), and the next Tick grants that seat the start level, one
// level step per grant (cmds::GrantXpNow), so a bot's own LabsPlayer_GrantXP ranks its skill order level by level.
#pragma once

#include <cstddef>
#include <cstdint>

#include "core/matchflow/maprules.h"

namespace maprules {

// At the install: the test overrides (0 = none).
void Configure(int kills, int minutes, int startLevel, int bindings);
// createGame's mapRules (an absent member is 0).
void SetFromCreate(long long kills, long long minutes, long long startLevel, long long bindings);
// startGame's victory (the lobby's win condition, WINBIND-B): replaces the victory, keeps the start level.
void SetFromStart(long long kills, long long minutes, long long bindings);
// A Binding of the other team was destroyed by `team` (1 or 2): counted for the Binding race.
void OnBindingDestroyed(int team, std::uint32_t id, double t);
// Each team's destructions of enemy Bindings so far; true when a Binding race runs (finishGame.bindings is sent).
bool Bindings(int out[2]);
const gms::maprules::Rules& Current();

// A seat's shaper was deployed (the starting gold's moment): the start level is granted at the next Tick, once a seat.
void OnSeatDeployed(int index);
// Every match tick while the match runs (server simulator bound), after the Guardian check.
void Tick(void* sim, double matchSeconds);

// "map rules kills 3, minutes 0, start level 6: kills 2-1, sudden death no, alerts 4 (failed 0), levelled 10 seats".
void FormatStats(char* buf, std::size_t size);

}  // namespace maprules
