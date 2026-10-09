"""Reader for AssetData.snap's binary asset definitions (AssetData/Binary/<Name>.<Class>.bin and
catalog_153.bin), driven by the client's own reflection tables (docs/data/assetdata_types.txt, written
by assetdata_types.py from Dawngate.exe). Stdlib only. Format and findings: docs/ASSETDATA.md.

Format. A .bin file is one object of its class, written by the build tools as a memory dump plus
the data its pointers point to:
  * the fixed part: the struct exactly as in memory (`size` bytes from the type table), including
    the build machine's pointer values (0x02xxxxxx-0x03xxxxxx) and uninitialized padding;
  * then the out-of-line data, back to back with no alignment, in member-table order, depth first:
      char*, asset      a NUL-terminated string (an asset is referenced by "<Name>.<Class>")
      Key               16 bytes {instance, type, group, char*} or {char*, instance, type, group};
                        its char* text ("Table!0x<id>", "Group!Name.ext" or "path\\name.ext")
      StringHash        {char*, FNV-1} or {FNV-1, char*}; the string
      array             {pointer, u32 count}: count x element size bytes of elements, then each
                        element's own out-of-line data in element order
      nullable          {pointer}: the pointed-to struct, then its out-of-line data
      inline struct     its members' out-of-line data, in its own member order
    A pointer only says "present" (non-zero) or "absent" (zero); its value is meaningless. An array
    is decided by its count alone (the client ignores its pointer).
This is the walk of the client's own loader (R11b: 0x008D4020, AssetCatalog.cpp; docs/ASSETDATA.md
*The loader*), including its element rule: an array's struct elements are walked only if the
client's 0x008CCDF0 says the type needs it, and arrays of anything but structs, char* and asset carry
no out-of-line data. Differences kept on purpose: the client accepts trailing bytes (it only checks
that the cursor stays inside the file), this reader demands an exact end; Key/StringHash hash parts
are read here only to check them.
Every reference is checked against itself: a Key's instance/type/group must be the FNV-1 of the
parts of its text (Locale keys: instance = the id, group = FNV-1(table)), a StringHash's hash the
FNV-1 of its string, and the whole file must be consumed exactly. `check` reports both per class,
and, like the client before it loads a .bin, compares each catalog entry's typeCrc with the type
table's crc (from assetdata_types.txt).
Bytes no member covers are the results of the classes' postLoad hooks, which the build tool ran
before it saved the object (Shape +0x14 mShapeArea, NonPlayerClass +0x50 drop-type mask, Item +0xB8,
...); the client recomputes them at every load.

Output of `dump`: <out>/<Class>.json per class ({"class", "count", "instances": {name: object}}),
<out>/catalog_ids.json (every CollectionItem by catalogID, the id the client and the Blaze meta
services use for items) and <out>/_summary.json. The dump is copyrighted game data: write it under
re/out/ (gitignored) and never commit it.

Values: enums print as their names, bitfields as lists of flag names, tObjID/guid as hex, Keys as
their text (with --locale, Locale keys become {"ref": "Table!0x<id>", "text": "<English>"}), floats
rounded to 7 significant digits. An object carries "_unk" with any non-zero bytes no member covers
(padding garbage), so nothing is hidden.

Usage:
    python -I re/scripts/assetdata.py check  <bin_dir> [--class C ...] [-v]
    python -I re/scripts/assetdata.py dump   <bin_dir> <out_dir> [--class C ...] [--locale Locale.package]
    python -I re/scripts/assetdata.py show   <bin_dir> <Name.Class> [--locale Locale.package]
    python -I re/scripts/assetdata.py fields <bin_dir> <Class>      (per member: distinct values, examples)
    before the command: --types <file> (default: docs/data/assetdata_types.txt of this checkout)
<bin_dir> is the extracted AssetData/Binary folder (snap.py extract ... re/out/snap gives
re/out/snap/AssetData/AssetData/Binary). The default class set of check/dump is every class with
files; `--class` takes class names (case-insensitive) such as CollectionItem or Catalog.
"""
import argparse
import json
import os
import re
import struct
import sys
from collections import Counter, defaultdict

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_TYPES = os.path.join(HERE, "..", "..", "docs", "data", "assetdata_types.txt")

