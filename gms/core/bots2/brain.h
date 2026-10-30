// gms/core/bots2 (BOTS2): the second bot brain's decision layer, engine-free. One team plan per team and one intent
// per living bot per think, from the World snapshot (world.h) and the tuning table (tuning.h) alone; the host
// (gms/engine/bots2.cpp) executes the intent with the engine glue the classic bots use (requests, the follow leaf, the
// recall, the shop visit).
//
// The states, checked in this order at every think (the first that applies picks the intent):
//   Retreat  HP below retreatHp, or outnumbered and below retreatOutnumberedHp; left at retreatExitHp or in the base
//            (hysteresis). Walks home; recalls once no seen enemy shaper is within recallSafeRange.
//   Recall   the recall channel runs: nothing else is asked.
//   Base     in the own base: to the shop, stand shopStandS (the host's shop visit buys), heal to leaveBaseHp.
//   Fight    a seen enemy shaper within engageRange with the odds for it (or one that just hit the bot); the target is
//            kept fightCommitS at least and until it dies, is lost from sight, runs chaseRange away or into its
//            structure's reach (unless low: a dive). BOTS2c, the laning phase (before the team pushes): only a short
//            trade (tradeRange, tradeS, every tradeGapS, then a step back), a kill chance (killOdds with the target at
//            killHp, or gankOdds) or an answer to a hit; a hurt bot with a potion heals at its Binding, recalls below
//            recallHp; the lane's bots stand apart (LaneSlotOf), every long walk is spread (walkSpread).
//   Recall   (for gold) recallGold with HP below recallGoldHp, or recallGoldAlways, nobody near.
//   Defend   the team's defenders while its base is threatened.
//   Well     (BOTS2c) a defender of an own well under capture; the team's well task: retake an own-side well, take an
//            enemy well or raid its workers (the well taker: the Predator, else the Hunter).
//   Parasite (BOTS2c) the team's parasite attempt (from parasiteFromS, after a won fight): gather, then hit it together.
//   Jungle   the Hunter before hunterJoinS: the best camp (own side first).
//   Lane     the bot's lane before groupAtS, the team's push lane after (Push): follow the own front, farm the enemy
//            minions, hit enemy structures when it is safe (own minions tank it, or siegeAllies bots together).
// The never-idle rule: a living bot outside its base always has a Move, an Attack or a Recall (IntentIdle).
// BOTS2c's extras: besides its intent a bot may cast one battle spell, drink a potion or place a ward per think
// (Intent::extra); the intent itself never changes for an extra (a ward's detour aside, which is a Move).
#pragma once

#include <cstdint>
#include <cstdio>
#include <cstring>
#include <map>
#include <vector>

#include "core/bots2/tuning.h"
#include "core/bots2/world.h"

