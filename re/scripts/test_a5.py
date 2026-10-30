"""Tests for roadmap A5's client-side tools: patch_client.py's profiles and egress_report.py --allow-host.

patch_client.py is tested on byte buffers of the untouched build-854 exe, plus one run of its main() on a
temporary copy (in the system temp folder). The exe is read only: from --exe, the environment variable
DG_PATCH_EXE, <repo>/re/out/a5/Dawngate.exe or <repo>/client/run_854/Dawngate/game/Dawngate.exe.orig
(the first that exists); it must have build 854's SHA-256 (docs/file_manifest.csv). Without it the
patcher tests are skipped. Nothing here touches client/ia_* or a working copy.

What is pinned:
  - the local profile with --laa off --preload off writes exactly what the A4 version of patch_client.py wrote
    (SHA-256 of its output for a fixed CA modulus, taken from that version before A5 changed it); Q3b added only the
    LARGE_ADDRESS_AWARE bit, SNDBANK only the preload call (5 bytes), MAPTABLE only the map-name cave (its jmp, the
    cave, the key, two VirtualSizes); each output is pinned;
  - the lan profile changes only the CA, the redirector host and UPnP, and leaves every bind site, the
    cave slack and .text's VirtualSize as shipped; with --client-bind it narrows to that address;
  - the refusals (non-loopback host in the local profile, loopback/public/wildcard hosts in lan, a
    wrong build, client/ia_* paths);
  - egress_report.py: only our own network's addresses can be allowed, an allowed address is not
    flagged for Dawngate.exe ports, and every other rule still flags; a capture whose last packet was cut
    short (P1's play mode stops dumpcap) is read, any other tshark error is not.

Usage:
    .venv/Scripts/python -I re/scripts/test_a5.py [--exe <untouched Dawngate.exe>] [-v]
"""
import hashlib
import os
import shutil
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # -I drops the script folder
import egress_report  # noqa: E402
import patch_client as pc  # noqa: E402

REPO = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))
BUILD_854_SHA256 = "6f7c8e8243bd7a820699535cd84b64017d5a843db12a51be7c9a2e759dde0e2c"
MODULUS = bytes([0x5A]) * 128
# patch_client.py as of commit c08d872 (A4), run on build 854 with MODULUS and host 127.0.0.1
A4_OUTPUT_SHA256 = "a15dd7ad91326f3114a054fd72ef20248a833a253e0c86a7d63fc13a0ecef027"
A4_SUMMARY = ("redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); "
              "sockets loopback-only (bind cave at 0x011ab100, 6 sites)")
# Q3b: the default since then, the A4 output plus IMAGE_FILE_LARGE_ADDRESS_AWARE (characteristics 0x0103 -> 0x0123)
Q3B_OUTPUT_SHA256 = "237512dc68b4135e220b410d92a6579cd602188079bd093b365871e38d214613"
Q3B_SUMMARY = A4_SUMMARY + "; LARGE_ADDRESS_AWARE (4 GB of address space)"
# SNDBANK: the default since then, the Q3b output plus the preload call (0x0066BD1F: call CountPlayers -> mov eax,15)
SNDBANK_OUTPUT_SHA256 = "a6ca0ae63b8815bf38aeb4575f61a951e4c92f5a159f0c1465f4a47cfe57267e"
SNDBANK_SUMMARY = Q3B_SUMMARY + "; every player's shaper preloaded (player indexes 0-14)"
# MAPTABLE: the default since then, the SNDBANK output plus the map-name cave (0x006843D2 -> jmp 0x011AB140, the cave,
# the key in .tls's slack, .text's and .tls's VirtualSize raised to their SizeOfRawData)
MAPTABLE_OUTPUT_SHA256 = "d81339e8e44cc4a6781c92db73488f20897e24ead406c7622a683dd9768a4543"
MAPTABLE_SUMMARY = (SNDBANK_SUMMARY + "; every map named by its LevelId's line in FE_CustomGameLobby "
                    "(map-name cave at 0x011ab140)")
EXE = None  # set in __main__


def find_exe(explicit):
    for path in [explicit, os.environ.get("DG_PATCH_EXE"), os.path.join(REPO, "re", "out", "a5", "Dawngate.exe"),
                 os.path.join(REPO, "client", "run_854", "Dawngate", "game", "Dawngate.exe.orig")]:
        if path and os.path.isfile(path):
            return path
    return None


