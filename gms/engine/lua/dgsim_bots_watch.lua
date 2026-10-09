-- D6a test probe ([host] test_lua=<s>:dgsim_bots_watch.lua, any number of times): every player's creature as the
-- server sees it: noun, team, bot or not, position, HP, level, its lane (nBot.GetLaneID) and closest lane, the agent's
-- best target (for a bot its mTarget) and that target's noun; then the lane fronts of dgsim_bots.LaneCenter. Returns the
-- report (logged by the host).
local out = {}
local function p(s) out[#out + 1] = s end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local ok, err = pcall(function()
  p('bots watch at match ' .. f1(nGameSimulator.GetMatchTime()) .. ' s')
  for i = 0, 9 do
    local c = nPlayer.GetPlayerControlledObjectID(i)
    if c ~= nil and c ~= kObjIDNone and nObjectManager.IsValidObject(c) then
      local pos = nGameObject.GetPosition(c)
      local t = nAgent.GetBestTarget(c)
      local tname = 'none'
      if t ~= nil and t ~= kObjIDNone and nObjectManager.IsValidObject(t) then tname = tostring(nGameObject.GetAssetName(t)) end
      local lane, closest = '-', '-'
      if nBot and nBot.GetLaneID then lane = tostring(nBot.GetLaneID(c)) end
      if nBot and nBot.GetClosestLane then closest = tostring(nBot.GetClosestLane(c)) end
      p(string.format('  player %d %-18s team %s bot %-5s at (%7.1f, %7.1f) hp %6s/%-6s level %s lane %s closest %s target %s',
        i, tostring(nGameObject.GetAssetName(c)), tostring(nGameObject.GetTeam(c)), tostring(nPlayer.IsBot(c)), pos.x, pos.y,
        f1(nGameObject.GetHitPoints(c)), f1(nGameObject.GetMaxHitPoints(c)), tostring(nGameObject.GetPlayerLevel(c)), lane,
        closest, tname))
    end
  end
  if type(dgsim_bots) == 'table' and dgsim_bots.LaneCenter then
    local s = '  fronts:'
    for lane = 1, 2 do
      for team = 1, 2 do
        local cpos, d = dgsim_bots.LaneCenter(lane, team)
        s = s .. string.format(' L%dT%d (%.0f, %.0f) dir (%.2f, %.2f);', lane, team, cpos.x, cpos.y, d.x, d.y)
      end
    end
    p(s)
  end
end)
if not ok then p('bots watch: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
