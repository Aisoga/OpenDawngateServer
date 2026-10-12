-- D15b test fixture ([host] test_lua=<s>:dgsim_d15b_wall.lua; case R14): Fluidity's Spirit Walk
-- (UnitPassThrough, attribute 45) against the server's movers, which collide since D1k. First teleports player 0's
-- shaper to dgsim_d15b_wall_at ({60, -100}: D12e's open disc, no static obstacle near; false = stay where it is) and
-- keeps where it was in dgsim_d15b_home (dgsim_d15b_home.lua takes it back, to the shop). Then puts every allied
-- player's creature (bots standing still with bot_brain=0) in a wall across the way east of the shaper:
-- dgsim_d15b_wall_dx (5) units east, dgsim_d15b_wall_gap (1.2) units apart in y, one of them on the shaper's y. A move
-- command to a point east of the wall then goes straight through with Spirit Walk and around (or stops) without it;
-- the host's `commands: player 0 creature ... position` lines and dgsim_d15b_state.lua show the path.
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local ok, err = pcall(function()
  local me = nPlayer.GetPlayerControlledObjectID(0)
  if me == nil or me == kObjIDNone then
    p('d15b wall: no shaper for player 0')
    return
  end
  local team = nGameObject.GetTeam(me)
  local at = dgsim_d15b_wall_at == nil and {60.0, -100.0} or dgsim_d15b_wall_at
  if at then
    local was = nGameObject.GetPosition(me)
    dgsim_d15b_home = {was.x, was.y}
    nLocomotion.TeleportToPositionAndFacing(me, nVector.create(at[1], at[2], 0), nVector.create(1, 0, 0))
    p('d15b wall: player 0 from', f1(was.x), f1(was.y), 'to', f1(at[1]), f1(at[2]))
  end
  local mp = nGameObject.GetPosition(me)
  local dx = dgsim_d15b_wall_dx or 5.0
  local gap = dgsim_d15b_wall_gap or 1.2
  local allies = {}
  for i = 1, 9 do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if id ~= nil and id ~= kObjIDNone and nGameObject.IsAlive(id) and nGameObject.GetTeam(id) == team then
      allies[#allies + 1] = id
    end
  end
  for k, id in ipairs(allies) do
    local y = mp.y + (k - 1 - math.floor(#allies / 2)) * gap   -- one ally right on the way (y = the shaper's)
    nLocomotion.TeleportToPositionAndFacing(id, nVector.create(mp.x + dx, y, 0), nVector.create(-1, 0, 0))
    local at = nGameObject.GetPosition(id)
    p('d15b wall: ally', tostring(id), nGameObject.GetAssetName(id), 'at', f1(at.x), f1(at.y))
  end
  p('d15b wall:', #allies, 'allies', f1(dx), 'units east of player 0 at', f1(mp.x), f1(mp.y), 'UnitPassThrough',
    f1(nAttribute.GetAttributeValue(me, nAttributeType.UnitPassThrough)), 'match time',
    string.format('%.2f', nGameSimulator.GetMatchTime()))
end)
if not ok then p('d15b wall: ERROR', err) end
return table.concat(out, '\n')
