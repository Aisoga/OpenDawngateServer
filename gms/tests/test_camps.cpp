// gms/core/matchflow/camps.h (CAMPICONS): a jungle camp's minimap icon as each team's knowledge.
#include "core/matchflow/camps.h"
#include "tests/check.h"

using gms::flow::CampKnowledge;

namespace {
constexpr uint8_t kMortal = 0x02, kSpirit = 0x04, kBoth = 0x06;
}

GMS_TEST(CampIconsNoIconBeforeTheFirstSpawn) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealSmallS);
  CHECK(!k.Spawned());
  CHECK_EQ(k.Bits(), 0x01);  // the NPC bit alone: no team draws the icon
  // seeing the empty spot before the first spawn changes nothing (no respawn time: the first spawn is public anyway)
  CHECK(k.Observe(30.0, 0, kBoth, false, -1.0).empty());
  CHECK_EQ(k.Bits(), 0x01);
  const auto c = k.OnSpawned();
  CHECK_EQ(c.size(), 2u);  // both teams: one change each
  CHECK_EQ(k.Bits(), 0x07);
  CHECK(k.Icon(1));
  CHECK(k.Icon(2));
  CHECK(k.OnSpawned().empty());  // nothing new
}

GMS_TEST(CampIconsWitnessedDeathOffUntilTheRespawn) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealSmallS);
  k.OnSpawned();
  // Mortal kills the camp in sight; Spirit doesn't see it
  const auto c = k.OnCleared(static_cast<uint8_t>(0x01 | kMortal), 160.0);
  CHECK_EQ(c.size(), 1u);
  CHECK_EQ(c[0].team, 1);
  CHECK(!c[0].icon);
  CHECK_EQ(c[0].knownRespawnAt, 160.0);
  CHECK_EQ(k.Bits(), static_cast<uint8_t>(0x01 | kSpirit));  // Mortal's icon off, Spirit's stays (stale)
  CHECK_EQ(k.KnownRespawnAt(1), 160.0);
  CHECK_EQ(k.KnownRespawnAt(2), -1.0);
  // Mortal still sees the empty spot: no change
  CHECK(k.Observe(120.0, 0, kMortal, false, 160.0).empty());
  CHECK_EQ(k.Bits(), static_cast<uint8_t>(0x01 | kSpirit));
  // the known respawn time comes: on again without sight
  const auto r = k.Observe(160.0, 0, 0, false, 160.0);
  CHECK_EQ(r.size(), 1u);
  CHECK_EQ(r[0].team, 1);
  CHECK(r[0].icon);
  CHECK_EQ(k.Bits(), 0x07);
  CHECK_EQ(k.KnownRespawnAt(1), -1.0);
}

GMS_TEST(CampIconsUnwitnessedDeathKeepsTheStaleIcon) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealSmallS);
  k.OnSpawned();
  CHECK(k.OnCleared(0x01, 200.0).empty());  // only the NPC bit saw it: nobody's knowledge changes
  CHECK_EQ(k.Bits(), 0x07);
  // a team that sees the empty spot outside the reveal window learns nothing (no EMPTY_UNKNOWN icon)
  CHECK(k.Observe(150.0, 0, kBoth, false, 200.0).empty());
  CHECK_EQ(k.Bits(), 0x07);
  CHECK_EQ(k.KnownRespawnAt(1), -1.0);
  // the respawn: nothing changes for either team (their icons were on)
  CHECK(k.OnSpawned().empty());
}

GMS_TEST(CampIconsTheEnemyTeamIsUnaffected) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealBuffS);
  k.OnSpawned();
  k.OnCleared(kSpirit, 500.0);  // Spirit's clear, Spirit watching
  CHECK(k.Icon(1));
  CHECK(!k.Icon(2));
  // Mortal sees nothing of it; its icon stays through every observation until the respawn
  for (double t = 201.0; t < 440.0; t += 1.0) CHECK(k.Observe(t, 0, 0, false, 500.0).empty());
  CHECK(k.Icon(1));
  CHECK(!k.Icon(2));
}

GMS_TEST(CampIconsRevealWindow) {
  // a buff camp (the Boar's too): 60 s before its respawn, seeing the empty spot makes the time known
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealBuffS);
  k.OnSpawned();
  k.OnCleared(0x01, 500.0);  // unwitnessed
  CHECK(k.Observe(439.0, 0, kMortal, false, 500.0).empty());  // 61 s before: outside
  const auto c = k.Observe(440.0, 0, kMortal, false, 500.0);  // 60 s before: inside
  CHECK_EQ(c.size(), 1u);
  CHECK_EQ(c[0].team, 1);
  CHECK(c[0].icon);  // no change on the minimap: the stale icon stays on
  CHECK_EQ(c[0].knownRespawnAt, 500.0);
  CHECK_EQ(k.KnownRespawnAt(1), 500.0);
  CHECK_EQ(k.KnownRespawnAt(2), -1.0);
  CHECK_EQ(k.Bits(), 0x07);
  // the time comes: the known time is spent (the icon was already on)
  const auto r = k.Observe(500.0, 0, 0, false, 500.0);
  CHECK_EQ(r.size(), 1u);
  CHECK_EQ(k.KnownRespawnAt(1), -1.0);
  // a small camp: 10 s
  CampKnowledge s;
  s.Init(gms::flow::kCampRevealSmallS);
  s.OnSpawned();
  s.OnCleared(0x01, 100.0);
  CHECK(s.Observe(89.0, 0, kSpirit, false, 100.0).empty());
  CHECK_EQ(s.Observe(90.0, 0, kSpirit, false, 100.0).size(), 1u);
  CHECK_EQ(s.KnownRespawnAt(2), 100.0);
}

