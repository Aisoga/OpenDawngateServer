-- MAPPHYS probe ([host] test_lua=<s>:dgsim_mapphys_terrain.lua): the server's terrain height
-- (nLocomotion.GetTerrainHeightAtPosition: a ray down onto the level's PhysX mesh) on a
-- 20-m grid over +-280 m, as a histogram of the heights (2 decimals), and at named points: the human's move goals of
-- the MM_Map_v3 test moves, a wall (outside MM_Map_v3's lane) and the map's centre. Moves and teleports nothing.
-- On a level whose mesh has a floor at 0.088 everywhere (SM_Map_v5's, and the cooked MM_Map_v3's) every height is 0.09.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'mapphys ' .. string.format('%.2f', nGameSimulator.GetMatchTime()) .. ' ' .. table.concat(t, ' ')
end
local function height(x, y, z)
  return nLocomotion.GetTerrainHeightAtPosition(nVector.create(x, y, z or 0))
end
local ok, err = pcall(function()
  local hist, n, lo, hi = {}, 0, nil, nil
  for x = -280, 280, 20 do
    for y = -280, 280, 20 do
      local z = height(x, y)
      local k = string.format('%.2f', z)
      hist[k] = (hist[k] or 0) + 1
      n = n + 1
      if lo == nil or z < lo then lo = z end
      if hi == nil or z > hi then hi = z end
    end
  end
  local keys = {}
  for k in pairs(hist) do keys[#keys + 1] = k end
  table.sort(keys)
  local parts = {}
  for _, k in ipairs(keys) do parts[#parts + 1] = k .. ' x' .. hist[k] end
  p('grid', n, 'points: heights', table.concat(parts, ', '), string.format('(min %.3f, max %.3f)', lo, hi))
  local points = {
    {'move1', -189.9, -205.8}, {'move4 (under an SM_Map_v5 wall)', -94.7, -115.1}, {'move5', -60.7, -60.7},
    {'guardian1', -166.3, -166.3}, {'centre', 0, 0}, {'wall', -150, 100}, {'corner', -280, -280},
  }
  for _, q in ipairs(points) do
    p('point', q[1], string.format('(%.1f, %.1f)', q[2], q[3]), string.format('z0 %.3f', height(q[2], q[3])),
      string.format('from 5 m %.3f', height(q[2], q[3], 5)))
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
