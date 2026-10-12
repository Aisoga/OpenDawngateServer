"""Set up (or refresh) the server-mode harness working copy for D1 (docs/SERVER_MODE.md).

The harness is gms/'s dgsim.dll. Dawngate.exe imports PhysXLoader.dll statically, so the harness
is installed in a separate working copy as PhysXLoader.dll and forwards PhysXLoader's ten exports
to the original, renamed PhysXLoader_orig.dll. No exe byte is changed beyond what patch_client.py
already did; the copy stays network-isolated like client/run_854 (it is a copy of it).

Steps:
  1. Refuses any path under client/ia_* (pristine builds) and a source that isn't patched
     (Dawngate.exe, Config.package, Config_Ship.package must differ from their .orig).
  2. Copies the source working copy to the destination if the destination doesn't exist
     (1.5 GB, timestamps kept). An existing destination is reused.
  3. Renames the destination's PhysXLoader.dll to PhysXLoader_orig.dll once, after checking that it
     is the game's PhysX loader (exports NxCreatePhysicsSDK, no dgsim marker), then copies dgsim.dll
     in as PhysXLoader.dll.
  4. Writes game/dgsim.ini with the harness defaults (and D1d's [host] section, since H1b with core_only=1: the
     headless host is the default) unless it exists (--ini replaces it; a missing [host] key takes the same default
     in the DLL, so an older dgsim.ini without core_only gets the headless host too; core_only=0 is stage 1), and
     copies the Lua probes gms/engine/lua/*.lua into game/ (always, so they match the repo).
  --remove puts the original PhysXLoader.dll back and deletes dgsim.ini (the copy stays).
  --laa on|off (H1-live, step 4) sets or clears IMAGE_FILE_LARGE_ADDRESS_AWARE in the host copy's game/Dawngate.exe
     (PE characteristics 0x0103 <-> 0x0123), so the host gets 4 GB of address space on 64-bit Windows instead of 2.
     Host copies only: it refuses an exe without PhysXLoader_orig.dll beside it (not a host copy), one whose .orig
     doesn't have the build's 0x0103, and any characteristics other than 0x0103/0x0123. One header bit, reversible;
     patch_client.py starts from the .orig, so after re-patching the exe run --laa on again. Without a dll argument
     only the bit changes. The DLL's [host] laa_reserve tests it (docs/SERVER_MODE.md *Headless host, stage 2*).

Usage:
  python -I re/scripts/setup_server_copy.py client/run_854/Dawngate client/run_854_server/Dawngate \
      gms/build/Release/dgsim.dll [--ini <file>] [--remove] [--laa on|off]
  python -I re/scripts/setup_server_copy.py client/run_854_c/Dawngate client/run_854_server_c/Dawngate --laa on
Then run a session on it:
  powershell -ExecutionPolicy Bypass -File re/scripts/run_session.ps1 -Name d1_runN -GameDir client\\run_854_server\\Dawngate
"""
import argparse
import hashlib
import os
import shutil
import sys

