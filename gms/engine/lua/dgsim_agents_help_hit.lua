-- D1b-live test probe (AGENT_AI.md T9, call for help, part 2; [host] test_lua=<s>:dgsim_agents_help_hit.lua a second or
-- more after dgsim_agents_help.lua): in one chunk, spawns a Mortal melee minion (the decoy) 10 units from the victim on
-- the side away from the source (about 10 units from the helpers: inside their aggroRange) and hits the victim
-- (dgsim_help_ids[1]) with the source's (dgsim_help_ids[4]) snapshot and IsBasic (the minions' call-for-help filter is
-- "basic"). The placeholder damage core's hook calls Agent_CallForHelp: the source goes onto both helpers' lists with
-- entry +0x20 stamped. At the helpers' next blackboard update the decoy is added from their perception and the list is
-- ordered: neither entry is the sticky best target yet, so the source (a non-player call for help within 3 s, rule 4)
-- should come before the nearer decoy. Reports the helpers' lists right after the hit. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

local ok, err = pcall(function()
  local ids = dgsim_help_ids or {}
  local victim, source = ids[1], ids[4]
  if victim == nil or source == nil then
    p('help: no call-for-help test')
    return
  end
  local v = nGameObject.GetPosition(victim)
  local decoy = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Mortal_Melee.Noun'), nTeamID.Mortal,
    nVector.create(v.x - 7.3, v.y - 6.8, 0), kZeroVector, 1)  -- 10 units down the lane
  ids[5] = decoy
  p('help: decoy', tostring(decoy), 'at', vec(decoy and nGameObject.GetPosition(decoy)))
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(source, kObjIDNone)
  local success, dealt = nGameObject.TakeDamage(snap, victim, 5, 0, nDescriptors.IsBasic, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  p('help: victim', tostring(victim), 'hit by the source', tostring(source), ':', tostring(success), tostring(dealt))
  for i = 2, 3 do
    local list = {}
    local okList, targets = pcall(nAgent.GetTargetsOnAggroList, ids[i])
    if okList and type(targets) == 'table' then
      for _, t in ipairs(targets) do list[#list + 1] = tostring(t) end
    end
    p('help: helper', tostring(ids[i]), 'list right after the hit [' .. table.concat(list, ' ') .. ']')
  end
end)
if not ok then p('help: ERROR', err) end
return table.concat(out, '\n')
