// HOSTHARD: how often one player's minimap pings, dances, taunts and voice lines are relayed. Each reaches every player of
// a team (or of the match) and plays a sound there, and the server had no limit of its own: the real client has its
// filters (minimapClientPingSpam*, minimumEmoteCooldown), a modified one had none. A token bucket per player and kind,
// generous enough that those filters always bite first [assumed: their values weren't read]; what is over the rate is
// answered like a stale command (GmsSession::Screen). Engine-free; the caller gives the clock.
#pragma once

#include <cstdint>
#include <map>
#include <utility>

#include "core/protocol/commands.h"

namespace gms {

struct CommRateOptions {
  bool on = true;
  double pingPerSec = 2, pingBurst = 10;    // minimap pings
  double emotePerSec = 1, emoteBurst = 6;   // dances, taunts and voice lines together
};

class CommRate {
 public:
  explicit CommRate(const CommRateOptions& o = CommRateOptions()) : o_(o) {}

  // Whether player `index` may send a command of `type` now (a type that isn't a comm is always let through).
  bool Allow(uint8_t index, uint8_t type, double nowMs) {
    if (!o_.on) return true;
    const bool ping = type == kCmdPing;
    if (!ping && type != kCmdDance && type != kCmdTaunt && type != kCmdVoiceLine) return true;
    const double perSec = ping ? o_.pingPerSec : o_.emotePerSec, burst = ping ? o_.pingBurst : o_.emoteBurst;
    auto it = buckets_.find({index, ping});
    if (it == buckets_.end()) it = buckets_.emplace(std::make_pair(index, ping), Bucket{burst, nowMs}).first;
    Bucket& b = it->second;
    if (nowMs > b.lastMs) {
      b.tokens += (nowMs - b.lastMs) / 1000.0 * perSec;
      if (b.tokens > burst) b.tokens = burst;
      b.lastMs = nowMs;
    }
    if (b.tokens < 1) return false;
    b.tokens -= 1;
    return true;
  }

 private:
  struct Bucket {
    double tokens, lastMs;
  };
  CommRateOptions o_;
  std::map<std::pair<uint8_t, bool>, Bucket> buckets_;  // by gameplay index and ping or not
};

}  // namespace gms
