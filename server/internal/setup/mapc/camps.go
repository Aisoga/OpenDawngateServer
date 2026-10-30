package mapc

import (
	"fmt"
	"math"
	"strings"

	"dawngate/server/internal/setup/assetdata"
)

// The jungle (MAPED3): the camps and the lane-agent spawners.
//
// A camp is the first map's (SM_Map_v5's) spawner marker of the camp's noun with its creature spots (the marker's
// children, SpawnPoint_CreepMarker markers whose campType is minor, medium or major), all moved by the camp's
// offset from the template marker. The host reads a spawner by its SpawnPointDef type (2 a jungle camp, 3 the
// parasite) and puts the camp's creatures on its children; the composition comes from the noun and the timing from
// the noun too (the host's camp_timers option, by default; with camp_timers=marker the copied marker's own override
// where the template has one), so both are the template's either way. Team 2's camps are team 1's turned half round the centre: the spots'
// offsets and the markers' heading turned by 180 degrees, so the map stays point-symmetric. The parasite's camp is
// the map's one and isn't mirrored.
//
// The lane-agent spawners are SM_Map_v5's spawnpoint_<team>_Strider markers (SpawnPointDef type 4): one per team and
// lane at the lane's minion spawner, pointing at the lane's head knot as the minion spawner does, its child the
// enemy's Outer Binding on that lane (as the shipped ones have it; their second child, the tower_respawn_graphics
// decoration, isn't copied). The host spawns lane agents from them only when the Level's gameplay tuning has
// striderSpawnEnabled (MM_Map_v3's tuning), as on the shipped maps.

// The camp kinds of map.json.
const (
	CampCamp     = "camp"     // a jungle camp
	CampBuff     = "buff"     // a jungle camp whose creatures give a buff (the host tells them by the noun until MAPED4)
	CampParasite = "parasite" // the parasite's camp: one on the map, not mirrored
)

// The template level's markerset the jungle camps come from (the parasite's is in its titans set).
const templateJungle = "jungle_creeps"

// setCamps is the map's markerset the camps go into: the titans set, which holds the parasite's on the first map too.
// (A set of their own would change every map's asset list.)
const setCamps = setTitans

// ParasiteOffCentre is how far the parasite's camp may stand from the centre before the check warns that the map
// isn't symmetric there (metres) [assumed: SM_Map_v5's stands 4.7 m from it].
const ParasiteOffCentre = 30.0

// buffNoun tells whether the host counts a camp noun as a buff camp (the 60-s reveal, the bots' buff steps): by the
// words Buff and Boar in its name (director.cpp; MAPED4 moves it into data).
func buffNoun(noun string) bool {
	n := strings.ToLower(noun)
	return strings.Contains(n, "buff") || strings.Contains(n, "boar")
}

// campRules are the camps' structural rules: one parasite at most, near the centre; a camp's kind and the host's
// buff test agree.
func (c *checker) campRules() {
	parasites := 0
	for k, cp := range c.m.Camps {
		path := fmt.Sprintf("camps[%d]", k)
		switch cp.Kind {
		case CampParasite:
			parasites++
			if parasites == 2 {
				c.errorf("camps.parasite", path, pt(cp.Pos), "a second parasite camp: a map has one, and it isn't mirrored")
			}
			if d := math.Hypot(cp.Pos[0], cp.Pos[1]); d > ParasiteOffCentre {
				c.warnf("camps.parasite", path+".pos", pt(cp.Pos), "the parasite's camp stands %.0f m from the centre "+
					"(it isn't mirrored, so the map isn't symmetric there; at most %.0f m)", d, ParasiteOffCentre)
			}
		case CampBuff, CampCamp:
			if cp.Noun != "" && buffNoun(cp.Noun) != (cp.Kind == CampBuff) {
				c.warnf("camps.role", path+".kind", pt(cp.Pos), "kind %s, but the match host counts %s as %s by its "+
					"name", cp.Kind, cp.Noun, map[bool]string{true: "a buff camp", false: "a small camp"}[buffNoun(cp.Noun)])
			}
		}
	}
}