PRIM = {"float": ("<f", 4), "bool": ("<B", 1), "int": ("<i", 4), "int32_t": ("<i", 4),
        "uint32_t": ("<I", 4), "uint8_t": ("<B", 1), "uint16_t": ("<H", 2), "uint64_t": ("<Q", 8),
        "int64_t": ("<q", 8), "enum": ("<i", 4), "bitfield": ("<I", 4), "tObjID": ("<I", 4),
        "guid": ("<I", 4)}
VECTORS = {"cSPVector2": 2, "cSPVector3": 3, "cSPVector4": 4, "cSPQuaternion": 4}
STRINGS = {"char*", "asset"}


def fnv1(text):
    h = 0x811C9DC5
    for c in text.lower().encode("latin-1", "replace"):
        h = (h * 0x01000193) & 0xFFFFFFFF
        h ^= c
    return h


class Member:
    __slots__ = ("index", "offset", "name", "type", "count", "elem", "esize", "delta", "enum")


class Type:
    def __init__(self, name, size):
        self.name, self.size, self.members = name, size, []
        self.crc = None      # typeCrc (None with a types file older than R11b)
        self.hooks = {}      # version/upgrade/postload/unload/hook4 -> value, the non-zero ones
        self.fixup = None    # the client's "needs a fixup walk" (0x008CCDF0), computed lazily


def load_types(path):
    types, enums, cur = {}, {}, None
    mem_re = re.compile(r"^\s+(\d+) \+0x([0-9a-f]+) (\S+) (\S+)(.*)$")
    for line in open(path, encoding="utf-8"):
        if line.startswith("== "):
            m = re.match(r"== (\S+) size=0x([0-9a-f]+) ", line)
            cur = Type(m.group(1), int(m.group(2), 16))
            kv = dict(re.findall(r" (\w+)=(\S+)", line))
            if "crc" in kv:
                cur.crc = int(kv["crc"], 16)
            cur.hooks = {k: int(v, 0) for k, v in kv.items()
                         if k in ("version", "upgrade", "postload", "unload", "hook4")}
            types[cur.name.lower()] = cur
        elif line.startswith("enum "):
            head, vals = line.rstrip("\n").split(": ", 1)
            _, va, kind, _n = head.split()
            pairs = []
            for item in vals.split(" | "):
                v, _, name = item.partition("=")
                pairs.append((int(v), name))
            enums[int(va, 16)] = (kind, pairs)
        else:
            m = mem_re.match(line)
            if m and cur is not None:
                mb = Member()
                mb.index, mb.offset, mb.name, mb.type = int(m.group(1)), int(m.group(2), 16), m.group(3), m.group(4)
                rest = m.group(5)
                kv = dict(re.findall(r"(\w+)=(\S+)", rest))
                cnt = re.search(r" x(\d+)", rest)
                mb.count = int(cnt.group(1)) if cnt else 1
                mb.elem = kv.get("elem")
                mb.esize = int(kv["esize"], 16) if "esize" in kv else 0
                mb.delta = int(kv["delta"]) if "delta" in kv else 0
                mb.enum = int(kv["enum"], 16) if "enum" in kv else None
                cur.members.append(mb)
    return types, enums


def load_locale(path):
    sys.path.insert(0, HERE)
    import dbpf  # noqa: E402  (stdlib-only sibling)
    pkg = dbpf.Package(path)
    out = {}
    for e in pkg.entries:
        text = pkg.read(e).decode("utf-8", "replace")
        table = {}
        for ln in text.splitlines():
            m = re.match(r"0x([0-9a-fA-F]{8})\s(.*)$", ln)
            if m:
                table[int(m.group(1), 16)] = m.group(2)
        out[e.instance & 0xFFFFFFFF] = table
    return out


