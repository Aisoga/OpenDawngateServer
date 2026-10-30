-- MAPED-IMPORT-ONCE live probe ([host] test_lua=<from>-<to>/<step>:dgsim_mapedonce.lua): each call teleports player
-- 0's shaper to the next spot on team 2's half where the level's own dressing differs most from a mirror of team 1's
-- half (25-m cells of the two placements compared), so the same recipe on the shipped level and on its imported
-- package (CM_Pass for MM_Map_v3, CM_Stand for SM_Map_v5) gives pictures at the same spots; the last spot is a team 1
-- reference. The map is told by the shaper's place at the first call (MM's spawn at (-205, -205), SM's at (-232,
-- -230)). Each call heals the shaper to full, so it lives through a visit to team 2's half. After the last spot the
-- list starts again. Report only: one line per call.
local out = {}
local function p(s) out[#out + 1] = s end

local lists = {
  mm = {
    {32.0, -9.0, 'the centre, east of the lane'},
    {104.0, 96.0, "team 2's Core A side, west of its base"},
    {15.0, 59.0, "north of the centre (team 2's half)"},
    {66.0, 110.0, "team 2's top lane approach"},
    {138.0, 104.0, "team 2's base, south of its Guardian"},
    {88.0, 138.0, "team 2's base, west of its Guardian"},
    {40.0, 83.0, "team 2's jungle north of the centre"},
    {-104.0, -96.0, "team 1's mirror of the second spot (reference)"},
  },
  sm = {
    {-92.0, 110.0, "team 2's top lane, west"},
    {32.0, 56.0, "team 2's jungle by the centre"},
    {123.0, -83.0, "team 2's bottom lane, south"},
    {4.0, 204.0, "team 2's river by its well"},
    {86.0, 39.0, "team 2's jungle east of the centre"},
    {143.0, 112.0, "team 2's base approach"},
    {183.0, -83.0, "team 2's bottom lane, east"},
    {-33.0, -57.0, "team 1's mirror of the second spot (reference)"},
  },
}

local ok, err = pcall(function()
  dgsim_mapedonce_n = (dgsim_mapedonce_n or 0) + 1
  local n = dgsim_mapedonce_n
  local t = nGameSimulator.GetMatchTime()
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p(string.format('mapedonce spot %d at match %.2f: no shaper for player 0', n, t))
    return
  end
  if dgsim_mapedonce_map == nil then
    local at = nGameObject.GetPosition(h)
    local dm = math.sqrt((at.x + 205) ^ 2 + (at.y + 205) ^ 2)
    local ds = math.sqrt((at.x + 232) ^ 2 + (at.y + 230) ^ 2)
    dgsim_mapedonce_map = dm <= ds and 'mm' or 'sm'
    p(string.format('mapedonce: player 0 starts at (%.1f %.1f): the %s spots', at.x, at.y, dgsim_mapedonce_map))
  end
  local spots = lists[dgsim_mapedonce_map]
  local s = spots[(n - 1) % #spots + 1]
  local hp, max = nGameObject.GetHitPoints(h), nGameObject.GetMaxHitPoints(h)
  if hp < max then nGameObject.HealDamage(nGameObject.GetAttributeSnapshot(h), h, max - hp + 1, 0, 0, false, false) end
  nLocomotion.TeleportToPositionAndFacing(h, nVector.create(s[1], s[2], 0), nVector.create(0, 1, 0))
  local at = nGameObject.GetPosition(h)
  p(string.format('mapedonce spot %d (%d of %d, %s) at match %.2f: player 0 at (%.1f %.1f), asked (%.1f %.1f), hp %.0f of %.0f: %s',
    n, (n - 1) % #spots + 1, #spots, dgsim_mapedonce_map, t, at.x, at.y, s[1], s[2], hp, max, s[3]))
end)
if not ok then p('mapedonce: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
