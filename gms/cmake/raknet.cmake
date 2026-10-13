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
# the protocol-5 copy also gets the receive-path fixes of cmake/raknet_patches/<file>.patch (split messages,
# ACK/NAK ranges, RangeList and BitStream reads; also
# RakPeer's handshake hardening and the ordered-ahead limit, switched by RakPeer::DgSetHardening
# and stock until then, with DG_RAKNET_HARDENING=1 defined for the users of the library): each patched
# file is written from the original with its @@@OLD/@@@NEW blocks applied (every OLD block must occur exactly once;
# CRLF read as LF), only when the result changed. The protocol-6 build (raknet_probe's research build) stays stock.
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

# `src` with the blocks of `patch` applied, into the variable `out`; a block that doesn't match once is fatal.
function(dg_patch_raknet_file src patch out)
  file(READ "${src}" text)
  string(REPLACE "\r\n" "\n" text "${text}")
  file(READ "${patch}" p)
  string(REPLACE "\r\n" "\n" p "${p}")
  set(n 0)
  while(TRUE)
    string(FIND "${p}" "@@@OLD" a)
    if(a EQUAL -1)
      break()
    endif()
    string(SUBSTRING "${p}" ${a} -1 p)
    string(FIND "${p}" "\n" eol)
    math(EXPR t0 "6")
    math(EXPR tl "${eol} - 6")
    string(SUBSTRING "${p}" ${t0} ${tl} title)
    math(EXPR b0 "${eol} + 1")
    string(SUBSTRING "${p}" ${b0} -1 p)
    string(FIND "${p}" "@@@NEW\n" b)
    string(FIND "${p}" "@@@END\n" c)
    if(b EQUAL -1 OR c EQUAL -1 OR c LESS b)
      message(FATAL_ERROR "${patch}: block '${title}' has no @@@NEW/@@@END")
    endif()
    string(SUBSTRING "${p}" 0 ${b} old)
    math(EXPR n0 "${b} + 7")
    math(EXPR nl "${c} - ${n0}")
    string(SUBSTRING "${p}" ${n0} ${nl} new)
    math(EXPR c1 "${c} + 7")
    string(SUBSTRING "${p}" ${c1} -1 p)
    string(FIND "${text}" "${old}" f1)
    string(FIND "${text}" "${old}" f2 REVERSE)
    if(f1 EQUAL -1 OR NOT f1 EQUAL f2)
      message(FATAL_ERROR "${patch}: block '${title}' does not occur exactly once in ${src}")
    endif()
    string(REPLACE "${old}" "${new}" text "${text}")
    math(EXPR n "${n} + 1")
  endwhile()
  if(n EQUAL 0)
    message(FATAL_ERROR "${patch}: no @@@OLD block")
  endif()
  set(${out} "${text}" PARENT_SCOPE)
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
    # the patched files are written below, not copied
    file(GLOB _patches "${CMAKE_CURRENT_FUNCTION_LIST_DIR}/raknet_patches/*.patch")
    # -DDG_RAKNET_PATCHES=OFF builds the stock receive path (for comparison only: gms_tests' RakNetReceivePathFixes
    # then fails); the patched files are written from the originals unchanged
    option(DG_RAKNET_PATCHES "Apply gms/cmake/raknet_patches to the protocol-5 RakNet" ON)
    foreach(_patch IN LISTS _patches)
      get_filename_component(_pname "${_patch}" NAME)
      string(REGEX REPLACE "\\.patch$" "" _target "${_pname}")
      list(REMOVE_ITEM _files "${RAKNET_SRC_DIR}/${_target}")
    endforeach()
    file(COPY ${_files} DESTINATION "${_dir}")
    foreach(_patch IN LISTS _patches)
      get_filename_component(_pname "${_patch}" NAME)
      string(REGEX REPLACE "\\.patch$" "" _target "${_pname}")
      dg_patch_raknet_file("${RAKNET_SRC_DIR}/${_target}" "${_patch}" _patched)  # checks the blocks either way
      if(NOT DG_RAKNET_PATCHES)
        file(READ "${RAKNET_SRC_DIR}/${_target}" _patched)
        string(REPLACE "\r\n" "\n" _patched "${_patched}")
      endif()
      set(_old "")
      if(EXISTS "${_dir}/${_target}")
        file(READ "${_dir}/${_target}" _old)
      endif()
      if(NOT _old STREQUAL _patched)
        file(WRITE "${_dir}/${_target}" "${_patched}")  # only when it changed, so RakNet isn't rebuilt every configure
      endif()
    endforeach()
    set_property(DIRECTORY APPEND PROPERTY CMAKE_CONFIGURE_DEPENDS ${_patches})
    list(LENGTH _patches _npatches)
    if(DG_RAKNET_PATCHES)
      message(STATUS "RakNet protocol 5: ${_npatches} file(s) patched from cmake/raknet_patches")
    else()
      message(WARNING "RakNet protocol 5: DG_RAKNET_PATCHES=OFF, the stock receive path (for comparison only)")
    endif()
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
  if(protocol EQUAL 5 AND DG_RAKNET_PATCHES)
    # RakPeer's DgHardening (cookies, the nonsense ban, the ordered-ahead limit) exists; gms/core/net uses it
    target_compile_definitions(${name} PUBLIC DG_RAKNET_HARDENING=1)
  endif()
  target_link_libraries(${name} PUBLIC ws2_32)
endfunction()
