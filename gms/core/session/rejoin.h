// RejoinMap (gms/core/session, G3a): which slot each user had, so a player who comes back on a new connection gets
// its old slot again (docs/GMS_PROTOCOL.md *Shaper select in `gms/` (D3-core)*, *Several humans (G3a)*). GmsSession
// keeps one per match: an accepted HelloReq of a user id seen before is a rejoin (the earlier gameplay index and team,
// whatever the match proposes; the earlier connection is reported to the match, IMatch::OnRejoin). The slot's gameplay
// index is the key that the match-wide parts use to find the player again: ShaperSelect::AddHuman binds the index to
// the new connection, surrender::SurrenderVote::AddPlayer moves an index's entry to it (MATCH_FLOW.md *Surrender and
// the karma vote (D8b-core)* section 8). Header-only and engine-free; user id 0 is never mapped.
#pragma once

#include <cstdint>
#include <map>

#include "core/net/endpoint.h"
#include "core/session/match.h"

namespace gms {

class RejoinMap {
 public:
  struct Seat {
    PlayerSlot slot;      // the gameplay index and team the user got first
    ConnId conn = 0;      // its latest connection
    bool connected = false;
  };
  struct Admission {
    PlayerSlot slot;      // the slot to use: the earlier one for a returning user, else the proposed one
    bool rejoin = false;  // the user had a slot before
    ConnId previous = 0;  // its connection before this one (0 for a new user)
    bool previousConnected = false;  // that connection hasn't ended yet (a rejoin before its loss was noticed)
  };

  // The slot a returning user had (false for a user without one): the session pre-fills the match's slot with it.
  bool Earlier(uint64_t userId, PlayerSlot* slot) const {
    auto it = seats_.find(userId);
    if (userId == 0 || it == seats_.end()) return false;
    if (slot) *slot = it->second.slot;
    return true;
  }

  // An accepted HelloReq of `userId` on `conn`, with the slot the match gave it. A user seen before keeps its first
  // slot (the gameplay index and team; the noun is the match's); a new user is seated with `proposed`.
  Admission Admit(uint64_t userId, ConnId conn, const PlayerSlot& proposed) {
    Admission a;
    a.slot = proposed;
    if (userId == 0) return a;
    auto it = seats_.find(userId);
    if (it != seats_.end()) {
      a.rejoin = true;
      a.previous = it->second.conn;
      a.previousConnected = it->second.connected && it->second.conn != conn;
      a.slot.gameplayIndex = it->second.slot.gameplayIndex;
      a.slot.team = it->second.slot.team;
    } else {
      it = seats_.emplace(userId, Seat()).first;
      it->second.slot = proposed;
    }
    it->second.conn = conn;
    it->second.connected = true;
    return a;
  }

  // The connection ended: its seat stays for a rejoin.
  void OnDisconnected(ConnId conn) {
    for (auto& kv : seats_)
      if (kv.second.conn == conn) kv.second.connected = false;
  }

  // B15b: the user's seat goes (its earlier gameplay index went to another user meanwhile: a spectator seat the host
  // gave away); its next HelloReq is seated with the match's slot.
  void Forget(uint64_t userId) { seats_.erase(userId); }

  const Seat* ByUser(uint64_t userId) const {
    auto it = seats_.find(userId);
    return it == seats_.end() ? nullptr : &it->second;
  }
  size_t Size() const { return seats_.size(); }

 private:
  std::map<uint64_t, Seat> seats_;  // by user id
};

}  // namespace gms