class ParseError(ValueError):
    pass


class Reader:
    """Parses one file. `pos` is the out-of-line cursor."""

    def __init__(self, data, types, enums, locale=None, stats=None):
        self.d, self.types, self.enums, self.locale = data, types, enums, locale
        self.pos = 0
        self.ref_bad = []          # (member path, text, why)
        self.stats = stats          # padding stats: (type, offset) -> count

    def need(self, off, n):
        if off < 0 or off + n > len(self.d):
            raise ParseError(f"read of {n} bytes at {off:#x} past the end ({len(self.d):#x})")

    def u32(self, off):
        self.need(off, 4)
        return struct.unpack_from("<I", self.d, off)[0]

    def take(self, n):
        start = self.pos
        self.need(start, n)
        self.pos += n
        return start

    def cstring(self):
        self.need(self.pos, 1)
        e = self.d.find(b"\0", self.pos)
        if e < 0:
            raise ParseError(f"unterminated string at {self.pos:#x}")
        s = self.d[self.pos:e].decode("utf-8", "replace")
        self.pos = e + 1
        return s

    # -- values -----------------------------------------------------------------------------------
    def prim(self, tname, off, m):
        fmt, size = PRIM[tname]
        self.need(off, size)
        v = struct.unpack_from(fmt, self.d, off)[0]
        if tname == "float":
            return float(f"{v:.7g}")
        if tname == "bool":
            return bool(v) if v in (0, 1) else v
        if tname in ("tObjID", "guid"):
            return f"0x{v:08x}"
        if tname == "enum" and m is not None and m.enum in self.enums:
            for val, name in self.enums[m.enum][1]:
                if val == v:
                    return name
            return v
        if tname == "bitfield" and m is not None and m.enum in self.enums:
            names, rest = [], v
            for mask, name in self.enums[m.enum][1]:
                if mask and v & mask == mask:
                    names.append(name)
                    rest &= ~mask
            if rest:
                names.append(f"0x{rest:x}")
            return names
        return v

    def key(self, off, delta, path):
        """Key registered at its char* (off); delta = position of the hash part relative to it."""
        hoff = off + delta if delta < 0 else off + 4
        self.need(min(off, hoff), 16)
        inst, typ, grp = struct.unpack_from("<III", self.d, hoff)
        ptr = self.u32(off)
        text = self.cstring() if ptr else None
        if text is None:
            if inst or typ or grp:
                return {"instance": f"0x{inst:08x}", "type": f"0x{typ:08x}", "group": f"0x{grp:08x}"}
            return None
        why = key_mismatch(text, inst, typ, grp)
        if why:
            self.ref_bad.append((path, text, why))
            return {"ref": text, "instance": f"0x{inst:08x}", "type": f"0x{typ:08x}", "group": f"0x{grp:08x}"}
        if self.locale is not None:
            m = re.match(r"([^!]+)!0x([0-9a-fA-F]{8})$", text)
            if m:
                return {"ref": text, "text": self.locale.get(grp, {}).get(inst)}
        return text

    def string_hash(self, off, delta, path):
        hoff = off + delta
        h = self.u32(hoff)
        ptr = self.u32(off)
        text = self.cstring() if ptr else None
        if text is None:
            return None if h == 0 else {"hash": f"0x{h:08x}"}
        if fnv1(text) != h:
            self.ref_bad.append((path, text, f"hash 0x{h:08x} != FNV-1 0x{fnv1(text):08x}"))
            return {"text": text, "hash": f"0x{h:08x}"}
        return text

    def localized_asset_string(self, off, delta, path):
        """cLocalizedAssetString (20 bytes, only in cLongDescription): two char* (+0, +4), then
        the Key hash part {instance, type, group} at +delta (8). Layout inferred from the files."""
        p1, p2 = self.u32(off), self.u32(off + 4)
        inst, typ, grp = struct.unpack_from("<III", self.d, off + delta)
        a = self.cstring() if p1 else None
        b = self.cstring() if p2 else None
        out = {"text": a, "ref": b}
        if b is not None:
            why = key_mismatch(b, inst, typ, grp)
            if why:
                self.ref_bad.append((path, b, why))
                out.update(instance=f"0x{inst:08x}", type=f"0x{typ:08x}", group=f"0x{grp:08x}")
            elif self.locale is not None and "!0x" in b:
                out["en"] = self.locale.get(grp, {}).get(inst)
        return out

    def value(self, tname, off, m, path, count=1):
        if count != 1 and tname != "char":
            size = type_size(self.types, tname, m)
            return [self.value(tname, off + i * size, m, f"{path}[{i}]") for i in range(count)]
        if tname == "char":
            n = m.count if m is not None else 1
            self.need(off, n)
            return self.d[off:off + n].split(b"\0", 1)[0].decode("utf-8", "replace")
        if tname in PRIM:
            return self.prim(tname, off, m)
        if tname in VECTORS:
            n = VECTORS[tname]
            self.need(off, 4 * n)
            return [float(f"{v:.7g}") for v in struct.unpack_from(f"<{n}f", self.d, off)]
        if tname in STRINGS:
            return self.cstring() if self.u32(off) else None
        if tname == "Key":
            return self.key(off, m.delta if m is not None else -12, path)
        if tname == "StringHash":
            return self.string_hash(off, m.delta if m is not None else 4, path)
        if tname == "cLocalizedAssetString":
            return self.localized_asset_string(off, m.delta if m is not None else 8, path)
        if tname == "array":
            n = self.u32(off + (m.delta or 4))   # the count decides; the pointer is ignored (0x008D4224)
            if n == 0:
                return []
            base = self.take(n * m.esize)
            return self.array_elements(m.elem, base, n, m.esize, path)
        if tname == "nullable":
            if not self.u32(off):
                return None
            base = self.take(m.esize)
            return self.element(m.elem, base, path)
        t = self.types.get(tname.lower())
        if t is not None:
            return self.struct(t, off, path)
        raise ParseError(f"{path}: unknown type {tname}")

    def element(self, tname, off, path):
        """A nullable's target: a struct, or a plain value."""
        t = self.types.get(tname.lower())
        if t is not None:
            return self.struct(t, off, path)
        return self.value(tname, off, None, path)

    def array_elements(self, elem, base, n, esize, path):
        """An array's elements, read the way the client's walker reads them (0x008D4266-0x008D4365):
        struct elements are walked if 0x008CCDF0 says the type needs it, char* and asset elements are
        strings, any other element type has no out-of-line data."""
        t = self.types.get(elem.lower())
        if t is not None:
            start = self.pos
            out = [self.struct(t, base + i * esize, f"{path}[{i}]") for i in range(n)]
            if self.pos != start and not needs_fixup(self.types, t):
                raise ParseError(f"{path}: {t.name} elements carry out-of-line data the client skips")
            return out
        if elem in POINTER_TYPES and elem not in STRINGS:
            raise ParseError(f"{path}: the client reads no out-of-line data for an array of {elem}")
        return [self.value(elem, base + i * esize, None, f"{path}[{i}]") for i in range(n)]

    def struct(self, t, base, path=""):
        self.need(base, t.size)
        out = {}
        covered = bytearray(t.size)
        for m in t.members:
            out[m.name] = self.value(m.type, base + m.offset, m, f"{path}.{m.name}" if path else m.name,
                                     m.count)
            lo, hi = member_span(self.types, m)
            for i in range(max(0, lo), min(t.size, hi)):
                covered[i] = 1
        unk = {}
        for i in range(0, t.size, 4):
            chunk = self.d[base + i:base + min(i + 4, t.size)]
            if any(chunk[k] and not covered[i + k] for k in range(len(chunk))):
                unk[f"+0x{i:x}"] = chunk.hex()
                if self.stats is not None:
                    self.stats[(t.name, i)] += 1
        if unk:
            out["_unk"] = unk
        return out


