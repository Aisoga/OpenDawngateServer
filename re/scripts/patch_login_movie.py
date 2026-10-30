"""The server picker's login movie: the player's own login movie with our picker class added.

Start-Client's picker mode shows a server list on the game's login screen. The list is our own ActionScript class
(client_ui/serverpicker/ServerPicker.as, compiled by re/scripts/build_picker.py into four SWF tags,
server/internal/setup/moviepatch/picker_tags.hex). This tool reads the login movie from the player's own copy of
the game, game/Data/LoginScreen.snap, adds those four tags to the root timeline's first frame (before its first
ShowFrame; the game's own tags and bytecode stay as they are) and writes the result into an added archive,
game/Data/zz_ServerPicker.snap, which sorts after LoginScreen.snap and so replaces the movie's path. Nothing of the
game is shipped: the movie is made on the player's PC from the player's files.

The output is deterministic, so the setup's Go port (server/internal/setup/moviepatch) writes the same bytes:
  - the movie keeps its CFX header (version, and the u32 length as stored plus the tags' size); its body is a zlib
    stream of stored blocks (no compression; blocks of 65,535 bytes, the last one final), the archive compresses it;
  - the archive has one entry, the movie, Snappy-compressed by snap.py's encoder.
The picker's character id (2000) and depth (2000) must be free in the movie; a movie that has them, or has the
picker already, is refused.

The same is done for the CREATE GAME dialog's bot check box (--spec matchoptions): client_ui/matchoptions/BotCheckbox.as,
its tags server/internal/setup/moviepatch/matchoptions_tags.hex, the movie UI/Scaleform/Battlegrounds/MatchOptionsUI.gfx
from game/Data/Battlegrounds.snap, the added archive game/Data/zz_MatchOptions.snap (the same id and depth, 2000). The
same class shows the dialog's map dropdown when the copy has a map besides The Dawngate: the archive also holds the
dialog's map list, UI/Scaleform/Battlegrounds/dgmaps.json (map_list: one line per map of EXTRA_MAPS whose archive is in
the copy's game/Data, found beside the source archive; --maps names them instead), which the class reads. The list
is padded to MAP_LIST_SIZE and stored as Snappy literals only, so Start-Client's front can write the maps of the server
a login goes to into it in place (write_map_list, server_maps; Go's moviepatch.WriteMapList); a check counts such a
list as current (map_list_only).
And for the chat rooms the server marks as open by default (--spec chatrooms): client_ui/chatrooms/AutoRooms.as, its tags
server/internal/setup/moviepatch/chatrooms_tags.hex, the chat movie UI/Scaleform/Global/Chat.gfx from
game/Data/Global.snap, the added archive game/Data/zz_Chat.snap (the same id and depth, 2000).
And for the After Action Review's tabs (--spec aftaction): client_ui/aftaction/AfterActionTabs.as, its tags
server/internal/setup/moviepatch/aftaction_tags.hex, the movie UI/Scaleform/Battlegrounds/AfterActionReviewUI.gfx from
game/Data/Battlegrounds.snap, the added archive game/Data/zz_AfterActionReview.snap (the same id and depth, 2000). That
archive also holds the web root of the server the copy is set up for, UI/Scaleform/Battlegrounds/dgweb.json
(web_root_file: the root the config packages' web content URLs use, http://<host>:18080/battlegrounds_test/live/www/
client/1/common; --host names the copy's server, default 127.0.0.1), from which the class loads the match history pages.
And for the item shop's inline refund (--spec itemshop): client_ui/itemshop/ShopRefund.as, its tags
server/internal/setup/moviepatch/itemshop_tags.hex, the shop movie UI/Scaleform/HUD/ItemShopUI.gfx from
game/Data/HUD.snap, the added archive game/Data/zz_ItemShop.snap (the same id and depth, 2000). The class's optional
settings file (HUD/dgrefund.json: a gate, a debug text box) is in the archive with no settings (REFUND_SETTINGS; a
missing file would log a FileDepot.cpp 138 assert at every match start); a test writes its own.
And for the Collection's Items category (--spec collection): client_ui/collection/CollectionItems.as, its tags
server/internal/setup/moviepatch/collection_tags.hex, the front end's movie UI/Scaleform/CDN/swf/code/UIRuntime.gfx from
game/Data/code.snap, the added archive game/Data/zz_CollectionItems.snap (the same id and depth, 2000). That archive
also holds the item list, UI/Scaleform/Collection/dgitems.json (item_list), written from the server's content.json
(--content; the item icons are the game's own textures, which the front end has mounted).

Usage (python -I: the game's files are untrusted input):
    python -I re/scripts/patch_login_movie.py <game/Data/LoginScreen.snap> <out zz_ServerPicker.snap> [--tags <file>]
    python -I re/scripts/patch_login_movie.py --spec matchoptions <game/Data/Battlegrounds.snap> <out zz_MatchOptions.snap>
                                              [--maps mm|none]   # the map list's maps (default: the copy's own)
    python -I re/scripts/patch_login_movie.py --read-map-list <copy holding game/Data>   # print the copy's map list
    python -I re/scripts/patch_login_movie.py --spec chatrooms <game/Data/Global.snap> <out zz_Chat.snap>
    python -I re/scripts/patch_login_movie.py --spec aftaction <game/Data/Battlegrounds.snap> <out zz_AfterActionReview.snap>
                                              [--host <address>]   # the copy's server (patch_config.py's --host)
    python -I re/scripts/patch_login_movie.py --spec itemshop <game/Data/HUD.snap> <out zz_ItemShop.snap>
    python -I re/scripts/patch_login_movie.py --spec collection --content <server/data/content.json> <game/Data/code.snap>
                                              <out zz_CollectionItems.snap> [--debug]   # --debug: the class's log lines
    python -I re/scripts/patch_login_movie.py [--spec matchoptions|chatrooms|aftaction|itemshop|collection] --movie <in .gfx> <out .gfx> [--tags <file>]
"""
import json
import math
import os
import struct
import sys
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)  # -I drops the script folder
import snap  # noqa: E402

