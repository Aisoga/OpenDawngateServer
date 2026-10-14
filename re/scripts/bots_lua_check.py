"""Offline check of the bots' Lua shims (D6b-shims; docs/SERVER_LUA_API.md tier 3 *D6b-shims*).

Checks gms/engine/lua/dgsim_bots_api.lua (and its probe and tests) without the game:
  1. compiles every file with LuaJ 2.0.3's port of the Lua 5.1 compiler (re/scripts/LuajCompileBatch.java), the same
     tooling that verifies the decompiled server scripts (docs/SERVERDATA_LUA.md);
  2. the 4-byte float lua_Number: flags integer literals of 2^24 or more (they wouldn't survive the client's float);
  3. runs gms/engine/lua/tests/test_dgsim_bots_api.lua (the shims against tests/stub_bound_api.lua) and expects
     "N checks, 0 failed";
  4. with --callers <dir> (the decompiled scripts, re/out/serverdata_lua/lua: game content, never committed) also runs
     tests/callers_smoke.lua: every live script that calls nBot or a bot-only nBattlegrounds getter, loaded with the
     real GlobalDefinitions/Global/Table/BotUtils and called against the shims in a stub world. Its per-script lines are
     printed; script bugs the docs list are expected there.

Usage (from the repo root; tools/ is gitignored, so in a worktree pass the main checkout's jars or let the script find
them through the git common dir):
    python -I re/scripts/bots_lua_check.py [--callers re/out/serverdata_lua/lua] [--java <java.exe>] [--luaj <jar>]
Exit code 0 when 1-3 pass (4 is informational).
"""
import argparse
import glob
import os
import re
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.normpath(os.path.join(HERE, "..", ".."))
LUA_DIR = os.path.join(REPO, "gms", "engine", "lua")
TESTS = os.path.join(LUA_DIR, "tests")
FILES = [
    os.path.join(LUA_DIR, "dgsim_bots_api.lua"),
    os.path.join(LUA_DIR, "dgsim_bots_report.lua"),
    os.path.join(LUA_DIR, "dgsim_bots_kills.lua"),  # D6a's probes
    os.path.join(LUA_DIR, "dgsim_bots_watch.lua"),
    os.path.join(LUA_DIR, "dgsim_bots_fire.lua"),  # D6b's fire-tracker probe
    os.path.join(LUA_DIR, "dgsim_d6c_trace.lua"),  # D6c's trace and fire probes
    os.path.join(LUA_DIR, "dgsim_d6c_fire.lua"),
    os.path.join(LUA_DIR, "dgsim_d6c_group.lua"),
    os.path.join(TESTS, "stub_bound_api.lua"),
    os.path.join(TESTS, "test_dgsim_bots_api.lua"),
    os.path.join(TESTS, "callers_smoke.lua"),
]
# the tier-3 functions whose callers the smoke test runs (docs/data/server_lua_missing_api.csv)
TIER3_GETTERS = {"GetNumTowers", "GetOuterTower", "GetVulnerableStructures", "GetClosestBaseStructure",
                 "GetMinesForTeam", "GetFollowersForTitan", "IsTitanNodeLocked"}
KINDS = {"Conditions": "condition", "Abilities": "ability", "Behaviors": "behavior", "Lua": "lua"}


def main_checkout():
    """The main checkout's root (a worktree's tools/ is missing: it is gitignored)."""
    try:
        common = subprocess.run(["git", "-C", REPO, "rev-parse", "--git-common-dir"], capture_output=True, text=True,
                                check=True).stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return REPO
    if not os.path.isabs(common):
        common = os.path.join(REPO, common)
    return os.path.dirname(os.path.normpath(common))


def find_tool(pattern):
    for root in (REPO, main_checkout()):
        hits = sorted(glob.glob(os.path.join(root, pattern)))
        if hits:
            return hits[-1]
    return None


def compile_all(java, luaj, files):
    """LuaJ compile of each file; returns the list of failures."""
    with tempfile.TemporaryDirectory() as tmp:
        lst = os.path.join(tmp, "list.tsv")
        with open(lst, "w", encoding="utf-8") as f:
            for i, src in enumerate(files):
                f.write(f"{src}\t{os.path.join(tmp, f'{i}.luac')}\n")
        out = subprocess.run([java, "-cp", luaj, os.path.join(HERE, "LuajCompileBatch.java"), lst],
                             capture_output=True, text=True)
    errors = [line for line in out.stdout.splitlines() if line.startswith("ERR")]
    if out.returncode != 0 and not errors:
        errors.append("LuajCompileBatch failed: " + out.stderr.strip()[:400])
    return errors


