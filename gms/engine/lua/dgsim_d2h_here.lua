-- D2h test fixture ([host] test_lua=<s>:dgsim_d2h_here.lua; docs/GMS_PROTOCOL.md *The enemy players' level and FoW data
-- (D2h)*): brings up to dgsim_d2h_count (default 1) live enemy players' creatures (bots standing still: run with
-- bot_brain=0) next to player 0's creature, dgsim_d2h_offset ({0, 9}) from it and 4 units apart, so player 0's team sees
-- them, and remembers where each came from (dgsim_d2h_from) for dgsim_d2h_away.lua. Then reports every player's level
-- and whether teams 1 and 2 see its creature (mVisibleToTeam, the host's vision model). Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f)', v.x, v.y) end

local ok, err = pcall(function()
  local me = nPlayer.GetPlayerControlledObjectID(0)
  if me == nil or me == kObjIDNone then
    p('d2h here: no shaper for player 0')
    return
  end
  local myTeam = nGameObject.GetTeam(me)
  local at = nGameObject.GetPosition(me)
  local off = dgsim_d2h_offset or {0.0, 9.0}
  local want = dgsim_d2h_count or 1
  dgsim_d2h_from = dgsim_d2h_from or {}
  local n = 0
  for i = 1, 9 do
    if n >= want then break end
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if id ~= nil and id ~= kObjIDNone and nGameObject.GetTeam(id) ~= myTeam and nGameObject.IsAlive(id) then
      local from = nGameObject.GetPosition(id)
      if dgsim_d2h_from[i] == nil then dgsim_d2h_from[i] = {from.x, from.y} end
      local to = nVector.create(at.x + off[1] + n * 4.0, at.y + off[2], 0)
      nLocomotion.TeleportToPositionAndFacing(id, to, nVector.create(0, -1, 0))
      p('d2h here: player', i, tostring(id), nGameObject.GetAssetName(id), 'level', nGameObject.GetPlayerLevel(id),
        'from', vec(from), 'to', vec(nGameObject.GetPosition(id)), string.format('(%.1f from player 0)',
          nVector.distance(nGameObject.GetPosition(id), at)), 'at match', string.format('%.2f',
          nGameSimulator.GetMatchTime()))
      n = n + 1
    end
  end
  if n == 0 then p('d2h here: no live enemy player') end
end)
if not ok then p('d2h here: ERROR', err) end

local ok2, err2 = pcall(function()
  for i = 0, 9 do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if id ~= nil and id ~= kObjIDNone then
      p(string.format('d2h here: report player %d %s team %d level %d alive %s seen by team 1 %s, team 2 %s at %s', i,
        nGameObject.GetAssetName(id), nGameObject.GetTeam(id), nGameObject.GetPlayerLevel(id),
        tostring(nGameObject.IsAlive(id)), tostring(nGameObject.IsVisibleToTeam(id, 1)),
        tostring(nGameObject.IsVisibleToTeam(id, 2)), vec(nGameObject.GetPosition(id))))
    end
  end
end)
if not ok2 then p('d2h here: report ERROR', err2) end
return table.concat(out, '\n')
