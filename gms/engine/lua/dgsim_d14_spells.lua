-- D14 live probe: battle spells (docs/MATCH_FLOW.md / SERVER_MODE.md D14; item 15d "Battle spells cast by a human").
-- Two modes, chosen by what the human owns:
--   * REPORT mode (the human owns a spell: run A, the command path from [host] commands_file): every call prints the
--     human's level, alive state, spell count and, per spell slot 7-9 (item slots 6-8), the ability GUID and its
--     cooldown remaining (nAbility.GetCooldownRemaining: the cooldown lives in the creature's ability container, keyed
--     by GUID, so it survives a swap and a death/respawn [code 0x00904E80, 0x00909230]).
--   * EFFECTS mode (no spell owned: run B): each call runs the next part, requesting the spells' abilities by GUID on
--     the human's shaper (as a cast would, minus the command queue), and prints each value beside its expectation.
--     D14a-live (d14a_spells_b2c): a requested spell applies its modifiers and damage on the next scheduler run, not
--     inside RequestAbility, so every request is read in the next part, half a second later:
--       part 1: request Exceed (+30 Power, +30 %, 5 s), Blitz (+0.30 MovementSpeedPercentIncrease 6 s, Unstoppable
--               3/1.5 s), Bastion (+20 armor/MR, shield 60 + 20 x level, 2 s), Tailwind (+0.45 3 s); a Wither slow on
--               the shaper (from a spawned enemy minion's snapshot);
--       part 2 (+0.5 s): their values and times; a 10 true-damage hit absorbed whole by Bastion; request Dispel;
--       part 3 (+0.5 s): the Wither slow cleansed, Dispel_Speed 1 s, Dispel_Tenacity 3 s;
--       part 4 (+2.5 s): Bastion and Dispel_Speed over, Tailwind/Exceed/Blitz on;
--       part 5 (+6.5 s): everything over, Power and move speed back;
--       part 6: request Stasis (ability_Item_Invuln);
--       part 7 (+0.5 s): Untargetable/Immune/Suppressed 1, a 10 true hit deals 0;
--       part 8 (+2.5 s): Stasis over (modifier_Item_Invuln sets no deactivationType: default OnDuration, 2 s, D1e);
--       part 9: the nearest enemy bot (a bot player's creature; not a structure) teleported 10 units east of the
--               human, then Stagger, Wither and Drain requested on it;
--       part 10 (+1 s): the bot's debuffs (Stagger: duration 0.5 + (d - 6.58) / 13.28 x 1.5, x (1 - tenacity);
--               Wither: 3 modifiers 4 s; Drain: DoT 5 x (13.74 + 4.26 x caster level) and Mortal Strike 5 s);
--       part 11: Vanquish (EnemyNPCs, 300 + 30 x level true damage) requested on an enemy minion spawned 8 units away;
--               the target-type check on the nearest creep, enemy worker (allowed), enemy bot and enemy Guardian
--               (refused: player-controlled / structure) [code 0x008FE540, Abilities_TargetMatchesTypeMask];
--       part 12 (+0.5 s): the minion dead;
--       part 13: Blink 12 units east (teleport, z before/after) and 30 units east (out of range: result 5, no teleport
--               from a Lua request; the command path walks up first, shouldPursue).
-- Schedule (run B, practice with bots, d14a_spells_b3c): test_lua=40,40.5,41,43,47,48,48.5,51,52,53,95,95.5,97 (this
-- file each time; parts 9-13 after the camps' spawn at 90 s so the creep check has a creep).
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end
local function pos(o)
  local v = nGameObject.GetPosition(o)
  return string.format('(%.2f, %.2f, %.2f)', v.x, v.y, v.z)
end
local function attr(o, a) return nAttribute.GetAttributeValue(o, a) end
local function has(o, name) return nModifier.AgentHasModifierMatchingGUID(o, nUtil.SPID(name)) end
local function remaining(o, name)
  if not has(o, name) then return 'absent' end
  local m = nModifier.GetFirstModifierByGUID(o, nUtil.SPID(name))
  return m and f2(nModifier.GetRemainingDuration(m)) or '?'
end
local function request(name, caster, target, at)
  return nAbility.RequestAbility(nUtil.SPID(name), caster, target or kObjIDNone, at or kZeroVector, 1, 0,
    nAbilitySlot.None, kObjIDNone, true, false, nil)
end

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)  -- nPlayer.IsBot takes the creature, not the index
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c, pid end
  end
end

-- the nearest living enemy bot's creature (through the player list: a structure has a player level too, so the
-- level test of d14a_spells_b2c picked Binding_Spirit_Outer)
local function nearestEnemyBot(from, team)
  local sp = nGameObject.GetPosition(from)
  local best, bestD = nil, 1e9
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and nPlayer.IsBot(c) and nGameObject.IsAlive(c) and nGameObject.GetTeam(c) ~= team then
      local q = nGameObject.GetPosition(c)
      local d = math.sqrt((q.x - sp.x) ^ 2 + (q.y - sp.y) ^ 2)
      if d < bestD then best, bestD = c, d end
    end
  end
  return best, bestD
