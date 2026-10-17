// CAMPICONS: a jungle camp's minimap icon as each team's knowledge of the camp. The client draws a camp spawn point's
// icon only while the camp's 8D entry (cBattlegroundsUICampData +0 mCampVisibleToTeam) has the local team's bit,
// 1 << team (GGMinimap_UpdateObjectIcon 0x005801F0), and the entry has a bit per team, so one byte carries what each
// team knows. The original's writer was stripped; the rule is the user's (2026-10-17, League's model: "an icon shows
// what a team knows"):
//   - no team has the icon before the camp's first spawn; the spawn is public: every team gets it;
//   - a witnessed death (the camp's last creature dies while a team sees it: that creature's mVisibleToTeam at its death
//     tick, and the killer's team whatever its vision) turns that team's icon off and makes the respawn time known to
//     it; at that time the icon comes back on without sight;
//   - an unwitnessed death changes nothing: the team's icon stays on (stale) until the team sees the camp;
//   - seeing a living member turns the icon on (the camp is alive);
//   - seeing the camp's spot empty turns the icon off (CAMPFIX, the user 2026-10-17: a camp seen empty must not keep
//     its icon); within the reveal window before the respawn (buff camps, the Boar's included, 60 s; the small camps
//     10 s) the respawn time becomes known too, outside it the time stays unknown and the icon comes back with the
//     (public) respawn;
//   - a spectator gets the truth (TruthBits), not a team's knowledge.
// Engine-free: the director (gms/engine/director.cpp) feeds the events and writes Bits() into the server's camp entry.
#pragma once

#include <cstdint>
#include <vector>

namespace gms {
namespace flow {

constexpr double kCampRevealBuffS = 60.0;   // buff camps and the Boar camp (the user, 2026-10-17: "Boar as buff")
constexpr double kCampRevealSmallS = 10.0;  // the small camps

// The camp entry's bits for a spectator: the camp's real state (every team's bit while a creature lives).
inline uint8_t CampTruthBits(bool alive) { return static_cast<uint8_t>(alive ? 0x07 : 0x01); }

class CampKnowledge {
 public:
  static constexpr int kTeams = 2;  // team 1 (Mortal, bit 0x02) and team 2 (Spirit, bit 0x04)

  // One change of a team's knowledge (for the host's log): the team (1 or 2), its icon now, the respawn time it knows
  // now (< 0 = none), and why.
  struct Change {
    int team = 0;
    bool icon = false;
    double knownRespawnAt = -1.0;
    const char* why = "";
  };

  void Init(double revealWindowS) {
    reveal_ = revealWindowS;
    spawned_ = false;
    for (int i = 0; i < kTeams; ++i) team_[i] = Team();
  }

  // The camp spawned (its first spawn or a respawn): public, every team gets the icon.
  std::vector<Change> OnSpawned() {
    std::vector<Change> out;
    spawned_ = true;
    for (int i = 0; i < kTeams; ++i) Set(i, true, -1.0, "the camp spawned", &out);
    return out;
  }

  // The camp's last creature died. `witnesses` = that creature's mVisibleToTeam (bit 1 << team); `respawnAt` = the
  // camp's next spawn (match s; < 0 = never). A witness loses the icon and knows the time; the others keep theirs.
  std::vector<Change> OnCleared(uint8_t witnesses, double respawnAt) {
    std::vector<Change> out;
    for (int i = 0; i < kTeams; ++i)
      if (witnesses & Bit(i)) Set(i, false, respawnAt, "saw the camp's last creature die", &out);
    return out;
  }

  // What the teams see now (match s `t`): `seeAlive` = the teams that see a living member, `seeSpot` = the teams that
  // see the camp's spot; `alive` and `respawnAt` = the truth (respawnAt only while the camp is empty). Then the known
  // respawn times that came.
  std::vector<Change> Observe(double t, uint8_t seeAlive, uint8_t seeSpot, bool alive, double respawnAt) {
    std::vector<Change> out;
    for (int i = 0; i < kTeams; ++i) {
      Team& tm = team_[i];
      if (alive && (seeAlive & Bit(i))) {
        Set(i, true, -1.0, "saw the camp alive", &out);
      } else if (!alive && (seeSpot & Bit(i)) && (respawnAt < 0.0 || t < respawnAt)) {
        // seen empty: no icon; the respawn time known within the reveal window (or already known)
        const bool inWindow = respawnAt >= 0.0 && t >= respawnAt - reveal_;
        if (tm.known >= 0.0)
          Set(i, false, tm.known, "saw the camp empty", &out);
        else if (inWindow)
          Set(i, false, respawnAt, "saw the camp empty within the reveal window", &out);
        else
          Set(i, false, -1.0, "saw the camp empty (respawn time unknown)", &out);
      }
      if (tm.known >= 0.0 && t >= tm.known) Set(i, true, -1.0, "the known respawn time came", &out);
    }
    return out;
  }

  // The camp entry's byte for the players: the NPC bit and every team with the icon.
  uint8_t Bits() const {
    uint8_t b = 0x01;
    for (int i = 0; i < kTeams; ++i)
      if (team_[i].icon) b = static_cast<uint8_t>(b | Bit(i));
    return b;
  }
  bool Icon(int team) const { return Valid(team) && team_[team - 1].icon; }
  // The respawn time `team` knows (< 0 = none): a witnessed death or the reveal window, until the respawn.
  double KnownRespawnAt(int team) const { return Valid(team) ? team_[team - 1].known : -1.0; }
  bool Spawned() const { return spawned_; }
  double RevealWindow() const { return reveal_; }

 private:
  struct Team {
    bool icon = false;
    double known = -1.0;
  };
  static bool Valid(int team) { return team >= 1 && team <= kTeams; }
  static uint8_t Bit(int i) { return static_cast<uint8_t>(1u << (i + 1)); }
  void Set(int i, bool icon, double known, const char* why, std::vector<Change>* out) {
    Team& tm = team_[i];
    if (tm.icon == icon && tm.known == known) return;
    tm.icon = icon;
    tm.known = known;
    out->push_back({i + 1, icon, known, why});
  }

  double reveal_ = kCampRevealSmallS;
  bool spawned_ = false;
  Team team_[kTeams];
};

}  // namespace flow
}  // namespace gms
