-- D15b test fixture ([host] test_lua=<s>:dgsim_d15b_bot_walk.lua, several times): a moving
-- victim for the slows (Control, Inevitability, Equilibrium, Pursuit). The enemy bot dgsim_d15_bot_here.lua brought
-- (dgsim_d15_bot; bot_brain=0, so nothing else moves it) walks back and forth: each run sends it
-- (nLocomotion.MoveToPointExact) dgsim_d15b_walk_dist (24) units north of where it is, or south on every second run.
-- dgsim_d15b_state.lua runs measure the distance it walked per second, with and without the slows on it.
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local ok, err = pcall(function()
  local bot = dgsim_d15_bot
  if bot == nil or bot == kObjIDNone or not nGameObject.IsAlive(bot) then
    p('d15b bot walk: no live bot (run dgsim_d15_bot_here.lua first)')
    return
  end
  dgsim_d15b_walks = (dgsim_d15b_walks or 0) + 1
  local d = dgsim_d15b_walk_dist or 24.0
  if dgsim_d15b_walks % 2 == 0 then d = -d end
  local bp = nGameObject.GetPosition(bot)
  local to = nVector.create(bp.x, bp.y + d, 0)
  nLocomotion.MoveToPointExact(bot, to)
  p('d15b bot walk #' .. dgsim_d15b_walks, tostring(bot), nGameObject.GetAssetName(bot), 'from', f1(bp.x), f1(bp.y), 'to',
    f1(to.x), f1(to.y), 'movespeed', string.format('%.2f',
      nAttribute.GetDerivedAttributeValue(bot, nDerivedAttributeType.Movespeed)), 'at match time',
    string.format('%.2f', nGameSimulator.GetMatchTime()))
end)
if not ok then p('d15b bot walk: ERROR', err) end
return table.concat(out, '\n')
