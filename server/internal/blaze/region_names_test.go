package blaze

import (
	"testing"

	"dawngate/server/internal/gmshost"
)

// The browser's Region (PSAS): the region's own name, escaped for the cell's htmlText, and nothing on a server with
// one region.
func TestBrowserRegion(t *testing.T) {
	m := gmshost.NewManager(gmshost.Config{})
	s := &Server{Hosts: m}
	if got := s.browserRegion("local"); got != "" {
		t.Errorf("one region: %q, want an empty column", got)
	}
	if got := (&Server{}).browserRegion(""); got != "" {
		t.Errorf("no host manager: %q", got)
	}
	m.SetRegions("eu", map[string]string{"na1": "na", "ap1": "ap"})
	m.SetRegionNames(map[string]string{"eu": "Europe", "na": "NA (Virginia)", "ap": "Asia <East> & Co"})
	for region, want := range map[string]string{
		"na": "NA (Virginia)",
		"eu": "Europe",
		"":   "Europe", // the home region
		"ap": "Asia &lt;East&gt; &amp; Co",
	} {
		if got := s.browserRegion(region); got != want {
			t.Errorf("%q: %q, want %q", region, got, want)
		}
	}
	m.SetRegionNames(nil)
	if got := s.browserRegion("na"); got != "NA" {
		t.Errorf("no names: %q, want the alias in capitals", got)
	}
}
