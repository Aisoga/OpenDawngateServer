-- D2h test fixture ([host] test_lua=<s>:dgsim_d2h_away.lua, after dgsim_d2h_here.lua; docs/GMS_PROTOCOL.md *The enemy
-- players' level and FoW data (D2h)*): sends every creature dgsim_d2h_here.lua brought next to player 0 back to where it
-- came from (its base: out of player 0's team's sight again), then reports every player's level and whether teams 1 and
-- 2 see its creature. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f)', v.x, v.y) end

local ok, err = pcall(function()
  local n = 0
  for i, from in pairs(dgsim_d2h_from or {}) do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if id ~= nil and id ~= kObjIDNone then
      nLocomotion.TeleportToPositionAndFacing(id, nVector.create(from[1], from[2], 0), nVector.create(0, 1, 0))
      p('d2h away: player', i, tostring(id), nGameObject.GetAssetName(id), 'level', nGameObject.GetPlayerLevel(id),
        'back to', vec(nGameObject.GetPosition(id)), 'at match', string.format('%.2f', nGameSimulator.GetMatchTime()))
      n = n + 1
    end
  end
  if n == 0 then p('d2h away: nothing to send back (run dgsim_d2h_here.lua first)') end
end)
if not ok then p('d2h away: ERROR', err) end

local ok2, err2 = pcall(function()
  for i = 0, 9 do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if id ~= nil and id ~= kObjIDNone then
      p(string.format('d2h away: report player %d %s team %d level %d alive %s seen by team 1 %s, team 2 %s at %s', i,
        nGameObject.GetAssetName(id), nGameObject.GetTeam(id), nGameObject.GetPlayerLevel(id),
        tostring(nGameObject.IsAlive(id)), tostring(nGameObject.IsVisibleToTeam(id, 1)),
        tostring(nGameObject.IsVisibleToTeam(id, 2)), vec(nGameObject.GetPosition(id))))
    end
  end
end)
if not ok2 then p('d2h away: report ERROR', err2) end
return table.concat(out, '\n')
