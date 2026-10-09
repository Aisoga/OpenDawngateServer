// gms/core/surrender (G3b): a team whose every human has left the running match counts as that team's surrender.
// Header-only, engine-free.
//
// From the Dawngate Revival project's public changelog (a test idea, pasted by the user 2026-10-09; none of their code):
// "a whole team leaving counts as that team's surrender, with the victory screen for the others". The original GMS's
// rule isn't in the client [assumed]: the match host ends the match by match::EndBySurrender (the surrender's reasons 2
// and 4, its cinematic and the matchmade post-game, D8b) once every human that had a seat on a team has been without a
// connection for `graceS` seconds (a rejoin in that time, G3, keeps the match going), and the other team still has a
// connected human (with nobody connected at all dgserver ends the game anyway: abortGame "no players left"). Bots
// don't count as the team's players: a team of bots and departed humans has left.
#pragma once

namespace gms::surrender {

// One team's humans at a tick: those that ever had a seat on it, and those connected now.
struct TeamHumans {
  int seated = 0;
  int connected = 0;
};

class TeamLeftWatch {
 public:
  // graceS < 0: off.
  explicit TeamLeftWatch(double graceS = 60.0) : graceS_(graceS) {}

  // Every tick of the running match: team 1's and team 2's humans. Returns the team (1 or 2) whose humans have all
  // been gone for graceS while the other team has a connected human, once; else 0.
  int Tick(double nowS, const TeamHumans& team1, const TeamHumans& team2) {
    if (graceS_ < 0 || fired_) return 0;
    const TeamHumans* t[2] = {&team1, &team2};
    for (int i = 0; i < 2; ++i) {
      const bool left = t[i]->seated > 0 && t[i]->connected == 0;
      const bool otherHere = t[1 - i]->connected > 0;
      if (!left || !otherHere) {
        since_[i] = -1.0;
        continue;
      }
      if (since_[i] < 0) since_[i] = nowS;
      if (nowS - since_[i] >= graceS_) {
        fired_ = true;
        return i + 1;
      }
    }
    return 0;
  }
  // The second the team's last human left (-1 = it has a connected human, or the other team has none).
  double LeftSince(int team) const { return team == 1 || team == 2 ? since_[team - 1] : -1.0; }
  double GraceS() const { return graceS_; }
  bool Fired() const { return fired_; }

 private:
  double graceS_;
  double since_[2] = {-1.0, -1.0};
  bool fired_ = false;
};

}  // namespace gms::surrender
