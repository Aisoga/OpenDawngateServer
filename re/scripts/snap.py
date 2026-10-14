"""Reader and unpacker for Dawngate's ".snap" archives (magic "pans", 91 files in game/Data).

Format (little-endian; see docs/SNAP.md):
    header  0x20 bytes: "pans", u16 version 0x0200, u16 1, u32 entry count, u32 name-table size,
            16 zero bytes
    data    entry payloads back to back from 0x20, in index order, no padding
    index   count x 32 bytes, directly before the name table:
            u32 name offset (into the name table), u32 size, u32 stored size, u32 0, u32 0, u32 0,
            u32 data offset (from the file start), u32 flags (bit 0 = stored raw, else Snappy)
    names   NUL-terminated paths ("script/preload.txt"), the last name-table-size bytes of the file

Payloads are raw Snappy blocks (google/snappy format_description.txt: varint size, then literal and
copy tags), hence the name. Flag 1 means stored as is (audio.snap's .ogg/.bnk). No name hashing.

Usage:
    python -I re/scripts/snap.py list    <file.snap>
    python -I re/scripts/snap.py summary <file.snap|dir> [...]
    python -I re/scripts/snap.py verify  <file.snap|dir> [...]
    python -I re/scripts/snap.py extract <file.snap|dir> [...] <out_dir>
    python -I re/scripts/snap.py diff    <old.snap> <new.snap>
    python -I re/scripts/snap.py pack    <out.snap> <dir> [--raw] [--stored-ext .ogg,.wem] [--base <archive path prefix>]

`summary` reads only the index. `verify` decompresses every entry and checks the size. `extract`
writes <out_dir>/<archive name>/<entry path> for each archive and prints ok/failed counts. A
directory argument means every *.snap in it (not recursive). `diff` compares two archives entry by
entry (by decompressed content). `pack` writes every file below <dir> as an entry named by
its path relative to <dir> (with '/', in sorted order, optionally under --base), Snappy-compressed
(or stored raw with --raw or for the --stored-ext extensions), in the layout above; the result
reads back with `verify`.
"""
import hashlib
import os
import struct
import sys
from collections import Counter

MAGIC = b"pans"
HEADER_SIZE = 0x20
ENTRY_SIZE = 32
FLAG_STORED = 0x1


class SnapError(ValueError):
    pass


def snappy_decompress(src, expected=None):
    """Decompress one raw Snappy block (no framing). Raises SnapError on malformed input."""
    n = len(src)
    pos = 0
    usize = 0
    shift = 0
    while True:
        if pos >= n or shift > 28:
            raise SnapError("bad Snappy length varint")
        b = src[pos]; pos += 1
        usize |= (b & 0x7F) << shift
        if b < 0x80:
            break
        shift += 7
    if expected is not None and usize != expected:
        raise SnapError(f"Snappy length {usize} != index size {expected}")
    out = bytearray()
    while pos < n:
        tag = src[pos]; pos += 1
        kind = tag & 3
        if kind == 0:  # literal
            ln = tag >> 2
            if ln >= 60:
                nb = ln - 59
                ln = int.from_bytes(src[pos:pos + nb], "little"); pos += nb
            ln += 1
            if pos + ln > n:
                raise SnapError("literal runs past the end")
            out += src[pos:pos + ln]; pos += ln
            continue
        if kind == 1:  # copy, 1-byte offset
            ln = ((tag >> 2) & 7) + 4
            off = ((tag >> 5) << 8) | src[pos]; pos += 1
        elif kind == 2:  # copy, 2-byte offset
            ln = (tag >> 2) + 1
            off = src[pos] | (src[pos + 1] << 8); pos += 2
        else:  # copy, 4-byte offset
            ln = (tag >> 2) + 1
            off = int.from_bytes(src[pos:pos + 4], "little"); pos += 4
        have = len(out)
        if off == 0 or off > have:
            raise SnapError(f"copy offset {off} outside {have} bytes of output")
        start = have - off
        if off >= ln:
            out += out[start:start + ln]
        else:  # overlapping copy repeats the last `off` bytes
            pat = out[start:]
            q, r = divmod(ln, off)
            out += pat * q + pat[:r]
    if len(out) != usize:
        raise SnapError(f"decompressed {len(out)} bytes, header says {usize}")
    return bytes(out)


