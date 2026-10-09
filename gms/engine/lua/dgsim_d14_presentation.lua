-- D14 live probe, presentation (docs/GMS_PROTOCOL.md *Fog details and presentation (D14)*). [host] test_lua with this
-- file at fifteen times, each call running the next part (global dgsim_d14_pres_part); the recipe's schedule:
--   88, 92, 95, 100, 101.5, 103, 104.5, 110, 135, 160, 185, 210, 240, 244, 252 s after EnterGame, a practice game
--   with its bots (bots on: the enemy health bar and the minimap need them), -ShotFocus on, shots around each part.
-- Parts:
--   1  (88 s)   the human's shaper onto the small Fish camp (65.7, -132.2; dgsim_d14_pres_camp = {x, y}), which spawns
--               at 90.00 s (MATCH_FLOW.md T8): expected no creature there yet;
--   2  (92 s)   the creatures within 12 of the camp (expected 3, visible to Mortal: no flicker, none missing) and the
--               creature nearest the Spirit-side Fish camp (-65.0, 130.7) (expected visible false: created, then 93);
--   3  (95 s)   the nearest living Spirit bot shaper teleported 12 units east of the human: its HP (expected full,
--               untouched) and visibility (expected true from the next fog step): its health bar must show at once;
--   4  (100 s)  the human into the grass patch beside the Mortal top base (-112, -100; D2g-rest's 0x16982688), an
--               immobilized Spirit melee minion at (-100, -98), a 10-point basic hit on it with the human's snapshot:
--               the damage hook puts the shaper on its aggro list (expected IsTargetOnAggroList true right now);
--   5  (101.5 s) expected the shaper dropped from the minion's list (hidden by the grass: the host's blackboard update,
--               reason "invisible"), IsInGrass true, visible to Spirit false;
--   6  (103 s)  the human out of the grass, 6 units west of the minion;
--   7  (104.5 s) expected the shaper on the minion's list again (visible, within its aggroRange 20); the minion deleted;
--   8-12 (110, 135, 160, 185, 210 s) the human killed by, in turn, a Spirit melee minion, Binding_Spirit_Outer, a
--               jungle creep, GuardianSpirit_v5 and the parasite (a true hit of its HP + 5000 with the killer's
--               snapshot through the host's damage core): the client's CombatEvent_KillingBlow picks the announcer by
--               the killer's NPC type (MinionKilledYou "A Minion has slain ..." for minion, creep and parasite;
--               TowerKilledYou for the Binding; TitanKilledYou for the Guardian); the death recap names the source;
--               the death animation is the noun's one death state whoever killed (nGameObject.SetAnimationStateToDeath);
--   13 (240 s)  the minimap: the first allied (Mortal) bot killed by a Spirit minion's snapshot (its icon becomes the
--               skull, Shaper_Dead, for 10 s), the second allied bot recalls (ability_returntobase: the spinner icon,
--               Shaper_Returning, while modifier_returntobase lasts), the nearest Spirit bot teleported 12 east of the
--               human (its icon shown while visible);
--   14 (244 s)  the states again (screenshot time);
--   15 (252 s)  that Spirit bot teleported to its start point (fogged: expected its icon gone); the dead ally's skull
--               gone after its 10 s.
-- Returns a report; every line starts with "d14 pres".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'd14 pres ' .. table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f, %.1f)', v.x, v.y) end
local function dist(a, b) return math.sqrt((a.x - b.x) ^ 2 + (a.y - b.y) ^ 2) end

local function each(fn)
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

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    -- nPlayer.IsBot takes an object (0x00947660), not a player id
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return pid, c end
  end
end

