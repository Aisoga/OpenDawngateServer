-- D15 test probe ([host] test_lua=<s>:dgsim_d15_hurt.lua; docs/ITEMS.md *The live batches*, batch C1): an enemy hits
-- player 0's shaper the ways the defensive item passives react to, through the host's damage core, each from the
-- snapshot of an enemy melee minion created beside it (marked for delete at the end, so nothing fights back):
--   1. dgsim_d15_basics (3) physical basic hits of dgsim_d15_basic (60): IsBasic | IsPhysicalDamage | IsMelee
--      (Resilience, Integrity, Order: ModifyDamageFinal; Adamance, Rebirth: OnTookDamage; armor);
--   2. one magical ability hit of dgsim_d15_magic (120): IsMagicalDamage (Will, Conviction, Faith, Retribution; MR);
--   3. a slow (modifier_Item_Stagger for 2 s) and a stun (modifier_Titan_AoEAbility_Stun, 0.75 s) with the minion as
--      caster, unless dgsim_d15_cc == false (Freedom, Momentum, Stability, Influence: Tenacity, CCShield,
--      OnCCRemoved_Movespeed, SlowEffectReduction);
--   4. true damage down to dgsim_d15_low (0.2) of its maximum health (the low-health passives: Resolve, Hope, Ambition,
--      Valor, Defiance, Rebirth, Vibrance; Empathy's redirection needs an ally).
-- Each hit's line has the damage asked, dealt and absorbed and the HP; the item report around it shows the modifiers
-- (the commands file's "report" lines). Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local ok, err = pcall(function()
  local shaper = nPlayer.GetPlayerControlledObjectID(0)
  if shaper == nil or shaper == kObjIDNone then
    p('d15 hurt: no shaper for player 0')
    return
  end
  local team = nGameObject.GetTeam(shaper)
  local enemy = team == nTeamID.Mortal and 'Spirit' or 'Mortal'
  local sp = nGameObject.GetPosition(shaper)
  local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_' .. enemy .. '_Melee.Noun'), nTeamID[enemy],
    nVector.create(sp.x + 4, sp.y + 4, 0), kZeroVector, 1)
  if m == nil or m == kObjIDNone then
    p('d15 hurt: could not create the enemy minion')
    return
  end
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(m, kObjIDNone)
  local dir = nGameObject.GetObjectDirection(m, shaper)
  local function hit(what, amount, desc)
    local hp0 = nGameObject.GetHitPoints(shaper)
    local s, dealt, crit, absorbed = nGameObject.TakeDamage(snap, shaper, amount, 0, desc, dir)
    p('d15 hurt:', what, 'asked', f1(amount), '->', tostring(s), 'dealt', f1(dealt), 'absorbed', f1(absorbed), 'hp',
      f1(hp0), '->', f1(nGameObject.GetHitPoints(shaper)), 'of', f1(nGameObject.GetMaxHitPoints(shaper)), 'armor',
      f1(nAttribute.GetDerivedAttributeValue(shaper, nDerivedAttributeType.TotalArmor)), 'mr',
      f1(nAttribute.GetDerivedAttributeValue(shaper, nDerivedAttributeType.TotalMR)))
  end
  local basic = nBit.Or(nBit.Or(nDescriptors.IsBasic, nDescriptors.IsPhysicalDamage), nDescriptors.IsMelee)
  for i = 1, dgsim_d15_basics or 3 do hit('basic ' .. i, dgsim_d15_basic or 60, basic) end
  hit('magical ability', dgsim_d15_magic or 120, nDescriptors.IsMagicalDamage)
  if dgsim_d15_cc ~= false then
    local slow = nModifier.RequestModifier(shaper, m, nUtil.SPID('modifier_Item_Stagger'), snap, 1, kZeroVector,
      {debuffDuration = 2})
    local stun = nModifier.RequestModifier(shaper, m, nUtil.SPID('modifier_Titan_AoEAbility_Stun'), snap, 1,
      kZeroVector, nil)
    p('d15 hurt: slow', tostring(slow), 'stun', tostring(stun), 'MovementSpeedPercentDecrease',
      f1(nAttribute.GetAttributeValue(shaper, nAttributeType.MovementSpeedPercentDecrease)), 'Immobilized',
      f1(nAttribute.GetAttributeValue(shaper, nAttributeType.Immobilized)), 'CrowdControlDurationDecrease',
      f1(nAttribute.GetAttributeValue(shaper, nAttributeType.CrowdControlDurationDecrease)))
  end
  local low = (dgsim_d15_low or 0.2) * nGameObject.GetMaxHitPoints(shaper)
  local hp = nGameObject.GetHitPoints(shaper)
  if hp > low then hit('down to ' .. f1(low), hp - low, nDescriptors.IsTrueDamage) end
  nAttribute.RemoveAttributeSnapshot(snap)
  nGameObject.MarkForDelete(m)
  p('d15 hurt: done at match time', f1(nGameSimulator.GetMatchTime()))
end)
if not ok then p('d15 hurt: ERROR', err) end
return table.concat(out, '\n')
