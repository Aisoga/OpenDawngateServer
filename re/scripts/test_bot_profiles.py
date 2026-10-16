"""Checks re/scripts/bot_profiles.py (the bot profiles archive, game/Data/zz_BotProfiles.snap).

What it checks:
  - a made-up AssetData.snap (built here, nothing from the game; server/internal/setup/botprofiles's test builds the
    same bytes) and the archive made from it, by SHA-256: the Go port must write the same bytes;
  - the archive read back: the catalog keeps every entry of the copy's own and lists each profile after them, each
    profile's file is its base's with the role changed; refusals (a base of another class, a name already listed, a cut
    catalog, a byte too many);
  - with the game files (client/ia_Dawngate, else skipped): the archive made from build 854's AssetData.snap, by
    SHA-256 (the Go test pins the same), parsed by assetdata.py (every byte, references, the typeCrc check).

Usage:
    .venv/Scripts/python -I re/scripts/test_bot_profiles.py [--print]
--print prints the SHA-256s instead of checking them (after a deliberate change to the profiles or the format).
Exit code 0 when every check passes.
"""
import hashlib
import os
import struct
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import assetdata  # noqa: E402
import bot_profiles as bp  # noqa: E402
import snap  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(HERE))
MADE_UP_SHA = "7baaa80e0bb296ba2d54c5dfb8c9277059d0cce7e94e340bd94417cb80432fea"
MADE_UP_ARCHIVE = "41066970b54e20a03c6cb57e2feacf44fdf399f5ed4cbadcba2af26e842b754b"
GAME_854 = "b0eff5ba01d1ace3df4839050c71b243f0ff711bd3b98d6bed9737b752a7d515"

failures = []


def check(cond, what):
    if not cond:
        failures.append(what)
        print("FAIL " + what)


def made_up():
    """The made-up AssetData.snap (the Go test's MadeUp)."""
    def ent(name, type_crc, source, tags):
        e = bytearray(bp.ENTRY_SIZE)
        struct.pack_into("<I", e, 0x00, name)
        struct.pack_into("<Q", e, 0x08, 1415127097)
        struct.pack_into("<I", e, 0x14, type_crc)
        struct.pack_into("<I", e, 0x1C, source)
        e[0x20], e[0x21] = 4, 4
        if tags:
            struct.pack_into("<II", e, 0x24, 0x02000030, tags)
        return bytes(e)
    cat = struct.pack("<II", 0x02000000, 2)
    cat += ent(0x02000010, bp.BOTCLASS_TYPECRC, 0x02000020, 0) + ent(0x02000040, 0x12345678, 0x02000050, 2)
    cat += b"PC_Warden.BotClass\0Heroes/warden/PC_Warden.Noun\0lantern.Noun\0Props/lantern.Noun\0"
    cat += struct.pack("<II", 0x02000060, 0x02000070) + b"placeable\0npc\0"
    bc = bytearray(bp.BOTCLASS_SIZE)
    struct.pack_into("<III", bc, 0, 0x02000080, 0x02000090, 1)
    bc += b"ember.Noun\0BOT_ember_Tactician.AIDefinition\0"
    return snap.pack_bytes([(bp.CATALOG, cat), ("AssetData/Binary/PC_Warden.BotClass.bin", bytes(bc))])


def read_archive(data):
    with tempfile.TemporaryDirectory() as d:
        p = os.path.join(d, "a.snap")
        with open(p, "wb") as f:
            f.write(data)
        s = snap.Snap(p)
        return {e.name: s.read(e) for e in s.entries}, [e.name for e in s.entries]


