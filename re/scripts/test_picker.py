"""Tests of the movie patcher of the server picker, the bot check box, the chat rooms class and the review's tabs
(re/scripts/patch_login_movie.py) and their
committed bytes.

The made-up movie and its patched form have the SHA-256s that server/internal/setup/moviepatch's Go tests pin (the
Go port writes the same bytes); the encoder and archive writer the ones server/internal/setup/snap's tests pin. With
the game's files (client/ia_Dawngate/Dawngate, or DAWNGATE_GAME) the player's own LoginScreen.snap is patched too and
read back, and its Battlegrounds.snap with the bot check box (with DAWNGATE_MATCHOPTIONS_GO naming the file that Go's
TestBuildMatchOptions854 wrote with DAWNGATE_MATCHOPTIONS_OUT, compared byte for byte), and its Global.snap with the
class that opens the chat rooms the server marks as open by default (DAWNGATE_CHATROOMS_GO naming the file that
TestBuildChatRooms854 wrote with DAWNGATE_CHATROOMS_OUT), and its Battlegrounds.snap with the After Action Review's tabs
and the web root file (DAWNGATE_AFTERACTION_GO naming the file TestBuildAfterAction854 wrote with
DAWNGATE_AFTERACTION_OUT), and its HUD.snap with the item shop's inline refund (DAWNGATE_ITEMSHOP_GO naming the file
TestBuildItemShop854 wrote with DAWNGATE_ITEMSHOP_OUT), and its code.snap with the Collection's Items
(DAWNGATE_COLLECTION_GO naming the file TestBuildCollection854 wrote with DAWNGATE_COLLECTION_OUT, and the content
beside it). --compile also checks that the committed ABC and tags are what the source compiles to (build_picker.py
check; needs the Flex SDK and Java).

Usage:
    python -I re/scripts/test_picker.py [--compile]
"""
import hashlib
import os
import re
import struct
import sys
import tempfile
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)  # -I drops the script folder
import patch_login_movie as plm  # noqa: E402
import snap  # noqa: E402

REPO = os.path.dirname(os.path.dirname(HERE))
# a made-up HUD.snap (the synth movie) with the tags b"OURTAGS" through the itemshop spec, as itemshop_test.go pins it
ITEMSHOP_SYNTH_SHA = "5094c226d16433e5616eaede8b29ea86fb503aa34bb508d9b2a017b9ccb80009"
# the item list of the made-up content (server/testdata/collection/content.json), and a made-up code.snap (the synth
# movie) with the tags b"OURTAGS" and that list through the collection spec, as collection_test.go pins them
COLLECTION_LIST_SHA = "60ed944ce678df5c0caea1bf3ff28d8f8f6ebce6f392082d3158fd1c78a5f9ea"
COLLECTION_SYNTH_SHA = "7cd32e2fbe63535ccd17a171b07a8b48227f2bbf3eab90e9cd8762fc5969dc5d"


def sha(b):
    return hashlib.sha256(b).hexdigest()


def tag(code, data):
    if len(data) < 0x3F:
        return struct.pack("<H", (code << 6) | len(data)) + data
    return struct.pack("<HI", (code << 6) | 0x3F, len(data)) + data


def synth():
    """The made-up movie of moviepatch_test.go's synth(): an import, a sprite with a nested long tag, a symbol, a
    placed object at depth 3, two frames."""
    body = bytes([0x00]) + struct.pack("<HH", 24 << 8, 1)
    body += tag(69, struct.pack("<I", 8))
    body += tag(9, b"\x10\x20\x30")
    body += tag(71, b"lib.swf\0\x01\x00" + struct.pack("<HH", 1, 9) + b"Imp\0")
    body += tag(39, struct.pack("<HH", 5, 1) + tag(2, struct.pack("<H", 7) + bytes(70)) + tag(1, b"") + tag(0, b""))
    body += tag(76, struct.pack("<HH", 1, 5) + b"Foo\0")
    body += tag(26, bytes([0x02]) + struct.pack("<HH", 3, 5))
    body += tag(1, b"") + tag(1, b"") + tag(0, b"")
    return b"CFX\x0c" + struct.pack("<I", len(body) + 8 - 100) + plm.zlib_stored(body)


