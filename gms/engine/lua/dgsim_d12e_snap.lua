-- D12e probe ([host] test_lua=<s>:dgsim_d12e_snap.lua, after dgsim_d12.lua and a plan that called dgsim_d12.init()):
-- a snapshot of the arena for positional checks of an ability's effects: the human's position, facing and the terrain's
-- height there, and every live object within 'radius' (default 45) units of the human except the arena's own
-- participants: asset name, id, position (x, y, z), distance to the human in the plane. The cast command is logged by
-- the host ('commands:' lines); this prints where the ability's objects (projectiles, effects, totems) were at the time.
local D = dgsim_d12
if D == nil or D.human == nil then return 'd12e snap: no arena' end
local radius = dgsim_d12e_radius or 45
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'd12e ' .. string.format('%.2f', nGameSimulator.GetMatchTime()) .. ' ' .. table.concat(t, ' ')
end
local function v(a) return string.format('(%.2f, %.2f, %.2f)', a.x, a.y, a.z) end
local ok, err = pcall(function()
  local h = D.human
  local hp = nGameObject.GetPosition(h)
  local th = nLocomotion.GetTerrainHeightAtPosition(nVector.create(hp.x, hp.y, 0))
  p('human', v(hp), 'terrain', string.format('%.3f', th))
  local skip = {}
  skip[tostring(h)] = true
  for _, c in ipairs(D.enemies or {}) do skip[tostring(c)] = true end
  for _, c in ipairs(D.allies or {}) do skip[tostring(c)] = true end
  for _, c in pairs(D.grid or {}) do skip[tostring(c)] = true end  -- D12e2: the hit map's dummies (dgsim_d12e_grid.lua)
  local seen = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      if not skip[key] and nObjectManager.IsValidObject(id) then
        local pos = nGameObject.GetPosition(id)
        local dx, dy = pos.x - hp.x, pos.y - hp.y
        local d = math.sqrt(dx * dx + dy * dy)
        if d <= radius then
          p('obj', nGameObject.GetAssetName(id) or '?', key, v(pos), 'dist', string.format('%.2f', d), 'dx', string.format('%.2f', dx), 'dy', string.format('%.2f', dy))
        end
      end
    end
  end
  for i, c in ipairs(D.enemies or {}) do
    if nObjectManager.IsValidObject(c) then p('enemy' .. i, v(nGameObject.GetPosition(c)), 'hp', string.format('%.0f', nGameObject.GetHitPoints(c))) end
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
