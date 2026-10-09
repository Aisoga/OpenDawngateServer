// gms/core/bots (D6b-shims): the bots' shopping plan without the engine (nBot.CanAffordNextItem / PurchaseNextItem /
// CanBuyPotion / PurchasePotions, called by ability_Bot_Buy and condition_Bot_CanBuy in the bots' BackToAction,
// RallyForSiege and Buy phases, behind condition_Bot_CanShop / condition_Bot_AtHomeBase). Header-only.
//
// Facts [code, data]:
//   - the price: Item_GetCost (0x008FCB30) is the item's ItemCost minus the ItemCost of the owned item it upgrades
//     (one item, "cost >= 0" asserted), when the new item is reachable from it through UpgradesInto, directly or
//     further up (0x008FCA90, D15): Basic_Hunger 460 -> Advanced_Desire 1200 (pays 740) -> Legendary_Ambition 2700
//     (pays 1500), and Ambition straight over Hunger pays 2240. LabsPlayer_CheckPurchase (0x00912E30) checks it
//     against the player's available gold (+0xA8) and finds the slot: an upgrade takes the slot of the most valuable
//     owned component (0x00912830, through UpgradesFrom recursively), a new item the first free one;
//   - the slots by item kind (0x009117B0): kind 0 (stat items) 0-5, kind 1 (spells) 6-8, kind 2 (consumables) 9-10;
//     a consumable stacks up to its StackCount (the Vitality Potion, Consumable_Potion_HealingPotion: 40 gold, 5);
//   - the build: RecommendedItemBuild_<Shaper>.ItemBuild (by name; nothing links PlayerClass to it): categories
//     Starting (a Basic item and the potion), Core (3 Legendaries), Offensive (2), Defensive (2); the bots' spells come
//     from BotClass.spellOrder through LabsPlayer_GrantXP, not from here.
// Rules [assumed]: the bot buys its build in order, skipping consumables (the potion rule buys those) and items it
// already has or has upgraded into something it owns; for the first missing build item it buys the first step: the item
// itself as an upgrade when it owns one of its components, else, when the item has components, the first component's
// step (recursively), else the item itself. No step is bought while it needs a free slot and all 6 are full. Potions:
// up to `potionTarget` (2) carried, whenever gold allows.
#pragma once

#include <algorithm>
#include <cstdint>
#include <map>
#include <utility>
#include <vector>

namespace gms::bots {

struct ItemDef {
  std::uint32_t id = 0;                     // the item asset's id (catalogID or asset hash: the host's choice)
  int cost = 0;                             // ItemCost (the total)
  std::vector<std::uint32_t> upgradesFrom;  // UpgradesFrom (components)
  bool consumable = false;                  // Consumable (Category "Consumable", slots 9-10)
  int stack = 1;                            // StackCount
};

class ItemCatalog {
 public:
  void Add(const ItemDef& d) { items_[d.id] = d; }
  const ItemDef* Find(std::uint32_t id) const {
    auto it = items_.find(id);
    return it == items_.end() ? nullptr : &it->second;
  }
  // Item_GetCost (0x008FCB30): the cost minus the cost of `component` when the item is built from it, directly or
  // further down (0x008FCA90 walks the component's UpgradesInto recursively; build 854's UpgradesFrom and UpgradesInto
  // mirror each other, item_catalog.py check). D6b-shims had only the direct component (D15 fixed it; the planner
  // only asks for direct components, so the bots' purchases are the same).
  int PriceWith(std::uint32_t item, std::uint32_t component) const {
    const ItemDef* d = Find(item);
    if (!d) return 0;
    const ItemDef* c = component ? Find(component) : nullptr;
    if (c && component != item && Covers(item, component)) return std::max(0, d->cost - c->cost);
    return d->cost;
  }
  // True when `owned` is `item` or was built (directly or further up) from it.
  bool Covers(std::uint32_t owned, std::uint32_t item, int depth = 0) const {
    if (owned == item) return true;
    const ItemDef* d = Find(owned);
    if (!d || depth > 8) return false;
    for (std::uint32_t c : d->upgradesFrom)
      if (Covers(c, item, depth + 1)) return true;
    return false;
  }

 private:
  std::map<std::uint32_t, ItemDef> items_;
};

// What the bot carries: the stat items in slots 0-5 (0 = empty) and the potions.
struct Inventory {
  std::vector<std::uint32_t> items;  // the item slots' contents, 0 for an empty slot; up to `slots` entries
  int potions = 0;                   // Vitality Potions carried
  int slots = 6;                     // stat-item slots (0-5)
  int Used() const {
    int n = 0;
    for (std::uint32_t i : items) n += i ? 1 : 0;
    return n;
  }
  bool Owns(std::uint32_t item) const { return std::find(items.begin(), items.end(), item) != items.end(); }
};

struct Purchase {
  std::uint32_t item = 0;      // what to buy
  int price = 0;               // ItemData_GetPurchaseCost's answer
  std::uint32_t replaces = 0;  // the owned component it upgrades (0 = a new slot)
};

class ShopPlanner {
 public:
  ShopPlanner(const ItemCatalog* catalog, std::vector<std::uint32_t> build) : catalog_(catalog), build_(std::move(build)) {}

  // The next step of the build (false when the build is done, blocked by full slots, or unknown items).
  bool Next(const Inventory& inv, Purchase* out) const {
    for (std::uint32_t item : build_) {
      const ItemDef* d = catalog_->Find(item);
      if (!d || d->consumable) continue;
      if (Done(inv, item)) continue;
      return Step(inv, item, out, 0);
    }
    return false;
  }
  // nBot.CanAffordNextItem(bot).
  bool CanAffordNext(const Inventory& inv, float gold) const {
    Purchase p;
    return Next(inv, &p) && static_cast<float>(p.price) <= gold;
  }
  // nBot.CanBuyPotion / PurchasePotions: how many potions to buy now (0 = none): up to `target` carried, as many as the
  // gold pays for.
  int PotionsToBuy(const Inventory& inv, float gold, int potionCost, int target = 2) const {
    if (potionCost <= 0 || inv.potions >= target) return 0;
    const int affordable = static_cast<int>(gold / static_cast<float>(potionCost));
    return std::max(0, std::min(target - inv.potions, affordable));
  }
  const std::vector<std::uint32_t>& Build() const { return build_; }

 private:
  bool Done(const Inventory& inv, std::uint32_t item) const {
    for (std::uint32_t owned : inv.items)
      if (owned && catalog_->Covers(owned, item)) return true;
    return false;
  }
  bool Step(const Inventory& inv, std::uint32_t item, Purchase* out, int depth) const {
    const ItemDef* d = catalog_->Find(item);
    if (!d || depth > 8) return false;
    for (std::uint32_t c : d->upgradesFrom) {
      if (inv.Owns(c)) {
        out->item = item;
        out->price = catalog_->PriceWith(item, c);
        out->replaces = c;
        return true;
      }
    }
    if (!d->upgradesFrom.empty()) return Step(inv, d->upgradesFrom.front(), out, depth + 1);  // no component owned yet
    if (inv.Used() >= inv.slots) return false;
    out->item = item;
    out->price = d->cost;
    out->replaces = 0;
    return true;
  }

  const ItemCatalog* catalog_;
  std::vector<std::uint32_t> build_;
};

}  // namespace gms::bots
