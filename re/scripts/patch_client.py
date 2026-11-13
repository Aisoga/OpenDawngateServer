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
  preload  Since SNDBANK, in every profile (--preload off leaves it out): GameClient_PreloadPlayerCharacters (0x0066BD10,
           called at pre-game 03 and at 0x0066CCB9) preloads each player's shaper skin, and with it the shaper's sound
           banks, for the player indexes i < GameSimulator_CountPlayers() (0x008F1110: the number of players and
           spectators, not the highest index). Our match host seats team 2 at indexes 5-9, so with fewer than 6
           participants the red shapers were never preloaded and their ability sounds and voice failed on every other
           client ("Unable to play audio event", GameGraphics.cpp 10310/1571; seen on a public server). The call at
           0x0066BD1F (E8 EC 53 28 00) becomes mov eax,15 (B8 0F 00 00 00; the caller's add esp,0x10 still pops the
           four arguments), so every index 0-14 is looked up: GameSimulator_GetPlayerByIndex (0x008F0D90, a hash map
           lookup, the spectator map for 10-14) returns 0 for an empty index and LabsPlayer_GetNounDef (0x00912160,
           +0x04) 0 for a spectator, and the loop skips both, as the shipped loop already did for spectators at 10+.
  maptable  Since MAPTABLE, in every profile (--maptable off leaves it out): GameEntryManager's map-name method
           (0x00684380; the custom lobby's map line and the game browser's Map column) names a game's map only from a
           one-entry table (0x013F3A78: {L"^labs_global!0x30000001", SM_Map_v5's LevelId 0x946D41FE}, kNumPlayableMaps
           1) and returns null for any other LevelId (the lobby then asserts resultName). Its not-found exit (0x006843D2)
           becomes a jmp to a 45-byte cave in .text's slack (MAP_CAVE, after the bind cave and the LAN string), which
           writes the LevelId as 8 hex digits into the key L"^FE_CustomGameLobby!0x????????" (MAP_KEY, in .tls's slack
           after the TLS template; .tls's VirtualSize raised to its SizeOfRawData so it is mapped, as .text's) and goes
           on as the found path does with that key (0x006843EF: the key check, the locale lookup through the resource
           key parser 0x007D9500, SPStringManager's line lookup 0x00A94540). So every map is named by the line of its
           LevelId in the lobby's own table, which the setup writes into AA_Server.package (patch_locale.py) for every
           installed map; a LevelId without a line gives an empty name (SPStringManager's empty string), no assert.
           SM_Map_v5 keeps its own entry.
  heaplocks  Since UICHAN, in every profile (--heap-locks off leaves it out): Scaleform's memory heaps take their lock only
           when created without Heap_ThreadUnsafe, and the movies' heaps are created with it (UseLocks, heap +0x64: the
           constructors at 0x00D294DF and 0x00D727B3 compute it as !(flags & 1)). flash.net.Socket's thread allocates
           from the movie's heap (0x00EB1DE0 -> AllocAutoHeap 0x00CF8520), so with a connection open the main thread and
           the socket's race on the heap: crashes at random places within a second (docs/development/ui-channel.md).
           Both constructors' not dl; and dl,1 (F6 D2 80 E2 01) become mov dl,1 (B2 01 90 90 90): every heap
           locks. An uncontended lock per allocation of the UI.
  numfmt   Since NUMFMT, in every profile (--number-format off leaves it out): Scaleform's AS3 number-to-string
           (0x00EA69F0, called from the AS3 value conversions) prints a double that isn't a whole number with
           "%.16g" (0x01329B88, which only it reads), and 16 digits show the double nearest 8.2 as 8.199999999999999: the
           profile's averages overran their panel. Its clean-up only
           cuts a "000...1" tail, so 8.3 was fine and 8.2 wasn't. The format becomes "%.15g", which prints every
           one-decimal value as it was meant. From 1e16 up the function raises the digits so that whole numbers print
           exactly; that step (fld [1e16] at 0x00EA6B3C) now reads a 1e15 already in .rdata (0x0135D140), so 16-digit whole
           numbers stay exact instead of turning into 1.23456789012346e+15.
  averages Since AVGPREC, in every profile (--averages off leaves it out): StatsManager_OnShaperHistoryStatsResult
           (0x00AA5E90) made the profile's averages as total * (1 / games) on an x87 at 24 bits, so an average that is an
           exact tenth could land just under it, and the UI's formatNumber cuts: 87 over 10 games showed 8.6, 41 over 5
           showed 8.1. The 97 bytes at 0x00AA63C8 are rewritten in place (.text's slack is nearly used up): with the games
           in st(0), the x87 goes to 53 bits, each of the five doubles in a row is divided by the games, and the control
           word goes back. Real divisions because the reciprocal stays wrong at any precision (7 over 35). The exe has no
           SSE2 doubles, so this stays on the x87. The seconds' integer division and the list's head as shipped.
  profilereload Since PROFILE2, in every profile (--profile-reload off leaves it out): the Profile screen loads a
           player's numbers only when the player it is asked to show differs from the one it shows
           (ProfileUI_SetShownPlayer 0x004EE200: the jnz at 0x004EE25B skips the load for the same id), so its own
           search native for the shown name reloads nothing. The jnz becomes a jmp (75 08 -> EB 08): the same player
           loads again, which the Profile's mode tabs (client_ui/profile/ProfileModes.as) use after the server changed
           the mode its numbers are answered in; a second click on Profile reloads it too.
  rating   Since RATINGSHOW1, in every profile (--rating off leaves it out): the main menu's profile box has EA's field
           mcProfile.m_glickoText (MainMenuUI.gfx; shown when SetExtendedData's second argument isn't null), and both
           C++ callers (MainMenuUI_OnUserExtendedDataChanged 0x004DE0D0, MainMenuUI_BindExtFunctions 0x004DFFC0) pass
           an empty wide string (0x0199D2A0) as that text. Each now calls a cave in .text's slack after the map-name
           cave (RATING_CAVE) for the text instead: the local user's extended-data value DMAP[0x00070037] (Blaze user
           of the local Blaze id, BlazeUser_GetExtendedDataValue 0x00C8B560, as BlazePlatform_GetUserExtendedInfo
           0x00AD4F90 reads 0x34-0x36), and when it is above 0 the text L"Rating <value>" (built in .tls's slack after
           the map key), else the empty string as shipped. The server sends 0x00070037 only when players are to see
           their rating (dgserver --show-rating), so the switch stays the server's. Sites: 0x004DE1C9 (16 bytes: the
           value's type 7 and pointer stores) and 0x004E0661 (mov ebp,7, whose ebp/ebx the caller stores as type and
           pointer); the cave keeps every register but ebx (the text) and ebp (7 at the second site).
  fade     Since FADE1, in every profile (--fade off leaves it out): the shipped client draws every enemy creature it has,
           by the fog texture's value at its feet (GameGraphics_UpdateCreatureFrame 0x00570140): a pale ghost under 0.5,
           solid above, at once. So a shaper the server handed over early (the host's head start, FOGEDGE, removed by
           FOGEDGE-RM) showed early. Now an enemy creature (noun creature 0x06C27D00 with fowControlsObjectVisibility, another
           team than GameClient_GetLocalTeamMask's, not for an observer, and since COLL3D only while there is a local
           player: the front end's shaper models are such creatures too, and with no player the target was 0) is
           drawn while mVisibleToTeam & the local mask
           (+0x888) is set or (FADE1b) its own fog query (+0x860 -> +0x5C, the fog texture at its feet, 0-255, without
           UpdateCreatureFrame's nearness boost) is at least 160, clearly lit (FADE1c: the texture fades out over about a
           second where the viewer was, so at 50, the radius's rim, an enemy stayed drawn out to 45 units behind a viewer
           walking away; 160 is about 32 units and drops before the bit); hidden otherwise, and its own opacity
           (+0x868) follows that target by the frame's seconds over 0.25 s. The target's test, FadeTarget, sits in
           UpdateCreatureOpacity's first assert block (0x0056E44D, 62 bytes, which only logged; its one store kept).
           FadeStep runs from GameGraphics_UpdateVisibleObject's fogged test (0x00576256, 18 bytes: call, test,
           jnz to its not-drawn exit), so at opacity 0 the creature is skipped as a fogged one is: no model, health bar or
           name. It stamps the frame (+0x86C; not stepped the frame before means off screen or new: straight to the
           target) and marks the object (+0x870 = "FADE"). Room, in place (.text's slack is used up):
           GameGraphics_ApplyCreatureOpacity (0x0056E750, only called by the fog exit's reader with 0) becomes the
           exit's reset (opacity 0, stamped now, so an exit fades in) followed by FadeStep, and
           GameGraphics_UpdateCreatureOpacity's front (0x0056E4DB, 141 bytes) draws a marked creature at opacity 1 with
           the normal materials, else with labsHeroStealth at opacity squared (0 is invisible, 1 nearly solid; grass
           and stealth at most the shipped 0.375), and any other creature as shipped; the glass parameter's load
           (0x0056E706) takes the stored value. +0x864..+0x870 are written only by GameObjectManager_CreateObject and
           these. Sounds are not muted in the window (only the fog step's Set_FOW_Invisible does).
  dgx      Since EXESECT, in every profile (--dgx off leaves it out): room for code and data that no longer fit .text's
           slack (15 bytes left after the caves above). A sixth section .dgx (code, execute, read; not writable) is
           appended: its header in the free slot after .rsrc's (file offset 0x2E0; the headers end at 0x400), VA
           0x01C84000 (the untouched SizeOfImage), 0x1000 bytes virtual and raw, its raw data after the file's end
           aligned to 0x200 (0x0110A800; EA's certificate before it stays as dead bytes), SizeOfImage 0x01884000 ->
           0x01885000, SizeOfCode raised by 0x1000, the security directory zeroed (the certificate has been invalid since
           the first patch and no longer ends the file), the PE checksum left stale as by every other patch. Its first
           8 bytes are DGX_MAGIC; every later piece has a fixed place in DGX_LAYOUT (append-only, so placed pieces never
           move) and is written with dgx_place(). Writable state does not go here: .tls's slack (0x01C000C0-0x01C00200).
  draggrab Since DRAGGRAB1, with .dgx (--drag-grab off leaves it out; --dgx off too): a grab-style middle-mouse drag
           scroll, switched in the game by the preference OptionDragGrab (FNV-1 0x14958012; missing = off). The 12
           bytes at 0x00697EB8 (the joystick's active check) jump to .dgx's draggrab piece (0x01C84010), which reads the
           preference at each drag's first frame (AppPrefs_GetBool) into .tls's slack: off runs the two replaced
           instructions and the shipped joystick; on stores G0 = the cursor's ground point, then each frame casts the
           cursor ray onto the plane z = G0.z and moves the camera's current and target look-at points
           (0x013F5724/0x013F5730) by G0 - G, so the grabbed point stays under the cursor. The two observer release
           sites (0x00697D3C, 0x006981A0) call a helper that skips the cursor's jump back after a grab. State at
           0x01C000C0-0x01C000EF (.tls). The assembly is drag_grab_code()'s docstring.
           Since DRAGGRAB2b the same switch also gives the Options menu's GAME tab the key: the 12 gameplay booleans'
           table (0x011D1988, {FNV-1 of the lower-case name, char *name}) is copied from the exe into .dgx's options
           piece (0x01C84400) with a 13th entry {0x14958012, "OptionDragGrab"} (the name at +0x70), and
           OptionsUI_GetGameSettings / OptionsUI_SaveSettings read that copy (four disp32 operands, two loop bounds
           0x60 -> 0x68). The option itself (its two settings, the bool app property the cave reads, its default and the
           name Preferences.prop is written with) is config data: patch_config.py's drag-grab lines. Ship the exe with the
           Options movie (zz_Options.snap): an exe with the entry and without the movie's box makes the movie call an
           "is undefined" ExternalInterface name at every opening.

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
import struct
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
# SNDBANK: GameClient_PreloadPlayerCharacters (0x0066BD10): push edi (sim); call GameSimulator_CountPlayers (0x008F1110),
# its loop bound; becomes mov eax,15 (the caller's add esp,0x10 still pops the four pushed arguments)
PRELOAD_COUNT = 0x0066BD1F
PRELOAD_COUNT_ORIG = bytes.fromhex("e8ec532800")
PRELOAD_COUNT_NEW = bytes.fromhex("b80f000000")
# MAPTABLE: GameEntryManager's map-name method (0x00684380) searches its one-entry table (0x013F3A78: {display key,
# LevelId}, kNumPlayableMaps 1) for the game's LevelId; the loop and its not-found exit (return null)
MAP_LOOP = 0x006843C1
MAP_LOOP_ORIG = bytes.fromhex("33c9" "3904cd7c3a3f01" "741b" "41" "83f901" "72f1")  # xor ecx,ecx; cmp; jz; inc; cmp 1; jc
MAP_MISS = 0x006843D2                     # not found: pop edi; mov eax,ebx (0); pop ebx; mov ecx,[esp+0x20] ...
MAP_MISS_ORIG = bytes.fromhex("5f" "8bc3" "5b" "8b4c2420")
MAP_FOUND = 0x006843E7                    # found: push esi; mov esi,[ecx*8+0x013F3A78] (the display key)
MAP_FOUND_ORIG = bytes.fromhex("56" "8b34cd783a3f01")
MAP_RESUME = 0x006843EF                   # cmp esi,edi; jz ...: the key's check, its locale lookup, the return
MAP_RESUME_ORIG = bytes.fromhex("3bf7" "740d")
MAP_CAVE = 0x011AB140                     # in .text's slack after the bind cave (0x011AB100) and the LAN string
TLS_VA, TLS_VSIZE, TLS_RAW = 0x01C00000, 0x39, 0x200   # .tls: the TLS template is 0x01C00000-0x01C00038
MAP_KEY = 0x01C00040                      # the key the cave writes, in .tls's slack (writable)
MAP_KEY_PREFIX = "^FE_CustomGameLobby!0x"  # the lobby's own table; the line id is the LevelId
MAP_KEY_TEXT = MAP_KEY_PREFIX + "00000000"
MAP_KEY_ROOM = 0x40


def map_cave_code():
    """The map-name cave, reached from the not-found exit with eax = the game's LevelId: write the id as 8 hex digits
    into the key L"^FE_CustomGameLobby!0x????????" (MAP_KEY) and go on as the found path does with that key."""
    hex_at = MAP_KEY + 2 * len(MAP_KEY_PREFIX)
    code = bytes.fromhex(
        "b908000000"        # mov ecx,8
        "8bd0"              # l: mov edx,eax
        "83e20f"            # and edx,0xf
        "80c230"            # add dl,'0'
        "80fa39"            # cmp dl,'9'
        "7603"              # jbe s
        "80c207"            # add dl,7            ; 'A'..'F'
        "6689144d"          # s: mov [ecx*2+hex-2],dx
    ) + (hex_at - 2).to_bytes(4, "little") + bytes.fromhex(
        "c1e804"            # shr eax,4
        "e2e3"              # loop l
        "56"                # push esi            ; as the found path does
        "be"                # mov esi,MAP_KEY
    ) + MAP_KEY.to_bytes(4, "little") + b"\xe9"
    rel = (MAP_RESUME - (MAP_CAVE + len(code) + 4)) & 0xFFFFFFFF
    return code + rel.to_bytes(4, "little")  # jmp MAP_RESUME


MAP_CAVE_CODE = map_cave_code()
MAP_MISS_NEW = b"\xe9" + ((MAP_CAVE - (MAP_MISS + 5)) & 0xFFFFFFFF).to_bytes(4, "little")  # jmp MAP_CAVE

# RATINGSHOW1: the profile box's rating text. Both SetExtendedData callers build GFx values {0, type, value} of 0x18
# bytes: wins (type 3, int) and the rating (type 7, a wide string: the empty string 0x0199D2A0).
RATING_SITE1 = 0x004DE1C9                 # MainMenuUI_OnUserExtendedDataChanged: mov [esp+0x58],7; mov [esp+0x5c],empty
RATING_SITE1_ORIG = bytes.fromhex("c744245807000000" "c744245ca0d29901")
RATING_SITE2 = 0x004E0661                 # MainMenuUI_BindExtFunctions: mov ebp,7 (stored at 0x004E067A, ebx at 0x004E067E)
RATING_SITE2_ORIG = bytes.fromhex("bd07000000")
RATING_NEXT2 = bytes.fromhex("8954244c" "c744245003000000" "89442454" "89542464" "896c2468" "895c246c")  # unchanged
EMPTY_WSTRING = 0x0199D2A0                # the shared empty wide string both callers pass
PLATFORM_PTR = 0x01BB6154                 # SporeOnline_GetPlatform (0x00AD3800) returns [0x01BB6154]
FIND_USER = 0x00C8AFD0                    # UserManager_FindUserByBlazeId (ecx = user manager; id lo, hi), ret 8
GET_EXT_VALUE = 0x00C8B560                # BlazeUser_GetExtendedDataValue (ecx = user; component, key, &int64), ret 0xC
RATING_KEY = 0x37                         # DMAP[0x00070037] (component 7 << 16 | 0x37): the rating to show
# UICHAN: the two heap constructors' UseLocks (heap +0x64), which the movies' sockets need
HEAP_LOCKS = (0x00D294E1, 0x00D727B5)
HEAP_LOCKS_ORIG = bytes.fromhex("f6d280e201")  # not dl; and dl,1: !(flags & Heap_ThreadUnsafe)
HEAP_LOCKS_NEW = bytes.fromhex("b201909090")   # mov dl,1; nop; nop; nop
# NUMFMT: Scaleform's number-to-string (0x00EA69F0): its format and the value from which it raises the digits
NUMFMT_FORMAT = 0x01329B88
NUMFMT_FORMAT_ORIG = b"%.16g\0"
NUMFMT_FORMAT_NEW = b"%.15g\0"
NUMFMT_STEP = 0x00EA6B3C
NUMFMT_STEP_ORIG = bytes.fromhex("dd05d87b3001")  # fld qword ptr [0x01307BD8]: 1e16
NUMFMT_STEP_NEW = bytes.fromhex("dd0540d13501")   # fld qword ptr [0x0135D140]: 1e15
NUMFMT_1E15 = 0x0135D140                          # a 1e15 double in .rdata
NUMFMT_1E15_VALUE = bytes.fromhex("00003426f56b0c43")
# PROFILE2: ProfileUI_SetShownPlayer (0x004EE200) reloads the Profile only for another player; the jump past the same
# player's id becomes unconditional, so the same player loads again (the Profile's mode tabs, a second Profile click)
PROFILE_RELOAD_SITE = 0x004EE25B
PROFILE_RELOAD_ORIG = bytes.fromhex("7508")  # jnz 0x004EE265: the low dword differs, so set it
PROFILE_RELOAD_NEW = bytes.fromhex("eb08")   # jmp 0x004EE265: always
# AVGPREC: the profile's averages, in StatsManager_OnShaperHistoryStatsResult with the games in st(0)
AVERAGES_BLOCK = 0x00AA63C8
# as shipped: 1 / games, the seconds' integer division and the list's head, then each total times the reciprocal
AVERAGES_ORIG = bytes.fromhex(
    "d9e8" "8b842490000000" "def1" "33d2" "f7f1" "8b5c2414" "83c378" "dd442468" "d8c9" "89842490000000" "8b03" "dd5c2468"
    "8bf0" "dd442470" "89742410" "d8c9" "dd5c2470" "dd442478" "d8c9" "dd5c2478" "dd842480000000" "d8c9" "dd9c2480000000"
    "dc8c2488000000" "dd9c2488000000")
AVERAGES_NEW = bytes.fromhex(
    "50"              # push eax: a slot for the x87's control word
    "d93c24"          # fnstcw word ptr [esp]: the game's
    "687f020000"      # push 0x27f: 53-bit precision, round to nearest, exceptions masked
    "d92c24"          # fldcw word ptr [esp]
    "58"              # pop eax
    "8d54246c"        # lea edx,[esp+0x6c]: the first total ([esp+0x68] before the push)
    "6a05"            # push 5: kills, deaths, assists, CS, vim, five doubles in a row
    "58"              # pop eax
    "dd02"            # loop: fld qword ptr [edx]; st(1) is the games
    "d8f1"            # fdiv st(0),st(1)
    "dd1a"            # fstp qword ptr [edx]
    "83c208"          # add edx,8
    "48"              # dec eax
    "75f4"            # jne loop
    "ddd8"            # fstp st(0): the games
    "d92c24"          # fldcw word ptr [esp]: the game's again
    "58"              # pop eax
    "8b842490000000"  # from here as shipped: mov eax,[esp+0x90], the seconds
    "33d2"            # xor edx,edx
    "f7f1"            # div ecx
    "89842490000000"  # mov [esp+0x90],eax
    "8b5c2414"        # mov ebx,[esp+0x14]
    "83c378"          # add ebx,0x78
    "8b03"            # mov eax,[ebx]
    "8bf0"            # mov esi,eax
    "89742410"        # mov [esp+0x10],esi
) + b"\x90" * 26
# FADE1: the fade at the fog's edge. An enemy creature is drawn while its team sees it (mVisibleToTeam & the local mask)
# or (FADE1b) the client's own fog texture at its feet is lit, fading in and out over 0.25 s. Observers as shipped.
FADE_APPLY = 0x0056E750       # GameGraphics_ApplyCreatureOpacity: called only by the fog exit (95's reader, 0x006BBAF0)
FADE_APPLY_ORIG = bytes.fromhex(  # its 215 bytes and the 9 bytes of int3 padding before 0x0056E830
    "53568b74240c33c039869008000068391d00000f95c06860f41d0150e84f83320083c40c85c0741c6a005068391d0000"
    "6860f41d016884061e016a00e84f7e320083c4188b9e9008000085db0f8482000000578b7b7c2b7b78c1ff024f787490"
    "8b4b788b14b98d04b96a0052e85f2bffff8bf033c085f60f95c068441d00006860f41d0150e8e682320083c41485c074"
    "1c6a005068441d00006860f41d01687c061e016a00e8e67d320083c41885f67406837e040174074f79a65f5e5bc3d944"
    "2414518b4e08d91c245651e8e0fbffff83c40c5f5e5bc3cccccccccccccccccc"
)
# the fog exit: the fade starts hidden and stamped now, so the next frame fades in; then FadeStep (0x0056E76A), called
# by GameGraphics_UpdateVisibleObject for each object on screen: al = 1 when the object is not drawn
FADE_APPLY_NEW = bytes.fromhex(
    "8b442404"                  # 0056e750 mov eax, dword ptr [esp + 4]: the object
    "83a06808000000"            # 0056e754 and dword ptr [eax + 0x868], 0: the fade's opacity: 0
    "8b0d38a05901"              # 0056e75b mov ecx, dword ptr [0x159a038]: GameGraphics
    "8b09"                      # 0056e761 mov ecx, dword ptr [ecx]: its frame number
    "89886c080000"              # 0056e763 mov dword ptr [eax + 0x86c], ecx: the stamp: stepped this frame
    "c3"                        # 0056e769 ret
    "f6c101"                    # 0056e76a test cl, 1: FadeStep (edi object, eax noun, cl flags, ebx the visitor): fogged?
    "7411"                      # 0056e76d je 0x56e780
    "80b8cd02000000"            # 0056e76f cmp byte ptr [eax + 0x2cd], 0: fowControlsObjectVisibility
    "7408"                      # 0056e776 je 0x56e780
    "b001"                      # 0056e778 mov al, 1: fogged, as shipped: not drawn
    "c3"                        # 0056e77a ret
    "5e"                        # 0056e77b pop esi
    "59"                        # 0056e77c pop ecx
    "31c0"                      # 0056e77d xor eax, eax
    "c3"                        # 0056e77f ret
    "85c0"                      # 0056e780 test eax, eax: no noun
    "74f9"                      # 0056e782 je 0x56e77d
    "80b8cd02000000"            # 0056e784 cmp byte ptr [eax + 0x2cd], 0: fowControlsObjectVisibility
    "74f0"                      # 0056e78b je 0x56e77d
    "8138007dc206"              # 0056e78d cmp dword ptr [eax], 0x6c27d00: not a creature
    "75e8"                      # 0056e793 jne 0x56e77d
    "51"                        # 0056e795 push ecx: the flags, which the caller reads again
    "56"                        # 0056e796 push esi
    "e8d4fcffff"                # 0056e797 call 0x56e470: LocalViewAsShipped: no local player, or an observer
    "84c0"                      # 0056e79c test al, al: as shipped from here: grass or stealth 0.5
    "75db"                      # 0056e79e jne 0x56e77b
    "6a00"                      # 0056e7a0 push 0
    "e879e60f00"                # 0056e7a2 call 0x66ce20: GameClient_GetLocalTeamMask(0)
    "59"                        # 0056e7a7 pop ecx
    "0fb6574d"                  # 0056e7a8 movzx edx, byte ptr [edi + 0x4d]
    "0fa3d0"                    # 0056e7ac bt eax, edx: the object's team (+0x4D) in the mask: drawn as shipped
    "72ca"                      # 0056e7af jb 0x56e77b
    "e89dfcffff"                # 0056e7b1 call 0x56e453: FadeTarget (0x0056E453): esi = the target
    "e8d5083800"                # 0056e7b6 call 0x8ef090: GameSimulator_GetCurrent
    "50"                        # 0056e7bb push eax
    "e8ffb13800"                # 0056e7bc call 0x8f99c0: GameTimeManager_GetDeltaSeconds: the frame's seconds
    "d91c24"                    # 0056e7c1 fstp dword ptr [esp]
    "f30f100424"                # 0056e7c4 movss xmm0, dword ptr [esp]
    "59"                        # 0056e7c9 pop ecx
    "f30f59056cf81b01"          # 0056e7ca mulss xmm0, dword ptr [0x11bf86c]: * 4.0: a whole fade in 0.25 s
    "8b4304"                    # 0056e7d2 mov eax, dword ptr [ebx + 4]: the visitor's frame number
    "87876c080000"              # 0056e7d5 xchg dword ptr [edi + 0x86c], eax: the stamp
    "2b4304"                    # 0056e7db sub eax, dword ptr [ebx + 4]
    "f7d8"                      # 0056e7de neg eax
    "83f801"                    # 0056e7e0 cmp eax, 1: stepped this frame or the last one?
    "7608"                      # 0056e7e3 jbe 0x56e7ed
    "f30f1005d4a31b01"          # 0056e7e5 movss xmm0, dword ptr [0x11ba3d4]: else (off screen, fogged, new) the target
    "85f6"                      # 0056e7ed test esi, esi
    "7508"                      # 0056e7ef jne 0x56e7f9
    "f30f590568f81b01"          # 0056e7f1 mulss xmm0, dword ptr [0x11bf868]: * -1.0: fading out
    "f30f588768080000"          # 0056e7f9 addss xmm0, dword ptr [edi + 0x868]: the fade's opacity
    "0f57c9"                    # 0056e801 xorps xmm1, xmm1
    "f30f5fc1"                  # 0056e804 maxss xmm0, xmm1
    "f30f5d05d4a31b01"          # 0056e808 minss xmm0, dword ptr [0x11ba3d4]: clamped to 0..1
    "f30f118768080000"          # 0056e810 movss dword ptr [edi + 0x868], xmm0
    "c7877008000046414445"      # 0056e818 mov dword ptr [edi + 0x870], 0x45444146: "FADE", drawn by +0x868
    "0f2fc1"                    # 0056e822 comiss xmm0, xmm1
    "5e"                        # 0056e825 pop esi
    "59"                        # 0056e826 pop ecx
    "0f96c0"                    # 0056e827 setbe al: 0: not drawn
    "c3"                        # 0056e82a ret
) + b"\xcc" * 5
FADE_TARGET = 0x0056E44D      # GameGraphics_UpdateCreatureOpacity's first assert (only logs): room for FadeTarget
FADE_TARGET_ORIG = bytes.fromhex(
    "33c085f60f95c068b01c00006860f41d018974241050e85886320083c40c85c0741c6a005068b01c00006860f41d0168"
    "04f51d016a00e85881320083c418"
)
FADE_TARGET_NEW = bytes.fromhex(
    "89742408"                  # 0056e44d mov dword ptr [esp + 8], esi: what the assert block stored for later
    "eb38"                      # 0056e451 jmp 0x56e48b: over FadeTarget to the second assert
    "228788080000"              # 0056e453 and al, byte ptr [edi + 0x888]: mVisibleToTeam & the mask
    "0fb6f0"                    # 0056e459 movzx esi, al: the target: 0 hidden, else shown
    "8b8f60080000"              # 0056e45c mov ecx, dword ptr [edi + 0x860]: the object's fog query
    "e30b"                      # 0056e462 jecxz 0x56e46f
    "81795ca0000000"            # 0056e464 cmp dword ptr [ecx + 0x5c], 0xa0: the texture (0-255) against 160
    "f5"                        # 0056e46b cmc
    "83d600"                    # 0056e46c adc esi, 0: lit: shown too
    "c3"                        # 0056e46f ret
    "6a00"                      # 0056e470 push 0: LocalViewAsShipped (COLL3D): al = 1 when FadeStep leaves the object
    "e8b9e80f00"                # 0056e472 call 0x66cd30: GameClient_GetLocalPlayer(0): the player or 0, no assert
    "59"                        # 0056e477 pop ecx
    "85c0"                      # 0056e478 test eax, eax: no local player (the front end): as shipped
    "7408"                      # 0056e47a je 0x56e484
    "50"                        # 0056e47c push eax
    "e8be3b3a00"                # 0056e47d call 0x912040: LabsPlayer_IsObserver: observers as shipped
    "59"                        # 0056e482 pop ecx
    "c3"                        # 0056e483 ret
    "b001"                      # 0056e484 mov al, 1
    "c3"                        # 0056e486 ret
) + b"\xcc" * 4
FADE_HOOK = 0x00576256        # GameGraphics_UpdateVisibleObject: the fogged test (flag 1 and fowControlsObjectVisibility)
FADE_HOOK_ORIG = bytes.fromhex("f6c101740d80b8cd020000000f85e6000000")
FADE_HOOK_NEW = bytes.fromhex(
    "e80f85ffff"                # 00576256 call 0x56e76a: FadeStep
    "84c0"                      # 0057625b test al, al: as shipped from here: grass or stealth 0.5
    "0f85eb000000"              # 0057625d jne 0x57634e: not drawn
    "90"                        # 00576263 nop
    "90"                        # 00576264 nop
    "90"                        # 00576265 nop
    "90"                        # 00576266 nop
    "90"                        # 00576267 nop
)
FADE_FRONT = 0x0056E4DB       # GameGraphics_UpdateCreatureOpacity after its asserts, up to the normal materials (0x0056E568)
FADE_FRONT_ORIG = bytes.fromhex(
    "e8b00b380050e8dab43800ddd88b86c408000083c40485c0741680b8741e000000750980b8751e0000007404b001eb02"
    "32c0d9866c080000f30f10442444f30f10155c5b3901d99e70080000d98664080000f30f11866c080000d99e68080000"
    "84c074030f28c2f30f100d64061e010f2fc8f30f11866408000076030f28c10f2fc257f30f118664080000765e"
)
FADE_FRONT_NEW = bytes.fromhex(
    "8b86c4080000"              # 0056e4db mov eax, dword ptr [esi + 0x8c4]: the blackboard
    "85c0"                      # 0056e4e1 test eax, eax: no noun
    "740b"                      # 0056e4e3 je 0x56e4f0
    "6683b8741e000000"          # 0056e4e5 cmp word ptr [eax + 0x1e74], 0: mStealthed or mbInGrass
    "0f95c0"                    # 0056e4ed setne al
    "f30f10442444"              # 0056e4f0 movss xmm0, dword ptr [esp + 0x44]: the fade as given (the fog texture)
    "f30f10155c5b3901"          # 0056e4f6 movss xmm2, dword ptr [0x1395b5c]: 0.5
    "81be7008000046414445"      # 0056e4fe cmp dword ptr [esi + 0x870], 0x45444146: an enemy creature FadeStep owns?
    "7528"                      # 0056e508 jne 0x56e532
    "f30f108668080000"          # 0056e50a movss xmm0, dword ptr [esi + 0x868]: its fade's opacity
    "f30f59c0"                  # 0056e512 mulss xmm0, xmm0: squared: the glass grows in evenly to the eye
    "57"                        # 0056e516 push edi
    "84c0"                      # 0056e517 test al, al: as shipped from here: grass or stealth 0.5
    "7408"                      # 0056e519 je 0x56e523
    "f30f5d05ec103401"          # 0056e51b minss xmm0, dword ptr [0x13410ec]: grass or stealth: at most the shipped 0.375
    "0f2f05d4a31b01"            # 0056e523 comiss xmm0, dword ptr [0x11ba3d4]: whole:
    "0f8338000000"              # 0056e52a jae 0x56e568: the normal materials
    "eb29"                      # 0056e530 jmp 0x56e55b
    "84c0"                      # 0056e532 test al, al: as shipped from here: grass or stealth 0.5
    "7403"                      # 0056e534 je 0x56e539
    "0f28c2"                    # 0056e536 movaps xmm0, xmm2
    "f30f5f0564061e01"          # 0056e539 maxss xmm0, dword ptr [0x11e0664]: at least 0.25
    "57"                        # 0056e541 push edi
    "0f2fc2"                    # 0056e542 comiss xmm0, xmm2
    "0f871d000000"              # 0056e545 ja 0x56e568: above 0.5: the normal materials
    "f30f590564061e01"          # 0056e54b mulss xmm0, dword ptr [0x11e0664]: 0.25 * opacity + 0.25
    "f30f580564061e01"          # 0056e553 addss xmm0, dword ptr [0x11e0664]
    "f30f118664080000"          # 0056e55b movss dword ptr [esi + 0x864], xmm0: the glass material's parameter
    "e95e000000"                # 0056e563 jmp 0x56e5c6: labsHeroStealth
)
FADE_PARAM = 0x0056E706       # the glass material's parameter: as shipped 0.25 * [+0x864] + 0.25, now [+0x864] itself
FADE_PARAM_ORIG = bytes.fromhex("f30f108e64080000f30f590d64061e01f30f580d64061e01")
FADE_PARAM_NEW = bytes.fromhex("f30f108e64080000") + b"\x90" * 16  # movss xmm1,[esi+0x864]
FADE_SITES = ((FADE_APPLY, FADE_APPLY_ORIG, FADE_APPLY_NEW, "ApplyCreatureOpacity and its padding"),
              (FADE_TARGET, FADE_TARGET_ORIG, FADE_TARGET_NEW, "UpdateCreatureOpacity's first assert"),
              (FADE_HOOK, FADE_HOOK_ORIG, FADE_HOOK_NEW, "UpdateVisibleObject's fogged test"),
              (FADE_FRONT, FADE_FRONT_ORIG, FADE_FRONT_NEW, "UpdateCreatureOpacity's fade"),
              (FADE_PARAM, FADE_PARAM_ORIG, FADE_PARAM_NEW, "UpdateCreatureOpacity's glass parameter"))
RATING_CAVE = 0x011AB170                  # in .text's slack after the map-name cave (0x011AB140, 45 bytes)
RATING_LABEL = "Rating "                  # before the number
RATING_LABEL_VA = 0x01C00080              # in .tls's slack after the map key (0x01C00040-0x01C0007F)
RATING_LABEL_ROOM = 0x10
RATING_TEXT = 0x01C00090                  # the text the cave builds backwards from its NUL at RATING_TEXT_END
RATING_TEXT_ROOM = 0x30
RATING_TEXT_END = RATING_TEXT + RATING_TEXT_ROOM - 2


def rel32(frm, to):
    """The rel32 of a jmp/call whose next instruction is at `frm`."""
    return ((to - frm) & 0xFFFFFFFF).to_bytes(4, "little")


def rating_cave_code():
    """The rating cave. Entry RATING_CAVE (the second site): mov ebp,7, then the text routine at RATING_CAVE+5 (the
    first site): ebx = L"Rating <n>" when the local user's DMAP[0x00070037] is above 0, else the empty string; every
    other register is kept (pushad, ebx written into its slot)."""
    a = RATING_CAVE
    c = bytearray()

    def emit(hexs, *tail):
        c.extend(bytes.fromhex(hexs))
        for t in tail:
            c.extend(t)

    emit("bd07000000")                                        # mov ebp,7        (the second site's instruction)
    emit("60")                                                # text: pushad
    emit("6a00" "6a00")                                       # push 0; push 0   ; [esp] = the int64 value
    emit("bb", EMPTY_WSTRING.to_bytes(4, "little"))           # mov ebx,empty
    emit("8b35", PLATFORM_PTR.to_bytes(4, "little"))          # mov esi,[platform]
    emit("8b06" "8bce" "ff90e4000000")                        # mov eax,[esi]; mov ecx,esi; call [eax+0xE4] ; edx:eax = local Blaze id
    emit("8b4e18" "8b8998040000")                             # mov ecx,[esi+0x18]; mov ecx,[ecx+0x498]    ; the user manager
    emit("85c9" "74")                                         # test ecx,ecx; jz done
    j1 = len(c); c.append(0)
    emit("52" "50" "e8")                                      # push edx; push eax; call FindUserByBlazeId
    c.extend(rel32(a + len(c) + 4, FIND_USER))
    emit("85c0" "74")                                         # test eax,eax; jz done
    j2 = len(c); c.append(0)
    emit("8bc8" "54")                                         # mov ecx,eax; push esp (&value)
    emit("6a", bytes([RATING_KEY]))                           # push 0x37
    emit("6a07" "e8")                                         # push 7; call GetExtendedDataValue
    c.extend(rel32(a + len(c) + 4, GET_EXT_VALUE))
    emit("84c0" "74")                                         # test al,al; jz done
    j3 = len(c); c.append(0)
    emit("8b0424" "85c0" "7e")                                # mov eax,[esp]; test eax,eax; jle done
    j4 = len(c); c.append(0)
    emit("bb", RATING_TEXT_END.to_bytes(4, "little"))         # mov ebx,the NUL
    emit("6a0a" "59")                                         # push 10; pop ecx
    d = len(c)
    emit("33d2" "f7f1" "83c230" "4b4b" "668913" "85c0" "75")  # d: xor edx,edx; div ecx; add edx,'0'; ebx-=2; [ebx]=dx; jnz d
    c.append((d - (len(c) + 1)) & 0xFF)
    emit("6a", bytes([len(RATING_LABEL)]), bytes.fromhex("59"))  # push len; pop ecx
    p = len(c)
    emit("4b4b" "668b044d", (RATING_LABEL_VA - 2).to_bytes(4, "little"))  # p: ebx-=2; mov ax,[ecx*2+label-2]
    emit("668903" "e2")                                       # mov [ebx],ax; loop p
    c.append((p - (len(c) + 1)) & 0xFF)
    done = len(c)
    for j in (j1, j2, j3, j4):
        c[j] = done - (j + 1)
    emit("83c408" "895c2410" "61" "c3")                       # done: add esp,8; mov [esp+0x10],ebx; popad; ret
    return bytes(c)


RATING_CAVE_CODE = rating_cave_code()
RATING_SITE1_NEW = (b"\xe8" + rel32(RATING_SITE1 + 5, RATING_CAVE + 5)   # call text
                    + bytes.fromhex("895c245c" "6a07" "59" "894c2458"))  # mov [esp+0x5c],ebx; push 7; pop ecx; mov [esp+0x58],ecx
RATING_SITE2_NEW = b"\xe8" + rel32(RATING_SITE2 + 5, RATING_CAVE)       # call mov ebp,7 + text


def wide(text):
    """A NUL-terminated UTF-16LE string."""
    return (text + "\0").encode("utf-16-le")


class PatchError(Exception):
    """The exe isn't the expected build, or the options don't fit the profile."""


# --- EXESECT: the section .dgx, appended after .rsrc, for code and data that no longer fit .text's slack
DGX_NAME = b".dgx"
DGX_VA = 0x01C84000            # the untouched exe's SizeOfImage: .rsrc (0x01C01000, 0x82338 bytes) ends in the page before
DGX_SIZE = 0x1000              # VirtualSize = SizeOfRawData: one page; it may grow by whole pages, no piece ever moves
DGX_CHARACTERISTICS = 0x60000020  # IMAGE_SCN_CNT_CODE | MEM_EXECUTE | MEM_READ, as .text's: not writable
DGX_MAGIC = b"DGX\0" + (1).to_bytes(4, "little")  # the section's first bytes: its name and the layout's version 1
# Every piece's fixed place in .dgx: (name, offset from DGX_VA, room in bytes). Append-only: a new piece goes after the
# last one (16-byte aligned), and a piece that has shipped never moves or shrinks, so the addresses the game's code is
# patched to reach stay where they are. exepatch's dgxLayout is the same table (both test suites pin it).
DGX_LAYOUT = (
    ("header", 0x000, 0x010),      # DGX_MAGIC
    ("draggrab", 0x010, 0x3F0),    # DRAGGRAB1: drag_grab_code() (452 bytes: the cave, the spectator helper, eps2)
    ("options", 0x400, 0x100),     # DRAGGRAB2b: drag_options_piece() (the 13-entry gameplay table, 104 bytes; the name at +0x70)
)
SECURITY_DIR = 4                   # IMAGE_DIRECTORY_ENTRY_SECURITY: EA's Authenticode certificate, at the file's end


def dgx_piece(name):
    """(VA, room) of a piece of .dgx's layout."""
    for piece, offset, room in DGX_LAYOUT:
        if piece == name:
            return DGX_VA + offset, room
    raise PatchError(f"no piece {name!r} in .dgx's layout")


def _pe_fields(data):
    """The offsets of IMAGE_FILE_HEADER and the optional header, and the section headers as
    (header offset, name, VirtualSize, VirtualAddress, SizeOfRawData, PointerToRawData)."""
    u16 = lambda o: int.from_bytes(data[o:o + 2], "little")  # noqa: E731
    u32 = lambda o: int.from_bytes(data[o:o + 4], "little")  # noqa: E731
    if len(data) < 0x40 or data[:2] != b"MZ":
        raise PatchError("not a PE file (no MZ header)")
    fh = u32(0x3C) + 4
    if data[fh - 4:fh] != b"PE\0\0":
        raise PatchError("not a PE file (no PE signature)")
    opt = fh + 20
    table = opt + u16(fh + 16)
    secs = []
    for i in range(u16(fh + 2)):
        h = table + 40 * i
        secs.append((h, bytes(data[h:h + 8]).rstrip(b"\0"), u32(h + 8), u32(h + 12), u32(h + 16), u32(h + 20)))
    return fh, opt, secs


def add_dgx_section(orig):
    """The exe's bytes with the empty section .dgx appended (the patches place its pieces): a sixth section header
    in the free slot after the fifth, VirtualAddress DGX_VA - ImageBase (the untouched SizeOfImage), its raw data
    after the file's end aligned to the file alignment (0x200), so EA's certificate stays where it is, unused;
    SizeOfImage and SizeOfCode raised by DGX_SIZE; the security directory zeroed (the certificate no longer ends the
    file and has been invalid since the first patch: the file now says it is unsigned). The PE checksum stays as
    shipped (stale, as after every other patch: the loader checks it only for drivers)."""
    data = bytearray(orig)
    fh, opt, secs = _pe_fields(data)
    u32 = lambda o: int.from_bytes(data[o:o + 4], "little")  # noqa: E731

    def put(o, v, n=4):
        data[o:o + n] = v.to_bytes(n, "little")

    size_of_image, size_of_headers = u32(opt + 56), u32(opt + 60)
    if (len(secs) != 5 or int.from_bytes(data[opt:opt + 2], "little") != 0x10B or u32(opt + 32) != 0x1000
            or u32(opt + 36) != 0x200 or size_of_image != DGX_VA - IMAGE_BASE or any(s[1] == DGX_NAME for s in secs)):
        raise PatchError(f"unexpected PE header for .dgx ({len(secs)} sections, SizeOfImage {size_of_image:#x}; "
                         "wrong build, or not the untouched exe?)")
    if max((va + vsize + 0xFFF) & ~0xFFF for _h, _n, vsize, va, _rs, _rp in secs) != size_of_image:
        raise PatchError("the last section doesn't end at SizeOfImage (wrong build?)")
    slot = secs[-1][0] + 40
    if slot + 40 > size_of_headers or any(data[slot:slot + 40]):
        raise PatchError("no free section header after the last one (wrong build?)")
    raw_end = max(rp + rs for _h, _n, _vs, _va, rs, rp in secs)
    sec_dir = opt + 96 + 8 * SECURITY_DIR
    cert_at, cert_len = u32(sec_dir), u32(sec_dir + 4)
    if (cert_at or cert_len) and (cert_at < raw_end or cert_at + cert_len != len(data)):
        raise PatchError(f"the certificate ({cert_at:#x}, {cert_len:#x} bytes) doesn't end the file (wrong build?)")
    if len(data) < raw_end:
        raise PatchError("the file ends before its last section's raw data (truncated?)")
    put(sec_dir, 0)
    put(sec_dir + 4, 0)
    raw = (len(data) + 0x1FF) & ~0x1FF
    data += bytes(raw - len(data) + DGX_SIZE)
    data[slot:slot + 8] = DGX_NAME.ljust(8, b"\0")
    put(slot + 8, DGX_SIZE)                   # VirtualSize
    put(slot + 12, DGX_VA - IMAGE_BASE)       # VirtualAddress
    put(slot + 16, DGX_SIZE)                  # SizeOfRawData
    put(slot + 20, raw)                       # PointerToRawData (relocations, line numbers: none)
    put(slot + 36, DGX_CHARACTERISTICS)
    put(fh + 2, len(secs) + 1, 2)             # NumberOfSections
    put(opt + 56, size_of_image + DGX_SIZE)   # SizeOfImage
    put(opt + 4, u32(opt + 4) + DGX_SIZE)     # SizeOfCode
    return bytes(data)


def dgx_place(data, piece, blob):
    """Write `blob` at its piece's fixed place in .dgx (data: a bytearray of an exe that has the section; the room must
    still be empty) and return its VA. The API for patches whose code or data no longer fit .text's slack: declare the
    piece in DGX_LAYOUT (and exepatch's dgxLayout), build its bytes for dgx_piece(name)'s VA, place them."""
    va, room = dgx_piece(piece)
    if len(blob) > room:
        raise PatchError(f".dgx's {piece} piece has room for {room} bytes, not {len(blob)}")
    _fh, _opt, secs = _pe_fields(data)
    sec = next((s for s in secs if s[1] == DGX_NAME), None)
    if sec is None or sec[3] != DGX_VA - IMAGE_BASE or sec[4] < DGX_SIZE:
        raise PatchError(f"{piece} needs the section .dgx (--dgx on)")
    at = sec[5] + va - DGX_VA
    if at + len(blob) > len(data):
        raise PatchError(f".dgx's {piece} piece is beyond the end of the file")
    if any(data[at:at + len(blob)]):
        raise PatchError(f".dgx's {piece} piece is written already")
    data[at:at + len(blob)] = blob
    return va


# --- DRAGGRAB1: a grab-style middle-mouse drag scroll, switched in the game by the preference OptionDragGrab
# GameplayControls_UpdateEdgeAndDragScroll (0x00697D80) runs the joystick while trigger 0xA686879F (the middle button)
# is down. Its frame at the hook (sub esp,0x40; push ebx; push ebp): ebx = the camera, [esp+0x08] = dx and [esp+0x4C] =
# dy (both 0 here) for SPGearsCamera_AddTranslateInput, the byte [esp+0x18] = the "trigger down" flag the epilogue
# hands to SetDragScrollActive.
DRAG_HOOK = 0x00697EB8                    # the joystick's first instructions (12 bytes): jmp into .dgx + 7 nops
DRAG_HOOK_ORIG = bytes.fromhex("803ddd7f970100" "c644241801")  # cmp byte [0x01977FDD],0; mov byte [esp+0x18],1
DRAG_RESUME = 0x00697EC4                  # jne 0x00697ED5 (later frames) / the first frame's SetDragScrollActive(1)
DRAG_NO_MOVE = 0x006980B5                 # the shared "no move" path (the joystick's first frame and dead zone)
DRAG_MOVED = 0x0069810B                   # the shared "moved" path: stopwatch, end a temporary lock, mode-6 translate
DRAG_SPEC_SITES = ((0x00697D3C, bytes.fromhex("e8cf52fdff")),  # SetDragScrollActive(0): call IsSpectating
                   (0x006981A0, bytes.fromhex("e86b4efdff")))  # the no-camera path: call IsSpectating
DRAG_ACTIVE = 0x01977FDD                  # byte: a drag is active (SetDragScrollActive)
APPPREFS = 0x01BA4118                     # the AppPrefs object
APPPREFS_GETBOOL = 0x00A83800             # thiscall (key), ret 4; a missing key is false
DRAG_GRAB_KEY = 0x14958012                # FNV-1 of "optiondraggrab" (the key OptionDragGrab)
SET_DRAG_ACTIVE = 0x00697CC0              # GameplayControls_SetDragScrollActive(bool), cdecl
GET_CURSOR_GROUND = 0x006993A0            # GameplayControls_GetCursorGroundPoint(out, fromMinimap), cdecl
CAMERA_MANAGER = 0x00A6F910               # returns [0x01BA40C0]; its slot +0x6C, then +0x1C: the viewer
VIEWER_GET_CURSOR_RAY = 0x00A6B670        # thiscall viewer (start*, end*), ret 8: the cursor's ray (x, y, z each)
IS_SPECTATING = 0x0066D010                # the observer test before the cursor's jump back
LOOK_AT_CURRENT = 0x013F5724              # SPGearsCamera's look-at point, current (x, y, z)
LOOK_AT_TARGET = 0x013F5730               # and target
DRAG_STATE = 0x01C000C0                   # in .tls's slack after the rating text (0x01C00090-0x01C000BF)
DRAG_STATE_ROOM = 0x30
DRAG_G0 = DRAG_STATE                      # the grabbed ground point G0 (x, y, z), set at the drag's start
DRAG_GRAB = DRAG_STATE + 0x0C             # byte: this drag is a grab (the preference read at its start)
DRAG_RAY_START = DRAG_STATE + 0x10        # the cursor ray's start (x, y, z)
DRAG_RAY_END = DRAG_STATE + 0x1C          # and end
DRAG_EPS2 = 1e-6                          # |d|^2 below this (1 mm): the mouse stopped, no move
DRAG_SPEC_OFFSET = 0x1A0                  # the spectator helper's offset in the piece (after the grab step)
DRAG_EPS2_OFFSET = 0x1C0                  # the constant's offset


def drag_grab_code():
    """The draggrab piece of .dgx (at dgx_piece("draggrab")'s VA): the cave the hook jumps to, the spectator helper
    at +DRAG_SPEC_OFFSET and the constant DRAG_EPS2 at +DRAG_EPS2_OFFSET. Its source, as assembled here:

    cave:   cmp byte [DRAG_ACTIVE],0 ; jne later           ; a drag's first frame:
            push DRAG_GRAB_KEY ; mov ecx,[APPPREFS] ; call AppPrefs_GetBool
            mov [DRAG_GRAB],al ; test al,al ; jz shipped    ; the preference, read once per drag
            mov byte [esp+0x18],1 ; push 1 ; call SetDragScrollActive
            push 0 ; push DRAG_G0 ; call GetCursorGroundPoint ; add esp,0xC
            jmp DRAG_NO_MOVE                                ; G0 = the ground point under the cursor
    later:  cmp byte [DRAG_GRAB],0 ; jne grab
    shipped: cmp byte [DRAG_ACTIVE],0 ; mov byte [esp+0x18],1 ; jmp DRAG_RESUME   ; the joystick, as shipped
    grab:   mov byte [esp+0x18],1
            call CameraManager ; ecx = eax ; call [vt+0x6C] ; ecx = eax ; call [vt+0x1C]   ; the viewer
            push DRAG_RAY_END ; push DRAG_RAY_START ; mov ecx,eax ; call Viewer_GetCursorRay
            xmm2 = end.z - start.z ; if 0 or NaN: jmp nomove
            t = (G0.z - start.z) / xmm2                    ; the ray on the plane z = G0.z
            d.x (xmm2) = G0.x - (start.x + t * (end.x - start.x))
            d.y (xmm3) = G0.y - (start.y + t * (end.y - start.y))
            if d.x^2 + d.y^2 <= DRAG_EPS2 (or NaN): jmp nomove     ; the mouse stopped: the camera stops
            if [ebx+0x30] == 6 and [ebx+0x2C] not in (3, 4):        ; the match camera, not following
                current.xy += d ; target.xy += d                     ; no smoothing lag, no overshoot
    zero:   [esp+0x08] = [esp+0x4C] = 0 ; jmp DRAG_MOVED            ; AddTranslateInput adds nothing
    nomove: jmp DRAG_NO_MOVE
    spec:   cmp byte [DRAG_GRAB],0 ; jne +5 ; jmp IsSpectating ; xor eax,eax ; ret   ; a grab: no jump back
    """
    va, _room = dgx_piece("draggrab")
    c = bytearray()
    labels, fixups = {}, []

    def emit(hexs, *tail):
        c.extend(bytes.fromhex(hexs))
        for t in tail:
            c.extend(t)

    def u32(v):
        return v.to_bytes(4, "little")

    def to(op, target):                       # call/jmp rel32 to an exe address
        emit(op)
        c.extend(rel32(va + len(c) + 4, target))

    def short(op, label):                     # jcc/jmp rel8 to a label in this piece
        emit(op)
        fixups.append((len(c), label, 1))
        c.append(0)

    def near(op, label):                      # jcc rel32 (0f 8x) to a label in this piece
        emit(op)
        fixups.append((len(c), label, 4))
        c.extend(bytes(4))

    def label(name):
        labels[name] = len(c)

    label("cave")
    emit("803d", u32(DRAG_ACTIVE), b"\x00")               # cmp byte [active],0
    short("75", "later")                                   # jne later
    emit("68", u32(DRAG_GRAB_KEY))                         # push key
    emit("8b0d", u32(APPPREFS))                            # mov ecx,[appprefs]
    to("e8", APPPREFS_GETBOOL)                             # call AppPrefs_GetBool
    emit("a2", u32(DRAG_GRAB))                             # mov [grab],al
    emit("84c0")                                           # test al,al
    short("74", "shipped")                                 # jz shipped
    emit("c644241801")                                     # mov byte [esp+0x18],1
    emit("6a01")                                           # push 1
    to("e8", SET_DRAG_ACTIVE)                              # call SetDragScrollActive
    emit("6a00" "68", u32(DRAG_G0))                        # push 0; push G0
    to("e8", GET_CURSOR_GROUND)                            # call GetCursorGroundPoint
    emit("83c40c")                                         # add esp,0xC
    to("e9", DRAG_NO_MOVE)                                 # jmp no-move
    label("later")
    emit("803d", u32(DRAG_GRAB), b"\x00")                  # cmp byte [grab],0
    short("75", "grab")                                    # jne grab
    label("shipped")
    c.extend(DRAG_HOOK_ORIG)                               # the two hooked instructions
    to("e9", DRAG_RESUME)                                  # jmp resume
    label("grab")
    emit("c644241801")                                     # mov byte [esp+0x18],1
    to("e8", CAMERA_MANAGER)                               # call CameraManager
    emit("8b10" "8bc8" "ff526c")                           # mov edx,[eax]; mov ecx,eax; call [edx+0x6c]
    emit("8b10" "8bc8" "ff521c")                           # mov edx,[eax]; mov ecx,eax; call [edx+0x1c]: viewer
    emit("68", u32(DRAG_RAY_END), b"\x68", u32(DRAG_RAY_START))  # push end; push start
    emit("8bc8")                                           # mov ecx,eax
    to("e8", VIEWER_GET_CURSOR_RAY)                        # call Viewer_GetCursorRay
    emit("f30f1015", u32(DRAG_RAY_END + 8))                # movss xmm2,[end.z]
    emit("f30f5c15", u32(DRAG_RAY_START + 8))              # subss xmm2,[start.z]
    emit("0f57db" "0f2ed3")                                # xorps xmm3,xmm3; ucomiss xmm2,xmm3
    near("0f8a", "nomove")                                 # jp nomove
    near("0f84", "nomove")                                 # jz nomove
    emit("f30f1005", u32(DRAG_G0 + 8))                     # movss xmm0,[G0.z]
    emit("f30f5c05", u32(DRAG_RAY_START + 8))              # subss xmm0,[start.z]
    emit("f30f5ec2")                                       # divss xmm0,xmm2: t
    for axis, (g0_reg, sub) in enumerate((("15", "d1"), ("1d", "d9"))):  # x into xmm2, y into xmm3
        emit("f30f100d", u32(DRAG_RAY_END + 4 * axis))     # movss xmm1,[end]
        emit("f30f5c0d", u32(DRAG_RAY_START + 4 * axis))   # subss xmm1,[start]
        emit("f30f59c8")                                   # mulss xmm1,xmm0
        emit("f30f580d", u32(DRAG_RAY_START + 4 * axis))   # addss xmm1,[start]: G
        emit("f30f10" + g0_reg, u32(DRAG_G0 + 4 * axis))   # movss xmm2|xmm3,[G0]
        emit("f30f5c" + sub)                               # subss xmm2|xmm3,xmm1: d
    emit("0f28c2" "f30f59c2")                              # movaps xmm0,xmm2; mulss xmm0,xmm2
    emit("0f28cb" "f30f59cb")                              # movaps xmm1,xmm3; mulss xmm1,xmm3
    emit("f30f58c1")                                       # addss xmm0,xmm1
    emit("0f2f05", u32(va + DRAG_EPS2_OFFSET))             # comiss xmm0,[eps2]
    short("76", "nomove")                                  # jbe nomove
    emit("837b3006")                                       # cmp dword [ebx+0x30],6
    short("75", "zero")                                    # jne zero
    emit("8b432c" "83f803")                                # mov eax,[ebx+0x2c]; cmp eax,3
    short("74", "zero")                                    # je zero
    emit("83f804")                                         # cmp eax,4
    short("74", "zero")                                    # je zero
    for point in (LOOK_AT_CURRENT, LOOK_AT_TARGET):
        for axis, reg in ((0, "c2"), (1, "c3")):
            emit("f30f1005", u32(point + 4 * axis))        # movss xmm0,[p]
            emit("f30f58" + reg)                           # addss xmm0,xmm2|xmm3
            emit("f30f1105", u32(point + 4 * axis))        # movss [p],xmm0
    label("zero")
    emit("33c0" "89442408" "8944244c")                     # xor eax,eax; mov [esp+8],eax; mov [esp+0x4c],eax
    to("e9", DRAG_MOVED)                                   # jmp moved
    label("nomove")
    to("e9", DRAG_NO_MOVE)                                 # jmp no-move
    for at, name, size in fixups:
        rel = labels[name] - (at + size)
        if size == 1 and not -128 <= rel <= 127:
            raise PatchError(f"draggrab: short jump to {name} out of range")
        c[at:at + size] = (rel & (1 << 8 * size) - 1).to_bytes(size, "little")
    if len(c) > DRAG_SPEC_OFFSET:
        raise PatchError(f"draggrab: the cave ({len(c)} bytes) runs into the spectator helper")
    c.extend(bytes(DRAG_SPEC_OFFSET - len(c)))
    emit("803d", u32(DRAG_GRAB), b"\x00")                  # spec: cmp byte [grab],0
    emit("7505")                                           # jne +5
    to("e9", IS_SPECTATING)                                # jmp IsSpectating
    emit("33c0" "c3")                                      # xor eax,eax; ret
    c.extend(bytes(DRAG_EPS2_OFFSET - len(c)))
    c.extend(struct.pack("<f", DRAG_EPS2))
    return bytes(c)


def drag_grab_sites():
    """((VA, original bytes, patched bytes), ...): the hook and the two spectator sites."""
    va, _room = dgx_piece("draggrab")
    hook = b"\xe9" + rel32(DRAG_HOOK + 5, va) + b"\x90" * (len(DRAG_HOOK_ORIG) - 5)
    sites = [(DRAG_HOOK, DRAG_HOOK_ORIG, hook)]
    for site, orig in DRAG_SPEC_SITES:
        sites.append((site, orig, b"\xe8" + rel32(site + 5, va + DRAG_SPEC_OFFSET)))
    return tuple(sites)


# The gameplay options' table, copied into .dgx with a 13th entry (OptionDragGrab, the Options menu's grab-style
# drag scroll)
OPT_TABLE = 0x011D1988                    # 12 x {u32 FNV-1 of the lower-case name, char *name}, OptionFilterProfanity..
OPT_TABLE_SIZE = 12 * 8
OPT_TABLE_FIRST = (0x90FBF794).to_bytes(4, "little")  # its first hash: FNV-1 of "optionfilterprofanity"
OPT_NAME = b"OptionDragGrab\0"
OPT_NAME_OFFSET = 0x70                    # the name's offset in the options piece (after the 13 entries)


def drag_options_piece(table):
    """The options piece of .dgx: `table` (EA's 12 entries, read from the exe being patched), the 13th entry
    {DRAG_GRAB_KEY, the name's VA}, zeros, the name at OPT_NAME_OFFSET."""
    va, _room = dgx_piece("options")
    if len(table) != OPT_TABLE_SIZE:
        raise PatchError(f"options table: {len(table)} bytes, expected {OPT_TABLE_SIZE}")
    blob = bytes(table) + struct.pack("<II", DRAG_GRAB_KEY, va + OPT_NAME_OFFSET)
    return blob + bytes(OPT_NAME_OFFSET - len(blob)) + OPT_NAME


def drag_options_sites():
    """((VA, original bytes, patched bytes), ...): the table's four disp32 readers and the two loop bounds in
    OptionsUI_GetGameSettings (0x004D3F90) and OptionsUI_SaveSettings (0x004D60C0)."""
    va, _room = dgx_piece("options")
    mov = lambda op, disp: bytes.fromhex(op) + struct.pack("<I", disp)  # noqa: E731
    return (
        (0x004D4070, mov("8bb7", OPT_TABLE), mov("8bb7", va)),            # mov esi,[edi+table]      (hash)
        (0x004D40AC, mov("8b87", OPT_TABLE + 4), mov("8b87", va + 4)),    # mov eax,[edi+table+4]    (name)
        (0x004D414E, bytes.fromhex("83ff60"), bytes.fromhex("83ff68")),   # cmp edi,0x60 -> 0x68
        (0x004D66A0, mov("8b95", OPT_TABLE + 4), mov("8b95", va + 4)),    # mov edx,[ebp+table+4]    (name)
        (0x004D66A6, mov("8b8d", OPT_TABLE), mov("8b8d", va)),            # mov ecx,[ebp+table]      (hash)
        (0x004D66BE, bytes.fromhex("83fd60"), bytes.fromhex("83fd68")),   # cmp ebp,0x60 -> 0x68
    )


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
                laa=True, preload=True, maptable=True, rating=True, heaplocks=True, numfmt=True, averages=True,
                fade=True, dgx=True, draggrab=True, profilereload=True):
    """Patch a copy of the untouched exe's bytes; returns (patched bytes, summary text). draggrab needs dgx (without
    the section it is left out: the cave lives there)."""
    server, cave_address = resolve_profile(profile, host, server_host, client_bind)
    check_data_suffix(data_suffix)
    data = bytearray(add_dgx_section(orig) if dgx else orig)  # EXESECT: first, so the patches can address .dgx
    if dgx:
        dgx_place(data, "header", DGX_MAGIC)
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

    # --- SNDBANK: preload every player index's shaper (0-14), not only the first CountPlayers() indexes
    expect(PRELOAD_COUNT, PRELOAD_COUNT_ORIG, "GameClient_PreloadPlayerCharacters' call to GameSimulator_CountPlayers")
    if preload:
        data[off(PRELOAD_COUNT):off(PRELOAD_COUNT) + len(PRELOAD_COUNT_NEW)] = PRELOAD_COUNT_NEW
        summary += "; every player's shaper preloaded (player indexes 0-14)"

    # --- MAPTABLE: a LevelId the map table lacks is named by the lobby table's line of that id
    for va, want, what in ((MAP_LOOP, MAP_LOOP_ORIG, "the map-name method's one-entry loop"),
                           (MAP_MISS, MAP_MISS_ORIG, "the map-name method's not-found exit"),
                           (MAP_FOUND, MAP_FOUND_ORIG, "the map-name method's found path"),
                           (MAP_RESUME, MAP_RESUME_ORIG, "the map-name method's key check")):
        expect(va, want, what)
    tls = next((s for s in pe.sections if s.Name.rstrip(b"\0") == b".tls"), None)
    if (tls is None or tls.VirtualAddress != TLS_VA - IMAGE_BASE or tls.Misc_VirtualSize != TLS_VSIZE
            or tls.SizeOfRawData != TLS_RAW or not tls.Characteristics & 0x80000000):
        raise PatchError("unexpected .tls section (wrong build?)")
    if maptable:
        if pe.OPTIONAL_HEADER.DllCharacteristics & 0x40 or not pe.FILE_HEADER.Characteristics & 0x1:
            raise PatchError("exe is relocatable (ASLR or relocations); absolute addresses in the cave would break")
        if not (LAN_LOOPBACK_STRING + LAN_LOOPBACK_ROOM <= MAP_CAVE
                and MAP_CAVE + len(MAP_CAVE_CODE) <= IMAGE_BASE + text.VirtualAddress + text.SizeOfRawData):
            raise PatchError("the map-name cave doesn't fit into .text's slack (wrong build?)")
        expect(MAP_CAVE, bytes(len(MAP_CAVE_CODE)), "unused slack after the bind cave")
        expect(MAP_KEY, bytes(MAP_KEY_ROOM), "unused slack after the TLS template")
        for sec, size in ((text, text.SizeOfRawData), (tls, TLS_RAW)):  # map the slack (VirtualSize = SizeOfRawData)
            vsize_at = sec.get_file_offset() + 8
            data[vsize_at:vsize_at + 4] = size.to_bytes(4, "little")
        data[off(MAP_CAVE):off(MAP_CAVE) + len(MAP_CAVE_CODE)] = MAP_CAVE_CODE
        data[off(MAP_KEY):off(MAP_KEY) + MAP_KEY_ROOM] = wide(MAP_KEY_TEXT).ljust(MAP_KEY_ROOM, b"\0")
        data[off(MAP_MISS):off(MAP_MISS) + len(MAP_MISS_NEW)] = MAP_MISS_NEW
        summary += f"; every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at {MAP_CAVE:#010x})"

    # --- RATINGSHOW1: the profile box's rating text from the local user's DMAP[0x00070037]
    for va, want, what in ((RATING_SITE1, RATING_SITE1_ORIG, "the extended-data handler's rating value"),
                           (RATING_SITE2, RATING_SITE2_ORIG + RATING_NEXT2, "the main menu bind's rating value")):
        expect(va, want, what)
    if rating:
        if pe.OPTIONAL_HEADER.DllCharacteristics & 0x40 or not pe.FILE_HEADER.Characteristics & 0x1:
            raise PatchError("exe is relocatable (ASLR or relocations); absolute addresses in the cave would break")
        if not (MAP_CAVE + len(MAP_CAVE_CODE) <= RATING_CAVE
                and RATING_CAVE + len(RATING_CAVE_CODE) <= IMAGE_BASE + text.VirtualAddress + text.SizeOfRawData):
            raise PatchError("the rating cave doesn't fit into .text's slack (wrong build?)")
        if not (MAP_KEY + MAP_KEY_ROOM <= RATING_LABEL_VA and RATING_TEXT + RATING_TEXT_ROOM <= TLS_VA + TLS_RAW
                and 2 * (len(RATING_LABEL) + 10) + 2 <= RATING_TEXT_ROOM):
            raise PatchError("the rating text doesn't fit into .tls's slack")
        expect(RATING_CAVE, bytes(len(RATING_CAVE_CODE)), "unused slack after the map-name cave")
        expect(RATING_LABEL_VA, bytes(RATING_LABEL_ROOM + RATING_TEXT_ROOM), "unused slack after the map key")
        for sec, size in ((text, text.SizeOfRawData), (tls, TLS_RAW)):  # map the slack (VirtualSize = SizeOfRawData)
            vsize_at = sec.get_file_offset() + 8
            data[vsize_at:vsize_at + 4] = size.to_bytes(4, "little")
        data[off(RATING_CAVE):off(RATING_CAVE) + len(RATING_CAVE_CODE)] = RATING_CAVE_CODE
        label = RATING_LABEL.encode("utf-16-le")
        data[off(RATING_LABEL_VA):off(RATING_LABEL_VA) + RATING_LABEL_ROOM] = label.ljust(RATING_LABEL_ROOM, b"\0")
        data[off(RATING_SITE1):off(RATING_SITE1) + len(RATING_SITE1_NEW)] = RATING_SITE1_NEW
        data[off(RATING_SITE2):off(RATING_SITE2) + len(RATING_SITE2_NEW)] = RATING_SITE2_NEW
        summary += f"; the profile box shows the rating the server sends (rating cave at {RATING_CAVE:#010x})"
    # --- UICHAN: every Scaleform heap takes its lock, as the movies' sockets need
    for va in HEAP_LOCKS:
        expect(va, HEAP_LOCKS_ORIG, "a heap constructor's UseLocks")
    if heaplocks:
        for va in HEAP_LOCKS:
            data[off(va):off(va) + len(HEAP_LOCKS_NEW)] = HEAP_LOCKS_NEW
        summary += "; the UI's memory heaps take their locks (a movie's socket reads on a thread)"
    # --- NUMFMT: the UI's numbers print with 15 digits: 8.2, not 8.199999999999999
    expect(NUMFMT_FORMAT, NUMFMT_FORMAT_ORIG, "the number-to-string's format")
    expect(NUMFMT_STEP, NUMFMT_STEP_ORIG, "the number-to-string's step to more digits")
    expect(NUMFMT_1E15, NUMFMT_1E15_VALUE, "the 1e15 constant")
    if numfmt:
        data[off(NUMFMT_FORMAT):off(NUMFMT_FORMAT) + len(NUMFMT_FORMAT_NEW)] = NUMFMT_FORMAT_NEW
        data[off(NUMFMT_STEP):off(NUMFMT_STEP) + len(NUMFMT_STEP_NEW)] = NUMFMT_STEP_NEW
        summary += "; the UI's numbers print with 15 digits (8.2, not 8.199999999999999)"
    # --- AVGPREC: the profile's averages are real divisions at full precision: 8.7, not 8.6
    expect(AVERAGES_BLOCK, AVERAGES_ORIG, "the profile averages' block")
    if averages:
        data[off(AVERAGES_BLOCK):off(AVERAGES_BLOCK) + len(AVERAGES_NEW)] = AVERAGES_NEW
        summary += "; the profile's averages at full precision (8.7, not 8.6)"
    # --- FADE1: an enemy creature is drawn by its team bit and fades in and out at the fog's edge
    for va, want, _new, what in FADE_SITES:
        expect(va, want, what)
    if fade:
        for va, _orig, new, _what in FADE_SITES:
            data[off(va):off(va) + len(new)] = new
        summary += "; enemy creatures fade in and out by their team's sight and the fog (0.25 s)"
    # --- DRAGGRAB1: the middle-mouse drag grabs the ground when the preference OptionDragGrab is on
    for va, want, _new in drag_grab_sites():
        expect(va, want, "the drag scroll's hook site")
    for va, want, _new in drag_options_sites():
        expect(va, want, "the gameplay options table's reader")
    expect(OPT_TABLE, OPT_TABLE_FIRST, "the gameplay options table (OptionFilterProfanity first)")
    if dgx and draggrab:
        if pe.OPTIONAL_HEADER.DllCharacteristics & 0x40 or not pe.FILE_HEADER.Characteristics & 0x1:
            raise PatchError("exe is relocatable (ASLR or relocations); absolute addresses in the cave would break")
        if not (RATING_TEXT + RATING_TEXT_ROOM <= DRAG_STATE and DRAG_STATE + DRAG_STATE_ROOM <= TLS_VA + TLS_RAW
                and DRAG_RAY_END + 12 <= DRAG_STATE + DRAG_STATE_ROOM):
            raise PatchError("the drag grab's state doesn't fit into .tls's slack")
        expect(DRAG_STATE, bytes(DRAG_STATE_ROOM), "unused slack after the rating text")
        vsize_at = tls.get_file_offset() + 8      # map .tls's slack (VirtualSize = SizeOfRawData)
        data[vsize_at:vsize_at + 4] = TLS_RAW.to_bytes(4, "little")
        cave_va = dgx_place(data, "draggrab", drag_grab_code())
        for va, _want, new in drag_grab_sites():
            data[off(va):off(va) + len(new)] = new
        table_va = dgx_place(data, "options", drag_options_piece(data[off(OPT_TABLE):off(OPT_TABLE) + OPT_TABLE_SIZE]))
        for va, _want, new in drag_options_sites():
            data[off(va):off(va) + len(new)] = new
        summary += (f"; the middle-mouse drag grabs the ground when the preference OptionDragGrab is on "
                    f"(cave at {cave_va:#010x}, the Options menu's 13-entry gameplay table at {table_va:#010x})")
    if dgx:
        summary += f"; an executable section .dgx at {DGX_VA:#010x} ({DGX_SIZE} bytes) for later patches"
    # --- PROFILE2: the Profile loads the same player again when asked (its mode tabs)
    expect(PROFILE_RELOAD_SITE, PROFILE_RELOAD_ORIG, "the Profile's same-player check")
    if profilereload:
        data[off(PROFILE_RELOAD_SITE):off(PROFILE_RELOAD_SITE) + len(PROFILE_RELOAD_NEW)] = PROFILE_RELOAD_NEW
        summary += "; the Profile loads its player again when asked (its mode tabs)"
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
    count = at(PRELOAD_COUNT, len(PRELOAD_COUNT_ORIG))
    out["preload"] = {PRELOAD_COUNT_NEW: "all", PRELOAD_COUNT_ORIG: "as-shipped"}.get(count, "unknown")
    miss = at(MAP_MISS, len(MAP_MISS_ORIG))
    if miss == MAP_MISS_ORIG:
        out["maptable"] = "as-shipped"
    elif (miss[:5] == MAP_MISS_NEW and at(MAP_CAVE, len(MAP_CAVE_CODE)) == MAP_CAVE_CODE
          and at(MAP_KEY, len(wide(MAP_KEY_PREFIX)) - 2) == wide(MAP_KEY_PREFIX)[:-2]):
        out["maptable"] = "on"
    else:
        out["maptable"] = "unknown"
    site1, site2 = at(RATING_SITE1, len(RATING_SITE1_ORIG)), at(RATING_SITE2, len(RATING_SITE2_ORIG))
    if site1 == RATING_SITE1_ORIG and site2 == RATING_SITE2_ORIG:
        out["rating"] = "as-shipped"
    elif (site1 == RATING_SITE1_NEW and site2 == RATING_SITE2_NEW
          and at(RATING_CAVE, len(RATING_CAVE_CODE)) == RATING_CAVE_CODE
          and at(RATING_LABEL_VA, 2 * len(RATING_LABEL)) == RATING_LABEL.encode("utf-16-le")):
        out["rating"] = "on"
    else:
        out["rating"] = "unknown"
    locks = {at(va, len(HEAP_LOCKS_NEW)) for va in HEAP_LOCKS}
    out["heaplocks"] = {frozenset({HEAP_LOCKS_NEW}): "on", frozenset({HEAP_LOCKS_ORIG}): "as-shipped"}.get(frozenset(locks), "unknown")
    numfmt = (at(NUMFMT_FORMAT, len(NUMFMT_FORMAT_NEW)), at(NUMFMT_STEP, len(NUMFMT_STEP_NEW)))
    out["numfmt"] = {(NUMFMT_FORMAT_NEW, NUMFMT_STEP_NEW): "on", (NUMFMT_FORMAT_ORIG, NUMFMT_STEP_ORIG): "as-shipped"}.get(numfmt, "unknown")
    out["averages"] = {AVERAGES_NEW: "on", AVERAGES_ORIG: "as-shipped"}.get(at(AVERAGES_BLOCK, len(AVERAGES_ORIG)), "unknown")
    fade = tuple(at(va, len(o)) for va, o, _n, _w in FADE_SITES)
    out["fade"] = {tuple(n for _v, _o, n, _w in FADE_SITES): "on",
                   tuple(o for _v, o, _n, _w in FADE_SITES): "as-shipped"}.get(fade, "unknown")
    sec = next((s for s in pe.sections if s.Name.rstrip(b"\0") == DGX_NAME), None)
    if sec is None:
        out["dgx"] = "as-shipped"
    elif (sec.VirtualAddress == DGX_VA - IMAGE_BASE and sec.Misc_VirtualSize == DGX_SIZE == sec.SizeOfRawData
          and sec.Characteristics == DGX_CHARACTERISTICS and at(DGX_VA, len(DGX_MAGIC)) == DGX_MAGIC):
        out["dgx"] = "on"
    else:
        out["dgx"] = "unknown"
    sites = drag_grab_sites() + drag_options_sites()
    got = tuple(at(va, len(want)) for va, want, _new in sites)
    if got == tuple(want for _va, want, _new in sites):
        out["draggrab"] = "as-shipped"
    elif (out["dgx"] == "on" and got == tuple(new for _va, _want, new in sites)
          and at(dgx_piece("draggrab")[0], len(drag_grab_code())) == drag_grab_code()
          and at(dgx_piece("options")[0], OPT_NAME_OFFSET + len(OPT_NAME))
          == drag_options_piece(at(OPT_TABLE, OPT_TABLE_SIZE))):
        out["draggrab"] = "on"
    else:
        out["draggrab"] = "unknown"
    out["profilereload"] = {PROFILE_RELOAD_NEW: "on", PROFILE_RELOAD_ORIG: "as-shipped"}.get(
        at(PROFILE_RELOAD_SITE, len(PROFILE_RELOAD_ORIG)), "unknown")
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
    ap.add_argument("--preload", choices=("on", "off"), default="on",
                    help="SNDBANK: preload every player index's shaper (and its sound banks); on by default")
    ap.add_argument("--maptable", choices=("on", "off"), default="on",
                    help="MAPTABLE: name every map by its LevelId's line in FE_CustomGameLobby; on by default")
    ap.add_argument("--heap-locks", choices=("on", "off"), default="on",
                    help="UICHAN: every Scaleform heap takes its lock, as the movies' sockets need; on by default")
    ap.add_argument("--number-format", choices=("on", "off"), default="on",
                    help="NUMFMT: the UI's numbers print with 15 digits (8.2, not 8.199999999999999); on by default")
    ap.add_argument("--averages", choices=("on", "off"), default="on",
                    help="AVGPREC: the profile's averages are real divisions at full precision (8.7, not 8.6); on by default")
    ap.add_argument("--profile-reload", choices=("on", "off"), default="on",
                    help="PROFILE2: the Profile loads the same player again when asked (its mode tabs); on by default")
    ap.add_argument("--fade", choices=("on", "off"), default="on",
                    help="FADE1: enemy creatures are drawn by their team's sight and fade in and out; on by default")
    ap.add_argument("--rating", choices=("on", "off"), default="on",
                    help="RATINGSHOW1: the main menu's profile box shows the rating the server sends; on by default")
    ap.add_argument("--dgx", choices=("on", "off"), default="on",
                    help="EXESECT: append the executable section .dgx (room for later patches); on by default")
    ap.add_argument("--drag-grab", choices=("on", "off"), default=None,
                    help="DRAGGRAB1: the middle-mouse drag grabs the ground when the game's preference OptionDragGrab "
                         "is on (the joystick as shipped while it is off or missing), and the Options menu's 13th "
                         "gameplay entry OptionDragGrab (DRAGGRAB2b); needs .dgx; on with --dgx on")
    args = ap.parse_args(argv[1:])
    if args.drag_grab == "on" and args.dgx == "off":
        sys.exit("--drag-grab on needs the section .dgx (--dgx on)")
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
                                    args.data_suffix, laa=args.laa == "on", preload=args.preload == "on",
                                    maptable=args.maptable == "on", rating=args.rating == "on",
                                    heaplocks=args.heap_locks == "on", numfmt=args.number_format == "on",
                                    averages=args.averages == "on", fade=args.fade == "on", dgx=args.dgx == "on",
                                    draggrab=args.drag_grab != "off", profilereload=args.profile_reload == "on")
    except PatchError as e:
        sys.exit(str(e))
    with open(exe, "wb") as f:
        f.write(data)
    profile = "" if args.profile == "local" else f" (profile {args.profile})"
    print(f"patched {exe}{profile}: OTG3 CA modulus -> {args.modulus}, EA-name restriction cleared; {summary}")


if __name__ == "__main__":
    main(sys.argv)
