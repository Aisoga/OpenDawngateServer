-- D13-live probe ([host] test_lua=<s>:dgsim_d13_roles.lua, any number of times; docs/DAMAGE.md *Combat edge cases
-- (D13)*, the roles' passives): one line per player with its shaper, role (nPlayer.GetPlayerRole: 0 Gladiator,
-- 1 Tactician, 2 Hunter, 3 Predator), attack style, which modifier_Role_* it holds (the host's [host] role_passives
-- activates them at the deploy and each respawn), the Gladiator's stored-last-hit counter, its available and total gold.
-- Compare two sessions with role_passives=1 and 0 at the same match times. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ROLES = {'modifier_Role_Gladiator', 'modifier_Role_Tactician', 'modifier_Role_Hunter', 'modifier_Role_Predator'}

local ok, err = pcall(function()
  p('d13 roles at match', string.format('%.1f', nGameSimulator.GetMatchTime()))
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone then
      local held = {}
      for _, n in ipairs(ROLES) do
        local id = nModifier.GetFirstModifierByGUID(c, nUtil.SPID(n))
        if id ~= nil and id ~= kObjIDNone then held[#held + 1] = string.gsub(n, '^modifier_Role_', '') end
      end
      local counter = nModifier.GetFirstModifierByGUID(c, nUtil.SPID('modifier_Role_Gladiator_Counter'))
      local stacks = (counter ~= nil and counter ~= kObjIDNone) and nModifier.GetStackCount(counter) or 0
      p(string.format('d13 roles: player %s %s %s team %s role %s %s alive %s; role modifier %s; gladiator counter %s; '
        .. 'gold available %.0f total %.0f', tostring(pid), nPlayer.IsBot(c) and 'bot' or 'human', nGameObject.GetAssetName(c),
        tostring(nGameObject.GetTeam(c)), tostring(nPlayer.GetPlayerRole(pid)),
        nGameObject.IsMeleeAttackStyle(c) and 'melee' or 'ranged', tostring(nGameObject.IsAlive(c)),
        #held > 0 and table.concat(held, ',') or 'none', tostring(stacks), nPlayer.GetPlayerAvailableGold(pid),
        nPlayer.GetPlayerTotalGold(pid)))
    end
  end
end)
if not ok then p('d13 roles: ERROR', err) end
return table.concat(out, '\n')
