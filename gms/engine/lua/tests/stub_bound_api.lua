-- D6b-shims: a small stand-in for the client's bound Lua API, enough to run gms/engine/lua/dgsim_bots_api.lua
-- offline (LuaJ 2.0.3, re/scripts/bots_lua_check.py). Our own code: it only mimics what the bindings do as far as the
-- shims use them (docs/SERVER_LUA_API.md *D6b-shims*); the enum values are GlobalDefinitions' numbers.
--
-- The world: stub.add{...} creates an object and returns its id (a table standing in for light userdata). Fields:
--   name, team (0), pos {x, y, z}, npc (nNPCType, -1), player, bot, alive (true), hp/maxhp (100), minion,
--   structure, untargetable, attackable (true), visible ({[0]=true,[1]=true,[2]=true}), velocity, best (aggro target),
--   level (1), target (mTarget), deleted.
-- Like the bindings, the stub raises an error when a numeric argument isn't a number (the real ones assert and push
-- nothing), so a shim that passes the wrong type fails its test.

stub = { objects = {}, order = {}, time = 100, matchTime = 100, guardians = {}, wells = {}, wellLocked = {},
         calls = {} }
local W = stub

local function called(name) W.calls[name] = (W.calls[name] or 0) + 1 end
local function needNumber(fn, i, v)
  if type(v) ~= 'number' then error(fn .. ': argument ' .. i .. ' must be a number, got ' .. type(v), 3) end
end

-- ---- vectors (the semantics of Lua!Vector.lua: tables with a metatable) -------------------------------------------
nVector = { metaTable = {} }
function nVector.create(x, y, z) return setmetatable({ x = x, y = y, z = z }, nVector.metaTable) end
nVector.metaTable.__add = function(a, b) return nVector.create(a.x + b.x, a.y + b.y, a.z + b.z) end
nVector.metaTable.__sub = function(a, b) return nVector.create(a.x - b.x, a.y - b.y, a.z - b.z) end
nVector.metaTable.__mul = function(a, b)
  if type(a) == 'number' then a, b = b, a end
  return nVector.create(a.x * b, a.y * b, a.z * b)
end
nVector.metaTable.__unm = function(a) return nVector.create(-a.x, -a.y, -a.z) end
function nVector.dot(a, b) return a.x * b.x + a.y * b.y + a.z * b.z end
function nVector.len(a) return math.sqrt(a.x * a.x + a.y * a.y + a.z * a.z + 1e-8) end
function nVector.distance(a, b) return nVector.len(a - b) end
function nVector.normalize(a) return a * (1 / nVector.len(a)) end
function nVector.cross(a, b) return nVector.create(a.y * b.z - a.z * b.y, a.z * b.x - a.x * b.z, a.x * b.y - a.y * b.x) end
function nVector.direction(a, b) return nVector.normalize(a - b) end

kObjIDNone = setmetatable({}, { __tostring = function() return 'kObjIDNone' end })
kInvalidGUID = kObjIDNone
kZeroVector = nVector.create(0, 0, 0)
kUpVector = nVector.create(0, 0, 1)

-- ---- enums (Lua/GlobalDefinitions values) --------------------------------------------------------------------------
nTeamID = { NPC = 0, Blue = 1, Red = 2, Invalid = 255, Mortal = 1, Spirit = 2 }
nTargetType = { EnemyPlayers = 1, EnemyNPCs = 2, EnemyStructures = 4, EnemyOthers = 8, AllyPlayers = 16,
                AllyNPCs = 32, AllyStructures = 64, AllyOthers = 128, Self = 256 }
nGetObjectsFlags = { FindUntargetableObjects = 1, IgnoreNeutralUnits = 2, CheckFoWVisibility = 4 }
nGetObjectsSortType = { None = 0, Distance = 1, DistancePrioritizePlayers = 2, FurthestDistance = 3,
                        FurthestDistancePrioritizePlayers = 4 }
nNPCType = { None = -1, MeleeMinion = 0, RangedMinion = 1, SuperMinion = 2, Creep = 3, Destructible = 4,
             Interactable = 5, ObjectiveMinor = 6, ObjectiveMedium = 7, ObjectiveMajor = 8, Tower = 9, GoldMine = 10,
             PlayerPet = 11, Follower = 12, Guardian = 13, Nub = 14, Locus = 15, Ward = 16 }
nLevelTags = { MortalSide = 0, SpiritSide = 1, Base = 2, Locus = 3, Lane = 4, Jungle = 5, SpiritWell = 6,
               SpiritWellCenter = 7, GreenBuff = 8, BlueBuff = 9, OrangeBuff = 10, MoneyPigs = 11, BindingRange = 12,
               Bottom = 13, Top = 14, Parasite = 15, Invalid = 255 }
