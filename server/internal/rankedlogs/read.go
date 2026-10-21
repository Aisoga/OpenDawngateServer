// Package rankedlogs rebuilds the Leaderboard's own counts (store.History.Ranked: the PvP and custom games and their
// wins, all time and per day) from dgserver's log files, for the games played on a server older than the first release
// that kept those counts (v0.9). That release started every account at 0, since the database never recorded a game's
// mode; the logs did. The operator runs it once, with the server stopped: dgserver and Start-Server's
// --import-ranked-logs, or the standalone import-ranked program.
//
// What a log says about a game (the same lines in every release from v0.5 to v0.8, and v0.9's still): the match host's
// createGame line ("[host] match <n> -> {"t":"createGame","gameId":<id>,…,"mode":…,"descriptors":<GameFlags>}"), the
// game's end ("game <id> finished: winner team …") and one line per player whose game was stored ("account <user
// id>: game <id> won|lost, …", after a "(account …: game … not stored: …)" line when storing failed). A spectator and a
// bot have no such line. The game counts by blaze.Ranked, the live server's rule, from createGame's mode and
// descriptors; each player's game goes to the UTC day of its line, as the live server's ApplyRanked does.
package rankedlogs

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/gmshost"
)

// FirstCountingVersion is the first release whose server keeps the Leaderboard's counts itself: its logs (and any
// later one's) are never imported.
var FirstCountingVersion = [3]int{0, 9, 0}

// File is what one log file holds.
type File struct {
	Path    string
	Name    string // its base name, YYYYMMDD-HHMMSS.log
	Version string // the version its start-up line names ("" = none)
	Named   bool   // given by name (not found in a folder): a developer build's or unknown version's file is read too
	Import  bool   // its games are imported
	Reason  string // why it is imported or skipped
	Games   []*Game
	Notes   []string // anything doubtful (a game without createGame, a long silence)
	ranked  bool     // it has "leaderboard:" lines: a server that kept the counts itself
}

// Game is one game of a log file (game ids start again at 1 in every file).
type Game struct {
	ID          int64
	Created     bool   // a createGame line was found
	Mode        string // createGame's mode
	Descriptors uint32 // createGame's descriptors (the GameFlags; 0 when absent)
	Finished    bool
	Spectators  int
	Results     []Result
}

// Result is one player's stored game.
type Result struct {
	Account int64
	Won     bool
	At      time.Time // the line's time (the server's local time, in the reader's location)
	Failed  bool      // the server logged that storing it failed
}

// Ranked reports whether the game counts for the Leaderboard (blaze.Ranked) and names its kind.
func (g *Game) Ranked() (bool, string) {
	if !g.Created {
		return false, "no createGame line, mode unknown"
	}
	r := blaze.Ranked(g.Mode, g.Descriptors)
	switch {
	case g.Mode == gmshost.ModeCustom && !r:
		return false, "practice group's game"
	case g.Mode == gmshost.ModeCustom || g.Mode == gmshost.ModePvP || g.Mode == gmshost.ModePractice:
		return r, g.Mode + " game"
	}
	return r, fmt.Sprintf("mode %q", g.Mode)
}

var (
	fileName      = regexp.MustCompile(`^(\d{8}-\d{6})\.log$`)
	logLine       = regexp.MustCompile(`^(\d{2}):(\d{2}):(\d{2})\.(\d{6}) \[([^\]]*)\] (.*)$`)
	versionLine   = regexp.MustCompile(`^version (\S+) \(`)
	createLine    = regexp.MustCompile(`^match \d+ -> (\{"t":"createGame".*)$`)
	finishedLine  = regexp.MustCompile(`^\s*game (\d+) finished: winner team `)
	storedLine    = regexp.MustCompile(`^\s*account (\d+): game (\d+) (won|lost), `)
	failedLine    = regexp.MustCompile(`^\s*\(account (\d+): game (\d+) not stored: `)
	spectatorLine = regexp.MustCompile(`^\s*account \d+: game (\d+) not stored \(a spectator\)`)
	rankedLine    = regexp.MustCompile(`^\s*account \d+: game \d+ leaderboard: `)
	releaseName   = regexp.MustCompile(`^v(\d+)\.(\d+)(?:\.(\d+))?$`)
)

// Read reads the log files at paths: a folder's dgserver log files (named YYYYMMDD-HHMMSS.log; other files and
// subfolders are left alone) and files named directly. Each file is read once, however often it is named. The times
// are read in loc, the zone of the server that wrote them (its local time). The files come back in name order.
func Read(paths []string, loc *time.Location) ([]*File, error) {
	seen := map[string]*File{}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			abs, _ := filepath.Abs(p)
			if f := seen[abs]; f != nil {
				f.Named = true
			} else {
				seen[abs] = &File{Path: p, Name: filepath.Base(p), Named: true}
			}
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !fileName.MatchString(e.Name()) {
				continue
			}
			fp := filepath.Join(p, e.Name())
			abs, _ := filepath.Abs(fp)
			if seen[abs] == nil {
				seen[abs] = &File{Path: fp, Name: e.Name()}
			}
		}
	}
	var files []*File
	for _, f := range seen {
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Name != files[j].Name {
			return files[i].Name < files[j].Name
		}
		return files[i].Path < files[j].Path
	})
	for _, f := range files {
		if err := f.read(loc); err != nil {
			return nil, fmt.Errorf("%s: %v", f.Path, err)
		}
		f.decide()
	}
	return files, nil
}

