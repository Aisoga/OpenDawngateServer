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
#include <utility>
#include <vector>

#include "core/agents/aggro.h"
#include "core/agents/brain.h"
#include "core/agents/campfinish.h"
#include "core/agents/desecrator.h"
#include "core/agents/killfinish.h"
#include "core/agents/leash.h"
#include "core/agents/lost.h"
#include "core/agents/mikella.h"
#include "core/agents/minion_target.h"
#include "core/agents/nullpos.h"
#include "core/agents/recall.h"
#include "core/agents/reflex.h"
#include "core/agents/tower_target.h"
#include "core/agents/towersafety.h"
#include "core/agents/welldef.h"
#include "core/bots/kits.h"
#include "core/bots/lanes.h"
#include "core/bots/route.h"
#include "bots.h"
#include "director.h"
#include "game.h"
#include "hunterroute.h"
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
  // WELLDEF: the well it defends (gms/core/agents/welldef.h) and its own walk there: the instance of
  // ability_Bot_MoveToTarget it started (adopted on the think after the request), so another walk of the same ability
  // (the plan's, e.g. the Hunter's MoveToNextCamp) is told apart and ended
  gms::agents::WellDefState wellDef;
  unsigned wellWalkInst = 0;
  unsigned long long wellWalkAskedMs = 0;
  // BOTTOWER (gms/core/agents/towersafety.h): the last enemy tower that targeted the bot within range (the frame
  // snapshot), its retreat step's own instance (REFLEXWALK, as the well walk's above), the measurement of its time under
  // tower fire, and the path its brain took at this think
  std::uint32_t lastTargetedTowerId = 0;
  unsigned long long lastTargetedTowerMs = 0;
  unsigned backWalkInst = 0;
  unsigned long long backWalkAskedMs = 0;
  gms::agents::TowerExposureTrack towerExp;
  unsigned long long towerExpFrameMs = 0;
  int towerThink = -1;
  // HUNTERBOT: the shaper's kit rules (gms/core/bots/kits.h; looked up once from the noun) and the finisher's next try
  bool kitRead = false;
  const gms::bots::ShaperKit* kit = nullptr;
  unsigned long long finisherNextMs = 0;
  int finisherResult = 0x7FFFFFFF;
  unsigned long long kitENextMs = 0;
  int kitEResult = 0x7FFFFFFF;
  // CAMPFINISH (gms/core/agents/campfinish.h): the Hunter's camp fight, sampled at its thinks in the Farm plan; the hold
  // of the Flee link (since when, on which camp, the HP then; 0 = none)
  gms::agents::CampFightTrack campTrack;
  ::director::CampFight campFight;  // the camp fight at the last sample (alive 0: none)
  float campFullHp = 0.f;         // the most max HP its living members had together in this fight
  unsigned long long campHoldSinceMs = 0;
  int campHoldCamp = -1;
  float campHoldHp = 0.f;
  bool campFled = false;  // the Flee link fired on this fight (its end is no "left otherwise")
  // round 3: he finishes this nearly dead camp first (the Lua's tasks wait, the camp conditions answer its last creature;
  // gms::agents::CampFinishingWanted), since when (0 = not yet in this fight)
  bool campFinishing = false;
  unsigned long long campFinishingSinceMs = 0;
  // DESECBOT-3: the Desecrator's kit layer (gms/core/agents/desecrator.h): its memory (the E's target, the corpses),
  // his spell slots (7-9; -1 none) as of the last think
  gms::agents::DesecMemory desec;
  int desecDrainSlot = -1, desecWitherSlot = -1;
  int desecResult = 0x7FFFFFFF;  // the layer's last request result (logged on change)
  // MIKELLABOT: Mikella's kit layer (gms/core/agents/mikella.h): its retry times and the R's channel hold
  gms::agents::MikMemory mik;
  int mikResult = 0x7FFFFFFF;  // the layer's last request result (logged on change)
  // KILLFINISH (gms/core/agents/killfinish.h): this think's decision in an EngageForKill plan (kfThinkMs: the think it
  // is for; 0 none), its sensors and target, the first hold of this plan visit, the open episode (1 held, 2 let go on
  // a low target, 3 held then let go: its target and start) and the last reason the Lua's BailOnKill gave
  gms::agents::KillFinishDecision kf;
  gms::agents::KillFinishSensors kfSensors;
  std::uint32_t kfTarget = 0;
  unsigned long long kfThinkMs = 0, kfHoldSinceMs = 0, kfExitLoggedVisit = ~0ull;
  int kfEpisode = 0;
  std::uint32_t kfEpisodeTarget = 0;
  unsigned long long kfEpisodeMs = 0;
  std::string bailWhy;
  unsigned long long bailWhyMs = 0;
  // BOTCYCLE: the EngageForKill plan was entered by a KillShaper join (a mission link from a lane plan); a join that
  // failed at once (and every engage left at once) blocks the next engages until kfEngageBlockUntilMs
  bool kfEnteredByJoin = false;
  unsigned long long kfEngageBlockUntilMs = 0;
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
  // TOWERSTICK: the last target change logged (what it shot -> the new best target), so a pair counts once
  std::uint32_t towerChangeFrom = 0, towerChangeTo = 0;
  // BACKTOACTION (gms/core/agents/lost.h): the Pursue leaf after a point pursuit, the net's windows; the centre stand's
  // start for the stats (0 = not on the centre without a target)
  gms::agents::LostState lost;
  unsigned long long centreStandSinceMs = 0, centreStandLastMs = 0;
  bool centreStandParasite = false;  // the stand had a think in a parasite plan or phase (its gather, contest, kill)
  // NULLPOS (gms/core/agents/nullpos.h): when the last point pursuit ended and its point (for the lines)
  unsigned long long pointEndedMs = 0;
  float pointEnded[2] = {};
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
  // TOWERSTICK: a tower's target changes (from what it shot to the new best target): the old one gone, the old one
  // still on the list (for a shaper: defend or taunt), a living non-player left for another non-player (by the rule
  // that decided), the change lines logged
  unsigned towerChanges = 0, towerOldGone = 0, towerOffLiving = 0, towerOffForShaper = 0, towerStickBroken = 0,
           towerBrokenByRule[gms::agents::kAggroRuleCount] = {}, towerChangeLogged = 0, towerWatchedUpdates = 0;
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

// A tower the rule is for: aggro type tower (Bindings, the Locus; nubs have aggro range 0), not the Guardian.
// TOWERSTICK watches their target changes whatever [host] tower_target says.
bool TowerWatched(void* o, AgentState& st, const AgentInfo& a) {
  if (a.aggroType != gms::agents::kAggroTower || a.aggro <= 0.f) return false;
  if (st.towerRule < 0) {
    st.towerRule = SehNpcType(o) == kTowerRuleSkipNpcType ? 0 : 1;
    if (st.towerRule && g_opt.towerTarget.enabled && TowerLogBudget())
      Log("towers: %08X %s: minions before shapers, a shaper that hits a shaper of team %d within %.1f stays the "
          "target until it leaves %.1f (TOWERAGGRO); %s",
          a.self.id, st.noun, a.self.team, static_cast<double>(a.aggro), static_cast<double>(a.aggro),
          g_opt.towerTarget.stick ? "its current target first among the non-players (TOWERSTICK)"
                                  : "the old order within a class (tower_stick=0)");
  }
  return st.towerRule == 1;
}

// ... and [host] tower_target on.
bool TowerRuleApplies(void* o, AgentState& st, const AgentInfo& a) {
  return g_opt.towerTarget.enabled && TowerWatched(o, st, a);
}

const char* AgentNoun(std::uint32_t id) {
  const auto it = g_agents.find(id);
  return it != g_agents.end() ? it->second.noun : "?";
}

// TOWERSTICK: a watched tower's target change at this update, from what it shot (`shot`, its mTarget) to `best`: the
// rule that decided (the old one on the list) or why the old one left it, both targets' HP and edge distance.
// `hpOf` holds the HP of every candidate, `removed` the entries this update pruned with their reason.
void TowerChangeCheck(AgentState& st, const AgentInfo& a,
                      const std::vector<gms::agents::AggroCandidate>& cands, std::uint32_t shot, std::uint32_t best,
                      std::uint32_t bestBefore, const std::vector<std::pair<std::uint32_t, float>>& hpOf,
                      const std::vector<std::pair<std::uint32_t, const char*>>& removed, bool stickFirst,
                      bool strict, unsigned long long nowMs) {
  ++g_st.towerWatchedUpdates;
  const gms::agents::AggroCandidate* from = nullptr;
  const gms::agents::AggroCandidate* to = nullptr;
  for (const auto& c : cands) {
    if (c.id == shot) from = &c;
    if (c.id == best) to = &c;
  }
  const gms::agents::TowerChange change = gms::agents::ClassifyTowerChange(shot, best, from != nullptr);
  if (change == gms::agents::kTowerKeep) return;
  if (st.towerChangeFrom == shot && st.towerChangeTo == best) return;  // counted at an earlier update
  st.towerChangeFrom = shot;
  st.towerChangeTo = best;
  ++g_st.towerChanges;
  auto hp = [&hpOf](std::uint32_t id) {
    for (const auto& h : hpOf)
      if (h.first == id) return h.second;
    return -1.f;
  };
  // the old target: on the list (its candidate), or looked up (gone, dead, out of range)
  float fromHp = -1.f, fromDist = -1.f;
  bool fromPlayer = false;
  const char* why = "not on the list";
  if (from) {
    fromHp = hp(shot);
    fromDist = from->distance;
    fromPlayer = from->isPlayer;
  } else {
    for (const auto& r : removed)
      if (r.first == shot) why = r.second;
    ObjInfo oi;
    void* t = SehFind(shot);
    if (t && SehObjInfo(t, &oi)) {
      fromHp = oi.hp;
      fromPlayer = oi.player;
      const float dx = oi.pos[0] - a.self.pos[0], dy = oi.pos[1] - a.self.pos[1];
      fromDist = std::sqrt(dx * dx + dy * dy) - a.self.radius - oi.radius;
    } else {
      why = "gone";
    }
  }
  const bool toPlayer = to && to->isPlayer;
  char what[160];
  bool broken = false;
  if (change == gms::agents::kTowerOldGone) {
    ++g_st.towerOldGone;
    std::snprintf(what, sizeof what, "the old one left the list (%s)", why);
  } else {
    ++g_st.towerOffLiving;
    bool newFirst = false;
    const int rule = to ? gms::agents::AggroDecide(*to, *from, gms::agents::NonPlayersFirst(a.aggroType),
                                                   gms::agents::kCallForHelpTimeMs, strict, stickFirst, &newFirst)
                        : -1;
    broken = gms::agents::TowerStickBroken(change, fromPlayer, toPlayer, best);
    if (toPlayer) ++g_st.towerOffForShaper;
    if (broken) {
      ++g_st.towerStickBroken;
      if (rule >= 0) ++g_st.towerBrokenByRule[rule];
    }
    std::snprintf(what, sizeof what, "the old one still on the list, %s%s",
                  to ? gms::agents::AggroRuleName(rule) : "no new target",
                  to && !newFirst ? " (but our order puts the old one first: the client's pick)" : "");
  }
  const bool log = broken ? ++g_st.towerChangeLogged <= 2000u : TowerLogBudget();
  if (!log) return;
  Log("towers: %08X %s target %08X %s (%s, hp %.0f, %.1f units) -> %08X %s (%s, hp %.0f, %.1f units): %s%s (best "
      "before %08X; %u on the list) (t %.2f s)",
      a.self.id, st.noun, shot, AgentNoun(shot), fromPlayer ? "shaper" : "non-player", static_cast<double>(fromHp),
      static_cast<double>(fromDist), best, AgentNoun(best), toPlayer ? "shaper" : "non-player",
      static_cast<double>(hp(best)), static_cast<double>(to ? to->distance : -1.f), what,
      broken ? "; STICK BROKEN: a living non-player left for another" : "", bestBefore,
      static_cast<unsigned>(cands.size()), static_cast<double>(nowMs) / 1000.0);
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
    // TOWERSTICK: a tower's candidates' HP and the entries pruned with their reason, for its target-change log
    const bool towerWatch = !selfDead && TowerWatched(o, st, a);
    std::vector<std::pair<std::uint32_t, float>> hpOf;
    std::vector<std::pair<std::uint32_t, const char*>> removedWhy;
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
        if (towerWatch) removedWhy.emplace_back(e.Id(), kWhy[why - 1]);
        continue;
      }
      if (towerWatch) hpOf.emplace_back(e.Id(), ti.hp);
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
        if (towerWatch) hpOf.emplace_back(p.info.id, p.info.hp);
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
    const bool towerRule = towerWatch && g_opt.towerTarget.enabled;
    if (towerRule) TowerDefendUpdate(st, a, &cands, nowMs);
    // TOWERSTICK: what the tower shoots (its passive's last SetTargetID) is its current target while on the list
    const std::uint32_t towerShot = towerWatch ? SehRawTarget(o) : 0u;
    const bool towerStick = towerRule && g_opt.towerTarget.stick;
    if (towerStick) {
      bool shotOnList = false;
      for (const auto& c : cands) shotOnList = shotOnList || c.id == towerShot;
      const std::uint32_t stickyId = gms::agents::TowerStickyId(true, towerShot, shotOnList, st.bestTarget);
      for (auto& c : cands) c.sticky = c.id == stickyId;
    }
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
      gms::agents::OrderAggro(&cands, a.aggroType, gms::agents::kCallForHelpTimeMs, strict || towerRule, towerStick);
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
    if (towerWatch)
      TowerChangeCheck(st, a, cands, towerShot, best, st.bestTarget, hpOf, removedWhy, towerStick,
                       strict || towerRule, nowMs);
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
void ReportConditionNullPos(std::uint32_t self, std::uint32_t conditionGuid);  // NULLPOS, below
bool ConditionTargetDead(std::uint32_t self, std::uint32_t conditionGuid, std::uint32_t target);  // DEADTARGET, below
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
                       (ownTarget && out.useTarget && r->pass && !r->negate && !*target))) {
      // DEADTARGET at the source (a bot's gambit, `pointOnly` given): a condition that names a dead object (the
      // parasite in its death, a creep killed this frame) gives no target; the request falls back as with none
      if (!(pointOnly && ConditionTargetDead(self, guid, out.target))) *target = out.target;
    }
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
    // NULLPOS: a passing condition that aims its gambit at its point alone (target info, no object) and gives (0, 0):
    // it handed over no point at all (the request then falls back to mTarget or the bot, D6d's rule)
    if (pointOnly && useTargetInfo && out.useTarget && !out.target && r->pass && !r->negate &&
        gms::agents::IsNullPos(out.x, out.y))
      ReportConditionNullPos(self, guid);
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
    case gms::agents::kResultNullPos: return "NULLPOS: the goal is the null position";
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

bool SehTargetPosition(void* o, float* out) {
  __try {
    char* bb = Field<char*>(o, agent::kOfObject);
    if (!bb) return false;
    for (int i = 0; i < 3; ++i) out[i] = Field<float>(bb, agent::kTargetPosition + 4 * i);
    return true;
  } __except (SehNote(GetExceptionInformation(), "read the target position")) {
    return false;
  }
}

// D6d/NULLPOS: "no point" in the blackboard is the engine's own zero vector (the constructor's, what the Pursue leaf's
// scorer compares with); the only write of the null position the host makes.
void ClearTargetPosition(void* o) {
  static const float kZero[3] = {};
  SehSetTargetPosition(o, kZero);
}

// ---- NULLPOS (gms/core/agents/nullpos.h) -------------------------------------------------------------------------------
gms::agents::NullPosCounts g_nullpos;
unsigned g_nullposLines = 0;
std::uint32_t g_forcedFollowId = 0;  // test_nullpos_follow's bot while its zero vector is written (ForcedFollowStep)

void NoteNullPos(const char* who, gms::agents::NullPosWhere kind, const char* where, const char* detail) {
  g_nullpos.Note(kind);
  ++g_nullposLines;
  if (!gms::agents::NullPosLogBudget(g_nullposLines, g_opt.botNullPosStrict)) return;
  char line[512];
  gms::agents::FormatNullPos(line, sizeof line, who, kind, where, detail);
  Log("%s", line);
}

void NoteBotNullPos(std::uint32_t id, const char* noun, gms::agents::NullPosWhere kind, const char* where,
                    const char* detail) {
  char who[96];
  std::snprintf(who, sizeof who, "bot %08X %s", id, noun ? noun : "?");
  NoteNullPos(who, kind, where, detail);
}

void ReportConditionNullPos(std::uint32_t self, std::uint32_t conditionGuid) {
  const auto it = g_agents.find(self);
  const AgentState* st = it != g_agents.end() ? &it->second : nullptr;
  char detail[128];
  std::snprintf(detail, sizeof detail, "condition %08X in phase %d, its target info without an object", conditionGuid,
                st ? st->botPhase : -1);
  NoteBotNullPos(self, st ? st->noun : "?", gms::agents::NullPosWhere::kCondition, "gambit condition", detail);
}

void ReportNullPosImpl(std::uint32_t object, int kind, const char* who, const char* where) {
  const auto w = kind >= 0 && kind < static_cast<int>(gms::agents::NullPosWhere::kCount)
                     ? static_cast<gms::agents::NullPosWhere>(kind)
                     : gms::agents::NullPosWhere::kRequest;
  if (!object) {
    NoteNullPos(who ? who : "?", w, where, "");
    return;
  }
  const auto it = g_agents.find(object);
  const AgentState* st = it != g_agents.end() ? &it->second : nullptr;
  char detail[192] = "";
  if (st && w == gms::agents::NullPosWhere::kFollow) {
    // where the Follow's goal came from: a point pursuit that ended (D6d writes the zero vector back), or not ours
    char how[96] = "no point pursuit of the host's ended for it";
    if (object == g_forcedFollowId)
      std::snprintf(how, sizeof how, "test_nullpos_follow's forced case (the host wrote the zero vector under it)");
    else if (st->pointEndedMs)
      std::snprintf(how, sizeof how, "the point pursuit at (%.1f, %.1f) ended %.1f s ago (bot_point_follow_fix %s)",
                    static_cast<double>(st->pointEnded[0]), static_cast<double>(st->pointEnded[1]),
                    g_sim ? static_cast<double>(NowMs(g_sim) - st->pointEndedMs) / 1000.0 : -1.0,
                    g_opt.botPointFollowFix ? "on" : "off");
    std::snprintf(detail, sizeof detail, "running nBehavior_Follow without an object, plan %d phase %d; %s", st->botPlan,
                  st->botPhase, how);
  }
  if (w == gms::agents::NullPosWhere::kHiddenPursuit) {
    // the recovery: the follow ends (request 0x400), as MINPURSUE and BACKTOACTION end theirs
    void* fo = SehFind(object);
    const char* held = nullptr;
    const bool ok = fo && !SehHeldByUninterruptible(fo, &held) && SehResetDefaultBehavior(fo);
    std::snprintf(detail, sizeof detail, "%s%s", who ? who : "", ok ? "" : " (restart failed or held)");
    char obj[96];  // a minion or creep as well as a bot
    std::snprintf(obj, sizeof obj, "%s %08X %s", st && st->isBot == 1 ? "bot" : "agent", object,
                  st ? st->noun : (fo ? NounName(fo) : "?"));
    NoteNullPos(obj, w, where, detail);
    return;
  }
  NoteBotNullPos(object, st ? st->noun : (who ? who : "?"), w, where, detail);
}

int SehBotRequest(std::uint32_t guid, int slot, std::uint32_t self, std::uint32_t target, const float* pos, int rank) {
  __try {
    int instance = 0;
    return RequestAbilityCall(guid, slot, self, target, pos[0], pos[1], pos[2], rank, 0, 0, 0, 1, nullptr, &instance);
  } __except (SehNote(GetExceptionInformation(), "LabsAbilities_RequestAbility (bot)")) {
    return -0x7FFF;
  }
}

bool SehPendingDelete(void* o) {
  __try {
    return Field<unsigned char>(o, obj::kPendingDelete) != 0;
  } __except (SehNote(GetExceptionInformation(), "pending delete")) {
    return true;
  }
}

// What a request's object is right now (DEADTARGET, NULLPOS): read fresh from the engine.
gms::agents::RequestObject ReadRequestObject(std::uint32_t target) {
  gms::agents::RequestObject ob;
  void* tobj = target ? SehFind(target) : nullptr;
  ObjInfo t;
  if (tobj && SehObjInfo(tobj, &t)) {
    ob.found = true;
    ob.hasHp = t.hasHp;
    ob.hp = t.hp;
    ob.pendingDelete = SehPendingDelete(tobj);
    ob.noun = NounName(tobj);
  }
  return ob;
}

unsigned g_deadTargetLines = 0;
unsigned g_deadConditionLines = 0;

// DEADTARGET at the source: true (counted, the first 100 logged, lower case: not a DEADTARGET line) when a bot's gambit
// condition named an object that is dead, pending delete or gone.
bool ConditionTargetDead(std::uint32_t self, std::uint32_t conditionGuid, std::uint32_t target) {
  const gms::agents::RequestObject ob = ReadRequestObject(target);
  if (!gms::agents::ObjectDead(ob)) return false;
  ++g_nullpos.deadConditions;
  if (++g_deadConditionLines <= 100) {
    const auto it = g_agents.find(self);
    Log("agents: bot %08X %s: deadtarget source: condition %08X named %08X %s (%s); left out (phase %d)", self,
        it != g_agents.end() ? it->second.noun : "?", conditionGuid, target, ob.noun ? ob.noun : "?",
        !ob.found ? "gone" : ob.pendingDelete ? "pending delete" : "hp 0", it != g_agents.end() ? it->second.botPhase : -1);
  }
  return true;
}

