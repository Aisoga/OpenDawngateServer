-- MSTATS4-live2 live probe (crowd control and wards, gms/core/stats/cc.h; ridden along with MSTATS3-live2):
-- a ward killed by an enemy shaper would log the ward's `death:` line (NPC type 16). The seat lines carry no
-- killsWards/wardsDestroyed since WARDSTAT (a ward can't be hit in 854, so they could only be 0).
-- [host] test_lua=<s>:dgsim_mstats4_wardkill.lua, any number of times.
-- Each call: walks the live objects (as dgsim_d6d_wards.lua) for a living ward (nGameObject.GetNPCType == nNPCType.Ward)
-- with a living shaper of the other team; if none, places one (wardskin_default.Noun, ability_Item_Ward's default
-- object) beside a living shaper for the other team. The shaper's snapshot (its own player index) then hits the ward
-- with 5000 true damage (lethal). Returns a report (logged by the host).
-- In build 854 a ward has no combatant (its noun lacks hasCombatantComponent), so
-- the hit must report `success false dealt 0.00` and the host's `stats damage` line count it under `no combatant`;
-- a ward that dies here means the data or the host changed.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local function shapers()
  local list = {}
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and nGameObject.GetHitPoints(c) > 0 then
      list[#list + 1] = {id = c, pid = pid, team = nGameObject.GetTeam(c), name = tostring(nGameObject.GetAssetName(c))}
    end
  end
  return list
end

local function wards()
  local list, seen = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      if nGameObject.GetNPCType(id) == nNPCType.Ward and nGameObject.GetHitPoints(id) > 0 then
        list[#list + 1] = id
      end
    end
  end
  return list
end

local ok, err = pcall(function()
  local now = nGameSimulator.GetMatchTime()
  local list = shapers()
  local ward, killer, how
  local ws = wards()
  for _, w in ipairs(ws) do
    for _, s in ipairs(list) do
      if s.team ~= nGameObject.GetTeam(w) then ward, killer, how = w, s, 'a placed ward' break end
    end
    if ward then break end
  end
  if not ward then
    local owner = list[1]
    for _, s in ipairs(list) do
      if owner and s.team ~= owner.team then killer = s break end
    end
    if not owner or not killer then
      p('mstats4 wardkill at', f2(now), ': no shapers of both teams alive')
      return
    end
    local pos = nGameObject.GetPosition(killer.id)
    ward = nObjectManager.CreateObject(nUtil.GetAsset('wardskin_default.Noun'), owner.team, pos, kIdentityQuaternion, 1)
    how = 'made by the probe for team ' .. tostring(owner.team)
    if ward == nil or ward == kObjIDNone then
      p('mstats4 wardkill at', f2(now), ': ward not created')
      return
    end
  end
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(killer.id, kObjIDNone)
  local hp = nGameObject.GetHitPoints(ward)
  local s, dealt = nGameObject.TakeDamage(snap, ward, 5000, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  p(string.format('mstats4 wardkill at %s: ward %s %s (%s, team %s, NPC type %s; %d living wards) hit by %s %s (player %s, '
    .. 'team %s): 5000 true -> success %s dealt %s; hp %s -> %s', f2(now), tostring(ward),
    tostring(nGameObject.GetAssetName(ward)), how, tostring(nGameObject.GetTeam(ward)), tostring(nGameObject.GetNPCType(ward)),
    #ws, tostring(killer.id), killer.name, tostring(killer.pid), tostring(killer.team), tostring(s), f2(dealt), f2(hp),
    f2(nGameObject.GetHitPoints(ward))))
end)
if not ok then p('mstats4 wardkill: ERROR', err) end
return table.concat(out, '\n')
