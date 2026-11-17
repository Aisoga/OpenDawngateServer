// MAPRULES3: Bindings respawn on the host (bindrespawn.h; the rules in gms/core/matchflow/mapbase.h).
#include "bindrespawn.h"

#include <windows.h>

#include <cstdio>
#include <cstring>
#include <string>

#include "director.h"
#include "game.h"
#include "host/host.h"
#include "log.h"

using namespace game;
namespace flow = gms::flow;

namespace bindrespawn {
namespace {

struct Options {
  int overrideS = -1;   // [host] tower_respawn_s: -1 the level's; >= 0 that many seconds (at least 5)
  bool passive = true;  // [host] tower_respawn_passive: request modifier_Binding_Passive if the resurrect left none
};
Options g_opt;

bool g_enabled = false;  // the level's towerRespawnEnabled
std::vector<flow::TowerTuning> g_list;
flow::BindingRespawns g_r;

// What the host knows of a slot's Binding between its death and its return.
struct Down {
  std::uint32_t id = 0;
  std::string noun;
  int team = 0, lane = -1, tier = -1, entry = -1;
  double diedAt = 0.0, seconds = -1.0;
};
Down g_down[flow::BindingRespawns::kSlots];

// The passive check a little after a resurrect (GameObject_OnRevived activates the AI passive; checked once the
// scheduler has run).
struct Check {
  std::uint32_t id = 0;
  std::string noun;
  double at = 0.0;
};
std::vector<Check> g_checks;

struct Stats {
  unsigned destroyed = 0, scheduled = 0, respawned = 0, gone = 0, passiveBack = 0, passiveRequested = 0,
           passiveMissing = 0, handlerCalls = 0, exceptions = 0;
} g_st;

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  ++g_st.exceptions;
  Log("EXCEPTION in bindrespawn (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

unsigned long long SehClockMs(void* sim) {
  __try {
    return GetTimeMs(sim);
  } __except (SehNote(GetExceptionInformation(), "GetTimeMs")) {
    return 0;
  }
}

bool SehWriteSlot(void* sim, int slot, unsigned long long value) {
  __try {
    char* ui = static_cast<char*>(sim) + matchsim::kUIData;
    Field<unsigned long long>(ui, ui::kTowerRespawnTime + 8 * slot) = value;
    return true;
  } __except (SehNote(GetExceptionInformation(), "WriteSlot")) {
    return false;
  }
}

// GameObject_Resurrect (0x008E05C0) on the Binding; false when the object is gone.
bool SehResurrect(std::uint32_t id) {
  __try {
    void* o = id ? GetObjectById(id) : nullptr;
    if (!o) return false;
    Resurrect(o);
    return true;
  } __except (SehNote(GetExceptionInformation(), "Resurrect")) {
    return false;
  }
}

// A Lua chunk on the server's lua_State under lua_pcall (as match.cpp's); its string result (or the error) in `out`.
bool SehRunLua(void* sim, const char* chunk, const char* name, char* out, unsigned cap) {
  __try {
    out[0] = 0;
    void* L = Field<void*>(static_cast<char*>(sim) + sim::kLua, luasim::kLuaState);
    const int top = LuaGetTop(L);
    int rc = LuaLoadBuffer(L, chunk, static_cast<unsigned>(std::strlen(chunk)), name);
    if (rc == 0) rc = LuaPcall(L, 0, 1, 0);
    const char* s = LuaToString(L, -1, nullptr);
    if (s) {
      unsigned k = 0;
      for (; s[k] && k + 1 < cap; ++k) out[k] = s[k];
      out[k] = 0;
    }
    LuaSetTop(L, top);
    return rc == 0;
  } __except (SehNote(GetExceptionInformation(), "RunLua")) {
    return false;
  }
}

// The Binding's passive back after the resurrect? If not (and [host] tower_respawn_passive), request it as the
// scripts request a modifier on an object (its own snapshot, rank 1).
void CheckPassive(void* sim, const Check& c) {
  char chunk[1024], out[256];
  std::snprintf(chunk, sizeof chunk,
                "local b = nUtil.ToGUID('0x%08X') "
                "local g = nUtil.SPID('modifier_Binding_Passive') "
                "local m = nModifier.GetFirstModifierByGUID(b, g) "
                "local had = m ~= nil and m ~= kObjIDNone "
                "local req = '' "
                "if not had and %d == 1 then "
                "  local ok, r = pcall(function() return nModifier.RequestModifier(b, b, g, "
                "nAttribute.CreateAttributeSnapshot_FromObject(b, kObjIDNone), 1, kZeroVector, nil) end) "
                "  req = ok and ('; requested: ' .. tostring(r)) or ('; request failed: ' .. tostring(r)) "
                "end "
                "return (had and ('back ' .. tostring(m)) or 'absent') .. req .. '; alive ' .. "
                "tostring(nGameObject.IsAlive(b)) .. ', hp ' .. string.format('%%.0f/%%.0f', nGameObject.GetHitPoints(b), "
                "nGameObject.GetMaxHitPoints(b))",
                c.id, g_opt.passive ? 1 : 0);
  const bool ok = SehRunLua(sim, chunk, "dgsim Binding passive", out, sizeof out);
  if (ok && std::strncmp(out, "back", 4) == 0)
    ++g_st.passiveBack;
  else if (ok && std::strstr(out, "requested"))
    ++g_st.passiveRequested;
  else
    ++g_st.passiveMissing;
  Log("match: Binding %08X %s: modifier_Binding_Passive after the resurrect: %s%s", c.id, c.noun.c_str(),
      ok ? "" : "FAILED ", out);
}

bool Respawn(void* sim, int slot, double t) {
  Down& d = g_down[slot];
  if (!SehResurrect(d.id)) {
    ++g_st.gone;
    SehWriteSlot(sim, slot, flow::kTowerSlotGone);
    Log("match: Binding %08X %s (team %d, lane %d, tier %d) due back at match %.2f s, but the object is gone: it stays "
        "down",
        d.id, d.noun.c_str(), d.team, d.lane, d.tier, t);
    return false;
  }
  ++g_st.respawned;
  SehWriteSlot(sim, slot, 0);  // standing, as the level load writes it
  Log("match: Binding %08X %s (team %d, lane %d, tier %d) respawned after %.0f s (died at match %.2f s, back at "
      "%.2f s; tower[%d]): GameObject_Resurrect, mTowerRespawnTime 0",
      d.id, d.noun.c_str(), d.team, d.lane, d.tier, t - d.diedAt, d.diedAt, t, d.entry);
  Check c;
  c.id = d.id;
  c.noun = d.noun;
  c.at = t + 0.5;
  g_checks.push_back(c);
  // nGameModeHandlers[OnTowerRespawned](tower, team, towerIndex, laneIndex, numTowersPerLane) (R10 §1: resets the
  // other team's v4 spawner of a four-tower lane; nothing on a three-tier level)
  char chunk[512], out[256];
  std::snprintf(chunk, sizeof chunk,
                "local h = nGameModeHandlers and nGameModeFns and nGameModeHandlers[nGameModeFns.OnTowerRespawned] "
                "if not h then return 'no handler' end "
                "local ok, err = pcall(h, nUtil.ToGUID('0x%08X'), %d, %d, %d, 3) "
                "return ok and 'ok' or ('error: ' .. tostring(err))",
                d.id, d.team, d.tier, d.lane);
  const bool ok = SehRunLua(sim, chunk, "dgsim OnTowerRespawned", out, sizeof out);
  ++g_st.handlerCalls;
  Log("match: nGameModeHandlers[OnTowerRespawned](%08X, team %d, index %d, lane %d, 3): %s %s", d.id, d.team, d.tier,
      d.lane, ok ? "" : "FAILED", out);
  if (d.entry >= 0 && d.entry < static_cast<int>(g_list.size()) && g_list[d.entry].laneAgentAdjust != 0) {
    char why[96];
    std::snprintf(why, sizeof why, "Binding %08X back", d.id);
    director::AdjustLaneAgents(d.team, g_list[d.entry].laneAgentAdjust, why);
  }
  return true;
}

}  // namespace

void OnLevel(bool enabled, const flow::TowerTuning* list, int n) {
  g_opt.overrideS = HostOptionInt("tower_respawn_s", -1);
  g_opt.passive = HostOptionInt("tower_respawn_passive", 1) != 0;
  g_enabled = enabled && n > 0;
  g_list.clear();
  for (int i = 0; list && i < n; ++i) g_list.push_back(list[i]);
  g_r = flow::BindingRespawns();
  g_checks.clear();
  for (Down& d : g_down) d = Down();
  if (g_opt.overrideS >= 0)
    Log("match: MAPRULES3: test aid [host] tower_respawn_s %d: a destroyed Binding is back after %.0f s%s",
        g_opt.overrideS, flow::EffectiveBindingRespawn(-1.0, g_opt.overrideS),
        enabled ? "" : " (the level's towerRespawnEnabled is 0: the client draws it destroyed until then)");
  else if (g_enabled)
    Log("match: MAPRULES3: Bindings respawn (towerRespawnEnabled, tower[] %d): the host resurrects a destroyed Binding "
        "at its due time (at least %.0f s; [host] tower_respawn_passive %d)",
        n, flow::kMinBindingRespawnSeconds, g_opt.passive ? 1 : 0);
}

bool Active() { return g_enabled || g_opt.overrideS >= 0; }

unsigned long long OnDestroyed(void* sim, std::uint32_t id, const char* noun, int team, int teamIndex, int lane,
                               int tier, unsigned tierMask, double matchSeconds) {
  ++g_st.destroyed;
  const int slot = flow::TowerSlot(teamIndex, lane, tier);
  if (!Active() || slot < 0) return flow::kTowerSlotGone;
  const int entry = flow::TowerTuningIndex(tierMask, tier);
  const double level = flow::BindingRespawnSeconds(g_enabled, g_list, entry);
  const double seconds = flow::EffectiveBindingRespawn(level, g_opt.overrideS);
  const unsigned long long now = SehClockMs(sim);
  const bool wasPending = g_r.Due(slot) != 0;
  const unsigned long long due = g_r.OnDestroyed(slot, seconds, now);
  if (due == flow::kTowerSlotGone) {
    Log("match: Binding %08X %s: no return (tower[%d] of %zu)", id, noun, entry, g_list.size());
    return due;
  }
  if (!wasPending) {
    ++g_st.scheduled;
    Down& d = g_down[slot];
    d.id = id;
    d.noun = noun ? noun : "";
    d.team = team;
    d.lane = lane;
    d.tier = tier;
    d.entry = entry;
    d.diedAt = matchSeconds;
    d.seconds = seconds;
  }
  Log("match: Binding %08X %s: back in %.0f s, at match %.2f s (server clock %llu ms; tower[%d] of %zu, death %d of "
      "this Binding)",
      id, noun, seconds, g_down[slot].diedAt + g_down[slot].seconds, due, entry, g_list.size(), g_r.Deaths(slot));
  return due;
}

std::vector<std::uint32_t> Tick(void* sim, double matchSeconds, bool ended) {
  std::vector<std::uint32_t> back;
  if (!Active() || ended) return back;
  if (!g_checks.empty()) {
    std::vector<Check> later;
    for (const Check& c : g_checks) {
      if (matchSeconds >= c.at)
        CheckPassive(sim, c);
      else
        later.push_back(c);
    }
    g_checks.swap(later);
  }
  if (!g_r.Pending()) return back;
  const std::vector<int> due = g_r.TakeDue(SehClockMs(sim));
  for (int slot : due) {
    if (Respawn(sim, slot, matchSeconds)) back.push_back(g_down[slot].id);
  }
  return back;
}

void FormatStats(char* buf, unsigned size) {
  std::snprintf(buf, size,
                "Bindings respawn %s: destroyed %u, scheduled %u, respawned %u, gone %u, pending %d; passive back %u, "
                "requested %u, missing %u; OnTowerRespawned %u; exceptions %u",
                Active() ? "on" : "off", g_st.destroyed, g_st.scheduled, g_st.respawned, g_st.gone, g_r.Pending(),
                g_st.passiveBack, g_st.passiveRequested, g_st.passiveMissing, g_st.handlerCalls, g_st.exceptions);
}

}  // namespace bindrespawn
