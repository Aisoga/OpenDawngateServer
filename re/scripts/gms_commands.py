"""Client match commands and the server's intake of them (D7-static): decode, encode, and check the facts
against Dawngate.exe.

What the client sends while it plays (build 1.0.903.854, GMS_PROTOCOL.md *Command intake (D7-static)*):
  0xA6 ActionCommandMsgs  8-byte header {u8 type, u8 seq, u16 pad, u32 caster (server id)} + data whose size
                          depends on the type (ActionCommand_GetDataSize 0x008CCBB0, ActionCommands.cpp):
                          1 move, 2 stop moving, 3 ability (slot or GUID; basic attack, items, return to base),
                          4 interact with object, 5 dance, 6 taunt, 7 ping, 8 stop, 9 attack-move,
                          10 voice line, 11 hold position. Writer GameClient_SendActionCommand 0x006B41B0.
  0xA7 AbilityRankUp      u32 ability slot (3-6). Writers 0x006B43B0 (keys), 0x00402A30 (HUD).
  0xC3 ItemTransaction    11 bytes packed: u8 type, u32 item asset id, u8 slot, u32 item 2, u8 slot 2.
  0xCA SurrenderMsgs      u8 (1 or 2). 0xCD PauseMsgs: u8 0, u8 paused.
and what answers them (server -> client):
  0xB4 ActionCommandResponse  72 bytes: +0 u32 command type, +4 u8 player index, +5 u8 seq, +6 u8 kind
                              (1 ability started, 2 failed, 4 ability ended), +8 u32 ability GUID, ...
  0x9B LocomotionCommand      u8 subtype (0 move to point ... 9 stop), u8 seq of the command it answers
                              (0 = unconditional), data.
  0xC3 ItemTransaction        8 bytes: u8 player index, u8 type, u32 item asset id, u8 slot, u8 success.
  0xC2 MiniMapPing            21 bytes: u32 pinger id, u32 target id (0 = none), f32 pos[3], u8 ping type.

The server's half (SERVER_MODE.md *Command intake on the server (D7-static)*): cGameSimulator's message
handler GameSimulator_HandleMessage 0x008F4940 (vtable 0x0124D6B0 slot 1, never subscribed) dispatches the
client messages to eight handlers; the ActionCommand handler 0x008F3F50 only appends {player, command} to a
vector at sim+0x19E7C that nothing reads; ServerCommandQueue_QueueCommand 0x00923E60 and
ServerCommandQueue_TickPlayer 0x00923450 execute commands in the server's own 0x48-byte format.

`selftest` re-reads those facts from the exe: the size table, the dispatcher's id -> handler map, the
message ids and sizes in the senders and in the response builder, the three references to the vector,
and the two send stubs. `decode` takes one whole message (wire byte first); `log` decodes every client
message line of a gms_script.out or a match host's dgsim.log (`<- a6 ... (n B body) <hex>`; bodies longer
than 64 bytes are cut by the session's log); `encode` builds ActionCommandMsgs for tests and for the host's
test-side commands file (D7-live: encode with --obj 0x0BADF00D, then replace its bytes 0df0ad0b by {caster}).

Usage:
    .venv\\Scripts\\python -I re\\scripts\\gms_commands.py <Dawngate.exe> selftest
    .venv\\Scripts\\python -I re\\scripts\\gms_commands.py decode <hex>
    .venv\\Scripts\\python -I re\\scripts\\gms_commands.py log <gms_script.out | host\\dgsim.log>
    .venv\\Scripts\\python -I re\\scripts\\gms_commands.py encode move X,Y,Z [--attack] [--flags F] [--seq N] [--obj ID]
    .venv\\Scripts\\python -I re\\scripts\\gms_commands.py encode cast (--slot S | --guid G) [--target ID]
                                                     [--aim X,Y,Z] [--pos X,Y,Z] [--repeat] [--approach] [--seq N] [--obj ID]
    .venv\\Scripts\\python -I re\\scripts\\gms_commands.py encode type T [--data HEX] [--seq N] [--obj ID]
    .venv\\Scripts\\python -I re\\scripts\\gms_commands.py encode item purchase|sell|use|consume|swap ITEM [--slot S]
                                                     [--item2 ITEM] [--slot2 S]
      (D7b) ITEM is an asset id or a name (Basic_Life, or Basic_Life.Item); --slot defaults to 11 (no slot: the
      server's LabsPlayer_CheckPurchase picks one), --slot2 to 255, as ItemShopUI sends a purchase.
"""
import re
import struct
import sys

