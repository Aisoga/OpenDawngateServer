-- LEASH live probe: puts the human onto the Spirit well's platform for a worker kite.
-- [host] test_lua=<s>:dgsim_leash_well.lua teleports the human's creature to (172.5, -45.2), 20 units south-west of the
-- well's centre (182.5, -27.9) among its south-west worker spots (about 22 out); the commands file then stops it,
-- attacks the nearest worker and walks off the platform. Run dgsim_leash_report.lua afterwards for the workers' state.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function xy(v) if not v then return 'nil' end return string.format('(%.1f, %.1f)', v.x, v.y) end

local ok, err = pcall(function()
  local me
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then me = c end
  end
  if not me then
    p('leash well: no human player creature')
    return
  end
  local spot = nVector.create(172.5, -45.2, 0)
  local done = nLocomotion.TeleportToPositionAndFacing(me, spot, nVector.create(0, 1, 0))
  p('leash well: human', tostring(me), 'teleported to', xy(spot), '->', tostring(done), 'now at',
    xy(nGameObject.GetPosition(me)), 'team', tostring(nGameObject.GetTeam(me)))
end)
if not ok then p('leash well: ERROR', tostring(err)) end
return table.concat(out, '\n')
