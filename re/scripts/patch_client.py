"""Patch a *working copy* of Dawngate.exe (build 1.0.903.854) so it talks to our server (server/).

Patches (each checks the original bytes first):
  ca       Replace the modulus of the built-in "OTG3 Certificate Authority" (DirtySDK ProtoSSL CA table,
           entry at 0x014647A0, 1024-bit modulus at 0x01301D48, exponent 3) with our own CA's modulus
           (server/certs/ca_modulus.bin from server/tools/make_certs.py), so certificates signed by
           that CA are trusted. Re-run this after regenerating the certificates.
  ca-flag  Clear bit 0 of the OTG3 entry's flags (0x01464960). With the bit set, ProtoSSL's certificate
           verification (0x00CDD690) only accepts OTG3-issued certificates whose common name matches
           "*.ea.com" or "*.easports.com" (else error -100); cleared, our CN=127.0.0.1 certificate passes.
  redirector  Replace the prod Blaze redirector host "gosredirector.ea.com" (0x012E2AD8) with our server's
           address (127.0.0.1 in the local profile). The port (42127) and TLS stay as they are. The client
           checks the redirector certificate's CN (or a subjectAltName) against this string (0x00CE1220,
           error 0x100A), so the certificate must be made for it: make_certs.py <dir> <server host>.
  noupnp   No UPnP on the LAN (roadmap A3). DirtySDK's NetConnStartup (0x00CDBE90) only creates
           ProtoUPnP if its parameters lack "-noupnp"; the JNZ after that strstr (0x00CDBF53) becomes a
           JMP, so the client always runs as if "-noupnp" was given: no SSDP M-SEARCH, no SOAP to the
           router, no AddPortMapping "EA Tunnel".
  upnp-disc  Guard in case anything else drives ProtoUPnP: ProtoUpnpControl's 'disc' command
           (0x00CEB0F1) returns -1 instead of starting discovery, and every other command needs a
           discovered device. Details in docs/RUNNING_THE_CLIENT.md, "Network isolation".
  loopback  Narrowed sockets (roadmap A4; the address is selectable since A5). A 40-byte code cave
           rewrites an IPv4 INADDR_ANY in bind()'s sockaddr to one address (127.0.0.1 in the local
           profile) and jumps to WSOCK32!bind ([0x011AC9CC]); the five `call [bind]` sites and the thunk
           `jmp [bind]` (0x00FD6660) go through it. .text has no padding run long enough (the longest CC
           run is 15 bytes), so the cave sits in the file-alignment slack after .text's last byte
           (0x011AB100) and .text's VirtualSize is raised to its SizeOfRawData to map it. The exe has no
           relocations and no ASLR, so absolute addresses are safe. Same rewrite as the D1 harness's IAT
           hook (gms/engine/experiment.cpp BindHook). Details in docs/RUNNING_THE_CLIENT.md, "Inbound:
           Windows Firewall".
  data-folders  Only with --data-suffix X (roadmap A6, a second test lane): the client's per-user folders
           get the suffix, so a second client copy keeps its own %APPDATA%\\DawngateDataX (Application.log,
           Preferences) and Documents\\DawngateX (portraits.snap). SporeApp_Initialize (0x006B2140) passes
           the defaults L"DawngateData" (0x011FA4F8, 32 bytes before the next string) and L"Dawngate"
           (0x011FA598, 20 bytes) to the registry lookup 0x00A8D0B0 (HKLM ...\\Electronic Arts\\Dawngate
           AppDir / PlayerDir; absent on this PC), which keeps them unless the value exists; their length
           is counted at run time, so a longer name fits as long as the zero padding after it holds it
           (one more character for the Documents name). X is one letter or digit.
  raknet-loopback  Only when the sockets are narrowed to a loopback address other than 127.0.0.1 (A6, lane
           b): RakNet's IPV4_LOOPBACK literal "127.0.0.1" (0x011FDBB8; IPV6_LOOPBACK "::1" follows at
           0x011FDBC4; pointer 0x013F6AC4) becomes that address. RakNet believes it bound the wildcard (the
           cave narrows it underneath), so its bind-time send test (4 zero bytes to its own port, seen in
           a6_run3) went to 127.0.0.1:<its port>, i.e. into the other lane's address. The literal is used
           only by RakNet's SystemAddress code (RakNet_SystemAddress_FixForIPVersion 0x006C3520,
           RakNet_SystemAddress_SetBinaryAddress 0x006C3170 for "localhost", one string compare at
           0x006C3F9A), so for that copy "loopback" means its own address.
  raknet-lan  Only in the lan profile with --client-bind (A5b, found live-critical): RakPeer::Startup
           (0x006D0060) binds the game socket (narrowed by the cave to the LAN address), then sends 4 zero bytes
           to SetToLoopback(4) (0x006C3460: the IPV4_LOOPBACK pointer at 0x013F6AC4) and gives up with result 7
           (SOCKET_FAILED_TEST_SEND) when that send fails. Windows refuses a send from a LAN-bound socket to
           127.0.0.1 (WSAEADDRNOTAVAIL 10049, tried on this PC), so the copy would never start its game network
           and could never reach a GMS. The address doesn't fit the 12-byte literal, so the pointer is repointed
           to a copy of the bound address in the cave's slack (LAN_LOOPBACK_STRING, mapped by the loopback patch);
           the literal itself keeps "127.0.0.1" for its other two users ("localhost", the is-local compare).
  laa      Since Q3b, in every profile (--laa off leaves it out): IMAGE_FILE_LARGE_ADDRESS_AWARE in the PE header
           (characteristics 0x0103 -> 0x0123), so on 64-bit Windows the 32-bit client gets 4 GB of address space
           instead of 2 GB. In a match the client uses about 1.6 GB of its 2 GB and its largest free block is 92-124 MB
           (43-75 MB back in the lobby after a match); a D3D9 draw that needs more fails with D3DERR_OUTOFVIDEOMEMORY
           (q3_run4; docs/SERVER_MODE.md *The client over a long match (Q3b)*). Checked: q3b_run3 played a whole
           30-minute match with the bit and the low 2 GB reserved from outside (va_map.py --reserve-low), so the
           match's allocations lay above 2 GB, without a new assert; H1-live did the same for the engine core alone
           (the host's laa_reserve=2).

Profiles (roadmap A5, docs/RUNNING_THE_CLIENT.md "Playing across machines (A5)"):
  local    (default) everything above; redirector host 127.0.0.1 (or a loopback address given as [host]
           or --server-host) and every wildcard bind narrowed to that loopback address (127.0.0.1 by
           default; --client-bind may name another loopback address). With --laa off the output is
           identical to the A4 version of this script for 127.0.0.1 (Q3b's default adds the LAA bit). Lane b (A6, docs/RUNNING_THE_CLIENT.md *Test lanes (A6,
           A6b)*) is this profile with 127.0.0.2 and --data-suffix B, lanes c and d (A6b) with 127.0.0.3 / C
           and 127.0.0.4 / D (re/scripts/setup_lane.py).
  lan      for a server on another machine (or on this one's LAN address): the redirector host is
           --server-host (an IPv4 address that isn't loopback or wildcard; public ones too). The loopback patch
           is left out, so the client's sockets bind 0.0.0.0 as shipped, unless --client-bind <IPv4>
           narrows them to that address of this machine instead (then also raknet-lan). ca, ca-flag, noupnp
           and upnp-disc stay.

The first run saves the untouched exe as Dawngate.exe.orig next to it; every run patches from that
backup, so re-running is safe and switching profiles is just another run. Refuses to touch the pristine
extractions under client/ia_*. patch_bytes() does the work on a byte buffer (tests:
re/scripts/test_patch_client.py).

--show prints what an exe carries (redirector host, bind cave address, data folders, the CA modulus's
SHA-256 prefix) as key=value lines and changes nothing; run_session.ps1 -Lane uses it to check a lane's copy.

Usage:
    .venv/Scripts/python -I re/scripts/patch_client.py <working copy Dawngate.exe> <ca_modulus.bin> [host]
        [--profile local|lan] [--server-host <IPv4>] [--client-bind <IPv4>] [--data-suffix <X>]
    .venv/Scripts/python -I re/scripts/patch_client.py --show <Dawngate.exe>
e.g. .venv/Scripts/python -I re/scripts/patch_client.py client/run_854/Dawngate/game/Dawngate.exe server/certs/ca_modulus.bin
     .venv/Scripts/python -I re/scripts/patch_client.py client/run_854_b/Dawngate/game/Dawngate.exe
        server/certs/lane_b/ca_modulus.bin 127.0.0.2 --data-suffix B
     .venv/Scripts/python -I re/scripts/patch_client.py <copy>/Dawngate/game/Dawngate.exe server/certs/lan/ca_modulus.bin
        --profile lan --server-host 192.168.1.20
"""
import argparse
import hashlib
import ipaddress
import os
import shutil
import sys

