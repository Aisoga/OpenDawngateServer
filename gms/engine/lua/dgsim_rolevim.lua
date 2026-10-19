-- ROLEVIM probe: the roles' NPC-kill gold, RolesTuning's lastHitMinionGoldModifier
-- (Gladiator 2.0) and proximityToMinionOrCreatureDeathGoldModifier (Tactician 2.0) that the host applies since ROLEVIM
-- ([host] role_passives; since ROLEVIM-2 the role's part is its own grant and a last hitter gets no area share), and
-- the Tactician's shaper-hit vim (modifier_Role_Tactician). The human is the Gladiator
-- (role 0 without shaper select; a melee shaper for the storage part: [host] noun=0x1A0C443C, warden.Noun); the
-- Tactician is a bot of role 1 (the practice game's Guardian, Artist, Warden or TidalQueen).
-- test_lua=<T>:dgsim_rolevim.lua,<T+2>:(the same),<T+4>:(the same). Parts:
--   part 1, the Gladiator: three enemy melee minions next to the human; two die by KillObject without a killer (its
--     area share each, the role's storage: modifier_Role_Gladiator_Counter 2 stacks), then the human's lethal hit on
--     the third: expected gold + 12 x vim (4: +48) = the fatal-hit bonus 3 x vim + the role's own grant 3 x vim + the
--     stored 2 x vim x 6 x 0.5 (no area share for the last hitter); with role_passives=0 + 3 x vim (12);
--   part 2, the Tactician's proximity: an enemy minion next to the Tactician bot dies by KillObject without a killer:
--     expected + 2 x vim (8 = 4 + the role's own 4; 1 x without); then its own lethal hit on another: expected + 3 x vim
--     (the fatal-hit bonus only: 12, as any role without a last-hit modifier, with or without);
--   part 3, the Tactician's shaper hits: 1 damage on an enemy shaper with the Tactician's basic ability's snapshot,
--     without IsMelee: expected + 20 (ranged when the basic's effective range > 351 / 34 = 10.32, else the melee
--     marker); again at once: expected + 0 (5-s marker); then with IsMelee: expected + 20 (build 854's meleeVim; a
--     wiki's 40 is another patch's) unless part 1 of this hit already took the melee marker.
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local ok, err = pcall(function()
  local human, humanPid, tact, tactPid, tactRange, tactBasic
  local players = {}
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone then
      players[#players + 1] = {pid = pid, c = c}
      if not human and not nPlayer.IsBot(c) then human, humanPid = c, pid end
    end
  end
  if not human then
    p('rolevim: no human player creature')
    return
  end
  -- the Tactician: a living role-1 player other than the human, preferring a ranged basic (for part 3's ranged hit)
  for _, e in ipairs(players) do
    if e.c ~= human and nPlayer.GetPlayerRole(e.pid) == 1 and nGameObject.IsAlive(e.c) then
      local g = nAbility.GetAbilityGUIDFromSlot(e.c, nAbilitySlot.Basic, nAbilityGUIDLookupType.Current)
      local s = nAttribute.CreateAttributeSnapshot_FromObject(e.c, g)
      local r = nAbility.GetEffectiveRange(e.c, s)
      nAttribute.RemoveAttributeSnapshot(s)
      if not tact or (tactRange <= 351 / 34 and r > 351 / 34) then tact, tactPid, tactRange, tactBasic = e.c, e.pid, r, g end
    end
  end
  dgsim_rolevim_part = (dgsim_rolevim_part or 0) + 1
  local part = dgsim_rolevim_part
  local function gold(pid) return nPlayer.GetPlayerAvailableGold(pid) end
  local function enemyOf(obj) return nGameObject.GetTeam(obj) == nTeamID.Spirit and nTeamID.Mortal or nTeamID.Spirit end
  local function spawnMinion(near, i)
    local tm = enemyOf(near)
    local noun = tm == nTeamID.Mortal and 'Minion_Mortal_Melee.Noun' or 'Minion_Spirit_Melee.Noun'
    local at = nGameObject.GetPosition(near)
    return nObjectManager.CreateObject(nUtil.GetAsset(noun), tm, nVector.create(at.x + 6 + 2 * i, at.y + 4, 0), kZeroVector, 1)
  end
  local function lethal(by, target)
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(by, kObjIDNone)
    local basic = nBit.Or(nBit.Or(nDescriptors.IsMelee, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
    local s, dealt = nGameObject.TakeDamage(snap, target, 5000, 0, basic, kZeroVector)
    nAttribute.RemoveAttributeSnapshot(snap)
    return s, dealt
  end
  local function counter()
    local id = nModifier.GetFirstModifierByGUID(human, nUtil.SPID('modifier_Role_Gladiator_Counter'))
    if id == nil or id == kObjIDNone then return 'none' end
    return tostring(nModifier.GetStackCount(id))
  end
  p(string.format('rolevim part %d: human %s %s pid %s role %s %s, gold %.2f; Tactician %s %s pid %s role %s, basic %s range %s, gold %s',
    part, tostring(human), nGameObject.GetAssetName(human), tostring(humanPid), tostring(nPlayer.GetPlayerRole(humanPid)),
    nGameObject.IsMeleeAttackStyle(human) and 'melee' or 'ranged', gold(humanPid), tostring(tact),
    tact and nGameObject.GetAssetName(tact) or '-', tostring(tactPid), tact and tostring(nPlayer.GetPlayerRole(tactPid)) or '-',
    tostring(tactBasic), f2(tactRange), tact and f2(gold(tactPid)) or '-'))
  if part == 1 then
    local m = {spawnMinion(human, 1), spawnMinion(human, 2), spawnMinion(human, 3)}
    local _, vim = nGameObject.GetRewardForNPC(m[1])
    local g0 = gold(humanPid)
    for i = 1, 2 do nGameObject.KillObject(m[i], kObjIDNone, false) end
    local g1 = gold(humanPid)
    local s, dealt = lethal(human, m[3])
    local g2 = gold(humanPid)
    p(string.format('rolevim 1 Gladiator: vim per minion %s; two deaths by nobody: gold +%.2f (expected %s = 2 x vim), counter %s '
      .. '(expected 2 for a melee shaper); the last hit -> %s dealt %s: gold +%.2f (expected %s = 12 x vim: bonus, role grant, '
      .. 'storage; %s = 3 x vim with role_passives=0), counter %s (expected none)', f2(vim), g1 - g0, f2(2 * vim), counter(),
      tostring(s), f2(dealt), g2 - g1, f2(12 * vim), f2(3 * vim), counter()))
  elseif part == 2 then
    if not tact then p('rolevim 2 Tactician: no living role-1 player') return end
    local m1 = spawnMinion(tact, 1)
    local _, vim = nGameObject.GetRewardForNPC(m1)
    local g0 = gold(tactPid)
    nGameObject.KillObject(m1, kObjIDNone, false)
    local g1 = gold(tactPid)
    local m2 = spawnMinion(tact, 2)
    local s, dealt = lethal(tact, m2)
    local g2 = gold(tactPid)
    p(string.format('rolevim 2 Tactician: vim per minion %s; a death by nobody beside it: gold +%.2f (expected %s = 2 x vim; %s '
      .. 'without); its own last hit -> %s dealt %s: gold +%.2f (expected %s = 3 x vim, with or without)', f2(vim), g1 - g0,
      f2(2 * vim), f2(vim), tostring(s), f2(dealt), g2 - g1, f2(3 * vim)))
  else
    if not tact then p('rolevim 3 Tactician: no living role-1 player') return end
    local victim
    for _, e in ipairs(players) do
      if nGameObject.GetTeam(e.c) ~= nGameObject.GetTeam(tact) and nGameObject.IsAlive(e.c) then victim = e.c break end
    end
    if not victim then p('rolevim 3 Tactician: no living enemy shaper') return end
    local function hit(desc)
      local snap = nAttribute.CreateAttributeSnapshot_FromObject(tact, tactBasic)
      local g = gold(tactPid)
      nGameObject.TakeDamage(snap, victim, 1, 0, desc, kZeroVector)
      nAttribute.RemoveAttributeSnapshot(snap)
      return gold(tactPid) - g
    end
    local ranged = nBit.Or(nBit.Or(nDescriptors.IsProjectile, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
    local melee = nBit.Or(nBit.Or(nDescriptors.IsMelee, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
    local a = hit(ranged)
    local b = hit(ranged)
    local c = hit(melee)
    p(string.format('rolevim 3 Tactician: on enemy shaper %s %s, basic range %s (%s): a hit without IsMelee +%.2f (expected 20), '
      .. 'again at once +%.2f (expected 0), a hit with IsMelee +%.2f (expected %s)', tostring(victim),
      nGameObject.GetAssetName(victim), f2(tactRange), tactRange > 351 / 34 and 'ranged' or 'melee range: both hits melee',
      a, b, c, tactRange > 351 / 34 and '20' or '0, the melee marker already taken'))
  end
end)
if not ok then p('rolevim: ERROR', err) end
return table.concat(out, '\n')
