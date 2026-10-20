// GUARDCORE: the Guardian's five Cores and the special each one powers.
//
// The link is the game's own scripts, which run on the host unchanged [script, data]: a Core's death ability
// (ability_BaseNub_Death) puts modifier_Guardian_Nub<X>_Destroyed on its team's Guardian (nBattlegrounds.
// GetGuardianOnTeam), duration 0 = no time limit; that special's condition (condition_Guardian_Use*, the gambits of the
// random phase BaseTitanSpecial_v5) fails while the modifier is on; the Core's resurrect 480 s later (the same ability's
// Deactivate) marks the modifier for delete. Every special also puts modifier_Guardian_SpecialAbility_Cooldown on the
// Guardian, 6 s + 2 s per destroyed Core (its Activate counts the five Destroyed modifiers), which keeps the special
// phase closed (condition_Titan_UseSpecial). So the host only names and logs the state; it decides nothing here.
// Header-only; gms_tests GuardCore*.
#pragma once

#include <string>

namespace gms {
namespace flow {

struct GuardianCore {
  char letter;            // the noun's suffix: nub_<team>_v5_<letter>
  const char* special;    // the Guardian's ability the Core enables
  const char* condition;  // its gambit's condition, which checks modifier_Guardian_Nub<letter>_Destroyed
  const char* name;       // the build's own name for it (locale 0x0fd17e50..5f, 0x110fa9b7)
  const char* place;      // where the Core stands, looking from the Locus toward the Guardian [data: positions]
};

constexpr int kGuardianCores = 5;

inline const GuardianCore& GuardianCoreAt(int i) {
  static const GuardianCore kTable[kGuardianCores] = {
      {'A', "ability_Guardian_HealthPacks", "condition_Guardian_UseHealthPacks", "Eidolic Infusion (health packs)",
       "front centre"},
      {'B', "ability_Guardian_OrbLauncher", "condition_Guardian_UseOrbLauncher", "Valkyrie Barrage", "front left"},
      {'C', "ability_Guardian_FireWall", "condition_Guardian_UseFireTrail", "Fissure", "front right"},
      {'D', "ability_Guardian_KnockUpOrbs", "condition_Guardian_UseKnockUpOrbs", "Eidolic Prison (knock-up orbs)",
       "rear left"},
      {'E', "ability_Guardian_LaserBlast", "condition_Guardian_UseLaserBlast", "Eradication Beam", "rear right"},
  };
  return kTable[i < 0 || i >= kGuardianCores ? 0 : i];
}

// The Core index 0..4 (A..E) of a nub's noun name ("nub_mortal_v5_C", "nub_spirit_v5_E.Noun"), -1 for anything else.
inline int NubCoreIndex(const std::string& noun) {
  static const char* const kPrefixes[] = {"nub_mortal_v5_", "nub_spirit_v5_"};
  for (const char* prefix : kPrefixes) {
    const std::string p(prefix);
    if (noun.size() <= p.size() || noun.compare(0, p.size(), p) != 0) continue;
    const char c = noun[p.size()];
    const bool end = noun.size() == p.size() + 1 || noun[p.size() + 1] == '.';
    if (end && c >= 'A' && c <= 'E') return c - 'A';
  }
  return -1;
}

constexpr unsigned kAllCores = (1u << kGuardianCores) - 1;

inline int LivingCoreCount(unsigned livingMask) {
  int n = 0;
  for (int i = 0; i < kGuardianCores; ++i) n += (livingMask >> i) & 1u;
  return n;
}

// modifier_Guardian_SpecialAbility_Cooldown's duration: 6 s + 2 s per destroyed Core (its Activate counts the
// Destroyed modifiers; the script's variable says "numNubsAlive" but it counts the destroyed ones) [script].
inline double SpecialCooldownSeconds(unsigned livingMask) {
  return 6.0 + 2.0 * (kGuardianCores - LivingCoreCount(livingMask & kAllCores));
}

// "ABCDE" with '-' for a destroyed Core ("A-C-E").
inline std::string CoreMaskString(unsigned livingMask) {
  std::string s;
  for (int i = 0; i < kGuardianCores; ++i) s += (livingMask >> i) & 1u ? static_cast<char>('A' + i) : '-';
  return s;
}

// The specials the scripts still allow ("HealthPacks OrbLauncher ..."), or "none (basic attacks only)".
inline std::string SpecialsOf(unsigned livingMask) {
  std::string s;
  for (int i = 0; i < kGuardianCores; ++i) {
    if (!((livingMask >> i) & 1u)) continue;
    const std::string ab = GuardianCoreAt(i).special;
    const std::string shortName = ab.rfind("ability_Guardian_", 0) == 0 ? ab.substr(17) : ab;
    s += (s.empty() ? "" : " ") + shortName;
  }
  return s.empty() ? "none (basic attacks only)" : s;
}

}  // namespace flow
}  // namespace gms
