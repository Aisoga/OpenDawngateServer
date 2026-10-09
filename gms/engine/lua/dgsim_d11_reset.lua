-- D11 test probe ([host] test_lua=<s>:dgsim_d11_reset.lua): sets every remaining cooldown of player 0's four abilities to
-- 0 (nAbility.SetCooldownRemaining; a test aid, so the ultimate can be cast again at its next rank without waiting its
-- 110-120 s) and reports the values before and after. dgsim_d11_abilities overrides the names (default Fenmore's).
local out = {}
local ok, err = pcall(function()
  local shaper = nPlayer.GetPlayerControlledObjectID(0)
  if shaper == nil or shaper == kObjIDNone then
    out[#out + 1] = 'd11 reset: no creature for player 0'
    return
  end
  local names = dgsim_d11_abilities or
      {'ability_Alpha_Q_SoulTear', 'ability_Alpha_W_Stim', 'ability_Alpha_E_HealWolf', 'ability_Alpha_R_WolfPack'}
  local parts = {}
  for i, n in ipairs(names) do
    local id = nUtil.SPID(n)
    local before = nAbility.GetCooldownRemaining(shaper, id) or -1
    if before > 0 then nAbility.SetCooldownRemaining(shaper, 0, id) end
    parts[#parts + 1] = string.format('%s %.2f -> %.2f', ({'Q', 'W', 'E', 'R'})[i] or n, before,
      nAbility.GetCooldownRemaining(shaper, id) or -1)
  end
  out[#out + 1] = 'd11 reset at match ' .. string.format('%.2f', nGameSimulator.GetMatchTime()) .. ': cooldowns ' ..
      table.concat(parts, '; ')
end)
if not ok then out[#out + 1] = 'd11 reset: ERROR ' .. tostring(err) end
return table.concat(out, '\n')
