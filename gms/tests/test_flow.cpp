// gms/core/matchflow (D1c-live): the match flow's engine-free rules (docs/MATCH_FLOW.md).
#include <cmath>
#include <string>
#include <vector>

#include "core/matchflow/flow.h"
#include "tests/check.h"

using namespace gms::flow;

GMS_TEST(FlowWaveSchedule) {
  WaveSchedule w;  // SM_Map_v5: minionSpawnDelay1 60, minionSpawnInterval1 30
  CHECK(!w.Due(0.0));
  CHECK(!w.Due(59.96));
  CHECK(w.Due(60.0));
  w.Fired();
  CHECK_EQ(w.TimeOf(w.next), 90.0);
  CHECK(!w.Due(89.9));
  CHECK(w.Due(90.03));
  w.Fired();
  CHECK_EQ(w.TimeOf(4), 180.0);
  // a late tick fires the waves one by one (one per call)
  WaveSchedule late;
  int fired = 0;
  for (int k = 0; k < 10 && late.Due(125.0); ++k, ++fired) late.Fired();
  CHECK_EQ(fired, 3);  // 60, 90, 120
  // single wave, none, limit
  WaveSchedule once;
  once.interval = 0.0;
  CHECK(once.Due(60.0));
  once.Fired();
  CHECK(!once.Due(1000.0));
  WaveSchedule none;
  none.delay = -1.0;
  CHECK(!none.Due(1000.0));
  WaveSchedule limited;
  limited.limit = 2;
  limited.Fired();
  limited.Fired();
  CHECK(!limited.Due(1000.0));
}

GMS_TEST(FlowCampTimer) {
  CampTimer c;  // a small Fish camp: first 90 s, respawn 60 s (marker override)
  c.Init(90.0, 60.0);
  CHECK(!c.Due(89.0));
  CHECK(c.Due(90.0));
  c.OnSpawned();
  CHECK(c.Alive());
  CHECK(!c.Due(500.0));  // alive: never due
  c.OnCleared(131.5);
  CHECK(!c.Alive());
  CHECK_EQ(c.DueAt(), 191.5);
  CHECK(!c.Due(191.0));
  CHECK(c.Due(191.5));
  c.OnSpawned();
  CHECK_EQ(c.Spawns(), 2);
  // no respawn (the parasite's camp): cleared means never again
  CampTimer p;
  p.Init(0.0, 0.0);
  CHECK(p.Due(0.0));
  p.OnSpawned();
  p.OnCleared(400.0);
  CHECK(!p.Due(100000.0));
}

GMS_TEST(FlowEndReason) {
  CHECK_EQ(EndReasonForDeadGuardian(2), 1u);  // Spirit Guardian died: Mortal wins
  CHECK_EQ(EndReasonForDeadGuardian(1), 3u);  // Mortal Guardian died: Spirit wins
  CHECK_EQ(EndReasonForDeadGuardian(0), 0u);
  CHECK_EQ(WinnerOfReason(1), 1);
  CHECK_EQ(WinnerOfReason(2), 1);
  CHECK_EQ(WinnerOfReason(3), 2);
  CHECK_EQ(WinnerOfReason(4), 2);
  CHECK_EQ(WinnerOfReason(0), 0xFF);
  CHECK(IsSurrender(2) && IsSurrender(4) && !IsSurrender(1) && !IsSurrender(3));
}

GMS_TEST(FlowEnergyLineTime) {
  CHECK_EQ(EnergyLineTime(0.f, 123456), 0ull);
  CHECK_EQ(EnergyLineTime(-1.f, 123456), ~0ull);
  CHECK_EQ(EnergyLineTime(480.f, 100000), 580000ull);  // R2: t + 480000
  CHECK_EQ(EnergyLineTime(0.5f, 1000), 1500ull);
}

GMS_TEST(FlowTowerSlots) {
  CHECK_EQ(TeamIndex(1), 0);
  CHECK_EQ(TeamIndex(2), 1);
  CHECK_EQ(TeamIndex(0), -1);
  CHECK_EQ(BindingTier("Binding_Mortal_Outer"), 0);
  CHECK_EQ(BindingTier("Binding_Spirit_Middle"), 1);
  CHECK_EQ(BindingTier("Binding_Spirit_Inner"), 2);
  CHECK_EQ(BindingTier("Binding_Mortal_Middle.Noun"), 1);  // the asset's own name
  CHECK_EQ(BindingTeam("Binding_Spirit_Outer.Noun"), 2);
  CHECK_EQ(BindingTier("Binding_Shared_Melee"), -1);
  CHECK_EQ(BindingTier("nub_mortal_v5_A"), -1);
  CHECK_EQ(BindingTeam("Binding_Mortal_Inner"), 1);
  CHECK_EQ(BindingTeam("Binding_Spirit_Outer"), 2);
  CHECK_EQ(BindingTeam("GuardianSpirit_v5"), 0);
}

