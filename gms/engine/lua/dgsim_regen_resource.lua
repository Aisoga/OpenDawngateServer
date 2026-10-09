-- D1i test probe ([host] test_lua=<s>:dgsim_regen_resource.lua, run four times; DAMAGE.md *D1i*): player 0's shaper's
-- resource regeneration, for a shaper whose resource isn't its health (e.g. [host] noun=0x8EA5D949, otter.Noun:
-- PC_Otter, energy 200, ResourcePointRegen 50 per 5 s). Run 1 empties the resource (nGameObject.SetResourcePoints 0),
-- run 2 adds DisableResourcePointRegen 1 (the regen must stop), run 3 removes it (the regen goes on), run 4 empties it
-- again (e.g. before a return to base: modifier_base_regen adds 2 % of the maximum every 0.1 s). Returns a report.
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
    p('regen resource: no shaper for player 0')
    return
  end
  dgsim_regen_ids = dgsim_regen_ids or {}
  dgsim_regen_ids.shaper = shaper
  dgsim_regen_resource_step = (dgsim_regen_resource_step or 0) + 1
  local step = dgsim_regen_resource_step
  local before = nGameObject.GetResourcePoints(shaper)
  local what
  if step == 1 or step == 4 then
    nGameObject.SetResourcePoints(shaper, 0)
    what = 'emptied'
  elseif step == 2 then
    dgsim_regen_disable_mod = nAttribute.AddAttributeModifier(shaper, nAttributeType.DisableResourcePointRegen, 1,
      nAttributeOp.kAdd)
    what = 'DisableResourcePointRegen added'
  elseif step == 3 and dgsim_regen_disable_mod ~= nil then
    nAttribute.RemoveAttributeModifier(shaper, dgsim_regen_disable_mod)
    dgsim_regen_disable_mod = nil
    what = 'DisableResourcePointRegen removed'
  else
    what = 'report only'
  end
  p('regen resource: step', step, what, tostring(shaper), nGameObject.GetAssetName(shaper), 'type',
    tostring(nGameObject.GetResourcePointType(shaper)), 'resource', f2(before), '->', f2(nGameObject.GetResourcePoints(shaper)),
    'of', f2(nGameObject.GetMaxResourcePoints(shaper)), 'ResourcePointRegen',
    f2(nAttribute.GetAttributeValue(shaper, nAttributeType.ResourcePointRegen)), 'ResourcePointPercentRegen',
    f2(nAttribute.GetAttributeValue(shaper, nAttributeType.ResourcePointPercentRegen)), 'DisableResourcePointRegen',
    f2(nAttribute.GetAttributeValue(shaper, nAttributeType.DisableResourcePointRegen)), 'hp',
    f2(nGameObject.GetHitPoints(shaper)), 'of', f2(nGameObject.GetMaxHitPoints(shaper)), 'at match time',
    nGameSimulator.GetMatchTime())
end)
if not ok then p('regen resource: ERROR', err) end
return table.concat(out, '\n')
