-- D6c test probe ([host] test_lua=<s>:dgsim_d6c_terrain.lua): nLocomotion.GetTerrainHeightAtPosition on the server
-- simulator at the places a Sakari iceberg was cast in a live match (the client's ground is above the server's z 0 there),
-- and at the human shaper's own position. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local ok, err = pcall(function()
  local pts = { {-24.79, 100.17}, {-214.04, -228.11}, {15.35, -107.79}, {66.66, -67.01}, {-90.03, -145.25}, {0, 0}, {-138.93, -198.95} }
  for _, c in ipairs(pts) do
    local h = nLocomotion.GetTerrainHeightAtPosition(nVector.create(c[1], c[2], 0))
    p('terrain height at', c[1], c[2], '=', h)
  end
end)
if not ok then p('terrain: ERROR', err) end
return table.concat(out, '\n')
