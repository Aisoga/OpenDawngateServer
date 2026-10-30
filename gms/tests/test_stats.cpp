// gms/core/stats (M18, MSTATS1): the match statistics' accumulator without the engine (gms/core/stats/stats.h):
// the hit rules, kills, time dead, wells, the events, the timeline's samples, the lines it builds and the
// line budget's worst case. MSTATS3: the sources (the table, the fallbacks, the rows' sums), the heals and shields.
#include <cstdint>
#include <cstdio>
#include <string>
#include <vector>

#include "control/messages.h"
#include "core/protocol/messages.h"
#include "core/stats/sources.h"
#include "core/stats/stats.h"
#include "tests/check.h"

using namespace gms::stats;
namespace ctl = dggms::control;

namespace {

Hit MakeHit(int attacker, int target, Kind kind, float before, float after, unsigned desc = kDescPhysical) {
  Hit h;
  h.attackerSeat = attacker;
  h.targetSeat = target;
  h.targetKind = kind;
  h.descLo = desc;
  h.hpBefore = before;
  h.hpAfter = after;
  h.dealt = before - after;
  h.lethal = after <= 0.f;
  return h;
}

std::string Line(const ctl::Message& m) {
  std::string s = ctl::Encode(m);
  s.pop_back();
  return s;
}

}  // namespace

GMS_TEST(StatsKindOf) {
  CHECK(KindOf(true, 0) == Kind::Shapers);
  CHECK(KindOf(false, 0) == Kind::Minions && KindOf(false, 2) == Kind::Minions);
  CHECK(KindOf(false, 3) == Kind::Jungle);
  CHECK(KindOf(false, 6) == Kind::Objectives && KindOf(false, 8) == Kind::Objectives);
  CHECK(KindOf(false, 9) == Kind::Structures && KindOf(false, 14) == Kind::Structures &&
        KindOf(false, 15) == Kind::Structures);
  CHECK(KindOf(false, 13) == Kind::Guardian);
  CHECK(KindOf(false, 10) == Kind::Wells);
  CHECK(KindOf(false, 11) == Kind::Pets);
  CHECK(KindOf(false, 12) == Kind::Workers);
  CHECK(KindOf(false, 16) == Kind::Wards);
  CHECK(KindOf(false, -1) == Kind::Other && KindOf(false, 4) == Kind::Other);
  CHECK_EQ(std::string(KindName(Kind::Objectives)), std::string("Objectives"));
}

GMS_TEST(StatsHits) {
  Accumulator acc;
  // seat 0 hits seat 5's shaper for 120 effective (physical), seat 5 had a shield absorbing 30
  Hit h = MakeHit(0, 5, Kind::Shapers, 500.f, 380.f);
  h.absorbed = 30.f;
  acc.OnHit(h);
  // seat 0 hits a minion with magic, killing it: 40 HP left, 75 dealt -> 35 overkill
  Hit k = MakeHit(0, -1, Kind::Minions, 40.f, 0.f, kDescMagical);
  k.dealt = 75.f;
  acc.OnHit(k);
  // an overload hit (true damage) on the Guardian
  acc.OnHit(MakeHit(0, -1, Kind::Guardian, 1000.f, 990.f, kDescTrue | kDescOverload));
  // a tower (no seat) hits seat 5
  acc.OnHit(MakeHit(-1, 5, Kind::Shapers, 380.f, 300.f));
  // not counted: a health cost (no snapshot), a self-inflicted hit, a same-team hit
  Hit cost = MakeHit(-1, 5, Kind::Shapers, 300.f, 250.f);
  cost.snapshot = false;
  acc.OnHit(cost);
  acc.OnHit(MakeHit(5, 5, Kind::Shapers, 300.f, 280.f, kDescMagical | kDescSelfInflicted));
  Hit friendly = MakeHit(1, 0, Kind::Shapers, 300.f, 290.f);
  friendly.sameTeam = true;
  acc.OnHit(friendly);
  CHECK_EQ(acc.Hits(), 4u);
  CHECK_EQ(acc.Skipped(), 3u);

  CHECK_EQ(acc.Stat(0, "damageDealt"), 120.0 + 40.0 + 10.0);
  CHECK_EQ(acc.Stat(0, "damageDealtPhysical"), 120.0);
  CHECK_EQ(acc.Stat(0, "damageDealtMagical"), 40.0);
  CHECK_EQ(acc.Stat(0, "damageDealtTrue"), 10.0);
  CHECK_EQ(acc.Stat(0, "damageToShapers"), 120.0);
  CHECK_EQ(acc.Stat(0, "damageToMinions"), 40.0);
  CHECK_EQ(acc.Stat(0, "damageToGuardian"), 10.0);
  CHECK_EQ(acc.Stat(0, "damageOverload"), 10.0);
  CHECK_EQ(acc.Stat(0, "overkill"), 35.0);
  CHECK_EQ(acc.Stat(0, "largestHit"), 120.0);
  CHECK_EQ(acc.Stat(0, "damageTaken"), 0.0);

  CHECK_EQ(acc.Stat(5, "damageTaken"), 200.0);
  CHECK_EQ(acc.Stat(5, "damageTakenPhysical"), 200.0);
  CHECK_EQ(acc.Stat(5, "damageTakenFromShapers"), 120.0);
  CHECK_EQ(acc.Stat(5, "damageTakenFromNpcs"), 80.0);
  CHECK_EQ(acc.Stat(5, "damageAbsorbed"), 30.0);
  CHECK_EQ(acc.Stat(5, "damageDealt"), 0.0);
  CHECK_EQ(acc.Stat(1, "damageDealt"), 0.0);  // the same-team hit

  // a heal-like "hit" (HP rose) counts as 0, not negative
  acc.OnHit(MakeHit(0, 5, Kind::Shapers, 100.f, 150.f));
  CHECK_EQ(acc.Stat(0, "damageDealt"), 170.0);
}

