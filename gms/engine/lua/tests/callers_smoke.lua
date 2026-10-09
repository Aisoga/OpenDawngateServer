-- D6b-shims: runs the decompiled server scripts that call nBot or the bot-only nBattlegrounds getters (game content,
-- read from the gitignored re/out/serverdata_lua/lua at run time, never committed) against dgsim_bots_api.lua and the
-- stub world of stub_bound_api.lua, to check that every caller gets the shapes it expects. Our own code.
--   java -cp luaj-jse-2.0.3.jar lua callers_smoke.lua <tests dir> <api file> <scripts dir> <list file>
-- <list file>: one "<kind> <relative path>" per line (kind condition / ability / behavior / lua), written by
-- re/scripts/bots_lua_check.py --callers. Prints one line per script: OK or the first error, and the stub functions the
-- scripts needed that the stub doesn't provide (as "missing stub").

local testsDir, apiFile, scriptsDir, listFile = arg[1], arg[2], arg[3], arg[4]
dofile(testsDir .. '/stub_bound_api.lua')
local W = stub

-- ---- permissive namespaces: an unknown function is a no-op that returns nil (noted once) -------------------------
local missing = {}
local function auto(name)
  local ns = _G[name] or {}
  _G[name] = ns
  setmetatable(ns, { __index = function(t, k)
    local key = name .. '.' .. tostring(k)
    missing[key] = (missing[key] or 0) + 1
    return function() return nil end
  end })
  return ns
end
for _, n in ipairs({ 'nAbility', 'nModifier', 'nAttribute', 'nAgent', 'nLocomotion', 'nThread', 'nBehaviorTree',
                     'nMathUtil', 'nLineUtils', 'nBit', 'nDebug', 'nEffect', 'nCondition', 'nDirector', 'nPlayer',
                     'nGameObject', 'nGameSimulator', 'nUtil', 'nFogOfWar', 'nClientFeedback', 'nLevel',
                     'nObjectManager', 'nBattlegrounds', 'nAudio', 'nRNG', 'nTimeManager', 'nThreadData',
                     'nCombatEvent', 'nFeedback', 'nCamera', 'nClient', 'nQuaternion' }) do
  auto(n)
end

-- what the scripts need beyond the shims' stub
local currentBot = nil
local function yieldIfThread() if coroutine.running() then coroutine.yield() end end
-- every nThread function waits: it yields (the runner resumes a thread at most 4 times), so `while true` loops end
setmetatable(nThread, { __index = function(t, k)
  return function() yieldIfThread() return true end
end })
nUtil.ToGUID = function(s) if s == '0x00000000' then return kObjIDNone end return 'guid:' .. s end
nUtil.GetAsset = function(s) return 'asset:' .. tostring(s) end
nAbility.create = function() return { isStatic = true } end
nAbility.RegisterAbility = function() end
nAbility.PreloadAsset = function() end
nAbility.GetRankedValue = function(t, rank) if type(t) == 'table' then return t[rank or 1] or t[1] end return t end
nAbility.GetAbilityRank = function() return 1 end
nAbility.GetAbilityGUIDFromSlot = function(obj, slot) return 'guid:slot' .. tostring(slot) end
nAbility.GetCooldownRemaining = function() return 0 end
nUtil.ToBitfield = function() return 0 end
nBit.LShift = function(a, n) return (a or 0) * 2 ^ (n or 0) end
nGameObject.IsCreep = function(id) local o = W.get(id); return o ~= nil and o.npc == nNPCType.Creep end
nCondition.create = function() return { isStatic = true } end
nCondition.RegisterCondition = function() end
nModifier.create = function() return { isStatic = true } end
nModifier.AgentHasModifierMatchingGUID = function() return false end
nModifier.GetModifiersMatchingDebuffDescriptor = function() return {} end
nAttribute.GetDerivedAttributeValue = function() return 50 end
nAttribute.GetAttributeValue = function() return 0 end
nAgent.GetBlackboardData_GUID = function() return kObjIDNone end
nAgent.GetBlackboardData_Int = function() return 0 end
nAgent.GetBlackboardData_Float = function() return 0 end
nAgent.GetBlackboardData_Bool = function() return false end
nAgent.SetBlackboardData_Int = function() end
nAgent.SetBlackboardData_GUID = function() end
nAgent.ClearBlackboardData = function() end
nAgent.HasTargetsOnAggroList = function() return false end
nAgent.GetTargetsOnAggroList = function() return {} end
nThread.WaitForNearGoal = function() yieldIfThread() return true end
nThread.WaitForNearObject = function() yieldIfThread() return true end
nThread.WaitForSeconds = function() yieldIfThread() end
nThread.WaitForever = function() yieldIfThread() end
nThread.Yield = function() yieldIfThread() end
nBehaviorTree.GetMyObjectID = function() return currentBot end
nBehaviorTree.GetTargetObjectID = function() return kObjIDNone end
nMathUtil.RotateVectorByAxisAngle = function(v) return nVector.create(v.x, v.y, v.z) end
nLineUtils.GetObjectsAlongLine = function() return {} end
nBit.Or = function(...)
  local r = 0
  for i = 1, select('#', ...) do
    local v, bit, acc = select(i, ...), 1, 0
    if type(v) ~= 'number' then
      missing['nBit.Or with a ' .. type(v) .. ' argument'] = 1
      v = 0
    end
    local a, b = r, v
    while a > 0 or b > 0 do
      if a % 2 == 1 or b % 2 == 1 then acc = acc + bit end
      a, b, bit = math.floor(a / 2), math.floor(b / 2), bit * 2
    end
    r = acc
  end
  return r
