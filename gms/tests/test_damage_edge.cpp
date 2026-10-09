// gms/core/damage/pipeline.h (D13): the combat edge cases (docs/DAMAGE.md *Combat edge cases (D13)*). The scripts are
// modelled here from the decompiled ServerData Lua (re/out/serverdata_lua/lua, game content, not in the repo); the
// numbers are their values at rank 1, level 1 and Power 0 unless a case says otherwise.
#include <algorithm>
#include <cmath>
#include <cstdint>
#include <functional>
#include <vector>

#include "core/damage/pipeline.h"
#include "core/damage/rules.h"
#include "tests/check.h"

using namespace gms::damage;

namespace {
bool Near(double a, double b, double eps) { return std::fabs(a - b) <= eps; }

constexpr unsigned kBasic = kIsMelee | kIsBasic | kIsPhysical;  // every melee basic attack's descriptors
constexpr unsigned kAppliesOnHitEffects = 0x1000000;
constexpr unsigned kHealthCost = kIsTrueDamage | kIsSelfInflicted;  // 0x400800, CombatantData_SetResourcePoints

// A shield modifier as the scripts write it (modifier_Alpha_Q_Shield, modifier_Dreamer_W_Shield, modifier_Item_Bastion,
// modifier_Item_DeflectShield, modifier_Enforcer_W_Shield, modifier_BloodMage_E_Shield, rank 3 of
// modifier_Item_Legendary_StackingMRShield_Shield): AbsorbDamage takes min(remaining, absorbDamage) and returns the rest;
// AbsorbDamageAvailable returns the remaining amount. A shield at 0 stays until its time is up.
struct Shield {
  enum Kind { kStandard, kPassThrough, kRecall };
  std::uint32_t id = 0;
  int index = kAbsorbGeneric;  // 14 generic, 15 magical
  int durationMs = 0;          // 0 = untimed
  std::uint64_t startMs = 0;
  float remaining = 0.f;
  Kind kind = kStandard;
  bool ended = false;
  bool interrupted = false;  // modifier_returntobase: its AbsorbDamage sets interrupt
  int calls = 0;
  float minSeen = 1e30f;     // the smallest absorbDamage it was handed

  float Absorb(float d) {
    ++calls;
    if (d < minSeen) minSeen = d;
    if (kind == kPassThrough) return d;  // StackingMRShield_Shield at rank 1: return absorbDamage
    if (kind == kRecall) {
      interrupted = true;
      return d;
    }
    if (remaining > d) {
      remaining -= d;
      return 0.f;
    }
    const float r = d - remaining;
    remaining = 0.f;
    return r;
  }
  // OnStackModifier of the stacking shields (Dibs W, Basko W, Viyana E): ResetDuration, + one more amount up to a cap.
  void Restack(std::uint64_t nowMs, float add, float cap) {
    startMs = nowMs;
    remaining = std::min(remaining + add, cap);
  }
};

class World : public HitHooks {
 public:
  std::vector<Shield> shields;
  std::uint64_t nowMs = 0;
  float mitigation = 1.f;
  std::function<float(float)> outgoing, incoming, final;
  int mitigationCalls = 0, shieldQueries = 0;

  Shield& Add(std::uint32_t id, int index, int durationMs, float amount, Shield::Kind kind = Shield::kStandard) {
    Shield s;
    s.id = id;
    s.index = index;
    s.durationMs = durationMs;
    s.startMs = nowMs;
    s.remaining = amount;
    s.kind = kind;
    shields.push_back(s);
    return shields.back();
  }
  Shield* Find(std::uint32_t id) {
    for (Shield& s : shields)
      if (s.id == id) return &s;
    return nullptr;
  }
  // abilities.cpp's end pass at `t`: OnDuration | OnAgentDeath shields whose time is up end
  void AdvanceTo(std::uint64_t t) {
    nowMs = t;
    for (Shield& s : shields)
      if (!s.ended && ModifierExpired(kDeactOnDuration | kDeactOnAgentDeath, s.durationMs, s.startMs, nowMs)) s.ended = true;
  }
  // the shield totals the host writes into mShieldHealth (17) / mMagicalShieldHealth (18)
  float Total(int index) const {
    float t = 0.f;
    for (const Shield& s : shields)
      if (!s.ended && s.index == index && s.kind == Shield::kStandard) t += s.remaining;
    return t;
  }

  float Outgoing(float d) override { return outgoing ? outgoing(d) : d; }
  float Incoming(float d) override { return incoming ? incoming(d) : d; }
  float Mitigation() override {
    ++mitigationCalls;
    return mitigation;
  }
  float Final(float d) override { return final ? final(d) : d; }
  int Shields(int index, ShieldRef* out, int cap) override {
    ++shieldQueries;
    int n = 0;
    for (const Shield& s : shields) {
      if (s.ended || s.index != index || n >= cap) continue;
      out[n].id = s.id;
      out[n].leftMs = TimeLeftMs(s.durationMs, s.startMs, nowMs);
      ++n;
    }
    return n;
  }
  float Absorb(const ShieldRef& ref, int, float left) override {
    Shield* s = Find(ref.id);
    return s ? s->Absorb(left) : left;
  }
};

HitInput Hit(float damage, unsigned lo, float hp, bool snapshot = true) {
  HitInput in;
  in.initial = damage;
  in.descLo = lo;
  in.attacker = snapshot;
  in.snapshot = snapshot;
  in.hp = hp;
  return in;
}

// Lua/AbilityUtils' nAbility.ShouldProcOnHit(target, descriptors) [script]: not a structure, and either a basic physical
// hit that isn't an item's, or AppliesOnHitEffects without IsSpellOverload.
bool ShouldProcOnHit(unsigned lo, bool structure) {
  if (structure) return false;
  const bool basicPhysical = !(lo & kIsItem) && (lo & kIsBasic) && (lo & kIsPhysical);
  const bool onHit = (lo & kAppliesOnHitEffects) && !(lo & kIsSpellOverload);
  return basicPhysical || onHit;
}
}  // namespace