GMS_TEST(StatsKills) {
  Accumulator acc;
  acc.OnKill(2, 1, Kind::Minions, 2);
  acc.OnKill(2, 1, Kind::Minions, 2);
  acc.OnKill(2, 1, Kind::Jungle, 0);  // a creep: neutral team
  acc.OnKill(2, 1, Kind::Guardian, 2);
  acc.OnKill(2, 1, Kind::Minions, 1);   // a deny: not counted
  acc.OnKill(2, 1, Kind::Shapers, 2);   // a player kill stays playerFinish's kills
  acc.OnKill(-1, 2, Kind::Minions, 1);  // no seat credited
  CHECK_EQ(acc.Stat(2, "killsMinions"), 2.0);
  CHECK_EQ(acc.Stat(2, "killsJungle"), 1.0);
  CHECK_EQ(acc.Stat(2, "killsGuardian"), 1.0);
  CHECK_EQ(acc.Stat(2, "killsShapers"), 0.0);
}

// WARDSTAT: a ward (NPC type 16) has no combatant in 854, so the accumulator writes no damageToWards or killsWards
// even if a hit or a kill of one were reported (the damage still counts in damageDealt).
GMS_TEST(StatsNoWardKeys) {
  Accumulator acc;
  acc.OnHit(MakeHit(2, -1, Kind::Wards, 100.f, 0.f));
  acc.OnKill(2, 1, Kind::Wards, 2);
  CHECK_EQ(acc.Stat(2, "damageDealt"), 100.0);
  SeatFacts facts[kSeats];
  facts[2].present = true;
  facts[2].team = 1;
  const auto lines = acc.BuildSeatStats(facts);
  CHECK(lines.size() == 1u);
  CHECK(lines[0].stats.count("damageToWards") == 0u);
  CHECK(lines[0].stats.count("killsWards") == 0u);
  CHECK(lines[0].stats.count("wardsDestroyed") == 0u);
}

GMS_TEST(StatsTimeDead) {
  Accumulator acc;
  acc.OnDeathTimer(3, 10000, 25000);  // 15 s
  acc.OnDeathTimer(3, 40000, 60000);  // 20 s
  acc.OnDeathTimer(4, 50000, 80000);  // cut at the end: 70000 -> 20 s
  acc.OnDeathTimer(6, 30000, 45000);
  acc.OnDeathTimer(6, 35000, 50000);  // a death before the respawn (a fixture's): the first ends at the second
  acc.Close(70000);
  CHECK_EQ(acc.Stat(3, "timeDeadMs"), 35000.0);
  CHECK_EQ(acc.Stat(4, "timeDeadMs"), 20000.0);
  CHECK_EQ(acc.Stat(6, "timeDeadMs"), 5000.0 + 15000.0);
  acc.Close(90000);  // once only
  CHECK_EQ(acc.Stat(4, "timeDeadMs"), 20000.0);
  acc.OnDeathTimer(4, 75000, 80000);  // after the end: nothing
  CHECK_EQ(acc.Stat(4, "timeDeadMs"), 20000.0);
}

