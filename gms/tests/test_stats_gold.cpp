// gms/core/stats/gold (M18, MSTATS2): gold and XP by source from the telemetry record, the role parts and the
// Tactician's harass as the rest of the Role stat (gms/core/stats/gold.h).
#include <cmath>
#include <cstdint>
#include <string>
#include <vector>

#include "core/stats/gold.h"
#include "core/stats/stats.h"
#include "tests/check.h"

using namespace gms::stats;

namespace {

bool Near(double a, double b) { return std::fabs(a - b) < 1e-3; }

}  // namespace

GMS_TEST(StatsGoldKeys) {
  CHECK_EQ(std::string(TelemetryKey(kStatGoldTotal)), std::string("goldTotal"));
  CHECK_EQ(std::string(TelemetryKey(kStatXpTotal)), std::string("xpTotal"));
  CHECK_EQ(std::string(TelemetryKey(7)), std::string("goldUnknown"));
  CHECK_EQ(std::string(TelemetryKey(11)), std::string("goldFromMinion"));
  CHECK_EQ(std::string(TelemetryKey(14)), std::string("goldFromWorkers"));
  CHECK_EQ(std::string(TelemetryKey(17)), std::string("goldStart"));
  CHECK_EQ(std::string(TelemetryKey(kStatGoldRole)), std::string("goldRole"));
  CHECK_EQ(std::string(TelemetryKey(20)), std::string("goldCheat"));
  CHECK_EQ(std::string(TelemetryKey(23)), std::string("xpFromPlayerKill"));
  CHECK_EQ(std::string(TelemetryKey(29)), std::string("xpPassive"));
  CHECK_EQ(std::string(TelemetryKey(30)), std::string("xpRole"));
  CHECK_EQ(std::string(TelemetryKey(kStatXpLast)), std::string("xpCheat"));
  CHECK(TelemetryKey(4) == nullptr);
  CHECK(TelemetryKey(32) == nullptr);
}

GMS_TEST(StatsGoldRoundToSum) {
  // 10.4 + 20.4 + 30.2 = 61: the floors give 60, the largest remainder (the first of the two .4) gets the 1
  const std::vector<std::int64_t> a = RoundToSum({10.4, 20.4, 30.2}, 61.0);
  CHECK_EQ(a.size(), static_cast<std::size_t>(3));
  CHECK_EQ(a[0], 11);
  CHECK_EQ(a[1], 20);
  CHECK_EQ(a[2], 30);
  // two up: 0.6 + 0.6 + 0.8 = 2
  const std::vector<std::int64_t> b = RoundToSum({0.6, 0.6, 0.8}, 2.0);
  CHECK_EQ(b[0] + b[1] + b[2], 2);
  CHECK_EQ(b[2], 1);
  // exact integers stay
  const std::vector<std::int64_t> c = RoundToSum({500.0, 0.0, 12.0}, 512.0);
  CHECK_EQ(c[0], 500);
  CHECK_EQ(c[1], 0);
  CHECK_EQ(c[2], 12);
  // values that don't add up to the total: each rounded on its own
  const std::vector<std::int64_t> d = RoundToSum({10.6, 20.0}, 50.0);
  CHECK_EQ(d[0], 11);
  CHECK_EQ(d[1], 20);
}

GMS_TEST(StatsGoldParts) {
  CHECK(PartOfGrant(GrantContext::Minion, true) == RolePart::GladiatorLastHit);
  CHECK(PartOfGrant(GrantContext::Minion, false) == RolePart::TacticianProximity);
  CHECK(PartOfGrant(GrantContext::Creature, false) == RolePart::TacticianProximity);
  CHECK(PartOfGrant(GrantContext::Creature, true) == RolePart::Other);
  CHECK(PartOfGrant(GrantContext::PlayerKill, true) == RolePart::PredatorKill);
  CHECK(PartOfGrant(GrantContext::PlayerKill, false) == RolePart::PredatorAssist);
  CHECK(PartOfGrant(GrantContext::Other, true) == RolePart::Other);
  CHECK(PartOfHandler(kGuidRoleGladiator) == RolePart::GladiatorStored);
  CHECK(PartOfHandler(kGuidRolePredator) == RolePart::PredatorWorkers);
  CHECK(PartOfHandler(kGuidRoleHunter) == RolePart::HunterProc);
  CHECK(PartOfHandler(kGuidRoleTactician) == RolePart::Other);
  CHECK(PartOfHandler(0) == RolePart::Other);
  CHECK_EQ(std::string(RolePartKey(RolePart::GladiatorLastHit)), std::string("goldRoleGladiatorLastHit"));
  CHECK_EQ(std::string(RolePartKey(RolePart::HunterProc)), std::string("goldRoleHunterProc"));
  CHECK_EQ(std::string(RolePartKey(RolePart::Other)), std::string("goldRoleOther"));
  // the Hunter's + 10 %: a 110 hit holds 10 of it
  CHECK(Near(HunterBonusPart(110.0), 10.0));
  CHECK(Near(HunterBonusPart(110.0, 1.0), 0.0));
  CHECK(Near(HunterBonusPart(-5.0), 0.0));
  CHECK(IsCreepType(3));
  CHECK(IsCreepType(6));
  CHECK(IsCreepType(8));
  CHECK(!IsCreepType(0));
  CHECK(!IsCreepType(2));
  CHECK(!IsCreepType(9));
  CHECK(!IsCreepType(12));
}

