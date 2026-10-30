#include "abilities.h"

#include <windows.h>

#include <intrin.h>

#include <cstdint>
#include <cstdio>
#include <cstring>
#include <string>

#include "core/damage/pipeline.h"
#include "core/damage/regen.h"
#include "core/damage/upkeep.h"
#include "game.h"
#include "hook.h"
#include "log.h"
#include "matchstats_cc.h"
#include "shop.h"

using namespace game;

namespace {

AbilityTickOptions g_opt;
void* g_sim;
bool g_inScheduler;
CreateTickThreadFn g_origCreate;
RecreateTickThreadFn g_origRecreate;
ResumeThreadFn g_origResume;

// Instances whose thread the harness watches: {instance id, thread id}.
struct Tracked {
  unsigned inst;
  unsigned tid;
  unsigned endCalls;
  bool dropped;
  bool seen;  // modifier lifetime logged once (D1e)
  // DISCIPLINE: the upkeep of a running agent ability (kept across reactivations: the instance keeps running)
  bool upkeepSeen;            // its static data looked at once
  bool upkeepOn;              // it has an upkeep
  unsigned long long upkeepLast;  // the last tick counted (ms, server clock)
  unsigned long long upkeepLog;   // the last progress line
  float upkeepPaid;
  unsigned upkeepTicks;
};
constexpr int kMaxTracked = 8192;
Tracked g_tracked[kMaxTracked];
int g_nTracked;

// Thread ids prepared while the scheduler ran; registered right after it.
constexpr int kMaxQueued = 2048;
unsigned g_queue[kMaxQueued];
int g_nQueue;

struct Counters {
  unsigned created, recreated, registered, queued, noTick, otherStatus, untracked;
  unsigned endFinished, endFailed, endGone, endNoTick, endExpired, endCalls, removed, deferred, overflow;
  unsigned timedModifiers, untimedModifiers;
  unsigned held, heldEndPending, endPending;  // D10: resumes held in use, ends deferred during them, ended afterwards
  unsigned upkeepInstances, upkeepTicks, endUpkeep, upkeepExceptions;  // DISCIPLINE
  double upkeepPaid;
} c;

unsigned g_eventLogs;
bool LogThis() {
  unsigned k = ++g_eventLogs;
  return k <= static_cast<unsigned>(g_opt.logLimit) || k % 100 == 0;
}

// D6c: the early-end trace (EndAbility and InterruptAbility detoured; see AbilityTickSetHostCause).
EndAbilityFn g_origEnd;
InterruptAbilityFn g_origInterrupt;
const char* g_hostCause;          // the host's own reason around its calls (the end pass, the death path)
std::uintptr_t g_interruptCaller; // InterruptAbility's caller while it runs
struct EarlyCause {
  std::string name;
  unsigned n = 0;
};
constexpr int kMaxCauses = 16;
EarlyCause g_causes[kMaxCauses];
int g_nCauses;
unsigned g_early, g_earlyHolding, g_earlyLogs;

char* LuaSim() { return static_cast<char*>(g_sim) + sim::kLua; }

bool Ours() { return g_sim && GetCurrentSim() == g_sim; }

// The instance's static ability name (its Lua table), read under SEH.
const char* InstName(void* instance) {
  __try {
    char* st = Field<char*>(instance, inst::kStatic);
    const char* name = st ? *reinterpret_cast<const char**>(st) : nullptr;
    if (name) {
      volatile char probe = name[0];
      (void)probe;
      return name;
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
  return "?";
}

double GameSeconds() {
  return static_cast<double>(GetTimeMs(g_sim)) / 1000.0;
}

int ThreadStatus(unsigned tid, void** threadOut) {
  void* th = tid ? GetThread(LuaSim(), tid) : nullptr;
  if (threadOut) *threadOut = th;
  return th ? Field<int>(th, thread::kStatus) : 99;  // 99 = no such thread
}

Tracked* Find(unsigned instId) {
  for (int i = 0; i < g_nTracked; ++i)
    if (!g_tracked[i].dropped && g_tracked[i].inst == instId) return &g_tracked[i];
  return nullptr;
}

void Track(unsigned instId, unsigned tid) {
  if (Tracked* t = Find(instId)) {
    t->tid = tid;
    t->endCalls = 0;
    return;
  }
  if (g_nTracked >= kMaxTracked) {
    ++c.overflow;
    return;
  }
  g_tracked[g_nTracked++] = {instId, tid, 0, false, false};
}

// After CreateTickThread / RecreateTickThread returned: hand a prepared Tick to the scheduler.
void OnTickThreadPrepared(void* instance, bool recreated) {
  unsigned id = Field<unsigned>(instance, inst::kId);
  unsigned tid = Field<unsigned>(instance, inst::kThreadId);
  int type = Field<int>(instance, inst::kType);
  int status = ThreadStatus(tid, nullptr);
  const char* what;
  if (recreated) ++c.recreated; else ++c.created;
  if (status == 2) {
    if (g_inScheduler || Field<unsigned char>(LuaSim(), luasim::kCheckingSleeping)) {
      if (g_nQueue < kMaxQueued) {
        g_queue[g_nQueue++] = tid;
        ++c.queued;
        what = "queued (scheduler running)";
      } else {
        ++c.overflow;
        what = "NOT queued (queue full)";
      }
    } else {
      AddSleepingThread(LuaSim(), tid);
      ++c.registered;
      what = "registered";
    }
  } else if (status == 1) {
    ++c.noTick;
    what = "no Tick (status 1)";
  } else {
    ++c.otherStatus;
    what = "unexpected status";
  }
  Track(id, tid);
  if (LogThis())
    Log("ability %s: inst %08X %s (type %d) agent %08X target %08X tid %08X status %d -> %s (t %.2f s)",
        recreated ? "tick thread recreated" : "tick thread", id, InstName(instance), type,
        Field<unsigned>(instance, inst::kAgent), Field<unsigned>(instance, inst::kTarget), tid, status, what, GameSeconds());
  matchstats::cc::OnTickThread(instance, recreated);  // MSTATS4: wardsPlaced
}

bool GuardedPrepared(void* instance, bool recreated) {
  __try {
    OnTickThreadPrepared(instance, recreated);
    return true;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

void __cdecl CreateTickThreadHook(void* instance, void* L, unsigned killerId) {
  g_origCreate(instance, L, killerId);
  if (instance && Ours()) {
    if (!GuardedPrepared(instance, false)) Log("ability tick thread: EXCEPTION while registering inst %p", instance);
  } else if (instance) {
    ++c.untracked;
  }
}

void __cdecl RecreateTickThreadHook(void* instance) {
  g_origRecreate(instance);
  if (instance && Ours()) {
    if (!GuardedPrepared(instance, true)) Log("ability tick thread: EXCEPTION while registering inst %p (recreated)", instance);
  } else if (instance) {
    ++c.untracked;
  }
}

// D10: the tracked instance whose Tick runs on thread `tid`, or 0.
unsigned InstanceOfThread(unsigned tid) {
  for (int i = 0; i < g_nTracked; ++i)
    if (!g_tracked[i].dropped && g_tracked[i].tid == tid) return g_tracked[i].inst;
  return 0;
}

// D10: before a resume, the thread's instance is held in use (+0x230), as the engine's own synchronous calls do
// (LabsAbilities_RunActivateFunction 0x008FEE80 and nine more increment it around GearsLua_CallFunction): an
// EndAbility (0x00905E90) while it runs only sets +0x234. Returns the held instance id or 0.
unsigned HoldRunning(void* thread) {
  __try {
    const unsigned tid = Field<unsigned>(thread, 0);
    const unsigned id = tid ? InstanceOfThread(tid) : 0;
    if (!id) return 0;
    void* in = GetInstance(id);
    if (!in || Field<unsigned>(in, inst::kThreadId) != tid) return 0;
    ++Field<int>(in, inst::kInUse);
    ++c.held;
    return id;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return 0;
  }
}

void ReleaseRunning(unsigned id) {
  __try {
    void* in = GetInstance(id);
    if (!in || Field<int>(in, inst::kInUse) <= 0) return;
    if (--Field<int>(in, inst::kInUse) == 0 && Field<unsigned char>(in, inst::kEndPending)) {
      ++c.heldEndPending;
      if (LogThis())
        Log("ability end: inst %08X %s ended from inside its own Tick (EndAbility deferred while it ran; t %.2f s)", id,
            InstName(in), GameSeconds());
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
}

// ---- D12: the Lua that runs right now (AbilityLuaContext) -----------------------------------------------------------
// A stack of the server simulator's synchronous GearsLua_CallFunction calls and Tick resumes, innermost last, so the
// assert and Lua error lines can name the script: the binding asserts (AbilitiesLua.cpp, ...) and the "Lua Runtime"
// tracebacks of a failed resume name only the C function, not the script.
struct LuaFrame {
  const char* table;  // the Lua table's name (an ability's or modifier's script name), or nullptr
  int fn;             // nAbilityFns index (2 = Tick for a resume)
  unsigned inst;      // the ability instance, 0 if unknown
  unsigned agent;
};
constexpr int kMaxFrames = 32;
constexpr int kFnEndAbility = -2;  // the host's frame around LabsAbilities_EndAbility (not an nAbilityFns index)
LuaFrame g_frames[kMaxFrames];
int g_depth;  // may exceed kMaxFrames; frames beyond it aren't recorded
CallFunctionFn g_origCall;

const char* FnName(int fn) {
  static const char* const kNames[] = {
      "?", "Activate", "Tick", "Deactivate", "ClientActivate", "ClientTick", "ClientDeactivate", "ClientEvent",
      "ClientOnAbilityRequest", "CanUse", "ModifyDamageInitial", "ModifyDamageOutgoing", "ModifyDamageIncoming",
      "ModifyDamageFinal", "AbsorbDamage", "AbsorbDamage_Magical", "AbsorbDamage_Physical", "AbsorbDamageAvailable",
      "AbsorbDamageAvailable_Magical", "AbsorbDamageAvailable_Physical", "GetResourcePointCost", "GetDuration", "GetIcon",
      "GetLocalizedName", "GetLocalizedDescription", "TranslateToken", "OnTookDamage", "OnDealtDamage",
      "OnStackModifier", "OnUnStackModifier", "OnAbilityUsed", "OnAbilityRankUp", "OnNearbyDeath", "OnGainedModifier",
      "OnLostModifier", "OnEnterGrass", "OnExitGrass", "OnHitTarget", "OnProjectileEnd", "OnAttributeChanged",
      "OnTargetEnterVolume", "OnTargetExitVolume", "OnAuraApplied", "GetRange", "OnAbilityOnCooldown",
      "OnAbilityOffCooldown", "OnUserDefinedEvent", "OnInterrupt", "OnGrantGold", "OnGrantXP", "GetFinishOnDeath",
      "OnPlayerDeath", "OnRevive", "GetCollisionVolume", "OnReactivate", "OnLevelUp", "OnPlayerLevelUp",
      "OnBasicAttackRequest", "OnModifierRequest", "OnUtilityUsed", "OnProjectileSpawned",
      "GetLocalizedDescriptionNoObject", "OnBestTargetChanged", "OnEnterBattleReady", "OnExitBattleReady", "OnKill",
      "OnPlayerKillOrAssist"};
  if (fn == kFnEndAbility) return "EndAbility";
  if (fn == shop::kOnItemPurchasedKey) return "onItemPurchased";  // D15c: the host's alias (shop.h)
  return fn >= 0 && fn < static_cast<int>(sizeof kNames / sizeof kNames[0]) ? kNames[fn] : "?";
}

// The frame of a synchronous call: the table name (read under SEH), and for an ability's or modifier's table the
// argument as an instance (the LabsAbilities callers pass it; its id must name it back through GetInstance).
LuaFrame CallFrame(const char* table, int fn, void* arg) {
  LuaFrame f = {nullptr, fn, 0, 0};
  __try {
    if (table) {
      volatile char probe = table[0];
      (void)probe;
      f.table = table;
      if ((std::strncmp(table, "ability_", 8) == 0 || std::strncmp(table, "modifier_", 9) == 0) && arg) {
        const unsigned id = Field<unsigned>(arg, inst::kId);
        if (id && GetInstance(id) == arg) {
          f.inst = id;
          f.agent = Field<unsigned>(arg, inst::kAgent);
        }
      }
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
  return f;
}

// The frame of a resumed thread: the tracked instance whose Tick runs on it.
LuaFrame ResumeFrame(void* thread) {
  LuaFrame f = {nullptr, 2, 0, 0};
  __try {
    const unsigned tid = Field<unsigned>(thread, 0);
    const unsigned id = tid ? InstanceOfThread(tid) : 0;
    void* in = id ? GetInstance(id) : nullptr;
    if (in) {
      f.inst = id;
      f.agent = Field<unsigned>(in, inst::kAgent);
      f.table = InstName(in);
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
  return f;
}

void PushFrame(const LuaFrame& f) {
  if (g_depth < kMaxFrames) g_frames[g_depth] = f;
  ++g_depth;
}

void PopFrame() {
  if (g_depth > 0) --g_depth;
}

bool __fastcall CallFunctionHook(void* thread, void* edx, const char* table, int fn, void* arg, void* key, int a5,
                                 int a6, int a7) {
  const bool ours = Ours();
  if (ours) PushFrame(CallFrame(table, fn, arg));
  const bool r = g_origCall(thread, edx, table, fn, arg, key, a5, a6, a7);
  if (ours) PopFrame();
  return r;
}

int __fastcall ResumeThreadHook(void* thread) {
  const bool ours = thread && Ours();
  const unsigned held = (ours && g_opt.holdRunning) ? HoldRunning(thread) : 0;
  if (ours && g_opt.luaContext) PushFrame(ResumeFrame(thread));
  const int r = g_origResume(thread);
  if (ours && g_opt.luaContext) PopFrame();
  if (held) ReleaseRunning(held);
  return r;
}

// ---- D6c: who ends an agent ability before its Tick finished ---------------------------------------------------------
// The scripts' "Attemping to call OnHitTarget/OnProjectileEnd, but the ability has already been destroyed" asserts
// (Lua/ProjectileUtils.lua: nAbility.IsAbilityRunning(casterAbility) is false when the projectile hits or ends) mean
// that the ability that fired the projectile ended while it flew. A Tick that waits for its projectiles
// (nThread.WaitForProjectileToEnd, WaitForAllProjectilesToEnd) can't end by itself before that, so something ended it
// from outside: this trace names what, for every agent ability (instance type 0) whose Tick thread hadn't finished.

const char* ClientCallerName(std::uintptr_t ra) {
  switch (ra) {
    case kRetBtUseAbilityAbort: return "the behavior tree's UseAbility abort";
    case kRetBtHoldsAgentAbort: return "the behavior tree's AbilityHoldsAgent abort";
    case kRetCommandQueue: return "the player's command queue";
    case kRetInterruptAbility: return "InterruptAbility";
    case kRetMarkForDelete: return "nAbility.MarkForDelete";
    case kRetMarkSelfForDelete: return "nAbility.MarkSelfForDelete";
    case kRetEventReturnedFalse: return "an event function that returned false";
    case kRetDecrementStackCount: return "nModifier.DecrementStackCount";
    default: return nullptr;
  }
}

void CountCause(const std::string& cause, bool holding) {
  ++g_early;
  if (holding) ++g_earlyHolding;
  for (int i = 0; i < g_nCauses; ++i)
    if (g_causes[i].name == cause) {
      ++g_causes[i].n;
      return;
    }
  if (g_nCauses < kMaxCauses) {
    g_causes[g_nCauses].name = cause;
    g_causes[g_nCauses].n = 1;
    ++g_nCauses;
  }
}

struct EarlyInfo {
  bool holding = false;
  int status = 0, inUse = 0;
  unsigned agent = 0;
  const char* name = "?";
};

// True if instance `id` is an agent ability whose Tick hasn't ended (and whose end wasn't deferred already).
bool SehEarlyInfo(unsigned id, EarlyInfo* e) {
  __try {
    void* in = GetInstance(id);
    if (!in || Field<int>(in, inst::kType) != 0) return false;   // agent abilities only
    if (Field<unsigned char>(in, inst::kEndPending)) return false;  // noted when the end was deferred
    e->status = ThreadStatus(Field<unsigned>(in, inst::kThreadId), nullptr);
    if (e->status == -1 || e->status == -2 || e->status == 99) return false;  // its Tick ended or failed: a normal end
    if (e->status == 1) return false;                                        // no Tick at all (end_no_tick)
    e->holding = Field<unsigned char>(in, inst::kHoldsAgent) != 0;
    e->inUse = Field<int>(in, inst::kInUse);
    e->agent = Field<unsigned>(in, inst::kAgent);
    e->name = InstName(in);
    return true;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

// Whether a return address lies in this DLL (the host's own calls: the end pass, the death path).
bool OurAddress(std::uintptr_t ra) {
  static HMODULE self = nullptr;
  if (!self)
    GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                       reinterpret_cast<LPCWSTR>(&OurAddress), &self);
  HMODULE m = nullptr;
  return self &&
         GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                            reinterpret_cast<LPCWSTR>(ra), &m) &&
         m == self;
}

void NoteEarlyEnd(unsigned id, std::uintptr_t ra) {
  EarlyInfo e;
  if (!SehEarlyInfo(id, &e)) return;
  std::string cause;
  char hex[40];
  if (OurAddress(ra)) {
    cause = g_hostCause ? g_hostCause : "the host";
  } else if (ra == kRetInterruptAbility) {
    const char* n = OurAddress(g_interruptCaller) ? (g_hostCause ? g_hostCause : "the host")
                                                  : ClientCallerName(g_interruptCaller);
    if (!n) std::snprintf(hex, sizeof hex, "%08X", static_cast<unsigned>(g_interruptCaller));
    cause = std::string("interrupted by ") + (n ? n : hex);
  } else {
    const char* n = ClientCallerName(ra);
    if (!n) std::snprintf(hex, sizeof hex, "a call from %08X", static_cast<unsigned>(ra));
    cause = n ? n : hex;
  }
  CountCause(cause, e.holding);
  const unsigned k = ++g_earlyLogs;
  if (k <= static_cast<unsigned>(g_opt.logLimit) || k % 100 == 0)
    Log("ability early end (D6c): inst %08X %s agent %08X, holds the agent %d, Tick thread status %d, in use %d: %s "
        "(t %.2f s)",
        id, e.name, e.agent, e.holding ? 1 : 0, e.status, e.inUse, cause.c_str(), GameSeconds());
}

LuaFrame EndFrame(unsigned id) {
  LuaFrame f = {nullptr, kFnEndAbility, id, 0};
  __try {
    void* in = GetInstance(id);
    if (in) {
      f.table = InstName(in);
      f.agent = Field<unsigned>(in, inst::kAgent);
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
  return f;
}

void __cdecl EndAbilityHook(unsigned id) {
  const bool ours = Ours();
  if (ours) NoteEarlyEnd(id, reinterpret_cast<std::uintptr_t>(_ReturnAddress()));
  // D12: an assert while the instance is removed (Deactivate's Lua has its own frame; DamageTracker_RecordModifierEvent
  // and the assist tracker run outside it) names the instance being ended
  if (ours && g_opt.luaContext) PushFrame(EndFrame(id));
  g_origEnd(id);
  if (ours && g_opt.luaContext) PopFrame();
}

int __cdecl InterruptAbilityHook(unsigned id) {
  const std::uintptr_t prev = g_interruptCaller;
  g_interruptCaller = reinterpret_cast<std::uintptr_t>(_ReturnAddress());
  const int r = g_origInterrupt(id);
  g_interruptCaller = prev;
  return r;
}

const char* StatusName(int status) {
  switch (status) {
    case -1: return "finished";
    case -2: return "failed";
    case 99: return "thread gone";
    case 1: return "no Tick";
    case 100: return "duration over (D1e)";
    case 101: return "end pending (D10)";
    case 102: return "out of resource for its upkeep (DISCIPLINE)";
    default: return "?";
  }
}

// D1e: modifier lifetime. cStaticAbility.deactivationType (+0x428, nDeactivationType: OnDuration 1,
// OnAgentDestroyed 2, OnAgentDeath 4; the constructor 0x00901219 defaults it to 1) is read by no
// instruction in the client. A modifier whose type has OnDuration ends once the simulator clock
// reaches start (+0x208) + duration (+0x220, ms), the same sum nModifier.GetRemainingDuration
// (0x008C7C40) counts down to. A duration <= 0 is taken as "no time limit" (passives such as
// modifier_base_regen have OnDuration by default and no duration). D13: the rule is
// gms::damage::ModifierExpired (core/damage/pipeline.h, gms_tests DamageEdgeLifetime).
bool ModifierExpired(Tracked& t, void* in) {
  if (!g_opt.modifierExpiry) return false;
  char* st = Field<char*>(in, inst::kStatic);
  if (!st) return false;
  unsigned deact = Field<unsigned>(st, kStaticDeactivationType);
  int duration = Field<int>(in, inst::kDurationMs);
  unsigned long long start = Field<unsigned long long>(in, inst::kStartMs);
  unsigned long long now = GetTimeMs(g_sim);
  if (!t.seen) {
    t.seen = true;
    if ((deact & 1) && duration > 0) ++c.timedModifiers; else ++c.untimedModifiers;
    if (LogThis())
      Log("modifier inst %08X %s: deactivationType %u, duration %d ms, start %llu ms (now %llu)", t.inst, InstName(in), deact,
          duration, start, now);
  }
  return gms::damage::ModifierExpired(deact, duration, start, now);
}

// DISCIPLINE: an agent ability's upkeep (cStaticAbility +0x340 upkeepResourcePointCost by rank, every +0x360
// upkeepResourcePointTickRate seconds) comes out of its agent's resource while the instance runs; a tick that finds
// less than the cost ends the ability (gms::damage::UpkeepStep). No client instruction but the reflection and the copy
// reads the pair: the stripped server update spent it. The one ability with an upkeep is Raina's W
// (ability_Guardian_W_ConeShield, 1.25 every 0.1 s). Written straight into the combatant data as regen.cpp does; the
// replication's A0 delta carries mResourcePoints to the clients. Returns true when the ability must end.
bool Upkeep(Tracked& t, void* in) {
  char* st = Field<char*>(in, inst::kStatic);
  if (!st) return false;
  const float tick = Field<float>(st, kStaticUpkeepTickRate);
  const int rank = Field<int>(in, inst::kRank);
  const float cost = gms::damage::UpkeepCostForRank(reinterpret_cast<const float*>(st + kStaticUpkeepCost), rank);
  const unsigned long long now = GetTimeMs(g_sim);
  if (!t.upkeepSeen) {
    t.upkeepSeen = true;
    t.upkeepOn = cost > 0.f && tick > 0.f;
    if (!t.upkeepOn) return false;
    const unsigned long long start = Field<unsigned long long>(in, inst::kStartMs);
    t.upkeepLast = start && start <= now ? start : now;
    t.upkeepLog = now;
    ++c.upkeepInstances;
    void* o = GetObjectById(Field<unsigned>(in, inst::kAgent));
    char* cd = o ? Field<char*>(o, gobj::kCombatant) : nullptr;
    Log("upkeep: inst %08X %s agent %08X rank %d: %.2f every %.2f s (%.2f/s), resource %.2f (t %.2f s)", t.inst, InstName(in),
        Field<unsigned>(in, inst::kAgent), rank, cost, tick, cost / tick,
        cd ? Field<float>(cd, combatant::kResourcePoints) : -1.f, GameSeconds());
  }
  if (!t.upkeepOn) return false;
  void* o = GetObjectById(Field<unsigned>(in, inst::kAgent));
  char* cd = o ? Field<char*>(o, gobj::kCombatant) : nullptr;
  if (!cd) return false;
  if (GetResourceType(o) == gms::damage::kResourceTypeHealth) return false;  // [assumed] no upkeep out of health
  float& res = Field<float>(cd, combatant::kResourcePoints);
  const float before = res;
  const gms::damage::UpkeepResult r = gms::damage::UpkeepStep(before, cost, tick, t.upkeepLast, now);
  t.upkeepLast = r.lastTickMs;
  if (r.ticksPaid > 0) {
    res = r.resource;
    t.upkeepPaid += before - r.resource;
    t.upkeepTicks += static_cast<unsigned>(r.ticksPaid);
    c.upkeepPaid += before - r.resource;
    c.upkeepTicks += static_cast<unsigned>(r.ticksPaid);
  }
  if (r.end) {
    ++c.endUpkeep;
    Log("upkeep: inst %08X %s agent %08X: resource %.2f below the cost %.2f after %u ticks (%.2f paid): ending it (t %.2f s)",
        t.inst, InstName(in), Field<unsigned>(in, inst::kAgent), r.resource, cost, t.upkeepTicks, t.upkeepPaid, GameSeconds());
    return true;
  }
  if (now - t.upkeepLog >= 1000 && LogThis()) {
    t.upkeepLog = now;
    Log("upkeep: inst %08X %s agent %08X: resource %.2f (%u ticks, %.2f paid so far; t %.2f s)", t.inst, InstName(in),
        Field<unsigned>(in, inst::kAgent), r.resource, t.upkeepTicks, t.upkeepPaid, GameSeconds());
  }
  return false;
}

bool SehUpkeep(Tracked& t, void* in) {
  __try {
    return Upkeep(t, in);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    if (++c.upkeepExceptions <= 5) Log("EXCEPTION in the upkeep of inst %08X; its upkeep off", t.inst);
    t.upkeepSeen = true;
    t.upkeepOn = false;
    return false;
  }
}

void EndPass() {
  // New instances may be created while we end others (Deactivate runs Lua); they are appended and
  // looked at on the next pass, so iterate over the entries that exist now.
  int n = g_nTracked;
  for (int i = 0; i < n; ++i) {
    Tracked& t = g_tracked[i];
    if (t.dropped) continue;
    void* in = GetInstance(t.inst);
    if (!in) {
      if (t.endCalls) {
        ++c.removed;
        if (LogThis()) Log("ability end: inst %08X removed (t %.2f s)", t.inst, GameSeconds());
      }
      t.dropped = true;
      continue;
    }
    unsigned tidNow = Field<unsigned>(in, inst::kThreadId);
    if (tidNow != t.tid) t.tid = tidNow;  // reactivated without our hook seeing it (shouldn't happen)
    void* th;
    int status = ThreadStatus(t.tid, &th);
    int type = Field<int>(in, inst::kType);
    bool end = status == -1 || status == -2 || status == 99 || (status == 1 && type == 0 && g_opt.endNoTick);
    if (!end && type == 1 && ModifierExpired(t, in)) {
      end = true;
      status = 100;
    }
    // DISCIPLINE: a running agent ability pays its upkeep; one that can't is ended
    if (!end && type == 0 && g_opt.upkeep && !Field<unsigned char>(in, inst::kEndPending) && SehUpkeep(t, in)) {
      end = true;
      status = 102;
    }
    // D10: an EndAbility that came while something held the instance (its own running Tick) is still pending
    if (!end && Field<unsigned char>(in, inst::kEndPending) && Field<int>(in, inst::kInUse) == 0) {
      end = true;
      status = 101;
    }
    if (!end) continue;
    g_hostCause = status == 101   ? "the end pass (an end deferred while it ran)"
                  : status == 102 ? "the end pass (out of resource for its upkeep)"
                                  : "the end pass";
    if (t.endCalls == 0) {
      switch (status) {
        case 100: ++c.endExpired; break;
        case 101: ++c.endPending; break;
        case 102: break;  // counted by Upkeep
        case -1: ++c.endFinished; break;
        case -2: ++c.endFailed; break;
        case 99: ++c.endGone; break;
        default: ++c.endNoTick; break;
      }
      if (LogThis() || status == -2)
        Log("ability end: inst %08X %s (type %d) agent %08X tid %08X %s -> EndAbility (t %.2f s)", t.inst, InstName(in), type,
            Field<unsigned>(in, inst::kAgent), t.tid, StatusName(status), GameSeconds());
    }
    EndAbility(t.inst);
    g_hostCause = nullptr;
    ++t.endCalls;
    ++c.endCalls;
    if (!GetInstance(t.inst)) {
      ++c.removed;
      t.dropped = true;
    } else if (t.endCalls == 1) {
      ++c.deferred;
      Log("ability end: inst %08X still exists after EndAbility (in use %u, end pending %u); retrying each tick", t.inst,
          Field<unsigned>(in, inst::kInUse), Field<unsigned char>(in, inst::kEndPending));
    }
  }
  // compact
  int w = 0;
  for (int i = 0; i < g_nTracked; ++i)
    if (!g_tracked[i].dropped) g_tracked[w++] = g_tracked[i];
  g_nTracked = w;
}

}  // namespace

bool AbilityTickInstall(const AbilityTickOptions& options) {
  g_opt = options;
  g_origCreate = reinterpret_cast<CreateTickThreadFn>(
      HookDetour(kCreateTickThread, kTickThreadBytes, sizeof kTickThreadBytes, reinterpret_cast<void*>(&CreateTickThreadHook)));
  g_origRecreate = reinterpret_cast<RecreateTickThreadFn>(
      HookDetour(kRecreateTickThread, kTickThreadBytes, sizeof kTickThreadBytes, reinterpret_cast<void*>(&RecreateTickThreadHook)));
  if (g_opt.holdRunning || g_opt.luaContext)
    g_origResume = reinterpret_cast<ResumeThreadFn>(HookDetour(kResumeThread, kResumeThreadBytes, sizeof kResumeThreadBytes,
                                                               reinterpret_cast<void*>(&ResumeThreadHook)));
  if (g_opt.luaContext)  // D12
    g_origCall = reinterpret_cast<CallFunctionFn>(HookDetour(kCallFunction, kCallFunctionBytes, sizeof kCallFunctionBytes,
                                                             reinterpret_cast<void*>(&CallFunctionHook)));
  Log("ability tick hooks: CreateTickThread %s, RecreateTickThread %s, ResumeThread %s; end_no_tick %d, modifier_expiry %d, "
      "hold_running %d, upkeep %d; Lua context (D12) %s",
      g_origCreate ? "installed" : "FAILED", g_origRecreate ? "installed" : "FAILED",
      !(g_opt.holdRunning || g_opt.luaContext) ? "off" : g_origResume ? "installed" : "FAILED", g_opt.endNoTick,
      g_opt.modifierExpiry, g_opt.holdRunning, g_opt.upkeep,
      !g_opt.luaContext ? "off" : g_origCall && g_origResume ? "installed (CallFunction, ResumeThread)" : "FAILED");
  if (!g_origResume) g_opt.holdRunning = 0;
  if (!g_origResume || !g_origCall) g_opt.luaContext = 0;
  if (g_opt.endTrace) {  // D6c
    g_origEnd = reinterpret_cast<EndAbilityFn>(
        HookDetour(kEndAbility, kEndAbilityBytes, sizeof kEndAbilityBytes, reinterpret_cast<void*>(&EndAbilityHook)));
    g_origInterrupt = reinterpret_cast<InterruptAbilityFn>(HookDetour(
        kInterruptAbility, kInterruptAbilityBytes, sizeof kInterruptAbilityBytes, reinterpret_cast<void*>(&InterruptAbilityHook)));
    Log("ability early-end trace (D6c): EndAbility %s, InterruptAbility %s", g_origEnd ? "installed" : "FAILED",
        g_origInterrupt ? "installed" : "FAILED");
  }
  return g_origCreate && g_origRecreate;
}

void AbilityTickSetHostCause(const char* cause) { g_hostCause = cause; }

void AbilityLuaContext(char* buf, std::size_t size) {
  if (!size) return;
  buf[0] = 0;
  if (!g_opt.luaContext || g_depth <= 0 || !Ours()) return;
  const int top = (g_depth < kMaxFrames ? g_depth : kMaxFrames) - 1;
  auto frame = [](const LuaFrame& f, char* out, std::size_t n) {
    // nAbilityFns names only for an ability's or modifier's table; other tables (behaviors, conditions) by index
    const char* t = f.table ? f.table : "?";
    const bool ability = std::strncmp(t, "ability_", 8) == 0 || std::strncmp(t, "modifier_", 9) == 0;
    char fn[24];
    if (ability || f.fn == kFnEndAbility) std::snprintf(fn, sizeof fn, "%s", FnName(f.fn));
    else std::snprintf(fn, sizeof fn, "[%d]", f.fn);
    if (f.inst) std::snprintf(out, n, "%s.%s inst %08X agent %08X", t, fn, f.inst, f.agent);
    else std::snprintf(out, n, "%s.%s", t, fn);
  };
  char inner[160], outer[160];
  frame(g_frames[top], inner, sizeof inner);
  if (top > 0) {
    frame(g_frames[0], outer, sizeof outer);
    std::snprintf(buf, size, " [in %s <- %s%s]", inner, top > 1 ? "... <- " : "", outer);
  } else {
    std::snprintf(buf, size, " [in %s]", inner);
  }
}

unsigned AbilityLuaInstance() {
  if (!g_opt.luaContext || g_depth <= 0 || g_depth > kMaxFrames || !Ours()) return 0;
  return g_frames[g_depth - 1].inst;
}

const char* AbilityInstanceName(unsigned id, int* type) {
  if (type) *type = -1;
  if (!id) return "gone";
  __try {
    void* in = GetInstance(id);
    if (!in) return "gone";
    if (type) *type = Field<int>(in, inst::kType);
    return InstName(in);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return "gone";
  }
}

void AbilityTickSetSim(void* sim) { g_sim = sim; }

void AbilityTickBeforeScheduler() { g_inScheduler = true; }

void AbilityTickAfterScheduler() {
  g_inScheduler = false;
  if (!g_sim) return;
  for (int i = 0; i < g_nQueue; ++i) {
    unsigned tid = g_queue[i];
    if (ThreadStatus(tid, nullptr) == 2) {
      AddSleepingThread(LuaSim(), tid);
      ++c.registered;
    }
  }
  g_nQueue = 0;
  EndPass();
}

void AbilityTickFormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size,
                "tick threads %u (+%u recreated): registered %u (queued %u), no Tick %u, other %u; ended %u (finished %u, failed %u, "
                "gone %u, no Tick %u, expired %u, end pending %u), removed %u, deferred %u, watched %d; modifiers timed %u untimed %u; "
                "resumes held %u (ends deferred %u)%s",
                c.created, c.recreated, c.registered, c.queued, c.noTick, c.otherStatus,
                c.endFinished + c.endFailed + c.endGone + c.endNoTick + c.endExpired + c.endPending, c.endFinished, c.endFailed,
                c.endGone, c.endNoTick, c.endExpired, c.endPending, c.removed, c.deferred, g_nTracked, c.timedModifiers,
                c.untimedModifiers, c.held, c.heldEndPending, c.overflow ? " OVERFLOW" : "");
  int len = static_cast<int>(std::strlen(buf));
  // DISCIPLINE: upkeep costs paid by running abilities
  if (g_opt.upkeep && len > 0 && static_cast<std::size_t>(len) < size)
    len += std::snprintf(buf + len, size - len, "; upkeep: %u instances, %u ticks, %.1f paid, %u ended out of resource%s",
                         c.upkeepInstances, c.upkeepTicks, c.upkeepPaid, c.endUpkeep,
                         c.upkeepExceptions ? " (EXCEPTIONS)" : "");
  // D6c: agent abilities ended before their Tick finished, by cause
  if (g_opt.endTrace && len > 0 && static_cast<std::size_t>(len) < size) {
    len += std::snprintf(buf + len, size - len, "; early ends (D6c) %u (%u holding the agent):", g_early, g_earlyHolding);
    for (int i = 0; i < g_nCauses && len > 0 && static_cast<std::size_t>(len) < size; ++i)
      len += std::snprintf(buf + len, size - len, "%s %s %u", i ? "," : "", g_causes[i].name.c_str(), g_causes[i].n);
  }
}
