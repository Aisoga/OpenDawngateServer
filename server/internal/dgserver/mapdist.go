package dgserver

import (
	"flag"
	"fmt"
	"strings"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/mapdist"
)

// mapServe is map delivery on the server's side (MAPED-DIST): the maps folder's packages of the maps it offers, handed
// to the players' Start-Clients on the HTTP port (through the tunnel; web.MapPackagesPath).
type mapServe struct {
	mode   string // --serve-maps: on (the default) or off
	maxMB  int    // --serve-maps-max-mb
	served *mapdist.Served
	notes  []string
	// download is --map-download (with --tunnel-front): the folder the front's map delivery writes into
	download string
}

func (m *mapServe) flags(fs *flag.FlagSet) {
	fs.StringVar(&m.mode, "serve-maps", "on", "map delivery: on hands the --maps packages of the maps this server offers "+
		"(--map, --custom-maps) to the players' Start-Clients that lack them (through the tunnel, checked there against "+
		"the host copy's version); off hands out none (players then copy the packages by hand)")
	fs.IntVar(&m.maxMB, "serve-maps-max-mb", 512, "map delivery: the largest map package handed out, in MB (a bigger one "+
		"isn't; Start-Client takes at most 512 MB by default)")
	fs.StringVar(&m.download, "map-download", "", "--tunnel-front test aid: map delivery into this folder (a scratch "+
		"maps folder, never the checkout's maps): at each login the front fetches the server's maps the --front-copy "+
		"lacks, as Start-Client does into its maps folder; default off")
}

// start decides what the server hands out (after mapVersions: only maps with a host-copy version, which the receiver
// must compile to). The error is a bad flag value.
func (m *mapServe) start(cfg *config) error {
	switch strings.ToLower(strings.TrimSpace(m.mode)) {
	case "on", "":
	case "off":
		if cfg.mapsDir != "" {
			m.notes = []string{"map delivery: off (--serve-maps off); players copy the maps folder's packages by hand"}
		}
		return nil
	default:
		return fmt.Errorf("--serve-maps %q: on or off", m.mode)
	}
	if m.maxMB < 1 || m.maxMB > 4096 {
		return fmt.Errorf("--serve-maps-max-mb %d: 1 to 4096", m.maxMB)
	}
	if cfg.mapsDir == "" {
		return nil
	}
	m.served, m.notes = mapdist.NewServed(cfg.mapsDir, mapOffers(cfg.level, cfg.customLevels, cfg.mapVersions),
		m.limits())
	return nil
}

func (m *mapServe) limits() mapdist.Limits {
	l := mapdist.DefaultLimits()
	l.Total = int64(m.maxMB) << 20
	return l
}

// mapOffers are the offered maps (blaze.OfferedLevels of --map and --custom-maps) whose archive has the host copy's
// version: the ones a player's Start-Client may install from this server.
func mapOffers(level string, custom []string, versions map[string][32]byte) []mapdist.Offer {
	var out []mapdist.Offer
	for _, l := range blaze.OfferedLevels(level, custom) {
		if _, ok := versions[strings.ToLower(l.Archive)]; ok && l.Archive != "" {
			out = append(out, mapdist.Offer{Name: l.Name, Tag: l.Tag})
		}
	}
	return out
}
