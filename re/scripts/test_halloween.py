"""Checks re/scripts/halloween.py (the Halloween decoration archive, game/Data/zz_Halloween.snap).

What it checks:
  - a made-up AssetData.snap (built here, nothing from the game; server/internal/setup/halloween's test builds the same
    bytes) and the archive made from it, by SHA-256: the Go port must write the same bytes;
  - the archive read back: the level's list is the old one plus the holiday markerset, every other byte in place; the
    markerset's condition list is [0] and the rest unchanged; refusals (a level that lists it already, a markerset with
    a condition, a cut file, a missing file);
  - with the game files (client/ia_Dawngate, else skipped): the archive made from build 854's AssetData.snap, by
    SHA-256 (the Go test pins the same), parsed by assetdata.py (every byte, references): 30 markersets, the last the
    holiday one, its condition [0] and its 94 markers.

Usage:
    .venv/Scripts/python -I re/scripts/test_halloween.py [--print]
--print prints the SHA-256s instead of checking them (after a deliberate change to the format).
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
import halloween as hw  # noqa: E402
import snap  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(HERE))
MADE_UP_SHA = "97143ec0d0b2a032d2281868543b7be8b9f4c075c0384af1d90a445677a3acda"
MADE_UP_ARCHIVE = "154b03df45c699539c6e31ba66a4643829615209436c7eb20962464be43ccb6c"
GAME_854 = "c9cc70a55cda03144e49c2d2c84206bb9ad6ee81d2532f409d61fa2e5eda65fa"

failures = []


def check(cond, what):
    if not cond:
        failures.append(what)
        print("FAIL: " + what)


def made_up_level():
    """A Level: the fixed part (zero but the markersets array {0x02000000, 2} and a byte at +0x61C), two pointer words,
    two names, then a made-up string the rest of a level would hold."""
    fixed = bytearray(hw.LEVEL_SIZE)
    struct.pack_into("<II", fixed, 0, 0x02000000, 2)
    fixed[0x61C] = 1
    return (bytes(fixed) + struct.pack("<II", 0x02000010, 0x02000030)
            + b"Map_one.Markerset\0Map_two.Markerset\0music_of_the_map\0")


def made_up_markerset():
    """A Markerset: markers {0x02000100, 1} with one made-up 8-byte marker, the group Key "none" (its text pointer at
    +0x14), weight 1.0, no condition; then the marker and the group's text."""
    fixed = bytearray(hw.MARKERSET_SIZE)
    struct.pack_into("<II", fixed, 0x00, 0x02000100, 1)
    struct.pack_into("<IIII", fixed, 0x08, 0x2CA33BDB, 0, 0, 0x02000200)
    struct.pack_into("<f", fixed, 0x18, 1.0)
    return bytes(fixed) + b"MARKER01" + b"none\0"


def made_up_snap():
    return snap.pack_bytes([
        ("AssetData/Binary/" + hw.LEVEL + ".bin", made_up_level()),
        ("AssetData/Binary/other.Noun.bin", b"not a level\0"),
        ("AssetData/Binary/" + hw.MARKERSET + ".bin", made_up_markerset()),
    ])


def refuses(fn, *args):
    try:
        fn(*args)
    except hw.HalloweenError:
        return True
    return False


def check_archive(level_in, ms_in, archive):
    s = snap.Snap(archive)
    names = [e.name for e in s.entries]
    check(names == ["AssetData/Binary/" + hw.LEVEL + ".bin", "AssetData/Binary/" + hw.MARKERSET + ".bin"],
          f"the archive's entries: {names}")
    level = s.read(s.entries[0])
    ms = s.read(s.entries[1])
    old = hw.markerset_names(level_in)
    check(hw.markerset_names(level) == old + [hw.MARKERSET], "the level lists the old markersets plus the holiday one")
    count = len(old)
    words_end = hw.LEVEL_SIZE + 4 * count
    check(level[8:hw.LEVEL_SIZE] == level_in[8:hw.LEVEL_SIZE], "the level's fixed part is unchanged after the count")
    check(level[words_end:words_end + 4] == level_in[words_end - 4:words_end], "the new pointer word is the last one's")
    names_end = level_in.index(old[-1].encode() + b"\0", words_end - 4) + len(old[-1]) + 1
    check(level.endswith(level_in[names_end:]), "the level's data after the names is unchanged")
    check(len(level) == len(level_in) + 4 + len(hw.MARKERSET) + 1, "the level grew by a word and a name")
    check(ms[:0x20] == ms_in[:0x20] and ms[0x28:len(ms_in)] == ms_in[0x28:], "the markerset is unchanged but the condition")
    check(struct.unpack_from("<II", ms, 0x20) == (struct.unpack_from("<I", ms_in, 0)[0], 1), "the condition array {ptr, 1}")
    check(ms[len(ms_in):] == struct.pack("<I", hw.CONDITION), "the condition's value is appended")


