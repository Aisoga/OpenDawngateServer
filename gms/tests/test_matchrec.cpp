// gms/core/matchrec: the match recorder's file without the engine: the records' bytes, the writer's buffer, the size
// cap (the file never passes it, ends with "X <ms> cap" and drops the rest), a failing sink, the rate and the name.
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <memory>
#include <string>
#include <vector>

#include "core/matchrec/matchrec.h"
#include "tests/check.h"

using namespace gms::matchrec;

namespace {

int CountLines(const std::string& s) {
  int n = 0;
  for (char c : s) n += c == '\n';
  return n;
}

std::string LastLine(const std::string& s) {
  if (s.empty()) return s;
  const std::size_t end = s.size() - 1;
  const std::size_t start = s.rfind('\n', end - 1);
  return s.substr(start == std::string::npos ? 0 : start + 1);
}

}  // namespace

GMS_TEST(MatchRecRecordBytes) {
  CHECK_EQ(EncodeHeader(), std::string("#dgmatchrec\t1\n"));
  CHECK_EQ(EncodeMeta("map", "SM_Map_v5.Level"), std::string("H\tmap\tSM_Map_v5.Level\n"));
  CHECK_EQ(EncodeMeta("note", "a\tb\nc"), std::string("H\tnote\ta b c\n"));
  CHECK_EQ(EncodeMeta("empty", ""), std::string("H\tempty\t-\n"));

  SeatInfo p;
  p.seat = 3;
  p.team = 2;
  p.bot = true;
  p.user = 4;
  p.noun = 0xF60BF244u;
  p.shaper = "savage.Noun";
  p.role = 2;
  p.botClass = "PC_Warden";
  CHECK_EQ(EncodeSeat(p), std::string("P\t3\t2\tbot\t4\tF60BF244\tsavage.Noun\t2\tPC_Warden\n"));
  SeatInfo h;
  h.seat = 0;
  h.team = 1;
  h.user = 1000000123;
  CHECK_EQ(EncodeSeat(h), std::string("P\t0\t1\thuman\t1000000123\t00000000\t-\t0\t-\n"));

  Snapshot s;
  s.seat = 1;
  s.state = kAlive;
  s.pos[0] = -123.46f;
  s.pos[1] = 45.04f;
  s.pos[2] = -0.01f;  // -0.0 is written as 0.0
  s.hp = 512.6f;
  s.hpMax = 600.f;
  s.res = 99.6f;
  s.resMax = 200.f;
  s.level = 7;
  s.gold = 120.4f;
  s.totalGold = 2450.4f;
  s.target = 0x7C710042u;
  s.seenBy = 6 | 1 | 8;  // only the team bits are kept
  CHECK_EQ(EncodeSnapshot(1200, s),
           std::string("S\t1200\t1\tA\t-123.5\t45.0\t0.0\t513\t600\t100\t200\t7\t120\t2450\t7C710042\t6\n"));

  Command c;
  c.seat = 0;
  c.source = "client";
  c.kind = "move";
  c.seq = 17;
  c.hasPos = true;
  c.pos[0] = 10.f;
  c.pos[1] = -20.3f;
  c.pos[2] = 1.f;
  CHECK_EQ(EncodeCommand(500, c),
           std::string("C\t500\t0\thuman\tclient\tmove\t17\t-1\t00000000\t00000000\t10.0\t-20.3\t1.0\t0\n"));
  Command b;
  b.seat = 6;
  b.bot = true;
  b.source = "bot:reflex attack";
  b.kind = "ability";
  b.slot = 2;
  b.ability = 0x1F827AC1u;
  b.target = 0x00120034u;
  b.value = -3;
  CHECK_EQ(EncodeCommand(61000, b),
           std::string("C\t61000\t6\tbot\tbot:reflex attack\tability\t-1\t2\t1F827AC1\t00120034\t-\t-\t-\t-3\n"));

  CHECK_EQ(EncodeEvent(90000, "kill", {"3", "7", JoinSeats({6, 8})}), std::string("E\t90000\tkill\t3\t7\t6,8\n"));
  CHECK_EQ(EncodeEvent(1, "end", {}), std::string("E\t1\tend\n"));
  CHECK_EQ(JoinSeats({}), std::string("-"));

  Final f;
  f.seat = 9;
  f.kills = 4;
  f.deaths = 2;
  f.assists = 11;
  f.creeps = 140;
  f.level = 16;
  f.totalGold = 13250;
  CHECK_EQ(EncodeFinal(1800000, f), std::string("F\t1800000\t9\t4\t2\t11\t140\t16\t13250\n"));
  CHECK_EQ(EncodeEnd(1800000, "end"), std::string("X\t1800000\tend\n"));
}

