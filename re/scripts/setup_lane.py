"""Set up (or check) an extra test lane on this PC (roadmap A6 and A6b, docs/RUNNING_THE_CLIENT.md *Test lanes (A6,
A6b)*).

A lane is everything one test session needs, separate from the other lanes', so up to four sessions (four clients,
four servers, four match hosts) run at the same time. Lane a is the original setup (client/run_854, set up by hand as
in docs/RUNNING_THE_CLIENT.md *How to start it*, or by setup_all.py); this script makes lanes b, c and d. Lane X
(b = 2, c = 3, d = 4) has:

  address     127.0.0.N (the whole loopback /8 works on Windows): dgserver binds every port there (--bind and
              --public-host), the match hosts' GMS ports and the control port too; lane a stays on 127.0.0.1, so
              all lanes use the same port numbers.
  client      client/run_854_X/Dawngate, a copy of client/ia_Dawngate/Dawngate (read only), patched by
              patch_client.py for 127.0.0.N (redirector host, every wildcard bind narrowed to 127.0.0.N, RakNet's
              loopback literal, --data-suffix <X upper case>: %APPDATA%\\DawngateDataX, Documents\\DawngateX; since
              Q3b LARGE_ADDRESS_AWARE) with the lane's CA, and by patch_config.py --host 127.0.0.N (CDN, telemetry and crash hosts). It runs with
              -multipleInstances, since lane a's client holds the Global\\Dawngate mutex. also
              patch_locale.py --host 127.0.0.N (game/Data/Locale/en-us/AA_Server.package: the login texts, the
              links at the lane's dgserver pages; also in the copies e, f and lan below).
  certs       server/certs/lane_X (make_certs.py, CN=127.0.0.N: the client checks the CN against the redirector
              host it connected to).
  host copy   client/run_854_server/Dawngate's twin client/run_854_server_X/Dawngate (setup_server_copy.py from
              the lane's client copy, with the dgsim.dll of the checkout that tests on that lane).
  accounts    server/data/lane_X (its own accounts.db; content from server/data/content.json). Made here;
              dgserver would make it at its first save too.
  server      run_session.ps1 -Lane X builds server/bin/lane_X/dgserver.exe in the checkout it runs from (a running
              server locks its binary).
  session     re/logs/session_X.lock (lane a: re/logs/session.lock), re/logs/<session>/lane.txt.

Every step is skipped when it is done already, and the patchers patch from their .orig backups, so re-running is
safe. Nothing under client/ia_* is written. Large copies: about 1.5 GB per copy (the client copy, the host copy), so
a new lane needs about 3 GB: before copying, the free space on the drive is printed, and the setup refuses with less
than MIN_FREE_GB (A6b).

The paths are relative to the main checkout (the folder that holds client/), also when this script runs from a
git worktree (the work package of a lane b, c or d): the dgsim.dll comes from the checkout this script is in, unless
--dll names one. --root <dir> (H3) puts them under another folder instead (setup_all.py's scratch test;
run_session.ps1 only knows the main checkout's).

Two more kinds of copy since A5b (several clients and the LAN profile on this PC, docs/RUNNING_THE_CLIENT.md *Playing
across machines (A5)*):
  e           lane d's second client client/run_854_e/Dawngate (EXTRA_CLIENTS): lane d's redirector (127.0.0.4) and
              CA, its sockets narrowed to its own 127.0.4.2 (both clients bind UDP 3659), RakNet's loopback literal
              with it, %APPDATA%\\DawngateDataE; no host copy (run_session.ps1 -Lane d -SecondClient).
  f           since G3b lane b's second client client/run_854_f/Dawngate, the same with lane b's redirector
              (127.0.0.2) and CA, bound to 127.0.2.2, %APPDATA%\\DawngateDataF (run_session.ps1 -Lane b -SecondClient).
  h           lane c's second client client/run_854_h/Dawngate, the same with lane c's redirector
              (127.0.0.3) and CA, bound to 127.0.3.2, %APPDATA%\\DawngateDataH (run_session.ps1 -Lane c -SecondClient).
  lan         the LAN profile's test copy client/run_854_l/Dawngate (LAN): patch_client.py --profile lan with this PC's
              LAN address as server and bind address (RakNet's start-up send test pointed at it), %APPDATA%\\
              DawngateDataL, patch_config.py --host <address>, and server/certs/lan with CN = the address
              (run_session.ps1 -Lan <address>). Certificates for another address are kept unless --new-certs.
Both run with -multipleInstances and are named by a letter no lane uses, so no run_session.ps1 since A6b counts them
as lane a's.

Usage:
    .venv/Scripts/python -I re/scripts/setup_lane.py b|c|d [--dll <dgsim.dll>] [--no-host] [--root <dir>]
    .venv/Scripts/python -I re/scripts/setup_lane.py c --check      # only check; exit 1 if something is missing
    .venv/Scripts/python -I re/scripts/setup_lane.py --print c      # the lane's settings as key=value lines
    .venv/Scripts/python -I re/scripts/setup_lane.py e [--check]    # A5b: lane d's second client
    .venv/Scripts/python -I re/scripts/setup_lane.py f [--check]    # G3b: lane b's second client
    .venv/Scripts/python -I re/scripts/setup_lane.py h [--check]    # lane c's second client
    .venv/Scripts/python -I re/scripts/setup_lane.py lan --address <IPv4>|auto [--check] [--new-certs]  # A5b
re/scripts/setup_all.py --lane <X>[,<Y>...] (or --lane-b) runs this script after the rest of the setup (--lane e,
--lane lan with --lan-certs <S> since A5b).
"""
import argparse
import hashlib
import os
import shutil
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)  # -I drops the script folder
import patch_client  # noqa: E402
import patch_locale  # noqa: E402

