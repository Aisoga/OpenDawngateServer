#include "core/protocol/messages.h"

#include <cctype>

#include "core/protocol/wire.h"

namespace gms {

uint32_t AssetIdOf(const std::string& name) {
  uint32_t h = 0x811C9DC5u;
  for (unsigned char c : name) {
    h *= 16777619u;
    h ^= static_cast<unsigned char>(std::tolower(c));
  }
  return h;
}

bool ParseHelloReq(const uint8_t* body, size_t n, HelloReq* out) {
  if (n < 8) return false;
  out->userId = GetU64LE(body);
  out->playgroupId = n >= 16 ? GetU64LE(body + 8) : 0;
  return true;
}

bool ParseClockSync(const uint8_t* body, size_t n, int64_t* clientMs) {
  if (n < 8) return false;
  *clientMs = GetI64LE(body);
  return true;
}

bool ParsePlayerStatusUpdate(const uint8_t* body, size_t n, PlayerStatusReport* out) {
  if (n < 8) return false;
  out->flags = GetU32LE(body);
  out->progress = GetF32LE(body + 4);
  return true;
}

Bytes BuildConnected() { return Bytes{kGmsConnected}; }

Bytes BuildHelloPlayer(const std::vector<HelloPlayerRecord>& records) {
  Bytes m{kGmsHelloPlayer};
  for (const HelloPlayerRecord& r : records) {
    PutU8(m, r.type);
    PutU8(m, r.gameplayIndex);
    PutU32BE(m, r.ipv4);
    PutU16BE(m, r.port);
  }
  return m;
}

Bytes BuildPlayerJoined(uint8_t gameplayIndex) { return Bytes{kGmsPlayerJoined, gameplayIndex}; }

Bytes BuildPlayerDeparted(uint8_t gameplayIndex) { return Bytes{kGmsPlayerDeparted, gameplayIndex}; }

Bytes BuildGamePrepareForStart(const GameSetup& s) {
  Bytes m{kGmsGamePrepareForStart};
  PutU32LE(m, 0);
  PutU32LE(m, s.prepareFlags);
  PutU32LE(m, s.level);
  PutU32LE(m, s.seed);
  PutU32LE(m, s.levelFlags);
  PutU8(m, 0);
  return m;
}

Bytes BuildGameAborted(uint64_t blazeGameId, const std::vector<uint64_t>& playerIds, uint8_t first, uint8_t count,
                       uint8_t gamePhase) {
  Bytes m{kGmsGameAborted};
  PutU64LE(m, blazeGameId);
  for (size_t i = 0; i < 10; ++i) PutU64LE(m, i < playerIds.size() ? playerIds[i] : 0);
  PutU8(m, first);
  PutU8(m, count);
  PutU8(m, gamePhase);
  return m;
}

Bytes BuildClockSyncAnswer(int64_t clientMs, int64_t serverMs) {
  Bytes m{kGmsClockSync};
  PutI64LE(m, clientMs);
  PutI64LE(m, serverMs);
  return m;
}

Bytes BuildEnterPreGameFlow() { return Bytes{kGmsEnterPreGameFlow}; }

Bytes BuildPreGame(uint8_t subtype) { return Bytes{kGmsBattlegroundsPreGameMsgs, subtype}; }

Bytes BuildEnterGame() { return Bytes{kGmsEnterGame, 0x00}; }

Bytes BuildPlayerUpdate(uint8_t gameplayIndex, uint32_t status, uint8_t team, uint32_t noun, uint64_t onlineId) {
  const bool withStatus = status != 0, withNoun = noun != 0;
  Bytes m{kGmsLabsPlayerGlobalUpdate, gameplayIndex};
  PutU8(m, static_cast<uint8_t>(0x30 | (withNoun ? 0x01 : 0) | 0x02));
  PutU8(m, 0x01);
  PutU8(m, static_cast<uint8_t>(withStatus ? 0x01 : 0x00));
  if (withStatus) PutU32LE(m, status);
  PutU8(m, team);
  if (withNoun) PutU32LE(m, noun);
  PutU64LE(m, onlineId);
  return m;
}

std::vector<int> PlayerUpdateMembers(uint32_t status, uint32_t noun) {
  std::vector<int> m;
  if (status) m.push_back(kPlayerStatus);
  m.push_back(kPlayerTeam);
  if (noun) m.push_back(kPlayerNounDef);
  m.push_back(kPlayerOnlineId);
  return m;
}

std::vector<int> PlayerStatusEchoMembers() { return {kPlayerStatus, kPlayerStatusProgress}; }

Bytes BuildPlayerStatusEcho(uint8_t gameplayIndex, uint32_t status, float progress) {
  Bytes m{kGmsLabsPlayerGlobalUpdate, gameplayIndex, 0x13};
  PutU32LE(m, status);
  PutF32LE(m, progress);
  return m;
}

namespace {

// labsPlayerGlobalData member sizes in bytes (docs/data/gms_reflected_types.txt; GMS_PROTOCOL.md D2c's table).
constexpr size_t kMemberSize[kPlayerMemberCount] = {4, 4, 4, 4, 4, 4, 8, 8, 1, 1, 1, 1, 4, 4, 4, 4, 4, 8};

void PutMember(Bytes& m, int i, const PlayerGlobalValues& v) {
  switch (i) {
    case 0: PutU32LE(m, v.status); break;
    case 1: PutF32LE(m, v.progress); break;
    case 2: PutU32LE(m, v.creature); break;
    case 3: PutU32LE(m, v.kills); break;
    case 4: PutU32LE(m, v.deaths); break;
    case 5: PutU32LE(m, v.assists); break;
    case 6: PutU64LE(m, v.deathTime); break;
    case 7: PutU64LE(m, v.respawnTime); break;
    case 8: PutU8(m, v.team); break;
    case 9: PutU8(m, v.role); break;
    case 10: PutU8(m, v.playerIndex); break;
    case 11: PutU8(m, v.isBot); break;
    case 12: PutU32LE(m, v.developerPermissions); break;
    case 13: PutU32LE(m, v.loadoutId); break;
    case 14: PutU32LE(m, v.loadoutVersion); break;
    case 15: PutU32LE(m, v.botData); break;
    case 16: PutU32LE(m, v.noun); break;
    default: PutU64LE(m, v.onlineId); break;
  }
}

void GetMember(const uint8_t* p, int i, PlayerGlobalValues* v) {
  switch (i) {
    case 0: v->status = GetU32LE(p); break;
    case 1: v->progress = GetF32LE(p); break;
    case 2: v->creature = GetU32LE(p); break;
    case 3: v->kills = GetU32LE(p); break;
    case 4: v->deaths = GetU32LE(p); break;
    case 5: v->assists = GetU32LE(p); break;
    case 6: v->deathTime = GetU64LE(p); break;
    case 7: v->respawnTime = GetU64LE(p); break;
    case 8: v->team = p[0]; break;
    case 9: v->role = p[0]; break;
    case 10: v->playerIndex = p[0]; break;
    case 11: v->isBot = p[0]; break;
    case 12: v->developerPermissions = GetU32LE(p); break;
    case 13: v->loadoutId = GetU32LE(p); break;
    case 14: v->loadoutVersion = GetU32LE(p); break;
    case 15: v->botData = GetU32LE(p); break;
    case 16: v->noun = GetU32LE(p); break;
    default: v->onlineId = GetU64LE(p); break;
  }
}

}  // namespace

Bytes BuildPlayerGlobalUpdate(uint8_t gameplayIndex, const std::vector<int>& members, const PlayerGlobalValues& v) {
  bool set[kPlayerMemberCount] = {};
  int highest = -1;
  for (int i : members) {
    if (i < 0 || i >= kPlayerMemberCount) return Bytes();
    set[i] = true;
    if (i > highest) highest = i;
  }
  if (highest < 0) return Bytes();
  const size_t n = static_cast<size_t>(highest + 5 + 7) / 8;  // ceil((highest + 5) / 8)
  Bytes mask(n, 0);
  for (int i = 0; i < kPlayerMemberCount; ++i)
    if (set[i]) mask[n - 1 - static_cast<size_t>(i >> 3)] |= static_cast<uint8_t>(1u << (i & 7));
  mask[0] |= static_cast<uint8_t>(n << 4);
  Bytes m{kGmsLabsPlayerGlobalUpdate, gameplayIndex};
  m.insert(m.end(), mask.begin(), mask.end());
  for (int i = 0; i < kPlayerMemberCount; ++i)
    if (set[i]) PutMember(m, i, v);
  return m;
}

bool ParsePlayerGlobalUpdate(const uint8_t* msg, size_t n, uint8_t* gameplayIndex, std::vector<int>* members,
                             PlayerGlobalValues* v) {
  if (n < 3 || msg[0] != kGmsLabsPlayerGlobalUpdate) return false;
  const size_t maskBytes = msg[2] >> 4;
  if (maskBytes == 0 || n < 2 + maskBytes) return false;
  const uint8_t* mask = msg + 2;
  members->clear();
  size_t pos = 2 + maskBytes;
  for (int i = 0; i < kPlayerMemberCount && static_cast<size_t>(i >> 3) < maskBytes; ++i) {
    uint8_t byte = mask[maskBytes - 1 - static_cast<size_t>(i >> 3)];
    if (maskBytes - 1 - static_cast<size_t>(i >> 3) == 0) byte &= 0x0F;  // the count nibble
    if (!(byte >> (i & 7) & 1)) continue;
    if (pos + kMemberSize[i] > n) return false;
    GetMember(msg + pos, i, v);
    pos += kMemberSize[i];
    members->push_back(i);
  }
  *gameplayIndex = msg[1];
  return true;
}

}  // namespace gms
