"""Flood a test lane's server and match host (the limits' live check).

Purpose: from a loopback source address of its own, hammer a lane's dgserver and match host while a normal client
plays there, so the session shows that the limits (dgserver --limits on, the hosts' net_limits=1) refuse the flood
and the normal client's match stays smooth (the host's `timing:` lines flat). Floods only loopback: by default lane
d's 127.0.0.4 from 127.0.4.9 (lane d's address range; no other lane's address is touched); any other target needs
--any-loopback and must still be 127.x. Stdlib only; nothing here reads game files.

Modes (run together, each in its own threads, for --seconds):
  connect  TCP connects to every server port (42127, 10041, 17502, 18080, 9988), up to --hold open at once
  login    Blaze login requests (Authentication::login, random names and passwords) on --login-conns connections,
           each waiting for its answer; counts replies and ERR_SERVER_BUSY (0x4072) refusals; reconnects when closed
  blaze    Util::ping requests as fast as the socket takes them on one connection; counts the replies and how long
           until the server closes it; then again
  qos      UDP datagrams to the QoS port (17502)
  raknet   UDP datagrams to the GMS ports (10042-10051): RakNet open-connection requests (offline) and junk that
           looks like a connected datagram (0x84)
  tunnel   TLS 1.3 handshakes with the tunnel gateway (--gateway-port, default 42128) as fast as they go, each
           connection closed after its handshake; counts handshakes completed and connections the gateway reset
           before any TLS (the limits refuse a connection before its handshake)
  tunnel-udp UDP datagrams to the gateway port that look like channel datagrams (type 1, a random session
           id, 100 bytes); the gateway's refusals show in dgserver's log only (the limits and stats lines)

Lane a's tunnel check (run_session.ps1 -Tunnel puts lane a's gateway on 127.0.1.9):
  python -I re/scripts/flood.py --any-loopback --target 127.0.1.9 --source 127.0.0.66 --modes tunnel,tunnel-udp

Usage:
  python re/scripts/flood.py [--target 127.0.0.4] [--source 127.0.4.9] [--seconds 60] [--start-after 0]
                             [--modes connect,login,blaze,qos,raknet,tunnel,tunnel-udp] [--udp-rate 20000]
                             [--gateway-port 42128] [--out <file.json>]
Prints one line per mode at the end (and every 10 s with --progress); --out writes the same as JSON.
"""
import argparse
import ipaddress
import json
import random
import select
import socket
import ssl
import string
import struct
import sys
import threading
import time

TCP_PORTS = {"redirector": 42127, "blaze": 10041, "qos": 17502, "http": 18080, "telemetry": 9988}
QOS_PORT = 17502
GMS_PORTS = range(10042, 10052)
SERVER_BUSY = 0x4072

# RakNet 4's offline message id (OFFLINE_MESSAGE_DATA_ID) and ID_OPEN_CONNECTION_REQUEST_1 with protocol version 5
RAKNET_MAGIC = bytes([0x00, 0xFF, 0xFF, 0x00, 0xFE, 0xFE, 0xFE, 0xFE, 0xFD, 0xFD, 0xFD, 0xFD, 0x12, 0x34, 0x56, 0x78])
OPEN_REQUEST_1 = bytes([0x05]) + RAKNET_MAGIC + bytes([5]) + bytes(400)


# ---- TDF and Fire (just what a login and a ping need; re/scripts/tdf.py has the whole codec) ----------------------
def _label(tag):
    tag = tag.ljust(4)[:4]
    b = [(ord(c) - 0x20) & 0x3F for c in tag]
    v = b[0] << 18 | b[1] << 12 | b[2] << 6 | b[3]
    return bytes([(v >> 16) & 0xFF, (v >> 8) & 0xFF, v & 0xFF])


def _varint(n):
    out = bytearray([n & 0x3F | (0x80 if n > 0x3F else 0)])
    n >>= 6
    while n:
        out.append(n & 0x7F | (0x80 if n > 0x7F else 0))
        n >>= 7
    return bytes(out)


