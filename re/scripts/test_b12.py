"""Tests for roadmap B12's patch_config.py option --afk-minutes / --afk-disconnect-minutes.

The option raises the client's AFK kick thresholds, the app properties AFKThreshhold (4) and
AFKDisconnectThreshhold (1), in the text app properties (Config.package entry 8C47952D) and the binary ones
(Config_Ship.package entry 08EA246B). These tests run patch_config.py's patch_text and patch_binary on
synthetic entries written in the packages' own formats (the text lines are copied from the shipped
Config.package; no game file is read). They pin:
  - without the option nothing AFK-related changes (the other rules still apply);
  - with it, the values change in both formats and each change is counted (main() checks for 2 per name);
  - an original value other than 4 / 1 stops the script (an unknown config is never patched).
The run on real packages (a scratch copy of the three config packages) is in docs/RUNNING_THE_CLIENT.md
*The AFK kick and the keep-alive (B12)*.

Usage:
    .venv/Scripts/python -I re/scripts/test_b12.py [-v]
"""
import argparse
import os
import struct
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # -I drops the script folder
import patch_config as pcfg  # noqa: E402
import props  # noqa: E402

# the two lines as Config.package entry 8C47952D has them (lines 132-133), plus a telemetry host line
TEXT = ("property AFKThreshhold (hash(AFKThreshhold)) uint 4 # minutes of inactivity before considered AFK\r\n"
        "property AFKDisconnectThreshhold (hash(AFKDisconnectThreshhold)) uint 1 # minutes of being AFK before a "
        "user is disconnected\r\n"
        "property NewTelemetryServerIP (hash(NewTelemetryServerIP)) string8 \"159.153.235.32\"\r\n")


def args(afk=None, disconnect=None):
    return argparse.Namespace(host="127.0.0.1", http_port=18080, telemetry_port=9988, afk_minutes=afk,
                              afk_disconnect_minutes=disconnect, afk_hits=[])


def binary(afk=4, disconnect=1):
    plist = [props.Prop(props.fnv("AFKThreshhold"), 0x0A, 0, struct.pack(">I", afk)),
             props.Prop(props.fnv("AFKDisconnectThreshhold"), 0x0A, 0, struct.pack(">I", disconnect)),
             props.Prop(props.fnv("NewTelemetryServerPort"), 0x0A, 0, struct.pack(">I", 9988))]
    return props.serialize(plist)


def afk_values(data):
    by_id = {p.id: p.int() for p in props.parse(data)}
    return by_id[props.fnv("AFKThreshhold")], by_id[props.fnv("AFKDisconnectThreshhold")]


class AfkOption(unittest.TestCase):
    def test_ids(self):
        self.assertEqual(sorted(pcfg.AFK_IDS), sorted([0x5EEB4D02, 0xFF234C60]))

    def test_text_without_option(self):
        a, changes = args(), []
        out = pcfg.patch_text(TEXT, a, changes, "t")
        self.assertIn("uint 4 # minutes of inactivity", out)
        self.assertIn("uint 1 # minutes of being AFK", out)
        self.assertIn('"127.0.0.1"', out)  # the host rule still applies
        self.assertEqual(a.afk_hits, [])

    def test_text_with_option(self):
        a, changes = args(600, 5), []
        out = pcfg.patch_text(TEXT, a, changes, "t")
        self.assertIn("AFKThreshhold (hash(AFKThreshhold)) uint 600 # minutes of inactivity", out)
        self.assertIn("AFKDisconnectThreshhold (hash(AFKDisconnectThreshhold)) uint 5 # minutes", out)
        self.assertEqual(sorted(a.afk_hits), ["AFKDisconnectThreshhold", "AFKThreshhold"])
        self.assertIn("t AFKThreshhold: 4 -> 600", changes)

    def test_text_only_threshold(self):
        a = args(600)
        out = pcfg.patch_text(TEXT, a, [], "t")
        self.assertIn("uint 600", out)
        self.assertIn("uint 1 # minutes of being AFK", out)
        self.assertEqual(a.afk_hits, ["AFKThreshhold"])

    def test_text_unknown_original_refused(self):
        with self.assertRaises(SystemExit):
            pcfg.patch_text(TEXT.replace("uint 4 #", "uint 7 #"), args(600), [], "t")

    def test_binary_without_option(self):
        a = args()
        self.assertIsNone(pcfg.patch_binary(binary(), a, [], "b"))  # nothing to change
        self.assertEqual(a.afk_hits, [])

    def test_binary_with_option(self):
        a, changes = args(600, 5), []
        out = pcfg.patch_binary(binary(), a, changes, "b")
        self.assertEqual(afk_values(out), (600, 5))
        self.assertEqual(sorted(a.afk_hits), ["AFKDisconnectThreshhold", "AFKThreshhold"])
        self.assertIn("b 0x5eeb4d02: 4 -> 600", changes)

    def test_binary_unknown_original_refused(self):
        with self.assertRaises(SystemExit):
            pcfg.patch_binary(binary(disconnect=2), args(600, 5), [], "b")


if __name__ == "__main__":
    unittest.main()
