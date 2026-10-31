package mapctool

import (
	"encoding/json"
	"strings"
	"testing"

	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/version"
)

// TestVersion: `mapc version --json` is {"release", "format", "api"} in that order; the plain form says the same;
// any other argument is a usage error.
func TestVersion(t *testing.T) {
	code, out, errw := call("version", "--json")
	want := "{\n  \"release\": " + quote(version.String()) + ",\n  \"format\": 1,\n  \"api\": 1\n}\n"
	if code != 0 || out != want || errw != "" {
		t.Fatalf("exit %d: %q (want %q), stderr %q", code, out, want, errw)
	}
	var v Version
	if err := json.Unmarshal([]byte(out), &v); err != nil || v != CurrentVersion() || v.Format != mapc.Format ||
		v.API != API {
		t.Fatalf("%v: %+v", err, v)
	}
	if code, out, _ := call("version"); code != 0 || out != "mapc "+version.String()+": map format 1, interface 1\n" {
		t.Errorf("plain: exit %d: %q", code, out)
	}
	for _, args := range [][]string{{"version", "x"}, {"version", "--copy", "c"}, {"version", "--bogus"}} {
		if code, out, errw := call(args...); code != 2 || out != "" || !strings.Contains(errw, "usage:") {
			t.Errorf("%q: exit %d: %q %q", args, code, out, errw)
		}
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
