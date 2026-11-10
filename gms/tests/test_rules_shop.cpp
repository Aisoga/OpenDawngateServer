// gms/core/rules/shop.h (RULES4a): Shop on Death's pass, the item pools, MayTransact, the refusal text's pace; pick.h (RULES5b).
#include <cstdint>
#include <initializer_list>
#include <string>

#include "core/protocol/messages.h"
#include "core/rules/items.h"
#include "core/rules/pick.h"
#include "core/rules/shop.h"
#include "tests/check.h"

using namespace gms::rules;

namespace {

constexpr std::uint32_t kTime = 0x7FDF6F5A;       // Basic_Time
constexpr std::uint32_t kMotion = 0x79E76C0B;     // Advanced_Motion (over Time)
constexpr std::uint32_t kFluidity = 0x05E8C38A;   // Legendary_Fluidity (over Motion)
constexpr std::uint32_t kPotion = 0x11A0F947;     // Consumable_Potion_HealingPotion
constexpr std::uint32_t kFlaskVim = 0xA7C7933C;   // Consumable_Flask_Vim (consume-on-purchase when full)
constexpr std::uint32_t kBlink = 0x77E54E39;      // Spell_Blink
constexpr std::uint32_t kCheatGold = 0xE294A308;  // Consumable_Debug_GrantGold

Rules R(ShopAccess s, ItemPool p) {
  Rules r;
  r.shop = s;
  r.pool = p;
  return r;
}

}  // namespace

GMS_TEST(RulesItemTable) {
  std::size_t n = 0;
  const ItemRow* rows = ItemTable(&n);
  CHECK_EQ(n, static_cast<std::size_t>(106));
  unsigned count[7] = {};
  for (std::size_t i = 0; i < n; ++i) {
    // the wire's id is FNV-1 of the lower-case "<name>.item"
    CHECK_EQ(gms::AssetIdOf(std::string(rows[i].name) + ".item"), rows[i].id);
    // the tier is the name's prefix for every stat item
    const std::string name = rows[i].name;
    const ItemKind k = rows[i].kind;
    if (k == ItemKind::kBasic) CHECK(name.rfind("Basic_", 0) == 0);
    if (k == ItemKind::kAdvanced) CHECK(name.rfind("Advanced_", 0) == 0);
    if (k == ItemKind::kLegendary) CHECK(name.rfind("Legendary_", 0) == 0);
    if (k == ItemKind::kSpell) CHECK(name.rfind("Spell_", 0) == 0);
    ++count[static_cast<int>(k)];
    CHECK(KindOf(rows[i].id) == k);
  }
  // The match items notes' catalog: 6 Basic, 23 Advanced, 59 Legendary, 12 spells, 4 consumables and 2 cheat items
  CHECK_EQ(count[static_cast<int>(ItemKind::kUnknown)], 0u);
  CHECK_EQ(count[static_cast<int>(ItemKind::kBasic)], 6u);
  CHECK_EQ(count[static_cast<int>(ItemKind::kAdvanced)], 23u);
  CHECK_EQ(count[static_cast<int>(ItemKind::kLegendary)], 59u);
  CHECK_EQ(count[static_cast<int>(ItemKind::kSpell)], 12u);
  CHECK_EQ(count[static_cast<int>(ItemKind::kConsumable)], 4u);
  CHECK_EQ(count[static_cast<int>(ItemKind::kCheat)], 2u);
  CHECK(KindOf(0x12345678) == ItemKind::kUnknown);
}

// core/rules/pick.h (RULES5b): All Random and the draft lock at once, the standard pick and anything else don't.
GMS_TEST(RulesPickLocksAtOnce) {
  CHECK(PickLocksAtOnce("random"));
  CHECK(PickLocksAtOnce("draft"));
  CHECK(!PickLocksAtOnce("blind"));
  CHECK(!PickLocksAtOnce(""));
  CHECK(!PickLocksAtOnce("Draft"));
  CHECK(std::string(PickName("draft")) == "Draft");
  CHECK(std::string(PickName("random")) == "All Random");
}

