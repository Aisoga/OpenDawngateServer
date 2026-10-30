// REFUND1: selling a purchase back while still in the base is an undo. Engine-free and header-only, so gms_tests checks the rule without the game; gms/engine/refund.cpp
// carries it out with the client's own inventory functions.
//
// The rule, per player and inventory slot:
//   - Every purchase that changes exactly one slot pushes an entry {the slot before, the slot after, the gold it took}
//     on that slot's history (LabsPlayer_CheckPurchase's cost: an upgrade's entry holds the price less the component).
//     Time -> Motion in one visit leaves two entries: the first undo brings Time back and 480, the second empties the
//     slot and brings 420.
//   - Not recorded: spells (bought anywhere with a spell point), cheat items, a consume-on-purchase (type 4: no slot
//     changes), a purchase that changed more than one slot or none.
//   - A slot's history holds only while the slot is exactly what its last purchase left (item and count). Anything
//     else that changes the slot clears it: a use (a potion drunk), a normal sale, a swap.
//   - All of a player's history is cleared while the player is alive and out of the shop's reach
//     (LabsPlayer_InProximityToItemShop: the creature's footprint + 40 units from the team's base object). A purchase
//     made while dead stays undoable until the player has respawned and walked out of the base.
//   - A sale of a slot with history is an undo: one of the bought item leaves the slot (its passives end), an upgrade's
//     component comes back with its build path (its passives start again; a kill count the upgrade handed over goes
//     back, HandBack), and the gold becomes the gold before the sale plus the entry's cost. The request is answered success 0, and the LabsPlayerFowUpdate (AA) delta brings the
//     slot and the gold to the clients.
#pragma once

#include <cstddef>
#include <cstdint>
#include <vector>

