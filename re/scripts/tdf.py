"""Blaze TDF (tagged data format) codec, enough to decode requests and captures and to build simple
replies. Research tool: tdf_tables.py imports it, and tdf_golden.py uses it to produce the golden bytes
that pin the Go server's codec (server/internal/tdf is the port the server runs).

Wire format of a field: 3-byte label (four 6-bit characters, MSB first, char = value + 0x20),
1-byte type, then the value. Struct members follow one another until a 0x00 byte.
Integers are varints: first byte = continue bit 0x80 | sign bit 0x40 | 6 value bits,
following bytes = continue bit | 7 value bits.
A union inside a list (HNET, a list of NetworkAddress) has no header: the list's element type is struct,
and each element is the member byte (0x7F = unset), then the member's value without its VALU tag (for a
struct member: its members and 0x00). The client's union decoder 0x00C6FB50 reads it that way when the
header flag is off (list elements), and its encoder wrote HNET so in b8_run4. The decoder below takes a
struct element whose first byte is 0x01..0x7F as such a union (a member tag starts at 0x84 or above);
member 0 can't be told from an empty struct and decodes as one.
"""
import struct

INT, STRING, BLOB, STRUCT, LIST, MAP, UNION, INTLIST, OBJTYPE, OBJID, FLOAT, TIMEVALUE = range(12)
TYPE_NAMES = ["int", "string", "blob", "struct", "list", "map", "union", "intlist",
              "objtype", "objid", "float", "timevalue"]
UNION_UNSET = 0x7F


# ---------------------------------------------------------------- primitives
def encode_label(label):
    v = 0
    for i, c in enumerate(label.ljust(4, "\0")[:4]):
        v |= (((ord(c) - 0x20) & 0x3F) if c != "\0" else 0) << (18 - 6 * i)
    return v.to_bytes(3, "big")


def decode_label(b):
    v = int.from_bytes(b, "big")
    return "".join(chr(((v >> (18 - 6 * i)) & 0x3F) + 0x20) for i in range(4) if (v >> (18 - 6 * i)) & 0x3F)


def encode_varint(n):
    neg = n < 0
    n = -n if neg else n
    first = (n & 0x3F) | (0x40 if neg else 0)
    n >>= 6
    out = bytearray([first | (0x80 if n else 0)])
    while n:
        b = n & 0x7F
        n >>= 7
        out.append(b | (0x80 if n else 0))
    return bytes(out)


class Reader:
    def __init__(self, data, pos=0):
        self.d, self.p = data, pos

    def byte(self):
        v = self.d[self.p]
        self.p += 1
        return v

    def varint(self):
        b = self.byte()
        neg, n, shift = b & 0x40, b & 0x3F, 6
        while b & 0x80:
            b = self.byte()
            n |= (b & 0x7F) << shift
            shift += 7
        return -n if neg else n

    def take(self, n):
        v = self.d[self.p:self.p + n]
        if len(v) < n:
            raise EOFError("truncated TDF")
        self.p += n
        return v

    # values -----------------------------------------------------------
    def value(self, t):
        if t in (INT, TIMEVALUE):
            return self.varint()
        if t == STRING:
            n = self.varint()
            return self.take(n)[:-1].decode("utf-8", "replace") if n else ""
        if t == BLOB:
            return self.take(self.varint())
        if t == STRUCT:
            return self.struct()
        if t == LIST:
            et = self.byte()
            return [self.list_item(et) for _ in range(self.varint())]
        if t == MAP:
            kt, vt = self.byte(), self.byte()
            return {self._key(self.value(kt)): self.value(vt) for _ in range(self.varint())}
        if t == UNION:
            member = self.byte()
            if member == UNION_UNSET:
                return ("union", member, None)
            label, ft = self.header()
            return ("union", member, {label: self.value(ft)})
        if t == INTLIST:
            return [self.varint() for _ in range(self.varint())]
        if t == OBJTYPE:
            return (self.varint(), self.varint())
        if t == OBJID:
            return (self.varint(), self.varint(), self.varint())
        if t == FLOAT:
            return struct.unpack(">f", self.take(4))[0]
        raise ValueError(f"unknown TDF type {t} at {self.p - 1}")

    def list_item(self, t):
        """A list element; a struct element starting with 0x01..0x7F is a union (see the module doc)."""
        if t == STRUCT and self.p < len(self.d) and 0 < self.d[self.p] <= UNION_UNSET:
            member = self.byte()
            return ("union", member, None if member == UNION_UNSET else self.struct())
        return self.value(t)

    @staticmethod
    def _key(k):
        return tuple(k) if isinstance(k, list) else k

    def header(self):
        label = decode_label(self.take(3))
        return label, self.byte()

    def struct(self):
        fields = {}
        while self.p < len(self.d):
            if self.d[self.p] == 0:
                self.p += 1
                break
            label, t = self.header()
            fields[label] = self.value(t)
        return fields


