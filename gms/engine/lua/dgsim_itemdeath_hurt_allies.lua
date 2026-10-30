-- ITEMDEATH test probe ([host] test_lua=<s>:dgsim_itemdeath_hurt_allies.lua): every live allied player's creature of
-- player 0 (the practice game's bots) above dgsim_itemdeath_hurt_frac (0.5) of its maximum health is brought down to
-- it by true damage from the snapshot of an enemy melee minion created beside player 0 and marked for delete at once,
-- so Purity's heal (modifier_Item_Advanced_HealNearbyAlly: the most hurt ally within 23.53 missing at least 30) has a
-- target; an ally already at or below it is left alone, so repeated calls never kill. Prints player 0's state (alive
-- or dead) and each ally's HP before and after. Run it outside the base zone (it heals). Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local ok, err = pcall(function()
  local me = nPlayer.GetPlayerControlledObjectID(0)
  if me == nil or me == kObjIDNone then
    p('itemdeath hurt allies: no shaper for player 0')
    return
  end
  local team = nGameObject.GetTeam(me)
  local enemy = team == nTeamID.Mortal and 'Spirit' or 'Mortal'
  local mp = nGameObject.GetPosition(me)
  local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_' .. enemy .. '_Melee.Noun'), nTeamID[enemy],
    nVector.create(mp.x + 6, mp.y + 6, 0), kZeroVector, 1)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(m, kObjIDNone)
  local n = 0
  for i = 1, 9 do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if id ~= nil and id ~= kObjIDNone and nGameObject.IsAlive(id) and nGameObject.GetTeam(id) == team then
      local hp0 = nGameObject.GetHitPoints(id)
      local amount = hp0 - nGameObject.GetMaxHitPoints(id) * (dgsim_itemdeath_hurt_frac or 0.5)
      if amount > 0 then
        nGameObject.TakeDamage(snap, id, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
      end
      local at = nGameObject.GetPosition(id)
      p('itemdeath hurt allies: ally', tostring(id), nGameObject.GetAssetName(id), 'hp', f1(hp0), '->',
        f1(nGameObject.GetHitPoints(id)), 'of', f1(nGameObject.GetMaxHitPoints(id)), 'distance',
        f1(nVector.distance(at, mp)))
      n = n + 1
    end
  end
  nAttribute.RemoveAttributeSnapshot(snap)
  nGameObject.MarkForDelete(m)
  p('itemdeath hurt allies:', n, 'allies hurt; player 0', tostring(me), 'alive', tostring(nGameObject.IsAlive(me)),
    'hp', f1(nGameObject.GetHitPoints(me)), 'at', f1(mp.x), f1(mp.y), 'match time',
    string.format('%.2f', nGameSimulator.GetMatchTime()))
end)
if not ok then p('itemdeath hurt allies: ERROR', err) end
return table.concat(out, '\n')
