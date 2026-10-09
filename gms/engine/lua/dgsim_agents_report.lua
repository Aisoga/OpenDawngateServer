-- D1b-live test probe ([host] test_lua=<s>:dgsim_agents_report.lua, any number of times): what the agent AI's Lua side
-- sees for the probe objects (dgsim_duel_ids from dgsim_agents_duel.lua, dgsim_creep_ids, dgsim_guardian_ids,
-- dgsim_wave_ids from the test wave, and the
-- Bindings named in dgsim_report_bindings): HP, position, nAgent.GetBestTarget, HasTargetsOnAggroList, the aggro list
-- (nAgent.GetTargetsOnAggroList), nGameObject.IsInCombat. Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f)', v.x, v.y) end

local function report(tag, id)
  if id == nil or id == kObjIDNone then return end
  if not nGameObject.IsValid or nGameObject.IsValid(id) then
    local list = {}
    local ok, targets = pcall(nAgent.GetTargetsOnAggroList, id)
    if ok and type(targets) == 'table' then
      for _, t in ipairs(targets) do list[#list + 1] = tostring(t) end
    end
    p(tag, tostring(id), nGameObject.GetAssetName(id), 'hp', nGameObject.GetHitPoints(id), 'at',
      vec(nGameObject.GetPosition(id)), 'best', tostring(nAgent.GetBestTarget(id)), 'hasTargets',
      tostring(nAgent.HasTargetsOnAggroList(id)), 'inCombat', tostring(nGameObject.IsInCombat(id)), 'list [' ..
      table.concat(list, ' ') .. ']')
  end
end

local ok, err = pcall(function()
  for _, id in ipairs(dgsim_duel_ids or {}) do pcall(report, 'duel', id) end
  for _, id in ipairs(dgsim_creep_ids or {}) do pcall(report, 'creep', id) end
  for _, id in ipairs(dgsim_guardian_ids or {}) do pcall(report, 'guardian', id) end
  for _, id in ipairs(dgsim_help_ids or {}) do pcall(report, 'help', id) end
  for _, id in ipairs(dgsim_parasite_ids or {}) do pcall(report, 'parasite', id) end
  -- the live objects through the pool slots (GetObjectIdByIndex: the next live object at or after a slot), up to the
  -- first slot after the last live object (beyond the pool's count it asserts, SPObjectPool.cpp 161)
  local live, seen = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if not seen[tostring(id)] then
      seen[tostring(id)] = true
      live[#live + 1] = id
    end
  end
  for _, s in ipairs(dgsim_wave_ids or {}) do  -- the wave keeps tostring()ed ids
    for _, id in ipairs(live) do
      if tostring(id) == s then pcall(report, 'wave', id) break end
    end
  end
  for _, id in ipairs(live) do
    if nGameObject.GetAssetName(id) == 'Binding_Spirit_Outer' then
      local pos = nGameObject.GetPosition(id)
      if pos and pos.x < 0 then pcall(report, 'binding', id) end
    end
  end
end)
if not ok then p('report: ERROR', err) end
return table.concat(out, '\n')
