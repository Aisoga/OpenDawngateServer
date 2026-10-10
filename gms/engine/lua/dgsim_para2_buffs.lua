-- PARA2 live probe: every kill buff of SM_Map_v5 on the human's team, one after another, with the server's numbers for
-- all ten players at each step. [host] test_lua=<s>:dgsim_para2_buffs.lua, nine calls
-- (e.g. 95, 105, 115, 125, 140, 170, 248, 258, 268), bot_brain=0;director_waves=0, no commands file (so nothing else
-- changes Power):
--   1: baseline; the human kills the Green (Fish) major        2: Blue (Fungus) major       3: Orange (Ugger) major
--   4: the human kills a stage-2 parasite (Minor buff, 120 s, to the human's team)
--   5: the human kills a stage-3 parasite (Major buff, 180 s, to the human's team; Minor still on)
--   6..9: reports (the buffs' remaining times; Green/Minor/Blue/Orange expire in this order)
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f1(v) if type(v) == 'number' then return string.format('%.1f', v) end return tostring(v) end

local NAMES = { 'Jungle_GreenBuff', 'Jungle_BlueBuff', 'Jungle_OrangeBuff', 'Parasite_MinorBuff', 'Parasite_MajorBuff' }
local function mods(o)
  local s = {}
  for _, n in ipairs(NAMES) do
    local ms = nModifier.GetModifiersMatchingGUID(o, nUtil.SPID('modifier_' .. n)) or {}
    local c, left = 0, nil
    for _, m in pairs(ms) do c = c + 1; left = nModifier.GetRemainingDuration(m) end
    if c > 0 then s[#s + 1] = string.format('%s x%d(%.0fs)', n, c, left or -1) end
  end
  return #s > 0 and table.concat(s, ',') or '-'
end
local function players()
  local r = {}
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone then
      r[#r + 1] = { pid = pid, c = c, team = nGameObject.GetTeam(c), bot = nPlayer.IsBot(c) and true or false }
    end
  end
  return r
end
local function report(tag)
  local human
  for _, q in ipairs(players()) do if not q.bot then human = q end end
  p(string.format('para2 [%s] match %.1f', tag, nGameSimulator.GetMatchTime()))
  for _, q in ipairs(players()) do
    local c = q.c
    p(string.format('  p%s team %s %s alive %s hp %.0f/%.0f Power %s Regen %s Haste %s Armor %s Gold %.0f dist-to-human %s mods %s',
      tostring(q.pid), tostring(q.team), q.bot and 'bot  ' or 'HUMAN', tostring(nGameObject.IsAlive(c)),
      nGameObject.GetHitPoints(c), nGameObject.GetMaxHitPoints(c),
      f1(nAttribute.GetAttributeValue(c, nAttributeType.Power)), f1(nAttribute.GetAttributeValue(c, nAttributeType.HealthRegen)),
      f1(nAttribute.GetAttributeValue(c, nAttributeType.Haste)),
      f1(nAttribute.GetAttributeValue(c, nAttributeType.PhysicalDamageReduction)),
      nPlayer.GetPlayerAvailableGold(q.pid), human and f1(nGameObject.GetObjectDistance(c, human.c)) or '?', mods(c)))
  end
  return human
end
local function findNear(prefix, x, y)
  local seen = {}
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    local key = tostring(id)
    if not seen[key] then
      seen[key] = true
      local name = nGameObject.GetAssetName(id) or ''
      local pos = nGameObject.GetPosition(id)
      if string.find(name, '^' .. prefix) and pos and math.abs(pos.x - x) < 15 and math.abs(pos.y - y) < 15 and
          nGameObject.IsAlive(id) then
        return id
      end
    end
  end
end
local function kill(att, o)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(att, kObjIDNone)
  local s, dealt = nGameObject.TakeDamage(snap, o, 1000000, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  return s, dealt
end

local ok, err = pcall(function()
  dgsim_para2_n = (dgsim_para2_n or 0) + 1
  local n = dgsim_para2_n
  local human = report('before part ' .. n)
  if n >= 1 and n <= 3 then
    local pref, x, y = 'Creep_Jungle_Fish_Major', 68.0, -165.6
    if n == 2 then pref, x, y = 'Creep_Jungle_Fungus_Major', -59.4, -57.4
    elseif n == 3 then pref, x, y = 'Creep_Jungle_Ugger_Major', -169.1, 61.5 end
    local t = findNear(pref, x, y)
    if not t then p('para2: no living', pref) else
      local s, d = kill(human.c, t)
      p('para2: the human kills', pref, tostring(s), d, 'alive', tostring(nGameObject.IsAlive(t)))
    end
  elseif n == 4 or n == 5 then
    local noun = n == 4 and 'Objective_SpiritMonster_State2.Noun' or 'Objective_SpiritMonster_State3.Noun'
    local o = nObjectManager.CreateObject(nUtil.GetAsset(noun), nTeamID.NPC, nVector.create(-8.0, 6.0, 0), kZeroVector, 1)
    local s, d = kill(human.c, o)
    p('para2: the human kills', noun, tostring(s), d, 'alive', tostring(nGameObject.IsAlive(o)))
  end
  if n <= 5 then report('after part ' .. n) end
end)
if not ok then p('para2: ERROR', err) end
return table.concat(out, '\n')
