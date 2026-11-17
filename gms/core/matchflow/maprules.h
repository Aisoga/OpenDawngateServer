// MAPRULES4: a map's own victory rules and start level, engine-free (gms_tests MapRules*). The host gets them from
// createGame.mapRules {kills, minutes, startLevel} (dgserver reads them from the map's map.json; [host] map_kills,
// map_minutes and start_level are test overrides) and runs them in gms/engine/maprules.cpp:
//   - a kill race: the first team whose kills reach `kills` wins (two teams over it in one tick: the higher count; a
//     tie there goes to sudden death);
//   - a time limit: when `minutes` are over the team with more kills wins; a tie goes to sudden death, which the first
//     tick with different counts ends;
//   - a Binding race (WINBIND-B): the first team to destroy `bindings` enemy Bindings wins (reason "bindings"; a
//     respawned Binding counts again; two teams there in one tick: the higher count, a tie goes to sudden death); with a
//     time limit the team with more Bindings wins at the time, then the one with more kills, then sudden death, which
//     the next Binding or kill ends;
//   - the Guardian and a surrender still end the match as before (the host stops calling Step once it has ended);
//   - a team's kills are the sum of its players' cLabsPlayer.mKills, bots included (the score bar's number);
//   - the alerts: the race's milestones per team (ally and enemy lines), the opening line, the countdown, the lead
//     changes (time limit only, at most every kLeadGapS), sudden death; at least kLineGapS between two lines; team lines
//     leave the spectators out, which get the neutral lines only. Each line is a ClientFeedbackDef of the setup
//     (Go botprofiles.MapRulesFeedbackDefs) whose text is a LabsAlerts line from 0x5e70e010 (Go localepatch): no
//     ~token~ prints a number, so one def per message;
//   - the start level: every seat, bots included, gets the XP of level N from the level's own LevelUpTuning, one level
//     step per grant (LevelSteps), so a bot's LabsPlayer_GrantXP ranks its skill order at each level as in play.
#pragma once

#include <cstdint>
#include <string>
#include <vector>

namespace gms {
namespace maprules {

// The host's own bounds (it clamps whatever createGame says; mapc's checks allow kills 1-200, minutes 3-90 and
// startLevel 1-20; the host lets a test go down to 1 minute).
constexpr int kMaxKills = 200;
constexpr int kMaxMinutes = 90;
constexpr int kMaxStartLevel = 20;
constexpr int kMaxBindings = 12;  // mapc's limit too (WINBIND-A); a respawning Binding may be counted again

constexpr double kLineGapS = 3.0;   // at least this much match time between two alert lines
constexpr double kLeadGapS = 30.0;  // a lead line at most this often
constexpr double kOpeningS = 15.0;  // the opening line ("First team to N kills wins!") this long after the start

constexpr unsigned kEveryone = 0x07u;          // team bits 0 (spectators), 1 and 2
constexpr unsigned kSpectatorBits = 0x7C00u;   // gameplay indices 10-14 (a custom game's observers)

struct Rules {
  int kills = 0;       // 0 = no kill race
  int minutes = 0;     // 0 = no time limit
  int startLevel = 0;  // 0 or 1 = level 1, as without the rule
  int bindings = 0;    // 0 = no Binding race (WINBIND-B): destructions of enemy Bindings to win
  bool Victory() const { return kills > 0 || minutes > 0 || bindings > 0; }
  bool Any() const { return Victory() || startLevel > 1; }
};

// The values within the host's bounds: below 0 -> 0, above the maximum -> the maximum, a start level of 1 -> 0. A
// note of what changed goes into *note ("" when nothing did).
Rules Clamp(long long kills, long long minutes, long long startLevel, long long bindings, std::string* note = nullptr);
// "kills 20, bindings 3, minutes 15, start level 6" ("none" when nothing is set).
std::string Describe(const Rules& r);

// WINLOBBY: the match's rules from their three sources (gms/engine/maprules.cpp holds one; gms_tests
// MapRulesSources*): the test overrides ([host] map_kills, map_minutes, start_level, map_bindings: a member set there
// wins over both messages), createGame's mapRules (the map's victory and start level) and startGame's victory (the
// lobby's win condition). The two messages may come in either order (host/control.cpp's Poll handles a startGame
// before a createGame read in the same frame): once startGame's victory is set, createGame's victory never replaces
// it; createGame's start level is always taken (startGame carries none). Each call takes clamped values (Clamp).
class Sources {
 public:
  // The test overrides, as given (Configure's [host] options): each value above 0 is one, clamped (Clamp's note).
  void Override(long long kills, long long minutes, long long startLevel, long long bindings, std::string* note);
  // createGame's mapRules.
  void FromCreate(const Rules& r);
  // startGame's victory (r.startLevel is ignored).
  void FromStart(const Rules& r);
  const Rules& Current() const { return rules_; }
  bool AnyOverride() const { return overKills_ || overMinutes_ || overLevel_ || overBindings_; }
  bool VictoryOverride() const { return overKills_ || overMinutes_ || overBindings_; }
  bool StartSet() const { return fromStart_; }

