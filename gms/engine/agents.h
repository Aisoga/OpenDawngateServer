// Agent AI per frame (roadmap D1b-live; docs/AGENT_AI.md). What Dawngate.exe lacks to make its agents act, written
// here, run once per server tick after the command intake (and, with D1c-live, the director) and before the Lua
// scheduler (AGENT_AI.md §5). Per agent, in this order:
//   step 2 (§6.2-§6.4), every 500 ms per agent (staggered by the init's random +0x0C; at once after
//     nAgent.ForceAggroListUpdate): the blackboard update. The perception list (+0x10) from the perception circle
//     (AgentBlackboard_GetPerceptionCircle 0x008AA4A0) and the spatial query (GameObjects_GetInRadius 0x008820B0),
//     nearest first by edge distance; for AI agents with an NPC class the aggro list (+0x430): entries that are gone,
//     dead, no valid hostile target, untargetable or unattackable, invisible, beyond dropAggroRange or out of the tether
//     are removed (taunts hold), perceived enemies within aggroRange are added (Agent_AddToAggroList 0x008AB710), and the
//     list is ordered by gms/core/agents/aggro.h (the targeting priority: AgentBlackboard_GetBestTarget 0x008AA270
//     takes the first valid entry); the engaged / in-combat flags (+0x1E71/+0x1E70/+0x1E72), battle ready (+0x1E73) for
//     players; OnBestTargetChanged (62, bestTargetNew) when the best target changes, OnEnter/ExitBattleReady (63/64),
//     through AbilityDataContainer_FireEvent (0x009064E0). The damage hook (OnDamaged) adds the attacker to the victim's
//     own list and calls Agent_CallForHelp (0x008AB860). With the same period the ids of ended ability and modifier
//     instances are dropped from the agent's container lists (object +0x8A8, +0x08 and +0x9C), which nothing in the
//     binary prunes any more (a minion's 33rd attack asserted). Without fog of war ([host] fog=0) every agent's
//     mVisibleToTeam (+0x888) gets all three team bits each frame (LineOfSight_AddObject narrows it to the own team; the
//     enemy pass is stripped); with it (the default since D2g-live step 2) the host's vision model writes it.
//   step 3 (§6.5), every frame (100 ms after a step that chose nothing): the brain step for AI agents with an AI
//     definition. The current node's gambits (conditions through AIConditions_Evaluate 0x008AE9B0, chosen by phase type
//     with gms/core/agents/brain.h), the chosen gambit's ability requested on the condition's target or the best target
//     (LabsAbilities_RequestAbility 0x0090A670, rank npcRank, noTick: the tree tick below runs it), the gambit cooldown,
//     tree flag 8 (pursue, leaf 12) on "out of range" (5), flag 0x40 (phase default), then the node's links.
//   step 1 (§6.1): AgentBehaviorTree_Tick (0x008A9060, cdecl (object, f32 dt)) for every object with behavior tree data
//     (+0x89C) that isn't pending delete (+0x55). Its per-frame caller is stripped; it is the only scheduler of the AI's
//     Lua behaviors (nBehavior_Assault, _Defend, _Death, _CombatIdle, _Follow), and its leaf 1 starts
//     AIDefinition.deathAbility. A tree is ticked at most once per frame: the command intake ticks the players' creatures
//     right after their command queues (TickTree), and the agent loop skips them then. Leaf changes are logged (the
//     decider state's active leaf record, tree data +0x2DC), deaths are followed until the object is deleted.
// Everything runs on the engine thread with the server simulator bound; every engine call is under SEH.
// Settings: [dgsim] tick_agents (1; D1 harness), [host] agents (default tick_agents), agent_blackboard (1),
// agent_all_visible (default !fog: every agent visible to every team; D2g-live step 2's fog writes the real answer),
// agent_brain (1), agent_log_limit (300).
// D5: AddBaseStructures, the engine side of nBattlegrounds.AddBaseStructuresToAggroList (marked entries are kept
// beyond the drop range and the tether).
// D6a: the bot brain step (docs/AGENT_AI.md *D6a*) for creatures GameObject_IsBot (0x00880440) calls a bot: every
// bot_think_ms (200) the plan node's links to other plans, then the plan's links to its phases (gms/core/agents/brain.h
// ChoosePhaseLink; the phase goes into brain +0x18, where AgentBrain_GetCurrentPhase reads it, and a new default
// behavior restarts through request 0x400), then the phase's gambits: an ability slot (Q-R with the player's rank, basic,
// ward, spells) through LabsAbilities_RequestAbility with GUID 0 (it takes the slot's ability from the player), a
// consumable through LabsPlayer_CanUseItem/UseItem, the gambit's own ability by GUID; the target is the condition's
// (a bot's conditions return it as their 5th value) or the agent's mTarget, which the step sets to a hostile target so
// leaf 12 (Pursue) follows it. Flag 0x40 stays set while the phase has a default behavior. [host] bot_brain (1),
// bot_think_ms (200).
// D6c: every AI node keeps its link timers (cooldown, next check, activated once) and gambit cooldowns across visits,
// as the engine's per-node brain state (brain +0x30) does, for NPCs and bots; [host] brain_node_timers (1). And while
// an ability whose cStaticAbility +0x49A canInterrupt is false holds the agent (the behavior tree's UseAbility
// instance), the brain requests no gambit and a bot restarts no default behavior: either would abort it (the tree's
// UseAbility abort interrupts the holding instance); [host] brain_can_interrupt (1).
// D6d: a bot's gambit aimed at a point only (a condition with useTargetInfo that returns a position and kObjIDNone as
// its target with useTarget, condition_Bot_Ward; gms/core/agents/brain.h AimGambit) is requested without the agent's
// mTarget; when it is out of range (5) the step writes the point into the blackboard's target position (+0x1E64) and
// clears mTarget, so the Pursue leaf (12) runs nBehavior_Follow to the point (what the stripped brain did: nothing else
// writes +0x1E64, and Follow walks to it only when the best target is none); the point is cleared when the pursuit
// ends. [host] bot_pursue_point (1; 0 = D6a's rule: the request and the pursuit fall back to mTarget).
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>

