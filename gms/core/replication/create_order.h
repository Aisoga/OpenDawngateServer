// The order in which a player's world is created (gms/core, D9b; docs/MATCH_FLOW.md *D9b*). No game addresses: the
// engine side (gms/engine/replicate.cpp BuildWorld) passes each object's id and owner (object +0x48).
//
// The client maps every tObjID member of a reflected struct through its server -> client id map, and an id it doesn't
// know yet becomes 0 without an assert (GameClient_ReflectObjectIdField 0x006B7A00). sporelabsObject.mOwnerID is such
// a member and only the full state carries it, so an object created before its owner keeps owner 0 on that client
// (the create data's own ownerID arrives raw and is overwritten by the mapped member). The world for a joining player
// is built in the object manager's pool order, where a worker can sit in a slot below its well's; so the world puts
// every object after its owner when the owner is in the same world, and keeps the pool order otherwise.
//
// FARISW: the same holds for cLocomotionData's +8 (the parent of nLocomotion.AttachObject, the target of a follow), a
// tObjID member of the 9C every create carries. Runtime creates go out in pool order too, and Faris's melee W creates
// its hologram and then the projectile it rides on; with the hologram in the lower slot the client got a parent it
// didn't know yet, mapped it to 0 and left the hologram where it was made while the projectile flew. So the creates of
// a replication step use the same order, with the parent as a second dependency.
#pragma once

#include <cstddef>
#include <cstdint>
#include <unordered_map>
#include <utility>
#include <vector>

namespace gms {

struct CreateOrderItem {
  std::uint32_t id = 0;
  std::uint32_t owner = 0;   // 0 = none
  std::uint32_t parent = 0;  // the locomotion's +8 (attach parent, follow target); 0 = none
};

// Indices into `items` in create order: the input order, except that an item's owner and parent that come after it
// are moved up to just before it (so every item follows its owner and its parent when they are among `items`; the
// owner first). Dependency chains are followed to any depth; a cycle (or an item that names itself) is broken in input
// order.
inline std::vector<std::size_t> CreateOrder(const std::vector<CreateOrderItem>& items) {
  const std::size_t n = items.size();
  std::unordered_map<std::uint32_t, std::size_t> index;
  index.reserve(n);
  for (std::size_t i = 0; i < n; ++i)
    if (items[i].id && !index.count(items[i].id)) index[items[i].id] = i;
  std::vector<char> state(n, 0);  // 0 not placed, 1 being placed (on the stack), 2 placed
  std::vector<std::size_t> out;
  out.reserve(n);
  // an explicit stack of (item, next dependency to look at): 0 owner, 1 parent, 2 done
  std::vector<std::pair<std::size_t, int>> stack;
  for (std::size_t start = 0; start < n; ++start) {
    if (state[start]) continue;
    state[start] = 1;
    stack.push_back({start, 0});
    while (!stack.empty()) {
      auto& top = stack.back();
      const std::size_t cur = top.first;
      if (top.second >= 2) {
        state[cur] = 2;
        out.push_back(cur);
        stack.pop_back();
        continue;
      }
      const std::uint32_t dep = top.second == 0 ? items[cur].owner : items[cur].parent;
      ++top.second;
      if (!dep || dep == items[cur].id) continue;
      const auto it = index.find(dep);
      if (it == index.end() || state[it->second]) continue;  // not among the items, placed, or a cycle
      state[it->second] = 1;
      stack.push_back({it->second, 0});
    }
  }
  return out;
}

}  // namespace gms
