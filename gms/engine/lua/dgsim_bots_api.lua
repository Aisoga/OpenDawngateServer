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
B.version = '8'  -- 8: BOTTEAM's team calls; 7: BOTCOMP's composition rules (Predator harass, Hunter parasite, lane lock); 2: D6b's script fix-ups; 3: D6c's BailOnKill fix-up; 4: D6e's GetClosestCampPosition; 5: BOTPARA's parasite fix-up; 6: BOTIDLE's camp radius
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
  -- BOTCOMP: with the composed setup (one Gladiator per lane, a support each) the lanes are the setup's: no balancing
  if not p3.nearTower and B.compositionWanted ~= true then
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
  if not f.parasite then
    if B.parasiteWanted == false then f.parasite = true else B.ApplyParasiteFixup() end
  end
  -- BOTIDLE fix-up 5: the Hunter's Farm plan leaves MoveToNextCamp only when the bot is within `radius` (5) of the camp's
  -- marker, but ability_Bot_MoveToTarget's MoveToPointExact ends where the camp's creatures (repulsors since D1k) let it,
  -- about 5.4 units out: the bot stood there for the match (in a test run: "gambit 0 at the point ... 5.4 units away" every
  -- 11 s). B.campRadius (host: [host] bot_camp_radius, 9; 0 keeps the script's 5) widens the link's test; the ClearCamp
  -- gambits look 30 units around, so nothing else changes.
  if not f.campRadius then
    local c = global('condition_Bot_IsNearNextCreepCamp')
    if type(c) == 'table' then
      local want = B.campRadius
      if want == nil then want = 9 end
      if want > 0 and (c.radius == nil or want > c.radius) then c.radius = want end
      f.campRadius = true
    end
  end
  if not f.composition then B.ApplyCompositionFixup() end
  if not f.lane then B.ApplyLaneFixup() end
  if f.lane and not f.team and type(B.ApplyTeamFixup) == 'function' then B.ApplyTeamFixup() end
  return 'fix-ups: VeryOutnumbered ' .. tostring(f.veryOutnumbered) .. ', GroupInLane ' .. tostring(f.groupInLane) ..
         ', BailOnKill ' .. tostring(f.bailOnKill == true) .. ', Parasite ' .. tostring(f.parasite == true) ..
         ', CampRadius ' .. tostring(f.campRadius == true) .. ', Composition ' .. tostring(f.composition == true) ..
         ', Lane ' .. tostring(f.lane == true) .. ', Team ' .. tostring(f.team == true)
end


-- ---- BOTPARA: the parasite missions (fix-up 4, [host] bot_parasite_guard) -------------------------------------------
-- The user's report: bots "guard" the parasite's area doing nothing, even when the parasite isn't there. The chain
--: a Rally mission (11) has timeout 0 and its CheckEnd ends it only when the target is no
-- valid hostile, fewer than 5 allies live or all 5 enemies live; its Check (match > 900 s, 5 allies, at most 3 enemies
-- alive) only gates the opening. Bots in the RallyForParasite plan pace (nBehavior_PaceNearMissionTarget) about the
-- mission's fixed rally point, 33 units from the parasite, until three of them stand within 20 of it AND a KillParasite
-- mission exists; with fewer bots there (others in Flee, a well, a lane) nothing ends the mission, and a link back to
-- Main fires only for a base defence. Fixes, all [assumed], none changes a script's table on disk:
--   a. CheckEnd of Rally, Kill and Contest also ends when the mission's target is no longer the director's parasite or
--      is not standing (a stale id after a stage change, a corpse);
--   b. a Rally mission ends after rallyPatience seconds (90): the KillParasite plan does not need the Rally mission, so
--      a gather that happened has left it already; the team's Rally Check then stays shut for rallyCooldown seconds
--      (120) so the bots do not re-form at once;
--   c. the two gather conditions fail while the director's parasite is not a valid standing target.
B.parasiteConfig = B.parasiteConfig or { rallyPatience = 90, rallyCooldown = 120 }
B.rallyOpened = B.rallyOpened or {}   -- team .. ':' .. target -> match time first seen open
B.rallyBlocked = B.rallyBlocked or {} -- team -> match time until which Rally's Check does nothing

local function matchTime() return nGameSimulator.GetMatchTime() or 0 end

local function parasiteStands(team, target)
  if isNone(target) or not exists(target) then return false end
  local para = nDirector and nDirector.GetParasiteID and nDirector.GetParasiteID() or kObjIDNone
  if para ~= target then return false end
  return nGameObject.IsAlive(target) == true and nBot.ValidateHostileBotTarget(team, target, false, true) == true
end
B.parasiteStands = parasiteStands

local function wrapParasiteEnd(original)
  return function(team, target, ...)
    local done = original(team, target, ...)
    if done then return done end
    if not parasiteStands(team, target) then return true end
    return done
  end
end

local function wrapRallyEnd(original)
  return function(team, target, ...)
    local done = original(team, target, ...)
    local key = tostring(team) .. ':' .. tostring(target)
    if done then B.rallyOpened[key] = nil return done end
    local now = matchTime()
    local e = B.rallyOpened[key]
    -- a gap in the checks (the mission ended some other way and came back) starts the count again
    if e == nil or now - e.last > 5 then e = { first = now } B.rallyOpened[key] = e end
    e.last = now
    if now - e.first >= B.parasiteConfig.rallyPatience then
      B.rallyOpened[key] = nil
      B.rallyBlocked[team] = now + B.parasiteConfig.rallyCooldown
      B.stats.rallyTimeouts = (B.stats.rallyTimeouts or 0) + 1
      return true
    end
    return done
  end
end

local function wrapRallyCheck(original)
  return function(team, ...)
    local blocked = B.rallyBlocked[team]
    if blocked ~= nil and matchTime() < blocked then return false, kObjIDNone, 0 end
    return original(team, ...)
  end
end

local function wrapGather(original)
  return function(bot, ...)
    local a, b, c, d, e, f = original(bot, ...)
    if a then
      local team = nGameObject.GetTeam(bot)
      local para = nDirector and nDirector.GetParasiteID and nDirector.GetParasiteID() or kObjIDNone
      if not parasiteStands(team, para) then return false end
    end
    return a, b, c, d, e, f
  end
end

function B.ApplyParasiteFixup()
  local f = B.fixups
  if f.parasite then return end
  local names = { 'condition_Mission_RallyForParasite', 'condition_Mission_KillParasite',
                  'condition_Mission_RallyForParasiteContest', 'condition_Bot_GatherForParasite',
                  'condition_Bot_GatherForParasiteContest' }
  local t = {}
  for _, n in ipairs(names) do
    t[n] = global(n)
    if type(t[n]) ~= 'table' then return end
  end
  local END, CHECK = nConditionFns.CheckEnd, nConditionFns.Check
  -- the rally points (-36,-6) / (36,6) lie 33 from the parasite, inside its reach (27 on sight, 40 kept) once the bots pace
  -- 5-15 around them; the mission, the gather count and the Hunter's rule all read these fields, so one move does it
  if B.rallyPoint ~= false then
    local rp = B.rallyPoint or { mortal = { -66, -10 }, spirit = { 66, 10 } }
    for _, n in ipairs({ 'condition_Mission_RallyForParasite', 'condition_Bot_GatherForParasite' }) do
      t[n].mortalRally = vec(rp.mortal[1], rp.mortal[2], 0)
      t[n].spiritRally = vec(rp.spirit[1], rp.spirit[2], 0)
    end
  end
  local rally = t.condition_Mission_RallyForParasite
  rally[END] = wrapRallyEnd(wrapParasiteEnd(rally[END]))
  rally[CHECK] = wrapRallyCheck(rally[CHECK])
  for _, n in ipairs({ 'condition_Mission_KillParasite', 'condition_Mission_RallyForParasiteContest' }) do
    t[n][END] = wrapParasiteEnd(t[n][END])
  end
  for _, n in ipairs({ 'condition_Bot_GatherForParasite', 'condition_Bot_GatherForParasiteContest' }) do
    t[n][1] = wrapGather(t[n][1])
  end
  f.parasite = true
end

-- BOTCOMP's block (below) defines B.ApplyCompositionFixup and B.CompositionTick before this wrapper is called.
local mainMissionTick = B.MissionTick
function B.MissionTick(names)
  if not (B.fixups.veryOutnumbered and B.fixups.groupInLane and B.fixups.bailOnKill and B.fixups.parasite and
          B.fixups.campRadius and B.fixups.composition and B.fixups.lane and B.fixups.team) then
    B.ApplyFixups()
  end
  local checks, ends, errors = mainMissionTick(names)
  if B.fixups.composition and B.compositionWanted == true then
    local ok, more = pcall(B.CompositionTick)
    if not ok then
      errors = (errors or 0) + 1
      noteError('CompositionTick: ' .. tostring(more))
    else
      ends = (ends or 0) + (more or 0)
    end
  end
  -- BOTTEAM: the team's call, after the missions' own Checks and ends
  if B.fixups.team and B.teamWanted == true and type(B.TeamTick) == 'function' then
    local ok, err = pcall(B.TeamTick)
    if not ok then
      errors = (errors or 0) + 1
      noteError('TeamTick: ' .. tostring(err))
    end
  end
  return checks, ends, errors
end

-- ---- BOTCOMP: the preferred 5v5 setup's role rules ----
-- The host composes each team (2 Gladiators, Tactician, Predator, Hunter; gms/core/bots/composition.h) and exposes a
-- bot's mRole as nBotHost.BotRole (0 Gladiator, 1 Tactician, 2 Hunter, 3 Predator). What the setup needs beyond the
-- scripts, all [assumed rules]:
--   a. Lanes: ability_Bot_GroupInLane (Creature's Push/BackToAction phases, the Sculptor's) no longer rebalances lanes
--      (see groupInLaneTick), so the Gladiator of each lane stays in it.
--   b. Predator (the BotClass of PC_Warden with mRole 3): the Warden's AI has the Well plan (missionType 3/4:
--      MoveToMissionTarget, WellFight) and nothing accepts HarassWell (12). nBot.GetAvailableMission(predator,
--      AttackWell) answers HarassWell instead (range ignored) while his team outnumbers the enemy in his lane, and
--      nothing else (he never takes a capture mission); B.CompositionTick ends his harass mission when his team has not
--      outnumbered the enemy for harassHold seconds, which sends him back to Main and his lane (`!Bot_IsMissionActive`).
--      The mission: HarassWell opens above harassStartWorkers (the script's 16) workers and ends below
--      harassEndWorkers (the script's 4) or when the well is his team's (not, as the script has it, when the well is
--      unlocked). In the Well plan his basic attack goes to the nearest hostile worker (condition_nearest_minion,
--      condition_Bot_IsNearMissionTarget).
--   c. Hunter (PC_Artificer): his AIDefinition has no parasite plans (Farm, Shop, Flee, Gank only). The parasite is made
--      a camp: while B.HunterParasitePos says so, nDirector.GetClosestCampPosition answers the parasite's position for
--      the Hunter, so Farm walks him there (MoveToNextCamp) and ClearCamp's `basic` on condition_nearest_objective hits
--      it. Since BOTTEAM that is the team's call: he goes when his team takes the parasite and the group has gathered
--      (the kill phase) or contests it with allies at it (B.TeamWantsHunterOnParasite); never alone. Outside that
--      condition_nearest_objective does not offer the parasite to a Hunter (a camp near it would draw him in), and
--      condition_Bot_ShouldGank is false for him unless hunterGank (the Gank plan has no way back to Farm).
B.compositionConfig = B.compositionConfig or { harassHold = 4, laneRadius = 60 }
B.harass = B.harass or {}         -- predator -> { last = match time his team last outnumbered the enemy }

local ROLE_HUNTER, ROLE_PREDATOR = 2, 3
local function roleOf(bot)
  local f = hostfn('BotRole')
  if not f or isNone(bot) then return -1 end
  return num(f(bot), -1)
end
B.roleOf = roleOf

-- living allied shapers other than `bot` within r of pos, and living enemy shapers within r of pos
local function alliesAround(bot, team, pos, r)
  local n = 0
  for _, o in ipairs(query(pos, r, nGetObjectsSortType.None, nTargetType.AllyPlayers, team, nil, 0)) do
    if o ~= bot and nGameObject.IsAlive(o) == true then n = n + 1 end
  end
  return n
end
local function enemiesAround(team, pos, r)
  local n = 0
  for _, o in ipairs(nBot.GetEnemyPlayersNearPosition(team, pos, r)) do
    if nGameObject.IsAlive(o) == true then n = n + 1 end
  end
  return n
end

-- The Predator's rule, pure: his team outnumbers the enemy in his lane when the allies there (not counting him: he is
-- there) are at least the enemies, and there is at least one. B.PredatorOutnumbering gathers the counts.
function B.OutnumbersInLane(alliesWithoutSelf, enemies)
  return alliesWithoutSelf >= 1 and alliesWithoutSelf >= enemies
end

function B.PredatorOutnumbering(bot)
  local team = nGameObject.GetTeam(bot)
  local lane = nBot.GetLaneID(bot)
  if lane ~= nLaneID.Top and lane ~= nLaneID.Bottom then lane = nBot.GetClosestLane(bot) end
  local c = B.LaneCenter(lane, team)
  local r = B.compositionConfig.laneRadius
  return B.OutnumbersInLane(alliesAround(bot, team, c, r), enemiesAround(team, c, r)), lane
end

-- The nearest hostile worker around a Predator on a harass trip (the target of his mission is an enemy well).
function B.HarassTarget(bot)
  local well = nBot.GetMissionTarget(bot)
  if isNone(well) or not exists(well) then return nil end
  local team = nGameObject.GetTeam(bot)
  if nGameObject.GetTeam(well) == team then return nil end
  for _, o in ipairs(nBot.GetNearbyObjects(bot, 30, nTargetType.EnemyNPCs, 0)) do
    if nGameObject.GetNPCType(o) == nNPCType.Follower and nBot.ValidateHostileBotTarget(team, o, false, true) then
      return o
    end
  end
  return nil
end

-- The parasite's position when `bot` (a Hunter) should go there now, else nil: only when his team's call takes him
-- (BOTTEAM: the parasite's kill phase, or a contest with allies at it).
function B.HunterParasitePos(bot)
  if roleOf(bot) ~= ROLE_HUNTER then return nil end
  local team = nGameObject.GetTeam(bot)
  local para = nDirector and nDirector.GetParasiteID and nDirector.GetParasiteID() or kObjIDNone
  if not parasiteStands(team, para) then return nil end
  if type(B.TeamWantsHunterOnParasite) == 'function' and B.TeamWantsHunterOnParasite(team, para) then
    return nGameObject.GetPosition(para)
  end
  return nil
end

-- B.CompositionTick (about once a second, after the mission tick): end the harass missions of Predators whose team has
-- not outnumbered the enemy in their lane for harassHold seconds. Returns the number of missions ended.
function B.CompositionTick()
  local openf, endf = hostfn('OpenMission'), hostfn('EndMission')
  local h = global('condition_Mission_HarassWell')
  if not (openf and endf and type(h) == 'table') then return 0 end
  local players = nPlayer.GetPlayerControlledObjects()
  if type(players) ~= 'table' then return 0 end
  local ended, t = 0, matchTime()
  for _, c in ipairs(players) do
    if exists(c) and roleOf(c) == ROLE_PREDATOR then
      local mid = nBot.GetMissionID(c)
      local harassing = false
      if mid > 0 then
        local team = nGameObject.GetTeam(c)
        for i = 1, 256 do
          local id, _, guid = openf(team, i)
          if id == nil then break end
          if id == mid and guid == h.conditionGUID then harassing = true break end
        end
      end
      if harassing then
        local st = B.harass[c]
        if st == nil then st = { last = t } B.harass[c] = st end
        local alive = nGameObject.IsAlive(c) == true
        if alive and B.PredatorOutnumbering(c) then
          st.last = t
        elseif not alive or t - st.last >= B.compositionConfig.harassHold then
          endf(mid)
          ended = ended + 1
          B.stats.harassEnds = (B.stats.harassEnds or 0) + 1
          B.harass[c] = nil
        end
      else
        B.harass[c] = nil
      end
    end
  end
  return ended
end

function B.ApplyCompositionFixup()
  local f = B.fixups
  if f.composition then return end
  if B.compositionWanted ~= true then f.composition = true return end
  local names = { 'condition_Mission_HarassWell', 'condition_nearest_minion', 'condition_Bot_IsNearMissionTarget',
                  'condition_nearest_objective', 'condition_Bot_ShouldGank', 'condition_Bot_NextCreepCamp',
                  'condition_Bot_IsNearNextCreepCamp' }
  local t = {}
  for _, n in ipairs(names) do
    t[n] = global(n)
    if type(t[n]) ~= 'table' then return end
  end
  if type(nBot.GetAvailableMission) ~= 'function' then return end
  local END = nConditionFns.CheckEnd
  -- b. HarassWell: the thresholds and the end rule
  local hw = t.condition_Mission_HarassWell
  hw.startWorkers = B.harassStartWorkers or hw.startWorkers
  hw.endWorkers = B.harassEndWorkers or hw.endWorkers
  hw[END] = function(team, well)
    if isNone(well) or not exists(well) then return true end
    if nGameObject.GetTeam(well) == team then return true end
    return nBattlegrounds.GetFollowersForTitan(well) < hw.endWorkers
  end
  local getMission = nBot.GetAvailableMission
  nBot.GetAvailableMission = function(bot, missionType, range)
    if missionType == nMissionType.AttackWell and roleOf(bot) == ROLE_PREDATOR then
      if B.PredatorOutnumbering(bot) then return getMission(bot, nMissionType.HarassWell, 0) end
      return 0
    end
    return getMission(bot, missionType, range)
  end
  local nearestMinion = t.condition_nearest_minion[1]
  t.condition_nearest_minion[1] = function(bot, ...)
    if roleOf(bot) == ROLE_PREDATOR then
      local w = B.HarassTarget(bot)
      if w ~= nil then return true, 0, 0, 0, w end
    end
    return nearestMinion(bot, ...)
  end
  local nearMission = t.condition_Bot_IsNearMissionTarget[1]
  t.condition_Bot_IsNearMissionTarget[1] = function(bot, ...)
    if roleOf(bot) == ROLE_PREDATOR and B.HarassTarget(bot) ~= nil then return true end
    return nearMission(bot, ...)
  end
  -- c. Hunter
  local nearestObjective = t.condition_nearest_objective[1]
  t.condition_nearest_objective[1] = function(bot, ...)
    local a, b, c, d, e = nearestObjective(bot, ...)
    if a and roleOf(bot) == ROLE_HUNTER and nDirector and nDirector.GetParasiteID and e == nDirector.GetParasiteID() and
        B.HunterParasitePos(bot) == nil then
      return false
    end
    return a, b, c, d, e
  end
  local shouldGank = t.condition_Bot_ShouldGank[1]
  t.condition_Bot_ShouldGank[1] = function(bot, ...)
    if B.hunterGank ~= true and roleOf(bot) == ROLE_HUNTER then return false end
    return shouldGank(bot, ...)
  end
  for _, n in ipairs({ 'condition_Bot_NextCreepCamp', 'condition_Bot_IsNearNextCreepCamp' }) do
    local original = t[n][1]
    t[n][1] = function(bot, ...)
      B.campAsker = bot
      local ok, a, b, c, d, e, g = pcall(original, bot, ...)
      B.campAsker = nil
      if not ok then error(a, 0) end
      return a, b, c, d, e, g
    end
  end
  -- the Hunter leaves the base at B.hunterStartS (40 s) instead of the plans' 65: he reaches his first camp at about 1 minute
  -- (the others keep the original start; the Idle plan's link passes idleTime 65 as the second argument)
  B.hunterStartS = B.hunterStartS or 40
  local gameStart = global('condition_Bot_GameStart')
  if type(gameStart) == 'table' and type(gameStart[nConditionFns.Check]) == 'function' then
    local startCheck = gameStart[nConditionFns.Check]
    gameStart[nConditionFns.Check] = function(bot, idleTime, ...)
      if roleOf(bot) == ROLE_HUNTER and type(idleTime) == 'number' and idleTime > B.hunterStartS then
        idleTime = B.hunterStartS
      end
      return startCheck(bot, idleTime, ...)
    end
  end
  if nDirector and type(nDirector.GetClosestCampPosition) == 'function' then
    local closest = nDirector.GetClosestCampPosition
    nDirector.GetClosestCampPosition = function(team, pos, ignoreFoW)
      local bot = B.campAsker
      if bot ~= nil then
        local p = B.HunterParasitePos(bot)
        if p ~= nil then
          -- condition_Bot_NextCreepCamp treats a zero coordinate as "no camp"
          return vec(p.x ~= 0 and p.x or 0.01, p.y ~= 0 and p.y or 0.01, p.z ~= 0 and p.z or 0.01)
        end
      end
      return closest(team, pos, ignoreFoW)
    end
  end
  f.composition = true
end

-- ---- BOTLANE: Gladiators stay in their lane, finish towers, group to siege the opened lane (fix-up 7) ---------------------
-- The user's report (2026-10-10): Gladiators leave their lane too much (the Main plan's links to Gank, Wells, Rally and
-- Contest/KillParasite missions come before the lane phases), do not finish off a tower, and when a lane's towers are all
-- down the bots never group up to push into the base.
--  a. (gone since BOTTEAM) The per-role mission blocks (Gladiators no Gank/Wells/parasite, Tacticians and Predators no
--     parasite missions): who is offered which mission is now the team's call (B.TeamOffer, *BOTTEAM* below).
--  b. The siege lane. condition_Bot_TimeToRally fires when an enemy lane has <= 1 tower standing, but the three siege
--     scripts (ability_Bot_AssignToSiegeLane, condition_Bot_AssignedToSiegeLane, condition_Bot_GroupedWithAllies) pick
--     the lane with MORE standing towers [GetNumTowers counted standing: the scripts' reading is unknown], i.e. the lane
--     that is not open. While they run, GetNumTowers answers 100 - n, so they pick the opened lane.
--  c. condition_Bot_TowerPush needs more than two of the bot's minions within 27 of the tower; it also passes when the
--     tower is at B.finishTowerHp (0.5) or less, the bot has at least half its HP and no enemy shaper stands within 35
--     of the tower, so a tower the minions have worn down is finished.
--  d. condition_Bot_InEnemyBase's radius 65 -> B.siegeBaseRadius (85): the BaseSiege plan starts from the lane's last
--     tower position.
B.finishTowerHp = B.finishTowerHp or 0.5
B.siegeBaseRadius = B.siegeBaseRadius or 85
B.inSiege = false

-- ward spots this close to the map's centre (the parasite's ground; (21,-38) and (-21,38) are 43 away) are not walked to
B.wardCenterRadius = B.wardCenterRadius or 60

local function inSiegeCall(original)
  return function(...)
    local prev = B.inSiege
    B.inSiege = true
    local r = { pcall(original, ...) }
    B.inSiege = prev
    if not r[1] then error(r[2], 0) end
    return unpack(r, 2)
  end
end

function B.ApplyLaneFixup()
  local f = B.fixups
  if f.lane then return end
  if B.laneWanted == false then f.lane = true return end
  local tp = global('condition_Bot_TowerPush')
  local assigned = global('condition_Bot_AssignedToSiegeLane')
  local grouped = global('condition_Bot_GroupedWithAllies')
  local assign = global('ability_Bot_AssignToSiegeLane')
  local inBase = global('condition_Bot_InEnemyBase')
  if not (type(tp) == 'table' and type(assigned) == 'table' and type(grouped) == 'table' and type(assign) == 'table' and
          type(inBase) == 'table') then
    return
  end
  if type(nBot.GetAvailableMission) ~= 'function' or type(nBattlegrounds.GetNumTowers) ~= 'function' or
      type(nAbilityFns) ~= 'table' then
    return
  end
  -- b. the siege lane is the opened one
  local numTowers = nBattlegrounds.GetNumTowers
  nBattlegrounds.GetNumTowers = function(team, lane)
    local n = numTowers(team, lane)
    if B.inSiege then return 100 - n end
    return n
  end
  assigned[1] = inSiegeCall(assigned[1])
  grouped[1] = inSiegeCall(grouped[1])
  local tick = nAbilityFns.Tick
  if type(assign[tick]) == 'function' then assign[tick] = inSiegeCall(assign[tick]) end
  -- c. finish a worn-down tower
  local towerPush = tp[1]
  tp[1] = function(bot, ...)
    local a, b, c, d, e = towerPush(bot, ...)
    if a then return a, b, c, d, e end
    if type(nBotUtils) ~= 'table' or type(nBotUtils.IsEnemyTowerNear) ~= 'function' then return a, b, c, d, e end
    local near, tower = nBotUtils.IsEnemyTowerNear(bot, tp.searchRadius or 60)
    if near and not isNone(tower) and standing(tower) then
      local hp, mx = nGameObject.GetHitPoints(tower), nGameObject.GetMaxHitPoints(tower)
      if mx and mx > 0 and hp / mx <= B.finishTowerHp and hpFraction(bot) >= 0.5 and
          enemiesAround(nGameObject.GetTeam(bot), nGameObject.GetPosition(tower), 35) == 0 then
        B.stats.towerFinishes = (B.stats.towerFinishes or 0) + 1
        return true, 0, 0, 0, kObjIDNone
      end
    end
    return a, b, c, d, e
  end
  -- d. the base
  inBase.defaultSearchRadius = B.siegeBaseRadius
  -- f. a kill is not chased into the parasite unless the team is there for it (BOTTEAM: the parasite's kill phase or a
  -- contest). EngageForKill is refused for a target standing within B.parasiteChaseRadius of the parasite, and an engaged
  -- bot whose target went there bails (BailOnKill)
  B.parasiteChaseRadius = B.parasiteChaseRadius or 45
  local function nearParasite(bot, target)
    if isNone(target) or not exists(target) then return false end
    local team = nGameObject.GetTeam(bot)
    local para = nDirector and nDirector.GetParasiteID and nDirector.GetParasiteID() or kObjIDNone
    if isNone(para) or not parasiteStands(team, para) then return false end
    if type(B.TeamAtParasite) == 'function' and B.TeamAtParasite(team) then return false end
    local a, b = nGameObject.GetPosition(para), nGameObject.GetPosition(target)
    return math.sqrt((a.x - b.x) ^ 2 + (a.y - b.y) ^ 2) <= B.parasiteChaseRadius
  end
  local engage = global('condition_Bot_EngageForKill')
  if type(engage) == 'table' and type(engage[1]) == 'function' then
    local engageCond = engage[1]
    engage[1] = function(bot, ...)
      local a, x, y, z, t, u = engageCond(bot, ...)
      if a and nearParasite(bot, t) then
        B.stats.parasiteChaseRefused = (B.stats.parasiteChaseRefused or 0) + 1
        return false
      end
      return a, x, y, z, t, u
    end
  end
  local bail = global('condition_Bot_BailOnKill')
  if type(bail) == 'table' and type(bail[1]) == 'function' and type(nGameObject.GetTargetID) == 'function' then
    local bailCond = bail[1]
    bail[1] = function(bot, ...)
      if nearParasite(bot, nGameObject.GetTargetID(bot)) then return true end
      return bailCond(bot, ...)
    end
  end
  -- e. wards: a Tactician or Predator in the first minutes walked to the ward spots in the middle and died there; the
  -- condition offers the nearest free spot, so a middle one means no ward trip
  local ward = global('condition_Bot_Ward')
  if type(ward) == 'table' and type(ward[1]) == 'function' then
    local wardCond = ward[1]
    ward[1] = function(bot, ...)
      local a, x, y, z, t, u = wardCond(bot, ...)
      if a and roleOf(bot) == 0 and B.gladiatorWards ~= true then  -- a Gladiator stays in his lane
        B.stats.wardGladiatorRefused = (B.stats.wardGladiatorRefused or 0) + 1
        return false
      end
      if a and roleOf(bot) ~= ROLE_HUNTER and math.sqrt((x or 0) * (x or 0) + (y or 0) * (y or 0)) < B.wardCenterRadius then
        B.stats.wardCenterRefused = (B.stats.wardCenterRefused or 0) + 1
        return false
      end
      return a, x, y, z, t, u
    end
  end
  f.lane = true
end

-- ---- BOTTEAM: the team's call (fix-up 8, [host] bot_team) ----------------------------------------------------------
-- The user (2026-10-11): the bots should feel like a team of humans: the parasite only together, wells taken, a push for
-- the win when there is an opening (enemies dead, a fight won). The bots' own data has the pieces (missions with group
-- sizes, Rally -> gather -> KillParasite, ContestParasite, CaptureWell/DefendWell, LateGameRally -> siege), but nothing
-- decides for the team: every bot takes any mission it is offered. This layer is the shot-caller. Once a second per team
-- (after the mission tick) it takes a snapshot, decides one call and carries it out through the data's own machinery:
--   defend    the base is attacked (a DefendBase/DefendCore mission is open): nothing else; the data's defence missions.
--   push      an opening: more of us alive than of them (their dead counted while their respawn is >= longRespawn s
--             away), two of theirs down, our parasite buff, or a lane of theirs open with even numbers. Everyone but the
--             Hunter groups in one lane (condition_Bot_TimeToRally answers true, the siege scripts take our lane) and
--             pushes with the wave (TowerPush, BaseSiege). Ends when the advantage is gone, after pushMax s, or when no
--             structure fell for pushStall s; the bots go back to their own lanes.
--   contest   the enemy is at the parasite (seen there in the last contestKnownWindow s) and our force (the healthy bots,
--             the healthy humans near it) is bigger: a ContestParasite mission for all (gather at the contest
--             point, then KillParasite), the Hunter when allies are at it.
--   parasite  an opening with the parasite nearer than the push, a calm moment from parasiteCalmFrom s (healthy, even, no
--             enemy seen near it, no bot in a fight), or a human ally fighting it: a Rally mission at the rally point for every bot; once
--             gatherNeed are there the kill phase opens KillParasite to everyone and the Hunter joins. Aborts (a retreat
--             ping) when they outnumber us at it, the group falls apart, or the gather takes longer than gatherMax s.
--   well      wells unlocked, even numbers and enough of us alive: two bots (Predator and Tacticians first, a Gladiator
--             to make up the pair, the Hunter has no well plan) take the unguarded enemy well nearest to them. Ends
--             when it is ours, when they guard it with more, or after wellMax s.
--   farm      the default: lanes, the Hunter's camps; Tacticians and Predators answer DefendTower (the Gank plan) and
--             DefendWell; the Predator harasses wells (BOTCOMP).
-- Offers (B.TeamOffer over nBot.GetAvailableMission): base defence (6, 10) and joining a nearby kill (8) always; Rally
-- (11) only in a parasite call, KillParasite (7) in its kill phase or a contest, ContestParasite (13) in a contest,
-- AttackWell (3) to the well call's bots (the target's mission, any distance), DefendTower (2) and DefendWell (4) to
-- Tacticians and Predators while farming. The original Rally and Contest Checks open nothing while the layer is on (it
-- opens its own, ended with the call), KillParasite's Check only in its kill phase or a contest. The bots ping each call
-- to their team (MiniMapPing: "on my way" to a gather point, "attention" on the parasite or the tower, "retreat").
-- A team facing a first-time player (FTUE, nBot.IsEnemyOfFirstTimePlayer) only farms, defends and takes wells, as the
-- data's !Bot_IsEnemyFTUE links want; a team whose human lags 6+ levels behind its bots (Bot_CheeseDetected) makes no
-- parasite call, one with 3+ humans (Bot_MoreHumanAlliesThanBots) neither a gather nor a push: the links refuse then. All thresholds are [assumed].
B.teamConfig = B.teamConfig or {
  longRespawn = 12,        -- an enemy whose respawn is at least this far counts as down
  pushNumbers = 2,         -- allies alive minus enemies up for an opening
  pushMinAllies = 3,
  pushHold = 6,            -- the advantage may be gone this long before the push ends
  pushMax = 150, pushStall = 75, pushCooldown = 45,
  pushStallCooldown = 120, -- after a stalled push (an inner Binding that does not fall: two pushes there in a test match)
  lateSiegeHp = 0.6,       -- average HP of the living for the "lane open" push
  parasiteCalmFrom = 480,  -- the calm take from this match second
  parasiteCalmAllies = 4,  -- healthy allies alive for it
  parasiteSeenRange = 70, parasiteSeenWindow = 15,
  gatherNeed = 3, gatherRange = 25, gatherMax = 60,
  parasiteMax = 120, parasiteCooldown = 60,
  contestRange = 35, contestMax = 60, contestCooldown = 30, contestKnownWindow = 10, contestForceRange = 100,
  fightRange = 40,         -- a bot with a visible enemy shaper this close is fighting (no calm call then)
  wellMinAllies = 4, wellSafeRange = 40, wellMax = 100, wellCooldown = 60, wellGladiatorsFrom = 1500, wellBots = 2,
  defendHold = 8,
  healthy = 0.5,           -- HP fraction of a healthy ally
  pingGap = 6,
}
B.teamState = B.teamState or {}  -- team -> { call, since, phase, phaseSince, lane, target, well, assigned, cool, ... }
B.stats.team = B.stats.team or { calls = {}, aborts = 0, pings = 0, ticks = 0 }

