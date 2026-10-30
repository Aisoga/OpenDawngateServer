// FOGSTACKS: the attached effects an ended modifier leaves are destroyed; see effects.h.
#include "effects.h"

#include <windows.h>

#include <cstdint>
#include <cstdio>
#include <cstring>

#include "abilities.h"
#include "core/replication/effect_ledger.h"
#include "game.h"
#include "hook.h"
#include "log.h"

using namespace game;

namespace effects {
namespace {

Options g_opt;
bool g_installed;
void* g_sim;
CreateAttachedEffectFn g_create;
DestroyAttachedEffectFn g_destroy;
gms::replication::EffectLedger g_ledger;

struct Stats {
  unsigned created, attributed, unattributed, forgotten, endedOwners, destroyed, notAttached, objectGone, exceptions;
} g_st;
unsigned g_logs;

bool LogBudget() {
  ++g_logs;
  return g_logs <= static_cast<unsigned>(g_opt.logLimit) || g_logs % 200 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  ++g_st.exceptions;
  Log("EXCEPTION in effects (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

bool Ours() { return g_sim && GetCurrentSim() == g_sim; }

// A name of the game's (an asset's, a script's) copied for a log line; "?" when unreadable.
void CopyName(const char* p, char* out, std::size_t n) {
  out[0] = 0;
  __try {
    if (!p) {
      std::snprintf(out, n, "?");
      return;
    }
    std::size_t i = 0;
    for (; i + 1 < n && p[i]; ++i) out[i] = p[i];
    out[i] = 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    std::snprintf(out, n, "?");
  }
}

void SehRecord(void* object, void* info, std::uint32_t handle) {
  __try {
    ++g_st.created;
    const unsigned inst = AbilityLuaInstance();
    if (!inst) {
      ++g_st.unattributed;
      return;
    }
    void* in = GetInstance(inst);
    if (!in) {
      ++g_st.unattributed;
      return;
    }
    const int type = Field<int>(in, inst::kType);
    void* stat = Field<void*>(in, inst::kStatic);
    const char* name = stat ? Field<const char*>(stat, 0) : nullptr;
    void* asset = info ? Field<void*>(info, kServerEventInfoAsset) : nullptr;
    gms::replication::EffectEntry e;
    e.object = Field<std::uint32_t>(object, gobj::kId);
    e.handle = handle;
    e.asset = asset ? Field<const char*>(asset, 0) : nullptr;
    if (g_ledger.OnCreated(inst, type, reinterpret_cast<std::uintptr_t>(stat), name, e)) ++g_st.attributed;
  } __except (SehNote(GetExceptionInformation(), "record")) {
  }
}

std::uint32_t __cdecl CreateHook(void* object, void* info) {
  const std::uint32_t handle = g_create(object, info);
  if (handle && object && Ours()) SehRecord(object, info, handle);
  return handle;
}

void __cdecl DestroyHook(void* object, std::uint32_t handle, bool flag) {
  if (object && Ours()) {
    __try {
      if (g_ledger.OnDestroyed(Field<std::uint32_t>(object, gobj::kId), handle)) ++g_st.forgotten;
    } __except (SehNote(GetExceptionInformation(), "forget")) {
    }
  }
  g_destroy(object, handle, flag);
}

bool SehAlive(std::uint32_t id, std::uintptr_t ident) {
  __try {
    void* in = GetInstance(id);
    return in && reinterpret_cast<std::uintptr_t>(Field<void*>(in, inst::kStatic)) == ident;
  } __except (SehNote(GetExceptionInformation(), "alive")) {
    return true;  // keep it; asked again next tick
  }
}

enum class Result { kDestroyed, kNotAttached, kObjectGone, kException };

Result SehDestroy(const gms::replication::EffectEntry& e) {
  __try {
    char* obj = static_cast<char*>(GetObjectById(e.object));
    if (!obj || Field<unsigned char>(obj, gobj::kPendingDelete)) return Result::kObjectGone;
    const int idx = gms::replication::EffectSlotOf(e.handle);
    if (idx >= kNumAttachedEffects) return Result::kNotAttached;
    char* slot = obj + kAttachedEffects + idx * kAttachedEffectSize;
    const bool used = Field<void*>(slot, kAttachedEffectAsset) != nullptr;
    if (!gms::replication::EffectStillAttached(Field<std::uint32_t>(slot, kAttachedEffectHandle), used, e.handle))
      return Result::kNotAttached;
    g_destroy(obj, e.handle, false);  // the original: the ledger has already let the owner go
    return Result::kDestroyed;
  } __except (SehNote(GetExceptionInformation(), "destroy")) {
    return Result::kException;
  }
}

}  // namespace

bool Install(const Options& options) {
  g_opt = options;
  if (!g_opt.endWithModifier) {
    Log("effects (FOGSTACKS): off (modifier_effects_end 0): an ended modifier's attached effects stay in their slots");
    return false;
  }
  g_create = reinterpret_cast<CreateAttachedEffectFn>(HookDetour(
      kCreateAttachedEffect, kCreateAttachedEffectBytes, sizeof kCreateAttachedEffectBytes, reinterpret_cast<void*>(&CreateHook)));
  g_destroy = reinterpret_cast<DestroyAttachedEffectFn>(HookDetour(kDestroyAttachedEffect, kDestroyAttachedEffectBytes,
                                                                   sizeof kDestroyAttachedEffectBytes,
                                                                   reinterpret_cast<void*>(&DestroyHook)));
  g_installed = g_create && g_destroy;
  Log("effects (FOGSTACKS): GameObject_CreateAttachedEffect detour %s, GameObject_DestroyAttachedEffect detour %s; an "
      "ended modifier's attached effects are destroyed",
      g_create ? "installed" : "FAILED", g_destroy ? "installed" : "FAILED");
  return g_installed;
}

bool Installed() { return g_installed; }

void SetSim(void* sim) {
  g_sim = sim;
  g_ledger.Clear();
}

void AfterAbilities() {
  if (!g_installed || !g_ledger.Owners() || !Ours()) return;
  const auto ended = g_ledger.TakeEnded([](std::uint32_t id, std::uintptr_t ident) { return SehAlive(id, ident); });
  for (const auto& o : ended) {
    ++g_st.endedOwners;
    for (const auto& e : o.effects) {
      const Result r = SehDestroy(e);
      const char* what = "destroyed";
      switch (r) {
        case Result::kDestroyed: ++g_st.destroyed; break;
        case Result::kNotAttached: ++g_st.notAttached; what = "no longer in its slot (left alone)"; break;
        case Result::kObjectGone: ++g_st.objectGone; what = "on an object that is gone (left alone)"; break;
        case Result::kException: what = "not destroyed (exception)"; break;
      }
      if (!LogBudget()) continue;
      char script[96], asset[96];
      CopyName(o.name, script, sizeof script);
      CopyName(e.asset, asset, sizeof asset);
      Log("effects (FOGSTACKS): modifier %s inst %08X ended, its effect %s (handle %08X, slot %d) on %08X %s; %zu "
          "instance(s) with %zu effect(s) recorded",
          script, o.instance, asset, e.handle, gms::replication::EffectSlotOf(e.handle), e.object, what, g_ledger.Owners(),
          g_ledger.Effects());
    }
  }
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size,
                "effects (FOGSTACKS): %s, attached effects made %u (by a script's instance %u, outside one %u), "
                "destroyed by their scripts %u, ended modifiers with effects left %u: destroyed %u, no longer in the "
                "slot %u, object gone %u; recorded now %zu instance(s), %zu effect(s); exceptions %u",
                g_installed ? "on" : "off", g_st.created, g_st.attributed, g_st.unattributed, g_st.forgotten,
                g_st.endedOwners, g_st.destroyed, g_st.notAttached, g_st.objectGone, g_ledger.Owners(), g_ledger.Effects(),
                g_st.exceptions);
}

}  // namespace effects
