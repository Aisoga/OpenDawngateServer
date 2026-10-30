-- MAPED-TEX live probe ([host] test_lua=<from>-<to>/<step>:dgsim_mapedtex.lua): each call teleports player 0's shaper
-- to the next spot of dgsim_mapedtex_spots (default: CM_Ring's baked-ground features, all in team 1's half), which
-- snaps the camera there (or a locked camera follows), so a picture taken a few seconds later frames that spot; after the
-- last spot the list starts again. Report only: one line per call.
local out = {}
local function p(s) out[#out + 1] = s end

local spots = dgsim_mapedtex_spots or {
  {-60.0, -142.0, 'the well, its path and the stamped disc'},
  {-172.0, -68.0, 'lane 1 path, the tombstone decal 8 m north'},
  {-179.0, -40.0, 'the disc decal over the lane west edge'},
  {-174.0, -5.0, 'the tiles edge y = 0, south of it'},
  {-174.0, 5.0, 'the tiles edge y = 0, north of it'},
  {-5.0, -174.0, 'the tiles edge x = 0, west of it'},
  {5.0, -174.0, 'the tiles edge x = 0, east of it'},
  {-165.0, -165.0, 'the base plaza'},
  {-176.0, -12.0, 'the moss patch on lane 1'},
  {-168.0, 60.0, 'lane 1 between cliffs'},
}

local ok, err = pcall(function()
  dgsim_mapedtex_n = (dgsim_mapedtex_n or 0) + 1
  local n = dgsim_mapedtex_n
  local t = nGameSimulator.GetMatchTime()
  local s = spots[(n - 1) % #spots + 1] -- the list again after its end (a second pass)
  if not s then
    p(string.format('mapedtex spot %d at match %.2f: no spot', n, t))
    return
  end
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p(string.format('mapedtex spot %d at match %.2f: no shaper for player 0', n, t))
    return
  end
  nLocomotion.TeleportToPositionAndFacing(h, nVector.create(s[1], s[2], 0), nVector.create(0, 1, 0))
  local at = nGameObject.GetPosition(h)
  p(string.format('mapedtex spot %d at match %.2f: player 0 at (%.1f %.1f): %s', n, t, at.x, at.y, s[3]))
end)
if not ok then p('mapedtex: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
