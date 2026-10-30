"""Tests for re/scripts/patch_locale.py (the player copies' added locale package AA_Server.package).

Pinned with the same SHA-256s as its Go twin's tests (server/internal/setup/localepatch/localepatch_test.go), so the
two setups make the same package byte for byte:
  - a made-up Locale.package with the text rule's odd cases (built here and in Go with write_package: same bytes);
  - the synthetic game's Locale.package (synthgame.Locale(), the Go setup tests' archive), for 127.0.0.1: the package
    setup_test.go's end-to-end run of the Go setup installs;
  - build 854's Locale.package for 127.0.0.1, 127.0.0.2 and a web domain (skipped without client/ia_Dawngate);
  - the text rule's cases and the option checks; setup_all.py's locale step on a scratch copy (made, current, stale).
  - MAPPICK-label: the custom lobby's map line (FE_CustomGameLobby 0x0f2b57c5) appended to the shipped table, and
    its text the create dialog's label for MM (patch_login_movie.EXTRA_MAPS).
  - MAPTABLE: a line per map by its LevelId (MM_Map_v3 always, the maps folder's packages), never over a shipped line.

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
MADE_UP_LANE_B = "346751fbfa659f69a74c91a6eebe15e97cc0206a35a276bf358e37b487233a3e"  # --host 127.0.0.2
MADE_UP_WEB = "e2ca3fa68bb3c50190a98fa8cc33f704793d64350452326e15f6c39bb5f302cf"  # 192.168.1.20, Play.Example.org:8443
MADE_UP_LOBBY_ANY = "b5313cd2bf11cafcf66eca7029b62d4dfa10971121ae5170d688d28d560be98e"  # 127.0.0.2, LOBBY_MAP_ANY, MAPS
# a maps folder's packages (localepatch_test.go's maps): CM_Ring, and one named like MM_Map_v3 (its line is the first's)
MAPS = [("CM_Ring", "Test Ring"), ("mm_map_V3", "Not this")]
SYNTH_LOCALE_SHA = "528ce9623b3b1542fa600b1036136a3c39e8e68bb586019fb3a6d3c2f6374832"
SYNTH_LOCAL = "476b2381284742ffbb866771e082af47beb87900b1567f35fc15c2cf3d420a7f"  # the synthetic game, 127.0.0.1
GAME_854 = {  # build 854's Locale.package
    ("127.0.0.1", None): "44bb48de9bd21aefa1886d8ea70e672e27d57c0ad1b3abfa53b6f3e35f690024",
    ("127.0.0.2", None): "0c36b450e08e6c92431236b9312431d3e093dce51c1f7cc6b700903edcb3cbb3",
    ("203.0.113.7", "dawngate.example.org"): "ce31788c2878cd41273039ac60765a9561bb51f6bdf70745b30c5dd783f64b97",
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
        self.assertEqual(sha(pl.build(p, "127.0.0.2", lobby_map=pl.LOBBY_MAP_ANY, maps=MAPS)), MADE_UP_LOBBY_ANY)

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
                if e.instance in (pl.FE_LEADERBOARD, pl.FE_MATCH_TYPE):  # LBMIN: every line replaced in place
                    self.assertNotIn(pl.ADDED_COMMENT.encode(), text)
            lb = [pkg.read(e) for e in pkg.entries if e.instance == pl.FE_LEADERBOARD]
            self.assertIn(b"0x00000101 Rank is based upon PvP and custom games. Must have at least 10 games played.\r\n",
                          lb[0])
            # MAPPICK-label: the lobby's table is the shipped one (no final line end) plus the appended map line
            shipped = pl.find_table(dbpf.Package(path), pl.FE_CUSTOM_GAME_LOBBY)
            lobby = [pkg.read(e) for e in pkg.entries if e.instance == pl.FE_CUSTOM_GAME_LOBBY]
            self.assertTrue(shipped.endswith(b"0x0f2b5801 Please Wait"), shipped[-40:])
            self.assertNotIn(b"0x0f2b57c5", shipped)
            self.assertEqual(lobby, [shipped + b"\r\n" + pl.ADDED_COMMENT.encode() +
                                     b"\r\n0x0f2b57c5 Anshavar Pass\r\n0x420de52e Anshavar Pass\r\n"])  # + MAPTABLE's
            # REFUND2: the HUD table is the shipped one plus the item shop's refund lines
            shipped = pl.find_table(dbpf.Package(path), pl.HUD)
            hud = [pkg.read(e) for e in pkg.entries if e.instance == pl.HUD]
            self.assertTrue(shipped.endswith(b"0x0e546204 Attention\r\n"), shipped[-40:])
            self.assertEqual(hud, [shipped + pl.ADDED_COMMENT.encode() + b"\r\n0x5e70c001 Refund\r\n"
                                   b"0x5e70c002 Refund for ~price~ vim?\r\n0x5e70c003 Refunds for: ~price~\r\n"
                                   b"0x5e70c004 Refund for ~item~ and ~price~ vim?\r\n"
                                   b"0x5e70c005 Refunds for: ~item~ and ~price~\r\n"])
            self.assertEqual(len(pkg.entries), 7)


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

    def test_lobby_map_label(self):
        # the lobby's line for the map the client can't name is the create dialog's label for it: one such map
        import patch_login_movie as plm
        self.assertEqual(pl.tables("127.0.0.1")[0x7BE32E1A], {0x0F2B57C5: "Anshavar Pass",  # fe_customgamelobby
                                                             0x420DE52E: "Anshavar Pass"})  # MAPTABLE: MM's LevelId
        self.assertEqual([m[2] for m in plm.EXTRA_MAPS], [pl.LOBBY_LINES[pl.LOBBY_MAP_LINE]])

    def test_map_lines(self):
        # MAPTABLE: a map's line id is its LevelId (dgserver's: MM_Map_v3 1108206894, CM_Ring 4050252090 in its logs)
        self.assertEqual((pl.level_id("MM_Map_v3"), pl.level_id("CM_Ring"), pl.level_id("SM_Map_v5")),
                         (0x420DE52E, 0xF169F13A, 0x946D41FE))
        self.assertEqual(pl.map_lines(pl.BUILT_IN_MAPS + MAPS), {0x420DE52E: "Anshavar Pass", 0xF169F13A: "Test Ring"})
        lobby = pl.tables("127.0.0.1", lobby_map="Custom map", maps=MAPS)[pl.FE_CUSTOM_GAME_LOBBY]
        self.assertEqual(lobby, {0x0F2B57C5: "Custom map", 0x420DE52E: "Anshavar Pass", 0xF169F13A: "Test Ring"})
        # never over a line the shipped table has
        self.assertEqual(pl.without_lines_of(b"\xef\xbb\xbf0x420de52e Kick\r\n0x0000000a x", lobby),
                         {0x0F2B57C5: "Custom map", 0xF169F13A: "Test Ring"})

    def test_folder_maps(self):
        with tempfile.TemporaryDirectory() as tmp:
            for sub, text in (("B_Two", '{"name": "CM_Two", "display": "Two"}'), ("A_One", '{"name": "CM_One", "display": "One"}'),
                              ("C_Bad", "{"), ("D_NoDisplay", '{"name": "CM_Four"}')):
                os.makedirs(os.path.join(tmp, sub))
                with open(os.path.join(tmp, sub, "map.json"), "w", encoding="utf-8") as f:
                    f.write(text)
            self.assertEqual(pl.folder_maps(tmp), [("CM_One", "One"), ("CM_Two", "Two")])
        self.assertEqual((pl.folder_maps(None), pl.folder_maps(os.path.join(HERE, "no_such_dir"))), ([], []))

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
        a = sa.parse(["setup_all.py", "--root", self.tmp, "--maps", ""])
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
