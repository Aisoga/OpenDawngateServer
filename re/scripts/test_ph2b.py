"""Tests for re/scripts/patch_locale.py (the player copies' added locale package AA_Server.package).

Pinned with the same SHA-256s as its Go twin's tests (server/internal/setup/localepatch/localepatch_test.go), so the
two setups make the same package byte for byte:
  - a made-up Locale.package with the text rule's odd cases (built here and in Go with write_package: same bytes);
  - the synthetic game's Locale.package (synthgame.Locale(), the Go setup tests' archive), for 127.0.0.1: the package
    setup_test.go's end-to-end run of the Go setup installs;
  - build 854's Locale.package for 127.0.0.1, 127.0.0.2 and a web domain (skipped without client/ia_Dawngate);
  - the text rule's cases and the option checks; setup_all.py's locale step on a scratch copy (made, current, stale).

Usage:
    .venv/Scripts/python -I re/scripts/test_ph2b.py [-v]
"""
import hashlib
import os
import shutil
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)  # -I drops the script folder
import dbpf  # noqa: E402
import patch_locale as pl  # noqa: E402

MADE_UP_SHA = "97766712cea3cf54c20204922cc472d86ac9d592c40e955f7c1a635e0567f25e"
MADE_UP_LANE_B = "e066d9ff54b91af187788a42c1ba13cd409375b20e19a66908e610a5961aa033"  # --host 127.0.0.2
MADE_UP_WEB = "2f25a969efbd9f831edf31d4e96a8c6fbabcb7896fe78c00ef694a0cc85f60e4"  # 192.168.1.20, Play.Example.org:8443
SYNTH_LOCALE_SHA = "528ce9623b3b1542fa600b1036136a3c39e8e68bb586019fb3a6d3c2f6374832"
SYNTH_LOCAL = "3e9e97561e8fe6287e28ba3e8a568d8abc560f119f3cf8c4bfacf8ca7755c2b6"  # the synthetic game, 127.0.0.1
GAME_854 = {  # build 854's Locale.package
    ("127.0.0.1", None): "558cb745ac78f9095ab8757edddb09c7f2a69d10baba1bcacb41e32b486cf28b",
    ("127.0.0.2", None): "48ced935bc1f26e558eb1b89f5e939a1a96c73463b00055fec737110536274d7",
    ("203.0.113.7", "dawngate.example.org"): "94985629c12da7cbf9175d7bb5f97362c2e15d1e56b09fb4f6addfcf1730e432",
}
# synthgame.Locale() (server/internal/setup/testgame/synthgame): one RefPack-compressed table, no login table
SYNTH_LOCALE = bytes.fromhex(
    "4442504603000000000000000000000000000000000000000000000000000000000000000100000000000000240000000000000000000000"
    "0000000003000000b90000000000000000000000000000000000000000000000000000000000000010fb000052f32f2f207469746c65730d"
    "0a3078303030303030303120456d6265720d0a3078303030303030303220456d62657220746865204272696768740d0a3078303030303030"
    "30332046726f737420456d626572fe0d0a00000000b6c0fa0200000000000000003ed819a3600000005900008052000000ffff0000")


def sha(b):
    return hashlib.sha256(b).hexdigest()


def made_up():
    """localepatch_test.go's MadeUp(), the same bytes."""
    blaze = (b"\xef\xbb\xbf0x000c0001 old wrong\n# c\n0X000B0001 old user\r\n0x00650001 disabled http://help\r\n"
             b"0x000c0001 dup\r\n0x0016000 short id\r\n0x00160001\tTabbed\r\n0x00290001 deact")
    login = b"# login\r\n0x0ac5be93 Enter Email\r\n0x00000009 Zo\xc3\xab\r\n0x0ac5be95 Remember Email\r\n"
    return dbpf.write_package([(pl.STRING_TYPE, pl.STRING_GROUP, pl.LOGIN_SCREEN, login),
                               (pl.STRING_TYPE, pl.STRING_GROUP, 0x12345678, b"0x00000001 other\r\n"),
                               (pl.STRING_TYPE, pl.STRING_GROUP, pl.BLAZE_ERRORS, blaze)])


def find_game_locale():
    root = os.path.normpath(os.path.join(HERE, "..", ".."))
    while True:
        cand = os.path.join(root, "client", "ia_Dawngate", "Dawngate", "game", "Data", "Locale", "en-us",
                            "Locale.package")
        if os.path.isfile(cand):
            return cand
        parent = os.path.dirname(root)
        if parent == root:
            return None
        root = parent


class Temp(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="test_ph2b_")

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def locale(self, data):
        p = os.path.join(self.tmp, "Locale.package")
        with open(p, "wb") as f:
            f.write(data)
        return p


