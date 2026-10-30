package launcher

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dawngate/server/internal/dgserver"
	"dawngate/server/internal/mapdist"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/mapc"
)

// dgserver --tunnel-client <gateway> --tunnel-front <folder> (a test aid for the lanes, which run the game from their
// own copies and dgserver rather than Start-Client): the front as Start-Client's picker mode runs it, its server list
// and trusted keys in <folder> (Dawngate-Data's layout: servers\bookmarks.json, servers\<address>\gateway.pin), the
// --tunnel-client gateway in the list (added when missing, its key trusted) and preselected, the login screen's list
// written into --front-copy.
func init() { dgserver.RunFront = runLaneFront }

func runLaneFront(c dgserver.FrontConfig) (func(), []int, error) {
	l := setup.Layout{Root: c.Folder}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		return nil, nil, err
	}
	host, port, err := net.SplitHostPort(c.Gateway)
	if err != nil {
		return nil, nil, fmt.Errorf("--tunnel-client %s: %v", c.Gateway, err)
	}
	gwPort, _ := strconv.Atoi(port)
	var first Bookmark
	found := false
	for _, b := range bm.List() {
		if strings.EqualFold(b.Address, host) {
			first, found = b, true
		}
	}
	if !found {
		if first, err = bm.Add(Bookmark{Address: host, GatewayPort: gwPort}); err != nil {
			return nil, nil, err
		}
	}
	pin := TrustedGateway(l, host)
	if _, err := os.Stat(pin); err != nil {
		if err := os.MkdirAll(filepath.Dir(pin), 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(pin, []byte(c.Pin.String()+"\n"), 0o644); err != nil {
			return nil, nil, err
		}
	}
	f := &Front{Log: c.Log, Layout: l, Bookmarks: bm, TunnelIP: net.ParseIP(c.TunnelAddress), RedirectorTLS: c.RedirectorTLS, Default: first.Tag}
	if c.MapsDir != "" { // --maps: the folder's maps, as Start-Client's maps folder (the dialog's list, the hello's maps)
		pkgs, _ := mapc.ReadFolder(c.MapsDir)
		f.Maps = setup.DialogMaps(pkgs)
	}
	if c.MapDownload != "" { // map delivery into a scratch folder (the lanes' --map-download)
		f.MapDownload = &MapDownload{Dir: c.MapDownload, Limits: mapdist.DefaultLimits()}
		f.logf("map delivery: on, into %s", c.MapDownload)
	}
	if err := f.Start(); err != nil {
		return nil, nil, err
	}
	if c.Copy != "" {
		if err := f.WriteServerList(c.Copy); err != nil {
			f.Close()
			return nil, nil, err
		}
	}
	f.logf("the front for a test lane: %d server(s) in %s, %s preselected", len(bm.List()), BookmarksPath(l), first.Tag)
	p := f.Ports
	return f.Close, []int{p.Redirector, p.Blaze, p.QoS, p.HTTP, p.Telemetry}, nil
}
