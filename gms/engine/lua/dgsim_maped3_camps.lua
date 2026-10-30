-- MAPED3 live probe ([host] test_lua=<T>:dgsim_maped3_camps.lua,<T2>:...): the jungle of
-- a compiled map. Every call reports each living jungle creature (asset Creep_Jungle_*) and the parasite
-- (Objective_SpiritMonster_*), grouped by the camp it stands at (the nearest 15-unit cluster), with its team, HP and
-- place. The first call also kills every jungle creature in team 1's half (x + y < 0) with nGameObject.KillObject, so
-- the director sees those camps cleared and respawns them respawnTime s later ("director: camp ... cleared",
-- "spawn 2"); team 2's half is left to the bots. Call it after the camps' first spawn (match 90 s). Every line starts
-- with "maped3 camps". Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  dgsim_maped3_camps_call = (dgsim_maped3_camps_call or 0) + 1
  local call = dgsim_maped3_camps_call
  local now = nGameSimulator.GetMatchTime()
  local seen, creeps = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      if (string.find(name, '^Creep_Jungle_') or string.find(name, '^Objective_SpiritMonster')) and
          nGameObject.IsAlive(id) then
        creeps[#creeps + 1] = {id = id, name = name, pos = nGameObject.GetPosition(id),
          hp = nGameObject.GetHitPoints(id), team = nGameObject.GetTeam(id)}
      end
    end
  end
  -- the camps: clusters of creatures within 15 units of a cluster's first
  local camps = {}
  for _, c in ipairs(creeps) do
    local home
    for _, k in ipairs(camps) do
      if math.abs(k.x - c.pos.x) < 15 and math.abs(k.y - c.pos.y) < 15 then home = k break end
    end
    if not home then
      home = {x = c.pos.x, y = c.pos.y, list = {}}
      camps[#camps + 1] = home
    end
    home.list[#home.list + 1] = c
  end
  local killed = 0
  for n, k in ipairs(camps) do
    local names = {}
    for _, c in ipairs(k.list) do
      names[#names + 1] = string.format('%s %s hp %.0f team %s (%.1f, %.1f)', c.name, tostring(c.id), c.hp or -1,
        tostring(c.team), c.pos.x, c.pos.y)
    end
    p(string.format('maped3 camps call %d: camp %d at (%.1f, %.1f), %s half: %d alive: %s', call, n, k.x, k.y,
      (k.x + k.y < 0) and "team 1's" or "team 2's", #k.list, table.concat(names, '; ')))
    if call == 1 then
      for _, c in ipairs(k.list) do
        if c.pos.x + c.pos.y < 0 and string.find(c.name, '^Creep_Jungle_') then
          nGameObject.KillObject(c.id)
          killed = killed + 1
        end
      end
    end
  end
  p(string.format('maped3 camps call %d at match %.2f s: %d creatures alive in %d camps%s', call, now, #creeps, #camps,
    call == 1 and string.format("; killed %d in team 1's half", killed) or ''))
end)
if not ok then p('maped3 camps: ERROR', err) end
return table.concat(out, '\n')
