// gms/core/bots (D6b-shims): the bots' host state, engine-free (docs/SERVER_LUA_API.md tier 3 *D6b-shims*,
// docs/AGENT_AI.md §11 *D6b-shims*). Header-only; nothing here calls the game.
//
// Tier 3 of the missing server Lua API splits in two:
//   - the queries over the bound API (nObjectManager, nGameObject, nAgent, nPlayer, nLevel, ...) are Lua:
//     gms/engine/lua/dgsim_bots_api.lua defines all 30 nBot functions and the 7 bot-only nBattlegrounds getters;
//   - what needs match state is here, and the host (D6a/D6b, main track) exposes it to that Lua file as the optional
//     C namespace `nBotHost` (registered with GearsLua_RegisterNamespace before the file runs; every function is
//     optional, and the file falls back to a safe answer without it):
//
//   nBotHost function (Lua)                          returns                         here
//   AssignLane(bot, lane)                            -                               BotLanes::Assign
//   GetLane(obj)                                     lane, 0 = none / not a bot      BotLanes::LaneOf
//   ClosestLane(x, y, z)                             lane                            LaneFronts::Closest
//   LaneCenterAndDirection(lane, team)               cx, cy, cz, dx, dy, dz | none   LaneFronts::Get
//   RequestMission(team, type, target, agents, timeoutSec, conditionGUID, hasDest, dx, dy, dz)
//                                                    id                              MissionBoard::Request
//   IsMissionAvailable(team, type)                   bool                            MissionBoard::IsAvailable
//   GetAvailableMission(bot, type, range)            id, 0 = none                    MissionBoard::FindAvailable (the
//                                                                                    host reads the bot's team and
//                                                                                    position; posOf = positions)
//   AcceptMission(bot, id)                           bool                            MissionBoard::Accept
//   GetMissionID(bot) / GetMissionTarget(bot)        id / object                     MissionOf / TargetOf
//   GetMissionDestination(bot)                       x, y, z (0, 0, 0 = none)        DestinationOf
//   OpenMission(team, i)  (i = 1, 2, ...)            id, target, conditionGUID | none  Open(team)[i - 1]
//   EndMission(id)                                   bool                            MissionBoard::End
//   CanAffordNextItem(bot) / CanBuyPotion(bot)       bool                            ShopPlanner (gold = the player's
//   PurchaseNextItem(bot) / PurchasePotions(bot)     bool / count                    +0xA8; the purchase = D7b's path)
//   IsFirstTimePlayer(obj)                           bool                            BotsState::firstTimePlayers
//   IsPositionSafe(team, x, y, z)                    bool                            FireTracker_IsPositionSafe
//                                                                                    (0x00876900), tracker on (§11.4 5.)
//   MinesForTeam(team, onlyUnlocked) / FollowersForTitan(well) / IsTitanNodeLocked(well)
//                                                    table / number / bool           gms::wells::Wells (D9)
//
// Object ids cross as light userdata (the host's SehArgId / SehPushId), the conditionGUID too (nUtil.SPID pushes the
// FNV-1 hash as light userdata); numbers are 4-byte floats, so mission ids stay small integers.
// The host also: calls board.Expire(now) and, about once a second [assumed], dgsim_bots.MissionTick() (the Lua driver
// that runs the plan's mission conditions per team: Check, then CheckEnd for the open missions); calls
// board.OnObjectGone(id) when an object is deleted; refreshes LaneFronts from the living lane minions; accepts missions
// in the bot brain's plan layer (missions.h header comment); fills teams with FillTeam (teamfill.h).
#pragma once

#include <cstdint>
#include <map>
#include <set>

#include "core/bots/lanes.h"
#include "core/bots/missions.h"
#include "core/bots/shopping.h"
#include "core/bots/teamfill.h"

