-- D1f-live test probe (DAMAGE.md T8, first half; MATCH_FLOW.md T13's Binding and the tower reward): the bottom lane's
-- Binding_Spirit_Outer (x > 0) killed with nGameObject.KillObject(binding, killer) by the bottom Binding_Mortal_Outer.
-- Through the host's GameObject_Kill detour (death.cpp): OnNearbyDeath reaches the victim's own modifier_Binding_Passive,
-- whose Tick wakes up and calls nBattlegrounds.OnTitanTowerDestroyed(binding, killer) before the OnAgentDeath end a few
-- scheduler runs later (log "match: nBattlegrounds.OnTitanTowerDestroyed"); the Mortal team gets the Tower reward (150
-- gold each, "team"). Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local function find(name)
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if nGameObject.GetAssetName(id) == name then
      local pos = nGameObject.GetPosition(id)
      if pos and pos.x > 0 then return id end
    end
  end
end

local ok, err = pcall(function()
  local victim = find(dgsim_kill_binding or 'Binding_Spirit_Outer')
  local killer = find('Binding_Mortal_Outer')
  if not victim then
    p('kill binding: not found')
    return
  end
  local hp = nGameObject.GetHitPoints(victim)
  nGameObject.KillObject(victim, killer or kObjIDNone, false)
  p('kill binding:', tostring(victim), nGameObject.GetAssetName(victim), 'hp', hp, '->', nGameObject.GetHitPoints(victim),
    'killer', tostring(killer), 'at match time', nGameSimulator.GetMatchTime(), 'gold of player 0',
    tostring(nPlayer.GetPlayerAvailableGold and nPlayer.GetPlayerAvailableGold(0)))
end)
if not ok then p('kill binding: ERROR', err) end
return table.concat(out, '\n')
