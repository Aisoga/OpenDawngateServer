"""Add the project's own bot profiles to a working copy: game/Data/zz_BotProfiles.snap.

A bot's profile is a `BotClass` asset (AssetData `<Name>.BotClass`, 0x1C bytes: nounDef asset, aiDefinition asset, role
enum, skillOrder, spellOrder). The match host gives each bot player one (LabsPlayer_ReflectBotData), and every client
sets the bot's shaper, role and "<shaper> BOT" name from the BotClass whose asset id (FNV-1 of the lower-cased
`<Name>.BotClass`) the bot's player update carries. The game ships no BotClass with the Predator role, so a Predator bot
ran on the Warden's Tactician profile and showed as a Tactician. This script makes new BotClass assets that copy a
shipped one and change only its role:

  PC_Warden_Predator.BotClass   PC_Warden.BotClass with role predator (3): the Warden's AI (BOT_warden_Tactician),
                                noun, skill and spell order unchanged

How the client finds them (the game's own loader): `AssetData.snap` holds `AssetData/Binary/<Name>.<Class>.bin` and the
index `AssetData/Binary/catalog_153.bin` (class Catalog: one CatalogEntry per asset). At start the client loads every
asset the catalog lists and nothing else, reading each `.bin` by its path from the package depot, where every
`game/Data/*.snap` is mounted in alphabetical order and the last archive holding a path wins. So the added archive
`zz_BotProfiles.snap` (sorts after AssetData.snap) holds:
  - `AssetData/Binary/catalog_153.bin`: the copy's own catalog with one entry appended per profile (a copy of the base
    asset's entry: the same compile time, typeCrc, source file and tags; only the name differs). The entry's typeCrc
    must equal the client's BotClass reflection checksum, which the base entry's does.
  - `AssetData/Binary/<Name>.BotClass.bin`: the base asset's file with the role field (+0x08, a u32) changed.
Nothing of the game's is committed: the archive is made at setup time from the copy's own AssetData.snap, which must be
the build's (the setups check its SHA-256 against docs/file_manifest.csv).

The archive's bytes (server/internal/setup/botprofiles is the Go twin, byte for byte): entries in the order catalog,
then the profiles in table order, each a Snappy block from snap.py's encoder, in snap.py's layout. A copy is current
when its archive is byte for byte what this script makes; a changed profile table changes the bytes, so every setup
rewrites older archives (the table is the version).

Usage:
    .venv/Scripts/python -I re/scripts/bot_profiles.py <working copy game/Data dir> [--check | --remove | --print]
    .venv/Scripts/python -I re/scripts/bot_profiles.py --build <AssetData.snap> <out.snap>
--check exits 1 when the archive isn't what this script makes now; --remove deletes it; --print lists the profiles.
Refuses to touch the pristine extractions under client/ia_*.
"""
import argparse
import hashlib
import os
import struct
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import snap  # noqa: E402

ARCHIVE = "zz_BotProfiles.snap"
REL = "game/Data/" + ARCHIVE  # in a copy (setup_all.py, setup_lane.py, test_p3b.py)
SOURCE = "game/Data/AssetData.snap"  # the copy's own asset data it is made from
CATALOG = "AssetData/Binary/catalog_153.bin"
BINARY = "AssetData/Binary/{}.bin"
ENTRY_SIZE = 0x30  # CatalogEntry
BOTCLASS_SIZE = 0x1C
BOTCLASS_ROLE = 0x08
BOTCLASS_TYPECRC = 0xDF9D7C39  # the client's checksum of its BotClass reflection table (docs/data/assetdata_types.txt)
ROLES = {"gladiator": 0, "tactician": 1, "hunter": 2, "predator": 3}

# name, base BotClass, role (RolesTuning.role[] index). The order is the archive's; append new profiles at the end.
PROFILES = [
    ("PC_Warden_Predator", "PC_Warden", "predator"),
]


class ProfileError(ValueError):
    pass


def fnv1(text):
    """FNV-1, 32 bit, of the lower-cased text: an asset's id."""
    h = 0x811C9DC5
    for b in text.lower().encode("utf-8"):
        h = (h * 0x01000193) & 0xFFFFFFFF
        h ^= b
    return h


def _cstr(data, pos):
    end = data.find(b"\0", pos)
    if end < 0:
        raise ProfileError(f"catalog: unterminated string at {pos:#x}")
    return data[pos:end].decode("utf-8"), end + 1


def parse_catalog(data):
    """The catalog's entries: [(fixed 0x30 bytes, name, source, tags)], checking that the walk ends on the last byte.

    Layout (the client's reflected walk): Catalog {entries ptr, count}; the count x 0x30 element block; then per element
    in order its out-of-line data in member order: assetNameType (char*), sourceFileNameType (char*), tags (array of
    char*: the count x 4 pointer block, then each present string)."""
    if len(data) < 8:
        raise ProfileError("catalog: too short")
    ptr, count = struct.unpack_from("<II", data, 0)
    if not ptr or not count:
        raise ProfileError("catalog: no entries")
    pos = 8 + count * ENTRY_SIZE
    if pos > len(data):
        raise ProfileError("catalog: the entry block exceeds the file")
    out = []
    for i in range(count):
        fixed = data[8 + i * ENTRY_SIZE:8 + (i + 1) * ENTRY_SIZE]
        name_ptr, source_ptr, tags_ptr, tags_n = (struct.unpack_from("<I", fixed, o)[0] for o in (0x00, 0x1C, 0x24, 0x28))
        name = source = None
        if name_ptr:
            name, pos = _cstr(data, pos)
        if source_ptr:
            source, pos = _cstr(data, pos)
        tags = []
        if tags_n:
            ptrs = struct.unpack_from(f"<{tags_n}I", data, pos)
            pos += 4 * tags_n
            for p in ptrs:
                if p:
                    t, pos = _cstr(data, pos)
                    tags.append(t)
        out.append((fixed, name, source, tags))
    if pos != len(data):
        raise ProfileError(f"catalog: the walk ends at {pos:#x}, the file at {len(data):#x}")
    return out