REPO = os.path.dirname(os.path.dirname(HERE))
MOVIEPATCH = os.path.join(REPO, "server", "internal", "setup", "moviepatch")


class Spec:
    """One added class: the archive its movie comes from, the added archive, the movie's path, the committed tags."""

    def __init__(self, what, source, rel, movie_path, tags, class_name, char_id=2000, depth=2000, map_list=False,
                 web_root=False, extra=(), items=False):
        self.what, self.source, self.rel, self.movie_path = what, source, rel, movie_path
        self.tags, self.class_name, self.char_id, self.depth = tags, class_name, char_id, depth
        self.map_list = map_list  # the archive also holds the create dialog's map list (MAP_LIST_PATH)
        self.web_root = web_root  # the archive also holds the server's web root (WEB_ROOT_PATH)
        self.extra = tuple(extra)  # more files of the archive, after the movie: (path, bytes)
        self.items = items  # the archive also holds the Collection's item list (ITEMS_PATH, from a content.json)


SPECS = {
    "picker": Spec("the server picker", "game/Data/LoginScreen.snap", "game/Data/zz_ServerPicker.snap",
                   "UI/Scaleform/LoginScreen/LoginScreen_BG.gfx", os.path.join(MOVIEPATCH, "picker_tags.hex"),
                   "ServerPicker"),
    "matchoptions": Spec("the bot check box", "game/Data/Battlegrounds.snap", "game/Data/zz_MatchOptions.snap",
                         "UI/Scaleform/Battlegrounds/MatchOptionsUI.gfx",
                         os.path.join(MOVIEPATCH, "matchoptions_tags.hex"), "BotCheckbox", map_list=True),
    "chatrooms": Spec("the chat rooms opened by default", "game/Data/Global.snap", "game/Data/zz_Chat.snap",
                      "UI/Scaleform/Global/Chat.gfx", os.path.join(MOVIEPATCH, "chatrooms_tags.hex"), "AutoRooms"),
    "aftaction": Spec("the After Action Review's tabs", "game/Data/Battlegrounds.snap",
                      "game/Data/zz_AfterActionReview.snap", "UI/Scaleform/Battlegrounds/AfterActionReviewUI.gfx",
                      os.path.join(MOVIEPATCH, "aftaction_tags.hex"), "AfterActionTabs", web_root=True),
    "itemshop": Spec("the shop's inline refund", "game/Data/HUD.snap", "game/Data/zz_ItemShop.snap",
                     "UI/Scaleform/HUD/ItemShopUI.gfx", os.path.join(MOVIEPATCH, "itemshop_tags.hex"), "ShopRefund",
                     extra=(("UI/Scaleform/HUD/dgrefund.json", b"dawngate-refund\t1\nend\n"),)),
    "collection": Spec("the Collection's items", "game/Data/code.snap", "game/Data/zz_CollectionItems.snap",
                       "UI/Scaleform/CDN/swf/code/UIRuntime.gfx", os.path.join(MOVIEPATCH, "collection_tags.hex"),
                       "CollectionItems", items=True),
}
# The shop refund's settings file (client_ui/itemshop/ShopRefund.as reads it as HUD/dgrefund.json): the setup writes
# the default (no settings), since the class always asks for it and a missing file costs a FileDepot.cpp 138 assert
# ("load failed") at every match start; a test writes its own. moviepatch.RefundSettings.
REFUND_SETTINGS_PATH, REFUND_SETTINGS = SPECS["itemshop"].extra[0]
PICKER = SPECS["picker"]

