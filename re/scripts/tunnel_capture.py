"""Check a tunnelled session's capture: only the gateway's port crosses between player and server.

A run_session.ps1 -Tunnel session has three sides on loopback:
  - the player's PC: the lane's address (127.0.0.2 for lane b) and the second client's (127.0.2.2), where the game and
    the tunnel's client side (dgserver --tunnel-client) live; the game's plaintext (TLS 1.1 to the redirector, Blaze,
    QoS, HTTP, telemetry, RakNet) stays here, between the game and the tunnel;
  - "the network": every packet between a player address and the server address (127.0.N.9);
  - the server machine: 127.0.N.9 alone (the gateway's own sockets to the match hosts, RakNet in plaintext, as on a
    real server's own machine).
The check reads the capture with tshark and reports:
  1. the conversations that cross between player and server by protocol and server port: only the gateway port may
     appear (any other is FLAGGED: a plaintext service reached past the tunnel);
  2. the gateway's TCP streams: the TLS versions the server chose (TLS 1.3 only, else FLAGGED), records by content
     type, and that no record but the handshake's is readable;
  3. the gateway's UDP datagrams: count, sizes, the first byte (1 = a tunnel datagram), the byte entropy of the
     payloads, and that no RakNet offline magic or Blaze/QoS plaintext marker appears in them (FLAGGED if one does);
  4. where the plaintext markers do appear (player side, server side), as a cross-check that the capture saw them.
Exit code: 0 clean, 1 flagged, 2 usage or tshark error.

Lane a: the match host's sockets aren't narrowed to a lane address there, so its control channel to the
server machine (TCP 10040) leaves from 127.0.0.1, the player side's address: --server-own-port 10040 lists that port
as the server machine's own traffic instead of flagging it (lanes b-d bind the host to the lane's address).

One PC as server and player on its LAN address (--same-host): the release's check runs Start-Server on this PC's
LAN address and Start-Client's tunnel on the same PC, so the tunnel and the gateway share that address (the game and
the tunnel's client side talk on 127.0.0.1). Then a packet between the server address and itself "crosses" when one
of its ports is the gateway's (the tunnel) or a plain service port of the server (--service-port, default 42127,
10041, 17502, 18080, 9988: FLAGGED, a player reached it past the tunnel); every other one is the server's own (the
gateway's flows to the match hosts, the website's HTTPS from a test client).

Usage:
    .venv/Scripts/python -I re/scripts/tunnel_capture.py <session dir | capture.pcapng> --same-host --server 192.168.20.40
        --client 127.0.0.1
    .venv/Scripts/python -I re/scripts/tunnel_capture.py <session dir | capture.pcapng> --server 127.0.2.9
        --client 127.0.0.2 [--client 127.0.2.2] [--gateway-port 42128] [--server-own-port 10040] [--tshark <tshark.exe>]
"""
import argparse
import collections
import math
import os
import subprocess
import sys

# plaintext that must never cross the network inside the tunnel: RakNet's offline message magic, Blaze's service name,
# the QoS HTTP path, the redirector component's TDF tag area ("NAME" + service), telemetry's packet tag
MARKERS = {
    "RakNet offline magic": bytes.fromhex("00ffff00fefefefefdfdfdfd12345678"),
    "Blaze service name": b"battlegrounds-1-pc",
    "QoS HTTP request": b"GET /qos/",
    "HTTP request": b"HTTP/1.1",
    "telemetry tag": b"&lgr",
}

TLS_VERSIONS = {"0x0301": "TLS 1.0", "0x0302": "TLS 1.1", "0x0303": "TLS 1.2", "0x0304": "TLS 1.3"}


