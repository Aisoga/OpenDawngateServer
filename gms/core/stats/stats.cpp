// M18 (MSTATS1): the match statistics' accumulator; the rules are in stats.h.
#include "core/stats/stats.h"

#include <algorithm>
#include <cmath>

namespace gms::stats {

namespace ctl = dggms::control;

namespace {

bool ValidSeat(int s) { return s >= 0 && s < kSeats; }
bool ValidTeam(int t) { return t == 1 || t == 2; }

std::int64_t Round(double v) { return static_cast<std::int64_t>(std::llround(v)); }

// The order events are dropped in when a line is too long (lowest first).
int EventPriority(const std::string& kind) {
  if (kind == "kill") return 0;
  if (kind == "well") return 1;
  if (kind == "structure") return 2;
  return 3;  // guardian, surrender
}

// The shortest event line: {"ms":0,"kind":"kill","a":0,"b":0}, = 35 bytes with its comma; removing ceil(excess / 34)
// events removes at least `excess` bytes.
constexpr std::size_t kMinEventBytes = 34;

}  // namespace

Kind KindOf(bool playerControlled, int npcType) {
  if (playerControlled) return Kind::Shapers;
  switch (npcType) {
    case 0: case 1: case 2: return Kind::Minions;
    case 3: return Kind::Jungle;
    case 6: case 7: case 8: return Kind::Objectives;
    case 9: case 14: case 15: return Kind::Structures;
    case 13: return Kind::Guardian;
    case 10: return Kind::Wells;
    case 12: return Kind::Workers;
    case 11: return Kind::Pets;
    case 16: return Kind::Wards;
    default: return Kind::Other;
  }
}

const char* KindName(Kind k) {
  switch (k) {
    case Kind::Shapers: return "Shapers";
    case Kind::Minions: return "Minions";
    case Kind::Jungle: return "Jungle";
    case Kind::Objectives: return "Objectives";
    case Kind::Structures: return "Structures";
    case Kind::Guardian: return "Guardian";
    case Kind::Wells: return "Wells";
    case Kind::Workers: return "Workers";
    case Kind::Pets: return "Pets";
    case Kind::Wards: return "Wards";
    default: return "Other";
  }
}

Accumulator::Accumulator() = default;

void Accumulator::Add(int seat, const std::string& key, double v) {
  if (!ValidSeat(seat) || v == 0.0) return;
  seats_[seat].sums[key] += v;
}

void Accumulator::AddStat(int seat, const std::string& key, double value) { Add(seat, key, value); }

void Accumulator::AddSource(int seat, const std::string& kind, int slot, std::int64_t id, double damage, double heal) {
  if (!ValidSeat(seat) || (damage == 0.0 && heal == 0.0)) return;
  auto& row = seats_[seat].sources[std::make_tuple(kind, kind == "ability" ? slot : -1, id)];
  row.first += damage;
  row.second += heal;
}

void Accumulator::OnHit(const Hit& h) {
  // health costs (no snapshot), self-inflicted hits and hits on the own team count nowhere
  if (!h.snapshot || (h.descLo & kDescSelfInflicted) || h.sameTeam) {
    ++skipped_;
    return;
  }
  ++hits_;
  const double effective = std::max(0.0, static_cast<double>(h.hpBefore) - static_cast<double>(h.hpAfter));
  const bool physical = (h.descLo & kDescPhysical) != 0, magical = (h.descLo & kDescMagical) != 0,
             isTrue = (h.descLo & kDescTrue) != 0;
  if (ValidSeat(h.attackerSeat) && h.attackerSeat != h.targetSeat) {
    const int s = h.attackerSeat;
    Add(s, "damageDealt", effective);
    if (physical) Add(s, "damageDealtPhysical", effective);
    if (magical) Add(s, "damageDealtMagical", effective);
    if (isTrue) Add(s, "damageDealtTrue", effective);
    // WARDSTAT: no damageToWards (a ward has no combatant in 854: the key could only be 0)
    if (h.targetKind != Kind::Wards) Add(s, std::string("damageTo") + KindName(h.targetKind), effective);
    if (h.descLo & kDescOverload) Add(s, "damageOverload", effective);
    // overkill: what a killing blow dealt beyond the HP left (shields excluded: `dealt` is after the absorb)
    if (h.lethal) Add(s, "overkill", std::max(0.0, static_cast<double>(h.dealt) - effective));
    seats_[s].largest = std::max(seats_[s].largest, effective);
    // MSTATS3: the same part to its source, so the rows sum to damageDealt
    AddSource(s, h.source.kind, h.source.slot, h.source.id, effective, 0.0);
  }
  if (ValidSeat(h.targetSeat) && h.attackerSeat != h.targetSeat) {
    const int s = h.targetSeat;
    Add(s, "damageTaken", effective);
    if (physical) Add(s, "damageTakenPhysical", effective);
    if (magical) Add(s, "damageTakenMagical", effective);
    if (isTrue) Add(s, "damageTakenTrue", effective);
    Add(s, ValidSeat(h.attackerSeat) ? "damageTakenFromShapers" : "damageTakenFromNpcs", effective);
    Add(s, "damageAbsorbed", std::max(0.0f, h.absorbed));
  }
}

void Accumulator::OnHeal(const HealEvent& h) {
  ++heals_;
  // a heal of a dead object resurrects it (0x008E0720): no healing; HP gained = this call's own part
  if (!(h.hpBefore > 0.f)) return;
  const double effective = std::max(0.0, static_cast<double>(h.hpAfter) - static_cast<double>(h.hpBefore));
  if (ValidSeat(h.targetSeat)) Add(h.targetSeat, "healingReceived", effective);
  if (!h.snapshot || !ValidSeat(h.healerSeat) || !h.sameTeam) return;
  const int s = h.healerSeat;
  Add(s, "healingDone", effective);
  if (h.targetSeat == s)
    Add(s, "healingSelf", effective);
  else if (h.targetPlayer)
    Add(s, "healingAllies", effective);
  else
    Add(s, "healingOther", effective);
  if (h.lifesteal) Add(s, "lifeDrain", effective);
  if (h.healed >= 0.f) Add(s, "overheal", std::max(0.0, static_cast<double>(h.healed) - effective));
  if (h.source.kind == "role" && h.source.role >= 0) Add(s, std::string("healRole") + RoleName(h.source.role), effective);
  AddSource(s, h.source.kind, h.source.slot, h.source.id, 0.0, effective);
}

void Accumulator::OnShieldAbsorb(int giverSeat, int targetSeat, bool sameTeam, double absorbed) {
  if (!ValidSeat(giverSeat) || giverSeat == targetSeat || !sameTeam || !(absorbed > 0.0)) return;
  Add(giverSeat, "shieldingDone", absorbed);
}

void Accumulator::SourceSums(int seat, double* damage, double* heal) const {
  *damage = *heal = 0.0;
  if (!ValidSeat(seat)) return;
  for (const auto& kv : seats_[seat].sources) {
    *damage += kv.second.first;
    *heal += kv.second.second;
  }
}

void Accumulator::OnKill(int creditSeat, int creditTeam, Kind victimKind, int victimTeam) {
  // WARDSTAT: no killsWards (a ward has no combatant in 854, so nothing hits or kills it: the key could only be 0)
  if (!ValidSeat(creditSeat) || victimKind == Kind::Shapers || victimKind == Kind::Wards) return;
  if (ValidTeam(creditTeam) && creditTeam == victimTeam) return;  // a deny, a worker killed by its own side
  Add(creditSeat, std::string("kills") + KindName(victimKind), 1.0);
}

void Accumulator::OnPlayerKill(std::int64_t ms, int killerSeat, int killerTeam, int victimSeat) {
  if (!ValidSeat(victimSeat)) return;
  const int killer = ValidSeat(killerSeat) ? killerSeat : -1;
  if (killer >= 0 && ValidTeam(killerTeam)) ++teamKills_[killerTeam];
  AddEvent(ms, "kill", killer, victimSeat);
}

void Accumulator::OnDeathTimer(int seat, std::uint64_t deathMs, std::uint64_t respawnMs) {
  if (!ValidSeat(seat) || closed_) return;
  Seat& s = seats_[seat];
  if (s.dead && deathMs > s.deathMs) s.deadMs += static_cast<double>(std::min(s.respawnMs, deathMs) - s.deathMs);
  s.dead = true;
  s.deathMs = deathMs;
  s.respawnMs = std::max(respawnMs, deathMs);
}

void Accumulator::OnWellCaptured(std::int64_t ms, int well, int team, const std::vector<int>& capturers) {
  for (int p : capturers) Add(p, "wellsCaptured", 1.0);
  AddEvent(ms, "well", team, well);
}

void Accumulator::OnStructure(std::int64_t ms, int destroyingTeam, int npcType) {
  AddEvent(ms, "structure", destroyingTeam, npcType);
}

void Accumulator::OnMatchEnd(std::int64_t ms, int winnerTeam, int surrenderTeam) {
  if (ValidTeam(surrenderTeam))
    AddEvent(ms, "surrender", surrenderTeam, 0);
  else
    AddEvent(ms, "guardian", winnerTeam, 0);
}

void Accumulator::AddEvent(std::int64_t ms, const char* kind, std::int64_t a, std::int64_t b) {
  ctl::TimelineEvent e{ms, kind, a, b};
  if (static_cast<int>(events_.size()) >= kMaxEvents) {
    ++eventsCapped_;
    // a full list keeps the rarer events: the newest kill gives way to anything else; a kill is dropped
    if (EventPriority(e.kind) == 0) return;
    for (auto it = events_.rbegin(); it != events_.rend(); ++it)
      if (EventPriority(it->kind) == 0) {
        events_.erase(std::next(it).base());
        events_.push_back(std::move(e));
        return;
      }
    return;
  }
  events_.push_back(std::move(e));
}

void Accumulator::Sample(double matchSeconds, const SeatSample seats[kSeats]) {
  if (closed_ || !(matchSeconds >= kStepSec)) return;
  const int minute = static_cast<int>(std::floor(matchSeconds / kStepSec));
  if (minute == lastMinute_ || static_cast<int>(sec_.size()) >= kMaxSamples) return;
  lastMinute_ = minute;
  const std::size_t n = sec_.size();
  sec_.push_back(static_cast<std::int64_t>(minute) * kStepSec);
  double teamGold[3] = {0.0, 0.0, 0.0};
  for (int i = 0; i < kSeats; ++i) {
    Seat& s = seats_[i];
    const SeatSample& in = seats[i];
    if (in.present && !s.sampled) {  // a seat seen first now: zeros for the samples before
      s.sampled = true;
      s.gold.assign(n, 0);
      s.xp.assign(n, 0);
      s.level.assign(n, 0);
      s.cs.assign(n, 0);
    }
    if (!s.sampled) continue;
    s.gold.push_back(in.present ? Round(in.gold) : 0);
    s.xp.push_back(in.present ? Round(in.xp) : 0);
    s.level.push_back(in.present ? in.level : 0);
    s.cs.push_back(in.present ? in.cs : 0);
    if (in.present && ValidTeam(in.team)) teamGold[in.team] += in.gold;
  }
  for (int t = 1; t <= 2; ++t) {
    teamGold_[t].push_back(Round(teamGold[t]));
    teamKillSamples_[t].push_back(teamKills_[t]);
  }
}

void Accumulator::Close(std::uint64_t endMs) {
  if (closed_) return;
  closed_ = true;
  for (Seat& s : seats_) {
    if (!s.dead) continue;
    const std::uint64_t until = std::min(s.respawnMs, endMs);
    if (until > s.deathMs) s.deadMs += static_cast<double>(until - s.deathMs);
    s.dead = false;
  }
}

double Accumulator::Stat(int seat, const std::string& key) const {
  if (!ValidSeat(seat)) return 0.0;
  const Seat& s = seats_[seat];
  if (key == "largestHit") return s.largest;
  if (key == "timeDeadMs") return s.deadMs;
  auto it = s.sums.find(key);
  return it == s.sums.end() ? 0.0 : it->second;
}

std::vector<ctl::SeatStats> Accumulator::BuildSeatStats(const SeatFacts seats[kSeats]) const {
  std::vector<ctl::SeatStats> out;
  for (int i = 0; i < kSeats; ++i) {
    const SeatFacts& f = seats[i];
    if (!f.present) continue;
    const Seat& s = seats_[i];
    ctl::SeatStats m;
    m.index = i;
    m.user = f.bot ? 0 : f.user;
    m.bot = f.bot;
    m.team = f.team;
    m.noun = f.noun;
    m.role = f.role;
    // always: the three the history shows first; the rest only when not 0 (an unknown key reads 0)
    m.stats["damageDealt"] = 0;
    m.stats["damageTaken"] = 0;
    for (const auto& kv : s.sums) {
      const std::int64_t v = Round(kv.second);
      if (v != 0 || kv.first == "damageDealt" || kv.first == "damageTaken") m.stats[kv.first] = v;
    }
    if (Round(s.largest) != 0) m.stats["largestHit"] = Round(s.largest);
    m.stats["timeDeadMs"] = Round(s.deadMs);
    if (f.bot) {  // the history's list columns for a bot (playerFinish's keys; dgserver gets no playerFinish for it)
      m.stats["kills"] = f.kills;
      m.stats["deaths"] = f.deaths;
      m.stats["assists"] = f.assists;
      m.stats["creeps"] = f.creeps;
      m.stats["gold"] = f.gold;
      m.stats["level"] = f.level;
    }
    // MSTATS3's rows: the largest (damage + heal) first, at most kMaxSources
    for (const auto& kv : s.sources) {
      ctl::StatSource r;
      r.kind = std::get<0>(kv.first);
      if (r.kind == "ability") r.slot = std::get<1>(kv.first);
      r.id = std::get<2>(kv.first);
      r.damage = Round(kv.second.first);
      r.heal = Round(kv.second.second);
      if (r.damage != 0 || r.heal != 0) m.sources.push_back(std::move(r));
    }
    auto largestFirst = [](const ctl::StatSource& a, const ctl::StatSource& b) {
      return a.damage + a.heal > b.damage + b.heal;
    };
    std::stable_sort(m.sources.begin(), m.sources.end(), largestFirst);
    if (m.sources.size() > static_cast<std::size_t>(kMaxSources)) {
      // MSTATS3: the rows past the cap fold into one `other` row (an existing one among the kept), so the rows still
      // sum to damageDealt and healingDone
      std::int64_t damage = 0, heal = 0;
      for (std::size_t k = kMaxSources - 1; k < m.sources.size(); ++k) {
        damage += m.sources[k].damage;
        heal += m.sources[k].heal;
      }
      m.sources.resize(kMaxSources - 1);
      auto other = std::find_if(m.sources.begin(), m.sources.end(),
                                [](const ctl::StatSource& r) { return r.kind == "other" && r.id == 0; });
      if (other == m.sources.end()) {
        ctl::StatSource r;
        r.kind = "other";
        m.sources.push_back(std::move(r));
        other = m.sources.end() - 1;
      }
      other->damage += damage;
      other->heal += heal;
      std::stable_sort(m.sources.begin(), m.sources.end(), largestFirst);
    }
    out.push_back(std::move(m));
  }
  return out;
}

ctl::MatchTimeline Accumulator::BuildTimeline(std::size_t budget, TrimReport* report) const {
  ctl::MatchTimeline tl;
  tl.stepSec = kStepSec;
  if (!sec_.empty()) {
    tl.sec = sec_;
    for (int i = 0; i < kSeats; ++i) {
      const Seat& s = seats_[i];
      if (!s.sampled) continue;
      ctl::TimelineSeat ts;
      ts.index = i;
      ts.gold = s.gold;
      ts.xp = s.xp;
      ts.level = s.level;
      ts.cs = s.cs;
      tl.seats.push_back(std::move(ts));
    }
    for (int t = 1; t <= 2; ++t) {
      ctl::TimelineTeam tt;
      tt.team = t;
      tt.gold = teamGold_[t];
      tt.kills = teamKillSamples_[t];
      tl.teams.push_back(std::move(tt));
    }
  }
  tl.events = events_;
  std::stable_sort(tl.events.begin(), tl.events.end(),
                   [](const ctl::TimelineEvent& a, const ctl::TimelineEvent& b) { return a.ms < b.ms; });
  TrimReport r;
  FitTimeline(tl, budget, &r);
  r.eventsCapped = eventsCapped_;
  if (report) *report = r;
  return tl;
}

void FitTimeline(ctl::MatchTimeline& tl, std::size_t budget, TrimReport* report) {
  TrimReport r;
  std::size_t size = ctl::Encode(tl).size();
  while (size > budget) {
    if (!tl.events.empty()) {
      // drop ceil(excess / kMinEventBytes) events, the lowest priority first, the latest of it first
      std::size_t drop = (size - budget + kMinEventBytes - 1) / kMinEventBytes;
      while (drop > 0 && !tl.events.empty()) {
        int lowest = 4;
        for (const ctl::TimelineEvent& e : tl.events) lowest = std::min(lowest, EventPriority(e.kind));
        bool removed = false;
        for (auto it = tl.events.rbegin(); it != tl.events.rend() && drop > 0;) {
          if (EventPriority(it->kind) == lowest) {
            it = std::make_reverse_iterator(tl.events.erase(std::next(it).base()));
            --drop;
            ++r.eventsDropped;
            removed = true;
          } else {
            ++it;
          }
        }
        if (!removed) break;
      }
    } else if (tl.sec.size() > 1) {
      // every second sample, counted back from the last (the end state stays)
      auto thin = [](std::vector<std::int64_t>& v) {
        if (v.empty()) return;
        std::vector<std::int64_t> kept;
        const std::size_t n = v.size();
        for (std::size_t i = 0; i < n; ++i)
          if ((n - 1 - i) % 2 == 0) kept.push_back(v[i]);
        v.swap(kept);
      };
      thin(tl.sec);
      for (ctl::TimelineSeat& s : tl.seats) {
        thin(s.gold);
        thin(s.xp);
        thin(s.level);
        thin(s.cs);
      }
      for (ctl::TimelineTeam& t : tl.teams) {
        thin(t.gold);
        thin(t.kills);
      }
      tl.stepSec *= 2;
      ++r.coarsened;
    } else {
      break;  // a single sample and no events: nothing more to drop
    }
    size = ctl::Encode(tl).size();
  }
  r.bytes = size;
  if (report) {
    report->eventsDropped = r.eventsDropped;
    report->coarsened = r.coarsened;
    report->bytes = r.bytes;
  }
}

}  // namespace gms::stats
