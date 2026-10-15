-- PARAMAP live probe for a bot match: reports the parasite and, at the first call that finds it alive and born
-- (HideHealthBar 0), kills it once with one true-damage hit from the human's snapshot, wherever the human stands (so
-- the minimap's centre is free of the human's own icon); later calls only report. The host's "director: camp icon"
-- lines show the icon's bits, the bots' "bots: team" lines their parasite calls.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end
local function attr(o, name)
  local ok, v = pcall(nAttribute.GetAttributeValue, o, nAttributeType[name])
  if ok then return f2(v) end
  return '?'
end

local ok, err = pcall(function()
  local h
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    -- nPlayer.IsBot takes an object (0x00947660), not a player id
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then h = c break end
  end
  dgsim_paramap_kill = (dgsim_paramap_kill or 0) + 1
  local part = dgsim_paramap_kill
  local o = nDirector.GetParasiteID()
  local t = nGameSimulator.GetMatchTime()
  if not o or o == kObjIDNone then
    p('paramap kill', part, 'at match', f2(t), ': no parasite')
    return
  end
  p(string.format('paramap kill %d at match %s: %s %s rank %s alive %s hp %s/%s HideHealthBar %s Untargetable %s inCombat %s',
    part, f2(t), tostring(o), tostring(nGameObject.GetAssetName(o)), tostring(nGameObject.GetNPCRank(o)),
    tostring(nGameObject.IsAlive(o)), f2(nGameObject.GetHitPoints(o)), f2(nGameObject.GetMaxHitPoints(o)),
    attr(o, 'HideHealthBar'), attr(o, 'Untargetable'), tostring(nGameObject.IsInCombat(o))))
  local born = attr(o, 'HideHealthBar') == '0.00'
  if not dgsim_paramap_killed and born and h and nGameObject.IsAlive(o) then
    dgsim_paramap_killed = true
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(h, kObjIDNone)
    local s, dealt = nGameObject.TakeDamage(snap, o, nGameObject.GetHitPoints(o) + 1000, 0, nDescriptors.IsTrueDamage,
      kZeroVector)
    nAttribute.RemoveAttributeSnapshot(snap)
    p('paramap kill', part, ': hit', tostring(s), 'dealt', f2(dealt), 'alive', tostring(nGameObject.IsAlive(o)))
  end
end)
if not ok then p('paramap kill: ERROR', err) end
return table.concat(out, '\n')
