-- MAPTAGS probe: the level tag map as the server's scripts read it (nLevel.ObjectPositionMatchesLevelTag, the same
-- LevelTagMap_PositionHasTag the client's bot target check BotDirector_IsValidTarget asks), on MM_Map_v3 (whose tag
-- map the setup now makes) or SM_Map_v5. [host] test_lua with four calls two seconds apart while the human stands at
-- his start, e.g.
--   20:dgsim_maptags.lua,22:dgsim_maptags.lua,24:dgsim_maptags.lua,26:dgsim_maptags.lua
-- Best with `bots=0;director_waves=0` or any practice match (the bots' shims are used when they are loaded).
--   call 1: every Locus, Binding, Guardian, Core and gold mine with the tags at its position (MM_Map_v3: each checked
--     against the tag map's rules); the human's tags at his start (expected: his side, Base, Locus) and the bots'
--     target check on him from the other team (nBot.ValidateHostileBotTarget: expected false there, true with
--     skipLocusCheck); then the human teleported into the lane (MM: 14 units from the centre on his side).
--   call 2: the human's tags in the lane (MM: Lane, Top, Bottom, his side; no Base, Locus) and the target check (true);
--     then teleported 5 units behind his team's Middle Binding.
--   call 3: the tags there (MM: BindingRange, Lane, his side); then teleported back to his start.
--   call 4: the tags at his start again and the target check (false), then the result.
-- Returns a report; every line starts with "maptags"; the last call's line "maptags RESULT PASS|FAIL n of m checks".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'maptags ' .. table.concat(t, ' ')
end

local S = dgsim_maptags or { step = 0, pass = 0, fail = 0, failures = {} }
dgsim_maptags = S

local TAGS = { 'MortalSide', 'SpiritSide', 'Base', 'Locus', 'Lane', 'Top', 'Bottom', 'BindingRange', 'Jungle',
  'SpiritWell', 'SpiritWellCenter', 'Parasite', 'GreenBuff', 'BlueBuff', 'OrangeBuff', 'MoneyPigs' }

local function none(id) return id == nil or id == kObjIDNone end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local function tagsOf(id)
  local got, set = {}, {}
  if not (nLevel and nLevel.ObjectPositionMatchesLevelTag and nLevelTags) then return '(no nLevel)', set end
  for _, n in ipairs(TAGS) do
    local e = nLevelTags[n]
    if e ~= nil and nLevel.ObjectPositionMatchesLevelTag(id, e) then
      got[#got + 1] = n
      set[n] = true
    end
  end
  return (#got > 0 and table.concat(got, ',') or '-'), set
end

local function posOf(id)
  local v = nGameObject.GetPosition(id)
  return v, '(' .. f1(v.x) .. ', ' .. f1(v.y) .. ')'
end

-- want: names that must be there; without: names that must not
local function check(what, set, want, without)
  local bad = {}
  for _, n in ipairs(want or {}) do if not set[n] then bad[#bad + 1] = 'lacks ' .. n end end
  for _, n in ipairs(without or {}) do if set[n] then bad[#bad + 1] = 'has ' .. n end end
  if #bad == 0 then
    S.pass = S.pass + 1
    return 'ok'
  end
  S.fail = S.fail + 1
  S.failures[#S.failures + 1] = what .. ': ' .. table.concat(bad, ', ')
  return 'FAIL (' .. table.concat(bad, ', ') .. ')'
end

local function checkBool(what, got, want)
  if got == want then
    S.pass = S.pass + 1
    return 'ok'
  end
  S.fail = S.fail + 1
  S.failures[#S.failures + 1] = what .. ': ' .. tostring(got) .. ', expected ' .. tostring(want)
  return 'FAIL (expected ' .. tostring(want) .. ')'
end

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if not none(c) and not nPlayer.IsBot(c) then return c end
  end
  return kObjIDNone
end

local function teleport(o, x, y)
  local v = nGameObject.GetPosition(o)
  nLocomotion.TeleportToPositionAndFacing(o, nVector.create(x, y, v.z), nVector.create(1, 0, 0))
end

local function sideOf(team) return team == nTeamID.Mortal and 'MortalSide' or 'SpiritSide' end
local function otherSide(team) return team == nTeamID.Mortal and 'SpiritSide' or 'MortalSide' end

-- The bots' target check on the human from the other team (the shims' nBot.ValidateHostileBotTarget, when loaded).
local function targetCheck(h, want, label)
  if not (nBot and nBot.ValidateHostileBotTarget) then
    p(label, 'target check: no nBot.ValidateHostileBotTarget (bots not loaded)')
    return
  end
  local team = nGameObject.GetTeam(h)
  local enemy = team == nTeamID.Mortal and nTeamID.Spirit or nTeamID.Mortal
  local v = nBot.ValidateHostileBotTarget(enemy, h, false, true, false)
  local skip = nBot.ValidateHostileBotTarget(enemy, h, false, true, true)
  p(label, 'target check by team', enemy, ': with the Locus rule', tostring(v), checkBool(label .. ' target check', v, want),
    '; skipLocusCheck', tostring(skip), checkBool(label .. ' target check, skipLocusCheck', skip, true))
end

local ok, err = pcall(function()
  S.step = S.step + 1
  local h = human()
  if none(h) then
    p('call', S.step, ': no human')
    return
  end
  local team = nGameObject.GetTeam(h)
  local mine, other = sideOf(team), otherSide(team)
  if S.step == 1 then
    local mm = false
    local objs = {}
    for i = 0, 2047 do
      local id = nObjectManager.GetObjectIdByIndex(i)
      if none(id) then break end
      local name = nGameObject.GetAssetName(id) or ''
      -- GetAssetName gives the noun's name without ".Noun" (SM_Map_v5's Locus is DEST_prefab_blue_tower_v5)
      if name == 'DEST_prefab_blue_tower' or name == 'DEST_prefab_blue_tower.Noun' then mm = true end
      if string.find(name, '^DEST_prefab_') or string.find(name, '^Binding_') or string.find(name, '^Guardian')
          or string.find(name, '^nub_') or string.find(name, '^gold_') then
        objs[#objs + 1] = { id = id, name = name }
      end
    end
    S.mm = mm
    p('call 1 at match', f1(nGameSimulator.GetMatchTime()), ': level', mm and 'MM_Map_v3' or 'not MM_Map_v3 (no checks of the structures)',
      ',', #objs, 'structures; the human', tostring(h), 'team', team)
    table.sort(objs, function(a, b) return a.name < b.name end)
    for _, o in ipairs(objs) do
      local t, set = tagsOf(o.id)
      local _, where = posOf(o.id)
      local side = sideOf(nGameObject.GetTeam(o.id))
      local res = ''
      if mm then
        if string.find(o.name, '^DEST_prefab_') then
          res = check(o.name, set, { side, 'Base', 'Locus' }, { 'Lane' })
        elseif string.find(o.name, '_Middle') then
          res = check(o.name, set, { side, 'Lane', 'Top', 'Bottom', 'BindingRange' }, { 'Base', 'Locus' })
        elseif string.find(o.name, '_Inner') then
          res = check(o.name, set, { side, 'BindingRange' }, { 'Locus' })
        elseif string.find(o.name, '^gold_') then
          res = check(o.name, set, { side, 'Base', 'SpiritWell', 'SpiritWellCenter' }, { 'Lane' })
        else
          res = check(o.name, set, { side, 'Base' }, { 'Lane', 'Locus' })
        end
      end
      p('  ', o.name, tostring(o.id), 'team', nGameObject.GetTeam(o.id), 'at', where, ':', t, res)
    end
    local v, where = posOf(h)
    S.home = { x = v.x, y = v.y }
    local t, set = tagsOf(h)
    p('the human at his start', where, ':', t, check('the human at his start', set, { mine, 'Base', 'Locus' }, { other, 'Lane' }))
    targetCheck(h, false, 'at his start:')
    local d = team == nTeamID.Mortal and -10 or 10
    if mm then
      teleport(h, d, d)
      p('the human teleported into the lane to (', d, ',', d, ')')
    end
  elseif S.step == 2 then
    local _, where = posOf(h)
    local t, set = tagsOf(h)
    if S.mm then
      p('call 2: the human in the lane at', where, ':', t,
        check('the human in the lane', set, { mine, 'Lane', 'Top', 'Bottom' }, { 'Base', 'Locus', other }))
      targetCheck(h, true, 'in the lane:')
      -- 5 units behind his team's Middle Binding (toward his base)
      local b
      for i = 0, 2047 do
        local id = nObjectManager.GetObjectIdByIndex(i)
        if none(id) then break end
        local name = nGameObject.GetAssetName(id) or ''
        if string.find(name, '^Binding_') and string.find(name, '_Middle') and nGameObject.GetTeam(id) == team then b = id end
      end
      if b then
        local bv = nGameObject.GetPosition(b)
        local s = team == nTeamID.Mortal and -1 or 1
        teleport(h, bv.x + s * 3.5, bv.y + s * 3.5)
        p('the human teleported behind', nGameObject.GetAssetName(b), 'at (', f1(bv.x), ',', f1(bv.y), ')')
      else
        p('no Middle Binding of team', team)
      end
    else
      p('call 2 (not MM_Map_v3): the human at', where, ':', t)
    end
  elseif S.step == 3 then
    local _, where = posOf(h)
    local t, set = tagsOf(h)
    if S.mm then
      p('call 3: the human behind his Middle Binding at', where, ':', t,
        check('the human by his Middle Binding', set, { mine, 'BindingRange', 'Lane' }, { 'Base', 'Locus' }))
      teleport(h, S.home.x, S.home.y)
      p('the human teleported back to his start (', f1(S.home.x), ',', f1(S.home.y), ')')
    else
      p('call 3 (not MM_Map_v3): the human at', where, ':', t)
    end
  else
    local _, where = posOf(h)
    local t, set = tagsOf(h)
    p('call', S.step, ': the human at', where, ':', t, check('the human back at his start', set, { mine, 'Base', 'Locus' }, { 'Lane' }))
    targetCheck(h, false, 'back at his start:')
    for _, f in ipairs(S.failures) do p('  failed:', f) end
    p('RESULT', S.fail == 0 and 'PASS' or 'FAIL', S.pass, 'of', S.pass + S.fail, 'checks')
  end
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
