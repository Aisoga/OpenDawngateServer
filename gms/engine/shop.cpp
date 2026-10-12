// D15c: the shop's purchase effect on the server: see shop.h.
#include "shop.h"

#include <windows.h>

#include <cmath>
#include <cstdio>
#include <cstring>
#include <vector>

#include "game.h"
#include "host/host.h"
#include "log.h"
#include "replicate.h"

using namespace game;

namespace shop {
namespace {

constexpr const char* kTable = "modifier_Base_ShoppeArchon";
constexpr const char* kArchonPrefix = "itemshoppe_archon";
constexpr std::uintptr_t kItemProjectileEffect = 0x58;  // Item +0x58 ProjectileEffect (asset pointer)
constexpr std::uintptr_t kItemProjectileOnHit = 0x5C;   // Item +0x5C ProjectileOnHit (asset pointer)

struct Options {
  int mode = 1;  // 0 off, 1 the nearest archon of the buyer's team, 2 every archon of the team
  int logLimit = 200;
};
Options g_opt;
bool g_configured;
void* g_sim;
bool g_aliased;  // modifier_Base_ShoppeArchon[kOnItemPurchasedKey] set

struct Archon {
  std::uint32_t object = 0;
  std::uint32_t inst = 0;  // its modifier_Base_ShoppeArchon instance
  unsigned team = 0;
  float pos[3] = {};
  char name[48] = {};
};
std::vector<Archon> g_archons;

struct Stats {
  unsigned purchases = 0, calls = 0, callFailed = 0, noArchon = 0, exceptions = 0;
  unsigned fxCalls = 0, fxNamed = 0, fxEmpty = 0;
  unsigned scans = 0, logLines = 0;
} g_st;

bool LogBudget() {
  ++g_st.logLines;
  return g_st.logLines <= static_cast<unsigned>(g_opt.logLimit) || g_st.logLines % 100 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  ++g_st.exceptions;
  Log("EXCEPTION in shop (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

void Configure() {
  if (g_configured) return;
  g_configured = true;
  g_opt.mode = HostOptionInt("shop_fx", 1);
  if (g_opt.mode < 0 || g_opt.mode > 2) g_opt.mode = 1;
  g_opt.logLimit = HostOptionInt("shop_fx_log_limit", g_opt.logLimit);
  Log("shop (D15c): the purchase effect (modifier_Base_ShoppeArchon.onItemPurchased, nBattlegrounds.GetItemFx*): %s",
      g_opt.mode == 0   ? "off ([host] shop_fx=0)"
      : g_opt.mode == 1 ? "on, the buyer team's archon nearest to the buyer"
                        : "on, every archon of the buyer's team ([host] shop_fx=2)");
}

bool Ours() { return g_sim && GetCurrentSim() == g_sim; }

// ---- nBattlegrounds.GetItemFxProjectile / GetItemFxOnHit -------------------------------------------------------------

// The item asset's effect name at `field` of its Item data, "" for none (the stub's value: nUtil.GetAsset("") gives
// kNullAsset, and the script skips the effect).
const char* SehItemFx(void* L, std::uintptr_t field) {
  __try {
    std::uint32_t id = 0;
    const int t = LuaType(L, 1);
    if (t == kLuaTypeLightUserdata) id = static_cast<std::uint32_t>(reinterpret_cast<std::uintptr_t>(LuaToUserdata(L, 1)));
    else if (t == kLuaTypeNumber) id = static_cast<std::uint32_t>(LuaToNumber(L, 1));
    void* asset = id ? FindAssetById(id) : nullptr;
    char* data = asset ? Field<char*>(asset, asset::kData) : nullptr;
    void* fx = data ? Field<void*>(data, field) : nullptr;
    const char* name = fx ? Field<const char*>(fx, asset::kName) : nullptr;
    return name ? name : "";
  } __except (SehNote(GetExceptionInformation(), "GetItemFx")) {
    return "";
  }
}

int ItemFx(void* L, std::uintptr_t field) {
  const char* name = "";
  if (Ours()) {
    ++g_st.fxCalls;
    name = SehItemFx(L, field);
    if (name[0]) ++g_st.fxNamed;
    else ++g_st.fxEmpty;
  }
  __try {
    LuaPushString(L, name);
  } __except (SehNote(GetExceptionInformation(), "lua_pushstring")) {
  }
  return 1;
}

int __cdecl LuaGetItemFxProjectile(void* L) { return ItemFx(L, kItemProjectileEffect); }
int __cdecl LuaGetItemFxOnHit(void* L) { return ItemFx(L, kItemProjectileOnHit); }

const LuaReg kRegs[] = {
    {"GetItemFxProjectile", &LuaGetItemFxProjectile},
    {"GetItemFxOnHit", &LuaGetItemFxOnHit},
};

// ---- the archons ---------------------------------------------------------------------------------------------------

// The object's modifier_Base_ShoppeArchon instance (not ending), 0 if none.
std::uint32_t SehArchonInstance(void* object) {
  __try {
    char* c = object ? Field<char*>(object, obj::kAbilityData) : nullptr;
    if (!c) return 0;
    for (const std::uint32_t* p = Field<const std::uint32_t*>(c, container::kModifiersBegin);
         p && p != Field<const std::uint32_t*>(c, container::kModifiersEnd); ++p) {
      void* in = GetInstance(*p);
      const char* st = in ? Field<const char*>(in, inst::kStatic) : nullptr;
      const char* n = st ? *reinterpret_cast<const char* const*>(st) : nullptr;
      if (n && std::strcmp(n, kTable) == 0 && !Field<unsigned char>(in, inst::kEndPending)) return *p;
    }
    return 0;
  } __except (SehNote(GetExceptionInformation(), "archon modifier")) {
    return 0;
  }
}

bool SehReadArchon(void* object, Archon* a) {
  __try {
    a->team = Field<std::uint8_t>(object, gobj::kTeam);
    std::memcpy(a->pos, static_cast<char*>(object) + gobj::kPosition, sizeof a->pos);
    return true;
  } __except (SehNote(GetExceptionInformation(), "archon object")) {
    return false;
  }
}

void ScanArchons(void* sim) {
  ++g_st.scans;
  g_archons.clear();
  std::vector<repl::NetObject> objs;
  if (repl::Scan(sim, &objs) < 0) return;
  for (const repl::NetObject& o : objs) {
    if (std::strncmp(o.name, kArchonPrefix, std::strlen(kArchonPrefix)) != 0) continue;
    Archon a;
    a.object = o.id;
    std::snprintf(a.name, sizeof a.name, "%s", o.name);
    if (!SehReadArchon(o.object, &a)) continue;
    a.inst = SehArchonInstance(o.object);
    Log("shop: archon %08X %s team %u at (%.1f, %.1f): %s instance %08X", a.object, a.name, a.team, a.pos[0], a.pos[1],
        kTable, a.inst);
    if (a.inst) g_archons.push_back(a);
  }
}

bool ArchonsValid() {
  if (g_archons.empty()) return false;
  for (const Archon& a : g_archons) {
    void* in = GetInstance(a.inst);
    if (!in || Field<unsigned char>(in, inst::kEndPending)) return false;
  }
  return true;
}

bool SehArchonsValid() {
  __try {
    return ArchonsValid();
  } __except (SehNote(GetExceptionInformation(), "archon check")) {
    return false;
  }
}

// modifier_Base_ShoppeArchon[kOnItemPurchasedKey] = modifier_Base_ShoppeArchon.onItemPurchased on the server's state.
bool SehAlias(void* sim, char* out, unsigned cap) {
  static const char kChunk[] =
      "local t = modifier_Base_ShoppeArchon\n"
      "if type(t) ~= 'table' then return 'no modifier_Base_ShoppeArchon table' end\n"
      "if type(t.onItemPurchased) ~= 'function' then return 'no onItemPurchased' end\n"
      "t[1001] = t.onItemPurchased\n"
      "return 'ok'\n";
  static_assert(kOnItemPurchasedKey == 1001, "the chunk's key");
  __try {
    out[0] = 0;
    void* L = Field<void*>(static_cast<char*>(sim) + sim::kLua, luasim::kLuaState);
    const int top = LuaGetTop(L);
    int rc = LuaLoadBuffer(L, kChunk, static_cast<unsigned>(sizeof kChunk - 1), "=dgsim_shop_alias");
    if (rc == 0) rc = LuaPcall(L, 0, 1, 0);
    const char* s = LuaToString(L, -1, nullptr);
    if (s) std::snprintf(out, cap, "%s", s);
    LuaSetTop(L, top);
    return rc == 0 && std::strcmp(out, "ok") == 0;
  } __except (SehNote(GetExceptionInformation(), "alias")) {
    return false;
  }
}

// The private table's shaperId and itemGUID (light userdata: an object id and the item's asset id), then
// onItemPurchased(_PRIVATETHREADTABLE) through GearsLua_CallFunction on the instance's thread. 1 called, 0 the call
// failed (a Lua error: GearsLua reports it), -1 no thread.
int SehCall(void* sim, std::uint32_t instId, std::uint32_t shaper, std::uint32_t item) {
  __try {
    void* in = GetInstance(instId);
    if (!in || Field<unsigned char>(in, inst::kEndPending)) return -1;
    void* th = GetThread(static_cast<char*>(sim) + sim::kLua, Field<unsigned>(in, inst::kThreadId));
    if (!th) return -1;
    void* L = Field<void*>(th, kThreadLuaState);
    if (!L) return -1;
    const int top = LuaGetTop(L);
    LuaPushString(L, "_PRIVATETHREADTABLE");
    LuaGetTable(L, kLuaGlobalsIndex);
    if (LuaType(L, -1) != 5) {  // LUA_TTABLE
      LuaSetTop(L, top);
      return -1;
    }
    LuaPushString(L, "shaperId");
    LuaPushLightUserdata(L, reinterpret_cast<void*>(static_cast<std::uintptr_t>(shaper)));
    LuaSetTable(L, -3);
    LuaPushString(L, "itemGUID");
    LuaPushLightUserdata(L, reinterpret_cast<void*>(static_cast<std::uintptr_t>(item)));
    LuaSetTable(L, -3);
    LuaSetTop(L, top);
    GearsLuaArg param = {kGearsArgGlobalTable, "_PRIVATETHREADTABLE", {0, 0}};
    GearsLuaArg result = {kGearsArgBool, reinterpret_cast<const void*>(1), {0, 0}};
    ++Field<int>(in, inst::kInUse);  // as LabsAbilities_CallEventFunction: an EndAbility meanwhile is deferred
    const bool ok = CallFunction(th, nullptr, kTable, kOnItemPurchasedKey, in, &param, 1,
                                 static_cast<int>(reinterpret_cast<std::uintptr_t>(&result)), 1);
    --Field<int>(in, inst::kInUse);
    return ok ? 1 : 0;
  } __except (SehNote(GetExceptionInformation(), "onItemPurchased")) {
    return 0;
  }
}

bool SehBuyerPos(std::uint32_t creature, float* pos) {
  __try {
    void* o = creature ? GetObjectById(creature) : nullptr;
    if (!o) return false;
    std::memcpy(pos, static_cast<char*>(o) + gobj::kPosition, 12);
    return true;
  } __except (SehNote(GetExceptionInformation(), "buyer")) {
    return false;
  }
}

const char* SehAssetName(std::uint32_t id) {
  __try {
    void* a = FindAssetById(id);
    const char* n = a ? Field<const char*>(a, asset::kName) : nullptr;
    return n ? n : "?";
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return "?";
  }
}

}  // namespace

bool Enabled() { return g_configured && g_opt.mode != 0; }

void RegisterLua(void* L, void* sim) {
  Configure();
  if (!g_opt.mode || !L) return;
  g_sim = sim;
  bool ok = false;
  __try {
    RegisterNamespace(L, "nBattlegrounds", kRegs, 2);
    ok = true;
  } __except (SehNote(GetExceptionInformation(), "RegisterNamespace")) {
  }
  Log("shop: nBattlegrounds.GetItemFxProjectile, GetItemFxOnHit over the stubs: %s", ok ? "ok" : "FAILED");
}

void OnItemPurchased(void* sim, unsigned index, std::uint32_t creature, unsigned team, std::uint32_t itemId,
                     const char* source) {
  Configure();
  if (!g_opt.mode || !sim) return;
  g_sim = sim;
  ++g_st.purchases;
  if (!g_aliased) {
    char why[160];
    g_aliased = SehAlias(sim, why, sizeof why);
    Log("shop: %s[%d] = %s.onItemPurchased: %s", kTable, kOnItemPurchasedKey, kTable, g_aliased ? "ok" : why);
    if (!g_aliased) {
      ++g_st.callFailed;
      return;
    }
  }
  if (!SehArchonsValid()) ScanArchons(sim);
  float pos[3] = {};
  const bool havePos = SehBuyerPos(creature, pos);
  // the archons to call: the buyer team's nearest one (mode 1) or all of the team's (mode 2)
  std::vector<const Archon*> to;
  const Archon* best = nullptr;
  float bestD = 0.f;
  for (const Archon& a : g_archons) {
    if (a.team != team) continue;
    if (g_opt.mode == 2) {
      to.push_back(&a);
      continue;
    }
    const float dx = a.pos[0] - pos[0], dy = a.pos[1] - pos[1];
    const float d = havePos ? std::sqrt(dx * dx + dy * dy) : 0.f;
    if (!best || d < bestD) {
      best = &a;
      bestD = d;
    }
  }
  if (best) to.push_back(best);
  if (to.empty()) {
    ++g_st.noArchon;
    if (LogBudget())
      Log("shop: %s player %u (team %u) bought %08X (%s): no archon of its team (%zu archons known)", source, index, team,
          itemId, SehAssetName(itemId), g_archons.size());
    return;
  }
  for (const Archon* a : to) {
    const int rc = SehCall(sim, a->inst, creature, itemId);
    if (rc == 1) ++g_st.calls;
    else ++g_st.callFailed;
    if (LogBudget()) {
      const float dx = a->pos[0] - pos[0], dy = a->pos[1] - pos[1];
      Log("shop: %s player %u's creature %08X (team %u) bought %08X (%s): %s.onItemPurchased on archon %08X %s "
          "(instance %08X, %.1f away) -> %s",
          source, index, creature, team, itemId, SehAssetName(itemId), kTable, a->object, a->name, a->inst,
          std::sqrt(dx * dx + dy * dy), rc == 1 ? "called" : rc == 0 ? "FAILED (a Lua error?)" : "NO THREAD");
    }
  }
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size,
                "purchases %u, onItemPurchased calls %u (failed %u, no archon %u), archons %zu (scans %u), GetItemFx "
                "calls %u (named %u, empty %u), exceptions %u",
                g_st.purchases, g_st.calls, g_st.callFailed, g_st.noArchon, g_archons.size(), g_st.scans, g_st.fxCalls,
                g_st.fxNamed, g_st.fxEmpty, g_st.exceptions);
}

}  // namespace shop