def read(path):
    with open(path, "rb") as f:
        return f.read()


def sha256(b):
    return hashlib.sha256(b).hexdigest()


def diff_ranges(a, b):
    """[(start, end)] of the byte ranges where a and b differ (same length)."""
    out, start = [], None
    for i, (x, y) in enumerate(zip(a, b)):
        if x != y and start is None:
            start = i
        elif x == y and start is not None:
            out.append((start, i))
            start = None
    if start is not None:
        out.append((start, len(a)))
    return out


class PatchClientTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not EXE:
            raise unittest.SkipTest("no untouched Dawngate.exe (use --exe or DG_PATCH_EXE)")
        cls.orig = read(EXE)
        if sha256(cls.orig) != BUILD_854_SHA256:
            raise unittest.SkipTest(f"{EXE} is not the untouched build 854")
        import pefile
        pe = pefile.PE(data=cls.orig, fast_load=True)
        cls.off = staticmethod(lambda va: pe.get_offset_from_rva(va - pc.IMAGE_BASE))
        cls.vsize_at = pe.sections[0].get_file_offset() + 8
        cls.local, cls.local_summary = pc.patch_bytes(cls.orig, MODULUS)
        cls.a4, cls.a4_summary = pc.patch_bytes(cls.orig, MODULUS, laa=False, preload=False, maptable=False)
        cls.q3b, cls.q3b_summary = pc.patch_bytes(cls.orig, MODULUS, preload=False, maptable=False)
        cls.sndbank, cls.sndbank_summary = pc.patch_bytes(cls.orig, MODULUS, maptable=False)

    def at(self, data, va, n):
        return data[self.off(va):self.off(va) + n]

    def test_local_without_laa_is_a4_output(self):
        self.assertEqual(sha256(self.a4), A4_OUTPUT_SHA256)
        self.assertEqual(self.a4_summary, A4_SUMMARY)

    def test_local_without_preload_is_a4_output_plus_laa(self):
        self.assertEqual(sha256(self.q3b), Q3B_OUTPUT_SHA256)
        self.assertEqual(self.q3b_summary, Q3B_SUMMARY)

    def test_sndbank_is_q3b_output_plus_preload(self):
        self.assertEqual(sha256(self.sndbank), SNDBANK_OUTPUT_SHA256)
        self.assertEqual(self.sndbank_summary, SNDBANK_SUMMARY)
        at = self.off(pc.PRELOAD_COUNT)
        self.assertEqual(diff_ranges(self.q3b, self.sndbank), [(at, at + 4)])  # the fifth byte is 00 in both
        self.assertEqual(self.sndbank[at:at + 5], bytes.fromhex("b80f000000"))
        self.assertEqual((pc.describe(self.q3b)["preload"], pc.describe(self.sndbank)["preload"]), ("as-shipped", "all"))
        self.assertEqual(pc.describe(self.orig)["preload"], "as-shipped")

    def test_local_default_is_sndbank_output_plus_map_cave(self):
        self.assertEqual(sha256(self.local), MAPTABLE_OUTPUT_SHA256)
        self.assertEqual(self.local_summary, MAPTABLE_SUMMARY)
        pe = pc.pefile.PE(data=self.orig, fast_load=True)
        tls_vsize_at = next(s for s in pe.sections if s.Name.startswith(b".tls")).get_file_offset() + 8
        allowed = [(self.off(pc.MAP_MISS), 5), (self.off(pc.MAP_CAVE), len(pc.MAP_CAVE_CODE)),
                   (self.off(pc.MAP_KEY), pc.MAP_KEY_ROOM), (self.vsize_at, 4), (tls_vsize_at, 4)]
        for start, end in diff_ranges(self.sndbank, self.local):
            self.assertTrue(any(s <= start and end <= s + n for s, n in allowed), hex(start))
        self.assertEqual(self.at(self.local, pc.MAP_MISS, 5), bytes.fromhex("e9696db200"))  # jmp 0x011AB140
        self.assertEqual(self.at(self.local, pc.MAP_KEY, 62), pc.wide("^FE_CustomGameLobby!0x00000000"))
        self.assertEqual(self.local[tls_vsize_at:tls_vsize_at + 4], (0x200).to_bytes(4, "little"))
        self.assertEqual(self.local[self.vsize_at:self.vsize_at + 4], (0xDAA200).to_bytes(4, "little"))
        # the cave ends in jmp 0x006843EF (the found path after its push esi; mov esi,<key>)
        cave = pc.MAP_CAVE_CODE
        self.assertEqual(pc.MAP_CAVE + len(cave) + int.from_bytes(cave[-4:], "little", signed=True), pc.MAP_RESUME)
        self.assertEqual(cave[-10:-5], bytes([0xBE]) + pc.MAP_KEY.to_bytes(4, "little"))  # mov esi,<key>
        self.assertEqual([pc.describe(d)["maptable"] for d in (self.orig, self.sndbank, self.local)],
                         ["as-shipped", "as-shipped", "on"])

    def test_laa_sets_only_the_header_bit(self):
        # Q3b: LARGE_ADDRESS_AWARE changes IMAGE_FILE_HEADER.Characteristics 0x0103 -> 0x0123 and nothing else
        for profile, kw in (("local", {}), ("lan", {"server_host": "192.168.1.20"})):
            base, _ = pc.patch_bytes(self.orig, MODULUS, profile, laa=False, **kw)
            laa, summary = pc.patch_bytes(self.orig, MODULUS, profile, **kw)  # the default since Q3b
            at = pc.pefile.PE(data=self.orig, fast_load=True).FILE_HEADER.get_file_offset() + 0x12
            self.assertEqual(diff_ranges(base, laa), [(at, at + 1)])
            self.assertEqual(laa[at:at + 2], (0x0123).to_bytes(2, "little"))
            self.assertEqual((pc.describe(base)["laa"], pc.describe(laa)["laa"]), ("off", "on"))
            self.assertIn("LARGE_ADDRESS_AWARE", summary)

    def test_local_explicit_host_is_the_same(self):
        for kw in ({"host": "127.0.0.1"}, {"server_host": "127.0.0.1"}, {"profile": "local", "host": "127.0.0.1",
                                                                           "server_host": "127.0.0.1"}):
            self.assertEqual(pc.patch_bytes(self.orig, MODULUS, **kw)[0], self.local, kw)

    def test_lan_leaves_the_sockets_as_shipped(self):
        # (the map-name cave left out: it uses .text's slack after the bind cave in every profile)
        lan, summary = pc.patch_bytes(self.orig, MODULUS, "lan", server_host="192.168.1.20", maptable=False)
        self.assertEqual(self.at(lan, pc.REDIRECTOR_HOST, 21), b"192.168.1.20".ljust(21, b"\0"))
        for va in pc.BIND_CALLS:
            self.assertEqual(self.at(lan, va, 6), pc.CALL_BIND)
        self.assertEqual(self.at(lan, pc.BIND_THUNK, 6), pc.JMP_BIND)
        self.assertEqual(self.at(lan, pc.LOOPBACK_CAVE, 0x100), bytes(0x100))
        self.assertEqual(lan[self.vsize_at:self.vsize_at + 4], pc.TEXT_VSIZE.to_bytes(4, "little"))
        # what the lan profile changes at all: modulus, flags, redirector host, the two UPnP patches (since Q3b also
        # the LARGE_ADDRESS_AWARE bit in IMAGE_FILE_HEADER.Characteristics, since SNDBANK the preload call)
        chars = pc.pefile.PE(data=self.orig, fast_load=True).FILE_HEADER.get_file_offset() + 0x12
        changed = {(self.off(pc.OTG3_MODULUS), 128), (self.off(pc.OTG3_FLAGS), 4),
                   (self.off(pc.REDIRECTOR_HOST), 21), (self.off(pc.NOUPNP_JNZ), 1),
                   (self.off(pc.UPNP_DISC), len(pc.UPNP_DISC_NEW)), (chars, 2),
                   (self.off(pc.PRELOAD_COUNT), len(pc.PRELOAD_COUNT_NEW))}
        for start, end in diff_ranges(self.orig, lan):
            self.assertTrue(any(s <= start and end <= s + n for s, n in changed), hex(start))
        self.assertIn("sockets as shipped", summary)
        self.assertIn("redirector host -> 192.168.1.20", summary)

    def test_lan_client_bind_narrows_to_that_address(self):
        lan, summary = pc.patch_bytes(self.orig, MODULUS, "lan", server_host="192.168.1.20", client_bind="192.168.1.30")
        cave = self.at(lan, pc.LOOPBACK_CAVE, len(pc.LOOPBACK_CAVE_CODE))
        self.assertEqual(cave, pc.bind_cave_code("192.168.1.30"))
        self.assertEqual(cave[-10:-6], bytes([192, 168, 1, 30]))
        # identical to the local profile except the redirector host, the cave's address and (A5b) RakNet's
        # IPV4_LOOPBACK pointer to a copy of the bound address in the cave's slack
        ranges = diff_ranges(self.local, lan)
        allowed = [(self.off(pc.REDIRECTOR_HOST), 21), (self.off(pc.LOOPBACK_CAVE) + len(pc.LOOPBACK_CAVE_CODE) - 10, 4),
                   (self.off(pc.LAN_LOOPBACK_STRING), pc.LAN_LOOPBACK_ROOM), (self.off(pc.RAKNET_LOOPBACK_PTR), 4)]
        for start, end in ranges:
            self.assertTrue(any(s <= start and end <= s + n for s, n in allowed), hex(start))
        self.assertIn("sockets narrowed to 192.168.1.30", summary)

    def test_lan_client_bind_points_raknets_send_test_at_that_address(self):
        # A5b: RakPeer::Startup (0x006D0060) sends 4 bytes to SetToLoopback(4)'s address and fails (result 7) when the
        # send fails; Windows refuses a LAN-bound socket's send to 127.0.0.1 (10049). So the pointer names the bound
        # address; the literal keeps 127.0.0.1 for "localhost" and the is-local compare.
        lan, summary = pc.patch_bytes(self.orig, MODULUS, "lan", server_host="192.168.20.40",
                                      client_bind="192.168.20.40")
        self.assertEqual(self.at(lan, pc.RAKNET_LOOPBACK_PTR, 4), pc.LAN_LOOPBACK_STRING.to_bytes(4, "little"))
        self.assertEqual(self.at(lan, pc.LAN_LOOPBACK_STRING, pc.LAN_LOOPBACK_ROOM), b"192.168.20.40".ljust(16, b"\0"))
        self.assertEqual(self.at(lan, pc.RAKNET_LOOPBACK, pc.RAKNET_LOOPBACK_ROOM), b"127.0.0.1".ljust(12, b"\0"))
        self.assertEqual(pc.describe(lan)["raknet_loopback"], "192.168.20.40")
        self.assertIn("RakNet's own-socket test to 192.168.20.40", summary)
        # the string lies after the cave, inside the slack the loopback patch maps (VirtualSize raised to the raw size)
        self.assertLessEqual(pc.LOOPBACK_CAVE + len(pc.LOOPBACK_CAVE_CODE), pc.LAN_LOOPBACK_STRING)
        self.assertEqual(lan[self.vsize_at:self.vsize_at + 4], (0xDAA200).to_bytes(4, "little"))
        # without --client-bind (wildcard binds) the send test reaches its own socket through 127.0.0.1: unchanged
        wide, _ = pc.patch_bytes(self.orig, MODULUS, "lan", server_host="192.168.20.40")
        self.assertEqual(self.at(wide, pc.RAKNET_LOOPBACK_PTR, 4), pc.RAKNET_LOOPBACK.to_bytes(4, "little"))
        self.assertEqual(pc.describe(wide)["raknet_loopback"], "127.0.0.1")

    def test_refusals(self):
        for profile, kw in [
            ("local", {"host": "192.168.1.20"}),
            ("local", {"server_host": "10.0.0.2"}),
            ("local", {"client_bind": "192.168.1.30"}),  # A6: the local profile binds loopback addresses only
            ("local", {"host": "127.0.0.1", "server_host": "127.0.0.2"}),
            ("lan", {}),
            ("lan", {"server_host": "127.0.0.1"}),
            ("lan", {"server_host": "0.0.0.0"}),
            ("lan", {"server_host": "224.0.0.251"}),
            ("lan", {"server_host": "myserver.lan"}),
            ("lan", {"server_host": "192.168.1.20", "client_bind": "127.0.0.1"}),
            ("lan", {"server_host": "192.168.1.20", "client_bind": "0.0.0.0"}),
            ("wan", {"server_host": "192.168.1.20"}),
        ]:
            with self.assertRaises(pc.PatchError, msg=(profile, kw)):
                pc.patch_bytes(self.orig, MODULUS, profile, **kw)
        with self.assertRaises(pc.PatchError):
            pc.patch_bytes(self.orig, MODULUS[:64])

    def test_wrong_build_is_refused_in_every_profile(self):
        for va in (pc.BIND_CALLS[0], pc.BIND_THUNK, pc.REDIRECTOR_HOST, pc.NOUPNP_JNZ, pc.PRELOAD_COUNT, pc.MAP_MISS,
                   pc.MAP_RESUME):
            bad = bytearray(self.orig)
            bad[self.off(va)] ^= 0xFF
            for profile, kw in [("local", {}), ("lan", {"server_host": "192.168.1.20"})]:
                with self.assertRaises(pc.PatchError, msg=(hex(va), profile)):
                    pc.patch_bytes(bytes(bad), MODULUS, profile, **kw)

    def test_main_switches_profiles_from_the_backup(self):
        with tempfile.TemporaryDirectory() as tmp:
            exe = os.path.join(tmp, "Dawngate.exe")
            mod = os.path.join(tmp, "ca_modulus.bin")
            shutil.copyfile(EXE, exe)
            with open(mod, "wb") as f:
                f.write(MODULUS)
            pc.main(["patch_client.py", exe, mod])
            self.assertEqual(sha256(read(exe)), MAPTABLE_OUTPUT_SHA256)
            pc.main(["patch_client.py", exe, mod, "--maptable", "off"])
            self.assertEqual(sha256(read(exe)), SNDBANK_OUTPUT_SHA256)
            pc.main(["patch_client.py", exe, mod, "--preload", "off", "--maptable", "off"])
            self.assertEqual(sha256(read(exe)), Q3B_OUTPUT_SHA256)
            pc.main(["patch_client.py", exe, mod, "--laa", "off", "--preload", "off", "--maptable", "off"])
            self.assertEqual(sha256(read(exe)), A4_OUTPUT_SHA256)
            pc.main(["patch_client.py", exe, mod, "--profile", "lan", "--server-host", "192.168.1.20"])
            lan = read(exe)
            self.assertEqual(self.at(lan, pc.REDIRECTOR_HOST, 12), b"192.168.1.20")
            self.assertEqual(self.at(lan, pc.BIND_THUNK, 6), pc.JMP_BIND)
            self.assertEqual(sha256(read(exe + ".orig")), BUILD_854_SHA256)
            pc.main(["patch_client.py", exe, mod, "127.0.0.1"])  # the A4 command line, back to local
            self.assertEqual(sha256(read(exe)), MAPTABLE_OUTPUT_SHA256)
            with self.assertRaises(SystemExit):  # refused before anything is written
                pc.main(["patch_client.py", exe, mod, "--profile", "lan"])
            self.assertEqual(sha256(read(exe)), MAPTABLE_OUTPUT_SHA256)


