// REFUND1: the undo of a purchase in the base, on the server player: see refund.h.
#include "refund.h"

#include <windows.h>

#include <cstdio>
#include <cstring>
#include <string>

#include "core/protocol/commands.h"
#include "game.h"
#include "log.h"

using namespace game;
using gms::refund::Entry;
using gms::refund::Inventory;
using gms::refund::Plan;
using gms::refund::Slot;

namespace refund {
namespace {

bool g_enabled = true;
int g_logLimit = 400;
unsigned g_logged;
gms::refund::Ledger g_ledger;

struct Stats {
  unsigned recorded = 0, notUndoable = 0, undone = 0, failed = 0, stale = 0;
  unsigned clearedLeft = 0, clearedChanged = 0;
  unsigned handedBack = 0, handBackFailed = 0;  // kill counts given back to Might (HandBack)
};
Stats g_st;

bool LogBudget() {
  ++g_logged;
  return g_logged <= static_cast<unsigned>(g_logLimit) || g_logged % 50 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  Log("EXCEPTION in refund (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

const char* SehName(const void* asset) {
  __try {
    const char* n = asset ? Field<const char*>(const_cast<void*>(asset), asset::kName) : nullptr;
    return n ? n : "?";
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return "?";
  }
}

void* SehAsset(std::uint32_t id) {
  __try {
    return id ? FindAssetById(id) : nullptr;
  } __except (SehNote(GetExceptionInformation(), "AssetCatalog_FindById")) {
    return nullptr;
  }
}

bool SehIsSpell(void* asset) {
  __try {
    return asset && ItemIsSpell(asset);
  } __except (SehNote(GetExceptionInformation(), "Item_IsSpell")) {
    return false;
  }
}

bool SehIsCheat(void* asset) {
  __try {
    void* data = asset ? Field<void*>(asset, asset::kData) : nullptr;
    return data && Field<std::uint8_t>(data, itemdata::kCheatItem) != 0;
  } __except (SehNote(GetExceptionInformation(), "Item_IsCheatItem")) {
    return true;  // unreadable: not undoable
  }
}

// "Advanced_Motion.Item x1 (path Basic_Time.Item)" or "empty"
std::string SlotText(const Slot& s) {
  if (!s.item) return "empty";
  char buf[200];
  std::snprintf(buf, sizeof buf, "%s x%d", SehName(SehAsset(s.item)), s.count);
  std::string t = buf;
  if (s.path[0] || s.path[1]) {
    t += " (path ";
    t += s.path[0] ? SehName(reinterpret_cast<const void*>(static_cast<std::uintptr_t>(s.path[0]))) : "-";
    if (s.path[1]) {
      t += ", ";
      t += SehName(reinterpret_cast<const void*>(static_cast<std::uintptr_t>(s.path[1])));
    }
    t += ")";
  }
  return t;
}

// The undo's engine steps (refund.h). False on an exception (the steps up to it stay done).
bool SehUndo(void* player, void* asset, unsigned slot, void* readd, bool writePath, const std::uint32_t* path,
             float refund, float* goldBefore, float* goldAfter) {
  __try {
    *goldBefore = Field<float>(player, player::kAvailableGold);
    SellItem(player, asset, slot);
    if (readd) PurchaseItem(player, readd, slot, 0);
    if (writePath) {
      char* it = static_cast<char*>(player) + player::kItems + slot * player::kItemSize;
      Field<std::uint32_t>(it, player::kItemPath) = path[0];
      Field<std::uint32_t>(it, player::kItemPath + 4) = path[1];
    }
    SetAvailableGold(player, *goldBefore + refund);
    *goldAfter = Field<float>(player, player::kAvailableGold);
    return true;
  } __except (SehNote(GetExceptionInformation(), "undo")) {
    return false;
  }
}

// ---- the kill count's hand-back (gms::refund::HandBack) ------------------------------------------------------------

// The creature's live (not ending) modifier instance whose script is `script`: its id, and its stack count in *stacks.
// 0 if none.
std::uint32_t SehFindModifier(void* object, const char* script, int* stacks) {
  __try {
    char* c = object ? Field<char*>(object, obj::kAbilityData) : nullptr;
    if (!c) return 0;
    for (const std::uint32_t* p = Field<const std::uint32_t*>(c, container::kModifiersBegin);
         p && p != Field<const std::uint32_t*>(c, container::kModifiersEnd); ++p) {
      void* in = GetInstance(*p);
      const char* st = in ? Field<const char*>(in, inst::kStatic) : nullptr;
      const char* n = st ? *reinterpret_cast<const char* const*>(st) : nullptr;
      if (!n || std::strcmp(n, script) != 0 || Field<unsigned char>(in, inst::kEndPending)) continue;
      if (stacks) *stacks = Field<int>(in, inst::kStackCount);
      return *p;
    }
    return 0;
  } __except (SehNote(GetExceptionInformation(), "modifier scan")) {
    return 0;
  }
}

// <passive>[kHandBackKey] = the hand-back function, on the server's lua_State (once per passive and state). The body is
// the passive's own code for a count: Might's Deactivate (RequestModifier, SetStackCount) and its kill branch (the
// bonus attribute modifiers, kept in the private table as healthModRemoval / damageModRemoval).
const char kHandBackChunk[] =
    "local t = %s\n"
    "if type(t) ~= 'table' then return 'no table' end\n"
    "if t.killCountMod == nil or t.healthPerMinion == nil or t.powerPerMinion == nil then return 'not a kill counter' end\n"
    "t[%d] = function(p)\n"
    "  local v = %s\n"
    "  local n = p.dgsimHandBack\n"
    "  p.dgsimHandBack = nil\n"
    "  if n == nil or n <= 0 or p.myObjectID == nil then return false end\n"
    "  local params = nTable.CreateTable()\n"
    "  nTable.SetTableValue(params, 'duration', 0)\n"
    "  local id = nModifier.RequestModifier(p.myObjectID, p.myObjectID, v.killCountMod, p.snapshot, p.rank, kZeroVector, "
    "params)\n"
    "  nModifier.SetStackCount(id, n)\n"
    "  p.countModID = nModifier.GetFirstModifierByGUID(p.myObjectID, v.killCountMod)\n"
    "  p.currentStackCount = nModifier.GetStackCount(p.countModID)\n"
    "  p.bonusHealthToAdd = v.healthPerMinion * p.currentStackCount\n"
    "  p.maxHealth = nGameObject.GetMaxHitPoints(p.myObjectID)\n"
    "  p.currentHealth = nGameObject.GetHitPoints(p.myObjectID)\n"
    "  p.noHealing = p.currentHealth < p.maxHealth\n"
    "  if p.healthModRemoval ~= nil then\n"
    "    nAttribute.UpdateAttributeModifier(p.myObjectID, p.healthModRemoval, p.bonusHealthToAdd, p.noHealing)\n"
    "  elseif p.noHealing then\n"
    "    p.healthModRemoval = nAttribute.AddHealthModifierWithoutHealing(p.myObjectID, p.bonusHealthToAdd, "
    "nAttributeOp.kAdd)\n"
    "  else\n"
    "    p.healthModRemoval = nAttribute.AddAttributeModifier(p.myObjectID, nAttributeType.MaxHealth, "
    "p.bonusHealthToAdd, nAttributeOp.kAdd)\n"
    "  end\n"
    "  p.bonusPowerToAdd = v.powerPerMinion * p.currentStackCount\n"
    "  if p.damageModRemoval ~= nil then\n"
    "    nAttribute.UpdateAttributeModifier(p.myObjectID, p.damageModRemoval, p.bonusPowerToAdd)\n"
    "  else\n"
    "    p.damageModRemoval = nAttribute.AddAttributeModifier(p.myObjectID, nAttributeType.Power, p.bonusPowerToAdd, "
    "nAttributeOp.kAdd)\n"
    "  end\n"
    "  return true\n"
    "end\n"
    "return 'ok'\n";

void* g_aliasedState;  // the lua_State the alias was made on
std::string g_aliasedPassive;

bool SehAliasChunk(void* L, const char* chunk, std::size_t n, char* out, unsigned cap) {
  __try {
    out[0] = 0;
    const int top = LuaGetTop(L);
    int rc = LuaLoadBuffer(L, chunk, static_cast<unsigned>(n), "=dgsim_refund_handback");
    if (rc == 0) rc = LuaPcall(L, 0, 1, 0);
    const char* s = LuaToString(L, -1, nullptr);
    if (s) std::snprintf(out, cap, "%s", s);
    LuaSetTop(L, top);
    return rc == 0 && std::strcmp(out, "ok") == 0;
  } __except (SehNote(GetExceptionInformation(), "hand-back alias")) {
    return false;
  }
}

void* SehLuaState(void* sim) {
  __try {
    return Field<void*>(static_cast<char*>(sim) + sim::kLua, luasim::kLuaState);
  } __except (SehNote(GetExceptionInformation(), "lua_State")) {
    return nullptr;
  }
}

bool AliasHandBack(void* sim, const char* passive, char* why, unsigned cap) {
  void* L = SehLuaState(sim);
  if (!L) {
    std::snprintf(why, cap, "no lua_State");
    return false;
  }
  if (L == g_aliasedState && g_aliasedPassive == passive) return true;
  std::string chunk(sizeof kHandBackChunk + 256, '\0');
  const int len = std::snprintf(&chunk[0], chunk.size(), kHandBackChunk, passive, kHandBackKey, passive);
  if (len <= 0 || static_cast<std::size_t>(len) >= chunk.size()) {
    std::snprintf(why, cap, "chunk too long");
    return false;
  }
  if (!SehAliasChunk(L, chunk.data(), static_cast<std::size_t>(len), why, cap)) return false;
  g_aliasedState = L;
  g_aliasedPassive = passive;
  return true;
}

// p.dgsimHandBack = count in the instance's private table, then <passive>[kHandBackKey](_PRIVATETHREADTABLE) on its
// thread. 1 called, 0 the call failed (a Lua error: GearsLua reports it), -1 no thread.
int SehCallHandBack(void* sim, std::uint32_t instId, const char* passive, int count) {
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
    LuaPushString(L, "dgsimHandBack");
    LuaPushNumber(L, static_cast<float>(count));
    LuaSetTable(L, -3);
    LuaSetTop(L, top);
    GearsLuaArg param = {kGearsArgGlobalTable, "_PRIVATETHREADTABLE", {0, 0}};
    GearsLuaArg result = {kGearsArgBool, reinterpret_cast<const void*>(1), {0, 0}};
    ++Field<int>(in, inst::kInUse);  // as LabsAbilities_CallEventFunction: an EndAbility meanwhile is deferred
    const bool ok = CallFunction(th, nullptr, passive, kHandBackKey, in, &param, 1,
                                 static_cast<int>(reinterpret_cast<std::uintptr_t>(&result)), 1);
    --Field<int>(in, inst::kInUse);
    return ok ? 1 : 0;
  } __except (SehNote(GetExceptionInformation(), "hand-back call")) {
    return 0;
  }
}

// After the undo's steps: the restored passive gets `count` (read from the Legendary before the steps). Appends what
// happened to *note; false on a failure.
bool GiveCountBack(void* sim, void* object, const gms::refund::HandBack& h, int count, std::string* note) {
  char buf[400];
  bool ok = true;
  if (count <= 0) {
    std::snprintf(buf, sizeof buf, "; kill count: %s held none, nothing to hand back", h.sourceStacks);
    *note += buf;
    return true;
  }
  char why[96] = "";
  const std::uint32_t inst = SehFindModifier(object, h.passive, nullptr);
  int rc = -2, got = -1;
  if (!inst) {
    std::snprintf(why, sizeof why, "the restored passive has no live instance");
  } else if (AliasHandBack(sim, h.passive, why, sizeof why)) {
    rc = SehCallHandBack(sim, inst, h.passive, count);
    if (!SehFindModifier(object, h.targetStacks, &got)) got = 0;
  }
  ok = rc == 1 && got == count;
  if (ok) ++g_st.handedBack;
  else ++g_st.handBackFailed;
  const char* call = rc == 1 ? "called" : rc == 0 ? "the call FAILED" : rc == -1 ? "NO THREAD" : "not called";
  std::snprintf(buf, sizeof buf, "; kill count handed back: %s x%d -> %s x%d (%s%s%s)%s", h.sourceStacks, count,
                h.targetStacks, got, call, why[0] ? ": " : "", why, ok ? "" : "; MISMATCH: the count differs");
  *note += buf;
  return ok;
}

}  // namespace

void Configure(bool enabled, int logLimit) {
  g_enabled = enabled;
  g_logLimit = logLimit;
  Log("refund: [host] item_undo %d (a sale in the base after a purchase undoes it), item_undo_log_limit %d", enabled,
      logLimit);
}

bool Enabled() { return g_enabled; }

bool Any(unsigned index) { return g_enabled && g_ledger.Any(index); }

bool ReadInventory(void* player, Inventory* out) {
  __try {
    for (unsigned s = 0; s < player::kItemSlots && s < static_cast<unsigned>(gms::refund::kSlots); ++s) {
      char* it = static_cast<char*>(player) + player::kItems + s * player::kItemSize;
      void* a = Field<void*>(it, 0);
      out->slot[s].item = a ? Field<std::uint32_t>(a, asset::kId) : 0;
      out->slot[s].count = Field<std::int32_t>(it, player::kItemCount);
      out->slot[s].path[0] = Field<std::uint32_t>(it, player::kItemPath);
      out->slot[s].path[1] = Field<std::uint32_t>(it, player::kItemPath + 4);
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "ReadInventory")) {
    return false;
  }
}

bool TryUndo(void* sim, void* player, void* object, unsigned index, void* asset, std::uint32_t item, unsigned slot,
             char* text, std::size_t n) {
  if (!g_enabled || slot >= static_cast<unsigned>(gms::refund::kSlots) || !g_ledger.Any(index)) return false;
  Inventory before;
  if (!ReadInventory(player, &before)) return false;
  const std::size_t depth = g_ledger.Depth(index, slot);
  const Entry* found = g_ledger.UndoFor(index, slot, item, before.slot[slot]);
  if (!found) {
    if (depth) {
      ++g_st.stale;
      Log("refund: player %u slot %u: history of %u dropped (the slot is %s, not what the last purchase left); a "
          "plain sale",
          index, slot, static_cast<unsigned>(depth), SlotText(before.slot[slot]).c_str());
    }
    return false;
  }
  const Entry e = *found;
  const Plan p = gms::refund::PlanUndo(e, before.slot[slot]);
  void* readd = p.readd ? SehAsset(p.readd) : nullptr;
  if (!p.valid || (p.readd && !readd)) {
    ++g_st.stale;
    g_ledger.Clear(index, slot);
    Log("refund: player %u slot %u: no undo (%s); a plain sale", index, slot,
        !p.valid ? p.why : "the component's asset wasn't found");
    return false;
  }
  // the kill count the Legendary took over, read before its removal deletes it
  int handCount = 0;
  if (p.handBack && !SehFindModifier(object, p.handBack->sourceStacks, &handCount)) handCount = 0;
  if (p.handBack) handCount = gms::refund::HandBackCount(*p.handBack, handCount);
  float goldBefore = 0, goldAfter = 0;
  const bool ran = SehUndo(player, asset, slot, readd, p.writePath, p.path, static_cast<float>(p.refund), &goldBefore,
                           &goldAfter);
  Inventory after;
  const bool read = ReadInventory(player, &after);
  const bool ok = ran && read && gms::refund::UndoTookEffect(e, after.slot[slot], goldBefore, goldAfter);
  g_ledger.Pop(index, slot);
  if (ok) {
    ++g_st.undone;
  } else {
    ++g_st.failed;
    g_ledger.Clear(index, slot);
  }
  std::string hand;
  if (p.handBack && ran) GiveCountBack(sim, object, *p.handBack, handCount, &hand);
  std::snprintf(text, n, "undone (%s): %s -> %s, refund %d: gold %.1f -> %.1f; history left %u%s%s", p.why,
                SlotText(before.slot[slot]).c_str(), read ? SlotText(after.slot[slot]).c_str() : "?", p.refund,
                goldBefore, goldAfter, static_cast<unsigned>(g_ledger.Depth(index, slot)),
                ok ? "" : !ran ? "; MISMATCH: an exception in the steps" : "; MISMATCH: the slot or the gold differ",
                hand.c_str());
  return true;
}

void AfterTransaction(void* player, unsigned index, std::uint8_t type, void* asset, unsigned slot, int cost,
                      const Inventory& before, const Inventory& after) {
  (void)player;
  if (!g_enabled || index >= static_cast<unsigned>(gms::refund::kMaxPlayers)) return;
  if (type == gms::kItemPurchase) {
    gms::refund::PurchaseFacts f;
    f.spell = SehIsSpell(asset);
    f.cheat = !f.spell && SehIsCheat(asset);
    if (g_ledger.OnPurchase(index, slot, before, after, cost, f)) {
      ++g_st.recorded;
      if (LogBudget())
        Log("refund: player %u slot %u: purchase recorded (cost %d): %s -> %s; history %u", index, slot, cost,
            SlotText(before.slot[slot]).c_str(), SlotText(after.slot[slot]).c_str(),
            static_cast<unsigned>(g_ledger.Depth(index, slot)));
    } else {
      ++g_st.notUndoable;
      if (LogBudget())
        Log("refund: player %u slot %u: purchase of %s not undoable (%s)", index, slot, SehName(asset),
            f.spell ? "a spell" : f.cheat ? "a cheat item" : "it didn't change exactly its slot");
    }
    return;
  }
  const int n = g_ledger.OnOtherChange(index, before, after);
  if (n) {
    g_st.clearedChanged += static_cast<unsigned>(n);
    if (LogBudget())
      Log("refund: player %u: %s changed a slot: %d history entr%s cleared", index, gms::ItemTransactionName(type), n,
          n == 1 ? "y" : "ies");
  }
}

void OnPosition(unsigned index, bool alive, bool inReach, float distance) {
  if (!g_enabled) return;
  const int n = g_ledger.OnPosition(index, alive, inReach);
  if (!n) return;
  g_st.clearedLeft += static_cast<unsigned>(n);
  if (LogBudget())
    Log("refund: player %u alive and out of the shop's reach (%.2f from the base object): %d history entr%s cleared",
        index, distance, n, n == 1 ? "y" : "ies");
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size,
                "undo: recorded %u, not undoable %u, undone %u (failed %u), plain sales after a stale history %u, "
                "cleared by leaving %u, by a change %u; kill counts handed back %u (failed %u)",
                g_st.recorded, g_st.notUndoable, g_st.undone, g_st.failed, g_st.stale, g_st.clearedLeft,
                g_st.clearedChanged, g_st.handedBack, g_st.handBackFailed);
}

}  // namespace refund
