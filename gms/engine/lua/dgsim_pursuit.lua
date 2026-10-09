-- D2g-pursuit test fixture ([host] test_lua=<s>:dgsim_pursuit.lua, several times; docs/GMS_PROTOCOL.md *The last-seen
-- pursuit position (D2g-pursuit)*): an enemy that walks into tall grass and out again, for a test-side basic attack
-- or Q on it. Each run does the next step of dgsim_pursuit_plan (default {'create', 'in', 'out', 'create', 'in'}):
--   create: a new Creep_Jungle_Fish_Minor (aggroRange 0: it stays put unless hit or moved) of team Spirit (an enemy of
--           the Mortal player) at dgsim_pursuit_open ({-90, -100}: open ground east of the grass patch beside the Mortal
--           top base, grass map value 0x16882698, x -120 .. -108 at y -100; seen from (-95, -88) all the way to the
--           patch);
--   in:     walks it into the patch, to dgsim_pursuit_grass ({-116, -98}) (nLocomotion.MoveToPointExact): a Mortal
--           source outside the patch loses it at the patch's edge, near (-106, -98.8);
--   out:    walks it out again, to dgsim_pursuit_out ({-96, -100}).
-- A step other than create on a creature that is gone or dead creates a new one instead. Every run reports the
-- creature's position, grass, HP and the step. dgsim_pursuit_noun, _team override the creature.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

local ok, err = pcall(function()
  dgsim_pursuit_step = (dgsim_pursuit_step or 0) + 1
  local step = dgsim_pursuit_step
  local plan = dgsim_pursuit_plan or {'create', 'in', 'out', 'create', 'in'}
  local action = plan[step] or 'report'
  local open = dgsim_pursuit_open or {-90.0, -100.0}
  local grass = dgsim_pursuit_grass or {-116.0, -98.0}
  local outside = dgsim_pursuit_out or {-96.0, -100.0}
  local id = dgsim_pursuit_id
  local alive = id ~= nil and nObjectManager.IsValidObject(id) and nGameObject.IsAlive(id)
  if action == 'create' or (action ~= 'report' and not alive) then
    local noun = nUtil.GetAsset(dgsim_pursuit_noun or 'Creep_Jungle_Fish_Minor.Noun')
    id = nObjectManager.CreateObject(noun, nTeamID[dgsim_pursuit_team or 'Spirit'],
      nVector.create(open[1], open[2], 0), kZeroVector, 1)
    if id == nil or id == kObjIDNone then
      p('pursuit fixture: step', step, action, 'creature NOT created')
      return
    end
    dgsim_pursuit_id = id
    p('pursuit fixture: step', step, action, 'created', tostring(id), nGameObject.GetAssetName(id), 'at',
      vec(nGameObject.GetPosition(id)))
  elseif action == 'in' or action == 'out' then
    local to = action == 'in' and grass or outside
    p('pursuit fixture: step', step, action, tostring(id), 'from', vec(nGameObject.GetPosition(id)), 'walks',
      action == 'in' and 'into the grass' or 'out of the grass', 'to', string.format('(%.1f %.1f)', to[1], to[2]))
    nLocomotion.MoveToPointExact(id, nVector.create(to[1], to[2], 0))
  end
  if id ~= nil and nObjectManager.IsValidObject(id) then
    p('pursuit fixture:', tostring(id), 'at', vec(nGameObject.GetPosition(id)), 'in grass',
      tostring(nGameObject.IsInGrass(id)), 'hp', nGameObject.GetHitPoints(id), 'match',
      string.format('%.2f', nGameSimulator.GetMatchTime()))
  end
end)
if not ok then p('pursuit fixture: ERROR', err) end
return table.concat(out, '\n')
