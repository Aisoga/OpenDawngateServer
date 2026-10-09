// The fog-of-war vision model (D2g-fog-core): see vision.h.
#include "core/vision/vision.h"

#include <cmath>
#include <cstdlib>

namespace gms::vision {

LosMap LosMap::ForLevel(const uint8_t* cells, int width, int height, float levelSizeX, float levelSizeY) {
  LosMap m;
  m.cells = cells;
  m.width = width;
  m.height = height;
  m.originX = static_cast<float>(width) * 0.5f;
  m.originY = static_cast<float>(height) * 0.5f;
  m.scaleX = levelSizeX > 0.f ? static_cast<float>(width) / levelSizeX : 0.f;
  m.scaleY = levelSizeY > 0.f ? static_cast<float>(height) / levelSizeY : 0.f;
  return m;
}

void LosMap::CellOf(float x, float y, int* cx, int* cy) const {
  // ROUND (fistp, round to nearest) of origin + coordinate x scale, clamped (0x009185E0)
  int i = static_cast<int>(std::lrintf(originX + x * scaleX));
  int j = static_cast<int>(std::lrintf(originY + y * scaleY));
  if (i < 0) i = 0;
  if (i >= width) i = width - 1;
  if (j < 0) j = 0;
  if (j >= height) j = height - 1;
  *cx = i;
  *cy = j;
}

uint8_t LosMap::At(int cx, int cy) const {
  if (!Valid() || cx < 0 || cy < 0 || cx >= width || cy >= height) return 0;
  return cells[static_cast<size_t>(cy) * static_cast<size_t>(width) + static_cast<size_t>(cx)];
}

bool LosMap::Blocked(float x0, float y0, float x1, float y1) const {
  if (!Valid()) return false;
  int ax, ay, bx, by;
  CellOf(x0, y0, &ax, &ay);
  CellOf(x1, y1, &bx, &by);
  const int dx = std::abs(bx - ax), dy = -std::abs(by - ay);
  const int sx = ax < bx ? 1 : -1, sy = ay < by ? 1 : -1;
  int err = dx + dy;
  int x = ax, y = ay;
  while (x != bx || y != by) {
    const int e2 = 2 * err;
    if (e2 >= dy) {
      err += dy;
      x += sx;
    }
    if (e2 <= dx) {
      err += dx;
      y += sy;
    }
    if (x == bx && y == by) break;  // the end cell is left out
    if (At(x, y) != 0) return true;
  }
  return false;
}

uint32_t GrassMap::Raw(float x, float y, uint32_t* packed) const {
  if (!Valid()) return 0;
  int cx, cy;
  geometry.CellOf(x, y, &cx, &cy);
  if (packed) *packed = static_cast<uint32_t>(cy) << 16 | static_cast<uint32_t>(cx);
  return cells[static_cast<size_t>(cy) * static_cast<size_t>(geometry.width) + static_cast<size_t>(cx)];
}

uint32_t GrassMap::At(float x, float y, uint32_t* packed) const {
  const uint32_t v = Raw(x, y, packed);
  return (v & 0xFF000000u) ? v : 0;
}

bool GrassMap::BlockedBetween(float x0, float y0, float x1, float y1, uint32_t sourcePatch, uint32_t targetPatch) const {
  if (!Valid()) return false;
  int ax, ay, bx, by;
  geometry.CellOf(x0, y0, &ax, &ay);
  geometry.CellOf(x1, y1, &bx, &by);
  const uint32_t sp = sourcePatch & 0x00FFFFFFu, tp = targetPatch & 0x00FFFFFFu;
  const int dx = std::abs(bx - ax), dy = -std::abs(by - ay);
  const int sx = ax < bx ? 1 : -1, sy = ay < by ? 1 : -1;
  int err = dx + dy;
  int x = ax, y = ay;
  while (x != bx || y != by) {
    const int e2 = 2 * err;
    if (e2 >= dy) {
      err += dy;
      x += sx;
    }
    if (e2 <= dx) {
      err += dx;
      y += sy;
    }
    const uint32_t v = cells[static_cast<size_t>(y) * static_cast<size_t>(geometry.width) + static_cast<size_t>(x)];
    if (!(v & 0xFF000000u)) continue;
    const uint32_t patch = v & 0x00FFFFFFu;
    if ((sp && patch == sp) || (tp && patch == tp)) continue;
    return true;
  }
  return false;
}

bool UpdateGrassId(const GrassMap& map, float x, float y, uint32_t* id, uint32_t* packed) {
  if (!map.Valid()) return false;
  uint32_t cell = 0;
  const uint32_t v = map.Raw(x, y, &cell);
  const bool grass = (v & 0xFF000000u) != 0;
  if (*id == 0) {
    if (!grass) return false;
    *id = v;
  } else {
    if (((*id ^ v) & 0x00FFFFFFu) == 0) return false;  // the same patch (also: no change while out of the grass)
    *id = grass ? v : 0;
  }
  if (packed) *packed = cell;
  return true;
}

uint8_t VisionModel::BitsOf(uint32_t id) const {
  auto it = index_.find(id);
  return it != index_.end() && it->second < bits_.size() ? bits_[it->second] : 0;
}

bool VisionModel::SeenBy(int team, const Entity& t, bool wasVisible) const {
  for (const Source& s : sources_) {
    if (s.team != team || (s.id != 0 && s.id == t.id)) continue;
    const float dx = s.x - t.x, dy = s.y - t.y;
    const float reach = s.radius + (t.areaRadius > 0.f ? t.areaRadius : 0.f) + (wasVisible ? s.hysteresis : 0.f);
    if (dx * dx + dy * dy > reach * reach) continue;
    if (params_.terrain && !s.ignoreTerrain && params_.terrain->Blocked(s.x, s.y, t.x, t.y)) continue;
    if (params_.grass && !s.ignoreGrass) {
      if (t.grassId != 0 && (t.grassConceal & TeamBit(team)) && s.grassId != t.grassId) continue;
      if (params_.grassMap && params_.grassMap->BlockedBetween(s.x, s.y, t.x, t.y, s.grassId, t.grassId)) continue;
    }
    if (params_.stealth && t.stealthed && !s.detectsStealth) continue;
    return true;
  }
  return false;
}

// state: 0 not yet, 1 in progress (an owner cycle), 2 done
uint8_t VisionModel::Resolve(size_t i, std::vector<uint8_t>* state) {
  if ((*state)[i] == 2) return bits_[i];
  const Entity& e = (*entities_)[i];
  const uint8_t own = TeamBit(e.team);
  uint8_t bits = own;
  (*state)[i] = 1;
  if (!e.tracked || e.neverFogged || !e.controlsVisibility) {
    bits = kAllTeams;
  } else {
    bool done = false;
    if (e.useOwnerVision && e.owner != 0 && e.owner != e.id) {
      auto it = index_.find(e.owner);
      if (it != index_.end() && (*state)[it->second] != 1) {
        bits = static_cast<uint8_t>(Resolve(it->second, state) | own);
        done = true;
      }
    }
    auto last = e.id ? last_.find(e.id) : last_.end();
    if (!done && !e.alive && last != last_.end()) {
      bits = static_cast<uint8_t>(last->second | own);
      done = true;
    }
    if (!done) {
      const uint8_t was = last != last_.end() ? last->second : 0;
      for (int team = 0; team < kNumTeams; ++team) {
        const uint8_t bit = TeamBit(team);
        if (bit == own) continue;
        if (SeenBy(team, e, (was & bit) != 0)) bits = static_cast<uint8_t>(bits | bit);
      }
    }
  }
  bits_[i] = bits;
  (*state)[i] = 2;
  return bits;
}

void VisionModel::Update(const std::vector<Entity>& entities, const Params& params) {
  params_ = params;
  if (params_.terrain && !params_.terrain->Valid()) params_.terrain = nullptr;
  if (params_.grassMap && (!params_.grass || !params_.grassMap->Valid())) params_.grassMap = nullptr;
  entities_ = &entities;
  sources_.clear();
  index_.clear();
  bits_.assign(entities.size(), 0);
  for (size_t i = 0; i < entities.size(); ++i) {
    const Entity& e = entities[i];
    if (e.id) index_[e.id] = i;
    if (e.alive && e.visionRadius > 0.f && e.team < kNumTeams && (e.tracked || e.id == 0))
      sources_.push_back({e.x, e.y, e.visionRadius, e.hysteresis > 0.f ? e.hysteresis : 0.f, e.team, e.ignoreTerrain,
                          e.ignoreGrass, e.detectsStealth, e.grassId, e.id});
  }
  std::vector<uint8_t> state(entities.size(), 0);
  for (size_t i = 0; i < entities.size(); ++i)
    if (entities[i].id) Resolve(i, &state);
  last_.clear();
  for (size_t i = 0; i < entities.size(); ++i)
    if (entities[i].id) last_[entities[i].id] = bits_[i];
  entities_ = nullptr;
}

uint8_t VisionModel::TeamsSeeingPoint(float x, float y, uint8_t team, float radius) const {
  uint8_t bits = TeamBit(team);
  const uint32_t pointPatch = params_.grassMap ? params_.grassMap->At(x, y) : 0;
  for (const Source& s : sources_) {
    const uint8_t bit = TeamBit(s.team);
    if (bits & bit) continue;
    const float dx = s.x - x, dy = s.y - y;
    const float reach = s.radius + (radius > 0.f ? radius : 0.f);
    if (dx * dx + dy * dy > reach * reach) continue;
    if (params_.terrain && !s.ignoreTerrain && params_.terrain->Blocked(s.x, s.y, x, y)) continue;
    if (params_.grassMap && !s.ignoreGrass && params_.grassMap->BlockedBetween(s.x, s.y, x, y, s.grassId, pointPatch))
      continue;
    bits = static_cast<uint8_t>(bits | bit);
  }
  return bits;
}

}  // namespace gms::vision
