// gms/core/bots (MAPED4): the bots' rally points for the parasite from the level's parasite camp, not SM_Map_v5's
// coordinates. Engine-free, header-only; the host (gms/engine/bots.cpp nBotHost.ParasiteRally) feeds it the parasite
// camp's marker, the team's Guardian and the level's line-of-sight map as the "open ground" test.
//
// Why: BOTPARA moved the Rally mission's and condition_Bot_GatherForParasite's points to (-66, -10) / (66, 10), 62 from
// SM_Map_v5's parasite at (-3.4, 3.2), out of its reach (27 on sight, 40 kept) with the bots' pacing of 5-15 around the
// point. On a compiled map those points lie anywhere:
// on MAPED3's CM_Grove (the river 24 m wide along x + y = 0, the pit 28 m round the centre) (66, -10) is 40 m off the
// river's axis, in a wall. The rule here keeps BOTPARA's distance and puts the point on the team's side:
//   - from the parasite camp's marker toward the team's Guardian, `dist` units out (BOTPARA's 62);
//   - when that point or the ground `clearance` round it is a wall, or a wall lies between it and the parasite, the
//     direction turns by `turnDeg` steps (+, -, ++, --, ...) up to `turns` steps, and at each direction the point comes
//     nearer in `stepDist` steps down to `minDist`; the first point that passes wins;
//   - then the same without the line to the parasite; else the plain first point (unchecked).
// SM_Map_v5 keeps BOTPARA's points ([host] bot_parasite_rally, RallyDerivedOn).
#pragma once

#include <cctype>
#include <cmath>
#include <string>

namespace gms::bots {

struct RallyConfig {
  float dist = 62.f;       // BOTPARA's distance from the parasite
  float minDist = 47.f;    // no nearer: 40 (the kept reach) + the pacing's 5-15, BOTPARA's "47+ with the pacing"
  float stepDist = 3.f;
  float turnDeg = 10.f;
  int turns = 9;           // up to +-90 degrees off the line to the Guardian
  float clearance = 5.f;   // open ground this far round the point (the pacing) [assumed]
  float lineStep = 2.f;    // the line to the parasite is tested every this many units
  float lineSkip = 10.f;   // ... except its last part at the parasite (the pit's own markers, the camp's spot)
};

enum class RallyQuality : int { kUnchecked = 0, kOpen = 1, kOpenWithLine = 2 };

inline const char* RallyQualityName(RallyQuality q) {
  switch (q) {
    case RallyQuality::kOpenWithLine:
      return "open ground with an open line to the parasite";
    case RallyQuality::kOpen:
      return "open ground (a wall between it and the parasite)";
    default:
      return "unchecked (no open ground found)";
  }
}

// The rally point of the team whose Guardian stands at (gx, gy) for the parasite camp at (px, py). `open(x, y)` is
// true on open ground (no wall). Writes out[0], out[1] and returns how good it is; out is always written.
template <class Open>
RallyQuality ParasiteRallyPoint(float px, float py, float gx, float gy, const RallyConfig& cfg, Open open, float* out) {
  float dx = gx - px, dy = gy - py;
  const float len = std::sqrt(dx * dx + dy * dy);
  if (len > 1e-3f) {
    dx /= len;
    dy /= len;
  } else {
    dx = -0.70710678f;  // no Guardian: toward the map's low corner (team 1's on SM_Map_v5)
    dy = -0.70710678f;
  }
  out[0] = px + dx * cfg.dist;
  out[1] = py + dy * cfg.dist;
  auto groundOk = [&](float x, float y) {
    if (!open(x, y)) return false;
    const float c = cfg.clearance;
    return open(x + c, y) && open(x - c, y) && open(x, y + c) && open(x, y - c);
  };
  auto lineOk = [&](float x, float y, float d) {
    for (float s = cfg.lineStep; s < d - cfg.lineSkip; s += cfg.lineStep) {
      const float t = s / d;
      if (!open(x + (px - x) * t, y + (py - y) * t)) return false;
    }
    return true;
  };
  const float kRad = 3.14159265f / 180.f;
  for (int pass = 0; pass < 2; ++pass) {
    for (int k = 0; k <= 2 * cfg.turns; ++k) {
      const int turn = (k + 1) / 2 * (k % 2 ? 1 : -1);  // 0, +1, -1, +2, -2, ...
      const float a = static_cast<float>(turn) * cfg.turnDeg * kRad;
      const float ux = dx * std::cos(a) - dy * std::sin(a), uy = dx * std::sin(a) + dy * std::cos(a);
      for (float d = cfg.dist; d >= cfg.minDist - 1e-3f; d -= cfg.stepDist) {
        const float x = px + ux * d, y = py + uy * d;
        if (!groundOk(x, y)) continue;
        if (pass == 0 && !lineOk(x, y, d)) continue;
        out[0] = x;
        out[1] = y;
        return pass == 0 ? RallyQuality::kOpenWithLine : RallyQuality::kOpen;
      }
    }
  }
  return RallyQuality::kUnchecked;
}

// [host] bot_parasite_rally: 0 BOTPARA's points everywhere; 1 (default) derived on every level but SM_Map_v5 (whose
// points BOTPARA tuned); 2 derived on every level, SM_Map_v5's too. `level` is the level asset's name, with or without
// ".Level", any case.
inline bool IsSmLevel(const std::string& level) {
  const std::string sm = "sm_map_v5";
  if (level.size() < sm.size()) return false;
  for (std::size_t i = 0; i < sm.size(); ++i)
    if (std::tolower(static_cast<unsigned char>(level[i])) != sm[i]) return false;
  return level.size() == sm.size() || level[sm.size()] == '.';
}

inline bool RallyDerivedOn(int option, const std::string& level) {
  if (option <= 0) return false;
  return option >= 2 || !IsSmLevel(level);
}

// ---- The ward spots (condition_Bot_Ward) ---------------------------------------------------------------------------
// The data's ward condition offers 11 fixed SM_Map_v5 spots per side (MortalBotWell (4, -143), SpiritTopWell (-4, 143),
// ...). On a compiled map such a spot can lie in a wall: on CM_Ring a Tactician pursued MortalBotWell from the lane
// below it, 20.7 units short, for over 90 s (the point out of reach, the gambit retried every 10 s). [host]
// bot_ward_spot_check: 1 (default) a spot on a wall of the level's line-of-sight map is refused on every level but
// SM_Map_v5 (whose spots are the data's own); 2 on every level; 0 never.
inline bool WardSpotCheckedOn(int option, const std::string& level) {
  if (option <= 0) return false;
  return option >= 2 || !IsSmLevel(level);
}

}  // namespace gms::bots
