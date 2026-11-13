// RULES4a: a custom game's shop rules on the match host (the match rules design, its section on Shop on Death and item
// pools). Engine-free and header-only, so gms_tests checks them without the game; gms/engine/shoprules.cpp holds
// the per-match state and commands.cpp asks MayTransact before GameSimulator_OnItemTransaction's switch (SehItemSwitch)
// and before an undo (REFUND1).
//
// Two categories of startGame.rules (the server's catalog, server/internal/rules):
//   shop:     "normal" | "onDeath": a player may shop only with the Shop on Death pass, which everyone holds at the
//             match start, a death gives, and which is kept while dead and while in the shop's reach after the respawn;
//             it is gone once the player is alive and out of the shop's reach (REFUND1's per-tick test,
//             LabsPlayer_InProximityToItemShop). Selling follows the same rule as buying (the orchestrator's default).
//   itemPool: "all" | "noLegendary" (Basic and Advanced stat items) | "basics" (the six Basic stat items) |
//             "lightning" (LIGHTNING1: the Time tree's Energy branch by id, kLightningItems). Pools cover stat items
//             only: potions, flasks and battle spells are always allowed. A sale is never refused by a pool.
// Battle spells (Item_IsSpell) are bought anywhere in the game, with a spell point at a level-up, so Shop on Death
// leaves them alone, their sale (the respec) too.
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>

#include "core/rules/items.h"

namespace gms::rules {

enum class ShopAccess : std::uint8_t { kNormal, kOnDeath };
enum class ItemPool : std::uint8_t { kAll, kNoLegendary, kBasics, kLightning };

// LIGHTNING1: the pool "lightning" ("Lightning items only", the user's decision of 2026-11-12): Time, Energy and the
// three Legendary items over Energy (Chaos, Potency, Resonance), the item catalog's Time > Energy branch. Closed
// downwards (Energy's component is Time, the three's is Energy; a skip purchase's undo gives Time back).
inline constexpr std::uint32_t kLightningItems[] = {
    0x7FDF6F5A,  // Basic_Time
    0xF485EF39,  // Advanced_Energy
    0x97AF2200,  // Legendary_Chaos
    0xD62F5190,  // Legendary_Potency
    0xB0FB4E50,  // Legendary_Resonance
};

// The option ids of server/internal/rules (rules.go's catalog). False for an id this host doesn't know.
inline bool ParseShopAccess(const std::string& id, ShopAccess* out) {
  if (id.empty() || id == "normal") {
    *out = ShopAccess::kNormal;
  } else if (id == "onDeath") {
    *out = ShopAccess::kOnDeath;
  } else {
    return false;
  }
  return true;
}

inline bool ParseItemPool(const std::string& id, ItemPool* out) {
  if (id.empty() || id == "all") {
    *out = ItemPool::kAll;
  } else if (id == "noLegendary") {
    *out = ItemPool::kNoLegendary;
  } else if (id == "basics") {
    *out = ItemPool::kBasics;
  } else if (id == "lightning") {
    *out = ItemPool::kLightning;
  } else {
    return false;
  }
  return true;
}

inline const char* Name(ShopAccess s) { return s == ShopAccess::kOnDeath ? "onDeath" : "normal"; }
inline const char* Name(ItemPool p) {
  switch (p) {
    case ItemPool::kNoLegendary:
      return "noLegendary";
    case ItemPool::kBasics:
      return "basics";
    case ItemPool::kLightning:
      return "lightning";
    case ItemPool::kAll:
    default:
      return "all";
  }
}

struct Rules {
  ShopAccess shop = ShopAccess::kNormal;
  ItemPool pool = ItemPool::kAll;
  bool Any() const { return shop != ShopAccess::kNormal || pool != ItemPool::kAll; }
};

// Whether the pool may hold items of this kind: the tier pools' rule. Only the stat items' tiers are pooled; every
// other kind (spells, consumables, the cheat items, an id not in build 854's catalog) passes. A pool by id
// (kLightning) holds items of every stat tier, so it passes here and InPool by id decides. Each pool is closed
// downwards (an allowed item's components are allowed too: a stat item's component is one tier below it), so
// REFUND1's undo, which puts an upgrade's component back, never meets an item the pool bans.
inline bool InPool(ItemPool pool, ItemKind kind) {
  switch (pool) {
    case ItemPool::kNoLegendary:
      return kind != ItemKind::kLegendary;
    case ItemPool::kBasics:
      return kind != ItemKind::kAdvanced && kind != ItemKind::kLegendary;
    case ItemPool::kLightning:
    case ItemPool::kAll:
    default:
      return true;
  }
}

inline bool IsStatKind(ItemKind kind) {
  return kind == ItemKind::kBasic || kind == ItemKind::kAdvanced || kind == ItemKind::kLegendary;
}

// LIGHTNING1: whether the pool lets a player buy this item (the wire's id): the tier rule above, and for a pool by id
// its list. Anything that isn't a stat item of build 854's catalog passes, as with the tier pools.
inline bool InPool(ItemPool pool, std::uint32_t item) {
  const ItemKind kind = KindOf(item);
  if (!InPool(pool, kind)) return false;
  if (pool != ItemPool::kLightning || !IsStatKind(kind)) return true;
  for (std::uint32_t id : kLightningItems)
    if (id == item) return true;
  return false;
}

// The Shop on Death pass of one player. Everyone holds it at the match start; Update is called every tick and before
// each of the player's transactions with the creature's state.
//   dead: the creature's hit points are 0 or less; inReach: LabsPlayer_InProximityToItemShop (the footprint + 40 units
//   from the team's base object).
// A death gives the pass; alive and out of reach takes it. The start's pass is taken only once the player has been seen
// in reach or dead (`armed`), so a creature read before its spawn (or a spawn out of the shop's reach) doesn't lose it.
class ShopPass {
 public:
  // True when the pass changed (for the log).
  bool Update(bool dead, bool inReach) {
    const bool before = pass_;
    if (dead || inReach) armed_ = true;
    if (dead) {
      pass_ = true;
    } else if (!inReach && armed_) {
      pass_ = false;
    }
    return pass_ != before;
  }
  bool Has() const { return pass_; }
  void Reset() { *this = ShopPass(); }

