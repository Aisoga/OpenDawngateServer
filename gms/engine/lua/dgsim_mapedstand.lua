-- MAPED-IMPORT-SM-DRESS live probe ([host] test_lua=<from>-<to>/<step>:dgsim_mapedstand.lua): each call teleports
-- player 0's shaper to the next spot of SM_Map_v5's places, which CM_Stand (the package mapc import --dress made from
-- it) shares, so the same session recipe on both maps gives pictures at the same spots (pshot a few seconds after
-- each call; a locked camera follows). Team 1's spots come first, then their mirrors on team 2's half (CM_Stand's
-- team 2 wears team 1's dressing turned, SM_Map_v5 its own). After the last spot the list starts again. Report
-- only: one line per call.
local out = {}
local function p(s) out[#out + 1] = s end

local spots = dgsim_mapedstand_spots or {
  {-232.0, -230.0, 'team 1 spawn: the dais and the Locus'},
  {-215.0, -248.0, "the base's south edge: the waterfall scene past the level's edge"},
  {-182.0, -170.0, 'the Guardian (12 m south): its platform'},
  {-152.0, -111.0, "the top lane's Inner Binding (6 m west): its platform"},
  {-59.0, -50.0, 'the Fungus buff camp: the jungle'},
  {30.0, -200.0, "the river by the south well (the ford's water)"},
  {-250.0, 0.0, "the west edge: the backdrop past the level's edge"},
  {-6.0, -6.0, "the centre: the parasite's pit"},
  {232.0, 230.0, "team 2's spawn"},
  {215.0, 248.0, "team 2's base edge: the waterfall scene's mirror"},
  {152.0, 111.0, "team 2's Inner Binding: its platform"},
  {-30.0, 200.0, "the river by team 2's well"},
}

local ok, err = pcall(function()
  dgsim_mapedstand_n = (dgsim_mapedstand_n or 0) + 1
  local n = dgsim_mapedstand_n
  local t = nGameSimulator.GetMatchTime()
  local s = spots[(n - 1) % #spots + 1]
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p(string.format('mapedstand spot %d at match %.2f: no shaper for player 0', n, t))
    return
  end
  nLocomotion.TeleportToPositionAndFacing(h, nVector.create(s[1], s[2], 0), nVector.create(0, 1, 0))
  local at = nGameObject.GetPosition(h)
  p(string.format('mapedstand spot %d (%d of %d) at match %.2f: player 0 at (%.1f %.1f), asked (%.1f %.1f): %s', n,
    (n - 1) % #spots + 1, #spots, t, at.x, at.y, s[1], s[2], s[3]))
end)
if not ok then p('mapedstand: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