class SameAsGo(Temp):
    def test_inputs(self):
        self.assertEqual(sha(made_up()), MADE_UP_SHA)
        self.assertEqual(sha(SYNTH_LOCALE), SYNTH_LOCALE_SHA)

    def test_made_up(self):
        p = self.locale(made_up())
        self.assertEqual(sha(pl.build(p, "127.0.0.2")), MADE_UP_LANE_B)
        self.assertEqual(sha(pl.build(p, "192.168.1.20", web_domain="Play.Example.org:8443")), MADE_UP_WEB)

    def test_synthetic_game(self):
        self.assertEqual(sha(pl.build(self.locale(SYNTH_LOCALE), "127.0.0.1")), SYNTH_LOCAL)

    def test_game_854(self):
        path = find_game_locale()
        if not path:
            self.skipTest("no client/ia_Dawngate")
        for (host, domain), want in GAME_854.items():
            got = pl.build(path, host, web_domain=domain)
            self.assertEqual(sha(got), want, (host, domain))
            # every new line once, read back as the client reads the package
            pkg = dbpf.Package(self.locale(got))
            tabs = pl.tables(host, web_domain=domain)
            for e in pkg.entries:
                text = pkg.read(e)
                for lid, line in tabs[e.instance].items():
                    self.assertEqual(text.count(f"0x{lid:08x} {line}\r\n".encode()), 1, (hex(e.instance), lid))


class Rule(unittest.TestCase):
    def test_patch_table(self):
        lines = {1: "one", 2: "two"}
        c = pl.ADDED_COMMENT.encode()
        for src, want in ((b"", c + b"\r\n0x00000001 one\r\n0x00000002 two\r\n"),
                          (b"0x00000001 a\r\n0x00000002 b", b"0x00000001 one\r\n0x00000002 two"),
                          (b"0x00000001 a\n", b"0x00000001 one\n" + c + b"\r\n0x00000002 two\r\n"),
                          (b"x", b"x\r\n" + c + b"\r\n0x00000001 one\r\n0x00000002 two\r\n"),
                          (b"\xef\xbb\xbf0X00000002 b\r\n0x00000001  a", b"\xef\xbb\xbf0X00000002 two\r\n0x00000001 one")):
            self.assertEqual(pl.patch_table(src, lines), want, src)

    def test_options(self):
        self.assertEqual(pl.site_base("127.0.0.1"), "http://127.0.0.1:18080/dawngate/")
        self.assertEqual(pl.site_base("192.168.1.20", 8080), "http://192.168.1.20:8080/dawngate/")
        self.assertEqual(pl.site_base("127.0.0.1", web_domain="Dawngate.Example.ORG"), "https://dawngate.example.org/")
        self.assertEqual(pl.site_base("127.0.0.1", web_domain="a.b:8443"), "https://a.b:8443/")
        for host, port, dom in (("localhost", 18080, None), ("::1", 18080, None), ("", 18080, None),
                                ("127.0.0.1", 70000, None), ("127.0.0.1", 18080, "nodots"),
                                ("127.0.0.1", 18080, "a.b/c"), ("127.0.0.1", 18080, "a.b:0"),
                                ("127.0.0.1", 18080, "-a.b"), ("127.0.0.1", 18080, "a.b:99999"),
                                ("127.0.0.1", 18080, "dä.de")):
            with self.assertRaises(pl.LocaleError, msg=(host, port, dom)):
                pl.site_base(host, port, dom)

    def test_pages(self):
        # the links name only pages dgserver serves (server/internal/sitelinks.Pages)
        self.assertEqual(set(pl.URL_PAGES.values()), set(pl.SITE_PAGES))


class SetupAllStep(Temp):
    """setup_all.py's locale step on a scratch copy holding the synthetic Locale.package."""

    def test_step(self):
        import contextlib
        import io
        import setup_all as sa
        copy = os.path.join(self.tmp, "copy")
        loc = os.path.join(copy, "game", "Data", "Locale", "en-us")
        os.makedirs(loc)
        with open(os.path.join(loc, "Locale.package"), "wb") as f:
            f.write(SYNTH_LOCALE)
        a = sa.parse(["setup_all.py", "--root", self.tmp])
        s = sa.Setup(a)
        s.manifest = {"Dawngate/" + pl.SOURCE: (len(SYNTH_LOCALE), SYNTH_LOCALE_SHA)}
        added = os.path.join(loc, pl.PACKAGE)
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertTrue(s.step_locale("client texts", copy, "127.0.0.1"))
            self.assertEqual(s.results[-1][0], sa.DONE)
            self.assertEqual(sha(sa.read(added)), SYNTH_LOCAL)
            self.assertTrue(s.step_locale("client texts", copy, "127.0.0.1"))
            self.assertEqual(s.results[-1][0], sa.OK)
            s.check = True
            self.assertFalse(s.step_locale("client texts", copy, "127.0.0.3"))
            self.assertEqual(s.results[-1][0], sa.STALE)
            s.check = False
            with open(os.path.join(loc, "Locale.package"), "ab") as f:
                f.write(b"x")  # not the build's file any more
            self.assertFalse(s.step_locale("client texts", copy, "127.0.0.1"))
            self.assertEqual(s.results[-1][0], sa.FAILED)


if __name__ == "__main__":
    if not sys.flags.isolated:
        sys.exit("run this with python -I")
    unittest.main()
