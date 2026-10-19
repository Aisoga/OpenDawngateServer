-- BUFFICON probe: the Gladiator's stored-vim counter (modifier_Role_Gladiator_Counter, 108B48DA) in the client's buff
-- bar. The human is a melee Gladiator (role 0 without shaper select; [host] noun=0x1A0C443C, warden.Noun). Each call is
-- the next part; between two calls the counter stays as it is, so a peek of the client (game_peek.py: the creature's
-- "mods" detail and the "buff bar" line) or a focused screenshot in that window shows what the client holds:
--   part 1: the counter's state (expected none), then an enemy melee minion beside the human dies by KillObject without
--           a killer: the role's storage requests the counter (1 stack once the scheduler ran);
--   parts 2 and 3: the state (expected 1, then 2 stacks, the same instance), then another such death (+1 stack);
--   part 4: the state (expected 3), another death: stays at 3 (maxStackCount; the script requests nothing at 3);
--   part 5: the state (expected 3), then the human's lethal hit on a new minion: the storage pays (OnGrantGold,
--           + 3 x 3 x vim on top of the last hit's 6 x vim) and marks the counter for delete (AD to the client);
--   part 6: the state (expected none).
-- test_lua=<T1>:dgsim_bufficon.lua,<T2>:dgsim_bufficon.lua,... (six times; about 15 s apart leaves time for peeks).
-- Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end

local ok, err = pcall(function()
  local human, humanPid
  for _, pid in ipairs(nPlayer.GetPlayerIds() or {}) do
    local c = nPlayer.GetPlayerControlledObjectID(pid)
    if c ~= nil and c ~= kObjIDNone and not human and not nPlayer.IsBot(c) then human, humanPid = c, pid end
  end
  if not human then
    p('bufficon: no human player creature')
    return
  end
  dgsim_bufficon_part = (dgsim_bufficon_part or 0) + 1
  local part = dgsim_bufficon_part
  local counterGuid = nUtil.SPID('modifier_Role_Gladiator_Counter')
  local function gold() return nPlayer.GetPlayerAvailableGold(humanPid) end
  local function counter()
    local id = nModifier.GetFirstModifierByGUID(human, counterGuid)
    if id == nil or id == kObjIDNone then return 'none' end
    return string.format('%s stacks %s', tostring(id), tostring(nModifier.GetStackCount(id)))
  end
  local function spawnMinion()
    local tm = nGameObject.GetTeam(human) == nTeamID.Spirit and nTeamID.Mortal or nTeamID.Spirit
    local noun = tm == nTeamID.Mortal and 'Minion_Mortal_Melee.Noun' or 'Minion_Spirit_Melee.Noun'
    local at = nGameObject.GetPosition(human)
    return nObjectManager.CreateObject(nUtil.GetAsset(noun), tm, nVector.create(at.x + 5 + part, at.y + 4, 0), kZeroVector, 1)
  end
  local expected = ({'none', '1', '2', '3', '3', 'none'})[part] or '?'
  p(string.format('bufficon part %d: human %s %s pid %s role %s %s alive %s, gold %.2f; counter %s (expected %s)', part,
    tostring(human), nGameObject.GetAssetName(human), tostring(humanPid), tostring(nPlayer.GetPlayerRole(humanPid)),
    nGameObject.IsMeleeAttackStyle(human) and 'melee' or 'ranged', tostring(nGameObject.IsAlive(human)), gold(), counter(),
    expected))
  if part >= 1 and part <= 4 then
    local m = spawnMinion()
    local _, vim = nGameObject.GetRewardForNPC(m)
    local g0 = gold()
    nGameObject.KillObject(m, kObjIDNone, false)
    p(string.format('bufficon part %d: minion %s (vim %s) died beside the human by nobody: gold +%.2f; counter now %s '
      .. '(the request shows after the scheduler: expected %s at the next part)', part, tostring(m), tostring(vim),
      gold() - g0, counter(), part == 4 and '3' or tostring(part)))
  elseif part == 5 then
    local m = spawnMinion()
    local _, vim = nGameObject.GetRewardForNPC(m)
    local g0 = gold()
    local snap = nAttribute.CreateAttributeSnapshot_FromObject(human, kObjIDNone)
    local basic = nBit.Or(nBit.Or(nDescriptors.IsMelee, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
    local s, dealt = nGameObject.TakeDamage(snap, m, 5000, 0, basic, kZeroVector)
    nAttribute.RemoveAttributeSnapshot(snap)
    p(string.format('bufficon part 5: the human\'s last hit on %s -> %s dealt %s: gold +%.2f (expected %s = 6 x vim + the '
      .. 'storage 3 x 3 x vim); counter now %s (marked for delete: expected none at part 6)', tostring(m), tostring(s),
      tostring(dealt), gold() - g0, tostring(15 * (vim or 0)), counter()))
  end
end)
if not ok then p('bufficon: ERROR', err) end
return table.concat(out, '\n')
