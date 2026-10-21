// gms_tests: BOTDRAFT, a custom game's bots draw their shapers and roles after the humans (core/bots/draft.h).
#include <algorithm>
#include <map>
#include <set>
#include <string>
#include <vector>

#include "core/bots/draft.h"
#include "tests/check.h"

using namespace gms::bots;

namespace {

std::set<std::string> Everything() {
  const std::vector<std::string> c = CatalogCandidates();
  return std::set<std::string>(c.begin(), c.end());
}

std::vector<DraftTeam> TwoTeams(int a, int b) { return {DraftTeam{1, a}, DraftTeam{2, b}}; }

DraftHuman Human(int index, int team, const std::string& cls, int role) {
  DraftHuman h;
  h.index = index;
  h.team = team;
  h.baseClass = cls;
  h.role = role;
  return h;
}

int Count(const std::vector<ComposedSeat>& seats, int role) {
  int n = 0;
  for (const ComposedSeat& s : seats) n += s.role == role ? 1 : 0;
  return n;
}

bool HasBase(const std::vector<ComposedSeat>& seats, const std::string& base) {
  for (const ComposedSeat& s : seats)
    if (BaseClass(s.botClass) == base) return true;
  return false;
}

bool DistinctBases(const std::vector<ComposedSeat>& seats) {
  std::set<std::string> b;
  for (const ComposedSeat& s : seats)
    if (!b.insert(BaseClass(s.botClass)).second) return false;
  return true;
}

DraftOptions Opts(unsigned seed) {
  DraftOptions o;
  o.seed = seed;
  o.hunters = ParseHunterList("default");
  return o;
}

}  // namespace

GMS_TEST(DraftHumanIntentOrder) {
  int src = -1;
  // the preferred-role boxes first; several: the scarcest role
  CHECK_EQ(HumanIntent((1u << kRoleGladiator) | (1u << kRoleHunter), 1, kRoleTactician, &src), kRoleHunter);
  CHECK_EQ(src, static_cast<int>(kIntentPreferred));
  CHECK_EQ(HumanIntent(1u << kRoleTactician, kRoleHunter, kRoleGladiator), kRoleTactician);
  // then the role card, then the shaper's usual role
  CHECK_EQ(HumanIntent(0, kRolePredator, kRoleGladiator, &src), kRolePredator);
  CHECK_EQ(src, static_cast<int>(kIntentRoleCard));
  CHECK_EQ(HumanIntent(0, -1, TypicalRole("PC_Artificer"), &src), kRoleHunter);
  CHECK_EQ(src, static_cast<int>(kIntentTypical));
  CHECK_EQ(HumanIntent(0, -1, TypicalRole("PC_NoSuchShaper"), &src), kRoleUnknown);
  CHECK_EQ(src, static_cast<int>(kIntentNone));
  // every playable shaper has a usual role (35, the guides' top role)
  for (const char* c : {"PC_Otter", "PC_Survivor", "PC_HuTank", "PC_Raider", "PC_Penitent"}) CHECK_EQ(TypicalRole(c), kRoleHunter);
  CHECK_EQ(TypicalRole("PC_HighHuntress"), kRoleGladiator);
  CHECK_EQ(TypicalRole("PC_Guardian"), kRoleTactician);
}

// The user's case: a human Hunter (Petrus) gets no bot Hunter on his team, and no bot Petrus anywhere on it.
GMS_TEST(DraftHumanHunterGetsNoBotHunter) {
  for (unsigned seed = 1; seed <= 50; ++seed) {
    const auto drawn =
        DraftMatch(TwoTeams(4, 5), {Human(0, 1, "PC_Artificer", kRoleHunter)}, Everything(), Opts(seed));
    const std::vector<ComposedSeat>& mine = drawn.at(1);
    const std::vector<ComposedSeat>& theirs = drawn.at(2);
    CHECK_EQ(mine.size(), size_t{4});
    CHECK_EQ(theirs.size(), size_t{5});
    CHECK_EQ(Count(mine, kRoleHunter), 0);
    CHECK(!HasBase(mine, "PC_Artificer"));
    CHECK_EQ(Count(mine, kRoleGladiator), 2);
    CHECK_EQ(Count(theirs, kRoleHunter), 1);
    CHECK_EQ(Count(theirs, kRoleGladiator), 2);
    CHECK(Count(mine, kRolePredator) <= 1 && Count(theirs, kRolePredator) <= 1);
    CHECK(DistinctBases(mine));
    CHECK(DistinctBases(theirs));
    // the other team's Hunter avoids the human's Petrus while Freia is free
    CHECK(!HasBase(theirs, "PC_Artificer"));
  }
}