# One entry per lane; run_session.ps1 has the same table ($LaneTable; test_a6.py checks they agree).
LANES = {
    "a": {"address": "127.0.0.1", "client": "client/run_854/Dawngate", "host": "client/run_854_server/Dawngate",
          "certs": "server/certs", "suffix": "", "data": "server/data", "server_exe": "server/bin/dgserver.exe",
          "lock": "re/logs/session.lock", "game_args": ""},
    "b": {"address": "127.0.0.2", "client": "client/run_854_b/Dawngate", "host": "client/run_854_server_b/Dawngate",
          "certs": "server/certs/lane_b", "suffix": "B", "data": "server/data/lane_b",
          "server_exe": "server/bin/lane_b/dgserver.exe", "lock": "re/logs/session_b.lock",
          "game_args": "-multipleInstances"},
    # A6b: lanes c and d, built like lane b
    "c": {"address": "127.0.0.3", "client": "client/run_854_c/Dawngate", "host": "client/run_854_server_c/Dawngate",
          "certs": "server/certs/lane_c", "suffix": "C", "data": "server/data/lane_c",
          "server_exe": "server/bin/lane_c/dgserver.exe", "lock": "re/logs/session_c.lock",
          "game_args": "-multipleInstances"},
    "d": {"address": "127.0.0.4", "client": "client/run_854_d/Dawngate", "host": "client/run_854_server_d/Dawngate",
          "certs": "server/certs/lane_d", "suffix": "D", "data": "server/data/lane_d",
          "server_exe": "server/bin/lane_d/dgserver.exe", "lock": "re/logs/session_d.lock",
          "game_args": "-multipleInstances"},
}
# A5b: further client copies on this PC, each a client of one lane's server, in a folder named with a letter no lane
# uses (run_session.ps1 counts every other copy under client\ as lane a's, and since A6b a copy run_854_<letter> of an
# unknown letter as nobody's). run_session.ps1 has the same table ($ExtraClients; test_a6.py checks they agree).
EXTRA_CLIENTS = {
    # lane d's second client (two clients on one server, run_session.ps1 -SecondClient): the lane's redirector and CA,
    # its own bind address (both clients bind UDP 3659; 127.0.N.2 = lane N's second client, outside the lanes'
    # 127.0.0.N) and its own data folders
    "e": {"lane": "d", "client": "client/run_854_e/Dawngate", "bind": "127.0.4.2", "suffix": "E"},
    # G3b: lane b's second client, built like e (lane d stays busy with long packages, G3b needs two clients)
    "f": {"lane": "b", "client": "client/run_854_f/Dawngate", "bind": "127.0.2.2", "suffix": "F"},
    # lane c's second client ('g' stays test_a6.py's letter nobody uses)
    "h": {"lane": "c", "client": "client/run_854_h/Dawngate", "bind": "127.0.3.2", "suffix": "H"},
}
# A5b: the LAN profile's test copy on this PC (run_session.ps1 -Lan): lan profile, server = this PC's LAN address, the
# sockets narrowed to it; certificates with that CN; one lan session at a time on the PC (its own lock, whatever lane)
LAN = {"client": "client/run_854_l/Dawngate", "suffix": "L", "certs": "server/certs/lan",
       "lock": "re/logs/session_lan.lock"}