GMS_TEST(RulesParseIds) {
  ShopAccess s = ShopAccess::kOnDeath;
  CHECK(ParseShopAccess("normal", &s) && s == ShopAccess::kNormal);
  CHECK(ParseShopAccess("onDeath", &s) && s == ShopAccess::kOnDeath);
  CHECK(ParseShopAccess("", &s) && s == ShopAccess::kNormal);  // absent = the standard game
  CHECK(!ParseShopAccess("ondeath", &s));                       // ids are exact, as the server's catalog
  ItemPool p = ItemPool::kBasics;
  CHECK(ParseItemPool("all", &p) && p == ItemPool::kAll);
  CHECK(ParseItemPool("noLegendary", &p) && p == ItemPool::kNoLegendary);
  CHECK(ParseItemPool("basics", &p) && p == ItemPool::kBasics);
  CHECK(ParseItemPool("", &p) && p == ItemPool::kAll);
  CHECK(!ParseItemPool("melee", &p));
  CHECK_EQ(std::string(Name(ItemPool::kNoLegendary)), std::string("noLegendary"));
  CHECK_EQ(std::string(Name(ShopAccess::kOnDeath)), std::string("onDeath"));
  CHECK(!Rules().Any());
  CHECK(R(ShopAccess::kNormal, ItemPool::kBasics).Any());
}

GMS_TEST(RulesShopPassLife) {
  ShopPass p;
  CHECK(p.Has());  // everyone may shop at the match start
  // read before the spawn (out of reach, alive): the start's pass stays
  CHECK(!p.Update(false, false));
  CHECK(p.Has());
  // spawned in the base, then the walk out: gone
  CHECK(!p.Update(false, true));
  CHECK(p.Has());
  CHECK(p.Update(false, false));
  CHECK(!p.Has());
  // back in the base alive: still none (walking back doesn't give it)
  CHECK(!p.Update(false, true));
  CHECK(!p.Has());
  // a death gives it, anywhere; kept while dead
  CHECK(p.Update(true, false));
  CHECK(p.Has());
  CHECK(!p.Update(true, false));
  CHECK(p.Has());
  // the respawn in the base: kept while in reach
  CHECK(!p.Update(false, true));
  CHECK(p.Has());
  // the walk out: gone
  CHECK(p.Update(false, false));
  CHECK(!p.Has());
  p.Reset();
  CHECK(p.Has());
}

GMS_TEST(RulesShopPassDeathOutOfReach) {
  // a player who dies before ever being seen in reach still loses the pass after the respawn's walk out
  ShopPass p;
  CHECK(!p.Update(true, false));
  CHECK(p.Has());
  CHECK(!p.Update(false, true));
  CHECK(p.Update(false, false));
  CHECK(!p.Has());
}

GMS_TEST(RulesMayBuyShopOnDeath) {
  const Rules r = R(ShopAccess::kOnDeath, ItemPool::kAll);
  CHECK(MayBuy(r, true, kTime) == Verdict::kAllowed);
  CHECK(MayBuy(r, false, kTime) == Verdict::kShopClosed);
  CHECK(MayBuy(r, false, kFluidity) == Verdict::kShopClosed);
  CHECK(MayBuy(r, false, kPotion) == Verdict::kShopClosed);  // a potion is shopping too
  CHECK(MayTransact(r, Transaction::kConsume, false, kFlaskVim) == Verdict::kShopClosed);
  // selling follows the same rule as buying (REFUND1's undo is a sale: inside the visit the pass is held)
  CHECK(MayTransact(r, Transaction::kSell, false, kTime) == Verdict::kShopClosed);
  CHECK(MayTransact(r, Transaction::kSell, true, kMotion) == Verdict::kAllowed);
  // battle spells are bought anywhere (a spell point), sold as the game sells them
  CHECK(MayBuy(r, false, kBlink) == Verdict::kAllowed);
  CHECK(MayTransact(r, Transaction::kSell, false, kBlink) == Verdict::kAllowed);
  // use and swap aren't shop actions
  CHECK(MayTransact(r, Transaction::kOther, false, kPotion) == Verdict::kAllowed);
  // the standard shop refuses nothing
  CHECK(MayBuy(Rules(), false, kFluidity) == Verdict::kAllowed);
  CHECK(MayTransact(Rules(), Transaction::kSell, false, kFluidity) == Verdict::kAllowed);
}

