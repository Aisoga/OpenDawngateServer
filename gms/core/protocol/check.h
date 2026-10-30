// The strict check of every client -> GMS message (gms/core). The match host parses every client packet in our code before the 2014 game logic sees a value
// (a public server's defence); this is where those values are checked, before they reach the session, the command queue,
// the item transactions, shaper select, surrender or the post-game.
//
// Two steps, both run by GmsSession::OnMessage (SessionOptions::strictCheck):
//   CheckFormat  engine-free: the exact size per type and subtype, the value ranges (command types 1-11 with their
//                data sizes, ability slots -1..11 with the GUID rule, rank-up slots 3-6, finite positions inside the
//                level, object ids inside the server's object table, the shop's item ids, item transaction slots,
//                status bits the client may report, progress 0..1, roles 0-3, vanity kinds, vote masks, surrender
//                votes, pause toggles), and the wire bytes a client never sends (server -> client only, CheatMessage,
//                DebugPing).
//   CheckWorld   against the match (IWorld, the host's server simulator): the caster is the sender's own creature,
//                a target exists and the sender's client has it in view (or had it within the fog grace), a sold, used
//                or consumed item is in the named inventory slot, commands come from a player (not a spectator).
// The verdicts:
//   kMalformed  a size, type or subtype the client's writers never produce: dropped, counted towards the kick
//   kInvalid    a well-formed message with a value the shipped client never sends (NaN, outside the map, a slot or
//               item that doesn't exist, another creature as the caster, a GMS-only status bit): dropped, counted
//               towards the kick
//   kStale      a value a normal client can send in a race (a target that died or went into fog a moment ago, an item
//               already used up): dropped and answered as a refusal (B4 kind 2 / C3 success 0), not counted towards
//               the kick
// The live client's census (every "<-" line of 586 sessions' logs, 2026-10-13: 81 x16, 88 x8, A6 per type, A7 x4,
// C0 04, C1 01/03, C3 x11, C7 x8, C8 per subtype, CA x1, CB x2, CC x1) is what the sizes and ranges were checked
// against; tests/test_check.cpp keeps a sample of it as the accepted set.
#pragma once

#include <cstddef>
#include <cstdint>
#include <deque>
#include <map>
#include <string>
#include <vector>

#include "core/common/bytes.h"
#include "core/net/endpoint.h"

namespace gms::check {

enum class Verdict : uint8_t { kOk = 0, kMalformed, kInvalid, kStale };
const char* VerdictName(Verdict v);
inline bool CountsTowardsKick(Verdict v) { return v == Verdict::kMalformed || v == Verdict::kInvalid; }

struct Result {
  Verdict verdict = Verdict::kOk;
  std::string reason;  // empty when ok
  bool ok() const { return verdict == Verdict::kOk; }
};

// The level's area: AssetData Level.levelSize, centred on the origin (SM_Map_v5 525.434 x 525.283; the live client's
// clicks reached |x|, |y| <= 246.0 and z -0.09..7.8 in the census), plus a margin of 2 units; z -100..300.
struct Bounds {
  float minX = -300.f, maxX = 300.f, minY = -300.f, maxY = 300.f, minZ = -100.f, maxZ = 300.f;
  bool Contains(float x, float y, float z) const;
};
Bounds BoundsForLevel(uint32_t levelAssetId);  // an unknown level: +-300 (the largest gameplay level, MM_Map_v3 580)

// The server's game objects (GameObjectManager: 1,024; "out of game objects!" at 1,010 of 1,024 in Q3): an object id
// is (serial << 16) | slot, so slot >= 1024 is no object (GetObjectById 0x008E2220 -> 0x00A81980 asserts on it).
constexpr uint32_t kMaxObjectSlots = 1024;
inline bool PlausibleObjectId(uint32_t id) { return id != 0 && (id & 0xFFFFu) < kMaxObjectSlots; }

// The shop's table: the items of ActiveItems_SM_Map_V5.ItemSet (every gameplay level's activeItems), 104 of its 106:
// Consumable_Debug_GrantExp and Consumable_Debug_GrantGold are left out (the shipped shop doesn't offer them [assumed:
// no session ever bought one]). Asset ids are FNV-1 of the lower-cased "<name>.Item" (asset +0x60).
bool IsShopItem(uint32_t assetId);
const std::vector<uint32_t>& ShopItems();  // sorted; the table (SM_Map_v5's), whatever the level set

// MAPED4: the level's own data, read by the match host when the level starts (Level +0x5C8 levelSize, +0x604
// activeItems), so a compiled map (in neither table) gets its own area and shop. Thread-safe.
// SetLevelSize: BoundsForLevel(levelAssetId) gives this size from now on (it takes precedence over the table).
void SetLevelSize(uint32_t levelAssetId, float sizeX, float sizeY);
// SetLevelShopItems: IsShopItem answers from these items (the level's activeItems without the debug consumables)
// instead of the table; an empty list goes back to the table.
void SetLevelShopItems(const std::vector<uint32_t>& assetIds);
// Counts the calls of both, so a holder of BoundsForLevel's result can tell it is out of date.
unsigned LevelDataGeneration();
// The two debug consumables of every ItemSet, which no shop offers (the table leaves them out too).
bool IsDebugItemName(const std::string& name);

// The status bits a client reports in PlayerStatusUpdate: 0x1 (with 0x400),
// 0x80 clock synchronised, 0x400 level loaded, 0x10000/0x20000 match accepted/declined, 0x40000 left the game, 0x80000
// ready for the karma vote. 0x4, 0x8, 0x10, 0x20 and 0x100 are the GMS's own (gone, skip shaper select, observer,
// shaper select done): a client reporting one would set it on itself (the session ORs reported bits in).
constexpr uint32_t kClientStatusBits = 0x1u | 0x80u | 0x400u | 0x10000u | 0x20000u | 0x40000u | 0x80000u;

// Engine-free: size, type and value ranges of the client's message `wire` (the wire byte) with `body` (after it).
Result CheckFormat(uint8_t wire, const uint8_t* body, size_t n, const Bounds& bounds);

// ---- against the match ------------------------------------------------------------------------------------------
struct SenderView {
  bool player = false;         // a seated player with a server player (false: a spectator, or not seated yet)
  uint8_t gameplayIndex = 0;
  uint32_t creature = 0;       // its creature's server id (0 = none yet)
};
enum class TargetView : uint8_t {
  kGone,     // no such object now (it died or was deleted: a normal client's race)
  kUnknown,  // it exists, but the sender's client never got it (its bit isn't in the object's known players)
  kHidden,   // known, but out of the sender's view for longer than the fog grace
  kVisible,  // in the sender's view, or out of it for less than the grace (the client's view lags the server's)
};
struct InventoryView {
  uint32_t item[11] = {};
  int32_t count[11] = {};
};
class IWorld {
 public:
  virtual bool Sender(ConnId conn, SenderView* out) = 0;
  virtual TargetView Target(ConnId conn, uint32_t id) = 0;
  virtual bool Inventory(ConnId conn, InventoryView* out) = 0;