 private:
  void Victory(const Rules& r);
  Rules rules_;
  bool overKills_ = false, overMinutes_ = false, overLevel_ = false, overBindings_ = false;
  bool fromStart_ = false;
};

// The alert lines (the order is the LabsAlerts ids': kRace10Ally = 0x5e70e010, ...).
enum class Line : int {
  kRace10Ally,
  kRace10Enemy,
  kRace5Ally,
  kRace5Enemy,
  kRace3Ally,
  kRace3Enemy,
  kMatchPointAlly,
  kMatchPointEnemy,
  kRaceTo10,
  kRaceTo15,
  kRaceTo20,
  kRaceTo25,
  kRaceTo30,
  kRaceTo50,
  kRaceToTarget,
  kTimeLimit,
  kLeadAlly,
  kLeadEnemy,
  kLeft10Min,
  kLeft5Min,
  kLeft1Min,
  kLeft30Sec,
  kSuddenDeath,
  // WINBIND-B: the Binding race's lines, appended (0x5e70e027-0x5e70e02f)
  kBindRaceTo1,
  kBindRaceTo2,
  kBindRaceTo3,
  kBindRaceTo6,
  kBindRaceToTarget,
  kBindMatchPointAlly,
  kBindMatchPointEnemy,
  kBindTimeLimit,
  kBindSuddenDeath,
  kCount
};
constexpr int kLineCount = static_cast<int>(Line::kCount);
constexpr std::uint32_t kFirstLineId = 0x5E70E010u;

const char* DefName(Line l);  // "Rules_Race10Ally.ClientFeedbackDef"
const char* Text(Line l);     // the en-us line, for the logs and the tests
inline std::uint32_t LineId(Line l) { return kFirstLineId + static_cast<std::uint32_t>(l); }
// The opening line of a kill race of `kills`: its own line for 10, 15, 20, 25, 30 and 50, else kRaceToTarget.
Line OpeningLine(int kills);
// The opening line of a Binding race of `bindings`: its own line for 1, 2, 3 and 6, else kBindRaceToTarget.
Line BindOpeningLine(int bindings);

// One send: a team line goes to 1 << team with the spectators left out, a neutral line to kEveryone.
struct Send {
  Line line = Line::kCount;
  unsigned teamBits = 0;
  unsigned excludePlayers = 0;
};

enum class Why { kNone, kKills, kTime, kSuddenDeath, kBindings };
// finishGame's reason: "kills", "time", "suddenDeath", "bindings" ("" for kNone).
const char* FinishReason(Why w);

struct StepResult {
  Why end = Why::kNone;
  int winner = 0;  // 1 or 2 with an end
  std::vector<Send> sends;
};

// One match's race. Step every tick with the match seconds, the two teams' kills and their destructions of enemy
// Bindings; after an end it returns nothing.
class Race {
 public:
  explicit Race(const Rules& r = Rules());
  StepResult Step(double t, int kills1, int kills2, int bindings1 = 0, int bindings2 = 0);
  bool Ended() const { return ended_; }
  bool SuddenDeath() const { return sudden_; }
  const Rules& rules() const { return rules_; }

 private:
  struct Item {
    Send s[2];
    int n = 0;
    int leadTeam = 0;  // a lead line's team: dropped if that team no longer leads when its turn comes
  };
  void Queue(const Item& it, bool front = false);
  void QueuePair(Line ally, Line enemy, int team);
  void QueueNeutral(Line l, bool front = false);
  // the team ahead (1 or 2; 0 = level): by Bindings first in a Binding race, then by kills
  int Leader(const int k[2], const int b[2]) const;
  void StartSudden(const int k[2], const int b[2]);
  // sudden death's winner: the first team to score after it began (a Binding before a kill), 0 = none yet
  int SuddenWinner(const int k[2], const int b[2]) const;

  Rules rules_;
  bool ended_ = false;
  bool sudden_ = false;
  bool opened_ = false;
  int nextMilestone_[2] = {0, 0};  // per team: the index into the milestones of the next one not yet passed
  bool bindPointDone_[2] = {true, true};  // per team: the Binding race's match point told (or none: a race of 1)
  int suddenKills_[2] = {0, 0};     // the counts when sudden death began
  int suddenBindings_[2] = {0, 0};
  bool countdownDone_[4] = {false, false, false, false};
  int leadAnnounced_ = 0;
  double leadAt_ = -1.0;
  double lastLineAt_ = -1.0e9;
  std::vector<Item> queue_;
};

// The XP grants that take a seat from level `from` to level `to`: one level step each (Level[l + 1].xpRequired -
// Level[l].xpRequired; `xpRequired[i]` is LevelUpTuning.Level[i], index 1 = level 1 with 0 XP). `to` is clamped to the
// table's last level; nothing when `to` <= `from`.
std::vector<float> LevelSteps(const float* xpRequired, int n, int from, int to);
// The cumulative XP of level `level` (xpRequired[level], clamped to the table), 0 without a table.
float CumulativeXp(const float* xpRequired, int n, int level);

}  // namespace maprules
}  // namespace gms
