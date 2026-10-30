// A player's item passives across its death and its respawn, engine-free (gms/engine/itempassives.cpp carries it out
// on the server simulator; the death path calls it from gms/engine/death.cpp).
//
// What the client build has: an item's passives start only at a purchase (LabsPlayer_OnItemAdded 0x00912950, server
// only), which keeps their instance ids in the item slot (cLabsPlayer mItems[s] +8, four ids; LabsItemSlot_Add-
// PassiveInstance 0x00910E10 takes the first 0 entry), and end at its removal (LabsPlayer_OnItemRemoved 0x00913C50,
// which finds them through LabsItemSlot_FindPassiveInstance 0x00910EF0: a 0 entry is skipped, an id whose instance is
// gone asserts LabsPlayer.cpp 278 "Item slot is holding onto stale passive instance id"). Nothing activates them again
// after a death. 20 of the 77 item passives end OnAgentDeath by their scripts' deactivation type (Purity's and
// Devotion's heal, the Legendary auras, Conviction/Faith/Retribution's regeneration, ...), so the rule here is the
// scripts': such a passive stops while its holder is dead and starts again at the respawn [assumed: the stripped
// server shell re-activated them at the respawn, where GameObject_OnRevived calls its compiled-out player hook
// 0x009A3F00, as for the shaper's passive].
//
//   - While dead (after the death path has ended the victim's OnAgentDeath modifiers, then after every scheduler run
//     until the respawn): a slot entry whose instance is gone or ending is cleared (0), so a sale or an upgrade while
//     dead finds no dead id; an OnAgentDeath passive running on the dead creature (one a purchase while dead started:
//     OnItemAdded doesn't ask whether its holder lives) is ended and cleared the same way. A passive that doesn't end
//     at death (Might's kill count, Retribution's store, Faith's shield: OnAgentDestroyed) is never touched.
//   - At the respawn (after the resurrect and the shaper's passive): each slot's passives that end OnAgentDeath and that
//     the slot no longer holds live are activated again as OnItemAdded does at a purchase, in slot order:
//       Default (0), Stacks (4), StacksAndCasterUnique (5): one activation per slot, at the slot's rank (a second
//         slot's activation adds a stack to the first one's instance and returns its id, so two Purities heal 2 x 30);
//       Unique (1): one instance at the highest rank among the slots that hold the passive (OnItemAdded ends a lower
//         rank's instance and moves the id), its id written into each of them;
//       any other type: none (OnItemAdded asserts "Unsupported ability activation type for item passive").
//     The id goes into the slot's first free entry; none is free: not activated (OnItemAdded would assert "Too many
//     passives on item").
// The scripts' own state of a death-ended passive goes with its instance, as the deactivation type says: a stack count
// that counts the item copies comes back with the per-slot activations; cooldown timers kept on the holder
// (Purity's modifier_Item_Advanced_HealNearbyAlly_Cooldown) run on through the death.
#pragma once

#include <array>
#include <cstdint>
#include <vector>

#include "pipeline.h"

