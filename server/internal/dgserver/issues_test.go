package dgserver

import (
	"crypto/tls"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/issues"
	"dawngate/server/internal/store"
)

// TestWebsiteIssues runs dgserver with the website and --issues on: the list of tracked guesses is synced into the
// store at the start, /issues asks for a login, and the start-up line says so; off, nothing is synced and there are no
// pages. The flag takes on and off only.
func TestWebsiteIssues(t *testing.T) {
	entries, err := issues.Entries()
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, on := range []bool{true, false} {
		logs := t.TempDir()
		cfg := config{bind: "127.0.0.1", certs: testCerts(t), logDir: logs, dataDir: t.TempDir(),
			accounts: accounts.Policy{Mode: accounts.Open}, issues: on}
		in := startWeb(t, &cfg)
		rows, err := in.store.ListIssues(store.IssueFilter{})
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if on {
			want = len(entries)
		}
		if len(rows) != want {
			t.Errorf("issues %v: %d issues stored, want %d", on, len(rows), want)
		}
		res, err := c.Get("https://127.0.0.1:" + strconv.Itoa(cfg.web.port) + "/issues")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if on && (res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/issues/login?next=%2Fissues") {
			t.Errorf("/issues: %d %q", res.StatusCode, res.Header.Get("Location"))
		}
		if !on && res.StatusCode != http.StatusNotFound {
			t.Errorf("/issues while off: %d", res.StatusCode)
		}
		// the API: a token needed while on, none while off
		res, err = c.Get("https://127.0.0.1:" + strconv.Itoa(cfg.web.port) + "/api/issues")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if want := map[bool]int{true: http.StatusUnauthorized, false: http.StatusNotFound}[on]; res.StatusCode != want {
			t.Errorf("issues %v: /api/issues %d, want %d", on, res.StatusCode, want)
		}
		in.stop()
		var log []byte
		filepath.WalkDir(logs, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				b, _ := os.ReadFile(p)
				log = append(log, b...)
			}
			return nil
		})
		line := "issue tracker (M22): off"
		if on {
			line = "issue tracker (M22): on, " + strconv.Itoa(len(entries)) + " tracked guesses"
		}
		if !strings.Contains(string(log), line) || (on && !strings.Contains(string(log), "the issue tracker at https://")) {
			t.Errorf("issues %v: the log lacks %q", on, line)
		}
	}
	for v, code := range map[string]int{"on": 0, "off": 0, "maybe": 2} {
		if got := Main([]string{"--issues", v, "--print-ports"}); got != code {
			t.Errorf("--issues %s: exit %d, want %d", v, got, code)
		}
	}
}
