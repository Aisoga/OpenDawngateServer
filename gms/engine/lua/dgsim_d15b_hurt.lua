-- D15b test probe ([host] test_lua=<s>:dgsim_d15b_hurt.lua): player 0's shaper loses
-- dgsim_d15b_hurt_frac (0.5) of its maximum health as true damage from the snapshot of an enemy melee minion created
-- beside it and marked for delete at once (so nothing fights back), so the heals that only show on a hurt shaper
-- (Passion's ability lifedrain, the last-hit heals of Life, Vigor, Prosperity and Rampancy, Assimilation's kill heal)
-- can be read from its HP (dgsim_d15b_state.lua, the reports). Run it outside the base zone (it heals). Returns a
-- report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local ok, err = pcall(function()
  local me = nPlayer.GetPlayerControlledObjectID(0)
  if me == nil or me == kObjIDNone then
    p('d15b hurt: no shaper for player 0')
    return
  end
  local enemy = nGameObject.GetTeam(me) == nTeamID.Mortal and 'Spirit' or 'Mortal'
  local mp = nGameObject.GetPosition(me)
  local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_' .. enemy .. '_Melee.Noun'), nTeamID[enemy],
    nVector.create(mp.x + 6, mp.y + 6, 0), kZeroVector, 1)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(m, kObjIDNone)
  local hp0 = nGameObject.GetHitPoints(me)
  local amount = nGameObject.GetMaxHitPoints(me) * (dgsim_d15b_hurt_frac or 0.5)
  local s, d = nGameObject.TakeDamage(snap, me, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  nGameObject.MarkForDelete(m)
  p('d15b hurt: player 0', tostring(me), 'true damage', f2(amount), 'dealt', f2(d), 'hp', f2(hp0), '->',
    f2(nGameObject.GetHitPoints(me)), 'of', f2(nGameObject.GetMaxHitPoints(me)), 'at', f2(mp.x), f2(mp.y),
    'match time', string.format('%.2f', nGameSimulator.GetMatchTime()))
end)
if not ok then p('d15b hurt: ERROR', err) end
return table.concat(out, '\n')
