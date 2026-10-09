-- D5 test probe (SERVER_LUA_API.md tier 1, MATCH_FLOW.md *D5*; [host] test_lua=<s>:dgsim_d5_clear_lane.lua, with
-- wave_spawners=Mortal_Top): clears the top lane for the Mortal waves, so they walk blue_top into the Spirit base. Kills
-- the Spirit Bindings of the top lane (Outer, Middle, Inner: y > x) with nGameObject.KillObject(binding, killer), the
-- killer the top lane's Binding_Mortal_Outer (x < 0), through the host's death processing (D1f-live): each Binding's
-- passive reports itself with nBattlegrounds.OnTitanTowerDestroyed. Unlike dgsim_match_clear_lane.lua (D1c-live) it
-- leaves the nubs standing: the minions attack them. Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local live, seen = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if not seen[tostring(id)] then
      seen[tostring(id)] = true
      live[#live + 1] = id
    end
  end
  local killer = kObjIDNone
  for _, id in ipairs(live) do
    local pos = nGameObject.GetPosition(id)
    if nGameObject.GetAssetName(id) == 'Binding_Mortal_Outer' and pos and pos.x < 0 then killer = id end
  end
  for _, id in ipairs(live) do
    local name = nGameObject.GetAssetName(id) or ''
    local pos = nGameObject.GetPosition(id)
    if string.find(name, '^Binding_Spirit_') and pos and pos.y > pos.x and nGameObject.IsAlive(id) then
      nGameObject.KillObject(id, killer, false)
      p('clear lane:', name, tostring(id), string.format('(%.1f %.1f)', pos.x, pos.y), 'killed by', tostring(killer),
        '-> hp', nGameObject.GetHitPoints(id), 'at match time', nGameSimulator.GetMatchTime())
    end
  end
end)
if not ok then p('clear lane: ERROR', err) end
return table.concat(out, '\n')
