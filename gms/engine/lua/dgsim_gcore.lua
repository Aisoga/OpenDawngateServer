-- GUARDCORE probe: the enemy Guardian's Cores destroyed one by one while the human stands in front of it, and what its
-- scripts then allow (gms/core/matchflow/guardcore.h). Run it every 2 s, e.g.
--   [host] test_lua=20-660/2:dgsim_gcore.lua      (bots=0; one call every 2 s from 20 s to 660 s)
-- Call 1: the enemy Locus removed (it would kill the human at once) and the human teleported 22 units in front of the
--   enemy Guardian (toward the map's centre), as NPCTARGET's dgsim_npct.lua did.
-- Every call: the human healed and, if alive and more than 26 units away (a respawn), teleported back, the Guardian
--   healed to full (without its shield nothing should end the match before the regrowths); at the kill calls
--   the next Core of the order dies (nGameObject.KillObject, the human as killer: its ability_BaseNub_Death runs, which
--   puts modifier_Guardian_Nub<X>_Destroyed on the Guardian); then one report line:
--   gcore call N (action) at match T: Guardian <id> hp H shield on|off; cores A+ B+ C- ...; destroyed mods A- B- C+ ...;
--   conditions A:false B:true ...; special cooldown on|off; human <id> dist D hp before->after
--   ("conditions" are the specials' own condition functions called now: true only with the Core alive, no special
--   cooldown and an enemy player within 67; HealthPacks also needs an allied player of the Guardian, none with bots=0).
-- The plan: dgsim_gcore_order (default E B C D A: the laser first, the health packs last, as the human can't see them)
--   and dgsim_gcore_first (the first kill's call, default 31 = 80 s with 20-…/2), dgsim_gcore_every (calls between two
--   kills, default 30 = 60 s). With the defaults: E at 80 s, B 140 s, C 200 s, D 260 s, A 320 s (the shield drops);
--   each Core regrows 480 s after its death (E at 560 s, B at 620 s), which the host logs as `match: GUARDCORE ...
--   restored`. Expected in the host's `npc target:` lines: each special only while its Core lives; the special
--   cooldown modifier 6000 + 2000 ms per destroyed Core.
-- Returns the report (logged by the host); every line starts with "gcore".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'gcore ' .. table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end
local function pos(o)
  local v = o and o ~= kObjIDNone and nGameObject.GetPosition(o)
  if not v then return 'nil' end
  return string.format('(%.1f, %.1f)', v.x, v.y)
end
local function alive(o)
  return o ~= nil and o ~= kObjIDNone and nGameObject.IsAlive(o)
end

local LETTERS = { 'A', 'B', 'C', 'D', 'E' }
local CONDITIONS = {
  A = 'condition_Guardian_UseHealthPacks', B = 'condition_Guardian_UseOrbLauncher',
  C = 'condition_Guardian_UseFireTrail', D = 'condition_Guardian_UseKnockUpOrbs', E = 'condition_Guardian_UseLaserBlast',
}

local S = dgsim_gcore or {}
dgsim_gcore = S

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c end  -- IsBot takes an object
  end
end

local function teleport(o, x, y)
  local v = nGameObject.GetPosition(o)
  nLocomotion.TeleportToPositionAndFacing(o, nVector.create(x, y, v.z), nVector.create(1, 0, 0))
end

local function heal(h)
  if not alive(h) then return 'dead' end
  local before = nGameObject.GetHitPoints(h)
  for _ = 1, 8 do
    local missing = nGameObject.GetMaxHitPoints(h) - nGameObject.GetHitPoints(h)
    if missing < 1 then break end
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(h, kObjIDNone)
    nGameObject.HealDamage(snap, h, missing, 0)
    nAttribute.RemoveAttributeSnapshot(snap)
  end
  return f1(before) .. '->' .. f1(nGameObject.GetHitPoints(h))
end

local function findCores(team)
  local prefix = team == nTeamID.Mortal and 'nub_mortal_v5_' or 'nub_spirit_v5_'
  local cores = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local name = tostring(nGameObject.GetAssetName(id))
    if string.sub(name, 1, #prefix) == prefix then cores[string.sub(name, #prefix + 1, #prefix + 1)] = id end
  end
  return cores
end

local function setup(h)
  local team = nGameObject.GetTeam(h)
  local enemy = team == nTeamID.Mortal and nTeamID.Spirit or nTeamID.Mortal
  S.guardian = nBattlegrounds.GetGuardianOnTeam(enemy)
  S.cores = findCores(enemy)
  local gp = nGameObject.GetPosition(S.guardian)
  local len = math.sqrt(gp.x * gp.x + gp.y * gp.y)
  S.spot = { gp.x - gp.x / len * 22, gp.y - gp.y / len * 22 }
  local ids = nObjectManager.GetObjectsInRadius(gp, 90, nGetObjectsSortType.Distance, nTargetType.EnemyNPCs, team,
    kObjIDNone, kObjIDNone, 0, 0) or {}
  for _, id in ipairs(ids) do
    local name = tostring(nGameObject.GetAssetName(id))
    if string.find(name, 'tower_v5', 1, true) then
      nGameObject.KillObject(id)
      p('the enemy Locus', tostring(id), name, 'removed for the test, alive', tostring(nGameObject.IsAlive(id)))
    end
  end
  teleport(h, S.spot[1], S.spot[2])
  local list = {}
  for _, l in ipairs(LETTERS) do list[#list + 1] = l .. '=' .. tostring(S.cores[l]) .. pos(S.cores[l]) end
  p(string.format('enemy Guardian %s at %s, its cores %s; the human %s teleported to %s (dist %s)',
    tostring(S.guardian), pos(S.guardian), table.concat(list, ' '), tostring(h), pos(h),
    f1(nGameObject.GetObjectDistance(S.guardian, h))))
end

local function has(g, name)
  return nModifier.AgentHasModifierMatchingGUID(g, nUtil.SPID(name)) == true
end

local function report(h, action)
  local g = S.guardian
  local cores, mods, conds = {}, {}, {}
  for _, l in ipairs(LETTERS) do
    cores[#cores + 1] = l .. (alive(S.cores[l]) and '+' or '-')
    mods[#mods + 1] = l .. (has(g, 'modifier_Guardian_Nub' .. l .. '_Destroyed') and '+' or '-')
    local env = _G or (getfenv and getfenv(0)) or {}
    local c = env[CONDITIONS[l]]
    local ok, r = false, nil
    if c and c[1] and alive(g) then ok, r = pcall(c[1], g) end
    conds[#conds + 1] = l .. ':' .. (ok and tostring(r == true) or 'n/a')
  end
  p(string.format('call %d (%s) at match %s: Guardian %s hp %s shield %s; cores %s; destroyed mods %s; conditions %s; '
    .. 'special cooldown %s; human %s dist %s hp %s', S.calls, action, f1(nGameSimulator.GetMatchTime()), tostring(g),
    alive(g) and f1(nGameObject.GetHitPoints(g)) or 'dead', tostring(has(g, 'modifier_Tower_Immunity')),
    table.concat(cores, ' '), table.concat(mods, ' '), table.concat(conds, ' '),
    tostring(has(g, 'modifier_Guardian_SpecialAbility_Cooldown')), tostring(h),
    alive(h) and f1(nGameObject.GetObjectDistance(g, h)) or '-', heal(h)))
end

local ok, err = pcall(function()
  S.calls = (S.calls or 0) + 1
  local h = human()
  if not h then
    p('call', S.calls, ': no human')
    return
  end
  local action = 'keep'
  if not S.guardian then
    setup(h)
    action = 'setup'
  end
  local order = dgsim_gcore_order or { 'E', 'B', 'C', 'D', 'A' }
  local first, every = dgsim_gcore_first or 31, dgsim_gcore_every or 30
  local k = (S.calls - first) / every
  if S.calls >= first and k == math.floor(k) and order[k + 1] then
    local l = order[k + 1]
    local core = S.cores[l]
    if alive(core) then
      nGameObject.KillObject(core, h, false)
      action = 'kill ' .. l
      p('core', l, tostring(core), 'killed at match', f1(nGameSimulator.GetMatchTime()), '-> alive',
        tostring(nGameObject.IsAlive(core)))
    else
      action = 'kill ' .. l .. ' (already dead)'
    end
  end
  if alive(h) and alive(S.guardian) and nGameObject.GetObjectDistance(S.guardian, h) > 26 then
    teleport(h, S.spot[1], S.spot[2])
  end
  -- once the last Core is down the shield is gone: keep the Guardian at full HP so that nothing (the human's
  -- auto-attack, a minion) ends the match before the regrowths
  if alive(S.guardian) and nGameObject.GetHitPoints(S.guardian) < nGameObject.GetMaxHitPoints(S.guardian) then
    heal(S.guardian)
  end
  report(h, action)
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
