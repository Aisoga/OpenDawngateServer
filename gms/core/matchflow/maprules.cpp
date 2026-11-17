// MAPRULES4: a map's victory rules and start level, engine-free (maprules.h).
#include "maprules.h"

#include <algorithm>
#include <cstdio>

namespace gms {
namespace maprules {
namespace {

struct LineRow {
  const char* def;
  const char* text;
};

// The defs and the en-us lines, in LabsAlerts id order (Go botprofiles.MapRulesFeedbackDefs and
// localepatch.LabsAlertsLines hold the same table; each line is at most 45 characters).
const LineRow kLines[kLineCount] = {
    {"Rules_Race10Ally", "Your team needs 10 more kills!"},
    {"Rules_Race10Enemy", "The enemy needs 10 more kills!"},
    {"Rules_Race5Ally", "Your team needs 5 more kills!"},
    {"Rules_Race5Enemy", "The enemy needs 5 more kills!"},
    {"Rules_Race3Ally", "Your team needs 3 more kills!"},
    {"Rules_Race3Enemy", "The enemy needs 3 more kills!"},
    {"Rules_MatchPointAlly", "Match point: one more kill wins!"},
    {"Rules_MatchPointEnemy", "The enemy is one kill from victory!"},
    {"Rules_RaceTo10", "First team to 10 kills wins!"},
    {"Rules_RaceTo15", "First team to 15 kills wins!"},
    {"Rules_RaceTo20", "First team to 20 kills wins!"},
    {"Rules_RaceTo25", "First team to 25 kills wins!"},
    {"Rules_RaceTo30", "First team to 30 kills wins!"},
    {"Rules_RaceTo50", "First team to 50 kills wins!"},
    {"Rules_RaceToTarget", "First team to the kill target wins!"},
    {"Rules_TimeLimit", "Most kills when time runs out wins!"},
    {"Rules_LeadAlly", "Your team takes the lead!"},
    {"Rules_LeadEnemy", "The enemy takes the lead!"},
    {"Rules_Left10Min", "10 minutes left!"},
    {"Rules_Left5Min", "5 minutes left!"},
    {"Rules_Left1Min", "1 minute left!"},
    {"Rules_Left30Sec", "30 seconds left!"},
    {"Rules_SuddenDeath", "Sudden death: the next kill wins!"},
    // WINBIND-B (0x5e70e027-)
    {"Rules_BindRaceTo1", "First team to destroy a Binding wins!"},
    {"Rules_BindRaceTo2", "First team to destroy 2 Bindings wins!"},
    {"Rules_BindRaceTo3", "First team to destroy 3 Bindings wins!"},
    {"Rules_BindRaceTo6", "First team to destroy 6 Bindings wins!"},
    {"Rules_BindRaceToTarget", "First team to the Binding target wins!"},
    {"Rules_BindMatchPointAlly", "Match point: one more Binding wins!"},
    {"Rules_BindMatchPointEnemy", "The enemy is one Binding from victory!"},
    {"Rules_BindTimeLimit", "Most Bindings when time runs out wins!"},
    {"Rules_BindSuddenDeath", "Sudden death: next Binding or kill wins!"},
};

std::string g_defNames[kLineCount];

// The race's milestones: kills still needed, with the team's own line (the enemy's follows it in Line).
struct Milestone {
  int remaining;
  Line ally;
};
constexpr Milestone kMilestones[] = {
    {10, Line::kRace10Ally}, {5, Line::kRace5Ally}, {3, Line::kRace3Ally}, {1, Line::kMatchPointAlly}};
constexpr int kMilestoneCount = static_cast<int>(sizeof kMilestones / sizeof kMilestones[0]);

// The countdown: seconds left, the line.
struct Countdown {
  double left;
  Line line;
};
constexpr Countdown kCountdowns[] = {
    {600.0, Line::kLeft10Min}, {300.0, Line::kLeft5Min}, {60.0, Line::kLeft1Min}, {30.0, Line::kLeft30Sec}};

Line Enemy(Line ally) { return static_cast<Line>(static_cast<int>(ally) + 1); }

int ClampInt(long long v, int hi) { return v < 0 ? 0 : v > hi ? hi : static_cast<int>(v); }

}  // namespace

Rules Clamp(long long kills, long long minutes, long long startLevel, long long bindings, std::string* note) {
  Rules r;
  r.kills = ClampInt(kills, kMaxKills);
  r.bindings = ClampInt(bindings, kMaxBindings);
  r.minutes = ClampInt(minutes, kMaxMinutes);
  r.startLevel = ClampInt(startLevel, kMaxStartLevel);
  if (r.startLevel == 1) r.startLevel = 0;
  if (note) {
    note->clear();
    char b[160];
    if (r.kills != kills) {
      std::snprintf(b, sizeof b, "%skills %lld -> %d", note->empty() ? "" : ", ", kills, r.kills);
      *note += b;
    }
    if (r.minutes != minutes) {
      std::snprintf(b, sizeof b, "%sminutes %lld -> %d", note->empty() ? "" : ", ", minutes, r.minutes);
      *note += b;
    }
    if (r.startLevel != startLevel && !(startLevel == 1 && r.startLevel == 0)) {
      std::snprintf(b, sizeof b, "%sstartLevel %lld -> %d", note->empty() ? "" : ", ", startLevel, r.startLevel);
      *note += b;
    }
    if (r.bindings != bindings) {
      std::snprintf(b, sizeof b, "%sbindings %lld -> %d", note->empty() ? "" : ", ", bindings, r.bindings);
      *note += b;
    }
  }
  return r;
}

std::string Describe(const Rules& r) {
  std::string s;
  char b[48];
  if (r.kills > 0) {
    std::snprintf(b, sizeof b, "kills %d", r.kills);
    s += b;
  }
  if (r.bindings > 0) {
    std::snprintf(b, sizeof b, "%sbindings %d", s.empty() ? "" : ", ", r.bindings);
    s += b;
  }
  if (r.minutes > 0) {
    std::snprintf(b, sizeof b, "%sminutes %d", s.empty() ? "" : ", ", r.minutes);
    s += b;
  }
  if (r.startLevel > 1) {
    std::snprintf(b, sizeof b, "%sstart level %d", s.empty() ? "" : ", ", r.startLevel);
    s += b;
  }
  return s.empty() ? "none" : s;
}

const char* DefName(Line l) {
  const int i = static_cast<int>(l);
  if (i < 0 || i >= kLineCount) return "";
  if (g_defNames[i].empty()) g_defNames[i] = std::string(kLines[i].def) + ".ClientFeedbackDef";
  return g_defNames[i].c_str();
}

const char* Text(Line l) {
  const int i = static_cast<int>(l);
  return i < 0 || i >= kLineCount ? "" : kLines[i].text;
}

Line OpeningLine(int kills) {
  switch (kills) {
    case 10: return Line::kRaceTo10;
    case 15: return Line::kRaceTo15;
    case 20: return Line::kRaceTo20;
    case 25: return Line::kRaceTo25;
    case 30: return Line::kRaceTo30;
    case 50: return Line::kRaceTo50;
    default: return Line::kRaceToTarget;
  }
}

Line BindOpeningLine(int bindings) {
  switch (bindings) {
    case 1: return Line::kBindRaceTo1;
    case 2: return Line::kBindRaceTo2;
    case 3: return Line::kBindRaceTo3;
    case 6: return Line::kBindRaceTo6;
    default: return Line::kBindRaceToTarget;
  }
}

const char* FinishReason(Why w) {
  switch (w) {
    case Why::kKills: return "kills";
    case Why::kTime: return "time";
    case Why::kSuddenDeath: return "suddenDeath";
    case Why::kBindings: return "bindings";
    default: return "";
  }
}

Race::Race(const Rules& r) : rules_(r) {
  // a milestone the race starts within is passed already (a race of 3 has no "3 more" line; its opening says it)
  for (int team = 0; team < 2; ++team)
    while (rules_.kills > 0 && nextMilestone_[team] < kMilestoneCount &&
           rules_.kills <= kMilestones[nextMilestone_[team]].remaining)
      ++nextMilestone_[team];
  const double limit = rules_.minutes * 60.0;
  for (int i = 0; i < 4; ++i) countdownDone_[i] = rules_.minutes <= 0 || limit - kCountdowns[i].left <= 0.0;
  // the Binding race's only milestone, the match point, needs a race of 2 or more
  bindPointDone_[0] = bindPointDone_[1] = rules_.bindings < 2;
}

int Race::Leader(const int k[2], const int b[2]) const {
  if (rules_.bindings > 0 && b[0] != b[1]) return b[0] > b[1] ? 1 : 2;
  return k[0] > k[1] ? 1 : k[1] > k[0] ? 2 : 0;
}

void Race::StartSudden(const int k[2], const int b[2]) {
  sudden_ = true;
  queue_.clear();  // what was still waiting is old news
  QueueNeutral(rules_.bindings > 0 ? Line::kBindSuddenDeath : Line::kSuddenDeath, true);
  for (int i = 0; i < 2; ++i) {
    suddenKills_[i] = k[i];
    suddenBindings_[i] = b[i];
  }
}

int Race::SuddenWinner(const int k[2], const int b[2]) const {
  if (rules_.bindings > 0) {
    const int db = (b[0] - suddenBindings_[0]) - (b[1] - suddenBindings_[1]);
    if (db != 0) return db > 0 ? 1 : 2;
  }
  const int dk = (k[0] - suddenKills_[0]) - (k[1] - suddenKills_[1]);
  return dk == 0 ? 0 : dk > 0 ? 1 : 2;
}

void Race::Queue(const Item& it, bool front) {
  if (front) queue_.insert(queue_.begin(), it);
  else queue_.push_back(it);
}

void Race::QueuePair(Line ally, Line enemy, int team) {
  Item it;
  it.s[0] = Send{ally, 1u << team, kSpectatorBits};
  it.s[1] = Send{enemy, 1u << (3 - team), kSpectatorBits};
  it.n = 2;
  Queue(it);
}

void Race::QueueNeutral(Line l, bool front) {
  Item it;
  it.s[0] = Send{l, kEveryone, 0};
  it.n = 1;
  Queue(it, front);
}

StepResult Race::Step(double t, int kills1, int kills2, int bindings1, int bindings2) {
  StepResult out;
  if (ended_ || !rules_.Victory()) return out;
  const int k[2] = {kills1, kills2};
  const int b[2] = {bindings1, bindings2};
  auto end = [&](Why why, int winner) {
    ended_ = true;
    queue_.clear();
    out.end = why;
    out.winner = winner;
    return out;
  };
  const double limit = rules_.minutes * 60.0;
  if (sudden_) {
    // sudden death: the first team to score since it began (a Binding, in a Binding race, or a kill)
    const int w = SuddenWinner(k, b);
    if (w != 0) return end(Why::kSuddenDeath, w);
  } else {
    // the ends: a team at the Binding target, then at the kill target (the higher count wins; a tie at a target goes
    // to sudden death), then the time limit's leader (Bindings, then kills; level: sudden death)
    bool tie = false;
    if (rules_.bindings > 0 && (bindings1 >= rules_.bindings || bindings2 >= rules_.bindings)) {
      if (bindings1 != bindings2) return end(Why::kBindings, bindings1 > bindings2 ? 1 : 2);
      tie = true;
    }
    if (!tie && rules_.kills > 0 && (kills1 >= rules_.kills || kills2 >= rules_.kills)) {
      if (kills1 != kills2) return end(Why::kKills, kills1 > kills2 ? 1 : 2);
      tie = true;
    }
    if (!tie && rules_.minutes > 0 && t >= limit) {
      const int w = Leader(k, b);
      if (w != 0) return end(Why::kTime, w);
      tie = true;
    }
    if (tie) StartSudden(k, b);
  }
  // the opening lines
  if (!opened_ && t >= kOpeningS) {
    opened_ = true;
    if (rules_.kills > 0 && !sudden_) QueueNeutral(OpeningLine(rules_.kills));
    if (rules_.bindings > 0 && !sudden_) QueueNeutral(BindOpeningLine(rules_.bindings));
    if (rules_.minutes > 0 && !sudden_)
      QueueNeutral(rules_.bindings > 0 ? Line::kBindTimeLimit : Line::kTimeLimit);
  }
  if (!sudden_) {
    // the race's milestones: the smallest one each team reached this tick (several at once: only that one)
    for (int team = 0; team < 2 && rules_.kills > 0; ++team) {
      const int remaining = rules_.kills - k[team];
      int reached = -1;
      while (nextMilestone_[team] < kMilestoneCount && remaining <= kMilestones[nextMilestone_[team]].remaining)
        reached = nextMilestone_[team]++;
      if (reached >= 0) QueuePair(kMilestones[reached].ally, Enemy(kMilestones[reached].ally), team + 1);
    }
    // the Binding race's match point, once per team
    for (int team = 0; team < 2 && rules_.bindings > 0; ++team) {
      if (bindPointDone_[team] || rules_.bindings - b[team] > 1) continue;
      bindPointDone_[team] = true;
      QueuePair(Line::kBindMatchPointAlly, Line::kBindMatchPointEnemy, team + 1);
    }
    // the countdown
    for (int i = 0; i < 4; ++i) {
      if (countdownDone_[i] || t < limit - kCountdowns[i].left) continue;
      for (int j = i; j < 4; ++j)  // a late tick passes the earlier marks without their lines
        if (t >= limit - kCountdowns[j].left) countdownDone_[j] = true;
      int last = i;
      for (int j = i; j < 4; ++j)
        if (t >= limit - kCountdowns[j].left) last = j;
      QueueNeutral(kCountdowns[last].line);
      break;
    }
    // a new leader (time limit only; Bindings first in a Binding race)
    const int leader = Leader(k, b);
    if (rules_.minutes > 0 && leader != 0 && leader != leadAnnounced_ && (leadAt_ < 0.0 || t - leadAt_ >= kLeadGapS)) {
      bool queued = false;
      for (const Item& it : queue_) queued = queued || it.leadTeam != 0;
      if (!queued) {
        Item it;
        it.s[0] = Send{Line::kLeadAlly, 1u << leader, kSpectatorBits};
        it.s[1] = Send{Line::kLeadEnemy, 1u << (3 - leader), kSpectatorBits};
        it.n = 2;
        it.leadTeam = leader;
        Queue(it);
        leadAnnounced_ = leader;
        leadAt_ = t;
      }
    }
  }
  // one line every kLineGapS
  while (!queue_.empty() && t - lastLineAt_ >= kLineGapS) {
    const Item it = queue_.front();
    queue_.erase(queue_.begin());
    if (it.leadTeam != 0 && (Leader(k, b) != it.leadTeam || sudden_)) {
      leadAnnounced_ = 0;  // that lead is gone already: the next one is told again
      continue;
    }
    for (int i = 0; i < it.n; ++i) out.sends.push_back(it.s[i]);
    lastLineAt_ = t;
  }
  return out;
}

std::vector<float> LevelSteps(const float* xpRequired, int n, int from, int to) {
  std::vector<float> steps;
  if (!xpRequired || n < 2) return steps;
  const int last = n - 1;
  to = std::min(to, last);
  for (int l = std::max(from, 1); l < to; ++l) {
    const float s = xpRequired[l + 1] - xpRequired[l];
    steps.push_back(s > 0.f ? s : 0.f);
  }
  return steps;
}

float CumulativeXp(const float* xpRequired, int n, int level) {
  if (!xpRequired || n < 2) return 0.f;
  const int l = std::min(std::max(level, 1), n - 1);
  return xpRequired[l];
}

void Sources::Override(long long kills, long long minutes, long long startLevel, long long bindings,
                       std::string* note) {
  overKills_ = kills > 0;
  overMinutes_ = minutes > 0;
  overLevel_ = startLevel > 0;
  overBindings_ = bindings > 0;
  if (AnyOverride()) rules_ = Clamp(kills, minutes, startLevel, bindings, note);
}

void Sources::Victory(const Rules& r) {
  if (!overKills_) rules_.kills = r.kills;
  if (!overMinutes_) rules_.minutes = r.minutes;
  if (!overBindings_) rules_.bindings = r.bindings;
}

void Sources::FromCreate(const Rules& r) {
  if (!fromStart_) Victory(r);  // a lobby's victory that came first stays
  if (!overLevel_) rules_.startLevel = r.startLevel;
}

void Sources::FromStart(const Rules& r) {
  fromStart_ = true;
  Victory(r);
}

}  // namespace maprules
}  // namespace gms
