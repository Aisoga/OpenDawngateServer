"""Compare a setup folder made by the release's Go setup (Start-Server.exe) with what setup_all.py's
Python tools make, file by file.

What setup_all.py would make for the same server address and the same CA (the CA is random per install, so the
references are computed with the Go setup's own ca_modulus.bin):
  client exe     patch_client.patch_bytes(untouched exe, CA, local or lan profile, the address)
  host exe       patch_client.patch_bytes(untouched exe, CA, "local", 127.0.0.1)  (setup_all.py's host exe step)
  client config  patch_config.patched_package(untouched package, --host <address>) for the three packages
  host config    the same with --host 127.0.0.1
  host DLL       PhysXLoader.dll = the release's dgsim.dll, PhysXLoader_orig.dll = the game's loader, dgsim.ini =
                 setup_server_copy.DEFAULT_INI (CRLF), every Lua script = the release's (setup_server_copy.py copies
                 them byte for byte)
  the rest       every other file of build 854 in both copies: the manifest's size and SHA-256 and, with --pristine,
                 the time extract_client.py gave it; every .orig the untouched file
  content        with --content-ref, data\\content.json byte for byte that file (e.g. the main checkout's
                 server\\data\\content.json, made by snap.py + assetdata.py + dgserver --make-content)
  certificates   certs\\<address>: the five files, the CA modulus = the CA certificate's, CN = the address

Usage (from the repository folder; it reads the game's files, so python -I):
    .venv\\Scripts\\python -I re\\scripts\\test_p3b.py <Dawngate-Data folder> --release <release folder>
        [--address 127.0.0.1] [--pristine client\\ia_Dawngate\\Dawngate] [--content-ref server\\data\\content.json]
Exit 0 when everything matches, 1 otherwise. Nothing is written.
"""
import argparse
import hashlib
import ipaddress
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)  # -I drops the script folder
import patch_client  # noqa: E402
import patch_config  # noqa: E402
import setup_all  # noqa: E402
import setup_server_copy  # noqa: E402

LOCAL = "127.0.0.1"
EXE = "game/Dawngate.exe"
CONFIGS = ("game/Data/Config.package", "game/Data/Config_Ship.package", "game/Data/Config_Pack.package")
LOADER = "game/PhysXLoader.dll"


def sha(b):
    return hashlib.sha256(b).hexdigest()


def read(p):
    with open(p, "rb") as f:
        return f.read()


class Report:
    def __init__(self):
        self.bad = 0
        self.good = 0

    def check(self, ok, what, detail=""):
        if ok:
            self.good += 1
        else:
            self.bad += 1
            print(f"  DIFFERS  {what}{': ' + detail if detail else ''}")
        return ok

    def line(self, text):
        print(f"  {text}")


