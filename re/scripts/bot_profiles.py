"""Add the project's own bot profiles to a working copy: game/Data/zz_BotProfiles.snap.

A bot's profile is a `BotClass` asset (AssetData `<Name>.BotClass`, 0x1C bytes: nounDef asset, aiDefinition asset, role
enum, skillOrder, spellOrder). The match host gives each bot player one (LabsPlayer_ReflectBotData), and every client
sets the bot's shaper, role and "<shaper> BOT" name from the BotClass whose asset id (FNV-1 of the lower-cased
`<Name>.BotClass`) the bot's player update carries. The game ships no BotClass with the Predator role, so a Predator bot
ran on the Warden's Tactician profile and showed as a Tactician. This script makes new BotClass assets that copy a
shipped one and change only its role:

  PC_Warden_Predator.BotClass   PC_Warden.BotClass with role predator (3): the Warden's AI (BOT_warden_Tactician),
                                noun, skill and spell order unchanged
  PC_Golem_Hunter.BotClass      HUNTERBOT: Faris (golem.Noun) as a Hunter (2) on PC_Artificer's AI
                                (BOT_Artificer_Hunter), with a skill order of his own and Petrus's spells
  PC_Survivor_Hunter.BotClass   HUNTERBOT: Freia (survivor.Noun) as a Hunter on the same AI, her own skill order
  PC_Desecrator_Tactician.BotClass  Desecrator (desecrator.Noun) as a Tactician (1) on PC_Guardian's AI
                                (BOT_Guardian_Tactician), with his own skill order and spells (Drain, Tailwind, Wither)
  PC_MerchantPrincess_Gladiator.BotClass  MIKELLABOT: Mikella (merchant_princess.Noun) as a Gladiator (0) on
                                PC_HighHuntress's AI (BOT_HighHuntress_Gladiator), her own skill order and spells
                                (Dispel, Blitz, Blink)
  PC_Otter_Hunter.BotClass      MOYABOT: Moya (otter.Noun) as a Hunter on PC_Artificer's AI, with a 2014 player guide's
                                skill order and spells (Vanquish, Blitz, Wither)
A borrowed-AI profile (HUNTERBOT, `ai` set) is written field by field: the noun from its base (PC_Golem.BotClass,
which ships without an AI), the AI definition, skill order and spell order from the `ai` BotClass unless the table
gives its own; the pointer words (non-zero = present; the client sets them from its cursor) are the `ai` file's. The
Hunter and Tactician AIs' gambits name ability slots (Q, W, E, R, basic, spell1-3, consume1), not abilities, so a
slot casts the bot's own shaper's ability (the host's gms/core/bots/kits.h says which slots fit which shaper).

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


class Profile(tuple):
    """(name, base, role, ai, skills, spells): `ai` = the BotClass whose AI definition (and, unless given, skill and
    spell order) the profile borrows; None = the base's own file with only the role changed."""
    __slots__ = ()

    def __new__(cls, name, base, role, ai=None, skills=None, spells=None):
        return tuple.__new__(cls, (name, base, role, ai, skills, spells))

    name = property(lambda self: self[0])
    base = property(lambda self: self[1])
    role = property(lambda self: self[2])
    ai = property(lambda self: self[3])
    skills = property(lambda self: self[4])
    spells = property(lambda self: self[5])


