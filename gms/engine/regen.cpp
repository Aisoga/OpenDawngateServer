// D1i: health and resource regeneration on the server simulator; see regen.h and docs/DAMAGE.md *D1i*.
#include "regen.h"

#include <windows.h>

#include <cstdio>
#include <cstring>
#include <unordered_map>

#include "core/damage/regen.h"
#include "game.h"
#include "log.h"
#include "match.h"

using namespace game;
namespace dmg = gms::damage;

namespace regen {
namespace {

Options g_opt;
bool g_installed;
void* g_sim;
bool g_haveLast;
unsigned long long g_lastMs;

struct Stats {
  unsigned steps, scanned, dead, healthObjects, resourceObjects, barCapped, exceptions, logs;
  double healed, restored;
} g_st;

bool LogBudget() {
  unsigned k = ++g_st.logs;
  return k <= static_cast<unsigned>(g_opt.logLimit) || k % 100 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  ++g_st.exceptions;
  Log("EXCEPTION in regen (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

bool CodeMatches(std::uintptr_t va, const unsigned char* bytes, std::size_t n) {
  __try {
    return std::memcmp(reinterpret_cast<const void*>(va), bytes, n) == 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

const char* NounName(void* o) {
  __try {
    void* n = o ? Field<void*>(o, gobj::kNoun) : nullptr;
    const char* s = n ? Field<const char*>(n, 0) : nullptr;
    if (s) {
      volatile char c = s[0];
      (void)c;
      return s;
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
  return "?";
}

// One regenerating value of one object (health or resource), for the progress lines.
struct Track {
  bool active = false;
  bool seen = false;       // regenerated in the current step
  double since = 0.0;      // server seconds when it started
  float startValue = 0.f;
  double lastLog = 0.0;
  float lastLogValue = 0.f;
  float per5 = 0.f;
  float cap = 0.f;
  float total = 0.f;       // gained since it started
};
std::unordered_map<unsigned long long, Track> g_tracks;

unsigned long long Key(unsigned id, int kind) { return (static_cast<unsigned long long>(id) << 1) | static_cast<unsigned>(kind); }

void Note(void* o, unsigned id, int kind, float before, float after, float per5, float cap, double now) {
  Track& t = g_tracks[Key(id, kind)];
  const char* what = kind == 0 ? "hp" : "resource";
  t.seen = true;
  t.per5 = per5;
  t.cap = cap;
  t.total += after - before;
  if (!t.active) {
    t.active = true;
    t.since = now;
    t.startValue = before;
    t.lastLog = now;
    t.lastLogValue = before;
    t.total = after - before;
    if (LogBudget())
      Log("regen: %08X %s: %s %.2f of %.2f regenerating at %.2f per 5 s (%.3f/s)", id, NounName(o), what, before, cap, per5,
          per5 / dmg::kRegenPeriodSeconds);
  }
  if (after >= cap) {
    if (LogBudget())
      Log("regen: %08X %s: %s full (%.2f) after %.2f s: %.2f -> %.2f, +%.2f regenerated", id, NounName(o), what, after,
          now - t.since, t.startValue, after, t.total);
    t.active = false;
    return;
  }
  if (now - t.lastLog >= g_opt.logSeconds) {
    if (LogBudget())
      Log("regen: %08X %s: %s %.2f -> %.2f in %.2f s (%.3f/s; rate %.2f per 5 s, cap %.2f)", id, NounName(o), what,
          t.lastLogValue, after, now - t.lastLog, (after - t.lastLogValue) / (now - t.lastLog), per5, cap);
    t.lastLog = now;
    t.lastLogValue = after;
  }
}

float SehAttr(void* ad, int index) {
  __try {
    return GetAttribute(ad, index, 1);
  } __except (SehNote(GetExceptionInformation(), "AttributeData_GetAttribute")) {
    return 0.f;
  }
}

// The top of the object's current health bar (the max HP without tiers).
float BarTop(void* o, float maxHp) {
  __try {
    char* cls = GetClassAttributes(o);
    const unsigned tiers = cls ? Field<unsigned>(cls, kClassTierCount) : 0u;
    if (tiers <= 1) return maxHp;
    const unsigned current = GetCurrentHealthBar(o);
    float bars[4] = {0.f, 0.f, 0.f, 0.f};
    const int n = static_cast<int>(current < 4 ? current : 4);
    for (int i = 0; i < n; ++i) bars[i] = GetHealthBarMax(o, static_cast<unsigned>(i));
    const float top = dmg::HealthBarTop(maxHp, bars, n, n);
    if (top < maxHp) ++g_st.barCapped;
    return top > 0.f ? top : maxHp;
  } __except (SehNote(GetExceptionInformation(), "health bars")) {
    return maxHp;
  }
}

int SehResourceType(void* o) {
  __try {
    return GetResourceType(o);
  } __except (SehNote(GetExceptionInformation(), "GameObject_GetResourceType")) {
    return dmg::kResourceTypeHealth;
  }
}

void StepObject(void* o, float seconds, double now) {
  char* cd = Field<char*>(o, gobj::kCombatant);
  void* ad = Field<void*>(o, gobj::kAttributes);
  if (!cd || !ad) return;
  ++g_st.scanned;
  float& hp = Field<float>(cd, combatant::kHitPoints);
  if (!(hp > 0.f)) {  // the dead regenerate nothing [assumed]
    ++g_st.dead;
    return;
  }
  const unsigned id = Field<unsigned>(o, gobj::kId);
  if (g_opt.health) {
    const float maxHp = SehAttr(ad, regenAttr::kMaxHealth);
    if (maxHp > 0.f && hp < maxHp) {
      const float per5 = SehAttr(ad, regenAttr::kTotalHealthRegen);
      if (per5 > 0.f) {
        const float cap = BarTop(o, maxHp);
        const float before = hp;
        const float after = dmg::RegenHealth(before, per5, cap, seconds);
        if (after != before) {
          hp = after;
          ++g_st.healthObjects;
          g_st.healed += after - before;
          Note(o, id, 0, before, after, per5, cap, now);
        }
      }
    }
  }
  if (g_opt.resource && SehResourceType(o) != dmg::kResourceTypeHealth) {
    const float maxRes = SehAttr(ad, regenAttr::kMaxResourcePoints);
    float& res = Field<float>(cd, combatant::kResourcePoints);
    if (maxRes > 0.f && res < maxRes) {
      const float per5 = dmg::ResourceRegenPer5(SehAttr(ad, regenAttr::kResourcePointRegen),
                                                SehAttr(ad, regenAttr::kResourcePointPercentRegen), maxRes,
                                                SehAttr(ad, regenAttr::kDisableResourcePointRegen) > 0.f);
      const float before = res;
      const float after = dmg::RegenResource(before, per5, maxRes, seconds);
      if (after != before) {
        res = after;
        ++g_st.resourceObjects;
        g_st.restored += after - before;
        Note(o, id, 1, before, after, per5, maxRes, now);
      }
    }
  }
}

void SehStepObject(void* o, float seconds, double now) {
  __try {
    StepObject(o, seconds, now);
  } __except (SehNote(GetExceptionInformation(), "object step")) {
  }
}

int SehCollect(void** out, int cap) {
  __try {
    void* om = Field<void*>(g_sim, sim::kObjectManager);
    int it = ObjMgrIterBegin(om);
    int n = 0;
    for (void* o = ObjMgrIterNext(om, &it); o && n < cap; o = ObjMgrIterNext(om, &it)) {
      if (Field<unsigned char>(o, gobj::kPendingDelete)) continue;
      if (!Field<void*>(o, gobj::kCombatant) || !Field<void*>(o, gobj::kAttributes)) continue;
      out[n++] = o;
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "object scan")) {
    return 0;
  }
}

// A value that didn't regenerate in this step (full, damaged to a stop, dead, its rate gone) ends its track.
void EndUnseen(double now) {
  for (auto it = g_tracks.begin(); it != g_tracks.end();) {
    Track& t = it->second;
    if (t.active && !t.seen) {
      if (LogBudget())
        Log("regen: %08X: %s stopped after %.2f s (+%.2f regenerated; none in this step: full (also when a script filled "
            "it), dead, rate 0 or the object gone)",
            static_cast<unsigned>(it->first >> 1), (it->first & 1) ? "resource" : "hp", now - t.since, t.total);
      t.active = false;
    }
    if (!t.active) {
      it = g_tracks.erase(it);
      continue;
    }
    t.seen = false;
    ++it;
  }
}

}  // namespace

bool Install(const Options& options) {
  g_opt = options;
  if (!g_opt.enabled) {
    Log("regen (D1i): off (regen = 0)");
    return false;
  }
  const bool ok = CodeMatches(0x0087E420, kGetCurrentHealthBarBytes, sizeof kGetCurrentHealthBarBytes) &&
                  CodeMatches(0x008DE370, kGetResourceTypeBytes, sizeof kGetResourceTypeBytes);
  if (!ok) {
    Log("regen (D1i): a client function's prologue differs (not build 854?); regeneration off");
    return false;
  }
  g_installed = true;
  Log("regen (D1i): health %d, resource %d, step %d ms, progress lines every %.1f s, log limit %d", g_opt.health,
      g_opt.resource, g_opt.stepMs, g_opt.logSeconds, g_opt.logLimit);
  return true;
}

bool Enabled() { return g_installed && g_opt.enabled; }

void SetSim(void* sim) { g_sim = sim; }

void Tick() {
  if (!Enabled() || !g_sim || GetCurrentSim() != g_sim) return;
  if (match::Ended()) return;  // the world is frozen after the end (D8-live)
  const unsigned long long now = GetTimeMs(g_sim);
  if (!g_haveLast) {
    g_haveLast = true;
    g_lastMs = now;
    return;
  }
  if (now < g_lastMs) {
    g_lastMs = now;
    return;
  }
  const unsigned long long elapsed = now - g_lastMs;
  if (elapsed < static_cast<unsigned long long>(g_opt.stepMs > 0 ? g_opt.stepMs : 1)) return;
  g_lastMs = now;
  // a long gap (a pause, a stall) counts as at most four steps
  const unsigned long long maxMs = static_cast<unsigned long long>((g_opt.stepMs > 0 ? g_opt.stepMs : 33) * 4);
  const float seconds = static_cast<float>(elapsed < maxMs ? elapsed : maxMs) / 1000.f;
  ++g_st.steps;
  static void* objects[0x400];
  const int n = SehCollect(objects, 0x400);
  const double nowS = static_cast<double>(now) / 1000.0;
  for (int i = 0; i < n; ++i) SehStepObject(objects[i], seconds, nowS);
  EndUnseen(nowS);
}

void FormatStats(char* buf, std::size_t size) {
  unsigned active = 0;
  for (const auto& kv : g_tracks)
    if (kv.second.active) ++active;
  std::snprintf(buf, size,
                "steps %u, object steps %u (dead %u), hp regenerated %u times (%.1f HP), resource %u times (%.1f), "
                "capped at a bar's top %u, regenerating now %u, exceptions %u",
                g_st.steps, g_st.scanned, g_st.dead, g_st.healthObjects, g_st.healed, g_st.resourceObjects, g_st.restored,
                g_st.barCapped, active, g_st.exceptions);
}

}  // namespace regen