# The create dialog's map list (client_ui/matchoptions/BotCheckbox.as reads it as Battlegrounds/dgmaps.json; the game's
# file opener keeps only .json): "dawngate-maps<TAB>1", a line "map<TAB><tag><TAB><label>" per map besides The Dawngate
# that the copy has, "end"; LF, UTF-8. No map line: the dialog stays as the game made it (The Dawngate only).
MAP_LIST_PATH = "UI/Scaleform/Battlegrounds/dgmaps.json"
MAP_LIST_MAGIC = "dawngate-maps\t1"
# The list's fixed size: the text padded with line ends, stored as Snappy literals only, so every list has the same
# layout and Start-Client's front can write the list of the server a login goes to in place while the game has the
# archive mounted (write_map_list; moviepatch.MapListSize, WriteMapList).
MAP_LIST_SIZE = 1 << 10
# The maps besides The Dawngate a copy can have: its name tag (the server's ~#<tag>), its archive in game/Data, the
# dropdown's text, the map's display name (the lobby's map line says the same, patch_locale.LOBBY_LINES; the browser
# and the lobby's title show the server's prefix, "[Pass] "). MM_Map_v3 is shown to players as Anshavar Pass.
EXTRA_MAPS = (("mm", "zz_MM_Map_v3.snap", "Anshavar Pass"),)
# The After Action Review's web root (client_ui/aftaction/AfterActionTabs.as reads it as Battlegrounds/dgweb.json):
# "dawngate-web<TAB>1", "common<TAB><the web content's common root>", "end"; LF, ASCII. The root is the one the copy's
# config packages give the game's web content (patch_config.py: http://<host>:18080, the client's DRC path 1; the
# server's web.webContentRoot), the same in a tunnelled copy (the front passes the HTTP port to the current server).
WEB_ROOT_PATH = "UI/Scaleform/Battlegrounds/dgweb.json"
WEB_ROOT_MAGIC = "dawngate-web\t1"
WEB_PORT = 18080
WEB_CONTENT_ROOT = "battlegrounds_test/live/www/client/1/common"
DEFAULT_HOST = "127.0.0.1"


# The Collection's item list (client_ui/collection/CollectionItems.as reads it as Collection/dgitems.json), written from
# the server's content.json (its matchItems and itemFilters, the game's own item data; moviepatch.ItemList in Go, the
# same bytes). The file (UTF-8, LF, tab-separated):
#     dawngate-collitems	2
#     filter	<group>	<tag>	<title>             one per tag of the shop's filter groups, in their order
#     item	<name>	<title>	<kind>	<cost>	<group>	<icon>     one per match item a player can buy, then its details:
#     stat	<runs>                              one per stat: the shop's line ("+20 Haste")
#     passive	<kind>	<title>	<runs>              one per passive (kind unique, stackable or empty)
#     active	<kind>	<title>	<cooldown>	<runs>  its active (a spell's ability, a consumable's effect; cooldown in s)
#     from	<name>                              one per item it builds from (in the list)
#     into	<name>                              one per item it builds into (in the list)
#     tags	<tag> <tag> ...                     its stats' attributes and its tags (the shop filters' keys)
#     end
# kind: basic, advanced or legendary (the item's tier), consumable or spell (its category); group: a spell's own
# (AbilityItemCategory: Offense, Utility or Defense), any other item's the shop's filter groups (their titles) that hold
# one of its tags, in the shop's order, joined by " / " ("Offense / Utility"), empty without such a tag (COLLITEMS10:
# the game gives the other items no category of their own); icon: its ShopIcons texture in UI_Icons.snap (forward
# slashes). <runs> is text in colours: pairs <colour><TAB><text>, the colour "#RRGGBB" or
# empty (plain) or "n" (a line break, empty text). Items ordered by kind (ITEM_KIND_ORDER), cost, then title; without
# the cheat items (the flag, or a name starting consumable_debug or holding cheat, as the server's isCheatItem) and
# without an item whose own fields hold a tab or a line end; in the details a tab or CR is a space. debug=True adds
# "debug<TAB>1" (the class's Assert lines; research only).
ITEMS_PATH = "UI/Scaleform/Collection/dgitems.json"
ITEMS_MAGIC = "dawngate-collitems\t2"
ITEM_KIND_ORDER = {"basic": 0, "advanced": 1, "legendary": 2, "consumable": 3, "spell": 4}