class RefusalWithoutExeTest(unittest.TestCase):
    def test_main_refuses_pristine_extractions(self):
        for path in [os.path.join("client", "ia_Dawngate", "Dawngate", "game", "Dawngate.exe"),
                     "D:/x/client/ia_dawngate_1-0-2/Dawngate.exe"]:
            with self.assertRaises(SystemExit) as cm:
                pc.main(["patch_client.py", path, "nonexistent.bin"])
            self.assertIn("pristine", str(cm.exception.code))


class EgressAllowHostTest(unittest.TestCase):
    def test_only_our_own_network_can_be_allowed(self):
        for ok in ["192.168.1.20", "10.0.0.2", "172.16.5.4", "169.254.10.1", "100.64.0.7",
                   "8.8.8.8"]:  # a public server is allowed too (at the operator's own risk)
            self.assertEqual(egress_report.lan_host(ok), ok)
        for bad in ["127.0.0.1", "0.0.0.0", "224.0.0.251", "255.255.255.255",
                    "gosredirector.ea.com", "192.168.1"]:
            with self.assertRaises(Exception, msg=bad):
                egress_report.lan_host(bad)

    def test_allowed_lan_machine_is_not_flagged(self):
        dg = {3659, 57955}
        r = egress_report.outbound_reasons
        self.assertEqual(r("192.168.1.20", "", {57955}, dg), ["Dawngate.exe port [57955]"])
        self.assertEqual(r("192.168.1.20", "", {57955}, dg, ["192.168.1.20"]), [])
        self.assertEqual(r("192.168.1.21", "", {3659}, dg, ["192.168.1.20"]), ["Dawngate.exe port [3659]"])
        self.assertEqual(r("192.168.1.21", "", {51000}, dg, ["192.168.1.20"]), [])  # not the game
        # the EA rules apply to every address, allowed or not
        self.assertEqual(r("159.153.51.18", "", {3659}, dg, ["159.153.51.18"]), ["EA address range"])
        self.assertEqual(r("192.168.1.20", "gosredirector.ea.com", {3659}, dg, ["192.168.1.20"]),
                         ["EA-related name"])

    def test_own_server_name_can_be_allowed(self):
        # VPSLIVE: our public server's name holds the word "dawngate"; only that exact name is exempt from the name rule
        self.assertEqual(egress_report.allowed_name("Dawngate.Example.org."), "dawngate.example.org")
        for bad in ["dawngate.com", "www.dawngate.com", "gosredirector.ea.com", "x.easports.com", "play.waystone.net",
                    "example.org", "dawngate", "bad_name.example.org", "127.0.0.1"]:
            with self.assertRaises(Exception, msg=bad):
                egress_report.allowed_name(bad)
        own = ["dawngate.example.org"]
        self.assertFalse(egress_report.name_flagged("dawngate.example.org", own))
        self.assertTrue(egress_report.name_flagged("dawngate.example.org"))
        self.assertTrue(egress_report.name_flagged("www.dawngate.example.org", own))  # exact names only
        self.assertTrue(egress_report.name_flagged("gosredirector.ea.com", own))
        self.assertFalse(egress_report.name_flagged("example.org", own))
        r = egress_report.outbound_reasons
        self.assertEqual(r("203.0.113.5", "dawngate.example.org", {51000}, {3659}, [], own), [])
        self.assertEqual(r("203.0.113.5", "dawngate.example.org", {51000}, {3659}), ["EA-related name"])
        # the game's own ports to that address still need --allow-host
        self.assertEqual(r("203.0.113.5", "dawngate.example.org", {3659}, {3659}, [], own), ["Dawngate.exe port [3659]"])

    def test_ports_per_protocol(self):
        # C1b (c1b_run7): the game's UDP port number on 127.0.0.1 doesn't make another program's TCP flow its own
        with tempfile.TemporaryDirectory() as d:
            mon = os.path.join(d, "monitor.log")
            with open(mon, "w", encoding="utf-8") as f:
                f.write("14:27:25.794 t=12s UDP bound 127.0.0.1:60576\n"
                        "14:27:26.000 t=13s TCP 127.0.0.1:60577 -> 127.0.0.1:10041\n")
            by = egress_report.dawngate_ports_by_proto(mon)
            self.assertEqual(by, {"TCP": {60577}, "UDP": {60576}})
            self.assertEqual(egress_report.dawngate_ports(mon), {60576, 60577})

    def test_cut_short_capture_is_read(self):
        # P1: play mode stops dumpcap at the end, which can cut the last packet; tshark 4.6.8 then exits 14
        r = egress_report.tshark_result
        self.assertEqual(r(0, ""), (True, ""))
        ok, note = r(14, 'tshark: The file "x.pcapng" appears to have been cut short in the middle of a packet.\n')
        self.assertTrue(ok)
        self.assertIn("cut short", note)
        self.assertEqual(r(2, 'tshark: The file "x.pcapng" doesn\'t exist.'), (False, ""))


if __name__ == "__main__":
    args = sys.argv[1:]
    explicit = None
    if "--exe" in args:
        i = args.index("--exe")
        explicit = args[i + 1]
        del args[i:i + 2]
    EXE = find_exe(explicit)
    print(f"exe: {EXE or 'none (patcher tests skipped)'}")
    unittest.main(argv=[sys.argv[0]] + args)
