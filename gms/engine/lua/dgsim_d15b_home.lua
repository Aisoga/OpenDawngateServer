-- D15b test fixture ([host] test_lua=<s>:dgsim_d15b_home.lua): takes player 0's shaper back to
-- where dgsim_d15b_wall.lua teleported it from (dgsim_d15b_home; the spawn by the shop when the wall came first), so
-- the next purchase is in the shop's reach without a walk or a recall. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local ok, err = pcall(function()
  local me = nPlayer.GetPlayerControlledObjectID(0)
  local home = dgsim_d15b_home
  if me == nil or me == kObjIDNone or home == nil then
    p('d15b home: no shaper for player 0 or no dgsim_d15b_home (run dgsim_d15b_wall.lua first)')
    return
  end
  local was = nGameObject.GetPosition(me)
  nLocomotion.TeleportToPositionAndFacing(me, nVector.create(home[1], home[2], 0), nVector.create(1, 0, 0))
  local now = nGameObject.GetPosition(me)
  p('d15b home: player 0 from', f1(was.x), f1(was.y), 'to', f1(now.x), f1(now.y), 'match time',
    string.format('%.2f', nGameSimulator.GetMatchTime()))
end)
if not ok then p('d15b home: ERROR', err) end
return table.concat(out, '\n')
