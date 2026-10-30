-- MAPED-PHYS probe ([host] test_lua=<s>:dgsim_mapedphys_place.lua, before each R cast of the session's commands
-- file): the human (player 0) teleported to the skillshot spot P = (-93, -77) on MM_Map_v3 (open ground; a rock
-- wall 6.5-12 m east of it, open ground behind it; open ground 44 m south) unless already within 1.5 m, facing +x;
-- Kensu's R cooldown set to 0 and the resource refilled, so the commands file's next R cast starts. Logs the human's
-- position and the server's terrain height at P and on the wall (from z 0 and from 5 m). Every line starts with
-- "mapedphys". Moves nothing but the human.
local P = {-93.0, -77.0}
local WALL = {-84.0, -77.0}
local R = 'ability_Savage_R_StunArrow'
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
local function height(x, y, z)
  return nLocomotion.GetTerrainHeightAtPosition(nVector.create(x, y, z or 0))
end
local ok, err = pcall(function()
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone or not nObjectManager.IsValidObject(h) then
    p('place: no creature for player 0')
    return
  end
  local pos = nGameObject.GetPosition(h)
  local dx, dy = pos.x - P[1], pos.y - P[2]
  if not nGameObject.IsAlive(h) then
    p('place: the human is dead at', vec(pos))
  elseif dx * dx + dy * dy > 1.5 * 1.5 then
    nLocomotion.TeleportToPositionAndFacing(h, nVector.create(P[1], P[2], pos.z), nVector.create(1, 0, 0))
    p('place: human', nGameObject.GetAssetName(h), 'from', vec(pos), 'to', vec(nGameObject.GetPosition(h)))
  else
    p('place: human', nGameObject.GetAssetName(h), 'already at', vec(pos))
  end
  nAbility.SetCooldownRemaining(h, 0, nUtil.SPID(R))
  local max = nGameObject.GetMaxResourcePoints(h)
  if max ~= nil and max > 0 then nGameObject.SetResourcePoints(h, max) end
  p('place: R cooldown reset, resource', tostring(nGameObject.GetResourcePoints(h)), 'of', tostring(max))
  p('height P', string.format('(%.1f, %.1f)', P[1], P[2]), string.format('z0 %.3f', height(P[1], P[2])),
    string.format('from 5 m %.3f', height(P[1], P[2], 5)))
  p('height wall', string.format('(%.1f, %.1f)', WALL[1], WALL[2]), string.format('z0 %.3f', height(WALL[1], WALL[2])),
    string.format('from 5 m %.3f', height(WALL[1], WALL[2], 5)))
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
