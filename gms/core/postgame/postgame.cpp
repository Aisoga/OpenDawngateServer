// The match end and the post-game on the GMS (D8-live): see postgame.h.
#include "core/postgame/postgame.h"

#include <algorithm>
#include <cctype>
#include <cmath>
#include <cstdio>
#include <cstring>

#include "core/protocol/wire.h"

namespace gms {
namespace postgame {

namespace {

void PutI32(Bytes& b, int32_t v) { PutU32LE(b, static_cast<uint32_t>(v)); }

int32_t GetI32(const uint8_t* p) { return static_cast<int32_t>(GetU32LE(p)); }

// LootBagTuning.FTUELoot of build 854: the Resonance perk shape and two FlatPower gems (B7c's corrected ids).
const uint32_t kFtueLoot[] = {0x10A4638Fu, 0x10A46383u, 0x10A46383u};

}  // namespace

int WinnerTeam(uint32_t reason) {
  switch (reason) {
    case kReasonMortalWins:
    case kReasonMortalBySurrender:
      return 1;
    case kReasonSpiritWins:
    case kReasonSpiritBySurrender:
      return 2;
    default:
      return 0;
  }
}

Mode ModeOf(uint32_t descriptors) {
  // GameOverState_OnGameOver's choice of cinematic: & 0x18 practice; else bit 0 matchmade; else custom
  if (descriptors & (kDescSoloPractice | kDescGroupPractice)) return Mode::kPractice;
  if (descriptors & kDescMatchmade) return Mode::kMatchmade;
  return Mode::kCustom;
}

const char* ModeName(Mode m) {
  switch (m) {
    case Mode::kPractice:
      return "practice";
    case Mode::kMatchmade:
      return "matchmade";
    default:
      return "custom";
  }
}

uint32_t AssetId(const std::string& name) {
  uint32_t h = 0x811C9DC5u;
  for (char c : name) {
    h *= 0x01000193u;
    h ^= static_cast<uint8_t>(std::tolower(static_cast<unsigned char>(c)));
  }
  return h;
}

const char* LootBagName(uint32_t id) {
  switch (id) {
    case kLootBagBronze:
      return "Bronze";
    case kLootBagSilver:
      return "Silver";
    case kLootBagGold:
      return "Gold";
    case kLootBagPlatinum:
      return "Platinum";
    case kLootBagDiamond:
      return "Diamond";
    default:
      return "";
  }
}

Bytes BuildGameOver() { return Bytes{kGmsBattlegroundsMsgs, 0x00}; }

Bytes BuildOver(uint32_t reason, uint32_t guardian, uint32_t descriptors) {
  Bytes b{kGmsBattlegroundsPostGameMsgs, kSubOver};
  PutU32LE(b, reason);
  PutU32LE(b, guardian);
  PutU32LE(b, descriptors);
  return b;
}

Bytes BuildVoteTimer(int64_t startMs, int64_t endMs) {
  Bytes b{kGmsBattlegroundsPostGameMsgs, kSubVoteTimer};
  PutI64LE(b, startMs);
  PutI64LE(b, endMs);
  return b;
}

bool BuildRewards(const Rewards& r, Bytes* out) {
  if (r.items.size() > kMaxItems) return false;
  Bytes b{kGmsBattlegroundsPostGameMsgs, kSubRewards};
  PutU32LE(b, r.lootBag);
  PutI32(b, r.destiny);
  PutI32(b, r.baseRoll);
  PutI32(b, r.winPoints);
  PutI32(b, r.winAltPoints);
  PutI32(b, r.firstWinPoints);
  PutI32(b, r.karmaPoints);
  PutI32(b, r.karmaParty);
  PutI32(b, r.karmaTeam);
  PutI32(b, r.karmaEnemy);
  for (size_t i = 0; i < kMaxItems; ++i) {
    const RewardItem it = i < r.items.size() ? r.items[i] : RewardItem{};
    PutU32LE(b, it.catalogId);
    PutU8(b, it.duplicate ? 1 : 0);
    PutU8(b, 0);
    PutU8(b, 0);
    PutU8(b, 0);
    PutU32LE(b, it.alternate);
    PutU32LE(b, 0);  // +0x0C: read by nothing seen
  }
  PutU32LE(b, static_cast<uint32_t>(r.items.size()));
  PutU32LE(b, r.extraItem);
  PutU32LE(b, r.extraCount);
  PutU8(b, r.ftue ? 1 : 0);
  PutU8(b, 0);
  PutU8(b, 0);
  PutU8(b, 0);
  *out = std::move(b);
  return out->size() == 2 + kRewardsBodySize;
}

Bytes BuildExit() { return Bytes{kGmsBattlegroundsPostGameMsgs, kSubExit}; }

bool ParseClientMessage(const uint8_t* body, size_t n, ClientMessage* out) {
  if (n < 1) return false;
  *out = ClientMessage{};
  out->sub = body[0];
  if (body[0] == kSubOver) return n == 1;
  if (body[0] == kSubVotes) {
    if (n < 3) return false;
    out->votes = static_cast<uint16_t>(body[1] | body[2] << 8);
    return true;
  }
  return false;
}

bool ParseRewards(const uint8_t* body, size_t n, Rewards* out) {
  if (n < kRewardsBodySize) return false;
  Rewards r;
  r.lootBag = GetU32LE(body + 0x00);
  r.destiny = GetI32(body + 0x04);
  r.baseRoll = GetI32(body + 0x08);
  r.winPoints = GetI32(body + 0x0C);
  r.winAltPoints = GetI32(body + 0x10);
  r.firstWinPoints = GetI32(body + 0x14);
  r.karmaPoints = GetI32(body + 0x18);
  r.karmaParty = GetI32(body + 0x1C);
  r.karmaTeam = GetI32(body + 0x20);
  r.karmaEnemy = GetI32(body + 0x24);
  const uint32_t count = GetU32LE(body + 0x68);
  if (count > kMaxItems) return false;
  for (uint32_t i = 0; i < count; ++i) {
    const uint8_t* e = body + 0x28 + 16 * i;
    RewardItem it;
    it.catalogId = GetU32LE(e);
    it.duplicate = e[4] != 0;
    it.alternate = GetU32LE(e + 8);
    r.items.push_back(it);
  }
  r.extraItem = GetU32LE(body + 0x6C);
  r.extraCount = GetU32LE(body + 0x70);
  r.ftue = body[0x74] != 0;
  *out = r;
  return true;
}

std::string Describe(const Rewards& r) {
  char buf[200];
  const char* bag = LootBagName(r.lootBag);
  std::snprintf(buf, sizeof buf, "bag %08X%s%s%s, Destiny %d, points %d/%d/%d/%d, ftue %d, items [", r.lootBag,
                bag[0] ? " (" : "", bag, bag[0] ? ")" : "", r.destiny, r.baseRoll, r.winPoints, r.winAltPoints,
                r.firstWinPoints, r.ftue ? 1 : 0);
  std::string s = buf;
  for (size_t i = 0; i < r.items.size(); ++i) {
    std::snprintf(buf, sizeof buf, "%s%08X%s", i ? " " : "", r.items[i].catalogId,
                  r.items[i].duplicate ? (" dup " + std::to_string(r.items[i].alternate)).c_str() : "");
    s += buf;
  }
  s += "]";
  if (r.karmaPoints || r.karmaParty || r.karmaTeam || r.karmaEnemy) {  // D8b-core: matchmade games only
    std::snprintf(buf, sizeof buf, ", karma %d (votes: party %d, team %d, enemy %d)", r.karmaPoints, r.karmaParty,
                  r.karmaTeam, r.karmaEnemy);
    s += buf;
  }
  return s;
}

// ---- rewards --------------------------------------------------------------------------------------------------------

int BaseRoll(double rand01) {
  const float x = 100.f * static_cast<float>(rand01) + 125.f;
  const float v = static_cast<float>(std::pow(static_cast<double>(x), 3.5)) / 325446.44f;
  return static_cast<int>(std::floor(static_cast<double>(v) + 0.5));  // nUtil.Round [assumed: half up]
}

void OutcomePoints(bool won, double seconds, int* win, int* alt) {
  *win = won ? 200 : 0;
  const float m = std::min(static_cast<float>(seconds) / 60.f, 55.f);
  *alt = static_cast<int>(m * 0.91f);
}

float EcAward(const char* bag, double seconds, uint32_t descriptors, bool practiceAllowed) {
  const float m = static_cast<float>(seconds) / 60.f;
  if (descriptors & (kDescSoloPractice | kDescGroupPractice)) {
    if (!practiceAllowed) return 0.f;
    if (m <= 30.f) return 5.f + 0.63f * std::min(m, 50.f);
    return 17.f + 0.22f * std::min(m, 50.f);
  }
  struct Tier {
    const char* name;
    float a, b, c, mid, late;
  };
  static const Tier kTiers[] = {{"bronze", 22.4f, 15.2f, 28.8f, 0.6f, 0.22f},
                                {"silver", 32.f, 25.f, 40.f, 0.6f, 0.19f},
                                {"gold", 40.f, 33.6f, 47.2f, 0.61f, 0.225f},
                                {"platinum", 49.6f, 44.f, 60.f, 0.62f, 0.21f},
                                {"diamond", 64.f, 55.2f, 76.f, 0.75f, 0.21f}};
  std::string lower(bag ? bag : "");
  for (char& c : lower) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
  for (const Tier& t : kTiers) {
    if (lower != t.name) continue;
    const float m55 = std::min(m, 55.f);
    if (m <= 18.f) return t.a + 0.225f * m55;
    if (m <= 37.f) return t.b + t.mid * m55;
    return t.c + t.late * m55;
  }
  return 0.f;
}

Rewards PracticeRewards(bool won, double seconds, uint32_t descriptors, double rand01, bool firstWin) {
  Rewards r;
  r.ftue = (descriptors & kDescFTUE) != 0;
  r.baseRoll = BaseRoll(rand01);
  int win = 0, alt = 0;
  OutcomePoints(won, seconds, &win, &alt);
  r.winPoints = win;
  r.winAltPoints = alt;
  r.firstWinPoints = firstWin && won ? 100 : 0;  // LootBagTuning.Points_FirstWinOfTheDay
  const char* bag = "Bronze";                     // LootBagTuning.FTUEBag_Default
  if (r.ftue && won) {
    bag = "Platinum";  // FTUEBag_Loot
    for (uint32_t id : kFtueLoot) r.items.push_back(RewardItem{id, false, 0});
  }
  r.lootBag = AssetId(std::string(bag) + ".LootBag");
  const float d = EcAward(bag, seconds, descriptors);
  r.destiny = d > 0.f ? static_cast<int32_t>(d) : 0;
  return r;
}

// ---- the karma vote -------------------------------------------------------------------------------------------------

std::map<int, KarmaVotes> TallyKarma(const std::vector<KarmaVoter>& voters) {
  std::map<int, KarmaVotes> out;
  std::map<int, const KarmaVoter*> byIndex;
  for (const KarmaVoter& v : voters) {
    out.emplace(v.index, KarmaVotes{});
    byIndex[v.index] = &v;
  }
  for (const KarmaVoter& v : voters) {
    for (int bit = 0; bit < 16; ++bit) {
      if (!(v.mask >> bit & 1) || bit == v.index) continue;  // no self-votes (the own row has no karma box)
      auto t = byIndex.find(bit);
      if (t == byIndex.end()) continue;  // nobody has that index
      KarmaVotes& k = out[bit];
      const KarmaVoter& target = *t->second;
      if (target.team != v.team)
        ++k.enemy;
      else if (v.partyId >= 0 && v.partyId == target.partyId)
        ++k.party;
      else
        ++k.team;
    }
  }
  return out;
}

// ---- the state machine ----------------------------------------------------------------------------------------------

void PostGame::AddPlayer(ConnId conn, int index, int team, const Rewards& own) {
  Player& p = players_[conn];
  p.index = index;
  p.team = team;
  p.own = own;
}

void PostGame::AddSpectator(ConnId conn, int index) {
  Player& p = players_[conn];
  p.index = index;
  p.team = 0;
  p.spectator = true;
}

void PostGame::SetParty(ConnId conn, long long partyId) {
  auto it = players_.find(conn);
  if (it != players_.end()) it->second.partyId = partyId;
}

void PostGame::Start(double nowS, uint32_t reason, uint32_t guardian, uint32_t descriptors, std::vector<Send>* out) {
  if (started_) return;
  started_ = true;
  endAt_ = nowS;
  reason_ = reason;
  guardian_ = guardian;
  descriptors_ = descriptors;
  mode_ = ModeOf(descriptors);
  winner_ = WinnerTeam(reason);
  for (auto& kv : players_) {
    if (kv.second.left) continue;
    out->push_back(Send{kv.first, BuildGameOver(), "BattlegroundsMsgs 00: game over (-> kGameStateGameOver)"});
  }
}

void PostGame::OnServerRewards(ConnId conn, const Rewards& r, bool withKarma) {
  auto it = players_.find(conn);
  if (it == players_.end() || it->second.rewardsSent) return;
  if (it->second.serverKarma && !withKarma) return;  // the karma-aware rewards stay
  it->second.haveServer = true;
  it->second.serverKarma = withKarma;
  it->second.server = r;
}

std::vector<KarmaReport> PostGame::TakeKarmaReports() {
  std::vector<KarmaReport> out;
  if (!voteClosed_ || karmaTaken_) return out;
  karmaTaken_ = true;
  for (const auto& kv : players_) {
    if (kv.second.spectator) continue;  // B15b
    const auto k = karma_.find(kv.second.index);
    out.push_back(KarmaReport{kv.first, kv.second.index, k != karma_.end() ? k->second : KarmaVotes{}});
  }
  return out;
}

bool PostGame::OnClientMessage(double nowS, ConnId conn, const uint8_t* body, size_t n) {
  auto it = players_.find(conn);
  ClientMessage m;
  if (!started_ || it == players_.end() || !ParseClientMessage(body, n, &m)) return false;
  Player& p = it->second;
  if (m.sub == kSubOver) {
    if (!p.acked) {
      p.acked = true;
      p.ackAt = nowS;
    }
    return true;
  }
  if (m.sub == kSubVotes) {
    if (voteClosed_ || p.spectator) return true;  // too late: the votes were counted; B15b: a spectator doesn't vote
    p.votesIn = true;
    p.votes = m.votes;
    return true;
  }
  return false;
}

void PostGame::OnStatus(double nowS, ConnId conn, uint32_t status) {
  auto it = players_.find(conn);
  if (!started_ || it == players_.end() || !(status & kStatusReadyForKarmaVote) || it->second.ready) return;
  it->second.ready = true;
  it->second.readyAt = nowS;
}

void PostGame::OnDisconnected(ConnId conn) {
  auto it = players_.find(conn);
  if (it != players_.end()) it->second.left = true;
}

bool PostGame::AllLeft() const {
  if (!started_) return false;
  for (const auto& kv : players_)
    if (!kv.second.left && !kv.second.spectator) return false;
  return true;
}

// Whether the player's C1 04 is due by the mode's rule (the rewards' source is decided in SendRewards).
bool PostGame::RewardsDue(const Player& p, double nowS) const {
  if (p.spectator || !p.overSent || p.rewardsSent || p.left || !o_.sendRewards) return false;  // B15b: none
  switch (mode_) {
    case Mode::kPractice:
      if (p.team != winner_) return true;  // lost: the custom cinematic, nothing waited for
      return p.ready || (nowS - p.overAt) * 1000.0 >= o_.readyWaitMs;
    case Mode::kMatchmade:
      return voteSent_ && (nowS - voteAt_) * 1000.0 >= o_.voteMs + o_.voteGraceMs;
    default:
      return true;
  }
}

void PostGame::SendRewards(ConnId conn, Player& p, double nowS, std::vector<Send>* out) {
  const bool matchmade = mode_ == Mode::kMatchmade && voteClosed_;
  // the server's rewards once in; the host's own after rewardsWaitMs from the end. D8c, matchmade: the server's
  // karma-aware rewards (after the host's playerKarma); the others after rewardsWaitMs from the vote's close
  const bool waitMore = matchmade ? !p.serverKarma && (nowS - voteClosedAt_) * 1000.0 < o_.rewardsWaitMs
                                  : !p.haveServer && (nowS - endAt_) * 1000.0 < o_.rewardsWaitMs;
  if (waitMore) return;
  Rewards r = p.haveServer ? p.server : p.own;
  if (p.haveServer && r.lootBag == 0) r.lootBag = p.own.lootBag;  // no content on the server: an unknown bag asserts
  if (r.items.size() > kMaxItems) r.items.resize(kMaxItems);
  if (matchmade && !p.serverKarma) {  // the karma votes the player got, by the host's own tally
    const auto k = karma_.find(p.index);
    const KarmaVotes kv = k != karma_.end() ? k->second : KarmaVotes{};
    r.karmaParty = kv.party;
    r.karmaTeam = kv.team;
    r.karmaEnemy = kv.enemy;
    r.karmaPoints = kv.Total() * o_.karmaPointsPerVote;
  }
  Bytes m;
  if (!BuildRewards(r, &m)) return;
  p.rewardsSent = true;
  p.rewardsAt = nowS;
  p.rewardsFromServer = p.haveServer;
  const char* source = !p.haveServer    ? "the host's own"
                       : p.serverKarma ? "dgserver's playerRewards with its karma"
                       : matchmade     ? "dgserver's playerRewards, the host's karma"
                                       : "dgserver's playerRewards";
  std::string what =
      std::string("BattlegroundsPostGameMsgs 04: rewards (") + source + "; " + postgame::Describe(r) + ")";
  out->push_back(Send{conn, std::move(m), what});
}

void PostGame::Tick(double nowS, int64_t serverMs, std::vector<Send>* out) {
  if (!started_) return;
  for (auto& kv : players_) {
    Player& p = kv.second;
    if (p.left || p.overSent) continue;
    if (!p.acked && (nowS - endAt_) * 1000.0 < o_.ackFallbackMs) continue;
    out->push_back(Send{kv.first, BuildOver(reason_, guardian_, descriptors_),
                        p.acked ? "BattlegroundsPostGameMsgs 01: game over (on the client's C1 01)"
                                : "BattlegroundsPostGameMsgs 01: game over (fallback: no C1 01 from the client)"});
    p.overSent = true;
    p.overAt = nowS;
  }
  // matchmade: the vote timer once every player is ready (or the wait is over)
  if (mode_ == Mode::kMatchmade && !voteSent_) {
    bool allReady = true, anyOver = false;
    double firstOver = nowS;
    for (const auto& kv : players_) {
      if (kv.second.left || kv.second.spectator) continue;  // B15b: a spectator isn't waited for
      if (!kv.second.overSent) {
        allReady = false;
        continue;
      }
      anyOver = true;
      firstOver = std::min(firstOver, kv.second.overAt);
      allReady = allReady && kv.second.ready;
    }
    if (anyOver && (allReady || (nowS - firstOver) * 1000.0 >= o_.readyWaitMs)) {
      voteSent_ = true;
      voteAt_ = nowS;
      for (const auto& kv : players_) {
        if (kv.second.left || !kv.second.overSent || kv.second.spectator) continue;  // B15b: no vote for a spectator
        char what[96];
        std::snprintf(what, sizeof what, "BattlegroundsPostGameMsgs 02: karma vote %lld..%lld ms (%d s)",
                      static_cast<long long>(serverMs), static_cast<long long>(serverMs + o_.voteMs), o_.voteMs / 1000);
        out->push_back(Send{kv.first, BuildVoteTimer(serverMs, serverMs + o_.voteMs), what});
      }
    }
  }
  // matchmade: the vote closes voteGraceMs after its end (the clients send C1 03 at the end); count the votes once
  if (mode_ == Mode::kMatchmade && voteSent_ && !voteClosed_ &&
      (nowS - voteAt_) * 1000.0 >= o_.voteMs + o_.voteGraceMs) {
    voteClosed_ = true;
    voteClosedAt_ = nowS;
    std::vector<KarmaVoter> voters;
    for (const auto& kv : players_) {
      if (kv.second.spectator) continue;  // B15b
      KarmaVoter v;
      v.index = kv.second.index;
      v.team = kv.second.team;
      v.partyId = kv.second.partyId;
      v.mask = kv.second.votesIn ? kv.second.votes : 0;
      voters.push_back(v);
    }
    karma_ = TallyKarma(voters);
  }
  for (auto& kv : players_)
    if (RewardsDue(kv.second, nowS)) SendRewards(kv.first, kv.second, nowS, out);
}

std::string PostGame::Describe(ConnId conn) const {
  auto it = players_.find(conn);
  if (!started_ || it == players_.end()) return std::string();
  const Player& p = it->second;
  char buf[200];
  if (p.spectator) {  // B15b
    std::snprintf(buf, sizeof buf, "post-game (%s, a spectator): ack %s, C1 01 %s", ModeName(mode_),
                  p.acked ? (std::to_string(static_cast<int>((p.ackAt - endAt_) * 1000)) + " ms").c_str() : "no",
                  p.overSent ? "sent" : "not yet");
    return buf;
  }
  std::snprintf(buf, sizeof buf, "post-game (%s, %s): ack %s, C1 01 %s, ready %s, C1 04 %s", ModeName(mode_),
                p.team == winner_ ? "won" : "lost",
                p.acked ? (std::to_string(static_cast<int>((p.ackAt - endAt_) * 1000)) + " ms").c_str() : "no",
                p.overSent ? "sent" : "not yet",
                p.ready ? (std::to_string(static_cast<int>(p.readyAt - endAt_)) + " s").c_str() : "no",
                !p.rewardsSent           ? "not sent"
                : !p.rewardsFromServer   ? "sent (the host's own)"
                : p.serverKarma          ? "sent (dgserver's, karma counted)"
                                         : "sent (dgserver's)");
  return buf;
}

}  // namespace postgame
}  // namespace gms