// campTemplate is the template level's spawner marker of a camp noun: of several, the one in team 1's half nearest
// p (team 1's place of the camp).
func (t *templates) campTemplate(noun string, p Point) (*assetdata.Object, error) {
	ms := t.byNoun[strings.ToLower(noun)]
	var best *assetdata.Object
	bestD := math.Inf(1)
	for _, m := range ms {
		q := vec(m, "pos")
		d := math.Hypot(q[0]-p[0], q[1]-p[1])
		if q[0]+q[1] > 0 {
			d += 1e6 // team 2's half: only when there is no other
		}
		if d < bestD {
			best, bestD = m, d
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%s has no camp marker of the noun %s", TemplateLevel, noun)
	}
	if len(vec(best, "childMarkerIds")) == 0 {
		return nil, fmt.Errorf("%s's %s marker has no creature spots", TemplateLevel, noun)
	}
	return best, nil
}

// turn adds 180 degrees to a marker's heading (rotDegrees' z, kept in (-180, 180]).
func turn(m *assetdata.Object) {
	r := vec(m, "rotDegrees")
	if len(r) != 3 {
		return
	}
	z := r[2] + 180
	if z > 180 {
		z -= 360
	}
	m.Set("rotDegrees", f32s(r[0], r[1], z))
}

// camps places every camp with its creature spots. It reports a camp whose spots stand on blocked ground.
func (b *assetBuild) camps() ([]Finding, error) {
	var fs []Finding
	for t := range 2 {
		for k, cp := range b.cm.Layout.Teams[t].Camps {
			turned := t == 1 // team 2's camps are the mirror; the parasite is only in team 1's list
			own := cp.Pos
			if turned {
				own = Mirror(cp.Pos)
			}
			tm, err := b.tm.campTemplate(cp.Noun, own)
			if err != nil {
				return nil, err
			}
			tp := vec(tm, "pos")
			camp := b.place(setCamps, tm, fmt.Sprintf("camp/%d/%d", t+1, k), cp.Pos)
			if turned {
				turn(camp)
			}
			var kids []uint32
			blocked := 0
			for s, c := range vec(tm, "childMarkerIds") {
				spot, ok := b.tm.byID[uint64(c)]
				if !ok {
					return nil, fmt.Errorf("%s: the camp %s's creature spot %d is missing", TemplateLevel, cp.Noun,
						uint64(c))
				}
				q := vec(spot, "pos")
				dx, dy := q[0]-tp[0], q[1]-tp[1]
				if turned {
					dx, dy = -dx, -dy
				}
				sp := Point{cp.Pos[0] + dx, cp.Pos[1] + dy}
				m := b.place(setCamps, spot, fmt.Sprintf("camp/%d/%d/spot/%d", t+1, k, s), sp)
				if turned {
					turn(m)
				}
				kids = append(kids, uint32(uval(m, "markerId")))
				if !Walkable(b.cm.Layers.KindAt(sp)) {
					blocked++
				}
			}
			setChildren(camp, kids...)
			if t == 0 && blocked > 0 {
				fs = append(fs, Finding{Rule: "camps.spots", Severity: Warning, Path: fmt.Sprintf("camps[%d].pos", k),
					At: &Point{cp.Pos[0], cp.Pos[1]}, Message: fmt.Sprintf("%d of the camp's %d creature spots stand "+
						"on blocked ground (they stand up to about 9 m round the camp; open that ground)", blocked,
						len(kids))})
			}
		}
	}
	return fs, nil
}

// laneAgents places each team's lane-agent spawner per lane: at the lane's minion spawner, pointing at the lane's head
// knot, its child the enemy's Outer Binding on that lane. It runs after lanes (the head knots' ids) and reserves the
// Bindings' ids by their keys (structures places them).
func (b *assetBuild) laneAgents() error {
	for t, set := range []string{setMinionBlue, setMinionRed} {
		team := &b.cm.Layout.Teams[t]
		noun := fmt.Sprintf("spawnpoint_%s_Strider.Noun", teamName[t])
		cands := b.tm.byNoun[strings.ToLower(noun)]
		if len(cands) == 0 {
			return fmt.Errorf("%s has no %s marker", TemplateLevel, noun)
		}
		for _, id := range b.cm.Layout.LaneIDs {
			tm := cands[0]
			for _, c := range cands {
				if smLane(c) == id {
					tm = c
					break
				}
			}
			m := b.place(set, tm, fmt.Sprintf("laneagent/%d/%d", t+1, id), team.Spawners[id])
			m.Set("targetMarkerId", uint64(b.markerID(fmt.Sprintf("lane/%d/%d/0", t+1, id))))
			// a one-path map may give the enemy Bindings on one lane only (fixed.bindings): then that lane's
			enemy, bl := &b.cm.Layout.Teams[1-t], id
			if len(enemy.Bindings[id]) == 0 {
				for _, o := range b.cm.Layout.LaneIDs {
					if len(enemy.Bindings[o]) > 0 {
						bl = o
						break
					}
				}
			}
			setChildren(m, b.markerID(fmt.Sprintf("binding/%d/%d/0", 2-t, bl)))
		}
	}
	return nil
}