end
nLocomotion.GetClosestPosition = function(obj, p) return true, nVector.create(p.x, p.y, p.z or 0) end
nLocomotion.FindFleeDestination = function(obj) return nGameObject.GetPosition(obj) end
nLocomotion.MoveToObject = function() return true end
nGameObject.SetTargetID = function(id, t) local o = W.get(id); if o then o.target = t end end
nGameObject.GetFootprintRadius = function() return 1 end
nGameObject.IsStructure = function(id) local o = W.get(id); return o ~= nil and o.structure == true end
nGameObject.IsInCombat = function() return true end
nGameObject.GetLastCombatTime = function() return 0 end
nGameObject.GetRecentDamage = function() return 10 end
nGameObject.ValidateFriendlyTarget = function(team, id) local o = W.get(id); return o ~= nil and o.alive and o.team == team end
nGameObject.GetFacing = function() return nVector.create(1, 0, 0) end
nGameObject.Exists = function(id) return W.get(id) ~= nil end
nPlayer.IsPlayerControlledObject = function(id) local o = W.get(id); return o ~= nil and o.player == true end
nPlayer.GetPlayerIdForObject = function(id) local o = W.get(id); return o and o.playerId or -1 end
nPlayer.GetPlayerAvailableGold = function() return 500 end
nGameSimulator.GetPlayerStartingPoint = function(pid)
  if pid ~= nil and pid >= 5 then return nVector.create(182, 182, 0), nVector.create(-1, 0, 0) end
  return nVector.create(-182, -182, 0), nVector.create(1, 0, 0)
end
nDirector.GetParasiteID = function() return W.parasite or kObjIDNone end

-- require("Folder!Name.lua") from the scripts dir
local loaded = {}
function require(key)
  if loaded[key] then return end
  loaded[key] = true
  local folder, name = string.match(key, '^([^!]+)!(.+)$')
  if not folder then return end
  local f = loadfile(scriptsDir .. '/' .. folder .. '/' .. name)
  if f then f() else missing['require ' .. key] = 1 end
end

-- the base scripts (Vector's own semantics replace the stub's), then the shims
require('Lua!GlobalDefinitions.lua')
require('Lua!Global.lua')
require('Lua!Table.lua')
require('Lua!BotUtils.lua')
local summary = dofile(apiFile)

