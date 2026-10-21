OpenDawngateServer: Start-Server and Start-Client          (release @VERSION@)
==============================================================================

An unofficial fan project to play the 2014 MOBA Dawngate again. It is not affiliated with or endorsed by
Electronic Arts or Waystone Games; Dawngate is a trademark of its owners. This release contains no game file:
every player needs their own Dawngate.zip from archive.org, which Start-Client.exe downloads for them.

A WORD ON SAFETY. The game is 2014 code. At home the game and the server send nothing to the internet (see
"Privacy" for what Start-Client.exe itself sends); a public server (see "Hosting on the internet") gets
checked passwords, an encrypted tunnel, limits and a website for accounts, and is still run at the operator's
own risk. Players: play only on servers you trust, with a password you use nowhere else.


What you need
-------------
- Windows 10 or 11, 64-bit, with a graphics card that runs DirectX 9 games.
- An internet connection at the first start: Start-Client.exe downloads Dawngate.zip (1.3 GB) from
  https://archive.org/details/Dawngate (or uses your copy of it, put next to Start-Client.exe).
- About 5 GB of free disk space on this drive: the zip, a copy of the game for playing and one for the
  server's match host (1.5 GB each; the second only once you play on This PC).
Nothing else: no Python, Go or Visual Studio.


Playing on this PC (a practice match against bots)
--------------------------------------------------
1. Unpack this release into a folder of its own, e.g. C:\Games\Dawngate.
2. If you have Dawngate.zip, put it into the same folder, next to Start-Client.exe.
3. Double-click Start-Client.exe. Without Dawngate.zip its first start asks "Download Dawngate.zip (1.3 GB)
   from archive.org? [Y/n]": press Enter (an interrupted download continues at the next start; mirrors:
   game_sources = <url> in Start-Client.ini). It checks the zip against the original build, makes the game
   copy in the folder Dawngate-Data (about half a minute), then the game starts. The login screen opens with
   "This PC" selected (SERVER, above the login): the server on this PC, which Start-Client runs by itself.
   You need no Start-Server.exe for this.
4. Log in with a login name (3-32 letters, digits and . _ - @, e.g. an e-mail address) and a password of
   8 or more characters: the first login with a new name creates the account with that password. The first
   login to This PC also sets up the server's match host (about half a minute; the Start-Client window shows
   the steps); later logins start it in a second or two. Then PLAY and PRACTICE DAWNGATE. Pick your shaper
   (click a card, then Choose), a role (then Finalize) and a skin; then a match with bots begins.
To stop: close the game. Start-Client ends with it, and the server on this PC with it.
If Dawngate.zip is somewhere else, answer n to the download: Start-Client.exe then asks for it (drag it onto
the window, or press Enter to choose it); it also looks in your Downloads folder.
If a Start-Server.exe of the same folder runs (see the next sections), This PC is that server instead. A
folder has one server at a time: Start-Server.exe doesn't start while Start-Client runs the folder's server.


Playing over your home network (LAN)
------------------------------------
On the PC that runs the server:
1. At Start-Server.exe's first start, answer 2 ("your home network") and pick this PC's address in your
   network (the one marked "the one your router connects"; "ipconfig" shows it as "IPv4 Address", e.g.
   192.168.1.20). Later: Start-Server.exe --configure asks again, or set   address = 192.168.1.20   in
   Start-Server.ini and start Start-Server.exe again.
2. Its window shows the server's key fingerprint, e.g. 3ff46759dcb1ea30, and the whole key for handing out.
3. If Windows asks whether Start-Server.exe may use the network, allow it for private networks only.
   (A public address: README section "Hosting on the internet" below; the risk applies.)
