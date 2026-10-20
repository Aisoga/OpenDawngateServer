// gms_tests: MIKELLABOT, a Gladiator Mikella on the High Huntress's AI: the profile, the Gladiator pin
// ([host] bot_gladiators), her kit layer (core/agents/mikella.h) and her guide build (core/bots/guidebuild.h).
#include <algorithm>
#include <cmath>
#include <cstdint>
#include <cstring>
#include <set>
#include <string>
#include <vector>

#include "core/agents/mikella.h"
#include "core/bots/bots.h"
#include "core/bots/guidebuild.h"
#include "core/bots/kits.h"
#include "core/protocol/messages.h"
#include "tests/check.h"

using namespace gms::bots;
using gms::agents::DecideMikella;
using gms::agents::MikAction;
using gms::agents::MikEnemy;
using gms::agents::MikMemory;
using gms::agents::MikSelf;

namespace {

std::set<std::string> Shipped() {
  return {"PC_HighHuntress", "PC_Creature", "PC_Guardian", "PC_Artist", "PC_TidalQueen", "PC_Sculptor", "PC_Warden",
          "PC_Artificer"};
}
std::vector<std::string> AnyClass() { return {"PC_HighHuntress", "PC_TidalQueen", "PC_Guardian", "PC_Artist", "PC_Warden"}; }

// Her at (100, 50); the enemies given relative to her.
MikSelf Her(int phaseClass, int target = -1) {
  MikSelf s;
  s.pos[0] = 100.f;
  s.pos[1] = 50.f;
  s.phaseClass = phaseClass;
  s.nowMs = 10000;
  s.rankW = s.rankE = s.rankR = 1;
  s.target = target;
  return s;
}
MikEnemy At(std::uint32_t id, float dx, float dy, float hp = 1.f, bool visible = true) {
  MikEnemy e;
  e.id = id;
  e.pos[0] = 100.f + dx;
  e.pos[1] = 50.f + dy;
  e.dist = std::sqrt(dx * dx + dy * dy);
  e.hpFrac = hp;
  e.visible = visible;
  return e;
}

std::uint32_t IdOf(const std::string& name) { return gms::AssetIdOf(name + ".Item"); }

}  // namespace

