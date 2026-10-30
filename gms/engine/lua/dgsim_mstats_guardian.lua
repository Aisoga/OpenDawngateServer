-- MSTATS1-live3 probe: the Guardian's damage in the match stats (gms/core/stats/stats.h). The enemy base falls
-- to the human's own hits, so the seat lines (seatStats) credit the human's seat. [host] test_lua with two calls, e.g.
--   60:dgsim_mstats_guardian.lua,64:dgsim_mstats_guardian.lua
--   call 1: every living Core (NPC type Nub) of the human's enemy team takes one lethal true-damage hit from the human's
--     snapshot (expected in the human's line: killsStructures 5, damageToStructures = the Cores' HP; 5 `structure`
--     events of type 14 in the matchTimeline); the host drops the Guardian's shield the next tick (D14, guardian_shield).
--   call 2: the enemy Guardian (expected unshielded: no modifier_Tower_Immunity, Immune 0; a shield still on is ended
--     here and reported) takes 1000 true damage from an allied bot's snapshot, 1000 from the human's, then a lethal hit
--     of its HP + 100 from the human's. Expected: the Guardian dead, the match ending through OnTitanTowerDestroyed,
--     the human's damageToGuardian = max HP - 1000, killsGuardian 1, overkill >= 100, the bot's damageToGuardian 1000
--     (the two sum to the Guardian's max HP when it was at full HP), and the timeline's `guardian` event with the
--     human's team as the winner.
-- Returns a report; every line starts with "mstats guardian".
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = 'mstats guardian ' .. table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

dgsim_mstats_guardian_call = (dgsim_mstats_guardian_call or 0) + 1
local call = dgsim_mstats_guardian_call

local function attr(o, name)
  local ok, v = pcall(nAttribute.GetAttributeValue, o, nAttributeType[name])
  if ok then return f2(v) end
  return '?'
end

local function hit(by, target, amount)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(by, kObjIDNone)
  local hp0 = nGameObject.GetHitPoints(target)
  local s, dealt = nGameObject.TakeDamage(snap, target, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  local hp1 = nGameObject.GetHitPoints(target)
  return string.format('%s %s: amount %.2f -> %s dealt %s, hp %s -> %s (effective %s), alive %s', tostring(target),
    tostring(nGameObject.GetAssetName(target)), amount, tostring(s), f2(dealt), f2(hp0), f2(hp1),
    f2((tonumber(hp0) or 0) - (tonumber(hp1) or 0)), tostring(nGameObject.IsAlive(target))), hp0, hp1
end

local ok, err = pcall(function()
  local human, humanPid, ally, allyPid
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not human and not nPlayer.IsBot(c) then human, humanPid = c, pid end
  end
  if not human then
    p('call', call, ': no human player creature')
    return
  end
  local team = nGameObject.GetTeam(human)
  local enemy = team == nTeamID.Spirit and nTeamID.Mortal or nTeamID.Spirit
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if not ally and c ~= nil and c ~= kObjIDNone and c ~= human and nPlayer.IsBot(c) and nGameObject.GetTeam(c) == team
      and nGameObject.IsAlive(c) then
      ally, allyPid = c, pid
    end
  end
  local g = nBattlegrounds.GetGuardianOnTeam(enemy)
  p('call', call, ': human', tostring(human), 'pid', tostring(humanPid), 'team', team, '; ally bot', tostring(ally), 'pid',
    tostring(allyPid), '; enemy team', enemy, 'Guardian', tostring(g), tostring(g and nGameObject.GetAssetName(g)),
    'hp', f2(g and nGameObject.GetHitPoints(g)), '/', f2(g and nGameObject.GetMaxHitPoints(g)), 'Immune', g and attr(g, 'Immune'),
    'at match time', f2(nGameSimulator.GetMatchTime()))
  if call == 1 then
    local n, total = 0, 0
    for i = 0, 2047 do
      local id = nObjectManager.GetObjectIdByIndex(i)
      if id == nil or id == kObjIDNone then break end
      if nGameObject.GetNPCType(id) == nNPCType.Nub and nGameObject.GetTeam(id) == enemy and nGameObject.IsAlive(id) then
        local line, hp0, hp1 = hit(human, id, (tonumber(nGameObject.GetHitPoints(id)) or 0) + 50)
        n = n + 1
        total = total + ((tonumber(hp0) or 0) - (tonumber(hp1) or 0))
        p('core', n, line)
      end
    end
    p('cores hit', n, 'effective damage', f2(total), '(expected in the human line: killsStructures', n,
      ', damageToStructures', f2(total), ')')
    return
  end
  if g == nil or g == kObjIDNone or not nGameObject.IsAlive(g) then
    p('no living enemy Guardian')
    return
  end
  local shield = nModifier.GetFirstModifierByGUID(g, nUtil.SPID('modifier_Tower_Immunity'))
  if shield ~= nil and shield ~= kObjIDNone then
    nModifier.MarkForDelete(g, shield)
    p('SHIELD STILL ON (modifier_Tower_Immunity', tostring(shield), '): ended for the test')
  else
    p('the shield is off (no modifier_Tower_Immunity)')
  end
  local max = tonumber(nGameObject.GetMaxHitPoints(g)) or 0
  local start = tonumber(nGameObject.GetHitPoints(g)) or 0
  local byAlly = 0
  if ally then
    local line, hp0, hp1 = hit(ally, g, 1000)
    byAlly = (tonumber(hp0) or 0) - (tonumber(hp1) or 0)
    p('ally hit', line)
  end
  local line, hp0, hp1 = hit(human, g, 1000)
  local byHuman = (tonumber(hp0) or 0) - (tonumber(hp1) or 0)
  p('human hit', line)
  line, hp0, hp1 = hit(human, g, (tonumber(nGameObject.GetHitPoints(g)) or 0) + 100)
  byHuman = byHuman + (tonumber(hp0) or 0) - (tonumber(hp1) or 0)
  p('human lethal hit', line)
  p(string.format('Guardian max HP %.2f, HP before %.2f, taken %.2f (expected damageToGuardian: human %.2f, ally bot %.2f; '
    .. 'killsGuardian 1 for the human), alive %s', max, start, byAlly + byHuman, byHuman, byAlly,
    tostring(nGameObject.IsAlive(g))))
end)
if not ok then p('ERROR', err) end
return table.concat(out, '\n')
