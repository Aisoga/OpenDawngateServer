-- ADAMSLOW test probe ([host] test_lua=<s>:dgsim_adamslow_reverse.lua, after dgsim_adamslow_lib.lua): the other side's
-- Lockdown, what a player sees when an enemy holds
-- Adamance. The first live enemy bot gets Lockdown's passive (modifier_Item_Legendary_OnTookPhysical_AoESlow, requested
-- on itself once, as LabsPlayer_OnItemAdded activates it) and is the holder; the minions of both teams and a jungle
-- creep are gathered around it, player 0's allied bots 4 south of it, player 0 3 east; player 0 hits it with
-- dgsim_adamslow_hits (6) physical basics of dgsim_adamslow_damage (10). The nova should slow player 0, its allied bots
-- and player 0's minions (the holder's enemies) and the creep, never the holder's own minions. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local L = dgsim_adamslow
  if L == nil then
    p('adamslow reverse: no dgsim_adamslow (run dgsim_adamslow_lib.lua first)')
    return
  end
  local me = nPlayer.GetPlayerControlledObjectID(0)
  if not L.ok_id(me) or not nGameObject.IsAlive(me) then
    p('adamslow reverse: player 0 has no live shaper')
    return
  end
  local team = nGameObject.GetTeam(me)
  local bot
  for i = 1, 9 do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if L.ok_id(id) and nGameObject.IsAlive(id) and nGameObject.GetTeam(id) ~= team then
      bot = id
      break
    end
  end
  if bot == nil then
    p('adamslow reverse: no live enemy player')
    return
  end
  local okm, m = pcall(nModifier.GetFirstModifierByGUID, bot, L.PASSIVE)
  if not okm or not L.ok_id(m) then
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(bot, kObjIDNone)
    nModifier.RequestModifier(bot, bot, L.PASSIVE, snap, 1, kZeroVector)
    nAttribute.RemoveAttributeSnapshot(snap)
    okm, m = pcall(nModifier.GetFirstModifierByGUID, bot, L.PASSIVE)
  end
  dgsim_adamslow_reverse_n = (dgsim_adamslow_reverse_n or 0) + 1
  p('adamslow reverse', dgsim_adamslow_reverse_n, ': holder enemy bot', tostring(bot), L.name(bot), 'team',
    nGameObject.GetTeam(bot), 'passive', okm and tostring(m) or '?', 'stacks before', L.stacks(bot))
  -- gather() with the bot as the holder: its own team's minions are "own", player 0's are "other"; gather() moves the
  -- bot's allied players (other bots of its team) beside it, and player 0's allied bots are placed here
  -- first the bot to player 0 (wherever the bot stood, its base's guns included), then everything around the bot
  local mp = nGameObject.GetPosition(me)
  nLocomotion.TeleportToPositionAndFacing(bot, nVector.create(mp.x - 3, mp.y, 0), nVector.create(1, 0, 0))
  L.gather(bot, me, p)
  local bp = nGameObject.GetPosition(bot)
  local k = 0
  for i = 1, 9 do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if L.ok_id(id) and nGameObject.IsAlive(id) and nGameObject.GetTeam(id) == team then
      k = k + 1
      nLocomotion.TeleportToPositionAndFacing(id, nVector.create(bp.x - 2 * k, bp.y - 4, 0), nVector.create(0, 1, 0))
      p('adamslow reverse: player 0\'s ally', tostring(id), L.name(id), 'to', L.f1(bp.x - 2 * k), L.f1(bp.y - 4))
    end
  end
  L.hit(bot, me, dgsim_adamslow_hits or 6, dgsim_adamslow_damage or 10, p)
  L.scan('reverse' .. dgsim_adamslow_reverse_n, p)
end)
if not ok then p('adamslow reverse: ERROR', err) end
return table.concat(out, '\n')
