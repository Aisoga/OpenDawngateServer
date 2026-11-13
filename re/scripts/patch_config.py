"""Point every network host in a *working copy*'s config packages at local stubs (roadmap item A1).

The client reads its hosts from properties, not from code:
  Config.package      text property files (type 024A0E52). Entry 8C47952D holds the app properties,
                      including the "Nightly Release" overrides at its end that the shipped client
                      actually uses (EA's CDN rules URL ".../live/...", telemetry 159.153.235.32:9988).
                      Entry B16AFE42 holds Spore-era hosts (pollinator, community, YouTube).
  Config_Ship.package binary property lists (type 00B1B104, see props.py). Entry 08EA246B is the
                      compiled form of the same app properties, but with the pre-override values
                      (".../test/..." rules URL, telemetry 159.153.239.205). Patched as well, in case
                      some path reads it without the text overrides.
  Config_Pack.package binary property lists too (roadmap B6). Entry 9DD4AB9B holds the EAWebKit
                      settings, among them WebkitAllowedDomains (0xC27EA5F8), the 43 domains the
                      main menu's home page view may open (".dawngate.com", "cdn.strategy.ea.com",
                      ...). SPEAWebKit_Init (0x00B55770) hands each to EAWebKit's allowed-domain list.

Every string property (string8/string16) whose value is
  - a URL           becomes http://<host>:<http-port><original path>   (scheme and authority replaced)
  - an IPv4 address or a host name ending in .com/.net/.org/.tv   becomes <host>
and NewTelemetryServerPort becomes <telemetry-port>. That covers NewTelemetryServerIP/Port,
NewTelemetryServerConfigURL (telemetry_rules.xml from cdn.strategy.ea.com), WebContentUrlRoot*Template,
CrashReportingServerURL, NewTelemetryServer_DebugTelemetryURL, the old TelemetryServer* hosts and
NetworkBootstrapURL. Comment lines are left alone. Changed entries are stored uncompressed.

Since B6, <host> is also added to WebkitAllowedDomains, since the home page now comes from our server
(~webContentUrlRootCommon~/home.html, docs/WEB_CONTENT.md). The other entries stay: the page is ours
and loads nothing from other hosts. That EAWebKit blocks a host missing from the list is an assumption
(no session ever requested home.html); B6-live checks it.

For a server on another machine (roadmap A5, the lan profile of patch_client.py) pass its LAN address as
--host; the default stays 127.0.0.1 (docs/RUNNING_THE_CLIENT.md "Playing across machines (A5)").

--afk-minutes N [--afk-disconnect-minutes M] (roadmap B12, opt-in) also raises the client's AFK kick: the app
properties AFKThreshhold (minutes without input in a game before the warning, 4) and AFKDisconnectThreshhold (minutes
from the warning to the disconnect and logout, 1), uint32 in both Config.package's text app properties (entry
8C47952D, the values the client uses) and Config_Ship.package's binary ones (entry 08EA246B). GameClient_Init
(0x0066D8D0) reads them once at start-up. Each value must be the original (4 / 1) in the .orig, else the script stops;
N and M are 1..10080 (7 days: the stopwatch deadline is minutes x 60 x QueryPerformanceFrequency in an int64). Without
the options the originals stay, so a later run without them takes the change back. The working copy client\run_854 is
also the copy play.ps1 plays from, so this changes the idle kick for manual play too; the default way to keep test
sessions alive is launch_monitor.ps1's -KeepAlive key (docs/RUNNING_THE_CLIENT.md *The AFK kick and the keep-alive
(B12)*).

Always: the option
OptionDragGrab, the Options menu's grab-style drag scroll, is defined as the game defines its own gameplay booleans:
in the compiled options of Config_Ship.package (the index 00B1B104-40470000-0 gets the option, two new entries its
settings, which set the bool app property OptionDragGrab the exe's drag cave reads; off is the default) and with its
Preferences.prop name in Config.package's OptionProperties (2CB7250D, DRAG_GRAB_LINES). Without them the game knows
no such option: the options code can't store it and drops it from Preferences.prop. Inert without patch_client.py's
drag grab (nothing reads it). --drag-option-only adds it to an already patched copy in place.

The first run saves <name>.package.orig next to each package (the suffix keeps it out of the
client's "*.package" scan); every run patches from that backup, so re-running is safe and
`--restore` puts the originals back. Refuses to touch the pristine extractions under client/ia_*.

Usage:
    .venv/Scripts/python -I re/scripts/patch_config.py <working copy game/Data dir>
        [--host 127.0.0.1] [--http-port 18080] [--telemetry-port 9988]
        [--afk-minutes N [--afk-disconnect-minutes M]] [--dry-run | --restore | --drag-option-only]
"""
import argparse
import os
import re
import shutil
import struct
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import dbpf  # noqa: E402
import props  # noqa: E402