import pefile

IMAGE_BASE = 0x00400000
OTG3_ENTRY = 0x014647A0          # ProtoSSLCACertT: Country[32] State[32] City[32] Org[32] Unit[256] Common[64] ...
OTG3_FLAGS = 0x01464960          # u32 flags; bit 0 = only *.ea.com / *.easports.com subjects
OTG3_KEYINFO = 0x01464964        # u32 modulus size, u32 modulus pointer, u32 exponent size, exponent bytes
OTG3_MODULUS = 0x01301D48
OTG3_MODULUS_HEAD = bytes.fromhex("9275a15b080240b8")
REDIRECTOR_HOST = 0x012E2AD8
REDIRECTOR_ORIG = b"gosredirector.ea.com\0"
# NetConnStartup: push "-noupnp"; push params; mov [netconn],esi; call strstr; add esp,8; test eax,eax
NOUPNP_STRSTR = 0x00CDBF3D
NOUPNP_STRSTR_ORIG = bytes.fromhex("6848173001" "57" "8935e022bf01" "e8f00b3000" "83c408" "85c0")
# ... jnz +0x20 (skip ProtoUpnpCreate); call ProtoUpnpCreate; mov [esi+0x10],eax
NOUPNP_JNZ = 0x00CDBF53
NOUPNP_JNZ_ORIG = bytes.fromhex("7520" "e8e6f70000" "894610")
NOUPNP_JMP = b"\xeb"                      # jmp +0x20: always take the "-noupnp" path
# ProtoUpnpControl 'disc': call (reset discovery state); mov dword [esi+0xaa8],1; xor eax,eax; pop esi
UPNP_DISC = 0x00CEB0F1
UPNP_DISC_ORIG = bytes.fromhex("e86af3ffff" "c786a80a000001000000" "33c0" "5e")
UPNP_DISC_NEW = bytes.fromhex("83c8ff" "eb0c")  # or eax,-1; jmp 0x00CEB102 (pop esi; ...; ret)
# Narrowed sockets: every reference to the bind IAT slot in the exe (none outside .text)
BIND_IAT = 0x011AC9CC                     # WSOCK32!bind, imported by ordinal 2
BIND_CALLS = (
    0x006D9344,   # RakNet SocketLayer: probe whether a port is free (bind, then closesocket)
    0x006D94B7,   # RakNet SocketLayer: CreateBoundSocket (the game network socket after login)
    0x006D9551,   # ... same, retry after WSAEADDRINUSE
    0x0084923B,   # utfsockets Socket_imp_Win32.cpp: Socket::Bind
    0x0107E04A,   # a socket wrapper's bind(this+4) (no Ghidra function yet)
)
BIND_THUNK = 0x00FD6660                   # jmp [bind], called by DirtySDK's SocketBind (0x00CE7EF0)
CALL_BIND = bytes.fromhex("ff15") + BIND_IAT.to_bytes(4, "little")
JMP_BIND = bytes.fromhex("ff25") + BIND_IAT.to_bytes(4, "little")
TEXT_VSIZE = 0x00DAA0FB                   # .text VirtualSize; SizeOfRawData is 0x00DAA200
LOOPBACK_CAVE = 0x011AB100                # in .text's file-alignment slack (zeros up to 0x011AB200)


