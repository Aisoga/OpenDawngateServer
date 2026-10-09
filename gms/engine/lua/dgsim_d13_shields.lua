-- D13 live probe (docs/DAMAGE.md *Combat edge cases (D13)*, cases "two or more shields" and "a health cost right after
-- a shield absorbed damage"; [host] test_lua=<T>:dgsim_d13_shields.lua,<T+2.2>:dgsim_d13_shields.lua,<T+3.6>:
-- dgsim_d13_shields.lua; the human plays Fenmore, the default shaper). Each call runs the next part:
--   part 1: the human's shaper gets Dibs' W (modifier_Dreamer_W_Shield r1: 35 + 0.35 Power, 3 s), Bastion
--     (modifier_Item_Bastion: 60 + 20 x level, 2 s, + 20 armor and MR), Deflect (modifier_Item_DeflectShield: 50 + 10 x
--     level, 3 s), Basko's W (modifier_Enforcer_W_Shield r1: 6, 2 s), its own Q (modifier_Alpha_Q_Shield r1: 50, 3 s) and
--     Faith's magic-only shield (modifier_Item_Legendary_StackingMRShield_Shield r3: 25, untimed); then
--       a) 100 true damage from an enemy minion's snapshot: expected all absorbed, Bastion (80 at level 1), Basko (6),
--          Dibs (14 of 35) in that order (the shortest time left first, ties in request order), HP unchanged;
--       b) Fenmore's W (ability_Alpha_W_Stim r1, cost 25, forced): the cost is true self-inflicted damage, taken from the
--          generic shields (Dibs' remaining 21, then 4 of Deflect), HP unchanged, GetResourcePoints (HP + mShieldHealth)
--          down by 25;
--       c) 40 magical: mitigated by MR 33 + Bastion's 20 to 26.14 (100/153); Faith's shield first (25, the typed pass),
--          the rest (1.14) from Deflect; HP unchanged.
--   part 2 (2.2 s later): Bastion and Basko's W have ended (OnDuration); 100 true: Dibs (0, asked), Deflect (54.86), Fenmore's
--     Q (45.14 of 50): 0 through, 4.86 left (D13-live, d13_run1b; the first estimate, ~5 through, forgot 1c's mitigation).
--   part 3 (3.6 s after part 1): every timed shield ended, Faith's empty one stays: 30 true -> 30 dealt.
-- Each line reports dealt/absorbed, HP, the pool and each shield's remaining time; the host's own lines are the
-- "damage: <shaper> <- ... absorbed N (k handlers)" and "damage: shield ... mShieldHealth a -> b" ones. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local SHIELDS = {
  {'modifier_Dreamer_W_Shield', 1}, {'modifier_Item_Bastion', 1}, {'modifier_Item_DeflectShield', 1},
  {'modifier_Enforcer_W_Shield', 1}, {'modifier_Alpha_Q_Shield', 1}, {'modifier_Item_Legendary_StackingMRShield_Shield', 3},
}

-- nPlayer.IsBot takes the player's creature (an object id), not the player id (D13-live)
local function humanShaper()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c end
  end
end

local function enemySnapshot(shaper)
  local team = nGameObject.GetTeam(shaper)
  local noun, enemy = 'Minion_Spirit_Melee.Noun', nTeamID.Spirit
  if team == nTeamID.Spirit then noun, enemy = 'Minion_Mortal_Melee.Noun', nTeamID.Mortal end
  local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), enemy, nVector.create(66.0, -71.0, 0), kZeroVector, 1)
  if not m then return nil, nil end
  return m, nAttribute.CreateAttributeSnapshot_FromObject(m, kObjIDNone)
end

