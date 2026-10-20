// gms_tests: MOYABOT, Moya (PC_Otter_Hunter) on the Hunter AI: the profile, the Hunter list, her kit rules
// (gms/core/bots/kits.h) and her guide build (gms/core/bots/guidebuild.h).
#include <cstdint>
#include <set>
#include <string>
#include <vector>

#include "core/bots/composition.h"
#include "core/bots/guidebuild.h"
#include "core/bots/kits.h"
#include "core/bots/profiles.h"
#include "core/protocol/messages.h"
#include "tests/check.h"

using namespace gms::bots;

namespace {

// Build 854's items of the guide's builds (id, cost, UpgradesFrom).
constexpr std::uint32_t kLife = 0xB22758B1, kPower = 0x595ED4FA, kResilience = 0x6F470916, kWill = 0xD1CDA3DF,
                        kPotion = 0x11A0F947, kMight = 0x584438CE, kFreedom = 0x813F2DE7, kPride = 0xF16A118F,
                        kResolve = 0xBD9441F1, kForm = 0x35EA2247, kDiscipline = 0xA90CD55F, kBalance = 0xD2DDFA15,
                        kStrife = 0xDD1E0AE9, kVengeance = 0x31E5063A, kStability = 0xE7327947, kHope = 0x38234B18,
                        kAdamance = 0xEAB6BFD2, kEquilibrium = 0xB9E0FE48, kBetrayal = 0x63C3F8AA;

ItemCatalog MoyaCatalog() {
  ItemCatalog c;
  c.Add({kLife, 380, {}, false, 1});
  c.Add({kPower, 380, {}, false, 1});
  c.Add({kResilience, 380, {}, false, 1});
  c.Add({kWill, 340, {}, false, 1});
  c.Add({kPotion, 40, {}, true, 5});
  c.Add({kMight, 900, {kPower}, false, 1});
  c.Add({kFreedom, 1200, {kWill}, false, 1});
  c.Add({kPride, 1500, {kLife}, false, 1});
  c.Add({kResolve, 1200, {kResilience, kWill}, false, 1});
  c.Add({kForm, 1500, {kResilience}, false, 1});
  c.Add({kDiscipline, 1500, {kResilience}, false, 1});
  c.Add({kBalance, 1500, {kWill}, false, 1});
  c.Add({kStrife, 2400, {kMight}, false, 1});
  c.Add({kVengeance, 3000, {kPride}, false, 1});
  c.Add({kStability, 2400, {kFreedom}, false, 1});
  c.Add({kHope, 3000, {kResolve}, false, 1});
  c.Add({kAdamance, 3000, {kForm}, false, 1});
  c.Add({kEquilibrium, 2700, {kDiscipline}, false, 1});
  c.Add({kBetrayal, 3300, {kBalance}, false, 1});
  return c;
}

std::uint32_t IdOf(const std::string& name) { return gms::AssetIdOf(name + ".Item"); }

GuidePlanner MoyaPlanner(const ItemCatalog* c) {
  const GuideBuildDef* def = GuideBuildFor("PC_Otter_Hunter");
  return GuidePlanner(c, GuidePlanner::Resolve(*def, IdOf));
}

// The purchase placed as LabsPlayer_CheckPurchase does: over its component, else the first free slot.
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

std::vector<std::uint32_t> BuyAll(const GuidePlanner& g, Inventory* inv, BuildMemory* m, const ShopContext& ctx) {
  std::vector<std::uint32_t> bought;
  for (int i = 0; i < 40; ++i) {
    Purchase p;
    int entry = -1;
    ResolvedBuild plan;
    if (!g.Next(*inv, ctx, *m, &p, &entry, &plan)) break;
    Apply(inv, p);
    g.Commit(plan, entry, m);
    bought.push_back(p.item);
  }
  return bought;
}

ShopContext Even() {
  ShopContext c;
  c.now = 600;
  c.level = 8;
  c.enemyLevel = 8;
  c.kills = 1;
  c.deaths = 1;
  return c;
}

}  // namespace

