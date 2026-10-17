package gmshost

import (
	"reflect"
	"runtime"
	"testing"
)

func TestRunnerFor(t *testing.T) {
	const exe = "/srv/hostcopy/Dawngate/game/Dawngate.exe"
	auto := []string{"wine"}
	if runtime.GOOS == "windows" {
		auto = nil
	}
	for _, c := range []struct {
		spec, exe string
		want      []string
	}{
		{"", exe, auto},
		{"auto", exe, auto},
		{" auto ", `C:\host\DAWNGATE.EXE`, auto},
		{"auto", "/usr/local/bin/gms_script", nil}, // not a Windows program: as it is (tests' helpers)
		{"none", exe, nil},
		{"wine", "/usr/local/bin/gms_script", []string{"wine"}},
		{"/opt/wine-stable/bin/wine  --some-flag", exe, []string{"/opt/wine-stable/bin/wine", "--some-flag"}},
	} {
		if got := RunnerFor(c.spec, c.exe); !reflect.DeepEqual(got, c.want) {
			t.Errorf("RunnerFor(%q, %q) = %q, want %q", c.spec, c.exe, got, c.want)
		}
	}
}

func TestWinePath(t *testing.T) {
	for in, want := range map[string]string{
		"/srv/dawngate/logs/host/3": `Z:\srv\dawngate\logs\host\3`,
		"/":                         `Z:\`,
		`C:\data\host`:              `C:\data\host`,
		"relative/dir":              "relative/dir",
	} {
		if got := WinePath(in); got != want {
			t.Errorf("WinePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWineVars(t *testing.T) {
	none := func(string) (string, bool) { return "", false }
	if got, want := wineVars(none), []string{"WINEDLLOVERRIDES=mscoree,mshtml=", "WINEDEBUG=-all"}; !reflect.DeepEqual(got, want) {
		t.Errorf("wineVars(empty) = %q, want %q", got, want)
	}
	both := func(k string) (string, bool) { return "x", k == "WINEDLLOVERRIDES" || k == "WINEDEBUG" }
	if got := wineVars(both); len(got) != 0 {
		t.Errorf("wineVars keeps the operator's settings, got %q", got)
	}
}

func TestRunnerCommand(t *testing.T) {
	l := &ExecLauncher{Path: "/host/Dawngate/game/Dawngate.exe", Args: []string{"-multipleInstances"}}
	if got := l.command().Args; !reflect.DeepEqual(got, []string{l.Path, "-multipleInstances"}) {
		t.Errorf("no runner: %q", got)
	}
	l.Runner = []string{"wine", "--opt"}
	if got := l.command().Args; !reflect.DeepEqual(got, []string{"wine", "--opt", l.Path, "-multipleInstances"}) {
		t.Errorf("runner: %q", got)
	}
	if got := l.command().Args; !reflect.DeepEqual(got, []string{"wine", "--opt", l.Path, "-multipleInstances"}) {
		t.Errorf("runner, second launch: %q", got) // the runner's slice must not grow
	}
	vars := []string{"DGGMS_MATCH=1", "DGGMS_DATA=/srv/logs/1"}
	got := l.hostVars(append([]string{}, vars...))
	if runtime.GOOS == "windows" {
		if !reflect.DeepEqual(got, vars) {
			t.Errorf("hostVars on Windows changed the environment: %q", got)
		}
		return
	}
	if got[1] != `DGGMS_DATA=Z:\srv\logs\1` {
		t.Errorf("hostVars: %q", got)
	}
}