GMS_TEST(StatsEvents) {
  Accumulator acc;
  acc.OnPlayerKill(95210, 3, 1, 7);
  acc.OnPlayerKill(96000, -1, 0, 3);   // executed by a tower: no team kill
  acc.OnPlayerKill(97000, 12, 2, 3);   // a bad seat counts as no killer
  acc.OnWellCaptured(200000, 2, 1, {0, 1, 11});
  acc.OnStructure(412000, 1, 9);
  acc.OnMatchEnd(900000, 1, 0);
  CHECK_EQ(acc.Events(), 6);
  CHECK_EQ(acc.Stat(0, "wellsCaptured"), 1.0);
  CHECK_EQ(acc.Stat(1, "wellsCaptured"), 1.0);
  const ctl::MatchTimeline tl = acc.BuildTimeline();
  CHECK_EQ(Line(tl),
           std::string("{\"t\":\"matchTimeline\",\"stepSec\":60,\"events\":[{\"ms\":95210,\"kind\":\"kill\",\"a\":3,"
                       "\"b\":7},{\"ms\":96000,\"kind\":\"kill\",\"a\":-1,\"b\":3},{\"ms\":97000,\"kind\":\"kill\","
                       "\"a\":-1,\"b\":3},{\"ms\":200000,\"kind\":\"well\",\"a\":1,\"b\":2},{\"ms\":412000,\"kind\":"
                       "\"structure\",\"a\":1,\"b\":9},{\"ms\":900000,\"kind\":\"guardian\",\"a\":1,\"b\":0}]}"));
  Accumulator s;
  s.OnMatchEnd(5000, 1, 2);
  CHECK_EQ(Line(s.BuildTimeline()),
           std::string("{\"t\":\"matchTimeline\",\"stepSec\":60,\"events\":[{\"ms\":5000,\"kind\":\"surrender\",\"a\":2,"
                       "\"b\":0}]}"));
  // a match under a minute without events: stepSec alone (the contract's line)
  Accumulator none;
  CHECK_EQ(Line(none.BuildTimeline()), std::string("{\"t\":\"matchTimeline\",\"stepSec\":60}"));
}

GMS_TEST(StatsEventCap) {
  Accumulator acc;
  for (int i = 0; i < kMaxEvents; ++i) acc.OnPlayerKill(1000 + i, 0, 1, 5);
  CHECK_EQ(acc.Events(), kMaxEvents);
  acc.OnPlayerKill(999999, 0, 1, 5);     // a kill past the cap: dropped
  acc.OnStructure(500000, 1, 9);         // a structure: replaces the newest kill
  CHECK_EQ(acc.Events(), kMaxEvents);
  TrimReport r;
  const ctl::MatchTimeline tl = acc.BuildTimeline(kLineBudget, &r);
  CHECK_EQ(r.eventsCapped, 2);
  CHECK_EQ(tl.events.size(), static_cast<std::size_t>(kMaxEvents));
  bool structure = false;
  for (const ctl::TimelineEvent& e : tl.events) structure = structure || e.kind == "structure";
  CHECK(structure);
  CHECK_EQ(tl.events.back().kind, std::string("structure"));  // sorted by time
}

