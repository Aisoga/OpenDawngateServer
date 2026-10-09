"""Spore/Darkspore binary property lists (DBPF type 00B1B104, e.g. Config_Ship.package).

Format (big-endian): u32 count, then per property: u32 id (FNV-1 hash of the lower-case name),
u16 type, u16 flags, value. If flags & 0x30, the value is an array: u32 count, u32 item size, items.
Strings (0x12 string8, 0x13 string16 UTF-16BE) are u32 character count + characters, also inside
arrays. Fixed sizes below were confirmed by parsing every 00B1B104 entry of Config_Ship.package and
Config_Pack.package to the last byte (vector3 0x31 is padded to 16 bytes; vector floats are LE).

Usage:
    python -I re/scripts/props.py dump <entry.bin>       # one line per property
    python -I re/scripts/props.py hash <name> [...]      # property ids
"""
import struct
import sys

STRING8, STRING16 = 0x12, 0x13
TYPE_NAMES = {0x01: "bool", 0x02: "int8", 0x03: "uint8", 0x05: "int16", 0x06: "uint16", 0x09: "int32",
              0x0A: "uint32", 0x0B: "int64", 0x0C: "uint64", 0x0D: "float", 0x12: "string8",
              0x13: "string16", 0x20: "key", 0x30: "vector2", 0x31: "vector3", 0x32: "colorRGB",
              0x33: "vector4", 0x34: "colorRGBA", 0x39: "bbox"}
FIXED = {0x01: 1, 0x02: 1, 0x03: 1, 0x05: 2, 0x06: 2, 0x09: 4, 0x0A: 4, 0x0B: 8, 0x0C: 8, 0x0D: 4,
         0x20: 12, 0x30: 8, 0x31: 16, 0x32: 16, 0x33: 16, 0x34: 16, 0x39: 24}


def fnv(name):
    """Property id: 32-bit FNV-1 of the lower-case name (Spore's hash())."""
    h = 0x811C9DC5
    for c in name.lower().encode():
        h = (h * 0x01000193) & 0xFFFFFFFF
        h ^= c
    return h


class Prop:
    __slots__ = ("id", "type", "flags", "raw")  # raw = value bytes after the 8-byte head

    def __init__(self, pid, typ, flags, raw):
        self.id, self.type, self.flags, self.raw = pid, typ, flags, raw

    @property
    def is_array(self):
        return bool(self.flags & 0x30)

    def string(self):
        """Value of a single string8/string16 property."""
        if self.type not in (STRING8, STRING16) or self.is_array:
            raise TypeError(f"{self.id:#010x} is not a single string")
        body = self.raw[4:]
        return body.decode("utf-16-be") if self.type == STRING16 else body.decode("latin-1")

    def set_string(self, s):
        if self.type not in (STRING8, STRING16) or self.is_array:
            raise TypeError(f"{self.id:#010x} is not a single string")
        body = s.encode("utf-16-be") if self.type == STRING16 else s.encode("latin-1")
        self.raw = struct.pack(">I", len(s)) + body

    def strings(self):
        """Items of a string8/string16 array property."""
        if self.type not in (STRING8, STRING16) or not self.is_array:
            raise TypeError(f"{self.id:#010x} is not a string array")
        n, = struct.unpack_from(">I", self.raw, 0)
        pos, out, wide = 8, [], self.type == STRING16
        for _ in range(n):
            k, = struct.unpack_from(">I", self.raw, pos)
            body = self.raw[pos + 4:pos + 4 + k * (2 if wide else 1)]
            out.append(body.decode("utf-16-be") if wide else body.decode("latin-1"))
            pos += 4 + len(body)
        return out

    def set_strings(self, items):
        """Replace the items of a string array; the item-size word (+4) is kept as it was."""
        self.strings()  # type check
        wide = self.type == STRING16
        body = b"".join(struct.pack(">I", len(s)) + (s.encode("utf-16-be") if wide else s.encode("latin-1"))
                        for s in items)
        self.raw = struct.pack(">I", len(items)) + self.raw[4:8] + body

    def int(self):
        if self.type not in (0x09, 0x0A) or self.is_array:
            raise TypeError(f"{self.id:#010x} is not a single int32/uint32")
        return struct.unpack(">i" if self.type == 0x09 else ">I", self.raw)[0]

    def set_int(self, v):
        self.int()  # type check
        self.raw = struct.pack(">i" if self.type == 0x09 else ">I", v)

    def __bytes__(self):
        return struct.pack(">IHH", self.id, self.type, self.flags) + self.raw


def _string_size(d, pos, typ):
    n, = struct.unpack_from(">I", d, pos)
    return 4 + n * (2 if typ == STRING16 else 1)


def parse(d):
    """Parse a binary property list into a list of Prop. Raises if it does not consume all bytes."""
    count, = struct.unpack_from(">I", d, 0)
    pos, out = 4, []
    for i in range(count):
        pid, typ, flags = struct.unpack_from(">IHH", d, pos)
        start = pos = pos + 8
        if flags & 0x30:
            n, size = struct.unpack_from(">II", d, pos)
            pos += 8
            if typ in (STRING8, STRING16):
                for _ in range(n):
                    pos += _string_size(d, pos, typ)
            else:
                pos += n * size
        elif typ in (STRING8, STRING16):
            pos += _string_size(d, pos, typ)
        elif typ in FIXED:
            pos += FIXED[typ]
        else:
            raise ValueError(f"property {i} ({pid:#010x}): unknown type {typ:#x} flags {flags:#x}")
        out.append(Prop(pid, typ, flags, bytes(d[start:pos])))
    if pos != len(d):
        raise ValueError(f"parsed {pos} of {len(d)} bytes")
    return out


def serialize(props):
    return struct.pack(">I", len(props)) + b"".join(bytes(p) for p in props)


def main(argv):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    if len(argv) >= 3 and argv[1] == "dump":
        for p in parse(open(argv[2], "rb").read()):
            t = TYPE_NAMES.get(p.type, f"{p.type:#x}")
            if p.is_array and p.type in (STRING8, STRING16):
                val = f"[array {struct.unpack_from('>I', p.raw)[0]}] {p.strings()!r}"
            elif p.is_array:
                val = f"[array {struct.unpack_from('>I', p.raw)[0]}] {p.raw[8:40].hex(' ')}"
            elif p.type in (STRING8, STRING16):
                val = repr(p.string())
            elif p.type in (0x09, 0x0A):
                val = str(p.int())
            elif p.type == 0x0D:
                val = str(struct.unpack(">f", p.raw)[0])
            else:
                val = p.raw.hex(" ")
            print(f"{p.id:#010x} {t:<9} {val}")
    elif len(argv) >= 3 and argv[1] == "hash":
        for name in argv[2:]:
            print(f"{fnv(name):#010x} {name}")
    else:
        print(__doc__)


if __name__ == "__main__":
    main(sys.argv)
