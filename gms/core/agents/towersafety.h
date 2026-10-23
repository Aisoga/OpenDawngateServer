// gms/core/agents (BOTTOWER): when an enemy tower is a danger to a bot, which targets it leaves alone because of that
// tower, how a retreat step deals with a walk that holds the bot already (REFLEXWALK), and the measurement of the bots'
// time under tower fire. Engine-free; the host (gms/engine/agents.cpp: BotReflexStep, the gambit list, the frame
// snapshot) measures the sensors and carries the decisions out.
//
// The user's report (2026-10-23): "they seem to have gotten a bit dumber when we added their behaviour. Mostly in terms
// of running into tower shots and not turning back". BOTREFLEX's tower rule (reflex.h rule 1) backed off only after a
// tower's hit was seen (OnDamaged) and only from that tower's 34 units; nothing kept the plan from walking straight back
// in, and the step itself did nothing while another walk of the same ability held the bot (the request answers -0x270B,
// which counted as done).
//
// The rules [assumed thresholds, the usual MOBA rules]:
//   - danger: a tower that hit the bot within hitZoneMs while the bot is within its reach (BOTREFLEX's rule; the reach is
//     the larger of hitReach and the tower's aggro range plus hitMargin, so the Locus counts too); with `aware` also a
//     tower that targets the bot (its mTarget) within its aggro range plus targetedMargin, and a tower with none of the
//     bot's lane minions within its aggro range (nothing else to shoot) while the bot is within aggro plus exposedMargin.
//   - keep-out: a target (a gambit's, a reflex fight's, the idle fallback's) within an enemy tower's aggro range plus
//     keepOutMargin is left alone while that tower is unsafe: it has none of the bot's minions in range, or it hit or
//     targeted the bot within keepOutMs. Exempt: a dive or a tower finish (reflex.h ReflexDiving's rules).
//   - REFLEXWALK: a retreat step whose request answers "an ability of the same kind holds the agent" ends that holder
//     unless it is the bot's own step (adopted as WELLDEF's well walk does), so the step starts at the next think.
#pragma once

#include <cstdint>