#include "core/agents/leash.h"
#include "core/agents/minion_target.h"
#include "core/agents/tower_target.h"

namespace agents {

struct Options {
  bool tickTrees = true;   // step 1: AgentBehaviorTree_Tick for every agent each frame
  bool blackboard = true;  // step 2: perception, aggro list, flags, events every 500 ms per agent
  bool allVisible = true;  // [host] fog=0: every agent's mVisibleToTeam (+0x888) gets all three team bits
  bool brain = true;       // step 3: gambits, ability requests, pursue and phase-default flags, links
  int logLimit = 300;      // leaf-change, aggro-list and death log lines before only every 100th is logged
  bool botBrain = true;    // D6a: the bots' plans, phases and gambits
  int botThinkMs = 200;    // D6a: a bot thinks (links, phase, gambits) this often [assumed rate]
  bool keepNodeTimers = true;  // D6c: a node's link and gambit timers outlive a visit (the engine's per-node state)
  bool respectCanInterrupt = true;  // D6c: no gambit while an ability with canInterrupt false holds the agent
  // MINTARGET: lane minions (aggro type minion) take towers/structures/minions before shapers, a shaper only when nothing
  // else is in range or it hits an own-team shaper in range, and drop it beyond the leash
  bool minionTarget = true;      // [host] minion_target_priority
  float minionLeash = 25.f;      // [host] minion_leash_range: a shaper farther than this (edge distance) is dropped
  int minionDefendMs = 2500;     // [host] minion_defend_ms: how long an own shaper counts as under attack
  bool botPursuePoint = true;  // D6d: a bot's gambit at a point out of range walks to the point (blackboard +0x1E64)
  bool objectiveOnSight = false;    // D14: the objective type (the parasite) adds perceived enemies too (D1b-live)
  // NPCTARGET (gms/core/agents/brain.h NpcThink): an NPC's gambit on a passing condition's own target (its 5th value
  // with the 6th true; the parasite's and the Guardian's conditions pick shapers), else on the best target, and none
  // without a best target; a prioritized list's conditions run lazily in order (the parasite's special after four
  // basics); an out-of-range refusal without pursuit lets the next passing gambit try
  bool npcConditionTarget = true;   // [host] npc_condition_target
  bool npcLazyConditions = true;    // [host] npc_lazy_conditions
  bool npcRangeFallthrough = true;  // [host] npc_range_fallthrough
  int npcTargetLogLimit = 2000;     // [host] npc_target_log_limit: the parasite's and the Guardians' "npc target:" lines
  // BOTIDLE: bots that stand still. All three are on with [host] bot_idle_fixes (1); bot_fixups=0 turns them off too.
  bool botGambitFallthrough = true;  // [host] bot_gambit_fallthrough: a refused gambit (cooldown, resource, CanUse) lets the next one try
  int botPursueStuckMs = 6000;       // [host] bot_pursue_stuck_ms: a pursuit that gets no closer for this long drops its gambit (0 = off)
  int botIdleFallbackMs = 2500;      // [host] bot_idle_fallback_ms: idle this long under attack -> hit the attacker (0 = off)
  // BOTREFLEX (gms/core/agents/reflex.h): towers' hits are fled, Hunters and Predators fight back, engage shapers in the
  // jungle and defend their wells
  bool botReflex = true;             // [host] bot_reflex
  bool botJungleFight = true;        // [host] bot_jungle_fight: a jungler's "weaker" compares HP points (PRETUSBOT)
  bool botWellDefense = true;        // [host] bot_well_defense: Tacticians, Hunters, Predators defend their wells (WELLDEF)
  bool botKits = true;               // [host] bot_kits: the shapers' kit rules (HUNTERBOT: Faris never casts his stance Q)
  bool botHunterUlt = true;          // [host] bot_hunter_ult: a Hunter casts his R at a low enemy shaper (HUNTERBOT)
  bool botDesecKit = true;           // [host] bot_desec_kit: the Desecrator's kit layer (DESECBOT-3, core/agents/desecrator.h)
  bool botMikellaKit = true;         // [host] bot_mikella_kit: Mikella's kit layer (MIKELLABOT, core/agents/mikella.h)
  int botHunterIdleMs = 4000;        // [host] bot_hunter_idle_ms: a Hunter standing this long beside his target hits it (0 = off)
  // CAMPFINISH (gms/core/agents/campfinish.h): a Hunter below the Flee threshold finishes the camp it wins against
  bool botCampFinish = true;         // [host] bot_camp_finish: hold the Farm plan's Flee link while the Hunter wins the trade
  int campFinishLogLimit = 600;      // [host] camp_finish_log_limit: the "camp finish:" lines (then every 10th)
  bool botBackStep = true;           // [host] bot_back_step: the lane phases' Back gambit is a short step, not a walk home
  bool botRecall = true;             // [host] bot_recall: a bot sent home casts Return to Base when it is quiet and far away
  // KILLFINISH (gms/core/agents/killfinish.h): a bot in an EngageForKill plan stays to finish a won trade (its exit
  // links, its FleeTowerTargeting phase, its Back gambit and BOTREFLEX's tower back-off held); needs bot_reflex. Off,
  // the decision is still made and logged ("would hold")
  bool botKillFinish = true;         // [host] bot_kill_finish
  // BOTTOWER (gms/core/agents/towersafety.h): a tower that targets the bot, or has none of its minions in range, is a
  // danger as a recent hit is (BOTREFLEX's rule 1); targets under an unsafe enemy tower are left alone; a retreat step
  // held off by another walk of the same ability ends it (REFLEXWALK). All need bot_reflex; the measurement runs anyway
  bool botTowerAware = true;         // [host] bot_tower_aware
  bool botTowerKeepOut = true;       // [host] bot_tower_keepout
  bool botReflexWalk = true;         // [host] bot_reflex_walk
  int botTowerLogLimit = 300;        // [host] bot_tower_log_limit: the "bottower:" episode lines (then every 20th)
  // BOTCYCLE: a plan entered in a think whose own link fires at once is passed through in that think (two plan
  // changes per think at most, never back into a plan visited in it)
  bool botPlanChain = true;          // [host] bot_plan_chain
  // BOTCYCLE: a stance link back into the plan left less than this ago is held unless a fight or a flight is involved
  // (gms/core/agents/killfinish.h PlanBounceHeld); 0 = off
  int botPlanDwellMs = 1200;         // [host] bot_plan_dwell_ms
  // BOTCYCLE: an engage the bot left again at once blocks its next engages this long (EngageBlocked); 0 = off
  int botEngageBlockMs = 8000;       // [host] bot_engage_block_ms
  // BOTTEMPO (gms/core/agents/brain.h HoldStuck, TargetStale): no bot stands for minutes under its own walk to an
  // unreachable point or on the spot of a target that became untargetable
  int botHoldStuckMs = 15000;        // [host] bot_hold_stuck_ms: an instance holding a bot that hasn't moved 3 units this long ends (0 = off)
  bool botStaleTarget = true;        // [host] bot_stale_target: a bot's mTarget that is no longer a valid hostile target is dropped
  // BACKTOACTION (gms/core/agents/lost.h): a Pursue leaf that outlived its point pursuit walked to the zero vector (the
  // map's centre); the net for every bot: a Pursue leaf without anything to follow, a bot lost on the centre
  bool botPointFollowFix = true;     // [host] bot_point_follow_fix: restart the Pursue leaf after a point pursuit ends
  int botLostMs = 10000;             // [host] bot_lost_ms: on the centre without a target this long -> its lane front (0 = off)
  // NULLPOS (gms/core/agents/nullpos.h): (0, 0) is "no position"; a goal there is logged ("NULLPOS: ..."), counted and
  // refused or ended either way; strict (test sessions: run_session.ps1 sets it) logs every one without a budget
  bool botNullPosStrict = false;     // [host] bot_nullpos_strict
  int testNullPosFollowS = 0;        // [host] test_nullpos_follow: the forced Follow-on-(0, 0) case at this match second (0 = off)
  // RANGES: the parasite keeps an attacker on its list out to this distance ([host] parasite_reach, 0 = the data's
  // 27.05; it only lets the list keep an attacker, the on-sight range stays 27)
  float parasiteReach = 40.f;
  // MINJUNGLE: the minion watch: a lane minion (aggro type minion) that has stood within 1 unit for minionIdleMs while
  // more than minionOffLane units from every lane spline gets a "minions: idle off lane" line with its state (leaf,
  // spline id and index, tether, flags, aggro list, Lua wait, locomotion goal) and counts in the stats
  int minionWatchMs = 1000;     // [host] minion_watch_ms: how often (0 = off)
  int minionIdleMs = 8000;      // [host] minion_idle_ms
  float minionOffLane = 12.f;   // [host] minion_off_lane
  bool minionUnstick = true;     // [host] minion_unstick: an idle minion (not engaged) gets its default behavior restarted
  int minionWatchLogLimit = 300;  // [host] minion_watch_log_limit (also the "minions: chase:" lines)
  // MINJUNGLE: how long a lane minion chases a shaper away from its lane (gms/core/agents/minion_target.h): [host]
  // minion_chase (1; 0 = MINTARGET's rules only), minion_chase_distance (20), minion_chase_ms (3000),
  // minion_chase_min_move (6), minion_lane_leash (20), minion_chase_ignore_ms (4000)
  gms::agents::MinionChaseOptions minionChase;
  // MINPURSUE: a lane minion's Pursue leaf ends (default behavior restarted, follow target cleared) when the followed
  // object is no longer its best target, its list is empty, or it is beyond minion_lane_leash following a shaper:
  // [host] minion_follow_reset (1; 0 = the follow runs on until it reaches its object or the object dies)
  gms::agents::MinionFollowOptions minionFollow;
  // TOWERAGGRO (gms/core/agents/tower_target.h): towers (aggro type tower, not the Guardian) take a taunt, then a
  // shaper that hit a shaper of their team within their range until it leaves that range, then every non-player before
  // every shaper (stickiness and reports only within a class): [host] tower_target (1; 0 = the old order),
  // tower_log_limit (300, the "towers:" lines); TOWERSTICK: among the non-players the tower's current target (what it
  // shoots, while on the list) before the reports and the nearer: [host] tower_stick (1; 0 = TOWERAGGRO's order)
  gms::agents::TowerTargetOptions towerTarget;
  int towerLogLimit = 300;
  // LEASH: how far jungle creatures and well workers follow a target from home (gms/core/agents/leash.h). Creeps:
  // home = their spawn spot (object +0x5C), group = their camp (+0x50); [host] creep_leash (18 since CAMPFINISH, 15
  // before; 0 = off, the tether only), creep_leash_reach (4: the target leash 22, the engine's own tether for them),
  // creep_leash_home (10; 6), creep_leash_chase_ms (5000; 3000). Workers: home = their well's centre, the own limit =
  // the platform radius (the farthest worker spot) + worker_leash (2; 0 = off), worker_leash_reach (2), group = the
  // well. Both: leash_chase_ms (given: both chase times; the workers' default 3000; 0 = no chase time),
  // leash_ignore_ms (2000), leash_log_limit (300, the "leash:" lines).
  gms::agents::LeashOptions creepLeash;
  gms::agents::LeashOptions workerLeash;  // ownLimit = the margin beyond the platform; homeRadius unused (the platform)
  int leashLogLimit = 300;
};

// Checks the prologues of the client functions it calls. False (logged) if AgentBehaviorTree_Tick differs; then nothing
// runs. A differing blackboard function only turns the blackboard update off.
bool Install(const Options& options);
bool Installed();

// Start of a server tick (before the command intake): a new frame for the once-per-frame rule.
void BeginFrame();

// AgentBehaviorTree_Tick(object, dt) unless this object's tree was already ticked in this frame. True if it ticked.
// For the command intake (the players' creatures) and the agent loop.
bool TickTree(void* object, float dt);

// The agent step (server simulator bound): the due blackboard updates and every behavior tree not ticked yet this
// frame, then the leaf log.
void Tick(void* sim, float dt);

// After a damaging hit (the damage core, server simulator bound): the attacker onto the victim's own aggro list (not
// for a lethal hit) and Agent_CallForHelp for its helpers. AGENT_AI.md §6.3, step 4 of D1b-live.
// KILLFINISH: `dealt` (the HP the hit took) feeds the attacker's damage rate.
void OnDamaged(void* victim, void* attacker, unsigned descLo, unsigned descHi, bool lethal, float dealt);

// nBattlegrounds.AddBaseStructuresToAggroList's engine side (D5; match.cpp's Lua function, server simulator bound): the
// structures `ids` that are alive, of another team, targetable and attackable go onto the agent's aggro list, nearest
// first, with the entry flag kEntryFlagBaseStructure (gms/core/agents/aggro.h; written into an entry already there),
// which keeps them beyond dropAggroRange and outside the tether at the blackboard updates. Returns the number marked
// (-1: no agent, or the blackboard update is off); `report` gets "<id> <edge distance>[ (was on)]" per structure.
int AddBaseStructures(std::uint32_t agentId, const std::uint32_t* ids, int n, std::string* report);

// D6a: a bot's brain as the bot brain step holds it, for the stats lines ("plan 9 phase 16 leaf 13 PhaseDefault"); ""
// for an object the step hasn't seen.
std::string DescribeBot(std::uint32_t objectId);

// NPCTARGET: the NPC brain's target, lazy-walk and fall-through counters and the parasite's and Guardians' starts per
// ability, for a "stats npc:" line; false before the first brain step.
bool FormatNpcStats(char* buf, std::size_t size);
// HUNTERBOT: the kits' and the Hunters' counts (its own "stats hunters:" line; false before any bot thought)
bool FormatHunterStats(char* buf, std::size_t size);
// CAMPFINISH round 3: a Hunter's current camp fight (nBotHost.CampFight): its nearest living creature's position, the
// camp's share of its full HP, whether he finishes it first; false without one
bool HunterCampFight(std::uint32_t bot, float* pos, float* frac, bool* finishing);
// CAMPFINISH: the Hunters' Flee links in a camp fight, the holds and how they ended ("stats campfinish:")
bool FormatCampFinishStats(char* buf, std::size_t size);
// DESECBOT-3: the Desecrator's kit layer's counts (its own "stats desecrator:" line; false while it never acted)
bool FormatDesecStats(char* buf, std::size_t size);
// MIKELLABOT: Mikella's kit layer's counts (its own "stats mikella:" line; false while it never acted)
bool FormatMikellaStats(char* buf, std::size_t size);
// BACKTOACTION: the Pursue restarts, the walks off the centre and the bots' stands there (its own "stats lost:" line;
// false before the first bot thought)
bool FormatLostStats(char* buf, std::size_t size);
// NULLPOS: the "stats nullpos:" line (always, from the first bot think on: 0 is the pass)
bool FormatNullPosStats(char* buf, std::size_t size);
// NULLPOS from outside the brain: the Lua (nBotHost.NullPos: a running Follow read (0, 0), the shim's lane centre),
// bots.cpp's lane fronts, director.cpp's camps. `object` 0 when no bot is at hand (`who` then names the place).
// `kind` is a gms::agents::NullPosWhere.
void ReportNullPos(std::uint32_t object, int kind, const char* who, const char* where);
// NULLPOS: the Lua's Follow guard saw a running Follow's pass on a point (nBotHost.NullPosRead; counted in the stats)
void NoteFollowPointRead();

// KILLFINISH: whether the host would hold this bot in a fight with its target now (bot_kill_finish on; the Lua's
// KillShaper join and EngageForKill checks ask it: nBotHost.KillFinish); the reason BailOnKill gave for leaving
// (nBotHost.BailWhy, for the host's disengage lines); the "stats killfinish:" line (false before the first decision)
bool KillFinishWouldHold(std::uint32_t bot);
// BOTCYCLE: whether this bot's engages are blocked now (an engage it left at once; nBotHost.EngageBlocked)
bool KillFinishEngageBlocked(std::uint32_t bot);
void NoteBailWhy(std::uint32_t bot, const char* why);
bool FormatKillFinishStats(char* buf, std::size_t size);
// BOTTOWER: "stats bottower:" (the counts, the exposure totals) and "stats bottower bots:" (per bot)
bool FormatBotTowerStats(char* buf, std::size_t size);
bool FormatBotTowerBots(char* buf, std::size_t size);

// "agents N (trees ticked N this frame), ticks N, ..., leaves: PassiveIdle=3 Idle=30 ..." for the stats lines.
void FormatStats(char* buf, std::size_t size);
// MINJUNGLE: "idle off lane now N (most at once N), episodes N, ..." for the "stats minions:" line; false when the
// watch is off.
bool FormatMinionStats(char* buf, std::size_t size);
// LEASH: "creeps: fights N (mean N s, longest N s; farthest from home mean N, max N), drops ..." for the
// "stats leash:" line; false before the first fight of a creep or a worker.
bool FormatLeashStats(char* buf, std::size_t size);
// TOWERAGGRO: "rule on: updates N; shaper-on-shaper hits N; defend locks N, ..." for the "stats towers:" line; false
// before a tower's first update under the rule.
bool FormatTowerStats(char* buf, std::size_t size);

}  // namespace agents