nMissionType = { None = 0, AttackTower = 1, DefendTower = 2, AttackWell = 3, DefendWell = 4, AttackBase = 5,
                 DefendBase = 6, KillParasite = 7, KillShaper = 8, AttackCore = 9, DefendCore = 10, Rally = 11,
                 HarassWell = 12, ContestParasite = 13 }
nLaneID = { None = 0, Top = 1, Bottom = 2 }
nConditionFns = { Check = 1, OnPass = 2, CheckEnd = 3 }

-- ---- the world -----------------------------------------------------------------------------------------------------
function W.add(o)
  local id = setmetatable({}, { __tostring = function() return 'id:' .. tostring(o.name) end })
  o.id = id
  o.team = o.team or 0
  o.pos = o.pos or { x = 0, y = 0, z = 0 }
  o.pos.z = o.pos.z or 0
  o.npc = o.npc or -1
  if o.alive == nil then o.alive = true end
  o.maxhp = o.maxhp or 100
  o.hp = o.hp or (o.alive and o.maxhp or 0)
  if o.attackable == nil then o.attackable = true end
  o.visible = o.visible or { [0] = true, [1] = true, [2] = true }
  o.level = o.level or 1
  W.objects[id] = o
  W.order[#W.order + 1] = id
  return id
end
function W.get(id) return W.objects[id] end
function W.kill(id) local o = W.objects[id]; o.alive = false; o.hp = 0 end
function W.reset()
  W.objects, W.order, W.guardians, W.wells, W.wellLocked, W.calls = {}, {}, {}, {}, {}, {}
  W.time, W.matchTime = 100, 100
end
local function live(id)
  local o = id ~= nil and W.objects[id]
  if o and not o.deleted then return o end
  return nil
end
local function d3(a, b)
  local dx, dy, dz = a.x - b.x, a.y - b.y, (a.z or 0) - (b.z or 0)
  return math.sqrt(dx * dx + dy * dy + dz * dz)
end

-- ---- nObjectManager ------------------------------------------------------------------------------------------------
nObjectManager = {}
function nObjectManager.IsValidObject(id) return live(id) ~= nil end
-- the next live object at or after slot i, kObjIDNone past the last one
function nObjectManager.GetObjectIdByIndex(i)
  called('GetObjectIdByIndex')
  needNumber('GetObjectIdByIndex', 1, i)
  for k = i + 1, #W.order do
    if live(W.order[k]) then return W.order[k] end
  end
  return kObjIDNone
end
-- (position, radius, sortType, targetTypes, team, ignoreModifier, self, descriptors, flags); dead objects never come
-- back (GameObjects_FilterAndSortQuery's IsDead test)
function nObjectManager.GetObjectsInRadius(pos, radius, sort, types, team, ignore, self, descriptors, flags)
  called('GetObjectsInRadius')
  needNumber('GetObjectsInRadius', 2, radius)
  needNumber('GetObjectsInRadius', 3, sort)
  needNumber('GetObjectsInRadius', 4, types)
  needNumber('GetObjectsInRadius', 5, team)
  needNumber('GetObjectsInRadius', 9, flags)
  if type(pos) ~= 'table' then error('GetObjectsInRadius: position must be a vector', 2) end
  local hits = {}
  for _, id in ipairs(W.order) do
    local o = live(id)
    if o and o.alive then
      local category = 8
      if o.player then category = 1 elseif o.structure then category = 4 elseif o.npc ~= -1 then category = 2 end
      local bit
      if id == self then
        bit = 256
      elseif o.team == team then
        bit = category * 16
      else
        bit = category
      end
      local ok = math.floor(types / bit) % 2 == 1
      if ok and o.untargetable and math.floor(flags / 1) % 2 == 0 then ok = false end
      if ok and o.team == 0 and math.floor(flags / 2) % 2 == 1 then ok = false end
      if ok and math.floor(flags / 4) % 2 == 1 and not o.visible[team] then ok = false end
      local d = d3(o.pos, pos)
      if ok and d <= radius then hits[#hits + 1] = { id = id, d = d } end
    end
  end
  if sort == 1 then table.sort(hits, function(a, b) return a.d < b.d end) end
  local out = {}
  for i, h in ipairs(hits) do out[i] = h.id end
  return out
end

-- ---- nGameObject ---------------------------------------------------------------------------------------------------
nGameObject = {}
function nGameObject.GetPosition(id)
  local o = live(id)
  if not o then return nVector.create(0, 0, 0) end
  return nVector.create(o.pos.x, o.pos.y, o.pos.z)
end
function nGameObject.GetTeam(id) local o = live(id); return o and o.team or 255 end
function nGameObject.GetNPCType(id) local o = live(id); return o and o.npc or -1 end
function nGameObject.GetAssetName(id) local o = live(id); return o and o.name end
function nGameObject.IsAlive(id) local o = live(id); return o ~= nil and o.alive == true end
function nGameObject.GetHitPoints(id) local o = live(id); return o and o.hp or 0 end
function nGameObject.GetMaxHitPoints(id) local o = live(id); return o and o.maxhp or 0 end
function nGameObject.IsMinion(id) local o = live(id); return o ~= nil and o.minion == true end
function nGameObject.GetPlayerLevel(id) local o = live(id); return o and o.level or 0 end
function nGameObject.GetTargetID(id) local o = live(id); return o and o.target or kObjIDNone end
function nGameObject.GetLinearVelocity(id)
  local o = live(id)
  if not o then error('Could not find object!') end
  local v = o.velocity or { x = 0, y = 0, z = 0 }
  return nVector.create(v.x, v.y, v.z or 0)
end
function nGameObject.IsVisibleToTeam(id, team)
  needNumber('IsVisibleToTeam', 2, team)
  local o = live(id)
  return o ~= nil and o.visible[team] == true
end
-- GameObject_ValidateHostileTarget: targetable, attackable, HP > 0 unless flag, another team
function nGameObject.ValidateHostileTarget(team, id, flag)
  called('ValidateHostileTarget')
  needNumber('ValidateHostileTarget', 1, team)
  local o = live(id)
  if not o or o.untargetable or not o.attackable then return false end
  if not flag and o.hp <= 0 then return false end
  return o.team ~= team
end

-- ---- nPlayer, nAgent, nLevel, nGameSimulator, nUtil ----------------------------------------------------------------
nPlayer = {}
function nPlayer.GetPlayerControlledObjects()
  local out = {}
  for _, id in ipairs(W.order) do
    local o = live(id)
    if o and o.player then out[#out + 1] = id end
  end
  return out
end
function nPlayer.IsBot(id) local o = live(id); return o ~= nil and o.bot == true end
function nPlayer.IsPlayerControlledObject(id) local o = live(id); return o ~= nil and o.player == true end

nAgent = {}
function nAgent.GetBestTarget(id) local o = live(id); return o and o.best or kObjIDNone end

-- The stub's tag map: the Loci within 30 of (-230, -230) (Mortal side) and (230, 230); the sides split by x + y; the
-- lanes Top (y - x > 20) and Bottom (x - y > 20) outside the bases (60 around (-200, -200) / (200, 200)).
nLevel = {}
function nLevel.ObjectPositionMatchesLevelTag(id, tag)
  needNumber('ObjectPositionMatchesLevelTag', 2, tag)
  local o = live(id)
  if not o then return false end
  local p = o.pos
  local inBase = d3(p, { x = -200, y = -200, z = 0 }) < 60 or d3(p, { x = 200, y = 200, z = 0 }) < 60
  if tag == nLevelTags.Locus then
    return d3(p, { x = -230, y = -230, z = 0 }) < 30 or d3(p, { x = 230, y = 230, z = 0 }) < 30
  elseif tag == nLevelTags.MortalSide then
    return p.x + p.y < 0
  elseif tag == nLevelTags.SpiritSide then
    return p.x + p.y >= 0
  elseif tag == nLevelTags.Top then
    return not inBase and p.y - p.x > 20
  elseif tag == nLevelTags.Bottom then
    return not inBase and p.x - p.y > 20
  end
  return false
end

nGameSimulator = {}
function nGameSimulator.GetGameTime() return W.time end
function nGameSimulator.GetMatchTime() return W.matchTime end

nUtil = {}
function nUtil.SPID(name) return 'spid:' .. string.lower(name) end

nBattlegrounds = {}
function nBattlegrounds.GetGuardianOnTeam(team) return W.guardians[team] or kObjIDNone end
-- the UI-data bindings: the nearest well's index (0-based) and its lock
function nBattlegrounds.SpiritWell_GetClosest(pos)
  local best, bestD = -1, nil
  for i, id in ipairs(W.wells) do
    local d = d3(W.objects[id].pos, pos)
    if bestD == nil or d < bestD then best, bestD = i - 1, d end
  end
  return best
end
function nBattlegrounds.SpiritWell_IsLocked(i) return W.wellLocked[i] == true end

return stub
