// M18 (MSTATS3): damage and healing by source, engine-free.
//
// A hit or a heal is credited to the root ability of its chain: the attribute snapshot's credit GUID (+0x10; a
// modifier's hits keep its cast's GUID, a utility projectile is transparent, as for the client's lifedrain). Each
// seat has a table GUID -> source that the engine (gms/engine/matchstats_sources.cpp) fills from what the player holds:
//   - the shaper's slots (LabsPlayer_GetAbilityInSlot 0x00911B80, default and alternate): 0 the basic attack ->
//     `basic`; 3-6 Q/W/E/R -> `ability` slot 0-3; 2 the shaper's passive -> `ability` slot 4; 1 the ward -> `other`;
//   - each inventory entry's item (Item data: ActiveAbility +0x80, ActiveModifier +0x88, Passive[] +0x78): `spell` for
//     a battle spell (Item.Category 1), else `item`, the id = the item's asset id;
//   - the perk page's modifiers (the roster's spirit stone modifiers, perks.cpp) -> `perk`, the id = the modifier's
//     SPID (dgserver maps it to its stone);
// and every seat shares the role passives (modifier_Role_*) -> `role`. A GUID the table lacks falls back on the hit's
// descriptors: IsBasic -> `basic`, IsItem -> `item` id 0, anything else -> `other`.
#pragma once

#include <cstdint>
#include <map>
#include <string>

namespace gms::stats {

// One row's key in seatStats.sources (the contract's kinds).
struct Source {
  std::string kind = "other";  // basic, ability, item, spell, perk, role, other
  int slot = -1;               // ability only: 0-3 Q/W/E/R, 4 the passive
  std::int64_t id = 0;         // item/spell: the item asset id; perk: the modifier's SPID; else 0
  int role = -1;               // role only: 0 Gladiator, 1 Tactician, 2 Hunter, 3 Predator (the modifier's)
};

constexpr unsigned kDescIsBasic = 0x2;
constexpr unsigned kDescIsItem = 0x10000;

// The contract's row for engine ability slot 0-6 (LabsPlayer_GetAbilityInSlot): 0 basic, 2 ability 4 (the passive),
// 3-6 ability 0-3, 1 (the ward) and anything else other.
Source SlotSource(int engineSlot);
// An item's row: `spell` when its Item.Category is 1 (Ability: Spell_*), else `item`; id = the item's asset id.
Source ItemSource(bool spell, std::uint32_t itemAssetId);
Source PerkSource(std::uint32_t modifierSpid);

// The four role passives (RolesTuning.role[].rolePassive): modifier_Role_Gladiator, _Tactician, _Hunter, _Predator.
// The role index for a GUID, or -1.
int RoleOfGuid(std::uint32_t guid);
const char* RoleName(int role);  // "Gladiator", ... ("" for -1)

// A seat's GUID -> source table. Rebuilds merge: what the player holds now overwrites (an upgraded item takes over
// its component's shared passive), what it no longer holds stays (a sold item's or a used potion's lingering effects
// still go to it; an ability's GUID is unique to it).
class SourceTable {
 public:
  // Starts a rebuild: entries set from now on win over the earlier ones (one rebuild's first setter wins).
  void BeginRebuild() { ++generation_; }
  void Set(std::uint32_t guid, const Source& s);
  const Source* Find(std::uint32_t guid) const;
  std::size_t Size() const { return m_.size(); }

 private:
  struct Entry {
    Source source;
    unsigned generation = 0;
  };
  std::map<std::uint32_t, Entry> m_;
  unsigned generation_ = 0;
};

// The source of a hit or heal credited to a seat: the seat's table, then the role passives, then the descriptors.
// `fromTable` (optional) tells whether the table or a role named it.
Source Classify(const SourceTable* table, std::uint32_t creditGuid, unsigned descLo, bool* fromTable = nullptr);

// MSTATS3-live2: whom a hit by an attacker object that is not a shaper (a pet, a summon, an owned object) is
// credited to; the pet rule's live check. `attackerIsShaper`: the snapshot's caster is a player's creature;
// `creditSeat`: the snapshot's seat (+0x0E, else +0x0D; -1 none); `ownerSeat`: the seat of the shaper the attacker's
// mOwnerID names (-1: no owner, or not a shaper).
enum class AttackerCredit {
  Shaper,     // a shaper's own hit (the snapshot names its seat or not: the rest of the stats decide)
  Owner,      // a non-shaper's hit credited to its owner's seat (the rule holds)
  OtherSeat,  // credited to a seat that is not its owner's (or it has no shaper owner: a seat's projectile object)
  Lost,       // no seat although a shaper owns the attacker: the hit counts for nobody (the rule fails)
  NoOwner,    // no seat, no shaper owner: a minion's, a creep's, a structure's hit
};
AttackerCredit CreditOfAttacker(bool attackerIsShaper, int creditSeat, int ownerSeat);
const char* AttackerCreditName(AttackerCredit c);

}  // namespace gms::stats
