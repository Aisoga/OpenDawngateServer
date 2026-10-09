// gms/core/bots (D6b-shims): the bots' mission board without the engine (docs/SERVER_LUA_API.md tier 3, docs/AGENT_AI.md
// §11 *D6b-shims*). Header-only, no game addresses, like core/wells.
//
// Nothing of the original mission system survives in the client (R10: cBotDirector is a shell that copies
// Tuning.BotPlan at reset), so this is the host's: one board per match, both teams on it.
//   - The seven condition_Mission_* of Tuning.BotPlan run per team (the Lua driver dgsim_bots.MissionTick in
//     gms/engine/lua/dgsim_bots_api.lua, about once a second [assumed rate]); their Check(team) calls
//     nBot.RequestMission(team, type, target, agents, timeoutSec, conditionGUID [, destination]) for every target that
//     needs one, every time it runs, so Request() dedupes by (team, type, target) and refreshes [script];
//   - a mission ends when its condition's CheckEnd(team, target) returns true (the driver calls End()), when it went
//     `timeout` seconds without a refresh (Expire(); timeout 0 = never by time) [assumed meaning of `timeout`: 0, 5 or
//     30 in the scripts], or when its target object is gone (OnObjectGone());
//   - a bot takes a mission through its plan layer: a link whose condition_Bot_IsMissionAvailable(bot, missionType,
//     range) passes returns GetAvailableMission's id as its 3rd value, and the brain accepts that id when it follows the
//     link into a plan with a missionType [inferred from the data: Main -> Well links ask for type 3 or 4 (range 80/150)
//     and both lead to the Well plan, whose missionType is 3, so the accepted mission is the link's, not the plan's];
//     the bot leaves when the mission ends (GetMissionID 0, so `!condition_Bot_IsMissionActive` links it back to Main);
//   - EngageForKill plans have requestMission set: the brain opens a KillShaper (8) mission of its own (other bots join
//     through IsMissionAvailable(8, range 60)) [inferred].
// [assumed] marks every rule no client code, script or data shows.
#pragma once

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <map>
#include <vector>

namespace gms::bots {

// nMissionType (Lua/GlobalDefinitions). The plans' missionType uses the same numbers (Plan.missionType: Well 3,
// DefendBase 10, DefendGuardian 6, Gank 2, KillParasite 7, EngageForKill 8, RallyForParasite 11, ContestParasite 13).
enum MissionType : int {
  kMissionNone = 0,
  kAttackTower = 1,
  kDefendTower = 2,
  kAttackWell = 3,
  kDefendWell = 4,
  kAttackBase = 5,
  kDefendBase = 6,
  kKillParasite = 7,
  kKillShaper = 8,
  kAttackCore = 9,
  kDefendCore = 10,
  kRally = 11,
  kHarassWell = 12,
  kContestParasite = 13,
};

struct Vec3 {
  float x = 0.f, y = 0.f, z = 0.f;
};
inline Vec3 MakeVec(float x, float y, float z) {
  Vec3 v;
  v.x = x;
  v.y = y;
  v.z = z;
  return v;
}
// nVector.distance (3-D, as the scripts compare).
inline float Distance(const Vec3& a, const Vec3& b) {
  const float dx = a.x - b.x, dy = a.y - b.y, dz = a.z - b.z;
  return std::sqrt(dx * dx + dy * dy + dz * dz);
}

struct Mission {
  std::uint32_t id = 0;         // > 0; what GetAvailableMission/GetMissionID return (a Lua number: stays below 2^24)
  int team = 0;                 // the requesting team (nTeamID 1 or 2)
  int type = kMissionNone;      // nMissionType
  std::uint32_t target = 0;     // the target object's server id (0 = kObjIDNone)
  int agents = 1;               // how many bots it wants (the condition's defaultAgents; DefendCore/DefendBase 5)
  float timeout = 0.f;          // seconds without a refresh before it closes; 0 = never by time [assumed meaning]
  std::uint32_t condition = 0;  // the mission condition's GUID (nUtil.SPID of its name); 0 = none (DefendWell passes none)
  bool hasDestination = false;  // a rally point (RallyForParasite (-36, -6, 0) Mortal, ...)
  Vec3 destination;
  double opened = 0.0, refreshed = 0.0;  // the board's clock (the caller's seconds)
  std::vector<std::uint32_t> bots;       // bots that accepted it, in order
  bool Full() const { return static_cast<int>(bots.size()) >= std::max(agents, 1); }
  bool Has(std::uint32_t bot) const { return std::find(bots.begin(), bots.end(), bot) != bots.end(); }
};

class MissionBoard {
 public:
  // An object's position for the range test; false when the object is gone.
  using PositionFn = std::function<bool(std::uint32_t object, Vec3* pos)>;

