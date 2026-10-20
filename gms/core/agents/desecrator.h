// gms/core/agents (DESECBOT-3): the Desecrator's kit layer, without the engine. Header-only.
//
// GLG_Tree's guide: E first (the root is the sure hit), then Q on the rooted target;
// R's three corpses into bunched or locked-down enemies, spread (the fields' modifier is Unique, they don't add);
// Wither only on the enemy carry or a diving bruiser; Drain to secure a kill. The kit in build 854 [script, data]:
//   E  ability_Desecrator_E_GrabHands  a circle at a point, range 22.36, radius 6, the hands strike 1 s after the cast
//                                      (timeToHit 1) with modifier_Desecrator_E_Root (1 s)
//   Q  ability_Desecrator_Q_Graves     a line of 28.98 at a point, two projectiles at 16.7 u/s,
//                                      modifier_Desecrator_Q_Knockup (0.75 s)
//   R  ability_Desecrator_R_Mortar     a circle at a point, range 50, radius 7, three activations within 14 s
//                                      (reactivationTime), each field ticking modifier_Desecrator_R_MortarIcon for 8 s
//   Drain, Wither (spells)             a creature target, enemy players, range 16.18
// The host (gms/engine/agents.cpp DesecratorKitStep) measures the enemies (their modifiers, role, archetype, motion)
// and the allies (recent damage, who hit them) and asks DecideDesecrator once per think in the fight and lane phases;
// it also aims every E of this kit with LeadPoint and holds every Q while DesecQHeld (the gambits' and BOTREFLEX's).
// Every number below is the kit's data or [assumed] where marked.
#pragma once

#include <cmath>
#include <cstdint>
#include <cstring>
#include <vector>

