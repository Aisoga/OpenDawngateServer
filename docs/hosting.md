# Hosting a server

This guide is for whoever runs the server that other people play on. It uses the release's `Start-Server.exe` on Windows, or `start-server` on Linux ([Linux server](#linux-server)). To only play on your own PC you don't need it: `Start-Client.exe` runs a server on your PC by itself (**This PC** in the login screen's server list; the quick start in the [README](../README.md#quick-start)).

- [What the server is](#what-the-server-is)
- [What you need](#what-you-need)
- [Setting it up](#setting-it-up)
- [Hosting on your home network](#hosting-on-your-home-network)
- [Hosting on the internet](#hosting-on-the-internet)
- [Accounts](#accounts)
- [The website and the admin page](#the-website-and-the-admin-page)
- [Announcements](#announcements)
- [Chat rooms](#chat-rooms)
- [Discord channel posts](#discord-channel-posts)
- [Backups](#backups)
- [Logs](#logs)
- [Security: what is protected and what isn't](#security-what-is-protected-and-what-isnt)
- [Linux server](#linux-server)

Every setting, command and port is listed in [reference.md](reference.md).

## What the server is

`Start-Server.exe` runs two kinds of programs:

- **The server**, inside `Start-Server.exe` itself. It handles logins, accounts, the menus, matchmaking, parties and chat, and the website.
- **One match host per running match.** A match host is a hidden copy of the game (from `Dawngate-Data\host`) with this project's `dgsim.dll`. It runs the match the way the original game server did. The server starts a host when a match begins and ends it afterwards.

A match host has no window, graphics or sound. It needs about 100 MB of memory and a few percent of one CPU core, and it doesn't need a graphics card. By default up to ten matches can run at once.

## What you need

- A Windows 10 or 11 PC, 64-bit, that stays on while people play. A Linux machine works too: see [Linux server](#linux-server).
- The release zip, and an internet connection for the first setup: `Start-Server.exe` downloads `Dawngate.zip` (1.3 GB) from <https://archive.org/details/Dawngate> itself. If you already have the zip, put it next to `Start-Server.exe` first.
- About 5 GB of free disk space (about 3 GB on a PC that only hosts, see [A server that only hosts](#a-server-that-only-hosts)).

## Setting it up

1. Unpack the release into a folder of its own. If you have `Dawngate.zip`, put it next to `Start-Server.exe` (or leave it in your Downloads folder).
2. Start `Start-Server.exe`. It asks who will play on the server:
   - **1, only this PC:** the server listens on `127.0.0.1` and nothing outside your PC can reach it.
   - **2, your home network:** it lists this PC's addresses in your network; pick the one your router connects to.
   - **3, the internet:** it asks the questions in [Hosting on the internet](#hosting-on-the-internet).
3. It writes your answers to `Start-Server.ini`, sets everything up in `Dawngate-Data` and starts the server. If it doesn't find `Dawngate.zip`, it first asks once whether to download it (see [Downloading the game](#downloading-the-game)). Leave the window open. Press Ctrl+C or close the window to stop the server and its running matches.

To change your answers later, run `Start-Server.exe --configure`, or edit `Start-Server.ini` and start the program again. The file explains each setting, and [reference.md](reference.md#start-serverini) lists them all.

Every start checks the setup and repairs anything missing, which usually takes a second. `Start-Server.exe --check` only reports what's missing.

You can play on the same PC that runs the server: just start `Start-Client.exe` there too. **This PC**, the first row of the login screen's server list, is this server while it runs in the same folder, and `Start-Client.exe` knows its key. When `Start-Server.exe` isn't running, This PC is a server that `Start-Client.exe` runs itself on the same folder, with the same accounts and `Start-Server.ini`'s game options (`shaper_select`, `halloween`, `map`, `custom_maps`, `signup`, `server_flags`; never the website or the public settings), only for the player on this PC. A folder has one server at a time: `Start-Server.exe` refuses to start while `Start-Client.exe` runs the folder's server ("This folder's server already runs inside Start-Client.exe"); close the game first.

**Every server uses the encrypted tunnel.** Players reach it on one port, **42128, TCP and UDP**; everything else the server runs answers inside the tunnel. Its match hosts listen on a loopback address of their own, so a server and a player's game run side by side on one PC. (`tunnel = off` in `Start-Server.ini` turns the tunnel off; that's an option for developers, and players then need `Start-Client.exe --tunnel off`.)

### Downloading the game

The project ships no game files. When neither `Start-Server.exe` nor `Start-Client.exe` finds `Dawngate.zip` (next to the program, in the folder above it or in your Downloads folder), it asks *Dawngate isn't set up yet. Download Dawngate.zip (1.3 GB) from archive.org? [Y/n]* and downloads it next to the program:

- It checks the free disk space first (the zip plus the game copies), shows the progress, speed and time left, and checks every file of the download against the original build 1.0.903.854 before using it.
- An interrupted download continues at the next start (from `Dawngate.zip.part`).
- **Mirrors:** `game_sources = <url>, <url>` in `Start-Server.ini` or `Start-Client.ini` lists other places to download the same zip from, tried in turn after archive.org. A source that gives another file is skipped (*... gave a file that isn't build 854's Dawngate.zip*) and never used, so any mirror is safe to list.
- **Scripts and services:** without a console window (or with `--non-interactive`) nothing is downloaded unless the command line names the sources: `Start-Server.exe --non-interactive --game-source <url>` downloads from those only, without asking.
- A `zip =` path in the `.ini` turns the download off: the program uses that file or asks for it.
- If archive.org is down, see [troubleshooting.md](troubleshooting.md).

### A server that only hosts

The server itself needs only one copy of the game, `Dawngate-Data\host`, for its match hosts (about 1.5 GB). With the tunnel, `Start-Server.exe` makes no copy for playing; `Start-Client.exe` makes its own the first time someone plays on this PC.

For a PC that only hosts, set `dedicated = yes` in `Start-Server.ini`, or start `Start-Server.exe --dedicated`:

- `Start-Server.exe` never makes or checks a copy for playing, also with `tunnel = off`. A copy that `Start-Client.exe` made in the same folder stays as it is.
- The window shows `mode dedicated server` at the start, and `--check` reports the copy for playing as skipped.
- Once `Dawngate-Data` holds `host`, `certs` and `data`, the server starts without `Dawngate.zip`. You need the zip again only if the host copy has to be repaired, or on a new PC.

To move a dedicated server to another PC, copy the release folder with `Start-Server.ini` and `Dawngate-Data` (the `client` folder can stay behind).

If someone also plays on a dedicated server's PC with `Start-Client.exe` from the same folder, **This PC** in their list is the dedicated server while it runs; `Start-Client.exe` makes its own copy for playing in `Dawngate-Data\client` then.

## Hosting on your home network

1. Choose **2** at the first question, or set `address = <this PC's LAN address>` in `Start-Server.ini`, for example `address = 192.168.1.20`.
2. The server window shows the server's **key fingerprint**, for example `3ff46759dcb1ea30`, and the whole key (64 hex digits). Give your players the address, and tell them the fingerprint or show them your window.
3. If Windows asks whether `Start-Server.exe` may use the network, allow **private networks** only.
4. Each player unpacks the release (its first start downloads their own `Dawngate.zip`) and starts `Start-Client.exe <address>` once (or adds the address on the login screen). Their first login stops with *New server key*, and their server list shows the key's fingerprint: they compare it with yours and log in again, which trusts it.

**Instead of comparing fingerprints**, you can hand out the whole key from your window. Players then start `Start-Client.exe <address> --gateway-pin <key>` once, and their first login goes straight through. Or put both into the `Start-Client.ini` you hand out next to `Start-Client.exe`:

```ini
server = 192.168.1.20
gateway_pin = <the whole key>
```

Its first start puts your server into the player's server list, preselects it and trusts the key. A player who already trusted another key for your server keeps that one: the key in the file is not taken over it. The key is public; never hand out any file from `Dawngate-Data\certs`.

If you change the server's address later, the server makes new certificates and a new key for it (in `Dawngate-Data\certs\<new address>`), so players add the new address and confirm its key once.

## Hosting on the internet

A public server is reachable by anyone. Run one only if you accept the risk described in [Security](#security-what-is-protected-and-what-isnt).

### What the public setup turns on

When the server's address is a public internet address, `Start-Server.exe` uses the **public profile**:

- **The encrypted tunnel**, as on every server: every player connection goes through one port (42128, TCP and UDP), encrypted with TLS 1.3 and AES-GCM, and the players' game only talks to `Start-Client.exe` on their own PC. A public server refuses `tunnel = off`.
- **Limits** on connections, logins, games and network traffic per address.
- **Checked passwords**, stored only as hashes and never written to a log, and a sign-up mode you choose.
- **A strict check** of everything a client sends in a match. This one is on for every server, not just public ones.

### Setting it up

Start `Start-Server.exe` and choose **3**, or run `Start-Server.exe --configure`. It asks for:

1. **Your internet address.** Your router shows it as its internet or WAN address. If this PC is behind a router, it also asks which of this PC's home-network addresses the router forwards to.
2. **Who may make accounts:**
   - `invite` (the default here): players register on the website with a code you make.
   - `closed`: only you create accounts.
   - `open`: anyone can make an account, at most 3 a day per internet address.
3. **A domain name for the website** (optional). If you have a name whose DNS record points at your internet address, the server gets a free certificate for it from Let's Encrypt by itself. Without one, the website uses its own certificate, and browsers warn about it once.
4. **An admin account** (optional) for the website's admin page. Its password is printed once.
5. **The risk notice.** The program prints what the setup protects and what remains your responsibility, and starts only after you accept (`i_accept_the_risk = yes` in `Start-Server.ini`).

### Router and firewall

On your router, forward only these ports to the server PC:

- **TCP and UDP 42128**: the tunnel.
- **TCP 443**: the website, and Let's Encrypt's check if you use a domain.

Forward nothing else. **Never forward TCP 10040**, the channel between the server and its match hosts, which has no login. `Start-Server.exe` never opens ports and never uses UPnP.

If the PC holds the public address itself, with no router in between (a rented server, for example), let the firewall in only TCP and UDP 42128 and TCP 443. Nothing else of the server listens on that address: the match hosts use a loopback address and the control channel `127.0.0.1`.

### Your server in the players' server lists

Players pick a server on the game's login screen, from a list that each player keeps on their own PC ([playing.md](playing.md#the-server-list)). Players add yours with `Start-Client.exe <your address>` or with **ADD SERVER** on the login screen. If your tunnel port isn't 42128, give them the address as `<address>:<port>`.

### The server's key

The server window shows the **key's fingerprint**, 16 hex digits, and the whole key, 64 hex digits. Publish the fingerprint where your players can check it, for example in your community's channel. A player's first login to a new server stops with *New server key* while their server list shows its fingerprint; once they have compared it, their next login trusts it. Every later login checks it. If you publish the whole key, players can start `Start-Client.exe <your address> --gateway-pin <key>` once instead, and nobody needs to compare.

The key is in `Dawngate-Data\certs\<address>\`. Keep it private, and keep it. If the key changes, for example because `Dawngate-Data\certs` was lost or made anew, every player's list shows **KEY CHANGED** with the old and the new fingerprint, and their logins are refused. Each player then has to confirm the new fingerprint with you and delete their old copy of the key ([troubleshooting.md](troubleshooting.md#logging-in)). That is what protects them from a stranger at your address, so tell them in advance if you ever change it.

### Hand out a ready package

With `client_package = on` in `Start-Server.ini`, your server hands out the game client ready for your server: the Windows release of the server's own version, with a `Start-Client.ini` next to `Start-Client.exe` that names your address (and your tunnel port, if it isn't 42128) and trusts your server's key. A player downloads it from your website's **Download** page (`https://<your website>/download`), unzips it into a folder of its own, starts `Start-Client.exe` and logs in: your server is preselected, and there is no key to compare. The page also shows your key's fingerprint and the three steps. The package never contains the game: `Start-Client.exe` sets the game up from `Dawngate.zip` as usual.

- The file is `OpenDawngate-<name>.zip`, where `<name>` is your `web_domain`, else your address. It is always the release of the version your server runs, so it fits your server; `Start-Server.exe` names the page when it starts.
- The server downloads that release from github.com once (from the project's releases page; the size and checksum GitHub gives and every file's checksum in the release's `BUILD.txt` are checked) and keeps the package in `Dawngate-Data\data\client-package\<version>\`. After an update of the server, the first start makes the package of the new version and deletes the old one. This download is the server's only outgoing connection besides the website's certificate, which is why the option is off by default.
- The programs in the package are the release's files byte for byte, so their signatures stay valid.
- A server you built yourself (not a numbered release) has no package; its Download page says so.
- The website must be on (`web_port`): with the tunnel, the website is the only place outside the tunnel that serves the package.

## Versions and updates

Every release has a version, its name, such as `OpenDawngateServer-20261016-905ef17f5f`. The server window shows it at the start and when the server runs (`Version …: players need Start-Client.exe of the same release`), and `Start-Server.exe --version` prints it. A player's `Start-Client.exe` logs in only on a server of exactly the same version: any difference refuses the login, and their server list shows your server as **other version**. A server of a release from before this check doesn't tell its version, and every current `Start-Client.exe` refuses it (**version unknown**).

Your server checks it too: each player's `Start-Client.exe` names its release when it connects, and the server lets in only its own release. A `Start-Client.exe` from before that check (or one changed to skip its own) can't name one and is refused; that player sees *The server you picked doesn't answer*, and your server's log says why (`session refused: its Start-Client names no release …` or `… is release X, this server is release Y …`). For tests with mixed builds, `server_flags = --allow-any-client` in `Start-Server.ini` lets every release in.

So when you update the server, tell your players which release it runs now, and publish it next to your key's fingerprint. To update:

1. Stop the server (Ctrl+C, or close its window). This ends running matches.
2. Unpack the new release into the same folder, replacing the old files. `Start-Server.ini` and `Dawngate-Data` (accounts, keys, the match hosts' copy) stay.
3. Start `Start-Server.exe`. The start checks the setup and brings the match hosts' copy up to date.

Players unpack the same release's `Start-Client.exe` into their folder the same way. Until they do, their logins to your server are refused, with a message that names both versions in their server list. **This PC**, the server in a player's own folder, is never refused.

### After updating from v0.8 or older

From v0.9 on, the Leaderboard counts only PvP and custom games, in counts of its own that start at 0 for every account: the older versions never stored a game's mode. Your server's log files did record it, so the server can count the older versions' PvP and custom games from them, once:

1. Stop the server.
2. Run the dry run. It changes nothing and prints, per log file, its version and whether it is read, and per account the games and wins it finds, per UTC day, next to the profile's totals (every game, practice included). An account marked `!` played more games than the logs show: games from before the oldest log (logs older than `log_days` are deleted) can't be told apart from practice games and aren't counted.

   ```
   Start-Server.exe --import-ranked-logs Dawngate-Data\logs
   ```

3. If the numbers look right, run it again with `--apply`. It writes everything at once, adds to the games played since the update, and notes in the database that it ran: a second `--apply` is refused (`--force` would count the same games twice).
4. Start the server.

Only log files written by v0.8 or older are read; a log of v0.9 or newer, whose games the server counted itself, is skipped. A developer build's log is read only when you name the file itself. On Linux, run it as the server's user: `sudo -u dawngate /srv/dawngate/start-server --import-ranked-logs /srv/dawngate/Dawngate-Data/logs` (and the same with `--apply`).

## Accounts

Players log in with a **login name** (3–32 of `A–Z a–z 0–9 . _ - @`, not case-sensitive) and a **password** (8–63 characters). The server stores only a hash of each password.

**Sign-up modes** (`signup` in `Start-Server.ini`):

| Mode | How accounts are made |
|---|---|
| `open` | The first login with a new name creates the account. This is the default for a home server. |
| `invite` | Players register on the website with an invite code you made. The default for a public server. |
| `closed` | Only you create accounts. |

Players see the mode in their server list (*open*, *invite only*, *closed*; a change on the admin page shows within seconds), and the login screen's *Sign up now!* opens your server's sign-up page before they ever log in. Under `invite` and `closed` an unknown login name and a wrong password get the same answer, which points to *Sign up now!*, so it doesn't tell which names exist.

**This PC's accounts.** When a player plays on *This PC* without your server running, `Start-Client.exe` runs a server of its own on the same folder's accounts, without passwords (only that PC reaches it; `local_trust = off` in `Start-Client.ini` turns passwords on). Accounts it makes have no password. Your server checks passwords: such an account takes the password of its first login from this PC (one of this PC's own addresses), and a login to it from anywhere else is refused, so nobody else can take it over.

**Account commands.** They work while the server is running. Each one prints its result and exits.

```
Start-Server.exe --create-account <name>               creates an account and prints its password once
Start-Server.exe --reset-password <name>               prints a new password (the server sends no e-mail)
Start-Server.exe --account-status <name>=disabled      also banned, deleted or active
Start-Server.exe --make-invite --invite-uses 5 --invite-days 14 --invite-note "the club"
Start-Server.exe --list-invites
Start-Server.exe --revoke-invite <id>
Start-Server.exe --make-admin <name>                   lets the account use the admin page
Start-Server.exe --remove-admin <name>
```

- A disabled, banned or deleted player can no longer log in. If they are playing, they are taken out of the game within a few seconds.
- An invite code is shown only once. The server keeps only its hash.
- Every change made by a command or on the admin page goes into an audit log, which the admin page shows.

## The website and the admin page

The server runs a small website at `https://<server address>/` (`web_port`, 443 by default). If another web server such as nginx already uses port 443 on the machine, it can serve the website instead: [The website behind nginx](#the-website-behind-nginx).

**Players** use it to:

- register (with an invite code under `invite`);
- change their password, download their data or delete their account. A deletion keeps match history, without the name.

`/status` shows how many players are online and how many matches are running, without names. The game's *Sign up now!* and *Forgot Password?* links lead to the website.

**The admin page**, `https://<server address>/admin`, is for accounts with the admin role (`--make-admin`). There you can:

- search accounts, and disable, ban, reset or delete them;
- make, list and revoke invite codes;
- change the sign-up mode while the server runs;
- see the running matches and end one;
- read the audit log.

The admin login is limited strictly against password guessing. Sessions end after 30 idle minutes.

**Your own pages:** put HTML fragments named `privacy.html`, `terms.html` and `about.html` into `Dawngate-Data\data\site`, and the website shows them. A public server should have a privacy page that says who runs it and what it keeps.

## Announcements

Write messages into `Dawngate-Data\data\broadcast.txt`, one per line. The server reads the file every 2 seconds and shows the messages to everyone who is logged in. By default they appear in the main menu's scrolling status line and in shaper select.

To choose where a message shows, put `@<number>` in front of the line. Add up the places you want:

- `4`: the main menu's status line and shaper select;
- `2`: a "System" line in the chat, even during a match;
- `1`: the chat's notification list.

Lines starting with `#` are comments. Delete the file, or empty it, to take the messages down.

```text
# shown in the main menu
Welcome to our Dawngate server
@6 Server restart at 20:00 UTC
```

After your messages the status line also shows how many players are online, for example "14 players online, 3 in a match" (only in the status line and in shaper select, never in the chat). A player counts from the login on; "in a match" counts the players of the running matches, from the loading screen to the end, and a custom game once it has started. When players log in or out, the number updates at most every 30 seconds, so a burst of logins makes one update. To turn it off, set `online_count = off` in `Start-Server.ini`; `online_count_every` sets how often it may update (for example `1m`).

## Chat rooms

Players find public chat rooms in the chat panel: the speech-bubble icon at the bottom right of the main menu opens the
room list. A double-click on a room (or right-click, *Join Chatroom*) joins it; its chat window then waits as a tab in
the bottom bar, where a click opens it and the X leaves the room. The window lists who is in the room, and every line
reaches everyone in it.

The server's rooms are set in `Start-Server.ini`:

```ini
chat_rooms = General, Looking for group, Matchmaking:queue:open
```

The rooms are separated by commas and listed in this order. A name may be followed by `:<capacity>` (2 to 1000
players, 50 if not given), `:queue` and `:open`, in any order. Into the `:queue` room (at most one) the server posts
the PvP queue's news, who is queuing, how many, when the match starts, and when it started or the queue closed, as
lines from *PvP queue*. Only players in the room see them. Every player joins an `:open` room by itself at each login:
its tab appears in the bottom bar, minimised, and the keyboard stays where it was. A player who closes it stays out
until the next login. So by default every player is in *Matchmaking* and sees the queue's news there; *General* and
*Looking for group* are joined by hand. This needs the game copies that Start-Client sets up (they carry the
`zz_Chat.snap` that does the joining); another copy lists an open room like any other. `chat_rooms = none` turns the
rooms off. Rooms have no passwords, nobody can create rooms in the game, and the rooms start empty with every server
start. A `Start-Server.ini` from before this setting keeps its own line (`…Matchmaking:queue`, no room opens); add
`:open` to the rooms you want opened.

## The Halloween decoration

The game ships a Halloween decoration for its map that it never showed: candles, pumpkins, jack-o'-lanterns and a tomb
around the map. To show it in every match on your server, set this in `Start-Server.ini` and start `Start-Server.exe`
again:

```
halloween = on
```

`off` (the default) shows the map as it shipped. The server tells each match whether to show it, so a player who
plays on several servers sees it only on the ones that have it on. It needs the game copies that Start-Client sets up
(they carry `zz_Halloween.snap`, the decoration's list); another copy shows the plain map. The decoration is scenery
only: it doesn't block movement or sight.

## The map

Every match on your server is played on The Dawngate (`SM_Map_v5`), the game's map. The game also holds a cut
one-lane map, `MM_Map_v3`, which can be played for testing, unfinished: its navigation, physics, ground and fog are
rebuilt, but its matches still follow The Dawngate's rules where the map has no rules of its own: each base has The
Dawngate's shop zone, shop keepers and healing, a match ends with The Dawngate's end screen (the victory view and the
loot bag), the bots have no jungler (the map has no camps) and all lane, and the map's super
minions don't spawn yet. Instead of spirit wells each team has one gold mine, on an island behind its base that no
shaper can reach: it starts with all 36 workers and pays every player of its team 36 gold a minute from the start,
and it can't be captured or raided. Every player's game and the match
hosts' copy need its files (`game\Data\zz_MM_Map_v3.snap`): `Start-Server.exe` and `Start-Client.exe` make them from
the game's own files at their first start (a few seconds). A match on a map the host copy lacks doesn't start
(the server's log warns about it at the start). Players need a release that makes these files, the one your server
runs: an older game without them would crash when it loads the map.

So your server lets only players it knows have the files into a match on `MM_Map_v3`: those whose `Start-Client.exe`
is your server's own release (it tells the server through the tunnel). Anyone else (an older `Start-Client.exe` let
in by `--allow-any-client`, or a game connected without the tunnel) can't join such a game from the browser, an
invite or a join code, and can't queue when `map = MM_Map_v3`: their game shows *Dawngate is unable to continue.
Please close the client and re-run the launcher. Error Code: 01310004* (as a notification, or a pop-up for the queue),
and the chat gets a *System* line with the reason. A practice or custom game such a player creates is played on
`SM_Map_v5` instead, with a *System* line saying so. The server's log names each refused player and why (`map
check`). Players connecting without the tunnel (`tunnel = off`, or on a LAN without `Start-Client.exe`) can't be
checked; if you know their games have the files, name them in `server_flags`: `--trust-maps all`, or their addresses
and networks (`--trust-maps 192.168.1.20,10.0.0.0/8`).

Two settings in `Start-Server.ini` choose the map:

```
map = SM_Map_v5
custom_maps = MM_Map_v3
```

- `map` is the map of every match: practice, the queue and custom games. Only `SM_Map_v5` and `MM_Map_v3` are
  accepted; another name stops the server at its start.
- `custom_maps` lists the maps a custom game's creator may pick, comma-separated. Players pick in the CREATE GAME
  dialog's map list (The Dawngate or MM), which adds the map's tag to the game's name: `~#mm` for `MM_Map_v3`, `~#sm`
  for `SM_Map_v5` (always allowed, as is `map`); typing the tag at the end of the name does the same. The game browser
  shows such a game as `[MM] <name>`: the game itself can only name The Dawngate in its Map column. The dialog lists
  the maps your server offers (`map` and `custom_maps`) to players whose game has their files: `Start-Client.exe`
  learns them from the server when the player logs in. Without `MM_Map_v3` in either setting players see
  *Map: The Dawngate* only. A pick of a map that isn't offered (a typed tag) is dropped, and that game is played
  on `map`.

## Discord channel posts

The server can post to channels of your Discord server through webhooks. Nothing is posted until you set a webhook address in `Start-Server.ini`.

**To make a webhook** (you need the *Manage Webhooks* permission on the Discord server): in Discord open *Server Settings*, *Integrations*, *Webhooks*, click *New Webhook*, give it a name (for example *Dawngate*) and choose the channel, then *Copy Webhook URL*. Paste the address into `Start-Server.ini` and start `Start-Server.exe` again:

```ini
discord_webhook_server = https://discord.com/api/webhooks/1234567890/AbCdEf...
```

- **Keep the address secret.** Whoever has it can post to the channel. The server never writes it into its logs; it names a webhook only by the last digits of its id (`webhook id …7890`). If it leaks, delete the webhook in Discord and make a new one.
**The server's status** goes to `discord_webhook_server` only:

- *Server online (version …)* when it has started;
- *Server going down (2 matches cut short)* when you stop it (Ctrl+C, closing its window, an update); the server waits up to 3 seconds for the post to go out;
- *Server back online (version …): it stopped unexpectedly around …* when the last run ended otherwise (a crash, a killed process, a power cut). The time is shown in each reader's own time zone and is accurate to about a minute.

**The queue message** goes to `discord_webhook_queue` (else `discord_webhook`):

- The first player into an empty PvP queue posts *Name is queuing for PvP — 1/10 · starts in 1:30, bots fill the open seats*. The countdown runs in each reader's Discord by itself.
- Joins and leaves edit that message (at most every 2 seconds); it ends as *PvP match started: 2 players + 8 bots* or *PvP queue closed*. The next player starts a new message. With `discord_queue_edit = off` every change is a new post instead.
- **The role ping:** set `discord_queue_ping` to a role's id (in Discord: *User Settings*, *Advanced*, *Developer Mode* on; then *Server Settings*, *Roles*, right-click the role, *Copy Role ID*). The role is mentioned on a queue's first post only, at most once per `discord_ping_cooldown` (15m), and only when at least `discord_ping_min_wait` (30s) is left before the match starts. Players who want the pings take the role.
- Custom and practice games never post here.

**The match results** go to `discord_webhook_result` (else `discord_webhook`): one post per finished match of the modes in `discord_result_modes` (`pvp,custom`; add `practice` for practice games): the winner, how and when it ended, and per team each player's shaper and kills/deaths/assists, plus how many bots played. With `discord_names = off` players show as *a player*. A match that was cut short without a winner posts nothing.

- **One webhook for everything, or one per kind:** `discord_webhook` serves the queue message and the match results; `discord_webhook_queue` and `discord_webhook_result` give either its own channel. The server's status posts only to `discord_webhook_server`. A public server might use two channels: `#queue`, and `#matches` for the results and the server's status.
- **No surprise pings:** no post pings `@everyone`, `@here` or a member, and a player name such as `@everyone` shows as plain text.
- Only addresses at `discord.com` (or the older `discordapp.com`) are accepted; anything else stops the server at its start with a message that names the key.
- The server on a player's own PC (**This PC** in `Start-Client.exe`) never posts, whatever its folder's `Start-Server.ini` says.

All keys are in [reference.md](reference.md#start-serverini).

## Backups

Accounts live in one database, `Dawngate-Data\data\accounts.db`, with `accounts.db-wal` and `accounts.db-shm` beside it.

- **To back up:** stop the server, then copy all three files, or the whole `Dawngate-Data\data` folder, to another disk. Do this regularly, for example daily.
- **To restore:** stop the server and copy them back.
- Also keep `Dawngate-Data\certs`. It holds your certificates and the tunnel key, and new ones would make every player confirm a new fingerprint.

## Logs

- The server's logs and one folder per match are in `Dawngate-Data\logs`.
- They contain login names and players' addresses, never passwords. Keep them private.
- `log_days` in `Start-Server.ini` (30 by default) deletes logs older than that.

## Security: what is protected and what isn't

**Protected on every server with the tunnel (the default):**

- Players' connections are encrypted end to end through the tunnel, and each player's `Start-Client.exe` checks the server's key.

**Also on a public server:**

- Passwords are hashed and never logged.
- Limits slow down floods and password guessing.
- Every message a client sends in a match is checked before the game logic sees it, and the network library is patched against known flaws.

**Your risk and your responsibility:**

- **Players trust your server.** The game is 2014 code without modern exploit protection (no ASLR). A server that is broken into could attack its players' games. Keep the server PC updated, don't use it for anything sensitive, and keep `Dawngate-Data\certs` private.
- **Shared addresses share limits.** Players behind one internet address share the limits. A household is fine, but a LAN party behind one address may be slowed down.
- **Personal data.** You keep login and display names, players' addresses in the logs, and match histories. Data-protection law, such as the GDPR in the EU, may apply to you. Publish a privacy page, keep `log_days` short and answer players' requests.
- **Backups** are up to you (see [Backups](#backups)).
- **The control port, TCP 10040, has no login.** Never forward it.

**With `tunnel = off`** (an option for developers), the game's own traffic isn't encrypted, including the password typed at login, and the server's plain ports listen on its address. Keep it to a network you trust.

## Linux server

The Linux release runs the same server on a 64-bit Linux machine, for example a rented server. Its match hosts run the game under Wine, so one Linux machine does everything; players still play with `Start-Client.exe` on Windows. Everything above applies, with `start-server` in place of `Start-Server.exe` and `/` in paths; this section covers what is different.

### What you need on Linux

- A 64-bit Linux machine with Wine and its 32-bit part. Debian 12 and Ubuntu 24.04 work with their own Wine packages.
- The release's Linux archive (`OpenDawngateServer-<version>-linux.tar.gz`). Its first start in a terminal downloads `Dawngate.zip` ([Downloading the game](#downloading-the-game)); if you have the zip already, copy it next to `start-server`.
- About 3 GB of free disk space, plus 1.3 GB while `Dawngate.zip` is there. Each running match needs about 120 MB of memory and a tenth of one CPU core.

Install Wine (Debian or Ubuntu):

```sh
sudo dpkg --add-architecture i386
sudo apt update
sudo apt install --no-install-recommends wine wine32 wine64
```

On other distributions, install Wine with its 32-bit (WoW64) support. The server needs no graphical desktop and no X server.

### Setting it up on Linux

1. Make a user for the server, and its folder:

   ```sh
   sudo useradd --system --create-home --home-dir /srv/dawngate --shell /usr/sbin/nologin dawngate
   ```

2. Unpack the archive into that folder, and give the folder to the user (copy `Dawngate.zip` next to `start-server` if you have it; otherwise the next step downloads it):

   ```sh
   sudo tar xzf OpenDawngateServer-<version>-linux.tar.gz -C /srv/dawngate --strip-components=1
   sudo cp Dawngate.zip /srv/dawngate/    # only if you have it
   sudo chown -R dawngate:dawngate /srv/dawngate
   ```

3. Start it once in a terminal:

   ```sh
   cd /srv/dawngate
   sudo -u dawngate ./start-server
   ```

   It asks who will play, as on Windows: **2** for your home network, **3** for the internet. It writes `Start-Server.ini`, checks Wine, makes the match hosts' Wine prefix (`Dawngate-Data/wine`, about half a minute), sets everything up in `Dawngate-Data` and starts the server. It shows the key fingerprint for your players. Press Ctrl+C to stop it.

4. Run it as a service from now on (below), so it starts with the machine.

If Wine or its 32-bit part is missing, `start-server` says what to install and starts nothing. `sudo -u dawngate ./start-server --check` reports what is missing without changing anything.

`Dawngate-Data` is readable by the `dawngate` user only: it holds the accounts, the server's keys and the logs.

### Running it as a service

The archive includes `dawngate.service`, a systemd unit for the folder `/srv/dawngate`:

```sh
sudo cp /srv/dawngate/dawngate.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now dawngate
```

- `systemctl status dawngate` shows whether it runs, and `journalctl -u dawngate -f` shows what the window would show, including the key fingerprint.
- `sudo systemctl stop dawngate` stops the server and ends its matches.
- The unit runs the server as `dawngate` and gives it the right to use port 443 for the website, without running as root (not needed when nginx serves the website).
- If your folder isn't `/srv/dawngate`, change the paths in the unit.

Account commands work while the service runs. Run them as the server's user, for example:

```sh
cd /srv/dawngate
sudo -u dawngate ./start-server --create-account alex
sudo -u dawngate ./start-server --make-invite --invite-uses 5
```

### Ports and the firewall on Linux

Players need one port, **TCP and UDP 42128**, and the website **TCP 443**. Nothing else of the server listens on the network: the control channel uses `127.0.0.1:10040`, and the match hosts use UDP 10042–10051 on `127.0.0.1`. With the website behind nginx ([below](#the-website-behind-nginx)), ports 80 and 443 are nginx's, and the server's website listens only on `127.0.0.1`.

Allow only those ports in the machine's firewall, and keep SSH open for yourself. With ufw:

```sh
sudo ufw default deny incoming
sudo ufw allow OpenSSH
sudo ufw allow 42128
sudo ufw allow 443/tcp
sudo ufw enable
```

Or with nftables, in `/etc/nftables.conf`:

```
table inet filter {
  chain input {
    type filter hook input priority 0; policy drop;
    ct state established,related accept
    iif "lo" accept
    ip protocol icmp accept
    ip6 nexthdr icmpv6 accept
    tcp dport 22 accept
    tcp dport { 443, 42128 } accept
    udp dport 42128 accept
  }
}
```

**Never open TCP 10040 or UDP 10042–10051.** The control channel has no login, and the matches are meant to be reached only through the tunnel. If your firewall lists them as open, close them.

With `tunnel = off` (an option for developers), the matches listen on every address of the server's network interface. Keep such a server on a network you trust.

### The website behind nginx

On a machine where nginx already serves other sites, nginx holds ports 80 and 443, and the server can't run its website there itself. Let nginx serve it: the server's website then listens only on `127.0.0.1`, over plain HTTP, and nginx answers `https://<your domain>/` with its own Let's Encrypt certificate and passes each request on. This works with any reverse proxy; the example is nginx with certbot, on Debian or Ubuntu.

You need a domain name whose DNS record points at the machine, for example `dawngate.example.org` (an A record, and an AAAA record if the machine has IPv6).

1. In `Start-Server.ini`, set:

   ```ini
   web_port = 8095
   web_domain = dawngate.example.org
   web_proxy = on
   ```

   `web_port` is any free port above 1023: only nginx connects to it, on `127.0.0.1`. `web_email` isn't used, because the certificate is nginx's.

2. Get the certificate with certbot's nginx plugin. It checks the domain over port 80 through your running nginx and renews the certificate by itself:

   ```sh
   sudo certbot certonly --nginx -d dawngate.example.org
   ```

3. Add a server block for the domain, for example `/etc/nginx/sites-available/dawngate`:

   ```nginx
   server {
       listen 80;
       listen [::]:80;
       server_name dawngate.example.org;
       return 301 https://$host$request_uri;
   }

   server {
       listen 443 ssl;
       listen [::]:443 ssl;
       server_name dawngate.example.org;

       ssl_certificate     /etc/letsencrypt/live/dawngate.example.org/fullchain.pem;
       ssl_certificate_key /etc/letsencrypt/live/dawngate.example.org/privkey.pem;
       include             /etc/letsencrypt/options-ssl-nginx.conf;
       ssl_dhparam         /etc/letsencrypt/ssl-dhparams.pem;

       location / {
           proxy_pass http://127.0.0.1:8095;
           proxy_set_header Host $host;
           proxy_set_header X-Real-IP $remote_addr;
           proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
           proxy_set_header X-Forwarded-Proto https;
       }
   }
   ```

   The port in `proxy_pass` is your `web_port`. The `include` and `ssl_dhparam` lines are certbot's recommended TLS settings, the same your other sites use; if the two files don't exist on your machine, leave those lines out.

4. Turn the site on, check the configuration and reload nginx:

   ```sh
   sudo ln -s /etc/nginx/sites-available/dawngate /etc/nginx/sites-enabled/
   sudo nginx -t
   sudo systemctl reload nginx
   ```

5. Restart the server (`sudo systemctl restart dawngate`). Its window, or `journalctl -u dawngate`, shows:

   ```
   The website: https://dawngate.example.org/ (behind your reverse proxy on 127.0.0.1:8095).
   ```

How this differs from the server's own website:

- The server makes no certificate and never contacts Let's Encrypt; certbot renews nginx's certificate.
- The server's website listens only on a loopback address. It refuses to listen anywhere else in this mode, so it is never reachable without nginx.
- The server takes each player's address from the headers nginx sets (the last entry of `X-Forwarded-For`, or `X-Real-IP`), and only from connections that come from the machine itself. The limits, the sign-up limit per address and the logs use that address. Keep the four `proxy_set_header` lines: without them every player would seem to come from `127.0.0.1`.
- The game itself doesn't go through nginx: players still need **TCP and UDP 42128** open ([Ports and the firewall on Linux](#ports-and-the-firewall-on-linux)). Ports 80 and 443 are nginx's, and are usually open already for your other sites.
- The game's *Sign up now!* and *Forgot Password?* links lead to `https://<your domain>/` as before.

If nginx answers `502 Bad Gateway`, the server isn't running, or `proxy_pass` names another port than `web_port`. If `start-server` says another program holds the website's port, choose another `web_port` and change `proxy_pass` to match.

### Updating on Linux

1. `sudo systemctl stop dawngate`
2. Unpack the new archive over the folder and give it to the user again:

   ```sh
   sudo tar xzf OpenDawngateServer-<new version>-linux.tar.gz -C /srv/dawngate --strip-components=1
   sudo chown -R dawngate:dawngate /srv/dawngate
   ```

3. `sudo systemctl start dawngate`. The start checks the setup and brings the match hosts' copy up to date.

`Start-Server.ini` and `Dawngate-Data` stay. Tell your players the new version, as on Windows ([Versions and updates](#versions-and-updates)).

### Removing a Linux server

```sh
sudo systemctl disable --now dawngate
sudo rm /etc/systemd/system/dawngate.service
sudo systemctl daemon-reload
sudo userdel --remove dawngate
```

`userdel --remove` deletes the user's home `/srv/dawngate` with everything in it (the programs, `Dawngate-Data` with the accounts and logs, the Wine prefix). Remove the firewall rules you added for ports 42128 and 443 (`sudo ufw delete allow 42128`, and so on), and the nginx site if you set one up. On Windows, see [Uninstalling](playing.md#uninstalling).

### Wine on Linux

- `start-server` uses `wine` from the `PATH`. For another Wine, for example WineHQ's packages in `/opt/wine-stable`, set `wine = /opt/wine-stable/bin/wine` in `Start-Server.ini`.
- The match hosts share one Wine prefix, `Dawngate-Data/wine`. If `WINEPREFIX` is set in the server's environment, that prefix is used instead. After a Wine upgrade, the next start checks the prefix again.
- Wine never downloads anything for the server. `start-server` turns off Wine's offer to fetch Mono and Gecko, which the match hosts don't need.

### In a container

`start-server` runs in a container too, with the same Wine packages installed. Two things matter:

- Start the container with host networking (`docker run --network host`). Behind Docker's port forwarding every player seems to come from one address, so the per-address limits and the sign-up limit would treat all players as one.
- Start it with `--init`, so that Ctrl+C and `docker stop` reach `start-server` and the processes Wine leaves behind are cleaned up.

Keep the server folder on the container's own file system or in a volume. Wine can't run the game from a folder shared from a Windows machine.
