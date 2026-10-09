Dawngate Restoration: Start-Server and Start-Client          (release @VERSION@)
==============================================================================

An unofficial fan project to play the 2014 MOBA Dawngate again. It is not affiliated with or endorsed by
Electronic Arts or Waystone Games; Dawngate is a trademark of its owners. This release contains no game file:
every player needs their own Dawngate.zip from archive.org.

WARNING - NOT FOR THE OPEN INTERNET. The server accepts any login (any e-mail and password open or create an
account), keeps the accounts in a plain file and isn't hardened against hostile clients or traffic. Run it for
yourself or for a group you trust, on your home network, never as a public server.


What you need
-------------
- Windows 10 or 11, 64-bit, with a graphics card that runs DirectX 9 games.
- Dawngate.zip (1.3 GB) from https://archive.org/details/Dawngate
- About 5 GB of free disk space on this drive: the zip, a copy of the game for playing and one for the
  server's match host (1.5 GB each).
Nothing else: no Python, Go or Visual Studio.


Playing on this PC (a practice match against bots)
--------------------------------------------------
1. Unpack this release into a folder of its own, e.g. C:\Games\Dawngate.
2. Put Dawngate.zip into the same folder, next to Start-Server.exe.
3. Double-click Start-Server.exe. Its first start sets everything up in the folder Dawngate-Data (about a
   minute), then the server runs. Leave its window open.
4. Double-click Start-Client.exe. The game starts.
5. Log in with any e-mail and password (the first login creates the account), then PLAY and
   PRACTICE DAWNGATE. Pick your shaper (click a card, then Choose), a role (then Finalize) and a skin;
   then a match with bots begins. (shaper_select = off in Start-Server.ini skips this: everyone plays
   Fenmore.)
To stop: close the game, then press Ctrl+C in the Start-Server window (or close that window).
If Dawngate.zip is somewhere else, Start-Server.exe asks for it (drag it onto the window, or press Enter to
choose it); it also looks in your Downloads folder.


Playing over your home network (LAN)
------------------------------------
On the PC that runs the server:
1. Find its address in your network: in a command prompt, "ipconfig" shows it as "IPv4 Address",
   e.g. 192.168.1.20.
2. Open Start-Server.ini (next to Start-Server.exe, written by its first start), set
       address = 192.168.1.20
   save it and start Start-Server.exe again. Its window shows the server's fingerprint, e.g. 3ff46759dcb1ea30.
3. If Windows asks whether the programs may use the network, allow them for private networks only.
On every other player's PC:
1. Unpack this release and put that player's own Dawngate.zip next to it.
2. Double-click Start-Client.exe and type the server's address when asked (it is remembered in
   Start-Client.ini), or start it as   Start-Client.exe 192.168.1.20
3. The first time, Start-Client.exe shows the server's fingerprint. Answer y only if it is the one the
   server's window shows: that is how the game knows it talks to your server and not to someone else.
   Instead, the server's owner can hand out the file Dawngate-Data\certs\<address>\ca_modulus.bin (public,
   only that file): Start-Client.exe 192.168.1.20 --ca ca_modulus.bin
The server's owner plays with Start-Client.exe on the server PC too.


Ports
-----
The server listens on TCP 42127, 10041, 17502, 18080 and 9988 and on UDP 17502; each running match uses one UDP
port of 10042-10051 (the match host). Other machines must reach them only for LAN play. Never forward them from
the internet.


Windows warnings
----------------
- SmartScreen may say the programs are unrecognized (they aren't signed): "More info", then "Run anyway".
- On a LAN address the Windows firewall asks once per program (Start-Server.exe, and the match host's
  Dawngate.exe in Dawngate-Data\host) whether it may use the network: allow private networks only. On this PC
  alone (address 127.0.0.1) the question can be cancelled.
- Your antivirus may look at the programs the first time; the setup patches a few bytes of the game's copy
  (the server address, the certificate authority), never your downloaded zip.


Options
-------
Start-Server.ini   address, shaper_select (on/off), folder (where Dawngate-Data goes), zip, server_flags (more
                   server options, e.g.  --owned starter  or  --mm-team-size 1 --mm-fill-after 5s).
Start-Server.exe --check     only reports what is missing; --setup-only sets up without starting the server.
Start-Client.exe --check     only reports; --setup-only prepares the game copy without starting the game.
Start-Client.ini   server (the address to play on), folder.
Logs: Dawngate-Data\logs (the server, and one folder per match under hosts). The game's own log is in
%APPDATA%\DawngateData.


When something goes wrong
-------------------------
- "Another program holds the server's ports": a server is running already (another Start-Server window).
- "No server is running": start Start-Server.exe first and wait for "The server runs".
- The setup stopped halfway: start the program again; it continues. If the same step fails again, delete
  the folder the message names inside Dawngate-Data; it is made anew.
- "is not the Dawngate.zip of archive.org": download it again; the setup checks every file against build
  1.0.903.854.


Files
-----
Start-Server.exe          the server and the setup (Go)
Start-Client.exe          starts the game (Go)
host\dgsim.dll            the match host, loaded by the game's copy in Dawngate-Data\host (C++, links RakNet)
host\lua\                 the match host's Lua scripts
LICENSE.txt               GNU Affero General Public License v3: this project's code
THIRD-PARTY-NOTICES.txt   RakNet 4.081 (BSD and its patent grant), Go (BSD)
BUILD.txt                 the source commit and the files' SHA-256
Dawngate-Data\            made by the first start: the game copies, certificates, accounts, logs. Back up
                          Dawngate-Data\data to keep the accounts. Never share the game copies (they are the
                          game's files), the certificates' *_key.pem files or the logs (they hold logins as typed).

Copyright (C) 2026 the Dawngate Restoration contributors. This project's code is licensed under the GNU AGPL v3
(LICENSE.txt); the source of this release is the project's repository at the commit in BUILD.txt. The game, its
files and its content belong to their owners and are not part of this release.
