// MAPED4b: the level's own ward spots for the bots' ward condition (rules: gms/core/bots/wardspots.h). The director
// calls ReadLevel with the server level once its markers are read; nBotHost.WardSpot (bots.cpp) asks Answer.
#pragma once

namespace wardspots {

// Reads the level's ward spot markers (BotWard_<Mortal|Spirit>_<nn>, every markerset) and logs them.
void ReadLevel(void* level);
// nBotHost.WardSpot(side, k): 0 keep the data's tables, 1 not ready, 2 the spot in xy, 3 no spot k (gms::bots::WardAnswer).
int Answer(int side, int k, float* xy);

}  // namespace wardspots