POINTER_TYPES = {"char*", "asset", "Key", "StringHash", "nullable", "cLocalizedAssetString"}


def needs_fixup(types, t):
    """The client's test whether an object of type t needs the fixup walk (0x008CCDF0): a postLoad
    or +0x10 hook, or a member with out-of-line data. Quirk kept: the first array member decides
    alone (the client returns its element's answer without looking at later members)."""
    if t.fixup is None:
        t.fixup = False   # a type that refers to itself stops here
        if t.hooks.get("postload") or t.hooks.get("hook4"):
            t.fixup = True
        else:
            for m in t.members:
                if m.type == "array":
                    t.fixup = type_has_pointers(types, m.elem)
                    break
                if type_has_pointers(types, m.type):
                    t.fixup = True
                    break
    return t.fixup


def type_has_pointers(types, tname):
    """0x008CCC50: a registered type asks needs_fixup; char*, asset, Key, StringHash, nullable and
    cLocalizedAssetString say yes; every other built-in type says no."""
    t = types.get(tname.lower())
    if t is not None:
        return needs_fixup(types, t)
    return tname in POINTER_TYPES


def type_size(types, tname, m):
    if tname in PRIM:
        return PRIM[tname][1]
    if tname in VECTORS:
        return 4 * VECTORS[tname]
    if tname in STRINGS or tname in ("Key", "StringHash", "nullable"):
        return 4
    if tname == "array":
        return 8
    if tname == "char":
        return 1
    t = types.get(tname.lower())
    return t.size if t else 4


