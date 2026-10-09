-- D14 live probe: spirit wells under attack (docs/MATCH_FLOW.md *D14*, "spirit wells under attack"). Each call runs the
-- next part of one of two modes, chosen at the first call:
--   * mode 'workers' (the human's creature is far from its own wells at the first call; run d14w_run1: bots=0,
--     director_waves=0, the wells locked as usual): the Spirit well at (182.5, -27.9) and its workers
--     (Titan_Node_Follower: aggro range 0, call for help 6, tether 15 around its spawn spot, ability_Worker_BasicAttack_Melee
--     3 + 0.8/level every ~2.5 s, 90 + 10/level HP, armor/MR 0 + 1/level, no passive, no regen):
--       part 1: teleport the human onto the first worker's mining line, 2 units in from its spot (else (175, -52) beside
--               the south-west spots), census of the well's workers;
--       part 2: one non-lethal hit (20 physical basic, the human's snapshot) on the nearest worker A: expected A's HP down,
--               the human on A's list (the host's damage hook) and on the lists of A's workers within 6 (call for help);
--       part 3 (+1.5 s): A engaged (IsInCombat true, best target the human), off its mining line (pursuing within 15 of its
--               spot), the human hurt by the slaps;
--       part 4: the human teleported to its start point (beyond every tether): expected the workers drop it at their next
--               update and walk back to their return-to-work point;
--       part 5 (+7 s): A not in combat, list empty, back near its return-to-work point (<= 1 unit, then mining), its HP
--               unchanged since the fight (no regeneration: no passive, base regen 0); then (D14a-live) the human back
--               beside the spots, so its client knows the workers before part 6 (d14a_wells1c: B's ObjectJump went to
--               nobody when the teleport and the knock-up came in one call);
--       part 6: modifier_Desecrator_Q_Knockup (from the human) on an idle worker B and Immobilized +1 on an idle worker C,
--               positions recorded;
--       part 7 (run it 5.5 s after part 6): B and C positions again (inferred offline: both kept moving along their
--               mining line, the type-3 loop reading neither jumps nor Immobilized; d14a_wells1c's client showed the rooted
--               C frozen and then 17 units ahead); C's root removed;
--       part 8 (D14a-live, the far kill): the worker nearest to the well's centre is rooted, the human teleported 17.0
--               units from it (inside Vanquish's 17.65, outside the host's assumed Worker reward radius 15.79) and
--               Vanquish requested on it from here (Lua; the commands file's later Vanquish is then on cooldown);
--       part 9 (+3 s): that worker dead, the human's gold and XP change (Worker reward: area gold 6 + 0.1/level and area
--               XP 15 + 1/level to players within the radius; the host's death line says "area N"), the count one lower;
--       part 10: census again (a freed slot refilled one period, 15 s, after the death).
--     The commands file stops the human right after part 2's hit and after part 6 (the server's auto-acquire takes an
--     engaged worker: in d14a_wells1c the human killed A within 3 s).
--   * mode 'capture' (the human stands within 40 units of one of its own wells at the first call; run d14w_run2: bots on,
--     wells_lock_first=45, the human parked 22 units south of the Mortal well (28.2, -182.6)): an enemy capture seen
--     from the owner's side:
--       part C1: a Spirit bot teleported 3 units from that well's centre and rooted (Immobilized +1): expected
--               "wells: well N ... capture started ... against team 1", MineUnderAttack to team 1, the capture after 16 s
--               unless the bot takes damage (the owner's workers never attack it on sight: aggro range 0);
--       part C2 (+3 s): 10 physical from that bot's snapshot on the nearest own worker: expected MineUnderAttack on the
--               human's client with the well as its object (the client raises it itself, CombatEvent_DamageAbsorb), the
--               worker engaging the bot and dropping it again (tether 15 around its spot, the bot >= 19 units from it);
--       part C3 (+20 s): the root removed; expected the well owned by team 2, its workers destroyed, locked 240 s.
-- Returns a report (logged by the host after "host: test_lua dgsim_d14_wells.lua at <s> s after EnterGame").
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local SPIRIT_WELL = nVector.create(182.5, -27.9, 0)
local MORTAL_WELLS = {nVector.create(28.2, -182.6, 0), nVector.create(-182.3, 27.9, 0)}
local SPIRIT_WELLS = {SPIRIT_WELL, nVector.create(-27.6, 182.7, 0)}
local NEAR_SPOTS = nVector.create(175.0, -52.0, 0)

local function dist(a, b)
  local dx, dy = a.x - b.x, a.y - b.y
  return math.sqrt(dx * dx + dy * dy)
end
local function xy(v) return string.format('(%.1f, %.1f)', v.x, v.y) end

-- nPlayer.IsBot takes the player's creature (nPlayer_IsBot 0x00947660 -> SporeLabsLua_GetObjectArg), not its index
local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c, pid end
  end
end

local function bots(team)
  local list = {}
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and nPlayer.IsBot(c) and nGameObject.GetTeam(c) == team and nGameObject.IsAlive(c) then
      list[#list + 1] = c
    end
  end
  return list
end

-- every living worker (Titan_Node_Follower) within `radius` of `centre`
local function workers(centre, radius)
  local list, seen = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      if string.find(name, '^Titan_Node_Follower') and nGameObject.IsAlive(id) then
        if dist(nGameObject.GetPosition(id), centre) <= radius then list[#list + 1] = id end
      end
    end
  end
  return list
end

local function nearest(list, from, skip)
  local best, bd
  for _, id in ipairs(list) do
    if not (skip and skip[tostring(id)]) then
      local d = dist(nGameObject.GetPosition(id), from)
      if bd == nil or d < bd then best, bd = id, d end
    end
  end
  return best, bd
end

local function aggro(id)
  local ok, targets = pcall(nAgent.GetTargetsOnAggroList, id)
  local s = {}
  if ok and type(targets) == 'table' then for _, t in ipairs(targets) do s[#s + 1] = tostring(t) end end
  return '[' .. table.concat(s, ' ') .. ']'
end

local function describe(id, tag)
  if id == nil or not nGameObject.Exists(id) then
    p('d14 wells:', tag, tostring(id), 'GONE')
    return
  end
  local pos = nGameObject.GetPosition(id)
  local okRet, hasRet, ret = pcall(nLocomotion.GetFollowerMiningReturnToWorkPoint, id)
  local retTxt = (okRet and hasRet and ret) and string.format('%s, %.1f away', xy(ret), dist(pos, ret)) or 'none'
  p(string.format('d14 wells: %s %s team %s owner %s alive %s hp %s/%s level %s armor %s mr %s at %s return-to-work %s '
    .. 'in combat %s best %s list %s', tag, tostring(id), tostring(nGameObject.GetTeam(id)),
    tostring(nGameObject.GetOwnerID(id)), tostring(nGameObject.IsAlive(id)), f2(nGameObject.GetHitPoints(id)),
    f2(nGameObject.GetMaxHitPoints(id)), tostring(nGameObject.GetNPCLevel(id)),
    f2(nAttribute.GetAttributeValue(id, nAttributeType.PhysicalDamageReduction)),
    f2(nAttribute.GetAttributeValue(id, nAttributeType.MagicalDamageReduction)), xy(pos), retTxt,
    tostring(nGameObject.IsInCombat(id)), tostring(nAgent.GetBestTarget(id)), aggro(id)))
end

local function census(centre, tag)
  local list = workers(centre, 40)
  p('d14 wells:', tag, '-', #list, 'living worker(s) within 40 of', xy(centre), '; GetFollowersForTitan count n/a here',
    '; match time', f2(nGameSimulator.GetMatchTime()))
  for _, id in ipairs(list) do describe(id, '  worker') end
  return list
end

local function teleport(o, pos)
  return nLocomotion.TeleportToPositionAndFacing(o, pos, nVector.create(0, 1, 0))
end

local ok, err = pcall(function()
  local me, pid = human()
  if not me then
    p('d14 wells: no human player creature')
    return
  end
  if dgsim_d14_wells_mode == nil then
    local here = nGameObject.GetPosition(me)
    local own = nGameObject.GetTeam(me) == 2 and SPIRIT_WELLS or MORTAL_WELLS
    dgsim_d14_wells_mode = 'workers'
    for _, w in ipairs(own) do
      if dist(here, w) <= 40 then
        dgsim_d14_wells_mode = 'capture'
        dgsim_d14_wells_own = w
      end
    end
    dgsim_d14_wells_part = 0
    p('d14 wells: mode', dgsim_d14_wells_mode, 'human', tostring(me), 'team', nGameObject.GetTeam(me), 'at', xy(here))
  end
  dgsim_d14_wells_part = dgsim_d14_wells_part + 1
  local part = dgsim_d14_wells_part
  p('d14 wells: mode', dgsim_d14_wells_mode, 'part', part, 'at match time', f2(nGameSimulator.GetMatchTime()), 'human hp',
    f2(nGameObject.GetHitPoints(me)), 'at', xy(nGameObject.GetPosition(me)), 'gold', f2(nPlayer.GetPlayerAvailableGold(pid)))

  if dgsim_d14_wells_mode == 'capture' then
    local well = dgsim_d14_wells_own
    if part == 1 then
      local list = bots(nGameObject.GetTeam(me) == 2 and 1 or 2)
      if #list == 0 then
        p('d14 wells C1: no living enemy bot (bots=0?): nothing to do')
        return
      end
      local b = list[1]
      local spot = nVector.create(well.x + 3, well.y, 0)
      local okT = teleport(b, spot)
      dgsim_d14_wells_bot = b
      dgsim_d14_wells_root = nAttribute.AddAttributeModifier(b, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
      p('d14 wells C1: enemy bot', tostring(b), 'teleported to', xy(spot), '->', tostring(okT), 'now at',
        xy(nGameObject.GetPosition(b)), 'rooted', tostring(dgsim_d14_wells_root), '; the well', xy(well), 'is',
        f2(dist(nGameObject.GetPosition(b), well)), 'away (capture sphere 12); human', f2(dist(nGameObject.GetPosition(me), well)),
        'away (expected > 12: no contest)')
      census(well, 'C1 own well')
      p('d14 wells C1: expected in dgsim.log: "capture started" against the human\'s team, MineUnderAttack to it, CAPTURE '
        .. '16 s later (if the bot takes no damage), workers destroyed, locked 240 s; screenshots: the ring filling in the '
        .. 'enemy colour from the owner\'s side, MineCaptured as the owner reads it')
    elseif part == 2 then
      -- an enemy hit on one of the human's own workers: the client's CombatEvent_DamageAbsorb raises MineUnderAttack for
      -- it with nGameObject.GetOwnerID(worker) as the object (needs the worker created with its well as owner)
      local b = dgsim_d14_wells_bot
      local list = workers(well, 40)
      local w = b and nearest(list, nGameObject.GetPosition(b))
      if not w then
        p('d14 wells C2: no enemy bot or no own worker')
        return
      end
      local snap = nAttribute.CreateAttributeSnapshot_FromObject(b, kObjIDNone)
      local before = nGameObject.GetHitPoints(w)
      local s, dealt = nGameObject.TakeDamage(snap, w, 10, 0, nBit.Or(nDescriptors.IsBasic, nDescriptors.IsPhysicalDamage),
        kZeroVector)
      nAttribute.RemoveAttributeSnapshot(snap)
      p(string.format('d14 wells C2: 10 physical from the enemy bot %s on own worker %s (owner %s, the well at %s): hp %s -> %s; '
        .. 'expected on the human\'s client: MineUnderAttack with the well as its object (game_peek.py feedback cooldowns; '
        .. 'no ClientFeedback.cpp assert); the worker engages the bot but drops it (the bot is >= 19 units from the worker\'s '
        .. 'spot, tether 15)', tostring(b), tostring(w), tostring(nGameObject.GetOwnerID(w)), xy(well), f2(before),
        f2(nGameObject.GetHitPoints(w))))
      describe(w, 'C2 hit worker')
    else
      local b = dgsim_d14_wells_bot
      if b and dgsim_d14_wells_root then
        nAttribute.RemoveAttributeModifier(b, dgsim_d14_wells_root)
        dgsim_d14_wells_root = nil
      end
      p('d14 wells C' .. part .. ': bot', tostring(b), 'at', b and xy(nGameObject.GetPosition(b)) or '-', 'alive',
        tostring(b and nGameObject.IsAlive(b)), 'hp', b and f2(nGameObject.GetHitPoints(b)) or '-', '; root removed')
      census(well, 'C' .. part .. ' own well (expected 0 workers right after an enemy capture)')
    end
    return
  end

  -- mode 'workers'
  if part == 1 then
    -- next to the first worker's spot, 2 units in from it on its mining line (walkable, 20 units from the well's
    -- centre: outside the 12-unit capture sphere); (175, -52), beside the south-west spots, without a worker
    local first = workers(SPIRIT_WELL, 40)[1]
    local okRet, hasRet, ret = false, false, nil
    if first then okRet, hasRet, ret = pcall(nLocomotion.GetFollowerMiningReturnToWorkPoint, first) end
    if okRet and hasRet and ret and dist(ret, SPIRIT_WELL) > 6 then
      local d = dist(ret, SPIRIT_WELL)
      dgsim_d14_wells_near = nVector.create(ret.x + (SPIRIT_WELL.x - ret.x) * 2 / d, ret.y + (SPIRIT_WELL.y - ret.y) * 2 / d, 0)
    else
      dgsim_d14_wells_near = NEAR_SPOTS
    end
    p('d14 wells 1: teleport to', xy(dgsim_d14_wells_near), '->', tostring(teleport(me, dgsim_d14_wells_near)), 'now at',
      xy(nGameObject.GetPosition(me)), '(first worker', tostring(first), ')')
    census(SPIRIT_WELL, 'part 1 census')
  elseif part == 2 then
    local list = census(SPIRIT_WELL, 'part 2 census')
    local a = nearest(list, nGameObject.GetPosition(me))
    if not a then
      p('d14 wells 2: no worker yet')
      return
    end
    dgsim_d14_wells_a = a
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(me, kObjIDNone)
    local before = nGameObject.GetHitPoints(a)
    local s, dealt, crit, absorbed = nGameObject.TakeDamage(snap, a, 20, 0, nBit.Or(nDescriptors.IsBasic,
      nDescriptors.IsPhysicalDamage), kZeroVector)
    nAttribute.RemoveAttributeSnapshot(snap)
    p(string.format('d14 wells 2: 20 physical basic on worker A %s at %.1f units: hp %s -> %s (dealt %s, absorbed %s; '
      .. 'expected 20 x 100/(100 + armor)), list right after the hit %s (expected the human: the damage hook)', tostring(a),
      dist(nGameObject.GetPosition(a), nGameObject.GetPosition(me)), f2(before), f2(nGameObject.GetHitPoints(a)), f2(dealt),
      f2(absorbed), aggro(a)))
    for _, w in ipairs(workers(nGameObject.GetPosition(a), 6)) do
      if w ~= a then p('d14 wells 2: helper within 6 of A:', tostring(w), 'list', aggro(w), '(expected the human: call for help)') end
    end
  elseif part == 3 then
    local a = dgsim_d14_wells_a
    describe(a, 'part 3 A (expected in combat, best = human, pursuing within 15 of its spot)')
    if a and nGameObject.Exists(a) then
      p('d14 wells 3: A is', f2(dist(nGameObject.GetPosition(a), nGameObject.GetPosition(me))), 'units from the human')
      for _, w in ipairs(workers(nGameObject.GetPosition(a), 10)) do
        if w ~= a then describe(w, 'part 3 neighbour') end
      end
    end
  elseif part == 4 then
    local spawn = nGameSimulator.GetPlayerStartingPoint(pid)
    p('d14 wells 4: teleport to the start point', spawn and xy(spawn) or '?', '->', tostring(spawn and teleport(me, spawn)),
      '(expected: every worker drops the human at its next update and walks back to its loop)')
    describe(dgsim_d14_wells_a, 'part 4 A')
  elseif part == 5 then
    describe(dgsim_d14_wells_a, 'part 5 A (expected not in combat, list [], <= 1 unit from its return-to-work point or mining '
      .. 'again, hp as after the fight: no regen)')
    p('d14 wells 5: the human back beside the spots ->', tostring(teleport(me, dgsim_d14_wells_near or NEAR_SPOTS)))
  elseif part == 6 then
    local list = workers(SPIRIT_WELL, 40)
    local skip = {}
    if dgsim_d14_wells_a then skip[tostring(dgsim_d14_wells_a)] = true end
    for _, w in ipairs(list) do if nGameObject.IsInCombat(w) then skip[tostring(w)] = true end end
    local b = nearest(list, nGameObject.GetPosition(me), skip)
    if b then skip[tostring(b)] = true end
    local c = nearest(list, nGameObject.GetPosition(me), skip)
    dgsim_d14_wells_b, dgsim_d14_wells_c = b, c
    if b then
      dgsim_d14_wells_bpos = nGameObject.GetPosition(b)
      local snap = nAttribute.CreateAttributeSnapshot_FromObject(me, kObjIDNone)
      local m = nModifier.RequestModifier(b, me, nUtil.SPID('modifier_Desecrator_Q_Knockup'), snap, 1, kZeroVector, nil)
      nAttribute.RemoveAttributeSnapshot(snap)
      p('d14 wells 6: knock-up (modifier_Desecrator_Q_Knockup, 0.75 s, 5.5 high) on idle worker B', tostring(b), 'at',
        xy(dgsim_d14_wells_bpos), '->', tostring(m))
    end
    if c then
      dgsim_d14_wells_cpos = nGameObject.GetPosition(c)
      dgsim_d14_wells_croot = nAttribute.AddAttributeModifier(c, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
      p('d14 wells 6: Immobilized +1 on idle worker C', tostring(c), 'at', xy(dgsim_d14_wells_cpos), '->',
        tostring(dgsim_d14_wells_croot))
    end
  elseif part == 7 then
    for _, t in ipairs({{dgsim_d14_wells_b, dgsim_d14_wells_bpos, 'B (knocked up)'}, {dgsim_d14_wells_c, dgsim_d14_wells_cpos,
      'C (rooted)'}}) do
      local id, was, tag = t[1], t[2], t[3]
      if id and nGameObject.Exists(id) then
        local now = nGameObject.GetPosition(id)
        p(string.format('d14 wells 7: worker %s %s moved %.2f units since part 6 from %s to %s, z %.2f, in combat %s '
          .. '(inferred offline: moved along its mining line, the type-3 loop ignoring jumps and Immobilized); compare '
          .. 'with the client (game_peek.py)', tag, tostring(id), dist(now, was), xy(was), xy(now), now.z,
          tostring(nGameObject.IsInCombat(id))))
      end
    end
    if dgsim_d14_wells_c and dgsim_d14_wells_croot then
      nAttribute.RemoveAttributeModifier(dgsim_d14_wells_c, dgsim_d14_wells_croot)
      dgsim_d14_wells_croot = nil
    end
  elseif part == 8 then
    -- the far kill: the worker nearest to the well's centre, rooted, the human 17.0 units from it toward where it stands
    local list = workers(SPIRIT_WELL, 40)
    local w = nearest(list, SPIRIT_WELL)
    dgsim_d14_wells_v = w
    dgsim_d14_wells_gold = nPlayer.GetPlayerAvailableGold(pid)
    dgsim_d14_wells_xp = nPlayer.GetPlayerXP and nPlayer.GetPlayerXP(pid) or nil
    dgsim_d14_wells_count = #list
    if not w then
      p('d14 wells 8: no worker')
      return
    end
    nAttribute.AddAttributeModifier(w, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
    local wp, hp = nGameObject.GetPosition(w), nGameObject.GetPosition(me)
    local dx, dy = hp.x - wp.x, hp.y - wp.y
    local dd = math.sqrt(dx * dx + dy * dy)
    if dd < 1 then dx, dy, dd = 0, -1, 1 end
    local at = nVector.create(wp.x + dx / dd * 17.0, wp.y + dy / dd * 17.0, 0)
    local okT = teleport(me, at)
    local d = dist(nGameObject.GetPosition(me), nGameObject.GetPosition(w))
    local r = nAbility.RequestAbility(nUtil.SPID('ability_Item_Vanquish'), me, w, kZeroVector, 1, 0, nAbilitySlot.None,
      kObjIDNone, true, false, nil)
    p(string.format('d14 wells 8: the far kill: worker %s (rooted) at %s, the human teleported -> %s, now %.2f units from it '
      .. '(Vanquish range 17.65; the host pays the Worker reward only within 15.79 [assumed]); Vanquish -> %s; gold %s, '
      .. 'xp %s, workers at the well %d', tostring(w), xy(wp), tostring(okT), d, tostring(r), f2(dgsim_d14_wells_gold),
      tostring(dgsim_d14_wells_xp), dgsim_d14_wells_count))
  elseif part == 9 then
    local w = dgsim_d14_wells_v
    p('d14 wells 9: Vanquish target', tostring(w), 'exists', tostring(w and nGameObject.Exists(w)), 'alive',
      tostring(w and nGameObject.Exists(w) and nGameObject.IsAlive(w)), '(expected dead: 300 + 30 x level true damage)',
      '; gold', f2(dgsim_d14_wells_gold), '->', f2(nPlayer.GetPlayerAvailableGold(pid)), '(the passive income adds about',
      '1.7 a second); xp', tostring(dgsim_d14_wells_xp), '->', tostring(nPlayer.GetPlayerXP and nPlayer.GetPlayerXP(pid)),
      '; workers at the well', tostring(dgsim_d14_wells_count), '->', #workers(SPIRIT_WELL, 40))
  else
    census(SPIRIT_WELL, 'part ' .. part .. ' census (a killed worker\'s slot refilled one period later)')
  end
end)
if not ok then p('d14 wells: ERROR', err) end
return table.concat(out, '\n')
