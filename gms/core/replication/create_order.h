// The order in which a player's world is created (gms/core, D9b; docs/MATCH_FLOW.md *D9b*). No game addresses: the
// engine side (gms/engine/replicate.cpp BuildWorld) passes each object's id and owner (object +0x48).
//
// The client maps every tObjID member of a reflected struct through its server -> client id map, and an id it doesn't
// know yet becomes 0 without an assert (GameClient_ReflectObjectIdField 0x006B7A00). sporelabsObject.mOwnerID is such
// a member and only the full state carries it, so an object created before its owner keeps owner 0 on that client
// (the create data's own ownerID arrives raw and is overwritten by the mapped member). The world for a joining player
// is built in the object manager's pool order, where a worker can sit in a slot below its well's; so the world puts
// every object after its owner when the owner is in the same world, and keeps the pool order otherwise.
#pragma once

#include <cstddef>
#include <cstdint>
#include <unordered_map>
#include <vector>

namespace gms {

struct CreateOrderItem {
  std::uint32_t id = 0;
  std::uint32_t owner = 0;  // 0 = none
};

// Indices into `items` in create order: the input order, except that an owner that comes after something it owns is
// moved up to just before the first such item (so every item follows its owner when the owner is among `items`). An
// owner chain is followed to any depth; a cycle (or an item that owns itself) is broken in input order.
inline std::vector<std::size_t> CreateOrder(const std::vector<CreateOrderItem>& items) {
  const std::size_t n = items.size();
  std::unordered_map<std::uint32_t, std::size_t> index;
  index.reserve(n);
  for (std::size_t i = 0; i < n; ++i)
    if (items[i].id && !index.count(items[i].id)) index[items[i].id] = i;
  std::vector<char> state(n, 0);  // 0 not placed, 1 being placed (on the chain), 2 placed
  std::vector<std::size_t> out;
  out.reserve(n);
  std::vector<std::size_t> chain;
  for (std::size_t start = 0; start < n; ++start) {
    if (state[start]) continue;
    // walk up the owner chain while the owner is in the world and not placed yet, then place from the top down
    chain.clear();
    std::size_t cur = start;
    for (;;) {
      state[cur] = 1;
      chain.push_back(cur);
      const std::uint32_t ow = items[cur].owner;
      if (!ow || ow == items[cur].id) break;
      const auto it = index.find(ow);
      if (it == index.end() || state[it->second]) break;  // not in the world, placed, or a cycle
      cur = it->second;
    }
    for (std::size_t k = chain.size(); k-- > 0;) {
      state[chain[k]] = 2;
      out.push_back(chain[k]);
    }
  }
  return out;
}

}  // namespace gms
