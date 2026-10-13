"""Drive the website's admin page from a test: the live checks of a
lane session use it instead of a browser (the pages need no script; every form carries the site's CSRF token).

It logs in as an admin (an account given the role with dgserver --make-admin), then runs one action:

    status <login name> <active|disabled|banned>   set an account's status (searched by its login name)
    delete <login name>                            delete the account (the admin's delete, confirmed)
    matches                                        print the running matches
    end-match                                      wait until a match has a player "in the match", wait --after
                                                   seconds more, then end it (the host's abortGame); prints the page
    audit                                          print the audit log's newest entries
    signup <open|invite|closed>                    set the sign-up mode
    invite [uses] [days] [note]                    make an invite code (default 1 use, 14 days); prints
                                                   "code: <code>" and writes it to --out
    register <login name> [display name]           register on the public /signup form as a player would:
                                                   no admin login (--name not needed), the password from
                                                   --password/--password-file, the invite code from --code or
                                                   --code-file; exit 0 when the account was made

--wait-online N first waits until /status.json counts N players online, --wait-match until a match on
/admin/matches has a player "in the match"; either then waits --after seconds before the action (status, delete),
so a session can disable a player at the main menu or in a match.

Every step is printed with the seconds since the start. The site's certificate is checked against --cafile (the lane's
<data>\\site\\site_selfsigned.pem), unless --insecure.

Usage:
    .venv\\Scripts\\python -I re\\scripts\\admin_page.py --base https://127.0.0.2:18402 --cafile <pem>
        --name admin1 --password-file <file> [--wait-site 60] status player1 disabled
    ... --wait-online 1 --after 10 status player1 banned          (a player at the main menu)
    ... --wait-match --after 30 status player1 disabled           (a player in a match)
    ... end-match [--after 20] [--timeout 300] [--reason "live check"]
    ... invite 1 1 "release check" --out <file>
    admin_page.py --base https://192.168.20.40 --cafile <pem> --password-file <file> --code-file <file> register player2 Player2
"""
import argparse
import html
import json
import http.cookiejar
import os
import re
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

T0 = time.monotonic()
TOKEN_RE = re.compile(r'name="csrf" value="([^"]+)"')


def say(msg):
    print(f"[{time.monotonic() - T0:7.1f}] {msg}", flush=True)


class Site:
    def __init__(self, base, cafile, insecure):
        self.base = base.rstrip("/")
        ctx = ssl.create_default_context(cafile=cafile) if not insecure else ssl._create_unverified_context()
        if not insecure:
            ctx.check_hostname = True
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPSHandler(context=ctx),
                                                  urllib.request.HTTPCookieProcessor(self.jar))

    def request(self, path, fields=None):
        data = None
        if fields is not None:
            data = urllib.parse.urlencode(fields).encode()
        req = urllib.request.Request(self.base + path, data=data)
        try:
            with self.opener.open(req, timeout=30) as res:
                return res.status, res.read().decode("utf-8", "replace")
        except urllib.error.HTTPError as e:
            return e.code, e.read().decode("utf-8", "replace")

    def post(self, path, fields, form="/admin"):
        _, page = self.request(form)
        m = TOKEN_RE.search(page)
        if not m:
            raise SystemExit("no CSRF token on /admin")
        fields = dict(fields, csrf=html.unescape(m.group(1)))
        return self.request(path, fields)

    def wait(self, seconds):
        end = time.monotonic() + seconds
        while True:
            try:
                return self.request("/status.json")
            except (urllib.error.URLError, ConnectionError, OSError) as e:
                if time.monotonic() > end:
                    raise SystemExit(f"the site isn't up after {seconds} s: {e}")
                time.sleep(1)


def text(page):
    """The page's error/done lines, for the log."""
    out = []
    for cls in ("error", "done"):
        for m in re.finditer(r'<p class="%s"[^>]*>(.*?)</p>' % cls, page, re.S):
            out.append(f"{cls}: {html.unescape(m.group(1))}")
    return "; ".join(out) or "(no message)"