def member_span(types, m):
    """Bytes [lo, hi) of the struct a member occupies (relative to the struct start)."""
    o = m.offset
    if m.type == "Key":
        return (o + m.delta, o + 4) if m.delta < 0 else (o, o + 16)
    if m.type == "StringHash":
        return (o + m.delta, o + 4) if m.delta < 0 else (o, o + 8)
    if m.type == "cLocalizedAssetString":
        return (o, o + 20)
    if m.type == "char":
        return (o, o + m.count)
    return (o, o + type_size(types, m.type, m) * m.count)


TYPE_IDS = {"prop": 0x00B1B104}  # Spore's fixed type id for .prop; other extensions hash with FNV-1


def part_id(s, ext=False):
    """A Key part: a hex literal ("0x0d0712d6") stands for itself, anything else is hashed."""
    if re.fullmatch(r"0x[0-9a-fA-F]{1,8}", s):
        return int(s, 16)
    if ext and s.lower() in TYPE_IDS:
        return TYPE_IDS[s.lower()]
    return fnv1(s)


def key_mismatch(text, inst, typ, grp):
    """'' if the Key's instance/type/group are those its text names, else the reason. Text forms:
    "Table!0x<id>" (Locale string), "Group!Name.ext", "Group!0x<inst>.0x<type>", "path\\name.ext"."""
    g, bang, rest = text.partition("!")
    if not bang:
        g, rest = "", text
    if re.fullmatch(r"0x[0-9a-fA-F]{1,8}", rest):
        name, ext = rest, ""
    else:
        name, dot, ext = rest.rpartition(".")
        if not dot:
            name, ext = rest, ""
    want = (part_id(name), part_id(ext, True) if ext else 0, fnv1(g) if g else 0)
    if (inst, typ, grp) == want:
        return ""
    return f"hashes {inst:08x}/{typ:08x}/{grp:08x}, expected {want[0]:08x}/{want[1]:08x}/{want[2]:08x}"


# -- files ----------------------------------------------------------------------------------------
def class_of(fn):
    """('alpha', 'collectionitem') for alpha.CollectionItem.bin; catalog_153.bin -> Catalog. The class
    is everything after the first dot, as the client takes it (strstr, 0x008CCFB0)."""
    base = fn[:-4] if fn.lower().endswith(".bin") else fn
    if re.fullmatch(r"catalog_\d+", base, re.I):
        return base, "catalog"
    name, _, cls = base.partition(".")
    return name, cls.lower()