GMS_TEST(MoyabotProfileAndTheHunterList) {
  CHECK(BaseClass("PC_Otter_Hunter") == "PC_Otter" && AiClass("PC_Otter_Hunter") == "PC_Artificer");
  CHECK(ProfileFor("PC_Otter", kRoleHunter) == "PC_Otter_Hunter" && IsProfile("PC_Otter_Hunter"));
  // the bot_hunters names
  for (const char* n : {"moya", "Otter", "PC_Otter", "pc_otter_hunter", " moya "})
    CHECK(ParseHunterList(n) == std::vector<std::string>{"PC_Otter_Hunter"});
  CHECK(ParseHunterList("moya,petrus") == (std::vector<std::string>{"PC_Otter_Hunter", "PC_Artificer"}));
  CHECK(ParseHunterList("all").back() == "PC_Otter_Hunter");
  // not in the default draw yet
  for (const std::string& h : DefaultHunters()) CHECK(h != "PC_Otter_Hunter");
  // pinned for both teams when the copy has her; an older copy: nothing (the seat takes PC_Artificer)
  std::set<std::string> avail = {"PC_Artificer", "PC_Survivor_Hunter", "PC_Otter_Hunter"};
  CHECK(DrawHunters(ParseHunterList("moya"), avail, 0, 2) ==
        (std::vector<std::string>{"PC_Otter_Hunter", "PC_Otter_Hunter"}));
  CHECK(DrawHunters(ParseHunterList("moya,freia"), avail, 0, 2) ==
        (std::vector<std::string>{"PC_Otter_Hunter", "PC_Survivor_Hunter"}));
  CHECK(DrawHunters(ParseHunterList("moya"), {"PC_Artificer"}, 0, 2) == (std::vector<std::string>{"", ""}));
}

GMS_TEST(MoyabotKitTheWNearAndTheReflexOrder) {
  using namespace gms::agents;
  const ShaperKit* k = KitForNoun("otter.Noun");
  CHECK(k != nullptr);
  CHECK(KitForNoun("OTTER") == k);
  CHECK(k->castsQ && k->eSpin && !k->desecLayer && k->wNear == 0.f);
  // round 2: her E at the camp creature too, in ClearCamp only and between two hits, within 5
  CHECK(k->castsE && k->eCamp && k->eReach == 5.f);
  CHECK(KitEPhaseAllows(k, true, false, true));
  CHECK(!KitEPhaseAllows(k, true, false, false));  // a swing runs: not now
  CHECK(!KitEPhaseAllows(k, false, true, true));   // never in a gank
  CHECK(KitEWanted(k, 1, true, 4.f) && !KitEWanted(k, 1, true, 6.f) && !KitEWanted(k, 0, true, 4.f));
  // Faris and Freia as before: ClearCamp and GankThings, whatever the leaf
  for (const char* n : {"golem", "survivor"}) {
    const ShaperKit* o = KitForNoun(n);
    CHECK(KitEPhaseAllows(o, true, false, false) && KitEPhaseAllows(o, false, true, false));
    CHECK(!KitEPhaseAllows(o, false, false, true));
  }
  CHECK(!KitEPhaseAllows(nullptr, true, false, true));
  // the W only with the target within 10 (her Q dashes 11.85); no target: none
  CHECK(KitAllowsWAt(k, kSlotQ + 1, 4.f));
  CHECK(KitAllowsWAt(k, kSlotQ + 1, 10.f));
  CHECK(!KitAllowsWAt(k, kSlotQ + 1, 10.5f));
  CHECK(!KitAllowsWAt(k, kSlotQ + 1, 38.f));
  CHECK(!KitAllowsWAt(k, kSlotQ + 1, -1.f));
  CHECK(KitAllowsWAt(k, kSlotQ, 38.f) && KitAllowsWAt(k, kSlotQ + 2, 38.f));  // other slots: always
  // the other kits keep their W at any distance
  for (const char* n : {"artificer.noun", "survivor.noun", "golem.noun", "desecrator.noun"})
    CHECK(KitAllowsWAt(KitForNoun(n), kSlotQ + 1, 38.f));
  CHECK(KitAllowsWAt(nullptr, kSlotQ + 1, -1.f));
  // BOTREFLEX's attack: W, Q, basic for her; Q, W, E, basic for the others; the Desecrator's layer E first
  CHECK(ReflexSlots(k, false) == (std::vector<int>{kSlotQ + 1, kSlotQ, kSlotBasic}));
  CHECK(ReflexSlots(nullptr, false) == (std::vector<int>{kSlotQ, kSlotQ + 1, kSlotQ + 2, kSlotBasic}));
  CHECK(ReflexSlots(KitForNoun("survivor"), false) == (std::vector<int>{kSlotQ, kSlotQ + 1, kSlotQ + 2, kSlotBasic}));
  CHECK(ReflexSlots(KitForNoun("desecrator"), true) == (std::vector<int>{kSlotQ + 2, kSlotQ, kSlotQ + 1, kSlotBasic}));
  // the finisher: her R (Izuna's line 19.12) at an enemy shaper at or below 45 % within 17
  std::vector<FinisherCandidate> c = {{16.f, 0.40f, true}, {12.f, 0.60f, true}, {18.f, 0.10f, true}};
  CHECK_EQ(FinisherTarget(k, 1, c), 0);
  CHECK_EQ(FinisherTarget(k, 0, c), -1);
}

