// Bot players on the match host (roadmap D6a; docs/AGENT_AI.md §11 and *D6a: bot players on the match host*;
// docs/SERVER_LUA_API.md *Tier 3 for the bots (D6b-shims)*). What the stripped bot director did for a practice game
// (nGameDescriptors.BotsFillTeams, 0x400, is set in the practice game's 0x50C), run by the host with the client's own
// pieces:
//   - the bot players (EnsurePlayers, at the first accepted HelloReq): Tuning.BotPlan's slots and bot classes, team
//     fill with gms/core/bots (FillTeam / PlanBotPlayers: the humans take slots first, the bots the rest, gameplay
//     indexes the humans don't hold, online id = index + 1), each a GameSimulator_AddPlayer (0x008F3E20) on the server
//     simulator with its team and online id, LabsPlayer_ReflectBotData (0x00915D30: mbIsBot, mpBotData, the BotClass's
//     noun and role, the "<shaper> BOT" name) and the status of a player that has accepted, picked and loaded
//     (0x10000 | 0x100 | 0x400, progress 1.0); the GMS session announces them to every client (IMatch::OtherPlayers:
//     HelloPlayer records of type 1, then their LabsPlayerGlobalUpdates written from the server's cLabsPlayer);
//   - their creatures (DeployAll, at the first client's level-loaded point, before the world is built): the same
//     LabsPlayer_DeployCharacter (0x00915400) as a human's shaper, at the team's start point; its bot branch ranks up
//     the first skill and buys the first spell; GameObjectManager_OnObjectCreated gives the creature a brain
//     (AgentBrain_InitBot 0x008AD970: the BotClass's AIDefinition, the start plan) and agents.cpp's bot brain step runs
//     its plans, phases and gambits;
//   - the bots' Lua API (OnLevelStarted): the optional nBotHost namespace over gms/core/bots's BotsState (lanes, lane
//     fronts, first-time players), registered before gms/engine/lua/dgsim_bots_api.lua runs on the server's lua_State;
//   - every tick (Tick): the lane fronts from the living lane minions twice a second (nBot.GetLaneCenterAndDirection).
// D6b (docs/AGENT_AI.md *D6b*): the rest of nBotHost over the same BotsState:
//   - missions: RequestMission, IsMissionAvailable, GetAvailableMission, AcceptMission, GetMissionID, GetMissionTarget,
//     GetMissionDestination, OpenMission, EndMission (gms/core/bots/missions.h); every `bot_mission_ms` (1000) the Tick
//     ends the missions whose target is gone (BotsState::Forget), expires the stale ones (MissionBoard::Expire) and runs
//     dgsim_bots.MissionTick() (Tuning.BotPlan's seven condition_Mission_*: Check, then CheckEnd); the brain's plan
//     layer (agents.cpp) reports the plans a bot enters (OnPlanEntered: BotsState::EnterPlan) and refreshes a
//     requestMission plan's own mission (OnRequestPlanThink);
//   - shopping: CanAffordNextItem, PurchaseNextItem, CanBuyPotion, PurchasePotions over gms/core/bots ShopPlanner with
//     the bot class's recommended build ("PC_<Shaper>".Noun recommendedItems) and its cLabsPlayer's slots and gold; a
//     purchase goes through D7b's item transaction (cmds::OnClientMessage, ItemTransaction purchase with "no slot"):
//     GameSimulator_OnItemTransaction's checks and LabsPlayer_PurchaseItem on the bot's player, the C3 answer to its team;
//   - IsPositionSafe over the bot director's fire tracker, which the host switches on with the bots (director +0x978).
// The wells' three nBotHost functions are wells.cpp's (D9); GearsLua_RegisterNamespace merges both into one table.
// Every engine call runs under SEH with the server simulator bound. Settings ([host] keys, experiment.cpp reads them):
// bots (-1 = auto: createGame's fillTeams seats a team when dgserver asks (B10d: a matchmade game that started with open
// seats), else 5 seats a team when the game's descriptors have BotsFillTeams, else none; N = N seats a team, 0 =
// none), bots_allies (1: fill the humans' teams too), bot_classes ("" = Tuning.BotPlan's bots; "PC_Warden,PC_Guardian"
// replaces the list), bots_lua (1), bot_lanes (1), bot_ftue (1: the humans of an IsFTUE game are first-time players for
// nBot.IsEnemyOfFirstTimePlayer), bot_log_limit (300); D6b: bot_missions (1), bot_mission_ms (1000), bot_shopping (1),
// bot_potions (2: carried at most), BOTSHOP-PASS: bot_shop_visit (1), bot_shop_visit_ms (1000),
// bot_shop_visit_repeat_ms (1000); bot_fire_tracker (1), bot_fire_zones (0: FIREZONE's stub for the tracker's NavPower
// penalty boxes; 1 = the game's code), bot_fixups (1: dgsim_bots_api.lua's two script fix-ups); the
// brain's bot_think_ms is agents.h's.
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <utility>
#include <vector>

