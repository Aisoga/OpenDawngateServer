package issues

import (
	"errors"
	"testing"
)

const vid = "dQw4w9WgXcQ"

func TestParseLink(t *testing.T) {
	ok := []struct {
		link, time string
		seconds    int
	}{
		{"https://www.youtube.com/watch?v=" + vid, "", -1},
		{"https://youtube.com/watch?v=" + vid, "", -1},
		{"https://m.youtube.com/watch?v=" + vid, "", -1},
		{"http://www.youtube.com/watch?v=" + vid, "", -1}, // http: only the id is kept
		{"HTTPS://WWW.YouTube.com/watch?v=" + vid, "", -1},
		{"  https://youtu.be/" + vid + "  ", "", -1},
		{"https://youtu.be/" + vid + "?si=AbCdEf&t=83", "", 83},
		{"https://www.youtube.com/shorts/" + vid, "", -1},
		{"https://www.youtube.com/live/" + vid + "?feature=share", "", -1},
		{"https://www.youtube.com/embed/" + vid + "?start=90", "", 90},
		{"https://www.youtube.com/watch?v=" + vid + "&t=83s", "", 83},
		{"https://www.youtube.com/watch?v=" + vid + "&t=1m23s", "", 83},
		{"https://www.youtube.com/watch?v=" + vid + "&t=1h2m3s", "", 3723},
		{"https://www.youtube.com/watch?v=" + vid + "&t=2m", "", 120},
		{"https://www.youtube.com/watch?v=" + vid + "&t=1h", "", 3600},
		{"https://www.youtube.com/watch?v=" + vid + "&t=0", "", 0},
		{"https://www.youtube.com/watch?v=" + vid + "&t=5&start=5", "", 5},
		{"https://www.youtube.com/watch?list=PL1&v=" + vid + "&index=3#comments", "", -1},
		{"https://www.youtube.com/watch?v=" + vid + "&t=10", "1:23", 83}, // the field wins
		{"https://www.youtube.com/watch?v=" + vid, "1:02:03", 3723},
		{"https://www.youtube.com/watch?v=" + vid, "83", 83},
		{"https://www.youtube.com/watch?v=" + vid, "1m23s", 83},
		{"https://www.youtube.com/watch?v=" + vid, "0:00", 0},
		{"https://www.youtube.com/watch?v=" + vid, "  ", -1},
		{"youtu.be/" + vid, "", -1}, // no scheme
		{"www.youtube.com/watch?v=" + vid, "", -1},
		{"https://www.youtube.com/watch?v=a-b_C0123zZ", "", -1},
	}
	for _, c := range ok {
		v, err := ParseLink(c.link, c.time)
		if err != nil || v.Seconds != c.seconds || (v.ID != vid && v.ID != "a-b_C0123zZ") {
			t.Errorf("%q %q: %+v %v", c.link, c.time, v, err)
		}
	}

	bad := []struct {
		link, time string
		want       error
	}{
		{"", "", ErrNoVideo},
		{"javascript:alert(1)", "", ErrNotYouTube},
		{"JavaScript://www.youtube.com/watch?v=" + vid, "", ErrNotYouTube},
		{"data:text/html,<b>x</b>", "", ErrNotYouTube},
		{"ftp://www.youtube.com/watch?v=" + vid, "", ErrNotYouTube},
		{"https://vimeo.com/12345", "", ErrNotYouTube},
		{"https://youtube.com.evil.example/watch?v=" + vid, "", ErrNotYouTube},
		{"https://evil.example/?u=https://youtube.com/watch?v=" + vid, "", ErrNotYouTube},
		{"https://www.youtube.com.:443/watch?v=" + vid, "", ErrNotYouTube},
		{"https://www.youtube.com./watch?v=" + vid, "", ErrNotYouTube},
		{"https://music.youtube.com/watch?v=" + vid, "", ErrNotYouTube},
		{"https://www.youtube-nocookie.com/embed/" + vid, "", ErrNotYouTube},
		{"https://user:pw@www.youtube.com/watch?v=" + vid, "", ErrNotYouTube},
		{"https://www.youtube.com@evil.example/watch?v=" + vid, "", ErrNotYouTube},
		{"https://www.youtube.com:443/watch?v=" + vid, "", ErrNotYouTube},
		{"https://www.youtube.com:/watch?v=" + vid, "", ErrNotYouTube},
		{"//www.youtube.com/watch?v=" + vid, "", ErrNotYouTube},
		{"https://www.youtube.com/watch?v=" + vid[:10], "", ErrNoVideo},
		{"https://www.youtube.com/watch?v=" + vid + "x", "", ErrNoVideo},
		{"https://www.youtube.com/watch?v=dQw4w9WgXc!", "", ErrNoVideo},
		{"https://www.youtube.com/watch?v=dQw4w9WgXc%51", "", ErrNoVideo},
		{"https://www.youtube.com/watch?%76=" + vid, "", ErrNoVideo},
		{"https://www.youtube.com/watch?v=" + vid + "&t=1%6D", "", ErrNoVideo},
		{"https://www.youtube.com/watch?v=" + vid + "&v=" + vid, "", ErrNoVideo},
		{"https://www.youtube.com/watch", "", ErrNoVideo},
		{"https://www.youtube.com/watch/?v=" + vid, "", ErrNoVideo},
		{"https://www.youtube.com/channel/UC123", "", ErrNoVideo},
		{"https://www.youtube.com/playlist?list=PL1", "", ErrNoVideo},
		{"https://www.youtube.com/shorts/" + vid + "/", "", ErrNoVideo},
		{"https://www.youtube.com/shorts/dQw4w9WgX%63Q", "", ErrNoVideo},
		{"https://www.youtube.com/" + vid, "", ErrNoVideo},
		{"https://youtu.be/", "", ErrNoVideo},
		{"https://youtu.be/" + vid + "/x", "", ErrNoVideo},
		{"https://www.youtube.com/watch?v=" + vid + " onclick=x", "", ErrNoVideo},
		{"https://www.youtube.com/watch?v=" + vid + "\"><script>", "", ErrNoVideo},
		{"https://www.youtube.com\\@evil.example/watch?v=" + vid, "", ErrNoVideo},
		{"https://www.yоutube.com/watch?v=" + vid, "", ErrNoVideo}, // a Cyrillic o
		{"https://www.youtube.com/watch?v=" + vid + "\n", "", nil},
		{"https://www.youtube.com/watch?v=" + vid + "&t=abc", "", ErrBadTime},
		{"https://www.youtube.com/watch?v=" + vid + "&t=1m23", "", ErrBadTime},
		{"https://www.youtube.com/watch?v=" + vid + "&t=3s2m", "", ErrBadTime},
		{"https://www.youtube.com/watch?v=" + vid + "&t=-5", "", ErrBadTime},
		{"https://www.youtube.com/watch?v=" + vid + "&t=5&t=6", "", ErrBadTime},
		{"https://www.youtube.com/watch?v=" + vid + "&t=5&start=6", "", ErrBadTime},
		{"https://www.youtube.com/watch?v=" + vid + "&t=9999999", "", ErrBadTime},
		{"https://www.youtube.com/watch?v=" + vid, "1:5", ErrBadTime},
		{"https://www.youtube.com/watch?v=" + vid, "1:60", ErrBadTime},
		{"https://www.youtube.com/watch?v=" + vid, "1:2:03", ErrBadTime},
		{"https://www.youtube.com/watch?v=" + vid, "1:02:03:04", ErrBadTime},
		{"https://www.youtube.com/watch?v=" + vid, ":30", ErrBadTime},
		{"https://www.youtube.com/watch?v=" + vid, "1.5", ErrBadTime},
		{"https://www.youtube.com/watch?v=" + vid, "200:00:00", ErrBadTime},
	}
	for _, c := range bad {
		if c.want == nil { // a trailing newline is trimmed like spaces
			if _, err := ParseLink(c.link, c.time); err != nil {
				t.Errorf("%q: %v", c.link, err)
			}
			continue
		}
		v, err := ParseLink(c.link, c.time)
		if !errors.Is(err, c.want) {
			t.Errorf("%q %q: %+v %v, want %v", c.link, c.time, v, err, c.want)
		}
		if err != nil && (v.ID != "" || v.Seconds != -1) {
			t.Errorf("%q: a refused link gives %+v", c.link, v)
		}
	}
	long := "https://www.youtube.com/watch?v=" + vid + "&x="
	for len(long) <= maxLinkLength {
		long += "a"
	}
	if _, err := ParseLink(long, ""); err == nil {
		t.Error("a link over the length taken")
	}
}