# ---- the layouts (GMS_PROTOCOL.md *Command intake (D7-static)*) -------------------------------------------------
WIRE_ACTION_COMMAND = 0xA6
WIRE_ABILITY_RANK_UP = 0xA7
WIRE_ITEM_TRANSACTION = 0xC3
WIRE_SURRENDER = 0xCA
WIRE_PAUSE = 0xCD
WIRE_ACTION_COMMAND_RESPONSE = 0xB4
WIRE_LOCOMOTION_COMMAND = 0x9B
WIRE_MINIMAP_PING = 0xC2

# ActionCommand_GetDataSize (0x008CCBB0): data bytes after the 8-byte header, per command type.
DATA_SIZE = {1: 0x18, 2: 0x18, 3: 0x2C, 4: 4, 5: 0, 6: 0, 7: 0x14, 8: 0, 9: 0x18, 10: 4, 11: 0}
TYPE_NAME = {1: "move", 2: "stop moving", 3: "ability", 4: "interact", 5: "dance", 6: "taunt", 7: "ping",
             8: "stop", 9: "attack-move", 10: "voice line", 11: "hold position"}
# Ability slots (LabsPlayer_GetAbilityInSlot 0x00911B80; PlayerClass members).
SLOT_NAME = {0: "basic", 1: "ward", 2: "passive", 3: "Q", 4: "W", 5: "E", 6: "R"}
GUID_RETURN_TO_BASE = 0x96B845C6  # SPID("ability_returntobase"), sent by GameplayControls_ReturnToBase 0x00696E20
# Voice lines (type 10), from the chat commands (locale table 0x8C5E4928) and the key bindings.
VOICE_LINE = {0: "charge", 1: "care", 2: "no", 3: "sorry", 4: "ty", 5: "yes", 8: "comp", 9: "laugh",
              10: "taunt", 11: "joke", 12: "flirt", 13: "missing"}  # 10 and 13 only from the quick-chat keys (D7b)
# D7b: 3 is "use an item" (LabsPlayer_UseItem 0x00914160), not "undo" as D7-static had it.
ITEM_TRANSACTION = {1: "purchase", 2: "sell", 3: "use", 4: "consume", 5: "swap"}
RESPONSE_KIND = {1: "ability started", 2: "failed", 4: "ability ended"}
LOCOMOTION_SUBTYPE = {0: "move to point", 1: "move (2 points)", 2: "move within range", 3: "follow",
                      4: "subtype 4", 5: "subtype 5", 6: "subtype 6", 7: "subtype 7", 8: "subtype 8", 9: "stop"}

# ---- checked against the exe ---------------------------------------------------------------------------------
SIZE_FN = 0x008CCBB0
SIZE_JT = 0x008CCC24            # 11 targets for types 1..11
DISPATCH = 0x008F4940           # GameSimulator_HandleMessage
DISPATCH_IDX = 0x008F4B50       # byte per GMS id 7..76
DISPATCH_JT = 0x008F4B28
HANDLERS = {7: 0x008F17F0, 38: 0x008F3F50, 40: 0x008F1740, 55: 0x008F3710, 61: 0x008F0490,
            67: 0x008F1390, 75: 0x008F1300, 76: 0x008F2340}