local function shieldState(shaper)
  local parts = {}
  for _, s in ipairs(SHIELDS) do
    local id = nModifier.GetFirstModifierByGUID(shaper, nUtil.SPID(s[1]))
    if id ~= nil and id ~= kObjIDNone then
      parts[#parts + 1] = string.format('%s %s s', string.gsub(s[1], '^modifier_', ''), f2(nModifier.GetRemainingDuration(id)))
    end
  end
  return #parts > 0 and table.concat(parts, ', ') or 'none'
end

local function hit(tag, snap, shaper, amount, desc, expected)
  local hp, pool = nGameObject.GetHitPoints(shaper), nGameObject.GetResourcePoints(shaper)
  local s, dealt, crit, absorbed = nGameObject.TakeDamage(snap, shaper, amount, 0, desc, kZeroVector)
  p(string.format('d13 shields %s: %s -> success %s dealt %s absorbed %s; hp %s -> %s, pool (HP + mShieldHealth) %s -> %s (%s)',
    tag, f2(amount), tostring(s), f2(dealt), f2(absorbed), f2(hp), f2(nGameObject.GetHitPoints(shaper)), f2(pool),
    f2(nGameObject.GetResourcePoints(shaper)), expected))
end

local ok, err = pcall(function()
  local shaper = humanShaper()
  if not shaper then
    p('d13 shields: no human player creature')
    return
  end
  dgsim_d13_shields_part = (dgsim_d13_shields_part or 0) + 1
  local part = dgsim_d13_shields_part
  local m, snap = enemySnapshot(shaper)
  if not snap then
    p('d13 shields: no enemy minion for the snapshot')
    return
  end
  p('d13 shields part', part, ': shaper', tostring(shaper), nGameObject.GetAssetName(shaper), 'level',
    nGameObject.GetPlayerLevel(shaper), 'hp', f2(nGameObject.GetHitPoints(shaper)), 'pool', f2(nGameObject.GetResourcePoints(shaper)),
    'armor', f2(nAttribute.GetAttributeValue(shaper, nAttributeType.PhysicalDamageReduction)), 'MR',
    f2(nAttribute.GetAttributeValue(shaper, nAttributeType.MagicalDamageReduction)), 'at match time', nGameSimulator.GetMatchTime())
  if part == 1 then
    local self = nAttribute.CreateAttributeSnapshot_FromObject(shaper, kObjIDNone)
    for _, s in ipairs(SHIELDS) do
      local r = nModifier.RequestModifier(shaper, shaper, nUtil.SPID(s[1]), self, s[2], kZeroVector, nil)
      p('d13 shields: RequestModifier', s[1], 'rank', s[2], '->', tostring(r))
    end
    nAttribute.RemoveAttributeSnapshot(self)
    p('d13 shields: shields', shieldState(shaper), '(mShieldHealth is summed at the frame end and after each hit)')
    hit('1a true', snap, shaper, 100, nDescriptors.IsTrueDamage,
      'expected absorbed 100: Bastion 60+20xlevel, Basko 6, Dibs the rest; HP unchanged')
    local hp, pool = nGameObject.GetHitPoints(shaper), nGameObject.GetResourcePoints(shaper)
    local r1, r2 = nAbility.RequestAbility(nUtil.SPID('ability_Alpha_W_Stim'), shaper, kObjIDNone, kZeroVector, 1, 0,
      nAbilitySlot.None, kObjIDNone, true, false, nil)
    p(string.format('d13 shields 1b W cost 25: RequestAbility -> %s %s; hp %s -> %s, pool %s -> %s (expected HP unchanged, pool -25: '
      .. 'Dibs 21 then 4 of Deflect)', tostring(r1), tostring(r2), f2(hp), f2(nGameObject.GetHitPoints(shaper)), f2(pool),
      f2(nGameObject.GetResourcePoints(shaper))))
    hit('1c magical', snap, shaper, 40, nDescriptors.IsMagicalDamage,
      'expected 26.14 after MR 53 (33 + Bastion 20): Faith 25 first (typed pass), 1.14 from Deflect; HP unchanged')
  elseif part == 2 then
    p('d13 shields: shields', shieldState(shaper), '(expected Bastion and Basko W gone)')
    hit('2 true', snap, shaper, 100, nDescriptors.IsTrueDamage,
      'expected Dibs (0), Deflect 54.86, Q 45.14 in request order (equal time left): dealt 0, 4.86 left')
  else
    p('d13 shields: shields', shieldState(shaper), '(expected only the untimed Faith shield)')
    hit('3 true', snap, shaper, 30, nDescriptors.IsTrueDamage, 'expected 30 dealt (Faith absorbs magical damage only)')
  end
  p('d13 shields: shields after', shieldState(shaper))
  nAttribute.RemoveAttributeSnapshot(snap)
  nGameObject.MarkForDelete(m)
end)
if not ok then p('d13 shields: ERROR', err) end
return table.concat(out, '\n')
