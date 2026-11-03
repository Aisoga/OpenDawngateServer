package dgserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/rating"
	"dawngate/server/internal/store"
)

// runBriefly starts dgserver with args on a data folder, stops it once it has started and returns its log.
func runBriefly(t *testing.T, data string, args ...string) string {
	t.Helper()
	stop, started, done := make(chan struct{}), make(chan Info, 1), make(chan int, 1)
	go func() {
		done <- Run(append([]string{"--bind", "127.0.9.73", "--certs", testCerts(t), "--log", t.TempDir(), "--data", data,
			"--broadcast-file", "", "--plain", "off", "--tunnel", "on", "--gateway-port", "0"}, args...),
			Hooks{Stop: stop, Quiet: true, Started: func(i Info) { started <- i }, StartFailed: func(error) { started <- Info{} }})
	}()
	info := <-started
	if info.Log == "" {
		t.Fatal("dgserver didn't start")
	}
	close(stop)
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("dgserver didn't stop")
	}
	b, err := os.ReadFile(info.Log)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The skill rating's seed at the start (RATING1): the first start replays the stored matches once (here
// --test-history-seed's made-up ones, which come first), the next start finds the mark; the Leaderboard's line names
// the ranking, by rating by default, by wins with --rating-leaderboard off; the profile box line says whether players
// see their rating (--show-rating).
func TestRatingSeedAtStart(t *testing.T) {
	data := t.TempDir()
	log := runBriefly(t, data, "--test-history-seed", "60:rseed@x.io")
	if !strings.Contains(log, "skill rating (RATING1): 60 stored matches replayed, ") ||
		!strings.Contains(log, "leaderboard: ranked by skill rating, at least 10 rated games (balanced PvP and custom games) (--rating-leaderboard on") ||
		!strings.Contains(log, "profile box: each player's skill rating shown from 10 rated games (--show-rating on)") {
		t.Fatalf("first start:\n%s", log)
	}
	st, err := store.Open(filepath.Join(data, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	mark, _, found, err := st.Setting(rating.SeedKey)
	_, p, _ := st.PersonaByName("rseed")
	rows, _ := st.RatingLeaderboard("", 1, 200)
	st.Close()
	if err != nil || !found || !strings.Contains(log, mark) {
		t.Errorf("the mark %q %v %v", mark, found, err)
	}
	ranked := false
	for _, r := range rows {
		ranked = ranked || r.PersonaID == p.ID
	}
	if len(rows) == 0 || !ranked {
		t.Errorf("the seeded ranking: %+v (the login's persona %d)", rows, p.ID)
	}
	log = runBriefly(t, data, "--rating-leaderboard", "off", "--show-rating", "off")
	if !strings.Contains(log, "skill rating (RATING1): seeded before ("+mark+")") ||
		!strings.Contains(log, "leaderboard: ranked by wins, at least 10 PvP and custom games (--rating-leaderboard off") ||
		!strings.Contains(log, "profile box: no rating shown (--show-rating off)") {
		t.Errorf("second start:\n%s", log)
	}
}
