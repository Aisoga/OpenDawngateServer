// gms/core/matchflow/maprules.h (MAPRULES4): the kill race, the time limit, sudden death, the alerts and their pace,
// the spectators left out of the team lines, the clamp and the start level's XP steps.
#include <cstring>
#include <string>
#include <vector>

#include "core/matchflow/maprules.h"
#include "tests/check.h"

using namespace gms::maprules;

namespace {

Rules R(int kills, int minutes, int startLevel = 0, int bindings = 0) {
  Rules r;
  r.bindings = bindings;
  r.kills = kills;
  r.minutes = minutes;
  r.startLevel = startLevel;
  return r;
}

// Every line a race sends from `from` to `to` (step 0.5 s) at fixed counts, as "<def>@<team bits>" strings.
struct Seen {
  std::vector<std::string> lines;
  StepResult last;
};

std::string Name(const Send& s) {
  std::string n = DefName(s.line);
  n = n.substr(0, n.find('.'));
  return n + "@" + std::to_string(s.teamBits);
}

void Run(Race& race, double from, double to, int k1, int k2, Seen* seen) {
  for (double t = from; t <= to + 1e-9; t += 0.5) {
    StepResult r = race.Step(t, k1, k2);
    for (const Send& s : r.sends) seen->lines.push_back(Name(s));
    if (r.end != Why::kNone) {
      seen->last = r;
      return;
    }
  }
}

int Count(const std::vector<std::string>& v, const std::string& s) {
  int n = 0;
  for (const std::string& x : v) n += x == s ? 1 : 0;
  return n;
}

}  // namespace

GMS_TEST(MapRulesLines) {
  CHECK_EQ(LineId(Line::kRace10Ally), 0x5E70E010u);
  CHECK_EQ(LineId(Line::kSuddenDeath), 0x5E70E010u + 22u);  // MAPRULES4's last; WINBIND-B appends from 0x5e70e027
  for (int i = 0; i < kLineCount; ++i) {
    const Line l = static_cast<Line>(i);
    const std::string text = Text(l);
    CHECK(!text.empty() && text.size() <= 45);
    CHECK(text.find('~') == std::string::npos);  // only the client's twelve tokens resolve; none prints a number
    const std::string def = DefName(l);
    CHECK(def.rfind("Rules_", 0) == 0);
    CHECK(def.size() > 18 && def.substr(def.size() - 18) == ".ClientFeedbackDef");
  }
  CHECK_EQ(std::string(DefName(Line::kMatchPointEnemy)), std::string("Rules_MatchPointEnemy.ClientFeedbackDef"));
  CHECK_EQ(std::string(Text(Line::kRaceTo20)), std::string("First team to 20 kills wins!"));
  CHECK(OpeningLine(20) == Line::kRaceTo20);
  CHECK(OpeningLine(50) == Line::kRaceTo50);
  CHECK(OpeningLine(3) == Line::kRaceToTarget);
  CHECK(OpeningLine(12) == Line::kRaceToTarget);
  CHECK_EQ(std::string(FinishReason(Why::kKills)), std::string("kills"));
  CHECK_EQ(std::string(FinishReason(Why::kTime)), std::string("time"));
  CHECK_EQ(std::string(FinishReason(Why::kSuddenDeath)), std::string("suddenDeath"));
  CHECK_EQ(std::string(FinishReason(Why::kNone)), std::string(""));
}