def _flat(s):
    """A detail field: tabs, CR and line ends as spaces."""
    return str(s or "").replace("\t", " ").replace("\r", " ").replace("\n", " ")


def _runs(parts, text=""):
    """Text runs as list fields (colour, text, ...): parts [{t, c}] (else `text` plain); a line end is the pair ("n", "")."""
    if not parts:
        parts = [{"t": text}] if text else []
    out = []
    for p in parts:
        c = str(p.get("c") or "")
        if not (len(c) == 7 and c[0] == "#" and all(x in "0123456789abcdefABCDEF" for x in c[1:])):
            c = ""
        for k, piece in enumerate(str(p.get("t") or "").split("\n")):
            if k > 0:
                out += ["n", ""]
            piece = piece.replace("\t", " ").replace("\r", " ")
            if piece:
                out += [c, piece]
    return out


def _item_tags(it):
    """A match item's tags in the list: its stats' attributes, then its tags, each once, none with a space."""
    tags = []
    for t in [str(s.get("id") or "") for s in it.get("stats") or []] + [str(t) for t in it.get("tags") or []]:
        if _tag_ok(t) and t not in tags:
            tags.append(t)
    return tags


def _tag_ok(t):
    """A tag the list carries: not empty, without a space, tab or line end."""
    return bool(t) and not any(c in t for c in " \t\r\n")


def _filter_groups(content):
    """The shop's filter groups as [(title, [tag, ...])] in the shop's order (the tags the list's filter lines carry)."""
    return [(_flat(f.get("title") or f.get("name")),
             [str(t.get("tag") or "") for t in f.get("tags") or [] if _tag_ok(str(t.get("tag") or ""))])
            for f in content.get("itemFilters") or []]


def item_group(it, groups):
    """A match item's group in the list: a spell's own, else the titles of the filter groups (`_filter_groups`) that
    hold one of its tags, in their order, joined by " / " (empty: none)."""
    if item_kind(it) == "spell":
        return str(it.get("group") or "")
    tags = set(_item_tags(it))
    out = []
    for title, gtags in groups:
        if title and title not in out and tags.intersection(gtags):
            out.append(title)
    return " / ".join(out)


def _details(it, listed):
    """The detail lines (without line ends) of a match item; `listed` the names of the items in the list."""
    lines = []
    for s in it.get("stats") or []:
        runs = _runs(s.get("parts") or [], str(s.get("id") or ""))
        if runs:
            lines.append("stat\t" + "\t".join(runs))
    for p in it.get("passives") or []:
        lines.append("\t".join(["passive", _flat(p.get("kind")), _flat(p.get("title"))]
                               + _runs(p.get("parts") or [], str(p.get("text") or ""))))
    a = it.get("activeInfo") or {}
    runs = _runs(a.get("parts") or [], str(a.get("text") or ""))
    if runs:
        cd = float(a.get("cooldown") or 0)
        lines.append("\t".join(["active", _flat(a.get("kind")), _flat(a.get("title")),
                                str(int(math.floor(cd + 0.5)) if cd > 0 else 0)] + runs))
    for key in ("from", "into"):
        for st in it.get(key) or []:
            if str(st.get("item") or "") in listed:
                lines.append(key + "\t" + str(st.get("item")))
    tags = _item_tags(it)
    if tags:
        lines.append("tags\t" + " ".join(tags))
    return lines


def item_kind(it):
    """A match item's kind in the list: spell (category Ability), consumable (category Consumable), else its tier."""
    if it.get("category") == "Ability":
        return "spell"
    if it.get("category") == "Consumable":
        return "consumable"
    return str(it.get("tier") or "").lower()


def item_list(content, debug=False):
    """The Collection's item list (bytes) from a content.json's data (a dict) or its path."""
    if isinstance(content, str):
        with open(content, encoding="utf-8") as f:
            content = json.load(f)
    rows = []
    groups = _filter_groups(content)
    for it in content.get("matchItems") or []:
        name = str(it.get("name") or "")
        low = name.lower()
        if it.get("cheat") or low.startswith("consumable_debug") or "cheat" in low:
            continue
        kind = item_kind(it)
        cost = int(it.get("cost") or 0)
        fields = [name, str(it.get("title") or name), kind, str(cost), item_group(it, groups),
                  str(it.get("icon") or "").replace("\\", "/")]
        if any("\t" in x or "\n" in x or "\r" in x for x in fields):
            continue
        rows.append((ITEM_KIND_ORDER.get(kind, len(ITEM_KIND_ORDER)), cost, fields[1].encode("utf-8"), fields, it))
    rows.sort(key=lambda r: r[:3])
    listed = {r[3][0] for r in rows}
    out = [ITEMS_MAGIC] + (["debug\t1"] if debug else [])
    for f in content.get("itemFilters") or []:
        group = _flat(f.get("title") or f.get("name"))
        for t in f.get("tags") or []:
            tag = str(t.get("tag") or "")
            if _tag_ok(tag):
                out.append("\t".join(["filter", group, tag, _flat(t.get("title") or tag)]))
    for r in rows:
        out.append("item\t" + "\t".join(r[3]))
        out += _details(r[4], listed)
    return ("\n".join(out + ["end"]) + "\n").encode("utf-8")


