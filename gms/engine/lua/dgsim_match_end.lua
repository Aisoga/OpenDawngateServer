-- D1c-live test probe (MATCH_FLOW.md T13/T14; [host] test_lua=<s>:dgsim_match_end.lua): the game end.
--   1. a Binding through nBattlegrounds.OnTitanTowerDestroyed(binding, killer) directly (what modifier_Binding_Passive's
--      HandleBindingDeath calls once D1f-live ends it): the host marks it destroyed in the tower table and calls
--      nGameModeHandlers[OnTowerDestroyed] (R10 test R10); dgsim_end_binding (default Binding_Spirit_Outer, top lane);
--   2. a Guardian killed with nGameObject.KillObject (T14: before D1f nothing ends its passive, so no
--      OnTitanTowerDestroyed; the host's HP check ends the match): dgsim_end_team (default 1: the Mortal Guardian,
--      reason 3, Spirit wins).
-- Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local bname = dgsim_end_binding or 'Binding_Spirit_Outer'
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if nGameObject.GetAssetName(id) == bname then
      local pos = nGameObject.GetPosition(id)
      if pos and pos.x < 0 then
        nBattlegrounds.OnTitanTowerDestroyed(id, kObjIDNone)
        p('end: OnTitanTowerDestroyed', bname, tostring(id), string.format('(%.1f %.1f)', pos.x, pos.y))
        break
      end
    end
  end
  local team = dgsim_end_team or nTeamID.Mortal
  local g = nBattlegrounds.GetGuardianOnTeam(team)
  if g == nil or g == kObjIDNone then
    p('end: no Guardian on team', team)
    return
  end
  local hp = nGameObject.GetHitPoints(g)
  nGameObject.KillObject(g)
  p('end: KillObject on the Guardian of team', team, tostring(g), nGameObject.GetAssetName(g), 'hp', hp, '->',
    nGameObject.GetHitPoints(g), 'at match time', nGameSimulator.GetMatchTime())
end)
if not ok then p('end: ERROR', err) end
return table.concat(out, '\n')
