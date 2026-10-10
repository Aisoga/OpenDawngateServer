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
  check(near(c.x, 0) and near(d.x, 0), 'an unknown lane: zero vectors')
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
  -- the Hunter: gather, hold, give up, the time cap with its cooldown, contest
  local cfg = B.compositionConfig
  local st = {}
  local function inp(t)
    local i = { standing = true, rallyAllies = 0, paraAllies = 0, paraEnemies = 0, inCombat = false, hp = 1 }
    for k, v in pairs(t or {}) do i[k] = v end
    return i
  end
  check(not B.HunterParasiteDecision(st, inp(), 100, cfg, 3), 'nobody gathered: no')
  check(not B.HunterParasiteDecision(st, inp({ rallyAllies = 2 }), 101, cfg, 3), '2 at the rally point are not enough')
  check(B.HunterParasiteDecision(st, inp({ rallyAllies = 3 }), 102, cfg, 3), '3 gathered: go')
  check(B.HunterParasiteDecision(st, inp(), 106, cfg, 3), 'held 4 s after it stopped holding')
  check(not B.HunterParasiteDecision(st, inp(), 111, cfg, 3), 'dropped after 8 s')
  check(B.HunterParasiteDecision(st, inp({ inCombat = true, paraAllies = 2 }), 112, cfg, 3), '2 allies fighting it: go')
  check(not B.HunterParasiteDecision(st, inp({ standing = false, rallyAllies = 5 }), 113, cfg, 3), 'no parasite: no')
  st = {}
  check(B.HunterParasiteDecision(st, inp({ rallyAllies = 3 }), 200, cfg, 3), 'start')
  check(B.HunterParasiteDecision(st, inp({ rallyAllies = 3 }), 340, cfg, 3), 'still within 150 s')
  check(not B.HunterParasiteDecision(st, inp({ rallyAllies = 3 }), 351, cfg, 3), 'capped at 150 s')
  check(not B.HunterParasiteDecision(st, inp({ rallyAllies = 3 }), 380, cfg, 3), 'cooldown')
  check(B.HunterParasiteDecision(st, inp({ rallyAllies = 3 }), 412, cfg, 3), 'after the cooldown')
  st = {}
  check(not B.HunterParasiteDecision(st, inp({ paraEnemies = 1 }), 10, cfg, 3), 'one enemy near, parasite idle: no')
  check(B.HunterParasiteDecision(st, inp({ paraEnemies = 1, inCombat = true }), 11, cfg, 3), 'a steal in progress: contest')
  st = {}
  check(not B.HunterParasiteDecision(st, inp({ paraEnemies = 4, inCombat = true }), 10, cfg, 3), 'four enemies: no')
  check(not B.HunterParasiteDecision(st, inp({ paraEnemies = 2, hp = 0.3 }), 11, cfg, 3), 'low HP: no')
  check(B.HunterParasiteDecision(st, inp({ paraEnemies = 2 }), 12, cfg, 3), 'two enemies at it: contest')
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

test('composition: the Hunter treats the parasite as a camp, only when allies gathered', function()
  local B = dgsim_bots
  local roles = compositionWorld()
  local hunter = shaper('hunter', 1, -100, -50, { bot = true, maxhp = 700 })
  roles[hunter] = 2
  local para = W.add({ name = 'parasite', team = 0, npc = nNPCType.ObjectiveMajor, pos = P(-3, 3), maxhp = 5000 })
  W.objectiveFor = para
  nDirector.GetParasiteID = function() return para end
  B.ApplyFixups()
  local ok, x = condition_Bot_NextCreepCamp[1](hunter)
  check(ok == true and x == -59, 'nobody gathered: the next camp')
  check(condition_nearest_objective[1](hunter) == false, 'the parasite is not offered to the Hunter')
  check(condition_Bot_ShouldGank[1](hunter) == false, 'no gank for the Hunter')
  roles[hunter] = 1
  check(condition_Bot_ShouldGank[1](hunter) == true and condition_nearest_objective[1](hunter) == true, 'other roles: the original')
  roles[hunter] = 2
  shaper('a1', 1, -36, -6, { bot = true })
  shaper('a2', 1, -37, -5, { bot = true })
  check(select(2, condition_Bot_NextCreepCamp[1](hunter)) == -59, 'two at the rally point: not yet')
  shaper('a3', 1, -35, -7, { bot = true })
  local ok2, px, py = condition_Bot_NextCreepCamp[1](hunter)
  check(ok2 == true and px == -3 and py == 3, 'three gathered: the parasite is his camp (' .. tostring(px) .. ', ' .. tostring(py) .. ')')
  check(condition_Bot_IsNearNextCreepCamp[1](hunter) == false, 'the near test uses the parasite too')
  check(select(1, condition_nearest_objective[1](hunter)) == true, 'on the parasite: ClearCamp may hit it')
  W.kill(para)
  W.matchTime = 130
  check(select(2, condition_Bot_NextCreepCamp[1](hunter)) == -59, 'the parasite died: back to the camps')
  roles[hunter] = 1
  check(select(2, condition_Bot_NextCreepCamp[1](hunter)) == -59, 'a Tactician never goes (the host asks BotRole)')
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

print(checks .. ' checks, ' .. failed .. ' failed')
if failed > 0 then error('dgsim_bots_api tests failed') end
