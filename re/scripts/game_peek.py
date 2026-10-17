"""Reads the client simulator's objects out of a running 32-bit Dawngate.exe (D2e): what the GMS created.

A read-only evidence tool for live sessions in which the GMS creates objects (docs/GMS_PROTOCOL.md *Replication
(D2d)*, *The level's objects on the live client (D2e)*). It walks, with ReadProcessMemory only:
  - cGameClient (global 0x019768C0) +8 = the client cGameSimulator;
  - the simulator's object pool: sim +0xD4 = object manager, +4 = cObjectPool {+0 data, +0x14 slots used so far
    (the iteration bound), +0x18 stride, +0x1C capacity, +0x20 name; ObjectPool_Init 0x00A81C70: 0x2000 objects
    on a client simulator, 0x400 on a server one}; slot i is live when (obj+0 & 0xFFFF) == i
    (ObjectPool_SeekNextValidEntry 0x00A81700);
  - the server -> client id map of SporeOnline_Common.cpp (buckets pointer 0x013F5DD4, bucket count 0x013F5DD8,
    nodes {u32 server id, u32 client id, next}, sentinel bucket[count]; GameClient_MapServerObjectId 0x006B78E0);
  - the players (mPlayers: sim +0x1A814 buckets, +0x1A818 count; nodes {u8 index, u32 handle, next}; the handle
    is a slot of the player pool at sim +0xD0); cLabsPlayer +4 noun def, +0x0C the player's creature (client id),
    +0x10 mStatus, +0x2C index, +0xA0 team, and (D1g) the death timer mDeathTime/mRespawnTime (+0x270/+0x278, u64 on
    the client's clock: the reader 0x008B43E0 translates the server's) against the client clock (sim +0x1CFD8 -> game
    timer, +8 u64 ms; GameTimeManager_GetTimeMs 0x008F98A0): "dead, respawn in N s" while mRespawnTime is ahead, as
    the client's panels decide it (0x005326F0, 0x00433AD0); since D1h also the counters the host sends: mKills/
    mDeaths/mAssists (+0x240/+0x244/+0x248, A9 members 3-5), the creep score (+0x24C, AA member 3), XP (+0xA4), total
    gold (+0xAC) and level (+0xB0); since D7b the available gold (+0xA8), spell points (+0x20) and the inventory
    (mItems[11] at +0xB4, 0x24 bytes each: asset, count +0x18), and the player creature's Power and HealthRegen;
    since WARDSKIN the vanity slots (+0x314 ward skin, +0x318 announcer pack; 0 = the default);
    since D14b-live any object's crowd control when set ("cc imm N immNoRot N sup N": Immobilized, ImmobilizedNoRotate,
    Suppressed);
  - (D1h) the death recap the client got (DeathRecap C9, GameClient_OnGmsDeathRecap 0x006B6200 -> the UI object
    [0x0148DDB4] + 0x20: +0x10C the newest cDeathRecapData not yet shown, +0x108 the one shown; each {+0 buffer, +4
    header}): the 25-byte header, the three type lists and the timeline (docs/GMS_PROTOCOL.md *The death recap (D1h)*).
For every networked object (network component +0x8BC) it prints the client id, the server id (component +8), the
noun name (noun +0), position (+0x10), team (+0x4D), client flags (+0x5A: 1 disabled/fogged, 2 preparing for
enable), visible (+0x54), whether the model exists (mpGraphicsData +0x890, made by ObjectCreateComplete through
GameGraphics_CreateObjectGraphics 0x00575850), HP (cCombatantData* +0x8EC, mHitPoints +0x48) and MaxHealth
(cAttributeData* +0x8E0, +0x10), since D1i "res": mResourcePoints (combatant +0x58) of MaxResourcePoints
(attribute data +0x14) when the object has a resource pool, since D2g-live "mods": the modifier instances in the
object's agent summary (+0x8C8; ModifierCreated adds them, ModifierDeleted and ObjectExitFoW remove them) and
"fx": the used attached-effect slots (+0x90 + 0x38 i, asset +0x10; AttachedEffectUpdate 92), "bar" when a health bar exists (combatant +0x38,
GameplayUI_CreateHealthBar 0x004A5BC0) and "player-controlled" (+0x56). It also prints the camera globals
SPGearsCamera_SetFollowObject (0x006AE1A0) writes when the camera snaps to an object (0x013F5724 anchor,
0x013F5730 followed position).

Since X2a a "tower graphics" line: the client's per-tower slot stride (0x01542889, a copy of the UI data's
mNumTowersPerLane taken at EnterGame; 3 on SM_Map_v5, 0 when the full UIDataState came after EnterGame) and the 16
slot states, so a live check sees whether a destroyed Binding keeps replaying its death (docs/ASSERTS.md *X2a*).

Since D8-live `--postgame [catalogID ...]` prints instead what the post-game left in the client, in any game state: the
account data's FLAG (bit 0, set by C1 04's FTUE byte), the inventory counts of the given catalogIDs (C1 04's items), and
the stats snapshot the client took at C1 01 (the records the game-end screen and the After Action Review show).

Usage (64-bit Python from .venv; the game runs as the same user, no admin needed):
    .venv/Scripts/python -I re/scripts/game_peek.py <pid> [--all] [--vision] [--out FILE]
    .venv/Scripts/python -I re/scripts/game_peek.py <pid> --postgame [0x10A4638F 0x10A46383] [--out FILE]
  --all also lists the objects without a network component (the client's own level objects; ~7,000 lines).
  --vision (D2g-rest) adds the vision fields: vis = mVisibleToTeam, vr = mVisionRadius, grass s<server id>/c<the
  client's own id>@<server cell>, STEALTHED (mStealthed), IN-GRASS (mbInGrass).
Since CAMPICONS a "camps" line: the client's camp list as 8D left it (each camp's spawn marker id and its
mCampVisibleToTeam byte), so a live check reads which camp icons the minimap draws for the client's team.
Exit code: 0 ok, 2 process not readable or no client simulator yet.
"""
import argparse
import ctypes
import ctypes.wintypes as wt
import struct
import sys

