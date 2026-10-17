"""Write the golden data that pins the Go server's TDF codec, Fire framing and replies (roadmap S1).

Output (both in the given folder, default server/testdata):
  tdf_golden.json      TDF cases and Fire frames, produced by the Python codec tdf.py:
                         "writer" cases: built with tdf.Writer below and with the Go tdf.Writer in
                                         server/internal/tdf/golden_test.go (same case name);
                         "decode" cases: raw bytes the Go decoder must read (types tdf.Writer can't
                                         write, real-shaped client requests);
                         "errors":       truncated or malformed payloads and tdf.py's error message;
                         "frames":       whole Fire frames (header fields plus hex).
                       Every case and frame carries `pretty` = tdf.pretty(tdf.decode(payload), 2), which
                       is exactly what the server logs, so the Go log format is pinned too.
  telemetry_rules.xml  the built-in rules file the HTTP logger serves.

The reply builders (getServerInstance, preAuth, ping, telemetry rules) are the reference versions of
the Python stub's replies (re/stubs/blaze_stub.py, which the client accepted in sessions b1_run1 and
b1_run2; replaced by server/ in S1). preAuth uses a fixed host and QoS port, ping a fixed STIM.
The B5 replies and notifications (login, loginPersona, UserAuthenticated, UserSessionExtendedDataUpdate,
postAuth, getTelemetryServer, enterService) are written from the client's member tables with fixed ids,
session key and time; the client accepted them in session b5_run3.
The client requests are rebuilt from session b1_run2's log with a dummy MAC address (B5's from b5_run2, B8's
createGame request from b8_run4 with a dummy LAN address). The B8 GameManager reply and NotifyGameSetup are
written from the member tables like B5's (session b8_run5). The B7a player-content replies (b7a_*) are written
from the member tables for the account of the server's synthetic content fixture (server/testdata/assetdata);
they haven't met the live client yet (roadmap B7b). The C1 notifications that end a hosted game
(NotifyGameStateChange, NotifyPlayerRemoved) are written from the member tables too (live check: C1b). So are
the B11 chat playgroup's reply and notifications (b11 live check pending); its createPlaygroup request is rebuilt
from session d2f_run2's log. The A5 cases (*_lan) are the address-carrying replies of a server with a public host
(`dgserver --public-host 192.168.1.20`), not yet seen by a live client (A5b). The B10a cases are PvP matchmaking
and the PvP lobby's party (startMatchmaking's reply, NotifyMatchmakingFailed, the matchmade NotifyGameSetup for a
session's owner and for a party guest, the party's playgroup notifications), written from the member tables; their
client requests (startMatchmaking solo and as a party leader, updateNetworkInfo with a region, the party's
createPlaygroup and joinPlaygroup) are rebuilt from a static reading of the client's builders (no live session yet:
B10b).
The B10c cases are the Messaging relay (sendMessage's reply, NotifyMessage for a PvP invite, its answer, a party
chat line and a whisper, Util::filterForProfanity's reply), written from the member tables; their requests (the
invite, the answer, the party chat line, the whisper, the profanity filter request) are rebuilt from a static reading
of the client's senders (no live session yet: B10b/G3).
The B7c cases are progression (perk pages, purchases, the commerce profile, the main menu's wins in the
extended data, the account and stats after a won practice game), written from the member tables for the
fixture accounts; their requests (getPerkPageData, updatePerkPage, purchaseWithEC/PC) are rebuilt from R11's
static reading of the client's senders (no live session yet: B7c-live).
The B7d cases are the profile's three keyed stat groups (ShaperStats, ShaperHistoryStats, RoleHistoryStats: their
getStatGroup replies and the keyed notifications after two hosted games), written from the member tables; their
getStatsByGroupAsync requests are rebuilt from session g2_run2's log with the fixture's persona (the profile itself
hasn't met them live: B7c-live).
The B12 cases are Authentication::logout (the request frame as the client sent it after its AFK kick in session
c1b_run11, and our empty reply frame) and the NotifyPlayerRemoved BLAZESERVER_CONN_LOST that the players still in a
hosted game get when another one's session ends, written from the member table.
The B13 cases are AssociationLists: getLists' reply (an empty list of each requested type, written from the member
tables) for the request as the client sent it in session a5b_run6d (rebuilt from the log's decoded TDF, 137 bytes as
logged), and addUsersToList's requests for the recent players list (rebuilt from a static reading of the client's
builder; its reply is empty).
The B13b cases are the association lists kept per account: getLists' reply with members, the users the client needs
before it (UserSessions UserAdded, UserUpdated) and a friend's presence (UserSessionExtendedDataUpdate with CMAP),
addUsersToList's and removeUsersFromList's replies and NotifyUpdateListMembership, written from the member tables;
their requests (the friend request and accept messages, the friends list's add and remove with MUTA 1, blocking and
unblocking by name, the presence attribute) are rebuilt from a static reading of the chat's friend actions (no live
session yet: B13b's live check).
The B14 cases are the server's system messages (Messaging NotifyMessage with TYPE 300: the kill switches, hiding the
broadcasts, a broadcast for the main menu's marquee) and a party member's extended data with its wins (UserAdded,
UserSessionExtendedDataUpdate), written from the member tables and R9's static reading (live check: B14).
The B15 cases are custom games and the game browser (the custom game's NotifyGameSetup for its creator and a joiner,
NotifyPlayerJoining, NotifyPlayerJoinCompleted, NotifyGameAttribChange with the packed rosters, NotifyPlayerAttribChange,
NotifyGamePlayerTeamChange, GetGameListResponse and NotifyGameListUpdate), written from the member tables; the game
list request is rebuilt from session g3b_run10b's log (289 bytes), the others from a static reading of the client's
senders (live check: B15-live).
The LBFIX cases answer the requests the public server left unanswered: Stats::getLeaderboard's reply (two fixture
rows, and empty), GpsContentController::filePetition's and Rooms::selectCategoryUpdates' replies, written from the
member tables, and the error reply frame for a request without a handler; the leaderboard request is rebuilt from the
public server's log (49 bytes), the player report request from a static reading of its builder with made-up names.

Usage:
    .venv/Scripts/python -I re/scripts/tdf_golden.py [out_dir]
Then run `go test ./...` in server/. Regenerate only on purpose: the Go tests compare against it.
"""
import base64
import json
import os
import struct
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # -I drops the script folder
import tdf  # noqa: E402
from tdf import INT, STRING, STRUCT  # noqa: E402

REPO = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))

# --------------------------------------------------------------------------- replies (from blaze_stub.py)
COMPONENTS = {1: "Authentication", 4: "GameManager", 5: "Redirector", 6: "Playgroups", 7: "Stats",
              9: "Util", 10: "CensusData", 15: "Messaging", 21: "Rooms", 25: "AssociationLists",
              27: "GpsContentController", 28: "GameReporting", 0x7802: "UserSessions",
              # Dawngate's own components (B4, docs/data/blaze_components.txt)
              0x81F: "BattlegroundsAccounts", 0x820: "BGEcommerce", 0x823: "BGOps", 0x825: "Entry",
              0x826: "LivingLore", 0x827: "BGWeb"}


def frame(component, command, msg_type, msg_id, payload=b"", error=0):
    return struct.pack(">HHHHI", len(payload), component, command, error,
                       (msg_type << 28) | (msg_id & 0xFFFFF)) + payload


def get_server_instance(host, blaze_port):
    w = tdf.Writer()
    with w.union("ADDR", 0):
        with w.struct("VALU"):
            w.string("HOST", host)
            w.int("IP", int.from_bytes(bytes(map(int, host.split("."))), "big"))
            w.int("PORT", blaze_port)
    w.int("SECU", 0)
    w.int("XDNS", 0)
    return bytes(w)


def ping_site(host, port):
    return tdf.Writer().string("PSA", host).int("PSP", port).string("SNA", "local")


def preauth(host, qos_port, service):
    w = tdf.Writer()
    w.string("ASRC", "303107")
    w.list("CIDS", tdf.INT, sorted(COMPONENTS))
    with w.struct("CONF"):
        w.map("CONF", tdf.STRING, tdf.STRING,
              {"connIdleTimeout": "90s", "defaultRequestTimeout": "80s", "pingPeriod": "20s"})
    w.string("ESRC", "303107")
    w.string("INST", service)
    w.string("NASP", "cem_ea_id")
    w.string("PILD", "")
    w.string("PLAT", "pc")
    with w.struct("QOSS"):
        with w.struct("BWPS"):
            w.string("PSA", host).int("PSP", qos_port).string("SNA", "local")
        w.int("LNP", 10)
        w.map("LTPS", tdf.STRING, tdf.STRUCT, {"local": ping_site(host, qos_port)})
        w.int("SVID", 0x45410805)
    w.string("RSRC", "303107")
    w.string("SVER", "Blaze 3.19.11.0 (Dawngate restoration stub)")
    return bytes(w)


def ping(stim):
    return bytes(tdf.Writer().int("STIM", stim & 0xFFFFFFFF))


# B5: login to the main menu. Written from the member tables in docs/data/tdf_member_tables.txt (members in
# tag order), independently of the Go builders in server/internal/blaze, with fixed ids, key and time.
B5_MAIL, B5_UID, B5_PID, B5_NAME = "player@example.com", 1000000001, 2000000001, "player"
B5_KEY, B5_TIME, B5_LOCALE, B5_PC = "0123456789abcdef0123456789abcdef", 1791405059, 0x656E5553, 4


def persona_details(w=None):  # Blaze::Authentication::PersonaDetails 0x01462E20
    w = w if w is not None else tdf.Writer()
    return w.string("DSNM", B5_NAME).int("LAST", B5_TIME).int("PID", B5_PID).int("PLAT", B5_PC)


def login_reply():  # Blaze::Authentication::LoginResponse 0x012F9B60
    w = tdf.Writer()
    w.int("ANON", 0).int("NTOS", 0).string("PCTK", "")
    w.list("PLST", STRUCT, [persona_details()])
    w.string("SKEY", B5_KEY).int("SPAM", 0).int("UID", B5_UID).int("UNDR", 0)
    return bytes(w)


def login_persona_reply():  # Blaze::Authentication::SessionInfo 0x012F9938
    w = tdf.Writer()
    w.int("BUID", B5_PID).int("FRST", 1).string("KEY", B5_KEY).int("LLOG", B5_TIME).string("MAIL", B5_MAIL)
    with w.struct("PDTL"):
        persona_details(w)
    w.int("UID", B5_UID)
    return bytes(w)


def user_authenticated():  # Blaze::UserSessionLoginInfo 0x01462488
    w = tdf.Writer()
    w.int("ALOC", B5_LOCALE).int("BUID", B5_PID).string("DSNM", B5_NAME).int("FRST", 1).string("KEY", B5_KEY)
    w.int("LAST", B5_TIME).int("LLOG", B5_TIME).string("MAIL", B5_MAIL).int("PID", B5_PID).int("PLAT", B5_PC)
    w.int("UID", B5_UID).int("XREF", 0)
    return bytes(w)


def extended_data_update(user_info=0):  # Blaze::UserSessionExtendedDataUpdate 0x012F1FF8
    w = tdf.Writer()
    with w.struct("DATA"):  # Blaze::UserSessionExtendedData 0x012F1F70
        w.string("BPS", "local").string("CTY", "")
        with w.struct("QDAT"):  # Blaze::Util::NetworkQosData 0x01462610
            w.int("DBPS", 0).int("NATT", 0).int("UBPS", 0)
        w.int("UATT", user_info)  # B7a: the stored user info attribute
    w.int("SUBS", 1).int("USID", B5_PID)
    return bytes(w)


def telemetry_server(w, host, port):  # Blaze::Util::GetTelemetryServerResponse 0x012FCAD0
    w.string("ADRS", host).int("ANON", 0).string("DISA", "").int("EDCT", 0).string("FILT", "")
    w.int("LOC", B5_LOCALE).int("MINR", 0).string("NOOK", "").int("PORT", port).int("SDLY", 15000)
    w.string("SESS", "").string("SKEY", "").int("SPCT", 75).string("STIM", "").string("SVNM", "")
    return w


def post_auth_reply(host, telemetry_port):  # Blaze::Util::PostAuthResponse 0x012FCCB4
    w = tdf.Writer()
    with w.struct("PSS"):  # Blaze::Util::PssConfig 0x012FCA70
        w.string("ADRS", "").string("PJID", "").int("PORT", 0).int("TIID", 0)
    with w.struct("TELE"):
        telemetry_server(w, host, telemetry_port)
    with w.struct("TICK"):  # Blaze::Util::GetTickerServerResponse 0x012FCA44
        w.string("ADRS", "").int("PORT", 0).string("SKEY", "")
    with w.struct("UROP"):  # Blaze::Util::UserOptions 0x01463318
        w.int("TMOP", 0).int("UID", B5_UID)
    return bytes(w)


def enter_service_reply():  # Blaze::Entry::EnterServiceResponse 0x01462308
    return bytes(tdf.Writer().int("CHEK", 0).int("QPOS", 0).int("RESP", 0).int("WAIT", 0))


# B5 part 2: the meta data after enterService (sessions b5_run4 to b5_run8), up to the main menu.
def bga_basic_data(w=None, avatar=0, flags=0):  # Blaze::BattlegroundsAccounts::BGABasicData 0x012E30C0
    w = w if w is not None else tdf.Writer()
    w.int("ABAD", 0).int("ALEV", 1).int("ALXP", 0).int("ATIL", 0).int("AVTR", avatar).int("BZID", B5_PID)
    return w.int("DEVF", 0).int("FIRS", 0).int("FLAG", flags)


def complete_account_data_reply():  # BGAMetagameGetCompleteAccountDataResponse 0x012E343C
    acct = tdf.Writer()  # BGACompleteAccount 0x012E3244
    with acct.struct("BASD"):
        bga_basic_data(acct)
    return bytes(tdf.Writer().list("LIST", STRUCT, [acct]))


def basic_account_data_reply():  # BGAMetagameGetBasicAccountDataResponse 0x012E3430
    return bytes(tdf.Writer().list("BASD", STRUCT, [bga_basic_data()]))


def commerce_profile_reply():  # Blaze::BGEcommerce::GetCommerceProfileResponse 0x012F34FC
    w = tdf.Writer()
    with w.struct("GCPR"):  # Blaze::BGEcommerce::CommerceProfile 0x012F2DC0
        for label in ("ASEC", "ASPC", "BLCC", "BLEC", "CTEC", "CTPC"):
            w.int(label, 0)
    return bytes(w)


def stats_async_notification():  # Blaze::Stats::KeyScopedStatValues 0x012F06F8
    return bytes(tdf.Writer().string("GRNM", "PlayerStats").string("KEY", "No_Scope_Defined").int("LAST", 1)
                 .int("VID", 1))


B5_META_CASES = {
    "getcompletedata_reply": complete_account_data_reply,
    "getbasicdata_reply": basic_account_data_reply,
    "getcatalog_reply": lambda: bytes(tdf.Writer().string("VERS", "")),  # GetCatalogResponse 0x012F3444
    "getcommerceprofile_reply": commerce_profile_reply,
    "getbalance_reply": lambda: bytes(tdf.Writer().int("CCBL", 0).int("ECBL", 0).int("PCBL", 0)),  # 0x012F3514
    "bgweb_getcurrentversion_reply": lambda: bytes(tdf.Writer().string("DRC", "1")),  # 0x012FC738
    "getstatgroup_reply": lambda: bytes(tdf.Writer().string("CNAM", "").string("DESC", "").string("META", "")
                                        .string("NAME", "PlayerStats")),  # StatGroupResponse 0x012F0178
    "getstatsasync_notification": stats_async_notification,
    "messagecount_reply": lambda: bytes(tdf.Writer().int("MCNT", 0)),  # Fetch/PurgeMessageResponse
    "getgameid_reply": lambda: bytes(tdf.Writer().string("GMID", "")),  # GetPlayerGameIDResponse 0x012EF1C0
    "bgops_getcurrentversion_reply": lambda: bytes(tdf.Writer().string("MENV", "prod-info")
                                                   .string("NVSR", "2014-11-04_10-25-56")),  # 0x012EF208
    "ismmlockedout_reply": lambda: bytes(tdf.Writer().int("LOCK", 0)),  # IsUserMMLockedoutResponse 0x012EF1E8
}


