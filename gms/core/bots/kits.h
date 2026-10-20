// gms/core/bots (HUNTERBOT, DESECBOT): what a borrowed AI may cast of a shaper's kit, without the engine. Header-only.
//
// The Hunter AI (BOT_Artificer_Hunter, Petrus's) names ability slots in its gambits, never abilities [data:
// Phase.json, Artificer_Hunter_*: ClearCamp spell1, consume1, W, Q, basic at the camp; SpinToWin spell1, consume1, E;
// GankThings spell2, W, Q, basic at a shaper], so a bot of any shaper on it casts its own shaper's abilities. What does
// not fit another kit is said here, per shaper (the abilities' scripts and data) [assumed fits]:
//   Petrus (artificer.Noun)  Q Demi (target 5), W DelaySwing (point jump 13.24), E Spin (self, 8.09 around, the
//                            Farm plan's SpinToWin phase is built for it), R HyperPunch (an enemy shaper within 8.08)
//   Faris (golem.Noun)       Q BasicSwitch is a stance toggle (self, cooldown 1.5): the AI's "Q at the camp" would flip
//                            his stance every 1.5 s, so the host never casts it for him and he keeps his first, ranged
//                            stance (basic 16.18, W Ranged_Sentinel cone, E RecallDash point 16.92, R Barrage 17.65)
//   Freia (survivor.Noun)    Q Bleed (target 5), W Dash (target 17.65), E SlowStun (target 5), R Execute (around her,
//                            10, more damage the more HP the enemy misses)
// The E: Petrus's spin holds SpinToWin while it lasts (the phase's only other gambits are the spell and the potion).
// Faris's and Freia's E needs a target or a point, which SpinToWin's E gambit lacks, so they'd stand there; the Lua's
// condition_Bot_IsEActive and condition_Bot_CanUseE answer false for them (no SpinToWin: dgsim_bots_api.lua,
// B.hunterKits), and the host casts their E at the target in ClearCamp and GankThings (castsE).
// The Q's ranks are Faris's basic attack's power (ability_Golem_Basic adds Q_BasicSwitch's magic damage, a part of the
// target's HP and attack speed by its rank), so his skill order maxes Q though he never casts it.
// The R: no Hunter phase casts it; the host's finisher (agents.cpp HunterFinisherStep) casts it at a visible enemy
// shaper within the kit's reach that is low on HP.
//
// DESECBOT: the Tactician AI (BOT_Guardian_Tactician) on Desecrator's kit (desecrator.noun): Q Graves (a line at a
// point, 28.98 long), W FrozenAura (a burst slow of radius 13.24 around himself, no damage), E GrabHands (a circle at a
// point, range 22.36, the root 1 s after the cast), R Mortar (three corpses at points, range 50). The Guardian's W is a
// shield, and its gambits fire it at enemies up to 40 away or at the base with nobody near; the Lua's
// condition_Guardian_ShieldActive answers "active" for a Desecrator without an enemy shaper within 12 (no W), and the
// host requests his W only with a living enemy shaper within wNear (12) as well (KitAllowsW), which also covers the
// gambit that casts it at the base and BOTREFLEX's attack.
//
// MIKELLABOT: the High Huntress's Gladiator AI (BOT_HighHuntress_Gladiator) on Mikella's kit (merchant_princess.noun):
// Q Return (a line of 24.27 at a point, out and back), W TriShot (self: her next three basics deal more), E Spin (a
// charge of 11.77 toward a point, shooting the enemies within 16), R Barrage (an enemy shaper within 55.89, a 1-s
// channel). The donor's E gambits aim at the nearest enemy shaper (a charge into it) and its R at anyone in a line, so
// her E and R are her kit layer's (gms/core/agents/mikella.h), and her W goes only with an enemy shaper within reach.
// MOYABOT: Moya (otter.noun) on the Hunter AI. Q Dash (a dash of 11.85 toward a point, 80-60 energy, cooldown
// 5.5-3.5; it consumes her W's buff for a stun and her E's for a heal), W Stun and E Lifesteal (self casts, instant:
// each resets her basic attack and puts a 3-s buff on her, W's the next hits' slow and the Q's stun, E's a heal per
// hit), R Izuna (a dash of 19.12 that grabs the first enemy shaper in the line and drops him 2-3.5 s later). The AI
// fits her mostly: ClearCamp and GankThings cast W then Q (her stun combo), SpinToWin's "E unless active" with two
// creatures within 8 is her E at the camp (the E ends at once, so condition_Bot_IsEActive holds nothing, eSpin). What
// doesn't fit: the W's buff lasts 3 s, and the plan's W gambits fire at a target up to 40 away (GankThings), where the
// buff runs out before she arrives: the host requests her W only with the target within wReach (10, inside the Q's
// dash). BOTREFLEX's attack (Q, W, E in slot order) would dash first and waste the stun, and the guide never uses E in
// a skirmish: for her it casts W, then Q, then the basic (wThenQ).
// Round 2 (the first live match): SpinToWin's E came only with two creatures within 8, 3-9 times in her first 5
// minutes, and her camps took her to Flee at 25-30 % HP (her E's heal per hit is her jungle sustain, the guide's clear
// "auto, E for the reset, autos"). So the host also casts her E at the camp creature she fights in ClearCamp, never in
// GankThings, and only between two basic attacks (the tree's IdleDefault leaf), so its basic reset follows a hit
// instead of cancelling one (eCamp; castsE within 5).
#pragma once