namespace gms::bots2 {

enum class Act : int { kNone, kMove, kAttack, kRecall, kShop, kCount };
enum class State : int {
  kDead, kRetreat, kRecall, kBase, kFight, kDefend, kWell, kParasite, kJungle, kLane, kPush, kCount
};
const char* StateName(State s);
const char* ActName(Act a);

// BOTS2c: the gambit slots of the extras (core/agents/brain.h GambitSlot: 1 ward, 7-9 spell1-3, 10-11 consume1-2).
constexpr int kExtraSlotWard = 1;
constexpr int kExtraSlotSpell1 = 7;
constexpr int kExtraSlotConsume1 = 10;

// BOTS2c: one extra besides the intent: a battle spell (gambit slot 7-9), a potion (10-11) or a ward (1).
enum class ExtraKind : int { kNone, kSpell, kPotion, kWard, kCount };
const char* ExtraName(ExtraKind k);
struct Extra {
  ExtraKind kind = ExtraKind::kNone;
  int slot = -1;
  Spell spell = Spell::kNone;
  std::uint32_t target = 0;  // a spell's object (an enemy shaper, an NPC, a structure); 0: a self cast or a point
  bool atPoint = false;      // aimed at `point` (the ward's spot, Blink's, Deflect's)
  Vec3 point;
  const char* why = "";
};

struct Intent {
  Act act = Act::kNone;
  Vec3 point;                // kMove: where to
  std::uint32_t target = 0;  // kAttack: whom
  bool abilities = false;    // kAttack: the kit's Q, W, E as well as the basic attack
  bool ult = false;          // kAttack: the R as well
  const char* why = "";      // a short reason (the log)
  Extra extra;               // BOTS2c
};

// Per bot, between thinks.
struct Memory {
  State state = State::kBase;
  double stateSince = -1e9;
  bool retreating = false;   // low HP: home and recall (latched until retreatExitHp)
  bool disengaging = false;  // outnumbered: back toward home (until the odds reach disengageExitOdds)
  double disengageSince = -1e9;
  bool escaping = false;     // stepping out of an enemy structure's fire
  std::uint32_t fightTarget = 0;  // the shaper fought
  double fightSince = -1e9;
  Vec3 engagePoint;
  double targetSeenAt = -1e9;
  std::uint32_t farmTarget = 0;   // the minion, creature or structure hit outside a fight
  double farmSince = -1e9;
  std::uint32_t hitBy = 0;        // the last enemy shaper that hit the bot, and when
  double hitByAt = -1e9;
  double hitAt = -1e9;            // any damage taken (a recall breaks on it)
  double recallAt = -1e9;         // the last recall asked
  double shopSince = -1.0;        // standing in the shop since (-1: not yet)
  bool shopDone = false;
  bool wasInBase = false;
  // BOTS2c, the laning phase: the fight's kind (a short trade or a fight to the kill), the last trade's end, and until
  // when the bot stands further back (after a trade, or hit at bad odds)
  enum FightMode : int { kFightNone = 0, kFightTrade = 1, kFightKill = 2 };
  int fightMode = kFightNone;
  double tradeEndAt = -1e9;
  double backOffUntil = -1e9;
  bool jungleStaying = false;  // a hurt Hunter clearing on with a potion (counted once a stay)
  bool waveWaited = false;     // waited in its base for its lane's first wave (counted once)
  // BOTS2c: when each extra may be asked again (a cast's cooldown, a refusal's retry), and the last well task's end
  double spellReadyAt[3] = {-1e9, -1e9, -1e9};
  double potionReadyAt = -1e9;
  double wardReadyAt = -1e9;
  double wellDoneAt = -1e9;
};

// Round 8: why a team's plan started no parasite attempt this think (counted in BrainStats::parasiteNo).
enum class ParasiteNo : int { kNone = 0, kEarly, kNoParasite, kRetry, kNoWonFight, kSiege, kTooFewNear, kCount };
inline const char* ParasiteNoName(ParasiteNo n) {
  switch (n) {
    case ParasiteNo::kEarly: return "before parasiteFromS";
    case ParasiteNo::kNoParasite: return "no parasite to hit";
    case ParasiteNo::kRetry: return "the retry wait";
    case ParasiteNo::kNoWonFight: return "no won fight";
    case ParasiteNo::kSiege: return "the base's siege";
    case ParasiteNo::kTooFewNear: return "too few near";
    default: return "-";
  }
}

struct TeamPlan {
  bool push = false;
  bool advantage = false;  // advantageLead more living shapers than the enemy
  int pushLane = 0;
  double pushLaneSince = -1e9;
  // the push's objective (the next enemy structure: the push lane's Bindings from the own side, then the Cores, then
  // the Guardian), the rally point before it, and whether the group is together there (with hysteresis)
  std::uint32_t objective = 0;
  Vec3 objectivePos;
  Vec3 rally;
  bool grouped = false;
  bool siege = false;  // BOTS2c round 6: grouped at a Core or the Guardian (the base's siege: it holds)
  bool siegeEdge = false;  // round 7: ... with an edge (a numbers advantage, or own minions at the objective)
  double groupedSince = -1e9;
  int groupCount = 0, groupNeeded = 0;
  std::vector<std::uint32_t> pushGroup;  // round 8: the bots the push takes for the hybrid (PlanObjective)
  bool rallySafe = false;  // round 8: the own wave at or past the rally point (the base: the siege's edge)
  // Round 11: the Guardian phase (the user's rule, 2026-10-29: "if the guardian is 'open', the team needs to group up
  // and push as a unit. If shapers are just 'tickling in' one by one, nothing happens"): from the enemy's last Core on,
  // every living bot gathers at a staging point outside the defences' reach, they go in together once the group is
  // there, and they back off together when outnumbered or half of them are down (PlanGuardian, GuardianIntent).
  enum GuardStage : int { kGuardOff = 0, kGuardGather = 1, kGuardEngage = 2 };
  int guard = kGuardOff;
  std::uint32_t guardTarget = 0;  // the Core the group goes for (round 12: from the first), then the Guardian
  Vec3 staging;
  double guardSince = -1e9;       // the stage's start
  double guardReadyAt = -1e9;     // when guardMinBots first stood at the staging point (the stragglers' wait)
  double guardBackOffAt = -1e9;   // the last back-off (no engage for guardRetryS)
  std::vector<std::uint32_t> guardMembers;  // the bots that went in together
  int guardEngaged = 0;           // ... how many at the engage
  int guardCount = 0, guardNeeded = 0;  // at the staging point, living
  const char* guardWhy = "";      // the last change's reason (for the host's log)
  bool threatened = false;
  double threatSeenAt = -1e9;
  Vec3 threat;  // the own structure under threat
  std::vector<std::uint32_t> defenders;

