// RULES3, RULES5b: a custom game's shaper pick rule on the match host (startGame.rules.pick, the server's catalog in
// server/internal/rules). Engine-free and header-only, so gms_tests checks it without the game.
//
//   pick: "blind" (the standard game) | "random" (All Random: each human's list is the shaper dgserver rolled) |
//         "draft" (the lobby's draft: each human's list is the shaper it drafted).
// For "random" and "draft" the human's list holds one shaper, which the host locks at once once its client's shaper
// select UI is up (ShaperSelectPlayer::lockAtOnce), so shaper select shows no pick turn of its own. A host without this
// rule for "draft" (an older build) still offers only the drafted shaper, and the phase-1 timer takes it.
#pragma once

#include <string>

namespace gms::rules {

// PickLocksAtOnce reports whether startGame's pick rule gives every human one shaper the host locks at once.
inline bool PickLocksAtOnce(const std::string& pick) { return pick == "random" || pick == "draft"; }

// PickName is the rule's name for the host's log.
inline const char* PickName(const std::string& pick) {
  return pick == "random" ? "All Random" : pick == "draft" ? "Draft" : "the standard pick";
}

}  // namespace gms::rules
