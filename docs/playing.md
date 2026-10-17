# Playing Dawngate

This guide covers installing the game, playing your first match, and playing with other people. To run a server for others, see [hosting.md](hosting.md).

- [What you need](#what-you-need)
- [Installing](#installing)
- [Your first match](#your-first-match)
- [Controls](#controls)
- [What's in a match](#whats-in-a-match)
- [The main menu](#the-main-menu)
- [Playing with other people](#playing-with-other-people)
- [Known limitations](#known-limitations)
- [Stopping and logs](#stopping-and-logs)

## What you need

- Windows 10 or 11, 64-bit, with a graphics card that runs DirectX 9 games.
- `Dawngate.zip` from <https://archive.org/details/Dawngate> (1.3 GB, build 1.0.903.854). Every player needs their own copy.
- The release zip from this project's *Releases* page.
- About 5 GB of free disk space: the zip, plus two copies of the game. One copy is for playing, the other is used by the server on your PC to run matches. If you only ever play on other people's servers, the second copy is never made.

Nothing else needs to be installed.

## Installing

1. Unpack the release zip into a folder of its own, for example `C:\Games\Dawngate`.
2. Put `Dawngate.zip` into that folder, next to `Start-Client.exe`. If you leave it in your Downloads folder, the program finds it there too.
3. Start **`Start-Client.exe`**. Its first start checks `Dawngate.zip` against the original archive and makes your copy of the game in a folder called `Dawngate-Data` (about half a minute); then the game opens. Later starts take a few seconds.
4. The login screen opens with **This PC** selected above the login panel: the server on your own PC. Log in (see [Your first match](#your-first-match)). Your first login to it sets up the server's copy of the game (about half a minute) and starts the server; the login waits for it, and the server list shows the progress. From then on the server starts within a second or two of your login.

That's all you need to play alone or against bots. The server on your PC runs inside `Start-Client.exe` and stops when you close the game. Your accounts stay in `Dawngate-Data`.

You don't need `Start-Server.exe` to play. It is for running a server that other people play on ([hosting.md](hosting.md)); if it runs in the same folder, **This PC** is that server. To join someone else's server, see [Playing on someone else's server](#playing-on-someone-elses-server).

**Windows warnings:** SmartScreen may call the programs unrecognized, because they aren't signed. Click *More info*, then *Run anyway*. Your antivirus may scan them the first time. The setup changes a few bytes in its own copy of the game (the address the game connects to on your PC and its certificate authority). It never modifies your `Dawngate.zip`.

## Your first match

1. **Log in.** On **This PC** there is nothing to sign up for: type any login name (3–32 letters, digits or `. _ - @`) and any password. The name is your profile on this PC; no password is checked or stored. On another server, choose a login name and a password of 8–63 characters: on a server with open sign-up, the first login with a new name creates the account, and after that you use the same password (one you don't use anywhere else). Other servers need an account first ([Signing up](#signing-up)).
2. **The news overlay.** The first time you log in, a news overlay with three pages appears. Click its right arrow twice, then OK. Escape doesn't close it. It doesn't come back for that account.
3. **PLAY, then PRACTICE, then PRACTICE DAWNGATE.** The server starts a match for you ("Creating game, please wait.").
4. **Shaper select:**
   - **Select your Shaper:** click a shaper card, then **Choose <name>** at the bottom right. The **?** card at the end of the list picks a random shaper. Your bot teammates pick theirs within a few seconds. The enemy team's picks stay hidden until the reveal.
   - **Loadout and role:** pick a perk page under *Choose your Loadout* and a role card (Gladiator, Tactician, Hunter or Predator), then click **Finalize your selections!**
   - **Skin:** click one of your shaper's skins, and a ward or announcer pack if you like. This phase ends by itself after 15 seconds.

   If you don't pick in time, the game picks for you. The game remembers your skin and perk page for each shaper, and your ward and announcer pack for the next match. The ward you pick is the one you place in the match, and every ward and announcer pack is yours to pick.
5. **Play.** The match loads after about 30 seconds. It's five against five: you and four bot teammates against five bots.

## Controls

| Key | Action |
|---|---|
| Right-click | move, or attack what you click |
| A, then left-click | attack-move |
| Q, W, E, R | cast an ability at the cursor |
| Ctrl + Q/W/E/R | spend an ability point on that ability |
| 1, 2, 3 | battle spells |
| 4, 5 | potions and other consumables |
| B | recall to your base |
| S | stop |
| P | open the shop |
| Tab | scoreboard |
| Shift + Enter | open the match chat |
| `/dance`, `/taunt` | emotes, typed in the chat |

The gear icon above the minimap opens the Options menu (Escape doesn't). It has *Surrender game* and *Leave current match*.

## What's in a match

The match runs as in the original game. The server uses the game's own data and scripts for every shaper, ability, item and creature.

**The map.** There is one map, with two lanes and a jungle.

- **Minion waves** appear every 30 seconds from 1:00. Six minions spawn one after another and walk their lane in a line.
- **Minions fight minions and towers first.** They attack a shaper only when nothing else is near them, or when that shaper hits a shaper of their team. They follow a shaper who walks away only a short way (about 20 units, or 3 seconds of chasing), never far into the jungle, then return to their lane and leave that shaper alone for a few seconds. A minion that loses sight of you, for example in grass, turns back at once.
- **Jungle camps** ignore you until you hit one; then the whole camp fights you. A camp follows you only a short way: a creature turns back about 15 units from its spot, together with the rest of its camp, walks home untouchable and heals to full. Hits from far outside the camp don't pull it.
- **Spirit well workers** fight back when hit but never leave their well's platform; step off it and they go back to mining.
- **Towers (Bindings)** shoot minions first, before any shaper, even one they were already shooting when the minions arrived. They switch to an enemy shaper who damages a shaper of their team within their range, and keep shooting that shaper until it leaves their range.
- The **Locus** (the big tower) guards each base.
- Units collide and push each other a little. The item *Fluidity* lets you walk through them.

**Winning.** A team wins when the enemy **Guardian** dies. The Guardian has a shield while any of its five **cores** stands, so destroy the cores first.

- A destroyed core makes your minions stronger.
- A core grows back after eight minutes, and the Guardian's shield comes back with it.
- The Guardian defends its base: its attacks go at enemy shapers in its reach before minions, and its special attacks (orbs, laser, fire wall) at shapers.

**Bots.** The bots take their lanes after about a minute. They last-hit minions, cast their abilities, buy items, drink potions, place wards, push towers and fight. They also play as a team: they defend towers, gather for the parasite, capture and defend spirit wells, and join an ally going for a kill. When they have the advantage, after won fights or with the parasite's buff, they group up in one lane and push with their minions until they lose the fight; from about 20 minutes, and whenever enemy cores are down, they siege the enemy base to end the match. Their jungler leaves its camps to gank a lane where an ally is fighting, to defend the base, and to join pushes, well captures and the parasite near it. The bots follow your minimap pings: ping an enemy Binding to push its lane, the parasite to take it together, an enemy spirit well to capture it, or "retreat" to call a push or an objective off; ping one of your own Bindings or wells, an enemy shaper or a spot on the map and the jungler comes, answering with an "on my way" ping. In a practice match they go easier on you, as the original's first-time rules did.

**Gold and experience.** You start with 500 gold and earn 100 gold a minute from 1:00. Last hits and kills give gold and experience. Each level gives you an ability point to spend with Ctrl + Q/W/E/R:

- Q, W and E go up to rank 5.
- R ranks up at levels 6, 11 and 16.
- Level 10 gives you a battle spell point instead of an ability point.

**The shop (P).** You can buy and sell items while you stand in your base or while you are dead. To buy, click an item, then **Purchase**. Selling pays half the price back.

**Battle spells.** Click **SPELL BOOK** under the spell slots to learn one of twelve spells. You can only learn spells at your base. You get one spell point at level 1, another at level 10 and a third at level 20. Use spells with keys 1–3.

**Spirit wells.** Each team starts with two wells.

- Workers gather at your wells and earn gold for your whole team. The panel at the top shows each team's well income.
- From 15:00 you can capture an enemy well: stand in its central ring for 16 seconds.
- Only enemy shapers can interrupt a capture, by hitting you or by standing in the ring.
- A captured well pays your team, then locks for four minutes.

**Jungle.** Creep camps respawn after you clear them.

- Killing the big creature of a buff camp gives you its buff for 150 seconds: Green for Power, Blue for Haste or Orange for armor.
- **Camp icons on the minimap** show what your team knows. Every camp gets its icon when it first spawns. When your team kills a camp or sees its last creature die, its icon goes away for your team and comes back when the camp respawns. A camp the enemy cleared out of your sight keeps its icon until your team sees it empty, so an icon can be out of date. Spectators see every camp as it really is.
- The **parasite** in the middle of the map becomes attackable at 5:00 and only fights players who attack it. Once it fights, it attacks the shaper next to it, or else the nearest within its reach; from 15:00 every fifth attack is a fire breath, and from 25:00 a fire breath or a barrage of orbs in turn. While it lives, its red mark shows in the middle of the minimap.
- Killing the parasite pays your team. A kill at its later stages gives your team a big buff and strong Strider minions in your waves for a few minutes.

**Fog of war.** You see what your team's units, towers and wards see.

- Walls block vision.
- Tall grass hides whoever stands in it from anyone outside it, until they use an ability.
- Stealthed enemies stay hidden even right next to you.
- An enemy's level and items show while your team can see that enemy.

**Death and respawn.** When you die the screen turns grey and your portrait counts down to your respawn. **View Death Recap** shows who hurt you. Your items keep their effects after a respawn.

**Passives.** Your shaper's passive ability and your role's bonus are always active:

- **Gladiator:** extra gold from nearby minion deaths.
- **Hunter:** bonuses against jungle creeps.
- **Predator:** double gold from well workers.
- **Tactician:** gold for hitting enemy shapers.

**Voice lines.** The shapers speak their kill, assist and banter lines as in the original. Emotes and quick-chat lines play for your team or for everyone.

**The end.** When a Guardian dies, the post-game screen comes up.

- **After a win**, click the loot bag to open it. The game then returns to the main menu by itself.
- **After a loss**, the game returns to the main menu after about 10 seconds and shows your After Action Review.
- Your win, Destiny, rewards and statistics are saved to your account.

**Idle kick.** After five minutes without any input in a match, the game warns you and then logs you out. Log in again to continue.

## The main menu

- **Your account** keeps everything between sessions: currencies, collection, perk pages, settings and match history. A new account starts with 10,000 Destiny (the currency you earn by playing) and 1,000 Waypoints (the store's other currency). By default it owns every shaper, skin, ward and announcer pack.
- **Collection:** your shapers, skins and so on. You buy Spiritstones (perk shapes) and Sparks (gems) here with Destiny.
- **Loadouts:** your perk pages. Drag a Spiritstone onto the grid, then fill its sockets with Sparks from the *Sparks* tab, and click **Save**.
  - A page only counts in a match when every socket of its Spiritstone holds a matching Spark.
  - The **+** beside the page tabs buys another page, up to 10.
- **Leaderboard:** the server's top 200 players with at least 50 games (the server's operator can change the number), ranked by wins, then by fewer games. Every match counts, practice and custom games too. The arrows show how a rank changed since the start of the day (UTC).
- **Profile:** your career, recent history and all-time statistics.
- **Wins** show on the trophy at the top right.

## Playing with other people

Everyone on a server needs their own account. The ways to play together:

- **The solo queue:** on the **VERSUS** tab, click **QUEUE SOLO**. The server waits about 15 seconds for other players, then starts a match. Bots fill any empty seats, and they don't go easier on you here. **CANCEL MATCHMAKING** takes you out of the queue.
- **A party:**
  1. The leader clicks **CREATE A GROUP**, then the add-players icon below the team list.
  2. The leader types the other player's name (shown at the top right of their main menu) and clicks the small person-plus icon.
  3. The invited player gets a **Matchmaking Invite** at the bottom right and clicks **YES**.
  4. The lobby has its own chat. **QUEUE AS GROUP** (leader only) puts the whole party on the same team.
- **In the match:** everyone sees and fights each other as in the original. Press **Shift + Enter** to chat.
- **Surrender** (queue and custom matches, not practice): from 20 minutes on, choose **SURRENDER GAME** in the gear menu or type `/surrender`.
  - Teammates vote with **Yes**/**No** or `/surrender`/`/nosurrender`.
  - Two thirds of the team must agree.
  - After a failed vote, your team must wait five minutes before the next one.
- **Karma:** after a queue match, give karma to other players with the green button next to their name. Each karma vote you receive adds to your loot bag.
- **If your game crashes or you close it** during a match, your shaper stays in the match. Start the game again and log in, then click **Rejoin** in the main menu.
  - **LEAVE CURRENT MATCH** in the gear menu works the same way: you can rejoin later.
  - If every player of a team stays away for 60 seconds, that team surrenders.
- **Custom games:** under **CUSTOM**, **CREATE GAME** makes a lobby with a name and an optional password; **BROWSE GAMES** lists the server's games (a padlock marks a password), with **JOIN GAME** and **SPECTATE GAME**.
  - In the lobby, **Join Mortal Team**/**Join Spirit Team** and **Join as Spectator** switch your side. The game's creator (the crown) can kick a player with a right-click on their slot and starts the game with **Start Game**.
  - If the creator leaves the lobby, the game closes for everyone.
  - Empty seats stay empty unless the server's operator has bots fill them.
- **Spectating:** a spectator sees both teams and the whole map, and can also join a game that is already running. If a spectator's game closes, **Rejoin** in the main menu brings them back.

### Playing on someone else's server

Ask the server's operator for its address. You don't need to run `Start-Server.exe`.

Every server, on the internet or on your home network, is reached through its encrypted tunnel, and you pick it on the game's login screen:

1. Start the client with the server's address once:

   ```
   Start-Client.exe 203.0.113.5
   ```

   The address goes into your server list. From then on, just start `Start-Client.exe`: the login screen opens with the server you played on last.
2. On the login screen, **SERVER** above the login panel shows the server you are about to log in to. Click it to open the list (**SELECT A SERVER**), click a server, then **DONE**.
3. Log in. **The first login to a new server stops** with *New server key: check the fingerprint shown in the list, then log in again to trust it.* Click OK, open the list and compare the server's key (16 hex digits, such as `25edc74b8876bbbc`) with the one its operator gives you. Only if they match, press **LOGIN** again: that login trusts the key and logs you in, and every later login checks it.

If the operator hands you the whole key (64 hex digits, shown in the server's window), start `Start-Client.exe <address> --gateway-pin <key>` once instead: the key is trusted without the question.

#### Signing up

Each row of the server list shows how that server makes accounts:

| Sign-up | Meaning |
|---|---|
| **open** | Your first login with a new name makes the account, with the password you type. |
| **invite only** | Sign up first, with an invite code from the operator, on the server's sign-up page. |
| **closed** | Only the operator makes accounts: ask them for one. |
| **no password** (This PC) | Any login name logs in with any password. |

To sign up, select the server in the list and click **Sign up now!** below the login: your browser opens that server's sign-up page, before you have ever logged in there. *Forgot Password?* opens the server's own page the same way. For a server whose key you haven't trusted yet, the page shows its key first: compare the fingerprint with the one its operator gives you, and only if they match, click **Trust … and open the page**. That trusts the key for your logins too. The links' pages come from `Start-Client.exe` on your PC, which reaches the server through its tunnel; they work only while the game runs.

A login with a name the server doesn't know, on a server that needs a sign-up first, says *The login name or password you entered is incorrect. No account on this server yet? Make one first: Sign up now! below.* (A wrong password says the same: the server doesn't tell which names exist.)

- On a server's website you can also change your password, download your data or delete your account.
- If you forget your password, ask the operator: the server doesn't send e-mail.

#### The server list

Each row shows a server's name, its address, its sign-up ([Signing up](#signing-up)) and its status. The status is checked again every few seconds while the login screen is open, so a server that goes down or comes back shows without a restart.

The first row is always **This PC**, the server on your own PC. It can't be removed. Logging in to it starts the server inside `Start-Client.exe` (or uses `Start-Server.exe`'s server when that runs in the same folder), and its key is never asked for: it is in your own folder. The server that `Start-Client.exe` runs has no passwords: any login name logs in with any password, and only your PC reaches it. Set `local_trust = off` in `Start-Client.ini` to have it check passwords like any server. While `Start-Server.exe` runs in the same folder, This PC is that server, which may have other players, so it checks passwords: an account you made on This PC without a password takes the password of your first login there from this PC (a login from elsewhere is refused).

| Status | Meaning |
|---|---|
| **starts at login** (This PC) | The server on your PC isn't running yet. Your login starts it; the first time it also sets up its copy of the game. |
| **setting up, 45 %** (This PC) | The server's copy of the game is being made. Your login waits for it. |
| **starting** (This PC) | The server on your PC is starting. |
| **didn't start** (This PC, red) | The server on your PC couldn't be set up or started: the `Start-Client` window says why. The next login tries again. |
| **online, 23 ms** (green) | The server answers with the key you trusted, in that many milliseconds. |
| **online, new key** | The server answers, and you haven't logged in there yet. Your first login shows its key, the second trusts it. |
| **KEY CHANGED** (red) | The server shows another key than the one you trusted. Logins are refused: see [troubleshooting.md](troubleshooting.md#logging-in). |
| **offline** (red) | Nothing answers: the server is down, or its address is wrong. |
| **no tunnel** | Something answers at that address, but not a Dawngate server with its tunnel on. |
| **added at login** | A server you added that couldn't be checked yet. Your next login to it checks it and adds it. |
| **not checked** | Not checked yet. |
| **other version** (red) | The server runs another release than your `Start-Client.exe`. Logins there are refused: see [Versions](#versions). |
| **version unknown** (red) | The server doesn't tell its version: it runs a release from before the version check. Logins there are refused. |

The sentence under the list says more about the selected server, its sign-up included (for a server of another version it names both versions instead). For an online server it names the server's version and key, a fingerprint of 16 hex digits such as `25edc74b8876bbbc`. For a new server, compare that fingerprint with the one its operator publishes: your first login there stops so that you can, and the next one trusts the key; every later login checks it. For a changed key, the sentence names both the key you trusted and the one the server shows now.

**Adding a server:** open the list, click **ADD SERVER**, type the address (an IPv4 address such as `203.0.113.5` or a host name, with `:<port>` if the operator's tunnel port isn't 42128) and, if you like, a name, then click **ADD**. ADD checks the server at once and adds it to your list without logging in: the login screen confirms with *The server was added to your list*, and a moment later the list shows its status, key and sign-up. Then sign up there, or log in: your first login stops with *New server key* while the list shows its key; compare it, and **LOGIN** again trusts it and logs you in. If the server doesn't answer, ADD says *The server you picked doesn't answer*: nothing is added, and the row stays marked **added at login** until a login to it succeeds. `Start-Client.exe <address>` adds a server too.

**Removing a server:** select it in the list and click **REMOVE**. The login screen confirms with *The server was removed from your list.* Its key stays remembered, so a server you add again is still checked against it. **This PC** has no **REMOVE**.

**Typing it instead:** the list puts the pick into your login name, and you can type it yourself. Each server has a tag (`a`, `b`, …; `Start-Client.exe` lists them in its window):

| Login name | Effect |
|---|---|
| `alex#b` | Log in as `alex` on the server with tag `b`. |
| `alex#pc` | Log in as `alex` on This PC. |
| `alex#+203.0.113.5` | Add that server (optionally `alex#+203.0.113.5=Friends` with a name) and log in there. |
| `alex#*203.0.113.5` | Add that server without logging in (what **ADD** does). |
| `alex#-b` | Remove the server with tag `b`. |

The server only ever sees `alex`.

**The list works with the mouse.** Tab moves between the login name and the password as usual, but doesn't reach **SERVER**: Shift+Tab from the login name goes to the game's **EXIT** button (Enter there asks whether to quit). Escape doesn't close the list; use **DONE**. With the list open, the arrow keys move the selection.

**Switching servers:** log out, or wait for the game's idle logout. You are back at the login screen with the list, and you can pick another server.

**The key check** makes sure you are talking to that server and not to someone else at the same address; a sign-up page asks for it too. This PC needs none (`Start-Client.exe` reads its key from your own folder), nor does a server whose key you got with `--gateway-pin`.

**A server without the tunnel** (its operator set `tunnel = off`, an option for developers) can't be in the list: start `Start-Client.exe <address> --tunnel off`, which plays on that one server directly and asks once whether the server's certificate fingerprint matches the one in its window.

#### Versions

Your `Start-Client.exe` and the server must be the same release: **any difference refuses the login**, and so does a server that doesn't tell its version (a release from before this check). The list marks such a server **other version** or **version unknown**, and the sentence under the list names the server's version and yours. A refused login says *This server runs another version than your Start-Client, or doesn't tell its version*.

- `Start-Client.exe --version` prints your version; its window shows it at the start too. A release's version is its name, such as `Dawngate-Restoration-20261016-905ef17f5f`.
- To play on a server of another version, get the release it runs (ask its operator) and use that release's `Start-Client.exe`. You can unpack it into the same folder over the old files: `Dawngate-Data`, your server list and the trusted keys stay.
- **This PC** is never refused: it is the server in your own folder.
- Without the tunnel (`--tunnel off`), `Start-Client.exe` asks the server's version when it starts and doesn't start the game on a server of another version.

## Known limitations

- The loot bag click after a win hasn't been confirmed in a real game yet. If you don't click the bag, the game returns to the main menu about 105 seconds after the end anyway.
- Pause isn't available. Surrender isn't available in practice matches.
- The minimap has no respawn timers for the jungle camps; a cleared camp's icon just comes back at the respawn.
- The bots follow rules partly reconstructed from the game's data, so their exact behaviour may differ from the original.
- Some details of the original server had to be guessed. If something works differently from how you remember it, a report helps, especially with a video of the original.

## Stopping and logs

- **To stop:** close the game in the main menu or at the login screen. In a match the window's close button doesn't work, so leave the match first. `Start-Client.exe` ends with the game, and the server on your PC with it. If you run `Start-Server.exe`, press Ctrl+C in its window, or close it.
- **The game's own log** is `%APPDATA%\DawngateData\Application.log`.
- **The server's logs** are in `Dawngate-Data\logs`, with one folder per match under `hosts`. `Start-Client.exe`'s own log is `front-<time>.log` there.

If something goes wrong, see [troubleshooting.md](troubleshooting.md).
