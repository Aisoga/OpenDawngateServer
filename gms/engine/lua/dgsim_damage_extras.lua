-- D1f-live test probe (DAMAGE.md steps 3, 13, 14; [host] test_lua=<s>:dgsim_damage_extras.lua, before the first wave):
-- the crit roll, lifedrain and spell overload through the host's damage core. A Spirit melee minion (the attacker) gets
-- Mastery +100 (crit chance min(100 / 1.0, 100) % = 100 %, overload 100 / 2.0 = 50 %) and LifeDrain +0.5 and is hurt
-- first (200 true damage from a Binding's snapshot) so it has HP to drain back; its snapshot then hits a Mortal melee
-- minion (armor and MR 0 at level 1):
--   a basic attack (12, IsMelee|IsBasic|IsPhysicalDamage, coefficient 0): a crit x criticalDamageBonusPercent 2.0 =
--     24 dealt, isCrit true, the attacker healed 12 (lifesteal, combat event 0x400);
--   100 magical (coefficient 0): 100 dealt, then the overload hit of 50 (50 % of the pre-mitigation damage, mitigated
--     again: MR 0) as its own log line "(overload)"; lifedrain 50 (+ 25 for the overload hit [assumed]).
-- Both helpers stand on the bottom lane out of the towers' reach and are marked for delete. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local function find(name)
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if nGameObject.GetAssetName(id) == name then return id end
  end
end

local spawned = {}
local function spawn(noun, team, x, y)
  local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), nTeamID[team], nVector.create(x, y, 0), kZeroVector, 1)
  if m then spawned[#spawned + 1] = m end
  return m
end

local ok, err = pcall(function()
  local a = spawn('Minion_Spirit_Melee.Noun', 'Spirit', 73.2, -64.4)
  local t = spawn('Minion_Mortal_Melee.Noun', 'Mortal', 62.6, -75.0)
  local tower = find('Binding_Mortal_Outer')
  if not a or not t or not tower then
    p('extras: attacker', tostring(a), 'target', tostring(t), 'tower', tostring(tower))
    return
  end
  nAttribute.AddAttributeModifier(a, nAttributeType.Mastery, 100, nAttributeOp.kAdd)
  nAttribute.AddAttributeModifier(a, nAttributeType.LifeDrain, 0.5, nAttributeOp.kAdd)
  local snapTower = nAttribute.CreateAttributeSnapshot_FromObject(tower, kObjIDNone)
  nGameObject.TakeDamage(snapTower, a, 200, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snapTower)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(a, kObjIDNone)
  p('extras: attacker', tostring(a), 'Mastery', f2(nAttribute.GetAttributeValue(a, nAttributeType.Mastery)), 'LifeDrain',
    f2(nAttribute.GetAttributeValue(a, nAttributeType.LifeDrain)), 'hp', f2(nGameObject.GetHitPoints(a)), 'target', tostring(t),
    'hp', f2(nGameObject.GetHitPoints(t)))
  local basic = nBit.Or(nBit.Or(nDescriptors.IsMelee, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
  local ah, th = nGameObject.GetHitPoints(a), nGameObject.GetHitPoints(t)
  local s1, d1, c1, b1 = nGameObject.TakeDamage(snap, t, 12, 0, basic, kZeroVector)
  p('extras: basic 12 ->', tostring(s1), 'dealt', f2(d1), 'crit', tostring(c1), 'absorbed', f2(b1), 'target hp', f2(th), '->',
    f2(nGameObject.GetHitPoints(t)), 'attacker hp', f2(ah), '->', f2(nGameObject.GetHitPoints(a)),
    '(expected crit, 24 dealt, +12 healed)')
  ah, th = nGameObject.GetHitPoints(a), nGameObject.GetHitPoints(t)
  local s2, d2, c2, b2 = nGameObject.TakeDamage(snap, t, 100, 0, nDescriptors.IsMagicalDamage, kZeroVector)
  p('extras: magical 100 ->', tostring(s2), 'dealt', f2(d2), 'crit', tostring(c2), 'absorbed', f2(b2), 'target hp', f2(th),
    '->', f2(nGameObject.GetHitPoints(t)), 'attacker hp', f2(ah), '->', f2(nGameObject.GetHitPoints(a)),
    '(expected 100 dealt + a 50 overload hit, +50 (+25) healed)')
  nAttribute.RemoveAttributeSnapshot(snap)
end)
if not ok then p('extras: ERROR', err) end
for _, id in ipairs(spawned) do nGameObject.MarkForDelete(id) end
return table.concat(out, '\n')