def account_id(site, name):
    code, page = site.request("/admin/accounts?" + urllib.parse.urlencode({"q": name}))
    for m in re.finditer(r"<td>(\d+)(?: <b>admin</b>)?(?: \(you\))?</td>\s*<td>([^<]+?)(?: <span|</td>)", page):
        if m.group(2).strip().lower() == name.lower():
            return m.group(1)
    raise SystemExit(f"no account {name!r} on the accounts page ({code})")


def matches(site):
    _, page = site.request("/admin/matches")
    rows = []
    for row in re.findall(r"<tr><td>(\d+)<br>(.*?)</tr>", page, re.S):
        game, rest = row
        players = [html.unescape(p) for p in re.findall(r"([^<>]+ \(account \d+, team \d, [a-z ]+\))<br>", rest)]
        rows.append((int(game), re.sub(r"\s+", " ", html.unescape(re.sub("<[^>]+>", " ", rest))).strip(), players))
    return rows


def online(site):
    _, body = site.request("/status.json")
    try:
        return int(json.loads(body).get("playersOnline", 0))
    except (ValueError, AttributeError):
        return 0


def wait_for(site, what, check, timeout):
    """Polls check() every second until it is true (the --wait-online, --wait-match)."""
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if check():
            say(f"{what}: yes")
            return True
        time.sleep(1)
    say(f"{what}: not within {timeout} s")
    return False


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--base", required=True)
    ap.add_argument("--cafile")
    ap.add_argument("--insecure", action="store_true")
    ap.add_argument("--name", help="the admin's login name (every action but register)")
    ap.add_argument("--password")
    ap.add_argument("--password-file")
    ap.add_argument("--wait-site", type=float, default=60)
    ap.add_argument("--after", type=float, default=20, help="end-match: seconds after a player is in the match")
    ap.add_argument("--timeout", type=float, default=300, help="end-match: give up after this long")
    ap.add_argument("--reason", default="")
    ap.add_argument("--wait-online", type=int, default=0, help="status/delete: first wait until this many players are online")
    ap.add_argument("--wait-match", action="store_true", help="status/delete: first wait until a match has a player in it")
    ap.add_argument("--out", help="invite: write the code to this file")
    ap.add_argument("--code", default="", help="register: the invite code")
    ap.add_argument("--code-file", help="register: read the invite code from this file")
    ap.add_argument("action", choices=["status", "delete", "matches", "end-match", "audit", "signup", "invite", "register"])
    ap.add_argument("args", nargs="*")
    a = ap.parse_args()
    if not a.cafile and not a.insecure:
        ap.error("--cafile or --insecure")
    pw = a.password
    if a.password_file:
        with open(a.password_file, encoding="utf-8") as f:
            pw = f.read().strip()
    if not pw:
        ap.error("--password or --password-file")
    deadline = time.monotonic() + a.wait_site
    while a.cafile and not os.path.exists(a.cafile):  # the server makes it at its start
        if time.monotonic() > deadline:
            raise SystemExit(f"no {a.cafile} after {a.wait_site} s")
        time.sleep(1)
    site = Site(a.base, a.cafile, a.insecure)
    site.wait(max(deadline - time.monotonic(), 5))
    if a.action == "register":  # the player's own registration, no admin
        if not a.args:
            ap.error("register <login name> [display name]")
        invite = a.code
        if a.code_file:
            with open(a.code_file, encoding="utf-8") as f:
                invite = f.read().strip()
        fields = {"name": a.args[0], "display": a.args[1] if len(a.args) > 1 else "", "password": pw, "password2": pw,
                  "code": invite}
        code, page = site.post("/signup", fields, form="/signup")
        say(f"register {a.args[0]} on /signup{' with an invite code' if invite else ''}: {code} {text(page)}")
        return 0 if code == 200 else 1
    if not a.name:
        ap.error("--name (the admin's login name)")
    code, page = site.post("/admin/login", {"name": a.name, "password": pw})
    say(f"admin login as {a.name}: {code} {text(page)}")
    if code != 200:
        return 1
    if a.action in ("status", "delete") and (a.wait_online or a.wait_match):
        if a.wait_online and not wait_for(site, f"{a.wait_online} player(s) online",
                                          lambda: online(site) >= a.wait_online, a.timeout):
            return 1
        if a.wait_match and not wait_for(site, "a match with a player in it", lambda: any(
                "in the match" in p for r in matches(site) for p in r[2]), a.timeout):
            return 1
        for game, info, _ in matches(site):
            say(f"match {game}: {info}")
        say(f"waiting {a.after} s")
        time.sleep(a.after)
    if a.action == "delete":
        name = a.args[0]
        uid = account_id(site, name)
        code, page = site.post("/admin/accounts/delete", {"id": uid, "confirm": "yes", "q": name})
        say(f"delete account {uid} {name}: {code} {text(page)}")
        return 0 if code == 200 else 1
    if a.action == "status":
        name, status = a.args
        uid = account_id(site, name)
        code, page = site.post("/admin/accounts/status", {"id": uid, "status": status, "q": name})
        say(f"status of account {uid} {name} -> {status}: {code} {text(page)}")
        if a.wait_match or a.wait_online:  # what the page shows afterwards
            time.sleep(5)
            say(f"players online now: {online(site)}")
            for game, info, _ in matches(site):
                say(f"match {game} now: {info}")
        return 0 if code == 200 else 1
    if a.action == "signup":
        code, page = site.post("/admin/signup", {"mode": a.args[0]})
        say(f"sign-up mode -> {a.args[0]}: {code} {text(page)}")
        return 0 if code == 200 else 1
    if a.action == "invite":
        uses = a.args[0] if a.args else "1"
        days = a.args[1] if len(a.args) > 1 else "14"
        note = a.args[2] if len(a.args) > 2 else ""
        code, page = site.post("/admin/invites", {"uses": uses, "days": days, "note": note})
        m = re.search(r'<p class="secret">([^<]+)</p>', page)
        say(f"invite ({uses} use(s), {days} day(s)): {code} {text(page)}")
        if code != 200 or not m:
            return 1
        inv = html.unescape(m.group(1)).strip()
        print(f"code: {inv}", flush=True)
        if a.out:
            with open(a.out, "w", encoding="utf-8") as f:
                f.write(inv + "\n")
        return 0
    if a.action == "matches":
        for game, info, players in matches(site):
            say(f"match {game}: {info}")
        return 0
    if a.action == "audit":
        _, page = site.request("/admin/audit")
        for row in re.findall(r"<tr><td>(\d{4}-.*?)</tr>", page, re.S):
            cells = [html.unescape(c) for c in re.findall(r"(?:^|<td>)([^<]*)</td>", row)]
            say(" | ".join(cells))
        return 0
    # end-match
    end = time.monotonic() + a.timeout
    seen = None
    while time.monotonic() < end:
        rows = [r for r in matches(site) if any("in the match" in p for p in r[2])]
        if rows:
            seen = rows[0]
            break
        time.sleep(2)
    if not seen:
        say("no match with a player in it")
        return 1
    say(f"match {seen[0]} has a player in it: {seen[1]}")
    time.sleep(a.after)
    for game, info, _ in matches(site):
        if game == seen[0]:
            say(f"match {game} before the end: {info}")
    code, page = site.post("/admin/matches/end", {"game": seen[0], "confirm": "yes", "reason": a.reason})
    say(f"end match {seen[0]}: {code} {text(page)}")
    for _ in range(15):
        time.sleep(1)
        if all(g != seen[0] for g, _, _ in matches(site)):
            say(f"match {seen[0]} is gone from the page")
            break
    return 0 if code == 200 else 1


if __name__ == "__main__":
    sys.exit(main())
