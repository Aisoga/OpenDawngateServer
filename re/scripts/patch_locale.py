"""Add the server's login texts and links to a working copy: game/Data/Locale/en-us/AA_Server.package.

The client looks every string table up by {instance = FNV-1 of the table's name, type 0x02FAC0B6, group 0x02FABF01} in
every *.package of game/Data/Locale/en-us/, and the package whose name sorts first wins (an added
AA_*.package was seen winning over Locale.package live). So this script writes changed copies of five of the copy's own tables into an added package
beside the shipped Locale.package, which stays untouched (the game's text can't ship: the package is made from the
copy's own file at setup time):

  BlazeErrors  0x1E5BC096   the login panel's texts: "login name" instead of "email" (0x000b0001, 0x000c0001,
                            0x00160001), the name and password rules (0x00ca0001, which has no line in the build; also
                            0x400c0000), too many sign-ups or attempts (0x40720000, no line in the build; the cap and
                            the limits), banned / disabled / deleted without EA's help address (0x00200001,
                            0x002b0001, 0x00650001, 0x00290001), Start-Client's server picker refusing a login
                            before it reached a server (0x00e00001-0x00e60001: unknown server, no answer, the key
                            changed, no tunnel, the server failed, removed, not an address, This PC's, the version,
                            added: 0x00e00001-0x00ec0001); 0x000b0001 also points to Sign up now! (a server that
                            needs a sign-up first refuses an unknown name and a wrong password with it)
  LoginScreen  0x69E51800   the field label 0x0ac5be93 "Enter Login Name", the check box 0x0ac5be95, the server
                            picker's labels, status words and sentences (0x5e70a101-0x5e70a137)
  URLs         0x30B0670B   the links the login screen opens in the player's browser: Forgot Password? (line 3), Sign
                            up now! (line 6, also 4 and 5), the legal links (1, 2, 7), forum, survey and funds (0x100,
                            0x101, 0x103), all at the server's own pages instead of EA's and Waystone's dead (and
                            re-registrable) domains; 0x102 (Adobe's Flash download) stays; and the server picker's
                            own links, Sign up now! and Forgot Password? per server of its list (0x5e70b100 +
                            slot, 0x5e70b200 + slot, 64 slots: http://<host>:<http port>/dawngate/pick/<slot>/<page>,
                            which Start-Client's front answers)
  FE_Leaderboard 0xE3B520DE the Leaderboard screen: the title 0x100 "Top 200" (was "Solo Top 200") and the rule 0x101
                            as dgserver ranks: PvP and custom games only, at least LEADERBOARD_MIN_GAMES (10, dgserver's
                            default --leaderboard-min-games; a server that changes it makes this number wrong)
  FE_MatchType 0xFF007FE0   the PLAY screen's lines that said a group's games don't count (0x0fd838a1, 0x0fd84a0a,
                            0x0ec1b32d): group PvP queue games count

The links (SITE_PAGES): without a website, http://<host>:<http port>/dawngate/<page>, which dgserver's HTTP port
answers with a short page of its own (server/internal/web/sitepages.go: how sign-up works in its mode, ask the operator
for a password reset); with --web-domain <D> (the website), https://<D>/<page>. The pages are "" (the start
page), signup, password, terms and privacy. <host> is the address the copy is patched for (patch_config.py --host).

The text rule (server/internal/setup/localepatch is its Go twin, byte for byte; tests: test_ph2b.py and the Go
package's tests pin the same SHA-256s): a table is cut at "\\n"; a line whose text (after a UTF-8 BOM on the first
line, before a final "\\r") starts with "0x", 8 hex digits and a blank gets the new text after that prefix, the line's
ends kept; ids the table lacks are appended after the comment line ADDED_COMMENT, each as "0x<id, 8 lower-case hex
digits> <text>\\r\\n" in id order (a table not ending in "\\n" gets "\\r\\n" first); a table missing from Locale.package
(only in a synthetic test build) is made of those appended lines alone. The package holds the five tables in instance
order, stored uncompressed (dbpf.py write_package).

Usage:
    .venv/Scripts/python -I re/scripts/patch_locale.py <working copy game/Data dir> [--host 127.0.0.1]
        [--http-port 18080] [--web-domain <D>] [--check | --remove | --print]
--check exits 1 when the package isn't what this script makes now; --remove deletes it; --print shows the lines.
Refuses to touch the pristine extractions under client/ia_*.
"""
import argparse
import hashlib
import ipaddress
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import dbpf  # noqa: E402

