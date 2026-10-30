// The second bot brain on the match host (BOTS2): the engine side of gms/core/bots2. Behind `[host] bot_ai=v2`; with
// `classic` (the default) the bots run the classic brain (agents.cpp BotBrainStep: EA's plans, phases and gambits) and
// this module only takes the census, so both brains are measured the same way.
//
// Every think (`bot2_think_ms`, the tuning's thinkMs) the agent step hands over the frame's objects (agents::Tick,
// ObjView); the module builds the World snapshot (shapers with the team's sight, lane minions, jungle creatures,
// structures with the Guardian's shield, the lanes, each team's spawn, Locus and Guardian), lets the core plan each
// team and decide each living bot, and executes the intent with the classic brain's engine glue (agents.h bot2io):
// Move = the follow leaf to a point, Attack = the follow leaf on the target plus the kit's abilities (core/bots/kits.h)
// and the basic attack through BotRequest, Recall = ability_returntobase, Shop = stand (the host's shop visit buys).
// Nothing is asked while an uninterruptible ability holds the bot, so casts and the recall are never cut.
// BOTS2c: the snapshot also holds the wells (wells::Views), their workers, the wards, the ward spots (the level's,
// else the data's), the parasite, and each bot's battle spells and potions (bot2io::Inventory); an intent's extra is
// executed before the intent: a battle spell (slot 7-9) or the ward (slot 1) through BotRequest, a potion through
// bot2io::UseItem (recorded as the bot's item command), and the brain hears the result (Brain::NoteExtra).
//
// Logs: `bots2: …` (the table at the start, state changes up to bot2_log_limit), `BOTS2 IDLE: …` (a living bot outside
// its base without a move, attack or recall: the never-idle rule broken; any line fails a test session), `bots2
// census: …` every 60 s (per bot: gold, XP, level, deaths, idle seconds; the living structures per team), `stats
// bots2:` with the agent stats (every 10 s); since BOTS2c `bots2: bot ... spell|potion|ward ...` per extra (600 lines,
// then every 50th), `bots2: team N well task|well ... threatened|parasite: ...` per change, `bots2: parasite ... down`,
// and `stats bots2c:`. [host] keys: bot_ai (classic), bot2_tune (""), bot2_census (1), bot2_log_limit (400).
#pragma once

#include <cstddef>
#include <cstdint>
#include <vector>

namespace bots2 {

// One object of the frame as the snapshot needs it (filled by agents.cpp from its own readers).
struct ObjView {
  std::uint32_t id = 0;
  int team = 0;
  float pos[3] = {};
  float hp = 0.f, maxHp = 0.f;
  bool player = false;       // a player's creature (a shaper)
  int npcType = -1;          // nNPCType (-1: none)
  float reach = 0.f;         // a tower's aggro range (a Binding's, the Guardian's, the Locus's)
  std::uint32_t target = 0;  // a tower's mTarget
  int playerIndex = -1;      // a shaper's gameplay index
  bool attackable = false;   // BOTS2c, the parasite only: targetable and attackable now (born, not transforming)
};

// Reads the [host] keys (bot_ai, bot2_tune, bot2_census, bot2_log_limit) and logs the tuning table. Once, before the
// agent step runs.
void Configure();
// The second brain drives a team's bots (BotBrainStep is skipped for them).
bool Drives();
// BOTS2c, head to head: [host] bot_ai_team1 / bot_ai_team2 (each "" = bot_ai's) give each team its brain. Whether the
// second brain drives team `team`, or the bot whose creature is `creature`; "v2" or "classic" for a team; the match's
// label ("v2", "classic", "t1v2_t2classic", "t1classic_t2v2").
bool DrivesTeam(int team);
bool DrivesBot(std::uint32_t creature);
const char* BrainOfTeam(int team);
const char* BrainLabel();
// Whether the agent step should hand over the frame now (a think or a census sample is due).
bool Due(unsigned long long nowMs);
// The frame's objects (agents::Tick, the server simulator bound): the census, and with v2 the thinks.
void Frame(void* sim, const std::vector<ObjView>& objects, unsigned long long nowMs);
// A hit (agents::OnDamaged): the census's "dealt damage", the brain's "hit by".
void OnDamage(std::uint32_t victim, std::uint32_t attacker, bool attackerIsShaper, float dealt);
bool FormatStats(char* buf, std::size_t size);
// BOTS2c: the "stats bots2c:" line: the extras (spells by name, potions, wards), the well tasks and the workers the
// teams' shapers killed, the parasite attempts and kills (both brains: the kills; v2 only: the rest).
bool FormatExtraStats(char* buf, std::size_t size);

}  // namespace bots2
