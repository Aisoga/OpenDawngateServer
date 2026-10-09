// SpectatorSeats (gms/core/session, B15b): which spectator gameplay index (10-14) each spectating user has on the
// match host (docs/GMS_PROTOCOL.md *Spectators on the match host (B15b)*). dgserver's roster gives a custom game's
// spectators indexes 10-14 in its own order, which shifts when one leaves (its lobbyRoster numbers them afresh), so the
// host keeps its own seats: a user keeps its index while connected and, after it left, for its return (the session's
// RejoinMap keeps the same index for a returning user); a newcomer gets the roster's index when that one is free, else
// the first free one; with every free index kept for users who left, the one kept longest goes to the newcomer.
// Header-only and engine-free; the host (gms/engine/gms_host.cpp) asks it at a spectator's HelloReq.
#pragma once

#include <cstdint>
#include <string>

#include "core/protocol/messages.h"

namespace gms {

class SpectatorSeats {
 public:
  // The gameplay index for `user` (10-14), `preferred` the roster's; -1 when all five are held by connected spectators.
  // `evicted` (optional) gets the user whose kept seat went to this one (0 for none).
  int Admit(uint64_t user, int preferred, uint64_t* evicted = nullptr) {
    if (evicted) *evicted = 0;
    if (user == 0) return -1;
    int i = Find(user);
    if (i < 0) {
      const int want = preferred - kFirstSpectatorIndex;
      if (want >= 0 && want < kMaxSpectators && seats_[want].user == 0) i = want;
      for (int k = 0; i < 0 && k < kMaxSpectators; ++k)
        if (seats_[k].user == 0) i = k;
      for (int k = 0; k < kMaxSpectators; ++k) {  // every seat taken: the one kept longest for a user who left
        if (seats_[k].user == 0 || seats_[k].connected) continue;
        if (i < 0 || (seats_[i].user != 0 && seats_[k].since < seats_[i].since)) i = k;
      }
      if (i < 0) return -1;
      if (evicted && seats_[i].user != 0) *evicted = seats_[i].user;
      seats_[i].user = user;
    }
    seats_[i].connected = true;
    return kFirstSpectatorIndex + i;
  }

  // The user's connection ended: its seat is kept for its return.
  void Left(uint64_t user) {
    const int i = Find(user);
    if (i < 0) return;
    seats_[i].connected = false;
    seats_[i].since = ++clock_;
  }

  int IndexOf(uint64_t user) const {
    const int i = Find(user);
    return i < 0 ? -1 : kFirstSpectatorIndex + i;
  }
  bool Connected(uint64_t user) const {
    const int i = Find(user);
    return i >= 0 && seats_[i].connected;
  }
  int ConnectedCount() const {
    int n = 0;
    for (const Seat& s : seats_) n += s.user != 0 && s.connected ? 1 : 0;
    return n;
  }

  // "10=2000000003 11=(2000000004, left)" for the stats line; "none" without seats.
  std::string Describe() const {
    std::string s;
    for (int k = 0; k < kMaxSpectators; ++k) {
      if (seats_[k].user == 0) continue;
      s += (s.empty() ? "" : " ") + std::to_string(kFirstSpectatorIndex + k) + "=" +
           (seats_[k].connected ? std::to_string(seats_[k].user) : "(" + std::to_string(seats_[k].user) + ", left)");
    }
    return s.empty() ? std::string("none") : s;
  }

 private:
  struct Seat {
    uint64_t user = 0;
    bool connected = false;
    uint64_t since = 0;  // when it left (Left's order)
  };
  int Find(uint64_t user) const {
    for (int k = 0; k < kMaxSpectators; ++k)
      if (user != 0 && seats_[k].user == user) return k;
    return -1;
  }
  Seat seats_[kMaxSpectators];
  uint64_t clock_ = 0;
};

}  // namespace gms
