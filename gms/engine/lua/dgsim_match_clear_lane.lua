-- D1c-live test probe (MATCH_FLOW.md T5; [host] test_lua=<s>:dgsim_match_clear_lane.lua, with wave_spawners=Mortal_Top):
-- clears the top lane for a lone Mortal wave, so its minions walk blue_top to the last knot without a fight: kills the
-- Spirit Bindings of the top lane (Outer, Middle, Inner: y > x) and the Spirit nubs A and C next to the lane's last knots
-- with nGameObject.KillObject (their death abilities run; the nubs' ask nBattlegrounds.GetGuardianOnTeam). Returns a
-- report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local seen = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      local pos = nGameObject.GetPosition(id)
      local kill = false
      if string.find(name, '^Binding_Spirit_') and pos and pos.y > pos.x then kill = true end
      if name == 'nub_spirit_v5_A' or name == 'nub_spirit_v5_C' then kill = true end
      if kill and nGameObject.IsAlive(id) then
        nGameObject.KillObject(id)
        p('clear lane:', name, key, string.format('(%.1f %.1f)', pos.x, pos.y), '->', nGameObject.GetHitPoints(id))
      end
    end
  end
end)
if not ok then p('clear lane: ERROR', err) end
return table.concat(out, '\n')
