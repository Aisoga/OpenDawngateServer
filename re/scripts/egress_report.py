"""Check a test-run capture for traffic that leaves the machine (roadmap item A1).

Reads the capture written by launch_monitor.ps1 (NIC + loopback) with tshark and prints
  1. DNS queries (mDNS/.local and LLMNR left out),
  2. outbound flows on non-loopback interfaces: TCP SYNs and UDP datagrams sent from this host,
     grouped by destination, with the DNS name the capture resolved for that address,
  3. loopback connections per destination port (our stubs) and multicast sent over loopback.
The capture is system-wide, so other programs show up too. A line is FLAGGED when it is EA-related
(159.153.0.0/16, or a name matching ea.com, easports.com, origin.com, dawngate, waystone) or when its
local port belongs to Dawngate.exe according to monitor.log ("TCP a:p -> ..." / "UDP bound a:p"), in the same
protocol: a TCP flow is matched against the game's TCP ports, a UDP flow against its UDP ports (C1b: c1b_run7 had
flagged another program's IPv6 TCP connection whose local port number the client held as a UDP port on 127.0.0.1).
Unflagged outbound lines are other processes as far as we can tell; read them anyway.

LAN tests (roadmap A5, docs/RUNNING_THE_CLIENT.md "Playing across machines (A5)"): --allow-host <IPv4>
names one of our own machines in the test (repeatable): the server's LAN address on a client machine, a
client's address on the server machine. Flows from Dawngate.exe ports to such an address are listed with
"(allowed: our LAN machine)" instead of being flagged. Only addresses of our own network are accepted
(not loopback, wildcard, multicast or public), and the EA rules still apply to them, so nothing else
changes: a Dawngate.exe flow to any other address, and anything EA-related, is still flagged.

A capture whose last packet was cut short (P1's play mode stops dumpcap at the end of a session) is read anyway,
with a note line; any other tshark error exits 2.

Test lanes (roadmap A6 and A6b, docs/RUNNING_THE_CLIENT.md *Test lanes (A6, A6b)*): up to four sessions can run at
once, each on its own loopback address (lane a 127.0.0.1, b 127.0.0.2, c 127.0.0.3, d 127.0.0.4; any loopback address
is accepted), and every capture sees all of them. --lane-address <loopback IPv4> (default 127.0.0.1) names the
session's own address: the loopback section counts the TCP connections to it by port (any source; until A6 only those
with source = destination), lists the loopback connections to other addresses (the other lanes, other programs)
separately, and FLAGS a loopback packet sent from one of this session's game endpoints (address and port, from
monitor.log) to another loopback address: the copy would be talking to another lane's server (cross-lane). The
endpoints carry the lane's address, so another lane's game on the same port number is not this session's. The
outbound rules above are unchanged, so every lane's report flags anything that leaves the machine, whichever lane sent
it.

Several clients and the LAN profile on this PC (roadmap A5b): --monitor and --lane-address are repeatable. Each
--monitor names one client's launch_monitor.ps1 log (the game endpoints of all of them count; default: monitor.log next
to the capture); each further --lane-address is another address of the same session (a second client's own loopback
address, 127.0.4.2 for lane d's), so its packets to itself or to the lane's server aren't cross-lane. With
--allow-host <this PC's LAN address> (a -Lan session: server and client on one PC, nothing leaves it), the traffic
between the PC's own non-loopback addresses (Npcap sees it on the loopback adapter) is listed in its own section, by
destination port; a game endpoint on the LAN address sending to a loopback address is still flagged as cross-lane.

Exit code: 0 clean, 1 flagged lines, 2 usage/tshark error.

Usage:
    .venv/Scripts/python -I re/scripts/egress_report.py <session dir | capture.pcapng> [--monitor monitor.log ...]
        [--tshark "C:/Program Files/Wireshark/tshark.exe"] [--allow-host <IPv4> ...] [--lane-address 127.0.0.3 ...]
"""
import argparse
import collections
import ipaddress
import os
import re
import socket
import subprocess
import sys

EA_NAME = re.compile(r"(^|\.)(ea\.com|easports\.com|origin\.com)$|dawngate|waystone", re.IGNORECASE)
EA_NET = ipaddress.ip_network("159.153.0.0/16")
FIELDS = ["frame.time_relative", "frame.interface_name", "ip.src", "ip.dst", "ipv6.src", "ipv6.dst",
          "tcp.srcport", "tcp.dstport", "tcp.flags.syn", "tcp.flags.ack", "udp.srcport", "udp.dstport",
          "dns.flags.response", "dns.qry.name", "dns.a", "dns.aaaa"]


