-- MAPED3 / MAPED2b-bush live probe ([host] test_lua=<T>:dgsim_maped3_bush.lua,<T+2>:...,<T+10>:...): does a compiled
-- map's bush hide the human's shaper from the other team? The human stands in a bush (moved
-- there by the commands file). The first call teleports the nearest living enemy shaper 10 units east and 8 north of
-- the human (outside an 8-unit-wide bush, inside its sight), facing it. Every call reports the human's place, its
-- grass (the host's fog lines give the id), whether each team sees the other's shaper (nGameObject.IsVisibleToTeam:
-- the host's mVisibleToTeam, which hides fog, grass and stealth) and their distance. Expected: in the bush the enemy
-- doesn't see the human while the human sees the enemy; once the human has stepped out (the commands file), both see
-- each other. Every line starts with "maped3 bush". Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  dgsim_maped3_bush_call = (dgsim_maped3_bush_call or 0) + 1
  local call = dgsim_maped3_bush_call
  local human
  local players = {}
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone then
      players[#players + 1] = c
      if not human and not nPlayer.IsBot(c) then human = c end
    end
  end
  if not human then
    p('maped3 bush: no human player creature')
    return
  end
  local team = nGameObject.GetTeam(human)
  local at = nGameObject.GetPosition(human)
  local enemy = dgsim_maped3_bush_enemy
  if call == 1 or enemy == nil or not nGameObject.IsAlive(enemy) then
    local best, bd
    for _, c in ipairs(players) do
      if nGameObject.GetTeam(c) ~= team and nGameObject.IsAlive(c) then
        local q = nGameObject.GetPosition(c)
        local d = math.sqrt((q.x - at.x) ^ 2 + (q.y - at.y) ^ 2)
        if not bd or d < bd then best, bd = c, d end
      end
    end
    enemy = best
    dgsim_maped3_bush_enemy = enemy
    if enemy and call == 1 then
      nLocomotion.TeleportToPositionAndFacing(enemy, nVector.create(at.x + 10, at.y + 8, 0), nVector.create(-1, 0, 0))
      p(string.format('maped3 bush call 1: enemy shaper %s %s teleported to (%.1f, %.1f), beside the human',
        tostring(enemy), tostring(nGameObject.GetAssetName(enemy)), at.x + 10, at.y + 8))
    end
  end
  if not enemy then
    p(string.format('maped3 bush call %d: no living enemy shaper', call))
    return
  end
  local e = nGameObject.GetPosition(enemy)
  local d = math.sqrt((e.x - at.x) ^ 2 + (e.y - at.y) ^ 2)
  local eteam = nGameObject.GetTeam(enemy)
  p(string.format('maped3 bush call %d at match %.2f s: human %s %s team %s at (%.1f, %.1f) hp %.0f; enemy %s %s ' ..
    'team %s at (%.1f, %.1f), %.1f units away; the enemy team sees the human: %s; the human\'s team sees the enemy: %s',
    call, nGameSimulator.GetMatchTime(), tostring(human), tostring(nGameObject.GetAssetName(human)), tostring(team),
    at.x, at.y, nGameObject.GetHitPoints(human) or -1, tostring(enemy), tostring(nGameObject.GetAssetName(enemy)),
    tostring(eteam), e.x, e.y, d, tostring(nGameObject.IsVisibleToTeam(human, eteam)),
    tostring(nGameObject.IsVisibleToTeam(enemy, team))))
end)
if not ok then p('maped3 bush: ERROR', err) end
return table.concat(out, '\n')
