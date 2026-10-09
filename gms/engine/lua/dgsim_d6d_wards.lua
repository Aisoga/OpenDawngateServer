-- D6d test probe ([host] test_lua=<s>:dgsim_d6d_wards.lua, any number of times; docs/AGENT_AI.md *D6d*): the ward
-- objects on the server now. Walks the live objects (nObjectManager.GetObjectIdByIndex, stopping at the first slot
-- that returns none) and lists every one whose nGameObject.GetNPCType is nNPCType.Ward: its id, asset, team, position
-- and the nearest of condition_Bot_Ward's 18 ward spots (Conditions!condition_Bot_Ward.lua's vectors; each side's
-- table holds 11 of them; name and distance), then the count per team.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local kSpots = {
  {'MortalTopBaseBush', -117, -99}, {'MortalTopWell', -143, 4}, {'MortalTopShroom', -78, -20},
  {'SpiritTopTower', -4, 85}, {'SpiritTopWell', -4, 143}, {'SpiritTopBaseBush', 98, 119},
  {'MortalBotBaseBush', -99, -117}, {'MortalBotWell', 4, -143}, {'MortalBotTower', 9, -87},
  {'SpiritMidShroom', 78, 20}, {'SpiritBotWell', 143, -4}, {'SpiritBotBaseBush', 117, 99},
  {'MortalMidJungle', -88, -85}, {'SpiritMidJungle', 85, 88}, {'MiddleTopOuter', -125, 127},
  {'SpiritTopMid', -21, 38}, {'MortalBotMid', 21, -38}, {'MiddleBotOuter', 136, -116},
}

local ok, err = pcall(function()
  local spots = {}
  for _, s in ipairs(kSpots) do spots[#spots + 1] = {name = s[1], pos = nVector.create(s[2], s[3], 0)} end
  local seen, wards, perTeam = {}, 0, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      if nGameObject.GetNPCType(id) == nNPCType.Ward then
        wards = wards + 1
        local team = nGameObject.GetTeam(id)
        perTeam[team] = (perTeam[team] or 0) + 1
        local pos = nGameObject.GetPosition(id)
        local best, bestD = 'none', -1
        for _, s in ipairs(spots) do
          local d = nVector.distance(pos, s.pos)
          if bestD < 0 or d < bestD then best, bestD = s.name, d end
        end
        p(string.format('d6d wards: %s %s team %d at (%.1f, %.1f), alive %s; nearest spot %s at %.1f', key,
          tostring(nGameObject.GetAssetName(id)), team, pos.x, pos.y, tostring(nGameObject.IsAlive(id)), best, bestD))
      end
    end
  end
  local teams = {}
  for team, n in pairs(perTeam) do teams[#teams + 1] = 'team ' .. tostring(team) .. ' ' .. tostring(n) end
  table.sort(teams)
  p(string.format('d6d wards: %d ward objects (%s); %d spots known', wards, table.concat(teams, ', '), #spots))
end)
if not ok then p('d6d wards: ERROR ' .. tostring(err)) end
return table.concat(out, '\n')