def web_root_file(host=DEFAULT_HOST, port=WEB_PORT):
    """dgweb.json's bytes for the copy's server `host` (moviepatch.WebRootFile)."""
    if not host or any(c <= " " or c >= "\x7f" or c in "\"'<>" for c in host):
        raise ValueError(f"web root: host {host!r}: only printable ASCII without spaces, quotes or angle brackets")
    if not 1 <= port <= 65535:
        raise ValueError(f"web root: port {port}: must be 1..65535")
    return f"{WEB_ROOT_MAGIC}\ncommon\thttp://{host}:{port}/{WEB_CONTENT_ROOT}\nend\n".encode("ascii")


# The picker's names, as before the specs.
TAGS = PICKER.tags
MOVIE_PATH = PICKER.movie_path
SOURCE = PICKER.source
REL = PICKER.rel
CHAR_ID = PICKER.char_id
DEPTH = PICKER.depth
CLASS_NAME = PICKER.class_name

# Tags whose first u16 is the character id they define (standard SWF and Scaleform's 1001-1009).
DEFINING = {2, 6, 7, 10, 11, 14, 20, 21, 22, 32, 33, 34, 35, 36, 37, 39, 46, 48, 60, 75,
            83, 84, 87, 90, 91, 1001, 1003, 1005, 1006, 1007, 1008, 1009}


class MovieError(ValueError):
    pass


def tags_of(buf, pos, end):
    """[(code, tag start, data start, data end)] of buf[pos:end] up to and including the first End tag."""
    out = []
    while pos < end:
        if pos + 2 > end:
            raise MovieError(f"a tag header runs past its container at {pos:#x}")
        (h,) = struct.unpack_from("<H", buf, pos)
        code, ln, hdr = h >> 6, h & 0x3F, 2
        if ln == 0x3F:
            if pos + 6 > end:
                raise MovieError(f"a long tag header runs past its container at {pos:#x}")
            (ln,) = struct.unpack_from("<I", buf, pos + 2)
            hdr = 6
        start = pos + hdr
        if start + ln > end:
            raise MovieError(f"tag {code} at {pos:#x} runs past its container")
        out.append((code, pos, start, start + ln))
        pos = start + ln
        if code == 0:
            break
    return out


def _cstring_end(buf, pos, end):
    i = buf.find(b"\0", pos, end)
    if i < 0:
        raise MovieError("a string without its NUL")
    return i + 1


def _pairs(buf, pos, end):
    """u16 count, then (u16 id, NUL-terminated name): ids and names."""
    (n,) = struct.unpack_from("<H", buf, pos)
    pos += 2
    out = []
    for _ in range(n):
        (cid,) = struct.unpack_from("<H", buf, pos)
        nxt = _cstring_end(buf, pos + 2, end)
        out.append((cid, buf[pos + 2:nxt - 1].decode("utf-8", "replace")))
        pos = nxt
    return out


def scan(body):
    """The movie body's character ids, class names, root depths and the offset of the root's first ShowFrame."""
    nbits = body[0] >> 3
    pos = (5 + 4 * nbits + 7) // 8 + 4
    ids, names, depths = set(), set(), set()
    first_show = None

    def walk(buf, start, end, root):
        nonlocal first_show
        for code, tpos, s, e in tags_of(buf, start, end):
            if code in DEFINING and e - s >= 2:
                ids.add(struct.unpack_from("<H", buf, s)[0])
            if code == 39 and e - s >= 4:
                walk(buf, s + 4, e, False)
            elif code in (56, 76):  # ExportAssets, SymbolClass
                for cid, name in _pairs(buf, s, e):
                    ids.add(cid)
                    names.add(name)
            elif code in (57, 71):  # ImportAssets, ImportAssets2
                p = _cstring_end(buf, s, e) + (2 if code == 71 else 0)
                ids.update(cid for cid, _ in _pairs(buf, p, e))
            if root:
                if code == 26 and e - s >= 3:
                    depths.add(struct.unpack_from("<H", buf, s + 1)[0])
                elif code == 70 and e - s >= 4:
                    depths.add(struct.unpack_from("<H", buf, s + 2)[0])
                elif code == 1 and first_show is None:
                    first_show = tpos

    walk(body, pos, len(body), True)
    return ids, names, depths, first_show


