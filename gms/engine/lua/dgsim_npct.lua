-- Test probe for the NPC brain's targets ([host] test_lua=<s>:dgsim_npct.lua, called many times; global
-- dgsim_npct_plan picks what each call does, default below). Two parts:
--   the parasite: a test stage-2 parasite at (-8, 6) beside the human (teleported to (-12, 2), within melee reach 8),
--     hit once by the human so that it fights back; later calls heal the human to full and report the parasite's
--     aggro list, its blackboard's attackCounter and stageThreeAlternate and both HPs; then a stage-3 one the same way.
--     Expected in the host log: `npc target:` lines of ability_Parasite_MeleeBasic on the human ("the condition's
--     target"), four basics, then ability_Parasite_PersistentFire (stage 2); stage 3: four basics, then
--     ability_Parasite_OrbLauncher and PersistentFire in turn.
--   the Guardian: the enemy Guardian's Locus removed (KillObject: it would kill the human at once), the human
--     teleported 22 units in front of the enemy Guardian and a minion of the human's team 15 units in front of it;
--     later calls heal the human, replace a dead minion and report the Guardian's list. Expected: basic attacks on the
--     human (the condition's target) while he lives, on the minion while he is dead, and specials on the human (a
--     random pick that indexes past the list, math.random(0, n) + 1, falls back to the Guardian's current target).
-- Plan entries: 'para2' (create the stage 2, teleport the human), 'para3' (the stage 3: the old test parasite killed
-- first), 'hit' (the human hits the current test parasite, 50 true), 'keep' (heal, report), 'guard' (the Guardian
-- setup), 'gkeep' (heal, report the Guardian). Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local function pos(o)
  local v = o and o ~= kObjIDNone and nGameObject.GetPosition(o)
  if not v then return 'nil' end
  return string.format('(%.1f, %.1f)', v.x, v.y)
end

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    -- nPlayer.IsBot takes an object, not a player id
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c, pid end
  end
end

local function list(o)
  local ids = {}
  local ok, targets = pcall(nAgent.GetTargetsOnAggroList, o)
  if ok and type(targets) == 'table' then
    for _, t in ipairs(targets) do ids[#ids + 1] = tostring(t) end
  end
  return '[' .. table.concat(ids, ' ') .. ']'
end

local function alive(o)
  return o ~= nil and o ~= kObjIDNone and nGameObject.IsAlive(o)
end

local function teleport(o, x, y)
  local v = nGameObject.GetPosition(o)
  nLocomotion.TeleportToPositionAndFacing(o, nVector.create(x, y, v.z), nVector.create(1, 0, 0))
end

local function heal(h)
  if not alive(h) then return 'dead' end
  local before = nGameObject.GetHitPoints(h)
  -- the parasite's fire stacks reduce healing: heal a few times
  for _ = 1, 8 do
    local missing = nGameObject.GetMaxHitPoints(h) - nGameObject.GetHitPoints(h)
    if missing < 1 then break end
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(h, kObjIDNone)
    nGameObject.HealDamage(snap, h, missing, 0)
    nAttribute.RemoveAttributeSnapshot(snap)
  end
  return f1(before) .. '->' .. f1(nGameObject.GetHitPoints(h))
end

local function hit(h, o, amount)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(h, kObjIDNone)
  local s, dealt = nGameObject.TakeDamage(snap, o, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  return s, dealt
end

local DEFAULT_PLAN = { 'para2', 'hit' }
for _ = 1, 30 do DEFAULT_PLAN[#DEFAULT_PLAN + 1] = 'keep' end
DEFAULT_PLAN[#DEFAULT_PLAN + 1] = 'para3'
DEFAULT_PLAN[#DEFAULT_PLAN + 1] = 'hit'
for _ = 1, 30 do DEFAULT_PLAN[#DEFAULT_PLAN + 1] = 'keep' end
DEFAULT_PLAN[#DEFAULT_PLAN + 1] = 'guard'
for _ = 1, 40 do DEFAULT_PLAN[#DEFAULT_PLAN + 1] = 'gkeep' end

local function newParasite(h, stage)
  if alive(dgsim_npct_para) then nGameObject.KillObject(dgsim_npct_para) end
  dgsim_npct_para = nObjectManager.CreateObject(nUtil.GetAsset('Objective_SpiritMonster_State' .. stage .. '.Noun'),
    nTeamID.NPC, nVector.create(-8.0, 6.0, 0), kZeroVector, 1)
  if alive(h) then teleport(h, -12.0, 2.0) end
  p(string.format('npct: test stage %d parasite %s at %s, the human %s at %s', stage, tostring(dgsim_npct_para),
    pos(dgsim_npct_para), tostring(h), pos(h)))
end

local function reportParasite(h)
  local o = dgsim_npct_para
  if not alive(o) then
    p('npct: no living test parasite')
    return
  end
  if alive(h) and nGameObject.GetObjectDistance(o, h) > 9 then teleport(h, -12.0, 2.0) end
  if alive(h) and list(o) == '[]' then  -- the human died and came back: hit it again so that it fights
    local s, dealt = hit(h, o, 50)
    p('npct: the human hits the test parasite again:', tostring(s), f1(dealt))
  end
  p(string.format('npct: parasite %s rank %s hp %s list %s attackCounter %s stageThreeAlternate %s; human %s at %s '
    .. 'dist %s hp %s', tostring(o), tostring(nGameObject.GetNPCRank(o)), f1(nGameObject.GetHitPoints(o)), list(o),
    tostring(nAgent.GetBlackboardData_Int(o, nUtil.SPID('attackCounter'))),
    tostring(nAgent.GetBlackboardData_Bool(o, nUtil.SPID('stageThreeAlternate'))), tostring(h), pos(h),
    alive(h) and f1(nGameObject.GetObjectDistance(o, h)) or '-', heal(h)))
end

local function guardSetup(h)
  if alive(dgsim_npct_para) then nGameObject.KillObject(dgsim_npct_para) end
  local team = nGameObject.GetTeam(h)
  local enemy = team == nTeamID.Mortal and nTeamID.Spirit or nTeamID.Mortal
  local g = nBattlegrounds.GetGuardianOnTeam(enemy)
  dgsim_npct_guard = g
  local gp = nGameObject.GetPosition(g)
  local len = math.sqrt(gp.x * gp.x + gp.y * gp.y)
  local dx, dy = -gp.x / len, -gp.y / len  -- toward the map's centre
  dgsim_npct_spot = { gp.x + dx * 22, gp.y + dy * 22 }
  dgsim_npct_minion_spot = { gp.x + dx * 15 - dy * 4, gp.y + dy * 15 + dx * 4 }
  local ids = nObjectManager.GetObjectsInRadius(gp, 90, nGetObjectsSortType.Distance, nTargetType.EnemyNPCs, team,
    kObjIDNone, kObjIDNone, 0, 0) or {}
  for _, id in ipairs(ids) do
    local name = tostring(nGameObject.GetAssetName(id))
    if string.find(name, 'tower_v5', 1, true) then
      nGameObject.KillObject(id)
      p('npct: the enemy Locus', tostring(id), name, 'at', pos(id), 'removed for the test, alive',
        tostring(nGameObject.IsAlive(id)))
    end
  end
  teleport(h, dgsim_npct_spot[1], dgsim_npct_spot[2])
  p(string.format('npct: enemy Guardian %s at %s, the human %s teleported to %s (dist %s)', tostring(g), pos(g),
    tostring(h), pos(h), f1(nGameObject.GetObjectDistance(g, h))))
end

local function guardKeep(h)
  local g = dgsim_npct_guard
  if not alive(dgsim_npct_minion) then
    local team = nGameObject.GetTeam(h)
    local noun = team == nTeamID.Mortal and 'Minion_Mortal_Melee.Noun' or 'Minion_Spirit_Melee.Noun'
    dgsim_npct_minion = nObjectManager.CreateObject(nUtil.GetAsset(noun), team,
      nVector.create(dgsim_npct_minion_spot[1], dgsim_npct_minion_spot[2], 0), kZeroVector, 1)
    p('npct: a minion of the human\'s team', tostring(dgsim_npct_minion), 'at', pos(dgsim_npct_minion))
  end
  if alive(h) and alive(g) and nGameObject.GetObjectDistance(g, h) > 26 then
    teleport(h, dgsim_npct_spot[1], dgsim_npct_spot[2])
  end
  p(string.format('npct: Guardian %s hp %s list %s shield %s special cooldown %s; human %s dist %s hp %s; minion %s '
    .. 'hp %s', tostring(g), f1(nGameObject.GetHitPoints(g)), list(g),
    tostring(nModifier.AgentHasModifierMatchingGUID(g, nUtil.SPID('modifier_Tower_Immunity'))),
    tostring(nModifier.AgentHasModifierMatchingGUID(g, nUtil.SPID('modifier_Guardian_SpecialAbility_Cooldown'))),
    tostring(h), alive(h) and f1(nGameObject.GetObjectDistance(g, h)) or '-', heal(h), tostring(dgsim_npct_minion),
    alive(dgsim_npct_minion) and f1(nGameObject.GetHitPoints(dgsim_npct_minion)) or '-'))
end

local ok, err = pcall(function()
  dgsim_npct_calls = (dgsim_npct_calls or 0) + 1
  local plan = dgsim_npct_plan or DEFAULT_PLAN
  local action = plan[dgsim_npct_calls] or 'keep'
  local h = human()
  p(string.format('npct call %d (%s) at match %s', dgsim_npct_calls, action, f1(nGameSimulator.GetMatchTime())))
  if not h then
    p('npct: no human')
    return
  end
  if action == 'para2' then
    newParasite(h, 2)
  elseif action == 'para3' then
    newParasite(h, 3)
  elseif action == 'hit' then
    local s, dealt = hit(h, dgsim_npct_para, 50)
    p('npct: the human hits the test parasite:', tostring(s), f1(dealt))
    reportParasite(h)
  elseif action == 'keep' then
    reportParasite(h)
  elseif action == 'guard' then
    guardSetup(h)
    guardKeep(h)
  elseif action == 'gkeep' then
    guardKeep(h)
  end
end)
if not ok then p('npct: ERROR', err) end
return table.concat(out, '\n')