PACKAGE = "AA_Server.package"
LOCALE_DIR = ("Locale", "en-us")
SHIPPED = "Locale.package"
REL = "game/Data/Locale/en-us/" + PACKAGE  # in a copy (setup_all.py, test_p3b.py)
SOURCE = "game/Data/Locale/en-us/" + SHIPPED  # the copy's own tables it is made from
STRING_TYPE, STRING_GROUP = 0x02FAC0B6, 0x02FABF01
BLAZE_ERRORS, URLS, LOGIN_SCREEN = 0x1E5BC096, 0x30B0670B, 0x69E51800
FE_LEADERBOARD, FE_MATCH_TYPE = 0xE3B520DE, 0xFF007FE0  # FNV-1 of fe_leaderboard, fe_matchtype
LEADERBOARD_MIN_GAMES = 10  # dgserver's --leaderboard-min-games default (blaze.LeaderboardMinGames)
HTTP_PORT = 18080
FALLBACK_PREFIX = "dawngate/"  # dgserver's own pages on its HTTP port (web.SitePrefix)
ADDED_COMMENT = "# PH2b: lines the build lacks, added by the server's setup"

# the pages a link opens (server/internal/sitelinks); "" is the start page
SITE_PAGES = ("", "signup", "password", "terms", "privacy")

