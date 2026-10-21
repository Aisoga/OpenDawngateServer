package launcher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dawngate/server/internal/gamedl"
	"dawngate/server/internal/setup/archive"
	"dawngate/server/internal/version"
)

// GameSourcesKey is the key of Start-Client.ini and Start-Server.ini that names mirrors of Dawngate.zip, tried after
// the built-in source when the zip isn't found.
const GameSourcesKey = "game_sources"

// GameSourceFlag is the command line's source list: only those sources, without asking (also with --non-interactive).
const GameSourceFlag = "game-source"

func init() {
	// both programs take the download's mirrors (kept here, beside the download, so the key lists stay as they are)
	clientKeys = append(clientKeys, GameSourcesKey)
	serverKeys = append(serverKeys, GameSourcesKey)
}

// The free space a download wants beside the zip (the game copies made from it): a player's copy and This PC's
// match-host copy (1.5 GB each, with room to spare); a dedicated server makes only the host copy.
const (
	gameCopiesSpace    = 3500 << 20
	gameHostCopySpace  = 2000 << 20
	gameProgressPeriod = 250 * time.Millisecond
)

// The download's seams for the tests: the disk's free space, the HTTP client (nil: gamedl's).
var (
	gameFree   = archive.FreeBytes
	gameClient *http.Client
)

// gameSources is where Dawngate.zip may be downloaded from when it isn't found.
type gameSources struct {
	list  []string // in order
	named bool     // given on the command line (--game-source): no question, also without a console
	need  int64    // free bytes wanted beside the zip
	// noDownloadsDir: a test lane's Start-Client (--test-lane) doesn't look in the user's Downloads folder, so a
	// test of the download isn't served by the tester's own copy there
	noDownloadsDir bool
}

// zipPlaces are the places Dawngate.zip is looked for (ZipCandidates), without the Downloads folder for a test lane.
func zipPlaces(exeDir, configured string, gs gameSources) []string {
	c := ZipCandidates(exeDir, configured)
	if configured == "" && gs.noDownloadsDir && len(c) > 2 {
		c = c[:2]
	}
	return c
}

// newGameSources reads the sources: --game-source's list alone when given, else the built-in source and then the
// config file's game_sources. Each is an http:// or https:// address, separated by commas or blanks.
func newGameSources(flagValue, iniValue string, need int64) (gameSources, error) {
	gs := gameSources{need: need}
	value := iniValue
	if strings.TrimSpace(flagValue) != "" {
		value, gs.named = flagValue, true
	} else {
		gs.list = []string{gamedl.DefaultSource}
	}
	for _, s := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if err := gamedl.CheckURL(s); err != nil {
			if gs.named {
				return gs, fmt.Errorf("--%s: %v", GameSourceFlag, err)
			}
			return gs, fmt.Errorf("%s: %v", GameSourcesKey, err)
		}
		gs.list = append(gs.list, s)
	}
	return gs, nil
}

// sourceHost is a source as the question names it: its host for the built-in one, else its address.
func sourceHost(s string) string {
	if s == gamedl.DefaultSource {
		if u, err := url.Parse(s); err == nil {
			return u.Host
		}
	}
	return gamedl.Name(s)
}

// errDeclined: the player answered No to the download.
var errDeclined = errors.New("download declined")

// offerGameDownload downloads Dawngate.zip next to the program when it isn't found: in a console after one question
// (Enter or y: yes), without one only from sources named on the command line. It returns the zip's path, or "" with
// errDeclined (the player's No), or "" and nil when it doesn't apply (no console and no named source).
func offerGameDownload(o IO, in *bufio.Reader, gs gameSources) (string, error) {
	if len(gs.list) == 0 || !(o.Interactive || gs.named) {
		return "", nil
	}
	dest := filepath.Join(o.ExeDir, archive.GameZipName)
	if !gs.named {
		a, err := Prompt(in, o.Out, fmt.Sprintf("\nDawngate isn't set up yet. Download %s (1.3 GB) from %s? [Y/n]: ",
			archive.GameZipName, sourceHost(gs.list[0])))
		if err != nil || !(a == "" || strings.EqualFold(a, "y") || strings.EqualFold(a, "yes")) {
			return "", errDeclined
		}
	}
	fmt.Fprintf(o.Out, "  download %s into %s (an interrupted download continues at the next start)\n",
		archive.GameZipName, o.ExeDir)
	pl := &progressLine{out: o.Out, live: o.Live}
	d := gamedl.Downloader{
		Fetcher: gamedl.Fetcher{Client: gameClient, UserAgent: "OpenDawngateServer/" + version.String(), Progress: pl.show},
		Sources: gs.list, Dest: dest, Need: gs.need, Size: archive.GameZipSize, Free: gameFree,
		Say: func(f string, a ...any) { pl.clear(); fmt.Fprintf(o.Out, "  download "+f+"\n", a...) },
		Check: func(path string, r gamedl.Result) error {
			pl.clear()
			if r.Size == archive.GameZipSize && r.SHA256 == archive.GameZipSHA256 {
				return nil // the archive.org download, byte for byte
			}
			fmt.Fprintf(o.Out, "  download checking every file of it against build 1.0.903.854\n")
			return archive.CheckZipFiles(path, manifest, nil)
		},
	}
	t0 := time.Now()
	src, err := d.Run(context.Background())
	pl.clear()
	if err != nil {
		if errors.Is(err, gamedl.ErrNoSpace) {
			return "", err
		}
		return "", fmt.Errorf("the download failed: %v", err)
	}
	var size int64
	if st, err := os.Stat(dest); err == nil {
		size = st.Size()
	}
	fmt.Fprintf(o.Out, "  download %s from %s: %s in %s, checked: build 1.0.903.854\n", archive.GameZipName,
		gamedl.Name(src), mb(size), clock(time.Since(t0)))
	return dest, nil
}

// progressLine shows a download's progress: in place on a console (a few times a second), else a line per tenth.
type progressLine struct {
	out   io.Writer
	live  bool
	shown bool
	at    time.Time
	tenth int64
}

func (p *progressLine) show(g gamedl.Progress) {
	if g.Done == g.Resumed { // a try begins (another source may start over)
		p.tenth = 0
		if g.Resumed > 0 {
			p.clear()
			fmt.Fprintf(p.out, "  download continuing after %s\n", mb(g.Resumed))
		}
	}
	text := "  download " + mb(g.Done)
	if g.Total > 0 {
		text += " of " + mb(g.Total)
	}
	if g.Speed > 0 {
		text += fmt.Sprintf(", %.1f MB/s", g.Speed/(1<<20))
	}
	if g.Left >= 0 {
		text += ", " + clock(g.Left) + " left"
	}
	if p.live {
		if time.Since(p.at) < gameProgressPeriod && g.Done != g.Total {
			return
		}
		p.at = time.Now()
		fmt.Fprintf(p.out, "\r%-72s", text)
		p.shown = true
		return
	}
	if g.Total <= 0 {
		return
	}
	if t := g.Done * 10 / g.Total; t > p.tenth {
		p.tenth = t
		fmt.Fprintln(p.out, text)
	}
}

func (p *progressLine) clear() {
	if p.live && p.shown {
		fmt.Fprintf(p.out, "\r%72s\r", "")
		p.shown = false
	}
}

func mb(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }

// clock is a duration as h:mm:ss or m:ss.
func clock(d time.Duration) string {
	s := int64(d.Round(time.Second) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
