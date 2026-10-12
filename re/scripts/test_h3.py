"""Tests for roadmap H3's setup command re/scripts/setup_all.py (and patch_config.py's in-memory patch it compares with).

Synthetic archives and trees in temp folders; no game file is written, nothing under client/ is touched. Pinned:
  - the manifest check: missing files, wrong sizes, a key file's SHA-256 (always) and another file's (only --verify);
  - the game zip's guard: everything under Dawngate/, no absolute or '..' paths, a size cap; extract_client.extract()
    keeps the archive's file times;
  - RakNet's extraction: Source/ and the licence files without the top folder, nothing else, unsafe names refused;
  - the staleness rules: run_session.ps1's for dgsim.dll (.lua and core/control/tests ignored) and gms_script.exe,
    and dgserver's (tests and outputs ignored); CMakeCache.txt parsing;
  - the address rule for --server-host/--lan-certs, path containment, the refusal to write into client/ia_* or to
    remove anything but a .partial folder, the option combinations;
  - patch_config.patched_package() gives exactly the bytes patch_config.py writes (real config packages from
    client/ia_Dawngate, copied to a temp folder: --data <game/Data dir> or DG_CONFIG_DIR; skipped without them).

Usage:
    .venv/Scripts/python -I re/scripts/test_h3.py [--data <client/ia_Dawngate/Dawngate/game/Data>] [-v]
"""
import argparse
import contextlib
import hashlib
import io
import os
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
import zipfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # -I drops the script folder
import extract_client  # noqa: E402
import setup_all as sa  # noqa: E402

DATA = None  # set in __main__


def write(path, data=b"x", mtime=None):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as f:
        f.write(data)
    if mtime is not None:
        os.utime(path, (mtime, mtime))
    return path


class Temp(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="test_h3_")

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def p(self, *parts):
        return os.path.join(self.tmp, *parts)


class Manifest(Temp):
    def tree(self):
        files = {"Dawngate/game/Dawngate.exe": b"exe-bytes", "Dawngate/game/Data/Alpha.snap": b"snap",
                 "Dawngate/launcher/Launcher.exe": b"launcher"}
        manifest = {rel: (len(d), hashlib.sha256(d).hexdigest()) for rel, d in files.items()}
        for rel, d in files.items():
            write(self.p("x", *rel.split("/")), d)
        return manifest

    def test_good_tree_and_extra_files(self):
        m = self.tree()
        write(self.p("x", "Dawngate", "game", "Data", "Config.package.orig"), b"extra")
        self.assertEqual(sa.verify_tree(self.p("x"), m), [])
        self.assertEqual(sa.verify_tree(self.p("x"), m, full=True), [])

    def test_missing_and_size(self):
        m = self.tree()
        os.remove(self.p("x", "Dawngate", "launcher", "Launcher.exe"))
        write(self.p("x", "Dawngate", "game", "Data", "Alpha.snap"), b"snap+")
        bad = sa.verify_tree(self.p("x"), m)
        self.assertEqual(len(bad), 2)
        self.assertTrue(any("Launcher.exe is missing" in b for b in bad))
        self.assertTrue(any("Alpha.snap has 5 bytes, want 4" in b for b in bad))

    def test_hash_key_file_always_other_only_full(self):
        m = self.tree()
        write(self.p("x", "Dawngate", "game", "Dawngate.exe"), b"EXE-bytes")  # same size, other bytes
        write(self.p("x", "Dawngate", "game", "Data", "Alpha.snap"), b"SNAP")
        self.assertEqual(sa.verify_tree(self.p("x"), m), ["Dawngate/game/Dawngate.exe differs (SHA-256)"])
        self.assertEqual(len(sa.verify_tree(self.p("x"), m, full=True)), 2)

    def test_real_manifest(self):
        m = sa.load_manifest(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "docs",
                                          "file_manifest.csv"))
        self.assertEqual(len(m), 167)  # Dawngate.zip's files (BASELINE.md)
        self.assertEqual(m["Dawngate/game/Dawngate.exe"][1],
                         "6f7c8e8243bd7a820699535cd84b64017d5a843db12a51be7c9a2e759dde0e2c")  # build 854
        for rel in sa.KEY_FILES:
            self.assertIn("Dawngate/" + rel, m)
        self.assertEqual(sa.in_copy("Dawngate/game/Data/Config.package"), "game/Data/Config.package")