GMS_TEST(CampIconsSeenAliveTurnsTheIconOn) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealSmallS);
  k.OnSpawned();
  k.OnCleared(kBoth, 160.0);
  CHECK_EQ(k.Bits(), 0x01);
  // the camp is back (the director's spawn would say so too); a team that sees a member gets the icon at once
  const auto c = k.Observe(161.0, kSpirit, kSpirit, true, -1.0);
  CHECK_EQ(c.size(), 2u);  // Spirit: seen alive; Mortal: its known time came
  CHECK_EQ(k.Bits(), 0x07);
  // an alive camp seen while the icon is on: nothing
  CHECK(k.Observe(162.0, kBoth, kBoth, true, -1.0).empty());
}

GMS_TEST(CampIconsSpectatorTruth) {
  CHECK_EQ(gms::flow::CampTruthBits(true), 0x07);   // every team's bit: any spectator view mask draws it
  CHECK_EQ(gms::flow::CampTruthBits(false), 0x01);  // empty: none
}

GMS_TEST(CampIconsNeverRespawns) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealSmallS);
  k.OnSpawned();
  const auto c = k.OnCleared(kMortal, -1.0);  // no respawn: off for the witness for good
  CHECK_EQ(c.size(), 1u);
  CHECK_EQ(c[0].knownRespawnAt, -1.0);
  CHECK(k.Observe(1.0e6, 0, kBoth, false, -1.0).empty());
  CHECK(!k.Icon(1));
  CHECK(k.Icon(2));
}

// CAMPICONS step 2: the world clock (a fixed 300-s effect) only for a time known one clock length ahead.
GMS_TEST(CampClockWitnessedBuffClearGetsAClock) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealBuffS);
  k.OnSpawned();
  double due[2] = {-1.0, -1.0};
  k.OnCleared(kMortal, 400.0);  // cleared at 100 s, respawn 300 s later
  auto a = gms::flow::PlanCampClocks(k, 100.0, due);
  CHECK_EQ(a.size(), 1u);
  CHECK_EQ(a[0].team, 1);
  CHECK(a[0].create);
  CHECK_EQ(a[0].dueAt, 400.0);
  due[0] = 400.0;
  CHECK(gms::flow::PlanCampClocks(k, 101.0, due).empty());  // running: nothing
  CHECK(gms::flow::PlanCampClocks(k, 399.0, due).empty());
  // the respawn: the known time is gone, the clock goes
  k.OnSpawned();
  a = gms::flow::PlanCampClocks(k, 400.0, due);
  CHECK_EQ(a.size(), 1u);
  CHECK_EQ(a[0].team, 1);
  CHECK(!a[0].create);
}

GMS_TEST(CampClockTheKnownTimeComesFirst) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealBuffS);
  k.OnSpawned();
  k.OnCleared(kBoth, 400.0);
  double due[2] = {400.0, 400.0};
  // the director's once-a-second step reaches the time before the spawn's own call: both clocks go
  auto a = gms::flow::PlanCampClocks(k, 400.5, due);
  CHECK_EQ(a.size(), 2u);
  CHECK(!a[0].create);
  CHECK(!a[1].create);
}

GMS_TEST(CampClockNoneForAShortRespawnOrALateLearnedTime) {
  CampKnowledge small;
  small.Init(gms::flow::kCampRevealSmallS);
  small.OnSpawned();
  small.OnCleared(kMortal, 160.0);  // a small camp: 60 s
  double due[2] = {-1.0, -1.0};
  CHECK(gms::flow::PlanCampClocks(small, 100.0, due).empty());
  CampKnowledge buff;
  buff.Init(gms::flow::kCampRevealBuffS);
  buff.OnSpawned();
  buff.OnCleared(0x01, 400.0);  // nobody saw it
  CHECK(gms::flow::PlanCampClocks(buff, 100.0, due).empty());
  buff.Observe(350.0, 0, kSpirit, false, 400.0);  // Spirit learns the time 50 s before (the reveal window)
  CHECK_EQ(buff.KnownRespawnAt(2), 400.0);
  CHECK(gms::flow::PlanCampClocks(buff, 350.0, due).empty());  // 50 s left: the 300-s dial would lie
}

GMS_TEST(CampClockOnlyTheWitnessesTeam) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealBuffS);
  k.OnSpawned();
  k.OnCleared(kSpirit, 500.0);
  double due[2] = {-1.0, -1.0};
  const auto a = gms::flow::PlanCampClocks(k, 200.0, due);
  CHECK_EQ(a.size(), 1u);
  CHECK_EQ(a[0].team, 2);
  CHECK(a[0].create);
  // within the tolerance (the death dated by the last tick): still a clock
  double none[2] = {-1.0, -1.0};
  CHECK_EQ(gms::flow::PlanCampClocks(k, 201.5, none).size(), 1u);
  CHECK(gms::flow::PlanCampClocks(k, 203.0, none).empty());
}
