# Troubleshooting

- [Starting up](#starting-up)
- [Logging in](#logging-in)
- [Starting a match](#starting-a-match)
- [During a match](#during-a-match)
- [Playing over a network](#playing-over-a-network)
- [Reporting a problem](#reporting-a-problem)

## Starting up

**"is not the Dawngate.zip of archive.org"**
The setup checks every file against build 1.0.903.854. Download the zip again from <https://archive.org/details/Dawngate>; the download may be incomplete.

**The download of Dawngate.zip fails, or archive.org is down**
The window names each source and why it failed. Start the program again later: an interrupted download continues where it stopped (from `Dawngate.zip.part` next to the program). To use another source, list a mirror of the same zip in `game_sources = <url>` in `Start-Client.ini` or `Start-Server.ini` (tried after archive.org), or download the zip yourself from anywhere and put it next to the program: it is used without asking. Every download is checked against the original build, so a mirror with another file is skipped (*... gave a file that isn't build 854's Dawngate.zip*) and never used.

**"not enough disk space" before the download**
The download wants room for the zip and the game copies made from it (about 5 GB in all). Free some space, or put the release on another drive.

**Start-Server or Start-Client can't find Dawngate.zip**
Started in a console window, the program offers to download it. Otherwise put the zip next to the program, or in your Downloads folder. You can also drag it onto the program's window when it asks, or set `zip = <path>` in `Start-Server.ini` or `Start-Client.ini`. The server on This PC sets itself up while the game runs and asks nothing then: if its `Dawngate.zip` isn't found, its login is refused (*The server on this PC didn't start*) and `Start-Client`'s window names the places it looked. Put the zip there and log in again.

**The setup stopped halfway**
Start the program again: it continues where it stopped. If the same step fails again, delete the folder the message names inside `Dawngate-Data`, and it will be made anew.

**"Another program holds the server's ports"**
A server is already running, probably another `Start-Server` window. Close it first. If no other server is running, some other program uses one of the [server's ports](reference.md#ports).

**"This folder's server already runs inside Start-Client.exe"**
`Start-Client.exe` runs the server of this folder for This PC, and a folder has one server at a time. Close the game (its server stops with it), then start `Start-Server.exe` again. To run both at once, give `Start-Server.exe` a folder of its own (`folder =` in `Start-Server.ini`).

**This PC says "didn't start", or "The server on this PC didn't start"**
The server on your PC couldn't be set up or started. `Start-Client`'s window shows why: usually `Dawngate.zip` wasn't found, the disk is full, or the release's `host` folder is missing (unpack the whole release zip). Fix that and log in again: each login tries again.

**"The server on this PC is still being set up"**
Its first setup took longer than the login waits (50 seconds), on a slow disk for example. It goes on: wait until the server list says *online*, then log in again.

**The game closes right away**
Only one Dawngate client can run at a time. Close the other one, including any that's still open from an earlier session.

**"The update to v… failed"**
`Start-Client.exe` keeps the release you have and starts it; the message says why (no connection, a damaged download, a file in use). To update by hand, close every Dawngate program of the folder, unpack the release zip (the one the message names in `Dawngate-Data\update\`, or one downloaded from the project's releases page) over your folder and replace the files it asks about; your `Start-*.ini`, `Dawngate.zip` and `Dawngate-Data` aren't in the zip and stay. The next start brings the game copies up to date.

**"Start-Server.exe runs from this folder: close it … to update"**
An update replaces `Start-Server.exe` too, which Windows doesn't allow while it runs. Close the `Start-Server` window, then start `Start-Client.exe` again.

**`Start-Client.old.exe` in the folder**
The previous version, left by an update. The next start of `Start-Client.exe` deletes it (it can't while it still runs).

**SmartScreen or antivirus warnings**
The programs aren't signed. Click *More info*, then *Run anyway*.

## Logging in

**"The login name or password you entered is incorrect"**
Check the password. On a server with open sign-up, a new login name creates a new account, so a typo in the name logs you into a different, new account.

**"… No account on this server yet? Make one first: Sign up now! below."**
The server needs an account before the first login (its row in the server list says **invite only** or **closed**), and it doesn't know this login name, or the password is wrong (the server doesn't say which). Check both; if you have no account there yet, select the server and click **Sign up now!** below the login. An **invite only** server's page takes an invite code from its operator; on a **closed** server ask the operator for an account.

**Sign up now! opens a list of servers instead of the server's page**
The link of a server you have just added, before the list knows it, or of a game copy without the server list, goes to that list: click the server's *open its page*. The pages come from `Start-Client.exe` and work only while the game runs.

**The sign-up page says "New server key"**
You haven't logged in to that server yet, so its key isn't trusted. Compare the fingerprint on the page with the one the server's operator gives you; only if they match, confirm. That trusts the key for your logins as well. "KEY CHANGED" on the page means the same as in the server list (below): don't continue until the operator confirms the new key.

**This PC takes any password**
That is how the server `Start-Client.exe` runs for This PC works: only your PC reaches it, so it checks no password (`local_trust = on` in `Start-Client.ini`, the default; `off` makes it check them). While `Start-Server.exe` runs in the same folder, This PC is that server, and it checks passwords: the first login there from this PC gives an account you made without a password its password.

**Forgot your password**
The server's operator can print a new one with `Start-Server.exe --reset-password <name>`, or reset it on the admin page. If the server is your own, you are the operator.

**"Your account is disabled" or "banned"**
Contact the server's operator.

**A LOGIN ERROR with only a number**
Too many login attempts from your address in a short time. Wait a minute or two and try again.

**"New server key: check the fingerprint shown in the list, then log in again to trust it"**
That's your first login to this server: its key isn't trusted yet. Click OK, open the server list (SERVER above the login panel) and compare the fingerprint in the sentence under the list with the one the server's operator gives you (their server window shows it). Only if they match, press LOGIN again: that login trusts the key. If the operator handed you the whole key, `Start-Client.exe <address> --gateway-pin <key>` trusts it without this step.

**"The server's key has changed since you last played", or KEY CHANGED in the server list**
Every server has a tunnel key, and `Start-Client.exe` remembers the key of each server you have logged in to. A different key means that the operator made new keys or moved the server, or that someone else answers at its address. Open the server list on the login screen: it shows the key you trusted and the one the server shows now. Ask the operator which fingerprint their server window shows. Only if it matches the new one, delete the file `Dawngate-Data\servers\<server address>\gateway.pin` and log in again. That login trusts the new key. If it doesn't match, don't log in to that server.

**"The server you picked doesn't answer", or offline in the server list**
The server is offline, or its address in your list is wrong. The list checks every server again every few seconds while the login screen is open, so the server turns green by itself when it is back. If it stays offline, check the address with its operator. An operator of a server on the internet forwards TCP and UDP 42128 to it. If the list shows the server online and only the login says this, your `Start-Client.exe` is older than the server's release: the server refuses a `Start-Client.exe` that can't name its release (its log says so). Get the release the server runs, as for *another version* below.

**"Something answers at that address, but no Dawngate server with the tunnel on"**
The server runs without its tunnel (`tunnel = off`, an option for developers), or it is an older release. Ask the operator to set `tunnel = auto` in `Start-Server.ini` and to use a current release; or play on it with `Start-Client.exe <address> --tunnel off`.

**"This server runs another version than your Start-Client, or doesn't tell its version", or other version / version unknown in the server list**
A server and your `Start-Client.exe` must be the same release; any difference refuses the login, by `Start-Client.exe` and by the server. Open the server list on the login screen: the sentence under it names the server's version and yours (`Start-Client.exe --version` prints yours). Ask the server's operator which release the server runs and use that release's `Start-Client.exe`; unpacking it into your folder over the old files keeps your game copy, server list and trusted keys. *version unknown* means the server runs a release from before this check: only its operator can fix that, by updating the server. If you run the server yourself, update `Start-Server.exe` and `Start-Client.exe` together (they come in the same release). This PC, the server in your own folder, is never refused.

**"That server isn't in your list"**
The login name had a `#` with a server that isn't in your list. Type only your login name and pick the server with SERVER above the login panel.

**"The server you picked answered, but couldn't take the login"**
The server's tunnel works, but its login service didn't answer in time, for example while it is starting. Try again in a moment.

**"That isn't a server address"**
The address typed in ADD SERVER (or after `#+` in the login name) is no IPv4 address or host name. Type it as `203.0.113.5` or `play.example.org`, with `:<port>` only if the operator's tunnel port isn't 42128.

**"The server was removed from your list"**
That is REMOVE's confirmation, not an error. Click OK and pick another server.

**"The server was added to your list"**
That is ADD's confirmation, not an error. Click OK; the list shows the new server within a few seconds.

**"This PC is always in the list: it can't be removed"**
A login typed as `<name>#-pc`. This PC is the server on your own PC and stays the first row of the list.

**There is no SERVER above the login panel**
The login screen shows the server list whenever the tunnel is used, which is the default. Without it (`--tunnel off` or `tunnel = off` in `Start-Client.ini`, or a `Start-Server.ini` with `tunnel = off` in the same folder) the game plays on one server directly, and `Start-Client.exe`'s window says so (*picker not used*). `picker = off` turns the list off too.

**Shift+Tab or Escape on the login screen does something else**
The server list is used with the mouse. Shift+Tab from the login name goes to the game's EXIT button, and Enter there asks whether to quit the game. Escape doesn't close the list; click DONE.

**Start-Client.exe says "the server ... runs version ..." and doesn't start the game**
You play without the tunnel (`--tunnel off`), and the server runs another release, or one that doesn't tell its version. Use the `Start-Client.exe` of the server's release, as above.

**The login screen can't connect, or stays at "Initializing"**
The game copy and the server don't match. This happens, for example, when the server's address or certificates changed. Start `Start-Client.exe` again: it checks the copy and patches it for the server.

## Starting a match

**Nothing happens after PRACTICE DAWNGATE, and the envelope at the bottom right shows "1"**
The server couldn't start a match. Click the envelope, then the notification: it says "Game creation failed - no servers available". Check the server window and its log in `Dawngate-Data\logs`. Ten matches at once is the default maximum.

**"Failed to connect to game server" about 20 seconds after the click**
The match host ended before it was ready. Its log is in `Dawngate-Data\logs\hosts\<match>\dgsim.log`.

## During a match

**The world freezes: nothing moves, and you can't attack or cast**
The server notices a stuck match after about 10 seconds and ends it, and you are back in the main menu. Start a new match. The match's folder under `Dawngate-Data\logs\hosts\` then has `watchdog.txt` and `watchdog.dmp`. Please include them in a report.

**You were logged out in the middle of a match**
The game logs you out after five minutes without any input. Log in again.

**The game crashed**
A crash window may appear. Its report stays on your PC and is never sent. It's under `%LOCALAPPDATA%\CrashRpt\UnsentCrashReports\` and includes screenshots of your screens; delete it if you like. Start the game again, log in and click **Rejoin** to return to the match.

**The window's close button does nothing**
In a match the game ignores it. Leave the match from the gear menu above the minimap, then close the game.

**Messages in the game's log**
`%APPDATA%\DawngateData\Application.log` contains many asserts. Most are harmless and come from the game itself, for example `GameSimulator.cpp` 7412 in every match.

## Playing over a network

**Other PCs can't reach your server**

- `address` in `Start-Server.ini` must be the server PC's LAN address, not `127.0.0.1`.
- When Windows asks whether `Start-Server.exe` may use the network, allow private networks. If you cancelled the question, allow it in the Windows Firewall settings: players need TCP and UDP 42128.

**"The server ... has another certificate authority" or another tunnel key**
With `--tunnel off` or `--picker off`, `Start-Client.exe` asks in its window when a server's fingerprint changed. That happens when its operator changed the address or made new keys. Ask the operator which fingerprint their window shows, and answer `y` only if it matches. For a server in the login screen's list, see *The server's key has changed* under [Logging in](#logging-in).

**"gateway unavailable"**
The server's tunnel refused the connection, usually because too many sessions come from your address. Close other game sessions and try again.

**A public server isn't reachable from outside**
Forward TCP and UDP 42128 and TCP 443 to the server PC, and check that `address` is your current internet address. Many home connections get a new address now and then.

## Reporting a problem

Please include:

- what you did and what happened, with the time;
- the game's log, `%APPDATA%\DawngateData\Application.log`;
- for a problem in a match, the match's folder from `Dawngate-Data\logs\hosts\`, if you run the server.

Logs contain login names and addresses. Remove what you don't want to share.