GMS_TEST(MapRulesClamp) {
  std::string note;
  Rules r = Clamp(20, 15, 6, 0, &note);
  CHECK_EQ(r.kills, 20);
  CHECK_EQ(r.minutes, 15);
  CHECK_EQ(r.startLevel, 6);
  CHECK_EQ(note, std::string(""));
  CHECK(r.Victory() && r.Any());
  r = Clamp(-5, 100000, 99, 0, &note);  // a hostile or broken createGame
  CHECK_EQ(r.kills, 0);
  CHECK_EQ(r.minutes, kMaxMinutes);
  CHECK_EQ(r.startLevel, kMaxStartLevel);
  CHECK_EQ(note, std::string("kills -5 -> 0, minutes 100000 -> 90, startLevel 99 -> 20"));
  r = Clamp(0, 0, 1, 0, &note);  // level 1 is the game's own start
  CHECK_EQ(r.startLevel, 0);
  CHECK_EQ(note, std::string(""));
  CHECK(!r.Any());
  CHECK_EQ(Describe(r), std::string("none"));
  CHECK_EQ(Describe(Clamp(3, 0, 6, 0)), std::string("kills 3, start level 6"));
  CHECK_EQ(Describe(Clamp(0, 3, 0, 0)), std::string("minutes 3"));
  CHECK(Clamp(0, 0, 6, 0).Any() && !Clamp(0, 0, 6, 0).Victory());
  r = Clamp(9223372036854775807LL, -9223372036854775807LL - 1, 0, 0);
  CHECK_EQ(r.kills, kMaxKills);
  CHECK_EQ(r.minutes, 0);
}

// A kill race of 3 (the live check's): its opening line, no 3/5/10 line (it starts inside them), the match point once,
// the end at 3 with the reason "kills".
GMS_TEST(MapRulesKillRaceOfThree) {
  Race race(R(3, 0));
  Seen s;
  Run(race, 0.0, 20.0, 0, 0, &s);
  CHECK_EQ(s.lines.size(), static_cast<size_t>(1));
  CHECK_EQ(s.lines[0], std::string("Rules_RaceToTarget@7"));  // neutral: everyone, spectators too
  Run(race, 20.5, 40.0, 1, 0, &s);
  CHECK_EQ(s.lines.size(), static_cast<size_t>(1));
  Run(race, 40.5, 60.0, 2, 0, &s);  // team 1 at match point
  CHECK_EQ(s.lines.size(), static_cast<size_t>(3));
  CHECK_EQ(s.lines[1], std::string("Rules_MatchPointAlly@2"));
  CHECK_EQ(s.lines[2], std::string("Rules_MatchPointEnemy@4"));
  Run(race, 60.5, 80.0, 2, 2, &s);  // team 2 too, once
  CHECK_EQ(Count(s.lines, "Rules_MatchPointAlly@4"), 1);
  CHECK_EQ(Count(s.lines, "Rules_MatchPointEnemy@2"), 1);
  CHECK_EQ(Count(s.lines, "Rules_MatchPointAlly@2"), 1);
  CHECK(s.last.end == Why::kNone);
  Run(race, 80.5, 81.0, 2, 3, &s);
  CHECK(s.last.end == Why::kKills);
  CHECK_EQ(s.last.winner, 2);
  CHECK(race.Ended());
  StepResult after = race.Step(82.0, 2, 9);  // ended: nothing more
  CHECK(after.end == Why::kNone && after.sends.empty());
}

