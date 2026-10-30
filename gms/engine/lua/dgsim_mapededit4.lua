-- Painted-ground probe ([host] test_lua=<from>-<to>/<step>:dgsim_mapededit4.lua): each call teleports player 0's
-- shaper to the next spot of a test copy of CM_Ring whose ground was painted (a brush stroke, a layer masked by a
-- package picture, a decal on a slope, a stamp cut from a shipped texture), so a locked camera frames it and a
-- picture taken a few seconds later shows it; after the last spot the list starts again. Report only: one line a call.
local out = {}
local function p(s) out[#out + 1] = s end

local spots = {
  {-147.0, -132.0, 'the painted zigzag path, the eraser gap at (-140, -115)'},
  {-100.0, -170.0, 'the layer masked by the package PNG (a blue checker disc)'},
  {-50.0, -170.0, 'the decal over the slope at (-50, -160)'},
  {-15.0, -170.0, 'the stamp cut from the base plaza texture (rect) at (-15, -176)'},
}

local ok, err = pcall(function()
  dgsim_mapededit4_n = (dgsim_mapededit4_n or 0) + 1
  local n = dgsim_mapededit4_n
  local t = nGameSimulator.GetMatchTime()
  local s = spots[(n - 1) % #spots + 1]
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p(string.format('mapededit4 spot %d at match %.2f: no shaper for player 0', n, t))
    return
  end
  nLocomotion.TeleportToPositionAndFacing(h, nVector.create(s[1], s[2], 0), nVector.create(0, 1, 0))
  local at = nGameObject.GetPosition(h)
  p(string.format('mapededit4 spot %d at match %.2f: player 0 at (%.1f %.1f): %s', n, t, at.x, at.y, s[3]))
end)
if not ok then p('mapededit4: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
