package issues

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"dawngate/server/internal/store"
)

// A claim's proof is YouTube links only, strictly parsed: the server keeps a video's id and its start in seconds,
// shows the canonical link (Video.URL) as a plain link, and never contacts YouTube (no embeds, no thumbnails, no
// lookup whether the video exists).

// Video is one YouTube video of a claim.
type Video struct {
	ID      string // the video id: 11 of A-Z a-z 0-9 _ -
	Seconds int    // where to start; -1 = no timestamp
}

// The hosts a link may name (compared in lowercase).
var youTubeHosts = map[string]bool{"youtube.com": true, "www.youtube.com": true, "m.youtube.com": true, "youtu.be": true}

// maxLinkLength bounds a pasted link.
const maxLinkLength = 512

// Errors of ParseLink (their texts are shown to the player).
var (
	ErrNotYouTube = errors.New("only YouTube links are taken (youtube.com or youtu.be)")
	ErrNoVideo    = errors.New("that link doesn't point to a YouTube video (a watch, shorts, live or youtu.be link)")
	ErrBadTime    = errors.New("the time isn't one of 83, 83s, 1m23s, 1h2m3s or 1:23")
)

// ParseLink reads a YouTube link and an optional time field. Taken are the hosts youtube.com, www.youtube.com,
// m.youtube.com and youtu.be over https or http (http is normalized away: only the id is kept), the paths
// /watch?v=<id>, /shorts/<id>, /live/<id>, /embed/<id> and youtu.be/<id>, and a missing scheme (a pasted
// "youtu.be/<id>"). Refused are other hosts and schemes (javascript:, data:, ...), a user name or password, a port,
// other paths, ids that aren't 11 of A-Z a-z 0-9 _ -, spaces, backslashes and control characters. The link's t= or
// start= gives the start (83, 83s, 1m23s, 1h2m3s); a non-empty timeField (the form's own field: 1:23, 1:02:03 or the
// same forms) wins over it. Other query parameters (si=, list=, ...) and a fragment are ignored.
func ParseLink(link, timeField string) (Video, error) {
	v := Video{Seconds: -1}
	link = strings.TrimSpace(link)
	if link == "" || len(link) > maxLinkLength {
		return v, ErrNoVideo
	}
	for i := 0; i < len(link); i++ {
		if c := link[i]; c <= ' ' || c == 0x7f || c == '\\' || c >= 0x80 {
			return v, ErrNoVideo
		}
	}
	lower := strings.ToLower(link)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") {
		if !hasYouTubeHostPrefix(lower) {
			return v, ErrNotYouTube // another scheme (javascript:, data:, ftp:, ...) or another host
		}
		link = "https://" + link
	}
	u, err := url.Parse(link)
	if err != nil || u.Opaque != "" {
		return v, ErrNoVideo
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return v, ErrNotYouTube
	}
	if u.User != nil || u.Port() != "" || strings.Contains(u.Host, ":") {
		return v, ErrNotYouTube
	}
	host := strings.ToLower(u.Host)
	if !youTubeHosts[host] {
		return v, ErrNotYouTube
	}
	if u.RawPath != "" || strings.Contains(u.EscapedPath(), "%") {
		return v, ErrNoVideo // no escaped path: the id is plain text
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return v, ErrNoVideo
	}
	for _, kv := range strings.Split(u.RawQuery, "&") { // the id and the times plain too: no %-escapes, no +
		k, val, _ := strings.Cut(kv, "=")
		if strings.Contains(k, "%") || (k == "v" || k == "t" || k == "start") && strings.ContainsAny(val, "%+") {
			return v, ErrNoVideo
		}
	}
	path := u.Path
	switch {
	case host == "youtu.be":
		v.ID = strings.TrimPrefix(path, "/")
	case path == "/watch":
		vs := q["v"]
		if len(vs) != 1 {
			return v, ErrNoVideo
		}
		v.ID = vs[0]
	default:
		for _, p := range []string{"/shorts/", "/live/", "/embed/"} {
			if strings.HasPrefix(path, p) {
				v.ID = strings.TrimPrefix(path, p)
				break
			}
		}
	}
	if !validID(v.ID) {
		return Video{Seconds: -1}, ErrNoVideo
	}
	if ts, ok := q["t"]; ok {
		if v.Seconds, err = linkTime(ts, q["start"]); err != nil {
			return Video{Seconds: -1}, err
		}
	} else if ss, ok := q["start"]; ok {
		if v.Seconds, err = linkTime(ss, nil); err != nil {
			return Video{Seconds: -1}, err
		}
	}
	if f := strings.TrimSpace(timeField); f != "" {
		s, err := ParseTime(f)
		if err != nil {
			return Video{Seconds: -1}, err
		}
		v.Seconds = s
	}
	return v, nil
}

