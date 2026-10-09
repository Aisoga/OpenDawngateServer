-- D1i test probe ([host] test_lua=<s>:dgsim_regen_creep.lua; DAMAGE.md *D1i*): a jungle creep's regen passive. The
-- first living Creep_Jungle_* within 12 units of (dgsim_camp_x, dgsim_camp_y) (default SpawnPoint_v5_Buff_Fish at
-- (68.0, -165.6), first spawn at match 90 s) is hit for dgsim_regen_creep_fraction (0.4) of its max HP by a Mortal melee
-- minion standing 8 units away, which stays and fights. Expected: the creep's modifier_Jungle_Creep_Regen /
-- modifier_Jungle_Buff_Regen_* drops its HealthRegen 2000 in OnTookDamage (the attacker is on its aggro list by then),
-- the camp fights without regenerating, and once the attacker is dead and the creep stands within 5 units of its spawn
-- point with an empty aggro list it gets HealthRegen 2000 per 5 s again (400 HP/s) and is full within a second.
-- Ids in dgsim_regen_ids for dgsim_regen_report.lua. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local ok, err = pcall(function()
  dgsim_regen_ids = dgsim_regen_ids or {}
  local cx, cy = dgsim_camp_x or 68.0, dgsim_camp_y or -165.6
  local creep
  local seen = {}
  -- GetObjectIdByIndex(i) is the iterator from pool slot i (GMS_PROTOCOL.md D2g-live): the next live object at or after
  -- slot i, none once nothing follows; a slot beyond the pool's high-water mark asserts (SPObjectPool.cpp 161, d1i_run1)
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if not seen[tostring(id)] then
      seen[tostring(id)] = true
      local name = nGameObject.GetAssetName(id) or ''
      local pos = nGameObject.GetPosition(id)
      if creep == nil and string.find(name, '^Creep_Jungle_') and pos and math.abs(pos.x - cx) < 12 and
          math.abs(pos.y - cy) < 12 and nGameObject.IsAlive(id) then
        creep = id
      end
    end
  end
  if creep == nil then
    p('regen creep: no living creep near', cx, cy)
    return
  end
  dgsim_regen_ids.creep = creep
  local cp = nGameObject.GetPosition(creep)
  local max = nGameObject.GetMaxHitPoints(creep)
  p('regen creep: before the hit', tostring(creep), nGameObject.GetAssetName(creep), 'hp', f2(nGameObject.GetHitPoints(creep)),
    'of', f2(max), 'HealthRegen', f2(nAttribute.GetAttributeValue(creep, nAttributeType.HealthRegen)), 'at', f2(cp.x), f2(cp.y))
  local attacker = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Mortal_Melee.Noun'), nTeamID.Mortal,
    nVector.create(cp.x - 8, cp.y, 0), kZeroVector, 1)
  dgsim_regen_ids.creepAttacker = attacker
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(attacker, kObjIDNone)
  local amount = max * (dgsim_regen_creep_fraction or 0.4)
  local s, d = nGameObject.TakeDamage(snap, creep, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  local list = {}
  local okList, targets = pcall(nAgent.GetTargetsOnAggroList, creep)
  if okList and type(targets) == 'table' then
    for _, t in ipairs(targets) do list[#list + 1] = tostring(t) end
  end
  p('regen creep: hit by', tostring(attacker), 'for', f2(amount), '->', tostring(s), f2(d), 'hp',
    f2(nGameObject.GetHitPoints(creep)), 'HealthRegen now', f2(nAttribute.GetAttributeValue(creep, nAttributeType.HealthRegen)),
    'aggro list [' .. table.concat(list, ' ') .. ']', 'at match time', nGameSimulator.GetMatchTime())
end)
if not ok then p('regen creep: ERROR', err) end
return table.concat(out, '\n')
