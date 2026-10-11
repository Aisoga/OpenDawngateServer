// D9: the spirit wells, their workers and the match economy on the match host: see wells.h.
#include "wells.h"

#include <windows.h>

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <map>
#include <set>
#include <string>
#include <vector>

#include "core/wells/wells.h"
#include "death.h"
#include "director.h"
#include "game.h"
#include "host/host.h"
#include "log.h"

using namespace game;
namespace core = gms::wells;

namespace wells {
namespace {

Options g_opt;
bool g_configured;
void* g_sim;
bool g_ready;  // the node list exists and the rules are initialized
core::Wells g_core;
core::TickResult g_out;
double g_now;  // match seconds of the last tick

struct WellInfo {
  std::uint32_t object = 0;
  std::uint32_t marker = 0;
  int team = 0;
  float pos[3] = {};
  std::vector<director::WellSpot> spots;
};
std::vector<WellInfo> g_info;
void* g_followerNoun;
char g_followerName[64];

std::map<std::uint32_t, double> g_lastDamaged;    // a player's creature -> match seconds of its last damage
std::map<std::uint32_t, double> g_lastPlayerHit;  // D9c: ... of its last hit by an enemy player (core IsPlayerHit)
struct ShaperSeen {  // a player's creature as the last tick saw it (for the hit lines)
  int player = -1, team = 0;
  float pos[3] = {};
};
std::map<std::uint32_t, ShaperSeen> g_shapers;  // the players' creatures in the last tick
bool g_trackerOk;  // D9c: IsPlayerPet and DamageTracker_HasTakenDamageWithin have build 854's prologues
unsigned g_hitLines;

struct Worker {
  int well = -1, slot = -1;
  double spawnedAt = 0.0;
  bool checked = false;
};
std::map<std::uint32_t, Worker> g_workers;
std::map<int, double> g_workerHitAt;  // BOTREFLEX: well index -> match seconds of the last enemy hit on one of its workers

double g_lastSecond = -1.0, g_lastTrace = -1.0;
double g_vim[10];          // VimPer5 accrual per player (gold not yet paid)
int g_lastGpm[3] = {-1, -1, -1};
unsigned g_logLines;

struct Stats {
  unsigned ticks = 0, states = 0, captures = 0, captureStarts = 0, spawns = 0, spawnFails = 0, workerDeaths = 0,
           destroyed = 0, nodeWrites = 0, sendMasks = 0, combatEvents = 0, announcer = 0, exceptions = 0, luaCalls = 0;
  // D9c: hits on players' creatures (any / by an enemy player), those inside an enemy well's ring, and the pauses
  unsigned hits = 0, playerHits = 0, ringHits = 0, ringPlayerHits = 0, pausedHit = 0, pausedContested = 0,
           pausedLeft = 0;
  double wellGold = 0, awardGold = 0, bountyGold = 0, vimGold = 0;  // granted, summed over the players
  unsigned grants = 0, grantFails = 0;
} g_st;

bool LogBudget() {
  ++g_logLines;
  return g_logLines <= static_cast<unsigned>(g_opt.logLimit) || g_logLines % 100 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  ++g_st.exceptions;
  Log("EXCEPTION in wells (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
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

double OptionDouble(const char* key, double def) {
  char v[32], d[32];
  std::snprintf(d, sizeof d, "%g", def);
  HostOptionString(key, d, v, sizeof v);
  return std::atof(v);
}

char* Ui(void* sim) { return static_cast<char*>(sim) + matchsim::kUIData; }

// ---- reading the level --------------------------------------------------------------------------------------------

// LevelGameplayTuning's well part and RewardsTuning.titanGoldRewardInterval into `t` (defaults: SM_Map_v5's values).
bool SehReadTuning(void* sim, core::Tuning* t, void** followerNoun) {
  __try {
    void* level = Field<void*>(sim, sim::kLevel);
    char* ld = level ? Field<char*>(level, noun::kData) : nullptr;
    if (!ld) return false;
    void* ga = Field<void*>(ld, level::kGameplayTuning);
    char* g = ga ? Field<char*>(ga, noun::kData) : nullptr;
    if (!g) return false;
    const float* d = Field<const float*>(g, gameplayTuning::kWellCaptureDurations);
    const unsigned n = Field<unsigned>(g, gameplayTuning::kWellCaptureDurations + 4);
    if (d && n > 0) {
      t->captureDurationCount = std::min(n, core::kMaxCaptureEntries);
      for (unsigned i = 0; i < t->captureDurationCount; ++i) t->captureDurations[i] = d[i];
    }
    t->hitSpeedMultiplier = Field<float>(g, gameplayTuning::kWellHitSpeedMultiplier);
    t->hitWindowSeconds = static_cast<double>(Field<long long>(g, gameplayTuning::kWellHitDurationMs)) / 1000.0;
    t->awardPerPlayer = Field<float>(g, gameplayTuning::kWellAwardPerPlayer);
    t->lockFirst = Field<float>(g, gameplayTuning::kWellLockFirst);
    t->lockAfterCapture = Field<float>(g, gameplayTuning::kWellLockAfterCapture);
    t->captureRate = Field<float>(g, gameplayTuning::kWellCaptureRate);
    t->decayRate = Field<float>(g, gameplayTuning::kWellDecayRate);
    *followerNoun = Field<void*>(g, gameplayTuning::kWellFollowerNoun);
    t->followersOnSpawn = Field<unsigned short>(g, gameplayTuning::kWellFollowersOnSpawn);
    t->autoFillPeriod = Field<float>(g, gameplayTuning::kWellAutoFillPeriod);
    t->autoFillCount = Field<unsigned short>(g, gameplayTuning::kWellAutoFillCount);
    t->autoFillMode = Field<int>(g, gameplayTuning::kWellAutoFillMode);
    t->startCapacity = Field<float>(g, gameplayTuning::kWellStartCapacity);
    t->minedPerMinute = Field<float>(g, gameplayTuning::kWellMinedPerMinute);
    t->minedPerMinutePerFollower = Field<float>(g, gameplayTuning::kWellMinedPerMinutePerFollower);
    t->bountyPercentage = Field<float>(g, gameplayTuning::kWellBountyPercentage);
    void* ra = Field<void*>(ld, level::kRewardsTuning);
    char* r = ra ? Field<char*>(ra, noun::kData) : nullptr;
    if (r) {
      const float iv = Field<float>(r, rewardsTuning::kTitanGoldRewardInterval);
      if (iv > 0.f) t->goldInterval = iv;
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "ReadTuning")) {
    return false;
  }
}

int SehFollowerSlots(void* titanNodeDef) {
  __try {
    return titanNodeDef ? Field<unsigned short>(titanNodeDef, titanNode::kNumFollowerSlots) : -1;
  } __except (SehNote(GetExceptionInformation(), "FollowerSlots")) {
    return -1;
  }
}

const char* SehAssetName(void* asset, char* out, std::size_t cap) {
  __try {
    const char* s = asset ? Field<const char*>(asset, 0) : nullptr;
    std::snprintf(out, cap, "%s", s ? s : "(none)");
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    std::snprintf(out, cap, "?");
  }
  return out;
}

// ---- engine writes ------------------------------------------------------------------------------------------------

bool SehCreateNodeList(void* sim, unsigned n) {
  __try {
    char* ui = Ui(sim);
    if (Field<void*>(ui, ui::kNodeList)) {
      Log("wells: the server's UI data already has a node list; not created again");
      return Field<int>(ui, ui::kNodeCapacity) >= static_cast<int>(n);
    }
    CreateNodeList(ui, n);
    return Field<void*>(ui, ui::kNodeList) != nullptr;
  } __except (SehNote(GetExceptionInformation(), "CreateNodeList")) {
    return false;
  }
}

// Node i's members from `v` (the frequency-1 ones, which only the full 8C carries, when `full`).
bool SehWriteNode(void* sim, int i, const core::NodeView& v, bool full) {
  __try {
    char* nodes = Field<char*>(Ui(sim), ui::kNodeList);
    if (!nodes || i >= Field<int>(Ui(sim), ui::kNodeCapacity)) return false;
    char* nd = nodes + i * ui::kNodeSize;
    Field<std::uint8_t>(nd, ui::node::kTeamControl) = v.teamControl;
    Field<std::uint8_t>(nd, ui::node::kFollowerCount) = v.followerCount;
    Field<std::uint8_t>(nd, ui::node::kFlags) = v.flags;
    Field<float>(nd, ui::node::kGoldPerMinute) = v.goldPerMinute;
    Field<float>(nd, ui::node::kAvailableResource) = v.availableResource;
    Field<float>(nd, ui::node::kBounty) = v.bounty;
    Field<float>(nd, ui::node::kCaptureScore) = v.captureScore;
    Field<float>(nd, ui::node::kCaptureScoreSpeed) = v.captureScoreSpeed;
    Field<float>(nd, ui::node::kLockedTimer) = v.lockedTimer;
    Field<float>(nd, ui::node::kLockedTimerDuration) = v.lockedTimerDuration;
    Field<float>(nd, ui::node::kClaimedTimer) = v.claimedTimer;
    Field<float>(nd, ui::node::kCaptureDuration) = v.captureDuration;
    if (full) {
      Field<float>(nd, ui::node::kGoldMineCapacity) = v.goldMineCapacity;
      Field<std::uint32_t>(nd, ui::node::kServerObjId) = v.serverObjId;
      Field<float>(nd, ui::node::kWorldPos) = v.worldPos.x;
      Field<float>(nd, ui::node::kWorldPos + 4) = v.worldPos.y;
      Field<float>(nd, ui::node::kWorldPos + 8) = v.worldPos.z;
      Field<std::uint8_t>(nd, ui::node::kFollowerCapacity) = v.followerCapacity;
      Field<std::uint8_t>(nd, ui::node::kDisplayIndex) = v.displayIndex;
      Field<float>(nd, ui::node::kPositionX) = v.positionX;
      Field<float>(nd, ui::node::kPositionY) = v.positionY;
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "WriteNode")) {
    return false;
  }
}

bool SehWriteGpm(void* sim, std::uint16_t blue, std::uint16_t red) {
  __try {
    Field<std::uint16_t>(Ui(sim), ui::kBlueGpm) = blue;
    Field<std::uint16_t>(Ui(sim), ui::kRedGpm) = red;
    return true;
  } __except (SehNote(GetExceptionInformation(), "WriteGpm")) {
    return false;
  }
}

unsigned long long SehClockMs(void* sim) {
  __try {
    return GetTimeMs(sim);
  } __except (SehNote(GetExceptionInformation(), "GetTimeMs")) {
    return 0;
  }
}

// The titan-node state (+0x88E) and its animation's start (+0x840, the server clock).
bool SehSetState(std::uint32_t id, std::uint16_t state, unsigned long long ms) {
  __try {
    void* o = GetObjectById(id);
    if (!o) return false;
    Field<std::uint16_t>(o, kObjectTitanNodeState) = state;
    Field<unsigned long long>(o, kObjectGraphicsStateStartMs) = ms;
    return true;
  } __except (SehNote(GetExceptionInformation(), "SetState")) {
    return false;
  }
}

bool SehSetTeam(std::uint32_t id, int team) {
  __try {
    void* o = GetObjectById(id);
    if (!o) return false;
    Field<std::uint8_t>(o, gobj::kTeam) = static_cast<std::uint8_t>(team);
    return true;
  } __except (SehNote(GetExceptionInformation(), "SetTeam")) {
    return false;
  }
}

bool SehCombatEvent(std::uint32_t target, std::uint32_t source) {
  __try {
    unsigned char ev[combatEvent::kSize];
    std::memset(ev, 0, sizeof ev);
    Field<std::uint32_t>(ev, combatEvent::kFlags) = combatEvent::kKillingBlow;
    Field<std::uint32_t>(ev, combatEvent::kTarget) = target;
    Field<std::uint32_t>(ev, combatEvent::kSource) = source;
    QueueCombatEvent(ev, target, true);
    return true;
  } __except (SehNote(GetExceptionInformation(), "QueueCombatEvent")) {
    return false;
  }
}

// A worker at `pos` for `team` owned by `owner` (object +0x48; D9b: its well), with its mining loop's points; its id,
// 0 if the creation failed.
std::uint32_t SehSpawnWorker(void* nounAsset, int team, std::uint32_t owner, const float* pos, const float* rotDeg,
                             std::uint32_t well, const float* gather, const float* dropOff) {
  __try {
    float q[4] = {0.f, 0.f, 0.f, 1.f};
    DegreesToQuaternion(q, rotDeg);
    // D9b (D14 found the same): the worker's owner (object +0x48, sporelabsObject member 13) is its well: the client's
    // own CombatEvent_DamageAbsorb.lua raises MineUnderAttack on an enemy hit on a Follower with objectId =
    // nGameObject.GetOwnerID(worker) (the well's minimap ping, 12-s cooldown and "well_danger" VO); with owner 0 the
    // event had no object. The owner comes from the core's WorkerSpawn ([host] wells_worker_owner 0 passes 0).
    void* o = CreateFromNoun(nounAsset, team, owner, nullptr, pos, q, nullptr);
    if (!o) return 0;
    char* lo = Field<char*>(o, loco::kOfObject);
    if (lo) {
      Field<std::uint32_t>(lo, loco::kFollowerMine) = well;
      std::memcpy(lo + loco::kFollowerGather, gather, 12);
      std::memcpy(lo + loco::kFollowerDropOff, dropOff, 12);
      std::memcpy(lo + loco::kFollowerReturnPoint, pos, 12);  // the loop starts where the worker stands
    }
    return Field<std::uint32_t>(o, gobj::kId);
  } __except (SehNote(GetExceptionInformation(), "SpawnWorker")) {
    return 0;
  }
}

// A worker's state a few seconds after its spawn: movement type, mining flag, travel time, position, owner; false if
// gone.
bool SehWorkerState(std::uint32_t id, int* type, int* mining, float* travel, float* pos, int* level,
                    std::uint32_t* owner) {
  __try {
    void* o = GetObjectById(id);
    if (!o) return false;
    *owner = Field<std::uint32_t>(o, gobj::kOwner);
    char* lo = Field<char*>(o, loco::kOfObject);
    *type = lo ? Field<std::uint8_t>(lo, loco::kMovementType) : -1;
    *mining = lo ? Field<std::uint8_t>(lo, loco::kFollowerMiningFlag) : -1;
    *travel = lo ? Field<float>(lo, loco::kFollowerTravelTime) : -1.f;
    pos[0] = Field<float>(o, gobj::kPosition);
    pos[1] = Field<float>(o, gobj::kPosition + 4);
    pos[2] = Field<float>(o, gobj::kPosition + 8);
    char* ag = Field<char*>(o, agent::kOfObject);
    *level = ag ? static_cast<int>(Field<unsigned>(ag, 0x1E90)) : -1;
    return true;
  } __except (SehNote(GetExceptionInformation(), "WorkerState")) {
    return false;
  }
}

struct RawPlayer {
  int index = -1;
  int team = 0;
  std::uint32_t creature = 0;
  bool alive = false;
  float pos[3] = {};
  float vim = 0.f;
};

int SehPlayers(void* sim, RawPlayer* out, int cap) {
  __try {
    int n = 0;
    for (int i = 0; i < 10 && n < cap; ++i) {
      void* p = GetPlayerByIndex(sim, static_cast<unsigned>(i));
      if (!p) continue;
      RawPlayer& r = out[n++];
      r.index = i;
      r.team = Field<unsigned char>(p, player::kTeam);
      r.creature = Field<std::uint32_t>(p, player::kCurrentCreature);
      void* o = r.creature ? GetObjectById(r.creature) : nullptr;
      if (!o) continue;
      void* c = Field<void*>(o, combatant::kOfObject);
      r.alive = c && Field<float>(c, combatant::kHitPoints) > 0.f;
      r.pos[0] = Field<float>(o, gobj::kPosition);
      r.pos[1] = Field<float>(o, gobj::kPosition + 4);
      r.pos[2] = Field<float>(o, gobj::kPosition + 8);
      void* ad = Field<void*>(o, gobj::kAttributes);
      r.vim = ad ? GetAttribute(ad, kAttrVimPer5, 1) : 0.f;
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "players")) {
    return 0;
  }
}

// D9c: a hit on a player's creature as the core classifies it, the way DamageTracker_RecordCombatTimes (0x008E0DC0)
// does: the attacker's team (+0x4D) against the target's, player-controlled (+0x56), a player's pet
// (GameObject_IsPlayerPet 0x00880830). The attacker's id and noun name for the log.
bool SehHit(void* target, void* attacker, float dealt, core::Hit* h, std::uint32_t* targetId, std::uint32_t* attackerId,
            char* noun, std::size_t cap) {
  __try {
    *h = core::Hit();
    h->dealt = dealt;
    *targetId = Field<std::uint32_t>(target, gobj::kId);
    *attackerId = 0;
    std::snprintf(noun, cap, "-");
    if (!attacker) return true;
    h->attacker = true;
    *attackerId = Field<std::uint32_t>(attacker, gobj::kId);
    h->self = attacker == target;
    h->sameTeam = Field<std::uint8_t>(attacker, gobj::kTeam) == Field<std::uint8_t>(target, gobj::kTeam);
    h->attackerPlayerControlled = Field<std::uint8_t>(attacker, gobj::kPlayerControlled) != 0;
    h->attackerPlayerPet = g_trackerOk && IsPlayerPet(attacker);
    void* n = Field<void*>(attacker, gobj::kNoun);
    const char* s = n ? Field<const char*>(n, 0) : nullptr;
    std::snprintf(noun, cap, "%s", s ? s : "?");
    return true;
  } __except (SehNote(GetExceptionInformation(), "hit")) {
    return false;
  }
}

// D9c: the engine's own record, DamageTracker_HasTakenDamageWithin(creature, ms, kind 1 "player damage") (0x008E0FC0),
// beside the wells' own (they should agree); -1 when it can't be asked. Server simulator bound.
int SehTrackerPlayerHit(std::uint32_t creature, double seconds) {
  if (!g_trackerOk || !creature || seconds <= 0.0) return -1;
  __try {
    void* o = GetObjectById(creature);
    if (!o) return -1;
    return HasTakenDamageWithin(o, static_cast<long long>(seconds * 1000.0 + 0.5), kDamageTrackerPlayer) ? 1 : 0;
  } __except (SehNote(GetExceptionInformation(), "HasTakenDamageWithin")) {
    return -1;
  }
}

// A Lua chunk on the server's lua_State under lua_pcall; its string result (or the error) into `out`.
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

// ---- the result of a tick -------------------------------------------------------------------------------------------

float Dist2D(const float* a, const float* b) {
  const float dx = a[0] - b[0], dy = a[1] - b[1];
  return std::sqrt(dx * dx + dy * dy);
}

// The drop-off and gather points of a worker at `spot` of well `w` [assumed: the worker mines at its spot and carries
// the Vim to the well's rim, where the client shows the deposit and the gold icon at mMine].
void LoopPoints(const WellInfo& w, const float* spot, float* gather, float* dropOff) {
  float rim[3] = {w.pos[0], w.pos[1], w.pos[2]};
  const float d = Dist2D(spot, w.pos);
  if (g_opt.dropOff > 0.f && d > g_opt.dropOff + 0.5f) {
    rim[0] = w.pos[0] + (spot[0] - w.pos[0]) / d * g_opt.dropOff;
    rim[1] = w.pos[1] + (spot[1] - w.pos[1]) / d * g_opt.dropOff;
  }
  const float* g = g_opt.gatherAtWell ? rim : spot;
  const float* o = g_opt.gatherAtWell ? spot : rim;
  std::memcpy(gather, g, 12);
  std::memcpy(dropOff, o, 12);
}

void Grant(const std::vector<RawPlayer>& players, int team, int amount, int source, std::uint32_t target) {
  if (!g_opt.gold || amount <= 0) return;
  const bool hide = source == core::kGoldSourceWellPassive;  // the per-second trickle shows no combat text
  for (const RawPlayer& p : players) {
    if (p.team != team) continue;
    if (death::GrantGold(p.index, static_cast<float>(amount), source, hide, target, 0)) {
      ++g_st.grants;
      if (source == core::kGoldSourceWellPassive) g_st.wellGold += amount;
      else if (source == core::kGoldSourceObjective) g_st.awardGold += amount;
      else if (source == core::kGoldSourceWellBounty) g_st.bountyGold += amount;
    } else {
      ++g_st.grantFails;
    }
  }
}

void DestroyWorkers(const std::vector<std::uint32_t>& ids, int well) {
  if (ids.empty()) return;
  std::string chunk =
      "local fx = nUtil.GetAsset('bg_follower_death_effect.ServerEventDef') local n = 0 for _, o in ipairs({";
  char b[48];
  for (std::uint32_t id : ids) {
    std::snprintf(b, sizeof b, "nUtil.ToGUID('0x%08X'),", id);
    chunk += b;
  }
  chunk +=
      "}) do if nGameObject.Exists(o) then pcall(nEffect.CreateEffect, false, fx, o, nGameObject.GetPosition(o), "
      "kZeroVector, o, nGameObject.GetTeam(o), 0, kObjIDNone, kZeroVector, 0) nGameObject.MarkForDelete(o) n = n + 1 "
      "end end return tostring(n)";
  char out[256];
  const bool ok = SehRunLua(g_sim, chunk.c_str(), "dgsim wells destroy", out, sizeof out);
  g_st.destroyed += static_cast<unsigned>(ids.size());
  for (std::uint32_t id : ids) g_workers.erase(id);
  Log("wells: well %d's %zu worker(s) destroyed by the capture (bg_follower_death_effect + MarkForDelete): %s %s", well,
      ids.size(),
      ok ? "ok, removed" : "FAILED", out);
}

void Announce(int team, std::uint32_t well) {
  char chunk[320], out[128];
  std::snprintf(chunk, sizeof chunk,
                "local a = nUtil.GetAsset('MineUnderAttack.ClientFeedbackDef') local w = nUtil.ToGUID('0x%08X') "
                "return tostring(nClientFeedback.SendEventToPlayersOnTeam(%d, a, w, w))",
                well, team);
  const bool ok = SehRunLua(g_sim, chunk, "dgsim wells announcer", out, sizeof out);
  ++g_st.announcer;
  Log("wells: MineUnderAttack to team %d for well %08X at match %.2f s: %s %s", team, well, g_now, ok ? "ok" : "FAILED",
      out);
}

// D9c: "player P: hit 0.4 s ago (by an enemy player 0.4 s ago, engine tracker 1)" for the log.
std::string Ago(const std::map<std::uint32_t, double>& m, std::uint32_t creature) {
  auto it = m.find(creature);
  if (it == m.end()) return "never";
  char b[32];
  std::snprintf(b, sizeof b, "%.2f s ago", g_now - it->second);
  return b;
}

std::string HitAges(std::uint32_t creature, int player) {
  char b[240];
  const double window = g_core.GetTuning().hitWindowSeconds;
  std::snprintf(b, sizeof b,
                "player %d: damaged %s, hit by an enemy player %s, engine tracker player damage within %.1f s: %d", player,
                Ago(g_lastDamaged, creature).c_str(), Ago(g_lastPlayerHit, creature).c_str(), window,
                SehTrackerPlayerHit(creature, window));
  return b;
}

std::uint32_t CreatureOf(int player) {
  for (const auto& kv : g_shapers)
    if (kv.second.player == player) return kv.first;
  return 0;
}

void Apply(const std::vector<RawPlayer>& players) {
  const unsigned long long ms = SehClockMs(g_sim);
  for (const core::StateChange& c : g_out.states) {
    const bool ok = SehSetState(c.object, c.to, ms);
    ++g_st.states;
    if (LogBudget())
      Log("wells: well %d (%08X) titan-node state %u -> %u at match %.2f s (server %llu ms)%s", c.well, c.object, c.from,
          c.to, c.at, ms, ok ? "" : " (write FAILED)");
  }
  for (const core::Capture& c : g_out.captures) {
    ++g_st.captures;
    const bool team = SehSetTeam(c.object, c.to);
    const bool ev = c.source && SehCombatEvent(c.object, c.source);
    if (ev) ++g_st.combatEvents;
    std::string who;
    for (int p : c.capturers) who += " " + std::to_string(p);
    Log("wells: CAPTURE of well %d (%08X) at match %.2f s: team %d -> %d by player(s)%s (source %08X); mTeam %s, "
        "MineCaptured combat event %s; award %.0f and bounty %.0f per player of team %d; locked %.0f s",
        c.well, c.object, c.at, c.from, c.to, who.c_str(), c.source, team ? "written" : "NOT written",
        ev ? "queued" : "not queued", c.award, c.bounty, c.to, g_core.GetTuning().lockAfterCapture);
  }
  // the capture's workers (destroyed), per well
  {
    std::map<int, std::vector<std::uint32_t>> byWell;
    for (const core::WorkerKill& k : g_out.kills)
      if (k.worker) byWell[k.well].push_back(k.worker);
    for (auto& kv : byWell) DestroyWorkers(kv.second, kv.first);
  }
  for (const core::WorkerSpawn& s : g_out.spawns) {
    if (s.well < 0 || s.well >= static_cast<int>(g_info.size())) continue;
    const WellInfo& w = g_info[static_cast<size_t>(s.well)];
    float pos[3] = {s.pos.x, s.pos.y, s.pos.z};
    float rot[3] = {0.f, 0.f, 0.f};
    if (s.slot >= 0 && s.slot < static_cast<int>(w.spots.size())) std::memcpy(rot, w.spots[s.slot].rot, 12);
    float gather[3], dropOff[3];
    LoopPoints(w, pos, gather, dropOff);
    const std::uint32_t owner = g_opt.workerOwner ? s.owner : 0u;
    const std::uint32_t id = (g_opt.workers && g_followerNoun)
                                 ? SehSpawnWorker(g_followerNoun, s.team, owner, pos, rot, w.object, gather, dropOff)
                                 : 0;
    if (id) {
      g_core.OnWorkerSpawned(s.well, s.slot, id);  // right away, before its death processing can run (Q2)
      Worker wk;
      wk.well = s.well;
      wk.slot = s.slot;
      wk.spawnedAt = g_now;
      g_workers[id] = wk;
      ++g_st.spawns;
      if (LogBudget())
        Log("wells: worker %08X (%s, team %d, owner %08X) for well %d slot %d at (%.1f, %.1f, %.1f), match %.2f s: "
            "gather (%.1f, %.1f), drop-off (%.1f, %.1f), %.1f units; the well has %d",
            id, g_followerName, s.team, owner, s.well, s.slot, pos[0], pos[1], pos[2], g_now, gather[0], gather[1],
            dropOff[0], dropOff[1], Dist2D(gather, dropOff), g_core.Workers(s.well));
    } else {
      g_core.OnWorkerSpawnFailed(s.well, s.slot);
      ++g_st.spawnFails;
      if (LogBudget())
        Log("wells: worker for well %d slot %d %s", s.well, s.slot,
            g_opt.workers ? "NOT created (GameObject_CreateFromNoun failed)" : "not created ([host] wells_workers 0)");
    }
  }
  for (const core::TeamGold& g : g_out.gold) {
    std::uint32_t target = 0;
    for (const core::Capture& c : g_out.captures)
      if (c.to == g.team) target = c.object;
    Grant(players, g.team, g.amount, g.source, target);
    if (g.source != core::kGoldSourceWellPassive && LogBudget())
      Log("wells: gold %d per player of team %d (source %s) at match %.2f s", g.amount, g.team,
          g.source == core::kGoldSourceObjective ? "Objective: the capture award" : "WellBounty: the bounty", g_now);
  }
  for (int i : g_out.captureStarted) {
    ++g_st.captureStarts;
    if (i < 0 || i >= g_core.Count()) continue;
    // D9b: the client announces MineUnderAttack itself when an enemy damages one of the well's workers (their owner);
    // the host's own announcement is off by default, and never for a capture about to complete (it played after the
    // flip, with the new owner's team in the minimap ping: MiniMapRenderer.cpp 2941)
    const double left = g_core.SecondsToCapture(i);
    const bool announce = g_opt.captureAnnounce && left >= g_opt.captureAnnounceMinSeconds;
    Log("wells: well %d (%08X) capture started at match %.2f s against team %d (score %.3f, %.1f s to capture)%s", i,
        g_info[i].object, g_now, g_core.Owner(i), g_core.Score(i), left,
        announce                ? ""
        : g_opt.captureAnnounce ? "; no MineUnderAttack: too little left ([host] wells_capture_announce_min_s)"
                                : "");
    if (announce) Announce(g_core.Owner(i), g_info[i].object);
  }
  // D9c: why a moving capture stopped (a resume is the "capture started" line above)
  for (const core::CapturePause& p : g_out.capturePaused) {
    if (p.well < 0 || p.well >= g_core.Count()) continue;
    const char* why = p.reason == core::kPauseHit         ? "every capturer hit"
                      : p.reason == core::kPauseContested ? "contested by the owner's shaper(s)"
                                                          : "no capturer in the ring (decaying)";
    if (p.reason == core::kPauseHit) ++g_st.pausedHit;
    else if (p.reason == core::kPauseContested) ++g_st.pausedContested;
    else ++g_st.pausedLeft;
    std::string who;
    for (int pl : p.players) who += "; " + HitAges(CreatureOf(pl), pl);
    Log("wells: well %d (%08X) capture PAUSED at match %.2f s: %s (score %.3f; rule: %s)%s", p.well,
        g_info[p.well].object, p.at, why, g_core.Score(p.well),
        g_core.GetTuning().hitRule == core::kHitByAnything ? "any damage pauses (D9)"
                                                           : "only an enemy player's hit pauses (D9c)",
        who.c_str());
  }
}

// Once a second: VimPer5 (attribute 69, gold per 5 s) to every player, paid in whole units [assumed: source Passive].
void PayVim(const std::vector<RawPlayer>& players, double dt) {
  if (!g_opt.vimPer5 || dt <= 0.0) return;
  for (const RawPlayer& p : players) {
    if (p.index < 0 || p.index >= 10 || !p.creature || p.vim <= 0.f) continue;
    g_vim[p.index] += static_cast<double>(p.vim) / 5.0 * dt;
    const double whole = std::floor(g_vim[p.index] + 1e-6);
    if (whole < 1.0) continue;
    g_vim[p.index] -= whole;
    if (death::GrantGold(p.index, static_cast<float>(whole), 11, true, 0, 0)) {  // Passive (11)
      g_st.vimGold += whole;
      ++g_st.grants;
      if (LogBudget())
        Log("wells: VimPer5 %.2f: player %d gets %.0f gold at match %.0f s", p.vim, p.index, whole, g_now);
    }
  }
}

void Trace(const std::vector<RawPlayer>& players) {
  std::string s;
  char b[200];
  for (int i = 0; i < g_core.Count(); ++i) {
    const core::NodeView v = g_core.Node(i);
    std::snprintf(b, sizeof b, " [%d %08X owner %d state %u score %.2f speed %.3f lock %.0f/%.0f workers %d gpm %.1f bounty %.1f]",
                  i, g_info[i].object, g_core.Owner(i), g_core.State(i), v.captureScore, v.captureScoreSpeed,
                  v.lockedTimer, v.lockedTimerDuration, g_core.Workers(i), v.goldPerMinute, v.bounty);
    s += b;
  }
  Log("wells: match %.0f s:%s; GPM blue %u red %u", g_now, s.c_str(), g_core.TeamGpm(1), g_core.TeamGpm(2));
  for (const RawPlayer& p : players) {
    if (!p.creature) continue;
    for (int i = 0; i < g_core.Count(); ++i) {
      const float* wp = g_info[i].pos;
      const float d2 = Dist2D(p.pos, wp);
      if (d2 > 30.f) continue;
      const float dz = p.pos[2] - wp[2];
      Log("wells: player %d (team %d, %s) %.1f units from well %d (2-D; z %.1f vs %.1f, 3-D %.1f); %s", p.index, p.team,
          p.alive ? "alive" : "dead", d2, i, p.pos[2], wp[2], std::sqrt(d2 * d2 + dz * dz),
          HitAges(p.creature, p.index).c_str());
    }
  }
}

void CheckWorkers() {
  for (auto& kv : g_workers) {
    Worker& w = kv.second;
    if (w.checked || g_now - w.spawnedAt < 3.0) continue;
    w.checked = true;
    int type = -1, mining = -1, level = -1;
    float travel = 0.f, pos[3] = {};
    std::uint32_t owner = 0;
    const bool alive = SehWorkerState(kv.first, &type, &mining, &travel, pos, &level, &owner);
    if (LogBudget())
      Log("wells: worker %08X (well %d slot %d) %.1f s after its spawn: %s movement type %d, mining flag %d, travel %.2f "
          "s, at (%.1f, %.1f), NPC level %d, owner %08X",
          kv.first, w.well, w.slot, g_now - w.spawnedAt, alive ? "" : "GONE;", type, mining, travel, pos[0], pos[1],
          level, owner);
  }
}

// ---- the bots' getters (nBattlegrounds over the stubs; nBotHost for dgsim_bots_api.lua) -------------------------------

bool Ours() { return g_sim && GetCurrentSim() == g_sim; }

float SehArgNumber(void* L, int idx) {
  __try {
    return LuaType(L, idx) == kLuaTypeNumber ? LuaToNumber(L, idx) : 0.f;
  } __except (SehNote(GetExceptionInformation(), "lua_tonumber")) {
    return 0.f;
  }
}
bool SehArgBool(void* L, int idx) {
  __try {
    return LuaToBoolean(L, idx) != 0;
  } __except (SehNote(GetExceptionInformation(), "lua_toboolean")) {
    return false;
  }
}
std::uint32_t SehArgId(void* L, int idx) {
  __try {
    return static_cast<std::uint32_t>(reinterpret_cast<std::uintptr_t>(LuaToUserdata(L, idx)));
  } __except (SehNote(GetExceptionInformation(), "lua_touserdata")) {
    return 0;
  }
}
void SehPushIds(void* L, const std::vector<std::uint32_t>& ids) {
  __try {
    LuaCreateTable(L, static_cast<int>(ids.size()), 0);
    for (size_t i = 0; i < ids.size(); ++i) {
      LuaPushNumber(L, static_cast<float>(i + 1));
      LuaPushLightUserdata(L, reinterpret_cast<void*>(static_cast<std::uintptr_t>(ids[i])));
      LuaSetTable(L, -3);
    }
  } __except (SehNote(GetExceptionInformation(), "push ids")) {
  }
}
void SehPushNumber(void* L, float v) {
  __try {
    LuaPushNumber(L, v);
  } __except (SehNote(GetExceptionInformation(), "lua_pushnumber")) {
  }
}
void SehPushBool(void* L, bool v) {
  __try {
    LuaPushBoolean(L, v ? 1 : 0);
  } __except (SehNote(GetExceptionInformation(), "lua_pushboolean")) {
  }
}

// GetMinesForTeam(team [, onlyUnlocked]) -> {wells}: the wells `team` owns (unlocked ones only if asked).
int __cdecl LuaMinesForTeam(void* L) {
  std::vector<std::uint32_t> ids;
  if (Ours() && g_ready) {
    ++g_st.luaCalls;
    ids = g_core.MinesForTeam(static_cast<int>(SehArgNumber(L, 1)), SehArgBool(L, 2));
  }
  SehPushIds(L, ids);
  return 1;
}
// GetFollowersForTitan(well) -> the workers at the well (0 for anything else).
int __cdecl LuaFollowersForTitan(void* L) {
  float n = 0.f;
  if (Ours() && g_ready) {
    ++g_st.luaCalls;
    n = static_cast<float>(g_core.FollowersForTitan(SehArgId(L, 1)));
  }
  SehPushNumber(L, n);
  return 1;
}
// IsTitanNodeLocked(well) -> true while the well can't be captured (false for anything else, as the stub).
int __cdecl LuaIsTitanNodeLocked(void* L) {
  bool locked = false;
  if (Ours() && g_ready) {
    ++g_st.luaCalls;
    locked = g_core.IsTitanNodeLocked(SehArgId(L, 1));
  }
  SehPushBool(L, locked);
  return 1;
}

const LuaReg kBattlegroundsRegs[] = {
    {"GetMinesForTeam", &LuaMinesForTeam},
    {"GetFollowersForTitan", &LuaFollowersForTitan},
    {"IsTitanNodeLocked", &LuaIsTitanNodeLocked},
};
const LuaReg kBotHostRegs[] = {
    {"MinesForTeam", &LuaMinesForTeam},
    {"FollowersForTitan", &LuaFollowersForTitan},
    {"IsTitanNodeLocked", &LuaIsTitanNodeLocked},
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

void Configure() {
  g_configured = true;
  g_opt = Options();
  g_opt.enabled = HostOptionInt("wells", 1) != 0;
  g_opt.lockFirst = OptionDouble("wells_lock_first", -1.0);
  g_opt.lockAfter = OptionDouble("wells_lock_after", -1.0);
  g_opt.workerPeriod = OptionDouble("wells_worker_period", -1.0);
  g_opt.workers = HostOptionInt("wells_workers", 1) != 0;
  g_opt.gold = HostOptionInt("wells_gold", 1) != 0;
  g_opt.damagePause = HostOptionInt("wells_damage_pause", 1) != 0;
  g_opt.dropOff = static_cast<float>(OptionDouble("wells_dropoff", 4.0));
  g_opt.gatherAtWell = HostOptionInt("wells_gather_at_well", 0) != 0;
  g_opt.transitionSeconds = static_cast<float>(OptionDouble("wells_transition_s", 2.0));
  g_opt.vimPer5 = HostOptionInt("vim_per5", 1) != 0;
  g_opt.traceSeconds = OptionDouble("wells_trace_s", 10.0);
  g_opt.logLimit = HostOptionInt("wells_log_limit", g_opt.logLimit);
  g_opt.workerOwner = HostOptionInt("wells_worker_owner", 1) != 0;  // D9b, D14
  g_opt.captureAnnounce = HostOptionInt("wells_capture_announce", 0) != 0;
  g_opt.captureAnnounceMinSeconds = OptionDouble("wells_capture_announce_min_s", g_opt.captureAnnounceMinSeconds);
  g_opt.damagePauseAny = HostOptionInt("wells_damage_pause_any", 0) != 0;  // D9c
  g_opt.hitLogLimit = HostOptionInt("wells_hit_log_limit", g_opt.hitLogLimit);
  g_trackerOk = CodeMatches(0x00880830, kIsPlayerPetBytes, sizeof kIsPlayerPetBytes) &&
                CodeMatches(0x008E0FC0, kHasTakenDamageWithinBytes, sizeof kHasTakenDamageWithinBytes);
  if (g_opt.enabled && !g_trackerOk)
    Log("wells: GameObject_IsPlayerPet / DamageTracker_HasTakenDamageWithin prologues differ (not build 854?): pets don't "
        "count as players, no tracker cross-check");
  if (g_opt.enabled &&
      (!CodeMatches(0x008F7360, kQueueCombatEventBytes, sizeof kQueueCombatEventBytes) ||
       !CodeMatches(0x008E1EC0, kCreateFromNounBytes, sizeof kCreateFromNounBytes) ||
       !CodeMatches(0x008DDAF0, kCreateNodeListBytes, sizeof kCreateNodeListBytes))) {
    Log("wells: a function's prologue differs (not build 854?); the wells stay off");
    g_opt.enabled = false;
  }
  Log("wells (D9): %s; lock first %.0f s, after a capture %.0f s, worker period %.0f s (-1 = the tuning's), workers %d, "
      "gold %d, damage pause %d (D9c: %s), drop-off %.1f units from the centre, gather at the %s, transitions %.1f s, "
      "VimPer5 %d, trace %.0f s; D9b: workers owned by their well %d, the host's own MineUnderAttack %d (only with %.1f s "
      "left)",
      g_opt.enabled ? "on" : "off ([host] wells = 0)", g_opt.lockFirst, g_opt.lockAfter, g_opt.workerPeriod,
      g_opt.workers, g_opt.gold, g_opt.damagePause,
      g_opt.damagePauseAny ? "any damage, D9's rule ([host] wells_damage_pause_any=1)" : "only an enemy player's hit",
      g_opt.dropOff, g_opt.gatherAtWell ? "well" : "spot", g_opt.transitionSeconds, g_opt.vimPer5, g_opt.traceSeconds,
      g_opt.workerOwner, g_opt.captureAnnounce, g_opt.captureAnnounceMinSeconds);
}

bool Enabled() { return g_configured && g_opt.enabled; }

void RegisterLua(void* L, void* sim) {
  if (!Enabled() || !L) return;
  g_sim = sim;
  const bool a = SehRegister(L, "nBattlegrounds", kBattlegroundsRegs, 3);
  const bool b = SehRegister(L, "nBotHost", kBotHostRegs, 3);
  Log("wells: nBattlegrounds.GetMinesForTeam, GetFollowersForTitan, IsTitanNodeLocked over the stubs: %s; nBotHost."
      "MinesForTeam, FollowersForTitan, IsTitanNodeLocked: %s",
      a ? "ok" : "FAILED", b ? "ok" : "FAILED");
}

void OnLevelStarted(void* sim) {
  if (!Enabled()) return;
  g_sim = sim;
  const std::vector<director::Well>& wells = director::Wells();
  if (wells.empty() || wells.size() > static_cast<size_t>(core::kMaxWells)) {
    Log("wells: %zu wells found (1-8 expected); no node list, no wells", wells.size());
    return;
  }
  core::Tuning tu;
  if (!SehReadTuning(sim, &tu, &g_followerNoun)) Log("wells: LevelGameplayTuning not readable; SM_Map_v5's values assumed");
  const int slots = SehFollowerSlots(wells.front().titanNodeDef);
  if (slots > 0) tu.followerSlots = slots;
  if (g_opt.lockFirst >= 0) tu.lockFirst = static_cast<float>(g_opt.lockFirst);
  if (g_opt.lockAfter >= 0) tu.lockAfterCapture = static_cast<float>(g_opt.lockAfter);
  if (g_opt.workerPeriod >= 0) tu.autoFillPeriod = static_cast<float>(g_opt.workerPeriod);
  if (!g_opt.damagePause) tu.hitWindowSeconds = 0.0;
  tu.hitRule = g_opt.damagePauseAny ? core::kHitByAnything : core::kHitByEnemyPlayer;  // D9c
  tu.transitionSeconds = g_opt.transitionSeconds;
  SehAssetName(g_followerNoun, g_followerName, sizeof g_followerName);
  std::string durs;
  for (unsigned i = 0; i < tu.captureDurationCount; ++i) durs += (i ? "/" : "") + std::to_string(static_cast<int>(tu.captureDurations[i]));
  Log("wells: tuning: capture %s s, rate %.2f, decay %.2f, hit multiplier %.2f for %.1f s after %s, award %.0f, locked "
      "%.0f s first and %.0f s after a capture, workers %s (%d on spawn, %d every %.0f s, mode %d, %d slots), start "
      "capacity %.0f, gold per minute %.2f + %.2f per worker, bounty %.0f %%, payout every %.1f s, capture radius %.1f",
      durs.c_str(), tu.captureRate, tu.decayRate, tu.hitSpeedMultiplier, tu.hitWindowSeconds,
      tu.hitRule == core::kHitByAnything ? "any damage (D9)" : "an enemy player's hit (D9c)", tu.awardPerPlayer,
      tu.lockFirst, tu.lockAfterCapture, g_followerName, tu.followersOnSpawn, tu.autoFillCount, tu.autoFillPeriod,
      tu.autoFillMode, tu.followerSlots, tu.startCapacity, tu.minedPerMinute, tu.minedPerMinutePerFollower,
      tu.bountyPercentage * 100.f, tu.goldInterval, tu.captureRadius);
  if (!SehCreateNodeList(sim, static_cast<unsigned>(wells.size()))) {
    Log("wells: the node list could not be created; no wells");
    return;
  }
  std::vector<core::WellDef> defs;
  g_info.clear();
  for (size_t i = 0; i < wells.size(); ++i) {
    const director::Well& w = wells[i];
    core::WellDef d;
    d.object = w.object;
    d.team = w.team;
    d.pos = {w.pos[0], w.pos[1], w.pos[2]};
    d.displayIndex = static_cast<int>(i);  // marker order [assumed], as D1c-live
    float lo = 1e9f, hi = 0.f;
    for (const director::WellSpot& s : w.spots) {
      d.spots.push_back({s.pos[0], s.pos[1], s.pos[2]});
      const float dd = Dist2D(s.pos, w.pos);
      lo = std::min(lo, dd);
      hi = std::max(hi, dd);
    }
    defs.push_back(d);
    WellInfo info;
    info.object = w.object;
    info.marker = w.marker;
    info.team = w.team;
    std::memcpy(info.pos, w.pos, sizeof info.pos);
    info.spots = w.spots;
    g_info.push_back(info);
    Log("wells: well %zu: object %08X (marker %u), team %d, at (%.1f, %.1f, %.1f), display %zu, %zu worker spots "
        "%.1f-%.1f units from it (first (%.1f, %.1f, %.1f))",
        i, w.object, w.marker, w.team, w.pos[0], w.pos[1], w.pos[2], i, w.spots.size(), w.spots.empty() ? 0.f : lo, hi,
        w.spots.empty() ? 0.f : w.spots[0].pos[0], w.spots.empty() ? 0.f : w.spots[0].pos[1],
        w.spots.empty() ? 0.f : w.spots[0].pos[2]);
  }
  g_core.Init(tu, defs, 0.0);
  const unsigned long long ms = SehClockMs(sim);
  for (int i = 0; i < g_core.Count(); ++i) {
    SehWriteNode(sim, i, g_core.Node(i), true);
    SehSetState(g_info[i].object, g_core.State(i), ms);
  }
  SehWriteGpm(sim, g_core.TeamGpm(1), g_core.TeamGpm(2));
  g_ready = true;
  Log("wells: node list of %d created on the server's UI data, every node written; titan-node state %u, locked %.0f s",
      g_core.Count(), g_core.State(0), tu.lockFirst);
}

void Tick(void* sim, double t, bool ended) {
  if (!Enabled() || !g_ready || ended) return;
  g_sim = sim;
  g_now = t;
  ++g_st.ticks;
  RawPlayer raw[10];
  const int n = SehPlayers(sim, raw, 10);
  std::vector<RawPlayer> players(raw, raw + n);
  std::vector<core::Shaper> shapers;
  g_shapers.clear();
  for (const RawPlayer& p : players) {
    if (!p.creature) continue;
    ShaperSeen& seen = g_shapers[p.creature];
    seen.player = p.index;
    seen.team = p.team;
    std::memcpy(seen.pos, p.pos, sizeof seen.pos);
    core::Shaper s;
    s.object = p.creature;
    s.player = p.index;
    s.team = p.team;
    s.alive = p.alive;
    s.pos = {p.pos[0], p.pos[1], p.pos[2]};
    auto it = g_lastDamaged.find(p.creature);
    if (it != g_lastDamaged.end()) s.lastDamagedAt = it->second;
    auto ph = g_lastPlayerHit.find(p.creature);  // D9c
    if (ph != g_lastPlayerHit.end()) s.lastPlayerHitAt = ph->second;
    shapers.push_back(s);
  }
  g_out.Clear();
  g_core.Tick(t, shapers, &g_out);
  Apply(players);
  // the 8C fields of the nodes due (D9-core's send mask), then the HUD's GPM (8B)
  const std::uint8_t mask = g_core.TakeSendMask(t);
  if (mask) {
    ++g_st.sendMasks;
    for (int i = 0; i < g_core.Count(); ++i)
      if (mask & (1u << i) && SehWriteNode(sim, i, g_core.Node(i), false)) ++g_st.nodeWrites;
  }
  const std::uint16_t blue = g_core.TeamGpm(1), red = g_core.TeamGpm(2);
  if (blue != g_lastGpm[1] || red != g_lastGpm[2]) {
    SehWriteGpm(sim, blue, red);
    if (LogBudget()) Log("wells: GPM blue %u red %u at match %.2f s (8B mBlueGpm/mRedGpm)", blue, red, t);
    g_lastGpm[1] = blue;
    g_lastGpm[2] = red;
  }
  if (std::floor(t) != g_lastSecond) {
    const double since = g_lastSecond < 0.0 ? 0.0 : t - g_lastSecond;
    g_lastSecond = std::floor(t);
    PayVim(players, since);
    CheckWorkers();
  }
  if (g_opt.traceSeconds > 0 && (g_lastTrace < 0.0 || t - g_lastTrace >= g_opt.traceSeconds)) {
    g_lastTrace = t;
    Trace(players);
    char st[900];
    FormatStats(st, sizeof st);
    Log("stats wells: %s", st);
  }
}

void OnDamaged(void* target, void* attacker, float dealt) {
  if (!g_ready || !target || !(dealt > 0.f)) return;
  core::Hit h;
  std::uint32_t targetId = 0, attackerId = 0;
  char noun[64];
  if (!SehHit(target, attacker, dealt, &h, &targetId, &attackerId, noun, sizeof noun)) return;
  {  // BOTREFLEX: an enemy hit on a well's worker (the bots defend the well)
    auto w = g_workers.find(targetId);
    if (w != g_workers.end() && h.attacker && !h.sameTeam && !h.self) g_workerHitAt[w->second.well] = g_now;
  }
  auto seen = g_shapers.find(targetId);
  if (seen == g_shapers.end()) return;  // not a player's creature
  ++g_st.hits;
  g_lastDamaged[targetId] = g_now;  // D9's record (any damage)
  const bool playerHit = core::IsPlayerHit(h);
  if (playerHit) {
    ++g_st.playerHits;
    g_lastPlayerHit[targetId] = g_now;  // D9c: what pauses a capture
  }
  // the hit lines: only for a shaper inside the ring of an unlocked well its team doesn't own
  const ShaperSeen& s = seen->second;
  const float radius = g_core.GetTuning().captureRadius;
  for (int i = 0; i < g_core.Count(); ++i) {
    const int owner = g_core.Owner(i);
    if (owner == s.team || (owner != 1 && owner != 2) || g_core.IsLocked(i)) continue;
    const float* wp = g_info[static_cast<size_t>(i)].pos;
    const float dx = s.pos[0] - wp[0], dy = s.pos[1] - wp[1], dz = s.pos[2] - wp[2];
    const float d = std::sqrt(dx * dx + dy * dy + dz * dz);
    if (d > radius) continue;
    ++g_st.ringHits;
    if (playerHit) ++g_st.ringPlayerHits;
    ++g_hitLines;
    if (g_hitLines <= static_cast<unsigned>(g_opt.hitLogLimit) || g_hitLines % 100 == 0)
      Log("wells: hit on player %d (%08X, team %d) in well %d's ring (%.1f units) at match %.2f s: %.2f from %08X %s "
          "(player-controlled %d, pet %d, same team %d) -> %s",
          s.player, targetId, s.team, i, d, g_now, dealt, attackerId, noun, h.attackerPlayerControlled ? 1 : 0,
          h.attackerPlayerPet ? 1 : 0, h.sameTeam ? 1 : 0,
          core::PausesCapture(h, g_core.GetTuning().hitRule)
              ? (playerHit ? "an enemy player's hit: pauses the capture" : "pauses the capture (D9's rule: any damage)")
              : "not a player's hit: no pause");
    break;
  }
}

int Threats(int team, Threat* out, int max) {
  if (!g_ready || !out || max <= 0) return 0;
  int n = 0;
  for (int i = 0; i < g_core.Count() && n < max; ++i) {
    if (g_core.Owner(i) != team || g_core.IsLocked(i) || static_cast<size_t>(i) >= g_info.size()) continue;
    const auto hit = g_workerHitAt.find(i);
    const bool attacked = hit != g_workerHitAt.end() && g_now - hit->second <= 5.0;
    const bool captured = g_core.SecondsToCapture(i) >= 0.0;
    if (!attacked && !captured) continue;
    out[n].object = g_info[static_cast<size_t>(i)].object;
    for (int k = 0; k < 3; ++k) out[n].pos[k] = g_info[static_cast<size_t>(i)].pos[k];
    ++n;
  }
  return n;
}

void OnObjectDied(std::uint32_t id) {
  if (!g_ready || !id) return;
  auto it = g_workers.find(id);
  if (it == g_workers.end()) return;
  const Worker w = it->second;
  g_workers.erase(it);
  const bool known = g_core.OnWorkerDied(id);
  ++g_st.workerDeaths;
  Log("wells: worker %08X of well %d (slot %d) died at match %.2f s: slot freed%s; the well has %d", id, w.well, w.slot,
      g_now, known ? "" : " (unknown to the rules)", g_core.Workers(w.well));
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size,
                "%s; ticks %u, state changes %u, captures %u (started %u, combat events %u), workers spawned %u (failed "
                "%u, alive %zu, died %u, destroyed %u), 8C node writes %u in %u masks, gold granted: wells %.0f, award "
                "%.0f, bounty %.0f, VimPer5 %.0f (grants %u, failed %u); announcer %u, bot getter calls %u; hits on "
                "shapers %u (by enemy players %u), in an enemy ring %u (by enemy players %u); pauses: hit %u, contested "
                "%u, left %u; exceptions %u",
                g_ready ? "ready" : "not ready", g_st.ticks, g_st.states, g_st.captures, g_st.captureStarts,
                g_st.combatEvents, g_st.spawns, g_st.spawnFails, g_workers.size(), g_st.workerDeaths, g_st.destroyed,
                g_st.nodeWrites, g_st.sendMasks, g_st.wellGold, g_st.awardGold, g_st.bountyGold, g_st.vimGold,
                g_st.grants, g_st.grantFails, g_st.announcer, g_st.luaCalls, g_st.hits, g_st.playerHits, g_st.ringHits,
                g_st.ringPlayerHits, g_st.pausedHit, g_st.pausedContested, g_st.pausedLeft, g_st.exceptions);
}

}  // namespace wells