def zlib_stored(data):
    """A zlib stream of stored deflate blocks (65,535 bytes each, the last one final; Adler-32 at the end)."""
    out = bytearray(b"\x78\x01")
    n = len(data)
    i = 0
    while True:
        chunk = data[i:i + 0xFFFF]
        i += len(chunk)
        final = i >= n
        out += bytes([1 if final else 0]) + struct.pack("<HH", len(chunk), len(chunk) ^ 0xFFFF) + chunk
        if final:
            break
    return bytes(out + struct.pack(">I", zlib.adler32(data) & 0xFFFFFFFF))


def patch_movie(raw, tags, spec=PICKER):
    """The movie's bytes with our tags before the root timeline's first ShowFrame."""
    if len(raw) < 8 or raw[:3] not in (b"CFX", b"CWS"):
        raise MovieError("not a compressed Scaleform movie (CFX)")
    (length,) = struct.unpack_from("<I", raw, 4)
    d = zlib.decompressobj()
    try:
        body = d.decompress(raw[8:]) + d.flush()
    except zlib.error as e:
        raise MovieError(f"zlib: {e}")
    if not d.eof:
        raise MovieError("zlib: the stream is cut short")
    ids, names, depths, show = scan(body)
    if spec.class_name in names:
        raise MovieError(f"the movie has {spec.what} already")
    if spec.char_id in ids:
        raise MovieError(f"the movie uses character id {spec.char_id}")
    if spec.depth in depths:
        raise MovieError(f"the movie uses depth {spec.depth} on its root timeline")
    if show is None:
        raise MovieError("no ShowFrame on the root timeline")
    new = body[:show] + tags + body[show:]
    head = raw[:4] + struct.pack("<I", (length + len(tags)) & 0xFFFFFFFF)
    return head + zlib_stored(new)


def read_tags(path=TAGS):
    """The tags' bytes from their committed hex text."""
    with open(path, "rb") as f:
        return bytes.fromhex(f.read().decode("ascii"))


def copy_maps(data_dir):
    """EXTRA_MAPS's entries whose archive is in the copy's game/Data folder `data_dir`."""
    return [m for m in EXTRA_MAPS if os.path.isfile(os.path.join(data_dir, m[1]))]


def map_list(maps):
    """The map list's bytes for `maps` (EXTRA_MAPS's entries): the text padded with line ends to MAP_LIST_SIZE (maps
    that don't fit are left out)."""
    text = (MAP_LIST_MAGIC + "\n").encode("utf-8")
    for tag, _, label in maps:
        line = f"map\t{tag}\t{label}\n".encode("utf-8")
        if len(text) + len(line) + len(b"end\n") > MAP_LIST_SIZE:
            break
        text += line
    text += b"end\n"
    return text + b"\n" * (MAP_LIST_SIZE - len(text))


def snappy_literals(src):
    """A Snappy block of literals only (snap.EncodeLiterals): its size depends on len(src) alone."""
    n, out = len(src), bytearray()
    while True:  # the uncompressed length, a varint
        b = n & 0x7F
        n >>= 7
        out.append(b | (0x80 if n else 0))
        if not n:
            break
    a = 0
    while a < len(src):
        ln = min(len(src) - a, 1 << 16)
        k = ln - 1
        if k < 60:
            out.append(k << 2)
        elif k < 256:
            out += bytes([60 << 2, k])
        else:
            out += bytes([61 << 2, k & 0xFF, k >> 8])
        out += src[a:a + ln]
        a += ln
    return bytes(out)


def server_maps(have, offered):
    """The maps of `have` (the copy's, copy_maps) that a server offers: `offered` holds its tags (the tunnel's maps
    fact). A map the copy lacks is never listed."""
    return [m for m in have if m[0] in offered]


def map_list_archive(copy):
    """The copy's archive that holds the map list (the copy holds game/Data)."""
    return os.path.join(copy, *SPECS["matchoptions"].rel.split("/"))


class MapListLayoutError(ValueError):
    """The archive has no map list of the fixed layout (a setup before it)."""