class GameZip(Temp):
    def make(self, names, data=b"abc"):
        path = self.p("a.zip")
        with zipfile.ZipFile(path, "w") as z:
            for n in names:
                info = zipfile.ZipInfo(n, date_time=(2014, 12, 2, 18, 12, 26))
                z.writestr(info, b"" if n.endswith("/") else data)
        return path

    def test_good_extract_keeps_times(self):
        zp = self.make(["Dawngate/", "Dawngate/game/", "Dawngate/game/Dawngate.exe"])
        with zipfile.ZipFile(zp) as z:
            infos = sa.check_game_zip(z)
        self.assertEqual(extract_client.extract(zp, self.p("out"), infos), 3)
        exe = self.p("out", "Dawngate", "game", "Dawngate.exe")
        want = time.mktime((2014, 12, 2, 18, 12, 26, 0, 0, -1))
        self.assertEqual(int(os.path.getmtime(exe)), int(want))

    def test_refusals(self):
        for names in (["Other/x"], ["Dawngate/../x"], ["Dawngate/a", "evil.dll"], ["Dawngate/c:x"]):
            with zipfile.ZipFile(self.make(names)) as z, self.subTest(names=names):
                with self.assertRaises(ValueError):
                    sa.check_game_zip(z)
        with zipfile.ZipFile(self.make(["Dawngate/a", "Dawngate/b"], data=b"0" * 100)) as z:
            with self.assertRaises(ValueError):
                sa.check_game_zip(z, max_bytes=150)


class RakNet(Temp):
    def make(self, names):
        path = self.p("r.zip")
        with zipfile.ZipFile(path, "w") as z:
            for n in names:
                data = b'#define RAKNET_VERSION "4.081"\n' if n.endswith("RakNetVersion.h") else b"x"
                z.writestr(zipfile.ZipInfo(n, date_time=(2015, 1, 2, 3, 4, 6)), data)
        return path

    def test_keeps_source_and_licence(self):
        top = "RakNet-" + sa.RAKNET_COMMIT
        zp = self.make([f"{top}/Source/RakNetVersion.h", f"{top}/Source/RakPeer.cpp", f"{top}/LICENSE",
                        f"{top}/PATENTS", f"{top}/README.md", f"{top}/Samples/a.cpp", f"{top}/Lib/LibStatic/x",
                        f"{top}/DependentExtensions/Swig/very/long/name.cs"])
        self.assertEqual(sa.extract_raknet(zp, self.p("RakNet")), 5)
        self.assertEqual(sorted(os.listdir(self.p("RakNet"))), ["LICENSE", "PATENTS", "README.md", "Source"])
        self.assertEqual(sa.raknet_version(self.p("RakNet")), "4.081")
        self.assertIsNone(sa.raknet_version(self.p("nothing")))

    def test_refusals(self):
        with self.assertRaises(ValueError):
            sa.extract_raknet(self.make(["a/Source/x.h", "b/Source/y.h"]), self.p("o1"))
        with self.assertRaises(ValueError):
            sa.extract_raknet(self.make(["a/Source/../../evil.h"]), self.p("o2"))


class Staleness(Temp):
    def test_gms_rules(self):
        src, build = self.p("gms"), self.p("build")
        old, dll_time, new = 1_000_000, 2_000_000, 3_000_000
        for d in ("engine/a.cpp", "host/d.cpp", "core/session/b.cpp", "script/c.cpp"):
            write(self.p("gms", *d.split("/")), mtime=old)
        write(self.p("build", "Release", "dgsim.dll"), mtime=dll_time)
        write(self.p("build", "Release", "gms_script.exe"), mtime=dll_time)
        self.assertEqual(sa.gms_stale(src, build), [])
        write(self.p("gms", "engine", "lua", "probe.lua"), mtime=new)  # Lua probes don't make the DLL stale
        write(self.p("gms", "core", "control", "tests", "t.cpp"), mtime=new)  # nor the control tests
        self.assertEqual(sa.gms_stale(src, build), [])
        write(self.p("gms", "host", "h.h"), mtime=new)  # dgsim only
        self.assertEqual([os.path.basename(p) for p, _ in sa.gms_stale(src, build)], ["dgsim.dll"])
        write(self.p("gms", "core", "wells", "wells.h"), mtime=new)  # both (the header-only case)
        self.assertEqual([os.path.basename(p) for p, _ in sa.gms_stale(src, build)], ["dgsim.dll", "gms_script.exe"])
        os.remove(self.p("build", "Release", "gms_script.exe"))
        self.assertIn((self.p("build", "Release", "gms_script.exe"), "not built"), sa.gms_stale(src, build))

    def test_server_rule(self):
        src = self.p("repo", "server")
        exe = write(self.p("repo", "server", "bin", "dgserver.exe"), mtime=2_000_000)
        write(self.p("repo", "server", "cmd", "dgserver", "main.go"), mtime=1_000_000)
        self.assertIsNone(sa.server_stale(src, exe))
        for rel in ("internal/x/x_test.go", "data/accounts.json", "certs/server_key.pem", "testdata/a.bin",
                    "tools/make_certs.py", "logs/a.log", "bin/other.exe"):
            write(self.p("repo", "server", *rel.split("/")), mtime=3_000_000)
        self.assertIsNone(sa.server_stale(src, exe))
        write(self.p("repo", "server", "internal", "web", "www", "home.html"), mtime=3_000_000)  # embedded file
        self.assertIn("home.html", sa.server_stale(src, exe))
        self.assertEqual(sa.server_stale(src, self.p("none.exe")), "not built")

    def test_cmake_cache(self):
        write(self.p("b", "CMakeCache.txt"), b"# comment\nRAKNET_ROOT:PATH=D:/x/RakNet\n"
                                             b"CMAKE_HOME_DIRECTORY:INTERNAL=D:/repo/gms\nweird line\n")
        self.assertEqual(sa.cmake_cache(self.p("b")), {"RAKNET_ROOT": "D:/x/RakNet",
                                                       "CMAKE_HOME_DIRECTORY": "D:/repo/gms"})
        self.assertEqual(sa.cmake_cache(self.p("none")), {})


