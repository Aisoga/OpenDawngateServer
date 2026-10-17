"""Tests for roadmap A6 and A6b (test lanes on one PC: a, b, c, d): patch_client.py's lane patches, egress_report.py's
lane rules and the lane tables of setup_lane.py, run_session.ps1 and setup_all.py.

patch_client.py is tested on byte buffers of the untouched build-854 exe, found as in test_a5.py (--exe, DG_PATCH_EXE,
re/out/a5/Dawngate.exe or client/run_854/Dawngate/game/Dawngate.exe.orig); without it those tests are skipped. Nothing
here touches client/ia_* or a working copy.

What is pinned:
  - lane b's exe (local profile, 127.0.0.2, --data-suffix B) differs from lane a's only in the redirector host, the bind
    cave's address, RakNet's loopback literal and the two data folder names; describe() (--show) reads all of it back;
    a lan profile copy keeps RakNet's literal (with --client-bind its IPV4_LOOPBACK pointer names the bound address,
    A5b); the same for lanes c and d (A6b), made from their LANES rows;
  - --data-suffix takes exactly one letter or digit; the local profile's --client-bind only a loopback address;
  - egress_report.py: the game's endpoints with their addresses, the cross-lane rule (this lane's game endpoint to
    another loopback address is flagged; another lane's game, multicast and the lane's own address are not; with four
    lanes too), and --lane-address takes loopback addresses only;
  - setup_lane.py's LANES and run_session.ps1's $LaneTable agree (address, copies, certificates, data, server binary,
    lock, the client's arguments, the data folder suffix), and so do run_session.ps1's -Lane values and setup_all.py's
    EXTRA_LANES (A6b); every lane's address, copies, suffix and lock are its own;
  - A5b: setup_lane.py's EXTRA_CLIENTS (lane d's second client) and LAN (the LAN profile's test copy) agree with
    run_session.ps1's $ExtraClients and $LanCopy and stay apart from every lane (letter, suffix, bind address, folder);
    Get-CopyLane gives the second client's copy to its lane and never counts client\run_854_lan or client\run_854_l as
    lane a's; Test-OwnBind takes a dgserver bound to the -Lan address as the lan session's; the cross-lane rule with a
    second client's address and with the lan copy's endpoints.

Usage:
    .venv/Scripts/python -I re/scripts/test_a6.py [--exe <untouched Dawngate.exe>] [-v]
"""
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # -I drops the script folder
import egress_report  # noqa: E402
import patch_client as pc  # noqa: E402
import setup_lane  # noqa: E402
import test_a5  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
MODULUS = test_a5.MODULUS
EXE = None  # set in __main__


class LanePatchTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not EXE:
            raise unittest.SkipTest("no untouched Dawngate.exe (use --exe or DG_PATCH_EXE)")
        cls.orig = test_a5.read(EXE)
        if test_a5.sha256(cls.orig) != test_a5.BUILD_854_SHA256:
            raise unittest.SkipTest(f"{EXE} is not the untouched build 854")
        import pefile
        pe = pefile.PE(data=cls.orig, fast_load=True)
        cls.off = staticmethod(lambda va: pe.get_offset_from_rva(va - pc.IMAGE_BASE))
        cls.lane_a, _ = pc.patch_bytes(cls.orig, MODULUS)
        cls.lane_b, cls.summary_b = pc.patch_bytes(cls.orig, MODULUS, host="127.0.0.2", data_suffix="B")

    def test_lane_a_is_still_the_a4_output(self):
        # since Q3b plus the LARGE_ADDRESS_AWARE bit, since SNDBANK plus the preload call (test_a5 pins each)
        self.assertEqual(test_a5.sha256(self.lane_a), test_a5.SNDBANK_OUTPUT_SHA256)

    def test_lane_b_changes_only_host_bind_and_folders(self):
        allowed = [(self.off(pc.REDIRECTOR_HOST), 21),
                   (self.off(pc.LOOPBACK_CAVE) + len(pc.LOOPBACK_CAVE_CODE) - 10, 4),
                   (self.off(pc.APPDIR_DEFAULT), pc.APPDIR_ROOM), (self.off(pc.PLAYERDIR_DEFAULT), pc.PLAYERDIR_ROOM),
                   (self.off(pc.RAKNET_LOOPBACK), pc.RAKNET_LOOPBACK_ROOM)]
        ranges = test_a5.diff_ranges(self.lane_a, self.lane_b)
        self.assertTrue(ranges)
        for start, end in ranges:
            self.assertTrue(any(s <= start and end <= s + n for s, n in allowed), hex(start))
        self.assertIn("sockets narrowed to 127.0.0.2", self.summary_b)
        self.assertIn("DawngateDataB", self.summary_b)

    def test_every_extra_lane_from_its_row(self):
        # A6b: lanes c and d (and b) as setup_lane.py patches them: the row's address and suffix, nothing else
        allowed = [(self.off(pc.REDIRECTOR_HOST), 21),
                   (self.off(pc.LOOPBACK_CAVE) + len(pc.LOOPBACK_CAVE_CODE) - 10, 4),
                   (self.off(pc.APPDIR_DEFAULT), pc.APPDIR_ROOM), (self.off(pc.PLAYERDIR_DEFAULT), pc.PLAYERDIR_ROOM),
                   (self.off(pc.RAKNET_LOOPBACK), pc.RAKNET_LOOPBACK_ROOM)]
        for lane, cfg in setup_lane.LANES.items():
            if lane == "a":
                continue
            with self.subTest(lane=lane):
                data, _ = pc.patch_bytes(self.orig, MODULUS, host=cfg["address"], data_suffix=cfg["suffix"])
                for start, end in test_a5.diff_ranges(self.lane_a, data):
                    self.assertTrue(any(s <= start and end <= s + n for s, n in allowed), hex(start))
                got = pc.describe(data)
                want = setup_lane.expected_exe(lane, os.path.join(HERE, "no_such_root"))  # no CA file: no ca_sha256
                self.assertEqual({k: got[k] for k in want}, want)

    def test_describe_reads_it_back(self):
        self.assertEqual(pc.describe(self.lane_b), {
            "redirector": "127.0.0.2", "bind": "127.0.0.2", "appdata": "DawngateDataB", "documents": "DawngateB",
            "raknet_loopback": "127.0.0.2", "ca_sha256": pc.hashlib.sha256(MODULUS).hexdigest()[:16], "ca_flag": "0",
            "laa": "on", "preload": "all"})
        a = pc.describe(self.lane_a)
        self.assertEqual((a["redirector"], a["bind"], a["appdata"], a["documents"], a["raknet_loopback"]),
                         ("127.0.0.1", "127.0.0.1", "DawngateData", "Dawngate", "127.0.0.1"))
        o = pc.describe(self.orig)
        self.assertEqual((o["redirector"], o["bind"], o["ca_flag"]), ("gosredirector.ea.com", "as-shipped", "1"))

    def test_client_bind_in_the_local_profile(self):
        same, _ = pc.patch_bytes(self.orig, MODULUS, host="127.0.0.2", client_bind="127.0.0.2", data_suffix="B")
        self.assertEqual(same, self.lane_b)
        other, _ = pc.patch_bytes(self.orig, MODULUS, host="127.0.0.2", client_bind="127.0.0.3")
        self.assertEqual(pc.describe(other)["bind"], "127.0.0.3")
        self.assertEqual(pc.describe(other)["raknet_loopback"], "127.0.0.3")
        lan, _ = pc.patch_bytes(self.orig, MODULUS, "lan", server_host="192.168.1.20", client_bind="192.168.1.30")
        self.assertEqual(pc.describe(lan)["raknet_loopback"], "192.168.1.30")  # A5b: through the pointer, not the literal
        with self.assertRaises(pc.PatchError):  # longer than RakNet's literal has room for
            pc.patch_bytes(self.orig, MODULUS, host="127.100.100.100")
        for kw in ({"client_bind": "192.168.1.30"}, {"client_bind": "0.0.0.0"}, {"client_bind": "x"}):
            with self.assertRaises(pc.PatchError, msg=kw):
                pc.patch_bytes(self.orig, MODULUS, **kw)

    def test_data_suffix_refusals(self):
        for bad in ("BB", "_", "é", "-", " "):
            with self.assertRaises(pc.PatchError, msg=bad):
                pc.patch_bytes(self.orig, MODULUS, data_suffix=bad)
        z, _ = pc.patch_bytes(self.orig, MODULUS, data_suffix="9")
        self.assertEqual(pc.describe(z)["documents"], "Dawngate9")

    def test_main_show(self):
        with tempfile.TemporaryDirectory() as tmp:
            exe = os.path.join(tmp, "Dawngate.exe")
            with open(exe, "wb") as f:
                f.write(self.lane_b)
            import io
            import contextlib
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                pc.main(["patch_client.py", "--show", exe])
            self.assertIn("appdata=DawngateDataB", out.getvalue().splitlines())
            self.assertIn("bind=127.0.0.2", out.getvalue().splitlines())


