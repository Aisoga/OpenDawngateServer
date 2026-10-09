-- D13 live probe (docs/DAMAGE.md *Combat edge cases (D13)*; [host] test_lua=<T>:dgsim_d13_misc.lua,<T+1.5>:
-- dgsim_d13_misc.lua,<T+3.5>:dgsim_d13_misc.lua,<T+16>:dgsim_d13_misc.lua; before the first wave, so the bottom lane is
-- empty). Helper minions stand on the bottom lane out of the towers' reach; the human's shaper is the caster. Parts:
--   part 1:
--     a) King of Masks' damage stacker (modifier_KingOfMasks_Q_DamageStacker: 0.1 s, its damage in Deactivate, which the
--        host's end pass runs after the instance ended its time) requested 3 times on an enemy minion: expected about
--        0.1 s later one hit of (25 + 0.35 Power) x 3 = 75 magical (MR 0 at level 1), no exception;
--     b) Mortal Strike (modifier_StatusEffect_MortalStrike with the request parameter duration 3) on another enemy minion
--        (its HealingReduction comes in its Tick, so part 2 hurts it 200 and heals 100 through nGameObject.HealDamage:
--        expected HealingReduction 0.5, +50);
--     c) the Purity lockout (modifier_Item_Advanced_HealNearbyAlly_RecentlyHealed, duration 15) on the shaper;
--     d) an attack reduced below zero: an enemy minion with Power -20 (Discipline gives -10 per stack) hits a friendly
--        minion with a basic of 12 (coefficient 1): 12 - 20 < 0, expected dealt 0, HP unchanged, the host's line showing
--        a negative initial damage and "final 0.00", no absorb handler called;
--     e) Finesse (modifier_Item_Legendary_OnBasic_StackingDamageAmp) and Resonance (_OnBasic_MagicDamageNearby) on the
--        shaper; an enemy minion with Fenmore's Q shield (50) and a second one beside it; a basic of 20 from the shaper:
--        + 20 (Finesse) = 40 into the shield (dealt 0); expected Finesse's stack on the target all the same, and
--        Resonance's zap of 0 (dealt x 0.5) to the second minion and its own 15 (+ 0.1 Power) magical hit on the target,
--        which the Finesse stack just placed raises 5 % in ModifyDamageFinal: 15.75 (10 into the shield, 5.75 through;
--        D13-live, d13_run1b);
--   part 2 (1.5 s): a) the stacker's target 75 down; b) HealingReduction 0.5, the heal halved; e) Finesse and Resonance
--     removed from the shaper again;
--   part 3 (3.5 s): b) Mortal Strike ended on time: HealingReduction 0, a heal of 100 gives +100; c) the lockout still on;
--   part 4 (16 s): c) the lockout ended (OnDuration, 15 s). The helpers are marked for delete here.
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

-- nPlayer.IsBot takes the player's creature (an object id), not the player id (D13-live: a player id asserted
-- SporeLabsLua.h 77 "Guid at index [1] is unexpected type" and answered false)
local function humanShaper()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c end
  end
end

local function has(o, name)
  local id = nModifier.GetFirstModifierByGUID(o, nUtil.SPID(name))
  if id == nil or id == kObjIDNone then return nil end
  return id
end

local function params(duration)
  local t = nTable.CreateTable()
  nTable.SetTableValue(t, 'duration', duration)
  return t
end

