package launcher

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/dgserver"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/sitelinks"
)

// Start-Server.ini's profile. "home" is a server on this PC or a home network; "public" is a
// server for strangers and turns on what it needs: the tunnel is required (tunnel = off is refused), the
// limits are on (dgserver --limits on, also on a LAN address), and the first start asks for the sign-up mode, the
// website's domain and an admin account. A public address always takes the public profile (profile = auto, the
// default). docs/OPERATORS.md *A public server*.

// The profiles in Start-Server.ini (profile =).
const (
	ProfileAuto   = "auto"
	ProfileHome   = "home"
	ProfilePublic = "public"
)

// profileMode checks a profile = value ("" = auto).
func profileMode(v string) (string, error) {
	switch v = strings.ToLower(strings.TrimSpace(v)); v {
	case "":
		return ProfileAuto, nil
	case ProfileAuto, ProfileHome, ProfilePublic:
		return v, nil
	}
	return "", fmt.Errorf("profile = %s: use auto, home or public", v)
}

// publicProfile says whether a server runs with the public profile: public always, auto for a public address. A
// public address with profile = home is refused (checkProfile).
func publicProfile(mode, address string) bool {
	return mode == ProfilePublic || (mode == ProfileAuto && IsPublicAddress(address))
}

// checkProfile refuses what the public profile doesn't allow: home on a public address, and a public server without
// the tunnel (by design: the tunnel is required on public servers).
func checkProfile(mode, address, tunnelMode string) error {
	if mode == ProfileHome && IsPublicAddress(address) {
		return fmt.Errorf("profile = home, but address = %s is a public internet address: a public server takes "+
			"profile = public (or auto)", address)
	}
	if publicProfile(mode, address) && tunnelMode == TunnelOff {
		return fmt.Errorf("tunnel = off: a public server needs the encrypted tunnel (profile = public); use tunnel = auto or on")
	}
	return nil
}

// LANAddress is one of this PC's addresses in a home network.
type LANAddress struct {
	IP, Interface string
	Route         bool // the address the PC's default route leaves from
}

// lanAddresses lists this PC's private IPv4 addresses (not loopback, not link-local, not shared/CGNAT such as a VPN's
// 100.64.0.0/10), the one of the default route first. Tests replace it.
var lanAddresses = func() []LANAddress {
	route := ""
	// the source address the system would use for an outside destination: a UDP "connection" sends nothing, and
	// 192.0.2.1 is a documentation address (RFC 5737) that no packet is ever meant to reach
	if c, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
			route = a.IP.String()
		}
		c.Close()
	}
	var out []LANAddress
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := n.IP.To4()
			if ip == nil || !ip.IsPrivate() {
				continue
			}
			out = append(out, LANAddress{IP: ip.String(), Interface: ifc.Name, Route: ip.String() == route})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Route && !out[j].Route })
	return out
}

// asker asks the first start's questions.
type asker struct {
	in  *bufio.Reader
	out io.Writer
}

func (a asker) ask(q string) (string, error) { return Prompt(a.in, a.out, q) }

// choose asks for one of n numbered choices (Enter = def); three tries.
func (a asker) choose(q string, n, def int) (int, error) {
	for try := 0; try < 3; try++ {
		ans, err := a.ask(q)
		if err != nil {
			return 0, err
		}
		if ans == "" {
			return def, nil
		}
		var k int
		if _, err := fmt.Sscanf(ans, "%d", &k); err == nil && k >= 1 && k <= n && fmt.Sprint(k) == ans {
			return k, nil
		}
		fmt.Fprintf(a.out, "  Please type a number from 1 to %d.\n", n)
	}
	return 0, fmt.Errorf("no valid answer")
}

