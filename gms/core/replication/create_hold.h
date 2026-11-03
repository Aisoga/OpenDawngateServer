// SEC5: a new object's create held from the players whose team doesn't see it (gms/core, engine-free). No game
// addresses: the engine side
// (gms/engine/gms_host.cpp ReplicationStep and SendWorld) passes per scanned object its id, its create dependencies
// (core/replication/create_order.h), the players that don't know it yet, the players whose team sees it this tick
// (the vision model's answer through gms::vision::PlayersForTeams: its own team and the spectators always) and whether
// it is exempt.
//
// Until SEC5 every new object was created (8E ... 8F, with its position, type, owner and state) for every player with
// the world and fogged in the same tick (93), so a modified client learned of every ward, summon, pet, projectile,
// minion and creep the moment it appeared, wherever it was. Now a player whose team doesn't see a held object gets
// nothing about it: no create, so its known bit (network component +0xE) stays clear, and every other stream follows
// that bit (the deltas, the kind-2 messages, the fog transitions 93/94/95/A3, the modifier ends, the deletion 91). On
// the first tick its team sees the object the create pass finds the bit still missing and sends the full create then,
// with the current state (BuildCreate always writes the whole state), instead of the fog exit's 94/full state/95. An
// object that dies unseen is never announced to that player.
//
// Exempt (always created, as before): the players' creatures (the scoreboard, the HUD, PlayerCharacterDeploy B3 and
// A9's mCurrentCreatureId name them: B3's reader GameClient_OnGmsPlayerCharacterDeploy 0x006BA460 asserts
// "localObjectID != kObjIDNone" for an unknown creature; they start at the public spawn points and a respawn resurrects
// the same object), and the level's own objects (sourceMarkerKey.markerId != 0: Bindings, nubs, shops, gold mines, the
// camps' spawn points that carry the minimap's camp icons; every client has their place from its level file). Objects
// the vision model never fogs (towers, obstacles, titans, gold mines, objects without a line-of-sight record or without
// Noun.fowControlsObjectVisibility) are seen by every team anyway, so they are created for everyone without a rule.
//
// Dependencies: the client maps a tObjID it doesn't know to 0 (GameClient_ReflectObjectIdField 0x006B7A00, no assert),
// so a create that names an owner, a locomotion parent or a projectile's target the player doesn't have would leave the
// client's object without it (FARISW's hologram, ABILVFX's projectile without a target). When a held object is the
// dependency of an object created for a player now, it is created for that player too, first (CreateOrder), and the
// fog step fogs it in the same tick (93; its 92/B1 left out, FOGSOUND): "pulled in", today's behaviour for that one
// object. The plan propagates through chains (a dependency's own dependencies).
#pragma once

#include <cstddef>
#include <cstdint>
#include <iterator>
#include <unordered_map>
#include <vector>