DEFAULT_INI = """[dgsim]
; D1 server-mode harness settings (gms/engine/experiment.cpp). Times in ms after the first
; main-thread frame; stop_after: 1 = create the simulator, 2 = also start the level, 3 = also tick.
start_ms=20000
stop_after=3
level=SM_Map_v5.Level
; markerset conditions: bit 0 hordeLevelsOnly, bit 1 bossLevelsOnly (Darkspore); 0 loads neither
flags=0
seed=12345
tick_ms=33
run_ms=60000
set_running=1
; sim+0x550, component storage mode: 1 registers the server component pools (the client uses 0)
component_mode=1
; activate the AI director before the level loads (the client never does; GameWorld.cpp:369 asserts it)
director_active=1
stats_ms=2000
histo_ms=10000
; dgsim_probe.lua interval while ticking
probe_ms=5000
; D1b-live: 1 = the agent step each tick (gms/engine/agents.h; host mode: [host] agents overrides it)
tick_agents=1
; 1 = activate the tower attack prepared by dgsim_start.lua directly (0x0090A390) instead of requesting it
direct_ability=0
; D1a-live: register prepared ability/modifier Tick threads with the Lua scheduler and end instances
; whose Tick finished (gms/engine/abilities.cpp); 0 = the binary's own behaviour (Ticks never run)
ability_tick=1
; 1 = also end agent abilities without a Tick right after Activate (they would hold the agent forever)
end_no_tick=1
; ability events logged one by one before only every 100th is logged
ability_log_limit=400
; D1e: 1 = end modifiers whose OnDuration time ran out (start + duration on the simulator clock)
modifier_expiry=1
; D1f-live: 1 = the TakeDamage core the client lacks (gms/engine/damage.cpp, docs/DAMAGE.md steps 1-15); 0 = the
; client's stub (no damage at all). D1a-live's key placeholder_damage is still read as its default.
; damage_absorb / _reductions / _lifedrain / _overload / _shields switch single steps off (all 1).
damage=1
damage_log_limit=200
; D1f-live: death processing (gms/engine/death.cpp): OnNearbyDeath/OnKill/player events, rewards, the OnGrantGold
; stub replaced, the OnAgentDeath ends death_end_runs Lua scheduler runs after a death; starting and passive gold.
; death_nearby / _rewards / _gold / _kill_hook / _interrupt, gold_start, gold_passive (all 1). In host mode every one
; of these [dgsim] keys can also be overridden with DGGMS_OPTIONS (run_session.ps1 -HostOptions).
death=1
death_end_runs=3
death_log_limit=300
; D1g: a dead player's death timer (mDeathTime/mRespawnTime: LevelGameplayTuning 15 + 0.5 x level + 1.25 x match
; minutes) and the respawn at its start point when it runs out (teleport + resurrect of the same creature).
; respawn_s > 0 replaces the formula with a fixed wait (tests).
respawn=1
respawn_s=0
log_all_asserts=0
; narrow the client's wildcard socket binds (UDP 3659, RakNet) to 127.0.0.1
loopback_bind=1

[host]
; D1d host mode (ADR-2 stage 1): used only when the process has DGGMS_* variables (run_session.ps1 -HostDir,
; dgserver); see gms/host/host.h. start_ms after the first frame; run_ms 0 = tick until the process ends.
start_ms=0
run_ms=0
; 1 = keep the front end in kGameStateBoot (no login, Blaze or lobby)
hold_boot=1
; 1 = Dawngate.exe's socket(), WSASocketA() and gethostbyname() fail (dgsim.dll's own sockets are not affected)
block_sockets=1
; 1 = top-level windows hidden, no activation, no low-level input hooks, message boxes answered unseen
hide_window=1
; 1 = per-user folders (AppData, Documents, ...) below the host's data folder (DGGMS_DATA)
redirect_folders=1
; 1 = also run the Lua probes dgsim_start.lua / dgsim_probe.lua (test minions, a scripted tower attack) in host mode
lua_probes=0
; G2, the host's GMS (gms/engine/gms_host.h; bound to DGGMS_BIND, default 127.0.0.1:10042, "off" = none):
; inject_file = messages to send at session points until D2f (gms_script's --inject-file format), e.g. D2e's
; re\\out\\d2e\\world_shaper_loaded.txt; empty = none
inject_file=
; the session's script A: skip shaper select (status 0x10) and echo the status; delay before each timed step
skip_shaper=1
echo_status=1
step_ms=1000
load_timeout_ms=60000
; GamePrepareForStart's flags (2 = connect type 3) and the player's slot: team 1 (blue), shaper alpha.Noun
prepare_flags=2
team=1
noun=0xEF044AAB
; C1b, the control channel (only when dgserver starts the host: DGGMS_CONTROL; gms/host/control.h): exit when
; createGame hasn't come this many seconds after the hello; after finishGame, stay until every GMS connection has
; ended or this many seconds have passed (then logoutGMS and exit)
create_timeout_s=60
postgame_cap_s=105
; H1-live, the headless host (stage 2; gms/host/headless.h, gms/engine/startup.h; docs/SERVER_MODE.md *Headless host,
; stage 2 (H1-live)*, *The headless host as the default (H1b)*): core_only = 1 (the default since H1b) skips the
; renderer, audio, UI and the client game at start-up (about 100 MB instead of 1 GB); 0 = stage 1, the full client
; start-up (the way back). With core_only = 0 the stage-1 fallbacks apply: nobake / nosound = 1 answer the shipped
; switches -nobake (no portrait bake) and -noSound (no Wwise); preload = 0 (no directory of script/preload.txt), server
; (only assetdata/binary and levels) or 1 (the game's own). Test aids: startup_marks = 1 logs a mark before and after
; about 20 start-up calls; laa_reserve = 1 reserves the free address space below 2 GB after the level load (with
; setup_server_copy.py --laa on: what the host allocates later lands above 2 GB), laa_reserve_margin_mb (32) stays free
core_only=1
startup_marks=0
nobake=0
nosound=0
preload=1
laa_reserve=0
; H1d: d3d9 = 0 (the default) starts a core-only
; host without any Direct3D 9 call (RwDevice_Initialize answered by the host), for machines without a GPU; 1 = the
; game's own start-up with a Direct3D 9 object, as before H1d. Test aid: test_no_d3d9 = 1 makes Direct3DCreate9 answer
; NULL, as on a machine without Direct3D 9.
d3d9=0
test_no_d3d9=0
; Every [host] key can be overridden for one run with DGGMS_OPTIONS="key=value;key=value" (run_session.ps1 -HostOptions).
"""


