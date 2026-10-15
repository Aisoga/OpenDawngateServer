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
- About 5 GB of free disk space: the zip, plus two copies of the game. One copy is for playing, the other is used by the server to run matches.

Nothing else needs to be installed.

## Installing

1. Unpack the release zip into a folder of its own, for example `C:\Games\Dawngate`.
2. Put `Dawngate.zip` into that folder, next to `Start-Server.exe`. If you leave it in your Downloads folder, the programs find it there too.
3. Start **`Start-Server.exe`**. On its first start it asks:

   ```
   Who will play on this server?
       1  only this PC: you play here with Start-Client.exe
       2  your home network: also players on the other PCs at home
       3  the internet: a public server for players anywhere (at your own risk)
   ```

   Press **Enter** to play on this PC. The program then checks `Dawngate.zip` against the original archive, sets everything up in a folder called `Dawngate-Data` and starts the server. Setup takes under a minute. Later starts take about a second. Leave the window open while you play.
4. Start **`Start-Client.exe`**. The game opens after a few seconds.

You only need `Start-Server.exe` if you run the server yourself. To join someone else's server, see [Playing on someone else's server](#playing-on-someone-elses-server).

**Windows warnings:** SmartScreen may call the programs unrecognized, because they aren't signed. Click *More info*, then *Run anyway*. Your antivirus may scan them the first time. The setup changes a few bytes in its own copy of the game (the server's address and the server's certificate authority). It never modifies your `Dawngate.zip`.

## Your first match

1. **Log in.** Choose a login name (3–32 letters, digits or `. _ - @`; an e-mail address works) and a password of 8–63 characters. On a server with open sign-up, the first login with a new name creates the account. After that, use the same password. Use a password you don't use anywhere else.
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
- **Towers (Bindings)** shoot minions first. They switch to an enemy shaper who attacks you near them.
- The **Locus** (the big tower) guards each base.
- Units collide and push each other a little. The item *Fluidity* lets you walk through them.

**Winning.** A team wins when the enemy **Guardian** dies. The Guardian has a shield while any of its five **cores** stands, so destroy the cores first.

- A destroyed core makes your minions stronger.
- A core grows back after eight minutes, and the Guardian's shield comes back with it.

**Bots.** The bots take their lanes after about a minute. They last-hit minions, cast their abilities, buy items, drink potions, place wards, push towers and fight. They also play as a team: they defend towers, gather for the parasite, capture and defend spirit wells, and join an ally going for a kill. When they have the advantage, after won fights or with the parasite's buff, they group up in one lane and push with their minions until they lose the fight; from about 20 minutes, and whenever enemy cores are down, they siege the enemy base to end the match. In a practice match they go easier on you, as the original's first-time rules did.

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
- The **parasite** in the middle of the map becomes attackable at 5:00 and only fights players who attack it.
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

Ask the server's operator for its address. Then start the client with it:

```
Start-Client.exe 192.168.1.20
```

You can also start `Start-Client.exe` without an address and type it in when asked. The address is remembered in `Start-Client.ini`. You don't need to run `Start-Server.exe`.

**The fingerprint question.** The first time you connect to a server, `Start-Client.exe` shows the server's fingerprint and asks whether it's the same as the one the operator has.

- Answer `y` only if it matches: on a home network, compare it with the one in the server's window; on a public server, with the one the operator published.
- This check makes sure you are talking to that server and not to someone else at the same address.
- If a server's fingerprint changes later, you get a warning and the question again.

**A server on the internet** works the same way, through the server's encrypted tunnel. You don't need to configure anything.

- You'll need an account first. Most public servers use invite codes: get one from the operator, then register on the server's website at `https://<server address>/signup`. The login screen's *Sign up now!* link opens the same page.
- On the website you can also change your password, download your data or delete your account.
- If you forget your password, ask the operator: the server doesn't send e-mail.

## Known limitations

- The loot bag click after a win hasn't been confirmed in a real game yet. If you don't click the bag, the game returns to the main menu about 105 seconds after the end anyway.
- Pause isn't available. Surrender isn't available in practice matches.
- The minimap doesn't show jungle camp icons.
- The bots follow rules partly reconstructed from the game's data, so their exact behaviour may differ from the original.
- Some details of the original server had to be guessed. If something works differently from how you remember it, a report helps, especially with a video of the original.

## Stopping and logs

- **To stop:** close the game in the main menu or at the login screen. In a match the window's close button doesn't work, so leave the match first. Then press Ctrl+C in the `Start-Server` window, or close it.
- **The game's own log** is `%APPDATA%\DawngateData\Application.log`.
- **The server's logs** are in `Dawngate-Data\logs`, with one folder per match under `hosts`.

If something goes wrong, see [troubleshooting.md](troubleshooting.md).
