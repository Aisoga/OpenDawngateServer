-- D1f-live test probe (DAMAGE.md T4-T6; [host] test_lua=<s>:dgsim_damage_mitigation.lua, before the first wave):
-- direct nGameObject.TakeDamage hits with attribute snapshots, through the host's damage core (gms/engine/damage.cpp).
--   T4: a Spirit melee minion's snapshot on Binding_Mortal_Outer (armor 50, a structure): 100 physical -> 66.67,
--       100 magical -> 100, 100 true -> 100, the minion's basic (12, IsMelee|IsBasic|IsPhysicalDamage, coefficient 1,
--       Power 0) -> 8.00;
--   T5: a Spirit Binding's snapshot (PercentDefensePenetration 0.7 from its passive) on a Strider_Mortal_Small (armor
--       15): 100 physical -> 95.69; the minion's snapshot (no penetration) -> 86.96;
--   T6: a Mortal melee minion with PhysicalDamageReduction -20: 100 physical -> 116.67 (2 - 100/120).
-- The helpers stand on the bottom lane between the outer Bindings, out of both towers' reach, before the first wave
-- (match 60 s); they are marked for delete at the end. The armor and MR actually read are logged next to each result.
-- Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

-- GetObjectIdByIndex(i) returns the next live object at or after pool slot i (nil: none left)
local function find(name, wantPositiveX)
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if nGameObject.GetAssetName(id) == name then
      local pos = nGameObject.GetPosition(id)
      if pos and ((pos.x > 0) == wantPositiveX) then return id end
    end
  end
end

local spawned = {}
local function spawn(noun, team, x, y)
  local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), nTeamID[team], nVector.create(x, y, 0), kZeroVector, 1)
  if m then spawned[#spawned + 1] = m end
  return m
end

local function defense(id)
  return 'armor ' .. f2(nAttribute.GetAttributeValue(id, nAttributeType.PhysicalDamageReduction)) .. ' MR ' ..
    f2(nAttribute.GetAttributeValue(id, nAttributeType.MagicalDamageReduction))
end

local function hit(label, snap, target, damage, coef, desc, expect)
  local before = nGameObject.GetHitPoints(target)
  local ok, dealt, crit, absorbed = nGameObject.TakeDamage(snap, target, damage, coef, desc, kZeroVector)
  local after = nGameObject.GetHitPoints(target)
  p(label, 'damage', damage, 'coef', coef, '->', tostring(ok), 'dealt', f2(dealt), 'crit', tostring(crit), 'absorbed',
    f2(absorbed), 'hp', f2(before), '->', f2(after), '(expected', expect .. ')')
end

local ok, err = pcall(function()
  local phys, mag, tru = nDescriptors.IsPhysicalDamage, nDescriptors.IsMagicalDamage, nDescriptors.IsTrueDamage
  local basic = nBit.Or(nBit.Or(nDescriptors.IsMelee, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
  local spirit = spawn('Minion_Spirit_Melee.Noun', 'Spirit', 73.2, -64.4)
  local snapMinion = nAttribute.CreateAttributeSnapshot_FromObject(spirit, kObjIDNone)
  p('mitigation: Spirit minion', tostring(spirit), 'level', tostring(nGameObject.GetLevel and nGameObject.GetLevel(spirit)),
    'power', f2(nAttribute.GetAttributeValue(spirit, nAttributeType.Power)), 'snapshot', tostring(snapMinion))
  -- T4
  local binding = find('Binding_Mortal_Outer', true)
  if binding then
    p('T4: Binding_Mortal_Outer', tostring(binding), defense(binding), 'structure', tostring(nGameObject.IsStructure(binding)))
    hit('T4 physical', snapMinion, binding, 100, 0, phys, '66.67')
    hit('T4 magical', snapMinion, binding, 100, 0, mag, '100')
    hit('T4 true', snapMinion, binding, 100, 0, tru, '100')
    hit('T4 basic', snapMinion, binding, 12, 1, basic, '8.00')
  else
    p('T4: Binding_Mortal_Outer (x > 0) not found')
  end
  -- T5
  local tower = find('Binding_Spirit_Outer', true)
  local strider = spawn('Strider_Mortal_Small.Noun', 'Mortal', 62.6, -75.0)
  if tower and strider then
    local snapTower = nAttribute.CreateAttributeSnapshot_FromObject(tower, kObjIDNone)
    p('T5: tower', tostring(tower), 'PercentDefensePenetration',
      f2(nAttribute.GetAttributeValue(tower, nAttributeType.PercentDefensePenetration)), 'strider', tostring(strider),
      defense(strider), 'hp', f2(nGameObject.GetHitPoints(strider)))
    hit('T5 tower', snapTower, strider, 100, 0, phys, '95.69 at armor 15')
    hit('T5 minion', snapMinion, strider, 100, 0, phys, '86.96 at armor 15')
    nAttribute.RemoveAttributeSnapshot(snapTower)
  else
    p('T5: tower', tostring(tower), 'strider', tostring(strider))
  end
  -- T6
  local m6 = spawn('Minion_Mortal_Melee.Noun', 'Mortal', 66.0, -71.0)
  if m6 then
    nAttribute.AddAttributeModifier(m6, nAttributeType.PhysicalDamageReduction, -20, nAttributeOp.kAdd)
    p('T6: Mortal minion', tostring(m6), defense(m6))
    hit('T6 physical', snapMinion, m6, 100, 0, phys, '116.67')
  end
  nAttribute.RemoveAttributeSnapshot(snapMinion)
  p('ability GUIDs: ability_Binding_Attack_Outer', tostring(nUtil.SPID('ability_Binding_Attack_Outer')),
    'ability_minion_melee', tostring(nUtil.SPID('ability_minion_melee')))
end)
if not ok then p('mitigation: ERROR', err) end
for _, id in ipairs(spawned) do nGameObject.MarkForDelete(id) end
p('mitigation: marked for delete', #spawned, 'helpers')
return table.concat(out, '\n')