#include "core/bots/lanes.h"  // BOTS2: LanePath

namespace bots {

struct Options {
  int teamSize = -1;
  bool allies = true;
  std::string classes;
  bool lua = true;
  bool lanes = true;
  bool ftue = true;
  int logLimit = 300;
  // D6b
  bool missions = true;
  int missionMs = 1000;
  bool shopping = true;
  int potions = 2;
  // BOTSHOP-PASS (core/bots/shopping.h ShopVisit): [host] bot_shop_visit, the host shops for a bot that stays in the
  // shop's reach `bot_shop_visit_ms` without its plan's purchase, again every `bot_shop_visit_repeat_ms`; 0 = only the
  // plans' ability_Bot_Buy
  bool shopVisit = true;
  int shopVisitMs = 1000;
  int shopVisitRepeatMs = 1000;
  bool fireTracker = true;
  bool fireZones = false;  // FIREZONE: [host] bot_fire_zones, 1 = keep the game's NavPower fire zones (0x00877870)
  bool fixups = true;
  bool parasiteGuard = true;  // BOTPARA: dgsim_bots_api.lua's fourth fix-up (the parasite missions)
  bool composition = true;    // BOTCOMP: the preferred 5v5 setup (2 Gladiators, Tactician, Predator, Hunter); 0 = Tuning.BotPlan's fill
  std::string compositionDrop = "predator,hunter,tactician,gladiator";  // BOTCOMP: the roles' order of giving up a seat
  bool profiles = true;  // BOTPROFILE: our own BotClass profiles (PC_Warden_Predator) when the copy's catalog lists them
  std::string hunters = "default";  // HUNTERBOT: [host] bot_hunters, the Hunter seat's classes (composition.h ParseHunterList)
  std::string tacticians;  // DESECBOT: [host] bot_tacticians, each team's first Tactician seat (ParseTacticianList); "" = drawn
  std::string gladiators;  // MIKELLABOT: [host] bot_gladiators, each team's first Gladiator seat (ParseGladiatorList); "" = drawn
  // BOTS2c: [host] bot_mirror, every team the same GTGTH lineup seat by seat (composition.h MirrorLineup; "" = off, "1"
  // the default lineup, or five classes); no draw, no pins, and no draft (the composed seats stand)
  std::string mirror;
  bool guideBuilds = true;  // DESECBOT-2: [host] bot_guide_builds, a profile's own build (core/bots/guidebuild.h); 0 = its ItemBuild
  bool kits = true;             // HUNTERBOT: [host] bot_kits, the shapers' kit rules (the Lua's E spin rule; kits.h)
  bool hunterFix = true;        // HUNTERBOT: [host] bot_hunter_fix, the shared Hunter changes (hold, push, defend, waves, route)
  bool campFinish = true;       // CAMPFINISH: [host] bot_camp_finish (the Lua's half: a Hunter finishes a nearly dead camp first)
  bool engageFix = true;        // DESECBOT-3: [host] bot_engage_fix, no EngageForKill that BailOnKill would leave at once
  bool joinFix = true;          // BOTCYCLE: [host] bot_join_fix, a KillShaper join takes the mission's target and only
                                // when BailOnKill would stay (or the host would finish: KILLFINISH)
  bool desecKit = true;         // DESECBOT-3: [host] bot_desec_kit, the Desecrator's early no-dive, no-chase rule (Lua)
  bool fogCheck = true;         // BOTBUSH: [host] bot_fog_check, the bots' enemy-shaper queries and target checks by sight (Lua)
  int harassStartWorkers = 4; // BOTCOMP: a well's workers above which the Predator may harass it (the script's HarassWell: 16)
  int harassEndWorkers = 2;   // BOTCOMP: ... and below which the harass ends (the script's 4)
  bool team = true;           // BOTTEAM: the team layer (one call per team: farm, parasite, contest, well, push, defend)
  bool teamPings = true;      // BOTTEAM: the bots ping their team's calls to the human teammates (MiniMapPing)
  bool tempo = true;          // BOTTEMPO: pushes that last (the walk is no stall, damage is progress) and the late siege
  int teamStatusS = 30;       // BOTTEMPO: a "team N status" line per team this often (0 = none)
  int hunterGank = 0;         // BOTCOMP: 1 keeps the Hunter's Gank plan (a trap: it never leaves it)
  bool laneFixes = true;      // BOTLANE: Gladiators stay in lane, towers get finished, the opened lane is sieged
  int campRadius = 9;         // BOTIDLE: dgsim_bots_api.lua's fifth fix-up: IsNearNextCreepCamp's radius (the script's 5; 0 = keep it)
  bool jungle = true;         // BOTJUNGLE: the Hunter takes part (ganks, defends, pushes, wells, the parasite gather)
  bool pingFollow = true;     // BOTJUNGLE: the bots follow their human teammates' minimap pings
  bool hunterParasiteStand = true;  // PRETUSBOT: the Hunter fights the parasite from its edge, not from its centre
  int draft = -1;  // BOTDRAFT: [host] bot_draft, -1 auto (custom, practice, PvP: gms::bots::DraftOn), 0 off, 1 always
  // MAPHOST: [host] bot_hunter_seat, -1 auto (a bot Hunter only on a level with jungle camps: director::JungleCamps),
  // 0 never (the seat is a laner: composition.h NoJungleSeat), 1 always (as before MAPHOST)
  int hunterSeat = -1;
  // BOTLANESPLIT: [host] bot_lane_split, the bots divide each role evenly over the lanes (gms/core/bots/lanesplit.h): at
  // the composition and the draw, after a role change in shaper select, and for a single bot's lane change in the match
  // (ability_Bot_AssignToThreatenedLane through nBotHost.LaneSplit); 0 = the lanes as composed and every change taken
  bool laneSplit = true;
};

// BOTJUNGLE: a human's minimap ping (the command intake's ping relay, commands.cpp): kept (the last 32) for
// nBotHost.HumanPing(team, afterSeq), so the bots of the pinger's team can follow it.
void OnHumanPing(int team, std::uint32_t pinger, std::uint32_t target, const float* pos, int type);

void Configure(const Options& o);

// B10d: createGame's fillTeams (the seats a team dgserver asks the host to fill with bots; 0 = not asked), for the
// `bots` auto rule. Called at createGame, before the first HelloReq plans the bots.
void SetFillTeams(int seats);
// BOTDRAFT: createGame's mode ("custom", "practice", "pvp" turn the draft on with [host] bot_draft -1). Called at
// createGame.
void SetGameMode(const std::string& mode);

// After GameSimulator_Init, before the level (server simulator bound): nBotHost on the shared lua_State.
void OnSimulatorCreated(void* sim);
// After the level load (bound): Tuning.BotPlan, the lanes, dgsim_bots_api.lua.
void OnLevelStarted(void* sim);

// A human of the match: gameplay index, team, shaper (its Noun asset, for the slot it takes).
struct Human {
  int index = -1;
  int team = 0;
  void* noun = nullptr;
  int role = -1;  // BOTCOMP: its mRole when it has picked (shaper select), else -1 (unknown)
};

struct Bot {
  int index = -1;
  int team = 0;
  std::uint64_t onlineId = 0;
  std::string botClass;
  int lane = 0;              // nLaneID: the first lane
  std::uint32_t creature = 0;
  std::uint32_t status = 0;  // the server player's mStatus
  bool created = false;      // the server player exists
  std::uint32_t noun = 0;    // D3-live: its mpNounDef's asset id (BotClass.nounDef: a default skin, as a list entry)
  int role = 0;              // D3-live: its mRole (BotClass.role)
  // BOTLANESPLIT: the last lane change it was asked (from, to, the answer), so that the same question at every think
  // (the threatened-lane condition asks it) is counted and logged once.
  mutable int splitAsk[3] = {-1, -1, -1};
};

// Plans and creates the bot players once (later calls return the same list): `humans` (connected) and `waiting`
// (createGame's roster humans not connected yet: index and roster team, noun unknown) keep their gameplay indexes, and
// since G3b a waiting human counts for its team's seats too (gms::bots::CountedHumans: a party whose guest connected
// first got a bot for its leader, d8b_run6d). Bound to the server simulator inside.
const std::vector<Bot>& EnsurePlayers(void* sim, const std::vector<Human>& humans, const std::vector<Human>& waiting);
const std::vector<Bot>& Players();
bool IsBotIndex(int index);
// BOTS2c: what the next step of the bot's build costs (its shop plan, as the shop visit would buy it): 0 when there is
// nothing to buy (the build done or blocked), -1 unknown (no such bot, shopping off). Server simulator bound.
int NextItemPrice(std::uint32_t creature);
// BACKTOACTION: the front of the bot's lane (nBot.AssignLane's, else its first lane) for `team`, as
// nBotHost.LaneCenterAndDirection returns it; false without lanes or fronts. `laneOut` (optional) gets the lane.
bool LaneFrontOf(std::uint32_t creature, int team, float* out, int* laneOut = nullptr);
// BOTS2: the lanes' polylines (team 1's base to team 2's; empty before the level) and the bot's lane now
// (nBot.AssignLane's, else its first lane; 0 unknown).
const std::vector<gms::bots::LanePath>& LanePaths();
int CurrentLane(std::uint32_t creature);
// Shaper select moved a bot to another role (its mRole is written by the host's pick write; this keeps our list, which
// the Lua shims' BotRole answers from, in step).
void SetRole(int index, int role);
// BOTLANESPLIT: the reveal's picks are in (the bots' roles after SetRole, the humans' picked roles as (team, role)):
// every team's bots divide their roles over the lanes again, the humans counted (gms/core/bots/lanesplit.h), before the
// deploy hands the lanes to nBot.AssignLane. Logs one "BOTLANESPLIT (the reveal)" line per team.
void SplitLanesAtReveal(const std::vector<std::pair<int, int>>& humanTeamRoles);

// BOTDRAFT (gms/core/bots/draft.h): in a custom, practice or PvP game the bots get their classes in
// shaper select, after the humans have chosen. BeginDraft (shaper select set up, the bots created) says whether the
// draft runs; from then until RevealDrafted the bots' player updates carry no bot data, noun or role (DraftHidden) and
// their status lacks 0x100, so the clients show "BOT n" with an empty slot. Draft draws every bot's class from the
// humans' shapers and intended roles, writes it into the server players (LabsPlayer_ReflectBotData again) and returns
// what shaper select announces; RevealDrafted (the reveal) sets 0x100 and lets the bot data go out.
struct DraftHumanIn {
  int index = -1;
  int team = 0;
  std::uint32_t shaper = 0;  // its record's shaper (default-skin Noun asset id), 0 = none
  unsigned preferred = 0;    // preferred-role toggles, bit per role
  int roleCard = -1;         // the role card it chose, -1 = none
};
struct DraftOut {
  int index = -1;
  std::uint32_t noun = 0;
  int role = 0;
  unsigned altRoles = 0;
};
bool BeginDraft(void* sim);
bool DraftHidden();
std::vector<DraftOut> Draft(void* sim, const std::vector<DraftHumanIn>& humans);
void RevealDrafted(void* sim);

// Deploys every bot's creature once (the replication must be initialized: repl::Init). Returns how many were deployed
// by this call. Bound inside.
int DeployAll(void* sim);

// Every server tick (bound): the lane fronts; D6b: the missions (gone targets, expiry, dgsim_bots.MissionTick).
void Tick(void* sim);

// D6b: the bot brain's plan layer (agents.cpp BotBrainStep, bound) moved the bot `creature` into plan `planName`
// (Plan +0x14 missionType, +0x18 requestMission) by a link whose condition_Bot_IsMissionAvailable gave `linkMission`
// (0 = none); `target` = the bot's mTarget. BotsState::EnterPlan; logged.
void OnPlanEntered(std::uint32_t creature, int team, int missionType, bool requestMission, std::uint32_t linkMission,
                   std::uint32_t target, const char* noun, const char* planName);
// D6b: every think while the bot is in a requestMission plan (EngageForKill): its own mission stays open.
void OnRequestPlanThink(std::uint32_t creature, int team, int missionType, std::uint32_t target);
// The bot's mission id (0 = none), for the brain's log.
std::uint32_t MissionOf(std::uint32_t creature);

void FormatStats(char* buf, std::size_t size);

}  // namespace bots
