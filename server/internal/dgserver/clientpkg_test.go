package dgserver

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/update/updatetest"
)

func fakeGitHubRelease(t *testing.T, tag string) *updatetest.FakeGitHub {
	t.Helper()
	f := updatetest.NewFakeGitHub()
	t.Cleanup(f.Close)
	z := updatetest.FakeRelease{Tag: tag, Files: map[string]string{"Start-Client.exe": "client", "Start-Server.exe": "server",
		"README.txt": "readme"}}.Zip(nil)
	h := sha256.Sum256(z)
	f.Tag, f.ZipBytes, f.Digest = tag, z, "sha256:"+hex.EncodeToString(h[:])
	return f
}

func pkgGet(t *testing.T, c *http.Client, u string) (int, []byte, http.Header) {
	t.Helper()
	res, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b, res.Header
}

// dgserver --client-package of a release: the HTTP port and the website serve the package with the ini for this
// server (its address, its tunnel port, its key), the website has the Download page and link, the HTTP port's page
// goes there; without the website the HTTP port's own pages link it.
func TestClientPackageServed(t *testing.T) {
	old := packageVersion
	packageVersion = func() string { return "v0.7" }
	defer func() { packageVersion = old }()
	f := fakeGitHubRelease(t, "v0.7")
	cfg := config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), dataDir: t.TempDir(), tunnel: TunnelOn,
		accounts:      accounts.Policy{Mode: accounts.Open},
		clientPackage: clientPackageConfig{on: true, name: "Test Server", source: f.URL}}
	in := startWeb(t, &cfg)
	defer in.stop()
	info := in.info(cfg)
	path := "/dawngate/download/OpenDawngate-Test-Server.zip"
	if info.ClientPackage != path || info.ClientPackageMissing != "" {
		t.Fatalf("info %q %q", info.ClientPackage, info.ClientPackageMissing)
	}
	c := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // the certificate isn't this test's point
	hp := "http://127.0.0.1:" + strconv.Itoa(in.ports[3])
	code, body, hdr := pkgGet(t, c, hp+path)
	if code != 200 || hdr.Get("Content-Disposition") != "attachment; filename=OpenDawngate-Test-Server.zip" ||
		hdr.Get("Content-Type") != "application/zip" {
		t.Fatalf("the HTTP port: %d %v %q", code, hdr, body)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var ini string
	for _, e := range zr.File {
		if e.Name == "v0.7/Start-Client.ini" {
			r, _ := e.Open()
			b, _ := io.ReadAll(r)
			r.Close()
			ini = string(b)
		}
	}
	pin := in.gateway.Identity.Pin.String()
	if !strings.Contains(ini, "server = 127.0.0.1\r\n") || !strings.Contains(ini, "gateway_pin = "+pin+"\r\n") ||
		!strings.Contains(ini, "gateway_port = "+strconv.Itoa(in.gateway.Port())+"\r\n") {
		t.Errorf("the ini:\n%s", ini)
	}
	// the website: the same file, the Download page with the key's fingerprint, the link on its pages
	base := info.WebURL
	if code, b2, _ := pkgGet(t, c, base+strings.TrimPrefix(path, "/")); code != 200 || !bytes.Equal(b2, body) {
		t.Errorf("the website's file: %d, %d bytes", code, len(b2))
	}
	code, page, _ := pkgGet(t, c, base+"download")
	if code != 200 || !strings.Contains(string(page), `href="`+path+`"`) ||
		!strings.Contains(string(page), in.gateway.Identity.Pin.Fingerprint()) || !strings.Contains(string(page), "Unzip") {
		t.Errorf("the website's page: %d\n%s", code, page)
	}
	if _, idx, _ := pkgGet(t, c, base); !strings.Contains(string(idx), `<a href="/download">Download</a>`) {
		t.Errorf("no Download link on the website's start page")
	}
	if code, _, hdr := pkgGet(t, c, hp+"/dawngate/download"); code != 302 || hdr.Get("Location") != base+"download" {
		t.Errorf("the HTTP port's page with the website: %d %s", code, hdr.Get("Location"))
	}
	if !strings.Contains(f.Requests[0], "/releases/tags/v0.7") {
		t.Errorf("GitHub asked %q", f.Requests)
	}

	// without the website: the HTTP port's own Download page and links
	cfg2 := config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), dataDir: t.TempDir(), tunnel: TunnelOn,
		accounts:      accounts.Policy{Mode: accounts.Open},
		clientPackage: clientPackageConfig{on: true, source: f.URL}}
	in2, err := start(cfg2)
	if err != nil {
		t.Fatal(err)
	}
	defer in2.stop()
	hp2 := "http://127.0.0.1:" + strconv.Itoa(in2.ports[3])
	code, page, _ = pkgGet(t, c, hp2+"/dawngate/download")
	if code != 200 || !strings.Contains(string(page), `href="/dawngate/download/OpenDawngate-127.0.0.1.zip"`) ||
		!strings.Contains(string(page), in2.gateway.Identity.Pin.Fingerprint()) {
		t.Errorf("the HTTP port's page: %d\n%s", code, page)
	}
	if _, idx, _ := pkgGet(t, c, hp2+"/dawngate/"); !strings.Contains(string(idx), `<a href="/dawngate/download">Download</a>`) {
		t.Errorf("no Download link on the HTTP port's start page:\n%s", idx)
	}
	if code, b3, _ := pkgGet(t, c, hp2+"/dawngate/download/OpenDawngate-127.0.0.1.zip"); code != 200 || len(b3) == 0 {
		t.Errorf("the HTTP port's file without the website: %d", code)
	}
}

