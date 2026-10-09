// The per-player fog-of-war transitions and their messages (D2g-fog-core): see fog.h.
#include "core/vision/fog.h"

#include <cmath>

#include "core/protocol/wire.h"

namespace gms::vision {

namespace {

bool SeesTeam(uint8_t teamBits, uint8_t playerTeam) {
  const uint8_t bit = TeamBit(playerTeam);
  return bit == 0 || (teamBits & bit) != 0;  // no team: sees everything [assumed]
}

uint16_t PlayerBit(int index) {
  return index >= 0 && index < 16 ? static_cast<uint16_t>(1u << index) : 0;
}

}  // namespace

uint16_t PlayersForTeams(uint8_t teamBits, const std::vector<FogPlayer>& players) {
  uint16_t m = 0;
  for (const FogPlayer& p : players)
    if (SeesTeam(teamBits, p.team)) m = static_cast<uint16_t>(m | PlayerBit(p.index));
  return m;
}

std::vector<FogAction> FogPlanner::Plan(const std::vector<FogObject>& objects, const std::vector<FogPlayer>& players,
                                        std::vector<uint16_t>* visibleAfter) {
  std::vector<FogAction> actions;
  if (visibleAfter) visibleAfter->assign(objects.size(), 0);
  std::unordered_map<uint32_t, LastPos> keep;
  for (size_t i = 0; i < objects.size(); ++i) {
    const FogObject& o = objects[i];
    uint16_t after = o.visible;
    uint16_t fogged = 0;  // fogged before this tick and still (a player entering now got this tick's 9C)
    for (const FogPlayer& p : players) {
      const uint16_t bit = PlayerBit(p.index);
      if (!bit || !(o.known & bit)) continue;
      const bool shown = (o.visible & bit) != 0;
      const bool seen = SeesTeam(o.teamBits, p.team);
      if (shown && !seen) {
        actions.push_back({FogActionKind::kEnter, p.index, o.id, i});
        after = static_cast<uint16_t>(after & ~bit);
      } else if (!shown && seen) {
        actions.push_back({FogActionKind::kExit, p.index, o.id, i});
        after = static_cast<uint16_t>(after | bit);
      } else if (!shown && !seen) {
        fogged = static_cast<uint16_t>(fogged | bit);
      }
    }
    if (visibleAfter) (*visibleAfter)[i] = after;
    if (!o.maintainPos || !o.id) continue;
    // A3 for the players that had it fogged before this tick, once it moved far enough from the last A3 (or from
    // where it was when nobody had it fogged yet)
    auto it = lastA3_.find(o.id);
    LastPos at = it != lastA3_.end() ? it->second : LastPos{o.x, o.y};
    if (!fogged) {
      at = {o.x, o.y};
    } else {
      const float dx = o.x - at.x, dy = o.y - at.y;
      const bool first = it == lastA3_.end();
      if (first || dx * dx + dy * dy >= kDisabledUpdateMinDistance * kDisabledUpdateMinDistance) {
        for (const FogPlayer& p : players)
          if (fogged & PlayerBit(p.index)) actions.push_back({FogActionKind::kDisabledPos, p.index, o.id, i});
        at = {o.x, o.y};
      }
    }
    keep[o.id] = at;
  }
  lastA3_.swap(keep);
  return actions;
}

Bytes BuildObjectEnterFoW(uint32_t id) {
  Bytes b;
  PutU8(b, kGmsObjectEnterFoW);
  PutU32LE(b, id);
  return b;
}

Bytes BuildObjectPrepareForExitFoW(uint32_t id, const float pos[3], const float eulerRadians[3]) {
  Bytes b;
  PutU8(b, kGmsObjectPrepareForExitFoW);
  PutU32LE(b, id);
  for (int k = 0; k < 3; ++k) PutF32LE(b, pos[k]);
  for (int k = 0; k < 3; ++k) PutF32LE(b, eulerRadians[k]);
  return b;
}

Bytes BuildObjectExitFoW(uint32_t id, const Bytes& modifierRecords) {
  Bytes b;
  PutU8(b, kGmsObjectExitFoW);
  PutU32LE(b, id);
  b.insert(b.end(), modifierRecords.begin(), modifierRecords.end());
  return b;
}

Bytes BuildDisabledObjectUpdate(uint32_t id, float x, float y) {
  uint16_t block = 0, xy = 0;
  QuantizeXY(x, y, &block, &xy);
  Bytes b;
  PutU8(b, kGmsDisabledObjectUpdate);
  PutU32LE(b, id);
  PutU16LE(b, block);
  PutU16LE(b, xy);
  return b;
}

void QuantizeXY(float x, float y, uint16_t* block, uint16_t* xy) {
  auto q = [](float c) -> uint32_t {
    if (c < -500.f) c = -500.f;
    if (c > 500.f) c = 500.f;
    const float v = (c + 500.f) * 65.535f;
    const uint32_t u = static_cast<uint32_t>(v);
    return u > 0xFFFFu ? 0xFFFFu : u;
  };
  const uint32_t qx = q(x), qy = q(y);
  *block = static_cast<uint16_t>((qx >> 8) | ((qy >> 8) << 8));
  *xy = static_cast<uint16_t>((qx & 0xFFu) | ((qy & 0xFFu) << 8));
}

void DequantizeXY(uint16_t block, uint16_t xy, float* x, float* y) {
  const float k = 0.015259022f;  // 1 / 65.535, the reader's constant
  *x = static_cast<float>(((block & 0xFFu) << 8) | (xy & 0xFFu)) * k - 500.f;
  *y = static_cast<float>((block & 0xFF00u) | (xy >> 8)) * k - 500.f;
}

void QuaternionToEuler(const float q[4], float euler[3]) {
  const double x = q[0], y = q[1], z = q[2], w = q[3];
  double s = 2.0 * (w * y - z * x);
  if (s > 1.0) s = 1.0;
  if (s < -1.0) s = -1.0;
  euler[0] = static_cast<float>(std::atan2(2.0 * (w * x + y * z), 1.0 - 2.0 * (x * x + y * y)));
  euler[1] = static_cast<float>(std::asin(s));
  euler[2] = static_cast<float>(std::atan2(2.0 * (w * z + x * y), 1.0 - 2.0 * (y * y + z * z)));
}

void EulerToQuaternion(const float euler[3], float q[4]) {
  // Math_EulerToQuaternion (0x00673A90), term by term
  const float sx = std::sin(euler[0] * 0.5f), cx = std::cos(euler[0] * 0.5f);
  const float sy = std::sin(euler[1] * 0.5f), cy = std::cos(euler[1] * 0.5f);
  const float sz = std::sin(euler[2] * 0.5f), cz = std::cos(euler[2] * 0.5f);
  const float szcx = sz * cx, szsx = sz * sx, czsx = cz * sx, czcx = cz * cx;
  q[0] = czsx * cy - szcx * sy;
  q[1] = szsx * cy + czcx * sy;
  q[2] = szcx * cy - czsx * sy;
  q[3] = szsx * sy + czcx * cy;
}

}  // namespace gms::vision