// The hit's steps: which hook runs when (DAMAGE.md steps 4-8, pipeline.h RunHit).
GMS_TEST(DamageEdgePipelineSteps) {
  World w;
  int out = 0, in = 0, fin = 0;
  w.outgoing = [&](float d) { ++out; return d + 1.f; };
  w.incoming = [&](float d) { ++in; return d * 2.f; };
  w.final = [&](float d) { ++fin; return d - 3.f; };
  w.mitigation = 0.5f;
  HitResult r = RunHit(Hit(10.f, kIsMagical, 100.f), w);
  CHECK_EQ(out + in + fin, 3);
  CHECK(Near(r.outgoing, 11.0, 1e-5));
  CHECK(Near(r.incoming, 22.0, 1e-5));
  CHECK(Near(r.preMitigation, 22.0, 1e-5));
  CHECK(Near(r.final, 8.0, 1e-5));  // 22 x 0.5 - 3
  CHECK(Near(r.hpAfter, 92.0, 1e-5));
  CHECK_EQ(r.intChange, 8);
  // no attacker object (a deleted minion's projectile): no Outgoing; with its snapshot Incoming and Final still run
  HitInput h = Hit(10.f, kIsMagical, 100.f);
  h.attacker = false;
  r = RunHit(h, w);
  CHECK_EQ(r.outgoingCalls, 0);
  CHECK_EQ(r.incomingCalls, 1);
  CHECK_EQ(r.finalCalls, 1);
  // true damage: no mitigation; a negative or NaN multiplier counts as 0
  w.outgoing = w.incoming = w.final = nullptr;
  const int before = w.mitigationCalls;
  r = RunHit(Hit(10.f, kIsTrueDamage, 100.f), w);
  CHECK_EQ(w.mitigationCalls, before);
  CHECK(Near(r.dealt, 10.0, 1e-6));
  w.mitigation = -0.5f;
  CHECK_EQ(RunHit(Hit(10.f, kIsPhysical, 100.f), w).dealt, 0.f);
  w.mitigation = std::nanf("");
  CHECK_EQ(RunHit(Hit(10.f, kIsPhysical, 100.f), w).dealt, 0.f);
  // a crit multiplies the initial damage before the ModifyDamage steps
  w.mitigation = 1.f;
  h = Hit(12.f, kBasic, 100.f);
  h.crit = true;
  h.critMult = 2.f;
  CHECK(Near(RunHit(h, w).dealt, 24.0, 1e-6));
  // absorb off ([dgsim] damage_absorb=0): the shields aren't asked
  w.Add(1, kAbsorbGeneric, 3000, 50.f);
  h = Hit(30.f, kIsPhysical, 100.f);
  h.absorb = false;
  const int q = w.shieldQueries;
  CHECK(Near(RunHit(h, w).dealt, 30.0, 1e-6));
  CHECK_EQ(w.shieldQueries, q);
  // lethal: HP floored at 0, integerHpChange from the integer parts
  r = RunHit(Hit(500.f, kIsTrueDamage, 108.5f), w);
  CHECK(r.lethal);
  CHECK_EQ(r.hpAfter, 0.f);
  CHECK_EQ(r.intChange, 108);
  CHECK(Near(r.absorbed, 50.0, 1e-6));  // the 50 shield first
}

