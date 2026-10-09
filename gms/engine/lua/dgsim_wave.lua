-- D2g-live test fixture ([host] test_wave, gms/engine/gms_host.cpp SpawnWave): a small wave of server minions spawned
-- at run time and sent toward an enemy Binding, so the match host has runtime objects to replicate: creates, movement
-- on the network-rate timers, the Binding's projectiles and hits, deaths and deletes. D1a-live's lua_probes minions die
-- before the client is in the game, and D1c-live's director waves come later.
-- Run once on the server simulator's lua_State (the master state, the server simulator bound), with these globals set
-- in front of the chunk by the host:
--   dgsim_wave_count    the number of minions (3)
--   dgsim_wave_noun     their noun ('Minion_Mortal_Melee.Noun')
--   dgsim_wave_team     their team, an nTeamID name ('Mortal')
--   dgsim_wave_from     the Binding they start beside, by asset name; of several, the one with x < 0 (the top lane)
--   dgsim_wave_to       the Binding they walk to, the same way
--   dgsim_wave_start    how far from `from` toward `to` they spawn (12 units), 3 units apart across the lane
--   dgsim_wave_stop     how far before `to` they stop (8 units: inside the Binding's attack range)
-- Returns a report; its line "wave ids: <id> <id> ..." lists the minions' server ids (tostring), which the host
-- watches (a dead one is marked for delete a little later).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

-- nObjectManager.GetObjectIdByIndex(i) (0x009361A0) is the object manager's iterator from pool slot i (the next live
-- object at or after it), not an index into the live objects: once objects were deleted, slots above
-- GetNumGameObjects() are still in use (d2g_run1 missed Binding_Spirit_Outer in slot 0x150 with 334 objects). So walk
-- every slot of the server pool (0x400).
local function find(name)
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id ~= nil and id ~= kObjIDNone and nGameObject.GetAssetName(id) == name then
      local pos = nGameObject.GetPosition(id)
      if pos and pos.x < 0 then return id, pos end
    end
  end
end

local ok, err = pcall(function()
  local count = dgsim_wave_count or 3
  local fromId, from = find(dgsim_wave_from)
  local toId, to = find(dgsim_wave_to)
  if not fromId or not toId then
    p('wave: Binding not found:', dgsim_wave_from, tostring(fromId), dgsim_wave_to, tostring(toId))
    return
  end
  local dx, dy = to.x - from.x, to.y - from.y
  local len = math.sqrt(dx * dx + dy * dy)
  local ux, uy = dx / len, dy / len
  local start, stop = dgsim_wave_start or 12, dgsim_wave_stop or 8
  local noun = nUtil.GetAsset(dgsim_wave_noun)
  local team = nTeamID[dgsim_wave_team]
  local dest = nVector.create(to.x - ux * stop, to.y - uy * stop, to.z)
  p('wave:', count, 'x', dgsim_wave_noun, 'team', dgsim_wave_team, tostring(team), 'from', dgsim_wave_from,
    tostring(fromId), vec(from), 'to', dgsim_wave_to, tostring(toId), vec(to), 'distance', string.format('%.1f', len),
    'destination', vec(dest))
  local ids = {}
  for k = 1, count do
    local off = (k - (count + 1) / 2) * 3
    local pos = nVector.create(from.x + ux * start - uy * off, from.y + uy * start + ux * off, from.z)
    local m = nObjectManager.CreateObject(noun, team, pos, kZeroVector, 1)
    if m then
      nLocomotion.MoveToPointExact(m, dest, false)
      ids[#ids + 1] = tostring(m)
      p('minion', tostring(m), 'at', vec(pos), 'hp', nGameObject.GetHitPoints(m), 'ordered to', vec(dest))
    else
      p('minion', k, 'NOT created at', vec(pos))
    end
  end
  dgsim_wave_ids = ids
  p('wave ids: ' .. table.concat(ids, ' '))
end)
if not ok then p('wave: ERROR', err) end
return table.concat(out, '\n')