namespace gms {

constexpr uint16_t kCreateSeesUnknown = 0xFFFF;  // no vision answer for the object this tick: never held

// SEC5b: the NPC types (nNPCType, the NonPlayerClass data's +0x68) whose objects the game announces to every team by
// name, so every client must have them: the parasite (8; modifier_Parasite_Passive's YouKilled/EnemyKilledParasite1-3
// to both teams with the parasite as the event's object, defs with pingMinimap: ClientFeedback_UpdateInstances
// 0x00665F20 asserts "pObject", ClientFeedback.cpp 1831, for an object the client doesn't have), the Bindings (9;
// targeting beam alerts, the kill lines), the Guardian (13), the Cores (nubs, 14; ability_BaseNub_Death's destroyed,
// respawning, respawned to all players) and the Locus (15). The structures are level objects or never fogged anyway;
// the parasite is the one a camp spawns at run time. A tally of every nClientFeedback.SendEventTo* call in ServerData:
// the others name the receiver's own creature or an object of the receiver's own team (wards, the recall).
constexpr int kNpcTypeParasite = 8;
inline bool AnnouncedNpcType(int npcType) {
  return npcType == kNpcTypeParasite || npcType == 9 || npcType == 13 || npcType == 14 || npcType == 15;
}

// SEC5b: the objects a ClientFeedbackEvent (A5) names. Its body (after the wire byte) is the reflected
// ClientFeedbackEvent: a one-byte member mask (7 members), then the set members in order: 0 eventId u32, 1 objectId
// tObjID, 2 targetId tObjID, 3 uintVal, 4 vectorVal, 5 eventDef, 6 objectNoun (e.g. "a5 2b d17fddb5 2e0177a6 01000000
// d17fddb5": members 0, 1, 3, 5). False if the body is too short for the members before the target.
inline bool FeedbackEventObjects(const uint8_t* body, std::size_t n, uint32_t* object, uint32_t* target) {
  *object = 0;
  *target = 0;
  if (!body || n < 1) return false;
  const uint8_t mask = body[0];
  std::size_t off = 1;
  auto u32 = [&](uint32_t* v) {
    if (off + 4 > n) return false;
    *v = static_cast<uint32_t>(body[off]) | static_cast<uint32_t>(body[off + 1]) << 8 |
         static_cast<uint32_t>(body[off + 2]) << 16 | static_cast<uint32_t>(body[off + 3]) << 24;
    off += 4;
    return true;
  };
  uint32_t skip = 0;
  if ((mask & 1) && !u32(&skip)) return false;
  if ((mask & 2) && !u32(object)) return false;
  if ((mask & 4) && !u32(target)) return false;
  return true;
}

struct CreateHoldItem {
  uint32_t id = 0;
  uint32_t owner = 0, parent = 0, target = 0;  // its create dependencies (0 = none), as for CreateOrderItem
  uint16_t missing = 0;  // the players with the world that don't know it (0: not replicated, failed, or known by all)
  uint16_t sees = kCreateSeesUnknown;  // the players whose team sees it this tick (own team, spectators included)
  bool exempt = false;                 // created for every player that lacks it (a player's creature, a level object)
};

struct CreatePlanEntry {
  uint16_t to = 0;      // create it now for these players
  uint16_t held = 0;    // missing & ~to: the players it is held from
  uint16_t pulled = 0;  // the part of `to` that doesn't see it: created as the dependency of an object created now
};

// One tick's creates. `order` is CreateOrder's of the same items (every dependency before its dependents). With `hold`
// off every item goes to its `missing` (the behaviour before SEC5).
inline std::vector<CreatePlanEntry> PlanCreates(const std::vector<CreateHoldItem>& items,
                                                const std::vector<std::size_t>& order, bool hold) {
  const std::size_t n = items.size();
  std::vector<CreatePlanEntry> plan(n);
  for (std::size_t i = 0; i < n; ++i) {
    const CreateHoldItem& it = items[i];
    const bool held = hold && !it.exempt && it.sees != kCreateSeesUnknown;
    plan[i].to = held ? static_cast<uint16_t>(it.missing & it.sees) : it.missing;
  }
  if (hold) {
    std::unordered_map<uint32_t, std::size_t> index;
    index.reserve(n);
    for (std::size_t i = 0; i < n; ++i)
      if (items[i].id && !index.count(items[i].id)) index[items[i].id] = i;
    // dependents come after their dependencies in `order`: walking it backwards hands each item's recipients to its
    // dependencies before those are visited themselves, so chains propagate in one pass
    for (std::size_t k = order.size(); k-- > 0;) {
      const std::size_t i = order[k];
      if (i >= n || !plan[i].to) continue;
      const uint32_t deps[3] = {items[i].owner, items[i].parent, items[i].target};
      for (const uint32_t dep : deps) {
        if (!dep || dep == items[i].id) continue;
        const auto f = index.find(dep);
        if (f == index.end()) continue;
        CreatePlanEntry& d = plan[f->second];
        const uint16_t add = static_cast<uint16_t>(plan[i].to & items[f->second].missing & ~d.to);
        if (!add) continue;
        d.to = static_cast<uint16_t>(d.to | add);
        d.pulled = static_cast<uint16_t>(d.pulled | add);
      }
    }
  }
  for (std::size_t i = 0; i < n; ++i) plan[i].held = static_cast<uint16_t>(items[i].missing & ~plan[i].to);
  return plan;
}

// Per object, the players it has been held from and not yet created for (for the log: a create at first sight, an
// object that died unseen). Objects that leave the scan are forgotten by Retain.
class HeldCreates {
 public:
  // After a tick's plan for one object (its `to` and `held`, at `nowMs`). Returns the players of `to` it had been held
  // from (a create at first sight); `heldMs` (optional) gets how long the oldest of them waited.
  uint16_t OnPlanned(uint32_t id, uint16_t to, uint16_t held, uint64_t nowMs, uint64_t* heldMs = nullptr) {
    auto it = held_.find(id);
    uint16_t first = 0;
    if (it != held_.end()) {
      first = static_cast<uint16_t>(to & it->second.players);
      if (heldMs) *heldMs = first ? nowMs - it->second.sinceMs : 0;
      it->second.players = static_cast<uint16_t>((it->second.players | held) & ~to);
      if (!it->second.players) held_.erase(it);
    } else {
      if (heldMs) *heldMs = 0;
      if (held) held_[id] = Entry{static_cast<uint16_t>(held & ~to), nowMs};
    }
    return first;
  }
  // At the object's deletion: the players it was held from and never created for (it died unseen by them). Forgets it.
  uint16_t OnDeleted(uint32_t id) {
    auto it = held_.find(id);
    if (it == held_.end()) return 0;
    const uint16_t p = it->second.players;
    held_.erase(it);
    return p;
  }
  // A player that left: its bit goes from every entry.
  void ForgetPlayer(int index) {
    if (index < 0 || index >= 16) return;
    for (auto it = held_.begin(); it != held_.end();) {
      it->second.players = static_cast<uint16_t>(it->second.players & ~(1u << index));
      it = it->second.players ? std::next(it) : held_.erase(it);
    }
  }
  // Keeps only the entries whose id `live` says is still there.
  template <class Pred>
  void Retain(Pred live) {
    for (auto it = held_.begin(); it != held_.end();) it = live(it->first) ? std::next(it) : held_.erase(it);
  }
  uint16_t HeldFrom(uint32_t id) const {
    auto it = held_.find(id);
    return it == held_.end() ? 0 : it->second.players;
  }
  std::size_t Size() const { return held_.size(); }

 private:
  struct Entry {
    uint16_t players = 0;
    uint64_t sinceMs = 0;
  };
  std::unordered_map<uint32_t, Entry> held_;
};

}  // namespace gms