TEXT_TYPE, BINARY_TYPE = 0x024A0E52, 0x00B1B104
PACKAGES = ("Config.package", "Config_Ship.package", "Config_Pack.package")
TELEMETRY_PORT_ID = props.fnv("NewTelemetryServerPort")
ALLOWED_DOMAINS_ID = props.fnv("WebkitAllowedDomains")  # 0xC27EA5F8, Config_Pack.package 9DD4AB9B
# B12: the AFK kick's app properties (uint32 minutes): name -> (original value, option attribute)
AFK = {"AFKThreshhold": (4, "afk_minutes"), "AFKDisconnectThreshhold": (1, "afk_disconnect_minutes")}
AFK_IDS = {props.fnv(n): n for n in AFK}  # 0x5EEB4D02, 0xFF234C60
AFK_MAX = 10080  # minutes (7 days)
# DRAGGRAB2b: the option OptionDragGrab (the Options menu's grab-style drag scroll), defined as the game defines its own
# gameplay booleans. The game takes its options from their compiled form in Config_Ship.package (the config manager's
# 0x009624B0; Config.package's text option definitions, entry F6D4A4B9, are not read), so the option goes there:
#   - the index 00B1B104-40470000-0 holds three uint32 arrays, the option ids (0x05DAAFFF), each option's first
#     setting (0x05DAB000) and its number of settings (0x05DAB001): + OptionDragGrab, 0, 2;
#   - each setting is the property list 00B1B104-(0x40470100 + setting)-<option id> the setting applies to the app
#     properties: OptionDragGrab's two set the bool app property OptionDragGrab (the key the exe's drag cave reads) to
#     false ($Off, setting 0, the default: a new option starts at 0) and true.
# Config.package's text gets one line: the option's name in the player options stored in Preferences.prop (instance ->
# the line it goes after, the last line of the entry's gameplay group in build 854, and the line). A `setOption` line
# for an option the compiled data lacks stops the config script ("Configuration Script Failed", alert 2000).
DRAG_GRAB_LINES = (
    (0x2CB7250D, "property OptionShowAttackMoveRange",  # the player options stored in Preferences.prop (the name)
     'property OptionDragGrab                         (hash("OptionDragGrab")) uint32'),
)
DRAG_GRAB_ID = props.fnv("OptionDragGrab")         # 0x14958012: the option, its app property and the exe table's key
OPTIONS_GROUP, OPTION_SETTING_GROUP = 0x40470000, 0x40470100
OPTION_IDS, OPTION_FIRST, OPTION_COUNT = 0x05DAAFFF, 0x05DAB000, 0x05DAB001


def drag_option_setting(value):
    """OptionDragGrab's setting `value` (0 off, 1 on): one bool property, OptionDragGrab = value (as EA's compiled
    boolean options, e.g. OptionFilterProfanity's)."""
    return struct.pack(">IIHHB", 1, DRAG_GRAB_ID, 0x01, 0, value)


def add_drag_option_index(data, where):
    """The options index (00B1B104-40470000-0) with OptionDragGrab appended (id, first setting 0, 2 settings). Exits
    on an index that isn't build 854's form (three uint32 arrays of the same length) or already has the option."""
    plist = props.parse(data)
    arrays = {p.id: p for p in plist}
    want = (OPTION_IDS, OPTION_FIRST, OPTION_COUNT)
    if (sorted(arrays) != sorted(want) or any(arrays[i].type != 0x0A or not arrays[i].is_array for i in want)
            or len({arrays[i].raw[:8] for i in want}) != 1 or arrays[OPTION_IDS].raw[4:8] != b"\0\0\0\4"):
        sys.exit(f"{where}: not the options index of build 854; not patching an unknown config")
    n = struct.unpack_from(">I", arrays[OPTION_IDS].raw)[0]
    if DRAG_GRAB_ID in struct.unpack_from(f">{n}I", arrays[OPTION_IDS].raw, 8):
        sys.exit(f"{where}: OptionDragGrab is already an option; not patching an unknown config")
    for pid, value in ((OPTION_IDS, DRAG_GRAB_ID), (OPTION_FIRST, 0), (OPTION_COUNT, 2)):
        a = arrays[pid]
        a.raw = struct.pack(">II", n + 1, 4) + a.raw[8:] + struct.pack(">I", value)
    return props.serialize(plist)

