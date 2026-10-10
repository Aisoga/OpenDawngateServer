-- D12e probe ([host] test_lua=<s>:dgsim_d12e_terrain.lua, needs dgsim_d12.lua loaded first): the server's terrain height
-- (nLocomotion.GetTerrainHeightAtPosition) on a grid over the map, and what z the server gives a creature teleported to
-- the highest and the lowest grid point (the first run teleports the human with its own z, the second run prints the
-- human's position, one or more ticks later). dgsim_d12e_grid = {step, min, max} may override the grid.
local D = dgsim_d12
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'd12e ' .. string.format('%.2f', nGameSimulator.GetMatchTime()) .. ' ' .. table.concat(t, ' ')
end
local ok, err = pcall(function()
  local h = D and D.human or nPlayer.GetPlayerControlledObjectID(0)
  if dgsim_d12e_tp == nil then
    local step, lo, hi = 40, -280, 280
    local best, worst
    for x = lo, hi, step do
      local row = {}
      for y = lo, hi, step do
        local z = nLocomotion.GetTerrainHeightAtPosition(nVector.create(x, y, 0))
        row[#row + 1] = string.format('%5.1f', z)
        if best == nil or z > best.z then best = {x = x, y = y, z = z} end
        if worst == nil or z < worst.z then worst = {x = x, y = y, z = z} end
      end
      p('grid x', x, 'y from', lo, 'step', step, ':', table.concat(row, ' '))
    end
    p('highest', best.x, best.y, best.z, 'lowest', worst.x, worst.y, worst.z)
    dgsim_d12e_tp = {best, worst, n = 0}
    local pos = nGameObject.GetPosition(h)
    nLocomotion.TeleportToPositionAndFacing(h, nVector.create(best.x, best.y, pos.z), nVector.create(1, 0, 0))
    p('teleported human to the highest point, z given', pos.z)
  else
    local pos = nGameObject.GetPosition(h)
    p('human now', string.format('(%.2f, %.2f, %.3f)', pos.x, pos.y, pos.z), 'terrain here',
      string.format('%.3f', nLocomotion.GetTerrainHeightAtPosition(nVector.create(pos.x, pos.y, 0))))
    dgsim_d12e_tp.n = dgsim_d12e_tp.n + 1
    if dgsim_d12e_tp.n == 2 then
      local w = dgsim_d12e_tp[2]
      nLocomotion.TeleportToPositionAndFacing(h, nVector.create(w.x, w.y, pos.z), nVector.create(1, 0, 0))
      p('teleported human to the lowest point, z given', pos.z)
    end
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