// Two or more shields on one target: Dibs' W, Bastion, Deflect, Basko's W and Fenmore's Q, all at once.
GMS_TEST(DamageEdgeShieldOrder) {
  World w;
  // modifier_Dreamer_W_Shield: 35 + 0.35 Power, 3 s, Stacks (max 3: the cap 3 x 35)
  w.Add(1, kAbsorbGeneric, DurationMsFromSeconds(3.f), 35.f);
  // modifier_Item_Bastion: 60 + 20 x level, 2 s (+20 armor and MR while it lasts)
  w.Add(2, kAbsorbGeneric, DurationMsFromSeconds(2.f), 60.f + 20.f * 1);
  // modifier_Item_DeflectShield: 50 + 10 x level, 3 s
  w.Add(3, kAbsorbGeneric, DurationMsFromSeconds(3.f), 50.f + 10.f * 1);
  // modifier_Enforcer_W_Shield: 6 + 0.1 Power per tick on a shaper (x 0.33 on others), 2 s
  w.Add(4, kAbsorbGeneric, DurationMsFromSeconds(2.f), 6.f);
  // modifier_Alpha_Q_Shield: 50 + 0.8 Power, 3 s
  w.Add(5, kAbsorbGeneric, DurationMsFromSeconds(3.f), 50.f);
  CHECK(Near(w.Total(kAbsorbGeneric), 231.0, 1e-4));  // mShieldHealth
  // the one that expires first absorbs first; ties in the order they were applied: Bastion, Basko, Dibs, Deflect, Fenmore
  HitResult r = RunHit(Hit(100.f, kIsPhysical, 450.f), w);
  CHECK_EQ(r.dealt, 0.f);
  CHECK(Near(r.absorbed, 100.0, 1e-5));
  CHECK_EQ(r.absorbHandlers, 3);
  CHECK_EQ(w.Find(2)->remaining, 0.f);   // Bastion 80 gone
  CHECK_EQ(w.Find(4)->remaining, 0.f);   // Basko 6 gone
  CHECK(Near(w.Find(1)->remaining, 21.0, 1e-5));  // Dibs took the last 14
  CHECK_EQ(w.Find(3)->remaining, 60.f);  // Deflect untouched
  CHECK(Near(w.Total(kAbsorbGeneric), 131.0, 1e-4));
  // an emptied shield stays until its time is up and is still asked (it lets everything through)
  w.AdvanceTo(1000);
  r = RunHit(Hit(30.f, kIsMagical, 450.f), w);
  CHECK_EQ(r.dealt, 0.f);
  CHECK_EQ(r.absorbHandlers, 4);  // Bastion (0), Basko (0), Dibs 21, Deflect 9
  CHECK_EQ(w.Find(1)->remaining, 0.f);
  CHECK(Near(w.Find(3)->remaining, 51.0, 1e-5));
  // Dibs casts W again at 1.5 s: ResetDuration and + 35 (cap 105)
  w.AdvanceTo(1500);
  w.Find(1)->Restack(w.nowMs, 35.f, 3 * 35.f);
  // expiry while another is up: at 2 s Bastion and Basko end; Dibs (2.5 s left) now absorbs after Deflect and Fenmore
  w.AdvanceTo(2000);
  CHECK(w.Find(2)->ended && w.Find(4)->ended);
  CHECK(!w.Find(1)->ended && !w.Find(3)->ended);
  r = RunHit(Hit(100.f, kIsPhysical, 450.f), w);
  CHECK_EQ(r.dealt, 0.f);
  CHECK_EQ(w.Find(3)->remaining, 0.f);                   // Deflect 51
  CHECK(Near(w.Find(5)->remaining, 1.0, 1e-5));         // Fenmore 50 - 49
  CHECK(Near(w.Find(1)->remaining, 35.0, 1e-5));        // Dibs untouched
  // at 3 s Deflect and Fenmore's Q end, Dibs' restacked W lasts to 4.5 s
  w.AdvanceTo(3000);
  CHECK(w.Find(3)->ended && w.Find(5)->ended && !w.Find(1)->ended);
  r = RunHit(Hit(50.f, kIsPhysical, 450.f), w);
  CHECK(Near(r.dealt, 15.0, 1e-5));
  CHECK(Near(r.hpAfter, 435.0, 1e-5));
  w.AdvanceTo(4500);
  CHECK(w.Find(1)->ended);
  CHECK_EQ(w.Total(kAbsorbGeneric), 0.f);
  // SortShields keeps ties stable and puts untimed shields last
  ShieldRef s[4];
  s[0].id = 10, s[0].leftMs = kUntimedMs;
  s[1].id = 11, s[1].leftMs = 500;
  s[2].id = 12, s[2].leftMs = 200;
  s[3].id = 13, s[3].leftMs = 500;
  SortShields(s, 4);
  CHECK_EQ(s[0].id, 12u);
  CHECK_EQ(s[1].id, 11u);
  CHECK_EQ(s[2].id, 13u);
  CHECK_EQ(s[3].id, 10u);
  CHECK_EQ(TimeLeftMs(0, 100, 5000), kUntimedMs);
  CHECK_EQ(TimeLeftMs(3000, 1000, 2500), 1500LL);
  CHECK_EQ(TimeLeftMs(3000, 1000, 4500), -500LL);
  CHECK_EQ(ClampAbsorbResult(-3.f, 10.f), 0.f);
  CHECK_EQ(ClampAbsorbResult(std::nanf(""), 10.f), 0.f);
  CHECK_EQ(ClampAbsorbResult(15.f, 10.f), 10.f);
}

// Faith's magic-only shield (modifier_Item_Legendary_StackingMRShield_Shield, rank 3: + 25 every 3 s up to 15 charges,
// untimed; AbsorbDamage_Magical only) beside Dibs' W.
GMS_TEST(DamageEdgeShieldMagicOnly) {
  World w;
  Shield& faith = w.Add(1, kAbsorbMagical, 0, 25.f);
  faith.Restack(3000, 25.f, 25.f * 15);  // the second charge (OnStackModifier at rank 3; untimed, so the start is moot)
  w.Add(2, kAbsorbGeneric, DurationMsFromSeconds(3.f), 35.f);
  CHECK_EQ(w.Total(kAbsorbMagical), 50.f);  // mMagicalShieldHealth
  CHECK_EQ(w.Total(kAbsorbGeneric), 35.f);  // mShieldHealth
  CHECK_EQ(TypedAbsorbIndex(kIsMagical), kAbsorbMagical);
  CHECK_EQ(TypedAbsorbIndex(kBasic), kAbsorbPhysical);
  CHECK_EQ(TypedAbsorbIndex(kIsTrueDamage | kIsMagical), 0);
  // magical: the typed pass first, so Faith absorbs before Dibs' W
  HitResult r = RunHit(Hit(40.f, kIsMagical, 450.f), w);
  CHECK_EQ(r.dealt, 0.f);
  CHECK_EQ(w.Find(1)->remaining, 10.f);
  CHECK_EQ(w.Find(2)->remaining, 35.f);
  r = RunHit(Hit(30.f, kIsMagical, 450.f), w);  // 10 from Faith, 20 from Dibs
  CHECK_EQ(r.dealt, 0.f);
  CHECK_EQ(w.Find(1)->remaining, 0.f);
  CHECK_EQ(w.Find(2)->remaining, 15.f);
  // physical: no script has an AbsorbDamage_Physical handler, so only the generic pass (Faith not asked)
  w.Find(1)->remaining = 50.f;
  const int faithCalls = w.Find(1)->calls;
  r = RunHit(Hit(40.f, kIsPhysical, 450.f), w);
  CHECK(Near(r.dealt, 25.0, 1e-5));
  CHECK_EQ(w.Find(1)->calls, faithCalls);
  CHECK_EQ(w.Find(1)->remaining, 50.f);
  // true damage: generic only
  r = RunHit(Hit(10.f, kIsTrueDamage, 450.f), w);
  CHECK_EQ(w.Find(1)->remaining, 50.f);
  CHECK(Near(r.dealt, 10.0, 1e-5));
  // rank 1 (Conviction's): no absorb, but ModifyDamageFinal -10 magical (-5 DoT), floored at 0 by the script
  World w1;
  w1.Add(1, kAbsorbMagical, 0, 0.f, Shield::kPassThrough);
  w1.final = [](float d) { return d - 10.f < 0.f ? 0.f : d - 10.f; };
  r = RunHit(Hit(8.f, kIsMagical, 450.f), w1);
  CHECK_EQ(r.dealt, 0.f);
  CHECK_EQ(w1.Find(1)->calls, 0);  // nothing left to absorb: not asked
  r = RunHit(Hit(25.f, kIsMagical, 450.f), w1);
  CHECK(Near(r.dealt, 15.0, 1e-5));
  CHECK_EQ(w1.Find(1)->calls, 1);  // asked, let everything through
}

