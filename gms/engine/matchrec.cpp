// The match recorder on the match host; see matchrec.h (the file: gms/core/matchrec/matchrec.h).
#include "matchrec.h"

#include <windows.h>

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <memory>
#include <string>
#include <vector>

#include "bots.h"
#include "bots2.h"
#include "core/matchrec/matchrec.h"
#include "core/protocol/commands.h"
#include "core/stats/stats.h"
#include "director.h"
#include "game.h"
#include "host/host.h"
#include "log.h"
#include "match.h"

using namespace game;
namespace mr = gms::matchrec;

namespace matchrec {
namespace {

constexpr int kSeats = 10;
constexpr std::int64_t kFlushEveryMs = 5000;  // the buffer goes to the file at least this often (a crash loses less)

int g_enabled = -1;  // -1 = not read yet
double g_hz = 5.0;
std::size_t g_cap = mr::kDefaultCapBytes;
mr::Writer g_w;
mr::Rate g_rate(5.0);
bool g_open = false, g_tried = false, g_done = false, g_capLogged = false, g_failLogged = false;
std::string g_path;
bool g_seatKnown[kSeats] = {};
std::uint32_t g_creature[kSeats] = {};
std::int64_t g_lastFlushMs = 0;
unsigned g_exceptions = 0;

// The recorder's own cost and output, per match minute and for the whole match.
struct Cost {
  double us = 0.0;        // time spent in the recorder
  double maxCallUs = 0.0; // the longest single call (a tick's snapshots, a command, an event)
  unsigned calls = 0, ticks = 0;
  std::uint64_t snapshots = 0, commands = 0, events = 0, other = 0;
};
Cost g_min, g_all;
std::int64_t g_minute = 0;
std::uint64_t g_minBytes0 = 0;
LARGE_INTEGER g_freq = {};

// Times one recorder call (QueryPerformanceCounter) into g_min and g_all.
class Spent {
 public:
  explicit Spent(bool tick = false) : tick_(tick) { QueryPerformanceCounter(&t0_); }
  ~Spent() {
    LARGE_INTEGER t1;
    QueryPerformanceCounter(&t1);
    if (!g_freq.QuadPart) QueryPerformanceFrequency(&g_freq);
    const double us = static_cast<double>(t1.QuadPart - t0_.QuadPart) * 1e6 / static_cast<double>(g_freq.QuadPart);
    for (Cost* c : {&g_min, &g_all}) {
      c->us += us;
      c->maxCallUs = std::max(c->maxCallUs, us);
      ++c->calls;
      if (tick_) ++c->ticks;
    }
  }
  Spent(const Spent&) = delete;
  Spent& operator=(const Spent&) = delete;