# The skill orders: the ability slot ranked at each level 1-18 (3 Q, 4 W, 5 E, 6 R; R at 6, 10, 15 as in every shipped
# BotClass). Faris: Q maxed first, though the host never casts it for him (gms/core/bots/kits.h): its ranks are his
# basic attack's power (ability_Golem_Basic adds Q_BasicSwitch's 10-30 magic damage, 2 % of the target's HP up to 30-170
# against creeps, and 15-35 % attack speed), then W (Sentinel), E (the dash). Freia: Q (Bleed) maxed, W (Dash)
# second, E (SlowStun) last. [assumed]
GOLEM_SKILLS = [3, 4, 5, 3, 3, 6, 3, 4, 3, 6, 4, 4, 4, 5, 6, 5, 5, 5]
SURVIVOR_SKILLS = [3, 4, 5, 3, 3, 6, 3, 4, 3, 6, 4, 4, 4, 5, 6, 5, 5, 5]
# Desecrator: a 2014 player guide's order (Q maxed first, E second, W last; R at entries 6, 10, 15 = levels 6, 11, 16:
# the 18 entries are the ability points of levels 1-9 and 11-19), the same as PC_Artist's; his spells in that guide's
# order, Drain at level 1, Tailwind at 10, Wither at 20 (the spell points of levels 1, 10, 20).
DESECRATOR_SKILLS = [3, 5, 4, 3, 3, 6, 3, 5, 3, 6, 5, 5, 5, 4, 6, 4, 4, 4]
DESECRATOR_SPELLS = ["Spell_Drain.Item", "Spell_Tailwind.Item", "Spell_Wither.Item"]
# MIKELLABOT: Mikella as a Gladiator, a 2014 player guide's order (`qeqwqrqeqreeewrwww`: Q maxed first, E second, W
# last; R at levels 6, 11, 16). The guide's spells are Dispel (level 1), Blink (10) and Blitz (20); here Blitz is the
# second and Blink the third, so each sits where the High Huntress AI casts that kind of spell (spell2 on a big hit:
# a self cast; spell3 toward the base in Flee: Blink) [assumed].
MERCHANTPRINCESS_SKILLS = [3, 5, 3, 4, 3, 6, 3, 5, 3, 6, 5, 5, 5, 4, 6, 4, 4, 4]
MERCHANTPRINCESS_SPELLS = ["Spell_Dispel.Item", "Spell_Blitz.Item", "Spell_Blink.Item"]
# Moya: mcscrag's guide (moba-champion.com 143, 2014-04-18), `qewqqrqeqrwwwwreee`: Q maxed first, two points in E early
# (her sustain in the jungle), then W, E last; R at entries 6, 10, 15. Spells: the guide's first set, Vanquish in slot 1
# (the Hunter AI's camp spell), Blitz in slot 2 (its gank spell), Wither third.
OTTER_SKILLS = [3, 5, 4, 3, 3, 6, 3, 5, 3, 6, 4, 4, 4, 4, 6, 5, 5, 5]
OTTER_SPELLS = ["Spell_Vanquish.Item", "Spell_Blitz.Item", "Spell_Wither.Item"]

