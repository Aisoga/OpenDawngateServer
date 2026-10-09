"""Reader and writer for EA/Maxis DBPF 3.0 ".package" files (same container family as SimCity 2013).

Writing: `rebuild(pkg, {key: data})` returns a new package with those entries replaced (stored
uncompressed, comp 0x0000, like most of Config_Ship.package); every other entry keeps its raw bytes.

Usage:
    python -I re/scripts/dbpf.py list    <file.package>
    python -I re/scripts/dbpf.py summary <file.package> [...]
    python -I re/scripts/dbpf.py extract <file.package> <out_dir>
"""
import os
import struct
import sys
import zlib
from collections import Counter

COMP_NONE, COMP_ZLIB, COMP_REFPACK, COMP_REFPACK_STREAM, COMP_DELETED = 0x0000, 0x5A42, 0xFFFF, 0xFFFE, 0xFFE0


class Entry:
    __slots__ = ("type", "group", "instance", "offset", "csize", "usize", "comp", "committed")

    def key(self):
        return f"{self.type:08X}-{self.group:08X}-{self.instance:016X}"


def refpack_decompress(src):
    """EA RefPack/QFS decompression."""
    pos = 0
    flags = src[pos]; magic = src[pos + 1]; pos += 2
    if magic != 0xFB:
        raise ValueError(f"not RefPack (magic {magic:#x})")
    width = 4 if flags & 0x80 else 3
    if flags & 0x01:
        pos += width  # compressed size, unused
    usize = int.from_bytes(src[pos:pos + width], "big"); pos += width
    out = bytearray()
    n = len(src)
    while pos < n:
        b0 = src[pos]
        if b0 <= 0x7F:
            b1 = src[pos + 1]; pos += 2
            plain = b0 & 3; copy = ((b0 & 0x1C) >> 2) + 3; off = ((b0 & 0x60) << 3) + b1 + 1
        elif b0 <= 0xBF:
            b1, b2 = src[pos + 1], src[pos + 2]; pos += 3
            plain = (b1 >> 6) & 3; copy = (b0 & 0x3F) + 4; off = ((b1 & 0x3F) << 8) + b2 + 1
        elif b0 <= 0xDF:
            b1, b2, b3 = src[pos + 1], src[pos + 2], src[pos + 3]; pos += 4
            plain = b0 & 3; copy = ((b0 & 0x0C) << 6) + b3 + 5; off = ((b0 & 0x10) << 12) + (b1 << 8) + b2 + 1
        elif b0 <= 0xFB:
            pos += 1
            plain = ((b0 & 0x1F) << 2) + 4; copy = 0; off = 0
        else:
            pos += 1
            plain = b0 & 3
            out += src[pos:pos + plain]
            break
        out += src[pos:pos + plain]; pos += plain
        start = len(out) - off
        for i in range(copy):  # byte-wise: copies may overlap
            out.append(out[start + i])
    if len(out) != usize:
        raise ValueError(f"RefPack size mismatch {len(out)} != {usize}")
    return bytes(out)


class Package:
    def __init__(self, path):
        self.path = path
        with open(path, "rb") as f:
            self.data = f.read()
        d = self.data
        if d[:4] != b"DBPF":
            raise ValueError("not a DBPF file")
        self.major, self.minor = struct.unpack_from("<II", d, 4)
        count, = struct.unpack_from("<I", d, 0x24)
        self.index_size, = struct.unpack_from("<I", d, 0x2C)
        self.index_version, = struct.unpack_from("<I", d, 0x3C)
        index_offset, = struct.unpack_from("<Q", d, 0x40)
        self.entries = self._read_index(index_offset, count)

    def _read_index(self, pos, count):
        d = self.data
        flags, = struct.unpack_from("<I", d, pos); pos += 4
        self.index_flags = flags
        const = {}
        for bit, name in ((1, "type"), (2, "group"), (4, "insthi")):
            if flags & bit:
                const[name], = struct.unpack_from("<I", d, pos); pos += 4
        entries = []
        for _ in range(count):
            e = Entry()
            vals = {}
            for name in ("type", "group", "insthi"):
                if name in const:
                    vals[name] = const[name]
                else:
                    vals[name], = struct.unpack_from("<I", d, pos); pos += 4
            instlo, e.offset, csize, e.usize, e.comp, e.committed = struct.unpack_from("<IIIIHH", d, pos)
            pos += 20
            e.type, e.group = vals["type"], vals["group"]
            e.instance = (vals["insthi"] << 32) | instlo
            e.csize = csize & 0x7FFFFFFF
            entries.append(e)
        return entries

    def read(self, e):
        raw = self.data[e.offset:e.offset + e.csize]
        if e.comp == COMP_NONE or e.csize == e.usize and e.comp != COMP_ZLIB:
            return raw
        if e.comp in (COMP_REFPACK, COMP_REFPACK_STREAM):
            return refpack_decompress(raw)
        if e.comp == COMP_ZLIB:
            return zlib.decompress(raw)
        if e.comp == COMP_DELETED:
            return b""
        raise ValueError(f"unknown compression {e.comp:#x}")