NUMBER = re.compile(r"(?<![\w.])(\d+)(?![\w.])")


def float_check(path):
    """Integer literals the client's 4-byte float can't hold exactly (comments and strings ignored roughly)."""
    bad = []
    with open(path, encoding="utf-8") as f:
        for n, line in enumerate(f, 1):
            code = line.split("--", 1)[0]
            code = re.sub(r"'[^']*'|\"[^\"]*\"", "", code)
            for m in NUMBER.finditer(code):
                if int(m.group(1)) >= 1 << 24:
                    bad.append(f"{os.path.relpath(path, REPO)}:{n}: {m.group(1)}")
    return bad


def run_lua(java, luaj, args, timeout=300):
    try:
        out = subprocess.run([java, "-cp", luaj, "lua"] + args, capture_output=True, text=True, cwd=REPO,
                             timeout=timeout)
    except subprocess.TimeoutExpired as e:
        return (e.stdout or "") + f"\nTIMEOUT after {timeout} s"
    return out.stdout + out.stderr


def caller_list(scripts_dir, path):
    """The live callers of the tier-3 functions (the CSV's live_scripts) plus every script that mentions nBot."""
    names = set()
    csv = os.path.join(REPO, "docs", "data", "server_lua_missing_api.csv")
    with open(csv, encoding="utf-8") as f:
        header = f.readline().strip().split(",")
        for line in f:
            row = dict(zip(header, line.rstrip("\n").split(",")))
            if row["namespace"] == "nBot" or (row["namespace"] == "nBattlegrounds" and row["function"] in TIER3_GETTERS):
                names.update(n for n in row["live_scripts"].split(";") if n)
    rows = []
    for folder, kind in KINDS.items():
        for p in sorted(glob.glob(os.path.join(scripts_dir, folder, "*.lua"))):
            name = os.path.splitext(os.path.basename(p))[0]
            if name in names:
                rows.append(f"{kind} {folder}/{os.path.basename(p)}")
    with open(path, "w", encoding="utf-8") as f:
        f.write("\n".join(rows) + "\n")
    return len(rows)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--java", default=find_tool("tools/jdk-*/bin/java.exe") or "java")
    ap.add_argument("--luaj", default=find_tool("tools/luaj/luaj-jse-*.jar"))
    ap.add_argument("--callers", help="the decompiled scripts (re/out/serverdata_lua/lua) for the caller smoke test")
    args = ap.parse_args()
    if not args.luaj:
        sys.exit("LuaJ not found (tools/luaj/luaj-jse-2.0.3.jar, docs/development/building.md); pass --luaj")
    ok = True

    errors = compile_all(args.java, args.luaj, FILES)
    print(f"compile: {len(FILES) - len(errors)} of {len(FILES)} files compile (LuaJ {os.path.basename(args.luaj)})")
    for e in errors:
        print("  " + e)
    ok &= not errors

    bad = [b for p in FILES[:2] for b in float_check(p)]
    print(f"float: {len(bad)} integer literal(s) >= 2^24 in the shims")
    for b in bad:
        print("  " + b)
    ok &= not bad

    out = run_lua(args.java, args.luaj, [os.path.join(TESTS, "test_dgsim_bots_api.lua"), TESTS, FILES[0]])
    m = re.search(r"(\d+) checks, (\d+) failed", out)
    passed = bool(m) and m.group(2) == "0"
    print(f"tests: {m.group(0) if m else 'no result'}")
    if not passed:
        print(out.strip())
    ok &= passed

    if args.callers:
        with tempfile.TemporaryDirectory() as tmp:
            lst = os.path.join(tmp, "callers.txt")
            n = caller_list(os.path.abspath(args.callers), lst)
            print(f"callers: {n} live scripts")
            out = run_lua(args.java, args.luaj, [os.path.join(TESTS, "callers_smoke.lua"), TESTS, FILES[0],
                                                 os.path.abspath(args.callers), lst])
        print(out.rstrip())

    print("result: " + ("ok" if ok else "FAILED"))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
