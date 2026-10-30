package launcher

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dawngate/server/internal/logging"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/tunnel"
)

// Start-Client's picker mode: one player copy for every tunnelled server, the server picked at the login through
// the front (front.go) from the bookmarks (bookmarks.go). picker = auto (the default) turns it on whenever the
// tunnel is used, which is for every server unless tunnel = off; a server without the tunnel (tunnel = off, the
// developers' option) is played on directly, with one copy patched for it, as before the picker.

// The picker's settings in Start-Client.ini (picker =) and --picker.
const (
	PickerAuto = "auto"
	PickerOn   = "on"
	PickerOff  = "off"
)

// pickerSetting checks a picker = value ("" = auto).
func pickerSetting(v string) (string, error) {
	switch v = strings.ToLower(strings.TrimSpace(v)); v {
	case "":
		return PickerAuto, nil
	case PickerAuto, PickerOn, PickerOff:
		return v, nil
	case "yes", "true":
		return PickerOn, nil
	case "no", "false":
		return PickerOff, nil
	}
	return "", fmt.Errorf("picker = %s: use auto, on or off", v)
}

// serverChoice is what Start-Client plays on: one server (picker false: server) or the picker's list (picker true:
// bm, with the servers in add put into it when missing; server is the one to preselect, "" for none).
type serverChoice struct {
	picker bool
	server string
	bm     *Bookmarks
	add    []listAdd
	ini    Config // Start-Client.ini when its server is in add (onlyFirst): its gateway_pin
}

// listAdd is a server Start-Client puts into the picker's list when it isn't there yet.
type listAdd struct {
	b         Bookmark
	why       string
	preselect bool // the server Start-Client was started for: a login without a pick goes there
	onlyFirst bool // only into an empty list (Start-Client.ini's server, migrated)
}

// chooseServer decides between the picker and one server. With the picker (picker = auto or on and the tunnel, the
// default) the list always has This PC first (thispc.go), so with no server named Start-Client goes to the login
// screen with the list, This PC preselected until a login used another one; a server named (the address or tag given,
// Start-Client.ini's server) is added and preselected, and a Start-Server.ini next to Start-Client with another setup
// folder adds its address. Without the picker (tunnel = off, picker = off) the one server comes from the address or
// tag given, Start-Client.ini's server, this PC's Start-Server, the player's answer, 127.0.0.1, and Start-Client says
// that the login screen shows no server list, and why.
func chooseServer(o IO, in *bufio.Reader, clientIni string, l setup.Layout, ccfg, scfg Config, named, pickerSet, mode string, gwPort int) (serverChoice, error) {
	var c serverChoice
	canPick := pickerSet != PickerOff && mode != TunnelOff
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		if canPick {
			return c, err
		}
		fmt.Fprintf(o.Out, "  note: %v\n", err)
		bm = nil
	}
	listed := func(s string) (Bookmark, bool) {
		if bm == nil {
			return Bookmark{}, false
		}
		return bm.Find(s)
	}
	inList := 0
	if bm != nil {
		inList = len(bm.List())
	}
	wants := func(server string) bool {
		_, ok := listed(server)
		return canPick && (pickerSet == PickerOn || tunnelFor(mode, server) || ok)
	}
	pick := func(server, why string, preselect bool) {
		c.picker, c.bm, c.server = true, bm, ""
		if preselect {
			c.server = server
		}
		if server != "" {
			c.add = append(c.add, listAdd{b: ownBookmark(server, gwPort, scfg), why: why, preselect: preselect})
		}
	}
	source := ""
	switch {
	case named != "":
		if b, ok := listed(named); ok && canPick { // a tag, address or name of the list
			named = b.Address
		}
		source, c.server = "given", named
		if wants(named) {
			if err := ValidServerAddress(named); err != nil {
				return c, err
			}
			pick(named, "the address given", true)
		}
	case ccfg["server"] != "":
		source, c.server = "ini", ccfg["server"]
		if wants(c.server) {
			pick("", "", false) // the list decides; the ini's server is the first server of a new list (below)
		}
	case canPick && scfg != nil && scfg["address"] != "" && !sameFolder(scfg, o.ExeDir, l):
		// a Start-Server.ini beside this program with a setup folder of its own: its server is one more of the list
		// (This PC is the server of Start-Client's folder)
		source, c.server = "own", scfg["address"]
		pick(c.server, "the Start-Server.ini beside this program", false)
	case canPick:
		pick("", "", false) // the list, This PC first: nothing to ask
	case scfg != nil && scfg["address"] != "":
		source, c.server = "own", scfg["address"]
	case o.Interactive:
		q := "\nWhich server do you want to play on? Type its address (e.g. 192.168.1.20), or just press Enter for a\n" +
			"server on this PC: "
		answer, err := Prompt(in, o.Out, q)
		if err != nil {
			return c, err
		}
		source, c.server = "answer", answer
		if answer == "" {
			c.server = setup.Local
		} else if wants(answer) {
			if err := ValidServerAddress(answer); err != nil {
				return c, err
			}
			pick(answer, "your answer", false)
		} else if err := setClientKey(clientIni, "server", answer); err == nil {
			fmt.Fprintf(o.Out, "  saved in %s (edit or delete it to play on another server)\n", clientIni)
		}
	default:
		source, c.server = "local", setup.Local
		if wants(c.server) {
			pick(c.server, "this PC", false)
		}
	}
	if c.picker {
		if iniServer := ccfg["server"]; iniServer != "" && ValidServerAddress(iniServer) == nil {
			c.add = append([]listAdd{{b: ownBookmark(iniServer, gwPort, scfg), why: "Start-Client.ini's server", onlyFirst: true}}, c.add...)
			c.ini = ccfg
		}
		return c, nil
	}
	if pickerSet == PickerOff {
		return c, nil
	}
	why := "the tunnel is off for it: reached directly"
	switch {
	case mode == TunnelOff:
		why = "tunnel = off: the picker needs the tunnel"
	case source == "ini":
		why += "; to pick from your list instead, empty server = in " + ClientConfigName
	}
	list := ""
	if inList > 0 {
		list = fmt.Sprintf(" (your list's %d server(s) aren't shown)", inList)
	}
	fmt.Fprintf(o.Out, "  picker   not used for %s (%s): the game plays on this one server, and the login screen shows no "+
		"server list%s\n", c.server, why, list)
	return c, nil
}