// hasYouTubeHostPrefix reports whether a link without a scheme starts with one of the hosts and a path.
func hasYouTubeHostPrefix(lower string) bool {
	for h := range youTubeHosts {
		if strings.HasPrefix(lower, h+"/") {
			return true
		}
	}
	return false
}

// linkTime reads a link's one t= (or start=) value; both given must agree.
func linkTime(vals, other []string) (int, error) {
	if len(vals) != 1 || len(other) > 1 {
		return -1, ErrBadTime
	}
	s, err := ParseTime(vals[0])
	if err != nil {
		return -1, err
	}
	if len(other) == 1 {
		if o, err := ParseTime(other[0]); err != nil || o != s {
			return -1, ErrBadTime
		}
	}
	return s, nil
}

// validID reports whether id is a YouTube video id.
func validID(id string) bool {
	if len(id) != 11 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// ParseTime reads a start time: seconds (83, 83s), minutes and seconds (1m23s, 2m), hours, minutes and seconds
// (1h2m3s, 1h), or a clock (1:23, 01:02:03: seconds and the minutes after an hour two digits under 60). At most
// store.MaxVideoSeconds.
func ParseTime(s string) (int, error) {
	if s == "" || len(s) > 16 {
		return -1, ErrBadTime
	}
	var total int
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		if len(parts) > 3 {
			return -1, ErrBadTime
		}
		for i, p := range parts {
			n, ok := digits(p)
			if !ok || (i > 0 && (len(p) != 2 || n >= 60)) {
				return -1, ErrBadTime
			}
			total = total*60 + n
		}
	} else if n, ok := digits(s); ok {
		total = n
	} else {
		rest, seen := s, false
		for _, unit := range []struct {
			c byte
			n int
		}{{'h', 3600}, {'m', 60}, {'s', 1}} {
			i := strings.IndexByte(rest, unit.c)
			if i < 0 {
				continue
			}
			n, ok := digits(rest[:i])
			if !ok {
				return -1, ErrBadTime
			}
			total += n * unit.n
			rest, seen = rest[i+1:], true
		}
		if !seen || rest != "" {
			return -1, ErrBadTime
		}
	}
	if total > store.MaxVideoSeconds {
		return -1, ErrBadTime
	}
	return total, nil
}

// digits reads 1 to 6 decimal digits.
func digits(s string) (int, bool) {
	if s == "" || len(s) > 6 {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// URL is the video's canonical link: https://www.youtube.com/watch?v=<id>, with &t=<s>s when it has a start. The
// pages show it as a plain link (rel="nofollow noopener noreferrer ugc").
func (v Video) URL() string {
	u := "https://www.youtube.com/watch?v=" + v.ID
	if v.Seconds >= 0 {
		u += "&t=" + strconv.Itoa(v.Seconds) + "s"
	}
	return u
}

// Timestamp is the start as a clock ("1:23", "1:02:03"; "" without one): the page's "Watch at 1:23".
func (v Video) Timestamp() string {
	if v.Seconds < 0 {
		return ""
	}
	h, m, s := v.Seconds/3600, v.Seconds/60%60, v.Seconds%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// Stored is the video as the store keeps it.
func (v Video) Stored() store.ClaimVideo { return store.ClaimVideo{VideoID: v.ID, Seconds: v.Seconds} }

// FromStored is a stored video.
func FromStored(c store.ClaimVideo) Video { return Video{ID: c.VideoID, Seconds: c.Seconds} }

// Videos are a claim's videos.
func Videos(c store.Claim) []Video {
	out := make([]Video, len(c.Videos))
	for i, sv := range c.Videos {
		out[i] = FromStored(sv)
	}
	return out
}
