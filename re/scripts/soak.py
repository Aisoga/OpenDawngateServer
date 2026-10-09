"""Long practice matches on the match host (Q3, the soak test): the player's scripted match, a peek loop and the report.

A full-length practice match (nine bots, waves, camps, wells, the Guardians) is played the way a player meets it:
the human's shaper walks the top lane, fights (attack-moves), casts its self and aimed abilities, ranks them up,
recalls and shops, and goes for an enemy spirit well once the wells unlock (15:00). The input comes only from the
test side ([host] commands_file, docs/SERVER_MODE.md *Command intake on the server (D7-live)*), never from the
real mouse or keyboard. While the session runs, a peek loop reads the client's objects every N seconds
(game_peek.py), and afterwards `report` reads the session's logs into one timeline: what breaks when everything
runs together for a long time (docs/SERVER_MODE.md *Soak test (Q3)*).

Commands:
  commands [--end S] [--out FILE]
      Writes the player's script (lines "<seconds after EnterGame> <hex>", "{caster}" for the player's creature,
      gms_commands.py's builders): the opening (rank W, Basic_Life and two potions, the walk to the top lane),
      the laning loop (an attack-move every 6 s between two points of the top lane's middle, W every 15 s, an
      aimed E every 25 s, R every 100 s from 6:00, a rank-up attempt every 20 s rotating R, W, E, Q), recalls
      with a shop visit (the next step of Fenmore's recommended build, potions), a voice line and a dance, and
      three well visits (the enemy wells at 15:05, 21:00 and 26:00). Until --end (1800).
  peek <out dir> [--every S] [--exe-part PART] [--until S]
      Every S seconds (60) runs game_peek.py on the Dawngate.exe whose path contains PART
      (client\\run_854_b\\ by default: lane b's client; lane a's is client\\run_854\\) and writes peek_<HHMMSS>.txt
      with the wall-clock time on its first line; stops when no such process exists for 5 minutes, or after --until
      seconds.
  edges [--end S] [--every S] [--first S] [--other-human N] [--no-xp] [--out FILE]
      (Q3c) The same match with the command edge cases in blocks of about 100 s (the first at --first, 90, then every
      --every, 300, until --end; laning pauses during a block): repeated right-clicks on one target, retargeting,
      an attack-move through the wave, a smart-cast (an aimed cast out of range with the walk-up flag) [assumed: what
      the client's quick cast sends], a cast at the caster's own position (zero-length aim, {casterpos}), commands
      during casts that hold the agent (a move 0.1-0.2 s after E and R), a battle spell (Blink, bought at the start)
      with a queued ability (an out-of-range Q, Blink 0.3 s later, then a basic attack), a targeted ability spammed on
      an enemy shaper (Q ten times in a second at {creature:5}, then basic attacks), recall then actions (a move
      during the channel, Q during the next, a full recall and purchases while walking out of the shop). XP 2200 at
      the first block (all four abilities ranked; --no-xp leaves it out). --other-human N adds the same attacks and
      spam on the other human's creature (gameplay index N; two clients, G3). Every line is commented with its case.
  timing <session dir>
      (Q3c) The host's timing per match minute from host\\<n>\\host_timing.csv (perf.h): tick durations, late ticks,
      lags, the slowest step, traffic per kind, ClockSync and answer delays, the command latency per kind, the records
      and the object id counter; then what grows over the match (the first five minutes against the last five).
  report <session dir> [--peek DIR] [--csv FILE]
      Reads the session's host log(s) (host\\<n>\\dgsim.log), host_samples.csv, application_log.txt (the client's
      asserts) and the peek files, and prints per minute of the match: the host's private bytes, working set and CPU,
      the replication bytes and messages per second (mean and max of the `replication: last 1.0 s` lines), the
      line-of-sight pool, the objects the player knows, the server objects, the Lua threads and resumes, asserts and
      Lua errors, exceptions; the deaths, kills, waves, camps, wells and workers; then the bots that didn't move
      (a living bot within 5 units for 3 minutes or more), the client's new asserts against the known ones, and
      for each peek the objects the client holds against the ones the host had created for the player and not
      deleted (desyncs: missing or extra server ids, and the players' positions and HP against the host's
      `stats bots:` line nearest in time); since Q3b also the client's memory per match minute from
      run_session.ps1's client_samples.csv (private bytes, working set, 32-bit address space used and committed,
      the largest free block, the GPU counters, the other clients running); since Q3c also `timing`'s table.

Usage:
  .venv\\Scripts\\python -I re\\scripts\\soak.py commands --out re\\out\\q3\\commands_run1.txt
  .venv\\Scripts\\python -I re\\scripts\\soak.py edges --out re\\out\\q3c\\commands_edges.txt
  .venv\\Scripts\\python -I re\\scripts\\soak.py peek re\\out\\q3\\run1 --every 60
  .venv\\Scripts\\python -I re\\scripts\\soak.py report re\\logs\\q3_run1 --peek re\\out\\q3\\run1
  .venv\\Scripts\\python -I re\\scripts\\soak.py timing re\\logs\\q3c_run1
"""
import argparse
import bisect
import collections
import csv
import datetime
import glob
import os
import re
import struct
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import gms_commands as gc  # noqa: E402

CASTER = 0x0BADF00D  # gms_commands' placeholder; written as {caster}
SENTINEL_ID = 0x0BADBEEF  # Q3c: a target written as a host placeholder ({nearest:...}, {creature:N})
SENTINEL_POS = (1234.5, -2345.5, 0.0)  # Q3c: a point written as one ({nearestpos:...}, {casterpos}, {creaturepos:N})

# ---- the player's match ------------------------------------------------------------------------------------------
LANE_HOME = (-85.0, 55.0)  # our side of the top lane (D10's move)
LANE_A, LANE_B = (-78.0, 62.0), (-66.0, 74.0)  # the top lane's middle, between the outer Bindings (D1b-live's duel)
E_AIM = (-60.0, 82.0)
ENEMY_WELL_TOP = (-27.6, 182.7)  # gold_red_side (MATCH_FLOW.md R10 §4.1)
ENEMY_WELL_BOTTOM = (182.5, -27.9)
SLOT_Q, SLOT_W, SLOT_E, SLOT_R = 3, 4, 5, 6
RECALLS = [  # (time, purchases): Fenmore's RecommendedItemBuild_Alpha, components first
    (300, ["Basic_Hunger", "Consumable_Potion_HealingPotion", "Consumable_Potion_HealingPotion"]),
    (600, ["Advanced_Desire", "Consumable_Potion_HealingPotion"]),
    (820, ["Basic_Will", "Consumable_Potion_HealingPotion", "Consumable_Potion_HealingPotion"]),
    (1140, ["Advanced_Balance"]),
    (1440, ["Legendary_Ambition", "Consumable_Potion_HealingPotion"]),
    (1740, ["Legendary_Valor"]),
]
WELLS = [(905, 965, ENEMY_WELL_TOP), (1260, 1320, ENEMY_WELL_TOP), (1560, 1650, ENEMY_WELL_BOTTOM)]


