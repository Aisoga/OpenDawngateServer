package mapc

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/botprofiles"
	"dawngate/server/internal/setup/localepatch"
	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

func obj(kv ...any) *assetdata.Object {
	o := assetdata.NewObject()
	for i := 0; i < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// madeUpMarker is a made-up template marker: a noun, an id, a place and more members.
func madeUpMarker(noun string, id uint64, x, y, z float32, kv ...any) *assetdata.Object {
	m := obj("markerName", noun, "markerId", id, "nounDef", noun, "pos", []any{x, y, z},
		"rotDegrees", []any{float32(0), float32(0), float32(id % 360)}, "scale", float32(1), "visible", true)
	for i := 0; i < len(kv); i += 2 {
		m.Set(kv[i].(string), kv[i+1])
	}
	return m
}

func ids(xs ...uint64) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

// madeUpAssetData is a made-up AssetData.snap holding what the compiler copies from (the template level's Level,
// its gameplay markersets, a light markerset, the terrain noun): every noun once or twice at made-up places, no
// game bytes.
func madeUpAssetData(t *testing.T) []byte {
	t.Helper()
	ts, err := assetdata.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	knot := func(id uint64, k int64, x, y float32) *assetdata.Object {
		return madeUpMarker("SplinePathNode.Noun", id, x, y, 0.05, "splinePathNodeData",
			obj("name", "blue_top", "knot", k, "lane", int64(1), "team", int64(1), "deleteInGame", k > 0))
	}
	spawner := func(noun string, id uint64, x, y float32, team string) *assetdata.Object {
		return madeUpMarker(noun, id, x, y, 0.01, "componentData", obj("spawnPointDef",
			obj("spawnType", "minions", "spawnTeam", team, "spawnDelay", float32(60))))
	}
	island := func(id uint64, x, y float32, players bool) *assetdata.Object {
		return madeUpMarker("navmeshmarker_valid.Noun", id, x, y, 0, "navmeshMarkerData",
			obj("allowPlayersHere", players, "allowNPCsHere", true))
	}
	camNode := func(name string, k int64) *assetdata.Object {
		return obj("name", name, "fov", float32(47), "near", float32(5), "far", float32(2000), "knot", k)
	}
	sets := map[string][]any{
		"design": {
			madeUpMarker("SpawnPoint_Team1.Noun", 101, -230, -226, 0.04), madeUpMarker("SpawnPoint_Team1.Noun", 102, -226, -230, 0.04),
			madeUpMarker("SpawnPoint_Team2.Noun", 103, 230, 226, 0.09), madeUpMarker("SpawnPoint_Team2.Noun", 104, 226, 230, 0.09),
			madeUpMarker("Base_Health_Regen_Defenders.Noun", 105, -250, -250, 0.09),
			madeUpMarker("Base_Health_Regen_Attackers.Noun", 106, 250, 250, 0.09),
			madeUpMarker("itemshoppe_archon_Mortal.Noun", 107, -208, -253, 5), madeUpMarker("itemshoppe_archon_Spirit.Noun", 108, 208, 253, 5),
			island(109, -182, -182, false), island(110, -165, -198, true),
		},
		"generated_grass": {madeUpMarker("Grass.Noun", 201, 90, -6, 1, "grassData", obj("diffuse", "grass_D.dds",
			"cut", "images/32bit/Made_Up_GrassMap.tga", "outline", "path1", "size", []any{float32(12), float32(10), float32(4)},
			"density", float32(2.15), "seed", int64(54549), "enable", true))},
		"environment": {madeUpMarker(templateTerrainNoun, 301, 0, 0, 0)},
		"followers": {
			madeUpMarker("gold_blue_side.Noun", 401, -182, 28, -0.37, "childMarkerIds", ids(402, 403)),
			madeUpMarker("follower_icon.Noun", 402, -166, 13, 0.55, "targetMarkerId", uint64(404)),
			madeUpMarker("follower_icon.Noun", 403, -178, 49, 0.05),
			madeUpMarker("follower_icon.Noun", 404, -160, 10, 0.55),
			madeUpMarker("gold_red_side.Noun", 405, 182, -28, -0.37, "childMarkerIds", ids(406)),
			madeUpMarker("follower_icon.Noun", 406, 166, -13, 0.55),
		},
		"minion_blue": {
			knot(501, 0, -210, -185), knot(502, 1, -172, -146), knot(503, 2, -153, -115),
			spawner("SpawnPoint_v5_Mortal_Top.Noun", 504, -225, -200, "blue"),
			spawner("SpawnPoint_v5_Mortal_Bottom.Noun", 505, -200, -225, "blue"),
			spawner("spawnpoint_Mortal_Strider.Noun", 506, -200, -225, "blue"),
			spawner("spawnpoint_Mortal_Strider.Noun", 507, -225, -200, "blue"),
		},
		"minion_red": {
			spawner("SpawnPoint_v5_Spirit_Top.Noun", 601, 200, 225, "red"),
			spawner("SpawnPoint_v5_Spirit_Bottom.Noun", 602, 225, 200, "red"),
			spawner("spawnpoint_Spirit_Strider.Noun", 603, 225, 200, "red"),
		},
		"titans": {
			madeUpMarker("TitanSpawnPoint.Noun", 701, -182, -182, 0.54), madeUpMarker("TitanSpawnPoint.Noun", 702, 182, 182, 0.54),
			madeUpMarker("titan_node_blue_base_v5.Noun", 703, -182, -182, 0.54), madeUpMarker("titan_node_red_base_v5.Noun", 704, 182, 182, 0.54),
			madeUpMarker("SpawnPoint_Objective_Titan.Noun", 705, -3, 3, 0.1, "childMarkerIds", ids(706)),
			madeUpMarker("SpawnPoint_CreepMarker.Noun", 706, 0, 0, 0),
		},
		// two camps of one noun (one a side) and a buff camp, each with its creature spots
		"jungle_creeps": {
			madeUpMarker("SpawnPoint_v5_CreepsUgger.Noun", 1201, 170, 9, 0.1, "childMarkerIds", ids(1202, 1203)),
			madeUpMarker("SpawnPoint_CreepMarker.Noun", 1202, 172, 6, 0.1),
			madeUpMarker("SpawnPoint_CreepMarker.Noun", 1203, 171, 12, 0.1),
			madeUpMarker("SpawnPoint_v5_CreepsUgger.Noun", 1204, -171, -9, 0.1, "rotDegrees", []any{float32(0), float32(0), float32(-108)},
				"childMarkerIds", ids(1205, 1206)),
			madeUpMarker("SpawnPoint_CreepMarker.Noun", 1205, -172, -6, 0.1),
			madeUpMarker("SpawnPoint_CreepMarker.Noun", 1206, -172, -13, 0.1, "rotDegrees", []any{float32(0), float32(0), float32(170)}),
			madeUpMarker("SpawnPoint_v5_Buff_Fungus.Noun", 1207, -59, -57, 0.1, "childMarkerIds", ids(1208)),
			madeUpMarker("SpawnPoint_CreepMarker.Noun", 1208, -53, -52, 0.1),
		},
		"towers": {
			madeUpMarker("DEST_prefab_blue_tower_v5.Noun", 801, -242, -242, 0.5),
			madeUpMarker("DEST_prefab_red_tower_v5.Noun", 802, 242, 242, 0.55),
		},
		"nubs":         {},
		"cameraMortal": {madeUpMarker("SplineCamera.Noun", 901, -158, -200, 56, "splineCameraData", obj("node", camNode("cameraMortal", 0), "duration", float32(7))), madeUpMarker("SplineCameraNode.Noun", 902, -148, -151, 36, "splineCameraNodeData", obj("node", camNode("cameraMortal", 1)))},
		"cameraSpirit": {madeUpMarker("SplineCamera.Noun", 903, 158, 200, 56, "splineCameraData", obj("node", camNode("cameraSpirit", 0), "duration", float32(7))), madeUpMarker("SplineCameraNode.Noun", 904, 148, 151, 36, "splineCameraNodeData", obj("node", camNode("cameraSpirit", 1)))},
		"World_Lights": {
			madeUpMarker("ParallelLight.Noun", 1001, -214, -158, 5, "parallelLightData", obj("diffuse_lamp_power", float32(0.75), "type", "global0")),
			madeUpMarker("PointLight.Noun", 1002, -130, -220, 11),
			madeUpMarker("GraphicsControl.Noun", 1003, -51, -1, 0.09),
		},
	}
	id := uint64(1100)
	for _, team := range []string{"Mortal", "Spirit"} {
		sign := float32(-1)
		if team == "Spirit" {
			sign = 1
		}
		for k, tier := range Tiers {
			for lane := range 2 { // one in each half of the diagonal
				x, y := sign*float32(40+40*k), sign*float32(120-40*k)
				if lane == 1 {
					x, y = y, x
				}
				id++
				sets["towers"] = append(sets["towers"], madeUpMarker(fmt.Sprintf("Binding_%s_%s.Noun", team, tier), id, x, y, -0.08))
			}
		}
		for k := range 5 {
			id++
			sets["nubs"] = append(sets["nubs"], madeUpMarker(fmt.Sprintf("nub_%s_v5_%c.Noun", strings.ToLower(team), 'A'+k),
				id, sign*float32(150+k), sign*float32(150-k), -0.09))
		}
	}
	var files []snap.File
	write := func(asset string, o *assetdata.Object) {
		b, err := ts.Write(ts.Type(classOfAsset(asset)), o, assetdata.WriteOptions{})
		if err != nil {
			t.Fatalf("%s: %v", asset, err)
		}
		files = append(files, snap.File{Name: AssetEntry(asset), Data: b})
	}
	var list []any
	for _, s := range append(append([]string{}, templateSets...), "World_Lights") {
		write(TemplateLevel+"_"+s+".Markerset", obj("markers", sets[s], "group", "none", "weight", float32(1)))
		list = append(list, obj("markersetAsset", TemplateLevel+"_"+s+".Markerset"))
	}
	write(TemplateLevel+".Level", obj("markersets", list, "music", "made_up_music",
		"gameEndScriptNameCustom", "end_custom", "levelSize", []any{float32(500), float32(500)},
		"activeItems", "Made_Up.ItemSet", "rolesTuning", "Made_Up.RolesTuning", "levelUpTuning", "Made_Up.LevelUpTuning",
		"rewardsTuning", "Made_Up.RewardsTuning", "gameplayTuning", "Made_Up.LevelGameplayTuning"))
	write(templateTerrainNoun, obj("nounType", "terrain", "isFixed", true, "modelKey", "made!made_terrain.bmdl",
		"modelPath", "Environments/made/made_terrain.bmdl"))
	write(DecalTemplateSet, obj("markers", []any{
		madeUpMarker("pumpkin.Noun", 1201, 3, 4, 5),
		madeUpMarker(decalNoun, 1202, -206, -207, 5.4, "decalData", obj("size", []any{float32(3.5), float32(3.5), float32(5.3)},
			"diffuse", `Environments\made\letters.dds`, "diffuseTint", []any{float32(1), float32(1), float32(1)},
			"opacity", float32(1), "tile", []any{float32(1), float32(1)}, "normalLevel", float32(2), "enable", true)),
	}, "group", "none", "weight", float32(1)))
	// MineLevel's followers: a mine a team, 3 worker spots (team 1's first with a look-at) and a loose icon
	write(MineLevel+"_"+setFollowers+".Markerset", obj("markers", []any{
		madeUpMarker("gold_blue_side_MM.Noun", 1301, -260, -260, 0.1, "childMarkerIds", ids(1302, 1303, 1304)),
		madeUpMarker("follower_icon.Noun", 1302, -244, -260, 0.5, "targetMarkerId", uint64(1305)),
		madeUpMarker("follower_icon.Noun", 1303, -260, -243, 0.5),
		madeUpMarker("follower_icon.Noun", 1304, -273, -270, 0.5),
		madeUpMarker("follower_icon.Noun", 1305, -255, -262, 0.5),
		madeUpMarker("gold_red_side_MM.Noun", 1311, 260, 260, 0.1, "childMarkerIds", ids(1312, 1313, 1314)),
		madeUpMarker("follower_icon.Noun", 1312, 244, 260, 0.5),
		madeUpMarker("follower_icon.Noun", 1313, 260, 243, 0.5),
		madeUpMarker("follower_icon.Noun", 1314, 273, 270, 0.5),
	}, "group", "none", "weight", float32(1)))
	return snap.Pack(files)
}

// madeUpBounds are made-up ground model bounds (centre, radius, half extents, radius squared).
var madeUpBounds = []float32{0, 0, -2, 300, 250, 250, 10, 90000}

// checkAssets reads every asset back (exactly, the bytes again from the value read) and checks the markers'
// references: ids unique and non-zero, every target and child a marker of the level, every minion spawner pointing
// at a spline's head knot, the Level naming the map's markersets.
func checkAssets(t *testing.T, m *Map, as *MapAssets) {
	t.Helper()
	ts, err := assetdata.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(as.Names, ",") != strings.Join(MapAssetNames(m), ",") {
		t.Errorf("assets %v", as.Names)
	}
	for _, n := range as.Names {
		typ := ts.Type(classOfAsset(n))
		back, err := ts.ParseExact(typ, as.Files[n])
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if a, b := assetdata.AppendJSON(nil, back), assetdata.AppendJSON(nil, as.Values[n]); !bytes.Equal(a, b) {
			t.Errorf("%s reads back as\n%s\nnot\n%s", n, a, b)
		}
		again, err := ts.Write(typ, back, assetdata.WriteOptions{})
		if err != nil || !bytes.Equal(again, as.Files[n]) {
			t.Errorf("%s: written again from its value: other bytes (%v)", n, err)
		}
	}
	byID := map[uint64]*assetdata.Object{}
	var all []*assetdata.Object
	for _, n := range as.Names {
		if !strings.HasSuffix(n, ".Markerset") {
			continue
		}
		for _, mk := range markers(as.Values[n]) {
			id := uval(mk, "markerId")
			if id == 0 || byID[id] != nil {
				t.Errorf("%s: marker id %d zero or taken", n, id)
			}
			byID[id] = mk
			all = append(all, mk)
		}
	}
	heads := 0
	for _, mk := range all {
		if tg := uval(mk, "targetMarkerId"); tg != 0 && byID[tg] == nil {
			t.Errorf("%s: target %d not on the level", text(mk, "markerName"), tg)
		}
		for _, c := range vec(mk, "childMarkerIds") {
			if byID[uint64(c)] == nil {
				t.Errorf("%s: child %d not on the level", text(mk, "markerName"), uint64(c))
			}
		}
		if n := text(mk, "nounDef"); strings.HasPrefix(n, "SpawnPoint_v5_Mortal_") || strings.HasPrefix(n, "SpawnPoint_v5_Spirit_") {
			h := byID[uval(mk, "targetMarkerId")]
			d := sub(h, "splinePathNodeData")
			if d == nil || fval(mustGet(d, "knot")) != 0 || fval(mustGet(d, "lane")) == 0 || fval(mustGet(d, "team")) == 0 {
				t.Errorf("%s doesn't point at a spline's head knot", text(mk, "markerName"))
			}
			heads++
		}
	}
	if want := 2 * len(m.Lanes); heads != want {
		t.Errorf("%d minion spawners, %d expected", heads, want)
	}
	sets, err := levelSets(as.Values[m.Name+".Level"])
	if err != nil || len(sets) != len(setsOf(m)) {
		t.Errorf("the Level's markersets %v (%v)", sets, err)
	}
}

// madeUpAssets are CM_Ring's assets from the made-up AssetData.
func madeUpAssets(t *testing.T) (*Map, *MapAssets) {
	t.Helper()
	m := ring(t)
	r, cm := Check(m, nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	as, err := cm.Assets(madeUpAssetData(t), madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	return m, as
}

// TestAssetsMadeUp: CM_Ring's assets from made-up templates read back as written, with sound references; the
// counts per markerset; the Level's fields.
func TestAssetsMadeUp(t *testing.T) {
	m, as := madeUpAssets(t)
	checkAssets(t, m, as)
	got := as.MarkerCount()
	// per team: 2 lanes of 10 knots, a minion and a lane-agent spawner each; 5 spawn points, the zone, an archon, 2
	// islands; a well with 2 spots and 1 look-at (team 1) or 1 spot (team 2); 6 Bindings, the Locus, 5 Cores; the
	// spot and the node (CM_Ring has no camps); the environment: the ground and 2 decals with their mirrors
	want := map[string]int{"environment": 5, "lights": 2, "generated_grass": 8, "design": 18, "followers": 6,
		"minion_blue": 24, "minion_red": 24, "titans": 4, "towers": 14, "nubs": 10, "cameraMortal": 2,
		"cameraSpirit": 2}
	for s, n := range want {
		if got[m.Name+"_"+s+".Markerset"] != n {
			t.Errorf("%s: %d markers, %d expected", s, got[m.Name+"_"+s+".Markerset"], n)
		}
	}
	lv := as.Values[m.Name+".Level"]
	e := EntriesFor(m.Name)
	for k, v := range map[string]string{"navMeshPlayer": "CM_Ring!CM_Ring_Player.bfx", "navMeshPathNPC": e.NPC,
		"physicsMesh": "CM_Ring!CM_Ring.bin", "lineOfSightMap": e.LoS, "grassMap": e.Grass, "tagMap": e.TagImage,
		"miniMap": e.Minimap, "outlinePath": e.Outlines, "music": "made_up_music", "energyLineTuning": EnergyLines,
		"gameplayTuning": "Made_Up.LevelGameplayTuning", "gameEndScriptNameCustom": "end_custom"} {
		if got := text(lv, k); got != v {
			t.Errorf("Level %s %q, %q expected", k, got, v)
		}
	}
	if s := vec(lv, "levelSize"); len(s) != 2 || s[0] != m.Size || s[1] != m.Size {
		t.Errorf("levelSize %v", s)
	}
	n := as.Values[TerrainNoun(m.Name)]
	if text(n, "modelPath") != e.Model || text(n, "modelKey") != "cm_ring!cm_ring_terrain.bmdl" {
		t.Errorf("the terrain noun names %q, %q", text(n, "modelKey"), text(n, "modelPath"))
	}
	// the grass markers name the bushes' outlines and the map's grass map
	_, cm := Check(m, nil)
	for k, mk := range markers(as.Values[m.Name+"_generated_grass.Markerset"]) {
		g := sub(mk, "grassData")
		if text(g, "outline") != cm.Layers.Bushes[k].Name || text(g, "cut") != e.Grass {
			t.Errorf("grass marker %d: %s, %s", k, text(g, "outline"), text(g, "cut"))
		}
	}
	// the decals: the template's decal data with the map's texture, size and turn; the mirror opposite
	env := markers(as.Values[m.Name+"_environment.Markerset"])
	d0, d1 := env[1], env[2]
	if text(d0, "nounDef") != decalNoun || text(sub(d0, "decalData"), "diffuse") != m.Ground.Decals[0].Diffuse ||
		fval(vec(d0, "rotDegrees")[2]) != m.Ground.Decals[0].Rot || vec(sub(d0, "decalData"), "size")[2] != 6 ||
		fval(mustGet(sub(d0, "decalData"), "normalLevel")) != 2 {
		t.Errorf("decal 0: %v", d0)
	}
	if p0, p1 := vec(d0, "pos"), vec(d1, "pos"); p1[0] != -p0[0] || p1[1] != -p0[1] || vec(d1, "rotDegrees")[2] != 210 {
		t.Errorf("decal 0's mirror at %v, %v", p1, vec(d1, "rotDegrees"))
	}
	// the well's spots stand off the well as the template's do
	fol := markers(as.Values[m.Name+"_followers.Markerset"])
	w, s := vec(fol[0], "pos"), vec(fol[1], "pos")
	if dx, dy := s[0]-w[0], s[1]-w[1]; dx != 16 || dy != -15 {
		t.Errorf("the first spot stands (%v, %v) off the well, (16, -15) expected", dx, dy)
	}
}

// TestCampsMadeUp: CM_Ring with two camps and the parasite from the made-up templates: each camp with its creature
// spots at the template's offsets (team 2's turned half round, its heading too), the parasite once, the lane-agent
// spawners on the head knots with the enemy's Outer Binding as their child, and a camp spot on a wall reported.
func TestCampsMadeUp(t *testing.T) {
	m := ring(t)
	m.Camps = []Camp{{Kind: CampCamp, Noun: "SpawnPoint_v5_CreepsUgger.Noun", Pos: Point{-80, -170}},
		{Kind: CampBuff, Noun: "SpawnPoint_v5_Buff_Fungus.Noun", Pos: Point{-170, -60}},
		{Kind: CampParasite, Noun: "SpawnPoint_Objective_Titan.Noun", Pos: Point{-172, 20}}}
	r, cm := Check(m, nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	as, err := cm.Assets(madeUpAssetData(t), madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	checkAssets(t, m, as)
	ms := markers(as.Values[m.Name+"_titans.Markerset"])
	byID := map[uint64]*assetdata.Object{}
	for _, n := range as.Names {
		if strings.HasSuffix(n, ".Markerset") {
			for _, mk := range markers(as.Values[n]) {
				byID[uval(mk, "markerId")] = mk
			}
		}
	}
	type camp struct {
		pos  []float64
		rot  float64
		kids [][]float64
	}
	got := map[string][]camp{}
	for _, mk := range ms {
		n := text(mk, "nounDef")
		if !strings.HasPrefix(n, "SpawnPoint_v5_") && !strings.HasPrefix(n, "SpawnPoint_Objective") {
			continue
		}
		c := camp{pos: vec(mk, "pos"), rot: vec(mk, "rotDegrees")[2]}
		for _, k := range vec(mk, "childMarkerIds") {
			c.kids = append(c.kids, vec(byID[uint64(k)], "pos"))
		}
		got[n] = append(got[n], c)
	}
	// the Ugger: team 1's from the template in team 1's half (-171, -9), spots (-1, 3) and (-1, -4) off it
	ug := got["SpawnPoint_v5_CreepsUgger.Noun"]
	if len(ug) != 2 || len(ug[0].kids) != 2 || len(ug[1].kids) != 2 {
		t.Fatalf("Ugger camps %+v", ug)
	}
	for k, want := range [][2]float64{{-1, 3}, {-1, -4}} {
		if dx, dy := ug[0].kids[k][0]-ug[0].pos[0], ug[0].kids[k][1]-ug[0].pos[1]; dx != want[0] || dy != want[1] {
			t.Errorf("team 1's Ugger spot %d (%v, %v) off the camp, %v expected", k, dx, dy, want)
		}
		if dx, dy := ug[1].kids[k][0]-ug[1].pos[0], ug[1].kids[k][1]-ug[1].pos[1]; dx != -want[0] || dy != -want[1] {
			t.Errorf("team 2's Ugger spot %d (%v, %v) off the camp, turned %v expected", k, dx, dy, want)
		}
	}
	if ug[1].pos[0] != 80 || ug[1].pos[1] != 170 || ug[0].rot != -108 || ug[1].rot != 72 {
		t.Errorf("team 2's Ugger at %v, headings %v and %v (-108 and 72 expected)", ug[1].pos, ug[0].rot, ug[1].rot)
	}
	if p := got["SpawnPoint_Objective_Titan.Noun"]; len(p) != 1 || p[0].kids[0][0] != -169 || p[0].kids[0][1] != 17 {
		t.Errorf("the parasite %+v: once, its creature (3, -3) off it as the template's", p)
	}
	if b := got["SpawnPoint_v5_Buff_Fungus.Noun"]; len(b) != 2 {
		t.Errorf("buff camps %+v", b)
	}
	// the lane-agent spawners: a lane each, at the minion spawner, on the head knot, the enemy's Outer as the child
	agents := 0
	for _, set := range []string{"minion_blue", "minion_red"} {
		for _, mk := range markers(as.Values[m.Name+"_"+set+".Markerset"]) {
			if !strings.HasSuffix(text(mk, "nounDef"), "_Strider.Noun") {
				continue
			}
			agents++
			h := sub(byID[uval(mk, "targetMarkerId")], "splinePathNodeData")
			kids := vec(mk, "childMarkerIds")
			if h == nil || fval(mustGet(h, "knot")) != 0 || len(kids) != 1 ||
				!strings.Contains(text(byID[uint64(kids[0])], "nounDef"), "_Outer") {
				t.Errorf("%s: head %v, children %v", text(mk, "markerName"), h, kids)
			}
		}
	}
	if agents != 4 {
		t.Errorf("%d lane-agent spawners, 4 expected", agents)
	}
	// a camp in a wall: its spots are reported
	m.Camps[0].Pos = Point{-60, -100}
	if _, cm = Check(m, nil); cm == nil {
		t.Fatal("no compiled map")
	}
	if as, err = cm.Assets(madeUpAssetData(t), madeUpBounds); err != nil {
		t.Fatal(err)
	}
	if len(as.Findings) != 1 || as.Findings[0].Rule != "camps.spots" || as.Findings[0].Path != "camps[0].pos" {
		t.Errorf("findings %+v", as.Findings)
	}
}

// TestGameAssets builds CM_Ring's assets from build 854's AssetData with each environment and tuning: they read
// back, their references hold, the environment's global lights and the tuning level's tunings are named, and the
// copy's one catalog takes their entries.
func TestGameAssets(t *testing.T) {
	dir := testgame.Dir(t)
	in, err := ReadInputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		level  string
		lights int
		tuning string
	}{{"SM_Map_v5", 4, "Tuning.LevelGameplayTuning"}, {"MM_Map_v3", 3, "Tuning_MM_V3.LevelGameplayTuning"}} {
		m := ring(t)
		m.Environment, m.Tuning = c.level, c.level
		r, cm := Check(m, nil)
		if !r.OK {
			t.Fatalf("%+v", r.Findings)
		}
		as, err := cm.Assets(in.AssetData, madeUpBounds)
		if err != nil {
			t.Fatal(err)
		}
		checkAssets(t, m, as)
		counts := as.MarkerCount()
		t.Logf("%s: %v; %d findings", c.level, counts, len(as.Findings))
		if n := counts["CM_Ring_lights.Markerset"]; n != c.lights {
			t.Errorf("%s: %d lights, %d expected", c.level, n, c.lights)
		}
		if n := counts["CM_Ring_followers.Markerset"]; n != 2*37 {
			t.Errorf("%s: %d well markers, 74 expected (a well, 18 spots and their 18 targets a side)", c.level, n)
		}
		if g := text(as.Values["CM_Ring.Level"], "gameplayTuning"); g != c.tuning {
			t.Errorf("%s: gameplayTuning %s", c.level, g)
		}
		if len(as.Findings) != 0 {
			t.Errorf("%s: %+v", c.level, as.Findings)
		}
		arch, err := botprofiles.Build(in.AssetData, Catalog(m))
		if err != nil {
			t.Fatal(err)
		}
		a, err := snap.Parse(arch)
		if err != nil {
			t.Fatal(err)
		}
		var cat []byte
		for _, en := range a.Entries {
			if en.Name == botprofiles.Catalog {
				if cat, err = a.Read(en); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, n := range as.Names {
			if !bytes.Contains(cat, []byte(n+"\x00")) {
				t.Errorf("the catalog archive lacks %s", n)
			}
		}
	}
}

// TestAssetNamesAndCatalog:the asset names carry the map's name, and the catalog source lists each once with its
// own source file.
func TestAssetNamesAndCatalog(t *testing.T) {
	m := ring(t)
	src := Catalog(m)
	if src.Name != m.Name || len(src.Entries) != len(AssetNames(m.Name)) {
		t.Fatalf("%+v", src)
	}
	sources := map[string]bool{}
	for i, e := range src.Entries {
		if e.Name != AssetNames(m.Name)[i] || !strings.HasPrefix(e.Name, m.Name) || e.Base != "" || sources[e.Source] {
			t.Errorf("entry %+v", e)
		}
		sources[e.Source] = true
	}
	if last := src.Entries[len(src.Entries)-1]; strings.Join(last.Tags, ",") != "placeable,terrain" {
		t.Errorf("the terrain noun's tags %v", last.Tags)
	}
}

// TestLabel: the label's map list line is the one the client copy's list writer makes, and its locale line is the
// line of the map's LevelId in the lobby's table (MAPTABLE; dgserver's id for CM_Ring, 4050252090).
func TestLabel(t *testing.T) {
	m := ring(t)
	l := LabelOf(m)
	if l.LocaleID != localepatch.LevelID(m.Name) || l.LocaleID != 0xF169F13A || l.LocaleText != m.Display || l.Archive != "zz_CM_Ring.snap" {
		t.Errorf("%+v", l)
	}
	list := moviepatch.MapList([]moviepatch.Map{{Tag: m.Tag, Archive: l.Archive, Label: m.Display}})
	if !bytes.Contains(list, []byte(l.MapListLine)) || l.MapListLine != "map\tring\tTest Ring\n" {
		t.Errorf("%q not in the map list", l.MapListLine)
	}
}
