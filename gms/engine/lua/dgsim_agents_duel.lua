-- D1b-live test probe (AGENT_AI.md T4/T5; run with [host] test_lua=<s>:dgsim_agents_duel.lua): a Mortal and a Spirit
-- melee minion spawned 15 units apart on the top lane between the two outer Bindings (out of both towers' reach, near
-- the first waves' meeting point (-69.5, 68.2), MATCH_FLOW.md §2.4), idle. With the blackboard update each puts the
-- other on its aggro list within 0.5 s and engages (leaf 14); with the brain step they fight. Their ids go into the Lua
-- global dgsim_duel_ids (dgsim_agents_report.lua reads it). Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

local ok, err = pcall(function()
  local spots = {
    {noun = dgsim_duel_mortal or 'Minion_Mortal_Melee.Noun', team = 'Mortal', x = -75.0, y = 62.6},
    {noun = dgsim_duel_spirit or 'Minion_Spirit_Melee.Noun', team = 'Spirit', x = -64.4, y = 73.2},
  }
  dgsim_duel_ids = {}
  for _, s in ipairs(spots) do
    local pos = nVector.create(s.x, s.y, 0)
    local m = nObjectManager.CreateObject(nUtil.GetAsset(s.noun), nTeamID[s.team], pos, kZeroVector, 1)
    if m then
      dgsim_duel_ids[#dgsim_duel_ids + 1] = m
      p('duel:', s.noun, s.team, tostring(m), 'at', vec(nGameObject.GetPosition(m)), 'hp', nGameObject.GetHitPoints(m))
    else
      p('duel:', s.noun, 'NOT created at', vec(pos))
    end
  end
  if #dgsim_duel_ids == 2 then
    p('duel: distance', string.format('%.2f', nVector.distance(nGameObject.GetPosition(dgsim_duel_ids[1]),
      nGameObject.GetPosition(dgsim_duel_ids[2]))))
  end
end)
if not ok then p('duel: ERROR', err) end
return table.concat(out, '\n')