  // nBot.RequestMission(team, type, target, agents, timeoutSec, conditionGUID [, destination]): opens a mission, or
  // refreshes the open one with the same (team, type, target) (its agents, timeout, condition and destination become the
  // latest call's [assumed]). Returns its id; 0 for a team other than 1/2 or type 0.
  std::uint32_t Request(int team, int type, std::uint32_t target, int agents, float timeout, std::uint32_t condition,
                        const Vec3* destination, double now) {
    if ((team != 1 && team != 2) || type == kMissionNone) return 0;
    Mission* m = FindKey(team, type, target);
    if (!m) {
      missions_.emplace_back();
      m = &missions_.back();
      m->id = nextId_++;
      m->team = team;
      m->type = type;
      m->target = target;
      m->opened = now;
    }
    m->agents = agents > 0 ? agents : 1;
    m->timeout = timeout > 0.f ? timeout : 0.f;
    m->condition = condition;
    m->hasDestination = destination != nullptr;
    m->destination = destination ? *destination : Vec3();
    m->refreshed = now;
    return m->id;
  }

  // nBot.IsMissionAvailable(team, type): an open mission of that type exists for the team, full or not
  // (condition_Mission_DefendTower's CheckEnd ends a tower defence once a DefendBase/DefendCore mission exists).
  bool IsAvailable(int team, int type) const {
    for (const Mission& m : missions_)
      if (m.team == team && m.type == type) return true;
    return false;
  }

  // nBot.GetAvailableMission(bot, type [, range]) -> id, 0 = none [assumed rules]: an open mission of the bot's team and
  // that type that still wants bots (or that the bot already holds); with range > 0 only one whose place (its
  // destination when set and non-zero, else its target's position) lies within `range` of the bot; the bot's own
  // mission first, then the nearest, then the oldest. Without a position for the target the mission counts only when
  // range is 0.
  std::uint32_t FindAvailable(std::uint32_t bot, int team, int type, const Vec3& botPos, float range,
                              const PositionFn& posOf) const {
    std::uint32_t best = 0;
    float bestDist = 0.f;
    for (const Mission& m : missions_) {
      if (m.team != team || m.type != type) continue;
      const bool mine = m.Has(bot);
      if (!mine && m.Full()) continue;
      Vec3 place;
      const bool known = PlaceOf(m, posOf, &place);
      const float d = known ? Distance(botPos, place) : 0.f;
      if (range > 0.f && (!known || d > range)) continue;
      if (mine) return m.id;
      if (!best || d < bestDist) {  // missions_ is in id order, so ties keep the older one
        best = m.id;
        bestDist = d;
      }
    }
    return best;
  }

  // nBot.AcceptMission(bot, id) and the brain's accept: the bot leaves its current mission and joins `id`; false (and no
  // change) when the mission is gone, belongs to the other team or is full.
  bool Accept(std::uint32_t bot, int team, std::uint32_t id) {
    Mission* m = Find(id);
    if (!m || m->team != team) return false;
    if (m->Has(bot)) return true;
    if (m->Full()) return false;
    Leave(bot);
    m->bots.push_back(bot);
    botMission_[bot] = id;
    return true;
  }

