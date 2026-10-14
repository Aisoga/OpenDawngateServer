# Reference

Settings, commands, ports and limits of the release's two programs. For explanations, see [hosting.md](hosting.md) and [playing.md](playing.md).

- [Start-Server.ini](#start-serverini)
- [Start-Server.exe options](#start-serverexe-options)
- [Start-Client.exe and Start-Client.ini](#start-clientexe-and-start-clientini)
- [Extra server options (server_flags)](#extra-server-options-server_flags)
- [The Dawngate-Data folder](#the-dawngate-data-folder)
- [Ports](#ports)
- [Limits](#limits)

## Start-Server.ini

`Start-Server.exe` writes this file next to itself on its first start. Use `key = value` lines; lines starting with `;` are comments. After editing, start `Start-Server.exe` again.

| Key | Default | Meaning |
|---|---|---|
| `address` | `127.0.0.1` | The address the server listens on and tells players. `127.0.0.1` means this PC only. Use a LAN address (for example `192.168.1.20`) for your home network, or your public internet address for a public server. |
| `bind` | empty | Only for a public server behind a router: this PC's LAN address, which the router forwards to. |
| `i_accept_the_risk` | `no` | A public address starts only with `yes` (or `--i-accept-the-risk`). |
| `profile` | `auto` | `auto` uses `public` for a public address and `home` otherwise. `public` requires the tunnel and turns on the limits. |
| `tunnel` | `auto` | The encrypted tunnel: `auto` turns it on for the public profile, `on` turns it on always, `off` never (not allowed with `public`). |
| `gateway_port` | `42128` | The tunnel's port, TCP and UDP. |
| `shaper_select` | `on` | `off` skips shaper select, and everyone plays Fenmore. |
| `signup` | `open` | Who can make accounts: `open`, `invite` or `closed`. See [Accounts](hosting.md#accounts). |
| `signups_per_address` | `3` | Under `open`: the most accounts one internet address may make per day. `0` means no limit. Home-network addresses never count. |
| `log_days` | `30` | Delete logs older than this many days. `0` keeps them. |
| `web_port` | `443` | The website's HTTPS port. `0` turns the website off. |
| `web_domain` | empty | Your domain name for the website, for a free Let's Encrypt certificate. Needs TCP 443 forwarded. |
| `web_email` | empty | An e-mail address for Let's Encrypt (optional). |
| `folder` | `Dawngate-Data` | Where the game copies, certificates, accounts and logs go: relative to the program's folder, or a full path. |
| `zip` | empty | Where `Dawngate.zip` is, if it's not next to the program or in your Downloads folder. Only needed for setup and repairs. |
| `server_flags` | empty | More server options, separated by spaces. See [below](#extra-server-options-server_flags). |

## Start-Server.exe options

| Option | Effect |
|---|---|
| `--configure` | Ask the first start's questions again. |
| `--check` | Only report what is missing; change nothing. |
| `--setup-only` | Set up or repair everything, then exit without starting the server. |
| `--zip <file>` | Use this `Dawngate.zip`. |
| `--config <file>` | Use another config file instead of `Start-Server.ini`. |
| `--i-accept-the-risk` | Accept the risk notice for a public address. |
| `--non-interactive` | Never ask anything (for scripts). |

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
Start-Client.exe [<server address>] [options]
```

| Option | Effect |
|---|---|
| `<server address>` or `--server <address>` | The server to play on. Without an address, the program uses the one in `Start-Client.ini`, or a server on this PC, or asks. |
| `--ca <file>` | Use the `ca_modulus.bin` the operator handed out instead of asking about the fingerprint. |
| `--tunnel auto\|on\|off` | The encrypted tunnel. `auto` (the default) uses it for public addresses. Use `on` for a home server whose operator set `tunnel = on`. |
| `--gateway-pin <64 hex digits>` | The server's tunnel key, if the operator handed it out (then there's no fingerprint question). |
| `--gateway-port <port>` | The server's tunnel port, if it's not 42128. |
| `--zip <file>` | Use this `Dawngate.zip` to set up or repair the game copy. |
| `--check` | Only report what is missing. |
| `--setup-only` | Prepare the game copy for the server without starting the game. |

`Start-Client.ini` remembers `server`, `folder`, `tunnel` and `gateway_port`.

## Extra server options (server_flags)

These go into `server_flags` in `Start-Server.ini`, separated by spaces, for example `server_flags = --mm-team-size 1 --mm-fill-after 5s`. They override the keys above.

| Option | Default | Effect |
|---|---|---|
| `--owned all\|starter` | `all` | What new accounts own: everything, or only the starter items. |
| `--ftue-required` | off | Bring back the original's first-time screen, where new accounts can only play practice. |
| `--free-rotation <names>` | none | Shapers everyone can pick in shaper select, comma-separated (for example `Fenmore`). |
| `--shaper-timers <ms>,<ms>,<ms>,<ms>` | the game's | The lengths of shaper select's four phases. |
| `--mm-team-size N` | `5` | Players per team in the queue (1 = 1v1 … 5 = 5v5). |
| `--mm-min-players N` | `1` | The fewest players a queue match starts with. |
| `--mm-fill-after <time>` | `15s` | How long the queue waits for full teams before starting a smaller match. |
| `--mm-max-wait <time>` | `0` (no limit) | The longest a player waits in the queue before being told it timed out. |
| `--mm-bots=false` | on | Leave the empty seats of a queue match empty instead of filling them with bots. |
| `--custom-bots` | off | Fill a custom game's empty seats with bots. |
| `--kill-switches <names>` | `WalletTopUp` | Client features to switch off, comma-separated: `Matchmaking`, `WalletTopUp` (the add-funds button), `EUGameServers`, `PracticeSoloGames`, `FTUESoloGames`, `PurchaseWithWaypoints`, `PracticeGroupGames`, `CustomGames`, `ForwardRenderer`. `none` switches nothing off. |
| `--broadcast <text>` | none | One announcement, in addition to `broadcast.txt`. |
| `--broadcast-flags N` | `4` | Where announcements show by default (see [Announcements](hosting.md#announcements)). |
| `--gms-ports <from>-<to>` | `10042-10051` | The match hosts' UDP ports, one per running match. |
| `--gms-warm N` | `0` | Keep N idle match hosts ready, so a match starts instantly. Each holds a port and about 90 MB of memory. |
| `--limits auto\|on\|off` | `auto` | The limits: `auto` turns them on for a public address. |
| `--status-check <time>` | `5s` | How often the server rechecks logged-in accounts, so that a disabled account is taken out quickly. |

## The Dawngate-Data folder

| Folder | Contents |
|---|---|
| `data\` | `accounts.db` (with `-wal` and `-shm`), `content.json` (what accounts can own, built from your game files), `broadcast.txt`, and `site\` (the website's certificates and your own pages). **Back this up.** |
| `certs\<address>\` | The server's certificates and the tunnel key. Keep private. Only `ca_modulus.bin` may be handed out. |
| `logs\` | The server's logs, and `hosts\<match>\` for each match. They contain login names and addresses. |
| `client\` | The game copy for playing, patched for your server. |
| `host\` | The game copy the match hosts run, with `dgsim.dll`. |

Never share the game copies: they are the game's files.

## Ports

| Port | Protocol | What | Who needs it |
|---|---|---|---|
| 42127 | TCP | Redirector (the client's first contact) | LAN players |
| 10041 | TCP | Logins, menus, matchmaking and chat | LAN players |
| 17502 | TCP and UDP | Connection-quality test | LAN players |
| 18080 | TCP | Web content and the login screen's links | LAN players |
| 9988 | TCP | Game telemetry (logged only) | LAN players |
| 10042–10051 | UDP | Matches, one port per running match | LAN players |
| 42128 | TCP and UDP | The encrypted tunnel; everything above travels inside it | Internet players: **forward this** |
| 443 | TCP | The website | Internet players and Let's Encrypt: **forward this** |
| 10040 | TCP | The server's control channel to its match hosts | **Nobody. Never forward it.** It stays on 127.0.0.1. |

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