# B8: PLAY -> PRACTICE DAWNGATE -> GameManager::resetDedicatedServer (session b8_run5). Game 1, our GMS at
# 127.0.0.1:10042, the creator's own address forced to loopback.
B8_GMS = (0x7F000001, 10042)
B8_CLIENT = (0x7F000001, 3659)
B8_ATTRS = [("ExpectedPlayerCount", "1"), ("GameFlags", "1292"), ("GameOwnerId", "77359401"),
            ("GameOwnerName", "player"), ("GameType", "1"), ("LevelId", "0")]
B8_DEDICATED_SERVER = 3000000001


def ip_pair(w, ext, internal):  # Blaze::IpPairAddress 0x012F1B10 (EXIP, INIP: Blaze::IpAddress 0x012F1ADC)
    with w.struct("EXIP"):
        w.int("IP", ext[0]).int("PORT", ext[1])
    with w.struct("INIP"):
        w.int("IP", internal[0]).int("PORT", internal[1])
    return w


def host_info(w):  # Blaze::GameManager::HostInfo 0x012E76F0
    return w.int("HPID", B8_DEDICATED_SERVER).int("HSLT", 0)


def notify_game_setup(gms=B8_GMS, client_ext=B8_CLIENT, client_in=B8_CLIENT, pgid=None):
    """Blaze::GameManager::NotifyGameSetup 0x012E7B60: GMS address in HNET, the creator's own in PNET (G3: a hosted
    game's PGID, the persisted id the client saves for a reconnect)."""
    w = tdf.Writer()
    with w.struct("GAME"):  # Blaze::GameManager::ReplicatedGameData 0x014615A8
        w.list("ADMN", INT, [B5_PID])
        # B9: plus ServerBuildVersion, which the client reads once it is connected to the GMS (= VSTR)
        w.map("ATTR", STRING, STRING, B8_ATTRS + [("ServerBuildVersion", "1.0.903.854-RL-Prod-Release")])
        w.list("CAP", INT, [10, 0])
        w.int("GID", 1).string("GNAM", "player's Game").int("GSET", 1284).int("GSTA", 0x82)  # PRE_GAME
        w.list("HNET", STRUCT, [tdf.UnionItem(2, ip_pair(tdf.Writer(), gms, gms))])  # NetworkAddress
        w.int("MCAP", 10).int("NTOP", 1)  # CLIENT_SERVER_DEDICATED
        if pgid is not None:
            w.string("PGID", pgid)
        with w.struct("PHST"):
            host_info(w)
        w.int("PRES", 1).string("PSAS", "local").int("SEED", 7919).int("TCAP", 5)
        with w.struct("THST"):
            host_info(w)
        w.list("TIDS", INT, [1, 2])
        w.string("UUID", "00000000-0000-0000-0000-000000000001").string("VSTR", "1.0.903.854-RL-Prod-Release")
    player = tdf.Writer()  # Blaze::GameManager::ReplicatedGamePlayer 0x01461EA0
    player.int("GID", 1).int("LOC", B5_LOCALE).string("NAME", B5_NAME).int("PID", B5_PID)
    with player.union("PNET", 2):
        with player.struct("VALU"):
            ip_pair(player, client_ext, client_in)
    player.int("SID", 0).int("SLOT", 0).int("STAT", 2).int("TIDX", 0).int("TIME", B5_TIME * 1000000)
    player.int("UID", B5_PID)
    w.list("PROS", STRUCT, [player])
    with w.union("REAS", 0):  # GameSetupReason 0x012EA098 member 0: DatalessSetupContext
        with w.struct("VALU"):
            w.int("DCTX", 0)  # CREATE_GAME_SETUP_CONTEXT
    return bytes(w)


B8_CASES = {
    "resetdedicatedserver_reply": lambda: bytes(tdf.Writer().int("GID", 1).int("JGS", 0)),  # JoinGameResponse
    "notifygamesetup_notification": notify_game_setup,
}

# A5: the same replies from `dgserver --public-host 192.168.1.20` (a LAN setup). Every address handed out is
# that host, and the creator's own addresses go back in PNET as its request sent them (create_game_request:
# EXIP 127.0.0.1:3659, INIP the dummy LAN address 10.0.0.2:3659) instead of being forced to loopback.
A5_PUBLIC = "192.168.1.20"
A5_GMS = (0xC0A80114, 10042)
B8_CLIENT_LAN = (0x0A000002, 3659)
A5_CASES = {
    "getserverinstance_reply_lan": lambda: get_server_instance(A5_PUBLIC, 10041),
    "preauth_reply_lan": lambda: preauth(A5_PUBLIC, 17502, "battlegrounds-1-pc"),
    "postauth_reply_lan": lambda: post_auth_reply(A5_PUBLIC, 9988),
    "gettelemetryserver_reply_lan": lambda: bytes(telemetry_server(tdf.Writer(), A5_PUBLIC, 9988)),
    "notifygamesetup_notification_lan": lambda: notify_game_setup(A5_GMS, B8_CLIENT, B8_CLIENT_LAN),
}

# C1: the notifications that end a hosted game (written from the member tables; not yet seen live, C1b).
C1_CASES = {
    # Blaze::GameManager::NotifyGameStateChange 0x014619F8: GID, GSTA POST_GAME (4)
    "notifygamestatechange_notification": lambda: bytes(tdf.Writer().int("GID", 1).int("GSTA", 4)),
    # Blaze::GameManager::NotifyPlayerRemoved 0x01461328: CNTX 0, GID, PID, REAS GAME_ENDED (5)
    "notifyplayerremoved_notification":
        lambda: bytes(tdf.Writer().int("CNTX", 0).int("GID", 1).int("PID", B5_PID).int("REAS", 5)),
    # C1b: the leaving player's own removal after its removePlayer (REAS 6 PLAYER_LEFT; c1b sessions)
    "notifyplayerremoved_playerleft_notification":
        lambda: bytes(tdf.Writer().int("CNTX", 0).int("GID", 1).int("PID", B5_PID).int("REAS", 6)),
}

# B12: a player whose Blaze session ended (Authentication::logout after the client's AFK kick, or a closed
# connection) leaves a running hosted game: the players still in it get NotifyPlayerRemoved with REAS 2
# BLAZESERVER_CONN_LOST (table 0x01461328, written from the member table; not yet seen live: G3).
B12_CASES = {
    "notifyplayerremoved_connlost_notification":
        lambda: bytes(tdf.Writer().int("CNTX", 0).int("GID", 1).int("PID", B5_PID).int("REAS", 2)),
}


# a logged-in account that the admin page disables, bans or deletes loses its session at once. The UserSessions
# notifications written from the member tables (UserSessionDisconnectReason 0x01462658: SDR, only DUPLICATE_LOGIN 0;
# UserSessionLogoutInfo 0x014625F0: BID, LOTP LOGOUT_TYPE_ACCOUNT 0) and the kick out of a hosted game (NotifyPlayerRemoved
# REAS 8 PLAYER_KICKED). Live (test runs): UserSessionDisconnected (the client disconnects itself with
# ERR_DUPLICATE_LOGIN and shows the login screen), UserUnauthenticated, PLAYER_KICKED (in a practice match).
PH5B_CASES = {
    "usersessiondisconnected_notification": lambda: bytes(tdf.Writer().int("SDR", 0)),
    "userunauthenticated_notification": lambda: bytes(tdf.Writer().int("BID", B5_PID).int("LOTP", 0)),
    "notifyplayerremoved_kicked_notification":
        lambda: bytes(tdf.Writer().int("CNTX", 0).int("GID", 1).int("PID", B5_PID).int("REAS", 8)),
}

# B7a: player content. The account of the synthetic content fixture (server/testdata/assetdata: made-up
# items, no game data) under the server's default policy: everything owned except the testing-only shaper
# and its skin, EC 10000, PC 1000, avatar = the lowest-catalogID avatar item, FLAG bit 0 (FTUE done,
# dgserver --ftue-done), user info attribute 1<<62 (the news overlay's setUserInfoAttribute, b8_run3).
B7A_AVATAR, B7A_FTUE_DONE, B7A_USER_INFO = 0x1000000A, 1, 1 << 62
B7A_INVENTORY = [  # BGAInventoryItem: ITID catalogID, TYPE BGEcommerce item type, CONT count
    (0x10000001, 1, 1), (0x10000002, 1, 1), (0x10000004, 2, 1), (0x10000006, 4, 2), (0x10000007, 3, 3),
    (0x10000008, 5, 1), (0x10000009, 6, 1), (0x1000000A, 7, 1), (0x1000000B, 9, 1)]
B7A_GAME_ITEMS = [  # GameItem: ITID, TYPE, STCK (max ownable), ENBL (not testing-only)
    (0x10000001, 1, 1, 1), (0x10000002, 1, 1, 1), (0x10000003, 1, 1, 0), (0x10000004, 2, 1, 1),
    (0x10000005, 2, 1, 0), (0x10000006, 4, 2, 1), (0x10000007, 3, 3, 1), (0x10000008, 5, 1, 1),
    (0x10000009, 6, 1, 1), (0x1000000A, 7, 1, 1), (0x1000000B, 9, 1, 1)]
B7A_OFFERS = [  # catalogID, EC price, PC price (our made-up prices by item type); since R11 the hidden
    # loadout page is sold too (buying a perk page needs an offer for it)
    (0x10000001, 4800, 975), (0x10000002, 4800, 975), (0x10000004, 0, 750), (0x10000006, 500, 0),
    (0x10000007, 150, 0), (0x1000000B, 2000, 300)]
B7A_SETTINGS = {"Audio.Master": "0.5", "Video.Width": "1280"}
B7A_PLAYER_STATS = ["ASSISTS", "ASSISTSINGAME", "KILLS", "KILLSINGAME", "DOUBLEKILLS", "TRIPLEKILLS", "OMNIKILLS",
                    "TETRAKILLS", "STREAKINGAME", "WINS", "PLAYED", "MPS", "MPSG"]


def b7a_complete_account_data():  # BGAMetagameGetCompleteAccountDataResponse 0x012E343C
    acct = tdf.Writer()  # BGACompleteAccount 0x012E3244
    with acct.struct("BASD"):
        bga_basic_data(acct, B7A_AVATAR, B7A_FTUE_DONE)
    acct.list("ITEM", STRUCT, [tdf.Writer().int("CONT", n).int("ITID", i).int("TYPE", t)  # BGAInventoryItem
                               for i, t, n in B7A_INVENTORY])
    return bytes(tdf.Writer().list("LIST", STRUCT, [acct]))


def b7a_catalog():  # Blaze::BGEcommerce::GetCatalogResponse 0x012F3444
    offers = []
    for cid, ec, pc in B7A_OFFERS:  # PurchasableUnitTdf 0x012F2F08
        o = tdf.Writer()
        with o.struct("ERNC"):  # PurchaseOption 0x012F2EC8
            o.int("AVIL", int(ec > 0)).int("PAIC", ec)
        o.int("FLGS", 1).list("ITMC", INT, [1]).list("ITMI", INT, [cid])  # BASE_FLAG
        with o.struct("PAIC"):
            o.int("AVIL", int(pc > 0)).int("PAIC", pc)
        # PUID and PUII: the catalogID in decimal. PUII is the client's offer key (FNV-1) and a purchase's
        # PROI, so it must be unique per offer (R11, docs/ACCOUNT_CONTENT.md)
        o.string("PUID", str(cid)).string("PUII", str(cid))
        offers.append(o)
    return bytes(tdf.Writer().list("CATA", STRUCT, offers).string("VERS", "1"))


def b7a_game_items():  # Blaze::BGEcommerce::GetGameItemsResponse 0x012F3554
    return bytes(tdf.Writer().list("ITMS", STRUCT, [  # GameItem 0x014626F0
        tdf.Writer().int("ENBL", e).int("ITID", i).int("RARE", 0).int("STCK", s).int("TYPE", t)
        for i, t, s, e in B7A_GAME_ITEMS]))


def b7a_stat_group():  # Blaze::Stats::StatGroupResponse 0x012F0178 with StatDescSummary 0x012EFFF8
    w = tdf.Writer().string("CNAM", "").string("DESC", "").string("META", "").string("NAME", "PlayerStats")
    return bytes(w.list("STAT", STRUCT, [tdf.Writer().string("DFLT", "0").string("FRMT", "%d").string("LDSC", s)
                                         .string("NAME", s).string("SDSC", s) for s in B7A_PLAYER_STATS]))


def b7a_stats_async():  # Blaze::Stats::KeyScopedStatValues 0x012F06F8
    w = tdf.Writer().string("GRNM", "PlayerStats").string("KEY", "No_Scope_Defined").int("LAST", 1)
    with w.struct("STS"):  # StatValues 0x012F0128, EntityStats 0x012F0090
        w.list("STAT", STRUCT, [tdf.Writer().int("EID", B5_PID).list("STAT", STRING, ["0"] * len(B7A_PLAYER_STATS))])
    return bytes(w.int("VID", 1))


B7A_CASES = {
    "b7a_getcompletedata_reply": b7a_complete_account_data,
    "b7a_getbasicdata_reply": lambda: bytes(tdf.Writer().list(
        "BASD", STRUCT, [bga_basic_data(None, B7A_AVATAR, B7A_FTUE_DONE)])),
    "b7a_getbalance_reply": lambda: bytes(tdf.Writer().int("CCBL", 0).int("ECBL", 10000).int("PCBL", 1000)),
    "b7a_getcatalog_reply": b7a_catalog,
    "b7a_getgameitems_reply": b7a_game_items,
    "b7a_getusersettings_reply": lambda: bytes(tdf.Writer().map("USET", STRING, STRING, B7A_SETTINGS)),
    "b7a_getstatgroup_reply": b7a_stat_group,
    "b7a_getstatsasync_notification": b7a_stats_async,
    "b7a_extendeddataupdate_notification": lambda: extended_data_update(B7A_USER_INFO),
}


# B7c: progression, written from the member tables (no live client yet: B7c-live). Perk pages (R11's layout,
# docs/ACCOUNT_CONTENT.md §7), purchases (§5), the commerce profile (§6), the main menu's wins (DMAP), and the
# account after a won 12-minute practice game. Two fixture accounts: the B7a one (everything owned) with its
# first perk page, and a starter account (--owned starter, EC 10000, PC 1000) that buys the fixture's perk
# shape Spark (0x10000006, 500 EC), its gem RedGem (0x10000007, 150 EC) and a loadout page (0x1000000B, 300 PC).
B7C_PAGE_LIMIT = 10  # dgserver --perk-pages default: the loadout page's GameItem.STCK [assumed]
B7C_SPARK, B7C_RED_GEM, B7C_PAGE_ITEM = 0x10000006, 0x10000007, 0x1000000B
B7C_PAGE1_EDITED = [(B7C_SPARK, 5, 1, [(B7C_RED_GEM, 0)])]  # Spark at cell 5 (row 1, column 1), turned once, RedGem in socket 0
B7C_STATS = {"ASSISTS": "2", "ASSISTSINGAME": "2", "KILLS": "3", "KILLSINGAME": "3", "DOUBLEKILLS": "1",
             "STREAKINGAME": "3", "WINS": "1", "PLAYED": "1"}  # kills 3, deaths 1, assists 2, streak 3, one double kill, won


def b7c_perk_page(w, pid, name, vers, shapes=()):  # BGACompletePerkPage 0x012E322C
    w.list("PERK", STRUCT, [  # BGAPerkPageShape 0x012E31C8, BGAPerkPageGem 0x012E31B0
        tdf.Writer().list("GEMS", STRUCT, [tdf.Writer().int("GID", g).int("SIDX", s) for g, s in gems])
        .int("GIDX", cell).int("PID", shape).int("ROT", rot) for shape, cell, rot, gems in shapes])
    with w.struct("SUMM"):  # BGASummaryPerkPage 0x012E31F8
        w.string("NAME", name).int("PID", pid).int("STAT", 1).int("VERS", vers)
    return w