GMS_TEST(MikellabotProfileAndTheGladiatorPin) {
  CHECK(BaseClass("PC_MerchantPrincess_Gladiator") == "PC_MerchantPrincess");
  CHECK(AiClass("PC_MerchantPrincess_Gladiator") == "PC_HighHuntress");
  CHECK(ProfileFor("PC_MerchantPrincess", kRoleGladiator) == "PC_MerchantPrincess_Gladiator");
  CHECK(IsProfile("PC_MerchantPrincess_Gladiator") && AltRoles("PC_MerchantPrincess_Gladiator") == 0u);
  // [host] bot_gladiators
  CHECK(ParseGladiatorList("").empty() && ParseGladiatorList("default").empty() && ParseGladiatorList("nobody").empty());
  CHECK(ParseGladiatorList("mikella") == std::vector<std::string>{"PC_MerchantPrincess_Gladiator"});
  CHECK(ParseGladiatorList("Merchant_Princess, PC_MerchantPrincess_Gladiator,merchantprincess") ==
        std::vector<std::string>{"PC_MerchantPrincess_Gladiator"});
  CHECK(ParseGladiatorList("mikella,Nissa,vex,creature") ==
        (std::vector<std::string>{"PC_MerchantPrincess_Gladiator", "PC_HighHuntress", "PC_Creature"}));
  std::set<std::string> avail = Shipped();
  // a copy without the profile: no pin, the teams as before
  CHECK(DrawPerTeam(ParseGladiatorList("mikella"), avail, 0, 2, 1u) == (std::vector<std::string>{"", ""}));
  const std::vector<int> drop = ParseDropOrder("");
  const std::vector<BotPlayer> plain = PlanBotPlayersComposed({}, 5, {1, 2}, avail, AnyClass(), drop, 0, 0,
                                                              {"PC_Artificer"}, {}, ParseGladiatorList("mikella"));
  for (const BotPlayer& b : plain) CHECK(b.botClass != "PC_MerchantPrincess_Gladiator");
  avail.insert("PC_MerchantPrincess_Gladiator");
  // unpinned: never drawn (the fixed setup and the free draw keep the two shipped Gladiators)
  for (unsigned seed : {0u, 3u, 9u, 21u})
    for (const BotPlayer& b : PlanBotPlayersComposed({}, 5, {1, 2}, avail, AnyClass(), drop, 0, seed))
      CHECK(b.botClass != "PC_MerchantPrincess_Gladiator");
  // pinned on both teams: the first Gladiator seat (top lane), once per team, the other Gladiator a shipped one
  for (unsigned seed : {0u, 5u, 11u, 77u}) {
    const std::vector<BotPlayer> bots = PlanBotPlayersComposed({}, 5, {1, 2}, avail, AnyClass(), drop, 0, seed,
                                                               {"PC_Artificer"}, {}, ParseGladiatorList("mikella"));
    int mik = 0, glad = 0;
    for (const BotPlayer& b : bots) {
      if (b.role == kRoleGladiator) ++glad;
      if (b.botClass == "PC_MerchantPrincess_Gladiator") {
        CHECK(b.role == kRoleGladiator && b.lane == kLaneTop);
        ++mik;
      }
    }
    CHECK_EQ(mik, 2);
    CHECK_EQ(glad, 4);
  }
  // mikella,highhuntress with bot_shuffle 0: team 1 Mikella top (the Creature bottom), team 2 as before
  const std::vector<BotPlayer> ab = PlanBotPlayersComposed({}, 5, {1, 2}, avail, AnyClass(), drop, 0, 0, {"PC_Artificer"},
                                                           {}, ParseGladiatorList("mikella,highhuntress"));
  std::vector<std::string> t1, t2;
  for (const BotPlayer& b : ab)
    if (b.role == kRoleGladiator) (b.team == 1 ? t1 : t2).push_back(b.botClass);
  CHECK(t1 == (std::vector<std::string>{"PC_MerchantPrincess_Gladiator", "PC_Creature"}));
  CHECK(t2 == (std::vector<std::string>{"PC_HighHuntress", "PC_Creature"}));
  // a pin whose shaper holds the other seat: the two swap (one shaper per team)
  std::vector<ComposedSeat> seats = ComposeTeam(WantedSeats(5, {}, drop), avail, AnyClass());
  CHECK(seats[0].botClass == "PC_HighHuntress" && seats[2].botClass == "PC_Creature");
  CHECK(PinGladiator(seats, "PC_Creature", avail));
  CHECK(seats[0].botClass == "PC_Creature" && seats[2].botClass == "PC_HighHuntress");
  CHECK(!PinGladiator(seats, "PC_Creature", avail));  // already there
  CHECK(!PinGladiator(seats, "", avail) && !PinGladiator(seats, "PC_Nobody", avail));
  // a human Gladiator: the remaining (bottom) Gladiator seat is the first and takes the pin
  seats = ComposeTeam(WantedSeats(4, {kRoleGladiator}, drop), avail, AnyClass());
  CHECK(PinGladiator(seats, "PC_MerchantPrincess_Gladiator", avail));
  int n = 0;
  for (const ComposedSeat& s : seats)
    if (s.role == kRoleGladiator) {
      ++n;
      CHECK(s.botClass == "PC_MerchantPrincess_Gladiator" && s.lane == kLaneBottom);
    }
  CHECK_EQ(n, 1);
}

// The host looks up every class it may seat (bots.cpp): each profile, each pin's and draw's class. MIKELLABOT's first
// live run lacked the Gladiator profile here, so its pin said "(missing)" in a copy that had it.
GMS_TEST(MikellabotTheHostLooksUpEveryProfile) {
  const std::vector<std::string> names = CatalogCandidates();
  auto has = [&names](const std::string& n) { return std::find(names.begin(), names.end(), n) != names.end(); };
  for (const Profile& p : Profiles()) CHECK(has(p.name));
  for (const std::string& n : GladiatorClasses()) CHECK(has(n));
  for (const std::string& n : HunterClasses()) CHECK(has(n));
  for (const std::string& n : TacticianClasses()) CHECK(has(n));
  CHECK(has("PC_MerchantPrincess_Gladiator") && has("PC_Warden_Predator"));
  for (size_t i = 0; i < names.size(); ++i)
    for (size_t j = i + 1; j < names.size(); ++j) CHECK(names[i] != names[j]);  // each once
}