class Script:
    def __init__(self):
        self.lines = []
        self.seq = 0

    def _next(self):
        self.seq = self.seq % 255 + 1
        return self.seq

    def add(self, t, msg, note, target_ph=None, pos_ph=None):
        h = msg.hex().replace("%08x" % int.from_bytes(CASTER.to_bytes(4, "little"), "big"), "{caster}")
        if target_ph:  # Q3c: the host's placeholders ({nearest:...}, {creature:N}) for the sentinel id ...
            h = h.replace(SENTINEL_ID.to_bytes(4, "little").hex(), target_ph)
        if pos_ph:  # ... and position ({nearestpos:...}, {casterpos}, {creaturepos:N})
            h = h.replace(struct.pack("<3f", *SENTINEL_POS).hex(), pos_ph)
        self.lines.append((round(t, 2), h, note))

    # Q3c: a cast or basic attack whose target and points the host fills in from placeholders
    def cast_at(self, t, slot=-1, guid=0, target_ph=None, pos_ph=None, aim=None, repeat=0, approach=0, note=""):
        a = SENTINEL_POS if pos_ph else ((aim[0], aim[1], 0.0) if aim else (0.0, 0.0, 0.0))
        msg = gc.build_cast(slot=slot, guid=guid, target=SENTINEL_ID if target_ph else 0, aim=a, pos=a, repeat=repeat,
                            approach=approach, seq=self._next(), obj=CASTER)
        self.add(t, msg, note, target_ph, pos_ph)

    def attack(self, t, target_ph, pos_ph, note):  # a right-click on an enemy: the basic attack, keep attacking
        self.cast_at(t, slot=0, target_ph=target_ph, pos_ph=pos_ph, repeat=1, approach=1, note=note)

    def line(self, t, text, note):  # a commands-file line that isn't a message (xp, gold, report)
        self.lines.append((round(t, 2), text, note))

    def move(self, t, pos, attack=False):
        self.add(t, gc.build_move((pos[0], pos[1], 0.0), seq=self._next(), obj=CASTER, attack=attack),
                 "%s to (%g, %g)" % ("attack-move" if attack else "move", pos[0], pos[1]))

    def cast(self, t, slot=-1, guid=0, aim=None, note=""):
        a = (aim[0], aim[1], 0.0) if aim else (0.0, 0.0, 0.0)
        self.add(t, gc.build_cast(slot=slot, guid=guid, aim=a, pos=a, seq=self._next(), obj=CASTER), note)

    def stop(self, t):
        self.add(t, gc.build_action_command(8, self._next(), CASTER, b""), "stop")

    def rank(self, t, slot):
        self.add(t, bytes([gc.WIRE_ABILITY_RANK_UP]) + slot.to_bytes(4, "little"), "rank-up %s" % gc.SLOT_NAME[slot])

    def buy(self, t, item):
        self.add(t, gc.build_item_transaction("purchase", item), "purchase " + item)

    def other(self, t, typ, data, note):
        self.add(t, gc.build_action_command(typ, self._next(), CASTER, data), note)

    def text(self, title="soak.py commands (Q3): the player's scripted practice match"):
        out = ["# %s; <s after EnterGame> <hex>  # what" % title]
        for t, h, note in sorted(self.lines, key=lambda x: x[0]):
            out.append("%g %s  # %s" % (t, h, note))
        return "\n".join(out) + "\n"


# ---- Q3c: the command edge cases -----------------------------------------------------------------------------------
EDGE_BLOCK_S = 95  # one block's length (laning pauses)
SLOT_SPELL_1 = 7  # the first battle spell (inventory slot 6 = ability slot 7, GMS_PROTOCOL.md *Ability slots*)
ENEMY = "{nearest:Minion_Spirit}"  # the human plays team 1 (Mortal): the Spirit wave is the enemy's
ENEMY_POS = "{nearestpos:Minion_Spirit}"


