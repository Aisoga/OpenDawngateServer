-- SAKFROST test probe (the respawn wake of passives.cpp's kWakeAtRespawn): player 0's shaper killed out of the base
-- and watched after its respawn: its resource (Sakari's frost) and Kensu's arrows (modifier_Savage_Basic_AmmoCounter's
-- stacks; 0 also when there is no counter, as for Sakari). Run it every 0.5 s from 3 s (test_lua=3-80/0.5:<this file>):
--   call 1 (3 s): the shaper is teleported out of the base zone (modifier_base_regen's 40 units), to (-120, -190);
--   call 29 (17 s): killed. For Sakari ([host] noun=0x413B4790) a commands_file ranks up her Q at 2 s
--     and casts it 8 times from 5 to 15.5 s (frost 10 -> 2), so her passive's Tick sees her dead within the 6.5 s regen
--     delay and sleeps; Kensu (noun=0xF60BF244) needs no commands (his reload sleeps once both arrows are in, and the
--     arrows' counter ends at the death);
--   the first call that finds it alive again: teleported out of the base zone at once (the fountain's 2 % per 0.1 s
--     would refill the frost otherwise).
-- Each call logs one line when the state, resource or arrows changed, or every 4th call.
local S = dgsim_sakfrost_state or { n = 0 }
dgsim_sakfrost_state = S
S.n = S.n + 1
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local OUT = nVector.create(-120, -190, 0)
local AMMO = nUtil.SPID('modifier_Savage_Basic_AmmoCounter')

local ok, err = pcall(function()
  local h = nPlayer.GetPlayerControlledObjectID(0)
  if h == nil or h == kObjIDNone then
    p('sakfrost', S.n, 'no creature for player 0')
    return
  end
  local alive = nGameObject.IsAlive(h)
  local note = ''
  if S.n == 1 then
    nLocomotion.TeleportToPositionAndFacing(h, OUT, nVector.create(1, 0, 0))
    note = 'teleported out of the base zone; asset ' .. tostring(nGameObject.GetAssetName(h))
  elseif S.n == 29 then
    nGameObject.KillObject(h, kObjIDNone, false)
    S.killed = nGameSimulator.GetMatchTime()
    alive = nGameObject.IsAlive(h)
    note = 'killed'
  elseif S.killed and not S.back and alive then
    S.back = nGameSimulator.GetMatchTime()
    nLocomotion.TeleportToPositionAndFacing(h, OUT, nVector.create(1, 0, 0))
    note = 'alive again (respawned): teleported out of the base zone'
  end
  local res = nGameObject.GetResourcePoints(h)
  local max = nGameObject.GetMaxResourcePoints(h)
  local arrows = '-'
  local ammo = nModifier.GetFirstModifierByGUID(h, AMMO)
  if ammo ~= nil then arrows = tostring(nModifier.GetStackCount(ammo)) end
  local key = tostring(alive) .. '/' .. string.format('%.2f', res) .. '/' .. arrows
  if note ~= '' or key ~= S.key or S.n % 4 == 0 then
    local pos = nGameObject.GetPosition(h)
    local since = ''
    if S.back then since = string.format(' (%.1f s after the respawn)', nGameSimulator.GetMatchTime() - S.back) end
    p(string.format('sakfrost %d: match %.2f s %s frost %.2f of %.0f arrows %s at (%.1f, %.1f)%s%s', S.n,
      nGameSimulator.GetMatchTime(), alive and 'alive' or 'dead', res, max, arrows, pos.x, pos.y, since,
      note ~= '' and ('; ' .. note) or ''))
  end
  S.key = key
end)
if not ok then p('sakfrost', S.n, 'ERROR', err) end
return table.concat(out, '\n')
