-- D15 test probe ([host] test_lua=<s>:dgsim_d15_bot_kill.lua; docs/ITEMS.md *The live batches*, batch B3): player 0
-- kills the enemy player dgsim_d15_bot_here.lua brought (else the first live enemy player): a lethal true-damage hit
-- from a snapshot of player 0's creature through the host's damage core, so the host's death path credits player 0
-- (OnNearbyDeath, OnKill, OnPlayerDeath, OnPlayerKillOrAssist on the killer: Grace's Zephyr Aura, Assimilation's heal,
-- the kill gold and the K/D/A). Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local me = nPlayer.GetPlayerControlledObjectID(0)
  if me == nil or me == kObjIDNone then
    p('d15 bot kill: no shaper for player 0')
    return
  end
  local bot = dgsim_d15_bot
  if bot == nil or bot == kObjIDNone or not nGameObject.IsAlive(bot) then
    bot = nil
    for i = 1, 9 do
      local id = nPlayer.GetPlayerControlledObjectID(i)
      if id ~= nil and id ~= kObjIDNone and nGameObject.GetTeam(id) ~= nGameObject.GetTeam(me) and nGameObject.IsAlive(id) then
        bot = id
        break
      end
    end
  end
  if bot == nil then
    p('d15 bot kill: no live enemy player')
    return
  end
  local hp0 = nGameObject.GetHitPoints(me)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(me, kObjIDNone)
  local hp = nGameObject.GetHitPoints(bot)
  local s, dealt = nGameObject.TakeDamage(snap, bot, hp + 1000, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  p('d15 bot kill:', tostring(bot), nGameObject.GetAssetName(bot), 'hp', hp, '->', nGameObject.GetHitPoints(bot), 'dealt',
    tostring(dealt), 'alive', tostring(nGameObject.IsAlive(bot)), '; player 0 hp', hp0, '->', nGameObject.GetHitPoints(me),
    'at match time', string.format('%.2f', nGameSimulator.GetMatchTime()))
end)
if not ok then p('d15 bot kill: ERROR', err) end
return table.concat(out, '\n')