def b7c_complete_account_data():  # B7a's account with its first perk page (BGACompleteAccount PERK)
    acct = tdf.Writer()
    with acct.struct("BASD"):
        bga_basic_data(acct, B7A_AVATAR, B7A_FTUE_DONE)
    acct.list("ITEM", STRUCT, [tdf.Writer().int("CONT", n).int("ITID", i).int("TYPE", t) for i, t, n in B7A_INVENTORY])
    acct.list("PERK", STRUCT, [b7c_perk_page(tdf.Writer(), 1, "Page 1", 1)])
    return bytes(tdf.Writer().list("LIST", STRUCT, [acct]))


def b7c_game_items():  # GetGameItemsResponse with the loadout page's STCK = the page limit
    return bytes(tdf.Writer().list("ITMS", STRUCT, [
        tdf.Writer().int("ENBL", e).int("ITID", i).int("RARE", 0)
        .int("STCK", B7C_PAGE_LIMIT if i == B7C_PAGE_ITEM else s).int("TYPE", t) for i, t, s, e in B7A_GAME_ITEMS]))


def b7c_perk_page_data(pages):  # BGAGetPerkPageDataResponse 0x012E3480
    return bytes(tdf.Writer().list("PPGS", STRUCT, [b7c_perk_page(tdf.Writer(), *p) for p in pages]).int("USER", B5_PID))


def b7c_basic_data_after_game():  # BGAMetagameGetBasicAccountDataResponse: level 2 with 124 XP (50 + 50 won + 2 x 12 min)
    w = tdf.Writer().int("ABAD", 0).int("ALEV", 2).int("ALXP", 124).int("ATIL", 0).int("AVTR", B7A_AVATAR)
    w.int("BZID", B5_PID).int("DEVF", 0).int("FIRS", 0).int("FLAG", 1)
    return bytes(tdf.Writer().list("BASD", STRUCT, [w]))


def b7c_extended_data_wins(wins):  # UserSessionExtendedDataUpdate with DMAP {0x00070034: wins} (R11)
    w = tdf.Writer()
    with w.struct("DATA"):
        w.string("BPS", "local").string("CTY", "").map("DMAP", INT, INT, [(0x00070034, wins)])
        with w.struct("QDAT"):
            w.int("DBPS", 0).int("NATT", 0).int("UBPS", 0)
        w.int("UATT", 0)
    return bytes(w.int("SUBS", 1).int("USID", B5_PID))


def b7c_commerce_profile(asec, aspc, ctec, ctpc):  # GetCommerceProfileResponse 0x012F34FC, CommerceProfile 0x012F2DC0
    w = tdf.Writer()
    with w.struct("GCPR"):
        w.int("ASEC", asec).int("ASPC", aspc).int("BLCC", 0).int("BLEC", 0).int("CTEC", ctec).int("CTPC", ctpc)
    return bytes(w)


def b7c_stats_async():  # KeyScopedStatValues after the won game, in the group's stat order
    w = tdf.Writer().string("GRNM", "PlayerStats").string("KEY", "No_Scope_Defined").int("LAST", 1)
    with w.struct("STS"):
        w.list("STAT", STRUCT, [tdf.Writer().int("EID", B5_PID).list(
            "STAT", STRING, [B7C_STATS.get(s, "0") for s in B7A_PLAYER_STATS])])
    return bytes(w.int("VID", 1))


B7C_CASES = {
    "b7c_getcompletedata_reply": b7c_complete_account_data,
    "b7c_getgameitems_reply": b7c_game_items,
    "b7c_getperkpagedata_reply": lambda: b7c_perk_page_data([(1, "Page 1", 1)]),
    "b7c_getperkpagedata_two_reply":
        lambda: b7c_perk_page_data([(1, "Page 1", 2, B7C_PAGE1_EDITED), (2, "Page 2", 1)]),
    # BGAUpdatePerkPageResponse 0x01460FF4: ENUM PERKPAGEUPDATE_ENUM_SUCCESS 1 / FAILED 2
    "b7c_updateperkpage_reply": lambda: bytes(tdf.Writer().int("ENUM", 1)),
    "b7c_updateperkpage_failed_reply": lambda: bytes(tdf.Writer().int("ENUM", 2)),
    # PurchaseWithECResponse 0x012F34B0 (Spark: 10000 - 500), PurchaseWithPCResponse 0x012F3470 (page: 1000 - 300)
    "b7c_purchasewithec_reply": lambda: bytes(tdf.Writer().int("BZID", B5_PID).int("CNT", 1).int("ECLB", 9500)
                                              .list("ITMS", INT, [B7C_SPARK]).list("QNTS", INT, [1])),
    "b7c_purchasewithpc_reply": lambda: bytes(tdf.Writer().int("BZID", B5_PID).int("CNT", 1)
                                              .list("ITMS", INT, [B7C_PAGE_ITEM]).int("PCBL", 700).list("QNTS", INT, [1])),
    # GetCommerceProfileResponse: ASEC 500 + 150, ASPC 300, CTEC 2, CTPC 1 (BLCC, BLEC unread: 0)
    "b7c_getcommerceprofile_reply": lambda: b7c_commerce_profile(650, 300, 2, 1),
    "b7c_extendeddataupdate_notification": lambda: b7c_extended_data_wins(1),
    "b7c_getbasicdata_reply": b7c_basic_data_after_game,
    "b7c_getstatsasync_notification": b7c_stats_async,
}


# B7d: the profile's shaper and role history (docs/ACCOUNT_CONTENT.md §11), written from the member tables (no live
# client yet: B7c-live). The fixture account plays two hosted games: game 1 as Frost Ember (the skin's Noun
# ember_frost.Noun, whose shaper is ember 0x10000001) in the Gladiator role (1), won, 12 minutes, kills 3, deaths 1,
# assists 2, creep score 40, gold 5000; game 2 two days later as Stone (stone.Noun, 0x10000002) in the Hunter role
# (3), lost, 30 minutes, kills 1, deaths 4, assists 6, creep score 90, gold 9000; the profile is looked at on game
# 2's day. The keyed groups answer with one KeyScopedStatValues per key ("shaper=<catalogID>", "role=<1-4>"), LAST on
# the last only; the daily groups (ShaperHistoryStats, RoleHistoryStats) carry each row's day as POFF (days before
# today).
B7D_EMBER, B7D_STONE = 0x10000001, 0x10000002
B7D_GROUP_STATS = {  # our stat names, in the order the client reads the values (it never reads the names)
    "ShaperStats": ["PLAYED", "WINS"],
    "ShaperHistoryStats": ["PLAYED", "WINS", "KILLS", "DEATHS", "ASSISTS", "CS", "VIM", "SECONDS"],
    "RoleHistoryStats": ["PLAYED", "WINS"],
}


def b7d_stat_group(group):  # StatGroupResponse 0x012F0178 with StatDescSummary 0x012EFFF8, as b7a_stat_group
    w = tdf.Writer().string("CNAM", "").string("DESC", "").string("META", "").string("NAME", group)
    return bytes(w.list("STAT", STRUCT, [tdf.Writer().string("DFLT", "0").string("FRMT", "%d").string("LDSC", s)
                                         .string("NAME", s).string("SDSC", s) for s in B7D_GROUP_STATS[group]]))


def b7d_keyed(group, vid, key, last, rows):  # KeyScopedStatValues 0x012F06F8 for one key; rows [(POFF, values)]
    daily = group != "ShaperStats"
    w = tdf.Writer().string("GRNM", group).string("KEY", key).int("LAST", last)
    with w.struct("STS"):  # StatValues 0x012F0128: one EntityStats 0x012F0090 per row
        entities = []
        for poff, values in rows:
            e = tdf.Writer().int("EID", B5_PID)
            if daily:
                e.int("POFF", poff)
            entities.append(e.list("STAT", STRING, [str(v) for v in values]))
        w.list("STAT", STRUCT, entities)
    return bytes(w.int("VID", vid))


B7D_CASES = {
    "b7d_getstatgroup_shaperstats_reply": lambda: b7d_stat_group("ShaperStats"),
    "b7d_getstatgroup_shaperhistorystats_reply": lambda: b7d_stat_group("ShaperHistoryStats"),
    "b7d_getstatgroup_rolehistorystats_reply": lambda: b7d_stat_group("RoleHistoryStats"),
    # no history yet: one notification with an empty KEY (the view stores none) and LAST, no STS
    "b7d_shaperstats_empty_notification":
        lambda: bytes(tdf.Writer().string("GRNM", "ShaperStats").string("KEY", "").int("LAST", 1).int("VID", 2)),
    # after the two games (VID 2-4 as the client numbered its views in g2_run2)
    "b7d_shaperstats_ember_notification":
        lambda: b7d_keyed("ShaperStats", 2, f"shaper={B7D_EMBER}", 0, [(0, [1, 1])]),
    "b7d_shaperstats_stone_notification":
        lambda: b7d_keyed("ShaperStats", 2, f"shaper={B7D_STONE}", 1, [(0, [1, 0])]),
    "b7d_shaperhistorystats_ember_notification":
        lambda: b7d_keyed("ShaperHistoryStats", 3, f"shaper={B7D_EMBER}", 0, [(2, [1, 1, 3, 1, 2, 40, 5000, 720])]),
    "b7d_shaperhistorystats_stone_notification":
        lambda: b7d_keyed("ShaperHistoryStats", 3, f"shaper={B7D_STONE}", 1, [(0, [1, 0, 1, 4, 6, 90, 9000, 1800])]),
    "b7d_rolehistorystats_gladiator_notification":
        lambda: b7d_keyed("RoleHistoryStats", 4, "role=1", 0, [(2, [1, 1])]),
    "b7d_rolehistorystats_hunter_notification":
        lambda: b7d_keyed("RoleHistoryStats", 4, "role=3", 1, [(0, [1, 0])]),
}


# B11: the game's chat playgroup. After joining game 1 the client sends Playgroups::createPlaygroup for "GG-1"
# (d2f_run2); the server answers with playgroup 1, owned by the creator, then NotifyJoinPlaygroup. A second
# player (persona 2000000002, "second") joining the same key gets the same playgroup, and the first member is
# told with NotifyMemberJoinedPlaygroup; a member that disconnects goes with NotifyMemberRemoveFromPlaygroup.
B11_KEY, B11_PGID = "GG-1", 1
B11_SECOND_PID, B11_SECOND_NAME, B11_SECOND_UID = 2000000002, "second", 1000000002


def playgroup_info(w):  # Blaze::Playgroups::PlaygroupInfo 0x01462028 (HNET, XNNC, XSES left out: defaults)
    w.map("ATTR", STRING, STRING, [("PlaygroupKey", B11_KEY)])
    w.int("ENBV", 0).int("HSID", 0).int("JOIN", 0).int("MLIM", 15).string("NAME", B11_KEY)
    w.int("NTOP", 0).int("OWNR", B5_PID).int("PGID", B11_PGID).int("PRES", 0).string("UKEY", B11_KEY)
    return w.int("UPRS", 0).string("UUID", "").int("VOIP", 0)


def playgroup_member(w, uid, pid, name, slot):  # Blaze::Playgroups::PlaygroupMemberInfo 0x012ED170
    w.int("JTIM", B5_TIME).int("PERM", 0).int("SID", slot)
    with w.struct("USER"):  # Blaze::UserIdentification 0x012F1C70
        w.int("AID", uid).int("ALOC", B5_LOCALE).int("ID", pid).string("NAME", name)
    return w


def join_playgroup_reply():  # Blaze::Playgroups::JoinPlaygroupResponse 0x012ED270
    w = tdf.Writer()
    with w.struct("INFO"):
        playgroup_info(w)
    return bytes(w)


def notify_join_playgroup():  # Blaze::Playgroups::NotifyJoinPlaygroup 0x012ED288
    w = tdf.Writer()
    with w.struct("INFO"):
        playgroup_info(w)
    w.list("MLST", STRUCT, [playgroup_member(tdf.Writer(), B5_UID, B5_PID, B5_NAME, 0)])
    return bytes(w.int("USER", B5_PID))


def notify_member_joined_playgroup():  # Blaze::Playgroups::NotifyMemberJoinedPlaygroup 0x012ED2B0
    w = tdf.Writer()
    with w.struct("MEMB"):
        playgroup_member(w, B11_SECOND_UID, B11_SECOND_PID, B11_SECOND_NAME, 1)
    return bytes(w.int("PGID", B11_PGID))


B11_CASES = {
    "createplaygroup_reply": join_playgroup_reply,
    "notifyjoinplaygroup_notification": notify_join_playgroup,
    "notifymemberjoinedplaygroup_notification": notify_member_joined_playgroup,
    # Blaze::Playgroups::NotifyMemberRemoveFromPlaygroup 0x01461FF8: MLST (the member's id), PGID,
    # REAS PLAYGROUP_MEMBER_REMOVE_REASON_DISCONNECTED (1)
    "notifymemberremovedfromplaygroup_notification":
        lambda: bytes(tdf.Writer().int("MLST", B11_SECOND_PID).int("PGID", B11_PGID).int("REAS", 1)),
}


# B10a: PvP matchmaking and the PvP lobby's party, written from the member tables (read statically; nothing
# was seen live yet, B10b). Game 1 is matchmade from session 1 of the first persona; the party is playgroup 1,
# led by the first persona, with the second as its guest.
B10A_VERSION = "1.0.903.854-RL-Prod-Release"
B10A_ATTRS = [("ExpectedPlayerCount", "10"), ("GameFlags", "1"), ("GameOwnerId", "77359401"),
              ("GameOwnerName", "player"), ("GameType", "1"), ("LevelId", "0")]
B10A_PARTY_NAME = "2f1e4c9a-0b7d-4e21-9c3f-5a6b7c8d9"  # a UuidToStringA cut to 33 characters
B10A_PING_SITES = [("eu", 150), ("eu-any", 200), ("na", 50), ("na-any", 100)]  # GameServerRegionList (R6)


def objid(w, label, component, typ, ident):
    w.out += hdr(label, tdf.OBJID) + vi(component) + vi(typ) + vi(ident)
    return w


def b10a_game(w, admin, state=0x82, pgid=None, cap=10, tcap=5):
    """Blaze::GameManager::ReplicatedGameData 0x014615A8 of matchmade game 1 (G3: a later state, the PGID, the 1 v 1
    capacities)."""
    w.list("ADMN", INT, [admin])
    w.map("ATTR", STRING, STRING, B10A_ATTRS + [("ServerBuildVersion", B10A_VERSION)])
    w.list("CAP", INT, [cap, 0])
    w.int("GID", 1).string("GNAM", "player's Game").int("GSET", 0x506).int("GSTA", state)
    w.list("HNET", STRUCT, [tdf.UnionItem(2, ip_pair(tdf.Writer(), B8_GMS, B8_GMS))])
    w.int("MCAP", cap).int("NTOP", 1)
    if pgid is not None:
        w.string("PGID", pgid)
    with w.struct("PHST"):
        host_info(w)
    w.int("PRES", 1).string("PSAS", "local").int("SEED", 7919).int("TCAP", tcap)
    with w.struct("THST"):
        host_info(w)
    w.list("TIDS", INT, [1, 2])
    return w.string("UUID", "00000000-0000-0000-0000-000000000001").string("VSTR", B10A_VERSION)


def b10a_player(pid, name, slot, team):  # Blaze::GameManager::ReplicatedGamePlayer 0x01461EA0
    p = tdf.Writer()
    p.int("GID", 1).int("LOC", B5_LOCALE).string("NAME", name).int("PID", pid)
    with p.union("PNET", 2):
        with p.struct("VALU"):
            ip_pair(p, B8_CLIENT, B8_CLIENT)
    p.int("SID", slot).int("SLOT", 0).int("STAT", 2).int("TIDX", team).int("TIME", B5_TIME * 1000000)
    return p.int("UID", pid)


def b10a_game_setup():
    """NotifyGameSetup of a matchmade game for the session's owner: REAS = GameSetupReason member 3,
    Blaze::GameManager::MatchmakingSetupContext 0x014613E8 (FIT, MAXF, MSID, RSLT SUCCESS_CREATED_GAME, USID)."""
    w = tdf.Writer()
    with w.struct("GAME"):
        b10a_game(w, B5_PID)
    w.list("PROS", STRUCT, [b10a_player(B5_PID, B5_NAME, 0, 0)])
    with w.union("REAS", 3):
        with w.struct("VALU"):
            w.int("FIT", 0).int("MAXF", 0).int("MSID", 1).int("RSLT", 0).int("USID", B5_PID)
    return bytes(w)


