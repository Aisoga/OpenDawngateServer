-- FOGSTACKS test fixture ([host] test_lua=<s>:dgsim_fogstacks.lua, the same file at every step; run with
-- bots=1;bots_allies=0;bot_brain=0;director_waves=0 so one enemy bot stands still): a debuff that ends while its
-- target is out of sight. Each run does the next step of the list below (the global dgsim_fogstacks_step counts):
--   1 setup: the human shaper H to (-100, -104), the enemy bot B to (-100, -90), 14 units north (in H's sight, out of
--     its basic range); B's start (its base) is kept as the place out of sight
--   2, 5, 9 debuffs on B from H: Duress's counter twice (modifier_Item_Legendary_OnBasic_Root_Counter: two stacks,
--     its stack marker netherbind_root_marker2 over B, 4 s, no Deactivate) and Equilibrium's slow
--     (modifier_Item_Legendary_OnDamage_AttackSpeedSlow_Slow: 3 s, its effect destroyed by its Deactivate)
--   3, 4, 8, 12 report: B's place, whether H's team sees it, which of the two debuffs it still has
--   6, 10 away: B to its start (out of H's team's sight); 7, 11 back: B to (-100, -90) again
-- A test brief times them: e.g. case A (seen all the time) 2 at 26 s, 3 at 27, 4 at 36; case B (10 s in the fog) 5 at
-- 40, 6 at 41, 7 at 51, 8 at 52; case C (60 s) 9 at 60, 10 at 61, 11 at 121, 12 at 122. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f)', v.x, v.y) end

local COUNTER = 'modifier_Item_Legendary_OnBasic_Root_Counter'
local SLOW = 'modifier_Item_Legendary_OnDamage_AttackSpeedSlow_Slow'
local H_AT = nVector.create(-100.0, -104.0, 0)
local B_AT = nVector.create(-100.0, -90.0, 0)
local NAMES = {'setup', 'debuffs', 'report', 'report', 'debuffs', 'away', 'back', 'report', 'debuffs', 'away', 'back',
               'report'}

dgsim_fogstacks_step = (dgsim_fogstacks_step or 0) + 1
local step = dgsim_fogstacks_step
local what = NAMES[step] or 'report'

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    -- nPlayer.IsBot takes an object (0x00947660), not a player id
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c end
  end
end

local function enemyBot(h)
  local team = nGameObject.GetTeam(h)
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and c ~= h and nGameObject.GetTeam(c) ~= team and nGameObject.IsAlive(c) then
      return c
    end
  end
end

local function has(o, name)
  local id = nModifier.GetFirstModifierByGUID(o, nUtil.SPID(name))
  if id == nil or id == kObjIDNone then return 'no' end
  return tostring(id)
end

local function request(target, caster, name)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(caster, kObjIDNone)
  local id = nModifier.RequestModifier(target, caster, nUtil.SPID(name), snap, 1, kZeroVector, nil)
  nAttribute.RemoveAttributeSnapshot(snap)
  return id
end

local function teleport(o, v)
  nLocomotion.TeleportToPositionAndFacing(o, v, nVector.create(0, -1, 0))
end

local ok, err = pcall(function()
  local h = human()
  if h == nil then
    p('fogstacks', step, what .. ': no human shaper')
    return
  end
  local b = dgsim_fogstacks_bot or enemyBot(h)
  if b == nil then
    p('fogstacks', step, what .. ': no live enemy player (run with bots=1;bots_allies=0;bot_brain=0)')
    return
  end
  if what == 'setup' then
    dgsim_fogstacks_bot = b
    dgsim_fogstacks_away = nGameObject.GetPosition(b)
    teleport(h, H_AT)
    teleport(b, B_AT)
  elseif what == 'debuffs' then
    local c1 = request(b, h, COUNTER)
    local c2 = request(b, h, COUNTER)
    local s = request(b, h, SLOW)
    p('fogstacks', step, 'debuffs: counter requested twice ->', tostring(c1), tostring(c2), 'slow ->', tostring(s))
  elseif what == 'away' then
    teleport(b, dgsim_fogstacks_away or nVector.create(230.0, 230.0, 0))
  elseif what == 'back' then
    teleport(b, B_AT)
  end
  local hp, bp = nGameObject.GetPosition(h), nGameObject.GetPosition(b)
  p(string.format('fogstacks %d %s at match %.2f: human %s at %s, bot %s %s at %s (distance %.1f), seen by team %d %s; '
    .. 'counter %s, slow %s', step, what, nGameSimulator.GetMatchTime(), tostring(h), vec(hp), tostring(b),
    nGameObject.GetAssetName(b), vec(bp), nVector.distance(hp, bp), nGameObject.GetTeam(h),
    tostring(nGameObject.IsVisibleToTeam(b, nGameObject.GetTeam(h))), has(b, COUNTER), has(b, SLOW)))
end)
if not ok then p('fogstacks', step, what .. ': ERROR', err) end
return table.concat(out, '\n')
