-- MAPED-PHYS probe ([host] test_lua=<from>-<to>/0.2:dgsim_mapedphys_track.lua around a cast): one line with the
-- human's position and every live object whose noun starts with "Ability_Savage_Ult" (Kensu's R arrow in flight; D12b's
-- track), so the steps show where the arrow ends: at its range (it flew through) or at a wall. Moves nothing.
local out = {}
local ok, err = pcall(function()
  local function vec(v)
    if v == nil then return 'nil' end
    return string.format('(%.1f, %.1f, %.2f)', v.x, v.y, v.z)
  end
  local h = nPlayer.GetPlayerControlledObjectID(0)
  local hp = 'none'
  if h ~= nil and h ~= kObjIDNone and nObjectManager.IsValidObject(h) then hp = vec(nGameObject.GetPosition(h)) end
  local seen, found = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      if string.sub(name, 1, 18) == 'Ability_Savage_Ult' then
        found[#found + 1] = name .. ' ' .. key .. ' ' .. vec(nGameObject.GetPosition(id))
      end
    end
  end
  out[#out + 1] = 'mapedphys ' .. string.format('%.2f', nGameSimulator.GetMatchTime()) .. ' track: human ' .. hp ..
    '; ' .. (#found > 0 and table.concat(found, '; ') or 'no arrow')
end)
if not ok then out[#out + 1] = 'mapedphys ERROR ' .. tostring(err) end
return table.concat(out, '\n')
