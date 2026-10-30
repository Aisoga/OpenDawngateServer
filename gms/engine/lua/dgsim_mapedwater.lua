-- Chasm water probe ([host] test_lua=<from>-<to>/<step>:dgsim_mapedwater.lua): each call teleports player 0's shaper
-- to the next spot beside a watered chasm of the test map CM_Water, so a locked camera frames the water and a picture
-- taken a few seconds later shows it; after the last spot the list starts again. Report only: one line a call.
local out = {}
local function p(s) out[#out + 1] = s end

local spots = {
  {-36.0, -36.0, 'the overlook at the centre chasm (water z -1.5, circle mask)'},
  {-104.0, -52.0, 'the path beside the slab chasm, its south end (water z -2, no mask)'},
  {-92.0, -32.0, 'the path beside the slab chasm, its middle (60 x 18 m, turned 60 degrees)'},
  {-82.0, -14.0, 'the path beside the slab chasm, its north end'},
  {92.0, 32.0, 'the mirrored slab chasm (team 2), its middle'},
  {36.0, 36.0, 'the mirrored overlook at the centre chasm'},
}

local ok, err = pcall(function()
  dgsim_mapedwater_n = (dgsim_mapedwater_n or 0) + 1
  local n = dgsim_mapedwater_n
  local t = nGameSimulator.GetMatchTime()
  local s = spots[(n - 1) % #spots + 1]
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p(string.format('mapedwater spot %d at match %.2f: no shaper for player 0', n, t))
    return
  end
  nLocomotion.TeleportToPositionAndFacing(h, nVector.create(s[1], s[2], 0), nVector.create(0, 1, 0))
  local at = nGameObject.GetPosition(h)
  p(string.format('mapedwater spot %d at match %.2f: player 0 at (%.1f %.1f): %s', n, t, at.x, at.y, s[3]))
end)
if not ok then p('mapedwater: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