GMS_TEST(MikellabotKitTheSlots) {
  const ShaperKit* mik = KitForNoun("merchant_princess.Noun");
  CHECK(mik != nullptr && mik->mikLayer && !mik->desecLayer && mik->castsQ && mik->wNear == 0.f);
  CHECK(KitForNoun("highhuntress.Noun") == nullptr);  // Nissa keeps her AI as it is
  using gms::agents::MikellaAllowsSlot;
  const int kQ = gms::agents::kSlotQ, kW = kQ + 1, kE = kQ + 2, kR = gms::agents::kSlotR;
  CHECK(MikellaAllowsSlot(kQ, -1.f) && MikellaAllowsSlot(gms::agents::kSlotBasic, -1.f));
  CHECK(MikellaAllowsSlot(gms::agents::kSlotSpell1, -1.f) && MikellaAllowsSlot(gms::agents::kSlotSpell3, -1.f));
  CHECK(!MikellaAllowsSlot(kE, 5.f) && !MikellaAllowsSlot(kR, 5.f));  // the layer's
  CHECK(!MikellaAllowsSlot(kW, -1.f) && !MikellaAllowsSlot(kW, 17.5f));
  CHECK(MikellaAllowsSlot(kW, 17.f) && MikellaAllowsSlot(kW, 4.f));
  // the phases by the High Huntress AI's names
  using gms::agents::MikellaPhaseClassOf;
  CHECK_EQ(MikellaPhaseClassOf("BOT_HighHuntress_Flee"), gms::agents::kMikPhaseFlee);
  CHECK_EQ(MikellaPhaseClassOf("BOT_HighHuntress_FleeTowerTargeting"), gms::agents::kMikPhaseFlee);
  CHECK_EQ(MikellaPhaseClassOf("BOT_HighHuntress_EngageForKill"), gms::agents::kMikPhaseFight);
  CHECK_EQ(MikellaPhaseClassOf("HighHuntress_Gladiator_SelfDefense"), gms::agents::kMikPhaseFight);
  CHECK_EQ(MikellaPhaseClassOf("HighHuntress_Gladiator_GatherForContest"), gms::agents::kMikPhaseFight);
  CHECK_EQ(MikellaPhaseClassOf("BOT_HighHuntress_Harass"), gms::agents::kMikPhaseLane);
  CHECK_EQ(MikellaPhaseClassOf("BOT_HighHuntress_TowerPush"), gms::agents::kMikPhaseLane);
  CHECK_EQ(MikellaPhaseClassOf("HighHuntress_Gladiator_Ward"), gms::agents::kMikPhaseOther);
  CHECK_EQ(MikellaPhaseClassOf(nullptr), gms::agents::kMikPhaseOther);
}