// Damage at the moment a recall starts: ability_returntobase (OnTookDamage returns false: the recall ends) and
// modifier_returntobase (untimed AbsorbDamage that sets interrupt and lets everything through).
GMS_TEST(DamageEdgeRecall) {
  World w;
  w.Add(1, kAbsorbGeneric, DurationMsFromSeconds(3.f), 35.f);  // Dibs' W
  w.Add(2, kAbsorbGeneric, 0, 0.f, Shield::kRecall);
  // a hit the W takes whole: the recall modifier isn't reached, but OnTookDamage fires (dealt 0) and ends the recall
  HitResult r = RunHit(Hit(20.f, kIsPhysical, 450.f), w);
  CHECK_EQ(r.dealt, 0.f);
  CHECK(!w.Find(2)->interrupted);
  CHECK(FiresDamageEvents(kIsPhysical));
  // a hit through the W: the untimed recall modifier comes last and sees what is left
  r = RunHit(Hit(50.f, kIsPhysical, 450.f), w);
  CHECK(Near(r.dealt, 35.0, 1e-5));
  CHECK(w.Find(2)->interrupted);
  CHECK(Near(w.Find(2)->minSeen, 35.0, 1e-5));
  // a hit without damage callbacks (an overload hit, a lifedrain heal's descriptors): no OnTookDamage
  CHECK(!FiresDamageEvents(kIsMagical | kIsSpellOverload | kNoDamageCallbacks));
  // a DoT tick and a self-inflicted cost fire it too (the recall's handler checks nothing)
  CHECK(FiresDamageEvents(kIsMagical | kIsDoT));
  CHECK(FiresDamageEvents(kHealthCost));
}

// A health cost right after a shield absorbed damage: Fenmore's W (ability_Alpha_W_Stim, 25..85) and Viyana's E
// (ability_BloodMage_E_SoulSuck, 25..65), both health-resource shapers (ClassAttributes characterResourceType health).
GMS_TEST(DamageEdgeHealthCost) {
  // the pool is HP + mShieldHealth; a cost equal to the pool is refused for health (not for mana)
  CHECK(Near(HealthPool(300.25f, 10.5f), 310.75, 1e-5));
  CHECK(CanPayCost(310.75f, 25.f, true));
  CHECK(!CanPayCost(25.f, 25.f, true));
  CHECK(CanPayCost(25.5f, 25.f, true));
  CHECK(!CanPayCost(24.f, 25.f, true));
  CHECK(CanPayCost(25.f, 25.f, false));
  CHECK(CanPayCost(0.f, 0.f, true));
  // the payment is the cost (pool - max(0, pool - cost)), or the whole pool when it is less
  CHECK_EQ(HealthCostDamage(300.25f, 10.5f, 25.f), 25.f);
  CHECK(Near(HealthCostDamage(10.f, 5.f, 25.f), 15.0, 1e-6));
  // Fenmore with his Q shield (10.5 left after a hit) pays W rank 1: the shield first, then HP; fractions kept
  World w;
  w.Add(1, kAbsorbGeneric, DurationMsFromSeconds(3.f), 10.5f);
  int calls = 0;
  w.incoming = w.final = [&](float d) { ++calls; return d * 0.9f; };  // a reduction handler (must not touch a cost)
  HitInput cost = Hit(HealthCostDamage(300.25f, w.Total(kAbsorbGeneric), 25.f), kHealthCost, 300.25f, false);
  HitResult r = RunHit(cost, w);
  CHECK_EQ(calls, 0);  // D13: no ModifyDamageIncoming/_Final without a snapshot
  CHECK_EQ(w.mitigationCalls, 0);
  CHECK(Near(r.absorbed, 10.5, 1e-5));
  CHECK(Near(r.dealt, 14.5, 1e-5));
  CHECK(Near(r.hpAfter, 285.75, 1e-4));
  CHECK_EQ(r.intChange, 15);  // (int)300.25 - (int)285.75
  CHECK(!r.lethal);
  CHECK_EQ(w.Total(kAbsorbGeneric), 0.f);  // the host refreshes mShieldHealth after the hit
  // the smallest payable cost never kills: HP 25.5, cost 25 -> 0.5 left
  World w2;
  r = RunHit(Hit(HealthCostDamage(25.5f, 0.f, 25.f), kHealthCost, 25.5f, false), w2);
  CHECK(Near(r.hpAfter, 0.5, 1e-5));
  CHECK(!r.lethal);
  // Viyana's E with modifier_BloodMage_E_Shield up (26 at rank 1): the 25 comes out of the shield, HP unchanged
  World v;
  v.Add(1, kAbsorbGeneric, DurationMsFromSeconds(2.f), 26.f);
  CHECK(CanPayCost(HealthPool(100.f, 26.f), 25.f, true));
  r = RunHit(Hit(HealthCostDamage(100.f, 26.f, 25.f), kHealthCost, 100.f, false), v);
  CHECK_EQ(r.dealt, 0.f);
  CHECK_EQ(r.hpAfter, 100.f);
  CHECK(Near(v.Total(kAbsorbGeneric), 1.0, 1e-5));
  // with HP 20 and the shield spent, a cost of 25 is refused, unless mShieldHealth is stale: then the payment kills
  // (the client validates against +0x4C, the shields absorb what they really have). The host keeps +0x4C current
  // after every hit and once per frame; a shield ended mid-frame by MarkForDelete stays counted until the frame's end.
  CHECK(!CanPayCost(HealthPool(20.f, 0.f), 25.f, true));
  CHECK(CanPayCost(HealthPool(20.f, 50.f), 25.f, true));  // stale +0x4C 50
  World stale;
  r = RunHit(Hit(HealthCostDamage(20.f, 50.f, 25.f), kHealthCost, 20.f, false), stale);
  CHECK(r.lethal);
}

