-- D1f-live test probe (DAMAGE.md *Death handling* 5, the rewards): the player's shaper last-hits a Spirit melee minion
-- spawned 5 units from it. Through the host's damage core and death module: the minion's CharacterRewardData
-- (Minion_Melee: 58 XP "area", 4 gold "area and last hit") gives player 0 58 XP (alone within 32.9: share 1.0) and
-- 4 + 4 x playerMinionFatalHitGoldBonusRatio 3.0 = 16 gold (log "death: ... rewards"). The minion is killed by one
-- lethal true-damage hit with the shaper's snapshot (its player index credits the last hit). Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local ok, err = pcall(function()
  local shaper = nPlayer.GetPlayerControlledObjectID(0)
  if shaper == nil or shaper == kObjIDNone then
    p('last hit: no shaper for player 0')
    return
  end
  local sp = nGameObject.GetPosition(shaper)
  local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Spirit_Melee.Noun'), nTeamID.Spirit,
    nVector.create(sp.x + 4, sp.y + 3, 0), kZeroVector, 1)
  local snap = nAttribute.CreateAttributeSnapshot_FromObject(shaper, kObjIDNone)
  local gold = nPlayer.GetPlayerAvailableGold(0)
  local s, d = nGameObject.TakeDamage(snap, m, 1000, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snap)
  p('last hit: shaper', tostring(shaper), 'at', string.format('(%.1f %.1f)', sp.x, sp.y), 'killed', tostring(m), '->',
    tostring(s), f2(d), 'alive', tostring(nGameObject.IsAlive(m)), 'gold', f2(gold), '->', f2(nPlayer.GetPlayerAvailableGold(0)),
    '(expected +16)')
end)
if not ok then p('last hit: ERROR', err) end
return table.concat(out, '\n')
