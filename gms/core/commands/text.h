// RULES0b: a text line for one player in a match (gms/engine/commands.cpp cmds::SendPlayerText, and the commands
// file's test aid `<s> text <def name>`). Engine-free and header-only, so gms_tests checks it without the game.
//
// The host shows a player a line in the HUD's alert line by sending a ClientFeedbackEvent whose ClientFeedbackDef has
// a text key (the match rules' two defs come from the setup's zz_BotProfiles.snap). The send takes team bits and a
// mask of players left out (PlayerMaskForTeams gives the teams' players and their spectators); for one player the
// bits are that player's team and every other gameplay index is left out, the team's spectators included.
#pragma once

#include <string>

namespace gms::cmdtext {

constexpr unsigned kMaxIndex = 32;  // the player mask's bits
constexpr const char* kDefClass = ".ClientFeedbackDef";

// The send's arguments for a line to the player with gameplay index `index` on team `team`.
struct TextMask {
  unsigned teamBits = 0;        // 1 << team
  unsigned excludePlayers = 0;  // every gameplay index but `index`
};

// False (and nothing set) for an index or team outside the mask's 32 bits.
inline bool PlayerTextMask(unsigned index, unsigned team, TextMask* out) {
  if (index >= kMaxIndex || team >= kMaxIndex) return false;
  out->teamBits = 1u << team;
  out->excludePlayers = ~(1u << index);
  return true;
}

// The def a commands-file line names after "text ": the first word, with ".ClientFeedbackDef" added when it has no
// class ("Rules_ShopClosed" and "Rules_ShopClosed.ClientFeedbackDef" are the same def); "" when there is none.
inline std::string TextDefName(const std::string& rest) {
  const size_t first = rest.find_first_not_of(" \t\r");
  if (first == std::string::npos) return "";
  const size_t end = rest.find_first_of(" \t\r", first);
  std::string name = rest.substr(first, end == std::string::npos ? std::string::npos : end - first);
  if (name.find('.') == std::string::npos) name += kDefClass;
  return name;
}

}  // namespace gms::cmdtext
