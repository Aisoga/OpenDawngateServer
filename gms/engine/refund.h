// REFUND1: a sale while still in the base undoes the purchase. The rule is engine-free in core/commands/refund.h; this part reads the server player's inventory with its
// build paths and carries an undo out with the client's own inventory functions:
//   LabsPlayer_SellItem (0x00914080): one of the bought item leaves the slot (LabsPlayer_RemoveItem 0x00913EE0: the
//     item's passives end through LabsPlayer_OnItemRemoved 0x00913C50, the attributes are recalculated),
//   LabsPlayer_PurchaseItem (0x00914690) at cost 0 for an upgrade's component: InventoryItem_Add (0x009139B0) into the
//     emptied slot, its passives start (LabsPlayer_OnItemAdded 0x00912950), AttributeData_Recalculate,
//   the slot's build path (+0x1C/+0x20) written back as it was before the purchase,
//   LabsPlayer_SetAvailableGold (0x009111C0): the gold before the sale plus the purchase's cost,
//   for Decay or Strife over Might: Might's kill count back (TryUndo).
// commands.cpp answers the request success 0 (no client repeats a sale on its copy); the AA delta brings the slot and
// the gold. [host] item_undo (1; 0 = every sale is a plain one), item_undo_log_limit (400).
#pragma once

#include <cstddef>
#include <cstdint>

#include "core/commands/refund.h"

namespace refund {

void Configure(bool enabled, int logLimit);
bool Enabled();

// The server player's 11 slots (asset id, count, the build path's two asset pointers). Server simulator bound; false
// on an exception.
bool ReadInventory(void* player, gms::refund::Inventory* out);

// A sale the shop check (LabsPlayer_CanUseItemShop) and LabsPlayer_CanSellItem allowed: if the slot has history, the
// undo is carried out and true returned (the caller answers success 0); `text` gets the log's part. False: a plain sale.
// `object` is the player's creature: an upgrade that took a kill count over (Decay or Strife over Might,
// gms::refund::HandBack) gives it back to the restored passive. Its function, aliased into the passive's Lua table at
// kHandBackKey, does what the script does with a count: RequestModifier + SetStackCount of the passive's count
// modifier, then the MaxHealth and Power bonus as the passive's kill branch adds it (kept in its private table, so its
// Deactivate removes them), run on the instance's thread through GearsLua_CallFunction as shop.cpp calls
// onItemPurchased.
bool TryUndo(void* sim, void* player, void* object, unsigned index, void* asset, std::uint32_t item, unsigned slot,
             char* text, std::size_t n);

// The key the host stores the hand-back function under in the passive's table (nAbilityFns ends at 66; shop.h uses
// 1001).
constexpr int kHandBackKey = 1002;

// After a transaction the binary carried out (type 1-5), with the inventories around it; `cost` is CheckPurchase's.
void AfterTransaction(void* player, unsigned index, std::uint8_t type, void* asset, unsigned slot, int cost,
                      const gms::refund::Inventory& before, const gms::refund::Inventory& after);

// Every tick for each player: alive and out of the shop's reach clears the player's history (logged with `distance`).
void OnPosition(unsigned index, bool alive, bool inReach, float distance);

// Whether the player has any history (cheap; AfterTick asks before it reads the position).
bool Any(unsigned index);

// "undo: recorded N, undone N (failed N), cleared N (left N, changed N)".
void FormatStats(char* buf, std::size_t size);

}  // namespace refund