URL = re.compile(r"^[A-Za-z][A-Za-z0-9+.-]*://[^/\s]*(/\S*)?$")
IPV4 = re.compile(r"^\d{1,3}(\.\d{1,3}){3}$")
HOSTNAME = re.compile(r"^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.(com|net|org|tv)$", re.IGNORECASE)
# property <name> <id: (hash(name)) | 0x.. | decimal> <type> <value>  (value optionally quoted)
TEXT_PROP = re.compile(r'^(?P<pre>[ \t]*property[ \t]+(?P<name>\S+)[ \t]+\S+[ \t]+(?P<type>\w+)[ \t]+)'
                       r'(?P<q>"?)(?P<val>[^"\s]*)(?P=q)', re.MULTILINE)


def localize(value, host, http_port):
    """Return the local replacement for a host/URL value, or None if it is neither."""
    m = URL.match(value)
    if m:
        return f"http://{host}:{http_port}{m.group(1) or '/'}"
    if IPV4.match(value) or HOSTNAME.match(value):
        return host
    return None


def afk_value(name, old, args, where):
    """B12: the new value of an AFK property, or None when its option isn't given. Stops on an unknown original."""
    orig, opt = AFK[name]
    want = getattr(args, opt)
    if want is None:
        return None
    if old != orig:
        sys.exit(f"{where} {name}: original value {old}, expected {orig}; not patching an unknown config")
    args.afk_hits.append(name)
    return want


def patch_text(text, args, changes, where):
    def sub(m):
        name, typ, val = m.group("name"), m.group("type"), m.group("val")
        new = None
        if typ in ("string8", "string16"):
            new = localize(val, args.host, args.http_port)
        elif name == "NewTelemetryServerPort" and typ in ("int", "uint"):
            new = str(args.telemetry_port)
        elif name in AFK and typ == "uint":
            v = afk_value(name, int(val) if val.isdigit() else val, args, where)
            new = None if v is None else str(v)
        if new is None or new == val:
            return m.group(0)
        changes.append(f"{where} {name}: {val} -> {new}")
        return f'{m.group("pre")}{m.group("q")}{new}{m.group("q")}'
    return TEXT_PROP.sub(sub, text)


def patch_binary(data, args, changes, where):
    plist = props.parse(data)
    changed = False
    for p in plist:
        if p.id == ALLOWED_DOMAINS_ID and p.type == props.STRING8 and p.is_array:
            domains = p.strings()
            if args.host not in domains:
                p.set_strings(domains + [args.host])
                changes.append(f"{where} WebkitAllowedDomains: + {args.host} ({len(domains)} -> {len(domains) + 1})")
                changed = True
            continue
        if p.type in (props.STRING8, props.STRING16) and not p.is_array:
            old = p.string()
            new = localize(old, args.host, args.http_port)
            if new is not None and new != old:
                p.set_string(new)
        elif p.id == TELEMETRY_PORT_ID and p.type in (0x09, 0x0A) and not p.is_array:
            old = p.int()
            new = args.telemetry_port
            if new != old:
                p.set_int(new)
        elif p.id in AFK_IDS and p.type == 0x0A and not p.is_array:
            old = p.int()
            new = afk_value(AFK_IDS[p.id], old, args, where)
            if new is None:
                continue
            if new != old:
                p.set_int(new)
        else:
            continue
        if new is not None and new != old:
            changes.append(f"{where} {p.id:#010x}: {old} -> {new}")
            changed = True
    return props.serialize(plist) if changed else None


def add_drag_grab(text, instance, changes, where):
    """DRAGGRAB2b: `text` with DRAG_GRAB_LINES' line for this entry after its anchor line (a line that starts with the
    anchor after blanks; the new line takes the anchor's line end, \\r\\n when it has none); other entries unchanged.
    Exits on an entry without exactly one anchor line (not build 854's config)."""
    for inst, anchor, line in DRAG_GRAB_LINES:
        if inst != instance:
            continue
        hits = list(re.finditer(r"(?m)^[ \t]*" + re.escape(anchor) + r"[^\r\n]*(\r\n|\n|\r)?", text))
        if len(hits) != 1:
            sys.exit(f"{where}: {len(hits)} lines start with {anchor!r}, expected 1; not patching an unknown config")
        m = hits[0]
        add = line + m.group(1) if m.group(1) else "\r\n" + line
        changes.append(f"{where} OptionDragGrab: + {line}")
        return text[:m.end()] + add + text[m.end():]
    return text