class EgressLaneTest(unittest.TestCase):
    def test_endpoints_with_addresses(self):
        with tempfile.TemporaryDirectory() as d:
            mon = os.path.join(d, "monitor.log")
            with open(mon, "w", encoding="utf-8") as f:
                f.write("t=4s UDP bound 127.0.0.2:3659\n"
                        "t=6s TCP 127.0.0.2:51000 -> 127.0.0.2:42127 Established\n"
                        "t=6s TCP 127.0.0.2:0 -> 0.0.0.0:0 Bound\n")
            self.assertEqual(egress_report.dawngate_endpoints(mon),
                             {("UDP", "127.0.0.2", 3659), ("TCP", "127.0.0.2", 51000)})

    def test_cross_lane_rule(self):
        eps = {("UDP", "127.0.0.2", 3659), ("TCP", "127.0.0.2", 51000)}
        cl = egress_report.cross_lane
        self.assertTrue(cl("TCP", "127.0.0.2", 51000, "127.0.0.1", eps, "127.0.0.2"))   # lane b's game to lane a
        self.assertTrue(cl("UDP", "127.0.0.2", 3659, "127.0.0.1", eps, "127.0.0.2"))
        self.assertFalse(cl("UDP", "127.0.0.2", 3659, "127.0.0.2", eps, "127.0.0.2"))   # its own lane
        self.assertFalse(cl("UDP", "127.0.0.1", 3659, "127.0.0.2", eps, "127.0.0.2"))   # lane a's game (same port)
        self.assertFalse(cl("TCP", "127.0.0.2", 3659, "127.0.0.1", eps, "127.0.0.2"))   # UDP port, TCP packet
        self.assertFalse(cl("UDP", "127.0.0.2", 3659, "239.255.255.250", eps, "127.0.0.2"))  # multicast

    def test_cross_lane_rule_with_four_lanes(self):
        # A6b: lane c's game (127.0.0.3) beside lanes a, b and d on the same port numbers
        eps = {("UDP", "127.0.0.3", 3659), ("TCP", "127.0.0.3", 51000)}
        cl = egress_report.cross_lane
        for other in ("127.0.0.1", "127.0.0.2", "127.0.0.4"):
            self.assertTrue(cl("UDP", "127.0.0.3", 3659, other, eps, "127.0.0.3"), other)   # lane c's game to another lane
            self.assertFalse(cl("UDP", other, 3659, "127.0.0.3", eps, "127.0.0.3"), other)  # another lane's game
        self.assertFalse(cl("TCP", "127.0.0.3", 51000, "127.0.0.3", eps, "127.0.0.3"))      # its own lane
        self.assertTrue(cl("TCP", "127.0.0.3", 51000, "127.0.0.4", eps, "127.0.0.3"))

    def test_cross_lane_rule_with_a_second_client(self):
        # A5b: lane d's second client (127.0.4.2) is the same session: its own address and the lane's aren't cross-lane
        eps = {("UDP", "127.0.0.4", 3659), ("UDP", "127.0.4.2", 3659), ("UDP", "127.0.4.2", 61000)}
        cl = egress_report.cross_lane
        own = ["127.0.0.4", "127.0.4.2"]
        self.assertFalse(cl("UDP", "127.0.4.2", 61000, "127.0.4.2", eps, own))  # RakNet's send test to itself
        self.assertFalse(cl("UDP", "127.0.4.2", 3659, "127.0.0.4", eps, own))   # to the lane's server
        self.assertTrue(cl("UDP", "127.0.4.2", 3659, "127.0.0.1", eps, own))    # to lane a: cross-lane
        self.assertTrue(cl("UDP", "127.0.4.2", 61000, "127.0.4.2", eps, "127.0.0.4"))  # without the second address
        # G3b: lane b's second client (127.0.2.2) the same way; lane d's second client is another lane for it
        eps_b = {("UDP", "127.0.0.2", 3659), ("UDP", "127.0.2.2", 3659)}
        own_b = ["127.0.0.2", "127.0.2.2"]
        self.assertFalse(cl("UDP", "127.0.2.2", 3659, "127.0.0.2", eps_b, own_b))
        self.assertTrue(cl("UDP", "127.0.2.2", 3659, "127.0.4.2", eps_b, own_b))
        self.assertTrue(cl("UDP", "127.0.2.2", 3659, "127.0.0.4", eps_b, own_b))
        # A5b: the lan copy's endpoint on the LAN address to a loopback address is cross-lane
        lan = {("UDP", "192.168.20.40", 3659)}
        self.assertTrue(cl("UDP", "192.168.20.40", 3659, "127.0.0.1", lan, "127.0.0.4"))
        self.assertFalse(cl("UDP", "192.168.20.40", 3659, "192.168.20.40", lan, "127.0.0.4"))  # not loopback

    def test_lane_address_is_loopback(self):
        for ok in ("127.0.0.1", "127.0.0.2", "127.1.2.3"):
            self.assertEqual(egress_report.lane_address(ok), ok)
        for bad in ("192.168.1.20", "0.0.0.0", "::1", "lane-b"):
            with self.assertRaises(Exception, msg=bad):
                egress_report.lane_address(bad)