// NULLPOS and DEADTARGET: every request the host makes for a bot goes through here. A request at an object that is
// dead, pending delete or gone is refused (kResultNullPos, a refusal: the list goes on) with a "DEADTARGET:" line; a
// position at (0, 0) in any form is refused with a "NULLPOS:" line. No offset, no exception: nothing living stands on
// (0, 0) (the parasite spawns at (0, -1), director.cpp).
int BotRequest(const char* noun, const char* where, gms::agents::NullPosUse use, std::uint32_t guid, int slot,
               std::uint32_t self, std::uint32_t target, const float* pos, int rank) {
  const float p[3] = {pos ? pos[0] : 0.f, pos ? pos[1] : 0.f, pos ? pos[2] : 0.f};
  const gms::agents::RequestObject ob = target ? ReadRequestObject(target) : gms::agents::RequestObject();
  char detail[192];
  switch (gms::agents::CheckRequest(target, p, use, ob)) {
    case gms::agents::RequestVerdict::kDeadTarget: {
      ++g_nullpos.deadTargets;
      ++g_deadTargetLines;
      if (gms::agents::NullPosLogBudget(g_deadTargetLines, g_opt.botNullPosStrict)) {
        char who[96], line[512];
        std::snprintf(who, sizeof who, "bot %08X %s", self, noun ? noun : "?");
        std::snprintf(detail, sizeof detail, "ability %08X slot %d at (%.1f, %.1f)", guid, slot, static_cast<double>(p[0]),
                      static_cast<double>(p[1]));
        gms::agents::FormatDeadTarget(line, sizeof line, who, where, target, ob, detail);
        Log("%s", line);
      }
      return gms::agents::kResultNullPos;
    }
    case gms::agents::RequestVerdict::kNullPos:
      std::snprintf(detail, sizeof detail, "ability %08X slot %d, no object", guid, slot);
      NoteBotNullPos(self, noun, gms::agents::NullPosWhere::kRequest, where, detail);
      return gms::agents::kResultNullPos;
    case gms::agents::RequestVerdict::kObjectAtOrigin:
      std::snprintf(detail, sizeof detail, "ability %08X slot %d, object %08X %s (hp %.0f) stands exactly on (0, 0)", guid,
                    slot, target, ob.noun ? ob.noun : "?", static_cast<double>(ob.hp));
      NoteBotNullPos(self, noun, gms::agents::NullPosWhere::kObjectAtOrigin, where, detail);
      return gms::agents::kResultNullPos;
    case gms::agents::RequestVerdict::kSelfAtOrigin:
      std::snprintf(detail, sizeof detail, "ability %08X slot %d, the bot stands exactly on (0, 0)", guid, slot);
      NoteBotNullPos(self, noun, gms::agents::NullPosWhere::kSelfAtOrigin, where, detail);
      return gms::agents::kResultNullPos;
    default: break;
  }
  return SehBotRequest(guid, slot, self, target, p, rank);
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
  unsigned passThroughs = 0;  // BOTCYCLE: plans left in the think they were entered (bot_plan_chain; not quick exits)
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
  // WELLDEF: defences started per role (Tactician, Hunter, Predator), how they ended, the plans' walks ended for one
  unsigned wellStarts[3] = {}, wellSafe = 0, wellLowHp = 0, wellOutnumbered = 0, wellSlow = 0, wellTooLong = 0;
  unsigned wellOwnFight = 0;
  unsigned wellWalkTakeovers = 0, wellHolds = 0;
  // HUNTERBOT: gambits left out by a kit (Faris's Q), the finisher's R requests and the ones that started; the idle
  // watchdog's attacks (a Hunter standing in his Farm plan) and the ones that started or pursued
  unsigned kitWSkips = 0;  // DESECBOT: W gambits left out without an enemy shaper near (also counted in kitSkips)
  unsigned kitWStarted = 0;  // DESECBOT: W started by a kit with wNear, and the farthest nearest enemy shaper then
  float kitWFarthest = -1.f;
  unsigned kitSkips = 0, finisherTries = 0, finisherStarted = 0, idleHunterTries = 0, idleHunterActed = 0;
  unsigned kitETries = 0, kitEStarted = 0;  // the kit's E at the target (Faris, Freia)
  // MOYABOT: W gambits and reflex W left out with the target beyond the kit's wReach (also in kitSkips for gambits);
  // W started by a kit with wReach, and the farthest target then
  unsigned kitWFar = 0, kitWReachStarted = 0;
  float kitWReachFarthest = -1.f;
  // CAMPFINISH: the Hunters' Flee links that opened in a camp fight (those with the camp under 25 % of its living
  // members' max HP), the holds and how they ended, the links that fired at once by reason, the deaths in a camp fight
  unsigned campEvents = 0, campEventsLow = 0;  // first openings per fight (a hold started or an immediate leave)
  unsigned campLeaves = 0, campLeavesLow = 0;  // the link fired in a camp fight (at once or after a hold)
  unsigned campHolds = 0, campHoldFinished = 0, campHoldLeft = 0, campHoldDied = 0;
  unsigned campHoldOther = 0;  // a hold that ended otherwise (the camp out of reach, another plan, healed and gone)
  unsigned campLeft[static_cast<int>(gms::agents::CampFinishWhy::kCount)] = {};
  unsigned campWouldStay = 0;  // [host] bot_camp_finish=0: links the rule would have held
  unsigned campDeaths = 0;     // Hunters that died in a camp fight (a sample within the last 3 s), once per death
  unsigned campFinishFights = 0, campFinishDone = 0;  // round 3: fights he was finishing, those that ended cleared
  unsigned campOther = 0, campOtherLow = 0;  // fights left otherwise (a task, a plan, a reflex, another camp), the camp
                                             // still alive and hurt; those with the camp under 25 %
  unsigned campLogged = 0;
  // BACKTOACTION (gms/core/agents/lost.h): Pursue leaves restarted after their point pursuit ended (the root fix) and
  // without anything to follow (the net), bots lost on the centre (walks to the lane front requested, started), the
  // longest such stand seen and the bot-seconds spent there (every think counted)
  unsigned pointFollowRestarts = 0, noTargetRestarts = 0, lostWalks = 0, lostWalksStarted = 0, lostNoFront = 0;
  double lostCentreSeconds = 0.0, lostLongestS = 0.0, lostLongestParasiteS = 0.0;
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
  st.kfHoldSinceMs = 0;  // KILLFINISH: a hold lasts one visit of the plan
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
  // NULLPOS: a point pursuit at (0, 0) would be the Follow's walk to the map's centre
  if (gms::agents::IsNullPos(p)) {
    char detail[96];
    std::snprintf(detail, sizeof detail, "phase %d (%s) gambit %d", st.botPhase, v.phaseName ? v.phaseName : "?", gambit);
    NoteBotNullPos(self.id, st.noun, gms::agents::NullPosWhere::kPointPursuit, "D6d point pursuit", detail);
    return;
  }
  SehSetTargetPosition(o, p);
  gms::agents::LostNotePointBegun(st.lost);  // its point replaced one an ended pursuit left there
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
// constructor's), so the Pursue leaf's scorer finds no point. BACKTOACTION: a Follow that still runs re-reads it every
// pass and walks to the zero vector, the map's centre; the next think restarts the leaf (gms/core/agents/lost.h).
// NULLPOS: with bot_point_follow_fix on and `deferClear` the point stays in the blackboard until that next think, which
// writes the zero vector back right before the restart (or when the leaf has changed), so no running Follow ever reads
// (0, 0); a path that won't reach BackToActionStep next think (no phase) clears at once and restarts a running Pursue.
void EndPointPursuit(void* o, AgentState& st, const ObjInfo& self, int r, const float* startedAt,
                     unsigned long long nowMs, bool deferClear = true) {
  const bool keep = deferClear && g_opt.botPointFollowFix;
  if (!keep) {
    ClearTargetPosition(o);
    const char* held = nullptr;
    if (!deferClear && g_opt.botPointFollowFix && st.leaf == kLeafPursue && !SehHeldByUninterruptible(o, &held) &&
        SehResetDefaultBehavior(o))
      ++g_bot.pointFollowRestarts;
  }
  st.pointPursuit = false;
  st.pointEndedMs = nowMs;
  st.pointEnded[0] = st.point[0];
  st.pointEnded[1] = st.point[1];
  gms::agents::LostNotePointEnded(st.lost, keep);
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
// A retreat or a walk to a well is made in steps: ability_Bot_MoveToTarget holds the agent until it arrives, and a new
// request of it is refused (-0x270B) while it runs, so a long walk would not be re-aimed. (The brain does think during
// it: the tree's leaf is UseAbility, not AbilityHoldsAgent; PRETUSBOT checked it in the logs.) ability_Bot_Back walks
// to the base: not used.
constexpr float kRetreatStep = 30.f;
constexpr float kWellStep = 25.f;

struct Snap {
  void* obj = nullptr;
  std::uint32_t id = 0;
  int team = 0;
  float pos[3] = {};
  float hp = 0.f, maxHp = 1.f;
  // BOTTOWER, towers only: the tower's mTarget, its aggro range (0: a structure that doesn't shoot) and the lane minions
  // of the other team within that range
  std::uint32_t target = 0;
  float reach = 0.f;
  int minionsIn = 0;
  float HpFrac() const { return maxHp > 0.f ? hp / maxHp : 1.f; }
};
struct MinionSnap {
  int team = 0;
  float pos[2] = {};
};
std::vector<MinionSnap> g_snapMinions;  // BOTTOWER: the lane minions (nNPCType 0-3), once per frame
std::vector<Snap> g_snapPlayers, g_snapTowers, g_snapObjectives;  // objectives: NPC type 8 (the parasite)

// DESECBOT-3: each shaper's motion and recent damage (the frame snapshot) and its last shaper hits (OnDamaged), for the
// Desecrator's kit layer
struct ShaperTrack {
  gms::agents::DesecMotion motion;
  gms::agents::DesecRecentDamage damage;
  std::uint32_t lastAttacker = 0;  // the enemy shaper that hit it last, and when
  unsigned long long lastAttackerMs = 0;
  int lastVictimTeam = 0;  // the team of the shaper it hit last, and when
  unsigned long long lastVictimMs = 0;
  int archetype = -2;  // PlayerClass.CharacterArchetype (-2 not read yet, -1 unknown)
  // KILLFINISH: the damage it dealt lately (OnDamaged), and the shaper whose hit killed it last, and when
  gms::agents::KillFinishDealt dealt;
  std::uint32_t killedBy = 0;
  unsigned long long killedMs = 0;
};
std::unordered_map<std::uint32_t, ShaperTrack> g_tracks;

struct DesecStats {
  unsigned eLed = 0;           // E requests aimed with the lead (started)
  double eLeadSum = 0.0;       // ... the lead distances' sum
  unsigned eStarted = 0;       // every started E at an enemy shaper
  unsigned qHeld = 0;          // Q gambits or reflex Qs left out while an E waited for its root
  unsigned comboTries = 0, comboStarted = 0;  // the combo's Q at the rooted target
  unsigned comboMissed = 0;    // E windows that ended without a root (or without a Q)
  double comboDelaySum = 0.0;  // the started combo Qs' seconds after their E
  unsigned rGambitsOut = 0, witherGambitsOut = 0;  // the plan's R and Wither gambits left out
  unsigned rTries = 0, rStarted[3] = {}, rCluster = 0, rLocked = 0, rSpread = 0;
  unsigned rHeld = 0;          // thinks with corpses left, R ready and no enemy to spread to
  float rMinSpread = -1.f;     // the smallest distance of a 2nd or 3rd corpse from the earlier ones
  unsigned drainTries = 0, drainStarted = 0, witherTries = 0, witherStarted = 0, witherGlad = 0, witherBruiser = 0;
  unsigned peelTries = 0, peelStarted = 0;
  unsigned earlyNoDive = 0;    // tower back-offs a level 1-3 Desecrator took where the dive rule would have let him on
};
DesecStats g_desec;

// MIKELLABOT: Mikella's kit layer's counts ("stats mikella:")
struct MikStats {
  unsigned tries[gms::agents::kMikKinds] = {}, started[gms::agents::kMikKinds] = {};
  unsigned rAssassinate = 0, rPoke = 0;  // started Rs by why
  unsigned eGambitsOut = 0, rGambitsOut = 0, wGambitsOut = 0;  // the plan's (and BOTREFLEX's) left out
  unsigned wStarted = 0;  // every started W of hers (the plan's and the layer's)
  unsigned holds = 0;     // thinks held for the R's channel
};
MikStats g_mik;

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

// BOTTOWER (gms/core/agents/towersafety.h): the counts ("stats bottower:", "stats bottower bots:"), the towers as one bot
// sees them, the keep-out test of a target and the measurement of the bots' time under tower fire.
struct BotTowerStats {
  unsigned backBy[4] = {};  // tower back-offs decided, by danger (1 a hit, 2 the tower targets it, 3 no minions there)
  unsigned stepsStarted = 0, stepsOwn = 0, stepsAdopted = 0, walksEnded = 0, stepsBlind = 0, stepsRefused = 0;
  unsigned otherBacks = 0;  // back-offs of rules 2 and 4 (an attacker, a jungle enemy), by the same step code
  unsigned planRetreats = 0;  // back-offs left to the plan's own retreat that holds the bot
  unsigned keptOutGambits = 0, keptOutFights = 0, keptOutFallbacks = 0, dives = 0;
  unsigned shotsOnBots = 0, shotsInEpisodes = 0, lethalShots = 0;
  gms::agents::TowerExposureTotals totals;
  unsigned logged = 0;
};
BotTowerStats g_tw;
struct BotTowerBot {
  const char* noun = "?";
  long long targetedMs = 0;
  unsigned episodes = 0, shots = 0, died = 0;
};
std::map<std::uint32_t, BotTowerBot> g_twBots;
constexpr long long kTowerEpisodeGapMs = 1500;  // a tower that stopped targeting the bot this long ends the episode
constexpr float kTowerViewReach = 60.f;          // the towers a bot looks at for its own danger

gms::agents::TowerSafetyConfig TowerSafetyCfg() {
  gms::agents::TowerSafetyConfig c;
  c.aware = g_opt.botTowerAware;
  c.keepOut = g_opt.botTowerKeepOut;
  return c;
}

gms::agents::TowerView MakeTowerView(const AgentState& st, std::uint32_t selfId, const float* selfPos, const Snap& t,
                                     unsigned long long nowMs) {
  gms::agents::TowerView v;
  v.dist = Dist2(t.pos, selfPos);
  v.aggro = t.reach;
  v.targetsMe = t.target == selfId;
  if (st.lastTowerId == t.id && st.lastTowerHitMs) v.hitAgeMs = static_cast<long long>(nowMs - st.lastTowerHitMs);
  if (st.lastTargetedTowerId == t.id && st.lastTargetedTowerMs)
    v.targetedAgeMs = static_cast<long long>(nowMs - st.lastTargetedTowerMs);
  v.minionsIn = t.minionsIn;
  return v;
}

// The most urgent tower danger for the bot now (nullptr: none) and its kind.
const Snap* BotTowerDanger(const AgentState& st, const ObjInfo& self, unsigned long long nowMs,
                           gms::agents::TowerDangerKind* kind) {
  const gms::agents::TowerSafetyConfig c = TowerSafetyCfg();
  constexpr int kMax = 8;
  gms::agents::TowerView v[kMax];
  const Snap* sn[kMax] = {};
  int n = 0;
  for (const Snap& t : g_snapTowers) {
    if (t.team == self.team || n >= kMax) continue;
    if (!(t.reach > 0.f) && t.id != st.lastTowerId) continue;  // a structure that doesn't shoot (unless it hit the bot)
    if (Dist2(t.pos, self.pos) > kTowerViewReach) continue;
    v[n] = MakeTowerView(st, self.id, self.pos, t, nowMs);
    sn[n++] = &t;
  }
  const int i = gms::agents::WorstTowerDanger(v, n, c, kind);
  return i >= 0 ? sn[i] : nullptr;
}

// True: the bot leaves the target (an object, else a point) alone because an enemy tower that would shoot it covers it.
bool TowerKeepsOutTarget(const AgentState& st, const ObjInfo& self, float selfHpFrac, std::uint32_t target,
                         const float* point, bool finishing, unsigned long long nowMs) {
  if (!g_opt.botTowerKeepOut || g_snapTowers.empty()) return false;
  gms::agents::KeepOutTarget g;
  float pos[3] = {};
  if (target) {
    if (const Snap* p = FindSnap(g_snapPlayers, target)) {
      if (p->team == self.team) return false;
      std::memcpy(pos, p->pos, sizeof pos);
      g.isShaper = true;
      g.shaperHpFrac = p->HpFrac();
      g.finishing = finishing;
    } else if (const Snap* t = FindSnap(g_snapTowers, target)) {
      std::memcpy(pos, t->pos, sizeof pos);
    } else {
      void* tobj = SehFind(target);
      ObjInfo ti;
      if (!tobj || !SehObjInfo(tobj, &ti)) return false;
      std::memcpy(pos, ti.pos, sizeof pos);
    }
  } else if (point && (point[0] != 0.f || point[1] != 0.f)) {
    std::memcpy(pos, point, sizeof pos);
  } else {
    return false;
  }
  const gms::agents::TowerSafetyConfig c = TowerSafetyCfg();
  for (const Snap& t : g_snapTowers) {
    if (t.team == self.team || !(t.reach > 0.f)) continue;
    g.distToTower = Dist2(t.pos, pos);
    if (g.distToTower > t.reach + c.keepOutMargin) continue;
    g.isThatTower = t.id == target;
    g.towerHpFrac = t.HpFrac();
    if (gms::agents::TowerKeepsOut(MakeTowerView(st, self.id, self.pos, t, nowMs), g, selfHpFrac, c)) return true;
  }
  return false;
}

bool BotTowerLogBudget() {
  ++g_tw.logged;
  return g_tw.logged <= static_cast<unsigned>(g_opt.botTowerLogLimit) || g_tw.logged % 20 == 0;
}

void EndTowerEpisode(std::uint32_t id, const AgentState& st, const gms::agents::TowerEpisode& e,
                     unsigned long long nowMs) {
  g_tw.totals.Add(e);
  BotTowerBot& b = g_twBots[id];
  b.noun = st.noun;
  b.targetedMs += e.targetedMs;
  ++b.episodes;
  b.shots += static_cast<unsigned>(e.shots);
  if (e.died) ++b.died;
  if (!BotTowerLogBudget()) return;
  std::string paths;
  for (int i = 0; i < gms::agents::kTtPathCount; ++i) {
    if (!e.thinks[i]) continue;
    char one[48];
    std::snprintf(one, sizeof one, "%s%s %d", paths.empty() ? "" : ", ", gms::agents::TowerThinkPathName(i), e.thinks[i]);
    paths += one;
  }
  void* tobj = SehFind(e.tower);
  const std::string what = DescribeBot(id);
  Log("bottower: bot %08X %s targeted by %08X %s for %.1f s (episode %.1f s): tower shots %d%s, hp %.0f%% -> %.0f%%, "
      "%.1f -> %.1f units, %s; thinks: %s; now %s (BOTTOWER) (t %.2f s)",
      id, st.noun, e.tower, tobj ? SehNounName(tobj) : "gone", static_cast<double>(e.targetedMs) / 1000.0,
      static_cast<double>(e.DurationMs()) / 1000.0, e.shots, e.lethal ? " (lethal)" : "",
      static_cast<double>(e.hpStart * 100.f), static_cast<double>(e.hpEnd * 100.f), static_cast<double>(e.distStart),
      static_cast<double>(e.distEnd), e.died ? "died" : "left", paths.empty() ? "none" : paths.c_str(),
      what.empty() ? "?" : what.c_str(), static_cast<double>(nowMs) / 1000.0);
}

// Once per frame, after the snapshot: which enemy tower targets each bot within range, and the episodes.
void BotTowerFrame(unsigned long long nowMs) {
  const gms::agents::TowerSafetyConfig c = TowerSafetyCfg();
  for (auto& kv : g_agents) {
    AgentState& st = kv.second;
    if (st.isBot != 1) continue;
    const Snap* p = FindSnap(g_snapPlayers, kv.first);
    std::uint32_t tower = 0;
    float dist = 0.f;
    if (p) {
      for (const Snap& t : g_snapTowers) {
        if (t.team == p->team || t.target != p->id || !(t.reach > 0.f)) continue;
        const float d = Dist2(t.pos, p->pos);
        if (d > t.reach + c.targetedMargin) continue;
        tower = t.id;
        dist = d;
        break;
      }
      if (tower) {
        st.lastTargetedTowerId = tower;
        st.lastTargetedTowerMs = nowMs;
      }
    }
    gms::agents::TowerEpisode e;
    if (st.towerExp.Frame(static_cast<long long>(nowMs), static_cast<long long>(st.towerExpFrameMs), tower,
                          p ? p->HpFrac() : 0.f, dist, p == nullptr, kTowerEpisodeGapMs, &e))
      EndTowerEpisode(kv.first, st, e, nowMs);
    st.towerExpFrameMs = nowMs;
  }
}

// HUNTERBOT: the bot's kit rules (gms/core/bots/kits.h), looked up once from its noun; nullptr = none.
const gms::bots::ShaperKit* KitOf(AgentState& st) {
  if (!st.kitRead && st.noun && st.noun[0] != '?') {
    st.kitRead = true;
    st.kit = gms::bots::KitForNoun(st.noun);
  }
  return st.kit;
}

// Whether the host may request `slot` for this bot ([host] bot_kits=0: every slot, as before).
bool KitAllows(AgentState& st, int slot) { return !g_opt.botKits || gms::bots::KitAllowsSlot(KitOf(st), slot); }

// DESECBOT: the distance to the nearest living enemy shaper the bot sees; -1 none.
float NearestEnemyShaper(void* o, const ObjInfo& self) {
  float best = -1.f;
  for (const Snap& p : g_snapPlayers) {
    if (p.team == self.team || p.id == self.id || p.hp <= 0.f) continue;
    const float d = Dist2(p.pos, self.pos);
    if ((best < 0.f || d < best) && SehVisible(o, p.obj)) best = d;
  }
  return best;
}

// DESECBOT-3: whether the bot's kit layer is on (the Desecrator's, [host] bot_desec_kit).
bool DesecLayer(AgentState& st) {
  if (!g_opt.botDesecKit || !g_opt.botKits) return false;
  const gms::bots::ShaperKit* kit = KitOf(st);
  return kit && kit->desecLayer;
}

// MIKELLABOT: whether the bot's kit layer is Mikella's and on ([host] bot_mikella_kit).
bool MikLayer(AgentState& st) {
  if (!g_opt.botMikellaKit || !g_opt.botKits) return false;
  const gms::bots::ShaperKit* kit = KitOf(st);
  return kit && kit->mikLayer;
}

// KitAllows, and (DESECBOT) a W that is a burst around the bot only with an enemy shaper near (kits.h KitAllowsW);
// (DESECBOT-3) the Desecrator's Q waits while his E waits for its root, his R and Wither are the kit layer's only.
bool KitAllowsNow(void* o, AgentState& st, const ObjInfo& self, int slot, unsigned long long nowMs) {
  if (!KitAllows(st, slot)) return false;
  if (!g_opt.botKits) return true;
  if (DesecLayer(st)) {
    static const gms::agents::DesecConfig kCfg;
    if (slot == gms::agents::kSlotQ &&
        gms::agents::DesecQHeld(st.desec, static_cast<long long>(nowMs), kCfg, false)) {
      ++g_desec.qHeld;
      return false;
    }
    if (slot == gms::agents::kSlotR) {
      ++g_desec.rGambitsOut;
      return false;
    }
    if (slot >= gms::agents::kSlotSpell1 && slot == st.desecWitherSlot) {
      ++g_desec.witherGambitsOut;
      return false;
    }
  }
  if (MikLayer(st)) {  // MIKELLABOT: her E and R are the layer's, her W only near
    if (gms::agents::MikellaAllowsSlot(slot, slot == gms::agents::kSlotQ + 1 ? NearestEnemyShaper(o, self) : -1.f))
      return true;
    ++(slot == gms::agents::kSlotR ? g_mik.rGambitsOut
                                   : slot == gms::agents::kSlotQ + 2 ? g_mik.eGambitsOut : g_mik.wGambitsOut);
    return false;
  }
  const gms::bots::ShaperKit* kit = KitOf(st);
  if (!kit || kit->wNear <= 0.f || slot != gms::agents::kSlotQ + 1) return true;
  if (gms::bots::KitAllowsW(kit, slot, NearestEnemyShaper(o, self))) return true;
  ++g_bot.kitWSkips;
  return false;
}

// MOYABOT: whether a kit with wReach (a W that empowers the next hits for a few seconds) may cast its W at `target` (the
// gambit's, else mTarget) now; a W without a living target within reach is left out ([host] bot_kits=0: always).
bool KitAllowsWNear(AgentState& st, const ObjInfo& self, int slot, std::uint32_t target, float* dist = nullptr) {
  if (dist) *dist = -1.f;
  if (!g_opt.botKits) return true;
  const gms::bots::ShaperKit* kit = KitOf(st);
  if (!kit || kit->wReach <= 0.f || slot != gms::agents::kSlotQ + 1) return true;
  float d = -1.f;
  ObjInfo t;
  void* tobj = target ? SehFind(target) : nullptr;
  if (tobj && SehObjInfo(tobj, &t) && (!t.hasHp || t.hp > 0.f)) d = Distance2D(t.pos, self.pos);
  if (dist) *dist = d;
  if (gms::bots::KitAllowsWAt(kit, slot, d)) return true;
  ++g_bot.kitWFar;
  return false;
}

// MOYABOT: a started W of a kit with wReach, its target's distance logged (the live check: no W beyond reach); `where`
// the gambit's phase or "reflex".
void NoteKitWReach(AgentState& st, const ObjInfo& self, int slot, int res, std::uint32_t target, const char* where,
                   unsigned long long nowMs) {
  if (!(res == 1 || res == 2) || slot != gms::agents::kSlotQ + 1 || !g_opt.botKits) return;
  const gms::bots::ShaperKit* kit = KitOf(st);
  if (!kit || kit->wReach <= 0.f) return;
  float d = -1.f;
  ObjInfo t;
  void* tobj = target ? SehFind(target) : nullptr;
  if (tobj && SehObjInfo(tobj, &t)) d = Distance2D(t.pos, self.pos);
  ++g_bot.kitWReachStarted;
  if (d > g_bot.kitWReachFarthest) g_bot.kitWReachFarthest = d;
  static unsigned logged = 0;  // its own budget: the first 300, then every 10th
  if (++logged <= 300 || logged % 10 == 0)
    Log("agents: bot %08X %s kit W started in %s at %08X (%s), %.1f units away (MOYABOT) (t %.2f s)", self.id, st.noun,
        where ? where : "?", target, tobj ? SehNounName(tobj) : "none", static_cast<double>(d),
        static_cast<double>(nowMs) / 1000.0);
}

// DESECBOT: a started W of a kit with wNear, its nearest enemy shaper logged (the live check's evidence: no W without
// one within reach); `where` the gambit's phase or "reflex".
void NoteKitW(void* o, AgentState& st, const ObjInfo& self, int slot, int res, const char* where,
              unsigned long long nowMs) {
  if (!(res == 1 || res == 2) || slot != gms::agents::kSlotQ + 1) return;
  if (MikLayer(st)) ++g_mik.wStarted;  // MIKELLABOT: the plan's and BOTREFLEX's Ws of hers
  const gms::bots::ShaperKit* kit = KitOf(st);
  if (!kit || kit->wNear <= 0.f) return;
  const float d = NearestEnemyShaper(o, self);
  ++g_bot.kitWStarted;
  if (d > g_bot.kitWFarthest) g_bot.kitWFarthest = d;
  // its own budget (the idle fixes' is spent within minutes in a bot match): the first 500, then every 10th
  static unsigned logged = 0;
  if (++logged <= 500 || logged % 10 == 0)
    Log("agents: bot %08X %s kit W started in %s, nearest enemy shaper %.1f units (DESECBOT) (t %.2f s)", self.id,
        st.noun, where ? where : "?", static_cast<double>(d), static_cast<double>(nowMs) / 1000.0);
}

// ---- DESECBOT-3: the Desecrator's kit layer (gms/core/agents/desecrator.h) ------------------------------------------

constexpr int kSlotE = gms::agents::kSlotQ + 2;

bool DesecLogBudget() {  // its own: the first 600 lines, then every 10th
  static unsigned n = 0;
  return ++n <= 600 || n % 10 == 0;
}

// The Desecrator's modifiers on an object (live instances only): 1 E_Root, 2 Q_Knockup, 4 R_MortarIcon.
unsigned SehDesecFlags(void* o) {
  __try {
    char* c = Field<char*>(o, obj::kAbilityData);
    if (!c) return 0;
    unsigned f = 0;
    for (const std::uint32_t* p = Field<const std::uint32_t*>(c, container::kModifiersBegin);
         p && p != Field<const std::uint32_t*>(c, container::kModifiersEnd); ++p) {
      void* in = GetInstance(*p);
      if (!in || Field<unsigned char>(in, inst::kEndPending)) continue;
      char* sa = Field<char*>(in, inst::kStatic);
      const char* n = sa ? *reinterpret_cast<const char**>(sa) : nullptr;
      if (!n || std::strncmp(n, "modifier_Desecrator_", 20) != 0) continue;
      const char* r = n + 20;
      if (!std::strcmp(r, "E_Root")) f |= 1u;
      else if (!std::strcmp(r, "Q_Knockup")) f |= 2u;
      else if (!std::strcmp(r, "R_MortarIcon")) f |= 4u;
    }
    return f;
  } __except (SehNote(GetExceptionInformation(), "Desecrator modifiers")) {
    return 0;
  }
}

// A shaper's mRole (its server player's), -1 unknown.
int SehShaperRole(void* o) {
  __try {
    if (!g_sim) return -1;
    void* p = GetPlayerByIndex(g_sim, Field<unsigned char>(o, gobj::kPlayerIndex));
    return p ? static_cast<int>(Field<unsigned char>(p, player::kRole)) : -1;
  } __except (SehNote(GetExceptionInformation(), "shaper role")) {
    return -1;
  }
}

// A shaper's PlayerClass.CharacterArchetype (1 bruiser), from its noun's playerClassData; -1 unknown.
int SehShaperArchetype(void* o) {
  __try {
    void* na = Field<void*>(o, gobj::kNoun);
    char* nd = na ? Field<char*>(na, noun::kData) : nullptr;
    void* pa = nd ? Field<void*>(nd, kNounPlayerClass) : nullptr;
    char* pc = pa ? Field<char*>(pa, asset::kData) : nullptr;
    return pc ? Field<int>(pc, playerclass::kArchetype) : -1;
  } __except (SehNote(GetExceptionInformation(), "shaper archetype")) {
    return -1;
  }
}

int SehPlayerLevel(void* player) {
  __try {
    return player ? static_cast<int>(Field<unsigned>(player, player::kLevel)) : -1;
  } __except (SehNote(GetExceptionInformation(), "player level")) {
    return -1;
  }
}

// Whether the item asset's name contains `part` (the spells: "Spell_Drain.Item", "Spell_Wither.Item").
bool SehItemNamed(void* item, const char* part) {
  __try {
    const char* n = item ? Field<const char*>(item, 0) : nullptr;
    return n && std::strstr(n, part) != nullptr;
  } __except (SehNote(GetExceptionInformation(), "item name")) {
    return false;
  }
}

// The E's point at an enemy shaper `t` with the lead (core LeadPoint, its velocity from the frame snapshots); false
// when `t` is no enemy shaper in range (the request keeps its own point).
bool DesecLead(const ObjInfo& self, std::uint32_t t, float* out, float* leadDist) {
  const Snap* s = t ? FindSnap(g_snapPlayers, t) : nullptr;
  if (!s || s->team == self.team || s->hp <= 0.f) return false;
  const auto it = g_tracks.find(t);
  static const float kZero[2] = {};
  static const gms::agents::DesecConfig kCfg;
  float p[2];
  if (!gms::agents::LeadPoint(self.pos, s->pos, it != g_tracks.end() ? it->second.motion.vel : kZero, kCfg, p))
    return false;
  out[0] = p[0];
  out[1] = p[1];
  out[2] = s->pos[2];
  *leadDist = gms::agents::DesecDist(p, s->pos);
  return true;
}

// A request of slot `slot` at `target` ended with `res`: the layer's memory (an E at an enemy shaper opens the combo's
// window, a Q closes it) and the counters.
void DesecNoteRequest(AgentState& st, int selfTeam, int slot, std::uint32_t target, int res, bool led, float leadDist,
                      unsigned long long nowMs) {
  if (!(res == 1 || res == 2)) return;
  if (slot == kSlotE) {
    const Snap* s = target ? FindSnap(g_snapPlayers, target) : nullptr;
    if (s && s->team != selfTeam) {
      gms::agents::DesecNoteE(st.desec, target, static_cast<long long>(nowMs));
      ++g_desec.eStarted;
      if (led) {
        ++g_desec.eLed;
        g_desec.eLeadSum += leadDist;
      }
    }
  } else if (slot == gms::agents::kSlotQ) {
    gms::agents::DesecNoteQ(st.desec);
  }
}

// The bot attacks `tgt`: its Q, W, E (as far as ranked and its kit allows), else its basic attack; 1 started, 5 out of
// range (the Pursue leaf follows mTarget), 0 nothing worked. DESECBOT-3: the Desecrator's E first (led), then Q, W.
int ReflexAttack(void* o, AgentState& st, const ObjInfo& self, const BotPlayerView& pv, std::uint32_t mtarget,
                 const Snap& tgt, unsigned long long nowMs) {
  if (tgt.id != mtarget && SehHostile(self.team, tgt.id)) SehSetTarget(o, tgt.id);
  const bool desec = DesecLayer(st);
  // MOYABOT: the order by the kit (kits.h ReflexSlots): Q, W, E, basic; the Desecrator's E first; Moya's W, Q, basic
  const std::vector<int> slots = gms::bots::ReflexSlots(g_opt.botKits ? KitOf(st) : nullptr, desec);
  for (int slot : slots) {
    const bool ranked = slot >= gms::agents::kSlotQ;
    if (ranked && pv.ranks[slot] <= 0) continue;
    if (!KitAllowsNow(o, st, self, slot, nowMs)) continue;  // HUNTERBOT: Faris's stance Q; DESECBOT: his W only near
    if (!KitAllowsWNear(st, self, slot, tgt.id)) continue;  // MOYABOT: her W only with the target within reach
    float pos[3] = {tgt.pos[0], tgt.pos[1], tgt.pos[2]};
    float lead = 0.f;
    const bool led = desec && slot == kSlotE && DesecLead(self, tgt.id, pos, &lead);
    // a led E goes to its point alone [assumed: a TerrainPoint request with an object aims at the object]
    const int res = BotRequest(st.noun, "reflex attack", gms::agents::NullPosUse::kGoal, 0, slot, self.id,
                               led ? 0u : tgt.id, pos, ranked ? pv.ranks[slot] : 1);
    if (desec) DesecNoteRequest(st, self.team, slot, tgt.id, res, led, lead, nowMs);
    NoteKitW(o, st, self, slot, res, "reflex", nowMs);
    NoteKitWReach(st, self, slot, res, tgt.id, "reflex", nowMs);
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

// ---- KILLFINISH (gms/core/agents/killfinish.h) ------------------------------------------------------------------------
// A bot in a fight plan (an EngageForKill plan: requestMission, missionType KillShaper; or a PlayerCombat plan) stays to
// finish a won trade: its exit links (to Flee by Bot_HealthLow / Bot_VeryOutnumbered, to its lane plan by
// Bot_BailOnKill or PlayerCombat's own), its FleeTowerTargeting phase link, its Back gambit and BOTREFLEX's tower
// back-off are left out while DecideKillFinish says finish. The decision is
// made once per think in such a plan from the frame snapshot (BOTREFLEX's) and the damage trackers, whether [host]
// bot_kill_finish applies it or not (off, the exits are still logged with what it would have done). An exit on a low
// target (held or let go) opens an episode that ends with the target's death (the bot's kill or another's), the bot's
// death, or after kKfEpisodeMs.
constexpr int kMissionKillShaper = 8;               // nMissionType.KillShaper
constexpr long long kKfWindowMs = 4000;             // the bot's damage rate over this
constexpr long long kKfMinSpanMs = 2000;            // ... over at least this span
constexpr float kKfNear = 30.f;                     // shapers counted around the bot
constexpr unsigned long long kKfEpisodeMs = 8000;   // an episode's watch
const gms::agents::KillFinishConfig kKillFinish;

struct KillFinishStats {
  unsigned thinks = 0, finishThinks = 0;   // thinks in an EngageForKill plan with a decision; ... that said finish
  unsigned exits = 0, exitsLow = 0;        // exit links whose conditions passed (every think); ... on a low target
  unsigned heldThinks = 0, holds = 0;      // exits held; plan visits with a hold
  unsigned letGo = 0, letGoLow = 0;        // exits taken; ... on a low target
  unsigned letGoWhy[gms::agents::kKfWhyCount] = {};  // why the low ones were let go (kKfFinish: the option was off)
  unsigned heldKill = 0, heldKillOther = 0, heldBotDied = 0, heldSurvived = 0, heldThenLetGo = 0;  // held episodes
  unsigned lowKill = 0, lowKillOther = 0, lowBotDied = 0, lowSurvived = 0;  // let-go-low episodes
  unsigned reflexDives = 0, backSkipped = 0, towerPhaseSkipped = 0, retargets = 0;
  unsigned luaQueries = 0, luaHolds = 0;   // the Lua's questions (KillShaper joins, EngageForKill) and the yeses
  // BOTCYCLE: KillShaper joins taken, left again within the quick-exit time (each blocks the next), refused while
  // blocked (the Lua asks nBotHost.EngageBlocked); stance links back into the plan just left, held (PlanBounceHeld)
  unsigned joins = 0, joinsFailed = 0, joinsBlocked = 0, dwellHeld = 0;
  unsigned engagesFailed = 0;  // every engage left within the quick-exit time (each blocks the next ones)
  double ttkSum = 0.0;                     // the holds' time to kill at their first hold
};
KillFinishStats g_kf;

bool KillFinishLogBudget() {  // its own: the first 3000 lines, then every 10th
  static unsigned n = 0;
  return ++n <= 3000 || n % 10 == 0;
}

bool IsEngagePlan(const BotView& v) { return v.planOk && v.requestMission && v.missionType == kMissionKillShaper; }
// A fight with shapers: an EngageForKill plan, or a PlayerCombat plan (entered on RecentPlayerDamage; it leaves to the
// lane plan on Bot_HealthLow / Bot_VeryOutnumbered as well)
bool IsFightPlan(const BotView& v) {
  return IsEngagePlan(v) || (v.planOk && v.planName && std::strstr(v.planName, "PlayerCombat"));
}
bool IsFightOrFleePlan(const BotView& v) {
  return IsFightPlan(v) || (v.planOk && v.planName && std::strstr(v.planName, "Flee"));
}
// BOTCYCLE (round 3): a plan that takes the bot away from danger: Flee, the Gladiators' cautious stances
// (Creature_Gladiator_Lane_Cautious, HighHuntress_Gladiator_Main_MatrixOfLeadership: entered on HealthMedium or
// Outnumbered). A link into one is never held.
bool IsDefensivePlan(const BotView& v) {
  return v.planOk && v.planName &&
         (std::strstr(v.planName, "Flee") || std::strstr(v.planName, "Cautious") ||
          std::strstr(v.planName, "MatrixOfLeadership"));
}

// The sensors for one bot at one think; `target` gets the enemy shaper it would finish (its best target when that is
// one, else the lowest visible one in reach; 0 none).
void KillFinishSense(void* o, const AgentState& st, const ObjInfo& self, std::uint32_t mtarget, unsigned long long nowMs,
                     gms::agents::KillFinishSensors* s, std::uint32_t* target) {
  *s = gms::agents::KillFinishSensors();
  *target = 0;
  const long long now = static_cast<long long>(nowMs);
  s->selfHp = self.hp;
  s->selfMaxHp = SehMaxHp(o);
  if (!(s->selfMaxHp > 0.f)) s->selfMaxHp = self.hp > 0.f ? self.hp : 1.f;
  const Snap* t = nullptr;
  if (const Snap* p = mtarget ? FindSnap(g_snapPlayers, mtarget) : nullptr)
    if (p->team != self.team) t = p;
  if (!t) {
    float best = 2.f;
    for (const Snap& p : g_snapPlayers) {
      if (p.team == self.team || p.id == self.id || Dist2(p.pos, self.pos) > kKillFinish.reach) continue;
      if (p.HpFrac() < best && SehVisible(o, p.obj)) {
        best = p.HpFrac();
        t = &p;
      }
    }
  }
  if (t) {
    *target = t->id;
    s->target = true;
    s->targetVisible = SehVisible(o, t->obj);
    s->targetHp = t->hp;
    s->targetMaxHp = t->maxHp;
    s->dist = Dist2(t->pos, self.pos);
  }
  const auto it = g_tracks.find(self.id);
  if (it != g_tracks.end()) {
    it->second.dealt.Rates(now, kKfWindowMs, kKfMinSpanMs, &s->dpsShapers, &s->dpsAll);
    s->takenDps = it->second.damage.Fraction(s->selfMaxHp, now, 3000) * s->selfMaxHp / 3.f;
  }
  const float towerReach = gms::agents::ReflexConfig().towerRange;
  for (const Snap& tw : g_snapTowers)
    if (tw.team != self.team && (Dist2(tw.pos, self.pos) <= towerReach || (t && Dist2(tw.pos, t->pos) <= towerReach)))
      s->underTower = true;
  s->towerHitting = st.lastTowerHitMs && nowMs - st.lastTowerHitMs <= 3000;
  for (const Snap& p : g_snapPlayers) {
    if (p.id == self.id || (t && p.id == t->id) || Dist2(p.pos, self.pos) > kKfNear) continue;
    if (p.team == self.team) ++s->alliesNear;
    else if (SehVisible(o, p.obj)) ++s->enemiesNear;
  }
  s->heldMs = st.kfHoldSinceMs ? static_cast<long long>(nowMs - st.kfHoldSinceMs) : -1;
}

// This think's decision for a bot in an EngageForKill plan (none in another plan, or without the snapshot).
void KillFinishThink(void* o, AgentState& st, const ObjInfo& self, const BotView& v, unsigned long long nowMs) {
  st.kfThinkMs = 0;
  if (!g_opt.botReflex || !IsFightPlan(v)) return;
  AgentInfo ai;
  const std::uint32_t mtarget = SehAgentInfo(o, &ai) ? SehBestTarget(ai.agent) : 0;
  KillFinishSense(o, st, self, mtarget, nowMs, &st.kfSensors, &st.kfTarget);
  st.kf = gms::agents::DecideKillFinish(st.kfSensors, kKillFinish);
  st.kfThinkMs = nowMs;
  ++g_kf.thinks;
  if (st.kf.finish) ++g_kf.finishThinks;
}

// Whether KILLFINISH keeps the bot in its fight this think (decided this think, and the option applies it).
bool KillFinishing(const AgentState& st, unsigned long long nowMs) {
  return g_opt.botKillFinish && st.kfThinkMs == nowMs && st.kf.finish;
}

bool KillFinishLow(const gms::agents::KillFinishSensors& s) {
  return s.target && s.targetMaxHp > 0.f && s.targetHp / s.targetMaxHp <= kKillFinish.targetMaxFrac;
}

// A hold: the first of this visit starts its clock and the episode; the bot's target becomes the one it finishes.
void KillFinishHold(void* o, AgentState& st, const ObjInfo& self, unsigned long long nowMs) {
  if (!st.kfHoldSinceMs) {
    st.kfHoldSinceMs = nowMs;
    ++g_kf.holds;
    g_kf.ttkSum += st.kf.ttk;
    if (st.kfEpisode == 0 || st.kfEpisodeTarget != st.kfTarget) {
      st.kfEpisode = 1;
      st.kfEpisodeTarget = st.kfTarget;
      st.kfEpisodeMs = nowMs;
    }
  }
  AgentInfo ai;
  if (st.kfTarget && SehAgentInfo(o, &ai) && SehBestTarget(ai.agent) != st.kfTarget &&
      SehHostile(self.team, st.kfTarget)) {
    SehSetTarget(o, st.kfTarget);
    ++g_kf.retargets;
  }
}

// An exit link of an EngageForKill plan passed its conditions: true = held (the bot stays). Counted, logged, and the
// episode opened or marked.
bool KillFinishExit(void* o, AgentState& st, const ObjInfo& self, const BotView& v, const BotView& next,
                    std::size_t link, unsigned long long nowMs) {
  if (st.kfThinkMs != nowMs) return false;  // no decision this think
  const gms::agents::KillFinishSensors& s = st.kfSensors;
  const bool low = KillFinishLow(s);
  ++g_kf.exits;
  if (low) ++g_kf.exitsLow;
  const bool hold = KillFinishing(st, nowMs);
  const bool first = hold && st.kfExitLoggedVisit != st.planEnteredMs;
  if (first) st.kfExitLoggedVisit = st.planEnteredMs;
  if (hold) {
    ++g_kf.heldThinks;
    KillFinishHold(o, st, self, nowMs);
  } else {
    ++g_kf.letGo;
    if (low) {
      ++g_kf.letGoLow;
      ++g_kf.letGoWhy[st.kf.why >= 0 && st.kf.why < gms::agents::kKfWhyCount ? st.kf.why : 0];
    }
    if (st.kfEpisode == 1) {
      st.kfEpisode = 3;
      ++g_kf.heldThenLetGo;
    } else if (st.kfEpisode == 0 && low) {
      st.kfEpisode = 2;
      st.kfEpisodeTarget = st.kfTarget;
      st.kfEpisodeMs = nowMs;
    }
  }
  // the held exits once per visit (its first), every let-go exit
  if ((hold && !first) || !KillFinishLogBudget()) return hold;
  const bool flee = next.planName && std::strstr(next.planName, "Flee");
  const bool engage = IsEngagePlan(v);
  const char* rule = flee ? "HealthLow or VeryOutnumbered" : engage ? "BailOnKill" : "no recent player damage or HealthLow";
  const bool bailNoted = engage && !flee && !st.bailWhy.empty() && nowMs - st.bailWhyMs <= 300;
  void* tobj = st.kfTarget ? SehFind(st.kfTarget) : nullptr;
  Log("killfinish: bot %08X %s %s: plan %d (%s) link %zu -> %s by %s%s%s%s: hp %.0f/%.0f (%.0f%%), target %08X %s hp "
      "%.0f/%.0f (%.0f%%) at %.1f units, dps %.0f (shapers %.0f, all %.0f), taken %.0f/s, ttk %.2f s, ttd %.2f s, "
      "enemies %d allies %d%s%s -> %s (KILLFINISH) (t %.2f s)",
      self.id, st.noun, hold ? "holds" : !g_opt.botKillFinish && st.kf.finish ? "lets go (would hold: off)" : "lets go",
      st.botPlan, v.planName, link, next.planName, rule,
      bailNoted ? " (" : "", bailNoted ? st.bailWhy.c_str() : "", bailNoted ? ")" : "", static_cast<double>(s.selfHp),
      static_cast<double>(s.selfMaxHp), static_cast<double>(100.f * s.selfHp / s.selfMaxHp), st.kfTarget,
      tobj ? SehNounName(tobj) : "none", static_cast<double>(s.targetHp), static_cast<double>(s.targetMaxHp),
      static_cast<double>(s.targetMaxHp > 0.f ? 100.f * s.targetHp / s.targetMaxHp : 0.f), static_cast<double>(s.dist),
      static_cast<double>(st.kf.dps), static_cast<double>(s.dpsShapers), static_cast<double>(s.dpsAll),
      static_cast<double>(s.takenDps), static_cast<double>(st.kf.ttk),
      static_cast<double>(st.kf.ttd > 1.0e8f ? -1.f : st.kf.ttd), s.enemiesNear, s.alliesNear,
      s.underTower ? ", under a tower" : "", s.towerHitting ? " (hit by it)" : "",
      gms::agents::KillFinishWhyName(st.kf.why), static_cast<double>(nowMs) / 1000.0);
  return hold;
}

// An open episode's end: the target's death (the bot's kill or another's), the bot's death, or the watch's end.
void KillFinishEpisodeStep(AgentState& st, std::uint32_t self, bool selfDead, unsigned long long nowMs) {
  if (!st.kfEpisode || !g_opt.botReflex) return;
  const int kind = st.kfEpisode;
  int end = 0;  // 1 the bot's kill, 2 another's, 3 the bot died, 4 the target lived
  if (selfDead) {
    end = 3;
  } else if (!FindSnap(g_snapPlayers, st.kfEpisodeTarget)) {  // dead (the snapshot keeps living shapers only)
    const auto it = g_tracks.find(st.kfEpisodeTarget);
    end = it != g_tracks.end() && it->second.killedBy == self && it->second.killedMs >= st.kfEpisodeMs ? 1 : 2;
  } else if (nowMs - st.kfEpisodeMs > kKfEpisodeMs) {
    end = 4;
  }
  if (!end) return;
  if (kind == 2) {
    ++(end == 1 ? g_kf.lowKill : end == 2 ? g_kf.lowKillOther : end == 3 ? g_kf.lowBotDied : g_kf.lowSurvived);
  } else {
    ++(end == 1 ? g_kf.heldKill : end == 2 ? g_kf.heldKillOther : end == 3 ? g_kf.heldBotDied : g_kf.heldSurvived);
  }
  if (KillFinishLogBudget())
    Log("killfinish: bot %08X %s %s on %08X: %s after %.1f s (KILLFINISH) (t %.2f s)", self, st.noun,
        kind == 1 ? "held" : kind == 2 ? "let go" : "held, then let go", st.kfEpisodeTarget,
        end == 1 ? "killed it" : end == 2 ? "the target died (another's kill)" : end == 3 ? "the bot died"
                                                                                       : "the target lived",
        static_cast<double>(nowMs - st.kfEpisodeMs) / 1000.0, static_cast<double>(nowMs) / 1000.0);
  st.kfEpisode = 0;
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

// ---- CAMPFINISH (gms/core/agents/campfinish.h) -------------------------------------------------------------------------
// BOT_Artificer_Hunter's Farm plan links to Flee on condition_Bot_HealthLow (below 30 % HP) whatever the camp has left.
// At every think of a Hunter in his Farm plan the host samples his camp fight (the camp with a living member within 30:
// its members' HP, his HP); when the Flee link's conditions pass in a camp fight, the link is held while the Hunter wins
// the trade ([host] bot_camp_finish, 1). The "camp finish:" lines log every opening, hold and end (their own budget).
constexpr unsigned long long kCampDeathWindowMs = 3000;  // a death this soon after a camp-fight sample was in the fight
constexpr float kCampLowFrac = 0.25f;                    // "the camp was nearly dead": under this share of its full HP
constexpr unsigned long long kCampHeldSampleMs = 200;     // the samples while an ability holds the agent (the thinks' pace)

// The nearest visible enemy shaper within `range` of the bot (-1: none), from the frame's snapshot.
float CampEnemyShaper(void* o, const ObjInfo& self, float range) {
  float best = -1.f;
  for (const Snap& p : g_snapPlayers) {
    if (p.team == self.team || p.id == self.id) continue;
    const float d = Dist2(p.pos, self.pos);
    if (d <= range && (best < 0.f || d < best) && SehVisible(o, p.obj)) best = d;
  }
  return best;
}

bool CampLogDue() {
  ++g_bot.campLogged;
  return g_bot.campLogged <= static_cast<unsigned>(g_opt.campFinishLogLimit) || g_bot.campLogged % 10 == 0;
}

float CampFrac(const AgentState& st) {
  return st.campFullHp > 0.f ? st.campFight.hp / st.campFullHp : 1.f;
}

// The hold ends: `how` is "finished" (its camp cleared), "left" (the rule says leave; counted by the caller) or another
// reason (counted as other).
void CampHoldEnd(AgentState& st, std::uint32_t id, float hp, float maxHp, const char* how, unsigned long long nowMs) {
  if (!st.campHoldSinceMs) return;
  const double held = static_cast<double>(nowMs - st.campHoldSinceMs) / 1000.0;
  if (std::strcmp(how, "finished") == 0) ++g_bot.campHoldFinished;
  else if (std::strcmp(how, "died") == 0) ++g_bot.campHoldDied;
  else if (std::strcmp(how, "left") != 0) ++g_bot.campHoldOther;
  if (CampLogDue())
    Log("agents: bot %08X %s camp finish: hold on camp %d ended (%s) after %.1f s; hp %.0f/%.0f (%.0f %%), %.0f at its "
        "start (CAMPFINISH) (t %.2f s)",
        id, st.noun, st.campHoldCamp, how, held, static_cast<double>(hp), static_cast<double>(maxHp),
        maxHp > 0.f ? static_cast<double>(hp / maxHp * 100.f) : 0.0, static_cast<double>(st.campHoldHp),
        static_cast<double>(nowMs) / 1000.0);
  st.campHoldSinceMs = 0;
  st.campHoldCamp = -1;
}

// The fight on camp st.campTrack.Camp() ended without the Flee link, a hold or a death: logged and counted when the
// camp still lives and he had hurt it (a camp he only walked past is no fight). `why` says what took him away.
void CampLeftOtherwise(AgentState& st, const ObjInfo& self, float maxHp, const char* why, unsigned long long nowMs) {
  if (st.campHoldSinceMs || st.campFled || st.campTrack.Camp() < 0 || !(st.campTrack.Dealt() > 0.0)) return;
  const int alive = ::director::CampAlive(st.campTrack.Camp());
  if (alive <= 0) return;  // cleared: finished
  const float frac = CampFrac(st);
  ++g_bot.campOther;
  if (frac < kCampLowFrac) ++g_bot.campOtherLow;
  if (CampLogDue())
    Log("agents: bot %08X %s camp finish: left camp %d %s otherwise (%s%s%s): hp %.0f/%.0f (%.0f %%), camp hp %.0f of "
        "%.0f (%.0f %%, %d alive) (CAMPFINISH) (t %.2f s)",
        self.id, st.noun, st.campTrack.Camp(), st.campFight.name, why, st.reflexKind ? ", reflex " : "",
        st.reflexKind == gms::agents::kReflexBack    ? "back"
        : st.reflexKind == gms::agents::kReflexFight ? "fight"
        : st.reflexKind == gms::agents::kReflexDefend ? "defend"
                                                      : "",
        static_cast<double>(self.hp),
        static_cast<double>(maxHp), maxHp > 0.f ? static_cast<double>(self.hp / maxHp * 100.f) : 0.0,
        static_cast<double>(st.campFight.hp), static_cast<double>(st.campFullHp), static_cast<double>(frac * 100.f),
        st.campFight.alive, static_cast<double>(nowMs) / 1000.0);
}

// A sample of a Hunter's camp fight at a think (false: not a Hunter in his Farm plan, or no camp near). `v` null: an
// ability holds the agent (no think; the plan is the one of the last think).
bool CampFinishSample(void* o, AgentState& st, const ObjInfo& self, float maxHp, const BotView* v,
                      unsigned long long nowMs) {
  const bool farm = (!v || (v->planOk && v->planName && std::strstr(v->planName, "_Farm"))) &&
                    BotRoleOf(self.id) == gms::agents::kReflexRoleHunter;
  const gms::agents::CampFinishConfig cfg;
  ::director::CampFight f;
  if (!farm || !::director::CampFightNear(self.pos[0], self.pos[1], cfg.campRadius, &f)) {
    if (st.campHoldSinceMs) {
      const int alive = ::director::CampAlive(st.campHoldCamp);
      CampHoldEnd(st, self.id, self.hp, maxHp, alive == 0 ? "finished" : !farm ? "another plan" : "the camp out of reach",
                  nowMs);
    } else {
      char why[96];
      if (!farm)
        std::snprintf(why, sizeof why, "plan %s", v && v->planName ? v->planName : "?");
      else
        std::snprintf(why, sizeof why, "out of reach in phase %s", v && v->phaseName ? v->phaseName : "?");
      CampLeftOtherwise(st, self, maxHp, why, nowMs);
    }
    if (st.campFinishingSinceMs && st.campTrack.Camp() >= 0 && ::director::CampAlive(st.campTrack.Camp()) == 0)
      ++g_bot.campFinishDone;
    st.campTrack.Reset();
    st.campFight = ::director::CampFight();
    st.campFullHp = 0.f;
    st.campFled = false;
    st.campFinishing = false;
    st.campFinishingSinceMs = 0;
    return false;
  }
  if (st.campHoldSinceMs && f.camp != st.campHoldCamp)  // the held camp is behind him: cleared or left
    CampHoldEnd(st, self.id, self.hp, maxHp, ::director::CampAlive(st.campHoldCamp) == 0 ? "finished" : "another camp",
                nowMs);
  else if (st.campTrack.Camp() >= 0 && f.camp != st.campTrack.Camp())
    CampLeftOtherwise(st, self, maxHp, "another camp", nowMs);
  if (f.camp != st.campTrack.Camp()) {
    if (st.campFinishingSinceMs && st.campTrack.Camp() >= 0 && ::director::CampAlive(st.campTrack.Camp()) == 0)
      ++g_bot.campFinishDone;
    st.campFled = false;
    st.campFinishing = false;
    st.campFinishingSinceMs = 0;
  }
  gms::agents::CampMemberHp m[::director::CampFight::kMembers];
  for (int i = 0; i < f.count; ++i) {
    m[i].id = f.ids[i];
    m[i].hp = f.hps[i];
  }
  st.campTrack.Note(static_cast<long long>(nowMs), self.hp, f.camp, m, f.count, cfg);
  st.campFullHp = f.fullHp > 0.f ? f.fullHp : f.maxHp;  // the camp's full HP (the director's, across the fights)
  st.campFight = f;
  // round 3: finishing it first? (a fight of his: he hurt the camp; [host] bot_camp_finish)
  bool fin = false;
  if (g_opt.botCampFinish && st.campTrack.Dealt() > 0.0 && maxHp > 0.f) {
    const gms::agents::CampFinishConfig cfg2;
    float dealt = 0.f, taken = 0.f;
    long long span = 0;
    const bool rates = st.campTrack.Rates(cfg2, &dealt, &taken, &span);
    const long long since = st.campFinishingSinceMs ? static_cast<long long>(nowMs - st.campFinishingSinceMs) : 0;
    fin = gms::agents::CampFinishingWanted(f.alive, CampFrac(st), rates, dealt, f.hp, self.hp / maxHp,
                                           CampEnemyShaper(o, self, cfg2.enemyShaperRange) >= 0.f, since);
    if (fin && !st.campFinishingSinceMs) {
      st.campFinishingSinceMs = nowMs;
      ++g_bot.campFinishFights;
      if (CampLogDue())
        Log("agents: bot %08X %s camp finish: finishing camp %d %s first: hp %.0f/%.0f (%.0f %%), camp hp %.0f of %.0f "
            "(%.0f %%, %d alive) (CAMPFINISH) (t %.2f s)",
            self.id, st.noun, f.camp, f.name, static_cast<double>(self.hp), static_cast<double>(maxHp),
            static_cast<double>(self.hp / maxHp * 100.f), static_cast<double>(f.hp), static_cast<double>(st.campFullHp),
            static_cast<double>(CampFrac(st) * 100.f), f.alive, static_cast<double>(nowMs) / 1000.0);
    }
  }
  st.campFinishing = fin;
  return true;
}

// round 3: the Hunter's camp fight for the Lua (nBotHost.CampFight): the nearest living creature's position, the camp's
// share of its full HP and whether he finishes it first. False without a current fight.
bool CampFightOf(std::uint32_t bot, float* pos, float* frac, bool* finishing) {
  auto it = g_agents.find(bot);
  if (it == g_agents.end() || !g_sim) return false;
  const AgentState& st = it->second;
  const long long last = st.campTrack.LastMs();
  if (st.campTrack.Camp() < 0 || st.campFight.alive <= 0 || last < 0 ||
      static_cast<long long>(NowMs(g_sim)) - last > 1000)
    return false;
  std::memcpy(pos, st.campFight.nearestPos, sizeof st.campFight.nearestPos);
  *frac = CampFrac(st);
  *finishing = st.campFinishing;
  return true;
}

// The Farm plan's link to `outputNode` passed its conditions: a Hunter's Flee link in a camp fight is held (true) while
// he wins the trade. Counts and logs each opening.
bool CampFinishHolds(void* o, AgentState& st, const ObjInfo& self, int outputNode, unsigned long long nowMs) {
  if (st.campTrack.Camp() < 0 || st.campFight.alive <= 0 ||
      st.campTrack.LastMs() != static_cast<long long>(nowMs))  // this think's sample: a Hunter in a camp fight
    return false;
  BotView next;
  if (!SehReadBotView(o, outputNode, -1, &next) || !next.planOk || !next.planName || !std::strstr(next.planName, "Flee"))
    return false;
  const gms::agents::CampFinishConfig cfg;
  const float maxHp = SehMaxHp(o);
  gms::agents::CampFinishInputs in;
  in.hp = self.hp;
  in.maxHp = maxHp;
  in.campAlive = st.campFight.alive;
  in.campHp = st.campFight.hp;
  in.campMaxHp = st.campFullHp;
  in.haveRates = st.campTrack.Rates(cfg, &in.dealtPerS, &in.takenPerS, &in.spanMs);
  const float enemyDist = CampEnemyShaper(o, self, cfg.enemyShaperRange);
  in.enemyShaperNear = enemyDist >= 0.f;
  in.heldMs = st.campHoldSinceMs ? static_cast<long long>(nowMs - st.campHoldSinceMs) : 0;
  const gms::agents::CampFinishDecision d = gms::agents::DecideCampFinish(in, cfg);
  const bool hold = d.stay && g_opt.botCampFinish;
  const float frac = CampFrac(st);
  const bool first = !st.campHoldSinceMs;  // a new opening (not a hold's re-check)
  if (first) {
    ++g_bot.campEvents;
    if (frac < kCampLowFrac) ++g_bot.campEventsLow;
  }
  if (!hold) {
    ++g_bot.campLeaves;
    if (frac < kCampLowFrac) ++g_bot.campLeavesLow;
    ++g_bot.campLeft[static_cast<int>(d.why)];
    if (d.stay) ++g_bot.campWouldStay;
  } else if (first) {
    ++g_bot.campHolds;
  }
  if ((first || !hold) && CampLogDue()) {
    char rates[96] = "rates not measured";
    if (in.haveRates)
      std::snprintf(rates, sizeof rates, "camp loses %.0f/s, he %.0f/s over %.1f s", static_cast<double>(in.dealtPerS),
                    static_cast<double>(in.takenPerS), static_cast<double>(in.spanMs) / 1000.0);
    char enemy[40] = "";
    if (in.enemyShaperNear) std::snprintf(enemy, sizeof enemy, ", enemy shaper at %.0f", static_cast<double>(enemyDist));
    Log("agents: bot %08X %s camp finish: %s camp %d %s (%s%s): hp %.0f/%.0f (%.0f %%), camp hp %.0f of %.0f (%.0f %%, %d "
        "alive); %s; kill %.1f s, die %.1f s%s (CAMPFINISH) (t %.2f s)",
        self.id, st.noun,
        hold ? "holds the Flee link on" : st.campHoldSinceMs ? "leaves after a hold" : "leaves", st.campFight.camp,
        st.campFight.name, gms::agents::CampFinishWhyName(d.why), enemy, static_cast<double>(self.hp),
        static_cast<double>(maxHp), maxHp > 0.f ? static_cast<double>(self.hp / maxHp * 100.f) : 0.0,
        static_cast<double>(in.campHp), static_cast<double>(st.campFullHp), static_cast<double>(frac * 100.f),
        in.campAlive, rates, static_cast<double>(d.killS), static_cast<double>(d.dieS),
        d.stay && !g_opt.botCampFinish ? "; bot_camp_finish 0: the rule would hold" : "",
        static_cast<double>(nowMs) / 1000.0);
  }
  if (hold) {
    if (first) {
      st.campHoldSinceMs = nowMs;
      st.campHoldCamp = st.campFight.camp;
      st.campHoldHp = self.hp;
    }
    return true;
  }
  if (st.campHoldSinceMs) {
    ++g_bot.campHoldLeft;
    CampHoldEnd(st, self.id, self.hp, maxHp, "left", nowMs);
  }
  st.campFled = true;  // the Flee link fires: not "left otherwise"
  return false;
}

// A Hunter's death: in a camp fight when its last sample is recent (counted once; the history ends).
void CampFinishDeath(AgentState& st, std::uint32_t id, unsigned long long nowMs) {
  const long long last = st.campTrack.LastMs();
  if (st.campTrack.Camp() >= 0 && last >= 0 && nowMs - static_cast<unsigned long long>(last) <= kCampDeathWindowMs) {
    ++g_bot.campDeaths;
    if (CampLogDue())
      Log("agents: bot %08X %s camp finish: died in a camp fight on camp %d %s (camp hp %.0f of %.0f, %d alive)%s "
          "(CAMPFINISH) (t %.2f s)",
          id, st.noun, st.campFight.camp, st.campFight.name, static_cast<double>(st.campFight.hp),
          static_cast<double>(st.campFullHp), st.campFight.alive, st.campHoldSinceMs ? " while holding" : "",
          static_cast<double>(nowMs) / 1000.0);
  }
  CampHoldEnd(st, id, 0.f, 0.f, "died", nowMs);
  st.campTrack.Reset();
  st.campFight = ::director::CampFight();
  st.campFullHp = 0.f;
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

// WELLDEF (gms/core/agents/welldef.h): the walk ends this far short of a defended well's centre, on the bot's side: inside
// the capture ring (12), where a defender contests a capture [assumed: the dais is walkable there; the workers drop
// their Vim 4 from the centre]. Its own log budget: BOTREFLEX's lines share the idle budget, spent early in a match.
constexpr float kWellStand = 5.f;
unsigned g_wellLogLines = 0;
bool WellLogBudget() {
  ++g_wellLogLines;
  return g_wellLogLines <= 400 || g_wellLogLines % 20 == 0;
}

// WELLDEF: the wells of the bot's team under threat, measured for StepWellDefense (the shapers within its crew range
// of each, the enemies only when the bot's team sees them), and its own fight (its target, an enemy shaper it sees
// within kNearShapers, else the shaper that hit it within the hit window). True when it defends a well; `out` = that well.
bool WellDefenseStep(void* o, AgentState& st, const ObjInfo& self, const gms::agents::ReflexSensors& s,
                     std::uint32_t mtarget, const Snap* attacker, long long hitWindowMs, unsigned long long nowMs,
                     ::wells::Threat* out) {
  const gms::agents::WellDefConfig wc;
  ::wells::Threat th[4];
  const int n = ::wells::Threats(self.team, th, 4);
  gms::agents::WellThreatView views[4];
  for (int i = 0; i < n; ++i) {
    gms::agents::WellThreatView& w = views[i];
    w.well = th[i].well;
    w.pos[0] = th[i].pos[0];
    w.pos[1] = th[i].pos[1];
    w.captureMoving = th[i].moving;
    w.secondsToCapture = th[i].secondsToCapture;
    w.capturers = th[i].capturers;
    w.workerHit = th[i].workerPlayerHit;
    for (const Snap& p : g_snapPlayers) {
      if (p.id == self.id || !(p.hp > 0.f) || Dist2(p.pos, th[i].pos) > wc.crewRange) continue;
      if (p.team == self.team)
        ++w.alliesNear;
      else if (SehVisible(o, p.obj))
        ++w.enemiesNear;
    }
  }
  // the threats WELLDEF counts: enemies in the ring, a moving capture, a worker hit by an enemy shaper
  int m = 0;
  for (int i = 0; i < n; ++i)
    if (views[i].capturers > 0 || views[i].captureMoving || views[i].workerHit) {
      views[m] = views[i];
      th[m] = th[i];
      ++m;
    }
  gms::agents::WellDefInputs in;
  in.role = s.role;
  in.hpFrac = s.hpFrac;
  in.pos[0] = self.pos[0];
  in.pos[1] = self.pos[1];
  const Snap* fight = nullptr;
  if (const Snap* t = mtarget ? FindSnap(g_snapPlayers, mtarget) : nullptr)
    if (t->team != self.team && t->hp > 0.f && Dist2(t->pos, self.pos) <= kNearShapers && SehVisible(o, t->obj))
      fight = t;
  if (!fight && attacker && s.shaperHitAgeMs >= 0 && s.shaperHitAgeMs <= hitWindowMs) fight = attacker;
  if (fight) {
    in.ownFight = true;
    in.fightPos[0] = fight->pos[0];
    in.fightPos[1] = fight->pos[1];
  }
  in.threats = views;
  in.count = m;
  in.nowMs = static_cast<long long>(nowMs);
  const long long since = st.wellDef.sinceMs;
  const gms::agents::WellDefDecision d = gms::agents::StepWellDefense(in, st.wellDef, wc);
  const gms::agents::WellThreatView* v = nullptr;
  for (int i = 0; i < m; ++i)
    if (views[i].well == d.well) v = &views[i];
  if (d.started) {
    const int r = s.role - gms::agents::kWellRoleTactician;
    if (r >= 0 && r < 3) ++g_bot.wellStarts[r];
    st.wellWalkInst = 0;
    st.wellWalkAskedMs = 0;
    if (WellLogBudget())
      Log("agents: bot %08X %s defends well %d at (%.1f, %.1f), %.0f units away: capture %s (%.1f s left), capturers "
          "%d, worker hit %d, enemies %d allies %d there; hp %.0f%%, role %d (WELLDEF) (t %.2f s)",
          self.id, st.noun, d.well, static_cast<double>(d.pos[0]), static_cast<double>(d.pos[1]),
          static_cast<double>(gms::agents::WellDist2D(in.pos, d.pos)), v && v->captureMoving ? "moving" : "still",
          v ? v->secondsToCapture : -1.0, v ? v->capturers : 0, v && v->workerHit ? 1 : 0, v ? v->enemiesNear : 0,
          v ? v->alliesNear : 0, static_cast<double>(s.hpFrac * 100.f), s.role, static_cast<double>(nowMs) / 1000.0);
  } else if (d.ended) {
    const char* e = d.ended;
    const std::string why = e;
    ++(why == "safe" ? g_bot.wellSafe
       : why == "low hp" ? g_bot.wellLowHp
       : why == "outnumbered" ? g_bot.wellOutnumbered
       : why == "own fight" ? g_bot.wellOwnFight
       : why == "too long" ? g_bot.wellTooLong
                           : g_bot.wellSlow);
    if (WellLogBudget())
      Log("agents: bot %08X %s leaves well %d: %s, after %.1f s (arrived %d); hp %.0f%%, role %d (WELLDEF) (t %.2f s)",
          self.id, st.noun, d.well, e, static_cast<double>(in.nowMs - since) / 1000.0, st.wellDef.arrived ? 1 : 0,
          static_cast<double>(s.hpFrac * 100.f), s.role, static_cast<double>(nowMs) / 1000.0);
  }
  if (!d.defend) return false;
  *out = ::wells::Threat();
  out->well = d.well;
  out->pos[0] = d.pos[0];
  out->pos[1] = d.pos[1];
  out->pos[2] = self.pos[2];
  for (int i = 0; i < m; ++i)
    if (th[i].well == d.well) *out = th[i];
  return true;
}

// WELLDEF: the walk to a defended well with nobody to fight there: steps of kWellStep, the last one to the point
// kWellStand short of the centre on the bot's side; standing there it holds (no request: the plan's gambits stay off
// while the defence lasts). A walk of the same ability that isn't its own (the plan's, e.g. the Hunter's
// MoveToNextCamp) answers every request with -0x270B and would carry the bot elsewhere: it is ended, and the request
// repeated on the next think. Returns BOTREFLEX's result codes (1 walking, 2 holding).
int WellDefenseWalk(void* o, AgentState& st, const ObjInfo& self, const ::wells::Threat& w, unsigned long long nowMs) {
  const float dist = Dist2(self.pos, w.pos);
  if (dist <= kWellStand + 2.f) {
    ++g_bot.wellHolds;
    return 2;
  }
  float goal[3];
  if (dist > kWellStep + kWellStand)
    StepPoint(self.pos, w.pos, kWellStep, true, self.team, goal);
  else
    StepPoint(w.pos, self.pos, kWellStand, true, self.team, goal);  // kWellStand from the centre toward the bot
  goal[2] = self.pos[2];
  const int res = BotRequest(st.noun, "well defence walk", gms::agents::NullPosUse::kGoal, kAbilityBotMoveToTarget, -1,
                             self.id, 0, goal, 1);
  if (res == 1 || res == 2) {
    st.wellWalkAskedMs = nowMs;
    st.wellWalkInst = 0;
    return 1;
  }
  if (res != gms::agents::kResultHoldsAgent) return res;
  unsigned hid = 0;
  const char* hname = nullptr;
  if (!SehHeldAbility(o, &hid, &hname) || hid == st.wellWalkInst) return 1;  // its own walk goes on
  if (!st.wellWalkInst && st.wellWalkAskedMs && nowMs - st.wellWalkAskedMs <= 1500) {
    st.wellWalkInst = hid;  // the walk it asked for just now
    return 1;
  }
  if (SehEndAbility(hid)) ++g_bot.wellWalkTakeovers;  // someone else's walk
  st.wellWalkInst = 0;
  st.wellWalkAskedMs = 0;
  return 1;
}

// BOTTOWER (REFLEXWALK, towersafety.h ChooseHeldWalk): a retreat step's request result. A walk of the same ability that
// isn't the bot's own step (the plan's, e.g. the Hunter's MoveToNextCamp, or a gambit's walk) answers -0x270B, which
// BOTREFLEX counted as done, so the step never started: with bot_reflex_walk that walk is ended and the step starts at
// the next think (WELLDEF's well walk does the same). Returns the result BOTREFLEX acts on (1 = the step goes on).
int ReflexStepResult(void* o, AgentState& st, const ObjInfo& self, int res, bool towerBack, unsigned long long nowMs) {
  unsigned hid = 0;
  const char* hname = nullptr;
  const bool known = res == gms::agents::kResultHoldsAgent && SehHeldAbility(o, &hid, &hname);
  const long long since = st.backWalkAskedMs ? static_cast<long long>(nowMs - st.backWalkAskedMs) : -1;
  const gms::agents::HeldWalkAction a = gms::agents::ChooseHeldWalk(res, known, hid, st.backWalkInst, since);
  int path = gms::agents::kTtBackRefused;
  switch (a) {
    case gms::agents::kHeldWalkStarted:
      st.backWalkAskedMs = nowMs;
      st.backWalkInst = 0;
      ++g_tw.stepsStarted;
      path = gms::agents::kTtBackStarted;
      break;
    case gms::agents::kHeldWalkOwn:
    case gms::agents::kHeldWalkAdopt:
      if (a == gms::agents::kHeldWalkAdopt) {
        st.backWalkInst = hid;
        ++g_tw.stepsAdopted;
      } else {
        ++g_tw.stepsOwn;
      }
      res = 1;
      path = gms::agents::kTtBackOwn;
      break;
    case gms::agents::kHeldWalkEnd:
      if (g_opt.botReflexWalk) {
        const bool ended = SehEndAbility(hid);
        if (ended) ++g_tw.walksEnded;
        if (BotTowerLogBudget())
          Log("agents: bot %08X %s: its back-off step was held off by %s (instance %08X): %s (BOTTOWER, REFLEXWALK) "
              "(t %.2f s)",
              self.id, st.noun, hname ? hname : "?", hid, ended ? "ended" : "gone already",
              static_cast<double>(nowMs) / 1000.0);
        st.backWalkInst = 0;
        st.backWalkAskedMs = 0;
        path = gms::agents::kTtBackEnded;
      } else {
        ++g_tw.stepsBlind;  // BOTREFLEX's rule: counted as done
        path = gms::agents::kTtBackBlind;
      }
      res = 1;
      break;
    default:
      ++g_tw.stepsRefused;
      break;
  }
  if (towerBack) st.towerThink = path;
  return res;
}

// Returns true when a reflex acted this think (the plan's gambits are skipped).
bool BotReflexStep(void* o, AgentState& st, const ObjInfo& self, const BotView& v, const BotPlayerView& pv,
                   std::uint32_t mtarget, unsigned long long nowMs) {
  const float maxHp = SehMaxHp(o);
  if (!(maxHp > 0.f) || self.hp <= 0.f) return false;
  gms::agents::ReflexConfig cfg;
  cfg.junglerHpPoints = g_opt.botJungleFight;  // PRETUSBOT
  if (g_opt.botTowerAware) cfg.finishTowerShotHp = gms::agents::TowerSafetyConfig().finishTowerShotHp;  // BOTTOWER
  // DESECBOT-3: a Desecrator at level 1-3 never stays under an enemy tower for a shaper (DESECBOT-1's early deaths)
  constexpr int kDesecEarlyLevel = 3;
  if (DesecLayer(st)) {
    const int level = SehPlayerLevel(pv.player);
    cfg.noShaperDive = level >= 1 && level <= kDesecEarlyLevel;
  }
  gms::agents::ReflexSensors s;
  s.role = BotRoleOf(self.id);
  s.hpFrac = self.hp / maxHp;
  s.hp = self.hp;
  // the tower that hit it last, while it stands and the bot is still within its reach; BOTTOWER (bot_tower_aware): also
  // a tower that targets it, or one with none of its minions in range (towersafety.h TowerDanger)
  gms::agents::TowerDangerKind towerKind = gms::agents::kTowerSafe;
  const Snap* hitTower = BotTowerDanger(st, self, nowMs, &towerKind);
  s.inTowerZone = hitTower != nullptr;
  s.towerKind = towerKind;
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
    s.attackerHp = attacker->hp;
  }
  if (jungle) {
    s.jungleEnemy = true;
    s.jungleEnemyHpFrac = jungle->HpFrac();
    s.jungleEnemyHp = jungle->hp;
    s.jungleEnemyDist = jungleDist;
  }
  // a well of its team on its side of the map under attack
  ::wells::Threat wellPos;
  bool wellThreat = false;
  if (g_opt.botWellDefense) {
    cfg.wellTacticians = true;
    wellThreat = WellDefenseStep(o, st, self, s, mtarget, attacker, cfg.hitWindowMs, nowMs, &wellPos);
  } else if (gms::agents::ReflexIsJungler(s.role)) {  // BOTREFLEX's rule
    ::wells::Threat th[4];
    const int n = ::wells::Threats(self.team, th, 4);
    float best = kWellSideReach;
    for (int i = 0; i < n; ++i) {
      if (!th[i].moving && !th[i].workerHit) continue;  // WELLDEF's contested and paused captures: not BOTREFLEX's
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
  s.finishing = KillFinishing(st, nowMs);  // KILLFINISH: a won trade's finish under the tower is a dive

  const gms::agents::ReflexDecision d = gms::agents::DecideReflex(s, cfg);
  if (s.finishing && s.inTowerZone && d.kind != gms::agents::kReflexBack) {
    gms::agents::ReflexSensors plain = s;
    plain.finishing = false;
    if (gms::agents::DecideReflex(plain, cfg).kind == gms::agents::kReflexBack) {
      ++g_kf.reflexDives;
      KillFinishHold(o, st, self, nowMs);
    }
  }
  if (cfg.noShaperDive && d.kind == gms::agents::kReflexBack && d.why[0] == 't') {
    gms::agents::ReflexConfig dive = cfg;
    dive.noShaperDive = false;
    if (gms::agents::DecideReflex(s, dive).kind != gms::agents::kReflexBack) ++g_desec.earlyNoDive;
  }
  const bool towerBack = d.kind == gms::agents::kReflexBack && d.why[0] == 't';
  if (d.kind == gms::agents::kReflexNone) {
    st.reflexKind = 0;
    st.towerThink = s.inTowerZone ? gms::agents::kTtDive : gms::agents::kTtNotSeen;
    if (s.inTowerZone) ++g_tw.dives;
    return false;
  }
  st.towerThink = towerBack ? gms::agents::kTtBackRefused : gms::agents::kTtOtherReflex;
  int res = 0;
  if (d.kind == gms::agents::kReflexBack) {
    // away from what threatens it: the tower, else the attacker, else the jungle shaper
    const float* from = towerBack && hitTower ? hitTower->pos : attacker ? attacker->pos : jungle ? jungle->pos : nullptr;
    float goal[3];
    StepPoint(self.pos, from ? from : self.pos, kRetreatStep, false, self.team, goal);
    if (towerBack) ++g_tw.backBy[towerKind >= 1 && towerKind <= 3 ? towerKind : 0];
    else ++g_tw.otherBacks;
    // BOTTOWER: the plan retreats already (its Back gambit, FleeTowerTargeting's, or a recall): a step of ours would abort
    // it and the tree's UseAbility abort ours in turn (round 1: a bot hovered 95 s at a tower's edge that way)
    unsigned hid = 0;
    const char* hname = nullptr;
    if (g_opt.botTowerAware && SehHeldAbility(o, &hid, &hname) && hname &&
        (std::strstr(hname, "Bot_Back") || std::strstr(hname, "ReturnToBase") || std::strstr(hname, "Recall"))) {
      ++g_tw.planRetreats;
      res = 1;
      if (towerBack) st.towerThink = gms::agents::kTtPlanRetreat;
    } else {
      res = BotRequest(st.noun, "reflex back-off", gms::agents::NullPosUse::kGoal, kAbilityBotMoveToTarget, -1, self.id,
                       0, goal, 1);
      res = ReflexStepResult(o, st, self, res, towerBack, nowMs);
    }
  } else if (d.kind == gms::agents::kReflexFight) {
    const Snap* tgt = d.why[0] == 'r' ? attacker : jungle;  // "retaliate" / "jungle"
    // BOTTOWER: no fight under an enemy tower that would shoot the bot (a dive aside)
    if (tgt && TowerKeepsOutTarget(st, self, s.hpFrac, tgt->id, nullptr, s.finishing, nowMs)) {
      ++g_tw.keptOutFights;
      tgt = nullptr;
    }
    if (tgt) res = ReflexAttack(o, st, self, pv, mtarget, *tgt, nowMs);
  } else {  // defend: whoever attacks the well, else walk to it
    const Snap* tgt = nullptr;
    float best = 30.f;
    for (const Snap& p : g_snapPlayers) {
      if (p.team == self.team || Dist2(p.pos, wellPos.pos) > best || !SehVisible(o, p.obj)) continue;
      best = Dist2(p.pos, wellPos.pos);
      tgt = &p;
    }
    if (tgt) {
      res = ReflexAttack(o, st, self, pv, mtarget, *tgt, nowMs);
    } else if (g_opt.botWellDefense) {
      res = WellDefenseWalk(o, st, self, wellPos, nowMs);
    } else {
      float goal[3];
      StepPoint(self.pos, wellPos.pos, kWellStep, true, self.team, goal);
      if (Dist2(self.pos, wellPos.pos) <= kWellStep) std::memcpy(goal, wellPos.pos, sizeof goal);
      res = BotRequest(st.noun, "reflex walk to a well", gms::agents::NullPosUse::kGoal, kAbilityBotMoveToTarget, -1,
                       self.id, 0, goal, 1);
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

constexpr int kRoleHunterIndex = 2;  // RolesTuning.role[] index (gms/core/bots/composition.h kRoleHunter)

// HUNTERBOT: the finisher. No Hunter phase casts the R (kits.h); a Hunter casts it at a visible enemy shaper within its
// kit's reach at or below its HP threshold, tried at most once a second. True when the R started (the think ends).
bool HunterFinisherStep(void* o, AgentState& st, const ObjInfo& self, const BotPlayerView& pv, std::uint32_t mtarget,
                        unsigned long long nowMs) {
  if (nowMs < st.finisherNextMs || self.hp <= 0.f) return false;
  st.finisherNextMs = nowMs + 1000;
  if (BotRoleOf(self.id) != kRoleHunterIndex) return false;
  const gms::bots::ShaperKit* kit = KitOf(st);
  const int rank = pv.ranks[gms::agents::kSlotR];
  if (!kit || rank <= 0) return false;
  std::vector<gms::bots::FinisherCandidate> cands;
  std::vector<const Snap*> who;
  for (const Snap& p : g_snapPlayers) {
    if (p.team == self.team || p.id == self.id || p.hp <= 0.f) continue;
    const float d = Dist2(p.pos, self.pos);
    if (d > kit->rReach) continue;
    cands.push_back({d, p.HpFrac(), SehVisible(o, p.obj)});
    who.push_back(&p);
  }
  const int i = gms::bots::FinisherTarget(kit, rank, cands);
  if (i < 0) return false;
  const Snap& t = *who[static_cast<std::size_t>(i)];
  const bool around = kit->rTarget == gms::bots::kKitTargetAround;
  if (!around && t.id != mtarget && SehHostile(self.team, t.id)) SehSetTarget(o, t.id);
  ++g_bot.finisherTries;
  const int res = BotRequest(st.noun, "Hunter finisher R",
                             around ? gms::agents::NullPosUse::kSelf : gms::agents::NullPosUse::kGoal, 0,
                             gms::agents::kSlotR, self.id, around ? 0 : t.id, around ? self.pos : t.pos, rank);
  const bool started = res == 1 || res == 2;
  if (started) ++g_bot.finisherStarted;
  if ((started || res != st.finisherResult) && IdleLogBudget())
    Log("agents: bot %08X %s finisher: R (rank %d) at %08X, %.1f units away at %.0f%% HP -> %d%s (HUNTERBOT) (t %.2f s)",
        self.id, st.noun, rank, t.id, static_cast<double>(cands[static_cast<std::size_t>(i)].dist),
        static_cast<double>(t.HpFrac() * 100.f), res, started ? " started" : "", static_cast<double>(nowMs) / 1000.0);
  st.finisherResult = res;
  return started;
}

// HUNTERBOT: the E of a kit that doesn't spin (Faris's dash, Freia's stun) at the bot's target in ClearCamp and
// GankThings, which have no E gambit (the AI's E is SpinToWin's, which the Lua turns off for them); tried once a second,
// after a start the kit's eWaitMs. MOYABOT: Moya's self-cast E (she keeps SpinToWin too) at her camp creature, in
// ClearCamp only and between two hits (kits.h KitEPhaseAllows). True when the E started (the think ends).
bool HunterKitEStep(void* o, AgentState& st, const ObjInfo& self, const BotPlayerView& pv, std::uint32_t mtarget,
                    const char* phaseName, unsigned long long nowMs) {
  if (!g_opt.botKits || nowMs < st.kitENextMs || self.hp <= 0.f || !mtarget || !phaseName) return false;
  const bool camp = std::strstr(phaseName, "ClearCamp") != nullptr;
  const bool gank = std::strstr(phaseName, "GankThings") != nullptr;
  if (!camp && !gank) return false;
  const gms::bots::ShaperKit* kit = KitOf(st);
  if (!kit || !kit->castsE) return false;
  // MOYABOT: her E in ClearCamp only, between two hits (tried again at the next think while a swing runs)
  if (!gms::bots::KitEPhaseAllows(kit, camp, gank, st.leaf == kLeafIdleDefault)) return false;
  st.kitENextMs = nowMs + 1000;
  if (BotRoleOf(self.id) != kRoleHunterIndex) return false;
  const int rank = pv.ranks[gms::agents::kSlotQ + 2];
  void* tobj = SehFind(mtarget);
  ObjInfo t;
  const bool live = tobj && SehObjInfo(tobj, &t) && t.hasHp && t.hp > 0.f && t.team != self.team &&
                    SehHostile(self.team, mtarget);
  const float d = live ? Distance2D(t.pos, self.pos) : 1e9f;
  if (!gms::bots::KitEWanted(kit, rank, live, d)) return false;
  ++g_bot.kitETries;
  const int res = BotRequest(st.noun, "Hunter kit E", gms::agents::NullPosUse::kGoal, 0, gms::agents::kSlotQ + 2,
                             self.id, mtarget, t.pos, rank);
  const bool started = res == 1 || res == 2;
  if (started) {
    ++g_bot.kitEStarted;
    st.kitENextMs = nowMs + static_cast<unsigned long long>(kit->eWaitMs);
  }
  if ((started || res != st.kitEResult) && IdleLogBudget())
    Log("agents: bot %08X %s phase %d (%s) kit E (rank %d) at %08X (%s), %.1f units away -> %d%s (HUNTERBOT) (t %.2f s)",
        self.id, st.noun, st.botPhase, phaseName, rank, mtarget, SehNounName(tobj), static_cast<double>(d), res,
        started ? " started" : "", static_cast<double>(nowMs) / 1000.0);
  st.kitEResult = res;
  return started;
}

// HUNTERROUTE: a laner's pull at the Hunter's first buff (hunterroute.cpp, gms/core/bots/route.h PullGoesOn): a bot
// that isn't a Hunter, chosen by the pull, basic-attacks the camp's biggest creature while the pull lasts. True while
// it does (the think ends: its plan's gambits wait).
bool HunterPullStep(void* o, AgentState& st, const ObjInfo& self, std::uint32_t mtarget) {
  if (hunterroute::Opts().pull <= 0 || self.hp <= 0.f) return false;
  const int role = BotRoleOf(self.id);
  if (role < 0 || role == kRoleHunterIndex) return false;
  const float maxHp = SehMaxHp(o);
  const std::uint32_t target =
      hunterroute::PullTarget(self.id, st.noun, self.team, self.pos, maxHp > 0.f ? self.hp / maxHp : 1.f);
  if (!target) return false;
  void* tobj = SehFind(target);
  ObjInfo t;
  if (!tobj || !SehObjInfo(tobj, &t) || (t.hasHp && t.hp <= 0.f)) return false;
  if (target != mtarget && SehHostile(self.team, target)) SehSetTarget(o, target);
  const int res = BotRequest(st.noun, "Hunter pull", gms::agents::NullPosUse::kGoal, 0, gms::agents::kSlotBasic,
                             self.id, target, t.pos, 1);
  hunterroute::NotePullRequest(self.id, target, res);
  return true;
}

// HUNTERROUTE: the host's Vanquish rule (gms/core/bots/route.h VanquishPick): a Hunter with Vanquish in a spell slot
// casts it at a creature of his camp fight that it kills (the mediums and majors; any at low HP), besides the data's
// gambit (the buff and pig majors only). True when it started (the think ends).
bool HunterVanquishStep(void* o, AgentState& st, const ObjInfo& self, const BotPlayerView& pv,
                        unsigned long long nowMs) {
  if (self.hp <= 0.f || !hunterroute::VanquishDue(self.id, nowMs) || BotRoleOf(self.id) != kRoleHunterIndex)
    return false;
  int slot = -1;
  for (int s = gms::agents::kSlotSpell1; s <= gms::agents::kSlotSpell3 && slot < 0; ++s)
    if (SehItemNamed(pv.items[gms::agents::SlotInventoryIndex(s)], "Vanquish")) slot = s;
  if (slot < 0) return false;
  static const gms::bots::VanquishConfig cfg;
  director::CampFight f;
  if (!director::CampFightNear(self.pos[0], self.pos[1], cfg.reach + 3.f, &f) || f.camp < 0) return false;
  std::vector<gms::bots::VanquishCandidate> cands;
  std::vector<std::uint32_t> ids;
  std::vector<ObjInfo> infos;
  for (int k = 0; k < f.count && k < director::CampFight::kMembers; ++k) {
    if (!(f.hps[k] > 0.f)) continue;
    void* m = SehFind(f.ids[k]);
    ObjInfo mi;
    if (!m || !SehObjInfo(m, &mi)) continue;
    const int npc = SehNpcType(m);
    gms::bots::VanquishCandidate c;
    c.hp = f.hps[k];
    c.maxHp = SehMaxHp(m);
    c.dist = Distance2D(mi.pos, self.pos);
    c.objective = npc >= 6 && npc <= 8;  // nNPCType ObjectiveMinor/Medium/Major
    cands.push_back(c);
    ids.push_back(f.ids[k]);
    infos.push_back(mi);
  }
  const float maxHp = SehMaxHp(o);
  const int i = gms::bots::VanquishPick(cands, SehPlayerLevel(pv.player), maxHp > 0.f ? self.hp / maxHp : 1.f, cfg);
  if (i < 0) return false;
  const std::size_t u = static_cast<std::size_t>(i);
  const int res = BotRequest(st.noun, "Hunter Vanquish", gms::agents::NullPosUse::kGoal, 0, slot, self.id, ids[u],
                             infos[u].pos, 1);
  hunterroute::NoteVanquish(self.id, true, res, cands[u].objective, ids[u], SehNounName(infos[u].obj), cands[u].hp,
                            nowMs);
  return res == 1 || res == 2;
}

// DESECBOT-3: the Desecrator's kit layer, once per think after BOTREFLEX: the combo's Q at his E's rooted target, in
// the fight phases R (the corpses spread), Drain at a low enemy, Wither on a Gladiator or a diving bruiser, in the fights
// and the lane the peel for a hurt ally (gms/core/agents/desecrator.h DecideDesecrator). True when a cast started (the
// think ends); the plan's gambits run otherwise, his E led and his Q held for the root (KitAllowsNow, the request).
bool DesecratorKitStep(void* o, AgentState& st, const ObjInfo& self, const BotPlayerView& pv, std::uint32_t mtarget,
                       const char* phaseName, unsigned long long nowMs) {
  if (!DesecLayer(st) || self.hp <= 0.f) return false;
  static const gms::agents::DesecConfig cfg;
  const long long now = static_cast<long long>(nowMs);
  gms::agents::DesecMemory& m = st.desec;
  // the spells in his slots (spell1-3: inventory 6-8)
  st.desecDrainSlot = st.desecWitherSlot = -1;
  for (int slot = gms::agents::kSlotSpell1; slot <= gms::agents::kSlotSpell3; ++slot) {
    void* item = pv.items[gms::agents::SlotInventoryIndex(slot)];
    if (!item) continue;
    if (SehItemNamed(item, "Wither")) st.desecWitherSlot = slot;
    else if (SehItemNamed(item, "Drain")) st.desecDrainSlot = slot;
  }
  // an E window that closed without its Q: the E missed (or the Q could not follow)
  if (m.eTarget && !gms::agents::DesecComboOpen(m, now, cfg)) {
    ++g_desec.comboMissed;
    if (DesecLogBudget())
      Log("agents: bot %08X %s kit combo: no Q at %08X rooted within %.1f s of the E (DESECBOT-3) (t %.2f s)",
          self.id, st.noun, m.eTarget, static_cast<double>(cfg.comboQMs) / 1000.0,
          static_cast<double>(nowMs) / 1000.0);
    gms::agents::DesecNoteQ(m);
  }
  gms::agents::DesecSelf s;
  s.pos[0] = self.pos[0];
  s.pos[1] = self.pos[1];
  s.phaseClass = gms::agents::DesecPhaseClassOf(phaseName);
  s.nowMs = now;
  s.rankQ = pv.ranks[gms::agents::kSlotQ];
  s.rankE = pv.ranks[kSlotE];
  s.rankR = pv.ranks[gms::agents::kSlotR];
  s.hasDrain = st.desecDrainSlot >= 0;
  s.hasWither = st.desecWitherSlot >= 0;
  std::vector<gms::agents::DesecEnemy> enemies;
  std::vector<const Snap*> who;
  std::vector<gms::agents::DesecAlly> allies;
  constexpr float kLook = 55.f;  // R's 50 and a margin
  for (const Snap& p : g_snapPlayers) {
    if (p.id == self.id || p.hp <= 0.f) continue;
    const float d = Dist2(p.pos, self.pos);
    if (d > kLook) continue;
    auto& tr = g_tracks[p.id];
    if (p.team == self.team) {
      gms::agents::DesecAlly a;
      a.id = p.id;
      a.dist = d;
      a.recentDamage = tr.damage.Fraction(p.maxHp, now);
      a.attacker = tr.lastAttacker;
      a.attackerAgeMs = tr.lastAttackerMs ? static_cast<long long>(nowMs - tr.lastAttackerMs) : -1;
      allies.push_back(a);
      continue;
    }
    gms::agents::DesecEnemy e;
    e.id = p.id;
    e.pos[0] = p.pos[0];
    e.pos[1] = p.pos[1];
    e.vel[0] = tr.motion.vel[0];
    e.vel[1] = tr.motion.vel[1];
    e.dist = d;
    e.hpFrac = p.HpFrac();
    e.visible = SehVisible(o, p.obj);
    if (e.visible) {
      const unsigned f = SehDesecFlags(p.obj);
      e.rooted = (f & 1u) != 0;
      e.knockedUp = (f & 2u) != 0;
      e.mortared = (f & 4u) != 0;
    }
    e.role = SehShaperRole(p.obj);
    if (tr.archetype == -2) tr.archetype = SehShaperArchetype(p.obj);
    e.archetype = tr.archetype;
    e.hitAllyAgeMs = tr.lastVictimMs && tr.lastVictimTeam == self.team ? static_cast<long long>(nowMs - tr.lastVictimMs) : -1;
    enemies.push_back(e);
    who.push_back(&p);
  }
  const gms::agents::DesecAction a = gms::agents::DecideDesecrator(s, enemies, allies, m, cfg);
  if (a.kind == gms::agents::kDesecNone) {
    if (s.phaseClass == gms::agents::kDesecPhaseFight && s.rankR > 0 && gms::agents::DesecRWindowOpen(m, now, cfg) &&
        now >= m.nextTryMs[gms::agents::kDesecR])
      ++g_desec.rHeld;
    return false;
  }
  const gms::agents::DesecEnemy& t = enemies[static_cast<std::size_t>(a.enemy)];
  const Snap& ts = *who[static_cast<std::size_t>(a.enemy)];
  int slot = 0;
  bool atPoint = false;  // TerrainPoint casts go to their point alone [assumed, as the led E]
  switch (a.kind) {
    case gms::agents::kDesecComboQ:
    case gms::agents::kDesecPeelQ: slot = gms::agents::kSlotQ; break;
    case gms::agents::kDesecPeelE: slot = kSlotE; atPoint = true; break;
    case gms::agents::kDesecR: slot = gms::agents::kSlotR; atPoint = true; break;
    case gms::agents::kDesecDrain: slot = st.desecDrainSlot; break;
    default: slot = st.desecWitherSlot; break;
  }
  if (slot <= 0) return false;
  const int rank = gms::agents::SlotIsRanked(slot) ? pv.ranks[slot] : 1;
  const float pos[3] = {a.point[0], a.point[1], ts.pos[2]};
  if (!atPoint && t.id != mtarget && SehHostile(self.team, t.id)) SehSetTarget(o, t.id);
  const int res = BotRequest(st.noun, "Desecrator kit", gms::agents::NullPosUse::kGoal, 0, slot, self.id,
                             atPoint ? 0u : t.id, pos, rank);
  const bool started = res == 1 || res == 2;
  const int corpse = m.rCorpses;  // before this one, of the open cast (0 a new cast)
  const bool newCast = !gms::agents::DesecRWindowOpen(m, now, cfg);
  const float spread = newCast ? -1.f : gms::agents::DesecNearestCorpse(m, a.point);
  const double sinceE = m.eCastMs >= 0 ? static_cast<double>(now - m.eCastMs) / 1000.0 : -1.0;
  switch (a.kind) {
    case gms::agents::kDesecComboQ:
      ++g_desec.comboTries;
      if (started) {
        ++g_desec.comboStarted;
        g_desec.comboDelaySum += sinceE;
      }
      break;
    case gms::agents::kDesecR:
      ++g_desec.rTries;
      if (started) {
        const int n = newCast ? 0 : corpse;
        if (n >= 0 && n < 3) ++g_desec.rStarted[n];
        if (std::strcmp(a.why, "cluster") == 0) ++g_desec.rCluster;
        else if (std::strcmp(a.why, "spread") == 0) ++g_desec.rSpread;
        else ++g_desec.rLocked;
        if (spread >= 0.f && (g_desec.rMinSpread < 0.f || spread < g_desec.rMinSpread)) g_desec.rMinSpread = spread;
        gms::agents::DesecNoteR(m, a.point, now, cfg);
      }
      break;
    case gms::agents::kDesecDrain:
      ++g_desec.drainTries;
      if (started) ++g_desec.drainStarted;
      break;
    case gms::agents::kDesecWither:
      ++g_desec.witherTries;
      if (started) {
        ++g_desec.witherStarted;
        ++(t.role == 0 && std::strstr(a.why, "Gladiator") ? g_desec.witherGlad : g_desec.witherBruiser);
      }
      break;
    default:
      ++g_desec.peelTries;
      if (started) ++g_desec.peelStarted;
      break;
  }
  float lead = 0.f;
  if (a.kind == gms::agents::kDesecPeelE) lead = gms::agents::DesecDist(a.point, t.pos);
  DesecNoteRequest(st, self.team, slot, t.id, res, a.kind == gms::agents::kDesecPeelE, lead, nowMs);
  // a refusal: tried again after retryMs, after 5 s when on cooldown (-9999: Drain's 180 s, the plan's gambits cast it
  // too: in a live match all of the layer's Drain requests were refused so)
  constexpr int kOnCooldown = -9999;
  if (!started) m.nextTryMs[a.kind] = now + (res == kOnCooldown ? 5000 : cfg.retryMs);
  if ((started || res != st.desecResult) && DesecLogBudget()) {
    char extra[160] = "";
    if (a.kind == gms::agents::kDesecR && spread < 0.f)
      std::snprintf(extra, sizeof extra, ", corpse 1, %d enemy shaper(s) in its field", a.count);
    else if (a.kind == gms::agents::kDesecR)
      std::snprintf(extra, sizeof extra, ", corpse %d, %d enemy shaper(s) in its field, %.1f from the earlier corpses",
                    corpse + 1, a.count, static_cast<double>(spread));
    if (a.kind == gms::agents::kDesecComboQ)
      std::snprintf(extra, sizeof extra, ", %.2f s after the E", sinceE);
    if (a.kind == gms::agents::kDesecWither)
      std::snprintf(extra, sizeof extra, ", role %d, archetype %d", t.role, t.archetype);
    Log("agents: bot %08X %s phase %d (%s) kit %s (%s) at %08X, %.1f units away at %.0f%% HP, point (%.1f, %.1f)%s -> "
        "%d%s (DESECBOT-3) (t %.2f s)",
        self.id, st.noun, st.botPhase, phaseName ? phaseName : "?", gms::agents::DesecKindName(a.kind), a.why, t.id,
        static_cast<double>(t.dist), static_cast<double>(t.hpFrac * 100.f), static_cast<double>(a.point[0]),
        static_cast<double>(a.point[1]), extra, res, started ? " started" : "", static_cast<double>(nowMs) / 1000.0);
  }
  st.desecResult = res;
  if (!started) return false;
  st.idleSinceMs = 0;
  return true;
}

bool MikLogBudget() {  // its own: the first 600 lines, then every 10th
  static unsigned n = 0;
  return ++n <= 600 || n % 10 == 0;
}

// MIKELLABOT: Mikella's kit layer, once per think after BOTREFLEX: the escape E in Flee, R to assassinate or before or
// after a fight, the trade W at her target, the kite E away from a shaper too close, the chase E at a low target
// (gms/core/agents/mikella.h DecideMikella). True when a cast started or her R channels (the think ends); the plan's
// gambits run otherwise, without her E and R and with her W only near (KitAllowsNow).
bool MikellaKitStep(void* o, AgentState& st, const ObjInfo& self, const BotPlayerView& pv, std::uint32_t mtarget,
                    const char* phaseName, unsigned long long nowMs) {
  if (!MikLayer(st) || self.hp <= 0.f) return false;
  static const gms::agents::MikellaConfig cfg;
  const long long now = static_cast<long long>(nowMs);
  gms::agents::MikMemory& m = st.mik;
  if (gms::agents::MikHolding(m, now)) {
    ++g_mik.holds;
    return true;
  }
  gms::agents::MikSelf s;
  s.pos[0] = self.pos[0];
  s.pos[1] = self.pos[1];
  s.phaseClass = gms::agents::MikellaPhaseClassOf(phaseName);
  s.nowMs = now;
  s.rankW = pv.ranks[gms::agents::kSlotQ + 1];
  s.rankE = pv.ranks[gms::agents::kSlotQ + 2];
  s.rankR = pv.ranks[gms::agents::kSlotR];
  if (s.phaseClass == gms::agents::kMikPhaseOther) return false;
  std::vector<gms::agents::MikEnemy> enemies;
  std::vector<const Snap*> who;
  constexpr float kLook = 55.f;  // R's 50 and a margin
  for (const Snap& p : g_snapPlayers) {
    if (p.id == self.id || p.hp <= 0.f || p.team == self.team) continue;
    const float d = Dist2(p.pos, self.pos);
    if (d > kLook) continue;
    gms::agents::MikEnemy e;
    e.id = p.id;
    e.pos[0] = p.pos[0];
    e.pos[1] = p.pos[1];
    e.dist = d;
    e.hpFrac = p.HpFrac();
    e.visible = SehVisible(o, p.obj);
    if (p.id == mtarget) s.target = static_cast<int>(enemies.size());
    enemies.push_back(e);
    who.push_back(&p);
  }
  const gms::agents::MikAction a = gms::agents::DecideMikella(s, enemies, m, cfg);
  if (a.kind == gms::agents::kMikNone) return false;
  const gms::agents::MikEnemy& t = enemies[static_cast<std::size_t>(a.enemy)];
  int slot = gms::agents::kSlotQ + 2;  // the Es
  std::uint32_t target = 0;            // the E at its point alone [assumed, as the Desecrator's led E]
  gms::agents::NullPosUse use = gms::agents::NullPosUse::kGoal;
  float pos[3] = {a.point[0], a.point[1], self.pos[2]};
  if (a.kind == gms::agents::kMikR) {
    slot = gms::agents::kSlotR;
    target = t.id;
    pos[2] = who[static_cast<std::size_t>(a.enemy)]->pos[2];
    if (t.id != mtarget && SehHostile(self.team, t.id)) SehSetTarget(o, t.id);
  } else if (a.kind == gms::agents::kMikTradeW) {
    slot = gms::agents::kSlotQ + 1;
    use = gms::agents::NullPosUse::kSelf;  // a self cast: her own position is no goal
  }
  const int rank = pv.ranks[slot];
  const int res = BotRequest(st.noun, "Mikella kit", use, 0, slot, self.id, target, pos, rank);
  const bool started = res == 1 || res == 2;
  ++g_mik.tries[a.kind];
  if (started) {
    ++g_mik.started[a.kind];
    if (a.kind == gms::agents::kMikR) ++(t.hpFrac <= cfg.rLowHp ? g_mik.rAssassinate : g_mik.rPoke);
    if (a.kind == gms::agents::kMikTradeW) ++g_mik.wStarted;
  }
  constexpr int kOnCooldown = -9999;
  gms::agents::MikNoteResult(&m, a, started, now, cfg, res == kOnCooldown);
  if ((started || res != st.mikResult) && MikLogBudget())
    Log("agents: bot %08X %s phase %d (%s) kit %s (%s) at %08X, %.1f units away at %.0f%% HP, point (%.1f, %.1f) -> "
        "%d%s (MIKELLABOT) (t %.2f s)",
        self.id, st.noun, st.botPhase, phaseName ? phaseName : "?", gms::agents::MikKindName(a.kind), a.why, t.id,
        static_cast<double>(t.dist), static_cast<double>(t.hpFrac * 100.f), static_cast<double>(pos[0]),
        static_cast<double>(pos[1]), res, started ? " started" : "", static_cast<double>(nowMs) / 1000.0);
  st.mikResult = res;
  if (!started) return false;
  st.idleSinceMs = 0;
  return true;
}

// ---- NULLPOS: the forced case ([host] test_nullpos_follow, a test aid) -----------------------------------------------
// Proves the Follow guard live: at the option's match second the first bot that thinks (alive, not held, over 30 from
// the centre) is taken out of the brain: its Pursue leaf is started on a point alone, (0.5, 0.5) inside the parasite's
// lair (not walkable to 0.5, so the Follow loops and re-reads the point every pass), mTarget cleared, flag 8 on. Once
// it is within 12 of the centre (or after 60 s) the host writes the zero vector under the running Follow and turns
// flag 8 off, as D6d's old EndPointPursuit did (BACKTOACTION's bug). The Follow's next pass must give a "NULLPOS: ...
// [Follow]" line, and the Follow must end (the leaf leaves 12 Pursue). After 15 s, or once it ended, the bot goes back
// to its brain. One "test_nullpos_follow:" line per step and a result line.
struct ForcedFollow {
  int phase = 0;  // 0 waiting, 1 walking to the point, 2 the zero vector written, 3 done
  std::uint32_t id = 0;
  unsigned long long sinceMs = 0, zeroMs = 0, lastKickMs = 0;
  unsigned followHitsBefore = 0;
  std::uintptr_t leafAtZero = 0;
};
ForcedFollow g_forced;

void ForcedFollowDone(void* o, AgentState& st, const char* how, unsigned long long nowMs) {
  const unsigned hits = g_nullpos.by[static_cast<int>(gms::agents::NullPosWhere::kFollow)] - g_forced.followHitsBefore;
  Log("agents: test_nullpos_follow: result: bot %08X %s: %s; NULLPOS [Follow] lines since the zero vector %u, leaf now "
      "%s (t %.2f s)",
      g_forced.id, st.noun, how, hits, LeafName(st.leaf), static_cast<double>(nowMs) / 1000.0);
  g_forced.phase = 3;
  g_forcedFollowId = 0;
  st.pursue = false;
  SehSetTreeFlag(o, 8, false);
  SehResetDefaultBehavior(o);
}

// True while the forced case holds this bot's think.
bool ForcedFollowStep(void* o, AgentState& st, const ObjInfo& self, unsigned long long nowMs) {
  if (g_opt.testNullPosFollowS <= 0 || g_forced.phase == 3) return false;
  const float point[3] = {0.5f, 0.5f, self.pos[2]};
  const float fromCentre = std::sqrt(self.pos[0] * self.pos[0] + self.pos[1] * self.pos[1]);
  if (g_forced.phase == 0) {
    const char* held = nullptr;
    if (nowMs < static_cast<unsigned long long>(g_opt.testNullPosFollowS) * 1000ull || fromCentre <= 30.f ||
        SehHeldByUninterruptible(o, &held))
      return false;
    g_forced.phase = 1;
    g_forced.id = self.id;
    g_forced.sinceMs = g_forced.lastKickMs = nowMs;
    if (st.pointPursuit) EndPointPursuit(o, st, self, 0, nullptr, nowMs, false);
    SehSetTarget(o, 0);
    SehSetTargetPosition(o, point);
    st.pursue = true;
    SehSetTreeFlag(o, 8, true);
    const bool ok = SehResetDefaultBehavior(o);
    Log("agents: test_nullpos_follow: bot %08X %s at (%.1f, %.1f), %.1f from the centre, leaf %s: Pursue started on the "
        "point (0.5, 0.5) alone (restart %s) (t %.2f s)",
        self.id, st.noun, static_cast<double>(self.pos[0]), static_cast<double>(self.pos[1]),
        static_cast<double>(fromCentre), LeafName(st.leaf), ok ? "ok" : "failed", static_cast<double>(nowMs) / 1000.0);
    return true;
  }
  if (self.id != g_forced.id) return false;
  if (self.hasHp && self.hp <= 0.f) {
    ForcedFollowDone(o, st, "the bot died", nowMs);
    return false;
  }
  if (g_forced.phase == 1) {
    SehSetTarget(o, 0);  // nothing to follow but the point
    if (st.leaf != kLeafPursue && nowMs - g_forced.lastKickMs >= 1000) {
      g_forced.lastKickMs = nowMs;
      SehSetTargetPosition(o, point);
      SehSetTreeFlag(o, 8, true);
      SehResetDefaultBehavior(o);
    }
    if ((st.leaf == kLeafPursue && fromCentre <= 12.f) || nowMs - g_forced.sinceMs >= 60000) {
      g_forced.phase = 2;
      g_forced.zeroMs = nowMs;
      g_forced.leafAtZero = st.leaf;
      g_forced.followHitsBefore = g_nullpos.by[static_cast<int>(gms::agents::NullPosWhere::kFollow)];
      g_forcedFollowId = self.id;
      ClearTargetPosition(o);  // under the running Follow, no restart: the old EndPointPursuit
      st.pursue = false;
      SehSetTreeFlag(o, 8, false);
      Log("agents: test_nullpos_follow: bot %08X %s at (%.1f, %.1f), %.1f from the centre, leaf %s after %.1f s: the zero "
          "vector written under it, flag 8 off, no restart (t %.2f s)",
          self.id, st.noun, static_cast<double>(self.pos[0]), static_cast<double>(self.pos[1]),
          static_cast<double>(fromCentre), LeafName(st.leaf), static_cast<double>(nowMs - g_forced.sinceMs) / 1000.0,
          static_cast<double>(nowMs) / 1000.0);
    }
    return true;
  }
  // phase 2: the guard must end the Follow
  SehSetTarget(o, 0);
  const unsigned hits = g_nullpos.by[static_cast<int>(gms::agents::NullPosWhere::kFollow)] - g_forced.followHitsBefore;
  char how[160];
  if (g_forced.leafAtZero == kLeafPursue && st.leaf != kLeafPursue) {
    std::snprintf(how, sizeof how, "RECOVERED: the Follow ended %.1f s after the zero vector (leaf 12 Pursue -> %s)",
                  static_cast<double>(nowMs - g_forced.zeroMs) / 1000.0, LeafName(st.leaf));
    ForcedFollowDone(o, st, how, nowMs);
    return false;
  }
  if (nowMs - g_forced.zeroMs >= 15000) {
    std::snprintf(how, sizeof how, "%s after 15 s (leaf at the zero vector %s)",
                  g_forced.leafAtZero != kLeafPursue ? "NOT SET UP: no Pursue leaf ran"
                  : hits ? "NOT RECOVERED: the guard fired but the leaf stayed" : "NOT RECOVERED: no guard line",
                  LeafName(g_forced.leafAtZero));
    ForcedFollowDone(o, st, how, nowMs);
    return false;
  }
  return true;
}

// ---- BACKTOACTION (gms/core/agents/lost.h) ----------------------------------------------------------------------------
unsigned g_lostLogLines = 0;
bool LostLogBudget() {
  ++g_lostLogLines;
  return g_lostLogLines <= 400 || g_lostLogLines % 20 == 0;
}
constexpr float kLostStandRadius = 20.f;               // the stand the stats measure: near the centre, no mTarget
constexpr unsigned long long kLostStandLogMs = 10000;  // a stand this long gets its own line when it ends

// Once per think, before the held check (so a hold defers a restart): the root fix and the safety net. True when it
// requested the walk to the lane front (the think ends there).
bool BackToActionStep(void* o, AgentState& st, const ObjInfo& self, const BotView& v, bool held,
                      unsigned long long nowMs) {
  const std::uint32_t raw = SehRawTarget(o);
  const bool pursueLeaf = st.leaf == kLeafPursue;
  const bool idleLeaf = st.leaf == kLeafIdleDefault;
  const bool parasite = gms::agents::ParasiteTaskName(v.planName) || gms::agents::ParasiteTaskName(v.phaseName);
  // the stand the stats measure (every leaf, the option on or off): near the centre without mTarget or a point
  const bool standing = !raw && !st.pointPursuit && gms::agents::NearCentre(self.pos[0], self.pos[1], kLostStandRadius);
  if (standing) {
    if (!st.centreStandSinceMs) st.centreStandSinceMs = nowMs;
    const unsigned long long thinkMs = static_cast<unsigned long long>(g_opt.botThinkMs > 0 ? g_opt.botThinkMs : 200);
    if (st.centreStandLastMs && nowMs - st.centreStandLastMs <= 4 * thinkMs)
      g_bot.lostCentreSeconds += static_cast<double>(nowMs - st.centreStandLastMs) / 1000.0;
    st.centreStandLastMs = nowMs;
    const double s = static_cast<double>(nowMs - st.centreStandSinceMs) / 1000.0;
    if (parasite) st.centreStandParasite = true;
    double& longest = st.centreStandParasite ? g_bot.lostLongestParasiteS : g_bot.lostLongestS;
    if (s > longest) longest = s;
  }
  if (!standing && st.centreStandSinceMs) {
    const unsigned long long len = st.centreStandLastMs - st.centreStandSinceMs;
    if (len >= kLostStandLogMs && LostLogBudget())
      Log("agents: bot %08X %s stood %.1f s within %.0f of the map's centre without a target (t %.2f-%.2f s; plan %d "
          "%s, phase %d %s)%s (BACKTOACTION)",
          self.id, st.noun, static_cast<double>(len) / 1000.0, static_cast<double>(kLostStandRadius),
          static_cast<double>(st.centreStandSinceMs) / 1000.0, static_cast<double>(st.centreStandLastMs) / 1000.0,
          st.botPlan, v.planName ? v.planName : "?", st.botPhase, v.phaseName ? v.phaseName : "?",
          st.centreStandParasite ? ", a parasite task" : "");
    st.centreStandSinceMs = st.centreStandLastMs = 0;
    st.centreStandParasite = false;
  }

  gms::agents::LostOptions lo;
  lo.pointFollowFix = g_opt.botPointFollowFix;
  lo.lostMs = g_opt.botLostMs;
  gms::agents::LostInput in;
  in.nowMs = nowMs;
  in.x = self.pos[0];
  in.y = self.pos[1];
  in.leaf = pursueLeaf ? gms::agents::LostLeaf::kPursue
                       : idleLeaf ? gms::agents::LostLeaf::kIdleDefault : gms::agents::LostLeaf::kOther;
  in.hasTarget = raw != 0;
  in.pointPursuit = st.pointPursuit;
  in.parasiteTask = parasite;
  in.held = held;
  bool clearPoint = false;
  const gms::agents::LostAction act = gms::agents::LostStep(st.lost, in, lo, &clearPoint);
  // NULLPOS: the ended pursuit's point leaves the blackboard now, before any restart (EndPointPursuit kept it)
  if (clearPoint) ClearTargetPosition(o);
  // NULLPOS: the blackboard's target position is only ever the zero vector or a pursuit's point; (0, 0) with a height
  // (not the engine's "none": the scorer would start a Follow to the centre) is a NULLPOS
  float tp[3];
  if (!st.pointPursuit && SehTargetPosition(o, tp) && gms::agents::IsNullPos(tp) && tp[2] != 0.f) {
    char detail[64];
    std::snprintf(detail, sizeof detail, "+0x1E64 = (0, 0, %.2f)", static_cast<double>(tp[2]));
    NoteBotNullPos(self.id, st.noun, gms::agents::NullPosWhere::kTargetPosition, "blackboard check", detail);
    ClearTargetPosition(o);
  }
  if (act == gms::agents::LostAction::kNone) return false;
  if (act == gms::agents::LostAction::kRestartPointFollow || act == gms::agents::LostAction::kRestartNoTarget) {
    const bool point = act == gms::agents::LostAction::kRestartPointFollow;
    const bool ok = SehResetDefaultBehavior(o);
    if (ok) ++(point ? g_bot.pointFollowRestarts : g_bot.noTargetRestarts);
    if (LostLogBudget())
      Log("agents: bot %08X %s phase %d (%s): the Pursue leaf %s at (%.1f, %.1f), %.1f from the centre: restarted%s "
          "(BACKTOACTION) (t %.2f s)",
          self.id, st.noun, st.botPhase, v.phaseName ? v.phaseName : "?",
          point ? "outlived its point pursuit" : "had nothing to follow", static_cast<double>(self.pos[0]),
          static_cast<double>(self.pos[1]),
          static_cast<double>(std::sqrt(self.pos[0] * self.pos[0] + self.pos[1] * self.pos[1])), ok ? "" : " (failed)",
          static_cast<double>(nowMs) / 1000.0);
    return false;  // the think goes on: the gambits may act at once
  }
  // the walk to the lane front
  ++g_bot.lostWalks;
  float front[3];
  int lane = 0;
  if (!bots::LaneFrontOf(self.id, self.team, front, &lane)) {
    ++g_bot.lostNoFront;
    const bool ok = SehResetDefaultBehavior(o);
    if (LostLogBudget())
      Log("agents: bot %08X %s phase %d (%s) lost %.1f s on the centre at (%.1f, %.1f): no lane front, default "
          "behavior restarted%s (BACKTOACTION) (t %.2f s)",
          self.id, st.noun, st.botPhase, v.phaseName ? v.phaseName : "?",
          static_cast<double>(g_opt.botLostMs) / 1000.0, static_cast<double>(self.pos[0]),
          static_cast<double>(self.pos[1]), ok ? "" : " (failed)", static_cast<double>(nowMs) / 1000.0);
    return false;
  }
  front[2] = self.pos[2];
  const int res = BotRequest(st.noun, "BACKTOACTION walk to the lane front", gms::agents::NullPosUse::kGoal,
                             kAbilityBotMoveToTarget, -1, self.id, 0, front, 1);
  const bool started = res == 1 || res == 2;
  if (started) ++g_bot.lostWalksStarted;
  if (LostLogBudget())
    Log("agents: bot %08X %s phase %d (%s) lost %.1f s on the centre at (%.1f, %.1f) in leaf %s without a target: walk "
        "to the lane-%d front (%.1f, %.1f), %.1f units -> %d%s (BACKTOACTION) (t %.2f s)",
        self.id, st.noun, st.botPhase, v.phaseName ? v.phaseName : "?", static_cast<double>(g_opt.botLostMs) / 1000.0,
        static_cast<double>(self.pos[0]), static_cast<double>(self.pos[1]), LeafName(st.leaf), lane,
        static_cast<double>(front[0]), static_cast<double>(front[1]), static_cast<double>(Distance2D(front, self.pos)),
        res, started ? " started" : "", static_cast<double>(nowMs) / 1000.0);
  return started;
}

void BotBrainStep(void* sim, void* o, AgentState& st, unsigned long long nowMs) {
  const bool dying = st.leaf == kLeafDeath || st.leaf == kLeafDeathAbility || st.leaf == kLeafDeathAbilityDefault;
  if (dying || st.leaf == 0x0141E1B8 || st.leaf == 0x0141DE90) {
    st.wellDef.well = -1;  // WELLDEF: a death ends a well's defence
    ObjInfo dead;  // KILLFINISH: a death ends an open episode (these leaves also run without one: HP 0 decides)
    if (st.kfEpisode && SehObjInfo(o, &dead) && dead.hasHp && dead.hp <= 0.f)
      KillFinishEpisodeStep(st, dead.id, true, nowMs);
    st.kfHoldSinceMs = 0;
    if (dying) {  // HUNTERROUTE: a Hunter's death (counted once; HP 0 decides, as for KILLFINISH)
      ObjInfo d;
      if (SehObjInfo(o, &d) && d.hasHp && d.hp <= 0.f) hunterroute::BotDying(d.id);
    }
    // CAMPFINISH: a death in a camp fight ends it; while an ability holds the agent (leaves 3 and D3: a cast, a spin)
    // the fight goes on and is sampled (round 1 took those leaves for deaths: 2 of 4 holds "died", 6 of 12 leaves
    // were "not measured")
    if (st.campTrack.Camp() >= 0 || st.campHoldSinceMs) {
      ObjInfo self;
      if (SehObjInfo(o, &self)) {
        if (dying || (self.hasHp && self.hp <= 0.f)) {
          CampFinishDeath(st, self.id, nowMs);
        } else if (st.campTrack.LastMs() < 0 ||
                   nowMs >= static_cast<unsigned long long>(st.campTrack.LastMs()) + kCampHeldSampleMs) {
          CampFinishSample(o, st, self, SehMaxHp(o), nullptr, nowMs);
        }
      }
    }
    return;
  }
  if (nowMs < st.botNextThinkMs) return;
  st.botNextThinkMs = nowMs + static_cast<unsigned long long>(g_opt.botThinkMs > 0 ? g_opt.botThinkMs : 200);
  // BOTTOWER: what the brain did at this think, counted into an open episode under tower fire when the think ends
  struct TowerThinkNote {
    AgentState& st;
    ~TowerThinkNote() {
      if (st.towerExp.open()) st.towerExp.Think(st.towerThink);
    }
  } towerThinkNote{st};
  st.towerThink = gms::agents::kTtBeforeReflex;
  ObjInfo self;
  if (!SehObjInfo(o, &self) || GameOver(sim)) return;
  if (self.hasHp && self.hp <= 0.f) {
    hunterroute::BotDying(self.id);  // HUNTERROUTE
    return;
  }
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
  KillFinishEpisodeStep(st, self.id, false, nowMs);  // KILLFINISH: an open episode's end
  if (ForcedFollowStep(o, st, self, nowMs)) return;  // NULLPOS's forced case (a test aid, off by default)
  const double inPlan = static_cast<double>(nowMs - st.planEnteredMs) / 1000.0;
  // CAMPFINISH: a Hunter's camp fight, sampled at every think in his Farm plan
  if (st.campTrack.Camp() >= 0 || st.campHoldSinceMs || (v.planOk && v.planName && std::strstr(v.planName, "_Farm")))
    CampFinishSample(o, st, self, SehMaxHp(o), &v, nowMs);
  // HUNTERROUTE: a Hunter's level, HP and plan for his opening route and its stats
  if (BotRoleOf(self.id) == kRoleHunterIndex) {
    BotPlayerView hv;
    const float maxHp = SehMaxHp(o);
    if (SehBotPlayer(sim, o, &hv))
      hunterroute::HunterThink(self.id, st.noun, self.team, SehPlayerLevel(hv.player),
                               maxHp > 0.f ? self.hp / maxHp : 1.f, self.pos,
                               v.planOk && v.planName && std::strstr(v.planName, "_Flee") != nullptr);
  }

  // KILLFINISH: this think's decision (an EngageForKill plan only)
  KillFinishThink(o, st, self, v, nowMs);
  // 1. the plan node's links to other plans (prioritized list). BOTCYCLE ([host] bot_plan_chain): a plan entered in this
  // think whose own link fires at once is passed through (kPlanChainHops changes per think at most, never back into a
  // plan visited in it), so a one-think visit such as EngageForKill -> Lane -> Lane_Cautious is no quick exit
  constexpr int kPlanChainHops = 2;
  int visited[kPlanChainHops + 1] = {st.botPlan, -1, -1};
  int visitedCount = 1;
  for (int hop = 0; hop < kPlanChainHops && v.planOk; ++hop) {
    const double inPlanNow = hop == 0 ? inPlan : 0.0;
    bool changed = false;
    for (std::size_t i = 0; i < v.nodeLinks.size() && i < st.nodeLinkReady.size(); ++i) {
      const BotLink& l = v.nodeLinks[i];
      if (!gms::agents::PlanChainAllows(visited, visitedCount, l.outputNode, hop, kPlanChainHops)) continue;
      if (nowMs < st.nodeLinkNext[i]) continue;
      if (l.interval > 0) st.nodeLinkNext[i] = nowMs + static_cast<unsigned long long>(l.interval * 1000.0f);
      gms::agents::LinkView lv = BotLinkView(l, inPlanNow, st.nodeLinkReady[i], st.nodeLinkFired[i] != 0, nowMs);
      if (!gms::agents::LinkOpen(lv)) continue;
      std::uint32_t unused = 0;
      std::int32_t linkMission = 0;  // D6b: condition_Bot_IsMissionAvailable's mission id (ConditionOut::linkValue)
      if (!Conditions(l.conditions, l.conditionCount, self.id, &unused, false, nullptr, &linkMission)) continue;
      bool isPlan = false, isPhase = false;
      if (!SehNodeKind(o, l.outputNode, &isPlan, &isPhase) || !isPlan) continue;
      // CAMPFINISH: a Hunter's Flee link is held while he wins the trade against his camp
      if (CampFinishHolds(o, st, self, l.outputNode, nowMs)) continue;
      BotView next;
      if (!SehReadBotView(o, l.outputNode, -1, &next) || !next.planOk) break;
      // KILLFINISH: an exit from a fight plan while the bot wins its trade is held (the link stays unfired)
      if (IsFightPlan(v) && !IsFightPlan(next) && KillFinishExit(o, st, self, v, next, i, nowMs)) continue;
      // BOTCYCLE: no stance flip back into the plan left a moment ago (Lane <-> Lane_Cautious, Main <-> Matrix)
      if (gms::agents::PlanBounceHeld(
              l.outputNode == st.prevPlan,
              st.prevPlanLeftMs ? static_cast<long long>(nowMs - st.prevPlanLeftMs) : -1LL,
              static_cast<long long>(g_opt.botPlanDwellMs), IsFightOrFleePlan(v),
              IsFightOrFleePlan(next) || IsDefensivePlan(next))) {
        ++g_kf.dwellHeld;
        static unsigned dwellLogged = 0;
        if (++dwellLogged <= 300 || dwellLogged % 50 == 0)
          Log("agents: bot %08X %s plan %d (%s) -> %d (%s) by link %zu held: back into the plan left %.2f s ago "
              "(BOTCYCLE) (t %.2f s)",
              self.id, st.noun, st.botPlan, v.planName, l.outputNode, next.planName, i,
              static_cast<double>(nowMs - st.prevPlanLeftMs) / 1000.0, static_cast<double>(nowMs) / 1000.0);
        continue;
      }
      st.nodeLinkFired[i] = 1;
      st.nodeLinkReady[i] =
          nowMs + static_cast<unsigned long long>(gms::agents::LinkCooldownSeconds(l.cooldown, l.maxCooldown, Random01()) *
                                                  1000.0);
      ++g_bot.planChanges;
      // D6c: a quick exit (the plan left within kQuickExitMs) and a bounce (back into the plan left just before);
      // BOTCYCLE: a plan left in the think it was entered is a pass-through, neither
      const bool passed = hop > 0;
      const bool quick = !passed && nowMs - st.planEnteredMs < kQuickExitMs;
      const bool bounce = !passed && l.outputNode == st.prevPlan && nowMs - st.prevPlanLeftMs < kQuickExitMs;
      if (passed) ++g_bot.passThroughs;
      // BOTCYCLE: an engage (a KillShaper join or the EngageForKill condition) left again at once blocks the bot's
      // next engages for a while (the Lua asks nBotHost.EngageBlocked; KILLFINISH's "would finish" still passes)
      if (quick && IsEngagePlan(v) && !IsEngagePlan(next)) {
        ++g_kf.engagesFailed;
        if (st.kfEnteredByJoin) ++g_kf.joinsFailed;
        if (g_opt.botEngageBlockMs > 0)
          st.kfEngageBlockUntilMs = nowMs + static_cast<unsigned long long>(g_opt.botEngageBlockMs);
      }
      st.kfEnteredByJoin = IsEngagePlan(next) && linkMission > 0 && !IsFightPlan(v);
      if (st.kfEnteredByJoin) ++g_kf.joins;
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
        Log("agents: bot %08X %s plan %d (%s) -> %d (%s) by link %zu after %.2f s%s%s%s (t %.2f s)", self.id, st.noun,
            st.botPlan, v.planName, l.outputNode, next.planName, i, inPlanNow, bounce ? ", a bounce" : "",
            passed ? ", passed through (BOTCYCLE)" : "", extra, static_cast<double>(nowMs) / 1000.0);
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
      changed = true;
      break;
    }
    if (!changed) break;
    KillFinishThink(o, st, self, v, nowMs);  // the plan entered: its own decision (an EngageForKill plan)
    if (!g_opt.botPlanChain) break;
    visited[visitedCount++] = st.botPlan;
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
      // KILLFINISH: no FleeTowerTargeting (its GoBack) while the bot finishes a won trade: the next link decides
      if (KillFinishing(st, nowMs)) {
        BotView fv;
        if (SehReadBotView(o, st.botPlan, l.outputNode, &fv) && fv.phaseOk && fv.phaseName &&
            std::strstr(fv.phaseName, "Flee")) {
          ++g_kf.towerPhaseSkipped;
          KillFinishHold(o, st, self, nowMs);
          continue;
        }
      }
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
      if (st.pointPursuit) EndPointPursuit(o, st, self, 0, nullptr, nowMs, false);  // D6d; NULLPOS: clear now
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
  const bool heldUninterruptible = g_opt.respectCanInterrupt && SehHeldByUninterruptible(o, &held);
  // BACKTOACTION: a Pursue leaf that outlived its point pursuit, one with nothing to follow, a bot lost on the centre
  if (BackToActionStep(o, st, self, v, heldUninterruptible, nowMs)) {
    st.idleSinceMs = 0;
    return;
  }
  if (heldUninterruptible) {
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
  const float maxHpNow = SehMaxHp(o);  // BOTTOWER: the keep-out's own HP fraction
  const float selfHpFrac = maxHpNow > 0.f ? self.hp / maxHpNow : 1.f;
  // HUNTERBOT: a Hunter's R at a low enemy shaper first
  if (g_opt.botHunterUlt && HunterFinisherStep(o, st, self, pv, mtarget, nowMs)) {
    st.idleSinceMs = 0;
    return;
  }
  // HUNTERROUTE: a laner's pull at the Hunter's first buff
  if (HunterPullStep(o, st, self, mtarget)) {
    st.idleSinceMs = 0;
    return;
  }
  // BOTREFLEX: danger and opportunity before the plan's own gambits
  if (g_opt.botReflex && BotReflexStep(o, st, self, v, pv, mtarget, nowMs)) return;
  // HUNTERROUTE: a Hunter's Vanquish at a camp creature it kills
  if (HunterVanquishStep(o, st, self, pv, nowMs)) {
    st.idleSinceMs = 0;
    return;
  }
  // DESECBOT-3: the Desecrator's kit layer (the combo's Q, R, Drain, Wither, the peel)
  if (DesecratorKitStep(o, st, self, pv, mtarget, v.phaseName, nowMs)) return;
  // MIKELLABOT: Mikella's kit layer (her E and R, the trade W)
  if (MikellaKitStep(o, st, self, pv, mtarget, v.phaseName, nowMs)) return;
  // HUNTERBOT: Faris's and Freia's E at their target (no SpinToWin for them)
  if (HunterKitEStep(o, st, self, pv, mtarget, v.phaseName, nowMs)) {
    st.idleSinceMs = 0;
    return;
  }
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
    bool ownPos = false;
    if (hasSpot) std::memcpy(pos, spot, sizeof pos);  // the condition's position first
    else if (tobj && SehObjInfo(tobj, &t)) std::memcpy(pos, t.pos, sizeof pos);
    else ownPos = true;
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
          res = BotRequest(st.noun, "back step", gms::agents::NullPosUse::kGoal, kAbilityBotMoveToTarget, -1, self.id,
                           0, goal, 1);
          if (res == gms::agents::kResultHoldsAgent) res = 1;  // the last step still runs
          if (res == 1 || res == 2) ++g_bot.backSteps;
        }
        // BOTRECALL: a Flee phase's walk home is a recall once the bot is out of immediate danger; a recall that runs
        // already (ours or the data's own Flee gambit) answers -0x270B and counts as started, never as a refusal that
        // falls through to the walk (the walk would abort it)
        if (flee && g_opt.botRecall && RecallPossible(st, self, v.phaseName, nowMs)) {
          // NULLPOS: the one request with the zero vector on purpose: Return to Base takes no position, and the zero
          // vector is the engine's own "none" [assumed: BOTRECALL's live recalls worked with it]; not a goal, so it skips
          // BotRequest's check (an offset or the bot's own position could become a destination)
          static const float kZero3[3] = {};
          const int raw = SehBotRequest(kAbilityReturnToBase, -1, self.id, 0, kZero3, 1);
          res = gms::agents::RecallRequestResult(raw);
          if (raw == 1 || raw == 2) ++g_bot.recalls;
        }
      }
      if (res != 1 && res != 2) {
        // DESECBOT-3: the Desecrator's E at an enemy shaper goes where it will stand when the hands strike, to that
        // point alone [assumed: a TerrainPoint request with an object aims at the object]
        float lead = 0.f;
        const bool led = slot == kSlotE && g.guid == 0 && DesecLayer(st) && DesecLead(self, target, pos, &lead);
        // NULLPOS: the bot's own position (no spot, no object: a self cast) is not a goal
        res = BotRequest(st.noun, "gambit", ownPos && !led ? gms::agents::NullPosUse::kSelf : gms::agents::NullPosUse::kGoal,
                         g.guid, slot, self.id, led ? 0u : target, pos, rank);
        if (DesecLayer(st)) DesecNoteRequest(st, self.team, g.guid == 0 ? slot : -1, target, res, led, lead, nowMs);
      }
    }
    NoteKitW(o, st, self, g.slot, res, v.phaseName, nowMs);
    NoteKitWReach(st, self, g.slot, res, target, v.phaseName, nowMs);
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
      if (!KitAllowsNow(o, st, self, g.slot, nowMs)) {  // HUNTERBOT: a slot the kit leaves out; DESECBOT: W only near
        ++g_bot.kitSkips;
        continue;
      }
      if (gv.coolingDown && !(prioritized && g.waitOnCondition)) continue;
      bool po = false;
      gv.conditionsPass =
          Conditions(g.conditions, g.conditionCount, self.id, &targets[i], true, &spots[i * 3], nullptr, &po);
      pointOnly[i] = po ? 1 : 0;
      ++g_bot.conditions;
      // MOYABOT: a W that empowers the next hits only with the gambit's target (else mTarget) within the kit's reach
      if (gv.conditionsPass && !KitAllowsWNear(st, self, g.slot, targets[i] ? targets[i] : mtarget)) {
        gv.conditionsPass = false;
        ++g_bot.kitSkips;
      }
      // KILLFINISH: no Back step away from a won trade (the EngageForKill phases' Back on an enemy shaper close)
      if (gv.conditionsPass && g.guid == kAbilityBotBack && KillFinishing(st, nowMs) && !InFleePhase(v.phaseName)) {
        gv.conditionsPass = false;
        ++g_kf.backSkipped;
        KillFinishHold(o, st, self, nowMs);
      }
      // BOTTOWER: no target under an enemy tower that would shoot the bot (towersafety.h TowerKeepsOut; a dive or a
      // tower's finish aside); the Back gambit and the items always pass
      if (gv.conditionsPass && g_opt.botTowerKeepOut && g.guid != kAbilityBotBack &&
          !gms::agents::SlotIsConsumable(g.slot)) {
        const float* sp = &spots[i * 3];
        const bool hasSpot = sp[0] != 0.f || sp[1] != 0.f;
        const std::uint32_t gt = hasSpot ? 0u : (targets[i] ? targets[i] : (pointOnly[i] ? 0u : mtarget));
        if (TowerKeepsOutTarget(st, self, selfHpFrac, gt, hasSpot ? sp : nullptr, KillFinishing(st, nowMs), nowMs)) {
          gv.conditionsPass = false;
          ++g_tw.keptOutGambits;
        }
      }
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
  // HUNTERROUTE: the plan's own Vanquish (ClearCamp's condition_Bot_VanquishTarget gambit), counted per Hunter
  if (chosen >= 0 && (r == 1 || r == 2)) {
    const int cs = v.gambits[static_cast<std::size_t>(chosen)].slot;
    if (cs >= gms::agents::kSlotSpell1 && cs <= gms::agents::kSlotSpell3 &&
        SehItemNamed(pv.items[gms::agents::SlotInventoryIndex(cs)], "Vanquish")) {
      void* tobj = aim.target ? SehFind(aim.target) : nullptr;
      const int npc = tobj ? SehNpcType(tobj) : -1;
      ObjInfo ti;
      const float thp = tobj && SehObjInfo(tobj, &ti) && ti.hasHp ? ti.hp : -1.f;
      hunterroute::NoteVanquish(self.id, false, r, npc >= 6 && npc <= 8, aim.target, tobj ? SehNounName(tobj) : "none",
                                thp, nowMs);
    }
  }
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
            gms::agents::FallbackAttackerType(SehNpcType(aobj), at.player) && SehHostile(self.team, at.id) &&
            !(TowerKeepsOutTarget(st, self, selfHpFrac, at.id, nullptr, false, nowMs) && ++g_tw.keptOutFallbacks)) {
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
  // HUNTERBOT (the Hunter's idle watchdog, [host] bot_hunter_idle_ms): a Hunter that has stood on the tree's IdleDefault
  // with nothing acting for the window while his mTarget is a living hostile non-player within 20 (a camp creep his
  // plan's gambits passed over; the snapshots of the user's "random idle stands") hits it, walking up on 5; restarts
  // the window either way
  if (g_opt.botHunterIdleMs > 0 && g_opt.botIdleFallbackMs > 0 && !acted && st.idleSinceMs && mtarget &&
      nowMs - st.idleSinceMs >= static_cast<unsigned long long>(g_opt.botHunterIdleMs) &&
      BotRoleOf(self.id) == kRoleHunterIndex) {
    void* tobj = SehFind(mtarget);
    ObjInfo t;
    if (tobj && SehObjInfo(tobj, &t) && t.hasHp && t.hp > 0.f && t.team != self.team && !t.player &&
        Distance2D(t.pos, self.pos) <= 20.f && SehHostile(self.team, mtarget)) {
      ++g_bot.idleHunterTries;
      const int fr = SehBotRequest(0, gms::agents::kSlotBasic, self.id, mtarget, t.pos, 1);
      if (fr == 1 || fr == 2 || fr == 5) {
        ++g_bot.idleHunterActed;
        if (fr == 5) pursue = true;
      }
      if (IdleLogBudget())
        Log("agents: bot %08X %s phase %d (%s) stood %.1f s with %08X (%s) %.1f units away: basic attack -> %d (HUNTERBOT "
            "idle watchdog) (t %.2f s)",
            self.id, st.noun, st.botPhase, v.phaseName, static_cast<double>(nowMs - st.idleSinceMs) / 1000.0, mtarget,
            SehNounName(tobj), static_cast<double>(Distance2D(t.pos, self.pos)), fr, static_cast<double>(nowMs) / 1000.0);
      st.idleSinceMs = nowMs;
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
      "minions before shapers, a shaper that hits a shaper in range kept until it leaves the range (TOWERAGGRO) %d; "
      "a tower's current target first among the non-players (TOWERSTICK) %d",
      g_opt.minionFollow.enabled ? 1 : 0, g_opt.towerTarget.enabled ? 1 : 0, g_opt.towerTarget.stick ? 1 : 0);
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
  g_snapMinions.clear();
  if ((g_opt.botReflex || g_opt.botCampFinish) && g_opt.botBrain) {  // BOTREFLEX, CAMPFINISH: the players' creatures
                                                                       // and the standing towers, once per frame
    for (void* o : objs) {
      ObjInfo i;
      if (!SehObjInfo(o, &i) || !i.hasHp || i.hp <= 0.f) continue;
      const int npcType = i.player ? -1 : SehNpcType(o);
      const bool tower = npcType == kNpcTower;
      if (npcType >= 0 && npcType <= 3) {  // BOTTOWER: a lane minion, what a tower shoots before a shaper
        MinionSnap m;
        m.team = i.team;
        m.pos[0] = i.pos[0];
        m.pos[1] = i.pos[1];
        g_snapMinions.push_back(m);
        continue;
      }
      if (!i.player && !tower && npcType != kNpcObjective) continue;
      Snap sn;
      sn.obj = o;
      sn.id = i.id;
      sn.team = i.team;
      std::memcpy(sn.pos, i.pos, sizeof sn.pos);
      sn.hp = i.hp;
      sn.maxHp = SehMaxHp(o);
      if (!(sn.maxHp > 0.f)) sn.maxHp = i.hp;
      if (tower) {  // BOTTOWER: what it shoots and how far it reaches (the Bindings, the Locus; not the nubs)
        sn.target = SehRawTarget(o);
        AgentInfo ta;
        if (SehAgentInfo(o, &ta) && ta.aggroType == gms::agents::kAggroTower && ta.aggro > 0.f) sn.reach = ta.aggro;
      }
      (tower ? g_snapTowers : npcType == kNpcObjective ? g_snapObjectives : g_snapPlayers).push_back(sn);
      if (i.player) {  // DESECBOT-3: the shapers' motion and recent damage (also KILLFINISH's damage taken)
        ShaperTrack& tr = g_tracks[i.id];
        tr.motion.Note(i.pos, static_cast<long long>(nowMs));
        tr.damage.Note(i.hp, static_cast<long long>(nowMs));
      }
    }
    for (Snap& t : g_snapTowers) {  // BOTTOWER: the other team's lane minions within each tower's reach
      if (!(t.reach > 0.f)) continue;
      for (const MinionSnap& m : g_snapMinions) {
        if (m.team == t.team) continue;
        const float dx = m.pos[0] - t.pos[0], dy = m.pos[1] - t.pos[1];
        if (dx * dx + dy * dy <= t.reach * t.reach) ++t.minionsIn;
      }
    }
    BotTowerFrame(nowMs);  // BOTTOWER: the measurement
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

void OnDamaged(void* victim, void* attacker, unsigned descLo, unsigned descHi, bool lethal, float dealt) {
  if (!g_installed || !g_opt.blackboard || !g_blackboardOk || !victim || !attacker || victim == attacker) return;
  ObjInfo v, at;
  if (!SehObjInfo(victim, &v) || !SehObjInfo(attacker, &at) || v.team == at.team) return;
  if (g_opt.botReflex && at.player && g_sim) {  // KILLFINISH: a shaper's damage rate, and who killed a shaper
    const unsigned long long now = NowMs(g_sim);
    g_tracks[at.id].dealt.Note(dealt, v.player, static_cast<long long>(now));
    if (lethal && v.player) {
      ShaperTrack& tv = g_tracks[v.id];
      tv.killedBy = at.id;
      tv.killedMs = now;
    }
  }
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
        ++g_tw.shotsOnBots;  // BOTTOWER
        if (lethal) ++g_tw.lethalShots;
        if (it->second.towerExp.Shot(at.id, lethal)) ++g_tw.shotsInEpisodes;
      }
    }
  }
  if (g_opt.botDesecKit && v.player && at.player && g_sim) {  // DESECBOT-3: who hit which shaper (the peel, Wither)
    const unsigned long long now = NowMs(g_sim);
    ShaperTrack& tv = g_tracks[v.id];
    tv.lastAttacker = at.id;
    tv.lastAttackerMs = now;
    ShaperTrack& ta = g_tracks[at.id];
    ta.lastVictimTeam = v.team;
    ta.lastVictimMs = now;
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
                  "BOTTEMPO: holds ended on one spot %u, stale targets dropped %u; WELLDEF: well defences started by "
                  "Tacticians %u, Hunters %u, Predators %u, ended: safe %u, low hp %u, outnumbered %u, too slow %u, too "
                  "long %u, own fight %u; the plan's walks ended for one %u, thinks holding at a well %u",
                  g_bot.skipped, g_bot.skippedThenActed, g_bot.pursuitStuck, g_bot.fallbackTries, g_bot.fallbackActed,
                  g_bot.recalls, g_bot.backSteps, g_bot.holdStuck, g_bot.staleTargets, g_bot.wellStarts[0],
                  g_bot.wellStarts[1], g_bot.wellStarts[2], g_bot.wellSafe, g_bot.wellLowHp, g_bot.wellOutnumbered,
                  g_bot.wellSlow, g_bot.wellTooLong, g_bot.wellOwnFight, g_bot.wellWalkTakeovers, g_bot.wellHolds);
}

bool HunterCampFight(std::uint32_t bot, float* pos, float* frac, bool* finishing) {
  return CampFightOf(bot, pos, frac, finishing);
}

bool FormatHunterStats(char* buf, std::size_t size) {
  if (!g_st.brainSteps) return false;
  std::snprintf(buf, size,
                "HUNTERBOT: gambits left out by a kit %u, kit E requests %u (started %u), finisher R requests %u "
                "(started %u), idle Hunter attacks %u (acted %u); DESECBOT: W left out without an enemy shaper near %u, "
                "W started %u (the farthest nearest enemy shaper %.1f); MOYABOT: W left out with the target beyond "
                "reach %u, W started %u (the farthest target %.1f)",
                g_bot.kitSkips, g_bot.kitETries, g_bot.kitEStarted, g_bot.finisherTries, g_bot.finisherStarted,
                g_bot.idleHunterTries, g_bot.idleHunterActed, g_bot.kitWSkips, g_bot.kitWStarted,
                static_cast<double>(g_bot.kitWFarthest), g_bot.kitWFar, g_bot.kitWReachStarted,
                static_cast<double>(g_bot.kitWReachFarthest));
  return true;
}

bool FormatCampFinishStats(char* buf, std::size_t size) {
  if (!g_st.brainSteps) return false;
  using W = gms::agents::CampFinishWhy;
  const unsigned* l = g_bot.campLeft;
  std::snprintf(buf, size,
                "CAMPFINISH (bot_camp_finish %d): Flee links opened in a camp fight %u (camp under 25 %% %u), holds %u "
                "(finished %u, left %u, died %u, other %u), leaves %u (camp under 25 %% %u; would hold %u) by reason: "
                "no camp %u, enemy shaper %u, too low %u, held too long %u, not measured %u, no damage %u, slow kill %u, "
                "losing %u, winning %u, unhurt %u; Hunter deaths in a camp fight %u; fights left otherwise %u (camp under "
                "25 %% %u); finishing a camp first %u (cleared %u)",
                g_opt.botCampFinish ? 1 : 0, g_bot.campEvents, g_bot.campEventsLow, g_bot.campHolds,
                g_bot.campHoldFinished, g_bot.campHoldLeft, g_bot.campHoldDied, g_bot.campHoldOther, g_bot.campLeaves,
                g_bot.campLeavesLow, g_bot.campWouldStay, l[static_cast<int>(W::kNoCamp)],
                l[static_cast<int>(W::kEnemyShaper)], l[static_cast<int>(W::kTooLow)],
                l[static_cast<int>(W::kHeldTooLong)], l[static_cast<int>(W::kNotMeasured)],
                l[static_cast<int>(W::kNoDamage)], l[static_cast<int>(W::kSlowKill)], l[static_cast<int>(W::kLosing)],
                l[static_cast<int>(W::kStayWinning)], l[static_cast<int>(W::kStayUnhurt)], g_bot.campDeaths,
                g_bot.campOther, g_bot.campOtherLow, g_bot.campFinishFights, g_bot.campFinishDone);
  return true;
}

bool FormatDesecStats(char* buf, std::size_t size) {
  const DesecStats& d = g_desec;
  if (!d.eStarted && !d.qHeld && !d.rTries && !d.rGambitsOut && !d.drainTries && !d.witherTries && !d.peelTries)
    return false;
  std::snprintf(buf, size,
                "DESECBOT-3: E started at an enemy shaper %u (led %u, mean lead %.1f units); Q gambits held for the "
                "root %u; combo Q %u (started %u, mean %.2f s after the E), E windows without the combo's Q %u; R gambits "
                "left out %u, R requests %u, corpses started 1st %u 2nd %u 3rd %u (cluster %u, locked %u, spread %u; "
                "the closest spread corpse %.1f from an earlier one), thinks holding a corpse %u; Drain %u (started "
                "%u); Wither %u (started %u: Gladiator %u, bruiser %u), Wither gambits left out %u; peel %u (started "
                "%u); tower dives refused at level 1-3 %u",
                d.eStarted, d.eLed, d.eLed ? d.eLeadSum / d.eLed : 0.0, d.qHeld, d.comboTries, d.comboStarted,
                d.comboStarted ? d.comboDelaySum / d.comboStarted : 0.0, d.comboMissed, d.rGambitsOut, d.rTries,
                d.rStarted[0], d.rStarted[1], d.rStarted[2], d.rCluster, d.rLocked, d.rSpread,
                static_cast<double>(d.rMinSpread), d.rHeld, d.drainTries, d.drainStarted, d.witherTries,
                d.witherStarted, d.witherGlad, d.witherBruiser, d.witherGambitsOut, d.peelTries, d.peelStarted,
                d.earlyNoDive);
  return true;
}

bool FormatMikellaStats(char* buf, std::size_t size) {
  const MikStats& k = g_mik;
  unsigned any = k.eGambitsOut + k.rGambitsOut + k.wGambitsOut + k.wStarted;
  for (int i = 1; i < gms::agents::kMikKinds; ++i) any += k.tries[i];
  if (!any) return false;
  using namespace gms::agents;
  std::snprintf(buf, size,
                "MIKELLABOT: escape E %u (started %u), kite E %u (%u), chase E %u (%u), R %u (%u: assassinate %u, "
                "before or after a fight %u), trade W %u (%u); her Ws started in all %u; the plan's gambits left out: "
                "E %u, R %u, W without an enemy shaper near %u; thinks held for the R's channel %u",
                k.tries[kMikEscapeE], k.started[kMikEscapeE], k.tries[kMikKiteE], k.started[kMikKiteE],
                k.tries[kMikChaseE], k.started[kMikChaseE], k.tries[kMikR], k.started[kMikR], k.rAssassinate, k.rPoke,
                k.tries[kMikTradeW], k.started[kMikTradeW], k.wStarted, k.eGambitsOut, k.rGambitsOut, k.wGambitsOut,
                k.holds);
  return true;
}

bool KillFinishWouldHold(std::uint32_t bot) {
  if (!g_installed || !g_opt.botKillFinish || !g_opt.botReflex || !g_sim || !bot) return false;
  void* o = SehFind(bot);
  const auto it = g_agents.find(bot);
  ObjInfo self;
  if (!o || it == g_agents.end() || !SehObjInfo(o, &self) || self.hp <= 0.f) return false;
  AgentInfo ai;
  const std::uint32_t mtarget = SehAgentInfo(o, &ai) ? SehBestTarget(ai.agent) : 0;
  gms::agents::KillFinishSensors s;
  std::uint32_t target = 0;
  KillFinishSense(o, it->second, self, mtarget, NowMs(g_sim), &s, &target);
  s.heldMs = -1;  // a new engage
  ++g_kf.luaQueries;
  const bool hold = gms::agents::DecideKillFinish(s, kKillFinish).finish;
  if (hold) ++g_kf.luaHolds;
  return hold;
}

bool KillFinishEngageBlocked(std::uint32_t bot) {
  if (!g_installed || !g_sim || !bot) return false;
  const auto it = g_agents.find(bot);
  if (it == g_agents.end()) return false;
  const bool blocked = gms::agents::EngageBlocked(static_cast<long long>(NowMs(g_sim)),
                                                static_cast<long long>(it->second.kfEngageBlockUntilMs));
  if (blocked) ++g_kf.joinsBlocked;
  return blocked;
}

void NoteBailWhy(std::uint32_t bot, const char* why) {
  const auto it = g_agents.find(bot);
  if (it == g_agents.end() || !why || !g_sim) return;
  it->second.bailWhy = why;
  it->second.bailWhyMs = NowMs(g_sim);
}

bool FormatBotTowerStats(char* buf, std::size_t size) {
  const BotTowerStats& w = g_tw;
  const gms::agents::TowerExposureTotals& t = w.totals;
  if (!t.episodes && !w.shotsOnBots && !w.backBy[1] && !w.backBy[2] && !w.backBy[3]) return false;
  std::string paths;
  for (int i = 0; i < gms::agents::kTtPathCount; ++i) {
    char one[48];
    std::snprintf(one, sizeof one, "%s%s %u", i ? ", " : "", gms::agents::TowerThinkPathName(i), t.thinks[i]);
    paths += one;
  }
  std::snprintf(buf, size,
                "BOTTOWER (aware %d, keep-out %d, reflex walk %d): under tower fire: episodes %u, bot-seconds targeted "
                "%.1f, episode mean %.2f s max %.1f s (over 3 s %u), tower shots on bots %u (in episodes %u, lethal %u), "
                "episodes with 2+ shots %u, 3+ %u, deaths in episodes %u, HP lost %.2f (in max HP), entered under 30 %% "
                "HP %u (died %u); thinks while targeted: %s; tower back-offs: a hit %u, targeted %u, no minions %u "
                "(other back-offs %u); steps started %u, own %u (adopted %u), walks ended %u, held and counted as done "
                "%u, refused %u, left to the plan's retreat %u; dive thinks %u; kept out: gambits %u, fights %u, idle "
                "fallbacks %u",
                g_opt.botTowerAware ? 1 : 0, g_opt.botTowerKeepOut ? 1 : 0, g_opt.botReflexWalk ? 1 : 0, t.episodes,
                static_cast<double>(t.targetedMs) / 1000.0,
                t.episodes ? static_cast<double>(t.durationMs) / 1000.0 / t.episodes : 0.0,
                static_cast<double>(t.maxDurationMs) / 1000.0, t.over3s, w.shotsOnBots, w.shotsInEpisodes,
                w.lethalShots, t.shots2, t.shots3, t.died, t.hpLost, t.lowEntries, t.lowDeaths, paths.c_str(),
                w.backBy[1], w.backBy[2], w.backBy[3], w.otherBacks, w.stepsStarted, w.stepsOwn, w.stepsAdopted, w.walksEnded, w.stepsBlind,
                w.stepsRefused, w.planRetreats, w.dives, w.keptOutGambits, w.keptOutFights, w.keptOutFallbacks);
  return true;
}

bool FormatBotTowerBots(char* buf, std::size_t size) {
  if (g_twBots.empty()) return false;
  std::string out;
  for (const auto& kv : g_twBots) {
    char one[128];
    std::snprintf(one, sizeof one, "%s%08X %s %.1f s, %u episodes, %u shots, %u died", out.empty() ? "" : "; ",
                  kv.first, kv.second.noun, static_cast<double>(kv.second.targetedMs) / 1000.0, kv.second.episodes,
                  kv.second.shots, kv.second.died);
    out += one;
  }
  std::snprintf(buf, size, "%s", out.c_str());
  return true;
}

bool FormatKillFinishStats(char* buf, std::size_t size) {
  const KillFinishStats& k = g_kf;
  if (!k.thinks && !k.luaQueries && !g_bot.passThroughs && !k.dwellHeld && !k.joins) return false;
  std::string why;
  for (int i = 0; i < gms::agents::kKfWhyCount; ++i) {
    if (!k.letGoWhy[i]) continue;
    char one[64];
    std::snprintf(one, sizeof one, "%s%s %u", why.empty() ? "" : ", ",
                  i == gms::agents::kKfFinish ? "would hold (off)" : gms::agents::KillFinishWhyName(i), k.letGoWhy[i]);
    why += one;
  }
  std::snprintf(buf, size,
                "KILLFINISH %s: engage thinks %u (finish %u); exits passed %u (low target %u): held %u (engages held %u, "
                "mean ttk %.2f s), let go %u (low target %u: %s); held engages ended: killed %u, another's kill %u, the "
                "bot died %u, the target lived %u, then let go %u; let go on a low target: the bot's kill later %u, "
                "another's %u, the bot died %u, the target lived %u; tower dives %u, Back steps left out %u, "
                "FleeTowerTargeting left out %u, retargets %u; Lua asked %u (yes %u); BOTCYCLE: plans passed through "
                "%u%s, stance flips held %u, engages left at once %u, KillShaper joins %u (left at once %u), engages "
                "refused while blocked %u",
                g_opt.botKillFinish ? "on" : "off (logged only)", k.thinks, k.finishThinks, k.exits, k.exitsLow,
                k.heldThinks, k.holds, k.holds ? k.ttkSum / k.holds : 0.0, k.letGo, k.letGoLow,
                why.empty() ? "none" : why.c_str(), k.heldKill, k.heldKillOther, k.heldBotDied, k.heldSurvived,
                k.heldThenLetGo, k.lowKill, k.lowKillOther, k.lowBotDied, k.lowSurvived, k.reflexDives, k.backSkipped,
                k.towerPhaseSkipped, k.retargets, k.luaQueries, k.luaHolds, g_bot.passThroughs,
                g_opt.botPlanChain ? "" : " (chain off)", k.dwellHeld, k.engagesFailed, k.joins, k.joinsFailed,
                k.joinsBlocked);
  return true;
}

bool FormatLostStats(char* buf, std::size_t size) {
  if (!g_bot.thinks) return false;
  int now = 0;
  double nowLongest = 0.0;
  for (const auto& kv : g_agents) {
    const AgentState& a = kv.second;
    if (!a.centreStandSinceMs) continue;
    ++now;
    const double s = static_cast<double>(a.centreStandLastMs - a.centreStandSinceMs) / 1000.0;
    if (s > nowLongest) nowLongest = s;
  }
  std::snprintf(buf, size,
                "BACKTOACTION: point-follow fix %s, net %s; Pursue restarted after a point pursuit %u, with nothing to "
                "follow %u; lost on the centre: walks to the lane front %u (started %u, no front %u); bots within %.0f "
                "of the centre without a target now %d (longest now %.1f s), longest stand %.1f s (in a parasite task "
                "%.1f s), bot-seconds %.1f",
                g_opt.botPointFollowFix ? "on" : "off", g_opt.botLostMs > 0 ? "on" : "off", g_bot.pointFollowRestarts,
                g_bot.noTargetRestarts, g_bot.lostWalks, g_bot.lostWalksStarted, g_bot.lostNoFront,
                static_cast<double>(kLostStandRadius), now, nowLongest, g_bot.lostLongestS, g_bot.lostLongestParasiteS,
                g_bot.lostCentreSeconds);
  return true;
}

bool FormatNullPosStats(char* buf, std::size_t size) {
  if (!g_bot.thinks && !g_nullpos.total) return false;
  gms::agents::FormatNullPosStats(buf, size, g_nullpos, g_opt.botNullPosStrict);
  return true;
}

void ReportNullPos(std::uint32_t object, int kind, const char* who, const char* where) {
  ReportNullPosImpl(object, kind, who, where);
}

void NoteFollowPointRead() { ++g_nullpos.followReads; }

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
  if (!g_installed || !size || (!g_st.towerUpdates && !g_st.towerWatchedUpdates)) return false;
  // TOWERSTICK: the rules that broke a tower's hold on a living non-player, the non-zero ones
  std::string byRule;
  for (int r = 0; r < gms::agents::kAggroRuleCount; ++r)
    if (g_st.towerBrokenByRule[r]) {
      char part[64];
      std::snprintf(part, sizeof part, "%s%s %u", byRule.empty() ? "" : ", ", gms::agents::AggroRuleName(r),
                    g_st.towerBrokenByRule[r]);
      byRule += part;
    }
  std::snprintf(buf, size,
                "rule %s, stick %s: updates %u; shaper-on-shaper hits %u; defend locks %u, released: left the range "
                "%u, left the list %u; a shaper as the best target with a non-player on the list: defended or "
                "taunting %u, other %u; target changes %u: the old one gone %u, the old one on the list %u (for a "
                "shaper %u), a living non-player left for another non-player %u%s%s%s",
                g_opt.towerTarget.enabled ? "on" : "off",
                g_opt.towerTarget.enabled && g_opt.towerTarget.stick ? "on" : "off",
                g_st.towerWatchedUpdates, g_st.towerHits, g_st.towerLocks, g_st.towerReleasedRange,
                g_st.towerReleasedList, g_st.towerShaperDefended, g_st.towerShaperOther, g_st.towerChanges,
                g_st.towerOldGone, g_st.towerOffLiving, g_st.towerOffForShaper, g_st.towerStickBroken,
                byRule.empty() ? "" : " (", byRule.c_str(), byRule.empty() ? "" : ")");
  return true;
}

}  // namespace agents