def config_args(host):
    return argparse.Namespace(host=host, http_port=18080, telemetry_port=9988, afk_minutes=None,
                              afk_disconnect_minutes=None, dry_run=False, restore=False, afk_hits=[])


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("root", help="the Go setup's folder (Dawngate-Data)")
    ap.add_argument("--release", required=True, help="the release folder (host\\dgsim.dll, host\\lua)")
    ap.add_argument("--address", default=LOCAL, help="the server address the setup was made for")
    ap.add_argument("--pristine", help="client\\ia_Dawngate\\Dawngate: compare the files' times too")
    ap.add_argument("--content-ref", help="a content.json made by the Python pipeline")
    a = ap.parse_args(argv[1:])
    if not sys.flags.isolated:
        sys.exit("run this with python -I: it reads the game's files")
    manifest = setup_all.load_manifest(os.path.join(HERE, "..", "..", "docs", "file_manifest.csv"))
    untouched = {setup_all.in_copy(k): v for k, v in manifest.items()}
    j = os.path.join
    client, host = j(a.root, "client", "Dawngate"), j(a.root, "host", "Dawngate")
    certs = j(a.root, "certs", a.address)
    r = Report()
    lan = not ipaddress.IPv4Address(a.address).is_loopback

    print(f"certificates ({certs})")
    from cryptography import x509
    from cryptography.x509.oid import NameOID
    modulus = read(j(certs, "ca_modulus.bin"))
    ca = x509.load_pem_x509_certificate(read(j(certs, "ca_cert.pem")))
    srv = x509.load_pem_x509_certificate(read(j(certs, "server_cert.pem")))
    r.check(ca.public_key().public_numbers().n.to_bytes(128, "big") == modulus, "ca_modulus.bin is the CA's modulus")
    r.check(srv.issuer == ca.subject, "server_cert.pem is issued by ca_cert.pem")
    cn = [x.value for x in srv.subject.get_attributes_for_oid(NameOID.COMMON_NAME)]
    r.check(cn == [a.address], "the server certificate's CN", f"{cn}")
    r.line(f"CA {sha(modulus)[:16]}")

    orig_exe = read(j(client, EXE + ".orig"))
    r.check(sha(orig_exe) == untouched[EXE][1], "client Dawngate.exe.orig is the untouched exe")
    profile = "lan" if lan else "local"
    for name, copy, prof, srv_host in (("client exe", client, profile, a.address), ("host exe", host, "local", LOCAL)):
        want, _ = patch_client.patch_bytes(orig_exe, modulus, prof, None, srv_host, None)
        got = read(j(copy, EXE))
        r.check(got == want, f"{name} = patch_client.py {prof} {srv_host}", f"{sha(got)[:16]} vs {sha(want)[:16]}")
        print(f"{name}: {sha(got)}  ({'equal' if got == want else 'DIFFERENT'})")

    for name, copy, cfg_host in (("client config", client, a.address), ("host config", host, LOCAL)):
        for rel in CONFIGS:
            base = j(copy, rel + ".orig")
            if not os.path.isfile(base):
                base = j(copy, rel)
            r.check(sha(read(base)) == untouched[rel][1], f"{name}: the untouched {os.path.basename(rel)}")
            want, _, _ = patch_config.patched_package(base, config_args(cfg_host), os.path.basename(rel))
            if want is None:
                want = read(base)
            got = read(j(copy, rel))
            r.check(got == want, f"{name} {os.path.basename(rel)} = patch_config.py --host {cfg_host}")
            print(f"{name} {os.path.basename(rel)}: {sha(got)}  ({'equal' if got == want else 'DIFFERENT'})")

    print("host DLL")
    game = j(host, "game")
    dll = read(j(a.release, "host", "dgsim.dll"))
    r.check(read(j(game, "PhysXLoader.dll")) == dll, "host PhysXLoader.dll = the release's dgsim.dll")
    r.check(sha(read(j(game, "PhysXLoader_orig.dll"))) == untouched[LOADER][1], "PhysXLoader_orig.dll = the game's loader")
    ini = setup_server_copy.DEFAULT_INI.replace("\r\n", "\n").replace("\n", "\r\n").encode("ascii")
    r.check(read(j(game, "dgsim.ini")) == ini, "dgsim.ini = setup_server_copy.py's DEFAULT_INI")
    lua_dir = j(a.release, "host", "lua")
    luas = sorted(n for n in os.listdir(lua_dir) if n.endswith(".lua"))
    lua_ok = sum(read(j(game, n)) == read(j(lua_dir, n)) for n in luas if os.path.isfile(j(game, n)))
    r.check(lua_ok == len(luas), "the Lua scripts", f"{lua_ok} of {len(luas)} equal")
    r.line(f"dgsim.dll {sha(dll)[:16]}, {len(luas)} Lua scripts, dgsim.ini")

    print("the other files of build 854 in both copies")
    for label, copy, own in (("client", client, set(CONFIGS) | {EXE}), ("host", host, set(CONFIGS) | {EXE, LOADER})):
        n = 0
        for rel, (size, digest) in sorted(untouched.items()):
            if rel in own:
                continue
            p = j(copy, *rel.split("/"))
            ok = os.path.isfile(p) and os.path.getsize(p) == size and setup_all.sha256(p) == digest
            if a.pristine and ok:
                q = j(a.pristine, *rel.split("/"))
                ok = int(os.path.getmtime(p)) == int(os.path.getmtime(q))
            r.check(ok, f"{label} {rel}")
            n += ok
        r.line(f"{label}: {n} files equal to the manifest{' and with extract_client.py' + chr(39) + 's times' if a.pristine else ''}")
        extra = []
        for dp, _dn, fn in os.walk(copy):
            for f in fn:
                rel = os.path.relpath(j(dp, f), copy).replace("\\", "/")
                if rel not in untouched and not rel.endswith(".orig") and not (label == "host" and (
                        rel.startswith("game/dgsim") or rel == "game/PhysXLoader_orig.dll")):
                    extra.append(rel)
        r.line(f"{label}: extra files {extra[:5] if extra else 'none'}")

    if a.content_ref:
        got, want = read(j(a.root, "data", "content.json")), read(a.content_ref)
        r.check(got == want, "content.json = the reference", f"{sha(got)[:16]} vs {sha(want)[:16]}")
        print(f"content.json: {sha(got)}  ({'equal' if got == want else 'DIFFERENT'})")

    print(f"\n{r.good} checks equal, {r.bad} different")
    return 1 if r.bad else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
