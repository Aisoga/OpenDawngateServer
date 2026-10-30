package launcher

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dawngate/server/internal/dgserver"
)

// Start-Server.ini's match_record key: in the template, off, and known, passed as --match-record, which dgserver
// accepts (and refuses a bad value); none from a file without it (dgserver's default, off).
func TestMatchRecordKey(t *testing.T) {
	cfg, _, err := LoadOrCreate(filepath.Join(t.TempDir(), ServerConfigName), ServerConfigText)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(serverKeys, "match_record") {
		t.Error("match_record isn't a known key")
	}
	if got := strings.Join(MatchRecordFlags(cfg), " "); got != "--match-record=off" {
		t.Errorf("the template's flag: %s", got)
	}
	for _, v := range []string{"off", "on"} {
		if code := dgserver.Run(append(MatchRecordFlags(Config{"match_record": v}), "--print-ports"), dgserver.Hooks{}); code != 0 {
			t.Errorf("match_record = %s: exit %d", v, code)
		}
	}
	if code := dgserver.Run(append(MatchRecordFlags(Config{"match_record": "maybe"}), "--print-ports"), dgserver.Hooks{}); code != 2 {
		t.Errorf("match_record = maybe: exit %d", code)
	}
	if f := MatchRecordFlags(Config{"address": "127.0.0.1"}); len(f) != 0 {
		t.Errorf("an older file: %v", f)
	}
}