def check_archive(label, src_bytes, out):
    src, _ = read_archive(src_bytes)
    got, order = read_archive(out)
    check(order == [bp.CATALOG] + [bp.BINARY.format(n + ".BotClass") for n, _, _ in bp.PROFILES], f"{label}: order")
    old = bp.parse_catalog(src[bp.CATALOG])
    new = bp.parse_catalog(got[bp.CATALOG])
    check(len(new) == len(old) + len(bp.PROFILES), f"{label}: catalog count")
    check(all(a[:3] == b[:3] for a, b in zip(old, new)), f"{label}: the copy's entries unchanged")
    for i, (name, base, role) in enumerate(bp.PROFILES):
        e = new[len(old) + i]
        check(e[1] == name + ".BotClass" and struct.unpack_from("<I", e[0], 0x14)[0] == bp.BOTCLASS_TYPECRC,
              f"{label}: {name}'s entry")
        b0 = src[bp.BINARY.format(base + ".BotClass")]
        b1 = got[bp.BINARY.format(name + ".BotClass")]
        check(len(b0) == len(b1) and b0[:8] == b1[:8] and b0[12:] == b1[12:] and
              struct.unpack_from("<I", b1, 8)[0] == bp.ROLES[role], f"{label}: {name}'s file")


def main(argv):
    show = "--print" in argv
    src = made_up()
    with tempfile.TemporaryDirectory() as d:
        p = os.path.join(d, "AssetData.snap")
        with open(p, "wb") as f:
            f.write(src)
        out = bp.build(p)
        sums = {"made-up AssetData.snap": (hashlib.sha256(src).hexdigest(), MADE_UP_SHA),
                "its archive": (hashlib.sha256(out).hexdigest(), MADE_UP_ARCHIVE)}
        check_archive("made-up", src, out)
        cat = read_archive(src)[0][bp.CATALOG]
        for adds, what in (([("x.BotClass", "lantern.Noun")], "a base of another class"),
                           ([("PC_Warden.BotClass", "PC_Warden.BotClass")], "a name already listed"),
                           ([("x.BotClass", "missing.BotClass")], "a missing base")):
            try:
                bp.build_catalog(cat, adds)
                check(False, f"refuses {what}")
            except bp.ProfileError:
                pass
        for bad, what in ((cat[:-1], "a cut catalog"), (cat + b"\0", "a byte too many")):
            try:
                bp.parse_catalog(bad)
                check(False, f"refuses {what}")
            except bp.ProfileError:
                pass
    game, up = None, ROOT  # this checkout's client/ia_Dawngate, else a folder above it's (a worktree finds the main one)
    while game is None:
        cand = os.path.join(up, "client", "ia_Dawngate", "Dawngate", "game", "Data", "AssetData.snap")
        game = cand if os.path.isfile(cand) else (None if os.path.dirname(up) != up else "")
        up = os.path.dirname(up)
    if game:
        out = bp.build(game)
        sums["build 854's archive"] = (hashlib.sha256(out).hexdigest(), GAME_854)
        check_archive("build 854", open(game, "rb").read(), out)
        types, enums = assetdata.load_types(os.path.join(ROOT, "docs", "data", "assetdata_types.txt"))
        got, _ = read_archive(out)
        with tempfile.TemporaryDirectory() as d:
            for name, data in got.items():
                with open(os.path.join(d, os.path.basename(name)), "wb") as f:
                    f.write(data)
            for name, _, role in bp.PROFILES:
                fn = os.path.join(d, name + ".BotClass.bin")
                obj, bad = assetdata.parse_file(fn, types["botclass"], types, enums)
                check(obj.get("role") == role and not bad, f"build 854: {name} parses with role {role}")
            cobj, _ = assetdata.parse_file(os.path.join(d, "catalog_153.bin"), types["catalog"], types, enums)
            names = [e["assetNameType"] for e in cobj["entries"]]
            check(names[-len(bp.PROFILES):] == [n + ".BotClass" for n, _, _ in bp.PROFILES],
                  "build 854: assetdata.py reads the catalog's new entries")
    else:
        print("skip: no game files (client/ia_Dawngate)")
    for what, (got, want) in sums.items():
        if show:
            print(f"{what}: {got}")
        else:
            check(got == want, f"{what}: SHA-256 {got}, pinned {want}")
    print(f"{'FAILED' if failures else 'ok'}: {len(failures)} failure(s)")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
