// M18 (MSTATS4): crowd control and wards for the match statistics; see matchstats_cc.h and gms/core/stats/cc.h.
#include "matchstats_cc.h"

#include <windows.h>

#include <map>
#include <string>
#include <utility>

#include "core/replication/modifiers.h"
#include "core/stats/cc.h"
#include "core/stats/stats.h"
#include "game.h"
#include "log.h"
#include "matchstats.h"

using namespace game;
namespace st = gms::stats;

namespace matchstats::cc {
namespace {

st::CcTally g_cc;
st::WardTally g_wards;
bool g_closed;
unsigned g_exceptions;
// counters for the end's summary line
unsigned g_verdicts[4];  // Counted, NotCc, Self, Nobody
unsigned g_updates, g_updatesUnknown, g_ended, g_carriersGone, g_noStatic, g_feedbackMismatch;
unsigned g_logged, g_wardsLogged;
constexpr unsigned kLogLimit = 60;       // the first counted CC modifiers, one line each
constexpr unsigned kWardLogLimit = 100;  // the ward abilities, one line each (their own count)

struct StaticFacts {
  bool found = false;
  unsigned debuff = 0;
  const char* name = "?";
};
std::map<std::uint32_t, StaticFacts> g_statics;  // ability GUID -> its static's facts (static abilities never change)

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  if (++g_exceptions <= 20)
    Log("EXCEPTION in match_stats cc (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
        reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

bool SehStatic(std::uint32_t guid, StaticFacts* out) {
  __try {
    char* stat = static_cast<char*>(FindStaticAbility(guid));
    if (!stat) return false;
    out->found = true;
    out->debuff = Field<unsigned>(stat, kStaticDebuffDescriptors);
    const char* n = *reinterpret_cast<const char**>(stat);
    if (n) {
      volatile char probe = n[0];
      (void)probe;
      out->name = n;
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "static ability")) {
    return false;
  }
}

const StaticFacts& StaticOf(std::uint32_t guid) {
  auto it = g_statics.find(guid);
  if (it == g_statics.end()) {
    StaticFacts f;
    SehStatic(guid, &f);
    it = g_statics.emplace(guid, f).first;
  }
  return it->second;
}

// An object's player index (+0x4C, 0xFF none -> -1), team and whether it is player-controlled.
bool SehObject(std::uint32_t id, int* seat, int* team, bool* player) {
  __try {
    void* o = id ? GetObjectById(id) : nullptr;
    if (!o) return false;
    const int idx = Field<unsigned char>(o, gobj::kPlayerIndex);
    *seat = idx < st::kSeats ? idx : -1;
    *team = Field<unsigned char>(o, gobj::kTeam);
    *player = Field<unsigned char>(o, gobj::kPlayerControlled) != 0;
    return true;
  } __except (SehNote(GetExceptionInformation(), "object")) {
    return false;
  }
}

unsigned long long SehNow() {
  __try {
    return GetTimeMs(GetCurrentSim());
  } __except (SehNote(GetExceptionInformation(), "GetTimeMs")) {
    return 0;
  }
}

bool SehWard(void* instance, std::uint32_t* guid, int* seat, const char** name) {
  __try {
    char* stat = Field<char*>(instance, inst::kStatic);
    if (!stat) return false;
    *guid = Field<std::uint32_t>(stat, kStaticGuid);
    if (!st::IsWardAbility(*guid)) return true;
    const char* n = *reinterpret_cast<const char**>(stat);
    *name = n ? n : "?";
    int team = 0;
    bool player = false;
    if (!SehObject(Field<std::uint32_t>(instance, inst::kAgent), seat, &team, &player)) *seat = -1;
    return true;
  } __except (SehNote(GetExceptionInformation(), "ward ability")) {
    return false;
  }
}

bool On() { return !g_closed && Enabled(); }

}  // namespace

void OnModifierCreated(const std::uint8_t* body, std::size_t n) {
  if (!On()) return;
  gms::ModifierRecord r;
  if (n != gms::kModifierRecordSize || !gms::ParseModifierRecord(body, n, &r) || r.instance == 0) return;
  const StaticFacts& sf = StaticOf(r.ability);
  unsigned debuff;
  if (sf.found) {
    debuff = sf.debuff;
    if (st::FeedbackFromDebuff(debuff) != r.feedback && ++g_feedbackMismatch <= 5)
      Log("match_stats cc: %s (ability %08X): the record's feedback 0x%x isn't the static's debuff 0x%x mapped (0x%x)",
          sf.name, r.ability, r.feedback, debuff, st::FeedbackFromDebuff(debuff));
  } else {
    debuff = st::DebuffFromFeedback(r.feedback);  // the wire's kinds only (no knockback, no pin)
    ++g_noStatic;
  }
  if (st::CcKindOf(debuff) == st::CcKind::None) {
    ++g_verdicts[static_cast<int>(st::CcVerdict::NotCc)];
    return;
  }
  st::CcStart s;
  s.instance = r.instance;
  s.carrier = r.carrier;
  s.caster = r.caster;
  s.debuff = debuff;
  s.startMs = r.startMs;
  s.durationMs = r.durationMs;
  bool casterPlayer = false, carrierPlayer = false;
  int carrierSeat = -1;
  if (!SehObject(r.caster, &s.casterSeat, &s.casterTeam, &casterPlayer)) {
    s.casterSeat = -1;
    s.casterTeam = 0;
  }
  if (!SehObject(r.carrier, &carrierSeat, &s.carrierTeam, &carrierPlayer)) {
    carrierSeat = -1;
    s.carrierTeam = 0;
  }
  s.carrierSeat = carrierPlayer ? carrierSeat : -1;
  const st::CcVerdict v = g_cc.OnCreated(s);
  ++g_verdicts[static_cast<int>(v)];
  if (v == st::CcVerdict::Counted && ++g_logged <= kLogLimit)
    Log("match_stats cc: %s (inst %08X, debuff 0x%x -> %s) by %08X (seat %d, team %d) on %08X (seat %d, team %d): "
        "%u ms from %llu",
        sf.name, r.instance, debuff, st::CcKindName(st::CcKindOf(debuff)), r.caster, s.casterSeat, s.casterTeam,
        r.carrier, s.carrierSeat, s.carrierTeam, r.durationMs, static_cast<unsigned long long>(r.startMs));
}

void OnModifierUpdated(const std::uint8_t* body, std::size_t n) {
  if (!On()) return;
  gms::ModifierUpdate u;
  if (n != gms::kModifierUpdateSize || !gms::ParseModifierUpdate(body, n, &u)) return;
  if (g_cc.OnUpdated(u.instance, u.startMs, u.durationMs)) ++g_updates;
  else ++g_updatesUnknown;  // not CC, or not counted
}

void OnModifierEnded(std::uint32_t instance) {
  if (!On()) return;
  if (g_cc.OnEnded(instance, SehNow())) ++g_ended;
}

void OnCarrierDeleted(std::uint32_t carrier) {
  if (!On()) return;
  g_carriersGone += static_cast<unsigned>(g_cc.OnCarrierGone(carrier, SehNow()));
}

void OnTickThread(void* instance, bool recreated) {
  if (!instance || !On()) return;
  std::uint32_t guid = 0;
  int seat = -1;
  const char* name = "?";
  if (!SehWard(instance, &guid, &seat, &name) || !st::IsWardAbility(guid)) return;
  const bool placed = g_wards.OnTickThread(guid, recreated, seat);
  if (++g_wardsLogged <= kWardLogLimit)
    Log("match_stats cc: ward ability %s (%s) for seat %d: %s", name, recreated ? "recreated" : "new tick thread", seat,
        placed ? "wardsPlaced + 1" : "not counted");
}

void AtMatchEnd(unsigned long long nowMs, st::Accumulator& acc) {
  if (g_closed || !Enabled()) return;
  g_closed = true;
  const int open = g_cc.Open();
  g_cc.Close(nowMs);
  for (int seat = 0; seat < st::kSeats; ++seat) {
    for (const auto& kv : g_cc.SeatStats(seat)) acc.AddStat(seat, kv.first, kv.second);
    acc.AddStat(seat, "wardsPlaced", g_wards.Placed(seat));  // WARDSTAT: no wardsDestroyed (a ward can't be killed)
  }
  std::string kinds;
  for (int k = 0; k < static_cast<int>(st::CcKind::Count); ++k) {
    const int c = g_cc.CountOf(static_cast<st::CcKind>(k));
    if (!c) continue;
    if (!kinds.empty()) kinds += ", ";
    kinds += std::string(st::CcKindName(static_cast<st::CcKind>(k))) + " " + std::to_string(c);
  }
  int wards = 0;
  for (int seat = 0; seat < st::kSeats; ++seat) wards += g_wards.Placed(seat);
  Log("match_stats cc (MSTATS4): CC modifiers counted %u (%s), not CC %u, self %u, nobody's %u; updates %u (others %u), "
      "ended %u, carriers deleted %u, open at the end %d; statics not found %u, feedback mismatches %u; wards placed %d; "
      "exceptions %u",
      g_verdicts[0], kinds.empty() ? "none" : kinds.c_str(), g_verdicts[1], g_verdicts[2], g_verdicts[3], g_updates,
      g_updatesUnknown, g_ended, g_carriersGone, open, g_noStatic, g_feedbackMismatch, wards, g_exceptions);
}

}  // namespace matchstats::cc
