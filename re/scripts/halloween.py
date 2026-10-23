"""Add the Halloween decoration switch to a working copy: game/Data/zz_Halloween.snap.

The game ships a Halloween decoration for its one map: the markerset `SM_Map_v5_holiday_treatment` (94 markers:
candles, pumpkins, jack-o'-lanterns, a tomb, a few lights and decals; every model ships), which `SM_Map_v5.Level`
doesn't list, so it never loads. This script makes an added archive in which the level lists it under a condition, so
that the server decides per match whether a client shows it:

  AssetData/Binary/SM_Map_v5.Level.bin
      the copy's own level with `SM_Map_v5_holiday_treatment.Markerset` appended to its markerset list (30 instead of
      29; appended last, so every shipped markerset keeps its place and load order)
  AssetData/Binary/SM_Map_v5_holiday_treatment.Markerset.bin
      the copy's own markerset with the condition list [hordeLevelsOnly] (enum value 0) instead of an empty one

How the client picks markersets (GameWorld_LoadMarkersets 0x008FBF10, called by GameWorld_LoadLevel 0x008FC260 from
GameSimulator_StartLevel 0x008F0C80 with the simulator's level, seed and level flags, +0x19DF0/+0x19DF4/+0x19DF8): a
markerset whose condition list names value c loads only when bit c of the level flags is set (all conditions must
hold); then a markerset of the group "none" (0x2CA33BDB) loads, one of every other group is drawn by weight with the
seed. The level flags come from the GMS: GamePrepareForStart's u32 at offset 16 (Client_LoadLevel → SetLevel). No
shipped markerset has a condition, and nothing else in the client reads the level flags (only the replay recorder
copies them), so bit 0 (LEVEL_FLAG, the Darkspore name hordeLevelsOnly) is free: the server sends level flags 1 when
its operator turned the decoration on, 0 otherwise, and a copy with this archive shows the decoration only then. A
copy without the archive never shows it.

Both files are in the catalog already (replacing an asset needs no catalog change): the client reads each `.bin` by
its path from the package depot, where every game/Data/*.snap is mounted alphabetically and the last archive holding
a path wins, so `zz_Halloween.snap` (after AssetData.snap) replaces them. Nothing of the game's is committed: the
archive is made at setup time from the copy's own AssetData.snap, which must be the build's (the setups check its
SHA-256 against docs/file_manifest.csv). server/internal/setup/halloween is the Go twin, byte for byte.

The edits, in AssetData's file format (a fixed part, then the out-of-line data in member order): a Level's fixed part is 0x620 bytes with the
markersets array {pointer, count} at +0x00; its out-of-line data starts with the array's count pointer words, then each
element's asset name (NUL-terminated). The new entry is one more pointer word (the last one's value: non-zero means
present, the client sets it from its cursor) after the last word and its name after the last name. A Markerset's fixed
part is 0x28 bytes: markers {pointer, count} +0x00, group Key +0x08..+0x17 (its text pointer at +0x14), weight +0x18,
condition {pointer, count} +0x20; condition is the last member, so its elements (u32 each) are the file's last bytes:
the count set, the pointer word set to the markers array's (non-zero), the values appended.

Usage:
    .venv/Scripts/python -I re/scripts/halloween.py <working copy game/Data dir> [--check | --remove]
    .venv/Scripts/python -I re/scripts/halloween.py --build <AssetData.snap> <out.snap>
--check exits 1 when the archive isn't what this script makes now; --remove deletes it.
Refuses to touch the pristine extractions under client/ia_*.
"""
import argparse
import hashlib
import os
import struct
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import snap  # noqa: E402

ARCHIVE = "zz_Halloween.snap"
BINARY = "AssetData/Binary/{}.bin"
LEVEL = "SM_Map_v5.Level"
MARKERSET = "SM_Map_v5_holiday_treatment.Markerset"
CONDITION = 0            # the condition enum's hordeLevelsOnly
LEVEL_FLAG = 1 << CONDITION  # what GamePrepareForStart's level flags carry for the decoration

LEVEL_SIZE = 0x620
MARKERSET_SIZE = 0x28


class HalloweenError(ValueError):
    pass


def _cstr_end(data, pos):
    end = data.find(b"\0", pos)
    if end < 0:
        raise HalloweenError(f"unterminated string at {pos:#x}")
    return end + 1