  // BOTS2c: the team's well task (one bot at a time: a capture or a raid) ...
  enum WellKind : int { kWellNone = 0, kWellRetake = 1, kWellTake = 2, kWellRaid = 3 };
  std::uint32_t wellBot = 0, wellTarget = 0;
  int wellKind = kWellNone;
  double wellSince = -1e9;
  const char* wellEndWhy = "";  // why the last task ended
  // ... and an own well under capture (or its workers under an enemy shaper's fire) with its defenders
  std::uint32_t wellThreat = 0;
  double wellThreatSeenAt = -1e9;
  std::vector<std::uint32_t> wellDefenders;
  char wellNoDefenderWhy[96] = "";  // why no bot defends it (the team's bots by reason), for the host's log
  // BOTS2c: the parasite attempt (a team objective like the push; from parasiteFromS, after a won fight)
  bool parasite = false;
  double parasiteSince = -1e9, parasiteEndedAt = -1e9;
  std::uint32_t parasiteId = 0;
  bool parasiteGrouped = false;
  int parasiteCount = 0, parasiteNeeded = 0;
  const char* parasiteEndWhy = "";
};

struct BrainStats {
  unsigned decisions = 0;
  unsigned byState[static_cast<int>(State::kCount)] = {};
  unsigned byAct[static_cast<int>(Act::kCount)] = {};
  unsigned idleViolations = 0;
  unsigned engages = 0, retreats = 0, recalls = 0, pushLaneChanges = 0, threats = 0;
  unsigned groupedOn = 0, objectives = 0, advantages = 0;  // the groups formed, the objectives set, the advantage pushes
  unsigned towerEscapes = 0;  // steps out of an enemy structure's fire
  // BOTS2c: the extras asked (by kind) and their results as the host reported them; the well tasks and defences, the
  // parasite attempts
  unsigned extras[static_cast<int>(ExtraKind::kCount)] = {};
  unsigned extrasStarted[static_cast<int>(ExtraKind::kCount)] = {};
  unsigned spellsStarted[static_cast<int>(Spell::kCount)] = {};
  unsigned wardDetours = 0;
  unsigned wellTasks[4] = {};  // by PlanTeams' kind: [1] retake, [2] take, [3] raid
  unsigned wellThreats = 0;
  unsigned parasiteStarts = 0, parasiteDowns = 0, parasiteGiveUps = 0, parasiteGroups = 0;
  // round 8: the thinks a team's plan started no parasite attempt, by reason (ParasiteNo)
  unsigned parasiteNo[static_cast<int>(ParasiteNo::kCount)] = {};
  // round 11: the Guardian phase: phases started, engages (with the bots each had), back-offs
  unsigned guardPhases = 0, guardEngages = 0, guardEngageBots = 0, guardBackOffs = 0;
  // BOTS2c, the laning phase: trades and fights to the kill started, backing off from a hit at bad odds, retreats that
  // stayed in lane (a potion) instead of recalling
  unsigned trades = 0, killLow = 0, killGanks = 0, tradeToKill = 0, backOffs = 0, laneStays = 0;
  unsigned jungleStays = 0, firstWaveWaits = 0;  // a hurt Hunter clearing on with a potion; a bot waiting in base
};

// BOTS2c, head to head (the user: "Imagine judging a chess bot by how well it plays against itself"): the brain of
// each team from [host] bot_ai (both teams) and bot_ai_team1 / bot_ai_team2 (one team; "" = bot_ai's). "v2" is the
// second brain, anything else the classic one.
// BOTS2c round 7 (the user's decision after round 6): "hybrid" = the classic brain's micro (lanes, trades, fights,
// retreats, recalls, the jungle) with v2's macro on top (the team plan's push, group and finish, the wells, the parasite,
// the extras while v2 drives, the never-idle rescue): per bot and think ChooseDriver picks who drives it.
enum BrainKind : int { kBrainClassic = 0, kBrainV2 = 1, kBrainHybrid = 2 };
inline const char* BrainKindName(int k) { return k == kBrainV2 ? "v2" : k == kBrainHybrid ? "hybrid" : "classic"; }
struct BrainChoice {
  bool v2[3] = {false, false, false};  // [team]: the second brain plans the team (v2 or hybrid)
  int kind[3] = {kBrainClassic, kBrainClassic, kBrainClassic};
  bool Any() const { return v2[1] || v2[2]; }
  bool Mixed() const { return kind[1] != kind[2]; }
  bool Hybrid(int team) const { return (team == 1 || team == 2) && kind[team] == kBrainHybrid; }
  // "v2", "classic", "hybrid", or "t1<a>_t2<b>" (one word: the census line and the scan read it)
  const char* Label() const {
    static char buf[3][3][32];
    if (!Mixed()) return BrainKindName(kind[1]);
    char* b = buf[kind[1]][kind[2]];
    std::snprintf(b, sizeof buf[0][0], "t1%s_t2%s", BrainKindName(kind[1]), BrainKindName(kind[2]));
    return b;
  }
};
inline BrainChoice ChooseBrains(const char* all, const char* team1, const char* team2) {
  auto kindOf = [](const char* s) {
    if (s && std::strcmp(s, "v2") == 0) return kBrainV2;
    if (s && std::strcmp(s, "hybrid") == 0) return kBrainHybrid;
    return kBrainClassic;
  };
  BrainChoice c;
  const int def = kindOf(all);
  c.kind[1] = team1 && team1[0] ? kindOf(team1) : def;
  c.kind[2] = team2 && team2[0] ? kindOf(team2) : def;
  for (int t = 1; t <= 2; ++t) c.v2[t] = c.kind[t] != kBrainClassic;
  return c;
}

// BOTS2c round 7: who drives a hybrid bot this think. v2 takes it for the team's macro (the push and its group and
// siege, a well task or defence, a parasite attempt) when the bot is calm (alive, out of its base, no enemy shaper the
// team sees within hybridFightRange, HP at retreatHp or more), and for the never-idle rescue (the classic brain left it
// hybridIdleS without a move or a hit); everything else is the classic brain's: its laning, trades, fights, retreats,
// recalls and jungle. A takeover holds hybridHoldS at least, unless an enemy comes near or the bot gets hurt.
// Round 8: a takeover for a task also needs hybridCalmS without an enemy shaper within hybridCalmRange (round 7's bots
// flipped between the brains several times a second at the 30-unit line); a driven bot keeps going until one comes
// within hybridFightRange (hysteresis). The push's task is only its push group's (TeamPlan::pushGroup), and only when
// it is safe or needed (the coordinator's round-7 reading: v2's tasks walked calm bots into the enemy half, met
// enemies there and handed them to the classic brain in a bad spot): grouped, the advantage, or the walk behind the
// own wave (TeamPlan::rallySafe); of the wells only an own-side retake and a defence; and a task an enemy ended
// isn't taken again for hybridTaskBackoffS. Round 9: the base's siege with its edge is the exception: v2 keeps its
// group there with enemies near (round 8: the hybrid had the enemy down to one or two Cores in three matches, the
// classic brain drove its bots at the base, and the Cores regrew; it finished none of them).
struct HybridView {
  bool alive = false, inBase = false;
  float hp = 1.f;                 // HP fraction
  float nearestEnemy = 1.0e30f;   // the nearest enemy shaper its team sees
  double calmS = 1.0e9;           // round 8: seconds since an enemy shaper was last within hybridCalmRange
  bool teamTask = false;          // a safe or needed task: the push group's push while grouped, with the advantage
                                  // or behind the own wave; an own-side well's retake or defence; the parasite attempt
  bool backoff = false;           // round 8: a task ended by an enemy coming near less than hybridTaskBackoffS ago
  bool finish = false;            // round 9: in the push group of the base's siege with its edge (TeamPlan::siege,
                                  // siegeEdge): v2 keeps it with enemies near, unless it is hurt
  double idleRunS = 0.0;          // the census's current idle stretch
  bool driven = false;            // v2 drives it now ...
  double drivenSince = -1e9;      // ... since
  double now = 0.0;
};
enum class HybridDriver : int { kClassic = 0, kV2Task = 1, kV2Rescue = 2 };
inline HybridDriver ChooseDriver(const HybridView& v, const Tuning& t) {
  if (!v.alive || v.inBase) return HybridDriver::kClassic;
  if (v.finish && v.hp >= t.retreatHp) return HybridDriver::kV2Task;  // round 9: the finish commits
  if (v.nearestEnemy <= t.hybridFightRange || v.hp < t.retreatHp) return HybridDriver::kClassic;  // its fight, its retreat
  if (v.idleRunS >= t.hybridIdleS) return HybridDriver::kV2Rescue;  // the classic brain left it standing
  const bool settled = v.driven || v.calmS >= t.hybridCalmS;
  if (v.teamTask && !v.backoff && settled) return HybridDriver::kV2Task;
  if (v.driven && v.now - v.drivenSince < t.hybridHoldS) return HybridDriver::kV2Task;
  return HybridDriver::kClassic;
}

// The never-idle rule: true when the intent leaves a living bot outside its base without a move, attack or recall.
inline bool IntentIdle(const Intent& in, bool alive, bool inBase) {
  if (!alive || inBase) return false;
  return in.act == Act::kNone || in.act == Act::kShop;
}

// Helpers (exposed for the tests).
bool InBase(const World& w, const Shaper& s, const Tuning& t);
float LevelWeight(int level);
// The own side's strength around `me` against the enemy's around `target` (see brain.cpp); `target` null: the enemies
// around `me`. A large number when the enemy side is empty.
float FightOdds(const World& w, const Shaper& me, const Shaper* target, const Tuning& t);
// An enemy (of `team`) structure that shoots and reaches `p` (its reach plus `margin`); null if none.
const Structure* EnemyStructureCovering(const World& w, int team, const Vec3& p, float margin);
// Whether `me` may hit enemy structure `s` now: it doesn't shoot, or it doesn't shoot `me` and own minions tank it or
// siegeAllies bots stand together.
bool StructureSafe(const World& w, const Shaper& me, const Structure& s, const Tuning& t);
// The jungle's camps: the creatures grouped by distance (`join`), with their centre.
struct Camp {
  Vec3 center;
  std::vector<std::uint32_t> members;
};
std::vector<Camp> Camps(const World& w, float join);
// A point moved out of the enemy Locus's keep-out circle (unchanged when outside it or without a Locus).
Vec3 AwayFromLocus(const World& w, int team, const Vec3& p, const Tuning& t);
// BOTS2c: whether `p` lies on `team`'s half of the map (nearer its spawn than the enemy's).
bool OwnSide(const World& w, int team, const Vec3& p);
// BOTS2c: the census's "capturing or contesting": `s` stands in the ring of an unlocked well the enemy owns, or of an
// own well with capturers in it (an action, not an idle second).
bool InWellAction(const World& w, const Shaper& s);
// BOTS2c: the team's well taker: its living Predator bot, else its living Hunter bot; 0 if none.
std::uint32_t WellTaker(const World& w, int team);
// BOTS2c: a bot's own spot among its team's living bots on its lane, by role (the Gladiator first) and id: the first
// stands laneCarryBack behind its minions on side +1, the second laneSupportBack on side -1, a third further back on
// side +1 again; so two bots of a lane never share a point, whatever their roles.
struct LaneSlot {
  float side = 1.f;
  float back = 0.f;
  int rank = 0;
};
LaneSlot LaneSlotOf(const World& w, const Shaper& me, const Tuning& t);
// BOTS2c: any bot's own side (+1 or -1, by its id) for a long walk's goal.
float WalkSideOf(std::uint32_t id);
// BOTS2c: `p` moved `by` to the `side` of the direction `dir` (2-D, unit or not); `p` when `dir` is zero or the result
// would be the null position.
Vec3 Beside(const Vec3& p, const Vec3& dir, float side, float by);

class Brain {
 public:
  explicit Brain(const Tuning& t = Tuning()) : t_(t) {}
  const Tuning& tuning() const { return t_; }
  void SetTuning(const Tuning& t) { t_ = t; }