GMS_TEST(StatsGoldSplit) {
  // a Tactician-like seat 1 with every kind of role part: the default (as bonus) parts and the handlers are in stat 19,
  // a role_gold_bonus=0 part (its own grant, the normal source) is not; the rest of stat 19 is the harass
  RoleLedger ledger;
  ledger.OnRoleGold(1, GrantContext::Minion, false, 40.0, true);        // proximity, as bonus
  ledger.OnRoleGold(1, GrantContext::Minion, true, 12.0, false);        // a last hit's part as its own grant
  ledger.OnRoleGold(1, GrantContext::PlayerKill, false, 66.15, true);   // an assist's part
  ledger.OnHandlerChange(1, kGuidRoleHunter, 30.0);                     // a proc
  ledger.OnHandlerChange(1, kGuidRoleGladiator, 24.0);                  // the stored minions
  ledger.OnHandlerChange(1, 0x12345678u, 1.0);                          // an unknown handler
  ledger.OnPredatorXp(1, 39.0);
  ledger.OnHunterDamage(1, 10.0);
  ledger.OnHunterDamage(1, 5.25);
  ledger.OnRoleGold(-1, GrantContext::Minion, true, 5.0, true);         // ignored
  ledger.OnRoleGold(10, GrantContext::Minion, true, 5.0, true);         // ignored
  CHECK(Near(ledger.InRoleStat(1), 40.0 + 66.15 + 30.0 + 24.0 + 1.0));
  CHECK(Near(ledger.Part(1, RolePart::GladiatorLastHit), 12.0));

  float rec[kTelemetryStats] = {};
  rec[kStatGoldTotal] = 1442.25f;
  rec[11] = 300.3f;   // Minion
  rec[17] = 500.0f;   // Start
  rec[18] = 420.7f;   // Passive
  rec[kStatGoldRole] = 221.25f;  // 161.15 the host's parts + 60.10 harass
  rec[kStatXpTotal] = 800.5f;
  rec[25] = 700.25f;  // Minion
  rec[23] = 100.25f;  // PlayerKill

  Accumulator acc;
  const GoldSplit g = AddGoldStats(acc, 1, rec, ledger);
  CHECK(g.telemetry);
  CHECK(Near(g.goldTotal, 1442.25));
  CHECK(Near(g.goldSources, 1442.25));
  CHECK(Near(g.harass, 221.25 - 161.15));
  // the sources sum to the rounded total (1442): 300 + 500 + 421 + 221
  const double sum = acc.Stat(1, "goldUnknown") + acc.Stat(1, "goldFromMinion") + acc.Stat(1, "goldStart") +
                     acc.Stat(1, "goldPassive") + acc.Stat(1, "goldRole");
  CHECK(Near(sum, 1442.0));
  CHECK(Near(acc.Stat(1, "goldTotal"), 1442.0));
  CHECK(Near(acc.Stat(1, "goldStart"), 500.0));
  CHECK(Near(acc.Stat(1, "xpTotal"), 801.0));  // llround(800.5)
  CHECK(Near(acc.Stat(1, "xpFromMinion") + acc.Stat(1, "xpFromPlayerKill"), 801.0));
  CHECK(Near(acc.Stat(1, "goldRoleTacticianProximity"), 40.0));
  CHECK(Near(acc.Stat(1, "goldRoleGladiatorLastHit"), 12.0));
  CHECK(Near(acc.Stat(1, "goldRolePredatorAssist"), 66.15));
  CHECK(Near(acc.Stat(1, "goldRoleHunterProc"), 30.0));
  CHECK(Near(acc.Stat(1, "goldRoleGladiatorStored"), 24.0));
  CHECK(Near(acc.Stat(1, "goldRoleOther"), 1.0));
  CHECK(Near(acc.Stat(1, "goldRoleTacticianHarass"), 60.10));
  CHECK(Near(acc.Stat(1, "xpRolePredator"), 39.0));
  CHECK(Near(acc.Stat(1, "damageRoleHunterBonus"), 15.25));

  // without a record: only the ledger's keys
  Accumulator none;
  const GoldSplit n = AddGoldStats(none, 1, nullptr, ledger);
  CHECK(!n.telemetry);
  CHECK(Near(none.Stat(1, "goldTotal"), 0.0));
  CHECK(Near(none.Stat(1, "goldRoleHunterProc"), 30.0));

  // a seat with nothing of the role: no harass key, and a zero part makes no key in the line
  RoleLedger empty;
  float plain[kTelemetryStats] = {};
  plain[kStatGoldTotal] = 512.f;
  plain[17] = 500.f;
  plain[18] = 12.f;
  Accumulator acc2;
  const GoldSplit p = AddGoldStats(acc2, 0, plain, empty);
  CHECK(Near(p.harass, 0.0));
  CHECK(Near(acc2.Stat(0, "goldRoleTacticianHarass"), 0.0));
  acc2.Close(0);
  SeatFacts seats[kSeats];
  seats[0].present = true;
  seats[0].team = 1;
  const auto lines = acc2.BuildSeatStats(seats);
  CHECK_EQ(lines.size(), static_cast<std::size_t>(1));
  if (!lines.empty()) {
    CHECK(lines[0].stats.count("goldStart") == 1);
    CHECK(lines[0].stats.count("goldRoleHunterProc") == 0);
    CHECK(lines[0].stats.count("goldRole") == 0);
    CHECK_EQ(lines[0].stats.at("goldTotal"), static_cast<std::int64_t>(512));
  }
}
