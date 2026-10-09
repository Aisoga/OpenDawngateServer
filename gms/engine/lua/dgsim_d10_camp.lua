-- D10 test probe ([host] test_lua=<s>:dgsim_d10_camp.lua): the human player's shaper pulls the jungle camp nearest to
-- it (within 30 units): one hit of 10 with the shaper's own snapshot (nGameObject.TakeDamage through the host's damage
-- core, whose damage hook puts the attacker on the creep's aggro list; the camp's call for help pulls the others), so
-- the camp's creeps attack the shaper in front of the camera and their attacks can be checked on the client's screen
-- (SERVER_MODE.md *Abilities and projectiles as the client shows them (D10)*). Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local shaper = nil
  local ids = nPlayer.GetPlayerIds()
  for _, pid in ipairs(ids or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)  -- nPlayer.IsBot takes the creature, not the player id (D13-live)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then
      shaper = c
      break
    end
  end
  if shaper == nil or shaper == kObjIDNone then
    p('d10 camp: no human player creature')
    return
  end
  local sp = nGameObject.GetPosition(shaper)
  local best, bestD = nil, 30
  local seen = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      if string.find(name, '^Creep_Jungle_') and nGameObject.IsAlive(id) then
        local pos = nGameObject.GetPosition(id)
        local d = math.sqrt((pos.x - sp.x) ^ 2 + (pos.y - sp.y) ^ 2)
        if d < bestD then best, bestD = id, d end
      end
    end
  end
  if best == nil then
    p('d10 camp: no living creep within 30 of the shaper', tostring(shaper), string.format('(%.1f %.1f)', sp.x, sp.y))
    return
  end
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(shaper, kObjIDNone)
  local success, dealt = nGameObject.TakeDamage(snap, best, 10, 0, nDescriptors.IsBasic, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  p('d10 camp: shaper', tostring(shaper), 'hit', tostring(best), nGameObject.GetAssetName(best), string.format('%.1f', bestD),
    'units away: success', tostring(success), 'dealt', tostring(dealt), 'best target now', tostring(nAgent.GetBestTarget(best)))
end)
if not ok then p('d10 camp: ERROR', err) end
return table.concat(out, '\n')