// A race of 20: each milestone once per team, the smallest one when a tick passes several, the enemy's line to the other
// team; every team line leaves the spectators out.
GMS_TEST(MapRulesMilestones) {
  Race race(R(20, 0));
  std::vector<Send> sends;
  auto step = [&](double t, int k1, int k2) {
    StepResult r = race.Step(t, k1, k2);
    sends.insert(sends.end(), r.sends.begin(), r.sends.end());
    return r;
  };
  step(16.0, 0, 0);  // the opening line
  CHECK_EQ(sends.size(), static_cast<size_t>(1));
  CHECK(sends[0].line == Line::kRaceTo20 && sends[0].teamBits == kEveryone && sends[0].excludePlayers == 0);
  step(20.0, 10, 0);  // 10 to go
  CHECK_EQ(sends.size(), static_cast<size_t>(3));
  CHECK(sends[1].line == Line::kRace10Ally && sends[1].teamBits == 2u && sends[1].excludePlayers == kSpectatorBits);
  CHECK(sends[2].line == Line::kRace10Enemy && sends[2].teamBits == 4u && sends[2].excludePlayers == kSpectatorBits);
  step(30.0, 11, 0);
  step(40.0, 10, 0);  // a count that goes down (a read glitch) says nothing again
  CHECK_EQ(sends.size(), static_cast<size_t>(3));
  step(50.0, 17, 0);  // 3 to go: passes 5 and 3 at once, only the 3 is told
  CHECK_EQ(sends.size(), static_cast<size_t>(5));
  CHECK(sends[3].line == Line::kRace3Ally && sends[4].line == Line::kRace3Enemy);
  step(60.0, 17, 15);  // team 2 at 5 to go (passing 10 at once): the 5
  CHECK_EQ(sends.size(), static_cast<size_t>(7));
  CHECK(sends[5].line == Line::kRace5Ally && sends[5].teamBits == 4u);
  CHECK(sends[6].line == Line::kRace5Enemy && sends[6].teamBits == 2u);
  step(70.0, 19, 15);
  CHECK_EQ(sends.size(), static_cast<size_t>(9));
  CHECK(sends[7].line == Line::kMatchPointAlly && sends[8].line == Line::kMatchPointEnemy);
  for (int t = 71; t < 200; ++t) step(t, 19, 15);  // nothing is told twice
  CHECK_EQ(sends.size(), static_cast<size_t>(9));
  for (const Send& s : sends)
    if (s.teamBits != kEveryone) CHECK_EQ(s.excludePlayers, kSpectatorBits);
  StepResult r = step(200.0, 20, 15);
  CHECK(r.end == Why::kKills && r.winner == 1);
}

// Two teams reach the target in one tick: the higher count wins; the same count goes to sudden death.
GMS_TEST(MapRulesSimultaneousKills) {
  {
    Race race(R(10, 0));
    StepResult r = race.Step(100.0, 11, 10);
    CHECK(r.end == Why::kKills && r.winner == 1);
  }
  {
    Race race(R(10, 0));
    race.Step(100.0, 9, 9);
    StepResult r = race.Step(101.0, 10, 10);
    CHECK(r.end == Why::kNone);
    CHECK(race.SuddenDeath());
    CHECK(r.sends.empty());  // the opening line went out at 100 s: the next one waits kLineGapS
    r = race.Step(104.0, 10, 10);
    CHECK_EQ(r.sends.size(), static_cast<size_t>(1));  // the match points still queued were dropped
    bool told = false;
    for (const Send& s : r.sends) told = told || (s.line == Line::kSuddenDeath && s.teamBits == kEveryone);
    CHECK(told);
    r = race.Step(110.0, 10, 10);
    CHECK(r.end == Why::kNone);
    r = race.Step(111.0, 10, 11);
    CHECK(r.end == Why::kSuddenDeath && r.winner == 2);
  }
}

// A 3-minute limit that ends tied (the live check's): the opening line, 1 minute and 30 seconds left (no 10 or 5 minute
// line: the limit starts inside them), sudden death at 180 s, its end at the first difference.
GMS_TEST(MapRulesTimeLimitSuddenDeath) {
  Race race(R(0, 3));
  Seen s;
  Run(race, 0.0, 179.5, 1, 1, &s);
  std::vector<std::string> want = {"Rules_TimeLimit@7", "Rules_Left1Min@7", "Rules_Left30Sec@7"};
  CHECK(s.lines == want);
  CHECK(s.last.end == Why::kNone);
  StepResult r = race.Step(180.0, 1, 1);
  CHECK(r.end == Why::kNone && race.SuddenDeath());
  CHECK(r.sends.size() == 1 && r.sends[0].line == Line::kSuddenDeath && r.sends[0].teamBits == kEveryone);
  for (double t = 180.5; t < 600.0; t += 0.5) {
    r = race.Step(t, 1, 1);  // no cap on sudden death
    CHECK(r.end == Why::kNone && r.sends.empty());
  }
  r = race.Step(600.0, 2, 1);
  CHECK(r.end == Why::kSuddenDeath && r.winner == 1);
}

