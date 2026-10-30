// M18 (MSTATS3): damage and healing by source; the rules are in sources.h.
#include "core/stats/sources.h"

#include "core/protocol/messages.h"

namespace gms::stats {

Source SlotSource(int engineSlot) {
  Source s;
  if (engineSlot == 0) {
    s.kind = "basic";
  } else if (engineSlot == 2) {
    s.kind = "ability";
    s.slot = 4;
  } else if (engineSlot >= 3 && engineSlot <= 6) {
    s.kind = "ability";
    s.slot = engineSlot - 3;
  }
  return s;
}

Source ItemSource(bool spell, std::uint32_t itemAssetId) {
  Source s;
  s.kind = spell ? "spell" : "item";
  s.id = itemAssetId;
  return s;
}

Source PerkSource(std::uint32_t modifierSpid) {
  Source s;
  s.kind = "perk";
  s.id = modifierSpid;
  return s;
}

int RoleOfGuid(std::uint32_t guid) {
  static const std::uint32_t kRoles[4] = {
      AssetIdOf("modifier_Role_Gladiator"), AssetIdOf("modifier_Role_Tactician"), AssetIdOf("modifier_Role_Hunter"),
      AssetIdOf("modifier_Role_Predator")};
  if (!guid) return -1;
  for (int i = 0; i < 4; ++i)
    if (kRoles[i] == guid) return i;
  return -1;
}

const char* RoleName(int role) {
  switch (role) {
    case 0: return "Gladiator";
    case 1: return "Tactician";
    case 2: return "Hunter";
    case 3: return "Predator";
    default: return "";
  }
}

void SourceTable::Set(std::uint32_t guid, const Source& s) {
  if (!guid) return;
  auto it = m_.find(guid);
  if (it != m_.end() && it->second.generation == generation_) return;  // this rebuild's first setter wins
  m_[guid] = Entry{s, generation_};
}

const Source* SourceTable::Find(std::uint32_t guid) const {
  auto it = m_.find(guid);
  return it == m_.end() ? nullptr : &it->second.source;
}

Source Classify(const SourceTable* table, std::uint32_t creditGuid, unsigned descLo, bool* fromTable) {
  if (fromTable) *fromTable = true;
  if (creditGuid && table)
    if (const Source* s = table->Find(creditGuid)) return *s;
  const int role = RoleOfGuid(creditGuid);
  if (role >= 0) {
    Source s;
    s.kind = "role";
    s.role = role;
    return s;
  }
  if (fromTable) *fromTable = false;
  Source s;
  if (descLo & kDescIsBasic)
    s.kind = "basic";  // a basic-attack override (its own GUID, not the class's)
  else if (descLo & kDescIsItem)
    s.kind = "item";  // an item's effect the table doesn't know: id 0
  return s;
}

AttackerCredit CreditOfAttacker(bool attackerIsShaper, int creditSeat, int ownerSeat) {
  if (attackerIsShaper) return AttackerCredit::Shaper;
  if (creditSeat >= 0) return creditSeat == ownerSeat ? AttackerCredit::Owner : AttackerCredit::OtherSeat;
  return ownerSeat >= 0 ? AttackerCredit::Lost : AttackerCredit::NoOwner;
}

const char* AttackerCreditName(AttackerCredit c) {
  switch (c) {
    case AttackerCredit::Shaper: return "shaper";
    case AttackerCredit::Owner: return "owner";
    case AttackerCredit::OtherSeat: return "other seat";
    case AttackerCredit::Lost: return "LOST";
    case AttackerCredit::NoOwner: return "no owner";
  }
  return "?";
}

}  // namespace gms::stats
