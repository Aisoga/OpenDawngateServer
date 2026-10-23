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
//     900 s and 600 s), so a destroyed Binding stays down [data].
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
// shipped levels have, or no entry for it). The host resurrects no Binding either way (no level enables it).
inline double BindingRespawnSeconds(bool towerRespawnEnabled, const std::vector<TowerTuning>& list, int index) {
  if (!towerRespawnEnabled || index < 0 || index >= static_cast<int>(list.size())) return -1.0;
  return list[static_cast<std::size_t>(index)].respawnTime;
}

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