// The time limit with a leader: the leader wins with the reason "time"; the countdown of a long limit.
GMS_TEST(MapRulesTimeLimitLeader) {
  Race race(R(0, 15));
  Seen s;
  Run(race, 0.0, 899.5, 0, 0, &s);
  std::vector<std::string> want = {"Rules_TimeLimit@7", "Rules_Left10Min@7", "Rules_Left5Min@7", "Rules_Left1Min@7",
                                   "Rules_Left30Sec@7"};
  CHECK(s.lines == want);
  StepResult r = race.Step(900.0, 4, 6);
  CHECK(r.end == Why::kTime && r.winner == 2);
}

// The lead lines: to the new leader and the other team, the spectators left out, at most every 30 s; a lead that is
// gone before its turn is dropped.
GMS_TEST(MapRulesLead) {
  Race race(R(0, 30));
  std::vector<Send> sends;
  auto step = [&](double t, int k1, int k2) {
    StepResult r = race.Step(t, k1, k2);
    sends.insert(sends.end(), r.sends.begin(), r.sends.end());
  };
  step(16.0, 0, 0);  // the opening
  sends.clear();
  step(40.0, 1, 0);
  CHECK_EQ(sends.size(), static_cast<size_t>(2));
  CHECK(sends[0].line == Line::kLeadAlly && sends[0].teamBits == 2u && sends[0].excludePlayers == kSpectatorBits);
  CHECK(sends[1].line == Line::kLeadEnemy && sends[1].teamBits == 4u && sends[1].excludePlayers == kSpectatorBits);
  sends.clear();
  step(45.0, 1, 1);  // a tie says nothing
  step(50.0, 1, 2);  // team 2 leads 10 s later: too soon
  CHECK(sends.empty());
  step(69.5, 1, 2);
  CHECK(sends.empty());
  step(70.0, 1, 2);  // 30 s after the last lead line
  CHECK_EQ(sends.size(), static_cast<size_t>(2));
  CHECK(sends[0].line == Line::kLeadAlly && sends[0].teamBits == 4u);
  sends.clear();
  step(80.0, 1, 3);  // the same leader: nothing
  step(120.0, 1, 3);
  CHECK(sends.empty());
}

// At least kLineGapS between two lines: lines due together come one by one.
GMS_TEST(MapRulesPace) {
  Race race(R(20, 15));
  std::vector<std::pair<double, Line>> got;
  for (double t = 0.0; t < 40.0; t += 0.25) {
    // both teams pass 10 to go at 15 s, with the two opening lines due at the same tick
    const int k = t >= 15.0 ? 10 : 0;
    StepResult r = race.Step(t, k, k);
    for (const Send& s : r.sends) got.push_back({t, s.line});
  }
  // opening race, opening time limit, team 1's 10 (two sends), team 2's 10 (two sends); team 1 leads nowhere (tied)
  CHECK_EQ(got.size(), static_cast<size_t>(6));
  if (got.size() == 6) {
    CHECK(got[0].second == Line::kRaceTo20);
    CHECK(got[1].second == Line::kTimeLimit);
    CHECK(got[1].first - got[0].first >= kLineGapS);
    CHECK(got[2].second == Line::kRace10Ally && got[3].second == Line::kRace10Enemy && got[2].first == got[3].first);
    CHECK(got[2].first - got[1].first >= kLineGapS);
    CHECK(got[4].first - got[2].first >= kLineGapS);
  }
}

