// gms_tests: DESECBOT-2, a profile's own build with its choices (gms/core/bots/guidebuild.h).
#include <cstdint>
#include <string>
#include <vector>

#include "core/bots/guidebuild.h"
#include "core/protocol/messages.h"
#include "tests/check.h"

using namespace gms::bots;

namespace {

// Build 854's items of the guide's build (the item catalog: id, cost, UpgradesFrom).
constexpr std::uint32_t kLife = 0xB22758B1, kPower = 0x595ED4FA, kResilience = 0x6F470916, kWill = 0xD1CDA3DF,
                        kPotion = 0x11A0F947, kPurity = 0x13587A9C, kDiscipline = 0xA90CD55F, kBalance = 0xD2DDFA15,
                        kMight = 0x584438CE, kForce = 0x9DA67B0A, kFreedom = 0x813F2DE7, kDevotion = 0xD0A7565C,
                        kSubjugation = 0xD74B4399, kOppression = 0xC9E846F6, kDecay = 0xFC34A372, kHarmony = 0xED6E120E,
                        kHostility = 0xDFC050AD, kStability = 0xE7327947;

ItemCatalog GuideCatalog() {
  ItemCatalog c;
  c.Add({kLife, 380, {}, false, 1});
  c.Add({kPower, 380, {}, false, 1});
  c.Add({kResilience, 380, {}, false, 1});
  c.Add({kWill, 340, {}, false, 1});
  c.Add({kPotion, 40, {}, true, 5});
  c.Add({kPurity, 1200, {kLife}, false, 1});
  c.Add({kDiscipline, 1500, {kResilience}, false, 1});
  c.Add({kBalance, 1500, {kWill}, false, 1});
  c.Add({kMight, 900, {kPower}, false, 1});
  c.Add({kForce, 1500, {kPower}, false, 1});
  c.Add({kFreedom, 1200, {kWill}, false, 1});
  c.Add({kDevotion, 2400, {kPurity}, false, 1});
  c.Add({kSubjugation, 3000, {kDiscipline}, false, 1});
  c.Add({kOppression, 2700, {kBalance}, false, 1});
  c.Add({kDecay, 2700, {kMight}, false, 1});
  c.Add({kHarmony, 2700, {kPurity}, false, 1});
  c.Add({kHostility, 3000, {kForce}, false, 1});
  c.Add({kStability, 2400, {kFreedom}, false, 1});
  return c;
}

std::uint32_t IdOf(const std::string& name) { return gms::AssetIdOf(name + ".Item"); }

GuidePlanner DesecratorPlanner(const ItemCatalog* c) {
  const GuideBuildDef* def = GuideBuildFor("PC_Desecrator_Tactician");
  return GuidePlanner(c, GuidePlanner::Resolve(*def, IdOf));
}

// The purchase in the inventory as LabsPlayer_CheckPurchase places it: over its component, else the first free slot.
void Apply(Inventory* inv, const Purchase& p) {
  if (p.replaces) {
    for (std::uint32_t& s : inv->items)
      if (s == p.replaces) {
        s = p.item;
        return;
      }
  }
  for (std::uint32_t& s : inv->items)
    if (!s) {
      s = p.item;
      return;
    }
  inv->items.push_back(p.item);
}

// Buys step after step (gold no object), `ctxAt(step)` giving the context of each purchase; the items bought.
template <typename Ctx>
std::vector<std::uint32_t> BuyAll(const GuidePlanner& g, Inventory* inv, BuildMemory* m, Ctx ctxAt, int limit = 40) {
  std::vector<std::uint32_t> bought;
  for (int i = 0; i < limit; ++i) {
    Purchase p;
    int entry = -1;
    ResolvedBuild plan;
    if (!g.Next(*inv, ctxAt(i), *m, &p, &entry, &plan)) break;
    Apply(inv, p);
    g.Commit(plan, entry, m);
    bought.push_back(p.item);
  }
  return bought;
}

ShopContext Behind(double now = 300) {
  ShopContext c;
  c.now = now;
  c.level = 5;
  c.enemyLevel = 5;
  c.kills = 0;
  c.deaths = 1;
  c.magicAllies = 2;
  return c;
}

}  // namespace