namespace gms::damage::items {

constexpr int kSlots = 11;      // cLabsPlayer mItems[11]
constexpr int kIdsPerSlot = 4;  // mItems[s] +8..+0x14
// nActivationType (ServerData Lua/GlobalDefinitions.lua), cStaticAbility +0x424
constexpr int kActDefault = 0, kActUnique = 1, kActStacks = 4, kActStacksAndCasterUnique = 5;

// One entry of the item data's Passive[] (+0x78: {GUID +0, rank +8}) with its static ability's activation (+0x424) and
// deactivation (+0x428) types.
struct PassiveDef {
  std::uint32_t guid = 0;
  int rank = 1;
  int activation = kActDefault;
  unsigned deactivation = 0;
};

// One of a slot's four instance ids, as the slot holds it: its instance's static GUID and deactivation type, and
// whether it lives (found and not ending). id 0 = a free entry.
struct HeldId {
  std::uint32_t id = 0;
  std::uint32_t guid = 0;
  unsigned deactivation = 0;
  bool live = false;
};

struct SlotView {
  bool item = false;
  std::vector<PassiveDef> passives;
  std::array<HeldId, kIdsPerSlot> held{};
};
using Inventory = std::array<SlotView, kSlots>;

inline bool EndsAtDeath(unsigned deactivation) { return (deactivation & kDeactOnAgentDeath) != 0; }

// ---- while dead ----------------------------------------------------------------------------------------------------
struct DeadStep {
  int slot = 0, k = 0;
  std::uint32_t id = 0;
  bool end = false;  // end the instance first (once per id, at its first entry); always clear the entry
};
inline std::vector<DeadStep> PlanWhileDead(const Inventory& inv) {
  std::vector<DeadStep> out;
  std::vector<std::uint32_t> ended;
  for (int s = 0; s < kSlots; ++s)
    for (int k = 0; k < kIdsPerSlot; ++k) {
      const HeldId& h = inv[s].held[k];
      if (!h.id) continue;
      if (!h.live) {
        out.push_back({s, k, h.id, false});
        continue;
      }
      if (!EndsAtDeath(h.deactivation)) continue;
      bool first = true;
      for (std::uint32_t e : ended) first = first && e != h.id;
      if (first) ended.push_back(h.id);
      out.push_back({s, k, h.id, first});
    }
  return out;
}

// ---- at the respawn ------------------------------------------------------------------------------------------------
enum class StepKind {
  kClear,        // a dead id left in the entry: cleared
  kActivate,     // LabsAbilities_ActivateFromDefinition(guid, creature, creature, 0, rank, zero vector, 0) into entry k
  kShare,        // a Unique passive's instance (activated by an earlier step, or live in another slot) into entry k
  kFull,         // no free entry in the slot: not activated
  kUnsupported,  // an activation type OnItemAdded refuses: not activated
};
struct Step {
  StepKind kind = StepKind::kClear;
  int slot = 0, k = 0;
  std::uint32_t guid = 0;
  int rank = 1;
  int activation = kActDefault;
};

namespace detail {
inline bool HoldsLive(const SlotView& s, std::uint32_t guid) {
  for (const HeldId& h : s.held)
    if (h.id && h.live && h.guid == guid) return true;
  return false;
}
inline int FreeEntry(const std::array<bool, kIdsPerSlot>& used) {
  for (int k = 0; k < kIdsPerSlot; ++k)
    if (!used[k]) return k;
  return -1;
}
}  // namespace detail

inline std::vector<Step> PlanRespawn(const Inventory& inv) {
  std::vector<Step> out;
  std::array<std::array<bool, kIdsPerSlot>, kSlots> used{};
  for (int s = 0; s < kSlots; ++s)
    for (int k = 0; k < kIdsPerSlot; ++k) {
      const HeldId& h = inv[s].held[k];
      if (!h.id) continue;
      if (h.live) {
        used[s][k] = true;
      } else {
        Step c;
        c.kind = StepKind::kClear;
        c.slot = s;
        c.k = k;
        c.guid = h.guid;
        out.push_back(c);
      }
    }
  std::vector<std::uint32_t> uniqueDone;  // Unique GUIDs activated or live: the later slots share the instance
  for (const SlotView& v : inv)
    for (const PassiveDef& p : v.passives)
      if (p.activation == kActUnique && detail::HoldsLive(v, p.guid)) uniqueDone.push_back(p.guid);
  for (int s = 0; s < kSlots; ++s) {
    const SlotView& v = inv[s];
    if (!v.item) continue;
    for (const PassiveDef& p : v.passives) {
      if (!EndsAtDeath(p.deactivation) || detail::HoldsLive(v, p.guid)) continue;
      Step st;
      st.slot = s;
      st.guid = p.guid;
      st.rank = p.rank;
      st.activation = p.activation;
      if (p.activation != kActDefault && p.activation != kActUnique && p.activation != kActStacks &&
          p.activation != kActStacksAndCasterUnique) {
        st.kind = StepKind::kUnsupported;
        out.push_back(st);
        continue;
      }
      const int k = detail::FreeEntry(used[s]);
      if (k < 0) {
        st.kind = StepKind::kFull;
        out.push_back(st);
        continue;
      }
      st.k = k;
      if (p.activation == kActUnique) {
        bool done = false;
        for (std::uint32_t g : uniqueDone) done = done || g == p.guid;
        if (done) {
          st.kind = StepKind::kShare;
        } else {
          st.kind = StepKind::kActivate;
          for (const SlotView& o : inv)  // the highest rank among the slots holding it
            if (o.item)
              for (const PassiveDef& q : o.passives)
                if (q.guid == p.guid && q.rank > st.rank) st.rank = q.rank;
          uniqueDone.push_back(p.guid);
        }
      } else {
        st.kind = StepKind::kActivate;
      }
      used[s][k] = true;
      out.push_back(st);
    }
  }
  return out;
}

}  // namespace gms::damage::items
