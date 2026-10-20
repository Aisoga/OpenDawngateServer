"""Checks re/scripts/bot_profiles.py (the bot profiles archive, game/Data/zz_BotProfiles.snap).

What it checks:
  - the match host's table (gms/core/bots/profiles.h) lists the same profiles, in order, with the same base, role and
    AI donor (MIKELLABOT: the host looks up only the profiles it knows);
  - a made-up AssetData.snap (built here, nothing from the game; server/internal/setup/botprofiles's test builds the
    same bytes) and the archive made from it, by SHA-256: the Go port must write the same bytes;
  - the archive read back: the catalog keeps every entry of the copy's own and lists each profile after them, each
    profile's file is its base's with the role changed, or (a borrowed AI, HUNTERBOT) the base's noun on the donor's
    AI with the table's skill order; every BotClass file round-trips through the parser and writer; refusals (a base of another class, a name already listed, a cut
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
MADE_UP_SHA = "077b45f323ca19dc1bb074c1c98b4854c1e91a88a8a8c96247046b2e10a7c100"
MADE_UP_ARCHIVE = "7bd9e97f1097388a9501a76bcd8d7045d1c17c6a7c9ba76d057dd29c0b002fb7"
GAME_854 = "933a9d5732368d53d6597dec2e54ca777f2252056533feb9a0d5856b42fe02fe"  # MOYABOT after MIKELLABOT: 6 profiles

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
    cat = struct.pack("<II", 0x02000000, 10)
    cat += ent(0x02000010, bp.BOTCLASS_TYPECRC, 0x02000020, 0) + ent(0x02000040, 0x12345678, 0x02000050, 2)
    # HUNTERBOT: the Hunter AI's donor and two shapers without an AI; DESECBOT: a Tactician pair; MIKELLABOT: a
    # Gladiator pair; MOYABOT: a fourth shaper without an AI
    for i in range(8):
        cat += ent(0x020000A0 + 0x20 * i, bp.BOTCLASS_TYPECRC, 0x020000B0 + 0x20 * i, 0)
    cat += b"PC_Warden.BotClass\0Heroes/warden/PC_Warden.Noun\0lantern.Noun\0Props/lantern.Noun\0"
    cat += struct.pack("<II", 0x02000060, 0x02000070) + b"placeable\0npc\0"
    cat += (b"PC_Artificer.BotClass\0Heroes/spark/PC_Spark.Noun\0PC_Golem.BotClass\0Heroes/stone/PC_Stone.Noun\0"
            b"PC_Survivor.BotClass\0Heroes/thorn/PC_Thorn.Noun\0PC_Guardian.BotClass\0Heroes/rock/PC_Rock.Noun\0"
            b"PC_Desecrator.BotClass\0Heroes/bone/PC_Bone.Noun\0"
            b"PC_HighHuntress.BotClass\0Heroes/blade/PC_Blade.Noun\0"
            b"PC_MerchantPrincess.BotClass\0Heroes/coin/PC_Coin.Noun\0"
            b"PC_Otter.BotClass\0Heroes/reed/PC_Reed.Noun\0")
    bc = bytearray(bp.BOTCLASS_SIZE)
    struct.pack_into("<III", bc, 0, 0x02000080, 0x02000090, 1)
    bc += b"ember.Noun\0BOT_ember_Tactician.AIDefinition\0"
    hunter = struct.pack("<7I", 0x02000100, 0x02000110, 2, 0x02000120, 18, 0x02000130, 3)
    hunter += b"spark.Noun\0BOT_spark_Hunter.AIDefinition\0"
    hunter += struct.pack("<18I", *([5, 4, 3] + [5] * 2 + [6] + [4] * 12))
    hunter += struct.pack("<3I", 0x02000140, 0x02000150, 0x02000160) + b"Spell_A.Item\0Spell_B.Item\0Spell_C.Item\0"
    stone = struct.pack("<7I", 0x02000170, 0, 1, 0, 0, 0, 0) + b"stone.Noun\0"
    thorn = struct.pack("<7I", 0x02000180, 0, 1, 0, 0, 0, 0) + b"thorn.Noun\0"
    rock = struct.pack("<7I", 0x02000190, 0x020001A0, 1, 0x020001B0, 18, 0x020001C0, 3)
    rock += b"rock.Noun\0BOT_rock_Tactician.AIDefinition\0"
    rock += struct.pack("<18I", *([5, 3, 4] + [5] * 2 + [6] + [3] * 12))
    rock += struct.pack("<3I", 0x020001D0, 0x020001E0, 0x020001F0) + b"Spell_A.Item\0Spell_D.Item\0Spell_E.Item\0"
    bone = struct.pack("<7I", 0x02000200, 0, 1, 0, 0, 0, 0) + b"bone.Noun\0"
    blade = struct.pack("<7I", 0x02000210, 0x02000220, 0, 0x02000230, 18, 0x02000240, 3)
    blade += b"blade.Noun\0BOT_blade_Gladiator.AIDefinition\0"
    blade += struct.pack("<18I", *([3, 5, 4] + [3] * 2 + [6] + [5] * 12))
    blade += struct.pack("<3I", 0x02000250, 0x02000260, 0x02000270) + b"Spell_A.Item\0Spell_F.Item\0Spell_G.Item\0"
    coin = struct.pack("<7I", 0x02000280, 0, 1, 0, 0, 0, 0) + b"coin.Noun\0"
    reed = struct.pack("<7I", 0x02000290, 0, 1, 0, 0, 0, 0) + b"reed.Noun\0"
    return snap.pack_bytes([(bp.CATALOG, cat), ("AssetData/Binary/PC_Warden.BotClass.bin", bytes(bc)),
                            ("AssetData/Binary/PC_Artificer.BotClass.bin", hunter),
                            ("AssetData/Binary/PC_Golem.BotClass.bin", stone),
                            ("AssetData/Binary/PC_Survivor.BotClass.bin", thorn),
                            ("AssetData/Binary/PC_Guardian.BotClass.bin", rock),
                            ("AssetData/Binary/PC_Desecrator.BotClass.bin", bone),
                            ("AssetData/Binary/PC_HighHuntress.BotClass.bin", blade),
                            ("AssetData/Binary/PC_MerchantPrincess.BotClass.bin", coin),
                            ("AssetData/Binary/PC_Otter.BotClass.bin", reed)])


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
    check(order == [bp.CATALOG] + [bp.BINARY.format(p.name + ".BotClass") for p in bp.PROFILES], f"{label}: order")
    old = bp.parse_catalog(src[bp.CATALOG])
    new = bp.parse_catalog(got[bp.CATALOG])
    check(len(new) == len(old) + len(bp.PROFILES), f"{label}: catalog count")
    check(all(a[:3] == b[:3] for a, b in zip(old, new)), f"{label}: the copy's entries unchanged")
    for i, p in enumerate(bp.PROFILES):
        name, base, role = p.name, p.base, p.role
        e = new[len(old) + i]
        base_entry = next(x for x in old if x[1] == base + ".BotClass")
        check(e[1] == name + ".BotClass" and struct.unpack_from("<I", e[0], 0x14)[0] == bp.BOTCLASS_TYPECRC and
              e[0] == base_entry[0] and e[2] == base_entry[2], f"{label}: {name}'s entry")
        b0 = src[bp.BINARY.format(base + ".BotClass")]
        b1 = got[bp.BINARY.format(name + ".BotClass")]
        if p.ai is None:
            check(len(b0) == len(b1) and b0[:8] == b1[:8] and b0[12:] == b1[12:] and
                  struct.unpack_from("<I", b1, 8)[0] == bp.ROLES[role], f"{label}: {name}'s file")
            continue
        # HUNTERBOT: the base's noun on the donor's AI, the role, the table's skill order, the donor's spells
        fb, fd, f1 = bp.parse_botclass(b0), bp.parse_botclass(src[bp.BINARY.format(p.ai + ".BotClass")]), \
            bp.parse_botclass(b1)
        check(f1["noun"] == fb["noun"] and f1["ai"] == fd["ai"] and f1["words"][2] == bp.ROLES[role] and
              f1["skills"] == list(p.skills or fd["skills"]) and f1["spells"] == list(p.spells or fd["spells"]),
              f"{label}: {name}'s file ({f1['noun']} on {f1['ai']})")
        check(len(f1["skills"]) == 18 and sorted(f1["skills"]).count(6) == 3 and
              all(sorted(f1["skills"]).count(s) == 5 for s in (3, 4, 5)), f"{label}: {name}'s skill order adds up")
    # every shipped-format BotClass file reads back byte for byte
    for n, data in src.items():
        if n.endswith(".BotClass.bin"):
            check(bp.write_botclass(bp.parse_botclass(data)) == data, f"{label}: {n} round-trips")


def check_host_table():
    """The match host's table (gms/core/bots/profiles.h Profiles()) lists PROFILES in the same order with the same base,
    role and AI donor; the host looks up only the classes it knows (composition.h CatalogCandidates, which takes every
    profile of that table; gms_tests checks that side), so a profile missing there never reaches a match."""
    import re
    path = os.path.join(ROOT, "gms", "core", "bots", "profiles.h")
    text = open(path, encoding="utf-8").read()
    body = text[text.index("kProfiles = {"):text.index("return kProfiles;")]
    rows = re.findall(r'\{\s*"([^"]+)"\s*,\s*"([^"]+)"\s*,\s*(\d+)\s*,\s*(nullptr|"[^"]+")\s*\}', body)
    host = [(n, b, int(r), None if a == "nullptr" else a.strip('"')) for n, b, r, a in rows]
    want = [(p.name, p.base, bp.ROLES[p.role], p.ai) for p in bp.PROFILES]
    check(host == want, f"profiles.h's table {host} is bot_profiles.py's {want}")


def main(argv):
    show = "--print" in argv
    check_host_table()
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
            for p in bp.PROFILES:
                fn = os.path.join(d, p.name + ".BotClass.bin")
                obj, bad = assetdata.parse_file(fn, types["botclass"], types, enums)
                check(obj.get("role") == p.role and not bad, f"build 854: {p.name} parses with role {p.role}")
                if p.ai:
                    want_ai = {"PC_Artificer": "BOT_Artificer_Hunter.AIDefinition",
                               "PC_Guardian": "BOT_Guardian_Tactician.AIDefinition",
                               "PC_HighHuntress": "BOT_HighHuntress_Gladiator.AIDefinition"}[p.ai]
                    want_noun = {"PC_MerchantPrincess": "merchant_princess.noun"}.get(p.base,
                                                                                      p.base[3:].lower() + ".noun")
                    check(obj.get("aiDefinition") == want_ai and obj.get("nounDef", "").lower() == want_noun,
                          f"build 854: {p.name} names its noun and its donor's AI ({obj.get('nounDef')}, "
                          f"{obj.get('aiDefinition')})")
                    if p.spells:  # DESECBOT: its own spells
                        check(list(obj.get("spellOrder") or []) == list(p.spells),
                              f"build 854: {p.name}'s spells {obj.get('spellOrder')}")
            cobj, _ = assetdata.parse_file(os.path.join(d, "catalog_153.bin"), types["catalog"], types, enums)
            names = [e["assetNameType"] for e in cobj["entries"]]
            check(names[-len(bp.PROFILES):] == [p.name + ".BotClass" for p in bp.PROFILES],
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