SIM_VTABLE = 0x0124D6B0
SIM_CONSTRUCT = 0x008F50B0
SENDERS = [  # (function, end, GMS id pushed for CreateMessage, payload size pushed or None, what)
    (0x006B41B0, 0x006B42AC, 0x26, None, "GameClient_SendActionCommand"),
    (0x006B43B0, 0x006B447C, 0x28, 4, "GameClient_SendAbilityRankUp"),
    (0x006B42B0, 0x006B43AF, 0x43, 0xB, "GameClient_SendItemTransaction"),
    (0x008F0110, 0x008F0200, 0x27, 0x48, "GameSimulator_SendActionCommandFailed"),
]
PENDING_VECTOR_OFFSET = 0x19E7C
PENDING_VECTOR_REFS = {0x008F3FF1, 0x008F51D1, 0x008F5403}   # reader, constructor, destructor
ITEM_RESULT_STUB = 0x009A3F00   # a bare `ret` (the item transaction's answer folds into it)
SEND_STUB = 0x008F9090          # GameSimulator_SendToPlayerListStub: releases the message, returns 0


def f3(b, off):
    return struct.unpack_from("<3f", b, off)


def fmt3(v):
    return "(%.2f, %.2f, %.2f)" % v


def decode_action_command(body):
    if len(body) < 8:
        return "short header (%d B)" % len(body)
    t, seq, _pad, obj = struct.unpack_from("<BBHI", body, 0)
    want = DATA_SIZE.get(t)
    head = "type %d %s, seq %d, caster 0x%08x" % (t, TYPE_NAME.get(t, "?"), seq, obj)
    if want is None:
        return head + ": unknown type"
    d = body[8:]
    if len(d) != want:
        return head + ": data %d B, expected %d" % (len(d), want)
    if t in (1, 9):
        tgt, = struct.unpack_from("<I", d, 0)
        z, flags = struct.unpack_from("<II", d, 0x10)
        return head + ": to %s, flags %d%s" % (fmt3(f3(d, 4)), flags,
                                               "" if tgt == 0 and z == 0 else " (+0 %08x +0x18 %08x)" % (tgt, z))
    if t == 2:
        return head + " (24 data bytes, not initialised by the client)"
    if t == 3:
        target, = struct.unpack_from("<I", d, 0)
        slot, guid, repeat = struct.unpack_from("<iII", d, 0x1C)
        approach, react = d[0x28], d[0x29]
        what = ("slot %d %s" % (slot, SLOT_NAME.get(slot, "item" if 7 <= slot <= 11 else "?"))) if slot != -1 else \
            ("guid 0x%08x%s" % (guid, " (return to base)" if guid == GUID_RETURN_TO_BASE else ""))
        return head + ": %s, target 0x%08x, aim %s, target pos %s, repeat %d, approach %d, reactivation %d" % (
            what, target, fmt3(f3(d, 4)), fmt3(f3(d, 0x10)), repeat, approach, react)
    if t == 4:
        return head + ": object 0x%08x" % struct.unpack_from("<I", d, 0)
    if t == 7:
        target, ptype = struct.unpack_from("<II", d, 0xC)
        return head + ": at %s, target 0x%08x, ping type %d" % (fmt3(f3(d, 0)), target, ptype)
    if t == 10:
        v, = struct.unpack_from("<I", d, 0)
        return head + ": line %d %s" % (v, VOICE_LINE.get(v, ""))
    return head