local ok, err = pcall(function()
  local shaper = humanShaper()
  if not shaper then
    p('d13 misc: no human player creature')
    return
  end
  local team = nGameObject.GetTeam(shaper)
  local enemyTeam, enemyNoun, ownNoun = nTeamID.Spirit, 'Minion_Spirit_Melee.Noun', 'Minion_Mortal_Melee.Noun'
  if team == nTeamID.Spirit then enemyTeam, enemyNoun, ownNoun = nTeamID.Mortal, 'Minion_Mortal_Melee.Noun', 'Minion_Spirit_Melee.Noun' end
  dgsim_d13_misc_part = (dgsim_d13_misc_part or 0) + 1
  local part = dgsim_d13_misc_part
  local H = dgsim_d13_misc or {}
  dgsim_d13_misc = H
  local function spawn(key, noun, tm, x, y)
    local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), tm, nVector.create(x, y, 0), kZeroVector, 1)
    H[key] = m
    return m
  end
  if part == 1 then
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(shaper, kObjIDNone)
    -- a) King of Masks' stacker
    local a = spawn('stacker', enemyNoun, enemyTeam, 66.0, -71.0)
    for i = 1, 3 do nModifier.RequestModifier(a, shaper, nUtil.SPID('modifier_KingOfMasks_Q_DamageStacker'), snap, 1, kZeroVector, nil) end
    local st = has(a, 'modifier_KingOfMasks_Q_DamageStacker')
    p('d13 misc 1a stacker: target', tostring(a), 'hp', f2(nGameObject.GetHitPoints(a)), 'stacks',
      tostring(st and nModifier.GetStackCount(st)), '(expected 3; 75 magical about 0.1 s later)')
    -- b) Mortal Strike (its HealingReduction is added in its Tick, at the next scheduler run: checked in part 2)
    local b = spawn('mortal', enemyNoun, enemyTeam, 73.2, -64.4)
    nModifier.RequestModifier(b, shaper, nUtil.SPID('modifier_StatusEffect_MortalStrike'), snap, 1, kZeroVector, params(3))
    local ms = has(b, 'modifier_StatusEffect_MortalStrike')
    p('d13 misc 1b Mortal Strike:', tostring(ms), 'remaining', f2(ms and nModifier.GetRemainingDuration(ms)), '(expected 3 s)')
    -- c) the Purity lockout on the shaper
    nModifier.RequestModifier(shaper, shaper, nUtil.SPID('modifier_Item_Advanced_HealNearbyAlly_RecentlyHealed'), snap, 1,
      kZeroVector, params(15))
    local lk = has(shaper, 'modifier_Item_Advanced_HealNearbyAlly_RecentlyHealed')
    p('d13 misc 1c lockout:', tostring(lk), 'remaining', f2(lk and nModifier.GetRemainingDuration(lk)), '(expected 15 s)')
    -- d) below zero
    local d1 = spawn('weak', enemyNoun, enemyTeam, 62.6, -75.0)
    local d2 = spawn('friend', ownNoun, team, 64.0, -76.0)
    nAttribute.AddAttributeModifier(d1, nAttributeType.Power, -20, nAttributeOp.kAdd)
    local ws = nAttribute.CreateAttributeSnapshot_FromObject(d1, kObjIDNone)
    local basic = nBit.Or(nBit.Or(nDescriptors.IsMelee, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
    local hd = nGameObject.GetHitPoints(d2)
    local s, dealt = nGameObject.TakeDamage(ws, d2, 12, 1, basic, kZeroVector)
    nAttribute.RemoveAttributeSnapshot(ws)
    p('d13 misc 1d below zero: Power', f2(nAttribute.GetAttributeValue(d1, nAttributeType.Power)), 'basic 12 (coef 1) ->',
      tostring(s), 'dealt', f2(dealt), 'hp', f2(hd), '->', f2(nGameObject.GetHitPoints(d2)), '(expected dealt 0, unchanged)')
    -- e) Finesse and Resonance against a full shield
    local e1 = spawn('shielded', enemyNoun, enemyTeam, 70.0, -60.0)
    local e2 = spawn('beside', enemyNoun, enemyTeam, 72.0, -61.0)
    local es = nAttribute.CreateAttributeSnapshot_FromObject(e1, kObjIDNone)
    nModifier.RequestModifier(e1, e1, nUtil.SPID('modifier_Alpha_Q_Shield'), es, 1, kZeroVector, nil)
    nAttribute.RemoveAttributeSnapshot(es)
    nModifier.RequestModifier(shaper, shaper, nUtil.SPID('modifier_Item_Legendary_OnBasic_StackingDamageAmp'), snap, 1, kZeroVector, nil)
    nModifier.RequestModifier(shaper, shaper, nUtil.SPID('modifier_Item_Legendary_OnBasic_MagicDamageNearby'), snap, 1, kZeroVector, nil)
    local fs = nAttribute.CreateAttributeSnapshot_FromObject(shaper, kObjIDNone)
    local h1, h2 = nGameObject.GetHitPoints(e1), nGameObject.GetHitPoints(e2)
    local s2, dealt2, c2, abs2 = nGameObject.TakeDamage(fs, e1, 20, 0, basic, kZeroVector)
    nAttribute.RemoveAttributeSnapshot(fs)
    local amp = has(e1, 'modifier_Item_Legendary_OnBasic_StackingDamageAmp_DamageAmp')
    p('d13 misc 1e Finesse/Resonance: basic 20 ->', tostring(s2), 'dealt', f2(dealt2), 'absorbed', f2(abs2),
      '(expected 0 and 40); Finesse stack on the target', tostring(amp and nModifier.GetStackCount(amp)), '(expected 1);',
      'target hp', f2(h1), '->', f2(nGameObject.GetHitPoints(e1)),
      '(expected -5.75: Resonance 15 magical x 1.05 from the Finesse stack the basic just put on it, 10 into the shield);',
      'beside hp', f2(h2), '->', f2(nGameObject.GetHitPoints(e2)), '(expected unchanged: a zap of 0)')
    nAttribute.RemoveAttributeSnapshot(snap)
  elseif part == 2 then
    p('d13 misc 2a stacker target hp', f2(H.stacker and nGameObject.GetHitPoints(H.stacker)), '(expected 75 less)')
    local b = H.mortal
    if b then
      local snap = nAttribute.CreateAttributeSnapshot_FromObject(shaper, kObjIDNone)
      nGameObject.TakeDamage(snap, b, 200, 0, nDescriptors.IsTrueDamage, kZeroVector)
      local hb = nGameObject.GetHitPoints(b)
      local healed = nGameObject.HealDamage(snap, b, 100, 0, 0, false, false, false, false)
      nAttribute.RemoveAttributeSnapshot(snap)
      p('d13 misc 2b Mortal Strike still', tostring(has(b, 'modifier_StatusEffect_MortalStrike')), 'HealingReduction',
        f2(nAttribute.GetAttributeValue(b, nAttributeType.HealingReduction)), 'heal 100 ->', f2(healed), 'hp', f2(hb), '->',
        f2(nGameObject.GetHitPoints(b)), '(expected on, 0.5, +50)')
    end
    for _, n in ipairs({'modifier_Item_Legendary_OnBasic_StackingDamageAmp', 'modifier_Item_Legendary_OnBasic_MagicDamageNearby'}) do
      local id = has(shaper, n)
      if id then nModifier.MarkForDelete(shaper, id) end
    end
  elseif part == 3 then
    local b = H.mortal
    if b then
      local snap = nAttribute.CreateAttributeSnapshot_FromObject(shaper, kObjIDNone)
      local hb = nGameObject.GetHitPoints(b)
      local healed = nGameObject.HealDamage(snap, b, 100, 0, 0, false, false, false, false)
      nAttribute.RemoveAttributeSnapshot(snap)
      p('d13 misc 3b Mortal Strike', tostring(has(b, 'modifier_StatusEffect_MortalStrike')), 'HealingReduction',
        f2(nAttribute.GetAttributeValue(b, nAttributeType.HealingReduction)), 'heal 100 ->', f2(healed), 'hp', f2(hb), '->',
        f2(nGameObject.GetHitPoints(b)), '(expected gone after 3 s, 0, +100)')
    end
    local lk = has(shaper, 'modifier_Item_Advanced_HealNearbyAlly_RecentlyHealed')
    p('d13 misc 3c lockout', tostring(lk), 'remaining', f2(lk and nModifier.GetRemainingDuration(lk)), '(expected still on)')
  else
    p('d13 misc 4c lockout', tostring(has(shaper, 'modifier_Item_Advanced_HealNearbyAlly_RecentlyHealed')),
      '(expected nil: ended after 15 s)')
    for _, m in pairs(H) do
      if m and m ~= kObjIDNone then nGameObject.MarkForDelete(m) end
    end
    dgsim_d13_misc = nil
  end
end)
if not ok then p('d13 misc: ERROR', err) end
return table.concat(out, '\n')
