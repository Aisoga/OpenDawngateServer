// D12c/D12e: attached trigger volumes follow their objects; see volumes.h.
#include "volumes.h"

#include <windows.h>

#include <cmath>
#include <cstdint>
#include <cstdio>
#include <vector>

#include "game.h"
#include "hook.h"
#include "log.h"

using namespace game;

namespace volumes {
namespace {

Options g_opt;
bool g_installed;
void* g_sim;
attach::CreateFn g_orig;

struct Entry {
  std::uint32_t record = 0;  // the attachment record's handle
  std::uint32_t owner = 0;   // the object's id
  std::uint32_t volume = 0;  // the volume's handle
  float last[3] = {0, 0, 0};
  bool placed = false;
};
std::vector<Entry> g_entries;

struct Stats {
  unsigned registered, moved, ended, exceptions, lastEnded;
} g_st;
unsigned g_logs;

bool LogBudget() {
  ++g_logs;
  return g_logs <= static_cast<unsigned>(g_opt.logLimit) || g_logs % 200 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  ++g_st.exceptions;
  Log("EXCEPTION in volumes (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

void* RecordPool() {
  __try {
    return ComponentPool(static_cast<char*>(g_sim) + kSimComponents, attach::kRecordPoolHash);
  } __except (SehNote(GetExceptionInformation(), "the record pool")) {
    return nullptr;
  }
}

int __cdecl CreateHook(void* object, int kind, std::uint32_t attached, const void* transform) {
  const int record = g_orig(object, kind, attached, transform);
  if (kind == 1 && record != 0 && object && g_sim && GetCurrentSim() == g_sim) {
    __try {
      Entry e;
      e.record = static_cast<std::uint32_t>(record);
      e.owner = Field<std::uint32_t>(object, gobj::kId);
      e.volume = attached;
      const float* p = reinterpret_cast<const float*>(static_cast<char*>(object) + gobj::kPosition);
      e.last[0] = p[0];
      e.last[1] = p[1];
      e.last[2] = p[2];
      e.placed = true;  // made at the owner's position
      g_entries.push_back(e);
      ++g_st.registered;
      if (LogBudget())
        Log("volumes (D12c): volume %08X attached to object %08X (record %08X) at (%.2f, %.2f, %.2f); %zu followed", attached,
            e.owner, e.record, p[0], p[1], p[2], g_entries.size());
    } __except (SehNote(GetExceptionInformation(), "register")) {
    }
  }
  return record;
}

// 1 moved, 0 nothing to do, -1 the entry ended, -2 exception
int SehStep(Entry& e, void* pool) {
  __try {
    if (!pool || !PoolTryToGet(pool, e.record)) return -1;
    char* owner = static_cast<char*>(GetObjectById(e.owner));
    if (!owner) return -1;
    if (Field<unsigned char>(owner, gobj::kPendingDelete)) return -1;
    const float* p = reinterpret_cast<const float*>(owner + gobj::kPosition);
    const float dx = p[0] - e.last[0], dy = p[1] - e.last[1], dz = p[2] - e.last[2];
    if (e.placed && dx * dx + dy * dy + dz * dz < 1e-4f) return 0;
    attach::VolumeSetPose(e.volume, p, reinterpret_cast<const float*>(owner + gobj::kOrientation));
    e.last[0] = p[0];
    e.last[1] = p[1];
    e.last[2] = p[2];
    e.placed = true;
    return 1;
  } __except (SehNote(GetExceptionInformation(), "step")) {
    return -2;
  }
}

}  // namespace

bool Install(const Options& options) {
  g_opt = options;
  if (!g_opt.follow) {
    Log("volumes (D12c): off (volume_follow 0)");
    return false;
  }
  g_orig = reinterpret_cast<attach::CreateFn>(HookDetour(attach::kCreate, attach::kCreateBytes, sizeof attach::kCreateBytes,
                                                         reinterpret_cast<void*>(&CreateHook)));
  g_installed = g_orig != nullptr;
  Log("volumes (D12c): AttachmentData_Create detour %s; attached trigger volumes follow their objects",
      g_installed ? "installed" : "FAILED");
  return g_installed;
}

bool Installed() { return g_installed; }

void SetSim(void* sim) { g_sim = sim; }

void Tick() {
  if (!g_installed || g_entries.empty() || !g_sim) return;
  void* pool = RecordPool();
  std::size_t out = 0;
  for (std::size_t i = 0; i < g_entries.size(); ++i) {
    const int r = SehStep(g_entries[i], pool);
    if (r == 1) ++g_st.moved;
    if (r < 0) {
      ++g_st.ended;
      continue;  // dropped
    }
    if (out != i) g_entries[out] = g_entries[i];
    ++out;
  }
  g_entries.resize(out);
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size, "volumes (D12c): %s, attached %u, followed now %zu, moved %u times, ended %u, exceptions %u",
                g_installed ? "following" : "off", g_st.registered, g_entries.size(), g_st.moved, g_st.ended, g_st.exceptions);
}

}  // namespace volumes
