-- D1h test probe (DAMAGE.md *D1h*, the death recap with two sources and three damage types): a Spirit melee minion
-- spawned 4 units from player 0's shaper hits it twice with a physical basic (40 each) and once with 60 magical damage,
-- then Binding_Spirit_Inner's snapshot kills it with a lethal true-damage hit, all through the host's damage core.
-- Expected (host log): the deaths counter + 1 before the killing blow's report ("kill bookkeeping (before the
-- report)"), the shaper's DeathRecap with 4 hits, 2 sources (the Binding first, the most recent), type lists 1/1/1, and
-- the client's mDeaths + 1 through the A9 delta. The minion is marked for delete. Returns a report.
local out = {}
local function p(...)
  local t = {}
  for i = 1, select('#', ...) do t[#t + 1] = tostring((select(i, ...))) end
  out[#out + 1] = table.concat(t, ' ')
end
local function f2(v) if type(v) == 'number' then return string.format('%.2f', v) end return tostring(v) end

local ok, err = pcall(function()
  local shaper = nPlayer.GetPlayerControlledObjectID(0)
  if shaper == nil or shaper == kObjIDNone or not nGameObject.IsAlive(shaper) then
    p('recap: no living shaper for player 0')
    return
  end
  local binding
  for i = 0, 1023 do
    local id = nObjectManager.GetObjectIdByIndex(i)
    if id == nil or id == kObjIDNone then break end
    if nGameObject.GetAssetName(id) == 'Binding_Spirit_Inner' then binding = id break end
  end
  if binding == nil then
    p('recap: no Binding_Spirit_Inner')
    return
  end
  local sp = nGameObject.GetPosition(shaper)
  local m = nObjectManager.CreateObject(nUtil.GetAsset('Minion_Spirit_Melee.Noun'), nTeamID.Spirit,
    nVector.create(sp.x + 4, sp.y, 0), kZeroVector, 1)
  local snapM = nAttribute.CreateAttributeSnapshot_FromObject(m, kObjIDNone)
  local snapB = nAttribute.CreateAttributeSnapshot_FromObject(binding, kObjIDNone)
  local basic = nBit.Or(nBit.Or(nDescriptors.IsMelee, nDescriptors.IsBasic), nDescriptors.IsPhysicalDamage)
  local hp0 = nGameObject.GetHitPoints(shaper)
  local _, d1 = nGameObject.TakeDamage(snapM, shaper, 40, 0, basic, kZeroVector)
  local _, d2 = nGameObject.TakeDamage(snapM, shaper, 40, 0, basic, kZeroVector)
  local _, d3 = nGameObject.TakeDamage(snapM, shaper, 60, 0, nDescriptors.IsMagicalDamage, kZeroVector)
  local hp1 = nGameObject.GetHitPoints(shaper)
  local s4, d4 = nGameObject.TakeDamage(snapB, shaper, 5000, 0, nDescriptors.IsTrueDamage, kZeroVector)
  nAttribute.RemoveAttributeSnapshot(snapM)
  nAttribute.RemoveAttributeSnapshot(snapB)
  nGameObject.MarkForDelete(m)
  p('recap: shaper', tostring(shaper), 'hp', f2(hp0), '-> minion', f2(d1), f2(d2), f2(d3), '->', f2(hp1),
    '-> Binding', tostring(binding), tostring(s4), f2(d4), '-> hp', f2(nGameObject.GetHitPoints(shaper)), 'alive',
    tostring(nGameObject.IsAlive(shaper)), 'at match time', nGameSimulator.GetMatchTime())
end)
if not ok then p('recap: ERROR', err) end
return table.concat(out, '\n')
