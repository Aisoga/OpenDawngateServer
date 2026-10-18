// Routing of the simulator's ready-made messages (D2g-live): see outgoing.h.
#include "core/replication/outgoing.h"

#include <cstdio>
#include <utility>

namespace gms {

void RouteOutgoing(std::vector<OutgoingRoute>* entries, uint16_t everyone, bool byVision) {
  // the deletions in this drain, by object (the last one wins; an id is deleted once): known mask, last visible mask
  std::map<uint32_t, std::pair<uint16_t, uint16_t>> deleted;
  for (const OutgoingRoute& e : *entries)
    if (e.kind == 0) deleted[e.object] = {e.recipients, e.lastVisible};
  for (OutgoingRoute& e : *entries) {
    e.fromDeletion = false;
    e.unseen = 0;
    switch (e.kind) {
      case 0:
      case 1:
        e.players = e.recipients;
        break;
      case 2: {
        uint16_t sees = kRouteUnknown;
        const bool limited = RoutedByVision(e.wire);
        if (e.objectAlive) {
          e.players = e.seers;
          sees = e.seeNow;
        } else {
          auto it = deleted.find(e.object);
          e.players = it != deleted.end() ? it->second.first : 0;
          e.fromDeletion = it != deleted.end();
          if (e.fromDeletion) sees = it->second.second;
        }
        if (!limited) break;
        e.unseen = static_cast<uint16_t>(e.players & ~sees);
        if (byVision) e.players = static_cast<uint16_t>(e.players & sees);
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
