// gms/core/agents (CAMPFINISH): does a Hunter whose HP fell below the Flee threshold finish the jungle camp it is
// fighting, or flee? Engine-free; the host (gms/engine/agents.cpp, BotBrainStep's plan links) samples the fight and
// holds the Farm plan's Flee link while the answer is "stay".
//
// The user's report (2026-10-20): Petrus and Freia "disengage just before the camp is dead, because his hp is too low".
// The rule behind it is the data's: BOT_Artificer_Hunter's Farm plan links to its Flee plan on condition_Bot_HealthLow
// (below 30 % HP), whatever the camp has left, and Flee links back only at full health (condition_FullHealth), so a
// camp at a few hundred HP was left for a walk or a recall home and found healed on the way back.
//
// The relative rule: stay while the bot wins the trade by a margin. From the fight's last windowMs:
//   time to kill = the camp's remaining HP / the damage the camp took per second (its members' HP lost; anyone's hits),
//   time to die  = the bot's HP / the HP the bot lost per second (net: regeneration and potions count),
// and stay when time to die >= margin * time to kill + bufferS, the kill is near (at most maxKillS), the bot is above
// minHpFrac, no enemy shaper is near and the hold is younger than maxHoldMs. Anything unmeasured leaves (the data's
// rule). The creatures that die take their damage with them, so the time to die only grows as the camp shrinks
// [assumed: every threshold].
#pragma once

#include <cstdint>

namespace gms {
namespace agents {

struct CampFinishConfig {
  long long windowMs = 4000;      // the rates over the fight's last this long
  long long minSpanMs = 1500;     // less fight measured than this: leave
  long long maxGapMs = 1500;      // a gap between two samples longer than this starts the measuring again
  float margin = 1.5f;            // stay when time to die >= margin * time to kill + bufferS
  float bufferS = 1.5f;
  float maxKillS = 15.f;          // a camp that needs longer than this is left (round 1: 10 s)
  float minHpFrac = 0.10f;        // below this HP fraction always leave
  long long maxHoldMs = 15000;    // one hold lasts at most this long
  float campRadius = 30.f;        // (host) the camp with a living member nearest the bot within this is its fight
  float enemyShaperRange = 45.f;  // (host) a visible enemy shaper this close: leave
};

// One member of the camp at a sample: its object id and HP (<= 0: dead or gone).
struct CampMemberHp {
  std::uint32_t id = 0;
  float hp = 0.f;
};

// The fight's history for one bot: its HP and the damage its camp took (cumulated), sampled at the bot's thinks.
class CampFightTrack {
 public:
  static constexpr int kSamples = 64;  // 12.8 s at the bots' 200-ms thinks
  static constexpr int kMembers = 8;   // the largest camp has 3; the parasite isn't one

  void Reset() {
    n_ = head_ = members_ = 0;
    camp_ = -1;
    dealt_ = 0.0;
  }

  // A sample at `ms`: the bot's HP and every member of camp `camp` (dead ones at 0). Another camp, a gap of more than
  // maxGapMs or a clock that went back starts the history again. A member that is no longer listed (the director
  // prunes the dead) counts as killed: its last HP is damage taken.
  void Note(long long ms, float hp, int camp, const CampMemberHp* m, int count, const CampFinishConfig& c) {
    if (camp_ != camp || (n_ > 0 && (ms < Last().ms || ms - Last().ms > c.maxGapMs))) {
      Reset();
      camp_ = camp;
      Remember(m, count);
    } else {
      for (int i = 0; i < members_; ++i) {
        const float before = mem_[i].hp > 0.f ? mem_[i].hp : 0.f;
        float now = 0.f;
        for (int j = 0; j < count; ++j)
          if (m[j].id == mem_[i].id) now = m[j].hp > 0.f ? m[j].hp : 0.f;
        if (before > now) dealt_ += before - now;
      }
      Remember(m, count);
    }
    if (n_ > 0 && Last().ms == ms) {  // one sample per clock tick: the newer replaces it
      Last().hp = hp;
      Last().dealt = dealt_;
      return;
    }
    s_[head_] = Sample{ms, hp, dealt_};
    head_ = (head_ + 1) % kSamples;
    if (n_ < kSamples) ++n_;
  }

