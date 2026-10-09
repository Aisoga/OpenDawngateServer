-- D6c test probe ([host] test_lua=<s>:dgsim_d6c_group.lua, with [host] bot_classes naming PC_Creature or PC_Sculptor):
-- ability_Bot_GroupInLane live. Only the Creature_Gladiator and Sculptor phases use it, as a gambit that waits for
-- condition_MatchTime(gameTime = 1200) (Push, BackToAction) or for condition_Bot_CheckForReinforcements in the enemy base
-- (GetReinforcements, WaitForReinforcements), so a short session never reaches it. The probe requests the ability for
-- every living Creature and Sculptor bot (nAbility.RequestAbility with force; it needs no agent) and reports each bot's
-- lane before; the Tick that runs is dgsim_bots_api.lua's fixed copy (fix-up 2), and dgsim_d6c_trace.lua (run before)
-- logs its start, its return and the lane after. Returns the report (logged by the host).
local out = {}
local function p(s) out[#out + 1] = s end
local ok, err = pcall(function()
  local guid = nUtil.SPID('ability_Bot_GroupInLane')
  local fixed = type(dgsim_bots) == 'table' and type(ability_Bot_GroupInLane) == 'table' and
                type(nAbilityFns) == 'table'
  p('d6c group probe at match ' .. string.format('%.1f', nGameSimulator.GetMatchTime()) .. ' s: fix-up 2 in place: ' ..
    tostring(fixed and dgsim_bots.fixups ~= nil and dgsim_bots.fixups.groupInLane == true))
  local n = 0
  for i = 0, 9 do
    local c = nPlayer.GetPlayerControlledObjectID(i)
    if c ~= nil and c ~= kObjIDNone and nObjectManager.IsValidObject(c) and nPlayer.IsBot(c) and nGameObject.IsAlive(c) then
      local name = tostring(nGameObject.GetAssetName(c))
      if string.find(name, 'creature') or string.find(name, 'sculptor') then
        n = n + 1
        local pos = nGameObject.GetPosition(c)
        local r1, r2 = nAbility.RequestAbility(guid, c, kObjIDNone, kZeroVector, 1, 0, nAbilitySlot.None, kObjIDNone, true,
                                               false, nil)
        p(string.format('  %s %s (team %s) at (%.1f, %.1f), lane %s: RequestAbility -> %s, %s', tostring(c), name,
          tostring(nGameObject.GetTeam(c)), pos.x, pos.y, tostring(nBot.GetLaneID(c)), tostring(r1), tostring(r2)))
      end
    end
  end
  if n == 0 then p('  no living Creature or Sculptor bot ([host] bot_classes)') end
end)
if not ok then p('d6c group probe: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