PRISTINE = "client/ia_Dawngate/Dawngate"
MIN_FREE_GB = 5  # A6b: a new lane copies about 3 GB (client copy and host copy, 1.5 GB each); keep a margin


def code_root():
    """The checkout this script is in (the main checkout or a worktree)."""
    return os.path.normpath(os.path.join(HERE, "..", ".."))


def main_root():
    """The main checkout (it holds client/, .venv, re/logs), also from a git worktree."""
    root = code_root()
    try:
        common = subprocess.run(["git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir"],
                                capture_output=True, text=True, check=True).stdout.strip()
        main = os.path.dirname(os.path.normpath(common))
        if os.path.isdir(os.path.join(main, "client")):
            return main
    except (OSError, subprocess.CalledProcessError):
        pass
    return root


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def refuse_pristine(path):
    if "\\client\\ia_" in os.path.abspath(path).replace("/", "\\").lower():
        sys.exit(f"refusing to write into a pristine extracted build: {path}")


def expected_exe(lane, root):
    """What patch_client.py --show must print for the lane's client copy."""
    cfg = LANES[lane]
    ca = os.path.join(root, cfg["certs"], "ca_modulus.bin")
    want = {"redirector": cfg["address"], "bind": cfg["address"], "appdata": "DawngateData" + cfg["suffix"],
            "documents": "Dawngate" + cfg["suffix"], "raknet_loopback": cfg["address"], "ca_flag": "0",
            "laa": "on"}
    if os.path.exists(ca):
        with open(ca, "rb") as f:
            want["ca_sha256"] = hashlib.sha256(f.read()).hexdigest()[:16]
    return want


# lane a's host copy predates A4 (its sockets are the harness's: block_sockets=1, the D1 bind hook), so only these are
# checked there; the host copy of lane b, c or d is a copy of that lane's client copy and carries every lane patch
HOST_KEYS = ("redirector", "appdata", "documents")


def exe_state(exe, lane, root, keys=None):
    """[] if the exe is patched for the lane, else the differences (only `keys` if given)."""
    if not os.path.exists(exe) or not os.path.exists(exe + ".orig"):
        return ["not patched (no .orig)"]
    with open(exe, "rb") as f:
        got = patch_client.describe(f.read())
    return [f"{k}={got.get(k)} (want {v})" for k, v in expected_exe(lane, root).items()
            if got.get(k) != v and (keys is None or k in keys)]


def config_patched(data_dir, address):
    """patch_config.py --host <address> ran: the text app properties name <address>:18080 (stored uncompressed)."""
    pkg = os.path.join(data_dir, "Config.package")
    if not os.path.exists(pkg + ".orig") or not os.path.exists(pkg):
        return False
    with open(pkg, "rb") as f:
        return f"http://{address}:18080".encode() in f.read()


def locale_step(data_dir, address, check_only, missing):
    """the client copy's added locale package (patch_locale.py --host <address>: the login texts, the links at
    the lane's dgserver pages), byte for byte what patch_locale.py makes from the copy's own Locale.package."""
    shipped, added = patch_locale.paths(data_dir)
    want = patch_locale.build(shipped, address)
    if os.path.isfile(added):
        with open(added, "rb") as f:
            if f.read() == want:
                return
    if check_only:
        missing.append(f"{added} (patch_locale.py --host {address})")
        return
    run([sys.executable, "-I", os.path.join(HERE, "patch_locale.py"), data_dir, "--host", address])


def run(cmd):
    print("  $ " + " ".join(cmd))
    r = subprocess.run(cmd)
    if r.returncode:
        sys.exit(f"failed (exit {r.returncode}): {' '.join(cmd)}")


def free_gb(path):
    """Free space in GB (10^9 bytes) on the drive that holds `path` (its nearest existing parent)."""
    while not os.path.exists(path) and os.path.dirname(path) != path:
        path = os.path.dirname(path)
    return shutil.disk_usage(path).free / 1e9


