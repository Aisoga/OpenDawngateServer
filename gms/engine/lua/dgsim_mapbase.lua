-- MAPBASE probe: one team's base taken apart step by step on any level, the checks of
-- MM_Map_v3's two Binding tiers, three Cores a team and the Locus against SM_Map_v5's. [host] test_lua with six calls,
-- e.g. (MM_Map_v3, with the Cores' 480-s regrowth and the Bindings down for more than MM's 600-s tower respawnTime):
--   20:dgsim_mapbase.lua,22:dgsim_mapbase.lua,26:dgsim_mapbase.lua,28:dgsim_mapbase.lua,515:dgsim_mapbase.lua,
--   640:dgsim_mapbase.lua
-- or quick (no regrowth; call 5 comes less than 470 s after the last Core's death and ends the match itself):
--   20:dgsim_mapbase.lua,22:dgsim_mapbase.lua,26:dgsim_mapbase.lua,28:dgsim_mapbase.lua,32:dgsim_mapbase.lua
-- Best with `bots=0;director_waves=0`, so nothing else hurts the unshielded Guardian between the calls. The attacked
-- team is dgsim_mapbase_team (default Spirit, so the human of team 1 wins); every hit and kill is credited to a living
-- shaper of the other team (the human).
--   call 1: the survey (every Binding with its tier, every Core, the Locus with its NPC type and HP, the Guardian and its
--     shield, modifier_Tower_Immunity); a 100 true-damage hit on the Guardian (expected refused: the shield) and on the
--     innermost Binding while the outer ones stand (expected dealt: no tier lock).
--   call 2: every Core of the team but the last killed (KillObject); expected the shield still on.
--   call 3: the last Core killed.
--   call 4: expected the shield off (the host lifts it the next tick), modifier_Guardian_Nub<X>_Destroyed on the
--     Guardian for each Core the level has, none for a letter it lacks (MM_Map_v3: D and E, so KnockUpOrbs and
--     LaserBlast stay allowed); a 100 hit on the Guardian dealt; then every Binding of the team killed, outermost first
--     (the host's "match: Binding ... destroyed" and tower table lines).
--   call 5: 470 s or more after the last Core died: each Core alive again (ability_BaseNub_Death's 480 s), its
--     Destroyed modifier gone, the shield back (D14), the Bindings still down (towerRespawnEnabled false), then every
--     Core killed again; a call before the regrowth is complete reports "not yet" and waits for the next. Earlier than
--     470 s: as call 6.
--   call 6: the Bindings still down (seconds since their death), the shield off; the Guardian hit for 100 and then for
--     its HP + 100 (true damage, the shaper's snapshot): its passive reports it through OnTitanTowerDestroyed and the
--     host ends the match (reason 1 when the Spirit Guardian dies).
-- Returns a report; every line starts with "mapbase".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'mapbase ' .. table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local S = dgsim_mapbase or { step = 0 }
dgsim_mapbase = S

local team = dgsim_mapbase_team or nTeamID.Spirit
local enemy = team == nTeamID.Mortal and nTeamID.Spirit or nTeamID.Mortal
local SHIELD = nUtil.SPID('modifier_Tower_Immunity')
local LETTERS = { 'A', 'B', 'C', 'D', 'E' }

local function none(id) return id == nil or id == kObjIDNone end
local function now() return nGameSimulator.GetMatchTime() or 0 end

local function tierOf(name)
  if string.find(name, '_Outer') then return 0 end
  if string.find(name, '_Middle') then return 1 end
  if string.find(name, '_Inner') then return 2 end
  return 3
end

-- The team's Bindings (outermost first), Cores (by letter), the Loci and the Guardian.
local function scan()
  local b, cores, loci = {}, {}, {}
  for i = 0, 2047 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if none(id) then break end
    local name = nGameObject.GetAssetName(id) or ''
    if string.find(name, '^Binding_') and nGameObject.GetTeam(id) == team then
      b[#b + 1] = { id = id, name = name, tier = tierOf(name) }
    elseif string.find(name, '^nub_') and nGameObject.GetTeam(id) == team then
      local letter = string.match(name, '_v5_([A-E])')
      if letter then cores[letter] = id end
    elseif string.find(name, '^DEST_prefab_') then
      loci[#loci + 1] = id
    end
  end
  table.sort(b, function(x, y) return x.tier < y.tier end)
  return b, cores, loci, nBattlegrounds.GetGuardianOnTeam(team)
end

local function alive(id) return not none(id) and nGameObject.IsAlive(id) == true end
local function shielded(g)
  local m = nModifier.GetFirstModifierByGUID(g, SHIELD)
  return not none(m)
end
local function destroyedMods(g)
  local s = ''
  for _, l in ipairs(LETTERS) do
    local on = nModifier.AgentHasModifierMatchingGUID(g, nUtil.SPID('modifier_Guardian_Nub' .. l .. '_Destroyed'))
    s = s .. (on and l or '-')
  end
  return s
end

local function attacker()
  local players = nPlayer.GetPlayerControlledObjects()
  if type(players) ~= 'table' then return kObjIDNone end
  for _, c in ipairs(players) do
    if nGameObject.GetTeam(c) == enemy and nGameObject.IsAlive(c) then return c end
  end
  return kObjIDNone
end

local function hit(target, amount)
  local a = attacker()
  if none(a) then return 'no attacker' end
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(a, kObjIDNone)
  local hp0 = nGameObject.GetHitPoints(target)
  local s, d = nGameObject.TakeDamage(snap, target, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  return 'status ' .. tostring(s) .. ' dealt ' .. f1(d) .. ' hp ' .. f1(hp0) .. ' -> ' .. f1(nGameObject.GetHitPoints(target))
end

local function kill(id)
  nGameObject.KillObject(id, attacker(), false)
  return nGameObject.GetAssetName(id) .. ' ' .. tostring(id) .. ' alive ' .. tostring(alive(id))
end

local function coreStates(cores)
  local s = ''
  for _, l in ipairs(LETTERS) do
    local id = cores[l]
    s = s .. (none(id) and '.' or (alive(id) and l or '-'))
  end
  return s
end

local function survey(b, cores, loci, g)
  for _, x in ipairs(b) do
    p('survey: Binding', x.name, tostring(x.id), 'tier', x.tier, 'alive', tostring(alive(x.id)), 'hp',
      f1(nGameObject.GetHitPoints(x.id)))
  end
  p('survey:', #b, 'Binding(s) of team', team, '; Cores', coreStates(cores), '(. = not on the level)')
  for _, id in ipairs(loci) do
    p('survey: Locus', nGameObject.GetAssetName(id), tostring(id), 'team', nGameObject.GetTeam(id), 'NPC type',
      nGameObject.GetNPCType(id), 'hp', f1(nGameObject.GetHitPoints(id)), 'a valid target for its enemy',
      tostring(nGameObject.ValidateHostileTarget(nGameObject.GetTeam(id) == 1 and 2 or 1, id, false)))
  end
  p('survey: Guardian', nGameObject.GetAssetName(g), tostring(g), 'hp', f1(nGameObject.GetHitPoints(g)), 'shield',
    tostring(shielded(g)), 'Destroyed modifiers', destroyedMods(g))
end

local ok, err = pcall(function()
  local b, cores, loci, g = scan()
  if none(g) then
    p('no Guardian on team', team)
    return
  end
  local t = now()
  if S.step == 0 then
    survey(b, cores, loci, g)
    p('call 1: hit the shielded Guardian (expected refused, dealt 0):', hit(g, 100))
    if #b > 0 then
      local inner = b[#b]
      p('call 1: hit the innermost Binding', inner.name, 'while', #b - 1, 'outer one(s) stand (expected dealt 100: no tier lock):',
        hit(inner.id, 100))
    end
    S.step = 1
  elseif S.step == 1 then
    local list = {}
    for _, l in ipairs(LETTERS) do
      if alive(cores[l]) then list[#list + 1] = l end
    end
    for i = 1, #list - 1 do p('call 2: Core', list[i], 'killed:', kill(cores[list[i]])) end
    S.last = list[#list]
    p('call 2: Cores', coreStates(cores), 'shield', tostring(shielded(g)), '(expected true: one Core lives)')
    S.step = 2
  elseif S.step == 2 then
    if S.last then p('call 3: the last Core', S.last, 'killed:', kill(cores[S.last])) end
    S.coresDownAt = t
    p('call 3: Cores', coreStates(cores), 'at match', f1(t))
    S.step = 3
  elseif S.step == 3 then
    p('call 4: shield', tostring(shielded(g)), '(expected false); Destroyed modifiers', destroyedMods(g),
      '(expected one per Core on the level, none for a missing letter); Cores', coreStates(cores))
    p('call 4: hit the unshielded Guardian (expected dealt 100):', hit(g, 100))
    for _, x in ipairs(b) do
      if alive(x.id) then p('call 4: Binding killed (tier', x.tier .. '):', kill(x.id)) end
    end
    S.bindingsDownAt = t
    S.step = 4
  elseif S.step == 4 and S.coresDownAt and t - S.coresDownAt >= 470 then
    local back = true
    for _, l in ipairs(LETTERS) do
      if not none(cores[l]) and not alive(cores[l]) then back = false end
    end
    if not back then
      p('call 5: not yet: Cores', coreStates(cores), f1(t - S.coresDownAt), 's after the last death; waiting')
      return
    end
    p('call 5: the Cores regrew:', coreStates(cores), f1(t - S.coresDownAt), 's after the last death; Destroyed modifiers',
      destroyedMods(g), '(expected none); shield', tostring(shielded(g)), '(expected true)')
    p('call 5: hit the Guardian again (expected refused, dealt 0):', hit(g, 100))
    for _, x in ipairs(b) do
      p('call 5: Binding', x.name, 'alive', tostring(alive(x.id)), f1(t - (S.bindingsDownAt or t)),
        's after its death (expected false: towerRespawnEnabled false)')
    end
    for _, l in ipairs(LETTERS) do
      if alive(cores[l]) then p('call 5: Core', l, 'killed again:', kill(cores[l])) end
    end
    S.step = 5
  elseif S.step == 4 or S.step == 5 then
    for _, x in ipairs(b) do
      p('call 6: Binding', x.name, 'alive', tostring(alive(x.id)), f1(t - (S.bindingsDownAt or t)),
        's after its death (expected false)')
    end
    p('call 6: Cores', coreStates(cores), 'shield', tostring(shielded(g)), '(expected false); Destroyed modifiers',
      destroyedMods(g))
    p('call 6: hit 100:', hit(g, 100))
    p('call 6: the lethal hit:', hit(g, nGameObject.GetHitPoints(g) + 100), 'alive', tostring(alive(g)), 'at match', f1(t))
    S.step = 6
  else
    p('call after the end: Guardian alive', tostring(alive(g)), 'at match', f1(t))
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
