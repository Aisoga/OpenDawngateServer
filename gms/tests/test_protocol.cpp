// gms/core/protocol: the wire-byte table and the session layouts. The golden bytes are what the live client sent and
// received in d2b_run2/d2e_run5 (docs/GMS_PROTOCOL.md "The live session (D2b-live)", re/logs/d2e_run5/gms_probe.out).
#include <cstdlib>
#include <fstream>
#include <string>

#include "core/common/bytes.h"
#include "core/protocol/messages.h"
#include "core/protocol/wire.h"
#include "tests/check.h"

#ifndef GMS_REPO_ROOT
#error "GMS_REPO_ROOT (the repository root) must be defined by gms/CMakeLists.txt"
#endif

using gms::Bytes;

namespace {

Bytes FromHex(const char* hex) {
  Bytes b;
  gms::ParseHex(hex, b);
  return b;
}

}  // namespace

GMS_TEST(WireTableMatchesTransportIdsCsv) {
  const std::string path = std::string(GMS_REPO_ROOT) + "/docs/data/gms_transport_ids.csv";
  std::ifstream in(path);
  CHECK(in.good());
  std::string line;
  std::getline(in, line);
  CHECK_EQ(line, std::string("id,name,wire_byte"));
  size_t rows = 0;
  while (std::getline(in, line)) {
    if (!line.empty() && line.back() == '\r') line.pop_back();
    if (line.empty()) continue;
    const size_t c1 = line.find(','), c2 = line.find(',', c1 + 1);
    CHECK(c1 != std::string::npos && c2 != std::string::npos);
    if (c1 == std::string::npos || c2 == std::string::npos) continue;
    const int id = std::atoi(line.substr(0, c1).c_str());
    const std::string name = line.substr(c1 + 1, c2 - c1 - 1);
    const unsigned long wire = std::strtoul(line.substr(c2 + 1).c_str(), nullptr, 16);
    CHECK(id >= 0 && static_cast<size_t>(id) < gms::kGmsMessageTypeCount);
    if (id < 0 || static_cast<size_t>(id) >= gms::kGmsMessageTypeCount) continue;
    const gms::GmsMessageType& t = gms::kGmsMessageTypes[id];
    CHECK_EQ(static_cast<int>(t.id), id);
    CHECK_EQ(std::string("kGms") + t.name, name);
    CHECK_EQ(static_cast<unsigned long>(t.wire), wire);
    ++rows;
  }
  CHECK_EQ(rows, gms::kGmsMessageTypeCount);
  // every wire byte is unique and a GMS byte
  for (const gms::GmsMessageType& t : gms::kGmsMessageTypes) {
    CHECK(t.wire >= gms::kGmsFirstWireByte);
    CHECK_EQ(gms::FindGmsByWire(t.wire), &t);
  }
  CHECK_EQ(std::string(gms::GmsName(0x13)), std::string("?"));
  CHECK_EQ(std::string(gms::GmsName(0xB3)), std::string("PlayerCharacterDeploy"));
}

GMS_TEST(HexHelpers) {
  Bytes b;
  CHECK(gms::ParseHex("\xEF\xBB\xBF" "84 0A:ff", b));
  CHECK_HEX(b, "840aff");
  CHECK(!gms::ParseHex("abc", b));  // odd digit count
  CHECK(!gms::ParseHex(" ", b));
  CHECK_EQ(gms::Hex(FromHex("0102030405"), 2), std::string("0102..."));
  Bytes w;
  gms::PutU16BE(w, 0x273A);
  gms::PutU32LE(w, 0x946D41FE);
  gms::PutF32LE(w, 1.0f);
  CHECK_HEX(w, "273afe416d940000803f");
  CHECK_EQ(gms::GetU32LE(w.data() + 2), 0x946D41FEu);
  CHECK_EQ(gms::GetF32LE(w.data() + 6), 1.0f);
}