class Rules(Temp):
    def test_lan_host(self):
        for ok in ("192.168.1.20", "10.0.0.5", "172.16.3.4", "100.64.0.9", "169.254.1.1", "8.8.8.8"):  # public too
            self.assertEqual(sa.lan_host(ok, "x"), ok)
        for bad in ("127.0.0.1", "0.0.0.0", "224.0.0.1", "server.lan", "255.255.255.255"):
            with self.assertRaises(ValueError, msg=bad):
                sa.lan_host(bad, "x")

    def test_under(self):
        self.assertTrue(sa.under(r"C:\A\B\c", r"c:\a\b"))
        self.assertTrue(sa.under(r"C:\A\B", r"c:\a\b\\"))
        self.assertFalse(sa.under(r"C:\A\BC", r"C:\A\B"))
        self.assertFalse(sa.under(r"C:\A", r"C:\A\B"))

    def setup(self, *extra):
        return sa.Setup(sa.parse(["setup_all.py", "--root", self.tmp, *extra]))

    def test_never_pristine_and_partial(self):
        s = self.setup()
        for bad in (("client", "ia_Dawngate", "Dawngate", "game"), ("client", "IA_dawngate_1-0-2", "x"),
                    ("client", "ia_Dawngate")):
            with self.assertRaises(RuntimeError, msg=bad):
                s.never_pristine(self.p(*bad))
        s.never_pristine(self.p("client", "run_854", "Dawngate"))  # fine
        s.never_pristine(self.p("client", "run_854_ia_x"))
        os.makedirs(self.p("client", "run_854", "Dawngate"))
        with self.assertRaises(RuntimeError):
            s.remove_partial(self.p("client", "run_854", "Dawngate"))
        os.makedirs(self.p("client", "run_854", "Dawngate.partial", "game"))
        s.remove_partial(self.p("client", "run_854", "Dawngate.partial"))
        self.assertFalse(os.path.exists(self.p("client", "run_854", "Dawngate.partial")))
        self.assertTrue(os.path.isdir(self.p("client", "run_854", "Dawngate")))

    def test_paths(self):
        s = self.setup()
        self.assertEqual(s.root, self.tmp)
        self.assertEqual(s.build, self.tmp)
        self.assertEqual(s.show(s.client), os.path.join("client", "run_854", "Dawngate"))
        s = self.setup("--player", "--server-host", "192.168.1.20")
        self.assertEqual(s.show(s.client), os.path.join("client", "run_854_lan", "Dawngate"))

    def test_options(self):
        bad = (["--player"], ["--server-host", "192.168.1.20"], ["--player", "--server-host", "192.168.1.20",
               "--lane-b"], ["--player", "--server-host", "192.168.1.20", "--lane", "c"], ["--lane", "a"],
               ["--lane", "g"], ["--lane", "c,x"], ["--lan-certs", "127.0.0.1"], ["--afk-disconnect-minutes", "3"],
               ["--lane", "lan"],  # A5b: the lan copy needs --lan-certs <S>
               ["--afk-minutes", "0"], ["--ca", "x.bin"])
        for args in bad:
            with self.subTest(args=args), self.assertRaises(SystemExit):
                with open(os.devnull, "w") as null:
                    old, sys.stderr = sys.stderr, null
                    try:
                        sa.parse(["setup_all.py", *args])
                    finally:
                        sys.stderr = old
        a = sa.parse(["setup_all.py", "--lan-certs", "192.168.1.20", "--afk-minutes", "600"])
        self.assertEqual((a.lan_certs, a.afk_minutes, a.player, a.lanes), ("192.168.1.20", 600, False, []))

    def test_lane_options(self):
        # A6b: --lane X[,Y] (repeatable) and --lane-b name the extra test lanes, in lane order, each once
        cases = ((["--lane-b"], ["b"]), (["--lane", "c"], ["c"]), (["--lane", "d", "--lane", "c"], ["c", "d"]),
                 (["--lane", "c,d", "--lane-b"], ["b", "c", "d"]), (["--lane", "D, c", "--lane", "c"], ["c", "d"]),
                 # A5b: lane d's second client and the lan copy come after the lanes
                 (["--lane", "e,d"], ["d", "e"]),
                 # G3b: lane b's second client after lane b
                 (["--lane", "f,b"], ["b", "f"]),
                 (["--lane", "lan", "--lane", "e", "--lan-certs", "192.168.1.20"], ["e", "lan"]))
        for args, want in cases:
            with self.subTest(args=args):
                self.assertEqual(sa.parse(["setup_all.py", *args]).lanes, want)

    def test_player_hint(self):
        # P2: a finished player setup names the player launcher: play.ps1 -Server <S>, or play_lan.ps1 with -GameDir for
        # a copy made with --client
        cases = ((["--player", "--server-host", "192.168.1.20"], "re\\scripts\\play.ps1 -Server 192.168.1.20"),
                 (["--player", "--server-host", "192.168.1.20", "--client", self.p("elsewhere", "Dawngate")],
                  "re\\scripts\\play_lan.ps1 -Server 192.168.1.20 -GameDir " + os.path.join("elsewhere", "Dawngate")))
        for extra, want in cases:
            with self.subTest(extra=extra):
                out = io.StringIO()
                with contextlib.redirect_stdout(out):
                    self.assertEqual(self.setup(*extra).finish(), 0)
                self.assertIn(want, out.getvalue())
                self.assertNotIn("launch_monitor.ps1", out.getvalue())

    def test_needs_isolated_mode(self):
        r = subprocess.run([sys.executable, os.path.join(os.path.dirname(os.path.abspath(__file__)), "setup_all.py"),
                            "--check", "--root", self.tmp], capture_output=True, text=True)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("python -I", r.stderr)
        self.assertEqual(os.listdir(self.tmp), [])


