package launcher

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/web"
)

// FetchMaps asks a server without the tunnel which maps a custom game may pick there besides The Dawngate
// (dgserver's /dawngate/maps.txt on the HTTP port, the tunnel's FactMaps value): their tags, and whether the server
// tells them (false with no error: a server of a release before the page, which answers 404).
func FetchMaps(address string, port int, timeout time.Duration) ([]string, bool, error) {
	b, found, err := fetchPage(address, port, web.MapsPath, timeout)
	if err != nil || !found {
		return nil, false, err
	}
	tags, _ := tunnel.FactMapTags(map[string]string{tunnel.FactMaps: strings.TrimSpace(string(b))})
	return tags, true, nil
}

// fetchPage reads a page of a server's HTTP port (at most 4 KB); found is false with no error for a 404 (a server
// of a release before the page).
func fetchPage(address string, port int, path string, timeout time.Duration) (b []byte, found bool, err error) {
	cl := &http.Client{Timeout: timeout}
	url := "http://" + net.JoinHostPort(address, strconv.Itoa(port)) + "/" + path
	resp, err := cl.Get(url)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("%s: %s", url, resp.Status)
	}
	b, err = io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// FetchMapVersions asks a server without the tunnel for its versions of the maps it offers (dgserver's
// /dawngate/mapversions.txt, tunnel.MapVersionsText): by tag; nil with no error from a server of a release before
// the page (404).
func FetchMapVersions(address string, port int, timeout time.Duration) (map[string][32]byte, error) {
	b, found, err := fetchPage(address, port, web.MapVersionsPath, timeout)
	if err != nil || !found {
		return nil, err
	}
	return tunnel.ParseMapVersions(string(b)), nil
}

// WriteServerMapList writes the CREATE GAME dialog's map list of the player copy (the folder with game\) for the
// server at address (its HTTP port), as the front does at a login through the tunnel (writeMapList, both dialogMaps):
// the maps besides The Dawngate whose files the copy has (folder: the maps folder's entries, setup.DialogMaps), that
// the server offers and, where it names its version (FetchMapVersions), in that version; a server before the pages
// gets the copy's maps. It returns the log line saying what it wrote.
func WriteServerMapList(copy string, folder []moviepatch.Map, address string, port int, timeout time.Duration) (string, error) {
	tags, told, err := FetchMaps(address, port, timeout)
	if err != nil {
		return "", err
	}
	versions, verr := FetchMapVersions(address, port, timeout)
	maps, why := dialogMaps(copy, folder, serverInfo{tags: tags, told: told, versions: versions})
	if verr != nil { // the server answered its maps, so list them as a server before the versions would get them
		why += "; its map versions unread: " + verr.Error()
	}
	names := dialogNames(maps)
	changed, err := moviepatch.WriteMapList(copy, maps)
	if err != nil {
		return "", err
	}
	state := "written into " + moviepatch.MapListArchive(copy)
	if !changed {
		state = "as written before"
	}
	return fmt.Sprintf("the create dialog's maps for %s: %s (%s), %s", address, names, why, state), nil
}
