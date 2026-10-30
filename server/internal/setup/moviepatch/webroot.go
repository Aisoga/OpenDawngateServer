package moviepatch

import "fmt"

// The After Action Review's web root: client_ui/aftaction/AfterActionTabs.as loads the match history pages from the
// server the copy is set up for, and reads that server's web root from zz_AfterActionReview.snap as
// Battlegrounds/dgweb.json (the game's file opener keeps only .json). The file (ASCII, LF, tab-separated):
//
//	dawngate-web	1
//	common	http://<host>:<port>/battlegrounds_test/live/www/client/1/common
//	end
//
// The root is the one the copy's config packages give the game's web content (configpatch: http://<host>:18080, the
// web content's DRC path 1, web.webContentRoot; the home page is <common>/home.html): the same host and port in a
// tunnelled copy, whose front passes its HTTP port to the server of the current login. The movie can't ask the game
// for the root itself (the home page's token expansion, UI_ExpandMoviePath 0x004BD8E0, is bound only to the home
// page's component). re/scripts/patch_login_movie.py's web_root_file writes the same bytes.

const (
	// WebRootPath is the file's path in zz_AfterActionReview.snap.
	WebRootPath = "UI/Scaleform/Battlegrounds/dgweb.json"
	// WebPort is the HTTP port of the copy's config packages (configpatch.Defaults().HTTPPort).
	WebPort = 18080
	// WebContentRoot is the common web content's path on that port (the server's web.webContentRoot + "/common").
	WebContentRoot = "battlegrounds_test/live/www/client/1/common"
	// DefaultWebHost is the host of a spec without WebHost (the tests; a setup always names the copy's server).
	DefaultWebHost = "127.0.0.1"
)

// WebRootFile returns dgweb.json's bytes for the server host (an IPv4 address or a host name) and port.
func WebRootFile(host string, port int) ([]byte, error) {
	if host == "" {
		return nil, fmt.Errorf("web root: no host")
	}
	for _, r := range host {
		if r <= ' ' || r >= 0x7F || r == '"' || r == '\'' || r == '<' || r == '>' {
			return nil, fmt.Errorf("web root: host %q: only printable ASCII without spaces, quotes or angle brackets", host)
		}
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("web root: port %d: must be 1..65535", port)
	}
	return fmt.Appendf(nil, "dawngate-web\t1\ncommon\thttp://%s:%d/%s\nend\n", host, port, WebContentRoot), nil
}
