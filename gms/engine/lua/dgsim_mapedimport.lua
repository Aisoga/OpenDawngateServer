-- MAPED-IMPORT live probe ([host] test_lua=<from>-<to>/<step>:dgsim_mapedimport.lua): each call teleports player 0's
-- shaper to the next spot of MM_Map_v3's places, which CM_Pass (the package mapc import made from it) shares, so the
-- same session recipe on both maps gives pictures at the same spots (pshot a few seconds after each call; a locked
-- camera follows). After the last spot the list starts again. Report only: one line per call.
local out = {}
local function p(s) out[#out + 1] = s end

local spots = dgsim_mapedimport_spots or {
  {-205.0, -205.0, 'team 1 spawn: the base and the Locus'},
  {-150.0, -170.0, 'beside the Guardian (16 m) and Core C'},
  {-120.0, -120.0, 'in front of Core A'},
  {-92.0, -100.0, 'the Inner Binding'},
  {-90.0, -64.0, 'in the west bush, the wall sliver east of it'},
  {-42.0, -36.0, 'the Middle Binding'},
  {-6.0, -6.0, 'the centre: the pillar and the centre bush'},
  {-212.0, -224.0, 'the base corner towards the mine (on both maps an island only NPCs walk)'},
  {90.0, 64.0, "in the east bush (team 2's half: the mirror)"},
  {205.0, 205.0, "team 2's spawn"},
}

local ok, err = pcall(function()
  dgsim_mapedimport_n = (dgsim_mapedimport_n or 0) + 1
  local n = dgsim_mapedimport_n
  local t = nGameSimulator.GetMatchTime()
  local s = spots[(n - 1) % #spots + 1]
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p(string.format('mapedimport spot %d at match %.2f: no shaper for player 0', n, t))
    return
  end
  nLocomotion.TeleportToPositionAndFacing(h, nVector.create(s[1], s[2], 0), nVector.create(0, 1, 0))
  local at = nGameObject.GetPosition(h)
  p(string.format('mapedimport spot %d (%d of %d) at match %.2f: player 0 at (%.1f %.1f), asked (%.1f %.1f): %s', n,
    (n - 1) % #spots + 1, #spots, t, at.x, at.y, s[1], s[2], s[3]))
end)
if not ok then p('mapedimport: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
