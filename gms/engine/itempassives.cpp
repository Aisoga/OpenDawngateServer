// A player's item passives across its death and its respawn; see itempassives.h and gms/core/damage/itempassives.h.
#include "itempassives.h"

#include <windows.h>

#include <cstdio>
#include <map>
#include <string>
#include <vector>

#include "core/damage/itempassives.h"
#include "game.h"
#include "log.h"

using namespace game;
namespace ip = gms::damage::items;

namespace itempassives {
namespace {

struct Stats {
  unsigned sweeps = 0, cleared = 0, ended = 0;                   // while dead
  unsigned respawns = 0, activated = 0, shared = 0, refused = 0;  // at the respawn
  unsigned full = 0, unsupported = 0, exceptions = 0;
} g_st;

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  if (++g_st.exceptions <= 20)
    Log("EXCEPTION in item passives (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
        reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

constexpr int kMaxDefs = 8;  // per item (the data has 0-3)

// The inventory as read, plain data (no unwinding inside __try).
struct RawSlot {
  const char* item = nullptr;
  bool has = false;
  int defs = 0;
  std::uint32_t guid[kMaxDefs] = {};
  int rank[kMaxDefs] = {};
  int activation[kMaxDefs] = {};
  unsigned deactivation[kMaxDefs] = {};
  const char* defName[kMaxDefs] = {};
  std::uint32_t id[ip::kIdsPerSlot] = {};
  std::uint32_t idGuid[ip::kIdsPerSlot] = {};
  unsigned idDeactivation[ip::kIdsPerSlot] = {};
  bool idLive[ip::kIdsPerSlot] = {};
  const char* idName[ip::kIdsPerSlot] = {};
};
struct RawInventory {
  void* player = nullptr;
  std::uint32_t creature = 0;
  RawSlot slot[ip::kSlots];
};

const char* StaticName(void* st) {
  const char* n = st ? *reinterpret_cast<const char**>(st) : nullptr;
  return n ? n : "?";
}

bool SehRead(void* sim, int index, RawInventory* r) {
  __try {
    char* p = static_cast<char*>(GetPlayerByIndex(sim, static_cast<unsigned>(index)));
    if (!p) return false;
    r->player = p;
    r->creature = Field<std::uint32_t>(p, player::kCurrentCreature);
    for (int s = 0; s < ip::kSlots; ++s) {
      RawSlot& out = r->slot[s];
      char* entry = p + player::kItems + s * player::kItemSize;
      char* asset = Field<char*>(entry, 0);
      char* data = asset ? Field<char*>(asset, asset::kData) : nullptr;
      if (data) {
        out.has = true;
        out.item = Field<const char*>(asset, asset::kName);
        char* ps = Field<char*>(data, itemdata::kPassives);
        const int n = Field<int>(data, itemdata::kPassives + 4);
        for (int k = 0; ps && k < n && out.defs < kMaxDefs; ++k) {
          const int d = out.defs++;
          out.guid[d] = Field<std::uint32_t>(ps, k * itemdata::kPassiveSize);
          out.rank[d] = Field<int>(ps, k * itemdata::kPassiveSize + 8);
          void* st = FindStaticAbility(out.guid[d]);
          out.activation[d] = st ? Field<int>(st, kStaticActivationType) : -1;
          out.deactivation[d] = st ? Field<unsigned>(st, kStaticDeactivationType) : 0;
          out.defName[d] = StaticName(st);
        }
      }
      for (int k = 0; k < ip::kIdsPerSlot; ++k) {
        const std::uint32_t id = Field<std::uint32_t>(entry, player::kItemPassives + 4 * k);
        out.id[k] = id;
        if (!id) continue;
        void* in = GetInstance(id);
        void* st = in ? Field<void*>(in, inst::kStatic) : nullptr;
        out.idLive[k] = in && !Field<unsigned char>(in, inst::kEndPending);
        out.idGuid[k] = st ? Field<std::uint32_t>(st, kStaticGuid) : 0;
        out.idDeactivation[k] = st ? Field<unsigned>(st, kStaticDeactivationType) : 0;
        out.idName[k] = st ? StaticName(st) : "(gone)";
      }
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "read the item slots")) {
    return false;
  }
}

ip::Inventory ToInventory(const RawInventory& r) {
  ip::Inventory inv;
  for (int s = 0; s < ip::kSlots; ++s) {
    const RawSlot& rs = r.slot[s];
    ip::SlotView& v = inv[s];
    v.item = rs.has;
    for (int d = 0; d < rs.defs; ++d) {
      ip::PassiveDef p;
      p.guid = rs.guid[d];
      p.rank = rs.rank[d] > 0 ? rs.rank[d] : 1;
      p.activation = rs.activation[d];
      p.deactivation = rs.deactivation[d];
      v.passives.push_back(p);
    }
    for (int k = 0; k < ip::kIdsPerSlot; ++k) {
      v.held[k].id = rs.id[k];
      v.held[k].guid = rs.idGuid[k];
      v.held[k].deactivation = rs.idDeactivation[k];
      v.held[k].live = rs.idLive[k];
    }
  }
  return inv;
}

bool SehWriteEntry(void* player, int slot, int k, std::uint32_t id) {
  __try {
    char* entry = static_cast<char*>(player) + player::kItems + slot * player::kItemSize;
    Field<std::uint32_t>(entry, player::kItemPassives + 4 * k) = id;
    return true;
  } __except (SehNote(GetExceptionInformation(), "write an item slot entry")) {
    return false;
  }
}

bool SehEnd(std::uint32_t id) {
  __try {
    EndAbility(id);
    return true;
  } __except (SehNote(GetExceptionInformation(), "LabsAbilities_EndAbility (an item passive)")) {
    return false;
  }
}

// LabsPlayer_OnItemAdded's call: (passive, creature, creature, no snapshot, the rank, the zero vector, 0). 0 = refused.
std::uint32_t SehActivate(std::uint32_t guid, std::uint32_t creature, int rank) {
  __try {
    return ActivateFromDefinition(guid, creature, creature, 0, rank, 0.f, 0.f, 0.f, 0);
  } __except (SehNote(GetExceptionInformation(), "LabsAbilities_ActivateFromDefinition (an item passive)")) {
    return 0;
  }
}

int SehStacks(std::uint32_t id) {
  __try {
    void* in = id ? GetInstance(id) : nullptr;
    return in ? Field<int>(in, inst::kStackCount) : -1;
  } __except (SehNote(GetExceptionInformation(), "stack count")) {
    return -1;
  }
}

const char* DefName(const RawSlot& s, std::uint32_t guid) {
  for (int d = 0; d < s.defs; ++d)
    if (s.guid[d] == guid) return s.defName[d];
  return "?";
}

}  // namespace

int WhileDead(void* sim, int index, std::uint32_t creature, const char* why) {
  if (!sim || index < 0) return 0;
  static RawInventory r;  // engine thread only
  r = RawInventory{};
  if (!SehRead(sim, index, &r) || r.creature != creature) return 0;
  const std::vector<ip::DeadStep> steps = ip::PlanWhileDead(ToInventory(r));
  if (steps.empty()) return 0;
  ++g_st.sweeps;
  std::string ended, cleared;
  int changed = 0;
  for (const ip::DeadStep& d : steps) {
    const RawSlot& s = r.slot[d.slot];
    char b[160];
    if (d.end) {
      const bool ok = SehEnd(d.id);
      ++g_st.ended;
      std::snprintf(b, sizeof b, " slot %d %s %08X%s", d.slot, s.idName[d.k], d.id, ok ? "" : " (EXCEPTION)");
      ended += b;
    }
    if (SehWriteEntry(r.player, d.slot, d.k, 0)) {
      ++g_st.cleared;
      ++changed;
      std::snprintf(b, sizeof b, " slot %d[%d] %s %08X", d.slot, d.k, s.idName[d.k], d.id);
      cleared += b;
    }
  }
  Log("items: player %d's %08X while dead (%s): item passives ended (running on the dead holder):%s; slot entries "
      "cleared %d:%s",
      index, creature, why, ended.empty() ? " -" : ended.c_str(), changed, cleared.empty() ? " -" : cleared.c_str());
  return changed;
}

void AtRespawn(void* sim, int index) {
  if (!sim || index < 0) return;
  static RawInventory r;  // engine thread only
  r = RawInventory{};
  if (!SehRead(sim, index, &r) || !r.creature) {
    Log("items: player %d at the respawn: no player or creature; the item passives not checked", index);
    return;
  }
  ++g_st.respawns;
  const ip::Inventory inv = ToInventory(r);
  const std::vector<ip::Step> steps = ip::PlanRespawn(inv);
  // the GUIDs' live instances now (for a Unique passive's share), then the ones activated below
  std::map<std::uint32_t, std::uint32_t> live;
  for (int s = 0; s < ip::kSlots; ++s)
    for (int k = 0; k < ip::kIdsPerSlot; ++k)
      if (r.slot[s].id[k] && r.slot[s].idLive[k]) live[r.slot[s].idGuid[k]] = r.slot[s].id[k];
  std::string done;
  int activated = 0, refused = 0, other = 0;
  for (const ip::Step& st : steps) {
    const RawSlot& s = r.slot[st.slot];
    char b[220];
    switch (st.kind) {
      case ip::StepKind::kClear:
        SehWriteEntry(r.player, st.slot, st.k, 0);
        ++g_st.cleared;
        std::snprintf(b, sizeof b, "; slot %d[%d] dead id %08X cleared", st.slot, st.k, s.id[st.k]);
        break;
      case ip::StepKind::kActivate: {
        const std::uint32_t id = SehActivate(st.guid, r.creature, st.rank);
        if (id) {
          SehWriteEntry(r.player, st.slot, st.k, id);
          live[st.guid] = id;
          ++activated;
          ++g_st.activated;
        } else {
          ++refused;
          ++g_st.refused;
        }
        std::snprintf(b, sizeof b, "; slot %d %s %s rank %d -> %s %08X x%d", st.slot, s.item ? s.item : "?",
                      DefName(s, st.guid), st.rank, id ? "activated" : "REFUSED", id, SehStacks(id));
        break;
      }
      case ip::StepKind::kShare: {
        const auto it = live.find(st.guid);
        const std::uint32_t id = it == live.end() ? 0 : it->second;
        if (id) {
          SehWriteEntry(r.player, st.slot, st.k, id);
          ++g_st.shared;
        }
        std::snprintf(b, sizeof b, "; slot %d %s %s shares %08X%s", st.slot, s.item ? s.item : "?",
                      DefName(s, st.guid), id, id ? "" : " (NONE: not written)");
        ++other;
        break;
      }
      case ip::StepKind::kFull:
        ++g_st.full;
        ++other;
        std::snprintf(b, sizeof b, "; slot %d %s %s NOT activated (the slot's four entries are taken)", st.slot,
                      s.item ? s.item : "?", DefName(s, st.guid));
        break;
      case ip::StepKind::kUnsupported:
      default:
        ++g_st.unsupported;
        ++other;
        std::snprintf(b, sizeof b, "; slot %d %s %s NOT activated (activation type %d)", st.slot, s.item ? s.item : "?",
                      DefName(s, st.guid), st.activation);
        break;
    }
    done += b;
  }
  // the passives the death left alone (OnAgentDestroyed ones: Might's count, Retribution's store)
  std::string kept;
  for (int s = 0; s < ip::kSlots; ++s)
    for (int k = 0; k < ip::kIdsPerSlot; ++k)
      if (r.slot[s].id[k] && r.slot[s].idLive[k]) {
        char b[120];
        std::snprintf(b, sizeof b, " slot %d %s %08X", s, r.slot[s].idName[k], r.slot[s].id[k]);
        kept += b;
      }
  Log("items: player %d's creature %08X at the respawn: item passives activated again %d, refused %d, other %d%s; "
      "kept through the death:%s",
      index, r.creature, activated, refused, other, done.empty() ? "; nothing to activate" : done.c_str(),
      kept.empty() ? " -" : kept.c_str());
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size,
                "item passives: while dead %u sweep(s) (cleared %u, ended %u); at %u respawn(s) activated %u, shared %u, "
                "refused %u, full %u, unsupported %u; exceptions %u",
                g_st.sweeps, g_st.cleared, g_st.ended, g_st.respawns, g_st.activated, g_st.shared, g_st.refused,
                g_st.full, g_st.unsupported, g_st.exceptions);
}

}  // namespace itempassives