def tdf_string(tag, s):
    b = s.encode() + b"\0"
    return _label(tag) + bytes([1]) + _varint(len(b)) + b


def tdf_int(tag, v):
    return _label(tag) + bytes([0]) + _varint(v)


def fire(component, command, msg_id, payload=b"", msg_type=0):
    return struct.pack(">HHHHI", len(payload), component, command, 0, (msg_type << 28) | (msg_id & 0xFFFFF)) + payload


def login_request(name, password):
    return tdf_string("MAIL", name) + tdf_string("PASS", password) + tdf_string("TOKN", "") + tdf_int("TYPE", 0)


def read_frame(sock):
    """One Fire frame: (component, command, error, type, id) or None when the connection ended."""
    head = _read_n(sock, 12)
    if head is None:
        return None
    length, comp, cmd, err, word = struct.unpack(">HHHHI", head)
    if length and _read_n(sock, length) is None:
        return None
    return comp, cmd, err, word >> 28, word & 0xFFFFF


def _read_n(sock, n):
    buf = b""
    while len(buf) < n:
        try:
            chunk = sock.recv(n - len(buf))
        except OSError:
            return None
        if not chunk:
            return None
        buf += chunk
    return buf


# ---- the modes ----------------------------------------------------------------------------------------------------
class Stats:
    def __init__(self):
        self.lock = threading.Lock()
        self.c = {}

    def add(self, mode, key, n=1):
        with self.lock:
            self.c.setdefault(mode, {}).setdefault(key, 0)
            self.c[mode][key] += n

    def snapshot(self):
        with self.lock:
            return {m: dict(v) for m, v in self.c.items()}


def tcp_connect(args, port, timeout=2.0):
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.settimeout(timeout)
    s.bind((args.source, 0))
    s.connect((args.target, port))
    return s


def mode_connect(args, stats, stop):
    held = []
    ports = list(TCP_PORTS.values())
    i = 0
    while not stop.is_set():
        port = ports[i % len(ports)]
        i += 1
        try:
            s = tcp_connect(args, port, 1.0)
            s.setblocking(False)
            held.append(s)
            stats.add("connect", "connected")
        except OSError:
            stats.add("connect", "connect failed")
        if len(held) > args.hold:
            held.pop(0).close()
        # the server closes a refused connection at once: count those (readable with EOF or a reset)
        if held:
            r, _, _ = select.select(held, [], [], 0)
            for s in r:
                try:
                    data = s.recv(1)
                    gone = not data
                except OSError:
                    gone = True
                if gone:
                    stats.add("connect", "closed by the server")
                    held.remove(s)
                    s.close()
        time.sleep(args.connect_pause)
    for s in held:
        s.close()


def mode_login(args, stats, stop, n):
    rnd = random.Random(n)
    sock = None
    msg_id = 0
    while not stop.is_set():
        if sock is None:
            try:
                sock = tcp_connect(args, TCP_PORTS["blaze"])
                stats.add("login", "connections")
            except OSError:
                stats.add("login", "connect failed")
                time.sleep(0.05)
                continue
        name = "".join(rnd.choice(string.ascii_lowercase) for _ in range(10))
        password = "".join(rnd.choice(string.ascii_letters + string.digits) for _ in range(12))
        try:
            sock.sendall(fire(1, 0x28, msg_id, login_request(name, password)))
        except OSError:
            stats.add("login", "closed by the server")
            sock.close()
            sock = None
            continue
        msg_id += 1
        stats.add("login", "sent")
        while True:  # its answer (notifications in between are skipped)
            f = read_frame(sock)
            if f is None:
                stats.add("login", "closed by the server")
                sock.close()
                sock = None
                break
            comp, cmd, err, typ, _ = f
            if comp == 1 and cmd == 0x28 and typ in (1, 3):
                if typ == 1:
                    stats.add("login", "logged in")
                elif err == SERVER_BUSY:
                    stats.add("login", "refused 0x4072")
                else:
                    stats.add("login", "error %#06x" % err)
                break
    if sock:
        sock.close()


