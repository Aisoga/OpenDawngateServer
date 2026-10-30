// gms/core/commands/refund.h (REFUND1): a sale while still in the base undoes the purchase.
#include <cstdint>
#include <string>

#include "core/commands/refund.h"
#include "tests/check.h"

using namespace gms::refund;

namespace {

// asset ids from AssetData (FNV-1 of the lower-case "<name>.item")
constexpr std::uint32_t kTime = 0x7FDF6F5A;    // Basic_Time 420
constexpr std::uint32_t kMotion = 0x79E76C0B;  // Advanced_Motion 900 (480 over Time)
constexpr std::uint32_t kPower = 0x595ED4FA;   // Basic_Power 380
constexpr std::uint32_t kPotion = 0x11A0F947;  // Consumable_Potion_HealingPotion 40
constexpr std::uint32_t kBlink = 0x77E54E39;   // Spell_Blink

Slot S(std::uint32_t item, int count, std::uint32_t p0 = 0, std::uint32_t p1 = 0) {
  Slot s;
  s.item = item;
  s.count = count;
  s.path[0] = p0;
  s.path[1] = p1;
  return s;
}

// The player's inventory with a purchase applied to one slot.
Inventory With(Inventory inv, unsigned slot, const Slot& s) {
  inv.slot[slot] = s;
  return inv;
}

// What the host does for an undo (gms/engine/refund.cpp): sell one, re-add the component, write the path, set the gold.
float Apply(const Plan& p, Slot* slot, float gold) {
  if (slot->count > 1) {
    --slot->count;
  } else {
    *slot = Slot{};
  }
  if (p.readd) *slot = S(p.readd, 1);
  if (p.writePath) {
    slot->path[0] = p.path[0];
    slot->path[1] = p.path[1];
  }
  return gold + static_cast<float>(p.refund);
}

}  // namespace

GMS_TEST(RefundTimeToMotionUndoneTwice) {
  Ledger l;
  Inventory i0;
  float gold = 1000;
  // Time into slot 0 (420), then Motion upgrades it in place (480; Motion's path holds Time)
  const Inventory i1 = With(i0, 0, S(kTime, 1));
  CHECK(l.OnPurchase(0, 0, i0, i1, 420, {}));
  gold -= 420;
  const Inventory i2 = With(i1, 0, S(kMotion, 1, kTime));
  CHECK(l.OnPurchase(0, 0, i1, i2, 480, {}));
  gold -= 480;
  CHECK_EQ(l.Depth(0, 0), static_cast<std::size_t>(2));

  // the first sale: Time back, +480
  Slot now = i2.slot[0];
  const Entry* e = l.UndoFor(0, 0, kMotion, now);
  CHECK(e != nullptr);
  const Plan p = PlanUndo(*e, now);
  CHECK(p.valid);
  CHECK_EQ(p.readd, kTime);
  CHECK(p.writePath);
  CHECK_EQ(p.refund, 480);
  const float g1 = Apply(p, &now, gold);
  CHECK(UndoTookEffect(*e, now, gold, g1));
  CHECK_EQ(now.item, kTime);
  CHECK_EQ(g1, 580.0f);
  l.Pop(0, 0);
  gold = g1;

  // the second sale: the slot empty, +420, back at the start
  const Entry* e2 = l.UndoFor(0, 0, kTime, now);
  CHECK(e2 != nullptr);
  const Plan p2 = PlanUndo(*e2, now);
  CHECK(p2.valid);
  CHECK_EQ(p2.readd, 0u);
  CHECK(!p2.writePath);
  const float g2 = Apply(p2, &now, gold);
  CHECK(UndoTookEffect(*e2, now, gold, g2));
  CHECK_EQ(now.item, 0u);
  CHECK_EQ(g2, 1000.0f);
  l.Pop(0, 0);
  CHECK_EQ(l.Depth(0, 0), static_cast<std::size_t>(0));
  // a third sale is a plain one
  CHECK(l.UndoFor(0, 0, kTime, now) == nullptr);
}