def snappy_compress(src):
    """One raw Snappy block: greedy LZ77 with a 4-byte hash table (64 KB windows like the
    reference encoder, so every copy offset fits a 2-byte copy tag)."""
    n = len(src)
    out = bytearray()
    v = n
    while True:
        b = v & 0x7F
        v >>= 7
        out.append(b | (0x80 if v else 0))
        if not v:
            break

    def literal(a, b):
        while a < b:
            ln = min(b - a, 1 << 16)
            k = ln - 1
            if k < 60:
                out.append(k << 2)
            elif k < 256:
                out.extend((60 << 2, k))
            else:
                out.extend((61 << 2, k & 0xFF, k >> 8))
            out.extend(src[a:a + ln])
            a += ln

    def copy(off, ln):
        while ln > 0:
            k = min(ln, 64)
            if ln - k < 4 and ln > 64:  # leave at least 4 for the last copy
                k = ln - 4
            if 4 <= k <= 11 and off < 2048:
                out.extend((1 | ((k - 4) << 2) | ((off >> 8) << 5), off & 0xFF))
            else:
                out.extend((2 | ((k - 1) << 2), off & 0xFF, off >> 8))
            ln -= k

    for block in range(0, n, 1 << 16):
        end = min(n, block + (1 << 16))
        table = {}
        i = block
        lit = block
        while i + 4 <= end:
            key = src[i:i + 4]
            cand = table.get(key)
            table[key] = i
            if cand is not None and i - cand < 65536:
                ln = 4
                while i + ln < end and src[cand + ln] == src[i + ln]:
                    ln += 1
                literal(lit, i)
                copy(i - cand, ln)
                i += ln
                lit = i
                continue
            i += 1
        literal(lit, end)
    return bytes(out)


def pack(out_path, files, raw=False, stored_ext=()):
    """files: [(archive name, bytes)] in index order."""
    data = bytearray()
    index = bytearray()
    names = bytearray()
    for name, blob in files:
        stored = raw or name.lower().endswith(tuple(stored_ext))
        payload = blob if stored else snappy_compress(blob)
        index += struct.pack("<8I", len(names), len(blob), len(payload), 0, 0, 0,
                             HEADER_SIZE + len(data), FLAG_STORED if stored else 0)
        names += name.encode("utf-8") + b"\0"
        data += payload
    head = MAGIC + struct.pack("<HHII", 0x200, 1, len(files), len(names)) + b"\0" * 16
    with open(out_path, "wb") as f:
        f.write(head + data + index + names)


def cmd_pack(argv):
    args = list(argv)
    raw = "--raw" in args
    if raw:
        args.remove("--raw")
    stored_ext, base = (), ""
    if "--stored-ext" in args:
        i = args.index("--stored-ext")
        stored_ext = tuple(x.strip().lower() for x in args[i + 1].split(",") if x.strip())
        del args[i:i + 2]
    if "--base" in args:
        i = args.index("--base")
        base = args[i + 1].strip("/") + "/"
        del args[i:i + 2]
    out_path, root = args
    files = []
    for dp, dn, fn in os.walk(root):
        for f in fn:
            full = os.path.join(dp, f)
            name = base + os.path.relpath(full, root).replace(os.sep, "/")
            safe_join(root, name[len(base):])
            files.append((name, full))
    files.sort(key=lambda x: x[0])
    names = Counter(n.lower() for n, _ in files)
    if any(c > 1 for c in names.values()):
        raise SnapError("duplicate names (case-insensitive)")
    pack(out_path, [(n, open(p, "rb").read()) for n, p in files], raw, stored_ext)
    s = Snap(out_path)
    for e, (n, p) in zip(s.entries, files):
        if s.read(e) != open(p, "rb").read():
            raise SnapError(f"{n}: does not read back")
    notes = s.layout_notes()
    print(f"wrote {out_path}: {len(files)} entries, {os.path.getsize(out_path)} bytes"
          + (f"; NOTES: {notes}" if notes else "; layout as shipped, every entry reads back"))
    return 0


class Entry:
    __slots__ = ("index", "name", "name_offset", "size", "stored_size", "reserved", "offset", "flags")

    @property
    def stored(self):
        return bool(self.flags & FLAG_STORED)


