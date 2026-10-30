// The match recorder's file, engine-free: the record encoding, the buffered writer with its size cap and the sample
// rate. The engine side (gms/engine/matchrec.cpp) samples the match and feeds it.
//
// The file is text, one record a line, fields separated by one tab, the record type first (format 1):
//   #dgmatchrec  1                                     the first line: the format and its version
//   H  <key>  <value>                                  a fact about the match (match, map, started_utc, rate_hz, ...)
//   P  <seat> <team> <human|bot> <user> <noun> <shaper> <role> <bot class>     one line per seat at the start
//   S  <ms> <seat> <state> <x> <y> <z> <hp> <hp max> <resource> <resource max> <level> <gold> <total gold>
//      <target> <seen by>                              a seat's snapshot (rate_hz a second)
//   C  <ms> <seat> <human|bot> <source> <kind> <seq> <slot> <ability> <target> <x> <y> <z> <value>
//                                                      a command as the host received or issued it
//   E  <ms> <kind> <fields...>                         a match event (kill, structure, objective, item, well, end)
//   F  <ms> <seat> <kills> <deaths> <assists> <creeps> <level> <total gold>     a seat's final counts at the end
//   X  <ms> <end|cap|stop>                             the last line: the match ended, the size cap was reached, or the
//                                                      host stopped before the end
// <ms> is match time in milliseconds (0 = the match's start). Ids (user, noun, ability, target) are decimal for the
// user and hex (8 digits) for the game's ids; "-" is an empty string.
#pragma once

#include <cstddef>
#include <cstdint>
#include <cstdio>
#include <memory>
#include <string>
#include <vector>

#include "core/protocol/commands.h"

namespace gms::matchrec {

constexpr int kFormatVersion = 1;
constexpr const char* kMagic = "#dgmatchrec";
constexpr std::size_t kDefaultCapBytes = 64u * 1024u * 1024u;  // [host] match_record_cap_mb 64
constexpr std::size_t kMinCapBytes = 4096;
constexpr std::size_t kDefaultBufferBytes = 64u * 1024u;  // written to the file when full, at the end and on Flush
constexpr std::size_t kTrailerReserve = 48;               // room the cap keeps for the X line

// Where the bytes go. Write and Flush return false on an error; the writer then stops (the match goes on).
class Sink {
 public:
  virtual ~Sink() = default;
  virtual bool Write(const char* p, std::size_t n) = 0;
  virtual bool Flush() = 0;
};

// A file opened for writing (truncated). Create returns nullptr when it can't be opened.
class FileSink : public Sink {
 public:
  static std::unique_ptr<FileSink> Create(const std::string& path);
  ~FileSink() override;
  bool Write(const char* p, std::size_t n) override;
  bool Flush() override;