// pickLAN asks for one of this PC's home-network addresses (a number of the list, or an address typed).
func (a asker) pickLAN(q string) (string, error) {
	list := lanAddresses()
	if len(list) == 0 {
		fmt.Fprintln(a.out, "  This PC has no address in a home network (ipconfig shows it as IPv4 Address, e.g. 192.168.1.20).")
	}
	for i, l := range list {
		note := ""
		if l.Route {
			note = "  (the one your router connects)"
		}
		fmt.Fprintf(a.out, "    %d  %s (%s)%s\n", i+1, l.IP, l.Interface, note)
	}
	for try := 0; try < 3; try++ {
		prompt := q + " (type the address"
		if len(list) > 0 {
			prompt += ", or its number; Enter = 1"
		}
		ans, err := a.ask(prompt + "): ")
		if err != nil {
			return "", err
		}
		if ans == "" && len(list) > 0 {
			return list[0].IP, nil
		}
		var k int
		if _, err := fmt.Sscanf(ans, "%d", &k); err == nil && fmt.Sprint(k) == ans && k >= 1 && k <= len(list) {
			return list[k-1].IP, nil
		}
		if ip := net.ParseIP(ans).To4(); ip != nil && ip.IsPrivate() {
			if err := thisMachine(ip.String()); err == nil {
				return ip.String(), nil
			}
			fmt.Fprintf(a.out, "  %s is not an address of this PC.\n", ans)
			continue
		}
		fmt.Fprintln(a.out, "  Please type one of this PC's home-network addresses (e.g. 192.168.1.20) or its number.")
	}
	return "", fmt.Errorf("no home-network address chosen")
}