def edge_block(s, t0, first, xp, other_human, enemy_shaper=5):
    """One block of Q3c's command edge cases from t0 (the shaper in the top lane's middle)."""
    if first and xp:
        s.line(t0, "xp 2200", "edge: XP for level 6 (Q, W, E, R can all be ranked)")
    for i, slot in enumerate((SLOT_Q, SLOT_W, SLOT_E, SLOT_R)):
        s.rank(t0 + 0.5 + 0.3 * i, slot)
    for i in range(5):  # the same right-click five times, 0.25 s apart
        s.attack(t0 + 3 + 0.25 * i, ENEMY, ENEMY_POS, "edge: repeated right-click #%d on one target" % (i + 1))
    s.attack(t0 + 6, "{nearest:Minion_Spirit_Melee}", "{nearestpos:Minion_Spirit_Melee}", "edge: target A (melee)")
    s.attack(t0 + 6.6, "{nearest:Minion_Spirit_Ranged}", "{nearestpos:Minion_Spirit_Ranged}",
             "edge: retarget to B (ranged)")
    s.attack(t0 + 7.2, "{nearest:Minion_Spirit_Melee}", "{nearestpos:Minion_Spirit_Melee}", "edge: retarget back to A")
    s.move(t0 + 10, (LANE_B[0] + 12, LANE_B[1] + 12), attack=True)  # through the wave
    far = (E_AIM[0] + 30, E_AIM[1] + 30)
    s.cast_at(t0 + 16, slot=SLOT_E, aim=far, approach=1, note="edge: smart-cast E out of range (walk-up flag)")
    s.cast_at(t0 + 22, slot=SLOT_E, pos_ph="{casterpos}", note="edge: E at the caster's own position (zero-length aim)")
    s.stop(t0 + 28)
    s.cast(t0 + 28.5, slot=SLOT_E, aim=E_AIM, note="edge: E aimed ...")
    s.move(t0 + 28.6, LANE_HOME)  # ... and a move 0.1 s later, during the cast
    s.cast(t0 + 34, slot=SLOT_R, note="edge: R ...")
    s.move(t0 + 34.2, LANE_A)  # ... and a move 0.2 s later
    s.cast_at(t0 + 40, slot=SLOT_Q, target_ph=ENEMY, pos_ph=ENEMY_POS, approach=1,
              note="edge: Q on the nearest enemy minion (walk-up queued if out of range) ...")
    s.cast(t0 + 40.3, slot=SLOT_SPELL_1, aim=LANE_HOME, note="edge: ... the battle spell (Blink) 0.3 s later ...")
    s.attack(t0 + 41, ENEMY, ENEMY_POS, "edge: ... then a basic attack: does it start?")
    s.attack(t0 + 44, ENEMY, ENEMY_POS, "edge: ... and another")
    shaper, shaper_pos = "{creature:%d}" % enemy_shaper, "{creaturepos:%d}" % enemy_shaper
    for i in range(10):  # a targeted ability spammed on an enemy shaper
        s.cast_at(t0 + 50 + 0.1 * i, slot=SLOT_Q, target_ph=shaper, pos_ph=shaper_pos, approach=1,
                  note="edge: Q spam #%d on enemy shaper %d" % (i + 1, enemy_shaper))
    for i in range(3):
        s.attack(t0 + 52 + 0.3 * i, shaper, shaper_pos, "edge: basic attack #%d on enemy shaper %d" % (i + 1, enemy_shaper))
    s.stop(t0 + 56)
    s.move(t0 + 56.2, LANE_A)
    s.cast(t0 + 62, guid=gc.GUID_RETURN_TO_BASE, note="edge: recall ...")
    s.move(t0 + 63, LANE_A)  # ... cancelled by a move 1 s later
    s.cast(t0 + 65, guid=gc.GUID_RETURN_TO_BASE, note="edge: recall ...")
    s.cast_at(t0 + 66, slot=SLOT_Q, target_ph=ENEMY, pos_ph=ENEMY_POS, approach=1, note="edge: ... and Q 1 s later")
    s.stop(t0 + 67.5)
    s.cast(t0 + 68, guid=gc.GUID_RETURN_TO_BASE, note="edge: a whole recall (8 s)")
    s.move(t0 + 77.5, LANE_HOME)
    s.buy(t0 + 77.8, "Consumable_Potion_HealingPotion")  # purchases while walking out of the shop
    s.buy(t0 + 78.3, "Consumable_Potion_HealingPotion")
    s.buy(t0 + 84, "Consumable_Potion_HealingPotion")  # out of the shop's reach by then: refused
    if other_human is not None:  # G3: the other human (two clients)
        other, other_pos = "{creature:%d}" % other_human, "{creaturepos:%d}" % other_human
        for i in range(3):
            s.attack(t0 + 86 + 0.3 * i, other, other_pos, "edge: basic attack #%d on the other human" % (i + 1))
        for i in range(5):
            s.cast_at(t0 + 88 + 0.1 * i, slot=SLOT_Q, target_ph=other, pos_ph=other_pos, approach=1,
                      note="edge: Q spam #%d on the other human" % (i + 1))
        s.stop(t0 + 92)


def build_script(end=1800, edges=None):
    s = Script()
    s.rank(1, SLOT_W)
    s.buy(3, "Basic_Life")
    s.buy(3.5, "Consumable_Potion_HealingPotion")
    s.buy(4, "Consumable_Potion_HealingPotion")
    if edges is not None:
        s.buy(4.5, "Spell_Blink")  # Q3c: the battle spell for the edge cases (the one spell point)
    s.move(6, LANE_HOME)
    busy = []  # (from, to): no laning commands
    for t, items in RECALLS:
        if t >= end:
            continue
        busy.append((t, t + 22))
        s.stop(t)
        s.cast(t + 0.5, guid=gc.GUID_RETURN_TO_BASE, note="return to base")
        for i, item in enumerate(items):
            s.buy(t + 11 + 0.5 * i, item)
        s.move(t + 15, LANE_HOME)
    for t0, t1, well in WELLS:
        if t0 >= end:
            continue
        busy.append((t0, t1))
        s.move(t0, well)
        s.stop(t1 - 2)
        s.move(t1, LANE_HOME)
    if edges is not None:  # Q3c: the edge-case blocks, each moved past any recall or well visit it would overlap
        t0, first = float(edges["first"]), True
        while t0 + EDGE_BLOCK_S <= end:
            clash = [b for a, b in busy if a < t0 + EDGE_BLOCK_S and t0 < b]
            if clash:
                t0 = max(clash) + 5
                continue
            edge_block(s, t0, first, edges["xp"], edges["other_human"])
            busy.append((t0, t0 + EDGE_BLOCK_S))
            first = False
            t0 += edges["every"]

    def free(t):
        return not any(a <= t < b for a, b in busy)

    t, k = 62.0, 0
    while t < end:
        if free(t):
            s.move(t, LANE_A if k % 2 == 0 else LANE_B, attack=True)
        t += 6
        k += 1
    for t in range(64, end, 15):
        if free(t):
            s.cast(t, slot=SLOT_W, note="W (self)")
    for t in range(66, end, 25):
        if free(t):
            s.cast(t, slot=SLOT_E, aim=E_AIM, note="E aimed")
    for t in range(368, end, 100):
        if free(t):
            s.cast(t, slot=SLOT_R, note="R (self)")
    order = [SLOT_R, SLOT_W, SLOT_E, SLOT_Q]
    for i, t in enumerate(range(21, end, 20)):
        s.rank(t + 0.3, order[i % 4])
    s.other(203, 10, (9).to_bytes(4, "little"), "voice line 9 (laugh)")
    s.other(453.5, 5, b"", "dance")
    return s


def cmd_commands(a):
    if a.cmd == "edges":
        edges = {"first": a.first, "every": a.every, "xp": not a.no_xp, "other_human": a.other_human}
        text = build_script(a.end, edges).text("soak.py edges (Q3c): the soak match with the command edge cases")
    else:
        text = build_script(a.end).text()
    if a.out:
        os.makedirs(os.path.dirname(os.path.abspath(a.out)), exist_ok=True)
        with open(a.out, "w", encoding="ascii", newline="\n") as f:
            f.write(text)
        print("%s: %d command(s)" % (a.out, text.count("\n") - 1))
    else:
        sys.stdout.write(text)
    return 0