def local_addresses():
    addrs = {"127.0.0.1", "::1"}
    for info in socket.getaddrinfo(socket.gethostname(), None):
        addrs.add(info[4][0].split("%")[0])
    return addrs


def dawngate_ports(monitor):
    """Local ports owned by Dawngate.exe according to launch_monitor.ps1's log."""
    ports = set()
    if monitor and os.path.exists(monitor):
        text = open(monitor, encoding="utf-8-sig", errors="replace").read()
        for m in re.finditer(r"(?:TCP|UDP bound) \S*:(\d+)", text):
            ports.add(int(m.group(1)))
    ports.discard(0)
    return ports


def dawngate_ports_by_proto(monitor):
    """Dawngate.exe's local ports per protocol ({"TCP": {...}, "UDP": {...}}) from launch_monitor.ps1's log."""
    ports = {"TCP": set(), "UDP": set()}
    if monitor and os.path.exists(monitor):
        text = open(monitor, encoding="utf-8-sig", errors="replace").read()
        for m in re.finditer(r"(TCP|UDP) (?:bound )?\S*:(\d+)", text):
            ports[m.group(1)].add(int(m.group(2)))
    for v in ports.values():
        v.discard(0)
    return ports


def dawngate_endpoints(monitor):
    """Dawngate.exe's local endpoints {(proto, address, port)} from launch_monitor.ps1's log (A6: the address too,
    so a loopback packet can be told apart from another lane's game on the same port number)."""
    eps = set()
    if monitor and os.path.exists(monitor):
        with open(monitor, encoding="utf-8-sig", errors="replace") as f:
            text = f.read()
        for m in re.finditer(r"(TCP|UDP) (?:bound )?(\d+\.\d+\.\d+\.\d+):(\d+)", text):
            if int(m.group(3)):
                eps.add((m.group(1), m.group(2), int(m.group(3))))
    return eps


def lane_address(text):
    """argparse type for --lane-address: an IPv4 loopback address (A6, A6b: lanes a-d are 127.0.0.1-127.0.0.4)."""
    try:
        ip = ipaddress.IPv4Address(text)
    except ValueError:
        raise argparse.ArgumentTypeError(f"not an IPv4 address: {text!r}") from None
    if not ip.is_loopback:
        raise argparse.ArgumentTypeError(f"{ip} is not a loopback address (a lane's address is 127.x.y.z)")
    return str(ip)


def cross_lane(proto, src, sport, dst, game_eps, lane):
    """A6: a loopback packet from this session's game endpoint to another loopback address (not multicast).
    `lane` is the session's address, or (A5b) a collection of its addresses (a second client's own)."""
    own = {lane} if isinstance(lane, str) else set(lane)
    if (proto, src, sport) not in game_eps or dst in own:
        return False
    ip = ipaddress.ip_address(dst)
    return ip.version == 4 and ip.is_loopback


def lan_host(text):
    """argparse type for --allow-host: an IPv4 address of our own network (A5)."""
    try:
        ip = ipaddress.IPv4Address(text)
    except ValueError:
        raise argparse.ArgumentTypeError(f"not an IPv4 address: {text!r}") from None
    if ip.is_loopback or ip.is_unspecified or ip.is_multicast or ip.is_reserved or ip.is_global:
        raise argparse.ArgumentTypeError(f"{ip} is not an address of our own network (LAN machines only)")
    return str(ip)


def outbound_reasons(dst, name, sports, dg_ports, allowed=()):
    """Why an outbound flow is flagged ([] = it isn't). A flow from Dawngate.exe ports to an allowed LAN
    machine (--allow-host) isn't flagged for that; the EA rules apply to every address."""
    reason = []
    ip = ipaddress.ip_address(dst)
    if ip.version == 4 and ip in EA_NET:
        reason.append("EA address range")
    if name and EA_NAME.search(name):
        reason.append("EA-related name")
    if sports & dg_ports and dst not in allowed:
        reason.append(f"Dawngate.exe port {sorted(sports & dg_ports)}")
    return reason


CUT_SHORT = "appears to have been cut short in the middle of a packet"


def tshark_result(returncode, stderr):
    """(ok, note) for a tshark run: a capture whose last packet was cut short is read anyway.

    P1's play mode stops dumpcap at the end of a session (it has no time limit), which can cut the last
    packet in half; tshark then exits non-zero (14 in 4.6.8) after printing every complete packet.
    """
    if returncode == 0:
        return True, ""
    if CUT_SHORT in stderr:
        return True, "the capture's last packet was cut short (dumpcap stopped while writing); every complete packet was read"
    return False, ""


