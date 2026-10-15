"""The server picker's login movie: the player's own login movie with our picker class added.

Start-Client's picker mode shows a server list on the game's login screen. The list is our own ActionScript class
(client_ui/serverpicker/ServerPicker.as, compiled by re/scripts/build_picker.py into four SWF tags,
server/internal/setup/moviepatch/picker_tags.hex). This tool reads the login movie from the player's own copy of
the game, game/Data/LoginScreen.snap, adds those four tags to the root timeline's first frame (before its first
ShowFrame; the game's own tags and bytecode stay as they are) and writes the result into an added archive,
game/Data/zz_ServerPicker.snap, which sorts after LoginScreen.snap and so replaces the movie's path. Nothing of the
game is shipped: the movie is made on the player's PC from the player's files.

The output is deterministic, so the setup's Go port (server/internal/setup/moviepatch) writes the same bytes:
  - the movie keeps its CFX header (version, and the u32 length as stored plus the tags' size); its body is a zlib
    stream of stored blocks (no compression; blocks of 65,535 bytes, the last one final), the archive compresses it;
  - the archive has one entry, the movie, Snappy-compressed by snap.py's encoder.
The picker's character id (2000) and depth (2000) must be free in the movie; a movie that has them, or has the
picker already, is refused.

Usage (python -I: the game's files are untrusted input):
    python -I re/scripts/patch_login_movie.py <game/Data/LoginScreen.snap> <out zz_ServerPicker.snap> [--tags <file>]
    python -I re/scripts/patch_login_movie.py --movie <LoginScreen_BG.gfx> <out .gfx> [--tags <file>]
"""
import os
import struct
import sys
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)  # -I drops the script folder
import snap  # noqa: E402

REPO = os.path.dirname(os.path.dirname(HERE))
TAGS = os.path.join(REPO, "server", "internal", "setup", "moviepatch", "picker_tags.hex")
MOVIE_PATH = "UI/Scaleform/LoginScreen/LoginScreen_BG.gfx"
SOURCE = "game/Data/LoginScreen.snap"
REL = "game/Data/zz_ServerPicker.snap"
CHAR_ID = 2000
DEPTH = 2000
CLASS_NAME = "ServerPicker"

# Tags whose first u16 is the character id they define (standard SWF and Scaleform's 1001-1009).
DEFINING = {2, 6, 7, 10, 11, 14, 20, 21, 22, 32, 33, 34, 35, 36, 37, 39, 46, 48, 60, 75,
            83, 84, 87, 90, 91, 1001, 1003, 1005, 1006, 1007, 1008, 1009}


class MovieError(ValueError):
    pass


def tags_of(buf, pos, end):
    """[(code, tag start, data start, data end)] of buf[pos:end] up to and including the first End tag."""
    out = []
    while pos < end:
        if pos + 2 > end:
            raise MovieError(f"a tag header runs past its container at {pos:#x}")
        (h,) = struct.unpack_from("<H", buf, pos)
        code, ln, hdr = h >> 6, h & 0x3F, 2
        if ln == 0x3F:
            if pos + 6 > end:
                raise MovieError(f"a long tag header runs past its container at {pos:#x}")
            (ln,) = struct.unpack_from("<I", buf, pos + 2)
            hdr = 6
        start = pos + hdr
        if start + ln > end:
            raise MovieError(f"tag {code} at {pos:#x} runs past its container")
        out.append((code, pos, start, start + ln))
        pos = start + ln
        if code == 0:
            break
    return out


def _cstring_end(buf, pos, end):
    i = buf.find(b"\0", pos, end)
    if i < 0:
        raise MovieError("a string without its NUL")
    return i + 1


def _pairs(buf, pos, end):
    """u16 count, then (u16 id, NUL-terminated name): ids and names."""
    (n,) = struct.unpack_from("<H", buf, pos)
    pos += 2
    out = []
    for _ in range(n):
        (cid,) = struct.unpack_from("<H", buf, pos)
        nxt = _cstring_end(buf, pos + 2, end)
        out.append((cid, buf[pos + 2:nxt - 1].decode("utf-8", "replace")))
        pos = nxt
    return out


