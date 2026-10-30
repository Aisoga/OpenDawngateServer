// MAPED4b: a level's own ward spots for the bots' ward condition (core/bots/wardspots.h): the markers' names, the
// order by their numbers, and nBotHost.WardSpot's answers.
#include <cstdio>

#include "core/bots/wardspots.h"
#include "tests/check.h"

using namespace gms::bots;

GMS_TEST(Maped4bWardMarkerNames) {
  int side = 0, index = 0;
  CHECK(ParseWardMarkerName("BotWard_Mortal_03", &side, &index));
  CHECK_EQ(side, 1);
  CHECK_EQ(index, 3);
  CHECK(ParseWardMarkerName("BotWard_Spirit_10", &side, &index));
  CHECK_EQ(side, 2);
  CHECK_EQ(index, 10);
  CHECK(!ParseWardMarkerName(nullptr, &side, &index));
  CHECK(!ParseWardMarkerName("", &side, &index));
  CHECK(!ParseWardMarkerName("BotWard_Mortal_", &side, &index));
  CHECK(!ParseWardMarkerName("BotWard_Neutral_01", &side, &index));
  CHECK(!ParseWardMarkerName("BotWard_Mortal_0x", &side, &index));
  CHECK(!ParseWardMarkerName("navmeshmarker_valid.Noun-2", &side, &index));
  CHECK(!ParseWardMarkerName("botward_mortal_01", &side, &index));  // the compiler's exact names only
}

GMS_TEST(Maped4bWardSpotsAnswer) {
  WardSpots w;
  float xy[2] = {};
  // before the level is read: ask again; [host] bot_ward_spots 0: the data's tables
  CHECK(w.Answer(1, 1, 1, xy) == WardAnswer::kNotReady);
  CHECK(w.Answer(0, 1, 1, xy) == WardAnswer::kKeep);
  // CM_Grove's first spots, out of order, with other markers among them
  CHECK(!w.Add("navmeshmarker_valid.Noun", -188.9f, -183.9f));
  CHECK(w.Add("BotWard_Mortal_01", -120.f, 120.f));
  CHECK(w.Add("BotWard_Spirit_00", 65.f, 65.f));
  CHECK(w.Add("BotWard_Mortal_00", -65.f, -65.f));
  CHECK(!w.Add("BotWard_Mortal_02", -174.f, 0.f));  // a coordinate of 0: the condition takes it for none
  CHECK(!w.Add("BotWard_Spirit_01", 0.f, 0.f));     // and (0, 0) is the bots' null position
  w.Finish();
  CHECK_EQ(w.Count(1), 2);
  CHECK_EQ(w.Count(2), 1);
  CHECK(w.Answer(1, 1, 1, xy) == WardAnswer::kSpot);
  CHECK_EQ(xy[0], -65.f);
  CHECK_EQ(xy[1], -65.f);
  CHECK(w.Answer(1, 1, 2, xy) == WardAnswer::kSpot);
  CHECK_EQ(xy[0], -120.f);
  CHECK_EQ(xy[1], 120.f);
  CHECK(w.Answer(1, 1, 3, xy) == WardAnswer::kEnd);
  CHECK(w.Answer(1, 1, 0, xy) == WardAnswer::kEnd);
  CHECK(w.Answer(1, 2, 1, xy) == WardAnswer::kSpot);
  CHECK_EQ(xy[0], 65.f);
  CHECK(w.Answer(1, 2, 2, xy) == WardAnswer::kEnd);
  CHECK(w.Answer(1, 3, 1, xy) == WardAnswer::kKeep);
  CHECK(w.Answer(0, 1, 1, xy) == WardAnswer::kKeep);
  // a level without ward markers (SM_Map_v5, a map without "wards"): the data's tables
  w.Clear();
  CHECK(w.Answer(1, 1, 1, xy) == WardAnswer::kNotReady);
  w.Finish();
  CHECK(w.Answer(1, 1, 1, xy) == WardAnswer::kKeep);
  CHECK(w.Answer(1, 2, 1, xy) == WardAnswer::kKeep);
  // the cap a side
  WardSpots many;
  int taken = 0;
  for (int k = 0; k < kMaxWardSpots + 5; ++k) {
    char name[32];
    std::snprintf(name, sizeof name, "BotWard_Mortal_%02d", k);
    if (many.Add(name, 10.f + k, -10.f)) ++taken;
  }
  many.Finish();
  CHECK_EQ(taken, kMaxWardSpots);
  CHECK(many.Answer(1, 1, kMaxWardSpots, xy) == WardAnswer::kSpot);
  CHECK(many.Answer(1, 1, kMaxWardSpots + 1, xy) == WardAnswer::kEnd);
}