 private:
  LARGE_INTEGER t0_;
  bool tick_;
};

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  if (++g_exceptions <= 20)
    Log("EXCEPTION in matchrec (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
        reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

std::int64_t NowMs() { return static_cast<std::int64_t>(std::llround(match::MatchSeconds() * 1000.0)); }

bool Recording() { return g_open && !g_done && g_w.Active(); }

void NoteWriterState() {
  const mr::WriterStats& s = g_w.Stats();
  if (s.capped && !g_capLogged) {
    g_capLogged = true;
    Log("matchrec: the size cap (%zu bytes, [host] match_record_cap_mb) reached at match %.1f s: the file ends with "
        "its X cap line, nothing more is recorded (the match goes on)",
        g_w.Cap(), match::MatchSeconds());
  }
  if (s.failed && !g_failLogged) {
    g_failLogged = true;
    Log("matchrec: a write to %s FAILED at match %.1f s: the recorder stopped (the match goes on)", g_path.c_str(),
        match::MatchSeconds());
  }
}

void Put(std::int64_t ms, const std::string& line) {
  if (!Recording()) return;
  switch (line[0]) {
    case 'S': ++g_min.snapshots, ++g_all.snapshots; break;
    case 'C': ++g_min.commands, ++g_all.commands; break;
    case 'E': ++g_min.events, ++g_all.events; break;
    default: ++g_min.other, ++g_all.other; break;
  }
  g_w.Line(ms, line);
  NoteWriterState();
}

// ---- engine reads (POD out, under SEH) -----------------------------------------------------------------------------

struct SeatRead {
  bool present = false;
  int team = 0;
  bool bot = false;
  long long user = 0;
  std::uint32_t noun = 0;
  char shaper[64] = "";
  int role = 0;
  int kills = 0, deaths = 0, assists = 0, creeps = 0, level = 0;
  float totalGold = 0.f;
};

bool SehSeat(void* sim, int seat, SeatRead* out) {
  __try {
    void* p = GetPlayerByIndex(sim, static_cast<unsigned>(seat));
    if (!p) return false;
    out->present = true;
    out->team = Field<unsigned char>(p, player::kTeam);
    out->bot = Field<unsigned char>(p, player::kIsBot) != 0;
    out->user = static_cast<long long>(Field<unsigned long long>(p, player::kOnlineId));
    void* n = Field<void*>(p, player::kNounDef);
    if (n) {
      out->noun = Field<std::uint32_t>(n, noun::kId);
      const char* name = Field<const char*>(n, asset::kName);
      if (name) strncpy_s(out->shaper, name, _TRUNCATE);
    }
    out->role = 1 + Field<unsigned char>(p, player::kRole);
    out->kills = static_cast<int>(Field<unsigned>(p, player::kKills));
    out->deaths = static_cast<int>(Field<unsigned>(p, player::kDeaths));
    out->assists = static_cast<int>(Field<unsigned>(p, player::kAssists));
    out->creeps = static_cast<int>(Field<unsigned>(p, player::kCreeps));
    out->level = static_cast<int>(Field<unsigned>(p, player::kLevel));
    out->totalGold = Field<float>(p, player::kTotalGold);
    return true;
  } __except (SehNote(GetExceptionInformation(), "seat")) {
    return false;
  }
}

// One seat's snapshot; *creature its creature id (0 none). `agentName` gets the name of the ability holding the
// creature (recall check).
bool SehSnapshot(void* sim, int seat, unsigned long long clockMs, mr::Snapshot* s, std::uint32_t* creature,
                 char* agentName, std::size_t agentNameSize) {
  __try {
    void* p = GetPlayerByIndex(sim, static_cast<unsigned>(seat));
    if (!p) return false;
    s->seat = seat;
    s->level = static_cast<int>(Field<unsigned>(p, player::kLevel));
    s->gold = Field<float>(p, player::kAvailableGold);
    s->totalGold = Field<float>(p, player::kTotalGold);
    const unsigned long long respawn = Field<unsigned long long>(p, player::kRespawnTime);
    const std::uint32_t c = Field<std::uint32_t>(p, player::kCurrentCreature);
    *creature = c;
    void* o = c ? GetObjectById(c) : nullptr;
    if (!o) {
      s->state = mr::kNoCreature;
      return true;
    }
    std::memcpy(s->pos, static_cast<char*>(o) + gobj::kPosition, sizeof s->pos);
    void* cb = Field<void*>(o, combatant::kOfObject);
    if (cb) {
      s->hp = Field<float>(cb, combatant::kHitPoints);
      s->res = Field<float>(cb, combatant::kResourcePoints);
    }
    s->hpMax = MaxHitPoints(o);
    void* ad = Field<void*>(o, gobj::kAttributes);
    if (ad) s->resMax = GetAttribute(ad, regenAttr::kMaxResourcePoints, 1);
    void* bb = Field<void*>(o, agent::kOfObject);
    s->target = bb ? Field<std::uint32_t>(bb, agent::kTarget) : 0u;
    s->seenBy = Field<std::uint8_t>(o, kObjectVisibleToTeam);
    const bool waiting = respawn != 0 && respawn != ~0ull && respawn > clockMs;
    if (!cb || s->hp <= 0.f || waiting) {
      s->state = mr::kDead;
      return true;
    }
    s->state = mr::kAlive;
    const std::uint32_t held = GetAgentAbility(c);
    void* in = held ? GetInstance(held) : nullptr;
    void* st = in ? Field<void*>(in, inst::kStatic) : nullptr;
    const char* name = st ? *reinterpret_cast<const char**>(st) : nullptr;
    if (name) strncpy_s(agentName, agentNameSize, name, _TRUNCATE);
    return true;
  } __except (SehNote(GetExceptionInformation(), "snapshot")) {
    return false;
  }
}

unsigned long long SehClockMs(void* sim) {
  __try {
    return GetTimeMs(sim);
  } __except (SehNote(GetExceptionInformation(), "GetTimeMs")) {
    return 0;
  }
}

// The seat of a creature: mPlayerIdx (+0x4C), -1 none.
int SehSeatOfObject(std::uint32_t id) {
  __try {
    void* o = id ? GetObjectById(id) : nullptr;
    if (!o) return -1;
    const int i = Field<unsigned char>(o, gobj::kPlayerIndex);
    return i < kSeats ? i : -1;
  } __except (SehNote(GetExceptionInformation(), "seat of creature")) {
    return -1;
  }
}

int SeatOfCreature(std::uint32_t id) {
  if (!id) return -1;
  for (int i = 0; i < kSeats; ++i)
    if (g_creature[i] == id) return i;
  return SehSeatOfObject(id);
}

// Return to Base holds its caster's agent (ability_ReturnToBase) [assumed: the name test is BOTTOWER's].
bool IsRecall(const char* name) {
  std::string n(name);
  std::transform(n.begin(), n.end(), n.begin(), [](char ch) { return static_cast<char>(std::tolower(static_cast<unsigned char>(ch))); });
  return n.find("returntobase") != std::string::npos || n.find("recall") != std::string::npos;
}

bool IsBot(unsigned index, const char* source) {
  return (source && std::strcmp(source, "bot") == 0) || bots::IsBotIndex(static_cast<int>(index));
}

std::string HexId(std::uint32_t v) {
  char b[16];
  std::snprintf(b, sizeof b, "%08X", v);
  return b;
}

std::string Num0(float v) {
  char b[32];
  std::snprintf(b, sizeof b, "%.0f", std::isfinite(v) ? static_cast<double>(v) : 0.0);
  return b;
}

void WriteSeat(void* sim, int seat) {
  SeatRead r;
  if (!SehSeat(sim, seat, &r) || !r.present) return;
  g_seatKnown[seat] = true;
  mr::SeatInfo s;
  s.seat = seat;
  s.team = r.team;
  s.bot = r.bot || bots::IsBotIndex(seat);
  s.user = r.user;
  s.noun = r.noun;
  s.shaper = r.shaper;
  s.role = r.role;
  for (const bots::Bot& b : bots::Players())
    if (b.index == seat) s.botClass = b.botClass;
  Put(0, mr::EncodeSeat(s));
}

void Snapshots(void* sim, std::int64_t ms) {
  const unsigned long long clock = SehClockMs(sim);
  for (int i = 0; i < kSeats; ++i) {
    mr::Snapshot s;
    std::uint32_t creature = 0;
    char held[96] = "";
    if (!SehSnapshot(sim, i, clock, &s, &creature, held, sizeof held)) continue;
    if (!g_seatKnown[i]) WriteSeat(sim, i);  // a seat that came after the start
    g_creature[i] = creature;
    if (s.state == mr::kAlive && held[0] && IsRecall(held)) s.state = mr::kRecalling;
    Put(ms, mr::EncodeSnapshot(ms, s));
  }
}

// "matchrec: minute 3: ..." (minute >= 0) or "matchrec: whole match: ..." (minute < 0).
void LogMinute(const Cost& c, std::int64_t minute, std::uint64_t bytes) {
  char what[32];
  if (minute >= 0) std::snprintf(what, sizeof what, "minute %lld", static_cast<long long>(minute));
  else std::snprintf(what, sizeof what, "whole match");
  Log("matchrec: %s: %llu snapshot(s), %llu command(s), %llu event(s); %llu bytes (file %llu bytes, %llu "
      "write(s)); the recorder's time %.2f ms in %u call(s), %.1f us a tick over %u tick(s), the longest call %.0f us",
      what, static_cast<unsigned long long>(c.snapshots),
      static_cast<unsigned long long>(c.commands), static_cast<unsigned long long>(c.events),
      static_cast<unsigned long long>(bytes), static_cast<unsigned long long>(g_w.Stats().bytes),
      static_cast<unsigned long long>(g_w.Stats().writes), c.us / 1000.0, c.calls, c.ticks ? c.us / c.ticks : 0.0,
      c.ticks, c.maxCallUs);
}

void Open(void* sim) {
  g_tried = true;
  const HostConfig& h = HostGet();
  if (!h.enabled || !h.dataDir[0]) {
    Log("matchrec (MATCHREC): not recording: no host data folder (the harness)");
    return;
  }
  SYSTEMTIME t;
  GetSystemTime(&t);
  g_path = std::string(h.dataDir) + "\\" + mr::FileName(h.match, t.wYear, t.wMonth, t.wDay, t.wHour, t.wMinute, t.wSecond);
  std::unique_ptr<mr::FileSink> sink = mr::FileSink::Create(g_path);
  if (!sink || !g_w.Open(std::move(sink), g_cap)) {
    Log("matchrec (MATCHREC): %s could not be opened: not recording (the match goes on)", g_path.c_str());
    return;
  }
  g_open = true;
  g_rate = mr::Rate(g_hz);
  char started[40], buf[64];
  std::snprintf(started, sizeof started, "%04u-%02u-%02uT%02u:%02u:%02uZ", t.wYear, t.wMonth, t.wDay, t.wHour, t.wMinute,
                t.wSecond);
  Put(0, mr::EncodeMeta("match", h.match));
  Put(0, mr::EncodeMeta("map", director::LevelName()));
  Put(0, mr::EncodeMeta("started_utc", started));
  std::snprintf(buf, sizeof buf, "0x%X", match::Descriptors());
  Put(0, mr::EncodeMeta("descriptors", buf));
  std::snprintf(buf, sizeof buf, "%g", g_hz);
  Put(0, mr::EncodeMeta("rate_hz", buf));
  Put(0, mr::EncodeMeta("cap_bytes", std::to_string(g_w.Cap())));
  Put(0, mr::EncodeMeta("time", "ms of match time"));
  // BOTS2c: each team's bot brain (head to head: bot_ai_team1 / bot_ai_team2)
  Put(0, mr::EncodeMeta("bot_ai_team1", bots2::BrainOfTeam(1)));
  Put(0, mr::EncodeMeta("bot_ai_team2", bots2::BrainOfTeam(2)));
  int seats = 0;
  for (int i = 0; i < kSeats; ++i) {
    WriteSeat(sim, i);
    seats += g_seatKnown[i] ? 1 : 0;
  }
  g_minBytes0 = 0;
  Log("matchrec (MATCHREC): recording to %s: %d seat(s), snapshots at %g Hz, cap %zu bytes", g_path.c_str(), seats,
      g_hz, g_w.Cap());
}

}  // namespace

bool Enabled() {
  if (g_enabled < 0) {
    g_enabled = HostOptionInt("match_record", 0) != 0 ? 1 : 0;
    g_hz = HostOptionInt("match_record_hz", 5);
    if (g_hz < 1) g_hz = 1;
    if (g_hz > 30) g_hz = 30;
    const int mb = HostOptionInt("match_record_cap_mb", 64);
    g_cap = static_cast<std::size_t>(mb < 1 ? 1 : mb > 1024 ? 1024 : mb) * 1024u * 1024u;
    if (g_enabled)
      Log("matchrec (MATCHREC): on ([host] match_record 1): snapshots at %g Hz ([host] match_record_hz), at most %zu "
          "bytes a match ([host] match_record_cap_mb)",
          g_hz, g_cap);
    else
      Log("matchrec (MATCHREC): off ([host] match_record 0)");
  }
  return g_enabled == 1;
}

void Tick(void* sim) {
  if (!Enabled() || g_done || !match::Started() || match::Ended()) return;
  if (!g_open && g_tried) return;
  Spent spent(true);
  if (!g_open) {
    Open(sim);
    if (!g_open) return;
  }
  if (!Recording()) return;
  const std::int64_t ms = NowMs();
  if (g_rate.Due(ms)) Snapshots(sim, ms);
  if (ms - g_lastFlushMs >= kFlushEveryMs) {
    g_lastFlushMs = ms;
    g_w.Flush();
    NoteWriterState();
  }
  const std::int64_t minute = ms / 60000;
  if (minute != g_minute) {
    const std::uint64_t bytes = g_w.Stats().bytes;
    LogMinute(g_min, g_minute, bytes - g_minBytes0);
    g_minBytes0 = bytes;
    g_min = Cost();
    g_minute = minute;
  }
}

void OnAction(unsigned index, const char* source, const gms::ActionCommand& c) {
  if (!Enabled() || !Recording()) return;
  Spent spent;
  Put(NowMs(), mr::EncodeCommand(NowMs(), mr::FromAction(c, static_cast<int>(index), IsBot(index, source),
                                                         source ? source : "")));
}

void OnRankUp(unsigned index, const char* source, unsigned slot, bool ranked) {
  if (!Enabled() || !Recording()) return;
  Spent spent;
  mr::Command c;
  c.seat = static_cast<int>(index);
  c.bot = IsBot(index, source);
  c.source = source ? source : "";
  c.kind = "rankup";
  c.slot = static_cast<int>(slot);
  c.value = ranked ? 1 : 0;
  Put(NowMs(), mr::EncodeCommand(NowMs(), c));
}

void OnItem(unsigned index, const char* source, const gms::ItemTransactionRequest& r) {
  if (!Enabled() || !Recording()) return;
  Spent spent;
  mr::Command c;
  c.seat = static_cast<int>(index);
  c.bot = IsBot(index, source);
  c.source = source ? source : "";
  c.kind = "item";
  c.slot = r.slot;
  c.ability = r.item;
  c.target = r.item2;
  c.value = r.type;
  Put(NowMs(), mr::EncodeCommand(NowMs(), c));
}

void OnItemResult(unsigned index, const char* source, const gms::ItemTransactionRequest& r, unsigned slot, bool done,
                  bool undo, float goldBefore, float goldAfter) {
  if (!Enabled() || !Recording()) return;
  Spent spent;
  const char* type = gms::ItemTransactionName(r.type);
  Put(NowMs(), mr::EncodeEvent(NowMs(), "item",
                               {std::to_string(index), IsBot(index, source) ? "bot" : "human", type ? type : "?",
                                HexId(r.item), std::to_string(slot), undo ? "undo" : done ? "done" : "refused",
                                Num0(goldBefore), Num0(goldAfter)}));
}

void OnBotRequest(std::uint32_t self, const char* where, std::uint32_t guid, int slot, std::uint32_t target,
                  const float* pos, int result) {
  if (!Enabled() || !Recording()) return;
  Spent spent;
  Put(NowMs(), mr::EncodeCommand(NowMs(), mr::FromBotRequest(SeatOfCreature(self), where, guid, slot, target, pos,
                                                             result)));
}

void OnBotItemUse(std::uint32_t self, const char* where, std::uint32_t item, int invSlot, bool used, float gold) {
  if (!Enabled() || !Recording()) return;
  Spent spent;
  const int seat = SeatOfCreature(self);
  const std::int64_t ms = NowMs();
  Put(ms, mr::EncodeCommand(ms, mr::FromBotItemUse(seat, where, item, invSlot)));
  Put(ms, mr::EncodeEvent(ms, "item", {std::to_string(seat), "bot", "use", HexId(item), std::to_string(invSlot),
                                       used ? "done" : "refused", Num0(gold), Num0(gold)}));
}

void OnDeath(bool playerControlled, int npcType, int victimTeam, int creditSeat, int victimSeat, int killerSeat,
             const std::vector<int>* assisters) {
  if (!Enabled() || !Recording()) return;
  Spent spent;
  const std::int64_t ms = NowMs();
  if (victimSeat >= 0) {
    Put(ms, mr::EncodeEvent(ms, "kill", {std::to_string(victimSeat), std::to_string(killerSeat >= 0 ? killerSeat : -1),
                                         mr::JoinSeats(assisters ? *assisters : std::vector<int>())}));
    return;
  }
  const gms::stats::Kind kind = gms::stats::KindOf(playerControlled, npcType);
  const bool credited = creditSeat >= 0 && creditSeat < kSeats;
  const bool objective = kind == gms::stats::Kind::Objectives || kind == gms::stats::Kind::Structures ||
                         kind == gms::stats::Kind::Guardian;
  if (!credited && !objective) return;  // a minion or creep nobody last-hit
  Put(ms, mr::EncodeEvent(ms, "death", {gms::stats::KindName(kind), std::to_string(npcType), std::to_string(victimTeam),
                                        std::to_string(credited ? creditSeat : -1)}));
}

void OnWellCaptured(double matchSeconds, int well, int team, const std::vector<int>& capturers) {
  if (!Enabled() || !Recording()) return;
  Spent spent;
  const std::int64_t ms = static_cast<std::int64_t>(std::llround(matchSeconds * 1000.0));
  Put(ms, mr::EncodeEvent(ms, "well", {std::to_string(well), std::to_string(team), mr::JoinSeats(capturers)}));
}

void AtMatchEnd(void* sim, int winnerTeam, unsigned long long durationMs, int surrenderTeam) {
  if (!Enabled() || g_done) return;
  if (!g_open) {
    g_done = true;
    return;
  }
  {
    Spent spent(true);
    const std::int64_t ms = NowMs();
    if (Recording()) {
      Snapshots(sim, ms);  // the last state
      Put(ms, mr::EncodeEvent(ms, "end", {std::to_string(winnerTeam), std::to_string(surrenderTeam),
                                          std::to_string(durationMs)}));
      for (int i = 0; i < kSeats; ++i) {
        SeatRead r;
        if (!SehSeat(sim, i, &r) || !r.present) continue;
        mr::Final f;
        f.seat = i;
        f.kills = r.kills;
        f.deaths = r.deaths;
        f.assists = r.assists;
        f.creeps = r.creeps;
        f.level = r.level;
        f.totalGold = std::llround(r.totalGold);
        Put(ms, mr::EncodeFinal(ms, f));
      }
    }
    g_w.Close(ms, "end");
    NoteWriterState();
  }
  g_done = true;
  LogMinute(g_min, g_minute, g_w.Stats().bytes - g_minBytes0);
  LogMinute(g_all, -1, g_w.Stats().bytes);
  const double minutes = durationMs > 0 ? static_cast<double>(durationMs) / 60000.0 : 0.0;
  Log("matchrec: closed %s: %llu bytes in %llu line(s) (%.1f KB a match minute), %llu dropped%s%s; the recorder's "
      "time %.1f ms over the match (%.1f us a tick on average, the longest call %.0f us), exceptions %u",
      g_path.c_str(), static_cast<unsigned long long>(g_w.Stats().bytes),
      static_cast<unsigned long long>(g_w.Stats().lines),
      minutes > 0 ? static_cast<double>(g_w.Stats().bytes) / 1024.0 / minutes : 0.0,
      static_cast<unsigned long long>(g_w.Stats().dropped), g_w.Stats().capped ? ", CAPPED" : "",
      g_w.Stats().failed ? ", a write FAILED" : "", g_all.us / 1000.0, g_all.ticks ? g_all.us / g_all.ticks : 0.0,
      g_all.maxCallUs, g_exceptions);
}

}  // namespace matchrec
