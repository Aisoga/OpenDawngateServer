-- D15b test probe ([host] test_lua=<s>:dgsim_d15b_bot_hit.lua): the enemy bot
-- dgsim_d15_bot_here.lua brought (dgsim_d15_bot) hits player 0's shaper with dgsim_d15b_bot_magic (300) magical damage
-- from a snapshot of its own creature, through the host's damage core: a player attacker, as Retribution's
-- StoreDamage (ModifyDamageIncoming: magical damage from a player, 10 % stored) asks, and a hurt shaper for the heals
-- that follow (Assimilation's kill heal). Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local ok, err = pcall(function()
  local me = nPlayer.GetPlayerControlledObjectID(0)
  local bot = dgsim_d15_bot
  if me == nil or me == kObjIDNone or bot == nil or bot == kObjIDNone or not nGameObject.IsAlive(bot) then
    p('d15b bot hit: no shaper or no live bot (run dgsim_d15_bot_here.lua first)')
    return
  end
  local amount = dgsim_d15b_bot_magic or 300
  local hp0 = nGameObject.GetHitPoints(me)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(bot, kObjIDNone)
  local s, dealt = nGameObject.TakeDamage(snap, me, amount, 0, nDescriptors.IsMagicalDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  p('d15b bot hit:', tostring(bot), nGameObject.GetAssetName(bot), '-> player 0', tostring(me), 'magical', amount,
    'dealt', f2(dealt), 'hp', f2(hp0), '->', f2(nGameObject.GetHitPoints(me)), 'of', f2(nGameObject.GetMaxHitPoints(me)),
    'MR', f2(nAttribute.GetAttributeValue(me, nAttributeType.MagicalDamageReduction)), 'at match time',
    string.format('%.2f', nGameSimulator.GetMatchTime()))
end)
if not ok then p('d15b bot hit: ERROR', err) end
return table.concat(out, '\n')