 protected:
  ~IWorld() = default;
};

// For a message CheckFormat passed: the values that need the match. `world` may refuse to answer (false from Sender:
// the sender is no player) — then commands, rank-ups and item transactions are invalid (a spectator sends none:
// GameClient_SendActionCommand 0x006B41B0 and the gameplay controls check the observer bit).
Result CheckWorld(uint8_t wire, const uint8_t* body, size_t n, ConnId conn, IWorld& world);

// ---- the per-connection guard: counting and the kick ---------------------------------------------------------------
struct GuardOptions {
  int mode = 1;              // 0 off, 1 drop (the default), 2 count and log only (every message passes)
  int kickCount = 10;        // this many malformed or invalid messages ...
  int kickWindowMs = 10000;  // ... within this window close the connection (0 = never)
  int logLimit = 100;        // bad messages logged one by one, then every 100th
};

struct Stats {
  uint64_t checked = 0, ok = 0, malformed = 0, invalid = 0, stale = 0, kicked = 0, other = 0;
  std::map<uint8_t, uint64_t> badByWire;  // malformed + invalid + stale, by wire byte (RakNet ids for `other`)
  uint64_t Bad() const { return malformed + invalid + stale; }
  void Add(const Stats& s);
  // "checked 12345, dropped 3 (malformed 1, invalid 1, stale 1; a6 x2, c3 x1), RakNet other 0, kicked 0"
  std::string Describe() const;
};

class Guard {
 public:
  explicit Guard(const GuardOptions& o = GuardOptions()) : o_(o) {}
  const GuardOptions& Options() const { return o_; }

  // One message's verdict for `conn` at `nowMs`. Returns true if the connection must be kicked now (once).
  bool Note(ConnId conn, uint8_t wire, Verdict v, double nowMs);
  // A RakNet packet with an id the application never expects from a connected client (not 0x13/0x15/0x16, below
  // 0x81): counted like a malformed message.
  bool NoteOther(ConnId conn, uint8_t id, double nowMs);
  void Forget(ConnId conn) { recent_.erase(conn); }
  // Whether this bad message is to be logged (the first logLimit, then every 100th).
  bool ShouldLog();

  const Stats& Total() const { return total_; }
  Stats TakeWindow();  // what happened since the last call (the host's `net:` line per match minute)

 private:
  bool Count(ConnId conn, double nowMs);
  GuardOptions o_;
  Stats total_, window_;
  std::map<ConnId, std::deque<double>> recent_;
  uint64_t logged_ = 0;
};

}  // namespace gms::check
