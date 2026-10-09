-- D6c test probe ([host] test_lua=<s>:dgsim_d6c_trace.lua, repeated): traces what D6c looks at, on the server's Lua.
-- The first run installs wrappers (kept in the global dgsim_d6c); every run returns the events since the last one
-- (at most 60 lines) and the counts so far (logged by the host).
--   - a projectile hit (nProjectile.OnProjectileHit) or end (DestroyProjectileObject) whose caster ability is no longer
--     running, the case of the scripts' "Attemping to call OnHitTarget/OnProjectileEnd, but the ability has already
--     been destroyed" asserts: the ability's name, its instance id (the host's "ability early end" lines name the same
--     id and who ended it), the caster and whether it is alive;
--   - ability_Artist_W_Shred's Tick at its start: the Artist's blackboard wHolder and wObject and whether each is a live
--     object (the "Could not find object!" error comes from GetPosition on one of them);
--   - ability_Bot_AvoidFireNP's Tick (a bot dodging fire): the bot, where it stood, where it went;
--   - nDirector.NotifyFireTrackerOfAbility (the Guardian's and the parasite's area abilities): caster, cast type;
--   - ability_Bot_GroupInLane's Tick: starts and returns per bot (the fixed copy from dgsim_bots_api.lua).
local D = dgsim_d6c
local fresh = D == nil
if fresh then
  D = { events = {}, counts = {}, lost = 0 }
  dgsim_d6c = D
end
local function now()
  local ok, t = pcall(nGameSimulator.GetMatchTime)
  return ok and t or -1
end
local function count(k) D.counts[k] = (D.counts[k] or 0) + 1 end
local function ev(s)
  if #D.events < 400 then D.events[#D.events + 1] = string.format('%.2f ', now()) .. s else D.lost = D.lost + 1 end
end
local function valid(id)
  if id == nil or id == kObjIDNone then return 'none' end
  local ok, v = pcall(nObjectManager.IsValidObject, id)
  return (ok and v) and 'live' or 'GONE'
end
local function name(o)
  local ok, n = pcall(nGameObject.GetAssetName, o)
  return ok and tostring(n) or '?'
end
local function pos(o)
  local ok, p = pcall(nGameObject.GetPosition, o)
  if ok and p then return string.format('(%.1f, %.1f)', p.x, p.y) end
  return '(?)'
end
local function alive(o)
  local ok, a = pcall(nGameObject.IsAlive, o)
  return ok and tostring(a) or '?'
end
local function abilityName(guid)
  local ok, n = pcall(nAbility.GetAbilityName, guid)
  return ok and tostring(n) or '?'
end
local function running(id)
  local ok, r = pcall(nAbility.IsAbilityRunning, id)
  return ok and r
end

