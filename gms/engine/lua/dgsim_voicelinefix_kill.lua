-- VOICELINE-fix test probe ([host] test_lua=<s>:dgsim_voicelinefix_kill.lua): kills player 0's shaper and the first live allied bot's with
-- nGameObject.KillObject (no killer), so both respawn and the host's respawn: lines show who got PlayerRespawned (the
-- human's bit alone; nothing for the bot). Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local me = nPlayer.GetPlayerControlledObjectID(0)
  if me == nil or me == kObjIDNone then
    p('voicelinefix kill: no shaper for player 0')
    return
  end
  local ally, allyIndex
  for i = 1, 9 do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if id ~= nil and id ~= kObjIDNone and nGameObject.GetTeam(id) == nGameObject.GetTeam(me)
        and nGameObject.IsAlive(id) then
      ally, allyIndex = id, i
      break
    end
  end
  for _, v in ipairs({{0, me}, {allyIndex, ally}}) do
    local index, id = v[1], v[2]
    if id ~= nil and nGameObject.IsAlive(id) then
      local hp = nGameObject.GetHitPoints(id)
      nGameObject.KillObject(id, kObjIDNone, false)
      p('voicelinefix kill: player', index, tostring(id), nGameObject.GetAssetName(id), 'hp', hp, '->',
        nGameObject.GetHitPoints(id), 'alive', tostring(nGameObject.IsAlive(id)), 'at match time',
        string.format('%.2f', nGameSimulator.GetMatchTime()))
    elseif index ~= nil then
      p('voicelinefix kill: player', index, 'not alive; skipped')
    end
  end
  if ally == nil then p('voicelinefix kill: no live allied player') end
end)
if not ok then p('voicelinefix kill: ERROR', err) end
return table.concat(out, '\n')
