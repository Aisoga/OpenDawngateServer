"""Tests of the movie patcher of the server picker and the bot check box (re/scripts/patch_login_movie.py) and their
committed bytes.

The made-up movie and its patched form have the SHA-256s that server/internal/setup/moviepatch's Go tests pin (the
Go port writes the same bytes); the encoder and archive writer the ones server/internal/setup/snap's tests pin. With
the game's files (client/ia_Dawngate/Dawngate, or DAWNGATE_GAME) the player's own LoginScreen.snap is patched too and
read back, and its Battlegrounds.snap with the bot check box (with DAWNGATE_MATCHOPTIONS_GO naming the file that Go's
TestBuildMatchOptions854 wrote with DAWNGATE_MATCHOPTIONS_OUT, compared byte for byte). --compile also checks that the committed ABC and tags are what the source compiles to (build_picker.py
check; needs the Flex SDK and Java).

Usage:
    python -I re/scripts/test_picker.py [--compile]
"""
import hashlib
import os
import re
import struct
import sys
import tempfile
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)  # -I drops the script folder
import patch_login_movie as plm  # noqa: E402
import snap  # noqa: E402

REPO = os.path.dirname(os.path.dirname(HERE))


def sha(b):
    return hashlib.sha256(b).hexdigest()


def tag(code, data):
    if len(data) < 0x3F:
        return struct.pack("<H", (code << 6) | len(data)) + data
    return struct.pack("<HI", (code << 6) | 0x3F, len(data)) + data


def synth():
    """The made-up movie of moviepatch_test.go's synth(): an import, a sprite with a nested long tag, a symbol, a
    placed object at depth 3, two frames."""
    body = bytes([0x00]) + struct.pack("<HH", 24 << 8, 1)
    body += tag(69, struct.pack("<I", 8))
    body += tag(9, b"\x10\x20\x30")
    body += tag(71, b"lib.swf\0\x01\x00" + struct.pack("<HH", 1, 9) + b"Imp\0")
    body += tag(39, struct.pack("<HH", 5, 1) + tag(2, struct.pack("<H", 7) + bytes(70)) + tag(1, b"") + tag(0, b""))
    body += tag(76, struct.pack("<HH", 1, 5) + b"Foo\0")
    body += tag(26, bytes([0x02]) + struct.pack("<HH", 3, 5))
    body += tag(1, b"") + tag(1, b"") + tag(0, b"")
    return b"CFX\x0c" + struct.pack("<I", len(body) + 8 - 100) + plm.zlib_stored(body)


def lcg(n, mod):
    x, out = 1, bytearray()
    for _ in range(n):
        x = (x * 1103515245 + 12345) & 0x7FFFFFFF
        out.append((97 + (x >> 16) % mod) & 0xFF)
    return bytes(out)


def game_dir():
    if os.environ.get("DAWNGATE_GAME"):
        return os.environ["DAWNGATE_GAME"]
    here = REPO
    for _ in range(5):
        cand = os.path.join(here, "client", "ia_Dawngate", "Dawngate")
        if os.path.isfile(os.path.join(cand, "game", "Data", "LoginScreen.snap")):
            return cand
        here = os.path.dirname(here)
    return None