GMS_TEST(FlowDistanceToPolyline) {
  // blue_top's first four knots and Binding_Mortal_Inner of the top lane at (-158.4, -111.0): 6.18 units from the
  // polyline (match_flow.py towers says 6.4, measured to the knots' full precision)
  std::vector<Point> top = {{-210.5f, -184.9f, 0.f}, {-171.7f, -146.0f, 0.f}, {-153.1f, -114.8f, 0.f}, {-131.6f, -44.2f, 0.f}};
  const float d = DistanceToPolyline(-158.4f, -111.0f, top);
  CHECK(std::fabs(d - 6.18f) < 0.05f);
  CHECK(DistanceToPolyline(0.f, 0.f, {}) > 1e30f);
  std::vector<Point> one = {{3.f, 4.f, 0.f}};
  CHECK(std::fabs(DistanceToPolyline(0.f, 0.f, one) - 5.f) < 1e-4f);
  // beyond the end: the distance to the last point
  std::vector<Point> seg = {{0.f, 0.f, 0.f}, {10.f, 0.f, 0.f}};
  CHECK(std::fabs(DistanceToPolyline(13.f, 4.f, seg) - 5.f) < 1e-4f);
}

GMS_TEST(FlowFormation) {
  // lane toward +x: the first row (0-2) 2.5 ahead, the second (3-5) at the spawner; columns right, centre, left (left
  // of the lane is +y)
  float dx = 0, dy = 0;
  FormationOffset(0, 1.f, 0.f, 2.5f, &dx, &dy);
  CHECK(std::fabs(dx - 2.5f) < 1e-5f && std::fabs(dy + 2.5f) < 1e-5f);
  FormationOffset(1, 1.f, 0.f, 2.5f, &dx, &dy);
  CHECK(std::fabs(dx - 2.5f) < 1e-5f && std::fabs(dy) < 1e-5f);
  FormationOffset(5, 1.f, 0.f, 2.5f, &dx, &dy);
  CHECK(std::fabs(dx) < 1e-5f && std::fabs(dy - 2.5f) < 1e-5f);
  FormationOffset(6, 1.f, 0.f, 2.5f, &dx, &dy);  // a seventh (the empowered Strider) behind the spawner
  CHECK(std::fabs(dx + 2.5f) < 1e-5f && std::fabs(dy + 2.5f) < 1e-5f);
  // a lane toward +y: "right" is +x
  FormationOffset(0, 0.f, 1.f, 2.f, &dx, &dy);
  CHECK(std::fabs(dx - 2.f) < 1e-5f && std::fabs(dy - 2.f) < 1e-5f);
}

GMS_TEST(FlowStaggeredWave) {
  // D1k: six minions, 0.1 s apart, from the wave at 60 s: one at 60.00, then one every 0.1 s up to 60.5
  CHECK_EQ(StaggeredDueCount(60.0, 0.1, 6, 59.99), 0);
  CHECK_EQ(StaggeredDueCount(60.0, 0.1, 6, 60.0), 1);
  CHECK_EQ(StaggeredDueCount(60.0, 0.1, 6, 60.033), 1);
  CHECK_EQ(StaggeredDueCount(60.0, 0.1, 6, 60.1), 2);  // exactly on the step (the slack covers the rounding)
  CHECK_EQ(StaggeredDueCount(60.0, 0.1, 6, 60.199), 2);
  CHECK_EQ(StaggeredDueCount(60.0, 0.1, 6, 60.5), 6);
  CHECK_EQ(StaggeredDueCount(60.0, 0.1, 6, 75.0), 6);  // never more than the wave
  // a 30 Hz tick: 0.1 s is three ticks, so the six spawn on six different ticks
  int last = 0, spawnTicks = 0;
  for (int tick = 0; tick < 30; ++tick) {
    const int n = StaggeredDueCount(90.0, 0.1, 6, 90.0 + tick / 30.0);
    CHECK(n - last <= 1);
    if (n > last) ++spawnTicks;
    last = n;
  }
  CHECK_EQ(spawnTicks, 6);
  // interval 0: D1c-live's whole wave at once; nothing in an empty wave
  CHECK_EQ(StaggeredDueCount(60.0, 0.0, 7, 60.0), 7);
  CHECK_EQ(StaggeredDueCount(60.0, 0.1, 0, 61.0), 0);
  // a late tick catches up (one slow frame of 0.25 s)
  CHECK_EQ(StaggeredDueCount(120.0, 0.1, 7, 120.25), 3);
  // the user's corrected footage (six minions between 01:00 and 01:03): 0.5 s apart, the last at 62.5 s
  CHECK_EQ(StaggeredDueCount(60.0, 0.5, 6, 60.49), 1);
  CHECK_EQ(StaggeredDueCount(60.0, 0.5, 6, 60.5), 2);
  CHECK_EQ(StaggeredDueCount(60.0, 0.5, 6, 62.49), 5);
  CHECK_EQ(StaggeredDueCount(60.0, 0.5, 6, 62.5), 6);
}

