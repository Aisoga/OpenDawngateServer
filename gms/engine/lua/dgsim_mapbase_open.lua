-- MAPBASE probe: a match ended by the game itself on a shortened path. One team's
-- (dgsim_mapbase_team, default Spirit) Bindings are removed at the first call and its Cores at the second, so the
-- Guardian's shield drops (D14) and the other team's minion waves and bots take the base on their own
-- (AddBaseStructuresToAggroList, the bots' AttackBase missions). Every later call reports the Guardian's HP; a call at
-- or after match dgsim_mapbase_fallback_s (default 570, before the Cores' 480-s regrowth when they died at 120 s)
-- with the Guardian still alive kills it with a lethal true-damage hit and says FALLBACK. E.g. with bots and waves:
--   60:dgsim_mapbase_open.lua,120:dgsim_mapbase_open.lua,150-570/30:dgsim_mapbase_open.lua
-- Expected: "match: END ... (OnTitanTowerDestroyed, killer <a minion or bot>)" before the fallback; the client's
-- kGameStateGameOver. Every hit is credited to a living shaper of the other team. Returns a report; every line starts
-- with "mapbase open".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'mapbase open ' .. table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local S = dgsim_mapbase_open or { step = 0 }
dgsim_mapbase_open = S

local team = dgsim_mapbase_team or nTeamID.Spirit
local enemy = team == nTeamID.Mortal and nTeamID.Spirit or nTeamID.Mortal
local fallback = dgsim_mapbase_fallback_s or 570

local function none(id) return id == nil or id == kObjIDNone end
local function alive(id) return not none(id) and nGameObject.IsAlive(id) == true end

local function attacker()
  local players = nPlayer.GetPlayerControlledObjects()
  if type(players) ~= 'table' then return kObjIDNone end
  for _, c in ipairs(players) do
    if nGameObject.GetTeam(c) == enemy and nGameObject.IsAlive(c) then return c end
  end
  return kObjIDNone
end

local function killAll(prefix)
  local n = 0
  for i = 0, 2047 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if none(id) then break end
    local name = nGameObject.GetAssetName(id) or ''
    if string.find(name, prefix) and nGameObject.GetTeam(id) == team and alive(id) then
      nGameObject.KillObject(id, attacker(), false)
      p('killed', name, tostring(id), 'alive', tostring(alive(id)))
      n = n + 1
    end
  end
  return n
end

local ok, err = pcall(function()
  local g = nBattlegrounds.GetGuardianOnTeam(team)
  local t = nGameSimulator.GetMatchTime() or 0
  if none(g) then
    p('no Guardian on team', team)
    return
  end
  local shield = nModifier.GetFirstModifierByGUID(g, nUtil.SPID('modifier_Tower_Immunity'))
  if S.step == 0 then
    p('call 1 at match', f1(t), ': the Bindings of team', team, 'removed:', killAll('^Binding_'))
    S.step = 1
  elseif S.step == 1 then
    p('call 2 at match', f1(t), ': the Cores of team', team, 'removed:', killAll('^nub_'))
    S.step = 2
  elseif alive(g) then
    p('at match', f1(t), ': Guardian', tostring(g), 'hp', f1(nGameObject.GetHitPoints(g)), 'shield', tostring(not none(shield)))
    if t >= fallback then
      local a = attacker()
      if none(a) then
        p('FALLBACK: no attacker')
        return
      end
      local snap = nAttribute.CreateAttributeSnapshot_FromObject(a, kObjIDNone)
      local s, d = nGameObject.TakeDamage(snap, g, nGameObject.GetHitPoints(g) + 100, 0, nDescriptors.IsTrueDamage, kZeroVector)
      nAttribute.RemoveAttributeSnapshot(snap)
      p('FALLBACK: the lethal hit at match', f1(t), 'status', tostring(s), 'dealt', f1(d), 'alive', tostring(alive(g)))
    end
  else
    p('at match', f1(t), ': the Guardian is dead')
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
