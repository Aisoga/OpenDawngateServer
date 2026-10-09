-- D6b-shims: the bots' missing server Lua API as Lua 5.1 over the bound API (docs/SERVER_LUA_API.md tier 3,
-- docs/AGENT_AI.md section 11). Defines all 30 nBot functions and the 7 bot-only nBattlegrounds getters (GetNumTowers,
-- GetOuterTower, GetVulnerableStructures, GetClosestBaseStructure, GetMinesForTeam, GetFollowersForTitan,
-- IsTitanNodeLocked), plus the mission driver dgsim_bots.MissionTick and a report (dgsim_bots.Report).
--
-- Loading: run this chunk once on the server's lua_State after SporeLabsLua_RegisterSimulatorBindings (D1c-live's
-- registration point in gms/engine/match.cpp), before the bots' brains run. Nothing loads it yet; a live session can
-- with `[host] test_lua=<s>:dgsim_bots_api.lua` (setup_server_copy.py installs it into the host copy's game folder).
-- Running it again is harmless (state in dgsim_bots survives). Its result is a one-line summary.
--
-- Host state: what needs match state (missions, the bots' lanes and the lanes' fronts, shopping, first-time players,
-- the fire tracker, the wells) is the host's, in gms/core/bots/ (and gms/core/wells for the wells); the host exposes it
-- as the C namespace `nBotHost` (gms/core/bots/bots.h lists every function, its arguments and returns). Every nBotHost
-- function is optional: without it this file answers safely (no missions, no purchases, lanes kept here, the lane
-- centres at the lanes' middles, the wells from the objects and the UI-data bindings), so the bots' scripts never fail
-- on a missing function. A C function the host registers into nBot or nBattlegrounds after this chunk replaces the
-- Lua one.
--
-- Conventions (SERVER_LUA_API.md *Lua value conventions*): object ids are light userdata, kObjIDNone the "none";
-- numbers are 4-byte floats; teams nTeamID (Mortal = Blue = 1, Spirit = Red = 2); lanes nLaneID (Top 1, Bottom 2).
-- The bound functions assert (and return nothing) when a numeric argument isn't a number, so every call here passes
-- numbers. [assumed] marks a rule no client code, script or data shows; SERVER_LUA_API.md has the evidence per function.
--
-- Offline check: python -I re/scripts/bots_lua_check.py (LuaJ compile + gms/engine/lua/tests/test_dgsim_bots_api.lua
-- against a stub of the bound API).

dgsim_bots = dgsim_bots or {}
local B = dgsim_bots
B.version = '3'  -- 2: D6b's script fix-ups; 3: D6c's BailOnKill fix-up
B.config = B.config or {
  scanSeconds = 5,            -- game seconds between walks of the object pool (structures, wells)
  scanLimit = 4096,           -- pool slots walked at most
  towerSearchRadius = 40,     -- IsTargetedByTower: towers whose best target is the bot [assumed; Binding aggro 28]
  minionAggroRadius = 35,     -- GetMinionAggroCount [assumed; a lane minion drops its target beyond 33]
  laneCenterThreshold = 5,    -- GetLaneCenterAndDirection(obj [, threshold]) default (nBotUtils passes 5)
  followerRadius = 40,        -- GetFollowersForTitan without the host: workers counted around the well [assumed]
  firstLock = 900,            -- the wells' first lock (s) without the host or the UI data ("At fifteen minutes")
  missionConditions = {       -- Tuning.BotPlan's mission[] (AGENT_AI.md section 11.2)
    'condition_Mission_DefendTower', 'condition_Mission_CaptureWell', 'condition_Mission_DefendWell',
    'condition_Mission_HarassWell', 'condition_Mission_RallyForParasite', 'condition_Mission_KillParasite',
    'condition_Mission_RallyForParasiteContest',
  },
}
B.stats = B.stats or { calls = {}, errors = {}, errorOrder = {} }
B.lanes = B.lanes or {}          -- bot -> nLaneID, when the host has no AssignLane
B.centerCache = B.centerCache or {}
B.seen = B.seen or {}            -- team -> { t = game time of the last refresh, enemies = { id -> {x, y, z, t} } }
B.scan = B.scan or { t = nil, ids = {}, structures = {}, wells = {} }

nBot = nBot or {}
nBattlegrounds = nBattlegrounds or {}

-- ---- helpers ---------------------------------------------------------------------------------------------------

local function count(name)
  local c = B.stats.calls
  c[name] = (c[name] or 0) + 1
end

local function hostfn(name)
  local h = nBotHost
  if type(h) == 'table' then
    local f = h[name]
    if type(f) == 'function' then return f end
  end
  return nil
end
B.hostfn = hostfn

local function isNone(id) return id == nil or id == kObjIDNone end
local function exists(id) return not isNone(id) and nObjectManager.IsValidObject(id) == true end
B.exists = exists

local function vec(x, y, z) return nVector.create(x or 0, y or 0, z or 0) end
local function copy(v) if v == nil then return vec(0, 0, 0) end return vec(v.x, v.y, v.z) end
local function dist(a, b)
  local dx, dy, dz = a.x - b.x, a.y - b.y, (a.z or 0) - (b.z or 0)
  return math.sqrt(dx * dx + dy * dy + dz * dz)
end
B.dist = dist

local function now()
  local f = nGameSimulator and nGameSimulator.GetGameTime
  if f then return f() or 0 end
  return 0
end

-- The scripts' own rule (`if team == Blue then Red else Blue`), so NPC 0 -> Blue.
local function enemyOf(team)
  if team == nTeamID.Blue then return nTeamID.Red end
  return nTeamID.Blue
end
B.enemyOf = enemyOf

local function hpFraction(id)
  local m = nGameObject.GetMaxHitPoints(id)
  if m == nil or m <= 0 then return 1 end
  return (nGameObject.GetHitPoints(id) or 0) / m
end

local function num(x, default)
  local n = tonumber(x)
  if n == nil then return default end
  return n
end

-- nObjectManager.GetObjectsInRadius(position, radius, sortType, targetTypes, team, ignoreModifier, self, descriptors,
-- flags) (0x009396F0; the query's fields in GameObjects_FilterAndSortQuery 0x00881E00); a missing or non-positive
-- radius finds nothing.
local function query(position, radius, sort, types, team, self, flags)
  if position == nil or num(radius, 0) <= 0 then return {} end
  local r = nObjectManager.GetObjectsInRadius(position, num(radius, 0), sort, types, num(team, 0), kObjIDNone,
                                              self or kObjIDNone, 0, flags or 0)
  if type(r) ~= 'table' then return {} end
  return r
end
B.query = query

-- The lane of a point without lane data: SM_Map_v5 mirrors across y = x and the Top lane runs through y > x (every
-- Top knot and Binding: y - x >= 20.8; gms/core/bots/lanes.h LaneByDiagonal). The host's polylines come first.
local function laneOfPoint(p)
  local f = hostfn('ClosestLane')
  if f and p then
    local lane = f(p.x, p.y, p.z or 0)
    if lane == nLaneID.Top or lane == nLaneID.Bottom then return lane end
  end
  if p == nil then return nLaneID.None end
  if p.y - p.x >= 0 then return nLaneID.Top end
  return nLaneID.Bottom
end
B.laneOfPoint = laneOfPoint

-- The lane by the level's tag map (SM_Map_v5_Tags.tga has Top and Bottom regions along the lanes, none in the
-- jungle or the bases): nLaneID.None off the lanes.
local function laneByTags(obj)
  if nLevel and nLevel.ObjectPositionMatchesLevelTag then
    if nLevel.ObjectPositionMatchesLevelTag(obj, nLevelTags.Top) then return nLaneID.Top end
    if nLevel.ObjectPositionMatchesLevelTag(obj, nLevelTags.Bottom) then return nLaneID.Bottom end
  end
  return nLaneID.None
end

-- A Binding's tier from its noun name "Binding_<Team>_<Tier>": Outer 0, Middle 1, Inner 2 (gms/core/matchflow
-- BindingTier); nil for anything else.
local function bindingTier(name)
  if name == nil or not string.find(name, '^Binding_') then return nil end
  if string.find(name, 'Outer') then return 0 end
  if string.find(name, 'Middle') then return 1 end
  if string.find(name, 'Inner') then return 2 end
  return 3
end
B.bindingTier = bindingTier

-- The level's structures and wells, from a walk of the object pool (nObjectManager.GetObjectIdByIndex(i) returns the
-- next live object at or after slot i, kObjIDNone past the last one). Structures and wells are
-- never deleted (a destroyed Binding stays an object), so a walk every scanSeconds is enough.
function B.Scan(force)
  local s = B.scan
  local t = now()
  if not force and s.t ~= nil and t - s.t < B.config.scanSeconds and t >= s.t then return s end
  local seen, ids, structures, wells = {}, {}, {}, {}
  for i = 0, B.config.scanLimit - 1 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if isNone(id) then break end
    if not seen[id] then
      seen[id] = true
      ids[#ids + 1] = id
      local npc = nGameObject.GetNPCType(id)
      if npc == nNPCType.Tower or npc == nNPCType.Nub or npc == nNPCType.Guardian then
        local name = nGameObject.GetAssetName(id)
        local tier = bindingTier(name)
        local lane = nLaneID.None
        if tier ~= nil then lane = laneOfPoint(nGameObject.GetPosition(id)) end
        structures[#structures + 1] = { id = id, npc = npc, name = name, tier = tier, lane = lane }
      elseif npc == nNPCType.GoldMine then
        wells[#wells + 1] = id
      end
    end
  end
  s.t, s.ids, s.structures, s.wells = t, ids, structures, wells
  return s
end

local function standing(id) return exists(id) and nGameObject.IsAlive(id) == true end

-- A well's lock without the host: the UI-data bindings (real code over the server's node list, which D1c-live
-- creates), else the first lock by the match clock [assumed].
function B.WellLocked(well)
  local f = hostfn('IsTitanNodeLocked')
  if f then return f(well) == true end
  if exists(well) and nBattlegrounds.SpiritWell_GetClosest and nBattlegrounds.SpiritWell_IsLocked then
    local ok, i = pcall(nBattlegrounds.SpiritWell_GetClosest, nGameObject.GetPosition(well))
    if ok and type(i) == 'number' and i >= 0 then
      local ok2, locked = pcall(nBattlegrounds.SpiritWell_IsLocked, i)
      if ok2 and type(locked) == 'boolean' then return locked end
    end
  end
  return (nGameSimulator.GetMatchTime() or 0) < B.config.firstLock
end

-- The positions of the enemy players `team` sees now, remembered with the game time (GetNumPlayersNearPosition's
-- history, GetLastSeenEnemyPosition). Refreshed at most once per game-time instant and team.
function B.RememberEnemies(team)
  local mem = B.seen[team]
  if mem == nil then
    mem = { t = nil, enemies = {} }
    B.seen[team] = mem
  end
  local t = now()
  if mem.t == t then return mem end
  mem.t = t
  local players = nPlayer.GetPlayerControlledObjects()
  if type(players) ~= 'table' then return mem end
  for _, c in ipairs(players) do
    if exists(c) and nGameObject.GetTeam(c) ~= team and nGameObject.IsAlive(c) and nGameObject.IsVisibleToTeam(c, team) then
      local p = nGameObject.GetPosition(c)
      mem.enemies[c] = { x = p.x, y = p.y, z = p.z, t = t }
    end
  end
  return mem
end

-- ---- nBattlegrounds: the bot-only getters ------------------------------------------------------------------------

-- GetNumTowers(team, lane) -> the standing Bindings of `team` in `lane` (3 per lane on SM_Map_v5). Callers: bots
-- rally when an enemy lane has <= 1 left (condition_Bot_TimeToRally), siege the lane with more
-- (ability_Bot_AssignToSiegeLane). The client stub asserts "Server only API" and returns 0.
function nBattlegrounds.GetNumTowers(team, lane)
  count('GetNumTowers')
  local n = 0
  for _, s in ipairs(B.Scan().structures) do
    if s.tier ~= nil and s.lane == lane and standing(s.id) and nGameObject.GetTeam(s.id) == team then n = n + 1 end
  end
  return n
end

-- GetOuterTower(team, lane) -> the outermost standing Binding of `team` in `lane`, or kObjIDNone when all are down
-- (the callers then use fixed base positions).
function nBattlegrounds.GetOuterTower(team, lane)
  count('GetOuterTower')
  local best, bestTier = kObjIDNone, nil
  for _, s in ipairs(B.Scan().structures) do
    if s.tier ~= nil and s.lane == lane and standing(s.id) and nGameObject.GetTeam(s.id) == team then
      if bestTier == nil or s.tier < bestTier then
        best, bestTier = s.id, s.tier
      end
    end
  end
  return best
end

-- GetVulnerableStructures(team) -> `team`'s structures (NPC type Tower, Nub, Guardian) the other team can attack now:
-- standing and a valid hostile target for it (targetable, attackable: GameObject_ValidateTarget) [assumed rule].
-- condition_Mission_DefendTower maps them to DefendTower / DefendCore / DefendBase missions.
function nBattlegrounds.GetVulnerableStructures(team)
  count('GetVulnerableStructures')
  local out = {}
  local enemy = enemyOf(team)
  for _, s in ipairs(B.Scan().structures) do
    if standing(s.id) and nGameObject.GetTeam(s.id) == team and nGameObject.ValidateHostileTarget(enemy, s.id, false) then
      out[#out + 1] = s.id
    end
  end
  return out
end

-- GetClosestBaseStructure(obj) -> the nearest enemy base structure (a nub or the Guardian) `obj` can attack, or
-- kObjIDNone (condition_nearest_base_structure, the AttackBase phases).
function nBattlegrounds.GetClosestBaseStructure(obj)
  count('GetClosestBaseStructure')
  if not exists(obj) then return kObjIDNone end
  local team = nGameObject.GetTeam(obj)
  local enemy = enemyOf(team)
  local p = nGameObject.GetPosition(obj)
  local best, bestD = kObjIDNone, nil
  for _, s in ipairs(B.Scan().structures) do
    if (s.npc == nNPCType.Nub or s.npc == nNPCType.Guardian) and standing(s.id) and nGameObject.GetTeam(s.id) == enemy
        and nGameObject.ValidateHostileTarget(team, s.id, false) then
      local d = dist(p, nGameObject.GetPosition(s.id))
      if bestD == nil or d < bestD then
        best, bestD = s.id, d
      end
    end
  end
  return best
end

-- GetMinesForTeam(team [, onlyUnlocked]) -> the spirit wells `team` owns (the well object's team; D9 changes it at a
-- capture). The client stub returns nothing, so its callers (CaptureWell, DefendWell, HarassWell) error today.
function nBattlegrounds.GetMinesForTeam(team, onlyUnlocked)
  count('GetMinesForTeam')
  local f = hostfn('MinesForTeam')
  if f then
    local t = f(team, onlyUnlocked == true)
    if type(t) == 'table' then return t end
  end
  local out = {}
  for _, w in ipairs(B.Scan().wells) do
    if exists(w) and nGameObject.GetTeam(w) == team and not (onlyUnlocked and B.WellLocked(w)) then out[#out + 1] = w end
  end
  return out
end

-- GetFollowersForTitan(well) -> the workers at the well (HarassWell starts above 16, ends below 4). Without the host
-- [assumed]: the Follower NPCs within followerRadius.
function nBattlegrounds.GetFollowersForTitan(well)
  count('GetFollowersForTitan')
  local f = hostfn('FollowersForTitan')
  if f then return num(f(well), 0) end
  if not exists(well) then return 0 end
  local n = 0
  local types = nTargetType.EnemyNPCs + nTargetType.AllyNPCs
  for _, o in ipairs(query(nGameObject.GetPosition(well), B.config.followerRadius, nGetObjectsSortType.None, types,
                           nTeamID.Blue, nil, nGetObjectsFlags.FindUntargetableObjects)) do
    if nGameObject.GetNPCType(o) == nNPCType.Follower then n = n + 1 end
  end
  return n
end

-- IsTitanNodeLocked(well) -> whether the well can't be captured now (HarassWell's CheckEnd).
function nBattlegrounds.IsTitanNodeLocked(well)
  count('IsTitanNodeLocked')
  return B.WellLocked(well) == true
end

-- ---- nBot: targets and objects around ----------------------------------------------------------------------------

-- ValidateHostileBotTarget(team, target [, flag [, skipFireCheck [, skipLocusCheck]]]) -> boolean. The arguments
-- follow BotDirector_IsValidTarget (0x008AFD10: team, target, flag, skipFireCheck, skipLocusCheck) [inferred; the
-- binding itself was compiled out]: unless skipped, false when the target stands in the enemy Locus (level tags Locus
-- and SpiritSide for team 1, MortalSide otherwise) or where the fire tracker marks danger for `team` (nBotHost.
-- IsPositionSafe; the tracker is off in the client), then nGameObject.ValidateHostileTarget(team, target, flag)
-- (targetable, attackable, HP > 0 unless flag, another team). 71 of 82 sites pass (..., false, true): no fire check.
function nBot.ValidateHostileBotTarget(team, target, flag, skipFireCheck, skipLocusCheck)
  count('ValidateHostileBotTarget')
  if type(team) ~= 'number' or not exists(target) then return false end
  if not skipLocusCheck and nLevel and nLevel.ObjectPositionMatchesLevelTag then
    local side = nLevelTags.MortalSide
    if team == nTeamID.Mortal then side = nLevelTags.SpiritSide end
    if nLevel.ObjectPositionMatchesLevelTag(target, nLevelTags.Locus) and nLevel.ObjectPositionMatchesLevelTag(target, side) then
      return false
    end
  end
  if not skipFireCheck then
    local safe = hostfn('IsPositionSafe')
    if safe then
      local p = nGameObject.GetPosition(target)
      if safe(team, p.x, p.y, p.z) == false then return false end
    end
  end
  return nGameObject.ValidateHostileTarget(team, target, flag == true) == true
end

-- GetNearbyObjects(obj, radius, targetTypes, getObjectsFlags) -> array, nearest first, relative to obj's team
-- (nObjectManager.GetObjectsInRadius with sort Distance and obj as "self"; dead objects never come back).
function nBot.GetNearbyObjects(obj, radius, targetTypes, flags)
  count('GetNearbyObjects')
  if not exists(obj) then return {} end
  return query(nGameObject.GetPosition(obj), radius, nGetObjectsSortType.Distance, num(targetTypes, 0),
               nGameObject.GetTeam(obj), obj, num(flags, 0))
end

-- GetEnemyPlayersNearPosition(team, position, radius [, descriptors, checkFoW]) -> array of enemy shapers, nearest
-- first (always a table; condition_Bot_EngageForKill tests it for nil). The 4th argument (0 at its one site) is taken
-- as the descriptor filter and the 5th (true) as the fog-of-war check [assumed].
function nBot.GetEnemyPlayersNearPosition(team, position, radius, descriptors, checkFoW)
  count('GetEnemyPlayersNearPosition')
  if position == nil or num(radius, 0) <= 0 then return {} end
  local flags = 0
  if checkFoW == true then flags = nGetObjectsFlags.CheckFoWVisibility end
  local r = nObjectManager.GetObjectsInRadius(position, num(radius, 0), nGetObjectsSortType.Distance,
                                              nTargetType.EnemyPlayers, num(team, 0), kObjIDNone, kObjIDNone,
                                              descriptors or 0, flags)
  if type(r) ~= 'table' then return {} end
  return r
end

-- GetAllyPlayersNearPosition(team, position, radius) -> a NUMBER (the count of allied shapers, the caller itself
-- included). nBotUtils.GetEnemiesInLane returns the enemy variant's table, and ability_Bot_GroupInLane compares the
-- two (`table > number` raises: a script bug, kept).
function nBot.GetAllyPlayersNearPosition(team, position, radius)
  count('GetAllyPlayersNearPosition')
  return #query(position, radius, nGetObjectsSortType.None, nTargetType.AllyPlayers, team, nil, 0)
end

-- GetNumPlayersNearPosition(team, posA, posB, radiusA, radiusB [, minHpA, minHpB [, historySec]]) -> allies, enemies:
-- allied shapers within radiusA of posA and enemy shapers within radiusB of posB whose health fraction is at least
-- minHpA / minHpB ("ignoreHPThreshold"); enemies only if `team` sees them, plus (historySec) those it saw in the last
-- historySec seconds whose last-seen position lies within radiusB [assumed]. A nil radius counts nothing
-- (condition_Bot_VeryOutnumbered passes its undefined `searchRadius`: a script bug, kept).
function nBot.GetNumPlayersNearPosition(team, posA, posB, radiusA, radiusB, minHpA, minHpB, historySec)
  count('GetNumPlayersNearPosition')
  local allies, enemies = 0, 0
  local hpA, hpB = num(minHpA, 0), num(minHpB, 0)
  for _, o in ipairs(query(posA, radiusA, nGetObjectsSortType.None, nTargetType.AllyPlayers, team, nil, 0)) do
    if hpFraction(o) >= hpA then allies = allies + 1 end
  end
  local counted = {}
  for _, o in ipairs(query(posB, radiusB, nGetObjectsSortType.None, nTargetType.EnemyPlayers, team, nil,
                           nGetObjectsFlags.CheckFoWVisibility)) do
    counted[o] = true
    if hpFraction(o) >= hpB then enemies = enemies + 1 end
  end
  local history = num(historySec, 0)
  if history > 0 and posB ~= nil and num(radiusB, 0) > 0 and type(team) == 'number' then
    local mem = B.RememberEnemies(team)
    local t = now()
    for id, rec in pairs(mem.enemies) do
      if not counted[id] and t - rec.t <= history and dist(rec, posB) <= num(radiusB, 0) and standing(id)
          and hpFraction(id) >= hpB then
        enemies = enemies + 1
      end
    end
  end
  return allies, enemies
end

-- NumAlliesRallying(bot, rallyPos, clashPos, minHp, radiusSelf, radiusEnemyBase, radiusRally, radiusClash) -> number:
-- the bot's living allied shapers (the bot included) at or above minHp that stand within radiusSelf of the bot, within
-- radiusEnemyBase of the enemy Guardian, within radiusRally of rallyPos or within radiusClash of clashPos [assumed].
-- condition_Bot_GroupedWithAllies wants 3.
function nBot.NumAlliesRallying(bot, rallyPos, clashPos, minHp, radiusSelf, radiusEnemyBase, radiusRally, radiusClash)
  count('NumAlliesRallying')
  if not exists(bot) then return 0 end
  local team = nGameObject.GetTeam(bot)
  local me = nGameObject.GetPosition(bot)
  local base = nil
  local g = nBattlegrounds.GetGuardianOnTeam and nBattlegrounds.GetGuardianOnTeam(enemyOf(team))
  if exists(g) then base = nGameObject.GetPosition(g) end
  local players = nPlayer.GetPlayerControlledObjects()
  if type(players) ~= 'table' then return 0 end
  local n, hp = 0, num(minHp, 0)
  for _, c in ipairs(players) do
    if standing(c) and nGameObject.GetTeam(c) == team and hpFraction(c) >= hp then
      local p = nGameObject.GetPosition(c)
      if dist(p, me) <= num(radiusSelf, 0) or (base ~= nil and dist(p, base) <= num(radiusEnemyBase, 0))
          or (rallyPos ~= nil and dist(p, rallyPos) <= num(radiusRally, 0))
          or (clashPos ~= nil and dist(p, clashPos) <= num(radiusClash, 0)) then
        n = n + 1
      end
    end
  end
  return n
end

-- IsNearTower(obj, towerTeam, radius) -> boolean, tower: the nearest standing tower (NPC type Tower) of towerTeam
-- within radius (nBotUtils passes 60 by default).
function nBot.IsNearTower(obj, towerTeam, radius)
  count('IsNearTower')
  if not exists(obj) then return false, kObjIDNone end
  for _, o in ipairs(query(nGameObject.GetPosition(obj), radius, nGetObjectsSortType.Distance,
                           nTargetType.AllyStructures, towerTeam, nil, nGetObjectsFlags.FindUntargetableObjects)) do
    if nGameObject.GetNPCType(o) == nNPCType.Tower then return true, o end
  end
  return false, kObjIDNone
end

-- IsTargetedByTower(bot) -> boolean: an enemy tower near the bot has it as its best target (nAgent.GetBestTarget, the
-- Binding's aggro list since D1b-live).
function nBot.IsTargetedByTower(bot)
  count('IsTargetedByTower')
  if not exists(bot) then return false end
  for _, o in ipairs(query(nGameObject.GetPosition(bot), B.config.towerSearchRadius, nGetObjectsSortType.Distance,
                           nTargetType.EnemyStructures, nGameObject.GetTeam(bot), nil,
                           nGetObjectsFlags.FindUntargetableObjects)) do
    if nGameObject.GetNPCType(o) == nNPCType.Tower and nAgent.GetBestTarget(o) == bot then return true end
  end
  return false
end

-- GetMinionAggroCount(bot) -> the enemy minions near the bot whose best target it is (condition_too_many_minions: > 3).
function nBot.GetMinionAggroCount(bot)
  count('GetMinionAggroCount')
  if not exists(bot) then return 0 end
  local n = 0
  for _, o in ipairs(query(nGameObject.GetPosition(bot), B.config.minionAggroRadius, nGetObjectsSortType.None,
                           nTargetType.EnemyNPCs, nGameObject.GetTeam(bot), nil, 0)) do
    if nGameObject.IsMinion(o) and nAgent.GetBestTarget(o) == bot then n = n + 1 end
  end
  return n
end

-- GetSkillshotTarget(bot, target, projectileSpeed, range) -> ok, aim: where a projectile of that speed from the bot
-- meets the target if it keeps its velocity (2-D lead; the aim keeps the target's height). ok is false (aim = the
-- target's position) when the target is gone, the speed isn't positive or the projectile can't catch it. The range is
-- left to the callers (condition_Artist_NearestPlayerQ checks it itself) [assumed].
function nBot.GetSkillshotTarget(bot, target, projectileSpeed, range)
  count('GetSkillshotTarget')
  if not exists(bot) or not exists(target) then return false, vec(0, 0, 0) end
  local s = nGameObject.GetPosition(bot)
  local p = nGameObject.GetPosition(target)
  local speed = num(projectileSpeed, 0)
  if speed <= 0 then return false, copy(p) end
  local v = nGameObject.GetLinearVelocity(target)
  local vx, vy = 0, 0
  if type(v) == 'table' then vx, vy = v.x or 0, v.y or 0 end
  local dx, dy = p.x - s.x, p.y - s.y
  local a = vx * vx + vy * vy - speed * speed
  local b = 2 * (dx * vx + dy * vy)
  local c = dx * dx + dy * dy
  local t = nil
  if math.abs(a) < 1e-6 then
    if b < 0 then t = -c / b end
  else
    local disc = b * b - 4 * a * c
    if disc >= 0 then
      local r = math.sqrt(disc)
      local t1, t2 = (-b - r) / (2 * a), (-b + r) / (2 * a)
      if t1 > t2 then t1, t2 = t2, t1 end
      if t1 >= 0 then t = t1 elseif t2 >= 0 then t = t2 end
    end
  end
  if t == nil then return false, copy(p) end
  return true, vec(p.x + vx * t, p.y + vy * t, p.z)
end

-- IsEnemyOfFirstTimePlayer(bot) -> boolean: an enemy human of the bot plays their first game (the bots then don't
-- harass humans and engage only below 20 % health). Host state: nBotHost.IsFirstTimePlayer(creature) (the roster's
-- FTUE flag); false without it.
function nBot.IsEnemyOfFirstTimePlayer(bot)
  count('IsEnemyOfFirstTimePlayer')
  local f = hostfn('IsFirstTimePlayer')
  if not f or not exists(bot) then return false end
  local team = nGameObject.GetTeam(bot)
  local players = nPlayer.GetPlayerControlledObjects()
  if type(players) ~= 'table' then return false end
  for _, c in ipairs(players) do
    if exists(c) and nGameObject.GetTeam(c) ~= team and not nPlayer.IsBot(c) and f(c) == true then return true end
  end
  return false
end

-- ---- nBot: lanes -------------------------------------------------------------------------------------------------

-- AssignLane(bot, lane): the bot's lane (host: BotLanes; else kept here).
function nBot.AssignLane(bot, lane)
  count('AssignLane')
  local f = hostfn('AssignLane')
  if f then
    f(bot, num(lane, 0))
    return
  end
  if isNone(bot) then return end
  if lane == nLaneID.Top or lane == nLaneID.Bottom then B.lanes[bot] = lane else B.lanes[bot] = nil end
end

-- GetLaneID(obj) -> nLaneID: a bot's assigned lane; for anyone else (condition_Bot_CheckForReinforcements asks for
-- every player) the lane by the tag map, None off the lanes [assumed].
function nBot.GetLaneID(obj)
  count('GetLaneID')
  local f = hostfn('GetLane')
  if f then
    local lane = f(obj)
    if lane == nLaneID.Top or lane == nLaneID.Bottom then return lane end
  end
  local own = B.lanes[obj]
  if own ~= nil then return own end
  if not exists(obj) then return nLaneID.None end
  return laneByTags(obj)
end

-- GetClosestLane(obj) -> nLaneID: the tag map's lane, else the nearest lane (the host's polylines, or the diagonal).
function nBot.GetClosestLane(obj)
  count('GetClosestLane')
  if not exists(obj) then return nLaneID.None end
  local lane = laneByTags(obj)
  if lane ~= nLaneID.None then return lane end
  return laneOfPoint(nGameObject.GetPosition(obj))
end

-- Without the host: each lane's middle on SM_Map_v5, where the first waves meet (MATCH_FLOW.md section 2.4, top (-69.5,
-- 68.2), bottom (66.5, -68.7)), and the Mortal direction along the lane there (top knot 5 -> 6; the bottom mirrored).
B.staticFronts = B.staticFronts or {
  [1] = { x = -69.5, y = 68.2, dx = 0.7333, dy = 0.6800 },
  [2] = { x = 66.5, y = -68.7, dx = 0.6800, dy = 0.7333 },
}

-- The centre and direction of `team`'s front on `lane` (host: LaneFronts; else the static middle).
function B.LaneCenter(lane, team)
  local f = hostfn('LaneCenterAndDirection')
  if f then
    local cx, cy, cz, dx, dy, dz = f(num(lane, 0), num(team, 0))
    if type(cx) == 'number' then return vec(cx, cy, cz), vec(dx, dy, dz) end
  end
  local s = B.staticFronts[lane]
  if s == nil then return vec(0, 0, 0), vec(0, 0, 0) end
  local sign = 1
  if team == nTeamID.Spirit then sign = -1 end
  return vec(s.x, s.y, 0), vec(sign * s.dx, sign * s.dy, 0)
end

-- GetLaneCenterAndDirection(lane, team) or (obj [, threshold]) -> centre, direction: the lane's clash point for that
-- team and the unit direction toward the enemy (nBotUtils.GetLanePosition stands bots 12-15 behind it). The object form
-- uses the object's lane (or the closest) and team, and keeps its last centre while the new one lies within `threshold`
-- (default 5) of it [assumed meaning of the threshold]. Note nBotUtils.GetAlliesInLane/GetEnemiesInLane pass (team,
-- lane) the other way round when ability_Bot_GroupInLane calls them as (lane, team) (a script bug, kept).
function nBot.GetLaneCenterAndDirection(a, b)
  count('GetLaneCenterAndDirection')
  if type(a) == 'number' then return B.LaneCenter(a, b) end
  if not exists(a) then return vec(0, 0, 0), vec(0, 0, 0) end
  local team = nGameObject.GetTeam(a)
  local lane = nBot.GetLaneID(a)
  if lane ~= nLaneID.Top and lane ~= nLaneID.Bottom then lane = nBot.GetClosestLane(a) end
  local c, d = B.LaneCenter(lane, team)
  local threshold = num(b, B.config.laneCenterThreshold)
  local cache = B.centerCache[a]
  if cache ~= nil and cache.lane == lane and cache.team == team and dist(cache, c) <= threshold then
    return vec(cache.x, cache.y, cache.z), d
  end
  B.centerCache[a] = { lane = lane, team = team, x = c.x, y = c.y, z = c.z }
  return c, d
end

-- ---- nBot: missions (host state: gms/core/bots/missions.h) --------------------------------------------------------

-- RequestMission(team, type, target, agents, timeoutSec, conditionGUID [, destination]): open or refresh a mission.
function nBot.RequestMission(team, missionType, target, agents, timeoutSec, conditionGUID, destination)
  count('RequestMission')
  local f = hostfn('RequestMission')
  if not f or type(team) ~= 'number' or type(missionType) ~= 'number' then return end
  local hasDest = destination ~= nil
  local dx, dy, dz = 0, 0, 0
  if hasDest then dx, dy, dz = destination.x or 0, destination.y or 0, destination.z or 0 end
  f(team, missionType, target or kObjIDNone, num(agents, 1), num(timeoutSec, 0), conditionGUID or kObjIDNone, hasDest,
    dx, dy, dz)
end

-- IsMissionAvailable(team, type) -> boolean: an open mission of that type exists for the team.
function nBot.IsMissionAvailable(team, missionType)
  count('IsMissionAvailable')
  local f = hostfn('IsMissionAvailable')
  if not f or type(team) ~= 'number' or type(missionType) ~= 'number' then return false end
  return f(team, missionType) == true
end

-- GetAvailableMission(bot, type [, range]) -> mission id, 0 = none (condition_Bot_IsMissionAvailable returns it as
-- its 3rd value; range 0 = anywhere).
function nBot.GetAvailableMission(bot, missionType, range)
  count('GetAvailableMission')
  local f = hostfn('GetAvailableMission')
  if not f or isNone(bot) or type(missionType) ~= 'number' then return 0 end
  return num(f(bot, missionType, num(range, 0)), 0)
end

-- AcceptMission(bot, id): only ability_Bot_AcceptMission (unreferenced) calls it; the brain accepts in C++.
function nBot.AcceptMission(bot, id)
  count('AcceptMission')
  local f = hostfn('AcceptMission')
  if f and not isNone(bot) and type(id) == 'number' and id > 0 then return f(bot, id) == true end
  return false
end

-- GetMissionID(bot) -> id, 0 = none (condition_Bot_IsMissionActive: `0 < id`).
function nBot.GetMissionID(bot)
  count('GetMissionID')
  local f = hostfn('GetMissionID')
  if not f or isNone(bot) then return 0 end
  return num(f(bot), 0)
end

-- GetMissionTarget(bot) -> object or kObjIDNone (never nil: the behaviours compare with kObjIDNone, then read its
-- position).
function nBot.GetMissionTarget(bot)
  count('GetMissionTarget')
  local f = hostfn('GetMissionTarget')
  if not f or isNone(bot) then return kObjIDNone end
  local t = f(bot)
  if t == nil then return kObjIDNone end
  return t
end

-- GetMissionDestination(bot) -> a vector, the zero vector for none (never nil: the callers read .x and .y).
function nBot.GetMissionDestination(bot)
  count('GetMissionDestination')
  local f = hostfn('GetMissionDestination')
  if not f or isNone(bot) then return vec(0, 0, 0) end
  local x, y, z = f(bot)
  return vec(num(x, 0), num(y, 0), num(z, 0))
end

-- ---- nBot: shopping (host state: gms/core/bots/shopping.h + D7b's purchase path) ----------------------------------

function nBot.CanAffordNextItem(bot)
  count('CanAffordNextItem')
  local f = hostfn('CanAffordNextItem')
  if not f or not exists(bot) then return false end
  return f(bot) == true
end

function nBot.PurchaseNextItem(bot)
  count('PurchaseNextItem')
  local f = hostfn('PurchaseNextItem')
  if f and exists(bot) then f(bot) end
end

function nBot.CanBuyPotion(bot)
  count('CanBuyPotion')
  local f = hostfn('CanBuyPotion')
  if not f or not exists(bot) then return false end
  return f(bot) == true
end

function nBot.PurchasePotions(bot)
  count('PurchasePotions')
  local f = hostfn('PurchasePotions')
  if f and exists(bot) then f(bot) end
end

-- ---- nBot: only unit-test scripts call these (tier 4; written anyway) -----------------------------------------------

-- GetLastSeenEnemyPosition(bot) -> where the bot's team last saw the bot's target (nGameObject.GetTargetID), else
-- the zero vector.
function nBot.GetLastSeenEnemyPosition(bot)
  count('GetLastSeenEnemyPosition')
  if not exists(bot) then return vec(0, 0, 0) end
  local target = nGameObject.GetTargetID(bot)
  if isNone(target) then return vec(0, 0, 0) end
  local rec = B.RememberEnemies(nGameObject.GetTeam(bot)).enemies[target]
  if rec == nil then return vec(0, 0, 0) end
  return vec(rec.x, rec.y, rec.z)
end

-- LostSightOfTarget(bot) -> lost, target: the bot's target is alive but its team doesn't see it.
function nBot.LostSightOfTarget(bot)
  count('LostSightOfTarget')
  if not exists(bot) then return false, kObjIDNone end
  local target = nGameObject.GetTargetID(bot)
  if not standing(target) then return false, target or kObjIDNone end
  return not nGameObject.IsVisibleToTeam(target, nGameObject.GetTeam(bot)), target
end

-- IsEnemyStronger(bot, radius, levelGap) -> an enemy shaper within radius is levelGap or more levels above the bot
-- [assumed].
function nBot.IsEnemyStronger(bot, radius, levelGap)
  count('IsEnemyStronger')
  if not exists(bot) then return false end
  local mine = num(nGameObject.GetPlayerLevel(bot), 0)
  for _, o in ipairs(nBot.GetEnemyPlayersNearPosition(nGameObject.GetTeam(bot), nGameObject.GetPosition(bot), radius, 0, true)) do
    if num(nGameObject.GetPlayerLevel(o), 0) - mine >= num(levelGap, 0) then return true end
  end
  return false
end

-- IsOutnumbered(bot, radius, minHp) -> fewer allies than enemies around the bot.
function nBot.IsOutnumbered(bot, radius, minHp)
  count('IsOutnumbered')
  if not exists(bot) then return false end
  local p = nGameObject.GetPosition(bot)
  local a, e = nBot.GetNumPlayersNearPosition(nGameObject.GetTeam(bot), p, p, radius, radius, minHp, minHp)
  return a < e
end

-- ---- the mission driver ------------------------------------------------------------------------------------------

local function noteError(text)
  local e = B.stats.errors
  local order = B.stats.errorOrder
  if e[text] == nil then
    if #order >= 64 then text = '(more distinct errors)' end
    if e[text] == nil then
      e[text] = 0
      order[#order + 1] = text
    end
  end
  e[text] = e[text] + 1
end

-- A global by name (the condition tables are globals of their scripts).
local function global(name)
  if type(_G) == 'table' then return _G[name] end
  return nil
end
B.noteError = noteError

-- dgsim_bots.MissionTick([conditionNames]): the host calls it about once a second [assumed rate]. Per team (Mortal,
-- Spirit): each mission condition's Check(team) (it requests its missions), then, for every open mission with a known
-- condition, CheckEnd(team, target); the missions whose CheckEnd returned true end (nBotHost.EndMission). Running Check
-- before CheckEnd in one tick closes a mission that would be re-requested and ended at once (DefendTower while a
-- DefendBase mission is open) before any bot can take it. Errors are caught per call and counted (dgsim_bots.Report).
-- Returns checks, ends, errors.
function B.MissionTick(names)
  names = names or B.config.missionConditions
  local byGuid = {}
  for _, name in ipairs(names) do
    local c = global(name)
    if type(c) == 'table' and c.conditionGUID ~= nil then byGuid[c.conditionGUID] = c end
  end
  local checks, ends, errors = 0, 0, 0
  local openf, endf = hostfn('OpenMission'), hostfn('EndMission')
  for _, team in ipairs({ nTeamID.Mortal, nTeamID.Spirit }) do
    for _, name in ipairs(names) do
      local c = global(name)
      local check = type(c) == 'table' and c[nConditionFns.Check]
      if type(check) == 'function' then
        checks = checks + 1
        local ok, err = pcall(check, team)
        if not ok then
          errors = errors + 1
          noteError(name .. ' Check: ' .. tostring(err))
        end
      end
    end
    if openf and endf then
      local finished = {}
      for i = 1, 256 do
        local id, target, guid = openf(team, i)
        if id == nil then break end
        local c = byGuid[guid]
        local checkEnd = c and c[nConditionFns.CheckEnd]
        if type(checkEnd) == 'function' then
          local ok, done = pcall(checkEnd, team, target)
          if not ok then
            errors = errors + 1
            noteError(tostring(c.namespace) .. ' CheckEnd: ' .. tostring(done))
          elseif done then
            finished[#finished + 1] = id
          end
        end
      end
      for _, id in ipairs(finished) do
        endf(id)
        ends = ends + 1
      end
    end
  end
  return checks, ends, errors
end

-- ---- D6b: the script fix-ups (D6c added the third) ------------------------------------------------------------------

-- Decided in D6b (docs/SERVER_LUA_API.md *Script bugs the shims keep*): they are fixed here, over the scripts' own
-- tables after they are loaded; the game's data stays as it is. The host turns them off with [host] bot_fixups=0
-- (it sets dgsim_bots.fixupsWanted before this chunk runs).
--   1. condition_Bot_VeryOutnumbered reads `p2 or defaultSearchRadius` into a local and then passes its undefined
--      `searchRadius` to nBot.GetNumPlayersNearPosition, which counts nothing for a nil radius, so the condition never
--      passed and the `VeryOutnumbered` half of every link into Flee was dead. Fix: the table's searchRadius =
--      defaultSearchRadius (30), what the local holds for every use in the data (no link passes a parameter).
--   2. ability_Bot_GroupInLane compares nBotUtils.GetEnemiesInLane's table with GetAlliesInLane's number (Lua 5.1
--      raises on every Tick, no metamethod applies between a table and a number) and passes (lane, team) to helpers that
--      take (team, lane). Fix: its Tick is replaced by a copy that passes (team, lane) and counts the table (#). Only the
--      Creature_Gladiator and Sculptor phases use it (not Tuning.BotPlan's five bots); ability_Bot_SpreadOutLanes has
--      the same bug but no phase uses it, so it stays.
local function countOf(x)
  if type(x) == 'table' then return #x end
  return tonumber(x) or 0
end

local function groupInLaneTick(p3)
  local v4 = ability_Bot_GroupInLane
  p3.nearTower = false
  p3.myPosition = nGameObject.GetPosition(p3.myObjectID)
  p3.nearbyTowers = nObjectManager.GetObjectsInRadius(p3.myPosition, v4.searchRadius, nGetObjectsSortType.Distance,
                                                      nTargetType.EnemyStructures, 0, kObjIDNone, kObjIDNone,
                                                      v4.descriptors, 0) or {}
  for _, tower in ipairs(p3.nearbyTowers) do
    if nBot.ValidateHostileBotTarget(p3.myTeam, tower, false, true) then
      p3.nearTower = true
      break
    end
  end
  p3.alliesTop = countOf(nBotUtils.GetAlliesInLane(p3.myTeam, nLaneID.Top))
  p3.enemiesTop = countOf(nBotUtils.GetEnemiesInLane(p3.myTeam, nLaneID.Top))
  p3.alliesBot = countOf(nBotUtils.GetAlliesInLane(p3.myTeam, nLaneID.Bottom))
  p3.enemiesBot = countOf(nBotUtils.GetEnemiesInLane(p3.myTeam, nLaneID.Bottom))
  if not p3.nearTower then
    if p3.enemiesTop > p3.alliesTop then
      nBot.AssignLane(p3.myObjectID, nLaneID.Top)
    elseif p3.enemiesBot > p3.alliesBot then
      nBot.AssignLane(p3.myObjectID, nLaneID.Bottom)
    elseif p3.alliesTop > p3.alliesBot then
      nBot.AssignLane(p3.myObjectID, nLaneID.Top)
    elseif p3.alliesBot > p3.alliesTop then
      nBot.AssignLane(p3.myObjectID, nLaneID.Bottom)
    end
  end
end
B.groupInLaneTick = groupInLaneTick

--   3. (D6c) condition_Bot_BailOnKill bails when nBotUtils.IsEnemyPlayerNear(bot, enemySearchRadius 30) finds no enemy
--      player, while condition_Bot_EngageForKill engages one up to its own enemySearchRadius 55 away. Both links have
--      no cooldown, so a bot whose target stood 30-55 units off entered EngageForKill and left it again at its next
--      think, every 0.4 s (d6c_run1: all 36 quick exits from EngageForKill had their target 30-55 units away; keeping
--      the link timers across visits doesn't help, there are none). Fix: inside BailOnKill the "is an enemy near"
--      search reaches as far as EngageForKill's (the enemy count keeps BailOnKill's 30), so a bot keeps engaging the
--      target it went for until the fight itself turns. [assumed: the original's think cadence is unknown; the two
--      scripts disagree either way]
local function bailOnKillCheck(original)
  return function(bot, ...)
    local near = nBotUtils.IsEnemyPlayerNear
    local bail = condition_Bot_BailOnKill
    local engage = global('condition_Bot_EngageForKill')
    local wide = (type(engage) == 'table' and tonumber(engage.enemySearchRadius)) or 55
    nBotUtils.IsEnemyPlayerNear = function(o, r)
      if o == bot and r ~= nil and r == bail.enemySearchRadius and wide > r then return near(o, wide) end
      return near(o, r)
    end
    local ok, a, b, c, d, e = pcall(original, bot, ...)
    nBotUtils.IsEnemyPlayerNear = near
    if not ok then error(a, 0) end
    return a, b, c, d, e
  end
end
B.bailOnKillCheck = bailOnKillCheck

B.fixups = B.fixups or { veryOutnumbered = false, groupInLane = false, bailOnKill = false }

-- Applies what isn't applied yet (the scripts' tables may load after this chunk: the driver calls it again every
-- tick until both are in). Returns a one-line state.
function B.ApplyFixups()
  if B.fixupsWanted == false then return 'fix-ups off' end
  local f = B.fixups
  local c = global('condition_Bot_VeryOutnumbered')
  if not f.veryOutnumbered and type(c) == 'table' then
    if c.searchRadius == nil then c.searchRadius = c.defaultSearchRadius or 30 end
    f.veryOutnumbered = true
  end
  local a = global('ability_Bot_GroupInLane')
  if not f.groupInLane and type(a) == 'table' and nAbilityFns and nAbilityFns.Tick ~= nil then
    a[nAbilityFns.Tick] = groupInLaneTick
    f.groupInLane = true
  end
  local k = global('condition_Bot_BailOnKill')
  if not f.bailOnKill and type(k) == 'table' and type(k[1]) == 'function' and type(nBotUtils) == 'table' and
      type(nBotUtils.IsEnemyPlayerNear) == 'function' then
    k[1] = bailOnKillCheck(k[1])
    f.bailOnKill = true
  end
  return 'fix-ups: VeryOutnumbered ' .. tostring(f.veryOutnumbered) .. ', GroupInLane ' .. tostring(f.groupInLane) ..
         ', BailOnKill ' .. tostring(f.bailOnKill == true)
end

local mainMissionTick = B.MissionTick
function B.MissionTick(names)
  if not (B.fixups.veryOutnumbered and B.fixups.groupInLane and B.fixups.bailOnKill) then B.ApplyFixups() end
  return mainMissionTick(names)
end

-- ---- report ------------------------------------------------------------------------------------------------------

-- dgsim_bots.Report() -> text: the shims' view of the match (for a [host] test_lua probe: dgsim_bots_report.lua).
function B.Report()
  local out = {}
  local function p(s) out[#out + 1] = s end
  local host = {}
  if type(nBotHost) == 'table' then
    for k, v in pairs(nBotHost) do if type(v) == 'function' then host[#host + 1] = k end end
  end
  table.sort(host)
  p('dgsim_bots ' .. B.version .. ': nBotHost ' .. (#host > 0 and table.concat(host, ',') or 'none'))
  p(B.ApplyFixups())
  local s = B.Scan(true)
  local bindings = 0
  for _, st in ipairs(s.structures) do if st.tier ~= nil then bindings = bindings + 1 end end
  p('scan: ' .. #s.ids .. ' objects, ' .. #s.structures .. ' structures (' .. bindings .. ' Bindings), ' .. #s.wells .. ' wells')
  for team = 1, 2 do
    local line = 'team ' .. team .. ':'
    for lane = 1, 2 do
      local outer = nBattlegrounds.GetOuterTower(team, lane)
      local name = 'none'
      if exists(outer) then name = tostring(nGameObject.GetAssetName(outer)) end
      line = line .. ' lane ' .. lane .. ' towers ' .. nBattlegrounds.GetNumTowers(team, lane) .. ' outer ' .. name .. ';'
    end
    line = line .. ' vulnerable ' .. #nBattlegrounds.GetVulnerableStructures(team) .. ', wells ' ..
           #nBattlegrounds.GetMinesForTeam(team) .. ' (unlocked ' .. #nBattlegrounds.GetMinesForTeam(team, true) .. ')'
    for lane = 1, 2 do
      local c, d = B.LaneCenter(lane, team)
      line = line .. string.format(' front %d (%.1f %.1f) dir (%.2f %.2f)', lane, c.x, c.y, d.x, d.y)
    end
    p(line)
  end
  local players = nPlayer.GetPlayerControlledObjects()
  if type(players) == 'table' then
    for _, c in ipairs(players) do
      if exists(c) then
        p(string.format('player %s team %s bot %s lane %d closest %d mission %d', tostring(nGameObject.GetAssetName(c)),
          tostring(nGameObject.GetTeam(c)), tostring(nPlayer.IsBot(c)), nBot.GetLaneID(c), nBot.GetClosestLane(c),
          nBot.GetMissionID(c)))
      end
    end
  end
  local calls = {}
  for k, v in pairs(B.stats.calls) do calls[#calls + 1] = k .. '=' .. v end
  table.sort(calls)
  p('calls: ' .. (#calls > 0 and table.concat(calls, ' ') or 'none'))
  for _, text in ipairs(B.stats.errorOrder) do p('error x' .. B.stats.errors[text] .. ': ' .. text) end
  return table.concat(out, '\n')
end

local defined = 0
for _, v in pairs(nBot) do if type(v) == 'function' then defined = defined + 1 end end
local hostCount = 0
if type(nBotHost) == 'table' then
  for _, v in pairs(nBotHost) do if type(v) == 'function' then hostCount = hostCount + 1 end end
end
return 'dgsim_bots_api ' .. B.version .. ': nBot ' .. defined .. ' functions, nBattlegrounds bot getters 7, nBotHost ' ..
       (type(nBotHost) == 'table' and ('present (' .. hostCount .. ' functions)') or 'absent') .. '; ' ..
       B.ApplyFixups()