namespace gms::bots {

// D6b: what the bot brain's plan layer did with a mission when it entered a plan (BotsState::EnterPlan).
enum class PlanMission : int {
  kNone = 0,  // a plan without a missionType, and the bot held no mission
  kAccepted,  // the firing link's mission id (condition_Bot_IsMissionAvailable's 3rd value) accepted
  kRefused,   // that id was gone, the other team's or full: the bot holds none (`!condition_Bot_IsMissionActive` takes
              // it back to Main)
  kOpened,    // a requestMission plan (EngageForKill) opened a mission of its own type on the bot's target
  kKept,      // a plan with a missionType, no new id: the bot keeps what it holds
  kLeft,      // a plan without a missionType: the bot left its mission (others may still take it)
};
inline const char* PlanMissionName(PlanMission m) {
  switch (m) {
    case PlanMission::kNone: return "none";
    case PlanMission::kAccepted: return "accepted";
    case PlanMission::kRefused: return "refused";
    case PlanMission::kOpened: return "opened";
    case PlanMission::kKept: return "kept";
    case PlanMission::kLeft: return "left";
  }
  return "?";
}

// The plan a bot enters (Plan +0x14 missionType, +0x18 requestMission) and how it got there.
struct PlanEntry {
  int missionType = kMissionNone;
  bool requestMission = false;
  std::uint32_t linkMission = 0;  // the firing link's mission id (0 = none)
  std::uint32_t target = 0;       // the bot's target (its mTarget: condition_Bot_EngageForKill sets it), for an own one
};

// A requestMission plan's own mission [assumed: no data gives them]: the requester plus two allies (they join through
// condition_Bot_IsMissionAvailable(missionType = 8, range = 60)), and open while the requester stays in the plan (it
// refreshes every think) plus 5 s.
constexpr int kOwnMissionAgents = 3;
constexpr float kOwnMissionTimeout = 5.f;

// The bot's inventory for the shopping plan from its 11 slots (cLabsPlayer mItems: asset id and count per slot): the
// stat items of slots 0-5 and the potions (`potion`'s id) wherever they lie (the consumable slots 9-10).
inline Inventory InventoryFromSlots(const std::uint32_t* items, const int* counts, int slots, std::uint32_t potion) {
  Inventory inv;
  for (int i = 0; i < slots; ++i) {
    if (i < inv.slots) inv.items.push_back(items[i] == potion ? 0u : items[i]);
    if (potion && items[i] == potion) inv.potions += counts[i] > 0 ? counts[i] : 0;
  }
  return inv;
}

// The per-match bot state the host keeps beside the engine (one instance per match host).
struct BotsState {
  MissionBoard missions;
  BotLanes lanes;
  LaneFronts fronts;
  std::set<std::uint32_t> firstTimePlayers;  // creature ids of human players in their first game (nBot.IsEnemyOf-
                                             // FirstTimePlayer; from the roster's FTUE flag [assumed source])
  std::set<std::uint32_t> bots;              // the bots' creature ids

  bool IsBot(std::uint32_t object) const { return bots.count(object) != 0; }
  // A bot or player object left the game.
  void Forget(std::uint32_t object) {
    missions.OnObjectGone(object);
    lanes.Forget(object);
    firstTimePlayers.erase(object);
    bots.erase(object);
  }

  // D6b: the plan layer moved `bot` (of `team`) into a plan. The rule [inferred from the plan data, AGENT_AI.md §11.8;
  // the rest assumed]: a bot holds a mission exactly while it is in a plan with a missionType. Into such a plan it takes
  // the firing link's mission id (the link's condition_Bot_IsMissionAvailable checked it for this bot), a
  // requestMission plan without one opens its own on the bot's target; into a plan without a missionType (Main, Flee,
  // PlayerCombat, Ward, ...) it leaves its mission, which stays open for the others. `*mission` gets the bot's mission
  // afterwards (0 = none).
  PlanMission EnterPlan(std::uint32_t bot, int team, const PlanEntry& e, double now, std::uint32_t* mission = nullptr) {
    PlanMission r = PlanMission::kNone;
    if (e.missionType == kMissionNone) {
      if (missions.MissionOf(bot)) {
        missions.Leave(bot);
        r = PlanMission::kLeft;
      }
    } else if (e.linkMission) {
      r = missions.Accept(bot, team, e.linkMission) ? PlanMission::kAccepted : PlanMission::kRefused;
      if (r == PlanMission::kRefused && e.requestMission && OpenOwn(bot, team, e.missionType, e.target, now))
        r = PlanMission::kOpened;
      else if (r == PlanMission::kRefused)
        missions.Leave(bot);
    } else if (e.requestMission) {
      r = OpenOwn(bot, team, e.missionType, e.target, now) ? PlanMission::kOpened : PlanMission::kKept;
    } else {
      r = PlanMission::kKept;
    }
    if (mission) *mission = missions.MissionOf(bot);
    return r;
  }

  // Every think while the bot is in a requestMission plan: refresh the mission it holds of that type (its own, or one
  // it joined), else open one on `target`. Returns the bot's mission id (0 = none: no target).
  std::uint32_t RefreshOwnMission(std::uint32_t bot, int team, int type, std::uint32_t target, double now) {
    const Mission* m = missions.Find(missions.MissionOf(bot));
    if (m && m->type == type) {
      if (m->bots.empty() || m->bots.front() == bot)  // the requester keeps it open; a joiner only rides along
        missions.Request(team, type, m->target, m->agents, m->timeout, m->condition, nullptr, now);
      return m->id;
    }
    return OpenOwn(bot, team, type, target, now) ? missions.MissionOf(bot) : 0u;
  }

 private:
  bool OpenOwn(std::uint32_t bot, int team, int type, std::uint32_t target, double now) {
    if (!target) return false;
    const Mission* held = missions.Find(missions.MissionOf(bot));
    if (held && held->type == type && held->target == target) return true;
    const std::uint32_t id = missions.Request(team, type, target, kOwnMissionAgents, kOwnMissionTimeout, 0, nullptr, now);
    return id && missions.Accept(bot, team, id);
  }

 public:
  // nBot.IsEnemyOfFirstTimePlayer(bot) given the bot's enemies (living or not): any of them a first-time human.
  template <class Container>
  bool AnyFirstTime(const Container& enemyHumans) const {
    for (std::uint32_t o : enemyHumans)
      if (firstTimePlayers.count(o)) return true;
    return false;
  }
};

}  // namespace gms::bots