def lcg(n, mod):
    x, out = 1, bytearray()
    for _ in range(n):
        x = (x * 1103515245 + 12345) & 0x7FFFFFFF
        out.append((97 + (x >> 16) % mod) & 0xFF)
    return bytes(out)


def game_dir():
    if os.environ.get("DAWNGATE_GAME"):
        return os.environ["DAWNGATE_GAME"]
    here = REPO
    for _ in range(5):
        cand = os.path.join(here, "client", "ia_Dawngate", "Dawngate")
        if os.path.isfile(os.path.join(cand, "game", "Data", "LoginScreen.snap")):
            return cand
        here = os.path.dirname(here)
    return None


def main(argv):
    if not sys.flags.isolated:
        sys.exit("run this with python -I: it reads the game's files")
    bad = 0

    def check(ok, what):
        nonlocal bad
        print(f"  {'ok     ' if ok else 'FAILED '} {what}")
        bad += not ok

    m = synth()
    check(sha(m) == "28afbd4438f7fc9e187c6c81e86cecbaf714502fab19f24893a385d0386aab68" and len(m) == 170, "the made-up movie")
    out = plm.patch_movie(m, b"OURTAGS")
    check(sha(out) == "2de38d5f994b8b116ec59c105917d030e7e882e68e81afd747f876b30a689b4b" and len(out) == 177,
          "patched as moviepatch_test.go pins it")
    check(plm.zlib_stored(b"").hex() == "7801010000ffff00000001", "an empty stored zlib stream")
    for name, data, want in (
            ("zeros", bytes(70000), "ce16974b6d393c0283bfc283b34a1764d29bb6a9ccfe6f96669eb5e55a597a86"),
            ("lcg7", lcg(200000, 7), "eb802cfe0142a0359ee2c587dd9b17eabce0d9badd896ae64106ad29182e0d2f")):
        check(sha(snap.snappy_compress(data)) == want, f"snappy_compress({name}) as snap/encode_test.go pins it")
    arc = plm.pack_bytes([("a/one.txt", b"hello hello hello hello, said the snappy encoder to the hello"),
                          ("b/two.bin", lcg(200000, 7))])
    check(sha(arc) == "355e68850a27f0b05de2945b07d36d4739556355559093206c05194af1f4bd16", "pack_bytes as Pack pins it")
    for what, data in (("twice", plm.patch_movie(m, plm.read_tags())), ("not a movie", b"FWS\x0c\0\0\0\0")):
        try:
            plm.patch_movie(data, plm.read_tags())
            check(False, f"refused: {what}")
        except plm.MovieError:
            check(True, f"refused: {what}")

    # the class's English fallbacks are the locale package's lines (patch_locale.py, its Go twin localepatch)
    import patch_locale  # noqa: E402
    with open(os.path.join(REPO, "client_ui", "serverpicker", "ServerPicker.as"), encoding="utf-8") as f:
        source = f.read()
    fallbacks = {int(i, 16): t for i, t in re.findall(r'\[(0x5e70a1[0-9a-f]{2}), "((?:[^"\\]|\\.)*)"\]', source)}
    locale = {i: t for i, t in patch_locale.LOGIN_LINES.items() if i >> 8 == 0x5E70A1}
    check(fallbacks == locale and len(locale) == 49,
          f"the picker's {len(fallbacks)} texts are the locale package's {len(locale)} LoginScreen lines")

    tags = plm.read_tags()
    codes = [c for c, *_ in plm.tags_of(tags, 0, len(tags))]
    check(codes == [39, 82, 76, 70], f"picker_tags.hex: DefineSprite, DoABC, SymbolClass, PlaceObject3 ({codes})")

    # the CREATE GAME dialog's bot check box: the same patcher with its spec
    mo = plm.SPECS["matchoptions"]
    mtags = plm.read_tags(mo.tags)
    mt = plm.tags_of(mtags, 0, len(mtags))
    check([c for c, *_ in mt] == [39, 82, 76, 70] and mt[-1][3] == len(mtags),
          "matchoptions_tags.hex: DefineSprite, DoABC, SymbolClass, PlaceObject3")
    check(plm._pairs(mtags, mt[2][2], mt[2][3]) == [(2000, "BotCheckbox")], "its SymbolClass: 2000 -> BotCheckbox")
    check(mtags[mt[3][2]:mt[3][3]] == bytes([0x06, 0]) + struct.pack("<HH", 2000, 2000) + b"\0",
          "its PlaceObject3: character 2000 at depth 2000, no name")
    once = plm.patch_movie(m, mtags, mo)
    check(plm.patch_movie(m, b"OURTAGS", mo) == out, "the spec patches as the picker's (the same bytes)")
    try:
        plm.patch_movie(once, mtags, mo)
        check(False, "refused: the bot check box twice")
    except plm.MovieError as e:
        check("the bot check box already" in str(e), f"refused: the bot check box twice ({e})")
    with open(os.path.join(REPO, "client_ui", "matchoptions", "BotCheckbox.as"), encoding="utf-8") as f:
        mo_as = f.read()
    check('MARKER:String = "~#bots"' in mo_as, "the class's marker is the server's ~#bots")
    # the map dropdown: the class reads the map list the archive holds, and sends ~#<tag> (the server's cutNameTags)
    check(f'MAPS_URL:String = "{plm.MAP_LIST_PATH[len("UI/Scaleform/"):]}"' in mo_as and
          f'MAPS_MAGIC:String = "{plm.MAP_LIST_MAGIC.split(chr(9))[0]}"' in mo_as and
          'FIRST_TAG:String = "sm"' in mo_as and 'tag = "~#" +' in mo_as,
          "the class reads the archive's map list and sends ~#sm or the listed map's tag")
    def pad(text):
        return text + b"\n" * (plm.MAP_LIST_SIZE - len(text))
    check(plm.map_list([]) == pad(b"dawngate-maps\t1\nend\n") and
          plm.map_list(plm.EXTRA_MAPS) == pad(b"dawngate-maps\t1\nmap\tmm\tAnshavar Pass\nend\n") and
          len(plm.map_list([("t", "", "a label of some twenty chars")] * 100)) == plm.MAP_LIST_SIZE,
          "the map list's bytes, padded to 1 KB, as moviepatch's MapList")
    check(plm.snappy_literals(b"") == b"\0" and plm.snappy_literals(b"ab") == b"\x02\x04ab" and
          plm.snappy_literals(bytes(300))[:5] == bytes([0xAC, 0x02, 61 << 2, 0x2B, 0x01]) and
          len(plm.snappy_literals(bytes(plm.MAP_LIST_SIZE))) == 1029 and
          snap.snappy_decompress(plm.snappy_literals(bytes(range(256)) * 300), 76800) == bytes(range(256)) * 300,
          "Snappy literals only, as snap.EncodeLiterals")
    check(plm.server_maps(plm.EXTRA_MAPS, ["mm"]) == list(plm.EXTRA_MAPS) and plm.server_maps(plm.EXTRA_MAPS, []) == []
          and plm.server_maps([], ["mm"]) == [], "server_maps: the copy's maps that the server offers")
    with tempfile.TemporaryDirectory() as d:
        bsnap = os.path.join(d, "Battlegrounds.snap")
        with open(bsnap, "wb") as f:
            f.write(plm.pack_bytes([(mo.movie_path, m)]))
        none = plm.build(bsnap, b"OURTAGS", mo)  # no map archive beside it
        check(sha(none) == "e4994e8df80b86886df2a24a92f90ab698bd91a572be6e0a66cff9ab3a881471" and len(none) == 1326,
              "a made-up dialog archive without maps as moviepatch_test.go pins it")
        with open(os.path.join(d, "zz_MM_Map_v3.snap"), "wb") as f:
            f.write(b"x")
        mm = plm.build(bsnap, b"OURTAGS", mo)  # the map's archive beside it
        check(sha(mm) == "4ecdc1bab98e4ace5f43e7a8711757aaf0da066d4a7f3ec264558bf6e1068105" and len(mm) == 1326,
              "with MM's archive in the copy: the list names MM, as moviepatch_test.go pins it")
        check(plm.build(bsnap, b"OURTAGS", mo, maps=[]) == none, "maps=[] lists none")
        # the front's in-place write (moviepatch.WriteMapList; TestWriteMapList pins the same)
        copy = os.path.join(d, "copy")
        os.makedirs(os.path.join(copy, "game", "Data"))
        arch = plm.map_list_archive(copy)
        with open(arch, "wb") as f:
            f.write(mm)
        changed = plm.write_map_list(copy, [])
        with open(arch, "rb") as f:
            after = f.read()
        check(changed and after == none and not plm.write_map_list(copy, []),
              "write_map_list: a server without MM, in place (the setup's archive without maps)")
        check(plm.map_list_only(after, mm) and not plm.map_list_only(mm, mm) and
              not plm.map_list_only(after[:40] + bytes([after[40] ^ 1]) + after[41:], mm),
              "map_list_only: the front's list, not a changed movie")
        check(plm.write_map_list(copy, plm.EXTRA_MAPS) and open(arch, "rb").read() == mm, "write_map_list: MM again")
        old = plm.pack_bytes([(mo.movie_path, m), (plm.MAP_LIST_PATH, b"dawngate-maps\t1\nend\n")])
        with open(arch, "wb") as f:
            f.write(old)
        try:
            plm.write_map_list(copy, [])
            check(False, "write_map_list refuses an older layout")
        except plm.MapListLayoutError:
            check(open(arch, "rb").read() == old and not plm.map_list_only(old, mm),
                  "write_map_list refuses an older layout, the file unchanged")

    # the chat rooms opened by default: the same patcher with its spec
    cr = plm.SPECS["chatrooms"]
    ctags = plm.read_tags(cr.tags)
    ct = plm.tags_of(ctags, 0, len(ctags))
    check([c for c, *_ in ct] == [39, 82, 76, 70] and ct[-1][3] == len(ctags),
          "chatrooms_tags.hex: DefineSprite, DoABC, SymbolClass, PlaceObject3")
    check(plm._pairs(ctags, ct[2][2], ct[2][3]) == [(2000, "AutoRooms")], "its SymbolClass: 2000 -> AutoRooms")
    check(ctags[ct[3][2]:ct[3][3]] == bytes([0x06, 0]) + struct.pack("<HH", 2000, 2000) + b"\0",
          "its PlaceObject3: character 2000 at depth 2000, no name")
    check(plm.patch_movie(m, b"OURTAGS", cr) == out, "the chat rooms spec patches as the picker's (the same bytes)")
    try:
        plm.patch_movie(plm.patch_movie(m, ctags, cr), ctags, cr)
        check(False, "refused: the chat rooms class twice")
    except plm.MovieError as e:
        check("the chat rooms opened by default already" in str(e), f"refused: the chat rooms class twice ({e})")
    with open(os.path.join(REPO, "client_ui", "chatrooms", "AutoRooms.as"), encoding="utf-8") as f:
        src_as = f.read()
    check("AUTO_CATEGORY_MIN:Number = 1000;" in src_as, "the class's marked categories start at the server's 1000")
    # a new line's sound: the game's interact event 74 (CHAT_NOTIFY_SYS_MESSAGE), its table entry Play_UI_notification
    check("SOUND_EVENT:uint = 74;" in src_as and 'ExternalInterface.call("Global.OnInteractEvent", SOUND_EVENT)' in src_as,
          "the class rings with the game's interact event 74 (Play_UI_notification)")

    game = game_dir()
    if game:
        gsrc = os.path.join(game, "game", "Data", "Global.snap")
        gbuilt = plm.build(gsrc, spec=cr)
        tmp = tempfile.NamedTemporaryFile(suffix=".snap", delete=False)
        tmp.write(gbuilt)
        tmp.close()
        back = snap.Snap(tmp.name)
        graw = back.read(back.entries[0])
        os.remove(tmp.name)
        ids, names, depths, _ = plm.scan(zlib.decompress(graw[8:]))
        check(back.entries[0].name == cr.movie_path and "AutoRooms" in names and "maxis.chat.Chat" in names and
              2000 in ids and 2000 in depths,
              f"the player's own chat movie patched ({len(gbuilt)} bytes, SHA-256 {sha(gbuilt)[:16]})")
        if os.environ.get("DAWNGATE_CHATROOMS_GO"):  # moviepatch's TestBuildChatRooms854 output
            with open(os.environ["DAWNGATE_CHATROOMS_GO"], "rb") as f:
                check(f.read() == gbuilt, "the Go setup's zz_Chat.snap is the same")

    # the After Action Review's tabs: the same patcher with its spec, and the web root of the copy's server
    aa = plm.SPECS["aftaction"]
    atags = plm.read_tags(aa.tags)
    at = plm.tags_of(atags, 0, len(atags))
    check([c for c, *_ in at] == [39, 82, 76, 70] and at[-1][3] == len(atags),
          "aftaction_tags.hex: DefineSprite, DoABC, SymbolClass, PlaceObject3")
    check(plm._pairs(atags, at[2][2], at[2][3]) == [(2000, "AfterActionTabs")], "its SymbolClass: 2000 -> AfterActionTabs")
    check(atags[at[3][2]:at[3][3]] == bytes([0x06, 0]) + struct.pack("<HH", 2000, 2000) + b"\0",
          "its PlaceObject3: character 2000 at depth 2000, no name")
    check(b"Battlegrounds/dgweb.json" in atags and b"dawngate-web\t1" in atags,
          "the class reads Battlegrounds/dgweb.json with its first line")
    check(plm.web_root_file("127.0.0.2") ==
          b"dawngate-web\t1\ncommon\thttp://127.0.0.2:18080/battlegrounds_test/live/www/client/1/common\nend\n",
          "web_root_file as moviepatch.WebRootFile")
    for bad_host in ("", "a b", 'a"b', "a<b", "hé", "a\tb"):
        try:
            plm.web_root_file(bad_host)
            check(False, f"web_root_file refuses {bad_host!r}")
        except ValueError:
            pass
    with tempfile.TemporaryDirectory() as d:
        bsnap = os.path.join(d, "Battlegrounds.snap")
        with open(bsnap, "wb") as f:
            f.write(plm.pack_bytes([(aa.movie_path, m)]))
        for host, want in ((None, "b397d4db173ebbcc86393a26fd73134d90f02d6bee39c08b4a028f6bda27d45c"),
                           ("192.168.1.20", "8a94e55eb9d5f6950880e9d2d06e3eb9c388ffb2994496fb38a40b1e1e6ffd7b")):
            arc = plm.build(bsnap, b"OURTAGS", aa, host=host)
            check(sha(arc) == want, f"a made-up review archive for {host or 'the default host'} as aftaction_test.go "
                                    f"pins it ({len(arc)} bytes)")
    with open(os.path.join(REPO, "client_ui", "aftaction", "AfterActionTabs.as"), encoding="utf-8") as f:
        src_as = f.read()
    check('PAGE:String = "/history.html?embed=1";' in src_as and '"&latest=1"' in src_as,
          "the class's pages: history.html?embed=1 (&latest=1 for Match Stats)")
    if game:
        asrc = os.path.join(game, "game", "Data", "Battlegrounds.snap")
        abuilt = plm.build(asrc, spec=aa)
        tmp = tempfile.NamedTemporaryFile(suffix=".snap", delete=False)
        tmp.write(abuilt)
        tmp.close()
        back = snap.Snap(tmp.name)
        araw = back.read(back.entries[0])
        aroot = back.read(back.entries[1])
        os.remove(tmp.name)
        ids, names, depths, _ = plm.scan(zlib.decompress(araw[8:]))
        check(back.entries[0].name == aa.movie_path and "AfterActionTabs" in names and
              "maxis.AfterActionReview" in names and "AARTabBtn" in names and 2000 in ids and 2000 in depths and
              back.entries[1].name == plm.WEB_ROOT_PATH and aroot == plm.web_root_file(),
              f"the player's own review movie patched ({len(abuilt)} bytes, SHA-256 {sha(abuilt)[:16]})")
        if os.environ.get("DAWNGATE_AFTERACTION_GO"):  # moviepatch's TestBuildAfterAction854 output
            with open(os.environ["DAWNGATE_AFTERACTION_GO"], "rb") as f:
                check(f.read() == abuilt, "the Go setup's zz_AfterActionReview.snap is the same")

    # the item shop's inline refund: the same patcher with its spec
    rs = plm.SPECS["itemshop"]
    rtags = plm.read_tags(rs.tags)
    rt = plm.tags_of(rtags, 0, len(rtags))
    check([c for c, *_ in rt] == [39, 82, 76, 70] and rt[-1][3] == len(rtags),
          "itemshop_tags.hex: DefineSprite, DoABC, SymbolClass, PlaceObject3")
    check(plm._pairs(rtags, rt[2][2], rt[2][3]) == [(2000, "ShopRefund"), (250, "ShopVim")],
          "its SymbolClass: 2000 -> ShopRefund, the shop's own vim symbol 250 -> ShopVim")
    check(rtags[rt[3][2]:rt[3][3]] == bytes([0x06, 0]) + struct.pack("<HH", 2000, 2000) + b"\0",
          "its PlaceObject3: character 2000 at depth 2000, no name")
    check(b"HUD/dgrefund.json" in rtags and b"dawngate-refund\t1" in rtags,
          "the class reads its optional settings, HUD/dgrefund.json")
    with tempfile.TemporaryDirectory() as d:
        hsnap = os.path.join(d, "HUD.snap")
        with open(hsnap, "wb") as f:
            f.write(plm.pack_bytes([(rs.movie_path, m)]))
        arc = plm.build(hsnap, b"OURTAGS", rs)
        check(sha(arc) == ITEMSHOP_SYNTH_SHA, f"a made-up shop archive as itemshop_test.go pins it ({len(arc)} bytes)")
    with open(os.path.join(REPO, "client_ui", "itemshop", "ShopRefund.as"), encoding="utf-8") as f:
        src_as = f.read()
    check('new ShopVim()' in src_as and 'setText(shop.mcCostTxt, price);' in src_as,
          "the badge is the shop's vim symbol (ShopVim), the price under the coin without a plus")
    import patch_locale  # noqa: E402  (the class's texts: the HUD lines the locale patch adds)
    for key, line_id in (("LINE_LABEL", 0x5E70C001), ("LINE_WARN", 0x5E70C002), ("LINE_TIP", 0x5E70C003),
                         ("LINE_WARN_BACK", 0x5E70C004), ("LINE_TIP_BACK", 0x5E70C005)):
        check(f'{key}:String = "^HUD!0x{line_id:08x}";' in src_as and line_id in patch_locale.HUD_LINES,
              f"the class's {key} is the locale patch's HUD line 0x{line_id:08x}")
    if game:
        hsrc = os.path.join(game, "game", "Data", "HUD.snap")
        hbuilt = plm.build(hsrc, spec=rs)
        tmp = tempfile.NamedTemporaryFile(suffix=".snap", delete=False)
        tmp.write(hbuilt)
        tmp.close()
        back = snap.Snap(tmp.name)
        hraw = back.read(back.entries[0])
        os.remove(tmp.name)
        ids, names, depths, _ = plm.scan(zlib.decompress(hraw[8:]))
        check(len(back.entries) == 2 and back.entries[0].name == rs.movie_path and "ShopRefund" in names and
              back.entries[1].name == plm.REFUND_SETTINGS_PATH and back.read(back.entries[1]) == plm.REFUND_SETTINGS and
              "maxis.Hud.ItemShopUI" in names and "ShopVim" in names and 250 in ids and 2000 in ids and
              2000 in depths,
              f"the player's own shop movie patched ({len(hbuilt)} bytes, SHA-256 {sha(hbuilt)[:16]})")
        if os.environ.get("DAWNGATE_ITEMSHOP_GO"):  # moviepatch's TestBuildItemShop854 output
            with open(os.environ["DAWNGATE_ITEMSHOP_GO"], "rb") as f:
                check(f.read() == hbuilt, "the Go setup's zz_ItemShop.snap is the same")
    # the Collection's Items: the same patcher with its spec and the item list from a content.json
    cs = plm.SPECS["collection"]
    ctags = plm.read_tags(cs.tags)
    ct = plm.tags_of(ctags, 0, len(ctags))
    check([c for c, *_ in ct] == [39, 82, 76, 70] and ct[-1][3] == len(ctags),
          "collection_tags.hex: DefineSprite, DoABC, SymbolClass, PlaceObject3")
    check(plm._pairs(ctags, ct[2][2], ct[2][3]) == [(2000, "CollectionItems")],
          "its SymbolClass: 2000 -> CollectionItems")
    check(ctags[ct[3][2]:ct[3][3]] == bytes([0x06, 0]) + struct.pack("<HH", 2000, 2000) + b"\0",
          "its PlaceObject3: character 2000 at depth 2000, no name")
    check(b"Collection/dgitems.json" in ctags and b"dawngate-collitems\t2" in ctags,
          "the class reads its item list, Collection/dgitems.json")
    made_up = os.path.join(REPO, "server", "testdata", "collection", "content.json")
    clist = plm.item_list(made_up)
    check(sha(clist) == COLLECTION_LIST_SHA and clist.count(b"\nitem\t") == 9 and b"Debug" not in clist and
          b"Cheat" not in clist and b"Zeta" not in clist and b"Io\tta" not in clist and
          clist.count(b"\nfilter\t") == 3 and b"\npassive\tunique\tSpark\t" in clist,
          f"the made-up content's item list as collection_test.go pins it ({len(clist)} bytes)")
    check(b"\tadvanced\t1200\tOffense / Defense\t" in clist and b"\tspell\t0\tUtility\t" in clist and
          b"\tGamma\tbasic\t350\t\t" in clist and b"\tconsumable\t50\t\t" in clist,
          "an item's group: the filter groups of its tags in their order, a spell's own (COLLITEMS10)")
    check(plm.item_list(made_up, debug=True) == clist.replace(b"\n", b"\ndebug\t1\n", 1),
          "--debug adds the class's log line switch")
    with tempfile.TemporaryDirectory() as d:
        csnap = os.path.join(d, "code.snap")
        with open(csnap, "wb") as f:
            f.write(plm.pack_bytes([(cs.movie_path, m)]))
        try:
            plm.build(csnap, b"OURTAGS", cs)
            check(False, "no archive without an item list")
        except ValueError:
            check(True, "no archive without an item list")
        arc = plm.build(csnap, b"OURTAGS", cs, content=made_up)
        check(sha(arc) == COLLECTION_SYNTH_SHA,
              f"a made-up Collection archive as collection_test.go pins it ({len(arc)} bytes)")
    if game and os.environ.get("DAWNGATE_COLLECTION_GO"):
        # moviepatch's TestBuildCollection854 output and the content it was made from (<file>.content.json)
        go_out = os.environ["DAWNGATE_COLLECTION_GO"]
        cbuilt = plm.build(os.path.join(game, "game", "Data", "code.snap"), spec=cs,
                           content=go_out + ".content.json")
        with open(go_out, "rb") as f:
            check(f.read() == cbuilt,
                  f"the Go setup's zz_CollectionItems.snap is the same ({len(cbuilt)} bytes, SHA-256 {sha(cbuilt)[:16]})")
    if game:
        bsrc = os.path.join(game, "game", "Data", "Battlegrounds.snap")
        bbuilt = plm.build(bsrc, spec=mo)
        tmp = tempfile.NamedTemporaryFile(suffix=".snap", delete=False)
        tmp.write(bbuilt)
        tmp.close()
        back = snap.Snap(tmp.name)
        braw = back.read(back.entries[0])
        os.remove(tmp.name)
        ids, names, depths, _ = plm.scan(zlib.decompress(braw[8:]))
        check(back.entries[0].name == mo.movie_path and "BotCheckbox" in names and "maxis.MatchOptionsUI" in names and
              2000 in ids and 2000 in depths,
              f"the player's own CREATE GAME movie patched ({len(bbuilt)} bytes, SHA-256 {sha(bbuilt)[:16]})")
        if os.environ.get("DAWNGATE_MATCHOPTIONS_GO"):  # moviepatch's TestBuildMatchOptions854 output
            with open(os.environ["DAWNGATE_MATCHOPTIONS_GO"], "rb") as f:
                check(f.read() == bbuilt, "the Go setup's zz_MatchOptions.snap is the same")
    if game:
        src = os.path.join(game, "game", "Data", "LoginScreen.snap")
        built = plm.build(src)
        tmp = tempfile.NamedTemporaryFile(suffix=".snap", delete=False)
        tmp.write(built)
        tmp.close()
        movie = snap.Snap(tmp.name)
        raw = movie.read(movie.entries[0])
        os.remove(tmp.name)
        ids, names, depths, _ = plm.scan(zlib.decompress(raw[8:]))
        check(plm.CLASS_NAME in names and "maxis.LoginScreen" in names and plm.CHAR_ID in ids and plm.DEPTH in depths,
              f"the player's own login movie patched ({len(built)} bytes, SHA-256 {sha(built)[:16]})")
        check(plm.build(src) == built, "deterministic")
    else:
        print("  skipped  the game's LoginScreen.snap (no client/ia_Dawngate)")

    if "--compile" in argv:
        import subprocess
        r = subprocess.run([sys.executable, "-I", os.path.join(HERE, "build_picker.py"), "check"])
        check(r.returncode == 0, "build_picker.py check: the committed bytes are the source's")
    print(f"{'all passed' if not bad else str(bad) + ' FAILED'}")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
