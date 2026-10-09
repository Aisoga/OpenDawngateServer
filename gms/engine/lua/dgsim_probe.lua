-- D1 probe, run every probe_ms while the harness ticks the server simulator: where is the minion
-- that dgsim_start.lua spawned, is it moving, does it fight; plus a few global counters.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function try(label, f, ...)
  if type(f) ~= 'function' then return 'MISSING' end
  local r = {pcall(f, ...)}
  if not r[1] then return 'ERROR ' .. tostring(r[2]) end
  return unpack(r, 2)
end
local function vec(v) if type(v) ~= 'table' and type(v) ~= 'userdata' then return tostring(v) end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

p('time: game', try('GetGameTime', nGameSimulator.GetGameTime), 'match', try('GetMatchTime', nGameSimulator.GetMatchTime),
  'objects', try('GetNumGameObjects', nObjectManager.GetNumGameObjects))
local m = dgsim_minion
-- D1e check: a 2 s slow (OnDuration | OnAgentDeath; its Tick adds the slow and waits forever, so
-- only the modifier's expiry ends it) on the lane minion once game time passes 5 s.
if m and not dgsim_slowed and (nGameSimulator.GetGameTime() or 0) >= 5 then
  dgsim_slowed = true
  local id = try('RequestModifier', nModifier.RequestModifier, m, m, nUtil.SPID('modifier_Savage_HauntedArrows_Slow'), kObjIDNone, 1,
    kZeroVector, {slowValue = 0.5})
  p('slow modifier requested on the minion ->', tostring(id))
end
if m then
  p('minion', tostring(m), 'exists', try('Exists', nGameObject.Exists, m), 'alive', try('IsAlive', nGameObject.IsAlive, m),
    'hp', try('GetHitPoints', nGameObject.GetHitPoints, m), 'pos', vec(try('GetPosition', nGameObject.GetPosition, m)),
    'moving', try('IsMoving', nGameObject.IsMoving, m), 'speed', try('GetCurrentSpeed', nGameObject.GetCurrentSpeed, m),
    'inCombat', try('IsInCombat', nGameObject.IsInCombat, m), 'aggro', try('HasTargetsOnAggroList', nAgent.HasTargetsOnAggroList, m),
    'target', tostring(try('GetBestTarget', nAgent.GetBestTarget, m)), 'dest', vec(dgsim_dest))
end
local v = dgsim_victim
if v then
  local alive = try('IsAlive', nGameObject.IsAlive, v)
  p('victim', tostring(v), 'exists', try('Exists', nGameObject.Exists, v), 'alive', alive, 'hp', try('GetHitPoints', nGameObject.GetHitPoints, v),
    'pos', vec(try('GetPosition', nGameObject.GetPosition, v)), 'tower hp', try('GetHitPoints', nGameObject.GetHitPoints, dgsim_tower),
    'tower inCombat', try('IsInCombat', nGameObject.IsInCombat, dgsim_tower), 'tower target', tostring(try('GetBestTarget', nAgent.GetBestTarget, dgsim_tower)))
  if alive == true and not dgsim_direct then
    local r1, r2 = try('RequestAbility', nAbility.RequestAbility, dgsim_attack, dgsim_tower, v, kZeroVector, 1, 0, nAbilitySlot.None, kObjIDNone, true, false, nil)
    p('tower attack requested again ->', tostring(r1), tostring(r2))
  end
end
-- events collected by dgsim_start.lua's wrappers since the last probe
if dgsim_events then
  for _, e in ipairs(dgsim_events) do p('event', e) end
  dgsim_events = {}
end
return table.concat(out, '\n')