// ownBookmark is the list entry for a server address: when this PC's own Start-Server serves that address from
// another one (a public address behind a router, bind =), the front dials the bind address and the list shows the
// public one as the server's name (a router may not take its own public address from inside; gatewayAddress).
func ownBookmark(address string, gwPort int, scfg Config) Bookmark {
	b := Bookmark{Address: address, GatewayPort: gwPort}
	if scfg != nil && scfg["address"] == address && scfg["bind"] != "" && scfg["bind"] != address {
		b.Address, b.Name = scfg["bind"], address
	}
	return b
}

// pickerBookmarks puts the choice's servers into the list when they are missing: Start-Client.ini's server (the
// first server of a new list), this PC's Start-Server or the player's answer, and the address given to Start-Client
// (preselected: a login without a pick goes there; without one, the ini's server while no server of the list has
// been used yet).
// --gateway-pin with that address stores the operator's key as its trusted one; Start-Client.ini's gateway_pin does
// so for the ini's server when no other key is trusted for it (trustIniGatewayPin). It returns the tag to preselect;
// check changes nothing.
func pickerBookmarks(o IO, l setup.Layout, c serverChoice, pinFlag string, check bool, pc *thisPC) (string, error) {
	bm := c.bm
	if pinFlag != "" && c.server == "" {
		return "", errors.New("--gateway-pin needs the server's address (Start-Client.exe <address> --gateway-pin <key>)")
	}
	add := func(want Bookmark, why string) (Bookmark, error) {
		if b, ok := bm.Find(want.Address); ok && strings.EqualFold(b.Address, want.Address) {
			return b, nil
		}
		if check {
			return want, nil
		}
		b, err := bm.Add(want)
		if err != nil {
			return b, err
		}
		fmt.Fprintf(o.Out, "  picker   %s added to the server list as %s (%s)\n", b.Gateway(), b.Tag, why)
		return b, nil
	}
	empty := true // no server of the player's yet (This PC doesn't count)
	for _, b := range bm.List() {
		empty = empty && b.Tag == ThisPCTag
	}
	preselect, iniAddr := "", ""
	for _, a := range c.add {
		if a.onlyFirst {
			iniAddr = a.b.Address
		}
		if a.onlyFirst && c.ini[GatewayPinKey] != "" { // the operator's key for the ini's server (a handed-out package)
			if err := trustIniGatewayPin(o, l, c.ini, a.b.Address, check); err != nil {
				return "", err
			}
		}
		if a.onlyFirst && !empty {
			continue
		}
		b, err := add(a.b, a.why)
		if err != nil {
			return "", err
		}
		if !a.preselect {
			continue
		}
		preselect = b.Tag
		if pinFlag != "" && !check {
			p, err := tunnel.ParsePin(pinFlag)
			if err != nil {
				return "", err
			}
			store := TrustedGateway(l, b.Address)
			if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(store, []byte(p.String()+"\n"), 0o644); err != nil {
				return "", err
			}
			fmt.Fprintf(o.Out, "  picker   %s's tunnel key %s trusted (--gateway-pin)\n", b.Address, p.Fingerprint())
		}
	}
	if last, ok := bm.Last(); preselect == "" && iniAddr != "" && (!ok || last.LastUsed == "") {
		// the ini's server until the first login used a server of the list (a handed-out package: its server, also
		// when the first start ended before a login), then the list's last one as for every list
		if b, ok := bm.Find(iniAddr); ok && strings.EqualFold(b.Address, iniAddr) {
			preselect = b.Tag
		}
	}
	list := bm.List()
	if len(list) == 0 {
		fmt.Fprintf(o.Out, "  servers  none in the list yet (%s): add one on the login screen (SERVER, then ADD SERVER), "+
			"or start Start-Client.exe <address>\n", BookmarksPath(l))
		return preselect, nil
	}
	fmt.Fprintf(o.Out, "  servers  %d in the list (%s):\n", len(list), BookmarksPath(l))
	for _, b := range list {
		if b.Tag == ThisPCTag && pc != nil {
			fmt.Fprintf(o.Out, "           #%-6s %s\n", b.Tag, pc.describe())
			continue
		}
		name := ""
		if b.Name != b.Address {
			name = " " + b.Name
		}
		fmt.Fprintf(o.Out, "           #%-6s %s%s\n", b.Tag, b.Gateway(), name)
	}
	return preselect, nil
}

