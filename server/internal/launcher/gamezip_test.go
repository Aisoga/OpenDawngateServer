package launcher

import (
	"archive/zip"
	"bufio"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dawngate/server/internal/gamedl"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/archive"
	"dawngate/server/internal/setup/synthzip"
)

// The download's tests never reach the internet: every source is a local test server (never gamedl.DefaultSource),
// the game is synthzip's synthetic one, and the user's Downloads folder is a temp folder.

// zipServer serves body at /Dawngate.zip and counts the requests.
func zipServer(t *testing.T, body []byte) (string, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		http.ServeContent(w, r, archive.GameZipName, time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(s.Close)
	return s.URL + "/" + archive.GameZipName, &n
}

// gameZip is the synthetic Dawngate.zip's bytes; its manifest is the setup's for the test.
func gameZip(t *testing.T) []byte {
	t.Helper()
	p, m := synthzip.Zip(t, t.TempDir())
	manifest = m
	t.Cleanup(func() { manifest = nil })
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// otherZip is a zip of one Dawngate/ folder whose files aren't the build's.
func otherZip(t *testing.T) []byte {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, _ := w.Create("Dawngate/game/Dawngate.exe")
	f.Write([]byte("MZ another game"))
	w.Close()
	return b.Bytes()
}

// noDownloads keeps the user's real Downloads folder out of ZipCandidates, and gives the download room.
func noDownloads(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	orig := gameFree
	gameFree = func(string) (uint64, error) { return 100 << 30, nil }
	t.Cleanup(func() { gameFree = orig })
}

func finder(t *testing.T, input string, interactive bool, gs gameSources) (string, error, string, string) {
	t.Helper()
	dir := t.TempDir()
	var out bytes.Buffer
	o := IO{In: strings.NewReader(input), Out: &out, Interactive: interactive, ExeDir: dir}
	p, err := zipFinder(o, bufio.NewReader(o.In), "", gs)()
	return p, err, out.String(), dir
}

func TestGameSourcesSetting(t *testing.T) {
	gs, err := newGameSources("", "https://a.example/Dawngate.zip, http://b.example:8080/x.zip", 7)
	if err != nil || gs.named || gs.need != 7 || strings.Join(gs.list, " ") !=
		gamedl.DefaultSource+" https://a.example/Dawngate.zip http://b.example:8080/x.zip" {
		t.Errorf("the ini's mirrors after the default: %+v %v", gs, err)
	}
	gs, err = newGameSources("http://127.0.0.1:9/Dawngate.zip", "https://a.example/Dawngate.zip", 0)
	if err != nil || !gs.named || strings.Join(gs.list, " ") != "http://127.0.0.1:9/Dawngate.zip" {
		t.Errorf("the command line's sources alone: %+v %v", gs, err)
	}
	if _, err := newGameSources("", "a.example/Dawngate.zip", 0); err == nil || !strings.Contains(err.Error(), GameSourcesKey) {
		t.Errorf("a bad mirror: %v", err)
	}
	if _, err := newGameSources(`D:\Dawngate.zip`, "", 0); err == nil || !strings.Contains(err.Error(), "--"+GameSourceFlag) {
		t.Errorf("a bad --game-source: %v", err)
	}
	if h := sourceHost(gamedl.DefaultSource); h != "archive.org" {
		t.Errorf("the default's name %q", h)
	}
}

// The player's yes (Enter): the download from the first source, checked against the (synthetic) build, next to the
// program; then it is the setup's zip.
func TestDownloadAfterTheQuestion(t *testing.T) {
	noDownloads(t)
	body := gameZip(t)
	url, n := zipServer(t, body)
	p, err, out, dir := finder(t, "\n", true, gameSources{list: []string{url}, need: 1 << 30})
	if err != nil || p != filepath.Join(dir, archive.GameZipName) {
		t.Fatalf("finder: %q %v\n%s", p, err, out)
	}
	if b, _ := os.ReadFile(p); !bytes.Equal(b, body) {
		t.Error("the downloaded zip differs from the source's")
	}
	for _, want := range []string{"Dawngate isn't set up yet. Download Dawngate.zip (1.3 GB) from " + url + "? [Y/n]: ",
		"checking every file of it against build 1.0.903.854", "Dawngate.zip from " + url + ": ", "checked: build 1.0.903.854",
		"of " + mb(int64(len(body)))} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if n.Load() != 1 || exists(p+".part") {
		t.Errorf("%d requests, part left %v", n.Load(), exists(p+".part"))
	}
}

// The player's No: nothing is downloaded; the question for the file follows (here without an answer).
func TestDownloadDeclined(t *testing.T) {
	noDownloads(t)
	url, n := zipServer(t, gameZip(t))
	p, err, out, dir := finder(t, "n\n", true, gameSources{list: []string{url}})
	if err == nil || p != "" || n.Load() != 0 || exists(filepath.Join(dir, archive.GameZipName)) {
		t.Errorf("after No: %q %v, %d requests\n%s", p, err, n.Load(), out)
	}
	if !strings.Contains(out, "Drag Dawngate.zip onto this window") {
		t.Errorf("no question for the file after No:\n%s", out)
	}
}

// A source with another file is skipped with its line; the next one is used.
func TestDownloadWrongSourceSkipped(t *testing.T) {
	noDownloads(t)
	body := gameZip(t)
	bad, nb := zipServer(t, otherZip(t))
	good, ng := zipServer(t, body)
	p, err, out, _ := finder(t, "y\n", true, gameSources{list: []string{bad, good}})
	if err != nil || p == "" || nb.Load() != 1 || ng.Load() != 1 {
		t.Fatalf("finder: %q %v (%d, %d requests)\n%s", p, err, nb.Load(), ng.Load(), out)
	}
	if want := bad + " gave a file that isn't build 854's Dawngate.zip (it doesn't match build 1.0.903.854: "; !strings.Contains(out, want) {
		t.Errorf("no %q in:\n%s", want, out)
	}
	if b, _ := os.ReadFile(p); !bytes.Equal(b, body) {
		t.Error("the wrong file was kept")
	}
}

// No console: the download only from sources named on the command line, without a question; never the configured
// mirrors or the default.
func TestDownloadWithoutConsole(t *testing.T) {
	noDownloads(t)
	body := gameZip(t)
	url, n := zipServer(t, body)
	_, err, out, _ := finder(t, "", false, gameSources{list: []string{url}})
	if !errors.Is(err, setup.ErrNoZip) || !strings.Contains(err.Error(), "start the program in a console to download it") ||
		n.Load() != 0 {
		t.Errorf("unnamed sources without a console: %v, %d requests\n%s", err, n.Load(), out)
	}
	p, err, out, _ := finder(t, "", false, gameSources{list: []string{url}, named: true})
	if err != nil || p == "" || strings.Contains(out, "[Y/n]") {
		t.Errorf("--game-source without a console: %q %v\n%s", p, err, out)
	}
	// every named source failing: the error says so and names the flag
	bad, _ := zipServer(t, otherZip(t))
	_, err, out, _ = finder(t, "", false, gameSources{list: []string{bad}, named: true})
	if !errors.Is(err, setup.ErrNoZip) || !strings.Contains(err.Error(), "--"+GameSourceFlag) {
		t.Errorf("a wrong named source: %v\n%s", err, out)
	}
}

func TestDownloadNoSpace(t *testing.T) {
	noDownloads(t)
	gameFree = func(string) (uint64, error) { return 1 << 30, nil }
	url, n := zipServer(t, gameZip(t))
	_, err, out, _ := finder(t, "\n", true, gameSources{list: []string{url}, need: gameCopiesSpace})
	if !errors.Is(err, gamedl.ErrNoSpace) || !strings.Contains(err.Error(), "1.0 GB free") || n.Load() != 0 {
		t.Errorf("no space: %v, %d requests\n%s", err, n.Load(), out)
	}
	if strings.Contains(out, "Drag Dawngate.zip") {
		t.Errorf("asked for the file although the disk is full:\n%s", out)
	}
}

// A zip next to the program still wins: no question, no download.
func TestZipNextToProgramWins(t *testing.T) {
	noDownloads(t)
	body := gameZip(t)
	url, n := zipServer(t, body)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, archive.GameZipName), body, 0o644)
	var out bytes.Buffer
	o := IO{In: strings.NewReader("\n"), Out: &out, Interactive: true, ExeDir: dir}
	p, err := zipFinder(o, bufio.NewReader(o.In), "", gameSources{list: []string{url}})()
	if err != nil || p != filepath.Join(dir, archive.GameZipName) || n.Load() != 0 || out.Len() != 0 {
		t.Errorf("%q %v, %d requests\n%s", p, err, n.Load(), out.String())
	}
}

