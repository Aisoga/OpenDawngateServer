# How the restoration works

This page explains, in plain terms, what the project rebuilt and how the pieces fit together. You don't need it to play or to host.

## What was lost

The Dawngate client (build 1.0.903.854, late 2014) is preserved on archive.org. It needed two kinds of online servers, and both went offline in 2015. Neither was ever released.

- **EA's online platform ("Blaze")**: logins, accounts, the menus' data, the store, matchmaking, parties and chat.
- **The game server**: one per match. It ran the match itself, with units, abilities, damage, bots and the rules of the map, and sent the result to every player over the RakNet network library.

## What the client still has

The client contains much more than a client needs. The game shares one simulation between client and server. The server's code paths are still in the client, just switched off, and so are the scripts that define the gameplay rules: about 1,400 Lua scripts for every ability, item, creature and bot.

What the client doesn't have is the server's outer shell: accepting players, taking their commands, running the server's update loop, and sending the world to the players. The project reconstructed that part by studying how the client reads and writes every network message.

## The pieces

```
   Player's PC                       Server PC
  +------------------------+        +--------------------------------------------+
  | Dawngate.exe (patched) |        | Start-Server.exe                           |
  |                        | Blaze  |   logins, accounts, menus, matchmaking,    |
  |                        |<------>|   parties, chat, website, tunnel           |
  |                        |        |        |                                   |
  |                        |        |        | starts one per match              |
  |                        |        |        v                                   |
  |                        | RakNet | Match host: Dawngate.exe + dgsim.dll       |
  |                        |<------>|   runs the game's own simulation as the    |
  |                        |        |   match's server                           |
  +------------------------+        +--------------------------------------------+
```

**The server** (`server/`, written in Go) replaces EA's platform. It speaks the same protocol the client expects: the same secure handshake, the same message formats and the same notifications. It stores accounts, collections, perk pages and match history in a SQLite database. It also runs the matchmaker, the website and the encrypted tunnel for public servers.

**The match host** (`gms/`, written in C++) replaces the game server. It's an unusual design:

- Each match runs inside an unmodified copy of the game's own `Dawngate.exe`.
- The project's `dgsim.dll` is loaded in place of one of the game's libraries.
- The DLL switches the game into server mode and runs the game's own simulation, with the original Lua scripts, as the match's server.
- It adds the pieces the client lacks: the server's update loop, damage and death handling, command intake, sending the world to the players, fog of war, match flow (waves, camps, wells, the end of the match), bots' team decisions, and the network session with the players.

Because the game's own code and data run the match, every shaper, ability and item behaves the way the original game defines it. The match host runs without a window, graphics or sound.

**The player's game copy** is patched in a few places, in its own copy and never in your download:

- It contacts your server instead of EA's addresses.
- It trusts your server's certificate authority, which is made fresh for each install, instead of EA's.
- Its network sockets stay on the addresses it needs, and router port-opening (UPnP) is switched off.
- It may use 4 GB of memory instead of 2 GB, so long matches don't run out.

**The tunnel** is used for servers on the internet. `Start-Client.exe` runs a small relay on the player's PC. The game connects to that relay, and the relay carries everything to the server over one encrypted port (TLS 1.3 for TCP, AES-GCM for UDP). The game's own 2014 security never crosses the internet.

**The server list on the login screen** works without any change to how the game talks to a server:

- Since the game only ever talks to the relay on its own PC, one game copy serves every tunnelled server. What differs per server is only where the relay connects to.
- The relay has a **front**: it answers the game's first requests itself (where to log in, the pre-login handshake, the connection-quality test), so the login screen appears before any server is chosen.
- The login screen's movie gets a small addition of this project's own, written in ActionScript and compiled into `Start-Client.exe`. `Start-Client.exe` adds it to a copy of the player's own movie. It shows the list, and at LOGIN it adds the pick to the login name (`alex#b`). The game sends the login name as typed, so the pick reaches the front.
- At the login, the front opens the tunnel to the picked server, checks the server's key against the one trusted before (or trusts it on the first login), replays the game's two pre-login requests there so the server sees an ordinary connection, and passes the login on with the pick removed. From then on the connection belongs to that server.
- After a logout the game asks the front again where to log in, so the player is back at the list. Additions and removals also travel in the login name (`alex#+<address>`, `alex#-b`), because the movie has no other way to reach `Start-Client.exe`; the front keeps the list and writes it into the game copy for the next time the login screen loads.

## Why three languages

- **Go** for the server and the release's two programs: a single program file with no installation, modern TLS, and enough flexibility to also speak the client's old TLS 1.1 handshake.
- **C++** for the match host, because it runs inside the 32-bit game process and calls the game's own code.
- **Python** for the developers' setup and test scripts. The release doesn't need it: its setup is written in Go and tested to produce the same result byte for byte.

The detailed decision records are in [development/decisions/](development/decisions/).

## What's not in this repository

- No game files. Every player downloads the client from archive.org, and the setup checks it against a list of the original files' checksums ([`file_manifest.csv`](file_manifest.csv)).
- The reverse-engineering notes and research tools that led here aren't published. The code's comments still name the parts of the client each piece relies on.
