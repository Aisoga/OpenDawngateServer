-- CAMPTIMER live probe ([host] test_lua=<from>-<to>/<step>:dgsim_camptimer.lua): clears two camps once each, as soon
-- as their creatures are up, with nGameObject.KillObject, so the director's "camp ... cleared ... respawn at" and
-- "spawn 2" lines time their respawn: team 1's small Fish camp (65.7, -132.2) (the noun's respawn 180 s, the marker's
-- 60 s) and team 1's Gold Pigs (Boar) camp (-175.8, 147.9) (300 s either way; first spawn the noun's 210 s, the
-- marker's 120 s). Every call also counts the living jungle creatures. Call it every few seconds from before the
-- small camps' first spawn (match 90 s) until after the Boar's (210 s). Every line starts with "camptimer".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local targets = {
  {key = 'fish', x = 65.7, y = -132.2, prefix = 'Creep_Jungle_Fish_'},
  {key = 'boar', x = -175.8, y = 147.9, prefix = 'Creep_Jungle_Boar_'},
}

local ok, err = pcall(function()
  dgsim_camptimer_done = dgsim_camptimer_done or {}
  local done = dgsim_camptimer_done
  local now = nGameSimulator.GetMatchTime()
  local seen, living = {}, 0
  local hits = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      if string.find(name, '^Creep_Jungle_') and nGameObject.IsAlive(id) then
        living = living + 1
        local pos = nGameObject.GetPosition(id)
        for _, t in ipairs(targets) do
          if not done[t.key] and pos and string.find(name, '^' .. t.prefix) and math.abs(pos.x - t.x) < 12 and
              math.abs(pos.y - t.y) < 12 then
            hits[t.key] = hits[t.key] or {}
            table.insert(hits[t.key], {id = id, key = key, name = name, pos = pos})
          end
        end
      end
    end
  end
  for _, t in ipairs(targets) do
    local list = hits[t.key]
    if list then
      for _, c in ipairs(list) do
        nGameObject.KillObject(c.id)
        p('camptimer: kill', t.key, c.name, c.key, string.format('(%.1f %.1f)', c.pos.x, c.pos.y))
      end
      done[t.key] = now
      p('camptimer: cleared', t.key, 'camp,', #list, 'creature(s), at match time', now)
    end
  end
  p('camptimer: match time', now, 'living jungle creatures', living, 'fish cleared at', done.fish or '-',
    'boar cleared at', done.boar or '-')
end)
if not ok then p('camptimer: ERROR', err) end
return table.concat(out, '\n')