func TestVideoURL(t *testing.T) {
	for _, c := range []struct {
		v        Video
		url, ts  string
		fromLink string
	}{
		{Video{vid, -1}, "https://www.youtube.com/watch?v=" + vid, "", "http://youtu.be/" + vid},
		{Video{vid, 83}, "https://www.youtube.com/watch?v=" + vid + "&t=83s", "1:23", "https://m.youtube.com/shorts/" + vid + "?t=1m23s"},
		{Video{vid, 0}, "https://www.youtube.com/watch?v=" + vid + "&t=0s", "0:00", "https://youtube.com/live/" + vid + "?t=0s"},
		{Video{vid, 3723}, "https://www.youtube.com/watch?v=" + vid + "&t=3723s", "1:02:03", "youtu.be/" + vid + "?t=1h2m3s"},
	} {
		if c.v.URL() != c.url || c.v.Timestamp() != c.ts {
			t.Errorf("%+v: %q %q", c.v, c.v.URL(), c.v.Timestamp())
		}
		if got, err := ParseLink(c.fromLink, ""); err != nil || got != c.v {
			t.Errorf("%q: %+v %v", c.fromLink, got, err)
		}
		// the canonical link parses back to the same video
		if back, err := ParseLink(c.v.URL(), ""); err != nil || back != c.v {
			t.Errorf("%q: %+v %v", c.v.URL(), back, err)
		}
		if FromStored(c.v.Stored()) != c.v {
			t.Errorf("stored %+v", c.v.Stored())
		}
	}
}
