// D1j: the ability data containers' release and the server pools' census; see components.h.
#include "components.h"

#include <windows.h>

#include <cstdint>
#include <cstdio>
#include <cstring>

#include "abilities.h"
#include "game.h"
#include "hook.h"
#include "log.h"

using namespace game;

namespace components {
namespace {

Options g_opt;
bool g_installed;
void* g_sim;
OnObjectDestroyedFn g_orig;
unsigned g_logs;

struct Stats {
  unsigned destroyed;      // server objects destroyed
  unsigned withContainer;  // of them with a container
  unsigned released;       // containers given back to the pool
  unsigned endedBefore;    // instances ended before the original's teardown
  unsigned endedAfter;     // instances the teardown left or added, ended after it
  unsigned pending;        // of those, still alive (in use: end pending, finished by abilities.cpp's end pass)
  unsigned heapFreed;      // heap parts freed (spell-shield buffers, cooldown nodes)
  unsigned heapKept;       // heap parts counted but kept (container_free_heap 0)
  unsigned mismatches;     // the container's handle didn't lead back to it: left alone
  unsigned notBound;       // destroyed while another simulator was current: left alone
  unsigned exceptions;
  int peakLive;
} g_st;

constexpr int kMaxIds = 96;  // 32 abilities + 32 modifiers, with room

bool LogBudget() {
  ++g_logs;
  return g_logs <= static_cast<unsigned>(g_opt.logLimit) || g_logs % 100 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  ++g_st.exceptions;
  Log("EXCEPTION in components (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

double GameSeconds() {
  __try {
    return static_cast<double>(GetTimeMs(g_sim)) / 1000.0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return 0.0;
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

void* Components() { return static_cast<char*>(g_sim) + kSimComponents; }

void* SehContainerPool() {
  __try {
    return ComponentPool(Components(), kAbilityDataContainerType);
  } __except (SehNote(GetExceptionInformation(), "the container pool")) {
    return nullptr;
  }
}

int SehPoolField(void* pool, std::uintptr_t off) {
  __try {
    return pool ? Field<int>(pool, off) : -1;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return -1;
  }
}

char* SehContainerOf(void* obj) {
  __try {
    return Field<char*>(obj, obj::kAbilityData);
  } __except (SehNote(GetExceptionInformation(), "the object's container")) {
    return nullptr;
  }
}

// The container's instance ids: its abilities, then its modifiers.
int SehIds(char* c, std::uint32_t* out, int cap) {
  static const std::uintptr_t kLists[2] = {container::kAbilitiesBegin, container::kModifiersBegin};
  int n = 0;
  __try {
    for (const std::uintptr_t begin : kLists) {
      const std::uint32_t* b = Field<const std::uint32_t*>(c, begin);
      const std::uint32_t* e = Field<const std::uint32_t*>(c, begin + 4);
      for (const std::uint32_t* p = b; p && p < e && p - b < 64 && n < cap; ++p)
        if (*p) out[n++] = *p;
    }
  } __except (SehNote(GetExceptionInformation(), "the container's lists")) {
  }
  return n;
}

// Ends instance `id` if it still exists: 0 gone already, 1 ended, 2 still alive (end pending while in use).
int SehEnd(std::uint32_t id, char* name, std::size_t cap) {
  __try {
    void* in = GetInstance(id);
    if (!in) return 0;
    char* st = Field<char*>(in, inst::kStatic);
    const char* nm = st ? *reinterpret_cast<const char**>(st) : nullptr;
    std::snprintf(name, cap, "%s", nm ? nm : "?");
    AbilityTickSetHostCause("the host's destroy path (D1j)");
    EndAbility(id);
    AbilityTickSetHostCause(nullptr);
    return GetInstance(id) ? 2 : 1;
  } __except (SehNote(GetExceptionInformation(), "EndAbility")) {
    AbilityTickSetHostCause(nullptr);
    return 0;
  }
}

// Ends the instances listed in container `c`; appends their names to `names`. Returns the number ended.
int EndListed(char* c, unsigned* pending, char* names, std::size_t cap) {
  std::uint32_t ids[kMaxIds];
  const int n = SehIds(c, ids, kMaxIds);
  int ended = 0;
  for (int i = 0; i < n; ++i) {
    char name[96];
    const int r = SehEnd(ids[i], name, sizeof name);
    if (r == 0) continue;
    ++ended;
    if (r == 2) ++*pending;
    const std::size_t len = std::strlen(names);
    if (len + 2 < cap) std::snprintf(names + len, cap - len, " %s%s", name, r == 2 ? " (pending)" : "");
  }
  return ended;
}

bool Inside(const char* p, const char* c, int size) { return p >= c && p < c + size; }

// A fixed vector's buffer that grew onto the heap (begin outside the element).
int HeapVector(char* c, int size, std::uintptr_t beginOff) {
  char* b = Field<char*>(c, beginOff);
  if (!b || Inside(b, c, size)) return 0;
  if (g_opt.freeHeap) Free(b);
  return 1;
}

// A fixed hash map's nodes beyond its inline ones (outside the element). Its bucket array stays inline (the map's
// 10000.0 load factor never rehashes), so only nodes are freed; a bucket array outside the element is left alone.
int HeapNodes(char* c, int size, std::uintptr_t mapOff, std::uintptr_t nextOff) {
  char* map = c + mapOff;
  char** buckets = Field<char**>(map, container::kMapBuckets);
  const unsigned count = Field<unsigned>(map, container::kMapBucketCount);
  if (!buckets || count == 0 || count > 1024) return 0;
  int n = 0;
  for (unsigned i = 0; i < count; ++i) {
    int guard = 0;
    for (char* node = buckets[i]; node && guard < 4096; ++guard) {
      char* next = Field<char*>(node, nextOff);
      if (!Inside(node, c, size)) {
        if (g_opt.freeHeap) Free(node);
        ++n;
      }
      node = next;
    }
  }
  return n;
}

// Gives container `c` (object `obj`'s) back to its pool. Returns 1 released, 0 mismatch, -1 exception.
int SehRelease(void* obj, char* c, int* heap) {
  __try {
    void* pool = SehContainerPool();
    const std::uint32_t handle = Field<std::uint32_t>(c, container::kHandle);
    if (!pool || PoolTryToGet(pool, handle) != c) {
      ++g_st.mismatches;
      return 0;
    }
    const int size = Field<int>(pool, opool::kElementSize);
    *heap = HeapVector(c, size, container::kAbilitiesBegin) + HeapVector(c, size, container::kModifiersBegin) +
            HeapVector(c, size, container::kSpellShieldsBegin) +
            HeapNodes(c, size, container::kCooldowns, container::kCooldownNext) +
            HeapNodes(c, size, container::kCooldowns2, container::kCooldown2Next);
    Field<std::uint32_t>(c, container::kHandle) = handle;
    PoolDelete(pool, handle);
    Field<char*>(obj, obj::kAbilityData) = nullptr;
    return 1;
  } __except (SehNote(GetExceptionInformation(), "the container's release")) {
    return -1;
  }
}

void __fastcall OnObjectDestroyedHook(void* om, void* /*edx*/, void* obj) {
  const bool ours = g_installed && g_sim && obj && om == Field<void*>(g_sim, sim::kObjectManager);
  if (!ours) {
    g_orig(om, obj);
    return;
  }
  ++g_st.destroyed;
  char* c = SehContainerOf(obj);
  if (!c) {
    g_orig(om, obj);
    return;
  }
  ++g_st.withContainer;
  if (GetCurrentSim() != g_sim) {  // the pools resolve through the current simulator
    ++g_st.notBound;
    g_orig(om, obj);
    return;
  }
  // before the teardown, with the object intact: end what is still listed (the original only removes the instances
  // that hold the agent)
  char before[512] = "", after[256] = "";
  unsigned pending = 0;
  int endedBefore = 0, endedAfter = 0;
  if (g_opt.endInstances) endedBefore = EndListed(c, &pending, before, sizeof before);
  g_orig(om, obj);
  int heap = 0, released = 0;
  if (g_opt.release) {
    c = SehContainerOf(obj);  // still the same element: nothing in the teardown touches +0x8A8
    if (c) {
      if (g_opt.endInstances) endedAfter = EndListed(c, &pending, after, sizeof after);
      released = SehRelease(obj, c, &heap);
    }
  }
  g_st.endedBefore += static_cast<unsigned>(endedBefore);
  g_st.endedAfter += static_cast<unsigned>(endedAfter);
  g_st.pending += pending;
  if (released == 1) {
    ++g_st.released;
    if (g_opt.freeHeap) g_st.heapFreed += static_cast<unsigned>(heap);
    else g_st.heapKept += static_cast<unsigned>(heap);
  }
  const int live = SehPoolField(SehContainerPool(), opool::kLive);
  if (live > g_st.peakLive) g_st.peakLive = live;
  if (LogBudget() || heap > 0 || released != 1 || endedBefore > 0 || endedAfter > 0)
    Log("containers (D1j): %s destroyed (t %.2f s): ended %d before the teardown:%s; %d after it:%s; %u pending; "
        "container %s, heap parts %d %s; pool live %d",
        NounName(obj), GameSeconds(), endedBefore, before[0] ? before : " -", endedAfter, after[0] ? after : " -", pending,
        !g_opt.release      ? "kept (container_release 0)"
        : released == 1     ? "released"
        : released == 0     ? "NOT released (handle mismatch)"
                            : "NOT released (exception)",
        heap, g_opt.freeHeap ? "freed" : "kept", live);
}

}  // namespace

bool Install(const Options& options) {
  g_opt = options;
  if (!g_opt.release && !g_opt.endInstances && !g_opt.poolStats) {
    Log("containers (D1j): off (container_release 0, container_end_instances 0, pool_stats 0)");
    return false;
  }
  if (g_opt.release || g_opt.endInstances) {
    g_orig = reinterpret_cast<OnObjectDestroyedFn>(HookDetour(kOnObjectDestroyed, kOnObjectDestroyedBytes,
                                                              sizeof kOnObjectDestroyedBytes,
                                                              reinterpret_cast<void*>(&OnObjectDestroyedHook)));
    if (!g_orig) {
      Log("containers (D1j): GameObjectManager_OnObjectDestroyed detour FAILED; the containers keep leaking");
      g_opt.release = g_opt.endInstances = false;
    }
  }
  g_installed = true;
  Log("containers (D1j): OnObjectDestroyed detour %s; release %d, end instances %d, free heap parts %d, pool stats %d",
      g_orig ? "installed" : "off", g_opt.release, g_opt.endInstances, g_opt.freeHeap, g_opt.poolStats);
  return true;
}

bool Installed() { return g_installed; }

void SetSim(void* sim) { g_sim = sim; }

void FormatStats(char* buf, std::size_t size, int objectsWithContainer) {
  void* pool = g_sim ? SehContainerPool() : nullptr;
  const int live = SehPoolField(pool, opool::kLive);
  if (live > g_st.peakLive) g_st.peakLive = live;
  std::snprintf(buf, size,
                "pool live %d of %d (high water %d, peak %d), objects with one %d; destroyed %u (%u with a container), "
                "released %u, instances ended %u before the teardown + %u after (%u pending), heap parts %s %u, "
                "mismatches %u, not bound %u, exceptions %u",
                live, SehPoolField(pool, opool::kCapacity), SehPoolField(pool, opool::kHighWater), g_st.peakLive,
                objectsWithContainer, g_st.destroyed, g_st.withContainer, g_st.released, g_st.endedBefore,
                g_st.endedAfter, g_st.pending, g_opt.freeHeap ? "freed" : "kept",
                g_opt.freeHeap ? g_st.heapFreed : g_st.heapKept, g_st.mismatches, g_st.notBound, g_st.exceptions);
}

namespace {
constexpr int kMaxTypes = 64;
int g_poolPeak[kMaxTypes];

struct PoolView {
  const char* name;
  int live, highWater, capacity;
};

// The i-th registered component type's pool on the server simulator (name null when it has none).
bool SehPoolView(void* desc, PoolView* v) {
  __try {
    v->name = Field<const char*>(desc, ctype::kName);
    void* pool = ComponentPool(Components(), Field<std::uint32_t>(desc, ctype::kHash));
    if (!pool) return false;
    v->live = Field<int>(pool, opool::kLive);
    v->highWater = Field<int>(pool, opool::kHighWater);
    v->capacity = Field<int>(pool, opool::kCapacity);
    return v->name && v->capacity > 0;
  } __except (SehNote(GetExceptionInformation(), "the pool census")) {
    return false;
  }
}

void* SehNextType(void* desc) {
  __try {
    return desc ? Field<void*>(desc, ctype::kNext) : *At<void**>(kComponentTypes);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return nullptr;
  }
}
}  // namespace

void FormatPools(char* buf, std::size_t size) {
  buf[0] = 0;
  if (!g_sim || !g_opt.poolStats) return;
  int len = 0, i = 0;
  for (void* d = SehNextType(nullptr); d && i < kMaxTypes; d = SehNextType(d), ++i) {
    PoolView v{};
    if (!SehPoolView(d, &v)) continue;
    if (v.live > g_poolPeak[i]) g_poolPeak[i] = v.live;
    const char* name = v.name[0] == 'c' && v.name[1] >= 'A' && v.name[1] <= 'Z' ? v.name + 1 : v.name;
    const int n = std::snprintf(buf + len, size - len, "%s%s %d/%d (hw %d, peak %d)", len ? ", " : "", name, v.live,
                                v.capacity, v.highWater, g_poolPeak[i]);
    if (n < 0 || static_cast<std::size_t>(len + n) >= size) break;
    len += n;
  }
}

bool PoolCount(const char* typeName, int* live, int* highWater, int* capacity) {
  if (!g_sim || !typeName) return false;
  int i = 0;
  for (void* d = SehNextType(nullptr); d && i < kMaxTypes; d = SehNextType(d), ++i) {
    PoolView v{};
    if (!SehPoolView(d, &v) || std::strcmp(v.name, typeName) != 0) continue;
    *live = v.live;
    *highWater = v.highWater;
    *capacity = v.capacity;
    return true;
  }
  return false;
}

}  // namespace components
