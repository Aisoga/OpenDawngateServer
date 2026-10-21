package discordhook

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// webhookHosts are the hosts a webhook URL may name: Discord's "Copy Webhook URL" gives discord.com, older ones
// discordapp.com.
var webhookHosts = map[string]bool{"discord.com": true, "discordapp.com": true}

// CheckURL accepts a Discord webhook URL, https://discord.com/api/webhooks/<id>/<token> (or discordapp.com, also
// with an API version such as /api/v10/webhooks/...), and refuses anything else. With allowLocal (a test aid) an
// http:// URL on a loopback IPv4 address (127.x.x.x, any port) with the same path is also accepted: a local fake
// receiver. The error never contains the URL's token.
func CheckURL(raw string, allowLocal bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return errors.New("not a URL")
	}
	host := u.Hostname()
	switch {
	case u.Scheme == "https" && webhookHosts[strings.ToLower(host)] && u.Port() == "":
	case allowLocal && u.Scheme == "http" && isLoopback4(host):
	case allowLocal:
		return fmt.Errorf("%s://%s is neither a Discord webhook (https://discord.com/api/webhooks/...) nor a local test receiver (http://127.0.0.1:<port>/api/webhooks/...)", u.Scheme, u.Host)
	default:
		return fmt.Errorf("%s://%s is not a Discord webhook address (https://discord.com/api/webhooks/<id>/<token>)", u.Scheme, u.Host)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return errors.New("a webhook address has no user, query or fragment (copy it again from Discord)")
	}
	if _, _, ok := splitPath(u.Path); !ok {
		return errors.New("the path is not /api/webhooks/<id>/<token> (copy the address again from Discord)")
	}
	return nil
}

// isLoopback4 reports whether host is a literal loopback IPv4 address (no name: a test receiver needs no DNS).
func isLoopback4(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() != nil && ip.IsLoopback()
}

// splitPath splits /api[/v<n>]/webhooks/<id>/<token> into the webhook's id (digits) and token.
func splitPath(p string) (id, token string, ok bool) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) == 5 && strings.HasPrefix(parts[1], "v") && isDigits(parts[1][1:]) {
		parts = append(parts[:1], parts[2:]...)
	}
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "webhooks" || !isDigits(parts[2]) || len(parts[2]) > 20 {
		return "", "", false
	}
	token = parts[3]
	if token == "" || len(token) > 200 || strings.ContainsAny(token, "?#%") {
		return "", "", false
	}
	return parts[2], token, true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Mask describes a webhook URL without its secret for the log: "webhook id …1234" (the last four digits of the
// webhook's id; the token is never shown), with "local test receiver 127.0.0.1:8099, " in front for a local one.
func Mask(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "an unreadable address"
	}
	id, _, ok := splitPath(u.Path)
	if !ok {
		return "an unreadable address"
	}
	if len(id) > 4 {
		id = "…" + id[len(id)-4:]
	}
	if u.Scheme == "http" {
		return "local test receiver " + u.Host + ", webhook id " + id
	}
	return "webhook id " + id
}

// cleanErr strips the request URL (which holds the token) from an HTTP client error: a *url.Error's text is
// `Post "<url>": <cause>`, so only the cause is kept.
func cleanErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return fmt.Errorf("timed out: %w", ue.Err)
		}
		return ue.Err
	}
	return err
}
