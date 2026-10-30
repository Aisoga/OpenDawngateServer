// M18 (MSTATS1): the match statistics' accumulator, engine-free.
//
// The engine (gms/engine/matchstats.cpp) feeds it from the damage core, the death processing, the death timer, the
// wells and the match's once-a-second step; at the match's end it builds one control line per seat (seatStats, bots
// included) and one matchTimeline (gms/core/control's codec), sent before the playerFinish lines.
//
//   OnHit          one hit of the damage core (after its HP step): damage dealt and taken by type, target and
//                  attacker kind, overload, overkill, the largest hit, shields absorbed
//   OnKill         a death credited to a seat: kills<Kind> (a player victim stays playerFinish's kills; WARDSTAT:
//                  no damageToWards or killsWards, a ward has no combatant in 854 so they could only be 0)
//   OnPlayerKill   a player's creature died: the timeline's kill event and its team's kill count
//   OnDeathTimer   a player's death timer: time dead = min(respawn, the next death, the end) - death
//   OnWellCaptured, OnStructure, OnMatchEnd    the other events (well, structure, guardian or surrender)
//   Sample         the match's once-a-second step: a timeline sample whenever floor(t / 60) changes
//   AddStat, AddSource   the seams for MSTATS2-4's keys and MSTATS3's damage/heal by source
//   OnHeal, OnShieldAbsorb   MSTATS3: the heal core's heals and the shields a seat gave (sources.h for the rows)
//   Close, BuildSeatStats, BuildTimeline   at the end
#pragma once

#include <cstddef>
#include <cstdint>
#include <map>
#include <string>
#include <tuple>
#include <vector>

#include "control/messages.h"
#include "core/stats/sources.h"

namespace gms::stats {

constexpr int kSeats = 10;            // gameplay indexes 0-9 (bots included)
constexpr int kStepSec = 60;          // one timeline sample per match minute
constexpr int kMaxSamples = 180;      // three hours
constexpr int kMaxEvents = 300;
constexpr int kMaxSources = 24;       // seatStats.sources, the largest first (MSTATS3: the rest folded into `other`)
// A control line must stay under the 64-KiB MaxLine (a longer one ends the host's control connection); the timeline is
// trimmed to this size: events first (kills, then wells, structures, the end), then every second sample.
constexpr std::size_t kLineBudget = 48 * 1024;

// The damage descriptors the stats read (gms/core/damage/rules.h has the full set).
constexpr unsigned kDescPhysical = 0x40;
constexpr unsigned kDescMagical = 0x80;
constexpr unsigned kDescTrue = 0x800;
constexpr unsigned kDescOverload = 0x100000;      // IsSpellOverload: the overload hit re-enters the core
constexpr unsigned kDescSelfInflicted = 0x400000;

// Target kinds (the seat lines' by-target keys, stats.cpp): player-controlled = Shapers; else by nNPCType.
enum class Kind : int {
  Shapers,
  Minions,     // 0-2
  Jungle,      // 3
  Objectives,  // 6-8: the buff creatures and the parasite
  Structures,  // 9 Tower (Binding), 14 Nub (Guardian core), 15 Locus
  Guardian,    // 13
  Wells,       // 10 GoldMine
  Workers,     // 12 Follower
  Pets,        // 11 PlayerPet
  Wards,       // 16
  Other,       // anything else (no NPC data, an unknown type)
  Count
};
Kind KindOf(bool playerControlled, int npcType);
const char* KindName(Kind k);  // "Shapers", "Minions", ... (the keys' suffix)

// One hit as the damage core saw it.
struct Hit {
  int attackerSeat = -1;  // the credited seat (snapshot +0x0E, else +0x0D); -1 none (a minion, a tower, a creep)
  int targetSeat = -1;    // the seat whose creature (player-controlled) was hit; -1 none
  Kind targetKind = Kind::Other;
  bool snapshot = true;   // false: a health cost (no snapshot): never counted
  bool sameTeam = false;  // the snapshot's team is the target's: never counted
  unsigned descLo = 0;
  float hpBefore = 0.f, hpAfter = 0.f;
  float dealt = 0.f;     // the damage after shields (may exceed the HP left)
  float absorbed = 0.f;  // what the target's shields absorbed
  bool lethal = false;
  Source source;  // MSTATS3: the row the dealt part goes to (sources.h Classify)
};

// MSTATS3: one call of the heal core (GameObject_HealDamage 0x008E0720) as the detour saw it. hpBefore/hpAfter are
// this call's own part (a nested overload heal's gain taken out by the engine).
struct HealEvent {
  bool snapshot = true;       // false: a script heal without a snapshot (healingReceived only)
  int healerSeat = -1;        // the snapshot's credited seat (+0x0E, else +0x0D); -1 none
  int targetSeat = -1;        // the seat whose creature (player-controlled) was healed; -1 none
  bool targetPlayer = false;  // the target is player-controlled (an ally shaper when not the healer's own)
  bool sameTeam = true;       // the snapshot's team is the target's (an enemy heal counts for nobody's healingDone)
  bool lifesteal = false;     // the core's isLifesteal: the host's lifedrain (damage.cpp step 13)
  float healed = -1.f;        // the core's *healed (the heal before the HP cap); < 0 unknown
  float hpBefore = 0.f, hpAfter = 0.f;
  Source source;
};

// What the end needs of one seat (the engine reads it from the cLabsPlayer).
struct SeatFacts {
  bool present = false;
  bool bot = false;
  std::int64_t user = 0;  // the Blaze user id (0 for a bot)
  int team = 0;
  std::int64_t noun = 0;
  int role = 0;  // as playerFinish's: 1 + mRole
  int kills = 0, deaths = 0, assists = 0, creeps = 0, level = 0;
  std::int64_t gold = 0;  // mTotalGold, rounded
};

// One seat's state at a timeline sample.
struct SeatSample {
  bool present = false;
  int team = 0;
  double gold = 0.0, xp = 0.0;  // mTotalGold, mExperience
  int level = 0, cs = 0;
};

// The trimming BuildTimeline did to fit the line budget.
struct TrimReport {
  int eventsDropped = 0;   // by the budget
  int eventsCapped = 0;    // past kMaxEvents while counting
  int coarsened = 0;       // times every second sample was dropped
  std::size_t bytes = 0;   // the line's size, newline included
};

class Accumulator {
 public:
  Accumulator();

