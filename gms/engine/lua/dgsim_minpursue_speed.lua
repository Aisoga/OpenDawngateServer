-- MINPURSUE test probe ([host] test_lua=<s>:dgsim_minpursue_speed.lua,<s2>:dgsim_minpursue_speed.lua): player 0's
-- shaper faster than a lane minion. The first call adds MovementSpeedPercentIncrease (dgsim_minpursue_speed_pct, 0.8),
-- the next call removes it, and so on. Returns a report (the host logs it after "host: test_lua ...").
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local ok, err = pcall(function()
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p('minpursue speed: no shaper for player 0')
    return
  end
  if dgsim_minpursue_speed_mod then
    nAttribute.RemoveAttributeModifier(h, dgsim_minpursue_speed_mod)
    dgsim_minpursue_speed_mod = nil
    p('minpursue speed: boost off')
  else
    dgsim_minpursue_speed_mod = nAttribute.AddAttributeModifier(h, nAttributeType.MovementSpeedPercentIncrease,
      dgsim_minpursue_speed_pct or 0.8, nAttributeOp.kAdd)
    p('minpursue speed: boost on', tostring(dgsim_minpursue_speed_mod))
  end
  p('minpursue speed: MovementSpeed', string.format('%.2f', nAttribute.GetAttributeValue(h, nAttributeType.MovementSpeed)),
    'at match time', string.format('%.1f', nGameSimulator.GetMatchTime()))
end)
if not ok then p('minpursue speed: ERROR', err) end
return table.concat(out, '\n')
