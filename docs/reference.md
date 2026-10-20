# Reference

Settings, commands, ports and limits of the release's two programs. For explanations, see [hosting.md](hosting.md) and [playing.md](playing.md). The Linux server's `start-server` takes the same settings and options as `Start-Server.exe`; what differs is in [Linux](#linux).

- [Start-Server.ini](#start-serverini)
- [Start-Server.exe options](#start-serverexe-options)
- [Start-Client.exe and Start-Client.ini](#start-clientexe-and-start-clientini)
- [Extra server options (server_flags)](#extra-server-options-server_flags)
- [The Dawngate-Data folder](#the-dawngate-data-folder)
- [Ports](#ports)
- [Linux](#linux)
- [Limits](#limits)

## Start-Server.ini

`Start-Server.exe` writes this file next to itself on its first start. Use `key = value` lines; lines starting with `;` are comments. After editing, start `Start-Server.exe` again.

| Key | Default | Meaning |
|---|---|---|
| `address` | `127.0.0.1` | The address the server listens on and tells players. `127.0.0.1` means this PC only. Use a LAN address (for example `192.168.1.20`) for your home network, or your public internet address for a public server. |
| `bind` | empty | Only for a public server behind a router: this PC's LAN address, which the router forwards to. |
| `i_accept_the_risk` | `no` | A public address starts only with `yes` (or `--i-accept-the-risk`). |
| `profile` | `auto` | `auto` uses `public` for a public address and `home` otherwise. `public` requires the tunnel and turns on the limits. |
| `tunnel` | `auto` | The encrypted tunnel: `auto` and `on` turn it on, for every address. Players then reach the server only on `gateway_port`. `off` is an option for developers: the server's plain ports instead, and players need `Start-Client.exe --tunnel off` (not allowed with `public`). A file from an earlier release keeps its value; `auto` now means on. |
| `gateway_port` | `42128` | The tunnel's port, TCP and UDP. |
| `shaper_select` | `on` | `off` skips shaper select, and everyone plays Fenmore. |
| `dedicated` | `no` | `yes` sets up and runs only the match hosts' copy `Dawngate-Data\host`, never a copy for playing, also with `tunnel = off`. A complete folder then starts without `Dawngate.zip`. See [A server that only hosts](hosting.md#a-server-that-only-hosts). |
| `signup` | `open` | Who can make accounts: `open`, `invite` or `closed`. See [Accounts](hosting.md#accounts). |
| `signups_per_address` | `3` | Under `open`: the most accounts one internet address may make per day. `0` means no limit. Home-network addresses never count. |
| `log_days` | `30` | Delete logs older than this many days. `0` keeps them. |
| `web_port` | `443` | The website's HTTPS port. `0` turns the website off. |
| `web_domain` | empty | Your domain name for the website, for a free Let's Encrypt certificate. Needs TCP 443 forwarded. |
| `web_email` | empty | An e-mail address for Let's Encrypt (optional). |
| `web_proxy` | `off` | `on` puts the website behind a reverse proxy such as nginx that already serves HTTPS on port 443: the website then listens on `127.0.0.1:<web_port>` over plain HTTP, for the proxy only, and has no certificate of its own. Needs `web_domain` and a `web_port` above 1023 (for example `8095`); `web_email` isn't used. See [The website behind nginx](hosting.md#the-website-behind-nginx). |
| `folder` | `Dawngate-Data` | Where the game copies, certificates, accounts and logs go: relative to the program's folder, or a full path. |
| `zip` | empty | Where `Dawngate.zip` is, if it's not next to the program or in your Downloads folder. Only needed for setup and repairs. |
| `server_flags` | empty | More server options, separated by spaces. See [below](#extra-server-options-server_flags). |
| `wine` | empty | Linux only: the Wine program that runs the match hosts, for example `/opt/wine-stable/bin/wine`. Empty: `wine` from the `PATH`. |

## Start-Server.exe options

| Option | Effect |
|---|---|
| `--configure` | Ask the first start's questions again. |
| `--check` | Only report what is missing; change nothing. |
| `--setup-only` | Set up or repair everything, then exit without starting the server. |
| `--dedicated` | The same as `dedicated = yes`: only the match hosts' copy. |
| `--zip <file>` | Use this `Dawngate.zip`. |
| `--config <file>` | Use another config file instead of `Start-Server.ini`. |
| `--i-accept-the-risk` | Accept the risk notice for a public address. |
| `--non-interactive` | Never ask anything (for scripts). |
| `--version` | Print the release's version and exit. Players need a `Start-Client.exe` of the same version. |

**Account commands** (they work while the server runs, print their result and exit):

| Command | Effect |
|---|---|
| `--create-account <name>` | Create an account and print its password once. |
| `--reset-password <name>` | Print a new password for the account. |
| `--account-status <name>=<state>` | `disabled`, `banned`, `deleted` or `active`. |
| `--make-invite [--invite-uses N] [--invite-days D] [--invite-note <text>]` | Print a new invite code once. |
| `--list-invites` | List the invite codes with their uses and expiry dates. |
| `--revoke-invite <id>` | Cancel an invite code. |
| `--make-admin <name>`, `--remove-admin <name>` | Give or take the admin role (for the website's admin page). |

## Start-Client.exe and Start-Client.ini

```
Start-Client.exe [<server address or tag>] [options]
```

| Option | Effect |
|---|---|
| `<server>` or `--server <server>` | The server to play on: an address, or the tag or name of a server in the server list. It is added to the list if it's missing, and preselected on the login screen. Without it the login screen preselects the server used last, This PC when none was used yet. |
| `--picker auto\|on\|off` | The server list on the login screen. `auto` (the default) and `on` show it whenever the tunnel is used, which is for every server unless `--tunnel off`. `off` never: the game plays on one server through its tunnel. |
| `--tunnel auto\|on\|off` | The encrypted tunnel. `auto` (the default) and `on` use it for every server. `off` (for a server with `tunnel = off`, an option for developers) plays on one server directly; a `Start-Server.ini` with `tunnel = off` in the same folder makes it the default. |
| `--gateway-pin <64 hex digits>` | The server's key, if the operator handed it out (the server's window shows it): it is trusted without the confirmation, and the server list checks it. |
| `--gateway-port <port>` | The server's tunnel port, if it's not 42128. |
| `--ca <file>` | With `--tunnel off`: the `ca_modulus.bin` the operator handed out, instead of asking about the fingerprint. |
| `--trust` | Trust a new key or certificate authority without asking (scripts; with `--picker off` or `--tunnel off`). |
| `--zip <file>` | Use this `Dawngate.zip` to set up or repair the game copy. |
| `--check` | Only report what is missing. |
| `--setup-only` | Prepare the game copy for the server without starting the game. |
| `--version` | Print the release's version and exit. It plays only on servers of the same version. |
| `--local-trust on\|off` | This PC without passwords: `on` (the default) lets any login name in on the server `Start-Client.exe` runs for This PC, with any password; `off` checks passwords there. |
| `--discord on\|off` | Your Discord status while the game runs: `on` (the default) or `off`. |
| `--discord-join on\|off` | Join from Discord: `on` (the default) or `off`. Needs the Discord status. |

`Start-Client.ini` has the keys `server` (empty: the server list), `folder`, `tunnel`, `gateway_port`, `picker` (`auto`, `on`, `off`), `local_trust` (`on`, `off`), `update_check` (`on`, `off`), `discord` (`on`, `off`), `discord_join` (`on`, `off`) and `discord_app_id`. An older `Start-Client.ini` keeps working: its `server` becomes the first server of a new list, and its comments are updated. The option on the command line wins over the key.

**Discord status.** While the game runs, `Start-Client.exe` shows your friends on Discord what you do in Dawngate, through the Discord app on the same PC (its local connection; nothing goes over the network, and nothing happens without the app). Your profile shows *Playing OpenDawngate* with the project's logo and the status below it, which shows only the state, the mode, a timer and your party's size, never a name or a server:

| You are | The status |
|---|---|
| at the login screen | *At the login screen* |
| in the main menu | *In the menu* |
| in a party, or in the PvP lobby | *In the lobby · PvP* in the menu, and *In a party (2 of 5)* below the line in the queue and the match too |
| in the queue | *In queue · PvP*, with the time in the queue |
| in shaper select | *Shaper select · PvP*, *· Practice vs bots*, *· Practice group* or *· Custom game* |
| in a custom game's lobby | *In the lobby · Custom game* |
| in a match | *In match · PvP*, with the time since the match started |
| after the match | *Post-game · PvP* |

With `--picker off` or `--tunnel off` (one server, without the server list) the status is only *In the game* with the time since the game started. It changes at most once every 4 seconds (Discord's limit), clears when the game ends, and comes back by itself when Discord starts after the game or restarts while it runs. `discord = off` (or `--discord off`) turns it off. `discord_app_id` is the Discord application the status is shown as (its numeric id); empty, the project's own (*OpenDawngate*).

**Join from Discord.** While you are in a party, a practice group's lobby or a custom game's lobby that has a free seat and no password, your Discord profile gets a *Join* button for your Discord friends, and the status shows the seats (*In a party (2 of 5)*, *Open to join (3 of 10)*). It goes away when the party or lobby is full, while your party is in the queue, when the match starts (a started custom game has no Join; friends can still watch it through the custom games list) and outside these screens. A friend who clicks *Join*:

- needs `Start-Client.exe` with the Discord app on the friend's own PC. If the game isn't running, Discord starts `Start-Client.exe` (it works after the friend started `Start-Client.exe` once, which registers it with Discord).
- gets the server added to the server list with its key (no fingerprint to confirm) and preselected, when it isn't in the list yet. Logged in on that server, the friend gets the game's usual invite pop-up in your name (the party's leader's, the lobby's owner's) right after the login (after the game's news panel, when it shows, is closed), and *YES* joins; when the game doesn't answer it, the invite comes again after half a minute (three times at most). Not logged in yet: the invite comes after the login there.
- logged in on another server: a line in the game's chat says to log out and log in on that server (it is selected in the list then).
- a refused join (the party is full or gone, the match started, the friend blocked you) is a line in the chat too.

Discord's own *Invite to join* in a channel or a direct message (the + button beside the message field) carries the same join, so anyone who sees that invite can use it while the party or lobby is open. The join tells the friend's `Start-Client.exe` the server's address and key and a code for the party; the code works only while somebody who was in the party when it was made is still in it, and the server never puts a player into a party by itself: the friend's *YES* does. The server on This PC that `Start-Client.exe` runs itself listens on this PC only, so it has no Join; a `Start-Server.exe` on the same folder that runs on a network address has. `discord_join = off` (or `--discord-join off`) turns Join off both ways: no *Join* button for others and none of your clicks taken; a server of an older version has no Join either.

To let Discord start the game, each start with Join on writes `HKEY_CURRENT_USER\Software\Classes\discord-<application id>` (for the built-in application `discord-1561733580789514310`): `URL Protocol`, `DefaultIcon` and `shell\open\command` = `"<the folder>\Start-Client.exe"`, only when the path changed. It is your own user's registry (no administrator); to remove it, delete that key with `regedit`, or run `reg delete HKCU\Software\Classes\discord-1561733580789514310 /f`. While a game runs, a second `Start-Client.exe` stops at once and changes nothing; a Join then goes to the running game.

**Updates.** A numbered release (`v0.6`, `v1.2.3`) asks once at each start for the project's latest release (one HTTPS request to `api.github.com`, at most 5 seconds; no answer: it starts as usual). When that one is newer (`v0.10` is newer than `v0.9`), it asks *Version v0.6 is available (you have v0.5). Update now? [Y/n]*; Enter or `y` updates:

1. The release's `<version>.zip` is downloaded into `Dawngate-Data\update\`, unpacked there, and every file is checked against the SHA-256 in its `BUILD.txt`; the new `Start-Client.exe` must say it is that version.
2. It refuses while the folder's `Start-Server.exe` runs (close it and start `Start-Client.exe` again).
3. The release's files replace the old ones (the programs, `README.txt`, the licences, `BUILD.txt`, `host\`); files only the old release had are removed. The running `Start-Client.exe` becomes `Start-Client.old.exe`, which the next start deletes. `Start-Client.ini`, `Start-Server.ini`, `Dawngate.zip` and `Dawngate-Data` are never touched. If a file can't be replaced, the files replaced so far are put back.
4. The new `Start-Client.exe` starts in the same window with the same options; its setup brings the game copy and the match-host copy up to date (they are made from the release's files).

It doesn't ask with `update_check = off`, `--check`, `--setup-only` or `--non-interactive`, without a console window, or in a build that isn't a numbered release (a nightly or a build from source). A failed update says why, keeps the old release and starts it; the downloaded zip stays in `Dawngate-Data\update\` when it was complete.

**This PC.** The server list's first entry, tag `pc`, is the server on this PC; it can't be removed. A login to it goes to the server of `Start-Server.exe` in the same folder while that runs (`Dawngate-Data\server.lock` says which program runs the folder's server, its address and its key). Otherwise `Start-Client.exe` runs the server itself, in its own process, until the game ends:

- It starts at the first login to This PC, not at `Start-Client.exe`'s start. Its first start sets up the match-host copy `host\` (about half a minute and 1.5 GB, from `Dawngate.zip`); the login waits up to 50 seconds for it (a longer setup goes on, and the login is refused with `00E90001` until it is done), and the list shows the progress.
- It listens on its own loopback address, `127.0.1.11` (`127.0.N.11` for a front on `127.0.0.N`): the tunnel's port 42128, the control port 10040 and its matches' UDP ports. Nothing else listens and no website runs.
- It uses the folder's accounts (`data\accounts.db`), the same certificate set as the folder's `Start-Server.exe` (`certs\<its address>`, else `certs\127.0.1.11`), and the game options of a `Start-Server.ini` in the same folder (`shaper_select`, `signup`, `signups_per_address`, `log_days`, `server_flags`); without one, shaper select is on and sign-up is open.
- It has no passwords while `local_trust` is `on` (the default): any login name logs in with any password, whatever the sign-up mode; a new name makes its account without a password, and none is checked or stored. The folder's `Start-Server.exe` checks passwords: an account made without one gets the password of its first login there from this PC (one of this PC's own addresses), and a login to it from anywhere else is refused.
- Its log goes to `Dawngate-Data\logs`, as `Start-Server.exe`'s; the console shows its setup and one line when it runs.
- A folder has one server at a time: `Start-Server.exe` refuses to start (or set up) while `Start-Client.exe` runs the folder's server.

**The server's key.** Each server's key is pinned in `Dawngate-Data\servers\<address>\gateway.pin`. A new key is confirmed once: the first login to that server is refused with `00E70001` while the list shows the key's fingerprint, and the next login to the same server, showing the same key, trusts it. No confirmation is needed for a key given with `--gateway-pin`, nor for This PC (its key is in the folder: `Dawngate-Data\certs`, or the running `Start-Server.exe`'s from `server.lock`). A key that differs from the pinned one is refused (`00E20001`).

### The server list

The servers on the login screen are kept in `Dawngate-Data\servers\bookmarks.json`, written by `Start-Client.exe` (and by the list's ADD SERVER and REMOVE at the next login):

```json
{
  "servers": [
    { "tag": "a", "name": "Alex's server", "address": "203.0.113.5", "gateway_port": 42128, "last_used": "2026-10-15T12:00:00Z" },
    { "tag": "b", "name": "play.example.org", "address": "play.example.org", "gateway_port": 42128 }
  ],
  "this_pc_last_used": "2026-10-15T13:00:00Z"
}
```

This PC isn't in `servers`: its address depends on which program runs it. `this_pc_last_used` is its last login, for the preselection.

| Field | Rule |
|---|---|
| `tag` | 1–16 of `a`–`z`, `0`–`9`, `_` and `-`, not starting with `-`; unique; not `pc` (This PC's; a server given it gets another). A server added without one gets the next free letter. |
| `name` | Shown in the list; the address when empty. |
| `address` | An IPv4 address or a host name; unique. |
| `gateway_port` | The server's tunnel port; 0 or missing means 42128. |
| `last_used` | When a login last went there (UTC). The login screen preselects the server used last. |

You may edit the file while the game isn't running. A file that doesn't follow these rules stops `Start-Client.exe` with its name: fix or delete it. Each server's trusted key is kept apart from the list, in `Dawngate-Data\servers\<address>\gateway.pin`, and stays when the server is removed from the list. `Start-Client.exe` writes the list into the game copy before each start, with each server's status (`game\Data\zz_ServerList.snap`, which the login screen reads; don't edit it).

The login name carries the pick ([playing.md](playing.md#the-server-list)):

| Login name | Effect |
|---|---|
| `<name>` | Log in on the server selected on the login screen, else the one used last. |
| `<name>#<tag>` | Log in on that server (a tag, address or name of the list). |
| `<name>#+<address>[:<port>][=<display name>]` | Add the server once it answers, then log in there. |
| `<name>#*<address>[:<port>][=<display name>]` | Add the server once it answers, without a login (the list's ADD; refused with `00EC0001` as its confirmation). |
| `<name>#-<tag>` | Remove the server from the list (the login is then refused with a confirmation). |
| `<name>#pc` | Log in on This PC. |

The refusals the list can show at LOGIN, besides the server's own:

| Code | Text |
|---|---|
| `00E00001` | That server isn't in your list. |
| `00E10001` | The server you picked doesn't answer. |
| `00E20001` | The server's key has changed since you last played, so the login was refused. |
| `00E30001` | Something answers at that address, but no Dawngate server with the tunnel on. |
| `00E40001` | The server you picked answered, but couldn't take the login. |
| `00E50001` | The server was removed from your list. |
| `00E60001` | That isn't a server address. |
| `00E70001` | New server key: check the fingerprint shown in the list, then log in again to trust it. |
| `00E80001` | This PC is always in the list: it can't be removed. |
| `00E90001` | The server on this PC is still being set up. Log in again when the list says online. |
| `00EA0001` | The server on this PC didn't start: Start-Client's window says why. |
| `00EB0001` | This server runs another version than your Start-Client, or doesn't tell its version: both need the same one. SERVER above the login shows both versions; restart Start-Client to update. |
| `00EC0001` | The server was added to your list. |

A server whose sign-up isn't open refuses an unknown login name and a wrong password alike with `000B0001`: *The login name or password you entered is incorrect. No account on this server yet? Make one first: Sign up now! below.* (An open server refuses a wrong password with `000C0001`, without the second sentence.)

**Sign-up and the login screen's links.** The list shows each server's sign-up mode (`open`, `invite only`, `closed`, `no password` for This PC without passwords), which the server answers inside its tunnel with its version. *Sign up now!* and *Forgot Password?* open the selected server's page through `Start-Client.exe` (`http://127.0.0.1:18080/dawngate/pick/<n>/signup` and `…/password`, where `<n>` is the server's place in the list for this run; the game's own links `/dawngate/signup` and `/dawngate/password` open a page that lists the servers). `Start-Client.exe` reaches a server for its page through a short tunnel session of its own, without a login; a key that isn't trusted yet is shown on the page with its fingerprint and trusted only when you confirm it there, and a changed key is refused. The server answers with its own page, or sends the browser on to its website. The start page and the legal links go to the server of your last login, else to the list.

**Versions.** Every release has a version, its name (`Start-Client.exe --version` and `Start-Server.exe --version` print it; both windows show it at the start). A login is refused with `00EB0001` before it reaches a server whose version differs from `Start-Client.exe`'s in any way, or that doesn't tell its version (a release from before this check); This PC never is. The server answers its version inside the tunnel, without opening a session, and the list asks it with each status check. A server without the tunnel answers it on its HTTP port (`/dawngate/version.txt`); `Start-Client.exe --tunnel off` asks it at its start and doesn't start the game on another version. The server checks the other side: `Start-Client.exe` names its release when it opens a tunnel session (and again when the session reconnects), and the server refuses a session of another release or of none (a `Start-Client.exe` from before this check, which then shows *The server you picked doesn't answer*, `00E10001`; the server's log names both). This PC's session names the folder's server's own release, so it is never refused. `--allow-any-client` (in `server_flags`) lets every release in, for tests with mixed builds. A build made from the source without the release script names its commit instead (`dev-<commit>`, `+dirty` with uncommitted changes).

## Extra server options (server_flags)

These go into `server_flags` in `Start-Server.ini`, separated by spaces, for example `server_flags = --mm-team-size 1 --mm-fill-after 5s`. They override the keys above.

| Option | Default | Effect |
|---|---|---|
| `--owned all\|starter` | `all` | What new accounts own: everything, or only the starter items (the always-owned items, a few shapers, a perk page, and the Spiritstones and Sparks of the filled starter page). |
| `--ftue-required` | off | Bring back the original's first-time screen, where new accounts can only play practice. |
| `--free-rotation <names>` | none | Shapers everyone can pick in shaper select, comma-separated (for example `Fenmore`). |
| `--shaper-timers <ms>,<ms>,<ms>,<ms>` | the game's | The lengths of shaper select's four phases. |
| `--mm-team-size N` | `5` | Players per team in the queue (1 = 1v1 … 5 = 5v5). |
| `--mm-min-players N` | `1` | The fewest players a queue match starts with. |
| `--mm-fill-after <time>` | `15s` | How long the queue waits for full teams before starting a smaller match. |
| `--mm-max-wait <time>` | `0` (no limit) | The longest a player waits in the queue before being told it timed out. |
| `--mm-bots=false` | on | Leave the empty seats of a queue match empty instead of filling them with bots. |
| `--custom-bots` | off | Fill a custom game's empty seats with bots. In shaper select a custom game's bots pick their shapers and roles after the players have chosen. |
| `--leaderboard-min-games N` | `50` | The fewest games a player needs to appear on the Leaderboard (the screen says 50). Every game counts, practice and custom games included. |
| `--kill-switches <names>` | `WalletTopUp` | Client features to switch off, comma-separated: `Matchmaking`, `WalletTopUp` (the add-funds button), `EUGameServers`, `PracticeSoloGames`, `FTUESoloGames`, `PurchaseWithWaypoints`, `PracticeGroupGames`, `CustomGames`, `ForwardRenderer`. `none` switches nothing off. |
| `--broadcast <text>` | none | One announcement, in addition to `broadcast.txt`. |
| `--broadcast-flags N` | `4` | Where announcements show by default (see [Announcements](hosting.md#announcements)). |
| `--gms-ports <from>-<to>` | `10042-10051` | The match hosts' UDP ports, one per running match. |
| `--gms-warm N` | `0` | Keep N idle match hosts ready, so a match starts instantly. Each holds a port and about 90 MB of memory. |
| `--gms-runner auto\|none\|<command>` | `auto` | What starts the match hosts' `Dawngate.exe`: `auto` starts it directly on Windows and through `wine` on other systems, `none` always directly, or a command such as `/opt/wine-stable/bin/wine`. |
| `--limits auto\|on\|off` | `auto` | The limits: `auto` turns them on for a public address. |
| `--status-check <time>` | `5s` | How often the server rechecks logged-in accounts, so that a disabled account is taken out quickly. |
| `--allow-any-client` | off | Let in a `Start-Client.exe` of any release, also one from before the release check (for tests with mixed builds). Without it only the server's own release logs in. |

## The Dawngate-Data folder

| Folder | Contents |
|---|---|
| `data\` | `accounts.db` (with `-wal` and `-shm`), `content.json` (what accounts can own, built from your game files), `broadcast.txt`, and `site\` (the website's certificates and your own pages). **Back this up.** |
| `certs\<address>\` | The server's certificates and the tunnel key. Keep private. Hand out only the key's 64 hex digits the server's window shows (and, with `tunnel = off`, `ca_modulus.bin`). |
| `certs\tunnel-127.0.0.1\` | `Start-Client.exe`'s own certificates for the tunnel on this PC. |
| `logs\` | The server's logs, and `hosts\<match>\` for each match. They contain login names and addresses. |
| `client\` | The game copy for playing, patched for the tunnel on this PC (with `--tunnel off`, for one server). `Start-Client.exe` makes it; `Start-Server.exe` only with `tunnel = off` and `dedicated = no`. |
| `servers\` | `Start-Client.exe`'s server list `bookmarks.json`, and per server `<address>\` the trusted tunnel key `gateway.pin` or the trusted certificate authority. |
| `host\` | The game copy the match hosts run, with `dgsim.dll`. `Start-Server.exe` makes it, or `Start-Client.exe` at the first login to This PC. |
| `server.lock` | Which program runs the folder's server (`Start-Server.exe`, or `Start-Client.exe` for This PC), while it runs. |
| `update\` | An update's download while it is installed; after a failed one, the release zip if it arrived whole. |

Never share the game copies: they are the game's files.

## Ports

With the tunnel (the default) the server listens for players on one port, on its address:

| Port | Protocol | What | Who needs it |
|---|---|---|---|
| 42128 | TCP and UDP | The encrypted tunnel: logins, menus, matchmaking, chat, the connection-quality test, web content, telemetry and the matches all travel inside it | Every player; from the internet: **forward this** |
| 443 | TCP | The website (with `web_proxy = on`: your reverse proxy, which forwards to `127.0.0.1:<web_port>`) | Internet players and Let's Encrypt: **forward this** |
| 10040 | TCP | The server's control channel to its match hosts | **Nobody. Never forward it.** It stays on 127.0.0.1 (on a server on a loopback address, that address). |
| 10042–10051 | UDP | Matches, one port per running match, on a loopback address of their own: `127.0.N.10` for a server on `127.0.0.N`, else `127.0.1.10` | Nobody: the tunnel reaches them inside the PC. |

Nothing else listens, so a `Start-Client.exe` on the same PC uses the game's own ports on `127.0.0.1` (TCP 42127, 10041, 17502, 18080 and 9988, UDP 17502 and 10042–10051) without a collision. The server `Start-Client.exe` runs for This PC listens on `127.0.1.11` only: TCP and UDP 42128, TCP 10040 and its matches' UDP 10042–10051.

With `tunnel = off` (an option for developers) the server listens on its address on TCP 42127 (the redirector, the client's first contact), 10041 (logins, menus, matchmaking and chat), 17502 (the connection-quality test, also UDP), 18080 (web content and the login screen's links) and 9988 (game telemetry), and the matches on UDP 10042–10051 there too.

On Linux the matches listen on `127.0.0.1` (see [Linux](#linux)).

## Linux

The Linux release has one program, `start-server`, with `host/` beside it. It is `Start-Server.exe` for Linux: the same `Start-Server.ini`, options, account commands and `Dawngate-Data` folder. Setting it up is described in [Linux server](hosting.md#linux-server). The differences:

| What | On Linux |
|---|---|
| Match hosts | Each runs the game under Wine, which needs its 32-bit part. The first start checks Wine and makes the hosts' Wine prefix, `Dawngate-Data/wine`, in about half a minute. If the environment sets `WINEPREFIX`, that prefix is used instead. Without Wine, `start-server` says what to install and starts nothing. |
| Matches' ports | UDP 10042–10051 on `127.0.0.1`, whatever the address. Under Wine, `127.0.0.1` is the one loopback address a match host listens on exactly. |
| Copy for playing | None. The game runs on Windows, so a Linux server is always a dedicated server: `dedicated` is always `yes`. |
| `Dawngate-Data` | Readable by the server's user only. |
| Stopping | Ctrl+C, or SIGTERM (`systemctl stop`, `docker stop`). Either ends the running matches and their hosts. |
| `tunnel = off` | The matches listen on the server's address. Under Wine, that means every address of its network interface, so `address` must be the interface's main address. |

`start-server` needs about 3 GB of disk space: the match hosts' copy (1.5 GB), the Wine prefix (1.3 GB) and the logs; `Dawngate.zip` (1.3 GB) is needed again only for repairs. Each running match needs about 120 MB of memory and 10 % of one CPU core.

## Limits

These limits apply on public servers, or with `--limits on`.

| What | Limit |
|---|---|
| New connections per address | 1 a second, 30 at once |
| Open connections per address | 24 |
| Tunnel sessions per address | 16 |
| Tunnel traffic per address | 400 datagrams a second (bursts of 1,200), 256 KB/s |
| Login attempts per address | 10 a minute |
| Failed logins | per account: 5 free, then a wait from 30 seconds doubling up to 15 minutes; per address: 20 free, then 1 minute doubling up to 30 minutes |
| Games a player starts | 3 a minute, at most 2 running at once |
| Queue joins per player | 6 a minute |
| Match traffic per connection | 200 game messages a second (bursts of 600); a connection that keeps exceeding it is closed |

A player who hits a limit sees a *LOGIN ERROR* or "no game server" message and can try again shortly. A household fits within the limits easily. A LAN party of ten players behind one internet address may not.