-- the bots' creatures of a team, alive first
local function bots(team)
  local list = {}
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and nPlayer.IsBot(c) and nGameObject.GetTeam(c) == team then
      list[#list + 1] = {pid = pid, c = c}
    end
  end
  return list
end

local function named(name)
  local found
  each(function(id) if not found and nGameObject.GetAssetName(id) == name then found = id end end)
  return found
end

local function nearest(prefix, P, maxD)
  local best, bestD = nil, maxD or 1e9
  each(function(id)
    local name = nGameObject.GetAssetName(id) or ''
    if string.find(name, prefix) and nGameObject.IsAlive(id) then
      local d = dist(nGameObject.GetPosition(id), P)
      if d < bestD then best, bestD = id, d end
    end
  end)
  return best, bestD
end

local function hit(from, to, amount, desc)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(from, kObjIDNone)
  local ok2, dealt = nGameObject.TakeDamage(snap, to, amount, 0, desc or nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  return ok2, dealt
end

local function teleport(o, pos)
  nLocomotion.TeleportToPositionAndFacing(o, pos, nVector.create(1, 0, 0))
end

local function state(id, what)
  if id == nil or id == kObjIDNone then
    p(what, 'none')
    return
  end
  p(what, tostring(id), nGameObject.GetAssetName(id), 'at', vec(nGameObject.GetPosition(id)), 'hp',
    f1(nGameObject.GetHitPoints(id)) .. '/' .. f1(nGameObject.GetMaxHitPoints(id)), 'alive', tostring(nGameObject.IsAlive(id)),
    'visible to Mortal', tostring(nGameObject.IsVisibleToTeam(id, nTeamID.Mortal)), 'to Spirit',
    tostring(nGameObject.IsVisibleToTeam(id, nTeamID.Spirit)))
end

local function spawnSpiritMinion(pos)
  local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Spirit_Melee.Noun'), nTeamID.Spirit, pos, kZeroVector, 1)
  if m == kObjIDNone then return nil end
  return m
end

local killers = {
  {'a Spirit melee minion', 'MinionKilledYou (death_npc)'},
  {'Binding_Spirit_Outer', 'TowerKilledYou (death_structure)'},
  {'a jungle creep', 'MinionKilledYou (death_npc: the client has no creep line)'},
  {'GuardianSpirit_v5', 'TitanKilledYou (death_structure)'},
  {'the parasite', 'MinionKilledYou (death_npc)'},
}

local ok, err = pcall(function()
  local pid, shaper = human()
  if not shaper then
    p('no human player creature')
    return
  end
  dgsim_d14_pres_part = (dgsim_d14_pres_part or 0) + 1
  local part = dgsim_d14_pres_part
  p('part', part, 'match time', f1(nGameSimulator.GetMatchTime()), 'shaper', tostring(shaper), 'at',
    vec(nGameObject.GetPosition(shaper)), 'hp', f1(nGameObject.GetHitPoints(shaper)))
  local campXY = dgsim_d14_pres_camp or {65.7, -132.2}
  local camp = nVector.create(campXY[1], campXY[2], 0)
  if part == 1 then
    teleport(shaper, camp)
    local n = 0
    each(function(id)
      local name = nGameObject.GetAssetName(id) or ''
      if string.find(name, '^Creep_Jungle_') and dist(nGameObject.GetPosition(id), camp) <= 12 then n = n + 1 end
    end)
    p('shaper onto the camp', vec(camp), '; creatures there now', n, '(expected 0; they spawn at 90.00 s)')
  elseif part == 2 then
    local n = 0
    each(function(id)
      local name = nGameObject.GetAssetName(id) or ''
      if string.find(name, '^Creep_Jungle_') and dist(nGameObject.GetPosition(id), camp) <= 12 then
        n = n + 1
        state(id, '  camp creature (expected visible to Mortal true):')
      end
    end)
    p('creatures at the camp', n, '(expected 3: 2 Fish minor + 1 Fish medium)')
    local far = nearest('^Creep_Jungle_', nVector.create(-65.0, 130.7, 0), 15)
    state(far, 'far camp creature (expected visible to Mortal false: its create was followed by 93):')
    -- the buff camp's major: where it stands against the camp's scale-2.0 child marker (the tutorial's buff ping,
    -- tutorialUtils.posGreenBuff), and its BodyScale (the object's scale = create scale 1.0 + BodyScale); then the
    -- shaper onto the Green buff camp for the size screenshots
    local buff = nVector.create(68.0, -165.6, 0)
    local mark = nVector.create(73.96, -162.46, 0)
    each(function(id)
      local name = nGameObject.GetAssetName(id) or ''
      if string.find(name, '^Creep_Jungle_Fish_') and dist(nGameObject.GetPosition(id), buff) <= 12 then
        p('  Green buff camp:', tostring(id), name, 'at', vec(nGameObject.GetPosition(id)), f1(dist(nGameObject.GetPosition(id), mark)),
          'from the scale-2.0 marker; BodyScale', f1(nAttribute.GetAttributeValue(id, nAttributeType.BodyScale)))
      end
    end)
    teleport(shaper, nVector.create(buff.x - 8, buff.y, 0))
    p('shaper 8 west of the Green buff camp (screenshots: the major\'s size against the minors)')
  elseif part == 3 then
    local sp = nGameObject.GetPosition(shaper)
    local best, bestD = nil, 1e9
    for _, b in ipairs(bots(nTeamID.Spirit)) do
      if nGameObject.IsAlive(b.c) then
        local d = dist(nGameObject.GetPosition(b.c), sp)
        if d < bestD then best, bestD = b.c, d end
      end
    end
    if not best then
      p('no living Spirit bot')
      return
    end
    dgsim_d14_pres_enemy = best
    teleport(best, nVector.create(sp.x + 12, sp.y, 0))
    state(best, 'Spirit bot teleported 12 east of the human (expected hp full, visible to Mortal from the next fog step; '
      .. 'its health bar on screen before any damage):')
  elseif part == 4 then
    teleport(shaper, nVector.create(-112, -100, 0))
    local m = spawnSpiritMinion(nVector.create(-100, -98, 0))
    if not m then
      p('Spirit minion not created')
      return
    end
    dgsim_d14_pres_minion = m
    nAttribute.AddAttributeModifier(m, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
    local a, d = hit(shaper, m, 10, nDescriptors.IsBasic)
    p('human into the grass (-112, -100); Spirit minion', tostring(m), 'at', vec(nGameObject.GetPosition(m)), '; basic hit ->',
      tostring(a), f1(d), '; shaper on its aggro list now:', tostring(nAgent.IsTargetOnAggroList(m, shaper)),
      '(expected true: the damage hook)')
  elseif part == 5 then
    local m = dgsim_d14_pres_minion
    if not m then return end
    p('shaper in grass', tostring(nGameObject.IsInGrass(shaper)), '(expected true); visible to Spirit',
      tostring(nGameObject.IsVisibleToTeam(shaper, nTeamID.Spirit)), '(expected false); on the minion\'s aggro list',
      tostring(nAgent.IsTargetOnAggroList(m, shaper)), '(expected false: dropped as invisible); minion best target',
      tostring(nAgent.GetBestTarget(m)))
  elseif part == 6 then
    local m = dgsim_d14_pres_minion
    if not m then return end
    local mp = nGameObject.GetPosition(m)
    teleport(shaper, nVector.create(mp.x - 6, mp.y, 0))
    p('human out of the grass, 6 west of the minion at', vec(mp))
  elseif part == 7 then
    local m = dgsim_d14_pres_minion
    if not m then return end
    p('shaper in grass', tostring(nGameObject.IsInGrass(shaper)), '(expected false); visible to Spirit',
      tostring(nGameObject.IsVisibleToTeam(shaper, nTeamID.Spirit)), '(expected true); on the minion\'s aggro list',
      tostring(nAgent.IsTargetOnAggroList(m, shaper)), '(expected true: perceived within aggroRange 20); best target',
      tostring(nAgent.GetBestTarget(m)), '(base structures come first for a minion)')
    nGameObject.MarkForDelete(m)
    dgsim_d14_pres_minion = nil
  elseif part >= 8 and part <= 12 then
    local k = part - 7
    if dgsim_d14_pres_killer_minion and nGameObject.IsAlive(dgsim_d14_pres_killer_minion) then
      nGameObject.MarkForDelete(dgsim_d14_pres_killer_minion)
      dgsim_d14_pres_killer_minion = nil
    end
    if not nGameObject.IsAlive(shaper) then
      p('death', k, 'by', killers[k][1], ': the shaper is dead (respawning); skipped')
      return
    end
    local killer
    local sp = nGameObject.GetPosition(shaper)
    if k == 1 then
      killer = spawnSpiritMinion(nVector.create(sp.x + 15, sp.y, 0))
      dgsim_d14_pres_killer_minion = killer
    elseif k == 2 then
      killer = named('Binding_Spirit_Outer')
    elseif k == 3 then
      killer = nearest('^Creep_Jungle_', sp)
    elseif k == 4 then
      killer = named('GuardianSpirit_v5')
    else
      killer = nDirector.GetParasiteID()
    end
    if not killer or killer == kObjIDNone then
      p('death', k, 'by', killers[k][1], ': no killer object')
      return
    end
    local a, d = hit(killer, shaper, nGameObject.GetHitPoints(shaper) + 5000)
    p('death', k, 'by', killers[k][1], tostring(killer), nGameObject.GetAssetName(killer), 'NPC type',
      tostring(nGameObject.GetNPCType(killer)), ': hit ->', tostring(a), f1(d), '; alive', tostring(nGameObject.IsAlive(shaper)),
      '; expected announcer', killers[k][2], ', the recap\'s source this noun and NPC type, the noun\'s death animation')
  elseif part == 13 then
    local allies = bots(nTeamID.Mortal)
    p('allied bots', #allies)
    local m = spawnSpiritMinion(nVector.create(150, 150, 0))
    if allies[1] and m then
      local a, d = hit(m, allies[1].c, nGameObject.GetHitPoints(allies[1].c) + 5000)
      dgsim_d14_pres_dead_ally = allies[1].c
      p('allied bot', tostring(allies[1].c), nGameObject.GetAssetName(allies[1].c), 'killed ->', tostring(a), f1(d),
        '(expected the minimap skull for 10 s)')
    end
    if m then dgsim_d14_pres_killer_minion = m end
    if allies[2] and nGameObject.IsAlive(allies[2].c) then
      local r1, inst = nAbility.RequestAbility(nUtil.SPID('ability_returntobase'), allies[2].c, kObjIDNone, kZeroVector, 1, 0,
        nAbilitySlot.None, kObjIDNone, true, false, nil)
      p('allied bot', tostring(allies[2].c), nGameObject.GetAssetName(allies[2].c), 'recall ->', tostring(r1), tostring(inst),
        '(expected the minimap spinner while modifier_returntobase lasts)')
    end
    local sp = nGameObject.GetPosition(shaper)
    local enemy
    for _, b in ipairs(bots(nTeamID.Spirit)) do
      if not enemy and nGameObject.IsAlive(b.c) then enemy = b end
    end
    if enemy then
      dgsim_d14_pres_enemy_bot = enemy
      teleport(enemy.c, nVector.create(sp.x + 12, sp.y, 0))
      state(enemy.c, 'Spirit bot 12 east of the human (expected its minimap icon and health bar):')
    end
  elseif part == 14 then
    state(dgsim_d14_pres_dead_ally, 'dead ally:')
    if dgsim_d14_pres_enemy_bot then state(dgsim_d14_pres_enemy_bot.c, 'Spirit bot:') end
  else
    local e = dgsim_d14_pres_enemy_bot
    if e and nGameObject.IsAlive(e.c) then
      local pos, orient = nGameSimulator.GetPlayerStartingPoint(nPlayer.GetPlayerIdForObject(e.c) or e.pid)
      nLocomotion.TeleportToPositionAndOrientation(e.c, pos, orient)
      state(e.c, 'Spirit bot at its start point (expected visible to Mortal false: no minimap icon):')
      -- a recall in fog: its B2B_teleport_effect is an attached effect (A4 kind 2, to the bot's seers only), its
      -- PlayerStartedReturnToBase goes to the Spirit team: the human must neither see nor hear it (attended check)
      local r1, inst = nAbility.RequestAbility(nUtil.SPID('ability_returntobase'), e.c, kObjIDNone, kZeroVector, 1, 0,
        nAbilitySlot.None, kObjIDNone, true, false, nil)
      p('fogged Spirit bot recalls ->', tostring(r1), tostring(inst), '(expected: no recall sound or effect for the human)')
    end
    state(dgsim_d14_pres_dead_ally, 'dead ally (12 s after its death: expected no skull any more):')
    if dgsim_d14_pres_killer_minion and nGameObject.IsAlive(dgsim_d14_pres_killer_minion) then
      nGameObject.MarkForDelete(dgsim_d14_pres_killer_minion)
    end
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