  void Leave(std::uint32_t bot) {
    auto it = botMission_.find(bot);
    if (it == botMission_.end()) return;
    if (Mission* m = Find(it->second)) m->bots.erase(std::remove(m->bots.begin(), m->bots.end(), bot), m->bots.end());
    botMission_.erase(it);
  }

  // nBot.GetMissionID(bot): 0 = none.
  std::uint32_t MissionOf(std::uint32_t bot) const {
    auto it = botMission_.find(bot);
    return it == botMission_.end() ? 0u : it->second;
  }
  // nBot.GetMissionTarget(bot): 0 (kObjIDNone) without a mission.
  std::uint32_t TargetOf(std::uint32_t bot) const {
    const Mission* m = Find(MissionOf(bot));
    return m ? m->target : 0u;
  }
  // nBot.GetMissionDestination(bot): the zero vector without a mission or destination (the callers use it only when
  // x and y are non-zero).
  Vec3 DestinationOf(std::uint32_t bot) const {
    const Mission* m = Find(MissionOf(bot));
    return m && m->hasDestination ? m->destination : Vec3();
  }

  const Mission* Find(std::uint32_t id) const {
    for (const Mission& m : missions_)
      if (m.id == id) return &m;
    return nullptr;
  }
  Mission* Find(std::uint32_t id) {
    for (Mission& m : missions_)
      if (m.id == id) return &m;
    return nullptr;
  }

  // Ends a mission (its CheckEnd returned true): its bots are released. False for an unknown id.
  bool End(std::uint32_t id) {
    for (size_t i = 0; i < missions_.size(); ++i) {
      if (missions_[i].id != id) continue;
      for (std::uint32_t bot : missions_[i].bots) botMission_.erase(bot);
      missions_.erase(missions_.begin() + static_cast<std::ptrdiff_t>(i));
      return true;
    }
    return false;
  }

  // Ends every mission whose timeout passed without a refresh; returns their ids.
  std::vector<std::uint32_t> Expire(double now) {
    std::vector<std::uint32_t> ended;
    for (const Mission& m : missions_)
      if (m.timeout > 0.f && now - m.refreshed >= m.timeout) ended.push_back(m.id);
    for (std::uint32_t id : ended) End(id);
    return ended;
  }

  // An object left the game: missions with it as their target end (returned), a bot leaves its mission.
  std::vector<std::uint32_t> OnObjectGone(std::uint32_t object) {
    std::vector<std::uint32_t> ended;
    if (object == 0) return ended;
    Leave(object);
    for (const Mission& m : missions_)
      if (m.target == object) ended.push_back(m.id);
    for (std::uint32_t id : ended) End(id);
    return ended;
  }

  // The open missions of a team, in id order (the driver runs CheckEnd over them; dgsim_bots.MissionTick).
  std::vector<const Mission*> Open(int team) const {
    std::vector<const Mission*> out;
    for (const Mission& m : missions_)
      if (m.team == team) out.push_back(&m);
    return out;
  }
  size_t Count() const { return missions_.size(); }
  void Clear() {
    missions_.clear();
    botMission_.clear();
  }

 private:
  Mission* FindKey(int team, int type, std::uint32_t target) {
    for (Mission& m : missions_)
      if (m.team == team && m.type == type && m.target == target) return &m;
    return nullptr;
  }
  // Where a bot goes for `m`: a non-zero destination (the scripts' `x ~= 0 and y ~= 0` test), else the target.
  static bool PlaceOf(const Mission& m, const PositionFn& posOf, Vec3* out) {
    if (m.hasDestination && m.destination.x != 0.f && m.destination.y != 0.f) {
      *out = m.destination;
      return true;
    }
    return m.target != 0 && posOf && posOf(m.target, out);
  }

  std::vector<Mission> missions_;                      // in id order
  std::map<std::uint32_t, std::uint32_t> botMission_;  // bot -> mission id
  std::uint32_t nextId_ = 1;
};

}  // namespace gms::bots
