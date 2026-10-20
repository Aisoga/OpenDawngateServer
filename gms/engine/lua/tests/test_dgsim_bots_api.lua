-- D6b-shims: offline tests of gms/engine/lua/dgsim_bots_api.lua against stub_bound_api.lua (LuaJ 2.0.3).
--   java -cp tools/luaj/luaj-jse-2.0.3.jar lua gms/engine/lua/tests/test_dgsim_bots_api.lua <tests dir> <api file>
-- (re/scripts/bots_lua_check.py runs it). Prints one line per failed check and a final "N checks, M failed".

local testsDir = arg and arg[1] or 'gms/engine/lua/tests'
local apiFile = arg and arg[2] or 'gms/engine/lua/dgsim_bots_api.lua'
dofile(testsDir .. '/stub_bound_api.lua')
local W = stub

local checks, failed, current = 0, 0, '?'
local function check(cond, what)
  checks = checks + 1
  if not cond then
    failed = failed + 1
    print('FAILED [' .. current .. '] ' .. tostring(what))
  end
end
local function near(a, b, eps) return math.abs(a - b) <= (eps or 1e-4) end

local function load()
  dgsim_bots = nil
  nBot = nil
  nBotHost = nil
  local chunk = assert(loadfile(apiFile))
  return chunk()
end

local function test(name, fn)
  current = name
  W.reset()
  nBattlegrounds.GetNumTowers = nil
  local summary = load()
  local ok, err = pcall(fn, summary)
  if not ok then
    failed = failed + 1
    print('FAILED [' .. name .. '] raised: ' .. tostring(err))
  end
end

local function P(x, y) return { x = x, y = y, z = 0 } end
local function shaper(name, team, x, y, extra)
  local o = extra or {}
  o.name, o.team, o.pos, o.player = name, team, P(x, y), true
  return W.add(o)
end

-- ---- loading -------------------------------------------------------------------------------------------------------
test('load', function(summary)
  check(type(summary) == 'string' and string.find(summary, 'nBot 30 functions') ~= nil, 'summary: ' .. tostring(summary))
  local names = { 'ValidateHostileBotTarget', 'GetNearbyObjects', 'GetEnemyPlayersNearPosition',
    'GetAllyPlayersNearPosition', 'GetNumPlayersNearPosition', 'NumAlliesRallying', 'IsNearTower', 'IsTargetedByTower',
    'GetMinionAggroCount', 'GetSkillshotTarget', 'IsEnemyOfFirstTimePlayer', 'AssignLane', 'GetLaneID',
    'GetClosestLane', 'GetLaneCenterAndDirection', 'RequestMission', 'IsMissionAvailable', 'GetAvailableMission',
    'AcceptMission', 'GetMissionID', 'GetMissionTarget', 'GetMissionDestination', 'CanAffordNextItem',
    'PurchaseNextItem', 'CanBuyPotion', 'PurchasePotions', 'GetLastSeenEnemyPosition', 'LostSightOfTarget',
    'IsEnemyStronger', 'IsOutnumbered' }
  for _, n in ipairs(names) do check(type(nBot[n]) == 'function', 'nBot.' .. n) end
  for _, n in ipairs({ 'GetNumTowers', 'GetOuterTower', 'GetVulnerableStructures', 'GetClosestBaseStructure',
                       'GetMinesForTeam', 'GetFollowersForTitan', 'IsTitanNodeLocked' }) do
    check(type(nBattlegrounds[n]) == 'function', 'nBattlegrounds.' .. n)
  end
  -- the client's real bindings and D1c-live's host functions are left alone
  check(type(nBattlegrounds.GetGuardianOnTeam) == 'function' and type(nBattlegrounds.SpiritWell_IsLocked) == 'function',
        'other nBattlegrounds functions kept')
  -- loading twice keeps the state
  dgsim_bots.lanes.x = 1
  local chunk = assert(loadfile(apiFile))
  chunk()
  check(dgsim_bots.lanes.x == 1, 'state survives a reload')
end)

-- ---- targets -------------------------------------------------------------------------------------------------------
test('ValidateHostileBotTarget', function()
  local bot = shaper('bot', 1, 0, 0, { bot = true })
  local enemy = shaper('enemy', 2, 10, 0)
  local ally = shaper('ally', 1, 5, 0)
  local dead = shaper('dead', 2, 12, 0, { alive = false, hp = 0 })
  local inLocus = shaper('locus', 2, 228, 232)       -- the Spirit Locus: the enemy's for team 1
  local ownLocus = shaper('ownlocus', 1, -228, -232)  -- the Mortal Locus
  local hidden = shaper('untargetable', 2, 14, 0, { untargetable = true })
  check(nBot.ValidateHostileBotTarget(1, enemy, false, true) == true, 'enemy')
  check(nBot.ValidateHostileBotTarget(1, enemy) == true, 'enemy, two arguments')
  check(nBot.ValidateHostileBotTarget(1, ally, false, true) == false, 'ally')
  check(nBot.ValidateHostileBotTarget(1, dead, false, true) == false, 'dead')
  check(nBot.ValidateHostileBotTarget(1, dead, true, true) == true, 'dead with flag (HP not checked)')
  check(nBot.ValidateHostileBotTarget(1, hidden, false, true) == false, 'untargetable')
  check(nBot.ValidateHostileBotTarget(1, inLocus, false, true) == false, 'in the enemy Locus')
  check(nBot.ValidateHostileBotTarget(1, inLocus, false, true, true) == true, 'Locus check skipped')
  check(nBot.ValidateHostileBotTarget(2, ownLocus, false, true) == false, 'team 2: the Mortal Locus is the enemy one')
  check(nBot.ValidateHostileBotTarget(1, kObjIDNone) == false and nBot.ValidateHostileBotTarget(1, nil) == false, 'none')
  check(nBot.ValidateHostileBotTarget(nil, enemy) == false, 'no team')
  -- the fire tracker through the host: only without skipFireCheck
  nBotHost = { IsPositionSafe = function(team, x, y, z) return not (team == 1 and x == 10) end }
  check(nBot.ValidateHostileBotTarget(1, enemy) == false, 'under fire')
  check(nBot.ValidateHostileBotTarget(1, enemy, false, true) == true, 'fire check skipped')
  check(bot ~= nil, 'bot')
end)

