-- D9 test probe ([host] test_lua=<s>:dgsim_wells_report.lua): the bots' well getters on the server
-- (nBattlegrounds.GetMinesForTeam / GetFollowersForTitan / IsTitanNodeLocked, the host's since D9) and the engine's own
-- UI-data bindings over the server's node list (SpiritWell_GetClosest / _GetOwningTeam / _IsLocked). Returns a report
-- (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  for team = 1, 2 do
    for _, unlocked in ipairs({false, true}) do
      local mines = nBattlegrounds.GetMinesForTeam(team, unlocked)
      local ids = {}
      for _, w in ipairs(mines or {}) do
        ids[#ids + 1] = string.format('%s(workers %s, locked %s)', tostring(w), tostring(nBattlegrounds.GetFollowersForTitan(w)),
                                      tostring(nBattlegrounds.IsTitanNodeLocked(w)))
      end
      p('wells report: GetMinesForTeam(' .. team .. ', ' .. tostring(unlocked) .. '):', #(mines or {}), table.concat(ids, ' '))
    end
  end
  for _, xy in ipairs({{182.5, -27.9}, {-182.3, 27.9}, {-27.6, 182.7}, {28.2, -182.6}}) do
    local v = nVector.create(xy[1], xy[2], 0)
    local ok2, i = pcall(nBattlegrounds.SpiritWell_GetClosest, v)
    if ok2 and i ~= nil then
      p('wells report: SpiritWell_GetClosest', xy[1], xy[2], '->', i, 'owner', nBattlegrounds.SpiritWell_GetOwningTeam(i),
        'locked', tostring(nBattlegrounds.SpiritWell_IsLocked(i)))
    else
      p('wells report: SpiritWell_GetClosest', xy[1], xy[2], 'failed:', tostring(i))
    end
  end
  p('wells report: at match time', nGameSimulator.GetMatchTime())
end)
if not ok then p('wells report: ERROR', err) end
return table.concat(out, '\n')