 private:
  explicit FileSink(std::FILE* f) : f_(f) {}
  std::FILE* f_ = nullptr;
};

// For the tests: the bytes in memory; failAfter >= 0 makes the (failAfter + 1)-th Write fail.
class StringSink : public Sink {
 public:
  bool Write(const char* p, std::size_t n) override;
  bool Flush() override {
    ++flushes;
    return true;
  }
  std::string data;
  int writes = 0, flushes = 0;
  int failAfter = -1;
};

// One seat at the match's start.
struct SeatInfo {
  int seat = -1;
  int team = 0;
  bool bot = false;
  std::int64_t user = 0;    // the server's account (Blaze user) id; a bot's is its seat + 1
  std::uint32_t noun = 0;   // the shaper's Noun asset id
  std::string shaper;       // its asset name ("savage.Noun")
  int role = 0;             // 1 + mRole, as the match statistics' role
  std::string botClass;     // a bot's class ("PC_Warden"), else empty
};

// A seat's state.
enum State : char { kAlive = 'A', kDead = 'D', kRecalling = 'R', kNoCreature = 'N' };

struct Snapshot {
  int seat = -1;
  char state = kNoCreature;
  float pos[3] = {0.f, 0.f, 0.f};
  float hp = 0.f, hpMax = 0.f, res = 0.f, resMax = 0.f;
  int level = 0;
  float gold = 0.f, totalGold = 0.f;  // available, earned in the match
  std::uint32_t target = 0;           // the creature's current target (its blackboard's mTarget), 0 = none
  unsigned seenBy = 0;                // the teams that see the creature: bit 1 team 1, bit 2 team 2
};

struct Command {
  int seat = -1;
  bool bot = false;
  std::string source;  // "client", "injected", "bot" or "bot:<what the bot layer did>"
  std::string kind;    // move, attackmove, stop, ability, ping, item, rankup, ...
  int seq = -1;        // the client's sequence, -1 none
  int slot = -1;       // the ability slot, -1 none
  std::uint32_t ability = 0;  // an ability by GUID, an item's asset id
  std::uint32_t target = 0;
  bool hasPos = false;
  float pos[3] = {0.f, 0.f, 0.f};
  std::int64_t value = 0;  // the kind's extra (a ping's type, an item transaction's type, a bot request's result)
};

// A client's ActionCommandMsgs as a C record: kind by type (move, stopmoving, ability, interact, dance, taunt, ping,
// stop, attackmove, voiceline, hold; an ability by Return to Base's GUID is "recall"), the move's or ping's point or
// the ability's aim as the position (none for the others), value = a ping's type or voice line, an ability's flags
// (1 walk up, 2 reactivation, 4 keep attacking).
const char* ActionKind(std::uint8_t type);
Command FromAction(const gms::ActionCommand& a, int seat, bool bot, const std::string& source);

// A request the host's bot layer made for a bot (LabsAbilities_RequestAbility): kind "move" (ability_Bot_MoveToTarget),
// "recall" (Return to Base), "attack" (the basic attack's slot 0), else "ability"; source "bot:<where>"; the position
// unless it is (0, 0, 0); value = the request's result (1 or more done, 0 or less refused).
constexpr std::uint32_t kGuidBotMoveToTarget = 0x2378FAE9;  // SPID("ability_Bot_MoveToTarget")
Command FromBotRequest(int seat, const char* where, std::uint32_t guid, int slot, std::uint32_t target,
                       const float* pos, int result);
// A consumable the bot layer used for a bot (a potion: LabsPlayer_UseItem, no request), recorded as a client's use is:
// kind "item", value = the transaction type "use" (3), the item's asset id, the inventory slot; source "bot:<where>".
// Its outcome goes into an "item" event, as a client's transaction's does.
Command FromBotItemUse(int seat, const char* where, std::uint32_t item, int invSlot);

// A seat's final counts (the cLabsPlayer's, as the match statistics read them at the end).
struct Final {
  int seat = -1;
  int kills = 0, deaths = 0, assists = 0, creeps = 0, level = 0;
  std::int64_t totalGold = 0;
};

// A string as one field: tabs, line ends and other control characters become blanks; empty becomes "-".
std::string Field(const std::string& s);

std::string EncodeHeader();
std::string EncodeMeta(const std::string& key, const std::string& value);
std::string EncodeSeat(const SeatInfo& s);
std::string EncodeSnapshot(std::int64_t ms, const Snapshot& s);
std::string EncodeCommand(std::int64_t ms, const Command& c);
std::string EncodeEvent(std::int64_t ms, const std::string& kind, const std::vector<std::string>& fields);
std::string EncodeFinal(std::int64_t ms, const Final& f);
std::string EncodeEnd(std::int64_t ms, const char* reason);
// "1,4,7" or "-" for an empty list.
std::string JoinSeats(const std::vector<int>& seats);

struct WriterStats {
  std::uint64_t bytes = 0;     // accepted (buffered or written), the X line included
  std::uint64_t lines = 0;
  std::uint64_t dropped = 0;   // lines refused after the cap or an error
  std::uint64_t writes = 0;    // the sink's writes (each a buffer's worth)
  bool capped = false;
  bool failed = false;
  bool closed = false;
};

// Buffers the lines and writes them to the sink a buffer at a time. The file never grows past the cap: the line that
// would pass it (with room for the X line) is refused, "X <ms> cap" ends the file and every later line is dropped.
// A sink error stops the writer the same way (no X line). Nothing here throws on an I/O error; the match never waits.
class Writer {
 public:
  Writer() = default;
  ~Writer();
  Writer(const Writer&) = delete;
  Writer& operator=(const Writer&) = delete;

  // capBytes below kMinCapBytes is raised to it. Writes the #dgmatchrec line. False if the sink is null.
  bool Open(std::unique_ptr<Sink> sink, std::size_t capBytes = kDefaultCapBytes,
            std::size_t bufferBytes = kDefaultBufferBytes);
  // One encoded line (ending in '\n'); ms is the time the cap's X line gets. False when refused or not open.
  bool Line(std::int64_t ms, const std::string& line);
  // Writes the buffer to the sink and flushes it.
  bool Flush();
  // "X <ms> <reason>", then the buffer; later lines are dropped. Once.
  void Close(std::int64_t ms, const char* reason);

  bool Active() const { return sink_ && !st_.capped && !st_.failed && !st_.closed; }
  const WriterStats& Stats() const { return st_; }
  std::size_t Cap() const { return cap_; }

 private:
  bool Drain();
  std::unique_ptr<Sink> sink_;
  std::string buf_;
  std::size_t cap_ = kDefaultCapBytes, bufMax_ = kDefaultBufferBytes;
  WriterStats st_;
};

// The snapshot clock: Due(ms) is true at the first call and then once every 1000 / hz ms of match time (a late call
// doesn't make a burst: the next one is a period after it). hz is clamped to 0.1-30; 0 or less = never.
class Rate {
 public:
  explicit Rate(double hz = 5.0);
  bool Due(std::int64_t ms);
  double PeriodMs() const { return periodMs_; }

 private:
  double periodMs_ = 200.0;
  double next_ = -1.0;
  bool never_ = false;
};

// The file's name for a match: "matchrec_<match>_<yyyymmdd-hhmmss>.txt", the match id's characters other than letters,
// digits, '-' and '_' replaced by '_' (and "match" when it is empty).
std::string FileName(const std::string& match, int year, int month, int day, int hour, int minute, int second);

}  // namespace gms::matchrec