# The table (gms/core/bots/profiles.h and server/internal/setup/botprofiles hold the same). The order is the archive's;
# append new profiles at the end.
PROFILES = [
    Profile("PC_Warden_Predator", "PC_Warden", "predator"),
    Profile("PC_Golem_Hunter", "PC_Golem", "hunter", ai="PC_Artificer", skills=GOLEM_SKILLS),
    Profile("PC_Survivor_Hunter", "PC_Survivor", "hunter", ai="PC_Artificer", skills=SURVIVOR_SKILLS),
    Profile("PC_Desecrator_Tactician", "PC_Desecrator", "tactician", ai="PC_Guardian", skills=DESECRATOR_SKILLS,
            spells=DESECRATOR_SPELLS),
    Profile("PC_MerchantPrincess_Gladiator", "PC_MerchantPrincess", "gladiator", ai="PC_HighHuntress",
            skills=MERCHANTPRINCESS_SKILLS, spells=MERCHANTPRINCESS_SPELLS),
    Profile("PC_Otter_Hunter", "PC_Otter", "hunter", ai="PC_Artificer", skills=OTTER_SKILLS, spells=OTTER_SPELLS),
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


def parse_botclass(data):
    """A BotClass file's fields: {words (the 7 fixed u32), noun, ai, skills, spells, spell_words}, checking that the
    walk ends on the last byte. Out of line, in member order: nounDef's name, aiDefinition's name, skillOrder's
    count x u32, spellOrder's count x 4 pointer words, then each name."""
    if len(data) < BOTCLASS_SIZE:
        raise ProfileError("BotClass: shorter than its fixed part")
    words = list(struct.unpack_from("<7I", data, 0))
    noun_p, ai_p, role, _, skill_n, _, spell_n = words
    pos = BOTCLASS_SIZE
    noun = ai = None
    if noun_p:
        noun, pos = _cstr(data, pos)
    if ai_p:
        ai, pos = _cstr(data, pos)
    if skill_n * 4 > len(data) - pos or spell_n * 4 > len(data) - pos:
        raise ProfileError("BotClass: an array exceeds the file")
    skills = list(struct.unpack_from(f"<{skill_n}I", data, pos)) if skill_n else []
    pos += 4 * skill_n
    spell_words = list(struct.unpack_from(f"<{spell_n}I", data, pos)) if spell_n else []
    pos += 4 * spell_n
    spells = []
    for w in spell_words:
        if not w:
            raise ProfileError("BotClass: a spell without a name")  # no shipped BotClass has one
        name, pos = _cstr(data, pos)
        spells.append(name)
    if pos != len(data):
        raise ProfileError(f"BotClass: the walk ends at {pos:#x}, the file at {len(data):#x}")
    if role not in ROLES.values():
        raise ProfileError(f"BotClass: role {role} is not a role")
    return {"words": words, "noun": noun, "ai": ai, "skills": skills, "spells": spells, "spell_words": spell_words}


def write_botclass(f):
    """The file of the fields `f` (parse_botclass's): its inverse."""
    noun_p, ai_p, role, skill_p, _, spell_p, _ = f["words"]
    out = bytearray(struct.pack("<7I", noun_p if f["noun"] else 0, ai_p if f["ai"] else 0, role,
                                skill_p if f["skills"] else 0, len(f["skills"]), spell_p if f["spells"] else 0,
                                len(f["spells"])))
    for s in (f["noun"], f["ai"]):
        if s:
            out += s.encode("utf-8") + b"\0"
    out += struct.pack(f"<{len(f['skills'])}I", *f["skills"])
    out += struct.pack(f"<{len(f['spells'])}I", *f["spell_words"])
    for s in f["spells"]:
        out += s.encode("utf-8") + b"\0"
    return bytes(out)


def build_borrowed(base, donor, role, skills=None, spells=None):
    """A borrowed-AI profile: the base BotClass's noun on the donor's AI definition with `role`; the skill and spell
    order the given ones, else the donor's; the pointer words the donor's (a spell beyond the donor's count gets the
    previous word + 0xE8: any non-zero word reads as present)."""
    b, d = parse_botclass(base), parse_botclass(donor)
    if not b["noun"] or not d["ai"]:
        raise ProfileError("BotClass: the base has no noun or the donor no AI definition")
    skills = list(skills if skills is not None else d["skills"])
    spells = list(spells if spells is not None else d["spells"])
    words = list(d["words"])
    words[2] = role
    if (skills and not words[3]) or (spells and not words[5]):
        raise ProfileError("BotClass: the donor has no pointer word for a member the profile fills")
    if any(s not in (3, 4, 5, 6) for s in skills):
        raise ProfileError("BotClass: a skill order entry is not a ranked slot (3-6)")
    sw = list(d["spell_words"][:len(spells)])
    while len(sw) < len(spells):
        sw.append(((sw[-1] if sw else words[5]) + 0xE8) & 0xFFFFFFFF)
    out = write_botclass({"words": words, "noun": b["noun"], "ai": d["ai"], "skills": skills, "spells": spells,
                          "spell_words": sw})
    parse_botclass(out)
    return out


def build_profile(p, read):
    """The profile's BotClass file; `read(name)` gives a shipped BotClass file by its asset name (without .BotClass)."""
    if p.ai is None:
        return build_botclass(read(p.base), ROLES[p.role])
    return build_borrowed(read(p.base), read(p.ai), ROLES[p.role], p.skills, p.spells)


def build(assetdata_snap):
    """The archive's bytes, made from the path of a copy's AssetData.snap (the build's)."""
    s = snap.Snap(assetdata_snap)
    entries = {e.name.lower(): e for e in s.entries}

    def read(rel):
        e = entries.get(rel.lower())
        if e is None:
            raise ProfileError(f"{assetdata_snap}: no {rel}")
        return s.read(e)

    catalog = build_catalog(read(CATALOG), [(p.name + ".BotClass", p.base + ".BotClass") for p in PROFILES])
    files = [(CATALOG, catalog)]
    for p in PROFILES:
        files.append((BINARY.format(p.name + ".BotClass"),
                      build_profile(p, lambda n: read(BINARY.format(n + ".BotClass")))))
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
        for p in PROFILES:
            print(f"{p.name}.BotClass  {fnv1(p.name + '.BotClass'):08X}  = {p.base}.BotClass "
                  f"({fnv1(p.base + '.BotClass'):08X}) with role {p.role} ({ROLES[p.role]})"
                  + (f" on {p.ai}.BotClass's AI" if p.ai else ""))
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