def decode_message(msg):
    if not msg:
        return "empty"
    w, b = msg[0], msg[1:]
    if w == WIRE_ACTION_COMMAND:
        return "ActionCommandMsgs " + decode_action_command(b)
    if w == WIRE_ABILITY_RANK_UP and len(b) == 4:
        s, = struct.unpack("<I", b)
        return "AbilityRankUp slot %d %s" % (s, SLOT_NAME.get(s, "?"))
    if w == WIRE_ITEM_TRANSACTION and len(b) == 11:
        t, item, slot, item2, slot2 = struct.unpack("<BIBIB", b)
        return "ItemTransaction (client) %s: item 0x%08x slot %d, item 2 0x%08x slot %d" % (
            ITEM_TRANSACTION.get(t, "type %d" % t), item, slot, item2, slot2)
    if w == WIRE_ITEM_TRANSACTION and len(b) == 8:
        p, t, item, slot, ok = struct.unpack("<BBIBB", b)
        return "ItemTransaction (server) player %d %s: item 0x%08x slot %d, success %d" % (
            p, ITEM_TRANSACTION.get(t, "type %d" % t), item, slot, ok)
    if w == WIRE_SURRENDER and len(b) == 1:
        return "SurrenderMsgs %d" % b[0]
    if w == WIRE_PAUSE and len(b) == 2:
        return "PauseMsgs subtype %d, value %d" % (b[0], b[1])
    if w == WIRE_ACTION_COMMAND_RESPONSE and len(b) == 0x48:
        ctype, p, seq, kind, guid = struct.unpack_from("<IBBBxI", b, 0)
        res, = struct.unpack_from("<i", b, 0x40)
        return "ActionCommandResponse %s: player %d, seq %d, command type %d, guid 0x%08x, result %d" % (
            RESPONSE_KIND.get(kind, "kind %d" % kind), p, seq, ctype, guid, res)
    if w == WIRE_LOCOMOTION_COMMAND and len(b) >= 2:
        return "LocomotionCommand %s, seq %d, data %s" % (LOCOMOTION_SUBTYPE.get(b[0], "?"), b[1], b[2:].hex())
    if w == WIRE_MINIMAP_PING and len(b) == 21:
        pinger, target = struct.unpack_from("<II", b, 0)
        return "MiniMapPing pinger 0x%08x, target 0x%08x, at %s, type %d" % (pinger, target, fmt3(f3(b, 8)), b[20])
    return "wire 0x%02x, %d B body (not a command message)" % (w, len(b))


def build_action_command(t, seq, obj, data):
    if DATA_SIZE.get(t) != len(data):
        raise ValueError("type %d needs %s data bytes, got %d" % (t, DATA_SIZE.get(t), len(data)))
    return bytes([WIRE_ACTION_COMMAND]) + struct.pack("<BBHI", t, seq, 0, obj) + data


def build_move(pos, seq=1, obj=0, attack=False, flags=0):
    return build_action_command(9 if attack else 1, seq, obj, struct.pack("<I3fII", 0, *pos, 0, flags))


def build_cast(slot=-1, guid=0, target=0, aim=(0.0, 0.0, 0.0), pos=(0.0, 0.0, 0.0), repeat=0, approach=0,
               reactivation=0, seq=1, obj=0):
    if (slot == -1) == (guid == 0):
        raise ValueError("exactly one of slot and guid (ClientCommandQueue.cpp asserts both set)")
    data = struct.pack("<I3f3fiIIBBH", target, *aim, *pos, slot, guid, repeat, approach, reactivation, 0)
    return build_action_command(3, seq, obj, data)


# ---- selftest ---------------------------------------------------------------------------------------------------
def asset_id(name):
    """An asset id: a number (0x...) as is, else FNV-1 of the lower-case "<name>.<class>" (".Item" added if missing)."""
    try:
        return int(name, 0)
    except ValueError:
        pass
    if "." not in name:
        name += ".Item"
    h = 0x811C9DC5
    for c in name.lower().encode("latin-1"):
        h = (h * 0x01000193) & 0xFFFFFFFF
        h ^= c
    return h


def build_item_transaction(kind, item, slot=11, item2="0", slot2=255):
    """C3 ItemTransaction as GameClient_SendItemTransaction writes it (D7b). kind: a number or a name of
    ITEM_TRANSACTION; slot 11 = "no slot" (LabsPlayer_CheckPurchase picks one)."""
    names = {v: k for k, v in ITEM_TRANSACTION.items()}
    t = names[kind] if kind in names else int(kind, 0)
    return bytes([WIRE_ITEM_TRANSACTION]) + struct.pack("<BIBIB", t, asset_id(item), slot, asset_id(item2), slot2)