namespace gms {
namespace agents {

struct TowerSafetyConfig {
  float hitReach = 34.f;        // BOTREFLEX's towerRange
  float hitMargin = 6.f;        // ... or the tower's aggro range plus this, when larger (with `aware`)
  long long hitZoneMs = 8000;   // BOTREFLEX's towerZoneMs
  float targetedMargin = 2.f;   // a tower targeting the bot is a danger within its aggro range plus this (round 1 had
                                // 6: a tower keeps a bot on its list beyond its reach, one bot hovered 95 s at 29-34)
  float exposedMargin = 2.f;    // a tower with none of the bot's minions in range is a danger within aggro plus this
  long long keepOutMs = 8000;   // a tower that hit or targeted the bot stays unsafe this long
  float keepOutMargin = 0.f;    // a target within aggro plus this of an unsafe tower is left alone
  float finishTowerShotHp = 0.2f;  // a tower that hits or targets the bot is finished only at or below this HP fraction
  bool aware = true;            // [host] bot_tower_aware: the targeted and exposed dangers (false: BOTREFLEX's hit only)
  bool keepOut = true;          // [host] bot_tower_keepout
};

// One standing enemy tower as one bot sees it.
struct TowerView {
  float dist = 0.f;              // bot to tower (2-D)
  float aggro = 28.f;            // the tower's aggro range (a Binding 28, the Locus 38.5)
  bool targetsMe = false;        // the tower's mTarget is the bot
  long long hitAgeMs = -1;       // since this tower last hit the bot (-1: never, or another tower hit it last)
  long long targetedAgeMs = -1;  // since this tower last targeted the bot within range (-1: never)
  int minionsIn = 0;             // the bot's team's lane minions within the tower's aggro range
};

enum TowerDangerKind : int { kTowerSafe = 0, kTowerHit = 1, kTowerTargets = 2, kTowerExposed = 3 };

inline const char* TowerDangerName(int k) {
  return k == kTowerHit ? "tower" : k == kTowerTargets ? "tower targets it" : k == kTowerExposed ? "tower, no minions" : "";
}

inline float TowerHitReach(const TowerView& t, const TowerSafetyConfig& c) {
  if (!c.aware) return c.hitReach;
  const float r = t.aggro + c.hitMargin;
  return r > c.hitReach ? r : c.hitReach;
}

inline TowerDangerKind TowerDanger(const TowerView& t, const TowerSafetyConfig& c) {
  if (t.hitAgeMs >= 0 && t.hitAgeMs <= c.hitZoneMs && t.dist <= TowerHitReach(t, c)) return kTowerHit;
  if (!c.aware || !(t.aggro > 0.f)) return kTowerSafe;
  if (t.targetsMe && t.dist <= t.aggro + c.targetedMargin) return kTowerTargets;
  if (t.minionsIn == 0 && t.dist <= t.aggro + c.exposedMargin) return kTowerExposed;
  return kTowerSafe;
}

// The most urgent danger of several towers (a hit before a targeting before an exposure, then the nearer); -1 = none.
inline int WorstTowerDanger(const TowerView* v, int n, const TowerSafetyConfig& c, TowerDangerKind* kind) {
  int best = -1;
  TowerDangerKind bk = kTowerSafe;
  for (int i = 0; i < n; ++i) {
    const TowerDangerKind k = TowerDanger(v[i], c);
    if (k == kTowerSafe) continue;
    if (best < 0 || k < bk || (k == bk && v[i].dist < v[best].dist)) {
      best = i;
      bk = k;
    }
  }
  if (kind) *kind = bk;
  return best;
}

// The tower would shoot the bot near it: nothing else of its team's enemies to shoot, or it shot or targeted the bot lately.
inline bool TowerUnsafe(const TowerView& t, const TowerSafetyConfig& c) {
  if (!(t.aggro > 0.f)) return false;
  if (t.minionsIn == 0 || t.targetsMe) return true;
  return (t.hitAgeMs >= 0 && t.hitAgeMs <= c.keepOutMs) || (t.targetedAgeMs >= 0 && t.targetedAgeMs <= c.keepOutMs);
}

// What a target is, for the exemptions (reflex.h ReflexDiving's rules).
struct KeepOutTarget {
  float distToTower = 0.f;     // the target's (or the point's) 2-D distance from the tower
  bool isThatTower = false;    // the target is the tower itself
  float towerHpFrac = 1.f;     // ... its HP fraction
  bool isShaper = false;       // an enemy shaper
  float shaperHpFrac = 1.f;
  bool finishing = false;      // KILLFINISH finishes a won trade on it
};

struct KeepOutRules {
  float diveTargetHp = 0.35f, diveSelfHp = 0.4f, finishTowerHp = 0.5f, finishSelfHp = 0.5f;
  float finishTowerShotHp = 0.2f;  // ... while that tower hits or targets the bot
  bool noShaperDive = false;
};

// True: the bot leaves the target alone because of this tower.
inline bool TowerKeepsOut(const TowerView& t, const KeepOutTarget& g, float selfHpFrac, const TowerSafetyConfig& c,
                          const KeepOutRules& r = KeepOutRules()) {
  if (!c.keepOut || g.distToTower > t.aggro + c.keepOutMargin || !TowerUnsafe(t, c)) return false;
  const bool shooting = t.targetsMe || (t.hitAgeMs >= 0 && t.hitAgeMs <= c.hitZoneMs);
  const float finish = shooting ? r.finishTowerShotHp : r.finishTowerHp;
  if (g.isThatTower && g.towerHpFrac <= finish && selfHpFrac >= r.finishSelfHp) return false;  // its finish
  if (g.isShaper && !r.noShaperDive &&
      (g.finishing || (g.shaperHpFrac <= r.diveTargetHp && selfHpFrac >= r.diveSelfHp)))
    return false;  // a dive
  return true;
}

// REFLEXWALK: what a retreat step does with its request's result. `res` the request's result (1, 2 started; -0x270B an
// ability of the same kind holds the agent); `holderKnown`/`holder` the instance that holds it; `own` the step the bot
// adopted (0 none); `sinceAskedMs` since the bot's own last started step (-1 none).
enum HeldWalkAction : int {
  kHeldWalkStarted = 0,  // the step started
  kHeldWalkOther = 1,    // another result (refused, out of range, ...): the caller's
  kHeldWalkOwn = 2,      // its own step goes on
  kHeldWalkAdopt = 3,    // the holder is the step it asked for just now: adopt it
  kHeldWalkEnd = 4,      // another walk holds the bot: end it (the step starts at the next think)
};

constexpr int kHeldWalkResultHolds = -0x270B;  // brain.h kResultHoldsAgent

inline HeldWalkAction ChooseHeldWalk(int res, bool holderKnown, unsigned holder, unsigned own, long long sinceAskedMs,
                                     long long adoptWindowMs = 1500) {
  if (res == 1 || res == 2) return kHeldWalkStarted;
  if (res != kHeldWalkResultHolds) return kHeldWalkOther;
  if (!holderKnown || (own && holder == own)) return kHeldWalkOwn;
  if (!own && sinceAskedMs >= 0 && sinceAskedMs <= adoptWindowMs) return kHeldWalkAdopt;
  return kHeldWalkEnd;
}

// The measurement: what a bot's brain did at a think while an enemy tower targeted it (the host's paths).
enum TowerThinkPath : int {
  kTtBeforeReflex = 0,  // the think ended before BOTREFLEX (a cast holds it, the plan's links, a finisher, ...)
  kTtBackStarted,       // a back-off step started
  kTtBackOwn,           // its own step goes on
  kTtBackEnded,         // REFLEXWALK: another walk ended for the step
  kTtBackBlind,         // the step was answered "held" by another walk and counted as done (REFLEXWALK off)
  kTtBackRefused,       // the step was refused otherwise
  kTtDive,              // a dive or a tower finish: it stays
  kTtNotSeen,           // BOTREFLEX saw no tower danger (no hit recorded, or out of its reach)
  kTtOtherReflex,       // another reflex acted (a fight, a well)
  kTtPlanRetreat,       // the plan's own retreat (ability_Bot_Back, a recall) holds the bot: left alone
  kTtPathCount
};

inline const char* TowerThinkPathName(int p) {
  switch (p) {
    case kTtBeforeReflex: return "before the reflex";
    case kTtBackStarted: return "step started";
    case kTtBackOwn: return "own step";
    case kTtBackEnded: return "walk ended for it";
    case kTtBackBlind: return "held by a walk";
    case kTtBackRefused: return "step refused";
    case kTtDive: return "dive";
    case kTtNotSeen: return "not seen";
    case kTtOtherReflex: return "other reflex";
    case kTtPlanRetreat: return "plan's retreat";
    default: return "?";
  }
}

// One stretch of time an enemy tower targeted one bot within range.
struct TowerEpisode {
  std::uint32_t tower = 0;
  long long startMs = 0, lastMs = 0;  // the first and the last frame it was targeted
  long long targetedMs = 0;           // the frames' time while targeted
  int shots = 0;                      // the tower's hits on it (OnDamaged)
  bool lethal = false;                // ... one of them killed it
  float hpStart = 1.f, hpEnd = 1.f;   // HP fractions
  float distStart = 0.f, distEnd = 0.f;
  bool died = false;
  int thinks[kTtPathCount] = {};
  long long DurationMs() const { return lastMs - startMs; }
};

// Per bot: frames in, episodes out. An episode ends when no tower targeted the bot for gapMs, when another tower takes
// over, or when the bot dies.
class TowerExposureTrack {
 public:
  bool open() const { return open_; }
  const TowerEpisode& current() const { return ep_; }