namespace gms::agents {

struct DesecConfig {
  float eRange = 22.36f;          // GrabHands' castRange
  float eLeadS = 1.0f;            // the hands strike this long after the cast
  float eMaxLead = 11.f;          // the lead is at most this far [assumed: a shaper's ~11 u/s over the 1 s]
  long long comboHoldMs = 1300;   // after an E, Q waits for the root this long at most (the root lands at 1 s)
  long long comboQMs = 2300;      // ... and the combo's Q goes at the rooted target until this long after the E (the
                                  // root lands 1.0-1.2 s after the request and lasts 1 s, as measured live)
  float qRange = 28.f;            // a Q at the rooted target only within the line's 28.98
  float rRange = 50.f;            // Mortar's castRange
  float rRadius = 7.f;            // a corpse's field
  float rSpread = 14.f;           // the 2nd and 3rd corpse at least this far from the earlier ones (two radii)
  long long rWindowMs = 14000;    // reactivationTime: the 2nd and 3rd corpse within this of the 1st
  int rClusterMin = 2;            // the 1st corpse: at least this many enemy shapers in one field (or one locked down)
  float drainRange = 16.18f;      // ability_Item_Drain's range
  float drainHp = 0.25f;          // Drain at an enemy shaper under this HP fraction (the guide's "secure kills")
  float witherRange = 16.f;       // Wither within this (its range 16.18)
  long long bruiserHitMs = 3000;  // a bruiser that hit an ally this recently is diving [assumed]
  float peelAllyRange = 20.f;     // condition_NearbyAllyHasTakenDamage's searchRadius
  float peelDamage = 0.10f;       // ... its damageThreshold (recent damage / max HP)
  long long peelHitMs = 3000;     // the ally's attacker hit it this recently [assumed]
  long long retryMs = 1000;       // a refused cast of the layer is tried again after this
};

// The phases the layer acts in, from the Guardian AI's phase names (BOT_Guardian_Tactician): the fights (its own casts:
// the combo, R, Drain, Wither, the peel) and the lane (the peel only); elsewhere only the combo's Q follows its E.
enum DesecPhaseClass : int { kDesecPhaseOther = 0, kDesecPhaseLane = 1, kDesecPhaseFight = 2 };

inline int DesecPhaseClassOf(const char* phase) {
  if (!phase) return kDesecPhaseOther;
  static const char* const kFight[] = {"EngageForKill", "Self-Defense", "BackToAction", "HarassWithE", "WellFight",
                                       "FightParasite", "BaseDefend", "AttackBase"};
  static const char* const kLane[] = {"CautiousLaning", "LastHit", "Push", "TowerPush"};
  for (const char* f : kFight)
    if (std::strstr(phase, f)) return kDesecPhaseFight;
  for (const char* l : kLane)
    if (std::strstr(phase, l)) return kDesecPhaseLane;
  return kDesecPhaseOther;
}

// One enemy shaper as the host saw it this think.
struct DesecEnemy {
  std::uint32_t id = 0;
  float pos[2] = {};
  float vel[2] = {};              // units/s (DesecMotion)
  float dist = 0.f;               // from the bot
  float hpFrac = 1.f;
  bool visible = false;
  bool rooted = false;            // carries modifier_Desecrator_E_Root
  bool knockedUp = false;         // carries modifier_Desecrator_Q_Knockup
  bool mortared = false;          // carries modifier_Desecrator_R_MortarIcon (stands in a corpse's field)
  int role = -1;                  // mRole (RolesTuning index): 0 Gladiator
  int archetype = -1;             // PlayerClass.CharacterArchetype: 1 bruiser
  long long hitAllyAgeMs = -1;    // since it last hit one of the bot's allied shapers (-1 never)
};

// One allied shaper (not the bot).
struct DesecAlly {
  std::uint32_t id = 0;
  float dist = 0.f;
  float recentDamage = 0.f;       // damage over the last DesecRecentDamage window / max HP
  std::uint32_t attacker = 0;     // the enemy shaper that hit it last
  long long attackerAgeMs = -1;   // how long ago (-1 never)
};

struct DesecSelf {
  float pos[2] = {};
  int phaseClass = kDesecPhaseOther;
  long long nowMs = 0;
  int rankQ = 0, rankE = 0, rankR = 0;
  bool hasDrain = false, hasWither = false;  // the spell sits in one of the bot's spell slots
};

// What the layer remembers per bot between thinks.
struct DesecMemory {
  std::uint32_t eTarget = 0;      // the enemy shaper the last E was aimed at (0 none or the combo done)
  long long eCastMs = -1;
  int rCorpses = 0;               // corpses of the current cast (0-3)
  long long rFirstMs = -1;        // the 1st corpse's time
  float rPoints[3][2] = {};
  long long nextTryMs[8] = {};    // per DesecKind: after a refusal
};

enum DesecKind : int { kDesecNone = 0, kDesecComboQ, kDesecR, kDesecDrain, kDesecWither, kDesecPeelE, kDesecPeelQ };

inline const char* DesecKindName(int k) {
  switch (k) {
    case kDesecComboQ: return "combo Q";
    case kDesecR: return "R";
    case kDesecDrain: return "Drain";
    case kDesecWither: return "Wither";
    case kDesecPeelE: return "peel E";
    case kDesecPeelQ: return "peel Q";
    default: return "none";
  }
}

struct DesecAction {
  int kind = kDesecNone;
  int enemy = -1;                 // index into the enemies
  float point[2] = {};            // where it is aimed
  const char* why = "";
  int count = 0;                  // R: enemy shapers within the field at the point
};

// ---- the motion and damage trackers (per shaper, fed once per frame) -------------------------------------------------

// The velocity from positions at least minDtMs apart, smoothed (half the new estimate each step). A jump (a recall, a
// respawn, a dash longer than maxSpeed) resets it to zero.
struct DesecMotion {
  float last[2] = {};
  long long lastMs = -1;
  float vel[2] = {};
  void Note(const float* p, long long ms, long long minDtMs = 100, float maxSpeed = 40.f) {
    if (lastMs < 0) {
      last[0] = p[0];
      last[1] = p[1];
      lastMs = ms;
      return;
    }
    const long long dt = ms - lastMs;
    if (dt < minDtMs) return;
    const float s = 1000.f / static_cast<float>(dt);
    const float vx = (p[0] - last[0]) * s, vy = (p[1] - last[1]) * s;
    if (std::sqrt(vx * vx + vy * vy) > maxSpeed) {
      vel[0] = vel[1] = 0.f;
    } else {
      vel[0] = 0.5f * vel[0] + 0.5f * vx;
      vel[1] = 0.5f * vel[1] + 0.5f * vy;
    }
    last[0] = p[0];
    last[1] = p[1];
    lastMs = ms;
  }
};

// The HP lost within the last windowMs (heals don't count), from HP samples at least stepMs apart; in max-HP fractions.
struct DesecRecentDamage {
  static constexpr int kSamples = 16;
  long long ms[kSamples] = {};
  float lost[kSamples] = {};  // HP lost since the sample before
  int n = 0, head = 0;
  float lastHp = -1.f;
  long long lastMs = -1;
  void Note(float hp, long long now, long long stepMs = 250) {
    if (lastMs >= 0 && now - lastMs < stepMs) return;
    const float l = lastHp >= 0.f && hp < lastHp ? lastHp - hp : 0.f;
    ms[head] = now;
    lost[head] = l;
    head = (head + 1) % kSamples;
    if (n < kSamples) ++n;
    lastHp = hp;
    lastMs = now;
  }
  float Fraction(float maxHp, long long now, long long windowMs = 3000) const {
    if (!(maxHp > 0.f)) return 0.f;
    float sum = 0.f;
    for (int i = 0; i < n; ++i)
      if (now - ms[i] <= windowMs) sum += lost[i];
    return sum / maxHp;
  }
};

// ---- the rules ---------------------------------------------------------------------------------------------------------

inline float DesecDist(const float* a, const float* b) {
  const float dx = a[0] - b[0], dy = a[1] - b[1];
  return std::sqrt(dx * dx + dy * dy);
}

// E's aim: where the target will be when the hands strike (its position + velocity × lead, the lead at most maxLead),
// pulled back along the velocity so the point stays within range when the target itself is in range. False when the
// target is out of range (the point is then its position).
inline bool LeadPoint(const float* self, const float* t, const float* v, const DesecConfig& c, float* out) {
  out[0] = t[0];
  out[1] = t[1];
  if (DesecDist(self, t) > c.eRange) return false;
  float lx = v[0] * c.eLeadS, ly = v[1] * c.eLeadS;
  const float len = std::sqrt(lx * lx + ly * ly);
  if (len > c.eMaxLead && len > 0.f) {
    lx *= c.eMaxLead / len;
    ly *= c.eMaxLead / len;
  }
  // the largest share k of the lead in [0, 1] with |t + k*l - self| <= range (bisection; the start is in range)
  float k = 1.f;
  const float p1[2] = {t[0] + lx, t[1] + ly};
  if (DesecDist(self, p1) > c.eRange) {
    float lo = 0.f, hi = 1.f;
    for (int i = 0; i < 20; ++i) {
      const float mid = 0.5f * (lo + hi);
      const float pm[2] = {t[0] + lx * mid, t[1] + ly * mid};
      (DesecDist(self, pm) <= c.eRange ? lo : hi) = mid;
    }
    k = lo;
  }
  out[0] = t[0] + lx * k;
  out[1] = t[1] + ly * k;
  return true;
}

// An E aimed at an enemy shaper started: the combo waits for its root.
inline void DesecNoteE(DesecMemory& m, std::uint32_t target, long long now) {
  m.eTarget = target;
  m.eCastMs = now;
}

// A Q started: the combo is done.
inline void DesecNoteQ(DesecMemory& m) {
  m.eTarget = 0;
  m.eCastMs = -1;
}

// Whether the E's hold is on (an E at an enemy shaper within comboHoldMs: every Q waits).
inline bool DesecEPending(const DesecMemory& m, long long now, const DesecConfig& c) {
  return m.eTarget != 0 && m.eCastMs >= 0 && now - m.eCastMs < c.comboHoldMs;
}

// Whether the combo's window is open (an E at an enemy shaper within comboQMs: a Q at its rooted target follows).
inline bool DesecComboOpen(const DesecMemory& m, long long now, const DesecConfig& c) {
  return m.eTarget != 0 && m.eCastMs >= 0 && now - m.eCastMs < c.comboQMs;
}

// Whether every Q of the bot waits (the gambits', BOTREFLEX's): an E is pending and its target isn't rooted yet. A
// rooted target gets the combo's Q from DecideDesecrator; after comboHoldMs the E missed and Q is free again.
inline bool DesecQHeld(const DesecMemory& m, long long now, const DesecConfig& c, bool targetRooted) {
  return DesecEPending(m, now, c) && !targetRooted;
}

// Whether the current R cast still has corpses to throw (1 or 2 thrown, within the window).
inline bool DesecRWindowOpen(const DesecMemory& m, long long now, const DesecConfig& c) {
  return m.rCorpses > 0 && m.rCorpses < 3 && m.rFirstMs >= 0 && now - m.rFirstMs < c.rWindowMs;
}

// A corpse started at `p`.
inline void DesecNoteR(DesecMemory& m, const float* p, long long now, const DesecConfig& c) {
  if (!DesecRWindowOpen(m, now, c)) {
    m.rCorpses = 0;
    m.rFirstMs = now;
  }
  m.rPoints[m.rCorpses][0] = p[0];
  m.rPoints[m.rCorpses][1] = p[1];
  ++m.rCorpses;
}

// The smallest distance from `p` to the current cast's corpses (a large number when none).
inline float DesecNearestCorpse(const DesecMemory& m, const float* p) {
  float best = 1e9f;
  for (int i = 0; i < m.rCorpses && i < 3; ++i) {
    const float d = DesecDist(m.rPoints[i], p);
    if (d < best) best = d;
  }
  return best;
}

// The cluster around enemy `i`: the centre of the visible enemy shapers within rRadius of it (it included) and their
// count, counted again around that centre (all of them within one field).
inline int DesecClusterAt(const std::vector<DesecEnemy>& e, std::size_t i, const DesecConfig& c, float* centre) {
  float sx = 0.f, sy = 0.f;
  int n = 0;
  for (const DesecEnemy& o : e) {
    if (!o.visible || o.hpFrac <= 0.f || DesecDist(o.pos, e[i].pos) > c.rRadius) continue;
    sx += o.pos[0];
    sy += o.pos[1];
    ++n;
  }
  if (n == 0) return 0;
  centre[0] = sx / static_cast<float>(n);
  centre[1] = sy / static_cast<float>(n);
  int in = 0;
  for (const DesecEnemy& o : e)
    if (o.visible && o.hpFrac > 0.f && DesecDist(o.pos, centre) <= c.rRadius) ++in;
  return in;
}

// Enemy shapers within the field of a corpse at `p`.
inline int DesecInField(const std::vector<DesecEnemy>& e, const float* p, const DesecConfig& c) {
  int n = 0;
  for (const DesecEnemy& o : e)
    if (o.visible && o.hpFrac > 0.f && DesecDist(o.pos, p) <= c.rRadius) ++n;
  return n;
}

namespace desec_detail {
inline bool Ready(const DesecMemory& m, int kind, long long now) { return now >= m.nextTryMs[kind]; }
inline bool Usable(const DesecEnemy& o) { return o.visible && o.hpFrac > 0.f; }
}  // namespace desec_detail

// The layer's choice for one think, in order: the combo's Q (any phase), then in a fight R, Drain, Wither, and in a fight
// or the lane the peel. kDesecNone: the plan's gambits run (with the E lead and the Q hold).
inline DesecAction DecideDesecrator(const DesecSelf& s, const std::vector<DesecEnemy>& e,
                                    const std::vector<DesecAlly>& allies, const DesecMemory& m,
                                    const DesecConfig& c = DesecConfig()) {
  using desec_detail::Ready;
  using desec_detail::Usable;
  DesecAction a;
  const long long now = s.nowMs;
  // 1. the combo: the E's target is rooted -> Q at it
  if (s.rankQ > 0 && DesecComboOpen(m, now, c) && Ready(m, kDesecComboQ, now)) {
    for (std::size_t i = 0; i < e.size(); ++i) {
      if (e[i].id != m.eTarget || !Usable(e[i]) || !e[i].rooted || e[i].dist > c.qRange) continue;
      a.kind = kDesecComboQ;
      a.enemy = static_cast<int>(i);
      a.point[0] = e[i].pos[0];
      a.point[1] = e[i].pos[1];
      a.why = "rooted";
      return a;
    }
  }
  if (s.phaseClass == kDesecPhaseOther) return a;
  const bool fight = s.phaseClass == kDesecPhaseFight;
  // 2. R: the 2nd and 3rd corpse at an enemy outside every field and away from the earlier corpses; the 1st at a
  // cluster or at a locked-down enemy
  if (fight && s.rankR > 0 && Ready(m, kDesecR, now)) {
    int best = -1;
    if (DesecRWindowOpen(m, now, c)) {
      for (std::size_t i = 0; i < e.size(); ++i) {
        const DesecEnemy& o = e[i];
        if (!Usable(o) || o.dist > c.rRange || o.mortared || DesecNearestCorpse(m, o.pos) < c.rSpread) continue;
        if (best < 0) {
          best = static_cast<int>(i);
          continue;
        }
        const DesecEnemy& b = e[static_cast<std::size_t>(best)];
        const bool lockedO = o.rooted || o.knockedUp, lockedB = b.rooted || b.knockedUp;
        if ((lockedO && !lockedB) || (lockedO == lockedB && o.hpFrac < b.hpFrac)) best = static_cast<int>(i);
      }
      if (best >= 0) {
        a.kind = kDesecR;
        a.enemy = best;
        a.point[0] = e[static_cast<std::size_t>(best)].pos[0];
        a.point[1] = e[static_cast<std::size_t>(best)].pos[1];
        a.why = "spread";
        a.count = DesecInField(e, a.point, c);
        return a;
      }
    } else {
      int bestCount = 0;
      float centre[2] = {};
      for (std::size_t i = 0; i < e.size(); ++i) {
        if (!Usable(e[i]) || e[i].dist > c.rRange) continue;
        float p[2];
        const int n = DesecClusterAt(e, i, c, p);
        if (n >= c.rClusterMin && n > bestCount && DesecDist(s.pos, p) <= c.rRange) {
          bestCount = n;
          best = static_cast<int>(i);
          centre[0] = p[0];
          centre[1] = p[1];
        }
      }
      if (best >= 0) {
        a.kind = kDesecR;
        a.enemy = best;
        a.point[0] = centre[0];
        a.point[1] = centre[1];
        a.why = "cluster";
        a.count = bestCount;
        return a;
      }
      for (std::size_t i = 0; i < e.size(); ++i) {
        if (!Usable(e[i]) || e[i].dist > c.rRange || !(e[i].rooted || e[i].knockedUp)) continue;
        a.kind = kDesecR;
        a.enemy = static_cast<int>(i);
        a.point[0] = e[i].pos[0];
        a.point[1] = e[i].pos[1];
        a.why = e[i].rooted ? "rooted" : "knocked up";
        a.count = DesecInField(e, a.point, c);
        return a;
      }
    }
  }
  // 3. Drain at the lowest enemy under drainHp within its range
  if (fight && s.hasDrain && Ready(m, kDesecDrain, now)) {
    int best = -1;
    for (std::size_t i = 0; i < e.size(); ++i)
      if (Usable(e[i]) && e[i].dist <= c.drainRange && e[i].hpFrac < c.drainHp &&
          (best < 0 || e[i].hpFrac < e[static_cast<std::size_t>(best)].hpFrac))
        best = static_cast<int>(i);
    if (best >= 0) {
      a.kind = kDesecDrain;
      a.enemy = best;
      a.point[0] = e[static_cast<std::size_t>(best)].pos[0];
      a.point[1] = e[static_cast<std::size_t>(best)].pos[1];
      a.why = "low";
      return a;
    }
  }
  // 4. Wither: a bruiser that just hit an ally first, else the nearest Gladiator
  if (fight && s.hasWither && Ready(m, kDesecWither, now)) {
    int bruiser = -1, glad = -1;
    for (std::size_t i = 0; i < e.size(); ++i) {
      const DesecEnemy& o = e[i];
      if (!Usable(o) || o.dist > c.witherRange) continue;
      if (o.archetype == 1 && o.hitAllyAgeMs >= 0 && o.hitAllyAgeMs <= c.bruiserHitMs &&
          (bruiser < 0 || o.dist < e[static_cast<std::size_t>(bruiser)].dist))
        bruiser = static_cast<int>(i);
      if (o.role == 0 && (glad < 0 || o.dist < e[static_cast<std::size_t>(glad)].dist)) glad = static_cast<int>(i);
    }
    const int w = bruiser >= 0 ? bruiser : glad;
    if (w >= 0) {
      a.kind = kDesecWither;
      a.enemy = w;
      a.point[0] = e[static_cast<std::size_t>(w)].pos[0];
      a.point[1] = e[static_cast<std::size_t>(w)].pos[1];
      a.why = bruiser >= 0 ? "a bruiser on an ally" : "the Gladiator";
      return a;
    }
  }
  // 5. the peel: the hurt ally's attacker gets the E (led), else the Q
  for (const DesecAlly& al : allies) {
    if (al.dist > c.peelAllyRange || al.recentDamage <= c.peelDamage || !al.attacker || al.attackerAgeMs < 0 ||
        al.attackerAgeMs > c.peelHitMs)
      continue;
    for (std::size_t i = 0; i < e.size(); ++i) {
      if (e[i].id != al.attacker || !Usable(e[i])) continue;
      if (s.rankE > 0 && e[i].dist <= c.eRange && !DesecEPending(m, now, c) && Ready(m, kDesecPeelE, now)) {
        a.kind = kDesecPeelE;
        LeadPoint(s.pos, e[i].pos, e[i].vel, c, a.point);
      } else if (s.rankQ > 0 && e[i].dist <= c.qRange && !DesecQHeld(m, now, c, false) && Ready(m, kDesecPeelQ, now)) {
        a.kind = kDesecPeelQ;
        a.point[0] = e[i].pos[0];
        a.point[1] = e[i].pos[1];
      } else {
        continue;
      }
      a.enemy = static_cast<int>(i);
      a.why = "an ally hurt";
      return a;
    }
  }
  return a;
}

}  // namespace gms::agents
