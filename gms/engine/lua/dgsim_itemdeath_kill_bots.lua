-- ITEMDEATH test probe ([host] test_lua=<s>:dgsim_itemdeath_kill_bots.lua): every live bot's creature (players 1-9
-- whose creature nPlayer.IsBot names a bot) killed with nGameObject.KillObject (no killer), so the host's death path
-- ends their OnAgentDeath item passives and its respawn (respawn_s) activates them again (the host's `items:` lines).
-- Prints each bot's creature, noun and HP. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local n = 0
  for i = 1, 9 do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if id ~= nil and id ~= kObjIDNone and nPlayer.IsBot(id) and nGameObject.IsAlive(id) then
      local hp = nGameObject.GetHitPoints(id)
      nGameObject.KillObject(id, kObjIDNone, false)
      p('itemdeath kill bots: player', i, tostring(id), nGameObject.GetAssetName(id), 'hp', hp, '->',
        nGameObject.GetHitPoints(id), 'alive', tostring(nGameObject.IsAlive(id)))
      n = n + 1
    end
  end
  p('itemdeath kill bots:', n, 'bots killed at match time', string.format('%.2f', nGameSimulator.GetMatchTime()))
end)
if not ok then p('itemdeath kill bots: ERROR', err) end
return table.concat(out, '\n')
