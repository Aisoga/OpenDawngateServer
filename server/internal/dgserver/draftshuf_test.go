package dgserver

import "testing"

// --draft-shuffle takes on and off only (on is the default).
func TestDraftShuffleFlag(t *testing.T) {
	if code := Main([]string{"--draft-shuffle", "maybe", "--print-ports"}); code != 2 {
		t.Errorf("--draft-shuffle maybe: exit %d", code)
	}
	for _, v := range []string{"on", "off"} {
		if code := Main([]string{"--draft-shuffle", v, "--print-ports"}); code != 0 {
			t.Errorf("--draft-shuffle %s: exit %d", v, code)
		}
	}
}
