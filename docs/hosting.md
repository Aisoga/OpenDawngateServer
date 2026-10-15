# Hosting a server

This guide is for whoever runs the server that other people play on. It uses the release's `Start-Server.exe`. To only play on your own PC, the quick start in the [README](../README.md#quick-start) is enough.

- [What the server is](#what-the-server-is)
- [What you need](#what-you-need)
- [Setting it up](#setting-it-up)
- [Hosting on your home network](#hosting-on-your-home-network)
- [Hosting on the internet](#hosting-on-the-internet)
- [Accounts](#accounts)
- [The website and the admin page](#the-website-and-the-admin-page)
- [Announcements](#announcements)
- [Backups](#backups)
- [Logs](#logs)
- [Security: what is protected and what isn't](#security-what-is-protected-and-what-isnt)

Every setting, command and port is listed in [reference.md](reference.md).

## What the server is

`Start-Server.exe` runs two kinds of programs:

- **The server**, inside `Start-Server.exe` itself. It handles logins, accounts, the menus, matchmaking, parties and chat, and the website.
- **One match host per running match.** A match host is a hidden copy of the game (from `Dawngate-Data\host`) with this project's `dgsim.dll`. It runs the match the way the original game server did. The server starts a host when a match begins and ends it afterwards.

A match host has no window, graphics or sound. It needs about 100 MB of memory and a few percent of one CPU core, and it doesn't need a graphics card. By default up to ten matches can run at once.

## What you need

- A Windows 10 or 11 PC, 64-bit, that stays on while people play.
- `Dawngate.zip` from <https://archive.org/details/Dawngate> and the release zip.
- About 5 GB of free disk space.

## Setting it up

1. Unpack the release into a folder of its own and put `Dawngate.zip` next to `Start-Server.exe`.
2. Start `Start-Server.exe`. It asks who will play on the server:
   - **1, only this PC:** the server listens on `127.0.0.1` and nothing outside your PC can reach it.
   - **2, your home network:** it lists this PC's addresses in your network; pick the one your router connects to.
   - **3, the internet:** it asks the questions in [Hosting on the internet](#hosting-on-the-internet).
3. It writes your answers to `Start-Server.ini`, sets everything up in `Dawngate-Data` and starts the server. Leave the window open. Press Ctrl+C or close the window to stop the server and its running matches.

To change your answers later, run `Start-Server.exe --configure`, or edit `Start-Server.ini` and start the program again. The file explains each setting, and [reference.md](reference.md#start-serverini) lists them all.

Every start checks the setup and repairs anything missing, which usually takes a second. `Start-Server.exe --check` only reports what's missing.

You can play on the same PC that runs the server: just start `Start-Client.exe` there too.

## Hosting on your home network

1. Choose **2** at the first question, or set `address = <this PC's LAN address>` in `Start-Server.ini`, for example `address = 192.168.1.20`.
2. The server window shows the server's **fingerprint**, for example `3ff46759dcb1ea30`. Give your players the address, and tell them the fingerprint or show them your window.
3. If Windows asks whether `Start-Server.exe` or the game copy in `Dawngate-Data\host` may use the network, allow **private networks** only.
4. Each player unpacks the release with their own `Dawngate.zip` and starts `Start-Client.exe <address>`. The first time, they compare the fingerprint and answer `y`. A home server without the tunnel isn't in the players' server lists on the login screen: `Start-Client.exe` plays on it directly. With `tunnel = on` in `Start-Server.ini` it is, and players start `Start-Client.exe <address> --tunnel on` once.

**Instead of comparing fingerprints**, you can hand out the file `Dawngate-Data\certs\<address>\ca_modulus.bin`. It's public and contains no key. Players then start `Start-Client.exe <address> --ca ca_modulus.bin`. Never hand out any other file from `certs`.

If you change the server's address later, the server makes new certificates for it, and every player confirms the new fingerprint once.

## Hosting on the internet

A public server is reachable by anyone. Run one only if you accept the risk described in [Security](#security-what-is-protected-and-what-isnt).

### What the public setup turns on

When the server's address is a public internet address, `Start-Server.exe` uses the **public profile**:

- **An encrypted tunnel.** Every player connection goes through one port (42128, TCP and UDP), encrypted with TLS 1.3 and AES-GCM. `Start-Client.exe` uses the tunnel by itself for a public address, and the players' game only talks to `Start-Client.exe` on their own PC.
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

If the PC holds the public address itself, with no router in between (a rented server, for example), its other ports listen on that address too. In that case, let the firewall in only TCP and UDP 42128 and TCP 443.

### Your server in the players' server lists

Players pick a server on the game's login screen, from a list that each player keeps on their own PC ([playing.md](playing.md#the-server-list)). A server appears in that list only with its tunnel on, which a public server always has. Players add yours with `Start-Client.exe <your address>` or with **ADD SERVER** on the login screen. If your tunnel port isn't 42128, give them the address as `<address>:<port>`.

### The tunnel's fingerprint

The server window shows the **tunnel key's fingerprint**, 16 hex digits. Publish it where your players can check it, for example in your community's channel. A player's server list shows the key of a new server, and its first login there trusts it. Every later login checks it.

The key is in `Dawngate-Data\certs\<address>\`. Keep it private, and keep it. If the key changes, for example because `Dawngate-Data\certs` was lost or made anew, every player's list shows **KEY CHANGED** with the old and the new fingerprint, and their logins are refused. Each player then has to confirm the new fingerprint with you and delete their old copy of the key ([troubleshooting.md](troubleshooting.md#logging-in)). That is what protects them from a stranger at your address, so tell them in advance if you ever change it.

## Accounts

Players log in with a **login name** (3–32 of `A–Z a–z 0–9 . _ - @`, not case-sensitive) and a **password** (8–63 characters). The server stores only a hash of each password.

**Sign-up modes** (`signup` in `Start-Server.ini`):

| Mode | How accounts are made |
|---|---|
| `open` | The first login with a new name creates the account. This is the default for a home server. |
| `invite` | Players register on the website with an invite code you made. The default for a public server. |
| `closed` | Only you create accounts. |

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

The server runs a small website at `https://<server address>/` (`web_port`, 443 by default).

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

**Protected on a public server:**

- Players' connections are encrypted end to end through the tunnel, and each player's `Start-Client.exe` checks the server's key.
- Passwords are hashed and never logged.
- Limits slow down floods and password guessing.
- Every message a client sends in a match is checked before the game logic sees it, and the network library is patched against known flaws.

**Your risk and your responsibility:**

- **Players trust your server.** The game is 2014 code without modern exploit protection (no ASLR). A server that is broken into could attack its players' games. Keep the server PC updated, don't use it for anything sensitive, and keep `Dawngate-Data\certs` private.
- **Shared addresses share limits.** Players behind one internet address share the limits. A household is fine, but a LAN party behind one address may be slowed down.
- **Personal data.** You keep login and display names, players' addresses in the logs, and match histories. Data-protection law, such as the GDPR in the EU, may apply to you. Publish a privacy page, keep `log_days` short and answer players' requests.
- **Backups** are up to you (see [Backups](#backups)).
- **The control port, TCP 10040, has no login.** Never forward it.

**On a home network without the tunnel**, the game's own traffic isn't encrypted, including the password typed at login. That's fine on a network you trust. If you want encryption there too, set `tunnel = on`; your players then start `Start-Client.exe <address> --tunnel on` once, and pick your server from their list on the login screen.
