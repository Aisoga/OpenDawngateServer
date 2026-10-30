-- ADAMSLOW test probe ([host] test_lua=<s>:dgsim_adamslow_round.lua, after dgsim_adamslow_lib.lua): one round with player 0 as the Lockdown holder (Adamance bought by the
-- commands file). Gathers player 0's allied bots, its own team's lane minions, other-team minions, a created jungle
-- creep and the first live enemy bot (the attacker, 3 east) around it, then the enemy bot hits it with
-- dgsim_adamslow_hits (6) physical basics of dgsim_adamslow_damage (10): the fifth stack fires the nova (the sixth in
-- case the first request counts 0). Then the first scan; dgsim_adamslow_scan.lua (repeated) logs the later ones.
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local L = dgsim_adamslow
  if L == nil then
    p('adamslow round: no dgsim_adamslow (run dgsim_adamslow_lib.lua first)')
    return
  end
  local me = nPlayer.GetPlayerControlledObjectID(0)
  if not L.ok_id(me) or not nGameObject.IsAlive(me) then
    p('adamslow round: player 0 has no live shaper')
    return
  end
  local team = nGameObject.GetTeam(me)
  local enemy
  for i = 1, 9 do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if L.ok_id(id) and nGameObject.IsAlive(id) and nGameObject.GetTeam(id) ~= team then
      enemy = id
      break
    end
  end
  if enemy == nil then
    p('adamslow round: no live enemy player')
    return
  end
  dgsim_adamslow_round_n = (dgsim_adamslow_round_n or 0) + 1
  p('adamslow round', dgsim_adamslow_round_n, ': holder player 0', tostring(me), 'stacks before', L.stacks(me))
  L.gather(me, enemy, p)
  L.hit(me, enemy, dgsim_adamslow_hits or 6, dgsim_adamslow_damage or 10, p)
  L.scan('round' .. dgsim_adamslow_round_n, p)
end)
if not ok then p('adamslow round: ERROR', err) end
return table.concat(out, '\n')