// A developer build has no package: 404 and pages that say so; --client-package needs the tunnel and a data folder.
func TestClientPackageDevBuildAndFlags(t *testing.T) {
	f := fakeGitHubRelease(t, "v0.7")
	cfg := config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), dataDir: t.TempDir(), tunnel: TunnelOn,
		accounts:      accounts.Policy{Mode: accounts.Open},
		clientPackage: clientPackageConfig{on: true, source: f.URL}}
	in, err := start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer in.stop()
	if info := in.info(cfg); !strings.Contains(info.ClientPackageMissing, "developer build") {
		t.Errorf("info %+v", info)
	}
	hp := "http://127.0.0.1:" + strconv.Itoa(in.ports[3])
	c := &http.Client{Timeout: 10 * time.Second}
	if code, b, _ := pkgGet(t, c, hp+"/dawngate/download/OpenDawngate-127.0.0.1.zip"); code != 404 || !strings.Contains(string(b), "developer build") {
		t.Errorf("the file: %d %s", code, b)
	}
	if code, b, _ := pkgGet(t, c, hp+"/dawngate/download"); code != 200 || !strings.Contains(string(b), "no client package") {
		t.Errorf("the page: %d %s", code, b)
	}
	if f.Count() != 0 {
		t.Errorf("GitHub asked: %q", f.Requests)
	}
	for _, bad := range []struct {
		cfg  config
		want string
	}{
		{config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), dataDir: t.TempDir(), tunnel: TunnelOff,
			clientPackage: clientPackageConfig{on: true}}, "needs the tunnel"},
		{config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), tunnel: TunnelOn,
			clientPackage: clientPackageConfig{on: true}}, "needs --data"},
		{config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), dataDir: t.TempDir(), tunnel: TunnelOn,
			clientPackage: clientPackageConfig{on: true, source: "https://api.github.com"}}, "loopback"},
	} {
		if in, err := start(bad.cfg); err == nil || !strings.Contains(err.Error(), bad.want) {
			if in != nil {
				in.stop()
			}
			t.Errorf("want %q: %v", bad.want, err)
		}
	}
}