def check_space(path, copies):
    """A6b: print the free space before copying `copies` game copies (1.5 GB each); refuse below MIN_FREE_GB."""
    free = free_gb(path)
    print(f"free space: {free:.1f} GB on the drive of {path} (this lane copies about {1.5 * copies:.1f} GB)")
    if free < MIN_FREE_GB:
        sys.exit(f"only {free:.1f} GB free; a lane needs about 3 GB (refusing below {MIN_FREE_GB} GB)")


def setup(lane, dll, with_host, check_only, root=None):
    cfg = LANES[lane]
    root = root or main_root()
    py = sys.executable
    j = lambda p: os.path.join(root, *p.split("/"))  # noqa: E731
    client, host, certs = j(cfg["client"]), j(cfg["host"]), j(cfg["certs"])
    exe = os.path.join(client, "game", "Dawngate.exe")
    data_dir = os.path.join(client, "game", "Data")
    missing = []
    print(f"lane {lane}: {cfg['address']}, main checkout {root}")

    # 0. A6b: room for the copies still to make (about 1.5 GB each)
    copies = (not os.path.exists(exe)) + (with_host and not os.path.exists(os.path.join(host, "game", "Dawngate.exe")))
    if copies and not check_only:
        check_space(client, copies)
    # 1. the client copy (from the pristine extraction, read only)
    if not os.path.exists(exe):
        if check_only:
            missing.append(f"client copy {client}")
        else:
            src = j(PRISTINE)
            if not os.path.exists(os.path.join(src, "game", "Dawngate.exe")):
                sys.exit(f"{src} is missing (extract Dawngate.zip first: re/scripts/extract_client.py)")
            refuse_pristine(client)
            print(f"copying {src} -> {client} (about 1.5 GB) ...")
            shutil.copytree(src, client, copy_function=shutil.copy2)
    # 2. the lane's certificates (CN = the lane's address)
    if not all(os.path.exists(os.path.join(certs, n)) for n in ("server_cert.pem", "server_key.pem", "ca_modulus.bin")):
        if check_only:
            missing.append(f"certificates {certs}")
        else:
            run([py, "-I", os.path.join(code_root(), "server", "tools", "make_certs.py"), certs, cfg["address"]])
    # 3. the patches (from the .orig backups; skipped when the copy carries them already)
    if os.path.exists(exe):
        diff = exe_state(exe, lane, root)
        if diff and check_only:
            missing.append(f"{exe}: {'; '.join(diff)}")
        elif diff:
            cmd = [py, "-I", os.path.join(HERE, "patch_client.py"), exe, os.path.join(certs, "ca_modulus.bin"),
                   cfg["address"]]
            run(cmd + (["--data-suffix", cfg["suffix"]] if cfg["suffix"] else []))
            if exe_state(exe, lane, root):
                sys.exit(f"{exe} is still not patched for lane {lane}: {exe_state(exe, lane, root)}")
        if not config_patched(data_dir, cfg["address"]):
            if check_only:
                missing.append(f"config packages in {data_dir} (patch_config.py --host {cfg['address']})")
            else:
                run([py, "-I", os.path.join(HERE, "patch_config.py"), data_dir, "--host", cfg["address"]])
        locale_step(data_dir, cfg["address"], check_only, missing)
    # 4. the match-host copy with the harness (setup_server_copy.py copies the lane's client copy once)
    host_keys = HOST_KEYS if lane == "a" else None
    if with_host:
        hexe = os.path.join(host, "game", "Dawngate.exe")
        loader = os.path.join(host, "game", "PhysXLoader.dll")
        if check_only:
            if not os.path.exists(os.path.join(host, "game", "PhysXLoader_orig.dll")):
                missing.append(f"host copy {host} (setup_server_copy.py)")
            elif exe_state(hexe, lane, root, host_keys):
                missing.append(f"{hexe}: {'; '.join(exe_state(hexe, lane, root, host_keys))}")
            elif dll and os.path.exists(dll) and sha256(dll) != sha256(loader):
                missing.append(f"{loader} is not {dll} (setup_server_copy.py installs it)")
        else:
            if not dll or not os.path.exists(dll):
                sys.exit(f"the host copy needs dgsim.dll: {dll} is missing (build gms/, or pass --dll; --no-host skips)")
            refuse_pristine(host)
            run([py, "-I", os.path.join(HERE, "setup_server_copy.py"), client, host, dll])
            if exe_state(hexe, lane, root, host_keys):  # a host copy made before the client copy's last patch
                run([py, "-I", os.path.join(HERE, "patch_client.py"), hexe, os.path.join(certs, "ca_modulus.bin"),
                     cfg["address"]] + (["--data-suffix", cfg["suffix"]] if cfg["suffix"] else []))
            if not config_patched(os.path.join(host, "game", "Data"), cfg["address"]):
                run([py, "-I", os.path.join(HERE, "patch_config.py"), os.path.join(host, "game", "Data"),
                     "--host", cfg["address"]])
    # 5. A6b: the lane's accounts folder (dgserver --data; it would also make it at its first save, so --check doesn't
    # count it as missing)
    data = j(cfg["data"])
    if not check_only and not os.path.isdir(data):
        os.makedirs(data)
        print(f"made the accounts folder {data}")
    if check_only:
        for m in missing:
            print(f"  MISSING  {m}")
        print(f"lane {lane}: {'ready' if not missing else f'{len(missing)} item(s) missing'}")
        return 1 if missing else 0
    print(f"lane {lane} is set up: run_session.ps1 -Lane {lane}")
    return 0


