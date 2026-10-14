"""Set up everything the test sessions and play.ps1 need, from the archive.org download, in one idempotent command
(the guide is docs/development/building.md).

Steps, in order. Each one checks its result, is skipped when its result is current already, and prints one line:
  tools          Python with pefile and cryptography (this script runs under .venv's python -I), Git for Windows'
                 openssl (make_certs.py signs with MD5), Go (dgserver; server/go.mod's version or newer), Visual Studio
                 2022 with the x86 C++ compiler and its bundled CMake (gms/). Wireshark with Npcap is only noted
                 (play.ps1 -NoCapture plays without it). A missing tool fails only the steps that need it.
  archive        client/ia_Dawngate from Dawngate.zip (archive.org; read only): extracted with the archive's file times
                 into client/ia_Dawngate.partial, every file checked against docs/file_manifest.csv (size and SHA-256
                 of build 1.0.903.854), then renamed. An existing client/ia_Dawngate is only read: the sizes of all
                 167 files and the SHA-256 of the files the patchers and the host copy start from (--verify: all).
  certificates   server/certs (server/tools/make_certs.py, CN 127.0.0.1); with --lan-certs <S> also server/certs/lan
                 (CN <S>) for clients on other machines. A set with another CN is never replaced unless --new-certs
                 (the client copy is then patched again for the new CA).
  client copy    client/run_854/Dawngate, copied from client/ia_Dawngate through a .partial folder; an existing copy
                 gets missing or truncated files of the build back from client/ia_Dawngate.
  client exe     re/scripts/patch_client.py, local profile, our CA. Current means byte for byte what patch_client.py
                 makes from the untouched exe (Dawngate.exe.orig, which must match the manifest).
  client config  re/scripts/patch_config.py --host 127.0.0.1; current means byte for byte (Config, Config_Ship,
                 Config_Pack; their .orig must match the manifest).
  client texts   re/scripts/patch_locale.py --host 127.0.0.1 [--web-domain D]: the added locale package
                 game/Data/Locale/en-us/AA_Server.package (the login screen's texts, its links at the server's pages),
                 made from the copy's own Locale.package; current means byte for byte (the Go setup's are the same).
  RakNet         tools/raknet/RakNet (Source/ and the licence files) from the facebookarchive/RakNet archive of commit
                 1a169895 (--raknet-zip; SHA-256 checked). This script downloads nothing.
  gms build      gms/build (cmake -G "Visual Studio 17 2022" -A Win32 -DRAKNET_ROOT=..., Release): dgsim.dll and
                 gms_script.exe newer than their sources (run_session.ps1's rule). An output still older after the
                 build (a header-only change) is deleted and built again.
  dgserver       server/bin/dgserver.exe (go build with GOTOOLCHAIN=local) newer than every source file under server/.
  host copy      client/run_854_server/Dawngate: copied like the client copy, patched (exe: patched, with the
                 redirector and data folders of the local profile, as setup_lane.py checks; config: patched), then
                 re/scripts/setup_server_copy.py installs the current dgsim.dll as PhysXLoader.dll, dgsim.ini and the
                 Lua probes.
  content        server/data/content.json in the server's current format: dgserver --make-content from an AssetData
                 dump (snap.py extract and assetdata.py dump into re/out/). --no-content leaves it out (new accounts
                 then own no items).
  RakNet probe   only with --probe: tools/raknet/build/Release/raknet_probe_p5.exe (run_session.ps1 -GmsProbe
                 silent|connected, a research tool).
  lane b, c, d   only with --lane <X>[,<Y>...] (repeatable; --lane-b = --lane b): re/scripts/setup_lane.py X for each
                 named lane (the extra test lanes; about 3 GB each).
  copy e, f, h, lan only with --lane e / f / h / lan (A5b): setup_lane.py e, lane d's second client client/run_854_e
                 (run_session.ps1 -SecondClient; after lane d; since G3b f, lane b's client/run_854_f, after lane b;
                 h, lane c's client/run_854_h, after lane c), and setup_lane.py lan --address <S>, the LAN
                 profile's test copy client/run_854_l (run_session.ps1 -Lan; needs --lan-certs <S>, this PC's LAN address);
                 about 1.5 GB each.

Player mode (--player --server-host <S>): only the archive and a client copy for a server elsewhere on our own
network, client/run_854_lan/Dawngate, patched by patch_client.py --profile lan --server-host <S> [--client-bind <C>]
with the server's CA modulus (--ca; default server/certs/lan/ca_modulus.bin), by patch_config.py --host <S> and
by patch_locale.py --host <S>. It is
played with play.ps1 -Server <S> (re/scripts/play_lan.ps1, roadmap P2; -GameDir for a copy made with --client).

--check only reports and writes nothing: exit 1 when something is missing or stale. Exit 2 when a step failed or a
tool it needs is missing. client/ia_* is never written once it exists, the zips never.

Where things go: the copies, certificates, server data, RakNet and re/out/ under the state root (default: the main
checkout, also when this script runs from a git worktree, as run_session.ps1 does); the builds (gms/build,
server/bin) under the build root (default: the checkout this script is in). --root <dir> puts both under <dir>
(the scratch test; run_session.ps1 and play.ps1 only use the checkout's own folders), --build-root moves the builds.

Usage (from the repository folder):
    .venv\\Scripts\\python -I re\\scripts\\setup_all.py [--zip <Dawngate.zip>] [--raknet-zip <zip>] [--lan-certs <S>]
        [--no-content] [--probe] [--lane b|c|d|e|lan[,...]] [--lane-b] [--verify] [--new-certs] [--check]
    .venv\\Scripts\\python -I re\\scripts\\setup_all.py --player --server-host <S> [--ca <ca_modulus.bin>]
        [--client-bind <C>] [--check]
    more: --root <dir>, --build-root <dir>, --client <Dawngate dir>, --afk-minutes N [--afk-disconnect-minutes M],
        --web-domain <D>
Tests: re/scripts/test_h3.py (synthetic archives, no game file), re/scripts/test_ph2b.py (the client texts).
"""
import argparse
import csv
import glob
import hashlib
import ipaddress
import json
import os
import re
import shutil
import subprocess
import sys
import time
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)  # -I drops the script folder
import extract_client  # noqa: E402  (stdlib only; patch_client and patch_config are imported once pefile is known)

GAME_ZIP_NAME = "Dawngate.zip"
GAME_ZIP_SIZE = 1310588859  # https://archive.org/details/Dawngate, as downloaded on 2026-10-07
GAME_ZIP_SHA256 = "56ae93939904605ac3145cb55ace854482f40bf128991a0a21ba56f14d4bdae1"
RAKNET_COMMIT = "1a169895a900c9fc4841c556e16514182b75faf8"
RAKNET_ZIP_NAME = f"RakNet-{RAKNET_COMMIT}.zip"
RAKNET_ZIP_SHA256 = "60b53557aa084fb5455c8e2e7efe309738dabe7f9db5c0bc103cf41c8f97c0e0"
RAKNET_URL = f"https://github.com/facebookarchive/RakNet/archive/{RAKNET_COMMIT}.zip"
RAKNET_KEEP = ("LICENSE", "PATENTS", "README.md")  # besides Source/: the notice binaries must carry
OPENSSL = r"C:\Program Files\Git\usr\bin\openssl.exe"  # server/tools/make_certs.py's
VSWHERE = r"C:\Program Files (x86)\Microsoft Visual Studio\Installer\vswhere.exe"
LOCAL = "127.0.0.1"
EXE = "game/Dawngate.exe"
CONFIGS = ("game/Data/Config.package", "game/Data/Config_Ship.package", "game/Data/Config_Pack.package")
LOADER = "game/PhysXLoader.dll"
PATCHED = (EXE,) + CONFIGS
KEY_FILES = PATCHED + (LOADER,)  # hashed in every check of client/ia_Dawngate
CERT_FILES = ("ca_key.pem", "ca_cert.pem", "ca_modulus.bin", "server_key.pem", "server_cert.pem")
HOST_KEYS = ("redirector", "appdata", "documents")  # what a host copy's exe must carry (setup_lane.py's rule)
EXTRA_LANES = ("b", "c", "d")  # A6, A6b: the lanes setup_lane.py makes (its LANES but a; test_a6.py checks they agree)
# A5b: further copies setup_lane.py makes: lane d's second client (its EXTRA_CLIENTS; since G3b lane b's, f) and the LAN
# profile's test copy
EXTRA_COPIES = ("e", "f", "h", "lan")
GB = 1 << 30
COPY_BYTES = int(1.6 * GB)  # one copy of the build (1.53 GB)
MARGIN = 1 * GB

