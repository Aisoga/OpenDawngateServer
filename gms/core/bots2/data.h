// gms/core/bots2 (BOTS2c): the game data the second bot brain's battle spells, potions, wards and the parasite read.
// Every number here is the data's (the scripts and assets of build 854), not a choice: the choices are tuning.h's.
// Header-only, no game addresses.
#pragma once

#include <cstring>

namespace gms::bots2 {

// The battle spells (AssetData Item, Category 1: Spell_<X>.Item), bought with the spell points of levels 1, 10 and 20
// from BotClass.spellOrder into inventory 6-8, cast through the gambit slots 7-9 (LabsPlayer_GetAbilityInSlot maps a
// slot s >= 7 to the item in inventory slot s - 1).
enum class Spell : int {
  kNone, kBastion, kBlink, kBlitz, kDeflect, kDispel, kDrain, kExceed, kStagger, kStasis, kTailwind, kVanquish, kWither,
  kCount
};

// How a spell is aimed: at the caster, an enemy shaper, an enemy NPC or structure, or a point.
enum class SpellAim : int { kSelf, kEnemyShaper, kEnemyNpc, kPoint };

struct SpellData {
  Spell spell;
  const char* name;  // the item's name part ("Spell_<name>.Item")
  SpellAim aim;
  float range;       // the ability's range (0: a self cast; Tailwind: its burst radius)
  float cooldownS;   // its cooldown (every spell has hasteCoefficient 0: no item shortens it)
};

// The spells' abilities [data, script]: ability_Item_<X> (Blink is ability_Item_Flash, Stasis ability_Item_Invuln).
inline const SpellData& DataOf(Spell s) {
  static const SpellData k[] = {
      {Spell::kNone, "", SpellAim::kSelf, 0.f, 0.f},
      {Spell::kBastion, "Bastion", SpellAim::kSelf, 0.f, 210.f},       // 2 s shield 60 + 20 x level, +20 armor and MR
      {Spell::kBlink, "Blink", SpellAim::kPoint, 18.39f, 300.f},       // a teleport (the cooldown only if it worked)
      {Spell::kBlitz, "Blitz", SpellAim::kSelf, 0.f, 210.f},           // +30 % move speed 6 s, Unstoppable 1.5-3 s
      {Spell::kDeflect, "Deflect", SpellAim::kPoint, 20.59f, 150.f},   // shield 50 + 10 x level, radius 12, allies + self
      {Spell::kDispel, "Dispel", SpellAim::kSelf, 0.f, 180.f},         // removes debuffs, Tenacity 0.35, +50 % speed 1 s
      {Spell::kDrain, "Drain", SpellAim::kEnemyShaper, 16.18f, 180.f}, // 5 ticks of magic damage, 90 % back as heal
      {Spell::kExceed, "Exceed", SpellAim::kSelf, 0.f, 90.f},          // +30 Power and +30 % for 5 s
      {Spell::kStagger, "Stagger", SpellAim::kEnemyShaper, 19.86f, 180.f},  // magic damage and a fading slow
      {Spell::kStasis, "Stasis", SpellAim::kSelf, 0.f, 240.f},         // stop; untargetable and immune 2 s
      {Spell::kTailwind, "Tailwind", SpellAim::kSelf, 16.92f, 120.f},  // +45 % move speed 3 s, allies within 16.92 too
      {Spell::kVanquish, "Vanquish", SpellAim::kEnemyNpc, 17.65f, 75.f},  // 300 + 30 x level true damage; NPCs, never
                                                                          // a shaper (refused live at a Binding too)
      {Spell::kWither, "Wither", SpellAim::kEnemyShaper, 16.18f, 180.f},  // 4 s: slow 0.4, attack slow 0.4, -30 % damage
  };
  const int i = static_cast<int>(s);
  return k[i >= 0 && i < static_cast<int>(Spell::kCount) ? i : 0];
}

inline const char* SpellName(Spell s) {
  const char* n = DataOf(s).name;
  return n[0] ? n : "none";
}

// The spell an inventory item is, by its asset name ("Spell_Drain.Item" -> kDrain); kNone for anything else.
inline Spell SpellFromItemName(const char* name) {
  if (!name || std::strncmp(name, "Spell_", 6) != 0) return Spell::kNone;
  const char* n = name + 6;
  for (int i = 1; i < static_cast<int>(Spell::kCount); ++i) {
    const SpellData& d = DataOf(static_cast<Spell>(i));
    const std::size_t len = std::strlen(d.name);
    if (std::strncmp(n, d.name, len) == 0 && (n[len] == '.' || n[len] == '\0')) return d.spell;
  }
  return Spell::kNone;
}

// The Vitality Potion (Consumable_Potion_HealingPotion.Item, 40 gold, stacks to 5, inventory 9-10; the bots' shop visit
// buys up to two): modifier_Item_Consumable_HealingPotion heals tickHealing 10 a second for its duration 15 s; refused at
// full health.
inline bool IsPotionItemName(const char* name) {
  return name && std::strncmp(name, "Consumable_Potion_HealingPotion", 31) == 0;
}
constexpr float kPotionHeal = 150.f;     // 10 x 15
constexpr float kPotionSeconds = 15.f;   // one heal at a time

// The ward (every shaper's slot 1, ability_Item_Ward): range 15.8, cooldown 180 s, a ward lives 180 s;
// condition_Bot_Ward places none within wardSearchRadius 29 of an allied ward (nNPCType Ward 16).
constexpr float kWardRange = 15.8f;
constexpr float kWardCooldownS = 180.f;
constexpr float kWardSpacing = 29.f;

// SM_Map_v5's ward spots: condition_Bot_Ward's MortalWards and SpiritWards (11 each, 18 distinct points; a level with
// ward markers of its own replaces them, MAPED4b). x, y.
inline const float (*DataWardSpots(int* n))[2] {
  static const float k[][2] = {
      {-117.f, -99.f}, {-143.f, 4.f},   {-78.f, -20.f}, {-99.f, -117.f}, {4.f, -143.f},    {9.f, -87.f},
      {-88.f, -85.f},  {21.f, -38.f},   {-4.f, 85.f},   {-4.f, 143.f},   {98.f, 119.f},    {78.f, 20.f},
      {143.f, -4.f},   {117.f, 99.f},   {85.f, 88.f},   {-21.f, 38.f},   {-125.f, 127.f},  {136.f, -116.f},
  };
  *n = static_cast<int>(sizeof k / sizeof k[0]);
  return k;
}

// The parasite (Objective_SpiritMonster_State1/2/3, nNPCType ObjectiveMajor 8): born at 300 s, stage 2 at 900 s
// (13,050 HP), stage 3 at 1,500 s (15,000 HP); its melee hits the nearest enemy player within 8, its ranged attacks
// players within 30 of the map's centre, it adds attackers within 27 (objective_aggro_on_sight); dead 300 s, then back.
constexpr float kParasiteRangedReach = 30.f;

}  // namespace gms::bots2
