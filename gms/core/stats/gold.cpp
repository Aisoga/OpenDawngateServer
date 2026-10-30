// M18 (MSTATS2): gold and XP by source and the roles' bonuses; see gold.h.
#include "core/stats/gold.h"

#include <algorithm>
#include <cmath>
#include <numeric>

namespace gms::stats {
namespace {

bool ValidSeat(int seat) { return seat >= 0 && seat < kSeats; }

// stats 5-31: nGoldSourceType + 7, nXPSourceType + 21 (GlobalDefinitions.lua)
const char* const kKeys[] = {
    "goldTotal",  // 5
    "xpTotal",  // 6
    "goldUnknown",  // 7  Unknown
    "goldFromTower",  // 8  Tower
    "goldFromPlayerKill",  // 9  PlayerKill
    "goldFromAssist",  // 10 PlayerAssist
    "goldFromMinion",  // 11 Minion
    "goldFromJungle",  // 12 JungleCreep
    "goldFromObjective",  // 13 Objective
    "goldFromWorkers",  // 14 Follower
    "goldFromWellPassive",  // 15 WellPassive
    "goldFromWellBounty",  // 16 WellBounty
    "goldStart",  // 17 Start
    "goldPassive",  // 18 Passive
    "goldRole",  // 19 Role
    "goldCheat",  // 20 Cheat
    "xpUnknown",  // 21 Unknown
    "xpFromTower",  // 22 Tower
    "xpFromPlayerKill",  // 23 PlayerKill
    "xpFromAssist",  // 24 PlayerAssist
    "xpFromMinion",  // 25 Minion
    "xpFromJungle",  // 26 JungleCreep
    "xpFromObjective",  // 27 Objective
    "xpFromWorkers",  // 28 Follower
    "xpPassive",  // 29 Passive
    "xpRole",  // 30 Role
    "xpCheat",  // 31 Cheat
};

// Adds the stats first..last of `stats` rounded to sum to `total`; returns their unrounded sum.
double AddSplit(Accumulator& acc, int seat, const float* stats, int first, int last, double total) {
  std::vector<double> v;
  for (int s = first; s <= last; ++s) v.push_back(std::max(0.0, static_cast<double>(stats[s])));
  const std::vector<std::int64_t> r = RoundToSum(v, total);
  for (int s = first; s <= last; ++s)
    if (r[s - first] != 0) acc.AddStat(seat, TelemetryKey(s), static_cast<double>(r[s - first]));
  return std::accumulate(v.begin(), v.end(), 0.0);
}

}  // namespace

const char* TelemetryKey(int stat) {
  if (stat < kStatGoldTotal || stat > kStatXpLast) return nullptr;
  return kKeys[stat - kStatGoldTotal];
}

std::vector<std::int64_t> RoundToSum(const std::vector<double>& values, double total) {
  const std::size_t n = values.size();
  std::vector<std::int64_t> out(n);
  std::int64_t sum = 0;
  for (std::size_t i = 0; i < n; ++i) {
    out[i] = static_cast<std::int64_t>(std::floor(values[i]));
    sum += out[i];
  }
  std::int64_t diff = static_cast<std::int64_t>(std::llround(total)) - sum;
  if (diff < 0 || diff > static_cast<std::int64_t>(n)) {
    // the values don't add up to the total (more than rounding): each rounded on its own
    for (std::size_t i = 0; i < n; ++i) out[i] = static_cast<std::int64_t>(std::llround(values[i]));
    return out;
  }
  std::vector<std::size_t> order(n);
  std::iota(order.begin(), order.end(), std::size_t{0});
  // the largest fractional parts first; ties by index, so the result doesn't depend on the sort
  std::stable_sort(order.begin(), order.end(), [&](std::size_t a, std::size_t b) {
    return values[a] - std::floor(values[a]) > values[b] - std::floor(values[b]);
  });
  for (std::size_t k = 0; k < n && diff > 0; ++k, --diff) ++out[order[k]];
  return out;
}

const char* RolePartKey(RolePart p) {
  switch (p) {
    case RolePart::GladiatorLastHit: return "goldRoleGladiatorLastHit";
    case RolePart::TacticianProximity: return "goldRoleTacticianProximity";
    case RolePart::PredatorKill: return "goldRolePredatorKill";
    case RolePart::PredatorAssist: return "goldRolePredatorAssist";
    case RolePart::GladiatorStored: return "goldRoleGladiatorStored";
    case RolePart::PredatorWorkers: return "goldRolePredatorWorkers";
    case RolePart::HunterProc: return "goldRoleHunterProc";
    default: return "goldRoleOther";
  }
}

RolePart PartOfGrant(GrantContext c, bool lastHit) {
  switch (c) {
    case GrantContext::Minion: return lastHit ? RolePart::GladiatorLastHit : RolePart::TacticianProximity;
    // a creature's last hit (lastHitCreatureGoldModifier, 1.0 for every role) has no mechanism of its own
    case GrantContext::Creature: return lastHit ? RolePart::Other : RolePart::TacticianProximity;
    case GrantContext::PlayerKill: return lastHit ? RolePart::PredatorKill : RolePart::PredatorAssist;
    default: return RolePart::Other;
  }
}

RolePart PartOfHandler(std::uint32_t staticGuid) {
  switch (staticGuid) {
    case kGuidRoleGladiator: return RolePart::GladiatorStored;
    case kGuidRolePredator: return RolePart::PredatorWorkers;
    case kGuidRoleHunter: return RolePart::HunterProc;
    default: return RolePart::Other;
  }
}

double HunterBonusPart(double effective, double factor) {
  if (!(effective > 0.0) || !(factor > 1.0)) return 0.0;
  return effective * (factor - 1.0) / factor;
}

bool IsCreepType(int npcType) { return npcType == 3 || (npcType >= 6 && npcType <= 8); }

void RoleLedger::OnRoleGold(int seat, GrantContext c, bool lastHit, double amount, bool asBonus) {
  if (!ValidSeat(seat) || !(amount > 0.0)) return;
  seats_[seat].parts[static_cast<int>(PartOfGrant(c, lastHit))] += amount;
  if (asBonus) seats_[seat].asBonus += amount;
}

void RoleLedger::OnHandlerChange(int seat, std::uint32_t staticGuid, double delta) {
  if (!ValidSeat(seat) || delta == 0.0) return;
  seats_[seat].parts[static_cast<int>(PartOfHandler(staticGuid))] += delta;
  seats_[seat].handlers += delta;
}

void RoleLedger::OnPredatorXp(int seat, double part) {
  if (ValidSeat(seat) && part > 0.0) seats_[seat].predatorXp += part;
}

void RoleLedger::OnHunterDamage(int seat, double hp) {
  if (ValidSeat(seat) && hp > 0.0) seats_[seat].hunterDamage += hp;
}

double RoleLedger::Part(int seat, RolePart p) const {
  return ValidSeat(seat) && p != RolePart::Count ? seats_[seat].parts[static_cast<int>(p)] : 0.0;
}

double RoleLedger::InRoleStat(int seat) const {
  return ValidSeat(seat) ? seats_[seat].asBonus + seats_[seat].handlers : 0.0;
}

double RoleLedger::PredatorXp(int seat) const { return ValidSeat(seat) ? seats_[seat].predatorXp : 0.0; }

double RoleLedger::HunterDamage(int seat) const { return ValidSeat(seat) ? seats_[seat].hunterDamage : 0.0; }

GoldSplit AddGoldStats(Accumulator& acc, int seat, const float* stats, const RoleLedger& ledger) {
  GoldSplit g;
  if (!ValidSeat(seat)) return g;
  for (int p = 0; p < static_cast<int>(RolePart::Count); ++p)
    acc.AddStat(seat, RolePartKey(static_cast<RolePart>(p)), ledger.Part(seat, static_cast<RolePart>(p)));
  acc.AddStat(seat, "xpRolePredator", ledger.PredatorXp(seat));
  acc.AddStat(seat, "damageRoleHunterBonus", ledger.HunterDamage(seat));
  g.inRole = ledger.InRoleStat(seat);
  if (!stats) return g;
  g.telemetry = true;
  g.goldTotal = std::max(0.0, static_cast<double>(stats[kStatGoldTotal]));
  g.xpTotal = std::max(0.0, static_cast<double>(stats[kStatXpTotal]));
  g.goldRole = std::max(0.0, static_cast<double>(stats[kStatGoldRole]));
  acc.AddStat(seat, TelemetryKey(kStatGoldTotal), static_cast<double>(std::llround(g.goldTotal)));
  acc.AddStat(seat, TelemetryKey(kStatXpTotal), static_cast<double>(std::llround(g.xpTotal)));
  g.goldSources = AddSplit(acc, seat, stats, kStatGoldFirst, kStatGoldLast, g.goldTotal);
  g.xpSources = AddSplit(acc, seat, stats, kStatXpFirst, kStatXpLast, g.xpTotal);
  g.harass = g.goldRole - g.inRole;
  if (g.harass >= 0.5) acc.AddStat(seat, "goldRoleTacticianHarass", g.harass);
  return g;
}

}  // namespace gms::stats
