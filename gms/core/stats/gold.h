// M18 (MSTATS2): gold and XP by source and the roles' bonuses, engine-free.
//
// Two inputs, joined at the match's end into gms::stats::Accumulator's keys:
//   - the engine's match-telemetry record per player (sim +0xCCB0, MSTATS0 sec. 1): 107 floats, stat 5 the total gold,
//     6 the total XP, 7-20 gold by internal source (nGoldSourceType + 7), 21-31 XP by source (nXPSourceType + 21).
//     LabsPlayer_AddGold adds each grant's base amount to its source and the Role parts (a grant's bonus, the
//     OnGrantGold handlers' change, the scripts' Role grants) to stat 19, so the sources sum to stat 5 exactly;
//   - RoleLedger: what the host's own grant code knows about each role part (a reward's role part, an OnGrantGold
//     handler's change by the role modifier that made it, a Predator's XP part, the Hunter's damage bonus).
// The Tactician's harass vim (its script's Role grant) is the rest of stat 19: goldRole - the role parts granted as a
// bonus - the handlers' changes.
#pragma once

#include <cstdint>
#include <vector>

#include "core/stats/stats.h"

namespace gms::stats {

constexpr int kTelemetryStats = 107;  // a player record: +0 the index, then 107 floats (stat s at +4 + 4s)
constexpr int kStatGoldTotal = 5;
constexpr int kStatXpTotal = 6;
constexpr int kStatGoldFirst = 7;   // nGoldSourceType.Unknown
constexpr int kStatGoldRole = 19;   // nGoldSourceType.Role
constexpr int kStatGoldLast = 20;   // nGoldSourceType.Cheat
constexpr int kStatXpFirst = 21;    // nXPSourceType.Unknown
constexpr int kStatXpLast = 31;     // nXPSourceType.Cheat

// The seatStats key of telemetry stat `stat` (5-31), or nullptr.
const char* TelemetryKey(int stat);

// Rounds `values` to integers that sum to llround(total) (the largest remainders get the difference), so a seat's
// sources add up to its total in the stored line too. Values and total are expected non-negative.
std::vector<std::int64_t> RoundToSum(const std::vector<double>& values, double total);

// The role modifiers' static GUIDs (SPID of the name).
constexpr std::uint32_t kGuidRoleGladiator = 0x33B6C2B7u;  // modifier_Role_Gladiator
constexpr std::uint32_t kGuidRoleTactician = 0xF1727C80u;  // modifier_Role_Tactician
constexpr std::uint32_t kGuidRoleHunter = 0xAEF2B3DAu;     // modifier_Role_Hunter
constexpr std::uint32_t kGuidRolePredator = 0x68502795u;   // modifier_Role_Predator

// Where a reward grant's role part came from (gms::damage::SplitReward's victim kind, or the player kill).
enum class GrantContext : int { Minion, Creature, PlayerKill, Other };

// A role mechanism: one seatStats key each.
enum class RolePart : int {
  GladiatorLastHit,    // lastHitMinionGoldModifier's part of a last hit on a lane minion
  TacticianProximity,  // proximityToMinionOrCreatureDeathGoldModifier's part of an area share
  PredatorKill,        // enemyPlayerKillGoldModifier's part of a kill
  PredatorAssist,      // enemyPlayerAssistGoldModifier's part of an assist
  GladiatorStored,     // modifier_Role_Gladiator's OnGrantGold: the stored minions
  PredatorWorkers,     // modifier_Role_Predator's OnGrantGold: the workers' x 2
  HunterProc,          // modifier_Role_Hunter's OnGrantGold: the + 30 procs
  Other,               // any other role part or handler change (none in build 854's data and scripts)
  Count
};
const char* RolePartKey(RolePart p);

// The role part a reward grant's bonus belongs to.
RolePart PartOfGrant(GrantContext c, bool lastHit);
// The role part an OnGrantGold handler's change belongs to, by the modifier's static GUID.
RolePart PartOfHandler(std::uint32_t staticGuid);

// The Hunter's damage bonus (modifier_Role_Hunter's ModifyDamageOutgoing: x `factor` on a creep): the part of an
// effective hit that the factor added, effective x (factor - 1) / factor [assumed: the handler's share of a combined
// ModifyDamageOutgoing]. 0 for a factor <= 1 or a non-positive hit.
double HunterBonusPart(double effective, double factor = 1.1);
// nGameObject.IsCreep (GameObject_IsCreep 0x00880FB0): NPC types 3 (jungle), 6, 7, 8 (the buff creatures, the parasite).
bool IsCreepType(int npcType);

class RoleLedger {
 public:
  // A reward grant's role part: `asBonus` when it went out as LabsPlayer_GrantGold's bonus (telemetry stat 19), false
  // when it was its own grant with the normal source ([host] role_gold_bonus 0).
  void OnRoleGold(int seat, GrantContext c, bool lastHit, double amount, bool asBonus);
  // An OnGrantGold handler changed a grant of `seat` by `delta` (stat 19 holds every such change).
  void OnHandlerChange(int seat, std::uint32_t staticGuid, double delta);
  void OnPredatorXp(int seat, double part);
  void OnHunterDamage(int seat, double hp);

  double Part(int seat, RolePart p) const;
  // What telemetry stat 19 holds of the host's role parts: the bonuses granted as a bonus and the handlers' changes.
  double InRoleStat(int seat) const;
  double PredatorXp(int seat) const;
  double HunterDamage(int seat) const;

 private:
  struct Seat {
    double parts[static_cast<int>(RolePart::Count)] = {};
    double asBonus = 0.0, handlers = 0.0, predatorXp = 0.0, hunterDamage = 0.0;
  };
  Seat seats_[kSeats];
};

// One seat's split, for the host's log line.
struct GoldSplit {
  bool telemetry = false;   // the seat's telemetry record was read
  double goldTotal = 0.0;   // stat 5
  double goldSources = 0.0; // stats 7-20 summed
  double xpTotal = 0.0;     // stat 6
  double xpSources = 0.0;   // stats 21-31 summed
  double goldRole = 0.0;    // stat 19
  double inRole = 0.0;      // the ledger's part of it (bonuses as bonus + handler changes)
  double harass = 0.0;      // goldRole - inRole (>= 0 when it adds up)
};

// Adds one seat's keys: from `stats` (the telemetry record's 107 floats, or nullptr when it couldn't be read) goldTotal,
// the gold sources (rounded to sum to goldTotal), xpTotal and the XP sources (likewise), goldRoleTacticianHarass (the
// rest of stat 19; left out when it is below 0.5); from the ledger the role keys, xpRolePredator and
// damageRoleHunterBonus.
GoldSplit AddGoldStats(Accumulator& acc, int seat, const float* stats, const RoleLedger& ledger);

}  // namespace gms::stats
