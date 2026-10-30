// M18 (MSTATS1): the match statistics on the match host. The rules
// and the lines are gms/core/stats (engine-free); this module feeds them from the engine and sends them:
//   - OnHit: the damage core (damage.cpp Core, after its HP step), every hit on our server simulator;
//   - OnDeath: death::OnDeath (kills by target kind, a player kill's event, a structure's event);
//   - OnDeathTimer: death.cpp StartDeathTimer (time dead);
//   - OnWellCaptured: wells.cpp Apply (wellsCaptured, the well event);
//   - Sample: match.cpp's once-a-second step (the timeline: each seat's mTotalGold, mExperience, mLevel and creep
//     score, each team's gold and kills, once per match minute);
//   - AtMatchEnd: match::EndMatch, before the playerFinish lines: one seatStats per seat (bots included), then one
//     matchTimeline, to dgserver (hostctl) or, without a control channel, to the log only.
// MSTATS3 (matchstats_sources.h): each hit's dealt part and each heal by source (seatStats.sources), the heal-core
// detour (healing keys) and the shields given (shieldingDone).
// [host] match_stats (1): 0 turns all of it off (no counting, no lines; dgserver stores the match with the basics).
// Every engine read is under SEH; everything runs on the engine thread with the server simulator bound.
#pragma once

#include <cstdint>
#include <vector>

namespace gms::stats {
class Accumulator;
}

namespace matchstats {

bool Enabled();
// The accumulator while the match counts (Enabled and its lines not sent yet), else nullptr (MSTATS3's heal detour and
// shields, matchstats_sources.cpp).
gms::stats::Accumulator* Counting();
// The damage core's hit: `target` the object hit, `creditSeat` the snapshot's credited seat (0xFF none), `sameTeam`
// the snapshot's team equals the target's; hasSnapshot false = a health cost. MSTATS3: creditGuid the snapshot's
// credit GUID (+0x10), the source row of the dealt part (matchstats_sources.h).
void OnHit(void* target, bool hasSnapshot, int creditSeat, bool sameTeam, unsigned descLo, float hpBefore,
           float hpAfter, float dealt, float absorbed, bool lethal, unsigned creditGuid);
// A death: the victim's kind (player-controlled, nNPCType) and team; creditSeat the credited seat (0xFF none);
// victimSeat the player whose creature died (-1 none) and killerSeat its credited killer (-1 none).
void OnDeath(bool playerControlled, int npcType, int victimTeam, int creditSeat, int victimSeat, int killerSeat);
void OnDeathTimer(int seat, unsigned long long deathMs, unsigned long long respawnMs);
void OnWellCaptured(double matchSeconds, int well, int team, const std::vector<int>& capturers);
void Sample(void* sim, double matchSeconds);
// surrenderTeam: 1 or 2 when that team surrendered, else 0.
void AtMatchEnd(void* sim, int winnerTeam, unsigned long long durationMs, int surrenderTeam);

}  // namespace matchstats
