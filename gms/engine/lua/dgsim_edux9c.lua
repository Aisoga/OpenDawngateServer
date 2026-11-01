-- EDUX9 live probe, the chasm ([host] test_lua=20-300/12:dgsim_edux9c.lua): as dgsim_edux9.lua, but each call
-- teleports player 0's shaper to one of two spots beside CM_Heights' 15-m chasm (south and west of it), so
-- the camera, which looks north, shows the chasm beyond the shaper. Report only: one line per call (the shaper's
-- position and z).
local out = {}
local function p(s) out[#out + 1] = s end

local spots = {
  {-20.0, -88.0, 'the path 14 m south of the 15-m chasm'},
  {-46.0, -60.0, 'the path west of the 15-m chasm'},
}

local ok, err = pcall(function()
  dgsim_edux9c_n = (dgsim_edux9c_n or 0) + 1
  local n = dgsim_edux9c_n
  local t = nGameSimulator.GetMatchTime()
  local s = spots[(n - 1) % #spots + 1]
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p(string.format('edux9c spot %d at match %.2f: no shaper for player 0', n, t))
    return
  end
  nLocomotion.TeleportToPositionAndFacing(h, nVector.create(s[1], s[2], 0), nVector.create(0, 1, 0))
  local at = nGameObject.GetPosition(h)
  p(string.format('edux9c spot %d (pass %d) at match %.2f: player 0 at (%.1f %.1f %.2f): %s', n,
    math.floor((n - 1) / #spots) + 1, t, at.x, at.y, at.z, s[3]))
end)
if not ok then p('edux9c: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
