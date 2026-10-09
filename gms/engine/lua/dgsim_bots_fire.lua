-- D6b test probe ([host] test_lua=<s>:dgsim_bots_fire.lua; AGENT_AI.md section 11.5 B5): the bot director's fire
-- tracker, switched on by the host with the bots (director +0x978). The Spirit Guardian "casts" a fire line through a
-- living Mortal bot and another through a living Spirit bot by the call its area abilities make
-- (nDirector.NotifyFireTrackerOfAbility: caster, ability, cast type Line, damage, descriptors, seconds, start, end,
-- width; ability_Guardian_FireWall's own arguments), then reports what the tracker answers:
--   - nBotHost.IsPositionSafe(team, pos) at each bot: unsafe for team 1 (a Spirit caster marks Mortal danger), safe
--     for team 2;
--   - nBot.ValidateHostileBotTarget(1, spiritBot, false, false) (with the fire check): false while the Spirit bot stands
--     in Mortal danger, true with skipFireCheck;
--   - the Mortal bot in the line gets ability_Bot_AvoidFireNP requested by FireTracker_RequestAvoidFire (the host's
--     request log and the bot's next moves show it).
-- Returns the report (logged by the host). Run it again 7 s later to see the cells expire (safe again).
local out = {}
local function p(s) out[#out + 1] = s end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local ok, err = pcall(function()
  local g = nBattlegrounds.GetGuardianOnTeam(nTeamID.Spirit)
  local mortal, spirit = nil, nil
  for i = 0, 9 do
    local c = nPlayer.GetPlayerControlledObjectID(i)
    if c ~= nil and c ~= kObjIDNone and nObjectManager.IsValidObject(c) and nPlayer.IsBot(c) and nGameObject.IsAlive(c) then
      if nGameObject.GetTeam(c) == nTeamID.Mortal and mortal == nil then mortal = c end
      if nGameObject.GetTeam(c) == nTeamID.Spirit and spirit == nil then spirit = c end
    end
  end
  p('fire probe at match ' .. f1(nGameSimulator.GetMatchTime()) .. ' s: Spirit Guardian ' .. tostring(g) .. ', Mortal bot ' ..
    tostring(mortal) .. ', Spirit bot ' .. tostring(spirit) .. ', nBotHost.IsPositionSafe ' ..
    tostring(type(nBotHost) == 'table' and type(nBotHost.IsPositionSafe) == 'function'))
  local function safe(team, pos) return tostring(nBotHost.IsPositionSafe(team, pos.x, pos.y, pos.z)) end
  local fresh = dgsim_bots_fire_marked == nil
  for _, b in ipairs({ mortal, spirit }) do
    if b ~= nil then
      local bp = nGameObject.GetPosition(b)
      local before = 'team 1 ' .. safe(1, bp) .. ', team 2 ' .. safe(2, bp)
      if fresh then
        local a = nVector.create(bp.x - 6, bp.y - 6, bp.z)
        local e = nVector.create(bp.x + 6, bp.y + 6, bp.z)
        nDirector.NotifyFireTrackerOfAbility(g, nUtil.SPID('ability_Guardian_FireWall'), nAbilityCastType.Line, 100, 0, 6, a,
                                             e, 4)
      end
      p(string.format('  %s %s at (%s, %s): safe before %s; after %s', tostring(nGameObject.GetAssetName(b)),
        tostring(nGameObject.GetTeam(b)), f1(bp.x), f1(bp.y), before,
        'team 1 ' .. safe(1, bp) .. ', team 2 ' .. safe(2, bp)))
    end
  end
  if spirit ~= nil then
    p('  ValidateHostileBotTarget(1, Spirit bot, false, false) = ' ..
      tostring(nBot.ValidateHostileBotTarget(1, spirit, false, false)) .. ', with skipFireCheck = ' ..
      tostring(nBot.ValidateHostileBotTarget(1, spirit, false, true)))
  end
  dgsim_bots_fire_marked = true
end)
if not ok then p('fire probe: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
