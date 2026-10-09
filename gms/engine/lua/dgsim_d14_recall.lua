-- D14 live probe: what cancels a recall, and the server's auto-attack (item 15d "Recall and auto-attack"). It only
-- observes; the commands come from [host] commands_file (re\out\d14\commands_recall.txt, D14 recipe):
--   recall = a603..{caster}..ffffffffc645b896 (ability_returntobase by GUID), move = a601, stop = a608 (type 8),
--   basic attack = a603 slot 0 on {nearest:Minion_...} with keep-attacking.
-- Each call prints: the recall running (modifier_returntobase on the shaper, which lives exactly as long as the recall),
-- position, HP, the agent's best target and whether the shaper hit anything since the last call (HP of the probe's
-- minion). Calls 1 and the call after "spawn" spawn an enemy melee minion 4 units east of the shaper (for the
-- out-of-range attack and the auto-attack checks). Expected per step: see the D14 recipe in MATCH_FLOW.md / SERVER_MODE.md.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local ok, err = pcall(function()
  local shaper
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)  -- nPlayer.IsBot takes the creature, not the index
    if c ~= nil and c ~= kObjIDNone and not nPlayer.IsBot(c) then
      shaper = c
      break
    end
  end
  if not shaper then
    p('d14 recall: no human player creature')
    return
  end
  dgsim_d14_recall_call = (dgsim_d14_recall_call or 0) + 1
  local n = dgsim_d14_recall_call
  local v = nGameObject.GetPosition(shaper)
  local minion = dgsim_d14_recall_minion
  local mhp = (minion and nGameObject.IsAlive(minion)) and f2(nGameObject.GetHitPoints(minion)) or 'none'
  p(string.format('d14 recall call %d: match %.1f s, recall running %s, at (%.2f, %.2f), hp %s, AutoAttackDisabled %s, best target %s, '
    .. 'probe minion %s hp %s', n, nGameSimulator.GetMatchTime(),
    tostring(nModifier.AgentHasModifierMatchingGUID(shaper, nUtil.SPID('modifier_returntobase'))), v.x, v.y,
    f2(nGameObject.GetHitPoints(shaper)), f2(nAttribute.GetAttributeValue(shaper, nAttributeType.AutoAttackDisabled)),
    tostring(nAgent.GetBestTarget(shaper)), tostring(minion), mhp))
  if dgsim_d14_recall_spawn_next == nil then dgsim_d14_recall_spawn_next = true end
  if dgsim_d14_recall_spawn_next and (minion == nil or not nGameObject.IsAlive(minion)) then
    local team = nGameObject.GetTeam(shaper)
    local noun, enemy = 'Minion_Spirit_Melee.Noun', nTeamID.Spirit
    if team == nTeamID.Spirit then noun, enemy = 'Minion_Mortal_Melee.Noun', nTeamID.Mortal end
    local dist = dgsim_d14_recall_spawn_dist or 25
    local m = nObjectManager.CreateObject(nUtil.GetAsset(noun), enemy, nVector.create(v.x + dist, v.y, 0), kZeroVector, 1)
    dgsim_d14_recall_minion = m
    p('d14 recall: spawned enemy minion', tostring(m), 'at', string.format('%.0f', dist), 'units east (for {nearest:Minion_})')
  end
  dgsim_d14_recall_spawn_next = false
end)
if not ok then p('d14 recall: ERROR', err) end
return table.concat(out, '\n')
