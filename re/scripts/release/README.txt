Dawngate Restoration: Start-Server and Start-Client          (release @VERSION@)
==============================================================================

An unofficial fan project to play the 2014 MOBA Dawngate again. It is not affiliated with or endorsed by
Electronic Arts or Waystone Games; Dawngate is a trademark of its owners. This release contains no game file:
every player needs their own Dawngate.zip from archive.org.

A WORD ON SAFETY. The game is 2014 code. At home nothing leaves your network; a public server (see "Hosting on
the internet") gets checked passwords, an encrypted tunnel, limits and a website for accounts, and is still
run at the operator's own risk. Players: play only on servers you trust, with a password you use nowhere else.


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
3. Double-click Start-Server.exe. Its first start asks who will play on the server: press Enter for "only this
   PC". Then it sets everything up in the folder Dawngate-Data (about a minute) and the server runs. Leave its
   window open.
4. Double-click Start-Client.exe. The game starts.
5. Log in with a login name (3-32 letters, digits and . _ - @, e.g. an e-mail address) and a password of
   8 or more characters: the first login with a new name creates the account with that password. Then PLAY and
   PRACTICE DAWNGATE. Pick your shaper (click a card, then Choose), a role (then Finalize) and a skin;
   then a match with bots begins. (shaper_select = off in Start-Server.ini skips this: everyone plays
   Fenmore.)
To stop: close the game, then press Ctrl+C in the Start-Server window (or close that window).
If Dawngate.zip is somewhere else, Start-Server.exe asks for it (drag it onto the window, or press Enter to
choose it); it also looks in your Downloads folder.


Playing over your home network (LAN)
------------------------------------
On the PC that runs the server:
1. At Start-Server.exe's first start, answer 2 ("your home network") and pick this PC's address in your
   network (the one marked "the one your router connects"; "ipconfig" shows it as "IPv4 Address", e.g.
   192.168.1.20). Later: Start-Server.exe --configure asks again, or set   address = 192.168.1.20   in
   Start-Server.ini and start Start-Server.exe again.
2. Its window shows the server's fingerprint, e.g. 3ff46759dcb1ea30.
3. If Windows asks whether the programs may use the network, allow them for private networks only.
   (A public address: README section "Hosting on the internet" below; the risk applies.)
On every other player's PC:
1. Unpack this release and put that player's own Dawngate.zip next to it.
2. Double-click Start-Client.exe and type the server's address when asked (it is remembered in
   Start-Client.ini), or start it as   Start-Client.exe 192.168.1.20
3. The first time, Start-Client.exe shows the server's fingerprint. Answer y only if it is the one the
   server's window shows: that is how the game knows it talks to your server and not to someone else.
   Instead, the server's owner can hand out the file Dawngate-Data\certs\<address>\ca_modulus.bin (public,
   only that file): Start-Client.exe 192.168.1.20 --ca ca_modulus.bin
The server's owner plays with Start-Client.exe on the server PC too.


Hosting on the internet (use at your own risk)
----------------------------------------------
At Start-Server.exe's first start (or with Start-Server.exe --configure) answer 3 ("the internet"). It asks:
- your internet address (your router shows it as its internet or WAN address) and, when this PC is behind a
  router, which of its home-network addresses the router forwards to (Start-Server.ini: address, bind);
- who may make accounts: invite (players register on the server's website with an invite code you make; the
  default), closed (only you make accounts) or open (anyone; at most 3 a day per internet address);
- a domain name for the website (optional: with one whose DNS A record points at your internet address the
  server gets a free certificate from Let's Encrypt by itself; without, browsers warn once about its own
  certificate);
- an admin account for the website's admin page (it prints its password once);
- whether you accept the risk notice it prints (i_accept_the_risk = yes; without it a public server doesn't
  start).
