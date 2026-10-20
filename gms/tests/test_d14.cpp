// D14 (offline): the map rules the host had to add or change (docs/MATCH_FLOW.md *D14*), engine-free.
#include <string>
#include <vector>

#include "core/agents/aggro.h"
#include "core/damage/rules.h"
#include "core/matchflow/flow.h"
#include "core/matchflow/guardcore.h"
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

// PARAMAP: the camp icon's team bits (8D mCampVisibleToTeam): the parasite's camp while it is up, by default.
GMS_TEST(ParamapCampIconBits) {
  using gms::flow::CampIconBits;
  CHECK_EQ(CampIconBits(1, true, true), 0x07);    // the parasite born and alive: both teams see the icon
  CHECK_EQ(CampIconBits(1, true, false), 0x01);   // unborn, dead or between its death and resurrect: the NPC bit only
  CHECK_EQ(CampIconBits(1, false, true), 0x01);   // a jungle camp: as the director wrote it
  CHECK_EQ(CampIconBits(2, false, true), 0x07);   // mode 2: every camp while up
  CHECK_EQ(CampIconBits(2, false, false), 0x01);
  CHECK_EQ(CampIconBits(0, true, true), 0x01);    // off
  CHECK_EQ(CampIconBits(3, true, true), 0x07);    // CAMPICONS (mode 3): the parasite as in mode 1
  CHECK_EQ(CampIconBits(3, true, false), 0x01);
  CHECK_EQ(CampIconBits(3, false, true), 0x07);   // a jungle camp's truth (the spectators' bits)
}

// GUARDCORE: the Cores' letters from their nouns and the special each one powers (the conditions' modifiers).
GMS_TEST(GuardCoreNounsAndTable) {
  using namespace gms::flow;
  CHECK_EQ(NubCoreIndex("nub_mortal_v5_A"), 0);
  CHECK_EQ(NubCoreIndex("nub_spirit_v5_E.Noun"), 4);
  CHECK_EQ(NubCoreIndex("nub_spirit_v5_C"), 2);
  CHECK_EQ(NubCoreIndex("nub_spirit_v5_F"), -1);
  CHECK_EQ(NubCoreIndex("nub_spirit_v5_AB"), -1);
  CHECK_EQ(NubCoreIndex("nub_mortal_v5_"), -1);
  CHECK_EQ(NubCoreIndex("GuardianSpirit_v5"), -1);
  CHECK_EQ(NubCoreIndex("Binding_Spirit_Inner"), -1);
  // condition_Guardian_Use* <- modifier_Guardian_Nub<X>_Destroyed [script]
  CHECK(std::string(GuardianCoreAt(0).special) == "ability_Guardian_HealthPacks");
  CHECK(std::string(GuardianCoreAt(1).special) == "ability_Guardian_OrbLauncher");
  CHECK(std::string(GuardianCoreAt(2).special) == "ability_Guardian_FireWall");
  CHECK(std::string(GuardianCoreAt(2).condition) == "condition_Guardian_UseFireTrail");
  CHECK(std::string(GuardianCoreAt(3).special) == "ability_Guardian_KnockUpOrbs");
  CHECK(std::string(GuardianCoreAt(4).special) == "ability_Guardian_LaserBlast");
  for (int i = 0; i < kGuardianCores; ++i) CHECK_EQ(GuardianCoreAt(i).letter, static_cast<char>('A' + i));
}

// GUARDCORE: what the host logs per Guardian: the living Cores, the specials left, the special cooldown (6 + 2 s per
// destroyed Core, modifier_Guardian_SpecialAbility_Cooldown.Activate).
GMS_TEST(GuardCoreMaskAndCooldown) {
  using namespace gms::flow;
  CHECK(CoreMaskString(kAllCores) == "ABCDE");
  CHECK(CoreMaskString(kAllCores & ~(1u << 1) & ~(1u << 3)) == "A-C-E");
  CHECK(CoreMaskString(0) == "-----");
  CHECK_EQ(LivingCoreCount(kAllCores), 5);
  CHECK_EQ(LivingCoreCount(1u << 4), 1);
  CHECK_EQ(SpecialCooldownSeconds(kAllCores), 6.0);
  CHECK_EQ(SpecialCooldownSeconds(kAllCores & ~(1u << 4)), 8.0);  // the laser core lost
  CHECK_EQ(SpecialCooldownSeconds(1u), 14.0);                     // only the health core left
  CHECK_EQ(SpecialCooldownSeconds(0), 16.0);
  CHECK(SpecialsOf(kAllCores) == "HealthPacks OrbLauncher FireWall KnockUpOrbs LaserBlast");
  CHECK(SpecialsOf((1u << 0) | (1u << 4)) == "HealthPacks LaserBlast");
  CHECK(SpecialsOf(0) == "none (basic attacks only)");
}
