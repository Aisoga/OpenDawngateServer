// Routing of the server simulator's ready-made messages and the host's per-second message counts (gms/core, D2g-live;
// docs/GMS_PROTOCOL.md *Live replication (D2g-static)*, *Runtime creation and deletion*). No game addresses: the
// engine side (gms/engine/replicate.cpp) reads the outgoing vector (sim +0x1A998) into OutgoingRoute entries.
//
// The vector's kinds: 0 an object's deletion (recipients = its known-players mask at the deletion, sent as 91
// ObjectDelete), 1 a message with its own recipients mask, 2 a message about an object (the players that know and see
// it), 3 a positional message (the players that see a position; everyone until fog of war, D2g step 2).
// D2f's drain read a kind-2 entry's mask from the live object, so a message about an object deleted in the same tick
// (its last ModifierCreated, SetAnimationState, ...) went to nobody. The fix: take its recipients from the object's
// kind-0 entry in the same drain (the deletion is queued after the messages about the dying object).
//
// FOGSOUND: with the vision rule on ([host] fog_events_by_vision, 1) a message about
// an object goes only to the players whose team sees the object now, the vision model's answer of this tick
// (seeNow), besides the known & visible masks. Those masks lag a tick behind the model (the fog step writes +0x10 after
// the drain) and a fresh create sets +0x10 for everyone it was created for, so before this an object created in fog
// (a projectile, a ward, a minion in its base) sent its first tick's ServerEvents, animation states and combat events
// to every player, and an object entering fog its last tick's. A message about an object deleted in the same tick
// goes to its deletion's known mask & the visible mask the host last left on it (lastVisible). The deletion (91)
// itself still goes to everyone that knew the object, kind 1 keeps its own mask (the kill announcement's 0x7FFF, an
// attached effect's stop) and kind 3 is routed by its point by the caller.
#pragma once

#include <cstddef>
#include <cstdint>
#include <map>
#include <string>
#include <vector>

namespace gms {

constexpr uint16_t kRouteUnknown = 0xFFFF;  // seeNow / lastVisible not known: no vision limit

struct OutgoingRoute {
  int kind = 0;                // 0 delete, 1 recipients, 2 object, 3 position
  uint8_t wire = 0;            // the message's wire byte (kind 2: RoutedByVision)
  uint32_t object = 0;         // kinds 0 and 2
  uint16_t recipients = 0;     // kind 0: the known mask at the deletion; kind 1: the entry's mask
  bool objectAlive = false;    // kind 2: the object still exists when the vector is drained
  uint16_t seers = 0;          // kind 2: the live object's known & visible masks
  uint16_t seeNow = kRouteUnknown;       // kind 2: the players whose team sees the object this tick (FOGSOUND)
  uint16_t lastVisible = kRouteUnknown;  // kind 0: the object's visible mask as the host last left it (FOGSOUND)
  uint16_t players = 0;        // out: who gets it
  uint16_t unseen = 0;         // out: kind 2, the players it would reach (or reaches, byVision off) without seeing it
  bool fromDeletion = false;   // out: kind 2 routed by the object's kind-0 entry
};

// Fills `players` of every entry (unknown kinds get 0). `byVision` (FOGSOUND): kind 2 also limited to seeNow, or for a
// deleted object to its deletion's lastVisible; `unseen` is filled either way (the audit of what fog would hide).
void RouteOutgoing(std::vector<OutgoingRoute>* entries, uint16_t everyone, bool byVision = false);

// FOGSOUND: the kind-2 messages the vision rule limits: all but PlayerCharacterDeploy (B3), which tells a client which
// creature is a player's (the client's LabsPlayer +0x0C; A9's mCurrentCreatureId sets it too) and plays nothing.
inline bool RoutedByVision(uint8_t wire) { return wire != 0xB3; }

// FOGSOUND: what a create for a player that doesn't see the new object leaves out. The attached effects (92) and the
// last animation (B1) start effects, sounds and animation events at once (the client makes graphics at 8F for most
// nouns, before the 93 that follows in the same flush); the fog exit sends both after 95 anyway.
inline bool SkippedInFoggedCreate(uint8_t wire) { return wire == 0x92 || wire == 0xB1; }

// Messages and bytes per wire byte, for the host's per-second log line.
class WireCounts {
 public:
  void Add(uint8_t wire, size_t bytes);
  bool Empty() const { return byWire_.empty(); }
  int Messages() const { return messages_; }
  size_t TotalBytes() const { return bytes_; }
  // "9c x15 150 B, a0 x2 20 B" in wire order; empty if nothing was counted.
  std::string Format() const;
  void Clear();

 private:
  struct Count {
    int n = 0;
    size_t bytes = 0;
  };
  std::map<uint8_t, Count> byWire_;
  int messages_ = 0;
  size_t bytes_ = 0;
};

}  // namespace gms
