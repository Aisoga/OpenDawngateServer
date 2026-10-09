-- D1i test probe ([host] test_lua=<s>:dgsim_regen_hurt.lua; DAMAGE.md *D1i*): hurt the Spirit Guardian (default 2000 true
-- damage, dgsim_regen_guardian_damage) and player 0's shaper (default 300, dgsim_regen_shaper_damage) through the host's
-- damage core, each by a snapshot of an enemy melee minion that is marked for delete right away (so nothing fights
-- back). Expected afterwards (regen.cpp): the Guardian regains its bar over 240 s (HealthRegen 5000 / 48 = 104.17 per
-- 5 s, 20.83 HP/s), the shaper its class regen (PC_Alpha 5 + 0.45 x (level - 1) per 5 s) while it stands outside its
-- base zone. Ids in dgsim_regen_ids for dgsim_regen_report.lua. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local function hurt(target, enemy, amount, what)
  local tp = nGameObject.GetPosition(target)
  local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_' .. enemy .. '_Melee.Noun'), nTeamID[enemy],
    nVector.create(tp.x + 6, tp.y + 6, 0), kZeroVector, 1)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(m, kObjIDNone)
  local hp0 = nGameObject.GetHitPoints(target)
  local s, d = nGameObject.TakeDamage(snap, target, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  nGameObject.MarkForDelete(m)
  p('regen hurt:', what, tostring(target), nGameObject.GetAssetName(target), 'hit', amount, '->', tostring(s), f2(d), 'hp',
    f2(hp0), '->', f2(nGameObject.GetHitPoints(target)), 'of', f2(nGameObject.GetMaxHitPoints(target)), 'HealthRegen',
    f2(nAttribute.GetAttributeValue(target, nAttributeType.HealthRegen)), 'TotalHealthRegen',
    f2(nAttribute.GetDerivedAttributeValue(target, nDerivedAttributeType.TotalHealthRegen)), 'resource',
    f2(nGameObject.GetResourcePoints(target)), 'of', f2(nGameObject.GetMaxResourcePoints(target)), 'type',
    tostring(nGameObject.GetResourcePointType(target)), 'pos', f2(tp.x), f2(tp.y))
end

local ok, err = pcall(function()
  dgsim_regen_ids = dgsim_regen_ids or {}
  local g = nBattlegrounds.GetGuardianOnTeam(nTeamID.Spirit)
  if g ~= nil and g ~= kObjIDNone then
    dgsim_regen_ids.guardian = g
    hurt(g, 'Mortal', dgsim_regen_guardian_damage or 2000, 'Spirit Guardian')
  else
    p('regen hurt: no Spirit Guardian')
  end
  local shaper = nPlayer.GetPlayerControlledObjectID(0)
  if shaper ~= nil and shaper ~= kObjIDNone then
    dgsim_regen_ids.shaper = shaper
    hurt(shaper, 'Spirit', dgsim_regen_shaper_damage or 300, 'shaper')
  else
    p('regen hurt: no shaper for player 0')
  end
  p('regen hurt: at match time', nGameSimulator.GetMatchTime())
end)
if not ok then p('regen hurt: ERROR', err) end
return table.concat(out, '\n')
