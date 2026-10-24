-- CAMPREACH-live probe: a census of every living jungle creature, for long bot matches (no human near the camps).
-- [host] test_lua=<from>-<to>/<step>:dgsim_campreach_census.lua. One summary line per call: the creatures alive, in
-- combat, the farthest from its spot (and whether it fights), the creatures out of combat more than 3 from their spot
-- (walking home or stuck), and every pair of creatures whose centres are less than 1.0 apart ("overlap": one stands
-- in the other). Each such creature and pair also gets a line. Read with: Select-String host\1\dgsim.log 'census'.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function xy(v) if not v then return 'nil' end return string.format('(%.1f, %.1f)', v.x, v.y) end
local function dist(a, b) local dx, dy = a.x - b.x, a.y - b.y return math.sqrt(dx * dx + dy * dy) end

local ok, err = pcall(function()
  local list, seen = {}, {}
  for i = 0, 2047 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      if string.find(name, '^Creep_Jungle_') ~= nil and nGameObject.IsAlive(id) then
        local pos, home = nGameObject.GetPosition(id), nGameObject.GetInitialPosition(id)
        list[#list + 1] = { id = id, name = string.gsub(name, '^Creep_Jungle_', ''), pos = pos, home = home,
          away = dist(pos, home), fight = nGameObject.IsInCombat(id) and true or false }
      end
    end
  end
  local fighting, far, out3 = 0, nil, {}
  for _, c in ipairs(list) do
    if c.fight then fighting = fighting + 1 end
    if not far or c.away > far.away then far = c end
    if not c.fight and c.away > 3 then out3[#out3 + 1] = c end
  end
  local pairs_ = {}
  for i = 1, #list do
    for j = i + 1, #list do
      local d = dist(list[i].pos, list[j].pos)
      if d < 1.0 then pairs_[#pairs_ + 1] = { list[i], list[j], d } end
    end
  end
  p(string.format('campreach census: match time %.1f: creatures %d, in combat %d, farthest %s %s %.1f from its spot '
    .. '(%s), out of combat >3 from the spot %d, overlaps %d', nGameSimulator.GetMatchTime(), #list, fighting,
    far and far.name or '-', far and tostring(far.id) or '-', far and far.away or 0,
    far and (far.fight and 'fighting' or 'not fighting') or '-', #out3, #pairs_))
  for _, c in ipairs(out3) do
    p(string.format('campreach census:   away %s %s at %s, %.1f from its spot %s', c.name, tostring(c.id), xy(c.pos),
      c.away, xy(c.home)))
  end
  for _, q in ipairs(pairs_) do
    p(string.format('campreach census:   overlap %s %s at %s (%s) and %s %s at %s (%s): %.2f apart', q[1].name,
      tostring(q[1].id), xy(q[1].pos), q[1].fight and 'fighting' or 'idle', q[2].name, tostring(q[2].id),
      xy(q[2].pos), q[2].fight and 'fighting' or 'idle', q[3]))
  end
end)
if not ok then p('campreach census: ERROR', tostring(err)) end
return table.concat(out, '\n')
