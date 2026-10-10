-- PARA live probe: "the parasite passive". [host] test_lua=<s>:dgsim_para_passive.lua, 6 calls
-- (e.g. 20, 24, 30, 36, 40, 44), with practice bots on (bot_brain=0 keeps them where the probe puts them).
--   1: a stage-2 test parasite at (-8, 6) (team NPC)
--   2: the human kills it: expected YouKilledParasite2 / EnemyKilledParasite2, 250 gold to the human's team, modifier_Parasite_MinorBuff on
--      the team's living creatures, NO EmpoweredMinions, no spawner override
--   3: a stage-3 test parasite at (-8, 6); five Spirit bots teleported 10 units from it, modifier_dance on them (the passive's dance
--      check: five dancing enemy players within 25 of its spawn position while its aggro list is empty)
--   4: report: the dance should have been noticed (SetAnimationState Special; see the host log's animation lines)
--   5: a Spirit bot kills the stage 3: expected the Spirit spawner overrides, MajorBuff on the living Spirit creatures,
--      EmpoweredMinions on the corpse
--   6: report buffs, override
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function has(o, name)
  local ok, v = pcall(nModifier.AgentHasModifierMatchingGUID, o, nUtil.SPID(name))
  return ok and v
end
local function players()
  local r = {}
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone then
      r[#r + 1] = { pid = pid, c = c, team = nGameObject.GetTeam(c), bot = nPlayer.IsBot(c) }
    end
  end
  return r
end
local function buffs()
  local s = {}
  for _, q in ipairs(players()) do
    s[#s + 1] = string.format('p%s(team %s bot %s alive %s Minor %s Major %s dance %s gold %.0f power %.1f regen %.1f)', tostring(q.pid), tostring(q.team),
      tostring(q.bot), tostring(nGameObject.IsAlive(q.c)), tostring(has(q.c, 'modifier_Parasite_MinorBuff')),
      tostring(has(q.c, 'modifier_Parasite_MajorBuff')), tostring(has(q.c, 'modifier_dance')), nPlayer.GetPlayerAvailableGold(q.pid),
      nAttribute.GetAttributeValue(q.c, nAttributeType.Power), nAttribute.GetAttributeValue(q.c, nAttributeType.HealthRegen))
  end
  return table.concat(s, ' ')
end
local function hit(att, o)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(att, kObjIDNone)
  local s, dealt = nGameObject.TakeDamage(snap, o, nGameObject.GetHitPoints(o) + 1000, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  return s, dealt
end

local ok, err = pcall(function()
  dgsim_para_n = (dgsim_para_n or 0) + 1
  local n = dgsim_para_n
  local t = nGameSimulator.GetMatchTime()
  p(string.format('para %d at match %.1f', n, t))
  local human
  for _, q in ipairs(players()) do if not q.bot then human = q end end
  if n == 1 then
    dgsim_para2 = nObjectManager.CreateObject(nUtil.GetAsset('Objective_SpiritMonster_State2.Noun'), nTeamID.NPC,
      nVector.create(-8.0, 6.0, 0), kZeroVector, 1)
    p('para 1: stage 2 created', tostring(dgsim_para2), 'buffs:', buffs())
  elseif n == 2 then
    local s, dealt = hit(human.c, dgsim_para2)
    p('para 2: the human kills the stage 2:', tostring(s), 'dealt', tostring(dealt), 'alive', tostring(nGameObject.IsAlive(dgsim_para2)))
    p('para 2: EmpoweredMinions on the corpse', tostring(has(dgsim_para2, 'modifier_Parasite_EmpoweredMinions')), '(expected false)')
    p('para 2: buffs (expected MinorBuff on the human team\'s living):', buffs())
  elseif n == 3 then
    dgsim_para3 = nObjectManager.CreateObject(nUtil.GetAsset('Objective_SpiritMonster_State3.Noun'), nTeamID.NPC,
      nVector.create(-8.0, 6.0, 0), kZeroVector, 1)
    local k = 0
    for _, q in ipairs(players()) do
      if q.bot and q.team ~= human.team then
        k = k + 1
        nLocomotion.TeleportToPositionAndFacing(q.c, nVector.create(-8.0 + 4 * (k % 3), 16.0 + 2 * k, 0), nVector.create(1, 0, 0))
        local snap = nAttribute.CreateAttributeSnapshot_FromObject(q.c, kObjIDNone)
        local id = nModifier.RequestModifier(q.c, q.c, nUtil.SPID('modifier_dance'), snap, 1, kZeroVector, nil)
        nAttribute.RemoveAttributeSnapshot(snap)
        p('para 3: dance on bot', tostring(q.pid), '->', tostring(id), 'dist to parasite',
          string.format('%.1f', nGameObject.GetObjectDistance(q.c, dgsim_para3)))
      end
    end
    p('para 3: stage 3 created', tostring(dgsim_para3), 'dancers', k, 'buffs:', buffs())
  elseif n == 4 then
    p('para 4: dance check (expected the host log to show SetAnimationState Special for the parasite); buffs:', buffs())
  elseif n == 5 then
    local att
    for _, q in ipairs(players()) do if q.bot and q.team ~= human.team then att = q end end
    local s, dealt = hit(att.c, dgsim_para3)
    p('para 5: Spirit bot', tostring(att.pid), 'kills the stage 3:', tostring(s), 'dealt', tostring(dealt))
    p('para 5: EmpoweredMinions on the corpse', tostring(has(dgsim_para3, 'modifier_Parasite_EmpoweredMinions')), '(expected true)')
    p('para 5: buffs (expected MajorBuff on the Spirit team\'s living):', buffs())
  else
    p('para 6: buffs:', buffs(), '; EmpoweredMinions', tostring(has(dgsim_para3, 'modifier_Parasite_EmpoweredMinions')))
  end
end)
if not ok then p('para: ERROR', err) end
return table.concat(out, '\n')