HEADER_SIZE = 0x60


def rebuild(pkg, replacements):
    """Return the bytes of `pkg` with the entries in `replacements` ({Entry.key(): bytes}) replaced.

    Replaced entries are stored uncompressed (comp 0x0000). The others keep their raw (possibly
    compressed) bytes, flags and order. Layout as in the originals: 0x60-byte header, entry data,
    index at the end. Only the header's entry count (0x24), index size (0x2C) and index offset (0x40)
    change."""
    missing = set(replacements) - {e.key() for e in pkg.entries}
    if missing:
        raise KeyError(f"not in {pkg.path}: {sorted(missing)}")
    body = bytearray()
    placed = []  # (entry, offset, stored size, uncompressed size, comp)
    for e in sorted(pkg.entries, key=lambda e: e.offset):
        new = replacements.get(e.key())
        if new is None:
            raw = pkg.data[e.offset:e.offset + e.csize]
            placed.append((e, HEADER_SIZE + len(body), len(raw), e.usize, e.comp))
        else:
            raw = bytes(new)
            placed.append((e, HEADER_SIZE + len(body), len(raw), len(raw), COMP_NONE))
        body += raw
    order = {id(e): i for i, e in enumerate(pkg.entries)}  # keep the original index order
    placed.sort(key=lambda p: order[id(p[0])])

    flags = pkg.index_flags if pkg.entries else 0
    first = pkg.entries[0] if pkg.entries else None
    const = {"type": first.type, "group": first.group, "insthi": first.instance >> 32} if first else {}
    index = bytearray(struct.pack("<I", flags))
    for bit, name in ((1, "type"), (2, "group"), (4, "insthi")):
        if flags & bit:
            index += struct.pack("<I", const[name])
    for e, off, size, usize, comp in placed:
        for bit, name, val in ((1, "type", e.type), (2, "group", e.group), (4, "insthi", e.instance >> 32)):
            if flags & bit:
                if val != const[name]:
                    raise ValueError(f"{e.key()}: {name} differs from the index constant")
            else:
                index += struct.pack("<I", val)
        index += struct.pack("<IIIIHH", e.instance & 0xFFFFFFFF, off, size | 0x80000000, usize, comp,
                             e.committed)

    header = bytearray(pkg.data[:HEADER_SIZE])
    struct.pack_into("<I", header, 0x24, len(placed))
    struct.pack_into("<I", header, 0x2C, len(index))
    struct.pack_into("<Q", header, 0x40, HEADER_SIZE + len(body))
    return bytes(header + body + index)


def sniff(blob):
    head = blob[:16]
    if head[:4].isascii() and all(32 <= c < 127 for c in head[:4]):
        return head[:4].decode()
    if blob[:3] == b"\xef\xbb\xbf" or (blob[:1] in (b"<", b"{", b"[", b"#") ):
        return "text"
    return head[:4].hex()


def main(argv):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    cmd = argv[1]
    if cmd == "list":
        p = Package(argv[2])
        print(f"DBPF {p.major}.{p.minor} index v{p.index_version}, {len(p.entries)} entries")
        for e in p.entries:
            print(e.key(), f"off={e.offset:#x} c={e.csize} u={e.usize} comp={e.comp:#06x}", sniff(p.read(e)))
    elif cmd == "summary":
        for path in argv[2:]:
            p = Package(path)
            types = Counter()
            magics = {}
            for e in p.entries:
                types[e.type] += 1
                if e.type not in magics:
                    try:
                        magics[e.type] = sniff(p.read(e))
                    except Exception as ex:  # keep going on unknown formats
                        magics[e.type] = f"err:{ex}"
            comps = Counter(f"{e.comp:#06x}" for e in p.entries)
            print(f"== {os.path.basename(path)}: DBPF {p.major}.{p.minor}, {len(p.entries)} entries, compression {dict(comps)}")
            for t, c in types.most_common():
                print(f"   type {t:08X} x{c:<5} first-magic={magics[t]}")
    elif cmd == "extract":
        p = Package(argv[2])
        out = argv[3]
        os.makedirs(out, exist_ok=True)
        for e in p.entries:
            with open(os.path.join(out, e.key() + ".bin"), "wb") as f:
                f.write(p.read(e))
        print(f"extracted {len(p.entries)} entries to {out}")
    else:
        print(__doc__)


if __name__ == "__main__":
    main(sys.argv)