def build_catalog(data, additions):
    """The catalog with one entry appended per (name, base entry): the base entry's fixed bytes and its source and tags,
    the new name. Every other byte stays where the client's walk expects it (the element block grows by 0x30 bytes, the
    new entry's strings go at the end)."""
    entries = parse_catalog(data)
    count = len(entries)
    by_name = {e[1].lower(): e for e in entries if e[1]}
    head = bytearray(data[:8 + count * ENTRY_SIZE])
    tail = bytearray()
    for name, base in additions:
        if name.lower() in by_name:
            raise ProfileError(f"catalog: {name} is already listed")
        e = by_name.get(base.lower())
        if e is None:
            raise ProfileError(f"catalog: {base} is not listed")
        fixed, _, source, tags = e
        if struct.unpack_from("<I", fixed, 0x14)[0] != BOTCLASS_TYPECRC:
            raise ProfileError(f"catalog: {base} has typeCrc {struct.unpack_from('<I', fixed, 0x14)[0]:#010x}, "
                               f"not BotClass's {BOTCLASS_TYPECRC:#010x}")
        if not source:
            raise ProfileError(f"catalog: {base} has no source file")
        if tags or struct.unpack_from("<I", fixed, 0x28)[0]:
            raise ProfileError(f"catalog: {base} has tags, which this writer does not copy")  # no shipped BotClass has
        head += fixed
        tail += name.encode("utf-8") + b"\0" + source.encode("utf-8") + b"\0"
        by_name[name.lower()] = (fixed, name, source, tags)
    struct.pack_into("<I", head, 4, count + len(additions))
    out = bytes(head) + data[8 + count * ENTRY_SIZE:] + bytes(tail)
    check = parse_catalog(out)
    if [e[1] for e in check[count:]] != [n for n, _ in additions]:
        raise ProfileError("catalog: the new entries do not read back")
    return out


def build_botclass(base, role):
    """The base BotClass file with its role field set to `role` (the rest, out-of-line strings included, unchanged)."""
    if len(base) < BOTCLASS_SIZE:
        raise ProfileError("BotClass: shorter than its fixed part")
    old = struct.unpack_from("<I", base, BOTCLASS_ROLE)[0]
    if old not in ROLES.values():
        raise ProfileError(f"BotClass: role {old} is not a role")
    out = bytearray(base)
    struct.pack_into("<I", out, BOTCLASS_ROLE, role)
    return bytes(out)


def build(assetdata_snap):
    """The archive's bytes, made from the path of a copy's AssetData.snap (the build's)."""
    s = snap.Snap(assetdata_snap)
    entries = {e.name.lower(): e for e in s.entries}

    def read(rel):
        e = entries.get(rel.lower())
        if e is None:
            raise ProfileError(f"{assetdata_snap}: no {rel}")
        return s.read(e)

    catalog = build_catalog(read(CATALOG), [(n + ".BotClass", b + ".BotClass") for n, b, _ in PROFILES])
    files = [(CATALOG, catalog)]
    for name, base, role in PROFILES:
        files.append((BINARY.format(name + ".BotClass"), build_botclass(read(BINARY.format(base + ".BotClass")),
                                                                          ROLES[role])))
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
    g.add_argument("--print", action="store_true", help="list the profiles and their asset ids")
    g.add_argument("--build", nargs=2, metavar=("ASSETDATA_SNAP", "OUT"), help="write the archive made from that file")
    a = ap.parse_args(argv)
    if a.print:
        for name, base, role in PROFILES:
            print(f"{name}.BotClass  {fnv1(name + '.BotClass'):08X}  = {base}.BotClass "
                  f"({fnv1(base + '.BotClass'):08X}) with role {role} ({ROLES[role]})")
        return 0
    if a.build:
        refuse_pristine(a.build[1])
        want = build(a.build[0])
        with open(a.build[1], "wb") as f:
            f.write(want)
        print(f"wrote {a.build[1]}: {len(PROFILES)} profile(s), {len(want)} bytes, "
              f"SHA-256 {hashlib.sha256(want).hexdigest()[:16]}")
        return 0
    if not a.data_dir:
        ap.error("a game/Data folder (or --print, --build)")
    src, added = paths(a.data_dir)
    if a.remove:
        refuse_pristine(added)
        if os.path.exists(added):
            os.remove(added)
            print(f"removed {added}")
        return 0
    want = build(src)
    have = open(added, "rb").read() if os.path.isfile(added) else None
    line = f"{added}: {len(PROFILES)} bot profile(s) ({hashlib.sha256(want).hexdigest()[:16]})"
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
