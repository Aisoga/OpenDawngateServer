package web

// The match items' icons for the history page (its scoreboard's items column and its Items tab): PNGs the setup
// writes beside the content file (itemicons/<item>.png, 64 x 64), served under the common web path beside the
// pages, ".../common/itemicons/<item>.png". The web view can't reach the game's own textures through game://
// (only Web.package's pictures), so the server hands them out. They change only with a new setup, so they are
// served cacheable with an ETag.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dawngate/server/internal/logging"
)

// ItemIconsPart is the common folder of the item icons (".../common/itemicons/<item>.png" for any DRC or locale).
const ItemIconsPart = "/common/itemicons/"

// iconMaxAge is an icon's cache lifetime: a day (a new setup's icons show within that).
const iconMaxAge = 86400

// ItemIcons is what the HTTP server answers for the item icons.
type ItemIcons struct {
	Dir   string          // the icons' folder
	Names map[string]bool // the files served ("Basic_Time.png"); any other name is 404

	log homeLog
}

// itemIconName is the file name a path asks for under ItemIconsPart ("" when the path isn't one: no folder, a
// further slash or backslash, or an escape).
func itemIconName(p string) string {
	i := strings.LastIndex(p, ItemIconsPart)
	if i < 0 {
		return ""
	}
	name := p[i+len(ItemIconsPart):]
	if name == "" || strings.ContainsAny(name, `/\%`) {
		return ""
	}
	return name
}

// isItemIconPath says whether a path is under the icons' folder at all (a refused name is answered 404 there).
func isItemIconPath(p string) bool { return strings.Contains(p, ItemIconsPart) }

// serveItemIcon answers a request under ItemIconsPart: a listed file as image/png with an ETag (304 on a matching
// If-None-Match), anything else 404. One log line per client address in homeLogEvery (a page asks for dozens of
// icons), and every refusal.
func (h *HTTP) serveItemIcon(c net.Conn, peer, method, target, ifNoneMatch string) {
	ic := h.ItemIcons
	const who = "http"
	status, ctype, etag, body := "404 Not Found", "text/plain; charset=utf-8", "", []byte("no such item icon\n")
	if name := itemIconName(urlPath(target)); name != "" && ic.Names[name] {
		if b, err := os.ReadFile(filepath.Join(ic.Dir, name)); err == nil {
			sum := sha256.Sum256(b)
			status, ctype, etag, body = "200 OK", "image/png", `"`+hex.EncodeToString(sum[:8])+`"`, b
		}
	}
	if etag != "" && ifNoneMatch == etag {
		status, body = "304 Not Modified", nil
	}
	head := fmt.Sprintf("HTTP/1.1 %s\r\nContent-Type: %s\r\nContent-Length: %d\r\n", status, ctype, len(body))
	if etag != "" {
		head += fmt.Sprintf("Cache-Control: public, max-age=%d\r\nETag: %s\r\n", iconMaxAge, etag)
	}
	out := []byte(head + "Connection: close\r\n\r\n")
	if method != "HEAD" {
		out = append(out, body...)
	}
	if _, err := c.Write(out); err != nil {
		h.Log.Logf(who, "%s %s: %v", peer, logging.ErrName(err), err)
		return
	}
	if ic.log.first(addrKey(c.RemoteAddr()), time.Now()) || !strings.HasPrefix(status, "2") && !strings.HasPrefix(status, "3") {
		h.Log.Logf(who, "%s %s %s -> %s, %d bytes (item icon; later requests from this address within %s not logged)",
			peer, method, target, status, len(body), homeLogEvery)
	}
}
