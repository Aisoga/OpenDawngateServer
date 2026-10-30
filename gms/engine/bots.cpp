// Bot players on the match host (D6a), their missions, shopping and the fire tracker (D6b): see bots.h.
#include "bots.h"

#include <windows.h>

#include <cmath>
#include <cstdio>
#include <cstring>
#include <fstream>
#include <iterator>
#include <map>
#include <memory>
#include <random>
#include <set>
#include <string>
#include <vector>

#include "core/agents/nullpos.h"
#include "core/bots/bots.h"
#include "core/bots/draft.h"
#include "core/bots/guidebuild.h"
#include "core/bots/lanesplit.h"
#include "core/bots/rally.h"
#include "core/vision/vision.h"
#include "core/protocol/commands.h"
#include "core/protocol/messages.h"
#include "agents.h"
#include "commands.h"
#include "death.h"
#include "director.h"
#include "fow.h"
#include "game.h"
#include "hook.h"
#include "host/host.h"
#include "hunterroute.h"
#include "log.h"
#include "match.h"
#include "replicate.h"
#include "wardspots.h"

using namespace game;

namespace bots {
namespace {

namespace gb = gms::bots;

Options g_opt;
void* g_sim;
gb::BotsState g_state;
std::vector<Bot> g_bots;
bool g_planned;
int g_teamSize = 0;  // the resolved team size (EnsurePlayers)
bool g_composed = false;  // BOTCOMP: the bots were planned by gms/core/bots/composition.h (for the log)

// Tuning.BotPlan as the server's catalog holds it.
struct Plan {
  bool read = false;
  std::vector<gb::Slot> slots;
  std::vector<gb::BotCandidate> bots;
  int topLaners = 2, botLaners = 3;
};
Plan g_plan;
std::map<std::string, std::uint32_t> g_classIds;  // BotClass name -> asset id

bool g_lanesSet;
unsigned long long g_nextFrontsMs;

constexpr std::uint32_t kStatusAccepted = 0x10000;   // match accepted (the lobby's match-found count)
constexpr std::uint32_t kStatusPicked = 0x100;       // shaper select done (LabsPlayer_SetNounDef sets it too)
constexpr std::uint32_t kStatusLoaded = 0x400;       // level loaded (what the pre-game's gates wait for)
constexpr std::uint32_t kDescriptorIsFtue = 0x100;   // nGameDescriptors.IsFTUE
int g_fillTeams = 0;  // B10d: createGame's fillTeams (0 = not asked); gb::BotTeamSize has the rule

// BOTDRAFT: the bots draw their classes in shaper select, after the humans (core/bots/draft.h)
std::string g_gameMode;           // createGame's mode ("custom", "practice", ...; "" = no createGame)
bool g_draft = false;             // decided at EnsurePlayers ([host] bot_draft)
bool g_draftHidden = false;       // until the reveal: the bots' updates carry no bot data, noun or role
bool g_drafted = false;           // the draft ran
std::set<std::string> g_available;  // the classes the composition may use (EnsurePlayers)
unsigned g_draftSeed = 1;

// BOTLANESPLIT (core/bots/lanesplit.h): the humans' roles by team the last split counted (the composition's, the
// draft's intents, the reveal's picks), and the match's single lane changes the rule refused or took
std::map<int, std::vector<int>> g_humanRoles;
unsigned g_splitKept = 0, g_splitTaken = 0;

struct Stats {
  unsigned created = 0, createFailed = 0, deployed = 0, deployFailed = 0;
  unsigned frontUpdates = 0, minions = 0;
  unsigned calls[6] = {};  // AssignLane, GetLane, ClosestLane, LaneCenterAndDirection, IsFirstTimePlayer, foreign
  unsigned exceptions = 0;
};
Stats g_st;
unsigned g_logLines;

bool LogBudget() {
  ++g_logLines;
  return g_logLines <= static_cast<unsigned>(g_opt.logLimit) || g_logLines % 100 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  ++g_st.exceptions;
  Log("EXCEPTION in bots (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

class BoundTo {
 public:
  explicit BoundTo(void* sim) : prev_(GetCurrentSim()) { SetCurrentSim(sim); }
  ~BoundTo() { SetCurrentSim(prev_); }
  BoundTo(const BoundTo&) = delete;
  BoundTo& operator=(const BoundTo&) = delete;

 private:
  void* prev_;
};

bool Ours() { return g_sim && GetCurrentSim() == g_sim; }

bool CodeMatches(std::uintptr_t va, const unsigned char* bytes, std::size_t n) {
  __try {
    return std::memcmp(reinterpret_cast<const void*>(va), bytes, n) == 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

// ---- the catalog ------------------------------------------------------------------------------------------------

const char* SehAssetName(void* asset) {
  __try {
    const char* n = asset ? Field<const char*>(asset, asset::kName) : nullptr;
    if (!n) return "?";
    volatile char probe = n[0];
    (void)probe;
    return n;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return "?";
  }
}

void* SehAsset(std::uint32_t id) {
  __try {
    return FindAssetById(id);
  } __except (SehNote(GetExceptionInformation(), "AssetCatalog_FindById")) {
    return nullptr;
  }
}

// A Noun's PlayerClass values (CharacterArchetype, ShaperStyle_AttackStyle, ShaperStyle_DamageStyle).
bool SehShaperValues(void* noun, int v[3]) {
  __try {
    char* nd = noun ? Field<char*>(noun, noun::kData) : nullptr;
    void* pcAsset = nd ? Field<void*>(nd, kNounPlayerClass) : nullptr;
    char* pc = pcAsset ? Field<char*>(pcAsset, asset::kData) : nullptr;
    if (!pc) return false;
    v[0] = Field<int>(pc, playerclass::kArchetype);
    v[1] = Field<int>(pc, playerclass::kAttackStyle);
    v[2] = Field<int>(pc, playerclass::kDamageStyle);
    return true;
  } __except (SehNote(GetExceptionInformation(), "PlayerClass")) {
    return false;
  }
}

bool ShaperTypeOfNoun(void* noun, gb::ShaperType* out) {
  int v[3];
  if (!SehShaperValues(noun, v)) return false;
  *out = gb::ShaperTypeOf(v[0], v[1], v[2]);
  return true;
}

// A BotClass asset's noun (BotClass.nounDef).
void* SehBotClassNoun(void* botClass) {
  __try {
    char* d = botClass ? Field<char*>(botClass, asset::kData) : nullptr;
    return d ? Field<void*>(d, botclass::kNounDef) : nullptr;
  } __except (SehNote(GetExceptionInformation(), "BotClass")) {
    return nullptr;
  }
}

struct RawPlan {
  void* bots[16];
  int numBots;
  int slotPatterns[8][4][3];  // per slot up to 4 shaper types {archetype, attackStyle, damageType}
  int slotCounts[8];
  int numSlots;
  int top, bottom;
};

bool SehReadRawPlan(void* planAsset, RawPlan* r) {
  __try {
    std::memset(r, 0, sizeof *r);
    char* d = planAsset ? Field<char*>(planAsset, asset::kData) : nullptr;
    if (!d) return false;
    void** bots = Field<void**>(d, botplan::kBots);
    const int nb = Field<int>(d, botplan::kBots + 4);
    for (int i = 0; i < nb && i < 16; ++i) r->bots[r->numBots++] = bots[i];
    char* slots = Field<char*>(d, botplan::kSlots);
    const int ns = Field<int>(d, botplan::kSlots + 4);
    for (int s = 0; s < ns && s < 8; ++s) {
      char* slot = slots + s * botplan::kSlotSize;
      char* types = Field<char*>(slot, 0);
      const int nt = Field<int>(slot, 4);
      for (int t = 0; t < nt && t < 4; ++t) {
        for (int k = 0; k < 3; ++k)
          r->slotPatterns[s][t][k] = Field<int>(types + t * botplan::kShaperTypeSize, static_cast<std::uintptr_t>(4 * k));
        ++r->slotCounts[s];
      }
      ++r->numSlots;
    }
    r->top = Field<int>(d, botplan::kTopLaners);
    r->bottom = Field<int>(d, botplan::kBotLaners);
    return true;
  } __except (SehNote(GetExceptionInformation(), "BotPlan")) {
    return false;
  }
}

std::uint32_t SehAssetId(void* asset) {
  __try {
    return asset ? Field<std::uint32_t>(asset, asset::kId) : 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return 0;
  }
}

// Tuning.BotPlan (BattlegroundsGameplayTuning.defaultBotPlan): the slots and the bot classes with their shapers'
// values; [host] bot_classes replaces the bot list.
void ReadPlan() {
  if (g_plan.read) return;
  g_plan = Plan();
  void* asset = SehAsset(gms::AssetIdOf("Tuning.BotPlan"));
  RawPlan raw;
  if (!asset || !SehReadRawPlan(asset, &raw)) {
    Log("bots: Tuning.BotPlan not found or unreadable; no team fill");
    return;
  }
  for (int s = 0; s < raw.numSlots; ++s) {
    gb::Slot slot;
    for (int t = 0; t < raw.slotCounts[s]; ++t)
      slot.shaperTypes.push_back(
          gb::SlotPatternOf(raw.slotPatterns[s][t][0], raw.slotPatterns[s][t][1], raw.slotPatterns[s][t][2]));
    g_plan.slots.push_back(slot);
  }
  std::vector<void*> classes;
  if (!g_opt.classes.empty()) {
    size_t pos = 0;
    while (pos <= g_opt.classes.size()) {
      size_t comma = g_opt.classes.find(',', pos);
      if (comma == std::string::npos) comma = g_opt.classes.size();
      std::string name = g_opt.classes.substr(pos, comma - pos);
      if (!name.empty()) {
        if (name.find('.') == std::string::npos) name += ".BotClass";
        void* a = SehAsset(gms::AssetIdOf(name));
        Log("bots: [host] bot_classes: %s -> %p", name.c_str(), a);
        if (a) classes.push_back(a);
      }
      pos = comma + 1;
    }
  } else {
    for (int i = 0; i < raw.numBots; ++i) classes.push_back(raw.bots[i]);
  }
  for (void* c : classes) {
    gb::BotCandidate b;
    b.botClass = SehAssetName(c);
    gb::ShaperType t;
    void* noun = SehBotClassNoun(c);
    if (!ShaperTypeOfNoun(noun, &t)) Log("bots: %s: its noun's PlayerClass is unreadable", b.botClass.c_str());
    b.shaper = t;
    g_classIds[b.botClass] = SehAssetId(c);
    g_plan.bots.push_back(b);
  }
  g_plan.topLaners = raw.top;
  g_plan.botLaners = raw.bottom;
  g_plan.read = true;
  std::string s;
  for (size_t i = 0; i < g_plan.slots.size(); ++i) {
    s += " [";
    for (size_t t = 0; t < g_plan.slots[i].shaperTypes.size(); ++t) {
      const gb::ShaperType& p = g_plan.slots[i].shaperTypes[t];
      s += (t ? " | " : "") + p.archetype + "/" + p.attackStyle + "/" + p.damageType;
    }
    s += "]";
  }
  std::string b;
  for (const gb::BotCandidate& c : g_plan.bots)
    b += " " + c.botClass + " (" + c.shaper.archetype + "/" + c.shaper.attackStyle + "/" + c.shaper.damageType + ")";
  Log("bots: Tuning.BotPlan: %zu slots%s; bots%s; top laners %d, bottom laners %d", g_plan.slots.size(), s.c_str(),
      b.c_str(), g_plan.topLaners, g_plan.botLaners);
}

// ---- the server players -----------------------------------------------------------------------------------------

struct CreatedPlayer {
  std::uint32_t status;
  int isBot;
  int role;
  void* noun;
};

bool SehCreateBot(void* sim, unsigned index, unsigned char team, unsigned long long onlineId, std::uint32_t classId,
                  int roleOverride, CreatedPlayer* out) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    if (!p) p = AddPlayer(sim, index, 0);
    if (!p) return false;
    Field<unsigned char>(p, player::kTeam) = team;
    Field<unsigned long long>(p, player::kOnlineId) = onlineId;
    const std::uint32_t id = classId;
    ReflectBotData(p, 0, 0, &id);
    if (roleOverride >= 0) Field<unsigned char>(p, player::kRole) = static_cast<unsigned char>(roleOverride);  // BOTCOMP
    Field<std::uint32_t>(p, player::kStatus) |= kStatusAccepted | kStatusPicked | kStatusLoaded;
    Field<float>(p, player::kStatusProgress) = 1.0f;
    out->status = Field<std::uint32_t>(p, player::kStatus);
    out->isBot = Field<unsigned char>(p, player::kIsBot);
    out->role = Field<unsigned char>(p, player::kRole);
    out->noun = Field<void*>(p, player::kNounDef);
    return true;
  } __except (SehNote(GetExceptionInformation(), "create bot player")) {
    return false;
  }
}

// The brain AgentBrain_InitBot set up (AGENT_AI.md §1.4, §11.1): active, the AI definition, the plan and phase nodes,
// and the tree's persistent flags.
struct BrainInfo {
  void* brain;
  int active;
  void* def;
  int plan, phase;
  unsigned treeFlags;
  int playerControlled, playerIndex;
};

bool SehBrainInfo(std::uint32_t id, BrainInfo* b) {
  __try {
    std::memset(b, 0, sizeof *b);
    void* o = GetObjectById(id);
    if (!o) return false;
    b->playerControlled = Field<unsigned char>(o, gobj::kPlayerControlled);
    b->playerIndex = Field<unsigned char>(o, gobj::kPlayerIndex);
    void* bt = Field<void*>(o, obj::kBehaviorTree);
    b->treeFlags = bt ? Field<unsigned>(bt, 0x700) : 0xFFFFFFFFu;
    b->brain = Field<void*>(o, obj::kBrain);
    if (!b->brain) return true;
    b->active = Field<unsigned char>(b->brain, brain::kActive);
    b->def = Field<void*>(b->brain, brain::kAiDefinition);
    b->plan = Field<int>(b->brain, brain::kPlan);
    b->phase = Field<int>(b->brain, brain::kNode);
    return true;
  } __except (SehNote(GetExceptionInformation(), "brain info")) {
    return false;
  }
}

// MAPHOST: whether the setup seats a bot Hunter ([host] bot_hunter_seat; auto: only with jungle camps), logged once
bool HunterSeat() {
  static int logged = -2;
  const int camps = director::JungleCamps();
  const bool on = g_opt.hunterSeat > 0 || (g_opt.hunterSeat < 0 && camps != 0);
  if (logged != (on ? 1 : 0)) {
    logged = on ? 1 : 0;
    Log("bots: MAPHOST: %s ([host] bot_hunter_seat %d, the level's jungle camps %d%s)",
        on ? "a Hunter seat per team" : "no Hunter seat: its seat is a laner at the bottom (flex)", g_opt.hunterSeat,
        camps, camps < 0 ? ": not read yet" : "");
  }
  return on;
}

// ---- the lane fronts --------------------------------------------------------------------------------------------

void SetLanes() {
  std::vector<gb::LanePath> lanes;
  for (int lane = gb::kLaneTop; lane <= gb::kLaneBottom; ++lane) {
    std::vector<float> xyz;
    if (!director::LaneKnots(lane, 1, &xyz)) continue;
    gb::LanePath p;
    p.lane = lane;
    for (size_t i = 0; i + 2 < xyz.size(); i += 3) p.knots.push_back(gb::MakeVec(xyz[i], xyz[i + 1], xyz[i + 2]));
    lanes.push_back(p);
  }
  g_lanesSet = true;
  g_state.fronts.SetLanes(lanes);
  std::string s;
  for (const gb::LanePath& l : lanes) {
    char b[96];
    std::snprintf(b, sizeof b, " lane %d: %zu knots, %.1f units;", l.lane, l.knots.size(), static_cast<double>(l.Length()));
    s += b;
  }
  Log("bots: lane fronts over the director's team-1 splines:%s", lanes.empty() ? " none (the shims' static centres)" : s.c_str());
  if (lanes.size() >= 2)
    Log("bots: MAPHOST: the lanes run up to %.1f units apart: %s", static_cast<double>(g_state.fronts.Separation()),
        g_state.fronts.Shared() ? "one shared path (a Binding stands in every lane, the fronts count the whole path)"
                                : "separate lanes");
}

struct RawUnit {
  int team;
  float pos[3];
};

// The living lane minions (NPC types 0-2: melee, ranged, super) of teams 1 and 2.
int SehCollectMinions(void* sim, RawUnit* out, int cap) {
  __try {
    void* om = Field<void*>(sim, sim::kObjectManager);
    int it = ObjMgrIterBegin(om);
    int n = 0;
    for (void* o = ObjMgrIterNext(om, &it); o && n < cap; o = ObjMgrIterNext(om, &it)) {
      if (Field<unsigned char>(o, gobj::kPendingDelete)) continue;
      void* npcAsset = Field<void*>(o, npc::kOfObject);
      char* d = npcAsset ? Field<char*>(npcAsset, noun::kData) : nullptr;
      if (!d) continue;
      const int type = Field<int>(d, npc::kType);
      if (type < 0 || type > 2) continue;
      const int team = Field<unsigned char>(o, gobj::kTeam);
      if (team != 1 && team != 2) continue;
      void* c = Field<void*>(o, combatant::kOfObject);
      if (c && Field<float>(c, combatant::kHitPoints) <= 0.f) continue;
      out[n].team = team;
      std::memcpy(out[n].pos, static_cast<char*>(o) + gobj::kPosition, sizeof out[n].pos);
      ++n;
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "collect minions")) {
    return -1;
  }
}

// A bot creature's position, HP and level (player +0xB0) for the stats line; false if it is gone.
bool SehBotLook(void* sim, const Bot& b, float pos[3], float* hp, int* level) {
  __try {
    void* o = b.creature ? GetObjectById(b.creature) : nullptr;
    if (!o) return false;
    std::memcpy(pos, static_cast<char*>(o) + gobj::kPosition, 3 * sizeof(float));
    void* c = Field<void*>(o, combatant::kOfObject);
    *hp = c ? Field<float>(c, combatant::kHitPoints) : -1.f;
    void* p = GetPlayerByIndex(sim, static_cast<unsigned>(b.index));
    *level = p ? static_cast<int>(Field<unsigned>(p, player::kLevel)) : -1;
    return true;
  } __except (SehNote(GetExceptionInformation(), "bot look")) {
    return false;
  }
}

unsigned long long SehNowMs(void* sim) {
  __try {
    return GetTimeMs(sim);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return 0;
  }
}

// ---- nBotHost (gms/core/bots/bots.h's contract; the D6a part: lanes, fronts, first-time players) -------------------

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
void SehPushNumber(void* L, float v) {
  __try {
    LuaPushNumber(L, v);
  } __except (SehNote(GetExceptionInformation(), "lua_pushnumber")) {
  }
}
void SehPushBoolean(void* L, bool v) {
  __try {
    LuaPushBoolean(L, v ? 1 : 0);
  } __except (SehNote(GetExceptionInformation(), "lua_pushboolean")) {
  }
}

// A human player's creature: player-controlled and its player index is no bot's.
bool SehHumanCreature(std::uint32_t id, int* index) {
  __try {
    void* o = id ? GetObjectById(id) : nullptr;
    if (!o || !Field<unsigned char>(o, gobj::kPlayerControlled)) return false;
    *index = Field<unsigned char>(o, gobj::kPlayerIndex);
    return true;
  } __except (SehNote(GetExceptionInformation(), "human creature")) {
    return false;
  }
}

// nBotHost.AssignLane(bot, lane)
int __cdecl LuaAssignLane(void* L) {
  if (!Ours()) {
    ++g_st.calls[5];
    return 0;
  }
  ++g_st.calls[0];
  const std::uint32_t bot = SehArgId(L, 1);
  const int lane = static_cast<int>(SehArgNumber(L, 2));
  const int before = g_state.lanes.LaneOf(bot);
  g_state.lanes.Assign(bot, lane);
  if (before != g_state.lanes.LaneOf(bot) && LogBudget())
    Log("bots: nBot.AssignLane(%08X, %d): lane %d -> %d", bot, lane, before, g_state.lanes.LaneOf(bot));
  return 0;
}

// nBotHost.GetLane(obj) -> nLaneID, 0 = none
int __cdecl LuaGetLane(void* L) {
  int lane = 0;
  if (Ours()) {
    ++g_st.calls[1];
    lane = g_state.lanes.LaneOf(SehArgId(L, 1));
  } else {
    ++g_st.calls[5];
  }
  SehPushNumber(L, static_cast<float>(lane));
  return 1;
}

// nBotHost.ClosestLane(x, y, z) -> nLaneID, 0 without lanes
int __cdecl LuaClosestLane(void* L) {
  int lane = 0;
  if (Ours()) {
    ++g_st.calls[2];
    lane = g_state.fronts.Closest(gb::MakeVec(SehArgNumber(L, 1), SehArgNumber(L, 2), SehArgNumber(L, 3)));
  } else {
    ++g_st.calls[5];
  }
  SehPushNumber(L, static_cast<float>(lane));
  return 1;
}

// MAPHOST: nBotHost.SharedLanes() -> true when the level's lanes are one path (gms/core/bots/lanes.h LanesShareOnePath)
int __cdecl LuaSharedLanes(void* L) {
  bool shared = false;
  if (Ours()) {
    if (!g_lanesSet) SetLanes();
    shared = g_state.fronts.Shared();
  } else {
    ++g_st.calls[5];
  }
  SehPushBoolean(L, shared);
  return 1;
}

// nBotHost.ClosestCamp(x, y, z [, team, penalty]) -> x, y, z of the nearest creep camp with a living creature, or
// nothing (D6e: backs nDirector.GetClosestCampPosition, whose client stub always answers a static vector); HUNTERBOT:
// with a team and a penalty, camps on that team's enemy half cost the penalty more
int __cdecl LuaClosestCamp(void* L) {
  if (!Ours()) {
    ++g_st.calls[5];
    return 0;
  }
  float p[3] = {};
  if (!director::ClosestCampPosition(SehArgNumber(L, 1), SehArgNumber(L, 2), p,
                                     static_cast<int>(SehArgNumber(L, 4)), SehArgNumber(L, 5)))
    return 0;
  SehPushNumber(L, p[0]);
  SehPushNumber(L, p[1]);
  SehPushNumber(L, p[2]);
  return 3;
}

// CAMPFINISH round 3: nBotHost.CampFight(bot) -> x, y, z of the nearest living creature of the Hunter's current camp
// fight, the camp's share of its full HP and whether he finishes it first; nothing without a fight
int __cdecl LuaCampFight(void* L) {
  if (!Ours()) {
    ++g_st.calls[5];
    return 0;
  }
  float pos[3] = {}, frac = 1.f;
  bool finishing = false;
  if (!agents::HunterCampFight(SehArgId(L, 1), pos, &frac, &finishing) || gms::agents::IsNullPos(pos)) return 0;
  SehPushNumber(L, pos[0]);
  SehPushNumber(L, pos[1]);
  SehPushNumber(L, pos[2]);
  SehPushNumber(L, frac);
  SehPushBoolean(L, finishing);
  return 5;
}

// HUNTERROUTE: nBotHost.HunterRoute(bot) -> x, y, z of the camp of the Hunter's opening route step (its marker), or
// nothing (no step now, the route over or off: the default camp)
int __cdecl LuaHunterRoute(void* L) {
  if (!Ours()) {
    ++g_st.calls[5];
    return 0;
  }
  float p[3] = {};
  if (!hunterroute::RoutePoint(SehArgId(L, 1), p) || gms::agents::IsNullPos(p)) return 0;
  SehPushNumber(L, p[0]);
  SehPushNumber(L, p[1]);
  SehPushNumber(L, p[2]);
  return 3;
}

// MAPED4: the parasite's rally point of each team from the level's parasite camp (gms/core/bots/rally.h), made once:
// state 0 = keep BOTPARA's points (SM_Map_v5, a level without a parasite camp, [host] bot_parasite_rally 0), 1 = not
// ready yet (the level or its line-of-sight map not read; ask again), 2 = derived (the point in pos).
// MAPED4: whether (x, y) is open ground on the level's line-of-sight map (no wall, inside the level); true without the
// map (nothing to tell).
bool OpenGround(const gms::vision::LosMap* los, float x, float y) {
  if (!los || !los->Valid()) return true;
  const float hx = los->scaleX > 0.f ? static_cast<float>(los->width) / los->scaleX / 2.f : 0.f;
  const float hy = los->scaleY > 0.f ? static_cast<float>(los->height) / los->scaleY / 2.f : 0.f;
  if (x < -hx || x > hx || y < -hy || y > hy) return false;
  int cx = 0, cy = 0;
  los->CellOf(x, y, &cx, &cy);
  return !los->BlocksCell(los->At(cx, cy));
}

struct RallyMade {
  bool made = false;
  int state = 1;
  float pos[2] = {};
};
RallyMade g_rally[3];

int ParasiteRallyFor(int team, float* pos) {
  if (team != 1 && team != 2) return 0;
  RallyMade& r = g_rally[team];
  if (!r.made) {
    const std::string& level = director::LevelName();
    if (level.empty()) return 1;
    static const int option = HostOptionInt("bot_parasite_rally", 1);
    float pp[3] = {};
    if (!gms::bots::RallyDerivedOn(option, level) || !director::ParasiteCampPos(pp)) {
      r.made = true;
      r.state = 0;
      Log("bots: parasite rally team %d: BOTPARA's points kept (%s; [host] bot_parasite_rally %d) (MAPED4)", team,
          !gms::bots::RallyDerivedOn(option, level) ? level.c_str() : "the level has no parasite camp", option);
    } else {
      const gms::vision::LosMap* los = fow::Terrain();
      if (!los && director::MatchNow() < 30.0) return 1;  // the fog reads the map with its first update
      float g[2] = {pp[0], pp[1]};
      bool haveG = false;
      for (const director::TitanSpawn& t : director::Guardians())
        if (t.team == team) {
          g[0] = t.pos[0];
          g[1] = t.pos[1];
          haveG = true;
        }
      auto open = [los](float x, float y) { return OpenGround(los, x, y); };
      const gms::bots::RallyConfig cfg;
      const gms::bots::RallyQuality q = gms::bots::ParasiteRallyPoint(pp[0], pp[1], g[0], g[1], cfg, open, r.pos);
      r.made = true;
      r.state = 2;
      const float dx = r.pos[0] - pp[0], dy = r.pos[1] - pp[1];
      Log("bots: parasite rally team %d: (%.1f, %.1f), %.1f from the parasite camp at (%.1f, %.1f) toward %s: %s%s "
          "(BOTPARA's on SM_Map_v5: (%d, %d)) (MAPED4)",
          team, r.pos[0], r.pos[1], std::sqrt(dx * dx + dy * dy), pp[0], pp[1],
          haveG ? "the team's Guardian" : "the map's low corner (no Guardian)", gms::bots::RallyQualityName(q),
          los ? "" : "; no line-of-sight map, unchecked", team == 1 ? -66 : 66, team == 1 ? -10 : 10);
    }
  }
  if (r.state == 2) {
    pos[0] = r.pos[0];
    pos[1] = r.pos[1];
  }
  return r.state;
}

// MAPED4: nBotHost.ParasiteRally(team) -> state[, x, y, z]: 0 keep BOTPARA's points, 1 not ready (ask again), 2 the
// derived point (the Rally mission's, GatherForParasite's and the Contest's points)
int __cdecl LuaParasiteRally(void* L) {
  if (!Ours()) {
    ++g_st.calls[5];
    return 0;
  }
  float p[2] = {};
  const int state = ParasiteRallyFor(static_cast<int>(SehArgNumber(L, 1)), p);
  SehPushNumber(L, static_cast<float>(state));
  if (state != 2 || gms::agents::IsNullPos(p)) return 1;
  SehPushNumber(L, p[0]);
  SehPushNumber(L, p[1]);
  SehPushNumber(L, 0.f);
  return 4;
}

// MAPED4: nBotHost.WardSpotOpen(x, y) -> false when the ward spot lies on a wall of the level's line-of-sight map
// ([host] bot_ward_spot_check, gms/core/bots/rally.h WardSpotCheckedOn; SM_Map_v5 keeps the data's spots), else true
unsigned g_wardChecks = 0, g_wardWalls = 0;
int __cdecl LuaWardSpotOpen(void* L) {
  if (!Ours()) {
    ++g_st.calls[5];
    return 0;
  }
  static const int option = HostOptionInt("bot_ward_spot_check", 1);
  const float x = SehArgNumber(L, 1), y = SehArgNumber(L, 2);
  bool ok = true;
  if (gms::bots::WardSpotCheckedOn(option, director::LevelName())) {
    ++g_wardChecks;
    ok = OpenGround(fow::Terrain(), x, y);
    // logged once per spot (the condition asks at every think); the refusals counted in g_wardWalls
    static std::set<std::pair<int, int>> logged;
    if (!ok && ++g_wardWalls && logged.insert({static_cast<int>(std::lround(x)), static_cast<int>(std::lround(y))}).second)
      Log("bots: ward spot (%.1f, %.1f) refused: a wall on %s (MAPED4, [host] bot_ward_spot_check %d; logged once a "
          "spot) (t %.2f s)",
          x, y, director::LevelName().c_str(), option, director::MatchNow());
  }
  SehPushBoolean(L, ok);
  return 1;
}

// MAPED4b: nBotHost.WardSpot(side, k) -> state[, x, y]: the level's own ward spots for condition_Bot_Ward's tables
// (side 1 Mortal, 2 Spirit; k from 1): 0 keep the data's tables, 1 not ready (ask again), 2 the spot, 3 no spot k
// (gms/core/bots/wardspots.h, engine/wardspots.cpp)
int __cdecl LuaWardSpot(void* L) {
  if (!Ours()) {
    ++g_st.calls[5];
    return 0;
  }
  float xy[2] = {};
  const int side = static_cast<int>(SehArgNumber(L, 1)), k = static_cast<int>(SehArgNumber(L, 2));
  const int state = wardspots::Answer(side, k, xy);
  static bool logged[3];
  if ((state == 0 || state == 3) && side >= 1 && side <= 2 && !logged[side]) {
    logged[side] = true;
    if (state == 0)
      Log("bots: ward spots: side %d keeps the data's table (MAPED4b) (t %.2f s)", side, director::MatchNow());
    else
      Log("bots: ward spots: side %d's table handed to condition_Bot_Ward: %d spot(s) of the level (MAPED4b) (t %.2f s)",
          side, k - 1, director::MatchNow());
  }
  SehPushNumber(L, static_cast<float>(state));
  if (state != 2) return 1;
  SehPushNumber(L, xy[0]);
  SehPushNumber(L, xy[1]);
  return 3;
}

// nBotHost.LaneCenterAndDirection(lane, team) -> cx, cy, cz, dx, dy, dz, or nothing (the shim's static centre)
int __cdecl LuaLaneCenterAndDirection(void* L) {
  if (!Ours()) {
    ++g_st.calls[5];
    return 0;
  }
  ++g_st.calls[3];
  gb::Front f;
  const int lane = static_cast<int>(SehArgNumber(L, 1)), team = static_cast<int>(SehArgNumber(L, 2));
  if (!g_state.fronts.Get(lane, team, &f)) return 0;
  if (gms::agents::IsNullPos(f.center.x, f.center.y)) {  // NULLPOS: nothing (the shim's static front) instead
    char who[64];
    std::snprintf(who, sizeof who, "lane %d team %d", lane, team);
    agents::ReportNullPos(0, static_cast<int>(gms::agents::NullPosWhere::kLaneFront), who, "LaneCenterAndDirection");
    return 0;
  }
  SehPushNumber(L, f.center.x);
  SehPushNumber(L, f.center.y);
  SehPushNumber(L, f.center.z);
  SehPushNumber(L, f.direction.x);
  SehPushNumber(L, f.direction.y);
  SehPushNumber(L, f.direction.z);
  return 6;
}

// nBotHost.IsFirstTimePlayer(obj) -> a human's creature in an IsFTUE game [assumed source: the game's descriptors,
// the practice game's 0x50C has IsFTUE; the account's own state isn't on the host]
int __cdecl LuaIsFirstTimePlayer(void* L) {
  bool ftue = false;
  if (Ours()) {
    ++g_st.calls[4];
    int index = -1;
    ftue = g_opt.ftue && (match::Descriptors() & kDescriptorIsFtue) && SehHumanCreature(SehArgId(L, 1), &index) &&
           index >= 0 && index < gb::kMaxPlayerIndexes && !IsBotIndex(index);
  } else {
    ++g_st.calls[5];
  }
  SehPushBoolean(L, ftue);
  return 1;
}

// ---- D6b: missions, shopping, the fire tracker ------------------------------------------------------------------

struct MissionStats {
  unsigned calls[9] = {};  // RequestMission, IsMissionAvailable, GetAvailableMission, AcceptMission, GetMissionID,
                           // GetMissionTarget, GetMissionDestination, OpenMission, EndMission
  unsigned opened = 0, endedByCheck = 0, expired = 0, gone = 0;
  unsigned planAccepted = 0, planRefused = 0, planOpened = 0, planLeft = 0, planKept = 0;
  unsigned planAgain = 0;  // accepted or opened what the bot already held (a plan bounce)
  unsigned ticks = 0, tickFailed = 0;
  double checks = 0, checkEnds = 0, luaErrors = 0;  // dgsim_bots.MissionTick's counts, summed
  unsigned byType[14] = {};                         // missions opened by nMissionType
  unsigned acceptedByType[14] = {};
};
MissionStats g_ms;
unsigned long long g_nextMissionMs;
std::string g_lastMissionTick;

struct ShopStats {
  unsigned calls[4] = {};  // CanAffordNextItem, PurchaseNextItem, CanBuyPotion, PurchasePotions
  unsigned bought = 0, potions = 0, refused = 0, noBuild = 0, awayFromShop = 0;
  float spent = 0;
  unsigned guideBots = 0, guideBought = 0, guideChoices = 0, guideDeferred = 0;  // DESECBOT-2
  unsigned guideLogged = 0;  // DESECBOT-2: the guide bots' purchase lines (their own budget)
  unsigned buyLogged = 0;    // BOTSHOP-PASS: the other purchase lines (their own budget too)
  unsigned visits = 0, visitItems = 0, visitPotions = 0;  // BOTSHOP-PASS: the host's shop visits that bought, and what
};
ShopStats g_ss;

struct FireStats {
  bool on = false;
  unsigned calls = 0, unsafe = 0, offGrid = 0;
  bool zonesStubbed = false;  // FIREZONE: 0x00877870 replaced (once a process)
  unsigned zonesSkipped = 0;  // FIREZONE: fire zones the stub didn't cut into the nav graph
};
FireStats g_fs;

const char* const kMissionNames[14] = {"None",        "AttackTower", "DefendTower", "AttackWell", "DefendWell",
                                       "AttackBase",  "DefendBase",  "KillParasite", "KillShaper", "AttackCore",
                                       "DefendCore",  "Rally",       "HarassWell",  "ContestParasite"};
const char* MissionName(int type) { return type >= 0 && type < 14 ? kMissionNames[type] : "?"; }

// Mission log lines per type and event: the first 10, then every 100th (HarassWell is requested and ended by its own
// CheckEnd every second: d6b_run1 had 1,904 of them, which ate the shared log budget).
unsigned g_missionLogs[2][14];
bool MissionLogDue(int event, int type) {
  if (type < 0 || type >= 14) type = 0;
  const unsigned n = ++g_missionLogs[event][type];
  return n <= 10 || n % 100 == 0;
}

double MissionNow() { return g_sim ? static_cast<double>(SehNowMs(g_sim)) / 1000.0 : 0.0; }

bool SehArgBool(void* L, int idx) {
  __try {
    return LuaToBoolean(L, idx) != 0;
  } __except (SehNote(GetExceptionInformation(), "lua_toboolean")) {
    return false;
  }
}
void SehPushId(void* L, std::uint32_t id) {
  __try {
    LuaPushLightUserdata(L, reinterpret_cast<void*>(static_cast<std::uintptr_t>(id)));
  } __except (SehNote(GetExceptionInformation(), "lua_pushlightuserdata")) {
  }
}

// An object's team and position (false when it is gone).
bool SehObjTeamPos(std::uint32_t id, int* team, gb::Vec3* pos) {
  __try {
    void* o = id ? GetObjectById(id) : nullptr;
    if (!o) return false;
    if (team) *team = Field<unsigned char>(o, gobj::kTeam);
    if (pos) {
      const float* p = reinterpret_cast<const float*>(static_cast<char*>(o) + gobj::kPosition);
      *pos = gb::MakeVec(p[0], p[1], p[2]);
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "object team/position")) {
    return false;
  }
}

const char* SehObjName(std::uint32_t id) {
  __try {
    void* o = id ? GetObjectById(id) : nullptr;
    if (!o) return id ? "gone" : "none";
    void* noun = Field<void*>(o, gobj::kNoun);
    const char* name = noun ? Field<const char*>(noun, asset::kName) : nullptr;
    if (!name) return "?";
    volatile char probe = name[0];
    (void)probe;
    return name;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return "?";
  }
}

const Bot* BotByCreature(std::uint32_t creature) {
  for (const Bot& b : g_bots)
    if (b.creature && b.creature == creature) return &b;
  return nullptr;
}

// BOTLANESPLIT: one line per team: its bots by lane (class and role), the humans counted, split or not.
void LogLaneSplit(const char* when) {
  for (int team = 1; team <= 2; ++team) {
    std::string top, bottom;
    std::vector<gb::LaneMember> members;
    for (const Bot& b : g_bots) {
      if (b.team != team) continue;
      members.push_back(gb::LaneMember{b.role, b.lane});
      std::string& side = b.lane == gb::kLaneTop ? top : bottom;
      side += (side.empty() ? "" : ", ") + b.botClass + " " + gb::RoleName(b.role);
    }
    if (members.empty()) continue;
    std::string humans;
    for (int r : g_humanRoles[team]) humans += (humans.empty() ? "" : ", ") + std::string(gb::RoleName(r));
    Log("bots: BOTLANESPLIT (%s): team %d top [%s], bottom [%s]; humans [%s]; the bots' roles %s", when, team,
        top.c_str(), bottom.c_str(), humans.c_str(),
        gb::LanesSplit(members) ? "split" : "NOT split (the lane counts allow no better)");
  }
}

// BOTLANESPLIT: the bots of every team divide their roles over the lanes again (their current lanes give the counts);
// a deployed bot's lane is reassigned too.
void ResplitLanes(const char* when) {
  if (!g_opt.laneSplit) return;
  for (int team = 1; team <= 2; ++team) {
    std::vector<gb::LaneMember> members;
    std::vector<Bot*> at;
    for (Bot& b : g_bots)
      if (b.team == team) {
        members.push_back(gb::LaneMember{b.role, b.lane});
        at.push_back(&b);
      }
    if (members.empty()) continue;
    const std::vector<int> lanes =
        gb::SplitLanes(members, g_humanRoles[team], g_plan.topLaners > 0 ? g_plan.topLaners : 2);
    for (size_t k = 0; k < at.size(); ++k) {
      Bot& b = *at[k];
      if (lanes[k] == b.lane) continue;
      Log("bots: BOTLANESPLIT (%s): player %d (%s, %s, team %d) lane %d -> %d", when, b.index, b.botClass.c_str(),
          gb::RoleName(b.role), team, b.lane, lanes[k]);
      b.lane = lanes[k];
      if (b.creature) g_state.lanes.Assign(b.creature, b.lane);
    }
  }
}

// BOTCOMP: nBotHost.BotRole(obj) -> the bot's mRole (RolesTuning.role index: 0 Gladiator, 1 Tactician, 2 Hunter, 3 Predator),
// -1 for an object that is no bot of ours.
int __cdecl LuaBotRole(void* L) {
  int role = -1;
  if (Ours()) {
    const Bot* b = BotByCreature(SehArgId(L, 1));
    if (b) role = b->role;
  } else {
    ++g_st.calls[5];
  }
  SehPushNumber(L, static_cast<float>(role));
  return 1;
}

// BOTLANESPLIT: nBotHost.LaneSplit(bot, lane) -> the lane the bot takes for a single lane change in the match
// (dgsim_bots_api.lua's ability_Bot_AssignToThreatenedLane): `lane` when its role's groups stay split over its team's
// other bots (core/bots/lanesplit.h MoveKeepsSplit) or [host] bot_lane_split is 0, else its current lane. It assigns
// nothing; the caller does.
int __cdecl LuaLaneSplit(void* L) {
  const std::uint32_t bot = SehArgId(L, 1);
  const int lane = static_cast<int>(SehArgNumber(L, 2));
  int out = lane;
  if (Ours()) {
    const Bot* me = BotByCreature(bot);
    const int from = g_state.lanes.LaneOf(bot);
    if (g_opt.laneSplit && me) {
      std::vector<gb::LaneMember> others;
      for (const Bot& b : g_bots)
        if (&b != me && b.team == me->team && b.creature)
          others.push_back(gb::LaneMember{b.role, g_state.lanes.LaneOf(b.creature)});
      if (!gb::MoveKeepsSplit(me->role, from, lane, others)) out = from;
    }
    // A repeat of the bot's last question with the same answer is neither counted nor logged: the Lua's
    // condition_Bot_AssignedToThreatenedLane asks at every think while the gambit stays refused, which used up the
    // bots' log budget within the first waves.
    const bool repeat = me && me->splitAsk[0] == from && me->splitAsk[1] == lane && me->splitAsk[2] == out;
    if (me) {
      me->splitAsk[0] = from;
      me->splitAsk[1] = lane;
      me->splitAsk[2] = out;
    }
    if (!repeat && from != lane && (lane == gb::kLaneTop || lane == gb::kLaneBottom)) {
      if (out == lane) ++g_splitTaken;
      else ++g_splitKept;
      if (LogBudget())
        Log("bots: BOTLANESPLIT: player %d (%s, %s) asked to change lane %d -> %d: %s (kept %u, taken %u)",
            me ? me->index : -1, me ? me->botClass.c_str() : "?", me ? gb::RoleName(me->role) : "?", from, lane,
            out == lane ? "taken" : "kept (its role's split)", g_splitKept, g_splitTaken);
    }
  } else {
    ++g_st.calls[5];
  }
  SehPushNumber(L, static_cast<float>(out));
  return 1;
}

// NULLPOS: nBotHost.NullPos(obj, kind [, lane]): the host's Lua found the null position as a goal (kind 6: a running
// nBehavior_Follow without an object read (0, 0) from nAgent.GetTargetPosition; 7: GetLaneCenterAndDirection for an
// unknown lane). Logs the "NULLPOS:" line and counts it (gms/core/agents/nullpos.h); the Lua recovers.
int __cdecl LuaNullPos(void* L) {
  if (!Ours()) {
    ++g_st.calls[5];
    return 0;
  }
  const std::uint32_t obj = SehArgId(L, 1);
  const int kind = static_cast<int>(SehArgNumber(L, 2));
  char who[64];
  std::snprintf(who, sizeof who, "lane %d", static_cast<int>(SehArgNumber(L, 3)));
  const char* where = kind == static_cast<int>(gms::agents::NullPosWhere::kFollow) ? "nBehavior_Follow (engine)"
                      : kind == static_cast<int>(gms::agents::NullPosWhere::kLaneCentre)
                          ? "nBot.GetLaneCenterAndDirection (Lua shim)"
                          : "host Lua";
  agents::ReportNullPos(obj, kind, obj ? nullptr : who, where);
  return 0;
}

// NULLPOS: nBotHost.NullPosRead(): one pass of a running Follow on a point (the guard's evidence in "stats nullpos:").
int __cdecl LuaNullPosRead(void* /*L*/) {
  if (Ours()) agents::NoteFollowPointRead();
  return 0;
}

std::string DescribeMission(const gb::Mission& m) {
  char b[200];
  std::snprintf(b, sizeof b, "%u (team %d %s on %08X %s, agents %d, timeout %.0f s%s%s)", m.id, m.team,
                MissionName(m.type), m.target, SehObjName(m.target), m.agents, static_cast<double>(m.timeout),
                m.hasDestination ? ", destination set" : "", m.condition ? "" : ", no condition");
  return b;
}

// nBotHost.RequestMission(team, type, target, agents, timeoutSec, conditionGUID, hasDest, dx, dy, dz) -> id
int __cdecl LuaRequestMission(void* L) {
  std::uint32_t id = 0;
  if (Ours() && g_opt.missions) {
    ++g_ms.calls[0];
    const int team = static_cast<int>(SehArgNumber(L, 1));
    const int type = static_cast<int>(SehArgNumber(L, 2));
    const std::uint32_t target = SehArgId(L, 3);
    const int agents = static_cast<int>(SehArgNumber(L, 4));
    const float timeout = SehArgNumber(L, 5);
    const std::uint32_t guid = SehArgId(L, 6);
    const bool hasDest = SehArgBool(L, 7);
    const gb::Vec3 dest = gb::MakeVec(SehArgNumber(L, 8), SehArgNumber(L, 9), SehArgNumber(L, 10));
    const std::size_t before = g_state.missions.Count();
    id = g_state.missions.Request(team, type, target, agents, timeout, guid, hasDest ? &dest : nullptr, MissionNow());
    if (g_state.missions.Count() > before) {
      ++g_ms.opened;
      if (type >= 0 && type < 14) ++g_ms.byType[type];
      if (const gb::Mission* m = g_state.missions.Find(id))
        if (MissionLogDue(0, type))
          Log("bots: mission %s requested at %.1f s (%u of this type)", DescribeMission(*m).c_str(), MissionNow(),
              g_missionLogs[0][type >= 0 && type < 14 ? type : 0]);
    }
  }
  SehPushNumber(L, static_cast<float>(id));
  return 1;
}

// nBotHost.IsMissionAvailable(team, type) -> bool
int __cdecl LuaIsMissionAvailable(void* L) {
  bool r = false;
  if (Ours() && g_opt.missions) {
    ++g_ms.calls[1];
    r = g_state.missions.IsAvailable(static_cast<int>(SehArgNumber(L, 1)), static_cast<int>(SehArgNumber(L, 2)));
  }
  SehPushBoolean(L, r);
  return 1;
}

// nBotHost.GetAvailableMission(bot, type, range) -> id, 0 = none
int __cdecl LuaGetAvailableMission(void* L) {
  std::uint32_t id = 0;
  if (Ours() && g_opt.missions) {
    ++g_ms.calls[2];
    const std::uint32_t bot = SehArgId(L, 1);
    int team = 0;
    gb::Vec3 pos;
    if (SehObjTeamPos(bot, &team, &pos))
      id = g_state.missions.FindAvailable(bot, team, static_cast<int>(SehArgNumber(L, 2)), pos, SehArgNumber(L, 3),
                                          [](std::uint32_t o, gb::Vec3* p) { return SehObjTeamPos(o, nullptr, p); });
  }
  SehPushNumber(L, static_cast<float>(id));
  return 1;
}

// nBotHost.AcceptMission(bot, id) -> bool (only the unreferenced ability_Bot_AcceptMission calls it)
int __cdecl LuaAcceptMission(void* L) {
  bool r = false;
  if (Ours() && g_opt.missions) {
    ++g_ms.calls[3];
    const std::uint32_t bot = SehArgId(L, 1);
    int team = 0;
    if (SehObjTeamPos(bot, &team, nullptr))
      r = g_state.missions.Accept(bot, team, static_cast<std::uint32_t>(SehArgNumber(L, 2)));
  }
  SehPushBoolean(L, r);
  return 1;
}

// nBotHost.GetMissionID(bot) -> id, 0 = none
int __cdecl LuaGetMissionID(void* L) {
  std::uint32_t id = 0;
  if (Ours() && g_opt.missions) {
    ++g_ms.calls[4];
    id = g_state.missions.MissionOf(SehArgId(L, 1));
  }
  SehPushNumber(L, static_cast<float>(id));
  return 1;
}

// nBotHost.GetMissionTarget(bot) -> object (kObjIDNone = light userdata 0)
int __cdecl LuaGetMissionTarget(void* L) {
  std::uint32_t t = 0;
  if (Ours() && g_opt.missions) {
    ++g_ms.calls[5];
    t = g_state.missions.TargetOf(SehArgId(L, 1));
  }
  SehPushId(L, t);
  return 1;
}

// nBotHost.GetMissionDestination(bot) -> x, y, z (0, 0, 0 = none)
int __cdecl LuaGetMissionDestination(void* L) {
  gb::Vec3 d;
  if (Ours() && g_opt.missions) {
    ++g_ms.calls[6];
    d = g_state.missions.DestinationOf(SehArgId(L, 1));
  }
  SehPushNumber(L, d.x);
  SehPushNumber(L, d.y);
  SehPushNumber(L, d.z);
  return 3;
}

// nBotHost.OpenMission(team, i) -> id, target, conditionGUID for the i-th (1-based) open mission of the team, or nothing
int __cdecl LuaOpenMission(void* L) {
  if (!Ours() || !g_opt.missions) return 0;
  ++g_ms.calls[7];
  const std::vector<const gb::Mission*> open = g_state.missions.Open(static_cast<int>(SehArgNumber(L, 1)));
  const int i = static_cast<int>(SehArgNumber(L, 2));
  if (i < 1 || i > static_cast<int>(open.size())) return 0;
  const gb::Mission& m = *open[static_cast<std::size_t>(i - 1)];
  SehPushNumber(L, static_cast<float>(m.id));
  SehPushId(L, m.target);
  SehPushId(L, m.condition);
  return 3;
}

// nBotHost.EndMission(id) -> bool (the driver: the mission condition's CheckEnd returned true)
int __cdecl LuaEndMission(void* L) {
  bool r = false;
  if (Ours() && g_opt.missions) {
    ++g_ms.calls[8];
    const std::uint32_t id = static_cast<std::uint32_t>(SehArgNumber(L, 1));
    std::string what;
    int type = 0;
    if (const gb::Mission* m = g_state.missions.Find(id)) {
      what = DescribeMission(*m);
      type = m->type;
    }
    r = g_state.missions.End(id);
    if (r) {
      ++g_ms.endedByCheck;
      if (MissionLogDue(1, type)) Log("bots: mission %s ended by its CheckEnd at %.1f s", what.c_str(), MissionNow());
    }
  }
  SehPushBoolean(L, r);
  return 1;
}

// ---- shopping ----

gb::ItemCatalog g_catalog;
std::set<std::uint32_t> g_catalogRead;  // item asset ids read into the catalog (or found unreadable)
struct BotShop {
  bool read = false;
  std::string buildName;
  std::vector<std::uint32_t> build;  // the build's items in order (asset ids)
  std::uint32_t potion = 0;          // the build's consumable (the Vitality Potion)
  int potionCost = 0;
  unsigned bought = 0, potions = 0;
  // DESECBOT-2: a profile with its own build (gms/core/bots/guidebuild.h) shops by it instead of `build`
  std::shared_ptr<gb::GuidePlanner> guide;
  gb::BuildMemory memory;
};
std::map<int, BotShop> g_shops;  // by gameplay index

struct RawItem {
  std::uint32_t id;
  int category, stack, cost;
  bool consumable;
  void* from[8];
  int numFrom;
};

bool SehReadItem(void* asset, RawItem* r) {
  __try {
    std::memset(r, 0, sizeof *r);
    char* d = asset ? Field<char*>(asset, asset::kData) : nullptr;
    if (!d) return false;
    r->id = Field<std::uint32_t>(asset, asset::kId);
    r->category = Field<int>(d, itemdata::kCategory);
    r->stack = Field<unsigned char>(d, itemdata::kStackCount);
    r->cost = static_cast<int>(Field<std::uint32_t>(d, itemdata::kCost));
    r->consumable = Field<unsigned char>(d, itemdata::kConsumable) != 0;
    void** from = Field<void**>(d, itemdata::kUpgradesFrom);
    const int n = Field<int>(d, itemdata::kUpgradesFrom + 4);
    for (int i = 0; i < n && i < 8 && from; ++i) r->from[r->numFrom++] = from[i];
    return true;
  } __except (SehNote(GetExceptionInformation(), "read item")) {
    return false;
  }
}

// The item and (recursively) its components into the catalog; returns its id (0 if unreadable).
std::uint32_t AddItem(void* asset, int depth = 0) {
  RawItem r;
  if (!asset || depth > 8 || !SehReadItem(asset, &r)) return 0;
  if (g_catalogRead.count(r.id)) return r.id;
  g_catalogRead.insert(r.id);
  gb::ItemDef d;
  d.id = r.id;
  d.cost = r.cost;
  d.consumable = r.consumable || r.category == itemdata::kCategoryConsumable;
  d.stack = r.stack > 0 ? r.stack : 1;
  for (int i = 0; i < r.numFrom; ++i)
    if (const std::uint32_t c = AddItem(r.from[i], depth + 1)) d.upgradesFrom.push_back(c);
  g_catalog.Add(d);
  return r.id;
}

// The ItemBuild asset's items in category order (Starting, Core, Offensive, Defensive).
bool SehBuildItems(void* buildAsset, std::vector<void*>* items) {
  __try {
    char* d = buildAsset ? Field<char*>(buildAsset, asset::kData) : nullptr;
    if (!d) return false;
    char* cats = Field<char*>(d, 0);
    const int nc = Field<int>(d, 4);
    for (int c = 0; c < nc && c < 8 && cats; ++c) {
      char* cat = cats + c * kBuildCategorySize;
      void** it = Field<void**>(cat, 0);
      const int ni = Field<int>(cat, 4);
      for (int i = 0; i < ni && i < 16 && it; ++i)
        if (it[i]) items->push_back(it[i]);
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "read item build")) {
    return false;
  }
}

// "PC_<Shaper>".Noun's recommendedItems[0] (the BotClass's namesake noun).
void* SehRecommendedBuild(void* noun) {
  __try {
    char* d = noun ? Field<char*>(noun, asset::kData) : nullptr;
    if (!d) return nullptr;
    void** builds = Field<void**>(d, kNounRecommendedItems);
    const int n = Field<int>(d, kNounRecommendedItems + 4);
    return n > 0 && builds ? builds[0] : nullptr;
  } __except (SehNote(GetExceptionInformation(), "recommendedItems")) {
    return nullptr;
  }
}

// DESECBOT-2: the profile's own build, its names resolved like the ItemBuild's items (an item asset each, read into
// the catalog with its components); false when none resolves (the bot shops by its ItemBuild then).
bool ShopOfGuide(const Bot& b, const gb::GuideBuildDef& def, BotShop* s) {
  std::string missing;
  auto idOf = [&missing](const std::string& name) -> std::uint32_t {
    const std::uint32_t id = AddItem(SehAsset(gms::AssetIdOf(name + ".Item")));
    if (!id) missing += (missing.empty() ? "" : ", ") + name;
    return id;
  };
  std::vector<gb::BuildEntry> entries = gb::GuidePlanner::Resolve(def, idOf);
  std::string list;
  std::size_t items = 0;
  for (const gb::BuildEntry& e : entries) {
    std::string opts;
    for (const std::vector<std::uint32_t>& option : e.options) {
      std::string names;
      for (std::uint32_t id : option) {
        const gb::ItemDef* d = g_catalog.Find(id);
        if (d && d->consumable && !s->potion) {
          s->potion = id;
          s->potionCost = d->cost;
        }
        names += std::string(names.empty() ? "" : " + ") + SehAssetName(SehAsset(id));
        ++items;
      }
      opts += std::string(opts.empty() ? "" : " | ") + (names.empty() ? "-" : names);
    }
    const char* rule = e.rule == gb::BuildRule::kWhenAhead     ? " [ahead]"
                       : e.rule == gb::BuildRule::kMagicAllies ? " [magic allies]"
                                                               : "";
    list += std::string(list.empty() ? "" : "; ") + e.label + rule + (e.notBeforeS > 0 ? " [not before]" : "") + ": " +
            opts;
  }
  if (!items) {
    Log("bots: player %d (%s): guide build %s: no item resolves (missing %s); its ItemBuild instead", b.index,
        b.botClass.c_str(), def.source, missing.c_str());
    return false;
  }
  s->guide = std::make_shared<gb::GuidePlanner>(&g_catalog, std::move(entries));
  s->buildName = std::string("guide: ") + def.source;
  s->build = s->guide->Plan(gb::Inventory{}, gb::ShopContext{}, gb::BuildMemory{}).items;  // the default order
  if (!s->potion) {
    void* p = SehAsset(gms::AssetIdOf("Consumable_Potion_HealingPotion.Item"));
    if (const std::uint32_t id = AddItem(p)) {
      s->potion = id;
      s->potionCost = g_catalog.Find(id) ? g_catalog.Find(id)->cost : 0;
    }
  }
  ++g_ss.guideBots;
  Log("bots: player %d (%s): guide build %s (%zu entries, %zu items: %s)%s%s, potion %08X at %d gold; catalog %zu items",
      b.index, b.botClass.c_str(), def.source, s->guide->Entries().size(), items, list.c_str(),
      missing.empty() ? "" : "; missing ", missing.c_str(), s->potion, s->potionCost, g_catalogRead.size());
  return true;
}

// DESECBOT-2: what the guide build's rules look at, read at the shop: the bot's level, kills and deaths, the enemies'
// average level, its allies' damage styles (PlayerClass ShaperStyle_DamageStyle through mpNounDef) and roles (mRole).
struct PlayerFacts {
  int team = 0, level = 0, kills = 0, deaths = 0, role = -1;
  void* noun = nullptr;
};

bool SehPlayerFacts(void* sim, int index, PlayerFacts* f) {
  __try {
    void* p = GetPlayerByIndex(sim, static_cast<unsigned>(index));
    if (!p) return false;
    f->team = Field<unsigned char>(p, player::kTeam);
    f->level = static_cast<int>(Field<unsigned>(p, player::kLevel));
    f->kills = static_cast<int>(Field<unsigned>(p, player::kKills));
    f->deaths = static_cast<int>(Field<unsigned>(p, player::kDeaths));
    f->role = Field<unsigned char>(p, player::kRole);
    f->noun = Field<void*>(p, player::kNounDef);
    return true;
  } __except (SehNote(GetExceptionInformation(), "player facts")) {
    return false;
  }
}

gb::ShopContext ShopContextOf(const Bot& b) {
  gb::ShopContext c;
  c.now = MissionNow();
  PlayerFacts self;
  if (!SehPlayerFacts(g_sim, b.index, &self)) return c;
  c.level = self.level;
  c.kills = self.kills;
  c.deaths = self.deaths;
  int enemies = 0, enemyLevels = 0;
  for (int i = 0; i < 10; ++i) {
    PlayerFacts f;
    if (i == b.index || !SehPlayerFacts(g_sim, i, &f) || (f.team != 1 && f.team != 2)) continue;
    if (f.team != self.team) {
      ++enemies;
      enemyLevels += f.level;
      continue;
    }
    int v[3];
    if (!f.noun || !SehShaperValues(f.noun, v)) continue;
    if (v[2] == 1) ++c.magicAllies;                                // ShaperStyle_DamageStyle magical
    if (f.role == 0 && v[2] == 0) c.physicalGladiatorAlly = true;  // a physical Gladiator
  }
  c.enemyLevel = enemies ? static_cast<double>(enemyLevels) / enemies : 0.0;
  return c;
}

// The next step of the bot's build: its guide build's (with the entry it serves, the plan and the context) or its
// ItemBuild's.
bool NextStep(const Bot& b, const BotShop& s, const gb::Inventory& inv, gb::Purchase* p, int* entry,
              gb::ResolvedBuild* plan, gb::ShopContext* ctx) {
  if (entry) *entry = -1;
  if (!s.guide) return gb::ShopPlanner(&g_catalog, s.build).Next(inv, p);
  *ctx = ShopContextOf(b);
  return s.guide->Next(inv, *ctx, s.memory, p, entry, plan);
}

BotShop& ShopOf(const Bot& b) {
  BotShop& s = g_shops[b.index];
  if (s.read) return s;
  s.read = true;
  std::string base = b.botClass;
  const std::size_t dot = base.find('.');
  if (dot != std::string::npos) base.resize(dot);
  base = gb::BaseClass(base);  // BOTPROFILE: a profile shops as the shaper it copies
  if (const gb::GuideBuildDef* def = g_opt.guideBuilds ? gb::GuideBuildFor(b.botClass) : nullptr) {
    if (ShopOfGuide(b, *def, &s)) return s;
  }
  void* build = SehRecommendedBuild(SehAsset(gms::AssetIdOf(base + ".Noun")));
  if (!build && base.size() > 3 && base.compare(0, 3, "PC_") == 0)
    build = SehAsset(gms::AssetIdOf("RecommendedItemBuild_" + base.substr(3) + ".ItemBuild"));
  std::vector<void*> items;
  if (build) SehBuildItems(build, &items);
  s.buildName = build ? SehAssetName(build) : "none";
  std::string list;
  for (void* a : items) {
    const std::uint32_t id = AddItem(a);
    if (!id) continue;
    const gb::ItemDef* d = g_catalog.Find(id);
    if (d && d->consumable && !s.potion) {
      s.potion = id;
      s.potionCost = d->cost;
    }
    s.build.push_back(id);
    list += std::string(list.empty() ? "" : ", ") + SehAssetName(a);
  }
  if (!s.potion) {  // a build without one: the Vitality Potion
    void* p = SehAsset(gms::AssetIdOf("Consumable_Potion_HealingPotion.Item"));
    if (const std::uint32_t id = AddItem(p)) {
      s.potion = id;
      s.potionCost = g_catalog.Find(id) ? g_catalog.Find(id)->cost : 0;
    }
  }
  if (s.build.empty()) ++g_ss.noBuild;
  Log("bots: player %d (%s): build %s (%zu items: %s), potion %08X at %d gold; catalog %zu items", b.index,
      b.botClass.c_str(), s.buildName.c_str(), s.build.size(), list.c_str(), s.potion, s.potionCost,
      g_catalogRead.size());
  return s;
}

struct Purse {
  std::uint32_t items[player::kItemSlots] = {};
  int counts[player::kItemSlots] = {};
  float gold = 0;
};

bool SehPurse(void* sim, int index, Purse* p) {
  __try {
    void* pl = GetPlayerByIndex(sim, static_cast<unsigned>(index));
    if (!pl) return false;
    p->gold = Field<float>(pl, player::kAvailableGold);
    for (unsigned s = 0; s < player::kItemSlots; ++s) {
      char* it = static_cast<char*>(pl) + player::kItems + s * player::kItemSize;
      void* a = Field<void*>(it, 0);
      p->items[s] = a ? Field<std::uint32_t>(a, asset::kId) : 0;
      p->counts[s] = Field<int>(it, player::kItemCount);
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "bot purse")) {
    return false;
  }
}

// A bot's shop, inventory and gold; false without them.
bool ShopViewOf(const Bot& b, BotShop** shop, gb::Inventory* inv, float* gold) {
  *shop = &ShopOf(b);
  Purse p;
  if (!SehPurse(g_sim, b.index, &p)) return false;
  *inv = gb::InventoryFromSlots(p.items, p.counts, static_cast<int>(player::kItemSlots), (*shop)->potion);
  *gold = p.gold;
  return true;
}

// The bot of a creature argument, its shop, inventory and gold; false without them.
bool ShopView(void* L, const Bot** bot, BotShop** shop, gb::Inventory* inv, float* gold) {
  if (!Ours() || !g_opt.shopping) return false;
  *bot = BotByCreature(SehArgId(L, 1));
  if (!*bot) return false;
  return ShopViewOf(**bot, shop, inv, gold);
}

// LabsPlayer_CanUseItemShop: dead, or alive within the shop's reach (D7b). ability_Bot_Buy's gambit checks it at its
// start (condition_Bot_CanShop) and its Tick runs on while the bot walks off (d6b_run1: refusals at 52.67 units).
bool SehCanUseShop(void* sim, int index) {
  __try {
    void* pl = GetPlayerByIndex(sim, static_cast<unsigned>(index));
    return pl && CanUseItemShop(pl);
  } __except (SehNote(GetExceptionInformation(), "LabsPlayer_CanUseItemShop")) {
    return false;
  }
}

// A purchase through D7b's item transaction path, as if the bot's client had sent it: true if the gold went down.
// Not tried away from the shop (counted).
bool Buy(const Bot& b, std::uint32_t item, const char* why, bool guide = false) {
  Purse before, after;
  if (!SehPurse(g_sim, b.index, &before)) return false;
  if (!SehCanUseShop(g_sim, b.index)) {
    ++g_ss.awayFromShop;
    return false;
  }
  gms::ItemTransactionRequest r;
  r.type = gms::kItemPurchase;
  r.item = item;
  r.slot = gms::kItemSlotNone;
  r.item2 = 0;
  r.slot2 = 0xFF;
  const gms::Bytes m = gms::BuildItemTransaction(r);
  cmds::OnClientMessage(g_sim, static_cast<unsigned>(b.index), m[0], m.data() + 1, m.size() - 1, "bot");
  const bool ok = SehPurse(g_sim, b.index, &after) && after.gold < before.gold;
  if (ok) g_ss.spent += before.gold - after.gold;
  else ++g_ss.refused;
  // DESECBOT-2: a guide build's purchases have their own budget (the first 400, then every 10th); BOTSHOP-PASS: so do
  // the others (the bot-session log scan's shop check reads these lines; the shared bot budget may be spent by then)
  const bool logIt = guide ? (++g_ss.guideLogged <= 400 || g_ss.guideLogged % 10 == 0)
                           : (++g_ss.buyLogged <= 400 || g_ss.buyLogged % 10 == 0);
  if (logIt)
    Log("bots: player %d (%s) buys %08X (%s) for its %s: %s; gold %.0f -> %.0f at %.1f s", b.index, b.botClass.c_str(),
        item, SehAssetName(SehAsset(item)), why, ok ? "done" : "REFUSED", before.gold, after.gold, MissionNow());
  return ok;
}

// nBotHost.CanAffordNextItem(bot) -> bool
int __cdecl LuaCanAffordNextItem(void* L) {
  bool r = false;
  const Bot* b = nullptr;
  BotShop* s = nullptr;
  gb::Inventory inv;
  float gold = 0;
  if (ShopView(L, &b, &s, &inv, &gold)) {
    ++g_ss.calls[0];
    gb::Purchase p;
    gb::ShopContext ctx;
    r = NextStep(*b, *s, inv, &p, nullptr, nullptr, &ctx) && static_cast<float>(p.price) <= gold;
  }
  SehPushBoolean(L, r);
  return 1;
}

// The next step of the bot's build when its gold pays for it: true if bought. `via` ends the purchase line's reason
// ("" for ability_Bot_Buy's own call; BOTSHOP-PASS: " (the host's shop visit)").
bool PurchaseNext(const Bot& b, BotShop& s, const gb::Inventory& inv, float gold, const char* via) {
  gb::Purchase p;
  int entry = -1;
  gb::ResolvedBuild plan;
  gb::ShopContext ctx;
  if (!NextStep(b, s, inv, &p, &entry, &plan, &ctx) || static_cast<float>(p.price) > gold) return false;
  char why[224];
  const gb::BuildEntry* e = s.guide && entry >= 0 ? &s.guide->Entries()[static_cast<std::size_t>(entry)] : nullptr;
  const std::size_t option = e ? static_cast<std::size_t>(plan.optionOf[static_cast<std::size_t>(entry)]) : 0;
  if (e)
    std::snprintf(why, sizeof why, "guide build, step %d %s: %s, price %d%s%s", entry, e->label.c_str(),
                  e->optionLabels[option].c_str(), p.price, p.replaces ? " (an upgrade)" : "", via);
  else
    std::snprintf(why, sizeof why, "build, price %d%s%s", p.price, p.replaces ? " (an upgrade)" : "", via);
  const bool r = Buy(b, p.item, why, e != nullptr);
  if (r) {
    ++s.bought;
    ++g_ss.bought;
  }
  if (r && e) {  // DESECBOT-2: the choice is kept from its first purchase on (and a deferral once its turn came)
    ++g_ss.guideBought;
    const std::set<int> deferredBefore = s.memory.deferred;
    if (s.guide->Commit(plan, entry, &s.memory)) {
      ++g_ss.guideChoices;
      Log("bots: player %d (%s) guide choice %s: %s (%s) at %.1f s", b.index, b.botClass.c_str(), e->label.c_str(),
          e->optionLabels[option].c_str(), plan.whyOf[static_cast<std::size_t>(entry)].c_str(), ctx.now);
    }
    for (int d : s.memory.deferred)
      if (!deferredBefore.count(d)) {
        ++g_ss.guideDeferred;
        const gb::BuildEntry& de = s.guide->Entries()[static_cast<std::size_t>(d)];
        Log("bots: player %d (%s) guide: %s deferred (its turn came before %.0f s), %s first, at %.1f s", b.index,
            b.botClass.c_str(), de.label.c_str(), de.notBeforeS, e->label.c_str(), ctx.now);
      }
  }
  return r;
}

// Potions up to `bot_potions` carried, as many as the gold pays for: how many were bought.
int PurchasePotionsFor(const Bot& b, BotShop& s, const gb::Inventory& inv, float gold, const char* why) {
  const int want = s.potion ? gb::ShopPlanner(&g_catalog, s.build).PotionsToBuy(inv, gold, s.potionCost, g_opt.potions) : 0;
  int n = 0;
  for (int i = 0; i < want; ++i) {
    if (!Buy(b, s.potion, why)) break;
    ++n;
    ++s.potions;
    ++g_ss.potions;
  }
  return n;
}

// nBotHost.PurchaseNextItem(bot) -> bool
int __cdecl LuaPurchaseNextItem(void* L) {
  bool r = false;
  const Bot* b = nullptr;
  BotShop* s = nullptr;
  gb::Inventory inv;
  float gold = 0;
  if (ShopView(L, &b, &s, &inv, &gold)) {
    ++g_ss.calls[1];
    r = PurchaseNext(*b, *s, inv, gold, "");
  }
  SehPushBoolean(L, r);
  return 1;
}

// nBotHost.CanBuyPotion(bot) -> bool
int __cdecl LuaCanBuyPotion(void* L) {
  bool r = false;
  const Bot* b = nullptr;
  BotShop* s = nullptr;
  gb::Inventory inv;
  float gold = 0;
  if (ShopView(L, &b, &s, &inv, &gold)) {
    ++g_ss.calls[2];
    r = s->potion && gb::ShopPlanner(&g_catalog, s->build).PotionsToBuy(inv, gold, s->potionCost, g_opt.potions) > 0;
  }
  SehPushBoolean(L, r);
  return 1;
}

// nBotHost.PurchasePotions(bot) -> how many were bought
int __cdecl LuaPurchasePotions(void* L) {
  int n = 0;
  const Bot* b = nullptr;
  BotShop* s = nullptr;
  gb::Inventory inv;
  float gold = 0;
  if (ShopView(L, &b, &s, &inv, &gold)) {
    ++g_ss.calls[3];
    n = PurchasePotionsFor(*b, *s, inv, gold, "potions");
  }
  SehPushNumber(L, static_cast<float>(n));
  return 1;
}

// BOTSHOP-PASS (gms/core/bots/shopping.h ShopVisit): whether the bot's creature is alive within the shop's reach
// (LabsPlayer_InProximityToItemShop; LabsPlayer_CanUseItemShop also says yes for a dead one, who shops at its respawn).
bool SehAliveInShopReach(void* sim, const Bot& b) {
  __try {
    void* o = b.creature ? GetObjectById(b.creature) : nullptr;
    if (!o) return false;
    void* c = Field<void*>(o, combatant::kOfObject);
    if (!c || Field<float>(c, combatant::kHitPoints) <= 0.f) return false;
    void* pl = GetPlayerByIndex(sim, static_cast<unsigned>(b.index));
    return pl && InProximityToItemShop(pl) && CanUseItemShop(pl);
  } __except (SehNote(GetExceptionInformation(), "bot shop reach")) {
    return false;
  }
}

std::map<int, gb::ShopVisit> g_visits;  // BOTSHOP-PASS: by gameplay index
unsigned long long g_nextVisitMs = 0;

// BOTSHOP-PASS: the host's shop visit for every bot that stays in the shop's reach while its plan buys nothing (a
// plan link taken on arrival skips the BackToAction phase's ability_Bot_Buy): its build's steps as far as its gold
// goes, then its potions, through the same purchase path; checked every 250 ms.
void ShopVisits(unsigned long long now) {
  if (!g_opt.shopping || !g_opt.shopVisit || now < g_nextVisitMs) return;
  g_nextVisitMs = now + 250;
  for (const Bot& b : g_bots) {
    if (!b.creature) continue;
    gb::ShopVisit& v = g_visits[b.index];
    if (!v.Due(SehAliveInShopReach(g_sim, b), now, static_cast<unsigned long long>(g_opt.shopVisitMs),
               static_cast<unsigned long long>(g_opt.shopVisitRepeatMs)))
      continue;
    BotShop* s = nullptr;
    gb::Inventory inv;
    float gold = 0;
    int items = 0, potions = 0;
    Purse before, after;
    if (!SehPurse(g_sim, b.index, &before)) continue;
    for (int i = 0; i < 8 && ShopViewOf(b, &s, &inv, &gold); ++i) {  // a step at a time: the slots change
      if (!PurchaseNext(b, *s, inv, gold, " (the host's shop visit)")) break;
      ++items;
    }
    if (ShopViewOf(b, &s, &inv, &gold)) potions = PurchasePotionsFor(b, *s, inv, gold, "potions (the host's shop visit)");
    if (!items && !potions) continue;
    ++g_ss.visits;
    g_ss.visitItems += static_cast<unsigned>(items);
    g_ss.visitPotions += static_cast<unsigned>(potions);
    const float first = before.gold;
    const float left = SehPurse(g_sim, b.index, &after) ? after.gold : 0.f;
    Log("bots: player %d (%s) shop visit (BOTSHOP-PASS): %.1f s in the shop's reach without its plan's purchase: "
        "bought %d item(s) and %d potion(s), gold %.0f -> %.0f at %.1f s",
        b.index, b.botClass.c_str(), static_cast<double>(now - v.Since()) / 1000.0, items, potions, first, left,
        MissionNow());
  }
}

// ---- the fire tracker ----

void* SehDirector(void* sim) {
  __try {
    return Field<void*>(sim, kSimBotDirector);
  } __except (SehNote(GetExceptionInformation(), "bot director")) {
    return nullptr;
  }
}

bool SehFireTrackerOn(void* director) {
  __try {
    Field<unsigned char>(director, kBotDirectorFireTrackerOn) = 1;
    return Field<unsigned char>(director, kBotDirectorFireTrackerOn) == 1;
  } __except (SehNote(GetExceptionInformation(), "fire tracker on")) {
    return false;
  }
}

bool SehIsPositionSafe(void* director, int team, const float* xy, bool* safe) {
  __try {
    *safe = FireTrackerIsPositionSafe(static_cast<char*>(director) + kBotDirectorFireTracker, team, xy);
    return true;
  } __except (SehNote(GetExceptionInformation(), "FireTracker_IsPositionSafe")) {
    return false;
  }
}

// FIREZONE: replaces the fire zone 0x00877870 (thiscall, 6 stack arguments: __fastcall with edx unused pops the same
// 0x18 bytes). Its callers mark the tracker's cells and request the dodge themselves; only the nav graph's penalty box
// and its timed list entry are left out.
void __fastcall NoFireZone(void* /*tracker*/, void* /*edx*/, void*, void*, void*, void*, void*, void*) {
  ++g_fs.zonesSkipped;
}

void StubFireZones() {
  if (g_opt.fireZones || g_fs.zonesStubbed) return;
  g_fs.zonesStubbed = HookReplace(kFireTrackerAddFireZone, kFireTrackerAddFireZoneBytes,
                                  sizeof kFireTrackerAddFireZoneBytes, reinterpret_cast<void*>(&NoFireZone));
  Log("bots: FIREZONE: the fire tracker's NavPower fire zones (00877870) %s",
      g_fs.zonesStubbed ? "replaced by a stub (no BotObstacle_FireZone cut into the nav graph; [host] bot_fire_zones=1 "
                          "keeps them)"
                        : "NOT replaced (other bytes): the game's code runs");
}

void EnableFireTracker(void* sim) {
  if (g_fs.on || !g_opt.fireTracker) return;
  if (!CodeMatches(0x00876900, kFireTrackerIsSafeBytes, sizeof kFireTrackerIsSafeBytes)) {
    Log("bots: FireTracker_IsPositionSafe at 00876900 has other bytes; the fire tracker stays off");
    return;
  }
  StubFireZones();
  void* director = SehDirector(sim);
  g_fs.on = director && SehFireTrackerOn(director);
  Log("bots: the bot director %p's fire tracker (+0x978) %s: the Guardian's and the parasite's area abilities mark their "
      "cells (nDirector.NotifyFireTrackerOfAbility), bots in them get ability_Bot_AvoidFireNP, nBotHost.IsPositionSafe "
      "answers",
      director, g_fs.on ? "switched on" : "NOT switched on (no director)");
}

// nBotHost.IsPositionSafe(team, x, y, z) -> bool (true while the tracker is off, for a team other than 1/2 and off the
// tracker's grid)
int __cdecl LuaIsPositionSafe(void* L) {
  bool safe = true;
  if (Ours() && g_fs.on) {
    ++g_fs.calls;
    const int team = static_cast<int>(SehArgNumber(L, 1));
    const float xy[3] = {SehArgNumber(L, 2), SehArgNumber(L, 3), SehArgNumber(L, 4)};
    void* director = SehDirector(g_sim);
    if (team < 1 || team > 2 || xy[0] <= -299.f || xy[0] >= 299.f || xy[1] <= -299.f || xy[1] >= 299.f || !director) {
      ++g_fs.offGrid;
    } else if (SehIsPositionSafe(director, team, xy, &safe) && !safe) {
      ++g_fs.unsafe;
      if (LogBudget()) Log("bots: IsPositionSafe(team %d, %.1f, %.1f): under fire", team, xy[0], xy[1]);
    }
  }
  SehPushBoolean(L, safe);
  return 1;
}

// ---- BOTTEAM: the team layer's host functions ----

// nBotHost.RespawnIn(playerIndex) -> seconds until the player's respawn (mRespawnTime - now, D1g), 0 when alive or unknown.
float SehRespawnIn(void* sim, int index) {
  __try {
    void* pl = GetPlayerByIndex(sim, static_cast<unsigned>(index));
    if (!pl) return 0.f;
    const unsigned long long respawn = Field<unsigned long long>(pl, player::kRespawnTime);
    const unsigned long long now = GetTimeMs(sim);
    return respawn > now ? static_cast<float>(respawn - now) / 1000.f : 0.f;
  } __except (SehNote(GetExceptionInformation(), "mRespawnTime")) {
    return 0.f;
  }
}
int __cdecl LuaRespawnIn(void* L) {
  float s = 0.f;
  if (Ours()) {
    const int index = static_cast<int>(SehArgNumber(L, 1));
    if (index >= 0 && index < gb::kMaxPlayerIndexes) s = SehRespawnIn(g_sim, index);
  }
  SehPushNumber(L, s);
  return 1;
}

// nBotHost.TeamPing(creature, target, x, y, z, pingType, why) -> bool: a bot's minimap ping to its team (BOTTEAM's calls:
// the human teammates see where the bots go). Only for a creature of one of our bots.
unsigned g_teamPings = 0;
int __cdecl LuaTeamPing(void* L) {
  bool sent = false;
  if (Ours() && g_opt.teamPings) {
    const std::uint32_t creature = SehArgId(L, 1);
    const Bot* b = BotByCreature(creature);
    if (b) {
      const float pos[3] = {SehArgNumber(L, 3), SehArgNumber(L, 4), SehArgNumber(L, 5)};
      const int type = static_cast<int>(SehArgNumber(L, 6));
      const char* why = nullptr;
      __try {
        why = LuaToString(L, 7, nullptr);
      } __except (SehNote(GetExceptionInformation(), "lua_tolstring")) {
      }
      sent = cmds::SendTeamPing(g_sim, static_cast<unsigned>(b->team), creature, SehArgId(L, 2), pos,
                                static_cast<std::uint8_t>(type >= 0 && type <= 4 ? type : 0), why) != 0;
      if (sent) ++g_teamPings;
    }
  }
  SehPushBoolean(L, sent);
  return 1;
}

// nBotHost.Log(text): one "bots: team:" line in the host log (the team layer's calls; budgeted like the other bot lines,
// but a call change is rare: a few per minute).
unsigned g_teamLogLines = 0;
int __cdecl LuaLog(void* L) {
  if (Ours()) {
    const char* text = nullptr;
    __try {
      text = LuaToString(L, 1, nullptr);
    } __except (SehNote(GetExceptionInformation(), "lua_tolstring")) {
    }
    ++g_teamLogLines;
    if (text && (g_teamLogLines <= 2000 || g_teamLogLines % 100 == 0))
      Log("bots: team: %s (match %.1f s)", text, MissionNow());
  }
  return 0;
}

// ---- KILLFINISH ----
// nBotHost.KillFinish(bot) -> bool: the host would keep this bot in a fight with its target now (agents.cpp's rule; the
// Lua's KillShaper join and EngageForKill checks ask before refusing an engage that BailOnKill would end at once).
int __cdecl LuaKillFinish(void* L) {
  bool r = false;
  if (Ours()) r = agents::KillFinishWouldHold(SehArgId(L, 1));
  SehPushBoolean(L, r);
  return 1;
}

// nBotHost.EngageBlocked(bot) -> bool: BOTCYCLE, the bot left its last engage at once; its next engages wait.
int __cdecl LuaEngageBlocked(void* L) {
  bool r = false;
  if (Ours()) r = agents::KillFinishEngageBlocked(SehArgId(L, 1));
  SehPushBoolean(L, r);
  return 1;
}

// nBotHost.BailWhy(bot, why): the reason the Lua's BailOnKill gave for leaving (for the host's disengage lines).
int __cdecl LuaBailWhy(void* L) {
  if (!Ours()) return 0;
  const char* why = nullptr;
  __try {
    why = LuaToString(L, 2, nullptr);
  } __except (SehNote(GetExceptionInformation(), "lua_tolstring")) {
  }
  if (why) agents::NoteBailWhy(SehArgId(L, 1), why);
  return 0;
}

// ---- BOTJUNGLE: the humans' pings ----
// Every human minimap ping the command intake relays (commands.cpp, kCmdPing) is kept here, the last kHumanPings;
// nBotHost.HumanPing(team, afterSeq) -> seq, pinger, target, x, y, z, type, match time of the team's first ping after
// `afterSeq` (nothing when there is none). The team layer polls it once a second (dgsim_bots_api.lua, *BOTJUNGLE*).
struct HumanPingRec {
  unsigned seq = 0;
  int team = 0;
  std::uint32_t pinger = 0, target = 0;
  float pos[3] = {};
  int type = 0;
  double t = 0.0;
};
constexpr std::size_t kHumanPings = 32;
HumanPingRec g_humanPings[kHumanPings];
unsigned g_humanPingSeq = 0;
unsigned g_humanPingsRead = 0;

int __cdecl LuaHumanPing(void* L) {
  if (!Ours() || !g_opt.pingFollow) return 0;
  const int team = static_cast<int>(SehArgNumber(L, 1));
  const unsigned after = static_cast<unsigned>(SehArgNumber(L, 2));
  const HumanPingRec* best = nullptr;
  for (const HumanPingRec& p : g_humanPings)
    if (p.seq > after && p.team == team && (!best || p.seq < best->seq)) best = &p;
  if (!best) return 0;
  ++g_humanPingsRead;
  SehPushNumber(L, static_cast<float>(best->seq));
  SehPushId(L, best->pinger);
  SehPushId(L, best->target);
  SehPushNumber(L, best->pos[0]);
  SehPushNumber(L, best->pos[1]);
  SehPushNumber(L, best->pos[2]);
  SehPushNumber(L, static_cast<float>(best->type));
  SehPushNumber(L, static_cast<float>(best->t));
  return 8;
}

const LuaReg kBotHostRegs[] = {
    {"AssignLane", &LuaAssignLane},
    {"LaneSplit", &LuaLaneSplit},  // BOTLANESPLIT
    {"GetLane", &LuaGetLane},
    {"ClosestLane", &LuaClosestLane},
    {"SharedLanes", &LuaSharedLanes},  // MAPHOST
    {"LaneCenterAndDirection", &LuaLaneCenterAndDirection},
    {"ClosestCamp", &LuaClosestCamp},
    {"CampFight", &LuaCampFight},  // CAMPFINISH
    {"HunterRoute", &LuaHunterRoute},  // HUNTERROUTE
    {"ParasiteRally", &LuaParasiteRally},  // MAPED4
    {"WardSpotOpen", &LuaWardSpotOpen},    // MAPED4
    {"WardSpot", &LuaWardSpot},            // MAPED4b
    {"IsFirstTimePlayer", &LuaIsFirstTimePlayer},
    {"BotRole", &LuaBotRole},  // BOTCOMP
    {"NullPos", &LuaNullPos},  // NULLPOS
    {"NullPosRead", &LuaNullPosRead},
    // D6b
    {"RequestMission", &LuaRequestMission},
    {"IsMissionAvailable", &LuaIsMissionAvailable},
    {"GetAvailableMission", &LuaGetAvailableMission},
    {"AcceptMission", &LuaAcceptMission},
    {"GetMissionID", &LuaGetMissionID},
    {"GetMissionTarget", &LuaGetMissionTarget},
    {"GetMissionDestination", &LuaGetMissionDestination},
    {"OpenMission", &LuaOpenMission},
    {"EndMission", &LuaEndMission},
    {"CanAffordNextItem", &LuaCanAffordNextItem},
    {"PurchaseNextItem", &LuaPurchaseNextItem},
    {"CanBuyPotion", &LuaCanBuyPotion},
    {"PurchasePotions", &LuaPurchasePotions},
    {"IsPositionSafe", &LuaIsPositionSafe},
    // BOTTEAM
    {"RespawnIn", &LuaRespawnIn},
    {"TeamPing", &LuaTeamPing},
    {"Log", &LuaLog},
    // BOTJUNGLE
    {"HumanPing", &LuaHumanPing},
    // KILLFINISH
    {"KillFinish", &LuaKillFinish},
    {"BailWhy", &LuaBailWhy},
    {"EngageBlocked", &LuaEngageBlocked},
};

void* SehLuaState(void* sim) {
  __try {
    return Field<void*>(static_cast<char*>(sim) + sim::kLua, luasim::kLuaState);
  } __except (SehNote(GetExceptionInformation(), "lua_State")) {
    return nullptr;
  }
}

bool SehRegister(void* L) {
  __try {
    RegisterNamespace(L, "nBotHost", kBotHostRegs, static_cast<int>(sizeof kBotHostRegs / sizeof kBotHostRegs[0]));
    return true;
  } __except (SehNote(GetExceptionInformation(), "RegisterNamespace nBotHost")) {
    return false;
  }
}

// A Lua chunk on the server's lua_State under lua_pcall; its string result (or the error) into `out`.
bool SehRunLua(void* sim, const char* chunk, unsigned size, const char* name, char* out, unsigned cap) {
  __try {
    out[0] = 0;
    void* L = Field<void*>(static_cast<char*>(sim) + sim::kLua, luasim::kLuaState);
    const int top = LuaGetTop(L);
    int rc = LuaLoadBuffer(L, chunk, size, name);
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

void LoadApi(void* sim) {
  char path[MAX_PATH];
  GetModuleFileNameA(nullptr, path, MAX_PATH);
  if (char* slash = std::strrchr(path, '\\')) *slash = 0;
  std::strncat(path, "\\dgsim_bots_api.lua", sizeof path - std::strlen(path) - 1);
  std::ifstream in(path, std::ios::binary);
  if (!in) {
    Log("bots: %s not found (setup_server_copy.py installs gms/engine/lua/*.lua); nBot stays the client's stubs", path);
    return;
  }
  const std::string chunk((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
  char out[512];
  // D6b: the script fix-ups' switch, read by the chunk (dgsim_bots.config.fixups)
  const std::string pre = std::string("dgsim_bots = dgsim_bots or {} dgsim_bots.fixupsWanted = ") +
                          (g_opt.fixups ? "true" : "false") +
                          " dgsim_bots.parasiteWanted = " + (g_opt.parasiteGuard ? "true" : "false") +
                          " dgsim_bots.campRadius = " + std::to_string(g_opt.campRadius) +
                          " dgsim_bots.laneWanted = " + (g_opt.laneFixes ? "true" : "false") +
                          " dgsim_bots.laneSplitWanted = " + (g_opt.laneSplit ? "true" : "false") +
                          " dgsim_bots.compositionWanted = " + (g_opt.composition && g_opt.classes.empty() ? "true" : "false") +
                          " dgsim_bots.harassStartWorkers = " + std::to_string(g_opt.harassStartWorkers) +
                          " dgsim_bots.harassEndWorkers = " + std::to_string(g_opt.harassEndWorkers) +
                          " dgsim_bots.teamWanted = " + (g_opt.team && g_opt.laneFixes ? "true" : "false") +
                          " dgsim_bots.tempoWanted = " + (g_opt.tempo ? "true" : "false") +
                          " dgsim_bots.teamStatusEvery = " + std::to_string(g_opt.teamStatusS) +
                          " dgsim_bots.hunterGank = " + (g_opt.hunterGank ? "true" : "false") +
                          " dgsim_bots.jungleWanted = " + (g_opt.jungle ? "true" : "false") +
                          " dgsim_bots.pingWanted = " + (g_opt.pingFollow ? "true" : "false") +
                          " dgsim_bots.hunterParasiteStand = " + (g_opt.hunterParasiteStand ? "true" : "false") +
                          " dgsim_bots.kitsWanted = " + (g_opt.kits ? "true" : "false") +
                          " dgsim_bots.hunterFixWanted = " + (g_opt.hunterFix ? "true" : "false") +
                          " dgsim_bots.campFinishWanted = " + (g_opt.campFinish ? "true" : "false") +
                          " dgsim_bots.engageFixWanted = " + (g_opt.engageFix ? "true" : "false") +
                          " dgsim_bots.joinFixWanted = " + (g_opt.joinFix ? "true" : "false") +
                          " dgsim_bots.desecKitWanted = " + (g_opt.desecKit ? "true" : "false") +
                          " dgsim_bots.fogCheckWanted = " + (g_opt.fogCheck ? "true" : "false") +
                          " return 'ok'";
  SehRunLua(sim, pre.c_str(), static_cast<unsigned>(pre.size()), "dgsim_bots_pre", out, sizeof out);
  const bool ok = SehRunLua(sim, chunk.c_str(), static_cast<unsigned>(chunk.size()), "dgsim_bots_api.lua", out, sizeof out);
  Log("bots: dgsim_bots_api.lua (%zu bytes) on the server's lua_State: %s %s", chunk.size(), ok ? "ok" : "FAILED", out);
}

void FormatMissionStats(char* buf, std::size_t size);

// D6b: once a second: missions whose target is gone end, stale ones expire, then the Lua driver runs the plan's
// mission conditions (dgsim_bots.MissionTick: Check per team, then CheckEnd per open mission).
void MissionTick(void* sim) {
  const double now = MissionNow();
  std::set<std::uint32_t> targets;
  for (int team = 1; team <= 2; ++team)
    for (const gb::Mission* m : g_state.missions.Open(team))
      if (m->target) targets.insert(m->target);
  for (std::uint32_t t : targets) {
    if (SehObjTeamPos(t, nullptr, nullptr)) continue;
    std::string what;
    for (int team = 1; team <= 2; ++team)
      for (const gb::Mission* m : g_state.missions.Open(team))
        if (m->target == t) what += (what.empty() ? "" : "; ") + DescribeMission(*m);
    g_state.Forget(t);
    ++g_ms.gone;
    if (LogBudget()) Log("bots: target %08X gone at %.1f s: mission(s) %s ended", t, now, what.c_str());
  }
  std::vector<std::string> stale;
  for (int team = 1; team <= 2; ++team)
    for (const gb::Mission* m : g_state.missions.Open(team))
      if (m->timeout > 0.f && now - m->refreshed >= m->timeout) stale.push_back(DescribeMission(*m));
  const std::vector<std::uint32_t> expired = g_state.missions.Expire(now);
  g_ms.expired += static_cast<unsigned>(expired.size());
  for (const std::string& s : stale)
    if (LogBudget()) Log("bots: mission %s expired at %.1f s (no refresh within its timeout)", s.c_str(), now);
  static const char kTick[] =
      "local c, e, r = dgsim_bots.MissionTick() "
      "return string.format('%d %d %d', c or 0, e or 0, r or 0)";
  char out[256];
  ++g_ms.ticks;
  if (!SehRunLua(sim, kTick, sizeof kTick - 1, "dgsim_bots_tick", out, sizeof out)) {
    ++g_ms.tickFailed;
    if (g_ms.tickFailed <= 5 || g_ms.tickFailed % 100 == 0) Log("bots: dgsim_bots.MissionTick FAILED: %s", out);
    return;
  }
  int c = 0, e = 0, r = 0;
  if (std::sscanf(out, "%d %d %d", &c, &e, &r) == 3) {
    g_ms.checks += c;
    g_ms.checkEnds += e;
    if (r > 0) {
      const bool first = g_ms.luaErrors == 0;
      g_ms.luaErrors += r;
      if (first || LogBudget()) Log("bots: dgsim_bots.MissionTick at %.1f s: %d check(s), %d ended, %d ERROR(s) (the "
                                    "report lists them: dgsim_bots_report.lua)", now, c, e, r);
    }
  }
  g_lastMissionTick = out;
}

}  // namespace

int NextItemPrice(std::uint32_t creature) {
  if (!Ours() || !g_opt.shopping) return -1;
  const Bot* b = BotByCreature(creature);
  if (!b) return -1;
  BotShop* s = nullptr;
  gb::Inventory inv;
  float gold = 0.f;
  if (!ShopViewOf(*b, &s, &inv, &gold)) return -1;
  gb::Purchase p;
  gb::ShopContext ctx;
  return NextStep(*b, *s, inv, &p, nullptr, nullptr, &ctx) ? std::max(1, p.price) : 0;
}

void Configure(const Options& o) {
  g_opt = o;
  // D6b's keys are read here (experiment.cpp fills the D6a ones)
  g_opt.missions = HostOptionInt("bot_missions", o.missions ? 1 : 0) != 0;
  g_opt.missionMs = HostOptionInt("bot_mission_ms", o.missionMs);
  g_opt.shopping = HostOptionInt("bot_shopping", o.shopping ? 1 : 0) != 0;
  g_opt.potions = HostOptionInt("bot_potions", o.potions);
  g_opt.shopVisit = HostOptionInt("bot_shop_visit", o.shopVisit ? 1 : 0) != 0;  // BOTSHOP-PASS
  g_opt.shopVisitMs = HostOptionInt("bot_shop_visit_ms", o.shopVisitMs);
  g_opt.shopVisitRepeatMs = HostOptionInt("bot_shop_visit_repeat_ms", o.shopVisitRepeatMs);
  if (g_opt.shopVisitMs < 0) g_opt.shopVisitMs = 0;
  if (g_opt.shopVisitRepeatMs < 250) g_opt.shopVisitRepeatMs = 250;
  g_opt.fireTracker = HostOptionInt("bot_fire_tracker", o.fireTracker ? 1 : 0) != 0;
  g_opt.fireZones = HostOptionInt("bot_fire_zones", o.fireZones ? 1 : 0) != 0;  // FIREZONE
  g_opt.fixups = HostOptionInt("bot_fixups", o.fixups ? 1 : 0) != 0;
  g_opt.parasiteGuard = HostOptionInt("bot_parasite_guard", o.parasiteGuard ? 1 : 0) != 0;
  g_opt.laneFixes = HostOptionInt("bot_lane_fixes", o.laneFixes ? 1 : 0) != 0;  // BOTLANE
  g_opt.campRadius = HostOptionInt("bot_camp_radius", o.campRadius);  // BOTIDLE: fix-up 5, 0 = the script's 5
  // BOTCOMP: the preferred 5v5 setup and the Predator's and Hunter's rules
  g_opt.composition = HostOptionInt("bot_composition", o.composition ? 1 : 0) != 0;
  {
    char drop[128];
    HostOptionString("bot_composition_drop", o.compositionDrop.c_str(), drop, sizeof drop);
    g_opt.compositionDrop = drop;
  }
  // BOTPROFILE: 0 = never our profiles (the Predator on PC_Warden with the role set after it, as before)
  g_opt.profiles = HostOptionInt("bot_profiles", o.profiles ? 1 : 0) != 0;
  {
    // HUNTERBOT: the Hunter seat's classes ("all", a list such as "golem,survivor", "artificer" = Petrus alone)
    char hunters[128];
    HostOptionString("bot_hunters", o.hunters.c_str(), hunters, sizeof hunters);
    g_opt.hunters = hunters;
    // DESECBOT: each team's first Tactician seat pinned to a class ("desecrator", "guardian,desecrator"; "" = drawn)
    char tacticians[128];
    HostOptionString("bot_tacticians", o.tacticians.c_str(), tacticians, sizeof tacticians);
    g_opt.tacticians = tacticians;
    // MIKELLABOT: each team's first Gladiator seat pinned to a class ("mikella", "mikella,highhuntress"; "" = drawn)
    char gladiators[128];
    HostOptionString("bot_gladiators", o.gladiators.c_str(), gladiators, sizeof gladiators);
    g_opt.gladiators = gladiators;
    // BOTS2c: the same lineup on both teams for head-to-head brain tests ("1" = MirrorDefault, or five classes)
    char mirror[160];
    HostOptionString("bot_mirror", o.mirror.c_str(), mirror, sizeof mirror);
    g_opt.mirror = mirror;
  }
  g_opt.guideBuilds = HostOptionInt("bot_guide_builds", o.guideBuilds ? 1 : 0) != 0;  // DESECBOT-2
  g_opt.draft = HostOptionInt("bot_draft", o.draft);  // BOTDRAFT: -1 auto (custom, practice, pvp), 0 off, 1 always
  g_opt.hunterSeat = HostOptionInt("bot_hunter_seat", o.hunterSeat);  // MAPHOST: -1 auto (camps), 0 never, 1 always
  g_opt.laneSplit = HostOptionInt("bot_lane_split", o.laneSplit ? 1 : 0) != 0;  // BOTLANESPLIT
  g_opt.kits = HostOptionInt("bot_kits", o.kits ? 1 : 0) != 0;               // HUNTERBOT
  g_opt.hunterFix = HostOptionInt("bot_hunter_fix", o.hunterFix ? 1 : 0) != 0;  // HUNTERBOT
  g_opt.campFinish = HostOptionInt("bot_camp_finish", o.campFinish ? 1 : 0) != 0;  // CAMPFINISH
  g_opt.engageFix = HostOptionInt("bot_engage_fix", o.engageFix ? 1 : 0) != 0;  // DESECBOT-3
  g_opt.joinFix = HostOptionInt("bot_join_fix", o.joinFix ? 1 : 0) != 0;        // BOTCYCLE
  g_opt.desecKit = HostOptionInt("bot_desec_kit", o.desecKit ? 1 : 0) != 0;     // DESECBOT-3
  g_opt.fogCheck = HostOptionInt("bot_fog_check", o.fogCheck ? 1 : 0) != 0;     // BOTBUSH
  g_opt.harassStartWorkers = HostOptionInt("bot_harass_start_workers", o.harassStartWorkers);
  g_opt.harassEndWorkers = HostOptionInt("bot_harass_end_workers", o.harassEndWorkers);
  g_opt.team = HostOptionInt("bot_team", o.team ? 1 : 0) != 0;                   // BOTTEAM
  g_opt.teamPings = HostOptionInt("bot_team_pings", o.teamPings ? 1 : 0) != 0;  // BOTTEAM
  g_opt.hunterGank = HostOptionInt("bot_hunter_gank", o.hunterGank) != 0 ? 1 : 0;
  g_opt.tempo = HostOptionInt("bot_tempo", o.tempo ? 1 : 0) != 0;                 // BOTTEMPO
  g_opt.teamStatusS = HostOptionInt("bot_team_status_s", o.teamStatusS);         // BOTTEMPO
  g_opt.jungle = HostOptionInt("bot_jungle", o.jungle ? 1 : 0) != 0;             // BOTJUNGLE
  g_opt.pingFollow = HostOptionInt("bot_ping_follow", o.pingFollow ? 1 : 0) != 0;  // BOTJUNGLE
  g_opt.hunterParasiteStand = HostOptionInt("bot_hunter_parasite", o.hunterParasiteStand ? 1 : 0) != 0;  // PRETUSBOT
  if (g_opt.teamStatusS < 0) g_opt.teamStatusS = 0;
  if (g_opt.missionMs < 100) g_opt.missionMs = 100;
  Log("bots (D6a, D6b): bots %d%s, allies %d, classes '%s', lua %d, lanes %d, ftue %d, log limit %d; missions %d (every "
      "%d ms), shopping %d (potions %d; BOTSHOP-PASS shop visits %d after %d ms, every %d ms), fire tracker %d, "
      "script fix-ups %d; BOTCOMP composition %d (drop '%s'), harass "
      "workers %d..%d, Hunter gank %d; BOTTEAM team layer %d, pings %d; BOTTEMPO %d, team status every %d s; "
      "BOTJUNGLE jungler %d, ping following %d; PRETUSBOT Hunter stand at the parasite %d; BOTPROFILE profiles %d; "
      "HUNTERBOT Hunters '%s', kits %d, Hunter fixes %d; DESECBOT Tacticians '%s', guide builds %d; BOTBUSH sight "
      "check %d",
      g_opt.teamSize,
      g_opt.teamSize < 0
          ? " (auto: createGame's fillTeams a team, else 5 a team when the game's descriptors have BotsFillTeams 0x400)"
          : "",
      g_opt.allies, g_opt.classes.c_str(), g_opt.lua, g_opt.lanes, g_opt.ftue, g_opt.logLimit, g_opt.missions,
      g_opt.missionMs, g_opt.shopping, g_opt.potions, g_opt.shopVisit, g_opt.shopVisitMs, g_opt.shopVisitRepeatMs,
      g_opt.fireTracker, g_opt.fixups, g_opt.composition,
      g_opt.compositionDrop.c_str(), g_opt.harassStartWorkers, g_opt.harassEndWorkers, g_opt.hunterGank,
      g_opt.team && g_opt.laneFixes, g_opt.teamPings, g_opt.tempo, g_opt.teamStatusS, g_opt.jungle, g_opt.pingFollow,
      g_opt.hunterParasiteStand, g_opt.profiles, g_opt.hunters.c_str(), g_opt.kits, g_opt.hunterFix,
      g_opt.tacticians.c_str(), g_opt.guideBuilds, g_opt.fogCheck);
}

void OnHumanPing(int team, std::uint32_t pinger, std::uint32_t target, const float* pos, int type) {
  if (!pos) return;
  HumanPingRec& r = g_humanPings[g_humanPingSeq % kHumanPings];
  r.seq = ++g_humanPingSeq;
  r.team = team;
  r.pinger = pinger;
  r.target = target;
  std::memcpy(r.pos, pos, sizeof r.pos);
  r.type = type;
  r.t = MissionNow();
  if (LogBudget())
    Log("bots: human ping %u (team %d, pinger %08X, target %08X, at (%.1f, %.1f), type %d) at %.1f s: for the bots "
        "(BOTJUNGLE)",
        r.seq, team, pinger, target, static_cast<double>(pos[0]), static_cast<double>(pos[1]), type, r.t);
}

void SetFillTeams(int seats) {
  g_fillTeams = seats > 0 ? seats : 0;
  Log("bots: createGame fillTeams %d (B10d: %s)", seats,
      g_fillTeams ? "bots in the open seats of a matchmade game" : "not asked; the descriptors decide");
}

void SetGameMode(const std::string& mode) { g_gameMode = mode; }

// ---- BOTDRAFT -------------------------------------------------------------------------------------------------------

namespace {

bool SehClearStatus(void* sim, unsigned index, std::uint32_t bits, std::uint32_t* out) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    if (!p) return false;
    Field<std::uint32_t>(p, player::kStatus) &= ~bits;
    *out = Field<std::uint32_t>(p, player::kStatus);
    return true;
  } __except (SehNote(GetExceptionInformation(), "clear bot status")) {
    return false;
  }
}

bool SehSetStatusBits(void* sim, unsigned index, std::uint32_t bits, std::uint32_t* out) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    if (!p) return false;
    Field<std::uint32_t>(p, player::kStatus) |= bits;
    *out = Field<std::uint32_t>(p, player::kStatus);
    return true;
  } __except (SehNote(GetExceptionInformation(), "set bot status")) {
    return false;
  }
}

// The PlayerClass name (PC_<X>) of a shaper's default-skin Noun asset id; "" when unreadable.
void* SehNounPlayerClass(void* noun) {
  __try {
    char* nd = noun ? Field<char*>(noun, noun::kData) : nullptr;
    return nd ? Field<void*>(nd, kNounPlayerClass) : nullptr;
  } __except (SehNote(GetExceptionInformation(), "noun PlayerClass")) {
    return nullptr;
  }
}

std::string SehPlayerClassOf(std::uint32_t nounId) {
  void* pcAsset = SehNounPlayerClass(nounId ? SehAsset(nounId) : nullptr);
  if (!pcAsset) return "";
  std::string name = SehAssetName(pcAsset);
  const size_t dot = name.find('.');
  if (dot != std::string::npos) name.resize(dot);
  return name;
}

}  // namespace

bool BeginDraft(void* sim) {
  if (!g_draft || g_drafted || g_bots.empty()) return false;
  g_draftHidden = true;
  BoundTo b(sim);
  for (Bot& bot : g_bots) {
    std::uint32_t st = bot.status;
    if (SehClearStatus(sim, static_cast<unsigned>(bot.index), kStatusPicked, &st)) bot.status = st;
  }
  Log("bots: BOTDRAFT: %zu bot(s) wait in shaper select for the humans; until the reveal their updates carry no bot "
      "data, noun or role, and no status 0x100 (the clients name them \"BOT n\")",
      g_bots.size());
  return true;
}

bool DraftHidden() { return g_draftHidden; }

std::vector<DraftOut> Draft(void* sim, const std::vector<DraftHumanIn>& humans) {
  std::vector<DraftOut> out;
  if (g_drafted) return out;
  g_drafted = true;
  BoundTo b(sim);
  std::vector<gb::DraftHuman> hs;
  std::string hdesc;
  for (const DraftHumanIn& h : humans) {
    gb::DraftHuman d;
    d.index = h.index;
    d.team = h.team;
    d.baseClass = SehPlayerClassOf(h.shaper);
    int source = gb::kIntentNone;
    d.role = gb::HumanIntent(h.preferred, h.roleCard, gb::TypicalRole(d.baseClass), &source);
    hs.push_back(d);
    char buf[200];
    std::snprintf(buf, sizeof buf, " [%d: team %d, %s, wants %s (%s)]", h.index, h.team,
                  d.baseClass.empty() ? "no shaper" : d.baseClass.c_str(), gb::RoleName(d.role),
                  gb::IntentSourceName(source));
    hdesc += buf;
  }
  std::vector<gb::DraftTeam> teams;
  for (const Bot& bot : g_bots) {
    auto it = std::find_if(teams.begin(), teams.end(), [&](const gb::DraftTeam& t) { return t.team == bot.team; });
    if (it == teams.end()) teams.push_back(gb::DraftTeam{bot.team, 1});
    else ++it->seats;
  }
  gb::DraftOptions o;
  o.seed = g_draftSeed;
  o.hunters = gb::ParseHunterList(g_opt.hunters);
  o.huntersPinned = !(g_opt.hunters.empty() || g_opt.hunters == "default" || g_opt.hunters == "1");
  o.tacticians = gb::ParseTacticianList(g_opt.tacticians);
  o.gladiators = gb::ParseGladiatorList(g_opt.gladiators);
  o.jungle = HunterSeat();  // MAPHOST
  o.laneSplit = g_opt.laneSplit;  // BOTLANESPLIT
  const std::map<int, std::vector<gb::ComposedSeat>> drawn = gb::DraftMatch(teams, hs, g_available, o);
  Log("bots: BOTDRAFT: draw with seed %u: humans%s", g_draftSeed, hdesc.empty() ? " none" : hdesc.c_str());
  g_humanRoles.clear();  // BOTLANESPLIT: the draw counted the humans' intents
  for (const gb::DraftHuman& h : hs) g_humanRoles[h.team].push_back(h.role);
  std::map<int, size_t> nth;
  for (Bot& bot : g_bots) {
    auto t = drawn.find(bot.team);
    const size_t k = nth[bot.team]++;
    if (t == drawn.end() || k >= t->second.size()) {
      Log("bots: BOTDRAFT: player %d (team %d) got no class from the draw; it keeps %s", bot.index, bot.team,
          bot.botClass.c_str());
      DraftOut d;
      d.index = bot.index;
      d.noun = bot.noun;
      d.role = bot.role;
      d.altRoles = gb::AltRoles(bot.botClass);
      out.push_back(d);
      continue;
    }
    const gb::ComposedSeat& s = t->second[k];
    const std::uint32_t classId = g_classIds.count(s.botClass) ? g_classIds[s.botClass] : 0;
    CreatedPlayer cp = {};
    const bool ok = classId != 0 && SehCreateBot(sim, static_cast<unsigned>(bot.index), static_cast<unsigned char>(bot.team),
                                                 bot.onlineId, classId, s.roleOverride ? s.role : -1, &cp);
    if (ok) {
      bot.botClass = s.botClass;
      bot.lane = s.lane;
      bot.noun = SehAssetId(cp.noun);
      bot.role = cp.role;
      std::uint32_t st = cp.status;
      if (SehClearStatus(sim, static_cast<unsigned>(bot.index), kStatusPicked, &st)) bot.status = st;  // until the reveal
    }
    Log("bots: BOTDRAFT: player %d (team %d) draws %s (asset %08X) as %s, first lane %d%s%s -> %s: noun %s, role %d",
        bot.index, bot.team, s.botClass.c_str(), classId, gb::RoleName(s.role), s.lane,
        s.roleOverride ? ", the role set after its BotClass" : "", s.fallback ? ", a fallback (its pools were used up)" : "",
        ok ? "LabsPlayer_ReflectBotData" : "FAILED (it keeps its first class)", SehAssetName(cp.noun), cp.role);
    DraftOut d;
    d.index = bot.index;
    d.noun = bot.noun;
    d.role = bot.role;
    d.altRoles = gb::AltRoles(bot.botClass);
    out.push_back(d);
  }
  if (g_opt.laneSplit) LogLaneSplit("the draft");
  return out;
}

void RevealDrafted(void* sim) {
  if (!g_draftHidden) return;
  g_draftHidden = false;
  BoundTo b(sim);
  for (Bot& bot : g_bots) {
    std::uint32_t st = bot.status;
    if (SehSetStatusBits(sim, static_cast<unsigned>(bot.index), kStatusPicked, &st)) bot.status = st;
  }
  Log("bots: BOTDRAFT: the reveal: the bots' updates carry their bot data, noun and role again (status 0x100 set)");
}

void OnSimulatorCreated(void* sim) {
  g_sim = sim;
  if (!CodeMatches(0x00873170, kRegisterNamespaceBytes, sizeof kRegisterNamespaceBytes)) return;
  void* L = SehLuaState(sim);
  if (!L) return;
  Log("bots: nBotHost (AssignLane, GetLane, ClosestLane, LaneCenterAndDirection, IsFirstTimePlayer; D6b: RequestMission, "
      "IsMissionAvailable, GetAvailableMission, AcceptMission, GetMissionID, GetMissionTarget, GetMissionDestination, "
      "OpenMission, EndMission, CanAffordNextItem, PurchaseNextItem, CanBuyPotion, PurchasePotions, IsPositionSafe) on "
      "lua_State %p: %s (merged with wells.cpp's MinesForTeam, FollowersForTitan, IsTitanNodeLocked: "
      "GearsLua_RegisterNamespace reuses an existing table)",
      L, SehRegister(L) ? "registered" : "FAILED");
}

void OnLevelStarted(void* sim) {
  g_sim = sim;
  ReadPlan();
  if (g_opt.lanes) SetLanes();
  if (g_opt.lua) LoadApi(sim);
}

const std::vector<Bot>& EnsurePlayers(void* sim, const std::vector<Human>& humans, const std::vector<Human>& waiting) {
  if (g_planned) return g_bots;
  g_planned = true;
  g_sim = sim;
  const std::uint32_t desc = match::Enabled() ? match::Descriptors() : 0;
  g_teamSize = gb::BotTeamSize(g_opt.teamSize, g_fillTeams, desc);  // B10d: createGame's fillTeams first
  if (g_teamSize <= 0) {
    Log("bots: no bot players (team size %d; [host] bots %d, createGame fillTeams %d, descriptors 0x%X)", g_teamSize,
        g_opt.teamSize, g_fillTeams, desc);
    return g_bots;
  }
  if (!CodeMatches(0x00915D30, kReflectBotDataBytes, sizeof kReflectBotDataBytes)) {
    Log("bots: LabsPlayer_ReflectBotData at 00915D30 has other bytes; no bot players");
    return g_bots;
  }
  BoundTo b(sim);
  ReadPlan();
  if (!g_plan.read || g_plan.bots.empty()) return g_bots;
  std::vector<gb::HumanPlayer> hs, ws;
  std::string hdesc, wdesc;
  for (const Human& h : humans) {
    gb::HumanPlayer p;
    p.index = h.index;
    p.team = h.team;
    p.role = h.role;
    ShaperTypeOfNoun(h.noun, &p.shaper);
    hs.push_back(p);
    char buf[160];
    std::snprintf(buf, sizeof buf, " [index %d, team %d, %s %s/%s/%s]", h.index, h.team, SehAssetName(h.noun),
                  p.shaper.archetype.c_str(), p.shaper.attackStyle.c_str(), p.shaper.damageType.c_str());
    hdesc += buf;
  }
  for (const Human& w : waiting) {
    gb::HumanPlayer p;
    p.index = w.index;
    p.team = w.team;
    ws.push_back(p);
    char buf[64];
    std::snprintf(buf, sizeof buf, " [index %d, team %d]", w.index, w.team);
    wdesc += buf;
  }
  // G3b: a roster human that hasn't connected yet counts for its team (CountedHumans) and keeps its index
  const std::vector<gb::HumanPlayer> counted = gb::CountedHumans(hs, ws);
  g_humanRoles.clear();  // BOTLANESPLIT
  for (const gb::HumanPlayer& h : counted) g_humanRoles[h.team].push_back(h.role);
  const std::vector<int> teams = gb::BotTeams(counted, g_opt.allies);
  std::vector<gb::BotPlayer> plan;
  g_composed = g_opt.composition && g_opt.classes.empty();  // [host] bot_classes overrides the setup
  if (g_composed) {
    // BOTCOMP: the BotClass assets the setup may use: the role lists' names and the
    // plan's own bots as the any-class fallback
    std::set<std::string> available;
    std::vector<std::string> anyClass;
    // the role lists, the Hunter and Gladiator classes and every profile (composition.h CatalogCandidates)
    std::vector<std::string> names = gb::CatalogCandidates();
    for (const gb::BotCandidate& c : g_plan.bots) {
      names.push_back(c.botClass);
      anyClass.push_back(c.botClass);
    }
    for (const std::string& n : names) {
      if (available.count(n)) continue;
      // a borrowed-AI profile's base has no AI of its own: without the profile its seat falls back to the AI's class
      const bool borrowed = gb::AiClass(n) != gb::BaseClass(n);
      const std::string fallback = borrowed ? gb::AiClass(n) + " instead" : gb::BaseClass(n) + " with the role set after it";
      if (gb::IsProfile(n) && !g_opt.profiles) {
        Log("bots: BOTPROFILE: %s not used ([host] bot_profiles=0): %s", n.c_str(), fallback.c_str());
        continue;
      }
      void* a = SehAsset(gms::AssetIdOf(n + ".BotClass"));
      if (!a) {
        if (gb::IsProfile(n))
          Log("bots: BOTPROFILE: %s.BotClass not in this copy's catalog (no %s or an older one): %s", n.c_str(),
              "game\\Data\\zz_BotProfiles.snap", fallback.c_str());
        else
          Log("bots: BOTCOMP: %s.BotClass not in the catalog", n.c_str());
        continue;
      }
      if (gb::IsProfile(n)) Log("bots: BOTPROFILE: %s.BotClass in the catalog (asset %08X)", n.c_str(), SehAssetId(a));
      g_classIds[n] = SehAssetId(a);
      available.insert(n);
    }
    // varied teams from game to game: [host] bot_shuffle 1 (default) draws a seed per game, bot_shuffle N > 1 fixes it
    // (N is the seed, to reproduce a team), 0 keeps composition.h's fixed order
    const int shuffleOpt = HostOptionInt("bot_shuffle", 1);
    const unsigned seed = shuffleOpt == 0 ? 0u : shuffleOpt > 1 ? static_cast<unsigned>(shuffleOpt) : (std::random_device{}() | 1u);
    Log("bots: BOTCOMP: class shuffle seed %u ([host] bot_shuffle %d)", seed, shuffleOpt);
    const std::vector<std::string> hunters = gb::ParseHunterList(g_opt.hunters);
    std::string hunterList;
    for (const std::string& h : hunters) hunterList += (hunterList.empty() ? "" : ", ") + h + (available.count(h) ? "" : " (missing)");
    Log("bots: HUNTERBOT: Hunter classes %s ([host] bot_hunters '%s')", hunterList.c_str(), g_opt.hunters.c_str());
    const std::vector<std::string> tacticians = gb::ParseTacticianList(g_opt.tacticians);
    std::string tacticianList;
    for (const std::string& t : tacticians)
      tacticianList += (tacticianList.empty() ? "" : ", ") + t + (available.count(t) ? "" : " (missing)");
    Log("bots: DESECBOT: Tactician pin %s ([host] bot_tacticians '%s')",
        tacticianList.empty() ? "none (drawn)" : tacticianList.c_str(), g_opt.tacticians.c_str());
    const std::vector<std::string> gladiators = gb::ParseGladiatorList(g_opt.gladiators);
    std::string gladiatorList;
    for (const std::string& g : gladiators)
      gladiatorList += (gladiatorList.empty() ? "" : ", ") + g + (available.count(g) ? "" : " (missing)");
    Log("bots: MIKELLABOT: Gladiator pin %s ([host] bot_gladiators '%s')",
        gladiatorList.empty() ? "none (drawn)" : gladiatorList.c_str(), g_opt.gladiators.c_str());
    // BOTDRAFT: [host] bot_draft -1 (auto) = the bots of a custom, practice or PvP game draw in shaper select after
    // the humans (gb::DraftOn; the plan below only fixes how many bots each team has and their indexes; Draft gives
    // them their classes)
    // BOTS2c: [host] bot_mirror, every team the same GTGTH lineup (no draw, no pins, no draft)
    std::string mirrorWhy;
    const std::vector<gb::ComposedSeat> mirror = gb::MirrorLineup(g_opt.mirror, available, &mirrorWhy);
    if (!mirror.empty()) {
      std::string lineup;
      for (const gb::ComposedSeat& m : mirror)
        lineup += (lineup.empty() ? "" : ", ") + (m.botClass.empty() ? std::string("(empty)") : m.botClass) + " " +
                  gb::RoleName(m.role) + (m.lane == gb::kLaneTop ? " top" : " bottom");
      Log("bots: BOTS2c mirror: every team %s ([host] bot_mirror '%s'%s%s); no draw, no pins, no draft", lineup.c_str(),
          g_opt.mirror.c_str(), mirrorWhy.empty() ? "" : ": ", mirrorWhy.c_str());
    }
    g_available = available;
    g_draftSeed = seed;
    g_draft = gb::DraftOn(g_opt.draft, g_gameMode) && mirror.empty();
    Log("bots: BOTDRAFT: %s ([host] bot_draft %d, createGame mode '%s')",
        g_draft ? "the bots draw their shapers and roles in shaper select, after the humans (if it runs)"
                : mirror.empty() ? "off: the bots are composed now" : "off: [host] bot_mirror",
        g_opt.draft, g_gameMode.c_str());
    plan = gb::PlanBotPlayersComposed(counted, g_teamSize, teams, available, anyClass, gb::ParseDropOrder(g_opt.compositionDrop),
                                      0, seed, hunters, tacticians, gladiators, HunterSeat(), g_opt.laneSplit, mirror);
    for (const gb::HumanPlayer& h : counted)
      Log("bots: BOTCOMP: human index %d team %d: role %s", h.index, h.team, gb::RoleName(h.role));
  } else {
    plan = gb::PlanBotPlayers(g_plan.slots, g_plan.bots, counted, g_teamSize, teams, g_plan.topLaners, g_plan.botLaners, 0,
                              g_opt.laneSplit);
  }
  Log("bots: team fill (team size %d from %s, descriptors 0x%X): humans%s; waiting roster humans (G3b: counted for "
      "their teams)%s; %zu bot(s)",
      g_teamSize,
      g_opt.teamSize >= 0 ? "[host] bots" : g_fillTeams > 0 ? "createGame fillTeams" : "the descriptors' BotsFillTeams",
      desc, hdesc.empty() ? " none" : hdesc.c_str(), wdesc.empty() ? " none" : wdesc.c_str(), plan.size());
  for (const gb::BotPlayer& p : plan) {
    Bot bot;
    bot.index = p.index;
    bot.team = p.team;
    bot.onlineId = p.onlineId;
    bot.botClass = p.botClass;
    bot.lane = p.lane;
    const std::uint32_t classId = g_classIds.count(p.botClass) ? g_classIds[p.botClass] : 0;
    CreatedPlayer cp = {};
    bot.created = classId != 0 && SehCreateBot(sim, static_cast<unsigned>(p.index), static_cast<unsigned char>(p.team),
                                               p.onlineId, classId, p.roleOverride ? p.role : -1, &cp);
    bot.status = cp.status;
    bot.noun = SehAssetId(cp.noun);
    bot.role = cp.role;
    if (bot.created) ++g_st.created;
    else ++g_st.createFailed;
    Log("bots: player %d: %s (asset %08X), team %d, slot %d, first lane %d, online id %llu -> %s: mbIsBot %d, noun %s, "
        "role %d%s, status 0x%x",
        p.index, p.botClass.c_str(), classId, p.team, p.slot, p.lane, static_cast<unsigned long long>(p.onlineId),
        bot.created ? "GameSimulator_AddPlayer + LabsPlayer_ReflectBotData" : "FAILED", cp.isBot, SehAssetName(cp.noun),
        cp.role, p.roleOverride ? " (BOTCOMP: the setup's Predator on this BotClass)" : g_composed ? " (BOTCOMP)" : "",
        cp.status);
    if (bot.created) g_bots.push_back(bot);
  }
  if (g_opt.laneSplit) LogLaneSplit(g_draft ? "planned; the draft decides" : "the composition");
  return g_bots;
}

const std::vector<Bot>& Players() { return g_bots; }

bool LaneFrontOf(std::uint32_t creature, int team, float* out, int* laneOut) {
  int lane = g_state.lanes.LaneOf(creature);
  if (lane <= 0)
    for (const Bot& b : g_bots)
      if (b.creature == creature) lane = b.lane;
  gb::Front f;
  if (lane <= 0 || !g_state.fronts.Get(lane, team, &f)) return false;
  if (gms::agents::IsNullPos(f.center.x, f.center.y)) {  // NULLPOS: an empty lane path's front
    char who[64];
    std::snprintf(who, sizeof who, "lane %d team %d", lane, team);
    agents::ReportNullPos(0, static_cast<int>(gms::agents::NullPosWhere::kLaneFront), who, "LaneFrontOf");
    return false;
  }
  out[0] = f.center.x;
  out[1] = f.center.y;
  out[2] = f.center.z;
  if (laneOut) *laneOut = lane;
  return true;
}

const std::vector<gb::LanePath>& LanePaths() { return g_state.fronts.Lanes(); }

int CurrentLane(std::uint32_t creature) {
  const int lane = g_state.lanes.LaneOf(creature);
  if (lane > 0) return lane;
  for (const Bot& b : g_bots)
    if (b.creature == creature) return b.lane;
  return 0;
}

void SetRole(int index, int role) {
  for (Bot& b : g_bots)
    if (b.index == index) b.role = role;
}

void SplitLanesAtReveal(const std::vector<std::pair<int, int>>& humanTeamRoles) {
  if (!g_opt.laneSplit) return;
  g_humanRoles.clear();
  for (const auto& tr : humanTeamRoles) g_humanRoles[tr.first].push_back(tr.second);
  ResplitLanes("the reveal");
  LogLaneSplit("the reveal");
}

bool IsBotIndex(int index) {
  for (const Bot& b : g_bots)
    if (b.index == index) return true;
  return false;
}

int DeployAll(void* sim) {
  int n = 0;
  BoundTo bound(sim);
  for (Bot& b : g_bots) {
    if (b.creature) continue;
    b.creature = repl::DeployShaper(sim, static_cast<unsigned>(b.index));
    if (!b.creature) {
      ++g_st.deployFailed;
      Log("bots: player %d (%s): LabsPlayer_DeployCharacter FAILED", b.index, b.botClass.c_str());
      continue;
    }
    ++g_st.deployed;
    ++n;
    death::OnPlayerDeployed(b.index);  // starting gold, as for a human
    g_state.bots.insert(b.creature);
    g_state.lanes.Assign(b.creature, b.lane);
    BrainInfo bi;
    if (SehBrainInfo(b.creature, &bi))
      Log("bots: player %d (%s, team %d) deployed: creature %08X (player-controlled %d, player index %d), brain %p "
          "(active %d, AI definition %s, plan node %d, phase node %d), tree flags 0x%x, first lane %d",
          b.index, b.botClass.c_str(), b.team, b.creature, bi.playerControlled, bi.playerIndex, bi.brain, bi.active,
          SehAssetName(bi.def), bi.plan, bi.phase, bi.treeFlags, b.lane);
    if (g_opt.shopping) ShopOf(b);  // D6b: the build, read once (logged)
  }
  if (n > 0) EnableFireTracker(sim);  // D6b: bots exist now
  return n;
}

void Tick(void* sim) {
  if (g_bots.empty()) return;
  g_sim = sim;
  const unsigned long long now = SehNowMs(sim);
  // D6b: the missions, about once a second [assumed rate], once dgsim_bots_api.lua is loaded
  if (g_opt.missions && g_opt.lua && now >= g_nextMissionMs) {
    g_nextMissionMs = now + static_cast<unsigned long long>(g_opt.missionMs);
    MissionTick(sim);
  }
  ShopVisits(now);  // BOTSHOP-PASS
  static unsigned long long nextStatsMs = 0;
  if (now >= nextStatsMs) {
    nextStatsMs = now + 10000;
    char ms[2048];
    FormatMissionStats(ms, sizeof ms);
    Log("stats bot missions: %s", ms);
  }
  if (!g_opt.lanes) return;
  if (!g_lanesSet) SetLanes();
  if (now < g_nextFrontsMs) return;
  g_nextFrontsMs = now + 500;  // twice a second [assumed]
  static RawUnit raw[512];
  const int n = SehCollectMinions(sim, raw, 512);
  if (n < 0) return;
  std::vector<gb::Unit> units;
  units.reserve(static_cast<size_t>(n));
  for (int i = 0; i < n; ++i) {
    gb::Unit u;
    u.team = raw[i].team;
    u.pos = gb::MakeVec(raw[i].pos[0], raw[i].pos[1], raw[i].pos[2]);
    units.push_back(u);
  }
  g_state.fronts.Update(units);
  ++g_st.frontUpdates;
  g_st.minions = static_cast<unsigned>(n);
}

void OnPlanEntered(std::uint32_t creature, int team, int missionType, bool requestMission, std::uint32_t linkMission,
                   std::uint32_t target, const char* noun, const char* planName) {
  if (!g_opt.missions || !creature) return;
  const std::uint32_t before = g_state.missions.MissionOf(creature);
  gb::PlanEntry e;
  e.missionType = missionType;
  e.requestMission = requestMission;
  e.linkMission = linkMission;
  e.target = target;
  std::uint32_t after = 0;
  const gb::PlanMission r = g_state.EnterPlan(creature, team, e, MissionNow(), &after);
  switch (r) {
    case gb::PlanMission::kAccepted: ++(after != before ? g_ms.planAccepted : g_ms.planAgain); break;
    case gb::PlanMission::kRefused: ++g_ms.planRefused; break;
    case gb::PlanMission::kOpened: ++(after != before ? g_ms.planOpened : g_ms.planAgain); break;
    case gb::PlanMission::kLeft: ++g_ms.planLeft; break;
    case gb::PlanMission::kKept: ++g_ms.planKept; break;
    default: break;
  }
  if (r == gb::PlanMission::kNone) return;
  const gb::Mission* m = g_state.missions.Find(after);
  const bool changed = after != before;
  if (m && changed && (r == gb::PlanMission::kAccepted || r == gb::PlanMission::kOpened) && m->type >= 0 && m->type < 14)
    ++g_ms.acceptedByType[m->type];
  // every change of the bot's mission; re-entries that change nothing (a plan bounce) only sparsely
  static unsigned quiet = 0;
  if ((changed || r == gb::PlanMission::kRefused || ++quiet <= 20 || quiet % 100 == 0) && LogBudget())
    Log("bots: %08X %s enters plan %s (missionType %d %s%s, link mission %u): %s; mission %u -> %s at %.1f s", creature,
        noun ? noun : "?", planName ? planName : "?", missionType, MissionName(missionType),
        requestMission ? ", requestMission" : "", linkMission, gb::PlanMissionName(r), before,
        m ? DescribeMission(*m).c_str() : "none", MissionNow());
}

void OnRequestPlanThink(std::uint32_t creature, int team, int missionType, std::uint32_t target) {
  if (!g_opt.missions || !creature) return;
  const std::uint32_t before = g_state.missions.MissionOf(creature);
  const std::uint32_t id = g_state.RefreshOwnMission(creature, team, missionType, target, MissionNow());
  if (id && id != before) {
    ++g_ms.planOpened;
    if (missionType >= 0 && missionType < 14) ++g_ms.acceptedByType[missionType];
    if (const gb::Mission* m = g_state.missions.Find(id))
      if (LogBudget())
        Log("bots: %08X opens its own mission %s (a requestMission plan) at %.1f s", creature, DescribeMission(*m).c_str(),
            MissionNow());
  }
}

std::uint32_t MissionOf(std::uint32_t creature) { return g_state.missions.MissionOf(creature); }

void FormatStats(char* buf, std::size_t size) {
  int team[3] = {};
  int deployed = 0;
  for (const Bot& b : g_bots) {
    if (b.team >= 0 && b.team <= 2) ++team[b.team];
    if (b.creature) ++deployed;
  }
  std::string fronts;
  for (int lane = 1; lane <= 2; ++lane)
    for (int t = 1; t <= 2; ++t) {
      gb::Front f;
      if (!g_state.fronts.Get(lane, t, &f)) continue;
      char b[96];
      std::snprintf(b, sizeof b, " L%dT%d (%.0f, %.0f) %d/%d;", lane, t, static_cast<double>(f.center.x),
                    static_cast<double>(f.center.y), f.own, f.enemy);
      fronts += b;
    }
  std::snprintf(buf, size,
                "bots %zu (team 1 %d, team 2 %d; created %u, failed %u), deployed %d (failed %u); lane fronts: updates %u, "
                "minions %u,%s nBotHost calls: AssignLane %u, GetLane %u, ClosestLane %u, LaneCenterAndDirection %u, "
                "IsFirstTimePlayer %u, other simulator %u; exceptions %u; BOTLANESPLIT lane changes kept %u, taken %u",
                g_bots.size(), team[1], team[2], g_st.created, g_st.createFailed, deployed, g_st.deployFailed,
                g_st.frontUpdates, g_st.minions, fronts.empty() ? " none;" : fronts.c_str(), g_st.calls[0], g_st.calls[1],
                g_st.calls[2], g_st.calls[3], g_st.calls[4], g_st.calls[5], g_st.exceptions, g_splitKept, g_splitTaken);
  std::size_t len = std::strlen(buf);
  // every bot: lane, position, HP, level, the brain step's plan / phase / leaf; D6b: mission, gold, items
  if (!g_sim || len + 1 >= size) return;
  BoundTo bound(g_sim);
  for (const Bot& b : g_bots) {
    float pos[3] = {};
    float hp = 0.f;
    int level = 0;
    if (!b.creature || !SehBotLook(g_sim, b, pos, &hp, &level)) continue;
    const int lane = g_state.lanes.LaneOf(b.creature);
    const std::string brain = agents::DescribeBot(b.creature);
    Purse p;
    int items = 0;
    if (SehPurse(g_sim, b.index, &p))
      for (unsigned s = 0; s < 6; ++s) items += p.items[s] ? 1 : 0;
    const int n = std::snprintf(buf + len, size - len, "; [%d %s t%d lane %d (%.0f, %.0f) hp %.0f L%d %s m%u g%.0f i%d]",
                                b.index, b.botClass.c_str(), b.team, lane, static_cast<double>(pos[0]),
                                static_cast<double>(pos[1]), static_cast<double>(hp), level, brain.c_str(),
                                g_state.missions.MissionOf(b.creature), static_cast<double>(p.gold), items);
    if (n < 0 || len + static_cast<std::size_t>(n) >= size) break;
    len += static_cast<std::size_t>(n);
  }
}

namespace {

// D6b: missions (open per team and type), the plan layer, shopping, the fire tracker; logged by Tick every 10 s as
// "stats bot missions:" (the "stats bots:" line is full).
void FormatMissionStats(char* buf, std::size_t size) {
  std::size_t len = 0;
  {
    std::string open;
    for (int team = 1; team <= 2; ++team)
      for (const gb::Mission* m : g_state.missions.Open(team)) {
        char b[64];
        std::snprintf(b, sizeof b, " %u:t%d %s %zu/%d;", m->id, m->team, MissionName(m->type), m->bots.size(), m->agents);
        open += b;
      }
    std::string types;
    for (int t = 1; t < 14; ++t)
      if (g_ms.byType[t] || g_ms.acceptedByType[t]) {
        char b[48];
        std::snprintf(b, sizeof b, " %s %u/%u", MissionName(t), g_ms.byType[t], g_ms.acceptedByType[t]);
        types += b;
      }
    const int n = std::snprintf(
        buf + len, size - len,
        "ticks %u (failed %u), checks %.0f, CheckEnds %.0f, Lua errors %.0f, open %zu:%s requested/"
        "taken by type:%s; ended: CheckEnd %u, expired %u, target gone %u; plan layer: accepted %u, refused %u, opened %u, "
        "again %u, kept %u, left %u; nBotHost mission calls %u/%u/%u/%u/%u/%u/%u/%u/%u; shopping: calls %u/%u/%u/%u, bought %u "
        "items and %u potions for %.0f gold, refused %u, away from the shop %u, bots without a build %u, guide builds %u (bought %u, choices %u, deferred %u), "
        "host shop visits %u (%u items, %u potions); fire tracker %s (IsPositionSafe %u, unsafe %u, off the grid %u, fire zone stub %s, skipped %u)",
        g_ms.ticks, g_ms.tickFailed, g_ms.checks, g_ms.checkEnds, g_ms.luaErrors, g_state.missions.Count(),
        open.empty() ? " none;" : open.c_str(), types.empty() ? " none" : types.c_str(), g_ms.endedByCheck, g_ms.expired,
        g_ms.gone, g_ms.planAccepted, g_ms.planRefused, g_ms.planOpened, g_ms.planAgain, g_ms.planKept, g_ms.planLeft,
        g_ms.calls[0],
        g_ms.calls[1], g_ms.calls[2], g_ms.calls[3], g_ms.calls[4], g_ms.calls[5], g_ms.calls[6], g_ms.calls[7],
        g_ms.calls[8], g_ss.calls[0], g_ss.calls[1], g_ss.calls[2], g_ss.calls[3], g_ss.bought, g_ss.potions,
        static_cast<double>(g_ss.spent), g_ss.refused, g_ss.awayFromShop, g_ss.noBuild, g_ss.guideBots, g_ss.guideBought,
        g_ss.guideChoices, g_ss.guideDeferred, g_ss.visits, g_ss.visitItems, g_ss.visitPotions, g_fs.on ? "on" : "off",
        g_fs.calls, g_fs.unsafe,
        g_fs.offGrid, g_fs.zonesStubbed ? "on" : "off", g_fs.zonesSkipped);
    if (n < 0 || len + static_cast<std::size_t>(n) >= size) return;
    len += static_cast<std::size_t>(n);
  }
}

}  // namespace

}  // namespace bots