// read parses the file. The lines carry only the time of day: the day is the file name's (the server's start), one
// more each time the clock goes back by more than two hours (midnight; a summer-time change goes back only one).
func (f *File) read(loc *time.Location) error {
	m := fileName.FindStringSubmatch(f.Name)
	if m == nil {
		return errors.New("not a dgserver log file's name (YYYYMMDD-HHMMSS.log, the server's start: the day of its lines)")
	}
	start, err := time.ParseInLocation("20060102-150405", m[1], loc)
	if err != nil {
		return err
	}
	fh, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	defer fh.Close()
	games := map[int64]*Game{}
	game := func(id int64) *Game {
		g := games[id]
		if g == nil {
			g = &Game{ID: id}
			games[id] = g
			f.Games = append(f.Games, g)
		}
		return g
	}
	y, mo, d := start.Date()
	prev := start.Hour()*3600 + start.Minute()*60 + start.Second()
	days := 0
	var last time.Time
	r := bufio.NewReader(fh)
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			if lm := logLine.FindStringSubmatch(strings.TrimRight(line, "\r\n")); lm != nil {
				h, _ := strconv.Atoi(lm[1])
				mi, _ := strconv.Atoi(lm[2])
				s, _ := strconv.Atoi(lm[3])
				us, _ := strconv.Atoi(lm[4])
				tod := h*3600 + mi*60 + s
				if tod < prev-2*3600 {
					days++
				}
				prev = tod
				at := time.Date(y, mo, d+days, h, mi, s, us*1000, loc)
				last = at
				f.line(lm[5], lm[6], at, game)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if info, err := fh.Stat(); err == nil && !last.IsZero() && info.ModTime().Sub(last) > 24*time.Hour {
		f.Notes = append(f.Notes, fmt.Sprintf("last written %s, more than a day after its last line's time %s: "+
			"if the server logged nothing for a whole day, the games after that silence are dated a day early (only the "+
			"per-day counts; the totals are exact)", info.ModTime().In(loc).Format("2006-01-02 15:04"), last.Format("2006-01-02 15:04")))
	}
	return nil
}

// line takes one log line apart.
func (f *File) line(who, msg string, at time.Time, game func(int64) *Game) {
	if m := versionLine.FindStringSubmatch(msg); m != nil && who == "stub" && f.Version == "" {
		f.Version = m[1]
		return
	}
	if m := createLine.FindStringSubmatch(msg); m != nil {
		var c struct {
			GameID      int64  `json:"gameId"`
			Mode        string `json:"mode"`
			Descriptors int64  `json:"descriptors"`
		}
		if json.Unmarshal([]byte(m[1]), &c) == nil && c.GameID != 0 {
			g := game(c.GameID)
			g.Created, g.Mode, g.Descriptors = true, c.Mode, uint32(c.Descriptors)
		}
		return
	}
	if m := finishedLine.FindStringSubmatch(msg); m != nil {
		id, _ := strconv.ParseInt(m[1], 10, 64)
		game(id).Finished = true
		return
	}
	if m := storedLine.FindStringSubmatch(msg); m != nil {
		acct, _ := strconv.ParseInt(m[1], 10, 64)
		id, _ := strconv.ParseInt(m[2], 10, 64)
		g := game(id)
		for i := range g.Results {
			if g.Results[i].Account == acct { // once per player and game
				g.Results[i].Won, g.Results[i].At = m[3] == "won", at
				return
			}
		}
		g.Results = append(g.Results, Result{Account: acct, Won: m[3] == "won", At: at})
		return
	}
	if m := failedLine.FindStringSubmatch(msg); m != nil {
		acct, _ := strconv.ParseInt(m[1], 10, 64)
		id, _ := strconv.ParseInt(m[2], 10, 64)
		g := game(id)
		for i := range g.Results {
			if g.Results[i].Account == acct {
				g.Results[i].Failed = true
				return
			}
		}
		g.Results = append(g.Results, Result{Account: acct, At: at, Failed: true})
		return
	}
	if m := spectatorLine.FindStringSubmatch(msg); m != nil {
		id, _ := strconv.ParseInt(m[1], 10, 64)
		game(id).Spectators++
		return
	}
	if rankedLine.MatchString(msg) {
		f.ranked = true
	}
}

// decide sets Import and Reason by the file's version.
func (f *File) decide() {
	v := f.Version
	if v == "" {
		v = "none"
	}
	rel, isRelease := release(f.Version)
	switch {
	case isRelease && !less(rel, FirstCountingVersion):
		f.Reason = fmt.Sprintf("skipped: version %s counts the Leaderboard's games itself", v)
	case f.ranked:
		f.Reason = fmt.Sprintf("skipped: version %s counted the Leaderboard's games itself (its \"leaderboard:\" lines)", v)
	case isRelease:
		f.Import, f.Reason = true, "version "+v
	case f.Named:
		f.Import, f.Reason = true, fmt.Sprintf("version %s (a developer build, or unknown): imported because the file was named", v)
	default:
		f.Reason = fmt.Sprintf("skipped: version %s is a developer build or unknown (name the file itself to import it)", v)
	}
	for _, g := range f.Games {
		if len(g.Results) > 0 && !g.Created {
			f.Notes = append(f.Notes, fmt.Sprintf("game %d: no createGame line, so its mode is unknown: not counted", g.ID))
		}
	}
}

// release parses a release's version, v<major>.<minor>[.<patch>].
func release(v string) ([3]int, bool) {
	m := releaseName.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var r [3]int
	for i := range 3 {
		r[i], _ = strconv.Atoi(m[i+1])
	}
	return r, true
}

func less(a, b [3]int) bool {
	for i := range 3 {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