GAME_CLIENT = 0x019768C0
ID_MAP_BUCKETS, ID_MAP_COUNT = 0x013F5DD4, 0x013F5DD8
# D7b: the voice lines' ClientFeedbackDefs (GMS_PROTOCOL.md *Item transactions, voice lines and emotes (D7b)*)
FEEDBACK_NAMES = ['PlayerCommCharge', 'PlayerCommCare', 'PlayerCommNo', 'PlayerCommSorry', 'PlayerCommThankYou',
                  'PlayerCommYes', 'PlayerCommCompliment', 'PlayerCommLaugh', 'PlayerCommTaunt', 'PlayerCommJoke',
                  'PlayerCommFlirt', 'PlayerCommMissing', 'PlayerCommDance',
                  'MineUnderAttack']  # the wells' alert: D9b the client's own (a worker hit, keyed to its well)


def fnv1(text):
    h = 0x811C9DC5
    for c in text.encode('latin-1'):
        h = (h * 0x01000193) & 0xFFFFFFFF
        h ^= c
    return h

k32 = ctypes.WinDLL('kernel32', use_last_error=True)
PROCESS_VM_READ, PROCESS_QUERY_INFORMATION = 0x10, 0x400
k32.OpenProcess.restype = wt.HANDLE
k32.ReadProcessMemory.argtypes = [wt.HANDLE, ctypes.c_void_p, ctypes.c_void_p, ctypes.c_size_t,
                                  ctypes.POINTER(ctypes.c_size_t)]