GMS_TEST(RefundTwoSlotsInOneVisit) {
  Ledger l;
  Inventory i0;
  const Inventory i1 = With(i0, 0, S(kTime, 1));
  const Inventory i2 = With(i1, 1, S(kPower, 1));
  CHECK(l.OnPurchase(3, 0, i0, i1, 420, {}));
  CHECK(l.OnPurchase(3, 1, i1, i2, 380, {}));
  // either can be undone first, each its own price
  const Entry* e1 = l.UndoFor(3, 1, kPower, i2.slot[1]);
  CHECK(e1 != nullptr);
  CHECK_EQ(PlanUndo(*e1, i2.slot[1]).refund, 380);
  const Entry* e0 = l.UndoFor(3, 0, kTime, i2.slot[0]);
  CHECK(e0 != nullptr);
  CHECK_EQ(PlanUndo(*e0, i2.slot[0]).refund, 420);
  // another player's history is separate
  CHECK(l.UndoFor(4, 0, kTime, i2.slot[0]) == nullptr);
  // walking out clears both
  CHECK_EQ(l.OnPosition(3, true, true), 0);
  CHECK_EQ(l.OnPosition(3, true, false), 2);
  CHECK(!l.Any(3));
  CHECK(l.UndoFor(3, 0, kTime, i2.slot[0]) == nullptr);
}

GMS_TEST(RefundPurchaseWhileDead) {
  Ledger l;
  Inventory i0;
  const Inventory i1 = With(i0, 2, S(kPower, 1));
  CHECK(l.OnPurchase(0, 2, i0, i1, 380, {}));
  // dead anywhere: kept; respawned in the base: kept; alive out of reach: cleared
  CHECK_EQ(l.OnPosition(0, false, false), 0);
  CHECK(l.Any(0));
  CHECK_EQ(l.OnPosition(0, true, true), 0);
  CHECK(l.Any(0));
  CHECK_EQ(l.OnPosition(0, true, false), 1);
  CHECK(!l.Any(0));
}

GMS_TEST(RefundStackUsed) {
  Ledger l;
  Inventory i0;
  const Inventory i1 = With(i0, 9, S(kPotion, 1));
  const Inventory i2 = With(i1, 9, S(kPotion, 2));
  CHECK(l.OnPurchase(0, 9, i0, i1, 40, {}));
  CHECK(l.OnPurchase(0, 9, i1, i2, 40, {}));
  // the second potion's undo before any use: one leaves the stack, +40
  const Entry* e = l.UndoFor(0, 9, kPotion, i2.slot[9]);
  CHECK(e != nullptr);
  const Plan p = PlanUndo(*e, i2.slot[9]);
  CHECK(p.valid);
  CHECK_EQ(p.readd, 0u);
  Slot now = i2.slot[9];
  const float g = Apply(p, &now, 100);
  CHECK(UndoTookEffect(*e, now, 100, g));
  CHECK_EQ(now.count, 1);
  CHECK_EQ(g, 140.0f);
  // buy it again, then drink one: the use clears the slot's history, and the sale is a plain one
  const Inventory i3 = With(i0, 9, S(kPotion, 2));
  l.Pop(0, 9);
  CHECK(l.OnPurchase(0, 9, i1, i3, 40, {}));
  const Inventory i4 = With(i0, 9, S(kPotion, 1));
  CHECK_EQ(l.OnOtherChange(0, i3, i4), 2);
  CHECK(l.UndoFor(0, 9, kPotion, i4.slot[9]) == nullptr);
  // a stack smaller than the purchase left it is stale even without the use's notice
  CHECK(l.OnPurchase(0, 9, i4, i3, 40, {}));
  CHECK(l.UndoFor(0, 9, kPotion, i4.slot[9]) == nullptr);
  CHECK_EQ(l.Depth(0, 9), static_cast<std::size_t>(0));
}

