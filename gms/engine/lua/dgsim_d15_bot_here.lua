-- D15 test fixture ([host] test_lua=<s>:dgsim_d15_bot_here.lua; docs/ITEMS.md *The live batches*, batch B3): brings
-- the first live enemy player's creature (a bot; run with bots=1;bots_allies=0;bot_brain=0 so it stands still) to
-- dgsim_d15_bot_at ({-100, -88}: 12 units north of D11's dummies, outside a melee shaper's basic range from them) and
-- keeps its id in dgsim_d15_bot. Impulse's charge (modifier_Item_Legendary_Charge: an attack command on an enemy
-- shaper beyond melee range, 13.24 at most) and the kill passives (dgsim_d15_bot_kill.lua) need it. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function vec(v) if v == nil then return 'nil' end return string.format('(%.1f %.1f %.1f)', v.x, v.y, v.z) end

local ok, err = pcall(function()
  local me = nPlayer.GetPlayerControlledObjectID(0)
  if me == nil or me == kObjIDNone then
    p('d15 bot here: no shaper for player 0')
    return
  end
  local myTeam = nGameObject.GetTeam(me)
  local bot
  for i = 1, 9 do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if id ~= nil and id ~= kObjIDNone and nGameObject.GetTeam(id) ~= myTeam and nGameObject.IsAlive(id) then
      bot = id
      break
    end
  end
  if bot == nil then
    p('d15 bot here: no live enemy player (run with bots=1;bots_allies=0)')
    return
  end
  local at = dgsim_d15_bot_at or {-100.0, -88.0}
  local from = nGameObject.GetPosition(bot)
  nLocomotion.TeleportToPositionAndFacing(bot, nVector.create(at[1], at[2], 0), nVector.create(0, -1, 0))
  dgsim_d15_bot = bot
  p('d15 bot here:', tostring(bot), nGameObject.GetAssetName(bot), 'from', vec(from), 'to', vec(nGameObject.GetPosition(bot)),
    'hp', nGameObject.GetHitPoints(bot), 'distance to player 0', string.format('%.1f',
      nVector.distance(nGameObject.GetPosition(bot), nGameObject.GetPosition(me))))
end)
if not ok then p('d15 bot here: ERROR', err) end
return table.concat(out, '\n')
