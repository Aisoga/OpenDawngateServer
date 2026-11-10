# Decision 7: Host machines in other regions

- **Status:** accepted (2026-11-09, the maintainer's request for a North American server beside the European one). A design: not built yet.
- **In short:** a server can have host machines elsewhere, each in a region. A host machine runs match hosts only, with no logins, accounts or website. It connects to the main server through the main server's tunnel port, encrypted and authenticated, and players reach its matches directly through a second tunnel session to the host machine, which the main server sets up for them.

## Context

- **Players log in once, on the main server.** The game's play screen has a region choice (North America, Europe), which the original used for its own server regions. A custom game should run where its creator chose.
- **Host machines on other machines already existed, for one trusted network:** a host machine's agent registered with the main server over a plain control connection (a challenge and response over a shared key), and its match hosts dialled the main server's control port directly. Across the internet that would put the control port, unencrypted, on the internet.
- **Every player connection already crosses the encrypted tunnel** (TLS 1.3 streams and an AES-GCM datagram channel), and the player pins the server's key. Routing a match on another machine through the main server's tunnel would send a North American player's match traffic across the Atlantic twice.
- **The main server is shared with other services,** so nothing that needs installing or a new port there is welcome.

## Decision

1. **The host machine dials the main server's tunnel port.** The link is a TLS 1.3 connection to the main server's pinned key, with a hello kind of its own. Inside it the registration stays a challenge and response, now with a key per host machine and bound to the TLS session. The main server's control port stays on its loopback address, and its firewall doesn't change.
2. **The match hosts' control connections go through the agent.** A host dials the agent on its own machine, and the agent carries its lines inside the link. The hosts are unchanged, and the main server sees each host's connection as coming from that machine's link.
3. **Players reach a host machine directly.** The host machine runs a tunnel gateway of its own, for match datagrams only, with its own key. When the main server puts a player into a game there, it sends the player's Start-Client that gateway's address, its key and a ticket for this match and this player, inside the player's existing session. Start-Client opens a second session there and sends only that match's port through it. The player's trust in the second gateway comes from the main server they already trust.
4. **Tickets are short-lived and single-use,** signed with a key that only the main server and that host machine derive from their current link. A ticket names the host machine, the match and the player, and a session with a ticket reaches only that match.
5. **The seat check stays the main server's.** The agent tells the main server which player's ticket stands behind a match connection, in the same channel as the host's question, and the main server answers.
6. **The region comes from the main server's configuration,** not from the host machine, and both machines run the same release. A host machine that is down, full or of another release means the game runs on the main server, and the lobby says so.

## The alternatives

- **A VPN between the machines.** It needs an install and a port on the main server, which serves other sites, and it would still leave the match traffic to choose a path.
- **Relaying the match traffic through the main server's tunnel,** which needs nothing new: the North American players would pay a transatlantic round trip twice on every datagram.
- **The hosts dialling the main server themselves over TLS.** It needs a TLS library in the match host, and gives up the ordering that lets the seat check work without a race.
- **A login on the host machine.** Players would need a second account or a second login, which the maintainer ruled out.
- **The host machine checking the seats alone.** It would answer faster, but the main server is where the players and their games are known, and a host machine's word about a seat is only as good as its tickets anyway.

## Consequences

- A host machine's operator is trusted with the results of its matches and sees its players' addresses, as any match host's operator is.
- A break of the link between the machines ends that machine's running matches, as a lost agent always did.
- A player needs nothing new: the same Start-Client, one login, the region choice the game already has.