  // One frame. `tower` targets the bot within range (0: none); `lastFrameMs` the previous frame's time. Returns true and
  // fills *out when an episode ended (a new one may have begun in the same frame).
  bool Frame(long long nowMs, long long lastFrameMs, std::uint32_t tower, float hpFrac, float dist, bool dead,
             long long gapMs, TowerEpisode* out) {
    bool ended = false;
    if (open_ && (dead || (tower && tower != ep_.tower) || (!tower && nowMs - ep_.lastMs > gapMs))) {
      if (dead) {
        ep_.died = true;
        ep_.hpEnd = 0.f;
      }
      *out = ep_;
      open_ = false;
      ended = true;
    }
    if (dead || !tower) return ended;
    if (!open_) {
      ep_ = TowerEpisode();
      ep_.tower = tower;
      ep_.startMs = ep_.lastMs = nowMs;
      ep_.hpStart = hpFrac;
      ep_.distStart = dist;
      open_ = true;
    } else {
      const long long dt = nowMs - lastFrameMs;
      if (dt > 0 && dt < 1000) ep_.targetedMs += dt;
      ep_.lastMs = nowMs;
    }
    ep_.hpEnd = hpFrac;
    ep_.distEnd = dist;
    return ended;
  }

  // A tower's hit on the bot: counted into the open episode of that tower (false: none open for it).
  bool Shot(std::uint32_t tower, bool lethal) {
    if (!open_ || tower != ep_.tower) return false;
    ++ep_.shots;
    if (lethal) ep_.lethal = true;
    return true;
  }

  void Think(int path) {
    if (open_ && path >= 0 && path < kTtPathCount) ++ep_.thinks[path];
  }

 private:
  bool open_ = false;
  TowerEpisode ep_;
};

// The match's totals over the ended episodes.
struct TowerExposureTotals {
  unsigned episodes = 0, shots = 0, lethal = 0, died = 0, over3s = 0, lowEntries = 0, lowDeaths = 0;
  unsigned shots2 = 0, shots3 = 0;  // episodes with at least 2, 3 tower shots
  long long targetedMs = 0, durationMs = 0, maxDurationMs = 0;
  double hpLost = 0.0;  // the HP fractions lost during the episodes
  unsigned thinks[kTtPathCount] = {};

  void Add(const TowerEpisode& e, float lowHp = 0.3f) {
    ++episodes;
    shots += static_cast<unsigned>(e.shots);
    if (e.shots >= 2) ++shots2;
    if (e.shots >= 3) ++shots3;
    if (e.lethal) ++lethal;
    if (e.died) ++died;
    const long long d = e.DurationMs();
    durationMs += d;
    targetedMs += e.targetedMs;
    if (d > maxDurationMs) maxDurationMs = d;
    if (d > 3000) ++over3s;
    if (e.hpStart < lowHp) {
      ++lowEntries;
      if (e.died) ++lowDeaths;
    }
    if (e.hpStart > e.hpEnd) hpLost += static_cast<double>(e.hpStart - e.hpEnd);
    for (int i = 0; i < kTtPathCount; ++i) thinks[i] += static_cast<unsigned>(e.thinks[i]);
  }
};

}  // namespace agents
}  // namespace gms