# ---- the peek loop -----------------------------------------------------------------------------------------------
def find_pid(part):
    """The pid of a Dawngate.exe whose path contains part (case-insensitive), through PowerShell's CIM."""
    ps = ("Get-CimInstance Win32_Process -Filter \"Name='Dawngate.exe'\" | ForEach-Object { "
          "'{0}|{1}' -f $_.ProcessId, $_.ExecutablePath }")
    try:
        out = subprocess.run(["powershell", "-NoProfile", "-Command", ps], capture_output=True, text=True,
                             timeout=60).stdout
    except (OSError, subprocess.SubprocessError):
        return None
    for line in out.splitlines():
        pid, _, path = line.partition("|")
        if part.lower() in path.lower():
            return int(pid)
    return None


def cmd_peek(a):
    os.makedirs(a.out_dir, exist_ok=True)
    peek = os.path.join(os.path.dirname(os.path.abspath(__file__)), "game_peek.py")
    start, last_seen = time.time(), time.time()
    while time.time() - start < a.until:
        pid = find_pid(a.exe_part)
        if pid is None:
            if time.time() - last_seen > 300:
                break
        else:
            last_seen = time.time()
            now = datetime.datetime.now()
            target = os.path.join(a.out_dir, "peek_%s.txt" % now.strftime("%H%M%S"))
            try:
                r = subprocess.run([sys.executable, "-I", peek, str(pid)], capture_output=True, text=True,
                                   timeout=120)
                body = r.stdout + r.stderr
            except (OSError, subprocess.SubprocessError) as e:
                body = "peek failed: %s\n" % e
            with open(target, "w", encoding="utf-8") as f:
                f.write("# wall %s pid %d\n" % (now.strftime("%H:%M:%S.%f")[:-3], pid))
                f.write(body)
        time.sleep(a.every)
    return 0


# ---- the report --------------------------------------------------------------------------------------------------
KNOWN_CLIENT_ASSERTS = ("GameSimulator.cpp(7412)", "ModelAnimator.cpp(2280)")
RE_LINE = re.compile(r"^\s*(\d+) \[\d+\] (.*)$")
RE_WALL = re.compile(r"^(\d\d):(\d\d):(\d\d)\.(\d\d\d) ")


def wall_s(text):
    m = RE_WALL.match(text)
    if not m:
        return None
    h, mi, s, ms = map(int, m.groups())
    return h * 3600 + mi * 60 + s + ms / 1000.0


def parse_host_log(path):
    """The host's timeline: per-second replication lines, stats lines, creates/deletes, asserts and errors."""
    r = {"repl": [], "stats": collections.defaultdict(list), "creates": {}, "deletes": {}, "events": [],
         "enter_ms": None, "enter_wall": None, "wall_at": [], "creatures": {}}
    day = 0
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            m = RE_LINE.match(line)
            if not m:
                continue
            ms, text = int(m.group(1)), m.group(2)
            w = wall_s(text)
            if w is not None:
                if r["wall_at"] and w + day < r["wall_at"][-1][1] - 43200:  # past midnight
                    day += 86400
                r["wall_at"].append((ms, w + day))
            if "the first EnterGame was sent" in text and r["enter_ms"] is None:
                r["enter_ms"] = ms
            if text.startswith("replication: last 1.0 s:"):
                mm = re.search(r"(\d+) messages, (\d+) bytes.*?(\d+) objects known.*?line-of-sight pool (\d+)/(\d+)",
                               text)
                if mm:
                    r["repl"].append((ms, int(mm.group(1)), int(mm.group(2)), int(mm.group(3)), int(mm.group(4)),
                                      int(mm.group(5))))
            elif text.startswith("replication: LabsPlayer_DeployCharacter(player "):
                mm = re.search(r"player (\d+),.*-> creature ([0-9A-F]{8})", text)
                if mm:
                    r["creatures"][mm.group(1)] = mm.group(2)
            elif text.startswith("replication: create "):
                mm = re.match(r"replication: create ([0-9A-F]{8}) (\S+).*?pos \(([-\d.]+), ([-\d.]+)", text)
                if mm:
                    r["creates"].setdefault(mm.group(1), []).append((ms, mm.group(2)))
            elif text.startswith("replication: delete "):
                mm = re.match(r"replication: delete ([0-9A-F]{8})", text)
                if mm:
                    r["deletes"].setdefault(mm.group(1), []).append(ms)
            elif text.startswith("stats "):
                key = text.split(":", 1)[0][6:]
                key = re.sub(r"t\+\d+s ticks \d+", "tick", key)
                r["stats"][key].append((ms, text))
            if text.startswith("stats ") or "hook installed" in text or "asserts server" in text:
                continue
            if "EXCEPTION" in text or "exception in" in text.lower():
                r["events"].append((ms, "exception", text[:300]))
            elif text.startswith("ASSERT"):
                r["events"].append((ms, "assert", text[:300]))
            elif re.search(r"lua error|script error|attempt to", text, re.I):
                r["events"].append((ms, "lua", text[:300]))
    return r


def ms_to_wall(r, ms):
    """A host log time (ms since the DLL loaded) as wall seconds, from the nearest line that has both."""
    pts = r["wall_at"]
    if not pts:
        return None
    if "wall_ms" not in r:
        r["wall_ms"] = [p[0] for p in pts]
    i = bisect.bisect_left(r["wall_ms"], ms)
    cands = [pts[j] for j in (i - 1, i) if 0 <= j < len(pts)]
    best = min(cands, key=lambda p: abs(p[0] - ms))
    return best[1] + (ms - best[0]) / 1000.0


def num(text, pattern, default=None):
    m = re.search(pattern, text)
    return float(m.group(1)) if m else default