def add_markerset(level, name):
    """The Level file with the markerset `name` (e.g. "X.Markerset") appended to its markersets list."""
    if len(level) < LEVEL_SIZE + 4:
        raise HalloweenError(f"a Level file has at least {LEVEL_SIZE + 4:#x} bytes, this one {len(level)}")
    ptr, count = struct.unpack_from("<II", level, 0)
    if ptr == 0 or count == 0 or count > 1000:
        raise HalloweenError(f"the level's markerset list is unexpected (pointer {ptr:#x}, count {count})")
    words_end = LEVEL_SIZE + 4 * count
    pos = words_end
    names = []
    for _ in range(count):
        end = _cstr_end(level, pos)
        names.append(level[pos:end - 1].decode("ascii", "replace"))
        pos = end
    if any(n.lower() == name.lower() for n in names):
        raise HalloweenError(f"the level lists {name} already")
    last_word = level[words_end - 4:words_end]
    out = bytearray(level[:words_end]) + last_word + level[words_end:pos] + name.encode("ascii") + b"\0" + level[pos:]
    struct.pack_into("<I", out, 4, count + 1)
    return bytes(out)


def markerset_names(level):
    """The markerset names a Level file lists."""
    count = struct.unpack_from("<I", level, 4)[0]
    pos = LEVEL_SIZE + 4 * count
    names = []
    for _ in range(count):
        end = _cstr_end(level, pos)
        names.append(level[pos:end - 1].decode("ascii", "replace"))
        pos = end
    return names


def set_condition(markerset, values):
    """The Markerset file with the condition list `values` (u32 enum values) instead of its empty one."""
    if len(markerset) < MARKERSET_SIZE:
        raise HalloweenError(f"a Markerset file has at least {MARKERSET_SIZE:#x} bytes, this one {len(markerset)}")
    markers_ptr = struct.unpack_from("<I", markerset, 0x00)[0]
    key_text = struct.unpack_from("<I", markerset, 0x14)[0]
    cptr, ccount = struct.unpack_from("<II", markerset, 0x20)
    if ccount != 0 or cptr != 0:
        raise HalloweenError(f"the markerset has a condition list already (pointer {cptr:#x}, count {ccount})")
    if markers_ptr == 0 or key_text == 0 or not markerset.endswith(b"\0"):
        raise HalloweenError("the markerset doesn't end with its group's text (no markers, or no group text)")
    out = bytearray(markerset)
    struct.pack_into("<II", out, 0x20, markers_ptr, len(values))
    for v in values:
        out += struct.pack("<I", v)
    return bytes(out)


def build(assetdata_snap):
    """The archive's bytes, made from the path of a copy's AssetData.snap (the build's)."""
    s = snap.Snap(assetdata_snap)
    entries = {e.name.lower(): e for e in s.entries}

    def read(asset):
        rel = BINARY.format(asset)
        e = entries.get(rel.lower())
        if e is None:
            raise HalloweenError(f"{assetdata_snap}: no {rel}")
        return s.read(e)

    files = [(BINARY.format(LEVEL), add_markerset(read(LEVEL), MARKERSET)),
             (BINARY.format(MARKERSET), set_condition(read(MARKERSET), [CONDITION]))]
    return snap.pack_bytes(files)


def paths(data_dir):
    """(the copy's AssetData.snap, the added archive) for a copy's game/Data folder."""
    return os.path.join(data_dir, "AssetData.snap"), os.path.join(data_dir, ARCHIVE)


def refuse_pristine(path):
    parts = [p.lower() for p in os.path.abspath(path).split(os.sep)]
    if any(p.startswith("ia_") for p in parts):
        sys.exit(f"refusing to touch the pristine extraction {path}")


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("data_dir", nargs="?", help="a working copy's game/Data folder")
    g = ap.add_mutually_exclusive_group()
    g.add_argument("--check", action="store_true", help="exit 1 when the archive isn't what this script makes now")
    g.add_argument("--remove", action="store_true", help="delete the archive")
    g.add_argument("--build", nargs=2, metavar=("ASSETDATA_SNAP", "OUT"), help="write the archive made from that file")
    a = ap.parse_args(argv)
    if a.build:
        refuse_pristine(a.build[1])
        want = build(a.build[0])
        with open(a.build[1], "wb") as f:
            f.write(want)
        print(f"wrote {a.build[1]}: {len(want)} bytes, SHA-256 {hashlib.sha256(want).hexdigest()[:16]}")
        return 0
    if not a.data_dir:
        ap.error("a game/Data folder (or --build)")
    src, added = paths(a.data_dir)
    if a.remove:
        refuse_pristine(added)
        if os.path.exists(added):
            os.remove(added)
            print(f"removed {added}")
        return 0
    want = build(src)
    have = open(added, "rb").read() if os.path.isfile(added) else None
    line = f"{added}: the Halloween decoration under level flag {LEVEL_FLAG} ({hashlib.sha256(want).hexdigest()[:16]})"
    if have == want:
        print("current: " + line)
        return 0
    if a.check:
        print(("missing: " if have is None else "stale: ") + line)
        return 1
    refuse_pristine(added)
    tmp = added + ".partial"
    with open(tmp, "wb") as f:
        f.write(want)
    os.replace(tmp, added)
    print("wrote: " + line)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
