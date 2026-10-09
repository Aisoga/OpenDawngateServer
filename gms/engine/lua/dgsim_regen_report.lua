-- D1i test probe ([host] test_lua=<s>:dgsim_regen_report.lua, several times; DAMAGE.md *D1i*): HP, regen attributes,
-- resource and position of the objects dgsim_regen_hurt.lua / dgsim_regen_creep.lua recorded (dgsim_regen_ids), so the
-- server side of the regeneration can be read next to regen.cpp's own lines. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local function report(k, id)
  local alive = nGameObject.IsAlive(id)
  local pos = nGameObject.GetPosition(id)
  local list = {}
  local okList, targets = pcall(nAgent.GetTargetsOnAggroList, id)
  if okList and type(targets) == 'table' then
    for _, t in ipairs(targets) do list[#list + 1] = tostring(t) end
  end
  p('regen report:', k, tostring(id), nGameObject.GetAssetName(id), 'alive', tostring(alive), 'hp',
    f2(nGameObject.GetHitPoints(id)), 'of', f2(nGameObject.GetMaxHitPoints(id)), 'HealthRegen',
    f2(nAttribute.GetAttributeValue(id, nAttributeType.HealthRegen)), 'TotalHealthRegen',
    f2(nAttribute.GetDerivedAttributeValue(id, nDerivedAttributeType.TotalHealthRegen)), 'resource',
    f2(nGameObject.GetResourcePoints(id)), 'of', f2(nGameObject.GetMaxResourcePoints(id)), 'pos',
    pos and f2(pos.x) or '?', pos and f2(pos.y) or '?', 'aggro [' .. table.concat(list, ' ') .. ']')
end

local ok, err = pcall(function()
  local ids = dgsim_regen_ids or {}
  for _, k in ipairs({'guardian', 'shaper', 'creep', 'creepAttacker'}) do
    local id = ids[k]
    if id ~= nil and id ~= kObjIDNone then
      local okOne, errOne = pcall(report, k, id)
      if not okOne then p('regen report:', k, tostring(id), 'gone or unreadable:', errOne) end
    end
  end
  p('regen report: at match time', nGameSimulator.GetMatchTime())
end)
if not ok then p('regen report: ERROR', err) end
return table.concat(out, '\n')
