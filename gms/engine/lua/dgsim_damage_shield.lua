-- D1f-live test probe, part 1 of DAMAGE.md T7 ([host] test_lua=<s>:dgsim_damage_shield.lua, then
-- dgsim_damage_shield_hit.lua about 1 s later, within the shield's 3 s): a Mortal melee minion on the bottom lane
-- (out of the towers' reach, before the first wave) gets modifier_Alpha_Q_Shield rank 1 (50 + 0.8 x its Power 0). The
-- host's shield totals (damage.cpp) then set its mShieldHealth to 50 (log line "damage: shield ..."). Its id goes into
-- the global dgsim_t7_id. Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Mortal_Melee.Noun'), nTeamID.Mortal,
    nVector.create(66.0, -71.0, 0), kZeroVector, 1)
  dgsim_t7_id = m
  if not m then
    p('T7: minion not created')
    return
  end
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(m, kObjIDNone)
  local r = nModifier.RequestModifier(m, m, nUtil.SPID('modifier_Alpha_Q_Shield'), snap, 1, kZeroVector, nil)
  nAttribute.RemoveAttributeSnapshot(snap)
  p('T7: Mortal minion', tostring(m), 'hp', nGameObject.GetHitPoints(m), 'power',
    nAttribute.GetAttributeValue(m, nAttributeType.Power), 'RequestModifier(modifier_Alpha_Q_Shield, rank 1) ->', tostring(r))
end)
if not ok then p('T7: ERROR', err) end
return table.concat(out, '\n')
