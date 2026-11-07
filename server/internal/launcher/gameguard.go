package launcher

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
)

// Who may use Start-Client's local ports. They listen on loopback, so every program on this PC reaches them, also another
// Windows user's and a browser running a web page. The UI channel has no password of its own (the tunnel session is the
// identity), and each stream counts against the player's limits at the server. So only programs of the player copy (the
// game, and CrashRpt's sender beside it) get these ports; the login screen's pages on the HTTP port are for the player's
// browser and have rules of their own (front_pages.go).
//
// What this stops: a browser (whatever a web page asks of it), another user's programs, and a program of the same user that
// simply connects. What it can't stop: a program of the same user that is willing to inject itself into the game. That
// program can already read the game's and Start-Client's memory, so no check on this PC would hold against it.

// checkGameOwner turns the guard on for Start-Client's own front and tunnel. A test's "game" is the test binary, so TestMain
// turns it off, and the tests of the guard turn it on again.
var checkGameOwner = true

// errOwnerUnknown: this OS can't tell who holds a connection at all (the call failed, or another OS), which no program can
// cause. The game must still start, so the connection passes, and the log says so.
var errOwnerUnknown = errors.New("this system can't say which program holds a connection")

// maxGuardLines bounds the log's refusals: a page in a browser can connect hundreds of times a second.
const maxGuardLines = 20

// gameGuard says whether a local connection comes from the player copy's programs (nil: every connection does).
type gameGuard struct {
	root    string // the player copy's folder
	logf    func(string, ...any)
	once    sync.Once
	refused atomic.Int64
}

// newGameGuard is the guard for a player copy; nil when the check is off.
func newGameGuard(root string, logf func(string, ...any)) *gameGuard {
	if !checkGameOwner {
		return nil
	}
	return &gameGuard{root: root, logf: logf}
}

// isGame reports whether the connection is the game's, and otherwise what holds it, for the log.
func (g *gameGuard) isGame(c net.Conn) (bool, string) {
	if g == nil {
		return true, ""
	}
	pid, image, err := peerProcess(c)
	switch {
	case errors.Is(err, errOwnerUnknown):
		g.once.Do(func() { g.logf("WARNING: %v: local connections aren't limited to the game", err) })
		return true, ""
	case err != nil:
		return false, err.Error()
	case underFolder(image, g.root):
		return true, ""
	}
	return false, image + " (pid " + strconv.Itoa(int(pid)) + ")"
}

// allows is isGame with the refusal logged, for a port with no use outside the game.
func (g *gameGuard) allows(port int, c net.Conn) bool {
	ok, who := g.isGame(c)
	if !ok {
		g.logRefused(port, who)
	}
	return ok
}

// logRefused logs a refusal, up to maxGuardLines of them in a run.
func (g *gameGuard) logRefused(port int, who string) {
	switch n := g.refused.Add(1); {
	case n < maxGuardLines:
		g.logf("port %d: refused a connection: not the game: %s", port, who)
	case n == maxGuardLines:
		g.logf("port %d: refused a connection: not the game: %s (no more of these lines in this run)", port, who)
	}
}

// underFolder reports whether a program's file lies under root. It compares folders by identity and not by name, since a
// path can reach the same folder through a junction or another spelling.
func underFolder(image, root string) bool {
	want, err := os.Stat(root)
	if err != nil {
		return false
	}
	for d := filepath.Dir(image); ; d = filepath.Dir(d) {
		if got, err := os.Stat(d); err == nil && os.SameFile(got, want) {
			return true
		}
		if parent := filepath.Dir(d); parent == d {
			return false
		}
	}
}