ERROR_LINES = {
    # a server that needs a sign-up first (invite, closed) refuses an unknown name and a wrong password alike with it
    0x000B0001: "The login name or password you entered is incorrect. No account on this server yet? Make one first: "
                "Sign up now! below.",
    0x000C0001: "The login name or password you entered is incorrect.",
    0x00160001: "Your login name contains invalid characters. Use letters, digits and . _ - @ only.",
    0x00CA0001: "Too short: a login name needs at least 3 characters and a password at least 8.",
    0x00200001: "This account has been banned. Please contact the server's operator.",
    0x00290001: "This account has been deleted. Please contact the server's operator if this is a mistake.",
    0x002B0001: "This account has been banned. Please contact the server's operator.",
    0x00650001: "This account has been disabled. Please contact the server's operator.",
    0x400C0000: "Too long: a login name may have at most 32 characters and a password at most 63.",
    0x40720000: "Too many sign-ups or login attempts from your address. Please wait a while and try again.",
    # Start-Client's server picker (launcher.FrontErr*): the login refused before it reached a server
    0x00E00001: "That server isn't in your list. Pick one with SERVER above the login, or add it there.",
    0x00E10001: "The server you picked doesn't answer. It may be offline, or its address may be wrong. Try again later.",
    0x00E20001: "The server's key has changed since you last played, so the login was refused. Ask its operator: see the "
                "troubleshooting guide.",
    0x00E30001: "Something answers at that address, but no Dawngate server with the tunnel on. Ask its operator.",
    0x00E40001: "The server you picked answered, but couldn't take the login. Try again in a moment.",
    0x00E50001: "The server was removed from your list.",
    0x00E60001: "That isn't a server address. Use an IPv4 address such as 203.0.113.5, or a host name.",
    0x00E70001: "New server key: check the fingerprint shown in the list, then log in again to trust it.",
    0x00E80001: "This PC is always in the list: it can't be removed.",
    0x00E90001: "The server on this PC is still being set up. Log in again when the list says online.",
    0x00EA0001: "The server on this PC didn't start: Start-Client's window says why.",
    0x00EB0001: "This server runs another version than your Start-Client, or doesn't tell its version: both need the "
                "same one. SERVER above the login shows both versions; restart Start-Client to update.",
    0x00EC0001: "The server was added to your list. Pick it and log in, or use Sign up now! below to make an account "
                "there first.",
}
# the field label, the check box and the server picker's texts (client_ui/serverpicker/ServerPicker.as reads
# 0x5e70a1xx through Global.ForceTranslateString, with the same English as its fallback; {1} a ping, {2} the key a
# server shows, {3} the trusted key; in the version lines {1} the server's version, {2} Start-Client's)
LOGIN_LINES = {
    0x0AC5BE93: "Enter Login Name",
    0x0AC5BE95: "Remember Login Name",
    0x5E70A101: "SERVER",
    0x5E70A102: "SELECT A SERVER",
    0x5E70A103: "ADD SERVER",
    0x5E70A104: "REMOVE",
    0x5E70A105: "DONE",
    0x5E70A106: "ADD",
    0x5E70A107: "CANCEL",
    0x5E70A108: "Server address",
    0x5E70A109: "Name (optional)",
    0x5E70A10A: "ADD checks the server and adds it to your list. Then pick it: Sign up now! below the login opens its "
                "sign-up page.",
    0x5E70A10B: "Type the server's address: an IPv4 address such as 203.0.113.5, or a host name.",
    0x5E70A10C: "No servers yet: add one.",
    0x5E70A10D: "none: add a server",
    0x5E70A10E: "pick a server",
    0x5E70A10F: "This PC",
    0x5E70A110: "online, {1} ms",
    0x5E70A111: "online, new key",
    0x5E70A112: "KEY CHANGED",
    0x5E70A113: "offline",
    0x5E70A114: "no tunnel",
    0x5E70A115: "added at login",
    0x5E70A116: "not checked",
    0x5E70A117: "starts at login",
    0x5E70A118: "setting up, {1} %",
    0x5E70A119: "starting",
    0x5E70A11A: "didn't start",
    0x5E70A11B: "other version",
    0x5E70A11C: "version unknown",
    0x5E70A120: "Online, {1} ms. Its key: {2}.",
    0x5E70A121: "New server key {2}: compare it with the fingerprint its operator gives you. The first login stops "
                "here; log in again to trust it.",
    0x5E70A122: "KEY CHANGED: you trusted {3}, the server now shows {2}. Don't log in until its operator confirms the "
                "new key: see the troubleshooting guide.",
    0x5E70A123: "No answer from this server. It may be offline, or its address may be wrong.",
    0x5E70A124: "Something answers at this address, but no Dawngate server with the tunnel on.",
    0x5E70A125: "Not checked yet.",
    0x5E70A126: "The server on this PC. It starts when you log in; the first time it sets up its match host first (about "
                "half a minute).",
    0x5E70A127: "Setting up the server on this PC: {1} %. Your login waits for it, or log in again when it says online.",
    0x5E70A128: "The server on this PC is starting.",
    0x5E70A129: "The server on this PC didn't start: Start-Client's window says why. Log in again to retry.",
    0x5E70A12A: "This server runs version {1}, your Start-Client is {2}. A login needs the same version: use the "
                "server's release.",
    0x5E70A12B: "This server doesn't tell its version (an older release). Your Start-Client is {2}: a login needs the "
                "same version.",
    0x5E70A12C: "Version {1}.",
    0x5E70A130: "open",
    0x5E70A131: "invite only",
    0x5E70A132: "closed",
    0x5E70A133: "no password",
    0x5E70A134: "Sign-up open: your first login with a new name makes the account.",
    0x5E70A135: "Sign up first: this server needs an invite code (Sign up now! below the login).",
    0x5E70A136: "Sign up first: only its operator makes accounts (Sign up now! below the login says how).",
    0x5E70A137: "No sign-up and no password here: any name logs in.",
}
# the main menu's Leaderboard screen: its title and the rule under it, as dgserver ranks (only PvP and custom games
# count, practice games don't; the number is dgserver's default --leaderboard-min-games)
LEADERBOARD_LINES = {
    0x00000100: "Top 200",
    0x00000101: f"Rank is based upon PvP and custom games. Must have at least {LEADERBOARD_MIN_GAMES} games played.",
}
# the PLAY screen's lines that said a group's games don't count for the Leaderboard (0x0fd84a0a keeps its 80 blanks,
# which break the line in the tile)
MATCH_TYPE_LINES = {
    0x0FD838A1: "Queue for a game with friends. Impacts Leaderboard ranking.",
    0x0FD84A0A: "Match with other players of similar skill." + " " * 80 + "Solo and group games impact Leaderboard ranking.",
    0x0EC1B32D: "Games queued as a group impact Leaderboard ranking too.",
}
URL_PAGES = {  # line id -> page
    0x00000001: "terms", 0x00000002: "privacy", 0x00000003: "password", 0x00000004: "signup",
    0x00000005: "signup", 0x00000006: "signup", 0x00000007: "terms", 0x00000100: "", 0x00000101: "",
    0x00000103: "",
}

