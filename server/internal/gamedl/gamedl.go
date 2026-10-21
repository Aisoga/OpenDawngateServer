// Package gamedl downloads Dawngate.zip for the first setup of Start-Client and Start-Server: from an ordered list of
// sources, each tried in turn when one fails, resuming an interrupted download (<zip>.part, an HTTP Range request;
// a server that ignores ranges starts over), and every finished download checked against build 1.0.903.854 before it
// becomes Dawngate.zip. A source that gives another file is skipped, never used.
//
// The project never hosts or ships the game: the sources are the public archive (DefaultSource) and mirrors the
// player or operator names.
package gamedl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultSource is the built-in source: the direct download of archive.org's item "Dawngate"
// (https://archive.org/details/Dawngate), which redirects to one of its storage servers.
const DefaultSource = "https://archive.org/download/Dawngate/Dawngate.zip"

// MaxSize bounds a download: Dawngate.zip has 1.3 GB; a source that announces or sends more gave another file.
const MaxSize = 4 << 30

// IdleTimeout ends a try whose source sends nothing for this long (a stalled connection).
const IdleTimeout = 60 * time.Second

// ErrWrongFile is a source's answer that can't be Dawngate.zip (too big, not a zip, or a file that fails the check).
var ErrWrongFile = errors.New("not build 854's Dawngate.zip")

// ErrNoSpace is a disk without room for the download and the game copies made from it.
var ErrNoSpace = errors.New("not enough disk space")

// Progress is how far a download is.
type Progress struct {
	Done    int64         // bytes in the .part file so far
	Total   int64         // the file's size; 0 when the source didn't say
	Resumed int64         // bytes the .part file had when this try began
	Speed   float64       // bytes per second in this try
	Left    time.Duration // the time left at that speed; -1 when unknown
}

// Result is a finished download (the .part file complete, not yet checked).
type Result struct {
	Size    int64
	SHA256  string // the whole file's
	Resumed int64  // bytes a previous try had already downloaded
}

// Fetcher downloads one URL into a .part file.
type Fetcher struct {
	Client    *http.Client // nil: NewClient()
	UserAgent string
	Idle      time.Duration  // 0: IdleTimeout
	Progress  func(Progress) // may be nil; called often (the caller throttles)
	Note      func(string)   // a remark such as a source that can't resume (may be nil)
}

// NewClient is the HTTP client for the sources: the system's proxy settings, timeouts for connecting and for the
// answer's header (the body has the Fetcher's idle timeout), at most 10 redirects and none from https to http.
func NewClient() *http.Client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 60 * time.Second}
	return &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" && via[0].URL.Scheme == "https" {
			return fmt.Errorf("redirected to %s, not https", req.URL.Redacted())
		}
		return nil
	}}
}

// CheckURL accepts a source's address: an http or https URL with a host.
func CheckURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not an http:// or https:// address", s)
	}
	return nil
}