class PatchedPackage(Temp):
    def test_same_bytes_as_patch_config(self):
        if not DATA:
            raise unittest.SkipTest("no client/ia_Dawngate game/Data (use --data or DG_CONFIG_DIR)")
        import patch_config as pcfg
        for name in pcfg.PACKAGES:
            shutil.copy2(os.path.join(DATA, name), self.p(name))
        args = argparse.Namespace(host="192.168.1.20", http_port=18080, telemetry_port=9988, afk_minutes=None,
                                  afk_disconnect_minutes=None, dry_run=False, restore=False, afk_hits=[])
        want = {n: pcfg.patched_package(self.p(n), args, n) for n in pcfg.PACKAGES}
        r = subprocess.run([sys.executable, "-I", os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                                               "patch_config.py"), self.tmp, "--host", "192.168.1.20"],
                           capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        for n, (data, changes, entries) in want.items():
            self.assertIsNotNone(data, n)
            self.assertGreater(len(changes), 0)
            self.assertGreater(entries, 0)
            with open(self.p(n), "rb") as f:
                self.assertEqual(f.read(), data, n)
            with open(self.p(n + ".orig"), "rb") as f, open(os.path.join(DATA, n), "rb") as g:
                self.assertEqual(f.read(), g.read())


def find_data(explicit):
    root = sa.main_root(os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..")))
    for path in [explicit, os.environ.get("DG_CONFIG_DIR"),
                 os.path.join(root, "client", "ia_Dawngate", "Dawngate", "game", "Data")]:
        if path and os.path.isfile(os.path.join(path, "Config.package")):
            return path
    return None


if __name__ == "__main__":
    args = sys.argv[1:]
    explicit = None
    if "--data" in args:
        i = args.index("--data")
        explicit = args[i + 1]
        del args[i:i + 2]
    DATA = find_data(explicit)
    print(f"config packages: {DATA or 'none (that test is skipped)'}")
    unittest.main(argv=[sys.argv[0]] + args)
