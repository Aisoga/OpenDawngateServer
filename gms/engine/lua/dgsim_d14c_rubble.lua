-- D14c picture probe: a destroyed Binding's rubble. [host] test_lua with this file at two
-- times (global dgsim_d14c_rubble_part counts the calls), recipe: 5 and 22 s after EnterGame, bots=0;director_waves=0:
--   1 (5 s)   kills the Spirit Bindings of the top lane (Outer, Middle, Inner: y > x) as dgsim_d5_clear_lane.lua does
--             (KillObject with the top lane's Binding_Mortal_Outer as killer): three Binding deaths in one frame, the
--             case that once flooded the audio pool;
--   2 (22 s)  the human's shaper 14 units from the lane's Outer Binding (toward the map centre), so the camera shows the
--             rubble; reports the Bindings' state (HP, alive, position).
-- Every line starts with "d14c rubble".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'd14c rubble ' .. table.concat(t, ' ')
end
local function all()
  local live, seen = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if not seen[tostring(id)] then seen[tostring(id)] = true; live[#live + 1] = id end
  end
  return live
end
local ok, err = pcall(function()
  dgsim_d14c_rubble_part = (dgsim_d14c_rubble_part or 0) + 1
  local live = all()
  if dgsim_d14c_rubble_part == 1 then
    local killer = kObjIDNone
    for _, id in ipairs(live) do
      local pos = nGameObject.GetPosition(id)
      if nGameObject.GetAssetName(id) == 'Binding_Mortal_Outer' and pos and pos.x < 0 then killer = id end
    end
    for _, id in ipairs(live) do
      local name = nGameObject.GetAssetName(id) or ''
      local pos = nGameObject.GetPosition(id)
      if string.find(name, '^Binding_Spirit_') and pos and pos.y > pos.x and nGameObject.IsAlive(id) then
        nGameObject.KillObject(id, killer, false)
        p('killed', name, tostring(id), string.format('(%.1f %.1f)', pos.x, pos.y), 'hp', nGameObject.GetHitPoints(id), 'match', nGameSimulator.GetMatchTime())
      end
    end
  else
    local human
    for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
      local c = nPlayer.GetPlayerControlledObjectID(pid)
      if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then human = c end
    end
    local target
    for _, id in ipairs(live) do
      local name = nGameObject.GetAssetName(id) or ''
      local pos = nGameObject.GetPosition(id)
      if string.find(name, '^Binding_Spirit_') and pos and pos.y > pos.x then
        p(name, tostring(id), string.format('(%.1f %.1f)', pos.x, pos.y), 'hp', nGameObject.GetHitPoints(id), 'alive', tostring(nGameObject.IsAlive(id)))
        if name == 'Binding_Spirit_Outer' then target = pos end
      end
    end
    if human and target then
      local d = math.sqrt(target.x ^ 2 + target.y ^ 2)
      nLocomotion.TeleportToPositionAndFacing(human, nVector.create(target.x - target.x / d * 14, target.y - target.y / d * 14, 0), nVector.create(1, 0, 0))
      p('human 14 units from the Outer Binding toward the centre')
    end
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
