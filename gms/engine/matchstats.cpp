// M18 (MSTATS1): the match statistics on the match host; see matchstats.h.
#include "matchstats.h"

#include <windows.h>

#include <cmath>
#include <cstdio>
#include <string>

#include "bots.h"
#include "control/messages.h"
#include "core/stats/stats.h"
#include "game.h"
#include "host/control.h"
#include "host/host.h"
#include "log.h"
#include "match.h"
#include "matchstats_cc.h"
#include "matchstats_sources.h"
#include "matchstats_gold.h"

using namespace game;
namespace st = gms::stats;
namespace ctl = dggms::control;

namespace matchstats {
namespace {

st::Accumulator g_acc;
int g_enabled = -1;  // -1 = not read yet
bool g_sent;
unsigned g_exceptions;

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  if (++g_exceptions <= 20)
    Log("EXCEPTION in match_stats (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
        reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

// The target's kind and, for a player-controlled object, its seat (+0x4C).
bool SehTarget(void* o, bool* player, int* npcType, int* seat) {
  __try {
    *player = Field<unsigned char>(o, gobj::kPlayerControlled) != 0;
    *seat = *player ? Field<unsigned char>(o, gobj::kPlayerIndex) : -1;
    void* n = Field<void*>(o, npc::kOfObject);
    char* nd = n ? Field<char*>(n, noun::kData) : nullptr;
    *npcType = nd ? Field<int>(nd, npc::kType) : -1;
    return true;
  } __except (SehNote(GetExceptionInformation(), "the hit's target")) {
    return false;
  }
}

// One player's facts: its team (0 = no player at that index).
int SehTeamOf(int seat) {
  __try {
    void* p = GetPlayerByIndex(GetCurrentSim(), static_cast<unsigned>(seat));
    return p ? Field<unsigned char>(p, player::kTeam) : 0;
  } __except (SehNote(GetExceptionInformation(), "player team")) {
    return 0;
  }
}

bool SehSeatSample(void* sim, int seat, st::SeatSample* out) {
  __try {
    void* p = GetPlayerByIndex(sim, static_cast<unsigned>(seat));
    if (!p) return false;
    out->present = true;
    out->team = Field<unsigned char>(p, player::kTeam);
    out->gold = Field<float>(p, player::kTotalGold);
    out->xp = Field<float>(p, player::kXp);
    out->level = static_cast<int>(Field<unsigned>(p, player::kLevel));
    out->cs = static_cast<int>(Field<unsigned>(p, player::kCreeps));
    return true;
  } __except (SehNote(GetExceptionInformation(), "timeline sample")) {
    return false;
  }
}

bool SehSeatFacts(void* sim, int seat, st::SeatFacts* out) {
  __try {
    void* p = GetPlayerByIndex(sim, static_cast<unsigned>(seat));
    if (!p) return false;
    out->present = true;
    out->user = static_cast<std::int64_t>(Field<unsigned long long>(p, player::kOnlineId));
    out->team = Field<unsigned char>(p, player::kTeam);
    void* n = Field<void*>(p, player::kNounDef);
    out->noun = n ? Field<std::uint32_t>(n, noun::kId) : 0;
    out->role = 1 + Field<unsigned char>(p, player::kRole);  // as playerFinish's role (B7d)
    out->kills = static_cast<int>(Field<unsigned>(p, player::kKills));
    out->deaths = static_cast<int>(Field<unsigned>(p, player::kDeaths));
    out->assists = static_cast<int>(Field<unsigned>(p, player::kAssists));
    out->creeps = static_cast<int>(Field<unsigned>(p, player::kCreeps));
    out->level = static_cast<int>(Field<unsigned>(p, player::kLevel));
    out->gold = static_cast<std::int64_t>(std::lround(Field<float>(p, player::kTotalGold)));
    return true;
  } __except (SehNote(GetExceptionInformation(), "seat facts")) {
    return false;
  }
}

unsigned long long SehNowMs(void* sim) {
  __try {
    return GetTimeMs(sim);
  } __except (SehNote(GetExceptionInformation(), "GetTimeMs")) {
    return 0;
  }
}

std::int64_t MatchMs(double matchSeconds) { return static_cast<std::int64_t>(std::llround(matchSeconds * 1000.0)); }

}  // namespace

bool Enabled() {
  if (g_enabled < 0) {
    g_enabled = HostOptionInt("match_stats", 1) != 0 ? 1 : 0;
    Log("match_stats (MSTATS1): %s", g_enabled ? "on: seatStats and matchTimeline before the playerFinish lines"
                                               : "off ([host] match_stats = 0)");
  }
  return g_enabled == 1;
}

st::Accumulator* Counting() { return Enabled() && !g_sent ? &g_acc : nullptr; }

void OnHit(void* target, bool hasSnapshot, int creditSeat, bool sameTeam, unsigned descLo, float hpBefore,
           float hpAfter, float dealt, float absorbed, bool lethal, unsigned creditGuid) {
  if (!Enabled() || g_sent || !target) return;
  st::Hit h;
  bool player = false;
  int npcType = -1, seat = -1;
  if (!SehTarget(target, &player, &npcType, &seat)) return;
  h.attackerSeat = creditSeat >= 0 && creditSeat < st::kSeats ? creditSeat : -1;
  h.targetSeat = seat >= 0 && seat < st::kSeats ? seat : -1;
  h.targetKind = st::KindOf(player, npcType);
  h.snapshot = hasSnapshot;
  h.sameTeam = sameTeam;
  h.descLo = descLo;
  h.hpBefore = hpBefore;
  h.hpAfter = hpAfter;
  h.dealt = dealt;
  h.absorbed = absorbed;
  h.lethal = lethal;
  // MSTATS3: the dealt part's row (only a counted hit by a seat on another's creature or an NPC needs one)
  if (h.attackerSeat >= 0 && h.snapshot && !sameTeam && h.attackerSeat != h.targetSeat)
    h.source = SourceOf(h.attackerSeat, creditGuid, descLo);
  g_acc.OnHit(h);
}

void OnDeath(bool playerControlled, int npcType, int victimTeam, int creditSeat, int victimSeat, int killerSeat) {
  if (!Enabled() || g_sent) return;
  const st::Kind kind = st::KindOf(playerControlled, npcType);
  const bool credited = creditSeat >= 0 && creditSeat < st::kSeats;
  if (credited) g_acc.OnKill(creditSeat, SehTeamOf(creditSeat), kind, victimTeam);
  const std::int64_t ms = MatchMs(match::MatchSeconds());
  if (victimSeat >= 0) {
    const bool killer = killerSeat >= 0 && killerSeat < st::kSeats;
    g_acc.OnPlayerKill(ms, killer ? killerSeat : -1, killer ? SehTeamOf(killerSeat) : 0, victimSeat);
  } else if (kind == st::Kind::Structures && (victimTeam == 1 || victimTeam == 2)) {
    g_acc.OnStructure(ms, 3 - victimTeam, npcType);  // the destroying team: the victim's enemy
  }
}

void OnDeathTimer(int seat, unsigned long long deathMs, unsigned long long respawnMs) {
  if (!Enabled() || g_sent) return;
  g_acc.OnDeathTimer(seat, deathMs, respawnMs);
}

void OnWellCaptured(double matchSeconds, int well, int team, const std::vector<int>& capturers) {
  if (!Enabled() || g_sent) return;
  g_acc.OnWellCaptured(MatchMs(matchSeconds), well, team, capturers);
}

void Sample(void* sim, double matchSeconds) {
  if (!Enabled() || g_sent) return;
  st::SeatSample seats[st::kSeats];
  for (int i = 0; i < st::kSeats; ++i) SehSeatSample(sim, i, &seats[i]);
  g_acc.Sample(matchSeconds, seats);
  RefreshSources(sim);  // MSTATS3: what each player holds now (a potion used within the second stays unknown)
}

void AtMatchEnd(void* sim, int winnerTeam, unsigned long long durationMs, int surrenderTeam) {
  if (!Enabled() || g_sent) return;
  g_sent = true;
  g_acc.OnMatchEnd(static_cast<std::int64_t>(durationMs), winnerTeam, surrenderTeam);
  g_acc.Close(SehNowMs(sim));
  cc::AtMatchEnd(SehNowMs(sim), g_acc);  // MSTATS4: crowd control and wards
  gold::AtMatchEnd(sim, g_acc);  // MSTATS2: gold and XP by source, the roles' bonuses
  st::SeatFacts seats[st::kSeats];
  for (int i = 0; i < st::kSeats; ++i)
    if (SehSeatFacts(sim, i, &seats[i])) seats[i].bot = bots::IsBotIndex(i);
  const bool channel = hostctl::Active();
  LogSources(g_acc, seats);  // MSTATS3: per seat the rows' sums against damageDealt/healingDone, the unknown GUIDs
  const std::vector<ctl::SeatStats> lines = g_acc.BuildSeatStats(seats);
  for (const ctl::SeatStats& m : lines) {
    std::string line = ctl::Encode(m);
    line.pop_back();
    Log("match: seatStats for player %lld: %s%s", static_cast<long long>(m.index), line.c_str(),
        channel ? " (to dgserver)" : " (no control channel: logged only)");
    hostctl::SeatStats(m);
  }
  st::TrimReport r;
  const ctl::MatchTimeline tl = g_acc.BuildTimeline(st::kLineBudget, &r);
  Log("match: matchTimeline: %zu sample(s) every %lld s, %zu seat(s), %zu event(s) (dropped for the line budget %d, "
      "past the cap %d, samples coarsened %d), %zu bytes%s; hits counted %u, skipped %u (health costs, self, own team), "
      "exceptions %u",
      tl.sec.size(), static_cast<long long>(tl.stepSec), tl.seats.size(), tl.events.size(), r.eventsDropped,
      r.eventsCapped, r.coarsened, r.bytes, channel ? " (to dgserver)" : " (no control channel: logged only)",
      g_acc.Hits(), g_acc.Skipped(), g_exceptions);
  {  // MSTATS1-live2: a short line to check a long match against the death: lines (a seat line may be clipped)
    int kills = 0, structures = 0, wells = 0, other = 0;
    for (const ctl::TimelineEvent& e : tl.events)
      ++(e.kind == "kill" ? kills : e.kind == "structure" ? structures : e.kind == "well" ? wells : other);
    std::string s;
    char b[64];
    for (const ctl::TimelineTeam& t : tl.teams) {
      std::snprintf(b, sizeof b, " team %lld %lld,", static_cast<long long>(t.team),
                    static_cast<long long>(t.kills.empty() ? 0 : t.kills.back()));
      s += b;
    }
    s += " time dead (ms):";
    for (const ctl::SeatStats& m : lines) {
      const auto it = m.stats.find("timeDeadMs");
      std::snprintf(b, sizeof b, " p%lld %lld", static_cast<long long>(m.index),
                    static_cast<long long>(it == m.stats.end() ? 0 : it->second));
      s += b;
    }
    Log("match: matchTimeline events: kill %d, structure %d, well %d, other %d; the last sample's team kills:%s", kills,
        structures, wells, other, s.c_str());
  }
  {
    std::string line = ctl::Encode(tl);
    line.pop_back();
    if (line.size() > 1500) line = line.substr(0, 1500) + "...";
    Log("match: matchTimeline line: %s", line.c_str());
  }
  hostctl::MatchTimeline(tl);
}

}  // namespace matchstats