// A human Gladiator (Nissa): one bot Gladiator on his team (not Nissa), a Hunter and two flex seats.
GMS_TEST(DraftHumanGladiator) {
  for (unsigned seed = 1; seed <= 50; ++seed) {
    const auto drawn =
        DraftMatch(TwoTeams(4, 5), {Human(0, 1, "PC_HighHuntress", kRoleGladiator)}, Everything(), Opts(seed));
    const std::vector<ComposedSeat>& mine = drawn.at(1);
    CHECK_EQ(Count(mine, kRoleGladiator), 1);
    CHECK_EQ(Count(mine, kRoleHunter), 1);
    CHECK_EQ(Count(mine, kRoleTactician) + Count(mine, kRolePredator), 2);
    CHECK(!HasBase(mine, "PC_HighHuntress"));
    // the remaining Gladiator goes bottom (the human's lane is free to choose; the bot's seat is the first one's)
    CHECK(DistinctBases(mine));
  }
}

// Two humans on one team (a Hunter and a Tactician): three bots, two Gladiators and one flex seat, no Hunter.
GMS_TEST(DraftTwoHumansOneTeam) {
  const auto drawn = DraftMatch(TwoTeams(3, 5),
                                {Human(0, 1, "PC_Survivor", kRoleHunter), Human(1, 1, "PC_Guardian", kRoleTactician)},
                                Everything(), Opts(7));
  const std::vector<ComposedSeat>& mine = drawn.at(1);
  CHECK_EQ(mine.size(), size_t{3});
  CHECK_EQ(Count(mine, kRoleGladiator), 2);
  CHECK_EQ(Count(mine, kRoleHunter), 0);
  CHECK(!HasBase(mine, "PC_Guardian"));
  CHECK(!HasBase(mine, "PC_Survivor"));
}

// A human of unknown role (a shaper without a usual role): the flex seat gives way, as in the practice draw.
GMS_TEST(DraftUnknownHumanDropsAFlexSeat) {
  const auto drawn = DraftMatch(TwoTeams(4, 5), {Human(0, 1, "", kRoleUnknown)}, Everything(), Opts(3));
  const std::vector<ComposedSeat>& mine = drawn.at(1);
  CHECK_EQ(mine.size(), size_t{4});
  CHECK_EQ(Count(mine, kRoleGladiator), 2);
  CHECK_EQ(Count(mine, kRoleHunter), 1);
}

// The shuffle uses every class in the pools over many games: the 8 AI classes and the profiles (Freia, the Predator
// Warden, Desecrator, Mikella); Faris only with bot_hunters=all.
GMS_TEST(DraftShuffleUsesEveryClass) {
  std::set<std::string> seen;
  for (unsigned seed = 1; seed <= 300; ++seed) {
    const auto drawn = DraftMatch(TwoTeams(4, 5), {Human(0, 1, "PC_Alpha", kRoleUnknown)}, Everything(), Opts(seed));
    for (const auto& kv : drawn)
      for (const ComposedSeat& s : kv.second) seen.insert(s.botClass);
  }
  for (const char* c : {"PC_HighHuntress", "PC_Creature", "PC_MerchantPrincess_Gladiator", "PC_Guardian", "PC_Artist",
                        "PC_TidalQueen", "PC_Sculptor", "PC_Warden", "PC_Desecrator_Tactician", "PC_Warden_Predator",
                        "PC_Artificer", "PC_Survivor_Hunter"})
    CHECK(seen.count(c) == 1);
  CHECK(seen.count("PC_Golem_Hunter") == 0);
  DraftOptions all = Opts(5);
  all.hunters = ParseHunterList("all");
  bool faris = false;
  for (unsigned seed = 1; seed <= 60 && !faris; ++seed) {
    all.seed = seed;
    for (const auto& kv : DraftMatch(TwoTeams(5, 5), {}, Everything(), all))
      faris = faris || HasBase(kv.second, "PC_Golem");
  }
  CHECK(faris);
}