OK, DONE, NOTE, SKIP = "ok", "done", "note", "skipped"
MISSING, STALE, FAILED = "MISSING", "STALE", "FAILED"
BAD = (MISSING, STALE, FAILED)


# ---- helpers without state (test_h3.py uses them) ------------------------------------------------------------------

def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def read(path):
    with open(path, "rb") as f:
        return f.read()


def load_manifest(path):
    """docs/file_manifest.csv's build-854 side: {"Dawngate/game/...": (size, sha256)} (Dawngate.zip's 167 files)."""
    files = {}
    with open(path, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            if row["size_A"]:
                files[row["path"]] = (int(row["size_A"]), row["sha256_A"])
    return files


def in_copy(rel):
    """A manifest path ("Dawngate/game/x") as a path inside a copy's Dawngate folder ("game/x")."""
    return rel.split("/", 1)[1]


def verify_tree(base, manifest, full=False, key_files=KEY_FILES):
    """Problems of an extracted build under `base` (which holds Dawngate/): missing files, wrong sizes, and wrong
    SHA-256 for the key files (every file with `full`). Extra files are allowed."""
    bad = []
    for rel, (size, digest) in sorted(manifest.items()):
        f = os.path.join(base, *rel.split("/"))
        if not os.path.isfile(f):
            bad.append(f"{rel} is missing")
        elif os.path.getsize(f) != size:
            bad.append(f"{rel} has {os.path.getsize(f)} bytes, want {size}")
        elif (full or in_copy(rel) in key_files) and sha256(f) != digest:
            bad.append(f"{rel} differs (SHA-256)")
    return bad


def check_game_zip(z, top="Dawngate", max_bytes=4 * GB, max_entries=2000):
    """Refuse an archive that isn't one Dawngate/ install folder (paths outside it, absolute or '..', too big)."""
    infos = z.infolist()
    total = 0
    for i in infos:
        parts = i.filename.split("/")
        if parts[0] != top or i.filename.startswith("/") or ".." in parts or ":" in i.filename or "\\" in i.filename:
            raise ValueError(f"unexpected entry {i.filename!r} (everything must be under {top}/)")
        total += i.file_size
    if not infos or len(infos) > max_entries:
        raise ValueError(f"{len(infos)} entries (Dawngate.zip has 185)")
    if total > max_bytes:
        raise ValueError(f"{total} bytes uncompressed")
    return infos


def extract_raknet(zip_path, dest):
    """Extract RakNet's Source/ and licence files from the GitHub archive (one top folder) into `dest`, without the
    top folder, keeping the archive's file times. Returns the number of files."""
    n = 0
    with zipfile.ZipFile(zip_path) as z:
        infos = z.infolist()
        tops = {i.filename.split("/", 1)[0] for i in infos}
        if len(tops) != 1:
            raise ValueError(f"expected one top folder, found {sorted(tops)[:5]}")
        for i in infos:
            parts = i.filename.split("/")
            rel = parts[1:]
            if i.is_dir() or not rel or not rel[-1]:
                continue
            if not (rel[0] == "Source" or (len(rel) == 1 and rel[0] in RAKNET_KEEP)):
                continue
            if ".." in rel or any(":" in p or "\\" in p for p in rel) or i.filename.startswith("/"):
                raise ValueError(f"unexpected entry {i.filename!r}")
            out = os.path.join(dest, *rel)
            os.makedirs(os.path.dirname(out), exist_ok=True)
            with z.open(i) as src, open(out, "wb") as dst:
                shutil.copyfileobj(src, dst)
            t = time.mktime(i.date_time + (0, 0, -1))
            os.utime(out, (t, t))
            n += 1
    return n


def raknet_version(raknet_root):
    """RAKNET_VERSION from Source/RakNetVersion.h, or None."""
    h = os.path.join(raknet_root, "Source", "RakNetVersion.h")
    if not os.path.isfile(h):
        return None
    m = re.search(r'#define RAKNET_VERSION "([^"]+)"', read(h).decode("latin-1"))
    return m.group(1) if m else "?"


def newer_source(dirs, t, skip_ext=(), skip_parts=()):
    """The first file under `dirs` modified after time `t` (skipping extensions and path fragments), or None."""
    for d in dirs:
        for dp, _dn, fn in os.walk(d):
            for name in fn:
                p = os.path.join(dp, name)
                low = p.replace("/", "\\").lower()
                if os.path.splitext(name)[1].lower() in skip_ext or any(s in low for s in skip_parts):
                    continue
                if os.path.getmtime(p) > t:
                    return p
    return None


def gms_stale(src, build_dir):
    """run_session.ps1's rules (Assert-HostDll, Assert-ScriptExe): [(output, why)] for dgsim.dll and gms_script.exe."""
    out = []
    j = os.path.join
    rules = (("dgsim.dll", ("host", "engine", "core", "script"), (".lua",)),
             ("gms_script.exe", ("core", "script"), ()))
    for name, dirs, skip_ext in rules:
        p = j(build_dir, "Release", name)
        if not os.path.isfile(p):
            out.append((p, "not built"))
            continue
        newer = newer_source([j(src, d) for d in dirs], os.path.getmtime(p), skip_ext, ("\\core\\control\\tests\\",))
        if newer:
            out.append((p, f"older than {os.path.relpath(newer, src)}"))
    return out


def server_stale(server_src, exe):
    """dgserver.exe is stale when it is missing or older than any file of the Go module (tests and outputs aside)."""
    if not os.path.isfile(exe):
        return "not built"
    skip = tuple(f"\\server\\{d}\\" for d in ("bin", "certs", "data", "logs", "testdata", "tools"))
    newer = newer_source([server_src], os.path.getmtime(exe), (), skip + ("_test.go",))
    return f"older than {os.path.relpath(newer, server_src)}" if newer else None


def cmake_cache(build_dir):
    """{key: value} of build_dir/CMakeCache.txt (empty when there is none)."""
    p = os.path.join(build_dir, "CMakeCache.txt")
    vals = {}
    if os.path.isfile(p):
        for line in read(p).decode("utf-8", "replace").splitlines():
            m = re.match(r"^([A-Za-z_][\w.-]*):[A-Z]+=(.*)$", line)
            if m:
                vals[m.group(1)] = m.group(2)
    return vals


def lan_host(text, what):
    """The IPv4 address of a server on another machine (patch_client.py's lan rule: not loopback, wildcard,
    multicast or reserved; a public address too)."""
    try:
        ip = ipaddress.IPv4Address(text)
    except ValueError:
        raise ValueError(f"{what} must be an IPv4 address, got {text!r}") from None
    if ip.is_loopback or ip.is_unspecified or ip.is_multicast or ip.is_reserved:
        raise ValueError(f"{what} {ip} is not a host address of another machine (loopback is the default setup)")
    return str(ip)


def under(path, base):
    """`path` is `base` or inside it (Windows: case-insensitive)."""
    p, b = os.path.normcase(os.path.abspath(path)), os.path.normcase(os.path.abspath(base))
    return p == b or p.startswith(b.rstrip("\\/") + os.sep)


def main_root(code):
    """The main checkout (it holds client/), also from a git worktree; else `code` (setup_lane.py's rule)."""
    try:
        common = subprocess.run(["git", "-C", code, "rev-parse", "--path-format=absolute", "--git-common-dir"],
                                capture_output=True, text=True, check=True).stdout.strip()
        main = os.path.dirname(os.path.normpath(common))
        if os.path.isdir(os.path.join(main, "client")):
            return main
    except (OSError, subprocess.CalledProcessError):
        pass
    return code


def find_go():
    exe = shutil.which("go") or next((p for p in (r"C:\Program Files\Go\bin\go.exe",) if os.path.isfile(p)), None)
    if not exe:
        return None, None
    r = subprocess.run([exe, "version"], capture_output=True, text=True, env=dict(os.environ, GOTOOLCHAIN="local"))
    m = re.search(r"go(\d+)\.(\d+)(?:\.(\d+))?", r.stdout)
    return exe, (tuple(int(x or 0) for x in m.groups()) if m else None)


def find_vs():
    """Visual Studio 2022 with the x86 C++ compiler: {"root", "cl", "cmake", "version"} or None."""
    roots = []
    if os.path.isfile(VSWHERE):
        r = subprocess.run([VSWHERE, "-version", "[17.0,18.0)", "-products", "*", "-requires",
                            "Microsoft.VisualStudio.Component.VC.Tools.x86.x64", "-property", "installationPath"],
                           capture_output=True, text=True)
        roots = [x.strip() for x in r.stdout.splitlines() if x.strip()]
    if not roots:
        roots = sorted(glob.glob(r"C:\Program Files\Microsoft Visual Studio\2022\*"))
    for root in roots:
        cl = sorted(glob.glob(os.path.join(root, "VC", "Tools", "MSVC", "*", "bin", "Host*", "x86", "cl.exe")))
        if not cl:
            continue
        cmake = os.path.join(root, "Common7", "IDE", "CommonExtensions", "Microsoft", "CMake", "CMake", "bin",
                             "cmake.exe")
        return {"root": root, "cl": cl[-1], "cmake": cmake if os.path.isfile(cmake) else shutil.which("cmake"),
                "version": os.path.basename(os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(cl[-1])))))}
    return None