class LaneTableTest(unittest.TestCase):
    def test_run_session_agrees_with_setup_lane(self):
        with open(os.path.join(HERE, "run_session.ps1"), encoding="utf-8") as f:
            text = f.read()
        block = re.search(r"^\$LaneTable = @\{\r?\n(.*?)^\}", text, re.M | re.S).group(1)  # (A5b: not $ExtraClients)
        rows = dict(re.findall(r"^\s+(\w) = @\{ (.*?) \}$", block, re.M))
        self.assertEqual(sorted(rows), sorted(setup_lane.LANES))
        for lane, cfg in setup_lane.LANES.items():
            ps = dict(re.findall(r"(\w+) = '([^']*)'", rows[lane]))
            norm = lambda p: p.replace("/", "\\")  # noqa: E731
            self.assertEqual(ps["Address"], cfg["address"], lane)
            self.assertEqual(ps["Client"], norm(cfg["client"]), lane)
            self.assertEqual(ps["HostCopy"], norm(cfg["host"]), lane)
            self.assertEqual(ps["Certs"], norm(cfg["certs"]), lane)
            self.assertEqual(ps["Suffix"], cfg["suffix"], lane)
            self.assertEqual(ps["Data"], norm(cfg["data"]), lane)
            self.assertEqual(ps["ServerExe"], norm(cfg["server_exe"]), lane)
            self.assertEqual(ps["Lock"], norm(cfg["lock"]), lane)
            self.assertEqual(ps["GameArgs"], cfg["game_args"], lane)

    def test_extra_clients_and_lan_copy_agree(self):
        # A5b: run_session.ps1's $ExtraClients and $LanCopy are setup_lane.py's EXTRA_CLIENTS and LAN
        with open(os.path.join(HERE, "run_session.ps1"), encoding="utf-8") as f:
            text = f.read()
        block = re.search(r"^\$ExtraClients = @\{\r?\n(.*?)^\}", text, re.M | re.S).group(1)
        rows = dict(re.findall(r"^\s+(\w) = @\{ (.*?) \}$", block, re.M))
        self.assertEqual(sorted(rows), sorted(setup_lane.EXTRA_CLIENTS))
        norm = lambda p: p.replace("/", "\\")  # noqa: E731
        for name, cfg in setup_lane.EXTRA_CLIENTS.items():
            ps = dict(re.findall(r"(\w+) = '([^']*)'", rows[name]))
            self.assertEqual((ps["Lane"], ps["Client"], ps["Bind"], ps["Suffix"]),
                             (cfg["lane"], norm(cfg["client"]), cfg["bind"], cfg["suffix"]), name)
        lan = dict(re.findall(r"(\w+) = '([^']*)'", re.search(r"^\$LanCopy = @\{ (.*?) \}$", text, re.M).group(1)))
        self.assertEqual((lan["Client"], lan["Suffix"], lan["Certs"], lan["Lock"]),
                         (norm(setup_lane.LAN["client"]), setup_lane.LAN["suffix"], norm(setup_lane.LAN["certs"]),
                          norm(setup_lane.LAN["lock"])))

    def test_extra_copies_are_their_own(self):
        # A5b: an extra client and the lan copy: a letter no lane uses (so no script since A6b takes them for lane a's),
        # their own suffix, a bind address outside the lanes' 127.0.0.N, folders apart from every other copy
        lane_suffixes = {cfg["suffix"] for cfg in setup_lane.LANES.values()}
        lane_addresses = {cfg["address"] for cfg in setup_lane.LANES.values()}
        suffixes = [cfg["suffix"] for cfg in setup_lane.EXTRA_CLIENTS.values()] + [setup_lane.LAN["suffix"]]
        self.assertEqual(len(suffixes), len(set(suffixes)))
        for name, cfg in setup_lane.EXTRA_CLIENTS.items():
            self.assertNotIn(name, setup_lane.LANES)
            self.assertIn(cfg["lane"], setup_lane.LANES)
            self.assertEqual(cfg["client"], f"client/run_854_{name}/Dawngate")
            self.assertNotIn(cfg["suffix"], lane_suffixes)
            self.assertNotIn(cfg["bind"], lane_addresses)
            self.assertTrue(egress_report.lane_address(cfg["bind"]))
            pc.check_data_suffix(cfg["suffix"])
        letter = re.fullmatch(r"client/run_854_([a-z])/Dawngate", setup_lane.LAN["client"]).group(1)
        self.assertNotIn(letter, setup_lane.LANES)
        self.assertNotIn(letter, setup_lane.EXTRA_CLIENTS)
        self.assertNotIn(setup_lane.LAN["suffix"], lane_suffixes)
        roots = ([os.path.dirname(cfg[k]) + "/" for cfg in setup_lane.LANES.values() for k in ("client", "host")]
                 + [os.path.dirname(c["client"]) + "/" for c in setup_lane.EXTRA_CLIENTS.values()]
                 + [os.path.dirname(setup_lane.LAN["client"]) + "/"])
        for r in roots:
            self.assertEqual([x for x in roots if x.startswith(r)], [r], r)

    def test_leftover_rules(self):
        # A6b: run_session.ps1's Get-CopyLane, Get-FolderLane and Get-BindLane, run in PowerShell on a scratch main
        # checkout: every lane's copies, session folders and servers are that lane's, and a lane the table doesn't know
        # (e) is never lane a's (d6a_run2's cleanup, from a two-lane table, stopped lanes c's and d's processes)
        ps = shutil.which("powershell")
        if not ps:
            self.skipTest("no powershell")
        with tempfile.TemporaryDirectory() as root:
            for name, lane in (("s_a", None), ("s_b", "b"), ("s_c", "c"), ("s_d", "d"), ("s_e", "e"), ("s_x", "?")):
                os.makedirs(os.path.join(root, "re", "logs", name))
                if lane:
                    with open(os.path.join(root, "re", "logs", name, "lane.txt"), "w") as f:
                        f.write(lane + "\n")
            client = os.path.join(root, "client").lower() + "\\"
            logs = os.path.join(root, "re", "logs").lower() + "\\"
            cases = {f"copy {c}": client + c + "\\dawngate\\game\\dawngate.exe"
                     for c in ("run_854", "run_854_server", "run_854_b", "run_854_server_b", "run_854_c",
                               "run_854_server_c", "run_854_d", "run_854_server_d", "run_854_e", "run_854_server_e",
                               "run_854_lan", "run_854_l", "run_854_f", "run_854_g")}
            cases.update({f"folder {n}": f'dumpcap.exe -w "{logs}{n}\\capture.pcapng"'
                          for n in ("s_a", "s_b", "s_c", "s_d", "s_e", "s_x")})
            cases.update({f"bind {b}": f"dgserver.exe {b}".strip()
                          for b in ("", "--bind 127.0.0.3", "--bind 127.0.0.4", "--bind 127.0.0.9",
                                    # P2-live: dgserver's --host counts when --bind isn't given (p2live_run1b's
                                    # server, --host <LAN address>, was stopped as lane a's)
                                    "--host 192.168.20.40", "--host 127.0.0.3", "--host 127.0.0.3 --bind 127.0.0.4",
                                    "--public-host 127.0.0.3", "--gms-control-bind 127.0.0.2")})
            # A5b: Test-OwnBind in a lane d session with -Lan 192.168.20.40
            cases.update({f"own {b}": f"dgserver.exe {b}".strip()
                          for b in ("--bind 127.0.0.4", "--bind 127.0.0.3", "--bind 192.168.20.40",
                                    "--bind 192.168.20.4", "--bind 192.168.20.401", "--host 192.168.20.40",
                                    "--host 192.168.20.40 --bind 127.0.0.2")})
            # D1k: a dgserver's lane by its binary (server\bin\lane_<x>\ in any checkout; server\bin\ = lane a)
            cases.update({f"exe {n}": p for n, p in (
                ("main", "d:\\x\\server\\bin\\dgserver.exe"),
                ("worktree_b", "d:\\x\\worktrees\\w1\\server\\bin\\lane_b\\dgserver.exe"),
                ("main_d", "d:\\x\\server\\bin\\lane_d\\dgserver.exe"))})
            script = os.path.join(root, "t.ps1")
            with open(script, "w", encoding="ascii") as f:
                f.write("$ErrorActionPreference = 'Stop'\n"
                        f"$ast = [System.Management.Automation.Language.Parser]::ParseFile('{HERE}\\run_session.ps1', "
                        "[ref]$null, [ref]$null)\n"
                        "foreach ($v in '$LaneTable', '$ExtraClients') {\n"
                        "  $t = @($ast.FindAll({ param($a) $a -is [System.Management.Automation.Language."
                        "AssignmentStatementAst] -and $a.Left.Extent.Text -eq $v }, $true))[0]\n"
                        "  . ([scriptblock]::Create($t.Extent.Text)) }\n"
                        "foreach ($n in 'Get-CopyLane', 'Get-FolderLane', 'Get-ServerBind', 'Get-BindLane', "
                        "'Test-OwnBind', 'Get-ServerExeLane') {\n"
                        "  $fn = @($ast.FindAll({ param($a) $a -is [System.Management.Automation.Language."
                        "FunctionDefinitionAst] -and $a.Name -eq $n }, $true))[0]\n"
                        "  . ([scriptblock]::Create($fn.Extent.Text)) }\n"
                        f"$MainRoot = '{root}'\n"
                        "$Lane = 'd'; $LanAddr = '192.168.20.40'\n")
                for key, arg in cases.items():
                    fn = {"copy": "Get-CopyLane", "folder": "Get-FolderLane", "bind": "Get-BindLane",
                          "own": "Test-OwnBind", "exe": "Get-ServerExeLane"}[key.split()[0]]
                    f.write(f"'{key}=' + ({fn} '{arg}')\n")
            r = subprocess.run([ps, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script],
                               capture_output=True, text=True)
            self.assertEqual(r.returncode, 0, r.stderr)
            got = dict(line.split("=", 1) for line in r.stdout.splitlines() if "=" in line)
        # A5b: run_854_e is lane d's second client, run_854_lan (setup_all.py --player) nobody's ('lan'), run_854_l
        # (the lan test copy) 'l' (the -Lan session's); a server bound to the LAN address is the -Lan session's
        # G3b: run_854_f is lane b's second client; run_854_g a letter nobody uses ('g': never lane a's)
        want = {"copy run_854": "a", "copy run_854_server": "a", "copy run_854_lan": "lan", "copy run_854_l": "l",
                "copy run_854_f": "b", "copy run_854_g": "g",
                "copy run_854_b": "b", "copy run_854_server_b": "b", "copy run_854_c": "c", "copy run_854_server_c": "c",
                "copy run_854_d": "d", "copy run_854_server_d": "d", "copy run_854_e": "d", "copy run_854_server_e": "e",
                "folder s_a": "a", "folder s_b": "b", "folder s_c": "c", "folder s_d": "d", "folder s_e": "e",
                "folder s_x": "a", "bind ": "a", "bind --bind 127.0.0.3": "c", "bind --bind 127.0.0.4": "d",
                "bind --bind 127.0.0.9": "", "bind --host 192.168.20.40": "", "bind --host 127.0.0.3": "c",
                "bind --host 127.0.0.3 --bind 127.0.0.4": "d", "bind --public-host 127.0.0.3": "a",
                "bind --gms-control-bind 127.0.0.2": "a",
                "own --bind 127.0.0.4": "True", "own --bind 127.0.0.3": "False",
                "own --bind 192.168.20.40": "True", "own --bind 192.168.20.4": "False",
                "own --bind 192.168.20.401": "False", "own --host 192.168.20.40": "True",
                "own --host 192.168.20.40 --bind 127.0.0.2": "False",
                "exe main": "a", "exe worktree_b": "b", "exe main_d": "d"}
        self.assertEqual(got, want)

    def test_lane_values_agree(self):
        # A6b: run_session.ps1's -Lane ValidateSet and setup_all.py's EXTRA_LANES name the same lanes as LANES
        with open(os.path.join(HERE, "run_session.ps1"), encoding="utf-8") as f:
            m = re.search(r"\[ValidateSet\(([^)]*)\)\]\[string\]\$Lane\b", f.read())
        self.assertTrue(m)
        self.assertEqual(sorted(re.findall(r"'(\w)'", m.group(1))), sorted(setup_lane.LANES))
        import setup_all
        self.assertEqual(sorted(setup_all.EXTRA_LANES), sorted(k for k in setup_lane.LANES if k != "a"))

    def test_lanes_are_disjoint(self):
        for key in ("address", "client", "host", "certs", "data", "server_exe", "lock", "suffix"):
            values = [cfg[key] for cfg in setup_lane.LANES.values()]
            self.assertEqual(len(values), len(set(values)), key)
        for i, (lane, cfg) in enumerate(sorted(setup_lane.LANES.items()), 1):
            self.assertTrue(egress_report.lane_address(cfg["address"]))
            self.assertEqual(cfg["address"], f"127.0.0.{i}", lane)  # a = .1, b = .2, c = .3, d = .4
            self.assertFalse(cfg["client"].startswith("client/ia_"))
            pc.check_data_suffix(cfg["suffix"] or None)
            if lane != "a":
                self.assertEqual(cfg["suffix"], lane.upper())
                self.assertEqual(cfg["game_args"], "-multipleInstances")  # lane a's client holds Global\Dawngate
        # run_session.ps1 tells the copies apart by folder prefix (Get-CopyLane): no copy folder inside another
        roots = [os.path.dirname(cfg[k]) + "/" for cfg in setup_lane.LANES.values() for k in ("client", "host")]
        for r in roots:
            self.assertEqual([x for x in roots if x.startswith(r)], [r], r)


if __name__ == "__main__":
    args = sys.argv[1:]
    explicit = None
    if "--exe" in args:
        i = args.index("--exe")
        explicit = args[i + 1]
        del args[i:i + 2]
    EXE = test_a5.find_exe(explicit)
    print(f"exe: {EXE or 'none (patcher tests skipped)'}")
    unittest.main(argv=[sys.argv[0]] + args)