  void OnHit(const Hit& h);
  // A death credited to `creditSeat` (-1 none). Kills of the seat's own team aren't counted.
  void OnKill(int creditSeat, int creditTeam, Kind victimKind, int victimTeam);
  // A player's creature died: killerSeat -1 = no player credited (a tower, a minion). The killer's team gets the kill.
  void OnPlayerKill(std::int64_t ms, int killerSeat, int killerTeam, int victimSeat);
  // A player's death timer (server clock ms; any clock works if the end uses the same).
  void OnDeathTimer(int seat, std::uint64_t deathMs, std::uint64_t respawnMs);
  void OnWellCaptured(std::int64_t ms, int well, int team, const std::vector<int>& capturers);
  // A structure died (npcType 9, 14, 15); destroyingTeam = the victim's enemy.
  void OnStructure(std::int64_t ms, int destroyingTeam, int npcType);
  // The match's end: guardian {a winner} or, when surrenderTeam is 1 or 2, surrender {a the surrendering team}.
  void OnMatchEnd(std::int64_t ms, int winnerTeam, int surrenderTeam);
  // The once-a-second step: a sample when floor(t / 60) >= 1 changed (at most kMaxSamples).
  void Sample(double matchSeconds, const SeatSample seats[kSeats]);

  // Seams for the later packages: a stat key added to a seat (MSTATS2-4), damage and heal by source (MSTATS3).
  void AddStat(int seat, const std::string& key, double value);
  void AddSource(int seat, const std::string& kind, int slot, std::int64_t id, double damage, double heal);
  // MSTATS3: a heal (healingDone, -Self/-Allies/-Other, healingReceived, lifeDrain, overheal, healRole<Name>, the
  // source's heal) and a shield's absorb credited to the seat that gave it (shieldingDone; not its own creature's).
  void OnHeal(const HealEvent& h);
  void OnShieldAbsorb(int giverSeat, int targetSeat, bool sameTeam, double absorbed);

  // The end: open death timers end at endMs (once; later calls do nothing).
  void Close(std::uint64_t endMs);
  // One seatStats per present seat, in index order. A bot's stats carry playerFinish's keys too.
  std::vector<dggms::control::SeatStats> BuildSeatStats(const SeatFacts seats[kSeats]) const;
  // The timeline, trimmed to `budget` bytes (Encode's line, newline included).
  dggms::control::MatchTimeline BuildTimeline(std::size_t budget = kLineBudget, TrimReport* report = nullptr) const;

  // For the logs and tests.
  double Stat(int seat, const std::string& key) const;
  int Samples() const { return static_cast<int>(sec_.size()); }
  int Events() const { return static_cast<int>(events_.size()); }
  unsigned Hits() const { return hits_; }
  unsigned Skipped() const { return skipped_; }
  unsigned Heals() const { return heals_; }
  // MSTATS3's check: a seat's source rows summed (damage, heal), before the cap and the rounding.
  void SourceSums(int seat, double* damage, double* heal) const;

 private:
  struct Seat {
    std::map<std::string, double> sums;
    double largest = 0.0;
    bool dead = false;
    std::uint64_t deathMs = 0, respawnMs = 0;
    double deadMs = 0.0;
    std::map<std::tuple<std::string, int, std::int64_t>, std::pair<double, double>> sources;
    bool sampled = false;
    std::vector<std::int64_t> gold, xp, level, cs;
  };
  void Add(int seat, const std::string& key, double v);
  void AddEvent(std::int64_t ms, const char* kind, std::int64_t a, std::int64_t b);

  Seat seats_[kSeats];
  std::vector<dggms::control::TimelineEvent> events_;
  int eventsCapped_ = 0;
  std::vector<std::int64_t> sec_;
  std::int64_t teamKills_[3] = {0, 0, 0};
  std::vector<std::int64_t> teamGold_[3], teamKillSamples_[3];
  int lastMinute_ = 0;
  bool closed_ = false;
  unsigned hits_ = 0, skipped_ = 0, heals_ = 0;
};

// Trims a timeline to `budget` bytes (exposed for the tests): events by priority (kill, well, structure, the end;
// the latest first), then every second sample (the last kept, stepSec doubled).
void FitTimeline(dggms::control::MatchTimeline& tl, std::size_t budget, TrimReport* report);

}  // namespace gms::stats