-- ---- the world: SM_Map_v5's structures, two teams of shapers, minions, wells, the parasite -------------------------
local function P(x, y) return { x = x, y = y, z = 0 } end
local bindings = {
  { 'Binding_Mortal_Outer', -116.3, 28.9 }, { 'Binding_Mortal_Middle', -124.5, -44.4 },
  { 'Binding_Mortal_Inner', -158.4, -111.0 }, { 'Binding_Mortal_Outer', 28.8, -116.4 },
  { 'Binding_Mortal_Middle', -44.3, -124.4 }, { 'Binding_Mortal_Inner', -110.9, -158.4 },
  { 'Binding_Spirit_Outer', -28.8, 116.4 }, { 'Binding_Spirit_Middle', 44.3, 124.4 },
  { 'Binding_Spirit_Inner', 110.9, 158.4 }, { 'Binding_Spirit_Outer', 116.3, -28.9 },
  { 'Binding_Spirit_Middle', 124.5, 44.4 }, { 'Binding_Spirit_Inner', 158.4, 111.0 },
}
for _, b in ipairs(bindings) do
  W.add({ name = b[1], team = string.find(b[1], 'Mortal') and 1 or 2, pos = P(b[2], b[3]), npc = nNPCType.Tower,
          structure = true, maxhp = 3000 })
end
W.add({ name = 'nub_mortal_v5_A', team = 1, pos = P(-149.5, -149.5), npc = nNPCType.Nub, structure = true })
W.add({ name = 'nub_spirit_v5_A', team = 2, pos = P(149.2, 149.5), npc = nNPCType.Nub, structure = true })
W.guardians[1] = W.add({ name = 'GuardianMortal_v5', team = 1, pos = P(-182, -182), npc = nNPCType.Guardian,
                         structure = true })
W.guardians[2] = W.add({ name = 'GuardianSpirit_v5', team = 2, pos = P(182, 182), npc = nNPCType.Guardian,
                         structure = true })
local wells = { { -182.3, 27.9, 1 }, { 28.2, -182.6, 1 }, { 182.5, -27.9, 2 }, { -27.6, 182.7, 2 } }
for i, w in ipairs(wells) do
  W.wells[i] = W.add({ name = 'gold_side_' .. i, team = w[3], pos = P(w[1], w[2]), npc = nNPCType.GoldMine })
end
W.wellLocked = { [0] = false, [1] = true, [2] = false, [3] = true }
for i = 1, 20 do
  W.add({ name = 'Titan_Node_Follower', team = 2, pos = P(182.5 - 1.5 * i, -27.9), npc = nNPCType.Follower })
end
W.parasite = W.add({ name = 'Objective_SpiritMonster_State1', team = 0, pos = P(-3.4, 3.2), npc = nNPCType.ObjectiveMajor })
local bots = {}
local spots = { P(-75, 62), P(-60, 75), P(66, -69), P(70, -60), P(-100, -100) }
for i = 1, 5 do
  bots[i] = W.add({ name = 'mortal_bot_' .. i, team = 1, pos = spots[i], player = true, bot = true, playerId = i - 1,
                    level = 6 })
  bots[5 + i] = W.add({ name = 'spirit_bot_' .. i, team = 2, pos = P(spots[i].x + 12, spots[i].y + 6), player = true,
                        bot = true, playerId = 4 + i, level = 7, hp = 40 })
end
for i = 1, 6 do
  W.add({ name = 'Minion_Shared_Melee', team = 1, pos = P(-70 + i, 60), npc = nNPCType.MeleeMinion, minion = true })
  W.add({ name = 'Minion_Shared_Melee', team = 2, pos = P(-60 + i, 70), npc = nNPCType.MeleeMinion, minion = true,
          best = bots[1] })
end
W.matchTime, W.time = 1000, 1000
for _, b in ipairs(bots) do nBot.AssignLane(b, nLaneID.Top) end

