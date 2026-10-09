# Build stamps (G5): what run_session.ps1 and play.ps1 check a gms/ build against (re/scripts/gms_stale.ps1).
#
# include()d by gms/CMakeLists.txt: dg_build_stamp(<target>) adds a POST_BUILD step that writes <binary>.stamp next to
# the target's binary (gms/build/Release/dgsim.dll.stamp) after every successful build of the target. The Visual Studio
# generator runs it whenever MSBuild builds the project, also when the link was up to date: MSBuild doesn't relink an
# unchanged DLL, so a header nothing includes (D9-core's core/wells/wells.h) left dgsim.dll older than gms/core for good
# (A6's gotcha). The stamp's time is when the target last built successfully; a failed compile or link writes none.
# Its text holds the binary's SHA-256, so the check ignores a stamp that belongs to another binary.
# (Ninja and Makefile generators run POST_BUILD only when they relink; this tree is built with Visual Studio 17 2022.)
#
# Run as a script it writes the stamp:  cmake -DBINARY=<path of the binary> -P gms/cmake/stamp.cmake
if(CMAKE_SCRIPT_MODE_FILE)
  if(NOT BINARY OR NOT EXISTS "${BINARY}")
    message(FATAL_ERROR "stamp.cmake: BINARY='${BINARY}' does not exist")
  endif()
  file(SHA256 "${BINARY}" dg_hash)
  string(TIMESTAMP dg_now "%Y-%m-%dT%H:%M:%SZ" UTC)
  file(WRITE "${BINARY}.stamp" "sha256=${dg_hash}\nbuilt=${dg_now}\n")
  file(TOUCH "${BINARY}.stamp")
else()
  function(dg_build_stamp target)
    add_custom_command(TARGET ${target} POST_BUILD
      COMMAND "${CMAKE_COMMAND}" "-DBINARY=$<TARGET_FILE:${target}>" -P "${CMAKE_CURRENT_FUNCTION_LIST_FILE}"
      COMMENT "build stamp for ${target} (G5)"
      VERBATIM)
  endfunction()
endif()
