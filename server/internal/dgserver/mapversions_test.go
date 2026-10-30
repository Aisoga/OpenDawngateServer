package dgserver

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"dawngate/server/internal/redirector"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/web"
)

// TestMapVersionsFact (MAPED-GUARD-dialog): the server names its version of each offered map that has one, in the
// info hello (tunnel.FactMapVersion) and on its HTTP port (web.MapVersionsPath); a map it doesn't offer, or one
// without a version, is named in neither.
func TestMapVersionsFact(t *testing.T) {
	sum := sha256.Sum256([]byte("mm"))
	versions := map[string][32]byte{"zz_mm_map_v3.snap": sum}
	if got := offeredVersions("", []string{"MM_Map_v3"}, versions); len(got) != 1 || got["mm"] != sum {
		t.Errorf("offeredVersions of MM = %v", got)
	}
	if got := offeredVersions("", nil, versions); len(got) != 0 {
		t.Errorf("a version of a map not offered: %v", got)
	}
	if got := offeredVersions("MM_Map_v3", nil, nil); len(got) != 0 {
		t.Errorf("no versions: %v", got)
	}

	certs := testCerts(t)
	for _, c := range []struct {
		custom   []string
		versions map[string][32]byte
		want     string // the version fact's value and the page's hash ("" = none)
	}{
		{[]string{"MM_Map_v3"}, versions, hex.EncodeToString(sum[:])},
		{[]string{"MM_Map_v3"}, nil, ""},
		{nil, versions, ""},
	} {
		cfg := config{bind: "127.0.9.36", certs: certs, logDir: t.TempDir(), tunnel: TunnelOn,
			redirectorPort: redirector.Port, customLevels: c.custom, mapVersions: c.versions}
		in, err := start(cfg)
		if err != nil {
			t.Fatal(err)
		}
		info, err := tunnel.ProbeInfo(in.gateway.Addr(), 3*time.Second, nil)
		if err != nil {
			in.stop()
			t.Fatal(err)
		}
		res, err := (&http.Client{Timeout: 3 * time.Second}).Get("http://" + cfg.bind + ":" + strconv.Itoa(in.ports[3]) + "/" + web.MapVersionsPath)
		var page []byte
		if err == nil {
			page, _ = io.ReadAll(res.Body)
			res.Body.Close()
		}
		in.stop()
		if err != nil {
			t.Fatal(err)
		}
		got, named := info.Facts["version.mm"]
		if c.want == "" && (named || len(page) != 0) || c.want != "" && got != c.want {
			t.Errorf("--custom-maps %v, versions %v: the fact %q (named %v), the page %q, want %q", c.custom, c.versions, got, named, page, c.want)
		}
		if c.want != "" && string(page) != "mm "+c.want+"\n" {
			t.Errorf("the page %q", page)
		}
		if info.Facts[tunnel.FactMaps] == "" && len(c.custom) > 0 {
			t.Errorf("the maps fact is gone beside the versions: %v", info.Facts)
		}
	}
}
