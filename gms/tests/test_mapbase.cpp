// MAPBASE: the structure rules on MM_Map_v3 (two Binding tiers a team, three Cores a team, the Locus of NPC type
// Tower) against SM_Map_v5's, engine-free (gms/core/matchflow/mapbase.h, guardcore.h).
#include <string>
#include <vector>

#include "core/matchflow/flow.h"
#include "core/matchflow/guardcore.h"
#include "core/matchflow/mapbase.h"
#include "tests/check.h"

using namespace gms::flow;

// The tiers from the noun names: MM_Map_v3's towers markerset has Binding_<Team>_Middle and _Inner only.
GMS_TEST(MapBaseTiers) {
  const std::vector<int> mm = {BindingTier("Binding_Mortal_Middle.Noun"), BindingTier("Binding_Mortal_Inner.Noun")};
  CHECK_EQ(TierMask(mm), 0x6u);
  CHECK(TierNames(TierMask(mm)) == "Middle, Inner");
  const std::vector<int> sm = {0, 1, 2, 0, 1, 2};  // two lanes of three
  CHECK_EQ(TierMask(sm), 0x7u);
  CHECK(TierNames(TierMask(sm)) == "Outer, Middle, Inner");
  CHECK(TierNames(0) == "none");
  CHECK_EQ(TierMask({-1, 3}), 0u);
  // the tuning entry by rank among the tiers present [assumed]: MM's Middle 0 (900 s), Inner 1 (600 s)
  CHECK_EQ(TowerTuningIndex(0x6u, 1), 0);
  CHECK_EQ(TowerTuningIndex(0x6u, 2), 1);
  CHECK_EQ(TowerTuningIndex(0x6u, 0), -1);
  CHECK_EQ(TowerTuningIndex(0x7u, 2), 2);  // SM_Map_v5: the tier itself
  CHECK_EQ(TowerTuningIndex(0x7u, -1), -1);
}

// towerRespawnEnabled is false on both shipped levels: a destroyed Binding stays down whatever the list says.
GMS_TEST(MapBaseBindingRespawn) {
  std::vector<TowerTuning> mm(2);
  mm[0].respawnTime = 900.f;
  mm[0].laneAgentAdjust = 0;
  mm[1].respawnTime = 600.f;
  mm[1].laneAgentAdjust = -1;
  CHECK_EQ(BindingRespawnSeconds(false, mm, 0), -1.0);  // MM_Map_v3's data
  CHECK_EQ(BindingRespawnSeconds(false, mm, 1), -1.0);
  CHECK_EQ(BindingRespawnSeconds(true, mm, 0), 900.0);  // a level that set it (none does)
  CHECK_EQ(BindingRespawnSeconds(true, mm, 1), 600.0);
  CHECK_EQ(BindingRespawnSeconds(true, mm, 2), -1.0);
  CHECK_EQ(BindingRespawnSeconds(true, mm, -1), -1.0);
  CHECK_EQ(BindingRespawnSeconds(true, {}, 0), -1.0);
}

// The Locus on both levels; nothing else is one (the Bindings, the nubs, the regen zones, the shop).
GMS_TEST(MapBaseLocusNouns) {
  CHECK(IsLocusNoun("DEST_prefab_blue_tower.Noun"));
  CHECK(IsLocusNoun("DEST_prefab_red_tower"));
  CHECK(IsLocusNoun("DEST_prefab_blue_tower_v5.Noun"));
  CHECK(IsLocusNoun("DEST_prefab_red_tower_v5"));
  CHECK(!IsLocusNoun("DEST_prefab_green_tower"));
  CHECK(!IsLocusNoun("DEST_prefab_red_tower_v6"));
  CHECK(!IsLocusNoun("Binding_Spirit_Inner.Noun"));
  CHECK(!IsLocusNoun("nub_spirit_v5_A.Noun"));
  CHECK(!IsLocusNoun("Base_Health_Regen_Attackers.Noun"));
  CHECK(!IsLocusNoun(".Noun"));
  CHECK(!IsLocusNoun(""));
}

// Three Cores a team (MM_Map_v3: A, B, C): D's and E's specials stay allowed, the cooldown counts only the Cores
// that exist and died; with all five present the one-argument rules are unchanged (SM_Map_v5).
GMS_TEST(MapBaseThreeCores) {
  const unsigned abc = 0x7u;
  CHECK(CoreMaskString(abc, abc) == "ABC..");
  CHECK(CoreMaskString(0x5u, abc) == "A-C..");
  CHECK(CoreMaskString(0, abc) == "---..");
  CHECK(SpecialsOf(abc, abc) == "HealthPacks OrbLauncher FireWall KnockUpOrbs LaserBlast");
  CHECK(SpecialsOf(0, abc) == "KnockUpOrbs LaserBlast");  // every Core dead: D and E still fire
  CHECK(SpecialsOf(0x2u, abc) == "OrbLauncher KnockUpOrbs LaserBlast");
  CHECK_EQ(SpecialCooldownSeconds(abc, abc), 6.0);
  CHECK_EQ(SpecialCooldownSeconds(0x3u, abc), 8.0);
  CHECK_EQ(SpecialCooldownSeconds(0, abc), 12.0);
  CHECK_EQ(DestroyedCoreCount(0, abc), 3);
  CHECK_EQ(DestroyedCoreCount(abc, abc), 0);
  CHECK_EQ(AllowedSpecialsMask(0, abc), 0x18u);
  // a bit outside the five is ignored
  CHECK_EQ(AllowedSpecialsMask(0x20u, abc), 0x18u);
  // SM_Map_v5: all five present, as before MAPBASE
  for (unsigned m = 0; m <= kAllCores; ++m) {
    CHECK(CoreMaskString(m, kAllCores) == CoreMaskString(m));
    CHECK(SpecialsOf(m, kAllCores) == SpecialsOf(m));
    CHECK_EQ(SpecialCooldownSeconds(m, kAllCores), SpecialCooldownSeconds(m));
  }
  // the shield step (D14) counts living Cores, so three a team change nothing there
  CHECK_EQ(GuardianShieldStep(true, LivingCoreCount(0x4u), true), 0);
  CHECK_EQ(GuardianShieldStep(true, LivingCoreCount(0), true), -1);
  CHECK_EQ(GuardianShieldStep(false, LivingCoreCount(0x1u), true), 1);
}
