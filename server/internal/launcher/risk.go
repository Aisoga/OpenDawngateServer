package launcher

import (
	"fmt"
	"io"

	"dawngate/server/internal/setup/exepatch"
	"dawngate/server/internal/tunnel"
)

// A server on a public (internet) address is allowed, at the operator's own risk; Start-Server runs on a public
// address only with an explicit acknowledgement (--i-accept-the-risk or i_accept_the_risk = yes in Start-Server.ini).
// The release has what a public server needs (checked passwords and the sign-up modes, the tunnel, the limits, the
// strict message check, the website with self-service and the admin page), and the public profile turns it all on; the
// notice now says what that covers and what remains the operator's risk and duty (docs/hosting.md *Hosting on the
// internet*, *Security*).

// RiskAckFlag is Start-Server's acknowledgement flag, RiskAckKey its Start-Server.ini key.
const (
	RiskAckFlag = "i-accept-the-risk"
	RiskAckKey  = "i_accept_the_risk"
)

// IsPublicAddress says whether a server address is on the internet (not loopback, private, link-local or shared).
func IsPublicAddress(address string) bool { return exepatch.IsPublic(address) }

// RiskInfo is what the server notice names: the public address, the bind address (empty or the same: this PC holds
// the public address itself), the tunnel's port (0 = the default), the sign-up mode and the website's domain, and
// whether a reverse proxy serves the website (web_proxy = on).
type RiskInfo struct {
	Address, Bind string
	GatewayPort   int
	Signup        string
	WebDomain     string
	WebProxy      bool
	LogDays       int
}

// ServerRiskNotice prints what a public server means for its operator (the public profile: the tunnel is always on).
func ServerRiskNotice(w io.Writer, r RiskInfo) {
	gp := r.GatewayPort
	if gp == 0 {
		gp = tunnel.DefaultPort
	}
	signup := "signup = " + r.Signup + ": "
	switch r.Signup {
	case "open":
		signup += "anyone who reaches the server can make accounts (at most signups_per_address a day per address);\n" +
			"    invite or closed keeps strangers out"
	case "closed":
		signup += "only you make accounts"
	default:
		signup += "only players with an invite code you made can make accounts"
	}
	logs := "log_days deletes old logs"
	if r.LogDays > 0 {
		logs = fmt.Sprintf("log_days = %d deletes them after that many days", r.LogDays)
	}
	fmt.Fprintf(w, `
==== A PUBLIC SERVER: %s is an internet address. You run it at your own risk. ====
What the public profile does for you:
  - Every player connection goes through the encrypted tunnel (TLS 1.3 and AES-GCM, TCP and UDP %d); each
    player's Start-Client checks this server's key. The server has no other port for players.
  - Passwords are checked and kept only as hashes (never logged); %s.
  - Limits on connections, logins, games and packets per address; the match hosts check every message a
    client sends before the game's 2014 code sees it.
What remains your risk and your duty:
  - The game is 2014 code (no ASLR). The players' PCs trust the server they play on: a server that is broken
    into or taken over could attack its players' game. Keep this PC updated and use it for nothing sensitive,
    and keep Dawngate-Data\certs (the keys) private.
  - Players behind one internet address (a household, a dormitory, a mobile network) share the limits: a
    household fits, a LAN party behind one address may be slowed down.
  - You keep personal data: login and display names, the players' addresses in the logs (%s), match
    history. Data-protection law (e.g. the GDPR) may apply to you: say who runs the server and what it keeps
    on the website's privacy page (Dawngate-Data\data\site\privacy.html), and answer players' requests (they
    can download and delete their own data on the website).
  - Back up the accounts: Dawngate-Data\data\accounts.db (README.txt, "Accounts and the website").
  - The match hosts' control channel (TCP 10040) has no login: it stays on 127.0.0.1; never forward it.
Forward on your router only TCP and UDP %d (the tunnel) and TCP 443 (the website%s); this program never opens
ports and never uses UPnP.
`, r.Address, gp, signup, logs, gp, riskWebsiteNote(r))
	if r.Bind == "" || r.Bind == r.Address {
		fmt.Fprintf(w, `This PC holds the public address itself (no router): only the tunnel's port and the website listen on it
(the match hosts listen on %s, a loopback address). Let your firewall in only TCP and UDP %d and TCP 443.
`, HostsAddress(r.Address), gp)
	}
	fmt.Fprintln(w, "Never use a password you use anywhere else. See README.txt (\"Hosting on the internet\").")
}

// riskWebsiteNote completes the notice's "TCP 443 (the website...)".
func riskWebsiteNote(r RiskInfo) string {
	switch {
	case r.WebProxy:
		return ", served by your reverse proxy, which holds its certificate"
	case r.WebDomain != "":
		return ", for Let's Encrypt too"
	}
	return ""
}

// ServerRiskRefusal is what Start-Server says when a public address wasn't acknowledged.
func ServerRiskRefusal(w io.Writer, ini string) {
	fmt.Fprintf(w, `
Start-Server will not start on a public address until you accept this risk. If you do, either start it as
  Start-Server.exe --%s
or add this line to %s:
  %s = yes
`, RiskAckFlag, ini, RiskAckKey)
}

// ClientRiskNotice is the short notice for a player connecting to a public address (through the tunnel or not).
func ClientRiskNotice(w io.Writer, address string, tunnel bool) {
	if tunnel {
		fmt.Fprintf(w, `
  Note: %s is a public internet address. Your connection goes through the encrypted tunnel to the server whose
  key you confirm. The game is 2014 code: play only on a server you trust (it sees your password as you type it
  and could, if it were taken over, attack your game), and use a password you use nowhere else.
`, address)
		return
	}
	fmt.Fprintf(w, `
  Note: %s is a public internet address. Without the tunnel your connection is not encrypted and the server sees
  (and may log) your password as typed. Play only on a server you trust, and use a password you use nowhere else.
`, address)
}