def ca_sha(certs):
    """The first 16 hex digits of the CA modulus's SHA-256 (patch_client.py --show's ca_sha256), or None."""
    path = os.path.join(certs, "ca_modulus.bin")
    if not os.path.exists(path):
        return None
    with open(path, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()[:16]


def expected_extra(name, root):
    """What patch_client.py --show must print for an extra client copy (A5b): its lane's redirector and CA, its own
    bind address (RakNet's loopback literal with it) and data folders."""
    cfg = EXTRA_CLIENTS[name]
    lane = LANES[cfg["lane"]]
    want = {"redirector": lane["address"], "bind": cfg["bind"], "appdata": "DawngateData" + cfg["suffix"],
            "documents": "Dawngate" + cfg["suffix"], "raknet_loopback": cfg["bind"], "ca_flag": "0",
            "laa": "on"}
    sha = ca_sha(os.path.join(root, *lane["certs"].split("/")))
    if sha:
        want["ca_sha256"] = sha
    return want


def expected_lan(address, root):
    """What patch_client.py --show must print for the LAN test copy (A5b): the lan profile for this PC's address,
    sockets narrowed to it, RakNet's start-up send test to it, its own data folders."""
    want = {"redirector": address, "bind": address, "appdata": "DawngateData" + LAN["suffix"],
            "documents": "Dawngate" + LAN["suffix"], "raknet_loopback": address, "ca_flag": "0",
            "laa": "on"}
    sha = ca_sha(os.path.join(root, *LAN["certs"].split("/")))
    if sha:
        want["ca_sha256"] = sha
    return want


def describe_state(exe, want):
    """[] if the exe carries `want` (patch_client.describe), else the differences."""
    if not os.path.exists(exe) or not os.path.exists(exe + ".orig"):
        return ["not patched (no .orig)"]
    with open(exe, "rb") as f:
        got = patch_client.describe(f.read())
    return [f"{k}={got.get(k)} (want {v})" for k, v in want.items() if got.get(k) != v]


def cert_cn(certs):
    """The redirector certificate's CN in `certs` (None if missing; '?' if it can't be read)."""
    path = os.path.join(certs, "server_cert.pem")
    if not os.path.exists(path):
        return None
    try:
        from cryptography import x509
        from cryptography.x509.oid import NameOID
        with open(path, "rb") as f:
            cert = x509.load_pem_x509_certificate(f.read())
        names = [a.value for a in cert.subject.get_attributes_for_oid(NameOID.COMMON_NAME)]
        return names[0] if len(names) == 1 else "?"
    except Exception:  # noqa: BLE001 (any parse problem: report, don't guess)
        return "?"


def ensure_copy(root, client, check_only, missing):
    """The client copy from the pristine extraction (read only); False if it is missing in a check."""
    exe = os.path.join(client, "game", "Dawngate.exe")
    if os.path.exists(exe):
        return True
    if check_only:
        missing.append(f"client copy {client}")
        return False
    check_space(client, 1)
    src = os.path.join(root, *PRISTINE.split("/"))
    if not os.path.exists(os.path.join(src, "game", "Dawngate.exe")):
        sys.exit(f"{src} is missing (extract Dawngate.zip first: re/scripts/extract_client.py)")
    refuse_pristine(client)
    print(f"copying {src} -> {client} (about 1.5 GB) ...")
    shutil.copytree(src, client, copy_function=shutil.copy2)
    return True


def finish(what, missing, check_only):
    if check_only:
        for m in missing:
            print(f"  MISSING  {m}")
        print(f"{what}: {'ready' if not missing else f'{len(missing)} item(s) missing'}")
        return 1 if missing else 0
    print(f"{what} is set up")
    return 0


def setup_extra(name, check_only, root=None):
    """A5b: an extra client copy of a lane (EXTRA_CLIENTS), e.g. lane d's second client client/run_854_e."""
    cfg = EXTRA_CLIENTS[name]
    lane = LANES[cfg["lane"]]
    root = root or main_root()
    py = sys.executable
    j = lambda p: os.path.join(root, *p.split("/"))  # noqa: E731
    client, certs = j(cfg["client"]), j(lane["certs"])
    exe = os.path.join(client, "game", "Dawngate.exe")
    missing = []
    print(f"copy {name}: lane {cfg['lane']}'s client {name} (server {lane['address']}, bind {cfg['bind']}, "
          f"%APPDATA%\\DawngateData{cfg['suffix']}), main checkout {root}")
    if not all(os.path.exists(os.path.join(certs, n)) for n in ("server_cert.pem", "ca_modulus.bin")):
        missing.append(f"lane {cfg['lane']}'s certificates {certs} (setup_lane.py {cfg['lane']} first)")
        return finish(f"copy {name}", missing, True) if check_only else sys.exit(missing[-1])
    if ensure_copy(root, client, check_only, missing):
        diff = describe_state(exe, expected_extra(name, root))
        if diff and check_only:
            missing.append(f"{exe}: {'; '.join(diff)}")
        elif diff:
            run([py, "-I", os.path.join(HERE, "patch_client.py"), exe, os.path.join(certs, "ca_modulus.bin"),
                 lane["address"], "--client-bind", cfg["bind"], "--data-suffix", cfg["suffix"]])
            if describe_state(exe, expected_extra(name, root)):
                sys.exit(f"{exe} is still not patched: {describe_state(exe, expected_extra(name, root))}")
        data_dir = os.path.join(client, "game", "Data")
        if not config_patched(data_dir, lane["address"]):
            if check_only:
                missing.append(f"config packages in {data_dir} (patch_config.py --host {lane['address']})")
            else:
                run([py, "-I", os.path.join(HERE, "patch_config.py"), data_dir, "--host", lane["address"]])
        locale_step(data_dir, lane["address"], check_only, missing)
    return finish(f"copy {name}", missing, check_only)


def lan_address(text):
    """--address: this PC's LAN address (A5b; patch_client.py's rule: our own network, not loopback or public), or
    'auto' = the IPv4 address of the interface with the default route (as launch_monitor.ps1 picks the capture NIC)."""
    if text == "auto":
        ps = ("$r = Get-NetRoute -DestinationPrefix 0.0.0.0/0 | Sort-Object RouteMetric | Select-Object -First 1; "
              "(Get-NetIPAddress -AddressFamily IPv4 -InterfaceIndex $r.ifIndex | Select-Object -First 1).IPAddress")
        text = subprocess.run(["powershell", "-NoProfile", "-Command", ps], capture_output=True,
                              text=True).stdout.strip()
    try:
        return patch_client.lan_address(text, "--address")
    except patch_client.PatchError as e:
        sys.exit(str(e))


def setup_lan(address, check_only, new_certs=False, root=None):
    """A5b: the LAN profile's test copy (LAN) and certificates with CN = this PC's LAN address."""
    root = root or main_root()
    py = sys.executable
    j = lambda p: os.path.join(root, *p.split("/"))  # noqa: E731
    client, certs = j(LAN["client"]), j(LAN["certs"])
    exe = os.path.join(client, "game", "Dawngate.exe")
    missing = []
    print(f"lan copy: {client} for a server on {address} (lan profile, sockets on {address}, "
          f"%APPDATA%\\DawngateData{LAN['suffix']}), certificates {certs}, main checkout {root}")
    cn = cert_cn(certs)
    if cn != address:
        if check_only or (cn is not None and not new_certs):
            missing.append(f"certificates {certs} with CN {address} (they are for CN {cn}; --new-certs replaces "
                           "them)" if cn else f"certificates {certs} (make_certs.py, CN {address})")
            if not check_only:
                sys.exit(missing[-1])
        else:
            run([py, "-I", os.path.join(code_root(), "server", "tools", "make_certs.py"), certs, address])
    if ensure_copy(root, client, check_only, missing):
        want = expected_lan(address, root)
        diff = describe_state(exe, want)
        if diff and check_only:
            missing.append(f"{exe}: {'; '.join(diff)}")
        elif diff:
            run([py, "-I", os.path.join(HERE, "patch_client.py"), exe, os.path.join(certs, "ca_modulus.bin"),
                 "--profile", "lan", "--server-host", address, "--client-bind", address,
                 "--data-suffix", LAN["suffix"]])
            if describe_state(exe, expected_lan(address, root)):
                sys.exit(f"{exe} is still not patched: {describe_state(exe, expected_lan(address, root))}")
        data_dir = os.path.join(client, "game", "Data")
        if not config_patched(data_dir, address):
            if check_only:
                missing.append(f"config packages in {data_dir} (patch_config.py --host {address})")
            else:
                run([py, "-I", os.path.join(HERE, "patch_config.py"), data_dir, "--host", address])
        locale_step(data_dir, address, check_only, missing)
    return finish("lan copy", missing, check_only)


def main(argv):
    if len(argv) > 1 and argv[1] in EXTRA_CLIENTS:
        # A5b: setup_lane.py e|f [--check] [--root <dir>] (--dll is accepted and ignored: no host copy)
        ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
        ap.add_argument("name", choices=sorted(EXTRA_CLIENTS))
        ap.add_argument("--check", action="store_true")
        ap.add_argument("--dll")
        ap.add_argument("--root")
        a = ap.parse_args(argv[1:])
        return setup_extra(a.name, a.check, os.path.abspath(a.root) if a.root else None)
    if len(argv) > 1 and argv[1] == "lan":
        # A5b: setup_lane.py lan --address <IPv4>|auto [--check] [--new-certs] [--root <dir>]
        ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
        ap.add_argument("name", choices=["lan"])
        ap.add_argument("--address", required=True, help="this PC's LAN address, or auto")
        ap.add_argument("--check", action="store_true")
        ap.add_argument("--new-certs", action="store_true", help="replace server/certs/lan made for another address")
        ap.add_argument("--dll")
        ap.add_argument("--root")
        a = ap.parse_args(argv[1:])
        return setup_lan(lan_address(a.address), a.check, a.new_certs, os.path.abspath(a.root) if a.root else None)
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("lane", nargs="?", choices=sorted(LANES))
    ap.add_argument("--print", dest="print_lane", choices=sorted(LANES), help="print the lane's settings and exit")
    ap.add_argument("--check", action="store_true", help="only check; exit 1 if something is missing")
    ap.add_argument("--dll", help="the dgsim.dll to install into the host copy (default: this checkout's "
                                  "gms/build/Release/dgsim.dll)")
    ap.add_argument("--no-host", action="store_true", help="leave the host copy out")
    ap.add_argument("--root", help="the folder that holds client/ and server/certs (default: the main checkout)")
    a = ap.parse_args(argv[1:])
    if a.print_lane:
        for k, v in LANES[a.print_lane].items():
            print(f"{k}={v}")
        return 0
    if not a.lane:
        ap.error("name a lane (b, c or d)")
    if a.lane == "a" and not a.check:
        sys.exit("lane a is the original setup (docs/RUNNING_THE_CLIENT.md *How to start it*, play.ps1 -SetupOnly); "
                 "this script only checks it (--check)")
    dll = a.dll or os.path.join(code_root(), "gms", "build", "Release", "dgsim.dll")
    return setup(a.lane, os.path.abspath(dll), not a.no_host, a.check, os.path.abspath(a.root) if a.root else None)


if __name__ == "__main__":
    sys.exit(main(sys.argv))