def go_required(code):
    """server/go.mod's go line as a tuple, e.g. (1, 24, 0)."""
    m = re.search(r"^go (\d+)\.(\d+)(?:\.(\d+))?", read(os.path.join(code, "server", "go.mod")).decode(), re.M)
    return tuple(int(x or 0) for x in m.groups()) if m else (1, 0, 0)


# ---- the setup ---------------------------------------------------------------------------------------------------

class Setup:
    def __init__(self, a):
        self.a = a
        self.check = a.check
        self.code = os.path.normpath(os.path.join(HERE, "..", ".."))
        if a.root:
            self.root = os.path.abspath(a.root)
            self.build = os.path.abspath(a.build_root) if a.build_root else self.root
        else:
            self.root = main_root(self.code)
            self.build = os.path.abspath(a.build_root) if a.build_root else self.code
        j = os.path.join
        self.pristine = j(self.root, "client", "ia_Dawngate")
        self.pristine_game = j(self.pristine, "Dawngate")
        default_client = "run_854_lan" if a.player else "run_854"
        self.client = os.path.abspath(a.client) if a.client else j(self.root, "client", default_client, "Dawngate")
        self.host = j(self.root, "client", "run_854_server", "Dawngate")
        self.certs = j(self.root, "server", "certs")
        self.lan_certs = j(self.root, "server", "certs", "lan")
        self.data = j(self.root, "server", "data")
        self.raknet = j(self.root, "tools", "raknet", "RakNet")
        self.gms_build = j(self.build, "gms", "build")
        self.dll = j(self.gms_build, "Release", "dgsim.dll")
        self.server_exe = j(self.build, "server", "bin", "dgserver.exe")
        self.out = j(self.root, "re", "out")
        self.manifest = load_manifest(j(self.code, "docs", "file_manifest.csv"))
        self.results = []
        self.tools = {}
        self.py = sys.executable

    # -- reporting and small utilities
    def show(self, p):
        p = os.path.abspath(p)
        for base in (self.root, self.build):
            try:
                rel = os.path.relpath(p, base)
            except ValueError:
                continue
            if not rel.startswith(".."):
                return rel
        return p

    def report(self, state, step, detail):
        self.results.append((state, step, detail))
        print(f"  {'[' + state + ']':<10} {step:<14} {detail}", flush=True)
        return state in (OK, DONE, NOTE, SKIP)

    def needs(self, step, **conditions):
        """Report the step as missing when a precondition (name=bool) failed; True when all hold."""
        unmet = [name.replace("_", " ") for name, ok in conditions.items() if not ok]
        if unmet:
            self.report(MISSING, step, "needs " + ", ".join(unmet))
        return not unmet

    def run(self, cmd, log=None, env=None, cwd=None, show=8):
        print("    $ " + subprocess.list2cmdline(cmd), flush=True)
        if log:
            os.makedirs(os.path.dirname(log), exist_ok=True)
            with open(log, "wb") as f:
                r = subprocess.run(cmd, stdout=f, stderr=subprocess.STDOUT, env=env, cwd=cwd)
            lines = read(log).decode("utf-8", "replace").splitlines() if r.returncode else []
            tail = lines[-show * 2:]
        else:
            r = subprocess.run(cmd, capture_output=True, env=env, cwd=cwd)
            lines = (r.stdout + r.stderr).decode("utf-8", "replace").splitlines()
            tail = lines[-show:]
        if len(lines) > len(tail):
            print(f"      ... ({len(lines) - len(tail)} more line(s){' in ' + self.show(log) if log else ''})")
        for line in tail:
            print("      " + line.rstrip())
        return r.returncode

    def script(self, name, *args):
        return self.run([self.py, "-I", "-u", os.path.join(HERE, name), *args])  # -u: its children's lines in order

    def room(self, path, need, step):
        probe = os.path.abspath(path)
        while not os.path.exists(probe):
            probe = os.path.dirname(probe)
        free = shutil.disk_usage(probe).free
        if free < need + MARGIN:
            return self.report(FAILED, step, f"not enough disk space at {probe}: {free / GB:.1f} GB free, "
                                             f"{(need + MARGIN) / GB:.1f} GB needed")
        return True

    def remove_partial(self, tmp):
        """Remove a .partial folder an interrupted run of this script left (only that)."""
        if os.path.exists(tmp):
            if not tmp.endswith(".partial") or not under(tmp, self.root) or under(tmp, self.pristine):
                raise RuntimeError(f"refusing to remove {tmp}")
            print(f"    removing {self.show(tmp)} (left by an interrupted run)")
            shutil.rmtree(tmp)

    def orig_sha(self, rel):
        return self.manifest["Dawngate/" + rel][1]

    def never_pristine(self, path):
        """Nothing is ever written under client/ia_* (the extracted builds) once they exist."""
        clients = os.path.join(self.root, "client")
        first = os.path.relpath(os.path.abspath(path), clients).split(os.sep)[0] if under(path, clients) else ""
        if first.lower().startswith("ia_") or under(path, self.pristine):
            raise RuntimeError(f"refusing to write into {path}: client/ia_* stays as extracted")

    def restore_file(self, dest_copy, rel):
        """Copy one file of the build from client/ia_Dawngate into a working copy."""
        self.never_pristine(dest_copy)
        src = os.path.join(self.pristine_game, *rel.split("/"))
        dst = os.path.join(dest_copy, *rel.split("/"))
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        shutil.copy2(src, dst)

    # -- tools
    def step_tools(self):
        t, have, absent = self.tools, [f"Python {sys.version.split()[0]}"], []
        for mod in ("pefile", "cryptography"):
            try:
                m = __import__(mod)
                t[mod] = True
                have.append(f"{mod} {getattr(m, '__version__', '?')}")
            except ImportError:
                t[mod] = False
                absent.append(f"{mod} (.venv\\Scripts\\pip install pefile cryptography)")
        if not t["pefile"]:
            self.report(FAILED, "tools", "this script needs pefile: python -m venv .venv; "
                                         ".venv\\Scripts\\pip install pefile cryptography; then run it with "
                                         ".venv\\Scripts\\python -I (docs/development/building.md *The setup command*)")
            return False
        if not self.a.player:
            t["openssl"] = os.path.isfile(OPENSSL)
            (have if t["openssl"] else absent).append("Git for Windows' openssl" + ("" if t["openssl"] else
                                                      f" ({OPENSSL}; needed to make certificates)"))
            go, ver = find_go()
            want = go_required(self.code)
            t["go"] = bool(go and ver and ver >= want)
            if t["go"]:
                have.append(f"Go {'.'.join(map(str, ver))}")
            else:
                absent.append(f"Go {'.'.join(map(str, want[:2]))} or newer ({'found ' + '.'.join(map(str, ver)) + ', ' if ver else ''}"
                              "https://go.dev/dl/; needed for dgserver)")
            t["go_exe"] = go
            vs = find_vs()
            t["vs"] = bool(vs and vs["cmake"])
            t["cmake"] = vs["cmake"] if vs else None
            if t["vs"]:
                have.append(f"Visual Studio 2022 (MSVC {vs['version']} x86, CMake)")
            else:
                absent.append("Visual Studio 2022 with 'Desktop development with C++' (the x86 compiler and its "
                              "CMake; needed for gms/)")
        ws = os.path.isfile(r"C:\Program Files\Wireshark\dumpcap.exe")
        npcap = os.path.isdir(r"C:\Windows\System32\Npcap")
        if ws and npcap:
            have.append("Wireshark with Npcap")
        else:
            absent.append("Wireshark with Npcap (optional: the capture and egress check; play.ps1 -NoCapture "
                          "plays without them)")
        self.report(OK, "tools", ", ".join(have))
        for x in absent:
            self.report(NOTE, "tools", "not found: " + x)
        return True

    # -- archive
    def find_zip(self, given, name, extra=()):
        if given:
            return os.path.abspath(given) if os.path.isfile(given) else None
        cands = [os.path.join(self.root, name), os.path.join(self.code, name), *extra,
                 os.path.join(os.path.expanduser("~"), "Downloads", name)]
        return next((c for c in cands if os.path.isfile(c)), None)

    def step_archive(self):
        step = "archive"
        exe = os.path.join(self.pristine_game, *EXE.split("/"))
        if os.path.isdir(self.pristine):
            bad = verify_tree(self.pristine, self.manifest, full=self.a.verify)
            if bad:
                return self.report(FAILED, step, f"{self.show(self.pristine)} is not build 1.0.903.854 as extracted: "
                                                 f"{bad[0]}{f' (+{len(bad) - 1} more)' if len(bad) > 1 else ''}; it "
                                                 "is never changed here: extract Dawngate.zip again into a new folder")
            what = "every file's SHA-256" if self.a.verify else f"sizes, SHA-256 of {len(KEY_FILES)} key files"
            return self.report(OK, step, f"{self.show(self.pristine)}: build 1.0.903.854, {len(self.manifest)} files "
                                         f"({what}; docs/file_manifest.csv)")
        zp = self.find_zip(self.a.zip, GAME_ZIP_NAME)
        if not zp:
            return self.report(MISSING, step, f"no {self.show(self.pristine)} and no Dawngate.zip (download it from "
                                              "https://archive.org/details/Dawngate; --zip <file>)")
        if self.check:
            return self.report(MISSING, step, f"{self.show(self.pristine)} (extract {zp})")
        if not self.room(self.pristine, COPY_BYTES, step):
            return False
        print(f"    checking {zp} ...", flush=True)
        size, digest = os.path.getsize(zp), sha256(zp)
        note = ""
        if (size, digest) != (GAME_ZIP_SIZE, GAME_ZIP_SHA256):
            note = f"; the zip is not the known download ({size} bytes, SHA-256 {digest[:16]}...), its files passed"
        try:
            with zipfile.ZipFile(zp) as z:
                infos = check_game_zip(z)
        except (zipfile.BadZipFile, ValueError) as e:
            return self.report(FAILED, step, f"{zp} is not Dawngate's archive: {e}")
        tmp = self.pristine + ".partial"
        self.remove_partial(tmp)
        t0 = time.time()
        print(f"    extracting {len(infos)} entries into {self.show(tmp)} ...", flush=True)
        extract_client.extract(zp, tmp, infos)
        bad = verify_tree(tmp, self.manifest, full=True)
        if bad:
            return self.report(FAILED, step, f"the extracted files don't match build 854 ({bad[0]}"
                                             f"{f', +{len(bad) - 1} more' if len(bad) > 1 else ''}); left in "
                                             f"{self.show(tmp)} for a look")
        os.rename(tmp, self.pristine)
        return self.report(DONE, step, f"{self.show(self.pristine)} extracted from {zp} and checked "
                                       f"({len(self.manifest)} files, SHA-256; {time.time() - t0:.0f} s){note}")

    # -- certificates
    def step_certs(self, step, certs, cn):
        files = [os.path.join(certs, n) for n in CERT_FILES]
        if all(os.path.isfile(f) for f in files) and not (self.a.new_certs and not self.check):
            if not self.tools.get("cryptography"):
                return self.report(OK, step, f"{self.show(certs)} (files present; CN not checked without "
                                             "cryptography)")
            from cryptography import x509
            from cryptography.x509.oid import NameOID
            cert = x509.load_pem_x509_certificate(read(files[4]))
            ca = x509.load_pem_x509_certificate(read(files[1]))
            got = [a.value for a in cert.subject.get_attributes_for_oid(NameOID.COMMON_NAME)]
            modulus = ca.public_key().public_numbers().n.to_bytes(128, "big")
            if modulus != read(files[2]) or cert.issuer != ca.subject:
                return self.report(FAILED, step, f"{self.show(certs)}: ca_modulus.bin or server_cert.pem doesn't "
                                                 "belong to ca_cert.pem (--new-certs makes a new set)")
            if got != [cn]:
                return self.report(FAILED, step, f"{self.show(certs)} is for CN {', '.join(got) or '?'}, not {cn} "
                                                 "(the client checks the CN against the server's address); "
                                                 "--new-certs replaces the set, and every copy patched with the old "
                                                 "CA must be patched again")
            return self.report(OK, step, f"{self.show(certs)} (CN {cn}, CA {hashlib.sha256(modulus).hexdigest()[:16]})")
        if self.check:
            return self.report(MISSING, step, f"{self.show(certs)} (make_certs.py, CN {cn})")
        if not self.needs(step, cryptography=self.tools.get("cryptography"), Git_openssl=self.tools.get("openssl")):
            return False
        self.never_pristine(certs)
        if self.script(os.path.join("..", "..", "server", "tools", "make_certs.py"), certs, cn) or \
                not all(os.path.isfile(f) for f in files):
            return self.report(FAILED, step, f"make_certs.py failed for {self.show(certs)}")
        modulus = read(files[2])
        return self.report(DONE, step, f"{self.show(certs)} made (CN {cn}, CA {hashlib.sha256(modulus).hexdigest()[:16]})")

    # -- working copies
    def ensure_copy(self, step, dest, src, exempt):
        """`dest` (a Dawngate folder) holds every file of the build: made by copying `src` through a .partial
        folder; an existing one gets missing or truncated files (other than `exempt`) back from client/ia_Dawngate."""
        self.never_pristine(dest)
        if not os.path.isdir(dest):
            if self.check:
                return self.report(MISSING, step, f"{self.show(dest)} (a copy of {self.show(src)}, 1.5 GB)")
            if not self.room(dest, COPY_BYTES, step):
                return False
            tmp = dest + ".partial"
            self.remove_partial(tmp)
            t0 = time.time()
            print(f"    copying {self.show(src)} -> {self.show(tmp)} (1.5 GB) ...", flush=True)
            shutil.copytree(src, tmp, copy_function=shutil.copy2)
            os.rename(tmp, dest)
            return self.report(DONE, step, f"{self.show(dest)} copied from {self.show(src)} ({time.time() - t0:.0f} s)")
        bad = []
        for rel, (size, _) in sorted(self.manifest.items()):
            r = in_copy(rel)
            f = os.path.join(dest, *r.split("/"))
            if r not in exempt and (not os.path.isfile(f) or os.path.getsize(f) != size):
                bad.append(r)
        if not bad:
            return self.report(OK, step, f"{self.show(dest)} (every file of the build is there)")
        if self.check:
            return self.report(STALE, step, f"{self.show(dest)}: {len(bad)} file(s) of the build missing or of "
                                            f"another size ({bad[0]}{', ...' if len(bad) > 1 else ''})")
        for r in bad:
            self.restore_file(dest, r)
        return self.report(DONE, step, f"{self.show(dest)}: {len(bad)} file(s) restored from "
                                       f"{self.show(self.pristine)} ({bad[0]}{', ...' if len(bad) > 1 else ''})")

    def base_of(self, step, copy, rel):
        """The untouched file a patcher starts from (<file>.orig, or the file before its first patch), checked
        against the manifest; None after a report."""
        live = os.path.join(copy, *rel.split("/"))
        orig = live + ".orig"
        base = orig if os.path.isfile(orig) else live
        if not os.path.isfile(base):
            if self.check:
                self.report(MISSING, step, f"{self.show(live)} and its .orig are missing")
                return None
            self.restore_file(copy, rel)
            base = live
        if sha256(base) != self.orig_sha(rel):
            self.report(FAILED, step, f"{self.show(base)} is not the untouched build-854 file (docs/file_manifest.csv);"
                                      " the patchers start from it: delete the copy and run this again")
            return None
        return base

    def step_exe(self, step, copy, modulus_file, profile, server_host, client_bind=None, exact=True):
        import patch_client as pc
        exe = os.path.join(copy, *EXE.split("/"))
        base = self.base_of(step, copy, EXE)
        if base is None:
            return False
        if not os.path.isfile(modulus_file):
            return self.report(MISSING, step, f"the CA modulus {self.show(modulus_file)}")
        try:
            want, _summary = pc.patch_bytes(read(base), read(modulus_file), profile, None, server_host, client_bind)
        except pc.PatchError as e:
            return self.report(FAILED, step, str(e))
        want_d = pc.describe(want)

        def state():
            if not os.path.isfile(exe) or not os.path.isfile(exe + ".orig"):
                return "not patched (no .orig)"
            cur = read(exe)
            if cur == want:
                return None
            if cur == read(exe + ".orig"):
                return "not patched"
            got = pc.describe(cur)
            keys = HOST_KEYS if not exact else list(want_d)
            diff = [f"{k} {got.get(k)} (want {want_d[k]})" for k in keys if got.get(k) != want_d[k]]
            if not exact and not diff:
                return None
            return "; ".join(diff) or "an older patch (patch_client.py makes other bytes now)"

        def line():
            d = pc.describe(read(exe))
            return (f"{self.show(exe)}: redirector {d['redirector']}, binds {d['bind']}, CA {d['ca_sha256']}"
                    f"{'' if exact else ' (host copy: the harness narrows its binds itself)'}")

        why = state()
        if why is None:
            return self.report(OK, step, line())
        if self.check:
            return self.report(STALE, step, f"{self.show(exe)}: {why}")
        self.never_pristine(exe)
        cmd = [exe, modulus_file, "--profile", profile, "--server-host", server_host]
        if client_bind:
            cmd += ["--client-bind", client_bind]
        if self.script("patch_client.py", *cmd) or (read(exe) != want):
            return self.report(FAILED, step, f"patch_client.py didn't produce the expected {self.show(exe)} "
                                             "(is the game running from this copy?)")
        return self.report(DONE, step, line() + f" (was: {why})")

    def config_args(self, host, afk=True):
        return argparse.Namespace(host=host, http_port=18080, telemetry_port=9988,
                                  afk_minutes=self.a.afk_minutes if afk else None,
                                  afk_disconnect_minutes=self.a.afk_disconnect_minutes if afk else None,
                                  dry_run=False, restore=False, afk_hits=[])

    def step_config(self, step, copy, host, exact=True):
        import patch_config as pcfg
        data_dir = os.path.join(copy, "game", "Data")

        def stale():
            out = []
            for rel in CONFIGS if exact else CONFIGS[:2]:  # a host copy: what run_session.ps1 checks
                live = os.path.join(copy, *rel.split("/"))
                base = self.base_of(step, copy, rel)
                if base is None:
                    return None
                name = os.path.basename(live)
                if exact:
                    want, _, _ = pcfg.patched_package(base, self.config_args(host), name)
                    if not os.path.isfile(live) or read(live) != (want if want is not None else read(base)):
                        out.append(name)
                elif base == live or read(live) == read(base):
                    out.append(name)
            if not exact and not out:
                if f"http://{host}:18080".encode() not in read(os.path.join(data_dir, "Config.package")):
                    out.append(f"Config.package (not for {host})")
            return out

        bad = stale()
        if bad is None:
            return False
        if not bad:
            return self.report(OK, step, f"{self.show(data_dir)}: the config packages point at {host}")
        cmd = [data_dir, "--host", host]
        if exact and self.a.afk_minutes is not None:
            cmd += ["--afk-minutes", str(self.a.afk_minutes)]
            if self.a.afk_disconnect_minutes is not None:
                cmd += ["--afk-disconnect-minutes", str(self.a.afk_disconnect_minutes)]
        if self.check:
            return self.report(STALE, step, f"{self.show(data_dir)}: not what patch_config.py {' '.join(cmd[1:])} "
                                            f"makes: {', '.join(bad)}")
        self.never_pristine(data_dir)
        if self.script("patch_config.py", *cmd) or stale():
            return self.report(FAILED, step, f"patch_config.py didn't patch {self.show(data_dir)} as expected")
        return self.report(DONE, step, f"{self.show(data_dir)}: patched for {host} (was: {', '.join(bad)})")

    def step_locale(self, step, copy, host):
        """the player copy's added locale package game/Data/Locale/en-us/AA_Server.package (patch_locale.py:
        the login texts, the links at the server's pages for `host` or --web-domain), made from the copy's own
        Locale.package; current means byte for byte what patch_locale.py makes (the Go setup's localepatch, the
        same bytes). Never a host copy: the server simulator shows no text."""
        import patch_locale as pl
        src = os.path.join(copy, *pl.SOURCE.split("/"))
        added = os.path.join(copy, *pl.REL.split("/"))
        if not os.path.isfile(src):
            return self.report(MISSING, step, f"{self.show(src)} (the copy is incomplete)")
        if sha256(src) != self.orig_sha(pl.SOURCE):
            return self.report(FAILED, step, f"{self.show(src)} is not the untouched build-854 file "
                                             "(docs/file_manifest.csv); delete the copy and run this again")
        try:
            want = pl.build(src, host, web_domain=self.a.web_domain)
        except (pl.LocaleError, ValueError) as e:
            return self.report(FAILED, step, str(e))
        links = pl.site_base(host, web_domain=self.a.web_domain)
        line = f"{self.show(added)}: login texts, links at {links} ({hashlib.sha256(want).hexdigest()[:16]})"
        have = read(added) if os.path.isfile(added) else None
        if have == want:
            return self.report(OK, step, line)
        why = "not there" if have is None else "another server or an older text"
        if self.check:
            return self.report(MISSING if have is None else STALE, step, f"{self.show(added)}: {why}")
        self.never_pristine(added)
        cmd = [os.path.join(copy, "game", "Data"), "--host", host] + \
            (["--web-domain", self.a.web_domain] if self.a.web_domain else [])
        if self.script("patch_locale.py", *cmd) or not os.path.isfile(added) or read(added) != want:
            return self.report(FAILED, step, f"patch_locale.py didn't write the expected {self.show(added)} "
                                             "(is the game running from this copy?)")
        return self.report(DONE, step, f"{line} (was: {why})")

    # -- RakNet and the builds
    def step_raknet(self):
        step = "RakNet"
        ver = raknet_version(self.raknet)
        if ver is not None:
            if "4.081" not in ver:
                return self.report(FAILED, step, f"{self.show(self.raknet)} is RakNet {ver}, not 4.081")
            return self.report(OK, step, f"{self.show(self.raknet)} (RakNet {ver})")
        zp = self.find_zip(self.a.raknet_zip, RAKNET_ZIP_NAME,
                           (os.path.join(self.root, "tools", "_downloads", "raknet", RAKNET_ZIP_NAME),))
        if not zp:
            return self.report(MISSING, step, f"{self.show(self.raknet)}: download {RAKNET_URL} (SHA-256 "
                                              f"{RAKNET_ZIP_SHA256}) and pass --raknet-zip <file>")
        if self.check:
            return self.report(MISSING, step, f"{self.show(self.raknet)} (extract {zp})")
        digest = sha256(zp)
        if digest != RAKNET_ZIP_SHA256:
            return self.report(FAILED, step, f"{zp} has SHA-256 {digest}, want {RAKNET_ZIP_SHA256} (commit "
                                             f"{RAKNET_COMMIT})")
        tmp = self.raknet + ".partial"
        self.remove_partial(tmp)
        try:
            n = extract_raknet(zp, tmp)
        except (zipfile.BadZipFile, ValueError) as e:
            return self.report(FAILED, step, f"{zp}: {e}")
        if "4.081" not in (raknet_version(tmp) or ""):
            return self.report(FAILED, step, f"{zp} holds no RakNet 4.081 (left in {self.show(tmp)})")
        os.rename(tmp, self.raknet)
        return self.report(DONE, step, f"{self.show(self.raknet)}: {n} files (Source/, LICENSE, PATENTS, README.md) "
                                       f"from {zp}")

    def needs_configure(self, build_dir, project):
        """No cache, no project for `project` (RakNet wasn't found then) or a cache whose RAKNET_ROOT isn't 4.081."""
        cache = cmake_cache(build_dir)
        return not cache or not os.path.isfile(os.path.join(build_dir, project)) or \
            "4.081" not in (raknet_version(cache.get("RAKNET_ROOT", "")) or "")

    def cmake_build(self, src, build_dir, project):
        """Configure (when needed) and build Release; returns the failure text or None."""
        cache = cmake_cache(build_dir)
        cmake = cache.get("CMAKE_COMMAND") if cache.get("CMAKE_COMMAND") and os.path.isfile(cache["CMAKE_COMMAND"]) \
            else self.tools.get("cmake")
        if self.needs_configure(build_dir, project):
            if self.run([cmake, "-S", src, "-B", build_dir, "-G", "Visual Studio 17 2022", "-A", "Win32",
                         f"-DRAKNET_ROOT={self.raknet.replace(chr(92), '/')}"],
                        log=os.path.join(build_dir, "setup_configure.log")):
                return "CMake's configure step failed"
            if not os.path.isfile(os.path.join(build_dir, project)):
                return f"CMake made no {project} (RakNet not found?)"
        t0 = time.time()
        if self.run([cmake, "--build", build_dir, "--config", "Release", "--parallel"],
                    log=os.path.join(build_dir, "setup_build.log")):
            return "the build failed"
        print(f"    built in {time.time() - t0:.0f} s", flush=True)
        return None

    def step_gms(self, raknet_ok):
        step = "gms build"
        cache = cmake_cache(self.gms_build)
        src = cache.get("CMAKE_HOME_DIRECTORY") or os.path.join(self.code, "gms")
        src = os.path.normpath(src)
        configured = not self.needs_configure(self.gms_build, "gms_script.vcxproj")
        stale = gms_stale(src, self.gms_build)
        if configured and not stale:
            return self.report(OK, step, f"{self.show(self.gms_build)}: dgsim.dll and gms_script.exe are newer than "
                                         f"their sources ({self.show(src)})")
        why = "; ".join(f"{os.path.basename(p)} {w}" for p, w in stale) or "not configured with RakNet 4.081"
        if self.check:
            return self.report(MISSING if not cache else STALE, step, f"{self.show(self.gms_build)}: {why}")
        if not self.needs(step, RakNet=raknet_ok, Visual_Studio_2022=self.tools.get("vs")):
            return False
        fail = self.cmake_build(src, self.gms_build, "gms_script.vcxproj")
        if not fail:
            stale = gms_stale(src, self.gms_build)
            if stale:  # a header-only change doesn't relink; delete and build again
                for p, w in stale:
                    print(f"    {os.path.basename(p)} is still {w}: deleting it and building again", flush=True)
                    if os.path.isfile(p):
                        os.remove(p)
                fail = self.cmake_build(src, self.gms_build, "gms_script.vcxproj")
                stale = gms_stale(src, self.gms_build)
                if not fail and stale:
                    fail = "; ".join(f"{os.path.basename(p)} still {w}" for p, w in stale)
        if fail:
            return self.report(FAILED, step, f"{self.show(self.gms_build)}: {fail} (logs setup_configure.log, "
                                             "setup_build.log there)")
        return self.report(DONE, step, f"{self.show(self.gms_build)}: dgsim.dll, gms_script.exe, gms_tests.exe "
                                       f"built from {self.show(src)} (was: {why})")

    def step_server(self):
        step = "dgserver"
        src = os.path.join(self.code, "server")
        why = server_stale(src, self.server_exe)
        if not why:
            return self.report(OK, step, f"{self.show(self.server_exe)} is newer than {self.show(src)}")
        if self.check:
            return self.report(MISSING if why == "not built" else STALE, step, f"{self.show(self.server_exe)}: {why}")
        if not self.needs(step, Go=self.tools.get("go")):
            return False
        env = dict(os.environ, GOTOOLCHAIN="local")  # never download a toolchain
        if self.run([self.tools["go_exe"], "build", "-C", src, "-o", self.server_exe, "./cmd/dgserver"], env=env):
            return self.report(FAILED, step, f"go build failed (see above; \"Access is denied\": a running "
                                             f"dgserver.exe locks {self.show(self.server_exe)})")
        if server_stale(src, self.server_exe):  # go keeps an up-to-date binary as it is: its time says nothing
            os.utime(self.server_exe, None)
        return self.report(DONE, step, f"{self.show(self.server_exe)} built ({why} before)")

    def step_host_dll(self, client_ok, gms_ok):
        step = "host DLL"
        game = os.path.join(self.host, "game")
        loader, orig, ini = (os.path.join(game, n) for n in ("PhysXLoader.dll", "PhysXLoader_orig.dll", "dgsim.ini"))

        def problems():
            out = []
            if not os.path.isfile(orig):
                out.append("no dgsim.dll installed (no PhysXLoader_orig.dll)")
            elif sha256(orig) != self.orig_sha(LOADER):
                return None
            if not os.path.isfile(self.dll):
                out.append("gms/build has no dgsim.dll")
            elif not os.path.isfile(loader) or sha256(loader) != sha256(self.dll):
                out.append("PhysXLoader.dll is not the current dgsim.dll")
            if not os.path.isfile(ini):
                out.append("no dgsim.ini")
            lua_dir = os.path.join(self.code, "gms", "engine", "lua")
            lf = lambda p: read(p).replace(b"\r\n", b"\n")  # noqa: E731  (checkouts differ in line endings)
            diff = [n for n in sorted(os.listdir(lua_dir)) if n.endswith(".lua") and
                    (not os.path.isfile(os.path.join(game, n)) or lf(os.path.join(game, n)) !=
                     lf(os.path.join(lua_dir, n)))]
            if diff:
                out.append(f"{len(diff)} Lua probe(s) differ from gms/engine/lua ({diff[0]}...)")
            return out

        bad = problems()
        if bad is None:
            return self.report(FAILED, step, f"{self.show(orig)} is not the game's PhysXLoader.dll; setup_server_copy.py"
                                             " --remove, then run this again")
        if not bad:
            return self.report(OK, step, f"{self.show(game)}: PhysXLoader.dll = {self.show(self.dll)} "
                                         f"({sha256(self.dll)[:16]}), dgsim.ini, Lua probes")
        if self.check:
            return self.report(STALE, step, f"{self.show(game)}: {'; '.join(bad)}")
        if not self.needs(step, client_copy=client_ok, gms_build=gms_ok):
            return False
        if self.script("setup_server_copy.py", self.client, self.host, self.dll) or problems():
            return self.report(FAILED, step, f"setup_server_copy.py didn't install {self.show(self.dll)}")
        return self.report(DONE, step, f"{self.show(game)}: dgsim.dll {sha256(self.dll)[:16]} installed "
                                       f"(was: {'; '.join(bad)})")

    def step_content(self, archive_ok):
        step = "content"
        content = os.path.join(self.data, "content.json")
        m = re.search(r'\bFormat\s*=\s*"([^"]+)"',
                      read(os.path.join(self.code, "server", "internal", "content", "content.go")).decode())
        want = m.group(1) if m else "?"
        got = None
        stats = False
        if os.path.isfile(content):
            try:
                c = json.loads(read(content))
                got = c.get("format")
                # D3-perks: the perks' stats (content.HasPerkStats); the format name stayed, so older servers still
                # read a newer file, and an older file is rebuilt here
                stats = any(p.get("attributes") or p.get("modifiers") for p in c.get("perks") or [])
            except ValueError:
                got = "unreadable"
        if got == want and stats:
            return self.report(OK, step, f"{self.show(content)} ({want}, perk stats)")
        why = ("missing" if got is None else f"format {got}, want {want}" if got != want
               else "no perk stats (built before D3-perks)")
        if self.check:
            return self.report(MISSING if got is None else STALE, step, f"{self.show(content)}: {why} "
                                                                        "(optional: --no-content)")
        if not self.needs(step, archive=archive_ok, dgserver=os.path.isfile(self.server_exe)):
            return False
        dump = os.path.join(self.out, "assetdata")
        if not os.path.isfile(os.path.join(dump, "_summary.json")):
            snap_out = os.path.join(self.out, "snap")
            bins = os.path.join(snap_out, "AssetData", "AssetData", "Binary")
            data_dir = os.path.join(self.pristine_game, "game", "Data")
            if not os.path.isdir(bins) and self.script("snap.py", "extract", os.path.join(data_dir, "AssetData.snap"),
                                                       snap_out):
                return self.report(FAILED, step, "snap.py extract AssetData.snap failed")
            if self.script("assetdata.py", "dump", bins, dump, "--locale",
                           os.path.join(data_dir, "Locale", "en-us", "Locale.package")):
                return self.report(FAILED, step, "assetdata.py dump failed")
        if self.run([self.server_exe, "--make-content", dump, "--data", self.data]):
            return self.report(FAILED, step, "dgserver --make-content failed")
        try:
            c = json.loads(read(content))
            got = c.get("format")
            stats = any(p.get("attributes") or p.get("modifiers") for p in c.get("perks") or [])
        except (OSError, ValueError):
            got, stats = None, False
        if got != want or not stats:
            return self.report(FAILED, step, f"{self.show(content)} has format {got}, want {want}"
                                             f"{'' if stats else ', and no perk stats'}")
        return self.report(DONE, step, f"{self.show(content)} built from {self.show(dump)} ({want})")

    def step_probe(self, raknet_ok):
        step = "RakNet probe"
        build_dir = os.path.join(self.root, "tools", "raknet", "build")
        cache = cmake_cache(build_dir)
        src = os.path.normpath(cache.get("CMAKE_HOME_DIRECTORY") or os.path.join(self.code, "re", "scripts",
                                                                                  "raknet_probe"))
        exe = os.path.join(build_dir, "Release", "raknet_probe_p5.exe")
        why = "not built"
        if os.path.isfile(exe):
            srcs = [os.path.join(src, "raknet_probe.cpp"), os.path.join(src, "CMakeLists.txt"),
                    os.path.join(src, "..", "..", "..", "gms", "cmake", "raknet.cmake")]  # its CMakeLists' include
            newer = [p for p in srcs if os.path.isfile(p) and os.path.getmtime(p) > os.path.getmtime(exe)]
            why = f"older than {os.path.basename(newer[0])}" if newer else None
        if not why:
            return self.report(OK, step, f"{self.show(exe)} (from {self.show(src)})")
        if self.check:
            return self.report(MISSING if why == "not built" else STALE, step, f"{self.show(exe)}: {why}")
        if not self.needs(step, RakNet=raknet_ok, Visual_Studio_2022=self.tools.get("vs")):
            return False
        fail = self.cmake_build(src, build_dir, "raknet_probe_p5.vcxproj")
        if fail or not os.path.isfile(exe):
            return self.report(FAILED, step, f"{self.show(build_dir)}: {fail or 'no raknet_probe_p5.exe'}")
        os.utime(exe, None)
        return self.report(DONE, step, f"{self.show(exe)} built ({why} before)")

    def step_lane(self, name, archive_ok, gms_ok):
        """An extra test lane (A6: b; A6b: c, d) through setup_lane.py <name>; since A5b also lane d's second client
        (e: client/run_854_e) and the LAN profile's test copy (lan: client/run_854_l, with --lan-certs <S>)."""
        step = f"lane {name}" if name in EXTRA_LANES else f"copy {name}"
        what = f"client\\run_854_{name}, client\\run_854_server_{name}, server\\certs\\lane_{name}"
        cmd = [self.py, "-I", "-u", os.path.join(HERE, "setup_lane.py"), name, "--dll", self.dll, "--root", self.root]
        if name == "lan":
            what = f"client\\run_854_l for {self.a.lan_certs}, server\\certs\\lan"
            cmd += ["--address", self.a.lan_certs]
        elif name not in EXTRA_LANES:
            what = f"client\\run_854_{name} (a second client)"
            gms_ok = True  # no host copy

        def lane(extra=()):
            print("    $ " + subprocess.list2cmdline(cmd + list(extra)), flush=True)
            r = subprocess.run(cmd + list(extra), capture_output=True)
            lines = (r.stdout + r.stderr).decode("utf-8", "replace").splitlines()
            for line in lines[-12:]:
                print("      " + line.rstrip())
            return r.returncode, [x.strip()[len("MISSING"):].strip() for x in lines if x.strip().startswith("MISSING")]

        code, missing = lane(["--check"])  # setup_lane.py <name> itself always reinstalls the DLL: look first
        if code == 0:
            return self.report(OK, step, f"{what} (setup_lane.py {name} --check)")
        if code != 1:
            return self.report(FAILED, step, f"setup_lane.py {name} --check failed (exit {code})")
        if self.check:
            return self.report(MISSING, step, f"{len(missing)} item(s): {missing[0] if missing else 'see above'}")
        if not self.needs(step, archive=archive_ok, gms_build=gms_ok):
            return False
        code, _ = lane()
        if code:
            return self.report(FAILED, step, f"setup_lane.py {name} failed (exit {code})")
        return self.report(DONE, step, f"{what} (setup_lane.py {name}; was: {missing[0] if missing else '?'}"
                                       f"{f' +{len(missing) - 1} more' if len(missing) > 1 else ''})")

    # -- the two plans
    def copy_there(self, step, dest, archive, exempt):
        """The copy step; True when its patches can be looked at: the copy is complete (or, in --check, exists)."""
        if not self.needs(step, archive=archive):
            return False
        return self.ensure_copy(step, dest, self.pristine_game, exempt) or (self.check and os.path.isdir(dest))

    def run_full(self):
        archive = self.step_archive()
        certs = self.step_certs("certificates", self.certs, LOCAL)
        if self.a.lan_certs:
            self.step_certs("lan certs", self.lan_certs, self.a.lan_certs)
        client = False
        if self.copy_there("client copy", self.client, archive, PATCHED):
            exe = self.needs("client exe", certificates=certs) and \
                self.step_exe("client exe", self.client, os.path.join(self.certs, "ca_modulus.bin"), "local", LOCAL)
            config = self.step_config("client config", self.client, LOCAL)
            self.step_locale("client texts", self.client, LOCAL)
            client = bool(exe and config)
        raknet = self.step_raknet()
        gms = self.step_gms(raknet)
        self.step_server()
        host_copy = self.copy_there("host copy", self.host, archive, PATCHED + (LOADER,))
        host = False
        if host_copy:
            exe = self.needs("host exe", certificates=certs) and \
                self.step_exe("host exe", self.host, os.path.join(self.certs, "ca_modulus.bin"), "local", LOCAL,
                              exact=False)
            config = self.step_config("host config", self.host, LOCAL, exact=False)
            host = bool(exe and config)
        if host or (host_copy and self.check):
            self.step_host_dll(client, gms)
        else:
            self.needs("host DLL", patched_host_copy=False)
        if self.a.no_content:
            self.report(SKIP, "content", "--no-content: new accounts own no items")
        else:
            self.step_content(archive)
        if self.a.probe:
            self.step_probe(raknet)
        for name in self.a.lanes:
            self.step_lane(name, archive, gms)

    def run_player(self):
        try:
            host = lan_host(self.a.server_host, "--server-host")
            bind = lan_host(self.a.client_bind, "--client-bind") if self.a.client_bind else None
        except ValueError as e:
            return self.report(FAILED, "player", str(e))
        ca = os.path.abspath(self.a.ca) if self.a.ca else os.path.join(self.lan_certs, "ca_modulus.bin")
        archive = self.step_archive()
        if not os.path.isfile(ca):
            return self.report(MISSING, "player CA", f"{self.show(ca)}: copy the server's ca_modulus.bin (only that "
                                                     "file) to this machine and pass --ca <file>")
        if len(read(ca)) != 128:
            return self.report(FAILED, "player CA", f"{self.show(ca)} is not a 128-byte CA modulus")
        self.report(OK, "player CA", f"{self.show(ca)} (CA {hashlib.sha256(read(ca)).hexdigest()[:16]})")
        if self.copy_there("client copy", self.client, archive, PATCHED):
            self.step_exe("client exe", self.client, ca, "lan", host, bind)
            self.step_config("client config", self.client, host)
            self.step_locale("client texts", self.client, host)

    def finish(self):
        bad = [r for r in self.results if r[0] in BAD]
        failed = any(r[0] == FAILED for r in self.results)
        done = sum(r[0] == DONE for r in self.results)
        ok = sum(r[0] == OK for r in self.results)
        print("")
        if self.check:
            title = "everything is current" if not bad else f"{len(bad)} item(s) not current"
            print(f"==== Dawngate setup check: {title} ({ok} ok)")
        elif not bad:
            print(f"==== Dawngate setup complete: {done} step(s) done, {ok} already current")
        else:
            print(f"==== Dawngate setup INCOMPLETE: {len(bad)} item(s) left ({done} done, {ok} already current)")
        for state, step, detail in bad:
            print(f"  {'[' + state + ']':<10} {step:<14} {detail}")
        print(f"  state root  {self.root}")
        print(f"  builds      {self.build}")
        if self.check and bad:
            print("  fix         run the same command without --check")
        elif not bad:
            if self.a.player:
                # P2: the player launcher (re/scripts/play_lan.ps1): the capture and the egress check with --allow-host
                shown = self.show(self.client)
                gamedir = "" if self.client == os.path.join(self.root, "client", "run_854_lan", "Dawngate") else \
                    (f' -GameDir "{shown}"' if " " in shown else f" -GameDir {shown}")
                print(f"  play        powershell -ExecutionPolicy Bypass -File re\\scripts\\"
                      f"{'play.ps1' if not gamedir else 'play_lan.ps1'} -Server {self.a.server_host}{gamedir}")
                print("              (docs/development/running-from-source.md *Playing on a server on another PC*)")
            elif self.root == self.build == self.code:
                print("  play        powershell -ExecutionPolicy Bypass -File re\\scripts\\play.ps1   (docs/development/running-from-source.md)")
                print("  serve       server\\bin\\dgserver.exe --gms-dir client\\run_854_server   (docs/development/running-from-source.md)")
        if failed:
            return 2
        if bad:
            return 1 if self.check else 2
        return 0


