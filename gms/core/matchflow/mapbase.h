// MAPBASE: the structure rules on a level other than SM_Map_v5 (MM_Map_v3: two Binding tiers a team, three Cores a
// team, a base defence of NPC type Tower). Engine-free; header-only; gms_tests MapBase*.
//
// What the data says and the host does:
//   - Bindings: the tier comes from the noun name (flow::BindingTier: Outer 0, Middle 1, Inner 2), so MM_Map_v3's
//     Middle and Inner fill tiers 1 and 2 of the tower table and tier 0 stays empty (the client's TowerGraphics_Update
//     skips a slot without an object). Every Binding can be attacked at any time, on every level (no tier lock: the
//     game has none that the data or the code shows).
//   - Binding respawns: LevelGameplayTuning.tower[] (respawnTime, laneAgentRespawnCounterAdjustment per entry) is used
//     only when towerRespawnEnabled (+0xD8) is set; SM_Map_v5 and MM_Map_v3 both have it false (MM_Map_v3's list is
//     900 s and 600 s), so a destroyed Binding stays down [data]. A compiled map may set it (`bindings.respawn`);
//     then the host writes the due time into the slot and resurrects the Binding at it (MAPRULES3).
//   - Cores (the nubs): their regrowth is ability_BaseNub_Death's own respawnTime 480 on every level; the level's
//     nubRespawnTime (MM_Map_v3 15, SM_Map_v5 480) only times the energy line's glow (SetEnergyLineState's value)
//     [script], and MM_Map_v3 has no EnergyLineTuning, so its client draws no line [data].
//   - The Locus (DEST_prefab_<blue|red>_tower[_v5]): ability_Tower_MegaZap, 50,000 HP; NPC type Locus (15) on
//     SM_Map_v5, Tower (9) on MM_Map_v3 (aggro 45 instead of 38.5). Not a Core: neither the Guardian's shield nor the
//     match's end depends on it.
#pragma once

#include <cstddef>
#include <string>
#include <vector>

