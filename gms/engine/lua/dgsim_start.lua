-- D1 probe, run once by the harness right after StartLevel, on the master lua_State with the
-- server simulator bound (gms/engine/experiment.cpp, RunLuaFile). Returns a report string.
-- It inventories the level's server objects through the bound API, then spawns one lane minion
-- and orders it down its lane, so dgsim_probe.lua can watch whether server-side locomotion,
-- combat and AI run.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function try(label, f, ...)
  if type(f) ~= 'function' then p(label, 'MISSING') return nil end
  local r = {pcall(f, ...)}
  if not r[1] then p(label, 'ERROR', r[2]) return nil end
  return unpack(r, 2)
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

-- D1a-live: trace the projectile hit path (Lua/ProjectileUtils.lua) and TakeDamage. The wrappers
-- replace the globals on the master state, which every ability thread looks up at call time; the
-- events are collected in dgsim_events and printed (and cleared) by dgsim_probe.lua.
dgsim_events = dgsim_events or {}
local function ev(...)
  if #dgsim_events < 200 then
    local t = {}
    for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
    dgsim_events[#dgsim_events + 1] = string.format('%.2f ', nGameSimulator.GetGameTime()) .. table.concat(t, ' ')
  end
end
if type(nProjectile) == 'table' and not dgsim_wrapped then
  dgsim_wrapped = true
  local collisions, hit, takeDamage = nProjectile.ProcessProjectileCollisions, nProjectile.OnProjectileHit, nGameObject.TakeDamage
  nProjectile.ProcessProjectileCollisions = function(params, hitObject, position, facing)
    local r = collisions(params, hitObject, position, facing)
    ev('projectile collisions: hit object', hitObject, 'radius', params.radius, 'damage', params.damage, '-> hit', r)
    return r
  end
  nProjectile.OnProjectileHit = function(params, target, position, direction)
    ev('projectile hit', target, 'damage', params.damage, 'coefficient', params.damageCoefficient, 'snapshot', params.snapshot,
      'hp before', nGameObject.GetHitPoints(target))
    return hit(params, target, position, direction)
  end
  nGameObject.TakeDamage = function(snapshot, target, damage, coefficient, descriptors, facing)
    local a, b = takeDamage(snapshot, target, damage, coefficient, descriptors, facing)
    ev('TakeDamage', target, damage, coefficient, descriptors, '->', a, b, 'hp now', nGameObject.GetHitPoints(target))
    return a, b
  end
end
p('projectile trace wrappers', dgsim_wrapped and 'installed' or 'NOT installed (nProjectile ' .. type(nProjectile) .. ')')

p('namespaces: nBattlegrounds', type(nBattlegrounds), 'nBot', type(nBot), 'nDirector', type(nDirector),
  'nGameModeHandlers', type(nGameModeHandlers), 'kObjIDNone', tostring(kObjIDNone))
p('time: game', try('GetGameTime', nGameSimulator.GetGameTime), 'match', try('GetMatchTime', nGameSimulator.GetMatchTime))

local n = try('GetNumGameObjects', nObjectManager.GetNumGameObjects) or 0
local objs = {}
for i = 0, n do
  local id = try('GetObjectIdByIndex', nObjectManager.GetObjectIdByIndex, i)
  if id ~= nil and id ~= kObjIDNone then
    local name = try('GetAssetName', nGameObject.GetAssetName, id) or '?'
    objs[#objs + 1] = {id = id, name = name}
  end
end
p('objects via Lua:', n, 'listed', #objs)

local function find(pattern)
  for _, o in ipairs(objs) do if string.find(o.name, pattern) then return o.id, o.name end end
end

for _, o in ipairs(objs) do
  if string.find(o.name, '^Binding_') or string.find(o.name, '^nub_') or string.find(o.name, '^titan_node') or string.find(o.name, 'Guardian') then
    p('structure', o.name, tostring(o.id), 'team', try('GetTeam', nGameObject.GetTeam, o.id), 'npctype', try('GetNPCType', nGameObject.GetNPCType, o.id),
      'hp', try('GetHitPoints', nGameObject.GetHitPoints, o.id), '/', try('GetMaxHitPoints', nGameObject.GetMaxHitPoints, o.id),
      'alive', try('IsAlive', nGameObject.IsAlive, o.id), 'pos', vec(try('GetPosition', nGameObject.GetPosition, o.id)))
  end
end
p('stub GetGuardianOnTeam(Mortal) =', tostring(try('GetGuardianOnTeam', nBattlegrounds.GetGuardianOnTeam, nTeamID.Mortal)))

local spawnId, spawnName = find('SpawnPoint_v5_Mortal_Top')
local targetId, targetName = find('SpawnPoint_v5_Spirit_Top')
p('spawn point', spawnName, tostring(spawnId), vec(spawnId and try('GetPosition', nGameObject.GetPosition, spawnId)),
  'target', targetName, vec(targetId and try('GetPosition', nGameObject.GetPosition, targetId)))
if spawnId then
  local noun = try('GetAsset', nUtil.GetAsset, 'Minion_Mortal_Melee.Noun')
  local pos = try('GetPosition', nGameObject.GetPosition, spawnId)
  local m = try('CreateObject', nObjectManager.CreateObject, noun, nTeamID.Mortal, pos, kZeroVector, 1)
  dgsim_minion = m
  p('minion', tostring(m), 'name', m and try('GetAssetName', nGameObject.GetAssetName, m), 'alive', m and try('IsAlive', nGameObject.IsAlive, m),
    'hp', m and try('GetHitPoints', nGameObject.GetHitPoints, m), 'isMinion', m and try('IsMinion', nGameObject.IsMinion, m))
  if m then
    local d = try('GetMinionDestination', nDirector.GetMinionDestination, m)
    p('director: minion destination', vec(d))
    local dest = d or (targetId and try('GetPosition', nGameObject.GetPosition, targetId))
    if dest then
      try('MoveToPointExact', nLocomotion.MoveToPointExact, m, dest, false)
      dgsim_dest = dest
      p('ordered to', vec(dest))
    end
  end
end

-- Combat test: a Mortal melee minion right next to the Spirit outer tower of the top lane, and the
-- tower asked (as its AI would) to use its attack ability on it. The ability script waits on a
-- Lua thread, spawns a homing projectile and deals 38 % of a melee minion's max HP per hit.
local towerId
for _, o in ipairs(objs) do
  if o.name == 'Binding_Spirit_Outer' then
    local tp = try('GetPosition', nGameObject.GetPosition, o.id)
    if tp and tp.x < 0 then towerId = o.id end
  end
end
if towerId and spawnId then
  local tp = nGameObject.GetPosition(towerId)
  local noun = nUtil.GetAsset('Minion_Mortal_Melee.Noun')
  local v = try('CreateObject', nObjectManager.CreateObject, noun, nTeamID.Mortal, nVector.create(tp.x + 6, tp.y - 10, tp.z), kZeroVector, 1)
  dgsim_victim, dgsim_tower = v, towerId
  dgsim_attack = nUtil.SPID('ability_Binding_Attack_Outer')
  p('victim', tostring(v), 'hp', v and try('GetHitPoints', nGameObject.GetHitPoints, v), 'tower', tostring(towerId), 'at', vec(tp))
  if v and not dgsim_direct then
    local r1, r2 = try('RequestAbility', nAbility.RequestAbility, dgsim_attack, towerId, v, kZeroVector, 1, 0, nAbilitySlot.None, kObjIDNone, true, false, nil)
    p('tower attack requested ->', tostring(r1), tostring(r2))
  end
end
return table.concat(out, '\n')