-- ---- run every caller ----------------------------------------------------------------------------------------------
local function run(fn, ...)
  local co = coroutine.create(fn)
  local ok, err = coroutine.resume(co, ...)
  local n = 1
  while ok and coroutine.status(co) == 'suspended' and n < 4 do
    ok, err = coroutine.resume(co)
    n = n + 1
  end
  if not ok then return false, err end
  return true
end

local results, okCount, errCount = {}, 0, 0
for line in io.lines(listFile) do
  local kind, rel = string.match(line, '^(%S+)%s+(.+)$')
  if kind then
    local name = string.match(rel, '([^/]+)%.lua$')
    local chunk, lerr = loadfile(scriptsDir .. '/' .. rel)
    local status = 'OK'
    if not chunk then
      status = 'LOAD ' .. tostring(lerr)
    else
      local ok, err = pcall(chunk)
      if not ok then
        status = 'RUN ' .. tostring(err)
      else
        -- D6b: the script fix-ups, as the host's MissionTick applies them once the scripts' tables exist
        if type(dgsim_bots) == 'table' and dgsim_bots.ApplyFixups then dgsim_bots.ApplyFixups() end
        local errors = {}
        local function try(what, fn, ...)
          if type(fn) ~= 'function' then return end
          local ok2, e = run(fn, ...)
          if not ok2 then errors[#errors + 1] = what .. ': ' .. tostring(e) end
        end
        if kind == 'condition' then
          local c = _G[name]
          if type(c) == 'table' then
            if c.conditionType == nConditionType.Mission then
              for team = 1, 2 do
                try('Check(' .. team .. ')', c[nConditionFns.Check], team)
                try('CheckEnd(' .. team .. ')', c[nConditionFns.CheckEnd], team, W.wells[3 - team + 1] or W.parasite)
                try('CheckEnd(' .. team .. ', parasite)', c[nConditionFns.CheckEnd], team, W.parasite)
              end
            else
              for _, b in ipairs({ bots[1], bots[6], bots[3] }) do
                currentBot = b
                try('[1](' .. tostring(b) .. ')', c[1], b)
              end
            end
          else
            errors[#errors + 1] = 'no global ' .. name
          end
        elseif kind == 'ability' then
          local a = _G[name]
          if type(a) == 'table' then
            for _, b in ipairs({ bots[1], bots[6] }) do
              currentBot = b
              local ctx = { myObjectID = b, myCasterID = b, myTeam = nGameObject.GetTeam(b), rank = 1 }
              try('Activate', a[nAbilityFns.Activate], ctx)
              try('Tick', a[nAbilityFns.Tick], ctx)
              try('Deactivate', a[nAbilityFns.Deactivate], ctx)
            end
          else
            errors[#errors + 1] = 'no global ' .. name
          end
        elseif kind == 'behavior' then
          local g = 'n' .. name
          local bh = _G[g]
          if type(bh) == 'table' then
            for _, b in ipairs({ bots[1], bots[6] }) do
              currentBot = b
              try('Activate', bh[nBehaviorFns.Activate])
              try('Tick', bh[nBehaviorFns.Tick])
              try('Deactivate', bh[nBehaviorFns.Deactivate])
            end
          else
            errors[#errors + 1] = 'no global ' .. g
          end
        end
        if #errors > 0 then status = table.concat(errors, ' | ') end
      end
    end
    if status == 'OK' then okCount = okCount + 1 else errCount = errCount + 1 end
    print((status == 'OK' and 'OK    ' or 'ERROR ') .. rel .. (status == 'OK' and '' or ('  ' .. status)))
  end
end
local m = {}
for k, v in pairs(missing) do m[#m + 1] = k end
table.sort(m)
if #m > 0 then print('missing stub: ' .. table.concat(m, ', ')) end
print(summary)
print('callers: ' .. okCount .. ' ok, ' .. errCount .. ' with errors')
-- LuaJ runs coroutines on Java threads: the ones left suspended above would keep the JVM alive
if os and os.exit then os.exit(0) end
