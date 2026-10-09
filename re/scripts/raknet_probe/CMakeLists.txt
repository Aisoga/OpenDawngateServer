# D2a: RakNet 4.081 (BSD, github.com/facebookarchive/RakNet, unpacked to tools/raknet/RakNet, see docs/SETUP.md)
# as static libraries, plus raknet_probe, which replays the Dawngate client's captured RakNet handshake against them
# and serves the GMS port for B9's --listen/--connected. Findings: docs/GMS_PROTOCOL.md "RakNet 4.081 vs. the client".
# A research tool: the match host's own build of RakNet and the GMS session are in gms/ (ADR-2, roadmap G1), and the
# library recipe below comes from there (gms/cmake/raknet.cmake), so it exists once.
#
#   raknet_p6  stock 4.081 sources (RAKNET_PROTOCOL_VERSION 6)
#   raknet_p5  the same sources rebuilt with RAKNET_PROTOCOL_VERSION 5, the version the client speaks (copied into
#              the build tree with that one line replaced; tools/raknet stays untouched)
#   raknet_probe_p6 / raknet_probe_p5  raknet_probe.cpp linked against each library.
#
# Build (VS 2022 x86, MSVC 14.44; CMake bundled with VS, see docs/SETUP.md). The build tree's source must be the main
# checkout's re/scripts/raknet_probe:
#   cmake -S re/scripts/raknet_probe -B tools/raknet/build -A Win32 [-DRAKNET_ROOT=<path to tools/raknet/RakNet>]
#   cmake --build tools/raknet/build --config Release
# Run: see raknet_probe.cpp.
cmake_minimum_required(VERSION 3.20)
project(raknet_probe LANGUAGES CXX)

if(NOT CMAKE_SIZEOF_VOID_P EQUAL 4)
  message(FATAL_ERROR "Configure for x86 (-A Win32): the client and the in-process host are 32-bit.")
endif()

include("${CMAKE_CURRENT_LIST_DIR}/../../../gms/cmake/raknet.cmake")
dg_find_raknet("${CMAKE_CURRENT_LIST_DIR}/../../../tools/raknet/RakNet")
if(NOT RAKNET_FOUND)
  message(FATAL_ERROR "${RAKNET_ERROR}")
endif()

dg_add_raknet(raknet_p6 6)
dg_add_raknet(raknet_p5 5)

foreach(p 5 6)
  add_executable(raknet_probe_p${p} raknet_probe.cpp)
  target_compile_features(raknet_probe_p${p} PRIVATE cxx_std_17)
  target_compile_options(raknet_probe_p${p} PRIVATE /W4)
  target_link_libraries(raknet_probe_p${p} PRIVATE raknet_p${p})
endforeach()