// No victory rule: Step says nothing and never ends (a start level alone).
GMS_TEST(MapRulesNone) {
  Race race(R(0, 0, 6));
  for (double t = 0.0; t < 2000.0; t += 1.0) {
    StepResult r = race.Step(t, static_cast<int>(t) % 7, 3);
    CHECK(r.end == Why::kNone && r.sends.empty());
  }
}

GMS_TEST(MapRulesLevelSteps) {
  // LevelUpTuning.Level[i].xpRequired: index 0 unused, level 1 = 0 XP, level 2 = 260, ...
  const float xp[] = {0.f, 0.f, 260.f, 600.f, 1000.f, 1460.f, 1980.f, 2560.f};
  const int n = static_cast<int>(sizeof xp / sizeof xp[0]);
  std::vector<float> steps = LevelSteps(xp, n, 1, 6);
  CHECK_EQ(steps.size(), static_cast<size_t>(5));
  float sum = 0.f;
  for (float s : steps) sum += s;
  CHECK_EQ(sum, 1980.f);
  CHECK_EQ(CumulativeXp(xp, n, 6), 1980.f);
  CHECK_EQ(steps[0], 260.f);
  CHECK_EQ(steps[4], 520.f);
  CHECK(LevelSteps(xp, n, 6, 6).empty());
  CHECK(LevelSteps(xp, n, 7, 6).empty());
  CHECK_EQ(LevelSteps(xp, n, 1, 20).size(), static_cast<size_t>(6));  // clamped to the table's last level
  CHECK_EQ(LevelSteps(xp, n, 3, 6).size(), static_cast<size_t>(3));   // a seat that is level 3 already
  CHECK(LevelSteps(nullptr, 0, 1, 6).empty());
  CHECK_EQ(CumulativeXp(xp, n, 99), 2560.f);
}

// ---- WINBIND-B: the Binding race --------------------------------------------------------------------------------

namespace {

void RunB(Race& race, double from, double to, int k1, int k2, int b1, int b2, Seen* seen) {
  for (double t = from; t <= to + 1e-9; t += 0.5) {
    StepResult r = race.Step(t, k1, k2, b1, b2);
    for (const Send& s : r.sends) seen->lines.push_back(Name(s));
    if (r.end != Why::kNone) {
      seen->last = r;
      return;
    }
  }
}

}  // namespace

// The new lines follow MAPRULES4's from 0x5e70e027; the clamp and the description name the Binding race.
GMS_TEST(MapRulesBindLines) {
  CHECK_EQ(kLineCount, 32);
  CHECK_EQ(LineId(Line::kBindRaceTo1), 0x5E70E027u);
  CHECK_EQ(LineId(Line::kBindSuddenDeath), 0x5E70E02Fu);
  CHECK_EQ(std::string(DefName(Line::kBindMatchPointEnemy)),
           std::string("Rules_BindMatchPointEnemy.ClientFeedbackDef"));
  CHECK_EQ(std::string(Text(Line::kBindRaceTo1)), std::string("First team to destroy a Binding wins!"));
  CHECK_EQ(std::string(Text(Line::kBindSuddenDeath)), std::string("Sudden death: next Binding or kill wins!"));
  CHECK(BindOpeningLine(1) == Line::kBindRaceTo1);
  CHECK(BindOpeningLine(2) == Line::kBindRaceTo2);
  CHECK(BindOpeningLine(3) == Line::kBindRaceTo3);
  CHECK(BindOpeningLine(6) == Line::kBindRaceTo6);
  CHECK(BindOpeningLine(4) == Line::kBindRaceToTarget);
  CHECK(BindOpeningLine(12) == Line::kBindRaceToTarget);
  CHECK_EQ(std::string(FinishReason(Why::kBindings)), std::string("bindings"));
  std::string note;
  Rules r = Clamp(0, 0, 0, 3, &note);
  CHECK_EQ(r.bindings, 3);
  CHECK(r.Victory() && r.Any());
  CHECK_EQ(note, std::string(""));
  r = Clamp(0, 0, 0, 99, &note);
  CHECK_EQ(r.bindings, kMaxBindings);
  CHECK_EQ(note, std::string("bindings 99 -> 12"));
  r = Clamp(0, 0, 0, -1, &note);
  CHECK_EQ(r.bindings, 0);
  CHECK(!r.Victory());
  CHECK_EQ(Describe(Clamp(20, 15, 6, 3)), std::string("kills 20, bindings 3, minutes 15, start level 6"));
  CHECK_EQ(Describe(Clamp(0, 0, 0, 1)), std::string("bindings 1"));
}

