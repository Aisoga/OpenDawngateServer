// Surrender on the GMS (D8b-core): see surrender.h.
#include "core/surrender/surrender.h"

#include <algorithm>
#include <cmath>
#include <cstdio>

namespace gms {
namespace surrender {

namespace {

const char* AvailabilityName(uint8_t r) {
  switch (r) {
    case kAvailMatchTime:
      return "match time";
    case kAvailDisconnect:
      return "a player disconnected too long";
    case kAvailEconomy:
      return "team economy difference";
    case kAvailKills:
      return "team kill difference";
    default:
      return "not available";
  }
}

const char* FailureName(uint8_t r) {
  switch (r) {
    case kFailNotYetAvailable:
      return "not yet available";
    case kFailRecentVote:
      return "recent failed vote";
    case kFailDisconnected:
      return "disconnected too long";
    case kFailSpectator:
      return "spectator";
    default:
      return "?";
  }
}

const char* StateName(uint8_t s) {
  switch (s) {
    case kVoteStarted:
      return "started";
    case kVotePassing:
      return "running, passing";
    case kVoteFailing:
      return "running, failing";
    case kVoteSucceeded:
      return "succeeded";
    case kVoteFailed:
      return "failed";
    case kVoteClosed:
      return "closed";
    case kVoteNone:
      return "none";
    default:
      return "?";
  }
}

const char kVoteLetter[] = "-ipaYN";  // SlotVote 0..5: empty, ineligible, pending, abstained, yes, no

bool Valid(int team) { return team == 1 || team == 2; }

}  // namespace

Bytes BuildAvailable(uint8_t reason) { return Bytes{kWire, kSubAvailable, reason}; }

Bytes BuildRequestFailed(uint8_t reason) { return Bytes{kWire, kSubRequestFailed, reason}; }

Bytes BuildStatus(const Status& s) {
  Bytes b{kWire, kSubStatus, s.team};
  PutI64LE(b, s.startMs);  // unaligned at +1, as the reader takes it (ReadBytes 0x14 into a packed local)
  PutU8(b, s.state);
  for (const Slot& sl : s.slots) PutU8(b, sl.index);
  for (const Slot& sl : s.slots) PutU8(b, static_cast<uint8_t>((sl.vote & 0x7F) | (sl.flag ? 0x80 : 0)));
  return b;
}

Bytes BuildTeamResult(uint8_t result, uint8_t team, uint8_t yesPercent) {
  return Bytes{kWire, kSubTeamResult, result, team, yesPercent};
}

bool ParseClientVote(const uint8_t* body, size_t n, bool* yes) {
  if (n < 1 || (body[0] != kClientVoteYes && body[0] != kClientVoteNo)) return false;
  *yes = body[0] == kClientVoteYes;
  return true;
}

bool ParseServerMessage(const uint8_t* body, size_t n, ServerMessage* out) {
  if (n < 1) return false;
  ServerMessage m;
  m.sub = body[0];
  switch (m.sub) {
    case kSubAvailable:
    case kSubRequestFailed:
      if (n < 2) return false;
      m.reason = body[1];
      break;
    case kSubStatus: {
      if (n < 1 + kStatusBodySize) return false;
      const uint8_t* p = body + 1;
      m.status.team = p[0];
      m.status.startMs = GetI64LE(p + 1);
      m.status.state = p[9];
      for (int i = 0; i < kSlots; ++i) {
        m.status.slots[i].index = p[10 + i];
        m.status.slots[i].vote = static_cast<uint8_t>(p[15 + i] & 0x7F);
        m.status.slots[i].flag = (p[15 + i] & 0x80) != 0;
      }
      break;
    }
    case kSubTeamResult:
      if (n < 4) return false;
      m.result = body[1];
      m.team = body[2];
      m.yesPercent = body[3];
      break;
    default:
      return false;
  }
  *out = m;
  return true;
}

std::string Describe(const Bytes& msg, bool fromClient) {
  char buf[200];
  if (msg.size() < 2 || msg[0] != kWire) return "not a SurrenderMsgs message";
  if (fromClient) {
    bool yes = false;
    if (!ParseClientVote(msg.data() + 1, msg.size() - 1, &yes)) {
      std::snprintf(buf, sizeof buf, "SurrenderMsgs %02X from the client: unknown", msg[1]);
      return buf;
    }
    return yes ? "SurrenderMsgs 01 from the client: vote for surrender (starts the team's vote if none runs)"
               : "SurrenderMsgs 02 from the client: vote against surrender";
  }
  ServerMessage m;
  if (!ParseServerMessage(msg.data() + 1, msg.size() - 1, &m)) {
    std::snprintf(buf, sizeof buf, "SurrenderMsgs %02X: the client asserts on it (or it is too short)", msg[1]);
    return buf;
  }
  switch (m.sub) {
    case kSubAvailable:
      std::snprintf(buf, sizeof buf, "SurrenderMsgs 00: surrender available (%u, %s)", m.reason,
                    AvailabilityName(m.reason));
      return buf;
    case kSubRequestFailed:
      std::snprintf(buf, sizeof buf, "SurrenderMsgs 01: request failed (%u, %s)", m.reason, FailureName(m.reason));
      return buf;
    case kSubStatus: {
      std::string slots;
      for (const Slot& sl : m.status.slots) {
        char s[16];
        std::snprintf(s, sizeof s, " %u:%c%s", sl.index, sl.vote < 6 ? kVoteLetter[sl.vote] : '?', sl.flag ? "" : "'");
        slots += s;
      }
      std::snprintf(buf, sizeof buf, "SurrenderMsgs 03: team %u vote status %u (%s), start %lld ms, slots", m.status.team,
                    m.status.state, StateName(m.status.state), static_cast<long long>(m.status.startMs));
      return buf + slots;
    }
    default:
      std::snprintf(buf, sizeof buf, "SurrenderMsgs 04: team %u's vote %s, %u %% in favour", m.team,
                    m.result == kVoteSucceeded ? "passed" : m.result == kVoteFailed ? "failed" : "(bad result)",
                    m.yesPercent);
      return buf;
  }
}

uint32_t EndReason(int surrenderingTeam) {
  if (surrenderingTeam == 1) return 4;  // Spirit wins by surrender
  if (surrenderingTeam == 2) return 2;  // Mortal wins by surrender
  return 0;
}

int YesPercent(int yes, int eligible) {
  if (eligible <= 0) return 0;
  return static_cast<int>(std::floor(100.0 * yes / eligible + 0.5));
}

// ---- the vote -------------------------------------------------------------------------------------------------------

void SurrenderVote::AddPlayer(ConnId conn, int index, int team, bool observer) {
  // G3a: an index another connection has is the same player back on a new connection (the session keeps a returning
  // user's slot, RejoinMap): its entry moves to `conn` with everything it had, its vote in a running vote included
  for (auto it = players_.begin(); it != players_.end(); ++it) {
    if (it->first == conn || it->second.index != index) continue;
    const ConnId previous = it->first;
    const Player moved = it->second;
    players_.erase(it);
    players_[conn] = moved;
    for (Team& tm : teams_) {
      auto v = tm.votes.find(previous);
      if (v == tm.votes.end()) continue;
      const uint8_t vote = v->second;
      tm.votes.erase(v);
      tm.votes[conn] = vote;
    }
    return;
  }
  Player& p = players_[conn];
  p.index = index;
  p.team = observer ? 0 : team;
  p.observer = observer || !Valid(team);
}

void SurrenderVote::OnConnection(ConnId conn, bool connected, double nowS, std::vector<Send>* out) {
  auto it = players_.find(conn);
  if (it == players_.end()) return;
  Player& p = it->second;
  if (p.connected != connected) {
    if (!connected) {
      p.disconnectedAt = nowS;
      // a pending voter can't vote any more: ineligible for this vote, like a player disconnected at its start (Q2)
      if (!p.observer && Valid(p.team) && teams_[p.team].running) {
        auto v = teams_[p.team].votes.find(conn);
        if (v != teams_[p.team].votes.end() && v->second == kSlotPending) {
          v->second = kSlotIneligible;
          teams_[p.team].recount = true;
        }
      }
    } else {
      p.disconnectedS += std::max(0.0, nowS - p.disconnectedAt);
    }
    p.connected = connected;
  }
  // G3a: a player on a new connection (back, or moved by AddPlayer before its old connection's loss was noticed)
  // during its team's vote, or an observer during any, gets the vote's status, so its new client's vote window shows
  // it [assumed]; a slot that became ineligible at the disconnect stays so for this vote
  if (!connected || !out || stopped_) return;
  for (int team = 1; team <= 2; ++team) {
    const Team& tm = teams_[team];
    if (!tm.running || (!p.observer && p.team != team)) continue;
    const Bytes msg = BuildStatus(BuildTeamStatus(team, tm.last.state));
    out->push_back(Send{conn, msg, surrender::Describe(msg, false)});
  }
}

double SurrenderVote::DisconnectedS(const Player& p, double nowS) const {
  return p.disconnectedS + (p.connected ? 0.0 : std::max(0.0, nowS - p.disconnectedAt));
}

bool SurrenderVote::Eligible(const Player& p, double nowS) const {
  return !p.observer && p.connected && DisconnectedS(p, nowS) < t_.enableDisconnectS;
}

uint8_t SurrenderVote::Availability(int team) const { return Valid(team) ? teams_[team].available : 0; }

uint8_t SurrenderVote::UiAvailability() const {
  return teams_[1].available ? teams_[1].available : teams_[2].available;
}

bool SurrenderVote::Running(int team) const { return Valid(team) && teams_[team].running; }

const Status& SurrenderVote::LastStatus(int team) const { return teams_[Valid(team) ? team : 0].last; }

Status SurrenderVote::BuildTeamStatus(int team, uint8_t state) const {
  const Team& tm = teams_[team];
  Status s;
  s.team = static_cast<uint8_t>(team);
  s.startMs = tm.startMs;
  s.state = state;
  // the team's players in gameplay-index order, at most five (5v5)
  std::vector<std::pair<int, ConnId>> order;
  for (const auto& kv : players_)
    if (!kv.second.observer && kv.second.team == team) order.emplace_back(kv.second.index, kv.first);
  std::sort(order.begin(), order.end());
  int i = 0;
  for (const auto& o : order) {
    if (i >= kSlots) break;
    auto v = tm.votes.find(o.second);
    s.slots[i].index = static_cast<uint8_t>(o.first);
    s.slots[i].vote = v != tm.votes.end() ? v->second : static_cast<uint8_t>(kSlotIneligible);
    s.slots[i].flag = players_.at(o.second).connected;  // [assumed meaning of bit 7]
    ++i;
  }
  return s;
}

void SurrenderVote::Recipients(int team, bool everyone, bool observers, std::vector<ConnId>* out) const {
  for (const auto& kv : players_) {
    if (!kv.second.connected) continue;
    const bool own = !kv.second.observer && kv.second.team == team;
    if (everyone || own || (observers && kv.second.observer)) out->push_back(kv.first);
  }
}

void SurrenderVote::SendStatus(int team, uint8_t state, std::vector<Send>* out) {
  Team& tm = teams_[team];
  tm.last = BuildTeamStatus(team, state);
  const Bytes msg = BuildStatus(tm.last);
  const std::string what = surrender::Describe(msg, false);
  std::vector<ConnId> to;
  Recipients(team, false, true, &to);
  for (ConnId c : to) out->push_back(Send{c, msg, what});
}

void SurrenderVote::Start(int team, ConnId requester, double nowS, int64_t serverMs, std::vector<Send>* out) {
  Team& tm = teams_[team];
  tm.running = true;
  tm.startS = nowS;
  tm.startMs = serverMs;
  tm.votes.clear();
  for (const auto& kv : players_) {
    if (kv.second.observer || kv.second.team != team) continue;
    tm.votes[kv.first] = Eligible(kv.second, nowS) ? kSlotPending : kSlotIneligible;
  }
  tm.votes[requester] = kSlotYes;
  Evaluate(team, nowS, false, out);
}

// Counts the team's votes; ends the vote when it is decided (or nobody is pending, or the time is up), else sends the
// running state.
void SurrenderVote::Evaluate(int team, double nowS, bool timeUp, std::vector<Send>* out) {
  Team& tm = teams_[team];
  tm.recount = false;
  int yes = 0, no = 0, pending = 0;
  for (const auto& kv : tm.votes) {
    yes += kv.second == kSlotYes ? 1 : 0;
    no += kv.second == kSlotNo ? 1 : 0;
    pending += kv.second == kSlotPending ? 1 : 0;
  }
  const int eligible = yes + no + pending;
  const double need = static_cast<double>(t_.threshold) * eligible;
  const bool passNow = eligible > 0 && yes >= need - 1e-9;
  const bool cannotPass = yes + pending < need - 1e-9;
  if (timeUp || pending == 0 || (t_.endWhenDecided && (passNow || cannotPass))) {
    Finish(team, passNow, yes, eligible, nowS, out);
    return;
  }
  SendStatus(team, passNow ? kVotePassing : kVoteFailing, out);
}

void SurrenderVote::Finish(int team, bool passed, int yes, int eligible, double nowS, std::vector<Send>* out) {
  Team& tm = teams_[team];
  tm.running = false;
  for (auto& kv : tm.votes)
    if (kv.second == kSlotPending) kv.second = kSlotAbstained;
  SendStatus(team, passed ? kVoteSucceeded : kVoteFailed, out);
  const int pct = YesPercent(yes, eligible);
  const Bytes msg = BuildTeamResult(passed ? kVoteSucceeded : kVoteFailed, static_cast<uint8_t>(team),
                                    static_cast<uint8_t>(std::min(pct, 255)));
  const std::string what = surrender::Describe(msg, false);
  std::vector<ConnId> to;
  // a pass to everyone (the other team reads "The Enemy team has surrendered!"), a failure to the team and observers
  Recipients(team, passed, true, &to);
  for (ConnId c : to) out->push_back(Send{c, msg, what});
  if (passed) {
    ended_ = true;
    stopped_ = true;
    end_.team = team;
    end_.reason = EndReason(team);
    end_.yesPercent = pct;
    end_.at = nowS;
  } else {
    tm.cooldownUntil = nowS + t_.cooldownS;
  }
}

void SurrenderVote::Tick(double nowS, int64_t serverMs, const MatchView& m, std::vector<Send>* out) {
  (void)serverMs;
  if (stopped_) return;
  for (int team = 1; team <= 2; ++team) {
    Team& tm = teams_[team];
    if (!tm.available) {
      const int other = 3 - team;
      uint8_t reason = kUnavailable;
      if (m.matchS >= t_.enableMatchMinutes * 60.0) {
        reason = kAvailMatchTime;
      } else {
        for (const auto& kv : players_)
          if (!kv.second.observer && kv.second.team == team && !kv.second.connected &&
              nowS - kv.second.disconnectedAt >= t_.enableDisconnectS)
            reason = kAvailDisconnect;
        if (!reason && m.gold[other] - m.gold[team] >= t_.enableEconomy) reason = kAvailEconomy;
        if (!reason && m.kills[other] - m.kills[team] >= t_.enableKills) reason = kAvailKills;
      }
      if (reason) {
        tm.available = reason;
        const Bytes msg = BuildAvailable(reason);
        char what[96];
        std::snprintf(what, sizeof what, "SurrenderMsgs 00: team %d may surrender (%u, %s)", team, reason,
                      AvailabilityName(reason));
        std::vector<ConnId> to;
        Recipients(team, false, false, &to);  // the team only [assumed]
        for (ConnId c : to) out->push_back(Send{c, msg, what});
      }
    }
    const bool timeUp = nowS - tm.startS >= t_.voteS;
    if (tm.running && (tm.recount || timeUp)) Evaluate(team, nowS, timeUp, out);
  }
}

bool SurrenderVote::OnClientMessage(double nowS, int64_t serverMs, ConnId conn, const uint8_t* body, size_t n,
                                    std::vector<Send>* out) {
  auto it = players_.find(conn);
  bool yes = false;
  if (it == players_.end() || !ParseClientVote(body, n, &yes)) return false;
  if (stopped_) return true;
  const Player& p = it->second;
  auto refuse = [&](uint8_t reason) {
    const Bytes msg = BuildRequestFailed(reason);
    out->push_back(Send{conn, msg, surrender::Describe(msg, false)});
  };
  if (p.observer) {
    refuse(kFailSpectator);
    return true;
  }
  if (DisconnectedS(p, nowS) >= t_.enableDisconnectS) {
    refuse(kFailDisconnected);
    return true;
  }
  Team& tm = teams_[p.team];
  if (tm.running) {
    auto v = tm.votes.find(conn);
    if (v == tm.votes.end() || v->second != kSlotPending) return true;  // voted already, or not a voter of this vote
    v->second = yes ? kSlotYes : kSlotNo;
    Evaluate(p.team, nowS, false, out);
    return true;
  }
  if (!yes) return true;  // a no without a vote [assumed: ignored]
  if (!tm.available) {
    refuse(kFailNotYetAvailable);
    return true;
  }
  if (tm.cooldownUntil >= 0 && nowS < tm.cooldownUntil) {
    refuse(kFailRecentVote);
    return true;
  }
  Start(p.team, conn, nowS, serverMs, out);
  return true;
}

bool SurrenderVote::TakeEnd(End* out) {
  if (!ended_ || endTaken_) return false;
  endTaken_ = true;
  *out = end_;
  return true;
}

std::string SurrenderVote::Describe() const {
  std::string s = "surrender:";
  char buf[160];
  for (int team = 1; team <= 2; ++team) {
    const Team& tm = teams_[team];
    std::snprintf(buf, sizeof buf, " team %d %s", team, tm.available ? AvailabilityName(tm.available) : "-");
    s += buf;
    if (tm.running) {
      int yes = 0, no = 0, pending = 0;
      for (const auto& kv : tm.votes) {
        yes += kv.second == kSlotYes ? 1 : 0;
        no += kv.second == kSlotNo ? 1 : 0;
        pending += kv.second == kSlotPending ? 1 : 0;
      }
      std::snprintf(buf, sizeof buf, " (vote: %d yes, %d no, %d pending)", yes, no, pending);
      s += buf;
    }
    s += team == 1 ? ";" : "";
  }
  if (ended_) {
    std::snprintf(buf, sizeof buf, "; team %d surrendered (%d %%, end reason %u)", end_.team, end_.yesPercent,
                  end_.reason);
    s += buf;
  }
  return s;
}

}  // namespace surrender
}  // namespace gms