local function teamState(team)
  local st = B.teamState[team]
  if st == nil then
    st = { call = 'farm', since = 0, phase = nil, phaseSince = 0, cool = {}, homeLane = {}, assigned = {}, lastPing = -1000 }
    B.teamState[team] = st
  end
  return st
end
B.TeamStateOf = teamState

local function teamOn() return B.teamWanted == true end

local function parasiteId()
  return nDirector and nDirector.GetParasiteID and nDirector.GetParasiteID() or kObjIDNone
end

local function rallyPoint(team)
  local c = global('condition_Bot_GatherForParasite')
  if team == nTeamID.Mortal then return (c and c.mortalRally) or vec(-66, -10, 0) end
  return (c and c.spiritRally) or vec(66, 10, 0)
end
local function contestPoint(team)
  local c = global('condition_Mission_RallyForParasiteContest')
  if team == nTeamID.Mortal then return (c and c.mortalRally) or vec(-66, -8, 0) end
  return (c and c.spiritRally) or vec(66, 8, 0)
end

local function spid(name)
  if nUtil and nUtil.SPID then return nUtil.SPID(name) end
  return name
end
local TEAM_RALLY_GUID = spid('dgsim_team_rally')
local TEAM_CONTEST_GUID = spid('dgsim_team_contest')
local MAJOR_BUFF = spid('modifier_Parasite_MajorBuff')
local MINOR_BUFF = spid('modifier_Parasite_MinorBuff')

