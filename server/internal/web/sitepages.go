package web

import (
	"fmt"
	"html"
	"strings"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/sitelinks"
)

// SitePages are dgserver's own pages at the links of the client's login screen:
// the setups point a player copy's Sign up now!, Forgot Password? and legal links at
// http://<server>:<HTTP port>/dawngate/<page> (sitelinks) while the server has no website of its own, and these
// answer there: how an account is made in the server's sign-up mode, that a forgotten password is the operator's to
// reset (the server never sends mail), and that terms and a privacy notice are the operator's. Plain HTML, no script,
// no form (the HTTP logger hexdumps request bodies, so nothing is ever posted here). the website serves the same
// page names at its root; once it runs, a server with a website answers these paths with a redirect there.
func SitePages(mode accounts.Mode) []Builtin {
	var signup string
	switch mode {
	case accounts.Open:
		signup = fmt.Sprintf(`<p>On this server an account is made at its first login. In the game's login screen, type
a new <b>login name</b> (%d to %d characters: letters, digits and <code>. _ - @</code>) and a <b>password</b> (%d to %d
characters) and click LOGIN. The account is yours from then on; log in with the same name and password next time.</p>
<p>Remember the password: this server never sends mail, so only its operator can reset it.</p>`,
			accounts.MinName, accounts.MaxName, accounts.MinPassword, accounts.MaxPassword)
	case accounts.Invite:
		signup = `<p>This server makes new accounts with an invite code from its operator. It has no sign-up page yet:
ask the operator for an account.</p>`
	default:
		signup = `<p>Only this server's operator makes accounts here: ask them for one.</p>`
	}
	pages := map[string]struct{ title, body string }{
		sitelinks.Index: {"Dawngate server", `<p>This is a community server for Dawngate. It has no website of its own
yet; these pages say how accounts work here.</p>`},
		sitelinks.Signup: {"Sign up", signup},
		sitelinks.Password: {"Forgotten password", `<p>This server never sends mail. If you forgot your password, ask the
server's operator: they can set a new one and tell it to you. Your login name stays the same.</p>`},
		sitelinks.Terms: {"Terms", `<p>The operator of this server has published no terms of use here. Ask them which
rules apply.</p>`},
		sitelinks.Privacy: {"Privacy", `<p>The operator of this server has published no privacy notice here. What the
server software keeps: your login name, a hash of your password (never the password itself), your game data and match
history, and logs of the connections; ask the operator how long they keep them.</p>`},
	}
	var out []Builtin
	for _, page := range sitelinks.Pages {
		p := pages[page]
		out = append(out, Builtin{Path: sitelinks.FallbackPrefix + page, ContentType: "text/html; charset=utf-8",
			Body: sitePage(p.title, p.body)})
	}
	return out
}

func sitePage(title, body string) []byte {
	var nav []string
	for _, p := range []struct{ page, name string }{{sitelinks.Index, "Start"}, {sitelinks.Signup, "Sign up"},
		{sitelinks.Password, "Forgotten password"}, {sitelinks.Terms, "Terms"}, {sitelinks.Privacy, "Privacy"}} {
		nav = append(nav, fmt.Sprintf(`<a href="/%s%s">%s</a>`, sitelinks.FallbackPrefix, p.page, p.name))
	}
	return []byte("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>" + html.EscapeString(title) +
		"</title>\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n<style>body{font-family:sans-serif;" +
		"max-width:40em;margin:2em auto;padding:0 1em;line-height:1.5}nav a{margin-right:1em}</style></head>\n<body><nav>" +
		strings.Join(nav, "") + "</nav>\n<h1>" + html.EscapeString(title) + "</h1>\n" + body + "\n</body></html>\n")
}