GMS_TEST(MatchRecWriterBuffersAndCloses) {
  auto sink = std::make_unique<StringSink>();
  StringSink* raw = sink.get();
  Writer w;
  CHECK(w.Open(std::move(sink), 1u << 20, 1024));
  CHECK(w.Active());
  CHECK_EQ(raw->writes, 0);  // the header waits in the buffer
  Snapshot s;
  s.seat = 2;
  s.state = kDead;
  const std::string line = EncodeSnapshot(200, s);
  int n = 0;
  while (raw->writes == 0) {
    CHECK(w.Line(200, line));
    ++n;
  }
  CHECK(n > 1);  // a buffer of 1,024 bytes holds many lines: one write for all of them
  CHECK_EQ(raw->writes, 1);
  CHECK(raw->data.size() >= 1024);
  w.Close(5000, "end");
  CHECK(!w.Active());
  CHECK(w.Stats().closed);
  CHECK(!w.Stats().capped);
  CHECK_EQ(raw->data.substr(0, 14), std::string("#dgmatchrec\t1\n"));
  CHECK_EQ(LastLine(raw->data), std::string("X\t5000\tend\n"));
  const WriterStats st = w.Stats();
  CHECK_EQ(st.bytes, static_cast<std::uint64_t>(raw->data.size()));
  CHECK_EQ(st.lines, static_cast<std::uint64_t>(CountLines(raw->data)));
  CHECK_EQ(st.dropped, 0u);
  // after the close every line is dropped
  CHECK(!w.Line(6000, line));
  CHECK_EQ(w.Stats().dropped, 1u);
  w.Close(7000, "end");  // once only
  CHECK_EQ(LastLine(raw->data), std::string("X\t5000\tend\n"));
}

GMS_TEST(MatchRecWriterStopsAtTheCap) {
  for (std::size_t buffer : {std::size_t(256), std::size_t(4096), std::size_t(1u << 20)}) {
    auto sink = std::make_unique<StringSink>();
    StringSink* raw = sink.get();
    Writer w;
    const std::size_t cap = 10000;
    CHECK(w.Open(std::move(sink), cap, buffer));
    Command c;
    c.seat = 4;
    c.source = "client";
    c.kind = "attackmove";
    c.hasPos = true;
    int accepted = 0, refused = 0;
    for (int i = 0; i < 1000; ++i) {
      c.seq = i & 255;
      if (w.Line(i * 33, EncodeCommand(i * 33, c))) ++accepted;
      else ++refused;
    }
    CHECK(w.Stats().capped);
    CHECK(!w.Active());
    CHECK(accepted > 50);
    CHECK(refused > 500);
    w.Close(99999, "end");  // no second X line after the cap's
    CHECK(raw->data.size() <= cap);
    CHECK(raw->data.size() > cap - 200);  // the cap is used, not left mostly empty
    const std::string last = LastLine(raw->data);
    CHECK_EQ(last.substr(0, 2), std::string("X\t"));
    CHECK(last.find("\tcap\n") != std::string::npos);
    CHECK_EQ(w.Stats().bytes, static_cast<std::uint64_t>(raw->data.size()));
    CHECK_EQ(w.Stats().dropped, static_cast<std::uint64_t>(refused));
    // every line is whole: the file ends with '\n' and the X line is the only one of its kind
    CHECK_EQ(raw->data.back(), '\n');
    CHECK_EQ(raw->data.find("\nX\t"), raw->data.rfind("\nX\t"));
  }
}

