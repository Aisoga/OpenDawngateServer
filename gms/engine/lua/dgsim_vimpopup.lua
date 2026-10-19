-- VIMPOPUP probe: the vim popups of a role's part. Since VIMPOPUP the host
-- grants a role's part as LabsPlayer_GrantGold's bonus ([host] role_gold_bonus 1): one gold CombatEvent with
-- integerBonusGoldChange, so the client's CombatEvent_Gold.lua shows two popups at the victim, the bonus
-- (combattext_gold_bonus) and the rest (combattext_gold); role_gold_bonus=0 is ROLEVIM-2's two grants (two
-- combattext_gold popups in the same frame; the user saw one). Popups show only for the local player, so the human
-- gets the role by [host] test_roles=0:<role> (0 Gladiator without it, 1 Tactician, 3 Predator).
-- test_lua=<T>:dgsim_vimpopup.lua,<T+1>:(the same),... Each call fires on the target the previous call prepared (so the
-- client has the object, and the teleported shaper at its new place, before the popup), then prepares the next. By the
-- human's role:
--   role 1, Tactician: an enemy melee minion 7 units from the human, killed by KillObject without a killer: expected
--     gold + 2 x vim (4 + 4: popups 4 and 4); 1 x with role_passives=0;
--   role 0, Gladiator: an enemy melee minion 7 units away, the human's lethal hit: expected + 6 x vim (12 + 12: popups
--     12 and 12), more with the melee storage (its 2 x vim per stored minion goes to the bonus popup);
--   role 3, Predator: a living enemy shaper teleported 7 units in front of the human and fired on in the same call
--     (since vimpopup_p1b; not rerun); odd shots the human's lethal hit
--     (expected: the kill's gold x 1.5, popups the bounty and its half; first blood + 100 as its own grant), even shots
--     an assist: the human's 1-damage hit, then a living allied player's lethal hit (expected: the assist share x 2,
--     popups share and share).
-- Returns a report line per call.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local human, humanPid
  local players = {}
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone then
      players[#players + 1] = {pid = pid, c = c}
      if not human and not nPlayer.IsBot(c) then human, humanPid = c, pid end
    end
  end
  if not human then
    p('vimpopup: no human player creature')
    return
  end
  dgsim_vimpopup_call = (dgsim_vimpopup_call or 0) + 1
  local call = dgsim_vimpopup_call
  local role = nPlayer.GetPlayerRole(humanPid)
  local function gold() return nPlayer.GetPlayerAvailableGold(humanPid) end
  local team = nGameObject.GetTeam(human)
  local enemy = team == nTeamID.Spirit and nTeamID.Mortal or nTeamID.Spirit
  local at = nGameObject.GetPosition(human)
  local spot = nVector.create(at.x + 7, at.y + 2, 0)
  local function hit(by, target, amount)
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(by, kObjIDNone)
    local basic = nBit.Or(nBit.Or(nDescriptors.IsMelee, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
    local _, dealt = nGameObject.TakeDamage(snap, target, amount, 0, basic, kZeroVector)
    nAttribute.RemoveAttributeSnapshot(snap)
    return dealt or -1
  end
  local head = string.format('vimpopup call %d: human %s pid %s role %s, gold %.2f', call, tostring(human),
    tostring(humanPid), tostring(role), gold())
  -- fire on the previous call's target; the Predator's victim is teleported and fired on in the same call (left beside
  -- the human for a call, it died to the base's tower in vimpopup_p1b, credited to the human as its last hitter)
  local target = dgsim_vimpopup_target
  dgsim_vimpopup_target = nil
  if role == 3 then
    target = nil
    for _, e in ipairs(players) do
      if nGameObject.GetTeam(e.c) ~= team and nGameObject.IsAlive(e.c) then
        nLocomotion.TeleportToPositionAndFacing(e.c, spot, nVector.create(-1, 0, 0))
        target = e.c
        break
      end
    end
  end
  if target and target ~= kObjIDNone and nGameObject.IsAlive(target) then
    dgsim_vimpopup_shots = (dgsim_vimpopup_shots or 0) + 1
    local shot = dgsim_vimpopup_shots
    local g0 = gold()
    if role == 1 then
      local _, vim = nGameObject.GetRewardForNPC(target)
      nGameObject.KillObject(target, kObjIDNone, false)
      p(string.format('%s; shot %d Tactician: minion %s (vim %.2f) died by nobody beside the human: gold +%.2f (expected %.2f = 4 + 4: popups %d and %d)',
        head, shot, tostring(target), vim, gold() - g0, 2 * vim, math.floor(vim), math.floor(vim)))
    elseif role == 0 then
      local _, vim = nGameObject.GetRewardForNPC(target)
      local dealt = hit(human, target, 50000)
      p(string.format('%s; shot %d Gladiator: minion %s (vim %.2f), the human\'s last hit (dealt %.2f): gold +%.2f (expected %.2f = 12 + 12, more with the storage)',
        head, shot, tostring(target), vim, dealt, gold() - g0, 6 * vim))
    elseif role == 3 then
      local ally
      for _, e in ipairs(players) do
        if not ally and e.c ~= human and nGameObject.GetTeam(e.c) == team and nGameObject.IsAlive(e.c) then ally = e.c end
      end
      if shot % 2 == 1 or not ally then
        local dealt = hit(human, target, 50000)
        p(string.format('%s; shot %d Predator kill: %s %s, the human\'s lethal hit (dealt %.2f): gold +%.2f (popups: bounty and its half; first blood +100 its own)',
          head, shot, tostring(target), nGameObject.GetAssetName(target), dealt, gold() - g0))
      else
        hit(human, target, 1)
        local g1 = gold()
        local dealt = hit(ally, target, 50000)
        p(string.format('%s; shot %d Predator assist: %s %s, the human\'s 1 hit (+%.2f), ally %s %s lethal (dealt %.2f): gold +%.2f (popups: share and share)',
          head, shot, tostring(target), nGameObject.GetAssetName(target), g1 - g0, tostring(ally),
          nGameObject.GetAssetName(ally), dealt, gold() - g1))
      end
    else
      p(head .. '; role not covered (test_roles=0:1|0:3, or none for the Gladiator)')
    end
  else
    p(head .. '; nothing to fire on (first call, or the target died)')
  end
  -- prepare the next call's target
  if role == 1 or role == 0 then
    local noun = enemy == nTeamID.Mortal and 'Minion_Mortal_Melee.Noun' or 'Minion_Spirit_Melee.Noun'
    dgsim_vimpopup_target = nObjectManager.CreateObject(nUtil.GetAsset(noun), enemy, spot, kZeroVector, 1)
  end
end)
if not ok then p('vimpopup: ERROR', err) end
return table.concat(out, '\n')
