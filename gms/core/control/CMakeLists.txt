# gms/core/control: the match host's end of the control channel to dgserver (ADR-2 decisions 6 and 7,
# roadmap C1; docs/HOST_CONTROL.md). Engine-free: a JSON codec, the messages, a Winsock client.
#
# Builds on its own or as a subdirectory of gms/ (add_subdirectory(core/control)); the host DLL links
# the static library dg_control and includes "control/client.h" (include root: gms/core).
#
# Standalone build and test (VS 2022 generator, x86 like the host; any build folder outside the repo
# or the gitignored gms/build works):
#   cmake -S gms/core/control -B <build> -G "Visual Studio 17 2022" -A Win32
#   cmake --build <build> --config Release
#   ctest --test-dir <build> -C Release --output-on-failure     (or run <build>\Release\dg_control_tests.exe)
cmake_minimum_required(VERSION 3.20)
project(dg_control LANGUAGES CXX)

option(DG_CONTROL_TESTS "Build the control channel's tests" ON)

add_library(dg_control STATIC
  json.cpp
  messages.cpp
  client.cpp
  host_control.cpp  # C1b: the host's lifecycle on the channel (gms_script and dgsim.dll)
)
target_include_directories(dg_control PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/..)
target_compile_features(dg_control PUBLIC cxx_std_17)
target_link_libraries(dg_control PUBLIC ws2_32)
if(MSVC)
  # as gms/CMakeLists.txt's dgsim: static CRT (the game ships msvcr100 only), /EHa, /W4
  set_property(TARGET dg_control PROPERTY MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
  target_compile_options(dg_control PRIVATE /W4 /WX /EHa /utf-8)
  target_compile_definitions(dg_control PUBLIC WIN32_LEAN_AND_MEAN NOMINMAX)
endif()

if(DG_CONTROL_TESTS)
  enable_testing()
  add_executable(dg_control_tests tests/control_tests.cpp)
  target_link_libraries(dg_control_tests PRIVATE dg_control)
  # the example lines shared with the Go side (server/internal/gmshost/proto_test.go)
  get_filename_component(DG_CONTROL_EXAMPLES "${CMAKE_CURRENT_SOURCE_DIR}/../../../docs/data/host_control_examples.jsonl" ABSOLUTE)
  target_compile_definitions(dg_control_tests PRIVATE DG_CONTROL_EXAMPLES="${DG_CONTROL_EXAMPLES}")
  if(MSVC)
    set_property(TARGET dg_control_tests PROPERTY MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
    target_compile_options(dg_control_tests PRIVATE /W4 /WX /EHa /utf-8)
  endif()
  add_test(NAME dg_control_tests COMMAND dg_control_tests)

  # a fake host process on this library, for the Go supervisor's interop test
  # (set DG_CONTROL_FAKEHOST=<build>\Release\dg_control_fakehost.exe, then go test ./internal/gmshost)
  add_executable(dg_control_fakehost tests/fake_host.cpp)
  target_link_libraries(dg_control_fakehost PRIVATE dg_control)
  if(MSVC)
    set_property(TARGET dg_control_fakehost PROPERTY MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
    target_compile_options(dg_control_fakehost PRIVATE /W4 /WX /EHa /utf-8)
  endif()
endif()
