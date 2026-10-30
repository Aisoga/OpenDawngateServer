#include "control/host_control.h"

#include <algorithm>
#include <cstdio>

namespace dggms::control {

namespace {

// A line as the log shows it (no newline).
std::string Line(const Message& m) {
  std::string s = Encode(m);
  while (!s.empty() && (s.back() == '\n' || s.back() == '\r')) s.pop_back();
  return s;
}

}  // namespace

std::vector<RosterEntry> WithSpectatorsOf(const std::vector<RosterEntry>& fixed,
                                          const std::vector<RosterEntry>& update) {
  std::vector<RosterEntry> out;
  for (const RosterEntry& e : fixed)
    if (!e.spectator) out.push_back(e);
  for (const RosterEntry& e : update) {
    if (!e.spectator) continue;
    bool player = false;  // a user who plays stays a player
    for (const RosterEntry& p : out) player = player || (!p.spectator && p.user == e.user);
    if (!player) out.push_back(e);
  }
  return out;
}

bool HostControl::Start(const HostEnv& env, int64_t pid, const std::string& build, int64_t nowMs, std::string* err) {
  env_ = env;
  startMs_ = lastNowMs_ = nowMs;
  nextBeatMs_ = nowMs + o_.heartbeatMs;
  std::string e;
  if (!client_.Connect(env, pid, build, o_.connectTimeoutMs, &e)) {
    Log("control: connect to " + env.control + " failed: " + e);
    if (err) *err = e;
    return false;
  }
  ++sent_;
  Log("control: connected to " + env.control + "; -> hello {match " + std::to_string(env.match) + ", pid " +
      std::to_string(pid) + ", build \"" + build + "\"} (token not logged)");
  return true;
}

void HostControl::Poll(int64_t nowMs) {
  lastNowMs_ = nowMs;
  if (!client_.Connected()) return;
  std::vector<Message> got;
  std::vector<std::string> bad;
  const bool open = client_.Poll(got, &bad);
  for (const std::string& b : bad) {
    ++badLines_;
    Log("control: (line ignored: " + b + ")");
  }
  for (Message& m : got) {
    ++received_;
    Log("control: <- " + Line(m));
    if (auto* cg = std::get_if<CreateGame>(&m)) {
      if (hasGame_) {
        Log("control: (createGame again, ignored)");
        continue;
      }
      game_ = *cg;
      hasGame_ = true;
    } else if (auto* ur = std::get_if<UpdateRoster>(&m)) {
      // B15: a custom game's lobby roster (who may connect); after startGame the players stay the final ones
      if (!hasGame_ || game_.mode != kModeCustom) {
        Log("control: (updateRoster without a custom game, ignored)");
        continue;
      }
      if (!started_) {
        game_.roster = ur->roster;
        continue;
      }
      // B15b: after the start only the spectators follow (a spectator of the running game, one who left)
      int before = 0, after = 0;
      for (const RosterEntry& e : game_.roster) before += e.spectator ? 1 : 0;
      game_.roster = WithSpectatorsOf(game_.roster, ur->roster);
      for (const RosterEntry& e : game_.roster) after += e.spectator ? 1 : 0;
      Log("control: (updateRoster after the start: the players kept, the spectators taken: " + std::to_string(before) +
          " -> " + std::to_string(after) + ")");
    } else if (auto* sg = std::get_if<StartGame>(&m)) {
      if (!hasGame_ || started_) {
        Log("control: (startGame without a custom game in its lobby, ignored)");
        continue;
      }
      game_.roster = sg->roster;
      started_ = true;
    } else if (auto* rp = std::get_if<RemovePlayer>(&m)) {
      removed_.push_back(*rp);
    } else if (auto* pr = std::get_if<PlayerRewards>(&m)) {
      rewards_.push_back(*pr);
    } else if (auto* ab = std::get_if<AbortGame>(&m)) {
      if (!abort_) {
        abort_ = true;
        abortReason_ = ab->reason;
      }
    } else {
      Log(std::string("control: (") + TypeName(m) + " from the server, ignored)");
    }
  }
  if (!open) {
    if (!loggedOut_) {
      lost_ = true;
      Log("control: the control connection ended (closed or broken)");
    }
    return;
  }
  if (nowMs >= nextBeatMs_) Heartbeat(nowMs);
}

const RosterEntry* HostControl::FindUser(int64_t user) const {
  for (const RosterEntry& e : game_.roster)
    if (e.user == user) return &e;
  return nullptr;
}

std::vector<RemovePlayer> HostControl::TakeRemoved() {
  std::vector<RemovePlayer> out;
  out.swap(removed_);
  return out;
}

std::vector<PlayerRewards> HostControl::TakeRewards() {
  std::vector<PlayerRewards> out;
  out.swap(rewards_);
  return out;
}

bool HostControl::SendMsg(const Message& m) {
  if (!client_.Connected()) return false;
  std::string err;
  if (!client_.Send(m, &err)) {
    if (!loggedOut_) lost_ = true;
    Log(std::string("control: (") + TypeName(m) + " not sent: " + err + ")");
    return false;
  }
  ++sent_;
  // the heartbeat is logged only when it changes something (phase changes are sent through SetPhase)
  if (!std::holds_alternative<GMSUpdate>(m)) Log("control: -> " + Line(m));
  return true;
}

void HostControl::Heartbeat(int64_t nowMs) {
  GMSUpdate u;
  u.phase = phase_;
  u.elapsedMs = nowMs - startMs_;
  u.players = players_;
  SendMsg(u);
  nextBeatMs_ = nowMs + o_.heartbeatMs;
}

void HostControl::Ready(int64_t port) {
  GameReady r;
  r.port = port;
  SendMsg(r);
}

void HostControl::PlayerAdded(int64_t user, int64_t index) {
  if (std::find(players_.begin(), players_.end(), user) == players_.end()) players_.push_back(user);
  AddPlayer a;
  a.user = user;
  a.index = index;
  SendMsg(a);
}

void HostControl::PlayerGone(int64_t user, const std::string& reason) {
  players_.erase(std::remove(players_.begin(), players_.end(), user), players_.end());
  DisconnectPlayer d;
  d.user = user;
  d.reason = reason;
  SendMsg(d);
}

void HostControl::SetPhase(const std::string& phase, int64_t nowMs) {
  if (phase == phase_) return;
  phase_ = phase;
  GMSUpdate u;
  u.phase = phase_;
  u.elapsedMs = nowMs - startMs_;
  u.players = players_;
  if (SendMsg(u)) Log("control: -> " + Line(u));
  nextBeatMs_ = nowMs + o_.heartbeatMs;
}

void HostControl::SendPlayerFinish(const PlayerFinish& m) { SendMsg(m); }

void HostControl::SendFinish(const FinishGame& m) { SendMsg(m); }

void HostControl::SendPlayerKarma(const PlayerKarma& m) { SendMsg(m); }

void HostControl::SendSeatStats(const SeatStats& m) { SendMsg(m); }

void HostControl::SendTimeline(const MatchTimeline& m) { SendMsg(m); }

bool HostControl::SendError(int64_t code, const std::string& text, bool fatal) {
  GMSError e;
  e.code = code;
  e.text = text;
  e.fatal = fatal;
  if (!SendMsg(e)) return false;
  ++errors_;
  return true;
}

void HostControl::Logout() {
  if (loggedOut_) return;
  SendMsg(LogoutGMS{});
  loggedOut_ = true;
  client_.Close();
}

std::string HostControl::Describe() const {
  std::string players;
  for (int64_t u : players_) players += (players.empty() ? "" : ",") + std::to_string(u);
  char buf[512];
  std::snprintf(buf, sizeof buf,
                "control %s %s, match %lld, game %lld, phase %s, players [%s], sent %d, received %d, bad lines %d%s%s%s",
                env_.control.c_str(),
                client_.Connected() ? "connected" : (loggedOut_ ? "logged out" : (lost_ ? "LOST" : "not connected")),
                static_cast<long long>(env_.match), static_cast<long long>(game_.gameId), phase_.c_str(),
                players.c_str(), sent_, received_, badLines_,
                errors_ ? (", errors reported " + std::to_string(errors_)).c_str() : "", abort_ ? ", abort: " : "",
                abort_ ? abortReason_.c_str() : "");
  return buf;
}

}  // namespace dggms::control
