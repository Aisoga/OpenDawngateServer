-- D9c live probe ([host] test_lua=<s>:dgsim_d9c_capture.lua,...; docs/MATCH_FLOW.md *D9c: only an enemy player's hit
-- pauses a capture*): the human captures the enemy spirit well nearest to it (walked there by the commands file, the
-- wells unlocked at 45 s by [host] wells_lock_first=45) while things hit it. Each call runs the next part of one of
-- two modes, chosen at the first call:
--   * mode 'npc' (no living enemy bot: bots=0): workers, minions, a structure and the human's own health cost hit the
--     capturer; expected under D9c's rule (the default) no "capture PAUSED" line and the capture 16 s after the unlock,
--     every "wells: hit" line "not a player's hit: no pause"; with [host] wells_damage_pause_any=1 (D9's rule) pauses
--     and a late capture instead:
--       part 1 (before the unlock, ~40 s): the human teleported 9 units from the well's centre toward the first worker's
--               spot (inside the 12-unit capture sphere, within the workers' tether reach), rooted (Immobilized +1),
--               its auto-attack off (AutoAttackDisabled +1, so it doesn't kill its attackers) and healed;
--       part 2 (~44 s): 1 true damage from the human on every living worker of the well within 30 (they engage it and
--               call for help) and two melee and one ranged enemy minion 2-5 units from it, rooted (unrooted they walk
--               off toward the enemy base);
--       parts 3+ (every few s): status (the human's HP and who has it on their aggro list), a 15 physical basic hit
--               from each probe minion's snapshot (minions spawned without a lane go for the enemy base's cores and
--               never swing at the human: d9c_npc3d), a heal when below 60 %; part 4 also a 25 true self-hit (the
--               human's own snapshot: a health cost), part 5 a 25 true hit from the nearest enemy Binding's snapshot (a
--               structure);
--       the last part (call it after the capture, ~64 s): the well's owner, cleanup (root, auto-attack, minions).
--   * mode 'shaper' (a living enemy bot: bots on, best with bot_brain=0 so the bots stand still): an enemy shaper
--     contests the ring, then hits the capturer:
--       part 1 (~40 s): the human placed as above; the first enemy bot's auto-attack off;
--       part 2 (~49 s): that bot teleported 3 units from the centre (in the ring) and rooted: expected "capture PAUSED
--               ... contested", the score held;
--       part 3 (~55 s): the bot teleported 30 units out: expected "capture started" again (the resume);
--       part 4 (~58 s): 20 true from the bot's snapshot on the human: expected a "wells: hit" line "an enemy player's
--               hit: pauses the capture" and "capture PAUSED ... every capturer hit" for 5 s, then the resume;
--       part 5+ : status; the last part (~76 s, after the capture ~72 s): owner, cleanup.
-- The mode's last part is the call after `dgsim_d9c_last` (default 7 in 'npc', 6 in 'shaper'). Returns a report
-- (logged by the host after "host: test_lua dgsim_d9c_capture.lua at <s> s after EnterGame").
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local WELLS = {
  {pos = nVector.create(-182.3, 27.9, 0), team = 1}, {pos = nVector.create(28.2, -182.6, 0), team = 1},
  {pos = nVector.create(182.5, -27.9, 0), team = 2}, {pos = nVector.create(-27.6, 182.7, 0), team = 2},
}

local function dist(a, b)
  local dx, dy = a.x - b.x, a.y - b.y
  return math.sqrt(dx * dx + dy * dy)
end
local function xy(v) return string.format('(%.1f, %.1f)', v.x, v.y) end

-- nPlayer.IsBot takes the player's creature, not its index
local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c, pid end
  end
end

local function enemyBots(team)
  local list = {}
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and nPlayer.IsBot(c) and nGameObject.GetTeam(c) ~= team and nGameObject.IsAlive(c) then
      list[#list + 1] = c
    end
  end
  return list
end

local function objects(prefix, centre, radius)
  local list, seen = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      if string.find(name, prefix) and nGameObject.IsAlive(id) then
        if centre == nil or dist(nGameObject.GetPosition(id), centre) <= radius then list[#list + 1] = id end
      end
    end
  end
  return list
end

local function teleport(o, pos) return nLocomotion.TeleportToPositionAndFacing(o, pos, nVector.create(0, 1, 0)) end

local function hit(from, to, amount)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(from, kObjIDNone)
  local hp0 = nGameObject.GetHitPoints(to)
  local s, dealt = nGameObject.TakeDamage(snap, to, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  return hp0, nGameObject.GetHitPoints(to), dealt
end

local function heal(me)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(me, kObjIDNone)
  local hp0 = nGameObject.GetHitPoints(me)
  nGameObject.HealDamage(snap, me, nGameObject.GetMaxHitPoints(me), 0)
  nAttribute.RemoveAttributeSnapshot(snap)
  return hp0, nGameObject.GetHitPoints(me)
end

-- who has the human on their aggro list (workers, minions, bots), within 30
local function attackers(me)
  local names = {}
  local n = 0
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if nGameObject.IsAlive(id) and nGameObject.GetTeam(id) ~= nGameObject.GetTeam(me)
        and dist(nGameObject.GetPosition(id), nGameObject.GetPosition(me)) <= 30 then
      local ok, targets = pcall(nAgent.GetTargetsOnAggroList, id)
      if ok and type(targets) == 'table' then
        for _, t in ipairs(targets) do
          if t == me then
            n = n + 1
            local nm = string.gsub(nGameObject.GetAssetName(id) or '?', '%.Noun$', '')
            names[nm] = (names[nm] or 0) + 1
          end
        end
      end
    end
  end
  local s = {}
  for k, v in pairs(names) do s[#s + 1] = k .. ' x' .. v end
  table.sort(s)
  return n, table.concat(s, ', ')
end

local function status(me, tag)
  local n, who = attackers(me)
  p(string.format('d9c %s: match %s, human hp %s/%s at %s (%.1f from the well), %d enemies with the human on their list '
    .. '[%s], workers at the well %d', tag, f2(nGameSimulator.GetMatchTime()), f2(nGameObject.GetHitPoints(me)),
    f2(nGameObject.GetMaxHitPoints(me)), xy(nGameObject.GetPosition(me)), dist(nGameObject.GetPosition(me),
    dgsim_d9c_well.pos), n, who, #objects('^Titan_Node_Follower', dgsim_d9c_well.pos, 40)))
end

local function place(me)
  -- 9 units from the centre toward the first worker's spot (its return-to-work point), else toward the well's workers
  local w = dgsim_d9c_well.pos
  local dir = nil
  for _, id in ipairs(objects('^Titan_Node_Follower', w, 40)) do
    local okRet, hasRet, ret = pcall(nLocomotion.GetFollowerMiningReturnToWorkPoint, id)
    local at = (okRet and hasRet and ret) or nGameObject.GetPosition(id)
    if dist(at, w) > 6 then
      dir = at
      break
    end
  end
  dir = dir or nVector.create(w.x, w.y - 20, 0)
  local d = dist(dir, w)
  local spot = nVector.create(w.x + (dir.x - w.x) * 9 / d, w.y + (dir.y - w.y) * 9 / d, 0)
  local okT = teleport(me, spot)
  dgsim_d9c_root = nAttribute.AddAttributeModifier(me, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
  dgsim_d9c_noaa = nAttribute.AddAttributeModifier(me, nAttributeType.AutoAttackDisabled, 1, nAttributeOp.kAdd)
  local h0, h1 = heal(me)
  p('d9c 1: human teleported to', xy(spot), '->', tostring(okT), 'now at', xy(nGameObject.GetPosition(me)), string.format(
    '(%.1f from the well %s, toward %s); rooted %s, auto-attack off %s, healed %s -> %s', dist(nGameObject.GetPosition(me),
    w), xy(w), xy(dir), tostring(dgsim_d9c_root), tostring(dgsim_d9c_noaa), f2(h0), f2(h1)))
end

local function cleanup(me)
  if dgsim_d9c_root then nAttribute.RemoveAttributeModifier(me, dgsim_d9c_root) dgsim_d9c_root = nil end
  if dgsim_d9c_noaa then nAttribute.RemoveAttributeModifier(me, dgsim_d9c_noaa) dgsim_d9c_noaa = nil end
  local n = 0
  for _, m in ipairs(dgsim_d9c_minions or {}) do
    if nGameObject.Exists(m) then nGameObject.MarkForDelete(m) n = n + 1 end
  end
  dgsim_d9c_minions = {}
  local b = dgsim_d9c_bot
  if b and dgsim_d9c_botroot then nAttribute.RemoveAttributeModifier(b, dgsim_d9c_botroot) dgsim_d9c_botroot = nil end
  if b and dgsim_d9c_botnoaa then nAttribute.RemoveAttributeModifier(b, dgsim_d9c_botnoaa) dgsim_d9c_botnoaa = nil end
  p('d9c: cleanup: the human\'s root and auto-attack restored,', n, 'probe minion(s) removed')
end

local ok, err = pcall(function()
  local me, pid = human()
  if not me then
    p('d9c: no human player creature')
    return
  end
  local team = nGameObject.GetTeam(me)
  if dgsim_d9c_mode == nil then
    dgsim_d9c_mode = #enemyBots(team) > 0 and 'shaper' or 'npc'
    dgsim_d9c_part = 0
    local here = nGameObject.GetPosition(me)
    local best, bd = nil, nil
    for _, w in ipairs(WELLS) do
      if w.team ~= team then
        local d = dist(here, w.pos)
        if bd == nil or d < bd then best, bd = w, d end
      end
    end
    dgsim_d9c_well = best
    p('d9c: mode', dgsim_d9c_mode, 'human', tostring(me), 'player', tostring(pid), 'team', team, 'at', xy(here),
      'the enemy well', xy(best.pos), string.format('%.1f away', bd))
  end
  dgsim_d9c_part = dgsim_d9c_part + 1
  local part = dgsim_d9c_part
  local last = dgsim_d9c_last or (dgsim_d9c_mode == 'npc' and 7 or 6)
  local well = dgsim_d9c_well.pos

  if part == 1 then
    place(me)
    if dgsim_d9c_mode == 'shaper' then
      local b = enemyBots(team)[1]
      dgsim_d9c_bot = b
      dgsim_d9c_botnoaa = nAttribute.AddAttributeModifier(b, nAttributeType.AutoAttackDisabled, 1, nAttributeOp.kAdd)
      p('d9c 1: enemy bot', tostring(b), nGameObject.GetAssetName(b), 'at', xy(nGameObject.GetPosition(b)), string.format(
        '(%.1f from the well); its auto-attack off %s', dist(nGameObject.GetPosition(b), well), tostring(dgsim_d9c_botnoaa)))
    end
    status(me, '1')
  elseif part >= last then
    status(me, 'last (' .. part .. ')')
    local mines = nBattlegrounds.GetMinesForTeam(team) or {}
    p('d9c last: wells of team', team, ':', #mines, '(expected 3 after the capture)')
    cleanup(me)
  elseif dgsim_d9c_mode == 'npc' then
    if part == 2 then
      local ws = objects('^Titan_Node_Follower', well, 30)
      local n = 0
      for _, w in ipairs(ws) do
        hit(me, w, 1)
        n = n + 1
      end
      dgsim_d9c_minions = {}
      local enemy = team == 1 and 'Spirit' or 'Mortal'
      local here = nGameObject.GetPosition(me)
      -- rooted (Immobilized +1) next to the human, or they walk off to their lane (d9c_npc1d: they attacked a core)
      local spawns = {{'Melee', 2, 0}, {'Melee', 0, 2}, {'Ranged', -5, 0}}
      for _, s in ipairs(spawns) do
        local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_' .. enemy .. '_' .. s[1] .. '.Noun'), nTeamID[enemy],
          nVector.create(here.x + s[2], here.y + s[3], 0), kZeroVector, 1)
        if m then
          dgsim_d9c_minions[#dgsim_d9c_minions + 1] = m
          nAttribute.AddAttributeModifier(m, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
        end
      end
      p('d9c 2: 1 true from the human on', n, 'worker(s) of the well (they engage and call for help);',
        #dgsim_d9c_minions, 'enemy minion(s) spawned 2-5 units from the human and rooted (aggro on sight)')
      status(me, '2')
    else
      status(me, tostring(part))
      -- the probe minions' hits: from each minion's own snapshot (a 15 physical basic), since minions spawned without a
      -- lane take the enemy base's cores as their best target (D5's AddBaseStructuresToAggroList; d9c_npc3d: rooted, they
      -- kept the human on their list but pursued nub A and never swung); the damage core sees the minion as the attacker
      for _, m in ipairs(dgsim_d9c_minions or {}) do
        if nGameObject.Exists(m) and nGameObject.IsAlive(m) then
          local snap = nAttribute.CreateAttributeSnapshot_FromObject(m, kObjIDNone)
          local h0 = nGameObject.GetHitPoints(me)
          nGameObject.TakeDamage(snap, me, 15, 0, nBit.Or(nDescriptors.IsBasic, nDescriptors.IsPhysicalDamage), kZeroVector)
          nAttribute.RemoveAttributeSnapshot(snap)
          p('d9c', part .. ': 15 physical basic from the minion', tostring(m), nGameObject.GetAssetName(m), ': hp', f2(h0), '->',
            f2(nGameObject.GetHitPoints(me)), '; expected "wells: hit ... not a player\'s hit: no pause"')
        end
      end
      if part == 4 then
        local h0, h1, d = hit(me, me, 25)
        p('d9c 4: 25 true self-hit (the human\'s own snapshot, a health cost): hp', f2(h0), '->', f2(h1),
          '; expected "wells: hit ... not a player\'s hit: no pause"')
      elseif part == 5 then
        local enemy = team == 1 and 'Spirit' or 'Mortal'
        local bs = objects('^Binding_' .. enemy, nil, 0)
        local b, bd = nil, nil
        for _, id in ipairs(bs) do
          local d = dist(nGameObject.GetPosition(id), nGameObject.GetPosition(me))
          if bd == nil or d < bd then b, bd = id, d end
        end
        if b then
          local h0, h1 = hit(b, me, 25)
          p('d9c 5: 25 true from the enemy structure', tostring(b), nGameObject.GetAssetName(b), string.format('(%.0f away)', bd),
            ': hp', f2(h0), '->', f2(h1), '; expected "wells: hit ... not a player\'s hit: no pause"')
        else
          p('d9c 5: no enemy Binding found')
        end
      end
      if nGameObject.GetHitPoints(me) < 0.6 * nGameObject.GetMaxHitPoints(me) then
        local h0, h1 = heal(me)
        p('d9c', part .. ': healed', f2(h0), '->', f2(h1))
      end
    end
  else  -- 'shaper'
    local b = dgsim_d9c_bot
    if part == 2 then
      local spot = nVector.create(well.x - 3, well.y, 0)
      local okT = teleport(b, spot)
      dgsim_d9c_botroot = nAttribute.AddAttributeModifier(b, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
      p('d9c 2: enemy bot', tostring(b), 'teleported into the ring at', xy(spot), '->', tostring(okT), string.format(
        '(%.1f from the centre), rooted %s; expected "capture PAUSED ... contested" and the score held',
        dist(nGameObject.GetPosition(b), well), tostring(dgsim_d9c_botroot)))
    elseif part == 3 then
      if dgsim_d9c_botroot then nAttribute.RemoveAttributeModifier(b, dgsim_d9c_botroot) dgsim_d9c_botroot = nil end
      local spot = nVector.create(well.x - 30, well.y, 0)
      local okT = teleport(b, spot)
      dgsim_d9c_botroot = nAttribute.AddAttributeModifier(b, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
      p('d9c 3: enemy bot teleported out to', xy(spot), '->', tostring(okT), string.format(
        '(%.1f from the centre); expected "capture started" (the resume)', dist(nGameObject.GetPosition(b), well)))
    elseif part == 4 then
      local h0, h1, d = hit(b, me, 20)
      p('d9c 4: 20 true from the enemy bot', tostring(b), 'on the human: hp', f2(h0), '->', f2(h1),
        '; expected "wells: hit ... an enemy player\'s hit: pauses the capture" and "capture PAUSED ... every capturer '
        .. 'hit", the resume 5 s later')
    end
    status(me, tostring(part))
  end
end)
if not ok then p('d9c: ERROR', err) end
return table.concat(out, '\n')
