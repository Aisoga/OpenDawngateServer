package dgserver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"dawngate/server/internal/clientpkg"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/site"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/update"
	"dawngate/server/internal/version"
	"dawngate/server/internal/web"
)

// The client package (--client-package; internal/clientpkg): the Windows release of the server's own version, with a
// Start-Client.ini that names this server (--public-host), its tunnel port when it isn't 42128 and its tunnel key, at
// /dawngate/download/OpenDawngate-<name>.zip on the HTTP port and the website, with a Download page on both. Off by
// default: the release comes from GitHub (the server's second outgoing connection, after the website's ACME).

// ClientIni makes the package's Start-Client.ini from the server's address, its tunnel port and its key (the
// launcher sets it: Start-Client's own template with these keys; dgserver can't import the launcher). nil:
// clientpkg.MinimalIni.
var ClientIni func(server string, gatewayPort int, pin string) []byte

// packageVersion is the release the package is made of: this build's version (a seam for the tests, whose build is a
// developer build).
var packageVersion = version.String

// clientPackageConfig is --client-package, --client-package-name and --client-package-source.
type clientPackageConfig struct {
	on           bool
	name, source string
}

// newClientPackage makes the package (nil without --client-package). It needs the tunnel (the ini trusts its key) and
// a data folder (the package is kept there). Its lines go to *lg ("web"), which is set before startClientPackage.
func newClientPackage(cfg config, tunnelOn bool, lg **logging.Log) (*clientpkg.Package, error) {
	c := cfg.clientPackage
	if !c.on {
		return nil, nil
	}
	if !tunnelOn {
		return nil, errors.New("--client-package needs the tunnel (--tunnel on): the package's Start-Client.ini trusts the tunnel's key")
	}
	if cfg.dataDir == "" {
		return nil, errors.New("--client-package needs --data: the package is kept in <data>\\" + clientpkg.DirName)
	}
	ua := "OpenDawngateServer-dgserver/" + version.String()
	src := update.GitHub(ua)
	if c.source != "" { // the tests' fake of GitHub, on a loopback address only
		var err error
		if src, err = update.Loopback(c.source, ua); err != nil {
			return nil, fmt.Errorf("--client-package-source: %w", err)
		}
	}
	name := c.name
	if name == "" {
		name = cfg.web.domain
	}
	if name == "" {
		name = cfg.publicHost
	}
	return clientpkg.New(clientpkg.Config{Version: packageVersion(), Source: src, Dir: filepath.Join(cfg.dataDir, clientpkg.DirName),
		Name: name, Log: func(format string, a ...any) {
			if *lg != nil {
				(*lg).Logf("web", format, a...)
			}
		}}), nil
}

// webDownload is the package on the HTTP port.
func webDownload(p *clientpkg.Package) *web.Download {
	return &web.Download{Path: p.Path(), File: p.FileName(), Disposition: p.Disposition(), State: p.State,
		Open: func() (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			return p.Get(ctx)
		}}
}

// siteDownload is the package on the website.
func siteDownload(p *clientpkg.Package) *site.Download {
	return &site.Download{Path: p.Path(), File: p.FileName(), Handler: p, State: p.State}
}

// startClientPackage gives the package its ini once the gateway runs (its key, its port) and starts the first build;
// it returns the start-up line.
func startClientPackage(p *clientpkg.Package, cfg config, gw *tunnel.Gateway) string {
	pin := gw.Identity.Pin
	port := gw.Port()
	var ini []byte
	if ClientIni != nil {
		ini = ClientIni(cfg.publicHost, port, pin.String())
	} else {
		ini = clientpkg.MinimalIni(cfg.publicHost, port, tunnel.DefaultPort, pin.String())
	}
	p.Start(ini, pin.Fingerprint())
	if why := p.Unavailable(); why != "" {
		return fmt.Sprintf("client package: none (%s); its Download page says so", why)
	}
	return fmt.Sprintf("client package: %s on the HTTP port and the website (its Download page): the Windows release %s "+
		"from GitHub with a Start-Client.ini for %s (tunnel port %d, key %s), built once into %s",
		p.Path(), packageVersion(), cfg.publicHost, port, pin.Fingerprint(), filepath.Join(cfg.dataDir, clientpkg.DirName))
}
