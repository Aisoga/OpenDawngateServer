// Fog of war on the match host, the engine side (D2g-live step 2): see fow.h.
#include "fow.h"

#include <windows.h>

#include <algorithm>
#include <cstring>
#include <unordered_map>

#include "core/vision/pursuit.h"
#include "game.h"
#include "log.h"

using namespace game;
namespace vision = gms::vision;

namespace fow {
namespace {

// The line-of-sight manager (sim +0x56C) and its record (LineOfSight_AllocTracked 0x00917450, LineOfSight_AddObject
// 0x00917F40; docs/GMS_PROTOCOL.md *The line-of-sight record*).
namespace mgr {
constexpr std::uintptr_t kLosMap = 0x04;     // u8 cells, width x height
constexpr std::uintptr_t kGrassMap = 0x08;   // u32 cells
constexpr std::uintptr_t kWidth = 0x0C;
constexpr std::uintptr_t kHeight = 0x10;
constexpr std::uintptr_t kOriginX = 0x1C;    // f32 width / 2
constexpr std::uintptr_t kOriginY = 0x20;
constexpr std::uintptr_t kScaleX = 0x24;     // f32 cells per unit
constexpr std::uintptr_t kScaleY = 0x28;
constexpr std::uintptr_t kPool = 0x1A030;    // record pointers; the first `in use` are the live ones
}  // namespace mgr
namespace rec {
constexpr std::uintptr_t kObject = 0x00;
constexpr std::uintptr_t kGridHandle = 0x04;  // != 0: in the grid (a vision source)
constexpr std::uintptr_t kX = 0x0C, kY = 0x10;
constexpr std::uintptr_t kRadius = 0x14;
constexpr std::uintptr_t kHysteresis = 0x18;
constexpr std::uintptr_t kFlags = 0x1C;       // 1 owner vision, 2 ignore terrain, 4 ignore grass, 8 proxy
constexpr std::uintptr_t kTeam = 0x1D;
constexpr std::uintptr_t kGrassConceal = 0x1F;
constexpr std::uintptr_t kDisabledUpdates = 0x20;  // u8 (AllocTracked clears the byte)
constexpr std::uintptr_t kLastSeen = 0x2C;         // f32 x, y per team (+ team x 8; GameObject_GetLastSeenPursuitPosition)
constexpr std::uintptr_t kTerrainMask = 0x51;      // u8 team bits (LineOfSightRecord_IsVisibleToTeam 0x00880640)
}  // namespace rec
// cLocomotionData's follow (Locomotion_FollowObject 0x0088FD50, Locomotion_CheckFollowTarget 0x00890470).
namespace follow {
constexpr std::uintptr_t kTarget = 0x08;      // tObjID
constexpr std::uintptr_t kTargetSeen = 0x0C;  // u8
constexpr std::uintptr_t kGoal = 0xBC;        // f32 x 3: the path goal (0x0088ED00 sets it; MoveToPoint's point)
// +0xC8 (loco::kGoal, MoveToPoint's point too) holds a follow's target position at its last refresh: FollowObject
// writes the target's real position there even for a hidden target; Locomotion_TickMovers compares it with the
// target's position (a seen target that moved > 1 unit gets a new goal) or, hidden, refreshes both from the last-seen
constexpr std::uintptr_t kNotFollowFlag = 0x100;  // goal flags: Locomotion_TickMovers follows only without it
// goal flags of a follow: 1 (Locomotion_FollowObject), 0x400 (Locomotion_FollowAsPet). nLocomotion.AttachObject
// (0x0091C2E0) also sets +8 (the parent) but flags 0, 0x2000 or 0x4000 and movement type 2 (attached): no follow.
constexpr std::uint32_t kFollowFlags = 0x401;
constexpr std::uint8_t kMovementAttached = 2;
}  // namespace follow
// cGameObject fields of the visibility pass (sporelabsObject members 0-3) and the record pointer.
constexpr std::uintptr_t kRecordOfObject = 0x858;
constexpr std::uintptr_t kServerGrassId = 0x87C;
constexpr std::uintptr_t kServerGrassPackedXY = 0x880;
constexpr std::uintptr_t kVisionRadius = 0x884;
constexpr std::uintptr_t kVisibleToTeam = 0x888;
constexpr std::uintptr_t kNounControlsVisibility = 0x2CD;  // noun data: fowControlsObjectVisibility
constexpr std::uintptr_t kStealthType = 0x1E74;            // cAgentBlackboard (GameObject_IsStealthed 0x008A9B30)
constexpr int kAttrStealthDetection = 47;
// cAgentBlackboard's grass fields (AgentBlackboard_RevealInGrass 0x008AA160): mbInGrass (reflected member 4 of the
// blackboard's A2), the teams revealed to, per team the reveal timer in ms and its state (1 timed, 2 untimed; this
// build's four calls, in LabsAbilities_CreateAbilityObject, all start timed ones of grassRevealDuration, 1 s).
constexpr std::uintptr_t kInGrass = 0x1E75;
constexpr std::uintptr_t kRevealedTeams = 0x1E76;
constexpr std::uintptr_t kRevealTimerMs = 0x1E78;  // i32 x 3
constexpr std::uintptr_t kRevealState = 0x1E84;    // i32 x 3

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  Log("EXCEPTION in fog of war (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

// The host's copies of the maps.
std::vector<std::uint8_t> g_los;
std::vector<std::uint32_t> g_grass;
vision::LosMap g_terrain;
vision::GrassMap g_grassMap;
bool g_haveTerrain = false, g_haveGrass = false, g_mapLogged = false;
unsigned long long g_terrainCopiedMs = 0;

struct Geometry {
  int width, height;
  float ox, oy, sx, sy;
  const void* los;
  const void* grass;
};

bool SehGeometry(void* sim, Geometry* g) {
  __try {
    char* m = Field<char*>(sim, los::kOfSim);
    if (!m) return false;
    g->los = Field<const void*>(m, mgr::kLosMap);
    g->grass = Field<const void*>(m, mgr::kGrassMap);
    g->width = Field<int>(m, mgr::kWidth);
    g->height = Field<int>(m, mgr::kHeight);
    g->ox = Field<float>(m, mgr::kOriginX);
    g->oy = Field<float>(m, mgr::kOriginY);
    g->sx = Field<float>(m, mgr::kScaleX);
    g->sy = Field<float>(m, mgr::kScaleY);
    return true;
  } __except (SehNote(GetExceptionInformation(), "geometry")) {
    return false;
  }
}

bool SehCopy(void* dst, const void* src, std::size_t n) {
  __try {
    std::memcpy(dst, src, n);
    return true;
  } __except (SehNote(GetExceptionInformation(), "map copy")) {
    return false;
  }
}

void RefreshMaps(void* sim) {
  const unsigned long long now = GetTickCount64();
  if (g_haveTerrain && g_haveGrass && now - g_terrainCopiedMs < 1000) return;
  Geometry g{};
  if (!SehGeometry(sim, &g) || g.width <= 0 || g.height <= 0 || g.width > 4096 || g.height > 4096) return;
  const std::size_t cells = static_cast<std::size_t>(g.width) * static_cast<std::size_t>(g.height);
  vision::LosMap geom;
  geom.width = g.width;
  geom.height = g.height;
  geom.originX = g.ox;
  geom.originY = g.oy;
  geom.scaleX = g.sx;
  geom.scaleY = g.sy;
  if (g.los && (now - g_terrainCopiedMs >= 1000 || !g_haveTerrain)) {
    g_los.resize(cells);
    if (SehCopy(g_los.data(), g.los, cells)) {
      g_terrain = geom;
      g_terrain.cells = g_los.data();
      g_haveTerrain = true;
      g_terrainCopiedMs = now;
    }
  }
  if (g.grass && !g_haveGrass) {
    g_grass.resize(cells);
    if (SehCopy(g_grass.data(), g.grass, cells * 4)) {
      g_grassMap.cells = g_grass.data();
      g_grassMap.geometry = geom;
      g_haveGrass = true;
    }
  }
  if (!g_mapLogged && g_haveTerrain) {
    g_mapLogged = true;
    std::size_t blocked = 0, grass = 0;
    for (std::uint8_t c : g_los) blocked += c != 0;
    for (std::uint32_t c : g_grass) grass += (c & 0xFF000000u) != 0;
    Log("fog: line-of-sight map %dx%d, origin (%.1f, %.1f), %.4f x %.4f cells per unit, %zu blocked cells (%.1f %%); "
        "grass map %s, %zu grass cells",
        g.width, g.height, g.ox, g.oy, g.sx, g.sy, blocked, 100.0 * static_cast<double>(blocked) / static_cast<double>(cells),
        g_haveGrass ? "copied" : "missing", grass);
  }
}

// One object's inputs (no C++ objects here: SEH). False on an exception.
struct Raw {
  bool tracked, inGrid, controls, maintain, stealthed, detects, grassChanged, inGrass;
  std::uintptr_t record;
  std::uint8_t flags, team, conceal, revealed, revealEnded;
  float radius, hysteresis, pos[3], quat[4];
  std::int32_t npcType;
  std::uint32_t owner, grassId;
};

// What the stripped pass did for an object's blackboard in grass (Read's comment): mbInGrass, and the reveal timers.
void GrassBlackboard(char* bb, char* t, bool inGrass, unsigned elapsedMs, Raw* r) {
  std::uint8_t& flag = Field<std::uint8_t>(bb, kInGrass);
  const std::uint8_t defaultConceal = (r->flags & 4) ? 0 : vision::kDefaultGrassConceal;
  if (!inGrass) {
    if (flag) {  // out of the grass: every reveal ends, the concealment is the record's default again
      flag = 0;
      Field<std::uint8_t>(bb, kRevealedTeams) = 0;
      for (int team = 0; team < vision::kNumTeams; ++team) {
        Field<std::int32_t>(bb, kRevealTimerMs + 4 * team) = 0;
        Field<std::int32_t>(bb, kRevealState + 4 * team) = 0;
      }
      if (t) Field<std::uint8_t>(t, rec::kGrassConceal) = defaultConceal;
    }
    return;
  }
  flag = 1;
  r->inGrass = true;
  std::uint8_t& revealed = Field<std::uint8_t>(bb, kRevealedTeams);
  for (int team = 0; team < vision::kNumTeams; ++team) {
    std::int32_t& state = Field<std::int32_t>(bb, kRevealState + 4 * team);
    if (state != 1) continue;  // 2 (untimed) isn't started by this build
    std::int32_t& ms = Field<std::int32_t>(bb, kRevealTimerMs + 4 * team);
    ms -= static_cast<std::int32_t>(elapsedMs);
    if (ms > 0) continue;
    ms = 0;
    state = 0;
    const std::uint8_t bit = vision::TeamBit(team);
    revealed = static_cast<std::uint8_t>(revealed & ~bit);
    if (t) Field<std::uint8_t>(t, rec::kGrassConceal) |= static_cast<std::uint8_t>(defaultConceal & bit);
    r->revealEnded = static_cast<std::uint8_t>(r->revealEnded | bit);
  }
  r->revealed = revealed;
}

bool SehReadObject(void* o, bool readStealth, bool writeRadius, bool writeGrass, bool grassRule, unsigned elapsedMs,
                   Raw* r, int* radiusWrites, int* grassWrites) {
  __try {
    std::memset(r, 0, sizeof *r);
    r->controls = true;
    r->npcType = -1;
    r->team = Field<std::uint8_t>(o, gobj::kTeam);
    r->owner = Field<std::uint32_t>(o, gobj::kOwner);
    std::memcpy(r->pos, static_cast<char*>(o) + gobj::kPosition, sizeof r->pos);
    std::memcpy(r->quat, static_cast<char*>(o) + gobj::kOrientation, sizeof r->quat);
    void* noun = Field<void*>(o, gobj::kNoun);
    char* nd = noun ? Field<char*>(noun, noun::kData) : nullptr;
    if (nd) r->controls = Field<std::uint8_t>(nd, kNounControlsVisibility) != 0;
    void* npc = Field<void*>(o, npc::kOfObject);
    char* npcData = npc ? Field<char*>(npc, noun::kData) : nullptr;
    if (npcData) r->npcType = Field<std::int32_t>(npcData, npc::kType);
    char* t = Field<char*>(o, kRecordOfObject);
    if (!t) return true;
    r->tracked = true;
    r->record = reinterpret_cast<std::uintptr_t>(t);
    r->inGrid = Field<std::uint32_t>(t, rec::kGridHandle) != 0;
    r->radius = Field<float>(t, rec::kRadius);
    r->hysteresis = Field<float>(t, rec::kHysteresis);
    r->flags = Field<std::uint8_t>(t, rec::kFlags);
    r->maintain = Field<std::uint8_t>(t, rec::kDisabledUpdates) != 0;
    if (readStealth) {
      char* bb = Field<char*>(o, agent::kOfObject);
      r->stealthed = bb && Field<std::uint8_t>(bb, kStealthType) != 0;
      if (r->inGrid) {
        void* ad = Field<void*>(o, gobj::kAttributes);
        r->detects = ad && GetAttribute(ad, kAttrStealthDetection, 1) > 0.f;
      }
    }
    if (writeRadius) {
      // mVisionRadius on every source (the client draws its team's vision circles from it)
      const float radius = r->inGrid ? r->radius : 0.f;
      float& vr = Field<float>(o, kVisionRadius);
      if (vr != radius) {
        vr = radius;
        ++*radiusWrites;
      }
    }
    if (writeGrass && g_haveGrass) {
      std::uint32_t& id = Field<std::uint32_t>(o, kServerGrassId);
      std::uint32_t& packed = Field<std::uint32_t>(o, kServerGrassPackedXY);
      if (vision::UpdateGrassId(g_grassMap, r->pos[0], r->pos[1], &id, &packed)) {
        ++*grassWrites;
        r->grassChanged = true;
      }
    }
    r->grassId = Field<std::uint32_t>(o, kServerGrassId);
    if (grassRule && g_haveGrass) {
      char* bb = Field<char*>(o, agent::kOfObject);
      if (bb) GrassBlackboard(bb, t, r->grassId != 0, elapsedMs, r);
    }
    r->conceal = Field<std::uint8_t>(t, rec::kGrassConceal);  // after the reveal timers
    return true;
  } __except (SehNote(GetExceptionInformation(), "object")) {
    return false;
  }
}

// The record pool's anonymous sources: proxies (flag 8) and records of live objects outside the scan. `scanned` holds
// the scanned objects, sorted. At most `max`.
struct PoolSource {
  bool proxy;
  std::uint8_t team, flags;
  float x, y, radius, hysteresis;
  bool inGrid;
};

int SehPoolSources(void* sim, const void* const* scanned, int nScanned, PoolSource* out, int max) {
  __try {
    char* m = Field<char*>(sim, los::kOfSim);
    if (!m) return 0;
    char** recs = Field<char**>(m, mgr::kPool);
    const int inUse = Field<int>(m, los::kPoolInUse);
    if (!recs || inUse <= 0) return 0;
    int n = 0;
    for (int i = 0; i < inUse && n < max; ++i) {
      char* r = recs[i];
      if (!r) continue;
      const std::uint8_t flags = Field<std::uint8_t>(r, rec::kFlags);
      void* obj = Field<void*>(r, rec::kObject);
      const bool proxy = (flags & 8) != 0;
      if (!proxy) {
        if (!obj) continue;  // a positional event's record (kind 3 of the outgoing vector)
        if (std::binary_search(scanned, scanned + nScanned, static_cast<const void*>(obj))) continue;
        if (Field<std::uint8_t>(obj, gobj::kPendingDelete)) continue;
      }
      PoolSource& s = out[n++];
      s.proxy = proxy;
      s.team = Field<std::uint8_t>(r, rec::kTeam);
      s.flags = flags;
      s.inGrid = Field<std::uint32_t>(r, rec::kGridHandle) != 0;
      s.radius = Field<float>(r, rec::kRadius);
      s.hysteresis = Field<float>(r, rec::kHysteresis);
      if (proxy || !obj) {
        s.x = Field<float>(r, rec::kX);
        s.y = Field<float>(r, rec::kY);
      } else {
        s.x = Field<float>(obj, gobj::kPosition);
        s.y = Field<float>(obj, gobj::kPosition + 4);
      }
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "record pool")) {
    return 0;
  }
}

bool SehWriteByte(void* o, std::uintptr_t off, std::uint8_t v, bool* changed) {
  __try {
    std::uint8_t& b = Field<std::uint8_t>(o, off);
    *changed = b != v;
    b = v;
    return true;
  } __except (SehNote(GetExceptionInformation(), "write")) {
    return false;
  }
}

bool SehWriteRecordByte(void* o, std::uintptr_t off, std::uint8_t v, bool* changed) {
  __try {
    char* t = Field<char*>(o, kRecordOfObject);
    if (!t) return false;
    std::uint8_t& b = Field<std::uint8_t>(t, off);
    *changed = b != v;
    b = v;
    return true;
  } __except (SehNote(GetExceptionInformation(), "record write")) {
    return false;
  }
}

// The object's record (still the one `record` names) +0x2C + team x 8 = (x, y) for every team in `teams`.
int SehWriteLastSeen(void* o, std::uintptr_t record, std::uint8_t teams, float x, float y) {
  __try {
    char* t = Field<char*>(o, kRecordOfObject);
    if (!t || reinterpret_cast<std::uintptr_t>(t) != record) return 0;
    int n = 0;
    for (int team = 0; team < vision::kNumTeams; ++team) {
      if (!(teams & vision::TeamBit(team))) continue;
      Field<float>(t, rec::kLastSeen + 8 * team) = x;
      Field<float>(t, rec::kLastSeen + 8 * team + 4) = y;
      ++n;
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "last-seen write")) {
    return 0;
  }
}

// The follow of object o and its team (no C++ objects here: SEH). False without a follow.
bool SehReadFollow(void* o, std::uint32_t* target, std::uint8_t* team, bool* seen, std::uint32_t* flags,
                   float* goal, float* targetPos) {
  __try {
    *team = Field<std::uint8_t>(o, gobj::kTeam);
    char* l = Field<char*>(o, loco::kOfObject);
    if (!l) return false;
    *target = Field<std::uint32_t>(l, follow::kTarget);
    *flags = Field<std::uint32_t>(l, loco::kGoalFlags);
    if (!*target || (*flags & follow::kNotFollowFlag) || !(*flags & follow::kFollowFlags)) return false;
    if (Field<std::uint8_t>(l, loco::kMovementType) == follow::kMovementAttached) return false;
    *seen = Field<std::uint8_t>(l, follow::kTargetSeen) != 0;
    std::memcpy(goal, l + follow::kGoal, 3 * sizeof(float));
    std::memcpy(targetPos, l + loco::kGoal, 3 * sizeof(float));
    return true;
  } __except (SehNote(GetExceptionInformation(), "follow read")) {
    return false;
  }
}

bool SehReadLastSeen(void* target, std::uint8_t team, float* xy) {
  __try {
    char* t = Field<char*>(target, kRecordOfObject);
    if (!t || team >= vision::kNumTeams) return false;
    xy[0] = Field<float>(t, rec::kLastSeen + 8 * team);
    xy[1] = Field<float>(t, rec::kLastSeen + 8 * team + 4);
    return true;
  } __except (SehNote(GetExceptionInformation(), "last-seen read")) {
    return false;
  }
}

vision::LastSeenBook g_lastSeen;

bool SehSetVisible(void* o, std::uint16_t mask) {
  __try {
    char* nc = Field<char*>(o, gobj::kNetwork);
    if (!nc) return false;
    Field<std::uint16_t>(nc, kNetworkVisibleMask) = mask;
    return true;
  } __except (SehNote(GetExceptionInformation(), "SetVisibleMask")) {
    return false;
  }
}

}  // namespace

bool Read(void* sim, const std::vector<repl::NetObject>& objs, const Options& options, unsigned elapsedMs,
          std::vector<vision::Entity>* entities, std::vector<ObjectInfo>* info, ReadCounts* counts) {
  *counts = ReadCounts{};
  entities->clear();
  info->assign(objs.size(), ObjectInfo());
  RefreshMaps(sim);
  if (!g_haveTerrain) return false;
  const bool writeRadius = options.render, writeGrass = options.render || options.grass;
  entities->reserve(objs.size() + 16);
  std::vector<const void*> scanned(objs.size());
  for (size_t i = 0; i < objs.size(); ++i) {
    const repl::NetObject& o = objs[i];
    scanned[i] = o.object;
    vision::Entity e;
    Raw r;
    if (!SehReadObject(o.object, options.stealth, writeRadius, writeGrass, options.grass, elapsedMs, &r,
                       &counts->radiusWrites, &counts->grassWrites)) {
      ++counts->failed;
      e.id = 0;  // no vision, no answer
      e.team = 0xFF;
      entities->push_back(e);
      continue;
    }
    e.id = o.id;
    e.team = r.team;
    e.x = r.pos[0];
    e.y = r.pos[1];
    e.alive = o.hp < 0.f || o.hp > 0.f;  // -1: no combatant data
    e.tracked = r.tracked;
    e.neverFogged = vision::NeverFoggedType(o.nounType, r.npcType);
    e.controlsVisibility = r.controls;
    if (r.tracked) {
      ++counts->tracked;
      if (r.inGrid) {
        e.visionRadius = r.radius;
        ++counts->sources;
      } else {
        e.areaRadius = r.radius;
      }
      e.hysteresis = r.hysteresis;
      e.useOwnerVision = (r.flags & 1) != 0;
      e.ignoreTerrain = (r.flags & 2) != 0;
      e.ignoreGrass = (r.flags & 4) != 0;
      e.grassConceal = r.conceal;
      e.stealthed = r.stealthed;
      e.detectsStealth = r.detects;
    }
    e.owner = r.owner;
    e.grassId = r.grassId;
    entities->push_back(e);
    ObjectInfo& in = (*info)[i];
    in.tracked = r.tracked;
    in.record = r.record;
    in.maintain = r.maintain;
    in.grassChanged = r.grassChanged;
    in.grassId = r.grassId;
    in.revealed = r.revealed;
    in.revealEnded = r.revealEnded;
    if (r.inGrass) ++counts->inGrass;
    if (r.inGrass && r.revealed) ++counts->revealed;
    std::memcpy(in.pos, r.pos, sizeof in.pos);
    std::memcpy(in.quat, r.quat, sizeof in.quat);
  }
  std::sort(scanned.begin(), scanned.end());
  static PoolSource pool[1024];
  const int n = SehPoolSources(sim, scanned.data(), static_cast<int>(scanned.size()), pool, 1024);
  for (int k = 0; k < n; ++k) {
    const PoolSource& s = pool[k];
    if (!s.inGrid || s.radius <= 0.f) continue;
    vision::Entity e;
    e.id = 0;
    e.team = s.team;
    e.x = s.x;
    e.y = s.y;
    e.tracked = true;
    e.visionRadius = s.radius;
    e.hysteresis = s.hysteresis;
    e.ignoreTerrain = (s.flags & 2) != 0;
    e.ignoreGrass = (s.flags & 4) != 0;
    entities->push_back(e);
    if (s.proxy) ++counts->proxies;
    else ++counts->unscanned;
  }
  return true;
}

const vision::LosMap* Terrain() { return g_haveTerrain ? &g_terrain : nullptr; }

const vision::GrassMap* Grass() { return g_haveGrass ? &g_grassMap : nullptr; }

int WriteTerrainMask(const std::vector<repl::NetObject>& objs, const std::vector<vision::Entity>& entities,
                     const std::vector<std::uint8_t>& bits) {
  int changed = 0;
  for (size_t i = 0; i < objs.size() && i < entities.size() && i < bits.size(); ++i) {
    if (!entities[i].id || !entities[i].tracked) continue;
    bool c = false;
    if (SehWriteRecordByte(objs[i].object, rec::kTerrainMask, bits[i], &c) && c) ++changed;
  }
  return changed;
}

int WriteVisibility(const std::vector<repl::NetObject>& objs, const std::vector<vision::Entity>& entities,
                    const std::vector<std::uint8_t>& bits) {
  int changed = 0;
  for (size_t i = 0; i < objs.size() && i < entities.size() && i < bits.size(); ++i) {
    if (!entities[i].id) continue;
    bool c = false;
    if (SehWriteByte(objs[i].object, kVisibleToTeam, bits[i], &c) && c) ++changed;
  }
  return changed;
}

bool SetVisibleMask(void* object, std::uint16_t mask) { return SehSetVisible(object, mask); }

int WriteLastSeen(const std::vector<repl::NetObject>& objs, const std::vector<vision::Entity>& entities,
                  const std::vector<ObjectInfo>& info, const std::vector<std::uint8_t>& bits, int* fresh) {
  int written = 0;
  g_lastSeen.Begin();
  for (size_t i = 0; i < objs.size() && i < entities.size() && i < info.size() && i < bits.size(); ++i) {
    const vision::Entity& e = entities[i];
    if (!e.id || !e.tracked || !info[i].record) continue;
    const std::uint8_t teams = g_lastSeen.Teams(e.id, info[i].record, bits[i]);
    if (teams) written += SehWriteLastSeen(objs[i].object, info[i].record, teams, e.x, e.y);
  }
  g_lastSeen.End();
  if (fresh) *fresh = g_lastSeen.Fresh();
  return written;
}

int ReadPursuits(const std::vector<repl::NetObject>& objs, std::vector<Pursuit>* out) {
  out->clear();
  std::unordered_map<std::uint32_t, void*> byId;
  for (const repl::NetObject& o : objs) byId[o.id] = o.object;
  for (size_t i = 0; i < objs.size(); ++i) {
    Pursuit p;
    if (!SehReadFollow(objs[i].object, &p.target, &p.team, &p.targetSeen, &p.flags, p.goal, p.targetPos)) continue;
    p.follower = i;
    auto it = byId.find(p.target);
    void* target = it != byId.end() ? it->second : GetObjectById(p.target);
    if (target) p.targetTracked = SehReadLastSeen(target, p.team, p.lastSeen);
    out->push_back(p);
  }
  return static_cast<int>(out->size());
}

}  // namespace fow