GMS_TEST(RefundNotUndoable) {
  Ledger l;
  Inventory i0;
  // a spell: a spell point, bought anywhere, sold for respecSpellCost
  PurchaseFacts spell;
  spell.spell = true;
  const Inventory i1 = With(i0, 6, S(kBlink, 1));
  CHECK(!l.OnPurchase(0, 6, i0, i1, 0, spell));
  CHECK(l.UndoFor(0, 6, kBlink, i1.slot[6]) == nullptr);
  // a cheat item, a consume-on-purchase
  PurchaseFacts cheat;
  cheat.cheat = true;
  CHECK(!l.OnPurchase(0, 0, i0, With(i0, 0, S(kPower, 1)), 380, cheat));
  PurchaseFacts consume;
  consume.consume = true;
  CHECK(!Undoable(consume));
  // a purchase that changed no slot or two slots
  CHECK(!l.OnPurchase(0, 0, i0, i0, 380, {}));
  const Inventory two = With(With(i0, 0, S(kPower, 1)), 1, S(kTime, 1));
  CHECK(!l.OnPurchase(0, 0, i0, two, 380, {}));
  CHECK(!l.Any(0));
}

GMS_TEST(RefundHandBack) {
  constexpr std::uint32_t kMight = 0x584438CE, kDecay = 0xFC34A372, kStrife = 0xDD1E0AE9;
  // the two upgrades that take Might's kill count over
  const HandBack* d = FindHandBack(kDecay, kMight);
  CHECK(d != nullptr);
  CHECK_EQ(std::string(d->sourceStacks),
           std::string("modifier_Item_Legendary_OnLastHit_PermanentPowerHealthCapped_DecayStacks"));
  CHECK_EQ(std::string(d->targetStacks),
           std::string("modifier_Item_Legendary_OnLastHit_PermanentPowerHealthCapped_Stacks"));
  const HandBack* s = FindHandBack(kStrife, kMight);
  CHECK(s != nullptr);
  CHECK_EQ(std::string(s->sourceStacks),
           std::string("modifier_Item_Legendary_OnLastHit_PermanentPowerHealthCapped_StrifeStacks"));
  // none for Might over Power, Decay straight over Power (the skip), Motion over Time
  CHECK(FindHandBack(kMight, kPower) == nullptr);
  CHECK(FindHandBack(kDecay, kPower) == nullptr);
  CHECK(FindHandBack(kMotion, kTime) == nullptr);
  // the count within 0 and Might's 30
  CHECK_EQ(HandBackCount(*d, 6), 6);
  CHECK_EQ(HandBackCount(*d, 0), 0);
  CHECK_EQ(HandBackCount(*d, 31), 30);
  CHECK_EQ(HandBackCount(*d, -1), 0);
  // the undo of Decay over Might carries it; a new slot or Might's own undo doesn't
  Entry e;
  e.before = S(kMight, 1, 0x1000);
  e.after = S(kDecay, 1, 0x1000, 0x2000);
  e.cost = 1800;
  const Plan p = PlanUndo(e, e.after);
  CHECK(p.valid);
  CHECK(p.handBack == d);
  CHECK_EQ(p.readd, kMight);
  Entry m;
  m.after = S(kMight, 1, 0x1000);
  m.cost = 900;
  CHECK(PlanUndo(m, m.after).handBack == nullptr);
}

GMS_TEST(RefundPlanShapes) {
  Entry e;
  e.before = S(kTime, 1);
  e.after = S(kMotion, 1, kTime);
  e.cost = 480;
  // the slot changed since: not valid
  CHECK(!PlanUndo(e, S(kPower, 1)).valid);
  // an upgrade of a stack, a stack that grew by two: not purchases
  Entry st;
  st.before = S(kPotion, 1);
  st.after = S(kPotion, 3);
  CHECK(!PlanUndo(st, st.after).valid);
  Entry up;
  up.before = S(kPotion, 2);
  up.after = S(kMotion, 1);
  CHECK(!PlanUndo(up, up.after).valid);
  // the path is restored with the component
  Entry deep;
  deep.before = S(kMotion, 1, kTime);
  deep.after = S(0x12345678, 1, kTime, kMotion);
  deep.cost = 1200;
  const Plan p = PlanUndo(deep, deep.after);
  CHECK(p.valid);
  CHECK_EQ(p.path[0], kTime);
  CHECK_EQ(p.path[1], 0u);
  // the undo's effect check wants the old slot and the gold exactly
  Slot back = S(kMotion, 1, kTime);
  CHECK(UndoTookEffect(deep, back, 100, 1300));
  CHECK(!UndoTookEffect(deep, back, 100, 700));  // half of a sale
  back.path[0] = 0;
  CHECK(!UndoTookEffect(deep, back, 100, 1300));
}