# the server picker's own links (server/internal/setup/localepatch's LinkSlots): Sign up now! and Forgot Password? of
# each server in the login screen's list, by its slot, always to the HTTP port of the address the copy is patched for
# (Start-Client's front answers them), also in a copy with a website
LINK_SLOTS = 64
SLOT_SIGNUP, SLOT_PASSWORD = 0x5E70B100, 0x5E70B200

_LINE = re.compile(rb"0[xX]([0-9A-Fa-f]{8}) ")
_DOMAIN = re.compile(r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+"
                     r"(?::[0-9]{1,5})?")


class LocaleError(Exception):
    pass


def check_options(host, http_port=HTTP_PORT, web_domain=None):
    """The checked (host, port, domain): host an IPv4 address, the port 1..65535, the domain a DNS name with dots
    (ASCII letters, digits, '-'), optionally :port, lower-cased; None/'' = no website."""
    try:
        ipaddress.IPv4Address(host)
    except (ipaddress.AddressValueError, ValueError):
        raise LocaleError(f"the server address must be an IPv4 address, not {host!r}") from None
    if not isinstance(http_port, int) or not 1 <= http_port <= 65535:
        raise LocaleError(f"the HTTP port must be 1..65535, not {http_port!r}")
    if web_domain:
        if not web_domain.isascii() or not _DOMAIN.fullmatch(web_domain):
            raise LocaleError(f"the web domain must be a DNS name such as dawngate.example.org, not {web_domain!r}")
        _, _, port = web_domain.partition(":")
        if port and not 1 <= int(port) <= 65535:
            raise LocaleError(f"the web domain's port must be 1..65535, not {port}")
        web_domain = web_domain.lower()
    return host, http_port, web_domain or None


def site_base(host, http_port=HTTP_PORT, web_domain=None):
    """Where the links point: the website or dgserver's own pages on its HTTP port."""
    host, http_port, web_domain = check_options(host, http_port, web_domain)
    if web_domain:
        return f"https://{web_domain}/"
    return f"http://{host}:{http_port}/{FALLBACK_PREFIX}"


def tables(host, http_port=HTTP_PORT, web_domain=None):
    """{table instance: {line id: new text}}."""
    base = site_base(host, http_port, web_domain)
    urls = {i: base + p for i, p in URL_PAGES.items()}
    for slot in range(LINK_SLOTS):
        for first, page in ((SLOT_SIGNUP, "signup"), (SLOT_PASSWORD, "password")):
            urls[first + slot] = f"http://{host}:{http_port}/{FALLBACK_PREFIX}pick/{slot}/{page}"
    return {BLAZE_ERRORS: dict(ERROR_LINES), URLS: urls, LOGIN_SCREEN: dict(LOGIN_LINES),
            FE_LEADERBOARD: dict(LEADERBOARD_LINES), FE_MATCH_TYPE: dict(MATCH_TYPE_LINES)}


