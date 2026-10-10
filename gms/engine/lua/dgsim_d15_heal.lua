-- D15 test probe ([host] test_lua=<s>:dgsim_d15_heal.lua; docs/ITEMS.md *The live batches*, batches B1/B2): the
-- healing reduction an enemy carries after player 0's hits (Corruption's and Pain's Mortal Strike,
-- modifier_StatusEffect_MortalStrike: HealingReduction 0.5 while it lasts). For every enemy within dgsim_d15_heal_radius
-- (25) of the shaper: HealingReduction (attribute 39), whether it has the Mortal Strike modifier, then a heal of
-- dgsim_d15_heal_amount (100) from its own snapshot (after true self-damage of 150 if it was at full health); the
-- client's heal core (GameObject_HealDamage 0x008E0720) applies the reduction, so healed = amount x (1 - reduction).
-- Returns a report.
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
    p('d15 heal: no shaper for player 0')
    return
  end
  -- D15-live: run it twice, 1 s apart. The first run (phase 'hit') hits every enemy once as player 0 and stops; the
  -- second (phase 'measure') heals them: the Mortal Strike modifier's Tick adds the reduction a frame after the request.
  -- dgsim_d15_heal_twice = false keeps the single-run behaviour.
  if dgsim_d15_heal_twice ~= false then
    if dgsim_d15_heal_phase ~= 'hit' then dgsim_d15_heal_phase = 'hit' else dgsim_d15_heal_phase = 'measure' end
  else
    dgsim_d15_heal_phase = 'measure'
  end
  local amount = dgsim_d15_heal_amount or 100
  local mortal = nUtil.SPID('modifier_StatusEffect_MortalStrike')
  local quiet = nDescriptors.IsTrueDamage
  if nDescriptors.DoesNotGenerateDamageCallbacks ~= nil then
    quiet = nBit.Or(quiet, nDescriptors.DoesNotGenerateDamageCallbacks)
  end
  local ids = nObjectManager.GetObjectsInRadius(nGameObject.GetPosition(shaper), dgsim_d15_heal_radius or 25,
    nGetObjectsSortType.Distance, nBit.Or(nTargetType.EnemyNPCs, nTargetType.EnemyPlayers), nGameObject.GetTeam(shaper),
    kObjIDNone, kObjIDNone, 0, 0)
  local n = 0
  for _, id in ipairs(ids or {}) do
    if nGameObject.IsAlive(id) then
      n = n + 1
      if dgsim_d15_heal_phase == 'hit' then
        -- D15-live: Mortal Strike lasts 3 s and the basic attacks kill the dummies, so hit each one once as player 0
        -- (a true-damage point from the shaper's snapshot: the items' OnDealtDamage runs) before measuring
        local ps = nAttribute.CreateAttributeSnapshot_FromObject(shaper, kObjIDNone)
        nGameObject.TakeDamage(ps, id, 1, 0, nDescriptors.IsTrueDamage, kZeroVector)
        nAttribute.RemoveAttributeSnapshot(ps)
        p('d15 heal: hit', tostring(id), nGameObject.GetAssetName(id), 'by player 0 (phase hit)')
      else
      local snap = nAttribute.CreateAttributeSnapshot_FromObject(id, kObjIDNone)
      if nGameObject.GetMaxHitPoints(id) - nGameObject.GetHitPoints(id) < amount + 1 then
        nGameObject.TakeDamage(snap, id, 150, 0, quiet, kZeroVector)
      end
      local reduction = nAttribute.GetAttributeValue(id, nAttributeType.HealingReduction)
      local mod = nModifier.GetFirstModifierByGUID(id, mortal)
      local hp0 = nGameObject.GetHitPoints(id)
      nGameObject.HealDamage(snap, id, amount, 0)
      local healed = nGameObject.GetHitPoints(id) - hp0
      nAttribute.RemoveAttributeSnapshot(snap)
      p('d15 heal:', tostring(id), nGameObject.GetAssetName(id), 'HealingReduction', f2(reduction), 'Mortal Strike',
        tostring(mod ~= nil and mod ~= kObjIDNone), 'heal', amount, '-> healed', f2(healed), '(expected',
        f2(amount * (1 - reduction)) .. ')')
      end
    end
  end
  p('d15 heal:', n, 'enemies checked at match time', f2(nGameSimulator.GetMatchTime()))
end)
if not ok then p('d15 heal: ERROR', err) end
return table.concat(out, '\n')