// The pins stay overrides: each team's first Tactician, Gladiator and Hunter seat takes its pin unless a human of the
// team holds that shaper.
GMS_TEST(DraftPinsAreOverrides) {
  DraftOptions o = Opts(11);
  o.tacticians = ParseTacticianList("desecrator");
  o.gladiators = ParseGladiatorList("mikella");
  o.hunters = ParseHunterList("freia");
  o.huntersPinned = true;
  const auto drawn = DraftMatch(TwoTeams(4, 5), {Human(0, 1, "PC_Desecrator", kRoleUnknown)}, Everything(), o);
  const std::vector<ComposedSeat>& mine = drawn.at(1);
  const std::vector<ComposedSeat>& theirs = drawn.at(2);
  CHECK(!HasBase(mine, "PC_Desecrator"));  // the human holds it
  CHECK_EQ(theirs[1].botClass, std::string("PC_Desecrator_Tactician"));
  CHECK_EQ(theirs[0].botClass, std::string("PC_MerchantPrincess_Gladiator"));
  CHECK_EQ(mine[0].botClass, std::string("PC_MerchantPrincess_Gladiator"));
  CHECK(HasBase(theirs, "PC_Survivor"));
  CHECK(HasBase(mine, "PC_Survivor"));
  // a human holding the pinned Hunter: the team draws the other Hunter
  const auto drawn2 = DraftMatch(TwoTeams(4, 5), {Human(0, 1, "PC_Survivor", kRoleGladiator)}, Everything(), o);
  CHECK(!HasBase(drawn2.at(1), "PC_Survivor"));
  CHECK_EQ(Count(drawn2.at(1), kRoleHunter), 1);
}

// A copy without the profiles: the draw keeps to the shipped classes, the Predator on PC_Warden with the role set.
GMS_TEST(DraftWithoutProfiles) {
  std::set<std::string> shipped = {"PC_HighHuntress", "PC_Creature", "PC_Guardian", "PC_Artist",
                                   "PC_TidalQueen",   "PC_Sculptor", "PC_Warden",   "PC_Artificer"};
  bool predator = false;
  for (unsigned seed = 1; seed <= 80; ++seed) {
    const auto drawn = DraftMatch(TwoTeams(4, 5), {Human(0, 1, "PC_Alpha", kRoleGladiator)}, shipped, Opts(seed));
    for (const auto& kv : drawn) {
      CHECK(DistinctBases(kv.second));
      for (const ComposedSeat& s : kv.second) {
        CHECK(shipped.count(s.botClass) == 1);
        if (s.role == kRolePredator) {
          predator = true;
          CHECK(s.roleOverride);
        }
      }
    }
    CHECK_EQ(drawn.at(2).size(), size_t{5});
  }
  CHECK(predator);
}

// BOTDRAFT-PRACTICE: [host] bot_draft -1 (auto) takes every game dgserver makes (custom, practice, the PvP queue's);
// 0 never, 1 always; a host without createGame (mode "") or an unknown mode keeps the composed bots.
GMS_TEST(DraftOnByModeAndOption) {
  CHECK(DraftOn(-1, "custom"));
  CHECK(DraftOn(-1, "practice"));
  CHECK(DraftOn(-1, "pvp"));
  CHECK(!DraftOn(-1, ""));
  CHECK(!DraftOn(-1, "tutorial"));
  CHECK(!DraftOn(0, "custom"));
  CHECK(!DraftOn(0, "practice"));
  CHECK(!DraftOn(0, "pvp"));
  CHECK(DraftOn(1, ""));
  CHECK(DraftOn(1, "practice"));
}
