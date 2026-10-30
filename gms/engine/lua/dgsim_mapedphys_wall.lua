-- MAPED-PHYS probe ([host] test_lua=<from>-<to>/<step>:dgsim_mapedphys_wall.lua): the human (player 0) teleported onto
-- the rock wall at W = (-84, -77) on MM_Map_v3 (a blocked cell of the line-of-sight map, 3.05 m high in the pinned
-- mesh, floor only in the scratch one) unless already within 1.5 m, so the client's camera and the client's placement
-- of the creature stand over a wall (the session's fshots). Logs where the server put it and the terrain height there
-- (from z 0 and from 5 m). Every line starts with "mapedphys". Moves nothing but the human.
local W = {-84.0, -77.0}
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'mapedphys ' .. string.format('%.2f', nGameSimulator.GetMatchTime()) .. ' ' .. table.concat(t, ' ')
end
local function vec(v)
  if v == nil then return 'nil' end
  return string.format('(%.2f, %.2f, %.3f)', v.x, v.y, v.z)
end
local ok, err = pcall(function()
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone or not nObjectManager.IsValidObject(h) then
    p('wall: no creature for player 0')
    return
  end
  local pos = nGameObject.GetPosition(h)
  local dx, dy = pos.x - W[1], pos.y - W[2]
  if not nGameObject.IsAlive(h) then
    p('wall: the human is dead at', vec(pos))
  elseif dx * dx + dy * dy > 1.5 * 1.5 then
    nLocomotion.TeleportToPositionAndFacing(h, nVector.create(W[1], W[2], pos.z), nVector.create(1, 0, 0))
    p('wall: human from', vec(pos), 'to', vec(nGameObject.GetPosition(h)))
  else
    p('wall: human still at', vec(pos))
  end
  local z0 = nLocomotion.GetTerrainHeightAtPosition(nVector.create(W[1], W[2], 0))
  local z5 = nLocomotion.GetTerrainHeightAtPosition(nVector.create(W[1], W[2], 5))
  p('height wall', string.format('(%.1f, %.1f)', W[1], W[2]), string.format('z0 %.3f', z0), string.format('from 5 m %.3f', z5))
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
