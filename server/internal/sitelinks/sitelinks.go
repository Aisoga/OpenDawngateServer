// Package sitelinks names the server's web pages that the client's login screen links to.
// The setup writes the links into a player copy's added locale package
// (setup/localepatch, re/scripts/patch_locale.py); dgserver's HTTP port serves a fallback page at each of them
// (web.SitePages) until the server has a website.
//
// A link is Base + page. Without a website, Base is http://<the address the copy is patched for>:<HTTP port>/dawngate/
// (dgserver's HTTP port, 18080); with one, https://<web domain>/, so the site serves the same page names at its root.
package sitelinks

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// The pages ("" is the start page).
const (
	Index    = ""
	Signup   = "signup"
	Password = "password"
	Terms    = "terms"
	Privacy  = "privacy"
)

// Pages are all of them, in this order.
var Pages = []string{Index, Signup, Password, Terms, Privacy}

// FallbackPrefix is where dgserver's HTTP port serves its own pages (the path below the root, with the slash).
const FallbackPrefix = "dawngate/"

// HTTPPort is dgserver's HTTP port as the setups patch it into the config packages (patch_config.py --http-port).
const HTTPPort = 18080

var domainRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+(?::[0-9]{1,5})?$`)

// CheckDomain checks a website's domain (a DNS name with at least one dot, ASCII letters, digits and '-', optionally
// :port) and returns it lower-cased; "" stays "" (no website). patch_locale.py's check_options, the same rule.
func CheckDomain(domain string) (string, error) {
	if domain == "" {
		return "", nil
	}
	if !domainRE.MatchString(domain) {
		return "", fmt.Errorf("the web domain must be a DNS name such as dawngate.example.org, not %q", domain)
	}
	if _, port, ok := strings.Cut(domain, ":"); ok {
		if n, _ := strconv.Atoi(port); n < 1 || n > 65535 {
			return "", fmt.Errorf("the web domain's port must be 1..65535, not %s", port)
		}
	}
	return strings.ToLower(domain), nil
}

// WebsiteDomain checks the domain of a website dgserver serves itself (--web-domain, Start-Server.ini's web_domain):
// CheckDomain's rule without a port (the certificate's name: Let's Encrypt reaches it on 443, so the links are
// https://<domain>/<page>) and not an IP address; blanks around it are dropped. "" stays "" (no domain).
func WebsiteDomain(domain string) (string, error) {
	d, err := CheckDomain(strings.TrimSpace(domain))
	if err == nil && (strings.Contains(d, ":") || net.ParseIP(d) != nil) {
		err = fmt.Errorf("the web domain must be a DNS name such as dawngate.example.org (no port, no IP address), not %q", d)
	}
	if err != nil {
		return "", err
	}
	return d, nil
}

// Base is where the links point: https://<domain>/ when the server has a website, else
// http://<host>:<port>/dawngate/ (dgserver's fallback pages). host must be an IPv4 address.
func Base(host string, port int, domain string) (string, error) {
	if ip := net.ParseIP(host); ip == nil || ip.To4() == nil || strings.Count(host, ".") != 3 || strings.ContainsAny(host, ":") {
		return "", fmt.Errorf("the server address must be an IPv4 address, not %q", host)
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("the HTTP port must be 1..65535, not %d", port)
	}
	d, err := CheckDomain(domain)
	if err != nil {
		return "", err
	}
	if d != "" {
		return "https://" + d + "/", nil
	}
	return fmt.Sprintf("http://%s:%d/%s", host, port, FallbackPrefix), nil
}
