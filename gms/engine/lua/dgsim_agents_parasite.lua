-- D1b-live test probe (AGENT_AI.md T11; [host] test_lua=<s>:dgsim_agents_parasite.lua): the parasite
-- (Objective_SpiritMonster_State1, team NPC) with a Mortal melee minion 10 units beside it and no player near. Expected:
-- the parasite's list holds the minion (aggroRange 27), but neither gambit's condition passes
-- (condition_Parasite_EnemyInMelee/InRanged look for enemy players only): no attack, nBehavior_DefendStationary stays.
-- Since D14 the objective type adds nobody on sight (MATCH_FLOW.md *D14*): the list stays empty unless the host runs
-- with [host] objective_aggro_on_sight=1 (the D1b-live behaviour this probe was written for).
-- Spawned where the director would (SpawnPoint_Objective_Titan, the map centre). Ids in dgsim_parasite_ids. Returns a
-- report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

local ok, err = pcall(function()
  local x, y = dgsim_parasite_x or 0, dgsim_parasite_y or 0
  local para = nObjectManager.CreateObject(nUtil.GetAsset('Objective_SpiritMonster_State1.Noun'), nTeamID.NPC,
    nVector.create(x, y, 0), kZeroVector, 1)
  local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Mortal_Melee.Noun'), nTeamID.Mortal,
    nVector.create(x + 7.1, y + 7.1, 0), kZeroVector, 1)
  dgsim_parasite_ids = {para, m}
  p('parasite:', tostring(para), 'at', vec(para and nGameObject.GetPosition(para)), 'hp', para and nGameObject.GetHitPoints(para),
    '; minion', tostring(m), 'at', vec(m and nGameObject.GetPosition(m)))
end)
if not ok then p('parasite: ERROR', err) end
return table.concat(out, '\n')
