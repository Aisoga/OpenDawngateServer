// TOWERAGGRO: whom a tower (a Binding or a Locus) shoots.
// The public server (2026-10-16): Bindings shot a shaper while 6-7 minions stood in range. Cause: our order
// (aggro.h AggroBefore, rules 2-4) put a call-for-help report and the sticky current target ahead of "non-players
// before players" (rule 5). Every NPC victim calls Agent_CallForHelp, so a Binding (call for help 28, filter any) heard
// a shaper hit any minion near it, took it, and stickiness kept it while the shaper stayed within 28.
// The client's tips: "Bindings prefer to attack Minions over Shapers. They will also defend their Shapers and will
// target you if you deal damage to a Shaper within their range." The user's rule (2026-10-17): a shaper that hit one
// of the tower's own-team shapers within the tower's range stays the tower's target until it leaves the tower's range.
// The rule (the stripped aggro update's own is unknown [assumed]), for aggro type tower except the Guardian (its
// gambits pick their own targets, NPCTARGET):
//   1. a taunt;
//   2. a defended-against shaper: one that damaged a shaper of the tower's team while that shaper stood within the
//      tower's range (edge distance <= the aggro range); it keeps this place until it leaves the tower's range (or
//      the list: dead, hidden, untargetable);
//   3. every non-player (minions, creeps, structures) before every other shaper;
//   4. within a class the old rules: a player's report, stickiness, a non-player's report, the nearer first;
//      since TOWERSTICK (a player's report, 2026-10-23: "turrets ... shot the first creep approaching once and switch
//      to other one without killing the first hit") among the non-players the current target first (what the tower
//      shoots, TowerStickyId), then the reports and the nearer: a minion it hit stays its target until it dies or
//      leaves the list. [host] tower_stick 0 = the old order within a class.
// So a non-player ally's call for help never lifts a shaper over minions, and stickiness holds only within a class.
// AggroBefore's strict mode is exactly 1, 2 (defend), 3, 4 (stickFirst: 4's stickiness first for the non-players);
// this header adds the defend lock and the tower's current target.
// Header-only and engine-free; gms_tests (test_agents.cpp) checks it.
#pragma once

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <vector>

namespace gms {
namespace agents {

struct TowerTargetOptions {
  bool enabled = true;
  bool stick = true;  // TOWERSTICK: within the non-players the current target first (AggroDecide's stickFirst)
  std::int64_t keepHitsMs = 2000;  // how long a shaper-on-shaper hit stays in the history (towers scan every 500 ms)
};

// One shaper's damaging hit on a shaper of the other team, with where the victim stood.
struct ShaperHit {
  std::uint32_t attacker = 0, victim = 0;
  int victimTeam = 0;
  float pos[2] = {};    // the victim's position at the hit
  float radius = 0.f;   // the victim's footprint radius
  std::int64_t ms = 0;
  std::uint64_t seq = 0;  // given by the history: 1, 2, ... in the order of the hits
};

// The recent shaper-on-shaper hits of the whole match (few: only shapers hitting shapers).
class ShaperHitHistory {
 public:
  void Record(ShaperHit h) {
    h.seq = ++seq_;
    hits_.push_back(h);
  }
  std::uint64_t LastSeq() const { return seq_; }
  void Expire(std::int64_t nowMs, std::int64_t keepMs) {
    hits_.erase(std::remove_if(hits_.begin(), hits_.end(),
                               [nowMs, keepMs](const ShaperHit& h) { return nowMs - h.ms > keepMs; }),
                hits_.end());
  }
  const std::vector<ShaperHit>& Hits() const { return hits_; }
  void Clear() { hits_.clear(); }

