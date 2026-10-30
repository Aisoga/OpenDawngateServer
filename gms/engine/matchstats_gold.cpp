// M18 (MSTATS2): gold and XP by source and the roles' bonuses on the match host; see matchstats_gold.h and
// gms/core/stats/gold.h.
#include "matchstats_gold.h"

#include <windows.h>

#include <cstdio>
#include <cstring>

#include "game.h"
#include "log.h"
#include "matchstats.h"
#include "passives.h"

using namespace game;
namespace st = gms::stats;

namespace matchstats::gold {
namespace {

st::RoleLedger g_ledger;
bool g_done;
unsigned g_exceptions, g_handlerChanges, g_handlerUnresolved, g_hunterHits;

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  if (++g_exceptions <= 20)
    Log("EXCEPTION in match_stats gold (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
        reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

bool Active() { return !g_done && matchstats::Enabled(); }

// The role modifiers' cStaticAbility, cached once found (null before the level's abilities are loaded).
struct RoleStatic {
  std::uint32_t guid;
  void* st;
};
RoleStatic g_static[] = {{st::kGuidRoleGladiator, nullptr},
                         {st::kGuidRoleTactician, nullptr},
                         {st::kGuidRoleHunter, nullptr},
                         {st::kGuidRolePredator, nullptr}};

void* SehStatic(RoleStatic& r) {
  if (r.st) return r.st;
  __try {
    r.st = FindStaticAbility(r.guid);
  } __except (SehNote(GetExceptionInformation(), "FindStaticAbility")) {
    r.st = nullptr;
  }
  return r.st;
}

// The seat of a player-controlled object, -1 for anything else.
int SehSeatOfObject(std::uint32_t id) {
  __try {
    void* o = id ? GetObjectById(id) : nullptr;
    if (!o || !Field<unsigned char>(o, gobj::kPlayerControlled)) return -1;
    const int seat = Field<unsigned char>(o, gobj::kPlayerIndex);
    return seat < st::kSeats ? seat : -1;
  } __except (SehNote(GetExceptionInformation(), "the grant's creature")) {
    return -1;
  }
}

// The static GUID of a modifier instance among the role modifiers', 0 for any other (or none).
std::uint32_t SehRoleGuidOf(std::uint32_t instance) {
  void* stp = nullptr;
  __try {
    void* in = instance ? GetInstance(instance) : nullptr;
    stp = in ? Field<void*>(in, inst::kStatic) : nullptr;
  } __except (SehNote(GetExceptionInformation(), "the handler's instance")) {
    return 0;
  }
  if (!stp) return 0;
  for (RoleStatic& r : g_static)
    if (SehStatic(r) == stp) return r.guid;
  return 0;
}

// A hit on a creep (nGameObject.IsCreep) by an attacker that holds modifier_Role_Hunter.
bool SehHunterHit(void* attacker, void* target) {
  void* hunter = SehStatic(g_static[2]);
  if (!hunter) return false;
  __try {
    if (Field<unsigned char>(target, gobj::kPlayerControlled)) return false;
    void* n = Field<void*>(target, npc::kOfObject);
    char* nd = n ? Field<char*>(n, noun::kData) : nullptr;
    if (!nd || !st::IsCreepType(Field<int>(nd, npc::kType))) return false;
    return passives::CreatureHasLiveInstance(attacker, hunter);
  } __except (SehNote(GetExceptionInformation(), "the Hunter's hit")) {
    return false;
  }
}

// One seat's telemetry record (107 floats); the block's sizes for the log.
bool SehTelemetry(void* sim, int seat, float* out, unsigned* records, unsigned* count) {
  __try {
    char* block = static_cast<char*>(sim) + telemetry::kOfSim;
    char* b = Field<char*>(block, telemetry::kPlayersBegin);
    char* e = Field<char*>(block, telemetry::kPlayersEnd);
    *count = Field<unsigned>(block, telemetry::kPlayerCount);
    *records = b && e > b ? static_cast<unsigned>((e - b) / telemetry::kPlayerRecordSize) : 0u;
    if (static_cast<unsigned>(seat) >= *records) return false;
    char* rec = b + seat * telemetry::kPlayerRecordSize;
    if (Field<unsigned>(rec, 0) != static_cast<unsigned>(seat)) return false;
    std::memcpy(out, rec + telemetry::kStat0, sizeof(float) * st::kTelemetryStats);
    return true;
  } __except (SehNote(GetExceptionInformation(), "the telemetry record")) {
    return false;
  }
}

struct PlayerTotals {
  bool present = false;
  unsigned role = 0;
  float gold = 0.f, xp = 0.f;
};

PlayerTotals SehTotals(void* sim, int seat) {
  PlayerTotals t;
  __try {
    void* p = GetPlayerByIndex(sim, static_cast<unsigned>(seat));
    if (!p) return t;
    t.present = true;
    t.role = Field<unsigned char>(p, player::kRole);
    t.gold = Field<float>(p, player::kTotalGold);
    t.xp = Field<float>(p, player::kXp);
  } __except (SehNote(GetExceptionInformation(), "the player's totals")) {
  }
  return t;
}

}  // namespace

void OnRoleGold(int seat, st::GrantContext c, bool lastHit, float amount, bool asBonus) {
  if (Active()) g_ledger.OnRoleGold(seat, c, lastHit, amount, asBonus);
}

void OnHandlerChange(std::uint32_t creature, std::uint32_t modifier, float before, float after) {
  if (!Active() || after == before) return;
  const int seat = SehSeatOfObject(creature);
  if (seat < 0) {
    ++g_handlerUnresolved;
    return;
  }
  ++g_handlerChanges;
  g_ledger.OnHandlerChange(seat, SehRoleGuidOf(modifier), static_cast<double>(after) - before);
}

void OnPredatorXp(int seat, float part) {
  if (Active()) g_ledger.OnPredatorXp(seat, part);
}

void OnHit(void* attacker, void* target, int creditSeat, bool sameTeam, float hpBefore, float hpAfter) {
  if (!Active() || !attacker || !target || sameTeam || creditSeat < 0 || creditSeat >= st::kSeats) return;
  const double effective = static_cast<double>(hpBefore) - hpAfter;
  if (!(effective > 0.0) || !SehHunterHit(attacker, target)) return;
  ++g_hunterHits;
  g_ledger.OnHunterDamage(creditSeat, st::HunterBonusPart(effective));
}

void AtMatchEnd(void* sim, st::Accumulator& acc) {
  if (g_done || !matchstats::Enabled() || !sim) return;
  g_done = true;
  unsigned records = 0, count = 0;
  int read = 0;
  for (int seat = 0; seat < st::kSeats; ++seat) {
    const PlayerTotals t = SehTotals(sim, seat);
    if (!t.present) continue;
    float rec[st::kTelemetryStats];
    const bool ok = SehTelemetry(sim, seat, rec, &records, &count);
    if (ok) ++read;
    const st::GoldSplit g = st::AddGoldStats(acc, seat, ok ? rec : nullptr, g_ledger);
    auto part = [&](st::RolePart p) { return g_ledger.Part(seat, p); };
    Log("match: gold by source (MSTATS2) player %d (role %u): goldTotal %.2f, sources %.2f (diff %.2f), mTotalGold %.2f; "
        "xpTotal %.2f, sources %.2f, mExperience %.2f; goldRole %.2f = the host's role parts in it %.2f + harass %.2f; "
        "parts: last hit %.2f, proximity %.2f, kill %.2f, assist %.2f, stored %.2f, workers %.2f, procs %.2f, other "
        "%.2f; xpRolePredator %.2f, damageRoleHunterBonus %.2f%s",
        seat, t.role, g.goldTotal, g.goldSources, g.goldSources - g.goldTotal, t.gold, g.xpTotal, g.xpSources, t.xp,
        g.goldRole, g.inRole, g.harass, part(st::RolePart::GladiatorLastHit), part(st::RolePart::TacticianProximity),
        part(st::RolePart::PredatorKill), part(st::RolePart::PredatorAssist), part(st::RolePart::GladiatorStored),
        part(st::RolePart::PredatorWorkers), part(st::RolePart::HunterProc), part(st::RolePart::Other),
        g_ledger.PredatorXp(seat), g_ledger.HunterDamage(seat),
        !ok                 ? " (NO TELEMETRY RECORD)"
        : g.harass <= -0.5 ? " (ROLE MISMATCH: the host's parts exceed stat 19)"
                            : "");
  }
  Log("match: gold by source (MSTATS2): telemetry block sim+0x%X: %u player record(s) of 0x%X (count field %u), %d "
      "seat(s) read; handler changes %u (unresolved %u), Hunter creep hits %u, exceptions %u",
      static_cast<unsigned>(telemetry::kOfSim), records, static_cast<unsigned>(telemetry::kPlayerRecordSize), count, read,
      g_handlerChanges, g_handlerUnresolved, g_hunterHits, g_exceptions);
}

}  // namespace matchstats::gold
