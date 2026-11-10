// gms/core/damage/gladiator.h (GLADVIM): the Gladiator's store keeps lane agents out ([host] gladiator_skip_lane_agents).
#include "core/damage/gladiator.h"
#include "tests/check.h"

using namespace gms::damage::gladiator;

namespace {

Store::Death LaneMinion(float vim) {
  Store::Death d;
  d.npcType = 0;
  d.vim = vim;
  return d;
}

Store::Death LaneAgent(float vim, bool spawned = true) {
  Store::Death d;
  d.npcType = kNpcSuperMinion;
  d.spawnedLaneAgent = spawned;
  d.vim = vim;
  return d;
}

}  // namespace

GMS_TEST(GladvimLaneAgentRule) {
  CHECK(IsLaneAgent(2, false));
  CHECK(IsLaneAgent(0, true));  // a spawned lane agent, whatever its type
  CHECK(!IsLaneAgent(0, false));
  CHECK(!IsLaneAgent(1, false));
  CHECK(!IsLaneAgent(3, false));  // a jungle creature
  CHECK(HideStore(true, 2, true));
  CHECK(!HideStore(false, 2, true));  // the old rule, one switch away
  CHECK(!HideStore(true, 0, false));
  CHECK(!HideStore(true, 1, false));
  for (int t : {0, 1, 2, 11, 12}) CHECK(IsMinion(t));
  CHECK(!IsMinion(3));
  CHECK(!IsMinion(-1));
}

// An ally's kill of a lane agent beside a melee Gladiator stores nothing and takes no stack.
GMS_TEST(GladvimAllyKillsLaneAgentStoresNothing) {
  Store s;
  s.OnNearbyDeath(LaneAgent(500.f));
  CHECK_EQ(s.stacks, 0);
  CHECK(s.total == 0.f);
  s.OnNearbyDeath(LaneAgent(100.f, false));  // a super minion the director didn't spawn
  CHECK_EQ(s.stacks, 0);
  // his next own last hit pays only its own amount
  CHECK(s.OnOwnLastHit(24.6f) == 24.6f);
  CHECK_EQ(s.creepScore, 0);
}

// A lane minion's death still stores vim x 3, up to three, and the store pays on his last hit (match 232's 18 -> 51).
GMS_TEST(GladvimLaneMinionStillStores) {
  Store s;
  s.OnNearbyDeath(LaneMinion(3.f));  // a ranged minion: 9
  s.OnNearbyDeath(LaneAgent(500.f));  // hidden: no stack
  s.OnNearbyDeath(LaneMinion(4.f));  // 12
  s.OnNearbyDeath(LaneMinion(4.f));  // 12
  s.OnNearbyDeath(LaneMinion(4.f));  // over the cap
  CHECK_EQ(s.stacks, 3);
  CHECK(s.total == 33.f);
  CHECK(s.OnOwnLastHit(18.f) == 51.f);
  CHECK_EQ(s.creepScore, 1);
  CHECK_EQ(s.unusedStacks, 1);
  CHECK_EQ(s.stacks, 0);
  // his own kill stores nothing; an ally's lane minion is not the store's when he is ranged
  Store::Death mine = LaneMinion(4.f);
  mine.killedByMe = true;
  s.OnNearbyDeath(mine);
  CHECK_EQ(s.stacks, 0);
  Store r;
  r.melee = false;
  r.OnNearbyDeath(LaneMinion(4.f));
  CHECK_EQ(r.stacks, 0);
}

// His own last hit on a lane agent pays the store as before (the grant's amount is the lane agent's own vim).
GMS_TEST(GladvimOwnLastHitOnLaneAgentPays) {
  Store s;
  s.OnNearbyDeath(LaneMinion(4.1f));
  const float paid = s.OnOwnLastHit(500.f);
  CHECK(paid > 512.29f && paid < 512.31f);
  CHECK_EQ(s.stacks, 0);
  // and with nothing stored the lane agent pays its own vim alone
  CHECK(s.OnOwnLastHit(500.f) == 500.f);
}

// [host] gladiator_skip_lane_agents=0: the script's rule as build 854 has it (match 232: a Deer beside him 1,500).
GMS_TEST(GladvimOldRuleStoresLaneAgents) {
  Store s;
  s.skipLaneAgents = false;
  s.OnNearbyDeath(LaneAgent(500.f));
  CHECK_EQ(s.stacks, 1);
  CHECK(s.total == 1500.f);
  const float paid = s.OnOwnLastHit(24.6f);
  CHECK(paid > 1524.59f && paid < 1524.61f);
  // a death ends the store
  s.OnNearbyDeath(LaneAgent(100.f));
  s.OnDeath();
  CHECK_EQ(s.stacks, 0);
  CHECK(s.total == 0.f);
  CHECK(!s.skipLaneAgents);
}
