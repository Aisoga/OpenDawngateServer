// RULES4a: build 854's 106 match items by kind, for the item pools (core/rules/shop.h). Generated from
// the item catalog (columns item, id, category, tier, flags: a Stat item's tier, Ability = a battle spell, a
// Consumable with the cheat flag = a cheat item). The id is the wire's (FNV-1 of the lower-case "<name>.item", checked
// by gms_tests RulesItemTable). All 106 are in the shop's ItemSet ActiveItems_SM_Map_V5 (the match items notes).
#pragma once

#include <cstddef>
#include <cstdint>

namespace gms::rules {

enum class ItemKind : std::uint8_t { kUnknown, kBasic, kAdvanced, kLegendary, kSpell, kConsumable, kCheat };

struct ItemRow {
  std::uint32_t id;
  const char* name;
  ItemKind kind;
};

inline const ItemRow* ItemTable(std::size_t* count) {
  static const ItemRow kRows[] = {
      {0x0484AAA0, "Advanced_Abolition", ItemKind::kAdvanced},
      {0xC8846639, "Advanced_Aggression", ItemKind::kAdvanced},
      {0xD2DDFA15, "Advanced_Balance", ItemKind::kAdvanced},
      {0x8DB56EF1, "Advanced_Clarity", ItemKind::kAdvanced},
      {0x7AE6CC48, "Advanced_Command", ItemKind::kAdvanced},
      {0x2D4AEB6E, "Advanced_Consumption", ItemKind::kAdvanced},
      {0xD37B324C, "Advanced_Control", ItemKind::kAdvanced},
      {0x008B0129, "Advanced_Conviction", ItemKind::kAdvanced},
      {0xEA8DF487, "Advanced_Desire", ItemKind::kAdvanced},
      {0xA90CD55F, "Advanced_Discipline", ItemKind::kAdvanced},
      {0xF485EF39, "Advanced_Energy", ItemKind::kAdvanced},
      {0x9DA67B0A, "Advanced_Force", ItemKind::kAdvanced},
      {0x35EA2247, "Advanced_Form", ItemKind::kAdvanced},
      {0x813F2DE7, "Advanced_Freedom", ItemKind::kAdvanced},
      {0xEA6C002A, "Advanced_Growth", ItemKind::kAdvanced},
      {0x5CB67D6D, "Advanced_Inspiration", ItemKind::kAdvanced},
      {0x22651152, "Advanced_Integrity", ItemKind::kAdvanced},
      {0x584438CE, "Advanced_Might", ItemKind::kAdvanced},
      {0x79E76C0B, "Advanced_Motion", ItemKind::kAdvanced},
      {0xF16A118F, "Advanced_Pride", ItemKind::kAdvanced},
      {0x13587A9C, "Advanced_Purity", ItemKind::kAdvanced},
      {0xBD9441F1, "Advanced_Resolve", ItemKind::kAdvanced},
      {0x46E84A06, "Advanced_Vigor", ItemKind::kAdvanced},
      {0x183E3A78, "Basic_Hunger", ItemKind::kBasic},
      {0xB22758B1, "Basic_Life", ItemKind::kBasic},
      {0x595ED4FA, "Basic_Power", ItemKind::kBasic},
      {0x6F470916, "Basic_Resilience", ItemKind::kBasic},
      {0x7FDF6F5A, "Basic_Time", ItemKind::kBasic},
      {0xD1CDA3DF, "Basic_Will", ItemKind::kBasic},
      {0xA31D4A5B, "Consumable_Debug_GrantExp", ItemKind::kCheat},
      {0xE294A308, "Consumable_Debug_GrantGold", ItemKind::kCheat},
      {0xB747A7D5, "Consumable_Flask_Power", ItemKind::kConsumable},
      {0xA7C7933C, "Consumable_Flask_Vim", ItemKind::kConsumable},
      {0xBEC8DBFA, "Consumable_Flask_Vision", ItemKind::kConsumable},
      {0x11A0F947, "Consumable_Potion_HealingPotion", ItemKind::kConsumable},
      {0xEAB6BFD2, "Legendary_Adamance", ItemKind::kLegendary},
      {0x4D83E1BF, "Legendary_Ambition", ItemKind::kLegendary},
      {0xA4593C15, "Legendary_Assimilation", ItemKind::kLegendary},
      {0x63C3F8AA, "Legendary_Betrayal", ItemKind::kLegendary},
      {0x97AF2200, "Legendary_Chaos", ItemKind::kLegendary},
      {0x79333B14, "Legendary_Conquest", ItemKind::kLegendary},
      {0x4DF411E3, "Legendary_Corruption", ItemKind::kLegendary},
      {0xD9E78243, "Legendary_Creation", ItemKind::kLegendary},
      {0xFC34A372, "Legendary_Decay", ItemKind::kLegendary},
      {0xFA8CE86F, "Legendary_Defiance", ItemKind::kLegendary},
      {0x49056D24, "Legendary_Destruction", ItemKind::kLegendary},
      {0xD0A7565C, "Legendary_Devotion", ItemKind::kLegendary},
      {0xF6F88EF6, "Legendary_Divinity", ItemKind::kLegendary},
      {0x6809FA7C, "Legendary_Dominance", ItemKind::kLegendary},
      {0xD26C7B6A, "Legendary_Duress", ItemKind::kLegendary},
      {0x3768ED3C, "Legendary_Empathy", ItemKind::kLegendary},
      {0xB9E0FE48, "Legendary_Equilibrium", ItemKind::kLegendary},
      {0x4792EAB0, "Legendary_Faith", ItemKind::kLegendary},
      {0xD4EBC6FC, "Legendary_Fate", ItemKind::kLegendary},
      {0x33DA840D, "Legendary_Finesse", ItemKind::kLegendary},
      {0x05E8C38A, "Legendary_Fluidity", ItemKind::kLegendary},
      {0xAD2F89A0, "Legendary_Furor", ItemKind::kLegendary},
      {0x848E0E69, "Legendary_Glory", ItemKind::kLegendary},
      {0x62A19ED4, "Legendary_Grace", ItemKind::kLegendary},
      {0xED6E120E, "Legendary_Harmony", ItemKind::kLegendary},
      {0x38234B18, "Legendary_Hope", ItemKind::kLegendary},
      {0xDFC050AD, "Legendary_Hostility", ItemKind::kLegendary},
      {0xC6E5AB8B, "Legendary_Impulse", ItemKind::kLegendary},
      {0x51AE1179, "Legendary_Inevitability", ItemKind::kLegendary},
      {0x2923BB47, "Legendary_Influence", ItemKind::kLegendary},
      {0x03C416DC, "Legendary_Insight", ItemKind::kLegendary},
      {0x8A86E54F, "Legendary_Judgement", ItemKind::kLegendary},
      {0xAB47FDDD, "Legendary_Justice", ItemKind::kLegendary},
      {0x51CD1140, "Legendary_Momentum", ItemKind::kLegendary},
      {0xC9E846F6, "Legendary_Oppression", ItemKind::kLegendary},
      {0x977E2A08, "Legendary_Order", ItemKind::kLegendary},
      {0x5F9338CA, "Legendary_Pain", ItemKind::kLegendary},
      {0xE777D941, "Legendary_Passion", ItemKind::kLegendary},
      {0xBA39CD7C, "Legendary_Pestilence", ItemKind::kLegendary},
      {0xD62F5190, "Legendary_Potency", ItemKind::kLegendary},
      {0x8B99C808, "Legendary_Preservation", ItemKind::kLegendary},
      {0x02696CC3, "Legendary_Prosperity", ItemKind::kLegendary},
      {0x6F553DFA, "Legendary_Pursuit", ItemKind::kLegendary},
      {0xA7AE27CB, "Legendary_Rage", ItemKind::kLegendary},
      {0x5FD24179, "Legendary_Rampancy", ItemKind::kLegendary},
      {0x99BD92EE, "Legendary_Rebirth", ItemKind::kLegendary},
      {0xB0FB4E50, "Legendary_Resonance", ItemKind::kLegendary},
      {0xA6C747D1, "Legendary_Retribution", ItemKind::kLegendary},
      {0xF4DE8330, "Legendary_Ruin", ItemKind::kLegendary},
      {0xE7327947, "Legendary_Stability", ItemKind::kLegendary},
      {0xDD1E0AE9, "Legendary_Strife", ItemKind::kLegendary},
      {0xD74B4399, "Legendary_Subjugation", ItemKind::kLegendary},
      {0x8DD2812F, "Legendary_Supremacy", ItemKind::kLegendary},
      {0x998E205E, "Legendary_Valor", ItemKind::kLegendary},
      {0x31E5063A, "Legendary_Vengeance", ItemKind::kLegendary},
      {0xF3CD7A80, "Legendary_Vibrance", ItemKind::kLegendary},
      {0xF1F810B3, "Legendary_Voracity", ItemKind::kLegendary},
      {0x0EC81301, "Legendary_Wisdom", ItemKind::kLegendary},
      {0x34D0A812, "Legendary_Zeal", ItemKind::kLegendary},
      {0xA64B17C3, "Spell_Bastion", ItemKind::kSpell},
      {0x77E54E39, "Spell_Blink", ItemKind::kSpell},
      {0x3BD2B13C, "Spell_Blitz", ItemKind::kSpell},
      {0x3E44297C, "Spell_Deflect", ItemKind::kSpell},
      {0xBD728862, "Spell_Dispel", ItemKind::kSpell},
      {0xC47D935D, "Spell_Drain", ItemKind::kSpell},
      {0x8D206703, "Spell_Exceed", ItemKind::kSpell},
      {0x48F32660, "Spell_Stagger", ItemKind::kSpell},
      {0x32ABE558, "Spell_Stasis", ItemKind::kSpell},
      {0x21ED00DF, "Spell_Tailwind", ItemKind::kSpell},
      {0xDF2644E2, "Spell_Vanquish", ItemKind::kSpell},
      {0xC7335020, "Spell_Wither", ItemKind::kSpell},
  };
  *count = sizeof kRows / sizeof kRows[0];
  return kRows;
}

// The item's kind; kUnknown for an id not in the table.
inline ItemKind KindOf(std::uint32_t id) {
  std::size_t n = 0;
  const ItemRow* rows = ItemTable(&n);
  for (std::size_t i = 0; i < n; ++i)
    if (rows[i].id == id) return rows[i].kind;
  return ItemKind::kUnknown;
}

// The item's name ("Basic_Time"); "?" for an id not in the table.
inline const char* ItemNameOf(std::uint32_t id) {
  std::size_t n = 0;
  const ItemRow* rows = ItemTable(&n);
  for (std::size_t i = 0; i < n; ++i)
    if (rows[i].id == id) return rows[i].name;
  return "?";
}

inline const char* KindName(ItemKind k) {
  switch (k) {
    case ItemKind::kBasic:
      return "Basic";
    case ItemKind::kAdvanced:
      return "Advanced";
    case ItemKind::kLegendary:
      return "Legendary";
    case ItemKind::kSpell:
      return "spell";
    case ItemKind::kConsumable:
      return "consumable";
    case ItemKind::kCheat:
      return "cheat item";
    case ItemKind::kUnknown:
    default:
      return "not in the catalog";
  }
}

}  // namespace gms::rules