def selftest(exe):
    import pefile
    from capstone import CS_ARCH_X86, CS_MODE_32, Cs
    from capstone.x86 import X86_OP_IMM

    pe = pefile.PE(exe, fast_load=True)
    base = pe.OPTIONAL_HEADER.ImageBase
    img = pe.get_memory_mapped_image()
    md = Cs(CS_ARCH_X86, CS_MODE_32)
    md.detail = True
    u32 = lambda va: struct.unpack_from("<I", img, va - base)[0]
    dis = lambda va, n: list(md.disasm(bytes(img[va - base:va - base + n]), va))
    ok = True

    def check(cond, text):
        nonlocal ok
        ok &= bool(cond)
        print(("ok    " if cond else "FAIL  ") + text)

    # 1. the size table
    head = dis(SIZE_FN, 0x14)
    check(head[4].mnemonic == "cmp" and head[4].operands[1].imm == 10 and head[6].op_str.endswith("0x8ccc24]"),
          "ActionCommand_GetDataSize: types 1..11 through the jump table at 0x%08X" % SIZE_JT)
    sizes = {}
    for t in range(1, 12):
        ins = dis(u32(SIZE_JT + 4 * (t - 1)), 8)[0]
        sizes[t] = ins.operands[1].imm if ins.mnemonic == "mov" else 0 if ins.mnemonic == "xor" else None
    check(sizes == DATA_SIZE, "data sizes per type: " + ", ".join("%d:%d" % kv for kv in sorted(sizes.items())))

    # 2. the server's dispatcher
    found = {}
    for gid in range(7, 77):
        k = img[DISPATCH_IDX - base + gid - 7]
        if k >= 10:
            continue
        target = u32(DISPATCH_JT + 4 * k)
        call = next((i for i in dis(target, 0x40) if i.mnemonic == "call"), None)
        if call is not None and call.operands[0].type == X86_OP_IMM and call.operands[0].imm != 0x00515440:
            found[gid] = call.operands[0].imm
    check(found == HANDLERS, "GameSimulator_HandleMessage 0x%08X: " % DISPATCH +
          ", ".join("id %d -> 0x%08X" % kv for kv in sorted(found.items())))
    check(u32(SIM_VTABLE + 4) == DISPATCH and struct.pack("<I", SIM_VTABLE) in bytes(img[SIM_CONSTRUCT - base:SIM_CONSTRUCT - base + 0x40]),
          "cGameSimulator vtable 0x%08X (written by GameSimulator_Construct) slot 1 = the dispatcher" % SIM_VTABLE)
    pushes_ff = [i for i in dis(0x008F49C1, 0x30) if i.mnemonic == "push" and i.op_str == "0xff"]
    check(pushes_ff, "the dispatcher passes player index 0xFF to the ActionCommand handler (sender lookup compiled out)")

    # 3. senders and the response builder
    for start, end, gid, size, name in SENDERS:
        code = dis(start, end - start)
        imms = [i.operands[0].imm for i in code if i.mnemonic == "push" and i.operands and
                i.operands[0].type == X86_OP_IMM]
        good = gid in imms and (size is None or size in imms)
        check(good, "%s 0x%08X: CreateMessage(GMS id %d%s)" % (name, start, gid,
                                                               "" if size is None else ", %d B" % size))

    # 4. the pending vector at sim+0x19E7C: nothing reads it
    text = next(s for s in pe.sections if s.Name.startswith(b".text"))
    tva, data = base + text.VirtualAddress, text.get_data()
    refs = set()
    for m in re.finditer(re.escape(struct.pack("<I", PENDING_VECTOR_OFFSET)), data):
        for back in range(2, 4):
            s = m.start() - back
            ins = next(md.disasm(data[s:s + 12], tva + s), None)
            if ins and ins.size == back + 4 and "0x19e7c" in ins.op_str and ins.mnemonic in ("mov", "lea"):
                refs.add(ins.address)
    check(refs == PENDING_VECTOR_REFS, "sim+0x19E7C referenced only by the reader, constructor and destructor: " +
          ", ".join("0x%08X" % a for a in sorted(refs)))

    # 5. the two send stubs
    check(img[ITEM_RESULT_STUB - base] == 0xC3, "0x%08X is a bare ret (the ItemTransaction answer)" % ITEM_RESULT_STUB)
    stub = dis(SEND_STUB, 0xC)
    check([i.mnemonic for i in stub] == ["lea", "call", "xor", "ret"],
          "GameSimulator_SendToPlayerListStub 0x%08X: release the message, return 0" % SEND_STUB)

    # 6. round trips of the builders
    m = build_move((10.0, -20.0, 0.5), seq=7, obj=0xA6710170)
    check(len(m) == 33 and "seq 7" in decode_message(m), "move: 33 bytes, " + decode_message(m))
    c = build_cast(slot=3, target=0, pos=(1.0, 2.0, 0.0), seq=8, obj=0xA6710170)
    check(len(c) == 53 and "slot 3 Q" in decode_message(c), "cast: 53 bytes, " + decode_message(c))
    print("selftest: " + ("all ok" if ok else "FAILED"))
    return ok


