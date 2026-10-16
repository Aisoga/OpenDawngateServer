// gms/core/bots (BOTPROFILE): the project's own bot profiles, BotClass assets the setup adds to every client and host
// copy (re/scripts/bot_profiles.py, server/internal/setup/botprofiles: game\Data\zz_BotProfiles.snap). Header-only.
//
// A profile copies a shipped BotClass (noun, AI definition, skill and spell order) and changes only its role, so a bot
// whose seat wants another role than its class's own shows that role on every client: the client's
// LabsPlayer_ReflectBotData sets the bot's mRole from the BotClass each time a player update carries mpBotData. The
// game ships no BotClass with the Predator role; PC_Warden_Predator is the Warden's Tactician profile as a Predator.
//
// The table must match bot_profiles.py's PROFILES (name, base, role); the host uses a profile only when its own copy's
// catalog lists it (a copy set up before the profiles existed falls back to the base class with the role set after it).
#pragma once

#include <string>
#include <vector>

namespace gms::bots {

struct Profile {
  const char* name;  // the BotClass asset's name without ".BotClass"
  const char* base;  // the shipped BotClass it copies
  int role;          // RolesTuning.role[] index: 0 Gladiator, 1 Tactician, 2 Hunter, 3 Predator
};

inline const std::vector<Profile>& Profiles() {
  static const std::vector<Profile> kProfiles = {{"PC_Warden_Predator", "PC_Warden", 3}};
  return kProfiles;
}

// The shipped class a profile copies; any other name is its own base.
inline std::string BaseClass(const std::string& botClass) {
  for (const Profile& p : Profiles())
    if (botClass == p.name) return p.base;
  return botClass;
}

// The profile of `base` with `role`, or "" when there is none.
inline std::string ProfileFor(const std::string& base, int role) {
  for (const Profile& p : Profiles())
    if (base == p.base && role == p.role) return p.name;
  return "";
}

inline bool IsProfile(const std::string& botClass) { return BaseClass(botClass) != botClass; }

}  // namespace gms::bots
