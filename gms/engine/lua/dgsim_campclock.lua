-- CAMPICONS step 2 probe ([host] test_lua=<s>:dgsim_campclock.lua, called up to seven times): the shipped world clock
-- jungle_respawn_clock_effect (Effects.package; named only by ability_Parasite_Death.timerEffect, which no function of
-- that script plays). Calls 1-6 each play one copy of the effect at the next slot of a 3 x 2 grid around player 0's
-- shaper (nEffect.CreateEffect(true, effect, none, position, zero, none, NPC, ...); the engine makes a placeable effect
-- object for it, LuaJobObjectWithGraphics), so one picture taken later shows the effect at six ages; call 7 destroys
-- them all. Each copy's creation time is in the report (the ages at a picture follow from the host log's times).
-- dgsim_campclock_attach = true plays the first copy attached to a timerObjectNoun object instead
-- (Ability_TEMPORARY_Guardian_LaserBlast_BeamObject), as the parasite's script would have.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local slots = {{-10, 6}, {0, 6}, {10, 6}, {-10, -6}, {0, -6}, {10, -6}}

local ok, err = pcall(function()
  dgsim_campclock_n = (dgsim_campclock_n or 0) + 1
  dgsim_campclock_fx = dgsim_campclock_fx or {}
  local n = dgsim_campclock_n
  local t = nGameSimulator.GetMatchTime()
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p('campclock', n, ': no shaper for player 0')
    return
  end
  if dgsim_campclock_home == nil then
    local at = nGameObject.GetPosition(h)
    dgsim_campclock_home = {at.x, at.y, at.z}
  end
  local home = dgsim_campclock_home
  local effect = nUtil.GetAsset('jungle_respawn_clock_effect.ServerEventDef')
  if n <= #slots then
    local pos = nVector.create(home[1] + slots[n][1], home[2] + slots[n][2], home[3])
    local carrier = kObjIDNone
    if n == 1 and dgsim_campclock_attach then
      carrier = nObjectManager.CreateObject(nUtil.GetAsset('Ability_TEMPORARY_Guardian_LaserBlast_BeamObject.Noun'),
        nTeamID.NPC, pos, kZeroVector, 1)
      dgsim_campclock_obj = carrier
    end
    local fx = nEffect.CreateEffect(true, effect, kObjIDNone, pos, kZeroVector, carrier, nTeamID.NPC, 0, kObjIDNone,
      kZeroVector, 0)
    dgsim_campclock_fx[#dgsim_campclock_fx + 1] = fx
    p(string.format('campclock %d at match %s: copy %d (%s) at (%.1f %.1f), slot (%d, %d) from player 0 at (%.1f %.1f)%s',
      n, f2(t), n, tostring(fx), pos.x, pos.y, slots[n][1], slots[n][2], home[1], home[2],
      carrier ~= kObjIDNone and (' on object ' .. tostring(carrier)) or ''))
  else
    for _, fx in ipairs(dgsim_campclock_fx) do nEffect.DestroyEffect(kObjIDNone, false, fx) end
    if dgsim_campclock_obj ~= nil then nGameObject.MarkForDelete(dgsim_campclock_obj) end
    p('campclock', n, 'at match', f2(t), ':', #dgsim_campclock_fx, 'copies destroyed')
    dgsim_campclock_fx = {}
  end
end)
if not ok then p('campclock: ERROR', err) end
return table.concat(out, '\n')