def _index(d):
    """An archive's entries from its bytes: [(name, size, stored_size, offset, flags)] (snap.Snap's reading)."""
    if len(d) < snap.HEADER_SIZE or d[:4] != snap.MAGIC:
        raise MapListLayoutError("not a .snap archive")
    count, names_size = struct.unpack_from("<II", d, 8)
    names_off = len(d) - names_size
    index_off = names_off - count * snap.ENTRY_SIZE
    if index_off < snap.HEADER_SIZE:
        raise MapListLayoutError("the index exceeds the file")
    out = []
    for i in range(count):
        v = struct.unpack_from("<8I", d, index_off + i * snap.ENTRY_SIZE)
        end = d.find(b"\0", names_off + v[0])
        if end < 0 or v[6] + v[2] > index_off:
            raise MapListLayoutError(f"entry {i} is outside the archive")
        out.append((d[names_off + v[0]:end].decode("utf-8", "replace"), v[1], v[2], v[6], v[7]))
    return out


def _map_list_entry(d):
    """The map list's (offset, stored size) in archive bytes `d`, checked: a literals-only block of MAP_LIST_SIZE."""
    want = len(snappy_literals(bytes(MAP_LIST_SIZE)))
    for name, size, stored_size, offset, flags in _index(d):
        if name == MAP_LIST_PATH:
            if flags & snap.FLAG_STORED or size != MAP_LIST_SIZE or stored_size != want:
                break
            return offset, stored_size
    raise MapListLayoutError("no map list of the fixed layout (an older setup; the next setup writes it)")


def write_map_list(copy, maps):
    """Write the list of `maps` into the copy's zz_MatchOptions.snap in place (only the entry's bytes; the game may
    have the archive mounted). True when the bytes changed; MapListLayoutError leaves the file as it was."""
    path = map_list_archive(copy)
    with open(path, "rb") as f:
        d = f.read()
    offset, size = _map_list_entry(d)
    payload = snappy_literals(map_list(maps))
    if d[offset:offset + size] == payload:
        return False
    with open(path, "r+b") as f:
        f.seek(offset)
        f.write(payload)
    return True


def map_list_only(have, want):
    """Whether archive bytes `have` differ from `want` only in the map list's text (both of the fixed layout, have's
    list whole): a list Start-Client's front wrote for the server a login went to, which the setup counts as current."""
    if len(have) != len(want) or have == want:
        return False
    try:
        offset, size = _map_list_entry(want)
        if _map_list_entry(have) != (offset, size):
            return False
        text = snap.snappy_decompress(have[offset:offset + size], MAP_LIST_SIZE)
    except (MapListLayoutError, snap.SnapError):
        return False
    return (have[:offset] == want[:offset] and have[offset + size:] == want[offset + size:] and
            text.startswith((MAP_LIST_MAGIC + "\n").encode("utf-8")) and b"\nend\n" in text)


def build(snap_path, tags=None, spec=PICKER, maps=None, host=None, content=None, debug=False):
    """The added archive's bytes (zz_ServerPicker.snap from the player's LoginScreen.snap; with the matchoptions spec
    zz_MatchOptions.snap from Battlegrounds.snap, with the chatrooms spec zz_Chat.snap from Global.snap, with the
    aftaction spec zz_AfterActionReview.snap from Battlegrounds.snap, with the itemshop spec zz_ItemShop.snap from
    HUD.snap, with the collection spec zz_CollectionItems.snap from code.snap). The matchoptions archive also holds the
    map list of `maps` (None: the maps whose archives are beside snap_path), the aftaction archive the web root of the
    server `host` (None: DEFAULT_HOST), the collection archive the item list of `content` (a content.json's path or
    data; required)."""
    if spec.items and content is None:
        raise ValueError(f"{spec.what} needs the server's content.json (its item list)")
    tags = read_tags(spec.tags) if tags is None else tags
    s = snap.Snap(snap_path)
    entry = next((e for e in s.entries if e.name == spec.movie_path), None)
    if entry is None:
        raise MovieError(f"{snap_path}: no {spec.movie_path}")
    movie = patch_movie(s.read(entry), tags, spec)
    files = [(spec.movie_path, movie)]
    if spec.map_list:
        if maps is None:
            maps = copy_maps(os.path.dirname(os.path.abspath(snap_path)))
        files.append((MAP_LIST_PATH, map_list(maps), True))
    if spec.web_root:
        files.append((WEB_ROOT_PATH, web_root_file(host or DEFAULT_HOST)))
    if spec.items:
        files.append((ITEMS_PATH, item_list(content, debug)))
    files.extend(spec.extra)
    return pack_bytes(files)


def pack_bytes(files):
    """snap.pack's archive as bytes: files [(name, bytes)] Snappy-compressed, [(name, bytes, True)] as literals only
    (snappy_literals; Go's snap.File.Literals)."""
    data = bytearray()
    index = bytearray()
    names = bytearray()
    for name, blob, *literals in files:
        payload = snappy_literals(blob) if literals and literals[0] else snap.snappy_compress(blob)
        index += struct.pack("<8I", len(names), len(blob), len(payload), 0, 0, 0, snap.HEADER_SIZE + len(data), 0)
        names += name.encode("utf-8") + b"\0"
        data += payload
    head = snap.MAGIC + struct.pack("<HHII", 0x200, 1, len(files), len(names)) + b"\0" * 16
    return bytes(head + data + index + names)


