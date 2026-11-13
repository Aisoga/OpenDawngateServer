// RULES3, RULES5b: a custom game's shaper pick rule on the match host (startGame.rules.pick, the server's catalog in
// server/internal/rules). Engine-free and header-only, so gms_tests checks it without the game.
//
//   pick: "blind" (the standard game) | "random" (All Random: each human's list is the shaper dgserver rolled) |
//         "draft" (the lobby's draft: each human's list is the shaper it drafted) |
//         "army" (Army Pick: each team votes in the lobby, one vote drawn per team; every human's list is its team's
//         drawn shaper) | "clone" (Clone Pick: one vote drawn over everyone's; every human's list is that shaper).
// For "random", "draft", "army" and "clone" the human's list holds one shaper, which the host locks at once once its
// client's shaper select UI is up (ShaperSelectPlayer::lockAtOnce), so shaper select shows no pick turn of its own. A
// host without this rule for "draft" (an older build) still offers only the drafted shaper, and the phase-1 timer takes
// it; "army" and "clone" go only to a host that names the feature "rules2" (an older one has no team duplicates).
#pragma once

#include <string>

namespace gms::rules {

// PickLocksAtOnce reports whether startGame's pick rule gives every human one shaper the host locks at once.
inline bool PickLocksAtOnce(const std::string& pick) {
  return pick == "random" || pick == "draft" || pick == "army" || pick == "clone";
}

// PickAllowsTeamDuplicates reports whether startGame's pick rule may give one shaper to several players of a team
// (ShaperSelectConfig::teamDuplicates; MODES0): Army Pick (a team's drawn shaper) and Clone Pick (everyone's).
inline bool PickAllowsTeamDuplicates(const std::string& pick) { return pick == "army" || pick == "clone"; }

// PickName is the rule's name for the host's log.
inline const char* PickName(const std::string& pick) {
  return pick == "random"  ? "All Random"
         : pick == "draft" ? "Draft"
         : pick == "army"  ? "Army Pick"
         : pick == "clone" ? "Clone Pick"
                           : "the standard pick";
}

}  // namespace gms::rules