class Proc:
    def __init__(self, pid):
        self.h = k32.OpenProcess(PROCESS_VM_READ | PROCESS_QUERY_INFORMATION, False, pid)
        if not self.h:
            raise OSError(f'OpenProcess({pid}) failed: {ctypes.get_last_error()}')

    def read(self, addr, n):
        buf = ctypes.create_string_buffer(n)
        got = ctypes.c_size_t()
        if not addr or not k32.ReadProcessMemory(self.h, ctypes.c_void_p(addr), buf, n, ctypes.byref(got)):
            return None
        return buf.raw[:got.value]

    def u32(self, addr):
        b = self.read(addr, 4)
        return struct.unpack('<I', b)[0] if b and len(b) == 4 else 0

    def u16(self, addr):
        b = self.read(addr, 2)
        return struct.unpack('<H', b)[0] if b and len(b) == 2 else 0

    def u8(self, addr):
        b = self.read(addr, 1)
        return b[0] if b else 0

    def f32(self, addr):
        b = self.read(addr, 4)
        return struct.unpack('<f', b)[0] if b and len(b) == 4 else float('nan')

    def cstr(self, addr, n=96):
        b = self.read(addr, n)
        return b.split(b'\0', 1)[0].decode('latin-1') if b else '?'


def hash_map(p, buckets_ptr, count, key_size=4, limit=100000):
    """Walk a Spore-style hash map: buckets[count] of node chains {key, value, next}; returns [(key, value)]."""
    out = []
    if not buckets_ptr or not count or count > 1 << 20:
        return out
    raw = p.read(buckets_ptr, 4 * count)
    if not raw:
        return out
    for (node,) in struct.iter_unpack('<I', raw):
        while node and len(out) < limit:
            key = p.u8(node) if key_size == 1 else p.u32(node)
            out.append((key, p.u32(node + 4)))
            node = p.u32(node + 8)
    return out


def describe_recap(p, data):
    """A cDeathRecapData {+0 buffer, +4 header, +8/+0xC/+0x10 type lists, +0x14 timeline} as text."""
    buf = p.u32(data)
    head = p.read(buf, 25) if buf else None
    if not head or len(head) < 25:
        return f'0x{data:08x}: buffer not readable'
    total, secs, rows, phys, mag, tru, n0, n1, n2, nt = struct.unpack('<ffBfffBBBB', head)
    out = [f'0x{data:08x}: total {total:.2f} in {secs:.1f} s, {rows} row(s), physical {phys:.2f}, magical {mag:.2f}, '
           f'true {tru:.2f}, type lists {n0}/{n1}/{n2}, timeline {nt}']
    pos = buf + 25
    for name, n in (('physical', n0), ('magical', n1), ('true', n2)):
        for _ in range(n):
            noun, npc, team, dmg = struct.unpack('<IiBf', p.read(pos, 13) or bytes(13))
            out.append(f'    {name}: noun 0x{noun:08x} npc type {npc} team {team} damage {dmg:.2f}')
            pos += 13
    for _ in range(nt):
        noun, npc, team, dmg, ability, hits = struct.unpack('<IiBfIB', p.read(pos, 18) or bytes(18))
        out.append(f'    timeline: noun 0x{noun:08x} npc type {npc} team {team} damage {dmg:.2f} ability '
                   f'0x{ability:08x} x{hits}')
        pos += 18
    return '\n'.join(out)


def ea_string(p, addr):
    """An EASTL string {begin, end, capacity} at addr."""
    b, e = p.u32(addr), p.u32(addr + 4)
    if not b or e < b or e - b > 4096:
        return ''
    return (p.read(b, e - b) or b'').decode('latin-1')


def rb_tree(p, root, limit=20000):
    """An EASTL rb-tree's nodes {+0 right, +4 left, +8 parent, +0xC colour, +0x10 key, +0x14 value}, in order."""
    out, stack, node = [], [], root
    while (stack or node) and len(out) < limit:
        while node:
            stack.append(node)
            node = p.u32(node + 4)
        node = stack.pop()
        out.append((p.u32(node + 0x10), p.u32(node + 0x14)))
        node = p.u32(node)
    return out