// First Binding wins: the opening line, no match point (a race of 1 starts at it), the end "bindings" whatever the
// kills.
GMS_TEST(MapRulesBindingRaceOfOne) {
  Race race(R(0, 0, 0, 1));
  Seen s;
  RunB(race, 0.0, 60.0, 4, 0, 0, 0, &s);
  std::vector<std::string> want = {"Rules_BindRaceTo1@7"};
  CHECK(s.lines == want);
  CHECK(s.last.end == Why::kNone);
  StepResult r = race.Step(61.0, 4, 0, 0, 1);
  CHECK(r.end == Why::kBindings && r.winner == 2);
  CHECK(race.Ended());
  r = race.Step(62.0, 9, 0, 3, 1);
  CHECK(r.end == Why::kNone && r.sends.empty());
}

// A race of 3: the match point once per team (ally and enemy lines, the spectators left out), the end at 3; two teams
// at the target in one tick: the higher count; a tie there goes to sudden death (the next Binding or kill).
GMS_TEST(MapRulesBindingRaceOfThree) {
  {
    Race race(R(0, 0, 0, 3));
    std::vector<Send> sends;
    auto step = [&](double t, int b1, int b2) {
      StepResult r = race.Step(t, 0, 0, b1, b2);
      sends.insert(sends.end(), r.sends.begin(), r.sends.end());
      return r;
    };
    step(16.0, 0, 0);
    CHECK(sends.size() == 1 && sends[0].line == Line::kBindRaceTo3 && sends[0].teamBits == kEveryone);
    step(30.0, 1, 0);
    CHECK_EQ(sends.size(), static_cast<size_t>(1));
    step(40.0, 2, 0);  // team 1 at match point
    CHECK_EQ(sends.size(), static_cast<size_t>(3));
    if (sends.size() == 3) {
      CHECK(sends[1].line == Line::kBindMatchPointAlly && sends[1].teamBits == 2u &&
            sends[1].excludePlayers == kSpectatorBits);
      CHECK(sends[2].line == Line::kBindMatchPointEnemy && sends[2].teamBits == 4u &&
            sends[2].excludePlayers == kSpectatorBits);
    }
    step(50.0, 2, 2);  // team 2 too, once
    for (int t = 51; t < 120; ++t) step(t, 2, 2);
    CHECK_EQ(sends.size(), static_cast<size_t>(5));
    if (sends.size() == 5) CHECK(sends[3].line == Line::kBindMatchPointAlly && sends[3].teamBits == 4u);
    StepResult r = step(120.0, 3, 2);
    CHECK(r.end == Why::kBindings && r.winner == 1);
  }
  {
    Race race(R(0, 0, 0, 2));
    StepResult r = race.Step(100.0, 0, 5, 3, 2);  // both over it: the higher count; kills don't matter
    CHECK(r.end == Why::kBindings && r.winner == 1);
  }
  {
    Race race(R(0, 0, 0, 2));
    race.Step(100.0, 4, 4, 1, 1);
    StepResult r = race.Step(101.0, 4, 4, 2, 2);  // a tie at the target
    CHECK(r.end == Why::kNone && race.SuddenDeath());
    r = race.Step(104.0, 4, 4, 2, 2);
    bool told = false;
    for (const Send& s : r.sends) told = told || s.line == Line::kBindSuddenDeath;
    CHECK(told);
    r = race.Step(130.0, 4, 5, 2, 2);  // the next kill
    CHECK(r.end == Why::kSuddenDeath && r.winner == 2);
  }
}