def b10a_game_setup_party_member():
    """NotifyGameSetup of the party's game for its guest: REAS = member 4,
    Blaze::GameManager::IndirectMatchmakingSetupContext 0x01461CE8 (FIT, GRID = the party, MAXF, MSID, RPVC, RSLT,
    USID)."""
    w = tdf.Writer()
    with w.struct("GAME"):
        b10a_game(w, B5_PID)
    w.list("PROS", STRUCT, [b10a_player(B5_PID, B5_NAME, 0, 0), b10a_player(B11_SECOND_PID, B11_SECOND_NAME, 1, 0)])
    with w.union("REAS", 4):
        with w.struct("VALU"):
            w.int("FIT", 0)
            objid(w, "GRID", 6, 1, 1)
            w.int("MAXF", 0).int("MSID", 1).int("RPVC", 0).int("RSLT", 0).int("USID", B11_SECOND_PID)
    return bytes(w)


def b10a_party_info(w):  # Blaze::Playgroups::PlaygroupInfo 0x01462028 of a PvP party (createPlaygroup JOIN 0)
    w.map("ATTR", STRING, STRING, []).int("ENBV", 0).int("HSID", 0).int("JOIN", 0).int("MLIM", 5)
    w.string("NAME", B10A_PARTY_NAME).int("NTOP", 0).int("OWNR", B5_PID).int("PGID", 1).int("PRES", 0)
    return w.string("UKEY", "").int("UPRS", 0).string("UUID", "").int("VOIP", 0)


def b10a_party_reply():  # Blaze::Playgroups::JoinPlaygroupResponse 0x012ED270
    w = tdf.Writer()
    with w.struct("INFO"):
        b10a_party_info(w)
    return bytes(w)


def b10a_party_join():  # Blaze::Playgroups::NotifyJoinPlaygroup 0x012ED288: the leader with PERM 0x1F
    w = tdf.Writer()
    with w.struct("INFO"):
        b10a_party_info(w)
    leader = tdf.Writer().int("JTIM", B5_TIME).int("PERM", 0x1F).int("SID", 0)
    with leader.struct("USER"):
        leader.int("AID", B5_UID).int("ALOC", B5_LOCALE).int("ID", B5_PID).string("NAME", B5_NAME)
    w.list("MLST", STRUCT, [leader])
    return bytes(w.int("USER", B5_PID))


B10A_CASES = {
    # Blaze::GameManager::StartMatchmakingResponse 0x012E8328
    "startmatchmaking_reply": lambda: bytes(tdf.Writer().int("MSID", 1)),
    # Blaze::GameManager::NotifyMatchmakingFailed 0x01461388: MAXF, MSID, RSLT SESSION_CANCELED (4), USID
    "notifymatchmakingfailed_notification":
        lambda: bytes(tdf.Writer().int("MAXF", 0).int("MSID", 1).int("RSLT", 4).int("USID", B5_PID)),
    "notifygamesetup_matchmade_notification": b10a_game_setup,
    "notifygamesetup_party_member_notification": b10a_game_setup_party_member,
    "createplaygroup_party_reply": b10a_party_reply,
    "notifyjoinplaygroup_party_notification": b10a_party_join,
    # Blaze::Playgroups::NotifyPlaygroupAttributesSet 0x012ED358: the leader's MatchmakingStatus (kGEMS 1)
    "notifyplaygroupattributesset_notification":
        lambda: bytes(tdf.Writer().map("ATTR", STRING, STRING, [("MatchmakingStatus", "1|1|0")]).int("PGID", 1)),
    # Blaze::Playgroups::NotifyMemberAttributesSet 0x012ED3A8: the guest's memberGEM
    "notifymemberattributesset_notification":
        lambda: bytes(tdf.Writer().map("ATTR", STRING, STRING, [("memberGEM", "0")]).int("EID", B11_SECOND_PID)
                      .int("PGID", 1)),
    # Blaze::Playgroups::NotifyLeaderChange 0x012ED2E0 and NotifyMemberPermissionsChange 0x012ED310: the guest leads
    "notifyleaderchange_notification":
        lambda: bytes(tdf.Writer().int("HSID", 1).int("LID", B11_SECOND_PID).int("PGID", 1)),
    "notifymemberpermissionschange_notification":
        lambda: bytes(tdf.Writer().int("LID", B11_SECOND_PID).int("PERM", 0x1F).int("PGID", 1)),
    # Blaze::Playgroups::NotifyJoinControlsChange 0x01461FB8: PLAYGROUP_CLOSED (1)
    "notifyjoincontrolschange_notification": lambda: bytes(tdf.Writer().int("OPEN", 1).int("PGID", 1)),
    # Blaze::Playgroups::NotifyDestroyPlaygroup 0x01462110: REAS PLAYGROUP_DESTROY_REASON_DEFAULT (0)
    "notifydestroyplaygroup_notification": lambda: bytes(tdf.Writer().int("PGID", 1).int("REAS", 0)),
}


# B10c: the Messaging relay (read statically; nothing seen live yet, B10b/G3). The first persona ("player") invites
# the second to PvP party 1 (message 1), the second accepts (message 2), the leader chats in the party with echo and
# the profanity filter (message 3) and whispers to the second, persistent (message 4). ATTR keys and values as the
# client builds them (GameEntryInviteManager 0x0067F100/0x0067EE10, cBlazeRoomsManager 0x00AB10F0/0x00AB0560).
B10C_USER = (0x7802, 1)  # the user ObjectType (.data 0x0146221C)
B10C_PLAYGROUP = (6, 1)
B10C_TEXT = "gl hf"
B10C_PVP_INVITE, B10C_PVP_ANSWER, B10C_CHAT, B10C_WHISPER = 0x176A0D2B, 0xA501C11E, 2, 3
B10C_INVITE_ATTRS = [(0xB0000, B5_NAME), (0xB0003, "1"), (0xB0005, "1"), (0xB0006, "0")]
B10C_ANSWER_ATTRS = [(0xB0002, "T"), (0xB0005, "1")]
B10C_CHAT_ATTRS = [(0xFF02, B10C_TEXT), (0xB0003, "2"), (0xB0009, B10C_TEXT)]  # 0xB0003: the channel kind (2 party)
B10C_WHISPER_ATTRS = [(0xFF02, B10C_TEXT), (0xB0003, "0"), (0xB0004, "false"), (0xB0009, B10C_TEXT)]


def objtype(w, label, component, typ):
    w.out += hdr(label, tdf.OBJTYPE) + vi(component) + vi(typ)
    return w


def client_message(w, attrs, flags, targets, ttyp, mtype, tag=0):
    """Blaze::Messaging::ClientMessage 0x012EE228: ATTR map<int,string>, FLAG, STAT, TAG, TIDS list<int>, TTYP objtype,
    TYPE; every member written, as the client's encoder does."""
    w.map("ATTR", INT, STRING, attrs).int("FLAG", flags).int("STAT", 0).int("TAG", tag).list("TIDS", INT, targets)
    return objtype(w, "TTYP", *ttyp).int("TYPE", mtype)


def notify_message(mgid, name, source, attrs, flags, targets, ttyp, mtype):
    """Messaging::NotifyMessage, Blaze::Messaging::ServerMessage 0x012EE2D0: FLAG 0, MGID, NAME = the sender's persona
    name, PYLD = the ClientMessage as sent, SRCE = (0x7802, 1, sender), TIME."""
    w = tdf.Writer().int("FLAG", 0).int("MGID", mgid).string("NAME", name)
    with w.struct("PYLD"):
        client_message(w, attrs, flags, targets, ttyp, mtype)
    objid(w, "SRCE", B10C_USER[0], B10C_USER[1], source)
    return bytes(w.int("TIME", B5_TIME))


B10C_CASES = {
    # Blaze::Messaging::SendMessageResponse 0x012EE3E8: MGID, MIDS (the ids kept for fetchMessages: none / the whisper)
    "sendmessage_reply": lambda: bytes(tdf.Writer().int("MGID", 1).list("MIDS", INT, [])),
    "sendmessage_stored_reply": lambda: bytes(tdf.Writer().int("MGID", 4).list("MIDS", INT, [4])),
    "notifymessage_pvpinvite_notification":
        lambda: notify_message(1, B5_NAME, B5_PID, B10C_INVITE_ATTRS, 0, [B11_SECOND_PID], B10C_USER, B10C_PVP_INVITE),
    "notifymessage_inviteanswer_notification":
        lambda: notify_message(2, B11_SECOND_NAME, B11_SECOND_PID, B10C_ANSWER_ATTRS, 0, [B5_PID], B10C_USER,
                               B10C_PVP_ANSWER),
    "notifymessage_chat_notification":
        lambda: notify_message(3, B5_NAME, B5_PID, B10C_CHAT_ATTRS, 6, [1], B10C_PLAYGROUP, B10C_CHAT),
    "notifymessage_whisper_notification":
        lambda: notify_message(4, B5_NAME, B5_PID, B10C_WHISPER_ATTRS, 7, [B11_SECOND_PID], B10C_USER, B10C_WHISPER),
    # Blaze::Util::FilterUserTextResponse 0x012FCD98: TLST of FilteredUserText 0x014632F8 {DIRT FILTER_RESULT_PASSED, UTXT}
    "filterforprofanity_reply":
        lambda: bytes(tdf.Writer().list("TLST", STRUCT, [tdf.Writer().int("DIRT", 0).string("UTXT", B10C_TEXT)])),
}


def a5b_user_data(uid, pid, name, online):
    """UserSessions::lookupUser's reply (A5b), Blaze::UserData 0x012F20C0: EDAT = UserSessionExtendedData 0x012F1F70
    as extended_data_update's DATA, FLGS = 2 (online [assumed: BlazeSDK's UserDataFlags subscribed 1, online 2]) or 0,
    USER = UserIdentification 0x012F1C70 {AID the account, ALOC, ID the persona, NAME}; EXBB, EXID, ORIG defaults."""
    w = tdf.Writer()
    with w.struct("EDAT"):
        w.string("BPS", "local").string("CTY", "")
        with w.struct("QDAT"):
            w.int("DBPS", 0).int("NATT", 0).int("UBPS", 0)
        w.int("UATT", 0)
    w.int("FLGS", 2 if online else 0)
    with w.struct("USER"):
        w.int("AID", uid).int("ALOC", B5_LOCALE).int("ID", pid).string("NAME", name)
    return bytes(w)


A5B_CASES = {
    "lookupuser_reply": lambda: a5b_user_data(B11_SECOND_UID, B11_SECOND_PID, B11_SECOND_NAME, True),
    "lookupuser_offline_reply": lambda: a5b_user_data(B11_SECOND_UID, B11_SECOND_PID, B11_SECOND_NAME, False),
}


def lookup_user_request():
    """UserSessions::lookupUser as MatchInviteUI's manual invite sends it (A5b; 0x004E8A20 ->
    UserManager_LookupUserByName 0x00C8CDB0): a UserIdentification (0x012F1C70) with only NAME set, the typed persona
    name. Rebuilt from the static reading (every member written, as join_party_request's USER) until captured."""
    w = tdf.Writer()
    w.int("AID", 0).int("ALOC", 0).blob("EXBB", b"").int("EXID", 0).int("ID", 0).string("NAME", B11_SECOND_NAME)
    return bytes(w.int("ORIG", 0))


# B13: AssociationLists. The lists the client asks for after enterService (BlazeFriends_RequestLists 0x00ABEFD0;
# a5b_run6d): (type, FLGS, the name dgserver gives it). 2 recent players, 4 block list, 1 friends.
B13_LISTS = [(2, 1, "recentPlayerList"), (4, 0, "blockList"), (1, 0x15, "friendList")]


def list_info(w, typ, flags, name, boid):
    """Blaze::Association::ListInfo 0x012F45A4: BOID objid, FLGS, LID = ListIdentification 0x012F4588 {LNM, TYPE},
    LMS, PRID."""
    objid(w, "BOID", *boid).int("FLGS", flags)
    with w.struct("LID"):
        w.string("LNM", name).int("TYPE", typ)
    return w.int("LMS", 0).int("PRID", 0)


def get_lists_request():
    """AssociationLists::getLists as the client sent it in a5b_run6d (Blaze::Association::GetListsRequest 0x012F4614):
    ALST = three ListInfo with an empty name and object id, MXRC 0xFFFFFFFF, OFRC 0; every member written."""
    w = tdf.Writer().list("ALST", STRUCT, [list_info(tdf.Writer(), t, f, "", (0, 0, 0)) for t, f, _ in B13_LISTS])
    return bytes(w.int("MXRC", 0xFFFFFFFF).int("OFRC", 0))


def b13_get_lists_reply():
    """getLists' reply, Blaze::Association::Lists 0x012F4608: LMAP = one ListMembers 0x012F45D8 per requested list in
    the request's order, INFO = the requested ListInfo with dgserver's name and the object id (0x19, 1, type), MEML
    empty, OFRC 0, TOCT 0."""
    items = []
    for t, f, name in B13_LISTS:
        w = tdf.Writer()
        with w.struct("INFO"):
            list_info(w, t, f, name, (0x19, 1, t))
        items.append(w.list("MEML", STRUCT, []).int("OFRC", 0).int("TOCT", 0))
    return bytes(tdf.Writer().list("LMAP", STRUCT, items))


B13_CASES = {
    "getlists_reply": b13_get_lists_reply,
}

# B13b: the association lists kept per account, with the users and presence the client needs, written from the member
# tables (server/internal/blaze/associationlists.go, presence.go). The fixture: player (B5) has the friend second, the
# recent player second and the blocked user third, each added at B5_TIME; second's presence 0x10001 (pre-game).
B13B_THIRD_UID, B13B_THIRD_PID, B13B_THIRD_NAME = 1000000003, 2000000003, "third"
B13B_PRESENCE = 0x10001  # game presence 1 << 16 | user presence 1 (updateExtendedDataAttribute ATID 0x5550)
B13B_MEMBERS = {2: [(B11_SECOND_PID, B11_SECOND_NAME)], 4: [(B13B_THIRD_PID, B13B_THIRD_NAME)],
                1: [(B11_SECOND_PID, B11_SECOND_NAME)]}
B13B_FRIEND_ATTRS = [(0xFF02, ""), (0xB0003, "6"), (0xB0009, "")]  # the chat UI's empty text, "%d" of 6
B13B_FRIEND_REQUEST, B13B_FRIEND_ACCEPTED = 8, 9


def list_member_id(w, blid, name):
    """Blaze::Association::ListMemberId 0x014627B8 as dgserver writes a member: BLID, EXBB empty, PLAT 4 (PC), PNAM,
    XREF 0."""
    return w.int("BLID", blid).blob("EXBB", b"").int("PLAT", B5_PC).string("PNAM", name).int("XREF", 0)


def list_member_info(w, blid, name, time):
    """Blaze::Association::ListMemberInfo 0x012F4568: LMID ListMemberId, TIME (Unix seconds)."""
    with w.struct("LMID"):
        list_member_id(w, blid, name)
    return w.int("TIME", time)


def b13b_get_lists_reply():
    """getLists' reply for the fixture: as b13_get_lists_reply with MEML the members and TOCT their count."""
    items = []
    for t, f, name in B13_LISTS:
        w = tdf.Writer()
        with w.struct("INFO"):
            list_info(w, t, f, name, (0x19, 1, t))
        members = [list_member_info(tdf.Writer(), pid, n, B5_TIME) for pid, n in B13B_MEMBERS[t]]
        items.append(w.list("MEML", STRUCT, members).int("OFRC", 0).int("TOCT", len(members)))
    return bytes(tdf.Writer().list("LMAP", STRUCT, items))


def b13b_extended_data(w, presence):
    """Blaze::UserSessionExtendedData 0x012F1F70 of another user: as extended_data_update's DATA with CMAP {0x5550: the
    presence} (the client attribute updateExtendedDataAttribute sets; the presence manager reads it, 0x00AA9A90)."""
    w.string("BPS", "local").map("CMAP", INT, INT, [(0x5550, presence)]).string("CTY", "")
    with w.struct("QDAT"):
        w.int("DBPS", 0).int("NATT", 0).int("UBPS", 0)
    return w.int("UATT", 0)