class Snap:
    def __init__(self, path):
        self.path = path
        with open(path, "rb") as f:
            self.data = d = f.read()
        if len(d) < HEADER_SIZE or d[:4] != MAGIC:
            raise SnapError(f"{path}: not a .snap archive")
        self.version, self.unknown, count, names_size = struct.unpack_from("<HHII", d, 4)
        self.header_tail = d[0x10:HEADER_SIZE]
        names_off = len(d) - names_size
        index_off = names_off - count * ENTRY_SIZE
        if index_off < HEADER_SIZE:
            raise SnapError(f"{path}: index ({count} entries) and names ({names_size} B) exceed the file")
        self.index_offset, self.names_offset = index_off, names_off
        names = d[names_off:]
        self.entries = []
        for i in range(count):
            e = Entry()
            e.index = i
            vals = struct.unpack_from("<8I", d, index_off + i * ENTRY_SIZE)
            e.name_offset, e.size, e.stored_size = vals[0:3]
            e.reserved = vals[3:6]
            e.offset, e.flags = vals[6], vals[7]
            end = names.find(b"\0", e.name_offset)
            if e.name_offset >= len(names) or end < 0:
                raise SnapError(f"{path}: entry {i} name offset {e.name_offset:#x} outside the name table")
            e.name = names[e.name_offset:end].decode("utf-8", "replace")
            if e.offset < HEADER_SIZE or e.offset + e.stored_size > index_off:
                raise SnapError(f"{path}: entry {i} ({e.name}) data outside the data area")
            self.entries.append(e)

    def raw(self, e):
        return self.data[e.offset:e.offset + e.stored_size]

    def read(self, e):
        raw = self.raw(e)
        if e.stored:
            if e.stored_size != e.size:
                raise SnapError(f"{e.name}: stored entry with size {e.size} != stored size {e.stored_size}")
            return raw
        return snappy_decompress(raw, e.size)

    def layout_notes(self):
        """Things that deviate from the layout described in the docstring (empty list = as expected)."""
        notes = []
        if self.version != 0x200 or self.unknown != 1:
            notes.append(f"header version {self.version:#x}/{self.unknown}")
        if any(self.header_tail):
            notes.append(f"header bytes 0x10-0x1F not zero: {self.header_tail.hex()}")
        pos = HEADER_SIZE
        for e in self.entries:
            if e.offset != pos:
                notes.append(f"entry {e.index} ({e.name}) at {e.offset:#x}, expected {pos:#x}")
                break
            pos = e.offset + e.stored_size
        else:
            if self.entries and pos != self.index_offset:
                notes.append(f"{self.index_offset - pos} bytes between data and index")
        if any(any(e.reserved) for e in self.entries):
            notes.append("non-zero reserved index fields")
        if any(e.flags & ~FLAG_STORED for e in self.entries):
            notes.append(f"unknown flags {sorted({e.flags for e in self.entries})}")
        expected = 0
        for e in self.entries:
            if e.name_offset != expected:
                notes.append(f"name table not in index order at entry {e.index}")
                break
            expected += len(e.name.encode("utf-8")) + 1
        else:
            if self.entries and expected != len(self.data) - self.names_offset:
                notes.append("name table has unused bytes")
        names = Counter(e.name.lower() for e in self.entries)
        dups = [n for n, c in names.items() if c > 1]
        if dups:
            notes.append(f"{len(dups)} duplicate names (case-insensitive), e.g. {dups[0]}")
        return notes


def ext_of(name):
    base = name.rsplit("/", 1)[-1]
    return base.rsplit(".", 1)[-1].lower() if "." in base else "(none)"


def sniff(blob):
    head = blob[:4]
    if len(head) == 4 and all(32 <= c < 127 for c in head):
        return head.decode()
    if blob[:3] == b"\xef\xbb\xbf":
        return "utf8-bom"
    return head.hex() or "(empty)"


def safe_join(root, name):
    """Map an archive path to a file below root; refuse absolute paths and '..'."""
    parts = [p for p in name.replace("\\", "/").split("/") if p not in ("", ".")]
    if not parts or any(p == ".." or ":" in p for p in parts):
        raise SnapError(f"unsafe entry path {name!r}")
    path = os.path.join(root, *parts)
    if os.path.commonpath([os.path.abspath(root), os.path.abspath(path)]) != os.path.abspath(root):
        raise SnapError(f"unsafe entry path {name!r}")
    return path


def expand(args):
    paths = []
    for a in args:
        if os.path.isdir(a):
            paths += sorted(os.path.join(a, f) for f in os.listdir(a) if f.lower().endswith(".snap"))
        else:
            paths.append(a)
    return paths


