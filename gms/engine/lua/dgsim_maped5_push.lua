-- MAPED5 probe: a bot match on a push test map (CM_PushA/B/C) in two halves. Until match dgsim_maped5_open_s
-- (default 480) it only reports; at the first call from then on it removes one team's (dgsim_maped5_team, default
-- Spirit) living Bindings, at the first call 30 s or more later its living Cores, so the Guardian's shield drops (D14)
-- and the other team's waves and bots can reach it (AddBaseStructuresToAggroList at the lane's last knot, the bots'
-- siege). No fallback hit: whether the match ends by itself is the question. E.g. with bots and waves:
--   60-1080/30:dgsim_maped5_push.lua
-- Every call reports per team the Guardian's HP and shield and the living Bindings (by tier) and Cores. Every line
-- starts with "maped5 push".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'maped5 push ' .. table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local S = dgsim_maped5_push or { step = 0, at = 0 }
dgsim_maped5_push = S

local team = dgsim_maped5_team or nTeamID.Spirit
local enemy = team == nTeamID.Mortal and nTeamID.Spirit or nTeamID.Mortal
local openAt = dgsim_maped5_open_s or 480

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

-- the living objects whose asset name starts with prefix, per team: { [team] = { names } }
local function living(prefix)
  local r = { [nTeamID.Mortal] = {}, [nTeamID.Spirit] = {} }
  for i = 0, 2047 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if none(id) then break end
    local name = nGameObject.GetAssetName(id) or ''
    if string.find(name, prefix) and alive(id) then
      local tm = nGameObject.GetTeam(id)
      if r[tm] then r[tm][#r[tm] + 1] = string.match(name, '_(%w+)%.Noun$') or name end
    end
  end
  return r
end

local function killAll(prefix)
  local n = 0
  for i = 0, 2047 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if none(id) then break end
    local name = nGameObject.GetAssetName(id) or ''
    if string.find(name, prefix) and nGameObject.GetTeam(id) == team and alive(id) then
      nGameObject.KillObject(id, attacker(), false)
      n = n + 1
    end
  end
  return n
end

local ok, err = pcall(function()
  local t = nGameSimulator.GetMatchTime() or 0
  if S.step == 0 and t >= openAt then
    p('open at match', f1(t), ': team', team, 'Bindings removed', killAll('^Binding_'))
    S.step, S.at = 1, t
  elseif S.step == 1 and t >= S.at + 25 then  -- the next call of a 30-s schedule (match times drift by ms)
    p('open at match', f1(t), ': team', team, 'Cores removed', killAll('^nub_'))
    S.step = 2
  end
  local b, c = living('^Binding_'), living('^nub_')
  for _, tm in ipairs({ nTeamID.Mortal, nTeamID.Spirit }) do
    local g = nBattlegrounds.GetGuardianOnTeam(tm)
    local hp, shield = 'none', 'none'
    if alive(g) then
      hp = f1(nGameObject.GetHitPoints(g))
      shield = tostring(not none(nModifier.GetFirstModifierByGUID(g, nUtil.SPID('modifier_Tower_Immunity'))))
    elseif not none(g) then
      hp = 'dead'
    end
    p('at match', f1(t), ': team', tm, 'Guardian hp', hp, 'shield', shield, 'Bindings', #b[tm],
      '[' .. table.concat(b[tm], ' ') .. ']', 'Cores', #c[tm], '[' .. table.concat(c[tm], ' ') .. ']')
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