local installed = {}
if fresh then
  -- projectiles whose caster ability is gone (Lua/ProjectileUtils.lua)
  local hit, finish = nProjectile.OnProjectileHit, nProjectile.DestroyProjectileObject
  nProjectile.OnProjectileHit = function(p, target, at, dir)
    if p and p.abilityNamespace ~= nil and p.abilityNamespace[nAbilityFns.OnHitTarget] ~= nil and
        not running(p.casterAbility) then
      count('hit after the caster ability ended')
      ev('projectile hit, caster ability gone: ' .. abilityName(p.casterAbilityGUID) .. ' inst ' ..
         tostring(p.casterAbility) .. ', caster ' .. tostring(p.myObjectID) .. ' ' .. name(p.myObjectID) .. ' alive ' ..
         alive(p.myObjectID) .. ', target ' .. tostring(target))
    end
    return hit(p, target, at, dir)
  end
  nProjectile.DestroyProjectileObject = function(p)
    if p and p.abilityNamespace ~= nil and p.abilityNamespace[nAbilityFns.OnProjectileEnd] ~= nil and
        not running(p.casterAbility) then
      count('end after the caster ability ended')
      ev('projectile end, caster ability gone: ' .. abilityName(p.casterAbilityGUID) .. ' inst ' ..
         tostring(p.casterAbility) .. ', caster ' .. tostring(p.myObjectID) .. ' alive ' .. alive(p.myObjectID))
    end
    return finish(p)
  end
  installed[#installed + 1] = 'projectiles'

  -- ability_Artist_W_Shred's Tick
  local shred = ability_Artist_W_Shred
  if type(shred) == 'table' and type(shred[nAbilityFns.Tick]) == 'function' then
    local tick = shred[nAbilityFns.Tick]
    shred[nAbilityFns.Tick] = function(p, ...)
      local okH, holder = pcall(nAgent.GetBlackboardData_GUID, p.myObjectID, shred.blackboardGUID)
      local okO, orb = pcall(nAgent.GetBlackboardData_GUID, p.myObjectID, shred.objectBlackboard)
      count('W_Shred Tick')
      local h, o = okH and holder or nil, okO and orb or nil
      local bad = (h ~= p.myTargetID) and (o == nil or o == kObjIDNone or valid(o) ~= 'live') and valid(h) ~= 'live'
      local badOrb = (h ~= p.myTargetID) and o ~= nil and o ~= kObjIDNone and valid(o) ~= 'live'
      if bad or badOrb then count('W_Shred Tick that will fail') end
      ev('W_Shred Tick: artist ' .. tostring(p.myObjectID) .. ' alive ' .. alive(p.myObjectID) .. ', target ' ..
         tostring(p.myTargetID) .. ', wHolder ' .. tostring(h) .. ' ' .. valid(h) .. ', wObject ' .. tostring(o) .. ' ' ..
         valid(o) .. ((bad or badOrb) and ' -> GetPosition on a gone object' or ''))
      return tick(p, ...)
    end
    installed[#installed + 1] = 'W_Shred'
  end

  -- ability_Bot_AvoidFireNP's Tick
  local avoid = ability_Bot_AvoidFireNP
  if type(avoid) == 'table' and type(avoid[nAbilityFns.Tick]) == 'function' then
    local tick = avoid[nAbilityFns.Tick]
    avoid[nAbilityFns.Tick] = function(p, a, b)
      count('AvoidFireNP')
      local from = pos(p.myObjectID)
      ev('AvoidFireNP: bot ' .. tostring(p.myObjectID) .. ' ' .. name(p.myObjectID) .. ' at ' .. from .. ' starts')
      tick(p, a, b)
      ev('AvoidFireNP: bot ' .. tostring(p.myObjectID) .. ' moved ' .. from .. ' -> ' .. pos(p.myObjectID))
    end
    installed[#installed + 1] = 'AvoidFireNP'
  end

  -- the fire tracker's input
  if type(nDirector) == 'table' and type(nDirector.NotifyFireTrackerOfAbility) == 'function' then
    local notify = nDirector.NotifyFireTrackerOfAbility
    nDirector.NotifyFireTrackerOfAbility = function(caster, ability, castType, ...)
      count('NotifyFireTrackerOfAbility')
      ev('NotifyFireTrackerOfAbility: ' .. abilityName(ability) .. ' by ' .. tostring(caster) .. ' ' .. name(caster) ..
         ', cast type ' .. tostring(castType))
      return notify(caster, ability, castType, ...)
    end
    installed[#installed + 1] = 'fire tracker'
  end

  -- ability_Bot_GroupInLane's Tick (whatever is in the table now: dgsim_bots_api.lua's fixed copy)
  local group = ability_Bot_GroupInLane
  if type(group) == 'table' and type(group[nAbilityFns.Tick]) == 'function' then
    local tick = group[nAbilityFns.Tick]
    group[nAbilityFns.Tick] = function(p, ...)
      count('GroupInLane Tick')
      local okL, lane = pcall(nBot.GetLaneID, p.myObjectID)
      local before = okL and tostring(lane) or '?'
      tick(p, ...)
      local okA, after = pcall(nBot.GetLaneID, p.myObjectID)
      count('GroupInLane Tick returned')
      ev('GroupInLane: bot ' .. tostring(p.myObjectID) .. ' ' .. name(p.myObjectID) .. ' lane ' .. before .. ' -> ' ..
         (okA and tostring(after) or '?'))
    end
    installed[#installed + 1] = 'GroupInLane'
  end
end

local out = {}
if fresh then out[#out + 1] = 'd6c trace installed: ' .. table.concat(installed, ', ') end
local keys = {}
for k in pairs(D.counts) do keys[#keys + 1] = k end
table.sort(keys)
local cs = {}
for _, k in ipairs(keys) do cs[#cs + 1] = k .. ' ' .. tostring(D.counts[k]) end
out[#out + 1] = 'd6c trace at match ' .. string.format('%.1f', now()) .. ' s: ' ..
                (#cs > 0 and table.concat(cs, ', ') or 'nothing yet') .. (D.lost > 0 and (', lost ' .. D.lost) or '')
local n = 0
for _, e in ipairs(D.events) do
  n = n + 1
  if n <= 60 then out[#out + 1] = '  ' .. e end
end
if n > 60 then out[#out + 1] = '  ... ' .. (n - 60) .. ' more' end
D.events = {}
return table.concat(out, '\n')