GMS_TEST(MikellabotLayerDecides) {
  using namespace gms::agents;
  const MikellaConfig c;
  MikMemory m;
  // nothing near: nothing
  CHECK_EQ(DecideMikella(Her(kMikPhaseLane), {}, m).kind, kMikNone);
  // the escape E: away from the enemy, 11.77 long
  std::vector<MikEnemy> e = {At(1, 8.f, 0.f)};
  MikAction a = DecideMikella(Her(kMikPhaseFlee), e, m);
  CHECK(a.kind == kMikEscapeE && a.enemy == 0);
  CHECK(std::fabs(a.point[0] - (100.f - c.eLength)) < 0.01f && std::fabs(a.point[1] - 50.f) < 0.01f);
  // ... not beyond fleeNear, not unranked, not an unseen enemy; in Flee nothing else (no R, no W)
  CHECK_EQ(DecideMikella(Her(kMikPhaseFlee), {At(1, 25.f, 0.f, 0.1f)}, m).kind, kMikNone);
  MikSelf noE = Her(kMikPhaseFlee);
  noE.rankE = 0;
  CHECK_EQ(DecideMikella(noE, e, m).kind, kMikNone);
  CHECK_EQ(DecideMikella(Her(kMikPhaseFlee), {At(1, 8.f, 0.f, 1.f, false)}, m).kind, kMikNone);
  // two enemies: away from their centre
  a = DecideMikella(Her(kMikPhaseFlee), {At(1, 6.f, 6.f), At(2, 6.f, -6.f)}, m);
  CHECK(a.kind == kMikEscapeE && a.point[0] < 100.f && std::fabs(a.point[1] - 50.f) < 0.01f);
  // R: a low one within 50 (assassinate), even in the middle of a fight
  a = DecideMikella(Her(kMikPhaseFight), {At(1, 5.f, 0.f, 0.9f), At(2, 40.f, 0.f, 0.3f)}, m);
  CHECK(a.kind == kMikR && a.enemy == 1 && std::strcmp(a.why, "assassinate") == 0);
  // ... a half-hurt one only with nobody within 20 of her
  CHECK_EQ(DecideMikella(Her(kMikPhaseLane), {At(1, 30.f, 0.f, 0.55f)}, m).kind, kMikR);
  CHECK(DecideMikella(Her(kMikPhaseLane), {At(1, 30.f, 0.f, 0.55f), At(2, 15.f, 0.f, 0.9f)}, m).kind != kMikR);
  CHECK(DecideMikella(Her(kMikPhaseLane), {At(1, 30.f, 0.f, 0.65f)}, m).kind != kMikR);  // healthy
  CHECK(DecideMikella(Her(kMikPhaseLane), {At(1, 52.f, 0.f, 0.1f)}, m).kind != kMikR);   // beyond 50
  MikSelf noR = Her(kMikPhaseFight);
  noR.rankR = 0;
  CHECK(DecideMikella(noR, {At(2, 40.f, 0.f, 0.3f)}, m).kind != kMikR);
  // the trade W: her target an enemy shaper within 17
  a = DecideMikella(Her(kMikPhaseLane, 0), {At(1, 15.f, 0.f, 0.9f)}, m);
  CHECK(a.kind == kMikTradeW && a.enemy == 0);
  CHECK(DecideMikella(Her(kMikPhaseLane, 0), {At(1, 18.f, 0.f, 0.9f)}, m).kind != kMikTradeW);
  CHECK(DecideMikella(Her(kMikPhaseLane, -1), {At(1, 15.f, 0.f, 0.9f)}, m).kind != kMikTradeW);  // a minion target
  // the kite E: an enemy shaper within 10 (W unranked here), the charge away from it
  MikSelf noW = Her(kMikPhaseFight, 0);
  noW.rankW = 0;
  a = DecideMikella(noW, {At(1, 0.f, 6.f, 0.9f)}, m);
  CHECK(a.kind == kMikKiteE && std::fabs(a.point[0] - 100.f) < 0.01f &&
        std::fabs(a.point[1] - (50.f - c.eLength)) < 0.01f);
  // the chase E: a low target beyond her reach, within 26, fight phases only
  a = DecideMikella(noW, {At(1, 22.f, 0.f, 0.3f)}, m);  // 0.3: the R would take it first ...
  CHECK_EQ(a.kind, kMikR);
  MikSelf chase = noW;
  chase.rankR = 0;
  a = DecideMikella(chase, {At(1, 22.f, 0.f, 0.3f)}, m);
  CHECK(a.kind == kMikChaseE && std::fabs(a.point[0] - (100.f + c.eLength)) < 0.01f);
  CHECK(DecideMikella(chase, {At(1, 30.f, 0.f, 0.3f)}, m).kind != kMikChaseE);  // too far
  CHECK(DecideMikella(chase, {At(1, 22.f, 0.f, 0.5f)}, m).kind != kMikChaseE);  // not low
  MikSelf laneChase = chase;
  laneChase.phaseClass = kMikPhaseLane;
  CHECK(DecideMikella(laneChase, {At(1, 22.f, 0.f, 0.3f)}, m).kind != kMikChaseE);
  // other phases: nothing
  CHECK_EQ(DecideMikella(Her(kMikPhaseOther, 0), {At(1, 5.f, 0.f, 0.1f)}, m).kind, kMikNone);
  // a refusal waits retryMs; a started R holds the think for the channel
  MikMemory r;
  a = DecideMikella(Her(kMikPhaseLane, 0), {At(1, 15.f, 0.f, 0.9f)}, r);
  MikNoteResult(&r, a, false, 10000);
  CHECK(DecideMikella(Her(kMikPhaseLane, 0), {At(1, 15.f, 0.f, 0.9f)}, r).kind != kMikTradeW);
  MikSelf later = Her(kMikPhaseLane, 0);
  later.nowMs = 10000 + c.retryMs;
  CHECK_EQ(DecideMikella(later, {At(1, 15.f, 0.f, 0.9f)}, r).kind, kMikTradeW);
  // ... a refusal on cooldown waits cooldownRetryMs
  MikNoteResult(&r, a, false, later.nowMs, c, true);
  later.nowMs += c.retryMs;
  CHECK(DecideMikella(later, {At(1, 15.f, 0.f, 0.9f)}, r).kind != kMikTradeW);
  later.nowMs += c.cooldownRetryMs - c.retryMs;
  CHECK_EQ(DecideMikella(later, {At(1, 15.f, 0.f, 0.9f)}, r).kind, kMikTradeW);
  MikAction ra;
  ra.kind = kMikR;
  MikNoteResult(&r, ra, true, 20000);
  CHECK(MikHolding(r, 20000) && MikHolding(r, 20000 + c.rHoldMs - 1) && !MikHolding(r, 20000 + c.rHoldMs));
  // NULLPOS: no point at (0, 0), no point without a direction
  float p[2];
  const float from[2] = {5.f, 0.f};
  CHECK(!MikPointToward(from, -1.f, 0.f, 5.f, p));
  CHECK(!MikPointToward(from, 0.f, 0.f, 5.f, p));
  CHECK(MikPointToward(from, 0.f, 1.f, 5.f, p) && p[0] == 5.f && p[1] == 5.f);
}

