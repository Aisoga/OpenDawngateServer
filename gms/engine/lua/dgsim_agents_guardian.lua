-- D1b-live test probe (AGENT_AI.md T10; [host] test_lua=<s>:dgsim_agents_guardian.lua): the brain's general path. Spawns
-- GuardianSpirit_v5 at its base titan node's spawn point (182, 182; D1c-live spawns it at the level load) and a Mortal
-- melee minion 15 units in front of it, inside the Guardian's aggroRange (29). Expected: the Guardian's aggro list holds
-- the minion, condition_Guardian_UseBasicAttack passes (an enemy NPC within 30.5) and ability_Guardian_BasicAttack is
-- requested; the link to node 1 (condition_Titan_UseSpecial: an enemy player within 67) never fires. Ids in
-- dgsim_guardian_ids (guardian, minion). Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

local ok, err = pcall(function()
  local g = nObjectManager.CreateObject(nUtil.GetAsset('GuardianSpirit_v5.Noun'), nTeamID.Spirit, nVector.create(182, 182, 0),
    kZeroVector, 1)
  local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Mortal_Melee.Noun'), nTeamID.Mortal,
    nVector.create(171.4, 171.4, 0), kZeroVector, 1)
  dgsim_guardian_ids = {g, m}
  p('guardian:', tostring(g), 'at', vec(g and nGameObject.GetPosition(g)), 'hp', g and nGameObject.GetHitPoints(g), '; minion',
    tostring(m), 'at', vec(m and nGameObject.GetPosition(m)), 'hp', m and nGameObject.GetHitPoints(m))
end)
if not ok then p('guardian: ERROR', err) end
return table.concat(out, '\n')
