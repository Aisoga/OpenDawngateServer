package dgserver

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/matchmaker"
)

var homeNow = time.Unix(1761400000, 0)

// emptyHomeSource is a server with nothing going on and no matchmaker.
func emptyHomeSource() homeSource {
	return homeSource{now: func() time.Time { return homeNow }, online: func() int { return 0 }, inMatches: func() int { return 0 },
		queue: func() (matchmaker.QueueState, matchmaker.Config, bool) {
			return matchmaker.QueueState{}, matchmaker.Config{}, false
		},
		botsFill: func() bool { return false }, matches: func() []blaze.HostedMatch { return nil },
		customs: func() []blaze.CustomLobby { return nil }, results: func() []blaze.RecentResult { return nil }}
}

func homeJSON(t *testing.T, src homeSource) string {
	t.Helper()
	b, err := json.Marshal(homeStatus(src)())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An empty server: every list empty (not null), no queue, the server's map.
func TestHomeStatusEmpty(t *testing.T) {
	want := `{"version":1,"serverTime":1761400000,"online":{"players":0,"inMatches":0,"matches":0},"queue":null,` +
		`"matches":[],"customGames":[],"results":[],` +
		`"server":{"release":"","map":"The Dawngate","halloween":false,"customMaps":[]}}`
	if got := homeJSON(t, emptyHomeSource()); got != want {
		t.Errorf("home.json\n got %s\nwant %s", got, want)
	}
}

// A busy server: the PvP queue with its start, the running matches (a lobby left out), the open custom games (a
// started one left out), the results; the players' names appear nowhere, a custom game's title does.
func TestHomeStatus(t *testing.T) {
	src := emptyHomeSource()
	src.online, src.inMatches = func() int { return 9 }, func() int { return 4 }
	src.queue = func() (matchmaker.QueueState, matchmaker.Config, bool) {
		return matchmaker.QueueState{Mode: gmshost.ModePvP, Players: 3, Capacity: 10,
				Tickets:  []matchmaker.TicketState{{ID: 1, Players: []matchmaker.Player{{Name: "QueuedSecret"}}}},
				StartsAt: homeNow.Add(41500 * time.Millisecond)},
			matchmaker.Config{TeamSize: 5, MinPlayers: 1, FillAfter: 2 * time.Minute, FillExtend: 30 * time.Second}, true
	}
	src.botsFill = func() bool { return true }
	src.matches = func() []blaze.HostedMatch {
		return []blaze.HostedMatch{
			{GameID: 1, Name: "MatchSecret", Mode: gmshost.ModePvP, Kind: "PvP match", State: "in game", Level: "SM_Map_v5",
				InGameAt: homeNow.Add(-750 * time.Second), Bots: [2]int{2, 3},
				Players: []blaze.HostedMatchPlayer{{Name: "PlayerSecret1", Team: 0}, {Name: "PlayerSecret2", Team: 0},
					{Name: "PlayerSecret3", Team: 0}, {Name: "PlayerSecret4", Team: 1}, {Name: "GoneSecret", Team: 1, Left: true}}},
			{GameID: 2, Name: "LobbySecret", Mode: gmshost.ModeCustom, Kind: "Custom game", State: "pre-game", Lobby: true},
			{GameID: 3, Mode: gmshost.ModePractice, Kind: "Practice match", State: "pre-game", Level: "MM_Map_v3",
				Bots: [2]int{4, 5}, Players: []blaze.HostedMatchPlayer{{Name: "PracticeSecret", Team: 0}}},
		}
	}
	src.customs = func() []blaze.CustomLobby {
		return []blaze.CustomLobby{
			{GameID: 2, Title: `Friday <fun> & "games"`, Level: "MM_Map_v3", Bots: true, Players: 3, Spectators: 1, Seats: 10},
			{GameID: 4, Title: "Started", Started: true, Players: 2, Seats: 4},
			{GameID: 5, Title: "Locked", Level: "SM_Map_v5", Private: true, Players: 1, Seats: 2},
		}
	}
	src.results = func() []blaze.RecentResult {
		return []blaze.RecentResult{
			{GameID: 8, MatchID: 17, Mode: gmshost.ModePvP, Kind: "PvP match", Level: "SM_Map_v5", WinnerTeam: 2, Reason: "surrender",
				Duration: 1501 * time.Second, At: homeNow.Add(-90 * time.Second),
				Teams: [2]blaze.RecentTeam{{Shapers: []string{"Ember", ""}, Bots: 3}, {Shapers: []string{"Fenmore"}, Bots: 4}}},
			{GameID: 7, Mode: gmshost.ModePractice, Kind: "Practice match", Level: "MM_Map_v3", WinnerTeam: 0,
				Duration: 60 * time.Second, At: homeNow.Add(-3600 * time.Second), Teams: [2]blaze.RecentTeam{{Bots: 4}, {Bots: 5}}},
		}
	}
	src.release, src.level, src.customLevels, src.halloween = "v0.9.0", "", []string{"MM_Map_v3"}, true
	want := `{"version":1,"serverTime":1761400000,"online":{"players":9,"inMatches":4,"matches":2},` +
		`"queue":{"players":3,"capacity":10,"teamSize":5,"minPlayers":1,"startsInSec":42,"botsFill":true,"fillAfterSec":120,"fillExtendSec":30},` +
		`"matches":[{"mode":"pvp","kind":"PvP match","map":"The Dawngate","state":"playing","elapsedSec":750,"minutes":12,` +
		`"teams":[{"humans":3,"bots":2},{"humans":1,"bots":3}]},` +
		`{"mode":"practice","kind":"Practice match","map":"Anshavar Pass","state":"starting","elapsedSec":0,"minutes":0,` +
		`"teams":[{"humans":1,"bots":4},{"humans":0,"bots":5}]}],` +
		`"customGames":[{"title":"Friday \u003cfun\u003e \u0026 \"games\"","map":"Anshavar Pass","players":3,"seats":10,` +
		`"spectators":1,"bots":true,"private":false},` +
		`{"title":"Locked","map":"The Dawngate","players":1,"seats":2,"spectators":0,"bots":false,"private":true}],` +
		`"results":[{"matchId":17,"mode":"pvp","kind":"PvP match","map":"The Dawngate","winner":2,"reason":"surrender","durationSec":1501,` +
		`"endedAgoSec":90,"teams":[{"shapers":["Ember",""],"bots":3},{"shapers":["Fenmore"],"bots":4}]},` +
		`{"matchId":0,"mode":"practice","kind":"Practice match","map":"Anshavar Pass","winner":0,"reason":"","durationSec":60,` +
		`"endedAgoSec":3600,"teams":[{"shapers":[],"bots":4},{"shapers":[],"bots":5}]}],` +
		`"server":{"release":"v0.9.0","map":"The Dawngate","halloween":true,"customMaps":["Anshavar Pass"]}}`
	got := homeJSON(t, src)
	if got != want {
		t.Errorf("home.json\n got %s\nwant %s", got, want)
	}
	if strings.Contains(got, "Secret") {
		t.Errorf("a name leaked: %s", got)
	}
	// a queue without a known start, an MM server as its map
	src.queue = func() (matchmaker.QueueState, matchmaker.Config, bool) {
		return matchmaker.QueueState{Players: 0, Capacity: 2}, matchmaker.Config{TeamSize: 1, MinPlayers: 2}, true
	}
	src.level = "MM_Map_v3"
	got = homeJSON(t, src)
	for _, w := range []string{`"queue":{"players":0,"capacity":2,"teamSize":1,"minPlayers":2,"startsInSec":null,`,
		`"server":{"release":"v0.9.0","map":"Anshavar Pass","halloween":true,"customMaps":["Anshavar Pass"]}`} {
		if !strings.Contains(got, w) {
			t.Errorf("home.json lacks %s:\n%s", w, got)
		}
	}
}

// The cache makes the JSON at most once per homeCacheFor, also under concurrent requests (go test -race).
func TestHomeCache(t *testing.T) {
	var made atomic.Int32
	var mu sync.Mutex
	now := homeNow
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	src := emptyHomeSource()
	src.online = func() int { return int(made.Add(1)) }
	c := newHomeCache(homeStatus(src), clock)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); c.JSON() }()
	}
	wg.Wait()
	if n := made.Load(); n != 1 {
		t.Errorf("made %d times for 20 requests at once", n)
	}
	first := string(c.JSON())
	mu.Lock()
	now = now.Add(homeCacheFor - time.Millisecond)
	mu.Unlock()
	if string(c.JSON()) != first || made.Load() != 1 {
		t.Error("remade within homeCacheFor")
	}
	mu.Lock()
	now = now.Add(time.Millisecond)
	mu.Unlock()
	if !strings.Contains(string(c.JSON()), `"players":2`) {
		t.Errorf("not remade after homeCacheFor: %s", c.JSON())
	}
	mu.Lock()
	now = homeNow // a clock that went back
	mu.Unlock()
	if c.JSON(); made.Load() != 3 {
		t.Error("not remade after the clock went back")
	}
}

