-- D14c live probe: the grass return and the minimap (leftovers of dgsim_d14_presentation.lua).
-- [host] test_lua with this file at nine times (global dgsim_d14c_part counts the calls); the recipe of d14c_pres6b
-- (practice game with bots, bot_brain=0 so nothing cancels the recall; '--ftue-required' on dgserver): 30, 32, 34, 36,
-- 138, 146, 150, 160, 166 s after EnterGame; shots every second monitor step from 120 (the HUD clock = this + ~10 s):
--   1  (30)  the human into the grass patch beside the Mortal top base (-112, -100), an immobilized Spirit minion at
--            (-100, -98) (outside the patch), a 10-point basic hit on it: expected the human on its aggro list;
--   2  (32)  expected IsInGrass true, invisible to Spirit, dropped from the list (reason "invisible");
--   3  (34)  the human 6 units EAST of the minion (-94, -98): outside the patch (D14b-live's spot was inside it);
--   4  (36)  expected IsInGrass false, visible to Spirit, on the list again; the minion deleted;
--   5  (138) two allied bots teleported apart (-150, -100) and (-94, -150), the nearest Spirit bot 18 east + 18 north of
--            the human (baseline of the icons);
--   6  (146) the first allied bot killed by a Spirit minion's snapshot (the minimap skull for 10 s), the second recalls
--            (ability_returntobase: the spinner while modifier_returntobase lasts, 8 s);
--   7  (150) states; 9 (166) states again (the skull gone, the recall over);
--   8  (160) the Spirit bot teleported to its start point (fogged: its icon leaves the minimap).
-- Every report line starts with "d14c".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'd14c ' .. table.concat(t, ' ')
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


local ok, err = pcall(function()
  local pid, shaper = human()
  if not shaper then p('no human player creature') return end
  dgsim_d14c_part = (dgsim_d14c_part or 0) + 1
  local part = dgsim_d14c_part
  p('part', part, 'match time', f1(nGameSimulator.GetMatchTime()), 'shaper at', vec(nGameObject.GetPosition(shaper)))
  if part == 1 then
    teleport(shaper, nVector.create(-112, -100, 0))
    local m = spawnSpiritMinion(nVector.create(-100, -98, 0))
    if not m then p('Spirit minion not created') return end
    dgsim_d14c_minion = m
    nAttribute.AddAttributeModifier(m, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
    local a, d = hit(shaper, m, 10, nDescriptors.IsBasic)
    p('human into the grass (-112, -100); minion', tostring(m), 'at', vec(nGameObject.GetPosition(m)), '; hit ->', tostring(a), f1(d),
      '; on its aggro list now:', tostring(nAgent.IsTargetOnAggroList(m, shaper)), '(expected true)')
  elseif part == 2 then
    local m = dgsim_d14c_minion
    if not m then return end
    p('in grass', tostring(nGameObject.IsInGrass(shaper)), '(expected true); visible to Spirit', tostring(nGameObject.IsVisibleToTeam(shaper, nTeamID.Spirit)),
      '(expected false); on the list', tostring(nAgent.IsTargetOnAggroList(m, shaper)), '(expected false)')
  elseif part == 3 then
    local m = dgsim_d14c_minion
    if not m then return end
    local mp = nGameObject.GetPosition(m)
    teleport(shaper, nVector.create(mp.x + 6, mp.y, 0))
    p('human outside the patch, 6 EAST of the minion at', vec(mp))
  elseif part == 4 then
    local m = dgsim_d14c_minion
    if not m then return end
    p('in grass', tostring(nGameObject.IsInGrass(shaper)), '(expected false); visible to Spirit', tostring(nGameObject.IsVisibleToTeam(shaper, nTeamID.Spirit)),
      '(expected true); on the list', tostring(nAgent.IsTargetOnAggroList(m, shaper)), '(expected true: back on the list); best target',
      tostring(nAgent.GetBestTarget(m)))
    nGameObject.MarkForDelete(m)
    dgsim_d14c_minion = nil
  elseif part == 5 then
    local allies = bots(nTeamID.Mortal)
    p('allied bots', #allies)
    -- the allies away from the base, apart from each other and from the human (the base icons overlap on the minimap):
    -- one call before the kill and the recall, so the screenshots have a baseline of the same icons
    if allies[1] then teleport(allies[1].c, nVector.create(-150, -100, 0)) end
    if allies[2] then teleport(allies[2].c, nVector.create(-94, -150, 0)) end
    dgsim_d14c_allies = allies
    local sp = nGameObject.GetPosition(shaper)
    for _, b in ipairs(bots(nTeamID.Spirit)) do
      if not dgsim_d14c_enemy and nGameObject.IsAlive(b.c) then
        dgsim_d14c_enemy = b
        teleport(b.c, nVector.create(sp.x + 18, sp.y + 18, 0))
        state(b.c, 'Spirit bot 18 east and 18 north of the human (its minimap icon and ring):')
      end
    end
  elseif part == 6 then
    local allies = dgsim_d14c_allies or {}
    local m = spawnSpiritMinion(nVector.create(150, 150, 0))
    if allies[1] and m then
      local a, d = hit(m, allies[1].c, nGameObject.GetHitPoints(allies[1].c) + 5000)
      dgsim_d14c_dead_ally = allies[1].c
      p('allied bot', tostring(allies[1].c), nGameObject.GetAssetName(allies[1].c), 'killed ->', tostring(a), f1(d), '(the minimap skull for 10 s)')
    end
    dgsim_d14c_killer = m
    if allies[2] and nGameObject.IsAlive(allies[2].c) then
      local guid = nUtil.SPID('ability_returntobase')
      local r1, inst = nAbility.RequestAbility(guid, allies[2].c, kObjIDNone, kZeroVector, 1, 0, nAbilitySlot.None, kObjIDNone, true, false, nil)
      -- the second result is 0 for a recall (D13-live): the instance comes from the agent's abilities
      local i2 = nAbility.GetFirstAbilityByGUID(allies[2].c, guid)
      dgsim_d14c_recaller = allies[2].c
      p('allied bot', tostring(allies[2].c), nGameObject.GetAssetName(allies[2].c), 'recall ->', tostring(r1), tostring(inst), 'instance', tostring(i2),
        'running', tostring(i2 ~= nil and i2 ~= kObjIDNone and nAbility.IsAbilityRunning(i2)), 'modifier_returntobase',
        tostring(nModifier.AgentHasModifierMatchingGUID(allies[2].c, nUtil.SPID('modifier_returntobase'))), '(the spinner for 8 s)')
    end
  elseif part == 7 or part == 9 then
    local r = dgsim_d14c_recaller
    if r then
      local i2 = nAbility.GetFirstAbilityByGUID(r, nUtil.SPID('ability_returntobase'))
      p('recaller', tostring(r), 'modifier_returntobase', tostring(nModifier.AgentHasModifierMatchingGUID(r, nUtil.SPID('modifier_returntobase'))),
        'running', tostring(i2 ~= nil and i2 ~= kObjIDNone and nAbility.IsAbilityRunning(i2)), 'at', vec(nGameObject.GetPosition(r)))
    end
    state(dgsim_d14c_dead_ally, 'dead ally:')
    if dgsim_d14c_enemy then state(dgsim_d14c_enemy.c, 'Spirit bot:') end
    if part == 9 and dgsim_d14c_killer and nGameObject.IsAlive(dgsim_d14c_killer) then nGameObject.MarkForDelete(dgsim_d14c_killer) end
  elseif part == 8 then
    local e = dgsim_d14c_enemy
    if e and nGameObject.IsAlive(e.c) then
      local pos, orient = nGameSimulator.GetPlayerStartingPoint(nPlayer.GetPlayerIdForObject(e.c) or e.pid)
      nLocomotion.TeleportToPositionAndOrientation(e.c, pos, orient)
      state(e.c, 'Spirit bot at its start point (expected visible to Mortal false: its minimap icon leaves):')
    end
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