def run_tshark(tshark, capture, port, ips):
    flt = " or ".join(f"ip.addr=={ip}" for ip in ips)
    fields = ["frame.number", "ip.src", "ip.dst", "tcp.srcport", "tcp.dstport", "udp.srcport", "udp.dstport",
              "tcp.payload", "udp.payload", "tls.record.content_type", "tls.handshake.type",
              "tls.handshake.extensions.supported_version", "tls.record.version"]
    cmd = [tshark, "-r", capture, "-d", f"tcp.port=={port},tls", "-Y", f"({flt}) and (tcp or udp)", "-T", "fields",
           "-E", "separator=\t", "-E", "occurrence=a", "-E", "aggregator=,"]
    for f in fields:
        cmd += ["-e", f]
    out = subprocess.run(cmd, capture_output=True, text=True, encoding="utf-8", errors="replace")
    if out.returncode != 0 and not out.stdout:
        print(f"tshark failed (exit {out.returncode}): {out.stderr.strip()}", file=sys.stderr)
        sys.exit(2)
    for line in out.stdout.splitlines():
        cols = line.split("\t")
        cols += [""] * (len(fields) - len(cols))
        yield dict(zip(fields, cols))


def entropy(data):
    if not data:
        return 0.0
    counts = collections.Counter(data)
    n = len(data)
    return -sum(c / n * math.log2(c / n) for c in counts.values())


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("session")
    ap.add_argument("--server", required=True, help="the server's address (127.0.N.9)")
    ap.add_argument("--client", action="append", required=True, help="a player-side address (repeatable)")
    ap.add_argument("--gateway-port", type=int, default=42128)
    ap.add_argument("--server-own-port", type=int, action="append", default=[],
                    help="a server port reached from a player-side address by the server machine's own processes "
                         "(lane a: the match host's control channel, 10040): listed, not flagged (repeatable)")
    ap.add_argument("--same-host", action="store_true",
                    help="the server's address is the player's too (one PC on its LAN address): classify its own "
                         "packets by port")
    ap.add_argument("--service-port", type=int, action="append", default=[],
                    help="--same-host: a plain service port of the server that no player may reach on the server "
                         "address (repeatable; default 42127, 10041, 17502, 18080, 9988)")
    ap.add_argument("--tshark", default=r"C:\Program Files\Wireshark\tshark.exe")
    a = ap.parse_args()
    services = set(a.service_port or [42127, 10041, 17502, 18080, 9988])
    capture = a.session if a.session.endswith(".pcapng") else os.path.join(a.session, "capture.pcapng")
    if not os.path.isfile(capture):
        print(f"no capture: {capture}", file=sys.stderr)
        return 2
    clients, server, gp = set(a.client), a.server, a.gateway_port
    crossing = collections.Counter()  # (proto, server port) -> packets
    crossing_bytes = collections.Counter()
    tls_versions = collections.Counter()
    tls_types = collections.Counter()
    udp_first = collections.Counter()
    udp_sizes = []
    udp_payload = bytearray()
    tcp_payload_bytes = 0
    marker_sides = collections.defaultdict(collections.Counter)  # marker -> side -> packets
    flagged = []
    for r in run_tshark(a.tshark, capture, gp, sorted(clients | {server})):
        src, dst = r["ip.src"], r["ip.dst"]
        if not src or not dst:
            continue
        proto = "tcp" if r["tcp.srcport"] else "udp"
        sport = int(r["tcp.srcport"] or r["udp.srcport"] or 0)
        dport = int(r["tcp.dstport"] or r["udp.dstport"] or 0)
        hexdata = (r["tcp.payload"] or r["udp.payload"]).replace(":", "").replace(",", "")
        try:
            payload = bytes.fromhex(hexdata)
        except ValueError:
            payload = b""
        if a.same_host and src == server and dst == server:
            if gp in (sport, dport):
                side, sport_srv = "crossing", gp
            elif dport in services or sport in services:
                side, sport_srv = "crossing", dport if dport in services else sport
            else:
                side, sport_srv = "server", None
        elif src in clients and dst == server:
            side, sport_srv = "crossing", dport
        elif src == server and dst in clients:
            side, sport_srv = "crossing", sport
        elif src == server and dst == server:
            side, sport_srv = "server", None
        elif src in clients and dst in clients:
            side, sport_srv = "player", None
        else:
            continue
        for name, m in MARKERS.items():
            if m in payload:
                marker_sides[name][side] += 1
        if side != "crossing":
            continue
        crossing[(proto, sport_srv)] += 1
        crossing_bytes[(proto, sport_srv)] += len(payload)
        if sport_srv != gp:
            continue
        if proto == "tcp":
            tcp_payload_bytes += len(payload)
            for t in filter(None, r["tls.record.content_type"].split(",")):
                tls_types[t] += 1
            if "2" in r["tls.handshake.type"].split(","):  # a ServerHello: the version the server chose
                sv = r["tls.handshake.extensions.supported_version"].split(",")[0] or r["tls.record.version"].split(",")[0]
                tls_versions[TLS_VERSIONS.get(sv, sv)] += 1
        elif payload:
            udp_first[payload[0]] += 1
            udp_sizes.append(len(payload))
            if len(udp_payload) < 4_000_000:
                udp_payload += payload[17:]  # past the clear header: the ciphertext

    print(f"capture {capture}")
    print(f"player side {', '.join(sorted(clients))}; server {server}; gateway port {gp}"
          f"{'; same host: the server address by port' if a.same_host else ''}")
    print("\n1. between player and server (protocol, server port: packets, payload bytes):")
    if not crossing:
        print("   nothing (did the session reach the server?)")
        flagged.append("no traffic between player and server")
    for (proto, port), n in sorted(crossing.items(), key=lambda kv: (kv[0][0], kv[0][1] or 0)):
        own = proto == "tcp" and port in a.server_own_port
        mark = "" if port == gp else ("   (the server machine's own: --server-own-port)" if own else
                                      "   <-- FLAGGED: not the gateway port")
        if port != gp and not own:
            flagged.append(f"{proto} {port} crosses")
        print(f"   {proto} {port}: {n} packets, {crossing_bytes[(proto, port)]} bytes{mark}")
    print("\n2. the gateway's TCP streams (TLS):")
    print(f"   server-chosen versions: {dict(tls_versions) or 'none seen'}")
    names = {"20": "change_cipher_spec", "21": "alert", "22": "handshake", "23": "application_data"}
    print(f"   records by content type: {', '.join(f'{names.get(k, k)} {v}' for k, v in sorted(tls_types.items())) or 'none'}")
    print(f"   payload bytes {tcp_payload_bytes}")
    bad_versions = [v for v in tls_versions if v != "TLS 1.3"]
    if bad_versions:
        flagged.append(f"TLS versions {bad_versions}")
        print(f"   <-- FLAGGED: {bad_versions}")
    print("\n3. the gateway's UDP datagrams:")
    if udp_sizes:
        print(f"   {len(udp_sizes)} datagrams, {min(udp_sizes)}-{max(udp_sizes)} bytes (mean {sum(udp_sizes) / len(udp_sizes):.0f}); "
              f"first byte {dict(udp_first)}; ciphertext entropy {entropy(udp_payload):.3f} bits/byte over {len(udp_payload)} bytes")
        if set(udp_first) - {1}:
            flagged.append("UDP datagrams that are no tunnel datagrams")
            print("   <-- FLAGGED: datagrams with another first byte")
    else:
        print("   none")
    print("\n4. plaintext markers by side (packets): player = between the game and the tunnel on the player's PC,"
          " server = the server machine's own sockets, crossing = between them")
    for name in MARKERS:
        sides = marker_sides.get(name, {})
        line = ", ".join(f"{s} {n}" for s, n in sorted(sides.items())) or "not seen"
        mark = ""
        if sides.get("crossing"):
            mark = "   <-- FLAGGED: plaintext crossed"
            flagged.append(f"{name} crossed")
        print(f"   {name}: {line}{mark}")
    print(f"\n{len(flagged)} flagged: {'; '.join(flagged) if flagged else 'only the gateway port crosses, TLS 1.3 and the channel'}")
    return 1 if flagged else 0


if __name__ == "__main__":
    sys.exit(main())
