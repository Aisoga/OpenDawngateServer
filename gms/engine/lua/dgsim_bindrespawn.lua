-- MAPRULES3 probe: a Binding's death and its return. Run on a level whose
-- Bindings respawn (a compiled map with `bindings.respawn`, e.g. the fixture CM_Tuned: 60 s) or with the test aid
-- [host] tower_respawn_s; the host resurrects the Binding (gms/engine/bindrespawn.cpp), this probe only kills, looks
-- and sets up a target. [host] test_lua with six calls (global dgsim_bindrespawn counts them), e.g. with 60 s:
--   40:dgsim_bindrespawn.lua,46:dgsim_bindrespawn.lua,104:dgsim_bindrespawn.lua,112:dgsim_bindrespawn.lua,
--   116:dgsim_bindrespawn.lua,182:dgsim_bindrespawn.lua
--   part 1: the Spirit top Outer Binding (Binding_Spirit_Outer with y > x): its state (alive, HP, modifier_Binding_Passive,
--     the archon of its blackboard, Untargetable, a valid hostile target for team 1), then KillObject (killer the Mortal
--     top Outer Binding);
--   part 2 (death + 6 s): expected dead, the passive ended (its Deactivate deleted the archon), not a valid target;
--   part 3 (after the host's return): expected alive, full HP, the passive back (a new instance, a new archon), a valid
--     target again; the number of bindingarchon objects near it (1 expected, not 2); then a Mortal melee minion,
--     immobilized, 12 units from it toward the map's centre (inside its 28-unit sphere);
--   part 4 (+8 s): the minion's HP (expected hit: 0.38 x its max HP a shot, dead after three) and the Binding's target;
--   part 5: the Binding killed again (the second death; the host logs a second due time);
--   part 6 (after the second return): alive again and the passive back.
-- Each part also prints the bots' view (GetNumTowers/GetOuterTower of the shims): 3 Spirit Bindings on top while it
-- stands, 2 while it is down.
-- Every line starts with "bindrespawn".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'bindrespawn ' .. table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local S = dgsim_bindrespawn or {}
dgsim_bindrespawn = S

local PASSIVE = nUtil.SPID('modifier_Binding_Passive')
local ARCHON_KEY = nUtil.SPID('binding_archon_object_id')

local function live()
  local list, seen = {}, {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if not seen[tostring(id)] then seen[tostring(id)] = true; list[#list + 1] = id end
  end
  return list
end

local function findTop(name)
  for _, id in ipairs(live()) do
    local pos = nGameObject.GetPosition(id)
    if nGameObject.GetAssetName(id) == name and pos and pos.y > pos.x then return id, pos end
  end
end

local function attr(o, name)
  local ok, v = pcall(nAttribute.GetAttributeValue, o, nAttributeType[name])
  if ok then return f1(v) end
  return '?'
end

local function archons(pos)
  local n = 0
  for _, id in ipairs(live()) do
    local q = nGameObject.GetPosition(id)
    if nGameObject.GetAssetName(id) == 'bindingarchon' and q and (q.x - pos.x) ^ 2 + (q.y - pos.y) ^ 2 < 25 then n = n + 1 end
  end
  return n
end

local function state(b)
  if not b then return 'no Binding' end
  local m = nModifier.GetFirstModifierByGUID(b, PASSIVE)
  local okA, a = pcall(nAgent.GetBlackboardData_GUID, b, ARCHON_KEY)
  local okV, valid = pcall(nGameObject.ValidateHostileTarget, nTeamID.Mortal, b, false)
  local okT, target = pcall(nGameObject.GetTargetID, b)
  return string.format('%s alive %s hp %s/%s passive %s archon %s (archons here %d) Untargetable %s valid target for Mortal %s target %s',
    tostring(b), tostring(nGameObject.IsAlive(b)), f1(nGameObject.GetHitPoints(b)), f1(nGameObject.GetMaxHitPoints(b)),
    tostring(m ~= nil and m ~= kObjIDNone and m or 'none'), okA and tostring(a) or ('? ' .. tostring(a)),
    S.pos and archons(S.pos) or -1, attr(b, 'Untargetable'), okV and tostring(valid) or ('? ' .. tostring(valid)),
    okT and tostring(target) or '?')
end

-- The bots' view (dgsim_bots_api.lua's shims, when the bots run): the Spirit Bindings standing per lane and the top
-- lane's outermost one.
local function botsView()
  local nb = nBattlegrounds
  if type(nb.GetNumTowers) ~= 'function' then return 'bots view n/a (no shims)' end
  local okA, top = pcall(nb.GetNumTowers, nTeamID.Spirit, nLaneID.Top)
  local okB, bottom = pcall(nb.GetNumTowers, nTeamID.Spirit, nLaneID.Bottom)
  local okC, outer = pcall(nb.GetOuterTower, nTeamID.Spirit, nLaneID.Top)
  return string.format('bots view: GetNumTowers(Spirit, top) %s, (Spirit, bottom) %s, GetOuterTower(Spirit, top) %s',
    tostring(okA and top or '?'), tostring(okB and bottom or '?'), tostring(okC and outer or '?'))
end

local function kill()
  if not nGameObject.IsAlive(S.b) then
    p('not killed: already dead ->', state(S.b))
    return
  end
  local killer = findTop('Binding_Mortal_Outer') or kObjIDNone
  nGameObject.KillObject(S.b, killer, false)
  p('killed by', tostring(killer), '->', state(S.b))
end

local ok, err = pcall(function()
  S.part = (S.part or 0) + 1
  p('part', S.part, 'at match time', f1(nGameSimulator.GetMatchTime()))
  if S.part > 1 then p(botsView()) end
  if S.part == 1 then
    S.b, S.pos = findTop('Binding_Spirit_Outer')
    p('before:', state(S.b))
    p(botsView())
    if S.b then kill() end
  elseif S.part == 2 then
    p('after the death:', state(S.b), '(expected dead, passive none, not a valid target)')
  elseif S.part == 3 or S.part == 6 then
    p('after the return:', state(S.b), '(expected alive, full hp, passive back, archons here 1, a valid target)')
    if S.part == 3 and S.b and S.pos then
      local d = math.sqrt(S.pos.x ^ 2 + S.pos.y ^ 2)
      local at = nVector.create(S.pos.x - S.pos.x / d * 12, S.pos.y - S.pos.y / d * 12, 0)
      S.minion = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Mortal_Melee.Noun'), nTeamID.Mortal, at, kZeroVector, 1)
      if S.minion and S.minion ~= kObjIDNone then
        nAttribute.AddAttributeModifier(S.minion, nAttributeType.Immobilized, 1, nAttributeOp.kAdd)
        p('minion', tostring(S.minion), 'at', f1(at.x), f1(at.y), 'hp', f1(nGameObject.GetHitPoints(S.minion)), '/',
          f1(nGameObject.GetMaxHitPoints(S.minion)))
      else
        p('minion not created')
      end
    end
  elseif S.part == 4 then
    local m = S.minion
    if m and m ~= kObjIDNone then
      p('minion', tostring(m), 'alive', tostring(nGameObject.IsAlive(m)), 'hp', f1(nGameObject.GetHitPoints(m)), '/',
        f1(nGameObject.GetMaxHitPoints(m)), '(expected hit or dead: the Binding fights)')
      if nGameObject.IsAlive(m) then nGameObject.MarkForDelete(m) end
    end
    p('Binding:', state(S.b))
  elseif S.part == 5 then
    p('second death: before', state(S.b))
    if S.b then kill() end
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
