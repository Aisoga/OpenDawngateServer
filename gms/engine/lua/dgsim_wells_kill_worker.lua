-- D9 test probe ([host] test_lua=<s>:dgsim_wells_kill_worker.lua): kills living spirit-well workers
-- (Titan_Node_Follower) with nGameObject.KillObject, so the host's death processing reports them to the wells
-- (a freed slot, refilled one period later) and pays the Worker reward. dgsim_kill_workers (2) sets how many.
-- Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local want = dgsim_kill_workers or 2
  local killed = 0
  local seen = {}
  for i = 0, 1023 do
    if killed >= want then break end
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      if string.find(name, '^Titan_Node_Follower') and nGameObject.IsAlive(id) then
        local pos = nGameObject.GetPosition(id)
        nGameObject.KillObject(id)
        killed = killed + 1
        p('kill worker:', name, key, 'team', nGameObject.GetTeam(id), string.format('(%.1f %.1f)', pos.x, pos.y))
      end
    end
  end
  p('kill worker: killed', killed, 'at match time', nGameSimulator.GetMatchTime())
end)
if not ok then p('kill worker: ERROR', err) end
return table.concat(out, '\n')