def postgame(p, say, items):
    """D8-live (MATCH_FLOW.md *Match end and post-game (D8-static)*): what the post-game left in the client.
      - the account data: platform [0x01BB6154] (SporeOnline_GetPlatform 0x00AD3800) -> vtable +0x78 -> the metagame
        manager, +0x24 the account data (GameOverState_OnRewards 0x00543A80 reads it this way), +0xD10 FLAG (bit 0 =
        FTUE done, set by C1 04's byte +0x74), +0x64 the inventory map (EASTL rb-tree, root at +0x70, nodes {key
        catalogID +0x10, count +0x14}; MetagameManager_AddInventoryItem 0x00AD2030 -> 0x00AD08E0 adds C1 04's items);
      - the stats snapshot GameOverState_BuildPostGameStats (0x005441C0) takes at C1 01: the local team's 0x90-byte
        records (vector {begin, end} at 0x013E0338), the enemy team's (0x013E0620), the victory flag (0x0148DDB0: 1
        victory, 2 defeat), matchmade (0x0148DDB1) and the duration text (EASTL string at 0x013E0908)."""
    plat = p.u32(0x01BB6154)
    acct = 0
    if plat:
        fn = p.u32(p.u32(plat) + 0x78)
        code = p.read(fn, 8) or b''
        mgr = 0
        if code[:2] == b'\x8b\x41' and code[3:4] == b'\xc3':      # mov eax, [ecx + disp8]; ret
            mgr = p.u32(plat + code[2])
        elif code[:2] == b'\x8b\x81' and code[6:7] == b'\xc3':    # mov eax, [ecx + disp32]; ret
            mgr = p.u32(plat + struct.unpack('<I', code[2:6])[0])
        else:
            say(f'platform 0x{plat:08x}: vtable +0x78 = 0x{fn:08x} ({code.hex()}): not a field getter; no account data')
        acct = p.u32(mgr + 0x24) if mgr else 0
        say(f'platform 0x{plat:08x}, metagame manager 0x{mgr:08x}, account data 0x{acct:08x}')
    if acct:
        flag = p.u32(acct + 0xD10)
        inv = rb_tree(p, p.u32(acct + 0x70))
        say(f'account FLAG (+0xD10) 0x{flag:x}: FTUE done (bit 0) {"yes" if flag & 1 else "no"}; inventory {len(inv)} '
            f'entries')
        have = dict(inv)
        for cid in items:
            say(f'  inventory 0x{cid:08x}: {have.get(cid, 0)}')
    else:
        say('no account data (not logged in?)')
    say(f'post-game snapshot: victory flag (0x0148DDB0) {p.u8(0x0148DDB0)} (1 victory, 2 defeat), matchmade '
        f'(0x0148DDB1) {p.u8(0x0148DDB1)}, duration "{ea_string(p, 0x013E0908)}"')
    for label, vec in (('my team', 0x013E0338), ('enemy team', 0x013E0620)):
        b, e = p.u32(vec), p.u32(vec + 4)
        n = (e - b) // 0x90 if b and e >= b else 0
        say(f'  {label} (0x{vec:08x}): {n} record(s)')
        for i in range(min(n, 10)):
            r = b + 0x90 * i
            raw = p.read(r, 0x90) or bytes(0x90)
            oid = struct.unpack_from('<Q', raw, 0x10)[0]
            slots = struct.unpack_from('<11I', raw, 0x18)
            counts = struct.unpack_from('<11I', raw, 0x44)
            shaper, level, k, d, a_, cs, gold, role = struct.unpack_from('<8I', raw, 0x70)
            inv = ', '.join(f'0x{s_:08x} x{c}' for s_, c in zip(slots, counts) if s_)
            say(f'    "{ea_string(p, r)}" online id {oid}: shaper 0x{shaper:08x}, level {level}, K/D/A {k}/{d}/{a_}, '
                f'creeps {cs}, gold {gold}, role {role}, items [{inv}]')