def patched_package(src, args, name):
    """Patch the package file `src` (normally the .orig) in memory; writes nothing.

    Returns (new bytes or None when nothing changes, the change lines, the number of changed entries). `name` is the
    package's file name in the change lines. patch_package writes the result; setup_all.py compares it with a working
    copy's package to tell whether the copy is current (dbpf.rebuild is deterministic)."""
    pkg = dbpf.Package(src)
    replacements, changes, added = {}, [], []
    for e in pkg.entries:
        where = f"{name} {e.key()}"
        data = pkg.read(e)
        if e.type == TEXT_TYPE:
            text = data.decode("latin-1")  # byte-preserving; the files are ASCII
            new = add_drag_grab(patch_text(text, args, changes, where), e.instance, changes, where)
            if new != text:
                replacements[e.key()] = new.encode("latin-1")
        elif e.type == BINARY_TYPE:
            new = patch_binary(data, args, changes, where)
            if e.group == OPTIONS_GROUP and e.instance == 0:  # DRAGGRAB2b: the compiled options' index
                new = add_drag_option_index(data if new is None else new, where)
                changes.append(f"{where} OptionDragGrab: + option {DRAG_GRAB_ID:#010x} (2 settings)")
                for value in (0, 1):
                    added.append((BINARY_TYPE, OPTION_SETTING_GROUP + value, DRAG_GRAB_ID, drag_option_setting(value)))
                    changes.append(f"{name} {BINARY_TYPE:08X}-{OPTION_SETTING_GROUP + value:08X}-"
                                   f"{DRAG_GRAB_ID:016X} OptionDragGrab: + setting {value} (OptionDragGrab = "
                                   f"{('false', 'true')[value]})")
            if new is not None:
                replacements[e.key()] = new
    if not replacements and not added:
        return None, changes, 0
    return dbpf.rebuild(pkg, replacements, added), changes, len(replacements) + len(added)


def patch_package(path, args):
    orig = path + ".orig"
    if not os.path.exists(path) and not os.path.exists(orig):
        print(f"{path}: missing, skipped")
        return
    if args.restore:
        if os.path.exists(orig):
            shutil.copy2(orig, path)
            print(f"restored {path}")
        return
    if not os.path.exists(orig) and not args.dry_run:
        shutil.copy2(path, orig)
        print(f"saved original as {orig}")
    data, changes, entries = patched_package(orig if os.path.exists(orig) else path, args, os.path.basename(path))
    for c in changes:
        print("  " + c)
    if data is None:
        print(f"{path}: nothing to change")
        return
    if args.dry_run:
        print(f"{path}: {len(changes)} changes in {entries} entries (dry run, not written)")
        return
    with open(path, "wb") as f:
        f.write(data)
    print(f"patched {path}: {len(changes)} changes in {entries} entries")


def drag_option_state(data_dir):
    """DRAGGRAB2b: "on" when the copy's config defines OptionDragGrab as this script does (Config.package's name line,
    Config_Ship.package's compiled option), "missing" when neither, "broken" otherwise (half of it, or the first
    DRAGGRAB2b attempt's text lines, whose setOption stopped the config script)."""
    with open(os.path.join(data_dir, "Config.package"), "rb") as f:
        text = f.read()
    ship = dbpf.Package(os.path.join(data_dir, "Config_Ship.package"))
    setting = f"{BINARY_TYPE:08X}-{OPTION_SETTING_GROUP:08X}-{DRAG_GRAB_ID:016X}"
    named, compiled = b"property OptionDragGrab " in text, any(e.key() == setting for e in ship.entries)
    if b"setOption OptionDragGrab" in text or b"boolean_option_MACRO(OptionDragGrab" in text or named != compiled:
        return "broken"
    return "on" if named else "missing"