// ownServerKey is the gateway key of this PC's Start-Server when it shares this Start-Client's setup folder
// (Start-Server.ini next to it, the same Dawngate-Data): its pin and the addresses a list entry for it may have
// (address and bind). ok is false without such a server or before its first start made the key. Its key is known
// without asking: it is in the folder the player set up.
func ownServerKey(scfg Config, exeDir string, l setup.Layout) (addrs []string, pin tunnel.Pin, ok bool) {
	if !sameFolder(scfg, exeDir, l) {
		return nil, pin, false
	}
	address := firstNonEmpty(scfg["address"], setup.Local)
	p, err := tunnel.PinFromCertFile(filepath.Join(l.Certs(address), tunnel.CertFile))
	if err != nil {
		return nil, pin, false
	}
	addrs = []string{address}
	if b := scfg["bind"]; b != "" && b != address {
		addrs = append(addrs, b)
	}
	return addrs, p, true
}

// sameFolder says whether a Start-Server.ini (scfg, next to the program in exeDir) uses the setup folder l.
func sameFolder(scfg Config, exeDir string, l setup.Layout) bool {
	if scfg == nil {
		return false
	}
	folder := firstNonEmpty(scfg["folder"], DefaultFolder)
	return strings.EqualFold(filepath.Clean(Resolve(exeDir, folder)), filepath.Clean(l.Root))
}

// pinOwnServer trusts this PC's Start-Server's key (ownServerKey) for its entries in the list: stored when missing,
// replaced when the server was set up anew (its folder's key is the one it presents). check changes nothing.
func pinOwnServer(o IO, l setup.Layout, bm *Bookmarks, addrs []string, pin tunnel.Pin, check bool) error {
	for _, b := range bm.List() {
		own := false
		for _, a := range addrs {
			own = own || strings.EqualFold(a, b.Address)
		}
		if !own {
			continue
		}
		store := TrustedGateway(l, b.Address)
		if raw, err := os.ReadFile(store); err == nil {
			if known, err := tunnel.ParsePin(strings.TrimSpace(string(raw))); err == nil && tunnel.SamePin(known, pin) {
				continue
			}
		}
		if check {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(store, []byte(pin.String()+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(o.Out, "  picker   #%s is this PC's Start-Server: its key %s trusted (the same folder)\n", b.Tag, pin.Fingerprint())
	}
	return nil
}

// startFront runs the front for the game (its log in Dawngate-Data\logs\front-<time>.log) and writes the login
// screen's server list into the player copy.
func startFront(o IO, l setup.Layout, bm *Bookmarks, tunnelIP string, tlsCfg *tls.Config, preselect string, pc *thisPC,
	tracker *presenceTracker, dc discordConfig, md *MapDownload) (func(), *logging.Log, *Front, error) {
	var log *logging.Log
	var f *os.File
	path := ""
	if err := os.MkdirAll(l.Logs(), 0o755); err == nil {
		path = filepath.Join(l.Logs(), "front-"+time.Now().Format("20060102-150405")+".log")
		if f, err = os.Create(path); err == nil {
			log = logging.New(f)
		}
	}
	front := &Front{Log: log, Layout: l, Bookmarks: bm, TunnelIP: net.ParseIP(tunnelIP), RedirectorTLS: tlsCfg, Default: preselect,
		ThisPC: pc, Presence: tracker, DiscordJoin: dc.join && tracker != nil, JoinLoopback: dc.hook != "",
		Maps: folderDialogMaps(o.ExeDir), MapDownload: md}
	if err := front.Start(); err != nil {
		if f != nil {
			f.Close()
		}
		return nil, nil, nil, err
	}
	// the login screen's list, with each server's status now (the movie reads it at the game's start)
	if err := front.WriteServerList(l.ClientCopy()); err != nil {
		fmt.Fprintf(o.Out, "  picker   WARNING: %v (the login screen shows no list; log in as <name>#<tag>)\n", err)
	}
	def := preselect
	if def == "" {
		if b, ok := bm.Last(); ok {
			def = b.Tag
		}
	}
	fmt.Fprintf(o.Out, "  picker   pick the server on the login screen with SERVER, above the login (preselected: %s); log %s\n",
		firstNonEmpty(def, "none"), path)
	return func() {
		front.Close()
		if pc != nil {
			pc.Close() // Start-Client's own server for This PC ends with it, its match hosts with it
		}
		if f != nil {
			f.Close()
		}
	}, log, front, nil
}
