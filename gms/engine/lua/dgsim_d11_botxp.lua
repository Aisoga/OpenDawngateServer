-- D11 test probe ([host] test_lua=<s>:dgsim_d11_botxp.lua, run once per level wanted; docs/SERVER_MODE.md *Ability ranks
-- on the host and the client (D11)*): every bot player (indices dgsim_d11_bots, default 1-9) gets exactly the XP of its
-- next level (LevelUpTuning's thresholds) through nGameObject.GrantXP (nXPSourceType.Cheat). LabsPlayer_GrantXP ranks
-- one skill of the bot's BotClass.skillOrder per call (the entry of its new point total), within
-- LabsPlayer_GetMaxAbilityRank's caps, so one level per run is how the game levels a bot. Assumes the bots had no XP of
-- their own (keep them in their base: [host] bot_brain=0). The host's "ranks:" lines show the results. Returns a report.
local xp = {0, 260, 610, 1050, 1580, 2200, 2910, 3710, 4600, 5580, 6650, 7810, 9060, 10400, 11830, 13350, 14960,
            16660, 18450, 20330}
dgsim_d11_botlevel = dgsim_d11_botlevel or 1
local out = {}
local ok, err = pcall(function()
  local level = dgsim_d11_botlevel
  if level >= #xp then
    out[#out + 1] = 'd11 bot xp: the bots are at level ' .. level .. ' already'
    return
  end
  local amount = xp[level + 1] - xp[level]
  local done = {}
  for _, i in ipairs(dgsim_d11_bots or {1, 2, 3, 4, 5, 6, 7, 8, 9}) do
    nGameObject.GrantXP(i, amount, false, nXPSourceType.Cheat)
    done[#done + 1] = tostring(i)
  end
  dgsim_d11_botlevel = level + 1
  out[#out + 1] = string.format('d11 bot xp: %d XP to bot(s) %s: level %d -> %d at match %.2f', amount,
    table.concat(done, ','), level, level + 1, nGameSimulator.GetMatchTime())
end)
if not ok then out[#out + 1] = 'd11 bot xp: ERROR ' .. tostring(err) end
return table.concat(out, '\n')
