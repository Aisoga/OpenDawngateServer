// MAPLUA: the host's fix-ups of MM_Map_v3's own scripts (mapfix.h).
#include "mapfix.h"

#include <windows.h>

#include <cstdio>
#include <cstring>
#include <fstream>
#include <iterator>
#include <string>

#include "core/matchflow/mapfix.h"
#include "game.h"
#include "host/host.h"
#include "log.h"

using namespace game;
namespace mf = gms::mapfix;

namespace mapfix {
namespace {

struct Stats {
  unsigned runs = 0, exceptions = 0, regenEnded = 0, regenStarted = 0, regenFailed = 0;
} g_st;
bool g_loaded = false;   // the chunk ran (dgsim_mapfix.Apply exists)
bool g_pending = false;  // a table was missing at the last Apply
int g_retries = 0;
DWORD g_lastRetryMs = 0;
std::string g_state;

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  ++g_st.exceptions;
  Log("EXCEPTION in mapfix (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

// A Lua chunk on the server's lua_State under lua_pcall; its string result (or the error) into `out`.
bool SehRunLua(void* sim, const char* chunk, unsigned size, const char* name, char* out, unsigned cap) {
  __try {
    out[0] = 0;
    void* L = Field<void*>(static_cast<char*>(sim) + sim::kLua, luasim::kLuaState);
    const int top = LuaGetTop(L);
    int rc = LuaLoadBuffer(L, chunk, size, name);
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

const char* SehLevelName(void* sim) {
  __try {
    void* level = Field<void*>(sim, sim::kLevel);
    const char* s = level ? Field<const char*>(level, 0) : nullptr;
    return s ? s : "?";
  } __except (SehNote(GetExceptionInformation(), "level name")) {
    return "?";
  }
}

void Remember(bool ok, const char* out) {
  ++g_st.runs;
  g_state = out;
  g_pending = ok && std::strstr(out, "(pending ") != nullptr;
}

void LoadChunk(void* sim, bool wanted) {
  char path[MAX_PATH];
  GetModuleFileNameA(nullptr, path, MAX_PATH);
  if (char* slash = std::strrchr(path, '\\')) *slash = 0;
  std::strncat(path, "\\dgsim_mapfix.lua", sizeof path - std::strlen(path) - 1);
  std::ifstream in(path, std::ios::binary);
  if (!in) {
    Log("mapfix: %s not found (setup_server_copy.py installs gms/engine/lua/*.lua); MM_Map_v3's scripts stay as "
        "they are",
        path);
    return;
  }
  const std::string chunk((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
  char out[1024];
  const std::string pre = mf::PreChunk(wanted);
  SehRunLua(sim, pre.c_str(), static_cast<unsigned>(pre.size()), "dgsim_mapfix_pre", out, sizeof out);
  const bool ok =
      SehRunLua(sim, chunk.c_str(), static_cast<unsigned>(chunk.size()), "dgsim_mapfix.lua", out, sizeof out);
  g_loaded = ok;
  Remember(ok, out);
  Log("mapfix: dgsim_mapfix.lua (%zu bytes) on the server's lua_State: %s %s", chunk.size(), ok ? "ok" : "FAILED",
      out);
}

// The live (not ending) instance of `guid` among the object's modifiers, 0 if none.
std::uint32_t LiveModifier(void* object, std::uint32_t guid) {
  void* st = FindStaticAbility(guid);
  char* c = st ? Field<char*>(object, obj::kAbilityData) : nullptr;
  if (!c) return 0;
  for (const std::uint32_t* p = Field<const std::uint32_t*>(c, container::kModifiersBegin);
       p && p != Field<const std::uint32_t*>(c, container::kModifiersEnd); ++p) {
    void* in = GetInstance(*p);
    if (in && Field<void*>(in, inst::kStatic) == st && !Field<unsigned char>(in, inst::kEndPending)) return *p;
  }
  return 0;
}

struct Zone {
  std::uint32_t id = 0, ended = 0, started = 0;
  int team = 0;
  float x = 0.f, y = 0.f;
  const char* noun = "?";
  bool hadMm = false;
};

// Team `team`'s base object: SM's regeneration off, MM's on. 1 done, 0 no base object, -1 refused, -2 exception.
int SehSwapRegen(void* sim, int team, std::uint32_t smGuid, std::uint32_t mmGuid, Zone* z) {
  __try {
    void* base = Field<void*>(sim, kSimBaseObjects + static_cast<unsigned>(team) * 4u);
    if (!base) return 0;
    z->id = Field<std::uint32_t>(base, gobj::kId);
    z->team = Field<unsigned char>(base, gobj::kTeam);
    z->x = Field<float>(base, gobj::kPosition);
    z->y = Field<float>(base, gobj::kPosition + 4);
    void* noun = Field<void*>(base, gobj::kNoun);
    const char* nm = noun ? Field<const char*>(noun, 0) : nullptr;
    z->noun = nm ? nm : "?";
    if (!FindStaticAbility(mmGuid)) return -1;
    if (LiveModifier(base, mmGuid)) {
      z->hadMm = true;
      return 1;
    }
    // as an AI definition's passive (AgentBrain_Init's call): the object itself as caster, no snapshot, rank 1; SM's
    // ends only once MM's runs, so a refusal leaves the zone as it was
    z->started = ActivateFromDefinition(mmGuid, z->id, z->id, 0, 1, 0.f, 0.f, 0.f, 0);
    if (!z->started) return -1;
    z->ended = LiveModifier(base, smGuid);
    if (z->ended) EndAbility(z->ended);  // its Deactivate removes the 40-unit trigger volume
    return 1;
  } __except (SehNote(GetExceptionInformation(), "MM base regen")) {
    return -2;
  }
}

void MmBaseRegen(void* sim) {
  const int option = HostOptionInt("mm_base_regen", 0);
  const char* level = SehLevelName(sim);
  const bool mm = mf::IsMmLevel(level);
  if (mf::ChooseBaseRegen(option, mm) != mf::BaseRegen::kMm) {
    Log("mapfix: base regeneration as placed on %s (%s)", level,
        mm ? "[host] mm_base_regen 0: SM_Map_v5's modifier_base_regen on MM's swapped base zones"
           : "not MM_Map_v3");
    return;
  }
  const std::uint32_t sm = mf::ModifierGuid("modifier_base_regen"), mmg = mf::ModifierGuid("modifier_MM_base_regen");
  for (int team = 1; team <= 2; ++team) {
    Zone z;
    const int rc = SehSwapRegen(sim, team, sm, mmg, &z);
    if (rc == 1 && !z.hadMm) {
      ++g_st.regenStarted;
      if (z.ended) ++g_st.regenEnded;
    } else if (rc < 0) {
      ++g_st.regenFailed;
    }
    if (rc == 0) {
      Log("mapfix: MM base regen: team %d has no base object; nothing changed", team);
      continue;
    }
    char ended[48], started[80];
    if (z.ended) std::snprintf(ended, sizeof ended, "inst %08X ended", z.ended);
    else std::snprintf(ended, sizeof ended, "%s", z.hadMm ? "left as it is" : "none to end");
    if (rc == 1 && z.hadMm) std::snprintf(started, sizeof started, "already on");
    else if (rc == 1) std::snprintf(started, sizeof started, "inst %08X started", z.started);
    else if (rc == -1) std::snprintf(started, sizeof started, "REFUSED (no static ability, or ActivateFromDefinition 0)");
    else std::snprintf(started, sizeof started, "EXCEPTION");
    Log("mapfix: MM base regen: team %d's base object %08X %s (team %d) at (%.1f, %.1f): modifier_base_regen %s; "
        "modifier_MM_base_regen %s",
        team, z.id, z.noun, z.team, z.x, z.y, ended, started);
  }
}

}  // namespace

void OnLevelStarted(void* sim) {
  g_loaded = false;
  g_pending = false;
  g_retries = 0;
  const bool wanted = HostOptionInt("map_script_fixups", 1) != 0;
  LoadChunk(sim, wanted);
  MmBaseRegen(sim);
}

void Tick(void* sim) {
  if (!g_loaded || !g_pending || g_retries >= 60) return;
  const DWORD now = GetTickCount();
  if (g_retries > 0 && now - g_lastRetryMs < 1000) return;
  g_lastRetryMs = now;
  ++g_retries;
  static const char kApply[] = "return dgsim_mapfix.Apply()";
  char out[1024];
  const bool ok = SehRunLua(sim, kApply, sizeof kApply - 1, "dgsim_mapfix_apply", out, sizeof out);
  Remember(ok, out);
  if (!g_pending || g_retries >= 60 || !ok)
    Log("mapfix: dgsim_mapfix.Apply() (try %d): %s %s", g_retries, ok ? "ok" : "FAILED", out);
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size, "mapfix: runs %u%s, MM base regen started %u (SM's ended %u, failed %u), exceptions %u",
                g_st.runs, g_pending ? " (pending)" : "", g_st.regenStarted, g_st.regenEnded, g_st.regenFailed,
                g_st.exceptions);
}

}  // namespace mapfix
