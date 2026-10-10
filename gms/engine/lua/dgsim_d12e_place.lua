-- D12e2 probe ([host] test_lua=<s>:dgsim_d12e_place.lua shortly before each targeted or formation cast, after
-- dgsim_d12.lua and a plan that called dgsim_d12.init()): the next entry of dgsim_d12e_place_plan (the k-th call uses the
-- k-th) places the enemy players.
--   a number d:  the first enemy bot stands d units north of the plan centre and the second 9 units east of it, so
--                {nearest:@enemyplayer} in the commands file names the first (targeted casts: walk-up checks);
--   a list {x1, y1, x2, y2, ...}: enemy k stands at (x_k, y_k) from the centre (a formation: a row across or along
--                the cast to see which positions the ability's shape hits).
-- The next dgsim_d12 reset step puts everybody back at their homes. Prints
-- "d12e <t> place <entry> <id>:<x>:<y> ..." with each placed enemy's id and actual position.
local D = dgsim_d12
if D == nil or D.human == nil then return 'd12e place: no arena' end
dgsim_d12e_place_k = (dgsim_d12e_place_k or 0) + 1
local d = (dgsim_d12e_place_plan or {})[dgsim_d12e_place_k]
if d == nil then return string.format('d12e %.2f place: no entry %d', nGameSimulator.GetMatchTime(), dgsim_d12e_place_k) end
local at = D.plan.at or {-100.0, -104.0}
local out = {}
local ok, err = pcall(function()
  local spots = {}
  if type(d) == 'table' then
    for i = 1, #d - 1, 2 do spots[#spots + 1] = {at[1] + d[i], at[2] + d[i + 1]} end
  else
    spots = {{at[1], at[2] + d}, {at[1] + 9, at[2] + d}}  -- north: the east side of the test arena has an obstacle near x +18..+24
  end
  for i = 1, #spots do
    local c = D.enemies[i]
    if c ~= nil and nObjectManager.IsValidObject(c) then
      local pos = nGameObject.GetPosition(c)
      nLocomotion.TeleportToPositionAndFacing(c, nVector.create(spots[i][1], spots[i][2], pos.z), nVector.create(-1, 0, 0))
      local np = nGameObject.GetPosition(c)
      out[#out + 1] = string.format('%s:%.1f:%.1f', (string.gsub(tostring(c), '^0x', '')), np.x, np.y)
    end
  end
end)
return string.format('d12e %.2f place %s %s %s', nGameSimulator.GetMatchTime(), type(d) == 'table' and 'formation' or tostring(d),
  table.concat(out, ' '), ok and '' or tostring(err))
