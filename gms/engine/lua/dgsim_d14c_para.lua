-- D14c live probe: the parasite's stage-up that falls due while it is dead, and its stage-2 kill.
-- [host] test_lua with this file at 310, 700, 905, 1003, 1010, 1320, 1515 s (global dgsim_d14c_para counts the calls),
-- bots=0;grant_xp=2200, commands_file = re\out\d14\commands_para_b2.txt (the human walks to the parasite at 268 s):
--   1 (310) stage-1 parasite born at 300 s, the human beside it: expected list EMPTY (no aggro on sight), human unhurt;
--   2 (700) the human kills the stage 1 (150 gold, no buff): dead for 300 s (resurrect at 1000 s);
--   3 (905) the stage-up (RankUpTimer, rankTwoTime 900) fell due while it is dead: expected still dead, same id;
--   4 (1003) after the resurrect at ~1000 s: expected a NEW stage-2 object (GetParasiteID changed, nDirector.OnCreepUpgrade);
--   5 (1010) the human kills the stage 2: expected 250 gold, modifier_Parasite_MinorBuff on the team, no override;
--   6 (1320) the stage 2 resurrected at kill + 300 s, same id; 7 (1515) the stage-3 timer (1500): expected a new object.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end
local function pos(o)
  local v = o and nGameObject.GetPosition(o)
  if not v then return 'nil' end
  return string.format('(%.1f, %.1f)', v.x, v.y)
end
local function attr(o, name)
  local ok, v = pcall(nAttribute.GetAttributeValue, o, nAttributeType[name])
  if ok then return f2(v) end
  return '?'
end

local function human()
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    -- nPlayer.IsBot takes an object (0x00947660), not a player id
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then return c, pid end
  end
end

local function list(o)
  local ids = {}
  local ok, targets = pcall(nAgent.GetTargetsOnAggroList, o)
  if ok and type(targets) == 'table' then
    for _, t in ipairs(targets) do ids[#ids + 1] = tostring(t) end
  end
  return '[' .. table.concat(ids, ' ') .. ']', #ids
end

local function has(o, name)
  local ok, v = pcall(nModifier.AgentHasModifierMatchingGUID, o, nUtil.SPID(name))
  return ok and v
end

local function report(tag, o, h)
  if not o or o == kObjIDNone then
    p('d14 parasite', tag, ': no object')
    return
  end
  local l, n = list(o)
  p(string.format('d14 parasite %s: %s %s rank %s level %s alive %s hp %s/%s at %s; Untargetable %s HideHealthBar %s '
    .. 'HealthRegen %s; inCombat %s; aggro list %s (%d); human %s at %s dist %s hp %s',
    tag, tostring(o), tostring(nGameObject.GetAssetName(o)), tostring(nGameObject.GetNPCRank(o)),
    tostring(nGameObject.GetNPCLevel and nGameObject.GetNPCLevel(o)), tostring(nGameObject.IsAlive(o)),
    f2(nGameObject.GetHitPoints(o)), f2(nGameObject.GetMaxHitPoints(o)), pos(o), attr(o, 'Untargetable'),
    attr(o, 'HideHealthBar'), attr(o, 'HealthRegen'), tostring(nGameObject.IsInCombat(o)), l, n, tostring(h), pos(h),
    h and f2(nGameObject.GetObjectDistance(o, h)) or '-', h and f2(nGameObject.GetHitPoints(h)) or '-'))
end

local function hit(h, o, amount)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(h, kObjIDNone)
  local before = nGameObject.GetHitPoints(o)
  local s, dealt = nGameObject.TakeDamage(snap, o, amount, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  return s, dealt, before
end

local function teamBuffs(team)
  local s = {}
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and nGameObject.GetTeam(c) == team then
      s[#s + 1] = string.format('%s(%s alive %s Major %s Minor %s gold %s)', tostring(pid), tostring(c),
        tostring(nGameObject.IsAlive(c)), tostring(has(c, 'modifier_Parasite_MajorBuff')),
        tostring(has(c, 'modifier_Parasite_MinorBuff')), f2(nPlayer.GetPlayerAvailableGold(pid)))
    end
  end
  return table.concat(s, ' ')
end


local ok, err = pcall(function()
  local h, hpid = human()
  local t = nGameSimulator.GetMatchTime()
  local real = nDirector.GetParasiteID()
  dgsim_d14c_para = (dgsim_d14c_para or 0) + 1
  local part = dgsim_d14c_para
  p(string.format('d14c para %d at match %s: director GetParasiteID %s %s, human %s (player %s) gold %s', part, f2(t),
    tostring(real), tostring(real and nGameObject.GetAssetName(real)), tostring(h), tostring(hpid), f2(nPlayer.GetPlayerAvailableGold(hpid))))
  local team = h and nGameObject.GetTeam(h)
  if part == 1 then
    report('c1 born, human beside (expected list EMPTY)', real, h)
  elseif part == 2 then
    dgsim_d14c_stage1 = real
    local s, dealt = hit(h, real, nGameObject.GetHitPoints(real) + 1000)
    p('d14c para 2: stage-1 kill', tostring(s), 'dealt', f2(dealt), 'alive', tostring(nGameObject.IsAlive(real)), 'buffs', teamBuffs(team))
  elseif part == 3 then
    report('c3 dead stage 1 at 905 s (stage-up due)', dgsim_d14c_stage1, h)
  elseif part == 4 or part == 5 then
    report('c' .. part .. ' director parasite', real, h)
    p('d14c para', part, 'same id as the stage 1:', tostring(tostring(real) == tostring(dgsim_d14c_stage1)))
    if part == 5 and real and nGameObject.IsAlive(real) and nGameObject.GetNPCRank(real) == 2 then
      dgsim_d14c_stage2 = real
      local s, dealt = hit(h, real, nGameObject.GetHitPoints(real) + 1000)
      p('d14c para 5: stage-2 kill', tostring(s), 'dealt', f2(dealt), 'alive', tostring(nGameObject.IsAlive(real)), 'buffs', teamBuffs(team))
    end
  elseif part == 6 then
    p('d14c para 6: team buffs now', teamBuffs(team))
    report('c6 stage 2 after kill + 310 s', dgsim_d14c_stage2 or real, h)
  else
    report('c7 at 1515 s (stage 3 due at 1500)', real, h)
    p('d14c para 7: buffs', teamBuffs(team))
  end
end)
if not ok then p('d14c para: ERROR', err) end
return table.concat(out, '\n')