def add_drag_option(data_dir):
    """--drag-option-only: add the option to an already patched copy in place (its hosts, ports and AFK values stay as
    they are): DRAG_GRAB_LINES into Config.package, the compiled option into Config_Ship.package. The result is byte
    for byte what a full patch with the same options makes (dbpf.rebuild keeps every other entry's bytes). A copy
    whose config is "broken" (drag_option_state) needs the full patch."""
    paths = [os.path.join(data_dir, n) for n in ("Config.package", "Config_Ship.package")]
    for path in paths:
        if not os.path.exists(path + ".orig"):
            sys.exit(f"{path}: not patched yet (no .orig); run patch_config.py without --drag-option-only")
    state = drag_option_state(data_dir)
    if state == "on":
        print(f"{data_dir}: OptionDragGrab already defined")
        return
    if state == "broken":
        sys.exit(f"{data_dir}: OptionDragGrab is half defined; run patch_config.py with the copy's options (from .orig)")
    for path in paths:
        pkg, name = dbpf.Package(path), os.path.basename(path)
        replacements, changes, added = {}, [], []
        for e in pkg.entries:
            where = f"{name} {e.key()}"
            if e.type == TEXT_TYPE:
                text = pkg.read(e).decode("latin-1")
                new = add_drag_grab(text, e.instance, changes, where)
                if new != text:
                    replacements[e.key()] = new.encode("latin-1")
            elif e.type == BINARY_TYPE and e.group == OPTIONS_GROUP and e.instance == 0:
                replacements[e.key()] = add_drag_option_index(pkg.read(e), where)
                added = [(BINARY_TYPE, OPTION_SETTING_GROUP + v, DRAG_GRAB_ID, drag_option_setting(v)) for v in (0, 1)]
                changes.append(f"{where} OptionDragGrab: + option {DRAG_GRAB_ID:#010x} (2 settings)")
        want = len(DRAG_GRAB_LINES) if name == "Config.package" else 1
        if len(replacements) != want:
            sys.exit(f"{path}: found {len(replacements)} of the {want} entries; not patching an unknown config")
        for c in changes:
            print("  " + c)
        with open(path, "wb") as f:
            f.write(dbpf.rebuild(pkg, replacements, added))
        print(f"patched {path}: OptionDragGrab added ({len(replacements) + len(added)} entries)")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("data_dir", help="working copy game/Data directory")
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--http-port", type=int, default=18080, help="stub HTTP logger (CDN, crash, debug telemetry)")
    ap.add_argument("--telemetry-port", type=int, default=9988, help="stub telemetry TCP logger")
    ap.add_argument("--afk-minutes", type=int, metavar="N",
                    help=f"B12: AFKThreshhold, minutes without input before the AFK warning (1..{AFK_MAX}; original 4)")
    ap.add_argument("--afk-disconnect-minutes", type=int, metavar="M",
                    help=f"B12: AFKDisconnectThreshhold, minutes from the warning to the kick (1..{AFK_MAX}; original 1)")
    mode = ap.add_mutually_exclusive_group()
    mode.add_argument("--dry-run", action="store_true", help="only list what would change")
    mode.add_argument("--restore", action="store_true", help="copy the .orig backups back")
    mode.add_argument("--drag-option-only", action="store_true",
                      help="DRAGGRAB2b: only add the option OptionDragGrab to an already patched copy's Config.package "
                           "and Config_Ship.package, in place (keeps its hosts and AFK values; nothing when it has it)")
    args = ap.parse_args()
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    for name, (_, opt) in AFK.items():
        v = getattr(args, opt)
        if v is not None and not 1 <= v <= AFK_MAX:
            sys.exit(f"--{opt.replace('_', '-')} {v}: must be 1..{AFK_MAX} minutes")
    if args.afk_disconnect_minutes is not None and args.afk_minutes is None:
        sys.exit("--afk-disconnect-minutes needs --afk-minutes")
    args.afk_hits = []
    data_dir = os.path.abspath(args.data_dir)
    if os.sep + "ia_" in data_dir or "/ia_" in data_dir:
        sys.exit("refusing to patch a pristine extraction (client/ia_*); use a working copy")
    if args.drag_option_only:
        add_drag_option(data_dir)
        return
    for name in PACKAGES:
        patch_package(os.path.join(data_dir, name), args)
    if not args.restore:
        # B12: each requested AFK value must have been found twice, as text (Config.package) and binary (Config_Ship)
        for name, (_, opt) in AFK.items():
            if getattr(args, opt) is not None and args.afk_hits.count(name) != 2:
                sys.exit(f"{name}: found {args.afk_hits.count(name)} time(s), expected 2 (Config.package text and "
                         f"Config_Ship.package binary app properties)")


if __name__ == "__main__":
    main()
