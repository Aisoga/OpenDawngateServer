// M18 (MSTATS4): crowd control and wards for the match statistics. The rules are gms/core/stats/cc.h (engine-free);
// this module feeds them and adds the keys to the seat lines:
//   - OnModifierCreated / OnModifierUpdated: gms_host.cpp DrainStep, every ModifierCreated (AB) and ModifierUpdated
//     (AC) the server simulator queued, as the host's modifier registry takes them;
//   - OnModifierEnded: DrainStep's end pass (the instance is gone on the server); OnCarrierDeleted: an object's delete;
//   - OnTickThread: abilities.cpp, a prepared tick thread (wardsPlaced: the two ward abilities' new threads);
//   - AtMatchEnd: matchstats.cpp, before the seat lines are built: ccCaused/Endured keys, wardsPlaced (WARDSTAT
//     dropped wardsDestroyed: a ward has no combatant in 854, so nothing kills it).
// Off with [host] match_stats = 0 (matchstats::Enabled). Engine reads under SEH, on the engine thread with the server
// simulator bound.
#pragma once

#include <cstddef>
#include <cstdint>

namespace gms::stats {
class Accumulator;
}

namespace matchstats::cc {

void OnModifierCreated(const std::uint8_t* body, std::size_t n);
void OnModifierUpdated(const std::uint8_t* body, std::size_t n);
void OnModifierEnded(std::uint32_t instance);
void OnCarrierDeleted(std::uint32_t carrier);
void OnTickThread(void* instance, bool recreated);
// Closes the tally at nowMs (the server clock) and adds every seat's keys to `acc`.
void AtMatchEnd(unsigned long long nowMs, gms::stats::Accumulator& acc);

}  // namespace matchstats::cc