def human(n):
    for unit in ("B", "KB", "MB", "GB"):
        if n < 1024 or unit == "GB":
            return f"{n:.0f} {unit}" if unit == "B" else f"{n:.1f} {unit}"
        n /= 1024


def cmd_list(path):
    s = Snap(path)
    print(f"{os.path.basename(path)}: {len(s.entries)} entries, version {s.version:#x}")
    for e in s.entries:
        try:
            kind = sniff(s.read(e)[:16])
        except SnapError as ex:
            kind = f"ERROR {ex}"
        mode = "stored" if e.stored else "snappy"
        print(f"{e.size:>10} {e.stored_size:>10} {mode} {kind:<8} {e.name}")
    for note in s.layout_notes():
        print("note:", note)


def cmd_summary(paths):
    total = Counter()
    for path in expand(paths):
        s = Snap(path)
        exts = Counter(ext_of(e.name) for e in s.entries)
        size = sum(e.size for e in s.entries)
        stored = sum(e.stored for e in s.entries)
        top = ", ".join(f"{k} {v}" for k, v in exts.most_common(6))
        notes = s.layout_notes()
        print(f"{os.path.basename(path):<26} {len(s.entries):>5} entries {human(size):>9}"
              f" (stored raw: {stored}) {top}" + (f"  NOTES: {'; '.join(notes)}" if notes else ""))
        total["archives"] += 1
        total["entries"] += len(s.entries)
        total["bytes"] += size
    print(f"total: {total['archives']} archives, {total['entries']} entries, {human(total['bytes'])} unpacked")


def run_all(paths, out_dir=None):
    ok = failed = 0
    failures = []
    for path in expand(paths):
        try:
            s = Snap(path)
        except SnapError as ex:
            failures.append(str(ex))
            failed += 1
            continue
        base = os.path.splitext(os.path.basename(path))[0]
        a_ok = a_failed = 0
        for e in s.entries:
            try:
                blob = s.read(e)
                if out_dir is not None:
                    target = safe_join(os.path.join(out_dir, base), e.name)
                    os.makedirs(os.path.dirname(target), exist_ok=True)
                    with open(target, "wb") as f:
                        f.write(blob)
                a_ok += 1
            except SnapError as ex:
                failures.append(f"{base}: {e.name}: {ex}")
                a_failed += 1
        print(f"{base:<26} {a_ok:>5} ok {a_failed:>3} failed", flush=True)
        ok += a_ok
        failed += a_failed
    for f in failures:
        print("FAIL", f)
    verb = "extracted" if out_dir is not None else "verified"
    print(f"{verb}: {ok} entries ok, {failed} failed" + (f", into {out_dir}" if out_dir is not None else ""))
    return 1 if failed else 0


def cmd_diff(old_path, new_path):
    old, new = Snap(old_path), Snap(new_path)
    a = {e.name: e for e in old.entries}
    b = {e.name: e for e in new.entries}
    for name in sorted(set(a) - set(b)):
        print("only in old:", name)
    for name in sorted(set(b) - set(a)):
        print("only in new:", name)
    same = 0
    for name in [e.name for e in new.entries if e.name in a]:
        da, db = old.read(a[name]), new.read(b[name])
        if da == db:
            same += 1
        else:
            print(f"changed: {name} ({len(da)} -> {len(db)} B, sha256 "
                  f"{hashlib.sha256(da).hexdigest()[:12]} -> {hashlib.sha256(db).hexdigest()[:12]})")
    order = [e.name for e in old.entries if e.name in b] == [e.name for e in new.entries if e.name in a]
    print(f"{same} identical entries; index order {'same' if order else 'differs'}")


def main(argv):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    if len(argv) < 3:
        print(__doc__)
        return 2
    cmd = argv[1]
    if cmd == "list":
        cmd_list(argv[2])
    elif cmd == "summary":
        cmd_summary(argv[2:])
    elif cmd == "verify":
        return run_all(argv[2:])
    elif cmd == "extract" and len(argv) >= 4:
        return run_all(argv[2:-1], argv[-1])
    elif cmd == "pack" and len(argv) >= 4:
        return cmd_pack(argv[2:])
    elif cmd == "diff" and len(argv) == 4:
        cmd_diff(argv[2], argv[3])
    else:
        print(__doc__)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
