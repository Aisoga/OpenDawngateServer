-- D13-live probe (docs/DAMAGE.md *Combat edge cases (D13)*, the roles' passives): what each role's modifier does, on
-- the human's shaper (melee: [host] noun=0x1A0C443C, warden.Noun), whether or not [host] role_passives gave it its own
-- (the probe requests a role modifier the shaper lacks). test_lua=<T>:dgsim_d13_role_effects.lua,<T+2>:(the same),
-- <T+4>:(the same). Parts:
--   part 1, the Gladiator (modifier_Role_Gladiator, melee shapers only): three enemy melee minions next to the human;
--     two die by KillObject without a killer (within its OnNearbyDeath radius, not its kills): expected
--     modifier_Role_Gladiator_Counter at 2 stacks and 2 x vim x 6 x 0.5 stored (vim = GetRewardForNPC's, the area
--     share: 4); then the human's lethal hit on the third: expected its last-hit gold (16, the host's death line) + the
--     stored 6 x vim (OnGrantGold returns grantGoldAmount + totalStoredRewards; d13_run5b: +40), the counter removed
--     (and nPlayer.IncrementCreepScore(floor(2 / 2) = 1) on top of the last hit's creep score);
--     the human also gets the area share of the two minions killed by nobody (+4 each, before the last hit);
--   part 2, the Hunter (modifier_Role_Hunter): a jungle creep (Creep_Jungle_Fish_Minor, team NPC) hit for 100 physical:
--     expected 110 (ModifyDamageOutgoing + 10 % on creeps); the human hurt 200, then kills the creep: expected a heal
--     of 2.5 % of the creep's max HP (OnKill) and its vim, + 30 in about a quarter of the kills (OnGrantGold,
--     GastyRNG 0.25); eight more creeps killed to count the procs;
--   part 3, the Predator (modifier_Role_Predator): an enemy well worker (Titan_Node_Follower) killed by the human:
--     expected 2 x its vim (OnGrantGold doubles gold from workers; the worker's gold is a team share, so the
--     teammates get 1 x).
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local ok, err = pcall(function()
  local human, pid0
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then
      human, pid0 = c, pid
      break
    end
  end
  if not human then
    p('d13 roles fx: no human player creature')
    return
  end
  dgsim_d13_glad_part = (dgsim_d13_glad_part or 0) + 1
  local part = dgsim_d13_glad_part
  local function has(name)
    local id = nModifier.GetFirstModifierByGUID(human, nUtil.SPID(name))
    if id == nil or id == kObjIDNone then return nil end
    return id
  end
  local function ensure(name)
    local had = has(name)
    if had then return 'had ' .. tostring(had) end
    local s = nAttribute.CreateAttributeSnapshot_FromObject(human, kObjIDNone)
    local r = nModifier.RequestModifier(human, human, nUtil.SPID(name), s, 1, kZeroVector, nil)
    nAttribute.RemoveAttributeSnapshot(s)
    return 'requested ' .. tostring(r)
  end
  local function gold() return nPlayer.GetPlayerAvailableGold(pid0) end
  local function counter()
    local id = has('modifier_Role_Gladiator_Counter')
    return id and tostring(nModifier.GetStackCount(id)) or 'none'
  end
  local function lethal(target)
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(human, kObjIDNone)
    local basic = nBit.Or(nBit.Or(nDescriptors.IsMelee, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
    local s, dealt = nGameObject.TakeDamage(snap, target, 5000, 0, basic, kZeroVector)
    nAttribute.RemoveAttributeSnapshot(snap)
    return s, dealt
  end
  local team = nGameObject.GetTeam(human)
  local enemy = team == nTeamID.Spirit and nTeamID.Mortal or nTeamID.Spirit
  local hp = nGameObject.GetPosition(human)
  local function spawn(noun, tm, i)
    return nObjectManager.CreateObject(nUtil.GetAsset(noun), tm, nVector.create(hp.x + 6 + 2 * i, hp.y + 4, 0), kZeroVector, 1)
  end
  p(string.format('d13 roles fx part %d: human %s %s role %s %s, gold %.2f', part, tostring(human),
    nGameObject.GetAssetName(human), tostring(nPlayer.GetPlayerRole(pid0)),
    nGameObject.IsMeleeAttackStyle(human) and 'melee' or 'ranged', gold()))
  if part == 1 then
    p('d13 roles fx 1 Gladiator:', ensure('modifier_Role_Gladiator'), 'counter', counter())
    local noun = team == nTeamID.Spirit and 'Minion_Mortal_Melee.Noun' or 'Minion_Spirit_Melee.Noun'
    local m = {spawn(noun, enemy, 1), spawn(noun, enemy, 2), spawn(noun, enemy, 3)}
    local _, vim = nGameObject.GetRewardForNPC(m[1])
    for i = 1, 2 do nGameObject.KillObject(m[i], kObjIDNone, false) end
    p(string.format('d13 roles fx 1 Gladiator: vim per minion %s; after two deaths by nobody: counter %s (expected 2)', f2(vim),
      counter()))
    local before = gold()
    local s, dealt = lethal(m[3])
    p(string.format('d13 roles fx 1 Gladiator: the last hit -> %s dealt %s; gold %.2f -> %.2f (+%.2f; expected the last hit\'s own '
      .. 'gold (the host\'s death line: 16 for a level-1 melee minion) + %s stored = 2 x vim x 6 x 0.5); counter %s (expected none)',
      tostring(s), f2(dealt), before, gold(), gold() - before, f2(type(vim) == 'number' and vim * 6 or nil), counter()))
  elseif part == 2 then
    p('d13 roles fx 2 Hunter:', ensure('modifier_Role_Hunter'), 'Gladiator counter', counter())
    local c = spawn('Creep_Jungle_Fish_Minor.Noun', nTeamID.NPC, 1)
    local maxhp = nGameObject.GetMaxHitPoints(c)
    local _, vim = nGameObject.GetRewardForNPC(c)
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(human, kObjIDNone)
    local h0 = nGameObject.GetHitPoints(c)
    nGameObject.TakeDamage(snap, c, 100, 0, nDescriptors.IsPhysicalDamage, kZeroVector)
    local h1 = nGameObject.GetHitPoints(c)
    nGameObject.TakeDamage(snap, human, 200, 0, nDescriptors.IsTrueDamage, kZeroVector)
    nAttribute.RemoveAttributeSnapshot(snap)
    local hh, before = nGameObject.GetHitPoints(human), gold()
    lethal(c)
    p(string.format('d13 roles fx 2 Hunter: creep %s IsCreep %s max hp %s vim %s; 100 physical -> hp %s -> %s (expected -110 before '
      .. 'its armor); the kill: human hp %s -> %s (expected +%s, 2.5 %%), gold +%.2f (expected %s or %s)', tostring(c),
      tostring(nGameObject.IsCreep(c)), f2(maxhp), f2(vim), f2(h0), f2(h1), f2(hh), f2(nGameObject.GetHitPoints(human)),
      f2(maxhp * 0.025), gold() - before, f2(vim), f2(type(vim) == 'number' and vim + 30 or nil)))
    local procs, total, sumVim = 0, 0, 0
    for i = 1, 8 do
      local k = spawn('Creep_Jungle_Fish_Minor.Noun', nTeamID.NPC, i)
      local _, v = nGameObject.GetRewardForNPC(k)
      local g = gold()
      lethal(k)
      local d = gold() - g
      total, sumVim = total + d, sumVim + v
      if d > v + 15 then procs = procs + 1 end
    end
    p(string.format('d13 roles fx 2 Hunter: 8 more creep kills: gold +%.2f, their vim %.2f, +30 procs %d (expected about 2)', total,
      sumVim, procs))
  else
    p('d13 roles fx 3 Predator:', ensure('modifier_Role_Predator'))
    local w = spawn('Titan_Node_Follower.Noun', enemy, 1)
    local _, vim = nGameObject.GetRewardForNPC(w)
    local before = gold()
    local s, dealt = lethal(w)
    p(string.format('d13 roles fx 3 Predator: worker %s vim %s; the kill -> %s dealt %s; gold +%.2f (expected %s = 2 x vim)',
      tostring(w), f2(vim), tostring(s), f2(dealt), gold() - before, f2(type(vim) == 'number' and 2 * vim or nil)))
  end
end)
if not ok then p('d13 roles fx: ERROR', err) end
return table.concat(out, '\n')
