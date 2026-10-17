package site

import (
	"net/http"
	"net/netip"
	"strings"
)

// Behind a reverse proxy (Config.Proxy): the proxy (nginx, for example) holds the domain's certificate, serves
// https://<domain>/ on its own port 443 and forwards each request over plain HTTP to the site on a loopback address.
// Every TCP connection then comes from the proxy, so the site takes the player's address from the headers the proxy
// sets, and only from a loopback peer (anyone else could write them):
//
//   - X-Forwarded-For: its last entry, the one the proxy appended (nginx: $proxy_add_x_forwarded_for). The entries
//     before it are what the browser sent and are never trusted.
//   - X-Real-IP (nginx: $remote_addr) when X-Forwarded-For is missing or its last entry is no address.
//   - X-Forwarded-Proto: "http" means the proxy forwarded a plain-HTTP request; it is redirected to the public
//     https:// address, so the Secure cookies always travel over HTTPS. Without the header the request counts as
//     HTTPS (the proxy terminated it).
//
// One proxy in front of the site is the supported setup; behind a further proxy (a CDN) the address would be that
// proxy's.

// forwardedAddr is the player's address as the proxy names it: X-Forwarded-For's last entry, else X-Real-IP.
func forwardedAddr(h http.Header) (netip.Addr, bool) {
	if vals := h.Values("X-Forwarded-For"); len(vals) > 0 {
		entries := strings.Split(vals[len(vals)-1], ",") // several header lines: the proxy's is the last
		if a, ok := parseForwarded(entries[len(entries)-1]); ok {
			return a, true
		}
	}
	if v := h.Get("X-Real-IP"); v != "" {
		return parseForwarded(v)
	}
	return netip.Addr{}, false
}

// parseForwarded reads one address of a forwarding header: an IP, optionally with a port ("192.0.2.7:51000",
// "[2001:db8::7]:51000") or in brackets.
func parseForwarded(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if a, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")); err == nil && a.Zone() == "" {
		return a.Unmap(), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil && ap.Addr().Zone() == "" {
		return ap.Addr().Unmap(), true
	}
	return netip.Addr{}, false
}

// peerAddr is the TCP peer's address (the invalid Addr if unknown).
func peerAddr(r *http.Request) netip.Addr {
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return ap.Addr().Unmap()
	}
	a, _ := netip.ParseAddr(r.RemoteAddr)
	return a.Unmap()
}

// fromProxy takes a request apart as Config.Proxy says: a loopback peer's forwarded address replaces the peer's
// (in a shallow copy of the request, which every later step reads with clientAddr), and https is whether the
// request reached the proxy over HTTPS. Without Config.Proxy, or from another peer, the headers are ignored.
func (s *Site) fromProxy(r *http.Request) (_ *http.Request, https bool) {
	if !s.cfg.Proxy || !peerAddr(r).IsLoopback() {
		return r, true
	}
	if a, ok := forwardedAddr(r.Header); ok {
		r = r.WithContext(r.Context()) // a shallow copy: the server's request stays as it came
		r.RemoteAddr = netip.AddrPortFrom(a, 0).String()
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return r, !strings.EqualFold(strings.TrimSpace(proto), "http")
}

// toHTTPS sends a request that came to the proxy over plain HTTP to the same path at the public address.
func (s *Site) toHTTPS(w http.ResponseWriter, r *http.Request) {
	to := strings.TrimSuffix(s.cfg.PublicURL, "/") + r.URL.RequestURI()
	http.Redirect(w, r, to, http.StatusPermanentRedirect)
}

// publicHost is the host of Config.PublicURL ("" without one): behind a proxy the address the browser's Origin names.
func (s *Site) publicHost() string {
	h := strings.TrimPrefix(s.cfg.PublicURL, "https://")
	h, _, _ = strings.Cut(h, "/")
	return h
}