// checkHomeJSON fetches home.json over c (closed after), as the home page does, and checks the answer: JSON of
// homeVersion, no-store. Then (new connections from the same dial) nothing more.
func checkHomeJSON(t *testing.T, what string, c net.Conn) {
	t.Helper()
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "GET /battlegrounds_test/live/www/client/1/common/home.json HTTP/1.1\r\nHost: x\r\n"+
		"X-Requested-With: XMLHttpRequest\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("home.json %s: %v", what, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var d struct {
		Version    int   `json:"version"`
		ServerTime int64 `json:"serverTime"`
		Server     struct {
			Release string `json:"release"`
		} `json:"server"`
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json; charset=utf-8" ||
		resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("home.json %s: %d, headers %v", what, resp.StatusCode, resp.Header)
	}
	if err := json.Unmarshal(body, &d); err != nil || d.Version != homeVersion || d.ServerTime == 0 || d.Server.Release == "" {
		t.Errorf("home.json %s: %v %+v\n%s", what, err, d, body)
	}
}

// The sample files (the page's states in a browser and in --test-home-json sessions) are home.json as the server
// makes it: every field known, the lists never null.
func TestHomeSamples(t *testing.T) {
	for _, name := range []string{"quiet", "queue", "lobby"} {
		b, err := os.ReadFile("../../testdata/home/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		var d homeData
		if err := dec.Decode(&d); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if d.Version != homeVersion || d.Matches == nil || d.CustomGames == nil || d.Results == nil || d.Server.CustomMaps == nil {
			t.Errorf("%s: %+v", name, d)
		}
	}
}

// The test aid --test-home-json answers with the file as it is at each request, and with the live data without it.
func TestTestHomeFile(t *testing.T) {
	path := t.TempDir() + "/home.json"
	h := testHomeFile(path, func() []byte { return []byte(`{"live":1}`) })
	if got := string(h()); got != `{"live":1}` {
		t.Fatalf("no file: %s", got)
	}
	for _, body := range []string{`{"version":1,"a":1}`, `{"version":1,"a":2}`} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := string(h()); got != body {
			t.Fatalf("file: %s, want %s", got, body)
		}
	}
}