 private:
  std::vector<ShaperHit> hits_;
  std::uint64_t seq_ = 0;
};

// Whether a hit makes its attacker a defended-against shaper for this tower: a hit on a shaper of the tower's team
// that stood within the tower's range (edge distance).
inline bool TowerDefendTrigger(const ShaperHit& h, int towerTeam, const float* towerPos, float towerRadius,
                               float range) {
  if (h.victimTeam != towerTeam) return false;
  const float dx = h.pos[0] - towerPos[0], dy = h.pos[1] - towerPos[1];
  return std::sqrt(dx * dx + dy * dy) - towerRadius - h.radius <= range;
}

// A tower's defended-against shapers.
struct TowerDefend {
  std::vector<std::uint32_t> locked;
  std::uint64_t scannedSeq = 0;  // the last hit of the history this tower has seen
  bool scanned = false;

  bool Has(std::uint32_t id) const { return std::find(locked.begin(), locked.end(), id) != locked.end(); }
  // The hits since the last scan (at the first scan, those of the last firstScanBackMs); returns how many shapers
  // were newly locked.
  int Scan(const ShaperHitHistory& history, int towerTeam, const float* towerPos, float towerRadius, float range,
           std::int64_t nowMs, std::int64_t firstScanBackMs = 1000) {
    int added = 0;
    for (const ShaperHit& h : history.Hits()) {
      if (scanned ? h.seq <= scannedSeq : nowMs - h.ms > firstScanBackMs) continue;
      if (!TowerDefendTrigger(h, towerTeam, towerPos, towerRadius, range)) continue;
      if (!Has(h.attacker)) {
        locked.push_back(h.attacker);
        ++added;
      }
    }
    scannedSeq = history.LastSeq();
    scanned = true;
    return added;
  }
  // After the list's prune and add: a lock holds while its shaper is on the list within the tower's range.
  // `onList(id, &distance)` gives the shaper's edge distance if it is on the list. Returns the ids released.
  template <typename OnList>
  std::vector<std::uint32_t> Release(float range, OnList onList) {
    std::vector<std::uint32_t> gone;
    for (std::uint32_t id : locked) {
      float d = 0.f;
      if (!onList(id, &d) || d > range) gone.push_back(id);
    }
    for (std::uint32_t id : gone) locked.erase(std::find(locked.begin(), locked.end(), id));
    return gone;
  }
};

// TOWERSTICK: the object a tower counts as its current target (the sticky candidate). The Binding's passive
// (modifier_Binding_Passive) shoots its own currentTargetID: OnTargetEnterVolume sets it to the first enemy that enters
// its trigger sphere while it has nothing to shoot, OnBestTargetChanged and HandleBindingAttack move it to
// nAgent.GetBestTarget, and every shot ends with nGameObject.SetTargetID (the blackboard's mTarget, +0x1E5C). With
// `stick` the tower's current target is that shot target while it is on the list, else the last update's best target
// (which the old order used alone: the first minion in, shot once, wasn't sticky, so the nearer one took over).
inline std::uint32_t TowerStickyId(bool stick, std::uint32_t shot, bool shotOnList, std::uint32_t bestBefore) {
  return stick && shot && shotOnList ? shot : bestBefore;
}

// A tower's target change at an update, from what it shot (`from`, 0 = nothing yet) to the new best target.
enum TowerChange : int {
  kTowerKeep = 0,     // no change (or nothing shot yet)
  kTowerOldGone,      // the old target left the list: dead, out of range, hidden, untargetable
  kTowerOffLiving,    // the old target is still on the list: a rule put another one first
};
inline TowerChange ClassifyTowerChange(std::uint32_t from, std::uint32_t best, bool fromOnList) {
  if (!from || from == best) return kTowerKeep;
  return fromOnList ? kTowerOffLiving : kTowerOldGone;
}
// The case the players reported (TOWERSTICK's done-when counts it): a living non-player on the list left for another
// non-player.
inline bool TowerStickBroken(TowerChange change, bool fromIsPlayer, bool bestIsPlayer, std::uint32_t best) {
  return change == kTowerOffLiving && best && !fromIsPlayer && !bestIsPlayer;
}

}  // namespace agents
}  // namespace gms