GMS_TEST(StatsSamples) {
  Accumulator acc;
  SeatSample seats[kSeats];
  seats[0] = SeatSample{true, 1, 740.4, 310.0, 2, 6};
  seats[5] = SeatSample{true, 2, 700.0, 300.0, 2, 4};
  acc.Sample(0.0, seats);
  acc.Sample(59.9, seats);  // before the first minute: no sample
  CHECK_EQ(acc.Samples(), 0);
  acc.OnPlayerKill(61000, 0, 1, 5);
  acc.Sample(60.2, seats);
  acc.Sample(61.0, seats);  // the same minute
  CHECK_EQ(acc.Samples(), 1);
  seats[0].gold = 1310.0;
  seats[0].level = 4;
  seats[7] = SeatSample{true, 2, 650.0, 200.0, 1, 0};  // a seat seen from the second sample on
  acc.Sample(120.0, seats);
  seats[5].present = false;  // a seat gone: zeros
  acc.Sample(181.5, seats);
  CHECK_EQ(acc.Samples(), 3);
  const ctl::MatchTimeline tl = acc.BuildTimeline();
  CHECK_EQ(Line(tl),
           std::string("{\"t\":\"matchTimeline\",\"stepSec\":60,\"sec\":[60,120,180],\"seats\":["
                       "{\"index\":0,\"gold\":[740,1310,1310],\"xp\":[310,310,310],\"level\":[2,4,4],\"cs\":[6,6,6]},"
                       "{\"index\":5,\"gold\":[700,700,0],\"xp\":[300,300,0],\"level\":[2,2,0],\"cs\":[4,4,0]},"
                       "{\"index\":7,\"gold\":[0,650,650],\"xp\":[0,200,200],\"level\":[0,1,1],\"cs\":[0,0,0]}],"
                       "\"teams\":[{\"team\":1,\"gold\":[740,1310,1310],\"kills\":[1,1,1]},{\"team\":2,\"gold\":"
                       "[700,1350,650],\"kills\":[0,0,0]}],\"events\":[{\"ms\":61000,\"kind\":\"kill\",\"a\":0,\"b\":5}]}"));
  // a skipped minute (a hitch) samples the minute it reaches
  acc.Sample(301.0, seats);
  CHECK_EQ(acc.BuildTimeline().sec.back(), 300);
  // samples stop at kMaxSamples
  Accumulator lng;
  for (int m = 1; m <= kMaxSamples + 5; ++m) lng.Sample(m * 60.0, seats);
  CHECK_EQ(lng.Samples(), kMaxSamples);
}

GMS_TEST(StatsSeatLines) {
  Accumulator acc;
  Hit h = MakeHit(3, 7, Kind::Shapers, 1000.f, 818.f);
  acc.OnHit(h);
  acc.OnDeathTimer(3, 1000, 42000);
  acc.Close(900000);
  acc.AddSource(3, "ability", 1, 0, 5210.0, 0.0);
  acc.AddSource(3, "item", 9, 279208847, 812.0, 0.0);  // slot ignored for an item
  acc.AddSource(3, "ability", 1, 0, 0.4, 0.0);          // the same row
  acc.AddStat(7, "goldTotal", 7420.2);                  // a later package's key
  SeatFacts f[kSeats];
  f[3] = SeatFacts{true, false, 2000000046, 1, 274088913, 2, 1, 1, 0, 50, 9, 6000};
  f[7] = SeatFacts{true, true, 8, 2, 284823512, 1, 2, 3, 4, 88, 11, 7420};
  f[9] = SeatFacts{true, true, 10, 2, 284823512, 0, 0, 0, 0, 0, 1, 0};
  const std::vector<ctl::SeatStats> lines = acc.BuildSeatStats(f);
  CHECK_EQ(lines.size(), 3u);
  if (lines.size() != 3) return;
  CHECK_EQ(Line(lines[0]),
           std::string("{\"t\":\"seatStats\",\"index\":3,\"user\":2000000046,\"bot\":false,\"team\":1,\"noun\":274088913,"
                       "\"role\":2,\"stats\":{\"damageDealt\":182,\"damageDealtPhysical\":182,\"damageTaken\":0,"
                       "\"damageToShapers\":182,\"largestHit\":182,\"timeDeadMs\":41000},\"sources\":[{\"kind\":"
                       "\"ability\",\"slot\":1,\"id\":0,\"damage\":5210,\"heal\":0},{\"kind\":\"item\",\"id\":279208847,"
                       "\"damage\":812,\"heal\":0},{\"kind\":\"other\",\"id\":0,\"damage\":182,\"heal\":0}]}"));
  // a bot: user 0, playerFinish's keys in its stats
  CHECK_EQ(Line(lines[1]),
           std::string("{\"t\":\"seatStats\",\"index\":7,\"user\":0,\"bot\":true,\"team\":2,\"noun\":284823512,\"role\":1,"
                       "\"stats\":{\"assists\":4,\"creeps\":88,\"damageDealt\":0,\"damageTaken\":182,"
                       "\"damageTakenFromShapers\":182,\"damageTakenPhysical\":182,\"deaths\":3,\"gold\":7420,"
                       "\"goldTotal\":7420,\"kills\":2,\"level\":11,\"timeDeadMs\":0}}"));
  // each line decodes back to itself (the codec both sides share)
  for (const ctl::SeatStats& s : lines) {
    ctl::Message back;
    std::string err;
    CHECK(ctl::Decode(ctl::Encode(s), back, &err));
    CHECK_EQ(ctl::Encode(back), ctl::Encode(s));
  }
  // sources: at most kMaxSources, the largest first; MSTATS3: the rest folded into one `other` row
  Accumulator many;
  for (int i = 0; i < 30; ++i) many.AddSource(0, "item", 0, 1000 + i, 10.0 * (i + 1), 0.0);
  SeatFacts one[kSeats];
  one[0].present = true;
  const std::vector<ctl::SeatStats> m = many.BuildSeatStats(one);
  CHECK_EQ(m[0].sources.size(), static_cast<std::size_t>(kMaxSources));
  CHECK_EQ(m[0].sources[0].id, 1029);
  CHECK(!m[0].sources[0].slot);
  std::int64_t sum = 0;
  bool folded = false;
  for (const ctl::StatSource& r : m[0].sources) {
    sum += r.damage;
    if (r.kind == "other") folded = r.damage == 10 + 20 + 30 + 40 + 50 + 60 + 70;  // ids 1000-1006
  }
  CHECK(folded);
  CHECK_EQ(sum, static_cast<std::int64_t>(10 * 30 * 31 / 2));
}

