package launcher

import (
	"fmt"
	"io"

	"dawngate/server/internal/setup/exepatch"
)

// A server on a public (internet) address is allowed, at the operator's own risk. The notices say what is
// true (docs/OPERATORS.md "What must never be exposed"); Start-Server runs on a public address only with an
// explicit acknowledgement (--i-accept-the-risk or i_accept_the_risk = yes in Start-Server.ini).

// RiskAckFlag is Start-Server's acknowledgement flag, RiskAckKey its Start-Server.ini key.
const (
	RiskAckFlag = "i-accept-the-risk"
	RiskAckKey  = "i_accept_the_risk"
)

// IsPublicAddress says whether a server address is on the internet (not loopback, private, link-local or shared).
func IsPublicAddress(address string) bool { return exepatch.IsPublic(address) }

// ServerRiskNotice prints what a public server means for its operator.
func ServerRiskNotice(w io.Writer, address string) {
	fmt.Fprintf(w, `
==== USE AT YOUR OWN RISK: %s is a public internet address ====
This server was built for friends on a home network and has NO real security:
  - Anyone who can reach it can log in with any e-mail and password (the first login creates the account),
    and can use any account that has the same e-mail.
  - The game's Blaze connection is plain TCP, not encrypted; passwords are written to the logs as typed.
  - The TLS parts use the game's old TLS 1.1 with MD5 and RSA-1024 certificates: that is no protection against
    anyone who wants to listen in or pretend to be your server.
  - The match hosts' control channel has no login (Start-Server keeps it on 127.0.0.1; never expose it).
  - Nothing limits connections or login attempts: strangers can fill your accounts, disk and bandwidth.
You must forward these ports on your router and firewall yourself (this program never opens ports or uses UPnP):
  TCP 42127 (the redirector, TLS), TCP 10041 (Blaze, plain), TCP+UDP 17502 (QoS), TCP 18080 (web), TCP 9988 (telemetry),
  UDP 10042-10051 (match hosts).
Only run this if you accept that. Never use a password you use anywhere else. See docs/OPERATORS.md.
`, address)
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

// ClientRiskNotice is the short notice for a player connecting to a public address.
func ClientRiskNotice(w io.Writer, address string) {
	fmt.Fprintf(w, `
  Note: %s is a public internet address. Dawngate servers have no real security: your connection is not
  encrypted and the server sees (and may log) your password as typed. Play only on a server you trust, and use
  a password you use nowhere else.
`, address)
}