def main():
    ap = argparse.ArgumentParser(description=__doc__.split('\n')[0])
    ap.add_argument('pid', type=int)
    ap.add_argument('--all', action='store_true')
    ap.add_argument('--vision', action='store_true')
    ap.add_argument('--out')
    ap.add_argument('--postgame', nargs='*', metavar='CATALOGID',
                    help='D8-live: the account FLAG, the inventory counts of these catalogIDs and the post-game '
                         'snapshot instead of the objects (works in every game state)')
    a = ap.parse_args()
    lines = []
    say = lines.append
    try:
        p = Proc(a.pid)
    except OSError as e:
        print(e)
        return 2
    if a.postgame is not None:
        postgame(p, say, [int(x, 0) for x in a.postgame])
        text = '\n'.join(lines)
        print(text)
        if a.out:
            with open(a.out, 'w', encoding='utf-8') as f:
                f.write(text + '\n')
        return 0
    gc = p.u32(GAME_CLIENT)
    sim = p.u32(gc + 8) if gc else 0
    if not sim:
        print(f'no client simulator (cGameClient 0x{gc:08x})')
        return 2
    om = p.u32(sim + 0xD4)
    pool = p.u32(om + 4)
    data, maxc, stride, cap = p.u32(pool), p.u32(pool + 0x14), p.u32(pool + 0x18), p.u32(pool + 0x1C)
    say(f'client sim 0x{sim:08x}, object manager 0x{om:08x}, pool 0x{pool:08x} "{p.cstr(p.u32(pool + 0x20))}" '
        f'data 0x{data:08x} slots used {maxc} of {cap}, stride 0x{stride:x}')

    idmap = hash_map(p, p.u32(ID_MAP_BUCKETS), p.u32(ID_MAP_COUNT))
    by_client = {c: s for s, c in idmap}
    say(f'id map (server -> client): {len(idmap)} entr{"y" if len(idmap) == 1 else "ies"}')

    # the client clock (GameTimeManager_GetTimeMs 0x008F98A0: sim +0x1CFD8 -> game timer, +8 u64 ms)
    timer = p.u32(sim + 0x1CFD8)
    clock = struct.unpack('<Q', p.read(timer + 8, 8) or bytes(8))[0] if timer else 0
    say(f'client clock {clock} ms')

    # mPlayers values are handles into the player pool at sim +0xD0 (0x008EE5B0, then ObjectPool_TryToGet)
    ppool = p.u32(sim + 0xD0)
    pdata, pmax, pstride = p.u32(ppool), p.u32(ppool + 0x14), p.u32(ppool + 0x18)
    for idx, h in hash_map(p, p.u32(sim + 0x1A814), p.u32(sim + 0x1A818), key_size=1):
        pl = pdata + pstride * (h & 0xFFFF) if (h & 0xFFFF) < pmax else 0
        if not pl or p.u32(pl) != h:
            say(f'player {idx}: handle 0x{h:08x} not in the player pool')
            continue
        noun = p.u32(pl + 4)
        death, respawn = struct.unpack('<QQ', p.read(pl + 0x270, 16) or bytes(16))
        if respawn and respawn != 0xFFFFFFFFFFFFFFFF and clock and respawn > clock:
            state = f'dead, respawn in {(respawn - clock) / 1000:.1f} s of {(respawn - death) / 1000:.1f} s'
        else:
            state = 'alive' if respawn else 'never died'
        say(f'player {idx}: handle 0x{h:08x} at 0x{pl:08x}, creature (+0x0C) 0x{p.u32(pl + 0x0C):08x}, '
            f'status (+0x10) 0x{p.u32(pl + 0x10):x}, index (+0x2C) {p.u8(pl + 0x2C)}, team (+0xA0) {p.u8(pl + 0xA0)}, '
            f'noun (+4) {p.cstr(p.u32(noun)) if noun else "-"}, mDeathTime {death}, mRespawnTime {respawn} ({state})')
        # BOTPROFILE: mRole (+0x08: 0 gladiator, 1 tactician, 2 hunter, 3 predator; what the scoreboard, target panel
        # and shaper panel read through LabsPlayer_GetRole 0x00912180), mbIsBot (+0x48), mpBotData (+0x4C, the BotClass
        # asset LabsPlayer_ReflectBotData 0x00915D30 set) and the name (+0x50, the bot's "<shaper> BOT")
        bot_data = p.u32(pl + 0x4C)
        name_ptr = p.u32(pl + 0x50)
        say(f'  role (+0x08) {p.u8(pl + 0x08)}, bot (+0x48) {p.u8(pl + 0x48)}, bot data (+0x4C) '
            f'{p.cstr(p.u32(bot_data)) if bot_data else "-"}, name (+0x50) {p.cstr(name_ptr) if name_ptr else "-"!r}')
        k, d, a_, cs = struct.unpack('<4I', p.read(pl + 0x240, 16) or bytes(16))
        say(f'  K/D/A {k}/{d}/{a_}, creeps {cs}, multi-kill (+0x250) {p.u32(pl + 0x250)}, streak (+0x260) '
            f'{p.u32(pl + 0x260)}, XP (+0xA4) {p.f32(pl + 0xA4):.2f}, total gold (+0xAC) {p.f32(pl + 0xAC):.2f}, '
            f'level (+0xB0) {p.u32(pl + 0xB0)}')
        # D7b: the shop's side: mAvailableGold (+0xA8), mSpellPoints (+0x20) and mItems[11] (+0xB4, 0x24 each: asset +0,
        # count +0x18), what the AA delta and the ItemTransaction answer (C3) change
        inv = []
        for s in range(11):
            item = pl + 0xB4 + s * 0x24
            asset = p.u32(item)
            if asset:
                inv.append(f'{s}: {p.cstr(p.u32(asset))} x{p.u32(item + 0x18)}')
        say(f'  available gold (+0xA8) {p.f32(pl + 0xA8):.2f}, spell points (+0x20) {p.u32(pl + 0x20)}, items '
            f'[{", ".join(inv) if inv else "none"}]')
        # WARDSKIN: the vanity slots (+0x314 ward skin Noun, +0x318 AnnouncerPack; LabsPlayer_SetVanityAsset 0x009113F0,
        # set by shaper select's C8 0E echo and OnLocalPlayerReconnect 0x8A; 0 = the kind's default)
        van = [p.u32(pl + 0x314), p.u32(pl + 0x318)]
        say('  vanity (+0x314): ward ' + (p.cstr(p.u32(van[0])) if van[0] else 'default') + ', announcer ' +
            (p.cstr(p.u32(van[1])) if van[1] else 'default'))

    # X2a: the tower graphics' slot stride (TowerGraphics_Init 0x00539310 copies mNumTowersPerLane, cBattlegroundsUIData
    # at sim +0xC930 + 0xD0, at EnterGame; 0 makes all four lane groups share slots) and the per-slot states that
    # TowerGraphics_Update (0x00538D50) compares (0 standing, 1 destroyed, 2/3 respawning): docs/ASSERTS.md *X2a*
    slot_states = struct.unpack('<16I', p.read(0x01542840, 64) or bytes(64))
    say(f'tower graphics: towers per lane {p.u8(0x01542889)} (the UI data has {p.u32(sim + 0xC930 + 0xD0)}), '
        f'slot states {list(slot_states)}')

    # CAMPICONS: the client's camp list (cBattlegroundsUIData at sim +0xC930: +0x368 entries of 0x18 bytes, +0x36C
    # count; +0 mCampVisibleToTeam, +4 mSpawnMarkerId), what 8D wrote: the minimap draws a camp's icon only while its
    # byte has the local team's bit (GGMinimap_UpdateObjectIcon 0x005801F0)
    camps, ncamps = p.u32(sim + 0xC930 + 0x368), p.u32(sim + 0xC930 + 0x36C)
    if camps and 0 < ncamps <= 64:
        parts = []
        for i in range(ncamps):
            raw = p.read(camps + i * 0x18, 8) or bytes(8)
            bits, marker = raw[0], struct.unpack('<I', raw[4:8])[0]
            if marker:
                parts.append(f'{marker}:0x{bits:02x}')
        say(f'camps (8D, mSpawnMarkerId:mCampVisibleToTeam; bit 0x02 Mortal, 0x04 Spirit): {" ".join(parts)}')
    else:
        say(f'camps: no camp list (0x{camps:08x}, count {ncamps})')

    ui = p.u32(0x0148DDB4)
    if ui:
        for label, off in (('not yet shown (+0x10C)', 0x10C), ('shown (+0x108)', 0x108)):
            rec = p.u32(ui + 0x20 + off)  # (D8-live: was `data`, which overwrote the object pool's pointer below)
            say(f'death recap {label}: ' + (describe_recap(p, rec) if rec else 'none'))
    else:
        say('death recap: no UI object at 0x0148DDB4')

    # D7b: the client feedback system (ClientFeedback.cpp; global 0x01974FBC): 0x01974FB8 counts the feedback instances
    # it created (FUN_00667fa0), and per channel (+0x1E44 + 0x20 x channel) a hash map {+4 buckets, +8 count} of the
    # cooldowns it recorded: nodes {u32 eventId, u32 objectId (0 unless cooldownPerObject), u64 until (client ms), next}.
    # A ClientFeedbackEvent (A5) the client played shows up here with its def's asset id (PlayerComm* = voice lines).
    fb = p.u32(0x01974FBC)
    names = {fnv1(n.lower() + '.clientfeedbackdef'): n for n in FEEDBACK_NAMES}
    cds = []
    if fb:
        for ch in (0, 1):
            m = fb + 0x1E44 + 0x20 * ch
            buckets, count = p.u32(m + 4), p.u32(m + 8)
            if not buckets or not count or count > 1 << 16:
                continue
            for b in range(count):
                node = p.u32(buckets + 4 * b)
                for _ in range(64):
                    if not node:
                        break
                    ev, obj, until = struct.unpack('<IIQ', p.read(node, 16) or bytes(16))
                    cds.append(f'{names.get(ev, f"0x{ev:08x}")} obj 0x{obj:08x} until {until}')
                    node = p.u32(node + 0x10)
    say(f'client feedback: {p.u32(0x01974FB8)} instance(s) created; cooldowns [{"; ".join(cds) if cds else "none"}]')

    # Camera globals written by SPGearsCamera_SetFollowObject (0x006AE1A0) when it (re)targets: the snap anchor
    # 0x013F5724, the followed object's position 0x013F5730 and that plus the camera offset 0x013F5744.
    cam = [struct.unpack('<3f', p.read(a, 12) or bytes(12)) for a in (0x013F5724, 0x013F5730, 0x013F5744)]
    say('camera globals: anchor ({:.2f},{:.2f},{:.2f}), follow pos ({:.2f},{:.2f},{:.2f}), '
        'eye target ({:.2f},{:.2f},{:.2f})'.format(*cam[0], *cam[1], *cam[2]))

    blob = p.read(data, maxc * stride) if data and stride and maxc < 1 << 16 else None
    if not blob and data and stride and maxc < 1 << 16:
        # a page of the pool not readable: slot by slot, unreadable slots as zeros (never live)
        blob = b''.join(p.read(data + i * stride, stride) or bytes(stride) for i in range(maxc))
    if not blob:
        say('object pool not readable')
        print('\n'.join(lines))
        return 2
    live = net = 0
    rows = []
    for i in range(maxc):
        oid = struct.unpack_from('<I', blob, i * stride)[0]
        if not oid or (oid & 0xFFFF) != i:
            continue
        live += 1
        obj = data + i * stride
        nc = struct.unpack_from('<I', blob, i * stride + 0x8BC)[0] if stride > 0x8C0 else 0
        if nc:
            net += 1
        if not nc and not a.all:
            continue
        noun = struct.unpack_from('<I', blob, i * stride + 4)[0]
        name = p.cstr(p.u32(noun)) if noun else '-'
        x, y, z = struct.unpack_from('<3f', blob, i * stride + 0x10)
        team = blob[i * stride + 0x4D]
        visible = blob[i * stride + 0x54]
        flags = struct.unpack_from('<H', blob, i * stride + 0x5A)[0]
        gfx = struct.unpack_from('<I', blob, i * stride + 0x890)[0]
        comb = struct.unpack_from('<I', blob, i * stride + 0x8EC)[0]
        attr = struct.unpack_from('<I', blob, i * stride + 0x8E0)[0]
        hp = f'{p.f32(comb + 0x48):.0f}' if comb else '-'
        bar = ' bar' if comb and p.u32(comb + 0x38) else ''   # health bar handle (GameplayUI_CreateHealthBar)
        # D7b: the player's creature also shows Power (attribute data +8, index 0) and HealthRegen (+0x18), which items
        # change
        pctl = (f' player-controlled power {p.f32(attr + 8):.1f} regen {p.f32(attr + 0x18):.1f}' if attr else
                ' player-controlled') if blob[i * stride + 0x56] else ''
        # D14b-live: crowd control as the client holds it (attribute data +8 + 4 i: Immobilized 43 +0xB4,
        # ImmobilizedNoRotate 44 +0xB8, Suppressed 50 +0xD0; the 9F/A0 deltas set them), printed when one is set
        if attr:
            imm, inr, sup = p.f32(attr + 0xB4), p.f32(attr + 0xB8), p.f32(attr + 0xD0)
            if imm or inr or sup:
                pctl += f' cc imm {imm:.0f} immNoRot {inr:.0f} sup {sup:.0f}'
        mx = f'{p.f32(attr + 0x10):.0f}' if attr else '-'
        # D1i: the resource (mResourcePoints, combatant +0x58) of an object with a resource pool (MaxResourcePoints,
        # attribute data +0x14)
        mres = p.f32(attr + 0x14) if attr else 0.0
        res = f' res {p.f32(comb + 0x58):.0f}/{mres:.0f}' if comb and mres > 0 else ''
        sid = p.u32(nc + 8) if nc else 0
        mapped = by_client.get(oid)
        # D2g-live: the agent summary's modifiers (object +0x8C8: +0xC buckets, +0x10 count; nodes {u32 instance, u32
        # pool handle, next}; AgentSummary_OnModifierCreated 0x00658480 adds, _OnModifierDeleted 0x00657F40 erases on AD)
        summ = struct.unpack_from('<I', blob, i * stride + 0x8C8)[0] if stride > 0x8CC else 0
        mods = ''
        if summ and nc:
            inst = sorted(k for k, _ in hash_map(p, p.u32(summ + 0xC), p.u32(summ + 0x10)))
            if inst:
                mods = ' mods [' + ' '.join(f'{k:08x}' for k in inst) + ']'
        # D2g-live step 3: the used attached-effect slots (+0x90 + 0x38 i, asset at +0x10; AttachedEffectUpdate 92 and
        # the attach ServerEvents fill them)
        fx = [k for k in range(16) if struct.unpack_from('<I', blob, i * stride + 0x90 + 0x38 * k + 0x10)[0]]
        fxs = f' fx {len(fx)}' if fx else ''
        # D2g-rest (--vision): the vision fields the host writes (sporelabsObject members 0-3: mVisibleToTeam +0x888, the
        # server's grass id +0x87C and cell +0x880, mVisionRadius +0x884), the client's own grass id (+0x878, the fog
        # renderer's LineOfSight_UpdateGrassId), and the blackboard's mStealthed/mbInGrass (+0x8C4 -> +0x1E74/+0x1E75, A2)
        vis = ''
        if a.vision and stride > 0x88C:
            vt = blob[i * stride + 0x888]
            cg, sg, sxy = struct.unpack_from('<3I', blob, i * stride + 0x878)
            vr, = struct.unpack_from('<f', blob, i * stride + 0x884)
            bb = struct.unpack_from('<I', blob, i * stride + 0x8C4)[0]
            st = p.read(bb + 0x1E74, 2) if bb else None
            vis = (f' vis {vt}' + (f' vr {vr:.1f}' if vr else '')
                   + (f' grass s{sg:08x}/c{cg:08x}@{sxy:08x}' if sg or cg else '')
                   + (' STEALTHED' if st and st[0] else '') + (' IN-GRASS' if st and st[1] else ''))
        rows.append(f'  0x{oid:08x} server 0x{sid:08x}{"" if not nc or mapped == sid else " (UNMAPPED)"} '
                    f'{name:40s} pos ({x:8.2f},{y:8.2f},{z:6.2f}) team {team} flags 0x{flags:x} '
                    f'visible {visible} model {"yes" if gfx else "no"} hp {hp}/{mx}{res}{bar}{pctl}{fxs}{mods}{vis}')
    say(f'objects: {live} live, {net} with a network component')
    lines += rows
    text = '\n'.join(lines)
    print(text)
    if a.out:
        with open(a.out, 'w', encoding='utf-8') as f:
            f.write(text + '\n')
    return 0


if __name__ == '__main__':
    sys.exit(main())