// MSTATS3: the table and the fallbacks (sources.h).
GMS_TEST(StatsSourceClassify) {
  CHECK_EQ(SlotSource(0).kind, std::string("basic"));
  CHECK_EQ(SlotSource(2).kind, std::string("ability"));
  CHECK_EQ(SlotSource(2).slot, 4);
  CHECK_EQ(SlotSource(3).slot, 0);
  CHECK_EQ(SlotSource(6).slot, 3);
  CHECK_EQ(SlotSource(1).kind, std::string("other"));  // the ward
  CHECK_EQ(ItemSource(true, 0x1234).kind, std::string("spell"));
  CHECK_EQ(ItemSource(false, 0x0484AAA0).id, 0x0484AAA0);
  // the role passives by their SPIDs (FNV-1 of the lower-cased name)
  CHECK_EQ(RoleOfGuid(gms::AssetIdOf("modifier_Role_Hunter")), 2);
  CHECK_EQ(RoleOfGuid(gms::AssetIdOf("modifier_role_gladiator")), 0);
  CHECK_EQ(RoleOfGuid(0), -1);
  CHECK_EQ(RoleOfGuid(gms::AssetIdOf("modifier_Item_Drain")), -1);

  SourceTable t;
  t.BeginRebuild();
  t.Set(0x1111, SlotSource(3));
  t.Set(0x2222, ItemSource(false, 0x0484AAA0));  // Abolition's passive
  t.Set(0x2222, ItemSource(false, 0x79333B14));  // the same rebuild: the first setter wins
  t.Set(0, SlotSource(0));                      // GUID 0 is never stored
  CHECK_EQ(t.Size(), 2u);
  bool known = false;
  Source s = Classify(&t, 0x2222, 0, &known);
  CHECK(known);
  CHECK_EQ(s.id, 0x0484AAA0);
  // the next rebuild: an upgrade (Conquest) holds the shared passive now; a sold item's GUID stays
  t.BeginRebuild();
  t.Set(0x2222, ItemSource(false, 0x79333B14));
  CHECK_EQ(Classify(&t, 0x2222, 0).id, 0x79333B14);
  CHECK_EQ(Classify(&t, 0x1111, 0).slot, 0);
  // fallbacks: a role, IsBasic, IsItem, anything else
  s = Classify(&t, gms::AssetIdOf("modifier_Role_Hunter"), 0, &known);
  CHECK(known);
  CHECK_EQ(s.kind, std::string("role"));
  CHECK_EQ(s.role, 2);
  s = Classify(&t, 0x9999, kDescIsBasic | kDescPhysical, &known);
  CHECK(!known);
  CHECK_EQ(s.kind, std::string("basic"));
  CHECK_EQ(Classify(&t, 0x9999, kDescIsItem).kind, std::string("item"));
  CHECK_EQ(Classify(&t, 0x9999, kDescIsItem).id, 0);
  CHECK_EQ(Classify(nullptr, 0x1111, kDescMagical).kind, std::string("other"));
  CHECK_EQ(Classify(&t, 0, 0).kind, std::string("other"));
}

