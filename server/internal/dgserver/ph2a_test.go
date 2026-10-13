package dgserver

import (
	"reflect"
	"testing"
)

// --test-login-errors (a test aid): hex or decimal 16-bit codes, comma-separated; "" = none.
func TestParseLoginErrors(t *testing.T) {
	got, err := parseLoginErrors(" 0x0C, 16498 ,0,0x2b")
	if err != nil || !reflect.DeepEqual(got, []uint16{0x0C, 0x4072, 0, 0x2B}) {
		t.Errorf("got %v, %v", got, err)
	}
	if got, err := parseLoginErrors(""); got != nil || err != nil {
		t.Errorf("empty: %v, %v", got, err)
	}
	for _, bad := range []string{"0x10000", "x", "1,,2", "-1"} {
		if _, err := parseLoginErrors(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if s := describeLoginErrors([]uint16{0x0C, 0x4072}); s != "0x000c, 0x4072" {
		t.Errorf("describe: %q", s)
	}
}
