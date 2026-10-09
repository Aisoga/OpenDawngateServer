-- D1f-live test probe, part 2 of DAMAGE.md T7 (after dgsim_damage_shield.lua, within the shield's 3 s): one 100
-- true-damage hit with a Spirit Binding's snapshot on the shielded minion (dgsim_t7_id). Expected: damageAbsorbed 50,
-- damageDealt 50, HP 450 -> 400, and the host's shield total back to 0 ("damage: shield ..." 50 -> 0). The minion is
-- marked for delete afterwards. Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local ok, err = pcall(function()
  local m = dgsim_t7_id
  if not m then
    p('T7 hit: no minion (dgsim_damage_shield.lua first)')
    return
  end
  local tower
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if nGameObject.GetAssetName(id) == 'Binding_Spirit_Outer' then tower = id break end
  end
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(tower or m, kObjIDNone)
  local before = nGameObject.GetHitPoints(m)
  local success, dealt, crit, absorbed = nGameObject.TakeDamage(snap, m, 100, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  p('T7 hit: 100 true on', tostring(m), 'from', tostring(tower), '->', tostring(success), 'dealt', f2(dealt), 'crit',
    tostring(crit), 'absorbed', f2(absorbed), 'hp', f2(before), '->', f2(nGameObject.GetHitPoints(m)),
    '(expected absorbed 50, dealt 50, 450 -> 400)')
  nGameObject.MarkForDelete(m)
  dgsim_t7_id = nil
end)
if not ok then p('T7 hit: ERROR', err) end
return table.concat(out, '\n')
