# RakNet 4.081 as a static library: stock (RAKNET_PROTOCOL_VERSION 6) or rebuilt for protocol 5, the version the
# Dawngate client speaks (D2a, docs/GMS_PROTOCOL.md "RakNet 4.081 vs. the client"). ADR-2 decision 4: the match host
# links the protocol-5 build. Included by gms/CMakeLists.txt (raknet_p5) and by re/scripts/raknet_probe/CMakeLists.txt
# (raknet_p5 and raknet_p6), so the recipe exists once.
#
# The RakNet source is a download, not in git: github.com/facebookarchive/RakNet unpacked to tools/raknet/RakNet
# (docs/SETUP.md). Binary distributions carry RakNet's BSD notice.
#
#   dg_find_raknet(<default root>)  sets the cache path RAKNET_ROOT (default: the argument), and RAKNET_FOUND,
#                                   RAKNET_SRC_DIR or RAKNET_ERROR in the caller's scope; checks for version 4.081
#   dg_add_raknet(<target> 5|6)     a static library <target> from RAKNET_SRC_DIR
#
# Protocol 5: RakNetVersion.h defines RAKNET_PROTOCOL_VERSION without #ifndef and RakPeer.cpp includes it by quoted
# name, so a -D can't override it. The sources are copied into the build tree (<binary dir>/raknet_p5_src; file(COPY)
# keeps timestamps and skips unchanged files) and that one line is replaced there. tools/raknet stays untouched.
#
# Library defaults otherwise (LIBCAT_SECURITY 0, IPv4 only, 64-bit RakNet::Time, sliding-window congestion control):
# the wire settings the client was found to use (D2a). The CRT is whatever the including project sets
# (gms/: static, as the host DLL needs).

function(dg_find_raknet default_root)
  set(RAKNET_ROOT "${default_root}" CACHE PATH
      "RakNet 4.081 tree (the facebookarchive/RakNet archive unpacked to tools/raknet/RakNet)")
  get_filename_component(_root "${RAKNET_ROOT}" ABSOLUTE)
  set(_src "${_root}/Source")
  set(RAKNET_FOUND FALSE PARENT_SCOPE)
  if(NOT EXISTS "${_src}/RakNetVersion.h")
    set(RAKNET_ERROR "RakNet not found at ${_root}; download it as docs/SETUP.md describes or set -DRAKNET_ROOT=<path>"
        PARENT_SCOPE)
    return()
  endif()
  file(STRINGS "${_src}/RakNetVersion.h" _version REGEX "#define RAKNET_VERSION \"")
  if(NOT _version MATCHES "4\\.081")
    set(RAKNET_ERROR "Expected RakNet 4.081 at ${_root}, found: ${_version}" PARENT_SCOPE)
    return()
  endif()
  set(RAKNET_SRC_DIR "${_src}" PARENT_SCOPE)
  set(RAKNET_FOUND TRUE PARENT_SCOPE)
endfunction()

function(dg_add_raknet name protocol)
  if(NOT RAKNET_SRC_DIR)
    message(FATAL_ERROR "dg_add_raknet: call dg_find_raknet first (RAKNET_SRC_DIR is not set)")
  endif()
  if(protocol EQUAL 6)
    set(_dir "${RAKNET_SRC_DIR}")
  elseif(protocol EQUAL 5)
    set(_dir "${CMAKE_CURRENT_BINARY_DIR}/raknet_p5_src")
    file(GLOB _files "${RAKNET_SRC_DIR}/*")
    list(REMOVE_ITEM _files "${RAKNET_SRC_DIR}/RakNetVersion.h")
    file(COPY ${_files} DESTINATION "${_dir}")
    file(READ "${RAKNET_SRC_DIR}/RakNetVersion.h" _ver)
    string(REPLACE "#define RAKNET_PROTOCOL_VERSION 6" "#define RAKNET_PROTOCOL_VERSION 5" _ver5 "${_ver}")
    if(_ver5 STREQUAL _ver)
      message(FATAL_ERROR "RakNetVersion.h has no '#define RAKNET_PROTOCOL_VERSION 6' line to patch")
    endif()
    set(_ver5_file "${_dir}/RakNetVersion.h")
    set(_ver5_old "")
    if(EXISTS "${_ver5_file}")
      file(READ "${_ver5_file}" _ver5_old)
    endif()
    if(NOT _ver5_old STREQUAL _ver5)
      file(WRITE "${_ver5_file}" "${_ver5}")  # only when it changed, so the library isn't rebuilt every configure
    endif()
  else()
    message(FATAL_ERROR "dg_add_raknet: protocol must be 5 or 6, not ${protocol}")
  endif()
  file(GLOB _sources "${_dir}/*.cpp")
  add_library(${name} STATIC ${_sources})
  target_include_directories(${name} PUBLIC "${_dir}")
  target_compile_definitions(${name} PUBLIC WIN32 _CRT_SECURE_NO_WARNINGS _CRT_NONSTDC_NO_DEPRECATE
                                            _WINSOCK_DEPRECATED_NO_WARNINGS)
  target_compile_options(${name} PRIVATE /W1 /wd4996 /MP)
  target_link_libraries(${name} PUBLIC ws2_32)
endfunction()
