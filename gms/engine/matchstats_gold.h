// M18 (MSTATS2): gold and XP by source and the roles' bonuses on the match host. The rules are gms/core/stats/gold.h
// (engine-free); this module feeds its RoleLedger from the host's own grant code and the damage core, and at the match's end reads each seat's match-telemetry record and adds the keys to
// matchstats' accumulator:
//   - OnRoleGold: death.cpp GrantGoldAll, a reward's role part (VIMPOPUP's bonus, or ROLEVIM-2's own grant);
//   - OnHandlerChange: death.cpp OnGrantGoldHook, an OnGrantGold handler that changed a grant's amount;
//   - OnPredatorXp: death.cpp PlayerKillRewards, the role's XP modifier's part of a kill or assist XP grant;
//   - OnHit: damage.cpp Core, after matchstats::OnHit (the Hunter's + 10 % on creeps);
//   - AtMatchEnd: matchstats::AtMatchEnd, before the seat lines are built.
// Behind [host] match_stats (matchstats::Enabled). Every engine read under SEH, on the engine thread with the server
// simulator bound.
#pragma once

#include <cstdint>

#include "core/stats/gold.h"

namespace matchstats::gold {

void OnRoleGold(int seat, gms::stats::GrantContext c, bool lastHit, float amount, bool asBonus);
// `creature` the grant's creature (the player's), `modifier` the handler's modifier instance id.
void OnHandlerChange(std::uint32_t creature, std::uint32_t modifier, float before, float after);
void OnPredatorXp(int seat, float part);
// A counted hit: `attacker` the snapshot's caster object (null: none), `creditSeat` the credited seat.
void OnHit(void* attacker, void* target, int creditSeat, bool sameTeam, float hpBefore, float hpAfter);
void AtMatchEnd(void* sim, gms::stats::Accumulator& acc);

}  // namespace matchstats::gold
