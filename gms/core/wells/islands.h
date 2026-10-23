// gms/core/wells/islands.h (MAPMINES): which gold mines no player can reach, from the level's island markers.
//
// MM_Map_v3's two team gold mines (gold_blue_side_MM at (-260.3, -260.3), gold_red_side_MM at (260.5, 260.4)) stand on
// the map's corner islands, behind each base. The level's own island markers say who may stand there: a marker with
// navmeshMarkerData (cLabsMarker +0xA8, cNavmeshMarkerData {bool allowPlayersHere, bool allowNPCsHere}) keeps the
// island it stands on in that layer's navigation graph (NavPower's island removal). On MM
// the markers on the two corner islands, at (-260.8, -260.1) and (260.6, 261.1), allow NPCs only, so the island is in
// the NPC graph (the workers walk there) but not in the Player graph: the Player graph's nearest area lies 53-55 units
// from each mine's centre (MM's grid graph; the capture ring is 12 units), so no shaper can stand in a
// mine's ring or reach its workers [data, measured on the graph]. SM_Map_v5's NPC-only markers sit on the Guardians'
// islands at (+-182, +-182), far from its four spirit wells, so every SM well stays in reach.
//
// The rule (engine-free; the host reads the markers, gms/engine/wells.cpp): a well is out of the players' reach when
// an island marker that allows NPCs but not players lies within `radius` of its centre (2-D; the host passes the
// capture radius) and no marker that allows players does [inferred: a marker that near stands on the well's own
// island]. The host keeps such a well out of the bots' GetMinesForTeam (no HarassWell or AttackWell mission, no team
// call to a mine nobody can reach); the capture rules stay the data's.
#pragma once

#include <cmath>
#include <vector>

namespace gms::wells {

struct IslandMarker {
  float x = 0.f, y = 0.f, z = 0.f;
  bool players = false;  // allowPlayersHere
  bool npcs = false;     // allowNPCsHere
};

// What the markers say about a point: the nearest marker within `radius` of each kind (-1 = none).
struct IslandView {
  float npcOnly = -1.f;     // the nearest marker that allows NPCs and not players
  float players = -1.f;     // the nearest marker that allows players
};

inline IslandView ViewAt(float x, float y, const std::vector<IslandMarker>& markers, float radius) {
  IslandView v;
  for (const IslandMarker& m : markers) {
    const float d = std::hypot(m.x - x, m.y - y);
    if (!(d <= radius)) continue;
    if (m.players) {
      if (v.players < 0.f || d < v.players) v.players = d;
    } else if (m.npcs) {
      if (v.npcOnly < 0.f || d < v.npcOnly) v.npcOnly = d;
    }
  }
  return v;
}

// True when the well at (x, y) stands on an island only NPCs may use (see above).
inline bool OutOfPlayersReach(float x, float y, const std::vector<IslandMarker>& markers, float radius) {
  const IslandView v = ViewAt(x, y, markers, radius);
  return v.npcOnly >= 0.f && v.players < 0.f;
}

}  // namespace gms::wells