def parse(argv):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--check", action="store_true", help="only report; write nothing (exit 1: something to do)")
    ap.add_argument("--zip", help="Dawngate.zip from archive.org (default: the repository folder, then Downloads)")
    ap.add_argument("--raknet-zip", help=f"the RakNet archive {RAKNET_ZIP_NAME} ({RAKNET_URL})")
    ap.add_argument("--verify", action="store_true", help="hash every file of client/ia_Dawngate, not only the key files")
    ap.add_argument("--lan-certs", metavar="S", help="also certificates for clients on other machines: server/certs/lan "
                                                     "with CN <S>, this machine's LAN address")
    ap.add_argument("--new-certs", action="store_true", help="replace existing certificates (copies are re-patched)")
    ap.add_argument("--no-content", action="store_true", help="leave out server/data/content.json")
    ap.add_argument("--probe", action="store_true", help="also build tools/raknet/build's raknet_probe_p5.exe")
    ap.add_argument("--lane", action="append", default=[], metavar="X[,Y...]",
                    help=f"also set up these extra test lanes ({', '.join(EXTRA_LANES)}; setup_lane.py X; repeatable "
                         f"or comma-separated, e.g. --lane c,d) or copies ({', '.join(EXTRA_COPIES)}, A5b)")
    ap.add_argument("--lane-b", action="store_true", help="the same as --lane b")
    ap.add_argument("--player", action="store_true", help="only a client copy for a server on another machine")
    ap.add_argument("--server-host", metavar="S", help="--player: the server's LAN address")
    ap.add_argument("--client-bind", metavar="C", help="--player: bind the client's sockets to this machine's address")
    ap.add_argument("--ca", help="--player: the server's ca_modulus.bin (default server/certs/lan/ca_modulus.bin)")
    ap.add_argument("--client", help="the client copy's Dawngate folder (default client/run_854/Dawngate, "
                                     "--player: client/run_854_lan/Dawngate)")
    ap.add_argument("--web-domain", metavar="D", help="the server's website: the client copy's login links go to "
                                                       "https://<D>/<page> instead of dgserver's own pages")
    ap.add_argument("--afk-minutes", type=int, metavar="N", help="patch_config.py --afk-minutes for the client copy")
    ap.add_argument("--afk-disconnect-minutes", type=int, metavar="M", help="patch_config.py --afk-disconnect-minutes")
    ap.add_argument("--root", help="put the copies, certificates, data, RakNet and builds under this folder")
    ap.add_argument("--build-root", help="put gms/build and server/bin under this folder")
    a = ap.parse_args(argv[1:])
    if a.player and not a.server_host:
        ap.error("--player needs --server-host <the server's LAN address>")
    if not a.player:
        for name in ("server_host", "client_bind", "ca"):
            if getattr(a, name):
                ap.error(f"--{name.replace('_', '-')} is for --player")
    else:
        for name in ("lan_certs", "lane", "lane_b", "probe", "new_certs"):
            if getattr(a, name):
                ap.error(f"--{name.replace('_', '-')} doesn't go with --player")
    # A6b: --lane c --lane d, --lane c,d and --lane-b, in lane order, each once
    named = [x.strip().lower() for v in a.lane for x in v.split(",") if x.strip()] + (["b"] if a.lane_b else [])
    for x in named:
        if x not in EXTRA_LANES + EXTRA_COPIES:
            ap.error(f"--lane {x}: the extra test lanes are {', '.join(EXTRA_LANES)} (lane a is this setup itself), "
                     f"the extra copies {', '.join(EXTRA_COPIES)}")
    if "lan" in named and not a.lan_certs:
        ap.error("--lane lan (the LAN profile's test copy, A5b) needs --lan-certs <this PC's LAN address>")
    a.lanes = sorted(set(named))  # b, c, d before e (lane d's second client needs lane d's certificates) and lan
    if a.lan_certs:
        try:
            a.lan_certs = lan_host(a.lan_certs, "--lan-certs")
        except ValueError as e:
            ap.error(str(e))
    if a.web_domain:  # patch_locale.py's rule
        import patch_locale
        try:
            patch_locale.check_options(LOCAL, web_domain=a.web_domain)
        except patch_locale.LocaleError as e:
            ap.error(f"--web-domain: {e}")
    if a.afk_disconnect_minutes is not None and a.afk_minutes is None:
        ap.error("--afk-disconnect-minutes needs --afk-minutes")
    for v in (a.afk_minutes, a.afk_disconnect_minutes):
        if v is not None and not 1 <= v <= 10080:
            ap.error("AFK minutes must be 1..10080")
    return a


def main(argv):
    a = parse(argv)
    sys.stdout.reconfigure(errors="replace")
    if not sys.flags.isolated:
        sys.exit("run this with python -I (.venv\\Scripts\\python -I re\\scripts\\setup_all.py): it reads the game "
                 "archive, which is untrusted input")
    s = Setup(a)
    mode = "check" if a.check else "setup"
    print(f"Dawngate {mode}{' (player copy for ' + a.server_host + ')' if a.player else ''}: state root {s.root}, "
          f"builds {s.build}", flush=True)
    if s.step_tools():
        if a.player:
            s.run_player()
        else:
            s.run_full()
    return s.finish()


if __name__ == "__main__":
    sys.exit(main(sys.argv))