GMS_TEST(MatchRecWriterTinyCapIsRaised) {
  auto sink = std::make_unique<StringSink>();
  StringSink* raw = sink.get();
  Writer w;
  CHECK(w.Open(std::move(sink), 10, 256));
  CHECK_EQ(w.Cap(), kMinCapBytes);
  const std::string line(100, 'x');
  for (int i = 0; i < 100; ++i) w.Line(i, line + "\n");
  w.Close(100, "end");
  CHECK(raw->data.size() <= kMinCapBytes);
  CHECK(w.Stats().capped);
}

GMS_TEST(MatchRecWriterSinkFailureStopsQuietly) {
  auto sink = std::make_unique<StringSink>();
  StringSink* raw = sink.get();
  raw->failAfter = 1;  // the first buffer is written, the second fails
  Writer w;
  CHECK(w.Open(std::move(sink), 1u << 20, 256));
  const std::string line(60, 'y');
  int ok = 0;
  for (int i = 0; i < 100; ++i) ok += w.Line(i, line + "\n") ? 1 : 0;
  CHECK(w.Stats().failed);
  CHECK(!w.Active());
  CHECK(ok < 100);
  CHECK(w.Stats().dropped > 0);
  const std::size_t before = raw->data.size();
  w.Close(100, "end");  // no exception, nothing more written
  CHECK_EQ(raw->data.size(), before);
  CHECK(!w.Flush());
}

GMS_TEST(MatchRecWriterNullSink) {
  Writer w;
  CHECK(!w.Open(nullptr));
  CHECK(!w.Active());
  CHECK(!w.Line(0, "x\n"));
  w.Close(0, "end");
}

GMS_TEST(MatchRecFileSinkWritesAFile) {
  const char* temp = std::getenv("TEMP");
  const std::string path = std::string(temp ? temp : ".") + "\\gms_tests_matchrec.txt";
  {
    Writer w;
    CHECK(w.Open(FileSink::Create(path), 1u << 20, 4096));
    CHECK(w.Line(0, EncodeMeta("map", "SM_Map_v5.Level")));
    w.Close(10, "end");
  }
  std::FILE* f = std::fopen(path.c_str(), "rb");
  CHECK(f != nullptr);
  if (!f) return;
  char buf[256] = {};
  const std::size_t n = std::fread(buf, 1, sizeof buf - 1, f);
  std::fclose(f);
  std::remove(path.c_str());
  CHECK_EQ(std::string(buf, n), std::string("#dgmatchrec\t1\nH\tmap\tSM_Map_v5.Level\nX\t10\tend\n"));
  CHECK(FileSink::Create(path + "\\no\\such\\folder.txt") == nullptr);  // a missing folder: nullptr, no throw
}

GMS_TEST(MatchRecRate) {
  Rate r(5.0);
  CHECK_EQ(r.PeriodMs(), 200.0);
  std::vector<std::int64_t> due;
  for (std::int64_t ms = 0; ms <= 1000; ms += 33)
    if (r.Due(ms)) due.push_back(ms);
  // a 33-ms tick: the first at 0, then the first tick at or after each 200 ms
  const std::vector<std::int64_t> want = {0, 231, 429, 627, 825};
  CHECK_EQ(due.size(), want.size());
  for (std::size_t i = 0; i < due.size() && i < want.size(); ++i) CHECK_EQ(due[i], want[i]);

  Rate late(5.0);
  CHECK(late.Due(0));
  CHECK(late.Due(5000));    // a long stall: one sample, not 25
  CHECK(!late.Due(5100));
  CHECK(late.Due(5200));

  Rate off(0.0);
  CHECK(!off.Due(0));
  CHECK(!off.Due(100000));
  Rate fast(1000.0);  // clamped to 30 Hz
  CHECK(fast.PeriodMs() > 33.0 && fast.PeriodMs() < 33.4);
}

GMS_TEST(MatchRecFileName) {
  CHECK_EQ(FileName("12", 2026, 10, 28, 9, 5, 7), std::string("matchrec_12_20261028-090507.txt"));
  CHECK_EQ(FileName("a/b\\c:d", 2026, 1, 2, 3, 4, 5), std::string("matchrec_a_b_c_d_20260102-030405.txt"));
  CHECK_EQ(FileName("", 2026, 1, 2, 3, 4, 5), std::string("matchrec_match_20260102-030405.txt"));
}

