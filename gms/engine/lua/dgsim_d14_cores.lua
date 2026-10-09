-- D14b-live probe: a Guardian Core's death, the enemy minions' bonus and the core's own 480-s regrowth (D14b
-- "Structures"; MATCH_FLOW.md *D14*, *Structures*: ability_BaseNub_Death). [host] test_lua with three calls, e.g.
--   20:dgsim_d14_cores.lua,22:dgsim_d14_cores.lua,503:dgsim_d14_cores.lua
-- in a session of at least 505 match seconds (d14b_para_b's). Only one core dies, so the Guardian keeps its shield
-- (D14's guardian_shield: on while any core lives) and the waves can't reach an unshielded Guardian.
--   part 1: a Mortal melee and a Mortal ranged minion, both immobilized, at the Mortal start (their MaxHealth and Power
--     before); then nub_spirit_v5_A is killed (KillObject by the melee minion).
--   part 2: expected the core dead and untargetable, modifier_Guardian_NubA_Destroyed on the Spirit Guardian, the
--     Guardian still shielded (four cores), and the Mortal minions' bonus from AddGlobalAttributeModifier: melee
--     MaxHealth +30 and Power +2, ranged +15 and +4 (the existing minions too).
--   part 3 (death + 483 s): expected the core alive again (ability_BaseNub_Death's Deactivate after 450 + 30 s:
--     ResurrectObject), full HP, Untargetable 0, the Guardian's NubA modifier gone, the minions' bonus removed, the shield
--     on; the host's log has `match: SetEnergyLineState(line 8, value 0.0, team 2)` about 1 s after the resurrect
--     (modifier_BaseNub_Passive) and the clients the NubRespawningSoon / NubRespawned feedback.
-- Returns a report; every line starts with "d14 cores".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'd14 cores ' .. table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local S = dgsim_d14_cores or {}
dgsim_d14_cores = S

local function find(name)
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if nGameObject.GetAssetName(id) == name then return id end
  end
end

local function attr(o, name)
  local ok, v = pcall(nAttribute.GetAttributeValue, o, nAttributeType[name])
  if ok then return f1(v) end
  return '?'
end

local function minion(o, tag)
  if not o or o == kObjIDNone then return tag .. ' none' end
  return string.format('%s %s MaxHealth %s hp %s Power %s', tag, tostring(o), f1(nGameObject.GetMaxHitPoints(o)),
    f1(nGameObject.GetHitPoints(o)), attr(o, 'Power'))
end

local function core(o)
  if not o then return 'core A none' end
  return string.format('core A %s alive %s hp %s/%s Untargetable %s', tostring(o), tostring(nGameObject.IsAlive(o)),
    f1(nGameObject.GetHitPoints(o)), f1(nGameObject.GetMaxHitPoints(o)), attr(o, 'Untargetable'))
end

local function guardian()
  local g = nBattlegrounds.GetGuardianOnTeam(nTeamID.Spirit)
  local shield = nModifier.GetFirstModifierByGUID(g, nUtil.SPID('modifier_Tower_Immunity'))
  local nubA = nModifier.GetFirstModifierByGUID(g, nUtil.SPID('modifier_Guardian_NubA_Destroyed'))
  return string.format('Spirit Guardian %s modifier_Tower_Immunity %s Immune %s, modifier_Guardian_NubA_Destroyed %s', tostring(g),
    tostring(shield ~= nil and shield ~= kObjIDNone and shield or 'none'), attr(g, 'Immune'),
    tostring(nubA ~= nil and nubA ~= kObjIDNone and nubA or 'none'))
end

local function spawn(noun, x, y)
  local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), nTeamID.Mortal, nVector.create(x, y, 0), kZeroVector, 1)
  if m and m ~= kObjIDNone then
    nAttribute.AddAttributeModifier(m, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
    return m
  end
end

local ok, err = pcall(function()
  S.part = (S.part or 0) + 1
  p('part', S.part, 'at match time', f1(nGameSimulator.GetMatchTime()))
  if S.part == 1 then
    S.melee = spawn('Minion_Mortal_Melee.Noun', -212, -238)
    S.ranged = spawn('Minion_Mortal_Ranged.Noun', -238, -212)
    S.core = find('nub_spirit_v5_A.Noun') or find('nub_spirit_v5_A')
    p('before:', minion(S.melee, 'melee'), ';', minion(S.ranged, 'ranged'), ';', core(S.core), ';', guardian())
    if S.core then nGameObject.KillObject(S.core, S.melee or kObjIDNone, false) end
    p('core A killed ->', core(S.core))
  elseif S.part == 2 then
    p('after the death:', core(S.core), '(expected dead, Untargetable 1) ;', guardian(),
      '(expected the shield still on and the NubA modifier)')
    p(minion(S.melee, 'melee'), '(expected MaxHealth and Power +30 / +2 over part 1) ;', minion(S.ranged, 'ranged'),
      '(expected +15 / +4)')
  else
    p('after the regrowth:', core(S.core), '(expected alive, full hp, Untargetable 0) ;', guardian(),
      '(expected the shield on and no NubA modifier)')
    p(minion(S.melee, 'melee'), ';', minion(S.ranged, 'ranged'), '(expected back to part 1\'s values)')
    for _, m in ipairs({S.melee, S.ranged}) do
      if m and nGameObject.IsAlive(m) then nGameObject.MarkForDelete(m) end
    end
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