namespace gms {
namespace flow {

// One entry of LevelGameplayTuning.tower[] (the asset type `tower`, 0x10 bytes: +0 respawnTime, +4 augmentation[],
// +0xC laneAgentRespawnCounterAdjustment).
struct TowerTuning {
  float respawnTime = 0.f;
  int laneAgentAdjust = 0;  // laneAgentRespawnCounterAdjustment (the lane agents' counter; MAPSTRIDER)
};

// The tiers a set of Bindings uses, as a mask (bit = flow::BindingTier), and its names in order ("Middle, Inner").
inline unsigned TierMask(const std::vector<int>& tiers) {
  unsigned m = 0;
  for (int t : tiers)
    if (t >= 0 && t < 3) m |= 1u << t;
  return m;
}
inline std::string TierNames(unsigned tierMask) {
  static const char* const kNames[3] = {"Outer", "Middle", "Inner"};
  std::string s;
  for (int t = 0; t < 3; ++t)
    if ((tierMask >> t) & 1u) s += (s.empty() ? "" : ", ") + std::string(kNames[t]);
  return s.empty() ? "none" : s;
}

// The entry of LevelGameplayTuning.tower[] a Binding of `tier` would use: its rank among the tiers the level has,
// outermost first (MM_Map_v3: Middle 0, Inner 1; SM_Map_v5: the tier itself) [assumed: the list has one entry per
// tier the level uses, MM_Map_v3's two for its two tiers]; -1 for a tier the mask doesn't have.
inline int TowerTuningIndex(unsigned tierMask, int tier) {
  if (tier < 0 || tier >= 3 || !((tierMask >> tier) & 1u)) return -1;
  int rank = 0;
  for (int t = 0; t < tier; ++t) rank += (tierMask >> t) & 1u;
  return rank;
}

// When a destroyed Binding comes back, in seconds after its death: -1 = never (towerRespawnEnabled false, which both
// shipped levels have, or no entry for it). A compiled map's `bindings.respawn` sets it (MAPRULES2); the host
// resurrects the Binding then (MAPRULES3, BindingRespawns below).
inline double BindingRespawnSeconds(bool towerRespawnEnabled, const std::vector<TowerTuning>& list, int index) {
  if (!towerRespawnEnabled || index < 0 || index >= static_cast<int>(list.size())) return -1.0;
  return list[static_cast<std::size_t>(index)].respawnTime;
}

// MAPRULES3: the shortest wait the host uses, in seconds. A dead Binding's passive (modifier_Binding_Passive,
// deactivation OnAgentDeath) ends 3 scheduler runs after the death; a resurrect before that would race it.
constexpr double kMinBindingRespawnSeconds = 5.0;

// The wait for a Binding after the test aid [host] tower_respawn_s: < 0 keeps the level's (`levelSeconds`, -1 =
// never), >= 0 replaces it, also on a level without towerRespawnEnabled (the server side only: such a client
// draws any non-zero slot as destroyed). Waits are at least kMinBindingRespawnSeconds.
inline double EffectiveBindingRespawn(double levelSeconds, int overrideSeconds) {
  const double s = overrideSeconds >= 0 ? static_cast<double>(overrideSeconds) : levelSeconds;
  if (s < 0.0) return -1.0;
  return s < kMinBindingRespawnSeconds ? kMinBindingRespawnSeconds : s;
}

// The UI data's mTowerRespawnTime slot (cBattlegroundsUIData +0x10, u64 [2][2][4]; TowerGraphics_GetTowerState
// 0x00537DF0): 0 standing, all ones destroyed for good, otherwise the server clock (ms) of the return.
constexpr unsigned long long kTowerSlotGone = ~0ull;
inline unsigned long long BindingDueMs(double seconds, unsigned long long nowMs) {
  if (seconds < 0.0) return kTowerSlotGone;
  unsigned long long due = nowMs + static_cast<unsigned long long>(seconds * 1000.0 + 0.5);
  if (due == 0) due = 1;                                // 0 would read as standing
  if (due == kTowerSlotGone) due = kTowerSlotGone - 1;  // all ones would read as gone for good
  return due;
}

// The slot of mTowers / mTowerRespawnTime: (teamIndex * 2 + lane) * 4 + tier, -1 when out of range.
inline int TowerSlot(int teamIndex, int lane, int tier) {
  if (teamIndex < 0 || teamIndex > 1 || lane < 0 || lane > 1 || tier < 0 || tier > 3) return -1;
  return (teamIndex * 2 + lane) * 4 + tier;
}

// The client's state for a slot (TowerGraphics_GetTowerState): 0 standing, 1 destroyed, 2 rebuilding (less than the
// rebuild animation's length left: gfx state 0x2EA8FB98, tower_rebirth_effect and TowerRespawnWarning), 3 about back
// (less than towerRespawnedFeedbackEventLeadTime, 2 s on both shipped levels: TowerRespawned). `nowMs` is the
// client's clock; `rebuildMs` the length the client read at EnterGame (0 when it read none: then no state 2).
inline int ClientTowerState(unsigned long long slot, bool towerRespawnEnabled, unsigned long long nowMs, double leadS,
                            double rebuildS) {
  if (slot == kTowerSlotGone || (!towerRespawnEnabled && slot != 0)) return 1;
  if (slot == 0) return 0;
  const long long left = static_cast<long long>(slot - nowMs);
  if (left < static_cast<long long>(leadS * 1000.0)) return 3;
  if (left < static_cast<long long>(rebuildS * 1000.0)) return 2;
  return 1;
}

// The host's record of the Bindings that are down and due back (one per tower slot).
class BindingRespawns {
 public:
  static constexpr int kSlots = 16;
  // A Binding of `slot` died at server clock `nowMs`: its wait (`seconds`, -1 = never) becomes the slot's value.
  // Counts the slot's deaths; a death while already pending keeps the first due time.
  unsigned long long OnDestroyed(int slot, double seconds, unsigned long long nowMs) {
    if (slot < 0 || slot >= kSlots) return kTowerSlotGone;
    Entry& e = e_[slot];
    ++e.deaths;
    if (e.due != 0) return e.due;
    e.due = BindingDueMs(seconds, nowMs);
    return e.due;
  }
  // The slots whose Binding is due at `nowMs` (each once); they are standing again for the record.
  std::vector<int> TakeDue(unsigned long long nowMs) {
    std::vector<int> out;
    for (int s = 0; s < kSlots; ++s) {
      Entry& e = e_[s];
      if (e.due == 0 || e.due == kTowerSlotGone || e.due > nowMs) continue;
      e.due = 0;
      ++e.returns;
      out.push_back(s);
    }
    return out;
  }
  unsigned long long Due(int slot) const { return slot >= 0 && slot < kSlots ? e_[slot].due : 0; }
  int Deaths(int slot) const { return slot >= 0 && slot < kSlots ? e_[slot].deaths : 0; }
  int Returns(int slot) const { return slot >= 0 && slot < kSlots ? e_[slot].returns : 0; }
  int Pending() const {
    int n = 0;
    for (const Entry& e : e_) n += e.due != 0 && e.due != kTowerSlotGone;
    return n;
  }

 private:
  struct Entry {
    unsigned long long due = 0;  // 0: standing (or never died)
    int deaths = 0, returns = 0;
  };
  Entry e_[kSlots];
};

// The base's own defence, the Locus: DEST_prefab_blue_tower(_v5) and DEST_prefab_red_tower(_v5) ("…Noun" ignored).
inline bool IsLocusNoun(const std::string& noun) {
  const std::string n = noun.size() > 5 && noun.compare(noun.size() - 5, 5, ".Noun") == 0
                            ? noun.substr(0, noun.size() - 5)
                            : noun;
  return n == "DEST_prefab_blue_tower" || n == "DEST_prefab_red_tower" || n == "DEST_prefab_blue_tower_v5" ||
         n == "DEST_prefab_red_tower_v5";
}

}  // namespace flow
}  // namespace gms