def scan(body):
    """The movie body's character ids, class names, root depths and the offset of the root's first ShowFrame."""
    nbits = body[0] >> 3
    pos = (5 + 4 * nbits + 7) // 8 + 4
    ids, names, depths = set(), set(), set()
    first_show = None

    def walk(buf, start, end, root):
        nonlocal first_show
        for code, tpos, s, e in tags_of(buf, start, end):
            if code in DEFINING and e - s >= 2:
                ids.add(struct.unpack_from("<H", buf, s)[0])
            if code == 39 and e - s >= 4:
                walk(buf, s + 4, e, False)
            elif code in (56, 76):  # ExportAssets, SymbolClass
                for cid, name in _pairs(buf, s, e):
                    ids.add(cid)
                    names.add(name)
            elif code in (57, 71):  # ImportAssets, ImportAssets2
                p = _cstring_end(buf, s, e) + (2 if code == 71 else 0)
                ids.update(cid for cid, _ in _pairs(buf, p, e))
            if root:
                if code == 26 and e - s >= 3:
                    depths.add(struct.unpack_from("<H", buf, s + 1)[0])
                elif code == 70 and e - s >= 4:
                    depths.add(struct.unpack_from("<H", buf, s + 2)[0])
                elif code == 1 and first_show is None:
                    first_show = tpos

    walk(body, pos, len(body), True)
    return ids, names, depths, first_show


def zlib_stored(data):
    """A zlib stream of stored deflate blocks (65,535 bytes each, the last one final; Adler-32 at the end)."""
    out = bytearray(b"\x78\x01")
    n = len(data)
    i = 0
    while True:
        chunk = data[i:i + 0xFFFF]
        i += len(chunk)
        final = i >= n
        out += bytes([1 if final else 0]) + struct.pack("<HH", len(chunk), len(chunk) ^ 0xFFFF) + chunk
        if final:
            break
    return bytes(out + struct.pack(">I", zlib.adler32(data) & 0xFFFFFFFF))


def patch_movie(raw, tags):
    """The login movie's bytes with our tags before the root timeline's first ShowFrame."""
    if len(raw) < 8 or raw[:3] not in (b"CFX", b"CWS"):
        raise MovieError("not a compressed Scaleform movie (CFX)")
    (length,) = struct.unpack_from("<I", raw, 4)
    d = zlib.decompressobj()
    try:
        body = d.decompress(raw[8:]) + d.flush()
    except zlib.error as e:
        raise MovieError(f"zlib: {e}")
    if not d.eof:
        raise MovieError("zlib: the stream is cut short")
    ids, names, depths, show = scan(body)
    if CLASS_NAME in names:
        raise MovieError("the movie has the server picker already")
    if CHAR_ID in ids:
        raise MovieError(f"the movie uses character id {CHAR_ID}")
    if DEPTH in depths:
        raise MovieError(f"the movie uses depth {DEPTH} on its root timeline")
    if show is None:
        raise MovieError("no ShowFrame on the root timeline")
    new = body[:show] + tags + body[show:]
    head = raw[:4] + struct.pack("<I", (length + len(tags)) & 0xFFFFFFFF)
    return head + zlib_stored(new)


def read_tags(path=TAGS):
    """The tags' bytes from their committed hex text."""
    with open(path, "rb") as f:
        return bytes.fromhex(f.read().decode("ascii"))


def build(login_snap_path, tags=None):
    """zz_ServerPicker.snap's bytes from the player's LoginScreen.snap."""
    tags = read_tags() if tags is None else tags
    s = snap.Snap(login_snap_path)
    entry = next((e for e in s.entries if e.name == MOVIE_PATH), None)
    if entry is None:
        raise MovieError(f"{login_snap_path}: no {MOVIE_PATH}")
    movie = patch_movie(s.read(entry), tags)
    return pack_bytes([(MOVIE_PATH, movie)])


def pack_bytes(files):
    """snap.pack's archive as bytes (Snappy entries)."""
    data = bytearray()
    index = bytearray()
    names = bytearray()
    for name, blob in files:
        payload = snap.snappy_compress(blob)
        index += struct.pack("<8I", len(names), len(blob), len(payload), 0, 0, 0, snap.HEADER_SIZE + len(data), 0)
        names += name.encode("utf-8") + b"\0"
        data += payload
    head = snap.MAGIC + struct.pack("<HHII", 0x200, 1, len(files), len(names)) + b"\0" * 16
    return bytes(head + data + index + names)


def main(argv):
    args = list(argv[1:])
    tags_path = TAGS
    if "--tags" in args:
        i = args.index("--tags")
        tags_path = args[i + 1]
        del args[i:i + 2]
    if not sys.flags.isolated:
        sys.exit("run this with python -I: it reads the game's files")
    tags = read_tags(tags_path)
    if len(args) == 3 and args[0] == "--movie":
        with open(args[1], "rb") as f:
            out = patch_movie(f.read(), tags)
        with open(args[2], "wb") as f:
            f.write(out)
        print(f"{args[2]}: {len(out)} bytes")
        return 0
    if len(args) != 2:
        print(__doc__)
        return 2
    out = build(args[0], tags)
    with open(args[1], "wb") as f:
        f.write(out)
    back = snap.Snap(args[1])
    movie = back.read(back.entries[0])
    print(f"{args[1]}: {len(out)} bytes, the movie {len(movie)} bytes with {len(tags)} bytes of the picker's tags")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
