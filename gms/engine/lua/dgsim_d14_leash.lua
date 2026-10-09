-- D14 live probe: a creep camp's leash, its reset and kiting (docs/MATCH_FLOW.md *D14*), plus a census of the camps'
-- aggro lists (who pulled a camp). [host] test_lua=<s>:dgsim_d14_leash.lua; the camp is the small Fish camp at
-- (65.7, -132.2) (first spawn 90 s); dgsim_camp_x/_y choose another. The human's moves come from the commands file.
-- Every call prints the census: each living Creep_Jungle_* with a non-empty aggro list and who is on it (human, bot,
-- minion or other; at most 24 lines). A call with the human more than 30 units from the camp does only that (a bot
-- session with the human idle in its base, the probe every 30 s: does anyone but the camp's attackers end up on a list?).
-- With the human near the camp the calls run the parts in order:
--   1 (~95 s; the human parked at (58, -124) since about 35 s): the members at home: aggro lists EMPTY (aggro range 0: no
--     aggro on sight with the human 8-12 units away), Untargetable 0, HealthRegen with the regen modifier's +2000 (home,
--     empty list); then the human hits the Fish minor (50 true): expected the human on EVERY member's list at once (the
--     damage hook and Agent_CallForHelp through the camp), the hit member's +2000 gone (OnTookDamage).
--   (commands: 101 s move to (30, -95), out of every member's 22-unit tether)
--   2 (~103 s): lists EMPTY (the tether prune at the next 500-ms update), members walking back with Untargetable 1
--     (nBehavior_Defend).
--   3 (~110 s): home again (within 1 of the initial position), Untargetable 0, hp = max (400 HP/s), +2000 regen.
--   (commands: 112 s move to (56, -121), inside the tether)
--   4 (~125 s): the human hits the medium (50 true): lists hold the human; the camp attacks it.
--   5 (~131 s): the camp still engaged although the human stands 10-15 units from the creeps (dropAggroRange 1e6, inside
--     the tether: no drop), the human's hp falling.
--   (commands: 132 s move to (30, -95))
--   6 (~142 s): reset again: lists empty, home, hp = max.
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end
local function pos(v)
  if not v then return 'nil' end
  return string.format('(%.1f, %.1f)', v.x, v.y)
end
local function attr(o, name)
  local ok, v = pcall(nAttribute.GetAttributeValue, o, nAttributeType[name])
  if ok then return f2(v) end
  return '?'
end

-- nPlayer.IsBot takes the player's creature (nPlayer_IsBot 0x00947660 -> SporeLabsLua_GetObjectArg), not its index
local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c, pid end
  end
end

local function kinds()
  local k = {}
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone then k[tostring(c)] = nPlayer.IsBot(c) and ('bot' .. tostring(pid)) or 'human' end
  end
  return k
end

local function list(o, who)
  local ids = {}
  local ok, targets = pcall(nAgent.GetTargetsOnAggroList, o)
  if ok and type(targets) == 'table' then
    for _, t in ipairs(targets) do
      local w = who and who[tostring(t)]
      if not w then
        local name = nGameObject.GetAssetName(t) or '?'
        w = string.find(name, '^Minion_') and 'minion' or name
      end
      ids[#ids + 1] = tostring(t) .. '=' .. w
    end
  end
  return '[' .. table.concat(ids, ' ') .. ']', #ids
end

local function live()
  local ids, seen = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if not seen[tostring(id)] then
      seen[tostring(id)] = true
      ids[#ids + 1] = id
    end
  end
  return ids
end

local function member(o, h, who)
  local l, n = list(o, who)
  local ip = nGameObject.GetInitialPosition(o)
  local cp = nGameObject.GetPosition(o)
  local home = (ip and cp) and nVector.distance(ip, cp) or -1
  p(string.format('d14 leash:   %s %s hp %s/%s at %s initial %s (%.1f from home), Untargetable %s, HealthRegen %s, '
    .. 'inCombat %s, list %s (%d), human dist %s', tostring(o), tostring(nGameObject.GetAssetName(o)),
    f2(nGameObject.GetHitPoints(o)), f2(nGameObject.GetMaxHitPoints(o)), pos(cp), pos(ip), home,
    attr(o, 'Untargetable'), attr(o, 'HealthRegen'), tostring(nGameObject.IsInCombat(o)), l, n,
    h and f2(nGameObject.GetObjectDistance(o, h)) or '-'))
end

local ok, err = pcall(function()
  local h, hpid = human()
  local who = kinds()
  local t = nGameSimulator.GetMatchTime()
  local ids = live()
  -- the census
  local lines = 0
  for _, id in ipairs(ids) do
    local name = nGameObject.GetAssetName(id) or ''
    if string.find(name, '^Creep_Jungle_') and nGameObject.IsAlive(id) then
      local l, n = list(id, who)
      if n > 0 and lines < 24 then
        lines = lines + 1
        p(string.format('d14 camp census at %s: %s %s at %s hp %s/%s list %s', f2(t), tostring(id), name,
          pos(nGameObject.GetPosition(id)), f2(nGameObject.GetHitPoints(id)), f2(nGameObject.GetMaxHitPoints(id)), l))
      end
    end
  end
  p(string.format('d14 camp census at %s: %d creep(s) with a non-empty aggro list', f2(t), lines))
  -- the camp's members
  local cx, cy = dgsim_camp_x or 65.7, dgsim_camp_y or -132.2
  local camp = {}
  for _, id in ipairs(ids) do
    local name = nGameObject.GetAssetName(id) or ''
    local v = nGameObject.GetPosition(id)
    local ip = nGameObject.GetInitialPosition(id)
    local ref = ip or v
    if string.find(name, '^Creep_Jungle_') and ref and math.abs(ref.x - cx) < 12 and math.abs(ref.y - cy) < 12 and
        nGameObject.IsAlive(id) then
      camp[#camp + 1] = id
    end
  end
  if not h then
    p('d14 leash: no human player creature')
    return
  end
  local hv = nGameObject.GetPosition(h)
  local dc = hv and math.sqrt((hv.x - cx) ^ 2 + (hv.y - cy) ^ 2) or 999
  if dc > 30 and (dgsim_d14_leash_part or 0) == 0 then
    p(string.format('d14 leash: the human at %s, %.1f from the camp: census only', pos(hv), dc))
    return
  end
  dgsim_d14_leash_part = (dgsim_d14_leash_part or 0) + 1
  local part = dgsim_d14_leash_part
  p(string.format('d14 leash part %d at match %s: camp (%.1f, %.1f), %d member(s); human %s at %s (%.1f from the camp) hp %s',
    part, f2(t), cx, cy, #camp, tostring(h), pos(hv), dc, f2(nGameObject.GetHitPoints(h))))
  local expect = {
    'members at home, lists EMPTY (no aggro on sight), Untargetable 0, HealthRegen with +2000',
    'lists EMPTY (out of the tether), walking home with Untargetable 1',
    'home (< 1), Untargetable 0, hp = max, HealthRegen with +2000',
    'before the second hit: at home',
    'still engaged: the human on the lists (inside the tether, no drop by distance), the human hp falling',
    'reset: lists EMPTY, home, hp = max',
  }
  p('d14 leash part ' .. part .. ' expected: ' .. tostring(expect[part] or 'report'))
  for _, o in ipairs(camp) do member(o, h, who) end
  if part == 1 or part == 4 then
    local want = part == 1 and 'Minor' or 'Medium'
    local target = camp[1]
    for _, o in ipairs(camp) do
      if string.find(nGameObject.GetAssetName(o) or '', want) then target = o end
    end
    if target then
      local snap = nAttribute.CreateAttributeSnapshot_FromObject(h, kObjIDNone)
      local s, dealt = nGameObject.TakeDamage(snap, target, 50, 0, nDescriptors.IsTrueDamage, kZeroVector)
      nAttribute.RemoveAttributeSnapshot(snap)
      p(string.format('d14 leash part %d: the human hits %s %s for 50 true: %s, dealt %s; right after (expected the human '
        .. 'on every member\'s list, the hit member\'s HealthRegen without the +2000):', part, tostring(target),
        tostring(nGameObject.GetAssetName(target)), tostring(s), f2(dealt)))
      for _, o in ipairs(camp) do member(o, h, who) end
    end
  end
end)
if not ok then p('d14 leash: ERROR', err) end
return table.concat(out, '\n')