// MSTATS3-live2: the pet rule's check, whom a non-shaper attacker's hit is credited to.
GMS_TEST(StatsAttackerCredit) {
  CHECK(CreditOfAttacker(true, 3, -1) == AttackerCredit::Shaper);
  CHECK(CreditOfAttacker(true, -1, -1) == AttackerCredit::Shaper);
  CHECK(CreditOfAttacker(false, 4, 4) == AttackerCredit::Owner);       // a pet whose snapshot names its owner
  CHECK(CreditOfAttacker(false, 4, 7) == AttackerCredit::OtherSeat);   // credited to another seat
  CHECK(CreditOfAttacker(false, 4, -1) == AttackerCredit::OtherSeat);  // a seat's object without a shaper owner
  CHECK(CreditOfAttacker(false, -1, 4) == AttackerCredit::Lost);       // owned by a shaper, counts for nobody
  CHECK(CreditOfAttacker(false, -1, -1) == AttackerCredit::NoOwner);   // a minion, a creep, a tower
  CHECK_EQ(std::string(AttackerCreditName(AttackerCredit::Lost)), std::string("LOST"));
  CHECK_EQ(std::string(AttackerCreditName(AttackerCredit::Owner)), std::string("owner"));
}

// MSTATS3's done-when: the rows sum to damageDealt (an item passive and a spirit stone as their own rows), and to
// healingDone.
GMS_TEST(StatsSourcesSumToDamageDealt) {
  Accumulator acc;
  Hit h = MakeHit(2, 6, Kind::Shapers, 600.f, 500.f);  // Q: 100
  h.source = SlotSource(3);
  acc.OnHit(h);
  h = MakeHit(2, -1, Kind::Minions, 300.f, 0.f);  // a basic attack's killing blow: 300 effective
  h.dealt = 340.f;
  h.source = SlotSource(0);
  acc.OnHit(h);
  h = MakeHit(2, 6, Kind::Shapers, 500.f, 480.5f, kDescMagical);  // an item passive's proc: 19.5
  h.source = ItemSource(false, 0x0484AAA0);
  acc.OnHit(h);
  h = MakeHit(2, 6, Kind::Shapers, 480.5f, 470.f, kDescTrue);  // a spirit stone's modifier: 10.5
  h.source = PerkSource(0xABCDEF01);
  acc.OnHit(h);
  h = MakeHit(2, 6, Kind::Shapers, 470.f, 460.f, kDescMagical | kDescOverload);  // Q's overload: the same row
  h.source = SlotSource(3);
  acc.OnHit(h);
  // not counted, so in no row: own team, self-inflicted, health cost
  h = MakeHit(2, 3, Kind::Shapers, 400.f, 300.f);
  h.sameTeam = true;
  acc.OnHit(h);
  h = MakeHit(2, 2, Kind::Shapers, 400.f, 300.f, kDescSelfInflicted);
  acc.OnHit(h);
  h = MakeHit(2, 6, Kind::Shapers, 400.f, 300.f);
  h.snapshot = false;
  acc.OnHit(h);
  double damage = 0.0, heal = 0.0;
  acc.SourceSums(2, &damage, &heal);
  CHECK_EQ(acc.Stat(2, "damageDealt"), 440.0);
  CHECK_EQ(damage, acc.Stat(2, "damageDealt"));
  CHECK_EQ(heal, 0.0);
  SeatFacts f[kSeats];
  f[2].present = true;
  const std::vector<ctl::SeatStats> lines = acc.BuildSeatStats(f);
  CHECK_EQ(lines.size(), 1u);
  if (lines.size() != 1) return;
  CHECK_EQ(Line(lines[0]),
           std::string("{\"t\":\"seatStats\",\"index\":2,\"user\":0,\"bot\":false,\"team\":0,\"noun\":0,\"role\":0,"
                       "\"stats\":{\"damageDealt\":440,\"damageDealtMagical\":30,\"damageDealtPhysical\":400,"
                       "\"damageDealtTrue\":11,\"damageOverload\":10,\"damageTaken\":0,\"damageToMinions\":300,"
                       "\"damageToShapers\":140,\"largestHit\":300,\"overkill\":40,\"timeDeadMs\":0},\"sources\":["
                       "{\"kind\":\"basic\",\"id\":0,\"damage\":300,\"heal\":0},"
                       "{\"kind\":\"ability\",\"slot\":0,\"id\":0,\"damage\":110,\"heal\":0},"
                       "{\"kind\":\"item\",\"id\":75803296,\"damage\":20,\"heal\":0},"
                       "{\"kind\":\"perk\",\"id\":2882400001,\"damage\":11,\"heal\":0}]}"));
}