def report(a):
    sess = a.session
    logs = sorted(glob.glob(os.path.join(sess, "host", "*", "dgsim.log")) +
                  glob.glob(os.path.join(sess, "host", "dgsim.log")))
    if not logs:
        print("no host log under %s" % sess)
        return 2
    out = []
    enter_walls = []  # the match start as wall seconds, for the client's samples (Q3b)
    for lg in logs:
        r = parse_host_log(lg)
        enter = r["enter_ms"] or 0
        if r["enter_ms"] is not None and ms_to_wall(r, enter) is not None:
            enter_walls.append(ms_to_wall(r, enter))
        out.append("== %s (EnterGame at host %.1f s)" % (os.path.relpath(lg, sess), enter / 1000.0))
        # per match minute
        minutes = collections.defaultdict(dict)
        for ms, msgs, by, known, los, loscap in r["repl"]:
            mi = int((ms - enter) // 60000)
            d = minutes[mi]
            d.setdefault("bytes", []).append(by)
            d.setdefault("msgs", []).append(msgs)
            d["known"] = known
            d["los"] = max(d.get("los", 0), los)
            d["loscap"] = loscap
        for ms, text in r["stats"].get("host", []):
            mi = int((ms - enter) // 60000)
            minutes[mi]["priv"] = num(text, r"private (\d+) MB")
            minutes[mi]["ws"] = num(text, r"working set (\d+) MB")
            minutes[mi]["as"] = num(text, r"address space used (\d+) MB")
            minutes[mi]["cpu"] = num(text, r"cpu ([\d.]+) s")
            minutes[mi]["luaheap"] = num(text, r"lua heap (\d+) KiB")
        for ms, text in r["stats"].get("tick", []):
            mi = int((ms - enter) // 60000)
            minutes[mi]["objects"] = num(text, r"objects (\d+)")
            minutes[mi]["threads"] = num(text, r"lua scheduled threads (\d+)")
            minutes[mi]["asserts"] = num(text, r"asserts server (\d+)")
            minutes[mi]["luaerr"] = num(text, r"lua errors server (\d+)")
        for ms, text in r["stats"].get("match", []):
            mi = int((ms - enter) // 60000)
            minutes[mi]["mtime"] = num(text, r"match ([\d.]+) s")
            minutes[mi]["waves"] = num(text, r"waves (\d+)")
            minutes[mi]["towers"] = num(text, r"towers destroyed (\d+)")
            minutes[mi]["ghp"] = re.findall(r"team (\d) hp (-?[\d.]+)", text)
        for ms, text in r["stats"].get("death", []):
            mi = int((ms - enter) // 60000)
            minutes[mi]["deaths"] = num(text, r"deaths (\d+)")
            minutes[mi]["pdeaths"] = num(text, r"player deaths counted (\d+)")
        for ms, text in r["stats"].get("containers", []):  # D1j's line: the ability data containers' pool
            minutes[int((ms - enter) // 60000)]["cont"] = num(text, r"pool live (\d+) of")
        for ms, text in r["stats"].get("abilities", []):
            mi = int((ms - enter) // 60000)
            minutes[mi]["ab_reg"] = num(text, r"registered (\d+)")
            minutes[mi]["ab_end"] = num(text, r"ended (\d+)")
        out.append("min  match  priv  ws   AS    cpu    B/s mean  B/s max  msg/s  known  objs  LoS      threads  "
                   "luaheap  asserts luaerr  deaths  waves towers  abil(reg-ended)  containers  Guardians")
        prev_cpu = None
        for mi in sorted(minutes):
            d = minutes[mi]
            bys = d.get("bytes", [0])
            cpu = d.get("cpu")
            dcpu = "" if cpu is None or prev_cpu is None else "%.0f%%" % ((cpu - prev_cpu) / 60 * 100)
            if cpu is not None:
                prev_cpu = cpu
            ab = (d.get("ab_reg") or 0) - (d.get("ab_end") or 0)
            out.append("%3d  %5s  %4s  %4s %5s %5s  %8.0f  %7d  %5.0f  %5s  %4s  %4s/%-4s %5s  %6s  %6s %6s  %6s  %5s %4s  %6.0f  %10s  %s" % (
                mi, "%.0f" % d["mtime"] if d.get("mtime") is not None else "-",
                "%.0f" % d["priv"] if d.get("priv") else "-", "%.0f" % d["ws"] if d.get("ws") else "-",
                "%.0f" % d["as"] if d.get("as") else "-", dcpu,
                sum(bys) / len(bys), max(bys), sum(d.get("msgs", [0])) / max(1, len(d.get("msgs", [0]))),
                d.get("known", "-"), "%.0f" % d["objects"] if d.get("objects") else "-",
                d.get("los", "-"), d.get("loscap", "-"), "%.0f" % d["threads"] if d.get("threads") else "-",
                "%.0f" % d["luaheap"] if d.get("luaheap") else "-",
                "%.0f" % d["asserts"] if d.get("asserts") is not None else "-",
                "%.0f" % d["luaerr"] if d.get("luaerr") is not None else "-",
                "%.0f" % d["deaths"] if d.get("deaths") is not None else "-",
                "%.0f" % d["waves"] if d.get("waves") is not None else "-",
                "%.0f" % d["towers"] if d.get("towers") is not None else "-", ab,
                "%.0f" % d["cont"] if d.get("cont") is not None else "-",
                " ".join("t%s %s" % g for g in d.get("ghp", []))))
        # events
        kinds = collections.Counter(k for _, k, _ in r["events"])
        out.append("events: %s" % (", ".join("%s %d" % kv for kv in sorted(kinds.items())) or "none"))
        firsts = {}
        for ms, k, text in r["events"]:
            key = (k, re.sub(r"[0-9A-F]{8}|\d+(\.\d+)?", "#", text)[:120])
            firsts.setdefault(key, [ms, 0, text])
            firsts[key][1] += 1
        for (k, _), (ms, n, text) in sorted(firsts.items(), key=lambda kv: kv[1][0])[:40]:
            out.append("  %-9s x%-5d first at match %7.1f s: %s" % (k, n, (ms - enter) / 1000.0, text[:220]))
        # stuck bots
        tracks = collections.defaultdict(list)
        for ms, text in r["stats"].get("bots", []):
            for b in re.finditer(r"\[(\d+) (\S+) t(\d) lane \d \((-?\d+), (-?\d+)\) hp (-?\d+) L(\d+) plan (\d+) "
                                 r"phase (-?\d+) leaf (\d+) (\S+)", text):
                tracks[(b.group(1), b.group(2))].append((ms, float(b.group(4)), float(b.group(5)), float(b.group(6)),
                                                         b.group(11), b.group(8)))
        out.append("bots that stood still (alive, within 5 units for >= 180 s):")
        any_stuck = False
        for key, tr in sorted(tracks.items()):
            i = 0
            while i < len(tr):
                j = i
                while j + 1 < len(tr) and abs(tr[j + 1][1] - tr[i][1]) <= 5 and abs(tr[j + 1][2] - tr[i][2]) <= 5 \
                        and tr[j + 1][3] > 0:
                    j += 1
                if tr[i][3] > 0 and tr[j][0] - tr[i][0] >= 180000:
                    any_stuck = True
                    out.append("  player %s %s at (%.0f, %.0f) from match %.0f s to %.0f s (leaf %s, plan %s)" % (
                        key[0], key[1], tr[i][1], tr[i][2], (tr[i][0] - enter) / 1000, (tr[j][0] - enter) / 1000,
                        tr[j][4], tr[j][5]))
                i = j + 1
        if not any_stuck:
            out.append("  none")
        # the last stats lines
        for key in ("tick", "abilities", "match", "bots", "bot missions", "wells", "death", "damage", "regen",
                    "agents", "commands", "host", "containers", "pools"):  # D1j's two lines since 2026-10-09
            if r["stats"].get(key):
                ms, text = r["stats"][key][-1]
                out.append("last %-12s (match %.0f s): %s" % (key, (ms - enter) / 1000.0, text[:900]))
        # desyncs against the peeks
        if a.peek:
            out.extend(compare_peeks(r, a.peek, enter))
    # the client's asserts
    app = os.path.join(sess, "application_log.txt")
    if os.path.exists(app):
        cnt = collections.Counter()
        first = {}
        with open(app, encoding="utf-8", errors="replace") as f:
            for line in f:
                m = re.search(r"Assert Failed:.*?File: \S*?([A-Za-z_0-9]+\.cpp)\s+Line: (\d+)(.*)$", line)
                if m:
                    key = "%s(%s)" % (m.group(1), m.group(2))
                    cnt[key] += 1
                    first.setdefault(key, line[:12] + " " + m.group(3).strip()[:160])
        for k, v in cnt.most_common():
            if k not in KNOWN_CLIENT_ASSERTS:
                out.append("client assert %s x%d, first %s" % (k, v, first[k]))
        out.append("client asserts: " + (", ".join(
            "%s x%d%s" % (k, v, "" if k in KNOWN_CLIENT_ASSERTS else " NEW") for k, v in cnt.most_common()) or "none"))
    samples = os.path.join(sess, "host_samples.csv")
    if os.path.exists(samples):
        with open(samples, encoding="utf-8-sig") as f:
            rows = list(csv.DictReader(f))
        if rows:
            pv = [float(x["private_mb"]) for x in rows if x.get("private_mb")]
            out.append("host_samples.csv: %d samples, private max %.0f MB, last %.0f MB, cpu %s s at %s s" % (
                len(rows), max(pv), pv[-1], rows[-1].get("cpu_s"), rows[-1].get("t_s")))
    out.extend(client_minutes(os.path.join(sess, "client_samples.csv"), enter_walls[0] if enter_walls else None))
    out.extend(timing_report(sess))  # Q3c: host_timing.csv, when the host wrote one
    text = "\n".join(out)
    print(text)
    return 0


def client_minutes(path, enter_wall):
    """The client's memory per match minute from run_session.ps1's client_samples.csv (Q3b): the maxima of private
    bytes, working set and 32-bit address space used, the smallest largest-free-block, the GPU counters (the process's
    dedicated and shared usage, every process's dedicated usage) and the other clients running."""
    if not os.path.exists(path):
        return []
    with open(path, encoding="utf-8-sig") as f:
        rows = list(csv.DictReader(f))
    if not rows:
        return ["client_samples.csv: no samples"]
    out = ["client per %s (client_samples.csv, %d samples; max, or min for the largest free block):" % (
        "match minute" if enter_wall is not None else "minute since the monitor's start", len(rows))]
    out.append("min  priv   ws    AS used  AS committed  free block  GPU ded  GPU shared  GPU all ded  other clients")
    minutes = collections.OrderedDict()
    day = 0.0
    prev = None
    for x in rows:
        if enter_wall is not None and x.get("wall"):
            w = wall_s(x["wall"] + " ")
            if w is None:
                continue
            if prev is not None and w + day < prev - 43200:  # past midnight
                day += 86400
            prev = w + day
            mi = int((w + day - enter_wall) // 60)
        else:
            mi = int(float(x["t_s"]) // 60)
        d = minutes.setdefault(mi, {})
        for key, col, fn in (("priv", "private_mb", max), ("ws", "working_set_mb", max), ("va", "va_used_mb", max),
                             ("com", "va_committed_mb", max), ("free", "va_free_largest_mb", min),
                             ("ded", "gpu_dedicated_mb", max), ("sh", "gpu_shared_mb", max),
                             ("all", "gpu_all_dedicated_mb", max), ("oth", "other_clients", max)):
            v = x.get(col)
            if v not in (None, ""):
                d[key] = fn(d[key], float(v)) if key in d else float(v)

    def f(d, k):
        return "%.0f" % d[k] if k in d else "-"
    for mi, d in minutes.items():
        out.append("%3d  %5s %5s  %7s  %12s  %10s  %7s  %10s  %11s  %13s" % (
            mi, f(d, "priv"), f(d, "ws"), f(d, "va"), f(d, "com"), f(d, "free"), f(d, "ded"), f(d, "sh"),
            f(d, "all"), f(d, "oth")))
    return out


TRAFFIC_KINDS = ("creates", "deltas", "forwarded", "players", "fog", "answers", "session", "other")
COMMAND_KINDS = ("move", "attack_move", "stop", "ability", "item", "rank_up", "other")
GROWTH = [  # (column or a function of a row, label): compared between the first and the last five match minutes
    ("dur_p50_ms", "tick p50 ms"), ("dur_p99_ms", "tick p99 ms"), ("late", "late ticks"),
    ("lag_max_ms", "lag max ms"), (lambda d: bytes_per_tick(d), "bytes/tick"), (lambda d: msgs_per_tick(d), "msgs/tick"),
    ("deltas_bytes", "delta bytes"), ("objects", "objects"), ("vision_objects", "vision objects"),
    ("modifier_records", "modifier records"), ("line_of_sight_in_use", "line of sight"),
    ("ability_instances", "ability instances"), ("damage_events", "damage events"), ("projectiles", "projectiles"),
    ("lua_threads", "Lua threads"), ("lua_heap_kb", "Lua heap KiB"), ("clock_answer_p99_ms", "ClockSync answer p99"),
    ("poll_wait_p99_ms", "poll wait p99"), ("answer_wire_p99_ms", "answer Send->wire p99"),
    ("raknet_queue_p99_ms", "RakNet queue p99"), ("move_answer_p50_ms", "move answer p50"),
    ("move_effect_p50_ms", "move effect p50"), ("ability_effect_p50_ms", "ability effect p50"),
]


def fnum(d, key, default=None):
    try:
        v = float(d.get(key, ""))
    except (TypeError, ValueError):
        return default
    return v


def bytes_per_tick(d):
    t = fnum(d, "ticks", 0) or 1
    return sum(fnum(d, k + "_bytes", 0) for k in TRAFFIC_KINDS) / t


def msgs_per_tick(d):
    t = fnum(d, "ticks", 0) or 1
    return sum(fnum(d, k + "_msgs", 0) for k in TRAFFIC_KINDS) / t


def read_timing_csv(path):
    """host_timing.csv's rows as dicts; a header line (\"minute,...\") may come again when the gauges changed."""
    rows, header = [], None
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            cells = line.split(",")
            if cells[0] == "minute":
                header = cells
                continue
            if header and len(cells) == len(header):
                rows.append(dict(zip(header, cells)))
    return rows


def timing_report(sess):
    """Q3c: the host's timing per match minute (host_timing.csv) and what grows over the match."""
    paths = sorted(glob.glob(os.path.join(sess, "host", "*", "host_timing.csv")) +
                   glob.glob(os.path.join(sess, "host", "host_timing.csv")))
    out = []
    for path in paths:
        rows = read_timing_csv(path)
        out.append("== %s: %d window(s)" % (os.path.relpath(path, sess), len(rows)))
        if not rows:
            continue
        out.append("min  match   ticks  tick p50/p99/max ms  late behind spikes lag max  slowest step (max ms)  "
                   "msg/s   B/s     B/tick: creates deltas fwd  players fog  answers  ClockSync ans/wire p99  "
                   "poll p99  ans wire p99  resends ping  objects vision mods  LoS  abil.inst  id serial created wraps")

        def g(d, k, fmt="%.0f"):
            v = fnum(d, k)
            return "-" if v is None or v < 0 else fmt % (int(v) if "X" in fmt else v)
        for d in rows:
            steps = [(fnum(d, k, 0), k[:-7]) for k in d if k.endswith("_max_ms") and k.startswith(("tick.", "frame."))]
            slow = max(steps) if steps else (0, "-")
            win = fnum(d, "window_s", 0) or 1
            ticks = fnum(d, "ticks", 0) or 1
            msgs = sum(fnum(d, k + "_msgs", 0) for k in TRAFFIC_KINDS)
            by = sum(fnum(d, k + "_bytes", 0) for k in TRAFFIC_KINDS)
            per = lambda k: "%.0f" % (fnum(d, k + "_bytes", 0) / ticks)  # noqa: E731
            out.append("%3s  %6s  %5s  %5s/%5s/%6s  %4s %6s %6s %7s  %-20s %5.0f  %6.0f  %7s %6s %4s %7s %4s %7s  "
                       "%11s/%-11s %8s  %12s  %7s %4s  %7s %6s %4s %4s  %9s  %9s %7s %5s" % (
                           d.get("minute"), g(d, "match_s", "%.0f"), g(d, "ticks"), g(d, "dur_p50_ms", "%.1f"),
                           g(d, "dur_p99_ms", "%.1f"), g(d, "dur_max_ms", "%.1f"), g(d, "late"), g(d, "behind"),
                           g(d, "spikes"), g(d, "lag_max_ms", "%.1f"), "%s (%.1f)" % (slow[1], slow[0]),
                           msgs / win, by / win, per("creates"), per("deltas"), per("forwarded"), per("players"),
                           per("fog"), per("answers"), g(d, "clock_answer_p99_ms", "%.1f"),
                           g(d, "clock_wire_p99_ms", "%.1f"), g(d, "poll_wait_p99_ms", "%.1f"),
                           g(d, "answer_wire_p99_ms", "%.1f"), g(d, "resends"), g(d, "ping_max_ms"), g(d, "objects"),
                           g(d, "vision_objects"), g(d, "modifier_records"), g(d, "line_of_sight_in_use"),
                           g(d, "ability_instances"), g(d, "id_serial", "0x%04X") if fnum(d, "id_serial", -1) >= 0
                           else "-", g(d, "id_created"), g(d, "id_wraps")))
        # the command latency over the whole match, per kind
        out.append("commands (ms; per-minute p50s' median, the windows' maximum):")
        for k in COMMAND_KINDS:
            n = sum(fnum(d, k + "_n", 0) for d in rows)
            if not n:
                continue
            p50s = sorted(fnum(d, k + "_answer_p50_ms", 0) for d in rows if fnum(d, k + "_n", 0) > 0)
            e50s = sorted(fnum(d, k + "_effect_p50_ms", 0) for d in rows if fnum(d, k + "_n", 0) > 0)
            out.append("  %-12s %5d  queued max %6.1f  answer p50 %6.1f max %7.1f  effect p50 %6.1f max %7.1f  "
                       "timeouts %d" % (
                           k, n, max(fnum(d, k + "_queued_max_ms", 0) for d in rows), p50s[len(p50s) // 2],
                           max(fnum(d, k + "_answer_max_ms", 0) for d in rows), e50s[len(e50s) // 2],
                           max(fnum(d, k + "_effect_max_ms", 0) for d in rows),
                           sum(fnum(d, k + "_timeouts", 0) for d in rows)))
        # what grows: the first five match minutes against the last five
        match = [d for d in rows if fnum(d, "minute", -1) >= 0]
        if len(match) >= 10:
            head, tail = match[:5], match[-5:]
            out.append("growth over the match (minutes %s-%s against %s-%s; GROWS = 1.5 x and more):" % (
                head[0]["minute"], head[-1]["minute"], tail[0]["minute"], tail[-1]["minute"]))
            for key, label in GROWTH:
                def val(d):
                    if callable(key):
                        return key(d)
                    kind = next((c for c in COMMAND_KINDS if key.startswith(c + "_")), None)
                    if kind and not fnum(d, kind + "_n", 0):  # a latency column of a minute without such commands
                        return None
                    return fnum(d, key)
                a = [v for v in (val(d) for d in head) if v is not None and v >= 0]
                b = [v for v in (val(d) for d in tail) if v is not None and v >= 0]
                if not a or not b:
                    continue
                ma, mb = sum(a) / len(a), sum(b) / len(b)
                grows = mb > 1.5 * ma and mb - ma > 1.0
                out.append("  %-24s %10.1f -> %10.1f  %s" % (label, ma, mb, "GROWS" if grows else "flat" if
                                                               mb <= 1.5 * ma else "small"))
        else:
            out.append("growth: fewer than 10 match minutes")
    return out


def compare_peeks(r, peek_dir, enter):
    """The client's networked objects (game_peek.py) against what the host created for the player and not deleted."""
    out = ["desyncs (client objects from game_peek against the host's creates and deletes for the player):"]
    files = sorted(glob.glob(os.path.join(peek_dir, "peek_*.txt")), key=os.path.getmtime)
    if not files:
        return out + ["  no peek files"]
    creatures = r["creatures"]
    prev_extra = set()
    intervals = []  # (server id, noun, created at, deleted at) in wall seconds, for the player
    for sid, cr in r["creates"].items():
        dels = sorted(r["deletes"].get(sid, []))
        for ms, noun in cr:
            d = next((x for x in dels if x >= ms), None)
            cw = ms_to_wall(r, ms)
            dw = ms_to_wall(r, d) if d is not None else 1e12
            if cw is not None:
                intervals.append((sid, noun, cw, dw if dw is not None else 1e12))
    for fn in files:
        with open(fn, encoding="utf-8", errors="replace") as f:
            lines = f.read().splitlines()
        if not lines or not lines[0].startswith("# wall "):
            continue
        hh, mm, ss = lines[0].split()[2].split(":")
        wall = int(hh) * 3600 + int(mm) * 60 + float(ss)
        if r["wall_at"] and wall < r["wall_at"][0][1] - 43200:  # a peek after midnight
            wall += 86400
        client, rows = {}, {}
        for line in lines[1:]:
            m = re.search(r"server 0x([0-9a-fA-F]{8})(?: \(UNMAPPED\))? +(\S+) +pos \( *([-\d.]+), *([-\d.]+),.*?"
                          r"flags 0x([0-9a-f]+).*? hp (\S+?)/", line)
            if m:
                sid = m.group(1).upper()
                client[sid] = m.group(2)
                try:
                    hp = float(m.group(6))
                except ValueError:
                    hp = -1.0
                rows[sid] = (float(m.group(3)), float(m.group(4)), int(m.group(5), 16), hp)
        if not client:
            out.append("  %s: no objects read (%s)" % (os.path.basename(fn), (lines[1] if len(lines) > 1 else "")[:80]))
            continue
        # the host's view at that wall time
        alive = {sid: noun for sid, noun, cw, dw in intervals if cw <= wall - 1.0 and dw > wall + 1.0}
        client.pop("00000000", None)
        missing = sorted(set(alive) - set(client))
        extra = sorted(set(client) - set(alive))
        # an object the host deleted and the client still holds at the next peek too is a leak, not timing
        kept = sorted(set(extra) & prev_extra)
        prev_extra = set(extra)
        out.append("  %s (match %.0f s): client %d, host %d, missing on the client %d%s, extra on the client %d "
                   "(still extra from the last peek: %d)%s" % (
                       os.path.basename(fn), wall - (ms_to_wall(r, enter) or wall), len(client), len(alive),
                       len(missing), (" " + ", ".join("%s %s" % (s, alive[s]) for s in missing[:6])) if missing else "",
                       len(extra), len(kept),
                       (" " + ", ".join("%s %s" % (s, client[s]) for s in (kept or extra)[:6])) if extra else ""))
        # the players' creatures and the Guardians: the client's (unfogged) position and HP against the host's
        # stats line nearest in time (stats bots and stats match come every 10 s, so moving bots differ by dt)
        near = None
        for ms, text in r["stats"].get("bots", []):
            w = ms_to_wall(r, ms)
            if w is not None and (near is None or abs(w - wall) < abs(near[0] - wall)):
                near = (w, text)
        if near:
            notes = []
            for b in re.finditer(r"\[(\d+) (\S+?)\.BotClass t\d lane \d \((-?\d+), (-?\d+)\) hp (-?\d+)", near[1]):
                cid = creatures.get(b.group(1))
                if cid and cid in rows and not rows[cid][2] & 1:
                    cx, cy, _, chp = rows[cid]
                    hx, hy, hhp = float(b.group(3)), float(b.group(4)), float(b.group(5))
                    notes.append("%s d%.0f hp %.0f/%.0f" % (b.group(2)[3:], ((cx - hx) ** 2 + (cy - hy) ** 2) ** 0.5,
                                                           chp, hhp))
            out.append("    bots in view vs the host %+.1f s away (distance, client/host HP): %s" % (
                near[0] - wall, "; ".join(notes) or "none in view"))
        gm = None
        for ms, text in r["stats"].get("match", []):
            w = ms_to_wall(r, ms)
            if w is not None and (gm is None or abs(w - wall) < abs(gm[0] - wall)):
                gm = (w, text)
        if gm:
            g = []
            for sid, team, hp in re.findall(r"([0-9A-F]{8}) team (\d) hp (-?[\d.]+)", gm[1]):
                if sid in rows:
                    g.append("team %s %.0f/%.0f" % (team, rows[sid][3], float(hp)))
            out.append("    Guardians client/host (%+.1f s): %s" % (gm[0] - wall, ", ".join(g) or "-"))
    return out


def main(argv):
    p = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    sub = p.add_subparsers(dest="cmd", required=True)
    c = sub.add_parser("commands")
    c.add_argument("--end", type=int, default=1800)
    c.add_argument("--out")
    e = sub.add_parser("edges")
    e.add_argument("--end", type=int, default=1800)
    e.add_argument("--first", type=float, default=90)
    e.add_argument("--every", type=float, default=300)
    e.add_argument("--other-human", type=int, default=None)
    e.add_argument("--no-xp", action="store_true")
    e.add_argument("--out")
    tm = sub.add_parser("timing")
    tm.add_argument("session")
    k = sub.add_parser("peek")
    k.add_argument("out_dir")
    k.add_argument("--every", type=float, default=60)
    k.add_argument("--exe-part", default="client\\run_854_b\\")
    k.add_argument("--until", type=float, default=4 * 3600)
    rp = sub.add_parser("report")
    rp.add_argument("session")
    rp.add_argument("--peek")
    a = p.parse_args(argv)
    if a.cmd in ("commands", "edges"):
        return cmd_commands(a)
    if a.cmd == "peek":
        return cmd_peek(a)
    if a.cmd == "timing":
        text = "\n".join(timing_report(a.session))
        print(text)
        return 0 if text else 2
    return report(a)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
