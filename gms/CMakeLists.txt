# Dawngate match host (ADR-1: C++17, MSVC x86, CMake; layout: ADR-2, docs/development/decisions/).
#
#   dgsim.dll, D1's server-mode harness and the match host: the game loads it from a separate working copy
#   (client/run_854_server) in place of PhysXLoader.dll. It forwards PhysXLoader's ten exports to the renamed original
#   and, on the game's main thread, creates a second cGameSimulator with mbClientInstance (+0x17) = 0 and drives it.
#   See docs/SERVER_MODE.md. In host mode (D1d) it is the match host, and since G2 it links gms_core and serves the
#   GMS port itself. Since G4 its sources are in two folders (ADR-2 decision 5):
#   host/     the DLL shell: DllMain and the export forwarders (proxy loading), host mode (the DGGMS_* environment,
#             boot hold, socket guard, hidden window, own folders; dgsim.ini [host] and DGGMS_OPTIONS lookups) and the
#             host's end of the control channel (host/control.cpp, C1b)
#   engine/   in process only, addresses in engine/game.h: the server simulator's lifecycle and frame update
#             (engine/experiment.cpp: the phases, the server tick, the PeekMessage frame hook, dgsim.ini [dgsim]),
#             abilities, damage, death, regen, agents, match and director, fog of war, command intake, replication,
#             ServerMatch (engine/gms_host.cpp: the session against the server simulator, IMatch), the hook and log
#             helpers, and the Lua probes and shims engine/lua/*.lua (installed into the host copy by
#             re/scripts/setup_server_copy.py)
#   core/     no game addresses; builds and is tested without Dawngate.exe (G1):
#     common/   bytes, hex, log sink
#     net/      RakNet 4.081 (protocol 5) endpoint: bind address and port, connections, raw GMS messages; since Q3c
#               arrival and send timing (a RakNet plugin), the network simulator (netsim.h) and the fast lane
#     protocol/ the wire-byte map (docs/data/gms_transport_ids.csv) and the raw fixed layouts
#     session/  GmsSession: Connected, HelloReq, HelloPlayer, ClockSync, the pre-game steps, the status model;
#               talks to the match through IMatch (session/match.h)
#     replication/ (D2g-live) the engine-free part of replication: the modifier registry, the outgoing vector's routing
#     vision/   (D2g-fog-core) fog of war without the engine: the vision model (team bits) and the per-player
#               transitions (93/94/95/A3); the host wires it in D2g-live step 2
#     postgame/ (D8-live) the match end and the post-game: BF 00, C1 01-05, a practice game's rewards, the per-player
#               PostGame machine the host drives (since D8b-core also the karma vote's tally)
#     surrender/ (D8b-core) surrender: CA 00-04 and the per-team SurrenderVote the host will drive (D8b)
#     agents/   (D1b-live) the agent AI's engine-free rules, header-only (the aggro order, gambit choice)
#     matchflow/ (D1c-live) the match flow's engine-free rules (wave schedule, camp timers, the end, energy lines); built
#               into dgsim and gms_tests directly (not gms_core, which the harness has only with RakNet)
#     wells/    (D9-core) the spirit wells' engine-free rules, header-only (capture, locks, workers, well gold, 8C)
#     bots/     (D6b-shims) the bots' host state, header-only (mission board, lanes and fronts, team fill, shopping);
#               the Lua side is engine/lua/dgsim_bots_api.lua
#     banter/   (D11b) the shapers' banter, kill, assist and presence lines and the sequential emotes, header-only (who
#               speaks, which line and variant, the cooldowns, who hears it); engine/banter.cpp sends them
#     watchdog/ (H5) the frozen host: the stall and step-failure rules (rules.h, header-only), the watchdog thread, the
#               thread sample, the stack scan and the stall minidump (monitor.cpp; Windows, no game address); built into
#               dgsim and gms_tests directly, like matchflow/
#     timing/   (Q3c) per match minute: tick durations and lags, traffic per kind, command latency, the CSV (timing.cpp;
#               built into dgsim and gms_tests directly, like matchflow/); clock.h is the one clock the endpoint shares
#     commands/ (D7c) the server command queue's rule for a queued move and the host's takeover, header-only
#     d3d9/     (H1d) the host without Direct3D 9: the plan from the [host] keys and the stand-in IDirect3D9,
#               header-only (engine/startup.cpp patches the start-up with it)
#     control/  (C1) the control-channel client and its JSON Lines codec: its own CMakeLists.txt, added below
#   script/   gms_script.exe, a standalone scripted GMS (D2b-live's script A, D2e's injected messages); since C1b also a
#             match host under dgserver (host mode: the control channel, script/hosted_match.cpp)
#   tests/    gms_tests.exe: protocol bytes, the inject format, the session against a RakNet test client on loopback
#
# Build (no Developer prompt needed; the VS generator finds MSVC itself):
#   cmake -S gms -B gms/build -G "Visual Studio 17 2022" -A Win32 [-DRAKNET_ROOT=<tools/raknet/RakNet>]
#   cmake --build gms/build --config Release
#   ctest --test-dir gms/build -C Release --output-on-failure     (or run gms/build/Release/gms_tests.exe)
# Output (gitignored): gms/build/Release/dgsim.dll (install it with re/scripts/setup_server_copy.py),
# gms_script.exe (run_session.ps1 -GmsProbe session runs it) and gms_tests.exe; dgsim.dll.stamp and gms_script.exe.stamp
# after each successful build of those two (G5: what run_session.ps1 and play.ps1 check them against; cmake/stamp.cmake).
# Without RakNet (tools/raknet/RakNet, docs/development/building.md) only the harness is built.
# Never name a folder under gms/ "tools": the root .gitignore hides every folder of that name.
cmake_minimum_required(VERSION 3.20)
project(dawngate_gms LANGUAGES CXX)

