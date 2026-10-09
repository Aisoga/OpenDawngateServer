-- D1b-live test probe (AGENT_AI.md T7, the leash; [host] test_lua=<s>:dgsim_agents_leash.lua after
-- dgsim_agents_creep.lua): teleports the creep's attacker (dgsim_creep_ids[2]) 45 units from the camp, beyond the
-- creep's tether (22 around its initial position) and the minion's dropAggroRange (33). The creep should drop it, run
-- nBehavior_Defend (leaf 16: back to its initial position, untargetable while walking) and the minion return to its
-- passive idle. Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

local ok, err = pcall(function()
  local ids = dgsim_creep_ids or {}
  local creep, minion = ids[1], ids[2]
  if creep == nil or minion == nil then
    p('leash: no creep test')
    return
  end
  local c = nGameObject.GetPosition(creep)
  local to = nVector.create((dgsim_creep_x or -65.0) + 31.8, (dgsim_creep_y or 130.7) + 31.8, c.z)
  nLocomotion.TeleportToPositionAndFacing(minion, to, nVector.create(0.7071, 0.7071, 0))
  p('leash: creep', tostring(creep), 'at', vec(c), 'hp', nGameObject.GetHitPoints(creep), '; attacker', tostring(minion),
    'moved to', vec(nGameObject.GetPosition(minion)), 'hp', nGameObject.GetHitPoints(minion), 'distance',
    string.format('%.1f', nVector.distance(nGameObject.GetPosition(minion), c)))
end)
if not ok then p('leash: ERROR', err) end
return table.concat(out, '\n')
