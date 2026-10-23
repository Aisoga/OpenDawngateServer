package blaze

import (
	"strings"
	"testing"
)

// TestOfferedMapTags: the tags the tunnel's info hello names (FactMaps) are the maps besides SM_Map_v5 that
// customLevelOffered lets a creator pick: the server's own level and --custom-maps, never SM_Map_v5 itself.
func TestOfferedMapTags(t *testing.T) {
	for _, c := range []struct {
		level  string
		custom []string
		want   string
	}{
		{"", nil, ""},
		{HostedLevel, []string{HostedLevel}, ""},
		{"", []string{"MM_Map_v3"}, "mm"},
		{"mm_map_v3", nil, "mm"},
		{"MM_Map_v3", []string{"MM_Map_v3"}, "mm"},
	} {
		got := OfferedMapTags(c.level, c.custom)
		if strings.Join(got, "+") != c.want {
			t.Errorf("OfferedMapTags(%q, %v) = %v, want %q", c.level, c.custom, got, c.want)
		}
		s := &Server{Level: c.level, CustomLevels: c.custom}
		for _, l := range PlayableLevels {
			offered := strings.Contains("+"+c.want+"+", "+"+l.Tag+"+")
			if l.Name != HostedLevel && s.customLevelOffered(l.Name) != offered {
				t.Errorf("%s on --map %q --custom-maps %v: offered %v, the fact says %v", l.Name, c.level, c.custom,
					s.customLevelOffered(l.Name), offered)
			}
		}
	}
}