end

local function walk(fn)
  local seen = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      fn(id)
    end
  end
end

local function nearest(from, pred)
  local sp = nGameObject.GetPosition(from)
  local best, bestD = nil, 1e9
  walk(function(id)
    if id ~= from and pred(id) then
      local q = nGameObject.GetPosition(id)
      local d = math.sqrt((q.x - sp.x) ^ 2 + (q.y - sp.y) ^ 2)
      if d < bestD then best, bestD = id, d end
    end
  end)
  return best, bestD
end

local function enemyMinionSnapshot(team, at)
  local noun, enemy = 'Minion_Spirit_Melee.Noun', nTeamID.Spirit
  if team == nTeamID.Spirit then noun, enemy = 'Minion_Mortal_Melee.Noun', nTeamID.Mortal end
  local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), enemy, at, kZeroVector, 1)
  if not m then return nil, nil end
  return m, nAttribute.CreateAttributeSnapshot_FromObject(m, kObjIDNone)
end

local function report(shaper, pid)
  local spells = {}
  for slot = 7, 9 do
    local g = nAbility.GetAbilityGUIDFromSlot(shaper, slot, nAbilityGUIDLookupType.Current)
    if g ~= nil and g ~= 0 then
      spells[#spells + 1] = string.format('slot %d %s cd %s', slot, tostring(g), f2(nAbility.GetCooldownRemaining(shaper, g)))
    else
      spells[#spells + 1] = string.format('slot %d empty', slot)
    end
  end
  p(string.format('d14 spells report: match %.1f s, shaper %s level %s alive %s hp %s at %s, spells owned %s; %s; Power %s',
    nGameSimulator.GetMatchTime(), tostring(shaper), tostring(nGameObject.GetPlayerLevel(shaper)),
    tostring(nGameObject.IsAlive(shaper)), f2(nGameObject.GetHitPoints(shaper)), pos(shaper),
    tostring(nPlayer.GetPlayerNumSpellsOwned(pid)), table.concat(spells, ', '), f2(attr(shaper, nAttributeType.Power))))
end

-- D14a-live: by the player's count. GetAbilityGUIDFromSlot returns a GUID value that prints 0x00000000 for an empty
-- slot but isn't the number 0, so the slot test took every human for a spell owner (d14a_spells_b1c ran no part).
local function ownsSpell(shaper, pid)
  local n = nPlayer.GetPlayerNumSpellsOwned(pid)
  return type(n) == 'number' and n > 0
end

local ok, err = pcall(function()
  local shaper, pid = human()
  if not shaper then
    p('d14 spells: no human player creature')
    return
  end
  if ownsSpell(shaper, pid) then
    report(shaper, pid)
    return
  end
  dgsim_d14_spells_part = (dgsim_d14_spells_part or 0) + 1
  local part = dgsim_d14_spells_part
  local team = nGameObject.GetTeam(shaper)
  local level = nGameObject.GetPlayerLevel(shaper)
  local sp = nGameObject.GetPosition(shaper)
  p('d14 spells part', part, ': shaper', tostring(shaper), 'level', tostring(level), 'hp', f2(nGameObject.GetHitPoints(shaper)),
    'at', pos(shaper), 'match', f2(nGameSimulator.GetMatchTime()))
  if part == 1 then
    dgsim_d14_power0 = attr(shaper, nAttributeType.Power)
    dgsim_d14_ms0 = attr(shaper, nAttributeType.MovementSpeedPercentIncrease)
    dgsim_d14_armor0 = attr(shaper, nAttributeType.PhysicalDamageReduction)
    dgsim_d14_mr0 = attr(shaper, nAttributeType.MagicalDamageReduction)
    p('d14 spells 1: before: Power', f2(dgsim_d14_power0), 'MovementSpeedPercentIncrease', f2(dgsim_d14_ms0), 'armor',
      f2(dgsim_d14_armor0), 'MR', f2(dgsim_d14_mr0), '; Exceed ->', tostring(request('ability_Item_Exceed', shaper)),
      'Blitz ->', tostring(request('ability_Item_Blitz', shaper)), 'Bastion ->',
      tostring(request('ability_Item_Bastion', shaper)), 'Tailwind ->', tostring(request('ability_Item_Tailwind', shaper)))
    local m, snap = enemyMinionSnapshot(team, nVector.create(sp.x + 40, sp.y, 0))
    if snap then
      p('d14 spells 1: Wither slow on the shaper ->', tostring(nModifier.RequestModifier(shaper, m, nUtil.SPID('modifier_Item_Wither_Slow'),
        snap, 1, kZeroVector, nil)))
      dgsim_d14_snap_minion = m
      dgsim_d14_snap = snap
    end
  elseif part == 2 then
    p(string.format('d14 spells 2 (+0.5 s): Power %s (expected (%s + 30) x 1.3 = %s); MovementSpeedPercentIncrease %s (expected '
      .. '%s + 0.75: Blitz 0.30 + Tailwind 0.45); armor %s, MR %s (expected %s + 20, %s + 20: Bastion)',
      f2(attr(shaper, nAttributeType.Power)), f2(dgsim_d14_power0), f2((dgsim_d14_power0 + 30) * 1.3),
      f2(attr(shaper, nAttributeType.MovementSpeedPercentIncrease)), f2(dgsim_d14_ms0),
      f2(attr(shaper, nAttributeType.PhysicalDamageReduction)), f2(attr(shaper, nAttributeType.MagicalDamageReduction)),
      f2(dgsim_d14_armor0), f2(dgsim_d14_mr0)))
    p('d14 spells 2: modifiers Exceed', remaining(shaper, 'modifier_Item_Exceed'), '(expected ~4.5) Blitz', remaining(shaper, 'modifier_Item_Blitz'),
      '(~5.5) Unstoppable', remaining(shaper, 'modifier_Item_Blitz_Unstoppable'), '(~2.5 melee / ~1 ranged) Bastion',
      remaining(shaper, 'modifier_Item_Bastion'), '(~1.5) Tailwind', remaining(shaper, 'modifier_Item_Tailwind'), '(~2.5)',
      'Wither slow', remaining(shaper, 'modifier_Item_Wither_Slow'), '(~3.5); MovementSpeedPercentDecrease',
      f2(attr(shaper, nAttributeType.MovementSpeedPercentDecrease)), '(0.4 from the slow)')
    if dgsim_d14_snap then
      local hp = nGameObject.GetHitPoints(shaper)
      local s, dealt, crit, absorbed = nGameObject.TakeDamage(dgsim_d14_snap, shaper, 10, 0, nDescriptors.IsTrueDamage, kZeroVector)
      p(string.format('d14 spells 2: 10 true damage into Bastion (shield %s expected): dealt %s absorbed %s, hp %s -> %s (expected '
        .. 'unchanged)', f2(60 + 20 * level), f2(dealt), f2(absorbed), f2(hp), f2(nGameObject.GetHitPoints(shaper))))
      nAttribute.RemoveAttributeSnapshot(dgsim_d14_snap)
      dgsim_d14_snap = nil
      if dgsim_d14_snap_minion then nGameObject.MarkForDelete(dgsim_d14_snap_minion) end
    end
    p('d14 spells 2: Dispel ->', tostring(request('ability_Item_Dispel', shaper)))
  elseif part == 3 then
    p('d14 spells 3 (+0.5 s after Dispel): Wither slow', remaining(shaper, 'modifier_Item_Wither_Slow'), '(expected absent: cleansed),',
      'Dispel_Speed', remaining(shaper, 'modifier_Item_Dispel_Speed'), '(expected ~0.5 of 1),', 'Dispel_Tenacity',
      remaining(shaper, 'modifier_Item_Dispel_Tenacity'), '(expected ~2.5 of 3); MovementSpeedPercentDecrease',
      f2(attr(shaper, nAttributeType.MovementSpeedPercentDecrease)), '(expected 0)')
  elseif part == 4 then
    p('d14 spells 4 (+3 s): Bastion', remaining(shaper, 'modifier_Item_Bastion'), '(expected absent) Dispel_Speed',
      remaining(shaper, 'modifier_Item_Dispel_Speed'), '(absent) Tailwind', remaining(shaper, 'modifier_Item_Tailwind'), '(absent or ~0) Exceed',
      remaining(shaper, 'modifier_Item_Exceed'), '(~2) Blitz', remaining(shaper, 'modifier_Item_Blitz'), '(~3); armor',
      f2(attr(shaper, nAttributeType.PhysicalDamageReduction)), '(back to', f2(dgsim_d14_armor0), ')')
  elseif part == 5 then
    p(string.format('d14 spells 5 (+7 s): Exceed %s Blitz %s Tailwind %s (expected absent); Power %s (expected %s), '
      .. 'MovementSpeedPercentIncrease %s (expected %s)', remaining(shaper, 'modifier_Item_Exceed'),
      remaining(shaper, 'modifier_Item_Blitz'), remaining(shaper, 'modifier_Item_Tailwind'), f2(attr(shaper, nAttributeType.Power)),
      f2(dgsim_d14_power0), f2(attr(shaper, nAttributeType.MovementSpeedPercentIncrease)), f2(dgsim_d14_ms0)))
  elseif part == 6 then
    p('d14 spells 6: Stasis ->', tostring(request('ability_Item_Invuln', shaper)))
  elseif part == 7 then
    p('d14 spells 7 (+0.5 s): Untargetable', f2(attr(shaper, nAttributeType.Untargetable)), 'Immune',
      f2(attr(shaper, nAttributeType.Immune)), 'Suppressed', f2(attr(shaper, nAttributeType.Suppressed)), '(expected 1 each)',
      'modifier', remaining(shaper, 'modifier_Item_Invuln'), '(expected ~1.5 of 2)')
    local m, snap = enemyMinionSnapshot(team, nVector.create(sp.x + 40, sp.y, 0))
    if snap then
      local s, dealt = nGameObject.TakeDamage(snap, shaper, 10, 0, nDescriptors.IsTrueDamage, kZeroVector)
      p('d14 spells 7: 10 true damage in Stasis: dealt', f2(dealt), '(expected 0: Immune)')
      nAttribute.RemoveAttributeSnapshot(snap)
      nGameObject.MarkForDelete(m)
    end
  elseif part == 8 then
    p('d14 spells 8 (+3 s): Stasis', remaining(shaper, 'modifier_Item_Invuln'), '(expected absent) Untargetable',
      f2(attr(shaper, nAttributeType.Untargetable)), 'Immune', f2(attr(shaper, nAttributeType.Immune)), 'Suppressed',
      f2(attr(shaper, nAttributeType.Suppressed)), '(expected 0 each)')
  elseif part == 9 then
    local bot = nearestEnemyBot(shaper, team)
    if not bot then
      p('d14 spells 9: no living enemy bot')
      return
    end
    dgsim_d14_bot = bot
    nLocomotion.TeleportToPositionAndFacing(bot, nVector.create(sp.x + 10, sp.y, sp.z), nGameObject.GetFacing(bot))
    dgsim_d14_bot_hp = nGameObject.GetHitPoints(bot)
    local d = 10
    p(string.format('d14 spells 9: enemy bot %s %s teleported to 10 east, hp %s; Stagger -> %s, Wither -> %s, Drain -> %s; '
      .. 'expected Stagger slow %.3f s x (1 - tenacity %s)', tostring(bot), nGameObject.GetAssetName(bot) or '?', f2(dgsim_d14_bot_hp),
      tostring(request('ability_Item_Stagger', shaper, bot)), tostring(request('ability_Item_Wither', shaper, bot)),
      tostring(request('ability_Item_Drain', shaper, bot)), 0.5 + (d - 6.58) / (19.86 - 6.58) * 1.5,
      f2(attr(bot, nAttributeType.CrowdControlDurationDecrease))))
  elseif part == 10 then
    local bot = dgsim_d14_bot
    if not bot then return end
    p(string.format('d14 spells 10 (+1 s): on %s: Stagger %s (expected up to ~0.9 - 1 s elapsed: absent or short), Wither_Slow %s, '
      .. 'Wither_DamageDamp %s, Wither_AttackSlow %s (expected ~3 each), Drain %s (~4), MortalStrike %s (~4), HealingReduction %s, '
      .. 'MovementSpeedPercentDecrease %s, PercentDamageDebuff %s (0.3), hp %s -> %s (Stagger 15 + 10 x %d magical, Drain ticks %.2f)',
      tostring(bot), remaining(bot, 'modifier_Item_Stagger'), remaining(bot, 'modifier_Item_Wither_Slow'),
      remaining(bot, 'modifier_Item_Wither_DamageDamp'), remaining(bot, 'modifier_Item_Wither_AttackSlow'), remaining(bot, 'modifier_Item_Drain'),
      remaining(bot, 'modifier_StatusEffect_MortalStrike'), f2(attr(bot, nAttributeType.HealingReduction)),
      f2(attr(bot, nAttributeType.MovementSpeedPercentDecrease)), f2(attr(bot, nAttributeType.PercentDamageDebuff)),
      f2(dgsim_d14_bot_hp), f2(nGameObject.GetHitPoints(bot)), level, 13.736844 + 4.263158 * level))
  elseif part == 11 then
    local vg = nUtil.SPID('ability_Item_Vanquish')
    local function check(what, id, expect)
      if not id then
        p('d14 spells 11:', what, 'none found')
        return
      end
      local q = nGameObject.GetPosition(id)
      p(string.format('d14 spells 11: %s %s %s %.1f units: TargetMatches(EnemyNPCs) %s (expected %s)', what, tostring(id),
        nGameObject.GetAssetName(id) or '?', math.sqrt((q.x - sp.x) ^ 2 + (q.y - sp.y) ^ 2),
        tostring(nAbility.TargetMatchesAbilityTargetTypeMask(shaper, team, id, nTargetType.EnemyNPCs)), expect))
    end
    local noun, enemy = 'Minion_Spirit_Melee.Noun', nTeamID.Spirit
    if team == nTeamID.Spirit then noun, enemy = 'Minion_Mortal_Melee.Noun', nTeamID.Mortal end
    local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), enemy, nVector.create(sp.x + 8, sp.y, 0), kZeroVector, 1)
    if m then
      dgsim_d14_vq_minion = m
      dgsim_d14_vq_hp = nGameObject.GetHitPoints(m)
      check('minion', m, 'true')
      local r1, inst = nAbility.RequestAbility(vg, shaper, m, kZeroVector, 1, 0, nAbilitySlot.None, kObjIDNone, true, false, nil)
      p(string.format('d14 spells 11: Vanquish on the minion -> %s %s (hp %s; expected %s true damage at the next run: dead)',
        tostring(r1), tostring(inst), f2(dgsim_d14_vq_hp), f2(300 + 30 * level)))
    end
    check('creep', (nearest(shaper, function(id) return nGameObject.IsAlive(id) and string.find(nGameObject.GetAssetName(id) or '', '^Creep_Jungle_') ~= nil end)), 'true')
    check('enemy worker', (nearest(shaper, function(id) return nGameObject.IsAlive(id) and nGameObject.GetTeam(id) ~= team
      and string.find(nGameObject.GetAssetName(id) or '', '^Titan_Node_Follower') ~= nil end)), 'true (follower = NPC)')
    check('enemy bot', (nearestEnemyBot(shaper, team)), 'false (player-controlled: EnemyPlayers)')
    check('enemy Guardian', nBattlegrounds.GetGuardianOnTeam(enemy), 'false (structure: EnemyStructures)')
    check('enemy Binding', (nearest(shaper, function(id) return nGameObject.IsAlive(id) and nGameObject.GetTeam(id) == enemy
      and string.find(nGameObject.GetAssetName(id) or '', '^Binding_') ~= nil end)), 'false (structure)')
  elseif part == 12 then
    local m = dgsim_d14_vq_minion
    p('d14 spells 12 (+0.5 s): the Vanquished minion', tostring(m), 'exists', tostring(m and nGameObject.Exists(m)), 'alive',
      tostring(m and nGameObject.Exists(m) and nGameObject.IsAlive(m)), 'hp', m and nGameObject.Exists(m) and
      f2(nGameObject.GetHitPoints(m)) or '-', '(expected dead), Vanquish cooldown',
      f2(nAbility.GetCooldownRemaining(shaper, nUtil.SPID('ability_Item_Vanquish'))), '(expected ~74.5 of 75)')
  elseif part == 13 then
    local before = pos(shaper)
    local r1 = request('ability_Item_Flash', shaper, kObjIDNone, nVector.create(sp.x + 12, sp.y, sp.z))
    p('d14 spells 13: Blink 12 east ->', tostring(r1), 'from', before, 'to', pos(shaper), '(the teleport runs at the next run;'
      .. ' the position in the next line), cooldown', f2(nAbility.GetCooldownRemaining(shaper, nUtil.SPID('ability_Item_Flash'))))
    local here = nGameObject.GetPosition(shaper)
    local r2 = request('ability_Item_Flash', shaper, kObjIDNone, nVector.create(here.x + 30, here.y, here.z))
    p('d14 spells 13: Blink 30 east (out of range 18.39) ->', tostring(r2), '(expected not 1: cooldown -9999 or out of range 5; no teleport)')
  else
    p('d14 spells', part, ': at', pos(shaper), '; Blink cooldown', f2(nAbility.GetCooldownRemaining(shaper, nUtil.SPID('ability_Item_Flash'))),
      '(expected ~300 minus the time since part 13)')
  end
end)
if not ok then p('d14 spells: ERROR', err) end
return table.concat(out, '\n')