def parse_vec(s):
    v = tuple(float(x) for x in s.split(","))
    if len(v) != 3:
        raise ValueError("need x,y,z")
    return v


def main(argv):
    args = argv[1:]
    if len(args) == 2 and args[1] == "selftest":
        sys.exit(0 if selftest(args[0]) else 1)
    if len(args) == 2 and args[0] == "decode":
        print(decode_message(bytes.fromhex(args[1])))
        return
    if len(args) == 2 and args[0] == "log":
        pat = re.compile(r"<- ([0-9a-f]{2}) \S+ \((\d+) B body\) ([0-9a-f ]*)")
        with open(args[1], encoding="utf-8", errors="replace") as f:
            for line in f:
                m = pat.search(line)
                if m and int(m.group(1), 16) in (WIRE_ACTION_COMMAND, WIRE_ABILITY_RANK_UP, WIRE_ITEM_TRANSACTION,
                                                 WIRE_SURRENDER, WIRE_PAUSE):
                    body = bytes.fromhex(m.group(3).replace(" ", "").split(".")[0])
                    note = "" if len(body) == int(m.group(2)) else " [log cut the body at %d B]" % len(body)
                    print(line.split("<-")[0].strip(), decode_message(bytes([int(m.group(1), 16)]) + body) + note)
        return
    if len(args) >= 2 and args[0] == "encode":
        opts, pos = {}, []
        i = 2
        while i < len(args):
            a = args[i]
            if a in ("--attack", "--repeat", "--approach"):
                opts[a] = True
            elif a.startswith("--"):
                opts[a] = args[i + 1]
                i += 1
            else:
                pos.append(a)
            i += 1
        seq = int(opts.get("--seq", "1"), 0)
        obj = int(opts.get("--obj", "0"), 0)
        if args[1] == "move":
            msg = build_move(parse_vec(pos[0]), seq, obj, "--attack" in opts, int(opts.get("--flags", "0"), 0))
        elif args[1] == "cast":
            msg = build_cast(int(opts.get("--slot", "-1"), 0), int(opts.get("--guid", "0"), 0),
                             int(opts.get("--target", "0"), 0), parse_vec(opts.get("--aim", "0,0,0")),
                             parse_vec(opts.get("--pos", "0,0,0")), int("--repeat" in opts),
                             int("--approach" in opts), 0, seq, obj)
        elif args[1] == "type":
            msg = build_action_command(int(pos[0], 0), seq, obj, bytes.fromhex(opts.get("--data", "")))
        elif args[1] == "item":
            msg = build_item_transaction(pos[0], pos[1] if len(pos) > 1 else "0",
                                         int(opts.get("--slot", "11"), 0), opts.get("--item2", "0"),
                                         int(opts.get("--slot2", "255"), 0))
        else:
            raise SystemExit(__doc__)
        print(msg.hex())
        print(decode_message(msg))
        return
    raise SystemExit(__doc__)


if __name__ == "__main__":
    main(sys.argv)
