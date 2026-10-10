-- D15-live test probe: removes the Spirit
-- dummies dgsim_d11_dummies.lua made (every live enemy NPC within dgsim_d15_clear_radius, 40, of player 0's creature),
-- so a recall isn't cancelled by their hits and the next round's dummies start fresh. The dummies fight back once hit
-- (a hit of about 4.5 on the player every 1.5 s, which cancels the recall) and surround the shaper, so a walk away from
-- them doesn't get through (B1's first runs). Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local shaper = nPlayer.GetPlayerControlledObjectID(0)
  if shaper == nil or shaper == kObjIDNone then
    p('d15 clear: no shaper for player 0')
    return
  end
  local ids = nObjectManager.GetObjectsInRadius(nGameObject.GetPosition(shaper), dgsim_d15_clear_radius or 40,
    nGetObjectsSortType.Distance, nTargetType.EnemyNPCs, nGameObject.GetTeam(shaper), kObjIDNone, kObjIDNone, 0, 0)
  local n = 0
  for _, id in ipairs(ids or {}) do
    n = n + 1
    p('d15 clear:', tostring(id), nGameObject.GetAssetName(id), 'hp', nGameObject.GetHitPoints(id))
    nGameObject.MarkForDelete(id)
  end
  p('d15 clear: removed', n)
end)
if not ok then p('d15 clear: ERROR', err) end
return table.concat(out, '\n')