GMS_TEST(MatchRecCommandsFromTheWireAndTheBots) {
  gms::ActionCommand m;
  m.type = gms::kCmdAttackMove;
  m.seq = 9;
  m.pos = {1.f, 2.f, 3.f};
  CHECK_EQ(EncodeCommand(10, FromAction(m, 0, false, "client")),
           std::string("C\t10\t0\thuman\tclient\tattackmove\t9\t-1\t00000000\t00000000\t1.0\t2.0\t3.0\t0\n"));
  gms::ActionCommand q;
  q.type = gms::kCmdAbility;
  q.seq = 10;
  q.slot = 3;
  q.target = 0x00450011u;
  q.aim = {5.f, 6.f, 0.f};
  q.walkUp = 1;
  q.keepAttacking = 1;
  CHECK_EQ(EncodeCommand(20, FromAction(q, 2, false, "injected")),
           std::string("C\t20\t2\thuman\tinjected\tability\t10\t3\t00000000\t00450011\t5.0\t6.0\t0.0\t5\n"));
  gms::ActionCommand r;
  r.type = gms::kCmdAbility;
  r.slot = -1;
  r.guid = gms::kGuidReturnToBase;
  const Command rc = FromAction(r, 1, false, "client");
  CHECK_EQ(rc.kind, std::string("recall"));
  gms::ActionCommand p;
  p.type = gms::kCmdPing;
  p.pos = {7.f, 8.f, 0.f};
  p.value = 2;
  const Command pc = FromAction(p, 1, false, "client");
  CHECK_EQ(pc.value, std::int64_t(2));
  gms::ActionCommand s;
  s.type = gms::kCmdStop;
  const Command sc = FromAction(s, 1, false, "client");
  CHECK(!sc.hasPos);
  CHECK_EQ(std::string(ActionKind(gms::kCmdHold)), std::string("hold"));
  CHECK_EQ(std::string(ActionKind(200)), std::string("unknown"));

  const float at[3] = {100.f, -50.f, 2.f};
  const float zero[3] = {0.f, 0.f, 0.f};
  CHECK_EQ(EncodeCommand(30, FromBotRequest(7, "gambit", kGuidBotMoveToTarget, -1, 0, at, 1)),
           std::string("C\t30\t7\tbot\tbot:gambit\tmove\t-1\t-1\t2378FAE9\t00000000\t100.0\t-50.0\t2.0\t1\n"));
  const Command atk = FromBotRequest(7, nullptr, 0, 0, 0x00120034u, zero, -2);
  CHECK_EQ(atk.kind, std::string("attack"));
  CHECK_EQ(atk.source, std::string("bot:direct"));
  CHECK(!atk.hasPos);
  CHECK_EQ(atk.value, std::int64_t(-2));
  const Command back = FromBotRequest(7, "back", gms::kGuidReturnToBase, -1, 0, zero, 1);
  CHECK_EQ(back.kind, std::string("recall"));
  const Command kit = FromBotRequest(7, "kit", 0, 3, 0, at, 1);
  CHECK_EQ(kit.kind, std::string("ability"));
}

// The size per minute the defaults give (documented in the format's notes): ten seats at 5 Hz.
GMS_TEST(MatchRecSnapshotBudget) {
  Snapshot s;
  s.seat = 9;
  s.state = kAlive;
  s.pos[0] = -1234.5f;
  s.pos[1] = 1234.5f;
  s.pos[2] = 12.3f;
  s.hp = 2345.f;
  s.hpMax = 2400.f;
  s.res = 500.f;
  s.resMax = 500.f;
  s.level = 18;
  s.gold = 3456.f;
  s.totalGold = 23456.f;
  s.target = 0xFFFFFFFFu;
  s.seenBy = 6;
  const std::size_t line = EncodeSnapshot(3599999, s).size();
  CHECK(line <= 96);
  std::printf("  a full snapshot line: %zu bytes; ten seats at 5 Hz: %.0f KB a minute at most\n", line,
              static_cast<double>(line) * 10 * 5 * 60 / 1024.0);
}