// Her guide build: every name an item of build 854; the order with the real costs and components; six legendaries.
GMS_TEST(MikellabotGuideBuild) {
  const GuideBuildDef* def = GuideBuildFor("PC_MerchantPrincess_Gladiator");
  CHECK(def != nullptr && GuideBuildFor("PC_MerchantPrincess") == nullptr && GuideBuildFor("PC_HighHuntress") == nullptr);
  const std::uint32_t hunger = IdOf("Basic_Hunger"), power = IdOf("Basic_Power"), time = IdOf("Basic_Time"),
                      potion = IdOf("Consumable_Potion_HealingPotion"), consumption = IdOf("Advanced_Consumption"),
                      energy = IdOf("Advanced_Energy"), force = IdOf("Advanced_Force"),
                      aggression = IdOf("Advanced_Aggression"), command = IdOf("Advanced_Command"),
                      clarity = IdOf("Advanced_Clarity"), dominance = IdOf("Legendary_Dominance"),
                      resonance = IdOf("Legendary_Resonance"), voracity = IdOf("Legendary_Voracity"),
                      rage = IdOf("Legendary_Rage"), justice = IdOf("Legendary_Justice"), fate = IdOf("Legendary_Fate");
  ItemCatalog c;  // build 854's costs and components
  c.Add({hunger, 460, {}, false, 1});
  c.Add({power, 380, {}, false, 1});
  c.Add({time, 420, {}, false, 1});
  c.Add({potion, 40, {}, true, 5});
  c.Add({consumption, 1500, {hunger}, false, 1});
  c.Add({energy, 1500, {time}, false, 1});
  c.Add({force, 1500, {power}, false, 1});
  c.Add({aggression, 1500, {power}, false, 1});
  c.Add({command, 1200, {power}, false, 1});
  c.Add({clarity, 900, {time}, false, 1});
  c.Add({dominance, 3600, {force}, false, 1});
  c.Add({resonance, 3300, {energy}, false, 1});
  c.Add({voracity, 3000, {consumption}, false, 1});
  c.Add({rage, 2400, {aggression}, false, 1});
  c.Add({justice, 3300, {command}, false, 1});
  c.Add({fate, 3000, {clarity}, false, 1});
  for (const BuildEntryDef& e : def->entries) {
    CHECK_EQ(e.optionLabels.size(), e.options.size());
    for (const auto& o : e.options)
      for (const char* n : o) CHECK(c.Find(IdOf(n)) != nullptr);
  }
  const GuidePlanner g(&c, GuidePlanner::Resolve(*def, IdOf));
  Inventory inv;
  BuildMemory m;
  std::vector<std::uint32_t> bought;
  ShopContext ctx;
  ctx.now = 600;
  for (int i = 0; i < 40; ++i) {
    Purchase p;
    int entry = -1;
    ResolvedBuild plan;
    if (!g.Next(inv, ctx, m, &p, &entry, &plan)) break;
    if (p.replaces) {
      for (std::uint32_t& s : inv.items)
        if (s == p.replaces) s = p.item;
    } else {
      bool put = false;
      for (std::uint32_t& s : inv.items)
        if (!s && !put) {
          s = p.item;
          put = true;
        }
      if (!put) inv.items.push_back(p.item);
    }
    g.Commit(plan, entry, &m);
    bought.push_back(p.item);
  }
  const std::vector<std::uint32_t> want = {hunger, consumption, time, power, force, dominance, energy, resonance,
                                           voracity, power, aggression, power, command, rage, justice, time,
                                           clarity, fate};
  CHECK(bought == want);
  const std::vector<std::uint32_t> six = {voracity, resonance, dominance, rage, justice, fate};
  CHECK(inv.items == six);
}
