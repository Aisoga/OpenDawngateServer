-- D14 live probe: the parasite (docs/MATCH_FLOW.md *D14*, the parasite's rules). [host] test_lua=<s>:dgsim_d14_parasite.lua
-- at the seconds below; the human's moves come from the commands file (gms_commands.py encode move, {caster}).
-- Two modes, chosen by the match time of each call:
--   mode A (calls before match 290 s): a stage-3 test parasite beside the egg, the human 20 units from both.
--     A1 (20 s): the director's parasite (GetParasiteID) before its birth: expected Objective_SpiritMonster_State1, rank 1,
--        Untargetable 1, HideHealthBar 1, alive.
--     A2 (40 s; the human walked to (-18, -10) at 2 s): the egg's aggro list, expected EMPTY (the human never hit it) and
--        the human not damaged by it; then a test Objective_SpiritMonster_State3 at (-8, 6) (team NPC).
--     A3 (44 s): the stage 3 after its 1.33-s transform: Untargetable 0, HideHealthBar 0, HP = max, aggro list EMPTY
--        (the human 19 units away never hit it), HealthRegen with its +2000 out-of-combat modifier.
--     A4 (46 s): the human hits it (100 true): expected the human on its list at once, the +2000 regen gone, and attacks
--        (dgsim.log `agents:` gambit requests of ability_Parasite_*). The commands file moves the human away at 48 s.
--     A5 (60 s): the human ~47 units away: expected the list EMPTY again (beyond dropAggroRange 27.05 / tether 27.1), the
--        +2000 regen back, HP = max.
--     A6 (62 s): the human kills it (a lethal true hit from its snapshot): expected OnNearbyDeath -> YouKilledParasite3
--        to team 1 and EnemyKilledParasite3 to team 2, modifier_Parasite_EmpoweredMinions on the corpse,
--        OverrideMinionSpawnerAsset for the killer's two spawners (`match:` lines), 350 gold to every player of the
--        killer's team (`rewards:` type 4), modifier_Parasite_MajorBuff on every living creature of that team (the reward
--        data's team buff), ability_Parasite_Death (8 s of vision for both teams), no delete.
--     A7 (64 s): the corpse and the team's buffs; the waves from 90 s on carry 1 Strider_<Team>_Large on the killer's
--        spawners for 240 s (`director: wave` lines), reset at about match 302 s. End the session before 362 s (the test
--        parasite's own resurrect).
--   mode B (calls from match 290 s): the director's parasite after its birth at 300 s.
--     B1 (310 s; the human walked to (-18, -10) at 290 s): alive, rank 1, Untargetable 0, HideHealthBar 0, HP = max, aggro
--        list EMPTY, the human undamaged by it.
--     B2 (314 s): the human hits it (100 true): the human on its list, regen gone, attacks; the human moves away at 316 s.
--     B3 (326 s): list empty, regen back, HP = max.
--     B4 (330 s): the human kills it: YouKilledParasite1 / EnemyKilledParasite1, 150 gold to the killer's team, no buff,
--        no override (stage 1), ability_Parasite_Death.
--     B5 (335 s): dead, the death ability running, Untargetable 1, HideHealthBar 1, GetParasiteID unchanged.
--     B6 (635 s): resurrected by the death ability's Deactivate (300 s): the same object id, alive, HP = max, rank 1,
--        Untargetable 0, director GetParasiteID unchanged.
-- Returns a report (logged by the host after "host: test_lua dgsim_d14_parasite.lua ...").
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
  local mode = t < 290 and 'A' or 'B'
  if mode == 'A' then
    dgsim_d14_para_a = (dgsim_d14_para_a or 0) + 1
  else
    dgsim_d14_para_b = (dgsim_d14_para_b or 0) + 1
  end
  local part = mode == 'A' and dgsim_d14_para_a or dgsim_d14_para_b
  p(string.format('d14 parasite %s%d at match %s: director GetParasiteID %s, human %s (player %s)', mode, part, f2(t),
    tostring(real), tostring(h), tostring(hpid)))
  local team = h and nGameObject.GetTeam(h)
  if mode == 'A' then
    if part == 1 then
      report('A1 egg (expected rank 1, Untargetable 1, HideHealthBar 1)', real, h)
    elseif part == 2 then
      report('A2 egg (expected aggro list EMPTY, the human undamaged)', real, h)
      dgsim_d14_para3 = nObjectManager.CreateObject(nUtil.GetAsset('Objective_SpiritMonster_State3.Noun'), nTeamID.NPC,
        nVector.create(-8.0, 6.0, 0), kZeroVector, 1)
      p('d14 parasite A2: test stage 3 created:', tostring(dgsim_d14_para3), 'at', pos(dgsim_d14_para3))
    elseif part == 3 then
      report('A3 stage 3 (expected Untargetable 0, HideHealthBar 0, hp = max, list EMPTY, HealthRegen with +2000)',
        dgsim_d14_para3, h)
    elseif part == 4 then
      local o = dgsim_d14_para3
      local s, dealt, before = hit(h, o, 100)
      p(string.format('d14 parasite A4: the human hits the stage 3: %s, dealt %s, hp %s -> %s', tostring(s), f2(dealt),
        f2(before), f2(nGameObject.GetHitPoints(o))))
      report('A4 after the hit (expected the human on the list, HealthRegen without the +2000)', o, h)
    elseif part == 5 then
      report('A5 the human away (expected list EMPTY, HealthRegen with +2000 again, hp = max)', dgsim_d14_para3, h)
    elseif part == 6 then
      local o = dgsim_d14_para3
      local s, dealt, before = hit(h, o, nGameObject.GetHitPoints(o) + 1000)
      p(string.format('d14 parasite A6: the human kills the stage 3: %s, dealt %s, hp %s -> %s, alive %s', tostring(s),
        f2(dealt), f2(before), f2(nGameObject.GetHitPoints(o)), tostring(nGameObject.IsAlive(o))))
      p('d14 parasite A6: EmpoweredMinions on the corpse', tostring(has(o, 'modifier_Parasite_EmpoweredMinions')),
        '(expected true); team', tostring(team), 'buffs:', teamBuffs(team))
    else
      local o = dgsim_d14_para3
      report('A' .. part .. ' corpse (expected alive false, Untargetable 1, HideHealthBar 1)', o, h)
      p('d14 parasite A' .. part .. ': EmpoweredMinions', tostring(has(o, 'modifier_Parasite_EmpoweredMinions')),
        '; team', tostring(team), 'buffs (expected MajorBuff true on the living):', teamBuffs(team))
    end
  else
    if part == 1 then
      report('B1 born (expected alive, Untargetable 0, HideHealthBar 0, hp = max, list EMPTY, the human undamaged)', real, h)
    elseif part == 2 then
      local s, dealt, before = hit(h, real, 100)
      p(string.format('d14 parasite B2: the human hits it: %s, dealt %s, hp %s -> %s', tostring(s), f2(dealt), f2(before),
        f2(nGameObject.GetHitPoints(real))))
      report('B2 after the hit (expected the human on the list, regen without +2000)', real, h)
    elseif part == 3 then
      report('B3 the human away (expected list EMPTY, regen +2000 back, hp = max)', real, h)
    elseif part == 4 then
      dgsim_d14_para_real = real
      local s, dealt, before = hit(h, real, nGameObject.GetHitPoints(real) + 1000)
      p(string.format('d14 parasite B4: the human kills it: %s, dealt %s, hp %s -> %s, alive %s', tostring(s), f2(dealt),
        f2(before), f2(nGameObject.GetHitPoints(real)), tostring(nGameObject.IsAlive(real))))
      p('d14 parasite B4: team', tostring(team), 'buffs (expected none for stage 1):', teamBuffs(team))
    else
      local was = dgsim_d14_para_real or real
      report('B' .. part .. ' (B5: dead, Untargetable 1; B6 at kill + 305 s: the same id alive, hp = max, rank 1, '
        .. 'Untargetable 0)', was, h)
      p('d14 parasite B' .. part .. ': GetParasiteID', tostring(real), 'same object', tostring(tostring(real) ==
        tostring(was)))
    end
  end
end)
if not ok then p('d14 parasite: ERROR', err) end
return table.concat(out, '\n')