GMS_TEST(RulesMayBuyItemPools) {
  const Rules noLeg = R(ShopAccess::kNormal, ItemPool::kNoLegendary);
  CHECK(MayBuy(noLeg, false, kTime) == Verdict::kAllowed);
  CHECK(MayBuy(noLeg, false, kMotion) == Verdict::kAllowed);
  CHECK(MayBuy(noLeg, false, kFluidity) == Verdict::kItemNotAllowed);
  const Rules basics = R(ShopAccess::kNormal, ItemPool::kBasics);
  CHECK(MayBuy(basics, false, kTime) == Verdict::kAllowed);
  CHECK(MayBuy(basics, false, kMotion) == Verdict::kItemNotAllowed);
  CHECK(MayBuy(basics, false, kFluidity) == Verdict::kItemNotAllowed);
  // potions, flasks and battle spells are always allowed; the cheat items are CheckPurchase's to refuse
  for (const Rules& r : {noLeg, basics}) {
    CHECK(MayBuy(r, false, kPotion) == Verdict::kAllowed);
    CHECK(MayTransact(r, Transaction::kConsume, false, kFlaskVim) == Verdict::kAllowed);
    CHECK(MayBuy(r, false, kBlink) == Verdict::kAllowed);
    CHECK(MayBuy(r, false, kCheatGold) == Verdict::kAllowed);
    CHECK(MayBuy(r, false, 0x12345678) == Verdict::kAllowed);  // not a stat item of this build
    // a pool never refuses a sale (an item a player holds came in before or is a component)
    CHECK(MayTransact(r, Transaction::kSell, false, kFluidity) == Verdict::kAllowed);
  }
  // both: the closed shop comes first; with the pass the pool decides
  const Rules both = R(ShopAccess::kOnDeath, ItemPool::kNoLegendary);
  CHECK(MayBuy(both, false, kFluidity) == Verdict::kShopClosed);
  CHECK(MayBuy(both, true, kFluidity) == Verdict::kItemNotAllowed);
  CHECK(MayBuy(both, true, kMotion) == Verdict::kAllowed);
}

GMS_TEST(RulesItemPoolsClosedDownwards) {
  // REFUND1's undo gives an upgrade's component back: a component is one tier below, so a pool that allows an item
  // allows its components (the undo also bypasses MayBuy: it calls LabsPlayer_PurchaseItem itself)
  for (ItemPool p : {ItemPool::kAll, ItemPool::kNoLegendary, ItemPool::kBasics}) {
    if (InPool(p, ItemKind::kLegendary)) CHECK(InPool(p, ItemKind::kAdvanced));
    if (InPool(p, ItemKind::kAdvanced)) CHECK(InPool(p, ItemKind::kBasic));
    CHECK(InPool(p, ItemKind::kBasic));
  }
}

GMS_TEST(RulesFeedbackDefsAndPace) {
  CHECK_EQ(std::string(FeedbackDef(Verdict::kShopClosed)), std::string("Rules_ShopClosed.ClientFeedbackDef"));
  CHECK_EQ(std::string(FeedbackDef(Verdict::kItemNotAllowed)), std::string("Rules_ItemNotAllowed.ClientFeedbackDef"));
  CHECK(FeedbackDef(Verdict::kAllowed) == nullptr);
  TextPace pace;
  CHECK(pace.Due(0, 1000));
  CHECK(!pace.Due(0, 2500));  // 1.5 s later: still within the pace
  CHECK(pace.Due(1, 2500));   // another player has its own
  CHECK(pace.Due(0, 3000));   // 2 s later
  CHECK(!pace.Due(0, 4999));
  CHECK(pace.Due(0, 5000));
  CHECK(!pace.Due(kMaxPlayers, 0));
}
