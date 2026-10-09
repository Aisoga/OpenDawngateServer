-- D1b-live test probe (AGENT_AI.md T5, the survivor; [host] test_lua=<s>:dgsim_agents_hurt.lua after
-- dgsim_agents_duel.lua): hits the duel's Spirit minion (dgsim_duel_ids[2]) once for dgsim_hurt_damage (200) with the
-- Mortal minion's snapshot, so the two equal minions don't kill each other in the same tick (d1b_run4) and the
-- survivor's aggro list empties. Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local ids = dgsim_duel_ids or {}
  if ids[1] == nil or ids[2] == nil then
    p('hurt: no duel')
    return
  end
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(ids[1], kObjIDNone)
  local success, dealt = nGameObject.TakeDamage(snap, ids[2], dgsim_hurt_damage or 200, 0, nDescriptors.IsBasic, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  p('hurt:', tostring(ids[2]), 'hit by', tostring(ids[1]), tostring(success), tostring(dealt), 'hp now',
    nGameObject.GetHitPoints(ids[2]))
end)
if not ok then p('hurt: ERROR', err) end
return table.concat(out, '\n')