if(NOT CMAKE_SIZEOF_VOID_P EQUAL 4)
  message(FATAL_ERROR "gms/ must be built for 32-bit x86 (Dawngate.exe is x86): use -A Win32")
endif()

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
# Static CRT: the DLL must not depend on a newer VC runtime than the game ships (msvcr100). Applies to every target
# here (RakNet and gms_core too), since the host DLL will link them (G2).
set(CMAKE_MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")

enable_testing()

# ---- dgsim.dll (D1; host/ + engine/ since G4) ------------------------------------------------------------------------
add_library(dgsim SHARED
  host/dllmain.cpp
  engine/hook.cpp
  engine/log.cpp
  engine/experiment.cpp
  engine/abilities.cpp
  engine/damage.cpp            # D1f-live: the TakeDamage core (docs/DAMAGE.md)
  engine/death.cpp             # D1f-live: death processing, rewards, gold
  engine/passives.cpp          # D6c: the shapers' passives at the deploy and each respawn
  engine/itempassives.cpp      # ITEMDEATH: item passives end at death, start again at the respawn (core/damage)
  engine/mapfix.cpp            # MAPLUA: MM_Map_v3's script fix-ups (dgsim_mapfix.lua), MM's own base regen (opt-in)
  engine/shop.cpp              # D15c: the shop's purchase effect (onItemPurchased, GetItemFx*)
  engine/refund.cpp            # REFUND1: a sale in the base undoes the purchase (rules: core/commands/refund.h)
  engine/volumes.cpp           # D12c: attached trigger volumes follow their objects
  engine/effects.cpp           # FOGSTACKS: an ended modifier's attached effects are destroyed
  engine/components.cpp        # D1j: the ability data containers' release, the server pools' census
  engine/regen.cpp             # D1i: health and resource regeneration
  engine/agents.cpp
  engine/hunterroute.cpp       # HUNTERROUTE: the Hunters' opening route, Vanquish, the first buff's pull (core/bots/route.h)
  engine/wardspots.cpp         # MAPED4b: the level's own ward spots (core/bots/wardspots.h)
  engine/match.cpp            # D1c-live: the match lifecycle (clock, Guardians, UI data, the end)
  engine/director.cpp          # D1c-live: waves, lanes, camps, the parasite
  engine/crowd.cpp             # D1k: the server space's NavPower constraint solver (the minions push each other)
  engine/wells.cpp             # D9: the spirit wells, workers and well gold (rules: core/wells)
  engine/matchstats.cpp        # MSTATS1: the match statistics (seatStats, matchTimeline; rules: core/stats)
  core/stats/stats.cpp         # MSTATS1: the statistics' accumulator, engine-free (also in gms_tests)
  engine/matchstats_cc.cpp     # MSTATS4: crowd control and wards (rules: core/stats/cc.h)
  core/stats/cc.cpp            # MSTATS4: crowd control caused and endured, wards placed, engine-free (also in gms_tests)
  engine/matchstats_sources.cpp  # MSTATS3: damage and heal by source, the heal-core detour (rules: core/stats/sources)
  core/stats/sources.cpp       # MSTATS3: the source table and its fallbacks, engine-free (also in gms_tests)
  engine/matchstats_gold.cpp   # MSTATS2: gold and XP by source (the telemetry block), the roles' bonuses
  core/stats/gold.cpp          # MSTATS2: gold/XP by source and the role parts, engine-free (also in gms_tests)
  engine/bots2.cpp             # BOTS2: the second bot brain on the host (snapshot, thinks, execution, census)
  core/bots2/brain.cpp         # BOTS2: the second bot brain's decision layer, engine-free (also in gms_tests)
  engine/matchrec.cpp          # MATCHREC: the match recorder ([host] match_record; the file: core/matchrec)
  core/matchrec/matchrec.cpp   # MATCHREC: the recorder's records, buffered writer, size cap and rate (also in gms_tests)
  core/matchflow/flow.cpp      # D1c-live: the match flow's engine-free rules (also in gms_tests)
  engine/startup.cpp           # H1-live: the headless host's start-up changes (stage 2, [host] keys)
  engine/guards.cpp            # H5: every step of the host's frame under SEH (skip, count, report; step_fail_s)
  core/watchdog/monitor.cpp    # H5: the watchdog thread, the thread sample, the stall minidump (also in gms_tests)
  core/timing/timing.cpp       # Q3c: tick, traffic and command-latency summaries (also in gms_tests)
  engine/perf.cpp              # Q3c: the host's timing, traffic, latency and records per match minute (host_timing.csv)
  host/host.cpp
  host/headless.cpp            # H1-live: stage 2's [host] keys, main-loop pacing, the LAA reservation test
  host/watchdog.cpp            # H5: the host's watchdog (a stalled tick: log, minidump, report, exit 6)
  host/forward.cpp
)
target_compile_options(dgsim PRIVATE /W4 /EHa /wd4100)
target_compile_definitions(dgsim PRIVATE WIN32_LEAN_AND_MEAN NOMINMAX _CRT_SECURE_NO_WARNINGS)
target_link_libraries(dgsim PRIVATE user32 kernel32 winmm)  # winmm: timeBeginPeriod (H1-live core_only)
# D1b-live: header-only engine-free rules (core/agents) are included from gms/, with or without gms_core; G4: so are
# host/ and engine/ headers across the two folders ("engine/log.h", "host/control.h")
target_include_directories(dgsim PRIVATE "${CMAKE_CURRENT_LIST_DIR}")

# ---- core (G1): RakNet 4.081 protocol 5, protocol, session ----------------------------------------------------------
# (dgsim links it below since G2; without RakNet dgsim gets engine/gms_host_none.cpp, a host without a GMS endpoint)
include("${CMAKE_CURRENT_LIST_DIR}/cmake/raknet.cmake")
dg_find_raknet("${CMAKE_CURRENT_LIST_DIR}/../tools/raknet/RakNet")

if(RAKNET_FOUND)
  dg_add_raknet(raknet_p5 5)

  add_library(gms_core STATIC
    core/common/bytes.cpp
    core/common/log.cpp
    core/net/endpoint.cpp
    core/protocol/wire.cpp
    core/protocol/messages.cpp
    core/protocol/commands.cpp
    core/protocol/check.cpp           # the strict check of every client message
    core/protocol/shaper_select.cpp   # D3-core: ShaperSelectMsgs and C0 02, byte for byte
    core/session/match.cpp
    core/session/gms_session.cpp
    core/session/shaper_select.cpp    # D3-core: the match-wide shaper select
    core/session/pump.cpp
    core/replication/modifiers.cpp  # D2g-live: the modifier registry
    core/replication/outgoing.cpp   # D2g-live: the outgoing vector's routing, per-second counts
    core/postgame/postgame.cpp      # D8-live: the match end and the post-game (BF 00, C1 01-05, the rewards)
    core/surrender/surrender.cpp    # D8b-core: surrender (CA 00-04, the per-team vote)
    core/vision/vision.cpp          # D2g-fog-core: the fog-of-war vision model
    core/vision/fog.cpp             # D2g-fog-core: the per-player fog transitions, 93/94/95/A3
  )
  # Includes are written from gms/ ("core/session/gms_session.h"), so host, script and tests read the same.
  target_include_directories(gms_core PUBLIC "${CMAKE_CURRENT_LIST_DIR}")
  target_compile_options(gms_core PRIVATE /W4)
  target_compile_definitions(gms_core PRIVATE WIN32_LEAN_AND_MEAN NOMINMAX _CRT_SECURE_NO_WARNINGS)
  target_link_libraries(gms_core PRIVATE raknet_p5)

  # ---- script: the standalone scripted GMS ------------------------------------------------------------------------
  add_library(gms_scripted_match STATIC script/scripted_match.cpp)
  target_compile_options(gms_scripted_match PRIVATE /W4)
  target_link_libraries(gms_scripted_match PUBLIC gms_core)

  add_executable(gms_script script/main.cpp
                            script/hosted_match.cpp)  # C1b: host mode under dgserver (the control channel)
  target_compile_options(gms_script PRIVATE /W4)
  target_compile_definitions(gms_script PRIVATE _CRT_SECURE_NO_WARNINGS WIN32_LEAN_AND_MEAN NOMINMAX)
  target_link_libraries(gms_script PRIVATE gms_scripted_match gms_core)

  # ---- the host's GMS (G2): dgsim.dll serves the GMS port in host mode ----------------------------------------------
  target_sources(dgsim PRIVATE engine/gms_host.cpp engine/replicate.cpp    # D2f: the world from the simulator
                               engine/commands.cpp                         # D7-live: command intake
                               engine/fow.cpp                              # D2g-live step 2: fog of war
                               engine/bots.cpp                             # D6a: bot players, team fill, nBotHost
                               engine/perks.cpp                            # D3-perks: a perk page on the server
                               engine/surrender.cpp                        # D8b: surrender's tuning, totals, 8B byte
                               engine/banter.cpp)                          # D11b: banter, kill and presence lines
  target_link_libraries(dgsim PRIVATE gms_scripted_match gms_core raknet_p5)

  # ---- tests ------------------------------------------------------------------------------------------------------
  add_executable(gms_tests
    tests/main.cpp
    tests/test_protocol.cpp
    tests/test_commands.cpp
    tests/test_script.cpp
    tests/test_session.cpp
    tests/test_replication.cpp
    tests/test_agents.cpp       # D1b-live: the aggro order, battle ready
    tests/test_brain.cpp        # D1b-live: gambit choice, cooldowns, conditions, links
    tests/test_flow.cpp         # D1c-live: waves, camp timers, the end, energy lines, tower slots
    tests/test_camps.cpp        # CAMPICONS: the jungle camps' minimap icons as each team's knowledge
    tests/test_damage.cpp       # D1f-live: PRD (T9), defense multiplier, reward split, passive gold; D1i: regen
    tests/test_damage_edge.cpp  # D13: the combat edge cases (core/damage/pipeline.h)
    tests/test_kills.cpp        # D1h: assists, kill book, player-kill gold and XP, the death recap's bytes
    tests/test_postgame.cpp     # D8-live: the post-game's bytes, the practice rewards, the PostGame machine
    tests/test_vision.cpp       # D2g-fog-core: the vision model, hysteresis, terrain, the fog transitions
    tests/test_wells.cpp        # D9-core: the spirit wells (capture, locks, workers, well gold, the 8C mask)
    tests/test_surrender.cpp    # D8b-core: surrender's bytes and the vote; the karma tally is in test_postgame.cpp
    tests/test_shaper_select.cpp  # D3-core: script B's bytes, ShaperSelect's rules and timers (O1-O5)
    tests/test_bots.cpp         # D6b-shims: the bots' missions, lanes and fronts, team fill, shopping
    tests/test_guidebuild.cpp   # DESECBOT-2: a profile's own build with its choices (core/bots/guidebuild.h)
    tests/test_moyabot.cpp      # MOYABOT: Moya on the Hunter AI (profile, kit rules, guide build)
    tests/test_campfinish.cpp   # CAMPFINISH: a Hunter finishes the camp he wins against (core/agents/campfinish.h)
    tests/test_campreach.cpp    # CAMPREACH, CAMPLEASH: big creeps pass small ones, a fight in the camp holds
    tests/test_hunterroute.cpp  # HUNTERROUTE: the opening route, Vanquish's pick, the pull (core/bots/route.h)
    tests/test_maped4.cpp       # MAPED4: camp roles from data, the route from the camps, the parasite rally, level data
    tests/test_maped4b.cpp      # MAPED4b: the level's own ward spots (core/bots/wardspots.h)
    tests/test_nullpos.cpp      # NULLPOS: (0, 0) as the bots' null position (core/agents/nullpos.h, lost.h's clear)
    tests/test_killfinish.cpp   # KILLFINISH: stay to finish a won trade, the damage ring, BOTCYCLE's plan chain
    tests/test_botdefend.cpp    # BOTDEFEND: shoot back while backing off or caught fleeing, no recall while chased
    tests/test_bottower.cpp     # BOTTOWER: tower dangers, the keep-out, REFLEXWALK, the time under tower fire
    tests/test_mikella.cpp      # MIKELLABOT: Mikella's profile, the Gladiator pin, her kit layer and guide build
    tests/test_draft.cpp        # BOTDRAFT: a custom game's bots draw after the humans (core/bots/draft.h)
    tests/test_lanesplit.cpp    # BOTLANESPLIT: the bots divide each role over the lanes (core/bots/lanesplit.h)
    tests/test_maphost.cpp      # MAPHOST: no Hunter seat without camps, the lanes of a one-lane level
    tests/test_mapbase.cpp      # MAPBASE: two Binding tiers, three Cores, the Locus (MM_Map_v3's base)
    tests/test_mapmines.cpp     # MAPMINES: the gold mines no player reaches (island markers), MM_Map_v3's full mines
    tests/test_mapstrider.cpp   # MAPSTRIDER: the lane agents' waves and nouns (core/matchflow/laneagents.h)
    tests/test_maplua.cpp       # MAPLUA: MM_Map_v3's script fix-ups, the base regen choice (core/matchflow/mapfix.h)
    tests/test_watchdog.cpp     # H5: the stall and step rules, a real watchdog thread against a test thread, the dump
    tests/test_d14.cpp          # D14: the map rules (reward buffs, aggro on sight, camp spots, the Guardian's shield)
    tests/test_timing.cpp       # Q3c: the tick, traffic and command-latency arithmetic, the CSV, the simulator's rule
    tests/test_net.cpp          # Q3c: the endpoint's timing and network simulator against a RakNet client on loopback
    tests/test_limits.cpp       # the network limits (NetLimiter) and the endpoint with them on, on loopback
    tests/test_banter.cpp       # D11b: the banter, kill, assist and presence lines, the sequential emotes
    tests/test_command_queue.cpp  # D7c: a move during an attack order (the queued-move step, the takeover)
    tests/test_d3d9.cpp         # H1d: the host without Direct3D 9 (the plan, the verdict, the stand-in IDirect3D9)
    tests/test_check.cpp        # the strict message check (every type's malformed inputs, the world, the guard)
    tests/test_stats.cpp        # MSTATS1: the match statistics' accumulator (core/stats), the lines and the line budget
    tests/test_stats_cc.cpp     # MSTATS4: crowd control (kinds, intervals, unions, teams), wards placed (core/stats/cc.h)
    tests/test_stats_gold.cpp   # MSTATS2: gold and XP by source, the role parts (core/stats/gold)
    tests/test_matchrec.cpp     # MATCHREC: the recorder's records, the writer's buffer and size cap, the rate
    tests/test_refund.cpp       # REFUND1: the undo history per slot, its clearing, the undo plan (core/commands/refund.h)
    tests/test_itempassives.cpp # ITEMDEATH: item passives while dead and at the respawn (core/damage/itempassives.h)
    tests/test_fogstacks.cpp    # FOGSTACKS: the attached effects an ended modifier leaves (core/replication/effect_ledger.h)
    tests/test_fuzz.cpp         # fuzzing the parsers, the session and RakNet's receive path (GMS_FUZZ_SECONDS)
    tests/test_hardening.cpp    # RakNet's handshake cookies, no IP ban, short REQUEST_1, the ordered-ahead limit
    tests/test_bots2.cpp        # BOTS2: the second bot brain (core/bots2: states, team plan, never idle, census)
    core/matchflow/flow.cpp
    core/watchdog/monitor.cpp
    core/timing/timing.cpp
    core/stats/stats.cpp
    core/stats/cc.cpp
    core/stats/sources.cpp
    core/stats/gold.cpp
    core/bots2/brain.cpp
    core/matchrec/matchrec.cpp
  )
  add_dependencies(gms_tests gms_script)  # D3-core: a test runs gms_script.exe --shaper-select on loopback
  target_compile_options(gms_tests PRIVATE /W4)
  target_compile_definitions(gms_tests PRIVATE _CRT_SECURE_NO_WARNINGS
                                               GMS_REPO_ROOT="${CMAKE_CURRENT_LIST_DIR}/..")
  target_link_libraries(gms_tests PRIVATE gms_scripted_match gms_core raknet_p5)
  add_test(NAME gms_tests COMMAND gms_tests)
else()
  message(WARNING "${RAKNET_ERROR}. Building only the harness (dgsim, without its GMS endpoint); gms_core, gms_script "
                  "and gms_tests need RakNet.")
  target_sources(dgsim PRIVATE engine/gms_host_none.cpp)
endif()

# ---- core/control (C1): control-channel client and JSON Lines codec --------------------------------------------------
# Its own CMakeLists.txt defines its targets (it needs no RakNet; tests can use tests/check.h and add_test).
if(EXISTS "${CMAKE_CURRENT_LIST_DIR}/core/control/CMakeLists.txt")
  add_subdirectory(core/control)
  # D1h: the host encodes its playerFinish lines with the channel's codec; C1b: dgsim and gms_script speak the
  # channel in host mode (HostControl)
  target_link_libraries(dgsim PRIVATE dg_control)
  target_sources(dgsim PRIVATE host/control.cpp)  # C1b: the host's end of the channel (host/control.h)
  if(TARGET gms_script)
    target_link_libraries(gms_script PRIVATE dg_control)
  endif()
  # D3-live step 4: the shaper select content from createGame (script/roster_shaper.h) is tested in gms_tests
  if(TARGET gms_tests)
    target_link_libraries(gms_tests PRIVATE dg_control)
  endif()
endif()

# ---- physcook (MAPPHYS): a level's PhysX triangle mesh, cooked with the game's own PhysXCooking.dll ---------------
# physcook.exe <PhysXCooking.dll> <in.dgmi> <out.bin>: a developers' tool (the mesh builder runs it; the setup ships the
# cooked MM_Map_v3 mesh as data, server/internal/setup/mapmm/physmesh); its engine-free part is the
# header core/physx/cook.h, tested in gms_tests (tests/test_physcook.cpp). Needs no RakNet; no part of dgsim.
add_executable(physcook physcook/main.cpp)
target_compile_options(physcook PRIVATE /W4)
target_compile_definitions(physcook PRIVATE WIN32_LEAN_AND_MEAN NOMINMAX _CRT_SECURE_NO_WARNINGS)
target_include_directories(physcook PRIVATE "${CMAKE_CURRENT_LIST_DIR}")
if(TARGET gms_tests)
  target_sources(gms_tests PRIVATE tests/test_physcook.cpp)
endif()

# ---- build stamps (G5): <binary>.stamp after every successful build, also when MSBuild doesn't relink ---------------
# run_session.ps1 and play.ps1 compare gms/'s sources with the stamp, not with the binary (re/scripts/gms_stale.ps1)
include("${CMAKE_CURRENT_LIST_DIR}/cmake/stamp.cmake")
dg_build_stamp(dgsim)
if(TARGET gms_script)
  dg_build_stamp(gms_script)
endif()
