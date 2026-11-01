-- EDUX9b live probe, CM_Chasms ([host] test_lua=20-300/12:dgsim_edux9d.lua): as dgsim_edux9c.lua, but each call
-- teleports player 0's shaper to the next of four spots beside the test map's chasms (made by the EDUX9b
-- test map's generator): the camera, which looks north, then shows the chasm beyond or beside the shaper. Report only: one line
-- per call (the shaper's position and z).
local out = {}
local function p(s) out[#out + 1] = s end

local spots = {
  {-15.0, -86.0, 'A1: the ring 7 m south of chasm A (15 m deep, in walkable ground)'},
  {-48.0, -64.0, 'A2: 16 m west of chasm A'},
  {-106.0, 30.0, 'B1: the path 12 m east of chasm B (15 m deep, cut into the walls)'},
  {-12.0, -131.0, 'D1: the path 28 m west of chasm D (5 m deep, cut into the walls)'},
}

local ok, err = pcall(function()
  dgsim_edux9d_n = (dgsim_edux9d_n or 0) + 1
  local n = dgsim_edux9d_n
  local t = nGameSimulator.GetMatchTime()
  local s = spots[(n - 1) % #spots + 1]
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p(string.format('edux9d spot %d at match %.2f: no shaper for player 0', n, t))
    return
  end
  nLocomotion.TeleportToPositionAndFacing(h, nVector.create(s[1], s[2], 0), nVector.create(0, 1, 0))
  local at = nGameObject.GetPosition(h)
  p(string.format('edux9d spot %d (pass %d) at match %.2f: player 0 at (%.1f %.1f %.2f): %s', n,
    math.floor((n - 1) / #spots) + 1, t, at.x, at.y, at.z, s[3]))
end)
if not ok then p('edux9d: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