#include <string>
#include <vector>

#include "core/agents/brain.h"  // kSlot*

namespace gms::bots {

enum KitTarget : int { kKitTargetNone = 0, kKitTargetEnemy = 1, kKitTargetAround = 2 };

struct ShaperKit {
  const char* noun = "";              // the shaper's noun asset name, lower case ("golem.noun")
  bool castsQ = true;                 // false: the host never requests the Q slot for it
  bool eSpin = false;                 // the Hunter AI's SpinToWin phase fits its E
  int rTarget = kKitTargetNone;       // how its R is aimed
  float rReach = 0.f;                 // the R's reach (range, or radius around it)
  float rTargetHp = 0.45f;            // the R only on an enemy shaper at or below this HP fraction [assumed]
  // the E outside SpinToWin (agents.cpp HunterKitEStep): at the bot's target in ClearCamp and GankThings, within
  // eReach; after a cast that started the next try waits eWaitMs (Faris: past the dash, so a repeat isn't at once
  // his recall; Freia: her empowered second activation may follow)
  bool castsE = false;
  float eReach = 0.f;
  int eWaitMs = 0;
  // DESECBOT: the W (a burst around the shaper) only with a living enemy shaper within this distance; 0 = any time
  float wNear = 0.f;
  // DESECBOT-3: the kit layer (gms/core/agents/desecrator.h, agents.cpp DesecratorKitStep): E led and before Q, the Q
  // held for the E's root, R and Wither cast by the layer only (the plan's R and Wither gambits left out)
  bool desecLayer = false;
  // MIKELLABOT: her kit layer (gms/core/agents/mikella.h, agents.cpp MikellaKitStep): the plan's E and R left out (the
  // layer's escape, kite and chase E and its R), her W only with an enemy shaper near, the trade W
  bool mikLayer = false;
  // MOYABOT: the W (a self cast that empowers the next hits for 3 s) only with the gambit's target within this
  // distance; 0 = any. And BOTREFLEX's attack W, Q, basic (no E) instead of Q, W, E, basic.
  float wReach = 0.f;
  bool wThenQ = false;
  // MOYABOT round 2: the kit E (castsE) only in ClearCamp, and only between two basic attacks
  bool eCamp = false;
};

inline const std::vector<ShaperKit>& Kits() {
  static const std::vector<ShaperKit> k = {
      {"artificer.noun", true, true, kKitTargetEnemy, 8.08f, 0.45f, false, 0.f, 0},
      {"golem.noun", false, false, kKitTargetEnemy, 17.65f, 0.45f, true, 16.f, 6000},       // RecallDash 16.92
      {"survivor.noun", true, false, kKitTargetAround, 9.0f, 0.40f, true, 5.f, 1000},  // Execute: 10 around; SlowStun 5
      {"desecrator.noun", true, false, kKitTargetNone, 0.f, 0.45f, false, 0.f, 0, 12.f, true},  // FrozenAura: 13.24
      {"merchant_princess.noun", true, false, kKitTargetNone, 0.f, 0.45f, false, 0.f, 0, 0.f, false, true},  // MIKELLABOT
      // MOYABOT: the E is SpinToWin's; R Izuna's line is 19.12 (the finisher's reach a little less) [assumed]
      {"otter.noun", true, true, kKitTargetEnemy, 17.f, 0.45f, true, 5.f, 1000, 0.f, false, false, 10.f, true, true},
  };
  return k;
}

// HunterKitEStep's test: the kit casts its E outside SpinToWin, the E is ranked, the target lives, is hostile and
// within reach.
inline bool KitEWanted(const ShaperKit* kit, int eRank, bool targetHostileAlive, float targetDist) {
  return kit && kit->castsE && eRank > 0 && targetHostileAlive && targetDist <= kit->eReach;
}

// HunterKitEStep's phase test: ClearCamp or GankThings; a kit with eCamp only in ClearCamp and between two basic
// attacks (`betweenHits`: the agent on its IdleDefault leaf).
inline bool KitEPhaseAllows(const ShaperKit* kit, bool clearCamp, bool gankThings, bool betweenHits) {
  if (!kit) return false;
  if (kit->eCamp) return clearCamp && betweenHits;
  return clearCamp || gankThings;
}

// The kit of a shaper by its noun's asset name (any case, ".Noun" optional); nullptr = none (the AI casts as it says).
inline const ShaperKit* KitForNoun(const std::string& noun) {
  std::string n;
  for (char c : noun) n += static_cast<char>(c >= 'A' && c <= 'Z' ? c - 'A' + 'a' : c);
  if (n.find('.') == std::string::npos) n += ".noun";
  for (const ShaperKit& k : Kits())
    if (n == k.noun) return &k;
  return nullptr;
}

// Whether the host may request `slot` for a bot of `kit` (a plan gambit, BOTREFLEX's attack, WELLDEF's fight).
inline bool KitAllowsSlot(const ShaperKit* kit, int slot) {
  if (!kit) return true;
  if (slot == gms::agents::kSlotQ && !kit->castsQ) return false;
  return true;
}

// DESECBOT: whether the host may request the W slot now, `nearestEnemy` the distance to the nearest living enemy shaper
// (negative: none). Other slots and kits without wNear: always.
inline bool KitAllowsW(const ShaperKit* kit, int slot, float nearestEnemy) {
  if (!kit || slot != gms::agents::kSlotQ + 1 || kit->wNear <= 0.f) return true;
  return nearestEnemy >= 0.f && nearestEnemy <= kit->wNear;
}

// MOYABOT: whether the host may request the W slot at a target `targetDist` away (negative: no target). Other slots and
// kits without wReach: always.
inline bool KitAllowsWAt(const ShaperKit* kit, int slot, float targetDist) {
  if (!kit || slot != gms::agents::kSlotQ + 1 || kit->wReach <= 0.f) return true;
  return targetDist >= 0.f && targetDist <= kit->wReach;
}

// MOYABOT: the slots BOTREFLEX's attack tries, in order (`desec`: the Desecrator's kit layer is on).
inline std::vector<int> ReflexSlots(const ShaperKit* kit, bool desec) {
  using namespace gms::agents;
  if (desec) return {kSlotQ + 2, kSlotQ, kSlotQ + 1, kSlotBasic};  // E first (led), then Q, W
  if (kit && kit->wThenQ) return {kSlotQ + 1, kSlotQ, kSlotBasic};
  return {kSlotQ, kSlotQ + 1, kSlotQ + 2, kSlotBasic};
}

// One enemy shaper the finisher may weigh: its distance, HP fraction, whether the bot sees it.
struct FinisherCandidate {
  float dist = 0.f;
  float hpFrac = 1.f;
  bool visible = false;
};

// HunterFinisherStep's choice: the index of the candidate the R goes to (the lowest HP fraction within reach, at or
// below the kit's threshold), -1 none. `rRank` 0 = R not learned.
inline int FinisherTarget(const ShaperKit* kit, int rRank, const std::vector<FinisherCandidate>& c) {
  if (!kit || rRank <= 0 || kit->rTarget == kKitTargetNone) return -1;
  int best = -1;
  for (size_t i = 0; i < c.size(); ++i) {
    if (!c[i].visible || c[i].dist > kit->rReach || c[i].hpFrac > kit->rTargetHp || c[i].hpFrac <= 0.f) continue;
    if (best < 0 || c[i].hpFrac < c[static_cast<size_t>(best)].hpFrac) best = static_cast<int>(i);
  }
  return best;
}

}  // namespace gms::bots
