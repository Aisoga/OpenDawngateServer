-- MSTATS3-live2 live probe (damage and healing by source, gms/core/stats/sources.h): a shield one
-- shaper gives another of its team, absorbing a hit, so the end's `match: sources (MSTATS3)` line counts absorbing
-- shields and the giver's seatStats has shieldingDone. [host] test_lua=<from>-<to>/<step>:dgsim_mstats3_shield.lua,
-- <from+1>-<to+1>/<step>:dgsim_mstats3_shield.lua in a bot match with a Sculptor bot on a team (bot_tacticians=sculptor).
-- Each call does one of two parts:
--   cast (no cast pending, or the last one older than 4.5 s): every living Sculptor bot casts its W
--     (ability_Sculptor_W_Shield rank 1, forced: modifier_Sculptor_W_Shield, 65 + 0.35 Power, 5 s) on the nearest
--     living shaper of its team (not itself); out of the W's range 20 the modifier is requested directly with the
--     Sculptor as its caster (what the W's Tick does);
--   hit (about 1 s later): each target that holds the shield (else the modifier is requested now) takes 60 true damage
--     from an enemy minion's snapshot; expected absorbed up to the shield's value, the host's absorb step crediting the
--     Sculptor's seat (shieldingDone).
-- Returns a report (logged by the host).
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local SHIELD = 'modifier_Sculptor_W_Shield'
local W = 'ability_Sculptor_W_Shield'

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

local function nearestAlly(s, list)
  local best, bestD
  local pos = nGameObject.GetPosition(s.id)
  for _, o in ipairs(list) do
    if o.id ~= s.id and o.team == s.team then
      local d = nVector.distance(pos, nGameObject.GetPosition(o.id))
      if bestD == nil or d < bestD then best, bestD = o, d end
    end
  end
  return best, bestD
end

local function shieldOf(id)
  local m = nModifier.GetFirstModifierByGUID(id, nUtil.SPID(SHIELD))
  if m ~= nil and m ~= kObjIDNone then return m end
end

local function requestShield(target, caster)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(caster, kObjIDNone)
  local r = nModifier.RequestModifier(target, caster, nUtil.SPID(SHIELD), snap, 1, kZeroVector, nil)
  nAttribute.RemoveAttributeSnapshot(snap)
  return r
end

local function enemySnapshot(target)
  local noun, enemy = 'Minion_Spirit_Melee.Noun', nTeamID.Spirit
  if nGameObject.GetTeam(target) == nTeamID.Spirit then noun, enemy = 'Minion_Mortal_Melee.Noun', nTeamID.Mortal end
  local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), enemy, nVector.create(66.0, -71.0, 0), kZeroVector, 1)
  if not m then return nil, nil end
  return m, nAttribute.CreateAttributeSnapshot_FromObject(m, kObjIDNone)
end

local ok, err = pcall(function()
  local now = nGameSimulator.GetMatchTime()
  local pending = dgsim_ms3_pending
  if pending and now - pending.t < 4.5 then
    dgsim_ms3_pending = nil
    for _, pr in ipairs(pending.pairs) do
      if nGameObject.GetHitPoints(pr.target) > 0 and nGameObject.GetHitPoints(pr.giver) > 0 then
        local how = 'from the W'
        if not shieldOf(pr.target) then
          how = 'requested now (no shield from the W): ' .. tostring(requestShield(pr.target, pr.giver))
        end
        local m, snap = enemySnapshot(pr.target)
        if snap then
          local hp, pool = nGameObject.GetHitPoints(pr.target), nGameObject.GetResourcePoints(pr.target)
          local s, dealt, crit, absorbed = nGameObject.TakeDamage(snap, pr.target, 60, 0, nDescriptors.IsTrueDamage, kZeroVector)
          p(string.format('mstats3 shield hit at %s: giver %s %s -> target %s %s, shield %s; 60 true -> success %s dealt %s '
            .. 'absorbed %s; hp %s -> %s, pool %s -> %s', f2(now), tostring(pr.giver), pr.giverName, tostring(pr.target),
            pr.targetName, how, tostring(s), f2(dealt), f2(absorbed), f2(hp), f2(nGameObject.GetHitPoints(pr.target)),
            f2(pool), f2(nGameObject.GetResourcePoints(pr.target))))
          nAttribute.RemoveAttributeSnapshot(snap)
          nGameObject.MarkForDelete(m)
        else
          p('mstats3 shield hit: no enemy minion for the snapshot')
        end
      else
        p('mstats3 shield hit: giver or target dead', tostring(pr.giver), tostring(pr.target))
      end
    end
    return
  end
  local list = shapers()
  local pairs_ = {}
  for _, s in ipairs(list) do
    if string.find(string.lower(s.name), 'sculptor', 1, true) then
      local a, d = nearestAlly(s, list)
      if a then
        local how
        if d <= 20 then
          local r1, r2 = nAbility.RequestAbility(nUtil.SPID(W), s.id, a.id, kZeroVector, 1, 0, nAbilitySlot.None,
            kObjIDNone, true, false, nil)
          how = string.format('W cast (distance %s) -> %s %s', f2(d), tostring(r1), tostring(r2))
        else
          how = string.format('out of range (%s): modifier requested -> %s', f2(d), tostring(requestShield(a.id, s.id)))
        end
        pairs_[#pairs_ + 1] = {giver = s.id, giverName = s.name, target = a.id, targetName = a.name}
        p(string.format('mstats3 shield cast at %s: giver %s %s (player %s, team %s) -> ally %s %s (player %s): %s',
          f2(now), tostring(s.id), s.name, tostring(s.pid), tostring(s.team), tostring(a.id), a.name, tostring(a.pid), how))
      else
        p('mstats3 shield cast: no living ally for', tostring(s.id), s.name)
      end
    end
  end
  if #pairs_ == 0 then p('mstats3 shield cast at', f2(now), ': no living Sculptor with a living ally') end
  dgsim_ms3_pending = {t = now, pairs = pairs_}
end)
if not ok then p('mstats3 shield: ERROR', err) end
return table.concat(out, '\n')