GMS_TEST(StatsHeals) {
  Accumulator acc;
  // seat 1 heals itself 80 of 100 (capped at max HP): healingSelf 80, overheal 20, its ability row's heal
  HealEvent e;
  e.healerSeat = 1;
  e.targetSeat = 1;
  e.targetPlayer = true;
  e.healed = 100.f;
  e.hpBefore = 420.f;
  e.hpAfter = 500.f;
  e.source = SlotSource(4);  // W
  acc.OnHeal(e);
  // seat 1 heals ally seat 3 for 50 with a spell; seat 3 received it
  e.targetSeat = 3;
  e.healed = 50.f;
  e.hpBefore = 100.f;
  e.hpAfter = 150.f;
  e.source = ItemSource(true, 0x1D74DD52);
  acc.OnHeal(e);
  // seat 1 heals a minion of its team (other) for 12 through an item passive
  e.targetSeat = -1;
  e.targetPlayer = false;
  e.healed = 12.f;
  e.hpBefore = 200.f;
  e.hpAfter = 212.f;
  e.source = ItemSource(false, 0x0484AAA0);
  acc.OnHeal(e);
  // lifedrain: on itself, from its basic attack's hit
  e.targetSeat = 1;
  e.targetPlayer = true;
  e.lifesteal = true;
  e.healed = 9.f;
  e.hpBefore = 300.f;
  e.hpAfter = 309.f;
  e.source = SlotSource(0);
  acc.OnHeal(e);
  e.lifesteal = false;
  // the Hunter's role heal on a creep kill: a role row and healRoleHunter
  e.healed = 25.f;
  e.hpBefore = 309.f;
  e.hpAfter = 334.f;
  e.source = Classify(nullptr, gms::AssetIdOf("modifier_Role_Hunter"), 0);
  acc.OnHeal(e);
  // not counted for any healer: a resurrect (HP 0 before), an enemy heal, no snapshot (seat 3 still receives it)
  e.hpBefore = 0.f;
  e.hpAfter = 500.f;
  acc.OnHeal(e);
  HealEvent enemy;
  enemy.healerSeat = 1;
  enemy.targetSeat = 7;
  enemy.targetPlayer = true;
  enemy.sameTeam = false;
  enemy.hpBefore = 10.f;
  enemy.hpAfter = 30.f;
  acc.OnHeal(enemy);
  HealEvent script;
  script.snapshot = false;
  script.targetSeat = 3;
  script.targetPlayer = true;
  script.hpBefore = 150.f;
  script.hpAfter = 160.f;
  acc.OnHeal(script);
  CHECK_EQ(acc.Stat(1, "healingDone"), 80.0 + 50.0 + 12.0 + 9.0 + 25.0);
  CHECK_EQ(acc.Stat(1, "healingSelf"), 80.0 + 9.0 + 25.0);
  CHECK_EQ(acc.Stat(1, "healingAllies"), 50.0);
  CHECK_EQ(acc.Stat(1, "healingOther"), 12.0);
  CHECK_EQ(acc.Stat(1, "lifeDrain"), 9.0);
  CHECK_EQ(acc.Stat(1, "overheal"), 20.0);
  CHECK_EQ(acc.Stat(1, "healRoleHunter"), 25.0);
  CHECK_EQ(acc.Stat(1, "healingReceived"), 80.0 + 9.0 + 25.0);
  CHECK_EQ(acc.Stat(3, "healingReceived"), 60.0);
  CHECK_EQ(acc.Stat(7, "healingReceived"), 20.0);
  CHECK_EQ(acc.Stat(1, "damageDealt"), 0.0);
  CHECK_EQ(acc.Heals(), 8u);
  double damage = 0.0, heal = 0.0;
  acc.SourceSums(1, &damage, &heal);
  CHECK_EQ(heal, acc.Stat(1, "healingDone"));
  CHECK_EQ(damage, 0.0);
  // shields: given to an ally counts, its own and an enemy's don't
  acc.OnShieldAbsorb(1, 3, true, 40.0);
  acc.OnShieldAbsorb(1, 1, true, 15.0);
  acc.OnShieldAbsorb(1, -1, true, 5.0);  // a minion of its team
  acc.OnShieldAbsorb(1, 7, false, 99.0);
  CHECK_EQ(acc.Stat(1, "shieldingDone"), 45.0);
  SeatFacts f[kSeats];
  f[1].present = true;
  const std::vector<ctl::SeatStats> lines = acc.BuildSeatStats(f);
  CHECK_EQ(lines.size(), 1u);
  if (lines.size() != 1) return;
  CHECK_EQ(Line(lines[0]),
           std::string("{\"t\":\"seatStats\",\"index\":1,\"user\":0,\"bot\":false,\"team\":0,\"noun\":0,\"role\":0,"
                       "\"stats\":{\"damageDealt\":0,\"damageTaken\":0,\"healRoleHunter\":25,\"healingAllies\":50,"
                       "\"healingDone\":176,\"healingOther\":12,\"healingReceived\":114,\"healingSelf\":114,"
                       "\"lifeDrain\":9,\"overheal\":20,\"shieldingDone\":45,\"timeDeadMs\":0},\"sources\":["
                       "{\"kind\":\"ability\",\"slot\":1,\"id\":0,\"damage\":0,\"heal\":80},"
                       "{\"kind\":\"spell\",\"id\":494198098,\"damage\":0,\"heal\":50},"
                       "{\"kind\":\"role\",\"id\":0,\"damage\":0,\"heal\":25},"
                       "{\"kind\":\"item\",\"id\":75803296,\"damage\":0,\"heal\":12},"
                       "{\"kind\":\"basic\",\"id\":0,\"damage\":0,\"heal\":9}]}"));
}

