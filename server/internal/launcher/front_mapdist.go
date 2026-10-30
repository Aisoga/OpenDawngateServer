package launcher

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/mapdist"
	"dawngate/server/internal/tunnel"
)

// MapDownload is map delivery on the player's side (MAPED-DIST): at a login the front fetches the maps the server
// offers that the player copy lacks or has in another version, through the login's tunnel session (the server whose
// key the player confirmed), checks each package and puts it into Dir; Start-Client's next start compiles it.
type MapDownload struct {
	Dir    string         // the maps folder the packages go into (Start-Client's maps folder beside it)
	Limits mapdist.Limits // the bounds of a package (mapdist.DefaultLimits, map_download_max_mb)
	// Notify shows a line to the player (Start-Client's window; nil: the front's log only).
	Notify func(line string)
	// Verify checks a package against the copy (nil: mapdist.BuildVerifier of the player copy).
	Verify func(copy string) mapdist.Verifier
	// Every is the progress lines' interval (0: 10 s).
	Every time.Duration
}

// frontMapDL is the front's delivery state: one delivery at a time, the packages refused for their content in this
// run (not fetched again), and a wait for the tests.
type frontMapDL struct {
	mu      sync.Mutex
	running bool
	refused map[[32]byte]bool
	wg      sync.WaitGroup
}

// deliverMaps starts the delivery for a login's session (after writeMapList, which uses the same facts): the server's
// offered maps with a version (tunnel.FactMapVersion) whose archive the player copy lacks or has in another version.
// It runs in the background; the login goes on.
func (f *Front) deliverMaps(s *frontSession) {
	md := f.MapDownload
	if md == nil || md.Dir == "" || s.b.Tag == ThisPCTag {
		return
	}
	f.listMu.Lock()
	copy := f.listCopy
	f.listMu.Unlock()
	if copy == "" {
		return
	}
	wants := f.mapWants(copy, s.facts)
	if len(wants) == 0 {
		return
	}
	f.mapDL.mu.Lock()
	if f.mapDL.running {
		f.mapDL.mu.Unlock()
		f.logf("map delivery from %s: another one runs; tried at the next login", s.b.Tag)
		return
	}
	f.mapDL.running = true
	f.mapDL.wg.Add(1)
	f.mapDL.mu.Unlock()
	go func() {
		defer func() {
			f.mapDL.mu.Lock()
			f.mapDL.running = false
			f.mapDL.mu.Unlock()
			f.mapDL.wg.Done()
		}()
		f.runDelivery(s, copy, wants)
	}()
}

// mapWants are the offered maps with a server version whose archive in the copy isn't that version (the copy's
// archive names come from the maps folder's packages, f.Maps; a map without a package there is wanted anyway).
func (f *Front) mapWants(copy string, facts map[string]string) []mapdist.Want {
	versions := tunnel.FactMapVersions(facts)
	tags, _ := tunnel.FactMapTags(facts)
	data := filepath.Join(copy, "game", "Data")
	var wants []mapdist.Want
	for _, tag := range tags {
		v, ok := versions[strings.ToLower(tag)]
		if !ok {
			continue // no version named (MM_Map_v3, a server without a host copy): nothing to check a package against
		}
		have := false
		for _, m := range f.Maps {
			if strings.EqualFold(m.Tag, tag) {
				if got := tunnel.ReadInstalledMaps(data, m.Archive); len(got) == 1 && got[0].SHA256 == v {
					have = true
				}
			}
		}
		if !have {
			wants = append(wants, mapdist.Want{Tag: strings.ToLower(tag), Version: v})
		}
	}
	return wants
}

