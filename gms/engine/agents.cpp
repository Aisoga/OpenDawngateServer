// Agent AI per frame (D1b-live): see agents.h.
#include "agents.h"

#include <windows.h>

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <map>
#include <string>
#include <unordered_map>
#include <vector>

#include "core/agents/aggro.h"
#include "core/agents/brain.h"
#include "core/agents/leash.h"
#include "core/agents/minion_target.h"
#include "core/agents/recall.h"
#include "core/agents/reflex.h"
#include "core/agents/tower_target.h"
#include "core/bots/lanes.h"
#include "bots.h"
#include "director.h"
#include "game.h"
#include "log.h"
#include "wells.h"

using namespace game;

namespace agents {
namespace {

Options g_opt;
bool g_installed;
bool g_blackboardOk;  // the blackboard functions' prologues matched
bool g_brainOk;       // AIConditions_Evaluate's prologue matched
unsigned g_frame = 1;
void* g_sim;
gms::agents::ShaperHitLog g_shaperHits;  // MINTARGET: shaper-on-shaper hits (OnDamaged)
gms::agents::ShaperHitHistory g_towerHits;  // TOWERAGGRO: shaper-on-shaper hits with the victim's position (OnDamaged)

// AgentBehaviorTree_Tick's prologue (sub esp, 8; push esi; mov esi, [esp+0x10]).
constexpr unsigned char kAgentTickBytes[] = {0x83, 0xEC, 0x08, 0x56, 0x8B, 0x74, 0x24, 0x10};

// The leaves of the two static trees by their decider record (tree data +0x2DC holds the active one; the id hashes at
// +0x2D8 repeat, 0x0A288B82 is five leaves). Numbers as in docs/AGENT_AI.md §2.2 (the default tree's as D1-D5).
struct Leaf {
  std::uintptr_t record;
  const char* name;
};
constexpr Leaf kLeaves[] = {
    {0x0141E0A8, "1 DeathAbility"},     {0x0141E130, "2 Death"},          {0x0141E1B8, "3 AbilityHoldsAgent"},
    {0x0141E240, "4 GameOver"},         {0x0141E2C8, "5 Idle"},           {0x0141E350, "6 Disconnected"},
    {0x0141E3D8, "7 ClearRequests"},    {0x0141E460, "8 UseAbility"},     {0x0141E4E8, "9 ClearRequests"},
    {0x0141E570, "10 MoveToRange"},     {0x0141E5F8, "11 FollowObject"},  {0x0141E680, "12 Pursue"},
    {0x0141E708, "13 PhaseDefault"},    {0x0141E790, "14 CombatIdle"},    {0x0141E818, "15 CombatIdleDefault"},
    {0x0141E8A0, "16 PassiveIdle"},     {0x0141E928, "17 IdleDefault"},   {0x0141DD80, "D1 DeathAbility"},
    {0x0141DE08, "D2 ClearRequests"},   {0x0141DE90, "D3 AbilityHoldsAgent"}, {0x0141DF18, "D4 UseAbility"},
    {0x0141DFA0, "D5 Idle"},
};
constexpr int kNumLeaves = static_cast<int>(sizeof kLeaves / sizeof kLeaves[0]);
constexpr std::uintptr_t kLeafDeath = 0x0141E130;
constexpr std::uintptr_t kLeafDeathAbility = 0x0141E0A8;
constexpr std::uintptr_t kLeafDeathAbilityDefault = 0x0141DD80;
constexpr std::uintptr_t kLeafPursue = 0x0141E680;  // 12: nBehavior_Follow (AIDefinition.targetTooFar if set)
constexpr std::uintptr_t kLeafIdleDefault = 0x0141E928;  // 17: the tree's last resort, nothing to do
constexpr std::uintptr_t kLeafIdle = 0x0141E2C8;         // 5

int LeafIndex(std::uintptr_t record) {
  for (int i = 0; i < kNumLeaves; ++i)
    if (kLeaves[i].record == record) return i;
  return -1;
}
const char* LeafName(std::uintptr_t record) {
  const int i = LeafIndex(record);
  if (i >= 0) return kLeaves[i].name;
  if (!record) return "none";
  static char buf[4][24];  // a record outside the leaf table (the shaper's tree, which runs no leaf: its root group)
  static unsigned k;
  char* b = buf[k++ & 3];
  std::snprintf(b, sizeof buf[0], "?%08X", static_cast<unsigned>(record));
  return b;
}

// Behavior tree data (object +0x89C, AGENT_AI.md §1.5).
namespace bt {
constexpr std::uintptr_t kActiveLeafId = 0x2D8;
constexpr std::uintptr_t kActiveLeaf = 0x2DC;  // the active leaf's decider record (BehaviorTree_Update 0x008B2910)
constexpr std::uintptr_t kFlags = 0x700;       // persistent agent flags (2 creature tree, 8 pursue, 0x20 bot, 0x40)
constexpr std::uintptr_t kTicking = 0x704;     // re-entrancy guard
}  // namespace bt

constexpr unsigned kBlackboardPeriodMs = 500;  // cAgentBlackboard +0x08 (constructor, AgentBlackboard_Init)

struct AgentState {
  std::uintptr_t leaf = 0;
  unsigned lastFrame = 0;  // frame of the last tree tick
  unsigned seenFrame = 0;  // frame the agent loop last saw it
  double deathAt = -1;     // game seconds the Death leaf (2) or a death ability (1) started
  float deathPos[3] = {};
  bool movedLogged = false;
  const char* noun = "?";
  // step 2: the blackboard update
  bool scheduled = false;
  unsigned long long nextUpdateMs = 0;
  std::uint32_t bestTarget = 0;   // the best target the last update reported (OnBestTargetChanged)
  bool battleReady = false;
  std::string listSig;            // the aggro list's ids after the last update (logged on change)
  unsigned updates = 0;
  // step 3: the brain (host-side state; the brain's own per-node arrays at +0x30 stay as AgentBrain_Init left them)
  bool brainSeen = false;
  int node = -1;                                   // the current AI node
  unsigned long long nodeEnteredMs = 0;
  std::vector<unsigned long long> gambitReadyMs;   // per gambit of the current node
  int seqCursor = -1;
  std::vector<unsigned long long> linkReadyMs, linkNextCheckMs;
  std::vector<char> linkFired;
  int lastResult = 0x7FFFFFFF;
  std::uint32_t lastGuid = 0;
  bool pursue = false;
  unsigned requests = 0;
  unsigned long long nextThinkMs = 0;  // after a step without a usable gambit: think again 100 ms later
  int lastSlot = -2;
  // NPCTARGET: the object's nNPCType (-99 not read yet) and the last logged request of a watched NPC
  int npcType = -99;
  int npcLogResult = 0x7FFFFFFF, npcLogGambit = -1;
  std::uint32_t npcLogTarget = 0;
  // D6a: a bot's brain (the plan and phase layer; host-side timers, the engine's +0x14/+0x18 written on changes)
  int isBot = -1;                      // GameObject_IsBot, asked once
  int botPlan = -2, botPhase = -1;     // -2: not seen yet
  unsigned long long planEnteredMs = 0, phaseEnteredMs = 0, botNextThinkMs = 0;
  std::vector<unsigned long long> nodeLinkReady, nodeLinkNext, phaseLinkReady, phaseLinkNext;
  std::vector<char> nodeLinkFired, phaseLinkFired;
  std::string botBehavior;             // the phase's default behavior last set
  // D6c: the timers of every AI node visited, kept across visits. The engine's brain holds one state block per AI node
  // (brain +0x30, 0x350 bytes a node: per output link a flag word and three u64 timers, per Plan.output link the same,
  // per gambit two u64 and a byte), which AgentBrain_Init (0x008AE050) and AgentBrain_InitBot (0x008AD970) zero once
  // and nothing else writes; so a link's cooldown, its next check, its activated-once bit and a gambit's cooldown
  // belong to the node and outlive a visit [inferred: the update that read them is stripped]. The vectors above are the
  // current node's; NodeTimers keeps the others. [host] brain_node_timers=0: D6a's fresh timers on every visit.
  struct NodeTimers {
    std::vector<unsigned long long> linkReady, linkNext, phaseLinkReady, phaseLinkNext, gambitReady;
    std::vector<char> linkFired, phaseLinkFired;
  };
  std::unordered_map<int, NodeTimers> nodeTimers;
  // D6c: plan bounces (a plan left again within kQuickExitMs of entering it)
  int prevPlan = -1;
  unsigned long long prevPlanLeftMs = 0;
  // D6d: a bot's pursuit of a point (its gambit at a point was out of range): the point the blackboard's target position
  // holds, since when, and the distance then
  bool pointPursuit = false;
  float point[3] = {};
  unsigned long long pointSinceMs = 0;
  float pointStartDistance = 0.f;
  std::uint32_t pointDiagTarget = 0;  // [host] bot_pursue_point=0: the object the last logged point request had
  // BOTIDLE: the last hit the bot took (OnDamaged), when it began to stand without an acting gambit, the pursuit watchdog's
  // window (gambit, target, since when, the distance then) and the last skipped refusal (logged on change only)
  std::uint32_t lastAttacker = 0;
  unsigned long long lastHitMs = 0, idleSinceMs = 0, pwSinceMs = 0;
  int pwGambit = -1;
  std::uint32_t pwTarget = 0;
  float pwDist = 0.f;
  int skipGambit = -1, skipResult = 0, fallbackResult = 0x7FFFFFFF;
  // BOTREFLEX: the last tower that hit the bot and the last enemy shaper that did (OnDamaged), and the reflex now active
  std::uint32_t lastTowerId = 0, lastShaperAttacker = 0;
  unsigned long long lastTowerHitMs = 0, lastShaperHitMs = 0;
  int reflexKind = 0;
  unsigned long long reflexLogMs = 0;
  // BOTTEMPO: the hold watchdog's window (the instance holding the agent, since when, where the bot stood then)
  unsigned holdInst = 0;
  unsigned long long holdSinceMs = 0;
  float holdPos[2] = {};
  // MINJUNGLE: the minion watch (a lane minion standing still away from its lane): the aggro type (-99 not read yet),
  // where it stood since when, the next check, whether it is counted as idle off its lane now
  int watchAggroType = -99;
  float watchAnchor[2] = {};
  unsigned long long watchSinceMs = 0, watchNextMs = 0;
  bool watchIdle = false;
  unsigned long long watchResetMs = 0;  // the unstick's last default-behavior restart in this episode (0: none)
  // MINJUNGLE: the chase rule's state (gms/core/agents/minion_target.h) and the chase log (a shaper as the best target:
  // since when, the minion's lane distance then and its largest since, why it ended)
  gms::agents::MinionChase chase;
  std::uint32_t chaseLogTarget = 0;
  unsigned long long chaseLogSinceMs = 0;
  float chaseStartLane = 0.f, chaseMaxLane = 0.f, chaseStartPos[2] = {};
  const char* chaseEndWhy = nullptr;
  // LEASH (gms/core/agents/leash.h): the class (-1 not known yet, 0 none, 1 jungle creature, 2 well worker), its home,
  // its group (a creep's camp marker, a worker's well), its limits, the rule's state and the fight log (the target,
  // since when, the farthest from home, why it ended)
  int leashClass = -1;
  float leashHome[3] = {};
  std::uint32_t leashGroup = 0;
  gms::agents::LeashOptions leashOpt;
  gms::agents::LeashState leash;
  std::uint32_t fightTarget = 0;
  unsigned long long fightSinceMs = 0;
  float fightMaxOwn = 0.f, fightMaxTarget = 0.f;
  const char* fightEndWhy = nullptr;
  // MINPURSUE (gms/core/agents/minion_target.h MinionFollowCheck): a lane minion's running Pursue leaf: the object it
  // follows (its best target when the leaf started; kFollowUnknown: none then), since when, from where, the farthest it
  // walked from there and from its lane, why it ended
  int followAggroType = -99;  // the aggro type, read when a Pursue leaf first starts (-99 not yet)
  std::uint32_t followTarget = 0;
  unsigned long long followSinceMs = 0;
  float followStart[2] = {}, followStartLane = 0.f, followMaxWalk = 0.f, followMaxLane = 0.f;
  const char* followEndWhy = nullptr;
  // TOWERAGGRO (gms/core/agents/tower_target.h): -1 not known yet, 0 not under the rule, 1 a tower under it; its
  // defended-against shapers
  int towerRule = -1;
  gms::agents::TowerDefend towerDefend;
};
std::unordered_map<std::uint32_t, AgentState> g_agents;  // by object id
gms::agents::LeashGroupDrops g_leashDrops;                // LEASH: what a camp or a well's workers dropped together

struct Stats {
  unsigned frames = 0, ticks = 0, refused = 0, exceptions = 0, viaCommands = 0;
  unsigned leafChanges = 0, deaths = 0, deathAbilities = 0, deletedAfterDeath = 0;
  unsigned agentsLastFrame = 0, ticksLastFrame = 0;
  double tickMsLastFrame = 0, tickMsMax = 0;
  // step 2
  unsigned updates = 0, forced = 0, perceived = 0, added = 0, addRefused = 0, removed = 0, reordered = 0;
  unsigned bestChanges = 0, events = 0, battleReadyChanges = 0, engagedAgents = 0, damageHooks = 0, callsForHelp = 0;
  unsigned removedBy[6] = {};  // gone/dead, invalid, untargetable/unattackable, invisible, beyond drop range, tether
  unsigned bbExceptions = 0;
  unsigned madeVisible = 0;
  // step 3
  unsigned brainSteps = 0, requests = 0, started = 0, outOfRange = 0, requestRefused = 0, noTarget = 0, conditionsRun = 0;
  unsigned linksFired = 0, brainExceptions = 0, pursueOn = 0, pursueOff = 0;
  unsigned nodeTimersKept = 0;  // D6c: NPC node entries that got their timers back from an earlier visit
  unsigned heldThinks = 0;      // D6c: NPC brain steps without gambits: an uninterruptible ability held the agent
  // NPCTARGET: requests on a condition's own target / the best target, condition lists run, gambits a lazy walk passed
  // over, thinks a waitOnCondition gambit held, thinks without a best target, out-of-range refusals passed on (and the
  // thinks in which a later gambit then acted)
  unsigned npcOwnTargets = 0, npcBestTargets = 0, conditionLists = 0, npcPassedOver = 0, npcWaiting = 0, npcNoBest = 0;
  unsigned npcFallthroughs = 0, npcFallthroughActed = 0;
  unsigned prunedAbilities = 0, prunedModifiers = 0;
  // D5: nBattlegrounds.AddBaseStructuresToAggroList
  unsigned baseCalls = 0, baseAdded = 0, baseMarked = 0, baseSkipped = 0, baseKeptBeyondRange = 0;
  unsigned minShaperDropped = 0, minShaperLeash = 0, minShaperDefend = 0;  // MINTARGET
  // MINJUNGLE: the minion watch
  unsigned watchEpisodes = 0, watchRecovered = 0, watchDiedIdle = 0, watchMaxNow = 0, watchLogged = 0, watchResets = 0;
  // MINJUNGLE: the chase rule's drops and the chases of shapers (a shaper as a lane minion's best target)
  unsigned chaseDropLane = 0, chaseDropTime = 0, chaseDropDistance = 0, chaseIgnored = 0, chases = 0;
  unsigned chaseLogged = 0;
  double chaseSecondsSum = 0, chaseSecondsMax = 0, chaseLaneSum = 0, chaseLaneMax = 0;
  // LEASH, per class (0 creeps, 1 workers): fights (a target as the best target), their length and the farthest the
  // NPC got from home, the rule's drops by kind (own, chase, target, group), hits not taken onto the list, early updates
  unsigned fights[2] = {}, fightLogged = 0, classLogged = 0, workerClassLogged = 0, leashDrops[2][5] = {}, leashHitsSkipped[2] = {}, leashForced = 0, leashResets = 0;
  double fightSecondsSum[2] = {}, fightSecondsMax[2] = {}, fightOwnSum[2] = {}, fightOwnMax[2] = {};
  // MINPURSUE: the lane minions' follows (Pursue leaves): how many, how long, how far walked and from the lane, how
  // many over 40 units, the restarts by kind (not the best target, list empty, lane leash)
  unsigned follows = 0, followsOver40 = 0, followLogged = 0, followResets[4] = {};
  double followSecondsSum = 0, followSecondsMax = 0, followWalkSum = 0, followWalkMax = 0, followLaneSum = 0,
         followLaneMax = 0;
  // TOWERAGGRO: shaper-on-shaper hits recorded, defend locks begun and released (left the range, left the list),
  // updates whose best target was a shaper while a non-player stood on the list (defended or not), tower log lines
  unsigned towerHits = 0, towerLocks = 0, towerReleasedRange = 0, towerReleasedList = 0, towerShaperDefended = 0,
           towerShaperOther = 0, towerUpdates = 0, towerLogged = 0;
};
Stats g_st;
unsigned g_logLines;

bool LogBudget() {
  ++g_logLines;
  return g_logLines <= static_cast<unsigned>(g_opt.logLimit) || g_logLines % 100 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  Log("EXCEPTION in agents (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
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

unsigned long long NowMs(void* sim) {
  __try {
    return GetTimeMs(sim);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return 0;
  }
}

double PreciseMs() {
  static LARGE_INTEGER freq;
  if (!freq.QuadPart) QueryPerformanceFrequency(&freq);
  LARGE_INTEGER c;
  QueryPerformanceCounter(&c);
  return static_cast<double>(c.QuadPart) * 1000.0 / static_cast<double>(freq.QuadPart);
}

// -1 exception, 0 refused (re-entry: the tree asserts and returns 0), 1 ticked.
int SehAgentTick(void* object, float dt) {
  __try {
    return AgentTickDt(object, dt) ? 1 : 0;
  } __except (SehNote(GetExceptionInformation(), "AgentBehaviorTree_Tick")) {
    return -1;
  }
}

// Every live object with tree data and no pending delete.
bool SehCollect(void* sim, std::vector<void*>* out) {
  __try {
    void* om = Field<void*>(sim, sim::kObjectManager);
    int it = ObjMgrIterBegin(om);
    for (void* o = ObjMgrIterNext(om, &it); o; o = ObjMgrIterNext(om, &it))
      if (Field<void*>(o, obj::kBehaviorTree) && !Field<unsigned char>(o, obj::kPendingDelete)) out->push_back(o);
    return true;
  } __except (SehNote(GetExceptionInformation(), "collect")) {
    return false;
  }
}

// The object's id, false if it is marked for delete (or unreadable).
bool SehLiveId(void* o, std::uint32_t* id) {
  __try {
    if (Field<unsigned char>(o, obj::kPendingDelete)) return false;
    *id = Field<std::uint32_t>(o, gobj::kId);
    return true;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

bool SehObjectId(void* o, std::uint32_t* id) {
  __try {
    *id = Field<std::uint32_t>(o, gobj::kId);
    return true;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

const char* NounName(void* o) {
  void* noun = Field<void*>(o, gobj::kNoun);
  const char* name = noun ? *static_cast<const char**>(noun) : nullptr;
  if (!name) return "?";
  volatile char probe = name[0];
  (void)probe;
  return name;
}

const char* SehNounName(void* o) {
  __try {
    return NounName(o);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return "?";
  }
}

struct Snapshot {
  std::uint32_t id = 0;
  std::uintptr_t leaf = 0;
  float pos[3] = {};
  float hp = 0;
  bool hasHp = false;
  const char* noun = "?";
};

bool SehSnapshot(void* o, Snapshot* s) {
  __try {
    s->id = Field<std::uint32_t>(o, gobj::kId);
    void* btd = Field<void*>(o, obj::kBehaviorTree);
    s->leaf = btd ? Field<std::uintptr_t>(btd, bt::kActiveLeaf) : 0;
    std::memcpy(s->pos, static_cast<char*>(o) + gobj::kPosition, sizeof s->pos);
    void* c = Field<void*>(o, combatant::kOfObject);
    s->hasHp = c != nullptr;
    s->hp = c ? Field<float>(c, combatant::kHitPoints) : 0.f;
    s->noun = NounName(o);
    return true;
  } __except (SehNote(GetExceptionInformation(), "snapshot")) {
    return false;
  }
}

AgentState& StateOf(std::uint32_t id) { return g_agents[id]; }

int SehAggroTypeOf(void* o);
std::uint32_t SehBestTarget(void* agentPtr);

// ---- MINPURSUE: a lane minion's follows (the Pursue leaf, nBehavior_Follow) -----------------------------------------

constexpr std::uint32_t kFollowUnknown = 0xFFFFFFFFu;  // a Pursue leaf that started without a best target

float LaneDistanceAt(const float* p) {
  float d = 0.f;
  return ::director::NearestLane(p[0], p[1], &d) != 0 ? d : 0.f;
}

// The farthest the follow got from where it began and from the lane, at its updates and its end.
void FollowNote(AgentState& a, const float* pos) {
  const float dx = pos[0] - a.followStart[0], dy = pos[1] - a.followStart[1], walk = std::sqrt(dx * dx + dy * dy);
  if (walk > a.followMaxWalk) a.followMaxWalk = walk;
  const float lane = LaneDistanceAt(pos);
  if (lane > a.followMaxLane) a.followMaxLane = lane;
}

std::uint32_t SehBestOf(void* o) {
  __try {
    void* ag = Field<void*>(o, agent::kOfObject);
    return ag ? GetBestTarget(ag) : 0u;
  } __except (SehNote(GetExceptionInformation(), "GetBestTarget (follow)")) {
    return 0;
  }
}

// A lane minion entered the Pursue leaf: it follows its best target of this frame (the leaf's start took it).
void FollowBegin(void* o, AgentState& a, const float* pos, unsigned long long nowMs) {
  if (a.followAggroType == -99) a.followAggroType = SehAggroTypeOf(o);
  if (a.followAggroType != gms::agents::kAggroMinion) return;
  const std::uint32_t best = SehBestOf(o);
  a.followTarget = best ? best : kFollowUnknown;
  a.followSinceMs = nowMs;
  a.followStart[0] = pos[0];
  a.followStart[1] = pos[1];
  a.followStartLane = a.followMaxLane = LaneDistanceAt(pos);
  a.followMaxWalk = 0.f;
  a.followEndWhy = nullptr;
}

// A follow ended (the leaf ended, or the rule restarted the default behavior): stats and the "minions: follow:" line.
void FollowEnd(AgentState& a, std::uint32_t id, const float* pos, unsigned long long nowMs, const char* why) {
  if (!a.followTarget) return;
  FollowNote(a, pos);
  const double secs = static_cast<double>(nowMs - a.followSinceMs) / 1000.0;
  ++g_st.follows;
  if (a.followMaxWalk > 40.f) ++g_st.followsOver40;
  g_st.followSecondsSum += secs;
  if (secs > g_st.followSecondsMax) g_st.followSecondsMax = secs;
  g_st.followWalkSum += a.followMaxWalk;
  if (a.followMaxWalk > g_st.followWalkMax) g_st.followWalkMax = a.followMaxWalk;
  g_st.followLaneSum += a.followMaxLane;
  if (a.followMaxLane > g_st.followLaneMax) g_st.followLaneMax = a.followMaxLane;
  if (g_st.followLogged < static_cast<unsigned>(g_opt.minionWatchLogLimit) || a.followMaxWalk > 40.f) {
    ++g_st.followLogged;
    Log("minions: follow: %08X %s followed %08X %.1f s (t %.1f-%.1f s), %.1f units from where it began at most, "
        "from %.1f to %.1f units from its lane (%.1f at most); ended: %s",
        id, a.noun, a.followTarget, secs, static_cast<double>(a.followSinceMs) / 1000.0,
        static_cast<double>(nowMs) / 1000.0, static_cast<double>(a.followMaxWalk),
        static_cast<double>(a.followStartLane), static_cast<double>(LaneDistanceAt(pos)),
        static_cast<double>(a.followMaxLane), why);
  }
  a.followTarget = 0;
  a.followEndWhy = nullptr;
}

// After a tree tick: the leaf log, the death watch.
void Observe(void* o, double now) {
  Snapshot s;
  if (!SehSnapshot(o, &s)) return;
  AgentState& a = StateOf(s.id);
  a.seenFrame = g_frame;
  a.noun = s.noun;
  if (s.leaf != a.leaf) {
    ++g_st.leafChanges;
    if (LogBudget())
      Log("agents: %08X %s leaf %s -> %s (t %.2f s, hp %.1f, at (%.2f, %.2f))", s.id, s.noun, LeafName(a.leaf),
          LeafName(s.leaf), now, s.hp, s.pos[0], s.pos[1]);
    const bool dying = s.leaf == kLeafDeath || s.leaf == kLeafDeathAbility || s.leaf == kLeafDeathAbilityDefault;
    if (a.leaf == kLeafPursue && a.followTarget)  // MINPURSUE: the follow ended by itself (reached, the target gone, ...)
      FollowEnd(a, s.id, s.pos, static_cast<unsigned long long>(now * 1000.0 + 0.5),
                dying ? "the minion died" : "the leaf ended");
    if (dying && a.deathAt < 0) {
      a.deathAt = now;
      std::memcpy(a.deathPos, s.pos, sizeof a.deathPos);
      if (s.leaf == kLeafDeath) ++g_st.deaths;
      else ++g_st.deathAbilities;
    }
    a.leaf = s.leaf;
  }
  // MINPURSUE: a Pursue leaf without a follow record (new, or started again after the rule's restart)
  if (s.leaf == kLeafPursue && !a.followTarget && !(s.hasHp && s.hp <= 0.f))
    FollowBegin(o, a, s.pos, static_cast<unsigned long long>(now * 1000.0 + 0.5));
  if (a.deathAt >= 0 && !a.movedLogged) {
    const float dx = s.pos[0] - a.deathPos[0], dy = s.pos[1] - a.deathPos[1];
    if (dx * dx + dy * dy > 0.01f) {
      a.movedLogged = true;
      Log("agents: %08X %s moved %.2f units after its death leaf (t %.2f s)", s.id, s.noun,
          static_cast<double>(std::sqrt(dx * dx + dy * dy)), now);
    }
  }
}

// ---- step 2: the blackboard update (AGENT_AI.md §6.2-§6.4) -------------------------------------------------------

// What the update needs of an object.
struct ObjInfo {
  void* obj = nullptr;
  std::uint32_t id = 0;
  float pos[3] = {};
  float radius = 0;
  std::uint8_t team = 0;
  bool player = false;  // mbPlayerControlled (+0x56)
  bool hasHp = false;
  float hp = 0;
};

bool SehObjInfo(void* o, ObjInfo* out) {
  __try {
    out->obj = o;
    out->id = Field<std::uint32_t>(o, gobj::kId);
    std::memcpy(out->pos, static_cast<char*>(o) + gobj::kPosition, sizeof out->pos);
    out->radius = FootprintRadius(o);
    if (!(out->radius >= 0.f && out->radius < 100.f)) out->radius = 0.f;
    out->team = Field<std::uint8_t>(o, gobj::kTeam);
    out->player = Field<unsigned char>(o, gobj::kPlayerControlled) != 0;
    void* c = Field<void*>(o, combatant::kOfObject);
    out->hasHp = c != nullptr;
    out->hp = c ? Field<float>(c, combatant::kHitPoints) : 0.f;
    return true;
  } __except (SehNote(GetExceptionInformation(), "object info")) {
    return false;
  }
}

void* SehFind(std::uint32_t id) {
  __try {
    return id ? GetObjectById(id) : nullptr;
  } __except (SehNote(GetExceptionInformation(), "find object")) {
    return nullptr;
  }
}

// The agent's own data for the update.
struct AgentInfo {
  ObjInfo self;
  void* agent = nullptr;
  bool hasBrain = false;
  bool hasNpc = false;
  float aggro = 0, help = 0, drop = 0, tether = 0;
  int aggroType = 0;
  int timer = 0;  // +0x0C
};

// RANGES: the NPC class data is shared by every object of the class, so it is tuned once in place: the parasite's
// (type 8) drop range and tether at least parasiteReach, so the list keeps a ranged attacker (its ranged attack reaches 30) instead of pruning
// it beyond 27.05 and leaving the parasite without a target.
std::vector<char*> g_tunedRanges;
void TuneRanges(char* d) {
  for (char* t : g_tunedRanges)
    if (t == d) return;
  g_tunedRanges.push_back(d);
  const int type = Field<int>(d, npc::kType);
  if (type == 8 && g_opt.parasiteReach > 0.f) {
    float& drop = Field<float>(d, npc::kDropAggroRange);
    float& tether = Field<float>(d, npc::kTetherRange);
    if (drop < g_opt.parasiteReach) drop = g_opt.parasiteReach;
    if (tether < g_opt.parasiteReach) tether = g_opt.parasiteReach;
    Log("agents: RANGES: parasite class data %p: aggro %.1f, drop %.1f, tether %.1f", static_cast<void*>(d),
        static_cast<double>(Field<float>(d, npc::kAggroRange)), static_cast<double>(drop), static_cast<double>(tether));
  }
}

bool SehAgentInfo(void* o, AgentInfo* out) {
  __try {
    out->agent = Field<void*>(o, agent::kOfObject);
    if (!out->agent) return false;
    out->hasBrain = Field<void*>(o, obj::kBrain) != nullptr;
    void* npcAsset = Field<void*>(o, npc::kOfObject);
    char* d = npcAsset ? Field<char*>(npcAsset, noun::kData) : nullptr;
    out->hasNpc = d != nullptr;
    if (d) {
      TuneRanges(d);
      out->aggro = Field<float>(d, npc::kAggroRange);
      out->help = Field<float>(d, npc::kCallForHelpRange);
      out->drop = Field<float>(d, npc::kDropAggroRange);
      out->tether = Field<float>(d, npc::kTetherRange);
    }
    out->aggroType = GetAggroType(o);
    out->timer = Field<int>(out->agent, agent::kUpdateTimer);
    return true;
  } __except (SehNote(GetExceptionInformation(), "agent info")) {
    return false;
  }
}

// The perception circle and the spatial query: every live, targetable player, NPC and structure of either team in it
// but the agent itself, nearest first (GameObjects_GetInRadius with distance sorting).
// The broadphase writes its raw results here first: GameObjects_GetInRadius asserts unless the raw count < max, and the
// physics query (GamePhysicsPhysX.cpp 1151, on every query in d1b_run2) unless max <= its internal buffer of 256.
constexpr int kQueryMax = 256;
std::uint32_t g_queryIds[kQueryMax];

int SehPerceive(void* sim, void* agentPtr, const ObjInfo& self, float centre[3], float* radius) {
  __try {
    PerceptionCircle(agentPtr, centre, radius, Field<void*>(sim, sim::kObjectManager));
    ObjectQuery q = {};
    q.sortType = 1;              // nGetObjectsSortType.Distance
    q.targetTypes = 0x77;        // enemy and allied players, NPCs and structures
    q.team = self.team;
    q.self = self.id;            // not the agent itself (nTargetType.Self is not set)
    return GetInRadius(g_queryIds, kQueryMax, centre[0], centre[1], centre[2], *radius, &q);
  } __except (SehNote(GetExceptionInformation(), "perception query")) {
    return -1;
  }
}

struct Perceived {
  ObjInfo info;
  float distance = 0;  // edge distance from the perception centre [assumed]: centre distance minus both footprints
};

bool SehWritePerception(void* agentPtr, const std::vector<Perceived>& list, unsigned* written) {
  __try {
    char* begin = Field<char*>(agentPtr, agent::kPerceptionBegin);
    char* cap = Field<char*>(agentPtr, agent::kPerceptionCap);
    const unsigned room = static_cast<unsigned>((cap - begin) / agent::kPerceptionEntrySize);
    unsigned n = 0;
    for (const Perceived& p : list) {
      if (n >= room) break;
      Field<std::uint32_t>(begin, n * agent::kPerceptionEntrySize) = p.info.id;
      Field<float>(begin, n * agent::kPerceptionEntrySize + 4) = p.distance;
      ++n;
    }
    Field<char*>(agentPtr, agent::kPerceptionEnd) = begin + n * agent::kPerceptionEntrySize;
    *written = n;
    return true;
  } __except (SehNote(GetExceptionInformation(), "write perception")) {
    return false;
  }
}

struct AggroEntry {
  unsigned char raw[agent::kAggroEntrySize];
  std::uint32_t Id() const { return *reinterpret_cast<const std::uint32_t*>(raw); }
  unsigned long long U64(std::size_t off) const { return *reinterpret_cast<const unsigned long long*>(raw + off); }
};

bool SehReadAggro(void* agentPtr, std::vector<AggroEntry>* out) {
  __try {
    char* b = Field<char*>(agentPtr, agent::kAggroBegin);
    char* e = Field<char*>(agentPtr, agent::kAggroEnd);
    const std::size_t n = static_cast<std::size_t>(e - b) / agent::kAggroEntrySize;
    if (n > 512) return false;
    out->resize(n);
    if (n) std::memcpy(out->data(), b, n * agent::kAggroEntrySize);
    return true;
  } __except (SehNote(GetExceptionInformation(), "read aggro list")) {
    return false;
  }
}

// Writes `list` over the vector (same or fewer entries: an erase or a reorder in place) and sets its end.
bool SehWriteAggro(void* agentPtr, const std::vector<AggroEntry>& list) {
  __try {
    char* b = Field<char*>(agentPtr, agent::kAggroBegin);
    char* e = Field<char*>(agentPtr, agent::kAggroEnd);
    const std::size_t had = static_cast<std::size_t>(e - b) / agent::kAggroEntrySize;
    if (list.size() > had) return false;
    if (!list.empty()) std::memcpy(b, list.data(), list.size() * agent::kAggroEntrySize);
    Field<char*>(agentPtr, agent::kAggroEnd) = b + list.size() * agent::kAggroEntrySize;
    return true;
  } __except (SehNote(GetExceptionInformation(), "write aggro list")) {
    return false;
  }
}

// The checks an aggro entry or a perceived enemy must pass (AGENT_AI.md §6.3, [assumed]); 0 = keep, else the reason
// index of Stats::removedBy. A base structure (an entry AddBaseStructures marked, D5) is kept beyond the drop range and
// outside the tether (gms/core/agents/aggro.h KeepByRange).
int SehTargetCheck(const AgentInfo& a, void* target, const ObjInfo& t, float distance, bool forAdd,
                   bool baseStructure = false) {
  __try {
    if (!target || (t.hasHp && t.hp <= 0.f)) return 1;
    if (!ValidateHostile(a.self.team, target, 0)) return 2;
    if (!IsTargetable(target) || !IsAttackable(target)) return 3;
    if (!IsVisibleTo(a.self.obj, target)) return 4;
    const float limit = forAdd ? a.aggro : a.drop;
    const bool inTether = baseStructure || IsInTether(a.agent, target);
    if (!gms::agents::KeepByRange(baseStructure, distance, limit, inTether)) return distance > limit ? 5 : 6;
    return 0;
  } __except (SehNote(GetExceptionInformation(), "target check")) {
    return 1;
  }
}

bool SehAdd(void* agentObj, std::uint32_t id) {
  __try {
    return AddToAggroList(agentObj, id, nullptr, 0, 0) != nullptr;
  } __except (SehNote(GetExceptionInformation(), "Agent_AddToAggroList")) {
    return false;
  }
}

std::uint32_t SehBestTarget(void* agentPtr) {
  __try {
    return GetBestTarget(agentPtr);
  } __except (SehNote(GetExceptionInformation(), "GetBestTarget")) {
    return 0;
  }
}

float SehSecondsSinceCombat(void* o) {
  __try {
    if (!Field<void*>(o, 0x918)) return 1e9f;  // no damage tracker (object +0x918)
    return DamageTrackerSecondsSinceCombat(o);
  } __except (SehNote(GetExceptionInformation(), "DamageTracker_GetSecondsSinceCombat")) {
    return 1e9f;
  }
}

void SehWriteFlags(void* agentPtr, bool engaged, bool inCombat, int battleReady /* -1 keep */) {
  __try {
    Field<unsigned char>(agentPtr, agent::kEngaged) = engaged ? 1 : 0;
    Field<unsigned char>(agentPtr, agent::kInCombat) = inCombat ? 1 : 0;
    Field<unsigned char>(agentPtr, agent::kFlag1E72) = inCombat ? 1 : 0;
    if (battleReady >= 0) Field<unsigned char>(agentPtr, agent::kBattleReady) = battleReady ? 1 : 0;
    Field<int>(agentPtr, agent::kUpdateTimer) = static_cast<int>(kBlackboardPeriodMs);
  } __except (SehNote(GetExceptionInformation(), "write flags")) {
  }
}

// One event on the object's ability and modifier instances, with one object-id argument or none
// (AbilityDataContainer_FireEvent; the record as LuaUtilities_FillOutPrivateTable reads it).
bool SehFireEvent(void* o, int event, const char* argName, std::uint32_t argValue) {
  __try {
    void* container = Field<void*>(o, obj::kAbilityData);
    if (!container) return false;
    alignas(8) unsigned char rec[kEventRecordSize];
    std::memset(rec, 0, sizeof rec);
    Field<const char*>(rec, 0) = argName;
    Field<std::uint32_t>(rec, 0x54) = argValue;
    Field<std::uint16_t>(rec, 0x60) = kEventTypeObjectId;
    FireEvent(container, event, argName ? rec : nullptr, argName ? 1 : 0);
    return true;
  } __except (SehNote(GetExceptionInformation(), "AbilityDataContainer_FireEvent")) {
    return false;
  }
}

std::string Hex8(std::uint32_t v) {
  char b[12];
  std::snprintf(b, sizeof b, "%08X", v);
  return b;
}

// The blackboard update of one agent (AGENT_AI.md §5 step 5.1): perception list, aggro list (prune, add, order),
// flags, events, timer.
// MINJUNGLE: whether a minion still walks its lane (locomotion +0x134, the spline id; 0 once it reached the lane's
// end). False if unreadable.
bool SehOnLane(void* o, bool* onLane) {
  __try {
    void* lo = Field<void*>(o, loco::kOfObject);
    *onLane = lo && Field<std::uint32_t>(lo, loco::kSplineId) != 0;
    return lo != nullptr;
  } __except (SehNote(GetExceptionInformation(), "on lane")) {
    return false;
  }
}

// MINJUNGLE: after a lane minion's aggro update: the chase rule's pursuit clock, and the chase log (a shaper as the
// best target: how long, how far from the lane, why it ended).
void MinionChaseAfterUpdate(void* o, AgentState& st, const AgentInfo& a,
                            const std::vector<gms::agents::AggroCandidate>& cands, std::uint32_t best,
                            unsigned long long nowMs) {
  bool bestIsShaper = false;
  for (const auto& c : cands)
    if (c.id == best) bestIsShaper = c.isPlayer;
  gms::agents::UpdateChase(&st.chase, best, bestIsShaper, a.self.pos, static_cast<std::int64_t>(nowMs));
  float lane = 0.f;
  if (::director::NearestLane(a.self.pos[0], a.self.pos[1], &lane) == 0) lane = 0.f;
  if (st.chaseLogTarget && lane > st.chaseMaxLane) st.chaseMaxLane = lane;
  if (st.chaseLogTarget && best != st.chaseLogTarget) {  // a chase ended
    const double secs = static_cast<double>(nowMs - st.chaseLogSinceMs) / 1000.0;
    const float dx = a.self.pos[0] - st.chaseStartPos[0], dy = a.self.pos[1] - st.chaseStartPos[1];
    ++g_st.chases;
    g_st.chaseSecondsSum += secs;
    if (secs > g_st.chaseSecondsMax) g_st.chaseSecondsMax = secs;
    g_st.chaseLaneSum += st.chaseMaxLane;
    if (st.chaseMaxLane > g_st.chaseLaneMax) g_st.chaseLaneMax = st.chaseMaxLane;
    if (g_st.chaseLogged < static_cast<unsigned>(g_opt.minionWatchLogLimit)) {
      ++g_st.chaseLogged;
      Log("minions: chase: %08X %s chased shaper %08X %.1f s (t %.1f-%.1f s), from %.1f to %.1f units from its lane "
          "(%.1f at most), %.1f units from where it began; ended: %s%s",
          a.self.id, st.noun, st.chaseLogTarget, secs, static_cast<double>(st.chaseLogSinceMs) / 1000.0,
          static_cast<double>(nowMs) / 1000.0, static_cast<double>(st.chaseStartLane), static_cast<double>(lane),
          static_cast<double>(st.chaseMaxLane), static_cast<double>(std::sqrt(dx * dx + dy * dy)),
          st.chaseEndWhy ? st.chaseEndWhy : (best ? "another best target" : "list empty"),
          a.self.hasHp && a.self.hp <= 0.f ? " (the minion died)" : "");
    }
    st.chaseLogTarget = 0;
  }
  if (best && bestIsShaper && st.chaseLogTarget != best) {  // a chase began
    st.chaseLogTarget = best;
    st.chaseLogSinceMs = nowMs;
    st.chaseStartLane = st.chaseMaxLane = lane;
    st.chaseStartPos[0] = a.self.pos[0];
    st.chaseStartPos[1] = a.self.pos[1];
  }
  if (st.chaseLogTarget && lane > st.chaseMaxLane) st.chaseMaxLane = lane;
  st.chaseEndWhy = nullptr;
  (void)o;
}

bool SehResetDefaultBehavior(void* o);
void SehSetTarget(void* o, std::uint32_t target);
std::uint32_t SehRawTarget(void* o);

// MINPURSUE: after a lane minion's aggro update, while its Pursue leaf runs: the follow's distances, and the rule
// (gms/core/agents/minion_target.h MinionFollowCheck): a followed object that is no longer the best target, an empty
// list, or the lane leash while following a shaper ends the follow: the follow target is cleared and the default
// behavior restarted (request 0x400), so the tree decides again in this frame's tick.
void MinionFollowAfterUpdate(void* o, AgentState& st, const AgentInfo& a,
                             const std::vector<gms::agents::AggroCandidate>& cands, std::uint32_t best,
                             unsigned long long nowMs) {
  if (st.leaf != kLeafPursue || !st.followTarget) return;
  FollowNote(st, a.self.pos);
  if (a.self.hasHp && a.self.hp <= 0.f) return;
  bool bestIsShaper = false, bestTaunting = false;
  for (const auto& c : cands)
    if (c.id == best) {
      bestIsShaper = c.isPlayer;
      bestTaunting = c.taunting;
    }
  bool onLane = false;
  float laneDistance = 0.f;
  const bool laneKnown =
      SehOnLane(o, &onLane) && ::director::NearestLane(a.self.pos[0], a.self.pos[1], &laneDistance) != 0;
  gms::agents::MinionFollowOptions fo = g_opt.minionFollow;
  fo.laneLeash = g_opt.minionChase.laneLeash;
  const int why = gms::agents::MinionFollowCheck(fo, st.followTarget, best, bestIsShaper, bestTaunting,
                                                 laneKnown && onLane, laneDistance);
  if (why == gms::agents::kFollowKeep) return;
  const std::uint32_t followed = st.followTarget;
  if (followed != kFollowUnknown && SehRawTarget(o) == followed) SehSetTarget(o, 0);
  if (!SehResetDefaultBehavior(o)) return;
  ++g_st.followResets[why];
  FollowEnd(st, a.self.id, a.self.pos, nowMs, gms::agents::MinionFollowEndName(why));
}

// ---- LEASH: jungle creatures and well workers (gms/core/agents/leash.h) ---------------------------------------------

float LeashDist(const float* a, const float* b) {
  const float dx = a[0] - b[0], dy = a[1] - b[1];
  return std::sqrt(dx * dx + dy * dy);
}

bool SehLeashHome(void* o, float* home, std::uint32_t* camp) {
  __try {
    std::memcpy(home, static_cast<char*>(o) + gobj::kInitialPosition, 12);
    *camp = Field<std::uint32_t>(o, kObjectCampMarker);
    return true;
  } __except (SehNote(GetExceptionInformation(), "leash home")) {
    return false;
  }
}

// Which leash an agent has: a jungle creature (its spawn spot, its camp) or a well's worker (the well's platform). A
// normal-type NPC is asked again at each update until the wells module knows it (a worker is registered right after its
// creation).
bool LeashClassify(void* o, AgentState& st, const AgentInfo& a) {
  if (st.leashClass >= 0) return st.leashClass > 0;
  if (a.self.player) {
    st.leashClass = 0;
    return false;
  }
  if (a.aggroType == gms::agents::kAggroJungleCreature) {
    std::uint32_t camp = 0;
    if (!SehLeashHome(o, st.leashHome, &camp)) return false;
    st.leashClass = 1;
    st.leashGroup = camp ? camp : a.self.id;
    st.leashOpt = g_opt.creepLeash;
    if (++g_st.classLogged <= 40)
      Log("leash: %08X %s jungle creature: home (%.1f, %.1f) (%.1f from it now), camp %u; rule %s: own leash %.1f, "
          "target leash %.1f, home radius %.1f, chase time %lld ms",
          a.self.id, st.noun, static_cast<double>(st.leashHome[0]), static_cast<double>(st.leashHome[1]),
          static_cast<double>(LeashDist(a.self.pos, st.leashHome)), camp, st.leashOpt.enabled ? "on" : "off",
          static_cast<double>(st.leashOpt.ownLimit), static_cast<double>(st.leashOpt.ownLimit + st.leashOpt.targetMargin),
          static_cast<double>(st.leashOpt.homeRadius), static_cast<long long>(st.leashOpt.chaseMs));
    return true;
  }
  if (a.aggroType == gms::agents::kAggroNormal) {
    std::uint32_t well = 0;
    float radius = 0.f;
    if (!::wells::WorkerPlatform(a.self.id, &well, st.leashHome, &radius)) return false;
    st.leashClass = 2;
    st.leashGroup = well;
    st.leashOpt = g_opt.workerLeash;
    st.leashOpt.ownLimit = radius + g_opt.workerLeash.ownLimit;  // the margin beyond the platform
    st.leashOpt.homeRadius = radius;
    if (++g_st.workerClassLogged <= 8)
      Log("leash: %08X %s well worker: well %08X at (%.1f, %.1f), platform radius %.1f, %.1f from the centre now; rule "
          "%s: own leash %.1f, target leash %.1f, chase time %lld ms",
          a.self.id, st.noun, well, static_cast<double>(st.leashHome[0]), static_cast<double>(st.leashHome[1]),
          static_cast<double>(radius), static_cast<double>(LeashDist(a.self.pos, st.leashHome)),
          st.leashOpt.enabled ? "on" : "off", static_cast<double>(st.leashOpt.ownLimit),
          static_cast<double>(st.leashOpt.ownLimit + st.leashOpt.targetMargin), static_cast<long long>(st.leashOpt.chaseMs));
    return true;
  }
  st.leashClass = 0;
  return false;
}

// The rule in the aggro update: the targets a leashed NPC drops now (its own leash or chase time: all of them; else
// each target beyond its reach from the leash's edge, and what its group dropped a moment ago). A taunt holds.
void LeashPrune(AgentState& st, const AgentInfo& a, std::vector<gms::agents::AggroCandidate>* cands,
                std::vector<std::uint32_t>* dropped, unsigned long long nowMs) {
  const int cls = st.leashClass - 1;
  const std::int64_t now = static_cast<std::int64_t>(nowMs);
  const float own = LeashDist(a.self.pos, st.leashHome);
  if (own > st.fightMaxOwn && st.fightTarget) st.fightMaxOwn = own;
  if (!st.leashOpt.enabled) return;
  g_leashDrops.Expire(now);
  const int self = gms::agents::LeashSelfCheck(st.leashOpt, st.leash, own, now);
  const std::size_t before = dropped->size();
  for (const auto& c : *cands) {
    if (c.taunting) continue;
    int why = self;
    if (!why) {
      void* t = SehFind(c.id);
      ObjInfo ti;
      if (t && SehObjInfo(t, &ti) &&
          gms::agents::LeashTargetBeyond(st.leashOpt, LeashDist(ti.pos, st.leashHome) - ti.radius))
        why = gms::agents::kLeashTarget;
    }
    if (!why && g_leashDrops.Ignored(st.leashGroup, c.id, now)) why = gms::agents::kLeashGroup;
    if (!why) continue;
    dropped->push_back(c.id);
    ++g_st.leashDrops[cls][why];
    if (why == gms::agents::kLeashOwn || why == gms::agents::kLeashChase)
      g_leashDrops.Drop(st.leashGroup, c.id, now + st.leashOpt.ignoreMs);  // the camp or the well's workers let go too
    if (c.id == st.fightTarget) st.fightEndWhy = gms::agents::LeashDropName(why);
  }
  if (dropped->size() != before)
    cands->erase(std::remove_if(cands->begin(), cands->end(),
                                [dropped, before](const gms::agents::AggroCandidate& c) {
                                  return std::find(dropped->begin() + static_cast<std::ptrdiff_t>(before),
                                                   dropped->end(), c.id) != dropped->end();
                                }),
                 cands->end());
}

// After the update: the rule's fight clock, and the fight log (a target as the best target: how long, how far the NPC
// got from home, why it ended).
void LeashAfterUpdate(void* o, AgentState& st, const AgentInfo& a, std::uint32_t best, unsigned long long nowMs) {
  const int cls = st.leashClass - 1;
  const float own = LeashDist(a.self.pos, st.leashHome);
  gms::agents::UpdateLeash(&st.leash, st.leashOpt, best, own, static_cast<std::int64_t>(nowMs));
  float targetDist = 0.f;
  if (best) {
    void* t = SehFind(best);
    ObjInfo ti;
    if (t && SehObjInfo(t, &ti)) targetDist = LeashDist(ti.pos, st.leashHome) - ti.radius;
  }
  if (st.fightTarget && best != st.fightTarget) {  // a fight ended
    if (own > st.fightMaxOwn) st.fightMaxOwn = own;
    const double secs = static_cast<double>(nowMs - st.fightSinceMs) / 1000.0;
    ++g_st.fights[cls];
    g_st.fightSecondsSum[cls] += secs;
    if (secs > g_st.fightSecondsMax[cls]) g_st.fightSecondsMax[cls] = secs;
    g_st.fightOwnSum[cls] += st.fightMaxOwn;
    if (st.fightMaxOwn > g_st.fightOwnMax[cls]) g_st.fightOwnMax[cls] = st.fightMaxOwn;
    if (g_st.fightLogged < static_cast<unsigned>(g_opt.leashLogLimit)) {
      ++g_st.fightLogged;
      Log("leash: fight: %08X %s (%s %u) fought %08X %.1f s (t %.1f-%.1f s), farthest %.1f from home (own leash %.1f, "
          "rule %s), %.1f at the end, the target %.1f at most; ended: %s",
          a.self.id, st.noun, cls ? "well" : "camp", st.leashGroup, st.fightTarget, secs,
          static_cast<double>(st.fightSinceMs) / 1000.0, static_cast<double>(nowMs) / 1000.0,
          static_cast<double>(st.fightMaxOwn), static_cast<double>(st.leashOpt.ownLimit),
          st.leashOpt.enabled ? "on" : "off", static_cast<double>(own), static_cast<double>(st.fightMaxTarget),
          a.self.hasHp && a.self.hp <= 0.f ? "it died"
          : st.fightEndWhy                 ? st.fightEndWhy
          : best                           ? "another best target"
                                           : "list empty");
    }
    // the list is empty: a Pursue leaf still follows the agent's mTarget (nBehavior_Follow ends only at the target), so
    // a worker walked on after a shaper for up to 12 s with nothing on its list; the target is cleared and the default
    // behavior (the walk home, the mining loop) restarted
    if (!best && st.leashOpt.enabled && !(a.self.hasHp && a.self.hp <= 0.f)) {
      if (SehRawTarget(o) == st.fightTarget) SehSetTarget(o, 0);
      if (st.leaf == kLeafPursue && SehResetDefaultBehavior(o)) ++g_st.leashResets;
    }
    st.fightTarget = 0;
  }
  if (best && st.fightTarget != best) {  // a fight began
    st.fightTarget = best;
    st.fightSinceMs = nowMs;
    st.fightMaxOwn = own;
    st.fightMaxTarget = targetDist;
  }
  if (st.fightTarget) {
    if (own > st.fightMaxOwn) st.fightMaxOwn = own;
    if (targetDist > st.fightMaxTarget) st.fightMaxTarget = targetDist;
  }
  st.fightEndWhy = nullptr;
}

// Whether the agent's aggro list holds anything (between two updates the damage hook and Agent_CallForHelp add to it).
bool SehListNonEmpty(void* o) {
  __try {
    void* ag = Field<void*>(o, agent::kOfObject);
    return ag && Field<char*>(ag, agent::kAggroEnd) != Field<char*>(ag, agent::kAggroBegin);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

// The groups (camps, wells) whose members a call for help just reached: each member gets its update in the next frame,
// so one beyond its leash or the group's ignore lets go before it walks on (Agent_CallForHelp adds the attacker to every
// camp member, wherever it stands).
std::vector<std::uint32_t> g_leashForceGroups, g_leashCalledNow;  // called since the last frame / in this frame

// Every frame for a leashed NPC with something on its list: the farthest it got, and an update at once when it passed
// its own leash, its chase time ran out or its group was just called (else it walked on for up to a blackboard period).
void LeashWatch(void* o, AgentState& st, unsigned long long nowMs) {
  if (!st.leash.target && !SehListNonEmpty(o)) return;
  ObjInfo self;
  if (!SehObjInfo(o, &self)) return;
  const float own = LeashDist(self.pos, st.leashHome);
  if (st.fightTarget && own > st.fightMaxOwn) st.fightMaxOwn = own;
  if (st.nextUpdateMs <= nowMs || !st.leashOpt.enabled) return;
  const bool called = std::find(g_leashCalledNow.begin(), g_leashCalledNow.end(), st.leashGroup) !=
                      g_leashCalledNow.end();
  if (called || gms::agents::LeashSelfCheck(st.leashOpt, st.leash, own, static_cast<std::int64_t>(nowMs)) !=
                    gms::agents::kLeashKeep) {
    st.nextUpdateMs = nowMs;
    ++g_st.leashForced;
  }
}

// ---- TOWERAGGRO: whom a tower shoots (gms/core/agents/tower_target.h) -------------------------------------------------

int SehNpcType(void* o);
constexpr int kTowerRuleSkipNpcType = 13;  // nNPCType: the Guardian (its gambits pick their own targets, NPCTARGET)

bool TowerLogBudget() {
  ++g_st.towerLogged;
  return g_st.towerLogged <= static_cast<unsigned>(g_opt.towerLogLimit) || g_st.towerLogged % 50 == 0;
}

// Aggro type tower (Bindings, the Locus; nubs have aggro range 0), not the Guardian, and [host] tower_target on.
bool TowerRuleApplies(void* o, AgentState& st, const AgentInfo& a) {
  if (!g_opt.towerTarget.enabled || a.aggroType != gms::agents::kAggroTower || a.aggro <= 0.f) return false;
  if (st.towerRule < 0) {
    st.towerRule = SehNpcType(o) == kTowerRuleSkipNpcType ? 0 : 1;
    if (st.towerRule && TowerLogBudget())
      Log("towers: %08X %s: minions before shapers, a shaper that hits a shaper of team %d within %.1f stays the "
          "target until it leaves %.1f (TOWERAGGRO)",
          a.self.id, st.noun, a.self.team, static_cast<double>(a.aggro), static_cast<double>(a.aggro));
  }
  return st.towerRule == 1;
}

// The new hits since the tower's last update lock their attackers; a lock ends when its shaper left the list or the
// tower's range; the candidates' defend flag is the lock.
void TowerDefendUpdate(AgentState& st, const AgentInfo& a, std::vector<gms::agents::AggroCandidate>* cands,
                       unsigned long long nowMs) {
  gms::agents::TowerDefend& d = st.towerDefend;
  const std::size_t before = d.locked.size();
  d.Scan(g_towerHits, a.self.team, a.self.pos, a.self.radius, a.aggro, static_cast<std::int64_t>(nowMs));
  for (std::size_t i = before; i < d.locked.size(); ++i) {
    ++g_st.towerLocks;
    float dist = -1.f;
    for (const auto& c : *cands)
      if (c.id == d.locked[i]) dist = c.distance;
    if (TowerLogBudget())
      Log("towers: %08X %s defends against %08X (it hit a shaper of team %d within %.1f; %s) (t %.2f s)", a.self.id,
          st.noun, d.locked[i], a.self.team, static_cast<double>(a.aggro),
          dist < 0.f ? "not on the list" : "on the list", static_cast<double>(nowMs) / 1000.0);
  }
  const auto released = d.Release(a.aggro, [cands](std::uint32_t id, float* dist) {
    for (const auto& c : *cands)
      if (c.id == id && c.isPlayer) {
        *dist = c.distance;
        return true;
      }
    return false;
  });
  for (std::uint32_t id : released) {
    bool onList = false;
    for (const auto& c : *cands) onList = onList || c.id == id;
    ++(onList ? g_st.towerReleasedRange : g_st.towerReleasedList);
    if (TowerLogBudget())
      Log("towers: %08X %s no longer defends against %08X: %s (t %.2f s)", a.self.id, st.noun, id,
          onList ? "it left the range" : "it left the list", static_cast<double>(nowMs) / 1000.0);
  }
  for (auto& c : *cands) c.defend = c.isPlayer && !c.taunting && d.Has(c.id);
}

// The check of the rule: a shaper as the best target while a non-player is on the list.
void TowerAfterUpdate(AgentState& st, const std::vector<gms::agents::AggroCandidate>& cands, std::uint32_t best) {
  ++g_st.towerUpdates;
  bool bestShaper = false, bestDefend = false, nonPlayer = false;
  for (const auto& c : cands) {
    if (c.id == best) {
      bestShaper = c.isPlayer;
      bestDefend = c.defend || c.taunting;
    }
    nonPlayer = nonPlayer || !c.isPlayer;
  }
  if (bestShaper && nonPlayer) ++(bestDefend ? g_st.towerShaperDefended : g_st.towerShaperOther);
  (void)st;
}

void UpdateBlackboard(void* sim, void* o, AgentState& st, unsigned long long nowMs) {
  AgentInfo a;
  if (!SehAgentInfo(o, &a) || !SehObjInfo(o, &a.self)) return;
  if (!a.self.player && !a.hasNpc) return;  // gold mines, regen zones, shops: no AI ranges, nothing reads their lists
  ++g_st.updates;
  ++st.updates;

  // 1. perception (everyone with an agent: the players' GetAutoAttackTarget and CallForHelp read it)
  float centre[3] = {}, radius = 0.f;
  const int n = SehPerceive(sim, a.agent, a.self, centre, &radius);
  if (n < 0) {
    ++g_st.bbExceptions;
    return;
  }
  std::vector<Perceived> seen;
  seen.reserve(static_cast<std::size_t>(n));
  for (int i = 0; i < n && i < kQueryMax; ++i) {
    Perceived p;
    void* t = SehFind(g_queryIds[i]);
    if (!t || !SehObjInfo(t, &p.info)) continue;
    const float dx = p.info.pos[0] - centre[0], dy = p.info.pos[1] - centre[1], dz = p.info.pos[2] - centre[2];
    p.distance = std::sqrt(dx * dx + dy * dy + dz * dz) - a.self.radius - p.info.radius;
    if (p.distance < 0.f) p.distance = 0.f;
    seen.push_back(p);
  }
  std::stable_sort(seen.begin(), seen.end(), [](const Perceived& x, const Perceived& y) { return x.distance < y.distance; });
  if (st.updates == 1 && LogBudget())
    Log("agents: %08X %s first blackboard update: footprint radius %.2f, perception radius %.1f, aggro %.1f, call for "
        "help %.1f, drop %.1f, tether %.1f, aggro type %d, %d perceived", a.self.id, st.noun, a.self.radius, radius,
        a.aggro, a.help, a.drop, a.tether, a.aggroType, static_cast<int>(seen.size()));
  unsigned written = 0;
  SehWritePerception(a.agent, seen, &written);
  g_st.perceived += written;

  // battle ready (player-controlled agents): an enemy shaper near
  int battleReady = -1;
  if (a.self.player) {
    float nearest = -1.f;
    for (const Perceived& p : seen)
      if (p.info.player && p.info.team != a.self.team) {
        nearest = p.distance;
        break;
      }
    const bool next = gms::agents::NextBattleReady(st.battleReady, nearest);
    if (next != st.battleReady) {
      ++g_st.battleReadyChanges;
      st.battleReady = next;
      if (SehFireEvent(o, next ? kEventOnEnterBattleReady : kEventOnExitBattleReady, nullptr, 0)) ++g_st.events;
      if (LogBudget())
        Log("agents: %08X %s battle ready %d (nearest enemy shaper %.1f)", a.self.id, st.noun, next ? 1 : 0, nearest);
    }
    battleReady = st.battleReady ? 1 : 0;
  }

  // 2. the aggro list: only AI agents with an NPC class (not the players' creatures, not ignore or bot)
  const bool aggroList = a.hasBrain && a.hasNpc && !a.self.player && a.aggroType != gms::agents::kAggroIgnore &&
                         a.aggroType != gms::agents::kAggroBot;
  std::uint32_t best = 0;
  // D5: a dead agent (a destroyed Binding or nub stays an object) keeps an empty list: d5_run1's three killed Spirit
  // Bindings went on adding the passing minions and changing their best target
  const bool selfDead = a.self.hasHp && a.self.hp <= 0.f;
  if (aggroList) {
    std::vector<AggroEntry> list;
    if (!SehReadAggro(a.agent, &list)) {
      ++g_st.bbExceptions;
      return;
    }
    // prune
    std::vector<AggroEntry> kept;
    std::vector<gms::agents::AggroCandidate> cands;
    for (const AggroEntry& e : list) {
      void* t = SehFind(e.Id());
      ObjInfo ti;
      int why = 1;
      float dist = 0.f;
      if (!selfDead && t && SehObjInfo(t, &ti)) {
        const float dx = ti.pos[0] - a.self.pos[0], dy = ti.pos[1] - a.self.pos[1], dz = ti.pos[2] - a.self.pos[2];
        dist = std::sqrt(dx * dx + dy * dy + dz * dz) - a.self.radius - ti.radius;
        if (dist < 0.f) dist = 0.f;
        const bool base = e.raw[agent::kAggroEntryFlag] == gms::agents::kEntryFlagBaseStructure;
        why = SehTargetCheck(a, t, ti, dist, false, base);
        if (!why && base && dist > a.drop) ++g_st.baseKeptBeyondRange;
        const bool taunting = e.U64(0x10) > nowMs;
        if (why >= 3 && taunting) why = 0;  // a taunt holds unless the target is gone, dead or no valid target
      }
      if (why) {
        ++g_st.removed;
        ++g_st.removedBy[why - 1];
        static const char* const kWhy[] = {"gone or dead", "invalid", "untargetable", "invisible", "beyond the drop range",
                                           "outside the tether"};
        if (e.Id() == st.chaseLogTarget) st.chaseEndWhy = kWhy[why - 1];
        if (e.Id() == st.fightTarget) st.fightEndWhy = kWhy[why - 1];  // LEASH
        continue;
      }
      gms::agents::AggroCandidate c;
      c.id = e.Id();
      c.taunting = e.U64(0x10) > nowMs;
      c.playerReportAgeMs = e.U64(0x18) ? static_cast<std::int64_t>(nowMs - e.U64(0x18)) : -1;
      c.otherReportAgeMs = e.U64(0x20) ? static_cast<std::int64_t>(nowMs - e.U64(0x20)) : -1;
      c.isPlayer = ti.player;
      c.sticky = c.id == st.bestTarget;
      c.distance = dist;
      c.index = static_cast<int>(kept.size());
      kept.push_back(e);
      cands.push_back(c);
    }
    if (kept.size() != list.size() && !SehWriteAggro(a.agent, kept)) ++g_st.bbExceptions;
    // add: every enemy in the perception list within aggroRange that passes the checks (D14: not for the objective
    // type, the parasite, unless [host] objective_aggro_on_sight=1; gms::agents::AddsOnSight)
    if (a.aggro > 0.f && !selfDead && (g_opt.objectiveOnSight || gms::agents::AddsOnSight(a.aggroType))) {
      for (const Perceived& p : seen) {
        if (p.info.team == a.self.team) continue;
        bool on = false;
        for (const auto& c : cands) on = on || c.id == p.info.id;
        if (on) continue;
        // the add test measures from the agent, like the prune
        const float dx = p.info.pos[0] - a.self.pos[0], dy = p.info.pos[1] - a.self.pos[1],
                    dz = p.info.pos[2] - a.self.pos[2];
        float dist = std::sqrt(dx * dx + dy * dy + dz * dz) - a.self.radius - p.info.radius;
        if (dist < 0.f) dist = 0.f;
        if (SehTargetCheck(a, p.info.obj, p.info, dist, true)) continue;
        if (!SehAdd(o, p.info.id)) {
          ++g_st.addRefused;
          continue;
        }
        ++g_st.added;
        gms::agents::AggroCandidate c;
        c.id = p.info.id;
        c.isPlayer = p.info.player;
        c.distance = dist;
        c.index = static_cast<int>(cands.size());
        cands.push_back(c);
      }
    }
    // MINTARGET: a lane minion's shapers (see gms/core/agents/minion_target.h)
    const bool strict = g_opt.minionTarget && a.aggroType == gms::agents::kAggroMinion;
    std::vector<std::uint32_t> dropped;
    // LEASH: a jungle creature's or a well worker's leash, measured on the NPC itself (gms/core/agents/leash.h)
    if (!selfDead && LeashClassify(o, st, a)) LeashPrune(st, a, &cands, &dropped, nowMs);
    if (strict) {
      gms::agents::MinionTargetOptions mo;
      mo.leashRange = g_opt.minionLeash;
      mo.defendWindowMs = g_opt.minionDefendMs;
      bool otherInRange = false;
      for (const auto& c : cands) otherInRange = otherInRange || (!c.isPlayer && c.distance <= a.aggro);
      // MINJUNGLE: the minion's own distance from its lane, and whether it still walks one (a spline id)
      bool onLane = false;
      float laneDistance = 0.f;
      const bool laneKnown = SehOnLane(o, &onLane) &&
                             ::director::NearestLane(a.self.pos[0], a.self.pos[1], &laneDistance) != 0;
      for (auto& c : cands) {
        if (!c.isPlayer) continue;
        bool defends = false;
        for (const Perceived& p : seen) {  // an own-team shaper in the minion's range that this shaper just hit
          if (!p.info.player || p.info.team != a.self.team || p.distance > a.aggro) continue;
          if (g_shaperHits.Attacked(c.id, p.info.id, static_cast<std::int64_t>(nowMs), mo.defendWindowMs)) {
            defends = true;
            break;
          }
        }
        const bool taunting = c.taunting;
        if (gms::agents::ShaperAllowed(mo, taunting, c.distance, otherInRange, defends)) {
          // MINJUNGLE: the lane leash, the chase time, a dropped shaper ignored on sight for a while
          const int chase = gms::agents::ShaperChaseCheck(g_opt.minionChase, st.chase, c.id, taunting, defends,
                                                          laneKnown && onLane, laneDistance, a.self.pos,
                                                          static_cast<std::int64_t>(nowMs));
          if (chase != gms::agents::kChaseKeep) {
            dropped.push_back(c.id);
            if (chase == gms::agents::kChaseIgnored) {
              ++g_st.chaseIgnored;
            } else {
              ++(chase == gms::agents::kChaseLaneLeash ? g_st.chaseDropLane
                 : chase == gms::agents::kChaseTime     ? g_st.chaseDropTime
                                                        : g_st.chaseDropDistance);
              gms::agents::NoteChaseDrop(g_opt.minionChase, &st.chase, c.id, static_cast<std::int64_t>(nowMs));
            }
            if (c.id == st.chaseLogTarget && chase != gms::agents::kChaseIgnored)
              st.chaseEndWhy = chase == gms::agents::kChaseLaneLeash ? "lane leash"
                               : chase == gms::agents::kChaseTime     ? "chase time"
                                                                      : "chase distance";
            continue;
          }
          c.defend = defends && !taunting;
          if (c.defend) ++g_st.minShaperDefend;
        } else {
          dropped.push_back(c.id);
          if (c.distance > mo.leashRange) ++g_st.minShaperLeash;
          if (c.id == st.chaseLogTarget) st.chaseEndWhy = c.distance > mo.leashRange ? "MINTARGET leash" : "other target in range";
        }
      }
      if (!dropped.empty()) {
        g_st.minShaperDropped += static_cast<unsigned>(dropped.size());
        cands.erase(std::remove_if(cands.begin(), cands.end(),
                                   [&dropped](const gms::agents::AggroCandidate& c) {
                                     return std::find(dropped.begin(), dropped.end(), c.id) != dropped.end();
                                   }),
                    cands.end());
      }
    }
    // TOWERAGGRO: a tower's defended-against shapers, then the strict order (gms/core/agents/tower_target.h)
    const bool towerRule = !selfDead && TowerRuleApplies(o, st, a);
    if (towerRule) TowerDefendUpdate(st, a, &cands, nowMs);
    // order (the targeting priority, gms/core/agents/aggro.h); the entries are re-read (the adds appended theirs)
    std::vector<AggroEntry> now;
    bool readOk = SehReadAggro(a.agent, &now);
    if (readOk && !dropped.empty()) {
      now.erase(std::remove_if(now.begin(), now.end(),
                               [&dropped](const AggroEntry& e) {
                                 return std::find(dropped.begin(), dropped.end(), e.Id()) != dropped.end();
                               }),
                now.end());
      if (!SehWriteAggro(a.agent, now)) ++g_st.bbExceptions;
    }
    if (readOk && now.size() == cands.size() && now.size() > 1) {
      gms::agents::OrderAggro(&cands, a.aggroType, gms::agents::kCallForHelpTimeMs, strict || towerRule);
      std::vector<AggroEntry> ordered;
      ordered.reserve(now.size());
      bool moved = false;
      for (std::size_t i = 0; i < cands.size(); ++i) {
        for (const AggroEntry& e : now)
          if (e.Id() == cands[i].id) {
            ordered.push_back(e);
            break;
          }
        moved = moved || ordered.back().Id() != now[i].Id();
      }
      if (moved && ordered.size() == now.size()) {
        if (SehWriteAggro(a.agent, ordered)) ++g_st.reordered;
        else ++g_st.bbExceptions;
      }
    }
    best = SehBestTarget(a.agent);
    if (towerRule) TowerAfterUpdate(st, cands, best);
    if (a.aggroType == gms::agents::kAggroMinion) {
      MinionChaseAfterUpdate(o, st, a, cands, best, nowMs);
      MinionFollowAfterUpdate(o, st, a, cands, best, nowMs);  // MINPURSUE
    }
    if (st.leashClass > 0) LeashAfterUpdate(o, st, a, best, nowMs);
    // the list after the update, logged when it changes
    std::string sig;
    for (const auto& c : cands) sig += (sig.empty() ? "" : " ") + Hex8(c.id);
    if (sig != st.listSig) {
      if (LogBudget())
        Log("agents: %08X %s aggro list [%s] -> [%s], best %08X (t %.2f s)", a.self.id, st.noun, st.listSig.c_str(),
            sig.c_str(), best, static_cast<double>(nowMs) / 1000.0);
      st.listSig = sig;
    }
  }

  // 3. flags and events
  const bool engaged = best != 0;
  if (engaged) ++g_st.engagedAgents;
  const bool inCombat = engaged || SehSecondsSinceCombat(o) < 5.f;  // [assumed] 5 s after the last hit
  SehWriteFlags(a.agent, engaged, inCombat, battleReady);
  if (aggroList && best != st.bestTarget) {
    ++g_st.bestChanges;
    if (LogBudget())
      Log("agents: %08X %s best target %08X -> %08X: OnBestTargetChanged (bestTargetNew)", a.self.id, st.noun,
          st.bestTarget, best);
    st.bestTarget = best;
    if (SehFireEvent(o, kEventOnBestTargetChanged, "bestTargetNew", best)) ++g_st.events;
  }
}

// When the agent's next blackboard update is due: the init's random 0-500 ms after it was first seen (+0x0C), then
// every 500 ms; at once when +0x0C reads 0 (nAgent.ForceAggroListUpdate).
bool SehTimer(void* o, int* timer) {
  __try {
    void* ag = Field<void*>(o, agent::kOfObject);
    if (!ag) return false;
    *timer = Field<int>(ag, agent::kUpdateTimer);
    return true;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

// No fog of war yet (D2g-live step 2): every agent visible to every team (bits 0 NPC, 1 Mortal, 2 Spirit).
// GameObjectManager_CreateObject sets mVisibleToTeam (+0x888) to 6 (teams 1 and 2), but LineOfSight_AddObject
// (0x00917F40) narrows fog-tracked objects to their own team (GameObject_SetVisibleToOwnTeam 0x00880610) and the pass
// that added the enemy team is stripped, so Agent_IsVisibleTo hid every minion and shaper from enemy viewers with the
// vision rule "everything" (d1b_run2: the minions saw the Binding, a Tower, but never an enemy minion), and from the
// NPC team's (bit 0, never set: the parasite, d1b_run5).
constexpr std::uintptr_t kVisibleToTeam = 0x888;
void SehMarkVisible(void* o) {
  __try {
    unsigned char& v = Field<unsigned char>(o, kVisibleToTeam);
    if ((v & 7) != 7) {
      v = static_cast<unsigned char>(v | 7);
      ++g_st.madeVisible;
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
}

// The agent's ability data container (object +0x8A8) keeps the ids of its ability instances (+0x08 begin, +0x0C end,
// a fixed vector of 32: AbilityDataContainer_AddAbilityInstance 0x00904AB0 asserts "Max abilities per agent exceeded"
// and adds nothing beyond) and of its modifiers (+0x9C/+0xA0); AbilityDataContainer_FireEvent walks both.
// LabsAbilities_RemoveInstance (0x00905D40) frees an ended instance but erases its id from neither list, so the ids of
// ended instances piled up until a minion's 33rd attack asserted (d1b_run4: "Ability List:" empty, every entry stale).
// The host drops the ids whose instance is gone (LabsAbilities_GetInstance null) with each agent's blackboard period.
int PruneIdList(char* container, std::uintptr_t beginOff) {
  std::uint32_t* b = Field<std::uint32_t*>(container, beginOff);
  std::uint32_t* e = Field<std::uint32_t*>(container, beginOff + 4);
  if (!b || e < b || e - b > 256) return 0;
  std::uint32_t* w = b;
  for (std::uint32_t* r = b; r < e; ++r)
    if (*r && GetInstance(*r)) *w++ = *r;
  Field<std::uint32_t*>(container, beginOff + 4) = w;
  return static_cast<int>(e - w);
}

void SehPruneInstanceLists(void* o) {
  __try {
    char* c = Field<char*>(o, obj::kAbilityData);
    if (!c) return;
    g_st.prunedAbilities += static_cast<unsigned>(PruneIdList(c, 0x08));
    g_st.prunedModifiers += static_cast<unsigned>(PruneIdList(c, 0x9C));
  } __except (SehNote(GetExceptionInformation(), "prune instance lists")) {
  }
}

bool UpdateDue(void* o, AgentState& st, unsigned long long nowMs) {
  int timer = 0;
  if (!SehTimer(o, &timer)) return false;
  if (!st.scheduled) {
    st.scheduled = true;
    st.nextUpdateMs = nowMs + static_cast<unsigned long long>(timer > 0 && timer <= 500 ? timer : 0);
  }
  if (timer == 0 && st.updates > 0) {
    ++g_st.forced;
    return true;
  }
  return nowMs >= st.nextUpdateMs;
}

// ---- step 3: the brain step (AGENT_AI.md §6.5) ----------------------------------------------------------------------

std::uint32_t g_rng = 0x9E3779B9u;
double Random01() {  // xorshift32: deterministic per run, enough for gambit picks and random cooldowns
  g_rng ^= g_rng << 13;
  g_rng ^= g_rng >> 17;
  g_rng ^= g_rng << 5;
  return static_cast<double>(g_rng % 1000000u) / 1000000.0;
}

struct GambitDef {
  std::uint32_t guid = 0;
  char* conditions = nullptr;
  int conditionCount = 0;
  bool randomize = false, waitOnCondition = false;
  float weight = 1.f, cooldown = 0.f, maxCooldown = 0.f;
};
struct LinkDef {
  int outputNode = 0;
  char* conditions = nullptr;
  int conditionCount = 0;
  bool startOnCooldown = false, activateOnce = false;
  float activateTime = 0.f, interval = 0.f, cooldown = 0.f, maxCooldown = 0.f;
};
struct NodeDef {
  int phaseType = 0;
  bool hasDefaultBehavior = false;
  std::vector<GambitDef> gambits;
  std::vector<LinkDef> links;
};
struct BrainView {
  void* brain = nullptr;
  int nodeCount = 0;
  int engineNode = 0;  // brain +0x18
  NodeDef node;        // the node the host keeps current
  int rank = 1;
};

// The brain's AI definition and node `wantNode` (-1: the brain's own +0x18). False if the object has no active brain
// with an AI definition.
bool SehReadBrain(void* o, int wantNode, BrainView* v) {
  __try {
    void* b = Field<void*>(o, obj::kBrain);
    if (!b || !Field<unsigned char>(b, brain::kActive)) return false;
    void* defAsset = Field<void*>(b, brain::kAiDefinition);
    char* def = defAsset ? Field<char*>(defAsset, noun::kData) : nullptr;
    if (!def) return false;
    v->brain = b;
    v->nodeCount = Field<int>(def, 4);
    v->engineNode = Field<int>(b, brain::kNode);
    if (v->nodeCount <= 0 || v->nodeCount > 64) return false;
    int n = wantNode >= 0 ? wantNode : v->engineNode;
    if (n < 0 || n >= v->nodeCount) n = 0;
    char* nodeRec = Field<char*>(def, 0) + n * aidef::kNodeSize;
    void* phaseAsset = Field<void*>(nodeRec, 0);
    char* phase = phaseAsset ? Field<char*>(phaseAsset, noun::kData) : nullptr;
    v->node.gambits.clear();
    v->node.links.clear();
    if (phase) {
      v->node.phaseType = Field<int>(phase, 8);
      const char* dflt = Field<const char*>(phase, 0x10);
      v->node.hasDefaultBehavior = dflt && dflt[0];
      const int ng = Field<int>(phase, 4);
      char* g0 = Field<char*>(phase, 0);
      for (int i = 0; i < ng && i < 32; ++i) {
        char* g = g0 + i * aidef::kGambitSize;
        GambitDef d;
        d.conditions = Field<char*>(g, 0);
        d.conditionCount = Field<int>(g, 4);
        d.guid = Field<std::uint32_t>(g, 8);
        d.randomize = Field<unsigned char>(g, 0x1C) != 0;
        d.waitOnCondition = Field<unsigned char>(g, 0x1D) != 0;
        d.weight = Field<float>(g, 0x20);
        d.cooldown = Field<float>(g, 0x24);
        d.maxCooldown = Field<float>(g, 0x28);
        v->node.gambits.push_back(d);
      }
    }
    const int nl = Field<int>(nodeRec, 0x18);
    char* l0 = Field<char*>(nodeRec, 0x14);
    for (int i = 0; i < nl && i < 16; ++i) {
      char* l = l0 + i * aidef::kLinkSize;
      LinkDef d;
      d.outputNode = Field<int>(l, 0);
      d.conditions = Field<char*>(l, 4);
      d.conditionCount = Field<int>(l, 8);
      d.startOnCooldown = Field<unsigned char>(l, 0xC) != 0;
      d.activateOnce = Field<unsigned char>(l, 0xD) != 0;
      d.activateTime = Field<float>(l, 0x10);
      d.interval = Field<float>(l, 0x14);
      d.cooldown = Field<float>(l, 0x1C);
      d.maxCooldown = Field<float>(l, 0x20);
      v->node.links.push_back(d);
    }
    void* npcAsset = Field<void*>(o, npc::kOfObject);
    char* npcData = npcAsset ? Field<char*>(npcAsset, noun::kData) : nullptr;
    v->rank = npcData ? Field<int>(npcData, npc::kRank) : 1;
    if (v->rank < 1) v->rank = 1;
    return true;
  } __except (SehNote(GetExceptionInformation(), "read brain")) {
    return false;
  }
}

// What AIConditions_Evaluate writes back (0x008AE9B0 dispatches by the condition's type, table 0x0123E75C): gambit and
// mission conditions (types 0, 2: AIConditions_CallLuaCondition 0x008AED20) x, y, z, target (+0xC), useTarget (+0x10);
// link conditions (type 1: AIConditions_CallLuaLinkCondition 0x008AEF90) +0xC (the 2nd value: an object, kObjIDNone for
// condition_Bot_IsMissionAvailable) and +0x14 (the 3rd: a number, its mission id). D6a found the +0x14 write: with a
// 0x14-byte struct the bots' first link conditions overran the caller's stack (d6a_run1: the host died with
// 0xC0000409, the stack cookie).
// D6b: the 3rd value is an integer (the call's result slot has GearsLua's type 3, which converts the Lua number to an int
// and asserts "Integer value passed to Lua will lose precision" beyond 2^23; d6b_run1 read it as a float: a denormal).
struct ConditionOut {
  float x, y, z;
  std::uint32_t target;
  unsigned char useTarget;
  unsigned char pad[3];
  std::int32_t linkValue;  // +0x14: a link condition's 3rd value (condition_Bot_IsMissionAvailable: the mission id)
  std::uint32_t spare[2];
};
static_assert(sizeof(ConditionOut) == 0x20, "ConditionOut layout");

// One condition record: AIConditions_Evaluate on its GUID and parameters. `anyTarget` (D6a, bots): a target the
// condition returns counts without useTargetInfo/useTarget (the bots' gambit conditions return it as their 5th value,
// condition_nearest_minion: `return true, 0, 0, 0, v5`, and set neither). `pointOnly` (D6d): set when a passing, not
// negated condition with useTargetInfo gives a position and, with useTarget, kObjIDNone as its target: its target info
// is that point and no object (condition_Bot_Ward: `return true, x, y, z, kObjIDNone, true`).
// NPCTARGET (`ownTarget`, NPCs): a passing, not negated condition's target counts when the condition returned it with
// useTarget (its 6th value) true, without the data's useTargetInfo, which no NPC condition record sets
// (condition_Parasite_EnemyInMelee: `return true, 0, 0, 0, target, true`); the first one given.
bool SehCondition(char* rec, std::uint32_t self, gms::agents::ConditionResult* r, std::uint32_t* target,
                  bool anyTarget, float* pos, std::int32_t* linkValue, bool* pointOnly, bool ownTarget) {
  __try {
    const std::uint32_t guid = Field<std::uint32_t>(rec, 0);
    r->postfixOperator = Field<int>(rec, 0x10);
    r->negate = Field<unsigned char>(rec, 0x14) != 0;
    const bool useTargetInfo = Field<unsigned char>(rec, 0x15) != 0;
    ConditionOut out = {};
    r->pass = guid ? ConditionEvaluate(guid, self, &out, Field<void*>(rec, 0x18), Field<int>(rec, 0x1C)) : true;
    if (out.target && ((useTargetInfo && out.useTarget) || (anyTarget && r->pass && !*target) ||
                       (ownTarget && out.useTarget && r->pass && !r->negate && !*target)))
      *target = out.target;
    // D6b: a passing, not negated link condition's 3rd value (condition_Bot_IsMissionAvailable: the mission id), the
    // first one given
    if (linkValue && r->pass && !r->negate && out.linkValue > 0 && *linkValue <= 0) *linkValue = out.linkValue;
    // D6a: a passing condition's position (the ward's spot, base_position) for a bot's gambit, the first one given
    if (pos && anyTarget && r->pass && (out.x != 0.f || out.y != 0.f) && pos[0] == 0.f && pos[1] == 0.f) {
      pos[0] = out.x;
      pos[1] = out.y;
      pos[2] = out.z;
    }
    if (pointOnly && useTargetInfo && out.useTarget && !out.target && r->pass && !r->negate &&
        (out.x != 0.f || out.y != 0.f))
      *pointOnly = true;
    ++g_st.conditionsRun;
    return true;
  } __except (SehNote(GetExceptionInformation(), "AIConditions_Evaluate")) {
    return false;
  }
}

// A condition list: true if it passes; `target` gets a condition's target when one gives it; `linkValue` (D6b) a
// passing link condition's 3rd value; `pointOnly` (D6d) whether a condition aimed the list at a point and no object.
bool Conditions(char* list, int count, std::uint32_t self, std::uint32_t* target, bool anyTarget = false,
                float* pos = nullptr, std::int32_t* linkValue = nullptr, bool* pointOnly = nullptr,
                bool ownTarget = false) {
  if (!list || count <= 0) return true;
  std::vector<gms::agents::ConditionResult> results;
  for (int i = 0; i < count && i < 16; ++i) {
    gms::agents::ConditionResult r;
    if (!SehCondition(list + i * aidef::kConditionSize, self, &r, target, anyTarget, pos, linkValue, pointOnly,
                      ownTarget)) {
      ++g_st.brainExceptions;
      return false;
    }
    results.push_back(r);
  }
  return gms::agents::CombineConditions(results);
}

int SehRequest(std::uint32_t guid, std::uint32_t self, std::uint32_t target, const float* pos, int rank) {
  __try {
    int instance = 0;
    return RequestAbilityCall(guid, -1, self, target, pos[0], pos[1], pos[2], rank, 0, 0, 0, 1, nullptr, &instance);
  } __except (SehNote(GetExceptionInformation(), "LabsAbilities_RequestAbility")) {
    return -0x7FFF;
  }
}

void SehSetTreeFlag(void* o, unsigned flag, bool on) {
  __try {
    void* btd = Field<void*>(o, obj::kBehaviorTree);
    if (!btd) return;
    unsigned& f = Field<unsigned>(btd, bt::kFlags);
    f = on ? (f | flag) : (f & ~flag);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
}

void SehSetEngineNode(void* brainPtr, int node) {
  __try {
    Field<int>(brainPtr, brain::kNode) = node;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
}

// D6c: whether the agent's current agent ability (container +0x53C, the behavior tree's UseAbility instance, else
// +0x540: AbilityDataContainer_GetAgentAbility 0x008A8800) still holds the agent and may not be interrupted
// (cStaticAbility +0x49A canInterrupt false). A gambit requested then would abort it: LabsAbilities_RequestAbility pushes
// a behavior-tree request unless the new ability may be cast on top, and the tree's UseAbility abort
// (BTAction_UseAbility_Abort 0x008A7FC0) interrupts the instance that holds the agent. The scripts mark the bots'
// movement abilities interruptible (canInterrupt true) and leave every other ability at the constructor's false, and no
// client instruction reads the flag, so the stripped brain update consulted it [inferred]: the brain doesn't replace an
// uninterruptible ability. `name` gets its static ability's name.
bool SehHeldByUninterruptible(void* o, const char** name) {
  __try {
    char* c = Field<char*>(o, obj::kAbilityData);
    if (!c) return false;
    unsigned id = Field<unsigned>(c, kContainerTreeAbility);
    if (!id) id = Field<unsigned>(c, kContainerHeldAbility);
    void* in = id ? GetInstance(id) : nullptr;
    if (!in || Field<int>(in, inst::kType) != 0 || !Field<unsigned char>(in, inst::kHoldsAgent) ||
        Field<unsigned char>(in, inst::kEndPending))
      return false;
    char* st = Field<char*>(in, inst::kStatic);
    if (!st || Field<unsigned char>(st, kStaticCanInterrupt)) return false;
    *name = *reinterpret_cast<const char**>(st);
    return true;
  } __except (SehNote(GetExceptionInformation(), "held ability")) {
    return false;
  }
}

// BOTTEMPO: the ability instance holding the bot's agent now (any kind of hold), its id and static name.
bool SehHeldAbility(void* o, unsigned* id, const char** name) {
  __try {
    char* c = Field<char*>(o, obj::kAbilityData);
    if (!c) return false;
    unsigned i = Field<unsigned>(c, kContainerTreeAbility);
    if (!i) i = Field<unsigned>(c, kContainerHeldAbility);
    void* in = i ? GetInstance(i) : nullptr;
    if (!in || Field<int>(in, inst::kType) != 0 || !Field<unsigned char>(in, inst::kHoldsAgent) ||
        Field<unsigned char>(in, inst::kEndPending))
      return false;
    char* st = Field<char*>(in, inst::kStatic);
    *id = i;
    *name = st ? *reinterpret_cast<const char**>(st) : "?";
    return true;
  } __except (SehNote(GetExceptionInformation(), "held ability (BOTTEMPO)")) {
    return false;
  }
}

bool SehEndAbility(unsigned id) {
  __try {
    if (!GetInstance(id)) return false;
    EndAbility(id);
    return true;
  } __except (SehNote(GetExceptionInformation(), "EndAbility (BOTTEMPO hold watchdog)")) {
    return false;
  }
}

bool GameOver(void* sim) {
  __try {
    return Field<unsigned char>(sim, 0x15) != 0;  // GameSimulator_IsGameOver 0x008EFC30
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

// NPCTARGET: the NPC types whose gambit requests get their own log lines (`npc target:`), the lines' budget
// ([host] npc_target_log_limit, then every 50th), the starts per ability of those NPCs (the stats line), an ability's
// name by GUID
constexpr int kNpcTypeObjective = 8;  // nNPCType: the parasite
constexpr int kNpcTypeGuardian = 13;  // nNPCType: the Guardian
int SehNpcType(void* o);
unsigned g_npcLogLines;
std::map<std::uint32_t, unsigned> g_npcStarts;
bool NpcLogBudget() {
  ++g_npcLogLines;
  return g_npcLogLines <= static_cast<unsigned>(g_opt.npcTargetLogLimit) || g_npcLogLines % 50 == 0;
}
const char* AbilityNameOf(std::uint32_t guid) {
  __try {
    void* st = guid ? FindStaticAbility(guid) : nullptr;
    const char* name = st ? *reinterpret_cast<const char**>(st) : nullptr;
    return name ? name : "?";
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return "?";
  }
}
float PlaneDistance(const float* a, const float* b) {
  const float dx = a[0] - b[0], dy = a[1] - b[1];
  return std::sqrt(dx * dx + dy * dy);
}

void EnterNode(AgentState& st, const BrainView& v, int node, unsigned long long nowMs) {
  // D6c: the node left keeps its timers; the node entered gets back its own from an earlier visit
  if (g_opt.keepNodeTimers && st.node >= 0) {
    AgentState::NodeTimers& t = st.nodeTimers[st.node];
    t.gambitReady = st.gambitReadyMs;
    t.linkReady = st.linkReadyMs;
    t.linkNext = st.linkNextCheckMs;
    t.linkFired = st.linkFired;
  }
  st.node = node;
  st.nodeEnteredMs = nowMs;
  st.seqCursor = -1;
  const auto kept = g_opt.keepNodeTimers ? st.nodeTimers.find(node) : st.nodeTimers.end();
  if (kept != st.nodeTimers.end() && kept->second.gambitReady.size() == v.node.gambits.size() &&
      kept->second.linkReady.size() == v.node.links.size()) {
    st.gambitReadyMs = kept->second.gambitReady;
    st.linkReadyMs = kept->second.linkReady;
    st.linkNextCheckMs = kept->second.linkNext;
    st.linkFired = kept->second.linkFired;
    ++g_st.nodeTimersKept;
  } else {
    st.gambitReadyMs.assign(v.node.gambits.size(), 0);
    st.linkReadyMs.assign(v.node.links.size(), 0);
    st.linkNextCheckMs.assign(v.node.links.size(), 0);
    st.linkFired.assign(v.node.links.size(), 0);
  }
  for (std::size_t i = 0; i < v.node.links.size(); ++i)  // startOnCooldown: at every entry [assumed]
    if (v.node.links[i].startOnCooldown)
      st.linkReadyMs[i] = nowMs + static_cast<unsigned long long>(gms::agents::GambitCooldownSeconds(
                                      v.node.links[i].cooldown, v.node.links[i].maxCooldown, false, 0) * 1000.0);
}

// The brain step of one AI agent (AGENT_AI.md §5 step 5.2): the current node's gambits (conditions, cooldowns, the
// phase type), the chosen gambit's ability requested on the condition's target or the best target (noTick: the tree
// tick below runs it), the pursue flag (8) on "out of range", the phase-default flag (0x40), then the node's links.
void BrainStep(void* sim, void* o, AgentState& st, unsigned long long nowMs) {
  if (st.leaf == kLeafDeath || st.leaf == kLeafDeathAbility || st.leaf == kLeafDeathAbilityDefault ||
      st.leaf == 0x0141E1B8 || st.leaf == 0x0141DE90)  // dying, or an ability holds the agent (leaves 1-3, D1, D3)
    return;
  if (nowMs < st.nextThinkMs) return;
  BrainView v;
  if (!SehReadBrain(o, st.brainSeen ? st.node : -1, &v)) return;  // no active brain with an AI definition
  ObjInfo self;
  if (!SehObjInfo(o, &self) || self.player || (self.hasHp && self.hp <= 0.f)) return;
  if (GameOver(sim)) return;
  if (!st.brainSeen || st.gambitReadyMs.size() != v.node.gambits.size()) {
    st.brainSeen = true;
    EnterNode(st, v, v.engineNode >= 0 && v.engineNode < v.nodeCount ? v.engineNode : 0, nowMs);
  }
  ++g_st.brainSteps;
  AgentInfo a;
  if (!SehAgentInfo(o, &a)) return;
  const std::uint32_t best = SehBestTarget(a.agent);

  // gambits: a cheap pass first (no Lua) to see whether any is off cooldown; D6c: none while an uninterruptible ability
  // holds the agent (the request would abort it)
  bool anyReady = false;
  const char* held = nullptr;
  if (g_opt.respectCanInterrupt && SehHeldByUninterruptible(o, &held)) {
    ++g_st.heldThinks;
  } else {
    for (std::size_t i = 0; i < v.node.gambits.size(); ++i)
      anyReady = anyReady || (v.node.gambits[i].guid && nowMs >= st.gambitReadyMs[i]);
  }
  if (st.npcType == -99) st.npcType = SehNpcType(o);
  const bool watched = st.npcType == kNpcTypeObjective || st.npcType == kNpcTypeGuardian;  // NPCTARGET's log lines
  int chosen = -1;
  int r = 0;
  int fallthroughs = 0;
  if (anyReady) {
    // NPCTARGET (gms/core/agents/brain.h NpcThink): the condition's own target, a prioritized list walked lazily, an
    // out-of-range refusal passed to the next passing gambit
    const std::size_t n = v.node.gambits.size();
    std::vector<gms::agents::GambitView> views(n);
    std::vector<bool> available(n);
    std::vector<std::uint32_t> condTargets(n, 0);
    for (std::size_t i = 0; i < n; ++i) {
      const GambitDef& g = v.node.gambits[i];
      views[i].coolingDown = nowMs < st.gambitReadyMs[i];
      views[i].waitOnCondition = g.waitOnCondition;
      views[i].weight = g.weight;
      available[i] = g.guid != 0;
    }
    gms::agents::NpcThinkOptions to;
    to.conditionTargets = g_opt.npcConditionTarget;
    to.lazy = g_opt.npcLazyConditions;
    to.rangeFallthrough = g_opt.npcRangeFallthrough;
    const gms::agents::NpcThinkResult think = gms::agents::NpcThink(
        v.node.phaseType, views, available, best, to, &st.seqCursor, Random01(),
        [&](int i, std::uint32_t* t) {
          const GambitDef& g = v.node.gambits[static_cast<std::size_t>(i)];
          const bool pass = Conditions(g.conditions, g.conditionCount, self.id, t, false, nullptr, nullptr, nullptr,
                                       to.conditionTargets);
          condTargets[static_cast<std::size_t>(i)] = *t;
          return pass;
        },
        [&](int i, std::uint32_t t) {
          const GambitDef& g = v.node.gambits[static_cast<std::size_t>(i)];
          ObjInfo ti;
          void* tobj = SehFind(t);
          const bool known = tobj && SehObjInfo(tobj, &ti);
          float pos[3] = {self.pos[0], self.pos[1], self.pos[2]};
          if (known) std::memcpy(pos, ti.pos, sizeof pos);
          const int res = SehRequest(g.guid, self.id, t, pos, v.rank);
          ++g_st.requests;
          ++st.requests;
          if (res == 1 || res == 2) ++g_st.started;
          else if (res == 5) ++g_st.outOfRange;
          else ++g_st.requestRefused;
          const std::uint32_t ct = condTargets[static_cast<std::size_t>(i)];
          const bool own = ct != 0 && ct == t;
          ++(own ? g_st.npcOwnTargets : g_st.npcBestTargets);
          // NPCTARGET: the parasite's and the Guardians' requests: every start, other results on change (not the
          // ability's own cooldown, its end within the command window (3) or a cast in progress: the rhythm between
          // two starts)
          if (watched) {
            const bool started = res == 1 || res == 2;
            if (started) ++g_npcStarts[g.guid];
            const bool quiet =
                res == 3 || res == gms::agents::kResultCooldown || res == gms::agents::kResultHoldsAgent;
            if ((started || (!quiet && (res != st.npcLogResult || i != st.npcLogGambit || t != st.npcLogTarget))) &&
                NpcLogBudget())
              Log("npc target: %08X %s node %d gambit %d %s on %08X (%s, %.1f units; %s, best %08X) -> %d%s "
                  "(t %.2f s)",
                  self.id, st.noun, st.node, i, AbilityNameOf(g.guid), t, known ? SehNounName(tobj) : "gone",
                  known ? static_cast<double>(PlaneDistance(ti.pos, self.pos)) : -1.0,
                  own ? "the condition's target" : "the best target", best, res,
                  started ? " started"
                          : res == 5 ? " out of range: pursue"
                                     : gms::agents::NpcRefusalFallsThrough(res) && to.rangeFallthrough
                                           ? " out of range: the next passing gambit may try"
                                           : "",
                  static_cast<double>(nowMs) / 1000.0);
            if (!quiet) {  // a quiet result between two equal refusals doesn't make the second one new
              st.npcLogResult = res;
              st.npcLogGambit = i;
              st.npcLogTarget = t;
            }
          }
          if ((res != st.lastResult || g.guid != st.lastGuid) && LogBudget())
            Log("agents: %08X %s gambit %d (ability %08X) on %08X%s -> %d%s (t %.2f s)", self.id, st.noun, i, g.guid, t,
                own ? " (the condition's target)" : "", res,
                res == 1 || res == 2 ? " started" : res == 5 ? " out of range: pursue" : "",
                static_cast<double>(nowMs) / 1000.0);
          st.lastResult = res;
          st.lastGuid = g.guid;
          return res;
        });
    chosen = think.chosen;
    r = think.result;
    fallthroughs = think.fallthroughs;
    g_st.conditionLists += static_cast<unsigned>(think.evaluated);
    g_st.npcPassedOver += static_cast<unsigned>(think.passedOver);
    g_st.npcFallthroughs += static_cast<unsigned>(think.fallthroughs);
    if (think.fallthroughs > 0 && (r == 1 || r == 2 || r == 5)) ++g_st.npcFallthroughActed;
    if (think.waiting) ++g_st.npcWaiting;
    if (think.noBest) ++g_st.npcNoBest;
    if (think.noTarget) ++g_st.noTarget;  // [assumed] a gambit needs a target: the condition's or the best target
  }
  // nothing to do now, or a gambit out of reach passed on and nothing started: think again in 100 ms (a usable
  // gambit's Lua conditions run at most 10 times a second, and an out-of-range request isn't repeated every frame)
  if (chosen < 0 || gms::agents::NpcThinkAgainLater(r, fallthroughs, g_opt.npcRangeFallthrough))
    st.nextThinkMs = nowMs + 100;
  if (chosen >= 0) {
    const GambitDef& g = v.node.gambits[static_cast<std::size_t>(chosen)];
    if (r == 1 || r == 2)
      st.gambitReadyMs[static_cast<std::size_t>(chosen)] =
          nowMs + static_cast<unsigned long long>(
                      gms::agents::GambitCooldownSeconds(g.cooldown, g.maxCooldown, g.randomize, Random01()) * 1000.0);
    const bool pursue = r == 5;
    if (pursue != st.pursue) {
      st.pursue = pursue;
      SehSetTreeFlag(o, 8, pursue);
      ++(pursue ? g_st.pursueOn : g_st.pursueOff);
    }
  } else if (!best && st.pursue && !held) {
    st.pursue = false;
    SehSetTreeFlag(o, 8, false);
    ++g_st.pursueOff;
  }
  // phase default (0x40): no usable gambit and the phase has a default behavior (D6c: left alone while held)
  if (!held) SehSetTreeFlag(o, 0x40, chosen < 0 && v.node.hasDefaultBehavior);

  // links: the first that fires moves the brain to its output node
  for (std::size_t i = 0; i < v.node.links.size(); ++i) {
    const LinkDef& l = v.node.links[i];
    if (nowMs < st.linkNextCheckMs[i]) continue;
    if (l.interval > 0) st.linkNextCheckMs[i] = nowMs + static_cast<unsigned long long>(l.interval * 1000.0f);
    gms::agents::LinkView lv;
    lv.activateOnce = l.activateOnce;
    lv.firedBefore = st.linkFired[i] != 0;
    lv.activateTime = l.activateTime;
    lv.secondsInNode = static_cast<double>(nowMs - st.nodeEnteredMs) / 1000.0;
    lv.cooldownLeft = nowMs < st.linkReadyMs[i] ? static_cast<double>(st.linkReadyMs[i] - nowMs) / 1000.0 : 0.0;
    if (lv.cooldownLeft > 0 || lv.secondsInNode < lv.activateTime || (lv.activateOnce && lv.firedBefore)) continue;
    std::uint32_t unused = 0;
    lv.conditionsPass = Conditions(l.conditions, l.conditionCount, self.id, &unused);
    if (!gms::agents::LinkFires(lv)) continue;
    if (l.outputNode < 0 || l.outputNode >= v.nodeCount) continue;
    st.linkFired[i] = 1;
    ++g_st.linksFired;
    if (LogBudget())
      Log("agents: %08X %s link %zu: node %d -> %d (t %.2f s)", self.id, st.noun, i, st.node, l.outputNode,
          static_cast<double>(nowMs) / 1000.0);
    BrainView next;
    if (SehReadBrain(o, l.outputNode, &next)) {
      EnterNode(st, next, l.outputNode, nowMs);
      SehSetEngineNode(v.brain, l.outputNode);
    }
    break;
  }
}

// ---- D6a: the bot brain step (AGENT_AI.md §11, *D6a*) -------------------------------------------------------------

struct BotLink {
  int outputNode = 0;
  char* conditions = nullptr;
  int conditionCount = 0;
  bool startOnCooldown = false, activateOnce = false;
  float activateTime = 0.f, interval = 0.f, cooldown = 0.f, maxCooldown = 0.f;
};
struct BotGambit {
  std::uint32_t guid = 0;
  int slot = -1;
  char* conditions = nullptr;
  int conditionCount = 0;
  bool randomize = false, waitOnCondition = false;
  float weight = 1.f, cooldown = 0.f, maxCooldown = 0.f;
};
struct BotView {
  void* brain = nullptr;
  int nodeCount = 0;
  int enginePlan = -1, enginePhase = -1;  // brain +0x14 / +0x18
  // the plan node asked for
  bool planOk = false;
  const char* planName = "?";
  std::vector<BotLink> nodeLinks;   // cAINode.output: to other plans
  std::vector<BotLink> phaseLinks;  // Plan.output: to its phases
  int missionType = 0;              // Plan +0x14
  bool requestMission = false;      // Plan +0x18 (D6b: the EngageForKill plans)
  // the phase node asked for
  bool phaseOk = false;
  const char* phaseName = "?";
  int phaseType = 0;
  const char* defaultBehavior = nullptr;
  std::vector<BotGambit> gambits;
};

const char* AssetNameOr(void* asset, const char* dflt) {
  const char* n = asset ? Field<const char*>(asset, 0) : nullptr;
  if (!n) return dflt;
  volatile char probe = n[0];
  (void)probe;
  return n;
}

void ReadLinks(char* l0, int nl, std::vector<BotLink>* out) {
  out->clear();
  for (int i = 0; i < nl && i < 32; ++i) {
    char* l = l0 + i * aidef::kLinkSize;
    BotLink d;
    d.outputNode = Field<int>(l, 0);
    d.conditions = Field<char*>(l, 4);
    d.conditionCount = Field<int>(l, 8);
    d.startOnCooldown = Field<unsigned char>(l, 0xC) != 0;
    d.activateOnce = Field<unsigned char>(l, 0xD) != 0;
    d.activateTime = Field<float>(l, 0x10);
    d.interval = Field<float>(l, 0x14);
    d.cooldown = Field<float>(l, 0x1C);
    d.maxCooldown = Field<float>(l, 0x20);
    out->push_back(d);
  }
}

// Whether node `n` of the bot's AIDefinition is a plan (cAINode +4 mpPlanData) or a phase (+0 mpPhaseData).
bool SehNodeKind(void* o, int n, bool* isPlan, bool* isPhase) {
  __try {
    *isPlan = *isPhase = false;
    void* b = Field<void*>(o, obj::kBrain);
    void* defAsset = b ? Field<void*>(b, brain::kAiDefinition) : nullptr;
    char* def = defAsset ? Field<char*>(defAsset, noun::kData) : nullptr;
    if (!def || n < 0 || n >= Field<int>(def, 4)) return false;
    char* rec = Field<char*>(def, 0) + n * aidef::kNodeSize;
    *isPhase = Field<void*>(rec, 0) != nullptr;
    *isPlan = Field<void*>(rec, 4) != nullptr;
    return true;
  } __except (SehNote(GetExceptionInformation(), "bot node kind")) {
    return false;
  }
}

// The bot brain's plan node `plan` and phase node `phase` (-1: none). False without an active brain with a definition.
bool SehReadBotView(void* o, int plan, int phase, BotView* v) {
  __try {
    void* b = Field<void*>(o, obj::kBrain);
    if (!b || !Field<unsigned char>(b, brain::kActive)) return false;
    void* defAsset = Field<void*>(b, brain::kAiDefinition);
    char* def = defAsset ? Field<char*>(defAsset, noun::kData) : nullptr;
    if (!def) return false;
    v->brain = b;
    v->nodeCount = Field<int>(def, 4);
    v->enginePlan = Field<int>(b, brain::kPlan);
    v->enginePhase = Field<int>(b, brain::kNode);
    if (v->nodeCount <= 0 || v->nodeCount > 64) return false;
    char* nodes = Field<char*>(def, 0);
    v->planOk = false;
    v->nodeLinks.clear();
    v->phaseLinks.clear();
    v->missionType = 0;
    v->requestMission = false;
    if (plan >= 0 && plan < v->nodeCount) {
      char* rec = nodes + plan * aidef::kNodeSize;
      void* planAsset = Field<void*>(rec, 4);
      char* pd = planAsset ? Field<char*>(planAsset, noun::kData) : nullptr;
      if (pd) {
        v->planOk = true;
        v->planName = AssetNameOr(planAsset, "?");
        ReadLinks(Field<char*>(rec, 0x14), Field<int>(rec, 0x18), &v->nodeLinks);
        ReadLinks(Field<char*>(pd, 0), Field<int>(pd, 4), &v->phaseLinks);
        v->missionType = Field<int>(pd, 0x14);
        v->requestMission = Field<unsigned char>(pd, 0x18) != 0;
      }
    }
    v->phaseOk = false;
    v->gambits.clear();
    v->defaultBehavior = nullptr;
    if (phase >= 0 && phase < v->nodeCount) {
      char* rec = nodes + phase * aidef::kNodeSize;
      void* phaseAsset = Field<void*>(rec, 0);
      char* ph = phaseAsset ? Field<char*>(phaseAsset, noun::kData) : nullptr;
      if (ph) {
        v->phaseOk = true;
        v->phaseName = AssetNameOr(phaseAsset, "?");
        v->phaseType = Field<int>(ph, 8);
        const char* dflt = Field<const char*>(ph, 0x10);
        v->defaultBehavior = dflt && dflt[0] ? dflt : nullptr;
        const int ng = Field<int>(ph, 4);
        char* g0 = Field<char*>(ph, 0);
        for (int i = 0; i < ng && i < 32; ++i) {
          char* g = g0 + i * aidef::kGambitSize;
          BotGambit d;
          d.conditions = Field<char*>(g, 0);
          d.conditionCount = Field<int>(g, 4);
          d.guid = Field<std::uint32_t>(g, 8);
          d.slot = Field<int>(g, 0x18);
          d.randomize = Field<unsigned char>(g, 0x1C) != 0;
          d.waitOnCondition = Field<unsigned char>(g, 0x1D) != 0;
          d.weight = Field<float>(g, 0x20);
          d.cooldown = Field<float>(g, 0x24);
          d.maxCooldown = Field<float>(g, 0x28);
          v->gambits.push_back(d);
        }
      }
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "read bot brain")) {
    return false;
  }
}

void SehSetBrainNodes(void* brainPtr, int plan, int phase) {
  __try {
    Field<int>(brainPtr, brain::kPlan) = plan;
    Field<int>(brainPtr, brain::kNode) = phase;
  } __except (SehNote(GetExceptionInformation(), "set bot brain nodes")) {
  }
}

bool SehResetDefaultBehavior(void* o) {
  __try {
    void* btd = Field<void*>(o, obj::kBehaviorTree);
    if (!btd) return false;
    PushTreeRequest(btd, kRequestResetDefaultBehavior, 0, kLifetimeForever, 0, 0);
    return true;
  } __except (SehNote(GetExceptionInformation(), "AgentBehaviorTree_PushRequest 0x400")) {
    return false;
  }
}

// The bot's player (object +0x4C on the current simulator) and what its gambits need of it.
struct BotPlayerView {
  void* player = nullptr;
  int ranks[7] = {};          // slots 0-6 (0-2: 1)
  void* items[11] = {};       // the inventory's assets
};

bool SehBotPlayer(void* sim, void* o, BotPlayerView* p) {
  __try {
    const unsigned index = Field<unsigned char>(o, gobj::kPlayerIndex);
    p->player = GetPlayerByIndex(sim, index);
    if (!p->player) return false;
    for (int s = 0; s < 7; ++s) p->ranks[s] = s >= 3 ? GetAbilityRank(p->player, s) : 1;
    for (unsigned i = 0; i < player::kItemSlots; ++i)
      p->items[i] = Field<void*>(p->player, player::kItems + i * player::kItemSize);
    return true;
  } __except (SehNote(GetExceptionInformation(), "bot player")) {
    return false;
  }
}

// Whether a gambit can be tried at all (before its conditions): its ability is learned or its item is carried.
bool GambitAvailable(const BotGambit& g, const BotPlayerView& p) {
  if (gms::agents::SlotIsRanked(g.slot)) return p.ranks[g.slot] > 0;
  if (gms::agents::SlotIsItem(g.slot)) {
    const int inv = gms::agents::SlotInventoryIndex(g.slot);
    return inv >= 0 && inv < static_cast<int>(player::kItemSlots) && p.items[inv] != nullptr;
  }
  if (g.slot == gms::agents::kSlotNone) return g.guid != 0;
  return g.slot == gms::agents::kSlotBasic || g.slot == gms::agents::kSlotWard;
}

bool SehHostile(int team, std::uint32_t target) {
  __try {
    void* t = target ? GetObjectById(target) : nullptr;
    return t && ValidateHostile(team, t, 0);
  } __except (SehNote(GetExceptionInformation(), "ValidateHostileTarget")) {
    return false;
  }
}

// BOTIDLE: the object's nNPCType (-1: none, a player or a projectile)
int SehNpcType(void* o) {
  __try {
    void* npcAsset = Field<void*>(o, npc::kOfObject);
    char* d = npcAsset ? Field<char*>(npcAsset, noun::kData) : nullptr;
    return d ? Field<int>(d, npc::kType) : -1;
  } __except (SehNote(GetExceptionInformation(), "npc type")) {
    return -1;
  }
}

// BOTIDLE: the idle fixes' own log budget (the agent log is busy in long matches): 400 lines, then every 20th.
unsigned g_idleLogLines;
bool IdleLogBudget() {
  ++g_idleLogLines;
  return g_idleLogLines <= 400 || g_idleLogLines % 20 == 0;
}

const char* RefusalName(int r) {
  switch (r) {
    case gms::agents::kResultCooldown: return "on cooldown";
    case -0x270D: return "not enough resource";
    case -0x2704: return "the script's CanUse/CanActivate says no";
    case -0x2708: return "invalid target";
    case -0x2709: return "caster disabled";
    case -0x2706: return "blocked by an attribute";
    case -0x270C: return "out of range, no pursuit";
    case gms::agents::kResultItemRefused: return "item refused";
    default: return "refused";
  }
}

void SehSetTarget(void* o, std::uint32_t target) {
  __try {
    SetAgentTarget(o, target);
  } __except (SehNote(GetExceptionInformation(), "AgentBlackboard_SetTarget")) {
  }
}

// D6d: the agent's raw mTarget (+0x1E5C; GetBestTarget's bot case also asks BotDirector_IsValidTargetById) and the
// blackboard's target position (+0x1E64, what the Pursue leaf and nBehavior_Follow read).
std::uint32_t SehRawTarget(void* o) {
  __try {
    char* bb = Field<char*>(o, agent::kOfObject);
    return bb ? Field<std::uint32_t>(bb, agent::kTarget) : 0u;
  } __except (SehNote(GetExceptionInformation(), "read mTarget")) {
    return 0;
  }
}

void SehSetTargetPosition(void* o, const float* p) {
  __try {
    char* bb = Field<char*>(o, agent::kOfObject);
    if (!bb) return;
    for (int i = 0; i < 3; ++i) Field<float>(bb, agent::kTargetPosition + 4 * i) = p[i];
  } __except (SehNote(GetExceptionInformation(), "write the target position")) {
  }
}

int SehBotRequest(std::uint32_t guid, int slot, std::uint32_t self, std::uint32_t target, const float* pos, int rank) {
  __try {
    int instance = 0;
    return RequestAbilityCall(guid, slot, self, target, pos[0], pos[1], pos[2], rank, 0, 0, 0, 1, nullptr, &instance);
  } __except (SehNote(GetExceptionInformation(), "LabsAbilities_RequestAbility (bot)")) {
    return -0x7FFF;
  }
}

// A consumable (potion): LabsPlayer_CanUseItem, then LabsPlayer_UseItem (it requests the item's ability and takes one
// of the stack). 1 = used, 0 = refused, -1 = exception.
int SehBotUseItem(void* player, void* item, unsigned invSlot) {
  __try {
    if (!CanUseItem(player, item, invSlot)) return 0;
    UseItem(player, item, invSlot);
    return 1;
  } __except (SehNote(GetExceptionInformation(), "LabsPlayer_UseItem (bot)")) {
    return -1;
  }
}

struct BotStats {
  unsigned thinks = 0, planChanges = 0, phaseChanges = 0, behaviorResets = 0, requests = 0, started = 0;
  unsigned outOfRange = 0, refused = 0, items = 0, noPhase = 0, conditions = 0, exceptions = 0;
  // D6c: timers kept across visits (plan and phase entries that got their node's timers back); plans left again
  // within kQuickExitMs of entering them, the back-and-forth among them (into the plan left within kQuickExitMs), and
  // for quick exits from a requestMission plan (EngageForKill) the bot's distance to its mTarget then
  unsigned planTimersKept = 0, phaseTimersKept = 0, quickExits = 0, bounces = 0;
  unsigned heldThinks = 0;  // thinks without gambits: an uninterruptible ability held the agent
  unsigned quickExitNear = 0, quickExitMid = 0, quickExitFar = 0, quickExitNoTarget = 0;  // < 30, 30-55, > 55, none
  // D6d: requests aimed at a point only, pursuits of a point (mTarget cleared for them), how they ended: the gambit
  // started at the point, or the bot chose something else (another gambit, phase or plan)
  unsigned pointRequests = 0, pointStarted = 0, pointPursuits = 0, pointTargetsCleared = 0, pointReached = 0;
  unsigned pointDropped = 0;
  unsigned pointLeafRestarts = 0;  // a running Pursue leaf restarted (request 0x400) to take the point
  // BOTIDLE: refusals that let the next gambit try (and the ones that then started or pursued), pursuits dropped as stuck,
  // the idle fallback (tries, requests that started or pursued)
  unsigned backSteps = 0;  // BOTBACK: Back gambits that stepped toward the base instead of walking home
  unsigned recalls = 0;  // BOTRECALL: Back gambits that cast Return to Base instead of walking
  unsigned skipped = 0, skippedThenActed = 0, pursuitStuck = 0, fallbackTries = 0, fallbackActed = 0;
  unsigned holdStuck = 0, staleTargets = 0;  // BOTTEMPO: holds ended by the watchdog, stale mTargets dropped
};
BotStats g_bot;
constexpr unsigned long long kQuickExitMs = 1000;

void BotEnterPlan(AgentState& st, const BotView& v, int plan, unsigned long long nowMs) {
  // D6c: the plan left keeps its link timers (its cAINode.output and its Plan.output); the plan entered gets back its own
  if (g_opt.keepNodeTimers && st.botPlan >= 0) {
    AgentState::NodeTimers& t = st.nodeTimers[st.botPlan];
    t.linkReady = st.nodeLinkReady;
    t.linkNext = st.nodeLinkNext;
    t.linkFired = st.nodeLinkFired;
    t.phaseLinkReady = st.phaseLinkReady;
    t.phaseLinkNext = st.phaseLinkNext;
    t.phaseLinkFired = st.phaseLinkFired;
  }
  st.botPlan = plan;
  st.planEnteredMs = nowMs;
  const auto kept = g_opt.keepNodeTimers ? st.nodeTimers.find(plan) : st.nodeTimers.end();
  if (kept != st.nodeTimers.end() && kept->second.linkReady.size() == v.nodeLinks.size() &&
      kept->second.phaseLinkReady.size() == v.phaseLinks.size()) {
    st.nodeLinkReady = kept->second.linkReady;
    st.nodeLinkNext = kept->second.linkNext;
    st.nodeLinkFired = kept->second.linkFired;
    st.phaseLinkReady = kept->second.phaseLinkReady;
    st.phaseLinkNext = kept->second.phaseLinkNext;
    st.phaseLinkFired = kept->second.phaseLinkFired;
    ++g_bot.planTimersKept;
  } else {
    st.nodeLinkReady.assign(v.nodeLinks.size(), 0);
    st.nodeLinkNext.assign(v.nodeLinks.size(), 0);
    st.nodeLinkFired.assign(v.nodeLinks.size(), 0);
    st.phaseLinkReady.assign(v.phaseLinks.size(), 0);
    st.phaseLinkNext.assign(v.phaseLinks.size(), 0);
    st.phaseLinkFired.assign(v.phaseLinks.size(), 0);
  }
  st.phaseEnteredMs = nowMs;
  for (std::size_t i = 0; i < v.nodeLinks.size(); ++i)  // startOnCooldown: at every entry [assumed]
    if (v.nodeLinks[i].startOnCooldown)
      st.nodeLinkReady[i] = nowMs + static_cast<unsigned long long>(gms::agents::LinkCooldownSeconds(
                                        v.nodeLinks[i].cooldown, v.nodeLinks[i].maxCooldown, Random01()) * 1000.0);
  for (std::size_t i = 0; i < v.phaseLinks.size(); ++i)
    if (v.phaseLinks[i].startOnCooldown)
      st.phaseLinkReady[i] = nowMs + static_cast<unsigned long long>(gms::agents::LinkCooldownSeconds(
                                         v.phaseLinks[i].cooldown, v.phaseLinks[i].maxCooldown, Random01()) * 1000.0);
}

// D6c: a phase node entered: the phase left keeps its gambit cooldowns, the phase entered gets back its own.
void BotEnterPhase(AgentState& st, int phase, std::size_t gambits) {
  if (g_opt.keepNodeTimers && st.botPhase >= 0) st.nodeTimers[st.botPhase].gambitReady = st.gambitReadyMs;
  st.botPhase = phase;
  const auto kept = g_opt.keepNodeTimers ? st.nodeTimers.find(phase) : st.nodeTimers.end();
  if (kept != st.nodeTimers.end() && kept->second.gambitReady.size() == gambits) {
    st.gambitReadyMs = kept->second.gambitReady;
    ++g_bot.phaseTimersKept;
  } else {
    st.gambitReadyMs.assign(gambits, 0);
  }
}

// D6c: a bot's distance to its mTarget (-1: none or gone).
float SehDistanceToTarget(void* o, const ObjInfo& self) {
  AgentInfo ai;
  const std::uint32_t t = SehAgentInfo(o, &ai) ? SehBestTarget(ai.agent) : 0;
  void* tobj = t ? SehFind(t) : nullptr;
  ObjInfo ti;
  if (!tobj || !SehObjInfo(tobj, &ti)) return -1.f;
  const float dx = ti.pos[0] - self.pos[0], dy = ti.pos[1] - self.pos[1];
  return std::sqrt(dx * dx + dy * dy);
}

// The link set's view for `i` (time in the node, cooldown, once).
gms::agents::LinkView BotLinkView(const BotLink& l, double secondsInNode, unsigned long long readyMs, bool fired,
                                  unsigned long long nowMs) {
  gms::agents::LinkView lv;
  lv.activateOnce = l.activateOnce;
  lv.firedBefore = fired;
  lv.activateTime = l.activateTime;
  lv.secondsInNode = secondsInNode;
  lv.cooldownLeft = nowMs < readyMs ? static_cast<double>(readyMs - nowMs) / 1000.0 : 0.0;
  return lv;
}

// D6d: the point pursuit's own log budget (the agent log is busy in long matches): 600 lines, then every 20th.
unsigned g_pointLogLines;
bool PointLogBudget() {
  ++g_pointLogLines;
  return g_pointLogLines <= 600 || g_pointLogLines % 20 == 0;
}

float Distance2D(const float* a, const float* b) {
  const float dx = a[0] - b[0], dy = a[1] - b[1];
  return std::sqrt(dx * dx + dy * dy);
}

// D6d: a bot's gambit at a point was out of range (5). The Pursue leaf (12, BTNode_Pursue_Evaluate 0x008A8CA0) hands
// nBehavior_Follow the best target and the blackboard's target position; Follow walks to the position only when the
// target is kObjIDNone, and a bot's best target is its mTarget (AgentBlackboard_GetBestTarget's aggro type 5). So the
// point goes into +0x1E64 and mTarget is cleared [inferred: nothing in the client writes +0x1E64, the stripped brain
// did]. A Pursue leaf that is running still follows the object it started with (Follow keeps the request block's
// target), so a new point restarts it through request 0x400 (leaf 9 preempts it and redecides).
void BeginPointPursuit(void* o, AgentState& st, const ObjInfo& self, const BotView& v, int gambit, const float* p,
                       unsigned long long nowMs) {
  SehSetTargetPosition(o, p);
  const std::uint32_t old = SehRawTarget(o);
  if (old) {
    SehSetTarget(o, 0);
    ++g_bot.pointTargetsCleared;
  }
  const bool newPoint = !st.pointPursuit || std::fabs(p[0] - st.point[0]) > 0.01f || std::fabs(p[1] - st.point[1]) > 0.01f;
  if (!newPoint) return;
  bool restarted = false;
  if (st.leaf == kLeafPursue && SehResetDefaultBehavior(o)) {
    ++g_bot.pointLeafRestarts;
    restarted = true;
  }
  if (!st.pointPursuit) ++g_bot.pointPursuits;
  st.pointPursuit = true;
  std::memcpy(st.point, p, sizeof st.point);
  st.pointSinceMs = nowMs;
  st.pointStartDistance = Distance2D(p, self.pos);
  if (PointLogBudget()) {
    char extra[64] = "";
    if (old) std::snprintf(extra, sizeof extra, ", mTarget %08X cleared", old);
    Log("agents: bot %08X %s phase %d (%s) gambit %d at the point (%.1f, %.1f), %.1f units away: out of range, pursue "
        "the point%s%s (t %.2f s)",
        self.id, st.noun, st.botPhase, v.phaseName, gambit, static_cast<double>(p[0]), static_cast<double>(p[1]),
        static_cast<double>(st.pointStartDistance), extra, restarted ? ", Pursue restarted" : "",
        static_cast<double>(nowMs) / 1000.0);
  }
}

// D6d: the pursuit of a point ends: the gambit started (`startedAt`: its point, the pursued one or another the
// condition chose meanwhile) or the bot chose something else. The target position goes back to the zero vector (the
// constructor's), so the Pursue leaf has nothing left to walk to.
void EndPointPursuit(void* o, AgentState& st, const ObjInfo& self, int r, const float* startedAt,
                     unsigned long long nowMs) {
  static const float kZero[3] = {};
  SehSetTargetPosition(o, kZero);
  st.pointPursuit = false;
  ++(startedAt ? g_bot.pointReached : g_bot.pointDropped);
  if (!PointLogBudget()) return;
  char how[96];
  if (startedAt)
    std::snprintf(how, sizeof how, "the gambit started at (%.1f, %.1f), %.1f units away",
                  static_cast<double>(startedAt[0]), static_cast<double>(startedAt[1]),
                  static_cast<double>(Distance2D(startedAt, self.pos)));
  else
    std::snprintf(how, sizeof how, "the bot chose otherwise, result %d", r);
  Log("agents: bot %08X %s pursuit of the point (%.1f, %.1f) ended after %.1f s, %.1f units from it (%.1f at the "
      "start): %s (t %.2f s)",
      self.id, st.noun, static_cast<double>(st.point[0]), static_cast<double>(st.point[1]),
      static_cast<double>(nowMs - st.pointSinceMs) / 1000.0, static_cast<double>(Distance2D(st.point, self.pos)),
      static_cast<double>(st.pointStartDistance), how, static_cast<double>(nowMs) / 1000.0);
}

// BOTIDLE: the pursuit watchdog's progress (units) and how long a stuck gambit is left out; the idle fallback's hit window
// (ms) and reach (units)
constexpr float kStuckProgress = 3.f;
constexpr unsigned long long kStuckHoldMs = 4000;
constexpr long long kFallbackHitWindowMs = 3000;
constexpr float kFallbackReach = 30.f;

// ---- BOTREFLEX (gms/core/agents/reflex.h) ------------------------------------------------------------------------------
// A layer above the plan's gambits: a bot hit by a tower backs off, a Hunter or Predator fights back, engages shapers in
// the jungle and defends its wells. The snapshots below are taken once per frame (Tick) from the objects the agent loop
// already collected.
constexpr std::uint32_t kAbilityBotBack = 0xE695B80B;          // ability_Bot_Back (GUID in its script header)
constexpr std::uint32_t kAbilityBotMoveToTarget = 0x2378FAE9;  // ability_Bot_MoveToTarget
constexpr int kNpcObjective = 8;                               // nNPCType: the parasite
constexpr int kNpcTower = 9;                                   // nNPCType.Tower
constexpr float kNearShapers = 35.f;                           // shapers counted for "outnumbered"
constexpr float kAttackerReach = 45.f;                         // an attacker farther than this no longer counts
constexpr float kWellSideReach = 220.f;                        // wells farther than this are not "on its side"
// A retreat or a walk to a well is made in steps: ability_Bot_MoveToTarget holds the agent until it arrives and the
// brain does not think meanwhile (BotBrainStep returns on the AbilityHoldsAgent leaf), so a long walk would blind the
// bot to the next attack. ability_Bot_Back walks to the base: not used.
constexpr float kRetreatStep = 30.f;
constexpr float kWellStep = 25.f;

struct Snap {
  void* obj = nullptr;
  std::uint32_t id = 0;
  int team = 0;
  float pos[3] = {};
  float hp = 0.f, maxHp = 1.f;
  float HpFrac() const { return maxHp > 0.f ? hp / maxHp : 1.f; }
};
std::vector<Snap> g_snapPlayers, g_snapTowers, g_snapObjectives;  // objectives: NPC type 8 (the parasite)

float SehMaxHp(void* o) {
  __try {
    return MaxHitPoints(o);
  } __except (SehNote(GetExceptionInformation(), "max hit points")) {
    return 0.f;
  }
}

bool SehVisible(void* viewer, void* target) {
  __try {
    return IsVisibleTo(viewer, target);
  } __except (SehNote(GetExceptionInformation(), "Agent_IsVisibleTo")) {
    return false;
  }
}

const Snap* FindSnap(const std::vector<Snap>& v, std::uint32_t id) {
  for (const Snap& s : v)
    if (s.id == id) return &s;
  return nullptr;
}

float Dist2(const float* a, const float* b) {
  return std::sqrt((a[0] - b[0]) * (a[0] - b[0]) + (a[1] - b[1]) * (a[1] - b[1]));
}

int BotRoleOf(std::uint32_t creature) {
  for (const bots::Bot& b : bots::Players())
    if (b.creature == creature) return b.role;
  return -1;
}

// The bot attacks `tgt`: its Q, W, E (as far as ranked), else its basic attack; 1 started, 5 out of range (the Pursue
// leaf follows mTarget), 0 nothing worked.
int ReflexAttack(void* o, const ObjInfo& self, const BotPlayerView& pv, std::uint32_t mtarget, const Snap& tgt) {
  if (tgt.id != mtarget && SehHostile(self.team, tgt.id)) SehSetTarget(o, tgt.id);
  static const int kSlots[] = {gms::agents::kSlotQ, gms::agents::kSlotQ + 1, gms::agents::kSlotQ + 2,
                               gms::agents::kSlotBasic};
  for (int slot : kSlots) {
    const bool ranked = slot >= gms::agents::kSlotQ;
    if (ranked && pv.ranks[slot] <= 0) continue;
    const int res = SehBotRequest(0, slot, self.id, tgt.id, tgt.pos, ranked ? pv.ranks[slot] : 1);
    if (res == 1 || res == 2) return 1;
    if (res == 5) return 5;
    if (res == gms::agents::kResultHoldsAgent) return 1;
  }
  return 0;
}

// The point `step` units from `from` away from (or toward) `other` in 2-D; a degenerate pair gives the direction toward
// the bot's own base corner (team 1 at the map's low corner, team 2 at the high one).
void StepPoint(const float* from, const float* other, float step, bool toward, int team, float* out) {
  float dx = other[0] - from[0], dy = other[1] - from[1];
  float len = std::sqrt(dx * dx + dy * dy);
  if (len < 0.5f) {
    dx = team == 1 ? -1.f : 1.f;
    dy = dx;
    len = std::sqrt(2.f);
    toward = true;
  }
  const float k = (toward ? 1.f : -1.f) * (len > 0.f ? step / len : 0.f);
  out[0] = from[0] + dx * k;
  out[1] = from[1] + dy * k;
  out[2] = from[2];
}

// BOTRECALL / BOTBACK (gms/core/agents/recall.h): a Flee phase's walk home becomes the shop's Return to Base once the
// bot is out of immediate danger; a lane phase's Back gambit is a short step toward the start point.
constexpr std::uint32_t kAbilityReturnToBase = 0x96B845C6;  // SPID("ability_returntobase") (GameplayControls_ReturnToBase)
const gms::agents::RecallConfig kRecall;

// BOTBACK: the start point of the bot's player (where ability_Bot_Back walks to), false when there is none.
bool BotStartPoint(const ObjInfo& self, float* out) {
  if (!g_sim) return false;
  for (const bots::Bot& b : bots::Players()) {
    if (b.creature != self.id) continue;
    float pos[3] = {1e30f, 0.f, 0.f}, rot[4] = {};
    GetPlayerStartPoint(g_sim, static_cast<unsigned>(b.team), static_cast<unsigned>(b.index), pos, rot);
    if (pos[0] >= 1e29f) return false;
    out[0] = pos[0];
    out[1] = pos[1];
    out[2] = pos[2];
    return true;
  }
  return false;
}

bool InFleePhase(const char* phaseName) {
  return phaseName && std::strstr(phaseName, "Flee") && !std::strstr(phaseName, "TowerTargeting");
}

// The recall rule's inputs from the per-frame snapshots (BOTREFLEX's: players, towers, the parasite) and OnDamaged.
bool RecallPossible(const AgentState& st, const ObjInfo& self, const char* phaseName, unsigned long long nowMs) {
  if (!g_opt.botRecall || !g_opt.botReflex || !g_sim) return false;
  gms::agents::RecallInputs in;
  in.fleePhase = InFleePhase(phaseName);
  if (!in.fleePhase) return false;
  // the latest hit by anything (OnDamaged: parasite, creeps, minions; shapers and towers also for BOTREFLEX)
  for (unsigned long long at : {st.lastHitMs, st.lastShaperHitMs, st.lastTowerHitMs}) {
    if (!at || at > nowMs) continue;
    const long long since = static_cast<long long>(nowMs - at);
    if (in.sinceHitMs < 0 || since < in.sinceHitMs) in.sinceHitMs = since;
  }
  for (const Snap& p : g_snapPlayers)
    if (p.team != self.team) in.nearestEnemyShaper = std::min(in.nearestEnemyShaper, Dist2(p.pos, self.pos));
  for (const Snap& t : g_snapTowers)
    if (t.team != self.team) in.nearestEnemyTower = std::min(in.nearestEnemyTower, Dist2(t.pos, self.pos));
  for (const Snap& ob : g_snapObjectives) in.nearestObjective = std::min(in.nearestObjective, Dist2(ob.pos, self.pos));
  float base[3];
  if (BotStartPoint(self, base)) in.baseDistance = Dist2(base, self.pos);
  return gms::agents::RecallAllowed(in, kRecall);
}

// Returns true when a reflex acted this think (the plan's gambits are skipped).
bool BotReflexStep(void* o, AgentState& st, const ObjInfo& self, const BotView& v, const BotPlayerView& pv,
                   std::uint32_t mtarget, unsigned long long nowMs) {
  const float maxHp = SehMaxHp(o);
  if (!(maxHp > 0.f) || self.hp <= 0.f) return false;
  const gms::agents::ReflexConfig cfg;
  gms::agents::ReflexSensors s;
  s.role = BotRoleOf(self.id);
  s.hpFrac = self.hp / maxHp;
  // the tower that hit it last, while it stands and the bot is still within its reach
  const Snap* hitTower = nullptr;
  if (st.lastTowerHitMs && nowMs - st.lastTowerHitMs <= static_cast<unsigned long long>(cfg.towerZoneMs))
    if (const Snap* t = FindSnap(g_snapTowers, st.lastTowerId)) {
      s.inTowerZone = Dist2(t->pos, self.pos) <= cfg.towerRange;
      hitTower = t;
    }
  // its target, when it is an enemy shaper
  if (const Snap* t = mtarget ? FindSnap(g_snapPlayers, mtarget) : nullptr) {
    if (t->team != self.team) {
      s.targetIsShaper = true;
      s.targetHpFrac = t->HpFrac();
      for (const Snap& tw : g_snapTowers)
        if (tw.team != self.team && Dist2(tw.pos, t->pos) <= cfg.towerRange) s.targetInTowerZone = true;
    }
  }
  s.engagePlan = v.requestMission && v.missionType > 0;
  if (const Snap* t = mtarget ? FindSnap(g_snapTowers, mtarget) : nullptr)
    if (t->team != self.team) {
      s.targetIsTower = true;
      s.targetTowerHpFrac = t->HpFrac();
    }
  // the shapers around: who counts, who attacked, who is in the jungle
  const Snap* attacker = nullptr;
  const Snap* jungle = nullptr;
  float jungleDist = 3.0e38f;
  for (const Snap& p : g_snapPlayers) {
    if (p.id == self.id) continue;
    const float d = Dist2(p.pos, self.pos);
    if (p.team == self.team) {
      if (d <= kNearShapers) ++s.alliesNear;
      continue;
    }
    if (!SehVisible(o, p.obj)) continue;
    if (d <= kNearShapers) ++s.enemiesNear;
    if (p.id == st.lastShaperAttacker && d <= kAttackerReach) attacker = &p;
    if (d <= cfg.jungleEngageRange && d < jungleDist) {
      float laneDist = 0.f;
      if (::director::NearestLane(p.pos[0], p.pos[1], &laneDist) != 0 && laneDist >= cfg.jungleLaneDistance) {
        jungle = &p;
        jungleDist = d;
      }
    }
  }
  if (attacker && st.lastShaperHitMs) {
    s.shaperHitAgeMs = static_cast<long long>(nowMs - st.lastShaperHitMs);
    s.attackerHpFrac = attacker->HpFrac();
  }
  if (jungle) {
    s.jungleEnemy = true;
    s.jungleEnemyHpFrac = jungle->HpFrac();
    s.jungleEnemyDist = jungleDist;
  }
  // a well of its team on its side of the map under attack
  ::wells::Threat wellPos;
  bool wellThreat = false;
  if (gms::agents::ReflexIsJungler(s.role)) {
    ::wells::Threat th[4];
    const int n = ::wells::Threats(self.team, th, 4);
    float best = kWellSideReach;
    for (int i = 0; i < n; ++i) {
      if (gms::bots::LaneByDiagonal(th[i].pos[0], th[i].pos[1]) !=
          gms::bots::LaneByDiagonal(self.pos[0], self.pos[1]))
        continue;
      const float d = Dist2(th[i].pos, self.pos);
      if (d < best) {
        best = d;
        wellPos = th[i];
        wellThreat = true;
      }
    }
  }
  s.wellThreat = wellThreat;

  const gms::agents::ReflexDecision d = gms::agents::DecideReflex(s, cfg);
  if (d.kind == gms::agents::kReflexNone) {
    st.reflexKind = 0;
    return false;
  }
  int res = 0;
  if (d.kind == gms::agents::kReflexBack) {
    // away from what threatens it: the tower, else the attacker, else the jungle shaper
    const float* from = d.why[0] == 't' && hitTower ? hitTower->pos : attacker ? attacker->pos : jungle ? jungle->pos : nullptr;
    float goal[3];
    StepPoint(self.pos, from ? from : self.pos, kRetreatStep, false, self.team, goal);
    res = SehBotRequest(kAbilityBotMoveToTarget, -1, self.id, 0, goal, 1);
    if (res == gms::agents::kResultHoldsAgent) res = 1;
  } else if (d.kind == gms::agents::kReflexFight) {
    const Snap* tgt = d.why[0] == 'r' ? attacker : jungle;  // "retaliate" / "jungle"
    if (tgt) res = ReflexAttack(o, self, pv, mtarget, *tgt);
  } else {  // defend: whoever attacks the well, else walk to it
    const Snap* tgt = nullptr;
    float best = 30.f;
    for (const Snap& p : g_snapPlayers) {
      if (p.team == self.team || Dist2(p.pos, wellPos.pos) > best || !SehVisible(o, p.obj)) continue;
      best = Dist2(p.pos, wellPos.pos);
      tgt = &p;
    }
    if (tgt) {
      res = ReflexAttack(o, self, pv, mtarget, *tgt);
    } else {
      float goal[3];
      StepPoint(self.pos, wellPos.pos, kWellStep, true, self.team, goal);
      if (Dist2(self.pos, wellPos.pos) <= kWellStep) std::memcpy(goal, wellPos.pos, sizeof goal);
      res = SehBotRequest(kAbilityBotMoveToTarget, -1, self.id, 0, goal, 1);
      if (res == gms::agents::kResultHoldsAgent) res = 1;
    }
  }
  const bool acted = res == 1 || res == 2 || res == 5;
  if (d.kind != st.reflexKind || nowMs - st.reflexLogMs >= 10000) {
    st.reflexLogMs = nowMs;
    if (IdleLogBudget())
      Log("agents: bot %08X %s reflex %s (%s): hp %.0f%%, role %d, enemies %d allies %d, result %d%s (BOTREFLEX) "
          "(t %.2f s)",
          self.id, st.noun,
          d.kind == gms::agents::kReflexBack ? "back off"
                                             : d.kind == gms::agents::kReflexFight ? "fight" : "defend a well",
          d.why, static_cast<double>(s.hpFrac * 100.f), s.role, s.enemiesNear, s.alliesNear, res,
          acted ? "" : " (not taken)", static_cast<double>(nowMs) / 1000.0);
  }
  st.reflexKind = d.kind;
  if (!acted) return false;
  if (st.pointPursuit) EndPointPursuit(o, st, self, 0, nullptr, nowMs);
  st.idleSinceMs = 0;
  const bool pursue = res == 5;
  if (pursue != st.pursue) {
    st.pursue = pursue;
    SehSetTreeFlag(o, 8, pursue);
    ++(pursue ? g_st.pursueOn : g_st.pursueOff);
  }
  return true;
}

void BotBrainStep(void* sim, void* o, AgentState& st, unsigned long long nowMs) {
  if (st.leaf == kLeafDeath || st.leaf == kLeafDeathAbility || st.leaf == kLeafDeathAbilityDefault ||
      st.leaf == 0x0141E1B8 || st.leaf == 0x0141DE90)
    return;
  if (nowMs < st.botNextThinkMs) return;
  st.botNextThinkMs = nowMs + static_cast<unsigned long long>(g_opt.botThinkMs > 0 ? g_opt.botThinkMs : 200);
  ObjInfo self;
  if (!SehObjInfo(o, &self) || (self.hasHp && self.hp <= 0.f) || GameOver(sim)) return;
  BotView v;
  if (st.botPlan == -2) {  // first sight: the start plan AgentBrain_InitBot chose
    if (!SehReadBotView(o, -1, -1, &v)) return;
    if (!SehReadBotView(o, v.enginePlan, -1, &v)) return;
    BotEnterPlan(st, v, v.enginePlan, nowMs);
    st.botPhase = -1;
    if (LogBudget())
      Log("agents: bot %08X %s brain: %d nodes, start plan %d (%s), phase %d", self.id, st.noun, v.nodeCount,
          v.enginePlan, v.planOk ? v.planName : "none", v.enginePhase);
  } else if (!SehReadBotView(o, st.botPlan, st.botPhase, &v)) {
    return;
  }
  ++g_bot.thinks;
  const double inPlan = static_cast<double>(nowMs - st.planEnteredMs) / 1000.0;

  // 1. the plan node's links to other plans (prioritized list)
  if (v.planOk) {
    for (std::size_t i = 0; i < v.nodeLinks.size() && i < st.nodeLinkReady.size(); ++i) {
      const BotLink& l = v.nodeLinks[i];
      if (nowMs < st.nodeLinkNext[i]) continue;
      if (l.interval > 0) st.nodeLinkNext[i] = nowMs + static_cast<unsigned long long>(l.interval * 1000.0f);
      gms::agents::LinkView lv = BotLinkView(l, inPlan, st.nodeLinkReady[i], st.nodeLinkFired[i] != 0, nowMs);
      if (!gms::agents::LinkOpen(lv)) continue;
      std::uint32_t unused = 0;
      std::int32_t linkMission = 0;  // D6b: condition_Bot_IsMissionAvailable's mission id (ConditionOut::linkValue)
      if (!Conditions(l.conditions, l.conditionCount, self.id, &unused, false, nullptr, &linkMission)) continue;
      bool isPlan = false, isPhase = false;
      if (!SehNodeKind(o, l.outputNode, &isPlan, &isPhase) || !isPlan) continue;
      st.nodeLinkFired[i] = 1;
      st.nodeLinkReady[i] =
          nowMs + static_cast<unsigned long long>(gms::agents::LinkCooldownSeconds(l.cooldown, l.maxCooldown, Random01()) *
                                                  1000.0);
      BotView next;
      if (!SehReadBotView(o, l.outputNode, -1, &next) || !next.planOk) break;
      ++g_bot.planChanges;
      // D6c: a quick exit (the plan left within kQuickExitMs) and a bounce (back into the plan left just before)
      const bool quick = nowMs - st.planEnteredMs < kQuickExitMs;
      const bool bounce = l.outputNode == st.prevPlan && nowMs - st.prevPlanLeftMs < kQuickExitMs;
      float targetDist = -2.f;
      if (quick) {
        ++g_bot.quickExits;
        if (v.requestMission) {
          targetDist = SehDistanceToTarget(o, self);
          ++(targetDist < 0 ? g_bot.quickExitNoTarget
                            : targetDist < 30.f ? g_bot.quickExitNear
                                                : targetDist <= 55.f ? g_bot.quickExitMid : g_bot.quickExitFar);
        }
      }
      if (bounce) ++g_bot.bounces;
      if (LogBudget()) {
        char extra[64] = "";
        if (targetDist >= 0.f)
          std::snprintf(extra, sizeof extra, "; quick exit, mTarget at %.1f units", static_cast<double>(targetDist));
        else if (targetDist == -1.f)
          std::snprintf(extra, sizeof extra, "; quick exit, no mTarget");
        Log("agents: bot %08X %s plan %d (%s) -> %d (%s) by link %zu after %.2f s%s%s (t %.2f s)", self.id, st.noun,
            st.botPlan, v.planName, l.outputNode, next.planName, i, inPlan, bounce ? ", a bounce" : "", extra,
            static_cast<double>(nowMs) / 1000.0);
      }
      st.prevPlan = st.botPlan;
      st.prevPlanLeftMs = nowMs;
      BotEnterPlan(st, next, l.outputNode, nowMs);
      SehSetBrainNodes(v.brain, l.outputNode, st.botPhase);
      // D6b: the plan's mission (bots.h OnPlanEntered: take the link's id, open an own one, or leave)
      AgentInfo ai;
      const std::uint32_t mtarget = SehAgentInfo(o, &ai) ? SehBestTarget(ai.agent) : 0;
      bots::OnPlanEntered(self.id, self.team, next.missionType, next.requestMission,
                          linkMission > 0 ? static_cast<std::uint32_t>(linkMission) : 0u, mtarget, st.noun,
                          next.planName);
      v = next;
      break;
    }
  }
  // D6b: a requestMission plan (EngageForKill) keeps its own mission open while the bot stays in it
  if (v.planOk && v.requestMission && v.missionType > 0) {
    AgentInfo ai;
    if (SehAgentInfo(o, &ai)) bots::OnRequestPlanThink(self.id, self.team, v.missionType, SehBestTarget(ai.agent));
  }

  // 2. the plan's links to its phases (ChoosePhaseLink: the current phase's link counts while it cools down)
  if (v.planOk) {
    const double inPhase = static_cast<double>(nowMs - st.phaseEnteredMs) / 1000.0;
    int chosen = -1;
    for (std::size_t i = 0; i < v.phaseLinks.size() && i < st.phaseLinkReady.size(); ++i) {
      const BotLink& l = v.phaseLinks[i];
      const bool current = l.outputNode == st.botPhase;
      if (nowMs < st.phaseLinkNext[i]) {
        if (current) {  // between its checks the current phase's link holds (checkTimeInterval)
          chosen = static_cast<int>(i);
          break;
        }
        continue;
      }
      if (l.interval > 0) st.phaseLinkNext[i] = nowMs + static_cast<unsigned long long>(l.interval * 1000.0f);
      gms::agents::LinkView lv = BotLinkView(l, current ? inPhase : inPlan, st.phaseLinkReady[i],
                                             st.phaseLinkFired[i] != 0, nowMs);
      if (!gms::agents::LinkOpen(lv, current)) continue;
      std::uint32_t unused = 0;
      if (!Conditions(l.conditions, l.conditionCount, self.id, &unused, false)) continue;
      chosen = static_cast<int>(i);
      break;
    }
    if (chosen >= 0 && v.phaseLinks[static_cast<std::size_t>(chosen)].outputNode != st.botPhase) {
      const BotLink& l = v.phaseLinks[static_cast<std::size_t>(chosen)];
      bool isPlan = false, isPhase = false;
      if (SehNodeKind(o, l.outputNode, &isPlan, &isPhase) && isPhase) {
        st.phaseLinkFired[static_cast<std::size_t>(chosen)] = 1;
        st.phaseLinkReady[static_cast<std::size_t>(chosen)] =
            nowMs + static_cast<unsigned long long>(
                        gms::agents::LinkCooldownSeconds(l.cooldown, l.maxCooldown, Random01()) * 1000.0);
        BotView next;
        if (SehReadBotView(o, st.botPlan, l.outputNode, &next) && next.phaseOk) {
          ++g_bot.phaseChanges;
          if (LogBudget())
            Log("agents: bot %08X %s phase %d -> %d (%s, default %s) in plan %d (%s) (t %.2f s)", self.id, st.noun,
                st.botPhase, l.outputNode, next.phaseName, next.defaultBehavior ? next.defaultBehavior : "none",
                st.botPlan, v.planName, static_cast<double>(nowMs) / 1000.0);
          BotEnterPhase(st, l.outputNode, next.gambits.size());
          st.phaseEnteredMs = nowMs;
          st.seqCursor = -1;
          SehSetBrainNodes(v.brain, st.botPlan, st.botPhase);
          v = next;
        }
      }
    }
  }
  if (!v.phaseOk) {
    // no phase yet (the Idle plan has none): the tree idles (leaf 17)
    ++g_bot.noPhase;
    if (!SehReadBotView(o, st.botPlan, st.botPhase, &v) || !v.phaseOk) {
      st.idleSinceMs = 0;  // BOTIDLE: the Idle plan's standing is the script's
      SehSetTreeFlag(o, 0x40, false);
      if (st.pointPursuit) EndPointPursuit(o, st, self, 0, nullptr, nowMs);  // D6d
      return;
    }
  }
  if (st.gambitReadyMs.size() != v.gambits.size()) st.gambitReadyMs.assign(v.gambits.size(), 0);

  // BOTTEMPO: a target that is no longer a valid hostile one is dropped (gms/core/agents/brain.h TargetStale), so the
  // Pursue leaf doesn't keep the bot on its spot; a running Pursue leaf is restarted (request 0x400) to redecide
  if (g_opt.botStaleTarget) {
    const std::uint32_t raw = SehRawTarget(o);
    if (gms::agents::TargetStale(raw != 0, raw != 0 && SehHostile(self.team, raw))) {
      SehSetTarget(o, 0);
      ++g_bot.staleTargets;
      const bool restart = st.leaf == kLeafPursue && !st.pointPursuit && SehResetDefaultBehavior(o);
      if (LogBudget()) {
        void* tobj = SehFind(raw);
        Log("agents: bot %08X %s phase %d (%s): mTarget %08X (%s) is no longer a valid hostile target: dropped%s "
            "(BOTTEMPO) (t %.2f s)",
            self.id, st.noun, st.botPhase, v.phaseName, raw, tobj ? SehNounName(tobj) : "gone",
            restart ? ", Pursue restarted" : "", static_cast<double>(nowMs) / 1000.0);
      }
    }
  }
  // BOTTEMPO: the hold watchdog (HoldStuck): the same instance holding the agent while the bot stands still away from
  // home for botHoldStuckMs ends
  if (g_opt.botHoldStuckMs > 0) {
    constexpr float kHoldMove = 3.f, kHomeRadius = 40.f;
    unsigned hid = 0;
    const char* hname = nullptr;
    float base[3];
    const bool atHome = BotStartPoint(self, base) && Distance2D(base, self.pos) <= kHomeRadius;
    if (atHome || !SehHeldAbility(o, &hid, &hname)) {
      st.holdInst = 0;
    } else if (hid != st.holdInst) {
      st.holdInst = hid;
      st.holdSinceMs = nowMs;
      st.holdPos[0] = self.pos[0];
      st.holdPos[1] = self.pos[1];
    } else {
      const float dx = self.pos[0] - st.holdPos[0], dy = self.pos[1] - st.holdPos[1];
      const float moved = std::sqrt(dx * dx + dy * dy);
      if (moved >= kHoldMove) {  // it walks: a new window
        st.holdSinceMs = nowMs;
        st.holdPos[0] = self.pos[0];
        st.holdPos[1] = self.pos[1];
      } else if (gms::agents::HoldStuck(static_cast<double>(nowMs - st.holdSinceMs) / 1000.0, moved,
                                        g_opt.botHoldStuckMs / 1000.0, kHoldMove)) {
        const bool ended = SehEndAbility(hid);
        ++g_bot.holdStuck;
        if (LogBudget())
          Log("agents: bot %08X %s phase %d (%s): %s (instance %08X) held it %.1f s on one spot at (%.1f, %.1f): %s "
              "(BOTTEMPO) (t %.2f s)",
              self.id, st.noun, st.botPhase, v.phaseName, hname ? hname : "?", hid,
              static_cast<double>(nowMs - st.holdSinceMs) / 1000.0, static_cast<double>(self.pos[0]),
              static_cast<double>(self.pos[1]), ended ? "ended" : "gone already", static_cast<double>(nowMs) / 1000.0);
        st.holdInst = 0;
      }
    }
  }

  // D6c: while an uninterruptible ability holds the agent (a cast before its ReleaseAgent, a dodge, a channel) the bot
  // neither requests a gambit nor restarts its default behavior: either would abort the ability (SehHeldByUninterruptible)
  const char* held = nullptr;
  if (g_opt.respectCanInterrupt && SehHeldByUninterruptible(o, &held)) {
    ++g_bot.heldThinks;
    st.idleSinceMs = 0;  // BOTIDLE: an ability holds the agent, the bot is not idle
    return;
  }

  // 3. the default behavior: flag 0x40 while the phase has one; a new one restarts through request 0x400
  const std::string behavior = v.defaultBehavior ? v.defaultBehavior : "";
  if (behavior != st.botBehavior) {
    if (!st.botBehavior.empty() && !behavior.empty() && SehResetDefaultBehavior(o)) ++g_bot.behaviorResets;
    st.botBehavior = behavior;
  }
  SehSetTreeFlag(o, 0x40, !behavior.empty());

  // 4. the phase's gambits (prioritized lists evaluated lazily: the first usable one wins)
  BotPlayerView pv;
  if (!SehBotPlayer(sim, o, &pv)) return;
  AgentInfo a;
  if (!SehAgentInfo(o, &a)) return;
  const std::uint32_t mtarget = SehBestTarget(a.agent);
  // BOTREFLEX: danger and opportunity before the plan's own gambits
  if (g_opt.botReflex && BotReflexStep(o, st, self, v, pv, mtarget, nowMs)) return;
  std::vector<gms::agents::GambitView> views(v.gambits.size());
  std::vector<std::uint32_t> targets(v.gambits.size(), 0);
  std::vector<float> spots(v.gambits.size() * 3, 0.f);
  std::vector<char> pointOnly(v.gambits.size(), 0);  // D6d: the conditions aimed the gambit at a point, no object
  int chosen = -1;
  const bool prioritized = v.phaseType == gms::agents::kPrioritizedList;
  int r = 0;
  gms::agents::GambitAim aim;  // D6d: the request's object, or a point alone
  const float* spot = nullptr;
  bool skippedThis = false;  // BOTIDLE: the last request was refused in a way that lets the next gambit try

  // the request of gambit `idx` (its object, its point, the slot's ability); its result
  auto request = [&](int idx) -> int {
    const BotGambit& g = v.gambits[static_cast<std::size_t>(idx)];
    std::uint32_t target = targets[static_cast<std::size_t>(idx)];
    if (target && !SehFind(target)) target = 0;
    spot = &spots[static_cast<std::size_t>(idx) * 3];
    const bool hasSpot = spot[0] != 0.f || spot[1] != 0.f;
    // D6d: a condition that aimed the gambit at a point (useTargetInfo; kObjIDNone with useTarget: the ward's spot)
    // gives it no object, so the agent's mTarget doesn't stand in for one
    aim = gms::agents::AimGambit(target, mtarget, hasSpot, pointOnly[static_cast<std::size_t>(idx)] != 0,
                                 g_opt.botPursuePoint);
    target = aim.target;
    if (aim.atPoint) ++g_bot.pointRequests;
    ObjInfo t;
    void* tobj = SehFind(target);
    float pos[3] = {self.pos[0], self.pos[1], self.pos[2]};
    if (hasSpot) std::memcpy(pos, spot, sizeof pos);  // the condition's position first
    else if (tobj && SehObjInfo(tobj, &t)) std::memcpy(pos, t.pos, sizeof pos);
    ++g_bot.requests;
    int res;
    if (gms::agents::SlotIsConsumable(g.slot)) {
      const int inv = gms::agents::SlotInventoryIndex(g.slot);
      res = SehBotUseItem(pv.player, pv.items[inv], static_cast<unsigned>(inv));
      res = res > 0 ? 1 : gms::agents::kResultItemRefused;  // used / refused
      ++g_bot.items;
    } else {
      // a hostile target becomes the agent's mTarget: GetBestTarget's bot case, which leaf 12 (Pursue) follows
      if (target && target != mtarget && SehHostile(self.team, target)) SehSetTarget(o, target);
      const int slot = g.guid ? (g.slot >= 0 ? g.slot : -1) : g.slot;
      const int rank = gms::agents::SlotIsRanked(g.slot) ? pv.ranks[g.slot] : 1;
      res = 0;
      if (g.guid == kAbilityBotBack) {
        const bool flee = InFleePhase(v.phaseName);
        float base[3], goal[3];
        // BOTBACK: outside a Flee phase the Back gambit (an enemy shaper or a pile of minions close) is a step back, not
        // the walk home that nothing ends
        if (!flee && g_opt.botBackStep && BotStartPoint(self, base) &&
            gms::agents::BackStepGoal(self.pos, base, kRecall, goal)) {
          res = SehBotRequest(kAbilityBotMoveToTarget, -1, self.id, 0, goal, 1);
          if (res == gms::agents::kResultHoldsAgent) res = 1;  // the last step still runs
          if (res == 1 || res == 2) ++g_bot.backSteps;
        }
        // BOTRECALL: a Flee phase's walk home is a recall once the bot is out of immediate danger; a recall that runs
        // already (ours or the data's own Flee gambit) answers -0x270B and counts as started, never as a refusal that
        // falls through to the walk (the walk would abort it)
        if (flee && g_opt.botRecall && RecallPossible(st, self, v.phaseName, nowMs)) {
          static const float kZero3[3] = {};
          const int raw = SehBotRequest(kAbilityReturnToBase, -1, self.id, 0, kZero3, 1);
          res = gms::agents::RecallRequestResult(raw);
          if (raw == 1 || raw == 2) ++g_bot.recalls;
        }
      }
      if (res != 1 && res != 2) res = SehBotRequest(g.guid, slot, self.id, target, pos, rank);
    }
    if (res == 1 || res == 2) {
      ++g_bot.started;
      st.gambitReadyMs[static_cast<std::size_t>(idx)] =
          nowMs + static_cast<unsigned long long>(
                      gms::agents::GambitCooldownSeconds(g.cooldown, g.maxCooldown, g.randomize, Random01()) * 1000.0);
    } else if (res == 5) {
      ++g_bot.outOfRange;
    } else {
      ++g_bot.refused;
    }
    // BOTIDLE: a refusal that lets the next gambit try is logged on change only, as its own line
    skippedThis = g_opt.botGambitFallthrough && prioritized && gms::agents::GambitRefusalSkips(res, g.slot);
    if (skippedThis) {
      if ((idx != st.skipGambit || res != st.skipResult) && IdleLogBudget())
        Log("agents: bot %08X %s phase %d (%s) gambit %d (slot %d, ability %08X) on %08X refused (%d: %s): the next "
            "gambit may try (BOTIDLE) (t %.2f s)",
            self.id, st.noun, st.botPhase, v.phaseName, idx, g.slot, g.guid, target, res, RefusalName(res),
            static_cast<double>(nowMs) / 1000.0);
      st.skipGambit = idx;
      st.skipResult = res;
      return res;
    }
    if ((res != st.lastResult || g.guid != st.lastGuid || g.slot != st.lastSlot) && LogBudget())
      Log("agents: bot %08X %s phase %d (%s) gambit %d (slot %d, ability %08X) on %08X -> %d%s (t %.2f s)", self.id,
          st.noun, st.botPhase, v.phaseName, idx, g.slot, g.guid, target, res,
          res == 1 || res == 2 ? " started" : res == 5 ? " out of range: pursue" : "",
          static_cast<double>(nowMs) / 1000.0);
    st.lastResult = res;
    st.lastGuid = g.guid;
    st.lastSlot = g.slot;
    return res;
  };

  // BOTIDLE: a refused request (cooldown, no resource, CanUse false, ...) no longer ends the think: the list goes on
  // with the next gambit whose conditions pass (GambitRefusalSkips), up to kMaxSkips
  constexpr int kMaxSkips = 8;
  std::size_t from = 0;
  int skips = 0;
  for (;;) {
    chosen = -1;
    for (std::size_t i = from; i < v.gambits.size(); ++i) {
      const BotGambit& g = v.gambits[i];
      gms::agents::GambitView& gv = views[i];
      gv.coolingDown = nowMs < st.gambitReadyMs[i];
      gv.waitOnCondition = g.waitOnCondition;
      gv.weight = g.weight;
      gv.conditionsPass = false;
      if (!GambitAvailable(g, pv)) continue;
      if (gv.coolingDown && !(prioritized && g.waitOnCondition)) continue;
      bool po = false;
      gv.conditionsPass =
          Conditions(g.conditions, g.conditionCount, self.id, &targets[i], true, &spots[i * 3], nullptr, &po);
      pointOnly[i] = po ? 1 : 0;
      ++g_bot.conditions;
      if (prioritized && gv.conditionsPass) {
        chosen = gv.coolingDown ? -1 : static_cast<int>(i);  // a waiting gambit holds the list while it cools down
        break;
      }
    }
    if (!prioritized) chosen = gms::agents::ChooseGambit(v.phaseType, views, &st.seqCursor, Random01());
    if (chosen < 0) break;
    r = request(chosen);
    if (!skippedThis || !prioritized || ++skips > kMaxSkips) break;
    ++g_bot.skipped;
    from = static_cast<std::size_t>(chosen) + 1;
    if (from >= v.gambits.size()) break;
  }
  if (skips > 0 && chosen >= 0 && (r == 1 || r == 2 || r == 5)) ++g_bot.skippedThenActed;
  // D6d: the pursuit of a point: the point into the blackboard, mTarget cleared; cleared again when it ends
  const bool startedAtPoint = chosen >= 0 && aim.atPoint && (r == 1 || r == 2);
  if (g_opt.botPursuePoint) {
    if (startedAtPoint) {  // a gambit at a point started (the bots' wards)
      ++g_bot.pointStarted;
      if (PointLogBudget())
        Log("agents: bot %08X %s phase %d (%s) gambit %d at the point (%.1f, %.1f) started, %.1f units away (t %.2f s)",
            self.id, st.noun, st.botPhase, v.phaseName, chosen, static_cast<double>(spot[0]),
            static_cast<double>(spot[1]), static_cast<double>(Distance2D(spot, self.pos)),
            static_cast<double>(nowMs) / 1000.0);
    }
    if (chosen >= 0 && gms::agents::PursuesPoint(aim, r)) BeginPointPursuit(o, st, self, v, chosen, spot, nowMs);
    else if (st.pointPursuit) EndPointPursuit(o, st, self, r, startedAtPoint ? spot : nullptr, nowMs);
  } else if (chosen >= 0 && r == 5 && spot && (spot[0] != 0.f || spot[1] != 0.f) &&
             (aim.target != st.pointDiagTarget || std::fabs(spot[0] - st.point[0]) > 0.01f ||
              std::fabs(spot[1] - st.point[1]) > 0.01f)) {
    // D6a's rule ([host] bot_pursue_point=0), for the comparison: what the Pursue leaf gets to follow
    const std::uint32_t requestTarget = aim.target;
    st.pointDiagTarget = requestTarget;
    std::memcpy(st.point, spot, sizeof st.point);
    ObjInfo t;
    void* tobj = requestTarget ? SehFind(requestTarget) : nullptr;
    const bool known = tobj && SehObjInfo(tobj, &t);
    if (PointLogBudget())
      Log("agents: bot %08X %s phase %d (%s) gambit %d at the point (%.1f, %.1f), %.1f units away: out of range; the "
          "request's object %08X (%s, hp %.0f, %.1f units away), mTarget %08X (t %.2f s)",
          self.id, st.noun, st.botPhase, v.phaseName, chosen, static_cast<double>(spot[0]),
          static_cast<double>(spot[1]), static_cast<double>(Distance2D(spot, self.pos)), requestTarget,
          known ? SehNounName(tobj) : (requestTarget ? "gone" : "none"), known && t.hasHp ? static_cast<double>(t.hp) : -1.0,
          known ? static_cast<double>(Distance2D(t.pos, self.pos)) : -1.0, SehRawTarget(o),
          static_cast<double>(nowMs) / 1000.0);
  }
  bool pursue = chosen >= 0 && r == 5;
  const bool acted = chosen >= 0 && (r == 1 || r == 2 || r == 5);

  // BOTIDLE (pursuit watchdog, [host] bot_pursue_stuck_ms): a pursuit (result 5, the Pursue leaf follows mTarget or the
  // point) that gets no closer for the window drops its gambit for kStuckHoldMs, so the list goes on without it
  if (g_opt.botPursueStuckMs > 0) {
    float dist = -1.f;
    std::uint32_t tid = 0;
    if (pursue) {
      tid = aim.target;
      void* tobj = tid ? SehFind(tid) : nullptr;
      ObjInfo t;
      if (tobj && SehObjInfo(tobj, &t)) dist = Distance2D(t.pos, self.pos);
      else if (spot && (spot[0] != 0.f || spot[1] != 0.f)) dist = Distance2D(spot, self.pos);
    }
    if (dist < 0.f) {
      st.pwGambit = -1;
    } else if (st.pwGambit != chosen || st.pwTarget != tid) {
      st.pwGambit = chosen;
      st.pwTarget = tid;
      st.pwSinceMs = nowMs;
      st.pwDist = dist;
    } else if (st.pwDist - dist >= kStuckProgress) {  // closer: a new window
      st.pwSinceMs = nowMs;
      st.pwDist = dist;
    } else if (gms::agents::PursuitStuck(static_cast<double>(nowMs - st.pwSinceMs) / 1000.0, st.pwDist, dist,
                                         g_opt.botPursueStuckMs / 1000.0, kStuckProgress)) {
      ++g_bot.pursuitStuck;
      if (IdleLogBudget())
        Log("agents: bot %08X %s phase %d (%s) gambit %d: pursuit of %08X made no progress in %.1f s (%.1f -> %.1f "
            "units); the gambit is left out for %.1f s (BOTIDLE) (t %.2f s)",
            self.id, st.noun, st.botPhase, v.phaseName, chosen, tid, static_cast<double>(nowMs - st.pwSinceMs) / 1000.0,
            static_cast<double>(st.pwDist), static_cast<double>(dist), static_cast<double>(kStuckHoldMs) / 1000.0,
            static_cast<double>(nowMs) / 1000.0);
      st.gambitReadyMs[static_cast<std::size_t>(chosen)] = nowMs + kStuckHoldMs;
      st.pwGambit = -1;
      pursue = false;
      if (st.pointPursuit) EndPointPursuit(o, st, self, r, nullptr, nowMs);
    }
  }

  // BOTIDLE (idle fallback, [host] bot_idle_fallback_ms): a bot that stands on the tree's IdleDefault with no default
  // behavior and no gambit acting while something hits it hits back (a basic attack on the attacker, walking up on 5),
  // so a phase whose gambits are all refused, cooling or condition-less cannot leave it to die standing
  if (g_opt.botIdleFallbackMs > 0) {
    const bool standing = !acted && behavior.empty() && (st.leaf == kLeafIdleDefault || st.leaf == kLeafIdle);
    if (!standing) {
      st.idleSinceMs = 0;
    } else {
      if (!st.idleSinceMs) st.idleSinceMs = nowMs;
      if (st.lastAttacker &&
          gms::agents::IdleFallbackDue(static_cast<long long>(nowMs - st.idleSinceMs),
                                       static_cast<long long>(nowMs - st.lastHitMs), g_opt.botIdleFallbackMs,
                                       kFallbackHitWindowMs)) {
        void* aobj = SehFind(st.lastAttacker);
        ObjInfo at;
        if (aobj && SehObjInfo(aobj, &at) && at.hasHp && at.hp > 0.f && at.team != self.team &&
            Distance2D(at.pos, self.pos) <= kFallbackReach &&
            gms::agents::FallbackAttackerType(SehNpcType(aobj), at.player) && SehHostile(self.team, at.id)) {
          ++g_bot.fallbackTries;
          if (at.id != mtarget) SehSetTarget(o, at.id);
          const int fr = SehBotRequest(0, gms::agents::kSlotBasic, self.id, at.id, at.pos, 1);
          if (fr == 1 || fr == 2 || fr == 5) {
            ++g_bot.fallbackActed;
            if (fr == 5) pursue = true;
          }
          if (fr != st.fallbackResult && IdleLogBudget())
            Log("agents: bot %08X %s phase %d (%s) idle %.1f s while %08X (%s) hit it %.1f s ago, %.1f units away: "
                "basic attack -> %d%s (BOTIDLE) (t %.2f s)",
                self.id, st.noun, st.botPhase, v.phaseName, static_cast<double>(nowMs - st.idleSinceMs) / 1000.0, at.id,
                SehNounName(aobj), static_cast<double>(nowMs - st.lastHitMs) / 1000.0,
                static_cast<double>(Distance2D(at.pos, self.pos)), fr,
                fr == 1 || fr == 2 ? " started" : fr == 5 ? " out of range: pursue" : "",
                static_cast<double>(nowMs) / 1000.0);
          st.fallbackResult = fr;
        }
      }
    }
  }
  if (pursue != st.pursue) {
    st.pursue = pursue;
    SehSetTreeFlag(o, 8, pursue);
    ++(pursue ? g_st.pursueOn : g_st.pursueOff);
  }
}

bool SehIsBot(void* o) {
  __try {
    return GameObjectIsBot(o);
  } __except (SehNote(GetExceptionInformation(), "GameObject_IsBot")) {
    return false;
  }
}

// ---- MINJUNGLE: the minion watch ----------------------------------------------------------------------------------

int SehAggroTypeOf(void* o) {
  __try {
    if (!Field<void*>(o, obj::kBrain) || !Field<void*>(o, agent::kOfObject)) return -1;
    return GetAggroType(o);
  } __except (SehNote(GetExceptionInformation(), "aggro type")) {
    return -1;
  }
}

// What a standing minion's engine state says: tree flags and leaf, the lane (spline id, index), the tether, the
// blackboard flags, the locomotion goal and the Lua thread's wait condition (GearsLua thread: +0x5C the wait function,
// +0x60 its object, +0x64 the goal, +0x70 the distance squared).
struct MinionDetail {
  unsigned treeFlags = 0;
  std::uintptr_t leaf = 0;
  std::uint32_t splineId = 0, splineIndex = 0;
  int tetherOff = -1, engaged = -1, inCombat = -1;
  unsigned goalFlags = 0;
  float goal[3] = {};
  std::uintptr_t waitFn = 0;
  std::uint32_t waitObj = 0;
  float waitGoal[3] = {}, waitDist2 = 0.f;
  std::uint32_t target = 0;
  float targetPos[3] = {};
};

bool SehMinionDetail(void* o, MinionDetail* d) {
  __try {
    void* btd = Field<void*>(o, obj::kBehaviorTree);
    if (btd) {
      d->treeFlags = Field<unsigned>(btd, bt::kFlags);
      d->leaf = Field<std::uintptr_t>(btd, bt::kActiveLeaf);
    }
    char* lo = Field<char*>(o, loco::kOfObject);
    if (lo) {
      d->splineId = Field<std::uint32_t>(lo, loco::kSplineId);
      d->splineIndex = Field<std::uint32_t>(lo, loco::kSplineIndex);
      d->goalFlags = Field<unsigned>(lo, loco::kGoalFlags);
      std::memcpy(d->goal, lo + loco::kGoal, sizeof d->goal);
    }
    char* ag = Field<char*>(o, agent::kOfObject);
    if (ag) {
      d->tetherOff = Field<unsigned char>(ag, agent::kTetherDisabled);
      d->engaged = Field<unsigned char>(ag, agent::kEngaged);
      d->inCombat = Field<unsigned char>(ag, agent::kInCombat);
      d->target = Field<std::uint32_t>(ag, agent::kTarget);
      std::memcpy(d->targetPos, ag + agent::kTargetPosition, sizeof d->targetPos);
    }
    const unsigned tid = Field<unsigned>(o, obj::kLuaThread);  // mLuaThreadID (BehaviorTree_StartLuaBehavior 0x008B0980)
    char* th = tid && g_sim ? static_cast<char*>(GetThread(static_cast<char*>(g_sim) + sim::kLua, tid)) : nullptr;
    if (th) {
      d->waitFn = Field<std::uintptr_t>(th, 0x5C);
      d->waitObj = Field<std::uint32_t>(th, 0x60);
      std::memcpy(d->waitGoal, th + 0x64, sizeof d->waitGoal);
      d->waitDist2 = Field<float>(th, 0x70);
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "minion detail")) {
    return false;
  }
}

// 1/0 whether `t` is visible to `o` (targetable: targetable and attackable), -1 on an exception.
int SehVisibleTargetable(void* o, void* t, bool targetable) {
  __try {
    if (targetable) return IsTargetable(t) && IsAttackable(t) ? 1 : 0;
    return IsVisibleTo(o, t) ? 1 : 0;
  } __except (SehNote(GetExceptionInformation(), "visible/targetable")) {
    return -1;
  }
}

void LogIdleMinion(void* o,const AgentState& st, const ObjInfo& self, float laneDist, int lane, double stillS,
                   double now) {
  MinionDetail d;
  SehMinionDetail(o, &d);
  AgentInfo a;
  std::string list;
  std::uint32_t best = 0;
  if (SehAgentInfo(o, &a)) {
    std::vector<AggroEntry> entries;
    if (SehReadAggro(a.agent, &entries)) {
      for (const AggroEntry& e : entries) {
        void* t = SehFind(e.Id());
        ObjInfo ti;
        char b[160];
        if (t && SehObjInfo(t, &ti)) {
          const float dx = ti.pos[0] - self.pos[0], dy = ti.pos[1] - self.pos[1];
          std::snprintf(b, sizeof b, " %08X %s d %.1f hp %.0f flag %u vis %d tgt %d;", e.Id(), SehNounName(t),
                        static_cast<double>(std::sqrt(dx * dx + dy * dy)), static_cast<double>(ti.hp),
                        static_cast<unsigned>(e.raw[agent::kAggroEntryFlag]), SehVisibleTargetable(o, t, false),
                        SehVisibleTargetable(o, t, true));
        } else {
          std::snprintf(b, sizeof b, " %08X gone;", e.Id());
        }
        list += b;
      }
    }
    best = SehBestTarget(a.agent);
  }
  Log("minions: idle off lane: %08X %s team %d at (%.1f, %.1f), %.1f from lane %d, still %.1f s (t %.1f s): leaf %s, "
      "tree flags %X, spline %08X index %u, tether off %d, engaged %d, in combat %d, best %08X, mTarget %08X, target "
      "pos (%.1f, %.1f); locomotion goal flags %X goal (%.1f, %.1f); Lua wait fn %08X obj %08X goal (%.1f, %.1f) dist "
      "%.1f; aggro list [%s]",
      self.id, st.noun, self.team, static_cast<double>(self.pos[0]), static_cast<double>(self.pos[1]),
      static_cast<double>(laneDist), lane, stillS, now, LeafName(d.leaf), d.treeFlags, d.splineId, d.splineIndex,
      d.tetherOff, d.engaged, d.inCombat, best, d.target, static_cast<double>(d.targetPos[0]),
      static_cast<double>(d.targetPos[1]), d.goalFlags, static_cast<double>(d.goal[0]), static_cast<double>(d.goal[1]),
      static_cast<unsigned>(d.waitFn), d.waitObj, static_cast<double>(d.waitGoal[0]), static_cast<double>(d.waitGoal[1]),
      static_cast<double>(d.waitDist2 > 0.f ? std::sqrt(d.waitDist2) : 0.f), list.c_str());
}

// Once every minionWatchMs per agent: is this lane minion standing still away from its lane?
void MinionWatch(void* o, AgentState& st, unsigned long long nowMs) {
  if (g_opt.minionWatchMs <= 0 || nowMs < st.watchNextMs) return;
  st.watchNextMs = nowMs + static_cast<unsigned long long>(g_opt.minionWatchMs);
  if (st.watchAggroType == -99) st.watchAggroType = SehAggroTypeOf(o);
  if (st.watchAggroType != gms::agents::kAggroMinion) return;
  ObjInfo self;
  if (!SehObjInfo(o, &self) || self.player) return;
  if (self.hasHp && self.hp <= 0.f) {
    if (st.watchIdle) {
      st.watchIdle = false;
      ++g_st.watchDiedIdle;
      if (g_st.watchLogged < static_cast<unsigned>(g_opt.minionWatchLogLimit))
        Log("minions: %08X %s died while idle off its lane (t %.1f s)", self.id, st.noun,
            static_cast<double>(nowMs) / 1000.0);
    }
    return;
  }
  const float dx = self.pos[0] - st.watchAnchor[0], dy = self.pos[1] - st.watchAnchor[1];
  if (st.watchSinceMs == 0 || dx * dx + dy * dy > 1.f) {
    if (st.watchIdle) {
      st.watchIdle = false;
      ++g_st.watchRecovered;
      if (g_st.watchLogged < static_cast<unsigned>(g_opt.minionWatchLogLimit))
        Log("minions: %08X %s moves again after %.1f s off its lane (t %.1f s, at (%.1f, %.1f))", self.id, st.noun,
            static_cast<double>(nowMs - st.watchSinceMs) / 1000.0, static_cast<double>(nowMs) / 1000.0,
            static_cast<double>(self.pos[0]), static_cast<double>(self.pos[1]));
    }
    st.watchAnchor[0] = self.pos[0];
    st.watchAnchor[1] = self.pos[1];
    st.watchSinceMs = nowMs;
    return;
  }
  const unsigned long long idleMs = static_cast<unsigned long long>(g_opt.minionIdleMs);
  if (st.watchIdle) {
    // the unstick: an idle minion that still stands one more idle period after its last restart gets another
    if (g_opt.minionUnstick && st.watchResetMs && nowMs - st.watchResetMs >= idleMs) {
      MinionDetail d;
      if (SehMinionDetail(o, &d) && d.engaged != 1 && SehResetDefaultBehavior(o)) {
        st.watchResetMs = nowMs;
        ++g_st.watchResets;
      }
    }
    return;
  }
  if (nowMs - st.watchSinceMs < idleMs) return;
  float laneDist = 0.f;
  const int lane = ::director::NearestLane(self.pos[0], self.pos[1], &laneDist);
  if (lane == 0 || laneDist <= g_opt.minionOffLane) return;
  // a minion that stands while it fights (a caster shooting) is busy, not idle: only a long stand counts then
  MinionDetail d;
  const bool engaged = SehMinionDetail(o, &d) && d.engaged == 1;
  if (engaged && nowMs - st.watchSinceMs < 4ull * idleMs) return;
  st.watchIdle = true;
  st.watchResetMs = 0;
  ++g_st.watchEpisodes;
  if (g_st.watchLogged < static_cast<unsigned>(g_opt.minionWatchLogLimit)) {
    ++g_st.watchLogged;
    LogIdleMinion(o, st, self, laneDist, lane, static_cast<double>(nowMs - st.watchSinceMs) / 1000.0,
                  static_cast<double>(nowMs) / 1000.0);
  }
  // MINJUNGLE's unstick: a minion that stands away from its lane with nothing to fight gets its default behavior
  // restarted (request 0x400, nAgent.ResetDefaultBehavior's): nBehavior_Assault starts over from the nearest knot and
  // plans its walk anew
  if (g_opt.minionUnstick && !engaged && SehResetDefaultBehavior(o)) {
    st.watchResetMs = nowMs;
    ++g_st.watchResets;
  }
}

}  // namespace

bool Install(const Options& options) {
  g_opt = options;
  if (g_installed) return true;
  if (!CodeMatches(0x008A9060, kAgentTickBytes, sizeof kAgentTickBytes)) {
    Log("agents: AgentBehaviorTree_Tick at 008A9060 has other bytes; agent AI off");
    return false;
  }
  struct Check {
    std::uintptr_t va;
    const unsigned char* bytes;
    std::size_t n;
    const char* name;
  };
  const Check checks[] = {
      {0x008AA4A0, kPerceptionCircleBytes, sizeof kPerceptionCircleBytes, "AgentBlackboard_GetPerceptionCircle"},
      {0x008820B0, kGetInRadiusBytes, sizeof kGetInRadiusBytes, "GameObjects_GetInRadius"},
      {0x008AB710, kAddToAggroListBytes, sizeof kAddToAggroListBytes, "Agent_AddToAggroList"},
      {0x008AA270, kGetBestTargetBytes, sizeof kGetBestTargetBytes, "AgentBlackboard_GetBestTarget"},
      {0x008AB040, kIsInTetherBytes, sizeof kIsInTetherBytes, "AgentBlackboard_IsInTether"},
      {0x008AB860, kCallForHelpBytes, sizeof kCallForHelpBytes, "Agent_CallForHelp"},
      {0x008E3390, kFootprintRadiusBytes, sizeof kFootprintRadiusBytes, "GameObject_GetFootprintRadius"},
      {0x009064E0, kFireEventBytes, sizeof kFireEventBytes, "AbilityDataContainer_FireEvent"},
  };
  g_blackboardOk = true;
  for (const Check& c : checks)
    if (!CodeMatches(c.va, c.bytes, c.n)) {
      Log("agents: %s at %08X has other bytes; blackboard update off", c.name, static_cast<unsigned>(c.va));
      g_blackboardOk = false;
    }
  g_brainOk = CodeMatches(0x008AE9B0, kConditionEvaluateBytes, sizeof kConditionEvaluateBytes);
  if (!g_brainOk) Log("agents: AIConditions_Evaluate at 008AE9B0 has other bytes; brain step off");
  g_installed = true;
  Log("agents: installed (D1b-live): tree tick %d, blackboard update %d%s, brain step %d%s, log limit %d; bot brain "
      "(D6a) %d, think every %d ms; node timers kept across visits (D6c) %d, canInterrupt respected (D6c) %d; a bot "
      "walks to a point out of range (D6d) %d; the objective type adds on sight (D14) %d; NPC gambits on their "
      "condition's target %d, lazy conditions %d, out-of-range fall-through %d (NPCTARGET)",
      g_opt.tickTrees, g_opt.blackboard, g_blackboardOk ? "" : " (prologues differ: off)", g_opt.brain,
      g_brainOk ? "" : " (prologue differs: off)", g_opt.logLimit, g_opt.botBrain, g_opt.botThinkMs,
      g_opt.keepNodeTimers, g_opt.respectCanInterrupt, g_opt.botPursuePoint, g_opt.objectiveOnSight,
      g_opt.npcConditionTarget, g_opt.npcLazyConditions, g_opt.npcRangeFallthrough);
  Log("agents: a lane minion's follow ends when its object is no longer the best target (MINPURSUE) %d; towers: "
      "minions before shapers, a shaper that hits a shaper in range kept until it leaves the range (TOWERAGGRO) %d",
      g_opt.minionFollow.enabled ? 1 : 0, g_opt.towerTarget.enabled ? 1 : 0);
  return true;
}

bool Installed() { return g_installed; }

void BeginFrame() { ++g_frame; }

bool TickTree(void* object, float dt) {
  if (!g_installed || !g_opt.tickTrees || !object) return false;
  std::uint32_t id = 0;
  if (!SehObjectId(object, &id)) return false;
  AgentState& a = StateOf(id);
  if (a.lastFrame == g_frame) return false;  // never tick a tree twice per frame (AGENT_AI.md §5)
  a.lastFrame = g_frame;
  const int r = SehAgentTick(object, dt);
  if (r > 0) ++g_st.ticks;
  else if (r == 0) ++g_st.refused;
  else ++g_st.exceptions;
  return r > 0;
}

void Tick(void* sim, float dt) {
  g_sim = sim;
  if (!g_installed) return;
  const double t0 = PreciseMs();
  ++g_st.frames;
  std::vector<void*> objs;
  objs.reserve(512);
  if (!SehCollect(sim, &objs)) return;
  const unsigned long long nowMs = NowMs(sim);
  const double now = static_cast<double>(nowMs) / 1000.0;
  const unsigned before = g_st.ticks;
  const bool blackboard = g_opt.blackboard && g_blackboardOk;
  const bool brain = g_opt.brain && g_brainOk && blackboard;  // the brain reads the best target the update keeps
  g_towerHits.Expire(static_cast<std::int64_t>(nowMs), g_opt.towerTarget.keepHitsMs);  // TOWERAGGRO
  g_leashCalledNow.swap(g_leashForceGroups);  // LEASH: the groups called since the last frame
  g_leashForceGroups.clear();
  g_snapPlayers.clear();
  g_snapTowers.clear();
  g_snapObjectives.clear();
  if (g_opt.botReflex && g_opt.botBrain) {  // BOTREFLEX: the players' creatures and the standing towers, once per frame
    for (void* o : objs) {
      ObjInfo i;
      if (!SehObjInfo(o, &i) || !i.hasHp || i.hp <= 0.f) continue;
      const int npcType = i.player ? -1 : SehNpcType(o);
      const bool tower = npcType == kNpcTower;
      if (!i.player && !tower && npcType != kNpcObjective) continue;
      Snap sn;
      sn.obj = o;
      sn.id = i.id;
      sn.team = i.team;
      std::memcpy(sn.pos, i.pos, sizeof sn.pos);
      sn.hp = i.hp;
      sn.maxHp = SehMaxHp(o);
      if (!(sn.maxHp > 0.f)) sn.maxHp = i.hp;
      (tower ? g_snapTowers : npcType == kNpcObjective ? g_snapObjectives : g_snapPlayers).push_back(sn);
    }
  }
  for (void* o : objs) {
    std::uint32_t id = 0;
    if (!SehLiveId(o, &id)) continue;  // marked for delete during this loop
    AgentState& st = StateOf(id);
    if (st.noun[0] == '?') st.noun = SehNounName(o);  // for the update's and the brain's log lines
    if (g_opt.allVisible) SehMarkVisible(o);
    // LEASH: a leashed NPC in a fight that passed its leash gets its update now
    if (blackboard && st.leashClass > 0) LeashWatch(o, st, nowMs);
    // 5.1 the blackboard update, when due
    if (blackboard && UpdateDue(o, st, nowMs)) {
      st.nextUpdateMs = nowMs + kBlackboardPeriodMs;
      SehPruneInstanceLists(o);
      UpdateBlackboard(sim, o, st, nowMs);
    }
    // 5.2 the brain step (AI agents with an AI definition; D6a: the bots' plans, phases and gambits)
    if (brain) {
      if (st.isBot < 0) st.isBot = SehIsBot(o) ? 1 : 0;
      if (st.isBot) {
        if (g_opt.botBrain) BotBrainStep(sim, o, st, nowMs);
      } else {
        BrainStep(sim, o, st, nowMs);
      }
    }
    // 5.3 the tree tick (a player's creature was ticked by the command intake already)
    if (g_opt.tickTrees) {
      if (st.lastFrame == g_frame) ++g_st.viaCommands;
      else TickTree(o, dt);
    }
    Observe(o, now);
    MinionWatch(o, st, nowMs);
  }
  if (g_opt.minionWatchMs > 0) {  // MINJUNGLE: the most minions idle off their lanes at once
    unsigned idleNow = 0;
    for (const auto& kv : g_agents) idleNow += kv.second.watchIdle ? 1u : 0u;
    if (idleNow > g_st.watchMaxNow) g_st.watchMaxNow = idleNow;
  }
  g_st.agentsLastFrame = static_cast<unsigned>(objs.size());
  g_st.ticksLastFrame = g_st.ticks - before;
  // agents gone since the last frame: deleted (pending delete) or freed
  for (auto it = g_agents.begin(); it != g_agents.end();) {
    if (it->second.seenFrame == g_frame) {
      ++it;
      continue;
    }
    if (it->second.deathAt >= 0) {
      ++g_st.deletedAfterDeath;
      if (LogBudget())
        Log("agents: %08X %s gone %.2f s after its death leaf (t %.2f s)%s", it->first, it->second.noun,
            now - it->second.deathAt, now, it->second.movedLogged ? "" : "; it did not move after its death");
    }
    it = g_agents.erase(it);
  }
  g_st.tickMsLastFrame = PreciseMs() - t0;
  if (g_st.tickMsLastFrame > g_st.tickMsMax) g_st.tickMsMax = g_st.tickMsLastFrame;
}

void OnDamaged(void* victim, void* attacker, unsigned descLo, unsigned descHi, bool lethal) {
  if (!g_installed || !g_opt.blackboard || !g_blackboardOk || !victim || !attacker || victim == attacker) return;
  ObjInfo v, at;
  if (!SehObjInfo(victim, &v) || !SehObjInfo(attacker, &at) || v.team == at.team) return;
  if ((g_opt.botIdleFallbackMs > 0 || g_opt.botRecall) && v.player && g_sim) {  // BOTIDLE/BOTRECALL: who hit a bot last, and when
    auto it = g_agents.find(v.id);
    if (it != g_agents.end() && it->second.isBot == 1) {
      it->second.lastAttacker = at.id;
      it->second.lastHitMs = NowMs(g_sim);
    }
  }
  if (g_opt.botReflex && v.player && g_sim) {  // BOTREFLEX: who hit a bot last, a tower or an enemy shaper
    auto it = g_agents.find(v.id);
    if (it != g_agents.end() && it->second.isBot == 1) {
      if (at.player) {
        it->second.lastShaperAttacker = at.id;
        it->second.lastShaperHitMs = NowMs(g_sim);
      } else if (SehNpcType(attacker) == kNpcTower) {
        it->second.lastTowerId = at.id;
        it->second.lastTowerHitMs = NowMs(g_sim);
      }
    }
  }
  AgentInfo a;
  if (!SehAgentInfo(victim, &a)) return;
  ++g_st.damageHooks;
  // the victim's own list (AGENT_AI.md §6.3, [assumed]): the attacker, stamped with the victim as the source
  // MINTARGET: the lane minions' defend rule reads who hit which shaper last
  if (g_opt.minionTarget && v.player && at.player && g_sim) g_shaperHits.Record(at.id, v.id, static_cast<std::int64_t>(NowMs(g_sim)));
  // TOWERAGGRO: the towers' defend rule reads who hit which shaper, and where that shaper stood
  if (g_opt.towerTarget.enabled && v.player && at.player && g_sim) {
    gms::agents::ShaperHit h;
    h.attacker = at.id;
    h.victim = v.id;
    h.victimTeam = v.team;
    h.pos[0] = v.pos[0];
    h.pos[1] = v.pos[1];
    h.radius = v.radius;
    h.ms = static_cast<std::int64_t>(NowMs(g_sim));
    g_towerHits.Record(h);
    ++g_st.towerHits;
  }
  // LEASH: a leashed attacker that hit its target is fighting, not chasing; a leashed victim beyond its own leash, or
  // hit from beyond its reach from the leash's edge, or by what its group just dropped, takes nobody onto its list and
  // calls nobody (else it turned back toward a target the next update drops again)
  if (g_sim) {
    const std::int64_t now = static_cast<std::int64_t>(NowMs(g_sim));
    const auto ita = g_agents.find(at.id);
    if (ita != g_agents.end() && ita->second.leashClass > 0) gms::agents::NoteLeashHit(&ita->second.leash, v.id, now);
    const auto itv = g_agents.find(v.id);
    if (itv != g_agents.end() && itv->second.leashClass > 0 && itv->second.leashOpt.enabled) {
      AgentState& s = itv->second;
      const float own = LeashDist(v.pos, s.leashHome);
      const bool beyondSelf = own > s.leashOpt.ownLimit;
      const bool beyondTarget = gms::agents::LeashTargetBeyond(s.leashOpt, LeashDist(at.pos, s.leashHome) - at.radius);
      const bool ignored = g_leashDrops.Ignored(s.leashGroup, at.id, now);
      if (beyondSelf || beyondTarget || ignored) {
        ++g_st.leashHitsSkipped[s.leashClass - 1];
        if (g_st.fightLogged < static_cast<unsigned>(g_opt.leashLogLimit) && LogBudget())
          Log("leash: %08X %s hit by %08X from %.1f (edge) of its home, %.1f from home itself: not taken (%s)", v.id,
              s.noun, at.id, static_cast<double>(LeashDist(at.pos, s.leashHome) - at.radius),
              static_cast<double>(own), beyondSelf ? "own leash" : beyondTarget ? "beyond the leash" : "group leash");
        return;
      }
    }
  }
  if (!lethal && a.hasBrain && !v.player) SehAdd(victim, at.id);
  // its helpers (Agent_CallForHelp: perception list, team, camp, callForHelpRange, filter, tether)
  __try {
    CallForHelp(victim, attacker, descLo, descHi);
    ++g_st.callsForHelp;
    const auto itg = g_agents.find(v.id);  // LEASH: the members it reached get their update in the next frame
    if (itg != g_agents.end() && itg->second.leashClass > 0 && itg->second.leashOpt.enabled &&
        std::find(g_leashForceGroups.begin(), g_leashForceGroups.end(), itg->second.leashGroup) ==
            g_leashForceGroups.end())
      g_leashForceGroups.push_back(itg->second.leashGroup);
  } __except (SehNote(GetExceptionInformation(), "Agent_CallForHelp")) {
  }
}

namespace {

// Puts `id` on the agent's list with the base-structure mark (a new entry gets it from Agent_AddToAggroList's flag, an
// entry the perception rule added earlier gets it written). -1 exception or refused, 0 marked, 1 newly added.
int SehAddBase(void* agentObj, void* agentPtr, std::uint32_t id) {
  __try {
    bool present = false;
    char* b = Field<char*>(agentPtr, agent::kAggroBegin);
    char* e = Field<char*>(agentPtr, agent::kAggroEnd);
    for (char* p = b; p < e; p += agent::kAggroEntrySize) present = present || Field<std::uint32_t>(p, 0) == id;
    char* entry = static_cast<char*>(AddToAggroList(agentObj, id, nullptr, 0, gms::agents::kEntryFlagBaseStructure));
    if (!entry) return -1;
    Field<unsigned char>(entry, agent::kAggroEntryFlag) = gms::agents::kEntryFlagBaseStructure;
    return present ? 0 : 1;
  } __except (SehNote(GetExceptionInformation(), "Agent_AddToAggroList (base structure)")) {
    return -1;
  }
}

bool SehAttackable(void* o) {
  __try {
    return IsTargetable(o) && IsAttackable(o);
  } __except (SehNote(GetExceptionInformation(), "targetable/attackable")) {
    return false;
  }
}

}  // namespace

int AddBaseStructures(std::uint32_t agentId, const std::uint32_t* ids, int n, std::string* report) {
  if (!g_installed || !g_opt.blackboard || !g_blackboardOk) return -1;
  void* o = SehFind(agentId);
  AgentInfo a;
  if (!o || !SehObjInfo(o, &a.self) || !SehAgentInfo(o, &a)) return -1;
  ++g_st.baseCalls;
  // nearest first (the order rule's last criterion), so the first entry is right before the next blackboard update
  struct Item {
    std::uint32_t id;
    float distance;
  };
  std::vector<Item> items;
  for (int i = 0; i < n; ++i) {
    void* t = SehFind(ids[i]);
    ObjInfo ti;
    if (!t || !SehObjInfo(t, &ti) || ti.team == a.self.team || (ti.hasHp && ti.hp <= 0.f) || !SehAttackable(t)) {
      ++g_st.baseSkipped;
      continue;
    }
    const float dx = ti.pos[0] - a.self.pos[0], dy = ti.pos[1] - a.self.pos[1], dz = ti.pos[2] - a.self.pos[2];
    float dist = std::sqrt(dx * dx + dy * dy + dz * dz) - a.self.radius - ti.radius;
    if (dist < 0.f) dist = 0.f;
    items.push_back({ids[i], dist});
  }
  std::stable_sort(items.begin(), items.end(), [](const Item& x, const Item& y) { return x.distance < y.distance; });
  int marked = 0;
  for (const Item& it : items) {
    const int r = SehAddBase(o, a.agent, it.id);
    if (r < 0) continue;
    ++marked;
    ++g_st.baseMarked;
    if (r > 0) ++g_st.baseAdded;
    if (report) {
      char b[48];
      std::snprintf(b, sizeof b, "%s%08X %.1f%s", report->empty() ? "" : " ", it.id, static_cast<double>(it.distance),
                    r > 0 ? "" : " (was on)");
      *report += b;
    }
  }
  return marked;
}

std::string DescribeBot(std::uint32_t objectId) {
  auto it = g_agents.find(objectId);
  if (it == g_agents.end() || it->second.botPlan == -2) return std::string();
  char b[200];
  int n = std::snprintf(b, sizeof b, "plan %d phase %d leaf %s%s%s", it->second.botPlan, it->second.botPhase,
                        LeafName(it->second.leaf), it->second.pursue ? " pursue" : "",
                        it->second.pointPursuit ? " point" : "");  // D6d: the pursuit goes to a point
  // BOTTEMPO: the mTarget (its noun) and the ability holding the agent, for reading a long match
  if (void* o = SehFind(objectId)) {
    if (n > 0 && n < static_cast<int>(sizeof b)) {
      const std::uint32_t tgt = SehRawTarget(o);
      if (tgt) {
        void* t = SehFind(tgt);
        const int k = std::snprintf(b + n, sizeof b - n, " tgt %s", t ? SehNounName(t) : "gone");
        if (k > 0) n += k;
      }
    }
    unsigned hid = 0;
    const char* hname = nullptr;
    if (n > 0 && n < static_cast<int>(sizeof b) && SehHeldAbility(o, &hid, &hname))
      std::snprintf(b + n, sizeof b - n, " held %s", hname ? hname : "?");
  }
  return b;
}

void FormatStats(char* buf, std::size_t size) {
  int counts[kNumLeaves + 2] = {};
  for (const auto& kv : g_agents) {
    const int i = LeafIndex(kv.second.leaf);
    ++counts[i >= 0 ? i : (kv.second.leaf ? kNumLeaves : kNumLeaves + 1)];
  }
  int len = std::snprintf(
      buf, size,
      "agents %u, tree ticks %u this frame (%u total, refused %u, exceptions %u, players' via the command intake %u), "
      "leaf changes %u, deaths %u, death abilities %u, deleted after death %u; blackboard updates %u (forced %u, "
      "exceptions %u), perceived %u, aggro added %u (refused %u) removed %u (gone/dead %u, invalid %u, untargetable %u, "
      "invisible %u, range %u, tether %u) reordered %u, best-target changes %u, events %u, battle ready %u, damage "
      "hooks %u, calls for help %u, made visible %u; brain steps %u, requests %u (started %u, out of range %u, "
      "refused %u), no target %u, conditions %u, links %u, pursue on %u off %u, brain exceptions %u; step %.2f ms "
      "(max %.2f); ended instance ids pruned: abilities %u, modifiers %u; base structures (D5): calls %u, marked %u "
      "(added %u), skipped %u, kept beyond the drop range %u; bots (D6a): thinks %u, plan changes %u, phase changes %u, "
      "default behavior resets %u, no phase %u, gambit conditions %u, requests %u (started %u, out of range %u, refused "
      "%u, items %u); node timers kept (D6c): NPC %u, bot plans %u, bot phases %u; quick plan exits %u (bounces %u; "
      "from EngageForKill with mTarget < 30: %u, 30-55: %u, > 55: %u, none: %u); held by an uninterruptible ability "
      "(D6c): NPC steps %u, bot thinks %u; points (D6d): requests %u (started %u), pursuits %u (mTarget cleared %u, "
      "Pursue restarted %u), started there %u, dropped %u; leaves:",
      g_st.agentsLastFrame, g_st.ticksLastFrame, g_st.ticks, g_st.refused, g_st.exceptions, g_st.viaCommands,
      g_st.leafChanges, g_st.deaths, g_st.deathAbilities, g_st.deletedAfterDeath, g_st.updates, g_st.forced,
      g_st.bbExceptions, g_st.perceived, g_st.added, g_st.addRefused, g_st.removed, g_st.removedBy[0],
      g_st.removedBy[1], g_st.removedBy[2], g_st.removedBy[3], g_st.removedBy[4], g_st.removedBy[5], g_st.reordered,
      g_st.bestChanges, g_st.events, g_st.battleReadyChanges, g_st.damageHooks, g_st.callsForHelp, g_st.madeVisible,
      g_st.brainSteps, g_st.requests, g_st.started, g_st.outOfRange, g_st.requestRefused, g_st.noTarget, g_st.conditionsRun,
      g_st.linksFired, g_st.pursueOn, g_st.pursueOff, g_st.brainExceptions,
      g_st.tickMsLastFrame, g_st.tickMsMax, g_st.prunedAbilities, g_st.prunedModifiers, g_st.baseCalls,
      g_st.baseMarked, g_st.baseAdded, g_st.baseSkipped, g_st.baseKeptBeyondRange, g_bot.thinks, g_bot.planChanges,
      g_bot.phaseChanges, g_bot.behaviorResets, g_bot.noPhase, g_bot.conditions, g_bot.requests, g_bot.started,
      g_bot.outOfRange, g_bot.refused, g_bot.items, g_st.nodeTimersKept, g_bot.planTimersKept, g_bot.phaseTimersKept,
      g_bot.quickExits, g_bot.bounces, g_bot.quickExitNear, g_bot.quickExitMid, g_bot.quickExitFar,
      g_bot.quickExitNoTarget, g_st.heldThinks, g_bot.heldThinks, g_bot.pointRequests, g_bot.pointStarted,
      g_bot.pointPursuits,
      g_bot.pointTargetsCleared, g_bot.pointLeafRestarts, g_bot.pointReached, g_bot.pointDropped);
  for (int i = 0; i < kNumLeaves + 2 && len > 0 && static_cast<std::size_t>(len) < size; ++i)
    if (counts[i])
      len += std::snprintf(buf + len, size - len, " %s=%d",
                           i < kNumLeaves ? kLeaves[i].name : (i == kNumLeaves ? "?" : "none"), counts[i]);
  if (len > 0 && static_cast<std::size_t>(len) < size)
    std::snprintf(buf + len, size - len,
                  "; bot idle fixes (BOTIDLE): refusals passed to the next gambit %u (then acted %u), pursuits dropped "
                  "as stuck %u, idle fallback tries %u (acted %u); recalls instead of walking home (BOTRECALL) %u, short steps instead of the walk (BOTBACK) %u; "
                  "BOTTEMPO: holds ended on one spot %u, stale targets dropped %u",
                  g_bot.skipped, g_bot.skippedThenActed, g_bot.pursuitStuck, g_bot.fallbackTries, g_bot.fallbackActed,
                  g_bot.recalls, g_bot.backSteps, g_bot.holdStuck, g_bot.staleTargets);
}

bool FormatNpcStats(char* buf, std::size_t size) {
  if (!g_st.brainSteps) return false;
  int len = std::snprintf(buf, size,
                          "NPCTARGET: requests on the condition's target %u, on the best target %u, condition lists %u, "
                          "gambits passed over %u, waits %u, no best target %u, out-of-range fall-throughs %u (then "
                          "acted %u); parasite/Guardian starts:",
                          g_st.npcOwnTargets, g_st.npcBestTargets, g_st.conditionLists, g_st.npcPassedOver,
                          g_st.npcWaiting, g_st.npcNoBest, g_st.npcFallthroughs, g_st.npcFallthroughActed);
  for (const auto& kv : g_npcStarts) {
    if (len <= 0 || static_cast<std::size_t>(len) >= size) break;
    len += std::snprintf(buf + len, size - len, " %s %u", AbilityNameOf(kv.first), kv.second);
  }
  return true;
}

bool FormatLeashStats(char* buf, std::size_t size) {
  if (!g_installed || !size || g_st.fights[0] + g_st.fights[1] + g_st.leashHitsSkipped[0] + g_st.leashHitsSkipped[1] == 0)
    return false;
  int len = 0;
  for (int c = 0; c < 2 && len >= 0 && static_cast<std::size_t>(len) < size; ++c) {
    const unsigned n = g_st.fights[c];
    const auto& d = g_st.leashDrops[c];
    len += std::snprintf(buf + len, size - len,
                         "%s%s (rule %s): fights %u (mean %.1f s, longest %.1f s; farthest from home: mean %.1f, max "
                         "%.1f); drops by the own leash %u, the chase time %u, the target leash %u, the group %u; hits "
                         "not taken %u",
                         c ? "; " : "", c ? "workers" : "creeps",
                         (c ? g_opt.workerLeash.enabled : g_opt.creepLeash.enabled) ? "on" : "off", n,
                         n ? g_st.fightSecondsSum[c] / n : 0.0, g_st.fightSecondsMax[c], n ? g_st.fightOwnSum[c] / n : 0.0,
                         g_st.fightOwnMax[c], d[1], d[2], d[3], d[4], g_st.leashHitsSkipped[c]);
  }
  if (len >= 0 && static_cast<std::size_t>(len) < size)
    std::snprintf(buf + len, size - len, "; early updates %u, pursuits ended %u", g_st.leashForced, g_st.leashResets);
  return true;
}

bool FormatMinionStats(char* buf, std::size_t size) {
  if (!g_installed || g_opt.minionWatchMs <= 0 || !size) return false;
  unsigned idleNow = 0;
  for (const auto& kv : g_agents) idleNow += kv.second.watchIdle ? 1u : 0u;
  std::snprintf(buf, size,
                "idle off lane now %u (most at once %u), episodes %u, moved again %u, died idle %u, unstick restarts "
                "%u; MINTARGET shapers "
                "dropped %u (beyond the leash %u), defended %u; chase rule %s: drops by the lane leash %u, by the chase "
                "time %u, by the chase distance %u, ignored on sight %u; chases of shapers %u (mean %.1f s, longest %.1f s; farthest from the "
                "lane: mean %.1f, max %.1f); follow rule %s: follows %u (mean %.1f s, longest %.1f s; walked: mean "
                "%.1f, max %.1f, over 40 units %u; farthest from the lane: mean %.1f, max %.1f), restarts: not the best "
                "target %u, list empty %u, lane leash %u",
                idleNow, g_st.watchMaxNow, g_st.watchEpisodes, g_st.watchRecovered, g_st.watchDiedIdle,
                g_st.watchResets, g_st.minShaperDropped, g_st.minShaperLeash, g_st.minShaperDefend, g_opt.minionChase.enabled ? "on" : "off",
                g_st.chaseDropLane, g_st.chaseDropTime, g_st.chaseDropDistance, g_st.chaseIgnored, g_st.chases,
                g_st.chases ? g_st.chaseSecondsSum / g_st.chases : 0.0, g_st.chaseSecondsMax,
                g_st.chases ? g_st.chaseLaneSum / g_st.chases : 0.0, g_st.chaseLaneMax,
                g_opt.minionFollow.enabled ? "on" : "off", g_st.follows,
                g_st.follows ? g_st.followSecondsSum / g_st.follows : 0.0, g_st.followSecondsMax,
                g_st.follows ? g_st.followWalkSum / g_st.follows : 0.0, g_st.followWalkMax, g_st.followsOver40,
                g_st.follows ? g_st.followLaneSum / g_st.follows : 0.0, g_st.followLaneMax,
                g_st.followResets[gms::agents::kFollowNotBest], g_st.followResets[gms::agents::kFollowListEmpty],
                g_st.followResets[gms::agents::kFollowLaneLeash]);
  return true;
}

bool FormatTowerStats(char* buf, std::size_t size) {
  if (!g_installed || !size || !g_st.towerUpdates) return false;
  std::snprintf(buf, size,
                "rule %s: updates %u; shaper-on-shaper hits %u; defend locks %u, released: left the range %u, left "
                "the list %u; a shaper as the best target with a non-player on the list: defended or taunting %u, "
                "other %u",
                g_opt.towerTarget.enabled ? "on" : "off", g_st.towerUpdates, g_st.towerHits, g_st.towerLocks,
                g_st.towerReleasedRange, g_st.towerReleasedList, g_st.towerShaperDefended, g_st.towerShaperOther);
  return true;
}

}  // namespace agents