// The time-out of a Binding race: Bindings, then kills, then sudden death; its lines.
GMS_TEST(MapRulesBindingTimeOut) {
  {
    Race race(R(0, 3, 0, 2));
    Seen s;
    RunB(race, 0.0, 179.5, 0, 0, 0, 0, &s);
    std::vector<std::string> want = {"Rules_BindRaceTo2@7", "Rules_BindTimeLimit@7", "Rules_Left1Min@7",
                                     "Rules_Left30Sec@7"};
    CHECK(s.lines == want);
    StepResult r = race.Step(180.0, 1, 5, 1, 0);  // more Bindings beat more kills
    CHECK(r.end == Why::kTime && r.winner == 1);
  }
  {
    Race race(R(0, 3, 0, 2));
    StepResult r = race.Step(180.0, 4, 2, 1, 1);  // Bindings level: the kills decide
    CHECK(r.end == Why::kTime && r.winner == 1);
  }
  {
    Race race(R(0, 3, 0, 2));
    StepResult r = race.Step(180.0, 2, 2, 1, 1);  // both level: sudden death
    CHECK(r.end == Why::kNone && race.SuddenDeath());
    CHECK(r.sends.size() == 1 && r.sends[0].line == Line::kBindSuddenDeath && r.sends[0].teamBits == kEveryone);
    for (double t = 180.5; t < 400.0; t += 0.5) {
      r = race.Step(t, 2, 2, 1, 1);
      CHECK(r.end == Why::kNone && r.sends.empty());
    }
    r = race.Step(400.0, 3, 2, 1, 2);  // a kill and a Binding in one tick: the Binding decides
    CHECK(r.end == Why::kSuddenDeath && r.winner == 2);
  }
  {
    Race race(R(0, 3, 0, 2));
    race.Step(180.0, 2, 2, 1, 1);
    StepResult r = race.Step(200.0, 2, 2, 2, 1);  // a Binding in sudden death (reaching the target too)
    CHECK(r.end == Why::kSuddenDeath && r.winner == 1);
  }
}

// A kill race and a Binding race together: whichever target is reached first; the Bindings' first when both are
// reached in one tick; a kill tie at its target goes to sudden death, which the next Binding ends even when the
// Bindings were uneven.
GMS_TEST(MapRulesBindingAndKills) {
  {
    Race race(R(5, 0, 0, 3));
    StepResult r = race.Step(100.0, 5, 0, 0, 2);
    CHECK(r.end == Why::kKills && r.winner == 1);
  }
  {
    Race race(R(5, 0, 0, 3));
    StepResult r = race.Step(100.0, 4, 0, 0, 3);
    CHECK(r.end == Why::kBindings && r.winner == 2);
  }
  {
    Race race(R(5, 0, 0, 3));
    StepResult r = race.Step(100.0, 5, 0, 0, 3);
    CHECK(r.end == Why::kBindings && r.winner == 2);
  }
  {
    Race race(R(5, 0, 0, 3));
    race.Step(100.0, 4, 4, 2, 0);
    StepResult r = race.Step(101.0, 5, 5, 2, 0);
    CHECK(r.end == Why::kNone && race.SuddenDeath());
    r = race.Step(110.0, 5, 5, 2, 1);  // team 2's Binding: the next score wins, not the totals
    CHECK(r.end == Why::kSuddenDeath && r.winner == 2);
  }
  {
    Race race(R(20, 15, 0, 3));  // all three: the opening lines in order, kLineGapS apart
    std::vector<std::pair<double, Line>> got;
    for (double t = 0.0; t < 30.0; t += 0.25) {
      StepResult r = race.Step(t, 0, 0, 0, 0);
      for (const Send& s : r.sends) got.push_back({t, s.line});
    }
    CHECK_EQ(got.size(), static_cast<size_t>(3));
    if (got.size() == 3) {
      CHECK(got[0].second == Line::kRaceTo20);
      CHECK(got[1].second == Line::kBindRaceTo3);
      CHECK(got[2].second == Line::kBindTimeLimit);
      CHECK(got[2].first - got[1].first >= kLineGapS);
    }
  }
  {
    Race race(R(0, 30, 0, 3));  // the lead goes by Bindings first
    for (double t = 0.0; t <= 30.0; t += 1.0) race.Step(t, 0, 0, 0, 0);  // both opening lines out
    const StepResult r = race.Step(40.0, 5, 0, 0, 1);
    CHECK_EQ(r.sends.size(), static_cast<size_t>(2));
    if (r.sends.size() == 2) CHECK(r.sends[0].line == Line::kLeadAlly && r.sends[0].teamBits == 4u);
  }
}

