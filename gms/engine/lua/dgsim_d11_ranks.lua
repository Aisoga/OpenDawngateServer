-- D11 test probe ([host] test_lua=<s>:dgsim_d11_ranks.lua; docs/SERVER_MODE.md *Ability ranks on the host and the client
-- (D11)*): for player 0's creature, each of its four abilities' rank on the server (nAbility.GetAbilityRank) and
-- cooldown remaining (nAbility.GetCooldownRemaining). Run it right after a cast: the remaining cooldown is the ranked
-- cooldown less the time since the cast (dgsim_d11_reset.lua sets them to 0). dgsim_d11_abilities overrides the names
-- (default Fenmore's). Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local shaper = nPlayer.GetPlayerControlledObjectID(0)
  if shaper == nil or shaper == kObjIDNone then
    p('d11 ranks: no creature for player 0')
    return
  end
  local names = dgsim_d11_abilities or
      {'ability_Alpha_Q_SoulTear', 'ability_Alpha_W_Stim', 'ability_Alpha_E_HealWolf', 'ability_Alpha_R_WolfPack'}
  local parts = {}
  for i, n in ipairs(names) do
    local id = nUtil.SPID(n)
    local rank = nAbility.GetAbilityRank(shaper, id)
    local cd = nAbility.GetCooldownRemaining(shaper, id)
    parts[#parts + 1] = string.format('%s rank %s cooldown %.2f', ({'Q', 'W', 'E', 'R'})[i] or n, tostring(rank), cd or -1)
  end
  p('d11 ranks at match ' .. string.format('%.2f', nGameSimulator.GetMatchTime()) .. ' (hp ' ..
    string.format('%.1f', nGameObject.GetHitPoints(shaper)) .. ', power ' ..
    string.format('%.1f', nAttribute.GetAttributeValue(shaper, nAttributeType.Power)) .. '): ' ..
    table.concat(parts, '; '))
end)
if not ok then p('d11 ranks: ERROR', err) end
return table.concat(out, '\n')
