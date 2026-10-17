-- MODCAP-live test probe (the 32-modifier cap; run with [host] test_lua=<s>:dgsim_modcap.lua
-- at each step's time, director=0, bots=0): the public server's burst on a lane. Each run of this file does the next
-- step; the global dgsim_modcap_plan may set {casts = 20, at = {-100, -104}, minions = 14, radius = 3, ability = name,
-- mods = {names}, noun = 'Creep_Jungle_….Noun' (the ring's creature instead of the enemy melee minion), npc = true (it on the
-- NPC team, as a camp's creeps)}; a plan file run before the first step sets it (the camp variant: the warlockKnight,
-- dgsim_modcap_plan = {noun = 'Creep_Jungle_Fungus_Major.Noun', npc = true, ability = 'ability_WarlockKnight_R_Gale'}).
--   run 1          the human player's creature (player 0) teleported to the arena (D11's spot, away from towers); the
--                  modifiers of the burst requested on it with itself as caster: Preservation's passive
--                  (modifier_Item_Legendary_AblativeArmor: 5 _Buff stacks; every hit dealt or taken a Unique 5-s
--                  _HiddenTimer) and the perk modifier_MediumPerk_SpeedWhenNotDamaging (+10 % speed; every hit a Unique
--                  5-s _Wait, the speed modifier ended)
--   runs 2..1+casts  `minions` enemy melee minions kept in a ring of `radius` around the human (dead ones replaced, the
--                  living healed and put back), the human healed and its resource refilled, the ability's cooldown set
--                  to 0, then the ability requested by the human (default ability_Artificer_E_Spin: its Tick hits every
--                  minion in one DamageEnemiesInArea call, so two stale ids per minion per tick)
--   later runs     the minions killed (no more hits), the state only: 5 s after the last hit the speed comes back
-- Every run reports the human's Ablative stacks, its timers' remaining durations, the speed modifier and
-- MovementSpeedPercentIncrease. Every line starts with "modcap <match time>".
dgsim_modcap = dgsim_modcap or {runs = 0, minions = {}}
local M = dgsim_modcap
local plan = dgsim_modcap_plan or {}

local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'modcap ' .. string.format('%.2f', nGameSimulator.GetMatchTime()) .. ' ' .. table.concat(t, ' ')
end
local function f(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end
local function live(id) return id ~= nil and id ~= kObjIDNone and nObjectManager.IsValidObject(id) end
local function hexid(id)
  if id == nil or id == kObjIDNone then return 'none' end
  local s = tostring(id)
  if string.sub(s, 1, 2) == '0x' then s = string.sub(s, 3) end
  return string.upper(s)
end

local kMods = plan.mods or {'modifier_Item_Legendary_AblativeArmor', 'modifier_MediumPerk_SpeedWhenNotDamaging'}
local kWatch = {
  {'buff', 'modifier_Item_Legendary_AblativeArmor_Buff'},
  {'hiddentimer', 'modifier_Item_Legendary_AblativeArmor_HiddenTimer'},
  {'wait', 'modifier_PerkTriggered_SpeedWhenNotDamaging_Wait'},
  {'speed', 'modifier_PerkTriggered_SpeedWhenNotDamaging'},
}

local function state(note)
  local h = M.human
  if not live(h) then
    p('state', note, ': no human')
    return
  end
  local parts = {}
  for _, w in ipairs(kWatch) do
    local m = nModifier.GetFirstModifierByGUID(h, nUtil.SPID(w[2]))
    if m ~= nil and m ~= kObjIDNone then
      parts[#parts + 1] = string.format('%s x%s %ss', w[1], tostring(nModifier.GetStackCount(m)),
        f(nModifier.GetRemainingDuration(m)))
    else
      parts[#parts + 1] = w[1] .. ' -'
    end
  end
  p('state', note, ':', table.concat(parts, ', '), '; MovementSpeedPercentIncrease',
    f(nAttribute.GetAttributeValue(h, nAttributeType.MovementSpeedPercentIncrease)), '; hp',
    f(nGameObject.GetHitPoints(h)), 'of', f(nGameObject.GetMaxHitPoints(h)))
end

local function heal(id)
  if not nGameObject.IsAlive(id) then return end
  local hp, max = nGameObject.GetHitPoints(id), nGameObject.GetMaxHitPoints(id)
  if hp < max then nGameObject.HealDamage(nGameObject.GetAttributeSnapshot(id), id, max - hp + 1, 0, 0, false, false) end
end

local function ring(i, n)
  local r = plan.radius or 3
  local a = 2 * math.pi * (i - 1) / n
  return M.x + r * math.cos(a), M.y + r * math.sin(a)
end

local function minions()
  local n = plan.minions or 14
  local made, kept = 0, 0
  for i = 1, n do
    local id = M.minions[i]
    local x, y = ring(i, n)
    if live(id) and nGameObject.IsAlive(id) then
      heal(id)
      local v = nGameObject.GetPosition(id)
      if (v.x - x) ^ 2 + (v.y - y) ^ 2 > 1.0 then
        nLocomotion.TeleportToPositionAndFacing(id, nVector.create(x, y, v.z), nVector.create(1, 0, 0))
      end
      kept = kept + 1
    else
      local name = plan.noun or (M.enemy == nTeamID.Mortal and 'Minion_Mortal_Melee.Noun' or 'Minion_Spirit_Melee.Noun')
      local team = plan.npc and nTeamID.NPC or M.enemy
      M.minions[i] = nObjectManager.CreateObject(nUtil.GetAsset(name), team, nVector.create(x, y, 0), kZeroVector, 1)
      if live(M.minions[i]) then made = made + 1 end
    end
  end
  return made, kept
end

local function init()
  M.human = nPlayer.GetPlayerControlledObjectID(plan.human or 0)
  if not live(M.human) then
    p('init: no creature for player', plan.human or 0)
    return
  end
  local at = plan.at or {-100.0, -104.0}
  M.x, M.y = at[1], at[2]
  local team = nGameObject.GetTeam(M.human)
  M.enemy = team == nTeamID.Mortal and nTeamID.Spirit or nTeamID.Mortal
  local v = nGameObject.GetPosition(M.human)
  nLocomotion.TeleportToPositionAndFacing(M.human, nVector.create(M.x, M.y, v.z), nVector.create(1, 0, 0))
  p('init: human', hexid(M.human), nGameObject.GetAssetName(M.human), 'team', team, 'level',
    nGameObject.GetPlayerLevel(M.human), 'at', f(nGameObject.GetPosition(M.human).x), f(nGameObject.GetPosition(M.human).y))
  local snap = nGameObject.GetAttributeSnapshot(M.human)
  for _, name in ipairs(kMods) do
    local id = nModifier.RequestModifier(M.human, M.human, nUtil.SPID(name), snap, 1, kZeroVector, nil)
    p('init: request', name, '->', tostring(id), '; active',
      tostring(nModifier.AgentHasModifierMatchingGUID(M.human, nUtil.SPID(name))))
  end
end

local ok, err = pcall(function()
  M.runs = M.runs + 1
  local casts = plan.casts or 20
  if M.runs == 1 then
    init()
    state('after init')
    return
  end
  if not live(M.human) then
    p('run', M.runs, ': no human')
    return
  end
  if M.runs <= 1 + casts then
    local made, kept = minions()
    heal(M.human)
    local max = nGameObject.GetMaxResourcePoints(M.human)
    if max ~= nil and max > 0 then nGameObject.SetResourcePoints(M.human, max) end
    local name = plan.ability or 'ability_Artificer_E_Spin'
    nAbility.SetCooldownRemaining(M.human, 0, nUtil.SPID(name))
    local r1, inst = nAbility.RequestAbility(nUtil.SPID(name), M.human, kObjIDNone, kZeroVector, 1, 0, nAbilitySlot.None,
      kObjIDNone, true, false, nil)
    p('cast', M.runs - 1, 'of', casts, name, '->', tostring(r1), tostring(inst), '; minions made', made, 'kept', kept)
    state('at cast ' .. (M.runs - 1))
    return
  end
  if not M.cleared then
    M.cleared = true
    local n = 0
    for _, id in ipairs(M.minions) do
      if live(id) then
        nGameObject.KillObject(id)
        n = n + 1
      end
    end
    p("cleared", n, "minions (killed: no more hits)")
  end
  state('after the casts, run ' .. M.runs)
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