// Damage against negative armor or magic resistance, and basic attacks against defense.
GMS_TEST(DamageEdgeNegativeDefense) {
  // below 0: 2 - 100 / (100 - d), penetration ignored
  CHECK(Near(DefenseMultiplierWithPenetration(-20.f, 15.f, true, 30.f, 0.5f, 0.5f), 1.16667, 1e-4));
  CHECK(Near(DefenseMultiplierWithPenetration(-100.f, 0.f, false, 0.f, 0.f, 0.f), 1.5, 1e-6));
  CHECK(DefenseMultiplierWithPenetration(-1e6f, 0.f, false, 0.f, 0.f, 0.f) < 2.f);
  CHECK_EQ(DefenseMultiplierWithPenetration(0.f, 0.f, true, 30.f, 0.5f, 0.5f), 1.f);
  // shred: armor's percentage is a plain product (it brings negative armor toward 0), MR's flips below 0
  CHECK(Near(TotalArmor(-20.f, -0.3f, 0.f, false), -14.0, 1e-5));
  CHECK(Near(TotalMR(-20.f, -0.3f, 0.f, false), -26.0, 1e-5));
  CHECK(Near(TotalArmor(50.f, -0.3f, -10.f, false), 25.0, 1e-5));
  CHECK(Near(TotalMR(50.f, -0.3f, -10.f, false), 25.0, 1e-5));
  CHECK_EQ(TotalArmor(50.f, -0.3f, -10.f, true), 50.f);  // a structure ignores shred
  CHECK_EQ(TotalMR(-5.f, -0.3f, -10.f, true), -5.f);
  // a flat shred past 0 (modifier_Artist_Q_Shred -15 on MR 10): -5 -> x 1.0476
  CHECK(Near(DefenseMultiplierWithPenetration(TotalMR(10.f, 0.f, -15.f, false), 10.f, true, 0.f, 0.f, 0.f), 1.04762, 1e-4));
  // the MR steal (modifier_Item_Legendary_OnDamage_StealMR_Shred: -3 per stack x the holder's stacks, 5 stacks) on a
  // level-1 Fenmore (MR 33): 33 - 15 = 18; on MR 9: -6 -> 2 - 100/106
  CHECK(Near(DefenseMultiplierWithPenetration(33.f - 15.f, 33.f, true, 0.f, 0.f, 0.f), 100.0 / 118.0, 1e-5));
  CHECK(Near(DefenseMultiplierWithPenetration(9.f - 15.f, 9.f, true, 0.f, 0.f, 0.f), 2.0 - 100.0 / 106.0, 1e-5));
  // basic attacks take the defense of their damage type like any hit (every shaper's and minion's basic is
  // IsBasic | IsPhysicalDamage); true damage takes none
  CHECK(UsesPhysicalDefense(kBasic));
  CHECK(UsesPhysicalDefense(kIsBasic | kIsPhysical | kIsProjectile));
  CHECK(!UsesMagicalDefense(kBasic));
  CHECK(!UsesPhysicalDefense(kIsTrueDamage | kIsPhysical));
  CHECK(UsesMagicalDefense(kIsMagical | kIsItem));
  World w;
  w.mitigation = DefenseMultiplierWithPenetration(TotalArmor(15.f, 0.f, 0.f, false), 15.f, true, 0.f, 0.f, 0.f);
  const HitResult r = RunHit(Hit(50.f, kBasic, 450.f), w);
  CHECK_EQ(w.mitigationCalls, 1);
  CHECK(Near(r.dealt, 50.0 * 100.0 / 115.0, 1e-4));
  // negative armor amplifies a basic too: armor -20 -> 50 x 1.1667
  w.mitigation = DefenseMultiplierWithPenetration(-20.f, 15.f, true, 0.f, 0.f, 0.f);
  CHECK(Near(RunHit(Hit(50.f, kBasic, 450.f), w).dealt, 58.3333, 1e-3));
}

