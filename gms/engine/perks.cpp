// D3-perks: a perk page's effect on the server simulator; see perks.h and docs/GMS_PROTOCOL.md *Perk pages in shaper
// select (D3-perks)*.
#include "perks.h"

#include <windows.h>

#include <cstdio>
#include <map>
#include <string>

#include "core/protocol/messages.h"
#include "core/session/shaper_select.h"
#include "game.h"
#include "log.h"
#include "passives.h"

using namespace game;

namespace perks {
namespace {

Options g_opt;
// D3c: each human's page modifiers as ApplyAtDeploy got them (by player index), for its respawns
std::map<unsigned, std::vector<std::uint32_t>> g_pageModifiers;

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  Log("EXCEPTION in perks (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

// The server simulator bound to the thread for one call, the previous one restored afterwards.
class BoundTo {
 public:
  explicit BoundTo(void* sim) : prev_(GetCurrentSim()) { SetCurrentSim(sim); }
  ~BoundTo() { SetCurrentSim(prev_); }
  BoundTo(const BoundTo&) = delete;
  BoundTo& operator=(const BoundTo&) = delete;

 private:
  void* prev_;
};

// One loadout entry into the player's attribute map, as LoadoutManager_ReadPlayerLoadout fills C0 02's: key
// (op << 16 | attribute) -> {attribute, op, value}. *readBack: what LabsPlayer_GetLoadoutAttribute returns for it.
bool TryWriteEntry(void* sim, unsigned index, const gms::LoadoutAttribute& a, float* readBack) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    if (!p) return false;
    const std::uint32_t key = (static_cast<std::uint32_t>(a.op) << 16) | a.attribute;
    std::uint32_t* e = LoadoutAttributeSlot(static_cast<char*>(p) + player::kLoadoutAttributes, &key);
    if (!e) return false;
    e[0] = a.attribute;
    e[1] = a.op;
    *reinterpret_cast<float*>(&e[2]) = a.value;
    *readBack = GetLoadoutAttribute(p, a.attribute, a.op);
    return true;
  } __except (SehNote(GetExceptionInformation(), "loadout entry")) {
    return false;
  }
}

// An attribute modifier on the creature (AttributeData_AddModifier); -1 if it failed. *after: the attribute then.
int TryAddModifier(std::uint32_t creature, const gms::LoadoutAttribute& a, float* after) {
  __try {
    void* o = GetObjectById(creature);
    void* ad = o ? Field<void*>(o, gobj::kAttributes) : nullptr;
    if (!ad) return -1;
    const int h = AddAttributeModifier(ad, a.attribute, a.op, a.value);
    *after = GetAttribute(ad, a.attribute, 1);
    return h;
  } __except (SehNote(GetExceptionInformation(), "attribute modifier")) {
    return -1;
  }
}

// A modifier (an ability) activated on the creature, caster and target the creature, rank 1, as a brain's passive
// (LabsAbilities_ActivateAbility: the instance, its ModifierCreated, its Activate). 0 if it failed.
unsigned TryActivate(std::uint32_t creature, std::uint32_t ability) {
  __try {
    if (!GetObjectById(creature)) return 0;
    return ActivateAbility(ability, creature, creature, 0, 1, 0.f, 0.f, 0.f, 0);
  } __except (SehNote(GetExceptionInformation(), "activate modifier")) {
    return 0;
  }
}

// D3c: the player's current creature and which of the page's modifiers it holds live. False without a creature.
bool TryHeld(void* sim, unsigned index, const std::vector<std::uint32_t>& modifiers, std::uint32_t* creature,
             std::vector<std::uint32_t>* held) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    *creature = p ? Field<std::uint32_t>(p, player::kCurrentCreature) : 0;
    void* o = *creature ? GetObjectById(*creature) : nullptr;
    if (!o) return false;
    for (std::uint32_t m : modifiers) {
      void* st = FindStaticAbility(m);
      if (st && passives::CreatureHasLiveInstance(o, st)) held->push_back(m);
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "held modifiers")) {
    return false;
  }
}

// The creature's current and max HP for the log (combatant data); false without one.
bool TryHp(std::uint32_t creature, float* hp, float* maxHp) {
  __try {
    void* o = GetObjectById(creature);
    void* ad = o ? Field<void*>(o, gobj::kAttributes) : nullptr;
    char* cd = o ? Field<char*>(o, gobj::kCombatant) : nullptr;
    if (!ad || !cd) return false;
    *hp = Field<float>(cd, combatant::kHitPoints);
    *maxHp = GetAttribute(ad, 2, 1);  // MaxHealth
    return true;
  } __except (SehNote(GetExceptionInformation(), "hp")) {
    return false;
  }
}

std::string Entry(const gms::LoadoutAttribute& a) {
  char b[48];
  std::snprintf(b, sizeof b, "%u/%u %.3f", a.attribute, a.op, a.value);
  return b;
}

}  // namespace

