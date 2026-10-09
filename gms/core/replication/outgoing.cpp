// Routing of the simulator's ready-made messages (D2g-live): see outgoing.h.
#include "core/replication/outgoing.h"

#include <cstdio>

namespace gms {

void RouteOutgoing(std::vector<OutgoingRoute>* entries, uint16_t everyone) {
  // the deletions in this drain, by object (the last one wins; an id is deleted once)
  std::map<uint32_t, uint16_t> deleted;
  for (const OutgoingRoute& e : *entries)
    if (e.kind == 0) deleted[e.object] = e.recipients;
  for (OutgoingRoute& e : *entries) {
    e.fromDeletion = false;
    switch (e.kind) {
      case 0:
      case 1:
        e.players = e.recipients;
        break;
      case 2: {
        if (e.objectAlive) {
          e.players = e.seers;
          break;
        }
        auto it = deleted.find(e.object);
        e.players = it != deleted.end() ? it->second : 0;
        e.fromDeletion = it != deleted.end();
        break;
      }
      case 3:
        e.players = everyone;
        break;
      default:
        e.players = 0;
        break;
    }
  }
}

void WireCounts::Add(uint8_t wire, size_t bytes) {
  Count& c = byWire_[wire];
  ++c.n;
  c.bytes += bytes;
  ++messages_;
  bytes_ += bytes;
}

std::string WireCounts::Format() const {
  std::string s;
  for (const auto& kv : byWire_) {
    char buf[48];
    std::snprintf(buf, sizeof buf, "%s%02x x%d %zu B", s.empty() ? "" : ", ", kv.first, kv.second.n, kv.second.bytes);
    s += buf;
  }
  return s;
}

void WireCounts::Clear() {
  byWire_.clear();
  messages_ = 0;
  bytes_ = 0;
}

}  // namespace gms