def bind_cave_code(address="127.0.0.1"):
    """The cave: narrow an IPv4 INADDR_ANY in bind()'s sockaddr to `address`, then jmp [bind]."""
    return bytes.fromhex(
        "8b442408"          # mov eax,[esp+8]          ; name
        "85c0"              # test eax,eax
        "741a"              # jz go
        "837c240c08"        # cmp dword [esp+0xc],8    ; namelen
        "7c13"              # jl go
        "66833802"          # cmp word [eax],2         ; AF_INET
        "750d"              # jne go
        "83780400"          # cmp dword [eax+4],0      ; INADDR_ANY
        "7507"              # jne go
        "c74004"            # mov dword [eax+4],<address>  (network order: 127.0.0.1 = 0x0100007F)
    ) + ipaddress.IPv4Address(address).packed + JMP_BIND  # go: jmp [bind]  (eax and flags are free)


LOOPBACK_CAVE_CODE = bind_cave_code()
PROFILES = ("local", "lan")
# A6: the per-user folder names (UTF-16LE, NUL-terminated) and the bytes each may use up to the next string
APPDIR_DEFAULT = 0x011FA4F8               # L"DawngateData": %APPDATA%\DawngateData
APPDIR_ROOM = 0x20                        # next: L"HKEY_LOCAL_MACHINE\...\PlayerDir" at 0x011FA518
PLAYERDIR_DEFAULT = 0x011FA598            # L"Dawngate": Documents\Dawngate
PLAYERDIR_ROOM = 0x14                     # next: L"noAsserts" at 0x011FA5AC
DATA_FOLDERS = ((APPDIR_DEFAULT, APPDIR_ROOM, "DawngateData"), (PLAYERDIR_DEFAULT, PLAYERDIR_ROOM, "Dawngate"))
# A6: RakNet's IPV4_LOOPBACK (and the padding before IPV6_LOOPBACK "::1" at 0x011FDBC4)
RAKNET_LOOPBACK = 0x011FDBB8
RAKNET_LOOPBACK_ORIG = b"127.0.0.1\0\0\0::1\0"
RAKNET_LOOPBACK_ROOM = 12
# A5b: RakNet's `const char *IPV4_LOOPBACK` (read by SetToLoopback 0x006C3460 and FixForIPVersion 0x006C3520) and where
# the lan profile's narrowed address goes instead (16 bytes in the cave's slack, after the 40-byte cave)
RAKNET_LOOPBACK_PTR = 0x013F6AC4
LAN_LOOPBACK_STRING = 0x011AB130
LAN_LOOPBACK_ROOM = 16
# Q3b: IMAGE_FILE_HEADER.Characteristics (build 854: relocations stripped, executable, 32-bit machine) and the LAA bit
CHAR_BUILD = 0x0103
CHAR_LAA = 0x0020