On every other player's PC:
1. Unpack this release (Start-Client.exe downloads that player's own Dawngate.zip, or uses theirs next to it).
2. Start it once as   Start-Client.exe 192.168.1.20   (or add the server on the login screen: see the next
   section).
3. Log in. The first login to a new server stops with "New server key" while the server list shows the key's
   fingerprint: compare it with the one the server's window shows, and only if it is the same, press LOGIN
   again. That is how the game knows it talks to your server and not to someone else; from then on the key is
   checked at every login. Instead, the server's owner can hand out the whole key its window shows:
   Start-Client.exe 192.168.1.20 --gateway-pin <the 64 hex digits>   (no question then).
The server's owner plays with just Start-Client.exe on the server PC: This PC is the running server there.


The server list on the login screen
-----------------------------------
Every server is reached through its encrypted tunnel, and the login screen has a list of your servers. Its first
row is always This PC, the server on this PC (it can't be removed; its status says "starts at login" while it
isn't running, "setting up" with a percentage during its first setup, and "didn't start" when the setup or
start failed: the Start-Client window says why, and the next login tries again).
1. Start Start-Client.exe with a server's address once, e.g.   Start-Client.exe 203.0.113.5
   The address goes into your list.
2. On the login screen, SERVER above the login shows the server you log in to. Click it to open the list
   (SELECT A SERVER): each server with its status (online and its ping, offline, a new key, KEY CHANGED,
   no tunnel) and a sentence about the selected one. Click a server, then DONE, and log in as usual.
3. ADD SERVER in the list adds another server (its address, and a name if you like); the next login goes to
   it and adds it once it answers. REMOVE takes the selected server out of the list.
4. After a logout (or after the game logs you out when you are idle) the login screen and the list come back,
   and you can pick another server.
From then on just double-click Start-Client.exe: the list opens with the server you used last. The selector
works with the mouse (not with Tab or Escape). A new server's key is confirmed once: the first login there stops
with "New server key" and the list shows the key's fingerprint (compare it with the one its operator gives you),
the second login trusts it. Every later login checks it: KEY CHANGED means it isn't the same server any more, so
don't log in, and ask its operator (see "When something goes wrong"). The list is the file
Dawngate-Data\servers\bookmarks.json.
Your Start-Client.exe and a server must be the same release (this one is @VERSION@; Start-Client.exe --version
and Start-Server.exe --version print it). Any other version is marked "other version" in the list (a server of a
release before this check: "version unknown") and its logins are refused; This PC never is.
Updates: at each start a numbered release (such as v0.6) asks github.com once for the newest release and, when
there is a newer one, asks "Version v0.7 is available (you have v0.6). Update now? [Y/n]". Enter downloads it,
checks every file against its BUILD.txt, replaces this release's files (the running Start-Client.exe becomes
Start-Client.old.exe, which the next start deletes) and starts the new Start-Client.exe; Start-Server.ini,
Start-Client.ini, Dawngate.zip and Dawngate-Data stay. Close Start-Server.exe first if it runs from this folder.
update_check = off in Start-Client.ini stops the question. To update by hand, unpack the new release into the
same folder over the old files.


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
1.3 and AES-GCM on TCP and UDP 42128, as on every server), limits on connections, logins, games and packets are
on, and the match hosts check everything a client sends.
On your router forward only TCP and UDP 42128 (the tunnel) and TCP 443 (the website) to that PC; nothing else,
and never TCP 10040. Start-Server.exe never opens ports and never uses UPnP. If the PC holds the internet
address itself (no router), let its firewall in only those ports: nothing else listens on that address.
Its window shows the server's key fingerprint: players see it in the server list on the login screen (their
first login there stops and shows it; the second trusts it) and compare it with the one you publish, or you
publish the whole key for Start-Client.exe <address> --gateway-pin <key>. If the key ever changes
(a new Dawngate-Data\certs folder), every player's list shows KEY CHANGED and their logins are refused until
they delete the old key (Dawngate-Data\servers\<address>\gateway.pin). What remains your risk and duty (the notice says it in full): the players'
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
Players reach the server only on TCP and UDP 42128 (the tunnel; gateway_port); its services answer inside the
tunnel. The website uses TCP 443 (web_port). From the internet forward only those two. Inside the PC each running
match uses one UDP port of 10042-10051 on a loopback address of its own (127.0.1.10; for a server on 127.0.0.N,
127.0.N.10), and the match hosts' control port (TCP 10040) stays on 127.0.0.1: never forward them. So a
Start-Server and a Start-Client run side by side on one PC. The server Start-Client runs for This PC listens
only on 127.0.1.11 (TCP and UDP 42128, TCP 10040, its matches' UDP 10042-10051). With tunnel = off (a
developers' option) the server
listens on TCP 42127, 10041, 17502, 18080 and 9988, UDP 17502 and the matches' 10042-10051 on its address
instead, and players need Start-Client.exe --tunnel off.


Windows warnings
----------------
- SmartScreen may say the programs are unrecognized while a release isn't signed yet: "More info", then
  "Run anyway". Code signing policy: the project's README (https://github.com/Aisoga/OpenDawngateServer).
- On a LAN address the Windows firewall asks once whether Start-Server.exe may use the network: allow private
  networks only. On this PC alone (address 127.0.0.1) the question can be cancelled.
- Your antivirus may look at the programs the first time; the setup patches a few bytes of the game's copy
  (the server address, the certificate authority), never your downloaded zip.


Options
-------
Start-Server.ini   address, bind, profile (auto/home/public: public = the tunnel required and the limits on),
                   shaper_select (on/off), dedicated (no/yes: yes = a PC that only hosts: only the match
                   host's copy Dawngate-Data\host, never a copy for playing; once set up it starts without
                   Dawngate.zip; or Start-Server.exe --dedicated),
                   signup (open, invite, closed: who can make accounts),
                   signups_per_address, log_days (logs are deleted after so many days), tunnel (auto/on/off:
                   auto and on = the tunnel, off = a developers' option), gateway_port (42128), web_port
                   (443; 0 = no website),
                   web_domain, web_email, web_proxy (on: the website behind a reverse proxy such as nginx
                   that serves port 443, on 127.0.0.1:web_port), folder (where Dawngate-Data goes), zip,
                   server_flags (more server
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
This PC            the server Start-Client runs takes the game options (shaper_select, signup, log_days,
                   server_flags) of a Start-Server.ini in the same folder, never its website or public settings.
Start-Client.ini   server (the address to play on; empty: the server list), folder, tunnel (auto/on/off:
                   auto and on = the tunnel for every server; off = a server with tunnel = off), gateway_port,
                   picker (auto/on/off: auto = the server list on the login screen whenever the tunnel is
                   used; off = one server). Start-Client.exe <address or tag> preselects a server of the list;
                   --gateway-pin <key> with an address trusts the key its operator handed out.
Logs: Dawngate-Data\logs (the server, and one folder per match under hosts). The game's own log is in
%APPDATA%\DawngateData.


When something goes wrong
-------------------------
- "Another program holds the server's ports": a server is running already (another Start-Server window).
- "This folder's server already runs inside Start-Client.exe": close the game first (its server for This PC
  stops with it), or give Start-Server a folder of its own (folder = in Start-Server.ini).
- This PC "didn't start" (or "The server on this PC didn't start" at LOGIN): the Start-Client window says why,
  usually Dawngate.zip not found or the disk full; fix it and log in again. "still being set up": its first
  setup took longer than the login waits; log in again when the list says online.
- "New server key" at LOGIN: your first login to that server. Open the list (SERVER), compare the key it shows
  with the one the server's operator gives you, and press LOGIN again if it is the same.
- "This server runs another version than your Start-Client" at LOGIN: the list's sentence names both versions.
  Use the Start-Client.exe of the server's release (ask its operator); "version unknown" means the server runs a
  release from before the version check, and only its operator can update it.
- The setup stopped halfway: start the program again; it continues. If the same step fails again, delete
  the folder the message names inside Dawngate-Data; it is made anew.
- "is not the Dawngate.zip of archive.org": download it again; the setup checks every file against build
  1.0.903.854.
- In the server list: "offline" (or "The server you picked doesn't answer" at LOGIN): the server is down or
  its address is wrong; the list checks again every few seconds. "no tunnel": that server runs with
  tunnel = off; ask its operator. "KEY CHANGED": ask the operator which fingerprint the server shows; only if
  it is the new one,
  delete Dawngate-Data\servers\<address>\gateway.pin and log in again.


Files
-----
Start-Server.exe          the server and the setup (Go), for hosting other players
Start-Client.exe          starts the game, with the server list on the login screen, and runs the server for
                          This PC (Go; it embeds the list's compiled ActionScript class, this project's code)
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

Privacy
-------
Nothing goes to this project's developers. Start-Client.exe sends your login and your game to the server you
pick; at each start a numbered release asks api.github.com for a newer release (update_check = off in
Start-Client.ini stops it); with the Discord app running, your status goes to Discord, and with Join the
address and key of the server you play on (discord = off, discord_join = off). The game's crash reports stay
on this PC. A server keeps accounts, match histories and logs with login names and addresses in
Dawngate-Data. The full list: docs/privacy.md in the project's repository.


Removing it
-----------
The programs install nothing into Windows. Close the game and Start-Server.exe, then delete this folder (with
Dawngate-Data; keep Dawngate.zip to play again later). The game also writes %APPDATA%\DawngateData,
Documents\Dawngate and, after a crash, %LOCALAPPDATA%\CrashRpt\UnsentCrashReports\Dawngate_1.0.903.854: delete
them too. With Discord's Join used:  reg delete HKCU\Software\Classes\discord-1561733580789514310 /f
Firewall answers Windows kept: Windows Security, Firewall & network protection, Allow an app through firewall.


Copyright (C) 2026 the OpenDawngateServer contributors. This project's code is licensed under the GNU AGPL v3
(LICENSE.txt); the source of this release is the project's repository at the commit in BUILD.txt. The game, its
files and its content belong to their owners and are not part of this release.