namespace gms::refund {

constexpr int kSlots = 11;       // cLabsPlayer mItems[11] (kNumInventoryItemSlots)
constexpr int kMaxPlayers = 10;  // gameplay indexes 0-9

// One inventory slot (mItems[i], 0x24 bytes): the item's asset id (asset +0x60), the count (+0x18) and the build path
// (+0x1C/+0x20: up to two components, InventoryItem_Add 0x009139B0 / InventoryItem_Upgrade 0x00914470; the engine keeps
// their asset addresses, which the rule only compares and writes back).
struct Slot {
  std::uint32_t item = 0;
  std::int32_t count = 0;
  std::uint32_t path[2] = {0, 0};
};
inline bool SameContent(const Slot& a, const Slot& b) { return a.item == b.item && a.count == b.count; }
inline bool SameSlot(const Slot& a, const Slot& b) {
  return SameContent(a, b) && a.path[0] == b.path[0] && a.path[1] == b.path[1];
}

struct Inventory {
  Slot slot[kSlots];
};

// One purchase's undo.
struct Entry {
  Slot before;
  Slot after;
  int cost = 0;
};

// What a purchase is, for the decision to record it.
struct PurchaseFacts {
  bool spell = false;    // Item_IsSpell (0x008FCD00): Category 1, bought with a spell point anywhere
  bool cheat = false;    // Item_IsCheatItem (0x008FCCE0): item data +0xB4
  bool consume = false;  // transaction 4: LabsPlayer_ConsumeItem, the item's modifier already runs
};
inline bool Undoable(const PurchaseFacts& f) { return !f.spell && !f.cheat && !f.consume; }

// A kill count an upgrade hands over to the Legendary, which the undo hands back to the component (the script code of
// build 854's ServerData): Might's passive (modifier_Item_Legendary_OnLastHit_PermanentPowerHealthCapped) counts its
// last hits in ..._Stacks (max 30) and adds 4 MaxHealth and 0.5 Power per stack at each kill. Its Deactivate keeps
// the count for 0.5 s; Decay's passive (..._Decay) copies it in its Activate into ..._DecayStacks, Strife's
// (modifier_Item_Legendary_OnLastHit_PermanentPowerHealth) into ..._StrifeStacks. Taking the Legendary out ends those
// (its Deactivate deletes them), and a re-added Might starts at 0. The undo reads the Legendary's count first and gives
// it to the new Might passive through the script's own hand-over calls (RequestModifier + SetStackCount, then the
// bonus as its kill branch adds it). No other item script hands a count over (every SetStackCount in the item
// modifiers: the two above, Might's own Deactivate keeping its count, Recover's marker and the stored-damage counter,
// which stay within one item).
struct HandBack {
  std::uint32_t removed;     // the item the undo takes out
  std::uint32_t restored;    // the component it puts back
  const char* sourceStacks;  // the removed item's modifier that holds the count
  const char* passive;       // the restored item's passive, which gets the count
  const char* targetStacks;  // the passive's own count modifier
  int maxStacks;             // that modifier's maxStackCount
};
constexpr std::uint32_t kItemAdvancedMight = 0x584438CE;
constexpr std::uint32_t kItemLegendaryDecay = 0xFC34A372;
constexpr std::uint32_t kItemLegendaryStrife = 0xDD1E0AE9;
inline const HandBack* FindHandBack(std::uint32_t removed, std::uint32_t restored) {
  static const HandBack kTable[] = {
      {kItemLegendaryDecay, kItemAdvancedMight,
       "modifier_Item_Legendary_OnLastHit_PermanentPowerHealthCapped_DecayStacks",
       "modifier_Item_Legendary_OnLastHit_PermanentPowerHealthCapped",
       "modifier_Item_Legendary_OnLastHit_PermanentPowerHealthCapped_Stacks", 30},
      {kItemLegendaryStrife, kItemAdvancedMight,
       "modifier_Item_Legendary_OnLastHit_PermanentPowerHealthCapped_StrifeStacks",
       "modifier_Item_Legendary_OnLastHit_PermanentPowerHealthCapped",
       "modifier_Item_Legendary_OnLastHit_PermanentPowerHealthCapped_Stacks", 30},
  };
  for (const HandBack& h : kTable)
    if (h.removed == removed && h.restored == restored) return &h;
  return nullptr;
}
// The count the restored passive gets: the source's, within 0 and the target's maximum.
inline int HandBackCount(const HandBack& h, int sourceCount) {
  return sourceCount < 0 ? 0 : sourceCount > h.maxStacks ? h.maxStacks : sourceCount;
}

// How the host undoes an entry, given the slot as it is now.
struct Plan {
  bool valid = false;
  const char* why = "";     // the reason when not valid, or the kind of undo
  std::uint32_t readd = 0;  // an upgrade's component to put back into the emptied slot (0: none)
  bool writePath = false;   // write before.path into the slot after the re-add
  std::uint32_t path[2] = {0, 0};
  int refund = 0;           // the gold the player gets back on top of the gold before the sale
  const HandBack* handBack = nullptr;  // an upgrade whose kill count goes back to the component
};

// The steps: sell one of the bought item (LabsPlayer_SellItem: one leaves the slot, the passives end), then the re-add
// and the path for an upgrade, then the gold. Valid only when the slot is what the purchase left and the entry is one
// of the three shapes a purchase makes: a new slot (before empty, after x1), one more in a stack (same item, count + 1),
// a component upgraded in place (both x1, different items).
inline Plan PlanUndo(const Entry& e, const Slot& now) {
  Plan p;
  p.refund = e.cost;
  if (!SameContent(now, e.after) || e.after.item == 0) {
    p.why = "the slot is no longer what the purchase left";
    return p;
  }
  if (e.cost < 0) {
    p.why = "a negative cost";
    return p;
  }
  if (e.before.item == 0) {
    if (e.after.count != 1) {
      p.why = "a new slot with a count other than 1";
      return p;
    }
    p.valid = true;
    p.why = "new slot";
    return p;
  }
  if (e.before.item == e.after.item) {
    if (e.after.count != e.before.count + 1) {
      p.why = "a stack that didn't grow by one";
      return p;
    }
    p.valid = true;
    p.why = "one more in a stack";
    return p;
  }
  if (e.before.count != 1 || e.after.count != 1) {
    p.why = "an upgrade of a stack";
    return p;
  }
  p.valid = true;
  p.why = "upgrade";
  p.readd = e.before.item;
  p.writePath = true;
  p.path[0] = e.before.path[0];
  p.path[1] = e.before.path[1];
  p.handBack = FindHandBack(e.after.item, e.before.item);
  return p;
}

// Whether a carried-out undo left the slot as it was before the purchase and the gold at `goldBeforeSale` + cost.
inline bool UndoTookEffect(const Entry& e, const Slot& after, float goldBeforeSale, float goldAfter) {
  const float want = goldBeforeSale + static_cast<float>(e.cost);
  const float d = goldAfter - want;
  return SameSlot(after, e.before) && d > -0.01f && d < 0.01f;
}

// The per-player, per-slot histories.
class Ledger {
 public:
  // After a purchase the binary carried out: records it on `slot` when it is undoable and changed exactly that slot;
  // every other changed slot's history is cleared. Returns true if an entry was recorded.
  bool OnPurchase(unsigned player, unsigned slot, const Inventory& before, const Inventory& after, int cost,
                  const PurchaseFacts& facts) {
    if (player >= kMaxPlayers || slot >= kSlots) return false;
    int changed = 0;
    bool slotChanged = false;
    for (unsigned s = 0; s < kSlots; ++s) {
      if (SameSlot(before.slot[s], after.slot[s])) continue;
      ++changed;
      if (s == slot) slotChanged = true;
      else Clear(player, s);
    }
    if (!Undoable(facts) || changed != 1 || !slotChanged || after.slot[slot].item == 0) {
      if (slotChanged) Clear(player, slot);
      return false;
    }
    std::vector<Entry>& h = hist_[player][slot];
    if (!h.empty() && !SameContent(h.back().after, before.slot[slot])) h.clear();  // stale: changed in between
    Entry e;
    e.before = before.slot[slot];
    e.after = after.slot[slot];
    e.cost = cost;
    h.push_back(e);
    return true;
  }

