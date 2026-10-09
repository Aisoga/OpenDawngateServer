// Surrender on the match host, the engine side (roadmap D8b; docs/MATCH_FLOW.md *Surrender and the karma vote (D8b-core)*
// and *D8b: surrender and the karma vote on the match host*). The rules are gms/core/surrender's SurrenderVote, which
// ServerMatch (gms_host.cpp) owns, one per match: it adds the players at HelloReq, reports their GMS connections, passes
// the clients' CA 01/CA 02 to it, ticks it with what this module reads and ends the match through
// match::EndBySurrender when a team's vote passed. This module only reads and writes the server simulator (the server
// simulator bound, every engine access under SEH):
//   - the level's LevelGameplayTuning surrender values (+0x38..+0x60; SM_Map_v5: 20 min, 20000 gold, 19 kills, 300 s
//     disconnected, a 60 s vote, a 300 s cooldown, 0.666), which the vote's availability, length and pass rule use;
//   - the team totals of the availability's economy and kill rules: every server player's mKills (+0x240) and
//     mTotalGold (+0xAC) summed per team, the bots' included [assumed: the team's whole score];
//   - mSurrenderAvailablity (cBattlegroundsUIData +0x08, 8B member 3), which the replication's 8B delta pass carries
//     to every client: its in-game menu enables SURRENDER GAME while it isn't 0.
#pragma once

#include <cstdint>

#include "core/surrender/surrender.h"

namespace surr {

// The level's surrender tuning into `t`; false (t unchanged: SM_Map_v5's values) if it can't be read.
bool ReadTuning(void* sim, gms::surrender::Tuning* t);
// The team totals (kills, total gold; index 1 and 2) of every server player 0-9; false on an exception.
bool ReadTeams(void* sim, gms::surrender::MatchView* m);
// mSurrenderAvailablity of the server's UI data; false on an exception.
bool WriteUiAvailability(void* sim, std::uint8_t value);

}  // namespace surr