def main(argv):
    if not sys.flags.isolated:
        sys.exit("run this with python -I: it reads the game's files")
    bad = 0

    def check(ok, what):
        nonlocal bad
        print(f"  {'ok     ' if ok else 'FAILED '} {what}")
        bad += not ok

    m = synth()
    check(sha(m) == "28afbd4438f7fc9e187c6c81e86cecbaf714502fab19f24893a385d0386aab68" and len(m) == 170, "the made-up movie")
    out = plm.patch_movie(m, b"OURTAGS")
    check(sha(out) == "2de38d5f994b8b116ec59c105917d030e7e882e68e81afd747f876b30a689b4b" and len(out) == 177,
          "patched as moviepatch_test.go pins it")
    check(plm.zlib_stored(b"").hex() == "7801010000ffff00000001", "an empty stored zlib stream")
    for name, data, want in (
            ("zeros", bytes(70000), "ce16974b6d393c0283bfc283b34a1764d29bb6a9ccfe6f96669eb5e55a597a86"),
            ("lcg7", lcg(200000, 7), "eb802cfe0142a0359ee2c587dd9b17eabce0d9badd896ae64106ad29182e0d2f")):
        check(sha(snap.snappy_compress(data)) == want, f"snappy_compress({name}) as snap/encode_test.go pins it")
    arc = plm.pack_bytes([("a/one.txt", b"hello hello hello hello, said the snappy encoder to the hello"),
                          ("b/two.bin", lcg(200000, 7))])
    check(sha(arc) == "355e68850a27f0b05de2945b07d36d4739556355559093206c05194af1f4bd16", "pack_bytes as Pack pins it")
    for what, data in (("twice", plm.patch_movie(m, plm.read_tags())), ("not a movie", b"FWS\x0c\0\0\0\0")):
        try:
            plm.patch_movie(data, plm.read_tags())
            check(False, f"refused: {what}")
        except plm.MovieError:
            check(True, f"refused: {what}")

    # the class's English fallbacks are the locale package's lines (patch_locale.py, its Go twin localepatch)
    import patch_locale  # noqa: E402
    with open(os.path.join(REPO, "client_ui", "serverpicker", "ServerPicker.as"), encoding="utf-8") as f:
        source = f.read()
    fallbacks = {int(i, 16): t for i, t in re.findall(r'\[(0x5e70a1[0-9a-f]{2}), "((?:[^"\\]|\\.)*)"\]', source)}
    locale = {i: t for i, t in patch_locale.LOGIN_LINES.items() if i >> 8 == 0x5E70A1}
    check(fallbacks == locale and len(locale) == 49,
          f"the picker's {len(fallbacks)} texts are the locale package's {len(locale)} LoginScreen lines")

    tags = plm.read_tags()
    codes = [c for c, *_ in plm.tags_of(tags, 0, len(tags))]
    check(codes == [39, 82, 76, 70], f"picker_tags.hex: DefineSprite, DoABC, SymbolClass, PlaceObject3 ({codes})")

    # the CREATE GAME dialog's bot check box: the same patcher with its spec
    mo = plm.SPECS["matchoptions"]
    mtags = plm.read_tags(mo.tags)
    mt = plm.tags_of(mtags, 0, len(mtags))
    check([c for c, *_ in mt] == [39, 82, 76, 70] and mt[-1][3] == len(mtags),
          "matchoptions_tags.hex: DefineSprite, DoABC, SymbolClass, PlaceObject3")
    check(plm._pairs(mtags, mt[2][2], mt[2][3]) == [(2000, "BotCheckbox")], "its SymbolClass: 2000 -> BotCheckbox")
    check(mtags[mt[3][2]:mt[3][3]] == bytes([0x06, 0]) + struct.pack("<HH", 2000, 2000) + b"\0",
          "its PlaceObject3: character 2000 at depth 2000, no name")
    once = plm.patch_movie(m, mtags, mo)
    check(plm.patch_movie(m, b"OURTAGS", mo) == out, "the spec patches as the picker's (the same bytes)")
    try:
        plm.patch_movie(once, mtags, mo)
        check(False, "refused: the bot check box twice")
    except plm.MovieError as e:
        check("the bot check box already" in str(e), f"refused: the bot check box twice ({e})")
    with open(os.path.join(REPO, "client_ui", "matchoptions", "BotCheckbox.as"), encoding="utf-8") as f:
        check('MARKER:String = "~#bots"' in f.read(), "the class's marker is the server's ~#bots")

    game = game_dir()
    if game:
        bsrc = os.path.join(game, "game", "Data", "Battlegrounds.snap")
        bbuilt = plm.build(bsrc, spec=mo)
        tmp = tempfile.NamedTemporaryFile(suffix=".snap", delete=False)
        tmp.write(bbuilt)
        tmp.close()
        back = snap.Snap(tmp.name)
        braw = back.read(back.entries[0])
        os.remove(tmp.name)
        ids, names, depths, _ = plm.scan(zlib.decompress(braw[8:]))
        check(back.entries[0].name == mo.movie_path and "BotCheckbox" in names and "maxis.MatchOptionsUI" in names and
              2000 in ids and 2000 in depths,
              f"the player's own CREATE GAME movie patched ({len(bbuilt)} bytes, SHA-256 {sha(bbuilt)[:16]})")
        if os.environ.get("DAWNGATE_MATCHOPTIONS_GO"):  # moviepatch's TestBuildMatchOptions854 output
            with open(os.environ["DAWNGATE_MATCHOPTIONS_GO"], "rb") as f:
                check(f.read() == bbuilt, "the Go setup's zz_MatchOptions.snap is the same")
    if game:
        src = os.path.join(game, "game", "Data", "LoginScreen.snap")
        built = plm.build(src)
        tmp = tempfile.NamedTemporaryFile(suffix=".snap", delete=False)
        tmp.write(built)
        tmp.close()
        movie = snap.Snap(tmp.name)
        raw = movie.read(movie.entries[0])
        os.remove(tmp.name)
        ids, names, depths, _ = plm.scan(zlib.decompress(raw[8:]))
        check(plm.CLASS_NAME in names and "maxis.LoginScreen" in names and plm.CHAR_ID in ids and plm.DEPTH in depths,
              f"the player's own login movie patched ({len(built)} bytes, SHA-256 {sha(built)[:16]})")
        check(plm.build(src) == built, "deterministic")
    else:
        print("  skipped  the game's LoginScreen.snap (no client/ia_Dawngate)")

    if "--compile" in argv:
        import subprocess
        r = subprocess.run([sys.executable, "-I", os.path.join(HERE, "build_picker.py"), "check"])
        check(r.returncode == 0, "build_picker.py check: the committed bytes are the source's")
    print(f"{'all passed' if not bad else str(bad) + ' FAILED'}")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
