-- Drain's heal, a live probe ([host] test_lua, this file at e.g. 40, 41, 43, 47, 50, 51, 53, 57 s; bots on, the human
-- moved out of its base first, e.g. commands_file with "1 <move to (-120, -190)>"; [host] damage_event_instance).
--   part 1: wraps modifier_Item_Drain.OnTookDamage (the instance test it makes), modifier_Item_Drain_ProjectileHandler's
--           Tick (start and end, the caster's HP) and nGameObject.HealDamage (every call while a handler runs); the
--           nearest enemy bot teleported 10 east of the human; the human hurt by 40 % of its max HP (true damage from
--           the bot's snapshot) so a heal can show; Drain requested from the human on the bot;
--   parts 2-4: the records so far, the human's HP (expected: up 0.9 x each tick's damage, about 0.9 x 13.5 a tick at
--           level 1 after the bot's magic resist, five ticks), whether the handler is on the human;
--   part 5: the bot (the next enemy bot if the first died) healed to full, hurt the same way and Drain requested
--           from the bot on the human (the bot's side);
--   parts 6-8: the records and the bot's HP.
-- Every call returns its lines (logged by the host).
dgsim_drainheal_log = dgsim_drainheal_log or {}
local out = {}
local function p(s) out[#out + 1] = s end
local function rec(s) dgsim_drainheal_log[#dgsim_drainheal_log + 1] = string.format('t=%.2f %s', nGameSimulator.GetMatchTime(), s) end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end
local function hp(o) return f2(nGameObject.GetHitPoints(o)) .. '/' .. f2(nGameObject.GetMaxHitPoints(o)) end
local function has(o, name) return nModifier.AgentHasModifierMatchingGUID(o, nUtil.SPID(name)) end
local function request(name, caster, target)
  return nAbility.RequestAbility(nUtil.SPID(name), caster, target or kObjIDNone, kZeroVector, 1, 0,
    nAbilitySlot.None, kObjIDNone, true, false, nil)
end
local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)  -- nPlayer.IsBot takes the creature
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c end
  end
end
local function nearestEnemyBot(from)
  local sp, team = nGameObject.GetPosition(from), nGameObject.GetTeam(from)
  local best, bestD = nil, 1e9
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and nPlayer.IsBot(c) and nGameObject.IsAlive(c) and nGameObject.GetTeam(c) ~= team then
      local q = nGameObject.GetPosition(c)
      local d = math.sqrt((q.x - sp.x) ^ 2 + (q.y - sp.y) ^ 2)
      if d < bestD then best, bestD = c, d end
    end
  end
  return best
end
local function hurt(victim, by)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(by, kObjIDNone)
  nGameObject.TakeDamage(snap, victim, 0.4 * nGameObject.GetMaxHitPoints(victim), 0, nDescriptors.IsTrueDamage, kZeroVector)
end

local function wrap()
  if dgsim_drainheal_wrapped then return end
  dgsim_drainheal_wrapped = true
  dgsim_drainheal_handlers = 0
  local m = modifier_Item_Drain
  local otd = m[nAbilityFns.OnTookDamage]
  m[nAbilityFns.OnTookDamage] = function(p1)
    pcall(function()
      local mine = nAbility.GetAbilityInstanceID()
      rec(string.format('Drain OnTookDamage: event instance %s, this instance %s, match %s, damage %s, caster %s alive %s hp %s',
        tostring(p1.tookDamageAbilityInstance), tostring(mine), tostring(p1.tookDamageAbilityInstance == mine),
        f2(p1.tookDamageDamage), tostring(p1.myCasterID), tostring(nGameObject.IsAlive(p1.myCasterID)), hp(p1.myCasterID)))
    end)
    return otd(p1)
  end
  local h = modifier_Item_Drain_ProjectileHandler
  local tick = h[nAbilityFns.Tick]
  h[nAbilityFns.Tick] = function(p1)
    dgsim_drainheal_handlers = dgsim_drainheal_handlers + 1
    pcall(function()
      rec(string.format('handler start on %s from %s: amountToHeal %s, hp %s', tostring(p1.myObjectID), tostring(p1.sourceID),
        f2(p1.amountToHeal), hp(p1.myObjectID)))
    end)
    tick(p1)
    dgsim_drainheal_handlers = dgsim_drainheal_handlers - 1
    pcall(function() rec(string.format('handler end on %s: hp %s', tostring(p1.myObjectID), hp(p1.myObjectID))) end)
  end
  local heal = nGameObject.HealDamage
  nGameObject.HealDamage = function(snap, target, amount, ...)
    local before = (dgsim_drainheal_handlers > 0) and nGameObject.GetHitPoints(target) or nil
    local r = { heal(snap, target, amount, ...) }
    if before then
      pcall(function()
        rec(string.format('HealDamage %s by %s: hp %s -> %s (returned %s)', tostring(target), f2(amount), f2(before),
          f2(nGameObject.GetHitPoints(target)), tostring(r[1])))
      end)
    end
    return unpack(r)
  end
end

local ok, err = pcall(function()
  local me = human()
  if not me then p('drainheal: no human creature') return end
  dgsim_drainheal_part = (dgsim_drainheal_part or 0) + 1
  local part = dgsim_drainheal_part
  if part == 1 then
    wrap()
    local bot = nearestEnemyBot(me)
    if not bot then p('drainheal 1: no living enemy bot') return end
    dgsim_drainheal_bot = bot
    local sp = nGameObject.GetPosition(me)
    nLocomotion.TeleportToPositionAndFacing(bot, nVector.create(sp.x + 10, sp.y, sp.z), nGameObject.GetFacing(bot))
    hurt(me, bot)
    p(string.format('drainheal 1: human %s hp %s, bot %s %s hp %s; Drain human -> bot: %s', tostring(me), hp(me),
      tostring(bot), nGameObject.GetAssetName(bot) or '?', hp(bot), tostring(request('ability_Item_Drain', me, bot))))
  elseif part == 5 then
    local bot = dgsim_drainheal_bot
    if not bot or not nGameObject.IsAlive(bot) then
      -- the human's auto-attacks may have killed part 1's bot (drainheal_live_d1): take the next one
      bot = nearestEnemyBot(me)
      if not bot then p('drainheal 5: no living enemy bot') return end
      dgsim_drainheal_bot = bot
      local sp = nGameObject.GetPosition(me)
      nLocomotion.TeleportToPositionAndFacing(bot, nVector.create(sp.x + 10, sp.y, sp.z), nGameObject.GetFacing(bot))
    end
    -- back to full first, so the 40 % below leaves it alive under the human's auto-attacks for Drain's 5 s
    nGameObject.HealDamage(nAttribute.CreateAttributeSnapshot_FromObject(bot, kObjIDNone), bot,
      nGameObject.GetMaxHitPoints(bot), 0, 0, false, false)
    hurt(bot, me)
    p(string.format('drainheal 5: bot %s hp %s, human hp %s; Drain bot -> human: %s', tostring(bot), hp(bot), hp(me),
      tostring(request('ability_Item_Drain', bot, me))))
  else
    local bot = dgsim_drainheal_bot
    p(string.format('drainheal %d: human hp %s (handler on it %s, Drain on it %s), bot hp %s (handler %s, Drain %s)', part,
      hp(me), tostring(has(me, 'modifier_Item_Drain_ProjectileHandler')), tostring(has(me, 'modifier_Item_Drain')),
      bot and hp(bot) or '-', bot and tostring(has(bot, 'modifier_Item_Drain_ProjectileHandler')) or '-',
      bot and tostring(has(bot, 'modifier_Item_Drain')) or '-'))
  end
  for _, s in ipairs(dgsim_drainheal_log) do p('  ' .. s) end
  dgsim_drainheal_log = {}
end)
if not ok then p('drainheal ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
