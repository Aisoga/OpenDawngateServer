-- D6b-shims test probe ([host] test_lua=<s>:dgsim_bots_report.lua, after <s>:dgsim_bots_api.lua): what the bots' Lua
-- API sees of the match (dgsim_bots.Report: the nBotHost functions present, the structures and wells found, towers and
-- outer towers per team and lane, vulnerable structures, wells owned and unlocked, the lane fronts, every player's lane
-- and mission, the call counts and the mission driver's errors). With the global dgsim_bots_tick set (by another
-- chunk) it first runs one dgsim_bots.MissionTick. Returns the report (logged by the host).
if type(dgsim_bots) ~= 'table' or type(dgsim_bots.Report) ~= 'function' then
  return 'dgsim_bots_report: dgsim_bots_api.lua is not loaded (test_lua it first)'
end
local out = {}
if dgsim_bots_tick then
  local ok, checks, ends, errors = pcall(dgsim_bots.MissionTick)
  if ok then
    out[#out + 1] = 'MissionTick: ' .. tostring(checks) .. ' checks, ' .. tostring(ends) .. ' ended, ' ..
                    tostring(errors) .. ' errors'
  else
    out[#out + 1] = 'MissionTick: ERROR ' .. tostring(checks)
  end
end
local ok, text = pcall(dgsim_bots.Report)
if not ok then text = 'dgsim_bots_report: ERROR ' .. tostring(text) end
out[#out + 1] = text
return table.concat(out, '\n')