GMS_TEST(GuideBuildTheDesecratorsListResolves) {
  CHECK(GuideBuildFor("PC_Desecrator_Tactician") != nullptr);
  CHECK(GuideBuildFor("PC_Desecrator_Tactician.BotClass") != nullptr);
  CHECK(GuideBuildFor("PC_Desecrator") == nullptr);  // EA's build for the shipped class
  CHECK(GuideBuildFor("PC_Guardian") == nullptr);
  CHECK_EQ(IdOf("Basic_Life"), kLife);  // the asset ids the host looks the names up by
  CHECK_EQ(IdOf("Legendary_Stability"), kStability);
  const GuideBuildDef* def = GuideBuildFor("PC_Desecrator_Tactician");
  const ItemCatalog c = GuideCatalog();
  for (const BuildEntryDef& e : def->entries) {
    CHECK_EQ(e.optionLabels.size(), e.options.size());
    for (const auto& o : e.options)
      for (const char* n : o) CHECK(c.Find(IdOf(n)) != nullptr);  // every name is an item of build 854
  }
  // an unknown name is left out of its option
  GuideBuildDef odd{"PC_X", "test", {{"x", BuildRule::kFixed, {{"Basic_Life", "Basic_Nothing"}}, {"x"}}}};
  const std::vector<BuildEntry> r = GuidePlanner::Resolve(odd, [](const std::string& n) {
    return n == "Basic_Life" ? kLife : 0u;
  });
  CHECK_EQ(r.size(), 1u);
  CHECK(r[0].options[0] == std::vector<std::uint32_t>{kLife});
}

GMS_TEST(GuideBuildIsAhead) {
  ShopContext c = Behind();
  CHECK(!IsAhead(c));
  c.level = 6;  // one level above the enemies' average
  CHECK(IsAhead(c));
  c.level = 5;
  c.enemyLevel = 4.5;  // half a level: not enough
  CHECK(!IsAhead(c));
  c.kills = 2;  // more kills than deaths
  CHECK(IsAhead(c));
  c.kills = 1;
  c.deaths = 1;
  CHECK(!IsAhead(c));
  c.enemyLevel = 0;  // no enemy known: only the kills count
  c.level = 9;
  CHECK(!IsAhead(c));
}

// The default order: Life, Purity over it, Discipline from Resilience, Devotion, Subjugation, then the Decay and
// Oppression chains, Harmony from a new Life (after minute 20), Stability; six legendaries in the six slots.
GMS_TEST(GuideBuildTheOrderBehind) {
  const ItemCatalog c = GuideCatalog();
  const GuidePlanner g = DesecratorPlanner(&c);
  Inventory inv;
  BuildMemory m;
  const std::vector<std::uint32_t> bought = BuyAll(g, &inv, &m, [](int i) { return Behind(i < 12 ? 300 : 1500); });
  const std::vector<std::uint32_t> want = {kLife,  kPurity,  kResilience, kDiscipline, kDevotion, kSubjugation,
                                           kPower, kMight,   kDecay,      kWill,       kBalance,  kOppression,
                                           kLife,  kPurity,  kHarmony,    kWill,       kFreedom,  kStability};
  CHECK(bought == want);
  const std::vector<std::uint32_t> six = {kDevotion, kSubjugation, kDecay, kOppression, kHarmony, kStability};
  CHECK(inv.items == six);
  // the start: Life first, its price 380; the potion is the potion rule's
  Inventory empty;
  Purchase p;
  int entry = -1;
  CHECK(g.Next(empty, Behind(0), BuildMemory{}, &p, &entry) && p.item == kLife && p.price == 380 && entry == 0);
  // Purity over the Life pays 820
  Inventory life;
  life.items = {kLife};
  CHECK(g.Next(life, Behind(), BuildMemory{}, &p, &entry) && p.item == kPurity && p.price == 820 &&
        p.replaces == kLife && entry == 1);
}

// Ahead at the first back: Balance and Might, Oppression over the Balance and Decay over the Might, then the rest.
GMS_TEST(GuideBuildAheadFirstBack) {
  const ItemCatalog c = GuideCatalog();
  const GuidePlanner g = DesecratorPlanner(&c);
  Inventory inv;
  BuildMemory m;
  ShopContext ahead = Behind();
  ahead.kills = 2;
  // ahead for the first purchases of the first back, behind afterwards: the choice made at Will's purchase stays
  const std::vector<std::uint32_t> bought = BuyAll(g, &inv, &m, [&](int i) {
    return i >= 1 && i <= 2 ? ahead : Behind(i < 12 ? 600 : 1500);
  });
  const std::vector<std::uint32_t> want = {kLife,        kWill,       kBalance,     kPower,  kMight,
                                           kOppression,  kDecay,      kPurity,      kDevotion, kResilience,
                                           kDiscipline,  kSubjugation, kLife,       kPurity, kHarmony,
                                           kWill,        kFreedom,    kStability};
  CHECK(bought == want);
  CHECK_EQ(m.chosen.at(1), 1);  // the first back: Balance and Might
  // the choice is made at the first back's first purchase, not before: Life bought while ahead decides nothing
  BuildMemory early;
  Inventory none;
  Purchase p;
  int entry = -1;
  ResolvedBuild plan;
  CHECK(g.Next(none, ahead, early, &p, &entry, &plan) && p.item == kLife);
  g.Commit(plan, entry, &early);
  CHECK(early.chosen.count(1) == 0);
  // a bot that owns Balance (whatever the context) keeps building towards the ahead option's items
  Inventory bal;
  bal.items = {kLife, kBalance, kMight};
  CHECK(g.Next(bal, Behind(), BuildMemory{}, &p, &entry) && p.item == kOppression && entry == 1);
}