  // Once per think round, before the bots' Decide: each team's plan (push, push lane, threat, defenders).
  void PlanTeams(const World& w);
  // BOTS2c, head to head: plan only the teams this brain drives (both by default).
  void SetTeams(bool team1, bool team2) {
    teams_[1] = team1;
    teams_[2] = team2;
  }
  // The bot's intent for this think (its memory is updated).
  Intent Decide(const World& w, std::uint32_t bot);

  // Events from the host: damage taken by a bot (attacker a shaper or not).
  void NoteDamage(std::uint32_t victim, std::uint32_t attacker, bool attackerIsShaper, double now);
  // BOTS2c: what the host's request of an extra gave (1, 2 started; a potion's 1 used; anything else refused): the
  // spell's cooldown, the potion's heal or the ward's cooldown starts, or a retry is set.
  void NoteExtra(std::uint32_t bot, const Extra& e, int result, double now);

  const Memory* MemoryOf(std::uint32_t bot) const;
  const TeamPlan& Plan(int team) const { return plans_[team == 2 ? 2 : 1]; }
  const BrainStats& Stats() const { return stats_; }
  void Forget(std::uint32_t bot) { mem_.erase(bot); }
  // BOTS2c round 7, the hybrid: v2 takes over a bot from the classic brain; what v2 held for it (a fight, a retreat, a
  // disengage, a farm target, a trade's back-off) is from before and is dropped.
  void ResetTactics(std::uint32_t bot) {
    const auto it = mem_.find(bot);
    if (it == mem_.end()) return;
    Memory& m = it->second;
    m.retreating = m.disengaging = m.escaping = false;
    m.fightTarget = m.farmTarget = 0;
    m.fightMode = Memory::kFightNone;
    m.backOffUntil = -1e9;
  }
  // Round 12: the way to `dest` past the enemy's living Bindings (by the push lane when the straight line passes one)
  Vec3 RouteTo(const World& w, const Shaper& me, const Vec3& dest, int pushLane) const;