GMS_TEST(ClientMessagesParse) {
  // HelloReq of d2e_run5: user id 2000000011, playgroup 0
  const Bytes hello = FromHex("0b943577000000000000000000000000");
  gms::HelloReq req;
  CHECK(gms::ParseHelloReq(hello.data(), hello.size(), &req));
  CHECK_EQ(req.userId, 2000000011ull);
  CHECK_EQ(req.playgroupId, 0ull);
  CHECK(!gms::ParseHelloReq(hello.data(), 7, &req));

  gms::PlayerStatusReport r;
  const Bytes s1 = FromHex("0000010000000000");
  CHECK(gms::ParsePlayerStatusUpdate(s1.data(), s1.size(), &r));
  CHECK_EQ(r.flags, 0x10000u);
  CHECK_EQ(r.progress, 0.0f);
  const Bytes s2 = FromHex("010400000000803f");
  CHECK(gms::ParsePlayerStatusUpdate(s2.data(), s2.size(), &r));
  CHECK_EQ(r.flags, 0x401u);
  CHECK_EQ(r.progress, 1.0f);

  int64_t ms = 0;
  const Bytes c = FromHex("527a000000000000");
  CHECK(gms::ParseClockSync(c.data(), c.size(), &ms));
  CHECK_EQ(ms, 31314ll);
}

GMS_TEST(SessionMessagesMatchTheLiveBytes) {
  CHECK_HEX(gms::BuildConnected(), "84");
  gms::HelloPlayerRecord me;  // type 0, index 0, 127.0.0.1:10042
  CHECK_HEX(gms::BuildHelloPlayer({me}), "8200007f000001273a");
  // d2b_run2 (user 2000000004) and d2e_run5 (user 2000000011): status 0x10, team 1, alpha.Noun, online id
  CHECK_HEX(gms::BuildPlayerUpdate(0, 0x10, 1, gms::kNounAlpha, 2000000004ull),
            "a9003301011000000001ab4a04ef0494357700000000");
  CHECK_HEX(gms::BuildPlayerUpdate(0, 0x10, 1, gms::kNounAlpha, 2000000011ull),
            "a9003301011000000001ab4a04ef0b94357700000000");
  CHECK_HEX(gms::BuildGamePrepareForStart(gms::GameSetup{}), "b50000000002000000fe416d94010000000000000000");
  CHECK_EQ(gms::BuildGamePrepareForStart(gms::GameSetup{}).size(), size_t{22});
  CHECK_HEX(gms::BuildPlayerStatusEcho(0, 0x10010, 0.0f), "a900131000010000000000");
  CHECK_HEX(gms::BuildPlayerStatusEcho(0, 0x10090, 0.0f), "a900139000010000000000");
  CHECK_HEX(gms::BuildPlayerStatusEcho(0, 0x10491, 1.0f), "a90013910401000000803f");
  // ClockSync answer #1 of d2e_run5: client 31314 ms, server 55055 ms
  CHECK_HEX(gms::BuildClockSyncAnswer(31314, 55055), "c7527a0000000000000fd7000000000000");
  CHECK_HEX(gms::BuildEnterPreGameFlow(), "b6");
  CHECK_HEX(gms::BuildPreGame(gms::kPreGameLoad), "c001");
  CHECK_HEX(gms::BuildPreGame(gms::kPreGameDone), "c003");
  CHECK_HEX(gms::BuildEnterGame(), "b700");
}

GMS_TEST(PlayerUpdateLeavesOutZeroStatusAndNoun) {
  // D2c's mask rules: mStatus (member 0) and mpNounDef (16) are only present when non-zero
  CHECK_HEX(gms::BuildPlayerUpdate(2, 0, 2, 0, 1ull), "a902320100020100000000000000");
  CHECK_HEX(gms::BuildPlayerUpdate(0, 0, 1, gms::kNounAlpha, 1ull), "a90033010001ab4a04ef0100000000000000");
}
