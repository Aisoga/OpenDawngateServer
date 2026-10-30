-- ADAMSLOW test library ([host] test_lua=<s>:dgsim_adamslow_lib.lua, once, before the other dgsim_adamslow_* probes).
-- A player reported that Adamance's passive (Lockdown) slows allied creeps. Lockdown: modifier_Item_Legendary_OnTookPhysical_AoESlow on the holder counts a shaper's physical hits
-- (OnTookDamage) into _Stacks; its fifth stack plays the nova and asks nAreaEffectUtils.ApplyModifierToObjectsInArea for
-- _Slow (3 s) on EnemyPlayers | EnemyNPCs within 10 of the holder, relative to the holder's team. This file defines
-- the global table dgsim_adamslow with the steps the other probes call:
--   gather(holder, attacker): around the nova holder: the allied players' creatures in a ring of 6, up to 4 live lane
--     minions of each team (the director's waves; an own-team one created when the waves have none), one jungle creep
--     (team NPC, aggroRange 0) created 5 north; the attacker 3 east;
--   hit(holder, attacker, n, amount): n physical basic hits (IsMelee | IsBasic | IsPhysicalDamage) from a snapshot of
--     the attacker's creature on the holder, the _Stacks count after each;
--   scan(label): every live object holding _Slow: id, noun, team, player or NPC, its caster and the caster's team, the
--     relation (enemy / NEUTRAL / ALLY / SELF), the distance to the caster; each instance once in detail, a totals line.
-- Ids are light userdata: compared by tostring. Returns a report.
local L = {}
local SLOW = nUtil.SPID('modifier_Item_Legendary_OnTookPhysical_AoESlow_Slow')
local STACKS = nUtil.SPID('modifier_Item_Legendary_OnTookPhysical_AoESlow_Stacks')
L.PASSIVE = nUtil.SPID('modifier_Item_Legendary_OnTookPhysical_AoESlow')
L.seen = {}      -- tostring(modifier id) -> true: the _Slow instances already logged
L.totals = {enemy = 0, NEUTRAL = 0, ALLY = 0, SELF = 0, unknown = 0}
L.novas = 0

local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local function ok_id(id) return id ~= nil and id ~= kObjIDNone end
L.f1 = f1
L.ok_id = ok_id

function L.time() return string.format('%.2f', nGameSimulator.GetMatchTime()) end

-- every live object: the pool's slots (GetObjectIdByIndex returns the next live object at or after a slot) up to the
-- first slot past the last one (beyond the pool's count it asserts, SPObjectPool.cpp 161)
function L.live()
  local live, seen = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if not ok_id(id) then break end
    if not seen[tostring(id)] then
      seen[tostring(id)] = true
      live[#live + 1] = id
    end
  end
  return live
end

function L.name(id)
  local okn, n = pcall(nGameObject.GetAssetName, id)
  if okn then return tostring(n) end
  return '?'
end

function L.isMinion(id)
  return string.sub(L.name(id), 1, 7) == 'Minion_'
end

function L.stacks(holder)
  local okm, m = pcall(nModifier.GetFirstModifierByGUID, holder, STACKS)
  if not okm or not ok_id(m) then return 'none' end
  return 'x' .. tostring(nModifier.GetStackCount(m))
end

local function place(id, x, y, fx, fy)
  nLocomotion.TeleportToPositionAndFacing(id, nVector.create(x, y, 0), nVector.create(fx, fy, 0))
end

function L.gather(holder, attacker, p)
  local team = nGameObject.GetTeam(holder)
  local hp = nGameObject.GetPosition(holder)
  pcall(nGameObject.HealDamage, holder, holder, nGameObject.GetMaxHitPoints(holder), 0)
  local allies, own, other = {}, {}, {}
  for i = 0, 9 do
    local id = nPlayer.GetPlayerControlledObjectID(i)
    if ok_id(id) and tostring(id) ~= tostring(holder) and tostring(id) ~= tostring(attacker) and nGameObject.IsAlive(id)
        and nGameObject.GetTeam(id) == team then
      allies[#allies + 1] = id
    end
  end
  for _, id in ipairs(L.live()) do
    if L.isMinion(id) and nGameObject.IsAlive(id) then
      if nGameObject.GetTeam(id) == team then
        if #own < 4 then own[#own + 1] = id end
      elseif #other < 4 then
        other[#other + 1] = id
      end
    end
  end
  local created = ''
  if #own == 0 then
    local noun = team == nTeamID.Spirit and 'Minion_Spirit_Melee.Noun' or 'Minion_Mortal_Melee.Noun'
    local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), team, nVector.create(hp.x - 4, hp.y, 0), kZeroVector, 1)
    if ok_id(m) then
      own[1] = m
      created = ' (no wave minion of the holder\'s team alive: created ' .. tostring(m) .. ')'
    end
  end
  for k, id in ipairs(allies) do
    local a = (k - 1) * 2 * math.pi / math.max(#allies, 1) + math.pi / 2
    place(id, hp.x + 6 * math.cos(a), hp.y + 6 * math.sin(a), 0, -1)
  end
  for k, id in ipairs(own) do
    local a = (k - 1) * 2 * math.pi / math.max(#own, 1)
    place(id, hp.x + 4 * math.cos(a), hp.y + 4 * math.sin(a), 0, -1)
  end
  for k, id in ipairs(other) do
    place(id, hp.x - 5, hp.y - 2 + (k - 1) * 1.5, 1, 0)
  end
  local creep = nObjectManager.CreateObject(nUtil.GetAsset('Creep_Jungle_Fish_Minor.Noun'), nTeamID.NPC,
    nVector.create(hp.x, hp.y + 5, 0), kZeroVector, 1)
  if ok_id(attacker) then place(attacker, hp.x + 3, hp.y, -1, 0) end
  local function row(kind, id)
    local at = nGameObject.GetPosition(id)
    p('adamslow gather:', kind, tostring(id), L.name(id), 'team', nGameObject.GetTeam(id), 'at', f1(at.x), f1(at.y),
      'distance', f1(nVector.distance(at, hp)))
  end
  row('holder', holder)
  if ok_id(attacker) then row('attacker', attacker) end
  for _, id in ipairs(allies) do row('allied player', id) end
  for _, id in ipairs(own) do row('minion of the holder\'s team', id) end
  for _, id in ipairs(other) do row('minion of the other team', id) end
  if ok_id(creep) then row('jungle creep (created)', creep) end
  p(string.format('adamslow gather: holder %s team %d at (%.1f, %.1f): %d allied players, %d own-team minions%s, '
    .. '%d other-team minions, jungle creep %s; match %s', tostring(holder), team, hp.x, hp.y, #allies, #own, created,
    #other, ok_id(creep) and tostring(creep) or 'NOT created', L.time()))
end

function L.hit(holder, attacker, n, amount, p)
  local basic = nBit.Or(nBit.Or(nDescriptors.IsMelee, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
  local steps = {}
  for i = 1, n do
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(attacker, kObjIDNone)
    local _, dealt = nGameObject.TakeDamage(snap, holder, amount, 0, basic, kZeroVector)
    nAttribute.RemoveAttributeSnapshot(snap)
    steps[#steps + 1] = string.format('%d: dealt %s stacks %s', i, f1(dealt), L.stacks(holder))
  end
  L.novas = L.novas + 1
  p(string.format('adamslow hit: %s %s (team %d) -> holder %s %s (team %d): %d physical basic hits of %s; %s; hp %s/%s; '
    .. 'match %s', tostring(attacker), L.name(attacker), nGameObject.GetTeam(attacker), tostring(holder), L.name(holder),
    nGameObject.GetTeam(holder), n, f1(amount), table.concat(steps, ', '), f1(nGameObject.GetHitPoints(holder)),
    f1(nGameObject.GetMaxHitPoints(holder)), L.time()))
end

function L.scan(label, p)
  local new, holders = 0, 0
  for _, id in ipairs(L.live()) do
    local okm, m = pcall(nModifier.GetFirstModifierByGUID, id, SLOW)
    if okm and ok_id(m) then
      holders = holders + 1
      local key = tostring(m)
      if not L.seen[key] then
        L.seen[key] = true
        new = new + 1
        local team = nGameObject.GetTeam(id)
        local okc, caster = pcall(nModifier.GetCasterID, m)
        local ct, rel, dist = nil, 'unknown', 'n/a'
        if okc and ok_id(caster) then
          local okt, t = pcall(nGameObject.GetTeam, caster)
          if okt then ct = t end
          local okp, cp = pcall(nGameObject.GetPosition, caster)
          if okp and cp then dist = f1(nVector.distance(nGameObject.GetPosition(id), cp)) end
        end
        if ct ~= nil then
          if tostring(caster) == tostring(id) then rel = 'SELF'
          elseif team == ct then rel = 'ALLY'
          elseif team == nTeamID.NPC then rel = 'NEUTRAL'
          else rel = 'enemy' end
        end
        L.totals[rel] = L.totals[rel] + 1
        local okr, left = pcall(nModifier.GetRemainingDuration, m)
        p(string.format('adamslow scan %s: Slow %s on %s %s team %d %s, caster %s %s team %s: %s of the caster, distance '
          .. '%s, %s s left; match %s', label, key, tostring(id), L.name(id), team,
          nPlayer.IsPlayerControlledObject(id) and 'player' or 'npc', okc and tostring(caster) or '?',
          okc and L.name(caster) or '?', tostring(ct), rel, dist, okr and f1(left) or '?', L.time()))
      end
    end
  end
  if new > 0 or label == 'final' then
    local t = L.totals
    p(string.format('adamslow scan %s: %d object(s) hold the Slow now, %d new; all seen so far: enemy %d, NEUTRAL %d, '
      .. 'ALLY %d, SELF %d, unknown %d (novas asked %d); match %s', label, holders, new, t.enemy, t.NEUTRAL, t.ALLY,
      t.SELF, t.unknown, L.novas, L.time()))
  end
end

dgsim_adamslow = L
return 'adamslow lib: loaded; match ' .. L.time()