def sha(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def refuse_pristine(path):
    norm = os.path.abspath(path).replace("/", "\\").lower()
    if "\\client\\ia_" in norm:
        sys.exit(f"refusing to touch a pristine extracted build: {path}")


def check_patched(game_dir):
    for rel in ("game/Dawngate.exe", "game/Data/Config.package", "game/Data/Config_Ship.package"):
        live = os.path.join(game_dir, rel)
        if not os.path.exists(live) or not os.path.exists(live + ".orig"):
            sys.exit(f"{live} or its .orig is missing; run patch_client.py and patch_config.py first")
        if sha(live) == sha(live + ".orig"):
            sys.exit(f"{live} is unpatched; run patch_client.py / patch_config.py first")


def is_dgsim(path):
    with open(path, "rb") as f:
        return b"dgsim.dll loaded" in f.read()


LAA = 0x0020          # IMAGE_FILE_LARGE_ADDRESS_AWARE
CHAR_BUILD = 0x0103   # build 854's characteristics: relocations stripped, executable, 32-bit machine


def pe_characteristics_offset(data):
    """The file offset of IMAGE_FILE_HEADER.Characteristics of an x86 PE (checked), else None."""
    if data[:2] != b"MZ":
        return None
    pe = int.from_bytes(data[0x3C:0x40], "little")
    if data[pe:pe + 4] != b"PE\0\0" or int.from_bytes(data[pe + 4:pe + 6], "little") != 0x14C:
        return None
    return pe + 4 + 18


def set_laa(game, on):
    """H1-live step 4: LARGE_ADDRESS_AWARE on the host copy's exe (see the docstring)."""
    exe = os.path.join(game, "Dawngate.exe")
    refuse_pristine(exe)
    if not os.path.exists(os.path.join(game, "PhysXLoader_orig.dll")):
        sys.exit(f"{game} is not a host copy (no PhysXLoader_orig.dll); --laa is for host copies only")
    with open(exe + ".orig", "rb") as f:
        orig = f.read(0x1000)
    off = pe_characteristics_offset(orig)
    if off is None or int.from_bytes(orig[off:off + 2], "little") != CHAR_BUILD:
        sys.exit(f"{exe}.orig is not build 854's exe (characteristics); --laa refused")
    with open(exe, "r+b") as f:
        head = f.read(0x1000)
        if pe_characteristics_offset(head) != off:
            sys.exit(f"{exe}: its PE header is not where the .orig has it; --laa refused")
        cur = int.from_bytes(head[off:off + 2], "little")
        if cur not in (CHAR_BUILD, CHAR_BUILD | LAA):
            sys.exit(f"{exe}: characteristics {cur:#06x}, expected {CHAR_BUILD:#06x} or {CHAR_BUILD | LAA:#06x}; refused")
        want = CHAR_BUILD | LAA if on else CHAR_BUILD
        if cur != want:
            f.seek(off)
            f.write(want.to_bytes(2, "little"))
    print(f"{exe}: characteristics {cur:#06x} -> {want:#06x} (LARGE_ADDRESS_AWARE {'on' if on else 'off'})")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("source", help="patched working copy, e.g. client/run_854/Dawngate")
    ap.add_argument("dest", help="harness copy, e.g. client/run_854_server/Dawngate")
    ap.add_argument("dll", nargs="?", help="gms/build/Release/dgsim.dll")
    ap.add_argument("--ini", help="replace game/dgsim.ini with this file")
    ap.add_argument("--remove", action="store_true", help="restore the original PhysXLoader.dll")
    ap.add_argument("--laa", choices=("on", "off"), help="LARGE_ADDRESS_AWARE on the host copy's exe (H1-live)")
    a = ap.parse_args()
    for p in (a.source, a.dest):
        refuse_pristine(p)
    game = os.path.join(a.dest, "game")
    loader, orig, ini = (os.path.join(game, n) for n in ("PhysXLoader.dll", "PhysXLoader_orig.dll", "dgsim.ini"))

    if a.remove:
        if os.path.exists(orig):
            os.replace(orig, loader)
            print(f"restored {loader}")
        for name in os.listdir(game):
            if name == "dgsim.ini" or (name.startswith("dgsim_") and name.endswith(".lua")):
                os.remove(os.path.join(game, name))
        return 0

    if a.laa and not a.dll:  # only the header bit (H1-live)
        set_laa(game, a.laa == "on")
        return 0
    if not a.dll or not os.path.exists(a.dll):
        sys.exit("dgsim.dll missing; build it: cmake -S gms -B gms/build -A Win32 && cmake --build gms/build --config Release")
    check_patched(a.source)
    if not os.path.exists(a.dest):
        print(f"copying {a.source} -> {a.dest} (about 1.5 GB) ...")
        shutil.copytree(a.source, a.dest, copy_function=shutil.copy2)
    check_patched(a.dest)

    if not os.path.exists(orig):
        if is_dgsim(loader):
            sys.exit(f"{loader} is already dgsim but {orig} is missing; restore the original by hand")
        with open(loader, "rb") as f:
            if b"NxCreatePhysicsSDK" not in f.read():
                sys.exit(f"{loader} doesn't look like PhysX's loader")
        os.replace(loader, orig)
        print(f"renamed the original loader to {orig}")
    shutil.copy2(a.dll, loader)
    print(f"installed {a.dll} as {loader} (sha256 {sha(loader)[:16]})")

    lua_dir = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "gms", "engine", "lua")
    for name in sorted(os.listdir(lua_dir)):
        if name.endswith(".lua"):
            shutil.copyfile(os.path.join(lua_dir, name), os.path.join(game, name))
            print(f"copied {name}")
    if a.ini:
        shutil.copyfile(a.ini, ini)
        print(f"dgsim.ini <- {a.ini}")
    elif not os.path.exists(ini):
        with open(ini, "w", encoding="ascii") as f:
            f.write(DEFAULT_INI)
        print(f"wrote default {ini}")
    if a.laa:
        set_laa(game, a.laa == "on")
    return 0


if __name__ == "__main__":
    sys.exit(main())
