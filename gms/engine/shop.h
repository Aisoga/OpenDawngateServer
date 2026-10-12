// D15c: the shop's purchase effect on the server. The stripped server shell called
// modifier_Base_ShoppeArchon.onItemPurchased(ctx) after a purchase: the archon's
// modifier instance's private table with shaperId and itemGUID set. The script (Modifiers/modifier_Base_ShoppeArchon)
// skips an enemy or dead buyer, then sends a homing projectile (Ability_Fireball.Noun, 40 u/s, the item's
// Item.ProjectileEffect) from the archon to the buyer, registers the item's ProjectileOnHit effect for its OnHitTarget
// and plays the archon's itemshoppe_archon_purchase_open/closed animation. Both item effects come from
// nBattlegrounds.GetItemFxProjectile/GetItemFxOnHit, which the client build stubs to "" (0x009248E0); the host
// registers them over the stubs (the item asset's Item +0x58 ProjectileEffect / +0x5C ProjectileOnHit names).
//
// The call goes through the engine's own GearsLua_CallFunction (0x00875330) on the instance's thread, as
// LabsAbilities_CallEventFunction (0x009063F0) calls an event handler: the instance held in use (+0x230), the
// argument the global table _PRIVATETHREADTABLE (parameter type 8). GearsLua_CallFunction takes an integer function key
// (nAbilityFns), so the host aliases onItemPurchased at a key no nAbilityFns uses (kOnItemPurchasedKey).
//
// [host] shop_fx: 1 (default) the archon of the buyer's team nearest to the buyer [assumed: SM_Map_v5 has two per
// base], 2 every archon of the buyer's team, 0 off (the stubs stay). shop_fx_log_limit (200).
#pragma once

#include <cstddef>
#include <cstdint>

namespace shop {

// The key the host stores modifier_Base_ShoppeArchon.onItemPurchased under (nAbilityFns ends at 66).
constexpr int kOnItemPurchasedKey = 1001;

// Registers nBattlegrounds.GetItemFxProjectile / GetItemFxOnHit over the client's stubs on the server's lua_State
// (match.cpp's OnSimulatorCreated). Reads the options at its first call.
void RegisterLua(void* L, void* sim);

// Server simulator bound. After a successful purchase (or a consume-on-purchase) of `itemId` by player `index` whose
// creature is `creature` (team `team`): calls onItemPurchased on the team's archon(s). `source` names the intake
// ("client", "bot", "test") for the log.
void OnItemPurchased(void* sim, unsigned index, std::uint32_t creature, unsigned team, std::uint32_t itemId,
                     const char* source);

// One-line counters for the stats ("stats shop:").
bool Enabled();
void FormatStats(char* buf, std::size_t size);

}  // namespace shop