// The Downloads folder counts, except for a test lane's Start-Client (noDownloadsDir).
func TestDownloadsFolder(t *testing.T) {
	noDownloads(t)
	home, _ := os.UserHomeDir()
	os.MkdirAll(filepath.Join(home, "Downloads"), 0o755)
	inDownloads := filepath.Join(home, "Downloads", archive.GameZipName)
	os.WriteFile(inDownloads, []byte("PK"), 0o644)
	exeDir := filepath.Join(t.TempDir(), "release")
	if p := FirstFile(zipPlaces(exeDir, "", gameSources{})); p != inDownloads {
		t.Errorf("found %q, want the Downloads folder's", p)
	}
	if p := FirstFile(zipPlaces(exeDir, "", gameSources{noDownloadsDir: true})); p != "" {
		t.Errorf("a test lane found %q", p)
	}
	if c := zipPlaces(exeDir, "x.zip", gameSources{noDownloadsDir: true}); len(c) != 1 {
		t.Errorf("configured: %v", c)
	}
}

// Start-Client and Start-Server on a release folder without Dawngate.zip: --game-source downloads it (no console),
// and the setup goes on from it as if it had been there.
func TestReleaseSetupWithGameSource(t *testing.T) {
	if testing.Short() {
		t.Skip("sets up two synthetic game copies")
	}
	noDownloads(t)
	dir := release(t)
	body, err := os.ReadFile(filepath.Join(dir, archive.GameZipName))
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, archive.GameZipName))
	url, n := zipServer(t, body)
	code, out := run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal)
	if code != 1 || !strings.Contains(out, "start the program in a console to download it") || n.Load() != 0 {
		t.Fatalf("Start-Client without the zip: exit %d, %d requests\n%s", code, n.Load(), out)
	}
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal, "--game-source", url)
	if code != 0 || !strings.Contains(out, "[done]     client copy") || !strings.Contains(out, "checked: build 1.0.903.854") {
		t.Fatalf("Start-Client --game-source: exit %d\n%s", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, archive.GameZipName)); !bytes.Equal(b, body) {
		t.Error("no Dawngate.zip next to Start-Client")
	}
	// Start-Server's first setup in another folder, the source from its ini... only with the flag without a console
	srv := release(t)
	body2, _ := os.ReadFile(filepath.Join(srv, archive.GameZipName))
	os.Remove(filepath.Join(srv, archive.GameZipName))
	url2, n2 := zipServer(t, body2)
	cfg := filepath.Join(srv, ServerConfigName)
	if _, _, err := LoadOrCreate(cfg, ServerConfigText); err != nil {
		t.Fatal(err)
	}
	SetKey(cfg, "address", testAddress)
	SetKey(cfg, GameSourcesKey, url2)
	if code, out = run(t, RunServer, srv, "--non-interactive", "--setup-only"); code != 1 || n2.Load() != 0 {
		t.Errorf("Start-Server with only an ini mirror, no console: exit %d, %d requests\n%s", code, n2.Load(), out)
	}
	code, out = run(t, RunServer, srv, "--non-interactive", "--setup-only", "--game-source", url2)
	if code != 0 || !strings.Contains(out, "[done]     host copy") || n2.Load() != 1 {
		t.Errorf("Start-Server --game-source: exit %d\n%s", code, out)
	}
	if code, out = run(t, RunServer, srv, "--non-interactive", "--setup-only", "--game-source", "nowhere"); code != 1 ||
		!strings.Contains(out, "--game-source") {
		t.Errorf("a bad --game-source: exit %d\n%s", code, out)
	}
}
