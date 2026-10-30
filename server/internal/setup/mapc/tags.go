package mapc

import (
	"strings"

	"dawngate/server/internal/setup/mapmm/tagmap"
)

// The tag map's jungle (MAPED4b): the lanes' bands and the camps' tags, at the first map's numbers (measured on
// SM_Map_v5's shipped tag map; tagmap's jungle.go has the rules).
const (
	// LaneTagMargin widens a lane's band beyond its corridor: SM_Map_v5's Lane band lies 24-26 m round its splines,
	// its imported corridor (CM_Stand) is 42 m wide, so half the width plus 4 m.
	LaneTagMargin = 4.0
	// The camp tags' radii: the discs on SM_Map_v5's Jungle that match its buff (18-20 m), pigs (24 m) and parasite
	// (38 m) regions best.
	BuffTagRadius     = 20.0
	PigsTagRadius     = 24.0
	ParasiteTagRadius = 38.0
)

// CampTag is the tag a camp gives the ground round it and its radius, "" for none: Parasite for the parasite's camp,
// MoneyPigs for a Boar camp, and by its creature a buff camp's colour, as SM_Map_v5's map has them (GreenBuff the
// Fish, BlueBuff the Fungus, OrangeBuff the Ugger). By the noun, as the match host takes a camp's role from its data.
func CampTag(c Camp) (string, float64) {
	n := strings.ToLower(c.Noun)
	switch {
	case c.Kind == CampParasite:
		return "Parasite", ParasiteTagRadius
	case strings.Contains(n, "boar"):
		return "MoneyPigs", PigsTagRadius
	case strings.Contains(n, "buff_fish"):
		return "GreenBuff", BuffTagRadius
	case strings.Contains(n, "buff_fungus"):
		return "BlueBuff", BuffTagRadius
	case strings.Contains(n, "buff_ugger"):
		return "OrangeBuff", BuffTagRadius
	}
	return "", 0
}

// jungleTags adds the lanes' bands (lane 1 Top, lane 2 Bottom) and both teams' camp discs to the tag map's spec.
func (cm *Compiled) jungleTags(s *tagmap.Spec) {
	for _, ln := range cm.Map.Lanes {
		ks := make([]tagmap.Point, len(ln.Knots))
		for i, k := range ln.Knots {
			ks[i] = tagmap.Point{X: k[0], Y: k[1]}
		}
		s.Lanes = append(s.Lanes, tagmap.LaneBand{ID: ln.ID, Half: ln.Width/2 + LaneTagMargin, Knots: ks})
	}
	for t := range 2 {
		for _, c := range cm.Layout.Teams[t].Camps {
			if tag, r := CampTag(c); tag != "" {
				s.Discs = append(s.Discs, tagmap.Disc{Tag: tag, Centre: tagmap.Point{X: c.Pos[0], Y: c.Pos[1]}, R: r})
			}
		}
	}
}