def b13b_user_added(uid, pid, name, presence):
    """UserSessions::UserAdded, Blaze::NotifyUserAdded 0x012F2020: DATA the user's extended data, USER UserIdentification
    {AID, ALOC, ID, NAME}."""
    w = tdf.Writer()
    with w.struct("DATA"):
        b13b_extended_data(w, presence)
    with w.struct("USER"):
        w.int("AID", uid).int("ALOC", B5_LOCALE).int("ID", pid).string("NAME", name)
    return bytes(w)


def b13b_presence(pid, presence):
    """Another user's UserSessionExtendedDataUpdate 0x012F1FF8 with its presence: DATA, SUBS 1, USID."""
    w = tdf.Writer()
    with w.struct("DATA"):
        b13b_extended_data(w, presence)
    return bytes(w.int("SUBS", 1).int("USID", pid))


def b13b_update_reply(added=(), removed=()):
    """addUsersToList's and removeUsersFromList's reply, UpdateListMembersResponse 0x012F46B0: LMID the added
    ListMemberInfos, REM the removed ListMemberIds."""
    w = tdf.Writer().list("LMID", STRUCT, [list_member_info(tdf.Writer(), pid, n, t) for pid, n, t in added])
    return bytes(w.list("REM", STRUCT, [list_member_id(tdf.Writer(), pid, n) for pid, n in removed]))


def b13b_membership(pid, name, time, lupt):
    """NotifyUpdateListMembership, UpdateListWithMembersRequest 0x012F4770: BIDL one ListMemberInfoUpdate 0x01462798
    {LMID ListMemberInfo, LUPT 1 add / 2 remove}, LID {friendList, 1}."""
    u = tdf.Writer()
    with u.struct("LMID"):
        list_member_info(u, pid, name, time)
    w = tdf.Writer().list("BIDL", STRUCT, [u.int("LUPT", lupt)])
    with w.struct("LID"):
        w.string("LNM", "friendList").int("TYPE", 1)
    return bytes(w)


B13B_CASES = {
    "getlists_members_reply": b13b_get_lists_reply,
    "useradded_notification": lambda: b13b_user_added(B11_SECOND_UID, B11_SECOND_PID, B11_SECOND_NAME, 0),
    # Blaze::UserStatus 0x012F1CD8: FLGS 3 known and online / 1 known and offline (0x00C8A130), ID
    "userupdated_online_notification": lambda: bytes(tdf.Writer().int("FLGS", 3).int("ID", B11_SECOND_PID)),
    "userupdated_offline_notification": lambda: bytes(tdf.Writer().int("FLGS", 1).int("ID", B11_SECOND_PID)),
    "presence_notification": lambda: b13b_presence(B11_SECOND_PID, B13B_PRESENCE),
    "adduserstolist_friend_reply": lambda: b13b_update_reply(added=[(B11_SECOND_PID, B11_SECOND_NAME, B5_TIME)]),
    "removeusersfromlist_friend_reply": lambda: b13b_update_reply(removed=[(B11_SECOND_PID, B11_SECOND_NAME)]),
    "notifyupdatelistmembership_add_notification": lambda: b13b_membership(B5_PID, B5_NAME, B5_TIME, 1),
    "notifyupdatelistmembership_remove_notification": lambda: b13b_membership(B5_PID, B5_NAME, 0, 2),
}

# B14: the server's system messages and other users' wins (server/internal/blaze/sysmsg.go, presence.go), written from
# the member tables. A system message is a Messaging NotifyMessage whose PYLD has TYPE 300 (cLobbyMessaging's global
# callback 0x00ACC9F0): no target (TTYP (0, 0), TIDS empty: only the global callbacks see it), SRCE (0, 0, 0), NAME
# empty; TAG 0 the kill switches (ATTR {id: "1"} = switched off: 2 WalletTopUp, 8 CustomGames), TAG 1 hide every
# broadcast, TAG 2 a broadcast (0xB0000 id, 0xB0001 display flags, 4 = the main menu's marquee, 0xB0002 seconds,
# 0xB0003 text). MGID 1-3 in the order a login gets them. The party member's data: the first persona with 3 wins as
# its party's guest learns it (UserAdded) or, already known, gets it again (UserSessionExtendedDataUpdate).
B14_SYSTEM = 300
B14_BROADCAST_ATTRS = [(0xB0000, "1"), (0xB0001, "4"), (0xB0002, "604800"), (0xB0003, "Server restart at 20:00 UTC")]


def b14_system_message(mgid, tag, attrs):
    w = tdf.Writer().int("FLAG", 0).int("MGID", mgid).string("NAME", "")
    with w.struct("PYLD"):
        client_message(w, attrs, 0, [], (0, 0), B14_SYSTEM, tag=tag)
    objid(w, "SRCE", 0, 0, 0)
    return bytes(w.int("TIME", B5_TIME))


def b14_extended_data(w, presence, wins):
    """UserSessionExtendedData 0x012F1F70 of another user with its presence (CMAP) and wins (DMAP {0x00070034: wins},
    R11's key the main menu and the party lobby read)."""
    w.string("BPS", "local").map("CMAP", INT, INT, [(0x5550, presence)]).string("CTY", "")
    w.map("DMAP", INT, INT, [(0x00070034, wins)])
    with w.struct("QDAT"):
        w.int("DBPS", 0).int("NATT", 0).int("UBPS", 0)
    return w.int("UATT", 0)


def b14_user_added(uid, pid, name, presence, wins):
    w = tdf.Writer()
    with w.struct("DATA"):
        b14_extended_data(w, presence, wins)
    with w.struct("USER"):
        w.int("AID", uid).int("ALOC", B5_LOCALE).int("ID", pid).string("NAME", name)
    return bytes(w)


def b14_member_data(pid, presence, wins):
    w = tdf.Writer()
    with w.struct("DATA"):
        b14_extended_data(w, presence, wins)
    return bytes(w.int("SUBS", 1).int("USID", pid))


B14_CASES = {
    "notifymessage_killswitches_notification": lambda: b14_system_message(1, 0, [(2, "1")]),
    "notifymessage_killswitches_two_notification": lambda: b14_system_message(1, 0, [(2, "1"), (8, "1")]),
    "notifymessage_hidebroadcasts_notification": lambda: b14_system_message(2, 1, []),
    "notifymessage_broadcast_notification": lambda: b14_system_message(3, 2, B14_BROADCAST_ATTRS),
    "useradded_wins_notification": lambda: b14_user_added(B5_UID, B5_PID, B5_NAME, 0, 3),
    "memberdata_notification": lambda: b14_member_data(B5_PID, 0, 3),
}

# G3: rejoining a running hosted game (server/internal/blaze/reconnect.go), written from the member tables: a hosted
# game's NotifyGameSetup with its PGID (the persisted id the client saves with setGameID), getGameID's reply with a
# saved id, getGameDataFromId's reply for running 1 v 1 game 1 (GameBrowserDataList 0x012E74B4 with one
# GameBrowserGameData 0x01461800: IN_GAME, both players in PCNT, the PlayerIdList_TeamCount/PlayerIdList_Team_<i>
# attributes the client finds its team in: base64 of the team's u64 ids, little-endian), the rejoin's NotifyGameSetup (IN_GAME, PROS
# both players, DatalessSetupContext JOIN_GAME_SETUP_CONTEXT 1) and NotifyPlayerJoining (0x012E7B98: GID, PDAT) for the
# player still in it. Read statically (HandleGetGameIDResponse 0x00AC4040, DiscoCheck_GetGameInfoCB 0x00AC2930,
# GetGameInfoForJoinCB 0x00AC5880); live: G3.
G3_PGID = "dawngate-game-1"


def g3_team_ids(*ids):
    return base64.b64encode(b"".join(struct.pack("<Q", i) for i in ids)).decode("ascii")


def g3_browser_data():
    w = tdf.Writer()
    g = tdf.Writer()
    g.list("ADMN", INT, [B5_PID])
    attrs = B10A_ATTRS + [("PlayerIdList_Team_0", g3_team_ids(B5_PID)), ("PlayerIdList_Team_1", g3_team_ids(B11_SECOND_PID)),
                          ("PlayerIdList_TeamCount", "2"), ("ServerBuildVersion", B10A_VERSION)]
    # the client searches the map with _stricmp over the wire order (0x0066D5B0): keys sorted case-insensitively
    g.map("ATTR", STRING, STRING, sorted(attrs, key=lambda kv: kv[0].lower()))
    g.list("CAP", INT, [2, 0])
    g.int("GID", 1).string("GNAM", "player's Game").int("GSET", 0x506).int("GSTA", 0x83)  # IN_GAME
    g.list("HNET", STRUCT, [tdf.UnionItem(2, ip_pair(tdf.Writer(), B8_GMS, B8_GMS))])
    g.int("NTOP", 1).list("PCNT", INT, [2, 0]).int("PRES", 1).string("PSAS", "local").int("TCAP", 1)
    g.string("VSTR", B10A_VERSION)
    w.list("GDAT", STRUCT, [g])
    return bytes(w)


def g3_rejoin_setup():
    w = tdf.Writer()
    with w.struct("GAME"):
        b10a_game(w, B5_PID, state=0x83, pgid=G3_PGID, cap=2, tcap=1)
    w.list("PROS", STRUCT, [b10a_player(B5_PID, B5_NAME, 0, 0), b10a_player(B11_SECOND_PID, B11_SECOND_NAME, 1, 1)])
    with w.union("REAS", 0):  # DatalessSetupContext
        with w.struct("VALU"):
            w.int("DCTX", 1)  # JOIN_GAME_SETUP_CONTEXT
    return bytes(w)


def g3_player_joining():
    w = tdf.Writer().int("GID", 1)
    with w.struct("PDAT"):
        w.out += b10a_player(B11_SECOND_PID, B11_SECOND_NAME, 1, 1).out
    return bytes(w)


G3_CASES = {
    "notifygamesetup_hosted_notification": lambda: notify_game_setup(pgid=G3_PGID),
    "notifygamesetup_hosted_notification_lan": lambda: notify_game_setup(A5_GMS, B8_CLIENT, B8_CLIENT_LAN, pgid=G3_PGID),
    "getgameid_saved_reply": lambda: bytes(tdf.Writer().string("GMID", G3_PGID)),
    "getgamedatafromid_reply": g3_browser_data,
    "notifygamesetup_rejoin_notification": g3_rejoin_setup,
    "notifyplayerjoining_notification": g3_player_joining,
}


# B15: custom games and the game browser (server/internal/blaze/customgames.go, gamebrowser.go), written from the member
# tables: hosted custom game 1 "Custom 1" of the first persona as CREATE GAME makes it (GameFlags 2, LevelId 2490188286 =
# 0x946D41FE, three teams TIDS [1, 2, 3] of TCAP 5, PCAP [15, 0], GSET 0x505; cBlazeGameManager::CreateGame 0x00AC6850,
# kCreateGameFlags_Custom); the creator's NotifyGameSetup (CREATE_GAME context), the second persona's after its join
# (JOIN_GAME context, both players, the creator's join completed), NotifyPlayerJoining, NotifyPlayerJoinCompleted, the
# roster manager's NotifyGameAttribChange (TeamRostersKey: the packed rosters, GameRosterManager 0x006A1460, base64),
# NotifyPlayerAttribChange (a private game's PrivateAuthToken), NotifyGamePlayerTeamChange (to the spectators), and the
# game browser's GetGameListResponse and NotifyGameListUpdate (the game in its lobby, then its removal). Requests:
# getGameListSubscription as g3b_run10b logged it (289 bytes), and resetDedicatedServer for a custom game, joinGame from
# the browser, setGameAttributes, swapPlayersTeam, advanceGameState and the lobby's Join Team message to the admin,
# rebuilt from the static reading (docs/BLAZE.md *Custom games and spectators (B15)*; live: B15-live).
B15_LEVEL = "2490188286"  # 0x946D41FE: SM_Map_v5, MatchOptionsUI's only map (.data 0x013E2B7C)
B15_ATTRS = [("ExpectedPlayerCount", "10"), ("GameFlags", "2"), ("GameOwnerId", "77359401"),
             ("GameOwnerName", "player"), ("GameType", "1"), ("LevelId", B15_LEVEL)]
B15_JOIN_TEAM = 0xC4063947  # the roster manager's Join Team request to the admin (0x006A2D30)
B15_AUTH_TOKEN = "1A2B3C4D"


def b15_rosters(author, seq, teams):
    """TeamRostersKey as cGameRosterManager packs it (0x006A1460, 0x006A0F20, 0x006A0C40): u64 the packing player, u16
    the sequence, u8 the team count, per team u8 capacity, u8 a bit per used slot, the used slots' u64 ids; base64."""
    raw = struct.pack("<QHB", author, seq, len(teams))
    for slots in teams:
        mask = 0
        for i, pid in enumerate(slots):
            if pid:
                mask |= 1 << i
        raw += struct.pack("<BB", len(slots), mask)
        for pid in slots:
            if pid:
                raw += struct.pack("<Q", pid)
    return base64.b64encode(raw).decode("ascii")


B15_ROSTERS = b15_rosters(B5_PID, 2, [[B5_PID, 0, 0, 0, 0], [B11_SECOND_PID, 0, 0, 0, 0], [0, 0, 0, 0, 0]])
B15_ROSTER_ATTRS = [("RosterLockedKey", "0"), ("SpectatorPlayerIdList", ""), ("TeamRostersKey", B15_ROSTERS)]


def b15_attrs(extra=()):
    # the client searches attribute maps with _stricmp over the wire order (G3): keys sorted case-insensitively
    return sorted(B15_ATTRS + [("ServerBuildVersion", B10A_VERSION)] + list(extra), key=lambda kv: kv[0].lower())


def b15_game(w):
    """Blaze::GameManager::ReplicatedGameData 0x014615A8 of custom game 1 in its lobby (PRE_GAME), hosted (PGID)."""
    w.list("ADMN", INT, [B5_PID])
    w.map("ATTR", STRING, STRING, b15_attrs())
    w.list("CAP", INT, [15, 0])
    w.int("GID", 1).string("GNAM", "Custom 1").int("GSET", 0x505).int("GSTA", 0x82)
    w.list("HNET", STRUCT, [tdf.UnionItem(2, ip_pair(tdf.Writer(), B8_GMS, B8_GMS))])
    w.int("MCAP", 15).int("NTOP", 1).string("PGID", G3_PGID)
    with w.struct("PHST"):
        host_info(w)
    w.int("PRES", 1).string("PSAS", "local").int("SEED", 7919).int("TCAP", 5)
    with w.struct("THST"):
        host_info(w)
    w.list("TIDS", INT, [1, 2, 3])
    return w.string("UUID", "00000000-0000-0000-0000-000000000001").string("VSTR", B10A_VERSION)


def b15_player(pid, name, slot, team, state, attrs=()):
    """Blaze::GameManager::ReplicatedGamePlayer 0x01461EA0 of a custom game (PATT when it has player attributes)."""
    p = tdf.Writer()
    p.int("GID", 1).int("LOC", B5_LOCALE).string("NAME", name)
    if attrs:
        p.map("PATT", STRING, STRING, list(attrs))
    p.int("PID", pid)
    with p.union("PNET", 2):
        with p.struct("VALU"):
            ip_pair(p, B8_CLIENT, B8_CLIENT)
    p.int("SID", slot).int("SLOT", 0).int("STAT", state).int("TIDX", team).int("TIME", B5_TIME * 1000000)
    return p.int("UID", pid)


def b15_setup(players, dctx):
    w = tdf.Writer()
    with w.struct("GAME"):
        b15_game(w)
    w.list("PROS", STRUCT, players)
    with w.union("REAS", 0):  # DatalessSetupContext
        with w.struct("VALU"):
            w.int("DCTX", dctx)
    return bytes(w)