test('GetNearbyObjects and players near', function()
  local bot = shaper('bot', 1, 0, 0, { bot = true })
  local e1 = shaper('e1', 2, 20, 0)
  local e2 = shaper('e2', 2, 8, 0)
  local eHidden = shaper('eHidden', 2, 5, 0, { visible = { [0] = true, [1] = false, [2] = true } })
  shaper('eDead', 2, 3, 0, { alive = false, hp = 0 })
  local a1 = shaper('a1', 1, 6, 0)
  local m1 = W.add({ name = 'minion', team = 2, pos = P(4, 0), npc = nNPCType.MeleeMinion, minion = true })
  local list = nBot.GetNearbyObjects(bot, 30, nTargetType.EnemyPlayers, 0)
  check(#list == 3 and list[1] == eHidden and list[2] == e2 and list[3] == e1, 'enemy players nearest first, no dead')
  list = nBot.GetNearbyObjects(bot, 30, nTargetType.EnemyPlayers, nGetObjectsFlags.CheckFoWVisibility)
  check(#list == 2 and list[1] == e2, 'fog of war flag')
  list = nBot.GetNearbyObjects(bot, 30, nTargetType.EnemyPlayers + nTargetType.EnemyNPCs, 0)
  check(#list == 4 and list[1] == m1, 'players and NPCs')
  list = nBot.GetNearbyObjects(bot, 30, nTargetType.AllyPlayers, 0)
  check(#list == 1 and list[1] == a1, 'allies without self')
  check(#nBot.GetNearbyObjects(kObjIDNone, 30, nTargetType.EnemyPlayers, 0) == 0, 'none')
  -- enemy players near a position: a table, nearest first; the 5th argument is the fog-of-war check
  local p = nGameObject.GetPosition(bot)
  list = nBot.GetEnemyPlayersNearPosition(1, p, 10)
  check(type(list) == 'table' and #list == 2 and list[1] == eHidden, 'enemies near a position')
  list = nBot.GetEnemyPlayersNearPosition(1, p, 10, 0, true)
  check(#list == 1 and list[1] == e2, 'enemies near a position, fog of war')
  check(#nBot.GetEnemyPlayersNearPosition(1, nil, 10) == 0, 'no position: empty table')
  -- allies near a position: a number, the caller included
  check(nBot.GetAllyPlayersNearPosition(1, p, 10) == 2, 'allies near: bot + a1')
  check(nBot.GetAllyPlayersNearPosition(2, p, 10) == 2, 'team 2 allies near: e2, eHidden')
end)

test('GetNumPlayersNearPosition', function()
  local bot = shaper('bot', 1, 0, 0, { bot = true })
  shaper('a1', 1, 5, 0)
  shaper('aLow', 1, 6, 0, { hp = 20 })
  local e1 = shaper('e1', 2, 10, 0)
  shaper('eLow', 2, 11, 0, { hp = 10 })
  local p = nGameObject.GetPosition(bot)
  local a, e = nBot.GetNumPlayersNearPosition(1, p, p, 30, 30)
  check(a == 3 and e == 2, 'all: ' .. a .. ' ' .. e)
  a, e = nBot.GetNumPlayersNearPosition(1, p, p, 30, 30, 0.3, 0.3)
  check(a == 2 and e == 1, 'HP threshold 0.3: ' .. a .. ' ' .. e)
  a, e = nBot.GetNumPlayersNearPosition(1, p, p, nil, nil, 0.3, 0.3)
  check(a == 0 and e == 0, 'nil radii count nothing (condition_Bot_VeryOutnumbered)')
  -- two centres: allies around the enemy, enemies around the bot (condition_Bot_EngageForKill)
  a, e = nBot.GetNumPlayersNearPosition(1, nGameObject.GetPosition(e1), p, 6, 8)
  check(a == 2 and e == 0, 'two centres: ' .. a .. ' ' .. e)
  -- history: an enemy seen at t 100, hidden at t 103: still counted with 5 s of history, not at t 106
  W.time = 100
  a, e = nBot.GetNumPlayersNearPosition(1, p, p, 30, 30, 0, 0, 5)
  check(e == 2, 'seen now')
  W.get(e1).visible[1] = false
  W.time = 103
  a, e = nBot.GetNumPlayersNearPosition(1, p, p, 30, 30, 0, 0)
  check(e == 1, 'hidden, no history')
  a, e = nBot.GetNumPlayersNearPosition(1, p, p, 30, 30, 0, 0, 5)
  check(e == 2, 'hidden, remembered for 5 s')
  W.time = 106
  a, e = nBot.GetNumPlayersNearPosition(1, p, p, 30, 30, 0, 0, 5)
  check(e == 1, 'forgotten after 5 s')
  -- the remembered position counts, not the current one
  W.time = 107
  W.get(e1).visible[1] = true
  nBot.GetNumPlayersNearPosition(1, p, p, 30, 30, 0, 0, 5)
  W.get(e1).visible[1] = false
  W.get(e1).pos = P(200, 0)
  W.time = 108
  a, e = nBot.GetNumPlayersNearPosition(1, p, p, 30, 30, 0, 0, 5)
  check(e == 2, 'last seen within the radius')
  -- IsOutnumbered (unit-test API)
  W.get(e1).visible[1] = true
  W.get(e1).pos = P(10, 0)
  check(nBot.IsOutnumbered(bot, 30, 0) == false, 'not outnumbered 3 v 2')
  shaper('e3', 2, 12, 0)
  shaper('e4', 2, 13, 0)
  check(nBot.IsOutnumbered(bot, 30, 0.3) == true, 'outnumbered 2 v 3 above 30 %')
end)

test('NumAlliesRallying', function()
  local bot = shaper('bot', 1, 0, 0, { bot = true })
  W.guardians[2] = W.add({ name = 'GuardianSpirit_v5', team = 2, pos = P(182, 182), npc = nNPCType.Guardian,
                           structure = true })
  shaper('nearMe', 1, 30, 0)            -- within 45 of the bot
  shaper('atBase', 1, 150, 150)         -- within 65 of the enemy Guardian
  shaper('atRally', 1, -100, -100)      -- within 30 of the rally point
  shaper('atClash', 1, 60, -60)         -- within 60 of the clash point
  shaper('away', 1, -100, 100)
  shaper('weak', 1, 10, 0, { hp = 10 })
  shaper('enemy', 2, 5, 0)
  local n = nBot.NumAlliesRallying(bot, P(-110, -100), P(70, -60), 0.3, 45, 65, 30, 60)
  check(n == 5, 'rallying: ' .. n)  -- the bot, nearMe, atBase, atRally, atClash
  check(nBot.NumAlliesRallying(kObjIDNone, P(0, 0), P(0, 0), 0, 1, 1, 1, 1) == 0, 'none')
end)

test('towers, aggro and skillshots', function()
  local bot = shaper('bot', 1, 0, 0, { bot = true })
  local tower = W.add({ name = 'Binding_Spirit_Outer', team = 2, pos = P(25, 0), npc = nNPCType.Tower,
                        structure = true, untargetable = true })
  W.add({ name = 'nub_spirit_v5_A', team = 2, pos = P(15, 0), npc = nNPCType.Nub, structure = true })
  local ownTower = W.add({ name = 'Binding_Mortal_Outer', team = 1, pos = P(-50, 0), npc = nNPCType.Tower,
                           structure = true })
  local isNear, id = nBot.IsNearTower(bot, 2, 60)
  check(isNear == true and id == tower, 'enemy tower near (untargetable towers count)')
  isNear, id = nBot.IsNearTower(bot, 2, 20)
  check(isNear == false and id == kObjIDNone, 'not within 20 (the nub is no tower)')
  isNear, id = nBot.IsNearTower(bot, 1, 60)
  check(isNear == true and id == ownTower, 'own tower')
  check(nBot.IsTargetedByTower(bot) == false, 'not targeted')
  W.get(tower).best = bot
  check(nBot.IsTargetedByTower(bot) == true, 'targeted by the tower')
  W.get(ownTower).best = bot
  W.get(tower).best = nil
  check(nBot.IsTargetedByTower(bot) == false, 'an own tower does not count')
  for i = 1, 4 do
    W.add({ name = 'm' .. i, team = 2, pos = P(5 + i, 5), npc = nNPCType.MeleeMinion, minion = true,
            best = (i <= 3) and bot or nil })
  end
  W.add({ name = 'creep', team = 0, pos = P(3, 3), npc = nNPCType.Creep, best = bot })
  check(nBot.GetMinionAggroCount(bot) == 3, 'three minions on the bot: ' .. nBot.GetMinionAggroCount(bot))
  -- skillshots
  local target = shaper('target', 2, 30, 0)
  local ok, aim = nBot.GetSkillshotTarget(bot, target, 20, 50)
  check(ok == true and near(aim.x, 30) and near(aim.y, 0), 'standing target')
  W.get(target).velocity = { x = 0, y = 5, z = 0 }
  ok, aim = nBot.GetSkillshotTarget(bot, target, 20, 50)
  local t = aim.y / 5
  check(ok == true and near(aim.x, 30) and aim.y > 0 and near(math.sqrt(aim.x * aim.x + aim.y * aim.y) / 20, t, 1e-3),
        'leads a moving target')
  W.get(target).velocity = { x = 30, y = 0, z = 0 }
  ok, aim = nBot.GetSkillshotTarget(bot, target, 20, 50)
  check(ok == false and near(aim.x, 30), 'can not catch a faster target running away')
  ok = nBot.GetSkillshotTarget(bot, target, 0, 50)
  check(ok == false, 'no speed')
  ok = nBot.GetSkillshotTarget(bot, kObjIDNone, 20, 50)
  check(ok == false, 'no target')
end)

-- ---- structures and wells ------------------------------------------------------------------------------------------
local function v5Structures()
  -- the twelve Bindings of SM_Map_v5_towers, with their noun names
  local b = {
    { 'Binding_Mortal_Outer', -116.3, 28.9 }, { 'Binding_Mortal_Middle', -124.5, -44.4 },
    { 'Binding_Mortal_Inner', -158.4, -111.0 }, { 'Binding_Mortal_Outer', 28.8, -116.4 },
    { 'Binding_Mortal_Middle', -44.3, -124.4 }, { 'Binding_Mortal_Inner', -110.9, -158.4 },
    { 'Binding_Spirit_Outer', -28.8, 116.4 }, { 'Binding_Spirit_Middle', 44.3, 124.4 },
    { 'Binding_Spirit_Inner', 110.9, 158.4 }, { 'Binding_Spirit_Outer', 116.3, -28.9 },
    { 'Binding_Spirit_Middle', 124.5, 44.4 }, { 'Binding_Spirit_Inner', 158.4, 111.0 },
  }
  local ids = {}
  for i, x in ipairs(b) do
    local team = string.find(x[1], 'Mortal') and 1 or 2
    ids[i] = W.add({ name = x[1], team = team, pos = P(x[2], x[3]), npc = nNPCType.Tower, structure = true })
  end
  W.add({ name = 'DEST_prefab_blue_tower_v5', team = 1, pos = P(-242, -242), npc = nNPCType.Tower, structure = true,
          attackable = false })
  local nubs = {}
  nubs[1] = W.add({ name = 'nub_spirit_v5_A', team = 2, pos = P(149.2, 149.5), npc = nNPCType.Nub, structure = true })
  nubs[2] = W.add({ name = 'nub_spirit_v5_C', team = 2, pos = P(138.9, 198.9), npc = nNPCType.Nub, structure = true })
  W.guardians[1] = W.add({ name = 'GuardianMortal_v5', team = 1, pos = P(-182, -182), npc = nNPCType.Guardian,
                           structure = true })
  W.guardians[2] = W.add({ name = 'GuardianSpirit_v5', team = 2, pos = P(182, 182), npc = nNPCType.Guardian,
                           structure = true })
  return ids, nubs
end

test('towers per lane', function()
  local b = v5Structures()
  check(nBattlegrounds.GetNumTowers(1, nLaneID.Top) == 3 and nBattlegrounds.GetNumTowers(1, nLaneID.Bottom) == 3,
        'Mortal 3 + 3')
  check(nBattlegrounds.GetNumTowers(2, nLaneID.Top) == 3 and nBattlegrounds.GetNumTowers(2, nLaneID.Bottom) == 3,
        'Spirit 3 + 3')
  check(nBattlegrounds.GetOuterTower(1, nLaneID.Top) == b[1], 'Mortal top outer')
  check(nBattlegrounds.GetOuterTower(2, nLaneID.Bottom) == b[10], 'Spirit bottom outer')
  W.kill(b[1])
  W.time = W.time + 10  -- past the scan cache
  check(nBattlegrounds.GetNumTowers(1, nLaneID.Top) == 2, 'one down')
  check(nBattlegrounds.GetOuterTower(1, nLaneID.Top) == b[2], 'the middle one is the outer one now')
  W.kill(b[2])
  W.kill(b[3])
  check(nBattlegrounds.GetOuterTower(1, nLaneID.Top) == kObjIDNone, 'all down: kObjIDNone')
  check(nBattlegrounds.GetNumTowers(1, nLaneID.Top) == 0, 'all down: 0')
  check(nBattlegrounds.GetNumTowers(1, nLaneID.None) == 0, 'lane None: 0')
  check(nBattlegrounds.GetNumTowers(1, nLaneID.Bottom) == 3, 'the other lane')
  -- the scan is cached for scanSeconds of game time
  local before = W.calls.GetObjectIdByIndex or 0
  nBattlegrounds.GetNumTowers(2, nLaneID.Top)
  nBattlegrounds.GetNumTowers(2, nLaneID.Bottom)
  check((W.calls.GetObjectIdByIndex or 0) == before, 'cached walk')
end)

test('vulnerable and base structures', function()
  local b, nubs = v5Structures()
  W.get(b[8]).untargetable = true  -- a protected Spirit Middle
  local v = nBattlegrounds.GetVulnerableStructures(2)
  -- Spirit: 6 Bindings - 1 untargetable + 2 nubs + the Guardian = 8
  check(#v == 8, 'Spirit vulnerable: ' .. #v)
  check(#nBattlegrounds.GetVulnerableStructures(1) == 7, 'Mortal: 6 Bindings + Guardian, not the fountain tower')
  local bot = shaper('bot', 1, 120, 150, { bot = true })
  check(nBattlegrounds.GetClosestBaseStructure(bot) == nubs[1], 'the nearest nub')
  W.kill(nubs[1])
  W.kill(nubs[2])
  check(nBattlegrounds.GetClosestBaseStructure(bot) == W.guardians[2], 'then the Guardian')
  W.get(W.guardians[2]).untargetable = true
  check(nBattlegrounds.GetClosestBaseStructure(bot) == kObjIDNone, 'nothing attackable')
  check(nBattlegrounds.GetClosestBaseStructure(kObjIDNone) == kObjIDNone, 'none')
end)

test('wells without the host', function()
  local pos = { { -182.3, 27.9, 1 }, { 28.2, -182.6, 1 }, { 182.5, -27.9, 2 }, { -27.6, 182.7, 2 } }
  for i, w in ipairs(pos) do
    W.wells[i] = W.add({ name = 'gold_side_' .. i, team = w[3], pos = P(w[1], w[2]), npc = nNPCType.GoldMine })
  end
  W.wellLocked = { [0] = true, [1] = false, [2] = false, [3] = true }
  local m = nBattlegrounds.GetMinesForTeam(1)
  check(#m == 2 and m[1] == W.wells[1] and m[2] == W.wells[2], 'Mortal owns 2')
  m = nBattlegrounds.GetMinesForTeam(2, true)
  check(#m == 1 and m[1] == W.wells[3], 'Spirit unlocked: 1 (UI data)')
  check(nBattlegrounds.IsTitanNodeLocked(W.wells[1]) == true and nBattlegrounds.IsTitanNodeLocked(W.wells[2]) == false,
        'locks from the UI data')
  for i = 1, 5 do
    W.add({ name = 'Titan_Node_Follower', team = 1, pos = P(-182.3 + 3 * i, 27.9), npc = nNPCType.Follower })
  end
  W.add({ name = 'Titan_Node_Follower', team = 1, pos = P(-100, 27.9), npc = nNPCType.Follower })  -- too far
  check(nBattlegrounds.GetFollowersForTitan(W.wells[1]) == 5, 'workers around the well')
  check(nBattlegrounds.GetFollowersForTitan(kObjIDNone) == 0, 'no well')
  -- without the UI-data bindings: locked until 900 s
  local closest = nBattlegrounds.SpiritWell_GetClosest
  nBattlegrounds.SpiritWell_GetClosest = nil
  W.matchTime = 899
  check(nBattlegrounds.IsTitanNodeLocked(W.wells[2]) == true, 'first lock by the clock')
  W.matchTime = 900
  check(nBattlegrounds.IsTitanNodeLocked(W.wells[2]) == false, 'unlocked at 900 s')
  nBattlegrounds.SpiritWell_GetClosest = closest
  -- with the host (D9's Wells)
  nBotHost = {
    MinesForTeam = function(team, onlyUnlocked) return { W.wells[team], onlyUnlocked and 'u' or 'a' } end,
    FollowersForTitan = function() return 17 end,
    IsTitanNodeLocked = function(w) return w == W.wells[4] end,
  }
  m = nBattlegrounds.GetMinesForTeam(2, true)
  check(m[1] == W.wells[2] and m[2] == 'u', 'forwarded with onlyUnlocked')
  check(nBattlegrounds.GetMinesForTeam(1)[2] == 'a', 'onlyUnlocked false when absent')
  check(nBattlegrounds.GetFollowersForTitan(W.wells[1]) == 17, 'host workers')
  check(nBattlegrounds.IsTitanNodeLocked(W.wells[4]) == true and nBattlegrounds.IsTitanNodeLocked(W.wells[3]) == false,
        'host locks')
end)

-- ---- lanes ---------------------------------------------------------------------------------------------------------
test('lanes without the host', function()
  local bot = shaper('bot', 1, -131.6, -44.2, { bot = true })  -- on the Top lane
  local human = shaper('human', 1, 66, -68, {})                 -- on the Bottom lane
  local jungler = shaper('jungler', 1, -60, -100, {})           -- the jungle (x - y = 40: the stub tags it Bottom)
  local inBase = shaper('inbase', 1, -195, -205, {})
  check(nBot.GetLaneID(bot) == nLaneID.Top, 'by tags before an assignment')
  nBot.AssignLane(bot, nLaneID.Bottom)
  check(nBot.GetLaneID(bot) == nLaneID.Bottom, 'assigned')
  nBot.AssignLane(bot, 7)
  check(nBot.GetLaneID(bot) == nLaneID.Top, 'a bad lane clears it')
  check(nBot.GetLaneID(human) == nLaneID.Bottom, 'a human by tags')
  check(nBot.GetLaneID(inBase) == nLaneID.None, 'in the base: None')
  check(nBot.GetClosestLane(inBase) == nLaneID.Bottom, 'closest from (-195, -205): y - x < 0 -> Bottom')
  check(nBot.GetClosestLane(jungler) == nLaneID.Bottom, 'jungle')
  check(nBot.GetLaneID(kObjIDNone) == nLaneID.None, 'none')
  -- the lane centre without the host: the lane's middle, the Mortal direction toward the Spirit base
  local c, d = nBot.GetLaneCenterAndDirection(nLaneID.Top, nTeamID.Mortal)
  check(near(c.x, -69.5) and near(c.y, 68.2) and d.x > 0 and d.y > 0, 'Top, Mortal')
  local c2, d2 = nBot.GetLaneCenterAndDirection(nLaneID.Top, nTeamID.Spirit)
  check(near(c2.x, c.x) and near(d2.x, -d.x) and near(d2.y, -d.y), 'Top, Spirit: the other way')
  c, d = nBot.GetLaneCenterAndDirection(nLaneID.Bottom, nTeamID.Mortal)
  check(near(c.x, 66.5) and near(c.y, -68.7), 'Bottom')
  c, d = nBot.GetLaneCenterAndDirection(9, 1)
  -- NULLPOS: never the null position; the top lane's front (and a NULLPOS report)
  check(near(c.x, -69.5) and near(c.y, 68.2) and near(d.x, 0.7333), 'an unknown lane: the top front, not (0, 0)')
  -- the results are nVectors (callers do vector arithmetic)
  c, d = nBot.GetLaneCenterAndDirection(nLaneID.Top, 1)
  local behind = c + d * -12
  check(near(behind.x, -69.5 - 12 * 0.7333), 'vector arithmetic on the result')
  -- the object form: the object's lane and team
  nBot.AssignLane(bot, nLaneID.Bottom)
  c = nBot.GetLaneCenterAndDirection(bot)
  check(near(c.x, 66.5), 'object form: the bot lane')
  c = nBot.GetLaneCenterAndDirection(inBase, 5)
  check(near(c.x, 66.5), 'object form off the lanes: the closest lane')
end)

test('lanes with the host', function()
  local bot = shaper('bot', 2, 0, 0, { bot = true })
  local lanes, cx = {}, 10
  nBotHost = {
    AssignLane = function(b, lane) lanes[b] = lane end,
    GetLane = function(b) return lanes[b] or 0 end,
    ClosestLane = function(x, y, z) return nLaneID.Bottom end,
    LaneCenterAndDirection = function(lane, team) return cx, lane * 100, 0, 0, team == 2 and -1 or 1, 0 end,
  }
  nBot.AssignLane(bot, nLaneID.Top)
  check(lanes[bot] == nLaneID.Top and nBot.GetLaneID(bot) == nLaneID.Top, 'host lanes')
  check(dgsim_bots.lanes[bot] == nil, 'nothing kept in Lua')
  local c, d = nBot.GetLaneCenterAndDirection(nLaneID.Bottom, 2)
  check(near(c.x, 10) and near(c.y, 200) and near(d.y, -1), 'host centre')
  -- the object form keeps its centre while the new one is within the threshold
  c = nBot.GetLaneCenterAndDirection(bot, 5)
  check(near(c.x, 10) and near(c.y, 100), 'first')
  cx = 13
  c = nBot.GetLaneCenterAndDirection(bot, 5)
  check(near(c.x, 10), 'moved 3: kept')
  cx = 20
  c = nBot.GetLaneCenterAndDirection(bot, 5)
  check(near(c.x, 20), 'moved 10: new centre')
  cx = 22
  c = nBot.GetLaneCenterAndDirection(bot)
  check(near(c.x, 20), 'default threshold 5')
  -- the closest lane through the host's polylines when the tags say nothing
  local jungle = shaper('j', 1, -100, -60, {})  -- the stub tags it Top (y - x = 40); force the host by the base
  local base = shaper('b', 1, -195, -205, {})
  check(nBot.GetClosestLane(base) == nLaneID.Bottom, 'host closest lane')
  check(nBot.GetClosestLane(jungle) == nLaneID.Top, 'tags first')
end)

-- ---- missions, shopping, first-time players ------------------------------------------------------------------------
test('missions without the host', function()
  local bot = shaper('bot', 1, 0, 0, { bot = true })
  nBot.RequestMission(1, nMissionType.AttackWell, bot, 2, 0, 'g')
  check(nBot.IsMissionAvailable(1, nMissionType.AttackWell) == false, 'no board: nothing available')
  check(nBot.GetAvailableMission(bot, nMissionType.AttackWell, 80) == 0, 'id 0')
  check(nBot.GetMissionID(bot) == 0, 'GetMissionID 0')
  check(nBot.GetMissionTarget(bot) == kObjIDNone, 'target kObjIDNone')
  local d = nBot.GetMissionDestination(bot)
  check(type(d) == 'table' and d.x == 0 and d.y == 0, 'destination: the zero vector')
  check(nBot.AcceptMission(bot, 3) == false, 'accept: false')
  check(dgsim_bots.MissionTick({}) == 0, 'driver with nothing')
end)

-- A Lua stand-in for the host's board (the nBotHost contract of gms/core/bots/bots.h)
local function hostBoard()
  local board = { missions = {}, byBot = {}, nextId = 1, requests = {} }
  local function find(id) for _, m in ipairs(board.missions) do if m.id == id then return m end end end
  nBotHost = {
    RequestMission = function(team, mtype, target, agents, timeout, guid, hasDest, dx, dy, dz)
      board.requests[#board.requests + 1] = { team, mtype, target, agents, timeout, guid, hasDest, dx, dy, dz }
      for _, m in ipairs(board.missions) do
        if m.team == team and m.type == mtype and m.target == target then return m.id end
      end
      local m = { id = board.nextId, team = team, type = mtype, target = target, agents = agents, guid = guid,
                  dest = hasDest and { dx, dy, dz } or nil }
      board.nextId = board.nextId + 1
      board.missions[#board.missions + 1] = m
      return m.id
    end,
    IsMissionAvailable = function(team, mtype)
      for _, m in ipairs(board.missions) do if m.team == team and m.type == mtype then return true end end
      return false
    end,
    GetAvailableMission = function(bot, mtype, range)
      for _, m in ipairs(board.missions) do if m.type == mtype then return m.id end end
      return 0
    end,
    AcceptMission = function(bot, id) if find(id) then board.byBot[bot] = id return true end return false end,
    GetMissionID = function(bot) return board.byBot[bot] or 0 end,
    GetMissionTarget = function(bot) local m = find(board.byBot[bot]); return m and m.target or kObjIDNone end,
    GetMissionDestination = function(bot)
      local m = find(board.byBot[bot])
      if m and m.dest then return m.dest[1], m.dest[2], m.dest[3] end
      return 0, 0, 0
    end,
    OpenMission = function(team, i)
      local k = 0
      for _, m in ipairs(board.missions) do
        if m.team == team then
          k = k + 1
          if k == i then return m.id, m.target, m.guid end
        end
      end
      return nil
    end,
    EndMission = function(id)
      for i, m in ipairs(board.missions) do
        if m.id == id then table.remove(board.missions, i) return true end
      end
      return false
    end,
  }
  return board
end

test('missions with the host', function()
  local board = hostBoard()
  local bot = shaper('bot', 1, 0, 0, { bot = true })
  local well = W.add({ name = 'well', team = 2, pos = P(10, 0), npc = nNPCType.GoldMine })
  nBot.RequestMission(1, nMissionType.Rally, well, 5, 0, 'spid:x', nVector.create(-36, -6, 0))
  local r = board.requests[1]
  check(r[1] == 1 and r[2] == nMissionType.Rally and r[3] == well and r[4] == 5 and r[5] == 0 and r[6] == 'spid:x'
        and r[7] == true and r[8] == -36 and r[9] == -6 and r[10] == 0, 'arguments with a destination')
  nBot.RequestMission(1, nMissionType.DefendWell, well, 2, 5)  -- condition_Mission_DefendWell passes no GUID
  r = board.requests[2]
  check(r[6] == kObjIDNone and r[7] == false and r[8] == 0, 'arguments without GUID and destination')
  nBot.RequestMission(nil, 3, well, 2, 0, 'g')
  check(#board.requests == 2, 'no team: not forwarded')
  check(nBot.IsMissionAvailable(1, nMissionType.Rally) == true, 'available')
  local id = nBot.GetAvailableMission(bot, nMissionType.Rally, 0)
  check(id == 1, 'id ' .. tostring(id))
  check(nBot.AcceptMission(bot, id) == true, 'accept')
  check(nBot.GetMissionID(bot) == 1 and nBot.GetMissionTarget(bot) == well, 'mission and target')
  local d = nBot.GetMissionDestination(bot)
  check(near(d.x, -36) and near(d.y, -6), 'destination')
end)

test('mission driver', function()
  local board = hostBoard()
  local wellA = W.add({ name = 'wellA', team = 2, pos = P(10, 0), npc = nNPCType.GoldMine })
  local wellB = W.add({ name = 'wellB', team = 2, pos = P(20, 0), npc = nNPCType.GoldMine })
  -- two test conditions in the mission conditions' shape (Check(team) requests, CheckEnd(team, target) ends)
  condition_Mission_TestA = { conditionGUID = 'spid:a', namespace = 'condition_Mission_TestA' }
  condition_Mission_TestA[nConditionFns.Check] = function(team)  -- like condition_Mission_CaptureWell
    for _, w in ipairs({ wellA, wellB }) do
      if nGameObject.GetTeam(w) ~= team then nBot.RequestMission(team, nMissionType.AttackWell, w, 2, 0, 'spid:a') end
    end
    return true, kObjIDNone, 0
  end
  condition_Mission_TestA[nConditionFns.CheckEnd] = function(team, target) return nGameObject.GetTeam(target) == team end
  condition_Mission_TestB = { conditionGUID = 'spid:b', namespace = 'condition_Mission_TestB' }
  condition_Mission_TestB[nConditionFns.Check] = function(team) error('boom ' .. team) end
  local names = { 'condition_Mission_TestA', 'condition_Mission_TestB', 'condition_Missing' }
  local checksRun, ends, errors = dgsim_bots.MissionTick(names)
  check(checksRun == 4 and ends == 0 and errors == 2, 'tick 1: ' .. checksRun .. ' ' .. ends .. ' ' .. errors)
  check(#board.missions == 2, 'two missions open')
  W.get(wellA).team = 1  -- captured
  checksRun, ends, errors = dgsim_bots.MissionTick(names)
  -- Mortal's mission on well A ends (its CheckEnd: the well is Mortal's now); Spirit now attacks well A
  local open = {}
  for _, m in ipairs(board.missions) do open[#open + 1] = m.team .. ':' .. tostring(m.target) end
  check(ends == 1 and table.concat(open, ' ') == '1:id:wellB 2:id:wellA', 'the captured well ends: ' ..
        table.concat(open, ' '))
  local counted = 0
  for _, n in pairs(dgsim_bots.stats.errors) do counted = counted + n end
  check(counted == 4 and #dgsim_bots.stats.errorOrder == 2, 'errors counted per distinct text: ' .. counted)
  -- a CheckEnd that raises is counted, the missions stay
  condition_Mission_TestA[nConditionFns.CheckEnd] = function() error('end boom') end
  checksRun, ends, errors = dgsim_bots.MissionTick({ 'condition_Mission_TestA' })
  check(ends == 0 and errors == 2 and #board.missions == 2, 'CheckEnd errors')
  -- the default names are Tuning.BotPlan's seven
  check(#dgsim_bots.config.missionConditions == 7, 'seven mission conditions')
end)

test('shopping and first-time players', function()
  local bot = shaper('bot', 1, 0, 0, { bot = true })
  local human = shaper('human', 2, 10, 0, {})
  local otherBot = shaper('otherbot', 2, 12, 0, { bot = true })
  check(nBot.CanAffordNextItem(bot) == false and nBot.CanBuyPotion(bot) == false, 'no host: nothing to buy')
  nBot.PurchaseNextItem(bot)
  nBot.PurchasePotions(bot)
  check(nBot.IsEnemyOfFirstTimePlayer(bot) == false, 'no host: false')
  local bought = {}
  nBotHost = {
    CanAffordNextItem = function(b) return b == bot end,
    PurchaseNextItem = function(b) bought[#bought + 1] = 'item' end,
    CanBuyPotion = function() return true end,
    PurchasePotions = function(b) bought[#bought + 1] = 'potions' end,
    IsFirstTimePlayer = function(o) return o == human or o == otherBot end,
  }
  check(nBot.CanAffordNextItem(bot) == true and nBot.CanBuyPotion(bot) == true, 'host answers')
  nBot.PurchaseNextItem(bot)
  nBot.PurchasePotions(bot)
  check(bought[1] == 'item' and bought[2] == 'potions', 'purchases forwarded')
  check(nBot.IsEnemyOfFirstTimePlayer(bot) == true, 'an enemy human in the first game')
  check(nBot.IsEnemyOfFirstTimePlayer(human) == false, 'the human: its enemy is a bot (bots never count)')
  check(nBot.IsEnemyOfFirstTimePlayer(otherBot) == false, 'team 2: no enemy first-timer')
end)

test('unit-test API and the report', function()
  local bot = shaper('bot', 1, 0, 0, { bot = true, level = 5 })
  local enemy = shaper('enemy', 2, 10, 0, { level = 8 })
  W.get(bot).target = enemy
  W.time = 50
  local lost, t = nBot.LostSightOfTarget(bot)
  check(lost == false and t == enemy, 'in sight')
  local p = nBot.GetLastSeenEnemyPosition(bot)
  check(near(p.x, 10), 'last seen now')
  W.get(enemy).visible[1] = false
  W.get(enemy).pos = P(40, 0)
  W.time = 51
  lost, t = nBot.LostSightOfTarget(bot)
  check(lost == true and t == enemy, 'lost')
  p = nBot.GetLastSeenEnemyPosition(bot)
  check(near(p.x, 10), 'still where it was last seen')
  W.get(enemy).visible[1] = true
  W.get(enemy).pos = P(10, 0)
  check(nBot.IsEnemyStronger(bot, 30, 3) == true and nBot.IsEnemyStronger(bot, 30, 4) == false, 'level gap')
  v5Structures()
  local text = dgsim_bots.Report()
  check(type(text) == 'string' and string.find(text, 'Bindings') ~= nil and string.find(text, 'calls:') ~= nil,
        'report')
end)

-- D6b: the two script fix-ups apply once the scripts' tables exist (and only once), and stay off when not wanted
test('fixups', function()
  nAbilityFns = nAbilityFns or { Activate = 0, Tick = 1, Deactivate = 2 }  -- GlobalDefinitions' table in the game
  dgsim_bots.fixups = { veryOutnumbered = false, groupInLane = false }
  dgsim_bots.fixupsWanted = nil
  condition_Bot_VeryOutnumbered = nil
  ability_Bot_GroupInLane = nil
  local s = dgsim_bots.ApplyFixups()
  check(string.find(s, 'VeryOutnumbered false') ~= nil and string.find(s, 'GroupInLane false') ~= nil, 'nothing yet: ' .. s)
  condition_Bot_VeryOutnumbered = { defaultSearchRadius = 30 }
  local original = function() error('the original Tick') end
  ability_Bot_GroupInLane = { searchRadius = 40, descriptors = 0 }
  ability_Bot_GroupInLane[nAbilityFns.Tick] = original
  s = dgsim_bots.ApplyFixups()
  check(condition_Bot_VeryOutnumbered.searchRadius == 30, 'VeryOutnumbered.searchRadius')
  check(ability_Bot_GroupInLane[nAbilityFns.Tick] == dgsim_bots.groupInLaneTick, 'GroupInLane Tick replaced')
  check(string.find(s, 'VeryOutnumbered true') ~= nil and string.find(s, 'GroupInLane true') ~= nil, s)
  condition_Bot_VeryOutnumbered.searchRadius = 12  -- applied once: a later value stays
  dgsim_bots.ApplyFixups()
  check(condition_Bot_VeryOutnumbered.searchRadius == 12, 'applied once')
  dgsim_bots.fixups = { veryOutnumbered = false, groupInLane = false }
  dgsim_bots.fixupsWanted = false
  condition_Bot_VeryOutnumbered = { defaultSearchRadius = 30 }
  check(dgsim_bots.ApplyFixups() == 'fix-ups off' and condition_Bot_VeryOutnumbered.searchRadius == nil, 'off')
  dgsim_bots.fixupsWanted = nil
  condition_Bot_VeryOutnumbered = nil
  ability_Bot_GroupInLane = nil
end)

-- D6c: BailOnKill's "is an enemy near" search reaches as far as EngageForKill's, only inside its own Check
test('fixup bailOnKill', function()
  dgsim_bots.fixups = { veryOutnumbered = true, groupInLane = true, bailOnKill = false }
  dgsim_bots.fixupsWanted = nil
  local seen = {}
  local hadUtils = nBotUtils ~= nil
  nBotUtils = nBotUtils or {}
  local savedNear = nBotUtils.IsEnemyPlayerNear
  nBotUtils.IsEnemyPlayerNear = function(o, r)
    seen[#seen + 1] = r
    return r >= 40, r >= 40 and 'enemy' or kObjIDNone
  end
  condition_Bot_EngageForKill = { enemySearchRadius = 55 }
  condition_Bot_BailOnKill = { enemySearchRadius = 30 }
  condition_Bot_BailOnKill[1] = function(bot)
    local found = nBotUtils.IsEnemyPlayerNear(bot, condition_Bot_BailOnKill.enemySearchRadius)
    local other = nBotUtils.IsEnemyPlayerNear('someone else', condition_Bot_BailOnKill.enemySearchRadius)
    return not found, other
  end
  local s = dgsim_bots.ApplyFixups()
  check(string.find(s, 'BailOnKill true') ~= nil, s)
  local bail, other = condition_Bot_BailOnKill[1]('bot')
  check(bail == false and seen[1] == 55, 'the bot looks 55 units out and stays')
  check(other == false and seen[2] == 30, 'another object keeps the 30')
  check(nBotUtils.IsEnemyPlayerNear('bot', 30) == false and seen[3] == 30, 'restored after the call')
  condition_Bot_BailOnKill[1] = function() error('boom') end
  dgsim_bots.fixups.bailOnKill = false
  dgsim_bots.ApplyFixups()
  local ok = pcall(condition_Bot_BailOnKill[1], 'bot')
  check(not ok and nBotUtils.IsEnemyPlayerNear('bot', 30) == false, 'an error passes and restores')
  nBotUtils.IsEnemyPlayerNear = savedNear
  if not hadUtils then nBotUtils = nil end
  condition_Bot_EngageForKill = nil
  condition_Bot_BailOnKill = nil
end)

-- DESECBOT-3 (fix-up 3b): no EngageForKill that BailOnKill would leave at once; the early Desecrator neither engages
-- nor stays on a target beyond his E or under its tower
test('fixup engageFix: no bounce, the early Desecrator (DESECBOT-3)', function()
  local B = dgsim_bots
  B.fixupsWanted, B.engageFixWanted, B.desecKitWanted, B.kitsWanted = nil, nil, nil, nil
  B.fixups = { veryOutnumbered = true, groupInLane = true, bailOnKill = false, parasite = true, campRadius = true,
               engageFix = false }
  local hadUtils = nBotUtils ~= nil
  nBotUtils = nBotUtils or {}
  local savedTower, savedSet, savedNear = nBotUtils.IsAllyTowerNear, nGameObject.SetTargetID, nBotUtils.IsEnemyPlayerNear
  nBotUtils.IsEnemyPlayerNear = function() return false, kObjIDNone end
  local towered = {}
  nBotUtils.IsAllyTowerNear = function(o, r) return towered[o] == true and r >= 25 end
  nGameObject.SetTargetID = function(o, t) W.get(o).best = t end
  local guard = shaper('guardian', 1, 0, 0, { bot = true, level = 2 })
  local des = shaper('desecrator', 1, 0, 0, { bot = true, level = 2 })
  local low = shaper('warden', 2, 16, 0, { maxhp = 1000, hp = 300 })
  local farOne = shaper('far', 2, 30, 0, { maxhp = 1000, hp = 300 })
  local old = shaper('old', 2, -10, 0)
  local bails = false
  condition_Bot_EngageForKill = { [1] = function(bot)
    local t = W.get(bot).aim
    nGameObject.SetTargetID(bot, t)
    return true, 0, 0, 0, t
  end }
  condition_Bot_BailOnKill = { [1] = function() return bails end }
  local s = B.ApplyFixups()
  check(s:find('EngageFix true') ~= nil and s:find('BailOnKill true') ~= nil, s)
  local EFK, BAIL = condition_Bot_EngageForKill[1], condition_Bot_BailOnKill[1]
  -- the Guardian: engages when BailOnKill would stay, not when it would leave (the target put back)
  W.get(guard).aim, W.get(guard).best = low, old
  local ok, _, _, _, t = EFK(guard)
  check(ok == true and t == low and W.get(guard).best == low, 'engages: BailOnKill stays')
  bails = true
  W.get(guard).best = old
  check(EFK(guard) == false and W.get(guard).best == old, 'refused: BailOnKill would leave at once; the old target back')
  B.engageFixWanted = false
  check(EFK(guard) == true, 'bot_engage_fix=0: the script alone')
  check(B.ApplyFixups():find('EngageFix off') ~= nil, 'bot_engage_fix=0: the fix-ups line says off')
  B.engageFixWanted = nil
  bails = false
  -- the Desecrator at level 2: not beyond his E (22), not under the target's tower
  W.get(des).aim = farOne
  check(EFK(des) == false, 'early Desecrator: a target 30 away is refused')
  W.get(des).aim = low
  check(EFK(des) == true, 'early Desecrator: 16 away, no tower: engages')
  towered[low] = true
  check(EFK(des) == false, 'early Desecrator: the target under its tower is refused')
  W.get(des).best = low
  check(BAIL(des) == true, 'early Desecrator: BailOnKill leaves a target under its tower')
  towered[low] = nil
  check(BAIL(des) == false, 'early Desecrator: and stays otherwise')
  W.get(des).best = farOne
  check(BAIL(des) == true, 'early Desecrator: BailOnKill leaves a target beyond his E')
  -- from level 4 the AI's own rules; the Guardian never had them
  W.get(des).level = 4
  check(BAIL(des) == false, 'level 4: the script decides')
  W.get(des).aim = farOne
  check(EFK(des) == true, 'level 4: engages a target 30 away')
  W.get(guard).aim = farOne
  W.get(guard).best = farOne
  check(EFK(guard) == true and BAIL(guard) == false, 'the Guardian: no early rule')
  W.get(des).level = 2
  B.desecKitWanted = false
  check(EFK(des) == true, 'bot_desec_kit=0: no early rule')
  B.desecKitWanted = nil
  -- an error in the script passes
  condition_Bot_EngageForKill[1] = function() error('boom') end
  B.fixups.engageFix = false
  B.ApplyFixups()
  check(not pcall(condition_Bot_EngageForKill[1], guard), 'an error passes')
  nBotUtils.IsAllyTowerNear, nGameObject.SetTargetID, nBotUtils.IsEnemyPlayerNear = savedTower, savedSet, savedNear
  if not hadUtils then nBotUtils = nil end
  condition_Bot_EngageForKill, condition_Bot_BailOnKill = nil, nil
end)

-- KILLFINISH: BailOnKill's reasons go to the host; the host's "would finish" keeps an engage fix-up 3b would refuse.
-- BOTCYCLE (fix-up 3c): a KillShaper join takes the mission's target and passes only when BailOnKill would stay or the
-- host would finish
test('fixup joinFix and the BailOnKill reasons (KILLFINISH, BOTCYCLE)', function()
  local B = dgsim_bots
  B.fixupsWanted, B.engageFixWanted, B.joinFixWanted, B.desecKitWanted = nil, nil, nil, nil
  B.fixups = { veryOutnumbered = true, groupInLane = true, bailOnKill = false, parasite = true, campRadius = true,
               engageFix = false, joinFix = false }
  local hadUtils = nBotUtils ~= nil
  nBotUtils = nBotUtils or {}
  local savedTower, savedSet, savedNear = nBotUtils.IsAllyTowerNear, nGameObject.SetTargetID, nBotUtils.IsEnemyPlayerNear
  local savedPCO = nPlayer.IsPlayerControlledObject
  nPlayer.IsPlayerControlledObject = function(o) return W.get(o) ~= nil and W.get(o).player == true end
  nGameObject.SetTargetID = function(o, t) W.get(o).best = t end
  local bot = shaper('creature', 1, 0, 0, { bot = true, level = 5, maxhp = 1000, hp = 200 })
  local low = shaper('warden', 2, 10, 0, { maxhp = 1000, hp = 250, level = 5 })
  local minion = W.add({ name = 'minion', team = 2, pos = P(5, 5) })
  local nearest = low
  nBotUtils.IsEnemyPlayerNear = function() return nearest ~= nil, nearest or kObjIDNone end
  local towered = false
  nBotUtils.IsAllyTowerNear = function() return towered end
  local hostWhy, holds, asked, engageBlocked = {}, false, 0, false
  local missions = { { 19, low } }
  nBotHost = {
    BailWhy = function(o, why) hostWhy[#hostWhy + 1] = why end,
    KillFinish = function() asked = asked + 1; return holds end,
    EngageBlocked = function() return engageBlocked end,
    OpenMission = function(team, i) local m = missions[i]; if m then return m[1], m[2] end end,
  }
  condition_Bot_BailOnKill = { enemySearchRadius = 30, searchRadius = 50, towerSearchRadius = 25,
    lowHealthThreshold = 0.3, enemyHealthThreshold = 0.49, increasedThresholdPerAlly = 0.12,
    decreasedThresholdPerEnemy = 0.09, braveryFactor = 0.5, levelGapCutoff = 2, enemySearchHistory = 5 }
  local bails = true
  condition_Bot_BailOnKill[1] = function() return bails end
  condition_Bot_EngageForKill = { enemySearchRadius = 55, [1] = function(b)
    nGameObject.SetTargetID(b, low)
    return true, 0, 0, 0, low
  end }
  condition_Bot_IsMissionAvailable = { defaultMission = 2, [nConditionFns.Check] = function(b, mt, range)
    return true, kObjIDNone, 19
  end }
  local s = B.ApplyFixups()
  check(s:find('JoinFix true') ~= nil and s:find('EngageFix true') ~= nil, s)
  -- the reasons, in the script's order: the bot at 20 %, the enemy at 25 % (low enough, but above the bot's own)
  W.get(bot).best = low
  check(B.BailWhy(bot) == "its HP above the bot's", 'relative HP: ' .. tostring(B.BailWhy(bot)))
  W.get(bot).hp = 300
  check(B.BailWhy(bot) == 'another rule', 'the bot at 30 %: none of the tests')
  towered = true
  check(B.BailWhy(bot) == 'its tower near', 'its tower')
  towered = false
  W.get(low).level = 8
  check(B.BailWhy(bot) == 'level gap', 'level gap')
  W.get(low).level = 5
  W.get(low).hp = 600
  check(B.BailWhy(bot) == 'the enemy not low', 'the enemy at 60 %')
  W.get(low).hp = 250
  W.get(bot).best = kObjIDNone
  check(B.BailWhy(bot) == 'no target', 'no target')
  W.get(bot).best = low
  nearest = nil
  check(B.BailWhy(bot) == 'no enemy shaper near', 'nobody near')
  nearest = low
  W.get(bot).hp = 200
  -- the wrapper hands the reason to the host when it bails
  check(condition_Bot_BailOnKill[1](bot) == true and hostWhy[#hostWhy] == "its HP above the bot's", 'host got the reason')
  -- fix-up 3b with the host: refused while it wouldn't finish, kept when it would
  W.get(bot).best = minion
  check(condition_Bot_EngageForKill[1](bot) == false and W.get(bot).best == minion, '3b refuses, target back')
  holds = true
  check(condition_Bot_EngageForKill[1](bot) == true and W.get(bot).best == low, '3b: the host would finish: engages')
  holds = false
  -- BOTCYCLE: an engage left at once blocks the next ones (unless the host would finish)
  bails, engageBlocked = false, true
  W.get(bot).best = minion
  check(condition_Bot_EngageForKill[1](bot) == false and W.get(bot).best == minion, 'engage blocked, target back')
  holds = true
  check(condition_Bot_EngageForKill[1](bot) == true, 'blocked, but the host would finish: engages')
  holds, bails, engageBlocked = false, true, false
  -- fix-up 3c: a KillShaper join
  local IMA = condition_Bot_IsMissionAvailable[nConditionFns.Check]
  W.get(bot).best = minion
  check(IMA(bot, 8, 60) == false and W.get(bot).best == minion, 'join refused: BailOnKill would leave; target back')
  bails = false
  local ok, _, id = IMA(bot, 8, 60)
  check(ok == true and id == 19 and W.get(bot).best == low, 'join: the mission target is the bot\'s target')
  engageBlocked = true
  W.get(bot).best = minion
  check(IMA(bot, 8, 60) == false and W.get(bot).best == minion, 'join refused while the host blocks it, target kept')
  engageBlocked = false
  bails = true
  holds = true
  W.get(bot).best = minion
  check(IMA(bot, 8, 60) == true and W.get(bot).best == low, 'join kept: the host would finish')
  holds = false
  W.get(bot).best = minion
  check(IMA(bot, 2) == true and W.get(bot).best == minion, 'another mission type: untouched')
  B.joinFixWanted = false
  check(IMA(bot, 8, 60) == true and W.get(bot).best == minion, 'bot_join_fix=0: the script alone')
  check(B.ApplyFixups():find('JoinFix off') ~= nil, 'the fix-ups line says off')
  B.joinFixWanted = nil
  missions = {}
  bails = false
  check(IMA(bot, 8, 60) == true and W.get(bot).best == minion, 'mission gone: no target to take')
  check(asked >= 3, 'the host was asked')
  nBotUtils.IsAllyTowerNear, nGameObject.SetTargetID, nBotUtils.IsEnemyPlayerNear = savedTower, savedSet, savedNear
  nPlayer.IsPlayerControlledObject = savedPCO
  if not hadUtils then nBotUtils = nil end
  nBotHost = nil
  condition_Bot_EngageForKill, condition_Bot_BailOnKill, condition_Bot_IsMissionAvailable = nil, nil, nil
end)

-- BOTIDLE: the Hunter's IsNearNextCreepCamp radius widens from 5 to the host's value (once; 0 keeps the script's)
test('fixup campRadius', function()
  dgsim_bots.fixupsWanted = nil
  dgsim_bots.fixups = { veryOutnumbered = true, groupInLane = true, bailOnKill = true, parasite = true, campRadius = false }
  condition_Bot_IsNearNextCreepCamp = nil
  local s = dgsim_bots.ApplyFixups()
  check(string.find(s, 'CampRadius false') ~= nil, 'nothing yet: ' .. s)
  condition_Bot_IsNearNextCreepCamp = { radius = 5 }
  dgsim_bots.campRadius = 9
  s = dgsim_bots.ApplyFixups()
  check(condition_Bot_IsNearNextCreepCamp.radius == 9 and string.find(s, 'CampRadius true') ~= nil, 'widened: ' .. s)
  condition_Bot_IsNearNextCreepCamp.radius = 6
  dgsim_bots.ApplyFixups()
  check(condition_Bot_IsNearNextCreepCamp.radius == 6, 'applied once')
  dgsim_bots.fixups.campRadius = false
  condition_Bot_IsNearNextCreepCamp = { radius = 5 }
  dgsim_bots.campRadius = 0
  dgsim_bots.ApplyFixups()
  check(condition_Bot_IsNearNextCreepCamp.radius == 5, '0 keeps the script radius')
  dgsim_bots.fixups.campRadius = false
  condition_Bot_IsNearNextCreepCamp = { radius = 12 }
  dgsim_bots.campRadius = 9
  dgsim_bots.ApplyFixups()
  check(condition_Bot_IsNearNextCreepCamp.radius == 12, 'never narrows')
  dgsim_bots.campRadius = nil
  condition_Bot_IsNearNextCreepCamp = nil
end)

-- ---- BOTCOMP: the setup's role rules ------------------------------------------------------------------------------
test('composition: pure rules', function()
  local B = dgsim_bots
  check(B.OutnumbersInLane(1, 0) and B.OutnumbersInLane(2, 2) and B.OutnumbersInLane(3, 1), 'allies >= enemies')
  check(not B.OutnumbersInLane(0, 0) and not B.OutnumbersInLane(1, 2), 'alone, or fewer allies than enemies: no')
end)

-- a world with the setup's globals: roles by creature, the missions of a fake host board
local function compositionWorld()
  local B = dgsim_bots
  local roles, missions, held, ended = {}, {}, {}, {}
  nDirector = { GetParasiteID = function() return kObjIDNone end,
                GetClosestCampPosition = function() return nVector.create(-59, -57, 1) end }
  function nGameObject.IsInCombat(id) return W.get(id).combat == true end
  nBotHost = {
    BotRole = function(bot) return roles[bot] or -1 end,
    GetAvailableMission = function(bot, mtype, range)
      return missions[mtype] or 0
    end,
    GetMissionID = function(bot) return held[bot] or 0 end,
    GetMissionTarget = function(bot) return W.wellOf and W.wellOf[bot] or kObjIDNone end,
    OpenMission = function(team, i)
      if i == 1 and held.open then return held.open, held.target, held.guid end
      return nil
    end,
    EndMission = function(id) ended[#ended + 1] = id end,
  }
  condition_Mission_HarassWell = { conditionGUID = 'harass', startWorkers = 16, endWorkers = 4 }
  condition_nearest_minion = { [1] = function() return false end }
  condition_Bot_IsNearMissionTarget = { [1] = function() return false end }
  condition_nearest_objective = { [1] = function(bot) return true, 0, 0, 0, W.objectiveFor end }
  condition_Bot_ShouldGank = { [1] = function() return true, 1, 2, 3 end }
  condition_Bot_NextCreepCamp = { [1] = function(bot)
    local v = nDirector.GetClosestCampPosition(1, nVector.create(0, 0, 0), false)
    return true, v.x, v.y, v.z, kObjIDNone, true
  end }
  condition_Bot_IsNearNextCreepCamp = { [1] = function(bot)
    return nDirector.GetClosestCampPosition(1, nVector.create(0, 0, 0), false).x == -59
  end }
  B.compositionWanted = true
  B.fixups = { veryOutnumbered = true, groupInLane = true, bailOnKill = true, parasite = true, campRadius = true }
  return roles, missions, held, ended
end

test('composition: the Predator harasses only while his team outnumbers the enemy in his lane', function()
  local B = dgsim_bots
  local roles, missions, held, ended = compositionWorld()
  local pred = shaper('pred', 1, 60, -60, { bot = true })
  local glad = shaper('glad', 1, 66, -68, { bot = true })
  local other = shaper('other', 1, 62, -62, { bot = true })
  roles[pred], roles[glad], roles[other] = 3, 0, 1
  nBot.AssignLane(pred, nLaneID.Bottom)
  missions[3], missions[12] = 31, 77
  local s = B.ApplyFixups()
  check(B.fixups.composition == true and string.find(s, 'Composition true') ~= nil, 'applied: ' .. s)
  check(condition_Mission_HarassWell.startWorkers == 4 or B.harassStartWorkers == nil, 'thresholds from the host')
  -- two allies in his lane, no enemy: he takes the harass mission instead of the capture one; the others do not
  check(nBot.GetAvailableMission(pred, nMissionType.AttackWell, 80) == 77, 'the Predator gets HarassWell')
  check(nBot.GetAvailableMission(glad, nMissionType.AttackWell, 80) == 31, 'the Gladiator gets the capture mission')
  check(nBot.GetAvailableMission(pred, nMissionType.DefendWell, 80) == 0, 'other types pass through')
  -- as many enemies as allies in the lane: still outnumbering; one more enemy: no mission at all
  shaper('e1', 2, 70, -68)
  shaper('e2', 2, 71, -68)
  check(nBot.GetAvailableMission(pred, nMissionType.AttackWell, 80) == 77, '2 allies against 2 enemies: go on')
  shaper('e3', 2, 72, -68)
  check(nBot.GetAvailableMission(pred, nMissionType.AttackWell, 80) == 0, 'outnumbered: none (and never the capture)')
  check(nBot.GetAvailableMission(glad, nMissionType.AttackWell, 80) == 31, 'others unchanged')
  -- the tick ends a held harass mission after the hold
  held[pred] = 77
  held.open, held.target, held.guid = 77, kObjIDNone, 'harass'
  W.matchTime = 200
  check(B.CompositionTick() == 0, 'first sight: the hold starts')
  W.matchTime = 203
  check(B.CompositionTick() == 0, 'within the hold')
  W.matchTime = 205
  check(B.CompositionTick() == 1 and ended[1] == 77, 'ended after the hold')
  -- outnumbering again resets the hold
  W.kill(W.order[#W.order])  -- e3
  held[pred] = 77
  W.matchTime = 300
  check(B.CompositionTick() == 0, 'outnumbering: kept')
  -- a mission of another kind is left alone
  held.guid = 'capture'
  W.matchTime = 400
  W.get(glad).alive = false
  check(B.CompositionTick() == 0 and #ended == 1, 'not a harass mission')
end)

test('composition: the Predator hits the workers of the well he harasses', function()
  local B = dgsim_bots
  local roles = compositionWorld()
  local pred = shaper('pred', 1, 100, 10, { bot = true })
  local grunt = shaper('plain', 1, 100, 11, { bot = true })
  roles[pred], roles[grunt] = 3, 1
  local well = W.add({ name = 'well', team = 2, npc = nNPCType.GoldMine, pos = P(100, 20) })
  local worker = W.add({ name = 'worker', team = 2, npc = nNPCType.Follower, pos = P(104, 14) })
  local minion = W.add({ name = 'minion', team = 2, npc = nNPCType.MeleeMinion, pos = P(101, 10), minion = true })
  W.wellOf = { [pred] = well, [grunt] = well }
  B.ApplyFixups()
  local ok, _, _, _, target = condition_nearest_minion[1](pred)
  check(ok == true and target == worker, 'the worker, not the minion')
  check(condition_Bot_IsNearMissionTarget[1](pred) == true, 'near enough to hit the workers')
  check(condition_nearest_minion[1](grunt) == false, 'another role: the original')
  W.wellOf = { [pred] = W.add({ name = 'ownwell', team = 1, npc = nNPCType.GoldMine, pos = P(0, 0) }) }
  check(condition_nearest_minion[1](pred) == false, 'his own team well: the original')
  W.wellOf = nil
end)

test('composition: the Hunter treats the parasite as a camp, only in the kill phase of his team', function()
  local B = dgsim_bots
  local roles = compositionWorld()
  local hunter = shaper('hunter', 1, -100, -50, { bot = true, maxhp = 700 })
  roles[hunter] = 2
  local para = W.add({ name = 'parasite', team = 0, npc = nNPCType.ObjectiveMajor, pos = P(-3, 3), maxhp = 5000 })
  W.objectiveFor = para
  nDirector.GetParasiteID = function() return para end
  local seenIdle
  condition_Bot_GameStart = { [nConditionFns.Check] = function(bot, idle) seenIdle = idle return true end }
  B.ApplyFixups()
  condition_Bot_GameStart[nConditionFns.Check](hunter, 65)
  check(seenIdle == 40, 'the Hunter starts after 40 s, not 65 (got ' .. tostring(seenIdle) .. ')')
  roles[hunter] = 0
  condition_Bot_GameStart[nConditionFns.Check](hunter, 65)
  check(seenIdle == 65, 'the others keep the plan start')
  roles[hunter] = 2
  condition_Bot_GameStart = nil
  local ok, x = condition_Bot_NextCreepCamp[1](hunter)
  check(ok == true and x == -59, 'nobody gathered: the next camp')
  check(condition_nearest_objective[1](hunter) == false, 'the parasite is not offered to the Hunter')
  check(condition_Bot_ShouldGank[1](hunter) == false, 'no gank for the Hunter')
  roles[hunter] = 1
  check(condition_Bot_ShouldGank[1](hunter) == true and condition_nearest_objective[1](hunter) == true, 'other roles: the original')
  roles[hunter] = 2
  shaper('a1', 1, -66, -10, { bot = true })
  shaper('a2', 1, -67, -9, { bot = true })
  shaper('a3', 1, -65, -11, { bot = true })
  check(select(2, condition_Bot_NextCreepCamp[1](hunter)) == -59, 'three at the rally point but no team call: the camps')
  B.teamWanted = true
  B.TeamStateOf(1).call, B.TeamStateOf(1).phase = 'parasite', 'gather'
  check(select(2, condition_Bot_NextCreepCamp[1](hunter)) == -59, 'the team gathers: he keeps farming')
  B.TeamStateOf(1).phase = 'kill'
  local ok2, px, py = condition_Bot_NextCreepCamp[1](hunter)
  check(ok2 == true and math.abs(math.sqrt((px + 3) ^ 2 + (py - 3) ^ 2) - 6) < 0.01 and px < -3,
        'the kill phase: the parasite (its edge on his side, PRETUSBOT) is his camp (' .. tostring(px) .. ', ' .. tostring(py) .. ')')
  check(condition_Bot_IsNearNextCreepCamp[1](hunter) == false, 'the near test uses the parasite too')
  check(select(1, condition_nearest_objective[1](hunter)) == true, 'on the parasite: ClearCamp may hit it')
  W.kill(para)
  W.matchTime = 130
  check(select(2, condition_Bot_NextCreepCamp[1](hunter)) == -59, 'the parasite died: back to the camps')
  B.teamWanted = nil
  roles[hunter] = 1
  check(select(2, condition_Bot_NextCreepCamp[1](hunter)) == -59, 'a Tactician never goes (the host asks BotRole)')
end)

test('composition: the Hunter fights the parasite from its edge (PRETUSBOT)', function()
  local B = dgsim_bots
  local roles = compositionWorld()
  -- the script's own near test: the camp position within its radius (9 with the camp-radius fix-up)
  condition_Bot_IsNearNextCreepCamp = { radius = 9, [1] = function(bot)
    local p = nGameObject.GetPosition(bot)
    return nVector.distance(nDirector.GetClosestCampPosition(1, p, false), p) < condition_Bot_IsNearNextCreepCamp.radius
  end }
  local hunter = shaper('hunter', 1, -64, -4, { bot = true, maxhp = 700 })
  roles[hunter] = 2
  local para = W.add({ name = 'parasite', team = 0, npc = nNPCType.ObjectiveMajor, pos = P(0, 0), maxhp = 5000 })
  nDirector.GetParasiteID = function() return para end
  B.ApplyFixups()
  B.teamWanted = true
  B.TeamStateOf(1).call, B.TeamStateOf(1).phase = 'parasite', 'kill'
  -- from the rally point: the walk ends 6 short of the centre on his side, not on the centre
  local ok, x, y = condition_Bot_NextCreepCamp[1](hunter)
  local d = math.sqrt(x * x + y * y)
  check(ok == true and math.abs(d - 6) < 0.01 and x < 0, string.format('the stand point (%.2f, %.2f), %.2f from the centre', x, y, d))
  check(condition_Bot_IsNearNextCreepCamp[1](hunter) == false, '64 away: not near, MoveToNextCamp walks')
  -- at the stand point, and after his W threw him 13 units out: still near, so ClearCamp keeps fighting
  W.get(hunter).pos = P(-5.9, -0.4)
  check(condition_Bot_IsNearNextCreepCamp[1](hunter) == true, 'at the stand point: near')
  W.get(hunter).pos = P(13, 0)
  check(condition_Bot_IsNearNextCreepCamp[1](hunter) == true, '13 out after a jump: still near (was a walk back)')
  W.get(hunter).pos = P(0, 0)
  check(condition_Bot_IsNearNextCreepCamp[1](hunter) == true, 'on the centre: near')
  W.get(hunter).pos = P(0, 22)
  check(condition_Bot_IsNearNextCreepCamp[1](hunter) == false, '22 out (knocked away): walks back')
  local _, sx, sy = condition_Bot_NextCreepCamp[1](hunter)
  check(math.abs(sx) < 0.02 and math.abs(sy - 6) < 0.01, string.format('back to his side of it (%.2f, %.2f)', sx, sy))
  -- the option off: the centre as before
  B.hunterParasiteStand = false
  W.get(hunter).pos = P(13, 0)
  local _, cx, cy = condition_Bot_NextCreepCamp[1](hunter)
  check(math.abs(cx) < 0.02 and math.abs(cy) < 0.02, 'off: the centre (the 0.01 of a zero coordinate)')
  check(condition_Bot_IsNearNextCreepCamp[1](hunter) == false, 'off: 13 out is not near (radius 9)')
  B.hunterParasiteStand = nil
  -- not the team's parasite time: the camps, the spot rule doesn't apply
  B.TeamStateOf(1).phase = 'gather'
  check(select(2, condition_Bot_NextCreepCamp[1](hunter)) == -59, 'the gather: the camps')
  B.teamWanted = nil
end)

test('composition: off unless the host wants it; lanes are kept', function()
  local B = dgsim_bots
  compositionWorld()
  B.compositionWanted = nil
  local s = B.ApplyFixups()
  check(B.fixups.composition == true and condition_Bot_ShouldGank[1]() == true, 'nothing wrapped: ' .. s)
  -- ability_Bot_GroupInLane's Tick no longer moves a bot between lanes with the setup on
  nBotUtils = { GetAlliesInLane = function() return {} end, GetEnemiesInLane = function(team, lane) return { 1, 2 } end }
  local moved = 0
  local assign = nBot.AssignLane
  nBot.AssignLane = function() moved = moved + 1 end
  local bot = shaper('g', 1, 60, -60, { bot = true })
  local p3 = { myObjectID = bot, myTeam = 1 }
  ability_Bot_GroupInLane = { searchRadius = 20, descriptors = 0 }
  B.groupInLaneTick(p3)
  check(moved == 1, 'without the setup the script balances lanes')
  B.compositionWanted = true
  B.groupInLaneTick(p3)
  check(moved == 1, 'with the setup no lane change')
  nBot.AssignLane = assign
end)

-- ---- BOTLANE: Gladiators stay, the siege lane is the opened one, a worn tower is finished -------------------------------
test('lane fix-up: Gladiators stay, the siege lane is the opened lane, the base radius', function()
  local B = dgsim_bots
  local roles, missions = compositionWorld()
  local glad = shaper('lg', 1, 60, -60, { bot = true })
  local hunter = shaper('lh', 1, 62, -62, { bot = true })
  local tact = shaper('lt', 1, 64, -64, { bot = true })
  roles[glad], roles[hunter], roles[tact] = 0, 2, 1
  for _, t in ipairs({ 2, 3, 4, 7, 11, 13, 6, 10, 8 }) do missions[t] = 40 + t end
  local stand = { [1] = 0, [2] = 3 }  -- the enemy's Top lane has no tower left, Bottom has all three
  local realTowers = nBattlegrounds.GetNumTowers
  nBattlegrounds.GetNumTowers = function(team, lane) return stand[lane] end
  condition_Bot_TowerPush = { searchRadius = 60, [1] = function() return false end }
  condition_Bot_AssignedToSiegeLane = { [1] = function() return nBattlegrounds.GetNumTowers(2, 1) > nBattlegrounds.GetNumTowers(2, 2) end }
  condition_Bot_GroupedWithAllies = { [1] = function() return nBattlegrounds.GetNumTowers(2, 1) > nBattlegrounds.GetNumTowers(2, 2) end }
  ability_Bot_AssignToSiegeLane = {}
  local picked
  ability_Bot_AssignToSiegeLane[nAbilityFns.Tick] = function()
    picked = nBattlegrounds.GetNumTowers(2, 1) > nBattlegrounds.GetNumTowers(2, 2) and 1 or 2
  end
  condition_Bot_InEnemyBase = { defaultSearchRadius = 65 }
  local wardAt = { 21, -38 }
  local para = W.add({ name = 'lparasite', team = 0, npc = nNPCType.ObjectiveMajor, pos = P(-3, 3), maxhp = 5000 })
  local victim = shaper('lv', 2, -10, 10, { maxhp = 700 })
  local far = shaper('lf', 2, 100, 100, { maxhp = 700 })
  local chased = victim
  nDirector.GetParasiteID = function() return para end
  condition_Bot_EngageForKill = { [1] = function() return true, 0, 0, 0, chased end }
  condition_Bot_BailOnKill = { [1] = function() return false end }
  condition_Bot_Ward = { [1] = function() return true, wardAt[1], wardAt[2], 0, kObjIDNone, true end }
  B.fixups.lane = false
  B.laneWanted = true
  B.ApplyLaneFixup()
  check(B.fixups.lane == true, 'applied')
  for _, t in ipairs({ 2, 3, 4, 7, 11, 13, 6, 10, 8 }) do
    check(nBot.GetAvailableMission(glad, t, 80) == 40 + t, 'without the team layer every role keeps type ' .. t)
  end
  check(nBattlegrounds.GetNumTowers(2, 1) == 0, 'outside the siege scripts the count is the real one')
  check(condition_Bot_AssignedToSiegeLane[1]() == true, 'AssignedToSiegeLane: the lane with fewer towers (Top, 0) wins')
  check(condition_Bot_GroupedWithAllies[1]() == true, 'GroupedWithAllies: same lane')
  ability_Bot_AssignToSiegeLane[nAbilityFns.Tick]()
  check(picked == 1, 'AssignToSiegeLane picks the opened lane')
  check(condition_Bot_InEnemyBase.defaultSearchRadius == 85, 'the base radius')
  check(condition_Bot_Ward[1](tact) == false, 'a Tactician is not sent to a ward spot in the middle')
  check(condition_Bot_Ward[1](hunter) == true, 'the Hunter may')
  wardAt = { -4, 85 }
  check(condition_Bot_Ward[1](tact) == true, 'a ward spot at the lane is kept')
  check(condition_Bot_Ward[1](glad) == false, 'a Gladiator places no ward: he stays in his lane')
  condition_Bot_Ward = nil
  check(condition_Bot_EngageForKill[1](glad) == false, 'a kill next to the parasite is not chased')
  check(condition_Bot_EngageForKill[1](hunter) == false, 'not by the Hunter either')
  B.teamWanted = true
  B.TeamStateOf(1).call, B.TeamStateOf(1).phase = 'parasite', 'kill'
  check(condition_Bot_EngageForKill[1](glad) == true, 'the team takes the parasite: the kill there is fine')
  B.TeamStateOf(1).call, B.TeamStateOf(1).phase = 'farm', nil
  B.teamWanted = nil
  chased = far
  check(condition_Bot_EngageForKill[1](glad) == true, 'a kill elsewhere is')
  W.get(glad).target = victim
  check(condition_Bot_BailOnKill[1](glad) == true, 'an engaged bot whose target is at the parasite bails')
  W.get(glad).target = far
  check(condition_Bot_BailOnKill[1](glad) == false, 'otherwise the original')
  condition_Bot_EngageForKill, condition_Bot_BailOnKill = nil, nil
  nBattlegrounds.GetNumTowers = realTowers
  condition_Bot_TowerPush, condition_Bot_AssignedToSiegeLane, condition_Bot_GroupedWithAllies = nil, nil, nil
  ability_Bot_AssignToSiegeLane, condition_Bot_InEnemyBase = nil, nil
end)

-- ---- BOTPARA: the rally points are moved out of the parasite's reach ---------------------------------------------------
test('parasite fix-up: the rally points leave the parasite reach', function()
  local B = dgsim_bots
  local function stub(extra)
    local c = { mortalRally = nVector.create(-36, -6, 0), spiritRally = nVector.create(36, 6, 0) }
    c[nConditionFns.CheckEnd] = function() return false end
    c[nConditionFns.Check] = function() end
    c[1] = function() return false end
    return c
  end
  condition_Mission_RallyForParasite, condition_Bot_GatherForParasite = stub(), stub()
  condition_Mission_KillParasite, condition_Mission_RallyForParasiteContest = stub(), stub()
  condition_Bot_GatherForParasiteContest = stub()
  B.fixups.parasite = false
  B.ApplyParasiteFixup()
  for _, c in ipairs({ condition_Mission_RallyForParasite, condition_Bot_GatherForParasite }) do
    for _, v in ipairs({ c.mortalRally, c.spiritRally }) do
      check(math.sqrt((v.x + 3) ^ 2 + (v.y - 3) ^ 2) >= 55, 'a rally point is 55+ from the parasite')
    end
  end
  check(condition_Mission_RallyForParasite.mortalRally.x == condition_Bot_GatherForParasite.mortalRally.x, 'the mission and the gather agree')
  condition_Mission_RallyForParasite, condition_Bot_GatherForParasite, condition_Mission_KillParasite = nil, nil, nil
  condition_Mission_RallyForParasiteContest, condition_Bot_GatherForParasiteContest = nil, nil
end)

-- ---- BOTTEAM: the team's call ------------------------------------------------------------------------------------------
test('team: the call, pure', function()
  local B = dgsim_bots
  local cfg = B.teamConfig
  local tower = W.add({ name = 'Binding_Spirit_Outer', team = 2, pos = P(116, -29), npc = nNPCType.Tower, structure = true })
  local function snap(o)
    local s = { t = 300, alliesAlive = 5, alliesHealthy = 5, enemiesAlive = 5, enemiesUp = 5, enemiesDown = 0, hpAvg = 0.9,
                levelDiff = 0, buff = 0, enemyLaneOpen = false, baseThreat = false, ftue = false, bots = {}, wells = {},
                pushLane = nLaneID.Bottom, pushTarget = tower, pushDistance = 100,
                parasite = { stands = true, inCombat = false, alliesNear = 0, enemiesNear = 0, humanOnIt = false,
                             seenRecently = false, gathered = 0, botDistance = 150, force = 5 } }
    for k, v in pairs(o or {}) do
      if k == 'parasite' then
        for pk, pv in pairs(v) do s.parasite[pk] = pv end
      else
        s[k] = v
      end
    end
    return s
  end
  local function st(o)
    local x = { call = 'farm', since = 0, phaseSince = 0, cool = {}, assigned = {}, homeLane = {} }
    for k, v in pairs(o or {}) do x[k] = v end
    return x
  end
  check(B.TeamDecide(snap(), st(), cfg).call == 'farm', 'even, early: farm')
  -- an opening: two of theirs on a long respawn
  local d = B.TeamDecide(snap({ enemiesAlive = 3, enemiesUp = 3, enemiesDown = 2 }), st(), cfg)
  check(d.call == 'push' and d.lane == nLaneID.Bottom and d.target == tower, 'two down: push the weaker lane (' .. d.call .. ')')
  -- their dead come back in a few seconds: no opening
  check(B.TeamDecide(snap({ enemiesAlive = 3, enemiesUp = 5 }), st(), cfg).call == 'farm', 'respawning soon: no opening')
  -- the parasite nearer than the tower: take it instead
  d = B.TeamDecide(snap({ enemiesAlive = 3, enemiesUp = 3, enemiesDown = 2, parasite = { botDistance = 60 } }), st(), cfg)
  check(d.call == 'parasite' and d.phase == 'gather', 'an opening near the parasite: gather for it (' .. d.call .. ')')
  -- the calm take: from 480 s, healthy, even, none of theirs seen near it
  check(B.TeamDecide(snap({ t = 470 }), st(), cfg).call == 'farm', 'calm, but too early')
  d = B.TeamDecide(snap({ t = 500 }), st(), cfg)
  check(d.call == 'parasite' and d.phase == 'gather', 'calm after 480 s: gather')
  check(B.TeamDecide(snap({ t = 500, parasite = { seenRecently = true } }), st(), cfg).call == 'farm', 'one of theirs seen near it: no')
  check(B.TeamDecide(snap({ t = 500, alliesHealthy = 3 }), st(), cfg).call == 'farm', 'only 3 healthy: no')
  check(B.TeamDecide(snap({ t = 500, parasite = { stands = false } }), st(), cfg).call == 'farm', 'no parasite (egg, dead): no')
  check(B.TeamDecide(snap({ t = 500 }), st({ cool = { parasite = 520 } }), cfg).call == 'farm', 'cooling down: no')
  -- a human ally on it: everyone comes
  d = B.TeamDecide(snap({ parasite = { humanOnIt = true, inCombat = true } }), st(), cfg)
  check(d.call == 'parasite' and d.phase == 'kill', 'a human on the parasite: straight to the kill')
  -- the gather
  local gathering = st({ call = 'parasite', phase = 'gather', since = 500, phaseSince = 500 })
  check(B.TeamDecide(snap({ t = 510, parasite = { gathered = 2 } }), gathering, cfg).phase == 'gather', '2 there: wait')
  d = B.TeamDecide(snap({ t = 512, parasite = { gathered = 3 } }), gathering, cfg)
  check(d.call == 'parasite' and d.phase == 'kill', '3 there: the kill')
  check(B.TeamDecide(snap({ t = 541, parasite = { gathered = 2 } }), gathering, cfg).phase == 'gather', '41 s: still waiting')
  d = B.TeamDecide(snap({ t = 561, parasite = { gathered = 2 } }), gathering, cfg)
  check(d.call == 'farm' and d.abort, 'the gather took 61 s: retreat')
  d = B.TeamDecide(snap({ t = 512, alliesAlive = 2, alliesHealthy = 2, parasite = { gathered = 2 } }), gathering, cfg)
  check(d.phase == 'kill', 'two of us alive and both there: the kill')
  -- the kill phase
  local killing = st({ call = 'parasite', phase = 'kill', since = 500, phaseSince = 512 })
  check(B.TeamDecide(snap({ t = 520, parasite = { alliesNear = 4, enemiesNear = 2 } }), killing, cfg).call == 'parasite',
        '2 of theirs against 4 of us: go on')
  d = B.TeamDecide(snap({ t = 520, parasite = { alliesNear = 2, enemiesNear = 3 } }), killing, cfg)
  check(d.call == 'farm' and d.abort, '3 of theirs against 2 of us: retreat')
  check(B.TeamDecide(snap({ t = 520, parasite = { stands = false } }), killing, cfg).call == 'farm', 'it died: farm')
  -- a contest: they are on it and we bring more
  d = B.TeamDecide(snap({ parasite = { inCombat = true, enemiesNear = 2 } }), st(), cfg)
  check(d.call == 'contest', 'two of theirs on it, five of us: contest')
  check(B.TeamDecide(snap({ parasite = { inCombat = true, enemiesNear = 3, force = 3 } }), st(), cfg).call ~= 'contest',
        'three of theirs, a force of three: no contest')
  check(B.TeamDecide(snap({ parasite = { inCombat = true, enemiesNear = 1, enemiesKnown = 4, force = 4 } }), st(), cfg).call ~= 'contest',
        'one seen now, four seen there a moment ago, a force of four: no contest')
  check(B.TeamDecide(snap({ t = 500, botsFighting = 1 }), st(), cfg).call == 'farm', 'a bot in a lane fight: no calm call')
  local contesting = st({ call = 'contest', since = 300 })
  d = B.TeamDecide(snap({ t = 310, parasite = { inCombat = true, enemiesNear = 3, force = 2 } }), contesting, cfg)
  check(d.call == 'farm' and d.abort, 'they are more now: retreat')
  d = B.TeamDecide(snap({ t = 310, parasite = { inCombat = true, enemiesNear = 4, alliesNear = 2 } }), contesting, cfg)
  check(d.call == 'farm' and d.abort, 'two of us there against four: retreat')
  d = B.TeamDecide(snap({ t = 310, parasite = { inCombat = false, enemiesNear = 0 } }), contesting, cfg)
  check(d.call == 'parasite' and d.phase == 'kill', 'they left: we take it')
  -- the push's end rules
  local pushing = st({ call = 'push', since = 300, lastAdvantage = 300, lastProgress = 300, lane = nLaneID.Bottom })
  check(B.TeamDecide(snap({ t = 305 }), pushing, cfg).call == 'push', 'the advantage gone 5 s: go on')
  d = B.TeamDecide(snap({ t = 307 }), pushing, cfg)
  check(d.call == 'farm' and d.abort, 'gone 7 s: back to the lanes')
  pushing.lastAdvantage = 370
  d = B.TeamDecide(snap({ t = 376 }), pushing, cfg)
  check(d.call == 'farm' and d.abort, 'no structure fell for 76 s: stalled')
  pushing.lastProgress = 440
  pushing.lastAdvantage = 450
  check(B.TeamDecide(snap({ t = 451 }), pushing, cfg).call == 'push', 'after 150 s: on (BOTTEMPO: 240 s)')
  pushing.lastProgress, pushing.lastAdvantage = 535, 540
  check(B.TeamDecide(snap({ t = 541 }), pushing, cfg).call == 'farm', 'after 240 s: done')
  check(B.TeamDecide(snap({ enemiesAlive = 3, enemiesUp = 3, enemiesDown = 2 }), st({ cool = { push = 320 } }), cfg).call == 'farm',
        'a push cooling down')
  -- a lane of theirs open, even numbers, healthy: siege
  d = B.TeamDecide(snap({ t = 1300, enemyLaneOpen = true, parasite = { seenRecently = true } }), st(), cfg)
  check(d.call == 'push', 'a lane open: siege (' .. d.call .. ')')
  check(B.TeamDecide(snap({ t = 1100, enemyLaneOpen = true, levelDiff = -1, parasite = { seenRecently = true } }), st(), cfg).call ~= 'push',
        'a lane open but behind in levels: no')
  -- BOTTEMPO: the late game (from 1200 s): even numbers, four of us, healthy, at most a level behind: group and siege
  local off = {}
  for k, v in pairs(cfg) do off[k] = v end
  off.tempo = false
  d = B.TeamDecide(snap({ t = 1300, enemyLaneOpen = true, levelDiff = -1, parasite = { seenRecently = true } }), st(), cfg)
  check(d.call == 'push', 'the late game: a level behind is fine (' .. d.call .. ')')
  check(B.TeamDecide(snap({ t = 1300, enemyLaneOpen = true, levelDiff = -1, parasite = { seenRecently = true } }), st(), off).call ~= 'push',
        'the late game, BOTTEAM rules: no')
  check(B.TeamDecide(snap({ t = 1300, levelDiff = -2, parasite = { seenRecently = true } }), st(), cfg).call ~= 'push',
        'the late game, two levels behind: no')
  check(B.TeamDecide(snap({ t = 1190, parasite = { seenRecently = true } }), st(), cfg).call == 'farm', 'before 1200 s: farm')
  d = B.TeamDecide(snap({ t = 1250, parasite = { seenRecently = true } }), st(), cfg)
  check(d.call == 'push' and string.find(d.reason, 'late game') ~= nil, 'from 1200 s, even: the siege (' .. tostring(d.reason) .. ')')
  check(B.TeamDecide(snap({ t = 1250, hpAvg = 0.5, parasite = { seenRecently = true } }), st(), cfg).call == 'farm', 'hurt: no siege')
  check(B.TeamDecide(snap({ t = 1250, alliesAlive = 3, alliesHealthy = 3, enemiesUp = 3, enemiesAlive = 3,
                            parasite = { seenRecently = true } }), st(), cfg).call == 'farm', 'three of us: no siege')
  check(B.TeamDecide(snap({ t = 1250, enemiesUp = 6 }), st(), cfg).call ~= 'push', 'outnumbered: no siege')
  check(B.TeamDecide(snap({ t = 1250, parasite = { seenRecently = true } }), st({ cool = { push = 1260 } }), cfg).call == 'farm',
        'the siege cooling down')
  -- BOTTEMPO: pushMax counts from the last structure that fell
  local going = st({ call = 'push', since = 300, lastAdvantage = 450, lastProgress = 445, lastKill = 400, lane = nLaneID.Bottom })
  check(B.TeamDecide(snap({ t = 451 }), going, off).call == 'farm', 'BOTTEAM rules: 150 s from the start')
  going.lastAdvantage, going.lastProgress = 600, 600
  check(B.TeamDecide(snap({ t = 601 }), going, cfg).call == 'push', 'a Binding fell at 400: 301 s in, go on')
  going.lastAdvantage, going.lastProgress = 641, 641
  check(B.TeamDecide(snap({ t = 641 }), going, cfg).call == 'farm', '241 s after the last structure: done')
  -- the base first
  check(B.TeamDecide(snap({ baseThreat = true, baseAttackers = 2, homeDefenders = 0 }), pushing, cfg).call == 'defend',
        'the base is attacked: defend')
  check(B.TeamDecide(snap({ t = 600 }), st({ call = 'defend', lastThreat = 595 }), cfg).call == 'defend', 'defend holds 8 s')
  -- BOTTEMPO: a siege leaves for a base attack only when the shapers at our base outnumber our bots there
  local sieging = st({ call = 'push', since = 1300, lastAdvantage = 1310, lastProgress = 1310, lane = nLaneID.Bottom })
  check(B.TeamDecide(snap({ t = 1311, baseThreat = true, baseAttackers = 0, homeDefenders = 0 }), sieging, cfg).call == 'push',
        'minions at our base: the siege goes on')
  check(B.TeamDecide(snap({ t = 1311, baseThreat = true, baseAttackers = 1, homeDefenders = 1 }), sieging, cfg).call == 'push',
        'one of theirs, one of ours at home: the siege goes on')
  check(B.TeamDecide(snap({ t = 1311, baseThreat = true, baseAttackers = 2, homeDefenders = 1 }), sieging, cfg).call == 'defend',
        'two of theirs against one at home: defend')
  check(B.TeamDecide(snap({ t = 1311, baseThreat = true, baseAttackers = 0, homeDefenders = 0 }), sieging, off).call == 'defend',
        'BOTTEAM rules: any base attack ends the push')
  check(B.TeamDecide(snap({ t = 1311, baseThreat = true, baseAttackers = 0 }), st(), cfg).call == 'defend',
        'farming: a base attack is defended as before')
  -- their cores down: an opening of its own (back before they regrow)
  d = B.TeamDecide(snap({ t = 700, enemyCoresDown = 2, parasite = { seenRecently = true } }), st(), cfg)
  check(d.call == 'push' and string.find(d.reason, 'cores are down') ~= nil, 'their cores down: finish it (' .. tostring(d.reason) .. ')')
  check(B.TeamDecide(snap({ t = 700, enemyCoresDown = 2, parasite = { seenRecently = true } }), st(), off).call ~= 'push',
        'BOTTEAM rules: no')
  -- an AFK human far behind: no parasite calls (the data's CheeseDetected); 3+ humans: no gather, no push
  check(B.TeamDecide(snap({ t = 500, cheese = true }), st(), cfg).call == 'farm', 'cheese: no calm parasite')
  check(B.TeamDecide(snap({ cheese = true, parasite = { inCombat = true, enemiesNear = 1 } }), st(), cfg).call ~= 'contest',
        'cheese: no contest')
  check(B.TeamDecide(snap({ cheese = true, enemiesAlive = 3, enemiesUp = 3, enemiesDown = 2, parasite = { botDistance = 60 } }),
                     st(), cfg).call == 'push', 'cheese: a push instead')
  check(B.TeamDecide(snap({ manyHumans = true, enemiesAlive = 3, enemiesUp = 3, enemiesDown = 2 }), st(), cfg).call == 'farm',
        'three humans: they lead')
  -- FTUE: no parasite, no push
  check(B.TeamDecide(snap({ ftue = true, t = 600, enemiesAlive = 3, enemiesUp = 3, enemiesDown = 2 }), st(), cfg).call == 'farm',
        'against a first-time player: farm')
  -- a well: the Predator and the nearest Tactician, never the Hunter, no Gladiator early
  local glad = W.add({ name = 'g', team = 1, player = true, bot = true, pos = P(20, -170) })
  local tact = W.add({ name = 't', team = 1, player = true, bot = true, pos = P(60, -150) })
  local pred = W.add({ name = 'p', team = 1, player = true, bot = true, pos = P(-100, 0) })
  local hunt = W.add({ name = 'h', team = 1, player = true, bot = true, pos = P(25, -175) })
  local well = W.add({ name = 'w', team = 2, pos = P(28, -182), npc = nNPCType.GoldMine })
  local bots = { { id = glad, role = 0, alive = true, hp = 1, pos = P(20, -170) },
                 { id = tact, role = 1, alive = true, hp = 1, pos = P(60, -150) },
                 { id = pred, role = 3, alive = true, hp = 1, pos = P(-100, 0) },
                 { id = hunt, role = 2, alive = true, hp = 1, pos = P(25, -175) } }
  d = B.TeamDecide(snap({ t = 1000, bots = bots, wells = { { id = well, pos = P(28, -182), guards = 0 } },
                          parasite = { stands = false } }), st(), cfg)
  check(d.call == 'well' and d.well == well and d.assigned[pred] and d.assigned[tact] and not d.assigned[glad] and
        not d.assigned[hunt], 'a well: the Predator and the Tactician')
  local noPred = { bots[1], bots[2], bots[4] }
  d = B.TeamDecide(snap({ t = 1000, bots = noPred, wells = { { id = well, pos = P(28, -182), guards = 0 } },
                          parasite = { stands = false } }), st(), cfg)
  check(d.call == 'well' and d.assigned[tact] and d.assigned[glad] and not d.assigned[hunt],
        'no Predator: the Tactician and a Gladiator make the pair')
  d = B.TeamDecide(snap({ t = 1000, bots = { bots[2], bots[4] }, wells = { { id = well, pos = P(28, -182), guards = 0 } },
                          parasite = { stands = false } }), st(), cfg)
  check(d.call == 'farm', 'one bot alone does not go')
  check(B.TeamDecide(snap({ t = 1000, bots = bots, wells = { { id = well, pos = P(28, -182), guards = 1 } },
                            parasite = { stands = false } }), st(), cfg).call == 'farm', 'a guarded well: no')
  local taking = st({ call = 'well', since = 1000, well = well, assigned = { [pred] = true, [tact] = true } })
  check(B.TeamDecide(snap({ t = 1010, bots = bots, wells = {}, parasite = { stands = false } }), taking, cfg).call == 'farm',
        'the well is ours: done')
  d = B.TeamDecide(snap({ t = 1010, bots = bots, wells = { { id = well, pos = P(28, -182), guards = 3 } },
                          parasite = { stands = false } }), taking, cfg)
  check(d.call == 'farm' and d.abort, 'three guards against two: retreat')
end)

test('team: the calls carried out through the missions, lanes and pings', function()
  local B = dgsim_bots
  local roles, missions = compositionWorld()
  local board = { open = {}, nextId = 100, ended = {}, pings = {}, logs = {}, requests = {} }
  nBotHost.RequestMission = function(team, mtype, target, agents, timeout, guid, hasDest, dx, dy, dz)
    for _, m in ipairs(board.open) do
      if m.team == team and m.type == mtype and m.target == target then m.agents = agents return m.id end
    end
    board.nextId = board.nextId + 1
    board.open[#board.open + 1] = { id = board.nextId, team = team, type = mtype, target = target, guid = guid,
                                    agents = agents, dest = hasDest and { dx, dy } or nil }
    return board.nextId
  end
  nBotHost.OpenMission = function(team, i)
    local k = 0
    for _, m in ipairs(board.open) do
      if m.team == team then
        k = k + 1
        if k == i then return m.id, m.target, m.guid end
      end
    end
  end
  nBotHost.EndMission = function(id)
    for i, m in ipairs(board.open) do
      if m.id == id then table.remove(board.open, i) board.ended[#board.ended + 1] = m return true end
    end
    return false
  end
  nBotHost.IsMissionAvailable = function(team, mtype)
    for _, m in ipairs(board.open) do if m.team == team and m.type == mtype then return true end end
    return false
  end
  nBotHost.GetAvailableMission = function(bot, mtype)
    for _, m in ipairs(board.open) do if m.type == mtype then return m.id end end
    return 0
  end
  nBotHost.TeamPing = function(bot, target, x, y, z, ptype, why) board.pings[#board.pings + 1] = { ptype, why } return true end
  nBotHost.Log = function(text) board.logs[#board.logs + 1] = text end
  local respawn = {}
  nBotHost.RespawnIn = function(i) return respawn[i] or 0 end
  local indexOf = {}
  nPlayer.GetPlayerIdForObject = function(c) return indexOf[c] end
  local lanes = {}
  nBotHost.AssignLane = function(bot, lane) lanes[bot] = lane end
  nBotHost.GetLane = function(bot) return lanes[bot] or 0 end
  -- the scripts' tables
  local function cond(extra)
    local c = extra or {}
    c[nConditionFns.Check] = c[nConditionFns.Check] or function() return true end
    c[nConditionFns.CheckEnd] = function() return false end
    c[1] = c[1] or function() return true end
    return c
  end
  condition_Mission_RallyForParasite = cond({ conditionGUID = 'rally', mortalRally = nVector.create(-66, -10, 0),
                                              spiritRally = nVector.create(66, 10, 0) })
  condition_Mission_RallyForParasiteContest = cond({ conditionGUID = 'contest', mortalRally = nVector.create(-66, -8, 0),
                                                     spiritRally = nVector.create(66, 8, 0) })
  condition_Mission_KillParasite = cond({ conditionGUID = 'kill' })
  condition_Mission_KillParasite[nConditionFns.Check] = function(team)
    nBot.RequestMission(team, nMissionType.KillParasite, nDirector.GetParasiteID(), 5, 0, 'kill')
    return true, kObjIDNone, 0
  end
  condition_Mission_CaptureWell = cond({ conditionGUID = 'capture' })
  condition_Bot_GatherForParasite = cond({ mortalRally = nVector.create(-66, -10, 0), spiritRally = nVector.create(66, 10, 0) })
  condition_Bot_GatherForParasite[1] = function() return true, -66, -10, 0, kObjIDNone, true end
  condition_Bot_GatherForParasiteContest = cond()
  condition_Bot_TimeToRally = cond()
  condition_Bot_TimeToRally[1] = function() return false end
  condition_Bot_GroupedWithAllies = cond()
  condition_Bot_GroupedWithAllies[1] = function() return false end
  condition_Bot_AssignedToSiegeLane = cond()
  ability_Bot_AssignToSiegeLane = {}
  ability_Bot_AssignToSiegeLane[nAbilityFns.Tick] = function() end
  condition_Bot_TowerPush = cond()
  condition_Bot_InEnemyBase = { defaultSearchRadius = 65 }
  local ids = v5Structures()
  local para = W.add({ name = 'parasite', team = 0, npc = nNPCType.ObjectiveMajor, pos = P(-3, 3), maxhp = 5000 })
  nDirector.GetParasiteID = function() return para end
  -- team 1: two Gladiators, a Tactician, a Predator, a Hunter; team 2: five shapers
  local g1 = shaper('g1', 1, -70, 70, { bot = true })
  local g2 = shaper('g2', 1, 70, -70, { bot = true })
  local tc = shaper('tc', 1, -80, 60, { bot = true })
  local pr = shaper('pr', 1, 80, -60, { bot = true })
  local hu = shaper('hu', 1, -150, -100, { bot = true })
  W.get(para).untargetable = true  -- the egg: no parasite call yet
  roles[g1], roles[g2], roles[tc], roles[pr], roles[hu] = 0, 0, 1, 3, 2
  lanes[g1], lanes[g2], lanes[tc], lanes[pr] = nLaneID.Top, nLaneID.Bottom, nLaneID.Top, nLaneID.Bottom
  local enemies = {}
  for i = 1, 5 do
    enemies[i] = shaper('e' .. i, 2, 150 + i, 150, { bot = true })
    indexOf[enemies[i]] = 4 + i
  end
  B.teamWanted, B.laneWanted = true, true
  B.fixups = { veryOutnumbered = true, groupInLane = true, bailOnKill = true, parasite = true, campRadius = true }
  B.ApplyFixups()
  check(B.fixups.team == true, 'applied')
  check(condition_Mission_RallyForParasite[nConditionFns.Check](1) == false, 'the original Rally opens nothing')
  check(condition_Mission_KillParasite[nConditionFns.Check](1) == false and #board.open == 0, 'KillParasite: not while farming')
  check(nBot.GetAvailableMission(g1, nMissionType.DefendBase, 0) == 0, 'nothing open: 0')
  -- farming: the offers
  nBot.RequestMission(1, nMissionType.DefendTower, ids[4], 1, 5, 'dt')
  check(nBot.GetAvailableMission(tc, nMissionType.DefendTower, 0) > 0, 'the Tactician answers a tower defence')
  check(nBot.GetAvailableMission(g1, nMissionType.DefendTower, 0) == 0, 'a Gladiator stays in lane')
  board.open = {}
  -- two enemies dead for a while, the parasite far: a push in the bottom lane (fewer towers there? equal: most bots)
  W.kill(enemies[1])
  W.kill(enemies[2])
  respawn[5], respawn[6] = 30, 25
  W.matchTime = 400
  B.TeamTick()
  local st = B.TeamStateOf(1)
  check(st.call == 'push', 'two of theirs down: push (' .. tostring(st.call) .. ')')
  check(lanes[g1] == st.lane and lanes[tc] == st.lane and lanes[hu] == nil, 'everyone but the Hunter in the push lane')
  check(condition_Bot_TimeToRally[1](g1) == true and condition_Bot_GroupedWithAllies[1](g1) == false, 'the siege rally runs')
  B.inSiege = true
  check(nBattlegrounds.GetNumTowers(2, st.lane) == 100 and nBattlegrounds.GetNumTowers(2, 3 - st.lane) == 0,
        'the siege scripts take the push lane')
  B.inSiege = false
  check(board.pings[#board.pings][1] == 0, 'a ping on the target')
  check(B.TeamStateOf(2).call == 'farm', 'the other team farms')
  -- they come back: even numbers, the push goes on (BOTTEMPO); the walk there is no stall
  W.get(enemies[1]).alive, W.get(enemies[1]).hp = true, 100
  W.get(enemies[2]).alive, W.get(enemies[2]).hp = true, 100
  W.matchTime = 403
  B.TeamTick()
  check(st.call == 'push', 'within the hold')
  W.matchTime = 410
  B.TeamTick()
  check(st.call == 'push', 'even numbers: the push goes on (' .. tostring(st.call) .. ')')
  W.matchTime = 480
  B.TeamTick()
  check(st.call == 'push' and st.pushersNear < 2, 'still on the way after 80 s: no stall (' .. tostring(st.call) .. ')')
  -- at the target: the stall clock runs; damage on it is progress
  local target = B.PushTargetOf(B.TeamSnapshot(1), st.lane)
  local tp = W.get(target).pos
  for _, b in ipairs({ g1, g2, tc }) do W.get(b).pos = P(tp.x + 20, tp.y) end
  W.matchTime = 485
  B.TeamTick()
  check(st.pushersNear == 3, 'three at the target (' .. tostring(st.pushersNear) .. ')')
  W.get(target).hp = W.get(target).maxhp * 0.9
  W.matchTime = 550
  B.TeamTick()
  check(st.call == 'push' and st.lastProgress == 550, 'the tower at 90 %: progress')
  W.matchTime = 620
  B.TeamTick()
  check(st.call == 'push', 'no damage for 70 s: not yet stalled')
  -- two of ours die: outnumbered, the push ends after the hold, the lanes go back
  W.kill(g2)
  W.kill(pr)
  W.matchTime = 622
  B.TeamTick()
  W.matchTime = 629
  B.TeamTick()
  check(st.call == 'farm' and lanes[g1] == nLaneID.Top and lanes[tc] == nLaneID.Top and lanes[g2] == nLaneID.Bottom,
        'outnumbered: back to their own lanes (' .. tostring(st.call) .. ')')
  check(board.pings[#board.pings][1] == 1, 'a retreat ping')
  check(condition_Bot_TimeToRally[1](g1) == false and condition_Bot_GroupedWithAllies[1](g1) == true, 'no siege rally')
  local ended
  for _, l in ipairs(board.logs) do if string.find(l, 'the push took 0 structure') then ended = l end end
  check(ended ~= nil, 'the push end line says what it took')
  W.get(target).hp = W.get(target).maxhp
  for _, b in ipairs({ g2, pr }) do W.get(b).alive, W.get(b).hp = true, W.get(b).maxhp end
  W.get(g1).pos, W.get(g2).pos, W.get(tc).pos = P(-70, 70), P(70, -70), P(-80, 60)
  W.get(para).untargetable = false
  -- the calm parasite at 700 s: Rally for all, KillParasite only once three are at the rally point
  W.matchTime = 700
  B.TeamTick()
  check(st.call == 'parasite' and st.phase == 'gather', 'calm at 700 s: gather (' .. tostring(st.call) .. ')')
  local rally
  for _, m in ipairs(board.open) do if m.team == 1 and m.type == nMissionType.Rally then rally = m end end
  check(rally ~= nil and rally.target == para and rally.dest and rally.dest[1] == -66 and rally.agents == 5,
        'a Rally mission at the rally point for the five bots')
  check(nBot.GetAvailableMission(g1, nMissionType.Rally, 0) == rally.id, 'a Gladiator comes too')
  check(nBot.GetAvailableMission(g1, nMissionType.KillParasite, 0) == 0, 'no KillParasite while gathering')
  check(select(1, condition_Bot_GatherForParasite[1](g1)) == false, 'the gather condition waits for the team')
  check(B.HunterParasitePos(hu) == nil, 'the Hunter farms while they gather')
  for _, b in ipairs({ g1, g2, tc }) do W.get(b).pos = P(-66, -10) end
  W.matchTime = 710
  B.TeamTick()
  check(st.phase == 'kill', 'three gathered: the kill')
  check(nBot.GetAvailableMission(tc, nMissionType.KillParasite, 0) > 0, 'KillParasite open to them')
  check(select(1, condition_Bot_GatherForParasite[1](g1)) == true, 'the gather condition passes')
  local hp = B.HunterParasitePos(hu)
  check(hp ~= nil and hp.x == -3, 'the Hunter joins')
  -- it dies: farm, the missions end
  W.kill(para)
  W.matchTime = 740
  B.TeamTick()
  check(st.call == 'farm', 'the parasite died: farm')
  local left = 0
  for _, m in ipairs(board.open) do if m.team == 1 and (m.type == nMissionType.Rally or m.type == nMissionType.KillParasite) then left = left + 1 end end
  check(left == 0, 'the parasite missions ended')
  -- the base: defend
  nBot.RequestMission(1, nMissionType.DefendBase, W.guardians[1], 5, 5, 'dt')
  W.matchTime = 745
  B.TeamTick()
  check(st.call == 'defend', 'the base is attacked: defend')
  check(nBot.GetAvailableMission(g2, nMissionType.DefendBase, 0) > 0, 'everyone may defend')
  board.open = {}
  W.matchTime = 760
  B.TeamTick()
  check(st.call == 'farm', 'the threat is over')
  -- a well: the Predator and the Tactician take the capture mission, anyone else gets none
  local well = W.add({ name = 'gold', team = 2, pos = P(28, -182), npc = nNPCType.GoldMine })
  nBotHost.MinesForTeam = function(team, onlyUnlocked) if team == 2 then return { well } end return {} end
  nBot.RequestMission(1, nMissionType.AttackWell, well, 2, 0, 'capture')
  W.matchTime = 1000
  B.TeamTick()
  check(st.call == 'well' and st.assigned[pr] and st.assigned[tc], 'a well for the Predator and the Tactician (' .. tostring(st.call) .. ')')
  local capture = 0
  for _, m in ipairs(board.open) do if m.type == nMissionType.AttackWell then capture = m.id end end
  check(nBot.GetAvailableMission(tc, nMissionType.AttackWell, 80) == capture, 'the crew gets the target\'s mission at any distance')
  check(nBot.GetAvailableMission(g1, nMissionType.AttackWell, 80) == 0, 'a Gladiator does not')
  W.get(well).team = 1
  nBotHost.MinesForTeam = function() return {} end
  W.matchTime = 1010
  B.TeamTick()
  check(st.call == 'farm', 'the well is ours')
  check(#board.logs >= 6 and string.find(board.logs[1], 'team 1: farm %-> push') ~= nil, 'the calls are logged: ' .. tostring(board.logs[1]))
  -- BOTTEMPO: a parasite call given up waits parasiteRetryCooldown (150 s) before the next one
  W.get(para).alive, W.get(para).hp = true, 5000
  W.get(g1).pos, W.get(g2).pos, W.get(tc).pos = P(-70, 70), P(70, -70), P(-80, 60)
  local gathering = B.TeamStateOf(1)
  gathering.call, gathering.phase, gathering.since, gathering.phaseSince = 'parasite', 'gather', 1020, 1020
  W.matchTime = 1090
  B.TeamTick()
  check(st.call == 'farm' and st.cool.parasite == 1090 + B.teamConfig.parasiteRetryCooldown,
        'a gather given up: the parasite waits 150 s (' .. tostring(st.cool.parasite) .. ')')
  local status
  for _, l in ipairs(board.logs) do if string.find(l, '^team 1 status at') then status = l end end
  check(status ~= nil and string.find(status, 'enemy top 100/100/100, bottom 100/100/100, cores 2/2, Guardian 100', 1, true) ~= nil,
        'a status line with the enemy structures: ' .. tostring(status))
  W.kill(ids[7])
  W.get(ids[8]).hp = W.get(ids[8]).maxhp * 0.25
  B.Scan(true)
  check(B.StructureStatus(2) == 'top -/25/100, bottom 100/100/100, cores 2/2, Guardian 100',
        'the structures: ' .. B.StructureStatus(2))
  B.teamWanted, B.laneWanted = nil, nil
  for _, n in ipairs({ 'condition_Mission_RallyForParasite', 'condition_Mission_RallyForParasiteContest',
                       'condition_Mission_KillParasite', 'condition_Mission_CaptureWell', 'condition_Bot_GatherForParasite',
                       'condition_Bot_GatherForParasiteContest', 'condition_Bot_TimeToRally', 'condition_Bot_GroupedWithAllies',
                       'condition_Bot_AssignedToSiegeLane', 'ability_Bot_AssignToSiegeLane', 'condition_Bot_TowerPush',
                       'condition_Bot_InEnemyBase' }) do
    _G[n] = nil
  end
  nPlayer.GetPlayerIdForObject = nil
end)

-- ---- BOTJUNGLE: the Hunter takes part, the bots follow the humans' pings ------------------------------------------------
test('jungle: the gank opening, pure', function()
  local B = dgsim_bots
  local cfg = B.jungleConfig
  local function c(hp, allies, enemies, extra)
    local x = { hp = hp, allies = allies, enemies = enemies, underTower = false, dist = 80, visible = true }
    for k, v in pairs(extra or {}) do x[k] = v end
    return x
  end
  check(B.GankOpening(c(0.9, 1, 1), cfg), 'an ally fights him there: go')
  check(B.GankOpening(c(0.4, 0, 1), cfg), 'alone but low: go')
  check(not B.GankOpening(c(0.9, 0, 1), cfg), 'alone and healthy: no')
  check(not B.GankOpening(c(0.4, 1, 3), cfg), 'three of theirs against two of us: no')
  check(not B.GankOpening(c(0.9, 1, 2), cfg), 'two against two with the Hunter: no')
  check(B.GankOpening(c(0.9, 2, 2), cfg), 'three against two: go')
  check(not B.GankOpening(c(0.9, 1, 1, { levelGap = 2 }), cfg), 'two levels above him: no')
  check(not B.GankOpening(c(0.3, 1, 1, { underTower = true }), cfg), 'under their tower: no')
  check(not B.GankOpening(c(0.3, 1, 1, { visible = false }), cfg), 'not seen: no')
  check(not B.GankOpening(c(0.3, 1, 1, { dist = 200 }), cfg), 'too far: no')
end)

-- the team world of the BOTTEAM test, with the jungle and the ping layer on and a host ping queue
local function jungleWorld()
  local B = dgsim_bots
  local roles = compositionWorld()
  local board = { open = {}, nextId = 100, ended = {}, pings = {}, logs = {}, human = {} }
  nBotHost.RequestMission = function(team, mtype, target, agents, timeout, guid)
    for _, m in ipairs(board.open) do
      if m.team == team and m.type == mtype and m.target == target then m.agents = agents return m.id end
    end
    board.nextId = board.nextId + 1
    board.open[#board.open + 1] = { id = board.nextId, team = team, type = mtype, target = target, guid = guid,
                                    agents = agents, timeout = timeout }
    return board.nextId
  end
  nBotHost.OpenMission = function(team, i)
    local k = 0
    for _, m in ipairs(board.open) do
      if m.team == team then
        k = k + 1
        if k == i then return m.id, m.target, m.guid end
      end
    end
  end
  nBotHost.EndMission = function(id)
    for i, m in ipairs(board.open) do if m.id == id then table.remove(board.open, i) return true end end
    return false
  end
  nBotHost.IsMissionAvailable = function(team, mtype)
    for _, m in ipairs(board.open) do if m.team == team and m.type == mtype then return true end end
    return false
  end
  nBotHost.GetAvailableMission = function(bot, mtype)
    for _, m in ipairs(board.open) do if m.type == mtype then return m.id end end
    return 0
  end
  nBotHost.TeamPing = function(bot, target, x, y, z, ptype, why)
    board.pings[#board.pings + 1] = { bot = bot, type = ptype, why = why, x = x, y = y }
    return true
  end
  nBotHost.Log = function(text) board.logs[#board.logs + 1] = text end
  nBotHost.RespawnIn = function() return 0 end
  local lanes = {}
  nBotHost.AssignLane = function(bot, lane) lanes[bot] = lane end
  nBotHost.GetLane = function(bot) return lanes[bot] or 0 end
  -- the humans' pings: { seq, team, pinger, target, x, y, z, type }
  nBotHost.HumanPing = function(team, after)
    for _, p in ipairs(board.human) do
      if p.seq > after and p.team == team then return p.seq, p.pinger, p.target or kObjIDNone, p.x, p.y, 0, p.type, 0 end
    end
  end
  local function cond(extra)
    local c = extra or {}
    c[nConditionFns.Check] = c[nConditionFns.Check] or function() return true end
    c[nConditionFns.CheckEnd] = function() return false end
    c[1] = c[1] or function() return true end
    return c
  end
  condition_Mission_RallyForParasite = cond({ conditionGUID = 'rally' })
  condition_Mission_RallyForParasiteContest = cond({ conditionGUID = 'contest' })
  condition_Mission_KillParasite = cond({ conditionGUID = 'kill' })
  condition_Mission_CaptureWell = cond({ conditionGUID = 'capture' })
  condition_Bot_GatherForParasite = cond({ mortalRally = nVector.create(-66, -10, 0), spiritRally = nVector.create(66, 10, 0) })
  condition_Bot_TimeToRally = cond()
  condition_Bot_GroupedWithAllies = cond()
  condition_Bot_AssignedToSiegeLane = cond()
  ability_Bot_AssignToSiegeLane = {}
  ability_Bot_AssignToSiegeLane[nAbilityFns.Tick] = function() end
  condition_Bot_TowerPush = cond()
  condition_Bot_InEnemyBase = { defaultSearchRadius = 65 }
  -- ClearCamp's condition: the creeps (none here)
  condition_nearest_creep = { [1] = function() return false end }
  local ids = v5Structures()
  local para = W.add({ name = 'parasite', team = 0, npc = nNPCType.ObjectiveMajor, pos = P(-3, 3), maxhp = 5000,
                       untargetable = true })
  nDirector.GetParasiteID = function() return para end
  B.teamWanted, B.laneWanted, B.jungleWanted, B.pingWanted = true, true, true, true
  B.fixups = { veryOutnumbered = true, groupInLane = true, bailOnKill = true, parasite = true, campRadius = true }
  return roles, board, ids, para, lanes
end

-- the Hunter's camp answer: the point (x, y) itself, or a step of 40 from him toward it
local function campToward(hu, x, y)
  local ok, cx, cy = condition_Bot_NextCreepCamp[1](hu)
  if ok ~= true then return false end
  local p = W.get(hu).pos
  local d = math.sqrt((x - p.x) ^ 2 + (y - p.y) ^ 2)
  if d <= 40 then return math.abs(cx - x) < 0.01 and math.abs(cy - y) < 0.01 end
  local ex, ey = p.x + (x - p.x) * 40 / d, p.y + (y - p.y) * 40 / d
  return math.abs(cx - ex) < 0.01 and math.abs(cy - ey) < 0.01
end

local function cleanJungle()
  local B = dgsim_bots
  B.teamWanted, B.laneWanted, B.jungleWanted, B.pingWanted = nil, nil, nil, nil
  for _, n in ipairs({ 'condition_Mission_RallyForParasite', 'condition_Mission_RallyForParasiteContest',
                       'condition_Mission_KillParasite', 'condition_Mission_CaptureWell', 'condition_Bot_GatherForParasite',
                       'condition_Bot_TimeToRally', 'condition_Bot_GroupedWithAllies', 'condition_Bot_AssignedToSiegeLane',
                       'ability_Bot_AssignToSiegeLane', 'condition_Bot_TowerPush', 'condition_Bot_InEnemyBase',
                       'condition_nearest_creep' }) do
    _G[n] = nil
  end
end

test('jungle: the Hunter ganks a lane with an opening, then farms again', function()
  local B = dgsim_bots
  local roles, board = jungleWorld()
  local hu = shaper('hu', 1, -150, -40, { bot = true, maxhp = 700 })
  local g1 = shaper('g1', 1, -95, 40, { bot = true })      -- the top lane, fighting
  local tc = shaper('tc', 1, -80, -120, { bot = true })
  roles[hu], roles[g1], roles[tc] = 2, 0, 1
  local e1 = shaper('e1', 2, -90, 45, { bot = true, hp = 60 })  -- next to g1, off their towers
  for i = 2, 5 do shaper('e' .. i, 2, 150 + i, 150, { bot = true }) end  -- the rest at home: no push for team 1
  local s = B.ApplyFixups()
  check(B.fixups.jungle == true and string.find(s, 'Jungle true') ~= nil, 'applied: ' .. s)
  -- before gankFrom: farm
  W.matchTime = 120
  B.TeamTick()
  check(B.hunterTasks[hu] == nil, 'no gank before 150 s')
  check(select(2, condition_Bot_NextCreepCamp[1](hu)) == -59, 'the camps')
  W.matchTime = 200
  B.TeamTick()
  local tk = B.hunterTasks[hu]
  check(tk ~= nil and tk.kind == 'gank' and tk.target == e1, 'a gank on e1 (' .. tostring(tk and tk.kind) .. ')')
  local ok, x, y = condition_Bot_NextCreepCamp[1](hu)
  check(ok == true and campToward(hu, -90, 45), 'his camp is a step toward the target (' .. tostring(x) .. ', ' .. tostring(y) .. ')')
  check(condition_Bot_IsNearNextCreepCamp[1](hu) == false, 'not there yet')
  check(condition_nearest_creep[1](hu) == false, 'too far to fight: the original')
  -- he arrives: ClearCamp, his gambits hit e1
  W.get(hu).pos = P(-100, 35)
  check(condition_Bot_IsNearNextCreepCamp[1](hu) == true, 'there (within the gank radius)')
  local ok2, _, _, _, tgt = condition_nearest_creep[1](hu)
  check(ok2 == true and tgt == e1, 'ClearCamp hits e1')
  -- e1 dies: the gank ends with a kill, the cooldown holds the next one
  W.kill(e1)
  W.matchTime = 205
  B.TeamTick()
  check(B.hunterTasks[hu] == nil, 'farm again')
  check(B.jungleStats.ganks == 1 and B.jungleStats.gankKills == 1, 'one gank, one kill')
  local line
  for _, l in ipairs(board.logs) do if string.find(l, 'the gank ended: the target died', 1, true) then line = l end end
  check(line ~= nil, 'the end is logged')
  local e3 = shaper('e3', 2, -98, 42, { bot = true, hp = 30 })
  W.matchTime = 230
  B.TeamTick()
  check(B.hunterTasks[hu] == nil, 'cooling down')
  W.matchTime = 266
  B.TeamTick()
  check(B.hunterTasks[hu] ~= nil and B.hunterTasks[hu].target == e3, 'after the cooldown: the next one')
  -- e3 walks under its tower: the gank ends
  W.get(e3).pos = P(-30, 110)
  W.matchTime = 267
  B.TeamTick()
  check(B.hunterTasks[hu] == nil, 'under their tower: over')
  -- low HP: no gank
  W.get(e3).pos = P(-98, 42)
  W.get(hu).hp = 300
  W.matchTime = 400
  B.TeamTick()
  check(B.hunterTasks[hu] == nil, 'below 60 % HP: no gank')
  cleanJungle()
end)

test('jungle: the Hunter answers the team calls', function()
  local B = dgsim_bots
  local roles, board, ids, para = jungleWorld()
  local hu = shaper('hu', 1, -150, -40, { bot = true, maxhp = 700 })
  local g1 = shaper('g1', 1, -70, 70, { bot = true })
  local g2 = shaper('g2', 1, 70, -70, { bot = true })
  local tc = shaper('tc', 1, -80, 60, { bot = true })
  roles[hu], roles[g1], roles[g2], roles[tc] = 2, 0, 0, 1
  local enemies = {}
  for i = 1, 4 do enemies[i] = shaper('e' .. i, 2, 200 + i, 200, { bot = true }) end
  B.ApplyFixups()
  -- the base is attacked: he defends it
  W.matchTime = 300
  nBot.RequestMission(1, nMissionType.DefendBase, W.guardians[1], 5, 5, 'dt')
  B.TeamTick()
  local tk = B.hunterTasks[hu]
  check(B.TeamStateOf(1).call == 'defend' and tk ~= nil and tk.kind == 'defend', 'defend (' .. tostring(tk and tk.kind) .. ')')
  check(campToward(hu, -182, -182), 'to the Guardian')
  board.open = {}
  W.matchTime = 320
  B.TeamTick()
  check(B.hunterTasks[hu] == nil, 'the threat is over: farm')
  -- the parasite: the gather takes him to the rally point, the kill to the parasite
  W.get(para).untargetable = false
  local st = B.TeamStateOf(1)
  st.call, st.phase, st.since, st.phaseSince = 'parasite', 'gather', 700, 700
  W.matchTime = 701
  B.TeamTick()
  tk = B.hunterTasks[hu]
  check(tk ~= nil and tk.kind == 'gather', 'the gather (' .. tostring(tk and tk.kind) .. ')')
  check(campToward(hu, -66, -10), 'to the rally point')
  st.phase = 'kill'
  W.matchTime = 702
  B.TeamTick()
  check(B.hunterTasks[hu] ~= nil and B.hunterTasks[hu].kind == 'parasite', 'the kill: the parasite')
  local _, px, py = condition_Bot_NextCreepCamp[1](hu)
  check(math.abs(math.sqrt((px + 3) ^ 2 + (py - 3) ^ 2) - 6) < 0.01, 'to the parasite (its edge, PRETUSBOT)')
  -- a push: he joins, standing off the target toward his base
  W.kill(para)
  st.call, st.phase, st.target, st.lane, st.since, st.lastAdvantage, st.lastProgress = 'push', nil, ids[10], nLaneID.Bottom, 800, 800, 800
  W.kill(enemies[1])
  W.kill(enemies[2])
  W.matchTime = 801
  B.TeamTick()
  check(B.hunterTasks[hu] == nil, 'a push far from him (a tower): he keeps his camps')
  W.get(hu).pos = P(40, -60)
  W.matchTime = 802
  B.TeamTick()
  tk = B.hunterTasks[hu]
  check(tk ~= nil and tk.kind == 'push' and tk.target == ids[10], 'the push near him (' .. tostring(tk and tk.kind) .. ')')
  local sx, sy = select(2, condition_Bot_NextCreepCamp[1](hu))
  check(sx ~= nil and B.dist(P(sx, sy), P(40, -60)) <= 40.01, 'walked in steps of 40')
  check(tk ~= nil and B.dist(tk.pos, W.get(ids[10]).pos) > 20, 'short of the tower')
  -- the status line names his task
  B.nextTeamStatus = 0
  B.TeamTick()
  local status
  for _, l in ipairs(board.logs) do if string.find(l, '^team 1 status') then status = l end end
  check(status ~= nil and string.find(status, 'hunter push', 1, true) ~= nil, 'status: ' .. tostring(status))
  local summary
  for _, l in ipairs(board.logs) do if string.find(l, '^jungle: ') then summary = l end end
  check(summary ~= nil and string.find(summary, 'defend=1', 1, true) ~= nil, 'the jungle line: ' .. tostring(summary))
  cleanJungle()
end)

test('jungle: the Hunter holds a call, keeps a push across targets, defends from near, farms waves (HUNTERBOT)', function()
  local B = dgsim_bots
  local roles, board, ids = jungleWorld()
  local hu = shaper('hu', 1, -150, -40, { bot = true, maxhp = 700 })
  roles[hu] = 2
  for i = 1, 4 do shaper('e' .. i, 2, 200 + i, 200, { bot = true }) end
  B.ApplyFixups()
  local cfg = B.jungleConfig
  local function decide(t, call, cur, pos, target)
    W.matchTime = t
    W.get(hu).pos = pos or W.get(hu).pos
    local s = { t = t, team = 1, enemy = 2, parasite = {}, wells = {} }
    local st = { call = call, target = target }
    local h = { id = hu, alive = true, hp = 0.9, pos = W.get(hu).pos, team = 1 }
    return B.HunterDecide(s, st, h, { task = cur }, cfg)
  end
  -- defend: from 145 away he goes; from 400 away not, unless the Guardian is low
  local tk = decide(300, 'defend', nil, P(-150, -40))
  check(tk ~= nil and tk.kind == 'defend', 'defend from near (' .. tostring(tk and tk.kind) .. ')')
  tk = decide(300, 'defend', nil, P(150, 40))
  check(tk == nil, 'no defend from across the map (' .. tostring(tk and tk.kind) .. ')')
  W.get(W.guardians[1]).hp = W.get(W.guardians[1]).maxhp * 0.3
  tk = decide(300, 'defend', nil, P(150, 40))
  check(tk ~= nil and tk.kind == 'defend', 'the Guardian at 30 %: from anywhere')
  W.get(W.guardians[1]).hp = W.get(W.guardians[1]).maxhp
  -- the hold: the call ends 5 s after he took it: he keeps going; after holdS not
  local cur = decide(300, 'defend', nil, P(-150, -40))
  cur.since = 300
  tk = decide(305, 'farm', cur)
  check(tk == cur, 'held 5 s after the call ended')
  tk = decide(311, 'farm', cur)
  check(tk == nil, 'farm after holdS')
  B.hunterFixWanted = false
  tk = decide(305, 'farm', cur)
  check(tk == nil, 'bot_hunter_fix=0: no hold')
  tk = decide(300, 'defend', nil, P(150, 40))
  check(tk ~= nil and tk.kind == 'defend', 'bot_hunter_fix=0: defend from anywhere')
  B.hunterFixWanted = nil
  -- the push: joins from 200 away (BOTJUNGLE: 170), keeps it while the push has no target for a moment
  local tower = ids[10]
  local tp = W.get(tower).pos
  local from = P(tp.x - 200 * 0.6, tp.y - 200 * 0.8)
  tk = decide(800, 'push', nil, from, tower)
  check(tk ~= nil and tk.kind == 'push' and tk.target == tower, 'joins a push 200 away')
  B.hunterFixWanted = false
  check(decide(800, 'push', nil, from, tower) == nil, 'bot_hunter_fix=0: not from 200 away')
  B.hunterFixWanted = nil
  tk.since = 800
  local gap = decide(820, 'push', tk, from, nil)
  check(gap == tk, 'between two targets of the push: kept')
  -- catching up: 2 levels below his team's others he keeps farming (no push), 1 below he joins
  W.get(hu).level = 4
  local function behind(sum)
    local s2 = { t = 800, team = 1, enemy = 2, parasite = {}, wells = {}, levelSum = sum, levelCount = 5 }
    return B.HunterDecide(s2, { call = 'push', target = tower }, { id = hu, alive = true, hp = 0.9, pos = from, team = 1 },
                          {}, cfg)
  end
  local cu = behind(4 + 4 * 6)
  check(cu == nil, 'catching up: no push (' .. tostring(cu and cu.kind) .. ')')
  cu = behind(4 + 4 * 5)
  check(cu ~= nil and cu.kind == 'push', 'one level behind: the push')
  W.get(hu).level = nil
  -- waves: after gankFrom, no camp of his near, an enemy minion near him
  local m = W.add({ name = 'minion', team = 2, npc = nNPCType.MeleeMinion, pos = P(10, 10), maxhp = 400, hp = 400 })
  local closestCamp = nBotHost.ClosestCamp
  nBotHost.ClosestCamp = function() return nil end
  tk = decide(400, 'farm', nil, P(0, 0))
  check(tk ~= nil and tk.kind == 'wave' and tk.target == m, 'a wave without a camp (' .. tostring(tk and tk.kind) .. ')')
  nBotHost.ClosestCamp = function(x, y, z, team, penalty) return x + 30, y, 0 end
  tk = decide(400, 'farm', nil, P(0, 0))
  check(tk == nil, 'a camp 30 away: the camp')
  nBotHost.ClosestCamp = function() return nil end
  check(decide(150, 'farm', nil, P(0, 0)) == nil, 'no waves before gankFrom')
  nBotHost.ClosestCamp = closestCamp
  W.kill(m)
  -- CAMPFINISH: a nearly dead camp he finishes first: no push, the camp conditions answer its last creature
  local campFight = nBotHost.CampFight
  nBotHost.CampFight = function(bot) if bot == hu then return -60, -58, 1, 0.12, true end end
  local fin, why = decide(800, 'push', nil, from, tower)
  check(fin == nil and why == 'finishing a camp', 'finishing a camp: no push (' .. tostring(fin and fin.kind) .. ')')
  check(decide(300, 'defend', nil, P(-150, -40)) == nil, 'finishing a camp: the base waits')
  W.get(W.guardians[1]).hp = W.get(W.guardians[1]).maxhp * 0.3
  local d2 = decide(300, 'defend', nil, P(-150, -40))
  check(d2 ~= nil and d2.kind == 'defend', 'finishing a camp: the Guardian at 30 % does not wait')
  W.get(W.guardians[1]).hp = W.get(W.guardians[1]).maxhp
  B.campAsker = hu
  local v = nDirector.GetClosestCampPosition(1, W.get(hu).pos, false)
  B.campAsker = nil
  check(v.x == -60 and v.y == -58, 'finishing a camp: the camp conditions answer its last creature')
  nBotHost.CampFight = function(bot) if bot == hu then return -60, -58, 1, 0.8, false end end
  tk = decide(800, 'push', nil, from, tower)
  check(tk ~= nil and tk.kind == 'push', 'a camp not to finish: the push')
  nBotHost.CampFight = function() return -60, -58, 1, 0.12, true end
  B.campFinishWanted = false
  tk = decide(800, 'push', nil, from, tower)
  check(tk ~= nil and tk.kind == 'push', 'bot_camp_finish=0: the push')
  B.campFinishWanted = nil
  nBotHost.CampFight = campFight
  -- HUNTERROUTE: the opening route's camp for a Hunter, after the finishing camp; nothing from the host: the default
  local hunterRoute = nBotHost.HunterRoute
  nBotHost.HunterRoute = function(bot) if bot == hu then return 68, -165.6, 1 end end
  B.campAsker = hu
  local rv = nDirector.GetClosestCampPosition(1, W.get(hu).pos, false)
  check(rv.x == 68 and rv.y == -165.6, 'the opening route: green (' .. tostring(rv.x) .. ')')
  nBotHost.CampFight = function(bot) if bot == hu then return -60, -58, 1, 0.12, true end end
  rv = nDirector.GetClosestCampPosition(1, W.get(hu).pos, false)
  check(rv.x == -60, 'finishing a camp comes before the route')
  nBotHost.CampFight = campFight
  nBotHost.HunterRoute = function() return nil end
  rv = nDirector.GetClosestCampPosition(1, W.get(hu).pos, false)
  check(rv.x ~= 68, 'no route step: the default camp')
  B.campAsker = nil
  check(B.HunterRoutePos(hu) == nil, 'HunterRoutePos: nil without a step')
  nBotHost.HunterRoute = hunterRoute
  cleanJungle()
end)

test('kits: Faris and Freia leave SpinToWin, Petrus keeps his spin (HUNTERBOT)', function()
  local B = dgsim_bots
  local faris = shaper('golem', 1, 0, 0, { bot = true })
  local petrus = shaper('artificer', 1, 5, 0, { bot = true })
  local spinning = true
  condition_Bot_IsEActive = { [1] = function() return spinning end }
  B.fixups.kits = nil
  B.ApplyKitFixup()
  check(B.fixups.kits ~= true, 'waits for condition_Bot_CanUseE')
  condition_Bot_CanUseE = { [1] = function() return true end }
  B.ApplyKitFixup()
  check(B.fixups.kits == true, 'applied')
  check(condition_Bot_IsEActive[1](faris) == false, 'Faris: E active answers false')
  check(condition_Bot_CanUseE[1](faris) == false, 'Faris: no SpinToWin through CanUseE')
  check(condition_Bot_IsEActive[1](petrus) == true and condition_Bot_CanUseE[1](petrus) == true,
        'Petrus: the spin holds SpinToWin')
  B.kitsWanted = false
  check(condition_Bot_IsEActive[1](faris) == true and condition_Bot_CanUseE[1](faris) == true, 'bot_kits=0: the data')
  B.kitsWanted = nil
  condition_Bot_IsEActive, condition_Bot_CanUseE = nil, nil
end)

test('kits: the Desecrator\'s W only with an enemy shaper within 12, the Guardian keeps his shield (DESECBOT)', function()
  local B = dgsim_bots
  local des = shaper('desecrator', 1, 0, 0, { bot = true })
  local guard = shaper('guardian', 1, 5, 0, { bot = true })
  local shielded = false
  B.fixups.tacticianKits = nil
  B.ApplyTacticianKitFixup()
  check(B.fixups.tacticianKits ~= true, 'waits for condition_Guardian_ShieldActive')
  condition_Guardian_ShieldActive = { [1] = function() return shielded end }
  B.ApplyTacticianKitFixup()
  check(B.fixups.tacticianKits == true, 'applied')
  local SA = condition_Guardian_ShieldActive[1]
  check(SA(des) == true, 'nobody near: "shield active", no W')
  local far = shaper('far', 2, 20, 0)
  check(SA(des) == true, 'an enemy at 20: no W')
  local hidden = shaper('hidden', 2, 8, 0, { visible = { [0] = true, [1] = false, [2] = true } })
  check(SA(des) == true, 'an enemy at 8 he does not see: no W')
  local ally = shaper('ally', 1, 3, 0)
  check(SA(des) == true, 'an ally near: no W')
  local near = shaper('near', 2, 10, 0)
  check(SA(des) == false, 'an enemy at 10: the W gambits fire')
  check(SA(guard) == false, 'the Guardian: his own modifier (off)')
  shielded = true
  check(SA(guard) == true, 'the Guardian: his own modifier (on)')
  B.kitsWanted = false
  check(SA(des) == true, 'bot_kits=0: the data (the modifier)')
  B.kitsWanted = nil
  check(not B.ApplyFixups():find('TacticianKits false'), 'the fix-ups line')
  condition_Guardian_ShieldActive = nil
  local _ = far and hidden and ally and near
end)

test('jungle: the bots follow the humans pings', function()
  local B = dgsim_bots
  local roles, board, ids, para, lanes = jungleWorld()
  local hu = shaper('hu', 1, -150, -40, { bot = true, maxhp = 700 })
  local g1 = shaper('g1', 1, -70, 70, { bot = true })
  local g2 = shaper('g2', 1, 70, -70, { bot = true })
  local tc = shaper('tc', 1, -80, 60, { bot = true })
  local me = shaper('me', 1, -180, -180, {})  -- the human
  roles[hu], roles[g1], roles[g2], roles[tc] = 2, 0, 0, 1
  lanes[g1], lanes[g2], lanes[tc] = nLaneID.Top, nLaneID.Bottom, nLaneID.Top
  local enemies = {}
  for i = 1, 5 do enemies[i] = shaper('e' .. i, 2, 200 + i, 200, { bot = true }) end
  B.ApplyFixups()
  local seq = 0
  local function ping(ptype, target, x, y)
    seq = seq + 1
    board.human[#board.human + 1] = { seq = seq, team = 1, pinger = me, target = target, x = x, y = y, type = ptype }
  end
  -- attention on their bottom outer tower: the team pushes that lane
  ping(0, ids[10], 116, -29)
  W.matchTime = 300
  B.TeamTick()
  local st = B.TeamStateOf(1)
  check(st.call == 'push' and st.lane == nLaneID.Bottom and st.target == ids[10], 'a push on the pinged tower (' .. tostring(st.call) .. ')')
  check(lanes[g1] == nLaneID.Bottom and lanes[tc] == nLaneID.Bottom, 'the lanes go to the push')
  check(B.jungleStats.ordersTaken == 1, 'taken')
  -- retreat: the push is called off, the lanes go back
  ping(1, kObjIDNone, 100, -40)
  W.matchTime = 302
  B.TeamTick()
  check(st.call == 'farm' and lanes[g1] == nLaneID.Top, 'a retreat ping calls it off (' .. tostring(st.call) .. ')')
  -- attention on our own top outer tower: a DefendTower mission and the Hunter, who answers "on my way"
  ping(0, ids[1], -116, 29)
  W.matchTime = 304
  B.TeamTick()
  local protect
  for _, m in ipairs(board.open) do if m.type == nMissionType.DefendTower and m.target == ids[1] then protect = m end end
  check(protect ~= nil and protect.guid == B.pingGuids.protect, 'a DefendTower mission on it')
  local tk = B.hunterTasks[hu]
  check(tk ~= nil and tk.kind == 'ping' and campToward(hu, -116.3, 28.9), 'the Hunter goes (' .. tostring(tk and tk.kind) .. ')')
  local ack = board.pings[#board.pings]
  check(ack ~= nil and ack.bot == hu and ack.type == 3, 'he answers on my way')
  -- an enemy shaper near our top lane: KillShaper and the Hunter goes for him
  local e = enemies[3]
  W.get(e).pos = P(-90, 60)
  ping(0, e, -90, 60)
  W.matchTime = 306
  B.TeamTick()
  local kill
  for _, m in ipairs(board.open) do if m.type == nMissionType.KillShaper and m.target == e then kill = m end end
  check(kill ~= nil, 'a KillShaper mission')
  tk = B.hunterTasks[hu]
  check(tk ~= nil and tk.kind == 'ping' and tk.target == e, 'the Hunter goes for him')
  W.get(hu).pos = P(-95, 55)
  local ok, _, _, _, tgt = condition_nearest_creep[1](hu)
  check(ok == true and tgt == e, 'and hits him there')
  -- he dies: the ping order ends
  W.kill(e)
  W.matchTime = 307
  B.TeamTick()
  check(B.hunterTasks[hu] == nil or B.hunterTasks[hu].kind ~= 'ping', 'the pinged shaper died: the order ends')
  -- the parasite pinged before it is targetable: declined; after: a gather
  ping(0, para, -3, 3)
  W.matchTime = 400
  B.TeamTick()
  check(st.call ~= 'parasite' and B.jungleStats.ordersDeclined >= 1, 'not targetable yet: declined')
  W.get(para).untargetable = false
  ping(0, kObjIDNone, -5, 5)
  W.matchTime = 402
  B.TeamTick()
  check(st.call == 'parasite' and st.phase == 'gather', 'a ping at the parasite: gather (' .. tostring(st.call) .. ')')
  check(B.hunterTasks[hu] ~= nil and B.hunterTasks[hu].kind == 'gather', 'the Hunter gathers too')
  -- a warning ping does nothing
  ping(2, kObjIDNone, 0, 0)
  W.matchTime = 404
  B.TeamTick()
  check(st.call == 'parasite', 'danger: nothing')
  local said
  for _, l in ipairs(board.logs) do if string.find(l, 'human ping', 1, true) then said = l end end
  check(said ~= nil, 'the pings are logged: ' .. tostring(said))
  -- without the switch the pings stay unread
  B.pingWanted = nil
  ping(1, kObjIDNone, 0, 0)
  W.matchTime = 406
  B.TeamTick()
  check(st.call == 'parasite', 'bot_ping_follow=0: not read')
  cleanJungle()
end)

-- NULLPOS: a running Follow without an object that reads (0, 0) is reported and gets the bot's own position (it ends);
-- a point or an object is left alone; an unknown lane's centre is reported and is the top front, never (0, 0)
test('nullpos', function()
  local B = dgsim_bots
  local bot = shaper('PC_Warden', 2, 86, 72)
  local reports = {}
  nBotHost = { NullPos = function(obj, kind, lane) reports[#reports + 1] = { obj = obj, kind = kind, lane = lane } end }
  local saved, savedTree = nAgent.GetTargetPosition, nBehaviorTree
  local point, followTarget = nVector.create(0, 0, 0), kObjIDNone
  nAgent.GetTargetPosition = function() return point end
  nBehaviorTree = { GetTargetObjectID = function() return followTarget end }
  B.fixups.nullPos = nil
  B.ApplyNullPosFixup()
  check(B.fixups.nullPos == true, 'applied')
  local p = nAgent.GetTargetPosition(bot)
  check(#reports == 1 and reports[1].obj == bot and reports[1].kind == B.NULLPOS_FOLLOW, 'Follow reported')
  check(p ~= nil and p.x == 86 and p.y == 72, 'the bot gets its own position: ' .. tostring(p and p.x))
  point = nVector.create(0, 0, 3.5)  -- a height doesn't make (0, 0) a place
  nAgent.GetTargetPosition(bot)
  check(#reports == 2, '(0, 0, z) too')
  point = nVector.create(78, 20, 0)  -- a ward spot
  p = nAgent.GetTargetPosition(bot)
  check(#reports == 2 and p.x == 78 and p.y == 20, 'a point stays')
  point, followTarget = nVector.create(0, 0, 0), bot  -- the Follow has an object: the point is not read
  p = nAgent.GetTargetPosition(bot)
  check(#reports == 2 and p.x == 0, 'with an object nothing happens')
  point = nVector.create(-0.1, -0.1, 0)  -- an offset point is a place
  followTarget = kObjIDNone
  nAgent.GetTargetPosition(bot)
  check(#reports == 2, 'the offset is not null')
  B.ApplyNullPosFixup()  -- once
  nAgent.GetTargetPosition(bot)
  check(#reports == 2, 'applied once')
  -- the evidence counts: every point-mode pass, and the ones at (0, 0)
  check(B.stats.calls['Follow point reads'] == 5 and B.stats.calls['Follow point reads at (0, 0)'] == 2,
        'counts ' .. tostring(B.stats.calls['Follow point reads']) .. ' ' .. tostring(B.stats.calls['Follow point reads at (0, 0)']))
  -- BACKTOACTION's exact sequence, with the decompiled Follow loop's two reads per pass: the pursuit's point, then the
  -- host writes the zero vector while the Follow runs; the next pass gets the bot's own position and ends
  local reached, passes = false, 0
  point = nVector.create(78, 20, 0)
  local function followPass()
    passes = passes + 1
    local obj = nBehaviorTree.GetTargetObjectID()
    local goal = nAgent.GetTargetPosition(bot)
    if obj == kObjIDNone then
      local at = nGameObject.GetPosition(bot)
      if math.abs(at.x - goal.x) < 0.5 and math.abs(at.y - goal.y) < 0.5 then reached = true end
      return goal
    end
  end
  local g1 = followPass()
  check(g1.x == 78 and not reached, 'pass 1 walks to the ward spot')
  point = nVector.create(0, 0, 0)  -- the old EndPointPursuit: the zero vector under the running Follow
  local g2 = followPass()
  check(g2.x == 86 and g2.y == 72 and reached, 'pass 2: the own position, the Follow ends there')
  check(#reports == 3 and reports[3].kind == B.NULLPOS_FOLLOW, 'pass 2 reported')
  reports = { reports[1], reports[2] }
  -- the lane centre of an unknown lane
  local c = B.LaneCenter(0, 1)
  check(#reports == 3 and reports[3].kind == B.NULLPOS_LANE_CENTRE and reports[3].lane == 0, 'lane centre reported')
  check(not B.IsNullPos(c) and c.x == B.staticFronts[1].x, 'the top front instead')
  nAgent.GetTargetPosition, nBehaviorTree = saved, savedTree
  B.fixups.nullPos = nil
end)

print(checks .. ' checks, ' .. failed .. ' failed')
if failed > 0 then error('dgsim_bots_api tests failed') end
