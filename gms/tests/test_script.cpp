// gms/script: the inject format of --inject and --inject-file (D2e), unchanged from raknet_probe except that unknown
// points are now refused (the probe accepted them and never sent the message).
#include <cstdio>
#include <string>
#include <vector>

#include "script/scripted_match.h"
#include "tests/check.h"

GMS_TEST(ParseInjectPointsDelaysAndHex) {
  gms::InjectSpec s;
  std::string err;
  CHECK(gms::ParseInject("loaded:8f01000100", &s, &err));
  CHECK(s.point == gms::SessionPoint::kLoaded);
  CHECK_EQ(s.delayMs, 0);
  CHECK_HEX(s.msg, "8f01000100");
  CHECK(gms::ParseInject("entergame+20000:9f 01 00", &s, &err));
  CHECK(s.point == gms::SessionPoint::kEnterGame);
  CHECK_EQ(s.delayMs, 20000);
  CHECK_HEX(s.msg, "9f0100");
  CHECK_EQ(gms::DescribeInject(s), std::string("entergame+20000:9f0100"));
  CHECK(gms::ParseInject("  clock5:C2", &s, &err));
  CHECK(s.point == gms::SessionPoint::kClock5);

  CHECK(!gms::ParseInject("loaded8f01", &s, &err));        // no colon
  CHECK(!gms::ParseInject("nowhere:8f01", &s, &err));      // unknown point
  CHECK(!gms::ParseInject("connected:84", &s, &err));      // not an inject point
  CHECK(!gms::ParseInject("loaded+x:8f01", &s, &err));     // bad delay
  CHECK(!gms::ParseInject("loaded:13", &s, &err));         // a RakNet id, not a GMS wire byte
  CHECK(!gms::ParseInject("loaded:8f0", &s, &err));        // odd digit count
  CHECK(!gms::ParseInject("loaded:", &s, &err));
}

GMS_TEST(ReadInjectFileSkipsCommentsBlanksAndBom) {
  char path[L_tmpnam + 16];
  std::snprintf(path, sizeof path, "%s.inject", std::tmpnam(nullptr));
  {
    FILE* f = std::fopen(path, "wb");
    CHECK(f != nullptr);
    if (!f) return;
    std::fputs("\xEF\xBB\xBFhello:c201   # first\r\n\r\n# a comment line\r\nloaded+5:8e0100\r\n   \r\nentergame:b300\r\n", f);
    std::fclose(f);
  }
  std::vector<gms::InjectSpec> v;
  std::string err;
  CHECK_EQ(gms::ReadInjectFile(path, &v, &err), 3);
  CHECK_EQ(v.size(), size_t{3});
  if (v.size() == 3) {
    CHECK(v[0].point == gms::SessionPoint::kHello);
    CHECK_HEX(v[0].msg, "c201");
    CHECK(v[1].point == gms::SessionPoint::kLoaded);
    CHECK_EQ(v[1].delayMs, 5);
    CHECK(v[2].point == gms::SessionPoint::kEnterGame);
  }
  {
    FILE* f = std::fopen(path, "wb");
    if (f) {
      std::fputs("hello:c201\nbad line\n", f);
      std::fclose(f);
    }
  }
  CHECK_EQ(gms::ReadInjectFile(path, &v, &err), -1);
  CHECK(err.find(":2:") != std::string::npos);
  std::remove(path);
  CHECK_EQ(gms::ReadInjectFile(path, &v, &err), -1);  // missing file
}
