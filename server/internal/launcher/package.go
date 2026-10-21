package launcher

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"dawngate/server/internal/dgserver"
	"dawngate/server/internal/site"
	"dawngate/server/internal/tunnel"
)

// ClientPackageKey is Start-Server.ini's client_package: on hands out the client package (dgserver --client-package).
const ClientPackageKey = "client_package"

func init() { dgserver.ClientIni = PackageClientIni }

// PackageClientIni is the Start-Client.ini of the server's client package: this release's template (ClientConfigText,
// the one Start-Client writes) with server = the server's address, gateway_port when it isn't 42128 and
// gateway_pin = its tunnel key, in CRLF lines.
func PackageClientIni(server string, gatewayPort int, pin string) []byte {
	text := setKeyText(ClientConfigText, "server", server)
	if gatewayPort != 0 && gatewayPort != tunnel.DefaultPort {
		text = setKeyText(text, "gateway_port", strconv.Itoa(gatewayPort))
	}
	text = setKeyText(text, GatewayPinKey, pin)
	return []byte(strings.ReplaceAll(text, "\n", "\r\n"))
}

// packageBanner is the banner's line on the client package (nothing without client_package = on).
func packageBanner(w io.Writer, info dgserver.Info) {
	switch {
	case info.ClientPackage == "":
	case info.ClientPackageMissing != "":
		fmt.Fprintf(w, "  No game client to hand out (%s = on): %s.\n", ClientPackageKey, info.ClientPackageMissing)
	case info.WebURL != "":
		fmt.Fprintf(w, "  Players download the game client ready for this server (preselected, its key trusted) at %s%s\n",
			info.WebURL, site.PageDownload)
	default:
		fmt.Fprintf(w, "  The game client for this server (%s = on) is served only through the tunnel without the website "+
			"(web_port = 0): turn the website on to hand it out.\n", ClientPackageKey)
	}
}

// setKeyText sets a key's line in a config text (LF lines) as SetKey does in a file: the key's line replaced, or the
// key appended.
func setKeyText(text, key, value string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if k, _, ok := strings.Cut(t, "="); ok && t[0] != ';' && t[0] != '#' && strings.EqualFold(strings.TrimSpace(k), key) {
			lines[i] = key + " = " + value
			return strings.Join(lines, "\n")
		}
	}
	return strings.TrimRight(text, "\n") + "\n" + key + " = " + value + "\n"
}
