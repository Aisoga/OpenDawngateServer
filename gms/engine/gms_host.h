// The match host's GMS (roadmap G2; ADR-2 decisions 1, 4, 5): in host mode dgsim.dll serves the GMS port itself.
// gms/core's endpoint (RakNet 4.081, protocol 5) is bound to DGGMS_BIND once the server simulator has loaded its
// level, and gms/core's GmsSession runs on the engine thread against ServerMatch, an IMatch over the server simulator:
//   - Setup(): GamePrepareForStart from the level the server loaded (its asset id, the server's seed and flags);
//   - HelloReq -> GameSimulator_AddPlayer (0x008F3E20) on the server simulator; the server's cLabsPlayer gets the
//     team, the shaper Noun (mpNounDef), the online id (the HelloReq's user id) and the status;
//   - every status change of the session's status model goes into the server player's mStatus/mStatusProgress;
//   - a disconnect removes the player (GameSimulator_RemovePlayer 0x008F1F00);
//   - D2f: at the client's level-loaded point (0x401, before EnterGame) the player's shaper is deployed on the server
//     (LabsPlayer_DeployCharacter 0x00915400) and the world is created from the server simulator's own objects with
//     the client's reflected writer (replicate.h); the simulator's outgoing vector is forwarded from then on every
//     tick (seam 1: the level's ModifierCreated, PlayerCharacterDeploy, ...); the session's player updates (the first
//     one and the status echoes) and the shaper's mCurrentCreatureId are written from the server's cLabsPlayer
//     (seam 5);
//   - an inject file ([host] inject_file, the format of gms_script's --inject-file) can still add messages at the
//     session's points, for format experiments (G2 sent D2e's AssetData-built world this way).
// ADR-2's loop: GmsHostPoll() runs first in every server tick (RakNet polled, the session's timed steps), then the
// simulation parts. Everything here runs on the game's main thread with the server simulator bound.
// gms/core's log lines (the gms_script format, which run_session.ps1 summarizes) go to dgsim.log.
//   - D7-live: the client's commands (ActionCommandMsgs, AbilityRankUp, ...) go to the command intake (commands.h) with
//     the connection's gameplay index; what the simulator sends through the send stub, the ping relay and the FoW and
//     locomotion updates go out after each tick (GmsHostFlush); [host] commands_file feeds test-side commands into the
//     same intake as if the player had sent them (lines "<seconds after EnterGame> <hex message>", "{caster}" = the
//     player's creature id).
//   - D2g-live (docs/GMS_PROTOCOL.md *Live replication (D2g-live)*): after each tick the replication step: creates of
//     every networked object a player with the world doesn't know (runtime minions, projectiles, ...), the outgoing
//     vector (with the modifier registry, the kind-2 fix and the kind-3 line-of-sight release), per-struct deltas
//     against the field states (90, 9C, 9F, A0, A1, A2; A9 and AA for the players) and ModifierDeleted for ended
//     instances; before the locomotion tick, the network-rate timers (GmsHostTickNetworkRate). The per-tick sends are
//     counted per wire byte (one "replication: last 1.0 s" line) instead of logged one by one.
//   - C1b (control.h): with dgserver's control channel only createGame's roster gets in (gameplay index, team, noun
//     from the roster), each accepted HelloReq is reported with addPlayer and each ended connection with
//     disconnectPlayer, EnterPreGameFlow/EnterGame are the phases preGame/inGame.
// Settings ([host] in dgsim.ini or DGGMS_OPTIONS): world (1), deploy (1), drain (1), player_updates (1), inject_file,
// step_ms (1000), load_timeout_ms (60000), skip_shaper (1), echo_status (1), prepare_flags (2), team (1), noun
// (alpha.Noun 0xEF044AAB), max_connections (12); D7-live: commands (1), shaper_bt (1), locomotion_updates (0 with
// replicate, else 1), command_log_limit (400), grant_ability_points (0), grant_xp (0, D10), commands_file (D11: also
// "<s> xp <amount>" lines and the {nearest:<noun prefix>}/{nearestpos:<noun prefix>} placeholders), rank_log (1, D11);
// D2g-live: replicate (1),
// create_types (1 = every networked noun type but spawn points, 0 = D2f's), network_rate (1), log_first (3), and the
// test wave: test_wave (seconds after EnterGame, 0 = off), test_wave_count (3), test_wave_noun
// (Minion_Mortal_Melee.Noun), test_wave_from (Binding_Mortal_Outer), test_wave_to (Binding_Spirit_Outer),
// test_wave_delete_ms (2000).
#pragma once

#include <cstddef>

// Binds the endpoint (host mode only; DGGMS_BIND, default 127.0.0.1:10042, "off" = none) and creates the session.
// `levelAsset`, `levelName`, `seed`, `levelFlags`: what the server simulator loaded. False if no endpoint runs.
bool GmsHostStart(void* sim, void* levelAsset, const char* levelName, unsigned seed, unsigned levelFlags);

// One poll of the endpoint into the session, then the session's timed steps (ADR-2 loop step 1). Once per server
// tick, with the server simulator current. Does nothing before GmsHostStart succeeded.
void GmsHostPoll();

bool GmsHostActive();

// C1b: the bound GMS port (0 = none), the number of GMS connections, and the session's last state lines before the
// host exits (control.h).
unsigned GmsHostPort();
int GmsHostConnections();
void GmsHostShutdown();

// D7-live, ADR-2's loop step 2: the players' command queues (ServerCommandQueue_TickPlayer) and their creatures'
// behavior trees (commands.h). Once per server tick after the time step, before the Lua scheduler.
void GmsHostTickCommands();

// D2g-live: GameSimulator_UpdateNetworkRateTimers (0x008EE900, its per-frame caller is stripped) on the server
// simulator, so Locomotion_TickMovers marks non-player movers' positions every 200 ms. Every server tick before the
// locomotion tick ([host] network_rate, 1).
void GmsHostTickNetworkRate();

// ABILVFX: every server tick just before DeletePending: the creates of the placeable effects a script made and destroyed
// in this tick (Khagen's Q), built while the objects still exist and sent first by the next replication step ([host]
// short_effects, 1).
void GmsHostBeforeDelete();

// ADR-2's loop steps 4-5, after the simulation: the replication step (D2g-live: creates, the outgoing vector (seam 1),
// updates, modifier ends), what the send stub caught (the sixth seam), the ping relay, test-side commands due
// ([host] commands_file) and the test wave ([host] test_wave), sent to the players.
void GmsHostFlush();

// "endpoint ..., polls n, packets n, connections n, players [...]" for the stats lines.
void GmsHostFormatStats(char* buf, std::size_t size);

// D7-live: the command intake's counters (cmds::FormatStats); false if it is not installed.
bool GmsHostFormatCommandStats(char* buf, std::size_t size);