def b15_browser_game(w):
    """Blaze::GameManager::GameBrowserGameData 0x01461800 of custom game 1 in the game browser: both players (the
    creator's join completed), region "na", the team sizes in TINF."""
    w.list("ADMN", INT, [B5_PID])
    w.map("ATTR", STRING, STRING, b15_attrs())
    w.list("CAP", INT, [15, 0])
    w.int("GID", 1).string("GNAM", "Custom 1").int("GSET", 0x505).int("GSTA", 0x82)
    w.list("HNET", STRUCT, [tdf.UnionItem(2, ip_pair(tdf.Writer(), B8_GMS, B8_GMS))])
    w.int("HOST", 3000000001).int("NTOP", 1).list("PCNT", INT, [2, 0]).int("PRES", 1).string("PSAS", "na")
    rost = [tdf.Writer().int("EXID", 0).int("LOC", B5_LOCALE).string("NAME", B5_NAME).int("PID", B5_PID).int("STAT", 4)
            .int("TIDX", 0),
            tdf.Writer().int("EXID", 0).int("LOC", B5_LOCALE).string("NAME", B11_SECOND_NAME).int("PID", B11_SECOND_PID)
            .int("STAT", 2).int("TIDX", 1)]
    w.list("ROST", STRUCT, rost).int("TCAP", 5)
    w.list("TINF", STRUCT, [tdf.Writer().int("TID", 1).int("TSZE", 1), tdf.Writer().int("TID", 2).int("TSZE", 1),
                            tdf.Writer().int("TID", 3).int("TSZE", 0)])
    return w.string("VSTR", B10A_VERSION)


def b15_list_update(with_game):
    w = tdf.Writer().int("DONE", 1).int("GLID", 1)
    if with_game:
        m = tdf.Writer().int("FIT", 100)
        with m.struct("GAM"):
            b15_browser_game(m)
        w.list("UPDT", STRUCT, [m])
    else:
        w.list("REMV", INT, [1])
    return bytes(w)


def b15_player_joining():  # NotifyPlayerJoining 0x012E7B98: GID, PDAT
    w = tdf.Writer().int("GID", 1)
    with w.struct("PDAT"):
        w.out += b15_player(B11_SECOND_PID, B11_SECOND_NAME, 1, 1, 2).out
    return bytes(w)


B15_CASES = {
    "notifygamesetup_custom_notification": lambda: b15_setup([b15_player(B5_PID, B5_NAME, 0, 0, 2)], 0),
    "notifygamesetup_customjoin_notification": lambda: b15_setup(
        [b15_player(B5_PID, B5_NAME, 0, 0, 4), b15_player(B11_SECOND_PID, B11_SECOND_NAME, 1, 1, 2)], 1),
    "notifyplayerjoining_custom_notification": lambda: b15_player_joining(),
    "notifyplayerjoincompleted_notification": lambda: bytes(tdf.Writer().int("GID", 1).int("PID", B11_SECOND_PID)),
    "notifygameattribchange_notification": lambda: bytes(
        tdf.Writer().map("ATTR", STRING, STRING, B15_ROSTER_ATTRS).int("GID", 1)),
    "notifyplayerattribchange_notification": lambda: bytes(
        tdf.Writer().map("ATTR", STRING, STRING, [("PrivateAuthToken", B15_AUTH_TOKEN)]).int("GID", 1)
        .int("PID", B11_SECOND_PID)),
    "notifygameplayerteamchange_notification": lambda: bytes(
        tdf.Writer().int("GID", 1).int("PID", B11_SECOND_PID).int("TIDX", 2)),
    "getgamelist_reply": lambda: bytes(tdf.Writer().int("GLID", 1).int("MAXF", 100).int("NGD", 1)),
    "notifygamelistupdate_notification": lambda: b15_list_update(True),
    "notifygamelistupdate_removed_notification": lambda: b15_list_update(False),
}


def get_game_list_request():
    """GameManager::getGameListSubscription as the client sent it in g3b_run10b (BROWSE GAMES; GetGameListRequest
    0x012E73C0 with GLID = MatchmakingCriteriaData 0x012E8110, every rule empty; 289 bytes), rebuilt from the log's
    decoded TDF (the client's encoder leaves out empty lists and maps, B10b)."""
    w = tdf.Writer().string("DNAM", "BGDefaultList")
    with w.struct("GLID"):
        for label in ("AGAM", "APLR", "CUST"):
            with w.struct(label):
                pass
        with w.struct("DNF"):
            w.int("DNF", 101)
        with w.struct("GEO"):
            w.string("THLD", "")
        with w.struct("GNAM"):
            w.string("SUBS", "")
        with w.struct("NAT"):
            w.string("THLD", "")
        with w.struct("PPLR"):
            objid(w, "PSET", 0, 0, 0)
            w.int("REQP", 0)
        with w.struct("PSR"):
            w.string("THLD", "")
        with w.struct("RANK"):
            w.string("THLD", "").int("VALU", 1)
        with w.struct("RSZR"):
            w.int("PCAP", 0xFFFF).int("PMIN", 0)
        with w.struct("SIZE"):
            w.int("ISSG", 0).int("PCAP", 1).int("PCNT", 1).int("PMIN", 1).string("THLD", "")
        with w.struct("TEAM"):
            w.int("PCAP", 0).int("PCNT", 0).int("PMIN", 0).int("SDIF", 0).string("THLD", "").int("TID", 0xFFFF)
        with w.struct("VIAB"):
            w.string("THLD", "")
        with w.struct("VIRT"):
            w.string("THLD", "").int("VALU", 1)
    return bytes(w.string("GVER", B10A_VERSION).int("IGNO", 1).int("LCAP", 1000).int("NOJM", 1))


def create_custom_game_request():
    """GameManager::resetDedicatedServer for CREATE GAME's custom game (CreateGameRequest 0x01461AF0 as
    cBlazeGameManager::CreateGame 0x00AC6850 fills it for kCreateGameFlags_Custom: GNAM the dialog's name, GSET 0x505,
    PCAP [15, 0], TCAP 5, TIDS [1, 2, 3], the attributes with GameFlags 2 and the map's LevelId); B8's request
    otherwise. Rebuilt from the static reading until captured (B15-live)."""
    w = tdf.Writer()
    w.map("ATTR", STRING, STRING, B15_ATTRS)
    w.out += hdr("BTPL", tdf.OBJID) + vi(0) + vi(0) + vi(0)
    w.string("GCTR", "").int("GENT", 0).string("GNAM", "Custom 1").int("GSET", 0x505)
    w.string("GTYP", "").string("GURL", "")
    w.list("HNET", STRUCT, [tdf.UnionItem(2, ip_pair(tdf.Writer(), B8_CLIENT, B8_CLIENT_LAN))])
    w.int("IGNO", 0).int("NRES", 0).int("NTOP", 1).list("PCAP", INT, [15, 0]).string("PGID", "")
    w.blob("PGSC", b"").int("PMAX", 0).int("PRES", 1).int("QCAP", 0).int("RGID", 0).int("SLOT", 0)
    w.int("TCAP", 5).list("TIDS", INT, [1, 2, 3]).int("TIDX", 0).int("VOIP", 0)
    w.string("VSTR", B10A_VERSION)
    return bytes(w)


def join_game_request(team, attrs=()):
    """GameManager::joinGame from the browser's JOIN GAME (team 0xFFFF: any; a try of team 0 or 1 when it retries) or
    SPECTATE GAME (team 2): cBlazeGameManager::JoinGame 0x00AC60E0 -> JoinGameRequest 0x01461470 with GID, JMET 1, the
    player attributes (a private game's PrivateAuthToken), the adapter's address as PNET and the team. Rebuilt from the
    static reading until captured (B15-live)."""
    w = tdf.Writer()
    if attrs:
        w.map("ATTR", STRING, STRING, list(attrs))
    w.out += hdr("BTPL", tdf.OBJID) + vi(0) + vi(0) + vi(0)
    w.int("GENT", 0).int("GID", 1).string("GVER", B10A_VERSION).int("JMET", 1)
    with w.union("PNET", 2):
        with w.struct("VALU"):
            ip_pair(w, B8_CLIENT, B8_CLIENT_LAN)
    w.int("SLEN", 0).int("SLID", 0xFF).int("SLOT", 0).int("STRT", 0).int("TIDX", team)
    with w.struct("USER"):
        w.int("AID", 0).int("ALOC", 0).blob("EXBB", b"").int("EXID", 0).int("ID", 0).string("NAME", "").int("ORIG", 0)
    return bytes(w.blob("XSES", b""))


def swap_players_team_request(moves):
    """GameManager::swapPlayersTeam from the admin's roster sync (0x006A3840 -> 0x006A36D0 -> 0x0069DAF0):
    SwapPlayersTeamRequest 0x012E7AC0 {GID, LGAM: SwapPlayerTeamData 0x012E7AA0 {PID, TIDX} per rostered player}."""
    w = tdf.Writer().int("GID", 1)
    return bytes(w.list("LGAM", STRUCT, [tdf.Writer().int("PID", pid).int("TIDX", team) for pid, team in moves]))


def add_users_to_list_request(members):
    """AssociationLists::addUsersToList for the recent players list, as every game's loading step sends it
    (0x00AC4850 -> 0x00ABD200 -> AssociationList_AddUsersToList 0x00C9AA30): UpdateListMembersRequest 0x012F4660 with
    BID 0, BIDL = one ListMemberId 0x014627B8 per other Blaze player of the game (only BLID set), LID = the list's
    ListIdentification from our getLists reply, MUTA 0, VALA 1, VALD 1; every member written. Rebuilt from the static
    reading until captured (G3)."""
    bidl = [tdf.Writer().int("BLID", m).blob("EXBB", b"").int("PLAT", 0).string("PNAM", "").int("XREF", 0)
            for m in members]
    w = tdf.Writer().int("BID", 0).list("BIDL", STRUCT, bidl)
    with w.struct("LID"):
        w.string("LNM", "recentPlayerList").int("TYPE", 2)
    return bytes(w.int("MUTA", 0).int("VALA", 1).int("VALD", 1))


def update_list_members_request(list_name, list_type, members, muta):
    """B13b: addUsersToList/removeUsersFromList as the chat's friend actions send them (UpdateListMembersRequest
    0x012F4660; 0x00ABD5F0 adds, 0x00ABD780 removes by BLID, 0x00ABD480/0x00ABD8F0 by PNAM alone; the friends list
    with MUTA 1 through 0x00C9AB20/0x00C9AD00, the others with 0; VALA and VALD 1; LID from our getLists reply).
    members: (BLID, PNAM) pairs. Rebuilt from the static reading until captured (B13b's live check)."""
    bidl = [tdf.Writer().int("BLID", blid).blob("EXBB", b"").int("PLAT", 0).string("PNAM", pnam).int("XREF", 0)
            for blid, pnam in members]
    w = tdf.Writer().int("BID", 0).list("BIDL", STRUCT, bidl)
    with w.struct("LID"):
        w.string("LNM", list_name).int("TYPE", list_type)
    return bytes(w.int("MUTA", muta).int("VALA", 1).int("VALD", 1))


def presence_request(value):
    """UserSessions::updateExtendedDataAttribute with the presence (UpdateUserSessionAttributeRequest 0x012F2088:
    ATID 21840 = 0x5550, OPER 0, UREA 0, VALU), as b5's sessions logged it (VALU 0x10001 on entering pre-game)."""
    return bytes(tdf.Writer().int("ATID", 0x5550).int("OPER", 0).int("UREA", 0).int("VALU", value))


def b10c_request(attrs, flags, targets, ttyp, mtype):
    return bytes(client_message(tdf.Writer(), attrs, flags, targets, ttyp, mtype))


def filter_for_profanity_request():
    """Util::filterForProfanity as MessagingAPI builds it for a message with flag 0x4 (0x00CAB1E0): one UserText per
    attribute with a key <= 0xFFFF (Blaze::Util::UserStringList 0x012FCA38 {UTXT list of UserText 0x012FCA28})."""
    return bytes(tdf.Writer().list("UTXT", STRUCT, [tdf.Writer().string("UTXT", B10C_TEXT)]))


def start_matchmaking_request(party=False):
    """GameManager::startMatchmaking as cBlazeGameManager::CreateGame (0x00AC6850, Matched) builds it
    (Blaze::GameManager::StartMatchmakingRequest 0x01461D78, CRIT = MatchmakingCriteriaData 0x012E8110). B10a read it
    statically; B10b checked it against the live client (b10b_run3c, 745 bytes; this case differs from that capture
    only in the persona (id 0x77359401 "player" instead of 0x77359404 "Player") and the dummy LAN address): the client's
    encoder leaves out every empty list and map (AGAM.GIDL, APLR.ALST/ASTS, CVAR, PPLR.PLST, SKLZ, ECRI, PLST, SIDL)
    but writes empty structs, empty strings and zeros; a fresh account (no DMAP game count, so 0) is gamesPlayed "New";
    UED skill_Rule's CVAL/OVAL are 0. Region NA (specificRegion). With party=True the leader of PvP party 1 (BTPL,
    premadeSize 2; not seen live yet). The PNET is B8's with the dummy LAN address."""
    w = tdf.Writer()
    w.map("ATTR", STRING, STRING, B10A_ATTRS)
    objid(w, "BTPL", 6, 1, 1) if party else objid(w, "BTPL", 0, 0, 0)
    with w.struct("CRIT"):
        with w.struct("AGAM"):
            pass
        with w.struct("APLR"):
            pass
        with w.struct("CUST"):
            pass
        with w.struct("DNF"):
            w.int("DNF", 101)
        with w.struct("GEO"):
            w.string("THLD", "")
        with w.struct("GNAM"):
            w.string("SUBS", "")
        with w.struct("NAT"):
            w.string("THLD", "")
        with w.struct("PPLR"):
            objid(w, "PSET", 0, 0, 0)
            w.int("REQP", 0)
        with w.struct("PSR"):
            w.string("THLD", "specificRegion")
        with w.struct("RANK"):
            w.string("THLD", "").int("VALU", 1)
        rules = [tdf.Writer().string("NAME", "gamesPlayedRule").string("THLD", "decayPlus0").list("VALU", STRING, ["New"]),
                 tdf.Writer().string("NAME", "premadeRule").string("THLD", "decayPlus0")
                 .list("VALU", STRING, ["2" if party else "1"])]
        w.list("RLST", STRUCT, rules)
        with w.struct("RSZR"):
            w.int("PCAP", 0xFFFF).int("PMIN", 0)
        with w.struct("SIZE"):
            w.int("ISSG", 0).int("PCAP", 1).int("PCNT", 1).int("PMIN", 1).string("THLD", "")
        with w.struct("TEAM"):
            w.int("PCAP", 5).int("PCNT", 5).int("PMIN", 1).int("SDIF", 0).string("THLD", "requireExactMatch")
            w.int("TID", 0xFFFE).list("TLST", INT, [0xFFFE, 0xFFFE])
        skill = tdf.Writer().int("CVAL", 0).string("NAME", "skill_Rule").int("OVAL", 0)
        w.map("UED", STRING, STRUCT, [("skill_Rule", skill.string("THLD", "decayPlus0"))])
        with w.struct("VIAB"):
            w.string("THLD", "")
        with w.struct("VIRT"):
            w.string("THLD", "").int("VALU", 1)
    w.int("DUR", 10800000).int("GENT", 0).string("GNAM", "player's Game")
    w.int("GSET", 0x506).string("GVER", B10A_VERSION).int("IGNO", 0).int("MODE", 2).int("NTOP", 1)
    w.map("PATT", STRING, STRING, [("gamesPlayed", "New"), ("premadeSize", "2" if party else "1")])
    w.int("PMAX", 0)
    with w.union("PNET", 2):
        with w.struct("VALU"):
            ip_pair(w, B8_CLIENT, B8_CLIENT_LAN)
    w.int("PRES", 1).int("QCAP", 0).int("VOIP", 0)
    return bytes(w)


def update_network_info_request():
    """UserSessions::updateNetworkInfo after picking NA on the play screen (Blaze::NetworkInfo 0x012F1C08: ADDR,
    NLMP = GameServerRegionList's InitialPing defaults as b5 logged them, NQOS as B2's QoS reports)."""
    w = tdf.Writer()
    with w.union("ADDR", 2):
        with w.struct("VALU"):
            ip_pair(w, B8_CLIENT, B8_CLIENT_LAN)
    w.map("NLMP", STRING, INT, B10A_PING_SITES)
    with w.struct("NQOS"):
        w.int("DBPS", 96000000).int("NATT", 0).int("UBPS", 96000000)
    return bytes(w)


