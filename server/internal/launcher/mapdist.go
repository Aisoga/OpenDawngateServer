package launcher

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"dawngate/server/internal/mapdist"
)

// Start-Client.ini's keys of map delivery (MAPED-DIST): map_download (on, the default, or off) and
// map_download_max_mb (the largest package taken; empty: 512).
const (
	MapDownloadKey    = "map_download"
	MapDownloadMaxKey = "map_download_max_mb"
)

// ServeMapsKey is Start-Server.ini's map delivery switch (dgserver --serve-maps: on, the default, or off).
const ServeMapsKey = "serve_maps"

func init() {
	clientKeys = append(clientKeys, MapDownloadKey, MapDownloadMaxKey)
	serverKeys = append(serverKeys, ServeMapsKey)
}

// ServeMapsFlags are dgserver's --serve-maps for Start-Server.ini's serve_maps when it is set.
func ServeMapsFlags(cfg Config) []string { return keyFlags(cfg, []string{ServeMapsKey}) }

// mapDownloadSetting is map delivery for Start-Client's picker front: on unless the flag or Start-Client.ini says off,
// into the maps folder beside the programs, with the size cap; its lines go to out. why is the start-up line.
func mapDownloadSetting(flagValue string, ccfg Config, exeDir string, out io.Writer) (md *MapDownload, why string, err error) {
	v := strings.ToLower(strings.TrimSpace(firstNonEmpty(flagValue, ccfg[MapDownloadKey])))
	switch v {
	case "", "on", "auto":
	case "off":
		return nil, "off (map_download = off): copy a server's map packages into " + MapsDir(exeDir) + " by hand", nil
	default:
		return nil, "", fmt.Errorf("map_download %q: on or off", v)
	}
	lim := mapdist.DefaultLimits()
	if s := strings.TrimSpace(ccfg[MapDownloadMaxKey]); s != "" {
		mb, err := strconv.Atoi(s)
		if err != nil || mb < 1 || mb > 4096 {
			return nil, "", fmt.Errorf("%s %q: a number of MB, 1 to 4096", MapDownloadMaxKey, s)
		}
		lim.Total = int64(mb) << 20
	}
	if err := mapdist.Leftovers(MapsDir(exeDir)); err != nil { // an earlier run closed during a download
		fmt.Fprintf(out, "  maps     note: %v\n", err)
	}
	var mu sync.Mutex
	md = &MapDownload{Dir: MapsDir(exeDir), Limits: lim, Notify: func(line string) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(out, "  maps     %s\n", line)
	}}
	return md, fmt.Sprintf("a server's maps this copy lacks are downloaded at the login into %s (at most %s a map; "+
		"map_download = off turns it off)", md.Dir, mapdist.Bytes(lim.Total)), nil
}