def wide(text):
    """A NUL-terminated UTF-16LE string."""
    return (text + "\0").encode("utf-16-le")


class PatchError(Exception):
    """The exe isn't the expected build, or the options don't fit the profile."""


def lan_address(text, what):
    """The IPv4 address of a server on another machine: not loopback, wildcard, multicast or reserved. A public
    address is accepted too (the operator's own risk, docs/hosting.md)."""
    try:
        ip = ipaddress.IPv4Address(text)
    except ValueError:
        raise PatchError(f"{what} must be an IPv4 address, got {text!r}") from None
    if ip.is_loopback:
        raise PatchError(f"{what} {ip} is loopback; that is the local profile")
    if ip.is_unspecified or ip.is_multicast or ip.is_reserved:
        raise PatchError(f"{what} {ip} is not a host address")
    return str(ip)


def resolve_profile(profile, host=None, server_host=None, client_bind=None):
    """Return (redirector host, cave address or None) for a profile and its options."""
    if profile not in PROFILES:
        raise PatchError(f"unknown profile {profile!r} (local or lan)")
    if host and server_host and host != server_host:
        raise PatchError(f"two different server hosts: {host} and --server-host {server_host}")
    server = server_host or host
    if profile == "local":
        server = server or "127.0.0.1"
        try:
            ip = ipaddress.IPv4Address(server)
            loopback = ip.is_loopback
        except ValueError:
            ip, loopback = None, server.lower() == "localhost"
        if not loopback:
            raise PatchError(f"the local profile's sockets are loopback-only, so its server host must be "
                             f"loopback, got {server!r}; use --profile lan --server-host {server}")
        # the sockets bind the server's loopback address (A6: lane b's copy binds 127.0.0.2), or --client-bind
        bind = str(ip) if ip else "127.0.0.1"
        if client_bind:
            try:
                bind_ip = ipaddress.IPv4Address(client_bind)
            except ValueError:
                raise PatchError(f"--client-bind must be an IPv4 address, got {client_bind!r}") from None
            if not bind_ip.is_loopback:
                raise PatchError(f"the local profile binds a loopback address, got --client-bind {client_bind}; "
                                 "use --profile lan")
            bind = str(bind_ip)
        return server, bind
    if not server:
        raise PatchError("the lan profile needs --server-host <the server's IPv4 address>")
    server = lan_address(server, "--server-host")
    return server, (lan_address(client_bind, "--client-bind") if client_bind else None)


