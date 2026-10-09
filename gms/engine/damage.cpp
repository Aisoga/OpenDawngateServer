// D1f-live: the TakeDamage core (0x008E01C0) and the shield totals; see damage.h and docs/DAMAGE.md. D13: the steps
// from the crit to the HP are core/damage/pipeline.h's RunHit (DAMAGE.md *Combat edge cases (D13)*).
#include "damage.h"

#include <windows.h>

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>

#include "agents.h"
#include "core/damage/pipeline.h"
#include "core/damage/rules.h"
#include "death.h"
#include "game.h"
#include "hook.h"
#include "log.h"
#include "wells.h"

using namespace game;
namespace dmg = gms::damage;

namespace {

DamageOptions g_opt;
void* g_sim;
TakeDamageFn g_orig;
bool g_installed;

struct Stats {
  unsigned calls, foreign, applied, kills, noCombatant, deadTarget, immune, crits, absorbedHits, lifedrains, overloads,
      reports, events, exceptions, shieldWrites, shieldObjects;
  unsigned noSnapshot, deadAttackerHits, lifedrainsDead;  // D13
  double total, absorbed;
} g_st;
unsigned g_logs;

bool LogBudget() {
  unsigned k = ++g_logs;
  return k <= static_cast<unsigned>(g_opt.logLimit) || k % 100 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  ++g_st.exceptions;
  Log("EXCEPTION in damage (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

bool CodeMatches(std::uintptr_t va, const unsigned char* bytes, std::size_t n) {
  __try {
    return std::memcmp(reinterpret_cast<const void*>(va), bytes, n) == 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

bool Ours() { return g_sim && GetCurrentSim() == g_sim; }

const float* ZeroDir() { return At<const float*>(kZeroDirection); }

const char* NounName(void* o) {
  __try {
    void* n = o ? Field<void*>(o, gobj::kNoun) : nullptr;
    const char* s = n ? Field<const char*>(n, 0) : nullptr;
    if (s) {
      volatile char c = s[0];
      (void)c;
      return s;
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
  return "?";
}

// ---- engine calls (each under SEH) ---------------------------------------------------------------------------------

float SehAttr(void* object, int index) {
  __try {
    void* ad = object ? Field<void*>(object, gobj::kAttributes) : nullptr;
    return ad ? GetAttribute(ad, index, 1) : 0.f;
  } __except (SehNote(GetExceptionInformation(), "AttributeData_GetAttribute")) {
    return 0.f;
  }
}

float SehSnapAttr(void* snapshot, int index) {
  __try {
    return snapshot ? SnapshotAttribute(snapshot, index) : 0.f;
  } __except (SehNote(GetExceptionInformation(), "AbilitySnapshot_GetAttribute")) {
    return 0.f;
  }
}

char* SehAttributeTuning() {
  __try {
    void* asset = GetTuningAsset(reinterpret_cast<void*>(kAttributeTuningHolder));
    return asset ? Field<char*>(asset, noun::kData) : nullptr;
  } __except (SehNote(GetExceptionInformation(), "AttributeTuning")) {
    return nullptr;
  }
}

float SehInitial(float damage, float coef, unsigned lo, unsigned hi, void* attacker, void* snapshot, unsigned targetId,
                 const float* dir) {
  __try {
    return ComputeInitialDamage(damage, coef, lo, hi, attacker, snapshot, &targetId, dir);
  } __except (SehNote(GetExceptionInformation(), "Damage_ComputeInitialDamage")) {
    return damage;
  }
}

float SehModify(void* object, float d, unsigned lo, unsigned hi, unsigned guid, const float* dir, unsigned other,
                int index) {
  __try {
    void* c = object ? Field<void*>(object, obj::kAbilityData) : nullptr;
    return c ? ModifyDamage(c, d, lo, hi, guid, dir, other, index) : d;
  } __except (SehNote(GetExceptionInformation(), "AbilityDataContainer_ModifyDamage")) {
    return d;
  }
}

bool SehRollCrit(void* snapshot, unsigned lo, unsigned hi, void* tuning, void* target) {
  __try {
    return RollCritical(snapshot, lo, hi, tuning, target);
  } __except (SehNote(GetExceptionInformation(), "Damage_RollCritical")) {
    return false;
  }
}

float SehMultiplier(bool physical, void* target, void* snapshot) {
  __try {
    return physical ? PhysicalMultiplier(target, snapshot) : MagicalMultiplier(target, snapshot);
  } __except (SehNote(GetExceptionInformation(), physical ? "Damage_GetPhysicalMultiplier" : "Damage_GetMagicalMultiplier")) {
    return 1.f;
  }
}

bool SehIsStructure(void* o) {
  __try {
    return IsStructure(o);
  } __except (SehNote(GetExceptionInformation(), "GameObject_IsStructure")) {
    return false;
  }
}

// The outgoing vector's length (28-byte entries); the combat event lands there.
int OutgoingCount() {
  char* s = static_cast<char*>(g_sim);
  return static_cast<int>((Field<char*>(s, sim::kOutgoingEnd) - Field<char*>(s, sim::kOutgoingBegin)) / kOutgoingEntrySize);
}

// The last queued message's first bytes, for the log (transport id + the reflected CombatEvent: the field mask, then the
// flags u32 first).
void LastOutgoingHex(char* out, std::size_t cap) {
  out[0] = 0;
  __try {
    char* s = static_cast<char*>(g_sim);
    char* end = Field<char*>(s, sim::kOutgoingEnd);
    if (end == Field<char*>(s, sim::kOutgoingBegin)) return;
    char* e = end - kOutgoingEntrySize;
    char* m = Field<char*>(e, 0x10);
    if (!m) return;
    const unsigned size = Field<unsigned>(m, msg::kSize);
    const unsigned char* b = Field<const unsigned char*>(m, msg::kBuffer);
    std::size_t len = static_cast<std::size_t>(
        std::snprintf(out, cap, "kind %u, %02X:", Field<unsigned>(e, 0), Field<unsigned char>(m, msg::kTransportId)));
    for (unsigned i = 0; b && i < size && i < 24 && len + 4 < cap; ++i)
      len += static_cast<std::size_t>(std::snprintf(out + len, cap - len, " %02x", b[i]));
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
}

bool SehReport(void* target, void* snapshot, float dealt, bool crit, bool lethal, const float* dir, unsigned extra,
               unsigned lo, unsigned hi, int intChange, int* queued, char* hex, std::size_t hexCap,
               unsigned victimKillStreak = 0, unsigned killerPrevKillStreak = 0) {
  __try {
    const int before = OutgoingCount();
    ReportHpChange(target, snapshot, dealt, crit, lethal, dir, extra, lo, hi, intChange, victimKillStreak,
                   killerPrevKillStreak);
    *queued = OutgoingCount() - before;
    if (*queued > 0) LastOutgoingHex(hex, hexCap);
    return true;
  } __except (SehNote(GetExceptionInformation(), "Damage_ReportHpChange")) {
    return false;
  }
}

void SehRecordAttacker(void* target, float dealt, unsigned lo, unsigned hi, unsigned attackerId, unsigned playerIdx,
                       unsigned abilityId, unsigned instance) {
  __try {
    RecordAttacker(target, dealt, lo, hi, attackerId, playerIdx, abilityId, instance);
  } __except (SehNote(GetExceptionInformation(), "Damage_RecordAttacker")) {
  }
}

void SehFireEvents(unsigned targetId, unsigned attackerId, unsigned team, unsigned playerIdx, float dealt, unsigned lo,
                   unsigned hi, unsigned guid, unsigned instance, float damage, float coef, bool snapshot,
                   unsigned snapshotId, bool crit) {
  __try {
    FireOnTookDamage(targetId, attackerId, team, playerIdx, dealt, lo, hi, guid, instance, damage, coef);
    if (snapshot) FireOnDealtDamage(attackerId, targetId, dealt, lo, hi, guid, instance, snapshotId, crit);
  } __except (SehNote(GetExceptionInformation(), "FireOnTookDamage/FireOnDealtDamage")) {
  }
}

float SehHeal(void* object, void* snapshot, float amount, unsigned lo, unsigned hi) {
  __try {
    float healed = 0.f;
    bool crit = false;
    HealDamage(object, snapshot, amount, 0.f, lo, hi, true, &healed, &crit, 0, true, false, false);
    return healed;
  } __except (SehNote(GetExceptionInformation(), "GameObject_HealDamage")) {
    return 0.f;
  }
}

// ---- the private-table writes and one handler call (the pattern of 0x008FE110 + 0x00900460) -----------------------

bool SehSetPrivate(void* inst, const HandlerField* v, int n) {
  __try {
    void* th = GetThread(static_cast<char*>(g_sim) + sim::kLua, Field<unsigned>(inst, inst::kThreadId));
    if (!th) return false;
    void* L = Field<void*>(th, kThreadLuaState);
    if (!L) return false;
    const int top = LuaGetTop(L);
    LuaPushString(L, "_PRIVATETHREADTABLE");
    LuaGetTable(L, kLuaGlobalsIndex);
    if (LuaType(L, -1) != 5) {  // LUA_TTABLE
      LuaSetTop(L, top);
      return false;
    }
    for (int i = 0; i < n; ++i) {
      LuaPushString(L, v[i].name);
      if (v[i].type == 0) LuaPushNumber(L, v[i].number);
      else if (v[i].type == 1) LuaPushVector(L, v[i].vector ? v[i].vector : ZeroDir());
      else LuaPushLightUserdata(L, reinterpret_cast<void*>(static_cast<std::uintptr_t>(v[i].id)));
      LuaSetTable(L, -3);
    }
    LuaSetTop(L, top);
    return true;
  } __except (SehNote(GetExceptionInformation(), "private table")) {
    return false;
  }
}

float SehCallHandler(unsigned instId, int index, float value, const float* dir, unsigned other, unsigned lo, unsigned hi,
                     unsigned guid) {
  __try {
    return CallNumberHandler(instId, index, value, dir, other, lo, hi, guid);
  } __except (SehNote(GetExceptionInformation(), "LabsAbilities_CallModifyDamageFunction")) {
    return value;
  }
}

// The object's modifiers that have handler `index` and aren't ending, with their time left (DAMAGE.md step 8; the
// order is gms::damage::SortShields', core/damage/pipeline.h).
int SehCollectMods(void* object, int index, dmg::ShieldRef* out, int cap) {
  __try {
    char* c = object ? Field<char*>(object, obj::kAbilityData) : nullptr;
    if (!c) return 0;
    const unsigned* b = Field<const unsigned*>(c, container::kModifiersBegin);
    const unsigned* e = Field<const unsigned*>(c, container::kModifiersEnd);
    const unsigned long long now = GetTimeMs(g_sim);
    int n = 0;
    for (const unsigned* p = b; p && p != e && n < cap; ++p) {
      void* in = GetInstance(*p);
      if (!in) continue;
      char* st = Field<char*>(in, inst::kStatic);
      if (!st || !Field<unsigned char>(st, fns::kHasHandler + index) || Field<unsigned char>(in, inst::kEndPending)) continue;
      out[n].id = *p;
      out[n].handle = in;
      out[n].leftMs = dmg::TimeLeftMs(Field<int>(in, inst::kDurationMs), Field<unsigned long long>(in, inst::kStartMs), now);
      ++n;
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "modifier list")) {
    return 0;
  }
}

// D13: whether an object is alive (combatant HP > 0); an object without combatant data counts as alive (nothing of it can
// die or be healed).
bool SehAlive(void* o) {
  __try {
    char* cd = o ? Field<char*>(o, combatant::kOfObject) : nullptr;
    return !cd || Field<float>(cd, combatant::kHitPoints) > 0.f;
  } __except (SehNote(GetExceptionInformation(), "alive")) {
    return false;
  }
}

// The shield totals of one object (17/18/19 summed over its modifiers) into combatant +0x4C/+0x50/+0x54. Returns true if
// the object carries any shield modifier or had a non-zero total.
bool UpdateShields(void* object) {
  float total[3] = {0.f, 0.f, 0.f};
  bool any = false;
  static const int kIdx[3] = {fns::kAbsorbAvailable, fns::kAbsorbAvailableMagical, fns::kAbsorbAvailablePhysical};
  for (int k = 0; k < 3; ++k) {
    dmg::ShieldRef mods[dmg::kMaxShields];
    const int n = SehCollectMods(object, kIdx[k], mods, dmg::kMaxShields);
    for (int i = 0; i < n; ++i) {
      any = true;
      const float r = SehCallHandler(mods[i].id, kIdx[k], 0.f, ZeroDir(), 0, 0, 0, 0);
      if (r > 0.f) total[k] += r;
    }
  }
  __try {
    char* cd = Field<char*>(object, combatant::kOfObject);
    if (!cd) return any;
    if (!(Field<float>(cd, combatant::kHitPoints) > 0.f)) total[0] = total[1] = total[2] = 0.f;  // a corpse: none
    static const std::uintptr_t kOff[3] = {combatant::kShield, combatant::kMagicalShield, combatant::kPhysicalShield};
    for (int k = 0; k < 3; ++k) {
      float& f = Field<float>(cd, kOff[k]);
      if (f != 0.f) any = true;
      if (f != total[k]) {
        if (LogBudget())
          Log("damage: shield %08X %s: %s %.2f -> %.2f", Field<unsigned>(object, gobj::kId), NounName(object),
              k == 0 ? "mShieldHealth" : k == 1 ? "mMagicalShieldHealth" : "mPhysicalShieldHealth", f, total[k]);
        f = total[k];
        ++g_st.shieldWrites;
      }
    }
  } __except (SehNote(GetExceptionInformation(), "shield totals")) {
  }
  return any;
}

// ---- the core ----------------------------------------------------------------------------------------------------

struct Trace {
  float base, coef, initial, critMult, outgoing, incoming, mult, final, absorbed, dealt, hpBefore, hpAfter, healed,
      overloadDealt;
  bool crit, lethal, attackerDead;
  int intChange, queued, absorbHandlers;
  unsigned ability;
  char hex[96];
};

// D13: the engine's side of gms::damage::RunHit (core/damage/pipeline.h): the client's ModifyDamage dispatcher, its
// defense multipliers with the extra reductions, and the scripts' absorb handlers through their private tables.
class EngineHooks final : public dmg::HitHooks {
 public:
  EngineHooks(void* target, void* attacker, void* snapshot, unsigned lo, unsigned hi, unsigned guid, const float* dir,
              unsigned targetId, unsigned attackerId)
      : target_(target), attacker_(attacker), snapshot_(snapshot), lo_(lo), hi_(hi), guid_(guid), dir_(dir),
        targetId_(targetId), attackerId_(attackerId) {}
  // 4. / 5. outgoing on the attacker, incoming on the target
  float Outgoing(float d) override { return SehModify(attacker_, d, lo_, hi_, guid_, dir_, targetId_, 11); }
  float Incoming(float d) override { return SehModify(target_, d, lo_, hi_, guid_, dir_, attackerId_, 12); }
  // 6. mitigation (not called for true damage)
  float Mitigation() override {
    float mult = 1.f;
    if (lo_ & dmg::kIsPhysical) mult *= SehMultiplier(true, target_, snapshot_);
    if (lo_ & dmg::kIsMagical) mult *= SehMultiplier(false, target_, snapshot_);
    if (g_opt.reductions) {  // [assumed]
      mult *= 1.f - SehAttr(target_, attr::kDamageReduction);
      if (snapshot_) mult *= 1.f - SehSnapAttr(snapshot_, attr::kPercentDamageDebuff);
      if (lo_ & dmg::kIsAoE) mult *= 1.f - SehAttr(target_, attr::kAoeResistance);
      if (SehIsStructure(target_)) {
        if (snapshot_) mult *= 1.f + SehSnapAttr(snapshot_, attr::kStructureDamageDealt);
        mult *= 1.f - SehAttr(target_, attr::kStructureDamageTaken);
      }
    }
    return mult;
  }
  // 7. final on the target
  float Final(float d) override { return SehModify(target_, d, lo_, hi_, guid_, dir_, attackerId_, 13); }
  // 8. absorb
  int Shields(int index, dmg::ShieldRef* out, int cap) override { return SehCollectMods(target_, index, out, cap); }
  float Absorb(const dmg::ShieldRef& s, int index, float left) override {
    HandlerField v[2] = {{"absorbDamage", 0, left, nullptr, 0}, {"absorbDirection", 1, 0.f, dir_, 0}};
    if (!SehSetPrivate(s.handle, v, 2)) return left;
    return SehCallHandler(s.id, index, left, dir_, attackerId_, lo_, hi_, guid_);
  }

 private:
  void* target_;
  void* attacker_;
  void* snapshot_;
  unsigned lo_, hi_, guid_;
  const float* dir_;
  unsigned targetId_, attackerId_;
};

bool Core(void* target, void* snapshot, float damage, float coef, unsigned lo, unsigned hi, const float* direction,
          float* dealtOut, float* absorbedOut, bool* critOut, int depth);

bool CoreGuarded(void* target, void* snapshot, float damage, float coef, unsigned lo, unsigned hi, const float* direction,
                 float* dealtOut, float* absorbedOut, bool* critOut, int depth) {
  __try {
    return Core(target, snapshot, damage, coef, lo, hi, direction, dealtOut, absorbedOut, critOut, depth);
  } __except (SehNote(GetExceptionInformation(), "TakeDamage core")) {
    return false;
  }
}

void LogHit(void* target, void* attacker, unsigned lo, const Trace& t, int depth) {
  if (!LogBudget()) return;
  Log("damage: %08X %s <- %08X %s ability %08X desc 0x%X%s: base %.2f + coef %.2f -> initial %.2f%s, outgoing %.2f, incoming %.2f, "
      "x %.4f -> %.2f, final %.2f, absorbed %.2f (%d handlers) -> dealt %.2f; hp %.2f -> %.2f (int %d)%s; event queued %d "
      "[%s]%s",
      Field<unsigned>(target, gobj::kId), NounName(target), attacker ? Field<unsigned>(attacker, gobj::kId) : 0u,
      attacker ? NounName(attacker) : "-", t.ability, lo, depth ? " (overload)" : "", t.base, t.coef, t.initial,
      t.crit ? " CRIT" : "", t.outgoing, t.incoming, t.mult, t.incoming * t.mult, t.final, t.absorbed, t.absorbHandlers,
      t.dealt, t.hpBefore, t.hpAfter, t.intChange, t.lethal ? " LETHAL" : "", t.queued, t.hex,
      t.healed > 0.f ? " lifedrain" : t.attackerDead ? " (attacker dead: no lifedrain, no aggro)" : "");
}

bool Core(void* target, void* snapshot, float damage, float coef, unsigned lo, unsigned hi, const float* direction,
          float* dealtOut, float* absorbedOut, bool* critOut, int depth) {
  Trace t;
  std::memset(&t, 0, sizeof t);
  t.base = damage;
  t.coef = coef;
  char* cd = Field<char*>(target, combatant::kOfObject);
  if (!cd) {
    ++g_st.noCombatant;
    return false;
  }
  float& hp = Field<float>(cd, combatant::kHitPoints);
  if (!(hp > 0.f)) {
    ++g_st.deadTarget;
    return false;
  }
  const float* dir = direction ? direction : ZeroDir();
  const unsigned targetId = Field<unsigned>(target, gobj::kId);
  const unsigned attackerId = snapshot ? Field<unsigned>(snapshot, snap::kCaster) : 0u;
  void* attacker = attackerId ? GetObjectById(attackerId) : nullptr;
  const unsigned guid = snapshot ? Field<unsigned>(snapshot, snap::kOriginalGuid) : 0u;
  const unsigned creditGuid = snapshot ? Field<unsigned>(snapshot, snap::kAbilityGuid) : 0u;
  t.ability = creditGuid;
  const unsigned instance = snapshot ? Field<unsigned>(snapshot, snap::kInstance) : 0u;
  const unsigned team = snapshot ? Field<unsigned char>(snapshot, snap::kTeam) : 0u;
  unsigned playerIdx = 0xFF;
  if (snapshot) {
    playerIdx = Field<unsigned char>(snapshot, snap::kCreditPlayerIndex);
    if (playerIdx == 0xFF) playerIdx = Field<unsigned char>(snapshot, snap::kPlayerIndex);
  }
  // 1. Immune (46) [assumed]: no damage, combat event 0x80
  if (SehAttr(target, attr::kImmune) > 0.f) {
    ++g_st.immune;
    int q = 0;
    SehReport(target, snapshot, 0.f, false, false, dir, 0x80, lo, hi, 0, &q, t.hex, sizeof t.hex);
    if (LogBudget())
      Log("damage: %08X %s <- %08X: Immune, no damage (combat event 0x80, queued %d)", targetId, NounName(target),
          attackerId, q);
    return false;
  }
  // 2. initial damage (+ ModifyDamageInitial on the attacker)
  dmg::HitInput hin;
  hin.initial = SehInitial(damage, coef, lo, hi, attacker, snapshot, targetId, dir);
  t.initial = hin.initial;
  // 3. crit (basic attacks)
  char* tuning = SehAttributeTuning();
  if (dmg::MayCrit(lo, snapshot != nullptr) && tuning && SehRollCrit(snapshot, lo, hi, tuning, target)) {
    t.crit = true;
    ++g_st.crits;
    t.critMult = Field<float>(tuning, attrTuning::kCritDamageBonus) + SehSnapAttr(snapshot, attr::kFlatCritDamage) +
                 SehSnapAttr(snapshot, attr::kPercentCritDamage);
  }
  // 4.-9. the steps of core/damage/pipeline.h's RunHit, with the client's dispatchers and the scripts' handlers. D13:
  // ModifyDamageIncoming/_Final only for a hit with a snapshot (a health cost has none; pipeline.h HitInput.snapshot).
  hin.crit = t.crit;
  hin.critMult = t.critMult;
  hin.descLo = lo;
  hin.attacker = attacker != nullptr;
  hin.snapshot = snapshot != nullptr;
  hin.absorb = g_opt.absorb != 0;
  hin.hp = hp;
  if (!snapshot) ++g_st.noSnapshot;
  EngineHooks hooks(target, attacker, snapshot, lo, hi, guid, dir, targetId, attackerId);
  const dmg::HitResult hr = dmg::RunHit(hin, hooks);
  t.outgoing = hr.outgoing;
  t.incoming = hr.incoming;
  const float preMitigation = hr.preMitigation;
  t.mult = hr.mult;
  t.final = hr.final;
  t.absorbHandlers = hr.absorbHandlers;
  t.absorbed = hr.absorbed;
  if (t.absorbed > 0.f) {
    ++g_st.absorbedHits;
    g_st.absorbed += t.absorbed;
  }
  const float dealt = hr.dealt;
  t.dealt = dealt;
  // 9. HP
  t.hpBefore = hp;
  hp = hr.hpAfter;
  t.hpAfter = hr.hpAfter;
  t.intChange = hr.intChange;
  t.lethal = hr.lethal;
  if (t.lethal) {
    Field<unsigned>(cd, combatant::kDeathDescriptors) = dmg::DeathDescriptors(t.crit, lo);
    std::memcpy(cd + combatant::kHitDirection, dir, 12);
    Field<float>(cd, combatant::kShield) = 0.f;
    Field<float>(cd, combatant::kMagicalShield) = 0.f;
    Field<float>(cd, combatant::kPhysicalShield) = 0.f;
    ++g_st.kills;
  }
  if (dealtOut) *dealtOut = dealt;
  if (absorbedOut) *absorbedOut = t.absorbed;
  if (critOut) *critOut = t.crit;
  ++g_st.applied;
  g_st.total += dealt;
  // 11. attacker bookkeeping (not for self-inflicted or same-team hits: DamageTracker.cpp asserts on friendly fire).
  // D1h: before the report (DAMAGE.md had it after), so a player's killing blow finds its hitter as the victim's last
  // player to hit; and every enemy hit on a player's creature goes into its death recap's log.
  if (dmg::RecordsAttacker(snapshot != nullptr, dealt, attackerId, targetId, team == Field<unsigned char>(target, gobj::kTeam))) {
    SehRecordAttacker(target, dealt, lo, hi, attackerId, playerIdx, creditGuid, instance);
    if (attacker) death::RecordPlayerHit(target, attacker, dealt, lo, creditGuid);
  }
  // 10. the combat event; D1h: a player's killing blow is counted first (the report reads the killer's kill streak
  // and multi-kill count, and takes the victim's streak, the killer's previous one and FirstKill as arguments)
  death::KillReport kr;
  if (t.lethal) kr = death::BeforeKillingBlowReport(target);
  if (SehReport(target, snapshot, dealt, t.crit, t.lethal, dir, kr.extraFlags, lo, hi, t.intChange, &t.queued, t.hex,
                sizeof t.hex, kr.victimKillStreak, kr.killerPrevKillStreak))
    ++g_st.reports;
  // 12. the agent AI's hook, then the damage events. D1i: the hook first, so the attacker is on the victim's aggro list
  // when OnTookDamage runs: modifier_Jungle_Creep_Regen and modifier_Jungle_Buff_Regen_* drop their HealthRegen 2000
  // only if nAgent.HasTargetsOnAggroList is true in OnTookDamage; with the old order the first hit found the list empty
  // and the creep kept regenerating 400 HP/s through the fight [inferred]. d1i_run1/2: 2000 -> 0 at the first hit.
  // D13: a dead attacker (its projectile or Deactivate damage landing after its death) draws no aggro and drains no
  // life (pipeline.h DrawsAggro, LifedrainAmount); it keeps its kill credit (step 11) and its OnDealtDamage.
  const bool attackerAlive = attacker && SehAlive(attacker);
  t.attackerDead = attacker && !attackerAlive;
  if (t.attackerDead && dealt > 0.f) ++g_st.deadAttackerHits;
  if (dmg::DrawsAggro(dealt, attacker != nullptr, attackerAlive)) agents::OnDamaged(target, attacker, lo, hi, t.lethal);
  // D9: a capturer's damage pauses its capture; since D9c only an enemy player's hit (the attacker player-controlled or
  // a player's pet, the damage tracker's "player damage"), not a worker's, minion's, creep's or structure's
  if (dealt > 0.f) wells::OnDamaged(target, attacker, dealt);
  if (dmg::FiresDamageEvents(lo)) {
    SehFireEvents(targetId, attackerId, team, playerIdx, dealt, lo, hi, creditGuid, instance, damage, coef,
                  snapshot != nullptr, snapshot ? Field<unsigned>(snapshot, snap::kId) : 0u, t.crit);
    ++g_st.events;
  }
  // 13. lifedrain (the snapshot's LifeDrain). D13: GameObject_HealDamage resurrects an object at HP 0 (0x008E0720), so a
  // dead attacker is skipped, as nGameObject.HealDamage (0x009401B0) refuses a heal on HP <= 0 without its resurrect flag.
  if (g_opt.lifedrain && dealt > 0.f && attacker && snapshot) {
    const float ld = SehSnapAttr(snapshot, attr::kLifeDrain);
    const float amount = dmg::LifedrainAmount(dealt, ld, lo, attackerAlive);
    if (amount > 0.f) {
      t.healed = SehHeal(attacker, snapshot, amount, lo | dmg::kNoDamageCallbacks, hi);
      ++g_st.lifedrains;
    } else if (ld > 0.f && !attackerAlive) {
      ++g_st.lifedrainsDead;
    }
  }
  if (g_opt.shields && !t.lethal) UpdateShields(target);
  LogHit(target, attacker, lo, t, depth);
  // 14. spell overload: a second hit, mitigated again (pipeline.h OverloadDamage: D13, never for a pre-mitigation
  // damage <= 0)
  if (g_opt.overload && depth == 0 && !t.lethal && dealt > 0.f && dmg::MayOverload(lo, snapshot != nullptr) && tuning) {
    const float pct = dmg::OverloadPercent(SehSnapAttr(snapshot, attr::kMastery),
                                           Field<float>(tuning, attrTuning::kOverloadFromMastery),
                                           SehSnapAttr(snapshot, attr::kFlatOverloadDamage));
    const float d2in = dmg::OverloadDamage(preMitigation, pct, depth, t.lethal, dealt, lo, snapshot != nullptr);
    if (d2in > 0.f) {
      float d2 = 0.f, a2 = 0.f;
      bool c2 = false;
      ++g_st.overloads;
      CoreGuarded(target, snapshot, d2in, 0.f, lo | dmg::kIsSpellOverload | dmg::kNoDamageCallbacks, hi, dir, &d2, &a2, &c2,
                  depth + 1);
      t.overloadDealt = d2;
    }
  }
  // 15. death processing
  if (t.lethal) death::OnDeath(target, attacker, attackerId, snapshot, "damage");
  return true;
}

bool __cdecl TakeDamageHook(void* object, void* snapshot, float damage, float coefficient, unsigned descLo, unsigned descHi,
                            const float* facing, float* dealt, float* absorbed, bool* isCrit) {
  if (!Ours() || !object) {
    ++g_st.foreign;
    return g_orig(object, snapshot, damage, coefficient, descLo, descHi, facing, dealt, absorbed, isCrit);
  }
  ++g_st.calls;
  if (dealt) *dealt = 0.f;
  if (absorbed) *absorbed = 0.f;
  if (isCrit) *isCrit = false;
  return CoreGuarded(object, snapshot, damage, coefficient, descLo, descHi, facing, dealt, absorbed, isCrit, 0);
}

int SehCollect(void** out, int cap) {
  __try {
    void* om = Field<void*>(g_sim, sim::kObjectManager);
    int it = ObjMgrIterBegin(om);
    int n = 0;
    for (void* o = ObjMgrIterNext(om, &it); o && n < cap; o = ObjMgrIterNext(om, &it)) {
      if (Field<unsigned char>(o, gobj::kPendingDelete)) continue;
      if (!Field<void*>(o, combatant::kOfObject) || !Field<void*>(o, obj::kAbilityData)) continue;
      out[n++] = o;
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "object scan")) {
    return 0;
  }
}

void* SehInstanceWith(unsigned instanceId, int index) {
  __try {
    void* in = GetInstance(instanceId);
    if (!in) return nullptr;
    char* st = Field<char*>(in, inst::kStatic);
    if (!st || !Field<unsigned char>(st, fns::kHasHandler + index) || Field<unsigned char>(in, inst::kEndPending))
      return nullptr;
    return in;
  } __except (SehNote(GetExceptionInformation(), "instance")) {
    return nullptr;
  }
}

}  // namespace

float CallInstanceHandler(unsigned instanceId, int index, float value, const HandlerField* fields, int count,
                          unsigned otherId, bool* called) {
  if (called) *called = false;
  if (!Ours()) return value;
  void* in = SehInstanceWith(instanceId, index);
  if (!in) return value;
  if (count > 0 && !SehSetPrivate(in, fields, count)) return value;
  if (called) *called = true;
  return SehCallHandler(instanceId, index, value, ZeroDir(), otherId, 0, 0, 0);
}

bool DamageInstall(const DamageOptions& options) {
  g_opt = options;
  const bool ok = CodeMatches(0x008E0130, kComputeInitialDamageBytes, sizeof kComputeInitialDamageBytes) &&
                  CodeMatches(0x00906220, kModifyDamageBytes, sizeof kModifyDamageBytes) &&
                  CodeMatches(0x00900460, kCallNumberHandlerBytes, sizeof kCallNumberHandlerBytes) &&
                  CodeMatches(0x008DFD50, kRollCriticalBytes, sizeof kRollCriticalBytes) &&
                  CodeMatches(0x008DFEB0, kPhysicalMultiplierBytes, sizeof kPhysicalMultiplierBytes);
  if (!ok) {
    Log("damage: a pipeline function's prologue differs (not build 854?); the TakeDamage core is not installed");
    return false;
  }
  g_orig = reinterpret_cast<TakeDamageFn>(
      HookDetour(kTakeDamage, kTakeDamageBytes, sizeof kTakeDamageBytes, reinterpret_cast<void*>(&TakeDamageHook)));
  g_installed = g_orig != nullptr;
  Log("damage (D1f-live): TakeDamage core %s; absorb %d, extra reductions %d, lifedrain %d, overload %d, shield totals %d, "
      "log limit %d",
      g_installed ? "installed" : "FAILED", g_opt.absorb, g_opt.reductions, g_opt.lifedrain, g_opt.overload, g_opt.shields,
      g_opt.logLimit);
  return g_installed;
}

void DamageSetSim(void* sim) { g_sim = sim; }

void DamageTickShields() {
  if (!g_installed || !g_opt.shields || !Ours()) return;
  static void* objects[0x400];
  const int n = SehCollect(objects, 0x400);
  unsigned with = 0;
  for (int i = 0; i < n; ++i)
    if (UpdateShields(objects[i])) ++with;
  g_st.shieldObjects = with;
}

void DamageFormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size,
                "damage calls %u (other sims %u), applied %u (total %.0f), kills %u, no combatant %u, dead target %u, "
                "immune %u, crits %u, absorbed %u hits (%.0f), lifedrains %u, overloads %u, reports %u, event pairs %u, "
                "shield writes %u (objects with shields %u), exceptions %u; D13: without snapshot %u, hits by a dead "
                "attacker %u, lifedrains skipped (attacker dead) %u",
                g_st.calls, g_st.foreign, g_st.applied, g_st.total, g_st.kills, g_st.noCombatant, g_st.deadTarget,
                g_st.immune, g_st.crits, g_st.absorbedHits, g_st.absorbed, g_st.lifedrains, g_st.overloads, g_st.reports,
                g_st.events, g_st.shieldWrites, g_st.shieldObjects, g_st.exceptions, g_st.noSnapshot,
                g_st.deadAttackerHits, g_st.lifedrainsDead);
}