// Defense penetration: flat DefensePenetration (30; Aggression +15, Judgement +15, Destruction's passive +30),
// PercentDefensePenetration (31; the Bindings' passive 0.7), PercentBonusDefensePenetration (32; Rage's passive 0.5),
// all from the attacker's snapshot, the same three for armor and MR.
GMS_TEST(DamageEdgePenetration) {
  // Fenmore level 6: RawArmor 15 + 3.1 x 5 = 30.5, + 40 from Discipline -> 70.5 (bonus 40)
  const float raw = 15.f + 3.1f * 5.f, total = raw + 40.f;
  CHECK(Near(DefenseMultiplierWithPenetration(total, raw, true, 0.f, 0.f, 0.f), 100.0 / 170.5, 1e-5));
  // Rage: half the bonus pierced: 70.5 - 20 = 50.5
  CHECK(Near(DefenseMultiplierWithPenetration(total, raw, true, 0.f, 0.f, 0.5f), 100.0 / 150.5, 1e-5));
  // Rage + Destruction: 50.5 - 30 = 20.5
  CHECK(Near(DefenseMultiplierWithPenetration(total, raw, true, 30.f, 0.f, 0.5f), 100.0 / 120.5, 1e-5));
  // Aggression + Judgement + Destruction (flat 60): 10.5
  CHECK(Near(DefenseMultiplierWithPenetration(total, raw, true, 60.f, 0.f, 0.f), 100.0 / 110.5, 1e-5));
  // penetration never takes defense below 0 (no bonus damage from it)
  CHECK_EQ(DefenseMultiplierWithPenetration(total, raw, true, 100.f, 0.f, 0.f), 1.f);
  // the order: percent of the bonus, then percent, then flat: (70.5 - 20) x 0.3 - 30 < 0 -> 0
  CHECK_EQ(DefenseMultiplierWithPenetration(total, raw, true, 30.f, 0.7f, 0.5f), 1.f);
  CHECK(Near(DefenseMultiplierWithPenetration(total, raw, true, 10.f, 0.7f, 0.5f), 100.0 / (50.5 * 0.3 - 10.0 + 100.0),
             1e-5));
  // no bonus (defense at its raw value): percent-of-bonus does nothing
  CHECK(Near(DefenseMultiplierWithPenetration(raw, raw, true, 0.f, 0.f, 0.5f), 100.0 / 130.5, 1e-5));
  // without a snapshot or against a structure: no penetration (DAMAGE.md T4/T5: armor 50 with 0.7 -> 66.67 on a Binding)
  CHECK(Near(DefenseMultiplierWithPenetration(total, raw, false, 60.f, 0.7f, 0.5f), 100.0 / 170.5, 1e-5));
  CHECK(Near(100.f * DefenseMultiplierWithPenetration(50.f, 50.f, false, 0.f, 0.7f, 0.f), 66.67, 0.01));
  CHECK(Near(100.f * DefenseMultiplierWithPenetration(15.f, 15.f, true, 0.f, 0.7f, 0.f), 95.69, 0.01));  // T5
}

// An attack reduced below zero (Discipline: modifier_Item_Legendary_OnDamage_PowerReduction_Debuff, Power -10 x the
// holder's stacks for 3 s): a minion's basic 12 + Power (-20) x 1 = -8.
GMS_TEST(DamageEdgeBelowZero) {
  World w;
  w.Add(1, kAbsorbGeneric, DurationMsFromSeconds(3.f), 35.f);
  HitInput h = Hit(12.f + (-20.f) * 1.f, kBasic, 450.f);
  h.crit = true;  // a crit doubles it to -16
  h.critMult = 2.f;
  HitResult r = RunHit(h, w);
  CHECK(Near(r.outgoing, -16.0, 1e-5));
  CHECK_EQ(r.final, 0.f);
  CHECK_EQ(r.dealt, 0.f);
  CHECK_EQ(r.hpAfter, 450.f);
  CHECK_EQ(r.intChange, 0);
  CHECK(!r.lethal);
  CHECK_EQ(w.Find(1)->calls, 0);  // no absorb handler ever sees 0 or less (it would grow the shield)
  CHECK_EQ(w.Find(1)->remaining, 35.f);
  // an outgoing flat bonus (Finesse's + 20 on a basic) still counts from the negative value: -16 + 20 = 4
  w.outgoing = [](float d) { return d + 20.f; };
  r = RunHit(h, w);
  CHECK(Near(r.final, 4.0, 1e-5));
  CHECK(Near(w.Find(1)->minSeen, 4.0, 1e-5));
  // nothing follows a hit of 0: no lifedrain, no overload; the events still fire [assumed]
  CHECK_EQ(LifedrainAmount(0.f, 0.5f, kBasic, true), 0.f);
  CHECK_EQ(OverloadDamage(-16.f, 0.5f, 0, false, 0.f, kIsMagical, true), 0.f);
  // a negative pre-mitigation damage that a Final handler lifted above 0 doesn't overload with a negative amount
  CHECK_EQ(OverloadDamage(-5.f, 0.5f, 0, false, 3.f, kIsMagical, true), 0.f);
  CHECK(Near(OverloadDamage(100.f, 0.5f, 0, false, 70.f, kIsMagical, true), 50.0, 1e-5));
  CHECK_EQ(OverloadDamage(100.f, 0.5f, 1, false, 70.f, kIsMagical, true), 0.f);  // never an overload of an overload
  CHECK_EQ(OverloadDamage(100.f, 0.5f, 0, true, 70.f, kIsMagical, true), 0.f);   // nor after a kill
  CHECK(Near(OverloadPercent(100.f, 2.f, 0.f), 0.5, 1e-6));
  CHECK(FiresDamageEvents(kBasic));
}