def check_data_suffix(suffix):
    """--data-suffix: one ASCII letter or digit (the Documents name has room for one more character)."""
    if suffix is not None and not (len(suffix) == 1 and suffix.isascii() and suffix.isalnum()):
        raise PatchError(f"--data-suffix must be one letter or digit, got {suffix!r}")
    return suffix


def patch_bytes(orig, modulus, profile="local", host=None, server_host=None, client_bind=None, data_suffix=None,
                laa=True):
    """Patch a copy of the untouched exe's bytes; returns (patched bytes, summary text)."""
    server, cave_address = resolve_profile(profile, host, server_host, client_bind)
    check_data_suffix(data_suffix)
    data = bytearray(orig)
    pe = pefile.PE(data=bytes(data), fast_load=True)
    off = lambda va: pe.get_offset_from_rva(va - IMAGE_BASE)  # noqa: E731

    def expect(va, want, what):
        got = bytes(data[off(va):off(va) + len(want)])
        if got != want:
            raise PatchError(f"unexpected bytes for {what} at {va:#010x}: {got.hex()} (wrong build?)")

    # --- CA modulus
    if len(modulus) != 128:
        raise PatchError(f"expected a 128-byte (1024-bit) CA modulus, got {len(modulus)}")
    expect(OTG3_ENTRY + 0x180, b"OTG3 Certificate Authority\0", "OTG3 common name")
    expect(OTG3_KEYINFO, (0x80).to_bytes(4, "little") + OTG3_MODULUS.to_bytes(4, "little") +
           (1).to_bytes(4, "little") + b"\x03", "OTG3 key info (size, pointer, exponent 3)")
    expect(OTG3_MODULUS, OTG3_MODULUS_HEAD, "OTG3 modulus")
    data[off(OTG3_MODULUS):off(OTG3_MODULUS) + 128] = modulus
    expect(OTG3_FLAGS, (1).to_bytes(4, "little"), "OTG3 flags")
    data[off(OTG3_FLAGS):off(OTG3_FLAGS) + 4] = (0).to_bytes(4, "little")

    # --- redirector host
    name = server.encode("ascii")
    if len(name) + 1 > len(REDIRECTOR_ORIG):
        raise PatchError(f"host must be at most {len(REDIRECTOR_ORIG) - 1} characters")
    expect(REDIRECTOR_HOST, REDIRECTOR_ORIG, "redirector host")
    data[off(REDIRECTOR_HOST):off(REDIRECTOR_HOST) + len(REDIRECTOR_ORIG)] = name.ljust(len(REDIRECTOR_ORIG), b"\0")

    # --- no UPnP: force NetConnStartup's "-noupnp" path, and refuse ProtoUPnP discovery
    expect(NOUPNP_STRSTR, NOUPNP_STRSTR_ORIG, "NetConnStartup strstr(params, \"-noupnp\")")
    expect(NOUPNP_JNZ, NOUPNP_JNZ_ORIG, "NetConnStartup jnz around ProtoUpnpCreate")
    data[off(NOUPNP_JNZ):off(NOUPNP_JNZ) + 1] = NOUPNP_JMP
    expect(UPNP_DISC, UPNP_DISC_ORIG, "ProtoUpnpControl 'disc'")
    data[off(UPNP_DISC):off(UPNP_DISC) + len(UPNP_DISC_NEW)] = UPNP_DISC_NEW

    # --- narrowed sockets: bind() goes through a cave that narrows 0.0.0.0 to one address
    text = pe.sections[0]
    if text.Name.rstrip(b"\0") != b".text" or text.Misc_VirtualSize != TEXT_VSIZE:
        raise PatchError(f"unexpected first section {text.Name!r} / VirtualSize {text.Misc_VirtualSize:#x} (wrong build?)")
    for site, orig_op in [(va, CALL_BIND) for va in BIND_CALLS] + [(BIND_THUNK, JMP_BIND)]:
        expect(site, orig_op, "call/jmp [bind]")  # checked in every profile: the right build
    if cave_address is None:
        sockets = "sockets as shipped (wildcard binds; loopback patch left out)"
    else:
        cave = bind_cave_code(cave_address)
        text_end = IMAGE_BASE + text.VirtualAddress + text.SizeOfRawData
        if not (IMAGE_BASE + text.VirtualAddress + TEXT_VSIZE <= LOOPBACK_CAVE
                and LOOPBACK_CAVE + len(cave) <= text_end):
            raise PatchError("the bind cave doesn't fit into .text's slack (wrong build?)")
        expect(LOOPBACK_CAVE, bytes(len(cave)), "unused slack after .text")
        if pe.OPTIONAL_HEADER.DllCharacteristics & 0x40 or not pe.FILE_HEADER.Characteristics & 0x1:
            raise PatchError("exe is relocatable (ASLR or relocations); absolute addresses in the cave would break")
        vsize_at = text.get_file_offset() + 8   # IMAGE_SECTION_HEADER.Misc.VirtualSize
        data[vsize_at:vsize_at + 4] = text.SizeOfRawData.to_bytes(4, "little")
        data[off(LOOPBACK_CAVE):off(LOOPBACK_CAVE) + len(cave)] = cave
        for site, op in [(va, 0xE8) for va in BIND_CALLS] + [(BIND_THUNK, 0xE9)]:
            # nop first, so a call's return address stays site + 6 as in the original
            rel = (LOOPBACK_CAVE - (site + 6)) & 0xFFFFFFFF
            data[off(site):off(site) + 6] = bytes([0x90, op]) + rel.to_bytes(4, "little")
        what = "loopback-only" if cave_address == "127.0.0.1" else f"narrowed to {cave_address}"
        sockets = f"sockets {what} (bind cave at {LOOPBACK_CAVE:#010x}, {len(BIND_CALLS) + 1} sites)"
    # --- A6: RakNet's loopback literal follows a loopback bind address other than 127.0.0.1 (lane b)
    expect(RAKNET_LOOPBACK, RAKNET_LOOPBACK_ORIG, "RakNet IPV4_LOOPBACK / IPV6_LOOPBACK")
    if cave_address and cave_address != "127.0.0.1" and ipaddress.IPv4Address(cave_address).is_loopback:
        lit = cave_address.encode("ascii") + b"\0"
        if len(lit) > RAKNET_LOOPBACK_ROOM:
            raise PatchError(f"{cave_address} doesn't fit RakNet's loopback literal")
        data[off(RAKNET_LOOPBACK):off(RAKNET_LOOPBACK) + RAKNET_LOOPBACK_ROOM] = lit.ljust(RAKNET_LOOPBACK_ROOM, b"\0")
        sockets += f", RakNet loopback {cave_address}"
    # --- A5b: a LAN bind address: RakNet's IPV4_LOOPBACK pointer to a copy of it (its start-up send test, see above)
    expect(RAKNET_LOOPBACK_PTR, RAKNET_LOOPBACK.to_bytes(4, "little"), "RakNet IPV4_LOOPBACK pointer")
    if cave_address and not ipaddress.IPv4Address(cave_address).is_loopback:
        lit = cave_address.encode("ascii") + b"\0"
        if len(lit) > LAN_LOOPBACK_ROOM or not (LOOPBACK_CAVE + len(LOOPBACK_CAVE_CODE) <= LAN_LOOPBACK_STRING):
            raise PatchError(f"{cave_address} doesn't fit the slack after the bind cave")
        expect(LAN_LOOPBACK_STRING, bytes(LAN_LOOPBACK_ROOM), "unused slack after the bind cave")
        data[off(LAN_LOOPBACK_STRING):off(LAN_LOOPBACK_STRING) + LAN_LOOPBACK_ROOM] = lit.ljust(LAN_LOOPBACK_ROOM, b"\0")
        data[off(RAKNET_LOOPBACK_PTR):off(RAKNET_LOOPBACK_PTR) + 4] = LAN_LOOPBACK_STRING.to_bytes(4, "little")
        sockets += f", RakNet's own-socket test to {cave_address}"
    summary = (f"redirector host -> {server}; UPnP off (-noupnp forced, discovery refused); {sockets}")

    # --- A6: per-user folders with a suffix (a second client copy beside the first)
    for va, room, name in DATA_FOLDERS:
        old = wide(name)
        expect(va, old + bytes(room - len(old)), f"data folder name {name!r} and its padding")
    if data_suffix:
        names = []
        for va, room, name in DATA_FOLDERS:
            new = wide(name + data_suffix)
            if len(new) > room:
                raise PatchError(f"{name + data_suffix!r} doesn't fit into {room} bytes")
            data[off(va):off(va) + room] = new.ljust(room, b"\0")
            names.append(name + data_suffix)
        summary += f"; data folders %APPDATA%\\{names[0]}, Documents\\{names[1]}"

    # --- Q3b: LARGE_ADDRESS_AWARE (4 GB of address space on 64-bit Windows)
    char_at = pe.FILE_HEADER.get_file_offset() + 0x12   # IMAGE_FILE_HEADER.Characteristics
    if int.from_bytes(data[char_at:char_at + 2], "little") != CHAR_BUILD:
        raise PatchError(f"unexpected PE characteristics {int.from_bytes(data[char_at:char_at + 2], 'little'):#06x} "
                         "(wrong build?)")
    if laa:
        data[char_at:char_at + 2] = (CHAR_BUILD | CHAR_LAA).to_bytes(2, "little")
        summary += "; LARGE_ADDRESS_AWARE (4 GB of address space)"
    return bytes(data), summary


