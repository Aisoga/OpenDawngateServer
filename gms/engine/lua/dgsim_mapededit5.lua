-- MAPED-EDIT5 live probe ([host] test_lua=20-200/12:dgsim_mapededit5.lua): each call teleports player 0's shaper to
-- the next spot of the test map CM_Marsh (server/testdata/maps/CM_Marsh: the pond, the hub's rock, the pasted flowers, the moved well,
-- a bush, a painted path, the bent lane, the centre's pillar), so a locked camera frames it for the next pictures;
-- after the last spot the list starts again (a second pass).
-- Report only: one line per call.
local out = {}
local function p(s) out[#out + 1] = s end

local spots = {
  {-95.0, -95.0, 'the pond: water, lily pads, mangrove, the moss PNG layer, the scatter'},
  {-50.0, -58.0, 'the hub: the blocking rock with its shadow card'},
  {-128.0, -110.0, 'the base clearing: the pasted SM_Map_v5 flower group'},
  {-136.0, -58.0, 'the moved well with its stamped disc'},
  {-99.0, -80.0, 'in the new pond-side bush'},
  {-88.0, -32.0, 'the painted path from the hub to the buff camp'},
  {-180.0, -40.0, 'lane 1 where its knots moved west'},
  {-6.0, -24.0, 'the centre: the new wall pillar'},
}

local ok, err = pcall(function()
  dgsim_mapededit5_n = (dgsim_mapededit5_n or 0) + 1
  local n = dgsim_mapededit5_n
  local t = nGameSimulator.GetMatchTime()
  local s = spots[(n - 1) % #spots + 1]
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p(string.format('mapededit5 spot %d at match %.2f: no shaper for player 0', n, t))
    return
  end
  nLocomotion.TeleportToPositionAndFacing(h, nVector.create(s[1], s[2], 0), nVector.create(0, 1, 0))
  local at = nGameObject.GetPosition(h)
  p(string.format('mapededit5 spot %d (pass %d) at match %.2f: player 0 at (%.1f %.1f): %s', n,
    math.floor((n - 1) / #spots) + 1, t, at.x, at.y, s[3]))
end)
if not ok then p('mapededit5: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