void Configure(const Options& options) {
  g_opt = options;
  Log("perks: D3-perks %s (the page's Add entries via the player's loadout; percent %s, modifiers %s; bots' "
      "modifier_Bot_DefaultLoadout %s; D3c: the page's death-ended modifiers at a respawn %s)",
      g_opt.enabled ? "on" : "OFF ([host] perks=0: only C8 09, C8 0A and C0 02)", g_opt.percent ? "on" : "off",
      g_opt.modifiers ? "on" : "off", g_opt.botLoadout ? "on" : "off",
      g_opt.respawn ? "activated again" : "not renewed ([host] perk_respawn=0)");
}

const Options& Settings() { return g_opt; }

bool WritePlayerLoadout(void* sim, unsigned index, const std::vector<gms::LoadoutAttribute>& attributes) {
  if (!g_opt.enabled || attributes.empty()) return true;
  BoundTo b(sim);
  std::string done;
  int failed = 0;
  for (const gms::LoadoutAttribute& a : attributes) {
    float back = 0.f;
    if (!TryWriteEntry(sim, index, a, &back)) {
      ++failed;
      continue;
    }
    char x[64];
    std::snprintf(x, sizeof x, " %s (reads %.3f)", Entry(a).c_str(), back);
    done += x;
  }
  Log("perks: player %u's loadout (attribute/op value): %zu entr%s written%s%s", index, attributes.size() - failed,
      attributes.size() - failed == 1 ? "y" : "ies", done.c_str(), failed ? "; SOME FAILED" : "");
  return failed == 0;
}

void ApplyAtDeploy(void* sim, unsigned index, std::uint32_t creature, const std::vector<gms::LoadoutAttribute>& attributes,
                   const std::vector<std::uint32_t>& modifiers) {
  if (!g_opt.enabled || !creature) return;
  BoundTo b(sim);
  float hp = 0.f, maxHp = 0.f;
  if (TryHp(creature, &hp, &maxHp))
    Log("perks: player %u's creature %08X at its deploy: HP %.1f / MaxHealth %.1f (with the loadout's Add entries)",
        index, creature, hp, maxHp);
  if (g_opt.percent)
    for (const gms::LoadoutAttribute& a : gms::LoadoutCreatureModifiers(attributes)) {
      float after = 0.f;
      const int h = TryAddModifier(creature, a, &after);
      Log("perks: player %u's creature %08X: attribute modifier %s -> handle %d, the attribute now %.3f [assumed]", index,
          creature, Entry(a).c_str(), h, after);
    }
  if (g_opt.modifiers) g_pageModifiers[index] = modifiers;
  if (g_opt.modifiers)
    for (std::uint32_t m : modifiers) {
      const unsigned inst = TryActivate(creature, m);
      Log("perks: player %u's creature %08X: modifier %08X activated -> instance %08X%s [assumed]", index, creature, m,
          inst, inst ? "" : " (FAILED)");
    }
}

void AtRespawn(void* sim, unsigned index) {
  if (!g_opt.enabled || !g_opt.modifiers) return;
  auto it = g_pageModifiers.find(index);
  if (it == g_pageModifiers.end() || it->second.empty()) return;
  BoundTo b(sim);
  std::uint32_t creature = 0;
  std::vector<std::uint32_t> held;
  if (!TryHeld(sim, index, it->second, &creature, &held)) {
    Log("perks: player %u at the respawn: no creature; the page's modifiers not checked", index);
    return;
  }
  std::string kept;
  for (std::uint32_t m : held) {
    char x[16];
    std::snprintf(x, sizeof x, " %08X", m);
    kept += x;
  }
  const std::vector<std::uint32_t> renew = gms::PageModifiersToRenew(it->second, held);
  Log("perks: player %u's creature %08X at the respawn: %zu of the page's %zu modifier(s) kept through the death:%s; "
      "%zu ended with it%s",
      index, creature, held.size(), it->second.size(), kept.empty() ? " -" : kept.c_str(), renew.size(),
      renew.empty() ? "" : (g_opt.respawn ? " (activated again)" : " (NOT renewed: [host] perk_respawn=0)"));
  if (!g_opt.respawn) return;
  for (std::uint32_t m : renew) {
    const unsigned inst = TryActivate(creature, m);
    Log("perks: player %u's creature %08X at the respawn: modifier %08X activated again -> instance %08X%s [assumed]",
        index, creature, m, inst, inst ? "" : " (FAILED)");
  }
}

void ApplyBotLoadout(void* sim, unsigned index, std::uint32_t creature) {
  if (!g_opt.enabled || !g_opt.botLoadout || !creature) return;
  BoundTo b(sim);
  static const std::uint32_t kBotLoadout = gms::AssetIdOf("modifier_Bot_DefaultLoadout");
  float hp0 = 0.f, max0 = 0.f, hp1 = 0.f, max1 = 0.f;
  const bool before = TryHp(creature, &hp0, &max0);
  const unsigned inst = TryActivate(creature, kBotLoadout);
  const bool after = TryHp(creature, &hp1, &max1);
  Log("perks: bot %u's creature %08X: modifier_Bot_DefaultLoadout (%08X) -> instance %08X%s; MaxHealth %.1f -> %.1f, "
      "HP %.1f -> %.1f [inferred]",
      index, creature, kBotLoadout, inst, inst ? "" : " (FAILED)", before ? max0 : -1.f, after ? max1 : -1.f,
      before ? hp0 : -1.f, after ? hp1 : -1.f);
}

}  // namespace perks