// ---- WINLOBBY: the rules' sources (createGame, startGame, the test overrides) --------------------------------------

// startGame's victory (the lobby's win condition) replaces createGame's victory and keeps its start level, in either
// order: Poll handles a startGame before a createGame read in the same frame.
GMS_TEST(MapRulesSourcesOrder) {
  {
    Sources s;  // createGame first, then startGame
    s.FromCreate(R(20, 15, 6, 0));
    s.FromStart(R(0, 0, 9, 1));  // startGame carries no start level: 9 is ignored
    CHECK_EQ(s.Current().kills, 0);
    CHECK_EQ(s.Current().minutes, 0);
    CHECK_EQ(s.Current().bindings, 1);
    CHECK_EQ(s.Current().startLevel, 6);
    CHECK(s.StartSet());
  }
  {
    Sources s;  // startGame first: createGame's victory never replaces it, its start level is taken
    s.FromStart(R(20, 20, 0, 0));
    s.FromCreate(R(5, 0, 6, 3));
    CHECK_EQ(s.Current().kills, 20);
    CHECK_EQ(s.Current().minutes, 20);
    CHECK_EQ(s.Current().bindings, 0);
    CHECK_EQ(s.Current().startLevel, 6);
  }
  {
    Sources s;  // createGame alone (a map's rules, no lobby victory)
    s.FromCreate(R(2, 4, 6, 0));
    CHECK_EQ(s.Current().kills, 2);
    CHECK_EQ(s.Current().minutes, 4);
    CHECK_EQ(s.Current().startLevel, 6);
    CHECK(!s.StartSet());
  }
  {
    Sources s;  // the standard map and the lobby's "First Binding"
    s.FromCreate(R(0, 0, 0, 0));
    s.FromStart(R(0, 0, 0, 1));
    CHECK(s.Current().Victory());
    CHECK_EQ(s.Current().bindings, 1);
  }
}

// The [host] test overrides win over both messages, member by member.
GMS_TEST(MapRulesSourcesOverride) {
  Sources s;
  std::string note;
  s.Override(0, 3, 0, 0, &note);  // [host] map_minutes=3
  CHECK(s.AnyOverride() && s.VictoryOverride());
  s.FromCreate(R(20, 15, 6, 0));
  s.FromStart(R(10, 30, 0, 1));
  CHECK_EQ(s.Current().kills, 10);
  CHECK_EQ(s.Current().minutes, 3);
  CHECK_EQ(s.Current().bindings, 1);
  CHECK_EQ(s.Current().startLevel, 6);
  Sources none;
  none.Override(0, 0, 0, 0, &note);
  CHECK(!none.AnyOverride());
  Sources level;
  level.Override(0, 0, 8, 0, &note);  // [host] start_level=8: createGame's level doesn't replace it
  CHECK(level.AnyOverride() && !level.VictoryOverride());
  level.FromCreate(R(0, 0, 6, 0));
  CHECK_EQ(level.Current().startLevel, 8);
}
