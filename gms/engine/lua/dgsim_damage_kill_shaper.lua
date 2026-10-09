-- D1f-live test probe (DAMAGE.md *Death handling* 4, a player victim): the player's shaper killed with
-- nGameObject.KillObject(shaper, Binding_Spirit_Inner). Through the host's GameObject_Kill detour: OnNearbyDeath,
-- OnPlayerDeath {playerDeathVictim, playerDeathLastPlayerToHit, playerDeathAssistPlayers (empty), myKillerID} on every
-- player's creature, and the Player reward data (0 XP / 0 gold) split. Its respawn is D1g (the shaper stays dead).
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local shaper = nPlayer.GetPlayerControlledObjectID(0)
  if shaper == nil or shaper == kObjIDNone then
    p('kill shaper: no shaper for player 0')
    return
  end
  local killer
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if nGameObject.GetAssetName(id) == 'Binding_Spirit_Inner' then killer = id break end
  end
  local hp = nGameObject.GetHitPoints(shaper)
  nGameObject.KillObject(shaper, killer or kObjIDNone, false)
  p('kill shaper:', tostring(shaper), nGameObject.GetAssetName(shaper), 'hp', hp, '->', nGameObject.GetHitPoints(shaper),
    'killer', tostring(killer), 'alive', tostring(nGameObject.IsAlive(shaper)), 'at match time', nGameSimulator.GetMatchTime())
end)
if not ok then p('kill shaper: ERROR', err) end
return table.concat(out, '\n')
