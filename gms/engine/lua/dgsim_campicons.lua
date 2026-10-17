-- CAMPICONS live probe ([host] test_lua=<s>:dgsim_campicons.lua at several times): each
-- call runs the next step of dgsim_campicons_steps (default below) and reports the camps' creatures and whether teams
-- 1 and 2 see them. The host's "director: camp knowledge" lines (camp_icons=3) are the result; this probe only moves
-- player 0's shaper and kills camps:
--   {'tp', x, y}                 teleport player 0's creature to (x, y)
--   {'kill', prefix, x, y}       kill every living creature whose asset name starts with prefix within 12 units of
--                                (x, y), each with one lethal true-damage hit from player 0's snapshot (the damage path,
--                                as a player's kill)
--   {'kill', prefix, x, y, pid}  the same with player pid's snapshot (its creature is the killer: e.g. a team-2 bot's
--                                clear that team 1 doesn't see)
--   {'report', prefix, x, y}     report only
--   {'vision', team, x, y, r, s} a vision object of `team` at (x, y), radius r, for s seconds (a team's witness)
--   {'tpo', x, y}                teleport the other human player (a second client) to (x, y)
-- (a test may set its own steps in dgsim_campicons_steps before the first call)
-- The defaults (a practice match, bots=0, director_waves=0): the human next to the CreepsFish camp at (65.7, -132.2)
-- at 95 s, kills it in sight at 100 s (a witnessed clear), kills the CreepsFungus camp at (-67.4, 3.3) and the
-- Buff_Fish camp at (-67.6, 165.9) unseen at 104 and 106 s, stands at the Fungus camp 7 s before its 60-s respawn
-- (157 s: the small camps' 10-s reveal window), goes home (170 s) and stands at the Buff_Fish camp 56 s before its
-- 300-s respawn (350 s: the buff camps' 60-s window).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local steps = dgsim_campicons_steps or {
  {'tp', 60.0, -126.0},
  {'kill', 'Creep_Jungle_Fish_', 65.7, -132.2},
  {'kill', 'Creep_Jungle_Fungus_', -67.4, 3.3},
  {'kill', 'Creep_Jungle_Fish_', -67.6, 165.9},
  {'tp', -60.0, 3.0},
  {'tp', 60.0, -126.0},
  {'tp', -60.0, 160.0},
  {'report', 'Creep_Jungle_Fish_', -67.6, 165.9},
}

local function creatures(prefix, cx, cy)
  local list, seen = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      local pos = nGameObject.GetPosition(id)
      if string.find(name, '^' .. prefix) and pos and math.abs(pos.x - cx) < 12 and math.abs(pos.y - cy) < 12 then
        list[#list + 1] = id
      end
    end
  end
  return list
end

local ok, err = pcall(function()
  dgsim_campicons_n = (dgsim_campicons_n or 0) + 1
  local n = dgsim_campicons_n
  local s = steps[n]
  local t = nGameSimulator.GetMatchTime()
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if not s then
    p('campicons step', n, 'at match', f2(t), ': no step')
    return
  end
  if h == nil or h == kObjIDNone then
    p('campicons step', n, ': no shaper for player 0')
    return
  end
  if s[1] == 'tp' then
    nLocomotion.TeleportToPositionAndFacing(h, nVector.create(s[2], s[3], 0), nVector.create(0, -1, 0))
    local at = nGameObject.GetPosition(h)
    p(string.format('campicons step %d at match %s: player 0 %s teleported to (%.1f %.1f)', n, f2(t), tostring(h), at.x,
      at.y))
    return
  end
  if s[1] == 'tpo' then
    -- {'tpo', x, y}: teleport the other human player (not player 0, not a bot) to (x, y)
    local moved = 0
    for _, pid in ipairs(nPlayer.GetPlayerIds()) do
      local o = nPlayer.GetPlayerControlledObjectID(pid)
      if pid ~= 0 and o ~= nil and o ~= kObjIDNone and not nPlayer.IsBot(o) then
        nLocomotion.TeleportToPositionAndFacing(o, nVector.create(s[2], s[3], 0), nVector.create(0, -1, 0))
        local at = nGameObject.GetPosition(o)
        p(string.format('campicons step %d at match %s: player %s %s teleported to (%.1f %.1f)', n, f2(t), tostring(pid),
          tostring(o), at.x, at.y))
        moved = moved + 1
      end
    end
    if moved == 0 then p(string.format('campicons step %d at match %s: no other human player', n, f2(t))) end
    return
  end
  if s[1] == 'vision' then
    -- {'vision', team, x, y, radius, seconds}: a vision object of that team (CAMPICONS step 2: a witness for team 2
    -- in a match without team-2 players)
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(h, kObjIDNone)
    local v = nFogOfWar.CreateVisionObjectWithDuration(h, snap, s[5], s[6], nVector.create(s[3], s[4], 0), s[2])
    p(string.format('campicons step %d at match %s: vision object %s for team %d at (%.1f %.1f), radius %.1f, %.0f s', n,
      f2(t), tostring(v), s[2], s[3], s[4], s[5], s[6]))
    return
  end
  local list = creatures(s[2], s[3], s[4])
  local killed = 0
  for _, id in ipairs(list) do
    local alive = nGameObject.IsAlive(id)
    local line = string.format('campicons step %d at match %s: %s %s alive %s hp %s, seen by team 1 %s, team 2 %s', n,
      f2(t), tostring(id), tostring(nGameObject.GetAssetName(id)), tostring(alive), f2(nGameObject.GetHitPoints(id)),
      tostring(nGameObject.IsVisibleToTeam(id, 1)), tostring(nGameObject.IsVisibleToTeam(id, 2)))
    if s[1] == 'kill' and alive then
      local by = h
      if s[5] ~= nil then by = nPlayer.GetPlayerControlledObjectID(s[5]) end
      local snap = nAttribute.CreateAttributeSnapshot_FromObject(by, kObjIDNone)
      local r, dealt = nGameObject.TakeDamage(snap, id, nGameObject.GetHitPoints(id) + 1000, 0,
        nDescriptors.IsTrueDamage, kZeroVector)
      nAttribute.RemoveAttributeSnapshot(snap)
      line = line .. string.format('; hit %s dealt %s alive %s', tostring(r), f2(dealt), tostring(nGameObject.IsAlive(id)))
      killed = killed + 1
    end
    p(line)
  end
  p(string.format('campicons step %d at match %s: %s %s near (%.1f %.1f): %d creature(s), %d killed%s', n, f2(t), s[1],
    s[2], s[3], s[4], #list, killed, s[5] ~= nil and (' by player ' .. tostring(s[5])) or ''))
end)
if not ok then p('campicons: ERROR', err) end
return table.concat(out, '\n')