// Name is how a source is named in messages: its address without credentials or query.
func Name(source string) string {
	u, err := url.Parse(source)
	if err != nil {
		return source
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

// Fetch downloads source into part. A part file that is there already is continued with a Range request (its bytes
// are hashed first); a source that answers with the whole file instead starts it over. The result's SHA-256 is the
// whole file's. On an error part stays for the next try (also with another source), except when the source gave
// something that can't be the archive (ErrWrongFile: more than MaxSize, or not a zip), which removes it.
func (f Fetcher) Fetch(ctx context.Context, source, part string) (Result, error) {
	h := sha256.New()
	have, err := hashPart(part, h)
	if err != nil {
		return Result{}, err
	}
	res, err := f.fetch(ctx, source, part, h, have)
	if errors.Is(err, errRangeRefused) { // 416 with a part that doesn't fit: start over once
		os.Remove(part)
		h.Reset()
		res, err = f.fetch(ctx, source, part, h, 0)
	}
	if errors.Is(err, ErrWrongFile) {
		os.Remove(part)
	}
	return res, err
}

var errRangeRefused = errors.New("the source refused the range")

func hashPart(part string, h hash.Hash) (int64, error) {
	p, err := os.Open(part)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer p.Close()
	return io.Copy(h, p)
}

func (f Fetcher) fetch(ctx context.Context, source, part string, h hash.Hash, have int64) (Result, error) {
	cl := f.Client
	if cl == nil {
		cl = NewClient()
	}
	idle := f.Idle
	if idle <= 0 {
		idle = IdleTimeout
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return Result{}, err
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	req.Header.Set("Accept-Encoding", "identity")
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	resp, err := cl.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	var total int64
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
		start, size, ok := contentRange(resp.Header.Get("Content-Range"))
		if !ok || start != have {
			return Result{}, fmt.Errorf("answered the range %d- with %q", have, resp.Header.Get("Content-Range"))
		}
		total = size
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && have > 0:
		if _, size, ok := contentRange(resp.Header.Get("Content-Range")); ok && size == have {
			return Result{Size: have, SHA256: hex.EncodeToString(h.Sum(nil)), Resumed: have}, nil // complete already
		}
		return Result{}, errRangeRefused
	case resp.StatusCode == http.StatusOK:
		if have > 0 && f.Note != nil {
			f.Note("it can't continue a download: starting over")
		}
		have = 0
		h.Reset()
		total = resp.ContentLength
		if total < 0 {
			total = 0
		}
	default:
		return Result{}, errors.New(resp.Status)
	}
	if total > MaxSize {
		return Result{}, fmt.Errorf("%w: it has %d bytes", ErrWrongFile, total)
	}
	flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if have == 0 {
		flag = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	out, err := os.OpenFile(part, flag, 0o644)
	if err != nil {
		return Result{}, err
	}
	// a stalled body ends the try after idle without data
	watchdog := time.AfterFunc(idle, func() { cancel(fmt.Errorf("nothing arrived for %v", idle)) })
	defer watchdog.Stop()
	t0, done := time.Now(), have
	report := func() {
		if f.Progress == nil {
			return
		}
		p := Progress{Done: done, Total: total, Resumed: have, Left: -1}
		if s := time.Since(t0).Seconds(); s > 0 {
			p.Speed = float64(done-have) / s
		}
		if p.Speed > 0 && total > 0 {
			p.Left = time.Duration(float64(total-done) / p.Speed * float64(time.Second))
		}
		f.Progress(p)
	}
	report()
	buf := make([]byte, 256<<10)
	var werr error
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			watchdog.Reset(idle)
			if done == 0 && !isZipStart(buf[:n]) {
				werr = fmt.Errorf("%w: it isn't a zip archive (it begins %q)", ErrWrongFile, printable(buf[:min(n, 24)]))
				break
			}
			if done+int64(n) > MaxSize {
				werr = fmt.Errorf("%w: more than %d bytes", ErrWrongFile, int64(MaxSize))
				break
			}
			if _, werr = out.Write(buf[:n]); werr != nil {
				break
			}
			h.Write(buf[:n])
			done += int64(n)
			report()
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if c := context.Cause(ctx); c != nil && ctx.Err() != nil {
				rerr = c
			}
			werr = fmt.Errorf("the download stopped after %s (%v)", sizeOf(done, total), rerr)
			break
		}
	}
	if cerr := out.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil && total > 0 && done != total {
		werr = fmt.Errorf("the download stopped after %s", sizeOf(done, total))
	}
	if werr != nil {
		return Result{}, werr
	}
	return Result{Size: done, SHA256: hex.EncodeToString(h.Sum(nil)), Resumed: have}, nil
}

// sizeOf is "<done> of <total> MB" (the total when known).
func sizeOf(done, total int64) string {
	s := fmt.Sprintf("%.1f", float64(done)/(1<<20))
	if total > 0 {
		s += fmt.Sprintf(" of %.1f", float64(total)/(1<<20))
	}
	return s + " MB"
}

// contentRange reads "bytes <start>-<end>/<size>" or "bytes */<size>" (start -1).
func contentRange(v string) (start, size int64, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(v), "bytes ")
	if !found {
		return 0, 0, false
	}
	span, sz, found := strings.Cut(rest, "/")
	if !found {
		return 0, 0, false
	}
	size, err := strconv.ParseInt(sz, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	if span == "*" {
		return -1, size, true
	}
	a, _, found := strings.Cut(span, "-")
	if start, err = strconv.ParseInt(a, 10, 64); err != nil || !found {
		return 0, 0, false
	}
	return start, size, true
}

// isZipStart: a zip archive begins with a local file header ("PK\x03\x04"); an empty one with the end record.
func isZipStart(b []byte) bool {
	for _, sig := range []string{"PK\x03\x04", "PK\x05\x06"} {
		if len(b) < len(sig) {
			if strings.HasPrefix(sig, string(b)) {
				return true // too short to tell yet
			}
			continue
		}
		if string(b[:len(sig)]) == sig {
			return true
		}
	}
	return false
}

func printable(b []byte) string {
	s := []rune{}
	for _, c := range string(b) {
		if c < 32 || c > 126 {
			c = '.'
		}
		s = append(s, c)
	}
	return string(s)
}

// Downloader gets Dawngate.zip into Dest from the first of Sources that gives a file passing Check.
type Downloader struct {
	Fetcher
	Sources []string
	Dest    string                            // the archive's final path (the download goes to Dest+".part")
	Check   func(path string, r Result) error // the file's check (nil: none); an error makes the source a wrong one
	Need    int64                             // free bytes wanted beside the download (the copies made from it)
	Size    int64                             // the archive's expected size, for the space check
	Free    func(dir string) (uint64, error)  // nil: no space check
	Say     func(format string, args ...any)  // the lines about each source (may be nil)
}

// Run tries the sources in turn and returns the source that gave the archive. A source that fails (no connection, an
// HTTP error, a cut connection) leaves the .part file for the next one; one that gives a file failing Check is
// skipped and its file removed (a resumed file that fails is fetched once more from its start first, since the part
// may have come from another source). The error lists every source's failure.
func (d Downloader) Run(ctx context.Context) (string, error) {
	say := d.Say
	if say == nil {
		say = func(string, ...any) {}
	}
	part := d.Dest + ".part"
	if d.Free != nil {
		var have int64
		if st, err := os.Stat(part); err == nil {
			have = st.Size()
		}
		need := d.Need + max(d.Size-have, 0)
		if free, err := d.Free(filepath.Dir(d.Dest)); err == nil && free < uint64(need) {
			return "", fmt.Errorf("%w for %s: %.1f GB free, %.1f GB needed (the download and the game copies)",
				ErrNoSpace, filepath.Dir(d.Dest), float64(free)/(1<<30), float64(need)/(1<<30))
		}
	}
	var failures []string
	for _, src := range d.Sources {
		name := Name(src)
		f := d.Fetcher
		f.Note = func(s string) { say("%s: %s", name, s) }
		err := d.try(ctx, f, src, part, say)
		if err == nil {
			return src, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if errors.Is(err, ErrWrongFile) {
			say("%s gave a file that isn't build 854's Dawngate.zip (%s); skipping it", name,
				strings.TrimPrefix(err.Error(), ErrWrongFile.Error()+": "))
		} else {
			say("%s: %v", name, err)
		}
		failures = append(failures, name+": "+err.Error())
	}
	if len(d.Sources) == 0 {
		return "", errors.New("no source to download Dawngate.zip from")
	}
	return "", fmt.Errorf("no source gave Dawngate.zip (%s)", strings.Join(failures, "; "))
}

// try downloads from one source and checks the file; a passing file becomes Dest.
func (d Downloader) try(ctx context.Context, f Fetcher, src, part string, say func(string, ...any)) error {
	for attempt := 0; ; attempt++ {
		res, err := f.Fetch(ctx, src, part)
		if err != nil {
			return err
		}
		if d.Check != nil {
			if err = d.Check(part, res); err != nil {
				os.Remove(part)
				if res.Resumed > 0 && attempt == 0 {
					say("%s: the continued download fails the check (%v); downloading it again from the start", Name(src), err)
					continue
				}
				return fmt.Errorf("%w: %v", ErrWrongFile, err)
			}
		}
		os.Remove(d.Dest)
		return os.Rename(part, d.Dest)
	}
}
