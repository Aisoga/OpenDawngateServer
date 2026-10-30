// HUNTERROUTE: the bot Hunters' opening route, their Vanquish and a laner's pull at the first buff, on the match
// host. The rules are gms/core/bots/route.h; agents.cpp's bot think calls in here and
// carries the requests out; the Lua's camp conditions ask RoutePoint (nBotHost.HunterRoute).
#pragma once

#include <cstddef>
#include <cstdint>

namespace hunterroute {

struct Options {
  bool route = true;     // [host] bot_hunter_route: the opening route (0: HUNTERBOT's nearest camp by route cost)
  bool vanquish = true;  // [host] bot_hunter_vanquish: the host's Vanquish rule besides the data's gambit
  int pull = 1;          // [host] bot_hunter_pull: 1 for the kits that want it (Faris), 2 every Hunter, 0 none
  int logLimit = 400;    // [host] hunter_route_log_limit: route, Vanquish and pull lines, then every 10th
  bool table = false;    // [host] bot_hunter_route_table: SM_Map_v5's coordinates (HUNTERROUTE) instead of the route
                         // from the level's camps (MAPED4, gms::bots::DeriveRoute)
};

void Configure(const Options& o);
const Options& Opts();

// A Hunter bot's think: its level, HP share, position and whether its plan is a Flee plan. Keeps the level times and
// the level-1 flees; refreshes the route's choice at most twice a second.
void HunterThink(std::uint32_t bot, const char* noun, int team, int level, float hpFrac, const float* pos, bool fleeing);
// A bot in a death leaf (counted once per death for the Hunters seen; others ignored).
void BotDying(std::uint32_t bot);

// The camp of the Hunter's current route step (its marker), for the Lua's camp conditions; false: none (the default).
bool RoutePoint(std::uint32_t bot, float* out);

// The pull: for a bot of `team` that isn't a Hunter, at `pos` with `hpFrac`: the creature to hit now (the first buff
// camp's biggest); 0 when there is nothing to pull.
std::uint32_t PullTarget(std::uint32_t bot, const char* noun, int team, const float* pos, float hpFrac);
void NotePullRequest(std::uint32_t bot, std::uint32_t target, int res);

// Vanquish: whether a Hunter may try now; a try's result (`host`: the host's rule, else the data's gambit);
// `objective`: at a buff or pig major.
bool VanquishDue(std::uint32_t bot, unsigned long long nowMs);
void NoteVanquish(std::uint32_t bot, bool host, int res, bool objective, std::uint32_t target, const char* targetNoun,
                  float targetHp, unsigned long long nowMs);

// "stats hunterroute:" (false before a Hunter thought)
bool FormatStats(char* buf, std::size_t size);

}  // namespace hunterroute