def describe(data):
    """What an exe (patched or not) carries, as an ordered dict of strings (--show)."""
    pe = pefile.PE(data=bytes(data), fast_load=True)
    off = lambda va: pe.get_offset_from_rva(va - IMAGE_BASE)  # noqa: E731
    at = lambda va, n: bytes(data[off(va):off(va) + n])  # noqa: E731
    out = {}
    host = at(REDIRECTOR_HOST, len(REDIRECTOR_ORIG))
    out["redirector"] = host.split(b"\0", 1)[0].decode("ascii", "replace")
    cave = at(LOOPBACK_CAVE, len(LOOPBACK_CAVE_CODE))
    if at(BIND_THUNK, 6) == JMP_BIND:
        out["bind"] = "as-shipped"
    elif cave[:-10] == LOOPBACK_CAVE_CODE[:-10] and cave[-6:] == JMP_BIND:
        out["bind"] = str(ipaddress.IPv4Address(cave[-10:-6]))
    else:
        out["bind"] = "unknown"
    for key, (va, room, _name) in zip(("appdata", "documents"), DATA_FOLDERS):
        raw = at(va, room)
        end = next((i for i in range(0, room - 1, 2) if raw[i:i + 2] == b"\0\0"), room)
        out[key] = raw[:end].decode("utf-16-le", "replace")
    # where RakNet's IPV4_LOOPBACK points: the literal (A6 rewrites it for a loopback lane) or, since A5b, the lan
    # profile's copy of its bind address in the cave's slack
    ptr = int.from_bytes(at(RAKNET_LOOPBACK_PTR, 4), "little")
    target, room = ((LAN_LOOPBACK_STRING, LAN_LOOPBACK_ROOM) if ptr == LAN_LOOPBACK_STRING
                    else (RAKNET_LOOPBACK, RAKNET_LOOPBACK_ROOM))
    out["raknet_loopback"] = at(target, room).split(b"\0", 1)[0].decode("ascii", "replace")
    if ptr not in (RAKNET_LOOPBACK, LAN_LOOPBACK_STRING):
        out["raknet_loopback"] = f"unknown pointer {ptr:#010x}"
    out["ca_sha256"] = hashlib.sha256(at(OTG3_MODULUS, 128)).hexdigest()[:16]
    out["ca_flag"] = str(int.from_bytes(at(OTG3_FLAGS, 4), "little"))
    out["laa"] = "on" if pe.FILE_HEADER.Characteristics & CHAR_LAA else "off"
    return out


