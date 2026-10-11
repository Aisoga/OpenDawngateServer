// The AI director's stripped part (D1c-live): see director.h.
#include "director.h"

#include <windows.h>

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <map>
#include <string>
#include <unordered_map>
#include <vector>

#include "core/matchflow/flow.h"
#include "crowd.h"
#include "game.h"
#include "log.h"

using namespace game;
namespace flow = gms::flow;

namespace director {
namespace {

Options g_opt;
void* g_sim;
bool g_levelOk;
bool g_started;
bool g_stopped;
unsigned g_logLines;

bool LogBudget() {
  ++g_logLines;
  return g_logLines <= static_cast<unsigned>(g_opt.logLimit) || g_logLines % 100 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  Log("EXCEPTION in director (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

// ---- raw level data (read under SEH into PODs) -----------------------------------------------------------------------

constexpr int kMaxChildren = 32;

struct MarkerPos {
  std::uint32_t id;
  float pos[3];
  float rot[3];
  float scale;  // D14: cLabsMarker.scale (a buff camp's major marker 2.0)
};

// A marker that matters to the director: a spline node, a spawn point (noun or marker SpawnPointDef) or a titan node.
struct RawMarker {
  std::uint32_t id;
  void* noun;
  char nounName[48];
  float pos[3];
  float rot[3];
  std::uint32_t target;
  std::uint32_t children[kMaxChildren];
  int numChildren;
  bool hasSpline;
  char splineName[33];
  int knot, lane, team;
  void* markerSpawnDef;
  void* nounSpawnDef;
  void* titanNodeDef;
  int nounTeam;
};

struct SpawnDef {
  bool ok = false;
  int type = 0;
  int team = 0;
  float delay = 0.f;
  float respawn = 0.f;
  void* creeps[4] = {};
  float counts[4] = {};
};

void CopyName(char* out, std::size_t size, const char* s) {
  std::size_t k = 0;
  if (s)
    for (; s[k] && k + 1 < size; ++k) out[k] = s[k];
  out[k] = 0;
}

bool SehReadMarkers(void* level, MarkerPos* pos, int maxPos, int* nPos, RawMarker* rel, int maxRel, int* nRel,
                    int* sets) {
  __try {
    *nPos = *nRel = *sets = 0;
    char* ld = Field<char*>(level, noun::kData);
    if (!ld) return false;
    void** setArr = Field<void**>(ld, level::kMarkersets);
    const int ns = Field<int>(ld, level::kMarkersets + 4);
    for (int s = 0; s < ns; ++s) {
      void* ms = setArr[s];
      char* md = ms ? Field<char*>(ms, noun::kData) : nullptr;
      if (!md) continue;
      ++*sets;
      char* markers = Field<char*>(md, 0);
      const int nm = Field<int>(md, 4);
      for (int i = 0; i < nm; ++i) {
        char* m = markers + i * marker::kSize;
        if (*nPos < maxPos) {
          MarkerPos& p = pos[(*nPos)++];
          p.id = Field<std::uint32_t>(m, marker::kId);
          std::memcpy(p.pos, m + marker::kPos, sizeof p.pos);
          std::memcpy(p.rot, m + marker::kRotDegrees, sizeof p.rot);
          p.scale = Field<float>(m, marker::kScale);
        }
        void* nounAsset = Field<void*>(m, marker::kNoun);
        char* nd = nounAsset ? Field<char*>(nounAsset, noun::kData) : nullptr;
        char* spline = Field<char*>(m, marker::kSplinePathNode);
        void* mDef = Field<void*>(m, marker::kSpawnPointDef);
        void* nDef = nd ? Field<void*>(nd, nounData::kSpawnPointDef) : nullptr;
        void* titan = nd ? Field<void*>(nd, nounData::kTitanNodeDef) : nullptr;
        if (!spline && !mDef && !nDef && !titan) continue;
        if (*nRel >= maxRel) continue;
        RawMarker& r = rel[(*nRel)++];
        std::memset(&r, 0, sizeof r);
        r.id = Field<std::uint32_t>(m, marker::kId);
        r.noun = nounAsset;
        CopyName(r.nounName, sizeof r.nounName, nounAsset ? Field<const char*>(nounAsset, 0) : nullptr);
        std::memcpy(r.pos, m + marker::kPos, sizeof r.pos);
        std::memcpy(r.rot, m + marker::kRotDegrees, sizeof r.rot);
        r.target = Field<std::uint32_t>(m, marker::kTarget);
        const std::uint32_t* kids = Field<const std::uint32_t*>(m, marker::kChildren);
        const int nk = Field<int>(m, marker::kChildren + 4);
        for (int k = 0; kids && k < nk && k < kMaxChildren; ++k) r.children[r.numChildren++] = kids[k];
        if (spline) {
          r.hasSpline = true;
          CopyName(r.splineName, sizeof r.splineName, spline);  // char[32] at +0
          r.knot = Field<int>(spline, 0x20);
          r.lane = Field<int>(spline, 0x24);
          r.team = Field<int>(spline, 0x28);
        }
        r.markerSpawnDef = mDef;
        r.nounSpawnDef = nDef;
        r.titanNodeDef = titan;
        r.nounTeam = nd ? Field<int>(nd, nounData::kSpawnTeam) : 0;
      }
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "ReadMarkers")) {
    return false;
  }
}

bool SehReadSpawnDef(void* def, SpawnDef* out) {
  __try {
    *out = SpawnDef();
    if (!def) return false;
    out->type = Field<int>(def, spawnDef::kType);
    out->team = Field<int>(def, spawnDef::kTeam);
    out->delay = Field<float>(def, spawnDef::kDelay);
    out->respawn = Field<float>(def, spawnDef::kRespawn);
    for (int k = 0; k < 4; ++k) out->creeps[k] = Field<void*>(def, spawnDef::kCreeps + 4 * k);
    const float* up = Field<const float*>(def, spawnDef::kCampUpgrade);
    const int n = Field<int>(def, spawnDef::kCampUpgrade + 4);
    if (up && n > 0)
      for (int k = 0; k < 4; ++k) out->counts[k] = up[k];
    out->ok = true;
    return true;
  } __except (SehNote(GetExceptionInformation(), "ReadSpawnDef")) {
    return false;
  }
}

// The spawn definition of a noun asset (noun data +0x2AC), for overrides.
bool SehNounSpawnDef(void* nounAsset, SpawnDef* out) {
  void* def = nullptr;
  __try {
    char* nd = nounAsset ? Field<char*>(nounAsset, noun::kData) : nullptr;
    def = nd ? Field<void*>(nd, nounData::kSpawnPointDef) : nullptr;
  } __except (SehNote(GetExceptionInformation(), "NounSpawnDef")) {
    return false;
  }
  return SehReadSpawnDef(def, out);
}

const char* SehAssetName(void* asset) {
  __try {
    const char* s = asset ? Field<const char*>(asset, 0) : nullptr;
    return s ? s : "?";
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return "?";
  }
}

bool SehReadTuning(void* level, Tuning* t) {
  __try {
    char* ld = Field<char*>(level, noun::kData);
    void* asset = ld ? Field<void*>(ld, level::kGameplayTuning) : nullptr;
    char* d = asset ? Field<char*>(asset, noun::kData) : nullptr;
    if (!d) return false;
    t->waveDelay = Field<float>(d, gameplayTuning::kMinionSpawnDelay1);
    t->waveInterval = Field<float>(d, gameplayTuning::kMinionSpawnInterval1);
    t->nubRespawn = Field<float>(d, gameplayTuning::kNubRespawnTime);
    t->wellLockFirst = Field<float>(d, 0x7C);
    t->read = true;
    return true;
  } __except (SehNote(GetExceptionInformation(), "ReadTuning")) {
    return false;
  }
}

bool SehTitanNode(void* titanDef, void** spawned, bool* isBase, bool* isGoldMine) {
  __try {
    *spawned = Field<void*>(titanDef, 0x18);
    *isBase = Field<unsigned char>(titanDef, 0x1E) != 0;
    *isGoldMine = Field<unsigned char>(titanDef, 0x1F) != 0;
    return true;
  } __except (SehNote(GetExceptionInformation(), "TitanNode")) {
    return false;
  }
}

// marker id -> object id for every object that came from a marker (object +0x84), before the first tick.
int SehScanMarkerObjects(void* sim, std::uint32_t* markers, std::uint32_t* ids, int max) {
  __try {
    void* om = Field<void*>(sim, sim::kObjectManager);
    int it = ObjMgrIterBegin(om);
    int n = 0;
    for (void* o = ObjMgrIterNext(om, &it); o && n < max; o = ObjMgrIterNext(om, &it)) {
      const std::uint32_t mk = Field<std::uint32_t>(o, gobj::kMarkerId);
      if (!mk) continue;
      markers[n] = mk;
      ids[n] = Field<std::uint32_t>(o, gobj::kId);
      ++n;
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "ScanMarkerObjects")) {
    return -1;
  }
}

bool CodeMatches(std::uintptr_t va, const unsigned char* bytes, std::size_t n) {
  __try {
    return std::memcmp(reinterpret_cast<const void*>(va), bytes, n) == 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

// ---- the level, as the director keeps it -----------------------------------------------------------------------------

struct Lane {
  std::string name;               // the spline's name (blue_top, ...)
  int laneId = 0;                 // nLaneID of knot 0 (1 Top, 2 Bottom)
  int team = 0;
  std::uint32_t headMarker = 0;
  std::uint32_t splineId = 0;     // the spline-path map's key (the head node's object id)
  std::vector<flow::Point> knots;
  bool inTable = false;
};

struct Spawner {
  std::uint32_t marker = 0;
  std::string name;
  void* noun = nullptr;
  SpawnDef def;    // effective: the noun's composition, the marker's timing where it has one
  bool markerTiming = false;
  float pos[3] = {};
  float rot[3] = {};
  std::uint32_t target = 0;
  int lane = -1;   // index into g_lanes
  int waves = 0, spawned = 0;
};

struct Camp {
  std::uint32_t marker = 0;
  std::string name;
  SpawnDef def;
  bool markerTiming = false;
  bool parasite = false;
  float pos[3] = {};
  float rot[3] = {};
  std::vector<MarkerPos> spots;  // the creature markers, in marker order
  void* record = nullptr;        // cCreepCampData (AIDirectorImpl_FindCampByMarkerId)
  std::uint32_t recordId = 0;    // its +0x00
  flow::CampTimer timer;
  std::vector<std::uint32_t> members;
};

std::vector<Lane> g_lanes;
std::vector<Spawner> g_spawners;
std::vector<Camp> g_camps;
std::vector<TitanSpawn> g_guardians;
std::vector<Well> g_wells;
std::unordered_map<std::uint32_t, std::uint32_t> g_markerObjects;
std::map<void*, void*> g_overrides;  // spawner noun asset -> override noun asset
flow::WaveSchedule g_waves;
Tuning g_tuning;
int g_camp_parasite = -1;
std::uint32_t g_parasite = 0;
double g_lastSecond = -1.0;
double g_lastTrace = -1.0;
std::vector<std::uint32_t> g_traced;  // the first wave's minions (one per spawner), followed in the lane trace

struct Stats {
  unsigned waves = 0, minions = 0, failed = 0, campSpawns = 0, creatures = 0, campClears = 0, members = 0;
  unsigned overrides = 0, resets = 0, parasiteStages = 0, exceptions = 0, laneEntries = 0, laneAssigned = 0;
  unsigned membersPruned = 0;
} g_st;

void* g_director;  // sim +0x560
void* g_impl;      // director +0x1C

// The game's string pool for allocations ("SP_..." names); one literal for the spline nodes.
const char kSplineNodeName[] = "SP_Simulator/SplinePath (host)";

// ---- the spline-path map ---------------------------------------------------------------------------------------------

struct SplineNode {
  SplineNode* right;
  SplineNode* left;
  SplineNode* parent;
  std::uint8_t color;  // EASTL: 0 red, 1 black
  std::uint8_t pad[3];
  std::uint32_t key;
  float* begin;
  float* end;
  float* capacity;
  std::uint32_t allocator;
};
static_assert(sizeof(SplineNode) == 0x24, "the spline-path map node is 0x24 bytes");

// The knots' storage: a zero word before each vector's begin, so the map's reset (0x008FB290) never frees it.
struct KnotStore {
  std::uint32_t zero;
  float pts[3 * 16];
};
KnotStore g_knotStore[8];

// A balanced tree over keys[lo, hi) (sorted), coloured black except the deepest level of an incomplete tree (a valid
// red-black tree; lookups ignore colours anyway, and nothing inserts after us).
SplineNode* BuildTree(SplineNode** nodes, int lo, int hi, SplineNode* parent, int depth, int fullDepth) {
  if (lo >= hi) return nullptr;
  const int mid = (lo + hi) / 2;
  SplineNode* n = nodes[mid];
  n->parent = parent;
  n->color = depth > fullDepth ? 0 : 1;
  n->left = BuildTree(nodes, lo, mid, n, depth + 1, fullDepth);
  n->right = BuildTree(nodes, mid + 1, hi, n, depth + 1, fullDepth);
  return n;
}

bool SehFillSplineTable(void* sim, SplineNode** nodes, int n) {
  __try {
    char* s = static_cast<char*>(sim);
    if (Field<void*>(s, matchsim::kSplineRoot) != nullptr) {
      Log("director: the spline-path map is not empty (root %p); lane table not filled",
          Field<void*>(s, matchsim::kSplineRoot));
      return false;
    }
    // depth of the last complete level: a tree of n nodes has floor(log2(n + 1)) complete levels
    int full = 0;
    while ((1 << (full + 1)) - 1 <= n) ++full;
    void* anchor = s + matchsim::kSplineAnchor;
    SplineNode* root = BuildTree(nodes, 0, n, static_cast<SplineNode*>(anchor), 1, full);
    Field<void*>(s, matchsim::kSplineAnchor) = nodes[n - 1];      // rightmost
    Field<void*>(s, matchsim::kSplineAnchor + 4) = nodes[0];      // leftmost
    Field<void*>(s, matchsim::kSplineRoot) = root;
    Field<std::uint32_t>(s, matchsim::kSplineSize) = static_cast<std::uint32_t>(n);
    return true;
  } __except (SehNote(GetExceptionInformation(), "FillSplineTable")) {
    return false;
  }
}

void* SehAllocNode() {
  __try {
    return Alloc(sizeof(SplineNode), kSplineNodeName, 0, 0, 0, 0);
  } __except (SehNote(GetExceptionInformation(), "AllocNode")) {
    return nullptr;
  }
}

int SehFindSplinePathSize(void* sim, std::uint32_t key) {
  __try {
    float** v = static_cast<float**>(FindSplinePath(sim, key));
    if (!v) return -1;
    return static_cast<int>((v[1] - v[0]) / 3);
  } __except (SehNote(GetExceptionInformation(), "FindSplinePath")) {
    return -2;
  }
}

void FillLaneTable(void* sim) {
  std::vector<int> order;
  for (int i = 0; i < static_cast<int>(g_lanes.size()); ++i)
    if (g_lanes[i].splineId && !g_lanes[i].knots.empty()) order.push_back(i);
  if (order.empty() || order.size() > 8) {
    Log("director: %zu lanes with a spline id; lane table not filled", order.size());
    return;
  }
  std::sort(order.begin(), order.end(), [](int a, int b) { return g_lanes[a].splineId < g_lanes[b].splineId; });
  std::vector<SplineNode*> nodes;
  for (size_t k = 0; k < order.size(); ++k) {
    Lane& l = g_lanes[order[k]];
    if (k > 0 && l.splineId == g_lanes[order[k - 1]].splineId) {
      Log("director: two lanes with spline id %08X; lane table not filled", l.splineId);
      return;
    }
    auto* node = static_cast<SplineNode*>(SehAllocNode());
    if (!node) {
      Log("director: no memory for a spline node; lane table not filled");
      return;
    }
    std::memset(node, 0, sizeof *node);
    KnotStore& store = g_knotStore[k];
    store.zero = 0;
    const int nk = std::min(static_cast<int>(l.knots.size()), 16);
    for (int j = 0; j < nk; ++j) {
      store.pts[3 * j] = l.knots[j].x;
      store.pts[3 * j + 1] = l.knots[j].y;
      store.pts[3 * j + 2] = l.knots[j].z;
    }
    node->key = l.splineId;
    node->begin = store.pts;
    node->end = store.pts + 3 * nk;
    node->capacity = node->end;
    nodes.push_back(node);
  }
  if (!SehFillSplineTable(sim, nodes.data(), static_cast<int>(nodes.size()))) return;
  for (int i : order) {
    Lane& l = g_lanes[i];
    const int n = SehFindSplinePathSize(sim, l.splineId);
    l.inTable = n == static_cast<int>(l.knots.size());
    ++g_st.laneEntries;
    Log("director: lane table: %s (nLaneID %d, team %d) spline id %08X -> GameSimulator_FindSplinePath %d knots%s",
        l.name.c_str(), l.laneId, l.team, l.splineId, n, l.inTable ? "" : " (MISMATCH)");
  }
}

// ---- spawning --------------------------------------------------------------------------------------------------------

struct Created {
  std::uint32_t id;
  float hp;
  int level;  // the agent's NPC level (+0x1E90), set at creation from the match time
};

bool SehCreate(void* noun, int team, const float* pos, const float* rotDegrees, std::uint32_t splineId, Created* out) {
  __try {
    out->id = 0;
    out->hp = -1.f;
    out->level = -1;
    float q[4] = {0.f, 0.f, 0.f, 1.f};
    DegreesToQuaternion(q, rotDegrees);
    void* o = CreateFromNoun(noun, team, 0, nullptr, pos, q, nullptr);
    if (!o) return false;
    out->id = Field<std::uint32_t>(o, gobj::kId);
    void* c = Field<void*>(o, combatant::kOfObject);
    out->hp = c ? Field<float>(c, combatant::kHitPoints) : -1.f;
    char* ag = Field<char*>(o, agent::kOfObject);
    out->level = ag ? Field<int>(ag, 0x1E90) : -1;
    if (splineId) {
      void* loco = Field<void*>(o, loco::kOfObject);
      if (loco) {
        Field<std::uint32_t>(loco, loco::kSplineId) = splineId;
        Field<std::uint32_t>(loco, loco::kSplineIndex) = 0;
      }
    }
    return out->id != 0;
  } __except (SehNote(GetExceptionInformation(), "CreateFromNoun")) {
    return false;
  }
}

bool SehJoinCamp(std::uint32_t id, void* camp, std::uint32_t campMarker) {
  __try {
    void* o = GetObjectById(id);
    if (!o || !g_impl || !camp) return false;
    Field<std::uint32_t>(o, kObjectCampMarker) = campMarker;
    AddCampMember(g_impl, camp, id);
    return true;
  } __except (SehNote(GetExceptionInformation(), "AddCampMember")) {
    return false;
  }
}

void* SehFindCamp(std::uint32_t marker, std::uint32_t* recordId) {
  __try {
    if (!g_impl) return nullptr;
    void* c = FindCampByMarkerId(g_impl, marker);
    *recordId = c ? Field<std::uint32_t>(c, 0) : 0;
    return c;
  } __except (SehNote(GetExceptionInformation(), "FindCamp")) {
    return nullptr;
  }
}

// Erases the camp-membership entries whose member no longer exists (impl +0x160, 8-byte entries); returns how many.
int SehPruneMembers() {
  __try {
    if (!g_impl) return 0;
    std::uint32_t* b = Field<std::uint32_t*>(g_impl, aidir::kCampMembersBegin);
    std::uint32_t* e = Field<std::uint32_t*>(g_impl, aidir::kCampMembersEnd);
    std::uint32_t* out = b;
    int pruned = 0;
    for (std::uint32_t* p = b; p < e; p += 2) {
      if (!GetObjectById(p[0])) {
        ++pruned;
        continue;
      }
      if (out != p) {
        out[0] = p[0];
        out[1] = p[1];
      }
      out += 2;
    }
    Field<std::uint32_t*>(g_impl, aidir::kCampMembersEnd) = out;
    return pruned;
  } __except (SehNote(GetExceptionInformation(), "PruneMembers")) {
    return 0;
  }
}

// A member's state: 1 alive, 0 dead (HP <= 0) or pending delete, -1 gone.
int SehMemberState(std::uint32_t id, float* hp, float* pos, std::uint32_t* splineIndex, std::uint32_t* splineId = nullptr) {
  __try {
    void* o = GetObjectById(id);
    if (!o) return -1;
    void* c = Field<void*>(o, combatant::kOfObject);
    *hp = c ? Field<float>(c, combatant::kHitPoints) : -1.f;
    if (pos) std::memcpy(pos, static_cast<char*>(o) + gobj::kPosition, 12);
    if (splineIndex) {
      void* loco = Field<void*>(o, loco::kOfObject);
      *splineIndex = loco ? Field<std::uint32_t>(loco, loco::kSplineIndex) : 0xFFFFFFFFu;
      if (splineId) *splineId = loco ? Field<std::uint32_t>(loco, loco::kSplineId) : 0;
    }
    if (Field<unsigned char>(o, gobj::kPendingDelete)) return 0;
    return (c && *hp <= 0.f) ? 0 : 1;
  } __except (SehNote(GetExceptionInformation(), "MemberState")) {
    return -1;
  }
}

bool SehDirector(void* sim) {
  __try {
    g_director = Field<void*>(sim, sim::kAIDirector);
    g_impl = g_director ? Field<void*>(g_director, aidir::kImpl) : nullptr;
    return g_impl != nullptr;
  } __except (SehNote(GetExceptionInformation(), "Director")) {
    return false;
  }
}

bool SehSetParasiteId(std::uint32_t id) {
  __try {
    if (!g_director) return false;
    Field<std::uint32_t>(g_director, aidir::kParasiteId) = id;
    return true;
  } __except (SehNote(GetExceptionInformation(), "SetParasiteId")) {
    return false;
  }
}

// The parasite camp's live member other than `current` (a new stage, OnCreepUpgrade moved the membership to it).
std::uint32_t SehParasiteMember(std::uint32_t recordId, std::uint32_t current) {
  __try {
    if (!g_impl) return 0;
    std::uint32_t* b = Field<std::uint32_t*>(g_impl, aidir::kCampMembersBegin);
    std::uint32_t* e = Field<std::uint32_t*>(g_impl, aidir::kCampMembersEnd);
    std::uint32_t found = 0;
    for (std::uint32_t* p = b; p < e; p += 2)
      if (p[1] == recordId && p[0] != current && GetObjectById(p[0])) found = p[0];
    return found;
  } __except (SehNote(GetExceptionInformation(), "ParasiteMember")) {
    return 0;
  }
}

std::string CompositionText(const SpawnDef& d) {
  std::string s;
  for (int k = 0; k < 4; ++k) {
    const int n = flow::CompositionOf(d.counts).counts[k];
    if (!n) continue;
    char b[96];
    std::snprintf(b, sizeof b, "%s%d x %s", s.empty() ? "" : ", ", n, SehAssetName(d.creeps[k]));
    s += b;
  }
  return s.empty() ? "nothing" : s;
}

// D1k: one spawner's wave in progress. Its members spawn one by one, `interval` s apart, in the composition's order
// (minor, medium, major, uber: melee before ranged on v5, the Strider first in an override); interval 0 spawns them
// all in the first tick in D1c-live's formation.
struct WaveGroup {
  int spawner = -1;
  int wave = 0;
  double start = 0.0;            // match s of the first member
  double interval = 0.0;
  SpawnDef def;                  // the composition at the wave's time (the override's if one was set)
  void* overrideNoun = nullptr;
  std::uint32_t splineId = 0;
  float dirX = 1.f, dirY = 0.f;  // spawner -> knot 0 (D1c-live's formation only)
  std::vector<int> kinds;        // per member: its composition slot k
  int next = 0;                  // members handled so far
  int level = -1;
  double firstAt = -1.0, lastAt = -1.0;
  std::vector<std::uint32_t> ids;
};
std::vector<WaveGroup> g_groups;

// D1k: a traced wave's minions (the first shapeWaves waves of every spawner), for the "wave shape" lines.
struct ShapeTrace {
  std::string spawner;
  int wave = 0;
  double start = 0.0;
  double last = -1.0;
  std::vector<std::uint32_t> ids;
};
std::vector<ShapeTrace> g_shapes;
bool g_repulsionLogged;

// D1k: the repulsion fields of a new minion's locomotion and its NavPower mover (read once, for the log).
bool SehRepulsion(std::uint32_t id, unsigned* disable, std::uint32_t* blockage, std::uint32_t* identity,
                  std::uint32_t* obstacle, std::uint32_t* autoObstacle, void** mover) {
  __try {
    void* o = GetObjectById(id);
    char* l = o ? Field<char*>(o, loco::kOfObject) : nullptr;
    if (!l) return false;
    *disable = Field<unsigned char>(l, loco::kDisableRepulsion);
    *blockage = Field<std::uint32_t>(l, loco::kRepulsorBlockage);
    *identity = Field<std::uint32_t>(l, loco::kRepulsorIdentity);
    *obstacle = Field<std::uint32_t>(l, loco::kObstacleBlockage);
    *autoObstacle = Field<std::uint32_t>(l, loco::kAutoObstacle);
    *mover = Field<void*>(o, obj::kMover);
    return true;
  } __except (SehNote(GetExceptionInformation(), "repulsion fields")) {
    return false;
  }
}

bool SehCushions(float* out5) {
  __try {
    std::memcpy(out5, reinterpret_cast<const void*>(kLabsTuningCushions), 5 * sizeof(float));
    return true;
  } __except (SehNote(GetExceptionInformation(), "LabsTuning cushions")) {
    return false;
  }
}

// Starts one spawner's wave (its override's composition if one is set); the members follow in TickGroups.
void StartWave(int spawnerIndex, int wave, double t) {
  Spawner& sp = g_spawners[spawnerIndex];
  WaveGroup g;
  g.spawner = spawnerIndex;
  g.wave = wave;
  g.start = t;
  g.interval = g_opt.spawnIntervalS > 0.0 ? g_opt.spawnIntervalS : 0.0;
  g.def = sp.def;
  auto ov = g_overrides.find(sp.noun);
  if (ov != g_overrides.end()) {
    SpawnDef od;
    if (SehNounSpawnDef(ov->second, &od) && od.ok) {
      std::memcpy(g.def.creeps, od.creeps, sizeof g.def.creeps);
      std::memcpy(g.def.counts, od.counts, sizeof g.def.counts);
      g.overrideNoun = ov->second;
    }
  }
  const Lane* lane = sp.lane >= 0 ? &g_lanes[sp.lane] : nullptr;
  if (lane && !lane->knots.empty()) {
    const float dx = lane->knots[0].x - sp.pos[0], dy = lane->knots[0].y - sp.pos[1];
    const float len = std::sqrt(dx * dx + dy * dy);
    if (len > 0.01f) g.dirX = dx / len, g.dirY = dy / len;
  }
  g.splineId = (lane && lane->inTable && g_opt.laneTable) ? lane->splineId : 0;
  const flow::Composition comp = flow::CompositionOf(g.def.counts);
  for (int k = 0; k < 4; ++k)
    for (int c = 0; c < comp.counts[k]; ++c) g.kinds.push_back(k);
  ++sp.waves;
  g_groups.push_back(g);
}

// Spawns member g.next of a wave (at the spawner; with interval 0 at its D1c-live formation place).
void SpawnMember(WaveGroup& g, double t) {
  Spawner& sp = g_spawners[g.spawner];
  const int i = g.next++;
  const int k = g.kinds[i];
  if (!g.def.creeps[k]) return;
  float dx = 0.f, dy = 0.f;
  if (g.interval <= 0.0) flow::FormationOffset(i, g.dirX, g.dirY, g_opt.formationSpacing, &dx, &dy);
  const float pos[3] = {sp.pos[0] + dx, sp.pos[1] + dy, sp.pos[2]};
  Created cr;
  if (!SehCreate(g.def.creeps[k], g.def.team, pos, sp.rot, g.splineId, &cr)) {
    ++g_st.failed;
    return;
  }
  ++g_st.minions;
  ++sp.spawned;
  if (g.splineId) ++g_st.laneAssigned;
  if (g.level < 0) g.level = cr.level;
  if (g.firstAt < 0.0) g.firstAt = t;
  g.lastAt = t;
  g.ids.push_back(cr.id);
  if (g.wave == 0 && i == 0) g_traced.push_back(cr.id);
  if (!g_repulsionLogged) {
    g_repulsionLogged = true;
    unsigned dis = 0;
    std::uint32_t blk = 0, idn = 0, obs = 0, ao = 0;
    void* mover = nullptr;
    void *bfxMover = nullptr, *repulsor = nullptr;
    unsigned on = 0;
    crowd::MoverRepulsor(cr.id, &bfxMover, &repulsor, &on);
    char cs[96];
    crowd::FormatStats(cs, sizeof cs);
    if (SehRepulsion(cr.id, &dis, &blk, &idn, &obs, &ao, &mover))
      Log("director: repulsion of the first wave minion %08X (%s): mDisableRepulsion %u, repulsor blockage flags %08X, "
          "identity flags %08X, obstacle blockage %08X, auto obstacle %u, NavPower mover %p (repulsor %p, on %u); %s",
          cr.id, SehAssetName(g.def.creeps[k]), dis, blk, idn, obs, ao, mover, repulsor, on, cs);
  }
}

// Spawns every member that is due; logs a finished wave and hands it to the shape trace.
void TickGroups(double t) {
  for (size_t gi = 0; gi < g_groups.size();) {
    WaveGroup& g = g_groups[gi];
    const int total = static_cast<int>(g.kinds.size());
    const int due = flow::StaggeredDueCount(g.start, g.interval, total, t);
    while (g.next < due) SpawnMember(g, t);
    if (g.next < total) {
      ++gi;
      continue;
    }
    Spawner& sp = g_spawners[g.spawner];
    const Lane* lane = sp.lane >= 0 ? &g_lanes[sp.lane] : nullptr;
    std::string ids;
    for (std::uint32_t id : g.ids) {
      char b[16];
      std::snprintf(b, sizeof b, "%s%08X", ids.empty() ? "" : " ", id);
      ids += b;
    }
    if (LogBudget())
      Log("director: wave %d at match %.2f s: %s (team %d) %s%s%s -> lane %s (spline id %08X), NPC level %d, spawned "
          "%.2f-%.2f s (%s): %s",
          g.wave + 1, g.start, sp.name.c_str(), g.def.team, CompositionText(g.def).c_str(),
          g.overrideNoun ? " (override " : "",
          g.overrideNoun ? (std::string(SehAssetName(g.overrideNoun)) + ")").c_str() : "",
          lane ? lane->name.c_str() : "none", g.splineId, g.level, g.firstAt, g.lastAt,
          g.interval > 0.0 ? (std::to_string(static_cast<int>(g.interval * 1000.0 + 0.5)) + " ms apart").c_str()
                           : "at once, formation",
          ids.c_str());
    if (g_opt.shapeTraceS > 0.0 && g.wave < g_opt.shapeWaves && g.ids.size() > 1) {
      ShapeTrace s;
      s.spawner = sp.name;
      s.wave = g.wave;
      s.start = g.start;
      s.ids = g.ids;
      g_shapes.push_back(s);
    }
    g_groups.erase(g_groups.begin() + static_cast<std::ptrdiff_t>(gi));
  }
}

// D1k: the shape of the traced waves' living minions (length, width, closest pair), once every shapeTraceS s.
void TraceShapes(double t) {
  for (size_t si = 0; si < g_shapes.size();) {
    ShapeTrace& s = g_shapes[si];
    const double age = t - s.start;
    if (age > g_opt.shapeForS) {
      g_shapes.erase(g_shapes.begin() + static_cast<std::ptrdiff_t>(si));
      continue;
    }
    ++si;
    if (s.last >= 0.0 && t - s.last < g_opt.shapeTraceS) continue;
    s.last = t;
    std::vector<flow::Point> pts;
    std::string where;
    int dead = 0;
    for (std::uint32_t id : s.ids) {
      float hp = 0.f, pos[3] = {};
      std::uint32_t idx = 0;
      const int st = SehMemberState(id, &hp, pos, &idx);
      if (st != 1) {
        ++dead;
        continue;
      }
      pts.push_back({pos[0], pos[1], pos[2]});
      char b[40];
      std::snprintf(b, sizeof b, " (%.1f, %.1f)", pos[0], pos[1]);
      where += b;
    }
    const flow::GroupShape sh = flow::ShapeOf(pts);
    Log("director: wave shape: wave %d %s at match %.2f s (+%.1f s): %d of %zu alive, length %.1f, width %.1f, closest "
        "%.2f, mean nearest %.2f; in spawn order:%s",
        s.wave + 1, s.spawner.c_str(), t, age, sh.n, s.ids.size(), sh.length, sh.width, sh.closest, sh.meanNearest,
        where.c_str());
    if (sh.n == 0) s.last = 1e30;  // all dead: no more lines (erased at the end of its window)
  }
}

void SpawnCamp(Camp& c, double t) {
  const flow::Composition comp = flow::CompositionOf(c.def.counts);
  int pruned = SehPruneMembers();
  g_st.membersPruned += static_cast<unsigned>(pruned);
  c.members.clear();
  // D14: which child marker each creature takes (gms::flow::AssignCampSpots: the major on a buff camp's scale-2.0
  // marker, the tutorial's buff ping; creation order otherwise, and with [host] camp_major_spot=0)
  std::vector<int> tiers;
  for (int k = 0; k < 4; ++k)
    for (int n = 0; n < comp.counts[k]; ++n) tiers.push_back(k);
  std::vector<float> scales;
  for (const MarkerPos& sp : c.spots) scales.push_back(g_opt.campMajorSpot ? sp.scale : 1.f);
  const std::vector<int> spot = flow::AssignCampSpots(tiers, scales);
  std::string ids;
  int i = 0;
  for (int k = 0; k < 4; ++k)
    for (int n = 0; n < comp.counts[k]; ++n, ++i) {
      if (!c.def.creeps[k]) continue;
      const float* pos = c.pos;
      const float* rot = c.rot;
      if (!c.parasite && i < static_cast<int>(spot.size()) && spot[i] >= 0) {
        pos = c.spots[spot[i]].pos;
        rot = c.spots[spot[i]].rot;
      }
      Created cr;
      if (!SehCreate(c.def.creeps[k], 0, pos, rot, 0, &cr)) {
        ++g_st.failed;
        continue;
      }
      ++g_st.creatures;
      c.members.push_back(cr.id);
      const bool joined = c.record && SehJoinCamp(cr.id, c.record, c.marker);
      if (joined) ++g_st.members;
      char b[48];
      std::snprintf(b, sizeof b, "%s%08X%s", ids.empty() ? "" : " ", cr.id, joined ? "" : "(no camp)");
      ids += b;
    }
  c.timer.OnSpawned();
  ++g_st.campSpawns;
  if (c.parasite && !c.members.empty()) {
    g_parasite = c.members[0];
    SehSetParasiteId(g_parasite);
  }
  if (LogBudget())
    Log("director: camp %s (marker %u%s) spawn %d at match %.2f s: %s: %s; camp record %p (id %u)%s", c.name.c_str(),
        c.marker, c.markerTiming ? ", marker timing" : "", c.timer.Spawns(), t, CompositionText(c.def).c_str(),
        ids.c_str(), c.record, c.recordId,
        c.parasite ? (" -> director +0x18 (GetParasiteID) = " + std::to_string(g_parasite)).c_str() : "");
}

// The test aid wave_spawners: true if `name` contains one of the filter's comma-separated parts (or no filter).
bool Selected(const std::string& name) {
  if (g_opt.spawnerFilter.empty()) return true;
  size_t pos = 0;
  while (pos <= g_opt.spawnerFilter.size()) {
    size_t comma = g_opt.spawnerFilter.find(',', pos);
    if (comma == std::string::npos) comma = g_opt.spawnerFilter.size();
    const std::string part = g_opt.spawnerFilter.substr(pos, comma - pos);
    if (!part.empty() && name.find(part) != std::string::npos) return true;
    pos = comma + 1;
  }
  return false;
}

void CheckCamps(double t) {
  for (Camp& c : g_camps) {
    if (!c.timer.Alive()) continue;
    if (c.parasite) continue;  // the parasite resurrects itself (ability_Parasite_Death); its camp never respawns
    bool any = false;
    for (std::uint32_t id : c.members) {
      float hp = 0.f;
      if (SehMemberState(id, &hp, nullptr, nullptr) == 1) {
        any = true;
        break;
      }
    }
    if (any) continue;
    c.timer.OnCleared(t);
    ++g_st.campClears;
    if (LogBudget())
      Log("director: camp %s (marker %u) cleared at match %.2f s; respawn at %.2f s", c.name.c_str(), c.marker, t,
          c.timer.DueAt());
  }
}

void FollowParasite(double t) {
  if (g_camp_parasite < 0) return;
  Camp& c = g_camps[g_camp_parasite];
  if (!g_parasite) return;
  float hp = 0.f;
  const std::uint32_t next = SehParasiteMember(c.recordId, g_parasite);
  if (next && SehMemberState(next, &hp, nullptr, nullptr) >= 0) {
    if (LogBudget())
      Log("director: parasite stage changed at match %.2f s: %08X -> %08X (camp member); director +0x18 updated", t,
          g_parasite, next);
    g_parasite = next;
    SehSetParasiteId(next);
    ++g_st.parasiteStages;
  }
}

void Trace(double t) {
  if (g_opt.traceSeconds <= 0 || g_traced.empty()) return;
  if (g_lastTrace >= 0 && t - g_lastTrace < g_opt.traceSeconds) return;
  g_lastTrace = t;
  std::string line;
  int alive = 0;
  for (std::uint32_t id : g_traced) {
    float hp = 0.f, pos[3] = {};
    std::uint32_t idx = 0, spline = 0;
    const int st = SehMemberState(id, &hp, pos, &idx, &spline);
    char b[112];
    if (st < 0) std::snprintf(b, sizeof b, " %08X gone;", id);
    else {
      std::snprintf(b, sizeof b, " %08X (%.1f, %.1f) knot %d%s hp %.0f%s;", id, pos[0], pos[1], static_cast<int>(idx),
                    spline ? "" : " (lane done: spline id 0)", hp, st == 0 ? " dead" : "");
      ++alive;
    }
    line += b;
  }
  Log("director: lane trace at match %.1f s:%s", t, line.c_str());
  if (!alive) g_traced.clear();
}

// ---- Lua: nDirector.OverrideMinionSpawnerAsset / ResetMinionSpawnerAsset -----------------------------------------

void* SehArgUserdata(void* L, int idx) {
  __try {
    return LuaToUserdata(L, idx);
  } __except (SehNote(GetExceptionInformation(), "lua_touserdata")) {
    return nullptr;
  }
}

int __cdecl LuaOverrideSpawner(void* L) {
  if (!g_sim || GetCurrentSim() != g_sim) return 0;  // the client's stub was a no-op
  void* spawner = SehArgUserdata(L, 1);
  void* over = SehArgUserdata(L, 2);
  if (!spawner || !over) return 0;
  g_overrides[spawner] = over;
  ++g_st.overrides;
  Log("director: nDirector.OverrideMinionSpawnerAsset(%s, %s)", SehAssetName(spawner), SehAssetName(over));
  return 0;
}

int __cdecl LuaResetSpawner(void* L) {
  if (!g_sim || GetCurrentSim() != g_sim) return 0;
  void* spawner = SehArgUserdata(L, 1);
  if (!spawner) return 0;
  const bool had = g_overrides.erase(spawner) > 0;
  ++g_st.resets;
  Log("director: nDirector.ResetMinionSpawnerAsset(%s)%s", SehAssetName(spawner), had ? "" : " (no override set)");
  return 0;
}

const LuaReg kDirectorRegs[] = {
    {"OverrideMinionSpawnerAsset", &LuaOverrideSpawner},
    {"ResetMinionSpawnerAsset", &LuaResetSpawner},
};

bool SehRegister(void* L, const char* ns, const LuaReg* regs, int n) {
  __try {
    RegisterNamespace(L, ns, regs, n);
    return true;
  } __except (SehNote(GetExceptionInformation(), "RegisterNamespace")) {
    return false;
  }
}

}  // namespace

void Configure(const Options& o) { g_opt = o; }

void RegisterLua(void* L, void* sim) {
  g_sim = sim;
  if (!CodeMatches(0x00873170, kRegisterNamespaceBytes, sizeof kRegisterNamespaceBytes)) {
    Log("director: GearsLua_RegisterNamespace differs; nDirector overrides not registered");
    return;
  }
  const bool ok = SehRegister(L, "nDirector", kDirectorRegs, 2);
  Log("director: nDirector.OverrideMinionSpawnerAsset / ResetMinionSpawnerAsset registered over the stubs: %s",
      ok ? "ok" : "FAILED");
}

bool OnLevelStarted(void* sim) {
  g_sim = sim;
  if (!CodeMatches(0x008E1EC0, kCreateFromNounBytes, sizeof kCreateFromNounBytes) ||
      !CodeMatches(0x00674430, kDegreesToQuaternionBytes, sizeof kDegreesToQuaternionBytes) ||
      !CodeMatches(0x008AF240, kFindCampBytes, sizeof kFindCampBytes) ||
      !CodeMatches(0x008AF780, kAddCampMemberBytes, sizeof kAddCampMemberBytes) ||
      !CodeMatches(0x008F05C0, kFindSplinePathBytes, sizeof kFindSplinePathBytes)) {
    Log("director: a function's prologue differs (not build 854?); the director stays off");
    return false;
  }
  if (!SehDirector(sim)) Log("director: no AI director implementation (sim +0x560 -> +0x1C); no camp membership");
  void* level = Field<void*>(sim, sim::kLevel);
  if (!level) {
    Log("director: the server simulator has no level");
    return false;
  }
  // every object that came from a marker (spline heads, titan nodes, wells, towers)
  {
    std::vector<std::uint32_t> mk(4096), ids(4096);
    const int n = SehScanMarkerObjects(sim, mk.data(), ids.data(), 4096);
    for (int i = 0; i < n; ++i) g_markerObjects[mk[i]] = ids[i];
    Log("director: %d objects from markers before the first tick", n);
  }
  std::vector<MarkerPos> pos(16384);
  std::vector<RawMarker> rel(1024);
  int nPos = 0, nRel = 0, sets = 0;
  if (!SehReadMarkers(level, pos.data(), static_cast<int>(pos.size()), &nPos, rel.data(), static_cast<int>(rel.size()),
                      &nRel, &sets)) {
    Log("director: the level's markers could not be read");
    return false;
  }
  pos.resize(nPos);
  rel.resize(nRel);
  if (SehReadTuning(level, &g_tuning))
    Log("director: LevelGameplayTuning: minionSpawnDelay1 %.1f, minionSpawnInterval1 %.1f, nubRespawnTime %.1f, "
        "goldMineLockedDurationFirstInSeconds %.1f",
        g_tuning.waveDelay, g_tuning.waveInterval, g_tuning.nubRespawn, g_tuning.wellLockFirst);
  else
    Log("director: LevelGameplayTuning not readable; SM_Map_v5's values assumed");
  {
    float c[5] = {};
    if (SehCushions(c))
      Log("director: NavPower repulsion (LabsTuning, D1k): innerCushion %.2f, outerCushion %.2f, initialAcc %.1f, "
          "outerCushionAcc %.1f, innerCushionAcc %.1f (exe defaults 1, 2, 30, 50, 100; ServerData's file 0.5, 1, 10, 30, "
          "200); minions spawn %s",
          c[0], c[1], c[2], c[3], c[4],
          g_opt.spawnIntervalS > 0.0
              ? ("one by one, " + std::to_string(static_cast<int>(g_opt.spawnIntervalS * 1000.0 + 0.5)) + " ms apart")
                    .c_str()
              : "all at once (D1c-live's formation)");
  }
  std::unordered_map<std::uint32_t, MarkerPos> byId;
  for (const MarkerPos& p : pos) byId[p.id] = p;
  Log("director: level %s: %d markersets, %d markers, %d relevant (splines, spawn points, titan nodes)",
      SehAssetName(level), sets, nPos, nRel);

  // lanes: spline nodes grouped by name, sorted by knot
  std::map<std::string, std::vector<const RawMarker*>> splines;
  for (const RawMarker& r : rel)
    if (r.hasSpline) splines[r.splineName].push_back(&r);
  for (auto& kv : splines) {
    std::sort(kv.second.begin(), kv.second.end(), [](const RawMarker* a, const RawMarker* b) { return a->knot < b->knot; });
    Lane l;
    l.name = kv.first;
    const RawMarker* head = kv.second.front();
    l.headMarker = head->id;
    l.laneId = head->lane;
    l.team = head->team;
    for (const RawMarker* r : kv.second) l.knots.push_back({r->pos[0], r->pos[1], r->pos[2]});
    auto obj = g_markerObjects.find(head->id);
    l.splineId = obj != g_markerObjects.end() ? obj->second : 0;
    Log("director: lane %s: %zu knots, knot 0 marker %u (object %08X) at (%.1f, %.1f), nLaneID %d, team %d, last knot "
        "(%.1f, %.1f)",
        l.name.c_str(), l.knots.size(), l.headMarker, l.splineId, l.knots.front().x, l.knots.front().y, l.laneId, l.team,
        l.knots.back().x, l.knots.back().y);
    g_lanes.push_back(l);
  }

  // spawners, camps, titan nodes
  for (const RawMarker& r : rel) {
    if (r.titanNodeDef) {
      void* spawned = nullptr;
      bool isBase = false, isGold = false;
      if (!SehTitanNode(r.titanNodeDef, &spawned, &isBase, &isGold)) continue;
      if (isBase && spawned) {
        TitanSpawn g;
        g.node = r.nounName;
        g.noun = spawned;
        g.nounName = SehAssetName(spawned);
        g.team = r.nounTeam;
        auto sp = byId.find(r.target);
        const MarkerPos& at = sp != byId.end() ? sp->second : MarkerPos{r.id, {r.pos[0], r.pos[1], r.pos[2]},
                                                                        {r.rot[0], r.rot[1], r.rot[2]}, 1.f};
        std::memcpy(g.pos, at.pos, sizeof g.pos);
        std::memcpy(g.rot, at.rot, sizeof g.rot);
        Log("director: titan node %s (team %d): Guardian %s at its spawn point %u (%.1f, %.1f) rot %.1f", g.node.c_str(),
            g.team, g.nounName.c_str(), r.target, g.pos[0], g.pos[1], g.rot[2]);
        g_guardians.push_back(g);
      }
      if (isGold) {
        Well w;
        w.marker = r.id;
        auto obj = g_markerObjects.find(r.id);
        w.object = obj != g_markerObjects.end() ? obj->second : 0;
        w.team = r.nounTeam;
        std::memcpy(w.pos, r.pos, sizeof w.pos);
        w.titanNodeDef = r.titanNodeDef;
        for (int k = 0; k < r.numChildren; ++k) {  // D9: the worker spots
          auto it = byId.find(r.children[k]);
          if (it == byId.end()) continue;
          WellSpot s;
          std::memcpy(s.pos, it->second.pos, sizeof s.pos);
          std::memcpy(s.rot, it->second.rot, sizeof s.rot);
          w.spots.push_back(s);
        }
        g_wells.push_back(w);
      }
    }
    if (!r.nounSpawnDef && !r.markerSpawnDef) continue;
    SpawnDef nd, md;
    SehReadSpawnDef(r.nounSpawnDef, &nd);
    SehReadSpawnDef(r.markerSpawnDef, &md);
    // composition: the noun's; timing: the marker's where it has one (MATCH_FLOW §2.2 [assumed])
    SpawnDef def = nd.ok ? nd : md;
    const bool markerTiming = md.ok;
    if (md.ok) {
      def.delay = md.delay;
      def.respawn = md.respawn;
      if (!nd.ok) def.team = md.team;
    }
    if (!def.ok) continue;
    if (def.type == 1) {
      Spawner sp;
      sp.marker = r.id;
      sp.name = r.nounName;
      sp.noun = r.noun;
      sp.def = def;
      sp.markerTiming = markerTiming;
      std::memcpy(sp.pos, r.pos, sizeof sp.pos);
      std::memcpy(sp.rot, r.rot, sizeof sp.rot);
      sp.target = r.target;
      for (int i = 0; i < static_cast<int>(g_lanes.size()); ++i)
        if (g_lanes[i].headMarker == r.target) sp.lane = i;
      Log("director: minion spawner %s (marker %u) team %d at (%.1f, %.1f): %s, delay %.0f -> lane %s", sp.name.c_str(),
          sp.marker, def.team, sp.pos[0], sp.pos[1], CompositionText(def).c_str(), def.delay,
          sp.lane >= 0 ? g_lanes[sp.lane].name.c_str() : "NONE");
      g_spawners.push_back(sp);
    } else if (def.type == 2 || def.type == 3) {
      Camp c;
      c.marker = r.id;
      c.name = r.nounName;
      c.def = def;
      c.markerTiming = markerTiming;
      c.parasite = def.type == 3;
      std::memcpy(c.pos, r.pos, sizeof c.pos);
      std::memcpy(c.rot, r.rot, sizeof c.rot);
      for (int k = 0; k < r.numChildren; ++k) {
        auto it = byId.find(r.children[k]);
        if (it != byId.end()) c.spots.push_back(it->second);
      }
      c.record = SehFindCamp(c.marker, &c.recordId);
      c.timer.Init(def.delay, def.respawn);
      if (c.parasite) g_camp_parasite = static_cast<int>(g_camps.size());
      if (LogBudget())
        Log("director: %s %s (marker %u) at (%.1f, %.1f): %s, %zu creature markers, first %.0f s, respawn %.0f s%s, camp "
            "record %p",
            c.parasite ? "parasite camp" : "camp", c.name.c_str(), c.marker, c.pos[0], c.pos[1],
            CompositionText(def).c_str(), c.spots.size(), def.delay, def.respawn,
            markerTiming ? " (marker)" : " (noun)", c.record);
      g_camps.push_back(c);
    } else if (def.type == 4) {
      Log("director: super-minion spawner %s (marker %u) not used (LevelGameplayTuning.striderSpawnEnabled is false on "
          "SM_Map_v5)",
          r.nounName, r.id);
    }
  }
  if (g_opt.laneTable) FillLaneTable(sim);
  g_levelOk = true;
  Log("director: level read: %zu lanes, %zu minion spawners, %zu camps (parasite %s), %zu Guardians, %zu wells",
      g_lanes.size(), g_spawners.size(), g_camps.size(), g_camp_parasite >= 0 ? "yes" : "no", g_guardians.size(),
      g_wells.size());
  return true;
}

void OnMatchStarted() {
  g_started = true;
  g_waves.delay = g_tuning.waveDelay;
  g_waves.interval = g_tuning.waveInterval;
  if (g_opt.waveDelay >= 0) g_waves.delay = g_opt.waveDelay;
  if (g_opt.waveInterval >= 0) g_waves.interval = g_opt.waveInterval;
  g_waves.limit = g_opt.waveLimit;
  Log("director: match started: waves at %.1f + %.1f k s (limit %d), %zu camps", g_waves.delay, g_waves.interval,
      g_waves.limit, g_camps.size());
}

void Tick(void* sim, double t) {
  g_sim = sim;
  if (!g_levelOk || !g_started || g_stopped) return;
  if (g_opt.waves)
    while (g_waves.Due(t)) {
      const int k = g_waves.next;
      g_waves.Fired();
      ++g_st.waves;
      for (int i = 0; i < static_cast<int>(g_spawners.size()); ++i)
        if (Selected(g_spawners[i].name)) StartWave(i, k, t);
    }
  TickGroups(t);
  TraceShapes(t);
  for (Camp& c : g_camps) {
    if (c.parasite ? !g_opt.parasite : !g_opt.camps) continue;
    if (c.timer.Due(t)) SpawnCamp(c, t);
  }
  if (std::floor(t) != g_lastSecond) {  // once a second: cleared camps, the parasite's stage
    g_lastSecond = std::floor(t);
    CheckCamps(t);
    FollowParasite(t);
  }
  Trace(t);
}

void Stop() {
  if (!g_stopped) {
    int pending = 0;
    for (const WaveGroup& g : g_groups) pending += static_cast<int>(g.kinds.size()) - g.next;
    Log("director: stopped (game over): no more waves or camps (%d minions of a running wave not spawned)", pending);
  }
  g_groups.clear();
  g_stopped = true;
}

double FirstWaveTime() {
  if (!g_opt.waves) return -1.0;
  return g_opt.waveDelay >= 0 ? g_opt.waveDelay : g_tuning.waveDelay;
}
int WavesFired() { return g_waves.next; }

const std::vector<TitanSpawn>& Guardians() { return g_guardians; }
const Tuning& LevelTuning() { return g_tuning; }
const std::vector<Well>& Wells() { return g_wells; }

int NearestLane(float x, float y, float* distance) {
  int best = 0;
  float bd = 3.0e38f;
  for (const Lane& l : g_lanes) {
    const float d = flow::DistanceToPolyline(x, y, l.knots);
    if (d < bd) {
      bd = d;
      best = l.laneId;
    }
  }
  if (distance) *distance = bd;
  return best;
}

bool LaneKnots(int laneId, int team, std::vector<float>* xyz) {
  xyz->clear();
  for (const Lane& l : g_lanes) {
    if (l.laneId != laneId || l.team != team || l.knots.size() < 2) continue;
    for (const flow::Point& p : l.knots) {
      xyz->push_back(p.x);
      xyz->push_back(p.y);
      xyz->push_back(p.z);
    }
    return true;
  }
  return false;
}

bool ClosestCampPosition(float x, float y, float* out) {
  int best = -1;
  float bd = 3.0e38f;
  for (int i = 0; i < static_cast<int>(g_camps.size()); ++i) {
    const Camp& c = g_camps[i];
    if (c.parasite || (!c.timer.Alive() && c.timer.Spawns() > 0)) continue;  // cleared, waiting for its respawn
    bool any = false;
    for (std::uint32_t id : c.members) {
      float hp = 0.f;
      if (SehMemberState(id, &hp, nullptr, nullptr) == 1) {
        any = true;
        break;
      }
    }
    // cleared and waiting for its respawn: no. Not spawned yet (no members at all): yes, so a Hunter walks to the nearest
    // camp before the first spawn (90 s) and is there when it comes
    if (!any && !c.members.empty()) continue;
    const float dx = c.pos[0] - x, dy = c.pos[1] - y;
    const float d = dx * dx + dy * dy;
    if (d < bd) {
      bd = d;
      best = i;
    }
  }
  if (best < 0) return false;
  out[0] = g_camps[best].pos[0];
  out[1] = g_camps[best].pos[1];
  out[2] = g_camps[best].pos[2];
  return true;
}

std::uint32_t ObjectOfMarker(std::uint32_t marker) {
  auto it = g_markerObjects.find(marker);
  return it != g_markerObjects.end() ? it->second : 0;
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size,
                "waves %u (next at %.0f s), minions %u (lane assigned %u, spawning %zu waves), creatures %u in %u camp "
                "spawns (cleared %u, members %u, pruned %u), parasite %08X (stages %u), overrides %u/%u, failed %u, lane "
                "table %u",
                g_st.waves, g_waves.TimeOf(g_waves.next), g_st.minions, g_st.laneAssigned, g_groups.size(), g_st.creatures,
                g_st.campSpawns, g_st.campClears, g_st.members, g_st.membersPruned, g_parasite, g_st.parasiteStages,
                g_st.overrides, g_st.resets, g_st.failed, g_st.laneEntries);
}

}  // namespace director