def list_files(bin_dir, classes=None):
    want = {c.lower() for c in classes} if classes else None
    out = defaultdict(list)
    for fn in sorted(os.listdir(bin_dir)):
        if not fn.lower().endswith(".bin"):
            continue
        name, cls = class_of(fn)
        if want is None or cls in want:
            out[cls].append((name, os.path.join(bin_dir, fn)))
    return out


def parse_file(path, t, types, enums, locale=None, stats=None):
    with open(path, "rb") as f:
        data = f.read()
    r = Reader(data, types, enums, locale, stats)
    r.take(t.size)
    obj = r.struct(t, 0)
    if r.pos != len(data):
        raise ParseError(f"{len(data) - r.pos} unread byte(s) after the out-of-line data (at {r.pos:#x})")
    return obj, r.ref_bad


def cmd_check(args, types, enums):
    files = list_files(args.bin_dir, args.classes)
    total = Counter()
    print(f"{'class':32} {'files':>6} {'exact':>6} {'failed':>6} {'badref':>6}  padding (type+offset: files)")
    for cls in sorted(files):
        t = types.get(cls)
        n = len(files[cls])
        if t is None:
            print(f"{cls:32} {n:6} {'-':>6} {'-':>6} {'-':>6}  no type table")
            total["notype"] += n
            continue
        stats = Counter()
        ok = failed = bad = 0
        first = None
        for name, path in files[cls]:
            try:
                _, refs = parse_file(path, t, types, enums, None, stats)
                ok += 1
                if refs:
                    bad += 1
                    if args.verbose:
                        print(f"    badref {name}: {refs[:3]}")
            except ParseError as e:
                failed += 1
                first = first or f"{name}: {e}"
                if args.verbose:
                    print(f"    FAIL {name}: {e}")
        pad = ", ".join(f"{tn}+0x{o:x}: {c}" for (tn, o), c in sorted(stats.items())[:4])
        print(f"{t.name:32} {n:6} {ok:6} {failed:6} {bad:6}  {pad}")
        if first and not args.verbose:
            print(f"    first failure: {first}")
        total.update(files=n, exact=ok, failed=failed, badref=bad)
    print(f"total: {total['files']} files, {total['exact']} parsed exactly, {total['failed']} failed, "
          f"{total['badref']} with a reference whose hashes don't match its text, "
          f"{total['notype']} without a type table")
    bad_crc = check_catalog_crcs(files.get("catalog", []), types, enums)
    return 1 if total["failed"] or bad_crc else 0


def check_catalog_crcs(catalogs, types, enums):
    """The client's test before it loads a .bin (0x008D4970): the catalog entry's typeCrc must equal
    the type table's typeCrc, or the asset isn't loaded ("out of date with reflection table").
    Returns the number of entries that would fail it."""
    bad = 0
    for name, path in catalogs:
        if types["catalog"].crc is None:
            print("catalog typeCrc: not checked (types file without crc=, older than R11b)")
            return 0
        obj, _ = parse_file(path, types["catalog"], types, enums)
        classes, missing = Counter(), Counter()
        for e in obj["entries"]:
            cls = e["assetNameType"].partition(".")[2].lower()
            t = types.get(cls)
            if t is None:
                missing[cls] += 1
            elif e["typeCrc"] != t.crc:
                bad += 1
                print(f"    typeCrc of {e['assetNameType']}: 0x{e['typeCrc']:08x}, type table 0x{t.crc:08x}")
            else:
                classes[t.name] += 1
        print(f"catalog typeCrc ({name}): {sum(classes.values())} of {len(obj['entries'])} entries "
              f"({len(classes)} classes) equal the type tables' crc, {bad} differ, "
              f"{sum(missing.values())} without a type table")
    return bad


