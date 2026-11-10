#include "passives.h"

#include <windows.h>

#include <cstdio>
#include <cstdlib>
#include <cstring>

#include "game.h"
#include "host/host.h"
#include "log.h"

using namespace game;

namespace passives {
namespace {

struct Stats {
  unsigned activated = 0, already = 0, none = 0, failed = 0, exceptions = 0;
} g_st;
int g_enabled = -1;  // [host] shaper_passives, read at the first call

struct Result {
  std::uint32_t creature = 0, guid = 0, modifier = 0;
  const char* name = "?";
  bool had = false;
};

// 1 activated, 0 nothing to do (no player, creature or passive, or it has it), -1 refused, -2 exception.
int SehActivate(void* sim, unsigned index, Result* r) {
  __try {
    void* player = GetPlayerByIndex(sim, index);
    if (!player) return 0;
    r->creature = Field<std::uint32_t>(player, player::kCurrentCreature);
    void* creature = r->creature ? GetObjectById(r->creature) : nullptr;
    if (!creature) return 0;
    r->guid = GetAbilityInSlot(player, kSlotPassive, 0);
    void* st = r->guid ? FindStaticAbility(r->guid) : nullptr;
    if (!st) return 0;
    const char* n = *reinterpret_cast<const char**>(st);
    if (n) r->name = n;
    if (CreatureHasLiveInstance(creature, st)) {
      r->had = true;
      return 0;
    }
    r->modifier = ActivateFromDefinition(r->guid, r->creature, r->creature, 0, 1, 0.f, 0.f, 0.f, 0);
    return r->modifier ? 1 : -1;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return -2;
  }
}

// D13: the role's passive. RolesTuning (server level data +0x608) role[mRole] (0xB0 each) +0x24 rolePassive[] {ptr,
// count} of 8-byte entries {+0 FNV-1 of the lower-case name = the modifier's GUID, +4 char* name} (assetdata_types.txt
// rolePassive, StringHash delta -4). 1 activated, 0 nothing to do, -1 refused, -2 exception, -3 the hash names another
// static ability than the entry's name.
int SehActivateRole(void* sim, unsigned index, Result* r, unsigned* role) {
  __try {
    void* player = GetPlayerByIndex(sim, index);
    if (!player) return 0;
    *role = Field<unsigned char>(player, player::kRole);
    r->creature = Field<std::uint32_t>(player, player::kCurrentCreature);
    void* creature = r->creature ? GetObjectById(r->creature) : nullptr;
    if (!creature) return 0;
    void* level = Field<void*>(sim, sim::kLevel);
    char* ld = level ? Field<char*>(level, noun::kData) : nullptr;
    void* rt = ld ? Field<void*>(ld, level::kRolesTuning) : nullptr;
    char* rtd = rt ? Field<char*>(rt, noun::kData) : nullptr;
    if (!rtd) return 0;
    char* roles = Field<char*>(rtd, 0);
    const int count = Field<int>(rtd, 4);
    if (!roles || count <= 0 || count > 16 || static_cast<int>(*role) >= count) return 0;
    char* entry = roles + *role * roleTuning::kSize;
    char* passives = Field<char*>(entry, roleTuning::kRolePassives);
    if (!passives || Field<int>(entry, roleTuning::kRolePassives + 4) <= 0) return 0;
    r->guid = Field<std::uint32_t>(passives, 0);
    const char* wanted = Field<const char*>(passives, 4);
    void* st = r->guid ? FindStaticAbility(r->guid) : nullptr;
    if (!st) return 0;
    const char* n = *reinterpret_cast<const char**>(st);
    if (n) r->name = n;
    if (wanted && n && _stricmp(wanted, n) != 0) return -3;
    if (CreatureHasLiveInstance(creature, st)) {
      r->had = true;
      return 0;
    }
    r->modifier = ActivateFromDefinition(r->guid, r->creature, r->creature, 0, 1, 0.f, 0.f, 0.f, 0);
    return r->modifier ? 1 : -1;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return -2;
  }
}

struct RoleStats {
  unsigned activated = 0, already = 0, none = 0, failed = 0;
} g_role;
int g_roleEnabled = -1;  // [host] role_passives, read at the first call

// VIMPOPUP test aid ([host] test_roles = "<player>:<role>[,<player>:<role>...]", roles 0-3: Gladiator, Tactician,
// Hunter, Predator): the server player's mRole written before its role passive at the deploy and each respawn, so a
// session without shaper select can make the human (player 0, role 0 there) a Tactician or a Predator for the role
// gold's rules. Only the server's player changes (the client keeps its role card).
int g_testRole[16];
bool g_testRolesRead = false;

void ReadTestRoles() {
  g_testRolesRead = true;
  for (int& r : g_testRole) r = -1;
  char spec[128] = {};
  HostOptionString("test_roles", "", spec, sizeof spec);
  for (const char* p = spec; *p;) {
    char* end = nullptr;
    const long idx = std::strtol(p, &end, 10);
    if (end == p || *end != ':') {
      ++p;
      continue;
    }
    const char* q = end + 1;
    const long role = std::strtol(q, &end, 10);
    if (end == q) {
      p = q;
      continue;
    }
    if (idx >= 0 && idx < 16 && role >= 0 && role <= 3) {
      g_testRole[idx] = static_cast<int>(role);
      Log("passives: test_roles: player %ld's mRole -> %ld at the deploy and each respawn", idx, role);
    }
    p = end;
  }
}

bool SehWriteRole(void* sim, unsigned index, unsigned char role) {
  __try {
    void* player = GetPlayerByIndex(sim, index);
    if (!player) return false;
    Field<unsigned char>(player, player::kRole) = role;
    return true;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

void ActivateRole(void* sim, unsigned index, const char* why) {
  if (g_roleEnabled < 0) {
    // On by default since D13-live (docs/DAMAGE.md *The roles' passives live (D13-live)*).
    g_roleEnabled = HostOptionInt("role_passives", 1) != 0 ? 1 : 0;
    Log("passives (D13): the roles' passives (RolesTuning.role[mRole].rolePassive) at the deploy and each respawn: %s",
        g_roleEnabled ? "on" : "off ([host] role_passives=0)");
  }
  if (!g_testRolesRead) ReadTestRoles();
  if (sim && index < 16 && g_testRole[index] >= 0)
    SehWriteRole(sim, index, static_cast<unsigned char>(g_testRole[index]));
  if (!g_roleEnabled || !sim) return;
  Result r;
  unsigned role = 0xFF;
  const int rc = SehActivateRole(sim, index, &r, &role);
  if (rc == 1) ++g_role.activated;
  else if (rc == 0 && r.had) ++g_role.already;
  else if (rc == 0) ++g_role.none;
  else ++g_role.failed;
  Log("passives: player %u's creature %08X at the %s: role %u passive %s %08X -> %s (modifier %08X)", index, r.creature,
      why, role, r.name, r.guid,
      rc == 1    ? "activated"
      : rc == 0  ? (r.had ? "already active" : "no role passive")
      : rc == -1 ? "REFUSED"
      : rc == -3 ? "NAME MISMATCH (not activated)"
                 : "EXCEPTION",
      r.modifier);
}

// The modifiers a passive grants once, from its own Tick, that end at the death while the passive itself survives it
// (deactivationType OnAgentDestroyed), so no respawn restarts that Tick: Sakari's modifier_SnowQueen_Passive (93480BB0)
// requests modifier_SnowQueen_W_Feedback (F69737A6, OnAgentDeath: the client's W ghost, its snap to the live icebergs and
// the cast position) once, and OnRevive renews only the crystals and the ammo, so after her first death the ghost and the
// snapping were gone for the match (the host log: "modifier ... W_Feedback ended" at the death, then "passive already
// active" at the respawn). The host renews them at a respawn, as the Tick's request would.
struct Renewal {
  std::uint32_t passive, modifier;
};
const Renewal kRenewals[] = {{0x93480BB0u, 0xF69737A6u}};

// 1 renewed, 0 nothing to do (no entry, or it is active), -1 refused, -2 exception.
int SehRenew(void* sim, unsigned index, std::uint32_t passive, std::uint32_t* creatureId, std::uint32_t* modifier) {
  __try {
    int rc = 0;
    for (const Renewal& e : kRenewals) {
      if (e.passive != passive) continue;
      void* player = GetPlayerByIndex(sim, index);
      if (!player) return 0;
      *creatureId = Field<std::uint32_t>(player, player::kCurrentCreature);
      void* creature = *creatureId ? GetObjectById(*creatureId) : nullptr;
      void* st = FindStaticAbility(e.modifier);
      if (!creature || !st || CreatureHasLiveInstance(creature, st)) return 0;
      *modifier = ActivateFromDefinition(e.modifier, *creatureId, *creatureId, 0, 1, 0.f, 0.f, 0.f, 0);
      rc = *modifier ? 1 : -1;
    }
    return rc;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return -2;
  }
}

// SAKFROST (2026-11-10): the passives that live through the death and whose Tick puts itself to sleep while the agent is
// dead (nThread.Sleep: the thread's sleeping byte), with an OnRevive that doesn't wake it, so only a later event's
// nThread.WakeUp resumes it. Sakari's modifier_SnowQueen_Passive (93480BB0): its Tick is the frost regeneration (6.5 s
// after the last Q/W/E/R, +1 per 0.25 s up to 10) and sleeps while IsAlive is false; only OnAbilityUsed (Q/W/E/R) wakes
// it, so after a respawn the frost stayed at its value at the death (plus what the base's modifier_base_regen gave while
// she stood in it) until her first cast (Strato match 21, 2026-11-10: 2 of 10 from 602 s to her cast at 622 s). The host
// wakes the Tick at the respawn, as nThread.WakeUp (0x009253D0) does: the regeneration then runs at once (the last cast
// was at least the death timer ago). Kensu's modifier_Savage_Passive (B499A7D4): its Tick is the arrow reload (one
// modifier_Savage_Basic_AmmoCounter stack per 1 / BasicAttacksPerSecond up to 2), asleep once full or while he is dead,
// woken only by his basic attack going on cooldown (OnAbilityOnCooldown); the counter ends at the death (OnAgentDeath), so
// he came back with no arrows and his first shot after the respawn was a single one on the basic's full 1.4 s cooldown.
// Woken at the respawn, the Tick reloads both arrows at once (its reloadTimerStarted keeps a shot's wake from starting a
// second reload). [host] respawn_wake (1).
const std::uint32_t kWakeAtRespawn[] = {0x93480BB0u, 0xB499A7D4u};
int g_wakeEnabled = -1;

void* FindLiveInstance(void* creature, void* staticAbility) {
  char* c = Field<char*>(creature, obj::kAbilityData);
  if (!c) return nullptr;
  for (const std::uint32_t* p = Field<const std::uint32_t*>(c, container::kModifiersBegin);
       p && p != Field<const std::uint32_t*>(c, container::kModifiersEnd); ++p) {
    void* in = GetInstance(*p);
    if (in && Field<void*>(in, inst::kStatic) == staticAbility && !Field<unsigned char>(in, inst::kEndPending))
      return in;
  }
  return nullptr;
}

// 1 woken, 2 it was awake (nothing done), 0 not on the list (or no creature, instance or thread), -2 exception.
bool WakesAtRespawn(std::uint32_t passive) {
  for (std::uint32_t g : kWakeAtRespawn)
    if (g == passive) return true;
  return false;
}

int SehWake(void* sim, unsigned index, std::uint32_t passive, std::uint32_t* creatureId, std::uint32_t* instId,
            std::uint32_t* tid) {
  __try {
    if (!WakesAtRespawn(passive)) return 0;
    void* player = GetPlayerByIndex(sim, index);
    if (!player) return 0;
    *creatureId = Field<std::uint32_t>(player, player::kCurrentCreature);
    void* creature = *creatureId ? GetObjectById(*creatureId) : nullptr;
    void* st = FindStaticAbility(passive);
    void* in = creature && st ? FindLiveInstance(creature, st) : nullptr;
    if (!in) return 0;
    *instId = Field<std::uint32_t>(in, inst::kId);
    *tid = Field<std::uint32_t>(in, inst::kThreadId);
    void* th = *tid ? GetThread(static_cast<char*>(sim) + sim::kLua, *tid) : nullptr;
    if (!th) return 0;
    unsigned char& sleeping = *reinterpret_cast<unsigned char*>(static_cast<char*>(th) + thread::kSleeping);
    if (!sleeping) return 2;
    sleeping = 0;
    return 1;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return -2;
  }
}

}  // namespace

bool CreatureHasLiveInstance(void* creature, void* staticAbility) {
  return FindLiveInstance(creature, staticAbility) != nullptr;
}

std::uint32_t Activate(void* sim, unsigned index, const char* why) {
  ActivateRole(sim, index, why);  // D13 ([host] role_passives, on by default since D13-live)
  if (g_enabled < 0) {
    g_enabled = HostOptionInt("shaper_passives", 1) != 0 ? 1 : 0;
    Log("passives (D6c): the shapers' passives (PlayerClass.passiveAbility) at the deploy and each respawn: %s",
        g_enabled ? "on" : "off ([host] shaper_passives=0)");
  }
  if (!g_enabled || !sim) return 0;
  Result r;
  const int rc = SehActivate(sim, index, &r);
  if (rc == 1) ++g_st.activated;
  else if (rc == 0 && r.had) ++g_st.already;
  else if (rc == 0) ++g_st.none;
  else if (rc == -1) ++g_st.failed;
  else ++g_st.exceptions;
  Log("passives: player %u's creature %08X at the %s: %s %08X -> %s (modifier %08X)", index, r.creature, why, r.name,
      r.guid,
      rc == 1 ? "activated" : rc == 0 ? (r.had ? "already active" : "no passive") : rc == -1 ? "REFUSED" : "EXCEPTION",
      r.modifier);
  if (rc == 0 && r.had && std::strcmp(why, "respawn") == 0) {
    std::uint32_t creatureId = 0, mod = 0;
    const int rr = SehRenew(sim, index, r.guid, &creatureId, &mod);
    if (rr != 0)
      Log("passives: player %u's creature %08X at the respawn: %s's companion modifier renewed -> %s (modifier %08X)",
          index, creatureId, r.name, rr == 1 ? "activated" : rr == -1 ? "REFUSED" : "EXCEPTION", mod);
    if (g_wakeEnabled < 0) g_wakeEnabled = HostOptionInt("respawn_wake", 1) != 0 ? 1 : 0;
    std::uint32_t instId = 0, tid = 0;
    const int rw = g_wakeEnabled ? SehWake(sim, index, r.guid, &creatureId, &instId, &tid) : 0;
    if (rw != 0)
      Log("passives: player %u's creature %08X at the respawn: %s's Tick (inst %08X, thread %08X) %s", index, r.creature,
          r.name, instId, tid,
          rw == 1 ? "was asleep -> woken (SAKFROST)" : rw == 2 ? "was awake" : "EXCEPTION");
    else if (!g_wakeEnabled && WakesAtRespawn(r.guid))
      Log("passives: player %u's creature %08X at the respawn: %s's Tick left asleep ([host] respawn_wake=0)", index,
          r.creature, r.name);
  }
  return rc == 1 ? r.modifier : 0;
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size,
                "passives activated %u (already active %u, none %u, refused %u, exceptions %u); role passives activated "
                "%u (already %u, none %u, failed %u)",
                g_st.activated, g_st.already, g_st.none, g_st.failed, g_st.exceptions, g_role.activated, g_role.already,
                g_role.none, g_role.failed);
}

}  // namespace passives