  // After any other transaction (a normal sale, a use, a swap, a consume): every slot that changed loses its history.
  // Returns the number of entries dropped.
  int OnOtherChange(unsigned player, const Inventory& before, const Inventory& after) {
    if (player >= kMaxPlayers) return 0;
    int n = 0;
    for (unsigned s = 0; s < kSlots; ++s)
      if (!SameSlot(before.slot[s], after.slot[s])) n += Clear(player, s);
    return n;
  }

  // Every tick: alive and out of the shop's reach clears all of the player's history. Returns the entries dropped.
  int OnPosition(unsigned player, bool alive, bool inReach) {
    if (player >= kMaxPlayers || !alive || inReach) return 0;
    return ClearPlayer(player);
  }

  // A sale request for `slot` holding `now`: the entry to undo, or null for a plain sale (no history, or a history the
  // slot no longer matches, which is then cleared). `item` is the request's item; a different one is a plain sale.
  const Entry* UndoFor(unsigned player, unsigned slot, std::uint32_t item, const Slot& now) {
    if (player >= kMaxPlayers || slot >= kSlots) return nullptr;
    std::vector<Entry>& h = hist_[player][slot];
    if (h.empty()) return nullptr;
    if (item != now.item || !SameContent(h.back().after, now)) {
      h.clear();
      return nullptr;
    }
    return &h.back();
  }

  // After the undo of the slot's last entry. The entry below it (an earlier purchase in the slot) stays undoable when
  // the slot is now what that one left.
  void Pop(unsigned player, unsigned slot) {
    if (player >= kMaxPlayers || slot >= kSlots) return;
    std::vector<Entry>& h = hist_[player][slot];
    if (!h.empty()) h.pop_back();
  }

  int Clear(unsigned player, unsigned slot) {
    if (player >= kMaxPlayers || slot >= kSlots) return 0;
    std::vector<Entry>& h = hist_[player][slot];
    const int n = static_cast<int>(h.size());
    h.clear();
    return n;
  }
  int ClearPlayer(unsigned player) {
    int n = 0;
    for (unsigned s = 0; s < kSlots; ++s) n += Clear(player, s);
    return n;
  }
  void Reset() {
    for (unsigned p = 0; p < kMaxPlayers; ++p) ClearPlayer(p);
  }

  std::size_t Depth(unsigned player, unsigned slot) const {
    return player < kMaxPlayers && slot < kSlots ? hist_[player][slot].size() : 0;
  }
  bool Any(unsigned player) const {
    if (player >= kMaxPlayers) return false;
    for (unsigned s = 0; s < kSlots; ++s)
      if (!hist_[player][s].empty()) return true;
    return false;
  }

 private:
  std::vector<Entry> hist_[kMaxPlayers][kSlots];
};

}  // namespace gms::refund
