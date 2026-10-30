// M18 (MSTATS3): damage and healing by source on the match host; see matchstats_sources.h and gms/core/stats/sources.h.
#include "matchstats_sources.h"

#include <windows.h>

#include <cstdio>
#include <cstring>
#include <map>
#include <string>
#include <vector>

#include "core/damage/pipeline.h"
#include "game.h"
#include "hook.h"
#include "log.h"
#include "matchstats.h"
#include "perks.h"

using namespace game;
namespace st = gms::stats;

namespace matchstats {
namespace {

void* g_sim;
HealDamageFn g_origHeal;
st::SourceTable g_tables[st::kSeats];
ULONGLONG g_lastBuild[st::kSeats];
unsigned g_exceptions, g_rebuilds, g_foreignHeals, g_shieldCalls;
// the GUIDs that fell back, per seat: GUID -> (fallback kind, hits and heals)
std::map<std::uint32_t, std::pair<std::string, unsigned>> g_unknown[st::kSeats];
// the heal core's nesting (the overload heal calls it again from inside): each level's nested gain
constexpr int kMaxDepth = 8;
int g_depth;
float g_nested[kMaxDepth];

// MSTATS3-live2: the pet rule's check (OnHitAttacker): enemy hits per AttackerCredit, the pets' part, and per
// non-shaper attacker noun with a seat or a shaper owner its credit and hits
constexpr int kCredits = 5;
unsigned g_creditHits[kCredits], g_creditPetHits[kCredits];
double g_creditDamage[kCredits];
std::map<std::string, unsigned> g_summons;
constexpr int kMaxSummonNouns = 12;
int g_petCheck = -1;  // GameObject_IsPlayerPet's prologue: -1 unchecked, 0 differs, 1 build 854's

constexpr ULONGLONG kRebuildGapMs = 250;
constexpr int kMaxUnknownPerSeat = 16;
constexpr int kEngineSlots = 7;      // 0 basic, 1 ward, 2 passive, 3-6 Q/W/E/R
constexpr int kMaxItemPassives = 8;  // per item (the data has 0-3)

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  if (++g_exceptions <= 20)
    Log("EXCEPTION in match_stats sources (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
        reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

bool Ours() { return g_sim && GetCurrentSim() == g_sim; }
bool ValidSeat(int s) { return s >= 0 && s < st::kSeats; }

// The GUIDs of what the player holds, in the table's order of precedence (a rebuild's first setter wins).
struct Holdings {
  std::uint32_t slots[kEngineSlots][2] = {};  // default and alternate
  struct Item {
    std::uint32_t asset = 0;
    bool spell = false;
    std::uint32_t active = 0, activeModifier = 0;
    std::uint32_t passives[kMaxItemPassives] = {};
    int passiveCount = 0;
  } items[player::kItemSlots];
};

bool SehHoldings(void* sim, int seat, Holdings* h) {
  __try {
    char* p = static_cast<char*>(GetPlayerByIndex(sim, static_cast<unsigned>(seat)));
    if (!p) return false;
    for (int s = 0; s < kEngineSlots; ++s)
      for (int k = 0; k < 2; ++k) h->slots[s][k] = GetAbilityInSlot(p, s, k + 1);
    for (unsigned i = 0; i < player::kItemSlots; ++i) {
      char* entry = p + player::kItems + i * player::kItemSize;
      char* asset = Field<char*>(entry, 0);
      char* data = asset ? Field<char*>(asset, asset::kData) : nullptr;
      if (!data) continue;
      Holdings::Item& it = h->items[i];
      it.asset = Field<std::uint32_t>(asset, asset::kId);
      it.spell = Field<int>(data, itemdata::kCategory) == itemdata::kCategorySpell;
      it.active = Field<std::uint32_t>(data, itemdata::kActiveAbility);
      it.activeModifier = Field<std::uint32_t>(data, itemdata::kActiveModifier);
      char* ps = Field<char*>(data, itemdata::kPassives);
      const int n = Field<int>(data, itemdata::kPassives + 4);
      for (int k = 0; ps && k < n && k < kMaxItemPassives; ++k)
        it.passives[it.passiveCount++] = Field<std::uint32_t>(ps, k * itemdata::kPassiveSize);
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "the player's holdings")) {
    return false;
  }
}

void Rebuild(void* sim, int seat) {
  Holdings h;
  g_lastBuild[seat] = GetTickCount64();
  if (!SehHoldings(sim, seat, &h)) return;
  st::SourceTable& t = g_tables[seat];
  t.BeginRebuild();
  for (int s = 0; s < kEngineSlots; ++s)
    for (int k = 0; k < 2; ++k) t.Set(h.slots[s][k], st::SlotSource(s));
  for (const Holdings::Item& it : h.items) {
    if (!it.asset) continue;
    const st::Source src = st::ItemSource(it.spell, it.asset);
    t.Set(it.active, src);
    t.Set(it.activeModifier, src);
    for (int k = 0; k < it.passiveCount; ++k) t.Set(it.passives[k], src);
  }
  for (std::uint32_t m : perks::PageModifiers(static_cast<unsigned>(seat))) t.Set(m, st::PerkSource(m));
  ++g_rebuilds;
}

// A static ability's Lua name for the log ("?" if none).
void SehAbilityName(std::uint32_t guid, char* buf, std::size_t size) {
  std::snprintf(buf, size, "?");
  __try {
    char* stc = guid ? static_cast<char*>(FindStaticAbility(guid)) : nullptr;
    const char* n = stc ? *reinterpret_cast<const char**>(stc) : nullptr;
    if (n) std::snprintf(buf, size, "%s", n);
  } __except (SehNote(GetExceptionInformation(), "ability name")) {
  }
}

// ---- the heal core ------------------------------------------------------------------------------------------------

float SehHp(void* o) {
  __try {
    char* cd = Field<char*>(o, combatant::kOfObject);
    return cd ? Field<float>(cd, combatant::kHitPoints) : -1.f;
  } __except (SehNote(GetExceptionInformation(), "the healed object's HP")) {
    return -1.f;
  }
}

// What the event needs of the snapshot and the target; false if they can't be read.
bool SehHealFacts(void* object, void* snapshot, st::HealEvent* e, std::uint32_t* guid) {
  __try {
    e->targetPlayer = Field<unsigned char>(object, gobj::kPlayerControlled) != 0;
    const unsigned ts = Field<unsigned char>(object, gobj::kPlayerIndex);
    e->targetSeat = e->targetPlayer && ts < st::kSeats ? static_cast<int>(ts) : -1;
    e->snapshot = snapshot != nullptr;
    *guid = 0;
    if (snapshot) {
      unsigned s = Field<unsigned char>(snapshot, snap::kCreditPlayerIndex);
      if (s == 0xFF) s = Field<unsigned char>(snapshot, snap::kPlayerIndex);
      e->healerSeat = s < st::kSeats ? static_cast<int>(s) : -1;
      e->sameTeam = Field<unsigned char>(snapshot, snap::kTeam) == Field<unsigned char>(object, gobj::kTeam);
      *guid = Field<std::uint32_t>(snapshot, snap::kAbilityGuid);
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "the heal's snapshot")) {
    return false;
  }
}

void CallOriginal(void* object, void* snapshot, float amount, float coefficient, unsigned descLo, unsigned descHi,
                  bool noScaling, float* healed, bool* isCrit, unsigned unknown, bool isLifesteal, bool canCrit,
                  bool allowOverload) {
  __try {
    g_origHeal(object, snapshot, amount, coefficient, descLo, descHi, noScaling, healed, isCrit, unknown, isLifesteal,
               canCrit, allowOverload);
  } __finally {
    --g_depth;  // also when an exception leaves the core (the caller's handler catches it)
  }
}

void __cdecl HealHook(void* object, void* snapshot, float amount, float coefficient, unsigned descLo, unsigned descHi,
                      bool noScaling, float* healed, bool* isCrit, unsigned unknown, bool isLifesteal, bool canCrit,
                      bool allowOverload) {
  st::Accumulator* acc = object && Ours() && g_depth < kMaxDepth ? Counting() : nullptr;
  if (!acc) {
    if (object && !Ours()) ++g_foreignHeals;
    g_origHeal(object, snapshot, amount, coefficient, descLo, descHi, noScaling, healed, isCrit, unknown, isLifesteal,
               canCrit, allowOverload);
    return;
  }
  const float before = SehHp(object);
  const int d = g_depth++;
  g_nested[d] = 0.f;
  CallOriginal(object, snapshot, amount, coefficient, descLo, descHi, noScaling, healed, isCrit, unknown, isLifesteal,
               canCrit, allowOverload);
  const float after = SehHp(object);
  if (before < 0.f || after < 0.f) return;  // no combatant: the core healed nothing
  const float gained = after - before;
  if (d > 0) g_nested[d - 1] += gained;  // the outer call's HP change includes this one
  st::HealEvent e;
  std::uint32_t guid = 0;
  if (!SehHealFacts(object, snapshot, &e, &guid)) return;
  e.lifesteal = isLifesteal;
  e.healed = healed ? *healed : -1.f;
  e.hpBefore = before;
  e.hpAfter = after - g_nested[d];
  if (e.healerSeat >= 0) e.source = SourceOf(e.healerSeat, guid, descLo);
  acc->OnHeal(e);
}

bool SehShieldGiver(void* shieldInstance, void* target, int* giverSeat, int* targetSeat, bool* sameTeam) {
  __try {
    const std::uint32_t caster = Field<std::uint32_t>(shieldInstance, inst::kTarget);  // a modifier's caster
    void* g = caster ? GetObjectById(caster) : nullptr;
    if (!g || !Field<unsigned char>(g, gobj::kPlayerControlled)) return false;
    const unsigned gs = Field<unsigned char>(g, gobj::kPlayerIndex);
    *giverSeat = gs < st::kSeats ? static_cast<int>(gs) : -1;
    const bool tp = Field<unsigned char>(target, gobj::kPlayerControlled) != 0;
    const unsigned ts = Field<unsigned char>(target, gobj::kPlayerIndex);
    *targetSeat = tp && ts < st::kSeats ? static_cast<int>(ts) : -1;
    *sameTeam = Field<unsigned char>(g, gobj::kTeam) == Field<unsigned char>(target, gobj::kTeam);
    return true;
  } __except (SehNote(GetExceptionInformation(), "the shield's giver")) {
    return false;
  }
}

bool SehCodeMatches(std::uintptr_t va, const unsigned char* bytes, std::size_t n) {
  __try {
    return std::memcmp(reinterpret_cast<const void*>(va), bytes, n) == 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

// The attacker's facts for the pet rule: a shaper, a player pet (NPC type 11), its owner's seat, its noun's name.
bool SehAttackerFacts(void* attacker, bool* shaper, bool* pet, int* ownerSeat, char* noun, std::size_t size) {
  __try {
    *shaper = Field<unsigned char>(attacker, gobj::kPlayerControlled) != 0;
    *pet = g_petCheck == 1 && IsPlayerPet(attacker);
    *ownerSeat = -1;
    const std::uint32_t ownerId = Field<std::uint32_t>(attacker, gobj::kOwner);
    void* o = ownerId && ownerId != 0xFFFFFFFFu ? GetObjectById(ownerId) : nullptr;
    if (o && o != attacker && Field<unsigned char>(o, gobj::kPlayerControlled)) {
      const unsigned s = Field<unsigned char>(o, gobj::kPlayerIndex);
      if (s < st::kSeats) *ownerSeat = static_cast<int>(s);
    }
    void* n = Field<void*>(attacker, gobj::kNoun);
    const char* name = n ? Field<const char*>(n, 0) : nullptr;
    std::snprintf(noun, size, "%s", name ? name : "?");
    return true;
  } __except (SehNote(GetExceptionInformation(), "the hit's attacker")) {
    return false;
  }
}

}  // namespace

void OnHitAttacker(void* attacker, int creditSeat, bool sameTeam, float effective) {
  if (!attacker || sameTeam || !Counting()) return;
  if (g_petCheck < 0) g_petCheck = SehCodeMatches(0x00880830, kIsPlayerPetBytes, sizeof kIsPlayerPetBytes) ? 1 : 0;
  bool shaper = false, pet = false;
  int ownerSeat = -1;
  char noun[96];
  if (!SehAttackerFacts(attacker, &shaper, &pet, &ownerSeat, noun, sizeof noun)) return;
  const st::AttackerCredit c = st::CreditOfAttacker(shaper, ValidSeat(creditSeat) ? creditSeat : -1, ownerSeat);
  const int k = static_cast<int>(c);
  ++g_creditHits[k];
  if (pet) ++g_creditPetHits[k];
  if (effective > 0.f) g_creditDamage[k] += effective;
  if (c == st::AttackerCredit::Shaper || c == st::AttackerCredit::NoOwner) return;
  char key[160];
  std::snprintf(key, sizeof key, "%s%s -> %s", noun, pet ? " (pet)" : "", st::AttackerCreditName(c));
  auto it = g_summons.find(key);
  if (it != g_summons.end())
    ++it->second;
  else if (static_cast<int>(g_summons.size()) < kMaxSummonNouns)
    g_summons.emplace(key, 1u);
}

void SetSim(void* sim) { g_sim = sim; }

bool InstallHealHook() {
  if (g_origHeal) return true;
  if (!Enabled()) {
    Log("match_stats (MSTATS3): heal-core detour not installed ([host] match_stats = 0)");
    return false;
  }
  g_origHeal = reinterpret_cast<HealDamageFn>(
      HookDetour(kHealDamage, kHealDamageBytes, sizeof kHealDamageBytes, reinterpret_cast<void*>(&HealHook)));
  Log("match_stats (MSTATS3): heal-core detour on GameObject_HealDamage %08X %s; damage and healing by source into "
      "seatStats.sources",
      static_cast<unsigned>(kHealDamage), g_origHeal ? "installed" : "FAILED (the prologue differs: no healing keys)");
  return g_origHeal != nullptr;
}

st::Source SourceOf(int seat, std::uint32_t creditGuid, unsigned descLo) {
  if (!ValidSeat(seat)) return st::Classify(nullptr, creditGuid, descLo);
  bool known = false;
  st::Source s = st::Classify(&g_tables[seat], creditGuid, descLo, &known);
  if (!known && creditGuid && Ours() && GetTickCount64() - g_lastBuild[seat] >= kRebuildGapMs) {
    Rebuild(g_sim, seat);
    s = st::Classify(&g_tables[seat], creditGuid, descLo, &known);
  }
  if (!known) {
    auto& u = g_unknown[seat];
    auto it = u.find(creditGuid);
    if (it != u.end())
      ++it->second.second;
    else if (static_cast<int>(u.size()) < kMaxUnknownPerSeat)
      u.emplace(creditGuid, std::make_pair(s.kind, 1u));
  }
  return s;
}

void RefreshSources(void* sim) {
  if (!sim || GetCurrentSim() != sim) return;
  for (int i = 0; i < st::kSeats; ++i) Rebuild(sim, i);
}

void OnShieldAbsorb(void* shieldInstance, void* target, float absorbed) {
  if (!(absorbed > 0.f) || !shieldInstance || !target) return;
  st::Accumulator* acc = Counting();
  if (!acc) return;
  ++g_shieldCalls;
  int giver = -1, seat = -1;
  bool same = false;
  if (SehShieldGiver(shieldInstance, target, &giver, &seat, &same)) acc->OnShieldAbsorb(giver, seat, same, absorbed);
}

void LogSources(const st::Accumulator& acc, const st::SeatFacts seats[st::kSeats]) {
  Log("match: sources (MSTATS3): heal-core calls counted %u (other simulators %u), absorbing shields %u, table "
      "rebuilds %u, exceptions %u",
      acc.Heals(), g_foreignHeals, g_shieldCalls, g_rebuilds, g_exceptions);
  // MSTATS3-live2: the pet rule (a non-shaper's hit counts for its owner when the snapshot names the owner's seat)
  std::string summons;
  for (const auto& kv : g_summons) {
    char b[200];
    std::snprintf(b, sizeof b, " %s x%u;", kv.first.c_str(), kv.second);
    summons += b;
  }
  using AC = st::AttackerCredit;
  auto n = [](AC c) { return g_creditHits[static_cast<int>(c)]; };
  auto p = [](AC c) { return g_creditPetHits[static_cast<int>(c)]; };
  auto d = [](AC c) { return g_creditDamage[static_cast<int>(c)]; };
  Log("match: attackers (MSTATS3-live2): enemy hits by shapers %u (%.0f); by other objects credited to their owner's "
      "seat %u (%.0f, pets %u), to a seat not their owner's %u (%.0f, pets %u), owned by a shaper but no seat %u (%.0f, "
      "pets %u), no owner and no seat %u (pets %u); IsPlayerPet %s; non-shaper attackers with a seat or owner:%s",
      n(AC::Shaper), d(AC::Shaper), n(AC::Owner), d(AC::Owner), p(AC::Owner), n(AC::OtherSeat), d(AC::OtherSeat),
      p(AC::OtherSeat), n(AC::Lost), d(AC::Lost), p(AC::Lost), n(AC::NoOwner), p(AC::NoOwner),
      g_petCheck == 1 ? "checked" : g_petCheck == 0 ? "PROLOGUE DIFFERS" : "unchecked",
      summons.empty() ? " none" : summons.c_str());
  for (int i = 0; i < st::kSeats; ++i) {
    if (!seats[i].present) continue;
    double damage = 0.0, heal = 0.0;
    acc.SourceSums(i, &damage, &heal);
    std::string unknown;
    for (const auto& kv : g_unknown[i]) {
      char name[96], b[160];
      SehAbilityName(kv.first, name, sizeof name);
      std::snprintf(b, sizeof b, " %08X %s -> %s x%u;", kv.first, name, kv.second.first.c_str(), kv.second.second);
      unknown += b;
    }
    const double dealt = acc.Stat(i, "damageDealt"), done = acc.Stat(i, "healingDone");
    Log("match: sources for player %d: damage %.1f of damageDealt %.1f (%s), heal %.1f of healingDone %.1f (%s); "
        "table %zu GUID(s); fell back:%s",
        i, damage, dealt, damage - dealt < 0.01 && dealt - damage < 0.01 ? "equal" : "DIFFERENT", heal, done,
        heal - done < 0.01 && done - heal < 0.01 ? "equal" : "DIFFERENT", g_tables[i].Size(),
        unknown.empty() ? " none" : unknown.c_str());
  }
}

}  // namespace matchstats