def create_party_request():
    """Playgroups::createPlaygroup from PVPPlaygroup_Create (0x0068CD40): JOIN 0, a new playgroup named by a uuid,
    MLIM 5, no key, ENBV 0 (CreatePlaygroupRequest 0x012ED1B0)."""
    w = tdf.Writer().int("JOIN", 0)
    with w.struct("PGRP"):
        w.map("ATTR", STRING, STRING, []).int("ENBV", 0)
        with w.union("HNET", tdf.UNION_UNSET):
            pass
        w.int("HSID", 0).int("JOIN", 0).int("MLIM", 5).string("NAME", B10A_PARTY_NAME).int("NTOP", 0).int("OWNR", 0)
        w.int("PGID", 0).int("PRES", 0).string("UKEY", "").int("UPRS", 0).string("UUID", "").int("VOIP", 0)
        w.blob("XNNC", b"").blob("XSES", b"")
    return bytes(w)


def join_party_request():
    """Playgroups::joinPlaygroup from PVPPlaygroup_Join (0x0068D260): PGID from the invite, PNET the adapter's
    address, UKEY empty, USER at its defaults (JoinPlaygroupRequest 0x012ED238, UserIdentification 0x012F1C70)."""
    w = tdf.Writer().int("PGID", 1)
    with w.union("PNET", 2):
        with w.struct("VALU"):
            ip_pair(w, B8_CLIENT, B8_CLIENT_LAN)
    w.string("UKEY", "")
    with w.struct("USER"):
        w.int("AID", 0).int("ALOC", 0).blob("EXBB", b"").int("EXID", 0).int("ID", 0).string("NAME", "").int("ORIG", 0)
    return bytes(w)


def create_playgroup_request():
    """Playgroups::createPlaygroup as the client sent it in d2f_run2 (Blaze::Playgroups::CreatePlaygroupRequest
    0x012ED1B0, 128 bytes): JOIN 1, PGRP = PlaygroupInfo with the game's key and every other member at its
    default, HNET unset."""
    w = tdf.Writer().int("JOIN", 1)
    with w.struct("PGRP"):
        w.map("ATTR", STRING, STRING, [("PlaygroupKey", B11_KEY)]).int("ENBV", 0)
        with w.union("HNET", tdf.UNION_UNSET):
            pass
        w.int("HSID", 0).int("JOIN", 0).int("MLIM", 15).string("NAME", B11_KEY).int("NTOP", 0).int("OWNR", 0)
        w.int("PGID", 0).int("PRES", 0).string("UKEY", B11_KEY).int("UPRS", 0).string("UUID", "").int("VOIP", 0)
        w.blob("XNNC", b"").blob("XSES", b"")
    return bytes(w)


def get_perk_page_data_request():
    """BattlegroundsAccounts::getPerkPageData as BGAComponent_SendGetPerkPageData (0x00ACE8F0) sends it after a
    granted loadout page: SMRY 1, USER the local persona id (BGAGetPerkPageDataRequest 0x012E3368; static, R11)."""
    return bytes(tdf.Writer().int("SMRY", 1).int("USER", B5_PID))


def update_perk_page_request():
    """BattlegroundsAccounts::updatePerkPage: PAGE = the whole page as PerkPage_WriteTdf (0x00ACEE20) writes it
    (BGAUpdatePerkPageRequest 0x012E3388; static, R11): page 1, version 2, Spark at cell 5 turned once, RedGem in
    socket 0."""
    w = tdf.Writer()
    with w.struct("PAGE"):
        b7c_perk_page(w, 1, "Page 1", 2, B7C_PAGE1_EDITED)
    return bytes(w)


def purchase_request(proi, item, ppri):
    """BGEcommerce::purchaseWithEC/PC as EcommerceManager_PurchaseOfferWith*Currency (0x00ABA200/0x00AB9F80) send it
    (PurchaseWithECRequest 0x012F3360 = PurchaseWithPCRequest 0x012F3310; static, R11): CNT 1, ITMS/QNTS = the
    offer's ITMI/ITMC, PPRI = the offer's paid price (also for EC), PROI = its PUII, VERS = the catalog's."""
    return bytes(tdf.Writer().int("CNT", 1).list("ITMS", INT, [item]).int("PPRI", ppri).string("PROI", proi)
                 .list("QNTS", INT, [1]).string("VERS", "1"))


def get_stats_by_group_request(group, vid, ptyp, pctr):
    """Stats::getStatsByGroupAsync as the client sent it in session g2_run2, with the fixture's persona as EID
    (GetStatsByGroupRequest 0x012F0220, from StatsManager_RequestPlayerStats 0x00AA6C60): AGGR 0, EID [the profile's
    Blaze id], NAME, PCTR, POFF 0, PRID 0, PTYP, TIME 0, VID. The two history groups ask for 7 days (PTYP 3 =
    STAT_PERIOD_DAILY, PCTR 7), PlayerStats and ShaperStats for all time (PTYP 0, PCTR 1); B7d."""
    return bytes(tdf.Writer().int("AGGR", 0).list("EID", INT, [B5_PID]).string("NAME", group).int("PCTR", pctr)
                 .int("POFF", 0).int("PRID", 0).int("PTYP", ptyp).int("TIME", 0).int("VID", vid))


def save_user_settings_request():  # Blaze::BattlegroundsAccounts::SaveUserSettingsRequest 0x012E3414
    return bytes(tdf.Writer().map("USET", STRING, STRING, B7A_SETTINGS))


def set_user_info_attribute_request():
    """UserSessions::setUserInfoAttribute as the client sent it in b8_run3 (Blaze::SetUserInfoAttributeRequest
    0x012F20E0: ATTV 1<<62, MASK 0, ULST [(0x7802, 1, BUID)])."""
    w = tdf.Writer().int("ATTV", B7A_USER_INFO).int("MASK", 0)
    w.out += hdr("ULST", tdf.LIST) + bytes([tdf.OBJID]) + vi(1) + vi(0x7802) + vi(1) + vi(B5_PID)
    return bytes(w)


TELEMETRY_KEYS = """ABILITY_RANKED CAMERA_MODES CHAT CLIENT_FPS COLLECTION_SEARCHRESULTS CONNECT_TO_GMS_FAILED
    EXTERNAL_BROWSER_REQUEST GAMEEXIT GAME_START GAME_TIP_CLOSED JOINED_MATCH LEVEL_GAINED LEVEL_MARKERSET_INFO
    LOADOUT_DETAILS LOADOUT_SUMMARY LOGIN_ERROR LOGIN_SUCCESS LOST_SERVER_BLAZE LOST_SERVER_GMS
    MATCHMAKER_ACCEPT_DIALOG MATCHMAKER_END OBJECT_POOL_ERROR PACE_AGGR PACE_AGGR_TEAM PING_STATS
    PLAYERLEFT_ABNORMAL PLAYERLEFT_NORMAL PLAYERREPORT PURCHASING QOS_DATA RAKNET_BITS SHOP_BUY SHOP_SELL
    SPECTATOR_SESSION STARTUP_OPTIONS SYSINFO UI USER_CHEATED VANITY VIEWED_PROFILE WARD_PLACED
    WAYPOINTS_PURCHASE WEBWIDGET""".split()
TELEMETRY_RULES = ("<!-- Dawngate restoration stub: telemetry rules -->\n<TelemetryRuleConfiguration>\n"
                   "    <GlobalThrottle>100.0</GlobalThrottle>\n    <PaceTimeSecs>10</PaceTimeSecs>\n" +
                   "".join(f"    <Rule>\n        <Key>LABS_{k}</Key>\n        <PercentSample>100.0</PercentSample>\n"
                           f"        <MaxReportsPerFrame>10000</MaxReportsPerFrame>\n    </Rule>\n"
                           for k in TELEMETRY_KEYS) +
                   "</TelemetryRuleConfiguration>\n").encode()


# --------------------------------------------------------------------------- client requests (b1_run2)
def get_server_instance_request():
    w = tdf.Writer()
    w.string("BSDK", "3.19.11.0 (3.19.11.0)").string("BTIM", "Sep 15 2014 18:19:11").string("CLNT", "Dawngate")
    w.int("CLTP", 0).int("CPLT", 4).string("CSKU", "1234").string("CVER", "version3")
    w.string("DSDK", "8.18.11.0").string("ENV", "prod")
    with w.union("FPID", tdf.UNION_UNSET):
        pass
    w.int("LOC", 0x656E5553).string("NAME", "battlegrounds-1-pc").string("PLAT", "Windows")
    w.string("PROF", "standardSecure_v3")
    return bytes(w)


def preauth_request():
    w = tdf.Writer()
    with w.struct("CDAT"):
        w.int("IITO", 0).int("LANG", 0x656E5553).string("SVCN", "battlegrounds-1-pc").int("TYPE", 0)
    with w.struct("CINF"):
        w.string("BSDK", "3.19.11.0 (3.19.11.0)").string("BTIM", "Sep 15 2014 18:18:47")
        w.string("CLNT", "Dawngate").int("CPFT", 4).string("CSKU", "1234").string("CVER", "version3")
        w.string("DSDK", "8.18.11.0").string("ENV", "prod").string("HWID", "").int("LOC", 0x656E5553)
        w.string("MAC", "00:00:00:00:00:00")
    with w.struct("FCCR"):
        w.string("CFID", "BlazeSDK")
    return bytes(w)


def login_request():
    return bytes(tdf.Writer().string("MAIL", "player@example.com").string("PASS", "dawngate1")
                 .string("TOKN", "").int("TYPE", 0))


def login_persona_request():
    return bytes(tdf.Writer().string("PNAM", "player"))


def create_game_request():
    """GameManager::resetDedicatedServer as the client sent it in b8_run4 (Blaze::GameManager::
    CreateGameRequest 0x01461AF0), with a dummy LAN address (10.0.0.2) as INIP."""
    w = tdf.Writer()
    w.map("ATTR", STRING, STRING, B8_ATTRS)
    w.out += hdr("BTPL", tdf.OBJID) + vi(0) + vi(0) + vi(0)
    w.string("GCTR", "").int("GENT", 0).string("GNAM", "player's Game").int("GSET", 1284)
    w.string("GTYP", "").string("GURL", "")
    w.list("HNET", STRUCT, [tdf.UnionItem(2, ip_pair(tdf.Writer(), B8_CLIENT, B8_CLIENT_LAN))])
    w.int("IGNO", 0).int("NRES", 0).int("NTOP", 1).list("PCAP", INT, [10, 0]).string("PGID", "")
    w.blob("PGSC", b"").int("PMAX", 0).int("PRES", 1).int("QCAP", 0).int("RGID", 0).int("SLOT", 0)
    w.int("TCAP", 5).list("TIDS", INT, [1, 2]).int("TIDX", 0).int("VOIP", 0)
    w.string("VSTR", "1.0.903.854-RL-Prod-Release")
    return bytes(w)


# --------------------------------------------------------------------------- writer cases
# Each one has a twin in server/internal/tdf/golden_test.go (writerCases) that must produce the same bytes.
def case_ints():
    w = tdf.Writer()
    for label, v in [("A", 0), ("B", 1), ("C", 63), ("D", 64), ("E", -1), ("F", -63), ("G", -64),
                     ("H", 8191), ("I", 8192), ("J", 0x7FFFFFFF), ("K", -0x80000000), ("L", 0xFFFFFFFF),
                     ("M", 2 ** 63 - 1), ("N", -(2 ** 63)), ("O", 2 ** 64 - 1), ("STIM", 1791405059),
                     ("LOC", 0x656E5553)]:
        w.int(label, v)
    return w


def case_labels():
    w = tdf.Writer()
    for label in ["X", "AB", "ABC", "ABCD", "IP", "PS1", "A_Z9", "ab", "TOOLONG"]:
        w.int(label, 1)
    return w


def case_strings():
    w = tdf.Writer()
    for label, s in [("EMPT", ""), ("HELO", "hello"), ("UTF8", "\u00e4\u00f6\u00fc \u20ac \U0001F600"),
                     ("SQ", "it's"), ("BOTH", "it's \"x\""), ("DQ", "say \"hi\""), ("CTRL", "a\tb\nc\rd\x01\x7f"),
                     ("BSL", "C:\\path"), ("NBSP", "a\u00a0b\u200bc")]:
        w.string(label, s)
    return w


def case_blobs():
    return tdf.Writer().blob("EMPT", b"").blob("BIN", bytes([0, 1, 0x7F, 0x80, 0xFF]) + b"'\"\\\t\n\rAz")


def case_structs():
    w = tdf.Writer()
    with w.struct("OUTR"):
        w.int("A", 1)
        with w.struct("INNR"):
            w.string("S", "x")
            with w.struct("EMPT"):
                pass
        w.int("B", 2)
    with w.struct("EMPT"):
        pass
    return w


def case_lists():
    w = tdf.Writer()
    w.list("INTS", INT, [1, 4, 5, 30722, -7])
    w.list("STRS", STRING, ["a", "", "b c"])
    w.list("STCT", STRUCT, [tdf.Writer().int("ID", 1).string("NAME", "one"), tdf.Writer()])
    w.list("EMPT", INT, [])
    return w


def case_maps():
    w = tdf.Writer()
    w.map("SS", STRING, STRING, {"connIdleTimeout": "90s", "pingPeriod": "20s"})
    w.map("ST", STRING, STRUCT, {"local": ping_site("127.0.0.1", 17502)})
    w.map("II", INT, INT, [(1, 10), (-2, 20)])
    w.map("IS", INT, STRING, [(7, "seven")])
    w.map("EMPT", STRING, INT, {})
    return w


def case_unions():
    w = tdf.Writer()
    with w.union("ADDR", 0):
        with w.struct("VALU"):
            w.string("HOST", "127.0.0.1")
    with w.union("UNST", tdf.UNION_UNSET):
        pass
    with w.union("INT", 2):
        w.int("VALU", 5)
    return w


def case_list_unions():  # a list of unions (HNET): member byte, then the struct member without VALU
    w = tdf.Writer()
    w.list("HNET", STRUCT, [tdf.UnionItem(2, ip_pair(tdf.Writer(), B8_GMS, B8_CLIENT)),
                            tdf.UnionItem(3, tdf.Writer().int("IP", 1).int("PORT", 2)),
                            tdf.UnionItem(tdf.UNION_UNSET)])
    w.list("EMPT", STRUCT, [])
    return w


# LBFIX: the requests the public server left unanswered (each one stopped the client's keep-alive pings, and the
# client closed Blaze 90 s later). Stats::getLeaderboard -> Blaze::Stats::LeaderboardStatValues 0x012F050C (LDLS,
# rows Blaze::Stats::LeaderboardStatValuesRow 0x012F0590: ENAM, ENID, RANK, STAT = wins, games, wins, shaper);
# GpsContentController::filePetition -> FilePetitionResponse 0x012FC3B0 (GUID); Rooms::selectCategoryUpdates ->
# SelectCategoryUpdatesResponse 0x012FFAC0 (VWID).
LBFIX_SHAPER = 1001  # a made-up catalogID


def lbfix_leaderboard(rows, first_rank=1):  # rows (persona id, name, games, wins, shaper)
    items = []
    for i, (pid, name, games, wins, shaper) in enumerate(rows):
        items.append(tdf.Writer().string("ENAM", name).int("ENID", pid).int("RANK", first_rank + i)
                     .list("STAT", STRING, [str(wins), str(games), str(wins), str(shaper)]))
    return bytes(tdf.Writer().list("LDLS", STRUCT, items))


def lbfix_leaderboard_request(name, poff):  # LeaderboardStatsRequest 0x012F0518 as the logs showed it
    w = tdf.Writer().int("COUN", 200).int("LBID", 0).string("NAME", name).int("POFF", poff).int("PRID", 0)
    w.int("STRT", 1).int("TIME", 0)
    return bytes(objid(w, "USET", 0, 0, 0))


def lbfix_petition_request():  # FilePetitionRequest 0x012FC350 (made-up names and lines; the shape of the client's)
    w = objid(tdf.Writer(), "COID", 0, 0, 0).string("COTY", "Harassment")
    w.string("PTDE", '[\n{\n"type":"Chat",\n"senderID":%d,\n"senderName":"%s",\n"message":"hello"\n}\n]'
             % (B11_SECOND_PID, B11_SECOND_NAME))
    w.string("SUBJ", "Player Report (name: %s) (type: 1, Griefing (Intentionally Feeding/Trolling))" % B11_SECOND_NAME)
    w.string("TMZO", "GMT-8").list("TRGT", INT, [B11_SECOND_PID])
    return bytes(w)


