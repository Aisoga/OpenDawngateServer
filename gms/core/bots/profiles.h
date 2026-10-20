// gms/core/bots (BOTPROFILE): the project's own bot profiles, BotClass assets the setup adds to every client and host
// copy (re/scripts/bot_profiles.py, server/internal/setup/botprofiles: game\Data\zz_BotProfiles.snap). Header-only.
//
// A profile copies a shipped BotClass (noun, AI definition, skill and spell order) and changes only its role, so a bot
// whose seat wants another role than its class's own shows that role on every client: the client's
// LabsPlayer_ReflectBotData sets the bot's mRole from the BotClass each time a player update carries mpBotData. The
// game ships no BotClass with the Predator role; PC_Warden_Predator is the Warden's Tactician profile as a Predator.
//
// HUNTERBOT: a borrowed-AI profile puts the noun of a shaper that ships without an AI (its `base`, e.g. PC_Golem) on
// another class's AI (`ai`: PC_Artificer's BOT_Artificer_Hunter), with a skill order of its own; the Hunter AI's
// gambits name ability slots, so the bot casts its own shaper's abilities (gms/core/bots/kits.h says which slots fit).
// DESECBOT: PC_Desecrator_Tactician puts Desecrator (no AI shipped) on PC_Guardian's BOT_Guardian_Tactician, with his
// own skill order and spells; it shops as PC_Desecrator (EA's Desecrator build).
// MIKELLABOT: PC_MerchantPrincess_Gladiator puts Mikella (no AI shipped) on PC_HighHuntress's BOT_HighHuntress_Gladiator,
// with a player guide's skill order and spells (her kit layer: gms/core/agents/mikella.h; her build: guidebuild.h).
// MOYABOT: PC_Otter_Hunter puts Moya (no AI shipped) on PC_Artificer's Hunter AI, with a 2014 guide's skill order and
// spells; it shops by that guide's build (guidebuild.h).
//
// The table must match bot_profiles.py's PROFILES (name, base, role, ai); the host uses a profile only when its own
// copy's catalog lists it (a copy set up before the profiles existed falls back to the base class with the role set
// after it; a borrowed-AI profile's base has no AI, so a copy without it falls back to its AI's class or another one:
// composition.h).
#pragma once

#include <string>
#include <vector>

namespace gms::bots {

struct Profile {
  const char* name;  // the BotClass asset's name without ".BotClass"
  const char* base;  // the shipped BotClass it copies
  int role;          // RolesTuning.role[] index: 0 Gladiator, 1 Tactician, 2 Hunter, 3 Predator
  const char* ai;    // HUNTERBOT: the BotClass whose AI it borrows; nullptr = its base's own
};

inline const std::vector<Profile>& Profiles() {
  static const std::vector<Profile> kProfiles = {{"PC_Warden_Predator", "PC_Warden", 3, nullptr},
                                                 {"PC_Golem_Hunter", "PC_Golem", 2, "PC_Artificer"},
                                                 {"PC_Survivor_Hunter", "PC_Survivor", 2, "PC_Artificer"},
                                                 {"PC_Desecrator_Tactician", "PC_Desecrator", 1, "PC_Guardian"},
                                                 {"PC_MerchantPrincess_Gladiator", "PC_MerchantPrincess", 0,
                                                  "PC_HighHuntress"},
                                                 {"PC_Otter_Hunter", "PC_Otter", 2, "PC_Artificer"}};
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

// HUNTERBOT: the class whose AI (and shopping-independent plan data) a bot of `botClass` runs: a borrowed-AI
// profile's donor, else the class's base.
inline std::string AiClass(const std::string& botClass) {
  for (const Profile& p : Profiles())
    if (botClass == p.name) return p.ai ? p.ai : p.base;
  return botClass;
}

}  // namespace gms::bots
