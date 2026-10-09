-- D1f-live test probe (DAMAGE.md T8, second half; MATCH_FLOW.md T13): the Mortal Guardian (dgsim_kill_team, default
-- nTeamID.Mortal) hit once by a Spirit melee minion's snapshot (its passive's OnTookDamage stores lastAttacker), then
-- killed by a lethal true-damage hit from the same snapshot, through the host's damage core. Expected: death processing,
-- the passive's OnAgentDeath end a few scheduler runs later, its Deactivate calling
-- nBattlegrounds.OnTitanTowerDestroyed(guardian, minion) ("match: nBattlegrounds.OnTitanTowerDestroyed"), and the match
-- ending through it (reason 3 for the Mortal Guardian), not through the HP check. The helper minion stands in the
-- Mortal base and is marked for delete. Returns a report (logged by the host).
-- D14: the host keeps the Guardian invincible while a core of its team lives (modifier_Tower_Immunity, [host]
-- guardian_shield); as a test fixture this probe ends that modifier first (nModifier.MarkForDelete -> EndAbility, at
-- once outside an ability's own Lua call), so the hits land; the host doesn't put it back on a dead Guardian. A real
-- game kills the five cores first (MATCH_FLOW.md *D14*).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local ok, err = pcall(function()
  local team = dgsim_kill_team or nTeamID.Mortal
  local g = nBattlegrounds.GetGuardianOnTeam(team)
  if g == nil or g == kObjIDNone then
    p('kill guardian: no Guardian on team', team)
    return
  end
  local shield = nModifier.GetFirstModifierByGUID(g, nUtil.SPID('modifier_Tower_Immunity'))
  if shield ~= nil and shield ~= kObjIDNone then
    nModifier.MarkForDelete(g, shield)
    p('kill guardian: its shield (modifier_Tower_Immunity', tostring(shield), ') ended for the test')
  end
  local gp = nGameObject.GetPosition(g)
  local enemy = team == nTeamID.Mortal and 'Spirit' or 'Mortal'
  local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_' .. enemy .. '_Melee.Noun'), nTeamID[enemy],
    nVector.create(gp.x + 6, gp.y + 6, 0), kZeroVector, 1)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(m, kObjIDNone)
  local hp0 = nGameObject.GetHitPoints(g)
  local s1, d1 = nGameObject.TakeDamage(snap, g, 100, 0, nDescriptors.IsTrueDamage, kZeroVector)
  local hp1 = nGameObject.GetHitPoints(g)
  local s2, d2 = nGameObject.TakeDamage(snap, g, hp1 + 100, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  p('kill guardian:', tostring(g), nGameObject.GetAssetName(g), 'by', tostring(m), 'hit 100 ->', tostring(s1), f2(d1),
    'hp', f2(hp0), '->', f2(hp1), '; lethal hit ->', tostring(s2), f2(d2), 'hp', f2(nGameObject.GetHitPoints(g)), 'alive',
    tostring(nGameObject.IsAlive(g)), 'at match time', nGameSimulator.GetMatchTime())
  nGameObject.MarkForDelete(m)
end)
if not ok then p('kill guardian: ERROR', err) end
return table.concat(out, '\n')
