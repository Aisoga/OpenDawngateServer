package version

import (
	"runtime/debug"
	"testing"
)

func info(settings ...string) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		bi := &debug.BuildInfo{}
		for i := 0; i+1 < len(settings); i += 2 {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
		}
		return bi, true
	}
}

func TestCompute(t *testing.T) {
	none := func() (*debug.BuildInfo, bool) { return nil, false }
	for _, c := range []struct {
		name, rel string
		info      func() (*debug.BuildInfo, bool)
		want      string
	}{
		{"release", "OpenDawngateServer-20261016-905ef17f5f", info("vcs.revision", "905ef17f5f0e07a9f573a87029426e7f5f8a2ccc"), "OpenDawngateServer-20261016-905ef17f5f"},
		{"release with odd characters", " Test Build #2 ", none, "Test-Build--2"},
		{"clean checkout", "", info("vcs.revision", "905ef17f5f0e07a9f573a87029426e7f5f8a2ccc", "vcs.modified", "false"), "dev-905ef17f5f"},
		{"modified checkout", "", info("vcs.revision", "905ef17f5f0e07a9f573a87029426e7f5f8a2ccc", "vcs.modified", "true"), "dev-905ef17f5f+dirty"},
		{"no stamp", "", info(), "dev"},
		{"no build info", "", none, "dev"},
	} {
		if got := compute(c.rel, c.info); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if s := String(); s == "" || Clean(s) != s {
		t.Errorf("String() = %q", s)
	}
}

func TestCleanAndSame(t *testing.T) {
	long := ""
	for len(long) < 100 {
		long += "abcdefghij"
	}
	if got := Clean(long); len(got) != MaxLen {
		t.Errorf("Clean of 100 characters: %d", len(got))
	}
	if got := Clean("v1\t2\n3"); got != "v1-2-3" {
		t.Errorf("Clean = %q", got)
	}
	for _, c := range []struct {
		own, server string
		want        bool
	}{
		{"r1", "r1", true}, {"r1", "r2", false}, {"r1", "", false}, {"", "", false}, {"dev-1+dirty", "dev-1", false},
	} {
		if Same(c.own, c.server) != c.want {
			t.Errorf("Same(%q, %q) != %v", c.own, c.server, c.want)
		}
	}
}
