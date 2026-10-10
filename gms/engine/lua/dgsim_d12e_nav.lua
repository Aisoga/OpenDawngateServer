-- D12e probe ([host] test_lua=<s>:dgsim_d12e_nav.lua, after dgsim_d12.lua): which points of the map the server's navigation
-- mesh covers, to find an open arena for positional ability checks. nLocomotion.GetClosestPosition(creature, point) returns
-- success and the nearest reachable point; a point is "open" when that is the point itself (within 0.3). Prints a map
-- (step 20, '#' not open, '.' open) and the centres where all points of a ring of radius 30 (8 points) and the centre are open.
local D = dgsim_d12
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'd12e ' .. table.concat(t, ' ')
end
local ok, err = pcall(function()
  local h = D.human
  local function open(x, y)
    local s, q = nLocomotion.GetClosestPosition(h, nVector.create(x, y, 0))
    if not s or q == nil then return false end
    local dx, dy = q.x - x, q.y - y
    return dx * dx + dy * dy < 0.09
  end
  local lo, hi, step = -280, 280, 20
  for y = hi, lo, -step do
    local row = {}
    for x = lo, hi, step do row[#row + 1] = open(x, y) and '.' or '#' end
    p(string.format('nav y %4d', y), table.concat(row))
  end
  local best = {}
  local R = 30
  for x = lo + R, hi - R, 10 do
    for y = lo + R, hi - R, 10 do
      local good = open(x, y)
      if good then
        for a = 0, 7 do
          local ang = a * math.pi / 4
          if not open(x + R * math.cos(ang), y + R * math.sin(ang)) then good = false break end
        end
      end
      if good then best[#best + 1] = string.format('(%d,%d)', x, y) end
    end
  end
  p('open centres (ring radius', R, '):', #best, table.concat(best, ' '))
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
