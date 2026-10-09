// D14 (offline): the map rules the host had to add or change (docs/MATCH_FLOW.md *D14*), engine-free.
#include <vector>

#include "core/agents/aggro.h"
#include "core/damage/rules.h"
#include "core/matchflow/flow.h"
#include "tests/check.h"

using namespace gms::damage;

// The jungle majors' buffs (CharacterRewardData.buffs, "last player to hit") and the parasite's team buffs ("team").
GMS_TEST(D14RewardBuffReceivers) {
  const std::vector<int> team = {0, 1, 2, 3, 4};
  // Jungle_Fish_Major killed by player 2 (alive): the Green buff goes to player 2 only
  std::vector<int> r = BuffReceivers(kRewardLastHit, {0, 2}, 2, team, {0, 1, 2, 3, 4});
  CHECK_EQ(r.size(), static_cast<size_t>(1));
  CHECK_EQ(r[0], 2);
  // a major killed by a minion or a Binding (no player hit it lately): nobody
  CHECK(BuffReceivers(kRewardLastHit, {0, 2}, -1, team, {0, 1, 2, 3, 4}).empty());
  // the last hitter died before the major (a DoT's killing tick): nobody [assumed]
  CHECK(BuffReceivers(kRewardLastHit, {0}, 2, team, {0, 1, 3, 4}).empty());
  // "and assist": still the last hitter alone (no assists for NPC kills)
  r = BuffReceivers(kRewardLastHitAndAssist, {0, 1, 2}, 1, team, {0, 1, 2});
  CHECK_EQ(r.size(), static_cast<size_t>(1));
  CHECK_EQ(r[0], 1);
  // the parasite's stage 2/3 (team): every living player of the killing team, wherever they are
  r = BuffReceivers(kRewardTeam, {}, 3, team, {0, 1, 3, 4});
  CHECK_EQ(r.size(), static_cast<size_t>(4));
  CHECK_EQ(r[2], 3);
  // area and last hit: the area plus the last hitter, once each
  r = BuffReceivers(kRewardAreaAndLastHit, {0, 1}, 1, team, {0, 1, 2});
  CHECK_EQ(r.size(), static_cast<size_t>(2));
  // the titan types: nobody
  CHECK(BuffReceivers(kRewardFriendlyTitans, {0}, 0, team, {0}).empty());
  CHECK(BuffReceivers(kRewardEnemyTitans, {0}, 0, team, {0}).empty());
}

// The parasite (aggro type objective) fights only those who hit it: no on-sight adds; everyone else as D1b-live.
GMS_TEST(D14AggroAddsOnSight) {
  using namespace gms::agents;
  CHECK(!AddsOnSight(kAggroObjective));
  CHECK(!AddsOnSight(kAggroIgnore));
  CHECK(!AddsOnSight(kAggroBot));
  CHECK(AddsOnSight(kAggroNormal));
  CHECK(AddsOnSight(kAggroMinion));
  CHECK(AddsOnSight(kAggroTower));
  CHECK(AddsOnSight(kAggroJungleCreature));  // aggro range 0 anyway
}

// The buff camps' majors stand on their camp's scale-2.0 marker (the tutorial's buff ping); small camps keep the order.
GMS_TEST(D14CampSpotsMajorAtScaledMarker) {
  using gms::flow::AssignCampSpots;
  // Buff_Fish: 2 minor + 1 major, markers (2.0, 1.0, 1.0): the major on marker 0, the minors on 1 and 2
  std::vector<int> s = AssignCampSpots({0, 0, 2}, {2.f, 1.f, 1.f});
  CHECK_EQ(s.size(), static_cast<size_t>(3));
  CHECK_EQ(s[0], 1);
  CHECK_EQ(s[1], 2);
  CHECK_EQ(s[2], 0);
  // Buff_Ugger: 1 minor + 1 major, markers (2.0, 1.0)
  s = AssignCampSpots({0, 2}, {2.f, 1.f});
  CHECK_EQ(s[0], 1);
  CHECK_EQ(s[1], 0);
  // a small Fish camp (2 minor + 1 medium) on three markers of scale 1: creation order, as D1c-live
  s = AssignCampSpots({0, 0, 1}, {1.f, 1.f, 1.f});
  CHECK_EQ(s[0], 0);
  CHECK_EQ(s[1], 1);
  CHECK_EQ(s[2], 2);
  // more creatures than markers (an override asking for more): the rest get -1 (the camp's own position)
  s = AssignCampSpots({0, 0, 0, 1}, {2.f, 1.f});
  CHECK_EQ(s[3], 0);
  CHECK_EQ(s[0], 1);
  CHECK_EQ(s[1], -1);
  CHECK_EQ(s[2], -1);
  // no markers: nobody gets one
  s = AssignCampSpots({0, 2}, {});
  CHECK_EQ(s[1], -1);
}

// The Guardian is invincible while any of its five Cores (nubs) lives (modifier_Tower_Immunity).
GMS_TEST(D14GuardianShield) {
  using gms::flow::GuardianShieldStep;
  CHECK_EQ(GuardianShieldStep(false, 5, true), 1);   // the level load: five cores, apply
  CHECK_EQ(GuardianShieldStep(true, 5, true), 0);
  CHECK_EQ(GuardianShieldStep(true, 1, true), 0);    // four cores down: still invincible
  CHECK_EQ(GuardianShieldStep(true, 0, true), -1);   // the last core falls: remove
  CHECK_EQ(GuardianShieldStep(false, 0, true), 0);
  CHECK_EQ(GuardianShieldStep(false, 1, true), 1);   // a core resurrected after 480 s: back on
  CHECK_EQ(GuardianShieldStep(false, 5, false), 0);  // a dead Guardian: nothing
  CHECK_EQ(GuardianShieldStep(true, 0, false), 0);
}
