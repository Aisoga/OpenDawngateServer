-- D1c-live test probe (MATCH_FLOW.md T13, the host's half; [host] test_lua=<s>:dgsim_match_end_tower.lua): the Spirit
-- Guardian reported destroyed through nBattlegrounds.OnTitanTowerDestroyed(guardian, killer), the call
-- modifier_BaseTitan_V5_Passive.Deactivate makes once D1f-live ends the passive with the Guardian's death. The killer is
-- the nearest Mortal minion if there is one. Expected: the host's EndGame(sim, 1) (Mortal wins), BF 00, C1 01. Returns
-- a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local g = nBattlegrounds.GetGuardianOnTeam(dgsim_tower_team or nTeamID.Spirit)
  if g == nil or g == kObjIDNone then
    p('end tower: no Guardian')
    return
  end
  local killer = kObjIDNone
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if string.find(nGameObject.GetAssetName(id) or '', '^Minion_Mortal_') and nGameObject.IsAlive(id) then
      killer = id
      break
    end
  end
  nBattlegrounds.OnTitanTowerDestroyed(g, killer)
  p('end tower: OnTitanTowerDestroyed', tostring(g), nGameObject.GetAssetName(g), 'killer', tostring(killer), 'at match time',
    nGameSimulator.GetMatchTime())
end)
if not ok then p('end tower: ERROR', err) end
return table.concat(out, '\n')