local function hasParasiteBuff(c)
  if type(nModifier) ~= 'table' or type(nModifier.AgentHasModifierMatchingGUID) ~= 'function' then return false end
  return nModifier.AgentHasModifierMatchingGUID(c, MAJOR_BUFF) == true or
         nModifier.AgentHasModifierMatchingGUID(c, MINOR_BUFF) == true
end

local function playerIndexOf(c)
  if type(nPlayer.GetPlayerIdForObject) ~= 'function' then return nil end
  return tonumber(nPlayer.GetPlayerIdForObject(c))
end

local function meanDistance(bots, pos)
  local sum, n = 0, 0
  for _, b in ipairs(bots) do
    if b.alive then sum, n = sum + dist(b.pos, pos), n + 1 end
  end
  if n == 0 then return 1e6 end
  return sum / n
end

-- What a team knows once a second (B.TeamDecide reads only this and its state). Who is alive and how long a dead enemy
-- stays dead are known (the scoreboard shows it); enemy positions only as seen (B.RememberEnemies).
function B.TeamSnapshot(team)
  local cfg = B.teamConfig
  local t = matchTime()
  local enemy = enemyOf(team)
  local s = { t = t, team = team, enemy = enemy, bots = {}, alliesAlive = 0, alliesHealthy = 0, enemiesAlive = 0,
              enemiesUp = 0, enemiesDown = 0, humans = 0, hpSum = 0, buff = 0, ftue = false }
  local respawnIn = hostfn('RespawnIn')
  local mem = B.RememberEnemies(team)
  local players = nPlayer.GetPlayerControlledObjects()
  if type(players) ~= 'table' then players = {} end
  local nAllies, nEnemies, levelAllies, levelEnemies = 0, 0, 0, 0
  local minHumanLevel, maxBotLevel = nil, 0
  for _, c in ipairs(players) do
    if exists(c) then
      local ct = nGameObject.GetTeam(c)
      local alive = nGameObject.IsAlive(c) == true
      if ct == team then
        nAllies = nAllies + 1
        levelAllies = levelAllies + num(nGameObject.GetPlayerLevel(c), 1)
        local bot = nPlayer.IsBot(c) == true
        local level = num(nGameObject.GetPlayerLevel(c), 1)
        if not bot then
          s.humans = s.humans + 1
          if minHumanLevel == nil or level < minHumanLevel then minHumanLevel = level end
        elseif level > maxBotLevel then
          maxBotLevel = level
        end
        if alive then
          local hp = hpFraction(c)
          s.alliesAlive = s.alliesAlive + 1
          s.hpSum = s.hpSum + hp
          if hp >= cfg.healthy then s.alliesHealthy = s.alliesHealthy + 1 end
          if hasParasiteBuff(c) then s.buff = s.buff + 1 end
        end
        if bot then
          s.bots[#s.bots + 1] = { id = c, role = roleOf(c), alive = alive, hp = alive and hpFraction(c) or 0,
                                  pos = nGameObject.GetPosition(c) }
          if not s.ftue and nBot.IsEnemyOfFirstTimePlayer(c) then s.ftue = true end
        end
      elseif ct == enemy then
        nEnemies = nEnemies + 1
        levelEnemies = levelEnemies + num(nGameObject.GetPlayerLevel(c), 1)
        if alive then
          s.enemiesAlive = s.enemiesAlive + 1
          s.enemiesUp = s.enemiesUp + 1
        else
          local idx = playerIndexOf(c)
          local left = cfg.longRespawn
          if respawnIn and idx ~= nil then left = num(respawnIn(idx), 0) end
          if left >= cfg.longRespawn then s.enemiesDown = s.enemiesDown + 1 else s.enemiesUp = s.enemiesUp + 1 end
        end
      end
    end
  end
  s.hpAvg = 0
  if s.alliesAlive > 0 then s.hpAvg = s.hpSum / s.alliesAlive end
  -- the data's own vetoes on the parasite and siege links: condition_Bot_CheeseDetected (a bot more than 5 levels above
  -- the team's lowest human: an AFK human is not carried to objectives; in a test match team 1's gathers drew nobody) and
  -- condition_Bot_MoreHumanAlliesThanBots (3+ humans on the team lead themselves)
  local cheese = global('condition_Bot_CheeseDetected')
  local gap = (type(cheese) == 'table' and tonumber(cheese.maxLevelGap)) or 5
  s.cheese = minHumanLevel ~= nil and maxBotLevel > minHumanLevel + gap
  s.manyHumans = s.humans >= 3
  -- bots in a fight: a visible enemy shaper within fightRange (a calm call waits for the lanes to settle)
  s.botsFighting = 0
  for _, b in ipairs(s.bots) do
    if b.alive then
      for _, o in ipairs(nBot.GetEnemyPlayersNearPosition(team, b.pos, cfg.fightRange, 0, true)) do
        if nGameObject.IsAlive(o) == true then
          s.botsFighting = s.botsFighting + 1
          break
        end
      end
    end
  end
  s.levelDiff = 0
  if nAllies > 0 and nEnemies > 0 then s.levelDiff = levelAllies / nAllies - levelEnemies / nEnemies end
  -- lanes and structures
  local towersTop = nBattlegrounds.GetNumTowers(enemy, nLaneID.Top)
  local towersBot = nBattlegrounds.GetNumTowers(enemy, nLaneID.Bottom)
  s.enemyLaneOpen = towersTop <= 1 or towersBot <= 1
  s.enemyStructures = 0
  for _, x in ipairs(B.Scan().structures) do
    if nGameObject.GetTeam(x.id) == enemy and standing(x.id) then s.enemyStructures = s.enemyStructures + 1 end
  end
  -- the push lane: the one with fewer towers of theirs; a tie goes to the lane most of our living bots are in
  local lane
  if towersTop ~= towersBot then
    if towersTop < towersBot then lane = nLaneID.Top else lane = nLaneID.Bottom end
  else
    local top, bottom = 0, 0
    for _, b in ipairs(s.bots) do
      if b.alive then
        if laneOfPoint(b.pos) == nLaneID.Top then top = top + 1 else bottom = bottom + 1 end
      end
    end
    if top > bottom then lane = nLaneID.Top else lane = nLaneID.Bottom end
  end
  s.pushLane = lane
  s.pushTarget = nBattlegrounds.GetOuterTower(enemy, lane)
  if isNone(s.pushTarget) then
    for _, b in ipairs(s.bots) do
      if b.alive then
        s.pushTarget = nBattlegrounds.GetClosestBaseStructure(b.id)
        break
      end
    end
  end
  s.pushDistance = 1e6
  if exists(s.pushTarget) then s.pushDistance = meanDistance(s.bots, nGameObject.GetPosition(s.pushTarget)) end
  -- the parasite
  local para = parasiteId()
  local p = { id = para, stands = parasiteStands(team, para), alliesNear = 0, enemiesNear = 0, humanOnIt = false,
              inCombat = false, seenRecently = false, gathered = 0, botDistance = 1e6, force = 0, enemiesKnown = 0 }
  p.rally = rallyPoint(team)
  for _, c in ipairs(players) do
    if exists(c) and nGameObject.GetTeam(c) == team and nGameObject.IsAlive(c) == true and
        dist(nGameObject.GetPosition(c), p.rally) <= cfg.gatherRange then
      p.gathered = p.gathered + 1
    end
  end
  if p.stands then
    p.pos = nGameObject.GetPosition(para)
    p.inCombat = nGameObject.IsInCombat(para) == true
    for _, c in ipairs(players) do
      if exists(c) and nGameObject.IsAlive(c) == true then
        local d = dist(nGameObject.GetPosition(c), p.pos)
        local ct = nGameObject.GetTeam(c)
        if ct == team and d <= cfg.contestRange then
          p.alliesNear = p.alliesNear + 1
          if p.inCombat and nPlayer.IsBot(c) ~= true then p.humanOnIt = true end
        elseif ct == enemy and d <= cfg.contestRange and nGameObject.IsVisibleToTeam(c, team) then
          p.enemiesNear = p.enemiesNear + 1
        end
      end
    end
    -- the enemies known at it: seen there now, or within the last contestKnownWindow s (the fog hides the rest of a group)
    p.enemiesKnown = p.enemiesNear
    local tnow = now()
    for id, rec in pairs(mem.enemies) do
      if tnow - rec.t <= cfg.parasiteSeenWindow and dist(rec, p.pos) <= cfg.parasiteSeenRange and standing(id) then
        p.seenRecently = true
      end
      if tnow - rec.t <= cfg.contestKnownWindow and dist(rec, p.pos) <= cfg.contestRange + 10 and standing(id) and
          not (nGameObject.IsVisibleToTeam(id, team) and dist(nGameObject.GetPosition(id), p.pos) <= cfg.contestRange) then
        p.enemiesKnown = p.enemiesKnown + 1
      end
    end
    -- our force for it: the healthy bots (they come), the healthy humans only when near (they may not)
    for _, c in ipairs(players) do
      if exists(c) and nGameObject.GetTeam(c) == team and nGameObject.IsAlive(c) == true and hpFraction(c) >= cfg.healthy and
          (nPlayer.IsBot(c) == true or dist(nGameObject.GetPosition(c), p.pos) <= cfg.contestForceRange) then
        p.force = p.force + 1
      end
    end
    p.botDistance = meanDistance(s.bots, p.pos)
  end
  s.parasite = p
  -- wells: the enemy's unlocked ones (CaptureWell's missions), with the enemy shapers seen at each
  s.wells = {}
  for _, w in ipairs(nBattlegrounds.GetMinesForTeam(enemy, true)) do
    if exists(w) then
      local wp = nGameObject.GetPosition(w)
      local guards = 0  -- the enemy shapers the team sees there (no look through the fog)
      for _, o in ipairs(nBot.GetEnemyPlayersNearPosition(team, wp, cfg.wellSafeRange, 0, true)) do
        if nGameObject.IsAlive(o) == true then guards = guards + 1 end
      end
      s.wells[#s.wells + 1] = { id = w, pos = wp, guards = guards }
    end
  end
  -- the base: the data's defence missions (condition_Mission_DefendTower opens them for a hit nub or Guardian)
  -- (not in the first 30 s: the DefendTower Check opens DefendCore on every core at the start, GetLastCombatTime < 0.5,
  -- and closes them again by itself, at 0.4-15.7 s in a test match)
  s.baseThreat = t >= 30 and (nBot.IsMissionAvailable(team, nMissionType.DefendBase) or
                              nBot.IsMissionAvailable(team, nMissionType.DefendCore))
  return s
end

local function cooling(st, call, t) return st.cool[call] ~= nil and t < st.cool[call] end

-- An opening, and why: their effective numbers (the dead with a long respawn don't count), two of theirs down, our
-- parasite buff, a lane of theirs open. False with fewer than pushMinAllies of us alive.
function B.TeamOpening(s, cfg)
  if s.alliesAlive < cfg.pushMinAllies then return false end
  local numbers = s.alliesAlive - s.enemiesUp
  if numbers >= cfg.pushNumbers then return true, string.format('%d of us up against %d', s.alliesAlive, s.enemiesUp) end
  if s.enemiesDown >= 2 and numbers >= 1 then return true, string.format('%d of theirs down', s.enemiesDown) end
  if s.buff >= 3 and numbers >= 0 and s.hpAvg >= 0.5 then return true, 'parasite buff on ' .. s.buff end
  if s.enemyLaneOpen and numbers >= 0 and s.alliesAlive >= 4 and s.hpAvg >= cfg.lateSiegeHp and s.levelDiff >= 0 then
    return true, 'a lane of theirs is open'
  end
  return false
end

-- The bots for a well: alive and healthy, a well plan (not the Hunter); the Predator first, then the Tacticians, a
-- Gladiator only to make up the crew before wellGladiatorsFrom (a lone Tactician was chased off in a test match), nearest
-- first. A crew smaller than wellBots does not go.
local function wellCrew(s, well, cfg)
  local cands = {}
  for _, b in ipairs(s.bots) do
    if b.alive and b.hp >= cfg.healthy and b.role ~= ROLE_HUNTER then
      local pref = 1
      if b.role == ROLE_PREDATOR then pref = 0 elseif b.role == 0 and s.t < cfg.wellGladiatorsFrom then pref = 2 end
      cands[#cands + 1] = { id = b.id, d = dist(b.pos, well.pos), pref = pref }
    end
  end
  table.sort(cands, function(a, b)
    if a.pref ~= b.pref then return a.pref < b.pref end
    return a.d < b.d
  end)
  local crew, n, d = {}, 0, 0
  for _, c in ipairs(cands) do
    if n >= cfg.wellBots then break end
    crew[c.id] = true
    n = n + 1
    d = d + c.d
  end
  if n > 0 then d = d / n end
  return crew, n, d
end

-- The call, pure: `s` a snapshot, `st` the team's state (read only), `cfg` the thresholds. Returns { call, phase, reason,
-- abort, lane, target, well, assigned }.
function B.TeamDecide(s, st, cfg)
  local t, cur = s.t, st.call or 'farm'
  local p = s.parasite or {}
  local function keep(reason)
    return { call = cur, phase = st.phase, lane = st.lane, target = st.target, well = st.well, assigned = st.assigned,
             reason = reason or 'holding' }
  end
  local function farm(reason, abort) return { call = 'farm', reason = reason, abort = abort == true } end
  -- 1. the base
  if s.baseThreat then return { call = 'defend', reason = 'the base is attacked' } end
  if cur == 'defend' then
    if t - (st.lastThreat or -1e6) < cfg.defendHold then return keep('the base was attacked') end
    cur = 'farm'
  end
  local numbers = s.alliesAlive - s.enemiesUp
  local soft = s.ftue == true
  local noParasite = soft or s.cheese == true                 -- the Rally/Contest/KillParasite links refuse then
  local noPush = soft or s.manyHumans == true                 -- the LateGameRally link refuses then
  local known = p.enemiesKnown or p.enemiesNear or 0
  local canContest = not noParasite and p.stands and p.inCombat and known >= 1 and s.alliesAlive >= 3 and
                     (p.force or 0) >= known + 1
  -- 2. a call under way: its end rules
  if cur == 'contest' then
    if not p.stands then return farm('the parasite is gone') end
    if (p.force or 0) < known or s.alliesAlive < 2 or (p.alliesNear >= 2 and p.enemiesNear > p.alliesNear + 1) then
      return farm('they are more at the parasite', true)
    end
    if t - st.since > cfg.contestMax then return farm('the contest took too long', true) end
    if p.enemiesNear == 0 and not p.inCombat then
      if s.alliesHealthy >= cfg.gatherNeed then return { call = 'parasite', phase = 'kill', reason = 'they left: we take it' } end
      return farm('they left the parasite')
    end
    return keep('contesting')
  end
  if cur == 'parasite' then
    if not p.stands then return farm('the parasite is gone') end
    if p.enemiesNear >= 2 and p.enemiesNear > p.alliesNear then return farm('they outnumber us at the parasite', true) end
    if s.alliesHealthy < 2 then return farm('too few of us healthy', true) end
    if st.phase == 'gather' then
      local need = math.min(cfg.gatherNeed, math.max(s.alliesAlive, 1))
      if p.gathered >= need then return { call = 'parasite', phase = 'kill', reason = p.gathered .. ' gathered' } end
      if t - st.phaseSince > cfg.gatherMax then return farm('the gather took too long', true) end
      return keep('gathering ' .. p.gathered .. '/' .. need)
    end
    if t - st.phaseSince > cfg.parasiteMax then return farm('the parasite took too long', true) end
    return keep('on the parasite')
  end
  if cur == 'push' then
    if t - (st.lastAdvantage or st.since) > cfg.pushHold then return farm('the advantage is gone', true) end
    if t - st.since > cfg.pushMax then return farm('the push time is up') end
    if t - (st.lastProgress or st.since) > cfg.pushStall then
      local d = farm('the push stalled', true)
      d.stalled = true
      return d
    end
    return keep('pushing')
  end
  -- 3. a new call: a contest, then an opening (the parasite or a push, whichever is nearer), the calm parasite, a well
  if canContest and not cooling(st, 'contest', t) then
    return { call = 'contest', reason = string.format('%d of theirs at the parasite (%d seen now), our force %d', known,
                                                      p.enemiesNear, p.force or 0) }
  end
  local wantPara = not noParasite and not s.manyHumans and p.stands and not cooling(st, 'parasite', t)
  if wantPara and p.humanOnIt then return { call = 'parasite', phase = 'kill', reason = 'a human ally is on the parasite' } end
  if not soft then
    local open, why = B.TeamOpening(s, cfg)
    if open and noPush and not wantPara then open = false end
    if open then
      if wantPara and s.alliesHealthy >= cfg.gatherNeed and p.botDistance <= s.pushDistance then
        return { call = 'parasite', phase = 'gather', reason = 'an opening (' .. why .. '), the parasite is nearer' }
      end
      if not noPush and not cooling(st, 'push', t) and exists(s.pushTarget) then
        return { call = 'push', lane = s.pushLane, target = s.pushTarget, reason = 'an opening (' .. why .. ')' }
      end
    end
  end
  if wantPara and t >= cfg.parasiteCalmFrom and s.alliesHealthy >= cfg.parasiteCalmAllies and numbers >= 0 and
      not p.seenRecently and s.levelDiff >= -1 and (s.botsFighting or 0) == 0 then
    return { call = 'parasite', phase = 'gather', reason = 'calm: none of theirs seen near it' }
  end
  if cur == 'well' then
    local w
    for _, x in ipairs(s.wells) do if x.id == st.well then w = x end end
    if w == nil then return farm('the well is ours or locked') end
    local alive = 0
    for _, b in ipairs(s.bots) do if b.alive and st.assigned[b.id] then alive = alive + 1 end end
    if alive == 0 then return farm('the well crew is down', true) end
    if w.guards > alive then return farm('they guard the well', true) end
    if t - st.since > cfg.wellMax then return farm('the well took too long', true) end
    return keep('taking the well')
  end
  if #s.wells > 0 and numbers >= 0 and s.alliesAlive >= cfg.wellMinAllies and not cooling(st, 'well', t) then
    local best
    for _, w in ipairs(s.wells) do
      if w.guards == 0 then
        local crew, n, d = wellCrew(s, w, cfg)
        if n >= cfg.wellBots and (best == nil or d < best.d) then best = { w = w, crew = crew, n = n, d = d } end
      end
    end
    if best ~= nil then
      return { call = 'well', well = best.w.id, assigned = best.crew, reason = best.n .. ' bot(s) for an unguarded well' }
    end
  end
  if cur == 'farm' then return farm('holding') end
  return farm('nothing to do')
end

-- ---- carrying the call out ----

local function teamLog(text)
  local f = hostfn('Log')
  if f then f(text) end
end

local function callName(call, phase)
  if phase then return call .. '/' .. phase end
  return call
end

local function endTeamMissions(team, guid, target)
  local openf, endf = hostfn('OpenMission'), hostfn('EndMission')
  if not (openf and endf) or guid == nil then return 0 end
  local ids = {}
  for i = 1, 256 do
    local id, tgt, g = openf(team, i)
    if id == nil then break end
    if g == guid and (target == nil or tgt == target) then ids[#ids + 1] = id end
  end
  for _, id in ipairs(ids) do endf(id) end
  return #ids
end

local function missionFor(team, guid, target)
  local openf = hostfn('OpenMission')
  if not openf or guid == nil then return 0 end
  for i = 1, 256 do
    local id, tgt, g = openf(team, i)
    if id == nil then break end
    if g == guid and tgt == target then return id end
  end
  return 0
end

local PING_ATTENTION, PING_RETREAT, PING_DANGER, PING_ONMYWAY = 0, 1, 2, 3

-- A ping by the living bot nearest `pos`, at most one per pingGap seconds unless forced (a call's start, a retreat).
local function teamPing(s, st, pos, target, pingType, why, force)
  local f = hostfn('TeamPing')
  if not f or pos == nil then return end
  if not force and s.t - st.lastPing < B.teamConfig.pingGap then return end
  local best, bestD
  for _, b in ipairs(s.bots) do
    if b.alive then
      local d = dist(b.pos, pos)
      if bestD == nil or d < bestD then best, bestD = b, d end
    end
  end
  if best == nil then return end
  if f(best.id, target or kObjIDNone, pos.x, pos.y, pos.z or 0, pingType, why) then
    st.lastPing = s.t
    B.stats.team.pings = B.stats.team.pings + 1
  end
end

local function groupCentre(s)
  local x, y, n = 0, 0, 0
  for _, b in ipairs(s.bots) do
    if b.alive then x, y, n = x + b.pos.x, y + b.pos.y, n + 1 end
  end
  if n == 0 then return nil end
  return vec(x / n, y / n, 0)
end

local function aliveBots(s)
  local n = 0
  for _, b in ipairs(s.bots) do if b.alive then n = n + 1 end end
  return math.max(n, 1)
end

local function guidOf(name)
  local c = global(name)
  return type(c) == 'table' and c.conditionGUID or nil
end

local function leaveCall(team, s, st, prev, nextCall, abort, stalled)
  if prev == 'parasite' or prev == 'contest' then
    endTeamMissions(team, TEAM_RALLY_GUID)
    endTeamMissions(team, TEAM_CONTEST_GUID)
    if nextCall ~= 'parasite' and nextCall ~= 'contest' then endTeamMissions(team, guidOf('condition_Mission_KillParasite')) end
  elseif prev == 'push' then
    for bot, lane in pairs(st.homeLane) do
      if exists(bot) then nBot.AssignLane(bot, lane) end
    end
  elseif prev == 'well' then
    if st.well ~= nil then endTeamMissions(team, guidOf('condition_Mission_CaptureWell'), st.well) end
  end
  if prev ~= 'farm' and prev ~= 'defend' then st.cool[prev] = s.t + (B.teamConfig[prev .. 'Cooldown'] or 30) end
  if prev == 'push' and stalled then st.cool.push = math.max(st.cool.push or 0, s.t + B.teamConfig.pushStallCooldown) end
  -- a retreat from the parasite holds both of its calls (a test match: a kill aborted as outnumbered, a contest 1 s later)
  if abort and (prev == 'parasite' or prev == 'contest') then
    st.cool.parasite = math.max(st.cool.parasite or 0, s.t + B.teamConfig.parasiteCooldown)
    st.cool.contest = math.max(st.cool.contest or 0, s.t + B.teamConfig.contestCooldown)
  end
end

local function enterCall(team, s, st, d)
  local call = d.call
  if call == 'push' then
    st.lastAdvantage, st.lastProgress, st.structures = s.t, s.t, s.enemyStructures
    for _, b in ipairs(s.bots) do
      if b.role ~= ROLE_HUNTER then
        if st.homeLane[b.id] == nil then st.homeLane[b.id] = nBot.GetLaneID(b.id) end
        nBot.AssignLane(b.id, d.lane)
      end
    end
    if exists(d.target) then teamPing(s, st, nGameObject.GetPosition(d.target), d.target, PING_ATTENTION, 'push', true) end
  elseif call == 'contest' then
    local p = s.parasite
    if p.stands then teamPing(s, st, p.pos, p.id, PING_ATTENTION, 'contest the parasite', true) end
  elseif call == 'well' then
    for _, w in ipairs(s.wells) do
      if w.id == d.well then teamPing(s, st, w.pos, kObjIDNone, PING_ONMYWAY, 'take a well', true) end
    end
  elseif call == 'defend' then
    st.lastThreat = s.t
    local g = nBattlegrounds.GetGuardianOnTeam and nBattlegrounds.GetGuardianOnTeam(team)
    if exists(g) then teamPing(s, st, nGameObject.GetPosition(g), kObjIDNone, PING_DANGER, 'defend the base', true) end
  end
  B.stats.team.calls[call] = (B.stats.team.calls[call] or 0) + 1
end

local function enterPhase(team, s, st, d)
  if d.call ~= 'parasite' then return end
  local p = s.parasite
  if d.phase == 'gather' then
    teamPing(s, st, p.rally, kObjIDNone, PING_ONMYWAY, 'gather for the parasite', true)
  elseif d.phase == 'kill' and p.stands then
    teamPing(s, st, p.pos, p.id, PING_ATTENTION, 'take the parasite', true)
    local kill = global('condition_Mission_KillParasite')
    if type(kill) == 'table' and type(kill[nConditionFns.Check]) == 'function' then pcall(kill[nConditionFns.Check], team) end
  end
end

-- Every tick: the team's own missions refreshed, the push's advantage and progress followed.
local function upkeep(team, s, st)
  if s.baseThreat then st.lastThreat = s.t end
  if st.call == 'push' then
    -- the advantage: an opening still (any of its rules), or at least one more of us up than of them
    if B.TeamOpening(s, B.teamConfig) or (s.alliesAlive - s.enemiesUp >= 1 and s.alliesHealthy >= 2 and s.hpAvg >= 0.35) then
      st.lastAdvantage = s.t
    end
    if st.structures ~= nil and s.enemyStructures < st.structures then st.lastProgress = s.t end
    st.structures = s.enemyStructures
  end
  local p = s.parasite
  if st.call == 'parasite' and p.stands then
    nBot.RequestMission(team, nMissionType.Rally, p.id, aliveBots(s), 0, TEAM_RALLY_GUID, p.rally)
  elseif st.call == 'contest' and p.stands then
    nBot.RequestMission(team, nMissionType.ContestParasite, p.id, aliveBots(s), 0, TEAM_CONTEST_GUID, contestPoint(team))
  end
end

function B.TeamTickOne(team)
  local st = teamState(team)
  local s = B.TeamSnapshot(team)
  upkeep(team, s, st)
  local d = B.TeamDecide(s, st, B.teamConfig)
  local prev, prevPhase = st.call, st.phase
  if d.call ~= prev then
    leaveCall(team, s, st, prev, d.call, d.abort, d.stalled)
    if d.abort then
      B.stats.team.aborts = B.stats.team.aborts + 1
      local c = groupCentre(s)
      if c then teamPing(s, st, c, kObjIDNone, PING_RETREAT, 'retreat: ' .. tostring(d.reason), true) end
    end
    st.call, st.since = d.call, s.t
    st.lane, st.target, st.well = d.lane, d.target, d.well
    st.assigned = d.assigned or {}
    st.phase, st.phaseSince = d.phase, s.t
    enterCall(team, s, st, d)
    enterPhase(team, s, st, d)
    upkeep(team, s, st)
  elseif d.phase ~= prevPhase then
    st.phase, st.phaseSince = d.phase, s.t
    enterPhase(team, s, st, d)
  end
  if d.call ~= prev or d.phase ~= prevPhase then
    teamLog(string.format('team %d: %s -> %s (%s); allies %d alive (%d healthy, %d bots), enemies %d up, %d down, ' ..
      'level %+.1f, hp %.2f', team, callName(prev, prevPhase), callName(d.call, d.phase), tostring(d.reason),
      s.alliesAlive, s.alliesHealthy, #s.bots, s.enemiesUp, s.enemiesDown, s.levelDiff, s.hpAvg))
  end
  return d
end

function B.TeamTick()
  if not teamOn() then return 0 end
  B.stats.team.ticks = B.stats.team.ticks + 1
  for _, team in ipairs({ nTeamID.Mortal, nTeamID.Spirit }) do
    local ok, err = pcall(B.TeamTickOne, team)
    if not ok then noteError('TeamTick: ' .. tostring(err)) end
  end
  return 0
end

-- ---- what the rest of the file asks ----

-- Whether `team` is at the parasite on purpose (its kill phase or a contest): kill chases there are fine then.
function B.TeamAtParasite(team)
  if not teamOn() then return false end
  local st = teamState(team)
  return (st.call == 'parasite' and st.phase == 'kill') or st.call == 'contest'
end

-- Whether the Hunter goes to the parasite: the kill phase, or a contest with two allies at it.
function B.TeamWantsHunterOnParasite(team, para)
  if not teamOn() then return false end
  local st = teamState(team)
  if st.call == 'parasite' and st.phase == 'kill' then return true end
  if st.call == 'contest' and exists(para) then
    return alliesAround(kObjIDNone, team, nGameObject.GetPosition(para), B.teamConfig.contestRange) >= 2
  end
  return false
end

-- The mission offer by the team's call: true = ask the board as usual, false = none, a number = that mission's id.
function B.TeamOffer(bot, missionType, role, st, team)
  local call, phase = st.call or 'farm', st.phase
  if missionType == nMissionType.DefendBase or missionType == nMissionType.DefendCore or
      missionType == nMissionType.KillShaper then
    return true
  end
  if missionType == nMissionType.Rally then return call == 'parasite' end
  if missionType == nMissionType.KillParasite then return (call == 'parasite' and phase == 'kill') or call == 'contest' end
  if missionType == nMissionType.ContestParasite then return call == 'contest' end
  if missionType == nMissionType.DefendTower then return call == 'farm' and (role == 1 or role == ROLE_PREDATOR) end
  if missionType == nMissionType.DefendWell then return (call == 'farm' or call == 'well') and role ~= 0 end
  if missionType == nMissionType.AttackWell then
    if call == 'well' and st.assigned[bot] then return missionFor(team, guidOf('condition_Mission_CaptureWell'), st.well) end
    return call == 'farm' and role == ROLE_PREDATOR  -- BOTCOMP turns it into his harass while he outnumbers
  end
  return true
end

function B.ApplyTeamFixup()
  local f = B.fixups
  if f.team then return end
  if not teamOn() then
    f.team = true
    return
  end
  local names = { 'condition_Mission_RallyForParasite', 'condition_Mission_RallyForParasiteContest',
                  'condition_Mission_KillParasite', 'condition_Bot_TimeToRally', 'condition_Bot_GroupedWithAllies',
                  'condition_Bot_GatherForParasite' }
  local t = {}
  for _, n in ipairs(names) do
    t[n] = global(n)
    if type(t[n]) ~= 'table' then return end
  end
  if type(nBot.GetAvailableMission) ~= 'function' or type(nBattlegrounds.GetNumTowers) ~= 'function' then return end
  local CHECK = nConditionFns.Check
  -- the team opens Rally and Contest itself; KillParasite only while it is at the parasite on purpose
  t.condition_Mission_RallyForParasite[CHECK] = function() return false, kObjIDNone, 0 end
  t.condition_Mission_RallyForParasiteContest[CHECK] = function() return false, kObjIDNone, 0 end
  local killCheck = t.condition_Mission_KillParasite[CHECK]
  t.condition_Mission_KillParasite[CHECK] = function(team, ...)
    if not B.TeamAtParasite(team) then return false, kObjIDNone, 0 end
    return killCheck(team, ...)
  end
  -- the offers
  local getMission = nBot.GetAvailableMission
  nBot.GetAvailableMission = function(bot, missionType, range)
    if not exists(bot) then return getMission(bot, missionType, range) end
    local team = nGameObject.GetTeam(bot)
    local r = B.TeamOffer(bot, missionType, roleOf(bot), teamState(team), team)
    if r == true then return getMission(bot, missionType, range) end
    if type(r) == 'number' then return r end
    return 0
  end
  -- the push: TimeToRally is the team's push; a bot left in LateGameRally after it goes back to Main
  t.condition_Bot_TimeToRally[1] = function(bot, ...)
    if not exists(bot) then return false end
    return teamState(nGameObject.GetTeam(bot)).call == 'push'
  end
  local grouped = t.condition_Bot_GroupedWithAllies[1]
  t.condition_Bot_GroupedWithAllies[1] = function(bot, ...)
    if exists(bot) and teamState(nGameObject.GetTeam(bot)).call ~= 'push' then return true end
    return grouped(bot, ...)
  end
  -- the siege scripts take the push lane (BOTLANE b answers 100 - n inside them; the push lane is the "fuller" one)
  local numTowers = nBattlegrounds.GetNumTowers
  nBattlegrounds.GetNumTowers = function(team, lane)
    if B.inSiege then
      local st = B.teamState[enemyOf(team)]
      if st ~= nil and st.call == 'push' and st.lane ~= nil then
        if lane == st.lane then return 100 end
        return 0
      end
    end
    return numTowers(team, lane)
  end
  -- the gather: the team's kill phase decides (it counts gatherNeed at the rally point itself)
  local gather = t.condition_Bot_GatherForParasite[1]
  t.condition_Bot_GatherForParasite[1] = function(bot, ...)
    local a, x, y, z, e, u = gather(bot, ...)
    if not exists(bot) then return a, x, y, z, e, u end
    local st = teamState(nGameObject.GetTeam(bot))
    return st.call == 'parasite' and st.phase == 'kill', x, y, z, e, u
  end
  f.team = true
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

-- D6e: nDirector.GetClosestCampPosition is a stub in the client (a static vector, so condition_Bot_NextCreepCamp
-- never passes and the Hunter's MoveToNextCamp phase idles). With the host's camps: the nearest camp with a living
-- creature, else the zero vector (the conditions treat it as "none").
do
  local closestCamp = hostfn('ClosestCamp')
  if closestCamp and nDirector then
    nDirector.GetClosestCampPosition = function(team, pos, ignoreFoW)
      count('GetClosestCampPosition')
      local x, y, z = closestCamp(pos.x, pos.y, pos.z)
      if x == nil then return vec(0, 0, 0) end
      return vec(x, y, z)
    end
  end
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