// Oppression with 2+ magic-damage allies; Hostility with fewer and a physical Gladiator ally; else Oppression.
GMS_TEST(GuideBuildOppressionOrHostility) {
  const ItemCatalog c = GuideCatalog();
  const GuidePlanner g = DesecratorPlanner(&c);
  Inventory upToDecay;
  upToDecay.items = {kDevotion, kSubjugation, kDecay};
  Purchase p;
  int entry = -1;
  std::string why;
  ShopContext ctx = Behind();
  ctx.magicAllies = 1;
  ctx.physicalGladiatorAlly = true;
  CHECK(g.Next(upToDecay, ctx, BuildMemory{}, &p, &entry) && p.item == kPower && entry == 5);  // Hostility's Force
  CHECK_EQ(g.Choose(5, upToDecay, ctx, BuildMemory{}, &why), 1);
  CHECK(why.find("physical Gladiator") != std::string::npos);
  ctx.magicAllies = 2;  // two magic-damage allies: Oppression even with a physical Gladiator
  CHECK(g.Next(upToDecay, ctx, BuildMemory{}, &p, &entry) && p.item == kWill && entry == 5);
  ctx.magicAllies = 0;
  ctx.physicalGladiatorAlly = false;  // no magic allies, no physical Gladiator: Oppression stays
  CHECK(g.Next(upToDecay, ctx, BuildMemory{}, &p, &entry) && p.item == kWill);
  // chosen at the first purchase and kept: Power bought for Hostility, then two magic allies join
  ctx.magicAllies = 1;
  ctx.physicalGladiatorAlly = true;
  BuildMemory m;
  ResolvedBuild plan;
  Inventory inv = upToDecay;
  CHECK(g.Next(inv, ctx, m, &p, &entry, &plan) && p.item == kPower);
  Apply(&inv, p);
  CHECK(g.Commit(plan, entry, &m));  // a choice of two settled
  ctx.magicAllies = 3;
  CHECK(g.Next(inv, ctx, m, &p, &entry) && p.item == kForce);
  Apply(&inv, p);
  CHECK(g.Next(inv, ctx, m, &p, &entry) && p.item == kHostility && p.price == 1500);
  // an owned Hostility counts as the entry done in any context
  Inventory done = upToDecay;
  done.items.push_back(kHostility);
  ctx.magicAllies = 3;
  CHECK_EQ(g.Choose(5, done, ctx, BuildMemory{}, &why), 1);
  CHECK(g.Next(done, ctx, BuildMemory{}, &p, &entry) && entry == 7);  // Harmony deferred at 300 s: Stability next
}

// Harmony not before minute 20: when its turn comes earlier, Stability first, and it stays so.
GMS_TEST(GuideBuildHarmonyAfterMinute20) {
  const ItemCatalog c = GuideCatalog();
  const GuidePlanner g = DesecratorPlanner(&c);
  Inventory five;
  five.items = {kDevotion, kSubjugation, kDecay, kOppression};
  Purchase p;
  int entry = -1;
  ResolvedBuild plan;
  // at 19 minutes: Stability's chain (Will) first
  CHECK(g.Next(five, Behind(1140), BuildMemory{}, &p, &entry, &plan) && p.item == kWill && entry == 7);
  CHECK(plan.deferredNow == std::vector<int>{6});
  // at 21 minutes: Harmony's (a new Life)
  CHECK(g.Next(five, Behind(1260), BuildMemory{}, &p, &entry) && p.item == kLife && entry == 6);
  // deferred once its turn came: minute 20 passes while Stability is half built, Stability goes on
  BuildMemory m;
  Inventory inv = five;
  CHECK(g.Next(inv, Behind(1140), m, &p, &entry, &plan) && p.item == kWill);
  Apply(&inv, p);
  g.Commit(plan, entry, &m);
  CHECK(m.deferred.count(6) == 1);
  CHECK(g.Next(inv, Behind(1260), m, &p, &entry) && p.item == kFreedom && entry == 7);
  // ... and once Stability is done, Harmony comes (before or after minute 20)
  Inventory six = five;
  six.items.push_back(kStability);
  CHECK(g.Next(six, Behind(1150), m, &p, &entry) && p.item == kLife && entry == 6);
  // a purchase for an entry before Harmony's turn doesn't defer it: Devotion at 10 minutes, Harmony's turn at 21
  BuildMemory early;
  Inventory start;
  start.items = {kPurity, kDiscipline};
  CHECK(g.Next(start, Behind(600), early, &p, &entry, &plan) && p.item == kDevotion);
  g.Commit(plan, entry, &early);
  CHECK(early.deferred.empty());
  CHECK(g.Next(five, Behind(1260), early, &p, &entry) && entry == 6);
  // the build done: nothing more
  Inventory full;
  full.items = {kDevotion, kSubjugation, kDecay, kOppression, kHarmony, kStability};
  CHECK(!g.Next(full, Behind(2000), BuildMemory{}, &p, &entry));
  CHECK_EQ(entry, -1);
}