 private:
  bool pass_ = true;
  bool armed_ = false;
};

// What the rules say about a transaction. kShopClosed comes before kItemNotAllowed (when both fail, the player can't
// shop now anyway).
enum class Verdict : std::uint8_t { kAllowed, kShopClosed, kItemNotAllowed };

// The transactions the rules look at (the wire's types: 1 purchase, 2 sell, 4 consume-on-purchase; use and swap are
// not shop actions).
enum class Transaction : std::uint8_t { kPurchase, kSell, kConsume, kOther };

inline Transaction TransactionOf(unsigned wireType) {
  return wireType == 1 ? Transaction::kPurchase
         : wireType == 2 ? Transaction::kSell
         : wireType == 4 ? Transaction::kConsume
                         : Transaction::kOther;
}

// MayBuy (a purchase or a consume-on-purchase) and the sale's rule in one: the rules, whether the player holds the
// Shop on Death pass now, and the item's id on the wire. A bot never comes here (the caller skips bot seats: RULES4b
// teaches the bots; until then the server's checks keep the bots off with these options).
inline Verdict MayTransact(const Rules& rules, Transaction t, bool hasPass, std::uint32_t item) {
  if (t == Transaction::kOther) return Verdict::kAllowed;
  const ItemKind kind = KindOf(item);
  if (kind == ItemKind::kSpell) return Verdict::kAllowed;
  if (rules.shop == ShopAccess::kOnDeath && !hasPass) return Verdict::kShopClosed;
  if (t != Transaction::kSell && !InPool(rules.pool, item)) return Verdict::kItemNotAllowed;
  return Verdict::kAllowed;
}

inline Verdict MayBuy(const Rules& rules, bool hasPass, std::uint32_t item) {
  return MayTransact(rules, Transaction::kPurchase, hasPass, item);
}

// The ClientFeedbackDef that tells the player (RULES0b's defs, in the setup's zz_BotProfiles.snap); nullptr for
// kAllowed. A pool by id names its items (LIGHTNING1: Rules_LightningOnly, "Only Time, Energy, Chaos, Potency,
// Resonance."); the tier pools keep the general line.
inline const char* FeedbackDef(Verdict v, ItemPool pool = ItemPool::kAll) {
  if (v == Verdict::kItemNotAllowed && pool == ItemPool::kLightning) return "Rules_LightningOnly.ClientFeedbackDef";
  return v == Verdict::kShopClosed       ? "Rules_ShopClosed.ClientFeedbackDef"
         : v == Verdict::kItemNotAllowed ? "Rules_ItemNotAllowed.ClientFeedbackDef"
                                         : nullptr;
}

inline const char* Describe(Verdict v) {
  return v == Verdict::kShopClosed       ? "the shop is closed (Shop on Death: no pass)"
         : v == Verdict::kItemNotAllowed ? "the item is not in the match's item pool"
                                         : "allowed";
}

// At most one refusal text per player every kTextPaceMs (the defs' own cooldown is 2000 ms, their line lives 3000 ms):
// a player clicking a refused item again and again gets a line that stays up, not a queue.
constexpr double kTextPaceMs = 2000.0;
constexpr unsigned kMaxPlayers = 16;

class TextPace {
 public:
  bool Due(unsigned index, double nowMs) {
    if (index >= kMaxPlayers) return false;
    if (sent_[index] && nowMs - last_[index] < kTextPaceMs) return false;
    sent_[index] = true;
    last_[index] = nowMs;
    return true;
  }

 private:
  double last_[kMaxPlayers] = {};
  bool sent_[kMaxPlayers] = {};
};

}  // namespace gms::rules