LBFIX_CASES = {
    "getleaderboard_reply": lambda: lbfix_leaderboard([(B11_SECOND_PID, B11_SECOND_NAME, 4, 3, LBFIX_SHAPER),
                                                       (B5_PID, B5_NAME, 5, 3, 0)]),
    "getleaderboard_empty_reply": lambda: lbfix_leaderboard([]),
    "filepetition_reply": lambda: bytes(tdf.Writer().string("GUID", "report-1")),
    "selectcategoryupdates_reply": lambda: bytes(tdf.Writer().int("VWID", 0)),
}


WRITER_CASES = {
    "ints": case_ints, "labels": case_labels, "strings": case_strings, "blobs": case_blobs,
    "structs": case_structs, "lists": case_lists, "maps": case_maps, "unions": case_unions,
    "list_unions": case_list_unions,
    "getserverinstance_reply": lambda: get_server_instance("127.0.0.1", 10041),
    "preauth_reply": lambda: preauth("127.0.0.1", 17502, "battlegrounds-1-pc"),
    "ping_reply": lambda: ping(1791405059),
    "login_reply": login_reply,
    "loginpersona_reply": login_persona_reply,
    "userauthenticated_notification": user_authenticated,
    "extendeddataupdate_notification": extended_data_update,
    "postauth_reply": lambda: post_auth_reply("127.0.0.1", 9988),
    "gettelemetryserver_reply": lambda: bytes(telemetry_server(tdf.Writer(), "127.0.0.1", 9988)),
    "enterservice_reply": enter_service_reply,
    **B5_META_CASES,
    **B8_CASES,
    **B7A_CASES,
    **C1_CASES,
    **B11_CASES,
    **A5_CASES,
    **B10A_CASES,
    **B10C_CASES,
    **A5B_CASES,
    **B7C_CASES,
    **B7D_CASES,
    **B12_CASES,
    **B13_CASES,
    **G3_CASES,
    **B13B_CASES,
    **B14_CASES,
    **B15_CASES,
    **PH5B_CASES,
    **LBFIX_CASES,
}


# --------------------------------------------------------------------------- decode-only cases
def hdr(label, t):
    return tdf.encode_label(label) + bytes([t])


def vi(n):
    return tdf.encode_varint(n)


DECODE_CASES = {
    "intlist": hdr("IL", tdf.INTLIST) + vi(3) + vi(1) + vi(-2) + vi(300) + hdr("EIL", tdf.INTLIST) + vi(0),
    "objtype": hdr("OT", tdf.OBJTYPE) + vi(4) + vi(1),
    "objid": hdr("OI", tdf.OBJID) + vi(30722) + vi(1) + vi(123456789),
    "float": (hdr("F1", tdf.FLOAT) + struct.pack(">f", 1.5) + hdr("F2", tdf.FLOAT) + struct.pack(">f", 0.1) +
              hdr("F3", tdf.FLOAT) + struct.pack(">f", -2.0) + hdr("F4", tdf.FLOAT) + struct.pack(">f", 1e20) +
              hdr("F5", tdf.FLOAT) + struct.pack(">f", 3e-6) + hdr("F6", tdf.FLOAT) + struct.pack(">f", 0.0) +
              hdr("F7", tdf.FLOAT) + struct.pack(">f", 1e16) + hdr("F8", tdf.FLOAT) + struct.pack(">f", 123456.0)),
    "timevalue": hdr("TV", tdf.TIMEVALUE) + vi(1791405059000000),
    "list_of_lists": hdr("LL", tdf.LIST) + bytes([tdf.LIST]) + vi(2) + bytes([INT]) + vi(2) + vi(1) + vi(2)
    + bytes([STRING]) + vi(0),
    "list_of_unions": hdr("LU", tdf.LIST) + bytes([tdf.UNION]) + vi(2) + bytes([0]) + hdr("VALU", INT) + vi(9)
    + bytes([tdf.UNION_UNSET]),
    "list_of_blobs_floats": hdr("LB", tdf.LIST) + bytes([tdf.BLOB]) + vi(1) + vi(2) + b"\x00'"
    + hdr("LF", tdf.LIST) + bytes([tdf.FLOAT]) + vi(1) + struct.pack(">f", 2.5)
    + hdr("LO", tdf.LIST) + bytes([tdf.OBJTYPE]) + vi(1) + vi(1) + vi(2),
    "list_of_empty_structs": hdr("LE", tdf.LIST) + bytes([STRUCT]) + vi(2) + b"\x00" + b"\x00",
    "map_of_lists": hdr("ML", tdf.MAP) + bytes([STRING, tdf.LIST]) + vi(2) + vi(2) + b"a\x00" + bytes([INT]) + vi(1)
    + vi(5) + vi(2) + b"b\x00" + bytes([INT]) + vi(0),
    "map_blob_keys": hdr("MB", tdf.MAP) + bytes([tdf.BLOB, INT]) + vi(1) + vi(2) + b"k'" + vi(1),
    "map_list_keys": hdr("MK", tdf.MAP) + bytes([tdf.LIST, INT]) + vi(2) + bytes([INT]) + vi(2) + vi(1) + vi(2)
    + vi(3) + bytes([INT]) + vi(1) + vi(4) + vi(6),
    "map_duplicate_keys": hdr("MD", tdf.MAP) + bytes([STRING, INT]) + vi(3) + vi(2) + b"a\x00" + vi(1)
    + vi(2) + b"b\x00" + vi(2) + vi(2) + b"a\x00" + vi(3),
    "map_union_values": hdr("MU", tdf.MAP) + bytes([INT, tdf.UNION]) + vi(1) + vi(1) + bytes([3])
    + hdr("VALU", STRING) + vi(2) + b"z\x00",
    "struct_in_union_in_map": hdr("SM", tdf.MAP) + bytes([STRING, STRUCT]) + vi(1) + vi(2) + b"k\x00"
    + hdr("U", tdf.UNION) + bytes([0]) + hdr("VALU", STRUCT) + hdr("A", INT) + vi(1) + b"\x00" + b"\x00",
    "duplicate_labels": hdr("DUP", INT) + vi(1) + hdr("OTHR", INT) + vi(2) + hdr("DUP", STRING) + vi(2) + b"x\x00",
    "string_no_nul": hdr("SNN", STRING) + vi(3) + b"abc",
    "string_bad_utf8": hdr("SBU", STRING) + vi(9) + b"a\xff\xe2\x82b\xed\xa0\x80\x00",
    "struct_unterminated": hdr("SU", STRUCT) + hdr("A", INT) + vi(1),
    "empty": b"",
    "getserverinstance_request": get_server_instance_request(),
    "preauth_request": preauth_request(),
    "login_request": login_request(),
    "loginpersona_request": login_persona_request(),
    "creategame_request": create_game_request(),
    "saveusersettings_request": save_user_settings_request(),
    "setuserinfoattribute_request": set_user_info_attribute_request(),
    "createplaygroup_request": create_playgroup_request(),
    "startmatchmaking_request": start_matchmaking_request(),
    "startmatchmaking_party_request": start_matchmaking_request(party=True),
    "updatenetworkinfo_request": update_network_info_request(),
    "createplaygroup_party_request": create_party_request(),
    "joinplaygroup_request": join_party_request(),
    "sendmessage_pvpinvite_request": b10c_request(B10C_INVITE_ATTRS, 0, [B11_SECOND_PID], B10C_USER, B10C_PVP_INVITE),
    "sendmessage_inviteanswer_request": b10c_request(B10C_ANSWER_ATTRS, 0, [B5_PID], B10C_USER, B10C_PVP_ANSWER),
    "sendmessage_partychat_request": b10c_request(B10C_CHAT_ATTRS, 6, [1], B10C_PLAYGROUP, B10C_CHAT),
    "sendmessage_whisper_request": b10c_request(B10C_WHISPER_ATTRS, 7, [B11_SECOND_PID], B10C_USER, B10C_WHISPER),
    "filterforprofanity_request": filter_for_profanity_request(),
    "lookupuser_request": lookup_user_request(),
    "getperkpagedata_request": get_perk_page_data_request(),
    "updateperkpage_request": update_perk_page_request(),
    "purchasewithec_request": purchase_request(str(B7C_SPARK), B7C_SPARK, 0),  # Spark: no PC price
    "purchasewithpc_request": purchase_request(str(B7C_PAGE_ITEM), B7C_PAGE_ITEM, 300),
    "getstatsbygroupasync_shaperstats_request": get_stats_by_group_request("ShaperStats", 2, 0, 1),
    "getstatsbygroupasync_shaperhistorystats_request": get_stats_by_group_request("ShaperHistoryStats", 3, 3, 7),
    "getstatsbygroupasync_rolehistorystats_request": get_stats_by_group_request("RoleHistoryStats", 4, 3, 7),
    "getlists_request": get_lists_request(),
    "adduserstolist_request": add_users_to_list_request([B11_SECOND_PID]),
    "adduserstolist_empty_request": add_users_to_list_request([]),
    # B13b: second's friend request to player, player's accept (the add, then the accept message), the unfriend, block
    # and unblock by name, the presence
    "sendmessage_friendrequest_request": b10c_request(B13B_FRIEND_ATTRS, 5, [B5_PID], B10C_USER, B13B_FRIEND_REQUEST),
    "sendmessage_friendaccepted_request": b10c_request(B13B_FRIEND_ATTRS, 5, [B11_SECOND_PID], B10C_USER,
                                                       B13B_FRIEND_ACCEPTED),
    "adduserstolist_friend_request": update_list_members_request("friendList", 1, [(B11_SECOND_PID, "")], 1),
    "removeusersfromlist_friend_request": update_list_members_request("friendList", 1, [(B11_SECOND_PID, "")], 1),
    "adduserstolist_blockbyname_request": update_list_members_request("blockList", 4, [(0, B13B_THIRD_NAME)], 0),
    "removeusersfromlist_unblockbyname_request": update_list_members_request("blockList", 4, [(0, B13B_THIRD_NAME)], 0),
    "updateextendeddataattribute_presence_request": presence_request(B13B_PRESENCE),
    # B15: the game browser's request as g3b_run10b sent it; the custom game's create, the browser's joins (any team,
    # the spectators, a private game with its auth token), the roster manager's attributes and team swaps, the start,
    # a player attribute, the Join Team message to the admin and the list's end, rebuilt from the static reading
    "getgamelistsubscription_request": get_game_list_request(),
    "resetdedicatedserver_custom_request": create_custom_game_request(),
    "joingame_browser_request": join_game_request(0xFFFF),
    "joingame_spectate_request": join_game_request(2),
    "joingame_private_request": join_game_request(0xFFFF, [("PrivateAuthToken", B15_AUTH_TOKEN)]),
    "setgameattributes_request": bytes(tdf.Writer().map("ATTR", STRING, STRING, B15_ROSTER_ATTRS).int("GID", 1)),
    "swapplayersteam_request": swap_players_team_request([(B5_PID, 0), (B11_SECOND_PID, 2)]),
    "advancegamestate_request": bytes(tdf.Writer().int("GID", 1).int("GSTA", 0x83)),
    "setplayerattributes_request": bytes(tdf.Writer().map("ATTR", STRING, STRING, [("PrivateAuthToken", B15_AUTH_TOKEN)])
                                         .int("GID", 1).int("PID", B11_SECOND_PID)),
    "sendmessage_jointeam_request": b10c_request([(0xB0000, "1"), (0xB0001, "2")], 0, [B5_PID], B10C_USER, B15_JOIN_TEAM),
    "destroygamelist_request": bytes(tdf.Writer().int("GLID", 1)),
    # LBFIX: the leaderboard requests (the first as the public server's log showed it, 49 bytes; the second, from the
    # first's callback 0x00AA4FA0, as the live client sent it: POFF 1, 54 bytes), the player report and the logout's
    # selectCategoryUpdates
    "getleaderboard_request": lbfix_leaderboard_request("Skill", 0),
    "getleaderboard_daily_request": lbfix_leaderboard_request("SkillDaily", 1),
    "filepetition_request": lbfix_petition_request(),
    "selectcategoryupdates_request": bytes(tdf.Writer().int("VWID", 0)),
}

ERROR_CASES = {
    "truncated_label": b"\x86",
    "missing_type": tdf.encode_label("ABC"),
    "truncated_varint": hdr("A", INT) + b"\x80",
    "truncated_string": hdr("S", STRING) + vi(5) + b"ab",
    "truncated_float": hdr("F", tdf.FLOAT) + b"\x00\x00",
    "unknown_type": hdr("X", 12) + b"\x00",
    "list_missing_elem_type": hdr("L", tdf.LIST),
    "map_truncated": hdr("M", tdf.MAP) + bytes([STRING, STRING]) + vi(1) + vi(2) + b"a\x00",
    "union_truncated": hdr("U", tdf.UNION) + bytes([0]) + b"\x01",
}

FRAMES = [  # name, component, command, type, msg id, error, payload
    ("getserverinstance_reply", 5, 1, 1, 0, 0, get_server_instance("127.0.0.1", 10041)),
    ("preauth_reply", 9, 7, 1, 0, 0, preauth("127.0.0.1", 17502, "battlegrounds-1-pc")),
    ("ping_reply", 9, 2, 1, 1, 0, ping(1791405059)),
    ("login_request", 1, 0x28, 0, 2, 0, login_request()),
    ("error_reply", 1, 0x28, 3, 2, 0x4004, b""),
    ("notification", 0x7802, 2, 2, 0, 0, tdf.Writer().int("A", 1).__bytes__()),
    ("msgid_masked", 9, 2, 0, 0x123456, 0, b""),
    # B12: Authentication::logout as the client sent it after its AFK kick (c1b_run11: request id 71, no
    # payload) and our empty reply (its RPC job has no response class)
    ("logout_request", 1, 0x46, 0, 71, 0, b""),
    ("logout_reply", 1, 0x46, 1, 71, 0, b""),
    # LBFIX: the error reply to a request without a handler (ERR_COMMAND_NOT_FOUND; ERR_COMPONENT_NOT_FOUND 0x4002
    # when no command of the component has one), here UserSessions::resumeSession (0x23), which the client sends after
    # a lost session
    ("resumesession_notfound_reply", 0x7802, 0x23, 3, 5, 0x4003, b""),
]


def pretty_or_error(payload):
    try:
        return tdf.pretty(tdf.decode(payload), 2), None
    except Exception as e:  # noqa: BLE001
        return None, str(e)


def main(argv):
    out = argv[1] if len(argv) > 1 else os.path.join(REPO, "server", "testdata")
    os.makedirs(out, exist_ok=True)
    doc = {"generator": "re/scripts/tdf_golden.py", "writer": [], "decode": [], "errors": [], "frames": []}
    for name, build in WRITER_CASES.items():
        payload = bytes(build())
        pretty, err = pretty_or_error(payload)
        assert err is None, (name, err)
        doc["writer"].append({"name": name, "hex": payload.hex(), "pretty": pretty})
    for name, payload in DECODE_CASES.items():
        pretty, err = pretty_or_error(payload)
        assert err is None, (name, err)
        doc["decode"].append({"name": name, "hex": payload.hex(), "pretty": pretty})
    for name, payload in ERROR_CASES.items():
        pretty, err = pretty_or_error(payload)
        assert err is not None, name
        doc["errors"].append({"name": name, "hex": payload.hex(), "error": err})
    for name, comp, cmd, mtype, mid, error, payload in FRAMES:
        pretty, err = pretty_or_error(payload)
        doc["frames"].append({"name": name, "component": comp, "command": cmd, "type": mtype, "id": mid,
                              "error": error, "payload": payload.hex(), "pretty": pretty,
                              "hex": frame(comp, cmd, mtype, mid, payload, error).hex()})
    with open(os.path.join(out, "tdf_golden.json"), "w", encoding="utf-8", newline="\n") as f:
        json.dump(doc, f, indent=1, ensure_ascii=False)
        f.write("\n")
    with open(os.path.join(out, "telemetry_rules.xml"), "wb") as f:
        f.write(TELEMETRY_RULES)
    print(f"wrote tdf_golden.json ({len(doc['writer'])} writer, {len(doc['decode'])} decode, "
          f"{len(doc['errors'])} error cases, {len(doc['frames'])} frames) and telemetry_rules.xml to {out}")


if __name__ == "__main__":
    main(sys.argv)