// AskServerProfile is the first start's dialog (and Start-Server.exe --configure's): who will play on the server.
// It writes the answers into the config file at path and returns what it chose (home or public). A home server needs
// at most the address; a public one also gets the sign-up mode, the website's domain, an admin account (made in the
// data folder) and the acknowledgement of the risk notice.
func AskServerProfile(in *bufio.Reader, out io.Writer, path, dataDir string) (string, error) {
	a := asker{in: in, out: out}
	set := func(k, v string) error { return SetKey(path, k, v) }
	fmt.Fprintln(out, "\nWho will play on this server? (You can change it later in "+ServerConfigName+", or run Start-Server.exe --configure.)")
	fmt.Fprintln(out, "    1  only this PC: you play here with Start-Client.exe")
	fmt.Fprintln(out, "    2  your home network: also players on the other PCs at home")
	fmt.Fprintln(out, "    3  the internet: a public server for players anywhere (at your own risk)")
	k, err := a.choose("  Your choice (Enter = 1): ", 3, 1)
	if err != nil {
		return "", err
	}
	switch k {
	case 1:
		if err := firstErr(set("profile", ProfileAuto), set("address", setup.Local), set("bind", "")); err != nil {
			return "", err
		}
		fmt.Fprintln(out, "  This PC only: address = 127.0.0.1.")
		return ProfileHome, nil
	case 2:
		fmt.Fprintln(out, "\n  This PC's addresses in your home network:")
		ip, err := a.pickLAN("  Which one do the players use?")
		if err != nil {
			return "", err
		}
		if err := firstErr(set("profile", ProfileAuto), set("address", ip), set("bind", "")); err != nil {
			return "", err
		}
		fmt.Fprintf(out, "  Home network: address = %s. Players start Start-Client.exe %s.\n", ip, ip)
		return ProfileHome, nil
	}

	// 3: the public profile
	fmt.Fprintln(out, "\n  A public server: players connect through the encrypted tunnel, limits are on, and you choose who may make accounts.")
	var public string
	for try := 0; try < 3 && public == ""; try++ {
		ans, err := a.ask("  Your internet address (the IPv4 address your router shows as its internet or WAN address, e.g. 203.0.113.7): ")
		if err != nil {
			return "", err
		}
		if ip := net.ParseIP(ans).To4(); ip != nil && IsPublicAddress(ip.String()) {
			public = ip.String()
		} else {
			fmt.Fprintln(out, "  That is no public internet address (not 127.x, 10.x, 172.16-31.x, 192.168.x, 100.64-127.x or 169.254.x).")
		}
	}
	if public == "" {
		return "", fmt.Errorf("no public address given")
	}
	bind := ""
	if thisMachine(public) != nil { // behind a router: listen on the LAN address the router forwards to
		fmt.Fprintln(out, "\n  This PC is behind a router. Its addresses in your home network:")
		if bind, err = a.pickLAN("  Which one does your router forward the ports to?"); err != nil {
			return "", err
		}
	}
	fmt.Fprintln(out, "\n  Who may make accounts?")
	fmt.Fprintln(out, "    1  invite: players register on the server's website with an invite code you make (admin page, or")
	fmt.Fprintln(out, "       Start-Server.exe --make-invite)")
	fmt.Fprintln(out, "    2  closed: only you make accounts (Start-Server.exe --create-account <login name> prints the password)")
	fmt.Fprintln(out, "    3  open: anyone who reaches the server makes one with the first login (at most 3 a day per address)")
	k, err = a.choose("  Your choice (Enter = 1): ", 3, 1)
	if err != nil {
		return "", err
	}
	signup := []accounts.Mode{accounts.Invite, accounts.Closed, accounts.Open}[k-1].String()

	fmt.Fprintln(out, "\n  The server's website (registration, password changes, account deletion, the admin page) runs on HTTPS,")
	fmt.Fprintln(out, "  port 443. With a domain name whose DNS A record points at "+public+", the server gets a free certificate")
	fmt.Fprintln(out, "  from Let's Encrypt by itself; without one, browsers warn once about its self-signed certificate.")
	domain := ""
	for try := 0; try < 3; try++ {
		ans, err := a.ask("  Your domain name for it (e.g. play.example.org), or Enter for none: ")
		if err != nil {
			return "", err
		}
		if ans == "" {
			break
		}
		d, err := sitelinks.WebsiteDomain(ans)
		if err == nil && d != "" {
			domain = d
			break
		}
		fmt.Fprintln(out, "  Please type a domain name such as play.example.org (letters, digits, dots and dashes), or press Enter.")
	}
	email := ""
	if domain != "" {
		if email, err = a.ask("  An e-mail address Let's Encrypt may write to about the certificate (optional, Enter for none): "); err != nil {
			return "", err
		}
	}
	if err := firstErr(set("profile", ProfilePublic), set("address", public), set("bind", bind), set("tunnel", TunnelAuto),
		set("signup", signup), set("web_domain", domain), set("web_email", email)); err != nil {
		return "", err
	}

	fmt.Fprintln(out, "\n  The admin page (https://<server>/admin) lists the accounts, invite codes and matches. Make an account")
	admin, err := a.ask("  with the admin role now? Its login name (3-32 of A-Z a-z 0-9 . _ - @), or Enter to skip: ")
	if err != nil {
		return "", err
	}
	if admin != "" {
		// the operator's account commands: the password is printed once
		if code := dgserver.Main([]string{"--data", dataDir, "--create-account", admin}); code != 0 {
			fmt.Fprintln(out, "  (the account wasn't made; if it exists already, it gets the admin role now)")
		}
		if code := dgserver.Main([]string{"--data", dataDir, "--make-admin", admin}); code != 0 {
			fmt.Fprintf(out, "  The admin role wasn't given; later: Start-Server.exe --make-admin %s\n", admin)
		}
	}

	ServerRiskNotice(out, RiskInfo{Address: public, Bind: bind, GatewayPort: 0, Signup: signup, WebDomain: domain})
	ans, err := a.ask("  Do you accept this risk? Type yes to start the server on " + public + ": ")
	if err != nil {
		return "", err
	}
	accepted := strings.EqualFold(ans, "yes") || strings.EqualFold(ans, "y")
	if err := set(RiskAckKey, map[bool]string{true: "yes", false: "no"}[accepted]); err != nil {
		return "", err
	}
	fmt.Fprintf(out, "\n  Saved in %s: profile = public, address = %s", path, public)
	if bind != "" {
		fmt.Fprintf(out, ", bind = %s", bind)
	}
	fmt.Fprintf(out, ", signup = %s, web_domain = %s, i_accept_the_risk = %s.\n", signup, orNone(domain), map[bool]string{true: "yes", false: "no"}[accepted])
	return ProfilePublic, nil
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
