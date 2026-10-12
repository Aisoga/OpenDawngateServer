#include "core/session/shaper_select.h"

#include <algorithm>
#include <array>
#include <cstdio>

#include "core/common/log.h"
#include "core/session/match.h"

namespace gms {
namespace {

std::string NounText(uint32_t n) {
  char b[16];
  std::snprintf(b, sizeof b, "0x%08x", n);
  return b;
}

bool Contains(const std::vector<uint32_t>& v, uint32_t x) { return std::find(v.begin(), v.end(), x) != v.end(); }

}  // namespace

std::vector<uint32_t> ShaperContent::SkinsFor(uint32_t shaper) const {
  std::vector<uint32_t> out{shaper};
  auto it = skins.find(shaper);
  if (it != skins.end())
    for (uint32_t s : it->second)
      if (s && !Contains(out, s)) out.push_back(s);
  return out;
}

std::vector<int> RevealMembers(const ShaperPick& p) {
  if (p.page) return {kPlayerStatus, kPlayerRole, kPlayerLoadoutId, kPlayerLoadoutVersion, kPlayerNounDef};
  return {kPlayerStatus, kPlayerRole, kPlayerNounDef};
}

Bytes RevealLoadout(const ShaperPick& p) {
  if (!p.page) return {};
  return BuildPreGameLoadout(p.gameplayIndex, p.loadout, p.modifiers);
}

std::vector<LoadoutAttribute> LoadoutCreatureModifiers(const std::vector<LoadoutAttribute>& attributes) {
  std::vector<LoadoutAttribute> out;
  for (const LoadoutAttribute& a : attributes)
    if (a.op != kLoadoutOpReadByPlayer && a.op != 0) out.push_back(a);  // op 0 (NoOp) changes nothing
  return out;
}

std::vector<uint32_t> PageModifiersToRenew(const std::vector<uint32_t>& page, const std::vector<uint32_t>& active) {
  std::vector<uint32_t> out;
  for (uint32_t m : page) {
    if (!m) continue;
    if (std::find(active.begin(), active.end(), m) != active.end()) continue;
    if (std::find(out.begin(), out.end(), m) != out.end()) continue;
    out.push_back(m);
  }
  return out;
}

PlayerGlobalValues RevealValues(const ShaperPick& p) {
  PlayerGlobalValues v;
  v.status = p.status;
  v.role = p.role;
  v.loadoutId = p.page;
  v.loadoutVersion = p.pageVersion;
  v.noun = p.skin;
  return v;
}

ShaperSelect::ShaperSelect(const ShaperSelectConfig& config)
    : timers_(config.timers), humanDefault_(config.humanDefault), rng_(config.seed) {
  for (const ShaperSelectPlayer& sp : config.players) {
    Player& p = players_[sp.gameplayIndex];
    p.team = sp.team;
    p.scripted = sp.scripted;
    p.plan = sp.plan;
    if (sp.scripted && sp.plan.role < kNumRoles) p.rec.role = sp.plan.role;  // its intention shows from the start
    p.content = sp.content;
    p.rec.index = sp.gameplayIndex;
  }
}

void ShaperSelect::AddHuman(ConnId conn, uint8_t gameplayIndex, uint8_t team) {
  auto it = players_.find(gameplayIndex);
  if (it != players_.end() && it->second.scripted) {
    Logf("%s    shaper select: gameplay index %u was a scripted player's; it is the connection's now", Clock().c_str(),
         gameplayIndex);
    players_.erase(it);
    it = players_.end();
  }
  const bool fresh = it == players_.end();
  if (fresh) {
    Player& p = players_[gameplayIndex];
    p.content = humanDefault_;
    p.rec.index = gameplayIndex;
    it = players_.find(gameplayIndex);
  }
  Player& p = it->second;
  if (p.team != team && p.bound)
    Logf("%s    shaper select: player %u rejoins with team %u (was %u)", Clock().c_str(), gameplayIndex, team, p.team);
  p.team = team;
  p.conn = conn;
  p.bound = true;
  p.gone = false;
  p.open = true;
  p.ready = false;  // a returning player's new client has no shaper select until its C8 02 (Q2)
  p.knowsAll = true;  // until the session's SetKnown for the new client (G3a)
  p.knows.clear();
  if (p.content.list.empty()) {
    Logf("%s    shaper select: player %u has no shaper list; it gets alpha.Noun (owned)", Clock().c_str(),
         gameplayIndex);
    ShaperListEntry e;
    e.noun = kNounAlpha;
    e.flags = kShaperOwned;
    p.content.list.push_back(e);
  }
  // G3a: a player new to shaper select after phase 1 gets what the timers gave everyone else, so its pick is never 0
  // (mpNounDef 0 asserts at C0 01). A player that was there already (a rejoin) has its record as the phases left it.
  if (!fresh || phase_ < 2) return;
  p.rec.shaper = p.rec.skin = Resolve(gameplayIndex);
  p.rec.flags |= kRecordShaperLocked;
  if (phase_ >= 3) p.rec.flags |= kRecordRoleLocked;
  if (phase_ == 4) p.skinFinal = true;
  Logf("%s    shaper select: player %u joins in phase %d; its shaper is locked to %s", Clock().c_str(), gameplayIndex,
       phase_, NounText(p.rec.shaper).c_str());
}

void ShaperSelect::SetKnown(ConnId conn, const std::vector<uint8_t>& indices) {
  auto ob = observers_.find(conn);
  if (ob != observers_.end()) {  // B15b: an observer's client has records of the players it knew at B6
    ob->second.knowsAll = false;
    ob->second.knows = std::set<uint8_t>(indices.begin(), indices.end());
    return;
  }
  Player* p = ByConn(conn);
  if (!p) return;
  p->knowsAll = false;
  p->knows = std::set<uint8_t>(indices.begin(), indices.end());
  p->knows.insert(p->rec.index);  // HelloPlayer's own record (type 0)
}

void ShaperSelect::RemoveHuman(ConnId conn) {
  observers_.erase(conn);  // B15b
  if (Player* p = ByConn(conn)) {
    p->gone = true;
    p->open = false;
  }
}

void ShaperSelect::AddObserver(ConnId conn) {
  observers_[conn] = Observer();
  Logf("%s    shaper select: connection %llu is an observer (a spectator: no record, both teams' records)",
       Clock().c_str(), static_cast<unsigned long long>(conn));
}

bool ShaperSelect::HasObserver(ConnId conn) const { return observers_.count(conn) != 0; }

// B15b: the observer's C8 01 (its records exist then: ShaperSelect_Init creates them right after sending it): every
// record it knows, whatever the team and phase, then the current phase (none before the first player's C8 02).
void ShaperSelect::OnObserverReady(ConnId conn, int64_t serverMs, ISender& out) {
  auto it = observers_.find(conn);
  if (it == observers_.end()) {
    Logf("%s    shaper select: observer C8 01 from a connection without an observer; ignored", Clock().c_str());
    return;
  }
  Advance(serverMs, out);
  Observer& o = it->second;
  if (o.ready) return;
  o.ready = true;
  int sent = 0;
  for (const auto& kv : players_) {
    if (!o.Knows(kv.first)) continue;
    const ShaperRecord& r = kv.second.rec;
    char what[128];
    std::snprintf(what, sizeof what,
                  "shaper select: current record %u for an observer (shaper %s, skin %s, role %u, flags 0x%x)", r.index,
                  NounText(r.shaper).c_str(), NounText(r.skin).c_str(), r.role, r.flags);
    out.Send(conn, BuildShaperRecord(r), what);
    ++sent;
  }
  // without a human player (a game of bots that spectators watch) nobody sends C8 02: the first observer starts phase 1
  bool humans = false;
  for (const auto& kv : players_) humans = humans || !kv.second.scripted;
  if (phase_ == 0 && !humans) {
    Logf("%s    shaper select: no human player; observer %llu's C8 01 starts phase 1", Clock().c_str(),
         static_cast<unsigned long long>(conn));
    Enter(1, serverMs, serverMs, out);
  } else if (phase_ != 0) {
    SendPhase(conn, false, out);
  }
  Logf("%s    shaper select: observer %llu ready (its C8 01): %d record(s)%s", Clock().c_str(),
       static_cast<unsigned long long>(conn), sent, phase_ ? ", the current phase" : "; phase 1 not started yet");
  Advance(serverMs, out);
}

ShaperSelect::Player* ShaperSelect::ByConn(ConnId conn) {
  for (auto& kv : players_)
    if (!kv.second.scripted && kv.second.bound && kv.second.conn == conn) return &kv.second;
  return nullptr;
}

bool ShaperSelect::HasHuman(ConnId conn) const {
  for (const auto& kv : players_)
    if (!kv.second.scripted && kv.second.bound && kv.second.conn == conn) return true;
  return false;
}

const ShaperRecord* ShaperSelect::Record(uint8_t gameplayIndex) const {
  auto it = players_.find(gameplayIndex);
  return it == players_.end() ? nullptr : &it->second.rec;
}

bool ShaperSelect::HeldByTeammate(uint8_t index, uint32_t noun) const {
  if (noun == 0 || noun == kNounRandom) return false;
  const Player& me = players_.at(index);
  for (const auto& kv : players_)
    if (kv.first != index && kv.second.team == me.team && kv.second.rec.shaper == noun) return true;
  return false;
}

bool ShaperSelect::Selectable(uint8_t index, uint32_t noun) const {
  if (noun == 0) return false;
  if (noun == kNounRandom) return true;
  if (HeldByTeammate(index, noun)) return false;
  const Player& p = players_.at(index);
  if (p.content.list.empty()) return p.scripted;
  for (const ShaperListEntry& e : p.content.list)
    if (e.noun == noun) return (e.flags & kShaperSelectableFlags) && !(e.flags & kShaperHidden);
  return false;
}

// The player's selectable list entries that no teammate holds, in list order; for a scripted player without a list,
// every selectable entry of every list in the match.
std::vector<uint32_t> ShaperSelect::FreeEntries(uint8_t index) const {
  const Player& me = players_.at(index);
  std::vector<uint32_t> out;
  auto take = [&](const std::vector<ShaperListEntry>& list) {
    for (const ShaperListEntry& e : list)
      if ((e.flags & kShaperSelectableFlags) && !(e.flags & kShaperHidden) && e.noun && e.noun != kNounRandom &&
          !HeldByTeammate(index, e.noun) && !Contains(out, e.noun))
        out.push_back(e.noun);
  };
  if (!me.content.list.empty()) {
    take(me.content.list);
  } else {
    for (const auto& kv : players_) take(kv.second.content.list);
    take(humanDefault_.list);
  }
  return out;
}

// The shaper a lock makes of the record: its pick if still free, a random free entry for Random, else the first free
// entry (GMS_PROTOCOL.md: C8 05 and the phase-1 timer).
uint32_t ShaperSelect::Resolve(uint8_t index) {
  const Player& p = players_.at(index);
  const uint32_t s = p.rec.shaper;
  if (s != 0 && s != kNounRandom && !HeldByTeammate(index, s)) return s;
  const std::vector<uint32_t> free = FreeEntries(index);
  if (!free.empty()) return s == kNounRandom ? free[rng_() % free.size()] : free.front();
  const uint32_t fallback = !p.content.list.empty() ? p.content.list.front().noun : kNounAlpha;
  Logf("%s    shaper select: player %u has no free shaper; it gets %s", Clock().c_str(), index,
       NounText(fallback).c_str());
  return fallback;
}

void ShaperSelect::Refuse(uint8_t index, const char* what, const char* why) const {
  Logf("%s    shaper select: player %u: %s ignored (%s)", Clock().c_str(), index, what, why);
}

void ShaperSelect::SendRecord(uint8_t index, ISender& out) {
  const ShaperRecord& r = players_.at(index).rec;
  const uint8_t team = players_.at(index).team;
  char what[120];
  std::snprintf(what, sizeof what, "shaper select: record %u (shaper %s, skin %s, role %u, flags 0x%x)", r.index,
                NounText(r.shaper).c_str(), NounText(r.skin).c_str(), r.role, r.flags);
  const Bytes m = BuildShaperRecord(r);
  for (const auto& kv : players_)
    if (kv.second.Receives() && kv.second.Knows(index) && (phase_ == 4 || kv.second.team == team))
      out.Send(kv.second.conn, m, what);
  for (const auto& kv : observers_)  // B15b: an observer sees both teams in every phase
    if (kv.second.Receives() && kv.second.Knows(index)) out.Send(kv.first, m, what);
}

void ShaperSelect::SendPhase(ConnId conn, bool quiet, ISender& out) {
  const Bytes m = BuildShaperPhase(phaseEnd_, static_cast<uint32_t>(phase_));
  if (quiet) {
    out.SendQuiet(conn, m);
    return;
  }
  char what[80];
  std::snprintf(what, sizeof what, "shaper select: phase %d until server ms %llu", phase_,
                static_cast<unsigned long long>(phaseEnd_));
  out.Send(conn, m, what);
}

void ShaperSelect::Pick(uint8_t index, uint32_t noun, ISender& out) {
  Player& p = players_.at(index);
  if (!Selectable(index, noun)) {
    Refuse(index, ("scripted pick " + NounText(noun)).c_str(), "not selectable for it, or a teammate holds it");
    return;
  }
  p.rec.shaper = p.rec.skin = noun;
  SendRecord(index, out);
}

// Locks the record (C8 05, a scripted player's plan, the phase-1 timer): the record to the team, then for a human in
// shaper select its skins (C8 0B, before C8 05 01 and before phase 3) and, if it asked, the answer.
void ShaperSelect::Lock(uint8_t index, bool answer, ISender& out) {
  Player& p = players_.at(index);
  const uint32_t s = Resolve(index);
  p.rec.shaper = p.rec.skin = s;
  p.rec.flags |= kRecordShaperLocked;
  SendRecord(index, out);
  if (!p.Receives()) return;
  const std::vector<uint32_t> skins = p.content.SkinsFor(s);
  out.Send(p.conn, BuildSkins(skins),
           "shaper select: skins of " + NounText(s) + " (" + std::to_string(skins.size()) + ")");
  if (answer) out.Send(p.conn, BuildShaperLockResult(s), "shaper select: lock answer " + NounText(s));
}

void ShaperSelect::Enter(int phase, int64_t at, int64_t now, ISender& out) {
  phase_ = phase;
  phaseStart_ = static_cast<uint64_t>(at);
  phaseEnd_ = phaseStart_ + timers_.ForPhase(phase);
  for (const auto& kv : players_)
    if (kv.second.Receives()) SendPhase(kv.second.conn, false, out);
  for (const auto& kv : observers_)
    if (kv.second.Receives()) SendPhase(kv.first, false, out);
  nextRepeat_ = now + 1000;
  if (phase == 2) AnnounceFollowerRoles(out);
}

// Phase 3 -> 4: every skin settled (last C8 0C/0D, else the saved skin if owned, else the default), C8 01 ... 04 to
// all, then the other team's records to each client.
void ShaperSelect::Reveal(int64_t at, int64_t now, ISender& out) {
  for (auto& kv : players_) {
    Player& p = kv.second;
    if (p.skinFinal || p.skinChosen) continue;
    uint32_t skin = p.rec.shaper;
    auto saved = p.content.savedSkins.find(p.rec.shaper);
    if (saved != p.content.savedSkins.end() && Contains(p.content.SkinsFor(p.rec.shaper), saved->second))
      skin = saved->second;
    if (p.rec.skin != skin) {
      p.rec.skin = skin;
      SendRecord(kv.first, out);
    }
  }
  Enter(4, at, now, out);
  for (const auto& to : players_) {
    if (!to.second.Receives()) continue;
    for (const auto& kv : players_) {
      if (kv.second.team == to.second.team || !to.second.Knows(kv.first)) continue;
      const ShaperRecord& r = kv.second.rec;
      char what[120];
      std::snprintf(what, sizeof what, "shaper select: reveal, record %u (shaper %s, skin %s, role %u, flags 0x%x)",
                    r.index, NounText(r.shaper).c_str(), NounText(r.skin).c_str(), r.role, r.flags);
      out.Send(to.second.conn, BuildShaperRecord(r), what);
    }
  }
}

// A preferred-role toggle (C8 06) of player `index` to its team's connected clients and to the observers.
void ShaperSelect::SendRoleToggle(uint8_t index, uint8_t role, ISender& out) {
  const Player& p = players_.at(index);
  for (const auto& kv : players_)
    if (kv.second.Receives() && kv.second.team == p.team && kv.second.Knows(index))
      out.Send(kv.second.conn, BuildRoleToggle(index, role),
               "shaper select: preferred role " + std::to_string(role) + " of player " + std::to_string(index));
  for (const auto& kv : observers_)  // B15b [inferred: the observer's slots show both teams]
    if (kv.second.Receives() && kv.second.Knows(index))
      out.Send(kv.first, BuildRoleToggle(index, role),
               "shaper select: preferred role " + std::to_string(role) + " of player " + std::to_string(index) +
                   " for an observer");
}

// The scripted players say which role they intend to play (the humans' teammates see it as a preferred role) when the
// role phase begins; the humans still have everything ahead of them.
void ShaperSelect::AnnounceFollowerRoles(ISender& out) {
  for (auto& kv : players_) {
    Player& p = kv.second;
    if (!p.scripted || p.plan.role >= kNumRoles || (p.preferredRoles & (1u << p.plan.role))) continue;
    p.preferredRoles |= static_cast<uint8_t>(1u << p.plan.role);
    SendRoleToggle(kv.first, p.plan.role, out);
  }
}

// How many players a team wants in a role: the 5v5 setup has two Gladiators and one of each other role.
static int WantedInRole(int role) { return role == 0 ? 2 : 1; }

// Scripted players and humans without a connection lock their role. A scripted player takes its plan role unless the
// team (the humans' roles, as selected or locked, and the other scripted players') already has more of it than the
// setup wants; then it takes a role from `altRoles` that the team lacks, if its class can play one, and says so with
// a toggle of the old and the new role.
void ShaperSelect::LockFollowerRoles(ISender& out) {
  std::map<uint8_t, std::array<int, kNumRoles>> count;
  for (const auto& kv : players_) {
    const Player& p = kv.second;
    const uint8_t role = p.scripted ? p.plan.role : p.rec.role;
    if (role < kNumRoles) ++count[p.team][role];
  }
  for (auto& kv : players_) {
    Player& p = kv.second;
    if (p.Acting() || (p.rec.flags & kRecordRoleLocked)) continue;
    if (p.scripted) {
      p.rec.role = p.plan.role;
      std::array<int, kNumRoles>& n = count[p.team];
      if (p.plan.role < kNumRoles && n[p.plan.role] > WantedInRole(p.plan.role)) {
        for (uint8_t r = 0; r < kNumRoles; ++r) {
          if (!(p.plan.altRoles & (1u << r)) || r == p.plan.role || n[r] >= WantedInRole(r)) continue;
          Logf("%s    shaper select: scripted player %u adjusts its role %u -> %u (its team has %d of %u, wants %d)",
               Clock().c_str(), kv.first, p.plan.role, r, n[p.plan.role], p.plan.role, WantedInRole(p.plan.role));
          --n[p.plan.role];
          ++n[r];
          p.preferredRoles = static_cast<uint8_t>((p.preferredRoles & ~(1u << p.plan.role)) | (1u << r));
          SendRoleToggle(kv.first, p.plan.role, out);
          SendRoleToggle(kv.first, r, out);
          p.rec.role = r;
          break;
        }
      }
    }
    p.rec.flags |= kRecordRoleLocked;
    SendRecord(kv.first, out);
  }
}

void ShaperSelect::Advance(int64_t now, ISender& out) {
  if (phase_ == 0) return;
  const uint64_t t = static_cast<uint64_t>(now);
  for (bool again = true; again;) {
    again = false;
    if (phase_ == 1) {
      for (auto& kv : players_) {
        Player& p = kv.second;
        if (!p.scripted || (p.rec.flags & kRecordShaperLocked)) continue;
        const uint64_t pickAt = phaseStart_ + static_cast<uint64_t>(std::max(0, p.plan.pickMs));
        const uint64_t lockAt = phaseStart_ + static_cast<uint64_t>(std::max(0, std::max(p.plan.pickMs, p.plan.lockMs)));
        if (!p.planPicked && p.plan.noun && t >= pickAt) {
          p.planPicked = true;
          Pick(kv.first, p.plan.noun, out);
        }
        if (t >= lockAt) Lock(kv.first, false, out);
      }
      bool all = true;
      for (const auto& kv : players_) all = all && (kv.second.rec.flags & kRecordShaperLocked);
      if (all) {
        Enter(2, now, now, out);
        again = true;
      } else if (t >= phaseEnd_) {
        for (auto& kv : players_)
          if (!(kv.second.rec.flags & kRecordShaperLocked)) Lock(kv.first, false, out);
        Enter(2, static_cast<int64_t>(phaseEnd_), now, out);
        again = true;
      }
    } else if (phase_ == 2) {
      bool humans = true, all = true;
      for (const auto& kv : players_)
        if (kv.second.Acting()) humans = humans && (kv.second.rec.flags & kRecordRoleLocked);
      if (humans) LockFollowerRoles(out);
      for (const auto& kv : players_) all = all && (kv.second.rec.flags & kRecordRoleLocked);
      if (all) {
        Enter(3, now, now, out);
        again = true;
      } else if (t >= phaseEnd_) {
        LockFollowerRoles(out);
        Enter(3, static_cast<int64_t>(phaseEnd_), now, out);
        again = true;
      }
    } else if (phase_ == 3) {
      bool humans = true;
      for (const auto& kv : players_)
        if (kv.second.Acting()) humans = humans && kv.second.skinFinal;
      if (humans) {
        Reveal(now, now, out);
      } else if (t >= phaseEnd_) {
        Reveal(static_cast<int64_t>(phaseEnd_), now, out);
      }
    }
  }
  if (now >= nextRepeat_) {
    for (const auto& kv : players_)
      if (kv.second.Receives()) SendPhase(kv.second.conn, true, out);
    for (const auto& kv : observers_)
      if (kv.second.Receives()) SendPhase(kv.first, true, out);
    nextRepeat_ = now + 1000;
  }
}

void ShaperSelect::OnReady(ConnId conn, int64_t serverMs, ISender& out) {
  Player* p = ByConn(conn);
  if (!p) {
    Logf("%s    shaper select: C8 02 from a connection without a player; ignored", Clock().c_str());
    return;
  }
  Advance(serverMs, out);
  p->ready = true;
  const uint8_t index = p->rec.index;
  out.Send(conn, BuildShaperList(p->content.list),
           "shaper select: list of player " + std::to_string(index) + " (" +
               std::to_string(p->content.list.size()) + " entries)");
  for (const auto& kv : players_) {
    if ((phase_ != 4 && kv.second.team != p->team) || !p->Knows(kv.first)) continue;
    const ShaperRecord& r = kv.second.rec;
    char what[120];
    std::snprintf(what, sizeof what, "shaper select: current record %u (shaper %s, skin %s, role %u, flags 0x%x)",
                  r.index, NounText(r.shaper).c_str(), NounText(r.skin).c_str(), r.role, r.flags);
    out.Send(conn, BuildShaperRecord(r), what);
  }
  if (!p->content.pages.empty()) {
    std::vector<uint32_t> ids;
    for (const PerkPage& pg : p->content.pages) ids.push_back(pg.id);
    out.Send(conn, BuildPerkPages(ids), "shaper select: perk pages (" + std::to_string(ids.size()) + ")");
  }
  if (p->rec.flags & kRecordShaperLocked) {
    // locked while this client had no shaper select (a late C8 02, a returning player): its skins before the phase,
    // as C8 0B must come before phase 3 (Q2)
    const std::vector<uint32_t> skins = p->content.SkinsFor(p->rec.shaper);
    out.Send(conn, BuildSkins(skins),
             "shaper select: skins of " + NounText(p->rec.shaper) + " (" + std::to_string(skins.size()) + ")");
  }
  if (phase_ == 0) {
    Enter(1, serverMs, serverMs, out);  // the first C8 02 starts phase 1 (and sends it to every ready client)
  } else {
    SendPhase(conn, false, out);
  }
  Advance(serverMs, out);
}

void ShaperSelect::OnMessage(ConnId conn, const uint8_t* body, size_t n, int64_t serverMs, ISender& out) {
  Advance(serverMs, out);  // a timer that ran out first decides
  Player* pp = ByConn(conn);
  ShaperSelectRequest r;
  if (!pp || !ParseShaperSelectRequest(body, n, &r)) {
    Logf("%s    shaper select: C8 %s from %s; ignored", Clock().c_str(), Hex(body, n).c_str(),
         pp ? "its player (unknown or short)" : "a connection without a player");
    return;
  }
  Player& p = *pp;
  const uint8_t index = p.rec.index;
  const std::string what = std::string(ShaperRequestName(r.sub)) + " " + NounText(r.id);
  switch (r.sub) {
    case kShaperHello:
    case kShaperReady:  // the session hands C8 02 to OnReady
      break;
    case kShaperRequest: {
      const char* why = phase_ != 1                                ? "not in phase 1"
                        : (p.rec.flags & kRecordShaperLocked)      ? "the shaper is locked"
                        : HeldByTeammate(index, r.id)              ? "a teammate holds it"
                        : !Selectable(index, r.id)                 ? "not selectable (not in the list, unowned or hidden)"
                                                                   : nullptr;
      if (why) {
        out.Send(conn, BuildShaperRequestResult(r.id, false),
                 "shaper select: request " + NounText(r.id) + " refused (" + why + ")");
        break;
      }
      p.rec.shaper = p.rec.skin = r.id;
      SendRecord(index, out);
      out.Send(conn, BuildShaperRequestResult(r.id, true), "shaper select: request " + NounText(r.id) + " accepted");
      break;
    }
    case kShaperLockIn:
      if (phase_ == 1 && !(p.rec.flags & kRecordShaperLocked)) {
        if (r.id != p.rec.shaper)
          Logf("%s    shaper select: player %u locks %s; its record holds %s", Clock().c_str(), index,
               NounText(r.id).c_str(), NounText(p.rec.shaper).c_str());
        Lock(index, true, out);
      } else if (phase_ <= 2) {  // locked meanwhile (the timer): the UI still waits for the answer, never 00
        out.Send(conn, BuildShaperLockResult(p.rec.shaper), "shaper select: lock answer (already locked) " +
                                                                NounText(p.rec.shaper));
      } else {
        Refuse(index, what.c_str(), "after phase 2");
      }
      break;
    case kShaperPreferredRole:
      if (r.role >= kNumRoles || phase_ < 1 || phase_ > 3) {
        Refuse(index, "preferred role", r.role >= kNumRoles ? "no such role" : "wrong phase");
        break;
      }
      p.preferredRoles ^= static_cast<uint8_t>(1u << r.role);
      SendRoleToggle(index, r.role, out);
      break;
    case kShaperSelectRole:
      if (r.role >= kNumRoles || phase_ < 1 || phase_ > 2 || (p.rec.flags & kRecordRoleLocked)) {
        Refuse(index, "role", r.role >= kNumRoles ? "no such role" : "wrong phase or role locked");
        break;
      }
      if (p.rec.role != r.role) {
        p.rec.role = r.role;
        SendRecord(index, out);
      }
      break;
    case kShaperLockRole:
      if (r.role >= kNumRoles || phase_ < 2 || phase_ > 3 || (p.rec.flags & kRecordRoleLocked)) {
        Refuse(index, "lock role", r.role >= kNumRoles ? "no such role" : "wrong phase or role locked");
        break;
      }
      p.rec.role = r.role;
      p.rec.flags |= kRecordRoleLocked;
      SendRecord(index, out);
      break;
    case kShaperPerkPage: {
      auto pg = std::find_if(p.content.pages.begin(), p.content.pages.end(),
                             [&](const PerkPage& x) { return x.id == r.id; });
      // D3-perks (d3perks_run1): the client sends C8 0A whenever its loadout drop-down selects a page, also at once on
      // our C8 09 (phase 1: its preselection) and on entering phase 2, not only before 08: so phases 1-3, until the
      // role lock
      if (phase_ < 1 || phase_ > 3 || (p.rec.flags & kRecordRoleLocked) || pg == p.content.pages.end()) {
        Refuse(index, what.c_str(),
               pg == p.content.pages.end() ? "not one of its pages" : "wrong phase or role locked");
        break;
      }
      p.rec.page = pg->id;  // record bytes 10-13 (read by nothing); goes out with the role lock that follows
      p.pageVersion = pg->version;
      break;
    }
    case kShaperSelectSkin:
    case kShaperLockSkin: {
      if (phase_ != 3 || p.skinFinal || !Contains(p.content.SkinsFor(p.rec.shaper), r.id)) {
        Refuse(index, what.c_str(), phase_ != 3 ? "not in phase 3" : p.skinFinal ? "skin locked" : "not in its skins");
        break;
      }
      p.skinChosen = true;
      if (r.sub == kShaperLockSkin) p.skinFinal = true;
      if (p.rec.skin != r.id) {
        p.rec.skin = r.id;
        SendRecord(index, out);
      }
      break;
    }
    case kShaperSelectVanity: {
      const std::vector<uint32_t>& owned = r.kind == kVanityWard ? p.content.wards : p.content.announcers;
      if (r.kind > kVanityAnnouncer || phase_ < 1 || phase_ > 3 || (!owned.empty() && !Contains(owned, r.id))) {
        Refuse(index, what.c_str(), r.kind > kVanityAnnouncer ? "no such kind" : "wrong phase or not owned");
        break;
      }
      (r.kind == kVanityWard ? p.ward : p.announcer) = r.id;
      out.Send(conn, BuildVanity(r.kind, r.id),
               std::string("shaper select: ") + (r.kind == kVanityWard ? "ward " : "announcer ") + NounText(r.id));
      break;
    }
    default:
      break;
  }
  Advance(serverMs, out);
}

void ShaperSelect::Tick(int64_t serverMs, ISender& out) { Advance(serverMs, out); }

void ShaperSelect::Close(ConnId conn) {
  auto ob = observers_.find(conn);
  if (ob != observers_.end()) ob->second.open = false;
  if (Player* p = ByConn(conn)) p->open = false;
}

std::vector<ShaperPick> ShaperSelect::Picks() const {
  std::vector<ShaperPick> out;
  for (const auto& kv : players_) {
    const Player& p = kv.second;
    ShaperPick k;
    k.gameplayIndex = kv.first;
    k.team = p.team;
    k.scripted = p.scripted;
    k.shaper = p.rec.shaper;
    k.skin = p.rec.skin ? p.rec.skin : p.rec.shaper;
    k.role = p.rec.role;
    k.page = p.rec.page;
    k.pageVersion = p.pageVersion;
    if (k.page)
      for (const PerkPage& pg : p.content.pages)
        if (pg.id == k.page) {
          k.loadout = pg.attributes;
          k.modifiers = pg.modifiers;
          break;
        }
    k.ward = p.ward;
    k.announcer = p.announcer;
    out.push_back(k);
  }
  return out;
}

std::string ShaperSelect::Describe() const {
  const std::string obs =
      observers_.empty() ? std::string() : "; " + std::to_string(observers_.size()) + " observer(s)";  // B15b
  if (phase_ == 0) return "shaper select waiting for C8 02" + obs;
  std::string s = "shaper select phase " + std::to_string(phase_) + obs;
  if (phase_ == 4) {
    s += "; picks";
    for (const ShaperPick& k : Picks()) {
      s += " " + std::to_string(k.gameplayIndex) + "=" + NounText(k.shaper) + "/" + NounText(k.skin) + " r" +
           std::to_string(k.role);
      if (k.page) s += " p" + NounText(k.page);
    }
  }
  return s;
}

}  // namespace gms
