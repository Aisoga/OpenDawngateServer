// gms/core/bots (HUNTERBOT): the Hunter's farm route without the engine. Header-only.
//
// The Hunter's Farm plan walks to nDirector.GetClosestCampPosition (the host's: the nearest camp with a living
// creature). Nearest alone sent him into the enemy's jungle as often as into his own: in 25-30 minute bot matches the
// bot Hunter died 2-6 times against his team's 0.7-2.8 and fell 1-4 levels behind it by 20-25 minutes. A camp on the
// enemy's half of the map costs `enemySidePenalty` units more, so he clears his own camps first and crosses only for
// one much nearer [assumed: 90 units, the river 15].
#pragma once

namespace gms::bots {

// The route cost of a camp at (campX, campY) `dist` units away for a bot of `team` (1: its base at the map's low
// corner, its half x + y < 0; 2 the other): the distance, plus the penalty on the enemy's half; a band of `river`
// units each side of the diagonal is nobody's. Other teams, or a penalty of 0: the distance.
inline float CampRouteCost(float dist, float campX, float campY, int team, float enemySidePenalty, float river = 15.f) {
  if ((team != 1 && team != 2) || !(enemySidePenalty > 0.f)) return dist;
  const float s = (campX + campY) * 0.70710678f;  // the signed distance from the diagonal
  const bool enemyHalf = team == 1 ? s > river : s < -river;
  return enemyHalf ? dist + enemySidePenalty : dist;
}

}  // namespace gms::bots
