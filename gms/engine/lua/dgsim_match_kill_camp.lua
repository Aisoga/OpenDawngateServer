-- D1c-live test probe (MATCH_FLOW.md T8; [host] test_lua=<s>:dgsim_match_kill_camp.lua): kills every creature of one
-- small creep camp with nGameObject.KillObject, so the director sees the camp cleared and respawns it respawnTime s
-- later. Default: SpawnPoint_v5_CreepsFish at (65.7, -132.2) (marker 4018962649, respawn 60 s); dgsim_camp_x/_y and
-- dgsim_camp_prefix choose another. Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local cx, cy = dgsim_camp_x or 65.7, dgsim_camp_y or -132.2
  local prefix = dgsim_camp_prefix or 'Creep_Jungle_Fish_'
  local killed = 0
  local seen = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      local pos = nGameObject.GetPosition(id)
      if string.find(name, '^' .. prefix) and pos and math.abs(pos.x - cx) < 12 and math.abs(pos.y - cy) < 12 and
          nGameObject.IsAlive(id) then
        local hp = nGameObject.GetHitPoints(id)
        nGameObject.KillObject(id)
        killed = killed + 1
        p('kill camp:', name, key, string.format('(%.1f %.1f)', pos.x, pos.y), 'hp', hp, '->', nGameObject.GetHitPoints(id))
      end
    end
  end
  p('kill camp: killed', killed, 'near', cx, cy, 'at match time', nGameSimulator.GetMatchTime())
end)
if not ok then p('kill camp: ERROR', err) end
return table.concat(out, '\n')
