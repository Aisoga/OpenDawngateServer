// gms_tests: MAPLUA, the host's fix-ups of MM_Map_v3's own scripts (core/matchflow/mapfix.h).
#include <cstdint>
#include <string>

#include "core/matchflow/mapfix.h"
#include "tests/check.h"

using namespace gms::mapfix;

GMS_TEST(MapLuaRedirects) {
  // the two Flocklord sub-modifiers, pointed at their SM_Map_v5 siblings
  const auto& r = Redirects();
  CHECK_EQ(r.size(), 2u);
  CHECK(std::string(r[0].holder) == "modifier_MM_BlueBuff");
  CHECK(std::string(r[0].field) == "bonusHasteMod");
  CHECK(std::string(r[0].missing) == "modifier_MM_BlueBuff_BonusHaste");
  CHECK(std::string(r[0].sibling) == "modifier_Jungle_BlueBuff_BonusHaste");
  CHECK(std::string(r[1].holder) == "modifier_MM_OrangeBuff");
  CHECK(std::string(r[1].field) == "damageReductionMod");
  CHECK(std::string(r[1].missing) == "modifier_MM_OrangeBuff_DamageReduction");
  CHECK(std::string(r[1].sibling) == "modifier_Jungle_OrangeBuff_DamageReduction");
}

GMS_TEST(MapLuaGuids) {
  // the scripts' GUIDs as ServerData's index names them (re/out/serverdata_lua/index.csv, the id's low word)
  CHECK_EQ(ModifierGuid("modifier_MM_BlueBuff"), 0x3FFD7829u);
  CHECK_EQ(ModifierGuid("modifier_MM_OrangeBuff"), 0xF04B711Bu);
  CHECK_EQ(ModifierGuid("modifier_Jungle_BlueBuff_BonusHaste"), 0xD291F26Fu);
  CHECK_EQ(ModifierGuid("modifier_Jungle_OrangeBuff_DamageReduction"), 0xE0822DCBu);
  CHECK_EQ(ModifierGuid("modifier_base_regen"), 0xA1B75C98u);
  CHECK_EQ(ModifierGuid("modifier_MM_base_regen"), 0x8D493C3Fu);
  CHECK_EQ(ModifierGuid("modifier_MM_regenSpeed"), 0xE8063436u);
  // the missing names (no script, so the asserts' "Unable to lookup modifier")
  CHECK_EQ(ModifierGuid("modifier_MM_BlueBuff_BonusHaste"), 0x70A688A2u);
  CHECK_EQ(ModifierGuid("modifier_MM_OrangeBuff_DamageReduction"), 0x66025358u);
  CHECK_EQ(ModifierGuid("MODIFIER_mm_bluebuff"), 0x3FFD7829u);  // the case doesn't count
}

GMS_TEST(MapLuaLevelAndRegen) {
  CHECK(IsMmLevel("MM_Map_v3.Level"));
  CHECK(IsMmLevel("mm_map_v3"));
  CHECK(!IsMmLevel("SM_Map_v5.Level"));
  CHECK(!IsMmLevel("MM_Map_v30.Level"));
  CHECK(!IsMmLevel("MM_Map"));
  CHECK(!IsMmLevel(""));
  // [host] mm_base_regen: only 1, only on MM_Map_v3
  CHECK(ChooseBaseRegen(0, true) == BaseRegen::kAsPlaced);
  CHECK(ChooseBaseRegen(1, true) == BaseRegen::kMm);
  CHECK(ChooseBaseRegen(1, false) == BaseRegen::kAsPlaced);
  CHECK(ChooseBaseRegen(-1, true) == BaseRegen::kAsPlaced);
  CHECK(ChooseBaseRegen(2, true) == BaseRegen::kAsPlaced);
  CHECK(PreChunk(true) == "dgsim_mapfix = dgsim_mapfix or {} dgsim_mapfix.wanted = true return 'ok'");
  CHECK(PreChunk(false) == "dgsim_mapfix = dgsim_mapfix or {} dgsim_mapfix.wanted = false return 'ok'");
}