  // The rates over the last windowMs (the newest sample against the oldest within the window): the camp's HP lost per
  // second, the bot's net HP lost per second (negative: it heals) and the span. False with less than minSpanMs.
  bool Rates(const CampFinishConfig& c, float* dealtPerS, float* takenPerS, long long* spanMs) const {
    if (n_ < 2) return false;
    const Sample& last = s_[(head_ + kSamples - 1) % kSamples];
    const Sample* first = nullptr;
    for (int k = n_ - 1; k >= 1; --k) {  // oldest first
      const Sample& x = s_[(head_ + kSamples - 1 - k) % kSamples];
      if (last.ms - x.ms <= c.windowMs) {
        first = &x;
        break;
      }
    }
    if (!first) return false;
    const long long span = last.ms - first->ms;
    if (span < c.minSpanMs || span <= 0) return false;
    const double sec = static_cast<double>(span) / 1000.0;
    *dealtPerS = static_cast<float>((last.dealt - first->dealt) / sec);
    *takenPerS = static_cast<float>((first->hp - last.hp) / sec);
    *spanMs = span;
    return true;
  }

  int Camp() const { return camp_; }
  double Dealt() const { return dealt_; }  // the camp's HP lost since the history began
  int Count() const { return n_; }
  long long LastMs() const { return n_ > 0 ? s_[(head_ + kSamples - 1) % kSamples].ms : -1; }

 private:
  struct Sample {
    long long ms;
    float hp;
    double dealt;
  };
  Sample& Last() { return s_[(head_ + kSamples - 1) % kSamples]; }
  void Remember(const CampMemberHp* m, int count) {
    members_ = 0;
    for (int j = 0; j < count && members_ < kMembers; ++j) mem_[members_++] = m[j];
  }

