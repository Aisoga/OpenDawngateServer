package launcher

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dawngate/server/internal/dgserver"
)

// Start-Server.ini's issues key: in the template, off, and known, passed as --issues, which dgserver accepts (and
// refuses a bad value); none from a file without it (dgserver's default, off).
func TestIssuesKey(t *testing.T) {
	cfg, _, err := LoadOrCreate(filepath.Join(t.TempDir(), ServerConfigName), ServerConfigText)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(serverKeys, "issues") {
		t.Error("issues isn't a known key")
	}
	if got := strings.Join(IssuesFlags(cfg), " "); got != "--issues=off" {
		t.Errorf("the template's flag: %s", got)
	}
	for _, v := range []string{"off", "on"} {
		if code := dgserver.Run(append(IssuesFlags(Config{"issues": v}), "--print-ports"), dgserver.Hooks{}); code != 0 {
			t.Errorf("issues = %s: exit %d", v, code)
		}
	}
	if code := dgserver.Run(append(IssuesFlags(Config{"issues": "maybe"}), "--print-ports"), dgserver.Hooks{}); code != 2 {
		t.Errorf("issues = maybe: exit %d", code)
	}
	if f := IssuesFlags(Config{"address": "127.0.0.1"}); len(f) != 0 {
		t.Errorf("an older file: %v", f)
	}
}