def print_map_list(copy):
    """Print the map list in a copy's zz_MatchOptions.snap (its maps and whether it has the fixed layout); 1 without
    one."""
    path = map_list_archive(copy)
    with open(path, "rb") as f:
        d = f.read()
    try:
        offset, size = _map_list_entry(d)
        text, layout = snap.snappy_decompress(d[offset:offset + size], MAP_LIST_SIZE), "the fixed layout"
    except MapListLayoutError:
        entry = next((e for e in _index(d) if e[0] == MAP_LIST_PATH), None)
        if entry is None:
            print(f"{path}: no map list")
            return 1
        _, size, stored_size, offset, _ = entry
        text, layout = snap.snappy_decompress(d[offset:offset + stored_size], size), "an older layout"
    maps = [line.split("\t")[1] for line in text.decode("utf-8").split("\nend\n")[0].splitlines()
            if line.startswith("map\t")]
    print(f"{path}: the map list ({layout}): The Dawngate{''.join(', ' + t for t in maps)}"
          f"{'' if maps else ' only (no dropdown)'}")
    return 0


def main(argv):
    args = list(argv[1:])
    spec = PICKER
    if "--spec" in args:
        i = args.index("--spec")
        if i + 1 >= len(args) or args[i + 1] not in SPECS:
            sys.exit(f"--spec takes one of {', '.join(SPECS)}")
        spec = SPECS[args[i + 1]]
        del args[i:i + 2]
    maps = None
    if "--maps" in args:
        i = args.index("--maps")
        names = args[i + 1].split(",") if i + 1 < len(args) else []
        known = {m[0]: m for m in EXTRA_MAPS}
        if not names or any(n not in known and n != "none" for n in names):
            sys.exit(f"--maps takes none or a comma-separated list of {', '.join(known)}")
        maps = [known[n] for n in names if n != "none"]
        del args[i:i + 2]
    host = None
    if "--host" in args:
        i = args.index("--host")
        if i + 1 >= len(args) or not spec.web_root:
            sys.exit("--host takes the copy's server address (only with --spec aftaction)")
        host = args[i + 1]
        del args[i:i + 2]
    content = None
    if "--content" in args:
        i = args.index("--content")
        if i + 1 >= len(args) or not spec.items:
            sys.exit("--content takes the server's content.json (only with --spec collection)")
        content = args[i + 1]
        del args[i:i + 2]
    debug = "--debug" in args
    if debug:
        if not spec.items:
            sys.exit("--debug only with --spec collection")
        args.remove("--debug")
    tags_path = spec.tags
    if "--tags" in args:
        i = args.index("--tags")
        tags_path = args[i + 1]
        del args[i:i + 2]
    if not sys.flags.isolated:
        sys.exit("run this with python -I: it reads the game's files")
    if len(args) == 2 and args[0] == "--read-map-list":
        return print_map_list(args[1])
    tags = read_tags(tags_path)
    if len(args) == 3 and args[0] == "--movie":
        with open(args[1], "rb") as f:
            out = patch_movie(f.read(), tags, spec)
        with open(args[2], "wb") as f:
            f.write(out)
        print(f"{args[2]}: {len(out)} bytes")
        return 0
    if len(args) != 2:
        print(__doc__)
        return 2
    try:
        out = build(args[0], tags, spec, maps, host, content, debug)
    except MovieError:
        raise
    except ValueError as e:
        sys.exit(str(e))
    with open(args[1], "wb") as f:
        f.write(out)
    back = snap.Snap(args[1])
    movie = back.read(back.entries[0])
    print(f"{args[1]}: {len(out)} bytes, the movie {len(movie)} bytes with {len(tags)} bytes of tags ({spec.what})")
    if spec.map_list:
        listed = [line.split("\t")[1] for line in back.read(back.entries[1]).decode("utf-8").splitlines()
                  if line.startswith("map\t")]
        print(f"  the map list ({MAP_LIST_PATH}): The Dawngate{''.join(', ' + t for t in listed)}"
              f"{'' if listed else ' only (no dropdown)'}")
    if spec.web_root:
        print(f"  the web root ({WEB_ROOT_PATH}): {back.read(back.entries[1]).decode('ascii').splitlines()[1][7:]}")
    if spec.items:
        listed = back.read(back.entries[1]).decode("utf-8").count("\nitem\t")
        print(f"  the item list ({ITEMS_PATH}): {listed} items")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
