// D1k: the minions' crowd on the server: see crowd.h.
#include "crowd.h"

#include <windows.h>

#include <cstdio>
#include <cstring>

#include "core/agents/camp_crowd.h"
#include "game.h"
#include "log.h"

using namespace game;

namespace crowd {
namespace {

Options g_opt;
void* g_handle;  // the server space's handle (cLabsAStarSystem +0xD20)

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  Log("EXCEPTION in crowd (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
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

// The space handle, the space object and its navigator space (null where missing).
bool SehSpace(void* sim, void** handle, void** space, void** navSpace) {
  __try {
    *handle = *space = *navSpace = nullptr;
    char* astar = Field<char*>(sim, navpower::kSimAStarSystem);
    if (!astar) return false;
    *handle = astar + navpower::kSpaceHandle;
    void** slot = Field<void**>(astar, navpower::kSpaceHandle);
    *space = slot ? *slot : nullptr;
    if (!*space) return false;
    char* comps = Field<char*>(*space, navpower::kSpaceComponents);
    *navSpace = comps ? Field<void*>(comps, navpower::kNavigatorSpace) : nullptr;
    return *navSpace != nullptr;
  } __except (SehNote(GetExceptionInformation(), "space")) {
    return false;
  }
}

int SehSolverFlag(void* navSpace) {
  __try {
    return navSpace ? Field<unsigned char>(navSpace, navpower::kConstraintSolver) : -1;
  } __except (SehNote(GetExceptionInformation(), "solver flag")) {
    return -1;
  }
}

bool SehModuleCounts(int* navigators, int* repulsors) {
  __try {
    *navigators = *repulsors = -1;
    char* sys = *reinterpret_cast<char**>(navpower::kSystem);
    char* modules = sys ? Field<char*>(sys, 8) : nullptr;
    char* nav = modules ? Field<char*>(modules, 0xC) : nullptr;
    if (!nav) return false;
    *navigators = Field<int>(nav, navpower::kNavigatorCount);
    *repulsors = Field<int>(nav, navpower::kRepulsorCount);
    return true;
  } __except (SehNote(GetExceptionInformation(), "module counts")) {
    return false;
  }
}

bool SehEnable(void* handle) {
  __try {
    navpower::EnableConstraintSolver(handle, 1);
    return true;
  } __except (SehNote(GetExceptionInformation(), "NavigatorEnableConstraintSolver")) {
    return false;
  }
}

bool SehMover(std::uint32_t id, void** mover, void** repulsor, unsigned* on) {
  __try {
    *mover = *repulsor = nullptr;
    *on = 0;
    void* o = GetObjectById(id);
    if (!o) return false;
    *mover = Field<void*>(o, obj::kMover);
    if (!*mover) return false;
    *repulsor = Field<void*>(*mover, navpower::kMoverRepulsor);
    *on = Field<unsigned char>(*mover, navpower::kMoverRepulsorOn);
    return true;
  } __except (SehNote(GetExceptionInformation(), "mover")) {
    return false;
  }
}

}  // namespace

void Configure(const Options& options) { g_opt = options; }

void OnLevelStarted(void* sim) {
  void *handle = nullptr, *space = nullptr, *navSpace = nullptr;
  const bool ok = SehSpace(sim, &handle, &space, &navSpace);
  int navigators = -1, repulsors = -1;
  SehModuleCounts(&navigators, &repulsors);
  const int before = SehSolverFlag(navSpace);
  Log("crowd: the server's NavPower space: handle %p, space %p, navigator space %p (%s); constraint solver %d, "
      "navigator module: %d navigators, %d repulsors",
      handle, space, navSpace, ok ? "ok" : "MISSING", before, navigators, repulsors);
  if (!ok) return;
  g_handle = handle;
  if (!g_opt.constraintSolver) {
    Log("crowd: navpower_constraint_solver=0: the constraint solver stays %s (movers don't push each other)",
        before ? "on" : "off");
    return;
  }
  if (!CodeMatches(0x00C173E0, navpower::kEnableConstraintSolverBytes, sizeof navpower::kEnableConstraintSolverBytes)) {
    Log("crowd: NavigatorEnableConstraintSolver's prologue differs (not build 854?); left off");
    return;
  }
  const bool called = SehEnable(handle);
  Log("crowd: bfx::NavigatorEnableConstraintSolver(server space, 1) %s: constraint solver %d -> %d", called ? "called"
      : "FAILED", before, SehSolverFlag(navSpace));
}

namespace {

// CAMPREACH: reads a creature's repulsor flags from its locomotion and writes the camp's (both words at once).
bool SehCampFlags(std::uint32_t id, int tier, gms::agents::RepulsorFlags* before, gms::agents::RepulsorFlags* after) {
  __try {
    void* o = GetObjectById(id);
    char* l = o ? Field<char*>(o, loco::kOfObject) : nullptr;
    if (!l) return false;
    before->blockage = Field<std::uint32_t>(l, loco::kRepulsorBlockage);
    before->identity = Field<std::uint32_t>(l, loco::kRepulsorIdentity);
    *after = gms::agents::CampCreepRepulsor(tier, *before);
    Field<std::uint32_t>(l, loco::kRepulsorBlockage) = after->blockage;
    Field<std::uint32_t>(l, loco::kRepulsorIdentity) = after->identity;
    return true;
  } __except (SehNote(GetExceptionInformation(), "camp repulsor flags")) {
    return false;
  }
}

unsigned g_campCreatures, g_campLogged;

}  // namespace

bool CampCreature(std::uint32_t id, int tier, const char* noun) {
  if (!g_opt.campReach) return true;
  gms::agents::RepulsorFlags before, after;
  if (!SehCampFlags(id, tier, &before, &after)) return false;
  ++g_campCreatures;
  if (g_campLogged < 12) {
    ++g_campLogged;
    Log("crowd: camp creature %08X %s tier %d: repulsor blockage %08X -> %08X, identity %08X -> %08X (CAMPREACH: not "
        "blocked by its camp's lower tiers)",
        id, noun ? noun : "?", tier, before.blockage, after.blockage, before.identity, after.identity);
  }
  return true;
}

bool MoverRepulsor(std::uint32_t id, void** mover, void** repulsor, unsigned* on) {
  return SehMover(id, mover, repulsor, on);
}

void FormatStats(char* buf, std::size_t size) {
  int navigators = -1, repulsors = -1;
  SehModuleCounts(&navigators, &repulsors);
  int flag = -1;
  if (g_handle) {
    void *h = nullptr, *s = nullptr, *n = nullptr;
    void* sim = GetCurrentSim();
    if (sim && SehSpace(sim, &h, &s, &n) && h == g_handle) flag = SehSolverFlag(n);
  }
  std::snprintf(buf, size, "constraint solver %d, navigators %d, repulsors %d, camp creatures %u (camp_reach %d)", flag,
                navigators, repulsors, g_campCreatures, g_opt.campReach ? 1 : 0);
}

}  // namespace crowd