def main(argv):
    printing = "--print" in argv
    with tempfile.TemporaryDirectory() as tmp:
        src = os.path.join(tmp, "AssetData.snap")
        data = made_up_snap()
        with open(src, "wb") as f:
            f.write(data)
        out = hw.build(src)
        if printing:
            print("MADE_UP_SHA =", hashlib.sha256(data).hexdigest())
            print("MADE_UP_ARCHIVE =", hashlib.sha256(out).hexdigest())
        else:
            check(hashlib.sha256(data).hexdigest() == MADE_UP_SHA, "the made-up AssetData.snap's SHA-256")
            check(hashlib.sha256(out).hexdigest() == MADE_UP_ARCHIVE, "the made-up archive's SHA-256")
        arc = os.path.join(tmp, hw.ARCHIVE)
        with open(arc, "wb") as f:
            f.write(out)
        check_archive(made_up_level(), made_up_markerset(), arc)
        twice = hw.add_markerset(made_up_level(), hw.MARKERSET)
        check(refuses(hw.add_markerset, twice, hw.MARKERSET), "refuses a level that lists the markerset already")
        check(refuses(hw.add_markerset, made_up_level()[:0x100], hw.MARKERSET), "refuses a cut level")
        check(refuses(hw.add_markerset, made_up_level()[:hw.LEVEL_SIZE + 8 + 5], hw.MARKERSET),
              "refuses a level whose names are cut")
        with_cond = hw.set_condition(made_up_markerset(), [0])
        check(refuses(hw.set_condition, with_cond, [0]), "refuses a markerset with a condition")
        check(refuses(hw.set_condition, made_up_markerset()[:0x20], [0]), "refuses a cut markerset")
        no_ms = os.path.join(tmp, "NoMarkerset.snap")
        with open(no_ms, "wb") as f:
            f.write(snap.pack_bytes([("AssetData/Binary/" + hw.LEVEL + ".bin", made_up_level())]))
        check(refuses(hw.build, no_ms), "refuses an AssetData.snap without the markerset")

    game, up = None, ROOT  # this checkout's client/ia_Dawngate, else a folder above it's (a worktree finds the main one)
    for _ in range(5):
        cand = os.path.join(up, "client", "ia_Dawngate", "Dawngate", "game", "Data", "AssetData.snap")
        if os.path.isfile(cand):
            game = cand
            break
        up = os.path.dirname(up)
    if game:
        out = hw.build(game)
        if printing:
            print("GAME_854 =", hashlib.sha256(out).hexdigest())
        else:
            check(hashlib.sha256(out).hexdigest() == GAME_854, "build 854: the archive's SHA-256")
        types, enums = assetdata.load_types(os.path.join(ROOT, "docs", "data", "assetdata_types.txt"))
        with tempfile.TemporaryDirectory() as tmp:
            s = snap.Snap(game)
            byname = {e.name.lower(): e for e in s.entries}
            level_in = s.read(byname[("AssetData/Binary/" + hw.LEVEL + ".bin").lower()])
            ms_in = s.read(byname[("AssetData/Binary/" + hw.MARKERSET + ".bin").lower()])
            arc = os.path.join(tmp, hw.ARCHIVE)
            with open(arc, "wb") as f:
                f.write(out)
            check_archive(level_in, ms_in, arc)
            a = snap.Snap(arc)
            for e in a.entries:
                fn = os.path.join(tmp, os.path.basename(e.name))
                with open(fn, "wb") as f:
                    f.write(a.read(e))
                cls = e.name.rsplit(".", 2)[-2].lower()
                obj, bad = assetdata.parse_file(fn, types[cls], types, enums)
                check(not bad, f"build 854: {e.name}'s references")
                if cls == "level":
                    ms = obj["markersets"]
                    check(len(ms) == 30 and ms[-1]["markersetAsset"] == hw.MARKERSET,
                          "build 854: 30 markersets, the holiday one last")
                else:
                    check(obj["condition"] == [0] and len(obj["markers"]) == 94 and obj["group"] == "none",
                          "build 854: the markerset's condition [0], 94 markers, group none")
    else:
        print("skip: no game files (client/ia_Dawngate)")
    if printing:
        return 0
    print("test_halloween: " + ("OK" if not failures else f"{len(failures)} failure(s)"))
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