This is the public profile (profile = public): every player connection goes through the encrypted tunnel (TLS
1.3 and AES-GCM on TCP and UDP 42128; Start-Client.exe uses it by itself for a public address), limits on
connections, logins, games and packets are on, and the match hosts check everything a client sends.
On your router forward only TCP and UDP 42128 (the tunnel) and TCP 443 (the website) to that PC; nothing else,
and never TCP 10040. Start-Server.exe never opens ports and never uses UPnP. If the PC holds the internet
address itself (no router), let its firewall in only those ports.
Its window shows the tunnel key's fingerprint: the first time, Start-Client.exe shows it to each player, who
answers y only if it is the same. What remains your risk and duty (the notice says it in full): the players'
PCs trust your server, so keep it updated and private; players behind one address share the limits; you keep
personal data (names, addresses in the logs: publish a privacy page in Dawngate-Data\data\site\privacy.html
and answer players' requests); back up the accounts (below).

Accounts and the website
------------------------
- The website (https://<server address>/, port web_port = 443): players register there (under signup =
  invite with your code), change their password, delete their account or download their data; /status shows
  the players online and the matches. The game's "Sign up now!" and "Forgot Password?" links lead there.
- The admin page (https://<server address>/admin) for accounts with the admin role
  (Start-Server.exe --make-admin <login name>): accounts (search, disable, ban, a new password, delete),
  invite codes (make, list, revoke), the sign-up mode, the running matches (end one) and the audit log.
- The server sends no mail: a player who forgot the password asks you; Start-Server.exe --reset-password
  <login name> (or the admin page) prints a new one.
- Backups: the accounts are Dawngate-Data\data\accounts.db (with accounts.db-wal and accounts.db-shm). Stop the
  server and copy those three files (or the whole Dawngate-Data\data folder) somewhere safe, e.g. daily.

Ports
-----
The server listens on TCP 42127, 10041, 17502, 18080 and 9988 and on UDP 17502; each running match uses one UDP
port of 10042-10051 (the match host). Other machines in your home network reach them for LAN play. The website
uses TCP 443 (web_port). With the tunnel (the public profile, or tunnel = on) it also listens on TCP and UDP
42128: from the internet forward only that port and 443. The match hosts' control port (TCP 10040) stays on
127.0.0.1 and must never be forwarded.


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
Start-Server.ini   address, bind, profile (auto/home/public: public = the tunnel required and the limits on),
                   shaper_select (on/off), signup (open, invite, closed: who can make accounts),
                   signups_per_address, log_days (logs are deleted after so many days), tunnel (auto/on/off:
                   auto = on for the public profile), gateway_port (42128), web_port (443; 0 = no website),
                   web_domain, web_email, folder (where Dawngate-Data goes), zip, server_flags (more server
                   options, e.g.  --owned starter  or  --mm-team-size 1 --mm-fill-after 5s).
                   Start-Server.exe --configure asks the first start's questions again.
Accounts           Start-Server.exe --create-account <name>   makes an account and prints its password;
                   --reset-password <name>   prints a new password for a player who forgot it (the server
                   sends no mail); --account-status <name>=disabled (or banned, active) locks one out;
                   --make-invite [--invite-uses 5] [--invite-days 14]   prints an invite code (signup = invite;
                   players type it on the website's sign-up page), --list-invites, --revoke-invite <id>,
                   --make-admin <name> / --remove-admin <name>   (the website's admin page).
Start-Server.exe --check     only reports what is missing; --setup-only sets up without starting the server.
Start-Client.exe --check     only reports; --setup-only prepares the game copy without starting the game.
Start-Client.ini   server (the address to play on), folder, tunnel (auto/on/off: auto = for a public address;
                   on for a LAN server with tunnel = on), gateway_port.
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
THIRD-PARTY-NOTICES.txt   RakNet 4.081 (BSD and its patent grant; with this project's patches), Go (BSD), the
                          Go modules the programs link (SQLite in Go, golang.org/x/crypto, ...: BSD and MIT)
BUILD.txt                 the source commit and the files' SHA-256
Dawngate-Data\            made by the first start: the game copies, certificates, accounts, logs. Back up
                          Dawngate-Data\data to keep the accounts (with the server stopped). Never share the
                          game copies (they are the game's files), the certificates' *_key.pem files or the
                          logs (they hold login names and addresses).

Copyright (C) 2026 the Dawngate Restoration contributors. This project's code is licensed under the GNU AGPL v3
(LICENSE.txt); the source of this release is the project's repository at the commit in BUILD.txt. The game, its
files and its content belong to their owners and are not part of this release.