// Finesse's bonus and stacks and Resonance when a shield blocks the whole hit.
GMS_TEST(DamageEdgeOnHitThroughShield) {
  // ShouldProcOnHit (Lua/AbilityUtils): a basic physical hit or AppliesOnHitEffects; not an item's hit, not a structure
  CHECK(ShouldProcOnHit(kBasic, false));
  CHECK(!ShouldProcOnHit(kBasic, true));
  CHECK(!ShouldProcOnHit(kIsMagical | kIsItem, false));  // Resonance's own zap doesn't proc Finesse
  CHECK(ShouldProcOnHit(kIsMagical | kAppliesOnHitEffects, false));
  CHECK(!ShouldProcOnHit(kIsMagical | kAppliesOnHitEffects | kIsSpellOverload, false));
  // modifier_Item_Legendary_OnBasic_StackingDamageAmp: + 20 in ModifyDamageOutgoing when it procs, before the shield
  World w;
  w.Add(1, kAbsorbGeneric, DurationMsFromSeconds(2.f), 80.f);  // Bastion at level 1
  w.outgoing = [](float d) { return ShouldProcOnHit(kBasic, false) ? d + 20.f : d; };
  HitResult r = RunHit(Hit(50.f, kBasic, 450.f), w);
  CHECK_EQ(r.dealt, 0.f);
  CHECK(Near(r.absorbed, 70.0, 1e-5));
  // OnDealtDamage fires with dealt 0 (FiresDamageEvents): Finesse's stack on the target is requested regardless of the
  // damage (its script checks only the descriptors and the target), so 3 hits on the shield make 3 stacks
  int stacks = 0;
  for (int i = 0; i < 3; ++i)
    if (FiresDamageEvents(kBasic) && ShouldProcOnHit(kBasic, false)) stacks = std::min(stacks + 1, 5);
  CHECK_EQ(stacks, 3);
  // the stacks' amp (_DamageAmp: x (1 + 0.05 x stacks) in ModifyDamageFinal for the caster's hits) also lands before
  // the shield: 70 x 1.15 = 80.5 against the remaining 10 -> 70.5 through
  w.final = [&](float d) { return d * (1.f + 0.05f * stacks); };
  r = RunHit(Hit(50.f, kBasic, 450.f), w);
  CHECK(Near(r.final, 80.5, 1e-4));
  CHECK(Near(r.dealt, 70.5, 1e-4));
  // Resonance (modifier_Item_Legendary_OnBasic_MagicDamageNearby): a zap of dealtDamageDamage x 0.5 to another enemy,
  // so 0 after a fully shielded hit, and its own 15 + 0.1 Power magical item hit on the shielded target
  World target;
  target.Add(1, kAbsorbGeneric, DurationMsFromSeconds(3.f), 100.f);
  r = RunHit(Hit(50.f, kBasic, 450.f), target);
  CHECK_EQ(r.dealt, 0.f);
  const float zap = r.dealt * 0.5f;
  World other;
  HitResult z = RunHit(Hit(zap, kIsMagical | kIsItem, 450.f), other);
  CHECK_EQ(z.dealt, 0.f);
  CHECK_EQ(z.intChange, 0);
  HitResult own = RunHit(Hit(15.f + 0.1f * 0.f, kIsMagical | kIsItem, 450.f), target);
  CHECK_EQ(own.dealt, 0.f);
  CHECK(Near(target.Find(1)->remaining, 35.0, 1e-5));
}

// Lifedrain on basic attacks and abilities, and the healing number.
GMS_TEST(DamageEdgeLifedrain) {
  CHECK(Near(LifedrainAmount(100.f, 0.1f, kBasic, true), 10.0, 1e-5));
  CHECK(Near(LifedrainAmount(100.f, 0.1f, kIsMagical, true), 10.0, 1e-5));  // abilities drain at full rate
  CHECK(Near(LifedrainAmount(100.f, 0.1f, kIsMagical | kIsAoE, true), 3.3, 1e-4));
  CHECK(Near(LifedrainAmount(100.f, 0.1f, kIsMagical | kIsDoT, true), 3.3, 1e-4));
  CHECK_EQ(LifedrainAmount(0.f, 0.1f, kBasic, true), 0.f);   // a shield took it all: nothing to drain
  CHECK_EQ(LifedrainAmount(100.f, 0.f, kBasic, true), 0.f);
  CHECK_EQ(LifedrainAmount(100.f, 0.1f, kBasic, false), 0.f);  // D13: never for a dead attacker
  // the heal core with noScaling (lifesteal): HealIncrease is ignored, HealingReduction is not
  HealResult h = ApplyHeal(200.f, 450.f, 10.f, true, 0.5f, 0.f, 1.f, 0.f);
  CHECK(Near(h.amount, 10.0, 1e-6));
  CHECK(Near(h.hpAfter, 210.0, 1e-5));
  CHECK_EQ(h.intGain, 10);
  CHECK(h.reported);
  h = ApplyHeal(200.f, 450.f, 10.f, true, 0.f, 0.f, 1.f, 0.5f);  // Mortal Strike 50 %
  CHECK(Near(h.amount, 5.0, 1e-6));
  CHECK(Near(h.hpAfter, 205.0, 1e-5));
  // the healing number: the combat event carries the full amount (deltaHealth -amount) and the integer gain; no event
  // when the integer HP didn't rise (at full HP, or a fraction that crosses no integer)
  h = ApplyHeal(445.f, 450.f, 10.f, true, 0.f, 0.f, 1.f, 0.f);
  CHECK(Near(h.amount, 10.0, 1e-6));
  CHECK_EQ(h.hpAfter, 450.f);
  CHECK_EQ(h.intGain, 5);
  h = ApplyHeal(450.f, 450.f, 10.f, true, 0.f, 0.f, 1.f, 0.f);
  CHECK(!h.reported);
  h = ApplyHeal(200.2f, 450.f, 0.5f, true, 0.f, 0.f, 1.f, 0.f);
  CHECK(!h.reported);
  h = ApplyHeal(200.6f, 450.f, 0.5f, true, 0.f, 0.f, 1.f, 0.f);
  CHECK(h.reported);
  CHECK_EQ(h.intGain, 1);
  // a scaled heal (an ability's): x (1 + HealIncrease) + Power x coefficient, x the crit
  h = ApplyHeal(100.f, 450.f, 100.f, false, 0.2f, 30.f, 1.f, 0.f);
  CHECK(Near(h.amount, 150.0, 1e-4));
  // a heal of a corpse resurrects it in the heal core; the Lua binding refuses it without its resurrect flag
  h = ApplyHeal(0.f, 450.f, 10.f, true, 0.f, 0.f, 1.f, 0.f);
  CHECK(h.resurrected);
  CHECK(!HealAllowed(0.f, false));
  CHECK(HealAllowed(0.f, true));
  CHECK(HealAllowed(0.1f, false));
  // HealingReduction above 1 would turn a heal into a loss (the core has no floor); Mortal Strike's sources combine with
  // diminishing returns (nAttributeOp.kDiminishingReturns), which stays below 1 [assumed]
  h = ApplyHeal(100.f, 450.f, 10.f, true, 0.f, 0.f, 1.f, 1.5f);
  CHECK(h.amount < 0.f);
}

