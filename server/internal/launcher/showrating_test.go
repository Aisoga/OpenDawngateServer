package launcher

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dawngate/server/internal/dgserver"
)

// Start-Server.ini's rating keys rating_leaderboard and show_rating: in the template, on, and known, passed as
// --rating-leaderboard and --show-rating, which dgserver accepts (and refuses a bad value); none from a file without
// them (dgserver's defaults, on).
func TestRatingKeys(t *testing.T) {
	cfg, _, err := LoadOrCreate(filepath.Join(t.TempDir(), ServerConfigName), ServerConfigText)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"rating_leaderboard", "show_rating"} {
		if !slices.Contains(serverKeys, k) {
			t.Errorf("%s isn't a known key", k)
		}
	}
	if got := strings.Join(RatingFlags(cfg), " "); got != "--rating-leaderboard=on --show-rating=on" {
		t.Errorf("the template's flags: %s", got)
	}
	for _, k := range []string{"rating_leaderboard", "show_rating"} {
		for _, v := range []string{"off", "on"} {
			if code := dgserver.Run(append(RatingFlags(Config{k: v}), "--print-ports"), dgserver.Hooks{}); code != 0 {
				t.Errorf("%s = %s: exit %d", k, v, code)
			}
		}
		if code := dgserver.Run(append(RatingFlags(Config{k: "maybe"}), "--print-ports"), dgserver.Hooks{}); code != 2 {
			t.Errorf("%s = maybe: exit %d", k, code)
		}
	}
	if f := RatingFlags(Config{"address": "127.0.0.1"}); len(f) != 0 {
		t.Errorf("an older file: %v", f)
	}
}
