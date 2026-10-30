// M18 (MSTATS3): damage and healing by source on the match host.
// The rules are gms/core/stats (sources.h: the table and its fallbacks; stats.h: OnHeal, OnShieldAbsorb); this module
// reads the players and the heal core:
//   - SourceOf: a hit's or heal's row from the credited seat's table (built lazily: on a GUID the table lacks, at most
//     every 250 ms per seat, and once a second from matchstats::Sample): the shaper's slots, the inventory's items and
//     spells, the perk page's modifiers;
//   - the heal-core detour on GameObject_HealDamage (0x008E0720): every heal on our server simulator, the host's own
//     lifedrain included, counted with the HP it actually gained (a nested overload heal's part taken out);
//   - OnShieldAbsorb: from the damage core's absorb step, the absorbing modifier's caster (instance +0xD8) gets it;
//   - LogSources: at the match's end, per seat the rows' sums against damageDealt and healingDone, and the GUIDs that
//     fell back (with their static ability's name) for the log.
// Everything under SEH, on the engine thread; [host] match_stats=0 turns it off with the rest (no detour installed).
#pragma once

#include <cstdint>

#include "core/stats/sources.h"
#include "core/stats/stats.h"

namespace matchstats {

void SetSim(void* sim);
// Installs the heal-core detour (with match_stats on); false if it isn't (off, or the prologue differs).
bool InstallHealHook();
// The row of a hit or heal credited to `seat` (0-9) with the snapshot's credit GUID and the hit's descriptors.
gms::stats::Source SourceOf(int seat, std::uint32_t creditGuid, unsigned descLo);
// Rebuilds every present seat's table (server simulator current; once a second).
void RefreshSources(void* sim);
// The damage core's absorb step: `shieldInstance` the absorbing modifier instance, `target` the object hit.
void OnShieldAbsorb(void* shieldInstance, void* target, float absorbed);
// MSTATS3-live2: the pet rule's check, from the damage core for every applied hit: the attacker (the snapshot's
// caster), the credited seat (-1 none), whether attacker and target share a team, the HP the hit took. Counts enemy
// hits by core/stats' AttackerCredit (a non-shaper's hit credited to its owner, another seat, or lost) for LogSources.
void OnHitAttacker(void* attacker, int creditSeat, bool sameTeam, float effective);
void LogSources(const gms::stats::Accumulator& acc, const gms::stats::SeatFacts seats[gms::stats::kSeats]);

}  // namespace matchstats