GMS_TEST(MoyabotGuideBuild) {
  CHECK(GuideBuildFor("PC_Otter_Hunter") != nullptr);
  CHECK(GuideBuildFor("PC_Otter") == nullptr);  // the shipped class: EA's ItemBuild
  CHECK(GuideBuildFor("PC_Desecrator_Tactician") != nullptr);  // the other profile's build is still there
  const ItemCatalog c = MoyaCatalog();
  const GuideBuildDef* def = GuideBuildFor("PC_Otter_Hunter");
  for (const BuildEntryDef& e : def->entries) {
    CHECK_EQ(e.optionLabels.size(), e.options.size());
    for (const auto& o : e.options)
      for (const char* n : o) CHECK(c.Find(IdOf(n)) != nullptr);  // every name is an item of build 854
  }
  // the guide's order: Resilience; Might, Freedom, Pride; Strife over the Might, Vengeance over the Pride, Stability
  // over the Freedom, Hope over a Resolve over the first Resilience; Adamance and Equilibrium each from a new Resilience
  const GuidePlanner g = MoyaPlanner(&c);
  Inventory inv;
  BuildMemory m;
  const std::vector<std::uint32_t> bought = BuyAll(g, &inv, &m, Even());
  const std::vector<std::uint32_t> want = {kResilience, kPower,   kMight,      kWill,     kFreedom,    kLife,
                                           kPride,      kStrife,  kVengeance,  kStability, kResolve,   kHope,
                                           kResilience, kForm,    kAdamance,   kResilience, kDiscipline, kEquilibrium};
  CHECK(bought == want);
  const std::vector<std::uint32_t> six = {kHope, kStrife, kStability, kVengeance, kAdamance, kEquilibrium};
  CHECK(inv.items == six);
  // ahead at the last item's first purchase: Betrayal (Will, Balance, Betrayal) instead of the Equilibrium
  Inventory five;
  five.items = {kHope, kStrife, kStability, kVengeance, kAdamance};
  ShopContext ahead = Even();
  ahead.kills = 3;
  BuildMemory m2;
  const std::vector<std::uint32_t> last = BuyAll(g, &five, &m2, ahead);
  CHECK(last == (std::vector<std::uint32_t>{kWill, kBalance, kBetrayal}));
  CHECK_EQ(m2.chosen.at(static_cast<int>(def->entries.size()) - 1), 1);
  // not ahead: the Equilibrium
  Inventory five2;
  five2.items = {kHope, kStrife, kStability, kVengeance, kAdamance};
  BuildMemory m3;
  CHECK(BuyAll(g, &five2, &m3, Even()) == (std::vector<std::uint32_t>{kResilience, kDiscipline, kEquilibrium}));
}