 private:
  Intent Finish(Memory& m, State s, const Intent& in, double now, bool inBase, bool alive = true);
  // BOTS2c: Finish with the extras added (every state of a living bot outside the base and the recall)
  Intent Done(const World& w, const Shaper& me, Memory& m, State s, Intent in, bool inBase);
  const Shaper* FightTarget(const World& w, const Shaper& me, Memory& m);
  bool WantsGoldRecall(const World& w, const Shaper& me, const Memory& m) const;
  bool RecallAllowed(const World& w, const Shaper& me, const Memory& m) const;
  bool DefendIntent(const World& w, const Shaper& me, Memory& m, Intent* out);
  bool JungleIntent(const World& w, const Shaper& me, Memory& m, Intent* out);
  Intent LaneIntent(const World& w, const Shaper& me, Memory& m, int lane, bool push);
  bool PushIntent(const World& w, const Shaper& me, Memory& m, Intent* out);
  void PlanObjective(const World& w, int team, TeamPlan& p);
  bool KeepFarm(const World& w, const Shaper& me, const Memory& m, float range) const;
  // BOTS2c
  bool LanePhase(int team) const { return !Plan(team).push; }
  int KillChance(const Shaper& me, const Shaper& target, float odds) const;
  Vec3 LaneStand(const World& w, const Shaper& me, const Memory& m, const gms::bots::LanePath& lane) const;
  bool LaneSafePoint(const World& w, const Shaper& me, Vec3* out) const;
  void PlanWells(const World& w, int team, TeamPlan& p);
  void PlanParasite(const World& w, int team, TeamPlan& p);
  void PlanGuardian(const World& w, int team, TeamPlan& p);  // round 11
  bool GuardianIntent(const World& w, const Shaper& me, Memory& m, Intent* out);
  bool WellIntent(const World& w, const Shaper& me, Memory& m, Intent* out);
  bool ParasiteIntent(const World& w, const Shaper& me, Memory& m, Intent* out);
  bool OnTeamDuty(const TeamPlan& p, std::uint32_t bot) const;  // a well task or well defence: not in a group
  bool SpellReady(const Memory& m, const Shaper& me, Spell s, int* slot, double now) const;
  Extra SpellAt(const Shaper& me, int slot, Spell s, std::uint32_t target, const char* why) const;
  Extra FightSpell(const World& w, const Shaper& me, const Memory& m, const Shaper& target) const;
  Extra RetreatSpell(const World& w, const Shaper& me, const Memory& m) const;
  Extra VanquishAt(const World& w, const Shaper& me, const Memory& m, std::uint32_t target, bool jungle) const;
  void AddExtras(const World& w, const Shaper& me, Memory& m, State s, Intent& in);

  Tuning t_;
  bool teams_[3] = {false, true, true};
  std::map<std::uint32_t, Memory> mem_;
  TeamPlan plans_[3];
  BrainStats stats_;
};

}  // namespace gms::bots2
