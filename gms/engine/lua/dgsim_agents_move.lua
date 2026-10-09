-- D1b-live test probe (AGENT_AI.md T6; [host] test_lua=<s>:dgsim_agents_move.lua after dgsim_agents_duel.lua): teleports
-- the duel's Spirit minion (dgsim_duel_ids[2]) 40 units further up the lane (nLocomotion.TeleportToPositionAndFacing),
-- beyond the Mortal minion's dropAggroRange (33): its entry should be removed at the next blackboard update, engaged
-- cleared, and nBehavior_Assault (leaf 16) resume. Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

local ok, err = pcall(function()
  local ids = dgsim_duel_ids or {}
  local id = ids[2]
  if id == nil then
    p('move: no duel minion')
    return
  end
  local from = nGameObject.GetPosition(id)
  local dx, dy = dgsim_move_dx or 28.3, dgsim_move_dy or 28.3  -- 40 units along the lane (direction about (0.7, 0.7))
  local to = nVector.create(from.x + dx, from.y + dy, from.z)
  nLocomotion.TeleportToPositionAndFacing(id, to, nVector.create(0.7071, 0.7071, 0))
  p('move:', tostring(id), 'from', vec(from), 'to', vec(nGameObject.GetPosition(id)), 'hp', nGameObject.GetHitPoints(id))
  if ids[1] ~= nil then
    p('move: distance to', tostring(ids[1]), 'now', string.format('%.2f',
      nVector.distance(nGameObject.GetPosition(id), nGameObject.GetPosition(ids[1]))))
  end
end)
if not ok then p('move: ERROR', err) end
return table.concat(out, '\n')
