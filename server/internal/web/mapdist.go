package web

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/logging"
)

// MapPackagesPath is where dgserver answers the manifest of the map packages it hands out (map delivery): a line
// "<tag> <name> <size> <sha256>" per map. A server that hands out none, or has map delivery off, answers 404 there
// (as a release before it does). MapPackagePrefix + a tag is one package's bundle (mapdist's format). Start-Client
// asks both through the tunnel only, so the bytes come from the gateway whose key the player confirmed.
const (
	MapPackagesPath  = "dawngate/mappkgs.txt"
	MapPackagePrefix = "dawngate/mappkg/"
)

// MapDownloads is how many map packages the HTTP port sends at once; a further request gets 503 (the client tries
// again at its next login).
const MapDownloads = 4

// MapPackages is what the HTTP port hands out (dgserver's mapdist.Served).
type MapPackages interface {
	Manifest() []byte
	Package(tag string) (size int64, write func(io.Writer) error, ok bool)
}

// mapDist counts the packages being sent.
type mapDist struct {
	once sync.Once
	sem  chan struct{}
}

// isMapDist says whether a request path is map delivery's.
func isMapDist(p string) bool {
	p = strings.TrimPrefix(p, "/")
	return p == MapPackagesPath || strings.HasPrefix(p, MapPackagePrefix)
}

// serveMapDist answers the manifest or a package (GET and HEAD). A package streams with its Content-Length; at most
// MapDownloads at once.
func (h *HTTP) serveMapDist(c net.Conn, peer, method, target string) {
	const who = "http"
	p := strings.TrimPrefix(urlPath(target), "/")
	head := method == "HEAD"
	reply := func(status, ctype string, body []byte, note string) {
		out := fmt.Sprintf("HTTP/1.1 %s\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", status, ctype, len(body))
		if !head {
			out += string(body)
		}
		c.Write([]byte(out))
		h.Log.Logf(who, "%s %s %s -> %s%s", peer, method, target, status, note)
	}
	if method != "GET" && !head {
		reply("405 Method Not Allowed", "text/plain", []byte("GET only\n"), "")
		return
	}
	if p == MapPackagesPath {
		body := h.Maps.Manifest()
		reply("200 OK", "text/plain", body, fmt.Sprintf(", %d bytes (the map packages' list)", len(body)))
		return
	}
	tag := strings.TrimPrefix(p, MapPackagePrefix)
	size, write, ok := h.Maps.Package(tag)
	if !ok {
		reply("404 Not Found", "text/plain", []byte("This server doesn't hand out that map.\n"), "")
		return
	}
	h.mapDist.once.Do(func() { h.mapDist.sem = make(chan struct{}, MapDownloads) })
	select {
	case h.mapDist.sem <- struct{}{}:
		defer func() { <-h.mapDist.sem }()
	default:
		reply("503 Service Unavailable", "text/plain", []byte("Busy handing out maps; try again later.\n"), " (busy)")
		return
	}
	out := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", size)
	if _, err := c.Write([]byte(out)); err != nil || head {
		return
	}
	h.Log.Logf(who, "%s GET %s -> 200 OK, sending the map package (%d bytes)", peer, target, size)
	if err := write(deadlineWriter{c}); err != nil {
		h.Log.Logf(who, "%s the map package %s: %s: %v", peer, tag, logging.ErrName(err), err)
		return
	}
	h.Log.Logf(who, "%s the map package %s sent (%d bytes)", peer, tag, size)
}

// deadlineWriter gives every write a minute: a client that stops reading frees its slot.
type deadlineWriter struct{ c net.Conn }

func (w deadlineWriter) Write(b []byte) (int, error) {
	w.c.SetWriteDeadline(time.Now().Add(time.Minute))
	return w.c.Write(b)
}
