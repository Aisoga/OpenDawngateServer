package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Repo is the project's public repository, whose releases Start-Client offers (built in; not a setting).
const Repo = "Aisoga/OpenDawngateServer"

// ReleasesPage is the page where a player finds every release (the hint after a failed update).
const ReleasesPage = "https://github.com/" + Repo + "/releases/latest"

// MaxZip bounds a release zip's download (today's are about 20 MB).
const MaxZip = 512 << 20

// Source is where the latest release is asked for and its zip downloaded: GitHub, or a fake on a loopback address
// in tests.
type Source struct {
	API       string        // the API's base: https://api.github.com
	Downloads string        // every asset's URL must start with this: https://github.com/<Repo>/releases/download/
	AskTime   time.Duration // the question's timeout (no answer: Start-Client goes on)
	UserAgent string        // GitHub's API wants one
	plainHTTP bool          // a loopback fake (tests): http allowed
}

// GitHub is the real source.
func GitHub(userAgent string) Source {
	return Source{API: "https://api.github.com", Downloads: "https://github.com/" + Repo + "/releases/download/",
		AskTime: 5 * time.Second, UserAgent: userAgent}
}

// Loopback is a fake of GitHub's API and downloads at base (http://127.x.y.z:port; tests only): the API under
// base/repos/<Repo>/releases/latest, the assets anywhere under base. Any other address is refused, so the test
// hook can never send Start-Client somewhere else on the network.
func Loopback(base, userAgent string) (Source, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return Source{}, fmt.Errorf("%q: not a http://<loopback address>:<port> URL", base)
	}
	if ip := net.ParseIP(u.Hostname()); ip == nil || !ip.IsLoopback() {
		return Source{}, fmt.Errorf("%q: only a loopback address (127.x.y.z) is allowed", base)
	}
	b := u.Scheme + "://" + u.Host
	return Source{API: b, Downloads: b + "/", AskTime: 5 * time.Second, UserAgent: userAgent, plainHTTP: true}, nil
}

// Release is the latest release and its Windows zip.
type Release struct {
	Tag string
	Zip Asset
}

// Asset is one file of a release.
type Asset struct {
	Name   string
	URL    string
	Size   int64
	SHA256 string // GitHub's digest of the file (lower-case hex), when it gives one
}

// LatestURL is the API request for the latest release (GitHub's "latest" skips drafts and pre-releases, so the
// nightly is never offered).
func (s Source) LatestURL() string {
	return strings.TrimRight(s.API, "/") + "/repos/" + Repo + "/releases/latest"
}

func (s Source) client(timeout time.Duration) *http.Client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
	return &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" && !s.plainHTTP {
			return fmt.Errorf("redirected to %s, not https", req.URL.Redacted())
		}
		return nil
	}}
}

func (s Source) get(ctx context.Context, cl *http.Client, u, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if s.UserAgent != "" {
		req.Header.Set("User-Agent", s.UserAgent)
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	return resp, nil
}

// Latest asks for the latest release: its tag, which must be a numbered release's name, and its asset <tag>.zip.
func (s Source) Latest(ctx context.Context) (Release, error) {
	resp, err := s.get(ctx, s.client(s.AskTime), s.LatestURL(), "application/vnd.github+json")
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	var r struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return Release{}, fmt.Errorf("the latest release's answer: %v", err)
	}
	if r.Draft || r.Prerelease {
		return Release{}, fmt.Errorf("the latest release %q is a draft or a pre-release", r.Tag)
	}
	if !IsRelease(r.Tag) {
		return Release{}, fmt.Errorf("the latest release %q is not named v<major>.<minor>[.<patch>]", r.Tag)
	}
	rel := Release{Tag: r.Tag}
	for _, a := range r.Assets {
		if a.Name != r.Tag+".zip" {
			continue
		}
		rel.Zip = Asset{Name: a.Name, URL: a.URL, Size: a.Size}
		if h, ok := strings.CutPrefix(a.Digest, "sha256:"); ok && len(h) == 64 {
			if _, err := hex.DecodeString(h); err == nil {
				rel.Zip.SHA256 = strings.ToLower(h)
			}
		}
		return rel, nil
	}
	return Release{Tag: r.Tag}, fmt.Errorf("%w: the release %s has no %s.zip", ErrNoZip, r.Tag, r.Tag)
}

// ErrNoZip: the latest release has no <tag>.zip (the hand-made releases before v0.6 named their zips otherwise), so
// there is nothing Start-Client can install: no update.
var ErrNoZip = errors.New("no zip to update from")

// Download writes the asset to dest (through dest.part, renamed when complete): only from the source's download
// address, at most MaxZip bytes, the size GitHub named and its SHA-256 when GitHub gave one. progress (may be nil)
// gets the bytes so far.
func (s Source) Download(ctx context.Context, a Asset, dest string, progress func(done int64)) error {
	if !strings.HasPrefix(a.URL, s.Downloads) {
		return fmt.Errorf("the asset's address %q is not the project's (%s...)", a.URL, s.Downloads)
	}
	if a.Size > MaxZip {
		return fmt.Errorf("%s is %d bytes, more than a release can be (%d)", a.Name, a.Size, MaxZip)
	}
	resp, err := s.get(ctx, s.client(0), a.URL, "application/octet-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	part := dest + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	var done int64
	count := progressWriter(func(k int64) {
		if done += k; progress != nil {
			progress(done)
		}
	})
	n, err := io.Copy(io.MultiWriter(f, h, count), io.LimitReader(resp.Body, MaxZip+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		switch sum := hex.EncodeToString(h.Sum(nil)); {
		case n > MaxZip:
			err = fmt.Errorf("%s: more than %d bytes", a.Name, MaxZip)
		case a.Size > 0 && n != a.Size:
			err = fmt.Errorf("%s: %d bytes arrived, the release has %d", a.Name, n, a.Size)
		case a.SHA256 != "" && sum != a.SHA256:
			err = fmt.Errorf("%s: SHA-256 %s, the release's is %s", a.Name, sum, a.SHA256)
		}
	}
	if err != nil {
		os.Remove(part)
		return err
	}
	os.Remove(dest)
	return os.Rename(part, dest)
}

type progressWriter func(int64)

func (p progressWriter) Write(b []byte) (int, error) {
	p(int64(len(b)))
	return len(b), nil
}
