-- D1b-live test probe (AGENT_AI.md T7; [host] test_lua=<s>:dgsim_agents_creep.lua): a jungle creature hit by a Mortal
-- minion. Spawns Creep_Jungle_Fish_Minor (team NPC) at the top fish camp (SpawnPoint_v5_CreepsFish, (-65.0, 130.7)),
-- a Mortal melee minion 6 units away, and hits the creep with the minion's snapshot (nGameObject.TakeDamage through the
-- placeholder damage core, whose damage hook puts the attacker on the creep's own aggro list: jungle creatures have
-- aggroRange 0 and only fight when hit). The creep's camp membership is D1c-live's (no camp here, so call for help finds
-- nobody). Ids in dgsim_creep_ids (creep, attacker); dgsim_agents_leash.lua moves the attacker away. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

local ok, err = pcall(function()
  local cx, cy = dgsim_creep_x or -65.0, dgsim_creep_y or 130.7
  local creep = nObjectManager.CreateObject(nUtil.GetAsset(dgsim_creep_noun or 'Creep_Jungle_Fish_Minor.Noun'), nTeamID.NPC,
    nVector.create(cx, cy, 0), kZeroVector, 1)
  local minion = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Mortal_Melee.Noun'), nTeamID.Mortal,
    nVector.create(cx + 4.2, cy - 4.2, 0), kZeroVector, 1)
  dgsim_creep_ids = {creep, minion}
  p('creep:', tostring(creep), 'at', vec(creep and nGameObject.GetPosition(creep)), 'hp', creep and nGameObject.GetHitPoints(creep),
    'attacker', tostring(minion), 'at', vec(minion and nGameObject.GetPosition(minion)))
  if creep and minion then
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(minion, kObjIDNone)
    local success, dealt = nGameObject.TakeDamage(snap, creep, 10, 0, nDescriptors.IsBasic, kZeroVector)
    nAttribute.RemoveAttributeSnapshot(snap)
    p('creep: hit by', tostring(minion), 'success', tostring(success), 'dealt', tostring(dealt), 'hp now',
      nGameObject.GetHitPoints(creep))
  end
end)
if not ok then p('creep: ERROR', err) end
return table.concat(out, '\n')