// The worst case the caps allow (MHIST1's TestTimelineFitsALine: 180 samples, ten seats with six-digit gold and XP, two
// teams, 300 structure events = 59,441 bytes) is trimmed under the budget: events go first.
GMS_TEST(StatsTimelineBudget) {
  Accumulator acc;
  SeatSample seats[kSeats];
  for (int i = 0; i < kSeats; ++i) seats[i] = SeatSample{true, i < 5 ? 1 : 2, 999999.0, 999999.0, 18, 9999};
  for (int m = 1; m <= kMaxSamples; ++m) acc.Sample(m * 60.0, seats);
  for (int i = 0; i < 150; ++i) acc.OnPlayerKill(10000000 + i, 1, 1, 7);
  for (int i = 0; i < 150; ++i) acc.OnStructure(10700000 + i, 2, 16);
  // untrimmed: over the budget (and close to MaxLine)
  ctl::MatchTimeline raw = acc.BuildTimeline(ctl::kMaxLine * 2);
  const std::size_t rawBytes = ctl::Encode(raw).size();
  CHECK(rawBytes > kLineBudget);
  CHECK(rawBytes < ctl::kMaxLine);
  TrimReport r;
  const ctl::MatchTimeline tl = acc.BuildTimeline(kLineBudget, &r);
  const std::size_t bytes = ctl::Encode(tl).size();
  CHECK(bytes <= kLineBudget);
  CHECK_EQ(r.bytes, bytes);
  CHECK(r.eventsDropped > 0);
  CHECK_EQ(r.coarsened, 0);
  CHECK_EQ(tl.sec.size(), static_cast<std::size_t>(kMaxSamples));
  std::printf("  the worst case: %zu bytes untrimmed, %zu after dropping %d of %zu events\n", rawBytes, bytes,
              r.eventsDropped, raw.events.size());
  // the kills went first: every structure event that fits is kept
  int kills = 0, structures = 0;
  for (const ctl::TimelineEvent& e : tl.events) (e.kind == "kill" ? kills : structures)++;
  CHECK(structures == 150 || kills == 0);
  // a tighter budget coarsens the samples once the events are gone; the last sample stays
  TrimReport r2;
  const ctl::MatchTimeline small = acc.BuildTimeline(16 * 1024, &r2);
  CHECK(ctl::Encode(small).size() <= 16 * 1024);
  CHECK(small.events.empty());
  CHECK(r2.coarsened >= 1);
  CHECK_EQ(small.stepSec, 60 << r2.coarsened);
  CHECK_EQ(small.sec.back(), static_cast<std::int64_t>(kMaxSamples) * 60);
  CHECK_EQ(small.seats[0].gold.size(), small.sec.size());
  CHECK_EQ(small.teams[1].kills.size(), small.sec.size());
  // it still decodes
  ctl::Message back;
  std::string err;
  CHECK(ctl::Decode(ctl::Encode(tl), back, &err));
}