def mode_blaze(args, stats, stop):
    while not stop.is_set():
        try:
            sock = tcp_connect(args, TCP_PORTS["blaze"])
        except OSError:
            stats.add("blaze", "connect failed")
            time.sleep(0.05)
            continue
        stats.add("blaze", "connections")
        sock.settimeout(None)
        got = [0]
        ended = threading.Event()

        def reader():
            while True:
                f = read_frame(sock)
                if f is None:
                    break
                got[0] += 1
            ended.set()

        threading.Thread(target=reader, daemon=True).start()
        t0 = time.time()
        sent = 0
        ping = fire(9, 0x02, 0)
        while not stop.is_set() and not ended.is_set():
            try:
                sock.sendall(ping * 50)
                sent += 50
            except OSError:
                break
        ended.wait(2)
        stats.add("blaze", "pings sent", sent)
        stats.add("blaze", "pings answered", got[0])
        if ended.is_set() and not stop.is_set():
            stats.add("blaze", "closed by the server")
            stats.add("blaze", "seconds until closed (sum)", round(time.time() - t0, 1))
        sock.close()


def udp_socket(args):
    u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    u.bind((args.source, 0))
    u.setblocking(False)
    return u


def paced(args, stop, send, rate):
    """Calls send() about `rate` times a second until stop."""
    batch = max(1, rate // 200)
    while not stop.is_set():
        t = time.perf_counter()
        for _ in range(batch):
            send()
        left = batch / rate - (time.perf_counter() - t)
        if left > 0:
            time.sleep(left)


def mode_qos(args, stats, stop):
    u = udp_socket(args)
    junk = bytes(20)

    def send():
        try:
            u.sendto(junk, (args.target, QOS_PORT))
            stats.add("qos", "datagrams")
        except OSError:
            stats.add("qos", "send failed")

    paced(args, stop, send, args.udp_rate // 4)
    u.close()


def mode_raknet(args, stats, stop):
    u = udp_socket(args)
    junk = bytes([0x84]) + bytes(300)
    ports = list(GMS_PORTS)
    i = [0]

    def send():
        i[0] += 1
        port = ports[i[0] % len(ports)]
        data = OPEN_REQUEST_1 if (i[0] // len(ports)) % 2 else junk  # both kinds to every port
        try:
            u.sendto(data, (args.target, port))
            stats.add("raknet", "datagrams")
        except OSError:
            stats.add("raknet", "send failed")  # e.g. WSAECONNRESET from an earlier ICMP port unreachable
        # drain RakNet's answers (open-connection replies), so the socket's buffer doesn't matter
        try:
            while True:
                u.recvfrom(2048)
                stats.add("raknet", "answers")
        except OSError:
            pass

    paced(args, stop, send, args.udp_rate)
    u.close()


def mode_tunnel(args, stats, stop):
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE  # the gateway's key is pinned by the real client; a flooder doesn't care
    ctx.minimum_version = ssl.TLSVersion.TLSv1_3
    ctx.set_alpn_protocols(["dawngate-tunnel/1"])
    while not stop.is_set():
        try:
            raw = tcp_connect(args, args.gateway_port, 2.0)
        except OSError:
            stats.add("tunnel", "connect failed")
            time.sleep(0.01)
            continue
        try:
            with ctx.wrap_socket(raw, do_handshake_on_connect=False) as t:
                t.do_handshake()
                stats.add("tunnel", "handshakes completed")
        except (ssl.SSLError, ConnectionError, OSError) as e:
            if isinstance(e, (ConnectionResetError, ConnectionAbortedError)) or getattr(e, "winerror", None) in (10054, 10053):
                stats.add("tunnel", "reset before the handshake")
            elif isinstance(e, socket.timeout):
                stats.add("tunnel", "handshake timed out")
            else:
                stats.add("tunnel", "handshake failed")
        finally:
            raw.close()
        time.sleep(args.connect_pause)


def mode_tunnel_udp(args, stats, stop):
    u = udp_socket(args)
    rnd = random.Random(7)

    def send():
        pkt = bytes([1]) + rnd.getrandbits(64).to_bytes(8, "big") + bytes(91)
        try:
            u.sendto(pkt, (args.target, args.gateway_port))
            stats.add("tunnel-udp", "datagrams")
        except OSError:
            stats.add("tunnel-udp", "send failed")

    paced(args, stop, send, args.udp_rate // 4)
    u.close()


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--target", default="127.0.0.4")
    ap.add_argument("--source", default="127.0.4.9")
    ap.add_argument("--any-loopback", action="store_true", help="allow a loopback target other than 127.0.0.4")
    ap.add_argument("--seconds", type=float, default=60)
    ap.add_argument("--start-after", type=float, default=0)
    ap.add_argument("--modes", default="connect,login,blaze,qos,raknet")
    ap.add_argument("--udp-rate", type=int, default=20000, help="RakNet datagrams per second (QoS: a quarter)")
    ap.add_argument("--login-conns", type=int, default=4)
    ap.add_argument("--hold", type=int, default=200, help="connect: open connections held at once")
    ap.add_argument("--connect-pause", type=float, default=0.002)
    ap.add_argument("--gateway-port", type=int, default=42128, help="tunnel, tunnel-udp: the gateway's port")
    ap.add_argument("--progress", action="store_true")
    ap.add_argument("--out")
    args = ap.parse_args()
    for what, addr in (("target", args.target), ("source", args.source)):
        if not ipaddress.ip_address(addr).is_loopback:
            sys.exit(f"flood.py: --{what} {addr} is not a loopback address; this tool floods only loopback")
    if args.target != "127.0.0.4" and not args.any_loopback:
        sys.exit("flood.py: only lane d's 127.0.0.4 by default (--any-loopback for another loopback address)")
    modes = [m.strip() for m in args.modes.split(",") if m.strip()]
    unknown = set(modes) - {"connect", "login", "blaze", "qos", "raknet", "tunnel", "tunnel-udp"}
    if unknown:
        sys.exit(f"flood.py: unknown modes {sorted(unknown)}")
    if args.start_after > 0:
        time.sleep(args.start_after)
    stats, stop = Stats(), threading.Event()
    threads = []
    for m in modes:
        if m == "login":
            for n in range(args.login_conns):
                threads.append(threading.Thread(target=mode_login, args=(args, stats, stop, n), daemon=True))
        else:
            fn = {"connect": mode_connect, "blaze": mode_blaze, "qos": mode_qos, "raknet": mode_raknet,
                  "tunnel": mode_tunnel, "tunnel-udp": mode_tunnel_udp}[m]
            threads.append(threading.Thread(target=fn, args=(args, stats, stop), daemon=True))
    t0 = time.time()
    print(f"flood: {args.source} -> {args.target}, modes {','.join(modes)}, {args.seconds:.0f} s, started "
          f"{time.strftime('%H:%M:%S')}", flush=True)
    for t in threads:
        t.start()
    while time.time() - t0 < args.seconds:
        time.sleep(min(10, max(0.1, args.seconds - (time.time() - t0))))
        if args.progress:
            print(f"flood: {time.time() - t0:5.0f} s {json.dumps(stats.snapshot())}", flush=True)
    stop.set()
    for t in threads:
        t.join(5)
    snap = stats.snapshot()
    print(f"flood: ended {time.strftime('%H:%M:%S')} after {time.time() - t0:.0f} s", flush=True)
    for m in modes:
        print(f"flood {m}: " + ", ".join(f"{k} {v}" for k, v in sorted(snap.get(m, {}).items())), flush=True)
    if args.out:
        with open(args.out, "w", encoding="utf-8") as f:
            json.dump({"target": args.target, "source": args.source, "seconds": args.seconds, "started": t0,
                       "modes": modes, "counts": snap}, f, indent=1)


if __name__ == "__main__":
    main()
