-- D1c-live test probe (MATCH_FLOW.md T7, AGENT_AI.md T7's first half; [host] test_lua=<s>:dgsim_match_hit_creep.lua):
-- a creep of a director-spawned camp is hit by a Mortal minion, and the whole camp must pull: the director put every
-- creature in its camp (object +0x50, AIDirectorImpl_AddCampMember), so Agent_CallForHelp (aggro type jungle creature)
-- adds the attacker to the other members' aggro lists. Default camp: SpawnPoint_v5_Buff_Fish at (68.0, -165.6) (2 minor
-- + 1 major). The attacker stands 8 units away. Ids in dgsim_campfight_ids (attacker first). Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local cx, cy = dgsim_camp_x or 68.0, dgsim_camp_y or -165.6
  local members = {}
  local seen = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      local pos = nGameObject.GetPosition(id)
      if string.find(name, '^Creep_Jungle_') and pos and math.abs(pos.x - cx) < 12 and math.abs(pos.y - cy) < 12 and
          nGameObject.IsAlive(id) then
        members[#members + 1] = id
      end
    end
  end
  if #members == 0 then
    p('camp fight: no creatures near', cx, cy)
    return
  end
  local first = members[1]
  local fp = nGameObject.GetPosition(first)
  local attacker = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Mortal_Melee.Noun'), nTeamID.Mortal,
    nVector.create(fp.x - 8, fp.y, 0), kZeroVector, 1)
  dgsim_campfight_ids = {attacker}
  for _, m in ipairs(members) do dgsim_campfight_ids[#dgsim_campfight_ids + 1] = m end
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(attacker, kObjIDNone)
  local success, dealt = nGameObject.TakeDamage(snap, first, 10, 0, nDescriptors.IsBasic, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  p('camp fight: attacker', tostring(attacker), 'hit', tostring(first), nGameObject.GetAssetName(first), 'success',
    tostring(success), 'dealt', tostring(dealt))
  for _, m in ipairs(members) do
    local list = {}
    local okList, targets = pcall(nAgent.GetTargetsOnAggroList, m)
    if okList and type(targets) == 'table' then
      for _, t in ipairs(targets) do list[#list + 1] = tostring(t) end
    end
    p('camp fight: member', tostring(m), nGameObject.GetAssetName(m), 'hp', nGameObject.GetHitPoints(m), 'aggro list [' ..
      table.concat(list, ' ') .. ']', 'best', tostring(nAgent.GetBestTarget(m)))
  end
end)
if not ok then p('camp fight: ERROR', err) end
return table.concat(out, '\n')