GMS_TEST(FlowGroupShape) {
  // a line of six along +x, 3 units apart, one of them 0.5 off the line
  std::vector<Point> line = {{0, 0, 0}, {3, 0, 0}, {6, 0.5f, 0}, {9, 0, 0}, {12, 0, 0}, {15, 0, 0}};
  GroupShape s = ShapeOf(line);
  CHECK_EQ(s.n, 6);
  CHECK(std::fabs(s.length - 15.f) < 1e-4f);
  CHECK(std::fabs(s.width - 0.5f) < 1e-4f);
  CHECK(std::fabs(s.closest - 3.f) < 1e-4f);
  CHECK(s.meanNearest > 2.99f && s.meanNearest < 3.05f);
  // D1c-live's formation: rows of three, 2.5 apart: a clump 5 x 2.5
  std::vector<Point> clump;
  for (int i = 0; i < 6; ++i) {
    float dx = 0, dy = 0;
    FormationOffset(i, 1.f, 0.f, 2.5f, &dx, &dy);
    clump.push_back({dx, dy, 0});
  }
  GroupShape c = ShapeOf(clump);
  CHECK(c.length < 6.f);
  CHECK(c.width > 2.f);
  CHECK(std::fabs(c.closest - 2.5f) < 1e-4f);
  // fewer than two
  CHECK_EQ(ShapeOf({}).n, 0);
  GroupShape one = ShapeOf({{1, 2, 3}});
  CHECK_EQ(one.n, 1);
  CHECK(one.length == 0.f && one.closest == 0.f);
  // two on one spot (stacked)
  GroupShape two = ShapeOf({{1, 1, 0}, {1, 1, 0}});
  CHECK(two.length == 0.f && two.width == 0.f && two.closest == 0.f);
}

GMS_TEST(FlowComposition) {
  const float minions[4] = {3.f, 3.f, 0.f, 0.f};
  Composition c = CompositionOf(minions);
  CHECK_EQ(c.counts[0], 3);
  CHECK_EQ(c.counts[1], 3);
  CHECK_EQ(c.Total(), 6);
  const float boar[4] = {0.f, 1.f, 1.f, 0.f};
  CHECK_EQ(CompositionOf(boar).Total(), 2);
  const float junk[4] = {-1.f, 1e9f, 0.4f, 2.6f};
  Composition j = CompositionOf(junk);
  CHECK_EQ(j.counts[0], 0);
  CHECK_EQ(j.counts[1], 0);
  CHECK_EQ(j.counts[2], 0);
  CHECK_EQ(j.counts[3], 3);
}

GMS_TEST(FlowBaseStructures) {
  // D5: AddBaseStructuresToAggroList's choice: the other team's living nubs and Guardian
  CHECK_EQ(NubTeam("nub_mortal_v5_A"), 1);
  CHECK_EQ(NubTeam("nub_spirit_v5_E.Noun"), 2);
  CHECK_EQ(NubTeam("Binding_Spirit_Inner"), 0);
  CHECK_EQ(NubTeam("GuardianSpirit_v5"), 0);
  std::vector<BaseStructure> all;
  auto add = [&all](uint32_t id, int team, bool alive) {
    BaseStructure b;
    b.id = id;
    b.team = team;
    b.alive = alive;
    all.push_back(b);
  };
  add(0x11, 1, true);   // a Mortal nub
  add(0x21, 2, true);   // Spirit nub A
  add(0x22, 2, false);  // Spirit nub C, dead (waiting for its resurrect)
  add(0x2F, 2, true);   // the Spirit Guardian
  add(0x1F, 1, true);   // the Mortal Guardian
  add(0x00, 2, true);   // no id
  CHECK(BaseStructuresAgainst(1, all) == (std::vector<uint32_t>{0x21, 0x2F}));
  CHECK(BaseStructuresAgainst(2, all) == (std::vector<uint32_t>{0x11, 0x1F}));
  CHECK(BaseStructuresAgainst(0, all).empty());  // a neutral object (a creep) gets nothing
  CHECK(BaseStructuresAgainst(3, all).empty());
}
