-- EDUX9 live probe ([host] test_lua=20-300/12:dgsim_edux9.lua): each call teleports player 0's shaper to the next
-- spot of the test map CM_Heights (the CM_Marsh fixture with a 12-m and a 24-m
-- ridge south of lane 2 and a 15-m chasm east of the hub), so a locked camera frames it for the next pictures; after
-- the last spot the list starts again (a second pass). It also reports the shaper's z (the server's floor).
-- Report only: one line per call.
local out = {}
local function p(s) out[#out + 1] = s end

local spots = {
  {-41.0, -176.0, 'lane 2 north of the 12-m ridge'},
  {-3.0, -176.0, 'lane 2 north of the 24-m ridge (the zoomed-in eye)'},
  {-20.0, -88.0, 'the path south of the 15-m chasm'},
  {-46.0, -60.0, 'the path west of the 15-m chasm'},
}

local ok, err = pcall(function()
  dgsim_edux9_n = (dgsim_edux9_n or 0) + 1
  local n = dgsim_edux9_n
  local t = nGameSimulator.GetMatchTime()
  local s = spots[(n - 1) % #spots + 1]
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p(string.format('edux9 spot %d at match %.2f: no shaper for player 0', n, t))
    return
  end
  nLocomotion.TeleportToPositionAndFacing(h, nVector.create(s[1], s[2], 0), nVector.create(0, 1, 0))
  local at = nGameObject.GetPosition(h)
  p(string.format('edux9 spot %d (pass %d) at match %.2f: player 0 at (%.1f %.1f %.2f): %s', n,
    math.floor((n - 1) / #spots) + 1, t, at.x, at.y, at.z, s[3]))
end)
if not ok then p('edux9: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
