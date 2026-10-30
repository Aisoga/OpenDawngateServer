// MAPED4: a jungle camp's role from the level's data, not from its noun's name. A map made in the editor names any
// camp noun it likes (or a new one), so the host can't tell a buff camp by "Buff" in the name (director.cpp did until
// MAPED4). What the data says instead [data, SM_Map_v5's markers and nouns]:
//   - a buff camp's major stands on a creature marker larger than the camp's others: each of SM_Map_v5's six buff
//     camps has one child marker with cLabsMarker.scale 2.0 and the others 1.0, every other camp's children are 1.0
//     (the tutorial's buff pings are exactly those markers);
//   - the Gold Pigs (SpawnPoint_v5_CreepsBoar) have no such marker but come back only after 300 s, like the buffs;
//     the small camps after 180 s (their nouns', the director's default since CAMPTIMER; 60-180 s with [host]
//     camp_timers=marker), both ways under kCampBigRespawnS.
// So: buff = a creature marker above the camp's smallest; big = no such marker but a respawn of at least
// kCampBigRespawnS; small = the rest. On SM_Map_v5 that is the name rule's split again (6 buff camps, 2 Boar camps,
// 14 small ones; the Boar's "big" counts as a buff for the icons' 60-s reveal window, the user's "Boar as buff").
// The parasite's camp (SpawnPointDef type 3) has no role here.
#pragma once

namespace gms {
namespace flow {

enum class CampRole : int { kSmall = 0, kBuff = 1, kBig = 2 };

constexpr double kCampBigRespawnS = 240.0;  // between the small camps' 180 s and the buffs' and pigs' 300 s [assumed]
constexpr float kCampBuffScaleStep = 0.25f;  // a marker counts as larger with this much more scale [assumed]

// The role of a camp whose creature markers have `scales` (`n` of them) and whose respawn time is `respawnS`.
inline CampRole CampRoleOf(const float* scales, int n, double respawnS) {
  if (scales && n > 1) {
    float lo = scales[0], hi = scales[0];
    for (int i = 1; i < n; ++i) {
      if (scales[i] < lo) lo = scales[i];
      if (scales[i] > hi) hi = scales[i];
    }
    if (hi - lo >= kCampBuffScaleStep) return CampRole::kBuff;
  }
  return respawnS >= kCampBigRespawnS ? CampRole::kBig : CampRole::kSmall;
}

inline const char* CampRoleName(CampRole r) {
  switch (r) {
    case CampRole::kBuff:
      return "buff";
    case CampRole::kBig:
      return "big";
    default:
      return "small";
  }
}

}  // namespace flow
}  // namespace gms
