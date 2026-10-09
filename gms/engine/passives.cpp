#include "passives.h"

#include <windows.h>

#include <cstdio>
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

// The passive's static ability among the creature's modifiers (object +0x8A8: modifier ids +0x9C..+0xA0).
bool HasInstanceOf(void* creature, void* staticAbility) {
  char* c = Field<char*>(creature, obj::kAbilityData);
  if (!c) return false;
  for (const std::uint32_t* p = Field<const std::uint32_t*>(c, container::kModifiersBegin);
       p && p != Field<const std::uint32_t*>(c, container::kModifiersEnd); ++p) {
    void* in = GetInstance(*p);
    if (in && Field<void*>(in, inst::kStatic) == staticAbility && !Field<unsigned char>(in, inst::kEndPending))
      return true;
  }
  return false;
}

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
    if (HasInstanceOf(creature, st)) {
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
    if (HasInstanceOf(creature, st)) {
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

void ActivateRole(void* sim, unsigned index, const char* why) {
  if (g_roleEnabled < 0) {
    // On by default since D13-live (docs/DAMAGE.md *The roles' passives live (D13-live)*).
    g_roleEnabled = HostOptionInt("role_passives", 1) != 0 ? 1 : 0;
    Log("passives (D13): the roles' passives (RolesTuning.role[mRole].rolePassive) at the deploy and each respawn: %s",
        g_roleEnabled ? "on" : "off ([host] role_passives=0)");
  }
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

}  // namespace

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
