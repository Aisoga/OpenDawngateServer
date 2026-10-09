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
#pragma once

#include <cstddef>
#include <cstdint>
#include <map>
#include <string>
#include <vector>

namespace gms {

struct OutgoingRoute {
  int kind = 0;                // 0 delete, 1 recipients, 2 object, 3 position
  uint32_t object = 0;         // kinds 0 and 2
  uint16_t recipients = 0;     // kind 0: the known mask at the deletion; kind 1: the entry's mask
  bool objectAlive = false;    // kind 2: the object still exists when the vector is drained
  uint16_t seers = 0;          // kind 2: the live object's known & visible masks
  uint16_t players = 0;        // out: who gets it
  bool fromDeletion = false;   // out: kind 2 routed by the object's kind-0 entry
};

// Fills `players` of every entry (unknown kinds get 0).
void RouteOutgoing(std::vector<OutgoingRoute>* entries, uint16_t everyone);

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
