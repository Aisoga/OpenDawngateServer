-- D9b test probe ([host] test_lua=<s>:dgsim_d9b_hit_worker.lua; docs/ASSERTS.md *D9b*): an enemy hits one living
-- spirit-well worker (Titan_Node_Follower) of player 0's team for a little true damage (dgsim_hit_worker_damage, 5)
-- through the host's damage core, so the human's client runs its own CombatEvents!CombatEvent_DamageAbsorb.lua on the
-- damage CombatEvent and shows MineUnderAttack about nGameObject.GetOwnerID(worker), which since D9b is the worker's
-- well: the announcer's "well under attack" line and its minimap ping at the well (cooldown 12 s per well, so run it
-- at most every 12 s per well). The attacker is a snapshot of an enemy player's creature (players 5-9 first, so the
-- client knows the event's source), else a helper minion of the other team marked for delete at once. Each run picks
-- the next worker (dgsim_hit_worker_next). Returns a report (logged by the host): the worker, its owner, the attacker.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local shaper = nPlayer.GetPlayerControlledObjectID(0)
  if shaper == nil or shaper == kObjIDNone then
    p('hit worker: no shaper for player 0')
    return
  end
  local team = nGameObject.GetTeam(shaper)
  local enemyName = team == nTeamID.Mortal and 'Spirit' or 'Mortal'
  -- the workers of player 0's team, in pool order
  local workers = {}
  local seen = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      if string.find(name, '^Titan_Node_Follower') and nGameObject.IsAlive(id) and nGameObject.GetTeam(id) == team then
        workers[#workers + 1] = id
      end
    end
  end
  if #workers == 0 then
    p('hit worker: no living worker of team', team, 'at match time', nGameSimulator.GetMatchTime())
    return
  end
  dgsim_hit_worker_next = ((dgsim_hit_worker_next or 0) % #workers) + 1
  local w = workers[dgsim_hit_worker_next]
  local wp = nGameObject.GetPosition(w)
  -- the attacker: an enemy player's creature, else a helper minion
  local attacker, helper = kObjIDNone, false
  for idx = 0, 9 do
    local c = nPlayer.GetPlayerControlledObjectID(idx)
    if c ~= nil and c ~= kObjIDNone and nGameObject.GetTeam(c) ~= team and nGameObject.IsAlive(c) then
      attacker = c
      break
    end
  end
  if attacker == kObjIDNone then
    attacker = nObjectManager.CreateObject(nUtil.GetAsset('Minion_' .. enemyName .. '_Melee.Noun'), nTeamID[enemyName],
      nVector.create(wp.x + 6, wp.y + 6, 0), kZeroVector, 1)
    helper = true
  end
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(attacker, kObjIDNone)
  local hp0 = nGameObject.GetHitPoints(w)
  local amount = dgsim_hit_worker_damage or 5
  local s, d = nGameObject.TakeDamage(snap, w, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  if helper then nGameObject.MarkForDelete(attacker) end
  local owner = nGameObject.GetOwnerID(w)
  local ownerText = '(none: the client announces about object 0)'
  if owner ~= nil and owner ~= kObjIDNone and nGameObject.Exists(owner) then
    ownerText = '(' .. tostring(nGameObject.GetAssetName(owner)) .. ', team ' .. tostring(nGameObject.GetTeam(owner)) .. ')'
  end
  p('hit worker:', tostring(w), 'of team', team, 'at', string.format('(%.1f %.1f)', wp.x, wp.y), 'owner', tostring(owner),
    ownerText,
    'hit', amount, 'by', tostring(attacker), nGameObject.GetAssetName(attacker) or '?', helper and '(helper)' or '',
    '->', tostring(s), tostring(d), 'hp', tostring(hp0), '->', tostring(nGameObject.GetHitPoints(w)), 'at match time',
    nGameSimulator.GetMatchTime(), '; workers of the team', #workers)
end)
if not ok then p('hit worker: ERROR', err) end
return table.concat(out, '\n')