func (f *Front) runDelivery(s *frontSession, copy string, wants []mapdist.Want) {
	md := f.MapDownload
	say := func(format string, a ...any) {
		line := fmt.Sprintf(format, a...)
		f.logf("map delivery: %s", line)
		if md.Notify != nil {
			md.Notify(line)
		}
	}
	verify := mapdist.BuildVerifier(copy)
	if md.Verify != nil {
		verify = md.Verify(copy)
	}
	d := &mapdist.Delivery{MapsDir: md.Dir, Server: s.b.Tag + " (" + s.b.Gateway() + ")", Limits: md.Limits,
		Get: f.tunnelGetter(s), Verify: verify, Every: md.Every,
		Skip: func(sum [32]byte) bool {
			f.mapDL.mu.Lock()
			defer f.mapDL.mu.Unlock()
			return f.mapDL.refused[sum]
		},
		Logf: func(format string, a ...any) { say("%s: "+format, append([]any{s.b.Tag}, a...)...) }}
	var tags []string
	for _, w := range wants {
		tags = append(tags, "~#"+w.Tag)
	}
	f.logf("map delivery: %s offers %s, which the copy lacks or has in another version: asking it", s.b.Tag,
		strings.Join(tags, ", "))
	for _, r := range d.Run(wants) {
		name := r.Display
		if name == "" {
			name = firstNonEmpty(r.Name, "~#"+r.Tag)
		}
		switch r.Status {
		case mapdist.Installed:
			say("%s from %s: downloaded and checked into %s; close the game and start Start-Client.exe again to "+
				"install it", name, s.b.Tag, filepath.Join(md.Dir, r.Name))
		case mapdist.Present:
			say("%s from %s: this server's version is in the maps folder already; close the game and start "+
				"Start-Client.exe again to install it", name, s.b.Tag)
		case mapdist.NotServed:
			say("%s from %s: not downloaded: %v. Ask the server's operator for the map package, copy it into %s and "+
				"start Start-Client.exe again", name, s.b.Tag, r.Err, md.Dir)
		case mapdist.Refused:
			if errors.Is(r.Err, mapdist.ErrBundle) && r.Package != ([32]byte{}) {
				f.mapDL.mu.Lock()
				if f.mapDL.refused == nil {
					f.mapDL.refused = map[[32]byte]bool{}
				}
				f.mapDL.refused[r.Package] = true
				f.mapDL.mu.Unlock()
			}
			say("%s from %s: not installed: %v", name, s.b.Tag, r.Err)
		default:
			say("%s from %s: not downloaded: %v (tried again at your next login to it)", name, s.b.Tag, r.Err)
		}
	}
}

// tunnelGetter asks the server's HTTP port through the session's tunnel: a stream like the game's own to that port,
// one request per stream (the server answers with Connection: close). Each read and write has a minute.
func (f *Front) tunnelGetter(s *frontSession) mapdist.Getter {
	return func(path string) (io.ReadCloser, int64, int, error) {
		src := &net.TCPAddr{IP: f.TunnelIP, Port: 0}
		c, err := s.c.OpenStream(s.serverPort(f, f.Ports.HTTP), src)
		if err != nil {
			return nil, 0, 0, err
		}
		dc := &idleConn{Conn: c}
		req, _ := http.NewRequest("GET", "http://dawngate/"+path, nil)
		req.Close = true
		if err := req.Write(dc); err != nil {
			c.Close()
			return nil, 0, 0, err
		}
		resp, err := http.ReadResponse(bufio.NewReader(dc), req)
		if err != nil {
			c.Close()
			return nil, 0, 0, err
		}
		return &closeBoth{ReadCloser: resp.Body, c: c}, resp.ContentLength, resp.StatusCode, nil
	}
}

// idleConn gives each read and write of a stream a minute.
type idleConn struct{ net.Conn }

func (c *idleConn) Read(b []byte) (int, error) {
	c.Conn.SetDeadline(time.Now().Add(time.Minute))
	return c.Conn.Read(b)
}

func (c *idleConn) Write(b []byte) (int, error) {
	c.Conn.SetDeadline(time.Now().Add(time.Minute))
	return c.Conn.Write(b)
}

type closeBoth struct {
	io.ReadCloser
	c net.Conn
}

func (b *closeBoth) Close() error {
	b.ReadCloser.Close()
	return b.c.Close()
}

// WaitMapDelivery waits until a running delivery has ended (tests).
func (f *Front) WaitMapDelivery() { f.mapDL.wg.Wait() }