def main(argv):
    if len(argv) == 3 and argv[1] == "--show":
        # read only: what the exe carries (A6: run_session.ps1 -Lane checks a lane's copy with it)
        with open(argv[2], "rb") as f:
            for key, value in describe(f.read()).items():
                print(f"{key}={value}")
        return
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("exe", help="the working copy's Dawngate.exe (never client/ia_*)")
    ap.add_argument("modulus", help="ca_modulus.bin from server/tools/make_certs.py")
    ap.add_argument("host", nargs="?", help="redirector host (the A4 form; same as --server-host)")
    ap.add_argument("--profile", choices=PROFILES, default="local")
    ap.add_argument("--server-host", help="the server's IPv4 address (lan: required)")
    ap.add_argument("--client-bind", help="narrow the client's wildcard binds to this address of this machine "
                                          "(lan: a LAN address; local: a loopback address, default the server host)")
    ap.add_argument("--data-suffix", help="A6: one letter or digit appended to the per-user folders "
                                          "(%%APPDATA%%\\DawngateData<X>, Documents\\Dawngate<X>)")
    ap.add_argument("--laa", choices=("on", "off"), default="on",
                    help="Q3b: LARGE_ADDRESS_AWARE (4 GB of address space instead of 2 GB); on by default")
    args = ap.parse_args(argv[1:])
    exe = os.path.abspath(args.exe)
    if os.sep + "ia_" in exe or "/ia_" in exe:
        sys.exit("refusing to patch a pristine extraction (client/ia_*); use a working copy")
    try:
        resolve_profile(args.profile, args.host, args.server_host, args.client_bind)  # before any file is written
        check_data_suffix(args.data_suffix)
    except PatchError as e:
        sys.exit(str(e))
    with open(args.modulus, "rb") as f:
        modulus = f.read()
    if len(modulus) != 128:
        sys.exit(f"{args.modulus}: expected a 128-byte (1024-bit) modulus, got {len(modulus)}")
    orig = exe + ".orig"
    if not os.path.exists(orig):
        shutil.copy2(exe, orig)
        print(f"saved original as {orig}")
    with open(orig, "rb") as f:
        untouched = f.read()
    try:
        data, summary = patch_bytes(untouched, modulus, args.profile, args.host, args.server_host, args.client_bind,
                                    args.data_suffix, laa=args.laa == "on")
    except PatchError as e:
        sys.exit(str(e))
    with open(exe, "wb") as f:
        f.write(data)
    profile = "" if args.profile == "local" else f" (profile {args.profile})"
    print(f"patched {exe}{profile}: OTG3 CA modulus -> {args.modulus}, EA-name restriction cleared; {summary}")


if __name__ == "__main__":
    main(sys.argv)