def patch_table(text, lines):
    """The table `text` (bytes) with `lines` ({id: str}) put in (the module docstring's rule)."""
    parts = text.split(b"\n")
    seen = set()
    for i, part in enumerate(parts):
        body, cr = (part[:-1], b"\r") if part.endswith(b"\r") else (part, b"")
        bom = b""
        if i == 0 and body.startswith(b"\xef\xbb\xbf"):
            bom, body = b"\xef\xbb\xbf", body[3:]
        m = _LINE.match(body)
        if not m:
            continue
        lid = int(m.group(1), 16)
        if lid in lines:
            seen.add(lid)
            parts[i] = bom + body[:m.end()] + lines[lid].encode("utf-8") + cr
    out = b"\n".join(parts)
    missing = sorted(set(lines) - seen)
    if missing:
        if out and not out.endswith(b"\n"):
            out += b"\r\n"
        out += (ADDED_COMMENT + "\r\n").encode("utf-8")
        for lid in missing:
            out += f"0x{lid:08x} {lines[lid]}\r\n".encode("utf-8")
    return out


def find_table(pkg, instance):
    for e in pkg.entries:
        if e.type == STRING_TYPE and e.group == STRING_GROUP and e.instance == instance:
            return pkg.read(e)
    return b""


def build(locale_path, host, http_port=HTTP_PORT, web_domain=None):
    """The bytes of AA_Server.package made from the copy's Locale.package at `locale_path`."""
    pkg = dbpf.Package(locale_path)
    want = tables(host, http_port, web_domain)
    return dbpf.write_package([(STRING_TYPE, STRING_GROUP, inst, patch_table(find_table(pkg, inst), want[inst]))
                               for inst in sorted(want)])


def paths(data_dir):
    loc = os.path.join(data_dir, *LOCALE_DIR)
    return os.path.join(loc, SHIPPED), os.path.join(loc, PACKAGE)


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("data_dir", help="working copy game/Data directory")
    ap.add_argument("--host", default="127.0.0.1", help="the server address the copy is patched for")
    ap.add_argument("--http-port", type=int, default=HTTP_PORT, help="dgserver's HTTP port (its fallback pages)")
    ap.add_argument("--web-domain", help="the server's website: the links go to https://<D>/<page>")
    mode = ap.add_mutually_exclusive_group()
    mode.add_argument("--check", action="store_true", help="exit 1 when the package isn't current")
    mode.add_argument("--remove", action="store_true", help="delete the package")
    mode.add_argument("--print", action="store_true", help="print the lines put in and where the links go")
    a = ap.parse_args(argv[1:])
    if not sys.flags.isolated:
        sys.exit("run this with python -I: it reads the game's files")
    data_dir = os.path.abspath(a.data_dir)
    if os.sep + "ia_" in data_dir or "/ia_" in data_dir:
        sys.exit("refusing to patch a pristine extraction (client/ia_*); use a working copy")
    shipped, added = paths(data_dir)
    try:
        if a.print:
            for inst, lines in tables(a.host, a.http_port, a.web_domain).items():
                for lid, text in sorted(lines.items()):
                    print(f"{inst:08X} 0x{lid:08x} {text}")
            return 0
        if a.remove:
            if os.path.isfile(added):
                os.remove(added)
                print(f"removed {added}")
            return 0
        want = build(shipped, a.host, a.http_port, a.web_domain)
    except (LocaleError, OSError, ValueError) as e:
        sys.exit(f"patch_locale: {e}")
    digest = hashlib.sha256(want).hexdigest()[:16]
    have = open(added, "rb").read() if os.path.isfile(added) else None
    if have == want:
        print(f"{added}: current ({digest}, links {site_base(a.host, a.http_port, a.web_domain)})")
        return 0
    if a.check:
        print(f"{added}: {'missing' if have is None else 'not current'} (want {digest})")
        return 1
    tmp = added + ".partial"
    with open(tmp, "wb") as f:
        f.write(want)
    os.replace(tmp, added)
    print(f"{added}: written ({len(want)} bytes, {digest}, links {site_base(a.host, a.http_port, a.web_domain)})")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
