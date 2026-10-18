package launcher

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/update"
	"dawngate/server/internal/update/updatetest"
)

func fakeRelease(tag string) updatetest.FakeRelease {
	return updatetest.FakeRelease{Tag: tag, Files: map[string]string{
		"Start-Client.exe": "client " + tag, "Start-Server.exe": "server " + tag, "README.txt": "readme " + tag,
		"host/dgsim.dll": "dll " + tag, "host/lua/dgsim_probe.lua": "-- probe " + tag}}
}

// updateFixture is a player's folder of release v0.5 (Start-Client.ini, Dawngate-Data) and a fake GitHub whose latest
// release is v0.6; Start-Client's own version is own, and starting the new one is recorded, not done.
type updateFixture struct {
	dir     string
	fake    *updatetest.FakeGitHub
	started [][]string // the new Start-Client's path, arguments, the version it replaced
}

func newUpdateFixture(t *testing.T, own string) *updateFixture {
	t.Helper()
	fx := &updateFixture{dir: t.TempDir(), fake: updatetest.NewFakeGitHub()}
	t.Cleanup(fx.fake.Close)
	old := fakeRelease("v0.5")
	for p, c := range old.Files {
		fp := filepath.Join(fx.dir, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(fp), 0o755)
		os.WriteFile(fp, []byte(c), 0o644)
	}
	os.WriteFile(filepath.Join(fx.dir, update.BuildFile), []byte(old.BuildText()), 0o644)
	os.WriteFile(filepath.Join(fx.dir, ClientConfigName), []byte("server = \r\n"), 0o644)
	os.MkdirAll(filepath.Join(fx.dir, DefaultFolder, "data"), 0o755)
	fx.fake.Tag, fx.fake.ZipBytes = "v0.6", fakeRelease("v0.6").Zip(nil)
	src, err := update.Loopback(fx.fake.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	saved := []any{updateOwnVersion, updateSource, updateRunning, updateExeVersion, updateStart}
	t.Cleanup(func() {
		updateOwnVersion = saved[0].(func() string)
		updateSource = saved[1].(func(string) update.Source)
		updateRunning = saved[2].(func() (string, error))
		updateExeVersion = saved[3].(func(string) (string, error))
		updateStart = saved[4].(func(string, []string, string) (int, error))
	})
	updateOwnVersion = func() string { return own }
	updateSource = func(string) update.Source { return src }
	updateRunning = func() (string, error) { return filepath.Join(fx.dir, "Start-Client.exe"), nil }
	updateExeVersion = func(exe string) (string, error) {
		b, err := os.ReadFile(exe)
		return strings.TrimPrefix(string(b), "client "), err
	}
	updateStart = func(exe string, args []string, from string) (int, error) {
		fx.started = append(fx.started, append([]string{exe, from}, args...))
		return 7, nil
	}
	return fx
}

func (fx *updateFixture) offer(t *testing.T, answer string, interactive bool, u updateOptions) (int, bool, string) {
	t.Helper()
	var out bytes.Buffer
	o := IO{In: strings.NewReader(answer), Out: &out, ExeDir: fx.dir, Interactive: interactive}
	u.root = filepath.Join(fx.dir, DefaultFolder)
	code, ran, err := offerUpdate(o, bufio.NewReader(o.In), u)
	if err != nil {
		t.Fatalf("offerUpdate: %v\n%s", err, out.String())
	}
	return code, ran, out.String()
}

func (fx *updateFixture) file(name string) string {
	b, _ := os.ReadFile(filepath.Join(fx.dir, name))
	return string(b)
}

func TestUpdateYes(t *testing.T) {
	fx := newUpdateFixture(t, "v0.5")
	code, ran, out := fx.offer(t, "\n", true, updateOptions{args: []string{"192.168.1.20", "--zip", "x.zip"}})
	if !ran || code != 7 || !strings.Contains(out, "Version v0.6 is available (you have v0.5). Update now? [Y/n]: ") ||
		!strings.Contains(out, "v0.6 installed") {
		t.Fatalf("ran %v, exit %d\n%s", ran, code, out)
	}
	want := []string{filepath.Join(fx.dir, "Start-Client.exe"), "v0.5", "192.168.1.20", "--zip", "x.zip"}
	if len(fx.started) != 1 || strings.Join(fx.started[0], "|") != strings.Join(want, "|") {
		t.Fatalf("started %v, want %v", fx.started, want)
	}
	if fx.file("Start-Client.exe") != "client v0.6" || fx.file("host/dgsim.dll") != "dll v0.6" ||
		fx.file("Start-Client.old.exe") != "client v0.5" || fx.file(ClientConfigName) != "server = \r\n" {
		t.Fatal("the folder isn't v0.6, or the ini changed")
	}
	if n := fx.fake.Count(); n != 3 { // the question, the asset's redirect, the asset
		t.Fatalf("%d requests: %v", n, fx.fake.Requests)
	}
}

func TestUpdateNotNow(t *testing.T) {
	fx := newUpdateFixture(t, "v0.5")
	_, ran, out := fx.offer(t, "n\n", true, updateOptions{})
	if ran || !strings.Contains(out, "not now") || fx.file("Start-Client.exe") != "client v0.5" || fx.fake.Count() != 1 {
		t.Fatalf("ran %v, %d requests\n%s", ran, fx.fake.Count(), out)
	}
	_, ran, _ = fx.offer(t, "", true, updateOptions{}) // no answer at all (end of input): not now
	if ran || fx.file("Start-Client.exe") != "client v0.5" {
		t.Fatal("updated without an answer")
	}
}

// The update never asks GitHub for a developer build, with update_check = off, --check, --setup-only,
// --non-interactive, --test-lane, without a console, or right after an update.
func TestUpdateNotAsked(t *testing.T) {
	for _, c := range []struct {
		name        string
		own         string
		interactive bool
		u           updateOptions
		env         map[string]string
	}{
		{name: "developer build", own: "dev-0123456789", interactive: true},
		{name: "developer build dirty", own: "dev-0123456789+dirty", interactive: true},
		{name: "nightly-style name", own: "Dawngate-Restoration-20261018-0123456789", interactive: true},
		{name: "update_check off", own: "v0.5", interactive: true, u: updateOptions{setting: "off"}},
		{name: "--check", own: "v0.5", interactive: true, u: updateOptions{check: true}},
		{name: "--setup-only", own: "v0.5", interactive: true, u: updateOptions{setupOnly: true}},
		{name: "--non-interactive", own: "v0.5", interactive: true, u: updateOptions{nonInteractive: true}},
		{name: "--test-lane", own: "v0.5", interactive: true, u: updateOptions{lane: "C"}},
		{name: "no console", own: "v0.5"},
		{name: "just updated", own: "v0.6", interactive: true, env: map[string]string{UpdatedFromEnv: "v0.5"}},
		{name: "test hook but --non-interactive", own: "v0.5", u: updateOptions{nonInteractive: true, lane: "C"},
			env: map[string]string{UpdateTestEnv: "http://127.0.0.1:1"}},
		{name: "test hook not loopback", own: "v0.5", interactive: true, env: map[string]string{UpdateTestEnv: "https://api.github.com"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := newUpdateFixture(t, c.own)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			_, ran, out := fx.offer(t, "y\n", c.interactive, c.u)
			if ran || fx.fake.Count() != 0 || strings.Contains(out, "available") {
				t.Fatalf("ran %v, %d requests\n%s", ran, fx.fake.Count(), out)
			}
		})
	}
	var out bytes.Buffer
	if _, _, err := offerUpdate(IO{Out: &out, In: strings.NewReader("")}, bufio.NewReader(strings.NewReader("")), updateOptions{setting: "sometimes"}); err == nil {
		t.Fatal("update_check = sometimes accepted")
	}
}

// The release test's hook: a loopback fake also with --test-lane and without a console (the answer from the input).
func TestUpdateTestHook(t *testing.T) {
	fx := newUpdateFixture(t, "v0.5")
	updateSource = update.GitHub // the hook must replace the real source
	t.Setenv(UpdateTestEnv, fx.fake.URL)
	code, ran, out := fx.offer(t, "y\n", false, updateOptions{lane: "C", args: []string{"--test-lane", "C"}})
	if !ran || code != 7 || fx.file("Start-Client.exe") != "client v0.6" {
		t.Fatalf("ran %v exit %d\n%s", ran, code, out)
	}
}

func TestUpdateNothingNewer(t *testing.T) {
	fx := newUpdateFixture(t, "v0.6")
	if _, ran, out := fx.offer(t, "y\n", true, updateOptions{}); ran || strings.Contains(out, "available") || fx.fake.Count() != 1 {
		t.Fatalf("the same version: ran %v\n%s", ran, out)
	}
	fx = newUpdateFixture(t, "v0.7")
	if _, ran, _ := fx.offer(t, "y\n", true, updateOptions{}); ran {
		t.Fatal("an older release offered")
	}
	fx = newUpdateFixture(t, "v0.5")
	fx.fake.ZipName = "Dawngate-Restoration-v0.6.zip" // a hand-made release: no <tag>.zip
	if _, ran, out := fx.offer(t, "y\n", true, updateOptions{}); ran || out != "" {
		t.Fatalf("no <tag>.zip: ran %v\n%s", ran, out)
	}
	fx = newUpdateFixture(t, "v0.5")
	fx.fake.Close()
	if _, ran, out := fx.offer(t, "y\n", true, updateOptions{}); ran || !strings.Contains(out, "no answer about a newer release") {
		t.Fatalf("no answer: ran %v\n%s", ran, out)
	}
}

func TestUpdateFails(t *testing.T) {
	fx := newUpdateFixture(t, "v0.5")
	updateExeVersion = func(string) (string, error) { return "v0.4", nil } // a broken release
	_, ran, out := fx.offer(t, "y\n", true, updateOptions{})
	if ran || !strings.Contains(out, "The update to v0.6 failed") || !strings.Contains(out, "goes on with v0.5") ||
		!strings.Contains(out, update.ReleasesPage) || fx.file("Start-Client.exe") != "client v0.5" {
		t.Fatalf("ran %v\n%s", ran, out)
	}
}

// Through RunClient: the update runs before anything else and its exit code is Start-Client's; --check never asks.
func TestRunClientUpdates(t *testing.T) {
	fx := newUpdateFixture(t, "v0.5")
	os.WriteFile(filepath.Join(fx.dir, "Start-Client.old.exe"), []byte("left by an earlier update"), 0o644)
	var out bytes.Buffer
	code := RunClient([]string{"--check"}, IO{In: strings.NewReader("y\n"), Out: &out, ExeDir: fx.dir, Interactive: true})
	if fx.fake.Count() != 0 || fx.file("Start-Client.exe") != "client v0.5" {
		t.Fatalf("--check asked (exit %d)\n%s", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(fx.dir, "Start-Client.old.exe")); err == nil || !strings.Contains(out.String(), "Start-Client.old.exe deleted") {
		t.Fatalf("the old program not deleted\n%s", out.String())
	}
	out.Reset()
	code = RunClient([]string{"--picker", "on"}, IO{In: strings.NewReader("y\n"), Out: &out, ExeDir: fx.dir, Interactive: true})
	if code != 7 || len(fx.started) != 1 || strings.Join(fx.started[0][2:], " ") != "--picker on" {
		t.Fatalf("exit %d, started %v\n%s", code, fx.started, out.String())
	}
}

func TestClientConfigHasUpdateCheck(t *testing.T) {
	if !strings.Contains(ClientConfigText, "\nupdate_check = on\n") || !strings.Contains(strings.Join(clientKeys, " "), "update_check") {
		t.Fatal("update_check missing from Start-Client.ini's text or keys")
	}
}
