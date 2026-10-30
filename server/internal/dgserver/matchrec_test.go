package dgserver

import (
	"strings"
	"testing"
)

// --match-record asks the match hosts for match_record=1, after the operator's --gms-options (which win), and only
// when this server starts match hosts.
func TestMatchRecordOption(t *testing.T) {
	cases := []struct {
		on, hosts bool
		options   string
		want      string
		note      string
	}{
		{false, true, "", "", "off"},
		{true, false, "", "", "no match hosts"},
		{true, true, "", "match_record=1", "on:"},
		{true, true, "timing=1", "timing=1;match_record=1", "on:"},
		{true, true, "match_record=0", "match_record=0", "on:"}, // the operator's own value wins
	}
	for _, c := range cases {
		cfg := config{matchRecord: c.on, gmsOptions: c.options}
		if c.hosts {
			cfg.gmsDir = `C:\host`
		}
		note := matchRecordOption(&cfg)
		if cfg.gmsOptions != c.want {
			t.Errorf("%+v: options %q, want %q", c, cfg.gmsOptions, c.want)
		}
		if !strings.Contains(note, c.note) {
			t.Errorf("%+v: note %q", c, note)
		}
	}
}
