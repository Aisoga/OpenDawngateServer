package moviepatch

import (
	"strings"
	"testing"

	"dawngate/server/internal/uichannel"
)

// The channel file names the port the server listens on, for the copy's server.
func TestChannelFile(t *testing.T) {
	if ChannelPort != uichannel.Port {
		t.Errorf("ChannelPort %d, uichannel.Port %d", ChannelPort, uichannel.Port)
	}
	got, err := ChannelFile("127.0.0.2", ChannelPort)
	if err != nil {
		t.Fatal(err)
	}
	if want := "dawngate-channel\t1\nhost\t127.0.0.2\nport\t10039\nend\n"; string(got) != want {
		t.Errorf("%q, want %q", got, want)
	}
	for _, bad := range []string{"", "a b", "x\"y", "<x>", "ä"} {
		if _, err := ChannelFile(bad, ChannelPort); err == nil || !strings.HasPrefix(err.Error(), "UI channel: ") {
			t.Errorf("host %q: %v", bad, err)
		}
	}
	if _, err := ChannelFile("x", 0); err == nil {
		t.Error("port 0 accepted")
	}
}
