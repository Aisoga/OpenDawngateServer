// The match lifecycle on the server simulator (D1c-live): see match.h.
#include "match.h"

#include <windows.h>

#include <cmath>
#include <cstdio>
#include <cstring>
#include <map>
#include <string>
#include <vector>

#include "agents.h"
#include "core/matchflow/flow.h"
#include "core/matchflow/guardcore.h"
#include "core/matchflow/mapbase.h"
#include "crowd.h"
#include "death.h"
#include "game.h"
#include "host/control.h"
#include "log.h"
#include "matchrec.h"
#include "matchstats.h"
#include "replicate.h"
#include "shop.h"
#include "wells.h"

using namespace game;
namespace flow = gms::flow;

namespace match {
namespace {

Options g_opt;
bool g_configured;
void* g_sim;
bool g_levelDone;
bool g_startRequested;
bool g_started;
bool g_ended;
bool g_endTaken;
End g_end;
double g_levelAt = -1;          // PreciseMs-free: the game clock (ms) at the level load
unsigned long long g_levelClockMs;
double g_t;                     // match seconds, updated every tick
double g_lastSecond = -1.0;
bool g_warned, g_announcedWave;
unsigned g_logLines;

struct Guardian {
  std::uint32_t id = 0;
  int team = 0;
  std::string noun;
  std::uint32_t shield = 0;  // D14: its modifier_Tower_Immunity instance while a core of its team lives
  int livingCores = -1;      // D14: at the last shield step (logged on a change)
  int coreMask = -1;         // GUARDCORE: bit i = Core A+i of its team alive (HP > 0); -1 before the first look
  unsigned presentMask = flow::kAllCores;  // MAPBASE: the Cores its team has on the level (MM_Map_v3: A, B, C)
};
std::vector<Guardian> g_guardians;

// MAPBASE: the base defence (the Locus, gms::flow::IsLocusNoun), watched for the stats line only.
struct Locus {
  std::uint32_t id = 0;
  std::string noun;
  int team = 0;
};
std::vector<Locus> g_loci;

struct Tower {
  std::uint32_t id = 0;
  std::string noun;
  int team = 0, teamIndex = -1, laneIndex = -1, tier = -1;
  bool destroyed = false;
};
std::vector<Tower> g_towers;

// D5: the nubs (base structures with the Guardians; AddBaseStructuresToAggroList)
struct Nub {
  std::uint32_t id = 0;
  std::string noun;
  int team = 0;
  int core = -1;  // GUARDCORE: 0..4 for Core A..E (gms::flow::NubCoreIndex), -1 if the noun has no letter
};
std::vector<Nub> g_nubs;

struct Destroyed {
  std::uint32_t structure, killer;
  double t;
};
std::vector<Destroyed> g_destroyed;  // OnTitanTowerDestroyed calls, handled in the next Tick (outside Lua)

struct Stats {
  // GetGuardianOnTeam, OnTitanTowerDestroyed, SetEnergyLineState, GetNubRespawnTuning, AddBaseStructuresToAggroList
  unsigned luaCalls[5] = {};
  unsigned baseMarked = 0, baseEmpty = 0;
  unsigned levelChecks = 0, levelUps = 0, exceptions = 0, towersDestroyed = 0, handlerCalls = 0, announcer = 0;
  unsigned shieldsOn = 0, shieldsOff = 0, shieldsRefused = 0;  // D14: the Guardian's shield
  unsigned coresLost = 0, coresBack = 0;                         // GUARDCORE: Core deaths and resurrects seen
} g_st;

bool LogBudget() {
  ++g_logLines;
  return g_logLines <= static_cast<unsigned>(g_opt.logLimit) || g_logLines % 100 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  ++g_st.exceptions;
  Log("EXCEPTION in match (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

bool CodeMatches(std::uintptr_t va, const unsigned char* bytes, std::size_t n) {
  __try {
    return std::memcmp(reinterpret_cast<const void*>(va), bytes, n) == 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

char* Ui(void* sim) { return static_cast<char*>(sim) + matchsim::kUIData; }

// ---- engine calls ------------------------------------------------------------------------------------------------

std::uint32_t SehSpawn(void* noun, int team, const float* pos, const float* rotDegrees, float* hp, int* npcType) {
  __try {
    float q[4] = {0.f, 0.f, 0.f, 1.f};
    DegreesToQuaternion(q, rotDegrees);
    void* o = CreateFromNoun(noun, team, 0, nullptr, pos, q, nullptr);
    if (!o) return 0;
    void* c = Field<void*>(o, combatant::kOfObject);
    *hp = c ? Field<float>(c, combatant::kHitPoints) : -1.f;
    void* npc = Field<void*>(o, npc::kOfObject);
    char* nd = npc ? Field<char*>(npc, noun::kData) : nullptr;
    *npcType = nd ? Field<int>(nd, npc::kType) : -1;
    return Field<std::uint32_t>(o, gobj::kId);
  } __except (SehNote(GetExceptionInformation(), "spawn Guardian")) {
    return 0;
  }
}

// 1 alive, 0 dead (HP <= 0), -1 gone.
int SehHp(std::uint32_t id, float* hp) {
  __try {
    void* o = GetObjectById(id);
    if (!o) return -1;
    void* c = Field<void*>(o, combatant::kOfObject);
    *hp = c ? Field<float>(c, combatant::kHitPoints) : -1.f;
    return (c && *hp <= 0.f) ? 0 : 1;
  } __except (SehNote(GetExceptionInformation(), "Hp")) {
    return -1;
  }
}

// ---- D14: the Guardian's shield (MATCH_FLOW.md *D14*, "Structures") ------------------------------------------------
// modifier_Tower_Immunity (GUID = FNV-1 of its lower-case name): Unique, ends only OnAgentDestroyed; Activate adds
// Immune 1 and Unattackable 1, its Tick puts titan_invulnerability_effect on the Guardian (nBattlegrounds.
// GetGuardianOnTeam), Deactivate plays titan_invulnerability_deactivate_effect. No script and no AssetData applies it.
constexpr std::uint32_t kTowerImmunityGuid = 0xB2A91715;

// ---- MINRANGE2: the lane casters' range -------------------------------------------------
// ability_Minion_Ranged (GUID = FNV-1 of its lower-case name) sets neither interfaceType nor shouldPursue, so the client's
// own validator never range-checks it (interface type 0 = always in range) and never asks for a walk-up. Its sibling
// ability_SuperMinion_Ranged has both: copy its interfaceType (static +0x42C, the Targeted value) and set shouldPursue
// (+0x3C4), so the casters walk up to range 15.5 like the melee minions do.
constexpr std::uint32_t kMinionRangedGuid = 0x42BAD29C;
constexpr std::uint32_t kSuperMinionRangedGuid = 0x38087193;
constexpr std::uintptr_t kStaticShouldPursue = 0x3C4;
constexpr std::uintptr_t kStaticInterfaceType = 0x42C;

int SehFixMinionRanged(int* before, int* targeted) {
  __try {
    void* st = FindStaticAbility(kMinionRangedGuid);
    void* ref = FindStaticAbility(kSuperMinionRangedGuid);
    if (!st || !ref) return -1;
    *before = Field<int>(st, kStaticInterfaceType);
    *targeted = Field<int>(ref, kStaticInterfaceType);
    if (*targeted == 0) return -2;
    Field<int>(st, kStaticInterfaceType) = *targeted;
    Field<unsigned char>(st, kStaticShouldPursue) = 1;
    return 1;
  } __except (SehNote(GetExceptionInformation(), "ability_Minion_Ranged static")) {
    return -3;
  }
}

bool SehInstanceLive(std::uint32_t id) {
  __try {
    void* in = id ? GetInstance(id) : nullptr;
    return in && !Field<unsigned char>(in, inst::kEndPending);
  } __except (SehNote(GetExceptionInformation(), "shield instance")) {
    return false;
  }
}

std::uint32_t SehActivateShield(std::uint32_t guardian) {
  __try {
    if (!FindStaticAbility(kTowerImmunityGuid)) return 0;
    return ActivateFromDefinition(kTowerImmunityGuid, guardian, guardian, 0, 1, 0.f, 0.f, 0.f, 0);
  } __except (SehNote(GetExceptionInformation(), "ActivateFromDefinition (modifier_Tower_Immunity)")) {
    return 0;
  }
}

bool SehEndShield(std::uint32_t instance) {
  __try {
    EndAbility(instance);
    return true;
  } __except (SehNote(GetExceptionInformation(), "EndAbility (modifier_Tower_Immunity)")) {
    return false;
  }
}

bool SehStartGame(void* sim, unsigned long long gameId, std::uint32_t descriptors) {
  __try {
    StartGame(sim, nullptr, static_cast<std::uint32_t>(gameId), static_cast<std::uint32_t>(gameId >> 32), descriptors);
    return true;
  } __except (SehNote(GetExceptionInformation(), "StartGame")) {
    return false;
  }
}

bool SehEndGame(void* sim, std::uint32_t reason, unsigned long long* durationMs, int* winner) {
  __try {
    if (Field<unsigned char>(sim, matchsim::kGameOver)) return false;
    EndGame(sim, reason);
    const unsigned long long s = Field<unsigned long long>(sim, matchsim::kMatchStart);
    const unsigned long long e = Field<unsigned long long>(sim, matchsim::kMatchEnd);
    *durationMs = e >= s ? e - s : 0;
    *winner = Field<unsigned char>(sim, matchsim::kWinner);
    return true;
  } __except (SehNote(GetExceptionInformation(), "EndGame")) {
    return false;
  }
}

double SehMatchSeconds(void* sim) {
  __try {
    return MatchElapsedMs(sim) / 1000.0;
  } __except (SehNote(GetExceptionInformation(), "GetMatchElapsedMs")) {
    return 0.0;
  }
}

unsigned long long SehClockMs(void* sim) {
  __try {
    return GetTimeMs(sim);
  } __except (SehNote(GetExceptionInformation(), "GetTimeMs")) {
    return 0;
  }
}

bool SehLogSimMatch(void* sim, const char* when) {
  __try {
    char* s = static_cast<char*>(sim);
    Log("match: sim %s: +0x14 %u +0x15 %u +0x16 %u, match start %llu ms, end %llu ms, reason %u, winner %u, descriptors "
        "0x%X, game id %llu, game clock %llu ms",
        when, Field<unsigned char>(s, sim::kRunning), Field<unsigned char>(s, matchsim::kGameOver),
        Field<unsigned char>(s, sim::kState16), Field<unsigned long long>(s, matchsim::kMatchStart),
        Field<unsigned long long>(s, matchsim::kMatchEnd), Field<unsigned>(s, matchsim::kEndReason),
        Field<unsigned char>(s, matchsim::kWinner), Field<unsigned>(s, matchsim::kDescriptors),
        Field<unsigned long long>(s, 0x08), GetTimeMs(sim));
    return true;
  } __except (SehNote(GetExceptionInformation(), "LogSimMatch")) {
    return false;
  }
}

// Once a second: NPC_UpdateLevelForMatchTime(object, t, 0) for every live object with an NPC class and an agent.
// Returns the objects checked; `ups` gets the level changes, `first` the first few of them as text.
int SehUpdateLevels(void* sim, float t, unsigned* ups, char* first, std::size_t cap) {
  __try {
    void* om = Field<void*>(sim, sim::kObjectManager);
    int it = ObjMgrIterBegin(om);
    int n = 0;
    std::size_t len = std::strlen(first);
    for (void* o = ObjMgrIterNext(om, &it); o; o = ObjMgrIterNext(om, &it)) {
      if (Field<unsigned char>(o, gobj::kPendingDelete)) continue;
      if (!Field<void*>(o, npc::kOfObject) || !Field<void*>(o, agent::kOfObject)) continue;
      char* ag = Field<char*>(o, agent::kOfObject);
      const unsigned before = Field<unsigned>(ag, 0x1E90);
      UpdateNpcLevel(o, t, 0);
      ++n;
      const unsigned after = Field<unsigned>(ag, 0x1E90);
      if (after != before) {
        ++*ups;
        if (len + 64 < cap) {
          void* nounAsset = Field<void*>(o, gobj::kNoun);
          const char* name = nounAsset ? Field<const char*>(nounAsset, 0) : "?";
          len += static_cast<std::size_t>(std::snprintf(first + len, cap - len, " %08X %s %u->%u;",
                                                        Field<unsigned>(o, gobj::kId), name ? name : "?", before, after));
        }
      }
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "UpdateNpcLevel")) {
    return -1;
  }
}

// The tower table from the server's Bindings (MATCH_FLOW §2.9).
struct RawTower {
  std::uint32_t id;
  char noun[48];
  int team;
  float pos[2];
};
int SehScanByPrefix(void* sim, const char* prefix, RawTower* out, int max) {
  __try {
    void* om = Field<void*>(sim, sim::kObjectManager);
    int it = ObjMgrIterBegin(om);
    int n = 0;
    for (void* o = ObjMgrIterNext(om, &it); o && n < max; o = ObjMgrIterNext(om, &it)) {
      void* nounAsset = Field<void*>(o, gobj::kNoun);
      const char* name = nounAsset ? Field<const char*>(nounAsset, 0) : nullptr;
      if (!name || std::strncmp(name, prefix, std::strlen(prefix)) != 0) continue;
      RawTower& r = out[n++];
      r.id = Field<std::uint32_t>(o, gobj::kId);
      std::snprintf(r.noun, sizeof r.noun, "%s", name);
      r.team = Field<unsigned char>(o, gobj::kTeam);
      r.pos[0] = Field<float>(o, gobj::kPosition);
      r.pos[1] = Field<float>(o, gobj::kPosition + 4);
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "ScanByPrefix")) {
    return -1;
  }
}

// MAPBASE: the server level's LevelGameplayTuning towerRespawnEnabled and tower[] (sim +0x19DF0 -> Level data +0x614
// -> the tuning's data; game.h gameplayTuning). Returns the list's length (at most `max` copied), -1 if unreadable.
int SehReadTowerTuning(void* sim, bool* enabled, flow::TowerTuning* out, int max) {
  __try {
    void* level = Field<void*>(sim, sim::kLevel);
    char* ld = level ? Field<char*>(level, noun::kData) : nullptr;
    void* asset = ld ? Field<void*>(ld, level::kGameplayTuning) : nullptr;
    char* d = asset ? Field<char*>(asset, noun::kData) : nullptr;
    if (!d) return -1;
    *enabled = Field<unsigned char>(d, gameplayTuning::kTowerRespawnEnabled) != 0;
    char* list = Field<char*>(d, gameplayTuning::kTowers);
    const int n = Field<int>(d, gameplayTuning::kTowers + 4);
    if (n < 0 || n > 64 || (n > 0 && !list)) return -1;
    for (int i = 0; i < n && i < max; ++i) {
      char* e = list + i * gameplayTuning::kTowerSize;
      out[i].respawnTime = Field<float>(e, gameplayTuning::kTowerEntryRespawnTime);
      out[i].laneAgentAdjust = Field<int>(e, gameplayTuning::kTowerLaneAgentAdjust);
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "ReadTowerTuning")) {
    return -1;
  }
}

// MAPBASE: an object's NPC type (NonPlayerClass +0x68), -1 without one.
int SehNpcTypeOf(std::uint32_t id) {
  __try {
    void* o = id ? GetObjectById(id) : nullptr;
    void* npcAsset = o ? Field<void*>(o, npc::kOfObject) : nullptr;
    char* d = npcAsset ? Field<char*>(npcAsset, noun::kData) : nullptr;
    return d ? Field<int>(d, npc::kType) : -1;
  } __except (SehNote(GetExceptionInformation(), "NpcTypeOf")) {
    return -1;
  }
}

bool SehWriteTower(void* sim, int teamIndex, int laneIndex, int tier, std::uint32_t id, unsigned long long respawn,
                   bool writeId) {
  __try {
    char* ui = Ui(sim);
    const int slot = (teamIndex * 2 + laneIndex) * 4 + tier;
    if (writeId) Field<std::uint32_t>(ui, ui::kTowers + 4 * slot) = id;
    Field<unsigned long long>(ui, ui::kTowerRespawnTime + 8 * slot) = respawn;
    return true;
  } __except (SehNote(GetExceptionInformation(), "WriteTower")) {
    return false;
  }
}

bool SehSetTowersPerLane(void* sim, unsigned n) {
  __try {
    Field<unsigned>(Ui(sim), ui::kNumTowersPerLane) = n;
    return true;
  } __except (SehNote(GetExceptionInformation(), "TowersPerLane")) {
    return false;
  }
}

bool SehSetGameTime(void* sim, int seconds) {
  __try {
    Field<int>(Ui(sim), ui::kGameTime) = seconds;
    return true;
  } __except (SehNote(GetExceptionInformation(), "GameTime")) {
    return false;
  }
}

// A Lua chunk on the server's lua_State (the master state; the caller binds the server simulator) under lua_pcall;
// its string result (or the error) into `out`.
bool SehRunLua(void* sim, const char* chunk, const char* name, char* out, unsigned cap) {
  __try {
    out[0] = 0;
    void* L = Field<void*>(static_cast<char*>(sim) + sim::kLua, luasim::kLuaState);
    const int top = LuaGetTop(L);
    int rc = LuaLoadBuffer(L, chunk, static_cast<unsigned>(std::strlen(chunk)), name);
    if (rc == 0) rc = LuaPcall(L, 0, 1, 0);
    const char* s = LuaToString(L, -1, nullptr);
    if (s) {
      unsigned k = 0;
      for (; s[k] && k + 1 < cap; ++k) out[k] = s[k];
      out[k] = 0;
    }
    LuaSetTop(L, top);
    return rc == 0;
  } __except (SehNote(GetExceptionInformation(), "RunLua")) {
    return false;
  }
}

void Announce(const char* feedback) {
  char chunk[256], out[256];
  std::snprintf(chunk, sizeof chunk,
                "local a = nUtil.GetAsset('%s.ClientFeedbackDef') "
                "return tostring(nClientFeedback.SendEventToAllPlayers(a, kObjIDNone, kObjIDNone))",
                feedback);
  const bool ok = SehRunLua(g_sim, chunk, "dgsim announcer", out, sizeof out);
  ++g_st.announcer;
  Log("match: announcer %s at match %.2f s (nClientFeedback.SendEventToAllPlayers, mask 0x7FFF): %s %s", feedback, g_t,
      ok ? "ok" : "FAILED", out);
}

// ---- the Lua functions over the client's stubs (SERVER_LUA_API.md, MATCH_FLOW.md §4, R10 §2) ---------------------

bool Ours() { return g_sim && GetCurrentSim() == g_sim; }

float SehArgNumber(void* L, int idx) {
  __try {
    return LuaToNumber(L, idx);
  } __except (SehNote(GetExceptionInformation(), "lua_tonumber")) {
    return 0.f;
  }
}
std::uint32_t SehArgId(void* L, int idx) {
  __try {
    return static_cast<std::uint32_t>(reinterpret_cast<std::uintptr_t>(LuaToUserdata(L, idx)));
  } __except (SehNote(GetExceptionInformation(), "lua_touserdata")) {
    return 0;
  }
}
void SehPushId(void* L, std::uint32_t id) {
  __try {
    LuaPushLightUserdata(L, reinterpret_cast<void*>(static_cast<std::uintptr_t>(id)));
  } __except (SehNote(GetExceptionInformation(), "lua_pushlightuserdata")) {
  }
}
void SehPushNumber(void* L, float v) {
  __try {
    LuaPushNumber(L, v);
  } __except (SehNote(GetExceptionInformation(), "lua_pushnumber")) {
  }
}

// nBattlegrounds.GetGuardianOnTeam(team) -> the Guardian spawned for that team (kObjIDNone if none).
int __cdecl LuaGetGuardianOnTeam(void* L) {
  std::uint32_t id = 0;
  if (Ours()) {
    ++g_st.luaCalls[0];
    const int team = static_cast<int>(SehArgNumber(L, 1));
    for (const Guardian& g : g_guardians)
      if (g.team == team) id = g.id;
  }
  SehPushId(L, id);
  return 1;
}

// nBattlegrounds.OnTitanTowerDestroyed(structure, killer): handled in the next Tick.
int __cdecl LuaOnTitanTowerDestroyed(void* L) {
  if (!Ours()) return 0;
  ++g_st.luaCalls[1];
  Destroyed d;
  d.structure = SehArgId(L, 1);
  d.killer = SehArgId(L, 2);
  d.t = g_t;
  g_destroyed.push_back(d);
  Log("match: nBattlegrounds.OnTitanTowerDestroyed(%08X, killer %08X) at match %.2f s", d.structure, d.killer, d.t);
  return 0;
}

// nBattlegrounds.SetEnergyLineState(line, value, team) (R10 §2).
int __cdecl LuaSetEnergyLineState(void* L) {
  if (!Ours()) return 0;
  ++g_st.luaCalls[2];
  const int line = static_cast<int>(SehArgNumber(L, 1));
  const float value = SehArgNumber(L, 2);
  const int team = static_cast<int>(SehArgNumber(L, 3));
  if (line < 0 || line >= flow::kEnergyLines) {
    Log("match: SetEnergyLineState(%d, %.1f, %d): line out of range; ignored", line, value, team);
    return 0;
  }
  const unsigned long long now = SehClockMs(g_sim);
  const unsigned long long v = flow::EnergyLineTime(value, now);
  __try {
    Field<unsigned long long>(Ui(g_sim), ui::kNubRespawnTime + 8 * line) = v;
    Field<int>(Ui(g_sim), ui::kNubTeams + 4 * line) = team;
  } __except (SehNote(GetExceptionInformation(), "SetEnergyLineState")) {
    return 0;
  }
  if (LogBudget())
    Log("match: SetEnergyLineState(line %d, value %.1f, team %d) at server %llu ms -> mNubRespawnTime %s%llu, mNubTeams %d",
        line, value, team, now, v == ~0ull ? "all ones " : "", v == ~0ull ? 0ull : v, team);
  return 0;
}

// nBattlegrounds.GetNubRespawnTuning() -> the server level's LevelGameplayTuning.nubRespawnTime (480 on v5).
int __cdecl LuaGetNubRespawnTuning(void* L) {
  float v = 0.f;  // the client's stub
  if (Ours()) {
    ++g_st.luaCalls[3];
    v = director::LevelTuning().nubRespawn;
  }
  SehPushNumber(L, v);
  return 1;
}

int SehTeamOf(std::uint32_t id) {
  __try {
    void* o = id ? GetObjectById(id) : nullptr;
    return o ? Field<unsigned char>(o, gobj::kTeam) : -1;
  } __except (SehNote(GetExceptionInformation(), "TeamOf")) {
    return -1;
  }
}

std::map<std::uint32_t, std::string> g_baseLast;  // per minion: what its last call put on the list (logged on change)

// nBattlegrounds.AddBaseStructuresToAggroList(minion) (D5; SERVER_LUA_API.md): Behavior_Assault calls it at the lane's
// last knot (with the tether disabled), then waits in nThread.WaitForEmptyAggroList, in a loop. The other team's living
// nubs and Guardian (gms::flow::BaseStructuresAgainst) go onto the minion's aggro list, nearest first, marked so the
// blackboard update keeps them beyond the drop range (agents::AddBaseStructures). Called every frame while nothing is
// left to attack (the wait yields once per frame on an empty list); logged when the set of structures changes.
int __cdecl LuaAddBaseStructuresToAggroList(void* L) {
  if (!Ours()) return 0;
  ++g_st.luaCalls[4];
  const std::uint32_t minion = SehArgId(L, 1);
  const int team = SehTeamOf(minion);
  std::vector<flow::BaseStructure> all;
  for (const Nub& n : g_nubs) {
    flow::BaseStructure b;
    float hp = 0.f;
    b.id = n.id;
    b.team = n.team;
    b.alive = SehHp(n.id, &hp) == 1;
    all.push_back(b);
  }
  for (const Guardian& g : g_guardians) {
    flow::BaseStructure b;
    float hp = 0.f;
    b.id = g.id;
    b.team = g.team;
    b.alive = SehHp(g.id, &hp) == 1;
    all.push_back(b);
  }
  const std::vector<std::uint32_t> ids = flow::BaseStructuresAgainst(team, all);
  std::string report;
  int marked = ids.empty() ? 0 : agents::AddBaseStructures(minion, ids.data(), static_cast<int>(ids.size()), &report);
  if (marked < 0) marked = 0;
  if (marked > 0) ++g_st.baseMarked;
  else ++g_st.baseEmpty;
  std::string key;
  for (std::uint32_t id : ids) key += std::to_string(id) + " ";
  key += "/" + std::to_string(marked);
  std::string& last = g_baseLast[minion];
  if (key != last) {
    last = key;
    if (LogBudget())
      Log("match: AddBaseStructuresToAggroList(%08X, team %d) at match %.2f s: %d of %zu living enemy base structures "
          "marked on its aggro list, nearest first (id, edge distance): [%s]",
          minion, team, g_t, marked, ids.size(), report.c_str());
  }
  return 0;
}

const LuaReg kBattlegroundsRegs[] = {
    {"GetGuardianOnTeam", &LuaGetGuardianOnTeam},
    {"OnTitanTowerDestroyed", &LuaOnTitanTowerDestroyed},
    {"SetEnergyLineState", &LuaSetEnergyLineState},
    {"GetNubRespawnTuning", &LuaGetNubRespawnTuning},
    {"AddBaseStructuresToAggroList", &LuaAddBaseStructuresToAggroList},
};
constexpr int kNumBattlegroundsRegs = static_cast<int>(sizeof kBattlegroundsRegs / sizeof kBattlegroundsRegs[0]);

bool SehRegister(void* L, const char* ns, const LuaReg* regs, int n) {
  __try {
    RegisterNamespace(L, ns, regs, n);
    return true;
  } __except (SehNote(GetExceptionInformation(), "RegisterNamespace")) {
    return false;
  }
}

void* SehLuaState(void* sim) {
  __try {
    return Field<void*>(static_cast<char*>(sim) + sim::kLua, luasim::kLuaState);
  } __except (SehNote(GetExceptionInformation(), "lua_State")) {
    return nullptr;
  }
}

// ---- the game's end and the structures --------------------------------------------------------------------------

// The match's end: `g` is the losing team's Guardian (C1 01 names it: the end cinematics' death camera looks at it).
// `surrenderReason` (D8b) is 2 or 4 when the team surrendered; 0 = a Guardian's death (1 or 3 from its team).
void EndMatch(const Guardian& g, std::uint32_t killer, const char* how, std::uint32_t surrenderReason = 0) {
  if (g_ended) return;
  const bool surrender = surrenderReason != 0;
  const std::uint32_t reason = surrender ? surrenderReason : flow::EndReasonForDeadGuardian(g.team);
  const char* finishReason = surrender ? "surrender" : "guardian";
  unsigned long long duration = 0;
  int winner = 0xFF;
  if (!SehEndGame(g_sim, reason, &duration, &winner)) {
    Log("match: the match ended (%s, Guardian %08X) but GameSimulator_EndGame did not run (game over already set?)",
        how, g.id);
  }
  g_ended = true;
  director::Stop();
  g_end.reason = reason;
  g_end.guardian = g.id;
  g_end.descriptors = g_opt.descriptors;
  g_end.killer = killer;
  g_end.winner = winner;
  g_end.durationMs = duration;
  SehSetGameTime(g_sim, static_cast<int>(duration / 1000));  // the post-game's duration (D8-static §2)
  if (surrender)
    Log("match: END at match %.2f s: team %d surrendered (%s; C1 01 names its Guardian %08X %s) -> "
        "GameSimulator_EndGame(sim, %u): winner team %d, duration %llu ms",
        g_t, g.team, how, g.id, g.noun.c_str(), reason, winner, duration);
  else
    Log("match: END at match %.2f s: %s (%s, team %d) died (%s, killer %08X) -> GameSimulator_EndGame(sim, %u): winner "
        "team %d, duration %llu ms",
        g_t, g.noun.c_str(), "Guardian", g.team, how, killer, reason, winner, duration);
  SehLogSimMatch(g_sim, "after EndGame");
  Log("match: finishGame {winnerTeam %d, durationMs %llu, reason \"%s\"}%s", winner, duration, finishReason,
      hostctl::Active() ? " (to dgserver after the playerFinish lines)" : " (no control channel: logged only)");
  if (!g_opt.endStats.empty()) {  // D8-live test fixture (T5)
    death::TestStats ts;
    float gold = 0.f;
    const int got = std::sscanf(g_opt.endStats.c_str(), "%d,%d,%d,%d,%f,%d,%d", &ts.kills, &ts.deaths, &ts.assists,
                                &ts.creeps, &gold, &ts.level, &ts.role);
    if ((got == 6 || got == 7) && ts.role <= 3) {  // B7c-live: the 7th value, the role 0-3, is optional
      if (got == 6) ts.role = -1;
      ts.totalGold = gold;
      death::SetTestStats(ts);
    } else {
      Log("match: [host] end_stats '%s' is not K,D,A,creeps,gold,level[,role 0-3]; ignored", g_opt.endStats.c_str());
    }
  }
  // MSTATS1: every seat's seatStats, then the matchTimeline, before the playerFinish lines ([host] match_stats)
  matchstats::AtMatchEnd(g_sim, winner, duration, surrender ? g.team : 0);
  matchrec::AtMatchEnd(g_sim, winner, duration, surrender ? g.team : 0);  // MATCHREC: the end, the final counts
  death::LogPlayerFinish(winner, duration, g_opt.descriptors);  // D1h: the counts kept, as playerFinish lines (C1b: sent)
  hostctl::Finish(winner, duration, finishReason);  // C1b: finishGame, phase postGame, the post-game timer
}

// D8-live test fixture ([host] end_after_s): the end as if the losing team's Guardian had died (its object stays; the
// client's death camera plays the death animation on it, MATCH_FLOW.md D8-static §1).
void EndByFixture() {
  const int deadTeam = flow::EndReasonForDeadGuardian(2) == static_cast<std::uint32_t>(g_opt.endReason) ? 2 : 1;
  Guardian g;
  g.team = deadTeam;
  g.noun = "(no Guardian)";
  for (const Guardian& x : g_guardians)
    if (x.team == deadTeam) g = x;
  Log("match: test fixture [host] end_after_s %.1f: the match ends at match %.2f s as if %s (team %d) had died "
      "(end_reason %d)",
      g_opt.endAfterS, g_t, g.noun.c_str(), deadTeam, g_opt.endReason);
  EndMatch(g, 0, "the test fixture end_after_s");
  if (g_opt.endGuardian) {
    Log("match: test fixture [host] end_guardian: C1 01 names %08X instead of %08X", g_opt.endGuardian, g_end.guardian);
    g_end.guardian = g_opt.endGuardian;
  }
}

void TowerDestroyed(Tower& tw, std::uint32_t killer, double t) {
  if (tw.destroyed) return;
  tw.destroyed = true;
  ++g_st.towersDestroyed;
  SehWriteTower(g_sim, tw.teamIndex, tw.laneIndex, tw.tier, tw.id, ~0ull, false);  // towerRespawnEnabled is false: gone
  Log("match: Binding %08X %s (team %d, lane %d, tier %d) destroyed at match %.2f s by %08X: mTowerRespawnTime all ones",
      tw.id, tw.noun.c_str(), tw.team, tw.laneIndex, tw.tier, t, killer);
  // nGameModeHandlers[OnTowerDestroyed](tower, team, towerIndex, laneIndex, numTowersPerLane) (R10 §1; no effect on v5)
  char chunk[512], out[256];
  std::snprintf(chunk, sizeof chunk,
                "local h = nGameModeHandlers and nGameModeFns and nGameModeHandlers[nGameModeFns.OnTowerDestroyed] "
                "if not h then return 'no handler' end "
                "local ok, err = pcall(h, nUtil.ToGUID('0x%08X'), %d, %d, %d, 3) "
                "return ok and 'ok' or ('error: ' .. tostring(err))",
                tw.id, tw.team, tw.tier, tw.laneIndex);
  const bool ok = SehRunLua(g_sim, chunk, "dgsim OnTowerDestroyed", out, sizeof out);
  ++g_st.handlerCalls;
  Log("match: nGameModeHandlers[OnTowerDestroyed](%08X, team %d, index %d, lane %d, 3): %s %s", tw.id, tw.team, tw.tier,
      tw.laneIndex, ok ? "" : "FAILED", out);
}

void HandleDestroyed() {
  if (g_destroyed.empty()) return;
  std::vector<Destroyed> list;
  list.swap(g_destroyed);
  for (const Destroyed& d : list) {
    bool known = false;
    for (const Guardian& g : g_guardians)
      if (g.id == d.structure) {
        known = true;
        EndMatch(g, d.killer, "OnTitanTowerDestroyed");
      }
    for (Tower& tw : g_towers)
      if (tw.id == d.structure) {
        known = true;
        TowerDestroyed(tw, d.killer, d.t);
      }
    if (!known) Log("match: OnTitanTowerDestroyed for %08X, neither a Guardian nor a Binding; ignored", d.structure);
  }
}

// The HP check is the fallback (T14). Since D1f-live a dead Guardian reports itself: its passive's Deactivate (the
// OnAgentDeath end a few scheduler runs after the death) calls OnTitanTowerDestroyed(guardian, lastAttacker) (T13), so
// the check waits match_end_hp_grace_ms (2,000) after it first sees HP <= 0 before it ends the match itself.
double g_guardianDownAt[2] = {-1.0, -1.0};

void CheckGuardians() {
  if (!g_opt.endOnHp || g_ended) return;
  for (std::size_t i = 0; i < g_guardians.size() && i < 2; ++i) {
    const Guardian& g = g_guardians[i];
    float hp = 0.f;
    const int st = SehHp(g.id, &hp);
    if (st == 1) {
      g_guardianDownAt[i] = -1.0;
      continue;
    }
    if (g_guardianDownAt[i] < 0.0) {
      g_guardianDownAt[i] = g_t;
      Log("match: Guardian %08X %s: %s at match %.2f s; the HP check ends the match in %d ms unless it is reported first",
          g.id, g.noun.c_str(), st < 0 ? "gone" : "HP <= 0", g_t, g_opt.endHpGraceMs);
    }
    if ((g_t - g_guardianDownAt[i]) * 1000.0 < g_opt.endHpGraceMs) continue;
    EndMatch(g, 0, st < 0 ? "the HP check: object gone" : "the HP check: HP <= 0");
    return;
  }
}

// D14: per Guardian, its team's living cores (the nubs, HP > 0) and gms::flow::GuardianShieldStep: the shield on while
// one lives, off when the last one died, on again when one is resurrected (480 s after its death). Each tick (two
// Guardians, ten HP reads) from the level load on; nothing after the match's end. [host] guardian_shield (1).
void UpdateGuardianShields(const char* why) {
  if (!g_opt.guardianShield || g_ended) return;
  for (Guardian& g : g_guardians) {
    float hp = 0.f;
    const bool alive = SehHp(g.id, &hp) == 1;
    int living = 0;
    for (const Nub& n : g_nubs) {
      float nhp = 0.f;
      if (n.team == g.team && SehHp(n.id, &nhp) == 1) ++living;
    }
    if (g.shield && !SehInstanceLive(g.shield)) g.shield = 0;  // ended by something else (or the Guardian's destroy)
    const int step = flow::GuardianShieldStep(g.shield != 0, living, alive);
    if (step > 0) {
      g.shield = SehActivateShield(g.id);
      if (g.shield) ++g_st.shieldsOn;
      else ++g_st.shieldsRefused;
      Log("match: Guardian shield (D14) ON for %s %08X (team %d) at match %.2f s (%s): %d living core(s) -> "
          "modifier_Tower_Immunity %08X%s",
          g.noun.c_str(), g.id, g.team, g_t, why, living, g.shield, g.shield ? "" : " REFUSED");
    } else if (step < 0) {
      const bool ok = SehEndShield(g.shield);
      if (ok) ++g_st.shieldsOff;
      Log("match: Guardian shield (D14) OFF for %s %08X (team %d) at match %.2f s (%s): no living core -> EndAbility(%08X) "
          "%s",
          g.noun.c_str(), g.id, g.team, g_t, why, g.shield, ok ? "done" : "FAILED");
      g.shield = 0;
    } else if (living != g.livingCores && g.livingCores >= 0 && LogBudget()) {
      Log("match: Guardian %s %08X (team %d): %d living core(s) at match %.2f s, shield %s", g.noun.c_str(), g.id, g.team,
          living, g_t, g.shield ? "on" : "off");
    }
    g.livingCores = living;
  }
}

// GUARDCORE: which of its team's five Cores each Guardian has, and so which specials its scripts still allow
// (gms/core/matchflow/guardcore.h). Only watched and logged: the gate itself is the game's scripts
// (ability_BaseNub_Death's modifier_Guardian_Nub<X>_Destroyed, read by each special's condition). A change logs
//   match: GUARDCORE <noun> <id> (team N): core <X> (<special>, <name>, <place>) destroyed|restored at match T s (t S s);
//   cores A-CDE, specials <list>, special cooldown <n> s
// (t = the server clock of the agents' `npc target:` lines). Every tick, also with [host] guardian_shield 0.
void UpdateGuardianCores(const char* why) {
  if (g_ended) return;
  const double clockS = static_cast<double>(SehClockMs(g_sim)) / 1000.0;
  for (Guardian& g : g_guardians) {
    unsigned mask = 0;
    for (const Nub& n : g_nubs) {
      float nhp = 0.f;
      if (n.team == g.team && n.core >= 0 && SehHp(n.id, &nhp) == 1) mask |= 1u << n.core;
    }
    const unsigned present = g.presentMask;  // MAPBASE: a Core the level lacks can't die, so its special stays
    if (g.coreMask < 0) {
      Log("match: GUARDCORE %s %08X (team %d) at %s: cores %s, specials %s, special cooldown %.0f s", g.noun.c_str(),
          g.id, g.team, why, flow::CoreMaskString(mask, present).c_str(), flow::SpecialsOf(mask, present).c_str(),
          flow::SpecialCooldownSeconds(mask, present));
    } else if (static_cast<unsigned>(g.coreMask) != mask) {
      for (int i = 0; i < flow::kGuardianCores; ++i) {
        const bool was = (static_cast<unsigned>(g.coreMask) >> i) & 1u, is = (mask >> i) & 1u;
        if (was == is) continue;
        ++(is ? g_st.coresBack : g_st.coresLost);
        const flow::GuardianCore& c = flow::GuardianCoreAt(i);
        Log("match: GUARDCORE %s %08X (team %d): core %c (%s, %s, %s) %s at match %.2f s (t %.2f s); cores %s, specials "
            "%s, special cooldown %.0f s",
            g.noun.c_str(), g.id, g.team, c.letter, c.special, c.name, c.place, is ? "restored" : "destroyed", g_t, clockS,
            flow::CoreMaskString(mask, present).c_str(), flow::SpecialsOf(mask, present).c_str(),
            flow::SpecialCooldownSeconds(mask, present));
      }
    }
    g.coreMask = static_cast<int>(mask);
  }
}

void EverySecond(double t) {
  // mGameTime: whole match seconds (the HUD clock, the post-game duration); frozen at the end
  if (!g_ended) SehSetGameTime(g_sim, static_cast<int>(t));
  // D1f-live: passive gold (RewardsTuning passiveGold per minute from passiveStartDelayInSeconds, R10 §5)
  if (!g_ended) death::PassiveGoldTick(t);
  if (!g_ended) matchstats::Sample(g_sim, t);  // MSTATS1: the timeline's sample once per match minute
  if (g_opt.levels) {
    char first[600];
    first[0] = 0;
    unsigned ups = 0;
    const int n = SehUpdateLevels(g_sim, static_cast<float>(t), &ups, first, sizeof first);
    if (n > 0) ++g_st.levelChecks;
    g_st.levelUps += ups;
    if (ups && LogBudget()) Log("match: NPC levels at match %.0f s: %u changed of %d:%s", t, ups, n, first);
  }
}

void Start() {
  if (g_started) return;
  if (!SehStartGame(g_sim, g_opt.gameId, g_opt.descriptors)) return;
  g_started = true;
  SehLogSimMatch(g_sim, "after StartGame");
  Log("match: STARTED: GameSimulator_StartGame(sim, no replay, game id %llu, descriptors 0x%X)", g_opt.gameId,
      g_opt.descriptors);
  Announce("MatchStart");
  director::OnMatchStarted();
}

// MAPBASE (gms/core/matchflow/mapbase.h): at the level load, after the tower table and the
// nubs. Sets each Guardian's present Cores (GUARDCORE's specials and cooldown count only Cores the level has) and
// finds the Locus; the rest is logged only, no rule changes:
//   match: MAPBASE: team 1's Bindings: 2, tiers Middle, Inner; team 2's ...
//   match: MAPBASE: LevelGameplayTuning towerRespawnEnabled 0, tower[] 2: 900 s (laneAgentRespawnCounterAdjustment
//          0), 600 s (-1): a destroyed Binding stays down
//   match: MAPBASE: <Guardian> (team N): 3 Core(s) on the level (ABC..); ...
//   match: MAPBASE: the Locus <noun> <id> (team N) at (x, y): NPC type T, HP H: ...
void LogMapBase(void* sim) {
  for (int team = 1; team <= 2; ++team) {
    std::vector<int> tiers;
    for (const Tower& tw : g_towers)
      if (tw.team == team) tiers.push_back(tw.tier);
    Log("match: MAPBASE: team %d's Bindings: %zu, tiers %s (every one attackable at any time: no tier lock)", team,
        tiers.size(), flow::TierNames(flow::TierMask(tiers)).c_str());
  }
  bool enabled = false;
  flow::TowerTuning list[8];
  const int n = SehReadTowerTuning(sim, &enabled, list, 8);
  if (n < 0) {
    Log("match: MAPBASE: LevelGameplayTuning's tower part could not be read");
  } else {
    std::string s;
    for (int i = 0; i < n && i < 8; ++i) {
      char b[96];
      std::snprintf(b, sizeof b, "%s%.0f s (laneAgentRespawnCounterAdjustment %d)", i ? ", " : "", list[i].respawnTime,
                    list[i].laneAgentAdjust);
      s += b;
    }
    Log("match: MAPBASE: LevelGameplayTuning towerRespawnEnabled %d, tower[] %d: %s: %s", enabled ? 1 : 0, n,
        s.empty() ? "-" : s.c_str(),
        enabled ? "towerRespawnEnabled is set, but the host resurrects no Binding (NOT IMPLEMENTED; no shipped level sets "
                  "it)"
                : "a destroyed Binding stays down");
  }
  for (Guardian& g : g_guardians) {
    unsigned present = 0;
    int count = 0;
    for (const Nub& nb : g_nubs)
      if (nb.team == g.team && nb.core >= 0) {
        present |= 1u << nb.core;
        ++count;
      }
    g.presentMask = present;
    std::string absent;
    for (int i = 0; i < flow::kGuardianCores; ++i)
      if (!((present >> i) & 1u)) absent += (absent.empty() ? "" : ", ") + std::string(1, static_cast<char>('A' + i));
    const std::string extra =
        absent.empty() ? std::string()
                       : "; absent " + absent +
                             ": their specials stay allowed (no modifier_Guardian_Nub<X>_Destroyed can come)";
    Log("match: MAPBASE: %s %08X (team %d): %d Core(s) on the level (%s)%s; its shield needs all of them dead",
        g.noun.c_str(), g.id, g.team, count, flow::CoreMaskString(present, present).c_str(), extra.c_str());
  }
  RawTower raw[8];
  const int nl = SehScanByPrefix(sim, "DEST_prefab_", raw, 8);
  for (int i = 0; i < nl; ++i) {
    if (!flow::IsLocusNoun(raw[i].noun)) continue;
    Locus l;
    l.id = raw[i].id;
    l.noun = raw[i].noun;
    l.team = raw[i].team;
    g_loci.push_back(l);
    float hp = 0.f;
    SehHp(l.id, &hp);
    Log("match: MAPBASE: the Locus %s %08X (team %d) at (%.1f, %.1f): NPC type %d, HP %.0f: the base's defence "
        "(ability_Tower_MegaZap), not a Core: neither the shield nor the end depends on it",
        l.noun.c_str(), l.id, l.team, raw[i].pos[0], raw[i].pos[1], SehNpcTypeOf(l.id), hp);
  }
  if (g_loci.empty()) Log("match: MAPBASE: no Locus (DEST_prefab_<blue|red>_tower[_v5]) on the level");
}

}  // namespace

bool Configure(const Options& o) {
  g_opt = o;
  g_configured = true;
  if (!o.enabled) {
    Log("match: off ([host] match = 0)");
    return false;
  }
  const bool ok = CodeMatches(0x008F2EC0, kStartGameBytes, sizeof kStartGameBytes) &&
                  CodeMatches(0x008EF160, kEndGameBytes, sizeof kEndGameBytes) &&
                  CodeMatches(0x00881170, kUpdateNpcLevelBytes, sizeof kUpdateNpcLevelBytes) &&
                  CodeMatches(0x008DDAF0, kCreateNodeListBytes, sizeof kCreateNodeListBytes) &&
                  CodeMatches(0x00873170, kRegisterNamespaceBytes, sizeof kRegisterNamespaceBytes);
  if (!ok) {
    Log("match: a function's prologue differs (not build 854?); the match module stays off");
    g_opt.enabled = false;
    return false;
  }
  director::Configure(o.director);
  if (o.wells) wells::Configure();  // D9
  Log("match (D14): the Guardian's shield while a core lives %d ([host] guardian_shield), a buff camp's major on its "
      "scale-2.0 marker %d ([host] camp_major_spot)",
      o.guardianShield, o.director.campMajorSpot);
  Log("match (D1c-live): guardians %d, levels %d, wells %d, lua %d, end on HP %d, descriptors 0x%X, game id %llu, start "
      "%s; director: waves %d (delay %.1f, interval %.1f, limit %d), camps %d, parasite %d, lane table %d, spacing %.1f, "
      "trace %.0f s, spawners '%s', minions %.0f ms apart (D1k), shape lines every %.1f s for %d waves, %.0f s",
      o.guardians, o.levels, o.wells, o.lua, o.endOnHp, o.descriptors, o.gameId,
      o.startAfterS > 0 ? (std::to_string(o.startAfterS) + " s after the level load").c_str() : "at the first EnterGame",
      o.director.waves, o.director.waveDelay, o.director.waveInterval, o.director.waveLimit, o.director.camps,
      o.director.parasite, o.director.laneTable, o.director.formationSpacing, o.director.traceSeconds,
      o.director.spawnerFilter.empty() ? "all" : o.director.spawnerFilter.c_str(), o.director.spawnIntervalS * 1000.0,
      o.director.shapeTraceS, o.director.shapeWaves, o.director.shapeForS);
  return true;
}

bool Enabled() { return g_configured && g_opt.enabled; }

void OnSimulatorCreated(void* sim) {
  if (!Enabled()) return;
  g_sim = sim;
  if (!g_opt.lua) return;
  void* L = SehLuaState(sim);
  if (!L) return;
  const bool ok = SehRegister(L, "nBattlegrounds", kBattlegroundsRegs, kNumBattlegroundsRegs);
  Log("match: nBattlegrounds.GetGuardianOnTeam, OnTitanTowerDestroyed, SetEnergyLineState, GetNubRespawnTuning, "
      "AddBaseStructuresToAggroList registered over the stubs (GearsLua_RegisterNamespace on lua_State %p): %s",
      L, ok ? "ok" : "FAILED");
  director::RegisterLua(L, sim);
  if (g_opt.wells) wells::RegisterLua(L, sim);  // D9: the bots' well getters
  shop::RegisterLua(L, sim);  // D15c: nBattlegrounds.GetItemFxProjectile/GetItemFxOnHit
}

void OnLevelStarted(void* sim) {
  if (!Enabled()) return;
  g_sim = sim;
  g_levelClockMs = SehClockMs(sim);
  if (g_opt.minionRangedPursue) {
    int before = 0, targeted = 0;
    const int r = SehFixMinionRanged(&before, &targeted);
    Log("match (MINRANGE2): ability_Minion_Ranged interfaceType %d -> %d, shouldPursue on: %s", before, targeted,
        r == 1 ? "ok" : "FAILED");
  }
  director::OnLevelStarted(sim);
  {
    // PARAMAP: the camps' spawn point objects that carry a minimap icon go to the players (8D's camp entry decides
    // when the client draws it: director.cpp UpdateCampIcons)
    const std::vector<std::uint32_t> icons = director::IconCampMarkers();
    repl::SetIconSpawnPoints(icons);
    Log("match: %zu camp spawn point(s) replicated for their minimap icons ([host] camp_icons)", icons.size());
  }
  crowd::OnLevelStarted(sim);  // D1k: the server space's constraint solver (minions push each other)
  // the Guardians (MATCH_FLOW §3.1)
  if (g_opt.guardians)
    for (const director::TitanSpawn& ts : director::Guardians()) {
      float hp = 0.f;
      int npcType = -1;
      const std::uint32_t id = SehSpawn(ts.noun, ts.team, ts.pos, ts.rot, &hp, &npcType);
      Log("match: Guardian %s for team %d at (%.1f, %.1f) (titan node %s): %08X, HP %.0f, NPC type %d%s",
          ts.nounName.c_str(), ts.team, ts.pos[0], ts.pos[1], ts.node.c_str(), id, hp, npcType, id ? "" : " FAILED");
      if (!id) continue;
      Guardian g;
      g.id = id;
      g.team = ts.team;
      g.noun = ts.nounName;
      g_guardians.push_back(g);
    }
  // the tower table (§2.9)
  {
    RawTower raw[32];
    const int n = SehScanByPrefix(sim, "Binding_", raw, 32);
    int filled = 0;
    for (int i = 0; i < n; ++i) {
      Tower tw;
      tw.id = raw[i].id;
      tw.noun = raw[i].noun;
      tw.team = raw[i].team;
      tw.teamIndex = flow::TeamIndex(raw[i].team);
      tw.tier = flow::BindingTier(tw.noun);
      float dist = 0.f;
      const int lane = director::NearestLane(raw[i].pos[0], raw[i].pos[1], &dist);
      tw.laneIndex = lane - 1;  // nLaneID 1 Top, 2 Bottom -> 0, 1
      if (tw.teamIndex < 0 || tw.tier < 0 || tw.laneIndex < 0 || tw.laneIndex > 1) {
        Log("match: Binding %08X %s (team %d) at (%.1f, %.1f) not placed in the tower table (lane %d)", tw.id,
            tw.noun.c_str(), tw.team, raw[i].pos[0], raw[i].pos[1], lane);
        continue;
      }
      SehWriteTower(sim, tw.teamIndex, tw.laneIndex, tw.tier, tw.id, 0, true);
      ++filled;
      Log("match: tower table [%d][%d][%d] = %08X %s at (%.1f, %.1f), %.1f units from its lane", tw.teamIndex,
          tw.laneIndex, tw.tier, tw.id, tw.noun.c_str(), raw[i].pos[0], raw[i].pos[1], dist);
      g_towers.push_back(tw);
    }
    SehSetTowersPerLane(sim, 3);
    Log("match: tower table: %d Bindings, mNumTowersPerLane 3", filled);
  }
  // D5: the nubs, the base structures besides the Guardians (AddBaseStructuresToAggroList)
  {
    RawTower raw[16];
    const int n = SehScanByPrefix(sim, "nub_", raw, 16);
    std::string list;
    for (int i = 0; i < n; ++i) {
      Nub nb;
      nb.id = raw[i].id;
      nb.noun = raw[i].noun;
      nb.team = flow::NubTeam(nb.noun);
      if (!nb.team) nb.team = raw[i].team;
      nb.core = flow::NubCoreIndex(nb.noun);
      g_nubs.push_back(nb);
      char b[96];
      std::snprintf(b, sizeof b, "%s%08X %s team %d (%.1f, %.1f)", list.empty() ? "" : "; ", nb.id, nb.noun.c_str(),
                    nb.team, raw[i].pos[0], raw[i].pos[1]);
      list += b;
    }
    Log("match: base structures: %d nubs [%s] and %zu Guardians", n, list.c_str(), g_guardians.size());
  }
  LogMapBase(sim);  // MAPBASE: the level's tiers, the Binding respawn rule, the Cores a team has, the Locus
  UpdateGuardianShields("the level load");  // D14: invincible while a core lives
  UpdateGuardianCores("the level load");    // GUARDCORE: which specials its Cores allow
  // D9: the spirit wells (wells.h; replaces D1c-live's minimal wells, R10 §4.3)
  if (g_opt.wells) wells::OnLevelStarted(sim);
  g_levelDone = true;
}

void OnEnterGame() {
  if (!Enabled() || g_startRequested) return;
  g_startRequested = true;
  Log("match: the first EnterGame was sent; the match starts this tick");
}

void Tick(void* sim) {
  if (!Enabled() || !g_levelDone) return;
  g_sim = sim;
  if (!g_startRequested && g_opt.startAfterS > 0) {
    const unsigned long long now = SehClockMs(sim);
    if (now >= g_levelClockMs + static_cast<unsigned long long>(g_opt.startAfterS * 1000.0)) {
      g_startRequested = true;
      Log("match: no EnterGame %.1f s after the level load; starting the match ([host] match_start_s)", g_opt.startAfterS);
    }
  }
  if (g_startRequested && !g_started) Start();
  if (!g_started) return;
  g_t = SehMatchSeconds(sim);
  HandleDestroyed();
  UpdateGuardianShields("the tick");  // D14: before the HP check, so the last core's death lifts it this tick
  UpdateGuardianCores("the tick");    // GUARDCORE: a Core's death or resurrect (logged only)
  CheckGuardians();
  if (!g_ended && g_opt.endAfterS > 0 && g_t >= g_opt.endAfterS) EndByFixture();  // D8-live
  if (!g_ended) {
    const double first = director::FirstWaveTime();
    if (first >= 0) {
      if (!g_warned && g_t >= first - 30.0) {  // [assumed] 30 s before the first wave
        g_warned = true;
        Announce("MinionsSpawnWarning");
      }
    }
    director::Tick(sim, g_t);
    if (!g_announcedWave && director::WavesFired() > 0) {
      g_announcedWave = true;
      Announce("MinionsSpawned");
    }
  }
  if (g_opt.wells) wells::Tick(sim, g_t, g_ended);  // D9: capture, locks, workers, well gold, 8C, the GPM
  if (std::floor(g_t) != g_lastSecond) {
    g_lastSecond = std::floor(g_t);
    EverySecond(g_t);
  }
}

bool Started() { return g_started; }
bool Ended() { return g_ended; }
double MatchSeconds() { return g_t; }

void SetGameId(unsigned long long gameId) {
  g_opt.gameId = gameId;
  Log("match: game id %llu (createGame)", gameId);
}

void SetDescriptors(std::uint32_t descriptors) {
  Log("match: descriptors 0x%X (createGame; [host] match_descriptors was 0x%X)", descriptors, g_opt.descriptors);
  g_opt.descriptors = descriptors;
}

std::uint32_t Descriptors() { return g_opt.descriptors; }

bool EndBySurrender(int team, std::uint32_t reason, int yesPercent) {
  if (!Enabled() || !g_started || g_ended || (team != 1 && team != 2) || (reason != 2 && reason != 4)) return false;
  Guardian g;
  g.team = team;
  g.noun = "(no Guardian)";
  for (const Guardian& x : g_guardians)
    if (x.team == team) g = x;
  char how[96];
  if (yesPercent < 0)  // G3b: every human of the team has left the match (gms/core/surrender/team_left.h)
    std::snprintf(how, sizeof how, "every human of the team has left the match");
  else
    std::snprintf(how, sizeof how, "its surrender vote passed with %d %%", yesPercent);
  EndMatch(g, 0, how, reason);
  return g_ended;
}

bool TakeEnd(End* out) {
  if (!g_ended || g_endTaken) return false;
  g_endTaken = true;
  *out = g_end;
  return true;
}

void FormatStats(char* buf, std::size_t size) {
  // the stats line runs outside the server tick: bind the server simulator for the object lookups
  void* prev = GetCurrentSim();
  if (g_sim) SetCurrentSim(g_sim);
  std::string gs;
  for (const Guardian& g : g_guardians) {
    float hp = 0.f;
    const int st = SehHp(g.id, &hp);
    char b[128];
    std::snprintf(b, sizeof b, " %08X team %d hp %.0f%s, cores %d (%s), shield %s", g.id, g.team, hp,
                  st < 0 ? " gone" : "", g.livingCores,
                  g.coreMask < 0 ? "?" : flow::CoreMaskString(static_cast<unsigned>(g.coreMask), g.presentMask).c_str(),
                  g.shield ? "on" : "off");
    gs += b;
  }
  std::string ls;  // MAPBASE: the Locus of each team
  for (const Locus& l : g_loci) {
    float hp = 0.f;
    const int st = SehHp(l.id, &hp);
    char b[96];
    std::snprintf(b, sizeof b, "%s%08X team %d hp %.0f%s", ls.empty() ? "" : ", ", l.id, l.team, hp,
                  st < 0 ? " gone" : "");
    ls += b;
  }
  if (g_sim) SetCurrentSim(prev);
  char dir[400];
  director::FormatStats(dir, sizeof dir);
  std::snprintf(buf, size,
                "%s, match %.1f s; Guardians%s; towers destroyed %u; lua calls guardian %u, destroyed %u, energy %u, nub "
                "tuning %u, base structures %u (marked some %u, none %u); level checks %u (ups %u); announcer %u; "
                "Guardian shield on %u, off %u, refused %u (D14); cores lost %u, restored %u (GUARDCORE); Locus %s "
                "(MAPBASE); exceptions %u; director: %s",
                g_ended ? "ENDED" : g_started ? "running" : "not started", g_t, gs.empty() ? " none" : gs.c_str(),
                g_st.towersDestroyed, g_st.luaCalls[0], g_st.luaCalls[1], g_st.luaCalls[2], g_st.luaCalls[3],
                g_st.luaCalls[4], g_st.baseMarked, g_st.baseEmpty, g_st.levelChecks, g_st.levelUps, g_st.announcer,
                g_st.shieldsOn, g_st.shieldsOff, g_st.shieldsRefused, g_st.coresLost, g_st.coresBack,
                ls.empty() ? "none" : ls.c_str(), g_st.exceptions, dir);
}

}  // namespace match