def rows(tshark, capture, notes):
    cmd = [tshark, "-r", capture, "-T", "fields", "-E", "separator=\t", "-E", "occurrence=a",
           "-E", "aggregator=,"] + [a for f in FIELDS for a in ("-e", f)]
    out = subprocess.run(cmd, capture_output=True, text=True, encoding="utf-8", errors="replace")
    ok, note = tshark_result(out.returncode, out.stderr)
    if not ok:
        print(f"tshark failed (exit {out.returncode}): {out.stderr.strip()}", file=sys.stderr)
        sys.exit(2)
    if note:
        notes.append(note)
    for line in out.stdout.splitlines():
        vals = line.split("\t")
        yield dict(zip(FIELDS, vals + [""] * (len(FIELDS) - len(vals))))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("path", help="session directory (with capture.pcapng, monitor.log) or a capture file")
    ap.add_argument("--monitor", action="append", default=[],
                    help="launch_monitor.ps1 log (default: monitor.log next to the capture; A5b: repeatable, one per "
                         "client of the session)")
    ap.add_argument("--tshark", default=r"C:\Program Files\Wireshark\tshark.exe")
    ap.add_argument("--allow-host", type=lan_host, action="append", default=[], metavar="IPV4",
                    help="one of our own LAN machines in an A5 test (repeatable); not flagged for Dawngate.exe ports")
    ap.add_argument("--lane-address", type=lane_address, action="append", default=[], metavar="IPV4",
                    help="A6: the session's own loopback address (lanes a, b, c, d: 127.0.0.1 to 127.0.0.4; default "
                         "127.0.0.1); A5b: repeatable, a further one is another client's of the same session")
    args = ap.parse_args()
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    capture = os.path.join(args.path, "capture.pcapng") if os.path.isdir(args.path) else args.path
    monitors = args.monitor or [os.path.join(os.path.dirname(capture), "monitor.log")]
    if not os.path.exists(capture):
        print(f"no capture at {capture}")
        return 2
    local = local_addresses()
    dg_ports, game_eps = set(), set()
    dg_by_proto = {"TCP": set(), "UDP": set()}
    for monitor in monitors:
        dg_ports |= dawngate_ports(monitor)
        for k, v in dawngate_ports_by_proto(monitor).items():
            dg_by_proto[k] |= v
        game_eps |= dawngate_endpoints(monitor)
    lanes = args.lane_address or ["127.0.0.1"]
    lane = lanes[0]
    lan_own = [a for a in args.allow_host if a in local]  # A5b: this PC's own LAN address (a -Lan session)

    names = {}                                   # address -> DNS name, from answers in the capture
    queries = collections.OrderedDict()          # name -> [count, first time]
    outbound = collections.OrderedDict()         # (proto, dst, dport) -> [count, first time, {sport}]
    loop_tcp = collections.Counter()             # loopback dst port on the lane's address -> SYN count
    loop_other = collections.Counter()           # (dst, dport) -> SYN count, loopback TCP to other addresses
    loop_cross = collections.OrderedDict()       # (proto, src, sport, dst, dport) -> [count, first time] (A6)
    loop_mcast = collections.Counter()           # (src, sport, dst, dport) multicast over loopback
    loop_lan = collections.Counter()             # (proto, dst, dport) -> count: to this PC's own LAN address (A5b)
    notes = []                                   # tshark notes (a cut-short capture)
    for r in rows(args.tshark, capture, notes):
        src, dst = r["ip.src"] or r["ipv6.src"], r["ip.dst"] or r["ipv6.dst"]
        if not src or not dst:
            continue
        src, dst = src.split(",")[-1], dst.split(",")[-1]  # innermost header if tunnelled
        t = float(r["frame.time_relative"] or 0)
        loopback = "loopback" in r["frame.interface_name"].lower()
        udp_ports = {r["udp.srcport"], r["udp.dstport"]}
        if r["dns.qry.name"] and not udp_ports & {"5353", "5355"}:
            if r["dns.flags.response"] in ("1", "True"):
                for a in (r["dns.a"] + "," + r["dns.aaaa"]).split(","):
                    if a:
                        names[a] = r["dns.qry.name"].split(",")[0]
            else:
                q = queries.setdefault(r["dns.qry.name"], [0, t])
                q[0] += 1
            continue
        if r["tcp.srcport"]:
            if r["tcp.flags.syn"] not in ("1", "True") or r["tcp.flags.ack"] in ("1", "True"):
                continue
            proto, sport, dport = "TCP", int(r["tcp.srcport"]), int(r["tcp.dstport"])
        elif r["udp.srcport"]:
            proto, sport, dport = "UDP", int(r["udp.srcport"]), int(r["udp.dstport"])
        else:
            continue
        if loopback:
            if cross_lane(proto, src, sport, dst, game_eps, lanes):
                c = loop_cross.setdefault((proto, src, sport, dst, dport), [0, t])
                c[0] += 1
            if dst in lan_own and src in local:
                loop_lan[(proto, dst, dport)] += 1  # A5b: on this PC, to its own LAN address (stays on the PC)
            elif proto == "TCP" and dst in lanes:
                loop_tcp[dport] += 1
            elif proto == "TCP" and ipaddress.ip_address(dst).is_loopback:
                loop_other[(dst, dport)] += 1
            elif ipaddress.ip_address(dst).is_multicast:
                loop_mcast[(src, sport, dst, dport)] += 1
            continue
        if src not in local or dport == 53:
            continue
        f = outbound.setdefault((proto, dst, dport), [0, t, set()])
        f[0] += 1
        f[2].add(sport)

    flagged = 0

    def flag(reason):
        nonlocal flagged
        if reason:
            flagged += 1
            return "  <-- FLAGGED: " + reason
        return ""

    print(f"capture {capture}")
    for note in notes:
        print(f"note: {note}")
    print(f"Dawngate.exe local ports from {', '.join(monitors)}:TCP {sorted(dg_by_proto['TCP']) or 'none'}, "
          f"UDP {sorted(dg_by_proto['UDP']) or 'none'}")
    print(f"this session's loopback address (A6 lane): {lane}"
          + (f" (and its further clients': {', '.join(lanes[1:])})" if lanes[1:] else ""))
    if args.allow_host:
        print(f"allowed LAN machines (A5): {', '.join(args.allow_host)}")
    print("\n== DNS queries (mDNS/LLMNR excluded)")
    for name, (n, t) in queries.items():
        print(f"  t={t:7.1f}s  {n:3}x  {name}" + flag("EA-related name" if EA_NAME.search(name) else ""))
    if not queries:
        print("  none")
    print("\n== Outbound flows on non-loopback interfaces (TCP SYN / UDP from this host)")
    for (proto, dst, dport), (n, t, sports) in outbound.items():
        name = names.get(dst, "")
        own = dg_by_proto[proto]  # the game's ports in this protocol only
        reason = outbound_reasons(dst, name, sports, own, args.allow_host)
        note = "  (allowed: our LAN machine)" if dst in args.allow_host and sports & own and not reason else ""
        print(f"  t={t:7.1f}s  {n:3}x  {proto} -> {dst}:{dport}  {name}  (local ports {sorted(sports)[:6]})"
              + note + flag(", ".join(reason)))
    if not outbound:
        print("  none")
    print(f"\n== Loopback TCP connections to this session's address {'/'.join(lanes)} by destination port (SYNs)")
    print("  " + (", ".join(f"{p}: {n}" for p, n in sorted(loop_tcp.items())) or "none"))
    if lan_own:
        print(f"\n== On this PC to its own LAN address {', '.join(lan_own)} (A5b lan profile; stays on the PC; "
              "TCP SYNs and UDP datagrams by destination port)")
        print("  " + (", ".join(f"{proto} {d}:{p}: {n}" for (proto, d, p), n in sorted(loop_lan.items())) or "none"))
    print("\n== Loopback TCP connections to other loopback addresses (other lanes, other programs; SYNs)")
    print("  " + (", ".join(f"{d}:{p}: {n}" for (d, p), n in sorted(loop_other.items())) or "none"))
    print(f"\n== This session's game on loopback to an address other than {'/'.join(lanes)} (cross-lane, A6)")
    for (proto, src, sport, dst, dport), (n, t) in loop_cross.items():
        print(f"  t={t:7.1f}s  {n:3}x  {proto} {src}:{sport} -> {dst}:{dport}"
              + flag("the game talks to another lane's address"))
    if not loop_cross:
        print("  none")
    print("\n== Multicast sent over loopback (stays on this machine)")
    for (src, sport, dst, dport), n in loop_mcast.items():
        who = "  (Dawngate.exe)" if sport in dg_by_proto["UDP"] else ""
        print(f"  {n:3}x  UDP {src}:{sport} -> {dst}:{dport}{who}")
    if not loop_mcast:
        print("  none")
    print(f"\n{flagged} flagged line(s)")
    return 1 if flagged else 0


if __name__ == "__main__":
    sys.exit(main())