// Mortal Strike ending on time, the Recently Transfused / Devotion lockout, King of Masks' stacker, Mikella's W coins:
// modifier lifetime.
GMS_TEST(DamageEdgeLifetime) {
  const unsigned dur = kDeactOnDuration | kDeactOnAgentDeath;
  // modifier_StatusEffect_MortalStrike: duration {0}, then SetDuration(3 s) from Pain's and Corruption's on-damage
  // passive, 5 s from the Drain spell (their "duration" request parameter)
  CHECK_EQ(DurationMsFromSeconds(3.f), 3000);
  CHECK_EQ(DurationMsFromSeconds(5.f), 5000);
  CHECK_EQ(DurationMsFromSeconds(0.1f), 100);
  CHECK_EQ(DurationMsFromSeconds(15.f), 15000);
  CHECK(!ModifierExpired(dur, 3000, 10000, 12999));
  CHECK(ModifierExpired(dur, 3000, 10000, 13000));
  CHECK(!ModifierExpired(dur, 0, 10000, 99999));         // without SetDuration it would never end on time
  CHECK(!ModifierExpired(kDeactOnAgentDeath, 3000, 10000, 13000));  // no OnDuration: no expiry
  CHECK(ModifierAtDeath(dur, false) == AtDeath::kEnd);     // and it ends at the victim's death
  // the Purity / Devotion lockout (modifier_Item_*_HealNearbyAlly_RecentlyHealed): SetDuration(15 s), OnDuration |
  // OnAgentDeath; the healing item's own passive (modifier_Item_*_HealNearbyAlly, OnAgentDestroyed | OnAgentDeath) is an
  // item passive the death keeps (Q3)
  CHECK(!ModifierExpired(dur, DurationMsFromSeconds(15.f), 60000, 74999));
  CHECK(ModifierExpired(dur, DurationMsFromSeconds(15.f), 60000, 75000));
  CHECK(ModifierAtDeath(dur, false) == AtDeath::kEnd);
  CHECK(ModifierAtDeath(kDeactOnAgentDestroyed | kDeactOnAgentDeath, true) == AtDeath::kKeep);
  CHECK(ModifierAtDeath(kDeactOnAgentDestroyed | kDeactOnAgentDeath, false) == AtDeath::kEnd);
  // King of Masks' damage stacker (modifier_KingOfMasks_Q_DamageStacker: 0.1 s, ResetDuration on each stack, its
  // damage in Deactivate): three tentacle hits 50 ms apart end it 100 ms after the last
  std::uint64_t start = 0;
  for (std::uint64_t hitMs : {20000ull, 20050ull, 20100ull}) {
    if (start) CHECK(!ModifierExpired(dur, 100, start, hitMs));  // still running when the next tentacle hits
    start = hitMs;                                                // ResetDuration
  }
  CHECK_EQ(TimeLeftMs(100, start, 20150), 50LL);
  CHECK(!ModifierExpired(dur, 100, start, 20199));
  CHECK(ModifierExpired(dur, 100, start, 20200));
  // the roles' passives: Gladiator, Tactician, Hunter end at the death (OnAgentDeath), Predator only when destroyed
  CHECK(ModifierAtDeath(kDeactOnAgentDeath, false) == AtDeath::kEnd);
  CHECK(ModifierAtDeath(kDeactOnAgentDestroyed, false) == AtDeath::kKeep);
  // Mikella's W: the ability's Tick waits 5 s, the three coin modifiers last 5 s (OnDuration | OnAgentDeath) from the
  // same moment, so all four end at the same end pass
  CHECK(ModifierExpired(dur, 5000, 30000, 35000));
  // the death's ability rule (D6c): finishOnDeath kept, a held agent interrupted, a released one runs on
  CHECK(AbilityAtDeath(true, true, false) == AtDeath::kKeep);
  CHECK(AbilityAtDeath(false, true, false) == AtDeath::kInterrupt);
  CHECK(AbilityAtDeath(false, false, false) == AtDeath::kKeepReleased);
  CHECK(AbilityAtDeath(false, false, true) == AtDeath::kInterrupt);
}

// A Tactician's attack landing just after the attacker died (a projectile in flight; damage from a Deactivate).
GMS_TEST(DamageEdgeDeadAttacker) {
  // the kill credit stays (a projectile's snapshot is its own copy, so the attacker is still known)
  CHECK(RecordsAttacker(true, 50.f, 7, 9, false));
  CHECK(!RecordsAttacker(false, 50.f, 7, 9, false));  // no snapshot (a health cost)
  CHECK(!RecordsAttacker(true, 0.f, 7, 9, false));
  CHECK(!RecordsAttacker(true, 50.f, 9, 9, false));   // self-inflicted
  CHECK(!RecordsAttacker(true, 50.f, 7, 9, true));    // friendly fire
  // no aggro from a corpse, and no life drained into it
  CHECK(DrawsAggro(50.f, true, true));
  CHECK(!DrawsAggro(50.f, true, false));
  CHECK(!DrawsAggro(50.f, false, true));
  CHECK(!DrawsAggro(0.f, true, true));
  CHECK_EQ(LifedrainAmount(50.f, 0.2f, kBasic, false), 0.f);
  // its OnDealtDamage still fires (the Tactician's vim, if its role passive still exists: it ends 3 scheduler runs
  // after the death)
  CHECK(FiresDamageEvents(kBasic | kIsProjectile));
}
