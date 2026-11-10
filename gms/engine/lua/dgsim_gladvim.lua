-- GLADVIM probe: a melee Gladiator's stored minions and the lane agents ([host] gladiator_skip_lane_agents, on by
-- default). On MM_Map_v3 (the lane agents' nouns); the human is the Gladiator (test_roles=0:0) and melee
-- ([host] noun=0x1A0C443C, warden.Noun). test_lua=<T>:dgsim_gladvim.lua,<T+3>:(the same). Parts:
--   part 1: an enemy Deer Major (Minion_MM_v3_Deer_Major, NPC type 2, vim 500) beside the human dies by KillObject
--     without a killer (not his kill): the counter none and nothing stored with the key at 1; with 0 the script's rule,
--     counter 1 and 1,500 stored. Then an enemy lane melee minion beside him dies the same way (stored 3 x vim, counter
--     +1), then his own lethal hit on a third: gold + 9 x vim (the fatal-hit bonus 3 x vim, the role's grant 3 x vim, the
--     stored lane minion 3 x vim) with the key at 1; + 9 x vim + 1,500 with 0;
--   part 2: a lane minion dies beside him (stored 3 x vim), then his own lethal hit on a Deer Major: gold + 500 + 500
--     (its vim, the role's lastHitMinionGoldModifier part) + 3 x vim (the store pays on a lane agent's last hit), the
--     same with the key at 0 (his own kill stores nothing).
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local ok, err = pcall(function()
  local human, humanPid
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then human, humanPid = c, pid break end
  end
  if not human then
    p('gladvim: no human player creature')
    return
  end
  dgsim_gladvim_part = (dgsim_gladvim_part or 0) + 1
  local part = dgsim_gladvim_part
  local function gold() return nPlayer.GetPlayerAvailableGold(humanPid) end
  local enemy = nGameObject.GetTeam(human) == nTeamID.Spirit and nTeamID.Mortal or nTeamID.Spirit
  local function spawn(noun, i)
    local at = nGameObject.GetPosition(human)
    return nObjectManager.CreateObject(nUtil.GetAsset(noun), enemy, nVector.create(at.x + 6 + 2 * i, at.y + 4, 0), kZeroVector, 1)
  end
  local function minion(i)
    return spawn(enemy == nTeamID.Mortal and 'Minion_Mortal_Melee.Noun' or 'Minion_Spirit_Melee.Noun', i)
  end
  local function deer(i) return spawn('Minion_MM_v3_Deer_Major.Noun', i) end
  local function lethal(target)
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(human, kObjIDNone)
    local basic = nBit.Or(nBit.Or(nDescriptors.IsMelee, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
    local s, dealt = nGameObject.TakeDamage(snap, target, 50000, 0, basic, kZeroVector)
    nAttribute.RemoveAttributeSnapshot(snap)
    return s, dealt
  end
  local function counter()
    local id = nModifier.GetFirstModifierByGUID(human, nUtil.SPID('modifier_Role_Gladiator_Counter'))
    if id == nil or id == kObjIDNone then return 'none' end
    return tostring(nModifier.GetStackCount(id))
  end
  p(string.format('gladvim part %d: human %s %s pid %s role %s %s, alive %s, gold %.2f, counter %s', part, tostring(human),
    nGameObject.GetAssetName(human), tostring(humanPid), tostring(nPlayer.GetPlayerRole(humanPid)),
    nGameObject.IsMeleeAttackStyle(human) and 'melee' or 'ranged', tostring(nGameObject.IsAlive(human)), gold(), counter()))
  if part == 1 then
    local d = deer(1)
    if d == nil or d == kObjIDNone then p('gladvim 1: no Deer Major (not MM_Map_v3?)') return end
    local _, dvim = nGameObject.GetRewardForNPC(d)
    local g0 = gold()
    nGameObject.KillObject(d, kObjIDNone, false)
    local g1 = gold()
    local c1 = counter()
    local m1, m2 = minion(2), minion(3)
    local _, vim = nGameObject.GetRewardForNPC(m1)
    nGameObject.KillObject(m1, kObjIDNone, false)
    local g2 = gold()
    local c2 = counter()
    local s, dealt = lethal(m2)
    local g3 = gold()
    p(string.format('gladvim 1: Deer Major %s vim %s killed by nobody beside him: gold +%.2f, counter %s (expected none; '
      .. '1 with gladiator_skip_lane_agents=0); a lane minion vim %s the same: gold +%.2f (expected %s), counter %s (expected 1; '
      .. '2 with 0); his last hit on another -> %s dealt %s: gold +%.2f (expected %s = 9 x vim; %s with 0), counter %s '
      .. '(expected none)', tostring(d), f2(dvim), g1 - g0, c1, f2(vim), g2 - g1, f2(vim), c2, tostring(s), f2(dealt), g3 - g2,
      f2(9 * vim), f2(9 * vim + 3 * dvim), counter()))
  else
    local m1 = minion(1)
    local _, vim = nGameObject.GetRewardForNPC(m1)
    local g0 = gold()
    nGameObject.KillObject(m1, kObjIDNone, false)
    local g1 = gold()
    local c1 = counter()
    local d = deer(2)
    if d == nil or d == kObjIDNone then p('gladvim 2: no Deer Major (not MM_Map_v3?)') return end
    local _, dvim = nGameObject.GetRewardForNPC(d)
    local s, dealt = lethal(d)
    local g2 = gold()
    p(string.format('gladvim 2: a lane minion vim %s killed by nobody beside him: gold +%.2f, counter %s (expected 1); his '
      .. 'last hit on a Deer Major %s vim %s -> %s dealt %s: gold +%.2f (expected %s = 2 x its vim + 3 x the minion vim, '
      .. 'with or without the key), counter %s (expected none)', f2(vim), g1 - g0, c1, tostring(d), f2(dvim), tostring(s),
      f2(dealt), g2 - g1, f2(2 * dvim + 3 * vim), counter()))
  end
end)
if not ok then p('gladvim: ERROR', err) end
return table.concat(out, '\n')