def cmd_dump(args, types, enums):
    locale = load_locale(args.locale) if args.locale else None
    files = list_files(args.bin_dir, args.classes)
    os.makedirs(args.out_dir, exist_ok=True)
    summary, items = {}, {}
    for cls in sorted(files):
        t = types.get(cls)
        if t is None:
            summary[cls] = {"files": len(files[cls]), "error": "no type table"}
            continue
        inst, errors = {}, {}
        for name, path in files[cls]:
            try:
                obj, refs = parse_file(path, t, types, enums, locale)
                if refs:
                    obj["_badref"] = [list(x) for x in refs]
                inst[name] = obj
            except ParseError as e:
                errors[name] = str(e)
        with open(os.path.join(args.out_dir, f"{t.name}.json"), "w", encoding="utf-8") as f:
            json.dump({"class": t.name, "count": len(inst), "errors": errors, "instances": inst}, f,
                      indent=1, ensure_ascii=False)
        summary[t.name] = {"files": len(files[cls]), "parsed": len(inst), "errors": len(errors)}
        if t.name == "CollectionItem":
            for name, obj in inst.items():
                cid = obj.get("catalogID")
                items[f"0x{cid:08x}"] = {"item": f"{name}.CollectionItem", "decimal": cid,
                                         "type": obj.get("type"), "parentAsset": obj.get("parentAsset"),
                                         "specialStatus": obj.get("specialStatus")}
    if items:
        with open(os.path.join(args.out_dir, "catalog_ids.json"), "w", encoding="utf-8") as f:
            json.dump(dict(sorted(items.items())), f, indent=1, ensure_ascii=False)
    with open(os.path.join(args.out_dir, "_summary.json"), "w", encoding="utf-8") as f:
        json.dump(summary, f, indent=1)
    n = sum(v.get("parsed", 0) for v in summary.values())
    print(f"wrote {len(summary)} class file(s), {n} object(s), {len(items)} catalog id(s) to {args.out_dir}")
    return 0


def cmd_show(args, types, enums):
    locale = load_locale(args.locale) if args.locale else None
    fn = args.asset if args.asset.lower().endswith(".bin") else args.asset + ".bin"
    path = os.path.join(args.bin_dir, fn)
    _, cls = class_of(fn)
    obj, refs = parse_file(path, types[cls], types, enums, locale)
    if refs:
        obj["_badref"] = [list(x) for x in refs]
    print(json.dumps(obj, indent=1, ensure_ascii=False))
    return 0


def cmd_fields(args, types, enums):
    cls = args.cls.lower()
    files = list_files(args.bin_dir, [cls])[cls]
    t = types[cls]
    vals = defaultdict(Counter)
    for name, path in files:
        obj, _ = parse_file(path, t, types, enums)
        for m in t.members:
            vals[m.name][json.dumps(obj[m.name], ensure_ascii=False)[:80]] += 1
    print(f"{t.name}: {len(files)} files, size {t.size:#x}")
    for m in t.members:
        c = vals[m.name]
        ex = "; ".join(f"{v} x{k}" for v, k in c.most_common(3))
        print(f"  {m.index:2} +0x{m.offset:03x} {m.name:28} {m.type:12} distinct {len(c):4}  {ex}")
    return 0


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--types", default=DEFAULT_TYPES)
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("check")
    p.add_argument("bin_dir")
    p.add_argument("--class", dest="classes", nargs="*")
    p.add_argument("-v", "--verbose", action="store_true")
    p = sub.add_parser("dump")
    p.add_argument("bin_dir")
    p.add_argument("out_dir")
    p.add_argument("--class", dest="classes", nargs="*")
    p.add_argument("--locale")
    p = sub.add_parser("show")
    p.add_argument("bin_dir")
    p.add_argument("asset")
    p.add_argument("--locale")
    p = sub.add_parser("fields")
    p.add_argument("bin_dir")
    p.add_argument("cls")
    args = ap.parse_args(argv[1:])
    types, enums = load_types(args.types)
    return {"check": cmd_check, "dump": cmd_dump, "show": cmd_show, "fields": cmd_fields}[args.cmd](args, types, enums)


if __name__ == "__main__":
    sys.exit(main(sys.argv))
