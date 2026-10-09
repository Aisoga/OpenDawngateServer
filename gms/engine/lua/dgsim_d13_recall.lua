-- D13 live probe (docs/DAMAGE.md *Combat edge cases (D13)*, "damage at the moment a recall starts"; [host]
-- test_lua=<T>:dgsim_d13_recall.lua,<T+2>:dgsim_d13_recall.lua,<T+4>:dgsim_d13_recall.lua,<T+13>:dgsim_d13_recall.lua;
-- the human's shaper away from its base, e.g. after a test-side move). ability_returntobase's Activate requests
-- modifier_returntobase (an untimed AbsorbDamage that sets interrupt and lets everything through) and its own
-- OnTookDamage returns false, which ends it. Each call runs the next part:
--   part 1: the recall forced, then in the same Lua call (before its Tick ever ran) 10 true damage from an enemy minion's
--     snapshot: expected the recall no longer running (IsAbilityRunning false), no teleport later;
--   part 2: Fenmore's Q shield (modifier_Alpha_Q_Shield r1, 50) first, then the recall and 20 true damage the shield takes
--     whole (dealt 0): expected the recall ended all the same (OnTookDamage fires for a fully absorbed hit, [assumed]);
--   part 3: the recall alone (no hit): expected running, the position kept for part 4;
--   part 4 (9 s after part 3, past its 8 s): expected the shaper at its start point (moved), the recall over.
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

-- nPlayer.IsBot takes the player's creature (an object id), not the player id (D13-live)
local function humanShaper()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c end
  end
end

local function enemySnapshot(shaper)
  local team = nGameObject.GetTeam(shaper)
  local noun, enemy = 'Minion_Spirit_Melee.Noun', nTeamID.Spirit
  if team == nTeamID.Spirit then noun, enemy = 'Minion_Mortal_Melee.Noun', nTeamID.Mortal end
  local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), enemy, nVector.create(66.0, -71.0, 0), kZeroVector, 1)
  if not m then return nil, nil end
  return m, nAttribute.CreateAttributeSnapshot_FromObject(m, kObjIDNone)
end

local function pos(o)
  local v = nGameObject.GetPosition(o)
  return string.format('(%.1f, %.1f)', v.x, v.y)
end

-- RequestAbility's second result is 0 for the recall (D13-live, d13_run1b: the instance existed all the same), so the
-- instance comes from the agent's abilities
local function recall(shaper)
  local guid = nUtil.SPID('ability_returntobase')
  local r1 = nAbility.RequestAbility(guid, shaper, kObjIDNone, kZeroVector, 1, 0, nAbilitySlot.None, kObjIDNone, true, false, nil)
  local inst = nAbility.GetFirstAbilityByGUID(shaper, guid)
  if inst == kObjIDNone then inst = nil end
  return r1, inst
end

local ok, err = pcall(function()
  local shaper = humanShaper()
  if not shaper then
    p('d13 recall: no human player creature')
    return
  end
  dgsim_d13_recall_part = (dgsim_d13_recall_part or 0) + 1
  local part = dgsim_d13_recall_part
  p('d13 recall part', part, ': shaper', tostring(shaper), 'hp', f2(nGameObject.GetHitPoints(shaper)), 'at', pos(shaper),
    'match time', nGameSimulator.GetMatchTime())
  if part <= 2 then
    local m, snap = enemySnapshot(shaper)
    if not snap then
      p('d13 recall: no enemy minion for the snapshot')
      return
    end
    if part == 2 then
      local self = nAttribute.CreateAttributeSnapshot_FromObject(shaper, kObjIDNone)
      p('d13 recall: Q shield ->', tostring(nModifier.RequestModifier(shaper, shaper, nUtil.SPID('modifier_Alpha_Q_Shield'), self, 1,
        kZeroVector, nil)))
      nAttribute.RemoveAttributeSnapshot(self)
    end
    local r1, inst = recall(shaper)
    local before = inst and nAbility.IsAbilityRunning(inst)
    local amount = part == 1 and 10 or 20
    local s, dealt, crit, absorbed = nGameObject.TakeDamage(snap, shaper, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
    p(string.format('d13 recall part %d: RequestAbility(ability_returntobase) -> %s %s, running %s; %s true -> dealt %s '
      .. 'absorbed %s; recall running after the hit: %s (expected false)', part, tostring(r1), tostring(inst),
      tostring(before), f2(amount), f2(dealt), f2(absorbed), tostring(inst and nAbility.IsAbilityRunning(inst))))
    nAttribute.RemoveAttributeSnapshot(snap)
    nGameObject.MarkForDelete(m)
  elseif part == 3 then
    local r1, inst = recall(shaper)
    dgsim_d13_recall_from = pos(shaper)
    dgsim_d13_recall_inst = inst
    p('d13 recall part 3: RequestAbility(ability_returntobase) ->', tostring(r1), tostring(inst), 'running',
      tostring(inst and nAbility.IsAbilityRunning(inst)), '(expected true; no hit)')
  else
    local inst = dgsim_d13_recall_inst
    p('d13 recall part 4: from', tostring(dgsim_d13_recall_from), 'now at', pos(shaper), 'recall running',
      tostring(inst and nAbility.IsAbilityRunning(inst)), '(expected at the start point, not running)')
  end
end)
if not ok then p('d13 recall: ERROR', err) end
return table.concat(out, '\n')
