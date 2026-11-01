-- EDUX9 live probe, the zoomed-in eye ([host] test_lua=20-300/12:dgsim_edux9b.lua): as dgsim_edux9.lua, but each
-- call teleports player 0's shaper to one of two spots on lane 2 just north of CM_Heights' 24-m ridge, 10 and 5 m
-- from its walkable edge, so the zoomed-in camera (its eye about 14 m up and 20 m behind the shaper) stands over
-- the ridge's cliff. Report only: one line per call (the shaper's position and z).
local out = {}
local function p(s) out[#out + 1] = s end

local spots = {
  {-3.0, -176.0, 'lane 2, 10 m north of the 24-m ridge'},
  {-3.0, -181.0, 'lane 2, 5 m north of the 24-m ridge (its walkable edge)'},
}

local ok, err = pcall(function()
  dgsim_edux9b_n = (dgsim_edux9b_n or 0) + 1
  local n = dgsim_edux9b_n
  local t = nGameSimulator.GetMatchTime()
  local s = spots[(n - 1) % #spots + 1]
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p(string.format('edux9b spot %d at match %.2f: no shaper for player 0', n, t))
    return
  end
  nLocomotion.TeleportToPositionAndFacing(h, nVector.create(s[1], s[2], 0), nVector.create(0, 1, 0))
  local at = nGameObject.GetPosition(h)
  p(string.format('edux9b spot %d (pass %d) at match %.2f: player 0 at (%.1f %.1f %.2f): %s', n,
    math.floor((n - 1) / #spots) + 1, t, at.x, at.y, at.z, s[3]))
end)
if not ok then p('edux9b: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