  Sample s_[kSamples] = {};
  int n_ = 0, head_ = 0;
  int camp_ = -1;
  double dealt_ = 0.0;
  CampMemberHp mem_[kMembers] = {};
  int members_ = 0;
};

// What the host measured for one bot at the moment its Flee link would open.
struct CampFinishInputs {
  float hp = 0.f, maxHp = 0.f;
  int campAlive = 0;  // the camp's living members (0: no camp fight)
  float campHp = 0.f, campMaxHp = 0.f;
  bool haveRates = false;  // CampFightTrack::Rates answered
  long long spanMs = 0;
  float dealtPerS = 0.f, takenPerS = 0.f;
  bool enemyShaperNear = false;  // a visible enemy shaper within enemyShaperRange
  long long heldMs = 0;          // how long the hold has lasted (0: none yet)
};

enum class CampFinishWhy : int {
  kStayWinning = 0,  // time to die >= margin * time to kill + buffer
  kStayUnhurt,       // the bot loses no HP (net)
  kNoCamp,           // no camp member alive near it
  kEnemyShaper,
  kTooLow,           // below minHpFrac
  kHeldTooLong,      // the hold passed maxHoldMs
  kNotMeasured,      // less than minSpanMs of fight measured
  kNoDamage,         // the camp lost no HP
  kSlowKill,         // time to kill > maxKillS
  kLosing,           // time to die too short
  kCount
};

inline const char* CampFinishWhyName(CampFinishWhy w) {
  switch (w) {
    case CampFinishWhy::kStayWinning: return "wins the trade";
    case CampFinishWhy::kStayUnhurt: return "loses no HP";
    case CampFinishWhy::kNoCamp: return "no camp";
    case CampFinishWhy::kEnemyShaper: return "an enemy shaper near";
    case CampFinishWhy::kTooLow: return "HP too low";
    case CampFinishWhy::kHeldTooLong: return "held too long";
    case CampFinishWhy::kNotMeasured: return "not measured yet";
    case CampFinishWhy::kNoDamage: return "the camp takes no damage";
    case CampFinishWhy::kSlowKill: return "the kill is too far";
    case CampFinishWhy::kLosing: return "loses the trade";
    default: return "?";
  }
}

struct CampFinishDecision {
  bool stay = false;
  CampFinishWhy why = CampFinishWhy::kNoCamp;
  float killS = -1.f;  // < 0: not computed
  float dieS = -1.f;   // < 0: not computed or never (the bot loses no HP)
};

inline CampFinishDecision DecideCampFinish(const CampFinishInputs& in, const CampFinishConfig& c = CampFinishConfig()) {
  CampFinishDecision d;
  if (in.campAlive <= 0 || !(in.campHp > 0.f)) {
    d.why = CampFinishWhy::kNoCamp;
    return d;
  }
  if (in.enemyShaperNear) {
    d.why = CampFinishWhy::kEnemyShaper;
    return d;
  }
  if (!(in.maxHp > 0.f) || !(in.hp > 0.f) || in.hp / in.maxHp < c.minHpFrac) {
    d.why = CampFinishWhy::kTooLow;
    return d;
  }
  if (in.heldMs > c.maxHoldMs) {
    d.why = CampFinishWhy::kHeldTooLong;
    return d;
  }
  if (!in.haveRates) {
    d.why = CampFinishWhy::kNotMeasured;
    return d;
  }
  if (!(in.dealtPerS > 0.f)) {
    d.why = CampFinishWhy::kNoDamage;
    return d;
  }
  d.killS = in.campHp / in.dealtPerS;
  if (d.killS > c.maxKillS) {
    d.why = CampFinishWhy::kSlowKill;
    return d;
  }
  if (!(in.takenPerS > 0.f)) {
    d.stay = true;
    d.why = CampFinishWhy::kStayUnhurt;
    return d;
  }
  d.dieS = in.hp / in.takenPerS;
  d.stay = d.dieS >= c.margin * d.killS + c.bufferS;
  d.why = d.stay ? CampFinishWhy::kStayWinning : CampFinishWhy::kLosing;
  return d;
}

// ---- round 3: the Hunter stays on a nearly dead camp ---------------------------------------------------------------
// The second live round measured the other way a Hunter leaves a camp: healthy, the camp at 1-23 % with one creature
// left, he took a task of the Lua's Hunter logic (a gank, the parasite's gather, a push) or his Farm plan's
// MoveToNextCamp walked him to another camp. While "finishing", the Lua starts no task but the base's defence, the
// parasite's kill and a human's ping, and the camp conditions answer the camp's last creature as his camp [assumed].
struct CampStayConfig {
  float finishFrac = 0.35f;   // the camp at or under this share of its full HP ...
  float finishKillS = 6.f;    // ... or killed within this (the measured rate)
  float minHpFrac = 0.35f;    // and the Hunter at least this healthy (below it the Flee rule above decides)
  long long maxMs = 12000;    // for at most this long per fight
};

// Whether the Hunter finishes the camp first. sinceMs: how long he has been finishing it (0 at the start).
inline bool CampFinishingWanted(int campAlive, float campFrac, bool haveRates, float dealtPerS, float campHp,
                                float hpFrac, bool enemyShaperNear, long long sinceMs,
                                const CampStayConfig& c = CampStayConfig()) {
  if (campAlive <= 0 || enemyShaperNear || hpFrac < c.minHpFrac || sinceMs > c.maxMs) return false;
  if (campFrac <= c.finishFrac) return true;
  return haveRates && dealtPerS > 0.f && campHp / dealtPerS <= c.finishKillS;
}

}  // namespace agents
}  // namespace gms