def decode(payload):
    """Decode a packet payload (a top-level struct without terminator)."""
    r = Reader(payload)
    fields = {}
    while r.p < len(payload):
        label, t = r.header()
        fields[label] = r.value(t)
    return fields


# ---------------------------------------------------------------- encoder
class Writer:
    """Build a payload: w.int("SECU", 0); with w.struct("VALU"): ...; bytes(w)."""

    def __init__(self):
        self.out = bytearray()

    def __bytes__(self):
        return bytes(self.out)

    def _hdr(self, label, t):
        self.out += encode_label(label) + bytes([t])

    def int(self, label, v):
        self._hdr(label, INT)
        self.out += encode_varint(v)
        return self

    def string(self, label, s):
        b = s.encode("utf-8") + b"\0"
        self._hdr(label, STRING)
        self.out += encode_varint(len(b)) + b
        return self

    def blob(self, label, b):
        self._hdr(label, BLOB)
        self.out += encode_varint(len(b)) + b
        return self

    def struct(self, label):
        self._hdr(label, STRUCT)
        return _Closer(self.out, b"\0")

    def union(self, label, member):
        """Open a union; write exactly one field (conventionally labelled VALU) inside it."""
        self._hdr(label, UNION)
        self.out.append(member)
        return _Closer(self.out, b"")

    def list(self, label, elem_type, items):
        """List of INT, STRING or STRUCT elements (a STRUCT element is a Writer holding its members, or a
        UnionItem for a list of unions)."""
        self._hdr(label, LIST)
        self.out.append(elem_type)
        self.out += encode_varint(len(items))
        for item in items:
            self._raw(elem_type, item)
        return self

    def map(self, label, key_type, value_type, items):
        """Map from INT/STRING keys to INT, STRING or STRUCT values; `items` is a dict or (key, value) pairs."""
        pairs = list(items.items()) if isinstance(items, dict) else list(items)
        self._hdr(label, MAP)
        self.out += bytes([key_type, value_type]) + encode_varint(len(pairs))
        for k, v in pairs:
            self._raw(key_type, k)
            self._raw(value_type, v)
        return self

    def _raw(self, t, v):
        if t == INT:
            self.out += encode_varint(v)
        elif t == STRING:
            b = v.encode("utf-8") + b"\0"
            self.out += encode_varint(len(b)) + b
        elif t == STRUCT and isinstance(v, UnionItem):
            self.out.append(v.member)
            if v.member != UNION_UNSET:
                self.out += bytes(v.body) + b"\0"
        elif t == STRUCT:
            self.out += bytes(v) + b"\0"
        else:
            raise ValueError(f"unsupported element type {t}")


class UnionItem:
    """A list element of a union class: member byte, then (unless UNION_UNSET) the struct member's
    members from `body` (a Writer) and 0x00. See the module doc."""

    def __init__(self, member, body=None):
        self.member, self.body = member, body if body is not None else Writer()


class _Closer:
    def __init__(self, out, trailer):
        self.out, self.trailer = out, trailer

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.out += self.trailer


def pretty(value, indent=0):
    pad = "  " * indent
    if isinstance(value, dict):
        return "\n".join(f"{pad}{k}: " + (("\n" + pretty(v, indent + 1)) if isinstance(v, (dict, list, tuple)) and v
                                         else repr(v)) for k, v in value.items())
    if isinstance(value, tuple) and value and value[0] == "union":
        return f"{pad}<union member {value[1]}>" + (("\n" + pretty(value[2], indent + 1)) if value[2] else "")
    if isinstance(value, list):
        return "\n".join(f"{pad}- " + (("\n" + pretty(v, indent + 1)) if isinstance(v, (dict, list)) else repr(v))
                         for v in value)
    return pad + repr(value)
